package application

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/modelcatalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/secrets"
)

type countingAPIRunner struct{ prompts atomic.Int32 }

func (r *countingAPIRunner) Prompt(context.Context, string) error { r.prompts.Add(1); return nil }
func (*countingAPIRunner) Cancel() bool                           { return false }
func (*countingAPIRunner) Abort(context.Context) error            { return nil }

func TestSelectedAPIRunnerCancelAndBusyFenceDuringCatalogPreflight(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var block atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if block.Load() {
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"chosen"}]}`)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(unblock)
	s, db, _ := setup(t)
	s.modelHTTPClient = server.Client()
	in := profileInput()
	in.ID, in.ProviderType, in.BaseURL = "api", "openai", server.URL+"/v1"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := s.prepareAPIModelSelection(t.Context(), apiSelectionInput(result, in.ID, "chosen", 321))
	if err != nil {
		t.Fatal(err)
	}
	base := &countingAPIRunner{}
	runner := &selectedAPIRunner{base: base, service: s, selection: *selection, forSDD: true}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record := catalog.SessionRecord{ID: "attempt-cancel", WorkspaceID: workspace.ID, BackendID: in.ID, Mode: "sdd_readonly", Status: "ready", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := db.UpsertSession(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.sessions[record.ID] = runner
	s.mu.Unlock()
	block.Store(true)
	finished := make(chan error, 1)
	go func() { finished <- runner.Prompt(t.Context(), "first") }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("preflight did not start")
	}
	busyCtx, stopBusy := context.WithTimeout(t.Context(), time.Second)
	defer stopBusy()
	if err := runner.Prompt(busyCtx, "second"); !errors.Is(err, agentcore.ErrSessionBusy) {
		t.Fatalf("concurrent prompt = %v", err)
	}
	if err := s.Cancel(record.ID); err != nil {
		t.Fatalf("service cancellation during preflight = %v", err)
	}
	unblock()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled prompt = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled prompt remained blocked")
	}
	if got := base.prompts.Load(); got != 0 {
		t.Fatalf("inference calls after cancellation = %d", got)
	}
}

func TestSelectedAPIRunnerAbortWaitsForPreflightAndSeals(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var block atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if block.Load() {
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"chosen"}]}`)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(unblock)
	s, _, _ := setup(t)
	s.modelHTTPClient = server.Client()
	in := profileInput()
	in.ID, in.ProviderType, in.BaseURL = "api", "openai", server.URL+"/v1"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := s.prepareAPIModelSelection(t.Context(), apiSelectionInput(result, in.ID, "chosen", 321))
	if err != nil {
		t.Fatal(err)
	}
	base := &countingAPIRunner{}
	runner := &selectedAPIRunner{base: base, service: s, selection: *selection, forSDD: true}
	block.Store(true)
	finished := make(chan error, 1)
	go func() { finished <- runner.Prompt(t.Context(), "first") }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("preflight did not start")
	}
	abortCtx, stopAbort := context.WithTimeout(t.Context(), 3*time.Second)
	defer stopAbort()
	abortErr := runner.Abort(abortCtx)
	unblock()
	if abortErr != nil {
		t.Fatal(abortErr)
	}
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("aborted prompt = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("aborted prompt remained blocked")
	}
	if err := runner.Prompt(t.Context(), "later"); !errors.Is(err, context.Canceled) {
		t.Fatalf("sealed runner = %v", err)
	}
	if got := base.prompts.Load(); got != 0 {
		t.Fatalf("inference calls after abort = %d", got)
	}
}

func apiSelectionInput(result modelcatalog.Result, profileID, modelID string, cap int) APIModelSelectionInput {
	return APIModelSelectionInput{ProfileID: profileID, ModelID: modelID, CatalogRevision: result.ProfileRevision, Source: result.Source, Destination: result.Destination, CheckedAt: result.CheckedAt, CredentialToken: result.CredentialToken, MaxOutputTokens: cap, ForSDD: cap > 0}
}

