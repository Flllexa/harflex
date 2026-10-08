/** A save the assistant is doing (or just did) for one worktree, followed until the worktree is judged again. */
export type WorktreeCleanup = {
  /** The project (the repository's main checkout) the save started in; the follow-up never looks anywhere else. */
  workspaceId: string
  path: string
  name: string
  branch: string
  base: string
  sessionId: string
  /** The main worktree is only committed, never merged or deleted: done means nothing is pending there. */
  isMain?: boolean
  deleteWhenSaved: boolean
  deleteBranch: boolean
  acknowledgeIgnored: boolean
  phase: 'saving' | 'checking' | 'deleted' | 'saved' | 'attention'
  message: string
}
