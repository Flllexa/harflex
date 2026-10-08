package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/externalagent"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/secrets"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

type memorySecrets struct {
	mu      sync.Mutex
	values  map[secrets.Reference]string
	failure error
	gets    int
	deletes int
}

func (m *memorySecrets) Put(_ context.Context, r secrets.Reference, v string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failure != nil {
		return m.failure
	}
	m.values[r] = v
	return nil
}
func (m *memorySecrets) Get(_ context.Context, r secrets.Reference) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gets++
	v, ok := m.values[r]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}
func (m *memorySecrets) Delete(_ context.Context, r secrets.Reference) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deletes++
	delete(m.values, r)
	return nil
}
func setup(t *testing.T) (*Service, *sqlite.Store, *memorySecrets) {
	t.Helper()
	db, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	vault := &memorySecrets{values: map[secrets.Reference]string{}}
	return NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ExecutionCacheRoot: t.TempDir()}), db, vault
}
func profileInput() SaveProviderProfileInput {
	return SaveProviderProfileInput{ID: "local", Name: "Local", Kind: "openai_compatible", ProviderType: "generic", BaseURL: "http://127.0.0.1:1234/v1", Model: "model", APIKey: "secret-canary-987"}
}

func TestClearProviderProfileInputRemovesOnlyAPIKey(t *testing.T) {
	input := profileInput()
	want := input
	want.APIKey = ""
	clearProviderProfileInput(&input)
	if input != want {
		t.Fatal("credential clearing must preserve non-secret profile fields")
	}
}

func TestOpenWorkspaceCanonicalStableAndValidated(t *testing.T) {
	s, db, _ := setup(t)
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	first, err := s.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.OpenWorkspace(link)
	if err != nil || first != second {
		t.Fatalf("reopen: %+v %+v %v", first, second, err)
	}
	canonical, _ := filepath.EvalSymlinks(root)
	if first.Path != canonical || first.Profile != "ask" || first.ID == "" {
		t.Fatal(first)
	}
	stored, err := db.GetWorkspace(t.Context(), first.ID)
	if err != nil || stored.Path != canonical {
		t.Fatal(stored, err)
	}
	file := filepath.Join(root, "file")
	os.WriteFile(file, []byte("x"), 0600)
	for _, path := range []string{"", filepath.Join(root, "missing"), file} {
		if _, err := s.OpenWorkspace(path); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid path %q: %v", path, err)
		}
	}
}

func TestListWorkspacesSurvivesRestartAndShowsMissingDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects.db")
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	for _, dir := range []string{first, second} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(t.Context(), Dependencies{Store: db, External: map[string]ExternalBackend{}})
	for _, dir := range []string{first, second, first} {
		if _, err := service.OpenWorkspace(dir); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(second, filepath.Join(root, "moved")); err != nil {
		t.Fatal(err)
	}
	canonicalFirst, err := filepath.EvalSymlinks(first)
	if err != nil {
		t.Fatal(err)
	}
	canonicalSecondParent, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service = NewService(t.Context(), Dependencies{Store: db, External: map[string]ExternalBackend{}})
	items, err := service.ListWorkspaces()
	if err != nil || len(items) != 2 {
		t.Fatalf("list workspaces: %+v %v", items, err)
	}
	if items[0].Path != canonicalFirst || !items[0].Available || items[1].Path != filepath.Join(canonicalSecondParent, "second") || items[1].Available {
		t.Fatalf("unexpected recent order/availability: %+v", items)
	}
}

