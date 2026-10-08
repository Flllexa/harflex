import { useEffect, useRef, useState, type FormEvent } from 'react'
import { FolderOpen, Plus, RefreshCw, ArrowUpRight, Archive, ArchiveRestore } from 'lucide-react'
import { errorMessage, type Backend, type WorkspaceSummary } from '../../lib/backend'
import { ProjectMemoryLine } from './ProjectMemory'

type Props = {
  backend: Backend
  currentWorkspaceId?: string
  busy: boolean
  onOpen: (path: string) => Promise<void>
}

const basename = (path: string) => path.split(/[\\/]/).filter(Boolean).pop() ?? path
const profileLabel = (profile: string) => profile === 'ask' ? 'perguntar' : profile === 'trusted_workspace' ? 'confiável' : profile === 'full_access' ? 'acesso total' : profile

export function ProjectsPage({ backend, currentWorkspaceId, busy, onOpen }: Props) {
  const [items, setItems] = useState<WorkspaceSummary[]>([])
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [path, setPath] = useState('')
  const [opening, setOpening] = useState(false)
  const [error, setError] = useState<string>()
  const [showArchived, setShowArchived] = useState(false)
  const [archiving, setArchiving] = useState<string>()
  const generation = useRef(0)

  async function refresh() {
    const current = ++generation.current
    setState('loading')
    try {
      const result = await backend.listWorkspaces()
      if (current === generation.current) { setItems(result); setState('ready') }
    } catch {
      if (current === generation.current) setState('error')
    }
  }

  useEffect(() => {
    void refresh()
    return () => { generation.current++ }
  }, [backend])

  async function open(candidate: string) {
    if (!candidate.trim() || busy || opening) return
    setOpening(true)
    setError(undefined)
    try { await onOpen(candidate.trim()) }
    catch (failure) { setError(errorMessage(failure)) }
    finally { setOpening(false) }
  }

  async function chooseDirectory() {
    try {
      const selected = await backend.pickDirectory()
      if (selected) await open(selected)
    } catch (failure) { setError(errorMessage(failure)) }
  }

  // Archiving keeps everything the project owns; it only moves the card to the archived list.
  async function setArchived(item: WorkspaceSummary, archived: boolean) {
    if (archiving) return
    setArchiving(item.id)
    setError(undefined)
    try {
      const updated = await backend.setWorkspaceArchived(item.id, archived)
      setItems(current => current.map(candidate => candidate.id === updated.id ? updated : candidate))
    } catch (failure) { setError(errorMessage(failure)) }
    finally { setArchiving(undefined) }
  }

  const active = items.filter(item => !item.archived)
  const archivedItems = items.filter(item => item.archived)

  function submit(event: FormEvent) {
    event.preventDefault()
    void open(path)
  }

  return <div className="projects-page">
    <div className="destination-heading">
      <div><h2>Projetos locais</h2><p className="muted">Pastas autorizadas para trabalho com agentes.</p></div>
      <button type="button" className="touch-target secondary-button" onClick={() => void refresh()} disabled={state === 'loading'}><RefreshCw aria-hidden="true" />Atualizar</button>
    </div>
    <form className="project-open-form" onSubmit={submit}>
      <div className="project-open-copy"><Plus aria-hidden="true" /><div><strong>Adicionar pasta</strong><span className="muted">Escolha um diretório existente para criar ou continuar um trabalho.</span></div></div>
      <label className="field">Caminho da pasta<input className="mono" value={path} onChange={event => setPath(event.target.value)} autoComplete="off" spellCheck={false} /></label>
      <div className="project-open-actions">
        <button type="button" className="touch-target secondary-button" onClick={() => void chooseDirectory()} disabled={busy || opening}><FolderOpen aria-hidden="true" />Escolher pasta</button>
        <button type="submit" className="touch-target primary-button" disabled={busy || opening || !path.trim()}>Abrir projeto</button>
      </div>
    </form>
    {busy && <p className="project-warning" role="status">Cancele ou conclua a execução atual antes de trocar de projeto.</p>}
    {error && <p className="form-error" role="alert">{error}</p>}
    <section className="project-catalog" aria-labelledby="saved-projects-heading">
      <div className="destination-heading"><div><h3 id="saved-projects-heading">Pastas recentes</h3><p className="muted">Salvas somente nesta máquina.</p></div><span className="muted mono">{active.length} {active.length === 1 ? 'pasta' : 'pastas'}</span></div>
      {state === 'loading' && <p className="muted" role="status">Carregando projetos…</p>}
      {state === 'error' && <div className="inline-error" role="alert"><p>Não foi possível carregar os projetos.</p><button type="button" className="touch-target secondary-button" onClick={() => void refresh()}>Tentar novamente</button></div>}
      {state === 'ready' && (active.length === 0
        ? <div className="catalog-empty"><FolderOpen aria-hidden="true" /><strong>{archivedItems.length ? 'Nenhuma pasta ativa' : 'Nenhuma pasta adicionada'}</strong><span className="muted">{archivedItems.length ? 'Reative uma pasta arquivada abaixo ou escolha outra acima.' : 'Escolha uma pasta acima para começar.'}</span></div>
        : <ul className="project-grid">{active.map(item => <li key={item.id} className="project-card">
          <div className="project-card-top"><span className="project-card-icon"><FolderOpen aria-hidden="true" /></span><span className={`status-chip${item.available ? ' status-ready' : ' status-unavailable'}`}>{item.available ? item.id === currentWorkspaceId ? 'Aberto' : 'Disponível' : 'Indisponível'}</span></div>
          <strong className="project-card-name">{basename(item.path)}</strong><span className="project-card-path mono">{item.path}</span>
          <div className="project-card-bottom"><span className="muted">Permissões: {profileLabel(item.profile)}</span>
            <div className="project-card-actions">
              <button type="button" className="touch-target secondary-button" disabled={archiving !== undefined} onClick={() => void setArchived(item, true)} aria-label={`Arquivar ${basename(item.path)}`}><Archive aria-hidden="true" />Arquivar</button>
              <button type="button" className="touch-target secondary-button" disabled={!item.available || busy || opening} onClick={() => void open(item.path)} aria-label={`Abrir ${basename(item.path)}`}><ArrowUpRight aria-hidden="true" />Abrir</button>
            </div>
          </div>
          {!item.available && <p className="project-card-note">A pasta mudou de lugar. Adicione o novo caminho acima.</p>}
          {item.available && <ProjectMemoryLine backend={backend} workspaceId={item.id} name={basename(item.path)} />}
        </li>)}</ul>)}
      {state === 'ready' && archivedItems.length > 0 && <div className="project-archived-toggle">
        <button type="button" className="touch-target secondary-button" aria-expanded={showArchived} aria-controls="archived-projects" onClick={() => setShowArchived(current => !current)}><Archive aria-hidden="true" />{showArchived ? 'Ocultar arquivados' : 'Mostrar arquivados'} ({archivedItems.length})</button>
      </div>}
      {state === 'ready' && showArchived && archivedItems.length > 0 && <section id="archived-projects" className="project-catalog" aria-labelledby="archived-projects-heading">
        <div><h3 id="archived-projects-heading">Arquivados</h3><p className="muted">Conversas, pipelines e configurações continuam guardados. Reative ou adicione a mesma pasta de novo para trazer de volta.</p></div>
        <ul className="project-grid">{archivedItems.map(item => <li key={item.id} className="project-card project-card-archived">
          <div className="project-card-top"><span className="project-card-icon"><Archive aria-hidden="true" /></span><span className="status-chip">Arquivado</span></div>
          <strong className="project-card-name">{basename(item.path)}</strong><span className="project-card-path mono">{item.path}</span>
          <div className="project-card-bottom"><span className="muted">Permissões: {profileLabel(item.profile)}</span>
            <button type="button" className="touch-target secondary-button" disabled={archiving !== undefined} onClick={() => void setArchived(item, false)} aria-label={`Reativar ${basename(item.path)}`}><ArchiveRestore aria-hidden="true" />Reativar</button>
          </div>
          {!item.available && <p className="project-card-note">A pasta não está mais neste caminho.</p>}
        </li>)}</ul>
      </section>}
    </section>
  </div>
}
