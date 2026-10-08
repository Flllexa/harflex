package repositories

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotRepository        = errors.New("folder is not inside a Git repository")
	ErrWorktreeNotFound     = errors.New("path is not a worktree of this repository")
	ErrWorktreeNotDeletable = errors.New("worktree still holds work that deleting it would lose")
	ErrWorktreeSaveBlocked  = errors.New("worktree cannot be saved right now")
	ErrWorktreeRemoveFailed = errors.New("git could not remove the worktree")
)

// Blocker codes. The frontend words them; the backend only decides.
const (
	BlockerMainWorktree     = "main_worktree"
	BlockerCurrentProject   = "current_project"
	BlockerLocked           = "locked"
	BlockerMissingDirectory = "missing_directory"
	BlockerStatusUnknown    = "status_unavailable"
	BlockerOperation        = "operation_in_progress"
	BlockerUncommitted      = "uncommitted_changes"
	BlockerUnmerged         = "unmerged_commits"
	BlockerDetachedCommits  = "detached_commits"
	BlockerBaseUnknown      = "base_unknown"
	BlockerBaseDirty        = "base_dirty"
	BlockerNothingToSave    = "nothing_to_save"
	BlockerIgnoredFiles     = "ignored_files"
	BlockerInUse            = "in_use"
)

type Blocker struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
	Count  int    `json:"count,omitempty"`
}

// NotDeletableError carries every reason a worktree could not be removed.
type NotDeletableError struct{ Blockers []Blocker }

func (e *NotDeletableError) Error() string        { return ErrWorktreeNotDeletable.Error() }
func (e *NotDeletableError) Is(target error) bool { return target == ErrWorktreeNotDeletable }

// SaveBlockedError carries every reason a worktree could not be handed to the assistant.
type SaveBlockedError struct{ Blockers []Blocker }

func (e *SaveBlockedError) Error() string        { return ErrWorktreeSaveBlocked.Error() }
func (e *SaveBlockedError) Is(target error) bool { return target == ErrWorktreeSaveBlocked }

type WorktreeChanges struct {
	Staged    int      `json:"staged"`
	Unstaged  int      `json:"unstaged"`
	Untracked int      `json:"untracked"`
	Conflicts int      `json:"conflicts"`
	Total     int      `json:"total"`
	Files     []string `json:"files"`
	Truncated bool     `json:"truncated"`
}

type Worktree struct {
	Path        string          `json:"path"`
	Branch      string          `json:"branch"`
	Head        string          `json:"head"`
	Detached    bool            `json:"detached"`
	IsMain      bool            `json:"isMain"`
	Locked      bool            `json:"locked"`
	LockReason  string          `json:"lockReason"`
	Missing     bool            `json:"missing"`
	StatusKnown bool            `json:"statusKnown"`
	Changes     WorktreeChanges `json:"changes"`
	Operation   string          `json:"operation"`
	Ahead       int             `json:"ahead"`
	Behind      int             `json:"behind"`
	Merged      bool            `json:"merged"`
	Ignored     []string        `json:"ignored"`
	IgnoredMore int             `json:"ignoredMore"`
	// InUse lists running programs inside the folder; deleting it under them would break them.
	InUse []ProcessUse `json:"inUse"`
}

type WorktreeSet struct {
	Root      string     `json:"root"`
	Base      string     `json:"base"`
	BaseKnown bool       `json:"baseKnown"`
	Items     []Worktree `json:"items"`
}

const (
	maxListedChanges = 8
	maxListedIgnored = 12
	inspectParallel  = 4
)

// worktreeRead runs a read-only Git command; nothing here takes the index lock.
func worktreeRead(ctx context.Context, dir string, limit int, args ...string) (string, error) {
	out, _, err := gitOutput(ctx, dir, limit, append([]string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + inertHooksDir()}, args...)...)
	return out, err
}

var (
	hooksOnce sync.Once
	hooksDir  string
)

