import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import type { ChatWatcher } from '../../lib/backend'
import { WatcherList } from './WatcherList'

const now = new Date('2026-10-21T19:00:00Z')
function watcher(overrides: Partial<ChatWatcher>): ChatWatcher {
  return { id: 'w1', sessionId: 's1', chatTitle: 'Graphiti no Dify', title: '4 manuais processados', command: 'curl -s localhost/status | grep -q done', intervalSeconds: 60,
    status: 'watching', reason: '', lastOutput: 'pending: 3 de 4\nprocessing manual 4', lastExitCode: 1, checks: 7, delivered: false,
    lastCheckAt: '2026-10-21T18:59:48Z', nextCheckAt: '2026-10-21T19:00:48Z', deadlineAt: '2026-10-21T20:00:00Z', createdAt: '2026-10-21T18:52:00Z', ...overrides }
}

it('shows what each watcher waits for, when it checks next, and lets the person stop it or open its chat', async () => {
  const user = userEvent.setup()
  const onStop = vi.fn()
  const onOpen = vi.fn()
  render(<WatcherList now={now} items={[
    watcher({}),
    watcher({ id: 'w2', title: 'lembrete do deploy', command: '', intervalSeconds: 0, nextCheckAt: '2026-10-21T19:20:00Z', lastOutput: '', checks: 0 }),
    watcher({ id: 'w3', title: 'build da imagem', status: 'fired', reason: 'condition_met', delivered: true, endedAt: '2026-10-21T18:58:00Z' }),
    watcher({ id: 'w4', title: 'fila', status: 'fired', reason: 'condition_met', delivered: false, endedAt: '2026-10-21T18:59:00Z' }),
  ]} onStop={onStop} onOpen={onOpen} />)
  const list = screen.getByRole('list', { name: 'Vigias · 3 ativas' })
  const first = within(list).getByRole('listitem', { name: '4 manuais processados' })
  expect(first).toHaveTextContent('Checando a cada 60 s · próxima em 48 s · 7 checagens')
  expect(first).toHaveTextContent('processing manual 4')
  expect(first).toHaveTextContent('Graphiti no Dify')
  expect(within(list).getByRole('listitem', { name: 'lembrete do deploy' })).toHaveTextContent('Volta ao chat em 20 min')
  expect(within(list).getByRole('listitem', { name: 'build da imagem' })).toHaveTextContent('Voltou ao chat')
  expect(within(list).getByRole('listitem', { name: 'fila' })).toHaveTextContent('Terminou; esperando o chat ficar livre')
  await user.click(within(first).getByRole('button', { name: 'Parar vigia 4 manuais processados' }))
  expect(onStop).toHaveBeenCalledWith('w1')
  await user.click(within(first).getByRole('button', { name: 'Abrir chat' }))
  expect(onOpen).toHaveBeenCalledWith(expect.objectContaining({ id: 'w1', sessionId: 's1' }))
  expect(within(within(list).getByRole('listitem', { name: 'build da imagem' })).queryByRole('button', { name: /Parar/ })).not.toBeInTheDocument()
})

it('shows nothing without watchers', () => {
  const { container } = render(<WatcherList now={now} items={[]} onStop={vi.fn()} onOpen={vi.fn()} />)
  expect(container).toBeEmptyDOMElement()
})
