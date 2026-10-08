import { Activity, BookOpen, Brain, Bot, CalendarClock, Coins, Folder, FolderGit2, GitBranch, GitFork, Inbox, ListTree, LockKeyhole, MessageSquare, Network, Plus, ScrollText, Settings, Sparkles, X, type LucideIcon } from 'lucide-react'

// Grouped by how often people reach for them: the daily loop first, then the
// building blocks the agents use, then project context and housekeeping.
const navigation = [
  { label: '', items: [['Projetos', Folder], ['Conversas', MessageSquare], ['Pipelines', ListTree]] },
  { label: 'Automação', items: [['Agentes', Bot], ['Workflows', GitFork], ['Agendamentos', CalendarClock], ['Skills', Sparkles], ['MCP Servers', Network]] },
  { label: 'Contexto', items: [['Memória', Brain], ['Conhecimento', BookOpen], ['Canais', Inbox], ['Repositórios', GitBranch], ['Worktrees', FolderGit2]] },
  { label: 'Sistema', items: [['Custos e execução', Coins], ['Logs', ScrollText], ['Diagnóstico', Activity], ['Vault', LockKeyhole], ['Configurações', Settings]] },
] as const

export type Destination = (typeof navigation)[number]['items'][number][0]
type Entry = readonly [Destination, LucideIcon]
const groups: readonly { label: string; items: readonly Entry[] }[] = navigation

export const destinations: readonly Entry[] = groups.flatMap(group => group.items)

type Props = { selected: Destination; onSelect: (name: Destination) => void; onNewWork: () => void; onToggleMode: () => void; onClose: () => void }

export function Sidebar({ selected, onSelect, onNewWork, onToggleMode, onClose }: Props) {
  return <>
    <div className="sidebar-brand">
      <span className="brand-word">Harflex</span><span className="local-label">Local</span>
      <button className="touch-target icon-button sidebar-close" aria-label="Fechar navegação" onClick={onClose}><X aria-hidden="true" /></button>
    </div>
    <button className="touch-target primary-button new-work" onClick={() => onNewWork()} title="Novo trabalho">
      <Plus aria-hidden="true" /><span className="nav-label">Novo trabalho</span>
    </button>
    <nav aria-label="Navegação principal" className="sidebar-nav">
      {groups.map((group, index) => <div key={group.label || index} className="nav-group" role="group" aria-label={group.label || undefined}>
        {group.label && <span className="nav-group-label" aria-hidden="true">{group.label}</span>}
        {group.items.map(([name, Icon]) => <button key={name} className="touch-target nav-item" aria-current={selected === name ? 'page' : undefined} onClick={() => onSelect(name)} title={name}>
          <Icon aria-hidden="true" /><span className="nav-label">{name}</span>
        </button>)}
      </div>)}
    </nav>
    <div className="sidebar-footer">
      <button type="button" className="touch-target secondary-button sidebar-mode-switch" aria-label="Mudar para Casual" onClick={onToggleMode}>
        <MessageSquare aria-hidden="true" /><span className="nav-label">Modo Casual</span>
      </button>
      <span className="nav-label">Ambiente local</span><span className="muted nav-label">Dados nesta máquina</span>
    </div>
  </>
}
