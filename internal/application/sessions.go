package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/externalagent"
	"github.com/persioflexa/harflex/internal/id"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/secrets"
	"github.com/persioflexa/harflex/internal/security"
	"github.com/persioflexa/harflex/internal/tools"
)

var (
	ErrWorkspaceNotFound   = errors.New("workspace not found")
	ErrBackendNotFound     = errors.New("backend not found or unavailable")
	ErrBackendChanged      = errors.New("backend changed; create a new session")
	ErrSessionNotFound     = errors.New("session not found")
	ErrNoActiveRun         = errors.New("no active run")
	ErrApprovalUnsupported = errors.New("approval unsupported for external sessions")
	ErrAuditUnavailable    = errors.New("audit exporter unavailable")
)

type sessionRunner interface {
	Prompt(context.Context, string) error
	Cancel() bool
}
type approver interface {
	Approve(context.Context, string, bool) error
}

func (s *Service) ListBackends() []BackendDTO {
	if err := s.beginCall(); err != nil {
		return []BackendDTO{}
	}
	defer s.endCall()
	profiles, err := s.store.ListProviderProfiles(s.ctx)
	result := make([]BackendDTO, 0, len(profiles)+len(s.external))
	if err == nil {
		for _, p := range profiles {
			result = append(result, backendDTO(p))
		}
	}
	for key, adapter := range s.external {
		item := BackendDTO{ID: key, Name: key, Kind: "cli"}
		if key == "claude" {
			item.Name = "Claude Code"
		}
		if adapter != nil {
			detected := adapter.Detect()
			item.Available = detected.Available
			item.ProfessionalAvailable = codexDocumentContractAvailable(adapter, detected)
			item.Capabilities = adapter.Capabilities()
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// The configured model belongs to the selected profile, not a frontend prompt.
type modelProvider struct {
	agentcore.Provider
	model           string
	reasoningEffort string
}

type selectedAPIRunner struct {
	base            sessionRunner
	service         *Service
	selection       catalog.ModelSelection
	forSDD          bool
	snapshotBound   bool
	validateAttempt func(context.Context) error
	mu              sync.Mutex
	cancel          context.CancelFunc
	done            chan struct{}
	cancelled       bool
	sealed          bool
}

func (r *selectedAPIRunner) Prompt(parent context.Context, text string) error {
	return r.execute(parent, func(ctx context.Context) error { return r.base.Prompt(ctx, text) })
}

func (r *selectedAPIRunner) Approve(parent context.Context, approvalID string, allow bool) error {
	base, ok := r.base.(approver)
	if !ok {
		return ErrApprovalUnsupported
	}
	return r.execute(parent, func(ctx context.Context) error { return base.Approve(ctx, approvalID, allow) })
}

func (r *selectedAPIRunner) execute(parent context.Context, action func(context.Context) error) error {
	r.mu.Lock()
	if r.sealed {
		r.mu.Unlock()
		return context.Canceled
	}
	if r.cancel != nil {
		r.mu.Unlock()
		return agentcore.ErrSessionBusy
	}
	ctx, cancel := context.WithCancel(parent)
	r.cancel, r.cancelled = cancel, false
	r.done = make(chan struct{})
	r.mu.Unlock()
	defer func() {
		cancel()
		r.mu.Lock()
		r.cancel, r.cancelled = nil, false
		close(r.done)
		r.done = nil
		r.mu.Unlock()
	}()
	if r.snapshotBound {
		if r.validateAttempt == nil {
			return ErrBackendChanged
		}
	} else if err := r.service.revalidateAPIModelSelection(ctx, r.selection, r.forSDD); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	r.mu.Lock()
	stopped := r.cancelled || r.sealed
	r.mu.Unlock()
	if stopped || ctx.Err() != nil {
		return context.Canceled
	}
	if r.validateAttempt != nil {
		if err := r.validateAttempt(ctx); err != nil {
			return err
		}
	}
	return action(ctx)
}
func (r *selectedAPIRunner) Cancel() bool {
	r.mu.Lock()
	if r.cancel == nil || r.cancelled {
		r.mu.Unlock()
		return r.base.Cancel()
	}
	r.cancelled = true
	r.cancel()
	r.mu.Unlock()
	_ = r.base.Cancel()
	return true
}
func (r *selectedAPIRunner) Abort(ctx context.Context) error {
	r.mu.Lock()
	r.sealed = true
	done := r.done
	if r.cancel != nil {
		r.cancelled = true
		r.cancel()
	}
	r.mu.Unlock()
	var err error
	if aborter, ok := r.base.(interface{ Abort(context.Context) error }); ok {
		err = aborter.Abort(ctx)
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		}
	}
	return err
}

func (p modelProvider) Stream(ctx context.Context, r agentcore.ChatRequest) (<-chan agentcore.StreamEvent, <-chan error) {
	r.Model = p.model
	r.ReasoningEffort = p.reasoningEffort
	return p.Provider.Stream(ctx, r)
}

func (s *Service) CreateDirectSession(in CreateDirectSessionInput) (SessionDTO, error) {
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || len(reason) > 1000 || !utf8.ValidString(reason) || strings.ContainsAny(reason, "\x00\r\n") {
		return SessionDTO{}, ErrInvalidInput
	}
	return s.CreateSession(CreateSessionInput{WorkspaceID: in.WorkspaceID, BackendID: in.BackendID, AgentID: in.AgentID, BypassReason: reason,
		ModelID: in.ModelID, ReasoningEffort: in.ReasoningEffort, CatalogRevision: in.CatalogRevision})
}

func (s *Service) CreateSession(in CreateSessionInput) (SessionDTO, error) {
	return s.createSession(in, nil)
}

func (s *Service) createSession(in CreateSessionInput, delegation *delegationCreation) (SessionDTO, error) {
	return s.createSessionWithModelSelection(in, delegation, nil)
}

func (s *Service) createSessionWithModelSelection(in CreateSessionInput, delegation *delegationCreation, modelSelection *catalog.ModelSelection) (SessionDTO, error) {
	return s.createSessionWithExecutionRoot(in, delegation, modelSelection, "")
}

func (s *Service) createSessionWithExecutionRoot(in CreateSessionInput, delegation *delegationCreation, modelSelection *catalog.ModelSelection, executionRoot string) (SessionDTO, error) {
	if err := s.beginCall(); err != nil {
		return SessionDTO{}, err
	}
	defer s.endCall()
	s.recoveryGate.RLock()
	defer s.recoveryGate.RUnlock()
	validMode := in.Mode == "" || in.Mode == "evaluation" || in.Mode == "sdd_code"
	validExecutionRoot := executionRoot == "" && in.Mode != "sdd_code" || executionRoot != "" && (in.Mode == "sdd_code" || in.Mode == "evaluation")
	if strings.TrimSpace(in.WorkspaceID) == "" || (strings.TrimSpace(in.BackendID) == "" && strings.TrimSpace(in.AgentID) == "") || !validMode || !validExecutionRoot || len(in.BypassReason) > 1000 || !utf8.ValidString(in.BypassReason) || strings.ContainsAny(in.BypassReason, "\x00\r\n") || (in.Mode != "" && in.BypassReason != "") || (delegation != nil && (in.Mode != "" || in.BypassReason != "" || executionRoot != "")) {
		return SessionDTO{}, ErrInvalidInput
	}
	var agentSnapshot *catalog.AgentSnapshot
	if in.AgentID != "" {
		agent, err := s.store.GetAgent(s.ctx, in.AgentID)
		if errors.Is(err, sql.ErrNoRows) {
			return SessionDTO{}, ErrAgentNotFound
		}
		if err != nil {
			return SessionDTO{}, safe("get agent", err)
		}
		if in.BackendID != "" && in.BackendID != agent.BackendID {
			return SessionDTO{}, ErrInvalidInput
		}
		in.BackendID = agent.BackendID
		agentSnapshot = &catalog.AgentSnapshot{AgentID: agent.ID, Instructions: agent.Instructions, AllowedTools: append([]string(nil), agent.AllowedTools...), CreatedAt: time.Now().UTC()}
	}
	workspace, err := s.store.GetWorkspace(s.ctx, in.WorkspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionDTO{}, ErrWorkspaceNotFound
	}
	if err != nil {
		return SessionDTO{}, safe("get workspace", err)
	}
	selection := modelSelection
	if selection == nil {
		selection, err = s.prepareCLIModelSelection(in, workspace)
		if err != nil {
			return SessionDTO{}, err
		}
	} else if selection.BackendID != in.BackendID || selection.ModelID == "" {
		return SessionDTO{}, ErrInvalidInput
	}
	skillContent := ""
	if in.Mode == "" {
		skillContent, err = s.enabledSkillSnapshot(workspace.ID)
		if err != nil {
			return SessionDTO{}, err
		}
		// The conversation keeps the memory it started with, so reopening it later behaves the same.
		skillContent += projectMemoryInstructions(s.projectMemoryContext(s.ctx, workspace.ID))
	}
	now := time.Now().UTC()
	record := catalog.SessionRecord{ID: id.New(), WorkspaceID: workspace.ID, BackendID: in.BackendID, Mode: in.Mode, Status: "ready", CreatedAt: now, UpdatedAt: now}
	if selection != nil {
		selection.SessionID = record.ID
	}
	runner, journal, err := s.makeRunnerAt(&record, workspace, restoredHistory{}, false, agentSnapshot, skillContent, selection, executionRoot)
	if err != nil {
		return SessionDTO{}, err
	}
	if agentSnapshot != nil {
		agentSnapshot.SessionID = record.ID
	}
	var bypass json.RawMessage
	streamKind := "agent_session"
	if _, external := s.external[record.BackendID]; external && !isCodexHostSession(record) {
		streamKind = "external_session"
	}
	if in.BypassReason != "" {
		bypass, err = journal.redactor.eventPayload(map[string]string{"reason": in.BypassReason}, "sdd.bypassed")
		if err != nil {
			journal.Close()
			return SessionDTO{}, errRedactionUnavailable
		}
	}
	var storeErr error
	if delegation == nil {
		storeErr = s.store.CreateSessionWithSnapshots(s.ctx, record, agentSnapshot, skillContent, bypass, streamKind, selection)
	} else {
		delegation.link.ChildSessionID = record.ID
		payload, err := journal.redactor.eventPayload(map[string]string{"prompt": delegation.prompt}, "subagent.prompt.queued")
		if err != nil {
			journal.Close()
			return SessionDTO{}, errRedactionUnavailable
		}
		var task struct {
			Prompt string `json:"prompt"`
		}
		if err := json.Unmarshal(payload, &task); err != nil || task.Prompt == "" {
			journal.Close()
			return SessionDTO{}, errRedactionUnavailable
		}
		delegation.link.TaskPrompt = delegationTaskPreview(task.Prompt)
		delegation.created, storeErr = s.store.CreateDelegatedSessionWithSnapshots(s.ctx, record, agentSnapshot, skillContent, streamKind, delegation.link, payload, maxChildDelegations)
		if storeErr == nil && !delegation.created {
			journal.Close()
			link, err := s.store.GetDelegationByRequestID(s.ctx, delegation.link.ParentSessionID, delegation.link.RequestID)
			if err != nil {
				return SessionDTO{}, safe("read existing delegation", err)
			}
			if link.AgentID != delegation.link.AgentID || link.TaskHash != delegation.link.TaskHash {
				return SessionDTO{}, ErrDelegationRequestConflict
			}
			stored, err := s.store.GetSession(s.ctx, link.ChildSessionID)
			if err != nil {
				return SessionDTO{}, safe("read existing delegated session", err)
			}
			delegation.link = link
			return s.sessionDTO(stored), nil
		}
	}
	if storeErr != nil {
		journal.Close()
		return SessionDTO{}, safe("create session", storeErr)
	}
	s.mu.Lock()
	s.sessions[record.ID], s.journals[record.ID] = runner, journal
	s.mu.Unlock()
	return s.sessionDTO(record), nil
}

func professionalSDDMode(mode string) bool {
	switch mode {
	case "sdd_readonly", "sdd_code", "evaluation":
		return true
	default:
		return false
	}
}

func (s *Service) makeRunner(record *catalog.SessionRecord, workspace catalog.Workspace, history restoredHistory, reopening bool, agentSnapshot *catalog.AgentSnapshot, skillContent string, selection *catalog.ModelSelection) (sessionRunner, *eventJournal, error) {
	if record.Mode == catalog.AuthoringCodeSessionMode && reopening {
		return nil, nil, ErrInvalidInput
	}
	if record.Mode == catalog.AuthoringCodeSessionMode {
		if selection == nil {
			return nil, nil, ErrBackendChanged
		}
		if _, external := s.external[record.BackendID]; external {
			return nil, nil, ErrInvalidInput
		}
	}
	if professionalSDDMode(record.Mode) && (documentCLI(record.BackendID) || s.external[record.BackendID] != nil) && !s.codexHostModeSupported(record.BackendID, record.Mode) {
		return nil, nil, ErrSDDCLIReadIsolationUnavailable
	}
	executionRoot := ""
	if record.Mode == "sdd_code" || record.Mode == "evaluation" {
		root, err := s.executionRootForSession(record.ID)
		if err == nil {
			executionRoot = root
		} else if record.Mode == "sdd_code" || !errors.Is(err, sql.ErrNoRows) {
			return nil, nil, err
		}
	}
	return s.makeRunnerAt(record, workspace, history, reopening, agentSnapshot, skillContent, selection, executionRoot)
}

func (s *Service) makeRunnerAt(record *catalog.SessionRecord, workspace catalog.Workspace, history restoredHistory, reopening bool, agentSnapshot *catalog.AgentSnapshot, skillContent string, selection *catalog.ModelSelection, executionRoot string) (sessionRunner, *eventJournal, error) {
	if professionalSDDMode(record.Mode) && (documentCLI(record.BackendID) || s.external[record.BackendID] != nil) && !s.codexHostModeSupported(record.BackendID, record.Mode) {
		return nil, nil, ErrSDDCLIReadIsolationUnavailable
	}
	if reopening {
		stored, err := s.store.GetSessionModelSelection(s.ctx, record.ID)
		if err == nil {
			selection = &stored
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, nil, safe("get session model", err)
		}
	}
	if record.Mode == "sdd_readonly" && (selection == nil || selection.MaxOutputTokens <= 0 || selection.Status == "unverified_manual") {
		return nil, nil, ErrBackendChanged
	}
	if record.Mode == "sdd_code" && executionRoot == "" {
		return nil, nil, ErrPipelineCodeSnapshotUnavailable
	}
	if record.Mode == "sdd_readonly" {
		_, external := s.external[record.BackendID]
		codexSelection := selection != nil && documentCLI(record.BackendID) && selection.BackendID == record.BackendID && selection.Source == documentCLISources[record.BackendID] &&
			selection.Status == "listed" && selection.Destination == "" && selection.CredentialIdentity == "" && selection.MaxAssistantOutputBytes > 0 &&
			selection.MaxAssistantOutputBytes <= 64*1024 && !selection.ConfirmUnfiltered && !selection.ConfirmJITLoad && !selection.ConfirmUnverifiedManual
		if external != codexSelection {
			return nil, nil, ErrBackendChanged
		}
	}
	if record.Mode == "sdd_readonly" || record.Mode == "sdd_code" || record.Mode == catalog.AuthoringCodeSessionMode {
		agentSnapshot, skillContent = nil, ""
	}
	if agentSnapshot == nil && record.Mode != "sdd_readonly" && record.Mode != catalog.AuthoringCodeSessionMode {
		stored, err := s.store.GetSessionAgentSnapshot(s.ctx, record.ID)
		if err == nil {
			agentSnapshot = &stored
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, nil, safe("get agent snapshot", err)
		}
	}
	if reopening && record.Mode != "sdd_readonly" {
		stored, err := s.store.GetSessionSkillSnapshot(s.ctx, record.ID)
		if err == nil {
			skillContent = stored
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, nil, safe("get skill snapshot", err)
		}
	}
	journal := &eventJournal{store: s.store, service: s, redactor: newSensitiveRedactor()}
	retained := false
	defer func() {
		if !retained {
			journal.Close()
		}
	}()
	var mcpServers []catalog.MCPServer
	if record.Mode == "" {
		var err error
		mcpServers, err = s.store.ListMCPServers(s.ctx, workspace.ID)
		if err != nil {
			return nil, nil, safe("list session MCP servers", err)
		}
	}
	for _, server := range mcpServers {
		if !server.Enabled || server.CredentialProvider == "" || server.CredentialAccount == "" {
			continue
		}
		if s.secrets == nil {
			return nil, nil, errRedactionUnavailable
		}
		value, err := s.secrets.Get(s.ctx, secrets.Reference{Provider: server.CredentialProvider, Account: server.CredentialAccount})
		if err != nil {
			return nil, nil, safe("get session MCP credential", err)
		}
		for _, form := range mcpSecretForms(server.AuthScheme, value) {
			journal.redactor.patterns = append(journal.redactor.patterns, []byte(form))
		}
	}
	var runner sessionRunner
	if s.codexHostModeSupported(record.BackendID, record.Mode) {
		var err error
		runner, err = s.makeCodexHostRunner(record, workspace, history, selection, executionRoot, journal)
		if err != nil {
			return nil, nil, err
		}
	} else if adapter, ok := s.external[record.BackendID]; ok {
		if record.Mode == "evaluation" && record.BackendID != "codex" {
			return nil, nil, ErrInvalidInput
		}
		detected := externalagent.Detection{}
		if adapter != nil {
			detected = adapter.Detect()
		}
		if adapter == nil || !detected.Available {
			return nil, nil, ErrBackendNotFound
		}
		if selection != nil {
			currentRevision, revisionErr := cliCatalogRevision(record.BackendID, detected.Path, detected.Version, workspace.Path)
			if revisionErr != nil || selection.SessionID != record.ID || selection.BackendID != record.BackendID || selection.WorkspacePath != workspace.Path || selection.ExecutablePath != detected.Path || selection.ExecutableVersion != detected.Version || selection.LocalRevision != currentRevision {
				return nil, nil, ErrBackendChanged
			}
		}
		revision := "cli:" + adapter.ID()
		if record.BackendRevision != "" && record.BackendRevision != revision {
			return nil, nil, ErrBackendChanged
		}
		record.BackendRevision = revision
		instructions := skillContent
		if agentSnapshot != nil {
			instructions = agentSnapshot.Instructions + "\n" + skillContent
		}
		if record.Mode == "" {
			instructions = strings.TrimSpace(harflexChatInstructions + "\n\n" + instructions)
			if note := s.coordinatorInstructions(record.ID); note != "" {
				instructions = strings.TrimSpace(note + "\n\n" + instructions)
			}
		}
		if record.BackendID == "claude" && (record.Mode == "" || record.Mode == "sdd_code") {
			instructions = strings.TrimSpace(instructions + "\n\n" + claudePlanInstructions)
		}
		requestCWD := workspace.Path
		if executionRoot != "" {
			requestCWD = executionRoot
		}
		request := externalagent.Request{CWD: requestCWD, SessionID: history.externalID, SystemPrompt: instructions}
		if record.Mode == "" && (record.BackendID == "codex" || record.BackendID == "claude") {
			// Without the platform tools the chat still works; the agent just cannot reach Harflex.
			if url, token, err := s.platformMCPEndpoint(record.ID, workspace.ID); err == nil {
				request.HarflexMCPURL, request.HarflexMCPToken = url, token
			}
		}
		if record.Mode == "sdd_code" {
			request.Sandbox = "workspace-write"
			request.IgnoreUserConfig = true
			request.ApproveForMe = true
			request.Policy = "sdd_code"
			request.MaxAssistantOutputBytes = 64 * 1024
		}
		if record.Mode == "evaluation" {
			request.Sandbox = "read-only"
			request.IgnoreUserConfig = true
			request.Ephemeral = true
			request.MaxAssistantOutputBytes = 64 * 1024
			request.SessionID = ""
		}
		if selection != nil {
			request.Model, request.ReasoningEffort = selection.ModelID, selection.ReasoningEffort
			if record.Mode == "sdd_readonly" {
				request.Sandbox = "read-only"
				request.IgnoreUserConfig = true
				request.Ephemeral = true
				request.MaxAssistantOutputBytes = selection.MaxAssistantOutputBytes
			}
		}
		runner = externalagent.RestoreSession(record.ID, adapter, journal, request, history.used)
		if selection != nil {
			runner = &selectedCLIRunner{base: runner, service: s, selection: *selection, workspace: workspace}
		}
	} else {
		if selection != nil {
			if selection.SessionID != record.ID || selection.BackendID != record.BackendID || selection.WorkspacePath != workspace.Path || !validAPIReasoningEffort(selection.ReasoningEffort) || selection.Status == "" {
				return nil, nil, ErrInvalidInput
			}
			if record.Mode == catalog.AuthoringCodeSessionMode {
				if selection.Status == "unverified_manual" || selection.MaxOutputTokens <= 0 {
					return nil, nil, ErrBackendChanged
				}
			} else if err := s.revalidateAPIModelSelection(s.ctx, *selection, record.Mode == "sdd_readonly" || record.Mode == "sdd_code" || record.Mode == "evaluation"); err != nil {
				return nil, nil, err
			}
		}
		s.profileGate.RLock()
		profileGateHeld := true
		releaseProfileGate := func() {
			if profileGateHeld {
				s.profileGate.RUnlock()
				profileGateHeld = false
			}
		}
		defer releaseProfileGate()
		profile, err := s.store.GetProviderProfile(s.ctx, record.BackendID)
		if err == nil && record.Mode == catalog.AuthoringCodeSessionMode {
			base, validBase := parseProfileURL(profile.BaseURL)
			if selection == nil || !validBase || profileRevision(profile) != selection.CatalogRevision ||
				canonicalProfileOrigin(base) != selection.Destination {
				releaseProfileGate()
				return nil, nil, ErrBackendChanged
			}
		}
		if err == nil && reopening && !matchesSessionProfile(*record, profile) {
			releaseProfileGate()
			return nil, nil, ErrBackendChanged
		}
		if err == nil {
			if accessErr := profileNetworkAccess(profile); accessErr != nil {
				releaseProfileGate()
				return nil, nil, accessErr
			}
		}
		var credential string
		hasCredential := profile.CredentialProvider != "" && profile.CredentialAccount != ""
		if err == nil && hasCredential {
			if s.secrets == nil {
				err = errRedactionUnavailable
			} else {
				credential, err = s.secrets.Get(s.ctx, secrets.Reference{Provider: profile.CredentialProvider, Account: profile.CredentialAccount})
			}
		}
		if errors.Is(err, sql.ErrNoRows) {
			releaseProfileGate()
			return nil, nil, ErrBackendNotFound
		}
		if err != nil {
			releaseProfileGate()
			return nil, nil, safe("get backend", err)
		}
		if profile.Kind != "openai_compatible" {
			releaseProfileGate()
			return nil, nil, ErrBackendNotFound
		}
		if record.Mode == catalog.AuthoringCodeSessionMode {
			current, currentErr := s.store.GetProviderProfile(s.ctx, record.BackendID)
			base, validBase := parseProfileURL(current.BaseURL)
			if currentErr != nil || selection == nil || !validBase || current.ID != profile.ID || profileRevision(current) != selection.CatalogRevision ||
				canonicalProfileOrigin(base) != selection.Destination {
				releaseProfileGate()
				return nil, nil, ErrBackendChanged
			}
		} else {
			releaseProfileGate()
		}
		record.BackendRevision = profileRevision(profile)
		if hasCredential {
			if credential == "" {
				return nil, nil, errRedactionUnavailable
			}
			keyBytes := []byte(credential)
			journal.redactor.patterns = append(journal.redactor.patterns, append([]byte(nil), keyBytes...))
			journal.modelCredentialIndex = len(journal.redactor.patterns) - 1
			journal.hasModelCredential = true
			clear(keyBytes)
			credential = ""
		}
		provider, err := s.providerFactory(openai.Config{ID: profile.ID, ProviderType: profile.ProviderType, BaseURL: profile.BaseURL, APIKey: func(ctx context.Context) (string, error) {
			s.profileGate.RLock()
			defer s.profileGate.RUnlock()
			if err := ctx.Err(); err != nil {
				return "", err
			}
			current, err := s.store.GetProviderProfile(ctx, profile.ID)
			if errors.Is(err, sql.ErrNoRows) {
				return "", ErrBackendChanged
			}
			if err != nil {
				return "", safe("read backend revision", err)
			}
			if current.ID != profile.ID || current.Kind != profile.Kind || current.ProviderType != profile.ProviderType || current.BaseURL != profile.BaseURL || current.Model != profile.Model || current.CredentialProvider != profile.CredentialProvider || current.CredentialAccount != profile.CredentialAccount || !current.UpdatedAt.Equal(profile.UpdatedAt) {
				return "", ErrBackendChanged
			}
			if !hasCredential {
				return "", nil
			}
			return journal.credential()
		}})
		releaseProfileGate()
		if err != nil {
			return nil, nil, safe("create provider", err)
		}
		var toolItems []tools.Tool
		if record.Mode == catalog.AuthoringCodeSessionMode {
			guard, err := tools.NewPathGuard(workspace.Path)
			if err != nil {
				return nil, nil, safe("open private Code tools", err)
			}
			toolItems = []tools.Tool{tools.NewReadTool(guard), tools.NewWriteTool(guard), tools.NewEditTool(guard)}
		} else if record.Mode != "sdd_readonly" {
			toolRoot := workspace.Path
			if executionRoot != "" {
				toolRoot = executionRoot
			}
			guard, err := tools.NewPathGuard(toolRoot)
			if err != nil {
				return nil, nil, safe("open workspace tools", err)
			}
			toolItems = []tools.Tool{tools.NewReadTool(guard), tools.NewListTool(guard), tools.NewFindTool(guard), tools.NewGrepTool(guard)}
			if record.Mode == "" || record.Mode == "evaluation" {
				toolItems = append(toolItems, knowledgeSearchTool{service: s, workspaceID: workspace.ID})
			}
			if record.Mode == "sdd_code" {
				toolItems = append(toolItems, tools.NewWriteTool(guard), tools.NewEditTool(guard), tools.NewPlanTool())
			} else if record.Mode == "evaluation" && isQALabRoot(toolRoot) {
				toolItems = append(toolItems, tools.NewShellTool(tools.ShellConfig{CWD: toolRoot, DefaultTimeout: 10 * time.Minute}), tools.NewPlanTool())
			} else if record.Mode == "" {
				toolItems = append(toolItems, tools.NewWriteTool(guard), tools.NewEditTool(guard), tools.NewShellTool(tools.ShellConfig{CWD: workspace.Path}), tools.NewPlanTool())
				toolItems = append(toolItems, s.harflexTools(workspace.ID, record.ID)...)
				if pipelineID := s.pullRequestPipelineFor(record.ID, workspace.ID); pipelineID != "" {
					toolItems = append(toolItems, s.pullRequestTools(pipelineID, record.ID)...)
				}
				for _, server := range mcpServers {
					if !server.Enabled {
						continue
					}
					for _, tool := range server.Tools {
						toolItems = append(toolItems, mcpTool{service: s, server: server, tool: tool})
					}
				}
			}
		}
		if agentSnapshot != nil {
			allowed := make(map[string]bool, len(agentSnapshot.AllowedTools))
			for _, name := range agentSnapshot.AllowedTools {
				allowed[name] = true
			}
			filtered := make([]tools.Tool, 0, len(toolItems))
			for _, item := range toolItems {
				// The plan changes nothing and the Harflex tools act on the platform, not the project's files, so an agent
				// with a restricted tool list keeps them.
				if allowed[item.Spec().Name] || item.Spec().Name == tools.PlanToolName || strings.HasPrefix(item.Spec().Name, "harflex_") {
					filtered = append(filtered, item)
				}
			}
			toolItems = filtered
		}
		registry, err := tools.NewRegistry(toolItems...)
		if err != nil {
			return nil, nil, safe("create workspace tools", err)
		}
		instructions := skillContent
		if agentSnapshot != nil {
			instructions = agentSnapshot.Instructions + "\n" + skillContent
		}
		if record.Mode == "" {
			instructions = strings.TrimSpace(harflexChatInstructions + "\n\n" + instructions)
			if note := s.coordinatorInstructions(record.ID); note != "" {
				instructions = strings.TrimSpace(note + "\n\n" + instructions)
			}
		}
		if instructions != "" {
			history.messages = append([]agentcore.Message{{Role: agentcore.RoleSystem, Content: instructions}}, history.messages...)
		}
		model := profile.Model
		if selection != nil {
			model = selection.ModelID
		}
		maxOutputTokens := 0
		reasoningEffort := ""
		if selection != nil {
			maxOutputTokens = selection.MaxOutputTokens
			reasoningEffort = selection.ReasoningEffort
		}
		policyProfile := pipelinePolicyProfile(record.Mode, security.Profile(workspace.Profile))
		if record.Mode == catalog.AuthoringCodeSessionMode {
			policyProfile = security.TrustedWorkspace
		}
		localRunner, err := agentcore.RestoreSessionWithLimit(record.ID, modelProvider{Provider: provider, model: model, reasoningEffort: reasoningEffort}, registry, journal, policyProfile, history.messages, maxOutputTokens)
		if err != nil {
			return nil, nil, ErrSessionCorrupt
		}
		localRunner.SetPolicyGuard(func(ctx context.Context, risk security.Risk, action func(security.Decision) error) error {
			s.workspacePolicyGate.RLock()
			defer s.workspacePolicyGate.RUnlock()
			current, err := s.store.GetWorkspace(ctx, workspace.ID)
			if err != nil {
				return safe("read workspace policy", err)
			}
			profile := pipelinePolicyProfile(record.Mode, security.Profile(current.Profile))
			if record.Mode == catalog.AuthoringCodeSessionMode {
				profile = security.TrustedWorkspace
			}
			decision := security.Decide(security.Request{Profile: profile, Risk: risk, WithinRoot: true, SandboxReady: false})
			return action(decision)
		})
		runner = localRunner
		if selection != nil {
			runner = &selectedAPIRunner{base: runner, service: s, selection: *selection, forSDD: record.Mode == "sdd_readonly" || record.Mode == "sdd_code" || record.Mode == "evaluation", snapshotBound: record.Mode == catalog.AuthoringCodeSessionMode}
		}
	}
	for _, message := range history.messages {
		for _, call := range message.ToolCalls {
			journal.redactor.aliases.tools[call.ID] = toolAlias{public: call.ID, done: true}
			journal.redactor.aliases.actualTools[call.ID] = call.ID
		}
	}
	retained = true
	return runner, journal, nil
}

func (s *Service) runner(id string) (sessionRunner, error) {
	s.mu.RLock()
	runner, ok := s.sessions[id]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrSessionNotFound
	}
	return runner, nil
}

// Prompt blocks for the whole run while events stream through the emitter.
func (s *Service) Prompt(in PromptInput) (RunResultDTO, error) {
	if err := s.beginCall(); err != nil {
		return RunResultDTO{}, err
	}
	defer s.endCall()
	if strings.TrimSpace(in.Text) == "" || len(in.Text) > maxPromptBytes || !utf8.ValidString(in.Text) {
		return RunResultDTO{}, ErrInvalidInput
	}
	runner, err := s.runner(in.SessionID)
	if err != nil {
		return RunResultDTO{}, ErrSessionNotFound
	}
	if _, internal := runner.(*sddAttemptRunner); internal {
		return RunResultDTO{}, ErrInvalidInput
	}
	record, err := s.store.GetSession(s.ctx, in.SessionID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return RunResultDTO{}, safe("get session", err)
	}
	if record.Mode == "sdd_readonly" || record.Mode == catalog.AuthoringCodeSessionMode {
		return RunResultDTO{}, ErrInvalidInput
	}
	if record.Mode == "sdd_code" {
		active, err := s.pipelineCodeSessionActive(record.ID)
		if err != nil {
			return RunResultDTO{}, err
		}
		if !active {
			return RunResultDTO{}, ErrPipelineCodeSessionClosed
		}
	}
	_, delegated, allowed, err := s.store.ReserveDelegationPrompt(s.ctx, in.SessionID, maxDelegatedPrompts)
	if err != nil {
		return RunResultDTO{}, safe("reserve delegated prompt", err)
	}
	if !allowed {
		return RunResultDTO{}, ErrDelegationBudgetExceeded
	}
	runCtx := s.ctx
	if delegated {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(s.ctx, s.delegationTimeout)
		defer cancel()
	}
	runErr := runner.Prompt(runCtx, in.Text)
	if delegated && (errors.Is(runErr, agentcore.ErrSessionBusy) || errors.Is(runErr, externalagent.ErrSessionBusy) || errors.Is(runErr, externalagent.ErrNotResumable) || errors.Is(runErr, ErrBackendChanged) || errors.Is(runErr, ErrProviderEndpointBlocked)) {
		if err := s.store.ReleaseDelegationPrompt(s.ctx, in.SessionID); err != nil {
			return RunResultDTO{}, safe("release refused delegated prompt", err)
		}
	}
	return s.sessionResult(in.SessionID, "prompt", runErr)
}
func (s *Service) Approve(in ApprovalInput) (RunResultDTO, error) {
	if err := s.beginCall(); err != nil {
		return RunResultDTO{}, err
	}
	defer s.endCall()
	if strings.TrimSpace(in.ApprovalID) == "" {
		return RunResultDTO{}, ErrInvalidInput
	}
	runner, err := s.runner(in.SessionID)
	if err != nil {
		return RunResultDTO{}, err
	}
	a, ok := runner.(approver)
	if !ok {
		return RunResultDTO{}, ErrApprovalUnsupported
	}
	s.mu.RLock()
	journal := s.journals[in.SessionID]
	s.mu.RUnlock()
	if journal == nil {
		return RunResultDTO{}, agentcore.ErrApprovalNotFound
	}
	journal.mu.Lock()
	actual := ""
	if journal.redactor != nil && !journal.redactor.closed && journal.redactor.aliases.activeApprovals[in.ApprovalID] {
		actual = journal.redactor.aliases.actualApprovals[in.ApprovalID]
	}
	journal.mu.Unlock()
	if actual == "" {
		return RunResultDTO{}, agentcore.ErrApprovalNotFound
	}
	runCtx := s.ctx
	if _, err := s.store.GetParentDelegation(s.ctx, in.SessionID); err == nil {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(s.ctx, s.delegationTimeout)
		defer cancel()
	} else if !errors.Is(err, sql.ErrNoRows) {
		return RunResultDTO{}, safe("read delegated approval budget", err)
	}
	return s.sessionResult(in.SessionID, "approve", a.Approve(runCtx, actual, in.Allow))
}

func (s *Service) sessionResult(sessionID, operation string, runErr error) (RunResultDTO, error) {
	result, err := runResult(operation, runErr)
	if err != nil || result.Approval == nil {
		return result, err
	}
	s.mu.RLock()
	journal := s.journals[sessionID]
	s.mu.RUnlock()
	if journal == nil {
		return RunResultDTO{}, errRedactionUnavailable
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.redactor == nil {
		return RunResultDTO{}, errRedactionUnavailable
	}
	approval := result.Approval
	public := journal.redactor.aliases.approvals[approval.ID]
	tool := journal.redactor.aliases.tools[approval.ToolCallID].public
	if public == "" || tool == "" {
		return RunResultDTO{}, errRedactionUnavailable
	}
	arguments, err := journal.redactor.payload(approval.Arguments, false)
	if err != nil {
		return RunResultDTO{}, err
	}
	approval.ID, approval.ToolCallID, approval.Arguments = public, tool, arguments
	approval.Name = string(redactAuditBytes([]byte(approval.Name), journal.redactor.patterns))
	if !safeLiveEnum("approval.requested", "risk", approval.Risk) {
		approval.Risk = string(redactAuditBytes([]byte(approval.Risk), journal.redactor.patterns))
	}
	return result, nil
}
func (s *Service) Cancel(sessionID string) error {
	if err := s.beginCall(); err != nil {
		return err
	}
	defer s.endCall()
	runner, err := s.runner(sessionID)
	if err != nil {
		return err
	}
	if codeRunner, ok := runner.(*selectedAPIRunner); ok && codeRunner.snapshotBound {
		return ErrInvalidInput
	}
	var cancelled bool
	if internal, ok := runner.(*authoringStageRunner); ok {
		ref := internal.ref
		_, err := s.CancelAuthoringStage(AuthoringStageDecisionInput{Ref: AuthoringStageRefInput{PipelineID: ref.PipelineID, Stage: string(ref.Stage), RequestID: "session_cancel_" + internal.attempt.ID, PipelineRevision: ref.PipelineRevision, StageRevision: ref.StageRevision, DiscoveryVersion: ref.DiscoveryVersion, ArtifactVersion: ref.ArtifactVersion}, AttemptID: internal.attempt.ID})
		if err != nil {
			return err
		}
		cancelled = true
	} else {
		cancelled = runner.Cancel()
	}
	childrenCancelled, err := s.cancelDelegatedRuns(sessionID, 0)
	if err != nil {
		return safe("cancel delegated runs", err)
	}
	if !cancelled && !childrenCancelled {
		return ErrNoActiveRun
	}
	return nil
}

func (s *Service) cancelDelegatedRuns(parentSessionID string, depth int) (bool, error) {
	if depth >= maxDelegationDepth {
		return false, nil
	}
	links, err := s.store.ListDelegations(s.ctx, parentSessionID)
	if err != nil {
		return false, err
	}
	cancelled := false
	for _, link := range links {
		if runner, err := s.runner(link.ChildSessionID); err == nil && runner.Cancel() {
			cancelled = true
		}
		nested, err := s.cancelDelegatedRuns(link.ChildSessionID, depth+1)
		if err != nil {
			return cancelled, err
		}
		cancelled = cancelled || nested
	}
	return cancelled, nil
}
func (s *Service) ListEvents(in ListEventsInput) ([]EventDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	if in.AfterSequence < 0 || in.Limit < 0 || in.Limit > 1000 {
		return nil, ErrInvalidInput
	}
	if _, err := s.store.GetSession(s.ctx, in.SessionID); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	} else if err != nil {
		return nil, safe("get session", err)
	}
	limit := in.Limit
	if limit == 0 {
		limit = 200
	}
	items, err := s.store.ListAfterLimit(s.ctx, in.SessionID, in.AfterSequence, limit)
	if err != nil {
		return nil, safe("list events", err)
	}
	result := make([]EventDTO, len(items))
	for i, item := range items {
		result[i] = eventDTO(item)
	}
	return result, nil
}
func (s *Service) ExportAudit(in ExportAuditInput) (string, error) {
	if err := s.beginCall(); err != nil {
		return "", err
	}
	defer s.endCall()
	if strings.TrimSpace(in.Destination) == "" {
		return "", ErrInvalidInput
	}
	if _, err := s.store.GetSession(s.ctx, in.SessionID); errors.Is(err, sql.ErrNoRows) {
		return "", ErrSessionNotFound
	} else if err != nil {
		return "", safe("get session", err)
	}
	if s.audit == nil {
		return "", ErrAuditUnavailable
	}
	result, err := s.audit.Export(s.ctx, in)
	return result, safe("export audit", err)
}

func eventDTO(e events.Event) EventDTO {
	return EventDTO{e.ID, e.StreamID, e.Sequence, e.Type, append(json.RawMessage(nil), e.Data...), e.CreatedAt}
}

// SetEmitter is configured by the composition root before the desktop starts.
func SetEmitter(s *Service, emit func(string, any)) { s.mu.Lock(); s.emit = emit; s.mu.Unlock() }

type eventJournal struct {
	store                Store
	service              *Service
	mu                   sync.Mutex
	redactor             *sensitiveRedactor
	modelCredentialIndex int
	hasModelCredential   bool
	streamID             string
	streamKind           string
	outbox               []events.Event
	emitting             bool
	fault                error
}

func (j *eventJournal) ListAfter(ctx context.Context, id string, after int64) ([]events.Event, error) {
	return j.store.ListAfter(ctx, id, after)
}
func (j *eventJournal) Append(ctx context.Context, id, kind, typ string, data any) (events.Event, error) {
	j.mu.Lock()
	defer j.unlockAndDispatch()
	if j.fault != nil {
		return events.Event{}, j.fault
	}
	if j.redactor == nil {
		j.redactor = newSensitiveRedactor()
	}
	if err := j.bindStream(id, kind); err != nil {
		return events.Event{}, err
	}
	redacted, err := j.redactor.eventPayload(data, typ)
	if err != nil {
		return events.Event{}, err
	}
	if typ != "assistant.delta" {
		if err := j.flush(ctx, id, kind); err != nil {
			return events.Event{}, err
		}
	}
	if err := j.flushToolsBefore(ctx, id, kind, typ, redacted); err != nil {
		return events.Event{}, err
	}
	event, err := j.store.Append(ctx, id, kind, typ, redacted)
	if err != nil {
		return events.Event{}, err
	}
	j.outbox = append(j.outbox, event)
	if err := j.updateCatalog(ctx, event); err != nil {
		j.fault = safe("update session status", err)
		return events.Event{}, j.fault
	}
	return event, nil
}

func (j *eventJournal) bindStream(id, kind string) error {
	if j.streamID != "" && (j.streamID != id || j.streamKind != kind) {
		return errRedactionUnavailable
	}
	j.streamID, j.streamKind = id, kind
	return nil
}

// Shutdown runs only after all admitted operations have drained. If a journal
// is unavailable, closing still clears every private buffer and alias mapping.
func (j *eventJournal) shutdown(ctx context.Context) error {
	j.mu.Lock()
	defer j.unlockAndDispatch()
	if j.redactor == nil {
		return nil
	}
	defer j.redactor.close()
	if j.redactor.closed || j.streamID == "" {
		return nil
	}
	if err := j.flush(ctx, j.streamID, j.streamKind); err != nil {
		return err
	}
	return j.flushToolsBefore(ctx, j.streamID, j.streamKind, "run.cancelled", nil)
}

func (j *eventJournal) flush(ctx context.Context, id, kind string) error {
	if len(j.redactor.assistant.pending) == 0 {
		return nil
	}
	text := j.redactor.assistant.flush(j.redactor.patterns)
	event, err := j.store.Append(ctx, id, kind, "assistant.delta", map[string]string{"delta": text})
	if err != nil {
		return err
	}
	j.outbox = append(j.outbox, event)
	return nil
}

func (j *eventJournal) flushToolsBefore(ctx context.Context, id, kind, typ string, data json.RawMessage) error {
	switch typ {
	case "tool.completed", "tool.failed", "tool.skipped", "tool.denied":
		var value struct {
			ToolCallID string `json:"toolCallId"`
		}
		if err := json.Unmarshal(data, &value); err != nil {
			return errRedactionUnavailable
		}
		return j.flushTool(ctx, id, kind, value.ToolCallID)
	case "run.completed", "run.failed", "run.cancelled", "run.interrupted":
		calls := make([]string, 0, len(j.redactor.tools))
		for call := range j.redactor.tools {
			calls = append(calls, call)
		}
		sort.Strings(calls)
		for _, call := range calls {
			if err := j.flushTool(ctx, id, kind, call); err != nil {
				return err
			}
		}
	}
	return nil
}

func (j *eventJournal) flushTool(ctx context.Context, id, kind, call string) error {
	buffer := j.redactor.tools[call]
	if buffer == nil {
		return nil
	}
	defer delete(j.redactor.tools, call)
	if len(buffer.pending) == 0 {
		return nil
	}
	text := buffer.flush(j.redactor.patterns)
	event, err := j.store.Append(ctx, id, kind, "tool.updated", map[string]string{"toolCallId": call, "stream": buffer.stream, "text": text})
	if err != nil {
		return err
	}
	j.outbox = append(j.outbox, event)
	return nil
}

// Queue under the persistence lock, but invoke external callbacks without it.
// A reentrant append joins the same ordered queue instead of waiting on itself.
func (j *eventJournal) unlockAndDispatch() {
	j.mu.Unlock()
	j.mu.Lock()
	if j.emitting {
		j.mu.Unlock()
		return
	}
	j.emitting = true
	for len(j.outbox) > 0 {
		event := j.outbox[0]
		j.outbox[0] = events.Event{}
		j.outbox = j.outbox[1:]
		j.mu.Unlock()
		j.emit(event)
		j.mu.Lock()
	}
	j.outbox = nil
	j.emitting = false
	j.mu.Unlock()
}

func (j *eventJournal) credential() (string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.redactor == nil || j.redactor.closed || !j.hasModelCredential || j.modelCredentialIndex < 0 || j.modelCredentialIndex >= len(j.redactor.patterns) {
		return "", errRedactionUnavailable
	}
	return string(j.redactor.patterns[j.modelCredentialIndex]), nil
}

func (j *eventJournal) Close() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.redactor != nil {
		j.redactor.close()
	}
}

func (j *eventJournal) emit(event events.Event) {
	j.service.mu.RLock()
	emit := j.service.emit
	j.service.mu.RUnlock()
	if emit != nil {
		emit("harflex:event", eventDTO(event))
	}
}
