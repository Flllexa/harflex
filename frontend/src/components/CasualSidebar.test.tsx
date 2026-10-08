import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Backend, Pipeline, Session, Workspace } from '../lib/backend'
import { createFakeBackend, sessionMetadata } from '../test/fakeBackend'
import { CasualSidebar } from './CasualSidebar'

afterEach(cleanup)

const workspace: Workspace = { id: 'workspace-1', path: '/Users/dev/todo-app', profile: 'ask' }
const session: Session = {
  id: 'session-1', workspaceId: workspace.id, backendId: 'codex', status: 'completed', resumable: true,
  createdAt: sessionMetadata.createdAt, updatedAt: sessionMetadata.updatedAt,
}

function renderSidebar(overrides: Partial<Parameters<typeof CasualSidebar>[0]> = {}, prepareBackend?: (backend: Backend) => void) {
  const { backend } = createFakeBackend()
  prepareBackend?.(backend)
  const props = {
    backend,
    workspace,
    activeSessionId: undefined,
    activeSessionUpdatedAt: undefined,
    historyHasUserMessage: false,
    busy: false,
    loadingHistory: false,
    hasDraft: false,
    mobileOpen: false,
    selected: 'Conversas' as const,
    onOpenSession: vi.fn(),
    onSelectDestination: vi.fn(),
    onNewChat: vi.fn(),
    onProjects: vi.fn(),
    onToggleMode: vi.fn(),
    onClose: vi.fn(),
    ...overrides,
  }
  const view = render(<CasualSidebar {...props} />)
  return { ...view, backend, props }
}

