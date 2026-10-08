package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/mcpserver"
	"github.com/persioflexa/harflex/internal/security"
	"github.com/persioflexa/harflex/internal/tools"
)

// The Harflex tools let a chat agent work with the platform itself, always inside the conversation's project:
// create an SDD pipeline from what was agreed in the chat, and read the pipelines back. Phase approvals stay with
// the person on the Pipelines screen.
const (
	harflexCreatePipelineTool = "harflex_create_pipeline"
	harflexListPipelinesTool  = "harflex_list_pipelines"
	harflexGetPipelineTool    = "harflex_get_pipeline"
	maxPipelineDocumentText   = 12000
)

func (s *Service) harflexTools(workspaceID string) []tools.Tool {
	return []tools.Tool{
		harflexTool{service: s, workspaceID: workspaceID, risk: security.Write, spec: agentcore.ToolSpec{
			Name: harflexCreatePipelineTool,
			Description: "Create an SDD pipeline (Discovery → SPEC → Plan → Code → QA → PRs) in this conversation's Harflex project. " +
				"Call it only after agreeing with the person on the problem, goal, scope and acceptance criteria. The discovery is Markdown that starts with \"# <short title>\" " +
				"and holds the context, the goal, what is in and out of scope, the acceptance criteria and open questions. The person then follows and approves each phase on the Pipelines screen.",
			Schema: json.RawMessage(`{"type":"object","properties":{"discovery":{"type":"string","minLength":20,"description":"Markdown discovery document starting with '# <short title>'."}},"required":["discovery"],"additionalProperties":false}`),
		}, run: harflexCreatePipeline},
		harflexTool{service: s, workspaceID: workspaceID, risk: security.ReadOnly, spec: agentcore.ToolSpec{
			Name:        harflexListPipelinesTool,
			Description: "List the SDD pipelines of this conversation's Harflex project, newest first, with their current phase and the status of each phase.",
			Schema:      json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		}, run: harflexListPipelines},
		harflexTool{service: s, workspaceID: workspaceID, risk: security.ReadOnly, spec: agentcore.ToolSpec{
			Name:        harflexGetPipelineTool,
			Description: "Read one SDD pipeline of this project: its phases, their status and, when asked, the text of its phase documents (Discovery, SPEC, Plan…).",
			Schema:      json.RawMessage(`{"type":"object","properties":{"pipelineId":{"type":"string","minLength":1},"includeDocuments":{"type":"boolean","default":false}},"required":["pipelineId"],"additionalProperties":false}`),
		}, run: harflexGetPipeline},
	}
}

type harflexTool struct {
	service     *Service
	workspaceID string
	risk        security.Risk
	spec        agentcore.ToolSpec
	run         func(*Service, string, json.RawMessage) (any, error)
}

func (t harflexTool) Spec() agentcore.ToolSpec { return t.spec }
func (t harflexTool) Risk() security.Risk      { return t.risk }

func (t harflexTool) Execute(ctx context.Context, args json.RawMessage, _ agentcore.ToolUpdateSink) (agentcore.ToolExecutionResult, error) {
	if err := ctx.Err(); err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	value, err := t.run(t.service, t.workspaceID, args)
	if err != nil {
		return agentcore.ToolExecutionResult{}, harflexToolFailure(err)
	}
	content, err := json.Marshal(value)
	if err != nil {
		return agentcore.ToolExecutionResult{}, err
	}
	return agentcore.ToolExecutionResult{Content: content}, nil
}

var errHarflexArguments = errors.New("invalid arguments")

// Every failure is one the model can act on; the run goes on.
func harflexToolFailure(err error) error {
	switch {
	case errors.Is(err, errHarflexArguments), errors.Is(err, ErrInvalidInput):
		return (&agentcore.ToolFailure{Code: "invalid_arguments", Message: "the arguments are not valid; check the tool's schema and try again"}).WithCause(err)
	case errors.Is(err, ErrPullRequestNotFound):
		return (&agentcore.ToolFailure{Code: "not_found", Message: "this pull request is not recorded for this pipeline; record it with harflex_register_pull_request first"}).WithCause(err)
	case errors.Is(err, ErrPullRequestNotFound):
		return (&agentcore.ToolFailure{Code: "not_found", Message: "this pull request is not recorded for this pipeline; record it with harflex_register_pull_request first"}).WithCause(err)
	case errors.Is(err, ErrPipelineNotFound):
		return (&agentcore.ToolFailure{Code: "not_found", Message: "no pipeline with this id in this project; list the pipelines first"}).WithCause(err)
	case errors.Is(err, ErrWorkspaceNotFound):
		return (&agentcore.ToolFailure{Code: "not_found", Message: "this conversation's project is no longer in Harflex"}).WithCause(err)
	default:
		return (&agentcore.ToolFailure{Code: "harflex_unavailable", Message: "Harflex could not complete this request"}).WithCause(err)
	}
}

