package application

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
	"github.com/persioflexa/harflex/internal/secrets"
)

// OpenRouterManagementKeyStatus never contains credential bytes. Usable means
// the stored reference still matches the allowed profile origin and type.
type OpenRouterManagementKeyStatus struct {
	ProfileID  string    `json:"profileId"`
	Origin     string    `json:"origin"`
	Configured bool      `json:"configured"`
	Usable     bool      `json:"usable"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

func (s *Service) SaveOpenRouterManagementKey(profileID, key string) (OpenRouterManagementKeyStatus, error) {
	if err := s.beginCall(); err != nil {
		return OpenRouterManagementKeyStatus{}, err
	}
	defer s.endCall()
	if profileID == "" || strings.TrimSpace(key) == "" || len(key) > 2048 || strings.ContainsAny(key, "\r\n") {
		return OpenRouterManagementKeyStatus{}, ErrInvalidInput
	}
	s.profileGate.Lock()
	defer s.profileGate.Unlock()
	profile, err := s.store.GetProviderProfile(s.ctx, profileID)
	if err != nil {
		return OpenRouterManagementKeyStatus{}, safe("read catalog profile", err)
	}
	if profile.ProviderType != "openrouter" {
		return OpenRouterManagementKeyStatus{}, ErrInvalidInput
	}
	if err := profileNetworkAccess(profile); err != nil {
		return OpenRouterManagementKeyStatus{}, err
	}
	if s.secrets == nil {
		return OpenRouterManagementKeyStatus{}, safe("save catalog credential", secrets.ErrNotFound)
	}
	base, _ := parseProfileURL(profile.BaseURL)
	origin := canonicalProfileOrigin(base)
	ref := secrets.Reference{Provider: "openrouter_management", Account: profile.ID + "-" + id.New()}
	if err := s.secrets.Put(s.ctx, ref, key); err != nil {
		return OpenRouterManagementKeyStatus{}, safe("save catalog credential", err)
	}
	now := time.Now().UTC()
	item := catalog.OpenRouterManagementCredential{
		ProfileID: profile.ID, Origin: origin, CredentialProvider: ref.Provider,
		CredentialAccount: ref.Account, UpdatedAt: now,
	}
	if err := s.store.PublishOpenRouterManagementCredential(s.ctx, item); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
		defer cancel()
		return OpenRouterManagementKeyStatus{}, safe("publish catalog credential", errors.Join(err, s.secrets.Delete(cleanupCtx, ref)))
	}
	return OpenRouterManagementKeyStatus{
		ProfileID: profile.ID, Origin: origin, Configured: true, Usable: true, UpdatedAt: now,
	}, nil
}

func (s *Service) GetOpenRouterManagementKeyStatus(profileID string) (OpenRouterManagementKeyStatus, error) {
	if err := s.beginCall(); err != nil {
		return OpenRouterManagementKeyStatus{}, err
	}
	defer s.endCall()
	if profileID == "" {
		return OpenRouterManagementKeyStatus{}, ErrInvalidInput
	}
	s.profileGate.RLock()
	defer s.profileGate.RUnlock()
	profile, err := s.store.GetProviderProfile(s.ctx, profileID)
	if err != nil {
		return OpenRouterManagementKeyStatus{}, safe("read catalog profile", err)
	}
	status := OpenRouterManagementKeyStatus{ProfileID: profileID}
	item, err := s.store.GetOpenRouterManagementCredential(s.ctx, profileID)
	if errors.Is(err, sql.ErrNoRows) {
		return status, nil
	}
	if err != nil {
		return status, safe("read catalog credential", err)
	}
	base, valid := parseProfileURL(profile.BaseURL)
	status.Origin, status.Configured, status.UpdatedAt = item.Origin, true, item.UpdatedAt
	status.Usable = valid && profile.ProviderType == "openrouter" && profileNetworkAccess(profile) == nil && item.Origin == canonicalProfileOrigin(base)
	return status, nil
}

func (s *Service) ClearOpenRouterManagementKey(profileID string) error {
	if err := s.beginCall(); err != nil {
		return err
	}
	defer s.endCall()
	if profileID == "" {
		return ErrInvalidInput
	}
	s.profileGate.Lock()
	defer s.profileGate.Unlock()
	err := s.store.ClearOpenRouterManagementCredential(s.ctx, profileID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return safe("clear catalog credential", err)
	}
	return nil
}
