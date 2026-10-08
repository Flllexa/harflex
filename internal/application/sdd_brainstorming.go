package application

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

const maxBrainstormHistoryLimit = 100

type StartBrainstormingInput struct {
	PipelineID       string                 `json:"pipelineId"`
	RequestID        string                 `json:"requestId"`
	PipelineRevision int64                  `json:"pipelineRevision"`
	DiscoveryVersion int64                  `json:"discoveryVersion"`
	Selection        APIModelSelectionInput `json:"selection"`
}

type GetBrainstormingInput struct {
	RunID            string `json:"runId"`
	PipelineID       string `json:"pipelineId"`
	DiscoveryVersion int64  `json:"discoveryVersion"`
}

type ListBrainstormingInput struct {
	PipelineID  string `json:"pipelineId"`
	WorkspaceID string `json:"workspaceId"`
	Limit       int    `json:"limit"`
}

type BrainstormRequestInput struct {
	RunID            string `json:"runId"`
	RequestID        string `json:"requestId"`
	PipelineRevision int64  `json:"pipelineRevision"`
	RunRevision      int64  `json:"runRevision"`
	DiscoveryVersion int64  `json:"discoveryVersion"`
}

type AnswerBrainstormQuestionInput struct {
	Ref        BrainstormRequestInput `json:"ref"`
	QuestionID string                 `json:"questionId"`
	Answer     string                 `json:"answer"`
}

var ErrBrainstormNotFound = errors.New("brainstorm not found")

type GenerateBrainstormInput struct {
	Ref       BrainstormRequestInput `json:"ref"`
	Selection APIModelSelectionInput `json:"selection"`
}

type BrainstormSelectionDTO struct {
	Executor                  string    `json:"executor"`
	BackendID                 string    `json:"backendId"`
	ModelID                   string    `json:"modelId"`
	ReasoningEffort           string    `json:"reasoningEffort"`
	SupportedReasoningEfforts []string  `json:"supportedReasoningEfforts"`
	CatalogRevision           string    `json:"catalogRevision"`
	Source                    string    `json:"source"`
	Destination               string    `json:"destination"`
	Status                    string    `json:"status"`
	ConfirmUnfiltered         bool      `json:"confirmUnfiltered"`
	ConfirmJITLoad            bool      `json:"confirmJitLoad"`
	MaxOutputTokens           int       `json:"maxOutputTokens"`
	ContextLength             int       `json:"contextLength"`
	ExecutableVersion         string    `json:"executableVersion"`
	WorkspacePath             string    `json:"workspacePath"`
	CheckedAt                 time.Time `json:"checkedAt"`
}

type BrainstormAttemptDTO struct {
	ID                   string                   `json:"id"`
	RequestID            string                   `json:"requestId"`
	Kind                 string                   `json:"kind"`
	Status               string                   `json:"status"`
	SessionID            string                   `json:"sessionId"`
	ErrorCode            string                   `json:"errorCode"`
	DiscoveryVersion     int64                    `json:"discoveryVersion"`
	SynthesisVersion     int                      `json:"synthesisVersion"`
	Selection            BrainstormSelectionDTO   `json:"selection"`
	ReservedInputTokens  int64                    `json:"reservedInputTokens"`
	ReservedOutputTokens int64                    `json:"reservedOutputTokens"`
	Usage                *catalog.BrainstormUsage `json:"usage"`
	CreatedAt            time.Time                `json:"createdAt"`
	UpdatedAt            time.Time                `json:"updatedAt"`
}

