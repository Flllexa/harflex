package repositories

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeSnapshotFile(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitSnapshotRepo(t *testing.T, root string) {
	t.Helper()
	gitSnapshot(t, root, "add", "-A")
	gitSnapshot(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-q", "-m", "baseline")
}

func initSnapshotRepo(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for path, content := range files {
		writeSnapshotFile(t, root, path, content)
	}
	gitSnapshot(t, root, "init", "-q", "-b", "main")
	commitSnapshotRepo(t, root)
}

// multiRepoWorkspace builds a workspace like the Destrava one: the root repository ignores domains/*, each domain is a
// repository of its own (one reached through a symlink to a folder elsewhere), and the root has a worktree of itself
// under an ignored folder.
func multiRepoWorkspace(t *testing.T) (root, linked string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "workspace")
	initSnapshotRepo(t, root, map[string]string{".gitignore": "domains/*\n!domains/.gitkeep\n.claude/\n.local/\nnode_modules/\n", "domains/.gitkeep": "", "README.md": "workspace\n"})
	initSnapshotRepo(t, filepath.Join(root, "domains", "contracts"), map[string]string{"baixa.go": "package contracts\n\nfunc Baixa() bool { return false }\n", "nested/rules.go": "package nested\n"})
	linked = filepath.Join(base, "settlement-elsewhere")
	initSnapshotRepo(t, linked, map[string]string{"settle.go": "package settlement\n"})
	if err := os.Symlink(linked, filepath.Join(root, "domains", "settlement")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	// Untracked files do not keep a repository out; a changed tracked file does.
	initSnapshotRepo(t, filepath.Join(root, "domains", "onboarding"), map[string]string{"app.go": "package onboarding\n"})
	writeSnapshotFile(t, root, "domains/onboarding/.DS_Store", "x")
	initSnapshotRepo(t, filepath.Join(root, "domains", "strategies"), map[string]string{"plan.md": "v1\n"})
	writeSnapshotFile(t, root, "domains/strategies/plan.md", "v2 not committed\n")
	gitSnapshot(t, root, "worktree", "add", "-q", filepath.Join(root, ".claude", "worktrees", "other"), "-b", "other")
	// Tools keep worktrees of the domain repositories in hidden folders too; they are not part of the project.
	gitSnapshot(t, filepath.Join(root, "domains", "contracts"), "worktree", "add", "-q", filepath.Join(root, ".claude", "worktrees", "contracts-fix"), "-b", "fix")
	// An ignored dependency folder sorted before domains/ must not use up the search.
	for index := 0; index < 2100; index++ {
		writeSnapshotFile(t, root, fmt.Sprintf("api-docs/node_modules/pkg%04d/index.js", index), "")
	}
	for index := 0; index < 450; index++ {
		writeSnapshotFile(t, root, filepath.Join(".local", "reports", "run", "r"+strings.Repeat("x", index%7)+string(rune('a'+index%26))+filepath.Base(t.TempDir()), "out.json"), "{}")
	}
	return root, linked
}

func TestExecutionSnapshotCopiesAndAppliesNestedRepositories(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	ctx := context.Background()
	root, linked := multiRepoWorkspace(t)
	canonicalRoot, _ := filepath.EvalSymlinks(root)
	canonicalLinked, _ := filepath.EvalSymlinks(linked)

	preview, err := PreviewExecutionSnapshot(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	var previewPaths []string
	for _, repository := range preview.Repositories {
		previewPaths = append(previewPaths, repository.Path)
	}
	if !reflect.DeepEqual(previewPaths, []string{"domains/contracts", "domains/onboarding", "domains/settlement"}) ||
		!reflect.DeepEqual(preview.SkippedRepositories, []ExecutionSkippedRepository{{Path: "domains/strategies", Reason: SkippedRepositoryUncommitted}}) {
		t.Fatalf("preview repositories = %+v skipped = %+v", preview.Repositories, preview.SkippedRepositories)
	}
	for _, excluded := range preview.ExcludedPaths {
		if excluded == "domains/contracts" || excluded == "domains/settlement" {
			t.Fatalf("an included repository is listed as excluded: %v", preview.ExcludedPaths)
		}
	}

	prepared, err := PrepareExecutionSnapshotAt(ctx, root, false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(prepared.Root) })
	if len(prepared.Repositories) != 3 || !samePath(prepared.Repositories[2].SourceRoot, canonicalLinked) || prepared.Repositories[0].Branch != "main" {
		t.Fatalf("snapshot repositories = %+v", prepared.Repositories)
	}
	for _, path := range []string{"domains/contracts/baixa.go", "domains/contracts/nested/rules.go", "domains/settlement/settle.go", "domains/onboarding/app.go"} {
		if _, err := os.Stat(filepath.Join(prepared.Root, filepath.FromSlash(path))); err != nil {
			t.Fatalf("%s is not in the copy: %v", path, err)
		}
	}
	for _, path := range []string{"domains/strategies", "domains/onboarding/.DS_Store", ".claude", "domains/contracts/.git"} {
		if _, err := os.Lstat(filepath.Join(prepared.Root, filepath.FromSlash(path))); !os.IsNotExist(err) {
			t.Fatalf("%s must stay out of the copy: %v", path, err)
		}
	}
	if info, err := os.Lstat(filepath.Join(prepared.Root, "domains", "settlement")); err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		t.Fatalf("a linked repository is a plain folder in the copy: %v %v", info, err)
	}

	// The Coder changes two repositories and adds a file to one of them.
	writeSnapshotFile(t, prepared.Root, "domains/contracts/baixa.go", "package contracts\n\nfunc Baixa() bool { return true }\n")
	writeSnapshotFile(t, prepared.Root, "domains/contracts/nested/new.go", "package nested\n\nconst New = 1\n")
	writeSnapshotFile(t, prepared.Root, "domains/settlement/settle.go", "package settlement\n\n// settled\n")
	diff, changed, err := ExecutionDiff(ctx, prepared, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 3 || !strings.Contains(diff, "diff --git a/domains/contracts/baixa.go b/domains/contracts/baixa.go") || !strings.Contains(diff, "-func Baixa() bool { return false }") || !strings.Contains(diff, "a/domains/settlement/settle.go") {
		t.Fatalf("diff = %s", diff)
	}

	if _, err := ApplyExecutionSnapshot(ctx, prepared, 1<<20); err != nil {
		t.Fatal(err)
	}
	if content, _ := os.ReadFile(filepath.Join(root, "domains", "contracts", "baixa.go")); !strings.Contains(string(content), "return true") {
		t.Fatalf("contracts not applied: %s", content)
	}
	if content, _ := os.ReadFile(filepath.Join(canonicalLinked, "settle.go")); !strings.Contains(string(content), "// settled") {
		t.Fatalf("the linked settlement repository not applied: %s", content)
	}
	if status := gitSnapshot(t, root, "status", "--porcelain"); status != "" {
		t.Fatalf("the workspace repository must stay clean: %q", status)
	}
	if status := gitSnapshot(t, filepath.Join(canonicalRoot, "domains", "contracts"), "status", "--porcelain", "-uall"); status != "M baixa.go\n?? nested/new.go" && status != "M baixa.go\n?? nested/new.go\n" {
		t.Fatalf("contracts status = %q", status)
	}
	if applied, err := ExecutionSnapshotApplied(ctx, prepared); err != nil || !applied {
		t.Fatalf("applied = %v, %v", applied, err)
	}
}

func TestExecutionSnapshotDriftsWhenANestedRepositoryMoves(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	ctx := context.Background()
	root, _ := multiRepoWorkspace(t)
	prepared, err := PrepareExecutionSnapshotAt(ctx, root, false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(prepared.Root) })
	if matches, err := ExecutionSourceStillMatches(ctx, prepared); err != nil || !matches {
		t.Fatalf("fresh snapshot must match: %v %v", matches, err)
	}
	contracts := filepath.Join(root, "domains", "contracts")
	writeSnapshotFile(t, contracts, "later.go", "package contracts\n")
	commitSnapshotRepo(t, contracts)
	if matches, err := ExecutionSourceStillMatches(ctx, prepared); err != nil || matches {
		t.Fatalf("a new commit in a nested repository is drift: %v %v", matches, err)
	}
}