func TestPipelineCoderPersistsSelectedAPIModelFromFreshCatalog(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"chosen-model"}]}`)
	}))
	t.Cleanup(server.Close)
	s, db, _ := setup(t)
	s.modelHTTPClient = server.Client()
	profile := profileInput()
	profile.ID, profile.ProviderType, profile.BaseURL = "api-work", "openai", server.URL+"/v1"
	if _, err := s.SaveProviderProfile(profile); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "TODO", Objective: "Criar TODO"})
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"discovery", "spec", "plan"} {
		if _, err := s.SavePipelineArtifact(SavePipelineArtifactInput{PipelineID: run.ID, Stage: stage, Content: "Critérios do TODO"}); err != nil {
			t.Fatal(err)
		}
		run, err = s.AdvancePipeline(run.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: profile.ID})
	if err != nil || catalog.Status != modelcatalog.StatusComplete {
		t.Fatalf("query API catalog: %+v %v", catalog, err)
	}
	selection := apiSelectionInput(catalog, profile.ID, "chosen-model", 0)
	selection.Executor = "api"
	if _, err := s.preparePipelineRoleModelSelection(t.Context(), workspace.ID, profile.ID, selection); err != nil {
		t.Fatalf("prepare pipeline API selection: %+v: %v", selection, err)
	}
	stale := selection
	stale.CatalogRevision = "stale-revision"
	if _, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: profile.ID, Role: "coder", Selection: &stale, ConfirmWorkspaceCopy: true}); !errors.Is(err, ErrBackendChanged) {
		t.Fatalf("stale API model selection admitted: %v", err)
	}
	var sessions int
	if err := db.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE workspace_id=?`, workspace.ID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("stale API model choice created %d sessions: %v", sessions, err)
	}
	mismatched := selection
	mismatched.BackendID = "another-profile"
	if _, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: profile.ID, Role: "coder", Selection: &mismatched, ConfirmWorkspaceCopy: true}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("API selection for another provider admitted: %v", err)
	}
	linked, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: profile.ID, Role: "coder", Selection: &selection, ConfirmWorkspaceCopy: true})
	if err != nil {
		t.Fatal(err)
	}
	links, err := db.ListPipelineSessions(t.Context(), run.ID, "coder")
	if err != nil || len(links) != 1 {
		t.Fatalf("read isolated API Code session: %+v %v", links, err)
	}
	codeSnapshot, err := s.executionSnapshotForLink(links[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codeSnapshot.Root, "todo.go"), []byte("package todo\nfunc addTask() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	codeSelection, err := db.GetSessionModelSelection(t.Context(), linked.Session.ID)
	if err != nil || codeSelection.BackendID != profile.ID || codeSelection.ModelID != "chosen-model" || codeSelection.CatalogRevision != catalog.ProfileRevision || codeSelection.Source != catalog.Source || codeSelection.CredentialIdentity == "" {
		t.Fatalf("pipeline Code model selection was not durably bound: %+v %v", codeSelection, err)
	}
	for _, event := range []struct {
		typeName string
		data     any
	}{
		{"tool.completed", map[string]any{"details": map[string]string{"diff": "diff --git a/todo.go b/todo.go\n+func addTask() {}"}}},
		{"run.completed", map[string]string{"reason": ""}},
	} {
		if _, err := db.Append(t.Context(), linked.Session.ID, "agent_session", event.typeName, event.data); err != nil {
			t.Fatal(err)
		}
	}
	run, err = s.CompletePipelineCode(run.ID)
	if err != nil || run.CurrentStage != "code" || run.StageStatus["code"] != "waiting_user" {
		t.Fatalf("API Code did not wait for human review: %+v %v", run, err)
	}
	run = decidePipelineStageReviewForTest(t, s, run, "code", "approve", "")
	evaluator, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: profile.ID, Role: "evaluator", Selection: &selection})
	if err != nil {
		t.Fatal(err)
	}
	evaluatorRecord, recordErr := db.GetSession(t.Context(), evaluator.Session.ID)
	evaluatorSelection, selectionErr := db.GetSessionModelSelection(t.Context(), evaluator.Session.ID)
	if recordErr != nil || selectionErr != nil || evaluatorRecord.Mode != "evaluation" || evaluatorSelection.BackendID != profile.ID || evaluatorSelection.ModelID != "chosen-model" || evaluatorSelection.CatalogRevision != catalog.ProfileRevision {
		t.Fatalf("pipeline Eval model selection was not durably bound: record=%+v selection=%+v errors=%v/%v", evaluatorRecord, evaluatorSelection, recordErr, selectionErr)
	}
}

