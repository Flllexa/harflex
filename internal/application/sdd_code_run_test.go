//go:build (linux && !android) || (darwin && !ios && cgo)

package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/sddworkspace"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

type codeRunProbeProvider struct {
	mu       sync.Mutex
	requests []agentcore.ChatRequest
	calls    int
	outside  string
}

func (*codeRunProbeProvider) ID() string { return "code-run-probe" }
func (*codeRunProbeProvider) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true, ToolCalls: true}
}
func (p *codeRunProbeProvider) Stream(_ context.Context, request agentcore.ChatRequest) (<-chan agentcore.StreamEvent, <-chan error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, request)
	p.calls++
	events := []agentcore.StreamEvent{{Type: "text_delta", Delta: "Code checked"}}
	if p.calls == 1 {
		events = []agentcore.StreamEvent{{Type: "tool_call", ToolCall: &agentcore.ToolCall{
			ID: "outside-write", Name: "write", Arguments: json.RawMessage(`{"path":"` + p.outside + `","content":"changed outside"}`),
		}}}
	}
	stream := make(chan agentcore.StreamEvent, len(events))
	for _, item := range events {
		stream <- item
	}
	close(stream)
	errors := make(chan error)
	close(errors)
	return stream, errors
}

func TestAuthoringCodeRunnerExposesOnlyPrivateFileTools(t *testing.T) {
	s, db, pipeline, sourceWorkspace, _, _ := authoringCodeCopyReadyFixture(t)
	if err := os.WriteFile(filepath.Join(sourceWorkspace.Path, "original.txt"), []byte("source baseline"), 0o600); err != nil {
		t.Fatal(err)
	}
	prepare := authoringCodeCopyRequest(t, s, pipeline, "code-run-copy-request-01")
	prepared, err := s.PrepareAuthoringCode(prepare)
	if err != nil || prepared.Status != "prepared" {
		t.Fatalf("prepare Code copy: status=%q error=%v", prepared.Status, err)
	}
	if err := os.WriteFile(filepath.Join(sourceWorkspace.Path, "original.txt"), []byte("source baseline"), 0o600); err != nil {
		t.Fatal(err)
	}

	preference, err := db.GetAuthoringStageModelPreference(t.Context(), pipeline.ID, sdd.Code)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := s.resolveAuthoringStageModelPreferenceWithCatalog(t.Context(), preference, false)
	if err != nil || resolved.DTO.Resolution != "ready" {
		t.Fatalf("resolve Code preference: resolution=%q error=%v", resolved.DTO.Resolution, err)
	}
	selection := resolved.Selection
	selection.SessionID = "code-run-session-probe"
	selection.WorkspacePath = prepared.PrivatePath
	workspace := sourceWorkspace
	workspace.Path = prepared.PrivatePath
	profile, err := db.GetProviderProfile(t.Context(), selection.BackendID)
	if err != nil {
		t.Fatal(err)
	}
	record := catalog.SessionRecord{
		ID: selection.SessionID, WorkspaceID: sourceWorkspace.ID, BackendID: selection.BackendID,
		BackendRevision: profileRevision(profile), Mode: catalog.AuthoringCodeSessionMode, Status: "ready",
	}
	if err := db.CreateSessionWithSnapshots(t.Context(), record, nil, "", nil, "agent_session", &selection); err != nil {
		t.Fatal(err)
	}
	provider := &codeRunProbeProvider{outside: filepath.Join(sourceWorkspace.Path, "original.txt")}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }

	runner, journal, err := s.makeRunner(&record, workspace, restoredHistory{}, false, nil, "", &selection)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	selected, ok := runner.(*selectedAPIRunner)
	if !ok {
		t.Fatalf("Code runner type = %T; want selected API runner", runner)
	}
	selected.validateAttempt = func(context.Context) error { return nil }
	if err := runner.Prompt(t.Context(), "Implement the approved Plan in the private Code copy."); err == nil {
		t.Fatal("the Code run should not report successful execution when its only write target is outside the private root")
	}

	provider.mu.Lock()
	if len(provider.requests) == 0 {
		provider.mu.Unlock()
		t.Fatal("provider did not receive the Code request")
	}
	gotTools := make([]string, 0, len(provider.requests[0].Tools))
	for _, spec := range provider.requests[0].Tools {
		gotTools = append(gotTools, spec.Name)
	}
	provider.mu.Unlock()
	if !reflect.DeepEqual(gotTools, []string{"edit", "read", "write"}) {
		t.Fatalf("Code tools = %v; want only edit, read, write", gotTools)
	}

	contents, err := os.ReadFile(filepath.Join(sourceWorkspace.Path, "original.txt"))
	if err != nil || string(contents) != "source baseline" {
		t.Fatalf("source changed outside the private copy: content=%q error=%v", contents, err)
	}
	if _, err := os.Stat(filepath.Join(prepared.PrivatePath, "original.txt")); err != nil {
		t.Fatalf("the private copy lost the prepared baseline: %v", err)
	}
	events, err := db.ListAfter(t.Context(), record.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var denied bool
	for _, event := range events {
		if event.Type == "tool.failed" {
			var payload struct {
				ErrorCode string `json:"errorCode"`
			}
			if json.Unmarshal(event.Data, &payload) == nil && payload.ErrorCode != "" {
				denied = true
			}
		}
	}
	if !denied {
		t.Fatalf("outside write was not durably recorded as denied: %v", eventsAsTypes(events))
	}
}

func TestAuthoringCodeTerminalStatusRecordsOutputAndTimeoutLimits(t *testing.T) {
	tests := []struct {
		name     string
		terminal string
		context  error
		result   RunResultDTO
		status   string
		code     string
	}{
		{name: "output limit", terminal: "run.failed", result: RunResultDTO{Status: RunFailed, Reason: "output_limit_exceeded"}, status: "failed", code: "output_limit_exceeded"},
		{name: "timeout", terminal: "run.failed", context: context.DeadlineExceeded, result: RunResultDTO{Status: RunFailed, Reason: "execution_failed"}, status: "failed", code: "timeout"},
		{name: "provider timeout journaled as cancellation", terminal: "run.cancelled", context: context.DeadlineExceeded, result: RunResultDTO{Status: RunCancelled}, status: "failed", code: "timeout"},
		{name: "cancelled", terminal: "run.cancelled", context: context.Canceled, result: RunResultDTO{Status: RunCancelled}, status: "cancelled", code: "cancelled"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, code := authoringCodeTerminalStatus(test.terminal, test.context, test.context, test.result)
			if status != test.status || code != test.code {
				t.Fatalf("terminal status = %s/%s; want %s/%s", status, code, test.status, test.code)
			}
		})
	}
}

func TestAuthoringCodeEnforcesAggregateOutputAndSixTurnLimits(t *testing.T) {
	s, _, input, _ := preparedCodeRunForTest(t, "code_round_limit")
	provider := &codeRunLimitProvider{
		toolsPerResponse:        []int{1, 1, 1, 1, 1, 1, 0},
		outputTokensPerResponse: []int64{4096, 4096, 4096, 4096, 4096, 4096, 1},
	}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	result, err := s.StartAuthoringCode(input)
	if err == nil || result.Status != "failed" || result.ErrorCode != "turn_limit" || provider.calls.Load() != 6 {
		t.Fatalf("seventh round reached provider or was not terminal: result=%+v error=%v providerCalls=%d", result, err, provider.calls.Load())
	}
	if result.Usage == nil || result.Usage.OutputTokens != 24576 || result.Limits.MaxTurns != 6 || result.Limits.MaxOutputTokens != 24576 {
		t.Fatalf("effective Code run output/turn limits = limits %+v usage %+v", result.Limits, result.Usage)
	}
}

