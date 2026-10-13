package application

import (
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

const coderDoneReply = "Implementei o plano.\n\n```harflex-code\n{\"status\":\"done\",\"summary\":\"Plano implementado.\"}\n```"

// coderSaidDone records the latest Coder saying it finished, as a real Coder ends its turn, for tests about what comes after.
func coderSaidDone(t *testing.T, s *Service, pipelineID string) *Service {
	t.Helper()
	link, err := s.latestPipelineSession(pipelineID, "coder")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.(*sqlite.Store).Append(t.Context(), link.SessionID, "agent_session", "message.assistant", agentcore.Message{Role: agentcore.RoleAssistant, Content: coderDoneReply}); err != nil {
		t.Fatal(err)
	}
	return s
}