// inertHooksDir is an empty directory so no user hook runs for an action the person did not
// ask Git to perform on their behalf.
func inertHooksDir() string {
	hooksOnce.Do(func() {
		dir, err := os.MkdirTemp("", "harflex-no-hooks-")
		if err != nil {
			dir = os.TempDir()
		}
		hooksDir = dir
	})
	return hooksDir
}

// runGit executes a Git command that may take locks or change refs. It returns stderr for
// diagnostics and never goes through a shell.
func runGit(ctx context.Context, dir string, timeout time.Duration, extraEnv []string, args ...string) (string, string, error) {
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	full := append([]string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + inertHooksDir()}, args...)
	cmd := exec.CommandContext(callCtx, "git", full...)
	cmd.Dir = dir
	cmd.Env = make([]string, 0, len(os.Environ())+len(extraEnv)+2)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat")
	cmd.Env = append(cmd.Env, extraEnv...)
	stdout, stderr := &boundedBuffer{limit: 1 << 20}, &boundedBuffer{limit: 4096}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if err != nil {
		return stdout.String(), strings.TrimSpace(stderr.String()), fmt.Errorf("git %s: %w", args[0], err)
	}
	return stdout.String(), strings.TrimSpace(stderr.String()), nil
}

type rawWorktree struct {
	path, head, branch, lockReason, prunableReason string
	detached, bare, locked, prunable               bool
}

func parseWorktreeList(raw string, nul bool) []rawWorktree {
	separator := "\n"
	if nul {
		separator = "\x00"
	}
	var records []rawWorktree
	var current *rawWorktree
	flush := func() {
		if current != nil && current.path != "" {
			records = append(records, *current)
		}
		current = nil
	}
	for _, line := range strings.Split(raw, separator) {
		if line == "" {
			flush()
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		if key == "worktree" {
			flush()
			current = &rawWorktree{path: value}
			continue
		}
		if current == nil {
			continue
		}
		switch key {
		case "HEAD":
			current.head = value
		case "branch":
			current.branch = strings.TrimPrefix(value, "refs/heads/")
		case "detached":
			current.detached = true
		case "bare":
			current.bare = true
		case "locked":
			current.locked, current.lockReason = true, value
		case "prunable":
			current.prunable, current.prunableReason = true, value
		}
	}
	flush()
	return records
}

func cleanWorktreePath(path string) string {
	cleaned := filepath.Clean(filepath.FromSlash(path))
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		return resolved
	}
	return cleaned
}

// ListWorktrees reads every working tree of the repository that contains path and judges
// what deleting each one would lose, including which running programs sit inside each folder.
func ListWorktrees(ctx context.Context, path string) (WorktreeSet, error) {
	return listWorktrees(ctx, path, true)
}

// ListWorktreesWithoutUsage is ListWorktrees without the scan of running programs, which costs about a
// second. It is for callers that only need Git's view, such as finding the open project's worktree.
func ListWorktreesWithoutUsage(ctx context.Context, path string) (WorktreeSet, error) {
	return listWorktrees(ctx, path, false)
}

