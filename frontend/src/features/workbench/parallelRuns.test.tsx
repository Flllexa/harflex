import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it } from 'vitest'
import App from '../../app/App'
import type { AgentEvent, RunResult, Session } from '../../lib/backend'
import { createFakeBackend } from '../../test/fakeBackend'

const at = '2026-10-18T10:00:00Z'

/** Two chats, each with its own journal; a prompt stays running until the test finishes it. */
function twoChats() {
  const { backend } = createFakeBackend()
  const listeners = new Set<(event: AgentEvent) => void>()
  const sessions: Session[] = [
    { id: 'chat-a', workspaceId: 'workspace-1', backendId: 'local', status: 'completed', resumable: true, createdAt: at, updatedAt: '2026-10-18T10:00:02Z' },
    { id: 'chat-b', workspaceId: 'workspace-1', backendId: 'local', status: 'completed', resumable: true, createdAt: at, updatedAt: '2026-10-18T10:00:01Z' },
  ]
  const journals: Record<string, AgentEvent[]> = { 'chat-a': [], 'chat-b': [] }
  const emit = (streamId: string, type: string, data: unknown = {}) => {
    const journal = journals[streamId]
    const event: AgentEvent = { id: `${streamId}-${journal.length + 1}`, streamId, sequence: journal.length + 1, type, data, createdAt: at }
    journal.push(event)
    listeners.forEach(listener => listener(event))
  }
  for (const [id, request] of [['chat-a', 'Pedido do chat A'], ['chat-b', 'Pedido do chat B']] as const) {
    emit(id, 'run.started'); emit(id, 'message.user', { role: 'user', content: request }); emit(id, 'message.assistant', { role: 'assistant', content: `Pronto ${id}` }); emit(id, 'run.completed')
  }
  const running = new Map<string, (result: RunResult) => void>()
  backend.listSessions = async workspaceId => sessions.filter(session => session.workspaceId === workspaceId)
  backend.openSession = async id => sessions.find(session => session.id === id)!
  backend.listEvents = async (id, after = 0) => (journals[id] ?? []).filter(event => event.sequence > after)
  backend.onEvent = listener => { listeners.add(listener); return () => listeners.delete(listener) }
  backend.prompt = (sessionId, text) => {
    emit(sessionId, 'run.started')
    emit(sessionId, 'message.user', { role: 'user', content: text })
    return new Promise(resolve => running.set(sessionId, resolve))
  }
  const finish = (sessionId: string, reply: string) => {
    emit(sessionId, 'message.assistant', { role: 'assistant', content: reply })
    emit(sessionId, 'run.completed')
    running.get(sessionId)?.({ status: 'completed' })
  }
  return { backend, finish }
}

async function openProject(user: ReturnType<typeof userEvent.setup>) {
  await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/todo-app')
  await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
}

