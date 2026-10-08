package application

import (
	"database/sql"
	"errors"

	"github.com/persioflexa/harflex/internal/secrets"
)

// ProbeCredentialStore reads only active credential references. It never returns values.
func (s *Service) ProbeCredentialStore(workspaceID string) (CredentialProbeDTO, error) {
	if err := s.beginCall(); err != nil {
		return CredentialProbeDTO{}, err
	}
	defer s.endCall()
	s.profileGate.RLock()
	defer s.profileGate.RUnlock()
	profiles, err := s.store.ListProviderProfiles(s.ctx)
	if err != nil {
		return CredentialProbeDTO{}, safe("list credential owners", err)
	}
	refs := make(map[secrets.Reference]struct{})
	for _, profile := range profiles {
		if profile.CredentialProvider != "" && profile.CredentialAccount != "" {
			refs[secrets.Reference{Provider: profile.CredentialProvider, Account: profile.CredentialAccount}] = struct{}{}
		}
	}
	if workspaceID != "" {
		if _, err := s.store.GetWorkspace(s.ctx, workspaceID); errors.Is(err, sql.ErrNoRows) {
			return CredentialProbeDTO{}, ErrWorkspaceNotFound
		} else if err != nil {
			return CredentialProbeDTO{}, safe("get credential workspace", err)
		}
		servers, err := s.store.ListMCPServers(s.ctx, workspaceID)
		if err != nil {
			return CredentialProbeDTO{}, safe("list MCP credential owners", err)
		}
		for _, server := range servers {
			if server.CredentialProvider != "" && server.CredentialAccount != "" {
				refs[secrets.Reference{Provider: server.CredentialProvider, Account: server.CredentialAccount}] = struct{}{}
			}
		}
	}
	result := CredentialProbeDTO{Status: "unconfigured", Checked: len(refs)}
	if len(refs) == 0 {
		return result, nil
	}
	if s.secrets == nil {
		result.Status = "unavailable"
		return result, nil
	}
	result.Status = "ready"
	for ref := range refs {
		value, err := s.secrets.Get(s.ctx, ref)
		if err == nil && value != "" {
			value = ""
			continue
		}
		value = ""
		if err != nil && !errors.Is(err, secrets.ErrNotFound) {
			result.Status = "unavailable"
			return result, nil
		}
		result.Missing++
	}
	if result.Missing > 0 {
		result.Status = "degraded"
	}
	return result, nil
}
