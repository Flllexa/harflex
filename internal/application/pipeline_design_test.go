package application

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/externalagent"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func pipelineDesignServiceFixture(t *testing.T) (*Service, *sqlite.Store, PipelineDTO) {
	t.Helper()
	s, db, _ := setup(t)
	profile := profileInput()
	profile.ID, profile.ProviderType, profile.BaseURL, profile.Model = "design-api", "openai", "https://design.synthetic.invalid/v1", "selected-model"
	if _, err := s.SaveProviderProfile(profile); err != nil {
		t.Fatal(err)
	}
	s.modelHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || !strings.HasSuffix(request.URL.Path, "/models") {
			t.Fatalf("unexpected catalog request: %s %s", request.Method, request.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"selected-model","context_length":64000}]}`))}, nil
	})}
	if err := db.SaveSettings(t.Context(), catalog.AppSettings{DefaultBackendID: profile.ID, DefaultModelBackendID: profile.ID, DefaultModelID: profile.Model}); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "create_design_pipeline_01", Discovery: "Criar um TODO em HTML com persistência em localStorage."})
	if err != nil {
		t.Fatal(err)
	}
	return s, db, pipeline
}

func designRefForTest(design PipelineDesignDTO, requestID string) PipelineDesignRefInput {
	return PipelineDesignRefInput{PipelineID: design.PipelineID, RequestID: requestID, PipelineRevision: design.PipelineRevision, DesignRevision: design.Revision}
}

func installDesignAnswers(t *testing.T, s *Service, db *sqlite.Store, pipelineID string) *[]*brainstormTestProvider {
	t.Helper()
	providers := []*brainstormTestProvider{}
	replies := []string{`{"document":"# SPEC\n\n## Requisitos\nAdicionar, concluir e remover tarefas.\n\n## Critérios de aceite\nAs tarefas persistem em localStorage.","reply":"Preparei a SPEC com persistência local."}`, `{"document":"# Plan\n\n1. Criar o HTML.\n2. Implementar tarefas e filtros.\n3. Verificar persistência em localStorage.","reply":"O plano está pronto para revisão."}`}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) {
		stored, err := db.GetPipelineDesign(t.Context(), pipelineID)
		if err != nil || len(stored.Attempts) != 1 || len(stored.Attempts[0].SessionIDs) == 0 {
			t.Fatalf("model was composed before its durable link: %+v %v", stored, err)
		}
		index := len(providers)
		if index >= len(replies) {
			t.Fatal("unexpected additional model inference")
		}
		provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "text_delta", Delta: replies[index]}}}
		providers = append(providers, provider)
		return provider, nil
	}
	return &providers
}

func TestPipelineDesignPreparesSpecAndPlanUsingDefaultWithoutIntermediateApprovals(t *testing.T) {
	s, db, pipeline := pipelineDesignServiceFixture(t)
	providers := installDesignAnswers(t, s, db, pipeline.ID)
	design, err := s.OpenPipelineDesign(pipeline.ID)
	if err != nil || design.Documents["discovery"].Content != pipeline.Artifacts["discovery"].Content || len(*providers) != 0 {
		t.Fatalf("opening draft workspace must not infer: %+v %v", design, err)
	}
	input := PreparePipelineDesignInput{Ref: designRefForTest(design, "prepare_design_default_01"), Target: "all", Message: "Prepare a SPEC e o Plan para o Discovery salvo."}
	prepared, err := s.PreparePipelineDesign(input)
	if err != nil || prepared.State != "ready" || prepared.Documents["spec"].Content == "" || prepared.Documents["plan"].Content == "" || len(*providers) != 2 {
		t.Fatalf("initial preparation did not generate both documents: %+v %v", prepared, err)
	}
	for _, provider := range *providers {
		if provider.calls.Load() != 1 || len(provider.requests) != 1 || len(provider.requests[0].Tools) != 0 || provider.requests[0].Model != "selected-model" {
			t.Fatalf("document generation ignored default or exposed tools: %+v", provider.requests)
		}
	}
	current, err := s.GetPipeline(pipeline.ID)
	if err != nil || current.CurrentStage != "discovery" || current.StageStatus["spec"] == "completed" {
		t.Fatalf("draft generation silently approved pipeline phases: %+v %v", current, err)
	}
	s.modelHTTPClient = &http.Client{Transport: offlineBrainstormCatalog{t}}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) {
		t.Fatal("durable replay performed inference")
		return nil, nil
	}
	replayed, err := s.PreparePipelineDesign(input)
	if err != nil || !reflect.DeepEqual(prepared, replayed) {
		t.Fatalf("retry did not return the durable result: %+v %v", replayed, err)
	}
}

func TestPipelineDesignManualChangesInvalidateDependenciesAndPublishExactApprovedText(t *testing.T) {
	s, db, pipeline := pipelineDesignServiceFixture(t)
	installDesignAnswers(t, s, db, pipeline.ID)
	design, err := s.OpenPipelineDesign(pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	design, err = s.PreparePipelineDesign(PreparePipelineDesignInput{Ref: designRefForTest(design, "prepare_design_manual_01"), Target: "all", Message: "Preparar documentos"})
	if err != nil {
		t.Fatal(err)
	}
	const revisedSpec = "# SPEC revisada pelo usuário\n\nPermitir também reativar tarefas."
	design, err = s.EditPipelineDesignDocument(EditPipelineDesignDocumentInput{Ref: designRefForTest(design, "edit_design_spec_user_01"), Stage: "spec", Content: revisedSpec})
	if err != nil || !design.Documents["plan"].Stale || design.Documents["spec"].Author != "user" {
		t.Fatalf("manual SPEC did not invalidate Plan: %+v %v", design, err)
	}
	digests := map[string]string{}
	for stage, document := range design.Documents {
		digests[stage] = document.ContentDigest
	}
	if _, err := s.ApprovePipelineDesign(ApprovePipelineDesignInput{Ref: designRefForTest(design, "approve_stale_design_01"), Digests: digests}); err == nil {
		t.Fatal("stale Plan was approved")
	}
	const revisedPlan = "# Plan revisado\n\nImplementar e testar conclusão e reativação."
	design, err = s.EditPipelineDesignDocument(EditPipelineDesignDocumentInput{Ref: designRefForTest(design, "edit_design_plan_user_01"), Stage: "plan", Content: revisedPlan})
	if err != nil {
		t.Fatal(err)
	}
	for stage, document := range design.Documents {
		digests[stage] = document.ContentDigest
	}
	approved, err := s.ApprovePipelineDesign(ApprovePipelineDesignInput{Ref: designRefForTest(design, "approve_current_design_01"), Digests: digests})
	if err != nil || approved.CurrentStage != "code" || approved.Artifacts["spec"].Content != revisedSpec || approved.Artifacts["plan"].Content != revisedPlan || approved.Artifacts["spec"].Author != "user" {
		t.Fatalf("approved content was not published exactly: %+v %v", approved, err)
	}
	if _, err := s.EditPipelineDesignDocument(EditPipelineDesignDocumentInput{Ref: designRefForTest(design, "edit_after_code_design_01"), Stage: "discovery", Content: "Novo Discovery"}); err == nil {
		t.Fatal("approved Code sources were rewritten instead of requiring derivation")
	}
}

type blockingDesignProvider struct {
	entered chan struct{}
	once    sync.Once
}

func (*blockingDesignProvider) ID() string { return "design-blocking" }
func (*blockingDesignProvider) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true}
}
func (p *blockingDesignProvider) Stream(ctx context.Context, _ agentcore.ChatRequest) (<-chan agentcore.StreamEvent, <-chan error) {
	p.once.Do(func() { close(p.entered) })
	events, errs := make(chan agentcore.StreamEvent), make(chan error, 1)
	go func() { defer close(events); defer close(errs); <-ctx.Done(); errs <- ctx.Err() }()
	return events, errs
}

func TestPipelineDesignCancellationKeepsExistingDocuments(t *testing.T) {
	s, _, pipeline := pipelineDesignServiceFixture(t)
	provider := &blockingDesignProvider{entered: make(chan struct{})}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	design, err := s.OpenPipelineDesign(pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.PreparePipelineDesign(PreparePipelineDesignInput{Ref: designRefForTest(design, "prepare_cancel_design_01"), Target: "all", Message: "Preparar documentos"})
		done <- err
	}()
	select {
	case <-provider.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("document model did not start")
	}
	running, err := s.OpenPipelineDesign(pipeline.ID)
	if err != nil || running.ActiveAttemptID == "" {
		t.Fatalf("missing running attempt: %+v %v", running, err)
	}
	cancelled, err := s.CancelPipelineDesign(CancelPipelineDesignInput{PipelineID: pipeline.ID, AttemptID: running.ActiveAttemptID})
	if err != nil || cancelled.State == "running" || cancelled.Documents["discovery"].Content != design.Documents["discovery"].Content || cancelled.Documents["spec"].Content != "" {
		t.Fatalf("cancellation lost drafts or remained running: %+v %v", cancelled, err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled document owner did not join")
	}
}

func TestPipelineDesignCancellationStopsItsOwnerAfterPipelineDrift(t *testing.T) {
	s, db, pipeline := pipelineDesignServiceFixture(t)
	provider := &blockingDesignProvider{entered: make(chan struct{})}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	design, err := s.OpenPipelineDesign(pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.PreparePipelineDesign(PreparePipelineDesignInput{Ref: designRefForTest(design, "prepare_cancel_drift_01"), Target: "all", Message: "Preparar documentos"})
		done <- err
	}()
	select {
	case <-provider.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not start")
	}
	running, err := s.OpenPipelineDesign(pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(t.Context(), `UPDATE pipeline_runs SET revision=revision+1 WHERE id=?`, pipeline.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelPipelineDesign(CancelPipelineDesignInput{PipelineID: pipeline.ID, AttemptID: running.ActiveAttemptID}); err == nil {
		t.Fatal("stale cancellation hid pipeline conflict")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stale pipeline left its provider running after cancellation")
	}
	readback, err := s.OpenPipelineDesign(pipeline.ID)
	if err != nil || readback.ActiveAttemptID != "" || readback.Documents["spec"].Content != "" {
		t.Fatalf("stale owner was not settled: %+v %v", readback, err)
	}
}

func TestPipelineDesignRejectsUnexpectedModelToolCall(t *testing.T) {
	s, _, pipeline := pipelineDesignServiceFixture(t)
	provider := &brainstormTestProvider{events: []agentcore.StreamEvent{{Type: "tool_call", ToolCall: &agentcore.ToolCall{ID: "unexpected", Name: "read", Arguments: json.RawMessage(`{"path":"outside.txt"}`)}}}}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	design, err := s.OpenPipelineDesign(pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.PreparePipelineDesign(PreparePipelineDesignInput{Ref: designRefForTest(design, "prepare_tool_design_01"), Target: "all", Message: "Preparar documentos"})
	readback, err := s.OpenPipelineDesign(pipeline.ID)
	if err != nil || readback.Documents["spec"].Content != "" || readback.State == "running" || len(provider.requests) != 1 || len(provider.requests[0].Tools) != 0 {
		t.Fatalf("unexpected tool published document output: %+v %v", readback, err)
	}
}

func TestPipelineDesignReopensOnlyAnAttemptWhoseOwnerProcessIsProvenDead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process liveness cannot be proved by this Windows runner")
	}
	child := exec.Command("true")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	deadPID := child.Process.Pid
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name  string
		pid   int
		state string
	}{{"dead owner", deadPID, "paused"}, {"live owner", os.Getpid(), "running"}} {
		t.Run(scenario.name, func(t *testing.T) {
			s, db, pipeline := pipelineDesignServiceFixture(t)
			design, err := s.OpenPipelineDesign(pipeline.ID)
			if err != nil {
				t.Fatal(err)
			}
			selection, err := s.preparePipelineDesignSelection(t.Context(), design.WorkspaceID, sdd.Spec, nil)
			if err != nil {
				t.Fatal(err)
			}
			request := catalog.PipelineDesignAttemptRequest{Ref: designRefForTest(design, "prepare_owner_proof_01").request(), Target: "all", Message: "Preparar documentos", OwnerPID: scenario.pid, Selections: map[sdd.Stage]catalog.ModelSelection{sdd.Spec: selection, sdd.Plan: selection}}
			request.IntentHash, err = request.Hash()
			if err != nil {
				t.Fatal(err)
			}
			_, _, admitted, err := db.BeginPipelineDesignAttempt(t.Context(), request)
			if err != nil || !admitted {
				t.Fatalf("admit owner fixture: %v", err)
			}
			readback, err := s.OpenPipelineDesign(pipeline.ID)
			if err != nil || readback.State != scenario.state {
				t.Fatalf("owner proof reopened state=%q, want %q: %v", readback.State, scenario.state, err)
			}
			if scenario.state == "paused" && readback.Attempts[0].Status != "interrupted" {
				t.Fatalf("dead owner did not retain interrupted evidence: %+v", readback.Attempts)
			}
		})
	}
}

type designCLIStub struct {
	cliCatalogStub
	requests []externalagent.DocumentRequest
}

func (*designCLIStub) DocumentContractSupported(externalagent.Detection) bool { return true }

func (stub *designCLIStub) GenerateDocument(_ context.Context, request externalagent.DocumentRequest, emit func(externalagent.Event)) (externalagent.DocumentResult, error) {
	stub.requests = append(stub.requests, request)
	content := `{"document":"# SPEC\n\nAs tarefas devem persistir em localStorage.","reply":"Preparei a SPEC."}`
	if len(stub.requests) == 2 {
		content = `{"document":"# Plan\n\nImplementar HTML, tarefas e persistência.","reply":"Preparei o plano."}`
	}
	if emit != nil {
		emit(externalagent.Event{Type: "text_delta", Text: content})
	}
	return externalagent.DocumentResult{Text: content, ThreadID: "synthetic-document-thread", UsageKnown: true}, nil
}

func TestPipelineDesignUsesDefaultCLIWithoutAPIProfilesOrWorkspaceTools(t *testing.T) {
	for _, backendID := range []string{"codex", "claude"} {
		t.Run(backendID, func(t *testing.T) { pipelineDesignUsesDefaultCLI(t, backendID) })
	}
}

func pipelineDesignUsesDefaultCLI(t *testing.T, backendID string) {
	s, db, _ := setup(t)
	stub := &designCLIStub{cliCatalogStub: cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: backendID, version: "0.157.0", efforts: []string{"low"}}}
	s.external[backendID] = stub
	if err := db.SaveSettings(t.Context(), catalog.AppSettings{DefaultBackendID: backendID, DefaultModelBackendID: backendID, DefaultModelID: "provider/model-exact"}); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "create_codex_design_01", Discovery: "Um TODO com localStorage."})
	if err != nil {
		t.Fatal(err)
	}
	design, err := s.OpenPipelineDesign(pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PreparePipelineDesign(PreparePipelineDesignInput{Ref: designRefForTest(design, "prepare_codex_design_01"), Target: "all", Message: "Preparar SPEC e Plan"})
	if err != nil || prepared.State != "ready" || len(stub.requests) != 2 || prepared.Documents["spec"].Selection == nil || prepared.Documents["spec"].Selection.Executor != "codex_cli" || prepared.Documents["spec"].Selection.BackendID != backendID {
		t.Fatalf("default Codex document preparation failed: state=%s calls=%d attempts=%+v err=%v", prepared.State, len(stub.requests), prepared.Attempts, err)
	}
	for _, request := range stub.requests {
		if request.CWD == workspace.Path || request.Model != "provider/model-exact" || request.ExpectedExecutableVersion != "0.157.0" || request.MaxAssistantOutputBytes != catalog.MaxPipelineDesignDocumentBytes || len(request.OutputSchema) == 0 || !strings.Contains(request.SystemPrompt, "sem ferramentas") {
			t.Fatalf("Codex document boundary changed: %+v", request)
		}
	}
}

func TestPipelineDesignDerivationRetainsDocumentsAndTheirUpstreamSource(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "same discovery", true: "revised discovery"}[changed], func(t *testing.T) {
			s, db, pipeline := pipelineDesignServiceFixture(t)
			installDesignAnswers(t, s, db, pipeline.ID)
			design, err := s.OpenPipelineDesign(pipeline.ID)
			if err != nil {
				t.Fatal(err)
			}
			design, err = s.PreparePipelineDesign(PreparePipelineDesignInput{Ref: designRefForTest(design, "prepare_derived_documents_01"), Target: "all", Message: "Preparar documentos"})
			if err != nil {
				t.Fatal(err)
			}
			digests := map[string]string{}
			for stage, document := range design.Documents {
				digests[stage] = document.ContentDigest
			}
			approved, err := s.ApprovePipelineDesign(ApprovePipelineDesignInput{Ref: designRefForTest(design, "approve_derived_documents_01"), Digests: digests})
			if err != nil {
				t.Fatal(err)
			}
			parentBefore, err := s.OpenPipelineDesign(pipeline.ID)
			if err != nil {
				t.Fatal(err)
			}
			discovery := design.Documents["discovery"].Content
			if changed {
				discovery += "\nUsar tema azul e filtros."
			}
			child, err := s.DeriveAuthoringPipeline(DeriveAuthoringPipelineInput{ParentPipelineID: pipeline.ID, RequestID: "derive_document_revision_01", ExpectedRevision: approved.Revision, Discovery: discovery})
			if err != nil {
				t.Fatal(err)
			}
			derived, err := s.OpenPipelineDesign(child.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, stage := range []string{"spec", "plan"} {
				before, after := design.Documents[stage], derived.Documents[stage]
				if after.Content != before.Content || after.SourceSessionID != before.SourceSessionID || after.SourceDigest != before.SourceDigest || after.Stale != changed {
					t.Fatalf("derived %s lost content or provenance: before=%+v after=%+v", stage, before, after)
				}
			}
			parentAfter, err := s.OpenPipelineDesign(pipeline.ID)
			if err != nil || !reflect.DeepEqual(parentBefore, parentAfter) {
				t.Fatalf("derivation changed approved parent: %v", err)
			}
		})
	}
}

func TestPipelineDesignPromptRequestsOnlyItsTargetDocument(t *testing.T) {
	documents := map[sdd.Stage]catalog.PipelineDesignDocument{sdd.Discovery: {Content: "TODO em tema azul"}, sdd.Spec: {Content: "# SPEC salva"}, sdd.Plan: {Content: "# Plan salvo"}}
	for _, stage := range designStages() {
		prompt, err := pipelineDesignPrompt(stage, "Prepare a SPEC e o Plan para o Discovery salvo.", documents, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		var payload struct {
			Target         string
			OutputDocument string
			Documents      map[string]string
		}
		if err := json.Unmarshal([]byte(prompt), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Target != string(stage) || !strings.Contains(payload.OutputDocument, "somente "+string(stage)) || payload.Documents["spec"] != "# SPEC salva" {
			t.Fatalf("ambiguous phase prompt: %s", prompt)
		}
	}
	if !strings.Contains(designSystemPrompt, "um único documento") {
		t.Fatal("system prompt permits combined documents")
	}
}

func TestPipelineDesignPlanCannotUseStaleSpecAfterDiscoveryChanges(t *testing.T) {
	s, db, pipeline := pipelineDesignServiceFixture(t)
	providers := installDesignAnswers(t, s, db, pipeline.ID)
	design, err := s.OpenPipelineDesign(pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	design, err = s.PreparePipelineDesign(PreparePipelineDesignInput{Ref: designRefForTest(design, "prepare_stale_source_01"), Target: "all", Message: "Preparar documentos"})
	if err != nil {
		t.Fatal(err)
	}
	design, err = s.EditPipelineDesignDocument(EditPipelineDesignDocumentInput{Ref: designRefForTest(design, "revise_stale_discovery_01"), Stage: "discovery", Content: "Novo Discovery com filtros"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PreparePipelineDesign(PreparePipelineDesignInput{Ref: designRefForTest(design, "prepare_stale_plan_01"), Target: "plan", Message: "Atualizar o plano"}); !errors.Is(err, ErrPipelineDesignSpecStale) {
		t.Fatalf("stale SPEC admitted Plan: %v", err)
	}
	readback, err := s.OpenPipelineDesign(pipeline.ID)
	if err != nil || !readback.Documents["plan"].Stale || len(*providers) != 2 {
		t.Fatalf("stale plan was refreshed or invoked provider: %+v %v", readback, err)
	}
}