func TestAuthoringCodeEnforcesCumulativeToolCallLimitAcrossResponses(t *testing.T) {
	s, db, input, _ := preparedCodeRunForTest(t, "code_tool_limit")
	provider := &codeRunLimitProvider{toolsPerResponse: []int{40, 25, 0}}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	result, err := s.StartAuthoringCode(input)
	if err == nil || result.Status != "failed" || result.ErrorCode != "tool_limit_exceeded" || provider.calls.Load() != 2 {
		t.Fatalf("cumulative 65th tool crossed the run budget: result=%+v error=%v providerCalls=%d", result, err, provider.calls.Load())
	}
	if provider.toolCalls.Load() != 65 {
		t.Fatalf("fake provider generated %d tools; want 65", provider.toolCalls.Load())
	}
	events, err := db.ListAfter(t.Context(), result.SessionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	for _, event := range events {
		if event.Type == "tool.called" {
			called++
		}
	}
	if called != 64 {
		t.Fatalf("tools executed=%d; 65th tool must not reach the executor", called)
	}
	if result.Limits.MaxToolCalls != 64 {
		t.Fatalf("persisted aggregate tool limit=%d; want 64", result.Limits.MaxToolCalls)
	}
}

func TestAuthoringCodeOutputBudgetStopsBeforeAnotherProviderCall(t *testing.T) {
	s, _, input, _ := preparedCodeRunForTest(t, "code_output_budget")
	provider := &codeRunLimitProvider{
		toolsPerResponse:        []int{1, 1, 1, 1, 1, 1, 0},
		outputTokensPerResponse: []int64{4096, 4096, 4096, 4096, 4096, 4096, 1},
	}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	result, err := s.StartAuthoringCode(input)
	if err == nil || result.Status != "failed" || result.ErrorCode != "turn_limit" || provider.calls.Load() != 6 {
		t.Fatalf("aggregate Code output budget was not enforced within six turns: result=%+v error=%v calls=%d", result, err, provider.calls.Load())
	}
	if result.Usage == nil || result.Usage.OutputTokens != 24576 || result.Limits.MaxOutputTokens != 24576 {
		t.Fatalf("aggregate output usage/limit = usage %+v limits %+v", result.Usage, result.Limits)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	for _, request := range provider.requests {
		if request.MaxOutputTokens != sdd.MaxAuthoringOutputTokens {
			t.Fatalf("per-response Code output cap=%d; want %d", request.MaxOutputTokens, sdd.MaxAuthoringOutputTokens)
		}
	}
}

func TestAuthoringCodeRejectsUsageAbovePerTurnMaximum(t *testing.T) {
	s, db, input, _ := preparedCodeRunForTest(t, "code_per_turn_usage")
	provider := &codeRunLimitProvider{
		toolsPerResponse:        []int{1, 0},
		outputTokensPerResponse: []int64{8192, 1},
	}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	result, err := s.StartAuthoringCode(input)
	if err == nil || result.Status != "failed" || result.ErrorCode != "output_limit_exceeded" || provider.calls.Load() != 1 {
		t.Fatalf("per-turn output overrun was not terminal before another response: result=%+v error=%v providerCalls=%d", result, err, provider.calls.Load())
	}
	if result.Usage == nil || result.Usage.OutputTokens != 8192 {
		t.Fatalf("overrun usage should remain visible for accounting: %+v", result.Usage)
	}
	events, err := db.ListAfter(t.Context(), result.SessionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "tool.called" {
			t.Fatal("tool call from over-budget response reached executor")
		}
	}
}

func TestAuthoringCodeRejectsUsageOverPerTurnMaximum(t *testing.T) {
	s, db, input, _ := preparedCodeRunForTest(t, "code_per_turn_usage")
	provider := &codeRunLimitProvider{
		toolsPerResponse:        []int{1, 0},
		outputTokensPerResponse: []int64{8192, 1},
	}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	result, err := s.StartAuthoringCode(input)
	if err == nil || result.Status != "failed" || result.ErrorCode != "output_limit_exceeded" || provider.calls.Load() != 1 {
		t.Fatalf("per-turn output overrun was not terminal before another response: result=%+v error=%v providerCalls=%d", result, err, provider.calls.Load())
	}
	if result.Usage == nil || result.Usage.OutputTokens != 8192 {
		t.Fatalf("overrun usage should remain visible for accounting: %+v", result.Usage)
	}
	events, err := db.ListAfter(t.Context(), result.SessionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "tool.called" {
			t.Fatal("tool call from over-budget response reached executor")
		}
	}
}

func TestAuthoringCodeReadbackDeadlinePersistsTerminalFailure(t *testing.T) {
	s, db, input, prepared := preparedCodeRunForTest(t, "code_readback_deadline")
	provider := &codeRunPrivateWriteProvider{}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	baseScan := s.authoringCodeScan
	var scanCalls atomic.Int32
	s.authoringCodeScan = func(ctx context.Context, path string) (sddworkspace.Manifest, error) {
		if scanCalls.Add(1) == 1 {
			return baseScan(ctx, path)
		}
		<-ctx.Done()
		return sddworkspace.Manifest{}, ctx.Err()
	}
	s.authoringCodeReadbackTimeout = 10 * time.Millisecond

	result, err := s.StartAuthoringCode(input)
	if err == nil || result.Status != "failed" || result.ErrorCode != "result_unavailable" || result.SourceReadbackStatus != "unavailable" {
		t.Fatalf("readback timeout was not exposed as terminal uncertainty: result=%+v error=%v", result, err)
	}
	if provider.calls.Load() != 2 {
		t.Fatalf("provider calls=%d; the completed inference should happen once", provider.calls.Load())
	}
	runs, err := db.ListAuthoringCodeRuns(t.Context(), input.PipelineID)
	if err != nil || len(runs) != 1 || runs[0].Status != "failed" || runs[0].ErrorCode != "result_unavailable" {
		t.Fatalf("expired readback left a nonterminal Code attempt: runs=%+v error=%v", runs, err)
	}
	if _, err := os.Stat(prepared.PrivatePath); err != nil {
		t.Fatalf("private copy should remain available after readback timeout: %v", err)
	}
}

func TestAuthoringCodeUsageReadbackRejectsTokenSumOverflow(t *testing.T) {
	s, db, _ := setup(t)
	const maxTokenCount = int64(1<<63 - 1)
	for _, tokens := range []agentcore.Usage{{InputTokens: maxTokenCount}, {InputTokens: 1}} {
		if _, err := db.Append(t.Context(), "code-usage-overflow-session", "agent_session", "usage.recorded", tokens); err != nil {
			t.Fatal(err)
		}
	}
	usage, _, err := s.readAuthoringCodeJournal(t.Context(), "code-usage-overflow-session")
	if err == nil || usage != nil {
		t.Fatalf("overflowed usage was returned as a valid aggregate: usage=%+v error=%v", usage, err)
	}
}

func TestAuthoringCodeUsageOverflowPersistsUnavailableTerminalState(t *testing.T) {
	s, db, input, _ := preparedCodeRunForTest(t, "code_usage_overflow_terminal")
	provider := &codeRunJournalOverflowProvider{store: db, pipelineID: input.PipelineID}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	result, err := s.StartAuthoringCode(input)
	if err == nil || result.Status != "failed" || result.ErrorCode != "usage_unavailable" || result.Usage != nil {
		t.Fatalf("overflowed journal usage was treated as valid: result=%+v error=%v", result, err)
	}
	runs, err := db.ListAuthoringCodeRuns(t.Context(), input.PipelineID)
	if err != nil || len(runs) != 1 || runs[0].Status != "failed" || runs[0].ErrorCode != "usage_unavailable" || runs[0].Usage != nil {
		t.Fatalf("usage overflow left unsafe terminal evidence: runs=%+v error=%v", runs, err)
	}
}

func TestStartAuthoringCodeWithoutPreparedReceiptHasNoSessionOrFilesystemEffect(t *testing.T) {
	s, db, pipeline, sourceWorkspace, _, catalogCalls := authoringCodeCopyReadyFixture(t)
	providerCalls := new(atomic.Int32)
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) {
		providerCalls.Add(1)
		return &codeRunProbeProvider{}, nil
	}
	before, err := sddworkspace.Scan(t.Context(), sourceWorkspace.Path)
	if err != nil {
		t.Fatal(err)
	}

	method := reflect.ValueOf(s).MethodByName("StartAuthoringCode")
	if !method.IsValid() {
		t.Fatal("application API StartAuthoringCode is missing")
	}
	input := reflect.New(method.Type().In(0)).Elem()
	setCodeRunString(t, input, "PipelineID", pipeline.ID)
	setCodeRunString(t, input, "PreparationID", "missing-preparation")
	setCodeRunString(t, input, "PreparationRequestID", "copy_request_missing_01")
	setCodeRunString(t, input, "RequestID", "code_request_unprepared_01")
	setCodeRunInt(t, input, "ExpectedPipelineRevision", pipeline.Revision)
	setCodeRunInt(t, input, "ExpectedPlanStageRevision", 1)
	setCodeRunInt(t, input, "ExpectedPlanArtifactVersion", 1)
	setCodeRunInt(t, input, "ExpectedCodePreferenceRevision", 0)
	setCodeRunString(t, input, "ExpectedCodeSelectionHash", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	setCodeRunString(t, input, "ExpectedManifestHash", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	results := method.Call([]reflect.Value{input})
	if results[1].IsNil() {
		t.Fatalf("StartAuthoringCode without a prepared receipt succeeded: %+v", results[0].Interface())
	}
	callErr, ok := results[1].Interface().(error)
	if !ok || !errors.Is(callErr, ErrInvalidInput) {
		t.Fatalf("StartAuthoringCode without a prepared receipt error = %v; want ErrInvalidInput", results[1].Interface())
	}
	if got, err := db.ListAllSessions(t.Context()); err != nil || len(got) != 0 {
		t.Fatalf("unprepared Code created sessions: sessions=%v error=%v", got, err)
	}
	if providerCalls.Load() != 0 || catalogCalls.Load() != 0 {
		t.Fatalf("unprepared Code made external work: providers=%d catalogHTTP=%d", providerCalls.Load(), catalogCalls.Load())
	}
	after, err := sddworkspace.Scan(t.Context(), sourceWorkspace.Path)
	if err != nil || after.Hash != before.Hash {
		t.Fatalf("unprepared Code changed source: before=%q after=%q error=%v", before.Hash, after.Hash, err)
	}
}

type codeRunPrivateWriteProvider struct {
	mu       sync.Mutex
	requests []agentcore.ChatRequest
	calls    atomic.Int32
}

type codeRunBlockingProvider struct {
	started  chan struct{}
	returned chan struct{}
}

type codeRunSourceDriftProvider struct {
	path  string
	calls atomic.Int32
}

type codeRunLimitProvider struct {
	mu                      sync.Mutex
	requests                []agentcore.ChatRequest
	calls                   atomic.Int32
	toolCalls               atomic.Int32
	toolsPerResponse        []int
	outputTokensPerResponse []int64
}

type codeRunJournalOverflowProvider struct {
	store      *sqlite.Store
	pipelineID string
	calls      atomic.Int32
}

func (*codeRunJournalOverflowProvider) ID() string { return "code-journal-overflow-probe" }
func (*codeRunJournalOverflowProvider) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true, ToolCalls: true}
}
func (p *codeRunJournalOverflowProvider) Stream(ctx context.Context, _ agentcore.ChatRequest) (<-chan agentcore.StreamEvent, <-chan error) {
	p.calls.Add(1)
	runs, err := p.store.ListAuthoringCodeRuns(ctx, p.pipelineID)
	if err == nil && len(runs) == 1 {
		_, err = p.store.Append(ctx, runs[0].SessionID, "agent_session", "usage.recorded", agentcore.Usage{InputTokens: int64(1<<63 - 1)})
		if err == nil {
			_, err = p.store.Append(ctx, runs[0].SessionID, "agent_session", "usage.recorded", agentcore.Usage{InputTokens: 1})
		}
	}
	stream := make(chan agentcore.StreamEvent, 1)
	if err == nil {
		stream <- agentcore.StreamEvent{Type: "text_delta", Delta: "Done"}
	}
	close(stream)
	errCh := make(chan error, 1)
	if err != nil {
		errCh <- err
	}
	close(errCh)
	return stream, errCh
}

type mutateCodeProfileAfterBeginStore struct {
	Store
	mutate func() error
}

type mutateCodeSourceAfterBeginStore struct {
	Store
	mutate func() error
}

type mutateCodeProfileAfterSnapshotStore struct {
	Store
	mutate func() error
	armed  atomic.Bool
	fired  atomic.Bool
}

func (store *mutateCodeProfileAfterSnapshotStore) BeginAuthoringCodeRun(ctx context.Context, attempt catalog.AuthoringCodeRun) (catalog.AuthoringCodeRun, bool, error) {
	run, created, err := store.Store.BeginAuthoringCodeRun(ctx, attempt)
	if err == nil && created {
		store.armed.Store(true)
	}
	return run, created, err
}

func (store *mutateCodeProfileAfterSnapshotStore) GetProviderProfile(ctx context.Context, id string) (catalog.ProviderProfile, error) {
	profile, err := store.Store.GetProviderProfile(ctx, id)
	if err == nil && store.armed.Load() && store.fired.CompareAndSwap(false, true) {
		if mutateErr := store.mutate(); mutateErr != nil {
			return profile, mutateErr
		}
	}
	return profile, err
}

func (store *mutateCodeProfileAfterBeginStore) BeginAuthoringCodeRun(ctx context.Context, attempt catalog.AuthoringCodeRun) (catalog.AuthoringCodeRun, bool, error) {
	run, created, err := store.Store.BeginAuthoringCodeRun(ctx, attempt)
	if err == nil && created {
		if mutateErr := store.mutate(); mutateErr != nil {
			return run, created, mutateErr
		}
	}
	return run, created, err
}

func (store *mutateCodeSourceAfterBeginStore) BeginAuthoringCodeRun(ctx context.Context, attempt catalog.AuthoringCodeRun) (catalog.AuthoringCodeRun, bool, error) {
	run, created, err := store.Store.BeginAuthoringCodeRun(ctx, attempt)
	if err == nil && created {
		if mutateErr := store.mutate(); mutateErr != nil {
			return run, created, mutateErr
		}
	}
	return run, created, err
}

func (*codeRunLimitProvider) ID() string { return "code-limit-probe" }
func (*codeRunLimitProvider) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true, ToolCalls: true}
}
func (p *codeRunLimitProvider) Stream(_ context.Context, request agentcore.ChatRequest) (<-chan agentcore.StreamEvent, <-chan error) {
	call := int(p.calls.Add(1))
	p.mu.Lock()
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	toolCount := 0
	if call <= len(p.toolsPerResponse) {
		toolCount = p.toolsPerResponse[call-1]
	}
	events := make([]agentcore.StreamEvent, 0, toolCount+1)
	if call <= len(p.outputTokensPerResponse) {
		events = append(events, agentcore.StreamEvent{Type: "usage", Usage: &agentcore.Usage{OutputTokens: p.outputTokensPerResponse[call-1]}})
	}
	for index := 0; index < toolCount; index++ {
		p.toolCalls.Add(1)
		events = append(events, agentcore.StreamEvent{Type: "tool_call", ToolCall: &agentcore.ToolCall{
			ID: fmt.Sprintf("round-%02d-tool-%03d", call, index+1), Name: "read", Arguments: json.RawMessage(`{"path":"source.txt"}`),
		}})
	}
	if toolCount == 0 {
		events = append(events, agentcore.StreamEvent{Type: "text_delta", Delta: "Done"})
	}
	stream := make(chan agentcore.StreamEvent, len(events))
	for _, item := range events {
		stream <- item
	}
	close(stream)
	errCh := make(chan error)
	close(errCh)
	return stream, errCh
}