type harflexPipelineSummary struct {
	ID           string            `json:"id"`
	Title        string            `json:"title"`
	CurrentPhase string            `json:"currentPhase"`
	PhaseStatus  map[string]string `json:"phaseStatus"`
	UpdatedAt    time.Time         `json:"updatedAt"`
}

func pipelineSummary(p PipelineDTO) harflexPipelineSummary {
	return harflexPipelineSummary{ID: p.ID, Title: p.Title, CurrentPhase: p.CurrentStage, PhaseStatus: p.StageStatus, UpdatedAt: p.UpdatedAt}
}

func harflexCreatePipeline(s *Service, workspaceID string, args json.RawMessage) (any, error) {
	var in struct {
		Discovery string `json:"discovery"`
	}
	if err := json.Unmarshal(args, &in); err != nil || utf8.RuneCountInString(strings.TrimSpace(in.Discovery)) < 20 {
		return nil, errHarflexArguments
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, err
	}
	created, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspaceID, RequestID: hex.EncodeToString(raw[:]), Discovery: strings.TrimSpace(in.Discovery)})
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	emit := s.emit
	s.mu.RUnlock()
	if emit != nil {
		emit("harflex:pipeline", map[string]string{"workspaceId": workspaceID, "pipelineId": created.ID})
	}
	return map[string]any{"created": true, "pipeline": pipelineSummary(created),
		"next": "The pipeline is on the Pipelines screen; the person follows and approves each phase there, starting with Discovery."}, nil
}

func harflexListPipelines(s *Service, workspaceID string, _ json.RawMessage) (any, error) {
	found, err := s.ListPipelines(workspaceID)
	if err != nil {
		return nil, err
	}
	items := make([]harflexPipelineSummary, 0, len(found))
	for _, item := range found {
		items = append(items, pipelineSummary(item))
	}
	return map[string]any{"pipelines": items}, nil
}

func harflexGetPipeline(s *Service, workspaceID string, args json.RawMessage) (any, error) {
	var in struct {
		PipelineID       string `json:"pipelineId"`
		IncludeDocuments bool   `json:"includeDocuments"`
	}
	if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.PipelineID) == "" {
		return nil, errHarflexArguments
	}
	found, err := s.GetPipeline(strings.TrimSpace(in.PipelineID))
	if err != nil {
		return nil, err
	}
	// Another project's pipeline does not exist for this conversation.
	if found.WorkspaceID != workspaceID {
		return nil, ErrPipelineNotFound
	}
	result := map[string]any{"pipeline": pipelineSummary(found), "objective": found.Objective}
	if in.IncludeDocuments {
		documents := map[string]any{}
		for stage, artifact := range found.Artifacts {
			text := artifact.Content
			clipped := false
			if len(text) > maxPipelineDocumentText {
				text, clipped = clipUTF8(text, maxPipelineDocumentText), true
			}
			documents[stage] = map[string]any{"version": artifact.Version, "review": artifact.ReviewStatus, "content": text, "clipped": clipped}
		}
		result["documents"] = documents
	}
	return result, nil
}

// platformMCPEndpoint returns the Harflex MCP server's URL and this session's token, starting the server the first
// time a CLI chat needs it. A session keeps its token, bound to its project's tools, until the app quits.
func (s *Service) platformMCPEndpoint(sessionID, workspaceID string) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.platformMCP == nil {
		server, err := mcpserver.Start()
		if err != nil {
			return "", "", err
		}
		s.platformMCP = server
		s.platformMCPTokens = map[string]string{}
	}
	token, ok := s.platformMCPTokens[sessionID]
	if !ok {
		token, _ = s.platformMCP.Register(s.harflexTools(workspaceID))
		s.platformMCPTokens[sessionID] = token
	}
	return s.platformMCP.URL(), token, nil
}
