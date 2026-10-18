package application

import (
	"sync"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
)

// reloadedService is a second service over the same catalog: its sessions are stored but not loaded yet.
func reloadedService(t *testing.T) (*Service, SessionDTO) {
	t.Helper()
	s, db, vault, session := sessionSetup(t, &fakeProvider{})
	_ = s
	reloaded := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(c openai.Config) (agentcore.Provider, error) { return &fakeProvider{key: c.APIKey}, nil }})
	return reloaded, session
}

// A running Coder holds the recovery gate for its whole run; opening any other conversation must not wait for it.
func TestOpenSessionIsNotBlockedByARunningCoder(t *testing.T) {
	s, session := reloadedService(t)
	held := make(chan struct{})
	release := make(chan struct{})
	go func() {
		s.recoveryGate.RLock()
		close(held)
		<-release
		s.recoveryGate.RUnlock()
	}()
	defer close(release)
	<-held
	opened := make(chan error, 1)
	go func() {
		_, err := s.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: session.WorkspaceID})
		opened <- err
	}()
	select {
	case err := <-opened:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("opening a conversation waited for another run")
	}
}

func TestOpeningTheSameSessionTwiceRecoversOnce(t *testing.T) {
	s, session := reloadedService(t)
	var wg sync.WaitGroup
	results := make([]SessionDTO, 2)
	failures := make([]error, 2)
	for index := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[index], failures[index] = s.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: session.WorkspaceID})
		}()
	}
	wg.Wait()
	if failures[0] != nil || failures[1] != nil || results[0].ID != session.ID || results[1].ID != session.ID {
		t.Fatalf("opens: %v %v %+v %+v", failures[0], failures[1], results[0], results[1])
	}
	s.mu.RLock()
	loaded := s.sessions[session.ID]
	s.mu.RUnlock()
	if loaded == nil {
		t.Fatal("the session was not loaded")
	}
}

// A long tool call in one project holds that project's permission gate; changing another project's profile goes on.
func TestProfileChangeInOneProjectDoesNotWaitForAnotherProjectsTool(t *testing.T) {
	s, _, _ := setup(t)
	first, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	held := make(chan struct{})
	go func() {
		unlock := s.workspacePolicyGate.rlock(second.ID)
		close(held)
		<-release
		unlock()
	}()
	defer close(release)
	<-held
	done := make(chan error, 1)
	go func() {
		_, err := s.SetWorkspaceProfile(SetWorkspaceProfileInput{WorkspaceID: first.ID, Profile: "ask"})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("a profile change waited for another project's tool")
	}
}

func TestListRunningSessionsReturnsOnlyLiveOnes(t *testing.T) {
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Work", Objective: "Change"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for index, item := range []struct{ id, workspace, status string }{
		{"chat-running", workspace.ID, "running"}, {"chat-approval", workspace.ID, "awaiting_approval"},
		{"chat-done", workspace.ID, "completed"}, {"coder-running", workspace.ID, "running"}, {"elsewhere", other.ID, "running"},
	} {
		at := now.Add(time.Duration(index) * time.Second)
		record := catalog.SessionRecord{ID: item.id, WorkspaceID: item.workspace, BackendID: "local", Status: item.status, CreatedAt: at, UpdatedAt: at}
		if err := db.CreateSessionWithSnapshots(t.Context(), record, nil, "", nil, "agent_session", nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.LinkPipelineSession(t.Context(), catalog.PipelineSession{ID: "coder-link", PipelineID: pipeline.ID, SessionID: "coder-running", Role: "coder", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	running, err := s.ListRunningSessions(ListRunningSessionsInput{WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]RunningSessionDTO{}
	for _, item := range running {
		ids[item.Session.ID] = item
	}
	if len(running) != 3 || ids["chat-running"].Session.ID == "" || ids["chat-approval"].Session.ID == "" || ids["coder-running"].PipelineID != pipeline.ID || ids["coder-running"].Role != "coder" || ids["chat-running"].Role != "" {
		t.Fatalf("running sessions: %+v", running)
	}
	if running[0].Session.ID != "coder-running" {
		t.Fatalf("not newest first: %+v", running)
	}
}
