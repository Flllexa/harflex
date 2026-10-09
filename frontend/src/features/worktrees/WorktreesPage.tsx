import { useCallback, useEffect, useRef, useState } from 'react'
import { CheckCircle2, FolderGit2, GitBranch, GitCommitHorizontal, Lock, RefreshCw, ShieldAlert, Sparkles, Trash2 } from 'lucide-react'
import { errorMessage, type Backend, type BackendOption, type Worktree, type WorktreeGroup, type WorktreeList, type WorktreeSavePlan } from '../../lib/backend'
import { t, useT } from '../../i18n'
import { DeleteWorktreeDialog, Rich, SaveWithAIDialog, type SaveWithAIOptions } from './WorktreeDialogs'
import { blockerText, deletableText, ignoredSummary, plural, shortWorktreePath, worktreeFacts, worktreeTitle } from './worktreeText'
import type { useWorktrees } from './useWorktrees'
import './worktrees.css'

type Props = {
  backend: Backend
  workspaceId?: string
  backends: BackendOption[]
  defaultBackendId: string
  worktrees: ReturnType<typeof useWorktrees>
  onProjects: () => void
  onSettings: () => void
  onSaveWithAI: (plan: WorktreeSavePlan, options: SaveWithAIOptions) => Promise<void>
}

/** A worktree an action is about, with the project and listing it was judged in. */
type Target = { item: Worktree; workspaceId: string; list: WorktreeList }

function Verdict({ item, base }: { item: Worktree; base: string }) {
  const t = useT()
  if (item.isMain) return <div className="worktree-verdict is-main"><FolderGit2 aria-hidden="true" /><div><strong>{t('Worktree principal')}</strong><span className="muted">{t('Não se exclui: é onde {base} está.', { base: base || t('a branch base') })}</span>
    {item.canSaveWithAi && <span className="muted">{t('Commite as {changes} para os outros worktrees poderem ser mesclados aqui.', { changes: item.changed === 1 ? t('alteração pendente') : t('{count} alterações pendentes', { count: item.changed }) })}</span>}</div></div>
  if (item.canDelete) {
    const ignored = ignoredSummary(item)
    return <div className="worktree-verdict is-clear"><CheckCircle2 aria-hidden="true" /><div><strong>{t('Pode excluir')}</strong><span className="muted">{t('Tudo está salvo e mesclado em {base}.', { base })}</span>{ignored && <span className="muted">{t('Ao excluir, apaga {items}.', { items: ignored })}</span>}</div></div>
  }
  const reasons = item.blockers.filter(blocker => blocker.code !== 'nothing_to_save')
  return <div className="worktree-verdict is-held"><ShieldAlert aria-hidden="true" /><div><strong>{t('Não pode excluir')}</strong>
    <ul className="worktree-reasons">{reasons.map(blocker => <li key={blocker.code}>{blockerText(blocker, base)}</li>)}</ul>
    {!item.canSaveWithAi && reasons.some(blocker => ['uncommitted_changes', 'unmerged_commits', 'detached_commits'].includes(blocker.code)) && item.saveBlockers.length > 0 &&
      <span className="muted">{t('A IA não pode ajudar agora: {reasons}', { reasons: item.saveBlockers.map(blocker => blockerText(blocker, base)).join(' ') })}</span>}
  </div></div>
}

type RowsProps = {
  list: WorktreeList
  label: string
  /** The working tree of the project that is open; it is never offered for deletion from another project's group. */
  openPath?: string
  busy: boolean
  pruning: boolean
  onDelete: (item: Worktree) => void
  onSave: (item: Worktree) => void
  onPrune: () => void
}