func TestArchivedWorkspaceKeepsItsSettingsAndComesBackWhenReactivatedOrReopened(t *testing.T) {
	s, db, _ := setup(t)
	root := t.TempDir()
	opened, err := s.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetWorkspaceProfile(SetWorkspaceProfileInput{WorkspaceID: opened.ID, Profile: "trusted_workspace"}); err != nil {
		t.Fatal(err)
	}
	archived, err := s.SetWorkspaceArchived(SetWorkspaceArchivedInput{WorkspaceID: opened.ID, Archived: true})
	if err != nil || !archived.Archived || archived.Profile != "trusted_workspace" || !archived.Available {
		t.Fatalf("archive: %+v %v", archived, err)
	}
	items, err := s.ListWorkspaces()
	if err != nil || len(items) != 1 || !items[0].Archived {
		t.Fatalf("archived project must stay listed and marked: %+v %v", items, err)
	}
	restored, err := s.SetWorkspaceArchived(SetWorkspaceArchivedInput{WorkspaceID: opened.ID})
	if err != nil || restored.Archived {
		t.Fatalf("reactivate: %+v %v", restored, err)
	}
	if _, err := s.SetWorkspaceArchived(SetWorkspaceArchivedInput{WorkspaceID: opened.ID, Archived: true}); err != nil {
		t.Fatal(err)
	}
	// Adding the same folder again brings the project back with what it had.
	reopened, err := s.OpenWorkspace(root)
	if err != nil || reopened != (WorkspaceDTO{ID: opened.ID, Path: opened.Path, Profile: "trusted_workspace"}) {
		t.Fatalf("reopen: %+v %v", reopened, err)
	}
	stored, err := db.ListWorkspaces(t.Context())
	if err != nil || len(stored) != 1 || stored[0].Archived {
		t.Fatalf("reopening must clear the archive: %+v %v", stored, err)
	}
	for _, input := range []SetWorkspaceArchivedInput{{}, {WorkspaceID: "workspace-missing", Archived: true}} {
		if _, err := s.SetWorkspaceArchived(input); !errors.Is(err, ErrInvalidInput) && !errors.Is(err, ErrWorkspaceNotFound) {
			t.Fatalf("invalid archive %+v: %v", input, err)
		}
	}
}

