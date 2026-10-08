package repositories

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseLsofCwdReadsOneRecordPerProcess(t *testing.T) {
	output := "p101\ncnode\nR1\nfcwd\nn/work/app\np102\ncGoogle Chrome Helper\nR101\nfcwd\nn/work/other dir\np103\ncorphan\nfcwd\nR1\nn/\n"
	got := parseLsofCwd(output)
	want := []processEntry{
		{pid: 101, ppid: 1, name: "node", cwd: "/work/app"},
		{pid: 102, ppid: 101, name: "Google Chrome Helper", cwd: "/work/other dir"},
		{pid: 103, ppid: 1, name: "orphan", cwd: "/"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lsof records = %+v, want %+v", got, want)
	}
}

func TestParsePsKeepsTheWholeCommandLine(t *testing.T) {
	output := "    1     0 /sbin/launchd\n  777   101 node /work/app/node_modules/.bin/vite --port 9245\n 4242   777 /bin/zsh -c echo hi\nnot a process line\n"
	got := parsePs(output)
	if len(got) != 3 {
		t.Fatalf("ps records = %+v", got)
	}
	if got[1].pid != 777 || got[1].ppid != 101 || got[1].name != "node" || got[1].text != "node /work/app/node_modules/.bin/vite --port 9245" {
		t.Fatalf("node record = %+v", got[1])
	}
	if got[2].name != "zsh" {
		t.Fatalf("shell name = %q", got[2].name)
	}
}

func TestMergeProcessesJoinsWhatLsofAndPsEachKnow(t *testing.T) {
	merged := mergeProcesses(
		[]processEntry{{pid: 7, ppid: 1, name: "node", cwd: "/work/app"}},
		[]processEntry{{pid: 7, ppid: 1, name: "node", text: "node server.js"}, {pid: 8, ppid: 7, name: "sh", text: "sh -c sleep"}},
	)
	want := []processEntry{{pid: 7, ppid: 1, name: "node", cwd: "/work/app", text: "node server.js"}, {pid: 8, ppid: 7, name: "sh", text: "sh -c sleep"}}
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("merged = %+v, want %+v", merged, want)
	}
}

