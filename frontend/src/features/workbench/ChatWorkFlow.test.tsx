import { act, cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import App from '../../app/App'
import { createFakeBackend, sessionMetadata } from '../../test/fakeBackend'
import { designPipeline } from '../../test/pipelineDesignFixture'

afterEach(() => { cleanup(); localStorage.clear() })

const stamp = '2026-10-15T10:00:00Z'

it('shows the open chat\'s own work when the person goes to Professional, not the work picked earlier', async () => {
  localStorage.setItem('harflex:view-mode', 'casual')
  const { backend } = createFakeBackend()
  const older = { ...designPipeline('older-work'), title: 'Ingestão oficial dos lotes ARRC018' }
  const own = { ...designPipeline('chat-work'), title: 'ola, tudo bem?' }
  const session = { id: 'chat-1', workspaceId: 'workspace-1', backendId: 'local', status: 'ready', ...sessionMetadata, title: 'ola, tudo bem?' }
  type Change = { workspaceId: string; pipelineId: string; sessionId?: string; reason?: 'chat' | 'agent' }
  const listeners = new Set<(change: Change) => void>()
  const announce = (change: Change) => listeners.forEach(listener => listener(change))
  let linked: typeof own | undefined
  backend.onPipelineCreated = listener => { listeners.add(listener); return () => { listeners.delete(listener) } }
  backend.listSessions = async () => [session]
  backend.openSession = async () => session
  backend.listEvents = async () => [{ id: 'e1', streamId: session.id, sequence: 1, type: 'message.user', data: { role: 'user', content: 'ola, tudo bem?' }, createdAt: stamp }]
  backend.listPipelines = async () => linked ? [older, own] : [older]
  backend.getPipeline = vi.fn(async (id: string) => id === own.id ? own : older)
  backend.getPipelineForSession = async () => linked as never
  const user = userEvent.setup()
  render(<App backend={backend} />)
  await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/workspace')
  await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
  await user.click(await screen.findByRole('button', { name: /ola, tudo bem\?/ }))
  // The chat's first message opened its work.
  linked = own
  act(() => announce({ workspaceId: 'workspace-1', pipelineId: own.id, sessionId: session.id, reason: 'chat' }))
  await waitFor(() => expect(backend.getPipeline).toHaveBeenCalledWith(own.id))
  expect(screen.queryByText('Pipeline criado pelo agente')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Professional' }))
  await user.click(within(await screen.findByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Pipelines' }))
  await waitFor(() => expect(screen.getByRole('combobox', { name: 'Trabalho deste projeto' })).toHaveValue('ola, tudo bem?'))
  expect(screen.getByRole('heading', { name: 'ola, tudo bem?' })).toBeInTheDocument()
})
