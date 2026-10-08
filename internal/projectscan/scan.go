// Package projectscan gathers, without any AI, the files that say what a project is: its documentation, its
// manifests, the shape of its folders and the Git repositories inside it. The result is bounded and never includes
// secrets (.env files, keys, certificates), so it can be handed to a model as plain text.
package projectscan

import (
	"bytes"
	"context"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// MaxFileBytes is how much of one file at the project root is kept, nestedFileBytes of any other one;
	// MaxTotalBytes bounds every file together.
	MaxFileBytes    = 8 * 1024
	nestedFileBytes = 3 * 1024
	MaxTotalBytes   = 120 * 1024
	maxDocFiles     = 40
	maxTreeLines    = 300
	maxRepos        = 60
	maxDepth        = 4
	maxEntries      = 20000
)

// File is one file kept for the model, by its path relative to the project.
type File struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
}

// Repository is a Git repository found inside the project (the project's own root included).
type Repository struct {
	Path   string `json:"path"`
	Remote string `json:"remote,omitempty"`
	Branch string `json:"branch,omitempty"`
}

// Snapshot is what was gathered from one project.
type Snapshot struct {
	Tree         []string     `json:"tree"`
	Repositories []Repository `json:"repositories"`
	Files        []File       `json:"files"`
}

// Sources lists the files kept, in the order they were kept.
func (s Snapshot) Sources() []string {
	out := make([]string, 0, len(s.Files))
	for _, file := range s.Files {
		out = append(out, file.Path)
	}
	return out
}

var skippedDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true, "build": true, "bin": true, "obj": true, "target": true,
	".worktrees": true, ".claude": true, ".codex": true, ".idea": true, ".vscode": true, ".gradle": true, ".next": true, ".nuxt": true,
	".venv": true, "venv": true, "__pycache__": true, ".terraform": true, ".aws-sam": true, "coverage": true, "test-results": true,
	"playwright-report": true, "tmp": true, ".cache": true, "graphify-out": true,
}

// Manifests say which technologies and services a folder uses.
var manifests = map[string]bool{
	"go.mod": true, "package.json": true, "pom.xml": true, "build.gradle": true, "build.gradle.kts": true, "settings.gradle": true,
	"settings.gradle.kts": true, "Cargo.toml": true, "pyproject.toml": true, "requirements.txt": true, "Pipfile": true, "Gemfile": true,
	"composer.json": true, "mix.exs": true, "pubspec.yaml": true, "Package.swift": true, "template.yaml": true, "template.yml": true,
	"samconfig.toml": true, "serverless.yml": true, "serverless.yaml": true, "docker-compose.yml": true, "docker-compose.yaml": true,
	"compose.yml": true, "compose.yaml": true, "Dockerfile": true, "Makefile": true, "Taskfile.yml": true, "main.tf": true,
	"openapi.yaml": true, "openapi.yml": true, "openapi.json": true, "asyncapi.yaml": true, "asyncapi.yml": true, "wails.json": true,
	"angular.json": true, "vite.config.ts": true, "next.config.js": true, "tsconfig.json": true, "cdk.json": true,
}

// Guides describe the project in prose.
var guides = map[string]bool{
	"readme.md": true, "readme": true, "readme.txt": true, "agents.md": true, "claude.md": true, "contributing.md": true,
	"architecture.md": true, "design.md": true, "product.md": true, "overview.md": true,
}

