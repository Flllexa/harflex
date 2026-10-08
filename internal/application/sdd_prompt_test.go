package application

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
)

func TestSDDPromptIncludesOnlyInspectedRejectionFeedback(t *testing.T) {
	run := catalog.BrainstormRun{DiscoveryVersion: 2, DiscoveryContent: "Discovery", InputBudgetRemaining: 100000, OutputBudgetRemaining: 4096,
		Syntheses:    []catalog.BrainstormSynthesis{{Version: 1, DiscoveryVersion: 2, Status: "rejected", Content: catalog.BrainstormSynthesisContent{Scope: "INSPECTED_SCOPE", Decisions: []string{"Decision"}}}, {Version: 2, DiscoveryVersion: 1, Status: "rejected", Content: catalog.BrainstormSynthesisContent{Scope: "OTHER_DISCOVERY"}}},
		HumanActions: []catalog.BrainstormHumanAction{{Kind: "revision", Actor: "local_user", SynthesisVersion: 1, Feedback: "HUMAN_FEEDBACK", Choice: "new_synthesis"}, {Kind: "revision", Actor: "local_user", SynthesisVersion: 2, Feedback: "OTHER_FEEDBACK"}}}
	for _, kind := range []string{"question", "synthesis"} {
		t.Run(kind, func(t *testing.T) {
			choice := catalog.ModelSelection{ModelID: "chosen", MaxOutputTokens: 256}
			if kind == "synthesis" {
				choice.MaxOutputTokens = 1024
			}
			got, err := prepareSDDBrainstormPrompt(run, kind, choice, "openai")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(got.Text, "INSPECTED_SCOPE") || !strings.Contains(got.Text, "HUMAN_FEEDBACK") {
				t.Fatal("revision omitted")
			}
			if strings.Contains(got.Text, "OTHER_DISCOVERY") || strings.Contains(got.Text, "OTHER_FEEDBACK") {
				t.Fatal("foreign Discovery leaked")
			}
			limited := run
			limited.InputBudgetRemaining = got.EstimatedInputTokens - 1
			if _, err := prepareSDDBrainstormPrompt(limited, kind, choice, "openai"); !errors.Is(err, errSDDPromptTooLarge) {
				t.Fatal("feedback outside budget")
			}
		})
	}
}

func TestSDDPromptContainsOnlyDiscoveryAndAnsweredTurns(t *testing.T) {
	run := catalog.BrainstormRun{DiscoveryContent: "Discovery café 🧭", InputBudgetRemaining: 100000, OutputBudgetRemaining: 4096,
		Selection: catalog.ModelSelection{WorkspacePath: "/secret/path", CredentialIdentity: "secret-fingerprint"},
		Turns: []catalog.BrainstormTurn{
			{Question: "What is needed?", Answer: "Confirmed scope", Status: "answered"},
			{Question: "PENDING_UNCONFIRMED", Status: "waiting_answer"},
			{Question: "SKIPPED_UNCONFIRMED", Answer: "not confirmed", Status: "skipped"},
		},
	}
	for _, kind := range []string{"question", "synthesis"} {
		t.Run(kind, func(t *testing.T) {
			choice := catalog.ModelSelection{ModelID: "chosen", MaxOutputTokens: 256}
			if kind == "synthesis" {
				choice.MaxOutputTokens = 1024
			}
			got, err := prepareSDDBrainstormPrompt(run, kind, choice, "openai")
			if err != nil {
				t.Fatal(err)
			}
			again, err := prepareSDDBrainstormPrompt(run, kind, choice, "openai")
			if err != nil || !reflect.DeepEqual(got, again) {
				t.Fatal("prompt is nondeterministic", err)
			}
			for _, denied := range []string{"PENDING_UNCONFIRMED", "SKIPPED_UNCONFIRMED", "/secret/path", "secret-fingerprint"} {
				if strings.Contains(got.Text, denied) {
					t.Fatalf("prompt leaked %q", denied)
				}
			}
			for _, required := range []string{"Discovery café 🧭", "What is needed?", "Confirmed scope"} {
				if !strings.Contains(got.Text, required) {
					t.Fatalf("prompt missing %q", required)
				}
			}
			request := agentcore.ChatRequest{Model: "chosen", Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: got.Text}}, MaxOutputTokens: choice.MaxOutputTokens}
			size, err := openai.RequestSize(request, "openai")
			if err != nil || got.SerializedBytes != size || got.EstimatedInputTokens != int64(size)+1024 || got.ContextLengthKnown {
				t.Fatalf("estimate=%+v size=%d err=%v", got, size, err)
			}
		})
	}
}

