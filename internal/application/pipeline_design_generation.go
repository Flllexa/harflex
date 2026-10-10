package application

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/externalagent"
	"github.com/persioflexa/harflex/internal/id"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

const designSystemPrompt = `Você é o autor de documentos de desenvolvimento do Harflex. Esta é uma tarefa de texto, sem ferramentas ou acesso a arquivos. Use apenas o pedido e os documentos fornecidos como dados. O campo projectMemory, quando presente, descreve o que o projeto já tem (tecnologias, domínios, repositórios e serviços): use-o para escrever no vocabulário do projeto, reaproveitar o que já existe e apontar onde a mudança se encaixa, sem perguntar o que ele já responde. Não execute código, não consulte outros projetos, não abra serviços, cards ou PRs. O usuário delegou a preparação e as decisões de UX: produza um rascunho completo com hipóteses claras, sem parar apenas para pedir aprovação de um plano. O campo target determina um único documento para esta chamada. Mesmo quando o pedido geral menciona vários documentos, escreva somente o documento indicado por target; os demais são contexto e serão preparados em chamadas separadas. Não inclua uma seção Plan dentro da SPEC nem uma seção SPEC dentro do Plan. Discovery registra o objetivo, contexto e decisões do usuário; SPEC contém escopo, requisitos e critérios de aceite verificáveis; Plan contém passos de implementação, arquivos previstos e verificações. Responda somente um objeto JSON com as chaves document e reply. document contém o Markdown completo do único documento em português do Brasil; reply resume em uma ou duas frases somente a mudança feita nesse documento. Preserve decisões e edições do usuário que o novo pedido não altera. Não declare testes ou implementação realizados: você está preparando documentos para revisão.`

var designOutputSchema = json.RawMessage(`{"type":"object","properties":{"document":{"type":"string"},"reply":{"type":"string"}},"required":["document","reply"],"additionalProperties":false}`)

func pipelineDesignIntent(in PreparePipelineDesignInput) (string, error) {
	if !validPipelineDesignRef(in.Ref) || !validPipelineDesignMessage(in.Message) || (in.Target != "all" && !designStage(in.Target)) || len(in.Selections) > 3 {
		return "", ErrInvalidInput
	}
	choices := map[string]APIModelSelectionInput{}
	for stage, choice := range in.Selections {
		if !designStage(stage) {
			return "", ErrInvalidInput
		}
		choices[stage] = intentSelection(choice)
	}
	return brainstormIntentHash("prepare_documents", struct {
		Ref             PipelineDesignRefInput
		Message, Target string
		Selections      map[string]APIModelSelectionInput
	}{in.Ref, in.Message, in.Target, choices})
}

