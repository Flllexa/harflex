package application

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/modelcatalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

type GetAuthoringStageModelPreferenceInput struct {
	PipelineID string `json:"pipelineId"`
	Stage      string `json:"stage"`
}

type SaveAuthoringStageModelPreferenceInput struct {
	PipelineID       string                 `json:"pipelineId"`
	Stage            string                 `json:"stage"`
	ExpectedRevision int64                  `json:"expectedRevision"`
	ModelMode        string                 `json:"modelMode"`
	EffortMode       string                 `json:"effortMode"`
	ExplicitEffort   string                 `json:"explicitEffort"`
	Selection        APIModelSelectionInput `json:"selection"`
}

type AuthoringStageModelPreferenceDTO struct {
	PipelineID                string                 `json:"pipelineId"`
	Stage                     string                 `json:"stage"`
	ModelMode                 string                 `json:"modelMode"`
	EffortMode                string                 `json:"effortMode"`
	ExplicitEffort            string                 `json:"explicitEffort"`
	PreferenceRevision        int64                  `json:"preferenceRevision"`
	Resolution                string                 `json:"resolution"`
	ModelSource               string                 `json:"modelSource"`
	EffortSource              string                 `json:"effortSource"`
	InheritedFrom             string                 `json:"inheritedFrom"`
	CatalogValidationRequired bool                   `json:"catalogValidationRequired"`
	ErrorCode                 string                 `json:"errorCode"`
	Selection                 BrainstormSelectionDTO `json:"selection"`
}

func validAuthoringStageModelPreferenceName(stage string) bool {
	return stage == "spec" || stage == "plan" || stage == "code" || stage == "eval"
}

func (s *Service) GetAuthoringStageModelPreference(in GetAuthoringStageModelPreferenceInput) (AuthoringStageModelPreferenceDTO, error) {
	if err := s.beginCall(); err != nil {
		return AuthoringStageModelPreferenceDTO{}, err
	}
	defer s.endCall()
	if !validSelectionText(in.PipelineID, 128) || !validAuthoringStageModelPreferenceName(in.Stage) {
		return AuthoringStageModelPreferenceDTO{}, ErrInvalidInput
	}
	preference, err := s.store.GetAuthoringStageModelPreference(s.ctx, in.PipelineID, sdd.Stage(in.Stage))
	if err != nil {
		return AuthoringStageModelPreferenceDTO{}, safe("read authoring model preference", err)
	}
	return s.resolveAuthoringStageModelPreference(s.ctx, preference)
}