func secret(name string) bool {
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, ".env") || strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".key") || strings.HasSuffix(lower, ".p12") ||
		strings.HasSuffix(lower, ".pfx") || strings.HasPrefix(lower, "id_rsa") || strings.HasPrefix(lower, "id_ed25519") {
		return true
	}
	for _, word := range []string{"secret", "credential", "token", "password"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

type candidate struct {
	rel      string
	priority int
}

// Scan gathers the snapshot of the project at root. It reads nothing outside root and follows no symlinks.
func Scan(ctx context.Context, root string) (Snapshot, error) {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return Snapshot{}, err
	}
	var snapshot Snapshot
	var candidates []candidate
	entries := 0
	walkErr := filepath.WalkDir(root, func(full string, entry fs.DirEntry, err error) error {
		if err != nil {
			if full == root {
				return err
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		entries++
		if entries > maxEntries {
			return filepath.SkipAll
		}
		rel, relErr := filepath.Rel(root, full)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		depth := strings.Count(rel, "/")
		name := entry.Name()
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() {
			if rel == "." {
				snapshot.addRepository(ctx, root, ".")
				return nil
			}
			if skippedDirs[name] || (strings.HasPrefix(name, ".") && name != ".github") || depth >= maxDepth {
				if _, statErr := os.Stat(filepath.Join(full, ".git")); statErr == nil && depth < maxDepth && !skippedDirs[name] {
					snapshot.addRepository(ctx, full, rel)
				}
				return filepath.SkipDir
			}
			if _, statErr := os.Stat(filepath.Join(full, ".git")); statErr == nil {
				snapshot.addRepository(ctx, full, rel)
			}
			if depth <= 1 && len(snapshot.Tree) < maxTreeLines {
				snapshot.Tree = append(snapshot.Tree, rel+"/")
			}
			return nil
		}
		if !entry.Type().IsRegular() || secret(name) {
			return nil
		}
		lower := strings.ToLower(name)
		// The guide and manifests at each repository's root say the most about it, so every repository gets them
		// before any deeper document.
		repositoryRoot := false
		for _, repository := range snapshot.Repositories {
			if path.Dir(rel) == repository.Path {
				repositoryRoot = true
			}
		}
		switch {
		case guides[lower] && depth == 0:
			candidates = append(candidates, candidate{rel, 0})
		case manifests[name] && depth == 0:
			candidates = append(candidates, candidate{rel, 5})
		case guides[lower] && repositoryRoot:
			candidates = append(candidates, candidate{rel, 10})
		case manifests[name] && repositoryRoot:
			candidates = append(candidates, candidate{rel, 15})
		case guides[lower]:
			candidates = append(candidates, candidate{rel, 20 + depth*10})
		case manifests[name]:
			candidates = append(candidates, candidate{rel, 25 + depth*10})
		case strings.HasPrefix(rel, ".github/workflows/") && (strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".yaml")):
			candidates = append(candidates, candidate{rel, 35})
		case strings.HasSuffix(lower, ".md") && (strings.HasPrefix(rel, "docs/") || strings.Contains(rel, "/docs/")):
			candidates = append(candidates, candidate{rel, 40 + depth*10})
		}
		return nil
	})
	if walkErr != nil && walkErr != filepath.SkipAll {
		if ctx.Err() != nil {
			return Snapshot{}, ctx.Err()
		}
		return Snapshot{}, walkErr
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].priority != candidates[j].priority {
			return candidates[i].priority < candidates[j].priority
		}
		return candidates[i].rel < candidates[j].rel
	})
	total, docs := 0, 0
	for _, item := range candidates {
		if total >= MaxTotalBytes {
			break
		}
		isDoc := strings.HasSuffix(strings.ToLower(item.rel), ".md") && !guides[strings.ToLower(path.Base(item.rel))]
		if isDoc && docs >= maxDocFiles {
			continue
		}
		limit := nestedFileBytes
		if !strings.Contains(item.rel, "/") {
			limit = MaxFileBytes
		}
		content, truncated, ok := readText(filepath.Join(root, filepath.FromSlash(item.rel)), min(limit, MaxTotalBytes-total))
		if !ok || strings.TrimSpace(content) == "" {
			continue
		}
		if isDoc {
			docs++
		}
		total += len(content)
		snapshot.Files = append(snapshot.Files, File{Path: item.rel, Content: content, Truncated: truncated})
	}
	return snapshot, nil
}

func readText(full string, limit int) (string, bool, bool) {
	if limit <= 0 {
		return "", false, false
	}
	file, err := os.Open(full)
	if err != nil {
		return "", false, false
	}
	defer file.Close()
	buffer := make([]byte, limit+1)
	n, _ := file.Read(buffer)
	data := buffer[:n]
	truncated := n > limit
	if truncated {
		data = data[:limit]
		for len(data) > 0 && !utf8.Valid(data) {
			data = data[:len(data)-1]
		}
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return "", false, false
	}
	return string(data), truncated, true
}

func (s *Snapshot) addRepository(ctx context.Context, dir, rel string) {
	if len(s.Repositories) >= maxRepos {
		return
	}
	for _, existing := range s.Repositories {
		if existing.Path == rel {
			return
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return
	}
	repository := Repository{Path: rel, Remote: safeRemote(gitValue(ctx, dir, "remote", "get-url", "origin")), Branch: gitValue(ctx, dir, "branch", "--show-current")}
	s.Repositories = append(s.Repositories, repository)
}

func gitValue(parent context.Context, dir string, args ...string) string {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(string(output))
	if len(value) > 512 || strings.ContainsAny(value, "\r\n") {
		return ""
	}
	return value
}

// safeRemote drops any user or password from a remote URL, so a token kept in it never reaches the model.
func safeRemote(remote string) string {
	if remote == "" {
		return ""
	}
	if parsed, err := url.Parse(remote); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		parsed.User = nil
		return parsed.String()
	}
	if at := strings.Index(remote, "@"); at >= 0 && strings.Contains(remote[at:], ":") {
		return remote[at+1:]
	}
	return remote
}
