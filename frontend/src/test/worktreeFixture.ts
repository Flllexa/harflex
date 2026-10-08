import type { Backend, DeleteWorktreeInput, Worktree, WorktreeBlocker, WorktreeList, WorktreeSavePlan } from '../lib/backend'

/** The main checkout of the synthetic repository, where the open project lives unless a test says otherwise. */
export const worktreeRoot = '/synthetic/workspace/api-faturas'

export const blocker = (code: string, count = 0, detail = ''): WorktreeBlocker => ({ code, detail, count })

/** A linked worktree that is clean and merged: the backend would let it go. Override a field together with the verdict that follows from it. */
export function worktreeItem(overrides: Partial<Worktree> = {}): Worktree {
  return {
    path: '/synthetic/worktrees/exportacao', name: 'exportacao', branch: 'feature/exportacao', head: '1a2b3c4d5e6f7a8b',
    isMain: false, isCurrent: false, detached: false, locked: false, lockReason: '', missing: false, statusKnown: true,
    staged: 0, unstaged: 0, untracked: 0, conflicts: 0, changed: 0, changedFiles: [], changesTruncated: false, operation: '',
    ahead: 0, behind: 0, merged: true, ignored: [], ignoredMore: 0,
    canDelete: true, blockers: [], canSaveWithAi: false, saveBlockers: [blocker('nothing_to_save')],
    ...overrides,
  }
}

export const mainWorktree = (overrides: Partial<Worktree> = {}) => worktreeItem({
  path: worktreeRoot, name: 'api-faturas', branch: 'main', head: '9f8e7d6c5b4a3921', isMain: true, isCurrent: true,
  canDelete: false, blockers: [blocker('main_worktree')], saveBlockers: [blocker('main_worktree')], ...overrides,
})

/** Three edited files and two commits that exist only on this branch: held, but the assistant can save it. */
export const pendingWorktree = (overrides: Partial<Worktree> = {}) => worktreeItem({
  path: '/synthetic/worktrees/relatorios', name: 'relatorios', branch: 'feature/relatorios', head: '2b3c4d5e6f7a8b9c',
  staged: 1, unstaged: 1, untracked: 1, changed: 3, changedFiles: ['M  internal/report/build.go', ' M internal/report/build_test.go', '?? docs/relatorios.md'],
  ahead: 2, merged: false, canDelete: false, blockers: [blocker('uncommitted_changes', 3), blocker('unmerged_commits', 2, 'main')], canSaveWithAi: true, saveBlockers: [], ...overrides,
})

export const lockedWorktree = (overrides: Partial<Worktree> = {}) => worktreeItem({
  path: '/synthetic/worktrees/travado', name: 'travado', branch: 'feature/travado', head: '3c4d5e6f7a8b9c0d', locked: true, lockReason: 'em uso no CI',
  canDelete: false, blockers: [blocker('locked', 0, 'em uso no CI')], saveBlockers: [blocker('locked', 0, 'em uso no CI')], ...overrides,
})

/** HEAD detached with commits no branch keeps. */
export const detachedWorktree = (overrides: Partial<Worktree> = {}) => worktreeItem({
  path: '/synthetic/worktrees/solto', name: 'solto', branch: '', head: '4d5e6f7a8b9c0d1e', detached: true, ahead: 1, merged: false,
  canDelete: false, blockers: [blocker('detached_commits', 1, 'main')], canSaveWithAi: true, saveBlockers: [], ...overrides,
})

export const missingWorktree = (overrides: Partial<Worktree> = {}) => worktreeItem({
  path: '/synthetic/worktrees/sumiu', name: 'sumiu', branch: 'feature/sumiu', head: '5e6f7a8b9c0d1e2f', missing: true, statusKnown: false,
  canDelete: false, blockers: [blocker('missing_directory')], saveBlockers: [blocker('missing_directory')], ...overrides,
})

/** Clean and merged, yet a terminal and an editor have the folder open: deleting it would break them. */
export const inUseWorktree = (overrides: Partial<Worktree> = {}) => worktreeItem({
  path: '/synthetic/worktrees/aberto', name: 'aberto', branch: 'feature/aberto', head: '7a8b9c0d1e2f3a4b',
  canDelete: false, blockers: [blocker('in_use', 2, 'zsh (pid 4242), code (pid 4300)')], ...overrides,
})

/** Clean and merged, but the folder holds files Git ignores: deletable only with an explicit acknowledgement. */
export const ignoredWorktree = (overrides: Partial<Worktree> = {}) => worktreeItem({
  path: '/synthetic/worktrees/cache', name: 'cache', branch: 'feature/cache', head: '6f7a8b9c0d1e2f3a',
  ignored: ['node_modules/', '.env.local', 'dist/'], ignoredMore: 2, ...overrides,
})