func listWorktrees(ctx context.Context, path string, withUsage bool) (WorktreeSet, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return WorktreeSet{}, ErrGitUnavailable
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return WorktreeSet{}, fmt.Errorf("resolve workspace: %w", err)
	}
	top, err := worktreeRead(ctx, canonical, 4096, "rev-parse", "--show-toplevel")
	if err != nil {
		if ctx.Err() != nil {
			return WorktreeSet{}, ctx.Err()
		}
		return WorktreeSet{}, ErrNotRepository
	}
	top = cleanWorktreePath(strings.TrimSpace(top))
	raw, err := worktreeRead(ctx, top, 1<<20, "worktree", "list", "--porcelain", "-z")
	nul := err == nil
	if err != nil {
		if ctx.Err() != nil {
			return WorktreeSet{}, ctx.Err()
		}
		if raw, err = worktreeRead(ctx, top, 1<<20, "worktree", "list", "--porcelain"); err != nil {
			return WorktreeSet{}, fmt.Errorf("list worktrees: %w", err)
		}
	}
	records := parseWorktreeList(raw, nul)
	if len(records) == 0 {
		return WorktreeSet{}, ErrNotRepository
	}
	set := WorktreeSet{Root: cleanWorktreePath(records[0].path), Items: make([]Worktree, len(records))}
	if branch := records[0].branch; branch != "" && !records[0].bare {
		set.Base, set.BaseKnown = branch, true
	} else {
		for _, candidate := range []string{"main", "master"} {
			if _, err := worktreeRead(ctx, set.Root, 256, "show-ref", "--verify", "--quiet", "refs/heads/"+candidate); err == nil {
				set.Base, set.BaseKnown = candidate, true
				break
			}
		}
	}
	// Who is using each folder is read while Git inspects them: lsof is the slowest step of the two.
	linked := make([]string, 0, len(records))
	for index, record := range records {
		if index > 0 && !record.prunable {
			linked = append(linked, cleanWorktreePath(record.path))
		}
	}
	usage := make(chan map[string][]ProcessUse, 1)
	if withUsage {
		go func() { usage <- ProcessesUsing(ctx, linked) }()
	} else {
		usage <- nil
	}
	slots := make(chan struct{}, inspectParallel)
	var group sync.WaitGroup
	for index, record := range records {
		group.Add(1)
		slots <- struct{}{}
		go func(index int, record rawWorktree) {
			defer group.Done()
			defer func() { <-slots }()
			set.Items[index] = inspectWorktree(ctx, record, index == 0, set)
		}(index, record)
	}
	group.Wait()
	users := <-usage
	if err := ctx.Err(); err != nil {
		return WorktreeSet{}, err
	}
	for index := range set.Items {
		if item := &set.Items[index]; !item.IsMain && !item.Missing {
			item.InUse = users[item.Path]
		}
	}
	return set, nil
}

var operationMarkers = []struct{ marker, name string }{
	{"MERGE_HEAD", "merge"}, {"rebase-merge", "rebase"}, {"rebase-apply", "rebase"},
	{"CHERRY_PICK_HEAD", "cherry-pick"}, {"REVERT_HEAD", "revert"}, {"BISECT_LOG", "bisect"}, {"sequencer", "sequencer"},
}

func inspectWorktree(ctx context.Context, record rawWorktree, isMain bool, set WorktreeSet) Worktree {
	item := Worktree{
		Path: cleanWorktreePath(record.path), Branch: record.branch, Head: record.head, Detached: record.detached || (record.branch == "" && !record.bare),
		IsMain: isMain, Locked: record.locked, LockReason: record.lockReason, Ignored: []string{}, Changes: WorktreeChanges{Files: []string{}},
	}
	if record.prunable {
		item.Missing = true
		return item
	}
	if info, err := os.Stat(item.Path); err != nil || !info.IsDir() {
		item.Missing = true
		return item
	}
	status, err := worktreeRead(ctx, item.Path, 512*1024, "status", "--porcelain=v1", "-z", "--untracked-files=normal", "--ignored=traditional")
	if err != nil {
		return item // StatusKnown stays false: nothing can be promised about this tree.
	}
	item.StatusKnown = true
	summarizeStatus(&item, status)
	item.Operation = pendingOperation(ctx, item.Path)
	if set.BaseKnown && item.Head != "" {
		if counts, err := worktreeRead(ctx, item.Path, 256, "rev-list", "--left-right", "--count", "refs/heads/"+set.Base+"..."+item.Head); err == nil {
			fields := strings.Fields(counts)
			if len(fields) == 2 {
				behind, _ := strconv.Atoi(fields[0])
				ahead, _ := strconv.Atoi(fields[1])
				item.Behind, item.Ahead = behind, ahead
				item.Merged = ahead == 0
			}
		}
	}
	return item
}

