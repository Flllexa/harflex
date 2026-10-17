package application

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
)

const specAnswerForTest = `{"document":"# SPEC\n\n## Requisitos\nListar contratos que vencem no mês.","reply":"Preparei a SPEC."}`

func brainstormAnswerForTest() string {
	screen := `{"name":"Contratos","purpose":"Ver o que vence","html":` + quoteJSON(brainstormPage) + `}`
	return `{"brainstorm":{"flow":"flowchart TD\n  A[Abrir contratos] --> B[Filtrar por mês]","design":{"screens":[` + screen + `]},"noDesignReason":"","notes":"## Decisões"},"reply":"Desenhei o fluxo e a tela."}`
}

// scriptDesignAnswers answers each document call, in order, with the next reply.
func scriptDesignAnswers(t *testing.T, s *Service, replies ...string) *[]*brainstormTestProvider {
	t.Helper()
	providers := []*brainstormTestProvider{}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) {
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

func lastUserPrompt(provider *brainstormTestProvider) string {
	messages := provider.requests[len(provider.requests)-1].Messages
	return messages[len(messages)-1].Content
}

func TestBrainstormPhaseWritesCanonicalContent(t *testing.T) {
	s, db, pipeline := pipelineDesignServiceFixture(t)
	workspace, err := db.GetWorkspace(t.Context(), pipeline.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.Path, "DESIGN.md"), []byte("# Identidade\n\nCor primária verde-floresta #1F6F4A."), 0o600); err != nil {
		t.Fatal(err)
	}
	providers := scriptDesignAnswers(t, s, specAnswerForTest, brainstormAnswerForTest())
	design, err := s.OpenPipelineDesign(pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PreparePipelineDesign(PreparePipelineDesignInput{Ref: designRefForTest(design, "prepare_brainstorm_01"), Target: "all", Message: "Prepare a SPEC e o Brainstorm."})
	if err != nil || prepared.State != "ready" || len(*providers) != 2 {
		t.Fatalf("preparation = %+v, %v", prepared, err)
	}
	want, _, err := parseBrainstormOutput(brainstormAnswerForTest())
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Documents["brainstorm"].Content != want.Canonical() || prepared.Documents["plan"].Content != "" || prepared.Documents["spec"].Content == "" {
		t.Fatalf("documents = spec %q / brainstorm %q / plan %q", prepared.Documents["spec"].Content, prepared.Documents["brainstorm"].Content, prepared.Documents["plan"].Content)
	}
	brainstorm := (*providers)[1]
	request := brainstorm.requests[0]
	prompt := lastUserPrompt(brainstorm)
	if !strings.Contains(request.Messages[0].Content, "Guia de design do Harflex") || !strings.Contains(prompt, "Listar contratos que vencem no mês.") || !strings.Contains(prompt, "verde-floresta #1F6F4A") {
		t.Fatalf("Brainstorm prompt misses the guide, the SPEC or DESIGN.md:\nsystem: %.200s\nprompt: %.400s", request.Messages[0].Content, prompt)
	}
	// No answer limit is asked: the provider gives the model's own maximum.
	if request.MaxOutputTokens != 0 {
		t.Fatalf("Brainstorm output budget = %d, want no limit (0)", request.MaxOutputTokens)
	}
	if strings.Contains((*providers)[0].requests[0].Messages[0].Content, "Guia de design do Harflex") {
		t.Fatal("SPEC must keep its own system prompt")
	}
}

func TestBrainstormTruncatedOutputIsRetriedNotSaved(t *testing.T) {
	s, _, pipeline := pipelineDesignServiceFixture(t)
	answer := brainstormAnswerForTest()
	providers := scriptDesignAnswers(t, s, specAnswerForTest, answer[:len(answer)/2], answer)
	design, err := s.OpenPipelineDesign(pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PreparePipelineDesign(PreparePipelineDesignInput{Ref: designRefForTest(design, "prepare_brainstorm_retry_01"), Target: "all", Message: "Prepare a SPEC e o Brainstorm."})
	if err != nil || prepared.State != "ready" || len(*providers) != 3 {
		t.Fatalf("preparation = %+v, %v (calls %d)", prepared, err, len(*providers))
	}
	retry := lastUserPrompt((*providers)[2])
	if !strings.Contains(retry, "not the expected JSON") {
		t.Fatalf("the retry must tell the AI why its answer was refused: %.400s", retry)
	}
	want, _, _ := parseBrainstormOutput(answer)
	document := prepared.Documents["brainstorm"]
	if document.Content != want.Canonical() || document.Version != 1 {
		t.Fatalf("Brainstorm must be saved once, from the corrected answer: %+v", document)
	}
	if prepared.Attempts[0].SessionIDs["brainstorm"] != document.SourceSessionID || document.SourceSessionID == "" {
		t.Fatalf("the document must come from the corrected session: %+v vs %+v", document.SourceSessionID, prepared.Attempts[0].SessionIDs)
	}
}

func TestBrainstormSecondInvalidAnswerPausesAttempt(t *testing.T) {
	s, _, pipeline := pipelineDesignServiceFixture(t)
	providers := scriptDesignAnswers(t, s, specAnswerForTest, `{"brainstorm":{"flow":"`, `não é JSON`)
	design, err := s.OpenPipelineDesign(pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := s.PreparePipelineDesign(PreparePipelineDesignInput{Ref: designRefForTest(design, "prepare_brainstorm_fail_01"), Target: "all", Message: "Prepare a SPEC e o Brainstorm."})
	if err != nil || len(*providers) != 3 {
		t.Fatalf("preparation = %+v, %v (calls %d)", prepared, err, len(*providers))
	}
	if prepared.Attempts[0].Status != "failed" || prepared.Attempts[0].ErrorCode != "pipeline_design_invalid_response" {
		t.Fatalf("two invalid answers must fail the attempt: %+v", prepared.Attempts[0])
	}
	if prepared.Documents["spec"].Content != "" || prepared.Documents["brainstorm"].Content != "" {
		t.Fatalf("a failed preparation must leave the documents untouched: %+v", prepared.Documents)
	}
}

func TestBrainstormRefusesSmallModel(t *testing.T) {
	s, db, _ := setup(t)
	s.modelHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"selected-model","context_length":16000}],"total_count":1}`))}, nil
	})}
	profile := profileInput()
	profile.ID, profile.ProviderType, profile.BaseURL, profile.Model = "router", "openrouter", "https://router.synthetic.invalid/api/v1", "selected-model"
	if _, err := s.SaveProviderProfile(profile); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "create_small_model_01", Discovery: "Listar contratos que vencem no mês."})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: profile.ID})
	if err != nil {
		t.Fatal(err)
	}
	choice := apiSelectionInput(result, profile.ID, "selected-model", 0)
	choice.ConfirmUnfiltered = true
	providers := scriptDesignAnswers(t, s)
	design, err := s.OpenPipelineDesign(pipeline.ID)
	if err != nil {
		t.Fatal(err)
	}
	var before int
	if err := db.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sessions`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	_, err = s.PreparePipelineDesign(PreparePipelineDesignInput{Ref: designRefForTest(design, "prepare_brainstorm_small_01"), Target: "all", Message: "Prepare a SPEC e o Brainstorm.",
		Selections: map[string]APIModelSelectionInput{"spec": choice, "brainstorm": choice}})
	if !errors.Is(err, ErrBrainstormModelTooSmall) || ErrorCode(err) != "brainstorm_model_too_small" {
		t.Fatalf("err = %v (code %q), want ErrBrainstormModelTooSmall", err, ErrorCode(err))
	}
	var after int
	if err := db.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sessions`).Scan(&after); err != nil || after != before || len(*providers) != 0 {
		t.Fatalf("a refused preparation must not start sessions: %d → %d, calls %d", before, after, len(*providers))
	}
	reopened, err := s.OpenPipelineDesign(pipeline.ID)
	if err != nil || len(reopened.Attempts) != 0 {
		t.Fatalf("a refused preparation must not leave an attempt: %+v %v", reopened.Attempts, err)
	}
}

func TestBrainstormSelectionFits(t *testing.T) {
	for _, tc := range []struct {
		name   string
		choice catalog.ModelSelection
		fits   bool
	}{
		{"unknown context", catalog.ModelSelection{MaxOutputTokens: brainstormOutputTokens}, true},
		{"large context", catalog.ModelSelection{MaxOutputTokens: brainstormOutputTokens, ContextLength: 128000}, true},
		{"small context", catalog.ModelSelection{MaxOutputTokens: brainstormOutputTokens, ContextLength: 32000}, false},
		{"small output budget", catalog.ModelSelection{MaxOutputTokens: 4096}, false},
	} {
		if err := brainstormSelectionFits(tc.choice); (err == nil) != tc.fits {
			t.Fatalf("%s: brainstormSelectionFits = %v, want fits=%v", tc.name, err, tc.fits)
		}
	}
}

func TestBrainstormPromptFitsCountsTokensNotBytes(t *testing.T) {
	prompt := strings.Repeat("<div>tela</div>", 100*1024/15)
	large := catalog.ModelSelection{MaxOutputTokens: brainstormOutputTokens, ContextLength: 128000}
	if !brainstormPromptFits(large, brainstormSystemPrompt, prompt) {
		t.Fatal("a 100 KB prompt fits a 128k-token context with room for the answer")
	}
	small := catalog.ModelSelection{MaxOutputTokens: brainstormOutputTokens, ContextLength: 49152}
	if brainstormPromptFits(small, brainstormSystemPrompt, prompt) {
		t.Fatal("a 100 KB prompt and a 32k-token answer do not fit a 49k-token context")
	}
	if !brainstormPromptFits(catalog.ModelSelection{MaxOutputTokens: brainstormOutputTokens}, brainstormSystemPrompt, prompt) {
		t.Fatal("an unknown context length is not a reason to refuse")
	}
}