type BrainstormTurnDTO struct {
	Number          int       `json:"number"`
	QuestionID      string    `json:"questionId"`
	Question        string    `json:"question"`
	Answer          string    `json:"answer"`
	SourceSessionID string    `json:"sourceSessionId"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type BrainstormSynthesisDTO struct {
	Version          int                                `json:"version"`
	DiscoveryVersion int64                              `json:"discoveryVersion"`
	Content          catalog.BrainstormSynthesisContent `json:"content"`
	SourceSessionID  string                             `json:"sourceSessionId"`
	Status           string                             `json:"status"`
	CreatedAt        time.Time                          `json:"createdAt"`
}

type BrainstormDTO struct {
	ID                    string                          `json:"id"`
	PipelineID            string                          `json:"pipelineId"`
	PipelineRevision      int64                           `json:"pipelineRevision"`
	DiscoveryVersion      int64                           `json:"discoveryVersion"`
	DiscoveryContent      string                          `json:"discoveryContent"`
	Selection             BrainstormSelectionDTO          `json:"selection"`
	State                 string                          `json:"state"`
	Revision              int64                           `json:"revision"`
	QuestionCount         int                             `json:"questionCount"`
	CurrentQuestionID     string                          `json:"currentQuestionId"`
	SynthesisVersion      int                             `json:"synthesisVersion"`
	AttemptCount          int                             `json:"attemptCount"`
	InputBudgetRemaining  int64                           `json:"inputBudgetRemaining"`
	OutputBudgetRemaining int64                           `json:"outputBudgetRemaining"`
	ActiveDurationMillis  int64                           `json:"activeDurationMillis"`
	CreatedAt             time.Time                       `json:"createdAt"`
	UpdatedAt             time.Time                       `json:"updatedAt"`
	Attempts              []BrainstormAttemptDTO          `json:"attempts"`
	Turns                 []BrainstormTurnDTO             `json:"turns"`
	Syntheses             []BrainstormSynthesisDTO        `json:"syntheses"`
	HumanActions          []catalog.BrainstormHumanAction `json:"humanActions"`
}

func brainstormSelectionDTO(c catalog.ModelSelection) BrainstormSelectionDTO {
	return BrainstormSelectionDTO{
		Executor: selectionExecutor(c), BackendID: c.BackendID, ModelID: c.ModelID, ReasoningEffort: c.ReasoningEffort,
		SupportedReasoningEfforts: append([]string(nil), c.SupportedReasoningEfforts...),
		CatalogRevision:           c.CatalogRevision, Source: c.Source, Destination: c.Destination,
		Status: c.Status, ConfirmUnfiltered: c.ConfirmUnfiltered, ConfirmJITLoad: c.ConfirmJITLoad,
		MaxOutputTokens: c.MaxOutputTokens, ContextLength: c.ContextLength, ExecutableVersion: c.ExecutableVersion, WorkspacePath: c.WorkspacePath, CheckedAt: c.CheckedAt,
	}
}

// selectionExecutor names how a selection runs. "codex_cli" stands for any CLI working as a model only; the backend
// says which one.
func selectionExecutor(selection catalog.ModelSelection) string {
	if documentCLI(selection.BackendID) && selection.Source == documentCLISources[selection.BackendID] {
		return "codex_cli"
	}
	return "api"
}

func brainstormDTO(run catalog.BrainstormRun) BrainstormDTO {
	out := BrainstormDTO{
		ID: run.ID, PipelineID: run.PipelineID, PipelineRevision: run.PipelineRevision,
		DiscoveryVersion: run.DiscoveryVersion, DiscoveryContent: run.DiscoveryContent,
		Selection: brainstormSelectionDTO(run.Selection), State: run.State, Revision: run.Revision,
		QuestionCount: run.QuestionCount, CurrentQuestionID: run.CurrentQuestionID,
		SynthesisVersion: run.SynthesisVersion, AttemptCount: run.AttemptCount,
		InputBudgetRemaining: run.InputBudgetRemaining, OutputBudgetRemaining: run.OutputBudgetRemaining,
		ActiveDurationMillis: run.ActiveDuration.Milliseconds(), CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
		Attempts: make([]BrainstormAttemptDTO, 0, len(run.Attempts)),
		Turns:    make([]BrainstormTurnDTO, 0, len(run.Turns)), Syntheses: make([]BrainstormSynthesisDTO, 0, len(run.Syntheses)),
		HumanActions: append([]catalog.BrainstormHumanAction{}, run.HumanActions...),
	}
	for _, a := range run.Attempts {
		out.Attempts = append(out.Attempts, BrainstormAttemptDTO{
			ID: a.ID, RequestID: a.RequestID, Kind: a.Kind, Status: a.Status, SessionID: a.SessionID,
			ErrorCode: a.ErrorCode, DiscoveryVersion: a.DiscoveryVersion, SynthesisVersion: a.SynthesisVersion,
			Selection: brainstormSelectionDTO(a.Selection), ReservedInputTokens: a.ReservedInputTokens,
			ReservedOutputTokens: a.ReservedOutputTokens, Usage: a.Usage, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
		})
	}
	for _, v := range run.Turns {
		out.Turns = append(out.Turns, BrainstormTurnDTO{Number: v.Number, QuestionID: v.QuestionID, Question: v.Question, Answer: v.Answer, SourceSessionID: v.SourceSessionID, Status: v.Status, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt})
	}
	for _, v := range run.Syntheses {
		out.Syntheses = append(out.Syntheses, BrainstormSynthesisDTO{Version: v.Version, DiscoveryVersion: v.DiscoveryVersion, Content: v.Content, SourceSessionID: v.SourceSessionID, Status: v.Status, CreatedAt: v.CreatedAt})
	}
	return out
}

func (in BrainstormRequestInput) request() catalog.BrainstormRequest {
	return catalog.BrainstormRequest{RunID: in.RunID, RequestID: in.RequestID, PipelineRevision: in.PipelineRevision, RunRevision: in.RunRevision, DiscoveryVersion: in.DiscoveryVersion}
}

func validBrainstormRef(in BrainstormRequestInput) bool {
	return validSelectionText(in.RunID, 128) && authoringRequestID.MatchString(in.RequestID) && in.PipelineRevision > 0 && in.RunRevision > 0 && in.DiscoveryVersion > 0
}

// Only service-controlled fields and the expiring credential proof are omitted.
// The immutable catalog timestamp and all visible selection/confirmation fields
// remain part of intent, so a changed selection cannot reuse an old admission.
func brainstormIntentHash(action string, input any) (string, error) {
	data, err := json.Marshal(struct {
		Action string
		Input  any
	}{action, input})
	if err != nil {
		return "", ErrInvalidInput
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func intentSelection(in APIModelSelectionInput) APIModelSelectionInput {
	in.CredentialToken = ""
	in.ForSDD = true
	return in
}

// Replay validates the bounded visible payload, but deliberately does not require
// a fresh timestamp or credential proof: historical readback is offline.
func validBrainstormIntentSelection(in APIModelSelectionInput) bool {
	if !validSelectionText(in.ModelID, 512) || !validSelectionText(in.CatalogRevision, 128) ||
		!validSelectionText(in.Source, 64) || in.CheckedAt.IsZero() || in.MaxOutputTokens < 0 || in.MaxOutputTokens > 32768 {
		return false
	}
	switch in.Executor {
	case "", "api":
		return in.BackendID == "" && validSelectionText(in.ProfileID, 128) && validSelectionText(in.Destination, 512) &&
			validAPIReasoningEffort(in.ReasoningEffort) && !in.ConfirmUnverifiedManual
	case "codex_cli":
		return in.ProfileID == "" && in.BackendID == "codex" && in.Source == "codex_app_server" && in.Destination == "" &&
			in.CredentialToken == "" && in.ReasoningEffort == strings.TrimSpace(in.ReasoningEffort) && len(in.ReasoningEffort) <= 64 &&
			utf8.ValidString(in.ReasoningEffort) && strings.IndexFunc(in.ReasoningEffort, unicode.IsControl) < 0 && !strings.ContainsAny(in.ReasoningEffort, " =") &&
			!strings.HasPrefix(in.ReasoningEffort, "-") && !in.ConfirmUnverifiedManual && !in.ConfirmUnfiltered && !in.ConfirmJITLoad
	default:
		return false
	}
}

func (s *Service) resolveBrainstormStart(in StartBrainstormingInput, hash string) (catalog.BrainstormRun, bool, error) {
	run, err := s.store.GetBrainstormingByStartRequest(s.ctx, in.PipelineID, in.RequestID)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.BrainstormRun{}, false, nil
	}
	if err != nil {
		return catalog.BrainstormRun{}, false, safe("read brainstorm request", err)
	}
	if run.StartClientIntentHash == "" || run.StartClientIntentHash != hash {
		return catalog.BrainstormRun{}, true, ErrPipelineRequestConflict
	}
	return run, true, nil
}

func (s *Service) StartBrainstorming(in StartBrainstormingInput) (BrainstormDTO, error) {
	if err := s.beginCall(); err != nil {
		return BrainstormDTO{}, err
	}
	defer s.endCall()
	if in.Selection.Executor == "codex_cli" {
		return BrainstormDTO{}, ErrSDDCLIReadIsolationUnavailable
	}
	if !validSelectionText(in.PipelineID, 128) || !authoringRequestID.MatchString(in.RequestID) || in.PipelineRevision < 1 || in.DiscoveryVersion < 1 || !validBrainstormIntentSelection(in.Selection) {
		return BrainstormDTO{}, ErrInvalidInput
	}
	intent := in
	intent.Selection = intentSelection(in.Selection)
	hash, err := brainstormIntentHash("start", intent)
	if err != nil {
		return BrainstormDTO{}, err
	}
	if run, found, err := s.resolveBrainstormStart(in, hash); found || err != nil {
		if err != nil {
			return BrainstormDTO{}, err
		}
		return brainstormDTO(run), nil
	}
	selectionInput := in.Selection
	selectionInput.ForSDD = true
	// Starting stores a question preference; generation chooses its own exact cap.
	selectionInput.MaxOutputTokens = 256
	pipeline, err := s.store.GetPipeline(s.ctx, in.PipelineID)
	if err != nil {
		return BrainstormDTO{}, safe("read brainstorm workspace", err)
	}
	choice, err := s.prepareSDDModelSelection(s.ctx, pipeline.WorkspaceID, selectionInput)
	if err != nil {
		return BrainstormDTO{}, safe("validate brainstorm model", err)
	}
	run, writeErr := s.store.StartBrainstorming(s.ctx, catalog.StartBrainstormingRequest{PipelineID: in.PipelineID, RequestID: in.RequestID, PipelineRevision: in.PipelineRevision, DiscoveryVersion: in.DiscoveryVersion, Selection: *choice, ClientIntentHash: hash})
	if writeErr != nil {
		recovered, found, err := s.resolveBrainstormStart(in, hash)
		if err != nil {
			return BrainstormDTO{}, err
		}
		if !found {
			return BrainstormDTO{}, safe("start brainstorm", writeErr)
		}
		run = recovered
	} else {
		run, err = s.store.GetBrainstorming(s.ctx, run.ID)
		if err != nil {
			return BrainstormDTO{}, safe("read started brainstorm", err)
		}
	}
	return brainstormDTO(run), nil
}

func (s *Service) GetBrainstorming(in GetBrainstormingInput) (BrainstormDTO, error) {
	if err := s.beginCall(); err != nil {
		return BrainstormDTO{}, err
	}
	defer s.endCall()
	var run catalog.BrainstormRun
	var err error
	switch {
	case validSelectionText(in.RunID, 128) && in.PipelineID == "" && in.DiscoveryVersion == 0:
		run, err = s.store.GetBrainstorming(s.ctx, in.RunID)
	case in.RunID == "" && validSelectionText(in.PipelineID, 128) && in.DiscoveryVersion >= 0:
		if in.DiscoveryVersion == 0 {
			pipeline, readErr := s.loadPipeline(in.PipelineID)
			if readErr != nil {
				return BrainstormDTO{}, readErr
			}
			in.DiscoveryVersion = pipeline.Artifacts[sdd.Discovery].Version
		}
		run, err = s.store.GetBrainstormingByPipeline(s.ctx, in.PipelineID, in.DiscoveryVersion)
	default:
		return BrainstormDTO{}, ErrInvalidInput
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return BrainstormDTO{}, ErrBrainstormNotFound
		}
		return BrainstormDTO{}, safe("read brainstorm", err)
	}
	return brainstormDTO(run), nil
}

func (s *Service) ListBrainstorming(in ListBrainstormingInput) ([]BrainstormDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	if !validSelectionText(in.PipelineID, 128) || !validSelectionText(in.WorkspaceID, 128) || in.Limit < 0 || in.Limit > maxBrainstormHistoryLimit {
		return nil, ErrInvalidInput
	}
	pipeline, err := s.store.GetPipeline(s.ctx, in.PipelineID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && pipeline.WorkspaceID != in.WorkspaceID) {
		return nil, ErrPipelineNotFound
	}
	if err != nil {
		return nil, safe("read brainstorm history pipeline", err)
	}
	runs, err := s.store.ListBrainstormingByPipeline(s.ctx, in.PipelineID, in.Limit)
	if err != nil {
		return nil, safe("list brainstorm history", err)
	}
	result := make([]BrainstormDTO, 0, len(runs))
	for _, run := range runs {
		result = append(result, brainstormDTO(run))
	}
	return result, nil
}

func (s *Service) AnswerBrainstormQuestion(in AnswerBrainstormQuestionInput) (BrainstormDTO, error) {
	if err := s.beginCall(); err != nil {
		return BrainstormDTO{}, err
	}
	defer s.endCall()
	if !validBrainstormRef(in.Ref) || !validSelectionText(in.QuestionID, 128) || strings.TrimSpace(in.QuestionID) == "" ||
		len(in.Answer) > 16*1024 || strings.TrimSpace(in.Answer) == "" || !utf8.ValidString(in.Answer) {
		return BrainstormDTO{}, ErrInvalidInput
	}
	intentHash, err := brainstormIntentHash("answer", in)
	if err != nil {
		return BrainstormDTO{}, err
	}
	run, err := s.store.AnswerBrainstormQuestion(s.ctx, catalog.AnswerBrainstormRequest{BrainstormRequest: in.Ref.request(), QuestionID: in.QuestionID, Answer: in.Answer, ClientIntentHash: intentHash})
	if err != nil {
		return BrainstormDTO{}, safe("answer brainstorm question", err)
	}
	return brainstormDTO(run), nil
}

func brainstormCommandIntent(in GenerateBrainstormInput, action string) (string, error) {
	if !validBrainstormRef(in.Ref) || !validBrainstormIntentSelection(in.Selection) || (action != "question" && action != "questions_sufficient" && action != "finish_synthesis") {
		return "", ErrInvalidInput
	}
	in.Selection = intentSelection(in.Selection)
	return brainstormIntentHash(action, in)
}

func (s *Service) resolveBrainstormCommand(in GenerateBrainstormInput, action, hash string) (catalog.BrainstormAttempt, bool, error) {
	receipt, err := s.store.GetBrainstormCommand(s.ctx, in.Ref.RunID, in.Ref.RequestID)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.BrainstormAttempt{}, false, nil
	}
	if err != nil {
		return catalog.BrainstormAttempt{}, false, safe("read brainstorm command", err)
	}
	if receipt.Action != action || receipt.ClientIntentHash == "" || receipt.ClientIntentHash != hash {
		return catalog.BrainstormAttempt{}, true, ErrPipelineRequestConflict
	}
	run, err := s.store.GetBrainstorming(s.ctx, in.Ref.RunID)
	if err != nil {
		return catalog.BrainstormAttempt{}, true, safe("read brainstorm attempt", err)
	}
	for _, attempt := range run.Attempts {
		if attempt.ID == receipt.AttemptID {
			return attempt, true, nil
		}
	}
	return catalog.BrainstormAttempt{}, true, safe("read brainstorm attempt", sql.ErrNoRows)
}

// Call only after complete serialized-prompt preflight. The caller must hold a
// service call lifetime and must never invoke a provider unless admitted=true.
// A commit followed by an uncertain response recovers read-only and returns false.
func (s *Service) admitBrainstormCommand(in GenerateBrainstormInput, action string, estimatedInputTokens int64) (catalog.BrainstormAttempt, bool, error) {
	hash, err := brainstormCommandIntent(in, action)
	if err != nil {
		return catalog.BrainstormAttempt{}, false, err
	}
	if attempt, found, err := s.resolveBrainstormCommand(in, action, hash); found || err != nil {
		return attempt, false, err
	}
	if estimatedInputTokens <= 0 || estimatedInputTokens > maxPromptBytes {
		return catalog.BrainstormAttempt{}, false, ErrInvalidInput
	}
	selectionInput := in.Selection
	selectionInput.ForSDD = true
	selectionInput.MaxOutputTokens = 256
	if action != "question" {
		selectionInput.MaxOutputTokens = 1024
	}
	run, err := s.store.GetBrainstorming(s.ctx, in.Ref.RunID)
	if err != nil {
		return catalog.BrainstormAttempt{}, false, safe("read brainstorm workspace", err)
	}
	pipeline, err := s.store.GetPipeline(s.ctx, run.PipelineID)
	if err != nil {
		return catalog.BrainstormAttempt{}, false, safe("read brainstorm workspace", err)
	}
	choice, err := s.prepareSDDModelSelection(s.ctx, pipeline.WorkspaceID, selectionInput)
	if err != nil {
		return catalog.BrainstormAttempt{}, false, safe("validate brainstorm model", err)
	}
	return s.admitPreparedBrainstormCommand(in, action, hash, *choice, estimatedInputTokens)
}

// The exact immutable selection used to serialize the prompt is admitted here.
// Fresh preflight belongs to the caller; this boundary never selects again.
func (s *Service) admitPreparedBrainstormCommand(in GenerateBrainstormInput, action, hash string, choice catalog.ModelSelection, estimatedInputTokens int64) (catalog.BrainstormAttempt, bool, error) {
	var attempt catalog.BrainstormAttempt
	var admitted bool
	var err error
	if action == "question" {
		attempt, admitted, err = s.store.BeginBrainstormQuestion(s.ctx, catalog.BeginBrainstormAttemptRequest{BrainstormRequest: in.Ref.request(), Kind: "question", Selection: choice, EstimatedInputTokens: estimatedInputTokens, ClientIntentHash: hash})
	} else {
		command := catalog.GenerateBrainstormSynthesisRequest{BrainstormRequest: in.Ref.request(), Selection: choice, EstimatedInputTokens: estimatedInputTokens, ClientIntentHash: hash}
		if action == "questions_sufficient" {
			attempt, admitted, err = s.store.QuestionsSufficient(s.ctx, command)
		} else {
			attempt, admitted, err = s.store.FinishAndGenerateSynthesis(s.ctx, command)
		}
	}
	if err != nil {
		recovered, found, readErr := s.resolveBrainstormCommand(in, action, hash)
		if readErr != nil {
			return catalog.BrainstormAttempt{}, false, readErr
		}
		if found {
			return recovered, false, nil
		}
		return catalog.BrainstormAttempt{}, false, safe("admit brainstorm command", err)
	}
	return attempt, admitted, nil
}
