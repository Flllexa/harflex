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

const designSystemPrompt = `Você é o autor de documentos de desenvolvimento do Harflex. Esta é uma tarefa de texto, sem ferramentas ou acesso a arquivos. Use apenas o pedido e os documentos fornecidos como dados. O campo projectMemory, quando presente, descreve o que o projeto já tem (tecnologias, domínios, repositórios e serviços): use-o para escrever no vocabulário do projeto, reaproveitar o que já existe e apontar onde a mudança se encaixa, sem perguntar o que ele já responde. Não execute código, não consulte outros projetos, não abra serviços, cards ou PRs. O usuário delegou a preparação e as decisões de UX: produza um rascunho completo com hipóteses claras, sem parar apenas para pedir aprovação de um plano. O campo target determina um único documento para esta chamada. Mesmo quando o pedido geral menciona vários documentos, escreva somente o documento indicado por target; os demais são contexto e serão preparados em chamadas separadas. Não inclua uma seção Plan dentro da SPEC nem uma seção SPEC dentro do Plan. Discovery registra o objetivo, contexto e decisões do usuário; SPEC contém escopo, requisitos e critérios de aceite verificáveis; Plan contém passos de implementação, arquivos previstos e verificações. Responda somente um objeto JSON com as chaves document e reply. document contém o Markdown completo do único documento em português do Brasil; reply resume em uma ou duas frases somente a mudança feita nesse documento. Preserve decisões e edições do usuário que o novo pedido não altera. Registre as decisões do documento numa seção "Decisões" em tabela Markdown com as colunas | # | Questão | Decisão | Justificativa |, com ids D1, D2…; e o que depende da pessoa numa seção "Perguntas em aberto", numerada P1, P2…, cada pergunta com as opções possíveis como sub-itens quando houver. O Harflex mostra essas decisões e perguntas como cartões para a pessoa manter, mudar ou responder; quando ela responder, ajuste as decisões e tire das perguntas em aberto o que foi respondido; uma pergunta respondida vira decisão com "(resposta à Pn)" no título e "Decisão da pessoa" no início da justificativa, e uma decisão mudada mantém o mesmo id e o início do título. Não declare testes ou implementação realizados: você está preparando documentos para revisão.`

var designOutputSchema = json.RawMessage(`{"type":"object","properties":{"document":{"type":"string"},"reply":{"type":"string"}},"required":["document","reply"],"additionalProperties":false}`)