func TestSDDRunnerFailsClosedWithoutPositiveSelectedCap(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"chosen"}]}`)
	}))
	t.Cleanup(server.Close)
	s, db, _ := setup(t)
	s.modelHTTPClient = server.Client()
	in := profileInput()
	in.ID, in.ProviderType, in.BaseURL = "api", "openai", server.URL+"/v1"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	storedWorkspace, err := db.GetWorkspace(t.Context(), workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := catalog.SessionRecord{ID: "attempt", WorkspaceID: workspace.ID, BackendID: in.ID, Mode: "sdd_readonly", Status: "ready", CreatedAt: now, UpdatedAt: now}
	var created atomic.Int32
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { created.Add(1); return &fakeProvider{}, nil }
	if _, journal, err := s.makeRunner(&record, storedWorkspace, restoredHistory{}, false, nil, "", nil); err == nil {
		if journal != nil {
			journal.Close()
		}
		t.Fatal("SDD runner admitted without selection")
	}
	if created.Load() != 0 {
		t.Fatal("provider constructed before selection gate")
	}
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	choice := apiSelectionInput(result, in.ID, "chosen", 0)
	selection, err := s.prepareAPIModelSelection(t.Context(), choice)
	if err != nil {
		t.Fatal(err)
	}
	selection.SessionID, selection.WorkspacePath = record.ID, storedWorkspace.Path
	if _, journal, err := s.makeRunner(&record, storedWorkspace, restoredHistory{}, false, nil, "", selection); err == nil {
		if journal != nil {
			journal.Close()
		}
		t.Fatal("SDD runner admitted with zero cap")
	}
	if created.Load() != 0 {
		t.Fatal("provider constructed before cap gate")
	}
	if err := db.UpsertSession(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	if resumable, _, err := s.sessionContinuation(record); err == nil || resumable {
		t.Fatal("missing SDD snapshot reported resumable", err)
	}
	if _, journal, err := s.makeRunner(&record, storedWorkspace, restoredHistory{}, true, nil, "", nil); err == nil {
		if journal != nil {
			journal.Close()
		}
		t.Fatal("SDD reopen admitted without snapshot")
	}
	if created.Load() != 0 {
		t.Fatal("provider constructed on missing snapshot")
	}
}

func TestPrepareAPISelectionFreezesExplicitModelAndOutputCap(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"selected-model"}]}`)
	}))
	t.Cleanup(server.Close)
	s, _, _ := setup(t)
	s.modelHTTPClient = server.Client()
	in := profileInput()
	in.ID, in.ProviderType, in.BaseURL, in.Model = "api", "openai", server.URL+"/v1", "default-model"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: in.ID})
	if err != nil || !result.Complete {
		t.Fatal(result, err)
	}
	selection, err := s.prepareAPIModelSelection(t.Context(), apiSelectionInput(result, in.ID, "selected-model", 321))
	if err != nil {
		t.Fatal(err)
	}
	if selection.ModelID != "selected-model" || selection.CatalogRevision != result.ProfileRevision || selection.Destination != result.Destination || selection.Source != result.Source || selection.Status != "listed" || selection.MaxOutputTokens != 321 || selection.CredentialIdentity == "" || selection.CheckedAt.IsZero() || selection.ExecutablePath != "" || selection.LocalRevision != "" {
		t.Fatalf("selection = %+v", selection)
	}
	if err := s.revalidateAPIModelSelection(t.Context(), *selection, true); err != nil {
		t.Fatal(err)
	}
}

