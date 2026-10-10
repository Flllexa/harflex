package externalagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/events"
)

type Recorder interface {
	Append(context.Context, string, string, string, any) (events.Event, error)
}

const terminalRecordTimeout = 2 * time.Second

var ErrSessionBusy = errors.New("external session is busy")
var ErrNotResumable = errors.New("session is not resumable; create a new session")
var ErrOutputLimitExceeded = errors.New("external assistant output limit exceeded")

type journalFailure struct{ cause error }

func (e *journalFailure) Error() string { return "external session journal unavailable" }
func (e *journalFailure) Unwrap() error { return e.cause }

type Session struct {
	ID        string
	Adapter   Adapter
	Recorder  Recorder
	mu        sync.Mutex
	cancel    context.CancelFunc
	cancelled bool
	sealed    bool
	done      chan struct{}
	request   Request
	getwd     func() (string, error)
	used      bool
	fault     error
}

func NewSession(id string, adapter Adapter, recorder Recorder, request Request) *Session {
	return &Session{ID: id, Adapter: adapter, Recorder: recorder, request: request}
}

// RestoreSession reconstructs idle CLI state without launching a process.
func RestoreSession(id string, adapter Adapter, recorder Recorder, request Request, used bool) *Session {
	s := NewSession(id, adapter, recorder, request)
	s.used = used
	return s
}

// SetFullAccess follows the project's permission profile from one run to the next: the person may change it between messages.
func (s *Session) SetFullAccess(on bool) {
	s.mu.Lock()
	s.request.FullAccess = on
	s.mu.Unlock()
}

func (s *Session) Cancel() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel == nil || s.cancelled {
		return false
	}
	s.cancelled = true
	s.cancel()
	return true
}