func pipelineDesignIntent(in PreparePipelineDesignInput) (string, error) {
	if !validPipelineDesignRef(in.Ref) || !validPipelineDesignMessage(in.Message) || (in.Target != "all" && !designStage(in.Target)) || len(in.Selections) > len(designStages()) {
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
	admitted, settled, err := s.admitPipelineDesign(in)
	if admitted == nil {
		return settled, err
	}
	return s.runPipelineDesignAttempt(admitted)
}

// startPipelineDesign admits a preparation now and writes its documents in the background, so the caller can answer the
// person while the AI works. The screen follows the attempt as it follows any other.
func (s *Service) startPipelineDesign(in PreparePipelineDesignInput) (PipelineDesignDTO, error) {
	admitted, settled, err := s.admitPipelineDesign(in)
	if admitted == nil {
		return settled, err
	}
	if err := s.beginCall(); err != nil {
		admitted.cancel()
		_, _ = s.runPipelineDesignAttempt(admitted)
		return settled, err
	}
	go func() {
		defer s.endCall()
		_, _ = s.runPipelineDesignAttempt(admitted)
	}()
	return pipelineDesignDTO(admitted.admitted), nil
}

// pipelineDesignAdmission is a preparation the store admitted: its attempt, the documents it writes and who writes them.
type pipelineDesignAdmission struct {
	ctx        context.Context
	cancel     context.CancelFunc
	owner      *pipelineDesignOwner
	workspace  catalog.PipelineDesignWorkspace // as the request saw it: the conversation before this request
	admitted   catalog.PipelineDesignWorkspace // after admission, with the running attempt
	attempt    catalog.PipelineDesignAttempt
	stages     []sdd.Stage
	selections map[sdd.Stage]catalog.ModelSelection
	message    string
}

// admitPipelineDesign checks a preparation request and admits its attempt. Without an admission it returns the design
// as it stands (a replay, or an attempt another call already admitted) or the error that refused it.
func (s *Service) admitPipelineDesign(in PreparePipelineDesignInput) (*pipelineDesignAdmission, PipelineDesignDTO, error) {
	hash, err := pipelineDesignIntent(in)
	if err != nil {
		return nil, PipelineDesignDTO{}, err
	}
	workspace, err := s.openPipelineDesign(s.ctx, in.Ref.PipelineID)
	if err != nil {
		return nil, PipelineDesignDTO{}, err
	}
	for _, attempt := range workspace.Attempts {
		if attempt.RequestID != in.Ref.RequestID {
			continue
		}
		if attempt.IntentHash != hash {
			return nil, PipelineDesignDTO{}, ErrPipelineRequestConflict
		}
		return nil, pipelineDesignDTO(workspace), nil
	}
	if workspace.NeedsDerivation || workspace.Revision != in.Ref.DesignRevision || workspace.PipelineRevision != in.Ref.PipelineRevision || workspace.CurrentPipelineRevision != in.Ref.PipelineRevision {
		return nil, PipelineDesignDTO{}, sqlite.ErrPipelineConflict
	}
	if workspace.State == "running" || workspace.State == "cancellation_pending" {
		return nil, PipelineDesignDTO{}, sdd.ErrInvalidTransition
	}
	stages := pipelineDesignStagesFor(in.Target, workspace)
	if in.Target == "plan" && workspace.Documents[sdd.Spec].Stale {
		return nil, PipelineDesignDTO{}, ErrPipelineDesignSpecStale
	}
	if len(stages) == 0 || workspace.Documents[sdd.Discovery].Content == "" || in.Target == "plan" && workspace.Documents[sdd.Spec].Content == "" {
		return nil, PipelineDesignDTO{}, sdd.ErrEvidenceRequired
	}
	selections := map[sdd.Stage]catalog.ModelSelection{}
	for _, stage := range stages {
		var override *APIModelSelectionInput
		if value, found := in.Selections[string(stage)]; found {
			override = &value
		}
		choice, choiceErr := s.preparePipelineDesignSelection(s.ctx, workspace.WorkspaceID, stage, override)
		if choiceErr != nil {
			return nil, PipelineDesignDTO{}, safe("choose document model", choiceErr)
		}
		if stage == sdd.Brainstorm {
			if err := brainstormSelectionFits(choice); err != nil {
				return nil, PipelineDesignDTO{}, err
			}
		}
		selections[stage] = choice
	}
	s.authoringAdmissionGate.Lock()
	s.mu.RLock()
	closing := s.closing
	s.mu.RUnlock()
	if closing {
		s.authoringAdmissionGate.Unlock()
		return nil, PipelineDesignDTO{}, context.Canceled
	}
	admittedWorkspace, attempt, admitted, err := s.store.BeginPipelineDesignAttempt(s.ctx, catalog.PipelineDesignAttemptRequest{Ref: in.Ref.request(), Message: in.Message, Target: in.Target, IntentHash: hash, OwnerPID: os.Getpid(), Selections: selections})
	if err != nil || !admitted {
		s.authoringAdmissionGate.Unlock()
		if err != nil {
			return nil, PipelineDesignDTO{}, safe("admit document preparation", err)
		}
		return nil, pipelineDesignDTO(admittedWorkspace), nil
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
	return &pipelineDesignAdmission{ctx: ctx, cancel: cancel, owner: owner, workspace: workspace, admitted: admittedWorkspace, attempt: attempt, stages: stages, selections: selections, message: in.Message}, PipelineDesignDTO{}, nil
}

// runPipelineDesignAttempt writes an admitted preparation's documents, phase by phase, and settles its attempt.
func (s *Service) runPipelineDesignAttempt(admitted *pipelineDesignAdmission) (PipelineDesignDTO, error) {
	ctx, attempt, stages, selections := admitted.ctx, admitted.attempt, admitted.stages, admitted.selections
	workspace := admitted.workspace
	defer func() {
		admitted.cancel()
		s.mu.Lock()
		delete(s.designOwners, attempt.ID)
		s.mu.Unlock()
		close(admitted.owner.done)
	}()
	documents := map[sdd.Stage]catalog.PipelineDesignDocument{}
	for stage, document := range attempt.InputDocuments {
		documents[stage] = document
	}
	outputs := map[sdd.Stage]catalog.PipelineDesignDocumentInput{}
	replies := []string{}
	memory := s.projectMemoryContext(ctx, workspace.WorkspaceID)
	var err error
	for _, stage := range stages {
		if err := ctx.Err(); err != nil {
			return s.failPipelineDesign(attempt, err)
		}
		if _, err := s.store.SetPipelineDesignPhase(ctx, workspace.PipelineID, attempt.ID, stage); err != nil {
			return s.failPipelineDesign(attempt, err)
		}
		var document, reply, sessionID string
		var choice catalog.ModelSelection
		if stage == sdd.Brainstorm {
			document, reply, sessionID, choice, err = s.runBrainstormPhase(ctx, workspace, attempt, selections[stage], admitted.message, documents, memory)
			if err != nil {
				return s.failPipelineDesign(attempt, err)
			}
		} else {
			prompt, err := pipelineDesignPrompt(stage, admitted.message, documents, workspace.Messages, memory)
			if err != nil {
				return s.failPipelineDesign(attempt, err)
			}
			var content string
			content, sessionID, choice, err = s.runPipelineDesignPhase(ctx, workspace, attempt, stage, selections[stage], designSystemPrompt, designOutputSchema, prompt, "")
			if err != nil {
				return s.failPipelineDesign(attempt, err)
			}
			document, reply, err = parsePipelineDesignOutput(content)
			if err != nil {
				return s.failPipelineDesign(attempt, err)
			}
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
	closing := s.closing
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
	content[string(sdd.Brainstorm)] = brainstormSummary(documents[sdd.Brainstorm].Content)
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

// runPipelineDesignPhase makes one document call in a session of its own, linked to the attempt before the model is
// composed. replaces names the session of an answer that was refused, when this call is its corrective retry.
func (s *Service) runPipelineDesignPhase(ctx context.Context, workspace catalog.PipelineDesignWorkspace, attempt catalog.PipelineDesignAttempt, stage sdd.Stage, choice catalog.ModelSelection, systemPrompt string, schema json.RawMessage, prompt, replaces string) (string, string, catalog.ModelSelection, error) {
	registered, err := s.store.GetWorkspace(ctx, workspace.WorkspaceID)
	if err != nil {
		return "", "", choice, err
	}
	if stage == sdd.Brainstorm && !brainstormPromptFits(choice, systemPrompt, prompt) {
		return "", "", choice, ErrBrainstormModelTooSmall
	}
	record, choice, err := s.createDocumentSession(ctx, registered, choice, systemPrompt, prompt)
	if err != nil {
		return "", record.ID, choice, err
	}
	if replaces == "" {
		_, err = s.store.LinkPipelineDesignSession(ctx, workspace.PipelineID, attempt.ID, stage, record.ID)
	} else {
		_, err = s.store.RelinkPipelineDesignSession(ctx, workspace.PipelineID, attempt.ID, stage, replaces, record.ID)
	}
	if err != nil {
		return "", record.ID, choice, err
	}
	content, err := s.runDocumentSession(ctx, registered, &record, choice, systemPrompt, schema, prompt, pipelineDesignOutputBytes(stage))
	return content, record.ID, choice, err
}

// runBrainstormPhase writes the Brainstorm document. An answer that is not a valid document (cut short, without the
// flow, with a forbidden tag) is refused once with its reason, and the AI answers again in a new session; a second
// refusal fails the preparation. The document saved is always the canonical form of an accepted answer.
func (s *Service) runBrainstormPhase(ctx context.Context, workspace catalog.PipelineDesignWorkspace, attempt catalog.PipelineDesignAttempt, choice catalog.ModelSelection, message string, documents map[sdd.Stage]catalog.PipelineDesignDocument, memory string) (string, string, string, catalog.ModelSelection, error) {
	registered, err := s.store.GetWorkspace(ctx, workspace.WorkspaceID)
	if err != nil {
		return "", "", "", choice, err
	}
	design := projectDesignContext(registered.Path)
	refused, previous := "", ""
	for {
		prompt, err := brainstormPrompt(message, documents, workspace.Messages, memory, design, refused)
		if err != nil {
			return "", "", "", choice, err
		}
		content, sessionID, used, err := s.runPipelineDesignPhase(ctx, workspace, attempt, sdd.Brainstorm, choice, brainstormSystemPrompt, brainstormOutputSchema, prompt, previous)
		if err != nil {
			return "", "", sessionID, used, err
		}
		brainstorm, reply, err := parseBrainstormOutput(content)
		if err == nil {
			return brainstorm.Canonical(), reply, sessionID, used, nil
		}
		if previous != "" || !errors.Is(err, errInvalidSDDOutput) {
			return "", "", sessionID, used, err
		}
		refused, previous = strings.TrimPrefix(err.Error(), errInvalidSDDOutput.Error()+": "), sessionID
	}
}

// createDocumentSession records the read-only session one document call runs in, with the selection it was made for.
func (s *Service) createDocumentSession(ctx context.Context, registered catalog.Workspace, choice catalog.ModelSelection, systemPrompt, prompt string) (catalog.SessionRecord, catalog.ModelSelection, error) {
	if !brainstormPromptFits(choice, systemPrompt, prompt) {
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
// or through the API profile's own runner. It returns the assistant's text, of at most outputBytes.
func (s *Service) runDocumentSession(ctx context.Context, registered catalog.Workspace, record *catalog.SessionRecord, choice catalog.ModelSelection, systemPrompt string, schema json.RawMessage, prompt string, outputBytes int) (string, error) {
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
		output, generationErr := generator.GenerateDocument(phaseCtx, externalagent.DocumentRequest{Prompt: prompt, SystemPrompt: systemPrompt, CWD: scratch, Model: choice.ModelID, ReasoningEffort: choice.ReasoningEffort, ExpectedExecutablePath: choice.ExecutablePath, ExpectedExecutableVersion: choice.ExecutableVersion, OutputSchema: schema, MaxAssistantOutputBytes: outputBytes}, func(event externalagent.Event) {
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
	if err := core.SetAssistantOutputByteLimit(outputBytes); err != nil {
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