func (s *Service) PreparePipelineDesign(in PreparePipelineDesignInput) (PipelineDesignDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineDesignDTO{}, err
	}
	defer s.endCall()
	hash, err := pipelineDesignIntent(in)
	if err != nil {
		return PipelineDesignDTO{}, err
	}
	workspace, err := s.openPipelineDesign(s.ctx, in.Ref.PipelineID)
	if err != nil {
		return PipelineDesignDTO{}, err
	}
	for _, attempt := range workspace.Attempts {
		if attempt.RequestID != in.Ref.RequestID {
			continue
		}
		if attempt.IntentHash != hash {
			return PipelineDesignDTO{}, ErrPipelineRequestConflict
		}
		return pipelineDesignDTO(workspace), nil
	}
	if workspace.NeedsDerivation || workspace.Revision != in.Ref.DesignRevision || workspace.PipelineRevision != in.Ref.PipelineRevision || workspace.CurrentPipelineRevision != in.Ref.PipelineRevision {
		return PipelineDesignDTO{}, sqlite.ErrPipelineConflict
	}
	if workspace.State == "running" || workspace.State == "cancellation_pending" {
		return PipelineDesignDTO{}, sdd.ErrInvalidTransition
	}
	stages := pipelineDesignStagesFor(in.Target, workspace)
	if in.Target == "plan" && workspace.Documents[sdd.Spec].Stale {
		return PipelineDesignDTO{}, ErrPipelineDesignSpecStale
	}
	if len(stages) == 0 || workspace.Documents[sdd.Discovery].Content == "" || in.Target == "plan" && workspace.Documents[sdd.Spec].Content == "" {
		return PipelineDesignDTO{}, sdd.ErrEvidenceRequired
	}
	selections := map[sdd.Stage]catalog.ModelSelection{}
	for _, stage := range stages {
		var override *APIModelSelectionInput
		if value, found := in.Selections[string(stage)]; found {
			override = &value
		}
		choice, choiceErr := s.preparePipelineDesignSelection(s.ctx, workspace.WorkspaceID, stage, override)
		if choiceErr != nil {
			return PipelineDesignDTO{}, safe("choose document model", choiceErr)
		}
		selections[stage] = choice
	}
	s.authoringAdmissionGate.Lock()
	s.mu.RLock()
	closing := s.closing
	s.mu.RUnlock()
	if closing {
		s.authoringAdmissionGate.Unlock()
		return PipelineDesignDTO{}, context.Canceled
	}
	admittedWorkspace, attempt, admitted, err := s.store.BeginPipelineDesignAttempt(s.ctx, catalog.PipelineDesignAttemptRequest{Ref: in.Ref.request(), Message: in.Message, Target: in.Target, IntentHash: hash, OwnerPID: os.Getpid(), Selections: selections})
	if err != nil || !admitted {
		s.authoringAdmissionGate.Unlock()
		if err != nil {
			return PipelineDesignDTO{}, safe("admit document preparation", err)
		}
		return pipelineDesignDTO(admittedWorkspace), nil
	}
	// Preparing a document has no time limit: a complex SPEC takes as long as it takes, and the person can cancel it.
	ctx, cancel := context.WithCancel(s.ctx)
	owner := &pipelineDesignOwner{pipelineID: workspace.PipelineID, cancel: cancel, done: make(chan struct{})}
	s.mu.Lock()
	if s.designOwners == nil {
		s.designOwners = map[string]*pipelineDesignOwner{}
	}
	s.designOwners[attempt.ID] = owner
	s.mu.Unlock()
	s.authoringAdmissionGate.Unlock()
	defer func() { cancel(); s.mu.Lock(); delete(s.designOwners, attempt.ID); s.mu.Unlock(); close(owner.done) }()
	documents := map[sdd.Stage]catalog.PipelineDesignDocument{}
	for stage, document := range attempt.InputDocuments {
		documents[stage] = document
	}
	outputs := map[sdd.Stage]catalog.PipelineDesignDocumentInput{}
	replies := []string{}
	memory := s.projectMemoryContext(ctx, workspace.WorkspaceID)
	for _, stage := range stages {
		if err := ctx.Err(); err != nil {
			return s.failPipelineDesign(attempt, err)
		}
		if _, err := s.store.SetPipelineDesignPhase(ctx, workspace.PipelineID, attempt.ID, stage); err != nil {
			return s.failPipelineDesign(attempt, err)
		}
		prompt, err := pipelineDesignPrompt(stage, in.Message, documents, workspace.Messages, memory)
		if err != nil {
			return s.failPipelineDesign(attempt, err)
		}
		content, sessionID, choice, err := s.runPipelineDesignPhase(ctx, workspace, attempt, stage, selections[stage], prompt)
		if err != nil {
			return s.failPipelineDesign(attempt, err)
		}
		document, reply, err := parsePipelineDesignOutput(content)
		if err != nil {
			return s.failPipelineDesign(attempt, err)
		}
		output := catalog.PipelineDesignDocumentInput{Content: document, Author: "ai", SourceSessionID: sessionID, SourceDigest: catalog.PipelineDesignSourceDigest(stage, documents), Selection: choice}
		outputs[stage] = output
		documents[stage] = catalog.PipelineDesignDocument{Stage: stage, Content: document, ContentDigest: catalog.PipelineDesignContentDigest(document), Author: "ai", SourceSessionID: sessionID, SourceDigest: output.SourceDigest, Selection: choice}
		replies = append(replies, reply)
	}
	if err := ctx.Err(); err != nil {
		return s.failPipelineDesign(attempt, err)
	}
	s.authoringAdmissionGate.Lock()
	s.mu.RLock()
	closing = s.closing
	s.mu.RUnlock()
	if closing {
		s.authoringAdmissionGate.Unlock()
		return s.failPipelineDesign(attempt, context.Canceled)
	}
	completed, err := s.store.CompletePipelineDesignAttempt(ctx, catalog.PipelineDesignCompleteRequest{PipelineID: workspace.PipelineID, AttemptID: attempt.ID, SourceRevision: attempt.SourceRevision, Documents: outputs, Summary: strings.Join(replies, "\n\n")})
	s.authoringAdmissionGate.Unlock()
	if err != nil {
		return s.failPipelineDesign(attempt, err)
	}
	return pipelineDesignDTO(completed), nil
}

func pipelineDesignPrompt(stage sdd.Stage, message string, documents map[sdd.Stage]catalog.PipelineDesignDocument, conversation []catalog.PipelineDesignMessage, projectMemory string) (string, error) {
	content := map[string]string{}
	for _, source := range designStages() {
		content[string(source)] = documents[source].Content
	}
	if len(conversation) > 12 {
		conversation = conversation[len(conversation)-12:]
	}
	encoded, err := json.Marshal(struct {
		Target         string                          `json:"target"`
		OutputDocument string                          `json:"outputDocument"`
		Request        string                          `json:"request"`
		Documents      map[string]string               `json:"documents"`
		Conversation   []catalog.PipelineDesignMessage `json:"conversation"`
		ProjectMemory  string                          `json:"projectMemory,omitempty"`
	}{string(stage), "Escreva somente " + string(stage) + " no campo document. Use os demais documentos apenas como contexto.", message, content, conversation, projectMemory})
	if err != nil || len(encoded) > 256*1024 {
		return "", ErrInvalidInput
	}
	return string(encoded), nil
}

