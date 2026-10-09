package repositories

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitAll(t *testing.T, dir, message string) {
	t.Helper()
	gitSnapshot(t, dir, "add", "-A")
	gitSnapshot(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", message)
}

type worktreeFixture struct {
	main    string
	root    string
	merged  string
	dirty   string
	ahead   string
	locked  string
	free    string // detached HEAD with a commit no branch holds
	ignored string
	midway  string // merge stopped on a conflict
}

// newWorktreeFixture builds one repository whose linked worktrees each hold a different kind of state.
func newWorktreeFixture(t *testing.T) worktreeFixture {
	t.Helper()
	requireGit(t)
	base := t.TempDir()
	canonical, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	f := worktreeFixture{root: canonical, main: filepath.Join(canonical, "main")}
	if err := os.MkdirAll(f.main, 0o755); err != nil {
		t.Fatal(err)
	}
	gitSnapshot(t, f.main, "init", "-b", "main")
	writeFile(t, filepath.Join(f.main, "README.md"), "base\n")
	writeFile(t, filepath.Join(f.main, "conflict.txt"), "from main\n")
	writeFile(t, filepath.Join(f.main, ".gitignore"), "node_modules/\n")
	commitAll(t, f.main, "base")
	add := func(name string, extra ...string) string {
		path := filepath.Join(canonical, name)
		gitSnapshot(t, f.main, append([]string{"worktree", "add"}, append(extra, path)...)...)
		return path
	}
	f.merged = add("merged", "-b", "task/merged")
	f.dirty = add("dirty", "-b", "task/dirty")
	writeFile(t, filepath.Join(f.dirty, "README.md"), "edited\n")
	writeFile(t, filepath.Join(f.dirty, "new.txt"), "untracked\n")
	f.ahead = add("ahead", "-b", "task/ahead")
	writeFile(t, filepath.Join(f.ahead, "ahead.txt"), "only here\n")
	commitAll(t, f.ahead, "ahead")
	f.locked = add("locked", "-b", "task/locked")
	gitSnapshot(t, f.main, "worktree", "lock", "--reason", "on a removable disk", f.locked)
	f.free = add("free", "--detach")
	writeFile(t, filepath.Join(f.free, "free.txt"), "unreachable\n")
	commitAll(t, f.free, "loose commit")
	f.ignored = add("ignored", "-b", "task/ignored")
	writeFile(t, filepath.Join(f.ignored, "node_modules", "pkg", "index.js"), "module.exports = 1\n")
	f.midway = add("midway", "-b", "task/midway")
	writeFile(t, filepath.Join(f.midway, "conflict.txt"), "from task\n")
	commitAll(t, f.midway, "task side")
	writeFile(t, filepath.Join(f.main, "conflict.txt"), "from main again\n")
	commitAll(t, f.main, "main side")
	if output, err := exec.Command("git", "-C", f.midway, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "merge", "main").CombinedOutput(); err == nil {
		t.Fatalf("the merge should have stopped on a conflict: %s", output)
	}
	return f
}

func findWorktree(t *testing.T, set WorktreeSet, path string) Worktree {
	t.Helper()
	item, ok := set.Find(path)
	if !ok {
		t.Fatalf("worktree %s not listed in %+v", path, set.Items)
	}
	return item
}

func TestListWorktreesJudgesWhatEachOneHolds(t *testing.T) {
	f := newWorktreeFixture(t)
	set, err := ListWorktrees(context.Background(), f.main)
	if err != nil {
		t.Fatal(err)
	}
	if set.Base != "main" || !set.BaseKnown || len(set.Items) != 8 {
		t.Fatalf("set: base=%q known=%v items=%d", set.Base, set.BaseKnown, len(set.Items))
	}
	cases := []struct {
		name string
		path string
		want []string
	}{
		{"main worktree", f.main, []string{BlockerMainWorktree}},
		{"clean and merged", f.merged, nil},
		{"edited and untracked files", f.dirty, []string{BlockerUncommitted}},
		{"commits only on its branch", f.ahead, []string{BlockerUnmerged}},
		{"locked", f.locked, []string{BlockerLocked}},
		{"detached with a loose commit", f.free, []string{BlockerDetachedCommits}},
		{"ignored files only", f.ignored, nil},
		{"merge stopped on a conflict", f.midway, []string{BlockerOperation, BlockerUncommitted, BlockerUnmerged}},
	}
	for _, c := range cases {
		item := findWorktree(t, set, c.path)
		got := SortedBlockerCodes(item.DeleteBlockers(set, nil))
		want := append([]string(nil), c.want...)
		if len(want) == 0 {
			want = []string{}
		}
		if !reflect.DeepEqual(got, sortedCopy(want)) {
			t.Fatalf("%s: blockers %v, want %v (%+v)", c.name, got, want, item)
		}
	}
	dirty := findWorktree(t, set, f.dirty)
	if dirty.Changes.Total != 2 || dirty.Changes.Unstaged != 1 || dirty.Changes.Untracked != 1 {
		t.Fatalf("dirty changes: %+v", dirty.Changes)
	}
	if ahead := findWorktree(t, set, f.ahead); ahead.Ahead != 1 || ahead.Merged || ahead.Branch != "task/ahead" {
		t.Fatalf("ahead state: %+v", ahead)
	}
	if merged := findWorktree(t, set, f.merged); !merged.Merged || merged.Ahead != 0 {
		t.Fatalf("merged state: %+v", merged)
	}
	if midway := findWorktree(t, set, f.midway); midway.Operation != "merge" || midway.Changes.Conflicts != 1 {
		t.Fatalf("midway: operation=%q conflicts=%d", midway.Operation, midway.Changes.Conflicts)
	}
	if locked := findWorktree(t, set, f.locked); !locked.Locked || locked.LockReason != "on a removable disk" {
		t.Fatalf("locked: %+v", locked)
	}
	if ignored := findWorktree(t, set, f.ignored); len(ignored.Ignored) != 1 || ignored.Ignored[0] != "node_modules/" {
		t.Fatalf("ignored entries: %+v", ignored.Ignored)
	}
}

func sortedCopy(values []string) []string {
	out := append([]string{}, values...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func TestListWorktreesWorksFromALinkedWorktree(t *testing.T) {
	f := newWorktreeFixture(t)
	set, err := ListWorktrees(context.Background(), f.dirty)
	if err != nil {
		t.Fatal(err)
	}
	if set.Root != f.main || len(set.Items) != 8 || !set.Items[0].IsMain {
		t.Fatalf("a linked worktree must list the whole repository: root=%q items=%d", set.Root, len(set.Items))
	}
}

func TestListWorktreesRejectsAFolderOutsideAnyRepository(t *testing.T) {
	requireGit(t)
	if _, err := ListWorktrees(context.Background(), t.TempDir()); !errors.Is(err, ErrNotRepository) {
		t.Fatalf("error = %v, want ErrNotRepository", err)
	}
}

func TestRemoveWorktreeDeletesOnlyWhatIsSaved(t *testing.T) {
	f := newWorktreeFixture(t)
	ctx := context.Background()
	for _, c := range []struct {
		name string
		path string
		code string
	}{
		{"edited files", f.dirty, BlockerUncommitted},
		{"unmerged commits", f.ahead, BlockerUnmerged},
		{"locked", f.locked, BlockerLocked},
		{"loose detached commit", f.free, BlockerDetachedCommits},
		{"the main worktree", f.main, BlockerMainWorktree},
		{"a stopped merge", f.midway, BlockerOperation},
	} {
		_, err := RemoveWorktree(ctx, f.main, c.path, RemoveOptions{DeleteBranch: true, AcknowledgeIgnored: true})
		var refused *NotDeletableError
		if !errors.As(err, &refused) || !errors.Is(err, ErrWorktreeNotDeletable) {
			t.Fatalf("%s: error = %v, want a refusal", c.name, err)
		}
		found := false
		for _, blocker := range refused.Blockers {
			found = found || blocker.Code == c.code
		}
		if !found {
			t.Fatalf("%s: blockers %+v do not include %s", c.name, refused.Blockers, c.code)
		}
		if _, err := os.Stat(c.path); err != nil {
			t.Fatalf("%s: the refused worktree disappeared: %v", c.name, err)
		}
	}
	if body, _ := os.ReadFile(filepath.Join(f.dirty, "new.txt")); string(body) != "untracked\n" {
		t.Fatalf("the untracked file did not survive a refused removal: %q", body)
	}

	removed, err := RemoveWorktree(ctx, f.main, f.merged, RemoveOptions{DeleteBranch: true})
	if err != nil || !removed.Removed || !removed.BranchDeleted || removed.Branch != "task/merged" {
		t.Fatalf("removing a clean merged worktree = %+v, %v", removed, err)
	}
	if _, err := os.Stat(f.merged); !os.IsNotExist(err) {
		t.Fatalf("the merged worktree directory is still there: %v", err)
	}
	if output := gitSnapshot(t, f.main, "branch", "--list", "task/merged"); output != "" {
		t.Fatalf("the merged branch survived: %q", output)
	}
	if output := gitSnapshot(t, f.main, "branch", "--list", "task/ahead"); !strings.Contains(output, "task/ahead") {
		t.Fatal("an unrelated branch was touched")
	}
}

func TestRemoveWorktreeAsksBeforeDiscardingIgnoredFiles(t *testing.T) {
	f := newWorktreeFixture(t)
	ctx := context.Background()
	_, err := RemoveWorktree(ctx, f.main, f.ignored, RemoveOptions{})
	var refused *NotDeletableError
	if !errors.As(err, &refused) || len(refused.Blockers) != 1 || refused.Blockers[0].Code != BlockerIgnoredFiles || refused.Blockers[0].Count != 1 {
		t.Fatalf("ignored files must be acknowledged first, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.ignored, "node_modules", "pkg", "index.js")); err != nil {
		t.Fatalf("the ignored file was lost without acknowledgement: %v", err)
	}
	if result, err := RemoveWorktree(ctx, f.main, f.ignored, RemoveOptions{AcknowledgeIgnored: true, DeleteBranch: true}); err != nil || !result.Removed {
		t.Fatalf("acknowledged removal = %+v, %v", result, err)
	}
}

func TestRemoveWorktreeNeverTouchesAProtectedOrUnlistedPath(t *testing.T) {
	f := newWorktreeFixture(t)
	ctx := context.Background()
	_, err := RemoveWorktree(ctx, f.main, f.merged, RemoveOptions{Protected: []string{f.merged}})
	var refused *NotDeletableError
	if !errors.As(err, &refused) || refused.Blockers[0].Code != BlockerCurrentProject {
		t.Fatalf("the open project must be protected, got %v", err)
	}
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "keep.txt"), "mine\n")
	for _, path := range []string{outside, filepath.Join(f.root, "does-not-exist"), filepath.Join(f.main, ".git"), f.root} {
		if _, err := RemoveWorktree(ctx, f.main, path, RemoveOptions{AcknowledgeIgnored: true}); !errors.Is(err, ErrWorktreeNotFound) {
			t.Fatalf("%s: error = %v, want ErrWorktreeNotFound", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "keep.txt")); err != nil {
		t.Fatalf("an unlisted folder was touched: %v", err)
	}
}

func TestSaveWorktreePendingKeepsACopyWithoutTouchingTheWorktree(t *testing.T) {
	f := newWorktreeFixture(t)
	ctx := context.Background()
	statusBefore := gitSnapshot(t, f.dirty, "status", "--porcelain")
	saved, err := SaveWorktreePending(ctx, f.main, f.dirty)
	if err != nil || !saved.HadChange || !strings.HasPrefix(saved.Ref, "refs/harflex/worktree-saves/task-dirty-") || saved.Commit == "" {
		t.Fatalf("save = %+v, %v", saved, err)
	}
	if got := gitSnapshot(t, f.main, "show", saved.Ref+":README.md"); got != "edited" {
		t.Fatalf("the kept copy lost the edit: %q", got)
	}
	if got := gitSnapshot(t, f.main, "show", saved.Ref+":new.txt"); got != "untracked" {
		t.Fatalf("the kept copy lost the untracked file: %q", got)
	}
	if got := gitSnapshot(t, f.dirty, "status", "--porcelain"); got != statusBefore {
		t.Fatalf("saving changed the worktree: before %q, after %q", statusBefore, got)
	}
	if got := gitSnapshot(t, f.dirty, "diff", "--cached", "--name-only"); got != "" {
		t.Fatalf("saving staged files: %q", got)
	}
	if got := gitSnapshot(t, f.dirty, "rev-parse", "HEAD"); got == saved.Commit {
		t.Fatal("saving moved the branch")
	}
}

func TestSaveWorktreePendingRefusesWhatTheAssistantCannotHandle(t *testing.T) {
	f := newWorktreeFixture(t)
	ctx := context.Background()
	for name, path := range map[string]string{"main worktree": f.main, "locked": f.locked, "stopped merge": f.midway} {
		_, err := SaveWorktreePending(ctx, f.main, path)
		var blocked *SaveBlockedError
		if !errors.As(err, &blocked) {
			t.Fatalf("%s: error = %v, want a refusal", name, err)
		}
	}
	// With the base checkout dirty nothing can be merged into it.
	gitSnapshot(t, f.midway, "merge", "--abort")
	writeFile(t, filepath.Join(f.main, "scratch.txt"), "unsaved on the base\n")
	set, err := ListWorktrees(ctx, f.main)
	if err != nil {
		t.Fatal(err)
	}
	blockers := findWorktree(t, set, f.dirty).SaveBlockers(set)
	if len(blockers) != 1 || blockers[0].Code != BlockerBaseDirty {
		t.Fatalf("a dirty base must block saving, got %+v", blockers)
	}
	if _, err := SaveWorktreePending(ctx, f.main, f.dirty); !errors.Is(err, ErrWorktreeSaveBlocked) {
		t.Fatalf("error = %v, want ErrWorktreeSaveBlocked", err)
	}
	// The dirty base itself can be saved: that is what unblocks the others.
	if blockers := findWorktree(t, set, f.main).SaveBlockers(set); len(blockers) != 0 {
		t.Fatalf("a dirty main worktree must be savable, got %+v", blockers)
	}
	saved, err := SaveWorktreePending(ctx, f.main, f.main)
	if err != nil || !saved.HadChange || !strings.HasPrefix(saved.Ref, "refs/harflex/worktree-saves/main-") {
		t.Fatalf("saving the dirty main worktree = %+v, %v", saved, err)
	}
	if body, _ := os.ReadFile(filepath.Join(f.main, "scratch.txt")); string(body) != "unsaved on the base\n" {
		t.Fatal("the safety copy touched the main worktree's files")
	}
}

func TestSaveBlockersReportNothingToSaveForACleanMergedWorktree(t *testing.T) {
	f := newWorktreeFixture(t)
	gitSnapshot(t, f.midway, "merge", "--abort")
	set, err := ListWorktrees(context.Background(), f.main)
	if err != nil {
		t.Fatal(err)
	}
	blockers := findWorktree(t, set, f.merged).SaveBlockers(set)
	if len(blockers) != 1 || blockers[0].Code != BlockerNothingToSave {
		t.Fatalf("a clean merged worktree has nothing to save, got %+v", blockers)
	}
	saved, err := SaveWorktreePending(context.Background(), f.main, f.merged)
	if err != nil || saved.HadChange || saved.Ref != "" {
		t.Fatalf("a clean worktree needs no safety copy: %+v, %v", saved, err)
	}
}

func TestPruneWorktreesForgetsOnlyMissingDirectories(t *testing.T) {
	f := newWorktreeFixture(t)
	if err := os.RemoveAll(f.merged); err != nil {
		t.Fatal(err)
	}
	set, err := ListWorktrees(context.Background(), f.main)
	if err != nil {
		t.Fatal(err)
	}
	if item := findWorktree(t, set, f.merged); !item.Missing || !reflect.DeepEqual(SortedBlockerCodes(item.DeleteBlockers(set, nil)), []string{BlockerMissingDirectory}) {
		t.Fatalf("a missing directory must be flagged: %+v", item)
	}
	if err := PruneWorktrees(context.Background(), f.main); err != nil {
		t.Fatal(err)
	}
	after, err := ListWorktrees(context.Background(), f.main)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := after.Find(f.merged); found {
		t.Fatal("the stale entry survived the prune")
	}
	if _, found := after.Find(f.dirty); !found {
		t.Fatal("the prune removed a live worktree")
	}
}