func TestSDDPromptRejectsOversizeWithoutTruncation(t *testing.T) {
	base := catalog.BrainstormRun{DiscoveryContent: "Discovery", InputBudgetRemaining: 10000000, OutputBudgetRemaining: 4096}
	choice := catalog.ModelSelection{ModelID: "chosen", MaxOutputTokens: 256}
	valid, err := prepareSDDBrainstormPrompt(base, "question", choice, "openai")
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"wire_bytes", "framing_bytes", "input_budget", "output_budget", "context", "invalid_utf8", "wrong_cap", "unknown_kind"} {
		t.Run(scenario, func(t *testing.T) {
			run, selection, kind := base, choice, "question"
			switch scenario {
			case "wire_bytes":
				run.DiscoveryContent = strings.Repeat("\"", maxPromptBytes/2)
			case "framing_bytes":
				run.DiscoveryContent = strings.Repeat("a", maxPromptBytes-valid.SerializedBytes+len(base.DiscoveryContent)-512)
			case "input_budget":
				run.InputBudgetRemaining = valid.EstimatedInputTokens - 1
			case "output_budget":
				run.OutputBudgetRemaining = 255
			case "context":
				selection.ContextLength = int(valid.EstimatedInputTokens) + 255
			case "invalid_utf8":
				run.DiscoveryContent = "\xff"
			case "wrong_cap":
				selection.MaxOutputTokens = 1024
			case "unknown_kind":
				kind = "spec"
			}
			if _, err := prepareSDDBrainstormPrompt(run, kind, selection, "openai"); err == nil {
				t.Fatal("invalid prompt admitted")
			}
		})
	}
	choice.ContextLength = int(valid.EstimatedInputTokens) + 256
	fit, err := prepareSDDBrainstormPrompt(base, "question", choice, "openai")
	if err != nil || !fit.ContextLengthKnown {
		t.Fatal("exact context fit rejected", err)
	}
	base.InputBudgetRemaining = valid.EstimatedInputTokens - 1
	if _, err := prepareSDDBrainstormPrompt(base, "question", choice, "openai"); !errors.Is(err, errSDDPromptTooLarge) {
		t.Fatalf("unsafe size code: %v", err)
	}
}

func TestSDDPromptMatchesActualInternalRequest(t *testing.T) {
	fake := &fakeProvider{output: `{"question":"What is needed?"}`}
	s, db, ref, attempt := sddSessionFixture(t, fake)
	run, err := db.GetBrainstorming(t.Context(), ref.RunID)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := prepareSDDBrainstormPrompt(run, attempt.Kind, attempt.Selection, "openai")
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.createSDDReadOnlySession(ref, attempt)
	if err != nil {
		t.Fatal(err)
	}
	attempt.SessionID = session.ID
	if _, err := s.promptSDDAttempt(t.Context(), ref, attempt, prompt.Text); err != nil {
		t.Fatal(err)
	}
	want := agentcore.ChatRequest{Model: attempt.Selection.ModelID, Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: prompt.Text}}, Tools: fake.request.Tools, MaxOutputTokens: 256}
	if !reflect.DeepEqual(fake.request, want) || len(fake.request.Tools) != 0 {
		t.Fatalf("request diverged: %+v", fake.request)
	}
	size, err := openai.RequestSize(fake.request, "openai")
	if err != nil || size != prompt.SerializedBytes {
		t.Fatalf("wire request size changed: %d %v", size, err)
	}
	encoded, _ := json.Marshal(fake.request)
	if strings.Contains(string(encoded), "system") {
		t.Fatal("global instructions in prompt")
	}
	evidence, err := s.readSDDBrainstormEvidence(t.Context(), session.ID, "question")
	if err != nil || evidence.Question != "What is needed?" || evidence.Usage != nil {
		t.Fatalf("actual journal evidence: %+v %v", evidence, err)
	}
}

func TestSDDPromptReservationIncludesCodexCLIContext(t *testing.T) {
	run := catalog.BrainstormRun{DiscoveryVersion: 1, DiscoveryContent: "Discovery", InputBudgetRemaining: 100000, OutputBudgetRemaining: 4096}
	choice := catalog.ModelSelection{BackendID: "codex", Source: "codex_app_server", ModelID: "gpt-6-sol", MaxOutputTokens: 256}
	prompt, err := prepareSDDBrainstormPrompt(run, "question", choice, "openai")
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

func TestSDDPromptChecksStorageReservationFloorWithoutConfirmingPendingTurns(t *testing.T) {
	run := catalog.BrainstormRun{DiscoveryContent: "Discovery", InputBudgetRemaining: 100000, OutputBudgetRemaining: 4096,
		Turns: []catalog.BrainstormTurn{{Question: strings.Repeat("🧭", 2000), Status: "waiting_answer"}},
	}
	choice := catalog.ModelSelection{ModelID: "chosen", MaxOutputTokens: 256}
	prompt, err := prepareSDDBrainstormPrompt(run, "question", choice, "openai")
	if err != nil {
		t.Fatal(err)
	}
	run.InputBudgetRemaining = prompt.EstimatedInputTokens
	if _, err := prepareSDDBrainstormPrompt(run, "question", choice, "openai"); !errors.Is(err, errSDDPromptTooLarge) {
		t.Fatalf("storage would reserve more than remaining budget: %v", err)
	}
}