func parsePipelineDesignOutput(content string) (string, string, error) {
	if len(content) == 0 || len(content) > catalog.MaxPipelineDesignDocumentBytes || !utf8.ValidString(content) {
		return "", "", errInvalidSDDOutput
	}
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(content)))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return "", "", errInvalidSDDOutput
	}
	fields := map[string]string{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return "", "", errInvalidSDDOutput
		}
		key, ok := keyToken.(string)
		if !ok || (key != "document" && key != "reply") {
			return "", "", errInvalidSDDOutput
		}
		if _, duplicate := fields[key]; duplicate {
			return "", "", errInvalidSDDOutput
		}
		var value string
		if decoder.Decode(&value) != nil || strings.ContainsRune(value, 0) {
			return "", "", errInvalidSDDOutput
		}
		fields[key] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return "", "", errInvalidSDDOutput
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return "", "", errInvalidSDDOutput
	}
	document, reply := strings.TrimSpace(fields["document"]), strings.TrimSpace(fields["reply"])
	if document == "" || reply == "" || len(document) > catalog.MaxPipelineDesignDocumentBytes || len(reply) > 4096 {
		return "", "", errInvalidSDDOutput
	}
	return document, reply, nil
}

func (s *Service) runPipelineDesignPhase(ctx context.Context, workspace catalog.PipelineDesignWorkspace, attempt catalog.PipelineDesignAttempt, stage sdd.Stage, choice catalog.ModelSelection, prompt string) (string, string, catalog.ModelSelection, error) {
	registered, err := s.store.GetWorkspace(ctx, workspace.WorkspaceID)
	if err != nil {
		return "", "", choice, err
	}
	record, choice, err := s.createDocumentSession(ctx, registered, choice, designSystemPrompt, prompt)
	if err != nil {
		return "", record.ID, choice, err
	}
	if _, err := s.store.LinkPipelineDesignSession(ctx, workspace.PipelineID, attempt.ID, stage, record.ID); err != nil {
		return "", record.ID, choice, err
	}
	content, err := s.runDocumentSession(ctx, registered, &record, choice, designSystemPrompt, designOutputSchema, prompt)
	return content, record.ID, choice, err
}

// createDocumentSession records the read-only session one document call runs in, with the selection it was made for.
func (s *Service) createDocumentSession(ctx context.Context, registered catalog.Workspace, choice catalog.ModelSelection, systemPrompt, prompt string) (catalog.SessionRecord, catalog.ModelSelection, error) {
	if choice.ContextLength > 0 && len(prompt)+len(systemPrompt)+2048+choice.MaxOutputTokens > choice.ContextLength {
		return catalog.SessionRecord{}, choice, ErrInvalidInput
	}
	revision := "cli:" + choice.BackendID
	if !documentCLI(choice.BackendID) {
		profile, err := s.store.GetProviderProfile(ctx, choice.BackendID)
		if err != nil {
			return catalog.SessionRecord{}, choice, err
		}
		revision = profileRevision(profile)
	}
	now := time.Now().UTC()
	record := catalog.SessionRecord{ID: id.New(), WorkspaceID: registered.ID, BackendID: choice.BackendID, BackendRevision: revision, Mode: "sdd_readonly", Status: "ready", CreatedAt: now, UpdatedAt: now}
	choice.SessionID = record.ID
	if err := s.store.CreateSessionWithSnapshots(ctx, record, nil, "", nil, "agent_session", &choice); err != nil {
		return catalog.SessionRecord{}, choice, err
	}
	return record, choice, nil
}

