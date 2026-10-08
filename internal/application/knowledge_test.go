package application

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func TestKnowledgeSearchIsAvailableToNativeAgentWithProvenance(t *testing.T) {
	s, _, _ := setup(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source.md"), []byte("A resposta depende da proveniência local."), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportKnowledge(ImportKnowledgeInput{WorkspaceID: workspace.ID, Path: "source.md"}); err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "knowledge-call", Name: "knowledge_search", Arguments: []byte(`{"query":"proveniência","limit":5}`)}}
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "Encontre a fonte"})
	if err != nil || result.Status != RunCompleted {
		t.Fatalf("knowledge tool did not complete: %+v %v", result, err)
	}
	events, err := s.ListEvents(ListEventsInput{SessionID: session.ID})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "source.md") || !strings.Contains(string(data), "proveniência") {
		t.Fatalf("knowledge tool lost citation: %s", data)
	}
	agent, err := s.SaveAgent(SaveAgentInput{Name: "Pesquisa", Instructions: "Consulte fontes locais", BackendID: "local", AllowedTools: []string{"knowledge_search"}})
	if err != nil {
		t.Fatal(err)
	}
	agentSession, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: "local", AgentID: agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	provider.toolCall = &agentcore.ToolCall{ID: "knowledge-agent-call", Name: "knowledge_search", Arguments: []byte(`{"query":"proveniência"}`)}
	result, err = s.Prompt(PromptInput{SessionID: agentSession.ID, Text: "Encontre a fonte"})
	if err != nil || result.Status != RunCompleted {
		t.Fatalf("agent profile lost knowledge tool: %+v %v", result, err)
	}
}