func TestSettingsPersistAndProfilesNeverReturnSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.db")
	db, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	vault := &memorySecrets{values: map[secrets.Reference]string{}}
	service := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}})
	workspace, err := service.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	configuredProfile := profileInput()
	configuredProfile.ProviderType = "openai"
	configuredProfile.BaseURL = "https://api.openai.com/v1"
	if _, err := service.SaveProviderProfile(configuredProfile); err != nil {
		t.Fatal(err)
	}
	profiles, err := service.ListProviderProfiles()
	if err != nil || len(profiles) != 1 || profiles[0].BaseURL != configuredProfile.BaseURL || profiles[0].Model != configuredProfile.Model || profiles[0].ProviderType != "openai" || profiles[0].EndpointBlocked {
		t.Fatalf("profiles: %+v %v", profiles, err)
	}
	encoded, _ := json.Marshal(profiles)
	if strings.Contains(string(encoded), configuredProfile.APIKey) || strings.Contains(string(encoded), "credentialAccount") {
		t.Fatalf("secret reference escaped profile DTO: %s", encoded)
	}
	if _, err := service.SaveSettings(SaveSettingsInput{DefaultBackendID: "local", DefaultModelBackendID: "local", DefaultModelID: "profile-model"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetWorkspaceProfile(SetWorkspaceProfileInput{WorkspaceID: workspace.ID, Profile: "trusted_workspace"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service = NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}})
	settings, err := service.GetSettings()
	if err != nil || settings.DefaultBackendID != "local" || settings.DefaultModelBackendID != "local" || settings.DefaultModelID != "profile-model" {
		t.Fatalf("settings after restart: %+v %v", settings, err)
	}
	workspaces, err := service.ListWorkspaces()
	if err != nil || len(workspaces) != 1 || workspaces[0].Profile != "trusted_workspace" {
		t.Fatalf("profile after restart: %+v %v", workspaces, err)
	}
	if _, err := service.SetWorkspaceProfile(SetWorkspaceProfileInput{WorkspaceID: workspace.ID, Profile: "sandbox"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unavailable sandbox accepted: %v", err)
	}
}

func TestSettingsRejectUnsupportedProviderAsSDDModelDefault(t *testing.T) {
	service, _, _ := setup(t)
	if _, err := service.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveSettings(SaveSettingsInput{DefaultBackendID: "local", DefaultModelBackendID: "local", DefaultModelID: "generic-model"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("generic API profile became an SDD model default: %v", err)
	}
}

func TestSaveProviderProfileUsesSecretReferenceAndPreservesEmptyUpdate(t *testing.T) {
	s, db, vault := setup(t)
	in := profileInput()
	dto, err := s.SaveProviderProfile(in)
	if err != nil {
		t.Fatal(err)
	}
	p, err := db.GetProviderProfile(t.Context(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.ProviderType != "generic" || p.CredentialProvider != in.Kind || !strings.HasPrefix(p.CredentialAccount, in.ID+"-") {
		t.Fatal(p)
	}
	ref := secrets.Reference{Provider: p.CredentialProvider, Account: p.CredentialAccount}
	key, err := vault.Get(t.Context(), ref)
	if err != nil || key != in.APIKey {
		t.Fatal("key not stored")
	}
	encoded, _ := json.Marshal(dto)
	if strings.Contains(string(encoded), in.APIKey) || strings.Contains(string(encoded), "credential") {
		t.Fatal(string(encoded))
	}
	in.APIKey = ""
	in.Name = "Updated"
	if _, err = s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	key, _ = vault.Get(t.Context(), ref)
	if key != profileInput().APIKey {
		t.Fatal("update lost key")
	}
	var rows string
	if err := db.DB().QueryRow("SELECT group_concat(name || base_url || model || credential_account) FROM provider_profiles").Scan(&rows); err != nil || strings.Contains(rows, key) {
		t.Fatal(rows, err)
	}
}

func TestSaveProviderValidation(t *testing.T) {
	for _, change := range []func(*SaveProviderProfileInput){func(i *SaveProviderProfileInput) { i.ID = "" }, func(i *SaveProviderProfileInput) { i.ID = "codex" }, func(i *SaveProviderProfileInput) { i.Name = "" }, func(i *SaveProviderProfileInput) { i.Kind = "other" }, func(i *SaveProviderProfileInput) { i.ProviderType = "" }, func(i *SaveProviderProfileInput) { i.ProviderType = "unknown" }, func(i *SaveProviderProfileInput) { i.Model = "" }, func(i *SaveProviderProfileInput) { i.APIKey = " \t" }, func(i *SaveProviderProfileInput) { i.APIKey = "key\nvalue" }, func(i *SaveProviderProfileInput) { i.APIKey = strings.Repeat("x", 2049) }, func(i *SaveProviderProfileInput) { i.BaseURL = "ftp://host" }, func(i *SaveProviderProfileInput) { i.BaseURL = "https://user:pass@host" }, func(i *SaveProviderProfileInput) { i.BaseURL = "https://host?a=b" }, func(i *SaveProviderProfileInput) { i.BaseURL = "https://host#fragment" }} {
		s, db, v := setup(t)
		in := profileInput()
		change(&in)
		if _, err := s.SaveProviderProfile(in); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid profile accepted: %v", err)
		}
		profiles, _ := db.ListProviderProfiles(t.Context())
		if len(profiles) != 0 || len(v.values) != 0 {
			t.Fatal("invalid input wrote state")
		}
	}
}

type failingCatalog struct{ Store }

func (f failingCatalog) PublishProviderProfile(context.Context, catalog.ProviderProfile) error {
	return errors.New("secret-canary-987")
}
func TestSaveProviderFailureCompensatesAndSanitizes(t *testing.T) {
	s, db, v := setup(t)
	v.failure = errors.New(profileInput().APIKey)
	if _, err := s.SaveProviderProfile(profileInput()); err == nil || strings.Contains(err.Error(), profileInput().APIKey) {
		t.Fatal(err)
	}
	profiles, _ := db.ListProviderProfiles(t.Context())
	if len(profiles) != 0 {
		t.Fatal("catalog changed on vault failure")
	}
	v.failure = nil
	s = NewService(t.Context(), Dependencies{Store: failingCatalog{db}, Secrets: v, External: map[string]ExternalBackend{}})
	if _, err := s.SaveProviderProfile(profileInput()); err == nil || strings.Contains(err.Error(), profileInput().APIKey) {
		t.Fatal(err)
	}
	if len(v.values) != 0 || v.deletes != 1 {
		t.Fatal("orphan secret")
	}
}

type fakeExternal struct{ available bool }

func (fakeExternal) ID() string { return "codex" }
func (f fakeExternal) Detect() externalagent.Detection {
	return externalagent.Detection{Available: f.available, Path: "/private/executable"}
}
func (fakeExternal) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true}
}
func (fakeExternal) Run(context.Context, externalagent.Request) (<-chan externalagent.Event, <-chan error) {
	e := make(chan externalagent.Event)
	r := make(chan error)
	close(e)
	close(r)
	return e, r
}
func TestListBackendsDeterministicAndPrivate(t *testing.T) {
	s, db, v := setup(t)
	s.SaveProviderProfile(profileInput())
	in := profileInput()
	in.ID = "aaa"
	s.SaveProviderProfile(in)
	s = NewService(t.Context(), Dependencies{Store: db, Secrets: v, External: map[string]ExternalBackend{"codex": fakeExternal{true}, "opencode": fakeExternal{false}}})
	got := s.ListBackends()
	if len(got) != 4 || got[0].ID != "aaa" || got[1].ID != "codex" || !got[1].Available || got[3].Available {
		t.Fatal(got)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "/private") || strings.Contains(string(raw), profileInput().APIKey) {
		t.Fatal(string(raw))
	}
}

type fakeProvider struct {
	key      func(context.Context) (string, error)
	request  agentcore.ChatRequest
	output   string
	block    bool
	started  chan struct{}
	toolCall *agentcore.ToolCall
}

func (*fakeProvider) ID() string { return "fake" }
func (*fakeProvider) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true, ToolCalls: true}
}
func (p *fakeProvider) Stream(ctx context.Context, r agentcore.ChatRequest) (<-chan agentcore.StreamEvent, <-chan error) {
	p.request = r
	e := make(chan agentcore.StreamEvent, 1)
	errs := make(chan error, 1)
	if p.key != nil {
		if _, err := p.key(ctx); err != nil {
			errs <- err
		}
	}
	if p.block {
		go func() { close(p.started); <-ctx.Done(); errs <- ctx.Err(); close(e); close(errs) }()
	} else {
		if p.toolCall != nil {
			e <- agentcore.StreamEvent{Type: "tool_call", ToolCall: p.toolCall}
			p.toolCall = nil
		} else {
			output := p.output
			if output == "" {
				output = "hello"
			}
			e <- agentcore.StreamEvent{Type: "text_delta", Delta: output}
		}
		close(e)
		close(errs)
	}
	return e, errs
}
func sessionSetup(t *testing.T, p *fakeProvider) (*Service, *sqlite.Store, *memorySecrets, SessionDTO) {
	t.Helper()
	_, db, v := setup(t)
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: v, External: map[string]ExternalBackend{"codex": fakeExternal{true}}, ProviderFactory: func(c openai.Config) (agentcore.Provider, error) { p.key = c.APIKey; return p, nil }})
	w, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(CreateSessionInput{WorkspaceID: w.ID, BackendID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	return s, db, v, session
}

func TestSessionCompositionAndEventsPersistBeforeEmission(t *testing.T) {
	p := &fakeProvider{}
	s, db, v, session := sessionSetup(t, p)
	if v.gets != 1 {
		t.Fatal("session redaction credential not captured")
	}
	stored, err := db.GetSession(t.Context(), session.ID)
	if err != nil || stored.WorkspaceID != session.WorkspaceID {
		t.Fatal(stored, err)
	}
	var emitted []EventDTO
	SetEmitter(s, func(name string, payload any) {
		if name != "harflex:event" {
			t.Fatal(name)
		}
		dto := payload.(EventDTO)
		persisted, err := db.ListAfter(t.Context(), dto.StreamID, dto.Sequence-1)
		// A buffered tail and its terminal may both commit before callbacks run.
		if err != nil || len(persisted) == 0 || persisted[0].ID != dto.ID {
			t.Fatal("emitted before append", err)
		}
		emitted = append(emitted, dto)
	})
	if result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "test prompt"}); err != nil || result.Status != RunCompleted {
		t.Fatal(result, err)
	}
	if v.gets != 1 || p.request.Model != "model" || len(p.request.Tools) != 12 {
		t.Fatal(v.gets, p.request)
	}
	names := []string{}
	for _, tool := range p.request.Tools {
		names = append(names, tool.Name)
	}
	if !reflect.DeepEqual(names, []string{"bash", "edit", "find", "grep", "harflex_create_pipeline", "harflex_get_pipeline", "harflex_list_pipelines", "knowledge_search", "ls", "read", "update_plan", "write"}) {
		t.Fatal(names)
	}
	listed, err := s.ListEvents(ListEventsInput{SessionID: session.ID})
	if err != nil || len(listed) < 4 || !reflect.DeepEqual(listed, emitted) {
		t.Fatal(listed, emitted, err)
	}
	first := listed[0]
	first.Data[0] = 'X'
	again, _ := s.ListEvents(ListEventsInput{SessionID: session.ID, AfterSequence: listed[0].Sequence, Limit: 1})
	if len(again) != 1 || again[0].Sequence != listed[1].Sequence {
		t.Fatal(again)
	}
	if err := s.Cancel(session.ID); !errors.Is(err, ErrNoActiveRun) {
		t.Fatal(err)
	}
}

