package agentcore

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/security"
)

// Journal is the durable event boundary for one agent session.
type Journal interface {
	Append(context.Context, string, string, string, any) (events.Event, error)
	ListAfter(context.Context, string, int64) ([]events.Event, error)
}

var (
	ErrSessionBusy = errors.New("session is busy")
	// ErrApprovalRequired means the run is waiting for an explicit Approve call.
	ErrApprovalRequired = errors.New("approval is required")
	ErrApprovalNotFound = errors.New("approval not found or already resolved")
	errApprovalDenied   = errors.New("approval denied")
	errTurnLimit        = errors.New("provider turn limit reached")
)

const (
	maxBatchBytes   = 16 * 1024
	maxMessageBytes = 1024 * 1024
	maxToolCalls    = 64
	// A 10 MiB read becomes approximately 14 MiB after JSON/base64 encoding.
	maxToolContentBytes = 16 * 1024 * 1024
	maxToolDetailsBytes = 16 * 1024 * 1024
	persistTimeout      = 2 * time.Second
	// A maximum-sized result includes up to 32 MiB before redaction/encoding.
	// Keep that durability path bounded without applying the small-event deadline.
	toolResultPersistTimeout = 10 * time.Second
	// Safety bound for one in-process run, including approval continuations.
	// Configurable limits and persisted budgets are a future increment.
	maxTurns = 50
)

type pendingApproval struct {
	id   string
	call ToolCall
}

type journalError struct{ cause error }

func (e *journalError) Error() string { return "session journal unavailable" }
func (e *journalError) Unwrap() error { return e.cause }

// safeJoinedError keeps diagnostics available to errors.Is/As without exposing
// external provider, executor, or journal messages through the public Error text.
type safeJoinedError struct{ causes []error }

func (e *safeJoinedError) Error() string   { return "session operation failed" }
func (e *safeJoinedError) Unwrap() []error { return e.causes }

func joinSessionErrors(causes ...error) error {
	joined := &safeJoinedError{causes: make([]error, 0, len(causes))}
	for _, cause := range causes {
		if cause != nil {
			joined.causes = append(joined.causes, cause)
		}
	}
	if len(joined.causes) == 0 {
		return nil
	}
	return joined
}

// executionError classifies a failure before the terminal event is durable.
// Only finish may create the public RunError outcome after persistence succeeds.
type executionError struct {
	code  string
	cause error
}

func (e *executionError) Error() string { return "execution " + e.code }
func (e *executionError) Unwrap() error { return e.cause }

// Session serializes structural operations. Providers must close non-nil channels
// when their producer finishes or is cancelled; executors must join their update
// workers before returning. Nil error values are ignored.
// This object owns live state; constructing one does not replay a prior journal.
// A journal failure seals the object: further operations return that failure
// until the owner reconciles durable state and constructs a new session.
// Completed history remains in memory; total retention and replay require an
// owner-level policy. Individual messages, tool results, and updates are bounded.
type Session struct {
	id          string
	provider    Provider
	tools       ToolExecutor
	journal     Journal
	profile     security.Profile
	policyGuard func(context.Context, security.Risk, func(security.Decision) error) error

	mu        sync.Mutex
	active    bool
	finishing bool
	cancelled bool
	sealed    bool
	cancel    context.CancelFunc
	done      chan struct{}
	pending   *pendingApproval
	fault     error
	messages  []Message
	queue     []ToolCall
	// Unanswered calls belong to the current assistant occurrence, never an ID set.
	unanswered         []ToolCall
	turns              int
	maxOutputTokens    int
	maxOutputBytes     int
	maxTurns           int
	maxRunOutputTokens int64
	maxRunToolCalls    int
	runOutputTokens    int64
	runToolCalls       int
}

// SetAssistantOutputByteLimit configures an opt-in stream guard before the
// session is published. A zero limit remains the ordinary 1 MiB behavior.
// hasConversation reports a message beyond the system instructions; callers hold s.mu.
func (s *Session) hasConversation() bool {
	for _, message := range s.messages {
		if message.Role != RoleSystem {
			return true
		}
	}
	return false
}