func summarizeStatus(item *Worktree, raw string) {
	parts := strings.Split(raw, "\x00")
	for i := 0; i < len(parts); i++ {
		entry := parts[i]
		if len(entry) < 4 {
			continue
		}
		x, y, path := entry[0], entry[1], entry[3:]
		if x == 'R' || y == 'R' || x == 'C' || y == 'C' {
			i++ // porcelain -z carries the rename source in the next field
		}
		switch {
		case x == '!' && y == '!':
			if len(item.Ignored) < maxListedIgnored {
				item.Ignored = append(item.Ignored, path)
			} else {
				item.IgnoredMore++
			}
			continue
		case x == '?' && y == '?':
			item.Changes.Untracked++
		case x == 'U' || y == 'U' || (x == 'A' && y == 'A') || (x == 'D' && y == 'D'):
			item.Changes.Conflicts++
		default:
			if x != ' ' {
				item.Changes.Staged++
			}
			if y != ' ' {
				item.Changes.Unstaged++
			}
		}
		item.Changes.Total++
		if len(item.Changes.Files) < maxListedChanges {
			item.Changes.Files = append(item.Changes.Files, entry[:2]+" "+path)
		} else {
			item.Changes.Truncated = true
		}
	}
}

func pendingOperation(ctx context.Context, dir string) string {
	gitDir, err := worktreeRead(ctx, dir, 4096, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return ""
	}
	gitDir = strings.TrimSpace(gitDir)
	for _, candidate := range operationMarkers {
		if _, err := os.Stat(filepath.Join(gitDir, candidate.marker)); err == nil {
			return candidate.name
		}
	}
	return ""
}

func samePathOrEqualFold(a, b string) bool {
	return samePath(cleanWorktreePath(a), cleanWorktreePath(b))
}

func (s WorktreeSet) find(target string) (Worktree, bool) {
	for _, item := range s.Items {
		if samePathOrEqualFold(item.Path, target) {
			return item, true
		}
	}
	return Worktree{}, false
}

// without is a copy of the set minus one worktree.
func (s WorktreeSet) without(target string) WorktreeSet {
	kept := make([]Worktree, 0, len(s.Items))
	for _, item := range s.Items {
		if !samePathOrEqualFold(item.Path, target) {
			kept = append(kept, item)
		}
	}
	s.Items = kept
	return s
}

// Find returns the listed worktree at target; only paths Git itself listed qualify.
func (s WorktreeSet) Find(target string) (Worktree, bool) { return s.find(target) }

// BaseWorktree is the main working tree, where the base branch is checked out.
func (s WorktreeSet) BaseWorktree() (Worktree, bool) {
	if len(s.Items) == 0 {
		return Worktree{}, false
	}
	return s.Items[0], true
}

// DeleteBlockers lists everything that makes removing the worktree unsafe. An empty list
// means nothing is held only here: the tree is clean and its commits live in the base branch.
func (w Worktree) DeleteBlockers(set WorktreeSet, protected []string) []Blocker {
	var out []Blocker
	if w.IsMain {
		return []Blocker{{Code: BlockerMainWorktree}}
	}
	for _, path := range protected {
		if path != "" && samePathOrEqualFold(path, w.Path) {
			out = append(out, Blocker{Code: BlockerCurrentProject})
			break
		}
	}
	if w.Locked {
		out = append(out, Blocker{Code: BlockerLocked, Detail: w.LockReason})
	}
	if w.Missing {
		return append(out, Blocker{Code: BlockerMissingDirectory})
	}
	if !w.StatusKnown {
		return append(out, Blocker{Code: BlockerStatusUnknown})
	}
	if len(w.InUse) > 0 {
		out = append(out, Blocker{Code: BlockerInUse, Count: len(w.InUse), Detail: describeProcesses(w.InUse)})
	}
	if w.Operation != "" {
		out = append(out, Blocker{Code: BlockerOperation, Detail: w.Operation})
	}
	if w.Changes.Total > 0 {
		out = append(out, Blocker{Code: BlockerUncommitted, Count: w.Changes.Total})
	}
	switch {
	case !set.BaseKnown:
		out = append(out, Blocker{Code: BlockerBaseUnknown})
	case w.Ahead > 0 && w.Detached:
		out = append(out, Blocker{Code: BlockerDetachedCommits, Count: w.Ahead, Detail: set.Base})
	case w.Ahead > 0:
		out = append(out, Blocker{Code: BlockerUnmerged, Count: w.Ahead, Detail: set.Base})
	}
	return out
}