function WorktreeRows({ list, label, openPath, busy, pruning, onDelete, onSave, onPrune }: RowsProps) {
  const t = useT()
  // A worktree held back only because the main one has changes gets the way out where the person is looking.
  const mainItem = list.items.find(item => item.isMain && item.canSaveWithAi)
  // Only one with something to save (pending changes or commits the base lacks); a merged, clean one is just deletable.
  const waitsForMain = (item: Worktree) => !item.isMain && !item.canDelete && (item.changed > 0 || item.ahead > 0) && !item.canSaveWithAi && !!mainItem &&
    item.saveBlockers.length > 0 && item.saveBlockers.every(blocker => blocker.code === 'base_dirty')
  return <ul className="worktree-list" aria-label={label} aria-busy={busy}>
    {list.items.map(item => {
      const open = item.isCurrent || (!!openPath && item.path === openPath)
      return <li key={item.path} className={`worktree-row${item.isMain ? ' is-main' : item.canDelete ? ' is-clear' : ' is-held'}`}>
        <div className="worktree-main">
          <div className="worktree-title"><GitBranch aria-hidden="true" /><strong title={worktreeTitle(item)}>{worktreeTitle(item)}</strong>
            {item.isMain && <span className="status-chip">{t('Principal')}</span>}
            {open && <span className="status-chip status-ready">{t('Projeto aberto')}</span>}
            {item.locked && <span className="status-chip"><Lock aria-hidden="true" />{t('Travado')}</span>}</div>
          <span className="muted mono worktree-path" title={item.path}>{shortWorktreePath(item.path)}</span>
          <ul className="worktree-facts" aria-label={t('Estado')}>{worktreeFacts(item, list.base).map(fact => <li key={fact}>{fact}</li>)}</ul>
          {item.changedFiles.length > 0 && <details className="worktree-files"><summary>{item.changedFiles.length < item.changed
            ? t('Ver os primeiros {shown} de {total}', { shown: item.changedFiles.length, total: item.changed })
            : t('Ver {files}', { files: plural(item.changed, t('arquivo'), t('arquivos')) })}</summary>
            <ul className="mono">{item.changedFiles.map(entry => <li key={entry}>{entry}</li>)}</ul></details>}
        </div>
        <Verdict item={item} base={list.base} />
        <div className="worktree-actions">
          {item.canDelete && !open && <button type="button" className="touch-target danger-button" onClick={() => onDelete(item)}><Trash2 aria-hidden="true" />{t('Excluir…')}</button>}
          {item.canSaveWithAi && item.isMain && <button type="button" className="touch-target primary-button" onClick={() => onSave(item)}><GitCommitHorizontal aria-hidden="true" />{t('Commitar com a IA…')}</button>}
          {item.canSaveWithAi && !item.isMain && <button type="button" className="touch-target primary-button" onClick={() => onSave(item)}><Sparkles aria-hidden="true" />{t('Salvar e mesclar com a IA…')}</button>}
          {waitsForMain(item) && <button type="button" className="touch-target primary-button" onClick={() => onSave(mainItem!)}><GitCommitHorizontal aria-hidden="true" />{t('Commitar o principal com a IA…')}</button>}
          {item.missing && <button type="button" className="touch-target secondary-button" disabled={pruning} onClick={onPrune}>{pruning ? t('Limpando…') : t('Limpar registro')}</button>}
        </div>
      </li>
    })}
  </ul>
}

function summary(list: WorktreeList) {
  const linked = list.items.filter(item => !item.isMain).length
  const clear = list.items.filter(item => !item.isMain && item.canDelete).length
  return linked === 0 ? t('Só o worktree principal') : t('{linked} além do principal · {deletable}', { linked: plural(linked, t('worktree'), t('worktrees')), deletable: deletableText(clear) })
}

/** Reads every repository among the active projects while the overview is shown. */
function useAllWorktrees(backend: Backend, enabled: boolean) {
  const [groups, setGroups] = useState<WorktreeGroup[]>()
  const [state, setState] = useState<'idle' | 'loading' | 'ready' | 'error'>('idle')
  const [error, setError] = useState('')
  const ticket = useRef(0)
  const refresh = useCallback(async () => {
    const mine = ++ticket.current
    setState('loading')
    setError('')
    try {
      const next = await backend.listAllWorktrees()
      if (mine === ticket.current) { setGroups(next); setState('ready') }
    } catch (failure) {
      if (mine === ticket.current) { setError(errorMessage(failure)); setState('error') }
    }
  }, [backend])
  useEffect(() => {
    if (enabled) void refresh()
    return () => { ticket.current++ }
  }, [enabled, refresh])
  return { groups, state, error, refresh }
}