describe('CasualSidebar', () => {
  it('filters pipeline sessions before reading events and uses a persisted chat title', async () => {
    const { backend } = renderSidebar({}, backend => {
      backend.listSessions = async () => [
        { ...session, id: 'prepare', purpose: 'preparation', title: '{"target":"spec"}' },
        { ...session, id: 'code', purpose: 'code' }, { ...session, id: 'eval', purpose: 'evaluation' },
        { ...session, id: 'chat', purpose: 'chat', title: 'Revisar acessibilidade' }, { ...session, id: 'legacy' },
      ]
      backend.listEvents = vi.fn(async id => [{ id: `${id}-user`, streamId: id, sequence: 1, type: 'message.user', data: { content: 'Conversa legada preservada' }, createdAt: session.createdAt }])
    })
    expect(await screen.findByRole('button', { name: /Revisar acessibilidade/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Conversa legada preservada/ })).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: /Revisar acessibilidade|Conversa legada preservada/ })).toHaveLength(2)
    expect(backend.listEvents).toHaveBeenCalledTimes(1)
    expect(backend.listEvents).toHaveBeenCalledWith('legacy', 0, 12)
    expect(screen.queryByText(/target/)).not.toBeInTheDocument()
  })

  it('does not turn a JSON prompt into a title and can use the next human request', async () => {
    renderSidebar({}, backend => {
      backend.listSessions = async () => [{ ...session, purpose: 'chat', title: '{"target":"spec"}' }]
      backend.listEvents = async id => [
        { id: 'technical', streamId: id, sequence: 1, type: 'message.user', data: { content: '{"documents":{"discovery":"interno"},"target":"spec"}' }, createdAt: session.createdAt },
        { id: 'human', streamId: id, sequence: 2, type: 'message.user', data: { content: 'Como melhorar a navegação?' }, createdAt: session.createdAt },
      ]
    })
    expect(await screen.findByRole('button', { name: /Como melhorar a navegação/ })).toBeInTheDocument()
    expect(screen.queryByText(/documents|target/)).not.toBeInTheDocument()
  })

  it('paginates chat sessions after filtering internal sessions', async () => {
    const { backend } = renderSidebar({}, backend => {
      backend.listSessions = async () => [
        ...Array.from({ length: 26 }, (_, index) => ({ ...session, id: `internal-${index}`, purpose: 'preparation' as const })),
        { ...session, id: 'chat-a', purpose: 'chat' as const, title: 'Primeira conversa' }, { ...session, id: 'chat-b', purpose: 'chat' as const, title: 'Segunda conversa' },
      ]
      backend.listEvents = vi.fn(async () => [])
    })
    expect(await screen.findByRole('button', { name: /Primeira conversa/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Segunda conversa/ })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Carregar mais conversas' })).not.toBeInTheDocument()
    expect(backend.listEvents).not.toHaveBeenCalled()
  })

  it('shows an honest empty state when no project is open', async () => {
    const { backend } = renderSidebar({ workspace: undefined }, backend => { backend.listSessions = vi.fn() })
    expect(screen.getByText('Nenhum projeto aberto')).toBeInTheDocument()
    expect(screen.getByText('Abra um projeto para ver o histórico de chats.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Trocar projeto' })).not.toBeInTheDocument()
    expect(within(screen.getByLabelText('Histórico de conversas')).getByRole('button', { name: 'Ver projetos' })).toBeInTheDocument()
    expect(backend.listSessions).not.toHaveBeenCalled()
  })

  it('shows project chat history by first user prompt and opens the selected session', async () => {
    const user = userEvent.setup()
    const { backend, props } = renderSidebar({}, backend => {
      backend.listSessions = vi.fn(async () => [session])
      backend.listEvents = vi.fn(async () => [
        { id: 'e1', streamId: session.id, sequence: 1, type: 'external.run.started', data: { adapter: 'codex' }, createdAt: session.createdAt },
        { id: 'e2', streamId: session.id, sequence: 2, type: 'message.user', data: { id: 'm1', role: 'user', content: 'Criar um TODO em HTML para revisar o fluxo' }, createdAt: session.createdAt },
      ])
    })

    await waitFor(() => expect(screen.getByRole('button', { name: /Criar um TODO em HTML/ })).toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: /Criar um TODO em HTML/ }))

    expect(props.onOpenSession).toHaveBeenCalledWith(session.id, workspace.id)
    expect(backend.listSessions).toHaveBeenCalledWith(workspace.id)
    expect(backend.listEvents).toHaveBeenCalledWith(session.id, 0, 12)
  })

  it('carries the active non-resumable chat request into New chat', async () => {
    const user = userEvent.setup()
    const failed: Session = { ...session, id: 'failed-codex', status: 'failed', resumable: false }
    const { backend, props } = renderSidebar({ activeSessionId: failed.id }, backend => {
      backend.listSessions = vi.fn(async () => [failed])
      backend.listEvents = vi.fn(async () => [{
        id: 'failed-request', streamId: failed.id, sequence: 1, type: 'message.user',
        data: { role: 'user', content: 'Retomar a tarefa Codex' }, createdAt: failed.createdAt,
      }])
    })

    await user.click(await screen.findByRole('button', { name: /Retomar a tarefa Codex/ }))
    await user.click(await screen.findByRole('button', { name: 'Novo chat' }))

    expect(props.onNewChat).toHaveBeenCalledWith('Retomar a tarefa Codex')
    expect(backend.listEvents).toHaveBeenCalledWith(failed.id, 0, 12)
  })

  it('names a continued chat after the person\'s words and carries only those into New chat', async () => {
    const user = userEvent.setup()
    const failed: Session = { ...session, id: 'failed-continued', status: 'failed', resumable: false }
    const continued = 'Corrija o nome da função\n\n---\nContexto da conversa anterior (referência; o pedido é o texto acima)\nPrimeiro pedido nela:\nCrie o endpoint'
    const { props } = renderSidebar({ activeSessionId: failed.id }, backend => {
      backend.listSessions = vi.fn(async () => [failed])
      backend.listEvents = vi.fn(async () => [{ id: 'continued-request', streamId: failed.id, sequence: 1, type: 'message.user', data: { role: 'user', content: continued }, createdAt: failed.createdAt }])
    })

    const chat = await screen.findByRole('button', { name: /Corrija o nome da função/ })
    expect(chat).not.toHaveTextContent('Contexto da conversa anterior')
    await user.click(chat)
    await user.click(await screen.findByRole('button', { name: 'Novo chat' }))
    expect(props.onNewChat).toHaveBeenCalledWith('Corrija o nome da função')
  })

  it('filters titles, marks the active chat and keeps all tools reachable', async () => {
    const user = userEvent.setup()
    const { props } = renderSidebar({ activeSessionId: session.id }, backend => {
      backend.listSessions = vi.fn(async () => [session, { ...session, id: 'session-2', updatedAt: '2026-09-26T10:00:00Z' }])
      backend.listEvents = vi.fn(async id => [{
        id: `${id}-message`, streamId: id, sequence: 1, type: 'message.user',
        data: { id: `${id}-user`, role: 'user', content: id === session.id ? 'Corrigir o fluxo Code' : 'Revisar documentação' },
        createdAt: session.createdAt,
      }])
    })

    const search = await screen.findByRole('textbox', { name: 'Buscar conversas' })
    await user.type(search, 'fluxo Code')
    expect(screen.getByRole('button', { name: /Corrigir o fluxo Code/ })).toHaveAttribute('aria-current', 'true')
    expect(screen.queryByRole('button', { name: /Revisar documentação/ })).not.toBeInTheDocument()

    await user.clear(search)
    await user.click(screen.getByText('Ferramentas'))
    const tools = screen.getByRole('navigation', { name: 'Ferramentas' })
    expect(within(tools).getByRole('button', { name: 'Pipelines' })).toBeInTheDocument()
    expect(within(tools).getByRole('button', { name: 'Configurações' })).toBeInTheDocument()
    await user.click(within(tools).getByRole('button', { name: 'Pipelines' }))
    expect(props.onSelectDestination).toHaveBeenCalledWith('Pipelines')
  })

  it('labels a failed non-resumable session as read only', async () => {
    const failed: Session = { ...session, status: 'failed', resumable: false }
    renderSidebar({}, backend => {
      backend.listSessions = async () => [failed]
      backend.listEvents = async id => [{ id: `${id}-failed`, streamId: id, sequence: 1, type: 'message.user', data: { content: 'Pedido encerrado' }, createdAt: session.createdAt }]
    })
    expect(await screen.findByRole('button', { name: /Pedido encerrado/ })).toHaveTextContent('Somente leitura')
  })

  it('shows recoverable loading, empty, error and busy states', async () => {
    const user = userEvent.setup()
    const { backend, props } = renderSidebar({ busy: true }, backend => {
      backend.listSessions = vi.fn(async () => [session])
      backend.listEvents = vi.fn(async () => [])
    })

    await waitFor(() => expect(screen.getByText(/Sessão em execução/)).toBeInTheDocument())
    expect(screen.getByRole('button', { name: /session-1/ })).toBeDisabled()

    backend.listSessions = vi.fn(async () => [])
    await user.click(screen.getByRole('button', { name: 'Atualizar histórico' }))
    await waitFor(() => expect(screen.getByText('Nenhuma conversa neste projeto.')).toBeInTheDocument())

    backend.listSessions = vi.fn().mockRejectedValueOnce(new Error('offline'))
    await user.click(screen.getByRole('button', { name: 'Atualizar histórico' }))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Não foi possível carregar o histórico.'))
    expect(props.onNewChat).toHaveBeenCalledTimes(0)
  })

  it('lets the person move on with a draft: each chat keeps its own', async () => {
    renderSidebar({ hasDraft: true })
    expect(await screen.findByRole('button', { name: 'Novo chat' })).toBeEnabled()
    expect(screen.queryByText(/Há um rascunho/)).not.toBeInTheDocument()
  })

  it('leads back to the running chat from another screen, without opening others', async () => {
    const user = userEvent.setup()
    const { props } = renderSidebar({ busy: true, selected: 'Projetos', activeSessionId: session.id }, backend => {
      backend.listSessions = async () => [session, { ...session, id: 'session-2' }]
      backend.listEvents = async id => [{ id: `${id}-e`, streamId: id, sequence: 1, type: 'message.user', data: { content: id === session.id ? 'Conversa com rascunho' : 'Outra conversa' }, createdAt: session.createdAt }]
    })
    expect(await screen.findByRole('button', { name: /Outra conversa/ })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: /Conversa com rascunho/ }))
    expect(props.onSelectDestination).toHaveBeenLastCalledWith('Conversas')
    expect(props.onOpenSession).not.toHaveBeenCalled()
  })

  it('allows replacing an in-flight history open without starting a new chat', async () => {
    const user = userEvent.setup()
    const { props } = renderSidebar({ loadingHistory: true }, backend => {
      backend.listSessions = async () => [session]
      backend.listEvents = async () => [{ id: 'e1', streamId: session.id, sequence: 1, type: 'message.user', data: { content: 'Abrir conversa' }, createdAt: session.createdAt }]
    })
    const row = await screen.findByRole('button', { name: /Abrir conversa/ })
    expect(row).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Novo chat' })).toBeDisabled()
    expect(screen.getByText(/Carregando conversa/)).toBeInTheDocument()
    await user.click(screen.getByText('Ferramentas'))
    expect(within(screen.getByRole('navigation', { name: 'Ferramentas' })).getByRole('button', { name: 'Pipelines' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Mudar para Professional' })).toBeDisabled()
    await user.click(row)
    expect(props.onOpenSession).toHaveBeenCalledWith(session.id, workspace.id)
  })

  it('loads older sessions while a search is active', async () => {
    const user = userEvent.setup()
    const sessions = Array.from({ length: 25 }, (_, index) => ({
      ...session,
      id: `session-${index + 1}`,
      updatedAt: `2026-09-${String(25 + (index % 5)).padStart(2, '0')}T10:00:00Z`,
    }))
    const { backend } = renderSidebar({}, backend => {
      backend.listSessions = vi.fn(async () => sessions)
      backend.listEvents = vi.fn(async id => [{
        id: `${id}-user`, streamId: id, sequence: 1, type: 'message.user',
        data: { content: id === 'session-25' ? 'Termo de busca no fim do histórico' : `Conversa ${id}` },
        createdAt: session.createdAt,
      }])
    })

    const search = await screen.findByRole('textbox', { name: 'Buscar conversas' })
    await user.type(search, 'Termo de busca')
    expect(screen.getByText('Nenhum chat corresponde à busca.')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Carregar mais para esta busca' }))
    expect(await screen.findByRole('button', { name: /Termo de busca no fim do histórico/ })).toBeInTheDocument()
    expect(backend.listEvents).toHaveBeenCalledWith('session-25', 0, 12)
  })

  it('refreshes chat titles when the active session receives its first user message', async () => {
    let hasMessage = false
    const { backend, props, rerender } = renderSidebar({}, backend => {
      backend.listSessions = vi.fn(async () => [session])
      backend.listEvents = vi.fn(async id => hasMessage ? [{
        id: `${id}-message`, streamId: id, sequence: 1, type: 'message.user',
        data: { content: 'Criar TODO no projeto' }, createdAt: session.createdAt,
      }] : [])
    })
    expect(await screen.findByRole('button', { name: /Conversa session-1/ })).toBeInTheDocument()
    hasMessage = true
    rerender(<CasualSidebar {...props} activeSessionId={session.id} activeSessionUpdatedAt={session.updatedAt} historyHasUserMessage />)
    expect(await screen.findByRole('button', { name: /Criar TODO no projeto/ })).toBeInTheDocument()
    expect(backend.listSessions).toHaveBeenCalledTimes(2)
  })

  it('groups the chats of every project, pins, reveals the folder and opens a chat or a new chat in another project', async () => {
    const user = userEvent.setup()
    const other = { id: 'workspace-2', path: '/Users/dev/painel', profile: 'ask', available: true }
    const current = { id: workspace.id, path: workspace.path, profile: 'ask', available: true }
    let pinned = false
    const onOpenProject = vi.fn()
    const { backend, props } = renderSidebar({ onOpenProject }, backend => {
      backend.listSessions = async () => [{ ...session, id: 'chat-here', purpose: 'chat', title: 'Revisar login' }]
      backend.listChatProjects = vi.fn(async () => [
        { workspace: current, chats: [{ ...session, id: 'chat-here', purpose: 'chat' as const, title: 'Revisar login', pinned }], total: 1 },
        { workspace: other, chats: [{ ...session, id: 'chat-there', workspaceId: other.id, purpose: 'chat' as const, title: 'Tema escuro do painel' }], total: 3 },
      ])
      backend.setSessionPinned = vi.fn(async (_id: string, value: boolean) => { pinned = value })
      backend.revealWorkspace = vi.fn(async () => undefined)
    })
    const group = await screen.findByRole('group', { name: 'Projeto painel' })
    await user.click(within(group).getByRole('button', { name: /Tema escuro do painel/ }))
    expect(props.onOpenSession).toHaveBeenCalledWith('chat-there', 'workspace-2', '/Users/dev/painel')
    await user.click(within(group).getByRole('button', { name: 'Abrir a pasta de painel' }))
    expect(backend.revealWorkspace).toHaveBeenCalledWith('workspace-2')
    await user.click(within(group).getByRole('button', { name: 'Novo chat em painel' }))
    expect(onOpenProject).toHaveBeenCalledWith('/Users/dev/painel')
    await user.click(within(group).getByRole('button', { name: 'Ver as 3 conversas' }))
    expect(onOpenProject).toHaveBeenCalledTimes(2)

    const history = screen.getByRole('list', { name: 'Histórico de chats' })
    await user.click(within(history).getByRole('button', { name: 'Fixar conversa' }))
    expect(backend.setSessionPinned).toHaveBeenCalledWith('chat-here', true)
    const pins = await screen.findByRole('list', { name: 'Conversas fixadas' })
    expect(within(pins).getByRole('button', { name: /Revisar login/ })).toBeInTheDocument()
    await user.click(within(pins).getByRole('button', { name: 'Desafixar conversa' }))
    expect(backend.setSessionPinned).toHaveBeenLastCalledWith('chat-here', false)
    await waitFor(() => expect(screen.queryByRole('list', { name: 'Conversas fixadas' })).not.toBeInTheDocument())

    await user.click(within(screen.getByRole('region', { name: 'Projeto atual' })).getByRole('button', { name: 'Abrir a pasta de todo-app' }))
    expect(backend.revealWorkspace).toHaveBeenCalledWith('workspace-1')
  })

  it('lists the project work with its stage, newest first, and opens it on Pipelines', async () => {
    const user = userEvent.setup()
    const onOpenPipeline = vi.fn()
    const run = (id: string, title: string, currentStage: Pipeline['currentStage'], status: string, updatedAt: string): Pipeline => ({ id, workspaceId: workspace.id, kind: 'ai_authoring', title, objective: title, currentStage,
      stageStatus: currentStage ? { [currentStage]: status } as Pipeline['stageStatus'] : {}, revision: 1, artifacts: {}, createdAt: updatedAt, updatedAt })
    renderSidebar({ onOpenPipeline }, backend => {
      backend.listPipelines = async () => [
        run('old', 'Exportar faturas', '', '', '2026-10-01T10:00:00Z'),
        run('todo', 'Crie um TODO com html', 'eval', 'waiting_user', '2026-10-08T10:00:00Z'),
        { ...run('other', 'Outro projeto', 'code', 'active', '2026-10-09T10:00:00Z'), workspaceId: 'workspace-2' },
      ]
    })
    const list = await screen.findByRole('list', { name: 'Trabalhos do projeto' })
    const items = within(list).getAllByRole('button')
    expect(items.map(item => item.textContent)).toEqual([expect.stringContaining('Crie um TODO com html'), expect.stringContaining('Exportar faturas')])
    expect(items[0]).toHaveTextContent('QA · aguardando você')
    expect(items[1]).toHaveTextContent('Concluído')
    await user.click(items[0])
    expect(onOpenPipeline).toHaveBeenCalledWith('todo', workspace.id)
  })
})
