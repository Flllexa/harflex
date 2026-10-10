import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import App from '../../app/App'
import { createFakeBackend, sessionMetadata } from '../../test/fakeBackend'
import { designPipeline } from '../../test/pipelineDesignFixture'

afterEach(() => { cleanup(); localStorage.clear() })

const stamp = '2026-10-10T10:00:00Z'
const session = { id: 'chat-1', workspaceId: 'workspace-1', backendId: 'local', status: 'ready', ...sessionMetadata, title: 'Integrar o arquivo' }
const asked = [
  { id: 'e1', streamId: session.id, sequence: 1, type: 'message.user', data: { role: 'user', content: 'Integre o arquivo da Nuclea com a V2' }, createdAt: stamp },
  { id: 'e2', streamId: session.id, sequence: 2, type: 'message.assistant', data: { role: 'assistant', content: 'Entendi: o arquivo precisa chegar na V2.\n\n```harflex-choice\n{"kind":"pipeline"}\n```' }, createdAt: stamp },
  { id: 'e3', streamId: session.id, sequence: 3, type: 'run.completed', data: { reason: '' }, createdAt: stamp },
]

/** A Casual project with one chat whose last message is the agent offering a pipeline. */
async function openAskedChat(mode: 'casual' | 'professional') {
  localStorage.setItem('harflex:view-mode', mode)
  const { backend } = createFakeBackend()
  const listeners = new Set<(change: { workspaceId: string; pipelineId: string }) => void>()
  backend.onPipelineCreated = listener => { listeners.add(listener); return () => { listeners.delete(listener) } }
  backend.listSessions = async () => [session]
  backend.openSession = async () => session
  backend.listEvents = async (_id: string, after: number) => asked.filter(event => event.sequence > after)
  backend.prompt = vi.fn(async () => ({ status: 'completed' as const }))
  const created = { ...designPipeline(), id: 'new-pipeline', workspaceId: 'workspace-1', title: 'Integrar o arquivo' }
  backend.getPipeline = vi.fn(async () => created)
  backend.listPipelines = async () => [created]
  const user = userEvent.setup()
  render(<App backend={backend} />)
  await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
  await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
  await user.click(await screen.findByRole('button', { name: /Integrar o arquivo/ }))
  await screen.findByRole('group', { name: 'Como você quer tocar isso?' })
  return { backend, user, announce: () => act(() => listeners.forEach(listener => listener({ workspaceId: 'workspace-1', pipelineId: 'new-pipeline' }))) }
}

it('sends the chosen way to the agent, and goes to Professional mode on the pipeline once the agent creates it', async () => {
  const { backend, user, announce } = await openAskedChat('casual')
  expect(screen.queryByText(/harflex-choice/)).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: /ir para o modo Profissional/ }))
  await waitFor(() => expect(backend.prompt).toHaveBeenCalledTimes(1))
  expect(vi.mocked(backend.prompt).mock.calls[0][1]).toContain('modo Profissional')
  announce()
  expect(await screen.findByRole('heading', { name: 'Pipelines SDD' })).toBeInTheDocument()
  expect(localStorage.getItem('harflex:view-mode')).toBe('professional')
  expect(screen.queryByText('Pipeline criado pelo agente')).not.toBeInTheDocument()
})

it('stays in Casual and opens the pipeline there when the person chose to do everything from Casual', async () => {
  const { backend, user, announce } = await openAskedChat('casual')
  await user.click(screen.getByRole('button', { name: /fazer tudo por aqui, no Casual/ }))
  await waitFor(() => expect(backend.prompt).toHaveBeenCalledTimes(1))
  expect(vi.mocked(backend.prompt).mock.calls[0][1]).toContain('no modo Casual')
  announce()
  await waitFor(() => expect(backend.getPipeline).toHaveBeenCalled())
  expect(localStorage.getItem('harflex:view-mode')).toBe('casual')
  expect(screen.queryByText('Pipeline criado pelo agente')).not.toBeInTheDocument()
})

it('keeps the notice for a pipeline the person did not ask for, and does nothing special after choosing to stay in the chat', async () => {
  const { backend, user, announce } = await openAskedChat('casual')
  await user.click(screen.getByRole('button', { name: /Só resolver aqui na conversa/ }))
  await waitFor(() => expect(backend.prompt).toHaveBeenCalledTimes(1))
  expect(vi.mocked(backend.prompt).mock.calls[0][1]).toContain('sem abrir uma pipeline')
  announce()
  expect(await screen.findByText('Pipeline criado pelo agente')).toBeInTheDocument()
  expect(localStorage.getItem('harflex:view-mode')).toBe('casual')
})
