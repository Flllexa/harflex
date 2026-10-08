import { useEffect, useRef, useState } from 'react'
import { Brain, Pencil, RefreshCw, Save, X } from 'lucide-react'
import { Modal } from '../../components/Modal'
import { errorMessage, type Backend, type ProjectMemory } from '../../lib/backend'
import { DesignMarkdown } from '../pipelines/PipelineDesignDocument'
import '../pipelines/pipelineDesign.css'

const pollMs = 3000

export function statusText(memory: ProjectMemory | undefined) {
  if (!memory) return 'Lendo…'
  switch (memory.status) {
    case 'reading': return 'A IA está lendo o projeto…'
    case 'ready': return memory.edited ? 'Memória pronta · editada por você' : `Memória pronta · ${memory.sources.length} ${memory.sources.length === 1 ? 'arquivo lido' : 'arquivos lidos'}`
    case 'needs_model': return 'Escolha uma IA para a fase Discovery (Pipelines ou Configurações) e leia o projeto.'
    case 'failed': return 'Não foi possível ler o projeto.'
    default: return 'O projeto ainda não foi lido pela IA.'
  }
}

/** Reads one project's memory and follows a background read until it ends. */
export function useProjectMemory(backend: Backend, workspaceId: string | undefined) {
  const [memory, setMemory] = useState<ProjectMemory>()
  const [error, setError] = useState('')
  const alive = useRef(true)

  useEffect(() => {
    alive.current = true
    setMemory(undefined)
    setError('')
    if (workspaceId) void backend.getProjectMemory(workspaceId).then(value => { if (alive.current) setMemory(value) }, failure => { if (alive.current) setError(errorMessage(failure)) })
    return () => { alive.current = false }
  }, [backend, workspaceId])

  // A read runs in the background; follow it until it ends.
  useEffect(() => {
    if (memory?.status !== 'reading' || !workspaceId) return
    const timer = window.setTimeout(() => {
      void backend.getProjectMemory(workspaceId).then(value => { if (alive.current) setMemory(value) }, () => undefined)
    }, pollMs)
    return () => window.clearTimeout(timer)
  }, [backend, workspaceId, memory])

  async function read() {
    if (!workspaceId) return
    setError('')
    try { setMemory(await backend.refreshProjectMemory(workspaceId)) }
    catch (failure) { setError(errorMessage(failure)) }
  }
  return { memory, error, read, setMemory }
}

/** What the AI knows about one project, on its card: state, and the way to read it again, view or edit it. */
export function ProjectMemoryLine({ backend, workspaceId, name }: { backend: Backend; workspaceId: string; name: string }) {
  const { memory, error, read, setMemory } = useProjectMemory(backend, workspaceId)
  const [viewing, setViewing] = useState(false)
  const reading = memory?.status === 'reading'
  return <div className="project-memory">
    <span className={`project-memory-state${memory?.status === 'ready' ? ' is-ready' : ''}`}><Brain aria-hidden="true" />{statusText(memory)}</span>
    {memory?.status === 'ready' && memory.errorCode && <span className="muted">A última leitura falhou; a memória anterior foi mantida.</span>}
    {error && <span className="form-error" role="alert">{error}</span>}
    <div className="project-memory-actions">
      {memory?.status === 'ready' && <button type="button" className="touch-target secondary-button" onClick={() => setViewing(true)} aria-label={`Ver a memória de ${name}`}>Ver memória</button>}
      {memory && !reading && <button type="button" className="touch-target secondary-button" onClick={() => void read()} aria-label={`${memory.status === 'ready' ? 'Ler de novo' : 'Ler o projeto'} ${name} com a IA`}>
        <RefreshCw aria-hidden="true" />{memory.status === 'ready' ? 'Ler de novo' : 'Ler o projeto com a IA'}</button>}
    </div>
    {viewing && memory && <ProjectMemoryDialog backend={backend} memory={memory} name={name} onClose={() => setViewing(false)} onSaved={setMemory} />}
  </div>
}

export function ProjectMemoryDialog({ backend, memory, name, editing = false, onClose, onSaved }: { backend: Backend; memory: ProjectMemory; name: string; editing?: boolean; onClose: () => void; onSaved: (memory: ProjectMemory) => void }) {
  const [draft, setDraft] = useState<string | undefined>(editing ? memory.content : undefined)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const close = useRef<HTMLButtonElement>(null)

  async function save() {
    if (draft === undefined || pending) return
    setPending(true)
    setError('')
    try { onSaved(await backend.saveProjectMemory(memory.workspaceId, draft)); setDraft(undefined) }
    catch (failure) { setError(errorMessage(failure)) }
    finally { setPending(false) }
  }

  return <Modal title={`Memória de ${name}`} titleId="project-memory-title" initialFocus={close} onClose={() => { if (!pending) onClose() }}>
    <p className="muted dialog-copy">É o que a IA usa como contexto ao escrever Discovery, SPEC e Plan neste projeto. Corrija o que estiver errado: sua versão vale até você pedir uma nova leitura.</p>
    {draft === undefined
      ? <div className="project-memory-content"><DesignMarkdown content={memory.content} /></div>
      : <label className="field">Texto da memória<textarea value={draft} onChange={event => setDraft(event.target.value)} rows={18} maxLength={65536} disabled={pending} /></label>}
    {memory.sources.length > 0 && <details className="project-memory-sources"><summary>{memory.sources.length} {memory.sources.length === 1 ? 'arquivo lido' : 'arquivos lidos'}{memory.modelId ? ` · ${memory.backendId} · ${memory.modelId}` : ''}</summary>
      <ul className="mono">{memory.sources.map(source => <li key={source}>{source}</li>)}</ul></details>}
    {error && <p className="form-error" role="alert">{error}</p>}
    <div className="dialog-actions">
      {draft === undefined
        ? <><button ref={close} type="button" className="touch-target secondary-button" onClick={onClose}>Fechar</button>
          <button type="button" className="touch-target primary-button" onClick={() => setDraft(memory.content)}><Pencil aria-hidden="true" />Editar</button></>
        : <><button type="button" className="touch-target secondary-button" disabled={pending} onClick={() => setDraft(undefined)}><X aria-hidden="true" />Cancelar edição</button>
          <button type="button" className="touch-target primary-button" disabled={pending || !draft.trim()} onClick={() => void save()}><Save aria-hidden="true" />{pending ? 'Salvando…' : 'Salvar memória'}</button></>}
    </div>
  </Modal>
}