func (s *Service) SaveAuthoringStageModelPreference(in SaveAuthoringStageModelPreferenceInput) (AuthoringStageModelPreferenceDTO, error) {
	if err := s.beginCall(); err != nil {
		return AuthoringStageModelPreferenceDTO{}, err
	}
	defer s.endCall()
	if !validSelectionText(in.PipelineID, 128) || !validAuthoringStageModelPreferenceName(in.Stage) || in.ExpectedRevision < 0 {
		return AuthoringStageModelPreferenceDTO{}, ErrInvalidInput
	}
	if in.ModelMode == "" {
		in.ModelMode = "inherit"
	}
	if in.EffortMode == "" {
		in.EffortMode = "inherit"
	}
	if (in.ModelMode != "inherit" && in.ModelMode != "override") ||
		(in.EffortMode != "inherit" && in.EffortMode != "automatic" && in.EffortMode != "explicit") {
		return AuthoringStageModelPreferenceDTO{}, ErrInvalidInput
	}
	if in.EffortMode == "explicit" {
		if !validAPIReasoningEffort(in.ExplicitEffort) || in.ExplicitEffort == "" {
			return AuthoringStageModelPreferenceDTO{}, ErrInvalidInput
		}
	} else if in.ExplicitEffort != "" {
		return AuthoringStageModelPreferenceDTO{}, ErrInvalidInput
	}

	preference := catalog.AuthoringStageModelPreference{
		PipelineID: in.PipelineID, Stage: sdd.Stage(in.Stage), ModelMode: in.ModelMode,
		EffortMode: in.EffortMode, ExplicitEffort: in.ExplicitEffort,
	}
	if in.ModelMode == "override" {
		selectionInput := in.Selection
		if selectionInput.ReasoningEffort != "" {
			return AuthoringStageModelPreferenceDTO{}, ErrInvalidInput
		}
		if selectionInput.MaxOutputTokens == 0 {
			selectionInput.MaxOutputTokens = sdd.MaxAuthoringOutputTokens
		}
		if selectionInput.MaxOutputTokens > sdd.MaxAuthoringOutputTokens {
			return AuthoringStageModelPreferenceDTO{}, ErrInvalidInput
		}
		selectionInput.ForSDD = true
		selection, err := s.prepareAPIModelSelectionWithPolicy(s.ctx, selectionInput, true)
		if err != nil {
			return AuthoringStageModelPreferenceDTO{}, err
		}
		// JIT loading is consent for one inference attempt, not permission that
		// survives a preference save or can be reused from a later readback.
		selection.ConfirmJITLoad = false
		preference.Selection = *selection
		if in.EffortMode == "explicit" && !supportsReasoningEffort(*selection, in.ExplicitEffort) {
			return AuthoringStageModelPreferenceDTO{}, ErrBackendChanged
		}
	} else if hasAPIModelSelection(in.Selection) {
		return AuthoringStageModelPreferenceDTO{}, ErrInvalidInput
	}
	if in.EffortMode == "explicit" && in.ModelMode == "inherit" {
		pipeline, err := s.store.GetPipeline(s.ctx, in.PipelineID)
		if err != nil {
			return AuthoringStageModelPreferenceDTO{}, safe("read authoring pipeline", err)
		}
		inherited, _, err := s.resolveParentAuthoringModelSelection(s.ctx, pipeline, true)
		if err != nil {
			return AuthoringStageModelPreferenceDTO{}, err
		}
		if !supportsReasoningEffort(inherited, in.ExplicitEffort) {
			return AuthoringStageModelPreferenceDTO{}, ErrBackendChanged
		}
	}

	saved, err := s.store.SaveAuthoringStageModelPreference(s.ctx, preference, in.ExpectedRevision)
	if err != nil {
		return AuthoringStageModelPreferenceDTO{}, safe("save authoring model preference", err)
	}
	return s.resolveAuthoringStageModelPreference(s.ctx, saved)
}

func hasAPIModelSelection(in APIModelSelectionInput) bool {
	return in.ProfileID != "" || in.ModelID != "" || in.CatalogRevision != "" || in.Source != "" || in.Destination != "" || in.CredentialToken != ""
}

func supportsReasoningEffort(selection catalog.ModelSelection, effort string) bool {
	for _, supported := range selection.SupportedReasoningEfforts {
		if supported == effort {
			return true
		}
	}
	return false
}

func authoringPreferenceSelectionDTO(selection catalog.ModelSelection) BrainstormSelectionDTO {
	selection.ConfirmJITLoad = false
	return brainstormSelectionDTO(selection)
}

type resolvedAuthoringStageModelPreference struct {
	DTO       AuthoringStageModelPreferenceDTO
	Selection catalog.ModelSelection
}

type authoringStageConsent struct {
	confirmUnfiltered bool
	confirmJITLoad    bool
	enforce           bool
	binding           *AuthoringModelConsentBindingInput
}

func trustedAuthoringPreferenceSource(preference catalog.AuthoringStageModelPreference, source string) bool {
	if preference.Revision == 0 {
		return preference.ModelMode == "inherit" && preference.EffortMode == "inherit" && (source == "brainstorm" || source == "global_default")
	}
	if preference.ModelMode == "override" {
		return source == "phase_override"
	}
	return preference.ModelMode == "inherit" && (source == "brainstorm" || source == "global_default")
}

func authoringPreferenceExecutionError(dto AuthoringStageModelPreferenceDTO) error {
	if dto.Resolution == "unconfigured" {
		return ErrBackendNotFound
	}
	return ErrBackendChanged
}

func (s *Service) resolveAuthoringStageModelPreference(ctx context.Context, preference catalog.AuthoringStageModelPreference) (AuthoringStageModelPreferenceDTO, error) {
	resolved, err := s.resolveAuthoringStageModelPreferenceWithCatalog(ctx, preference, false)
	return resolved.DTO, err
}

func (s *Service) resolveAuthoringStageModelPreferenceDetails(ctx context.Context, preference catalog.AuthoringStageModelPreference, consent authoringStageConsent) (resolvedAuthoringStageModelPreference, error) {
	return s.resolveAuthoringStageModelPreferenceForGeneration(ctx, preference, consent)
}

