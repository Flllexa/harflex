package repositories

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxNestedExecutionRepositories = 60
	maxNestedRepositoryProbes      = 2000
	nestedRepositoryProbeDepth     = 2
)

// ExecutionRepository is a Git repository inside the project that the project's own repository ignores, such as
// domains/<name> in a multi-repo workspace. Its tracked files join the copy under Path, and each change made there is
// applied back to that repository, on its own branch.
type ExecutionRepository struct {
	// Path is where the repository sits inside the project and the copy, with forward slashes.
	Path string `json:"path"`
	// SourceRoot is the repository's canonical top level; a repository reached through a symlink lives elsewhere.
	SourceRoot string `json:"sourceRoot"`
	Head       string `json:"head"`
	Branch     string `json:"branch"`
}

// ExecutionSkippedRepository is a nested repository left out of the copy, with the reason.
type ExecutionSkippedRepository struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

const (
	SkippedRepositoryUncommitted = "uncommitted_changes"
	SkippedRepositoryNoCommits   = "no_commits"
	SkippedRepositorySubmodules  = "submodules"
	SkippedRepositoryLimit       = "repository_limit"
)

// executionLocation is where one project path lives on disk: the root of the repository that owns it and the path
// inside that root.
type executionLocation struct {
	root  string
	inner string
	// repository is the index in Repositories, or -1 for the project's own repository.
	repository int
}

func (l executionLocation) abs() string { return filepath.Join(l.root, filepath.FromSlash(l.inner)) }

// locate maps a project path to the repository that owns it. The deepest nested repository wins.
func (s ExecutionSnapshot) locate(slashPath string) executionLocation {
	best := -1
	for index, repository := range s.Repositories {
		if strings.HasPrefix(slashPath, repository.Path+"/") && (best < 0 || len(repository.Path) > len(s.Repositories[best].Path)) {
			best = index
		}
	}
	if best < 0 {
		return executionLocation{root: s.SourceRoot, inner: slashPath, repository: -1}
	}
	repository := s.Repositories[best]
	return executionLocation{root: repository.SourceRoot, inner: strings.TrimPrefix(slashPath, repository.Path+"/"), repository: best}
}

// sourceEntry reads the manifest entry of a project path from the repository that owns it.
func (s ExecutionSnapshot) sourceEntry(slashPath string, maxBytes int64) (ExecutionManifestEntry, int64, error) {
	location := s.locate(slashPath)
	entry, size, err := executionManifestPath(location.root, location.inner, maxBytes)
	entry.Path = slashPath
	return entry, size, err
}

// nestedExecutionRepositories finds the Git repositories inside ignored paths of the project's repository. A worktree of
// the project's own repository (such as .claude/worktrees/x) is not a nested repository and is left alone.
func nestedExecutionRepositories(ctx context.Context, root string, ignored []string) ([]ExecutionRepository, []ExecutionSkippedRepository, error) {
	common, err := gitCommonDir(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	var repositories []ExecutionRepository
	var skipped []ExecutionSkippedRepository
	type candidate struct {
		path  string
		depth int
	}
	// Breadth first: every ignored entry is looked at before any folder below one, so a large ignored folder (a
	// build output, a cache) never uses up the search before domains/<name> is reached.
	queue := make([]candidate, 0, len(ignored))
	for _, path := range ignored {
		if path != ".git" && safeExecutionRelativePath(filepath.FromSlash(path)) && !hiddenPath(path) && !dependencyPath(path) {
			queue = append(queue, candidate{path: path})
		}
	}
	seen := map[string]bool{}
	for probes := 0; len(queue) > 0 && probes < maxNestedRepositoryProbes; probes++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		next := queue[0]
		queue = queue[1:]
		if seen[next.path] {
			continue
		}
		seen[next.path] = true
		path := filepath.Join(root, filepath.FromSlash(next.path))
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}
		if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
			repository, reason, err := nestedExecutionRepository(ctx, next.path, path, common)
			if err != nil {
				return nil, nil, err
			}
			switch {
			case reason != "":
				skipped = append(skipped, ExecutionSkippedRepository{Path: next.path, Reason: reason})
			case repository == nil:
			case len(repositories) >= maxNestedExecutionRepositories:
				skipped = append(skipped, ExecutionSkippedRepository{Path: next.path, Reason: SkippedRepositoryLimit})
			default:
				repositories = append(repositories, *repository)
			}
			continue
		}
		// A link is followed only to the repository it names, never searched below.
		if linked, err := os.Lstat(path); next.depth >= nestedRepositoryProbeDepth || err != nil || linked.Mode()&os.ModeSymlink != 0 {
			continue
		}
		children, err := os.ReadDir(path)
		if err != nil {
			continue
		}
		for _, child := range children {
			if !hiddenOrDependency(child.Name()) && (child.IsDir() || child.Type()&os.ModeSymlink != 0) {
				queue = append(queue, candidate{path: next.path + "/" + child.Name(), depth: next.depth + 1})
			}
		}
	}
	sort.Slice(repositories, func(i, j int) bool { return repositories[i].Path < repositories[j].Path })
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].Path < skipped[j].Path })
	return repositories, skipped, nil
}

