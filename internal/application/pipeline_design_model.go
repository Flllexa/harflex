package application

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/externalagent"
	"github.com/persioflexa/harflex/internal/sdd"
)

var ErrPipelineDesignModelRequired = errors.New("configure a default model or choose a model for this document")
var ErrPipelineDesignExecutorUnavailable = errors.New("the configured executor cannot generate documents without tools")
var ErrPipelineDesignSpecStale = errors.New("update SPEC against the current Discovery before preparing Plan")

// ErrPipelineDesignPhaseExecutor says a document phase could not start on the executor the project chose for it.
var ErrPipelineDesignPhaseExecutor = errors.New("the executor chosen for this phase cannot write its document")

// phaseExecutorError says which phase it was, so the screen can name it.
type phaseExecutorError struct{ stage sdd.Stage }

func (e phaseExecutorError) Error() string {
	return "the executor chosen for " + string(e.stage) + " cannot write its document"
}
func (e phaseExecutorError) Is(target error) bool { return target == ErrPipelineDesignPhaseExecutor }

func pipelineDesignStagesFor(target string, workspace catalog.PipelineDesignWorkspace) []sdd.Stage {
	switch target {
	case "discovery":
		return designStages()
	case "spec":
		return []sdd.Stage{sdd.Spec, sdd.Plan}
	case "plan":
		return []sdd.Stage{sdd.Plan}
	case "all":
		if workspace.Documents[sdd.Spec].Content == "" && workspace.Documents[sdd.Plan].Content == "" {
			return []sdd.Stage{sdd.Spec, sdd.Plan}
		}
		return designStages()
	default:
		return nil
	}
}

// designStageExecutor says who writes a document: what the project chose for that phase, else the default of Settings.
func (s *Service) designStageExecutor(ctx context.Context, workspaceID string, stage sdd.Stage) (backendID, modelID string, err error) {
	settings, err := s.store.GetSettings(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", "", safe("read default document model", err)
	}
	configured, err := s.store.GetWorkspaceStageExecutor(ctx, workspaceID, stage)
	switch {
	case err == nil:
		backendID, modelID = configured.BackendID, configured.ModelID
		// A CLI has no model of its own: the phase may lean on the model Settings saved for it.
		if modelID == "" && documentCLI(backendID) && settings.DefaultModelBackendID == backendID {
			modelID = settings.DefaultModelID
		}
		return backendID, modelID, nil
	case !errors.Is(err, sql.ErrNoRows):
		return "", "", safe("read the phase executor", err)
	}
	if settings.DefaultBackendID == "" {
		return "", "", ErrPipelineDesignModelRequired
	}
	backendID = settings.DefaultBackendID
	if settings.DefaultModelBackendID == backendID {
		modelID = settings.DefaultModelID
	}
	return backendID, modelID, nil
}

// preparePipelineDesignSelection chooses and confirms who writes one document. When the project chose the executor of
// the phase and it cannot start (the model left the catalog, the profile or Codex is gone, a confirmation is needed
// that a saved choice cannot give), the failure says so about the phase instead of surfacing whichever check tripped.
func (s *Service) preparePipelineDesignSelection(ctx context.Context, workspaceID string, stage sdd.Stage, override *APIModelSelectionInput) (catalog.ModelSelection, error) {
	selection, err := s.resolvePipelineDesignSelection(ctx, workspaceID, stage, override)
	if err == nil || override != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ErrorCode(err) == codeInternal {
		return selection, err
	}
	if _, lookupErr := s.store.GetWorkspaceStageExecutor(ctx, workspaceID, stage); lookupErr == nil {
		return catalog.ModelSelection{}, phaseExecutorError{stage}
	}
	return selection, err
}

