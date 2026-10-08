package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/secrets"
)

func TestSaveProviderTypeDestinationAndCredentialMatrix(t *testing.T) {
	tests := []struct {
		name, providerType, baseURL, apiKey string
		valid                               bool
	}{
		{"OpenAI HTTPS keyed", "openai", "https://api.openai.example/v1", "test-key", true},
		{"OpenAI HTTPS keyless blocked", "openai", "https://api.openai.example/v1", "", false},
		{"OpenRouter HTTPS keyed", "openrouter", "https://openrouter.example/api", "test-key", true},
		{"OpenRouter HTTP blocked", "openrouter", "http://openrouter.example/api", "test-key", false},
		{"remote generic HTTPS keyed", "generic", "https://remote.example/v1", "test-key", true},
		{"remote generic HTTPS keyless blocked", "generic", "https://remote.example/v1", "", false},
		{"remote generic HTTP blocked", "generic", "http://remote.example/v1", "test-key", false},
		{"generic literal loopback keyless", "generic", "http://127.0.0.1:1234/v1", "", true},
		{"LM Studio literal loopback keyless", "lm_studio", "http://127.0.0.1:1234/v1", "", true},
		{"LM Studio optional token", "lm_studio", "http://127.0.0.1:1234/v1", "local-token", true},
		{"Ollama IPv6 keyless", "ollama", "http://[::1]:11434/v1", "", true},
		{"Ollama localhost alias keyless blocked", "ollama", "http://localhost:11434/v1", "", false},
		{"localhost alias keyless blocked", "generic", "https://localhost:1234/v1", "", false},
		{"LAN keyless blocked", "generic", "https://192.168.1.2:1234/v1", "", false},
		{"LAN HTTP keyless blocked", "generic", "http://192.168.1.3:1234/v1", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, db, vault := setup(t)
			in := profileInput()
			in.ProviderType, in.BaseURL, in.APIKey = tt.providerType, tt.baseURL, tt.apiKey
			_, err := s.SaveProviderProfile(in)
			if !tt.valid {
				if !errors.Is(err, ErrInvalidInput) {
					t.Fatalf("invalid profile accepted: %v", err)
				}
				profiles, listErr := db.ListProviderProfiles(t.Context())
				if listErr != nil || len(profiles) != 0 || len(vault.values) != 0 {
					t.Fatalf("invalid profile changed catalog or vault: %+v, %v", profiles, listErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			stored, err := db.GetProviderProfile(t.Context(), in.ID)
			if err != nil || stored.ProviderType != tt.providerType || (stored.CredentialProvider != "") != (tt.apiKey != "") || (stored.CredentialAccount != "") != (tt.apiKey != "") {
				t.Fatalf("stored profile has wrong type or credential pair: %+v, %v", stored, err)
			}
			profiles, err := s.ListProviderProfiles()
			if err != nil || len(profiles) != 1 || profiles[0].ProviderType != tt.providerType || profiles[0].HasCredential != (tt.apiKey != "") || profiles[0].EndpointBlocked {
				t.Fatalf("profile DTO is inconsistent: %+v, %v", profiles, err)
			}
			refs, err := db.ListCredentialReferences(t.Context())
			if err != nil || len(refs) != len(vault.values) {
				t.Fatalf("active credential and historical inventory disagree: %+v, %v", refs, err)
			}
			if tt.apiKey == "" {
				if len(vault.values) != 0 {
					t.Fatal("keyless profile wrote a secret")
				}
			} else {
				ref := secrets.Reference{Provider: stored.CredentialProvider, Account: stored.CredentialAccount}
				key, err := vault.Get(t.Context(), ref)
				if err != nil || key != tt.apiKey || len(vault.values) != 1 {
					t.Fatal("keyed profile did not publish its secret", err)
				}
			}
		})
	}
}

func TestSameOriginKeepsKeyAndChangedOriginRequiresClearOrNewKey(t *testing.T) {
	for _, clear := range []bool{true, false} {
		name := "fresh key"
		if clear {
			name = "clear credential"
		}
		t.Run(name, func(t *testing.T) {
			s, db, vault := setup(t)
			in := profileInput()
			in.BaseURL = "https://REMOTE.example/v1"
			if _, err := s.SaveProviderProfile(in); err != nil {
				t.Fatal(err)
			}
			initial, err := db.GetProviderProfile(t.Context(), in.ID)
			if err != nil {
				t.Fatal(err)
			}
			oldRef := secrets.Reference{Provider: initial.CredentialProvider, Account: initial.CredentialAccount}
			in.BaseURL, in.ProviderType, in.APIKey = "https://remote.example:443/models", "openrouter", ""
			if _, err := s.SaveProviderProfile(in); err != nil {
				t.Fatal("same canonical origin should keep credential", err)
			}
			previous, err := db.GetProviderProfile(t.Context(), in.ID)
			if err != nil || previous.CredentialProvider != oldRef.Provider || previous.CredentialAccount != oldRef.Account || previous.ProviderType != "openrouter" {
				t.Fatalf("same-origin edit lost credential or type: %+v, %v", previous, err)
			}
			factoryCalls := 0
			s.providerFactory = func(openai.Config) (agentcore.Provider, error) {
				factoryCalls++
				return nil, nil
			}
			in.BaseURL, in.ProviderType = "http://127.0.0.1:1234/v1", "lm_studio"
			gets := vault.gets
			if _, err := s.SaveProviderProfile(in); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("changed origin reused old credential: %v", err)
			}
			unchanged, err := db.GetProviderProfile(t.Context(), in.ID)
			if err != nil || unchanged != previous || vault.gets != gets || factoryCalls != 0 || len(vault.values) != 1 {
				t.Fatalf("rejected move changed profile, vault or provider: %+v, %v", unchanged, err)
			}
			if clear {
				in.ClearCredential = true
			} else {
				in.APIKey = "fresh-local-key"
			}
			if _, err := s.SaveProviderProfile(in); err != nil {
				t.Fatal(err)
			}
			current, err := db.GetProviderProfile(t.Context(), in.ID)
			if err != nil || current.BaseURL != in.BaseURL || current.ProviderType != "lm_studio" || profileRevision(current) == profileRevision(previous) {
				t.Fatalf("changed destination was not published as a new revision: %+v, %v", current, err)
			}
			refs, err := db.ListCredentialReferences(t.Context())
			if err != nil || len(refs) != map[bool]int{true: 1, false: 2}[clear] {
				t.Fatalf("historical references were lost or added on clear: %+v, %v", refs, err)
			}
			oldKey, err := vault.Get(t.Context(), oldRef)
			if err != nil || oldKey != profileInput().APIKey {
				t.Fatal("historical key was removed", err)
			}
			if clear {
				if current.CredentialProvider != "" || current.CredentialAccount != "" || len(vault.values) != 1 || vault.deletes != 0 {
					t.Fatal("clear left an active reference or removed historical secret")
				}
				profiles, err := s.ListProviderProfiles()
				if err != nil || len(profiles) != 1 || profiles[0].HasCredential || profiles[0].EndpointBlocked {
					t.Fatalf("cleared profile DTO is inconsistent: %+v, %v", profiles, err)
				}
			} else {
				newRef := secrets.Reference{Provider: current.CredentialProvider, Account: current.CredentialAccount}
				newKey, err := vault.Get(t.Context(), newRef)
				if err != nil || newRef == oldRef || newKey != in.APIKey || len(vault.values) != 2 {
					t.Fatal("new origin did not get a fresh immutable credential", err)
				}
			}
		})
	}
}

func TestOriginChangeWithBlankKeyNeverPublishesNewDestination(t *testing.T) {
	s, db, vault := setup(t)
	in := profileInput()
	in.BaseURL = "https://first.example/v1"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	previous, err := db.GetProviderProfile(t.Context(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldRef := secrets.Reference{Provider: previous.CredentialProvider, Account: previous.CredentialAccount}
	factoryCalls := 0
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) {
		factoryCalls++
		return nil, nil
	}
	in.BaseURL, in.APIKey = "https://second.example/v1", ""
	gets := vault.gets
	if _, err := s.SaveProviderProfile(in); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("blank key moved to another origin: %v", err)
	}
	unchanged, err := db.GetProviderProfile(t.Context(), in.ID)
	if err != nil || unchanged != previous || vault.gets != gets || factoryCalls != 0 || len(vault.values) != 1 {
		t.Fatalf("rejected origin change touched profile or secret boundary: %+v, %v", unchanged, err)
	}
	in.APIKey = "second-key"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	current, err := db.GetProviderProfile(t.Context(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	newRef := secrets.Reference{Provider: current.CredentialProvider, Account: current.CredentialAccount}
	if newRef == oldRef || len(vault.values) != 2 {
		t.Fatalf("new origin did not receive a fresh key: %+v", current)
	}
}

func TestClearCredentialRequiresExistingKeyedLocalProfileAndEmptyInput(t *testing.T) {
	for _, name := range []string{"new profile", "remote destination", "move to remote", "new key and clear", "no old key"} {
		t.Run(name, func(t *testing.T) {
			s, db, vault := setup(t)
			in := profileInput()
			switch name {
			case "remote destination":
				in.BaseURL = "https://remote.example/v1"
			case "no old key":
				in.APIKey = ""
			}
			if name != "new profile" {
				if _, err := s.SaveProviderProfile(in); err != nil {
					t.Fatal(err)
				}
			}
			before, err := db.ListProviderProfiles(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			beforeVault := make(map[secrets.Reference]string, len(vault.values))
			for ref, key := range vault.values {
				beforeVault[ref] = key
			}
			if name != "new key and clear" && name != "new profile" {
				in.APIKey = ""
			} else {
				in.APIKey = "replacement-key"
			}
			if name == "move to remote" {
				in.BaseURL = "https://remote.example/v1"
			}
			in.ClearCredential = true
			gets, deletes := vault.gets, vault.deletes
			if _, err := s.SaveProviderProfile(in); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("invalid clear accepted: %v", err)
			}
			after, err := db.ListProviderProfiles(t.Context())
			if err != nil || !reflect.DeepEqual(after, before) || !reflect.DeepEqual(vault.values, beforeVault) || vault.gets != gets || vault.deletes != deletes {
				t.Fatalf("invalid clear changed catalog or vault: %+v, %v", after, err)
			}
		})
	}
}

func TestListProviderProfilesKeepsLegacyBlockedRemoteHTTPVisible(t *testing.T) {
	s, db, _ := setup(t)
	now := time.Now().UTC()
	legacy := catalog.ProviderProfile{ID: "legacy", Name: "Legacy", Kind: "openai_compatible", ProviderType: "generic", BaseURL: "http://remote.example/v1", Model: "model", CredentialProvider: "vault", CredentialAccount: "old", CreatedAt: now, UpdatedAt: now}
	if err := db.UpsertProviderProfile(t.Context(), legacy); err != nil {
		t.Fatal(err)
	}
	profiles, err := s.ListProviderProfiles()
	if err != nil || len(profiles) != 1 || profiles[0].ID != "legacy" || profiles[0].ProviderType != "generic" || !profiles[0].EndpointBlocked || !profiles[0].HasCredential {
		t.Fatalf("legacy blocked profile hidden or mislabeled: %+v, %v", profiles, err)
	}
}

func TestPublishProviderProfileAcceptsKeylessAndRejectsPartialCredentialPair(t *testing.T) {
	_, db, _ := setup(t)
	now := time.Now().UTC()
	profile := catalog.ProviderProfile{ID: "local", Name: "Local", Kind: "openai_compatible", ProviderType: "generic", BaseURL: "http://127.0.0.1:1234/v1", Model: "model", CredentialProvider: "vault", CredentialAccount: "first", CreatedAt: now, UpdatedAt: now}
	if err := db.PublishProviderProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	cleared := profile
	cleared.CredentialProvider, cleared.CredentialAccount = "", ""
	cleared.UpdatedAt = now.Add(time.Second)
	if err := db.PublishProviderProfile(t.Context(), cleared); err != nil {
		t.Fatal("keyless publication failed", err)
	}
	refs, err := db.ListCredentialReferences(t.Context())
	if err != nil || len(refs) != 1 || refs[0].Account != "first" {
		t.Fatalf("clear lost historical reference: %+v, %v", refs, err)
	}
	for _, partial := range []catalog.ProviderProfile{
		func() catalog.ProviderProfile { p := cleared; p.CredentialProvider = "vault"; return p }(),
		func() catalog.ProviderProfile { p := cleared; p.CredentialAccount = "second"; return p }(),
	} {
		partial.Name = "Partial"
		if err := db.PublishProviderProfile(t.Context(), partial); err == nil {
			t.Fatal("partial credential pair published")
		}
		stored, err := db.GetProviderProfile(t.Context(), profile.ID)
		if err != nil || stored != cleared {
			t.Fatalf("partial publication changed profile: %+v, %v", stored, err)
		}
	}
}

func TestProviderEndpointPolicy(t *testing.T) {
	tests := []struct {
		name, providerType, rawURL string
		keyRequired, allowed       bool
	}{
		{"openai remote HTTPS", "openai", "https://api.openai.example/v1", true, true},
		{"openai loopback HTTP", "openai", "http://127.0.0.1:1234/v1", true, false},
		{"openrouter remote HTTPS", "openrouter", "https://openrouter.example/api", true, true},
		{"openrouter loopback HTTP", "openrouter", "http://[::1]:1234/v1", true, false},
		{"generic remote HTTPS", "generic", "https://api.example/v1", true, true},
		{"generic remote HTTP", "generic", "http://api.example/v1", true, false},
		{"generic IPv4 loopback HTTP", "generic", "http://127.0.0.2:1234/v1", false, true},
		{"generic IPv6 loopback HTTP", "generic", "http://[::1]:1234/v1", false, true},
		{"generic localhost HTTP", "generic", "http://localhost:1234/v1", true, false},
		{"LM Studio IPv4 loopback HTTP", "lm_studio", "http://127.0.0.1:1234/v1", false, true},
		{"LM Studio IPv6 loopback HTTPS", "lm_studio", "https://[::1]:1234/v1", false, true},
		{"Ollama IPv4 loopback HTTP", "ollama", "http://127.0.0.1:11434/v1", false, true},
		{"Ollama LAN HTTP", "ollama", "http://192.168.1.2:11434/v1", false, false},
		{"unknown type", "unknown", "https://api.example/v1", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, err := url.Parse(tt.rawURL)
			if err != nil {
				t.Fatal(err)
			}
			keyRequired, allowed := providerEndpointPolicy(tt.providerType, base)
			if keyRequired != tt.keyRequired || allowed != tt.allowed {
				t.Fatalf("policy(%q, %q) = (%t, %t), want (%t, %t)", tt.providerType, tt.rawURL, keyRequired, allowed, tt.keyRequired, tt.allowed)
			}
		})
	}
	if keyRequired, allowed := providerEndpointPolicy("generic", nil); keyRequired || allowed {
		t.Fatalf("nil URL policy = (%t, %t), want (false, false)", keyRequired, allowed)
	}
}

func TestParseProfileURL(t *testing.T) {
	tests := []struct {
		rawURL string
		valid  bool
	}{
		{"https://api.example/v1", true},
		{"https://API.example/v1", true},
		{"https://xn--bcher-kva.example/v1", true},
		{"http://[::1]:65535/v1", true},
		{"https://[fe80::1%25EN0]:443/v1", false},
		{"https://İ.example/v1", true},
		{"ftp://api.example/v1", false},
		{"http:/missing-host", false},
		{"https://user:pass@api.example/v1", false},
		{"https://api.example/v1?token=x", false},
		{"https://api.example/v1?", false},
		{"https://api.example/v1#fragment", false},
		{"https://api.example/v1#", false},
		{"https://api.example/v1%23fragment", true},
		{"https://api.example:0/v1", false},
		{"https://api.example:65536/v1", false},
		{"https://api.example:abc/v1", false},
	}
	for _, tt := range tests {
		t.Run(tt.rawURL, func(t *testing.T) {
			base, valid := parseProfileURL(tt.rawURL)
			if valid != tt.valid || (valid && base == nil) || (!valid && base != nil) {
				t.Fatalf("parseProfileURL(%q) = (%v, %t), want valid %t", tt.rawURL, base, valid, tt.valid)
			}
		})
	}
}

func TestCanonicalProfileOrigin(t *testing.T) {
	parse := func(raw string) *url.URL {
		t.Helper()
		base, valid := parseProfileURL(raw)
		if !valid {
			t.Fatalf("invalid profile URL %q", raw)
		}
		return base
	}
	remote := canonicalProfileOrigin(parse("https://API.example/v1"))
	if remote != "https://api.example:443" || remote != canonicalProfileOrigin(parse("https://api.example:443/models")) {
		t.Fatalf("default HTTPS port and case should share origin, got %q", remote)
	}
	idn := canonicalProfileOrigin(parse("https://İ.example/v1"))
	if idn == canonicalProfileOrigin(parse("https://i.example/models")) {
		t.Fatalf("Unicode and ASCII hostnames unexpectedly share origin %q", idn)
	}
	loopback := canonicalProfileOrigin(parse("http://127.0.0.1:80/v1"))
	if loopback != "http://127.0.0.1:80" || loopback != canonicalProfileOrigin(parse("http://127.0.0.1/models")) {
		t.Fatalf("default HTTP port and path should share origin, got %q", loopback)
	}
	for _, raw := range []string{
		"https://api.example:8443/v1",
		"http://api.example/v1",
		"https://other.example/v1",
	} {
		if got := canonicalProfileOrigin(parse(raw)); got == remote {
			t.Errorf("%q unexpectedly shares origin %q", raw, remote)
		}
	}
}

func TestGenericRevisionKeepsHistoricalHash(t *testing.T) {
	profile := catalog.ProviderProfile{
		ID: "profile", Kind: "openai_compatible", ProviderType: "generic",
		BaseURL: "https://api.example/v1", Model: "model",
		CredentialProvider: "keychain", CredentialAccount: "profile-1",
		UpdatedAt: time.Date(2026, 9, 27, 12, 34, 56, 123456789, time.FixedZone("BRT", -3*60*60)),
	}
	legacyFields := []string{
		profile.ID, profile.Kind, profile.BaseURL, profile.Model,
		profile.CredentialProvider, profile.CredentialAccount,
		profile.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000000000Z"),
	}
	data, err := json.Marshal(legacyFields)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	wantLegacy := "api:" + hex.EncodeToString(digest[:])
	if got := profileRevision(profile); got != wantLegacy {
		t.Fatalf("generic revision = %q, want historical hash %q", got, wantLegacy)
	}
	profile.ProviderType = "openai"
	data, err = json.Marshal(append(legacyFields, profile.ProviderType))
	if err != nil {
		t.Fatal(err)
	}
	digest = sha256.Sum256(data)
	wantTyped := "api:" + hex.EncodeToString(digest[:])
	if got := profileRevision(profile); got != wantTyped || got == wantLegacy {
		t.Fatalf("typed revision = %q, want %q and distinct from generic", got, wantTyped)
	}
}

func TestLegacyRemoteHTTPIsDenied(t *testing.T) {
	profile := catalog.ProviderProfile{
		Kind: "openai_compatible", ProviderType: "generic",
		BaseURL:            "http://api.example/v1",
		CredentialProvider: "keychain", CredentialAccount: "profile-1",
	}
	if err := profileNetworkAccess(profile); !errors.Is(err, ErrProviderEndpointBlocked) {
		t.Fatalf("remote HTTP access error = %v, want ErrProviderEndpointBlocked", err)
	}
}

func TestProfileNetworkAccess(t *testing.T) {
	base := catalog.ProviderProfile{
		Kind: "openai_compatible", ProviderType: "generic",
		BaseURL:            "https://api.example/v1",
		CredentialProvider: "keychain", CredentialAccount: "profile-1",
	}
	tests := []struct {
		name   string
		change func(*catalog.ProviderProfile)
		want   error
	}{
		{"remote HTTPS with key", func(*catalog.ProviderProfile) {}, nil},
		{"keyless literal loopback", func(p *catalog.ProviderProfile) {
			p.BaseURL = "http://127.0.0.2:1234/v1"
			p.CredentialProvider, p.CredentialAccount = "", ""
		}, nil},
		{"remote HTTPS without key", func(p *catalog.ProviderProfile) { p.CredentialProvider, p.CredentialAccount = "", "" }, ErrBackendNotFound},
		{"partial reference", func(p *catalog.ProviderProfile) { p.CredentialAccount = "" }, ErrBackendNotFound},
		{"invalid URL", func(p *catalog.ProviderProfile) { p.BaseURL = "https://api.example:0/v1" }, ErrProviderEndpointBlocked},
		{"IPv6 zone", func(p *catalog.ProviderProfile) { p.BaseURL = "https://[fe80::1%25EN0]:443/v1" }, ErrProviderEndpointBlocked},
		{"legacy HTTPS IDN", func(p *catalog.ProviderProfile) { p.BaseURL = "https://İ.example/v1" }, nil},
		{"invalid kind", func(p *catalog.ProviderProfile) { p.Kind = "other" }, ErrProviderEndpointBlocked},
		{"unknown type", func(p *catalog.ProviderProfile) { p.ProviderType = "other" }, ErrProviderEndpointBlocked},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := base
			tt.change(&profile)
			if err := profileNetworkAccess(profile); !errors.Is(err, tt.want) {
				t.Fatalf("profileNetworkAccess() error = %v, want %v", err, tt.want)
			}
		})
	}
}
