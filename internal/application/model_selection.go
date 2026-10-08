package application

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/externalagent"
	"github.com/persioflexa/harflex/internal/modelcatalog"
)

type SessionModelSelectionDTO struct {
	SessionID                 string    `json:"sessionId"`
	BackendID                 string    `json:"backendId"`
	ModelID                   string    `json:"modelId"`
	ReasoningEffort           string    `json:"reasoningEffort"`
	SupportedReasoningEfforts []string  `json:"supportedReasoningEfforts,omitempty"`
	Source                    string    `json:"source"`
	Destination               string    `json:"destination"`
	Status                    string    `json:"status"`
	ConfirmUnverifiedManual   bool      `json:"confirmUnverifiedManual"`
	ConfirmUnfiltered         bool      `json:"confirmUnfiltered"`
	ConfirmJITLoad            bool      `json:"confirmJitLoad"`
	MaxOutputTokens           int       `json:"maxOutputTokens"`
	ContextLength             int       `json:"contextLength"`
	CheckedAt                 time.Time `json:"checkedAt"`
}

func (s *Service) GetSessionModelSelection(sessionID string) (SessionModelSelectionDTO, error) {
	if err := s.beginCall(); err != nil {
		return SessionModelSelectionDTO{}, err
	}
	defer s.endCall()
	if sessionID == "" {
		return SessionModelSelectionDTO{}, ErrInvalidInput
	}
	if _, err := s.store.GetSession(s.ctx, sessionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SessionModelSelectionDTO{}, ErrSessionNotFound
		}
		return SessionModelSelectionDTO{}, safe("read session model", err)
	}
	selected, err := s.store.GetSessionModelSelection(s.ctx, sessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SessionModelSelectionDTO{}, sql.ErrNoRows
		}
		return SessionModelSelectionDTO{}, safe("read session model", err)
	}
	return SessionModelSelectionDTO{SessionID: selected.SessionID, BackendID: selected.BackendID, ModelID: selected.ModelID, ReasoningEffort: selected.ReasoningEffort, SupportedReasoningEfforts: append([]string(nil), selected.SupportedReasoningEfforts...), Source: selected.Source, Destination: selected.Destination, Status: selected.Status, ConfirmUnverifiedManual: selected.ConfirmUnverifiedManual, ConfirmUnfiltered: selected.ConfirmUnfiltered, ConfirmJITLoad: selected.ConfirmJITLoad, MaxOutputTokens: selected.MaxOutputTokens, ContextLength: selected.ContextLength, CheckedAt: selected.CheckedAt}, nil
}

// APIModelSelectionInput binds an explicit catalog choice to admission. Public
// session creation continues to use its existing legacy/default behavior.
type APIModelSelectionInput struct {
	Executor                string    `json:"executor"`
	BackendID               string    `json:"backendId"`
	ProfileID               string    `json:"profileId"`
	ModelID                 string    `json:"modelId"`
	CatalogRevision         string    `json:"catalogRevision"`
	Source                  string    `json:"source"`
	Destination             string    `json:"destination"`
	CheckedAt               time.Time `json:"checkedAt"`
	CredentialToken         string    `json:"credentialToken"`
	ReasoningEffort         string    `json:"reasoningEffort"`
	ConfirmUnverifiedManual bool      `json:"confirmUnverifiedManual"`
	ConfirmUnfiltered       bool      `json:"confirmUnfiltered"`
	ConfirmJITLoad          bool      `json:"confirmJitLoad"`
	MaxOutputTokens         int       `json:"maxOutputTokens"`
	ForSDD                  bool      `json:"-"`
	// Revalidating marks the re-check of a selection a session already holds: the catalog is queried live again, so the
	// age of the original pick is not a condition (it is for a new pick).
	Revalidating bool `json:"-"`
}

const apiCatalogMaxAge = 5 * time.Minute
const pipelineRoleOutputTokens = 4096

var ErrSDDCLIReadIsolationUnavailable = errors.New("Codex CLI does not restrict reads to the SDD workspace")

func (s *Service) prepareAPIModelSelection(ctx context.Context, in APIModelSelectionInput) (*catalog.ModelSelection, error) {
	return s.prepareAPIModelSelectionWithPolicy(ctx, in, false)
}

