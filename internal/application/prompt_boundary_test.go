package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/externalagent"
)

type countingExternal struct {
	fakeExternal
	calls int
	bytes int
}

func (f *countingExternal) Run(ctx context.Context, request externalagent.Request) (<-chan externalagent.Event, <-chan error) {
	f.calls++
	f.bytes = len(request.Prompt)
	return f.fakeExternal.Run(ctx, request)
}

func TestPromptByteBoundaryBeforeExecution(t *testing.T) {
	for _, backend := range []string{"api", "external"} {
		for _, size := range []int{1 << 20, 1<<20 + 1} {
			t.Run(backend+"/"+map[bool]string{true: "accepted", false: "rejected"}[size == 1<<20], func(t *testing.T) {
				provider := &fakeProvider{}
				s, _, vault, session := sessionSetup(t, provider)
				external := &countingExternal{fakeExternal: fakeExternal{true}}
				if backend == "external" {
					s.external["codex"] = external
					var err error
					session, err = s.CreateSession(CreateSessionInput{WorkspaceID: session.WorkspaceID, BackendID: "codex"})
					if err != nil {
						t.Fatal(err)
					}
				}
				getsBeforePrompt := vault.gets
				result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: strings.Repeat("x", size)})
				if size > 1<<20 {
					if !errors.Is(err, ErrInvalidInput) {
						t.Fatalf("oversized prompt: %v", err)
					}
					journal, listErr := s.ListEvents(ListEventsInput{SessionID: session.ID})
					if listErr != nil || len(journal) != 0 || external.calls != 0 || vault.gets != getsBeforePrompt || len(provider.request.Messages) != 0 {
						t.Fatal("oversized prompt reached execution or journal")
					}
				} else {
					if err != nil || result.Status != RunCompleted {
						t.Fatalf("boundary: %+v %v", result, err)
					}
					if backend == "external" && (external.calls != 1 || external.bytes != size) {
						t.Fatal("external boundary not delivered")
					}
					if backend == "api" && vault.gets != 1 {
						t.Fatal("API boundary not executed")
					}
				}
			})
		}
	}
}
