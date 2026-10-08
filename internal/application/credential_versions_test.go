package application

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/secrets"
)

type cleanupFailureSecrets struct {
	secrets.Store
	previous    secrets.Reference
	previousKey string
	attempted   secrets.Reference
	deleted     secrets.Reference
	failure     error
}

func (s *cleanupFailureSecrets) Put(ctx context.Context, ref secrets.Reference, key string) error {
	if ref == s.previous && key == s.previousKey {
		return s.failure
	}
	s.attempted = ref
	return s.Store.Put(ctx, ref, key)
}
func (s *cleanupFailureSecrets) Delete(_ context.Context, ref secrets.Reference) error {
	s.deleted = ref
	return s.failure
}

func TestFailedPublicationAndCleanupPreservePublishedCredential(t *testing.T) {
	s, db, v := setup(t)
	var requests []string
	s.providerFactory = func(c openai.Config) (agentcore.Provider, error) {
		c.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests = append(requests, r.URL.Host+" "+r.Header.Get("Authorization"))
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: [DONE]\n\n"))}, nil
		})}
		return openai.New(c)
	}
	in := profileInput()
	in.BaseURL = "https://endpoint-a.example/v1"
	in.APIKey = "key-a"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	profileA, err := db.GetProviderProfile(t.Context(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	refA := secrets.Reference{Provider: profileA.CredentialProvider, Account: profileA.CredentialAccount}
	w, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(CreateSessionInput{WorkspaceID: w.ID, BackendID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	cleanupFailure := errors.New("private-key-b")
	vault := &cleanupFailureSecrets{Store: v, previous: refA, previousKey: in.APIKey, failure: cleanupFailure}
	s.secrets = vault
	s.store = failingCatalog{db}
	in.BaseURL = "https://endpoint-b.example/v1"
	in.APIKey = "private-key-b"
	if _, err := s.SaveProviderProfile(in); err == nil || !errors.Is(err, cleanupFailure) || strings.Contains(err.Error(), in.APIKey) {
		t.Fatal("publication failure must be sanitized and retain cleanup cause")
	}
	if result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "old session"}); err != nil || result.Status != RunCompleted {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests[0] != "endpoint-a.example Bearer key-a" {
		t.Fatal("failed publication changed published endpoint/credential pairing")
	}
	current, err := db.GetProviderProfile(t.Context(), in.ID)
	if err != nil || current != profileA {
		t.Fatal("failed publication changed catalog", err)
	}
	if vault.attempted == refA || vault.deleted != vault.attempted {
		t.Fatal("cleanup targeted published credential")
	}
	key, err := v.Get(t.Context(), refA)
	if err != nil || key != "key-a" {
		t.Fatal("published credential was modified")
	}
	orphan, err := v.Get(t.Context(), vault.attempted)
	if err != nil || orphan != in.APIKey {
		t.Fatal("failed cleanup must leave only an unreferenced version")
	}
}

func TestEveryCredentialWritePublishesDistinctOpaqueReference(t *testing.T) {
	s, db, v := setup(t)
	in := profileInput()
	in.ID = strings.Repeat("p", 128)
	accounts := make(map[string]bool)
	for version := range 3 {
		in.APIKey = []string{"canary-first", "canary-second", "canary-third"}[version]
		dto, err := s.SaveProviderProfile(in)
		if err != nil {
			t.Fatal(err)
		}
		profile, err := db.GetProviderProfile(t.Context(), in.ID)
		if err != nil {
			t.Fatal(err)
		}
		account := profile.CredentialAccount
		if account == in.ID || account == "" || len(account) > 255 || strings.Contains(account, "/") || accounts[account] {
			t.Fatal("credential reference is not a distinct valid version")
		}
		accounts[account] = true
		data, _ := json.Marshal(profile)
		output, _ := json.Marshal(dto)
		if strings.Contains(string(data), in.APIKey) || strings.Contains(string(output), in.APIKey) || strings.Contains(string(output), account) {
			t.Fatal("credential leaked into catalog or DTO")
		}
		if key, err := v.Get(t.Context(), secrets.Reference{Provider: profile.CredentialProvider, Account: account}); err != nil || key != in.APIKey {
			t.Fatal("published reference points at wrong version")
		}
	}
	if len(v.values) != 3 || v.deletes != 0 {
		t.Fatal("successful publication removed a prior version")
	}
}

func TestEmptyCredentialRequiresPublishedReference(t *testing.T) {
	s, db, _ := setup(t)
	in := profileInput()
	in.BaseURL = "https://remote.example/v1"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	profile, err := db.GetProviderProfile(t.Context(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	profile.CredentialAccount = ""
	if err := db.UpsertProviderProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	in.APIKey = ""
	if _, err := s.SaveProviderProfile(in); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("empty input accepted without a published credential reference", err)
	}
}
