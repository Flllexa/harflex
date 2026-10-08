package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/repositories"
)

func worktreeGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid"}, args...)...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

type applicationWorktrees struct{ main, merged, dirty, ahead string }

func newApplicationWorktrees(t *testing.T) applicationWorktrees {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := applicationWorktrees{main: filepath.Join(base, "main"), merged: filepath.Join(base, "merged"), dirty: filepath.Join(base, "dirty"), ahead: filepath.Join(base, "ahead")}
	if err := os.MkdirAll(w.main, 0o755); err != nil {
		t.Fatal(err)
	}
	worktreeGit(t, w.main, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(w.main, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.main, ".gitignore"), []byte("dist/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	worktreeGit(t, w.main, "add", "-A")
	worktreeGit(t, w.main, "commit", "-m", "base")
	worktreeGit(t, w.main, "worktree", "add", "-b", "task/merged", w.merged)
	if err := os.MkdirAll(filepath.Join(w.merged, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.merged, "dist", "bundle.js"), []byte("built\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	worktreeGit(t, w.main, "worktree", "add", "-b", "task/dirty", w.dirty)
	if err := os.WriteFile(filepath.Join(w.dirty, "README.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	worktreeGit(t, w.main, "worktree", "add", "-b", "task/ahead", w.ahead)
	if err := os.WriteFile(filepath.Join(w.ahead, "ahead.txt"), []byte("only here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	worktreeGit(t, w.ahead, "add", "-A")
	worktreeGit(t, w.ahead, "commit", "-m", "ahead")
	return w
}

func itemFor(t *testing.T, list WorktreeListDTO, path string) WorktreeDTO {
	t.Helper()
	for _, item := range list.Items {
		if item.Path == path {
			return item
		}
	}
	t.Fatalf("worktree %s is not in %+v", path, list.Items)
	return WorktreeDTO{}
}

func blockerCodes(blockers []WorktreeBlockerDTO) []string {
	codes := []string{}
	for _, blocker := range blockers {
		codes = append(codes, blocker.Code)
	}
	return codes
}

func TestListWorktreesSaysWhichOnesCanBeDeletedAndMarksTheOpenProject(t *testing.T) {
	s, _, _ := setup(t)
	w := newApplicationWorktrees(t)
	workspace, err := s.OpenWorkspace(w.ahead)
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.ListWorktrees(workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !list.IsRepository || list.Root != w.main || list.Base != "main" || list.CurrentPath != w.ahead || len(list.Items) != 4 || list.Items[0].Path != w.main {
		t.Fatalf("list: %+v", list)
	}
	merged := itemFor(t, list, w.merged)
	if !merged.CanDelete || len(merged.Blockers) != 0 || merged.CanSaveWithAI || len(merged.Ignored) != 1 || merged.Ignored[0] != "dist/" {
		t.Fatalf("a clean merged worktree must be deletable and list its ignored files: %+v", merged)
	}
	dirty := itemFor(t, list, w.dirty)
	if dirty.CanDelete || !dirty.CanSaveWithAI || dirty.Changed != 1 || dirty.Staged != 0 || dirty.Unstaged != 1 || strings.Join(blockerCodes(dirty.Blockers), ",") != "uncommitted_changes" {
		t.Fatalf("a worktree with edits must not be deletable but can be saved: %+v", dirty)
	}
	current := itemFor(t, list, w.ahead)
	if !current.IsCurrent || current.CanDelete || !current.CanSaveWithAI || strings.Join(blockerCodes(current.Blockers), ",") != "current_project,unmerged_commits" || current.Ahead != 1 {
		t.Fatalf("the open project must be protected: %+v", current)
	}
	if main := itemFor(t, list, w.main); main.CanDelete || main.CanSaveWithAI || !main.IsMain {
		t.Fatalf("the main worktree is never deletable: %+v", main)
	}
}

func TestListWorktreesTreatsAPlainFolderAsNotARepository(t *testing.T) {
	s, _, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.ListWorktrees(workspace.ID)
	if err != nil || list.IsRepository || len(list.Items) != 0 {
		t.Fatalf("a folder outside Git is not an error: %+v %v", list, err)
	}
	if _, err := s.ListWorktrees(""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty workspace id: %v", err)
	}
	if _, err := s.ListWorktrees("workspace-unknown"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("unknown workspace: %v", err)
	}
}

func TestDeleteWorktreeOnlyRemovesWhatIsSavedAndListed(t *testing.T) {
	s, _, _ := setup(t)
	w := newApplicationWorktrees(t)
	workspace, err := s.OpenWorkspace(w.main)
	if err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"relative": "merged", "empty": "", "control character": w.merged + "\n"} {
		if _, err := s.DeleteWorktree(DeleteWorktreeInput{WorkspaceID: workspace.ID, Path: path, AcknowledgeIgnored: true}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s path: error = %v, want invalid input", name, err)
		}
	}
	if _, err := s.DeleteWorktree(DeleteWorktreeInput{WorkspaceID: "workspace-unknown", Path: w.merged}); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("unknown workspace: %v", err)
	}
	outside := t.TempDir()
	if _, err := s.DeleteWorktree(DeleteWorktreeInput{WorkspaceID: workspace.ID, Path: outside, AcknowledgeIgnored: true}); ErrorCode(err) != "worktree_not_found" {
		t.Fatalf("an unlisted folder must be refused, got %v", err)
	}
	if _, err := s.DeleteWorktree(DeleteWorktreeInput{WorkspaceID: workspace.ID, Path: w.dirty, DeleteBranch: true, AcknowledgeIgnored: true}); ErrorCode(err) != "worktree_not_deletable" {
		t.Fatalf("a worktree with edits must be refused, got %v", err)
	}
	if body, _ := os.ReadFile(filepath.Join(w.dirty, "README.md")); string(body) != "edited\n" {
		t.Fatalf("the refused worktree lost its edit: %q", body)
	}
	if _, err := s.DeleteWorktree(DeleteWorktreeInput{WorkspaceID: workspace.ID, Path: w.merged}); ErrorCode(err) != "worktree_not_deletable" {
		t.Fatalf("ignored files must be acknowledged, got %v", err)
	}
	result, err := s.DeleteWorktree(DeleteWorktreeInput{WorkspaceID: workspace.ID, Path: w.merged, DeleteBranch: true, AcknowledgeIgnored: true})
	if err != nil || !result.Removed || !result.BranchDeleted || result.Branch != "task/merged" {
		t.Fatalf("delete = %+v, %v", result, err)
	}
	if _, err := os.Stat(w.merged); !os.IsNotExist(err) {
		t.Fatalf("the directory survived: %v", err)
	}
	for _, item := range result.List.Items {
		if item.Path == w.merged {
			t.Fatal("the refreshed list still shows the deleted worktree")
		}
	}
	if len(result.List.Items) != 3 {
		t.Fatalf("refreshed list: %+v", result.List.Items)
	}
}

func TestDeleteWorktreeNeverRemovesTheOpenProject(t *testing.T) {
	s, _, _ := setup(t)
	w := newApplicationWorktrees(t)
	workspace, err := s.OpenWorkspace(w.merged)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteWorktree(DeleteWorktreeInput{WorkspaceID: workspace.ID, Path: w.merged, AcknowledgeIgnored: true}); ErrorCode(err) != "worktree_not_deletable" {
		t.Fatalf("the open project must be protected, got %v", err)
	}
	if _, err := os.Stat(w.merged); err != nil {
		t.Fatalf("the open project disappeared: %v", err)
	}
}

func TestPrepareWorktreeSaveKeepsACopyAndWritesPreciseInstructions(t *testing.T) {
	s, _, _ := setup(t)
	w := newApplicationWorktrees(t)
	workspace, err := s.OpenWorkspace(w.main)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PrepareWorktreeSave(PrepareWorktreeSaveInput{WorkspaceID: workspace.ID, Path: w.dirty})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Workspace.Path != w.main || plan.Path != w.dirty || plan.Branch != "task/dirty" || plan.Base != "main" || !plan.HadPending ||
		!strings.HasPrefix(plan.SnapshotRef, "refs/harflex/worktree-saves/task-dirty-") || plan.SnapshotCommit == "" || plan.Reason != "Salvar e mesclar o worktree task/dirty" {
		t.Fatalf("plan: %+v", plan)
	}
	if got := worktreeGit(t, w.main, "show", plan.SnapshotRef+":README.md"); got != "edited" {
		t.Fatalf("the kept copy lost the edit: %q", got)
	}
	for _, want := range []string{w.main, w.dirty, `"task/dirty"`, plan.SnapshotRef, "merge --no-ff", "Não use --force", "Não apague o worktree nem a branch"} {
		if !strings.Contains(plan.Prompt, want) {
			t.Fatalf("the instructions must mention %q:\n%s", want, plan.Prompt)
		}
	}
	if got := worktreeGit(t, w.dirty, "status", "--porcelain"); got != "M README.md" {
		t.Fatalf("preparing the save changed the worktree: %q", got)
	}
}

func TestPrepareWorktreeSaveRefusesWhatNoAssistantShouldTouch(t *testing.T) {
	s, _, _ := setup(t)
	w := newApplicationWorktrees(t)
	workspace, err := s.OpenWorkspace(w.main)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareWorktreeSave(PrepareWorktreeSaveInput{WorkspaceID: workspace.ID, Path: w.merged}); ErrorCode(err) != "worktree_save_blocked" {
		t.Fatalf("a clean merged worktree has nothing to save, got %v", err)
	}
	if _, err := s.PrepareWorktreeSave(PrepareWorktreeSaveInput{WorkspaceID: workspace.ID, Path: w.main}); ErrorCode(err) != "worktree_save_blocked" {
		t.Fatalf("the main worktree cannot be saved, got %v", err)
	}
	if _, err := s.PrepareWorktreeSave(PrepareWorktreeSaveInput{WorkspaceID: workspace.ID, Path: t.TempDir()}); ErrorCode(err) != "worktree_not_found" {
		t.Fatalf("an unlisted folder must be refused, got %v", err)
	}
	if err := os.WriteFile(filepath.Join(w.main, "scratch.txt"), []byte("unsaved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = s.PrepareWorktreeSave(PrepareWorktreeSaveInput{WorkspaceID: workspace.ID, Path: w.dirty})
	var blocked *repositories.SaveBlockedError
	if !errors.As(err, &blocked) || len(blocked.Blockers) != 1 || blocked.Blockers[0].Code != repositories.BlockerBaseDirty {
		t.Fatalf("a dirty base checkout must stop the save, got %v", err)
	}
	if out := worktreeGit(t, w.main, "for-each-ref", "refs/harflex"); out != "" {
		t.Fatalf("a refused save must not leave safety refs behind: %q", out)
	}
	// The dirty main worktree is what can be saved now, by committing on the base branch only.
	plan, err := s.PrepareWorktreeSave(PrepareWorktreeSaveInput{WorkspaceID: workspace.ID, Path: w.main})
	if err != nil || plan.SnapshotRef == "" || !plan.HadPending || !strings.Contains(plan.Reason, "Commitar") ||
		!strings.Contains(plan.Prompt, "Commite as alterações pendentes do worktree principal") || strings.Contains(plan.Prompt, "merge --no-ff") {
		t.Fatalf("saving the dirty main worktree = %+v, %v", plan, err)
	}
}

func TestPruneWorktreesReturnsTheRefreshedList(t *testing.T) {
	s, _, _ := setup(t)
	w := newApplicationWorktrees(t)
	workspace, err := s.OpenWorkspace(w.main)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(w.merged); err != nil {
		t.Fatal(err)
	}
	before, err := s.ListWorktrees(workspace.ID)
	if err != nil || !itemFor(t, before, w.merged).Missing {
		t.Fatalf("a missing folder must be flagged: %v %+v", err, before)
	}
	after, err := s.PruneWorktrees(workspace.ID)
	if err != nil || len(after.Items) != 3 {
		t.Fatalf("prune = %+v, %v", after, err)
	}
}

// The assistant is a stand-in here: plain Git does what the prompt asks, and the app must reach its own
// verdict from the repository, never from the assistant's word.
func TestSavedByTheAssistantThenDeletedAfterTheAppJudgesAgain(t *testing.T) {
	s, _, _ := setup(t)
	w := newApplicationWorktrees(t)
	inside, err := s.OpenWorkspace(w.dirty)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.ListWorktrees(inside.ID)
	if err != nil {
		t.Fatal(err)
	}
	held := itemFor(t, before, w.dirty)
	if held.CanDelete || !held.IsCurrent || !held.CanSaveWithAI || !strings.Contains(strings.Join(blockerCodes(held.Blockers), ","), "uncommitted_changes") {
		t.Fatalf("the task worktree must be held and savable, got %+v", held)
	}

	plan, err := s.PrepareWorktreeSave(PrepareWorktreeSaveInput{WorkspaceID: inside.ID, Path: w.dirty})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Workspace.Path != w.main || plan.Workspace.ID == inside.ID {
		t.Fatalf("the assistant works in the main checkout, got %+v", plan.Workspace)
	}
	// The app now has the main checkout open; the assistant has not done anything yet, so nothing may be deleted.
	if _, err := s.DeleteWorktree(DeleteWorktreeInput{WorkspaceID: plan.Workspace.ID, Path: w.dirty, DeleteBranch: true, AcknowledgeIgnored: true}); ErrorCode(err) != "worktree_not_deletable" {
		t.Fatalf("pending work must block the delete, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(w.dirty, "README.md")); err != nil {
		t.Fatalf("the refused delete touched the worktree: %v", err)
	}

	// What the prompt asks of the assistant: commit in the task worktree, merge from the main checkout.
	worktreeGit(t, w.dirty, "add", "-A")
	worktreeGit(t, w.dirty, "commit", "-m", "feat: edit the readme")
	worktreeGit(t, w.main, "merge", "--no-ff", "task/dirty", "-m", "merge task/dirty")

	after, err := s.ListWorktrees(plan.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settled := itemFor(t, after, w.dirty); !settled.CanDelete || settled.Changed != 0 || settled.Ahead != 0 || !settled.Merged || len(settled.Blockers) != 0 {
		t.Fatalf("saved and merged work must clear the verdict, got %+v", settled)
	}
	result, err := s.DeleteWorktree(DeleteWorktreeInput{WorkspaceID: plan.Workspace.ID, Path: w.dirty, DeleteBranch: true})
	if err != nil || !result.Removed || !result.BranchDeleted {
		t.Fatalf("delete = %+v, %v", result, err)
	}
	if _, err := os.Stat(w.dirty); !os.IsNotExist(err) {
		t.Fatalf("the directory survived: %v", err)
	}
	if got := worktreeGit(t, w.main, "show", "main:README.md"); got != "edited" {
		t.Fatalf("the edit did not reach the base branch: %q", got)
	}
	if got := worktreeGit(t, w.main, "show", plan.SnapshotRef+":README.md"); got != "edited" {
		t.Fatalf("the safety copy must outlive the worktree: %q", got)
	}
	if got := worktreeGit(t, w.main, "branch", "--list", "task/dirty"); got != "" {
		t.Fatalf("the merged branch should be gone: %q", got)
	}
}

func TestADeleteIsRefusedWhenTheAssistantLeftSomethingBehind(t *testing.T) {
	s, _, _ := setup(t)
	w := newApplicationWorktrees(t)
	workspace, err := s.OpenWorkspace(w.main)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.PrepareWorktreeSave(PrepareWorktreeSaveInput{WorkspaceID: workspace.ID, Path: w.dirty})
	if err != nil {
		t.Fatal(err)
	}
	// The assistant commits but never merges.
	worktreeGit(t, w.dirty, "add", "-A")
	worktreeGit(t, w.dirty, "commit", "-m", "feat: edit the readme")
	list, err := s.ListWorktrees(workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if item := itemFor(t, list, w.dirty); item.CanDelete || !strings.Contains(strings.Join(blockerCodes(item.Blockers), ","), "unmerged_commits") {
		t.Fatalf("committed but unmerged work must still hold the worktree, got %+v", item)
	}
	if _, err := s.DeleteWorktree(DeleteWorktreeInput{WorkspaceID: workspace.ID, Path: w.dirty, DeleteBranch: true, AcknowledgeIgnored: true}); ErrorCode(err) != "worktree_not_deletable" {
		t.Fatalf("an unmerged branch must be refused, got %v", err)
	}
	// Merged afterwards, but a new edit appears before the app deletes: judged again, refused again.
	worktreeGit(t, w.main, "merge", "--no-ff", "task/dirty", "-m", "merge task/dirty")
	if err := os.WriteFile(filepath.Join(w.dirty, "late.txt"), []byte("written after the merge\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteWorktree(DeleteWorktreeInput{WorkspaceID: workspace.ID, Path: w.dirty, DeleteBranch: true, AcknowledgeIgnored: true}); ErrorCode(err) != "worktree_not_deletable" {
		t.Fatalf("a late edit must be refused, got %v", err)
	}
	if body, err := os.ReadFile(filepath.Join(w.dirty, "late.txt")); err != nil || string(body) != "written after the merge\n" {
		t.Fatalf("the late edit was lost: %q %v", body, err)
	}
	if out := worktreeGit(t, w.main, "for-each-ref", plan.SnapshotRef); out == "" {
		t.Fatal("the safety copy must still exist")
	}
}

// The renderer validates every answer against a strict schema, where a JSON null is not an empty list.
func assertNoJSONNull(t *testing.T, label string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	var walk func(path string, node any)
	walk = func(path string, node any) {
		switch typed := node.(type) {
		case nil:
			t.Errorf("%s: %s is null in %s", label, path, raw)
		case map[string]any:
			for key, child := range typed {
				walk(path+"."+key, child)
			}
		case []any:
			for index, child := range typed {
				walk(fmt.Sprintf("%s[%d]", path, index), child)
			}
		}
	}
	walk("$", decoded)
}

func TestWorktreeAnswersNeverCarryJSONNull(t *testing.T) {
	s, _, _ := setup(t)
	w := newApplicationWorktrees(t)
	workspace, err := s.OpenWorkspace(w.main)
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.ListWorktrees(workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := itemFor(t, list, w.merged); len(got.Ignored) == 0 {
		t.Fatalf("the fixture must exercise a worktree with ignored files: %+v", got)
	}
	assertNoJSONNull(t, "list", list)

	plan, err := s.PrepareWorktreeSave(PrepareWorktreeSaveInput{WorkspaceID: workspace.ID, Path: w.dirty})
	if err != nil {
		t.Fatal(err)
	}
	assertNoJSONNull(t, "save plan", plan)

	if err := os.RemoveAll(w.ahead); err != nil {
		t.Fatal(err)
	}
	withMissing, err := s.ListWorktrees(workspace.ID)
	if err != nil || !itemFor(t, withMissing, w.ahead).Missing {
		t.Fatalf("a missing folder must be flagged: %v", err)
	}
	assertNoJSONNull(t, "list with a missing folder", withMissing)

	deleted, err := s.DeleteWorktree(DeleteWorktreeInput{WorkspaceID: workspace.ID, Path: w.merged, AcknowledgeIgnored: true})
	if err != nil {
		t.Fatal(err)
	}
	assertNoJSONNull(t, "delete result", deleted)

	plain, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	notRepository, err := s.ListWorktrees(plain.ID)
	if err != nil || notRepository.IsRepository {
		t.Fatalf("a plain folder is not a repository: %v %+v", err, notRepository)
	}
	assertNoJSONNull(t, "list of a plain folder", notRepository)
}

// A folder that holds nothing Git would lose can still be somebody's working directory.
func TestAFolderInUseIsNotOfferedForDeletion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("listing running processes is not supported on Windows")
	}
	if runtime.GOOS != "linux" {
		if _, err := exec.LookPath("lsof"); err != nil {
			t.Skip("lsof is unavailable")
		}
	}
	s, _, _ := setup(t)
	w := newApplicationWorktrees(t)
	workspace, err := s.OpenWorkspace(w.main)
	if err != nil {
		t.Fatal(err)
	}
	sleeper := exec.Command("sleep", "120")
	sleeper.Dir = w.merged
	if err := sleeper.Start(); err != nil {
		t.Skip("sleep is unavailable")
	}
	defer func() { _ = sleeper.Process.Kill(); _, _ = sleeper.Process.Wait() }()

	var held WorktreeDTO
	for deadline := time.Now().Add(5 * time.Second); ; {
		list, err := s.ListWorktrees(workspace.ID)
		if err != nil {
			t.Fatal(err)
		}
		held = itemFor(t, list, w.merged)
		if !held.CanDelete || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if held.CanDelete || len(held.Blockers) != 1 || held.Blockers[0].Code != "in_use" || held.Blockers[0].Count != 1 || !strings.Contains(held.Blockers[0].Detail, "sleep (pid ") {
		t.Fatalf("a folder with a process inside must not be deletable, got %+v", held)
	}
	if _, err := s.DeleteWorktree(DeleteWorktreeInput{WorkspaceID: workspace.ID, Path: w.merged, AcknowledgeIgnored: true}); ErrorCode(err) != "worktree_not_deletable" {
		t.Fatalf("delete must be refused while the folder is in use, got %v", err)
	}
	if _, err := os.Stat(w.merged); err != nil {
		t.Fatalf("the refused delete touched the folder: %v", err)
	}
	_ = sleeper.Process.Kill()
	_, _ = sleeper.Process.Wait()
	if _, err := s.DeleteWorktree(DeleteWorktreeInput{WorkspaceID: workspace.ID, Path: w.merged, AcknowledgeIgnored: true}); err != nil {
		t.Fatalf("once nothing uses the folder it can go: %v", err)
	}
}

func TestListAllWorktreesGroupsActiveProjectsByRepository(t *testing.T) {
	s, _, _ := setup(t)
	w := newApplicationWorktrees(t)
	main, err := s.OpenWorkspace(w.main)
	if err != nil {
		t.Fatal(err)
	}
	// A worktree opened as a project of its own belongs to the same repository.
	if _, err := s.OpenWorkspace(w.dirty); err != nil {
		t.Fatal(err)
	}
	other, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	worktreeGit(t, other, "init", "-b", "main")
	worktreeGit(t, other, "commit", "--allow-empty", "-m", "base")
	if _, err := s.OpenWorkspace(other); err != nil {
		t.Fatal(err)
	}
	archivedRepo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	worktreeGit(t, archivedRepo, "init", "-b", "main")
	archived, err := s.OpenWorkspace(archivedRepo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetWorkspaceArchived(SetWorkspaceArchivedInput{WorkspaceID: archived.ID, Archived: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenWorkspace(t.TempDir()); err != nil { // not a repository
		t.Fatal(err)
	}
	groups, err := s.ListAllWorktrees()
	if err != nil || len(groups) != 2 {
		t.Fatalf("groups = %+v, %v", groups, err)
	}
	var shared *WorktreeGroupDTO
	for i := range groups {
		if groups[i].List.Root == w.main {
			shared = &groups[i]
		}
	}
	if shared == nil || shared.WorkspaceID != main.ID || shared.Name != "main" || len(shared.Projects) != 2 || len(shared.List.Items) != 4 || shared.Error != "" {
		t.Fatalf("shared repository group = %+v", shared)
	}
	for _, group := range groups {
		if group.List.Root == archivedRepo {
			t.Fatal("an archived project must stay out of the overview")
		}
	}
}
