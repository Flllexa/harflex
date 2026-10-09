import { Fragment, useMemo, useRef, useState, type ReactNode } from 'react'
import { Modal } from '../../components/Modal'
import { IonPicker } from '../../components/IonPicker'
import { errorCode, errorMessage, type Backend, type BackendOption, type DeleteWorktreeResult, type Worktree, type WorktreeSavePlan } from '../../lib/backend'
import { useT } from '../../i18n'
import { ignoredSummary, plural, shortWorktreePath, worktreeTitle } from './worktreeText'
import './worktrees.css'

export type SaveWithAIOptions = { backendId: string; deleteWhenSaved: boolean; deleteBranch: boolean; acknowledgeIgnored: boolean }

/** A translated sentence whose marks are filled with markup (bold names, paths), so the markup never enters the translation. */
export function Rich({ text, values }: { text: string; values: Record<string, ReactNode> }) {
  return <>{text.split(/(\{\w+\})/).map((part, index) => {
    const name = part.startsWith('{') && part.endsWith('}') ? part.slice(1, -1) : ''
    return name && name in values ? <Fragment key={index}>{values[name]}</Fragment> : part
  })}</>
}

/** The backend refused because the worktree is no longer what the list showed: the caller reads it again and explains. */
const staleCodes = new Set(['worktree_not_deletable', 'worktree_not_found', 'worktree_save_blocked'])
export const isStaleWorktree = (failure: unknown) => staleCodes.has(errorCode(failure))

type DeleteProps = { backend: Backend; workspaceId: string; item: Worktree; base: string; onClose: () => void; onDeleted: (result: DeleteWorktreeResult) => void; onStale: (message: string) => void }

export function DeleteWorktreeDialog({ backend, workspaceId, item, base, onClose, onDeleted, onStale }: DeleteProps) {
  const t = useT()
  const ignored = item.ignored.length + item.ignoredMore
  const [deleteBranch, setDeleteBranch] = useState(!!item.branch && item.merged)
  const [acknowledged, setAcknowledged] = useState(false)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const cancel = useRef<HTMLButtonElement>(null)
  const ready = !pending && (ignored === 0 || acknowledged)

  async function confirm() {
    if (!ready) return
    setPending(true)
    setError('')
    try {
      onDeleted(await backend.deleteWorktree({ workspaceId, path: item.path, deleteBranch: deleteBranch && !!item.branch, acknowledgeIgnored: acknowledged }))
    } catch (failure) {
      if (isStaleWorktree(failure)) { onStale(errorMessage(failure)); return }
      setError(errorMessage(failure))
      setPending(false)
    }
  }

  return <Modal title={t('Excluir o worktree {name}?', { name: worktreeTitle(item) })} titleId="worktree-delete-title" initialFocus={cancel} onClose={() => { if (!pending) onClose() }}>
    <p className="muted dialog-copy"><Rich text={t('Tudo o que este worktree tinha foi salvo e mesclado em {base}. A pasta abaixo será removida do disco.')} values={{ base: <strong>{base}</strong> }} /></p>
    <p className="mono-path worktree-dialog-path" title={item.path}>{shortWorktreePath(item.path)}</p>
    {ignored > 0 && <div className="worktree-ignored" role="alert">
      <strong>{t('Estes itens não estão no Git e serão apagados para sempre')}</strong>
      <p className="muted">{ignoredSummary(item)}</p>
      <label className="worktree-check"><input type="checkbox" checked={acknowledged} onChange={event => setAcknowledged(event.target.checked)} />{t('Entendo que serão apagados.')}</label>
    </div>}
    {item.branch && <label className="worktree-check"><input type="checkbox" checked={deleteBranch} onChange={event => setDeleteBranch(event.target.checked)} /><Rich text={t('Apagar também a branch {branch} (já mesclada em {base}).')} values={{ branch: <strong>{item.branch}</strong>, base }} /></label>}
    {error && <p className="form-error" role="alert">{error}</p>}
    <div className="dialog-actions">
      <button ref={cancel} type="button" className="touch-target secondary-button" disabled={pending} onClick={onClose}>{t('Cancelar')}</button>
      <button type="button" className="touch-target danger-button" disabled={!ready} onClick={() => void confirm()}>{pending ? t('Excluindo…') : t('Excluir worktree')}</button>
    </div>
  </Modal>
}

type SaveProps = {
  backend: Backend; workspaceId: string; item: Worktree; base: string; root: string; backends: BackendOption[]; defaultBackendId: string
  onClose: () => void; onSettings: () => void; onStale: (message: string) => void
  onStart: (plan: WorktreeSavePlan, options: SaveWithAIOptions) => Promise<void>
}

function initialBackend(backends: BackendOption[], preferred: string): string {
  const usable = backends.filter(item => item.available)
  return usable.find(item => item.id === preferred)?.id ?? usable.find(item => item.kind === 'api')?.id ?? usable[0]?.id ?? ''
}