func (s *Service) resolveAuthoringStageModelPreferenceWithCatalog(ctx context.Context, preference catalog.AuthoringStageModelPreference, refreshCatalog bool) (resolvedAuthoringStageModelPreference, error) {
	return s.resolveAuthoringStageModelPreferenceWithConsent(ctx, preference, refreshCatalog, authoringStageConsent{})
}

func (s *Service) resolveAuthoringStageModelPreferenceForGeneration(ctx context.Context, preference catalog.AuthoringStageModelPreference, consent authoringStageConsent) (resolvedAuthoringStageModelPreference, error) {
	consent.enforce = true
	return s.resolveAuthoringStageModelPreferenceWithConsent(ctx, preference, true, consent)
}

func (s *Service) resolveAuthoringStageModelPreferenceWithConsent(ctx context.Context, preference catalog.AuthoringStageModelPreference, refreshCatalog bool, consent authoringStageConsent) (resolvedAuthoringStageModelPreference, error) {
	dto := AuthoringStageModelPreferenceDTO{
		PipelineID: preference.PipelineID, Stage: string(preference.Stage), ModelMode: preference.ModelMode,
		EffortMode: preference.EffortMode, ExplicitEffort: preference.ExplicitEffort,
		PreferenceRevision: preference.Revision, Resolution: "stale",
	}
	pipeline, err := s.store.GetPipeline(ctx, preference.PipelineID)
	if err != nil {
		return resolvedAuthoringStageModelPreference{}, safe("read authoring pipeline", err)
	}
	if pipeline.Kind != "ai_authoring" {
		return resolvedAuthoringStageModelPreference{}, ErrInvalidInput
	}
	if pipeline.DiscoveryFrozenVersion < 1 {
		dto.Resolution, dto.ErrorCode = "unconfigured", "discovery_not_frozen"
		return resolvedAuthoringStageModelPreference{DTO: dto}, nil
	}
	if pipeline.Status[sdd.Discovery] != sdd.Completed {
		history, historyErr := s.store.ListBrainstormingByPipeline(ctx, pipeline.ID, 1)
		if historyErr != nil {
			return resolvedAuthoringStageModelPreference{}, safe("read brainstorm history", historyErr)
		}
		if len(history) != 0 {
			return authoringPreferenceResolutionFailure(dto, history[0].Selection, "brainstorm", ErrBackendChanged)
		}
		dto.Resolution, dto.ErrorCode = "unconfigured", "discovery_not_frozen"
		return resolvedAuthoringStageModelPreference{DTO: dto}, nil
	}

	var selection catalog.ModelSelection
	var modelSource string
	var parent catalog.ModelSelection
	var parentSource string
	hasParent := false
	if preference.ModelMode == "inherit" || preference.EffortMode == "inherit" {
		parentConsent := consent
		if preference.ModelMode == "override" {
			// The parent contributes only the inherited effort here; it is not
			// the provider/model used by this attempt and cannot consume its
			// JIT or OpenRouter consent.
			parentConsent = authoringStageConsent{}
		}
		parent, parentSource, err = s.resolveParentAuthoringModelSelectionWithConsent(ctx, pipeline, refreshCatalog, parentConsent)
		if err != nil && preference.ModelMode == "override" && errors.Is(err, ErrBackendChanged) {
			// An override brings its own freshly validated model. The parent then only
			// lends its effort, so its catalog snapshot expiring (it lives five minutes)
			// must not strand the override: fall back to the effort Brainstorm stored.
			if fallback, ok := s.storedBrainstormEffort(ctx, pipeline); ok {
				parent, parentSource, err = fallback, "brainstorm", nil
			}
		}
		if err != nil {
			return authoringPreferenceResolutionFailure(dto, parent, parentSource, err)
		}
		hasParent = true
	}
	if preference.ModelMode == "inherit" {
		selection, modelSource = parent, parentSource
	} else {
		modelSource = "phase_override"
		selection = preference.Selection
		var current *catalog.ModelSelection
		if refreshCatalog {
			current, err = s.revalidatedAuthoringModelSelection(ctx, selection, consent)
		} else {
			current, err = s.readPersistedAuthoringModelSelection(ctx, selection)
		}
		if err != nil {
			return authoringPreferenceResolutionFailure(dto, selection, modelSource, err)
		}
		selection = *current
	}

	switch preference.EffortMode {
	case "automatic":
		selection.ReasoningEffort = ""
		dto.EffortSource = "automatic"
	case "explicit":
		if !supportsReasoningEffort(selection, preference.ExplicitEffort) {
			if !refreshCatalog && modelSource == "global_default" && selection.Status == "unverified_default" {
				dto.CatalogValidationRequired = true
			} else {
				selection.ReasoningEffort = preference.ExplicitEffort
				return authoringPreferenceResolutionFailure(dto, selection, modelSource, ErrBackendChanged)
			}
		}
		selection.ReasoningEffort = preference.ExplicitEffort
		dto.EffortSource = "phase_override"
	case "inherit":
		if preference.ModelMode == "override" {
			if !hasParent {
				parent, parentSource, err = s.resolveParentAuthoringModelSelectionWithConsent(ctx, pipeline, refreshCatalog, authoringStageConsent{})
				if err != nil {
					return authoringPreferenceResolutionFailure(dto, selection, modelSource, err)
				}
			}
			if parent.ReasoningEffort != "" && !supportsReasoningEffort(selection, parent.ReasoningEffort) {
				selection.ReasoningEffort = parent.ReasoningEffort
				return authoringPreferenceResolutionFailure(dto, selection, modelSource, ErrBackendChanged)
			}
			selection.ReasoningEffort = parent.ReasoningEffort
			dto.EffortSource = parentSource
		} else {
			dto.EffortSource = modelSource
		}
	default:
		return resolvedAuthoringStageModelPreference{}, ErrInvalidInput
	}
	dto.Resolution, dto.ModelSource = "ready", modelSource
	if !refreshCatalog && modelSource == "global_default" && selection.Status == "unverified_default" {
		dto.CatalogValidationRequired = true
	}
	if preference.ModelMode == "override" && preference.EffortMode == "inherit" {
		dto.InheritedFrom = parentSource
	} else if preference.ModelMode == "inherit" || preference.EffortMode == "inherit" {
		dto.InheritedFrom = modelSource
	}
	dto.Selection = authoringPreferenceSelectionDTO(selection)
	return resolvedAuthoringStageModelPreference{DTO: dto, Selection: selection}, nil
}

