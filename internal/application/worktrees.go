package application

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/repositories"
)

type WorktreeBlockerDTO struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
	Count  int    `json:"count"`
}

type WorktreeDTO struct {
	Path             string               `json:"path"`
	Name             string               `json:"name"`
	Branch           string               `json:"branch"`
	Head             string               `json:"head"`
	IsMain           bool                 `json:"isMain"`
	IsCurrent        bool                 `json:"isCurrent"`
	Detached         bool                 `json:"detached"`
	Locked           bool                 `json:"locked"`
	LockReason       string               `json:"lockReason"`
	Missing          bool                 `json:"missing"`
	StatusKnown      bool                 `json:"statusKnown"`
	Staged           int                  `json:"staged"`
	Unstaged         int                  `json:"unstaged"`
	Untracked        int                  `json:"untracked"`
	Conflicts        int                  `json:"conflicts"`
	Changed          int                  `json:"changed"`
	ChangedFiles     []string             `json:"changedFiles"`
	ChangesTruncated bool                 `json:"changesTruncated"`
	Operation        string               `json:"operation"`
	Ahead            int                  `json:"ahead"`
	Behind           int                  `json:"behind"`
	Merged           bool                 `json:"merged"`
	Ignored          []string             `json:"ignored"`
	IgnoredMore      int                  `json:"ignoredMore"`
	CanDelete        bool                 `json:"canDelete"`
	Blockers         []WorktreeBlockerDTO `json:"blockers"`
	CanSaveWithAI    bool                 `json:"canSaveWithAi"`
	SaveBlockers     []WorktreeBlockerDTO `json:"saveBlockers"`
}

type WorktreeListDTO struct {
	IsRepository bool          `json:"isRepository"`
	Root         string        `json:"root"`
	Base         string        `json:"base"`
	BaseKnown    bool          `json:"baseKnown"`
	CurrentPath  string        `json:"currentPath"`
	Items        []WorktreeDTO `json:"items"`
}

type DeleteWorktreeInput struct {
	WorkspaceID        string `json:"workspaceId"`
	Path               string `json:"path"`
	DeleteBranch       bool   `json:"deleteBranch"`
	AcknowledgeIgnored bool   `json:"acknowledgeIgnored"`
}

type DeleteWorktreeResultDTO struct {
	Path          string          `json:"path"`
	Branch        string          `json:"branch"`
	Removed       bool            `json:"removed"`
	BranchDeleted bool            `json:"branchDeleted"`
	List          WorktreeListDTO `json:"list"`
}

type PrepareWorktreeSaveInput struct {
	WorkspaceID string `json:"workspaceId"`
	Path        string `json:"path"`
}

// WorktreeSavePlanDTO is everything the renderer needs to hand a worktree's pending work to the
// assistant: the project it runs in (the main checkout), the reason that session records and
// the instructions. The safety copy already exists when this is returned.
type WorktreeSavePlanDTO struct {
	Workspace      WorkspaceDTO `json:"workspace"`
	Path           string       `json:"path"`
	Branch         string       `json:"branch"`
	Base           string       `json:"base"`
	SnapshotRef    string       `json:"snapshotRef"`
	SnapshotCommit string       `json:"snapshotCommit"`
	HadPending     bool         `json:"hadPending"`
	Reason         string       `json:"reason"`
	Prompt         string       `json:"prompt"`
}

func validWorktreePath(path string) bool {
	if path == "" || len(path) > 4096 || !utf8.ValidString(path) || !filepath.IsAbs(path) {
		return false
	}
	return strings.IndexFunc(path, unicode.IsControl) < 0
}

func (s *Service) worktreeWorkspace(workspaceID string) (catalog.Workspace, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return catalog.Workspace{}, ErrInvalidInput
	}
	workspace, err := s.store.GetWorkspace(s.ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.Workspace{}, ErrWorkspaceNotFound
	}
	if err != nil {
		return catalog.Workspace{}, safe("get workspace", err)
	}
	return workspace, nil
}

func worktreeBlockerDTOs(blockers []repositories.Blocker) []WorktreeBlockerDTO {
	out := make([]WorktreeBlockerDTO, 0, len(blockers))
	for _, blocker := range blockers {
		out = append(out, WorktreeBlockerDTO{Code: blocker.Code, Detail: blocker.Detail, Count: blocker.Count})
	}
	return out
}