export function SaveWithAIDialog({ backend, workspaceId, item, base, root, backends, defaultBackendId, onClose, onSettings, onStale, onStart }: SaveProps) {
  const t = useT()
  const usable = useMemo(() => backends.filter(entry => entry.available), [backends])
  const ignored = item.ignored.length + item.ignoredMore
  const [backendId, setBackendId] = useState(() => initialBackend(backends, defaultBackendId))
  const [deleteWhenSaved, setDeleteWhenSaved] = useState(true)
  const [deleteBranch, setDeleteBranch] = useState(true)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const cancel = useRef<HTMLButtonElement>(null)
  const label = worktreeTitle(item)

  async function start() {
    if (pending || !backendId) return
    setPending(true)
    setError('')
    try {
      const plan = await backend.prepareWorktreeSave({ workspaceId, path: item.path })
      await onStart(plan, item.isMain ? { backendId, deleteWhenSaved: false, deleteBranch: false, acknowledgeIgnored: false } : { backendId, deleteWhenSaved, deleteBranch: deleteWhenSaved && deleteBranch && !!item.branch, acknowledgeIgnored: deleteWhenSaved })
      onClose()
    } catch (failure) {
      if (isStaleWorktree(failure)) { onStale(errorMessage(failure)); return }
      setError(errorMessage(failure))
      setPending(false)
    }
  }

  const picker = usable.length === 0
    ? <div className="worktree-ignored" role="alert"><strong>{t('Nenhum provedor de IA disponível')}</strong><p className="muted">{t('Configure um provedor para a IA poder trabalhar.')}</p><button type="button" className="touch-target secondary-button" onClick={onSettings}>{t('Configurar provedor')}</button></div>
    : <IonPicker id="worktree-ai-backend" label={t('IA que fará o trabalho')} value={backendId} onChange={setBackendId} required disabled={pending}
      options={usable.map(entry => ({ value: entry.id, label: `${entry.name} · ${entry.kind === 'api' ? 'API' : 'CLI'}` }))} />
  const actions = <>{error && <p className="form-error" role="alert">{error}</p>}
    <div className="dialog-actions">
      <button ref={cancel} type="button" className="touch-target secondary-button" disabled={pending} onClick={onClose}>{t('Cancelar')}</button>
      <button type="button" className="touch-target primary-button" disabled={pending || !backendId} onClick={() => void start()}>{pending ? t('Preparando…') : t('Começar')}</button>
    </div></>

  // The main worktree has nowhere to merge into: its pending changes are committed on the base branch, which is
  // what lets the other worktrees be merged.
  if (item.isMain) return <Modal title={t('Commitar com a IA')} titleId="worktree-save-title" initialFocus={cancel} onClose={() => { if (!pending) onClose() }}>
    <p className="muted dialog-copy"><Rich text={t('O worktree principal {label} tem {changes}. Enquanto ele não estiver limpo, nenhum outro worktree pode ser mesclado em {base}.')}
      values={{ label: <strong>{label}</strong>, changes: plural(item.changed, t('alteração não salva'), t('alterações não salvas')), base: <strong>{base}</strong> }} /></p>
    <ol className="worktree-steps">
      <li>{t('O Harflex guarda uma cópia de segurança do que está pendente, sem mexer nos seus arquivos.')}</li>
      <li><Rich text={t('A IA commita as alterações em {base}, em commits coerentes, no projeto principal {root}, que passa a ser o projeto aberto. Ela não mescla nem apaga nada, e cada comando passa pela sua aprovação na conversa.')}
        values={{ base: <strong>{base}</strong>, root: <span className="mono" title={root}>{shortWorktreePath(root)}</span> }} /></li>
      <li>{t('O Harflex confere se não sobrou nada pendente. Depois disso os outros worktrees podem ser salvos e mesclados.')}</li>
    </ol>
    {picker}
    {actions}
  </Modal>

  return <Modal title={t('Salvar e mesclar com a IA')} titleId="worktree-save-title" initialFocus={cancel} onClose={() => { if (!pending) onClose() }}>
    <p className="muted dialog-copy"><Rich text={t('O worktree {label} ainda tem trabalho que só existe nele. A IA vai guardá-lo na branch e levá-lo para {base}.')} values={{ label: <strong>{label}</strong>, base: <strong>{base}</strong> }} /></p>
    <ol className="worktree-steps">
      <li>{t('O Harflex guarda uma cópia de segurança do que está pendente, sem mexer nos seus arquivos.')}</li>
      <li><Rich text={t('A IA commita o que estiver pendente em {label}, em commits coerentes.')} values={{ label: <strong>{label}</strong> }} /></li>
      <li><Rich text={t('A IA mescla a branch em {base}, no projeto principal {root}, que passa a ser o projeto aberto. Cada comando passa pela sua aprovação na conversa.')}
        values={{ base: <strong>{base}</strong>, root: <span className="mono" title={root}>{shortWorktreePath(root)}</span> }} /></li>
      <li>{deleteWhenSaved ? t('O Harflex confere se tudo ficou salvo e mesclado e então exclui o worktree.') : t('O Harflex confere se tudo ficou salvo e mesclado.')}</li>
    </ol>
    {picker}
    <label className="worktree-check"><input type="checkbox" checked={deleteWhenSaved} disabled={pending} onChange={event => setDeleteWhenSaved(event.target.checked)} />{t('Excluir o worktree quando tudo estiver salvo e mesclado.')}</label>
    {deleteWhenSaved && ignored > 0 && <p className="project-warning" role="note">{t('Isso também apaga {items}.', { items: ignoredSummary(item) })}</p>}
    {deleteWhenSaved && item.branch && <label className="worktree-check worktree-check-nested"><input type="checkbox" checked={deleteBranch} disabled={pending} onChange={event => setDeleteBranch(event.target.checked)} />{t('Apagar também a branch {branch} depois de mesclada.', { branch: item.branch })}</label>}
    {actions}
  </Modal>
}
