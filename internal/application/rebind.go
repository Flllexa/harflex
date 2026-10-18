package application

import (
	"database/sql"
	"errors"
	"slices"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/modelcatalog"
)

// rebindSession lets a conversation follow its provider after the person edited the profile: it goes on with the same
// model when the provider still lists it (else with the profile's model), and its history stays. A CLI, a deleted
// profile or a profile that is no longer an API one cannot follow, and the conversation stays as it was.
func (s *Service) rebindSession(record *catalog.SessionRecord) error {
	if _, external := s.external[record.BackendID]; external {
		return ErrBackendChanged
	}
	profile, err := s.store.GetProviderProfile(s.ctx, record.BackendID)
	if err != nil {
		return ErrBackendNotFound
	}
	if profile.Kind != "openai_compatible" || profileNetworkAccess(profile) != nil {
		return ErrBackendChanged
	}
	// The history follows the profile only to the address it already went to: a profile that now points elsewhere
	// would carry the conversation to a server the person never sent it to, so that one starts a new chat instead.
	if !profile.BaseURLChangedAt.IsZero() && profile.BaseURLChangedAt.After(record.CreatedAt) {
		return ErrBackendChanged
	}
	stored, err := s.store.GetSessionModelSelection(s.ctx, record.ID)
	if errors.Is(err, sql.ErrNoRows) {
		if err := s.store.RebindSessionModelSelection(s.ctx, record.ID, profileRevision(profile), nil); err != nil {
			return safe("rebind session", err)
		}
		record.BackendRevision = profileRevision(profile)
		return nil
	}
	if err != nil {
		return safe("read session model", err)
	}
	result, err := s.QueryHTTPModelCatalog(s.ctx, HTTPModelCatalogQuery{ProfileID: profile.ID, Refresh: true})
	if err != nil || !result.Complete {
		return ErrBackendChanged
	}
	modelID := stored.ModelID
	if !slices.ContainsFunc(result.Models, func(model modelcatalog.Model) bool { return model.ID == modelID }) {
		modelID = profile.Model
	}
	choice, err := s.prepareAPIModelSelection(s.ctx, APIModelSelectionInput{Executor: "api", ProfileID: profile.ID, ModelID: modelID, ReasoningEffort: stored.ReasoningEffort,
		CatalogRevision: result.ProfileRevision, Source: result.Source, Destination: result.Destination, CheckedAt: result.CheckedAt, CredentialToken: result.CredentialToken,
		MaxOutputTokens: stored.MaxOutputTokens, ForSDD: record.Mode == "sdd_code" || record.Mode == "evaluation", ConfirmUnfiltered: stored.ConfirmUnfiltered})
	if err != nil || choice == nil {
		return ErrBackendChanged
	}
	choice.SessionID, choice.WorkspacePath = record.ID, stored.WorkspacePath
	if err := s.store.RebindSessionModelSelection(s.ctx, record.ID, profileRevision(profile), choice); err != nil {
		return safe("rebind session model", err)
	}
	record.BackendRevision = profileRevision(profile)
	return nil
}

// reloadAfterProviderChange swaps the runner of an open conversation whose provider was edited while it was on
// screen: the conversation is opened again, which follows the profile as it is now, and the new runner takes over.
func (s *Service) reloadAfterProviderChange(record catalog.SessionRecord) (sessionRunner, bool) {
	s.mu.Lock()
	journal := s.journals[record.ID]
	delete(s.sessions, record.ID)
	delete(s.journals, record.ID)
	s.mu.Unlock()
	if journal != nil {
		journal.Close()
	}
	opened, err := s.OpenSession(OpenSessionInput{SessionID: record.ID, WorkspaceID: record.WorkspaceID})
	if err != nil || !opened.Resumable {
		return nil, false
	}
	runner, err := s.runner(record.ID)
	return runner, err == nil
}