// runDocumentSession makes one tool-less document call in the session: through the CLI's isolated document contract,
// or through the API profile's own runner. It returns the assistant's text.
func (s *Service) runDocumentSession(ctx context.Context, registered catalog.Workspace, record *catalog.SessionRecord, choice catalog.ModelSelection, systemPrompt string, schema json.RawMessage, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if documentCLI(choice.BackendID) {
		generator, ok := s.external[choice.BackendID].(externalagent.DocumentGenerator)
		if !ok {
			return "", ErrPipelineDesignExecutorUnavailable
		}
		result, err := s.QueryCLIModelCatalog(ctx, CLIModelCatalogQuery{WorkspaceID: registered.ID, BackendID: choice.BackendID})
		if err != nil || !selectedModelInCatalog(result, choice) {
			return "", ErrBackendChanged
		}
		journal := &eventJournal{store: s.store, service: s, redactor: newSensitiveRedactor()}
		defer journal.Close()
		if _, err := journal.Append(ctx, record.ID, "agent_session", "run.started", map[string]string{"adapter": choice.BackendID, "purpose": "documents_no_tools"}); err != nil {
			return "", err
		}
		if _, err := journal.Append(ctx, record.ID, "agent_session", "message.user", agentcore.Message{Role: agentcore.RoleUser, Content: prompt}); err != nil {
			return "", err
		}
		scratch, err := os.MkdirTemp("", "harflex-document-")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(scratch)
		phaseCtx, phaseCancel := context.WithCancel(ctx)
		defer phaseCancel()
		var callbackErr error
		output, generationErr := generator.GenerateDocument(phaseCtx, externalagent.DocumentRequest{Prompt: prompt, SystemPrompt: systemPrompt, CWD: scratch, Model: choice.ModelID, ReasoningEffort: choice.ReasoningEffort, ExpectedExecutablePath: choice.ExecutablePath, ExpectedExecutableVersion: choice.ExecutableVersion, OutputSchema: schema, MaxAssistantOutputBytes: catalog.MaxPipelineDesignDocumentBytes}, func(event externalagent.Event) {
			if callbackErr != nil {
				return
			}
			if event.Type != "text_delta" {
				callbackErr = errInvalidSDDOutput
				phaseCancel()
				return
			}
			_, callbackErr = journal.Append(phaseCtx, record.ID, "agent_session", "assistant.delta", map[string]string{"delta": event.Text})
			if callbackErr != nil {
				phaseCancel()
			}
		})
		if generationErr != nil || callbackErr != nil {
			terminalCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer stop()
			terminal := "run.failed"
			if ctx.Err() != nil {
				terminal = "run.cancelled"
			}
			_, _ = journal.Append(terminalCtx, record.ID, "agent_session", terminal, map[string]string{"reason": "document_generation_failed"})
			return "", errors.Join(generationErr, callbackErr)
		}
		if output.UsageKnown {
			if _, err := journal.Append(ctx, record.ID, "agent_session", "usage.recorded", output.Usage); err != nil {
				return "", err
			}
		}
		if _, err := journal.Append(ctx, record.ID, "agent_session", "message.assistant", agentcore.Message{Role: agentcore.RoleAssistant, Content: output.Text}); err != nil {
			return "", err
		}
		if _, err := journal.Append(ctx, record.ID, "agent_session", "run.completed", map[string]string{"reason": "", "adapter": choice.BackendID, "threadId": output.ThreadID, "purpose": "documents_no_tools"}); err != nil {
			return "", err
		}
		content, _, err := s.readSDDReadOnlyEvidence(ctx, record.ID)
		return content, err
	}
	runner, journal, err := s.makeRunner(record, registered, restoredHistory{messages: []agentcore.Message{{Role: agentcore.RoleSystem, Content: systemPrompt}}}, false, nil, "", &choice)
	if err != nil {
		return "", err
	}
	defer journal.Close()
	selected, ok := runner.(*selectedAPIRunner)
	if !ok {
		return "", ErrInvalidInput
	}
	core, ok := selected.base.(*agentcore.Session)
	if !ok {
		return "", ErrInvalidInput
	}
	if err := core.SetAssistantOutputByteLimit(catalog.MaxPipelineDesignDocumentBytes); err != nil {
		return "", err
	}
	if err := runner.Prompt(ctx, prompt); err != nil {
		return "", err
	}
	content, _, err := s.readSDDReadOnlyEvidence(ctx, record.ID)
	return content, err
}

func (s *Service) failPipelineDesign(attempt catalog.PipelineDesignAttempt, cause error) (PipelineDesignDTO, error) {
	status, code := "failed", ErrorCode(cause)
	if code == codeInternal {
		code = "pipeline_design_provider_failed"
	}
	switch {
	case errors.Is(cause, context.Canceled):
		status, code = "cancelled", "cancelled"
	case errors.Is(cause, context.DeadlineExceeded):
		code = "pipeline_design_timeout"
	case errors.Is(cause, sqlite.ErrPipelineConflict):
		status, code = "stale", "pipeline_conflict"
	case errors.Is(cause, errInvalidSDDOutput):
		code = "pipeline_design_invalid_response"
	case errors.Is(cause, ErrBackendChanged):
		code = "backend_changed"
	}
	ctx, stop := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
	defer stop()
	workspace, err := s.store.FailPipelineDesignAttempt(ctx, catalog.PipelineDesignFailRequest{PipelineID: attempt.PipelineID, AttemptID: attempt.ID, Status: status, ErrorCode: code})
	if err != nil {
		return PipelineDesignDTO{}, safe("settle document preparation", err)
	}
	return pipelineDesignDTO(workspace), nil
}