// currentWorktree names the working tree the open project lives in (the deepest one that contains it).
func currentWorktree(set repositories.WorktreeSet, workspacePath string) string {
	canonical, err := filepath.EvalSymlinks(workspacePath)
	if err != nil {
		canonical = filepath.Clean(workspacePath)
	}
	best := ""
	for _, item := range set.Items {
		rel, err := filepath.Rel(item.Path, canonical)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if len(item.Path) > len(best) {
			best = item.Path
		}
	}
	return best
}

func worktreeListDTO(set repositories.WorktreeSet, workspacePath string) WorktreeListDTO {
	current := currentWorktree(set, workspacePath)
	out := WorktreeListDTO{IsRepository: true, Root: set.Root, Base: set.Base, BaseKnown: set.BaseKnown, CurrentPath: current, Items: make([]WorktreeDTO, 0, len(set.Items))}
	for _, item := range set.Items {
		blockers := item.DeleteBlockers(set, []string{current})
		saveBlockers := item.SaveBlockers(set)
		head := item.Head
		if len(head) > 12 {
			head = head[:12]
		}
		out.Items = append(out.Items, WorktreeDTO{
			Path: item.Path, Name: filepath.Base(item.Path), Branch: item.Branch, Head: head,
			IsMain: item.IsMain, IsCurrent: item.Path == current, Detached: item.Detached, Locked: item.Locked, LockReason: item.LockReason,
			Missing: item.Missing, StatusKnown: item.StatusKnown,
			Staged: item.Changes.Staged, Unstaged: item.Changes.Unstaged, Untracked: item.Changes.Untracked, Conflicts: item.Changes.Conflicts,
			Changed: item.Changes.Total, ChangedFiles: item.Changes.Files, ChangesTruncated: item.Changes.Truncated,
			Operation: item.Operation, Ahead: item.Ahead, Behind: item.Behind, Merged: item.Merged,
			Ignored: item.Ignored, IgnoredMore: item.IgnoredMore,
			CanDelete: len(blockers) == 0, Blockers: worktreeBlockerDTOs(blockers),
			CanSaveWithAI: len(saveBlockers) == 0, SaveBlockers: worktreeBlockerDTOs(saveBlockers),
		})
	}
	sort.SliceStable(out.Items[1:], func(i, j int) bool {
		return strings.ToLower(out.Items[1+i].Name) < strings.ToLower(out.Items[1+j].Name)
	})
	return out
}

// ListWorktrees judges every working tree of the project's repository and says, for each one,
// whether deleting it would lose anything.
func (s *Service) ListWorktrees(workspaceID string) (WorktreeListDTO, error) {
	if err := s.beginCall(); err != nil {
		return WorktreeListDTO{}, err
	}
	defer s.endCall()
	workspace, err := s.worktreeWorkspace(workspaceID)
	if err != nil {
		return WorktreeListDTO{}, err
	}
	return s.listWorktrees(workspace)
}

func (s *Service) listWorktrees(workspace catalog.Workspace) (WorktreeListDTO, error) {
	set, err := repositories.ListWorktrees(s.ctx, workspace.Path)
	switch {
	case errors.Is(err, repositories.ErrNotRepository):
		return WorktreeListDTO{IsRepository: false, Items: []WorktreeDTO{}}, nil
	case errors.Is(err, repositories.ErrGitUnavailable):
		return WorktreeListDTO{}, err
	case err != nil:
		return WorktreeListDTO{}, safe("list worktrees", err)
	}
	return worktreeListDTO(set, workspace.Path), nil
}