// allowUnloadedJIT is used only when saving a model choice: saving is not an
// inference attempt and must not require or retain permission to load it.
func (s *Service) prepareAPIModelSelectionWithPolicy(ctx context.Context, in APIModelSelectionInput, allowUnloadedJIT bool) (*catalog.ModelSelection, error) {
	if !validSelectionText(in.ProfileID, 128) || !validSelectionText(in.ModelID, 512) || !validSelectionText(in.CatalogRevision, 128) || !validSelectionText(in.Destination, 512) || len(in.Source) > 64 || !utf8.ValidString(in.Source) || strings.IndexFunc(in.Source, unicode.IsControl) >= 0 || len(in.CredentialToken) != 64 || !validAPIReasoningEffort(in.ReasoningEffort) || in.MaxOutputTokens < 0 || in.CheckedAt.IsZero() || (!in.Revalidating && time.Since(in.CheckedAt) > apiCatalogMaxAge) || in.CheckedAt.After(time.Now().Add(time.Minute)) {
		return nil, ErrInvalidInput
	}
	if in.MaxOutputTokens > 32768 || (in.ForSDD && in.MaxOutputTokens == 0) {
		return nil, ErrInvalidInput
	}
	profile, err := s.store.GetProviderProfile(ctx, in.ProfileID)
	if err != nil || profile.Kind != "openai_compatible" || profileNetworkAccess(profile) != nil {
		return nil, ErrBackendChanged
	}
	if in.MaxOutputTokens > 0 && profile.ProviderType != "openai" && profile.ProviderType != "openrouter" && profile.ProviderType != "ollama" && profile.ProviderType != "lm_studio" {
		return nil, ErrBackendChanged
	}
	if in.ForSDD && profile.ProviderType == "generic" {
		return nil, ErrBackendChanged
	}
	result, err := s.QueryHTTPModelCatalog(ctx, HTTPModelCatalogQuery{ProfileID: in.ProfileID, Refresh: true})
	if err != nil {
		return nil, err
	}
	if result.ProfileRevision != in.CatalogRevision || result.BackendID != profile.ID || result.Source != in.Source || result.Destination != in.Destination || time.Since(result.CheckedAt) > apiCatalogMaxAge || result.CheckedAt.After(time.Now().Add(time.Minute)) {
		return nil, ErrBackendChanged
	}
	expectedToken := s.catalogSelectionToken(result.CredentialIdentity, profile.ID, in.CatalogRevision, in.Source, in.Destination, in.CheckedAt)
	if expectedToken == "" || !hmac.Equal([]byte(expectedToken), []byte(in.CredentialToken)) {
		return nil, ErrBackendChanged
	}
	selection := &catalog.ModelSelection{BackendID: profile.ID, ModelID: in.ModelID, ReasoningEffort: in.ReasoningEffort, CatalogRevision: in.CatalogRevision, Source: result.Source, Destination: result.Destination, CredentialIdentity: result.CredentialIdentity, CheckedAt: in.CheckedAt, MaxOutputTokens: in.MaxOutputTokens}
	if profile.ProviderType == "generic" && result.Status == modelcatalog.StatusUnsupported && result.ErrorCode == "catalog_unsupported" && len(result.Models) == 0 {
		if !in.ConfirmUnverifiedManual || in.ReasoningEffort != "" {
			return nil, ErrInvalidInput
		}
		selection.Source, selection.Status, selection.ConfirmUnverifiedManual = "generic_manual", "unverified_manual", true
		return selection, nil
	}
	if !result.Complete || result.Status != modelcatalog.StatusComplete || len(result.Models) == 0 {
		return nil, ErrBackendChanged
	}
	var selected *modelcatalog.Model
	for i := range result.Models {
		if result.Models[i].ID == in.ModelID && result.Models[i].BackendID == profile.ID && result.Models[i].Source == result.Source {
			selected = &result.Models[i]
			break
		}
	}
	if selected == nil || !selectedModelInCatalog(result, *selection) {
		return nil, ErrBackendChanged
	}
	selection.SupportedReasoningEfforts = append([]string(nil), selected.SupportedReasoningEfforts...)
	if !catalog.ValidAPIReasoningEffort(*selection) {
		return nil, ErrBackendChanged
	}
	selection.Status = "listed"
	selection.ContextLength = max(0, selected.ContextLength)
	if result.Source == "openrouter_general_unfiltered" {
		if !in.ConfirmUnfiltered {
			return nil, ErrInvalidInput
		}
		selection.Status, selection.ConfirmUnfiltered = "listed_unfiltered", true
	}
	if profile.ProviderType == "lm_studio" && selected.Loaded != nil && !*selected.Loaded {
		if !in.ConfirmJITLoad && !allowUnloadedJIT {
			return nil, ErrInvalidInput
		}
		selection.ConfirmJITLoad = in.ConfirmJITLoad
	}
	return selection, nil
}