// storedBrainstormEffort returns only the reasoning effort of the approved Brainstorm for the
// frozen Discovery, as a selection that carries nothing else. It is used when the parent model
// snapshot can no longer be revalidated but an override still needs the inherited effort.
func (s *Service) storedBrainstormEffort(ctx context.Context, pipeline catalog.PipelineRun) (catalog.ModelSelection, bool) {
	brain, err := s.store.GetBrainstormingByPipeline(ctx, pipeline.ID, pipeline.DiscoveryFrozenVersion)
	if err != nil || brain.State != "approved" || brain.DiscoveryVersion != pipeline.DiscoveryFrozenVersion || !catalog.ValidAPIReasoningEffort(brain.Selection) {
		return catalog.ModelSelection{}, false
	}
	return catalog.ModelSelection{ReasoningEffort: brain.Selection.ReasoningEffort}, true
}

func authoringPreferenceResolutionFailure(dto AuthoringStageModelPreferenceDTO, selection catalog.ModelSelection, source string, cause error) (resolvedAuthoringStageModelPreference, error) {
	dto.ModelSource = source
	dto.Selection = authoringPreferenceSelectionDTO(selection)
	dto.ErrorCode = ErrorCode(cause)
	if errors.Is(cause, ErrBackendNotFound) {
		dto.Resolution = "unconfigured"
	} else {
		dto.Resolution = "stale"
	}
	return resolvedAuthoringStageModelPreference{DTO: dto, Selection: selection}, nil
}

func (s *Service) readPersistedAuthoringModelSelection(ctx context.Context, selection catalog.ModelSelection) (*catalog.ModelSelection, error) {
	if selection.ModelID == "" || selection.CatalogRevision == "" || selection.Destination == "" || selection.CredentialIdentity == "" ||
		selection.CheckedAt.IsZero() || time.Since(selection.CheckedAt) > apiCatalogMaxAge || selection.CheckedAt.After(time.Now().Add(time.Minute)) ||
		selection.Status == "unverified_manual" {
		return nil, ErrBackendChanged
	}
	profile, err := s.store.GetProviderProfile(ctx, selection.BackendID)
	if err != nil || profile.Kind != "openai_compatible" || profile.ProviderType == "generic" ||
		profileRevision(profile) != selection.CatalogRevision || profileNetworkAccess(profile) != nil || !catalog.ValidAPIReasoningEffort(selection) {
		return nil, ErrBackendChanged
	}
	selection.SupportedReasoningEfforts = append([]string(nil), selection.SupportedReasoningEfforts...)
	return &selection, nil
}

