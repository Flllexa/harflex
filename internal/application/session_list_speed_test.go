package application

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/externalagent"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

// Detecting a CLI runs it (--version), a quarter of a second or more. Listing a project's conversations asks once
// per CLI, however many of its conversations ran on it.
func TestListingConversationsDetectsEachCLIOnce(t *testing.T) {
	_, db, vault := setup(t)
	stub := &cliCatalogStub{fakeExternal: fakeExternal{available: true}, id: "codex", version: "codex 1"}
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{"codex": stub}})
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i, mode := range []string{"", "", "", "sdd_code", "evaluation", "", ""} {
		record := catalog.SessionRecord{ID: fmt.Sprintf("codex-chat-%d", i), WorkspaceID: workspace.ID, BackendID: "codex", Mode: mode, Status: "ready", CreatedAt: now, UpdatedAt: now.Add(time.Duration(i) * time.Second)}
		if err := db.UpsertSession(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	stub.detectCount = 0
	list, err := s.ListSessions(ListSessionsInput{WorkspaceID: workspace.ID})
	if err != nil || len(list) != 7 {
		t.Fatalf("listed %d: %v", len(list), err)
	}
	if stub.detectCount > 1 {
		t.Fatalf("listing 7 conversations ran the CLI %d times", stub.detectCount)
	}
	stub.detectCount = 0
	if _, err := s.ListChatProjects(ListChatProjectsInput{}); err != nil {
		t.Fatal(err)
	}
	if stub.detectCount > 1 {
		t.Fatalf("the sidebar ran the CLI %d times", stub.detectCount)
	}
}

var _ externalagent.Adapter = (*cliCatalogStub)(nil)

// countingStore counts the reads that decode a pipeline's links with their snapshots.
type countingStore struct {
	*sqlite.Store
	heavy int
}

func (c *countingStore) ListPipelineSessions(ctx context.Context, pipelineID, role string) ([]catalog.PipelineSession, error) {
	c.heavy++
	return c.Store.ListPipelineSessions(ctx, pipelineID, role)
}

// A pipeline's links carry the project's whole file list; listing its conversations never decodes them.
func TestListingConversationsSkipsThePipelineSnapshots(t *testing.T) {
	_, db, vault := setup(t)
	store := &countingStore{Store: db}
	provider := &qaLabProvider{}
	s := NewService(t.Context(), Dependencies{Store: store, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return provider, nil }, ExecutionCacheRoot: t.TempDir()})
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	run, err := s.CreatePipeline(CreatePipelineInput{WorkspaceID: workspace.ID, Title: "Exportar", Objective: "CSV"})
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"discovery", "spec", "brainstorm", "plan"} {
		if _, err := s.SavePipelineArtifact(SavePipelineArtifactInput{PipelineID: run.ID, Stage: stage, Content: stage + " critérios"}); err != nil {
			t.Fatal(err)
		}
		if run, err = s.AdvancePipeline(run.ID); err != nil {
			t.Fatal(err)
		}
	}
	coder, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "local", Role: "coder", ConfirmWorkspaceCopy: true})
	if err != nil {
		t.Fatal(err)
	}
	store.heavy = 0
	list, err := s.ListSessions(ListSessionsInput{WorkspaceID: workspace.ID})
	if err != nil || len(list) == 0 {
		t.Fatalf("listed %d: %v", len(list), err)
	}
	found := false
	for _, item := range list {
		found = found || item.ID == coder.Session.ID && item.Resumable
	}
	if !found {
		t.Fatalf("the active Coder is no longer offered to continue: %+v", list)
	}
	if store.heavy != 0 {
		t.Fatalf("listing decoded the pipeline links %d times", store.heavy)
	}
}