func (s *Service) revalidateAPIModelSelection(ctx context.Context, selection catalog.ModelSelection, forSDD bool) error {
	_, err := s.revalidatedAPIModelSelection(ctx, selection, forSDD)
	return err
}

func (s *Service) revalidatedAPIModelSelection(ctx context.Context, selection catalog.ModelSelection, forSDD bool) (*catalog.ModelSelection, error) {
	// A session keeps the model it was started with for as long as it lives. What is checked before every prompt is the
	// live state (the profile, the credential, the catalog still listing the model, the context length), not how long ago the
	// person picked it: the pick must be fresh when it is made (prepareAPIModelSelection), and a conversation that waits
	// minutes for an approval or a follow-up must not stop working because of that wait.
	if selection.ModelID == "" || selection.CatalogRevision == "" || selection.Destination == "" || selection.CredentialIdentity == "" || selection.CheckedAt.IsZero() || selection.CheckedAt.After(time.Now().Add(time.Minute)) {
		return nil, ErrBackendChanged
	}
	if forSDD && selection.Status == "unverified_manual" {
		return nil, ErrBackendChanged
	}
	profile, err := s.store.GetProviderProfile(ctx, selection.BackendID)
	if err != nil || profileRevision(profile) != selection.CatalogRevision {
		return nil, ErrBackendChanged
	}
	source := selection.Source
	if selection.Status == "unverified_manual" {
		source = ""
	}
	storedToken := s.catalogSelectionToken(selection.CredentialIdentity, selection.BackendID, selection.CatalogRevision, source, selection.Destination, selection.CheckedAt)
	checked, err := s.prepareAPIModelSelection(ctx, APIModelSelectionInput{ProfileID: selection.BackendID, ModelID: selection.ModelID, CatalogRevision: selection.CatalogRevision, Source: source, Destination: selection.Destination, CheckedAt: selection.CheckedAt, CredentialToken: storedToken, ReasoningEffort: selection.ReasoningEffort, ConfirmUnverifiedManual: selection.ConfirmUnverifiedManual, ConfirmUnfiltered: selection.ConfirmUnfiltered, ConfirmJITLoad: selection.ConfirmJITLoad, MaxOutputTokens: selection.MaxOutputTokens, ForSDD: forSDD, Revalidating: true})
	if err != nil || checked.Status != selection.Status || checked.Source != selection.Source || checked.Destination != selection.Destination || (forSDD && checked.ContextLength != selection.ContextLength) {
		return nil, ErrBackendChanged
	}
	return checked, nil
}