func (*codeRunSourceDriftProvider) ID() string { return "source-drift-code-probe" }
func (*codeRunSourceDriftProvider) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true, ToolCalls: true}
}
func (p *codeRunSourceDriftProvider) Stream(_ context.Context, _ agentcore.ChatRequest) (<-chan agentcore.StreamEvent, <-chan error) {
	if p.calls.Add(1) == 1 {
		if err := os.WriteFile(p.path, []byte("external source drift"), 0o600); err != nil {
			panic(err)
		}
	}
	stream := make(chan agentcore.StreamEvent, 1)
	stream <- agentcore.StreamEvent{Type: "text_delta", Delta: "Done"}
	close(stream)
	errCh := make(chan error)
	close(errCh)
	return stream, errCh
}

func (*codeRunBlockingProvider) ID() string { return "blocking-code-probe" }
func (*codeRunBlockingProvider) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true, ToolCalls: true}
}
func (p *codeRunBlockingProvider) Stream(ctx context.Context, _ agentcore.ChatRequest) (<-chan agentcore.StreamEvent, <-chan error) {
	stream := make(chan agentcore.StreamEvent)
	errCh := make(chan error, 1)
	go func() {
		close(p.started)
		<-ctx.Done()
		errCh <- ctx.Err()
		close(stream)
		close(errCh)
		close(p.returned)
	}()
	return stream, errCh
}

func (*codeRunPrivateWriteProvider) ID() string { return "private-code-probe" }
func (*codeRunPrivateWriteProvider) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true, ToolCalls: true}
}
func (p *codeRunPrivateWriteProvider) Stream(_ context.Context, request agentcore.ChatRequest) (<-chan agentcore.StreamEvent, <-chan error) {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	call := p.calls.Add(1)
	events := []agentcore.StreamEvent{{Type: "usage", Usage: &agentcore.Usage{InputTokens: 10, OutputTokens: 2}}}
	// Every run takes two turns: a write, then the closing text. A run that starts on an even call is a second run.
	if call%2 == 1 {
		events = append(events, agentcore.StreamEvent{Type: "tool_call", ToolCall: &agentcore.ToolCall{
			ID: fmt.Sprintf("private-write-%d", call), Name: "write", Arguments: json.RawMessage(`{"path":"code-only.txt","content":"written in isolated Code"}`),
		}})
	} else {
		events = append(events, agentcore.StreamEvent{Type: "text_delta", Delta: "Implemented the approved Plan in the private copy."})
	}
	stream := make(chan agentcore.StreamEvent, len(events))
	for _, item := range events {
		stream <- item
	}
	close(stream)
	errCh := make(chan error)
	close(errCh)
	return stream, errCh
}

