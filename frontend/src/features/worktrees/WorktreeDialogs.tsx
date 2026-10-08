import { useMemo, useRef, useState } from 'react'
import { Modal } from '../../components/Modal'
import { IonPicker } from '../../components/IonPicker'
import { errorCode, errorMessage, type Backend, type BackendOption, type DeleteWorktreeResult, type Worktree, type WorktreeSavePlan } from '../../lib/backend'
import { ignoredSummary, shortWorktreePath, worktreeTitle } from './worktreeText'
import './worktrees.css'

export type SaveWithAIOptions = { backendId: string; deleteWhenSaved: boolean; deleteBranch: boolean; acknowledgeIgnored: boolean }

/** The backend refused because the worktree is no longer what the list showed: the caller reads it again and explains. */
const staleCodes = new Set(['worktree_not_deletable', 'worktree_not_found', 'worktree_save_blocked'])
export const isStaleWorktree = (failure: unknown) => staleCodes.has(errorCode(failure))

type DeleteProps = { backend: Backend; workspaceId: string; item: Worktree; base: string; onClose: () => void; onDeleted: (result: DeleteWorktreeResult) => void; onStale: (message: string) => void }

export function DeleteWorktreeDialog({ backend, workspaceId, item, base, onClose, onDeleted, onStale }: DeleteProps) {
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

  return <Modal title={`Excluir o worktree ${worktreeTitle(item)}?`} titleId="worktree-delete-title" initialFocus={cancel} onClose={() => { if (!pending) onClose() }}>
    <p className="muted dialog-copy">Tudo o que este worktree tinha foi salvo e mesclado em <strong>{base}</strong>. A pasta abaixo será removida do disco.</p>
    <p className="mono-path worktree-dialog-path" title={item.path}>{shortWorktreePath(item.path)}</p>
    {ignored > 0 && <div className="worktree-ignored" role="alert">
      <strong>Estes itens não estão no Git e serão apagados para sempre</strong>
      <p className="muted">{ignoredSummary(item)}</p>
      <label className="worktree-check"><input type="checkbox" checked={acknowledged} onChange={event => setAcknowledged(event.target.checked)} />Entendo que serão apagados.</label>
    </div>}
    {item.branch && <label className="worktree-check"><input type="checkbox" checked={deleteBranch} onChange={event => setDeleteBranch(event.target.checked)} />Apagar também a branch <strong>{item.branch}</strong> (já mesclada em {base}).</label>}
    {error && <p className="form-error" role="alert">{error}</p>}
    <div className="dialog-actions">
      <button ref={cancel} type="button" className="touch-target secondary-button" disabled={pending} onClick={onClose}>Cancelar</button>
      <button type="button" className="touch-target danger-button" disabled={!ready} onClick={() => void confirm()}>{pending ? 'Excluindo…' : 'Excluir worktree'}</button>
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
    ? <div className="worktree-ignored" role="alert"><strong>Nenhum provedor de IA disponível</strong><p className="muted">Configure um provedor para a IA poder trabalhar.</p><button type="button" className="touch-target secondary-button" onClick={onSettings}>Configurar provedor</button></div>
    : <IonPicker id="worktree-ai-backend" label="IA que fará o trabalho" value={backendId} onChange={setBackendId} required disabled={pending}
      options={usable.map(entry => ({ value: entry.id, label: `${entry.name} · ${entry.kind === 'api' ? 'API' : 'CLI'}` }))} />
  const actions = <>{error && <p className="form-error" role="alert">{error}</p>}
    <div className="dialog-actions">
      <button ref={cancel} type="button" className="touch-target secondary-button" disabled={pending} onClick={onClose}>Cancelar</button>
      <button type="button" className="touch-target primary-button" disabled={pending || !backendId} onClick={() => void start()}>{pending ? 'Preparando…' : 'Começar'}</button>
    </div></>

  // The main worktree has nowhere to merge into: its pending changes are committed on the base branch, which is
  // what lets the other worktrees be merged.
  if (item.isMain) return <Modal title="Commitar com a IA" titleId="worktree-save-title" initialFocus={cancel} onClose={() => { if (!pending) onClose() }}>
    <p className="muted dialog-copy">O worktree principal <strong>{label}</strong> tem {item.changed} {item.changed === 1 ? 'alteração não salva' : 'alterações não salvas'}. Enquanto ele não estiver limpo, nenhum outro worktree pode ser mesclado em <strong>{base}</strong>.</p>
    <ol className="worktree-steps">
      <li>O Harflex guarda uma cópia de segurança do que está pendente, sem mexer nos seus arquivos.</li>
      <li>A IA commita as alterações em <strong>{base}</strong>, em commits coerentes, no projeto principal <span className="mono" title={root}>{shortWorktreePath(root)}</span>, que passa a ser o projeto aberto. Ela não mescla nem apaga nada, e cada comando passa pela sua aprovação na conversa.</li>
      <li>O Harflex confere se não sobrou nada pendente. Depois disso os outros worktrees podem ser salvos e mesclados.</li>
    </ol>
    {picker}
    {actions}
  </Modal>

  return <Modal title="Salvar e mesclar com a IA" titleId="worktree-save-title" initialFocus={cancel} onClose={() => { if (!pending) onClose() }}>
    <p className="muted dialog-copy">O worktree <strong>{label}</strong> ainda tem trabalho que só existe nele. A IA vai guardá-lo na branch e levá-lo para <strong>{base}</strong>.</p>
    <ol className="worktree-steps">
      <li>O Harflex guarda uma cópia de segurança do que está pendente, sem mexer nos seus arquivos.</li>
      <li>A IA commita o que estiver pendente em <strong>{label}</strong>, em commits coerentes.</li>
      <li>A IA mescla a branch em <strong>{base}</strong>, no projeto principal <span className="mono" title={root}>{shortWorktreePath(root)}</span>, que passa a ser o projeto aberto. Cada comando passa pela sua aprovação na conversa.</li>
      <li>O Harflex confere se tudo ficou salvo e mesclado{deleteWhenSaved ? ' e então exclui o worktree' : ''}.</li>
    </ol>
    {picker}
    <label className="worktree-check"><input type="checkbox" checked={deleteWhenSaved} disabled={pending} onChange={event => setDeleteWhenSaved(event.target.checked)} />Excluir o worktree quando tudo estiver salvo e mesclado.</label>
    {deleteWhenSaved && ignored > 0 && <p className="project-warning" role="note">Isso também apaga {ignoredSummary(item)}.</p>}
    {deleteWhenSaved && item.branch && <label className="worktree-check worktree-check-nested"><input type="checkbox" checked={deleteBranch} disabled={pending} onChange={event => setDeleteBranch(event.target.checked)} />Apagar também a branch {item.branch} depois de mesclada.</label>}
    {actions}
  </Modal>
}
