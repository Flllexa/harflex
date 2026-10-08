package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
)

func TestReasoningEffortWireAndSize(t *testing.T) {
	for _, provider := range []string{"openai", "openrouter", "lm_studio", "ollama"} {
		for _, effort := range []string{"", "high"} {
			t.Run(provider+"/"+effort, func(t *testing.T) {
				request := agentcore.ChatRequest{Model: "exact", ReasoningEffort: effort, MaxOutputTokens: 321}
				size, err := RequestSize(request, provider)
				if err != nil {
					t.Fatal(err)
				}
				client := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil || len(body) != size {
						t.Errorf("wire size=%d estimate=%d error=%v", len(body), size, err)
					}
					var fields map[string]json.RawMessage
					if err := json.Unmarshal(body, &fields); err != nil {
						t.Fatal(err)
					}
					actual, present := fields["reasoning_effort"]
					if effort == "" && present {
						t.Error("Automatic must omit reasoning_effort")
					}
					if effort != "" && string(actual) != `"high"` {
						t.Errorf("effort=%s", actual)
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: [DONE]\n\n")
				})
				client.providerType = provider
				if _, err := collect(t, t.Context(), client, request); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestRequestSizeMatchesActualWireBody(t *testing.T) {
	for _, provider := range []string{"openai", "openrouter", "ollama", "lm_studio"} {
		t.Run(provider, func(t *testing.T) {
			request := agentcore.ChatRequest{Model: "selected", Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "café 🧭 \"quoted\" <data>"}}, MaxOutputTokens: 256}
			size, err := RequestSize(request, provider)
			if err != nil {
				t.Fatal(err)
			}
			c := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil || len(body) != size {
					t.Errorf("size=%d actual=%d err=%v", size, len(body), err)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: [DONE]\n\n")
			})
			c.providerType = provider
			if _, err := collect(t, t.Context(), c, request); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestIncompleteProviderUsageRemainsUnknown(t *testing.T) {
	for _, usage := range []string{`{}`, `{"prompt_tokens":11}`, `{"completion_tokens":7}`, `{"prompt_tokens":null,"completion_tokens":7}`, `{"prompt_tokens":-1,"completion_tokens":7}`} {
		t.Run(usage, func(t *testing.T) {
			c := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: {\"choices\":[],\"usage\":%s}\n\ndata: [DONE]\n\n", usage)
			})
			events, err := collect(t, t.Context(), c, agentcore.ChatRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 0 {
				t.Fatalf("missing usage became known: %+v", events)
			}
		})
	}
}