func TestAPIRunnerUsesFrozenSelectedIDAndOutputCap(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"selected-model"}]}`)
	}))
	t.Cleanup(server.Close)
	s, db, _ := setup(t)
	s.modelHTTPClient = server.Client()
	in := profileInput()
	in.ID, in.ProviderType, in.BaseURL, in.Model = "api", "openai", server.URL+"/v1", "default-model"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := s.prepareAPIModelSelection(t.Context(), apiSelectionInput(result, in.ID, "selected-model", 321))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := catalog.SessionRecord{ID: "attempt", WorkspaceID: workspace.ID, BackendID: in.ID, Mode: "sdd_readonly", Status: "ready", CreatedAt: now, UpdatedAt: now}
	selection.SessionID, selection.WorkspacePath = record.ID, workspace.Path
	if err := db.CreateSessionWithSnapshots(t.Context(), record, nil, "", nil, "agent_session", selection); err != nil {
		t.Fatal(err)
	}
	dto, err := s.GetSessionModelSelection(record.ID)
	if err != nil || dto.ModelID != "selected-model" || dto.MaxOutputTokens != 321 || dto.Destination != selection.Destination || dto.Status != "listed" {
		t.Fatal(dto, err)
	}
	raw, _ := json.Marshal(dto)
	if strings.Contains(string(raw), selection.CredentialIdentity) || strings.Contains(string(raw), in.APIKey) {
		t.Fatal("credential identity escaped readback")
	}
	fake := &fakeProvider{}
	s.providerFactory = func(c openai.Config) (agentcore.Provider, error) { fake.key = c.APIKey; return fake, nil }
	storedWorkspace, err := db.GetWorkspace(t.Context(), workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	runner, journal, err := s.makeRunner(&record, storedWorkspace, restoredHistory{}, true, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(journal.Close)
	if err := runner.Prompt(context.Background(), "generate"); err != nil {
		t.Fatal(err)
	}
	if fake.request.Model != "selected-model" || fake.request.MaxOutputTokens != 321 {
		t.Fatalf("provider request = %+v", fake.request)
	}
}

func TestPrepareAPISelectionRejectsRemovedModelAndStaleRevision(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"other-model"}]}`)
	}))
	t.Cleanup(server.Close)
	s, _, _ := setup(t)
	s.modelHTTPClient = server.Client()
	in := profileInput()
	in.ID, in.ProviderType, in.BaseURL = "api", "openai", server.URL+"/v1"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	removed := apiSelectionInput(result, in.ID, "removed", 321)
	staleRevision := apiSelectionInput(result, in.ID, "other-model", 321)
	staleRevision.CatalogRevision = "stale"
	staleTimestamp := apiSelectionInput(result, in.ID, "other-model", 321)
	staleTimestamp.CheckedAt = time.Now().Add(-6 * time.Minute)
	unsupportedEffort := apiSelectionInput(result, in.ID, "other-model", 321)
	unsupportedEffort.ReasoningEffort = "high"
	for _, tc := range []APIModelSelectionInput{removed, staleRevision, staleTimestamp, unsupportedEffort} {
		if _, err := s.prepareAPIModelSelection(t.Context(), tc); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
}

func TestGenericManualRequiresUnsupportedAndConfirmationAndIsNotSDDEligible(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	t.Cleanup(server.Close)
	s, _, _ := setup(t)
	s.modelHTTPClient = server.Client()
	in := profileInput()
	in.ID, in.APIKey, in.BaseURL = "generic", "", server.URL+"/v1"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: in.ID})
	if err != nil || result.Status != modelcatalog.StatusUnsupported || result.ErrorCode != "catalog_unsupported" || result.CredentialToken == "" {
		t.Fatal(result, err)
	}
	choice := apiSelectionInput(result, in.ID, "exact-manual", 0)
	if _, err := s.prepareAPIModelSelection(t.Context(), choice); err == nil {
		t.Fatal("unconfirmed manual selection accepted")
	}
	choice.ConfirmUnverifiedManual = true
	selection, err := s.prepareAPIModelSelection(t.Context(), choice)
	if err != nil || selection.Status != "unverified_manual" {
		t.Fatal(selection, err)
	}
	if err := s.revalidateAPIModelSelection(t.Context(), *selection, false); err != nil {
		t.Fatal(err)
	}
	choice.ForSDD, choice.MaxOutputTokens = true, 321
	if _, err := s.prepareAPIModelSelection(t.Context(), choice); err == nil {
		t.Fatal("generic SDD accepted without tested capability")
	}
}

