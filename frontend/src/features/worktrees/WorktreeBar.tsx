import { useState } from 'react'
import { CheckCircle2, GitBranch, ShieldAlert, Sparkles, X } from 'lucide-react'
import type { Backend, BackendOption, WorktreeSavePlan } from '../../lib/backend'
import { SaveWithAIDialog, type SaveWithAIOptions } from './WorktreeDialogs'
import { blockerText, plural, worktreeFacts, worktreeTitle } from './worktreeText'
import type { useWorktrees } from './useWorktrees'
import './worktrees.css'

type Props = {
  backend: Backend
  workspaceId?: string
  worktrees: ReturnType<typeof useWorktrees>
  backends: BackendOption[]
  defaultBackendId: string
  showManage: boolean
  onOpenWorktrees: () => void
  onSettings: () => void
  onSaveWithAI: (plan: WorktreeSavePlan, options: SaveWithAIOptions) => Promise<void>
}

/** The task's own view of its worktree: where it stands, whether it is safe to delete, and the way to save it. */
export function WorktreeBar({ backend, workspaceId, worktrees, backends, defaultBackendId, showManage, onOpenWorktrees, onSettings, onSaveWithAI }: Props) {
  const { list } = worktrees
  const [saving, setSaving] = useState(false)
  const [refusal, setRefusal] = useState('')
  if (!workspaceId || !list?.isRepository) return null
  const current = list.items.find(item => item.isCurrent) ?? list.items.find(item => item.isMain)
  const held = current ? current.blockers.filter(blocker => blocker.code !== 'current_project' && blocker.code !== 'main_worktree') : []
  const others = list.items.filter(item => !item.isMain)
  const clear = others.filter(item => item.canDelete).length

  return <div className="worktree-bar-wrap">
    {current && <div className="worktree-bar" role="region" aria-label="Worktree da tarefa">
      <div className="worktree-bar-state">
        <GitBranch aria-hidden="true" />
        <strong title={worktreeTitle(current)}>{worktreeTitle(current)}</strong>
        <span className="status-chip">{current.isMain ? 'Principal' : 'Worktree'}</span>
        <span className="muted worktree-bar-facts">{worktreeFacts(current, list.base).join(' · ')}</span>
      </div>
      {current.isMain
        ? <span className="worktree-bar-verdict muted">{others.length === 0 ? 'Sem outros worktrees.' : `${plural(others.length, 'outro worktree', 'outros worktrees')} · ${clear} ${clear === 1 ? 'pode' : 'podem'} ser ${clear === 1 ? 'excluído' : 'excluídos'}`}</span>
        : held.length === 0
          ? <span className="worktree-bar-verdict is-clear"><CheckCircle2 aria-hidden="true" />Tudo salvo e mesclado em {list.base}: pode ser excluído ao sair deste projeto.</span>
          : <span className="worktree-bar-verdict is-held"><ShieldAlert aria-hidden="true" />Não pode ser excluído: {blockerText(held[0], list.base)}{held.length > 1 ? ` +${plural(held.length - 1, 'motivo', 'motivos')}` : ''}</span>}
      <div className="worktree-bar-actions">
        {current.canSaveWithAi && <button type="button" className="touch-target secondary-button" onClick={() => { setRefusal(''); setSaving(true) }}><Sparkles aria-hidden="true" />Salvar com a IA…</button>}
        {showManage && <button type="button" className="touch-target secondary-button" onClick={onOpenWorktrees}>Gerenciar worktrees</button>}
      </div>
    </div>}
    {refusal && <div className="worktree-cleanup is-attention" role="alert"><span>{refusal}</span>
      <button type="button" className="touch-target icon-button" aria-label="Fechar aviso" onClick={() => setRefusal('')}><X aria-hidden="true" /></button></div>}
    {saving && current && <SaveWithAIDialog backend={backend} workspaceId={workspaceId} item={current} base={list.base} root={list.root} backends={backends} defaultBackendId={defaultBackendId}
      onClose={() => setSaving(false)} onSettings={() => { setSaving(false); onSettings() }} onStart={onSaveWithAI}
      onStale={message => { setSaving(false); setRefusal(message); void worktrees.refresh() }} />}
  </div>
}