func TestStartAuthoringCodeRunsOnlyInPreparedCopyAndReplaysWithoutPrompt(t *testing.T) {
	s, db, pipeline := approvedPlanForCodeRun(t)
	// Resolve the workspace from the pipeline, then bind both the baseline and
	// private output checks to the current approved Plan revision.
	pipeline, err := db.GetPipeline(t.Context(), pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := db.GetWorkspace(t.Context(), pipeline.WorkspaceID)
	if err != nil {
		t.Fatalf("workspace = %+v error=%v", workspace, err)
	}
	originalPath := filepath.Join(workspace.Path, "source.txt")
	if err := os.WriteFile(originalPath, []byte("approved source baseline"), 0o600); err != nil {
		t.Fatal(err)
	}
	privateParent := t.TempDir()
	s.privateWorkspaceParent = privateParent
	plan, err := db.GetAuthoringStage(t.Context(), pipeline.ID, sdd.Plan)
	if err != nil || plan.State != "approved" {
		t.Fatalf("current Plan = %+v error=%v", plan, err)
	}
	preparationInput := authoringCodeCopyRequest(t, s, pipeline, "code_run_copy_confirmed_01")
	prepared, err := s.PrepareAuthoringCode(preparationInput)
	if err != nil || prepared.Status != "prepared" {
		t.Fatalf("private preparation = %+v error=%v", prepared, err)
	}
	if _, err := os.Stat(filepath.Join(prepared.PrivatePath, "source.txt")); err != nil {
		t.Fatalf("prepared root missing baseline file: %v", err)
	}

	previousClient := s.modelHTTPClient
	catalogCalls := new(atomic.Int32)
	s.modelHTTPClient = &http.Client{Transport: brainstormHTTPTransport(func(request *http.Request) (*http.Response, error) {
		catalogCalls.Add(1)
		return previousClient.Transport.RoundTrip(request)
	})}
	codePreference, err := db.GetAuthoringStageModelPreference(t.Context(), pipeline.ID, sdd.Code)
	if err != nil {
		t.Fatal(err)
	}
	freshSelection, err := s.resolveAuthoringStageModelPreferenceWithCatalog(t.Context(), codePreference, true)
	if err != nil {
		t.Fatalf("fresh catalog resolution: %+v error=%v", freshSelection.DTO, err)
	}
	if !authoringCodePreferenceMatches(authoringCodeCopyPreferenceSnapshot(prepared.CodePreference, prepared.CodeSelectionHash), freshSelection.DTO, freshSelection.Selection) {
		t.Fatalf("fresh catalog selection no longer matches preparation: prepared=%+v current=%+v selection=%+v", prepared.CodePreference, freshSelection.DTO, freshSelection.Selection)
	}
	profile, err := db.GetProviderProfile(t.Context(), prepared.CodePreference.Selection.BackendID)
	profileBase, validProfileBase := parseProfileURL(profile.BaseURL)
	if err != nil || profile.Kind != "openai_compatible" || profileRevision(profile) != prepared.CodePreference.Selection.CatalogRevision ||
		!validProfileBase || canonicalProfileOrigin(profileBase) != prepared.CodePreference.Selection.Destination {
		t.Fatalf("profile no longer matches prepared selection: profile=%+v selection=%+v error=%v", profile, prepared.CodePreference.Selection, err)
	}
	provider := &codeRunPrivateWriteProvider{}
	providerFactoryCalls := authoringProviderFactoryCounter(s, &brainstormTestProvider{events: nil})
	providerFactorySawUnlockedProfile := false
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) {
		providerFactoryCalls.Add(1)
		if s.profileGate.TryLock() {
			providerFactorySawUnlockedProfile = true
			s.profileGate.Unlock()
		}
		return provider, nil
	}
	codeStart := StartAuthoringCodeInput{
		PipelineID: pipeline.ID, PreparationID: prepared.ID, PreparationRequestID: prepared.RequestID,
		RequestID: "code_run_start_request_01", ExpectedPipelineRevision: pipeline.Revision,
		ExpectedPlanStageRevision: plan.Revision, ExpectedPlanArtifactVersion: plan.ArtifactVersion,
		ExpectedCodePreferenceRevision: prepared.CodePreferenceRevision,
		ExpectedCodeSelectionHash:      prepared.CodeSelectionHash, ExpectedManifestHash: prepared.ManifestHash,
	}
	staleCatalogCalls := catalogCalls.Load()
	stalePlan := codeStart
	stalePlan.ExpectedPlanStageRevision--
	if _, err := s.StartAuthoringCode(stalePlan); !errors.Is(err, sqlite.ErrPipelineConflict) {
		t.Fatalf("stale Plan revision reached Code admission: %v", err)
	}
	if runs, err := db.ListAuthoringCodeRuns(t.Context(), pipeline.ID); err != nil || len(runs) != 0 || providerFactoryCalls.Load() != 0 || catalogCalls.Load() != staleCatalogCalls {
		t.Fatalf("stale Plan created Code effects: runs=%+v providerFactory=%d catalog=%d error=%v", runs, providerFactoryCalls.Load(), catalogCalls.Load(), err)
	}
	callsBeforeStart := catalogCalls.Load()
	started, err := s.StartAuthoringCode(codeStart)
	if err != nil || started.Status != "completed" || started.SessionID == "" {
		runs, readErr := db.ListAuthoringCodeRuns(t.Context(), pipeline.ID)
		sessions, sessionErr := db.ListAllSessions(t.Context())
		t.Fatalf("StartAuthoringCode = %+v error=%v runs=%+v readErr=%v sessions=%+v sessionErr=%v", started, err, runs, readErr, sessions, sessionErr)
	}
	if catalogCalls.Load() <= callsBeforeStart {
		t.Fatal("Code started without validating the current model catalog")
	}
	if started.SourceManifest == nil || started.SourceManifest.Hash != prepared.ManifestHash || started.SourceDrift || started.SourceReadbackStatus != "matched" {
		t.Fatalf("successful Code run omitted the final source readback: %+v", started)
	}
	if provider.calls.Load() != 2 || providerFactoryCalls.Load() != 1 {
		t.Fatalf("provider runs=%d factories=%d; want one two-response run", provider.calls.Load(), providerFactoryCalls.Load())
	}
	if providerFactorySawUnlockedProfile {
		t.Fatal("Code provider was composed outside the profile revision fence")
	}
	provider.mu.Lock()
	for _, request := range provider.requests {
		if request.Model != prepared.CodePreference.Selection.ModelID || request.ReasoningEffort != prepared.CodePreference.Selection.ReasoningEffort ||
			request.MaxOutputTokens != sdd.MaxAuthoringOutputTokens {
			provider.mu.Unlock()
			t.Fatalf("Code provider selection drifted from prepared snapshot: request=%+v prepared=%+v", request, prepared.CodePreference.Selection)
		}
		got := make([]string, 0, len(request.Tools))
		for _, tool := range request.Tools {
			got = append(got, tool.Name)
		}
		if !reflect.DeepEqual(got, []string{"edit", "read", "write"}) {
			provider.mu.Unlock()
			t.Fatalf("Code provider tools = %v; want only edit, read, write", got)
		}
	}
	provider.mu.Unlock()
	if got, err := os.ReadFile(originalPath); err != nil || string(got) != "approved source baseline" {
		t.Fatalf("Code changed the original project: %q error=%v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(prepared.PrivatePath, "code-only.txt")); err != nil || string(got) != "written in isolated Code" {
		t.Fatalf("Code did not write inside its private root: %q error=%v", got, err)
	}
	if started.Usage == nil || started.Usage.InputTokens != 20 || started.Usage.OutputTokens != 4 || started.Usage.CostUSD != nil {
		t.Fatalf("Code usage must preserve tokens and unknown cost: %+v", started.Usage)
	}
	if len(started.Changes) != 1 || started.Changes[0].Path != "code-only.txt" || started.Changes[0].Kind != sddworkspace.ChangeAdded || started.Result == nil {
		t.Fatalf("Code result did not read back the private manifest delta: %+v", started)
	}
	if len(started.PatchHash) != 64 {
		t.Fatalf("completed Code run has no patch hash: %+v", started)
	}
	patchContent := ""
	var patchCursor int64
	for {
		page, pageErr := s.GetAuthoringCodeRunPatch(GetAuthoringCodeRunPatchInput{PipelineID: pipeline.ID, RunID: started.ID, Cursor: patchCursor})
		if pageErr != nil || page.Cursor != patchCursor || page.PipelineID != pipeline.ID || page.RunID != started.ID || page.PatchHash != started.PatchHash ||
			page.BaselineHash != started.ManifestHash || page.SourceManifestHash != prepared.ManifestHash || page.ResultManifestHash != started.Result.Hash || page.SourceReadbackStatus != "matched" {
			t.Fatalf("patch page binding mismatch: page=%+v error=%v", page, pageErr)
		}
		if len([]byte(page.Content)) > authoringCodePatchPageBytes {
			t.Fatalf("patch page exceeded %d bytes: %d", authoringCodePatchPageBytes, len([]byte(page.Content)))
		}
		patchContent += page.Content
		if page.NextCursor == nil {
			if int64(len([]byte(patchContent))) != page.TotalBytes {
				t.Fatalf("patch pages ended at %d bytes, total=%d", len([]byte(patchContent)), page.TotalBytes)
			}
			break
		}
		if *page.NextCursor <= patchCursor {
			t.Fatalf("patch cursor did not advance: current=%d next=%d", patchCursor, *page.NextCursor)
		}
		patchCursor = *page.NextCursor
	}
	patchDigest := sha256.Sum256([]byte(patchContent))
	if hex.EncodeToString(patchDigest[:]) != started.PatchHash || !json.Valid([]byte(patchContent)) {
		t.Fatalf("paged patch was not exact/canonical: hash=%x patchHash=%s", patchDigest, started.PatchHash)
	}
	if started.Limits.MaxTurns != authoringCodeMaxTurns || started.Limits.MaxToolCalls != authoringCodeMaxToolCalls ||
		started.Limits.MaxOutputTokens != authoringCodeMaxOutputTokens || started.Limits.MaxOutputTokensPerTurn != sdd.MaxAuthoringOutputTokens ||
		started.Limits.TimeoutMillis != sdd.AuthoringAttemptTimeout.Milliseconds() || started.Preference.Selection.ModelID != prepared.CodePreference.Selection.ModelID {
		t.Fatalf("Code selection/limits not persisted: %+v", started)
	}

	catalogAfterFirst := catalogCalls.Load()
	replay, err := s.StartAuthoringCode(codeStart)
	if err != nil || replay.ID != started.ID || replay.Status != "completed" || provider.calls.Load() != 2 || catalogCalls.Load() != catalogAfterFirst {
		t.Fatalf("idempotent Code replay repeated work: result=%+v err=%v provider=%d catalog=%d/%d", replay, err, provider.calls.Load(), catalogAfterFirst, catalogCalls.Load())
	}
	if _, err := s.Prompt(PromptInput{SessionID: started.SessionID, Text: "repeat the Code run"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("generic Prompt must not repeat an admitted Code attempt: %v", err)
	}
	sessions, err := db.ListAllSessions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	codeSessions := 0
	for _, session := range sessions {
		if session.Mode == catalog.AuthoringCodeSessionMode {
			codeSessions++
		}
	}
	if codeSessions != 1 {
		t.Fatalf("dedicated Code session count = %d; sessions=%+v", codeSessions, sessions)
	}
	currentPipeline, err := db.GetPipeline(t.Context(), pipeline.ID)
	if err != nil || currentPipeline.Status[sdd.Code] != sdd.Active || currentPipeline.Status[sdd.Eval] != sdd.Pending {
		t.Fatalf("Code changed the phase gate or finalized Eval: %+v error=%v", currentPipeline.Status, err)
	}
}

func TestAuthoringCodePatchLimitFailsClosedAndPreservesPrivateRoot(t *testing.T) {
	s, db, input, prepared := preparedCodeRunForTest(t, "code_patch_limit")
	s.authoringCodePatchMaxBytes = 64
	provider := &codeRunPrivateWriteProvider{}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }

	result, err := s.StartAuthoringCode(input)
	if err == nil || result.Status != "failed" || result.ErrorCode != "patch_too_large" || result.Result == nil {
		t.Fatalf("oversized patch did not fail with a preserved result snapshot: result=%+v error=%v", result, err)
	}
	stored, readErr := db.GetAuthoringCodeRunByID(t.Context(), result.ID)
	if readErr != nil || stored.Status != "failed" || stored.ErrorCode != "patch_too_large" || len(stored.PatchJSON) != 0 || stored.PatchHash != "" {
		t.Fatalf("oversized patch was truncated, reviewable, or not terminally recorded: %+v error=%v", stored, readErr)
	}
	if _, statErr := os.Stat(filepath.Join(prepared.PrivatePath, "code-only.txt")); statErr != nil {
		t.Fatalf("private result root was not preserved after patch limit failure: %v", statErr)
	}
}

func TestCancelAuthoringCodeJoinsAndReadsBackTerminalState(t *testing.T) {
	s, db, pipeline := approvedPlanForCodeRun(t)
	workspace, err := db.GetWorkspace(t.Context(), pipeline.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "source.txt"), []byte("before cancel"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.privateWorkspaceParent = t.TempDir()
	plan, err := db.GetAuthoringStage(t.Context(), pipeline.ID, sdd.Plan)
	if err != nil {
		t.Fatal(err)
	}
	preparationInput := authoringCodeCopyRequest(t, s, pipeline, "code_cancel_copy_confirmed_01")
	prepared, err := s.PrepareAuthoringCode(preparationInput)
	if err != nil {
		t.Fatal(err)
	}
	provider := &codeRunBlockingProvider{started: make(chan struct{}), returned: make(chan struct{})}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	start := StartAuthoringCodeInput{
		PipelineID: pipeline.ID, PreparationID: prepared.ID, PreparationRequestID: prepared.RequestID,
		RequestID: "code_cancel_start_request_01", ExpectedPipelineRevision: pipeline.Revision,
		ExpectedPlanStageRevision: plan.Revision, ExpectedPlanArtifactVersion: plan.ArtifactVersion,
		ExpectedCodePreferenceRevision: prepared.CodePreferenceRevision,
		ExpectedCodeSelectionHash:      prepared.CodeSelectionHash, ExpectedManifestHash: prepared.ManifestHash,
	}
	startedCh := make(chan struct {
		run AuthoringCodeRunDTO
		err error
	}, 1)
	go func() {
		run, err := s.StartAuthoringCode(start)
		startedCh <- struct {
			run AuthoringCodeRunDTO
			err error
		}{run: run, err: err}
	}()
	select {
	case <-provider.started:
	case <-time.After(3 * time.Second):
		t.Fatal("Code provider did not start")
	}
	runs, err := db.ListAuthoringCodeRuns(t.Context(), pipeline.ID)
	if err != nil || len(runs) != 1 || runs[0].SessionID == "" {
		t.Fatalf("durable active Code session = %+v error=%v", runs, err)
	}
	if err := s.Cancel(runs[0].SessionID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("generic Cancel bypassed durable Code cancellation: %v", err)
	}
	cancelled, err := s.CancelAuthoringCode(CancelAuthoringCodeInput{PipelineID: pipeline.ID, AttemptID: runs[0].ID})
	if err != nil || cancelled.Status != "cancelled" || cancelled.CancellationPending {
		t.Fatalf("CancelAuthoringCode readback = %+v error=%v", cancelled, err)
	}
	select {
	case <-provider.returned:
	default:
		t.Fatal("CancelAuthoringCode returned before the provider stream joined")
	}
	select {
	case result := <-startedCh:
		if result.run.Status != "cancelled" {
			t.Fatalf("StartAuthoringCode terminal result = %+v error=%v", result.run, result.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("StartAuthoringCode did not settle after cancellation")
	}
	readback, err := db.GetAuthoringCodeRunByID(t.Context(), runs[0].ID)
	if err != nil || readback.Status != "cancelled" || readback.ErrorCode != "cancelled" {
		t.Fatalf("durable cancellation state = %+v error=%v", readback, err)
	}
}

func TestRecoverInterruptsCodeRunAndReplayDoesNotResubmitPrompt(t *testing.T) {
	s, db, pipeline := approvedPlanForCodeRun(t)
	workspace, err := db.GetWorkspace(t.Context(), pipeline.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "source.txt"), []byte("restart baseline"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.privateWorkspaceParent = t.TempDir()
	plan, err := db.GetAuthoringStage(t.Context(), pipeline.ID, sdd.Plan)
	if err != nil {
		t.Fatal(err)
	}
	preparationInput := authoringCodeCopyRequest(t, s, pipeline, "code_restart_copy_confirmed_01")
	prepared, err := s.PrepareAuthoringCode(preparationInput)
	if err != nil {
		t.Fatal(err)
	}
	start := StartAuthoringCodeInput{
		PipelineID: pipeline.ID, PreparationID: prepared.ID, PreparationRequestID: prepared.RequestID,
		RequestID: "code_restart_start_request_01", ExpectedPipelineRevision: pipeline.Revision,
		ExpectedPlanStageRevision: plan.Revision, ExpectedPlanArtifactVersion: plan.ArtifactVersion,
		ExpectedCodePreferenceRevision: prepared.CodePreferenceRevision,
		ExpectedCodeSelectionHash:      prepared.CodeSelectionHash, ExpectedManifestHash: prepared.ManifestHash,
	}
	source, err := s.authoringCodePromptSource(pipeline, plan)
	if err != nil {
		t.Fatal(err)
	}
	sourceHash := sha256.Sum256([]byte(source))
	intentHash, err := authoringCodeRunIntentHash(start)
	if err != nil {
		t.Fatal(err)
	}
	attempt := catalog.AuthoringCodeRun{
		ID: "recovery_code_run_0001", PipelineID: pipeline.ID, PreparationID: prepared.ID,
		PreparationRequestID: prepared.RequestID, RequestID: start.RequestID, IntentHash: intentHash,
		WorkspaceID: pipeline.WorkspaceID, PipelineRevision: pipeline.Revision, PlanStageRevision: plan.Revision,
		PlanArtifactVersion: plan.ArtifactVersion, CodePreferenceRevision: prepared.CodePreferenceRevision,
		CodeSelectionHash: prepared.CodeSelectionHash, ManifestHash: prepared.ManifestHash,
		PlanSourceHash: hex.EncodeToString(sourceHash[:]), PrivatePath: prepared.PrivatePath,
		PreferenceSnapshot: authoringCodeCopyPreferenceSnapshot(prepared.CodePreference, prepared.CodeSelectionHash),
		ManifestSnapshot:   authoringCodeCopyManifestSnapshot(prepared.Manifest), MaxPromptBytes: authoringCodeMaxPromptBytes,
		MaxOutputTokens: authoringCodeMaxOutputTokens, MaxOutputTokensPerTurn: sdd.MaxAuthoringOutputTokens, MaxTurns: authoringCodeMaxTurns,
		MaxToolCalls: authoringCodeMaxToolCalls, TimeoutMillis: sdd.AuthoringAttemptTimeout.Milliseconds(), Status: "running",
	}
	reserved, created, err := db.BeginAuthoringCodeRun(t.Context(), attempt)
	if err != nil || !created {
		t.Fatalf("reserve pre-crash Code attempt: %+v created=%v error=%v", reserved, created, err)
	}
	providerCalls := new(atomic.Int32)
	restarted := NewService(s.ctx, Dependencies{Store: db, Secrets: s.secrets, External: map[string]ExternalBackend{}, PrivateWorkspaceParent: s.privateWorkspaceParent})
	restarted.providerFactory = func(openai.Config) (agentcore.Provider, error) {
		providerCalls.Add(1)
		return &codeRunPrivateWriteProvider{}, nil
	}
	t.Cleanup(func() { _ = Shutdown(restarted) })
	if err := restarted.Recover(t.Context()); err != nil {
		t.Fatalf("recover service after interrupted Code attempt: %v", err)
	}
	readback, err := db.GetAuthoringCodeRunByID(t.Context(), reserved.ID)
	if err != nil || readback.Status != "interrupted" || readback.ErrorCode != "app_restart" {
		t.Fatalf("recovered Code state = %+v error=%v", readback, err)
	}
	replay, err := restarted.StartAuthoringCode(start)
	if err != nil || replay.ID != reserved.ID || replay.Status != "interrupted" || providerCalls.Load() != 0 {
		t.Fatalf("post-restart replay repeated Code: %+v error=%v providerCalls=%d", replay, err, providerCalls.Load())
	}
}

func TestAuthoringCodeFinalReadbackBlocksCompletionWhenOriginDrifts(t *testing.T) {
	s, db, pipeline := approvedPlanForCodeRun(t)
	workspace, err := db.GetWorkspace(t.Context(), pipeline.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	originalPath := filepath.Join(workspace.Path, "source.txt")
	if err := os.WriteFile(originalPath, []byte("confirmed source"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.privateWorkspaceParent = t.TempDir()
	plan, err := db.GetAuthoringStage(t.Context(), pipeline.ID, sdd.Plan)
	if err != nil {
		t.Fatal(err)
	}
	preparation, err := s.PrepareAuthoringCode(authoringCodeCopyRequest(t, s, pipeline, "code_drift_copy_confirmed_01"))
	if err != nil || preparation.Status != "prepared" {
		t.Fatalf("Code preparation = %+v error=%v", preparation, err)
	}
	provider := &codeRunSourceDriftProvider{path: originalPath}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	input := StartAuthoringCodeInput{
		PipelineID: pipeline.ID, PreparationID: preparation.ID, PreparationRequestID: preparation.RequestID,
		RequestID: "code_drift_start_request_01", ExpectedPipelineRevision: pipeline.Revision,
		ExpectedPlanStageRevision: plan.Revision, ExpectedPlanArtifactVersion: plan.ArtifactVersion,
		ExpectedCodePreferenceRevision: preparation.CodePreferenceRevision,
		ExpectedCodeSelectionHash:      preparation.CodeSelectionHash, ExpectedManifestHash: preparation.ManifestHash,
	}
	result, err := s.StartAuthoringCode(input)
	if err == nil || result.Status != "failed" || result.ErrorCode != "snapshot_stale" {
		t.Fatalf("origin drift was offered as verifiable Code: result=%+v error=%v", result, err)
	}
	if result.SourceManifest == nil || !result.SourceDrift || result.SourceReadbackStatus != "drifted" || result.SourceManifest.Hash == result.ManifestHash {
		t.Fatalf("origin drift reconciliation evidence missing: %+v", result)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider call count = %d; want the one run that introduced concurrent drift", provider.calls.Load())
	}
	if _, err := os.Stat(preparation.PrivatePath); err != nil {
		t.Fatalf("private copy must be preserved for reconciliation: %v", err)
	}
	if original, err := os.ReadFile(originalPath); err != nil || string(original) != "external source drift" {
		t.Fatalf("source readback = %q error=%v", original, err)
	}
	if private, err := os.ReadFile(filepath.Join(preparation.PrivatePath, "source.txt")); err != nil || string(private) != "confirmed source" {
		t.Fatalf("private baseline was not preserved: %q error=%v", private, err)
	}
}

func TestAuthoringCodeClassifiesOriginDriftBeforeProviderComposition(t *testing.T) {
	s, db, input, prepared := preparedCodeRunForTest(t, "code_pre_provider_source_drift")
	pipeline, err := db.GetPipeline(t.Context(), input.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := db.GetWorkspace(t.Context(), pipeline.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	originalPath := filepath.Join(workspace.Path, "source.txt")
	provider := &codeRunPrivateWriteProvider{}
	factoryCalls := new(atomic.Int32)
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) {
		factoryCalls.Add(1)
		return provider, nil
	}
	s.store = &mutateCodeSourceAfterBeginStore{Store: db, mutate: func() error {
		return os.WriteFile(originalPath, []byte("source changed after reservation"), 0o600)
	}}

	result, err := s.StartAuthoringCode(input)
	if err == nil || result.Status != "failed" || result.ErrorCode != "snapshot_stale" {
		t.Fatalf("pre-provider source drift was not classified as stale: result=%+v error=%v", result, err)
	}
	if result.SourceManifest == nil || !result.SourceDrift || result.SourceReadbackStatus != "drifted" || result.SourceManifest.Hash == result.ManifestHash {
		t.Fatalf("pre-provider source drift readback missing: %+v", result)
	}
	if factoryCalls.Load() != 0 || provider.calls.Load() != 0 {
		t.Fatalf("source drift reached provider composition/inference: factory=%d provider=%d", factoryCalls.Load(), provider.calls.Load())
	}
	stored, err := db.GetAuthoringCodeRun(t.Context(), input.PipelineID, input.RequestID)
	if err != nil || stored.Status != "failed" || stored.ErrorCode != "snapshot_stale" || stored.SourceManifest == nil || stored.SourceManifest.Hash == stored.ManifestHash {
		t.Fatalf("pre-provider drift was not durably recorded: %+v error=%v", stored, err)
	}
	if _, err := os.Stat(prepared.PrivatePath); err != nil {
		t.Fatalf("private copy must remain available for reconciliation: %v", err)
	}
	private, err := os.ReadFile(filepath.Join(prepared.PrivatePath, "source.txt"))
	if err != nil || string(private) != "Code runtime budget input" {
		t.Fatalf("private baseline changed after source drift: content=%q error=%v", private, err)
	}
	original, err := os.ReadFile(originalPath)
	if err != nil || string(original) != "source changed after reservation" {
		t.Fatalf("source mutation readback = %q error=%v", original, err)
	}
}

func TestAuthoringCodeFinalReadbackDriftOverridesPreInferenceFailure(t *testing.T) {
	s, db, input, prepared := preparedCodeRunForTest(t, "code_pre_inference_source_drift")
	pipeline, err := db.GetPipeline(t.Context(), input.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := db.GetWorkspace(t.Context(), pipeline.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	originalPath := filepath.Join(workspace.Path, "source.txt")
	provider := &codeRunPrivateWriteProvider{}
	factoryCalls := new(atomic.Int32)
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) {
		factoryCalls.Add(1)
		return provider, nil
	}
	baseScan := s.authoringCodeScan
	mutated := new(atomic.Bool)
	s.authoringCodeScan = func(ctx context.Context, root string) (sddworkspace.Manifest, error) {
		manifest, scanErr := baseScan(ctx, root)
		if scanErr == nil && filepath.Clean(root) == filepath.Clean(workspace.Path) && mutated.CompareAndSwap(false, true) {
			if writeErr := os.WriteFile(originalPath, []byte("source changed after pre-provider scan"), 0o600); writeErr != nil {
				return sddworkspace.Manifest{}, writeErr
			}
		}
		return manifest, scanErr
	}

	result, err := s.StartAuthoringCode(input)
	if !mutated.Load() || err == nil || result.Status != "failed" || result.ErrorCode != "snapshot_stale" {
		t.Fatalf("pre-inference source drift was not classified as stale: result=%+v error=%v mutated=%v", result, err, mutated.Load())
	}
	if result.SourceManifest == nil || !result.SourceDrift || result.SourceReadbackStatus != "drifted" || result.SourceManifest.Hash == result.ManifestHash {
		t.Fatalf("pre-inference source drift readback missing: %+v", result)
	}
	if factoryCalls.Load() != 1 || provider.calls.Load() != 0 {
		t.Fatalf("pre-inference validation boundary calls: factory=%d provider=%d; want one factory and zero inference", factoryCalls.Load(), provider.calls.Load())
	}
	stored, err := db.GetAuthoringCodeRun(t.Context(), input.PipelineID, input.RequestID)
	if err != nil || stored.Status != "failed" || stored.ErrorCode != "snapshot_stale" || stored.SourceManifest == nil || stored.SourceManifest.Hash == stored.ManifestHash {
		t.Fatalf("pre-inference drift was not durably recorded: %+v error=%v", stored, err)
	}
	if _, err := os.Stat(prepared.PrivatePath); err != nil {
		t.Fatalf("private copy must remain available for reconciliation: %v", err)
	}
	private, err := os.ReadFile(filepath.Join(prepared.PrivatePath, "source.txt"))
	if err != nil || string(private) != "Code runtime budget input" {
		t.Fatalf("private baseline changed after source drift: content=%q error=%v", private, err)
	}
	original, err := os.ReadFile(originalPath)
	if err != nil || string(original) != "source changed after pre-provider scan" {
		t.Fatalf("source mutation readback = %q error=%v", original, err)
	}
}

func TestAuthoringCodeRejectsProfileEndpointDriftBeforeProviderComposition(t *testing.T) {
	s, db, input, prepared := preparedCodeRunForTest(t, "code_endpoint_drift")
	secondEndpoint := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(secondEndpoint.Close)
	profile, err := db.GetProviderProfile(t.Context(), prepared.CodePreference.Selection.BackendID)
	if err != nil {
		t.Fatal(err)
	}
	changedProfile := profile
	changedProfile.BaseURL = secondEndpoint.URL + "/v1"
	changedProfile.UpdatedAt = time.Now().UTC()
	providerCalls := new(atomic.Int32)
	factoryCalls := new(atomic.Int32)
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) {
		factoryCalls.Add(1)
		providerCalls.Add(1)
		return &codeRunPrivateWriteProvider{}, nil
	}
	s.store = &mutateCodeProfileAfterBeginStore{Store: db, mutate: func() error {
		return db.UpsertProviderProfile(t.Context(), changedProfile)
	}}
	result, err := s.StartAuthoringCode(input)
	if err == nil || result.Status != "failed" || factoryCalls.Load() != 0 || providerCalls.Load() != 0 {
		t.Fatalf("unapproved endpoint crossed Code composition: result=%+v error=%v factory=%d provider=%d", result, err, factoryCalls.Load(), providerCalls.Load())
	}
	stored, err := db.GetAuthoringCodeRun(t.Context(), input.PipelineID, input.RequestID)
	if err != nil || stored.Status != "failed" || stored.ErrorCode != "composition_failed" || stored.SessionID == "" {
		t.Fatalf("profile drift did not settle the admitted Code session: %+v error=%v", stored, err)
	}
	var eventCount int
	if err := db.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM events WHERE stream_id=?`, stored.SessionID).Scan(&eventCount); err != nil || eventCount != 0 {
		t.Fatalf("profile drift created provider/journal events: count=%d error=%v", eventCount, err)
	}
}

func TestAuthoringCodeRechecksProfileAtProviderCompositionBoundary(t *testing.T) {
	s, db, input, prepared := preparedCodeRunForTest(t, "code_profile_composition_race")
	secondEndpoint := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(secondEndpoint.Close)
	profile, err := db.GetProviderProfile(t.Context(), prepared.CodePreference.Selection.BackendID)
	if err != nil {
		t.Fatal(err)
	}
	changedProfile := profile
	changedProfile.BaseURL = secondEndpoint.URL + "/v1"
	changedProfile.UpdatedAt = time.Now().UTC()
	factoryCalls := new(atomic.Int32)
	provider := &codeRunPrivateWriteProvider{}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) {
		factoryCalls.Add(1)
		return provider, nil
	}
	store := &mutateCodeProfileAfterSnapshotStore{Store: db, mutate: func() error {
		return db.UpsertProviderProfile(t.Context(), changedProfile)
	}}
	s.store = store
	result, err := s.StartAuthoringCode(input)
	if err == nil || result.Status != "failed" || result.ErrorCode != "composition_failed" || !store.fired.Load() {
		t.Fatalf("profile mutation between snapshot and provider composition was not rejected: result=%+v error=%v mutated=%v", result, err, store.fired.Load())
	}
	if factoryCalls.Load() != 0 || provider.calls.Load() != 0 {
		t.Fatalf("unapproved endpoint reached provider composition/inference: factories=%d calls=%d", factoryCalls.Load(), provider.calls.Load())
	}
	if _, err := s.runner(result.SessionID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("failed profile-bound Code session remained executable: %v", err)
	}
	var eventCount int
	if err := db.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM events WHERE stream_id=?`, result.SessionID).Scan(&eventCount); err != nil || eventCount != 0 {
		t.Fatalf("profile mismatch emitted inference events: count=%d error=%v", eventCount, err)
	}
}

func TestAuthoringCodePatchReadbackRejectsCurrentSourceDrift(t *testing.T) {
	s, db, input, _ := preparedCodeRunForTest(t, "code_patch_source_drift")
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) {
		return &codeRunPrivateWriteProvider{}, nil
	}
	run, err := s.StartAuthoringCode(input)
	if err != nil || run.Status != "completed" {
		t.Fatalf("complete Code run: status=%q error=%v", run.Status, err)
	}
	pipeline, err := db.GetPipeline(t.Context(), input.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := db.GetWorkspace(t.Context(), pipeline.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "source.txt"), []byte("changed after Code completed"), 0o600); err != nil {
		t.Fatal(err)
	}
	firstPage, err := s.GetAuthoringCodeRunPatch(GetAuthoringCodeRunPatchInput{PipelineID: run.PipelineID, RunID: run.ID})
	if err != nil || firstPage.SourceReadbackStatus != "drifted" || firstPage.Content == "" || firstPage.PatchHash != run.PatchHash {
		t.Fatalf("patch inspection after source drift = %+v error=%v; want immutable patch marked drifted", firstPage, err)
	}
	continuation, err := s.GetAuthoringCodeRunPatch(GetAuthoringCodeRunPatchInput{PipelineID: run.PipelineID, RunID: run.ID, Cursor: 1})
	if err != nil || continuation.SourceReadbackStatus != "unchecked" || continuation.Cursor != 1 || continuation.Content == "" {
		t.Fatalf("continuation page after initial readback = %+v error=%v; want unchecked", continuation, err)
	}
	decisionInput := authoringCodeDecisionInput(run, "code_drift_decision_01", "approve")
	if _, err := s.DecideAuthoringCode(decisionInput); !errors.Is(err, ErrAuthoringCodePatchUnavailable) {
		t.Fatalf("Code approval after source drift error = %v; want fail-closed unavailable", err)
	}
	current, err := db.GetPipeline(t.Context(), run.PipelineID)
	if err != nil || current.Current != sdd.Code || current.Revision != run.PipelineRevision {
		t.Fatalf("source drift advanced pipeline: %+v error=%v", current, err)
	}
}

func TestDecideAuthoringCodeApprovalAdvancesOnceAndReplaysReceipt(t *testing.T) {
	s, db, run, provider := completedCodeRunForDecisionTest(t, "code_approve_decision")
	in := authoringCodeDecisionInput(run, "code_approve_request_01", "approve")
	decision, err := s.DecideAuthoringCode(in)
	if err != nil || decision.Action != "approve" || decision.RunID != run.ID || decision.PipelineRevision != run.PipelineRevision || decision.ResultPipelineRevision != run.PipelineRevision+1 {
		t.Fatalf("approve Code decision = %+v error=%v", decision, err)
	}
	pipeline, err := db.GetPipeline(t.Context(), run.PipelineID)
	if err != nil || pipeline.Current != sdd.Eval || pipeline.Status[sdd.Code] != sdd.Completed || pipeline.Status[sdd.Eval] != sdd.Active || pipeline.Revision != decision.ResultPipelineRevision {
		t.Fatalf("approved Code pipeline = %+v error=%v", pipeline, err)
	}
	replay, err := s.DecideAuthoringCode(in)
	if err != nil || !reflect.DeepEqual(replay, decision) {
		t.Fatalf("idempotent Code approval replay = %+v error=%v; want %+v", replay, err, decision)
	}
	if provider.calls.Load() != 2 {
		t.Fatalf("Code provider calls after decision replay = %d; want one run (two turns)", provider.calls.Load())
	}
	var decisions int
	if err := db.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM pipeline_authoring_code_decisions WHERE pipeline_id=?`, run.PipelineID).Scan(&decisions); err != nil || decisions != 1 {
		t.Fatalf("persisted Code decisions = %d error=%v; want exactly one", decisions, err)
	}
}

func TestDecideAuthoringCodeRevisionPersistsFeedbackWithoutRerun(t *testing.T) {
	s, db, run, provider := completedCodeRunForDecisionTest(t, "code_revision_decision")
	in := authoringCodeDecisionInput(run, "code_revision_request_01", "request_revision")
	in.Feedback = "Preserve the original API result when updating the file."
	decision, err := s.DecideAuthoringCode(in)
	if err != nil || decision.Action != "request_revision" || decision.Feedback != in.Feedback || decision.ResultPipelineRevision != run.PipelineRevision+1 {
		t.Fatalf("request Code revision = %+v error=%v", decision, err)
	}
	pipeline, err := db.GetPipeline(t.Context(), run.PipelineID)
	if err != nil || pipeline.Current != sdd.Code || pipeline.Status[sdd.Code] != sdd.Active || pipeline.Revision != decision.ResultPipelineRevision {
		t.Fatalf("revision request changed Code state: %+v error=%v", pipeline, err)
	}
	replay, err := s.DecideAuthoringCode(in)
	if err != nil || !reflect.DeepEqual(replay, decision) {
		t.Fatalf("idempotent revision request replay = %+v error=%v; want %+v", replay, err, decision)
	}
	conflictingReplay := in
	conflictingReplay.Feedback = "different payload with the same idempotency key"
	if _, err := s.DecideAuthoringCode(conflictingReplay); !errors.Is(err, sqlite.ErrPipelineConflict) {
		t.Fatalf("divergent replay error = %v; want pipeline conflict", err)
	}
	outOfOrder := in
	outOfOrder.RequestID = "code_revision_request_02"
	outOfOrder.PipelineRevision = decision.ResultPipelineRevision
	if _, err := s.DecideAuthoringCode(outOfOrder); !errors.Is(err, sqlite.ErrPipelineConflict) {
		t.Fatalf("second revision request against the old run error = %v; want conflict", err)
	}
	if provider.calls.Load() != 2 {
		t.Fatalf("revision request re-executed Code: provider calls=%d; want two turns total", provider.calls.Load())
	}
	pipeline, err = db.GetPipeline(t.Context(), run.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	preparation, err := s.PrepareAuthoringCode(authoringCodeCopyRequest(t, s, pipeline, "code_after_revision_preparation"))
	if err != nil || preparation.Status != "prepared" {
		t.Fatalf("preparation after explicit revision request = %+v error=%v", preparation, err)
	}
	if runs, err := db.ListAuthoringCodeRuns(t.Context(), pipeline.ID); err != nil || len(runs) != 1 || provider.calls.Load() != 2 {
		t.Fatalf("new preparation re-executed Code: runs=%d provider calls=%d error=%v", len(runs), provider.calls.Load(), err)
	}
	plan, err := db.GetAuthoringStage(t.Context(), pipeline.ID, sdd.Plan)
	if err != nil {
		t.Fatal(err)
	}
	nextRun, err := s.StartAuthoringCode(StartAuthoringCodeInput{
		PipelineID: pipeline.ID, PreparationID: preparation.ID, PreparationRequestID: preparation.RequestID,
		RequestID: "code_after_revision_start", ExpectedPipelineRevision: pipeline.Revision,
		ExpectedPlanStageRevision: plan.Revision, ExpectedPlanArtifactVersion: plan.ArtifactVersion,
		ExpectedCodePreferenceRevision: preparation.CodePreferenceRevision,
		ExpectedCodeSelectionHash:      preparation.CodeSelectionHash, ExpectedManifestHash: preparation.ManifestHash,
	})
	if err != nil || nextRun.Status != "completed" || nextRun.ID == run.ID || provider.calls.Load() != 4 {
		t.Fatalf("explicit second Code execution after revision request = %+v error=%v calls=%d", nextRun, err, provider.calls.Load())
	}
	var feedback string
	if err := db.DB().QueryRowContext(t.Context(), `SELECT feedback FROM pipeline_authoring_code_decisions WHERE run_id=?`, run.ID).Scan(&feedback); err != nil || feedback != in.Feedback {
		t.Fatalf("persisted revision feedback = %q error=%v", feedback, err)
	}
}

func TestPrepareAuthoringCodeRequiresRevisionDecisionAfterCompletedRun(t *testing.T) {
	s, db, run, provider := completedCodeRunForDecisionTest(t, "code_prepare_revision_gate")
	pipeline, err := db.GetPipeline(t.Context(), run.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	request := authoringCodeCopyRequest(t, s, pipeline, "code_prepare_without_revision_decision")
	if _, err := s.PrepareAuthoringCode(request); !errors.Is(err, sqlite.ErrPipelineConflict) {
		t.Fatalf("new preparation before revision decision error = %v; want pipeline conflict", err)
	}
	var preparations int
	if err := db.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM pipeline_authoring_code_copy_attempts WHERE pipeline_id=?`, run.PipelineID).Scan(&preparations); err != nil || preparations != 1 {
		t.Fatalf("unapproved follow-up preparation count = %d error=%v; want original only", preparations, err)
	}
	if provider.calls.Load() != 2 {
		t.Fatalf("rejected follow-up preparation executed Code: calls=%d", provider.calls.Load())
	}
}

func TestStartAuthoringCodeRequiresRevisionDecisionForPrecreatedCopy(t *testing.T) {
	s, db, firstInput, _ := preparedCodeRunForTest(t, "code_precreated_round_two")
	pipeline, err := db.GetPipeline(t.Context(), firstInput.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	secondPreparation, err := s.PrepareAuthoringCode(authoringCodeCopyRequest(t, s, pipeline, "code_precreated_before_first_run"))
	if err != nil || secondPreparation.Status != "prepared" {
		t.Fatalf("precreate second Code copy = %+v error=%v", secondPreparation, err)
	}
	provider := &codeRunPrivateWriteProvider{}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	firstRun, err := s.StartAuthoringCode(firstInput)
	if err != nil || firstRun.Status != "completed" {
		t.Fatalf("complete first Code run: status=%q error=%v", firstRun.Status, err)
	}
	plan, err := db.GetAuthoringStage(t.Context(), pipeline.ID, sdd.Plan)
	if err != nil {
		t.Fatal(err)
	}
	secondInput := StartAuthoringCodeInput{
		PipelineID: pipeline.ID, PreparationID: secondPreparation.ID, PreparationRequestID: secondPreparation.RequestID,
		RequestID: "code_precreated_second_start", ExpectedPipelineRevision: pipeline.Revision,
		ExpectedPlanStageRevision: plan.Revision, ExpectedPlanArtifactVersion: plan.ArtifactVersion,
		ExpectedCodePreferenceRevision: secondPreparation.CodePreferenceRevision,
		ExpectedCodeSelectionHash:      secondPreparation.CodeSelectionHash, ExpectedManifestHash: secondPreparation.ManifestHash,
	}
	if _, err := s.StartAuthoringCode(secondInput); !errors.Is(err, sqlite.ErrPipelineConflict) {
		t.Fatalf("run from a precreated copy before revision decision error = %v; want pipeline conflict", err)
	}
	if provider.calls.Load() != 2 {
		t.Fatalf("unreviewed second run reached Code provider: calls=%d", provider.calls.Load())
	}
}

func TestDecideAuthoringCodeRejectsPatchHashAndPipelineRevisionDrift(t *testing.T) {
	s, _, run, _ := completedCodeRunForDecisionTest(t, "code_decision_bindings")
	base := authoringCodeDecisionInput(run, "code_binding_request_01", "approve")
	tests := []struct {
		name   string
		mutate func(*DecideAuthoringCodeInput)
	}{
		{name: "patch hash", mutate: func(in *DecideAuthoringCodeInput) { in.PatchHash = strings.Repeat("0", 64) }},
		{name: "baseline hash", mutate: func(in *DecideAuthoringCodeInput) { in.BaselineHash = strings.Repeat("0", 64) }},
		{name: "source manifest hash", mutate: func(in *DecideAuthoringCodeInput) { in.SourceManifestHash = strings.Repeat("0", 64) }},
		{name: "result manifest hash", mutate: func(in *DecideAuthoringCodeInput) { in.ResultManifestHash = strings.Repeat("0", 64) }},
		{name: "pipeline revision", mutate: func(in *DecideAuthoringCodeInput) { in.PipelineRevision++ }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			in := base
			in.RequestID = "code_binding_" + strings.ReplaceAll(test.name, " ", "_")
			test.mutate(&in)
			if _, err := s.DecideAuthoringCode(in); !errors.Is(err, sqlite.ErrPipelineConflict) {
				t.Fatalf("decision with stale %s error = %v; want pipeline conflict", test.name, err)
			}
		})
	}
}

func TestAuthoringCodePatchReadbackReportsModeAndSymlinkDrift(t *testing.T) {
	s, db, run, _ := completedCodeRunForDecisionTest(t, "code_snapshot_drift")
	pipeline, err := db.GetPipeline(t.Context(), run.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := db.GetWorkspace(t.Context(), pipeline.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(workspace.Path, "source.txt")
	if err := os.Chmod(sourcePath, 0o700); err != nil {
		t.Fatal(err)
	}
	page, err := s.GetAuthoringCodeRunPatch(GetAuthoringCodeRunPatchInput{PipelineID: run.PipelineID, RunID: run.ID})
	if err != nil || page.SourceReadbackStatus != "drifted" {
		t.Fatalf("readback after executable-mode change = status %q error=%v; want drifted", page.SourceReadbackStatus, err)
	}
	if err := os.Chmod(sourcePath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sourcePath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "replacement.txt"), []byte("replacement target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("replacement.txt", sourcePath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	page, err = s.GetAuthoringCodeRunPatch(GetAuthoringCodeRunPatchInput{PipelineID: run.PipelineID, RunID: run.ID})
	if err != nil || page.SourceReadbackStatus != "drifted" {
		t.Fatalf("readback after symlink replacement = status %q error=%v; want drifted", page.SourceReadbackStatus, err)
	}
}

func TestAuthoringCodePatchReadbackReportsPrivateResultDrift(t *testing.T) {
	s, _, run, _ := completedCodeRunForDecisionTest(t, "code_result_drift")
	if err := os.WriteFile(filepath.Join(run.PrivatePath, "code-only.txt"), []byte("changed after run completion"), 0o600); err != nil {
		t.Fatal(err)
	}
	page, err := s.GetAuthoringCodeRunPatch(GetAuthoringCodeRunPatchInput{PipelineID: run.PipelineID, RunID: run.ID})
	if err != nil || page.SourceReadbackStatus != "drifted" {
		t.Fatalf("patch readback after private result drift = status %q error=%v; want drifted", page.SourceReadbackStatus, err)
	}
	if _, err := s.DecideAuthoringCode(authoringCodeDecisionInput(run, "code_result_drift_approval", "approve")); !errors.Is(err, ErrAuthoringCodePatchUnavailable) {
		t.Fatalf("approval after private result drift error = %v; want fail-closed unavailable", err)
	}
}

type gatedCodeRunProvider struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (*gatedCodeRunProvider) ID() string { return "gated-code-run" }
func (*gatedCodeRunProvider) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true, ToolCalls: true}
}
func (p *gatedCodeRunProvider) Stream(ctx context.Context, _ agentcore.ChatRequest) (<-chan agentcore.StreamEvent, <-chan error) {
	p.once.Do(func() { close(p.started) })
	select {
	case <-ctx.Done():
	case <-p.release:
	}
	stream := make(chan agentcore.StreamEvent, 1)
	stream <- agentcore.StreamEvent{Type: "text_delta", Delta: "Code result"}
	close(stream)
	errors := make(chan error)
	close(errors)
	return stream, errors
}

func TestDecideAuthoringCodeRejectsNonCompletedRun(t *testing.T) {
	s, db, start, _ := preparedCodeRunForTest(t, "code_decision_running")
	provider := &gatedCodeRunProvider{started: make(chan struct{}), release: make(chan struct{})}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	completed := make(chan AuthoringCodeRunDTO, 1)
	startErr := make(chan error, 1)
	go func() {
		run, err := s.StartAuthoringCode(start)
		completed <- run
		startErr <- err
	}()
	<-provider.started
	started, err := db.GetAuthoringCodeRun(t.Context(), start.PipelineID, start.RequestID)
	if err != nil || started.Status != "running" {
		close(provider.release)
		t.Fatalf("in-flight Code run = %+v error=%v", started, err)
	}
	decision := DecideAuthoringCodeInput{
		PipelineID: started.PipelineID, PipelineRevision: started.PipelineRevision, RunID: started.ID,
		RequestID: "code_running_decision_01", Action: "approve", PatchHash: strings.Repeat("a", 64),
		BaselineHash: started.ManifestHash, SourceManifestHash: started.ManifestHash, ResultManifestHash: started.ManifestHash,
	}
	if _, err := s.DecideAuthoringCode(decision); !errors.Is(err, sqlite.ErrPipelineConflict) {
		close(provider.release)
		t.Fatalf("decision for running Code run error = %v; want pipeline conflict", err)
	}
	close(provider.release)
	if run := <-completed; run.Status != "completed" {
		t.Fatalf("gated Code run ended with status %q", run.Status)
	}
	if err := <-startErr; err != nil {
		t.Fatalf("gated Code run finish: %v", err)
	}
}

func completedCodeRunForDecisionTest(t *testing.T, prefix string) (*Service, *sqlite.Store, AuthoringCodeRunDTO, *codeRunPrivateWriteProvider) {
	t.Helper()
	s, db, input, _ := preparedCodeRunForTest(t, prefix)
	provider := &codeRunPrivateWriteProvider{}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	run, err := s.StartAuthoringCode(input)
	if err != nil || run.Status != "completed" || run.SourceManifest == nil || run.Result == nil {
		t.Fatalf("complete Code run = %+v error=%v", run, err)
	}
	return s, db, run, provider
}

func authoringCodeDecisionInput(run AuthoringCodeRunDTO, requestID, action string) DecideAuthoringCodeInput {
	return DecideAuthoringCodeInput{
		PipelineID: run.PipelineID, PipelineRevision: run.PipelineRevision, RunID: run.ID,
		RequestID: requestID, Action: action, PatchHash: run.PatchHash,
		BaselineHash: run.ManifestHash, SourceManifestHash: run.SourceManifest.Hash,
		ResultManifestHash: run.Result.Hash,
	}
}

func preparedCodeRunForTest(t *testing.T, prefix string) (*Service, *sqlite.Store, StartAuthoringCodeInput, AuthoringCodeCopyPreparationDTO) {
	t.Helper()
	s, db, pipeline := approvedPlanForCodeRun(t)
	workspace, err := db.GetWorkspace(t.Context(), pipeline.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "source.txt"), []byte("Code runtime budget input"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.privateWorkspaceParent = t.TempDir()
	plan, err := db.GetAuthoringStage(t.Context(), pipeline.ID, sdd.Plan)
	if err != nil {
		t.Fatal(err)
	}
	preparation, err := s.PrepareAuthoringCode(authoringCodeCopyRequest(t, s, pipeline, prefix+"_copy_request"))
	if err != nil {
		t.Fatalf("prepare %s: %v", prefix, err)
	}
	start := StartAuthoringCodeInput{
		PipelineID: pipeline.ID, PreparationID: preparation.ID, PreparationRequestID: preparation.RequestID,
		RequestID: prefix + "_start_request", ExpectedPipelineRevision: pipeline.Revision,
		ExpectedPlanStageRevision: plan.Revision, ExpectedPlanArtifactVersion: plan.ArtifactVersion,
		ExpectedCodePreferenceRevision: preparation.CodePreferenceRevision,
		ExpectedCodeSelectionHash:      preparation.CodeSelectionHash, ExpectedManifestHash: preparation.ManifestHash,
	}
	return s, db, start, preparation
}

func approvedPlanForCodeRun(t *testing.T) (*Service, *sqlite.Store, catalog.PipelineRun) {
	t.Helper()
	s, db, initial, _ := authoringGenerationFixture(t)
	specProvider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: authoringSpecOutput}}}
	authoringProviderFactoryCounter(s, specProvider)
	spec, err := s.GenerateAuthoringStage(initial)
	if err != nil || spec.State != "waiting_user" {
		t.Fatalf("generate SPEC = %+v error=%v", spec, err)
	}
	approveTestAuthoringStage(t, s, spec)
	planState, err := db.GetAuthoringStage(t.Context(), spec.PipelineID, sdd.Plan)
	if err != nil {
		t.Fatal(err)
	}
	planInput := GenerateAuthoringStageInput{
		Ref:       AuthoringStageRefInput{PipelineID: spec.PipelineID, Stage: string(sdd.Plan), RequestID: "code_run_generate_plan_01", PipelineRevision: planState.PipelineRevision, StageRevision: planState.Revision, DiscoveryVersion: planState.DiscoveryVersion, ArtifactVersion: planState.ArtifactVersion},
		Selection: initial.Selection,
	}
	planProvider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: authoringPlanOutput}}}
	authoringProviderFactoryCounter(s, planProvider)
	plan, err := s.GenerateAuthoringStage(planInput)
	if err != nil || plan.State != "waiting_user" {
		t.Fatalf("generate Plan = %+v error=%v", plan, err)
	}
	approveTestAuthoringStage(t, s, plan)
	pipeline, err := db.GetPipeline(t.Context(), spec.PipelineID)
	if err != nil || pipeline.Current != sdd.Code || pipeline.Status[sdd.Plan] != sdd.Completed {
		t.Fatalf("approved Plan did not admit Code: %+v error=%v", pipeline, err)
	}
	return s, db, pipeline
}

func approveTestAuthoringStage(t *testing.T, s *Service, stage AuthoringStageDTO) {
	t.Helper()
	_, err := s.ApproveAuthoringStage(AuthoringStageDecisionInput{Ref: AuthoringStageRefInput{
		PipelineID: stage.PipelineID, Stage: stage.Stage, RequestID: "approve_" + stage.Stage + "_for_code_run",
		PipelineRevision: stage.PipelineRevision, StageRevision: stage.Revision,
		DiscoveryVersion: stage.DiscoveryVersion, ArtifactVersion: stage.ArtifactVersion,
	}})
	if err != nil {
		t.Fatalf("approve %s: %v", stage.Stage, err)
	}
}

func setCodeRunString(t *testing.T, value reflect.Value, name, input string) {
	t.Helper()
	field := value.FieldByName(name)
	if !field.IsValid() || field.Kind() != reflect.String || !field.CanSet() {
		t.Fatalf("Code run input field %s is unavailable", name)
	}
	field.SetString(input)
}

func setCodeRunInt(t *testing.T, value reflect.Value, name string, input int64) {
	t.Helper()
	field := value.FieldByName(name)
	if !field.IsValid() || field.Kind() != reflect.Int64 || !field.CanSet() {
		t.Fatalf("Code run input field %s is unavailable", name)
	}
	field.SetInt(input)
}

func eventsAsTypes(items []events.Event) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Type)
	}
	return out
}