func TestAPISelectionRevalidationRejectsRemovedModelAndChangedKey(t *testing.T) {
	var removed atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if removed.Load() {
			_, _ = io.WriteString(w, `{"data":[{"id":"replacement"}]}`)
		} else {
			_, _ = io.WriteString(w, `{"data":[{"id":"chosen"}]}`)
		}
	}))
	t.Cleanup(server.Close)
	s, db, vault := setup(t)
	s.modelHTTPClient = server.Client()
	in := profileInput()
	in.ID, in.ProviderType, in.BaseURL = "api", "openai", server.URL+"/v1"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	if result.CredentialToken == "" {
		t.Fatal("catalog omitted credential binding token")
	}
	profile, err := db.GetProviderProfile(t.Context(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	ref := secrets.Reference{Provider: profile.CredentialProvider, Account: profile.CredentialAccount}
	if err := vault.Put(t.Context(), ref, "rotated-before-admission"); err != nil {
		t.Fatal(err)
	}
	rotatedCatalog, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: in.ID})
	if err != nil || rotatedCatalog.CredentialToken == result.CredentialToken {
		t.Fatal("catalog cache retained prior credential binding", err)
	}
	if _, err := s.prepareAPIModelSelection(t.Context(), apiSelectionInput(result, in.ID, "chosen", 321)); err == nil {
		t.Fatal("rotated key admitted from prior catalog choice")
	}
	if err := vault.Put(t.Context(), ref, in.APIKey); err != nil {
		t.Fatal(err)
	}
	selection, err := s.prepareAPIModelSelection(t.Context(), apiSelectionInput(result, in.ID, "chosen", 321))
	if err != nil {
		t.Fatal(err)
	}
	removed.Store(true)
	if err := s.revalidateAPIModelSelection(t.Context(), *selection, true); err == nil {
		t.Fatal("removed model accepted")
	}
	removed.Store(false)
	if err := vault.Put(t.Context(), ref, "rotated-key"); err != nil {
		t.Fatal(err)
	}
	if err := s.revalidateAPIModelSelection(t.Context(), *selection, true); err == nil {
		t.Fatal("rotated credential accepted")
	}
}

func TestOpenRouterUnfilteredRequiresConfirmationAndKeepsStatus(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"chosen"}],"total_count":1}`)
	}))
	t.Cleanup(server.Close)
	s, _, _ := setup(t)
	s.modelHTTPClient = server.Client()
	in := profileInput()
	in.ID, in.ProviderType, in.BaseURL = "router", "openrouter", server.URL+"/v1"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	choice := apiSelectionInput(result, in.ID, "chosen", 321)
	if _, err := s.prepareAPIModelSelection(t.Context(), choice); err == nil {
		t.Fatal("unconfirmed unfiltered catalog accepted")
	}
	choice.ConfirmUnfiltered = true
	selection, err := s.prepareAPIModelSelection(t.Context(), choice)
	if err != nil || selection.Status != "listed_unfiltered" || !selection.ConfirmUnfiltered {
		t.Fatal(selection, err)
	}
}

func TestLMStudioUnloadedModelRequiresSavedJITConfirmation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"models":[{"type":"llm","key":"installed","loaded_instances":[]}]}`)
	}))
	t.Cleanup(server.Close)
	s, _, _ := setup(t)
	s.modelHTTPClient = server.Client()
	in := profileInput()
	in.ID, in.ProviderType, in.BaseURL, in.APIKey = "lm", "lm_studio", server.URL+"/v1", ""
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	choice := apiSelectionInput(result, in.ID, "installed", 321)
	if _, err := s.prepareAPIModelSelection(t.Context(), choice); err == nil {
		t.Fatal("unconfirmed JIT accepted")
	}
	choice.ConfirmJITLoad = true
	selection, err := s.prepareAPIModelSelection(t.Context(), choice)
	if err != nil || !selection.ConfirmJITLoad {
		t.Fatal(selection, err)
	}
	if err := s.revalidateAPIModelSelection(t.Context(), *selection, true); err != nil {
		t.Fatal(err)
	}
}