// DeleteWorktree removes one working tree after judging it again; the open project is never removed.
func (s *Service) DeleteWorktree(in DeleteWorktreeInput) (DeleteWorktreeResultDTO, error) {
	if err := s.beginCall(); err != nil {
		return DeleteWorktreeResultDTO{}, err
	}
	defer s.endCall()
	if !validWorktreePath(in.Path) {
		return DeleteWorktreeResultDTO{}, ErrInvalidInput
	}
	workspace, err := s.worktreeWorkspace(in.WorkspaceID)
	if err != nil {
		return DeleteWorktreeResultDTO{}, err
	}
	current := ""
	if set, listErr := repositories.ListWorktreesWithoutUsage(s.ctx, workspace.Path); listErr == nil {
		current = currentWorktree(set, workspace.Path)
	}
	result, err := repositories.RemoveWorktree(s.ctx, workspace.Path, in.Path, repositories.RemoveOptions{
		DeleteBranch: in.DeleteBranch, AcknowledgeIgnored: in.AcknowledgeIgnored, Protected: []string{current},
	})
	switch {
	case errors.Is(err, repositories.ErrWorktreeNotFound), errors.Is(err, repositories.ErrWorktreeNotDeletable),
		errors.Is(err, repositories.ErrWorktreeRemoveFailed), errors.Is(err, repositories.ErrGitUnavailable), errors.Is(err, repositories.ErrNotRepository):
		return DeleteWorktreeResultDTO{}, err
	case err != nil:
		return DeleteWorktreeResultDTO{}, safe("delete worktree", err)
	}
	return DeleteWorktreeResultDTO{Path: result.Path, Branch: result.Branch, Removed: result.Removed, BranchDeleted: result.BranchDeleted, List: worktreeListDTO(result.Remaining, workspace.Path)}, nil
}

// PruneWorktrees forgets entries whose folder no longer exists and returns the refreshed list.
func (s *Service) PruneWorktrees(workspaceID string) (WorktreeListDTO, error) {
	if err := s.beginCall(); err != nil {
		return WorktreeListDTO{}, err
	}
	defer s.endCall()
	workspace, err := s.worktreeWorkspace(workspaceID)
	if err != nil {
		return WorktreeListDTO{}, err
	}
	if err := repositories.PruneWorktrees(s.ctx, workspace.Path); err != nil {
		if errors.Is(err, repositories.ErrGitUnavailable) || errors.Is(err, repositories.ErrNotRepository) {
			return WorktreeListDTO{}, err
		}
		return WorktreeListDTO{}, safe("prune worktrees", err)
	}
	return s.listWorktrees(workspace)
}

// PrepareWorktreeSave keeps a copy of everything the worktree holds, then returns what the
// assistant needs to commit it and merge it into the base branch. Deleting stays a separate,
// re-checked step, so the assistant's work is verified rather than trusted.
func (s *Service) PrepareWorktreeSave(in PrepareWorktreeSaveInput) (WorktreeSavePlanDTO, error) {
	if err := s.beginCall(); err != nil {
		return WorktreeSavePlanDTO{}, err
	}
	defer s.endCall()
	if !validWorktreePath(in.Path) {
		return WorktreeSavePlanDTO{}, ErrInvalidInput
	}
	workspace, err := s.worktreeWorkspace(in.WorkspaceID)
	if err != nil {
		return WorktreeSavePlanDTO{}, err
	}
	set, err := repositories.ListWorktreesWithoutUsage(s.ctx, workspace.Path)
	if err != nil {
		if errors.Is(err, repositories.ErrGitUnavailable) || errors.Is(err, repositories.ErrNotRepository) {
			return WorktreeSavePlanDTO{}, err
		}
		return WorktreeSavePlanDTO{}, safe("list worktrees", err)
	}
	item, found := set.Find(in.Path)
	if !found {
		return WorktreeSavePlanDTO{}, repositories.ErrWorktreeNotFound
	}
	if blockers := item.SaveBlockers(set); len(blockers) > 0 {
		return WorktreeSavePlanDTO{}, &repositories.SaveBlockedError{Blockers: blockers}
	}
	saved, err := repositories.SaveWorktreePending(s.ctx, workspace.Path, item.Path)
	if err != nil {
		var blocked *repositories.SaveBlockedError
		if errors.Is(err, repositories.ErrWorktreeNotFound) || errors.As(err, &blocked) || errors.Is(err, repositories.ErrGitUnavailable) {
			return WorktreeSavePlanDTO{}, err
		}
		return WorktreeSavePlanDTO{}, safe("keep a copy of the pending work", err)
	}
	root, err := s.OpenWorkspace(set.Root)
	if err != nil {
		return WorktreeSavePlanDTO{}, err
	}
	label := item.Branch
	if label == "" {
		label = "HEAD destacado " + shortCommit(item.Head)
	}
	if item.IsMain {
		return WorktreeSavePlanDTO{
			Workspace: root, Path: item.Path, Branch: item.Branch, Base: set.Base,
			SnapshotRef: saved.Ref, SnapshotCommit: saved.Commit, HadPending: saved.HadChange,
			Reason: "Commitar as alterações pendentes de " + label,
			Prompt: mainWorktreeSavePrompt(item, set.Base, saved),
		}, nil
	}
	return WorktreeSavePlanDTO{
		Workspace: root, Path: item.Path, Branch: item.Branch, Base: set.Base,
		SnapshotRef: saved.Ref, SnapshotCommit: saved.Commit, HadPending: saved.HadChange,
		Reason: "Salvar e mesclar o worktree " + label,
		Prompt: worktreeSavePrompt(set.Root, item, set.Base, saved),
	}, nil
}

