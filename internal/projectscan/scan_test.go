package projectscan

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
}

func TestScanKeepsDocsManifestsAndNestedRepositoriesButNoSecrets(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	write(t, root, "README.md", "# Workspace\nServiços de pagamento.")
	write(t, root, "AGENTS.md", "Regras do workspace.")
	write(t, root, "go.mod", "module example.com/workspace\n")
	write(t, root, "docs/architecture.md", "Arquitetura orientada a eventos.")
	write(t, root, ".env", "API_KEY=supersecret")
	write(t, root, "config/credentials.json", "{\"token\":\"x\"}")
	write(t, root, "node_modules/lib/README.md", "não deve entrar")
	write(t, root, "domains/billing/README.md", "Domínio de cobrança.")
	write(t, root, "domains/billing/template.yaml", "Resources: {}")
	write(t, root, "big.md", strings.Repeat("x", MaxFileBytes*2))
	git(t, root, "init", "-q", "-b", "main")
	git(t, filepath.Join(root, "domains", "billing"), "init", "-q", "-b", "main")
	git(t, filepath.Join(root, "domains", "billing"), "remote", "add", "origin", "https://user:token123@bitbucket.org/acme/billing.git")

	snapshot, err := Scan(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	sources := snapshot.Sources()
	for _, want := range []string{"README.md", "AGENTS.md", "go.mod", "docs/architecture.md", "domains/billing/README.md", "domains/billing/template.yaml"} {
		if !slices.Contains(sources, want) {
			t.Fatalf("missing %s in %v", want, sources)
		}
	}
	for _, file := range snapshot.Files {
		if strings.Contains(file.Content, "supersecret") || strings.Contains(file.Path, "credentials") || strings.Contains(file.Path, "node_modules") || len(file.Content) > MaxFileBytes {
			t.Fatalf("unsafe or oversized file kept: %s", file.Path)
		}
	}
	if !slices.Contains(sources[:2], "README.md") || !slices.Contains(sources[:2], "AGENTS.md") {
		t.Fatalf("the root guides come first, got %v", sources)
	}
	var billing *Repository
	for i := range snapshot.Repositories {
		if snapshot.Repositories[i].Path == "domains/billing" {
			billing = &snapshot.Repositories[i]
		}
	}
	if len(snapshot.Repositories) != 2 || billing == nil || billing.Remote != "https://bitbucket.org/acme/billing.git" || billing.Branch != "main" {
		t.Fatalf("repositories = %+v", snapshot.Repositories)
	}
	if !slices.Contains(snapshot.Tree, "domains/") || !slices.Contains(snapshot.Tree, "domains/billing/") || slices.Contains(snapshot.Tree, "node_modules/") {
		t.Fatalf("tree = %v", snapshot.Tree)
	}
}

func TestSafeRemoteDropsCredentials(t *testing.T) {
	for input, want := range map[string]string{
		"https://user:tok@github.com/a/b.git": "https://github.com/a/b.git",
		"git@bitbucket.org:org/repo.git":      "bitbucket.org:org/repo.git",
		"":                                    "",
	} {
		if got := safeRemote(input); got != want {
			t.Fatalf("%q: got %q want %q", input, got, want)
		}
	}
}