// A conversation started with a catalog-confirmed model keeps working for as long as it lives. Before every prompt the
// live catalog, the profile and the credential are checked again, but the minutes since the person picked the model do
// not matter: an approval or a follow-up that comes late must not end the conversation.
func TestSessionSelectionKeepsWorkingLongAfterTheCatalogCheck(t *testing.T) {
	var removed atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if removed.Load() {
			_, _ = io.WriteString(w, `{"data":[{"id":"replacement"}]}`)
		} else {
			_, _ = io.WriteString(w, `{"data":[{"id":"chosen"}]}`)
		}
	}))
	t.Cleanup(server.Close)
	s, _, _ := setup(t)
	s.modelHTTPClient = server.Client()
	in := profileInput()
	in.ID, in.ProviderType, in.BaseURL = "api", "openai", server.URL+"/v1"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := s.prepareAPIModelSelection(t.Context(), apiSelectionInput(result, in.ID, "chosen", 321))
	if err != nil {
		t.Fatal(err)
	}
	// The pick was made half an hour ago; the conversation is still going.
	aged := *selection
	aged.CheckedAt = selection.CheckedAt.Add(-30 * time.Minute)
	if err := s.revalidateAPIModelSelection(t.Context(), aged, true); err != nil {
		t.Fatalf("a long-lived conversation lost its model only because time passed: %v", err)
	}
	// What is checked live still ends it: the model gone from the catalog, or a pick that claims to be from the future.
	future := *selection
	future.CheckedAt = time.Now().Add(time.Hour)
	if err := s.revalidateAPIModelSelection(t.Context(), future, true); err == nil {
		t.Fatal("a selection dated in the future was accepted")
	}
	removed.Store(true)
	if err := s.revalidateAPIModelSelection(t.Context(), aged, true); err == nil {
		t.Fatal("a model that left the catalog kept working because the pick was old")
	}
}

// A stage of SDD authoring is not a conversation that already holds its model: it starts from a saved pick, and that
// pick keeps its five-minute life (the confirmations that came with it are as old as it is).
func TestAuthoringStageSelectionStillExpiresFiveMinutesAfterThePick(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"chosen"}]}`)
	}))
	t.Cleanup(server.Close)
	s, _, _ := setup(t)
	s.modelHTTPClient = server.Client()
	in := profileInput()
	in.ID, in.ProviderType, in.BaseURL = "api", "openai", server.URL+"/v1"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := s.prepareAPIModelSelection(t.Context(), apiSelectionInput(result, in.ID, "chosen", 321))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.revalidatedAuthoringModelSelection(t.Context(), *selection, authoringStageConsent{}); err != nil {
		t.Fatalf("a fresh stage pick was refused: %v", err)
	}
	aged := *selection
	aged.CheckedAt = selection.CheckedAt.Add(-6 * time.Minute)
	if _, err := s.revalidatedAuthoringModelSelection(t.Context(), aged, authoringStageConsent{}); !errors.Is(err, ErrBackendChanged) {
		t.Fatalf("a stage pick older than five minutes still ran: %v", err)
	}
	// The same pick, held by a conversation, keeps working: only the stage has the five-minute life.
	if err := s.revalidateAPIModelSelection(t.Context(), aged, true); err != nil {
		t.Fatalf("a conversation lost its model only because time passed: %v", err)
	}
}

func TestQALoopConfirmsAnOldModelPickAgainBeforeEachRound(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"chosen-model"}]}`)
	}))
	t.Cleanup(server.Close)
	s, _, _ := setup(t)
	s.modelHTTPClient = server.Client()
	profile := profileInput()
	profile.ID, profile.ProviderType, profile.BaseURL = "api-work", "openai", server.URL+"/v1"
	if _, err := s.SaveProviderProfile(profile); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "TODO", Objective: "Criar TODO"})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: profile.ID})
	if err != nil {
		t.Fatal(err)
	}
	// The pick the person made when the loop started, checked longer ago than a pick stays valid.
	old := apiSelectionInput(catalog, profile.ID, "chosen-model", 0)
	old.Executor, old.CheckedAt = "api", old.CheckedAt.Add(-10*time.Minute)
	if _, err := s.preparePipelineRoleModelSelection(t.Context(), workspace.ID, profile.ID, old); err == nil {
		t.Fatal("an old pick was admitted as it was")
	}
	fresh := s.freshRoleChoice(run.ID, PipelineRoleChoice{BackendID: profile.ID, Selection: &old})
	if fresh.BackendID != profile.ID || fresh.Selection.ModelID != "chosen-model" {
		t.Fatalf("the executor or the model changed: %+v", fresh)
	}
	if _, err := s.preparePipelineRoleModelSelection(t.Context(), workspace.ID, profile.ID, *fresh.Selection); err != nil {
		t.Fatalf("the confirmed pick was refused: %v", err)
	}
}