func shortCommit(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func worktreeSavePrompt(root string, item repositories.Worktree, base string, saved repositories.SavedWorktree) string {
	branch := item.Branch
	where := fmt.Sprintf("branch %q", branch)
	if branch == "" {
		where = "HEAD destacado em " + shortCommit(item.Head)
	}
	var b strings.Builder
	b.WriteString("Salve o trabalho pendente de um worktree Git e mescle-o na branch base. Faça só isso.\n\n")
	b.WriteString("Contexto\n")
	fmt.Fprintf(&b, "- Worktree principal (branch base %q): %s\n", base, root)
	fmt.Fprintf(&b, "- Worktree da tarefa (%s): %s\n", where, item.Path)
	if saved.Ref != "" {
		fmt.Fprintf(&b, "- O Harflex já guardou uma cópia de segurança do que estava pendente em %s. Não apague essa referência.\n", saved.Ref)
	} else {
		b.WriteString("- Não havia alterações pendentes no worktree; falta apenas mesclar os commits da branch.\n")
	}
	fmt.Fprintf(&b, "- Alterações pendentes: %d arquivo(s); commits na branch que ainda não estão em %q: %d.\n\n", item.Changes.Total, base, item.Ahead)
	b.WriteString("Passos\n")
	fmt.Fprintf(&b, "1. Veja o estado com `git -C %q status` e `git -C %q diff`, e o histórico com `git -C %q log --oneline -10`.\n", item.Path, item.Path, item.Path)
	if branch == "" {
		fmt.Fprintf(&b, "2. O HEAD está destacado: crie uma branch antes de commitar (`git -C %q switch -c worktree/%s`).\n", item.Path, shortCommit(item.Head))
	} else {
		b.WriteString("2. Se houver alterações pendentes, commite-as nesta branch em commits coerentes, com mensagens no estilo do histórico.\n")
	}
	b.WriteString("   Não commite segredos (.env, chaves, tokens) nem arquivos gerados (node_modules, dist, bin, test-results). Se algo assim aparecer fora do .gitignore, pare e me avise.\n")
	target := branch
	if target == "" {
		target = fmt.Sprintf("worktree/%s", shortCommit(item.Head))
	}
	fmt.Fprintf(&b, "3. A partir do worktree principal, mescle a branch: `git -C %q merge --no-ff %q`. Se houver conflitos, resolva-os com cuidado, rode os testes relevantes e conclua o merge; se não tiver certeza, pare e descreva o conflito.\n", root, target)
	fmt.Fprintf(&b, "4. Confirme que `git -C %q status` e `git -C %q status` estão limpos e que `git -C %q branch --contains %q` lista %q.\n\n", root, item.Path, root, target, base)
	b.WriteString("Regras\n")
	b.WriteString("- Não use --force, reset --hard, clean, checkout -- ., stash drop nem rebase.\n")
	b.WriteString("- Não apague o worktree nem a branch: o Harflex faz isso depois de conferir que tudo foi salvo e mesclado.\n")
	b.WriteString("- Se um comando falhar, pare e explique o que aconteceu em vez de insistir.\n")
	b.WriteString("- Ao terminar, responda com o hash do commit de merge e a lista dos commits criados.\n")
	return b.String()
}

// mainWorktreeSavePrompt asks for the main worktree's pending changes to be committed on the base branch, and
// nothing else: no merge, no branch, no cleanup. A clean base is what lets the other worktrees be merged.
func mainWorktreeSavePrompt(item repositories.Worktree, base string, saved repositories.SavedWorktree) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Commite as alterações pendentes do worktree principal na branch %q. Faça só isso.\n\n", base)
	b.WriteString("Contexto\n")
	fmt.Fprintf(&b, "- Worktree principal (branch %q): %s\n", base, item.Path)
	if saved.Ref != "" {
		fmt.Fprintf(&b, "- O Harflex já guardou uma cópia de segurança do que estava pendente em %s. Não apague essa referência.\n", saved.Ref)
	}
	fmt.Fprintf(&b, "- Alterações pendentes: %d arquivo(s). Os outros worktrees só podem ser mesclados aqui depois que este estiver limpo.\n\n", item.Changes.Total)
	b.WriteString("Passos\n")
	fmt.Fprintf(&b, "1. Veja o estado com `git -C %q status`, `git -C %q diff` e `git -C %q log --oneline -10`.\n", item.Path, item.Path, item.Path)
	b.WriteString("2. Agrupe as alterações em commits coerentes, com mensagens no estilo do histórico.\n")
	b.WriteString("   Não commite segredos (.env, chaves, tokens) nem arquivos gerados (node_modules, dist, bin, test-results). Se algo assim aparecer fora do .gitignore, pare e me avise; se uma alteração parecer acidental ou incompleta, pergunte antes de commitá-la.\n")
	fmt.Fprintf(&b, "3. Confirme que `git -C %q status` está limpo.\n\n", item.Path)
	b.WriteString("Regras\n")
	b.WriteString("- Não mude de branch, não mescle, não crie nem apague branches ou worktrees.\n")
	b.WriteString("- Não use --force, reset --hard, clean, checkout -- ., stash drop nem rebase.\n")
	b.WriteString("- Se um comando falhar, pare e explique o que aconteceu em vez de insistir.\n")
	b.WriteString("- Ao terminar, responda com a lista dos commits criados.\n")
	return b.String()
}

