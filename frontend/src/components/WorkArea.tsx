import { useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import { BarChart3, FileText, Folder, MessageSquare } from 'lucide-react'
import type { Destination } from './Sidebar'

const tabs = ['Conversa', 'Artefatos', 'Métricas'] as const
const emptyCopy = ['Nenhuma conversa iniciada', 'Nenhum artefato', 'Nenhuma métrica']
const tabIcons = [MessageSquare, FileText, BarChart3]

export type WorkAreaProps = {
  selected: Destination
  workState: 'empty' | 'loading'
  compact?:boolean
  /** Real content from the workbench; omitted in the static interface preview. */
  project?: { name: string; detail: string }
  panels?: [ReactNode, ReactNode, ReactNode]
  projectContent?: ReactNode
  settingsContent?: ReactNode
  logsContent?: ReactNode
  repositoryContent?: ReactNode
  worktreesContent?: ReactNode
  /** Shown under the project name in every destination (the task's worktree control). */
  projectExtra?: ReactNode
  /** A running or finished piece of work worth keeping in view; shown even where the project header is hidden. */
  projectNotice?: ReactNode
  pipelineContent?: ReactNode
  agentsContent?: ReactNode
  workflowsContent?: ReactNode
  schedulesContent?: ReactNode
  skillsContent?: ReactNode
  mcpContent?: ReactNode
  diagnosticsContent?: ReactNode
  executionContent?: ReactNode
  knowledgeContent?: ReactNode
  memoryContent?: ReactNode
  vaultContent?: ReactNode
  channelsContent?: ReactNode
  footer?: [string, string]
}

const exampleProject = { name: 'api-faturas', detail: 'Projeto de exemplo' }

/** Keeps the end of a long absolute path, where the project's own folders are. */
export function shortPath(path: string): string {
  const parts = path.split(/[\\/]/).filter(Boolean)
  if (!/[\\/]/.test(path) || parts.length <= 4) return path
  return `…/${parts.slice(-3).join('/')}`
}

export function WorkArea({ selected, workState, compact=false, project = exampleProject, panels, projectContent, settingsContent, logsContent, repositoryContent, worktreesContent, projectExtra, projectNotice, pipelineContent, agentsContent, workflowsContent, schedulesContent, skillsContent, mcpContent, diagnosticsContent, executionContent, knowledgeContent, memoryContent, vaultContent, channelsContent, footer = ['Prévia local', 'Nenhuma execução ativa'] }: WorkAreaProps) {
  const [activeTab, setActiveTab] = useState(0)
  const tabRefs = useRef<(HTMLButtonElement | null)[]>([])
  const destinationContent: Partial<Record<Destination, ReactNode>> = {
    Projetos: projectContent, Configurações: settingsContent, Logs: logsContent,
    Repositórios: repositoryContent, Worktrees: worktreesContent, Pipelines: pipelineContent, Agentes: agentsContent,
    Workflows: workflowsContent, Skills: skillsContent, 'MCP Servers': mcpContent,
    Diagnóstico: diagnosticsContent, 'Custos e execução': executionContent,
    Conhecimento: knowledgeContent,
    Memória: memoryContent,
    Vault: vaultContent,
    Agendamentos: schedulesContent,
    Canais: channelsContent,
  }
  function navigateTabs(event: KeyboardEvent<HTMLButtonElement>) {
    let next: number
    if (event.key === 'ArrowRight') next = (activeTab + 1) % tabs.length
    else if (event.key === 'ArrowLeft') next = (activeTab + tabs.length - 1) % tabs.length
    else if (event.key === 'Home') next = 0
    else if (event.key === 'End') next = tabs.length - 1
    else return
    event.preventDefault()
    setActiveTab(next)
    tabRefs.current[next]?.focus()
  }
  return <main id="work-area" className={`work-area${selected === 'Conversas' ? ' is-conversation' : ''}`} aria-label="Área de trabalho" tabIndex={-1}>
    {compact ? <><h1 className="visually-hidden">Conversa no projeto {project.name}</h1>{projectNotice && <div className="project-notice">{projectNotice}</div>}</> : <section className="project-header" aria-labelledby="project-title">
      <div className="project-title"><Folder aria-hidden="true" /><h1 id="project-title">{project.name}</h1><span className="project-path mono-path muted" title={project.detail}>{shortPath(project.detail)}</span></div>
      {projectExtra}
      {projectNotice}
    </section>}
    {selected === 'Conversas' ? <>
      {!compact && <div className="workspace-tabs" role="tablist" aria-label="Visualização do trabalho">
        {tabs.map((tab, index) => {
          const Icon = tabIcons[index]
          return <button key={tab} id={`tab-${index}`} ref={element => { tabRefs.current[index] = element }} className="touch-target workspace-tab" role="tab" aria-selected={activeTab === index} aria-controls="work-panel" tabIndex={activeTab === index ? 0 : -1} onClick={() => setActiveTab(index)} onKeyDown={navigateTabs}><Icon aria-hidden="true" />{tab}</button>
        })}
      </div>}
      <section id="work-panel" className={`work-panel${panels ? ' has-content' : ''}`} role={compact ? undefined : 'tabpanel'} aria-label={compact ? 'Conversa' : undefined} aria-labelledby={compact ? undefined : `tab-${activeTab}`} tabIndex={0} aria-busy={workState === 'loading'}>
        {panels && workState !== 'loading' ? panels[compact ? 0 : activeTab] : workState === 'loading' ? <div className="empty-state" role="status"><p>Carregando área de trabalho</p><div className="loading-line" aria-hidden="true" /></div> : <div className="empty-state"><p>{emptyCopy[activeTab]}</p><span className="muted">{activeTab === 0 ? 'O trabalho começa com uma conversa.' : activeTab === 1 ? 'Documentos e evidências aparecerão aqui.' : 'Os dados aparecerão após uma execução.'}</span></div>}
      </section>
    </> : destinationContent[selected] ?? <section className="destination-failure" role="alert" aria-label={selected}><h2>Falha ao abrir {selected}</h2><p className="muted">Esta área não foi carregada. Reinicie o aplicativo e tente novamente.</p></section>}
    <footer className="workspace-footer"><span>{footer[0]}</span><span>{footer[1]}</span></footer>
  </main>
}
