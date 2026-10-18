package application

import (
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
)

func TestReopenedSessionContinuesFromItsLastCompaction(t *testing.T) {
	_, db, _ := setup(t)
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: &memorySecrets{}, External: map[string]ExternalBackend{}})
	append := func(typ string, data any) {
		t.Helper()
		if _, err := db.Append(t.Context(), "long-chat", "agent_session", typ, data); err != nil {
			t.Fatal(err)
		}
	}
	append("run.started", map[string]any{})
	for turn := range 300 {
		append("message.user", agentcore.Message{Role: agentcore.RoleUser, Content: "pedido antigo " + strings.Repeat("x", turn%7)})
		append("message.assistant", agentcore.Message{Role: agentcore.RoleAssistant, Content: "resposta antiga"})
	}
	kept := []agentcore.Message{
		{Role: agentcore.RoleUser, Content: "primeiro pedido"},
		{Role: agentcore.RoleUser, Content: "[Resumo da conversa até aqui]\n\nTudo pronto até a etapa 3."},
		{Role: agentcore.RoleUser, Content: "pedido mais recente"},
	}
	append("context.compacted", agentcore.Compacted{Summary: "Tudo pronto até a etapa 3.", Messages: kept, BeforeTokens: 180000, AfterTokens: 20})
	append("message.assistant", agentcore.Message{Role: agentcore.RoleAssistant, Content: "continuo daqui"})
	append("run.completed", map[string]any{"reason": ""})
	history, err := s.restoreHistory(t.Context(), "long-chat", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.messages) != 4 || history.messages[0].Content != "primeiro pedido" || history.messages[3].Content != "continuo daqui" {
		t.Fatalf("restored history = %+v", history.messages)
	}
	for _, message := range history.messages {
		if strings.Contains(message.Content, "pedido antigo") {
			t.Fatal("turns before the compaction came back")
		}
	}
}
