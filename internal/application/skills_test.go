package application

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/secrets"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func TestImportSkillConfinesSourceAndEnablesSnapshot(t *testing.T) {
	s, _, _ := setup(t)
	root := t.TempDir()
	workspace, err := s.OpenWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, ".agents", "skills", "review")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: review\ndescription: Revise a cobertura\n---\nRevise os testes antes de concluir.\n"
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	imported, err := s.ImportSkill(ImportSkillInput{WorkspaceID: workspace.ID, Path: ".agents/skills/review/SKILL.md", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if imported.Name != "review" || imported.Description != "Revise a cobertura" || imported.Content != strings.TrimSpace(content) || !imported.Enabled {
		t.Fatalf("imported wrong skill: %+v", imported)
	}
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(strings.ReplaceAll(content, "Revise os testes", "Revise também os testes")), 0o600); err != nil {
		t.Fatal(err)
	}
	reimported, err := s.ImportSkill(ImportSkillInput{WorkspaceID: workspace.ID, Path: ".agents/skills/review/SKILL.md", Enabled: true})
	if err != nil || reimported.ID != imported.ID || reimported.Revision != imported.Revision+1 || !strings.Contains(reimported.Content, "também") {
		t.Fatalf("reimport did not version same source: %+v %v", reimported, err)
	}
	snapshot, err := s.enabledSkillSnapshot(workspace.ID)
	if err != nil || !strings.Contains(snapshot, "Revise também os testes") {
		t.Fatalf("missing imported skill snapshot: %q %v", snapshot, err)
	}
	outside := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(outside, []byte("SECRET OUTSIDE"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportSkill(ImportSkillInput{WorkspaceID: workspace.ID, Path: outside, Enabled: true}); err == nil {
		t.Fatal("absolute path escaped workspace")
	}
	escapeDir := filepath.Join(root, ".agents", "skills", "escape")
	if err := os.MkdirAll(escapeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(escapeDir, "SKILL.md")); err == nil {
		if _, err := s.ImportSkill(ImportSkillInput{WorkspaceID: workspace.ID, Path: ".agents/skills/escape/SKILL.md", Enabled: true}); err == nil {
			t.Fatal("symlink escaped workspace")
		}
	}
}

func TestEnabledSkillIsSnapshottedIntoSessionAcrossEditAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skills.db")
	db, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	vault := &memorySecrets{values: map[secrets.Reference]string{}}
	provider := &fakeProvider{}
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return provider, nil }})
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	skill, err := s.SaveSkill(SaveSkillInput{WorkspaceID: workspace.ID, Name: "Revisão", Description: "Analisa testes", Content: "Revise cobertura antes de concluir", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "Analise"}); err != nil {
		t.Fatal(err)
	}
	if len(provider.request.Messages) == 0 || !strings.Contains(provider.request.Messages[0].Content, "Revise cobertura") {
		t.Fatalf("skill not used: %+v", provider.request.Messages)
	}
	if _, err := s.SaveSkill(SaveSkillInput{ID: skill.ID, WorkspaceID: workspace.ID, Name: "Revisão", Description: "Editada", Content: "NOVO CONTEÚDO", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	Shutdown(s)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	provider = &fakeProvider{}
	s = NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return provider, nil }})
	if _, err := s.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: workspace.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "Continue"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(provider.request.Messages[0].Content, "Revise cobertura") || strings.Contains(provider.request.Messages[0].Content, "NOVO CONTEÚDO") {
		t.Fatalf("skill snapshot changed: %+v", provider.request.Messages)
	}
}