// nestedExecutionRepository inspects one candidate. It returns the repository, or the reason it stays out, or neither
// when the folder is not the top of a repository of its own.
func nestedExecutionRepository(ctx context.Context, slashPath, path, projectCommon string) (*ExecutionRepository, string, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, "", nil
	}
	top, _, err := gitOutput(ctx, canonical, 4096, "rev-parse", "--show-toplevel")
	if err != nil || !samePath(strings.TrimSpace(top), canonical) {
		return nil, "", nil
	}
	common, err := gitCommonDir(ctx, canonical)
	if err != nil || samePath(common, projectCommon) {
		return nil, "", nil
	}
	head, _, err := gitOutput(ctx, canonical, 256, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return nil, SkippedRepositoryNoCommits, nil
	}
	// Untracked files never reach the copy, so only changes to tracked files keep a repository out.
	status, truncated, err := gitOutput(ctx, canonical, 1024*1024, "status", "--porcelain=v1", "-z", "--untracked-files=no", "--no-renames")
	if err != nil {
		return nil, "", err
	}
	if truncated || status != "" {
		return nil, SkippedRepositoryUncommitted, nil
	}
	branch, _, err := gitOutput(ctx, canonical, 4096, "branch", "--show-current")
	if err != nil {
		return nil, "", err
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		branch = "HEAD destacado"
	}
	return &ExecutionRepository{Path: slashPath, SourceRoot: canonical, Head: strings.TrimSpace(head), Branch: branch}, "", nil
}

func gitCommonDir(ctx context.Context, root string) (string, error) {
	common, _, err := gitOutput(ctx, root, 4096, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", ErrGitUnavailable
	}
	resolved, err := filepath.EvalSymlinks(strings.TrimSpace(common))
	if err != nil {
		return strings.TrimSpace(common), nil
	}
	return resolved, nil
}

// nestedRepositoryPaths lists the tracked files of a nested repository, as project paths.
func nestedRepositoryPaths(ctx context.Context, repository ExecutionRepository) ([]string, bool, error) {
	tracked, truncated, err := gitOutput(ctx, repository.SourceRoot, 8*1024*1024, "ls-files", "-z", "--stage", "--cached")
	if err != nil || truncated {
		return nil, false, ErrExecutionSnapshotTooLarge
	}
	paths := make([]string, 0)
	for _, raw := range strings.Split(tracked, "\x00") {
		if raw == "" {
			continue
		}
		tab := strings.IndexByte(raw, '\t')
		if tab <= 0 || tab == len(raw)-1 {
			return nil, false, ErrExecutionSnapshotUnsafePath
		}
		mode := strings.Fields(raw[:tab])
		if len(mode) != 3 {
			return nil, false, ErrExecutionSnapshotUnsafePath
		}
		if mode[0] == "160000" {
			return nil, true, nil
		}
		paths = append(paths, repository.Path+"/"+raw[tab+1:])
	}
	return paths, false, nil
}

// repositoryIdentityMatches reports whether a nested repository is still on the commit and branch the copy was made from.
func repositoryIdentityMatches(ctx context.Context, repository ExecutionRepository) bool {
	head, _, err := gitOutput(ctx, repository.SourceRoot, 256, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || strings.TrimSpace(head) != repository.Head {
		return false
	}
	branch, _, err := gitOutput(ctx, repository.SourceRoot, 4096, "branch", "--show-current")
	if err != nil {
		return false
	}
	current := strings.TrimSpace(branch)
	if current == "" {
		current = "HEAD destacado"
	}
	return current == repository.Branch
}

// hiddenPath reports a path with a hidden segment. Hidden folders (.claude, .local, .codex) hold tool state, such as
// worktrees of the project's repositories, and never a repository the project is made of.
func hiddenPath(slashPath string) bool {
	for _, part := range strings.Split(slashPath, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

var dependencyFolders = map[string]bool{"node_modules": true, "vendor": true, "__pycache__": true, "dist": true, "build": true, "target": true}

func hiddenOrDependency(name string) bool {
	return strings.HasPrefix(name, ".") || dependencyFolders[name]
}

// dependencyPath reports a path inside a dependency or build folder, where no repository of the project lives.
func dependencyPath(slashPath string) bool {
	for _, part := range strings.Split(slashPath, "/") {
		if dependencyFolders[part] {
			return true
		}
	}
	return false
}

// RepositoryFor returns the nested repository that owns a project path, if any.
func (s ExecutionSnapshot) RepositoryFor(slashPath string) (ExecutionRepository, bool) {
	location := s.locate(slashPath)
	if location.repository < 0 {
		return ExecutionRepository{}, false
	}
	return s.Repositories[location.repository], true
}

// ChangedRepositories lists, in path order, the nested repositories that own at least one of the paths.
func (s ExecutionSnapshot) ChangedRepositories(paths []string) []ExecutionRepository {
	touched := map[string]ExecutionRepository{}
	for _, path := range paths {
		if repository, ok := s.RepositoryFor(path); ok {
			touched[repository.Path] = repository
		}
	}
	result := make([]ExecutionRepository, 0, len(touched))
	for _, repository := range touched {
		result = append(result, repository)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}