describe('parallel activities', () => {
  afterEach(() => { cleanup(); localStorage.clear() })

  it('a run that ends after switching chats never lands in the chat on screen', async () => {
    const user = userEvent.setup()
    const { backend, finish } = twoChats()
    localStorage.setItem('harflex:view-mode', 'casual')
    render(<App backend={backend} />)
    await openProject(user)
    await user.click(await screen.findByRole('button', { name: /Pedido do chat A/ }))
    await user.type(await screen.findByLabelText('Mensagem'), 'Continue A{Enter}')
    await screen.findByText('Continue A')
    await user.click(screen.getByRole('button', { name: /Pedido do chat B/ }))
    expect(await screen.findByText('Pronto chat-b')).toBeInTheDocument()
    expect(screen.queryByText(/Conclua ou cancele a execução atual/)).not.toBeInTheDocument()
    finish('chat-a', 'Resposta do A')
    await new Promise(resolve => setTimeout(resolve, 50))
    expect(screen.queryByText('Resposta do A')).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /Pedido do chat A/ }))
    expect(await screen.findByText('Resposta do A')).toBeInTheDocument()
  })

  it('returning to a chat still answering keeps its composer busy', async () => {
    const user = userEvent.setup()
    const { backend, finish } = twoChats()
    localStorage.setItem('harflex:view-mode', 'casual')
    render(<App backend={backend} />)
    await openProject(user)
    await user.click(await screen.findByRole('button', { name: /Pedido do chat A/ }))
    await user.type(await screen.findByLabelText('Mensagem'), 'Continue A{Enter}')
    await screen.findByText('Continue A')
    await user.click(screen.getByRole('button', { name: /Pedido do chat B/ }))
    await screen.findByText('Pronto chat-b')
    await user.click(screen.getByRole('button', { name: /Pedido do chat A/ }))
    await screen.findByText('Continue A')
    // Still answering: the box offers to stop the run, not to send a second message.
    expect(await screen.findByRole('button', { name: 'Cancelar execução' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Enviar' })).not.toBeInTheDocument()
    finish('chat-a', 'Resposta do A')
    expect(await screen.findByText('Resposta do A')).toBeInTheDocument()
    await user.type(await screen.findByLabelText('Mensagem'), 'Outra')
    await waitFor(() => expect(screen.getByRole('button', { name: 'Enviar' })).toBeEnabled())
  })

  it('starts new work while a chat runs', async () => {
    const user = userEvent.setup()
    const { backend } = twoChats()
    localStorage.setItem('harflex:view-mode', 'casual')
    render(<App backend={backend} />)
    await openProject(user)
    await user.click(await screen.findByRole('button', { name: /Pedido do chat A/ }))
    await user.type(await screen.findByLabelText('Mensagem'), 'Continue A{Enter}')
    await screen.findByText('Continue A')
    await user.click(screen.getAllByRole('button', { name: 'Novo chat' })[0])
    expect(await screen.findByLabelText('Mensagem inicial')).toBeEnabled()
    expect(screen.queryByText(/Cancele ou conclua a execução atual/)).not.toBeInTheDocument()
  })

  it('the casual sidebar never disables chats while one runs', async () => {
    const user = userEvent.setup()
    const { backend } = twoChats()
    localStorage.setItem('harflex:view-mode', 'casual')
    render(<App backend={backend} />)
    await openProject(user)
    await user.click(await screen.findByRole('button', { name: /Pedido do chat A/ }))
    await user.type(await screen.findByLabelText('Mensagem'), 'Continue A{Enter}')
    await screen.findByText('Continue A')
    const history = screen.getByRole('button', { name: /Pedido do chat B/ })
    expect(history).toBeEnabled()
    for (const button of screen.getAllByRole('button', { name: 'Novo chat' })) expect(button).toBeEnabled()
  })
})

describe('running Coders', () => {
  afterEach(() => { cleanup(); localStorage.clear() })

  it('a Coder that finishes while another chat is focused still has its code checked for review', async () => {
    const user = userEvent.setup()
    const { backend, finish } = twoChats()
    // The backend says chat A works as a pipeline's Coder while it runs.
    const [coder] = await backend.listSessions('workspace-1')
    let codeRunning = false
    let seenRunning = 0
    backend.listRunningSessions = async () => { if (codeRunning) seenRunning++; return codeRunning ? [{ session: coder, pipelineId: 'pipeline-1', role: 'coder' as const }] : [] }
    const verified: string[] = []
    backend.completePipelineCode = async pipelineId => { verified.push(pipelineId); throw new Error('no evidence in this fake') }
    localStorage.setItem('harflex:view-mode', 'casual')
    render(<App backend={backend} />)
    await openProject(user)
    await user.click(await screen.findByRole('button', { name: /Pedido do chat A/ }))
    codeRunning = true
    await user.type(await screen.findByLabelText('Mensagem'), 'Continue o código{Enter}')
    await screen.findByText('Continue o código')
    await user.click(screen.getByRole('button', { name: /Pedido do chat B/ }))
    await screen.findByText('Pronto chat-b')
    await waitFor(() => expect(seenRunning).toBeGreaterThan(0))
    codeRunning = false
    finish('chat-a', 'Código pronto')
    await waitFor(() => expect(verified).toEqual(['pipeline-1']))
  })
})

describe('the chat composer', () => {
  afterEach(() => { cleanup(); localStorage.clear() })

  it('queues a message with Cmd+Enter and sends it when the answer ends', async () => {
    const user = userEvent.setup()
    const { backend, finish } = twoChats()
    const sent: string[] = []
    const prompt = backend.prompt
    backend.prompt = (sessionId, text) => { sent.push(text); return prompt(sessionId, text) }
    localStorage.setItem('harflex:view-mode', 'casual')
    render(<App backend={backend} />)
    await openProject(user)
    await user.click(await screen.findByRole('button', { name: /Pedido do chat A/ }))
    await user.type(await screen.findByLabelText('Mensagem'), 'Primeiro{Enter}')
    await screen.findByText('Primeiro')
    await user.type(screen.getByLabelText('Mensagem'), 'Depois rode os testes')
    await user.keyboard('{Meta>}{Enter}{/Meta}')
    expect(await screen.findByRole('list', { name: 'Na fila · 1' })).toHaveTextContent('Depois rode os testes')
    expect(screen.getByLabelText('Mensagem')).toHaveValue('')
    expect(sent).toEqual(['Primeiro'])
    finish('chat-a', 'Feito')
    await waitFor(() => expect(sent).toEqual(['Primeiro', 'Depois rode os testes']))
    expect(screen.queryByRole('list', { name: /Na fila/ })).not.toBeInTheDocument()
  })

  it('sends an attached text file with the message', async () => {
    const user = userEvent.setup()
    const { backend } = twoChats()
    const sent: string[] = []
    const prompt = backend.prompt
    backend.prompt = (sessionId, text) => { sent.push(text); return prompt(sessionId, text) }
    localStorage.setItem('harflex:view-mode', 'casual')
    render(<App backend={backend} />)
    await openProject(user)
    await user.click(await screen.findByRole('button', { name: /Pedido do chat A/ }))
    await user.upload(await screen.findByLabelText('Arquivos para anexar'), new File(['regra: usar UTC'], 'regras.md', { type: 'text/markdown' }))
    expect(await screen.findByRole('list', { name: 'Anexos' })).toHaveTextContent('regras.md')
    await user.type(screen.getByLabelText('Mensagem'), 'Siga as regras{Enter}')
    await waitFor(() => expect(sent).toHaveLength(1))
    expect(sent[0]).toContain('Siga as regras')
    expect(sent[0]).toContain('regra: usar UTC')
    expect(screen.queryByRole('list', { name: 'Anexos' })).not.toBeInTheDocument()
  })
})

describe('a run that ends while the next chat opens', () => {
  afterEach(() => { cleanup(); localStorage.clear() })

  it('never lands on the chat being opened', async () => {
    const user = userEvent.setup()
    const { backend, finish } = twoChats()
    const open = backend.openSession
    let release!: () => void
    const gate = new Promise<void>(resolve => { release = resolve })
    localStorage.setItem('harflex:view-mode', 'casual')
    render(<App backend={backend} />)
    await openProject(user)
    await user.click(await screen.findByRole('button', { name: /Pedido do chat A/ }))
    await user.type(await screen.findByLabelText('Mensagem'), 'Continue A{Enter}')
    await screen.findByText('Continue A')
    backend.openSession = async (id, workspaceId) => { if (id === 'chat-b') await gate; return open(id, workspaceId) }
    // A's journal is slow to read back, so A's call is still finishing when B takes the screen.
    const events = backend.listEvents
    let slowA: Promise<void> | undefined
    let releaseA!: () => void
    backend.listEvents = async (id, after, limit) => { if (id === 'chat-a' && slowA) await slowA; return events(id, after, limit) }
    await user.click(screen.getByRole('button', { name: /Pedido do chat B/ }))
    slowA = new Promise<void>(resolve => { releaseA = resolve })
    finish('chat-a', 'Resposta do A')
    await new Promise(resolve => setTimeout(resolve, 20))
    release()
    await screen.findByText('Pronto chat-b')
    releaseA()
    slowA = undefined
    expect(await screen.findByText('Pronto chat-b')).toBeInTheDocument()
    await new Promise(resolve => setTimeout(resolve, 100))
    expect(screen.queryByText('Resposta do A')).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.queryByText(/Não foi possível atualizar/)).not.toBeInTheDocument()
  })
})