// SaveBlockers lists why the assistant cannot be asked to commit and merge this worktree. The main
// worktree has nowhere to merge into, but its pending changes can be committed on the base branch:
// that is what unblocks every other worktree, which only merges into a clean base.
func (w Worktree) SaveBlockers(set WorktreeSet) []Blocker {
	var out []Blocker
	if w.IsMain {
		switch {
		case !w.StatusKnown:
			return []Blocker{{Code: BlockerStatusUnknown}}
		case w.Operation != "":
			return []Blocker{{Code: BlockerOperation, Detail: w.Operation}}
		case w.Changes.Total == 0:
			return []Blocker{{Code: BlockerMainWorktree}}
		}
		return nil
	}
	if w.Locked {
		out = append(out, Blocker{Code: BlockerLocked, Detail: w.LockReason})
	}
	if w.Missing {
		return append(out, Blocker{Code: BlockerMissingDirectory})
	}
	if !w.StatusKnown {
		return append(out, Blocker{Code: BlockerStatusUnknown})
	}
	if w.Operation != "" {
		out = append(out, Blocker{Code: BlockerOperation, Detail: w.Operation})
	}
	if !set.BaseKnown {
		return append(out, Blocker{Code: BlockerBaseUnknown})
	}
	if base, ok := set.BaseWorktree(); ok && (!base.StatusKnown || base.Changes.Total > 0 || base.Operation != "") {
		out = append(out, Blocker{Code: BlockerBaseDirty, Detail: set.Base, Count: base.Changes.Total})
	}
	if w.Changes.Total == 0 && w.Ahead == 0 && len(out) == 0 {
		out = append(out, Blocker{Code: BlockerNothingToSave})
	}
	return out
}

type RemoveOptions struct {
	DeleteBranch       bool
	AcknowledgeIgnored bool
	// Protected paths are never removed (the project the person has open).
	Protected []string
}

type RemoveResult struct {
	Path          string `json:"path"`
	Branch        string `json:"branch"`
	Removed       bool   `json:"removed"`
	BranchDeleted bool   `json:"branchDeleted"`
	// Remaining is the listing the removal was judged on, minus the removed worktree; removing one
	// working tree changes nothing about the others, so there is no need to read them all again.
	Remaining WorktreeSet `json:"-"`
}