// Abort fences a workflow-owned CLI run, including the gap before Prompt.
func (s *Session) Abort(ctx context.Context) error {
	s.mu.Lock()
	if s.fault != nil {
		err := s.fault
		s.mu.Unlock()
		return err
	}
	previouslySealed := s.sealed
	if s.cancel != nil {
		s.sealed = true
		if !s.cancelled {
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
	if previouslySealed {
		s.mu.Unlock()
		return nil
	}
	if s.used {
		s.mu.Unlock()
		return ErrSessionBusy
	}
	s.sealed = true
	s.mu.Unlock()
	_, err := s.Recorder.Append(ctx, s.ID, "external_session", "external.run.cancelled", map[string]string{"adapter": s.Adapter.ID(), "reason": "cancelled"})
	return err
}
func (s *Session) Prompt(parent context.Context, text string) error {
	s.mu.Lock()
	if s.sealed {
		s.mu.Unlock()
		return context.Canceled
	}
	if s.fault != nil {
		err := s.fault
		s.mu.Unlock()
		return err
	}
	if s.cancel != nil {
		s.mu.Unlock()
		return ErrSessionBusy
	}
	if s.used && s.Adapter != nil && (!s.Adapter.Capabilities().Resumable || s.request.SessionID == "") {
		s.mu.Unlock()
		return ErrNotResumable
	}
	firstRun := !s.used
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.cancelled = false
	s.done = make(chan struct{})
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		s.cancel = nil
		s.cancelled = false
		close(s.done)
		s.done = nil
		s.mu.Unlock()
	}()
	if s.Adapter == nil || s.Recorder == nil || s.ID == "" {
		return errors.New("external session requires ID, adapter and recorder")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	request := s.request
	s.mu.Unlock()
	boundSessionID := request.SessionID
	request.Prompt = text
	// Codex and Claude Code take the instructions as such on every turn; only a CLI without that option gets them
	// ahead of its first message.
	if firstRun && request.SystemPrompt != "" && s.Adapter.ID() != "codex" && s.Adapter.ID() != "claude" {
		request.Prompt = request.SystemPrompt + "\n\n" + text
	}
	if request.CWD == "" {
		getwd := s.getwd
		if getwd == nil {
			getwd = os.Getwd
		}
		var err error
		request.CWD, err = getwd()
		if err != nil {
			return fmt.Errorf("resolve external session working directory: %w", err)
		}
	}
	if err := validateRequest(request, s.Adapter.ID(), s.Adapter.Capabilities().Resumable); err != nil {
		return fmt.Errorf("validate external session request: %w", err)
	}
	appendEvent := func(ctx context.Context, typ string, data any) error {
		_, err := s.Recorder.Append(ctx, s.ID, "external_session", typ, data)
		if err != nil {
			err = &journalFailure{cause: err}
			s.mu.Lock()
			s.fault = err
			s.mu.Unlock()
			return fmt.Errorf("record external session: %w", err)
		}
		return nil
	}
	if err := appendEvent(ctx, "external.run.started", map[string]string{"adapter": s.Adapter.ID()}); err != nil {
		return err
	}
	s.mu.Lock()
	s.used = true
	s.mu.Unlock()
	if err := appendEvent(ctx, "message.user", agentcore.Message{Role: agentcore.RoleUser, Content: text}); err != nil {
		terminalCtx, terminalCancel := context.WithTimeout(context.WithoutCancel(parent), terminalRecordTimeout)
		defer terminalCancel()
		terminalErr := appendEvent(terminalCtx, "external.run.failed", map[string]string{"adapter": s.Adapter.ID(), "reason": "journal_unavailable"})
		failure := &journalFailure{cause: errors.Join(err, terminalErr)}
		s.mu.Lock()
		s.fault = failure
		s.mu.Unlock()
		return failure
	}
	es, errs := s.Adapter.Run(ctx, request)
	var runErr, recordErr error
	assistantBytes := 0
	assistantSnapshots := make(map[string]int)
	outputLimitExceeded := false
	for es != nil || errs != nil {
		select {
		case e, ok := <-es:
			if !ok {
				es = nil
				continue
			}
			if recordErr == nil && ctx.Err() == nil {
				if request.MaxAssistantOutputBytes > 0 {
					messageID, assistantText, snapshot, hasAssistantText := assistantOutputSnapshot(e)
					if hasAssistantText {
						nextBytes := assistantBytes + len(assistantText)
						if snapshot {
							nextBytes = assistantBytes - assistantSnapshots[messageID] + len(assistantText)
						}
						if nextBytes > request.MaxAssistantOutputBytes {
							outputLimitExceeded = true
							runErr = errors.Join(runErr, ErrOutputLimitExceeded)
							cancel()
							continue
						}
						assistantBytes = nextBytes
						if snapshot {
							assistantSnapshots[messageID] = len(assistantText)
						}
					}
				}
				e.Raw = append([]byte(nil), e.Raw...)
				recordErr = appendEvent(ctx, "external.event", e)
				if recordErr == nil && ValidSessionID(e.SessionID) && s.Adapter.Capabilities().Resumable && e.SessionID != boundSessionID {
					recordErr = appendEvent(ctx, "external.session.bound", map[string]string{"sessionId": e.SessionID})
					if recordErr == nil {
						boundSessionID = e.SessionID
						s.mu.Lock()
						s.request.SessionID = e.SessionID
						s.mu.Unlock()
					}
				}
				if recordErr != nil {
					cancel()
				}
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			runErr = errors.Join(runErr, err)
		}
	}
	if recordErr != nil {
		return recordErr
	}
	runErr = errors.Join(runErr, ctx.Err())
	typ, reason := "external.run.completed", ""
	if runErr != nil {
		typ, reason = "external.run.failed", "execution_failed"
	}
	if outputLimitExceeded {
		reason = "output_limit_exceeded"
	}
	if ctx.Err() != nil && !outputLimitExceeded {
		typ, reason = "external.run.cancelled", "cancelled"
	}
	// The terminal record survives cancellation, but remains bounded by the recorder.
	terminalCtx, terminalCancel := context.WithTimeout(context.WithoutCancel(parent), terminalRecordTimeout)
	defer terminalCancel()
	if err := appendEvent(terminalCtx, typ, map[string]string{"adapter": s.Adapter.ID()}); err != nil {
		return errors.Join(runErr, err)
	}
	if runErr != nil {
		return agentcore.NewRunError(reason, runErr)
	}
	return nil
}

func assistantOutputSnapshot(event Event) (messageID, text string, snapshot, found bool) {
	if event.Type == "assistant.message" {
		if event.MessageID != "" && event.Mode == "replace" {
			return event.MessageID, event.Text, true, true
		}
		return "", event.Text, false, event.Text != ""
	}
	if event.Type != "external.raw" || !json.Valid(event.Raw) {
		return "", "", false, false
	}
	var record struct {
		Type string `json:"type"`
		Item struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"item"`
	}
	if json.Unmarshal(event.Raw, &record) != nil || (record.Type != "item.started" && record.Type != "item.updated" && record.Type != "item.completed") || record.Item.Type != "agent_message" || record.Item.Text == "" {
		return "", "", false, false
	}
	if record.Item.ID == "" {
		return "", record.Item.Text, false, true
	}
	return record.Item.ID, record.Item.Text, true, true
}
