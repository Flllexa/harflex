package application

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/secrets"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOldSessionCannotReleaseUpdatedBackendCredential(t *testing.T) {
	s, db, vault := setup(t)
	var configs []openai.Config
	var requests []string
	s.providerFactory = func(c openai.Config) (agentcore.Provider, error) {
		configs = append(configs, c)
		c.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests = append(requests, r.URL.Host+" "+r.Header.Get("Authorization"))
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: [DONE]\n\n"))}, nil
		})}
		return openai.New(c)
	}
	w, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	in := profileInput()
	in.BaseURL = "https://endpoint-a.example/v1"
	in.APIKey = "key-a"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	before, err := db.GetProviderProfile(t.Context(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.CreateSession(CreateSessionInput{WorkspaceID: w.ID, BackendID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	in.BaseURL = "https://endpoint-b.example/v1"
	in.Model = "model-b"
	in.APIKey = "key-b"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	after, err := db.GetProviderProfile(t.Context(), in.ID)
	if err != nil || after.CredentialAccount == before.CredentialAccount {
		t.Fatal("profile update reused credential reference", err)
	}
	gets := vault.gets
	if key, err := configs[0].APIKey(t.Context()); !errors.Is(err, ErrBackendChanged) || key != "" {
		t.Fatal("stale backend released credential", err)
	}
	if result, err := s.Prompt(PromptInput{SessionID: old.ID, Text: "private prompt"}); err != nil || result != (RunResultDTO{Status: RunFailed, Reason: "execution_failed", Code: "backend_changed"}) {
		t.Fatal(result, err)
	}
	if vault.gets != gets || len(requests) != 0 {
		t.Fatal("stale backend accessed vault or HTTP")
	}
	fresh, err := s.CreateSession(CreateSessionInput{WorkspaceID: w.ID, BackendID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := s.Prompt(PromptInput{SessionID: fresh.ID, Text: "new prompt"}); err != nil || result.Status != RunCompleted {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests[0] != "endpoint-b.example Bearer key-b" {
		t.Fatal("new session used stale endpoint or key")
	}
}

func TestCredentialCallbackRejectsEveryRelevantProfileChange(t *testing.T) {
	changes := map[string]func(*catalog.ProviderProfile){
		"kind":                func(p *catalog.ProviderProfile) { p.Kind = "other" },
		"provider type":       func(p *catalog.ProviderProfile) { p.ProviderType = "ollama" },
		"base URL":            func(p *catalog.ProviderProfile) { p.BaseURL = "https://other.example" },
		"model":               func(p *catalog.ProviderProfile) { p.Model = "other" },
		"credential provider": func(p *catalog.ProviderProfile) { p.CredentialProvider = "other" },
		"credential account":  func(p *catalog.ProviderProfile) { p.CredentialAccount = "other" },
		"revision":            func(p *catalog.ProviderProfile) { p.UpdatedAt = p.UpdatedAt.Add(time.Nanosecond) },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			p := &fakeProvider{}
			_, db, v, _ := sessionSetup(t, p)
			profile, err := db.GetProviderProfile(t.Context(), "local")
			if err != nil {
				t.Fatal(err)
			}
			change(&profile)
			if err := db.UpsertProviderProfile(t.Context(), profile); err != nil {
				t.Fatal(err)
			}
			gets := v.gets
			key, err := p.key(t.Context())
			if !errors.Is(err, ErrBackendChanged) || key != "" || v.gets != gets {
				t.Fatal("changed profile released credential", err)
			}
		})
	}
	t.Run("missing", func(t *testing.T) {
		p := &fakeProvider{}
		_, db, v, _ := sessionSetup(t, p)
		if _, err := db.DB().Exec("DELETE FROM provider_profiles WHERE id = ?", "local"); err != nil {
			t.Fatal(err)
		}
		gets := v.gets
		key, err := p.key(t.Context())
		if !errors.Is(err, ErrBackendChanged) || key != "" || v.gets != gets {
			t.Fatal("missing backend released credential", err)
		}
	})
}

type barrierSecrets struct {
	secrets.Store
	written chan struct{}
	release chan struct{}
}

func (s barrierSecrets) Put(ctx context.Context, ref secrets.Reference, key string) error {
	if err := s.Store.Put(ctx, ref, key); err != nil {
		return err
	}
	if key == "replacement-key" {
		close(s.written)
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func TestProfileUpdateSerializesCredentialCallback(t *testing.T) {
	p := &fakeProvider{}
	s, _, v, _ := sessionSetup(t, p)
	written, release := make(chan struct{}), make(chan struct{})
	s.secrets = barrierSecrets{Store: v, written: written, release: release}
	in := profileInput()
	in.BaseURL = "https://replacement.example"
	in.APIKey = "replacement-key"
	saved := make(chan error, 1)
	go func() { _, err := s.SaveProviderProfile(in); saved <- err }()
	<-written
	type result struct {
		key string
		err error
	}
	read := make(chan result, 1)
	started := make(chan struct{})
	go func() { close(started); key, err := p.key(t.Context()); read <- result{key, err} }()
	<-started
	select {
	case got := <-read:
		close(release)
		<-saved
		t.Fatal("callback crossed incomplete update", got.err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	got := <-read
	if got.key != "" || !errors.Is(got.err, ErrBackendChanged) {
		t.Fatal("callback released replacement key for stale endpoint", got.err)
	}
}

func TestSaveProfileRepairsAbsentCredential(t *testing.T) {
	for _, failCatalog := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "catalog failure"}[failCatalog], func(t *testing.T) {
			s, db, v := setup(t)
			in := profileInput()
			if _, err := s.SaveProviderProfile(in); err != nil {
				t.Fatal(err)
			}
			previous, err := db.GetProviderProfile(t.Context(), in.ID)
			if err != nil {
				t.Fatal(err)
			}
			ref := secrets.Reference{Provider: previous.CredentialProvider, Account: previous.CredentialAccount}
			if err := v.Delete(t.Context(), ref); err != nil {
				t.Fatal(err)
			}
			if failCatalog {
				s.store = failingCatalog{db}
			}
			in.APIKey = "replacement-key"
			_, err = s.SaveProviderProfile(in)
			if (err != nil) != failCatalog {
				t.Fatal(err)
			}
			current, readErr := db.GetProviderProfile(t.Context(), in.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !failCatalog && current.CredentialAccount == previous.CredentialAccount {
				t.Fatal("repair reused reference")
			}
			key, err := v.Get(t.Context(), secrets.Reference{Provider: current.CredentialProvider, Account: current.CredentialAccount})
			if failCatalog {
				if !errors.Is(err, secrets.ErrNotFound) || v.deletes != 2 {
					t.Fatal("failed repair retained key")
				}
			} else if err != nil || key != in.APIKey {
				t.Fatal("missing credential was not repaired", err)
			}
		})
	}
}
