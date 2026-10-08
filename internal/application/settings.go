package application

import (
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/security"
)

func (s *Service) ListProviderProfiles() ([]ProviderProfileDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	profiles, err := s.store.ListProviderProfiles(s.ctx)
	if err != nil {
		return nil, safe("list provider profiles", err)
	}
	result := make([]ProviderProfileDTO, 0, len(profiles))
	for _, item := range profiles {
		result = append(result, ProviderProfileDTO{
			ID: item.ID, Name: item.Name, Kind: item.Kind, ProviderType: item.ProviderType, BaseURL: item.BaseURL,
			Model: item.Model, HasCredential: item.CredentialProvider != "" && item.CredentialAccount != "",
			EndpointBlocked: errors.Is(profileNetworkAccess(item), ErrProviderEndpointBlocked), UpdatedAt: item.UpdatedAt,
		})
	}
	return result, nil
}

func (s *Service) GetSettings() (SettingsDTO, error) {
	if err := s.beginCall(); err != nil {
		return SettingsDTO{}, err
	}
	defer s.endCall()
	settings, err := s.store.GetSettings(s.ctx)
	if err != nil {
		return SettingsDTO{}, safe("get settings", err)
	}
	return SettingsDTO{DefaultBackendID: settings.DefaultBackendID, DefaultModelBackendID: settings.DefaultModelBackendID, DefaultModelID: settings.DefaultModelID}, nil
}

func (s *Service) SaveSettings(in SaveSettingsInput) (SettingsDTO, error) {
	if err := s.beginCall(); err != nil {
		return SettingsDTO{}, err
	}
	defer s.endCall()
	s.profileGate.Lock()
	defer s.profileGate.Unlock()
	if in.DefaultBackendID != "" {
		if strings.TrimSpace(in.DefaultBackendID) != in.DefaultBackendID || !profileID.MatchString(in.DefaultBackendID) {
			return SettingsDTO{}, ErrInvalidInput
		}
		if adapter, ok := s.external[in.DefaultBackendID]; ok {
			if adapter == nil || !adapter.Detect().Available {
				return SettingsDTO{}, ErrBackendNotFound
			}
		} else {
			profile, err := s.store.GetProviderProfile(s.ctx, in.DefaultBackendID)
			if errors.Is(err, sql.ErrNoRows) {
				return SettingsDTO{}, ErrBackendNotFound
			}
			if err != nil {
				return SettingsDTO{}, safe("validate default backend", err)
			}
			if err := profileNetworkAccess(profile); err != nil {
				return SettingsDTO{}, err
			}
		}
	}
	if in.DefaultModelID != "" && (in.DefaultModelBackendID == "" || in.DefaultModelBackendID != in.DefaultBackendID || strings.TrimSpace(in.DefaultModelID) != in.DefaultModelID || len(in.DefaultModelID) > 512 || !utf8.ValidString(in.DefaultModelID)) {
		return SettingsDTO{}, ErrInvalidInput
	}
	if in.DefaultModelID == "" && in.DefaultModelBackendID != "" {
		return SettingsDTO{}, ErrInvalidInput
	}
	if in.DefaultModelID != "" && !documentCLI(in.DefaultModelBackendID) {
		if _, isExternal := s.external[in.DefaultModelBackendID]; isExternal {
			return SettingsDTO{}, ErrInvalidInput
		}
		profile, err := s.store.GetProviderProfile(s.ctx, in.DefaultModelBackendID)
		if errors.Is(err, sql.ErrNoRows) {
			return SettingsDTO{}, ErrBackendNotFound
		}
		if err != nil {
			return SettingsDTO{}, safe("validate default SDD model provider", err)
		}
		localProvider := profile.ProviderType == "lm_studio" || profile.ProviderType == "ollama"
		if profile.ProviderType == "generic" || (!localProvider && (profile.CredentialProvider == "" || profile.CredentialAccount == "")) {
			return SettingsDTO{}, ErrInvalidInput
		}
	}
	settings := catalog.AppSettings{DefaultBackendID: in.DefaultBackendID, DefaultModelBackendID: in.DefaultModelBackendID, DefaultModelID: in.DefaultModelID}
	if err := s.store.SaveSettings(s.ctx, settings); err != nil {
		return SettingsDTO{}, safe("save settings", err)
	}
	return SettingsDTO{DefaultBackendID: settings.DefaultBackendID, DefaultModelBackendID: settings.DefaultModelBackendID, DefaultModelID: settings.DefaultModelID}, nil
}

func (s *Service) SetWorkspaceProfile(in SetWorkspaceProfileInput) (WorkspaceDTO, error) {
	if err := s.beginCall(); err != nil {
		return WorkspaceDTO{}, err
	}
	defer s.endCall()
	if in.WorkspaceID == "" || !selectableWorkspaceProfile(in.Profile, in.ConfirmFullAccess) {
		return WorkspaceDTO{}, ErrInvalidInput
	}
	s.workspacePolicyGate.Lock()
	defer s.workspacePolicyGate.Unlock()
	workspace, err := s.store.GetWorkspace(s.ctx, in.WorkspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkspaceDTO{}, ErrWorkspaceNotFound
	}
	if err != nil {
		return WorkspaceDTO{}, safe("get workspace", err)
	}
	if err := s.store.SetWorkspaceProfile(s.ctx, in.WorkspaceID, in.Profile); err != nil {
		return WorkspaceDTO{}, safe("set workspace profile", err)
	}
	return WorkspaceDTO{ID: workspace.ID, Path: workspace.Path, Profile: in.Profile}, nil
}

// selectableWorkspaceProfile says whether a profile can be saved for a project. Full access is the one
// that stops every approval prompt, so it is only accepted together with the person's explicit confirmation.
func selectableWorkspaceProfile(profile string, confirmedFullAccess bool) bool {
	switch security.Profile(profile) {
	case security.Ask, security.TrustedWorkspace:
		return true
	case security.FullAccess:
		return confirmedFullAccess
	}
	return false
}