func TestSessionValidationAndExternalApproval(t *testing.T) {
	s, _, _, session := sessionSetup(t, &fakeProvider{})
	if _, err := s.CreateSession(CreateSessionInput{WorkspaceID: "missing", BackendID: "local"}); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatal(err)
	}
	if _, err := s.CreateSession(CreateSessionInput{WorkspaceID: session.WorkspaceID, BackendID: "missing"}); !errors.Is(err, ErrBackendNotFound) {
		t.Fatal(err)
	}
	external, err := s.CreateSession(CreateSessionInput{WorkspaceID: session.WorkspaceID, BackendID: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ApprovalInput{SessionID: external.ID, ApprovalID: "approval", Allow: true}); !errors.Is(err, ErrApprovalUnsupported) {
		t.Fatal(err)
	}
	if _, err := s.Approve(ApprovalInput{SessionID: session.ID, ApprovalID: "missing"}); !errors.Is(err, agentcore.ErrApprovalNotFound) {
		t.Fatal(err)
	}
	if _, err := s.Prompt(PromptInput{SessionID: session.ID, Text: " "}); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
	if _, err := s.Prompt(PromptInput{SessionID: "missing", Text: "hello"}); !errors.Is(err, ErrSessionNotFound) {
		t.Fatal(err)
	}
	if err := s.Cancel("missing"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatal(err)
	}
	for _, in := range []ListEventsInput{{SessionID: session.ID, AfterSequence: -1}, {SessionID: session.ID, Limit: -1}, {SessionID: session.ID, Limit: 1001}} {
		if _, err := s.ListEvents(in); !errors.Is(err, ErrInvalidInput) {
			t.Fatal(err)
		}
	}
	if _, err := s.ListEvents(ListEventsInput{SessionID: "missing"}); !errors.Is(err, ErrSessionNotFound) {
		t.Fatal(err)
	}
}