func validSelectionText(value string, max int) bool {
	return value != "" && len(value) <= max && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validAPIReasoningEffort(value string) bool {
	return value == "" || (validSelectionText(value, 64) && strings.TrimSpace(value) == value)
}

func selectedModelInCatalog(result modelcatalog.Result, selection catalog.ModelSelection) bool {
	if !result.Complete || result.Status != modelcatalog.StatusComplete || result.ProfileRevision != selection.CatalogRevision || result.BackendID != selection.BackendID || result.Source != selection.Source {
		return false
	}
	for _, model := range result.Models {
		if model.ID != selection.ModelID || model.BackendID != selection.BackendID || model.Source != selection.Source {
			continue
		}
		if selection.ReasoningEffort == "" {
			return true
		}
		for _, effort := range model.SupportedReasoningEfforts {
			if effort == selection.ReasoningEffort {
				return true
			}
		}
	}
	return false
}

func (s *Service) prepareCLIModelSelection(in CreateSessionInput, workspace catalog.Workspace) (*catalog.ModelSelection, error) {
	return s.prepareCLIModelSelectionContext(s.ctx, in, workspace)
}

func (s *Service) prepareCLIModelSelectionContext(ctx context.Context, in CreateSessionInput, workspace catalog.Workspace) (*catalog.ModelSelection, error) {
	if in.ModelID == "" && in.ReasoningEffort == "" && in.CatalogRevision == "" {
		return nil, nil // Legacy/profile default path remains unchanged.
	}
	if !validSelectionText(in.ModelID, 512) || strings.HasPrefix(in.ModelID, "-") || !validSelectionText(in.CatalogRevision, 128) ||
		len(in.ReasoningEffort) > 64 || !utf8.ValidString(in.ReasoningEffort) || strings.IndexFunc(in.ReasoningEffort, unicode.IsControl) >= 0 || strings.ContainsAny(in.ReasoningEffort, " =") || strings.HasPrefix(in.ReasoningEffort, "-") {
		return nil, ErrInvalidInput
	}
	adapter, ok := s.external[in.BackendID]
	if !ok || adapter == nil || adapter.ID() != in.BackendID {
		return nil, ErrInvalidInput // API selection follows its own provider contract.
	}
	result, err := s.QueryCLIModelCatalog(ctx, CLIModelCatalogQuery{WorkspaceID: workspace.ID, BackendID: in.BackendID})
	if err != nil {
		return nil, err
	}
	selection := catalog.ModelSelection{BackendID: in.BackendID, ModelID: in.ModelID, ReasoningEffort: in.ReasoningEffort,
		CatalogRevision: in.CatalogRevision, LocalRevision: result.LocalRevision, Source: result.Source, WorkspacePath: workspace.Path, CheckedAt: result.CheckedAt}
	if !selectedModelInCatalog(result, selection) || in.BackendID == "opencode" && in.ReasoningEffort != "" {
		return nil, ErrBackendChanged
	}
	for _, model := range result.Models {
		if model.ID == selection.ModelID && model.BackendID == selection.BackendID && model.Source == selection.Source {
			selection.SupportedReasoningEfforts = append([]string(nil), model.SupportedReasoningEfforts...)
			break
		}
	}
	detected := adapter.Detect()
	currentRevision, revisionErr := cliCatalogRevision(in.BackendID, detected.Path, detected.Version, workspace.Path)
	if !detected.Available || revisionErr != nil || currentRevision != result.LocalRevision {
		return nil, ErrBackendChanged
	}
	selection.ExecutablePath, selection.ExecutableVersion = detected.Path, detected.Version
	selection.Status = "listed"
	for _, model := range result.Models {
		if model.ID == in.ModelID && model.BackendID == in.BackendID && model.Source == result.Source {
			selection.ContextLength = max(0, model.ContextLength)
			break
		}
	}
	return &selection, nil
}

// sddExecutorAdmitted says whether the executor named by an SDD selection may start. The Codex CLI
// stays refused until a runner proves it cannot read outside the SDD workspace.
func sddExecutorAdmitted(in APIModelSelectionInput) error {
	switch in.Executor {
	case "", "api":
		if in.BackendID != "" {
			return ErrInvalidInput
		}
		return nil
	case "codex_cli":
		return ErrSDDCLIReadIsolationUnavailable
	default:
		return ErrInvalidInput
	}
}

func (s *Service) prepareSDDModelSelection(ctx context.Context, workspaceID string, in APIModelSelectionInput) (*catalog.ModelSelection, error) {
	if err := sddExecutorAdmitted(in); err != nil {
		return nil, err
	}
	return s.prepareAPIModelSelection(ctx, in)
}

func (s *Service) preparePipelineRoleModelSelection(ctx context.Context, workspaceID, backendID string, in APIModelSelectionInput) (*catalog.ModelSelection, error) {
	if backendID == "" || !validSelectionText(in.ModelID, 512) || !validSelectionText(in.CatalogRevision, 128) || in.CheckedAt.IsZero() ||
		time.Since(in.CheckedAt) > apiCatalogMaxAge || in.CheckedAt.After(time.Now().Add(time.Minute)) || in.MaxOutputTokens != 0 {
		return nil, ErrInvalidInput
	}
	switch in.Executor {
	case "api":
		if documentCLI(backendID) || in.BackendID != "" || in.ProfileID != backendID {
			return nil, ErrInvalidInput
		}
		in.MaxOutputTokens = pipelineRoleOutputTokens
		in.ForSDD = true
		selection, err := s.prepareAPIModelSelection(ctx, in)
		if err != nil {
			return nil, err
		}
		if selection.BackendID != backendID {
			return nil, ErrBackendChanged
		}
		selection.MaxOutputTokens = pipelineRoleOutputTokens
		workspace, err := s.store.GetWorkspace(ctx, workspaceID)
		if err != nil {
			return nil, ErrWorkspaceNotFound
		}
		selection.WorkspacePath = workspace.Path
		return selection, nil
	case "cli", "codex_cli":
		if !s.codexHostModeSupported(backendID, "sdd_code") {
			return nil, ErrSDDCLIReadIsolationUnavailable
		}
		if in.BackendID != backendID || in.ProfileID != "" || in.Source != documentCLISources[backendID] || in.Destination != "" || in.CredentialToken != "" || in.ConfirmUnverifiedManual || in.ConfirmUnfiltered || in.ConfirmJITLoad {
			return nil, ErrInvalidInput
		}
		workspace, err := s.store.GetWorkspace(ctx, workspaceID)
		if err != nil {
			return nil, ErrWorkspaceNotFound
		}
		selection, err := s.prepareCLIModelSelectionContext(ctx, CreateSessionInput{BackendID: backendID, ModelID: in.ModelID, ReasoningEffort: in.ReasoningEffort, CatalogRevision: in.CatalogRevision}, workspace)
		if err != nil {
			return nil, err
		}
		selection.MaxOutputTokens = pipelineRoleOutputTokens
		return selection, nil
	default:
		return nil, ErrInvalidInput
	}
}

func (s *Service) sddSelectionEstimator(ctx context.Context, selection catalog.ModelSelection) (string, error) {
	if selection.BackendID == "codex" && selection.Source == "codex_app_server" {
		return "openai", nil
	}
	profile, err := s.store.GetProviderProfile(ctx, selection.BackendID)
	if err != nil || profileRevision(profile) != selection.CatalogRevision {
		return "", ErrBackendChanged
	}
	return profile.ProviderType, nil
}

type selectedCLIRunner struct {
	base            sessionRunner
	service         *Service
	selection       catalog.ModelSelection
	workspace       catalog.Workspace
	validateAttempt func(context.Context) error
	mu              sync.Mutex
	cancel          context.CancelFunc
	done            chan struct{}
	cancelled       bool
	sealed          bool
	timeout         time.Duration
}

func (r *selectedCLIRunner) Prompt(parent context.Context, text string) error {
	return r.execute(parent, func(ctx context.Context) error { return r.base.Prompt(ctx, text) })
}

func (r *selectedCLIRunner) Approve(parent context.Context, approvalID string, allow bool) error {
	base, ok := r.base.(approver)
	if !ok {
		return ErrApprovalUnsupported
	}
	return r.execute(parent, func(ctx context.Context) error { return base.Approve(ctx, approvalID, allow) })
}

func (r *selectedCLIRunner) execute(parent context.Context, action func(context.Context) error) error {
	r.mu.Lock()
	if r.sealed {
		r.mu.Unlock()
		return context.Canceled
	}
	if r.cancel != nil {
		r.mu.Unlock()
		return externalagent.ErrSessionBusy
	}
	ctx, cancel := context.WithCancel(parent)
	if r.timeout > 0 {
		cancel()
		ctx, cancel = context.WithTimeout(parent, r.timeout)
	}
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
	result, err := r.service.QueryCLIModelCatalog(ctx, CLIModelCatalogQuery{WorkspaceID: r.workspace.ID, BackendID: r.selection.BackendID})
	if err != nil {
		return err
	}
	if !selectedModelInCatalog(result, r.selection) {
		return ErrBackendChanged
	}
	if r.validateAttempt != nil {
		if err := r.validateAttempt(ctx); err != nil {
			return err
		}
	}
	return action(ctx)
}

func (r *selectedCLIRunner) Cancel() bool {
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

func (r *selectedCLIRunner) Abort(ctx context.Context) error {
	r.mu.Lock()
	r.sealed = true
	done := r.done
	if r.cancel != nil {
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
