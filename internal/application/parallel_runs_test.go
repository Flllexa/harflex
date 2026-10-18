package application

import (
	"sync"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
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