// WorktreeGroupDTO is the worktrees of one repository, reached from the projects that live in it. Actions on
// these worktrees go through WorkspaceID, the project that stands for the repository.
type WorktreeGroupDTO struct {
	WorkspaceID string          `json:"workspaceId"`
	Name        string          `json:"name"`
	Path        string          `json:"path"`
	Projects    []string        `json:"projects"`
	List        WorktreeListDTO `json:"list"`
	Error       string          `json:"error,omitempty"`
}

// ListAllWorktrees judges the worktrees of every repository among the active projects, one group per repository.
// Archived projects and folders that moved are left out; a repository that cannot be read says so in its group.
func (s *Service) ListAllWorktrees() ([]WorktreeGroupDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	workspaces, err := s.store.ListWorkspaces(s.ctx)
	if err != nil {
		return nil, safe("list workspaces", err)
	}
	active := make([]catalog.Workspace, 0, len(workspaces))
	folders := make([]string, 0, len(workspaces))
	for _, workspace := range workspaces {
		if info, statErr := os.Stat(workspace.Path); workspace.Archived || statErr != nil || !info.IsDir() {
			continue
		}
		active, folders = append(active, workspace), append(folders, workspace.Path)
	}
	repositoriesFound, err := repositories.ListRepositoryWorktrees(s.ctx, folders)
	if err != nil {
		if errors.Is(err, repositories.ErrGitUnavailable) {
			return nil, err
		}
		return nil, safe("list worktrees", err)
	}
	groups := make([]WorktreeGroupDTO, 0, len(repositoriesFound))
	for _, repository := range repositoriesFound {
		// The project opened at the repository's own folder stands for it; otherwise the first one found.
		representative := active[repository.Folders[0]]
		projects := make([]string, 0, len(repository.Folders))
		for _, index := range repository.Folders {
			workspace := active[index]
			projects = append(projects, filepath.Base(workspace.Path))
			if canonical, err := filepath.EvalSymlinks(workspace.Path); err == nil && canonical == repository.Set.Root {
				representative = workspace
			}
		}
		group := WorktreeGroupDTO{WorkspaceID: representative.ID, Name: filepath.Base(representative.Path), Path: representative.Path, Projects: projects}
		if repository.Err != nil {
			group.Error = "Não foi possível ler os worktrees deste projeto."
			group.List = WorktreeListDTO{Items: []WorktreeDTO{}}
		} else {
			group.Name = filepath.Base(repository.Set.Root)
			group.List = worktreeListDTO(repository.Set, representative.Path)
		}
		groups = append(groups, group)
	}
	sort.SliceStable(groups, func(i, j int) bool { return strings.ToLower(groups[i].Name) < strings.ToLower(groups[j].Name) })
	return groups, nil
}