export function WorktreesPage({ backend, workspaceId, backends, defaultBackendId, worktrees, onProjects, onSettings, onSaveWithAI }: Props) {
  const t = useT()
  const { list, state, error, refresh, replace } = worktrees
  const [scope, setScope] = useState<'project' | 'all'>('project')
  const all = useAllWorktrees(backend, scope === 'all')
  const [deleting, setDeleting] = useState<Target>()
  const [saving, setSaving] = useState<Target>()
  const [notice, setNotice] = useState('')
  const [actionError, setActionError] = useState('')
  const [pruning, setPruning] = useState(false)

  async function prune(target: string) {
    if (pruning) return
    setPruning(true)
    setActionError('')
    try {
      const fresh = await backend.pruneWorktrees(target)
      if (target === workspaceId) replace(fresh)
      if (scope === 'all') void all.refresh()
      setNotice(t('Registros de pastas ausentes foram limpos.'))
    } catch (failure) { setActionError(errorMessage(failure)) }
    finally { setPruning(false) }
  }

  /** The backend judged again and disagreed with the list: say why and show what is true now. */
  function stale(message: string) {
    setDeleting(undefined); setSaving(undefined); setNotice(''); setActionError(message)
    void refresh()
    if (scope === 'all') void all.refresh()
  }

  // A read in progress keeps showing the last answer instead of blanking the page; a failed read does not.
  const ready = (state === 'ready' || state === 'loading') && list?.isRepository ? list : undefined
  const openPath = list?.currentPath || undefined

  return <div className="worktrees-page">
    <div className="destination-heading"><div><h2>{t('Worktrees')}</h2><p className="muted">{t('As pastas de trabalho dos repositórios. Só se exclui o que está salvo e mesclado; o resto pode ser salvo pela IA.')}</p></div>
      <div className="worktree-heading-actions">
        <div className="worktree-scope" role="group" aria-label={t('Quais worktrees mostrar')}>
          <button type="button" className="touch-target secondary-button" aria-pressed={scope === 'project'} onClick={() => setScope('project')}>{t('Este projeto')}</button>
          <button type="button" className="touch-target secondary-button" aria-pressed={scope === 'all'} onClick={() => setScope('all')}>{t('Todos os projetos')}</button>
        </div>
        {(scope === 'all' || workspaceId) && <button type="button" className="touch-target secondary-button" disabled={scope === 'all' ? all.state === 'loading' : state === 'loading'}
          onClick={() => { setNotice(''); void (scope === 'all' ? all.refresh() : refresh()) }}><RefreshCw aria-hidden="true" />{t('Atualizar')}</button>}
      </div></div>
    {notice && <p className="muted" role="status">{notice}</p>}
    {actionError && <p className="form-error" role="alert">{actionError}</p>}

    {scope === 'project' && <>
      {!workspaceId && <div className="catalog-empty"><FolderGit2 aria-hidden="true" /><strong>{t('Nenhum projeto aberto')}</strong><span className="muted">{t('Abra a pasta de um repositório para ver os worktrees dele, ou veja os de todos os projetos.')}</span><button type="button" className="touch-target secondary-button" onClick={onProjects}>{t('Ir para Projetos')}</button></div>}
      {workspaceId && state === 'loading' && !list && <p className="muted" role="status">{t('Lendo worktrees…')}</p>}
      {workspaceId && state === 'error' && <div className="inline-error" role="alert"><p>{error}</p><button type="button" className="touch-target secondary-button" onClick={() => void refresh()}>{t('Tentar novamente')}</button></div>}
      {workspaceId && state === 'ready' && list && !list.isRepository && <div className="catalog-empty"><FolderGit2 aria-hidden="true" /><strong>{t('Esta pasta não está dentro de um repositório Git')}</strong><span className="muted">{t('Worktrees só existem em projetos Git.')}</span><button type="button" className="touch-target secondary-button" onClick={onProjects}>{t('Ir para Projetos')}</button></div>}
      {ready && workspaceId && <>
        <p className="worktree-summary" role="status">{ready.items.length <= 1 ? t('Este repositório só tem o worktree principal.') : summary(ready)}{ready.baseKnown && <> · <Rich text={t('base {base}')} values={{ base: <strong>{ready.base}</strong> }} /></>}{state === 'loading' && <> · {t('atualizando…')}</>}</p>
        {!ready.baseKnown && <p className="project-warning" role="note">{t('Não foi possível identificar a branch base; por segurança, nenhum worktree pode ser excluído até lá.')}</p>}
        <WorktreeRows list={ready} label={t('Worktrees do repositório')} busy={state === 'loading'} pruning={pruning}
          onDelete={item => setDeleting({ item, workspaceId, list: ready })} onSave={item => setSaving({ item, workspaceId, list: ready })} onPrune={() => void prune(workspaceId)} />
      </>}
    </>}

    {scope === 'all' && <>
      {all.state === 'loading' && !all.groups && <p className="muted" role="status">{t('Lendo os worktrees de todos os projetos…')}</p>}
      {all.state === 'error' && <div className="inline-error" role="alert"><p>{all.error}</p><button type="button" className="touch-target secondary-button" onClick={() => void all.refresh()}>{t('Tentar novamente')}</button></div>}
      {all.groups && all.groups.length === 0 && <div className="catalog-empty"><FolderGit2 aria-hidden="true" /><strong>{t('Nenhum projeto Git')}</strong><span className="muted">{t('Nenhum projeto ativo está dentro de um repositório Git.')}</span><button type="button" className="touch-target secondary-button" onClick={onProjects}>{t('Ir para Projetos')}</button></div>}
      {all.groups && all.groups.length > 0 && <>
        <p className="worktree-summary" role="status">{t('{repos} · {worktrees} além dos principais', {
          repos: plural(all.groups.length, t('repositório'), t('repositórios')),
          worktrees: plural(all.groups.reduce((total, group) => total + Math.max(group.list.items.length - 1, 0), 0), t('worktree'), t('worktrees')),
        })}{all.state === 'loading' && <> · {t('atualizando…')}</>}</p>
        {all.groups.map(group => <section key={group.workspaceId} className="worktree-group" aria-labelledby={`worktree-group-${group.workspaceId}`}>
          <div className="worktree-group-heading">
            <h3 id={`worktree-group-${group.workspaceId}`}><FolderGit2 aria-hidden="true" />{group.name}</h3>
            <span className="muted mono" title={group.list.root || group.path}>{shortWorktreePath(group.list.root || group.path)}</span>
            {group.projects.length > 1 && <span className="muted">{t('Projetos: {projects}', { projects: group.projects.join(', ') })}</span>}
            {!group.error && <span className="muted">{summary(group.list)}{group.list.baseKnown && <> · <Rich text={t('base {base}')} values={{ base: <strong>{group.list.base}</strong> }} /></>}</span>}
          </div>
          {group.error ? <p className="form-error" role="alert">{group.error}</p>
            : <WorktreeRows list={group.list} label={t('Worktrees de {name}', { name: group.name })} openPath={openPath} busy={all.state === 'loading'} pruning={pruning}
              onDelete={item => setDeleting({ item, workspaceId: group.workspaceId, list: group.list })} onSave={item => setSaving({ item, workspaceId: group.workspaceId, list: group.list })}
              onPrune={() => void prune(group.workspaceId)} />}
        </section>)}
      </>}
    </>}

    {deleting && <DeleteWorktreeDialog backend={backend} workspaceId={deleting.workspaceId} item={deleting.item} base={deleting.list.base} onClose={() => setDeleting(undefined)} onStale={stale}
      onDeleted={result => {
        if (deleting.workspaceId === workspaceId) replace(result.list as WorktreeList)
        if (scope === 'all') void all.refresh()
        setDeleting(undefined)
        const name = deleting.item.branch || deleting.item.path
        setNotice(result.branchDeleted ? t('Worktree {name} excluído e a branch {branch} também.', { name, branch: result.branch ?? '' }) : t('Worktree {name} excluído.', { name }))
      }} />}
    {saving && <SaveWithAIDialog backend={backend} workspaceId={saving.workspaceId} item={saving.item} base={saving.list.base} root={saving.list.root} backends={backends} defaultBackendId={defaultBackendId}
      onClose={() => setSaving(undefined)} onSettings={() => { setSaving(undefined); onSettings() }} onStale={stale} onStart={onSaveWithAI} />}
  </div>
}