func (s *Service) revalidatedAuthoringModelSelection(ctx context.Context, selection catalog.ModelSelection, consent authoringStageConsent) (*catalog.ModelSelection, error) {
	// A stage starts from its saved pick, so the pick keeps its five-minute life: it must not run on a model (and on the
	// JIT or unfiltered confirmations that came with it) the person confirmed long ago. A conversation that already holds
	// its model is different (revalidatedAPIModelSelection does not look at the age of the pick).
	if time.Since(selection.CheckedAt) > apiCatalogMaxAge {
		return nil, ErrBackendChanged
	}
	// JIT permission is always one-attempt-only. OpenRouter permission is
	// durable only when it was explicitly captured for this exact selected
	// model/catalog (Brainstorm or phase override); a global default has no such
	// user-confirmed selection and must use the current attempt signal.
	if consent.enforce {
		if err := validateAuthoringConsentBinding(selection, consent); err != nil {
			return nil, err
		}
		selection.ConfirmJITLoad = consent.confirmJITLoad
		selection.ConfirmUnfiltered = selection.ConfirmUnfiltered || consent.confirmUnfiltered
	}
	return s.revalidatedAPIModelSelection(ctx, selection, true)
}

func validateAuthoringConsentBinding(selection catalog.ModelSelection, consent authoringStageConsent) error {
	if !consent.confirmJITLoad && !consent.confirmUnfiltered {
		return nil
	}
	binding := consent.binding
	if binding == nil || binding.BackendID != selection.BackendID || binding.ModelID != selection.ModelID ||
		binding.CatalogRevision != selection.CatalogRevision || binding.Source != selection.Source || binding.Destination != selection.Destination {
		return ErrBackendChanged
	}
	if consent.confirmJITLoad && selection.Source != "lm_studio_native" {
		return ErrInvalidInput
	}
	if consent.confirmUnfiltered && selection.Source != "openrouter_general_unfiltered" {
		return ErrInvalidInput
	}
	return nil
}

func (s *Service) resolveParentAuthoringModelSelection(ctx context.Context, pipeline catalog.PipelineRun, refreshCatalog bool) (catalog.ModelSelection, string, error) {
	return s.resolveParentAuthoringModelSelectionWithConsent(ctx, pipeline, refreshCatalog, authoringStageConsent{})
}

func (s *Service) resolveParentAuthoringModelSelectionWithConsent(ctx context.Context, pipeline catalog.PipelineRun, refreshCatalog bool, consent authoringStageConsent) (catalog.ModelSelection, string, error) {
	if pipeline.DiscoveryFrozenVersion < 1 || pipeline.Status[sdd.Discovery] != sdd.Completed {
		return catalog.ModelSelection{}, "", ErrBackendNotFound
	}
	brain, err := s.store.GetBrainstormingByPipeline(ctx, pipeline.ID, pipeline.DiscoveryFrozenVersion)
	if err == nil {
		if brain.DiscoveryVersion != pipeline.DiscoveryFrozenVersion || brain.State != "approved" {
			return brain.Selection, "brainstorm", ErrBackendChanged
		}
		var selection *catalog.ModelSelection
		if refreshCatalog {
			selection, err = s.revalidatedAuthoringModelSelection(ctx, brain.Selection, consent)
		} else {
			selection, err = s.readPersistedAuthoringModelSelection(ctx, brain.Selection)
		}
		if err != nil {
			return brain.Selection, "brainstorm", ErrBackendChanged
		}
		return *selection, "brainstorm", nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return catalog.ModelSelection{}, "", safe("read current brainstorm selection", err)
	}
	history, err := s.store.ListBrainstormingByPipeline(ctx, pipeline.ID, 1)
	if err != nil {
		return catalog.ModelSelection{}, "", safe("read brainstorm history", err)
	}
	if hasCurrentAuthoringBrainstormSkip(pipeline) {
		return s.resolveGlobalAuthoringModelSelectionWithConsent(ctx, refreshCatalog, consent)
	}
	if len(history) != 0 {
		return history[0].Selection, "brainstorm", ErrBackendChanged
	}
	return catalog.ModelSelection{}, "global_default", ErrBackendNotFound
}

