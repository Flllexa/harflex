package application

import (
	"context"
	"encoding/json"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/security"
	"github.com/persioflexa/harflex/internal/tools"
)

type knowledgeSearchTool struct {
	service     *Service
	workspaceID string
}

var _ tools.Tool = knowledgeSearchTool{}

func (knowledgeSearchTool) Spec() agentcore.ToolSpec {
	return agentcore.ToolSpec{Name: "knowledge_search", Description: "Busca em documentos locais deste projeto; informa modo textual, semântico ou híbrido e retorna caminho, linha, trecho e horário da fonte. Os resultados da ferramenta podem ser enviados ao provedor de IA da sessão.", Schema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"documentId":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":20}},"required":["query"],"additionalProperties":false}`)}
}

func (knowledgeSearchTool) Risk() security.Risk { return security.ReadOnly }

func (t knowledgeSearchTool) Execute(ctx context.Context, input json.RawMessage, _ agentcore.ToolUpdateSink) (agentcore.ToolExecutionResult, error) {
	var args struct {
		Query      string `json:"query"`
		DocumentID string `json:"documentId"`
		Limit      int    `json:"limit"`
	}
	if len(input) > 4096 {
		return agentcore.ToolExecutionResult{}, ErrInvalidInput
	}
	if err := json.Unmarshal(input, &args); err != nil || args.Query == "" || args.Limit < 0 || args.Limit > 20 {
		return agentcore.ToolExecutionResult{}, ErrInvalidInput
	}
	if args.Limit == 0 {
		args.Limit = 5
	}
	if err := ctx.Err(); err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	result, err := t.service.searchKnowledgeDetailed(ctx, SearchKnowledgeInput{WorkspaceID: t.workspaceID, DocumentID: args.DocumentID, Query: args.Query, Limit: args.Limit})
	if err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	content, err := json.Marshal(result)
	if err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	return agentcore.ToolExecutionResult{Content: content}, nil
}
