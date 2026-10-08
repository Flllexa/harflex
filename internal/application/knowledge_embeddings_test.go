package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func TestLocalEmbeddingIndexResumesAndSearchReportsMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var request struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request.Model != "local-model" || len(request.Input) > 8 {
			t.Errorf("invalid batch: %+v", request)
		}
		vectors := make([][]float64, len(request.Input))
		for index, input := range request.Input {
			vectors[index] = []float64{1, 0}
			if strings.Contains(input, "bússola") {
				vectors[index] = []float64{0, 1}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": vectors})
	}))
	root := t.TempDir()
	content := strings.Repeat("alfabeto ", 1400) + "\nbússola aponta a resposta\n"
	if err := os.WriteFile(filepath.Join(root, "source.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "knowledge.db")
	store, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(t.Context(), Dependencies{Store: store, External: map[string]ExternalBackend{}})
	workspace, err := service.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ImportKnowledge(ImportKnowledgeInput{WorkspaceID: workspace.ID, Path: "source.md"}); err != nil {
		t.Fatal(err)
	}
	textual, err := service.SearchKnowledgeDetailed(SearchKnowledgeInput{WorkspaceID: workspace.ID, Query: "bússola", Limit: 5})
	if err != nil || textual.Mode != "textual" || len(textual.Hits) != 1 {
		t.Fatalf("textual baseline = %+v, %v", textual, err)
	}
	profile, err := service.SaveKnowledgeEmbeddingProfile(SaveKnowledgeEmbeddingProfileInput{WorkspaceID: workspace.ID, Kind: "ollama", BaseURL: server.URL, Model: "local-model"})
	if err != nil || !profile.Configured || profile.Progress.Indexed != 0 || profile.Progress.Total < 9 {
		t.Fatalf("profile = %+v, %v", profile, err)
	}
	first, err := service.IndexKnowledgeVectors(IndexKnowledgeVectorsInput{WorkspaceID: workspace.ID, ExpectedFingerprint: profile.Fingerprint})
	if err != nil || first.Progress.Indexed != 8 || first.Progress.Total <= first.Progress.Indexed {
		t.Fatalf("first checkpoint = %+v, %v", first, err)
	}
	Shutdown(service)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service = NewService(t.Context(), Dependencies{Store: store, External: map[string]ExternalBackend{}})
	for attempt := 0; attempt < 10; attempt++ {
		profile, err = service.IndexKnowledgeVectors(IndexKnowledgeVectorsInput{WorkspaceID: workspace.ID, ExpectedFingerprint: profile.Fingerprint})
		if err != nil {
			t.Fatal(err)
		}
		if profile.Progress.Indexed == profile.Progress.Total {
			break
		}
	}
	if profile.Progress.Indexed != profile.Progress.Total || profile.Progress.Dimension != 2 {
		t.Fatalf("incomplete index: %+v", profile)
	}
	hybrid, err := service.SearchKnowledgeDetailed(SearchKnowledgeInput{WorkspaceID: workspace.ID, Query: "bússola", Limit: 5})
	if err != nil || hybrid.Mode != "hybrid" || len(hybrid.Hits) < 2 || hybrid.Hits[0].Path != "source.md" || hybrid.Hits[0].LineStart < 1 || !strings.Contains(hybrid.Hits[0].Snippet, "bússola") {
		t.Fatalf("hybrid result = %+v, %v", hybrid, err)
	}
	toolResult, err := (knowledgeSearchTool{service: service, workspaceID: workspace.ID}).Execute(context.Background(), json.RawMessage(`{"query":"bússola","limit":5}`), nil)
	if err != nil || !strings.Contains(string(toolResult.Content), `"mode":"hybrid"`) || !strings.Contains(string(toolResult.Content), `"path":"source.md"`) {
		t.Fatalf("agent tool lost hybrid mode or citation: %s %v", toolResult.Content, err)
	}
	semantic, err := service.SearchKnowledgeDetailed(SearchKnowledgeInput{WorkspaceID: workspace.ID, Query: "nenhuma-palavra-igual", Limit: 5})
	if err != nil || semantic.Mode != "semantic" || len(semantic.Hits) == 0 {
		t.Fatalf("semantic result = %+v, %v", semantic, err)
	}
	server.Close()
	fallback, err := service.SearchKnowledgeDetailed(SearchKnowledgeInput{WorkspaceID: workspace.ID, Query: "bússola", Limit: 5})
	if err != nil || fallback.Mode != "textual_fallback" || len(fallback.Hits) != 1 {
		t.Fatalf("unavailable endpoint did not fallback: %+v, %v", fallback, err)
	}
}

func TestCancelledVectorBatchLeavesDurableCheckpointForRetry(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseBatch := func() { releaseOnce.Do(func() { close(release) }) }
	var respond atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !respond.Load() {
			close(started)
			<-release
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float64{{1, 0}}})
	}))
	defer func() { releaseBatch(); server.Close() }()
	path := filepath.Join(t.TempDir(), "cancel.db")
	store, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx, cancel := context.WithCancel(context.Background())
	service := NewService(ctx, Dependencies{Store: store, External: map[string]ExternalBackend{}})
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source.md"), []byte("small local source"), 0600); err != nil {
		t.Fatal(err)
	}
	workspace, err := service.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ImportKnowledge(ImportKnowledgeInput{WorkspaceID: workspace.ID, Path: "source.md"}); err != nil {
		t.Fatal(err)
	}
	configured, err := service.SaveKnowledgeEmbeddingProfile(SaveKnowledgeEmbeddingProfileInput{WorkspaceID: workspace.ID, Kind: "ollama", BaseURL: server.URL, Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	completed := make(chan error, 1)
	go func() {
		_, err := service.IndexKnowledgeVectors(IndexKnowledgeVectorsInput{WorkspaceID: workspace.ID, ExpectedFingerprint: configured.Fingerprint})
		completed <- err
	}()
	<-started
	cancel()
	releaseBatch()
	if err := <-completed; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	Shutdown(service)
	respond.Store(true)
	service = NewService(t.Context(), Dependencies{Store: store, External: map[string]ExternalBackend{}})
	profile, err := service.GetKnowledgeEmbeddingProfile(workspace.ID)
	if err != nil || profile.Progress.Indexed != 0 || profile.Progress.Total != 1 {
		t.Fatalf("cancelled batch committed: %+v, %v", profile, err)
	}
	profile, err = service.IndexKnowledgeVectors(IndexKnowledgeVectorsInput{WorkspaceID: workspace.ID, ExpectedFingerprint: configured.Fingerprint})
	if err != nil || profile.Progress.Indexed != 1 {
		t.Fatalf("retry failed: %+v, %v", profile, err)
	}
}

func TestKnowledgeSearchDoesNotUseVectorsAfterProfileChangesMidQuery(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var blockQuery atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if blockQuery.Load() && len(request.Input) == 1 && request.Input[0] == "different question" {
			close(started)
			<-release
		}
		vectors := make([][]float64, len(request.Input))
		for index := range vectors {
			vectors[index] = []float64{1, 0}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": vectors})
	}))
	defer server.Close()
	service, _, _ := setup(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source.md"), []byte("local knowledge source"), 0600); err != nil {
		t.Fatal(err)
	}
	workspace, err := service.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ImportKnowledge(ImportKnowledgeInput{WorkspaceID: workspace.ID, Path: "source.md"}); err != nil {
		t.Fatal(err)
	}
	configured, err := service.SaveKnowledgeEmbeddingProfile(SaveKnowledgeEmbeddingProfileInput{WorkspaceID: workspace.ID, Kind: "ollama", BaseURL: server.URL, Model: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.IndexKnowledgeVectors(IndexKnowledgeVectorsInput{WorkspaceID: workspace.ID, ExpectedFingerprint: configured.Fingerprint}); err != nil {
		t.Fatal(err)
	}
	blockQuery.Store(true)
	type answer struct {
		result KnowledgeSearchDTO
		err    error
	}
	completed := make(chan answer, 1)
	go func() {
		result, err := service.SearchKnowledgeDetailed(SearchKnowledgeInput{WorkspaceID: workspace.ID, Query: "different question", Limit: 5})
		completed <- answer{result, err}
	}()
	<-started
	if _, err := service.SaveKnowledgeEmbeddingProfile(SaveKnowledgeEmbeddingProfileInput{WorkspaceID: workspace.ID, Kind: "ollama", BaseURL: server.URL, Model: "second"}); err != nil {
		t.Fatal(err)
	}
	close(release)
	response := <-completed
	if response.err != nil || response.result.Mode != "textual" || len(response.result.Hits) != 0 {
		t.Fatalf("old vectors crossed profile switch: %+v, %v", response.result, response.err)
	}
}

func TestVectorIndexRequiresProfileSeenByCallerBeforeSendingSource(t *testing.T) {
	var oldCalls, newCalls atomic.Int32
	server := func(calls *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float64{{1, 0}}})
		}))
	}
	oldServer := server(&oldCalls)
	defer oldServer.Close()
	newServer := server(&newCalls)
	defer newServer.Close()
	service, _, _ := setup(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source.md"), []byte("private source for local indexing"), 0600); err != nil {
		t.Fatal(err)
	}
	workspace, err := service.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ImportKnowledge(ImportKnowledgeInput{WorkspaceID: workspace.ID, Path: "source.md"}); err != nil {
		t.Fatal(err)
	}
	oldProfile, err := service.SaveKnowledgeEmbeddingProfile(SaveKnowledgeEmbeddingProfileInput{WorkspaceID: workspace.ID, Kind: "ollama", BaseURL: oldServer.URL, Model: "old-model"})
	if err != nil {
		t.Fatal(err)
	}
	newProfile, err := service.SaveKnowledgeEmbeddingProfile(SaveKnowledgeEmbeddingProfileInput{WorkspaceID: workspace.ID, Kind: "ollama", BaseURL: newServer.URL, Model: "new-model"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.IndexKnowledgeVectors(IndexKnowledgeVectorsInput{WorkspaceID: workspace.ID, ExpectedFingerprint: oldProfile.Fingerprint}); !errors.Is(err, ErrEmbeddingProfileChanged) {
		t.Fatalf("stale profile was not rejected: %v", err)
	}
	if _, err := service.IndexKnowledgeVectors(IndexKnowledgeVectorsInput{WorkspaceID: workspace.ID}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unconfirmed profile was not rejected: %v", err)
	}
	if oldCalls.Load() != 0 || newCalls.Load() != 0 {
		t.Fatalf("source sent without matching consent: old=%d new=%d", oldCalls.Load(), newCalls.Load())
	}
	if _, err := service.IndexKnowledgeVectors(IndexKnowledgeVectorsInput{WorkspaceID: workspace.ID, ExpectedFingerprint: newProfile.Fingerprint}); err != nil || newCalls.Load() != 1 {
		t.Fatalf("confirmed profile did not index: calls=%d err=%v", newCalls.Load(), err)
	}
}