func (s *Session) SetAssistantOutputByteLimit(limit int) error {
	if limit < 1 || limit > maxMessageBytes {
		return errors.New("invalid assistant output byte limit")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active {
		return ErrSessionBusy
	}
	if s.hasConversation() {
		return ErrSessionBusy
	}
	s.maxOutputBytes = limit
	return nil
}

// SetRunLimits configures cumulative limits for one Prompt before the session is
// published. The provider still receives maxOutputTokens per response; this
// budget accounts usage across turns and prevents another request at exhaustion.
func (s *Session) SetRunLimits(turnLimit int, totalOutputTokenLimit int64, totalToolCallLimit int) error {
	if turnLimit < 1 || turnLimit > maxTurns || totalOutputTokenLimit < 1 || totalOutputTokenLimit > int64(turnLimit*32768) ||
		totalToolCallLimit < 1 || totalToolCallLimit > maxToolCalls {
		return errors.New("invalid run limits")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active || s.hasConversation() {
		return ErrSessionBusy
	}
	s.maxTurns = turnLimit
	s.maxRunOutputTokens = totalOutputTokenLimit
	s.maxRunToolCalls = totalToolCallLimit
	return nil
}

// SetPolicyGuard installs the workspace's live authorization boundary before
// the session is published. The guard must serialize policy changes with the
// complete tool effect, not only with the decision snapshot.
func (s *Session) SetPolicyGuard(guard func(context.Context, security.Risk, func(security.Decision) error) error) {
	s.policyGuard = guard
}

func (s *Session) withPolicy(ctx context.Context, risk security.Risk, action func(security.Decision) error) error {
	if s.policyGuard != nil {
		return s.policyGuard(ctx, risk, action)
	}
	return action(security.Decide(security.Request{Profile: s.profile, Risk: risk, WithinRoot: true, SandboxReady: false}))
}

func NewSession(id string, provider Provider, executor ToolExecutor, journal Journal, profile security.Profile) *Session {
	return &Session{id: id, provider: provider, tools: executor, journal: journal, profile: profile}
}

func nilDependency(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func (s *Session) append(ctx context.Context, typ string, data any) error {
	_, err := s.journal.Append(ctx, s.id, "agent_session", typ, data)
	if err != nil {
		return &journalError{fmt.Errorf("append %s: %w", typ, err)}
	}
	return nil
}

// Once admitted, accepted events outlive execution cancellation. Each write
// retains context values and gets its own bounded durability deadline.
func (s *Session) persist(ctx context.Context, typ string, data any) error {
	timeout := persistTimeout
	if typ == "tool.completed" || typ == "tool.failed" {
		timeout = toolResultPersistTimeout
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	return s.append(persistCtx, typ, data)
}

// Prompt starts a new run or returns ErrSessionBusy while a run is active or
// awaiting approval. ErrApprovalRequired leaves that run open for Approve.
func (s *Session) Prompt(ctx context.Context, prompt string) error {
	if strings.TrimSpace(s.id) == "" || strings.TrimSpace(prompt) == "" {
		return errors.New("session ID and prompt are required")
	}
	if len(prompt) > maxMessageBytes || !utf8.ValidString(prompt) {
		return errors.New("invalid prompt size or encoding")
	}
	if nilDependency(s.provider) || nilDependency(s.tools) || nilDependency(s.journal) {
		return errors.New("session dependencies are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.sealed {
		s.mu.Unlock()
		return context.Canceled
	}
	if s.active || s.pending != nil {
		s.mu.Unlock()
		return ErrSessionBusy
	}
	if s.fault != nil {
		err := s.fault
		s.mu.Unlock()
		return err
	}
	opCtx := s.beginLocked(ctx)
	s.turns, s.runOutputTokens, s.runToolCalls = 0, 0, 0
	s.mu.Unlock()
	defer s.end()
	if err := s.append(opCtx, "run.started", struct{}{}); err != nil {
		return s.recordFault(err)
	}
	message := Message{Role: RoleUser, Content: prompt}
	if err := s.persist(opCtx, "message.user", message); err != nil {
		return s.finish(opCtx, err)
	}
	s.mu.Lock()
	s.messages = append(s.messages, message)
	s.mu.Unlock()
	return s.finish(opCtx, s.drive(opCtx))
}

// Approve consumes exactly one pending approval. Denial fails the run with
// reason approval_denied; an unknown or consumed ID returns ErrApprovalNotFound.
func (s *Session) Approve(ctx context.Context, approvalID string, allow bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.sealed {
		s.mu.Unlock()
		return ErrApprovalNotFound
	}
	if s.active {
		s.mu.Unlock()
		return ErrSessionBusy
	}
	if s.fault != nil {
		err := s.fault
		s.mu.Unlock()
		return err
	}
	if s.pending == nil || s.pending.id != approvalID {
		s.mu.Unlock()
		return ErrApprovalNotFound
	}
	pending := *s.pending
	s.pending = nil
	opCtx := s.beginLocked(ctx)
	s.mu.Unlock()
	defer s.end()
	typ := "approval.approved"
	if !allow {
		typ = "approval.denied"
	}
	if err := s.persist(opCtx, typ, map[string]string{"approvalId": approvalID, "toolCallId": pending.call.ID}); err != nil {
		return s.finish(opCtx, err)
	}
	if !allow {
		return s.finish(opCtx, errApprovalDenied)
	}
	if err := s.withPolicy(opCtx, security.Risk(s.tools.Risk(pending.call.Name)), func(decision security.Decision) error {
		if decision == security.Deny {
			if err := s.answerTool(opCtx, pending.call, "tool.denied", "policy_denied", "tool denied by security policy"); err != nil {
				return err
			}
			return &executionError{"policy_denied", errors.New("tool denied by security policy")}
		}
		return s.execute(opCtx, pending.call)
	}); err != nil {
		return s.finish(opCtx, err)
	}
	return s.finish(opCtx, s.drive(opCtx))
}

func (s *Session) beginLocked(ctx context.Context) context.Context {
	opCtx, cancel := context.WithCancel(ctx)
	s.active = true
	s.finishing = false
	s.cancelled = false
	s.cancel = cancel
	s.done = make(chan struct{})
	return opCtx
}

func (s *Session) end() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel()
	s.cancel = nil
	s.active = false
	s.finishing = false
	close(s.done)
	s.done = nil
}

// Abort fences a workflow-owned runner. It joins an in-flight operation,
// denies a pending approval, or records cancellation before the first prompt.
func (s *Session) Abort(ctx context.Context) error {
	s.mu.Lock()
	if s.fault != nil {
		err := s.fault
		s.mu.Unlock()
		return err
	}
	previouslySealed := s.sealed
	if s.active {
		s.sealed = true
		if !s.finishing && !s.cancelled {
			s.cancelled = true
			s.cancel()
		}
		done := s.done
		s.mu.Unlock()
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if s.pending == nil {
		if previouslySealed {
			s.mu.Unlock()
			return nil
		}
		if s.hasConversation() {
			s.mu.Unlock()
			return ErrSessionBusy
		}
		s.sealed = true
		s.mu.Unlock()
		if err := s.persist(ctx, "run.cancelled", map[string]string{"reason": "cancelled"}); err != nil {
			return s.recordFault(err)
		}
		return nil
	}
	pending := *s.pending
	s.sealed = true
	s.pending = nil
	opCtx := s.beginLocked(ctx)
	s.cancelled = true
	s.cancel()
	s.mu.Unlock()
	defer s.end()
	if err := s.persist(opCtx, "approval.denied", map[string]string{"approvalId": pending.id, "toolCallId": pending.call.ID}); err != nil {
		return s.recordFault(err)
	}
	err := s.finish(opCtx, context.Canceled)
	var ended *RunError
	if errors.As(err, &ended) && ended.Reason == "cancelled" {
		return nil
	}
	return err
}

// Cancel cancels active execution once. A run waiting for approval is idle;
// reject it with Approve(..., false) instead.
func (s *Session) Cancel() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active || s.finishing || s.cancelled {
		return false
	}
	s.cancelled = true
	s.cancel()
	return true
}

func (s *Session) recordFault(err error) error {
	s.mu.Lock()
	s.fault = err
	s.mu.Unlock()
	return err
}

func (s *Session) finish(ctx context.Context, cause error) error {
	s.mu.Lock()
	s.finishing = true
	ctxErr := ctx.Err()
	if cause == nil {
		// A cancellation that lands after the run completed does not undo it.
		ctxErr = nil
	}
	if errors.Is(cause, ErrApprovalRequired) && ctxErr == nil {
		s.mu.Unlock()
		return cause
	}
	s.pending = nil
	s.queue = nil
	var unanswered []ToolCall
	if cause != nil {
		unanswered = append([]ToolCall(nil), s.unanswered...)
	}
	s.mu.Unlock()
	var persistenceFailure *journalError
	if errors.As(cause, &persistenceFailure) {
		return s.recordFault(cause)
	}
	typ, reason, outcome := "run.completed", "", ""
	if ctxErr != nil {
		typ, outcome = "run.cancelled", "cancelled"
		if errors.Is(cause, ErrApprovalRequired) {
			cause = nil
		}
		cause = joinSessionErrors(cause, ctxErr)
	} else if cause != nil {
		typ, reason = "run.failed", "execution_failed"
		var classified *executionError
		if errors.As(cause, &classified) {
			reason = classified.code
		}
		if errors.Is(cause, errApprovalDenied) {
			reason = "approval_denied"
		}
		if errors.Is(cause, errTurnLimit) {
			reason = "turn_limit"
		}
		outcome = reason
	}
	// History stays valid for the next prompt: every tool call gets a result.
	for i, call := range unanswered {
		code := "skipped_after_failure"
		if outcome == "cancelled" || (outcome == "approval_denied" && i == 0) {
			code = outcome
		}
		if err := s.answerTool(ctx, call, "tool.skipped", code, "tool call not executed"); err != nil {
			return s.recordFault(joinSessionErrors(cause, err))
		}
	}
	if err := s.persist(ctx, typ, map[string]string{"reason": reason}); err != nil {
		return s.recordFault(joinSessionErrors(cause, err))
	}
	if cause == nil {
		return nil
	}
	return &RunError{Reason: outcome, cause: cause}
}

// answerTool records a safe response for in-process continuity after termination.
func (s *Session) answerTool(ctx context.Context, call ToolCall, typ, reason, text string) error {
	if err := s.persist(ctx, typ, map[string]string{"toolCallId": call.ID, "name": call.Name, "reason": reason, "errorCode": reason, "error": text}); err != nil {
		return err
	}
	s.recordToolResponse(call, syntheticToolResponse(reason))
	return nil
}

func syntheticToolResponse(code string) string {
	content, _ := json.Marshal(map[string]string{"error": code})
	return string(content)
}

func (s *Session) recordToolResponse(call ToolCall, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, Message{Role: RoleTool, Content: content, ToolCallID: call.ID})
	// Execution and closure both consume calls in the assistant's original order.
	s.unanswered = s.unanswered[1:]
}

func (s *Session) knownTool(name string) bool {
	for _, spec := range s.tools.Specs() {
		if spec.Name == name {
			return true
		}
	}
	return false
}

func (s *Session) drive(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.mu.Lock()
		var call *ToolCall
		if len(s.queue) > 0 {
			next := s.queue[0]
			s.queue = s.queue[1:]
			call = &next
		}
		s.mu.Unlock()
		if call != nil {
			s.mu.Lock()
			if s.maxRunToolCalls > 0 && s.runToolCalls >= s.maxRunToolCalls {
				s.mu.Unlock()
				return &executionError{"tool_limit_exceeded", errors.New("run tool call limit reached")}
			}
			s.runToolCalls++
			s.mu.Unlock()
			// Unknown names never reach the policy, so they cannot prompt the user.
			if !s.knownTool(call.Name) {
				if err := s.answerTool(ctx, *call, "tool.denied", "unknown_tool", "unknown tool"); err != nil {
					return err
				}
				return &executionError{"unknown_tool", errors.New("unknown tool")}
			}
			// The executor/PathGuard owns path confinement. Sandbox execution is
			// unavailable until a composition root can supply verified readiness.
			risk := s.tools.Risk(call.Name)
			err := s.withPolicy(ctx, security.Risk(risk), func(decision security.Decision) error {
				switch decision {
				case security.Deny:
					if err := s.answerTool(ctx, *call, "tool.denied", "policy_denied", "tool denied by security policy"); err != nil {
						return err
					}
					return &executionError{"policy_denied", errors.New("tool denied by security policy")}
				case security.AskUser:
					request := ApprovalRequest{ID: rand.Text(), ToolCallID: call.ID, Name: call.Name, Risk: risk, Arguments: append(json.RawMessage(nil), call.Arguments...)}
					if err := s.persist(ctx, "approval.requested", request); err != nil {
						return err
					}
					s.mu.Lock()
					s.pending = &pendingApproval{id: request.ID, call: *call}
					s.mu.Unlock()
					return &ApprovalRequiredError{Approval: request}
				case security.Allow:
					return s.execute(ctx, *call)
				}
				return nil
			})
			if err != nil {
				return err
			}
			continue
		}
		turnLimit := s.maxTurns
		if turnLimit == 0 {
			turnLimit = maxTurns
		}
		if s.turns++; s.turns > turnLimit {
			return errTurnLimit
		}
		calls, err := s.respond(ctx)
		if err != nil {
			return err
		}
		if len(calls) == 0 {
			return nil
		}
		s.mu.Lock()
		s.queue = calls
		s.mu.Unlock()
	}
}

func textChunk(text string) (string, string) {
	if len(text) <= maxBatchBytes {
		return text, ""
	}
	end := maxBatchBytes
	for !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end], text[end:]
}

func (s *Session) respond(ctx context.Context) (result []ToolCall, resultErr error) {
	var providerErr error
	defer func() {
		if providerErr != nil {
			resultErr = joinSessionErrors(resultErr, providerErr)
		}
	}()
	s.mu.Lock()
	messages := cloneMessages(s.messages)
	maxOutputTokens := s.maxOutputTokens
	maxOutputBytes := s.maxOutputBytes
	if s.maxRunOutputTokens > 0 {
		remaining := s.maxRunOutputTokens - s.runOutputTokens
		if remaining <= 0 {
			s.mu.Unlock()
			return nil, &executionError{"output_limit_exceeded", errors.New("run output token limit reached")}
		}
		if maxOutputTokens <= 0 || int64(maxOutputTokens) > remaining {
			maxOutputTokens = int(remaining)
		}
	}
	s.mu.Unlock()
	streamCtx, stopStream := context.WithCancel(ctx)
	defer stopStream()
	items, failures := s.provider.Stream(streamCtx, ChatRequest{Messages: messages, Tools: s.tools.Specs(), MaxOutputTokens: maxOutputTokens})
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var full, batch strings.Builder
	var calls []ToolCall
	flush := func(flushCtx context.Context) error {
		text := batch.String()
		for text != "" {
			chunk, rest := textChunk(text)
			if err := s.persist(flushCtx, "assistant.delta", map[string]string{"delta": chunk}); err != nil {
				return err
			}
			text = rest
		}
		batch.Reset()
		return nil
	}
	flushCancelled := func() error {
		return joinSessionErrors(ctx.Err(), flush(ctx))
	}
	for items != nil || failures != nil {
		if ctx.Err() != nil {
			return nil, flushCancelled()
		}
		select {
		case <-ctx.Done():
			return nil, flushCancelled()
		case <-ticker.C:
			if err := flush(ctx); err != nil {
				return nil, err
			}
		case err, ok := <-failures:
			if !ok {
				failures = nil
				continue
			}
			if err != nil && providerErr == nil {
				// Events and errors use independent channels. Retain the first
				// error while draining both channels so producers cannot block
				// on a subsequent error before closing the events channel.
				providerErr = err
			}
		case item, ok := <-items:
			if !ok {
				items = nil
				continue
			}
			switch item.Type {
			case "text_delta":
				if maxOutputBytes > 0 && len(item.Delta) > maxOutputBytes-full.Len() {
					stopStream()
					return nil, &executionError{code: "output_limit_exceeded"}
				}
				if len(item.Delta) > maxMessageBytes-full.Len() || !utf8.ValidString(item.Delta) {
					return nil, errors.New("invalid assistant text size or encoding")
				}
				full.WriteString(item.Delta)
				batch.WriteString(item.Delta)
				if batch.Len() >= maxBatchBytes {
					if err := flush(ctx); err != nil {
						return nil, err
					}
				}
			case "usage":
				if err := flush(ctx); err != nil {
					return nil, err
				}
				if item.Usage != nil {
					if s.maxRunOutputTokens > 0 && (item.Usage.InputTokens < 0 || item.Usage.OutputTokens < 0) {
						return nil, &executionError{"output_limit_exceeded", errors.New("invalid provider usage")}
					}
					if err := s.persist(ctx, "usage.recorded", *item.Usage); err != nil {
						return nil, err
					}
					if s.maxRunOutputTokens > 0 && maxOutputTokens > 0 && item.Usage.OutputTokens > int64(maxOutputTokens) {
						return nil, &executionError{"output_limit_exceeded", errors.New("response output exceeded its token limit")}
					}
					if s.maxRunOutputTokens > 0 {
						s.mu.Lock()
						previous := s.runOutputTokens
						s.runOutputTokens += item.Usage.OutputTokens
						overBudget := s.runOutputTokens < previous || s.runOutputTokens > s.maxRunOutputTokens
						s.mu.Unlock()
						if overBudget {
							return nil, &executionError{"output_limit_exceeded", errors.New("run output token limit exceeded")}
						}
					}
				}
			case "tool_call":
				if err := flush(ctx); err != nil {
					return nil, err
				}
				if item.ToolCall == nil || item.ToolCall.ID == "" || item.ToolCall.Name == "" || len(item.ToolCall.ID) > 256 || len(item.ToolCall.Name) > 256 || len(item.ToolCall.Arguments) > maxMessageBytes || !json.Valid(item.ToolCall.Arguments) || len(calls) >= maxToolCalls {
					return nil, errors.New("invalid tool call")
				}
				for _, call := range calls {
					if call.ID == item.ToolCall.ID {
						return nil, errors.New("duplicate tool call ID")
					}
				}
				call := *item.ToolCall
				call.Arguments = append(json.RawMessage(nil), call.Arguments...)
				calls = append(calls, call)
			}
		}
	}
	if ctx.Err() != nil {
		return nil, flushCancelled()
	}
	if err := flush(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if providerErr != nil && full.Len() == 0 && len(calls) == 0 {
		return nil, nil
	}
	message := Message{Role: RoleAssistant, Content: full.String(), ToolCalls: calls}
	if err := s.persist(ctx, "message.assistant", message); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.messages = append(s.messages, message)
	s.unanswered = append([]ToolCall(nil), calls...)
	s.mu.Unlock()
	return calls, nil
}

func cloneMessages(messages []Message) []Message {
	result := append([]Message(nil), messages...)
	for i := range result {
		result[i].Metadata = append(json.RawMessage(nil), result[i].Metadata...)
		result[i].ToolCalls = append([]ToolCall(nil), result[i].ToolCalls...)
		for j := range result[i].ToolCalls {
			result[i].ToolCalls[j].Arguments = append(json.RawMessage(nil), result[i].ToolCalls[j].Arguments...)
		}
	}
	return result
}

func (s *Session) execute(ctx context.Context, call ToolCall) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.persist(ctx, "tool.called", map[string]string{"toolCallId": call.ID, "name": call.Name}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	toolCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var updateMu sync.Mutex
	var updateErr error
	updateBytes := 0
	sink := func(effectiveCtx context.Context, update ToolUpdate) {
		updateMu.Lock()
		defer updateMu.Unlock()
		if updateErr != nil || effectiveCtx.Err() != nil || toolCtx.Err() != nil {
			return
		}
		if len(update.Text) > maxMessageBytes-updateBytes || len(update.Stream) > 256 || !utf8.ValidString(update.Text) {
			updateErr = errors.New("invalid tool update size or encoding")
			cancel()
			return
		}
		updateBytes += len(update.Text)
		text := update.Text
		for text != "" {
			chunk, rest := textChunk(text)
			if err := s.persist(effectiveCtx, "tool.updated", map[string]string{"toolCallId": call.ID, "stream": update.Stream, "text": chunk}); err != nil {
				updateErr = err
				cancel()
				return
			}
			text = rest
		}
	}
	result, err := s.tools.Execute(toolCtx, call.Name, append(json.RawMessage(nil), call.Arguments...), sink)
	toolErr := err
	updateMu.Lock()
	sinkErr := updateErr
	updateMu.Unlock()
	var persistenceFailure *journalError
	if errors.As(sinkErr, &persistenceFailure) {
		return &journalError{joinSessionErrors(sinkErr, err)}
	}
	// Validate and own partial evidence even when execution itself failed.
	noResult := len(result.Content) == 0 && len(result.Details) == 0
	resultErr := ownToolResult(&result)
	completed := struct {
		ToolCallID string `json:"toolCallId"`
		Name       string `json:"name"`
		ToolExecutionResult
	}{call.ID, call.Name, result}
	if resultErr == nil {
		if sizeErr := events.ValidateDataSize(completed); sizeErr != nil {
			resultErr = sizeErr
			result = ToolExecutionResult{}
		}
	}
	// A failure the model can act on (a missing file, a bad argument, a command's exit status) is the
	// tool's result, not the end of the run: it is journaled as a failed call and handed back so the
	// model can adjust. Anything else the tool, the sink or the result check reported still ends the run.
	// A failing tool usually returns no result at all, which is not an invalid one.
	if failure, ok := soleToolFailure(toolErr); ok && failure.valid() && sinkErr == nil && (resultErr == nil || noResult) && ctx.Err() == nil {
		recoverable := struct {
			ToolCallID  string `json:"toolCallId"`
			Name        string `json:"name"`
			ErrorCode   string `json:"errorCode"`
			Error       string `json:"error"`
			Recoverable bool   `json:"recoverable"`
			ToolExecutionResult
		}{call.ID, call.Name, failure.Code, failure.Message, true, result}
		if events.ValidateDataSize(recoverable) == nil {
			if answerErr := s.persist(ctx, "tool.failed", recoverable); answerErr != nil {
				return &journalError{answerErr}
			}
			s.recordToolResponse(call, ToolFailureResponse(failure.Code, failure.Message, result.Content))
			return nil
		}
	}
	executionFailed := err != nil || sinkErr != nil
	err = joinSessionErrors(sinkErr, err, resultErr)
	if err != nil {
		code, message := "tool_failed", "tool execution failed"
		if resultErr != nil && !executionFailed {
			code, message = "invalid_tool_result", "tool result failed validation"
		}
		if ctx.Err() != nil {
			code, message = "cancelled", "tool execution cancelled"
		}
		failed := struct {
			ToolCallID string `json:"toolCallId"`
			Name       string `json:"name"`
			ErrorCode  string `json:"errorCode"`
			Error      string `json:"error"`
			ToolExecutionResult
		}{call.ID, call.Name, code, message, result}
		if sizeErr := events.ValidateDataSize(failed); sizeErr != nil {
			failed.ToolExecutionResult = ToolExecutionResult{}
			err = joinSessionErrors(err, sizeErr)
		}
		if answerErr := s.persist(ctx, "tool.failed", failed); answerErr != nil {
			return &journalError{joinSessionErrors(answerErr, err)}
		}
		s.recordToolResponse(call, syntheticToolResponse(code))
		return &executionError{code, err}
	}
	// The effect already happened, so its result is recorded even when the run
	// was cancelled meanwhile; the drive loop observes cancellation afterwards.
	if err := s.persist(ctx, "tool.completed", completed); err != nil {
		return err
	}
	s.recordToolResponse(call, string(result.Content))
	return nil
}

func ownToolResult(result *ToolExecutionResult) error {
	var err error
	if len(result.Content) > maxToolContentBytes || !utf8.Valid(result.Content) || !json.Valid(result.Content) {
		result.Content = nil
		err = errors.New("invalid tool result")
	} else {
		result.Content = append(json.RawMessage(nil), result.Content...)
	}
	if len(result.Details) > maxToolDetailsBytes || !utf8.Valid(result.Details) || (len(result.Details) > 0 && !json.Valid(result.Details)) {
		result.Details = nil
		err = errors.New("invalid tool result")
	} else {
		result.Details = append(json.RawMessage(nil), result.Details...)
	}
	return err
}
