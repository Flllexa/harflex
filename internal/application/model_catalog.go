package application

import (
	"context"
	"crypto/hmac"
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
	"github.com/persioflexa/harflex/internal/modelcatalog"
	"github.com/persioflexa/harflex/internal/secrets"
)

// HTTPModelCatalogDraft carries an unsaved, one-shot key. QueryHTTPModelCatalog
// clears APIKey on return and never caches draft catalog results.
type HTTPModelCatalogDraft struct {
	ProviderType string `json:"providerType"`
	BaseURL      string `json:"baseUrl"`
	APIKey       string `json:"apiKey"`
}

type HTTPModelCatalogQuery struct {
	ProfileID  string                 `json:"profileId"`
	SearchTerm string                 `json:"searchTerm"`
	Refresh    bool                   `json:"refresh"`
	Draft      *HTTPModelCatalogDraft `json:"draft,omitempty"`
}

func catalogCredentialIdentity(reference, inferenceKey, managementReference, managementKey string) string {
	data, _ := json.Marshal([]string{reference, inferenceKey, managementReference, managementKey})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (s *Service) catalogSelectionToken(identity, profileID, revision, source, destination string, checkedAt time.Time) string {
	if !s.catalogTokenReady {
		return ""
	}
	mac := hmac.New(sha256.New, s.catalogTokenKey[:])
	data, _ := json.Marshal([]string{identity, profileID, revision, source, destination, checkedAt.UTC().Format(time.RFC3339Nano)})
	_, _ = mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Service) QueryHTTPModelCatalog(ctx context.Context, in HTTPModelCatalogQuery) (modelcatalog.Result, error) {
	if in.Draft != nil {
		defer func() { in.Draft.APIKey = "" }()
	}
	if err := s.beginCall(); err != nil {
		return modelcatalog.Result{}, err
	}
	defer s.endCall()
	if ctx == nil || in.ProfileID == "" || len(in.SearchTerm) > 256 || !utf8.ValidString(in.SearchTerm) || strings.IndexFunc(in.SearchTerm, unicode.IsControl) >= 0 {
		return modelcatalog.Result{}, ErrInvalidInput
	}
	var profile catalog.ProviderProfile
	var key, management, identity, managementIdentity string
	var err error
	if in.Draft == nil {
		s.profileGate.RLock()
		profile, err = s.store.GetProviderProfile(ctx, in.ProfileID)
		if errors.Is(err, sql.ErrNoRows) {
			s.profileGate.RUnlock()
			return modelcatalog.Result{Status: modelcatalog.StatusFailed, ErrorCode: "catalog_profile_not_found"}, nil
		}
		if err != nil {
			s.profileGate.RUnlock()
			return modelcatalog.Result{}, safe("read catalog profile", err)
		}
		if err = profileNetworkAccess(profile); err != nil {
			s.profileGate.RUnlock()
			return modelcatalog.Result{Status: modelcatalog.StatusFailed, ErrorCode: "catalog_profile_blocked"}, nil
		}
		var managementRef *catalog.OpenRouterManagementCredential
		if profile.ProviderType == "openrouter" {
			item, readErr := s.store.GetOpenRouterManagementCredential(ctx, profile.ID)
			if readErr == nil {
				base, _ := parseProfileURL(profile.BaseURL)
				if item.Origin != canonicalProfileOrigin(base) {
					s.profileGate.RUnlock()
					return modelcatalog.Result{Status: modelcatalog.StatusFailed, ErrorCode: "catalog_management_origin_changed"}, nil
				}
				managementRef = &item
			} else if !errors.Is(readErr, sql.ErrNoRows) {
				s.profileGate.RUnlock()
				return modelcatalog.Result{}, safe("read catalog credential", readErr)
			}
		}
		if s.secrets == nil && (profile.CredentialProvider != "" || managementRef != nil) {
			s.profileGate.RUnlock()
			return modelcatalog.Result{Status: modelcatalog.StatusFailed, ErrorCode: "catalog_credential_missing"}, nil
		}
		if profile.CredentialProvider != "" {
			key, err = s.secrets.Get(ctx, secrets.Reference{Provider: profile.CredentialProvider, Account: profile.CredentialAccount})
			if err != nil || key == "" {
				s.profileGate.RUnlock()
				return modelcatalog.Result{Status: modelcatalog.StatusFailed, ErrorCode: "catalog_credential_missing"}, nil
			}
		}
		identity = profile.CredentialProvider + ":" + profile.CredentialAccount
		if managementRef != nil {
			management, err = s.secrets.Get(ctx, secrets.Reference{Provider: managementRef.CredentialProvider, Account: managementRef.CredentialAccount})
			if err != nil || management == "" {
				s.profileGate.RUnlock()
				return modelcatalog.Result{Status: modelcatalog.StatusFailed, ErrorCode: "catalog_management_credential_missing"}, nil
			}
			managementIdentity = managementRef.Origin + "|" + managementRef.CredentialProvider + ":" + managementRef.CredentialAccount
			identity += "|management:" + managementRef.CredentialProvider + ":" + managementRef.CredentialAccount
		}
		s.profileGate.RUnlock()
	} else {
		base, valid := parseProfileURL(in.Draft.BaseURL)
		required, allowed := providerEndpointPolicy(in.Draft.ProviderType, base)
		if !valid || !allowed || (required && in.Draft.APIKey == "") || len(in.Draft.APIKey) > 2048 ||
			(in.Draft.APIKey != "" && strings.TrimSpace(in.Draft.APIKey) == "") || strings.ContainsAny(in.Draft.APIKey, "\r\n") {
			return modelcatalog.Result{Status: modelcatalog.StatusFailed, ErrorCode: "catalog_profile_blocked"}, nil
		}
		profile = catalog.ProviderProfile{ID: in.ProfileID, Kind: "openai_compatible", ProviderType: in.Draft.ProviderType, BaseURL: in.Draft.BaseURL}
		key = in.Draft.APIKey
	}
	base, _ := parseProfileURL(profile.BaseURL)
	origin := canonicalProfileOrigin(base)
	revision := profileRevision(profile)
	credentialIdentity := catalogCredentialIdentity(identity, key, managementIdentity, management)
	if in.Draft == nil && !s.catalogTokenReady {
		return modelcatalog.Result{}, errors.New("catalog selection token unavailable")
	}
	cacheKey := modelcatalog.CacheKey{BackendID: profile.ID, Revision: revision, Origin: origin, CredentialIdentity: credentialIdentity, SearchTerm: in.SearchTerm}
	if in.Draft == nil && !in.Refresh {
		if cached, ok := s.modelCatalogCache.Get(cacheKey); ok {
			return cached, nil
		}
	}
	request := modelcatalog.HTTPQuery{
		BackendID: profile.ID, ProviderType: profile.ProviderType, BaseURL: profile.BaseURL,
		InferenceKey: key, ManagementKey: management, SearchTerm: in.SearchTerm,
	}
	found := modelcatalog.Collect(ctx, func(pageCtx context.Context, cursor string, remaining int64) (modelcatalog.Page, error) {
		return modelcatalog.FetchHTTPPage(pageCtx, s.modelHTTPClient, request, cursor, remaining)
	})
	found.BackendID, found.Destination, found.ProfileRevision, found.SearchTerm = profile.ID, origin, revision, in.SearchTerm
	if in.Draft == nil {
		found.CredentialIdentity = credentialIdentity
		found.CredentialToken = s.catalogSelectionToken(credentialIdentity, profile.ID, revision, found.Source, origin, found.CheckedAt)
	}
	for _, item := range found.Models {
		for _, secret := range []string{key, management} {
			if secret != "" && (strings.Contains(item.ID, secret) || strings.Contains(item.DisplayName, secret) ||
				strings.Contains(item.OwnedBy, secret) || strings.Contains(item.DefaultReasoningEffort, secret)) {
				found.Models = nil
				found.Status, found.Complete, found.ErrorCode = modelcatalog.StatusFailed, false, "catalog_secret_echo"
				return found, nil
			}
			for _, effort := range item.SupportedReasoningEfforts {
				if secret != "" && strings.Contains(effort, secret) {
					found.Models = nil
					found.Status, found.Complete, found.ErrorCode = modelcatalog.StatusFailed, false, "catalog_secret_echo"
					return found, nil
				}
			}
		}
	}
	if found.Complete && profile.ProviderType == "openrouter" && management != "" && in.SearchTerm != "" {
		term := strings.ToLower(in.SearchTerm)
		kept := make([]modelcatalog.Model, 0, len(found.Models))
		for _, item := range found.Models {
			if strings.Contains(strings.ToLower(item.ID), term) || strings.Contains(strings.ToLower(item.DisplayName), term) {
				kept = append(kept, item)
			}
		}
		found.Models = kept
		if len(kept) == 0 {
			found.Status = modelcatalog.StatusEmpty
		}
	}
	if in.Draft == nil {
		s.profileGate.RLock()
		current, readErr := s.store.GetProviderProfile(ctx, profile.ID)
		currentManagementIdentity := ""
		if readErr == nil && current.ProviderType == "openrouter" {
			active, keyErr := s.store.GetOpenRouterManagementCredential(ctx, profile.ID)
			if keyErr == nil {
				currentManagementIdentity = active.Origin + "|" + active.CredentialProvider + ":" + active.CredentialAccount
			}
			if keyErr != nil && !errors.Is(keyErr, sql.ErrNoRows) {
				readErr = keyErr
			}
		}
		s.profileGate.RUnlock()
		if readErr != nil || profileRevision(current) != revision {
			found.Status, found.Complete, found.ErrorCode = modelcatalog.StatusPartial, false, "catalog_profile_changed"
			return found, nil
		}
		if currentManagementIdentity != managementIdentity {
			found.Status, found.Complete, found.ErrorCode = modelcatalog.StatusPartial, false, "catalog_management_changed"
			return found, nil
		}
		s.modelCatalogCache.Put(cacheKey, found)
	}
	return found, nil
}