// RemoveWorktree deletes one working tree, only after judging it again from scratch. It never
// forces: Git itself refuses a tree with changes, so a race between the judgement and the
// removal cannot discard work either.
func RemoveWorktree(ctx context.Context, workspacePath, target string, options RemoveOptions) (RemoveResult, error) {
	set, err := ListWorktrees(ctx, workspacePath)
	if err != nil {
		return RemoveResult{}, err
	}
	item, found := set.find(target)
	if !found {
		return RemoveResult{}, ErrWorktreeNotFound
	}
	blockers := item.DeleteBlockers(set, options.Protected)
	if ignored := len(item.Ignored) + item.IgnoredMore; ignored > 0 && !options.AcknowledgeIgnored && len(blockers) == 0 {
		blockers = append(blockers, Blocker{Code: BlockerIgnoredFiles, Count: ignored})
	}
	if len(blockers) > 0 {
		return RemoveResult{}, &NotDeletableError{Blockers: blockers}
	}
	if _, _, err := runGit(ctx, set.Root, 3*time.Minute, nil, "worktree", "remove", "--", item.Path); err != nil {
		return RemoveResult{}, ErrWorktreeRemoveFailed
	}
	if _, err := os.Lstat(item.Path); err == nil {
		return RemoveResult{}, ErrWorktreeRemoveFailed
	}
	result := RemoveResult{Path: item.Path, Branch: item.Branch, Removed: true, Remaining: set.without(item.Path)}
	if options.DeleteBranch && item.Branch != "" && item.Branch != set.Base {
		// -d refuses a branch the checked-out base does not contain, so this is safe by itself.
		if _, _, err := runGit(ctx, set.Root, 30*time.Second, nil, "branch", "-d", "--", item.Branch); err == nil {
			result.BranchDeleted = true
		}
	}
	return result, nil
}

// PruneWorktrees forgets entries whose directory no longer exists. Git leaves locked ones alone.
func PruneWorktrees(ctx context.Context, workspacePath string) error {
	set, err := ListWorktreesWithoutUsage(ctx, workspacePath)
	if err != nil {
		return err
	}
	if _, _, err := runGit(ctx, set.Root, 30*time.Second, nil, "worktree", "prune"); err != nil {
		return fmt.Errorf("prune worktrees: %w", err)
	}
	return nil
}

type SavedWorktree struct {
	Ref       string `json:"ref"`
	Commit    string `json:"commit"`
	HadChange bool   `json:"hadChange"`
}

var refUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// SaveWorktreePending keeps a commit of everything the worktree holds right now under a ref
// of its own, without touching its index, files or branch. If anything later goes wrong, the
// pending work is still there to recover.
func SaveWorktreePending(ctx context.Context, workspacePath, target string) (SavedWorktree, error) {
	set, err := ListWorktreesWithoutUsage(ctx, workspacePath)
	if err != nil {
		return SavedWorktree{}, err
	}
	item, found := set.find(target)
	if !found {
		return SavedWorktree{}, ErrWorktreeNotFound
	}
	if blockers := item.SaveBlockers(set); len(blockers) > 0 {
		// A clean, already merged tree has nothing to save; every other blocker is real.
		if !(len(blockers) == 1 && blockers[0].Code == BlockerNothingToSave) {
			return SavedWorktree{}, &SaveBlockedError{Blockers: blockers}
		}
	}
	indexDir, err := os.MkdirTemp("", "harflex-worktree-save-")
	if err != nil {
		return SavedWorktree{}, fmt.Errorf("prepare temporary index: %w", err)
	}
	defer os.RemoveAll(indexDir)
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(indexDir, "index")}
	if _, _, err := runGit(ctx, item.Path, time.Minute, env, "read-tree", "HEAD"); err != nil {
		return SavedWorktree{}, fmt.Errorf("read the worktree head: %w", err)
	}
	if _, _, err := runGit(ctx, item.Path, 5*time.Minute, env, "add", "-A"); err != nil {
		return SavedWorktree{}, fmt.Errorf("copy the pending files: %w", err)
	}
	tree, _, err := runGit(ctx, item.Path, time.Minute, env, "write-tree")
	if err != nil {
		return SavedWorktree{}, fmt.Errorf("write the pending tree: %w", err)
	}
	headTree, _, err := runGit(ctx, item.Path, time.Minute, nil, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return SavedWorktree{}, fmt.Errorf("read the head tree: %w", err)
	}
	tree, headTree = strings.TrimSpace(tree), strings.TrimSpace(headTree)
	if tree == headTree {
		return SavedWorktree{Commit: item.Head}, nil
	}
	label := item.Branch
	if label == "" {
		label = "detached"
	}
	identity := commitIdentity(ctx, item.Path)
	commit, _, err := runGit(ctx, item.Path, time.Minute, identity, "commit-tree", tree, "-p", "HEAD", "-m", "Harflex: pending work of "+label+" before the assistant saved it")
	if err != nil {
		return SavedWorktree{}, fmt.Errorf("write the safety commit: %w", err)
	}
	commit = strings.TrimSpace(commit)
	ref := "refs/harflex/worktree-saves/" + strings.Trim(refUnsafe.ReplaceAllString(label, "-"), "-.") + "-" + time.Now().UTC().Format("20060102T150405Z")
	if _, _, err := runGit(ctx, item.Path, time.Minute, nil, "update-ref", ref, commit); err != nil {
		return SavedWorktree{}, fmt.Errorf("record the safety ref: %w", err)
	}
	return SavedWorktree{Ref: ref, Commit: commit, HadChange: true}, nil
}

