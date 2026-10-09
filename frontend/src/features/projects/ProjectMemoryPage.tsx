import { useState } from 'react'
import { Boxes, Brain, FolderGit2, Layers, Pencil, RefreshCw, Server, Wrench } from 'lucide-react'
import type { Backend } from '../../lib/backend'
import { localeTag, useT } from '../../i18n'
import { DesignMarkdown } from '../pipelines/PipelineDesignDocument'
import { ProjectMemoryDialog, statusText, useProjectMemory } from './ProjectMemory'
import './projectMemory.css'

type Props = { backend: Backend; workspaceId?: string; projectName?: string; onProjects: () => void }

export type MemorySection = { title: string; body: string; items: string[] }

/** Splits the memory into its "## " sections, with the top-level list items of each. */
export function memorySections(content: string): MemorySection[] {
  const sections: MemorySection[] = []
  let current: MemorySection | undefined
  for (const line of content.split('\n')) {
    const heading = /^##\s+(.+?)\s*$/.exec(line)
    if (heading) {
      current = { title: heading[1], body: '', items: [] }
      sections.push(current)
      continue
    }
    if (!current) continue
    current.body += `${line}\n`
    const item = /^[-*]\s+(.+)$/.exec(line)
    if (item) current.items.push(item[1].trim())
  }
  return sections.map(section => ({ ...section, body: section.body.trim() }))
}

/** The names a list item starts with: `code` spans before the description, else the text before ":" or "—". */
export function itemName(item: string): string {
  const code = /^`([^`]+)`/.exec(item)
  if (code) return code[1] === '.' ? 'raiz' : code[1]
  const cut = item.split(/\s[—–-]\s|:\s/)[0]
  return cut.replace(/\*\*/g, '').trim()
}

// The titles are the headings the AI writes in the memory, so they stay in Portuguese; only the labels on screen are translated.
const tiles = [
  { title: 'Tecnologias', Icon: Wrench },
  { title: 'Domínios', Icon: Layers },
  { title: 'Repositórios', Icon: FolderGit2 },
  { title: 'Serviços', Icon: Server },
] as const

const sectionId = (title: string) => `memory-${title.toLowerCase().normalize('NFD').replace(/[^a-z0-9]+/g, '-')}`

/** The project memory of the open project, summarized: what the AI read and what the document phases will know. */
export function ProjectMemoryPage({ backend, workspaceId, projectName, onProjects }: Props) {
  const t = useT()
  const projectLabel = projectName ?? t('Projeto')
  const { memory, error, read, setMemory } = useProjectMemory(backend, workspaceId)
  const [editing, setEditing] = useState(false)
  const sections = memory?.content ? memorySections(memory.content) : []
  const find = (title: string) => sections.find(section => section.title.toLowerCase().startsWith(title.toLowerCase()))
  const overview = find('Visão geral')
  const chips = (title: string) => (find(title)?.items ?? []).map(itemName).filter(name => name && name.length <= 48)
  const reading = memory?.status === 'reading'

  return <div className="memory-page">
    <div className="destination-heading"><div><h2>{t('Memória do projeto')}</h2><p className="muted">{t('O que a IA leu da documentação de {project} e usa como contexto em Discovery, SPEC e Plan.', { project: projectLabel })}</p></div>
      {workspaceId && memory && <div className="memory-heading-actions">
        {memory.status === 'ready' && <button type="button" className="touch-target secondary-button" onClick={() => setEditing(true)}><Pencil aria-hidden="true" />{t('Editar')}</button>}
        {!reading && <button type="button" className="touch-target secondary-button" onClick={() => void read()}><RefreshCw aria-hidden="true" />{memory.status === 'ready' ? t('Ler de novo') : t('Ler o projeto com a IA')}</button>}
      </div>}</div>

    {!workspaceId && <div className="catalog-empty"><Brain aria-hidden="true" /><strong>{t('Nenhum projeto aberto')}</strong><span className="muted">{t('Abra um projeto para ver o que a IA sabe dele.')}</span><button type="button" className="touch-target secondary-button" onClick={onProjects}>{t('Ir para Projetos')}</button></div>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {workspaceId && memory && memory.status !== 'ready' && <div className="catalog-empty" role="status"><Brain aria-hidden="true" /><strong>{statusText(memory)}</strong>
      {reading && <span className="muted">{t('Isso leva menos de um minuto na maioria dos projetos. A página atualiza sozinha.')}</span>}
      {memory.status === 'failed' && memory.errorCode && <span className="muted mono">{memory.errorCode}</span>}</div>}

    {workspaceId && memory?.status === 'ready' && <>
      <p className="memory-meta muted" role="status">{statusText(memory)}{memory.modelId ? ` · ${memory.backendId} · ${memory.modelId}` : ''} · {t('atualizada em {date}', { date: new Date(memory.updatedAt).toLocaleString(localeTag()) })}
        {memory.errorCode && ` · ${t('a última leitura falhou e a memória anterior foi mantida')}`}</p>

      <ul className="memory-tiles" aria-label={t('Resumo')}>
        {tiles.map(({ title, Icon }) => {
          const section = find(title)
          return <li key={title}><a className="memory-tile" href={section ? `#${sectionId(section.title)}` : undefined} aria-disabled={!section}>
            <Icon aria-hidden="true" /><strong>{section?.items.length ?? 0}</strong><span>{t(title)}</span></a></li>
        })}
      </ul>

      {overview && <section className="memory-overview" aria-labelledby="memory-overview-title"><h3 id="memory-overview-title"><Boxes aria-hidden="true" />{t('Visão geral')}</h3><DesignMarkdown content={overview.body} /></section>}

      {(chips('Domínios').length > 0 || chips('Repositórios').length > 0) && <div className="memory-chip-groups">
        {chips('Domínios').length > 0 && <div><span className="memory-chip-label">{t('Domínios')}</span><ul className="memory-chips">{chips('Domínios').map(name => <li key={name}>{name}</li>)}</ul></div>}
        {chips('Repositórios').length > 0 && <div><span className="memory-chip-label">{t('Repositórios')}</span><ul className="memory-chips is-mono">{chips('Repositórios').map(name => <li key={name}>{name}</li>)}</ul></div>}
      </div>}

      <div className="memory-sections">
        {sections.filter(section => section !== overview).map(section => <details key={section.title} id={sectionId(section.title)} className="memory-section" open={section.items.length <= 12}>
          <summary><strong>{section.title}</strong>{section.items.length > 0 && <span className="muted">{section.items.length}</span>}</summary>
          <DesignMarkdown content={section.body} />
        </details>)}
      </div>

      {memory.sources.length > 0 && <details className="memory-section"><summary><strong>{t('Arquivos lidos')}</strong><span className="muted">{memory.sources.length}</span></summary>
        <ul className="memory-sources mono">{memory.sources.map(source => <li key={source}>{source}</li>)}</ul></details>}
    </>}

    {editing && memory && <ProjectMemoryDialog backend={backend} memory={memory} name={projectLabel} editing onClose={() => setEditing(false)} onSaved={value => { setMemory(value); setEditing(false) }} />}
  </div>
}