type fakeAudit struct{ input ExportAuditInput }

func (a *fakeAudit) Export(_ context.Context, in ExportAuditInput) (string, error) {
	a.input = in
	return in.Destination, nil
}
func TestExportAuditValidationAndDelegation(t *testing.T) {
	s, _, _, session := sessionSetup(t, &fakeProvider{})
	in := ExportAuditInput{SessionID: session.ID, Destination: filepath.Join(t.TempDir(), "audit.json")}
	if _, err := s.ExportAudit(in); err != nil {
		t.Fatal(err)
	}
	a := &fakeAudit{}
	s.audit = a
	path, err := s.ExportAudit(in)
	if err != nil || path != in.Destination || a.input != in {
		t.Fatal(path, err)
	}
	in.Destination = ""
	if _, err := s.ExportAudit(in); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
}

func TestLifecycleCancellationAndConcurrentAccess(t *testing.T) {
	p := &fakeProvider{block: true, started: make(chan struct{})}
	s, _, _, session := sessionSetup(t, p)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.ctx = ctx
	done := make(chan error, 1)
	go func() { done <- cancelledRun(s.Prompt(PromptInput{SessionID: session.ID, Text: "test"})) }()
	<-p.started
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.ListEvents(ListEventsInput{SessionID: session.ID})
			s.ListBackends()
			s.CreateSession(CreateSessionInput{WorkspaceID: session.WorkspaceID, BackendID: "codex"})
		}()
	}
	wg.Wait()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("lifecycle cancellation blocked")
	}
}