export const worktreeListOf = (items: Worktree[], overrides: Partial<WorktreeList> = {}): WorktreeList => ({
  isRepository: true, root: worktreeRoot, base: 'main', baseKnown: true, currentPath: items.find(item => item.isCurrent)?.path ?? '', items, ...overrides,
})

/** What the repository looks like once the assistant committed and merged everything: clean, merged, ahead of nothing. */
export const settledWorktree = (item: Worktree): Worktree => ({
  ...item, staged: 0, unstaged: 0, untracked: 0, conflicts: 0, changed: 0, changedFiles: [], changesTruncated: false, ahead: 0, merged: true,
  canDelete: !item.locked && !item.blockers.some(entry => entry.code === 'current_project'), blockers: item.blockers.filter(entry => entry.code === 'current_project'), canSaveWithAi: false, saveBlockers: [blocker('nothing_to_save')],
})

const refused = (code: string) => Object.assign(new Error(code), { cause: { code } })

const contains = (folder: string, path: string) => path === folder || path.startsWith(`${folder.replace(/\/$/, '')}/`)

/**
 * A stateful stand-in for the Go worktree service. Deleting and pruning act on its own list, and
 * `settle` plays the part of the assistant finishing its commit and merge.
 *
 * With `followProject`, the open project decides which worktree is current, exactly as the Go service does:
 * the deepest worktree holding the project folder gets `isCurrent`, and a linked one also gets `current_project`.
 * Stored `isCurrent` flags and `current_project` blockers are ignored and recomputed on every read.
 */
export function installWorktrees(backend: Backend, initial: Worktree[] = [mainWorktree(), worktreeItem(), pendingWorktree(), lockedWorktree(), detachedWorktree(), missingWorktree(), ignoredWorktree()], options: { followProject?: boolean } = {}) {
  let items = initial
  let project = ''
  const calls = { list: 0, deleted: [] as DeleteWorktreeInput[], prepared: [] as string[], pruned: 0 }
  if (options.followProject) {
    const open = backend.openWorkspace
    backend.openWorkspace = async path => { project = path; return open(path) }
  }
  const decorate = (list: Worktree[]): Worktree[] => {
    if (!options.followProject) return list
    const holder = [...list].filter(item => contains(item.path, project)).sort((a, b) => b.path.length - a.path.length)[0]
    return list.map(item => {
      const current = item === holder
      const blockers = item.blockers.filter(entry => entry.code !== 'current_project')
      const linkedCurrent = current && !item.isMain
      return { ...item, isCurrent: current, blockers: linkedCurrent ? [...blockers, blocker('current_project')] : blockers, canDelete: item.canDelete && !linkedCurrent }
    })
  }
  const listOf = () => worktreeListOf(decorate(items))
  const find = (path: string) => decorate(items).find(item => item.path === path)
  backend.listWorktrees = async () => { calls.list++; return listOf() }
  backend.pruneWorktrees = async () => { calls.pruned++; items = items.filter(item => !item.missing); return listOf() }
  backend.deleteWorktree = async input => {
    calls.deleted.push(input)
    const item = find(input.path)
    if (!item) throw refused('worktree_not_found')
    if (!item.canDelete || (item.ignored.length + item.ignoredMore > 0 && !input.acknowledgeIgnored)) throw refused('worktree_not_deletable')
    items = items.filter(entry => entry.path !== input.path)
    return { path: item.path, branch: item.branch, removed: true, branchDeleted: input.deleteBranch && !!item.branch, list: listOf() }
  }
  backend.prepareWorktreeSave = async (input): Promise<WorktreeSavePlan> => {
    calls.prepared.push(input.path)
    const item = find(input.path)
    if (!item) throw refused('worktree_not_found')
    if (!item.canSaveWithAi) throw refused('worktree_save_blocked')
    const name = item.branch || item.name
    return {
      workspace: { id: 'workspace-1', path: worktreeRoot, profile: 'ask' }, path: item.path, branch: item.branch, base: 'main',
      snapshotRef: `refs/harflex/worktree-saves/${name}-20261001T120000Z`, snapshotCommit: 'c0ffee0123456789', hadPending: item.changed > 0,
      reason: `Salvar e mesclar o worktree ${name}`, prompt: `Commite o que está pendente em ${name} e mescle em main.`,
    }
  }
  return {
    calls,
    /** The stored worktrees, without the per-project decoration. */
    items: () => items,
    set: (next: Worktree[]) => { items = next },
    settle: (path: string) => { items = items.map(item => item.path === path ? settledWorktree(item) : item) },
  }
}
