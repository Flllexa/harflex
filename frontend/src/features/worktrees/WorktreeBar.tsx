import { useState } from 'react'
import { CheckCircle2, GitBranch, ShieldAlert, Sparkles, X } from 'lucide-react'
import type { Backend, BackendOption, WorktreeSavePlan } from '../../lib/backend'
import { useT } from '../../i18n'
import { Rich, SaveWithAIDialog, type SaveWithAIOptions } from './WorktreeDialogs'
import { blockerText, deletableText, plural, worktreeFacts, worktreeTitle } from './worktreeText'
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
  const t = useT()
  const { list } = worktrees
  const [saving, setSaving] = useState(false)
  const [refusal, setRefusal] = useState('')
  if (!workspaceId || !list?.isRepository) return null
  const current = list.items.find(item => item.isCurrent) ?? list.items.find(item => item.isMain)
  const held = current ? current.blockers.filter(blocker => blocker.code !== 'current_project' && blocker.code !== 'main_worktree') : []
  const others = list.items.filter(item => !item.isMain)
  const clear = others.filter(item => item.canDelete).length

  return <div className="worktree-bar-wrap">
    {current && <div className="worktree-bar" role="region" aria-label={t('Worktree da tarefa')}>
      <div className="worktree-bar-state">
        <GitBranch aria-hidden="true" />
        <strong title={worktreeTitle(current)}>{worktreeTitle(current)}</strong>
        <span className="status-chip">{current.isMain ? t('Principal') : t('Worktree')}</span>
        <span className="muted worktree-bar-facts">{worktreeFacts(current, list.base).join(' · ')}</span>
      </div>
      {current.isMain
        ? <span className="worktree-bar-verdict muted">{others.length === 0 ? t('Sem outros worktrees.') : t('{others} · {deletable}', { others: plural(others.length, t('outro worktree'), t('outros worktrees')), deletable: deletableText(clear) })}</span>
        : held.length === 0
          ? <span className="worktree-bar-verdict is-clear"><CheckCircle2 aria-hidden="true" /><Rich text={t('Tudo salvo e mesclado em {base}: pode ser excluído ao sair deste projeto.')} values={{ base: list.base }} /></span>
          : <span className="worktree-bar-verdict is-held"><ShieldAlert aria-hidden="true" />{held.length > 1
            ? t('Não pode ser excluído: {reason} +{more}', { reason: blockerText(held[0], list.base), more: plural(held.length - 1, t('motivo'), t('motivos')) })
            : t('Não pode ser excluído: {reason}', { reason: blockerText(held[0], list.base) })}</span>}
      <div className="worktree-bar-actions">
        {current.canSaveWithAi && <button type="button" className="touch-target secondary-button" onClick={() => { setRefusal(''); setSaving(true) }}><Sparkles aria-hidden="true" />{t('Salvar com a IA…')}</button>}
        {showManage && <button type="button" className="touch-target secondary-button" onClick={onOpenWorktrees}>{t('Gerenciar worktrees')}</button>}
      </div>
    </div>}
    {refusal && <div className="worktree-cleanup is-attention" role="alert"><span>{refusal}</span>
      <button type="button" className="touch-target icon-button" aria-label={t('Fechar aviso')} onClick={() => setRefusal('')}><X aria-hidden="true" /></button></div>}
    {saving && current && <SaveWithAIDialog backend={backend} workspaceId={workspaceId} item={current} base={list.base} root={list.root} backends={backends} defaultBackendId={defaultBackendId}
      onClose={() => setSaving(false)} onSettings={() => { setSaving(false); onSettings() }} onStart={onSaveWithAI}
      onStale={message => { setSaving(false); setRefusal(message); void worktrees.refresh() }} />}
  </div>
}