func TestKnowledgeImportSearchReindexAndRemoveAcrossRestart(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "docs", "guide.md")
	if err := os.WriteFile(source, []byte("# Guia\n\n"+strings.Repeat("Contexto do projeto.\n", 90)+"\nA senha nunca deve entrar no índice de exemplo; termo unicórnio.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(t.TempDir(), "knowledge.db")
	store, err := sqlite.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(t.Context(), Dependencies{Store: store, External: map[string]ExternalBackend{}})
	workspace, err := service.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.ImportKnowledge(ImportKnowledgeInput{WorkspaceID: workspace.ID, Path: source})
	if err != nil {
		t.Fatal(err)
	}
	if first.Path != filepath.Join("docs", "guide.md") || first.ChunkCount < 2 || first.SourceModifiedAt.IsZero() || first.IndexedAt.IsZero() {
		t.Fatalf("missing document provenance or chunks: %+v", first)
	}
	hits, err := service.SearchKnowledge(SearchKnowledgeInput{WorkspaceID: workspace.ID, Query: "unicórnio", Limit: 10})
	if err != nil || len(hits) != 1 || hits[0].DocumentID != first.ID || hits[0].Path != first.Path || hits[0].LineStart < 1 || hits[0].SourceModifiedAt.IsZero() || !strings.Contains(hits[0].Snippet, "unicórnio") {
		t.Fatalf("search lost source citation: %+v, %v", hits, err)
	}
	unchanged, err := service.ImportKnowledge(ImportKnowledgeInput{WorkspaceID: workspace.ID, Path: filepath.Join("docs", "guide.md")})
	if err != nil || unchanged.ID != first.ID || !unchanged.IndexedAt.Equal(first.IndexedAt) || unchanged.ChunkCount != first.ChunkCount {
		t.Fatalf("identical import was not idempotent: %+v, %v", unchanged, err)
	}
	if err := os.WriteFile(source, []byte("# Guia\n\nA fonte agora contém o termo abacaxi.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	updated, err := service.ReindexKnowledge(KnowledgeDocumentInput{WorkspaceID: workspace.ID, DocumentID: first.ID})
	if err != nil || updated.ID != first.ID || updated.ChunkCount != 1 {
		t.Fatalf("reindex failed: %+v, %v", updated, err)
	}
	hits, err = service.SearchKnowledge(SearchKnowledgeInput{WorkspaceID: workspace.ID, Query: "unicórnio", Limit: 10})
	if err != nil || len(hits) != 0 {
		t.Fatalf("stale chunks remain: %+v, %v", hits, err)
	}
	Shutdown(service)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = sqlite.Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service = NewService(t.Context(), Dependencies{Store: store, External: map[string]ExternalBackend{}})
	listed, err := service.ListKnowledge(workspace.ID)
	if err != nil || len(listed) != 1 || listed[0].ID != first.ID {
		t.Fatalf("document missing after restart: %+v, %v", listed, err)
	}
	hits, err = service.SearchKnowledge(SearchKnowledgeInput{WorkspaceID: workspace.ID, Query: "abacaxi", Limit: 10})
	if err != nil || len(hits) != 1 || hits[0].Path != first.Path {
		t.Fatalf("FTS missing after restart: %+v, %v", hits, err)
	}
	if err := service.RemoveKnowledge(KnowledgeDocumentInput{WorkspaceID: workspace.ID, DocumentID: first.ID}); err != nil {
		t.Fatal(err)
	}
	hits, err = service.SearchKnowledge(SearchKnowledgeInput{WorkspaceID: workspace.ID, Query: "abacaxi", Limit: 10})
	if err != nil || len(hits) != 0 {
		t.Fatalf("removed document still searchable: %+v, %v", hits, err)
	}
}

func TestKnowledgeImportRejectsEscapesAndUnsupportedSources(t *testing.T) {
	service, _, _ := setup(t)
	root := t.TempDir()
	workspace, err := service.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("private content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "unsupported.pdf"), []byte("PDF"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "binary.txt"), []byte{'a', 0, 'b'}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(strings.Repeat("x", 2*1024*1024+1)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../outside.md", outside, "escape.md", "unsupported.pdf", "binary.txt", "large.txt", "docs/"} {
		t.Run(path, func(t *testing.T) {
			if _, err := service.ImportKnowledge(ImportKnowledgeInput{WorkspaceID: workspace.ID, Path: path}); err == nil {
				t.Fatalf("accepted unsafe or unsupported source %q", path)
			}
		})
	}
	listed, err := service.ListKnowledge(workspace.ID)
	if err != nil || len(listed) != 0 {
		t.Fatalf("rejected imports changed catalog: %+v, %v", listed, err)
	}
}

func TestKnowledgeSearchAndRemovalAreWorkspaceScoped(t *testing.T) {
	service, _, _ := setup(t)
	firstRoot, secondRoot := t.TempDir(), t.TempDir()
	for _, root := range []string{firstRoot, secondRoot} {
		if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("segredo local pesquisável"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	firstWorkspace, err := service.OpenWorkspace(firstRoot)
	if err != nil {
		t.Fatal(err)
	}
	secondWorkspace, err := service.OpenWorkspace(secondRoot)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.ImportKnowledge(ImportKnowledgeInput{WorkspaceID: firstWorkspace.ID, Path: "note.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ImportKnowledge(ImportKnowledgeInput{WorkspaceID: secondWorkspace.ID, Path: "note.txt"}); err != nil {
		t.Fatal(err)
	}
	hits, err := service.SearchKnowledge(SearchKnowledgeInput{WorkspaceID: firstWorkspace.ID, Query: "segredo", Limit: 10})
	if err != nil || len(hits) != 1 || hits[0].DocumentID != first.ID {
		t.Fatalf("cross-workspace result: %+v, %v", hits, err)
	}
	if err := service.RemoveKnowledge(KnowledgeDocumentInput{WorkspaceID: secondWorkspace.ID, DocumentID: first.ID}); !errors.Is(err, ErrKnowledgeNotFound) {
		t.Fatalf("cross-workspace removal accepted: %v", err)
	}
	if _, err := service.SearchKnowledge(SearchKnowledgeInput{WorkspaceID: firstWorkspace.ID, Query: `segredo OR *`, Limit: 10}); err != nil {
		t.Fatalf("query syntax escaped storage boundary: %v", err)
	}
}

func TestKnowledgeSearchFiltersByDocument(t *testing.T) {
	service, _, _ := setup(t)
	root := t.TempDir()
	for _, name := range []string{"first.md", "second.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("guia de integração local"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	workspace, err := service.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.ImportKnowledge(ImportKnowledgeInput{WorkspaceID: workspace.ID, Path: "first.md"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ImportKnowledge(ImportKnowledgeInput{WorkspaceID: workspace.ID, Path: "second.md"}); err != nil {
		t.Fatal(err)
	}
	hits, err := service.SearchKnowledge(SearchKnowledgeInput{WorkspaceID: workspace.ID, DocumentID: first.ID, Query: "integração", Limit: 10})
	if err != nil || len(hits) != 1 || hits[0].DocumentID != first.ID {
		t.Fatalf("document filter ignored: %+v, %v", hits, err)
	}
}

func TestKnowledgeReindexRejectsSourceRetargetedOutsideWorkspace(t *testing.T) {
	service, _, _ := setup(t)
	root := t.TempDir()
	source := filepath.Join(root, "note.md")
	if err := os.WriteFile(source, []byte("conteúdo autorizado verificável"), 0600); err != nil {
		t.Fatal(err)
	}
	workspace, err := service.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	indexed, err := service.ImportKnowledge(ImportKnowledgeInput{WorkspaceID: workspace.ID, Path: "note.md"})
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("conteúdo fora do projeto"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, filepath.Join(root, "saved.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, source); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := service.ReindexKnowledge(KnowledgeDocumentInput{WorkspaceID: workspace.ID, DocumentID: indexed.ID}); !errors.Is(err, ErrKnowledgeSourceUnavailable) {
		t.Fatalf("retargeted source was opened: %v", err)
	}
	hits, err := service.SearchKnowledge(SearchKnowledgeInput{WorkspaceID: workspace.ID, Query: "verificável", Limit: 10})
	if err != nil || len(hits) != 1 || hits[0].DocumentID != indexed.ID {
		t.Fatalf("failed reindex damaged prior index: %+v, %v", hits, err)
	}
}
