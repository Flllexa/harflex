import { act, cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import App from '../../app/App'
import type { ChatCommandEvent } from '../../lib/backend'
import { createFakeBackend, sessionMetadata } from '../../test/fakeBackend'

afterEach(() => { cleanup(); localStorage.clear() })

const stamp = '2026-10-14T10:00:00Z'
const session = { id: 'chat-1', workspaceId: 'workspace-1', backendId: 'local', status: 'ready', ...sessionMetadata, title: 'Relatórios' }
const journal = [
  { id: 'e1', streamId: session.id, sequence: 1, type: 'message.user', data: { role: 'user', content: 'Como aplico o relatório?' }, createdAt: stamp },
  { id: 'e2', streamId: session.id, sequence: 2, type: 'message.assistant', data: { role: 'assistant', content: 'Rode:\n\n```bash\nmake update-report-apply DATE=2026-10-14\n```' }, createdAt: stamp },
  { id: 'e3', streamId: session.id, sequence: 3, type: 'run.completed', data: { reason: '' }, createdAt: stamp },
]

it('runs a command from the chat, shows its output, and sends the result to the agent so it goes on', async () => {
  localStorage.setItem('harflex:view-mode', 'casual')
  const { backend } = createFakeBackend()
  let push: (event: ChatCommandEvent) => void = () => undefined
  backend.onChatCommand = listener => { push = listener; return () => undefined }
  backend.listSessions = async () => [session]
  backend.openSession = async () => session
  backend.listEvents = async (_id: string, after: number) => journal.filter(event => event.sequence > after)
  backend.runChatCommand = vi.fn(async () => 'run-1')
  backend.prompt = vi.fn(async () => ({ status: 'completed' as const }))
  const user = userEvent.setup()
  render(<App backend={backend} />)
  await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/relatorios')
  await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
  await user.click(await screen.findByRole('button', { name: /Relatórios/ }))
  const run = await screen.findByRole('button', { name: 'Executar o comando no projeto' })
  await waitFor(() => expect(run).toBeEnabled())
  await user.click(run)
  expect(backend.runChatCommand).toHaveBeenCalledWith('chat-1', 'make update-report-apply DATE=2026-10-14')
  await waitFor(() => expect(screen.getByRole('button', { name: 'Parar o comando' })).toBeInTheDocument())
  act(() => push({ id: 'run-1', sessionId: 'chat-1', text: 'Gravando status do lote 14102026_1\n', done: false, exitCode: -1, cancelled: false, timedOut: false, truncated: false, durationMs: 0 }))
  expect(within(screen.getByRole('region', { name: 'Saída do comando' })).getByText(/Gravando status do lote/)).toBeInTheDocument()
  act(() => push({ id: 'run-1', sessionId: 'chat-1', done: true, exitCode: 0, cancelled: false, timedOut: false, truncated: false, durationMs: 1200, output: 'Gravando status do lote 14102026_1\n3 linhas atualizadas\n' }))
  await waitFor(() => expect(backend.prompt).toHaveBeenCalledTimes(1))
  const [sessionId, text] = vi.mocked(backend.prompt).mock.calls[0]
  expect(sessionId).toBe('chat-1')
  expect(text).toContain('make update-report-apply DATE=2026-10-14')
  expect(text).toContain('código de saída 0')
  expect(text).toContain('3 linhas atualizadas')
  expect(await screen.findByText(/Concluído · resultado enviado ao agente/)).toBeInTheDocument()
})