func (s *Service) resolvePipelineDesignSelection(ctx context.Context, workspaceID string, stage sdd.Stage, override *APIModelSelectionInput) (catalog.ModelSelection, error) {
	workspace, err := s.store.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return catalog.ModelSelection{}, ErrWorkspaceNotFound
	}
	var input APIModelSelectionInput
	if override != nil {
		input = *override
	} else {
		backendID, modelID, err := s.designStageExecutor(ctx, workspace.ID, stage)
		if err != nil {
			return catalog.ModelSelection{}, err
		}
		if documentCLI(backendID) {
			if modelID == "" {
				return catalog.ModelSelection{}, ErrPipelineDesignModelRequired
			}
			result, err := s.QueryCLIModelCatalog(ctx, CLIModelCatalogQuery{WorkspaceID: workspace.ID, BackendID: backendID})
			if err != nil {
				return catalog.ModelSelection{}, err
			}
			if !result.Complete || result.Status != "complete" {
				return catalog.ModelSelection{}, ErrBackendChanged
			}
			input = APIModelSelectionInput{Executor: "codex_cli", BackendID: backendID, ModelID: modelID, CatalogRevision: result.ProfileRevision, Source: result.Source, CheckedAt: result.CheckedAt}
		} else {
			profile, err := s.store.GetProviderProfile(ctx, backendID)
			if err != nil {
				return catalog.ModelSelection{}, ErrBackendNotFound
			}
			if modelID == "" {
				modelID = profile.Model
			}
			if modelID == "" {
				return catalog.ModelSelection{}, ErrPipelineDesignModelRequired
			}
			result, err := s.QueryHTTPModelCatalog(ctx, HTTPModelCatalogQuery{ProfileID: backendID, Refresh: true})
			if err != nil {
				return catalog.ModelSelection{}, err
			}
			if !result.Complete || result.Status != "complete" {
				return catalog.ModelSelection{}, ErrBackendChanged
			}
			input = APIModelSelectionInput{Executor: "api", ProfileID: backendID, ModelID: modelID, CatalogRevision: result.ProfileRevision, Source: result.Source, Destination: result.Destination, CheckedAt: result.CheckedAt, CredentialToken: result.CredentialToken}
		}
	}
	input.MaxOutputTokens, input.ForSDD = sdd.MaxAuthoringOutputTokens, true
	if input.Executor == "codex_cli" {
		if !documentCLI(input.BackendID) || input.ProfileID != "" || input.CredentialToken != "" || input.Source != documentCLISources[input.BackendID] || input.Destination != "" || input.ConfirmUnfiltered || input.ConfirmJITLoad || input.ConfirmUnverifiedManual {
			return catalog.ModelSelection{}, ErrInvalidInput
		}
		adapter := s.external[input.BackendID]
		if _, capable := adapter.(externalagent.DocumentGenerator); !capable {
			return catalog.ModelSelection{}, ErrPipelineDesignExecutorUnavailable
		}
		if !codexDocumentContractAvailable(adapter, adapter.Detect()) {
			return catalog.ModelSelection{}, ErrPipelineDesignExecutorUnavailable
		}
		choice, err := s.prepareCLIModelSelectionContext(ctx, CreateSessionInput{WorkspaceID: workspace.ID, BackendID: input.BackendID, ModelID: input.ModelID, ReasoningEffort: input.ReasoningEffort, CatalogRevision: input.CatalogRevision}, workspace)
		if err != nil {
			return catalog.ModelSelection{}, err
		}
		if choice == nil || choice.Source != documentCLISources[input.BackendID] {
			return catalog.ModelSelection{}, ErrBackendChanged
		}
		choice.MaxOutputTokens, choice.MaxAssistantOutputBytes = sdd.MaxAuthoringOutputTokens, catalog.MaxPipelineDesignDocumentBytes
		return *choice, nil
	}
	if input.Executor != "" && input.Executor != "api" {
		return catalog.ModelSelection{}, ErrInvalidInput
	}
	choice, err := s.prepareAPIModelSelection(ctx, input)
	if err != nil {
		return catalog.ModelSelection{}, err
	}
	if choice == nil {
		return catalog.ModelSelection{}, ErrPipelineDesignModelRequired
	}
	choice.WorkspacePath = workspace.Path
	return *choice, nil
}

func validPipelineDesignMessage(message string) bool {
	return strings.TrimSpace(message) != "" && len(message) <= catalog.MaxPipelineDesignMessageBytes && utf8.ValidString(message) && !strings.ContainsRune(message, 0)
}