func hasCurrentAuthoringBrainstormSkip(pipeline catalog.PipelineRun) bool {
	return pipeline.DiscoveryFrozenVersion > 0 && pipeline.Status[sdd.Discovery] == sdd.Completed && pipeline.Status[sdd.Spec] != sdd.Pending
}

func (s *Service) resolveGlobalAuthoringModelSelection(ctx context.Context, refreshCatalog bool) (catalog.ModelSelection, string, error) {
	return s.resolveGlobalAuthoringModelSelectionWithConsent(ctx, refreshCatalog, authoringStageConsent{})
}

func (s *Service) resolveGlobalAuthoringModelSelectionWithConsent(ctx context.Context, refreshCatalog bool, consent authoringStageConsent) (catalog.ModelSelection, string, error) {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return catalog.ModelSelection{}, "global_default", safe("read default backend", err)
	}
	if settings.DefaultBackendID == "" {
		return catalog.ModelSelection{}, "global_default", ErrBackendNotFound
	}
	profile, err := s.store.GetProviderProfile(ctx, settings.DefaultBackendID)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.ModelSelection{}, "global_default", ErrBackendChanged
	}
	if err != nil {
		return catalog.ModelSelection{}, "global_default", safe("read default provider profile", err)
	}
	if strings.TrimSpace(profile.Model) == "" {
		return catalog.ModelSelection{}, "global_default", ErrBackendNotFound
	}
	if profile.Kind != "openai_compatible" || profileNetworkAccess(profile) != nil {
		return catalog.ModelSelection{}, "global_default", ErrBackendChanged
	}
	if !refreshCatalog {
		base, _ := parseProfileURL(profile.BaseURL)
		return catalog.ModelSelection{
			BackendID: profile.ID, ModelID: profile.Model, CatalogRevision: profileRevision(profile),
			Source: "global_default", Destination: canonicalProfileOrigin(base),
			Status: "unverified_default", MaxOutputTokens: sdd.MaxAuthoringOutputTokens,
		}, "global_default", nil
	}
	result, err := s.QueryHTTPModelCatalog(ctx, HTTPModelCatalogQuery{ProfileID: profile.ID, Refresh: true})
	if err != nil || !result.Complete || result.Status != modelcatalog.StatusComplete || result.BackendID != profile.ID || result.ProfileRevision != profileRevision(profile) || result.Destination == "" {
		return catalog.ModelSelection{}, "global_default", ErrBackendChanged
	}
	var selected *modelcatalog.Model
	for i := range result.Models {
		item := &result.Models[i]
		if item.ID == profile.Model && item.BackendID == profile.ID && item.Source == result.Source {
			selected = item
			break
		}
	}
	if selected == nil {
		return catalog.ModelSelection{}, "global_default", ErrBackendChanged
	}
	selection := catalog.ModelSelection{
		BackendID: profile.ID, ModelID: selected.ID, CatalogRevision: result.ProfileRevision,
		Source: result.Source, Destination: result.Destination, CredentialIdentity: result.CredentialIdentity,
		Status: "listed", MaxOutputTokens: sdd.MaxAuthoringOutputTokens,
		ContextLength: max(0, selected.ContextLength), CheckedAt: result.CheckedAt,
		SupportedReasoningEfforts: append([]string(nil), selected.SupportedReasoningEfforts...),
	}
	if consent.enforce {
		if err := validateAuthoringConsentBinding(selection, consent); err != nil {
			return selection, "global_default", err
		}
	}
	if result.Source == "openrouter_general_unfiltered" {
		selection.Status = "listed_unfiltered"
		selection.ConfirmUnfiltered = consent.enforce && consent.confirmUnfiltered
		if consent.enforce && !consent.confirmUnfiltered {
			return selection, "global_default", ErrInvalidInput
		}
	}
	if profile.ProviderType == "lm_studio" && selected.Loaded != nil && !*selected.Loaded {
		if consent.enforce && !consent.confirmJITLoad {
			return selection, "global_default", ErrInvalidInput
		}
		selection.ConfirmJITLoad = consent.enforce && consent.confirmJITLoad
	}
	if !selectedModelInCatalog(result, selection) {
		return catalog.ModelSelection{}, "global_default", ErrBackendChanged
	}
	return selection, "global_default", nil
}