func TestEmptyKeyUpdatePreservesExistingReference(t *testing.T) {
	s, db, _ := setup(t)
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	previous, err := db.GetProviderProfile(t.Context(), "local")
	if err != nil {
		t.Fatal(err)
	}
	previous.CredentialAccount = "existing-account"
	if err := db.UpsertProviderProfile(t.Context(), previous); err != nil {
		t.Fatal(err)
	}
	in := profileInput()
	in.APIKey = ""
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	after, err := db.GetProviderProfile(t.Context(), in.ID)
	if err != nil || after.CredentialAccount != previous.CredentialAccount || after.CredentialProvider != previous.CredentialProvider {
		t.Fatal("empty update changed reference", after, err)
	}
}

func TestUpdateFailurePreservesPreviousCredential(t *testing.T) {
	s, db, v := setup(t)
	in := profileInput()
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	previous, err := db.GetProviderProfile(t.Context(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.store = failingCatalog{db}
	in.APIKey = "replacement-canary"
	if _, err := s.SaveProviderProfile(in); err == nil || strings.Contains(err.Error(), in.APIKey) {
		t.Fatal(err)
	}
	key, err := v.Get(t.Context(), secrets.Reference{Provider: previous.CredentialProvider, Account: previous.CredentialAccount})
	if err != nil || key != profileInput().APIKey {
		t.Fatal("rollback lost previous key")
	}
}

type failingJournal struct{ Store }

func (f failingJournal) Append(context.Context, string, string, string, any) (events.Event, error) {
	return events.Event{}, errors.New("secret-canary-987")
}
func TestAppendFailureNeverEmits(t *testing.T) {
	_, db, v, session := sessionSetup(t, &fakeProvider{})
	s := NewService(t.Context(), Dependencies{Store: failingJournal{db}, Secrets: v, External: map[string]ExternalBackend{"codex": fakeExternal{true}}})
	created, err := s.CreateSession(CreateSessionInput{WorkspaceID: session.WorkspaceID, BackendID: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	emitted := 0
	SetEmitter(s, func(string, any) { emitted++ })
	if _, err := s.Prompt(PromptInput{SessionID: created.ID, Text: "private prompt"}); err == nil || strings.Contains(err.Error(), profileInput().APIKey) || strings.Contains(err.Error(), "private prompt") {
		t.Fatal(err)
	}
	if emitted != 0 {
		t.Fatal("failed append emitted")
	}
}

func TestCancelActiveRun(t *testing.T) {
	p := &fakeProvider{block: true, started: make(chan struct{})}
	s, _, _, session := sessionSetup(t, p)
	done := make(chan error, 1)
	go func() { done <- cancelledRun(s.Prompt(PromptInput{SessionID: session.ID, Text: "test"})) }()
	<-p.started
	if err := s.Cancel(session.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel blocked")
	}
}

func TestListEventsLimitsAndDefensiveData(t *testing.T) {
	s, db, _, session := sessionSetup(t, &fakeProvider{})
	for range 205 {
		if _, err := db.Append(t.Context(), session.ID, "agent_session", "test", map[string]string{"value": "data"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListEvents(ListEventsInput{SessionID: session.ID})
	if err != nil || len(got) != 200 {
		t.Fatal(len(got), err)
	}
	got[0].Data[0] = 'X'
	all, err := s.ListEvents(ListEventsInput{SessionID: session.ID, Limit: 1000})
	if err != nil || len(all) != 205 || !json.Valid(all[0].Data) {
		t.Fatal(len(all), err)
	}
	for i := 1; i < len(all); i++ {
		if all[i].Sequence <= all[i-1].Sequence {
			t.Fatal("events out of order")
		}
	}
}

func TestShutdownDrainsAcceptedRunBeforeCatalogClose(t *testing.T) {
	p := &fakeProvider{block: true, started: make(chan struct{})}
	s, db, _, session := sessionSetup(t, p)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.ctx = ctx
	done := make(chan error, 1)
	go func() { done <- cancelledRun(s.Prompt(PromptInput{SessionID: session.ID, Text: "test"})) }()
	<-p.started
	cancel()
	Shutdown(s)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	persisted, err := db.ListAfter(t.Context(), session.ID, 0)
	if err != nil || persisted[len(persisted)-1].Type != "run.cancelled" {
		t.Fatal(persisted, err)
	}
	if _, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "after shutdown"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestApprovalResumesOnlyAuthorizedWorkspaceTool(t *testing.T) {
	for _, allow := range []bool{false, true} {
		t.Run(map[bool]string{false: "deny", true: "allow"}[allow], func(t *testing.T) {
			p := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "call", Name: "write", Arguments: json.RawMessage(`{"path":"result.txt","content":"approved"}`)}}
			s, db, _, session := sessionSetup(t, p)
			result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "write file"})
			if err != nil || result.Status != RunAwaitingApproval || result.Approval == nil || result.Approval.Name != "write" || string(result.Approval.Arguments) != `{"path":"result.txt","content":"approved"}` {
				t.Fatal(result, err)
			}
			list, err := s.ListEvents(ListEventsInput{SessionID: session.ID})
			if err != nil {
				t.Fatal(err)
			}
			var approval struct {
				ApprovalID string `json:"approvalId"`
			}
			for _, event := range list {
				if event.Type == "approval.requested" {
					if err := json.Unmarshal(event.Data, &approval); err != nil {
						t.Fatal(err)
					}
				}
			}
			if approval.ApprovalID != result.Approval.ID {
				t.Fatal("approval result does not match the persisted request")
			}
			want := RunResultDTO{Status: RunCompleted}
			if !allow {
				want = RunResultDTO{Status: RunFailed, Reason: "approval_denied"}
			}
			if result, err := s.Approve(ApprovalInput{SessionID: session.ID, ApprovalID: approval.ApprovalID, Allow: allow}); err != nil || result.Status != want.Status || result.Reason != want.Reason {
				t.Fatal(result, err)
			}
			workspace, err := db.GetWorkspace(t.Context(), session.WorkspaceID)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(workspace.Path, "result.txt"))
			if allow && (err != nil || string(data) != "approved") {
				t.Fatal(string(data), err)
			}
			if !allow && !os.IsNotExist(err) {
				t.Fatal("denied tool wrote a file")
			}
		})
	}
}

// cancelledRun turns a cancelled terminal status back into context.Canceled so
// lifecycle tests can wait on a single error channel.
func cancelledRun(result RunResultDTO, err error) error {
	if err == nil && result.Status == RunCancelled {
		return context.Canceled
	}
	if err == nil {
		return errors.New("run was not cancelled: " + result.Status)
	}
	return err
}