describe('the queue and the stop button', () => {
  afterEach(() => { cleanup(); localStorage.clear() })

  it('stopping the answer puts the queued message back in the box instead of sending it', async () => {
    const user = userEvent.setup()
    const { backend } = twoChats()
    const sent: string[] = []
    const prompt = backend.prompt
    backend.prompt = (sessionId, text) => { sent.push(text); return prompt(sessionId, text) }
    backend.cancel = async () => undefined
    localStorage.setItem('harflex:view-mode', 'casual')
    render(<App backend={backend} />)
    await openProject(user)
    await user.click(await screen.findByRole('button', { name: /Pedido do chat A/ }))
    await user.type(await screen.findByLabelText('Mensagem'), 'Primeiro{Enter}')
    await screen.findByText('Primeiro')
    await user.type(screen.getByLabelText('Mensagem'), 'Depois isto')
    await user.keyboard('{Meta>}{Enter}{/Meta}')
    await screen.findByRole('list', { name: 'Na fila · 1' })
    await user.click(screen.getByRole('button', { name: 'Cancelar execução' }))
    await waitFor(() => expect(screen.getByLabelText('Mensagem')).toHaveValue('Depois isto'))
    expect(screen.queryByRole('list', { name: /Na fila/ })).not.toBeInTheDocument()
    expect(sent).toEqual(['Primeiro'])
  })

  it('Cmd+Enter in an idle chat sends, never queues', async () => {
    const user = userEvent.setup()
    const { backend } = twoChats()
    localStorage.setItem('harflex:view-mode', 'casual')
    render(<App backend={backend} />)
    await openProject(user)
    await user.click(await screen.findByRole('button', { name: /Pedido do chat A/ }))
    await user.type(await screen.findByLabelText('Mensagem'), 'Agora')
    await user.keyboard('{Meta>}{Enter}{/Meta}')
    await screen.findByText('Agora')
    expect(screen.queryByRole('list', { name: /Na fila/ })).not.toBeInTheDocument()
  })
})
