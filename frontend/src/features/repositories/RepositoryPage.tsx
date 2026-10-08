import { useEffect, useState } from 'react'
import { FileDiff, FolderGit2, GitBranch, RefreshCw } from 'lucide-react'
import { errorMessage, type Backend, type Repository } from '../../lib/backend'

type Props = { backend: Backend; workspaceId?: string; onProjects: () => void }

function lineClass(line: string) {
  if (line.startsWith('+++') || line.startsWith('---')) return 'diff-meta'
  if (line.startsWith('+')) return 'diff-add'
  if (line.startsWith('-')) return 'diff-del'
  if (line.startsWith('@@')) return 'diff-hunk'
  return undefined
}

export function RepositoryPage({ backend, workspaceId, onProjects }: Props) {
  const [snapshot, setSnapshot] = useState<Repository>()
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [view, setView] = useState<'unstaged' | 'staged'>('unstaged')
  const [error, setError] = useState<string>()

  async function refresh() {
    if (!workspaceId) return
    setState('loading')
    setError(undefined)
    try { setSnapshot(await backend.inspectRepository(workspaceId)); setState('ready') }
    catch (failure) { setError(errorMessage(failure)); setState('error') }
  }
  useEffect(() => { void refresh() }, [backend, workspaceId])

  return <div className="repository-page">
    <div className="destination-heading"><div><h2>Repositório Git</h2><p className="muted">Estado e diferenças da raiz autorizada do projeto.</p></div>{workspaceId && <button type="button" className="touch-target secondary-button" onClick={() => void refresh()} disabled={state === 'loading'}><RefreshCw aria-hidden="true" />Atualizar</button>}</div>
    {!workspaceId && <div className="catalog-empty"><FolderGit2 aria-hidden="true" /><strong>Nenhum projeto aberto</strong><span className="muted">Escolha a raiz do repositório para inspecionar mudanças.</span><button type="button" className="touch-target primary-button" onClick={onProjects}>Abrir projetos</button></div>}
    {workspaceId && state === 'loading' && <p className="muted" role="status">Lendo estado Git…</p>}
    {workspaceId && state === 'error' && <div className="inline-error" role="alert"><p>{error}</p><button type="button" className="touch-target secondary-button" onClick={() => void refresh()}>Tentar novamente</button></div>}
    {workspaceId && state === 'ready' && snapshot && (!snapshot.isRepository
      ? <div className="catalog-empty"><FolderGit2 aria-hidden="true" /><strong>Esta pasta não é a raiz de um repositório Git</strong><span className="muted">Se o repositório estiver acima dela, abra a raiz como projeto para autorizar a inspeção.</span><button type="button" className="touch-target secondary-button" onClick={onProjects}>Escolher outra pasta</button></div>
      : <>
        <section className="repository-summary" aria-label="Estado do repositório"><div className="repository-branch"><GitBranch aria-hidden="true" /><div><strong>{snapshot.branch}</strong><span className="muted mono">{snapshot.root}</span></div></div><span className={`status-chip${snapshot.files.length ? ' status-unavailable' : ' status-ready'}`}>{snapshot.files.length ? `${snapshot.files.length} mudanças` : 'Diretório limpo'}</span></section>
        {snapshot.truncated && <p className="project-warning" role="status">A saída Git excedeu o limite de visualização. Confira o repositório antes de agir.</p>}
        <section className="repository-changes" aria-labelledby="changed-files-title"><div className="destination-heading"><div><h3 id="changed-files-title">Arquivos alterados</h3><p className="muted">Inclui arquivos preparados, não preparados e não rastreados.</p></div></div>
          {snapshot.files.length === 0 ? <p className="muted">Nenhuma mudança local.</p> : <ul className="repository-file-list">{snapshot.files.map((entry, index) => {
            const status = entry.slice(0, 2), path = entry.slice(3)
            return <li key={`${entry}-${index}`}><span className={`repo-file-status${status === '??' ? ' is-new' : ''}`}>{status}</span><span className="mono">{path}</span></li>
          })}</ul>}
        </section>
        <section className="repository-diffs" aria-label="Diferenças do repositório"><div className="segmented" role="group" aria-label="Tipo de diferença"><button type="button" className="touch-target segment" aria-pressed={view === 'unstaged'} onClick={() => setView('unstaged')}>Mudanças não preparadas</button><button type="button" className="touch-target segment" aria-pressed={view === 'staged'} onClick={() => setView('staged')}>Mudanças preparadas</button></div>
          {(view === 'staged' ? snapshot.stagedDiff : snapshot.unstagedDiff) ? <pre className="mono diff-body">{(view === 'staged' ? snapshot.stagedDiff : snapshot.unstagedDiff).split('\n').map((line, index) => <span key={index} className={lineClass(line)}>{line}{'\n'}</span>)}</pre> : <div className="catalog-empty"><FileDiff aria-hidden="true" /><strong>Nenhuma diferença nesta área</strong><span className="muted">Arquivos não rastreados aparecem na lista, mas ainda não têm diff Git.</span></div>}
        </section>
      </>)}
  </div>
}