func commitIdentity(ctx context.Context, dir string) []string {
	name, _ := worktreeRead(ctx, dir, 512, "config", "user.name")
	email, _ := worktreeRead(ctx, dir, 512, "config", "user.email")
	name, email = strings.TrimSpace(name), strings.TrimSpace(email)
	if name == "" {
		name = "Harflex"
	}
	if email == "" {
		email = "harflex@localhost.invalid"
	}
	return []string{"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + email, "GIT_COMMITTER_NAME=" + name, "GIT_COMMITTER_EMAIL=" + email}
}

// SortedBlockerCodes is a small helper for tests and logs.
func SortedBlockerCodes(blockers []Blocker) []string {
	codes := make([]string, 0, len(blockers))
	for _, blocker := range blockers {
		codes = append(codes, blocker.Code)
	}
	sort.Strings(codes)
	return codes
}

// RepositoryWorktrees is one repository reached from one or more of the given folders.
type RepositoryWorktrees struct {
	// Folders are the indexes, in the given list, of the folders that live in this repository.
	Folders []int
	Set     WorktreeSet
	Err     error
}

// ListRepositoryWorktrees judges the worktrees of every repository the folders live in. Folders of the same
// repository share one answer; a folder outside any repository is left out. Who uses each folder is read
// once for all of them, because that read (lsof) is the slow part.
func ListRepositoryWorktrees(ctx context.Context, folders []string) ([]RepositoryWorktrees, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, ErrGitUnavailable
	}
	type answer struct {
		set WorktreeSet
		err error
	}
	answers := make([]answer, len(folders))
	slots := make(chan struct{}, 4)
	var group sync.WaitGroup
	for index, folder := range folders {
		group.Add(1)
		slots <- struct{}{}
		go func(index int, folder string) {
			defer group.Done()
			defer func() { <-slots }()
			set, err := listWorktrees(ctx, folder, false)
			answers[index] = answer{set, err}
		}(index, folder)
	}
	group.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []RepositoryWorktrees
	byRoot := make(map[string]int)
	for index, item := range answers {
		if errors.Is(item.err, ErrNotRepository) {
			continue
		}
		if item.err != nil {
			out = append(out, RepositoryWorktrees{Folders: []int{index}, Err: item.err})
			continue
		}
		if at, seen := byRoot[item.set.Root]; seen {
			out[at].Folders = append(out[at].Folders, index)
			continue
		}
		byRoot[item.set.Root] = len(out)
		out = append(out, RepositoryWorktrees{Folders: []int{index}, Set: item.set})
	}
	var linked []string
	for _, repository := range out {
		for _, item := range repository.Set.Items {
			if !item.IsMain && !item.Missing {
				linked = append(linked, item.Path)
			}
		}
	}
	users := ProcessesUsing(ctx, linked)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for r := range out {
		for i := range out[r].Set.Items {
			if item := &out[r].Set.Items[i]; !item.IsMain && !item.Missing {
				item.InUse = users[item.Path]
			}
		}
	}
	return out, nil
}