func TestMatchProcessesAssignsEachProcessToTheDeepestFolder(t *testing.T) {
	folders := []string{"/work/repo/.claude/worktrees/alpha", "/work/repo/.claude/worktrees/alpha/vendor/inner", "/work/repo/.claude/worktrees/beta"}
	entries := []processEntry{
		{pid: 11, name: "zsh", cwd: "/work/repo/.claude/worktrees/alpha"},
		{pid: 12, name: "node", cwd: "/work/repo/.claude/worktrees/alpha/frontend"},
		{pid: 13, name: "claude", cwd: "/work/repo/.claude/worktrees/alpha/vendor/inner/src"},
		{pid: 14, name: "vite", cwd: "/", text: "node /work/repo/.claude/worktrees/beta/node_modules/.bin/vite --port 9245"},
		{pid: 15, name: "tail", cwd: "/", text: "tail -f /work/repo/.claude/worktrees/beta"},
		{pid: 16, name: "zsh", cwd: "/work/repo"},                           // the main checkout is not one of the folders
		{pid: 17, name: "zsh", cwd: "/work/repo/.claude/worktrees/alpha-2"}, // shares a prefix with alpha, but is another folder
		{pid: 18, name: "grep", cwd: "/", text: "grep -r todo /work/repo/.claude/worktrees/beta-extra/src"},
	}
	got := matchProcesses(entries, folders, 1, "harflex")
	want := map[string][]ProcessUse{
		"/work/repo/.claude/worktrees/alpha":              {{PID: 11, Command: "zsh"}, {PID: 12, Command: "node"}},
		"/work/repo/.claude/worktrees/alpha/vendor/inner": {{PID: 13, Command: "claude"}},
		"/work/repo/.claude/worktrees/beta":               {{PID: 14, Command: "vite"}, {PID: 15, Command: "tail"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matches = %+v, want %+v", got, want)
	}
}

func TestMatchProcessesIgnoresOurOwnInspectionHelpers(t *testing.T) {
	folders := []string{"/work/wt"}
	entries := []processEntry{
		{pid: 21, ppid: 500, name: "git", cwd: "/work/wt"},    // ours: inspecting the folder
		{pid: 22, ppid: 500, name: "lsof", cwd: "/work/wt"},   // ours
		{pid: 23, ppid: 500, name: "node", cwd: "/work/wt"},   // a real child of ours that lives there
		{pid: 24, ppid: 999, name: "git", cwd: "/work/wt"},    // somebody else's git: a rebase or a hook, so it counts
		{pid: 500, ppid: 1, name: "harflex", cwd: "/work/wt"}, // the app itself running from the folder
	}
	got := matchProcesses(entries, folders, 500, "harflex")["/work/wt"]
	want := []ProcessUse{{PID: 23, Command: "node"}, {PID: 24, Command: "git"}, {PID: 500, Command: "harflex"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("users = %+v, want %+v", got, want)
	}
}

// Between fork and exec a child of ours still carries our own name, and Go has already moved it into the
// directory it is about to run Git in. Seen by lsof in that instant, it must not count as a user.
func TestMatchProcessesIgnoresChildrenCaughtBetweenForkAndExec(t *testing.T) {
	folders := []string{"/work/wt"}
	entries := []processEntry{
		{pid: 500, ppid: 1, name: "harflex", cwd: "/"},
		{pid: 41, ppid: 500, name: "harflex", cwd: "/work/wt"},   // forked by us, not yet exec'd into git
		{pid: 42, ppid: 41, name: "git-helper", cwd: "/work/wt"}, // what that git starts
		{pid: 43, ppid: 500, name: "node", cwd: "/work/wt"},      // a real child with a program of its own
		{pid: 44, ppid: 900, name: "harflex", cwd: "/work/wt"},   // another instance of the app is genuinely there
	}
	got := matchProcesses(entries, folders, 500, "harflex")["/work/wt"]
	want := []ProcessUse{{PID: 43, Command: "node"}, {PID: 44, Command: "harflex"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("users = %+v, want %+v", got, want)
	}
}

func TestMatchProcessesFollowsSymlinkedFolders(t *testing.T) {
	real := t.TempDir()
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(resolved, link); err != nil {
		t.Skip("symlinks are unavailable")
	}
	// The registry names the folder through the link; the OS reports the real path.
	got := matchProcesses([]processEntry{{pid: 31, name: "zsh", cwd: filepath.Join(resolved, "src")}}, []string{link}, 1, "harflex")
	if len(got[link]) != 1 || got[link][0].PID != 31 {
		t.Fatalf("a process in the real path must count for the linked folder: %+v", got)
	}
}

func TestDescribeProcessesNamesTheFirstFew(t *testing.T) {
	users := []ProcessUse{{PID: 1, Command: "zsh"}, {PID: 2, Command: "node"}, {PID: 3, Command: "claude"}, {PID: 4, Command: "vite"}}
	if got := describeProcesses(users); got != "zsh (pid 1), node (pid 2), claude (pid 3)" {
		t.Fatalf("description = %q", got)
	}
	if got := cleanProcessName("  weird\x00name\n"); got != "weirdname" {
		t.Fatalf("clean name = %q", got)
	}
	if got := cleanProcessName(strings.Repeat("é", 60)); len([]rune(got)) != 41 {
		t.Fatalf("a long name must be cut, got %d runes", len([]rune(got)))
	}
}

func requireProcessListing(t *testing.T) {
	t.Helper()
	switch runtime.GOOS {
	case "windows", "plan9", "js", "wasip1":
		t.Skip("listing running processes is not supported here")
	case "linux":
	default:
		if _, err := exec.LookPath("lsof"); err != nil {
			t.Skip("lsof is unavailable")
		}
	}
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep is unavailable")
	}
}

// startInside runs a long process whose working directory is the folder, and stops it with the test.
func startInside(t *testing.T, folder string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "120")
	cmd.Dir = folder
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	return cmd
}

func TestAProcessInsideAWorktreeKeepsItFromBeingDeleted(t *testing.T) {
	requireProcessListing(t)
	f := newWorktreeFixture(t)
	cmd := startInside(t, f.merged)

	var held Worktree
	// The listing is a snapshot of the process table; give the child a moment to take its place in it.
	deadline := time.Now().Add(5 * time.Second)
	for {
		set, err := ListWorktrees(context.Background(), f.main)
		if err != nil {
			t.Fatal(err)
		}
		held = findWorktree(t, set, f.merged)
		if len(held.InUse) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(held.InUse) != 1 || held.InUse[0].PID != cmd.Process.Pid || held.InUse[0].Command != "sleep" {
		t.Fatalf("the sleeping child must be reported as using the folder: %+v", held.InUse)
	}
	set, _ := ListWorktrees(context.Background(), f.main)
	blockers := findWorktree(t, set, f.merged).DeleteBlockers(set, nil)
	if got := SortedBlockerCodes(blockers); !reflect.DeepEqual(got, []string{BlockerInUse}) || blockers[0].Count != 1 || !strings.Contains(blockers[0].Detail, "sleep (pid ") {
		t.Fatalf("blockers = %+v", blockers)
	}
	// Other worktrees are untouched by it, and the main one is never judged by users.
	if other := findWorktree(t, set, f.dirty); len(other.InUse) != 0 {
		t.Fatalf("the sleeper is not in the dirty worktree: %+v", other.InUse)
	}

	_, err := RemoveWorktree(context.Background(), f.main, f.merged, RemoveOptions{AcknowledgeIgnored: true})
	var refused *NotDeletableError
	if !errors.As(err, &refused) || !reflect.DeepEqual(SortedBlockerCodes(refused.Blockers), []string{BlockerInUse}) {
		t.Fatalf("deleting a folder in use must be refused, got %v", err)
	}
	if _, statErr := os.Stat(f.merged); statErr != nil {
		t.Fatalf("the refused delete touched the folder: %v", statErr)
	}

	// Once the program is gone, the very same worktree can be deleted.
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
	if _, err := RemoveWorktree(context.Background(), f.main, f.merged, RemoveOptions{AcknowledgeIgnored: true}); err != nil {
		t.Fatalf("after the process ended the worktree should go: %v", err)
	}
}
