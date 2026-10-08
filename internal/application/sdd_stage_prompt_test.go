package application

import (
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/sdd"
)

func TestAuthoringStagePromptReservationIncludesCodexCLIContext(t *testing.T) {
	input := catalog.AuthoringStageInput{DiscoveryVersion: 1, DiscoveryContent: "Discovery", DiscoveryBypassReason: "Discovery is sufficient"}
	run := catalog.AuthoringStageRun{Stage: sdd.Spec, DiscoveryVersion: 1, InputBudgetRemaining: 100000, OutputBudgetRemaining: 8192}
	choice := catalog.ModelSelection{BackendID: "codex", Source: "codex_app_server", ModelID: "gpt-6-sol", MaxOutputTokens: 4096}
	prompt, err := prepareAuthoringStagePrompt(run, input, choice, "openai")
	if err != nil {
		t.Fatal(err)
	}
	size, err := openai.RequestSize(agentcore.ChatRequest{Model: choice.ModelID, Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: prompt.Text}}, MaxOutputTokens: choice.MaxOutputTokens}, "openai")
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(size) + 1024 + 24576; prompt.EstimatedInputTokens != want || prompt.EstimatedInputTokens < 18903 {
		t.Fatalf("Codex CLI input reservation = %d, want %d (observed live usage: 18903)", prompt.EstimatedInputTokens, want)
	}
}
