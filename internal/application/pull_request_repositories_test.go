package application

import (
	"strings"
	"testing"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/repositories"
)

func TestPullRequestsAreOpenedOnePerChangedRepository(t *testing.T) {
	snapshot := repositories.ExecutionSnapshot{SourceRoot: "/w", SourceBranch: "main", Repositories: []repositories.ExecutionRepository{
		{Path: "domains/contracts", SourceRoot: "/w/domains/contracts", Head: "a", Branch: "main"},
		{Path: "domains/settlement", SourceRoot: "/elsewhere/settlement", Head: "b", Branch: "develop"},
		{Path: "domains/fidc", SourceRoot: "/w/domains/fidc", Head: "c", Branch: "main"},
	}}
	code := "diff --git a/x b/x\n# Harflex isolated Code provenance\n" +
		"# file=\"domains/contracts/baixa.go\" action=modified before=1 mode=0644 type=file after=2 mode=0644 type=file\n" +
		"# file=\"domains/settlement/settle.go\" action=added before=absent mode=- type=- after=2 mode=0644 type=file\n" +
		"# file=\"docs/notes.md\" action=modified before=1 mode=0644 type=file after=2 mode=0644 type=file\n"
	repos := pipelinePRRepositories(code, snapshot)
	want := []prRepository{{Path: ".", Branch: "main"}, {Path: "domains/contracts", Branch: "main"}, {Path: "domains/settlement", Branch: "develop"}}
	if len(repos) != len(want) {
		t.Fatalf("repositories = %+v", repos)
	}
	for index := range want {
		if repos[index] != want[index] {
			t.Fatalf("repositories = %+v, want %+v", repos, want)
		}
	}
	prompt := pipelinePRPrompt(catalog.PipelineRun{Title: "Baixa"}, repos)
	for _, part := range []string{"um pull request para cada um", "`domains/settlement`, a partir da branch develop", "`.` (o repositório do projeto)", "informe também repository"} {
		if !strings.Contains(prompt, part) {
			t.Fatalf("prompt lacks %q:\n%s", part, prompt)
		}
	}

	// A work that only changed the project's own repository keeps the single repository prompt.
	single := pipelinePRRepositories("# file=\"docs/notes.md\" action=modified\n", snapshot)
	if single != nil || strings.Contains(pipelinePRPrompt(catalog.PipelineRun{}, single), "um pull request para cada um") {
		t.Fatalf("single repository: %+v", single)
	}
}

func TestPullRequestRepositoryIsKeptAndChecked(t *testing.T) {
	for raw, ok := range map[string]bool{"": true, ".": true, "domains/contracts": true, "domains/contracts/": true, "../outside": false, "/abs": false, "a/../../b": false} {
		if validPullRequestRepository(raw) != ok {
			t.Fatalf("validPullRequestRepository(%q) = %v", raw, !ok)
		}
	}
	if got := cleanPullRequestRepository("domains/contracts/"); got != "domains/contracts" {
		t.Fatalf("clean = %q", got)
	}
	prompt := pullRequestWatchPrompt(catalog.PipelinePullRequest{URL: "https://github.com/acme/contracts/pull/3", Branch: "harflex/baixa", Repository: "domains/contracts"}, catalog.PipelinePullRequest{}.CreatedAt)
	if !strings.Contains(prompt, "pasta domains/contracts") {
		t.Fatalf("the watch must work in the repository's folder:\n%s", prompt)
	}
	if strings.Contains(pullRequestWatchPrompt(catalog.PipelinePullRequest{URL: "https://x.y/1", Repository: "."}, catalog.PipelinePullRequest{}.CreatedAt), "pasta") {
		t.Fatal("the project's own repository needs no folder")
	}
}
