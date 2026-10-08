import { X } from 'lucide-react'
import type { WorktreeCleanup } from './cleanup'
import './worktrees.css'

type Props = {
  cleanup: WorktreeCleanup
  showManage: boolean
  onOpenWorktrees: () => void
  onCheckCleanup: () => void
  onDismissCleanup: () => void
}

/** What became of a save the assistant was asked to do: working, checking, done, or what is still pending. */
export function WorktreeNotice({ cleanup, showManage, onOpenWorktrees, onCheckCleanup, onDismissCleanup }: Props) {
  return <div className={`worktree-cleanup is-${cleanup.phase}`} role={cleanup.phase === 'attention' ? 'alert' : 'status'} aria-label="Salvamento do worktree">
    <span>{cleanup.message}</span>
    <div className="worktree-bar-actions">
      {(cleanup.phase === 'saving' || cleanup.phase === 'attention') && <button type="button" className="touch-target secondary-button" onClick={onCheckCleanup}>Conferir agora</button>}
      {(cleanup.phase === 'saved' || cleanup.phase === 'attention') && showManage && <button type="button" className="touch-target secondary-button" onClick={onOpenWorktrees}>Abrir Worktrees</button>}
      {cleanup.phase !== 'saving' && cleanup.phase !== 'checking' && <button type="button" className="touch-target icon-button" aria-label="Fechar aviso" onClick={onDismissCleanup}><X aria-hidden="true" /></button>}
    </div>
  </div>
}
