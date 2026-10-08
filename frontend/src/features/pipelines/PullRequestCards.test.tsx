import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import type { PullRequest } from '../../lib/backend'
import { createFakeBackend } from '../../test/fakeBackend'
import { PullRequestCards } from './PullRequestCards'

afterEach(cleanup)

const at = '2026-10-07T12:00:00Z'
const pr = (overrides: Partial<PullRequest> = {}): PullRequest => ({ id: 'pr-1', pipelineId: 'pipe', sessionId: 'chat', url: 'https://github.com/acme/todo/pull/7', title: 'Exportar CSV', branch: 'harflex/exportar-csv', state: 'open', watch: false, checking: false,
  timeline: [{ at, kind: 'opened', summary: 'PR aberto: Exportar CSV' }], ...overrides })

it('turns the review watch on and shows what it will do', async () => {
  const { backend } = createFakeBackend()
  backend.listPipelinePullRequests = async () => [pr()]
  backend.setPullRequestWatch = vi.fn(async (_id, watch) => pr({ watch, nextCheckAt: '2026-10-07T12:10:00Z', timeline: [...pr().timeline, { at, kind: 'watch_on', summary: 'Vigia da revisão ligada' }] }))
  render(<PullRequestCards backend={backend} pipelineId="pipe" permissionProfile="full_access" />)
  expect(await screen.findByRole('link', { name: 'Exportar CSV' })).toBeInTheDocument()
  expect(screen.getByText('harflex/exportar-csv')).toBeInTheDocument()
  const toggle = screen.getByRole('switch', { name: 'Vigiar revisão' })
  expect(toggle).toHaveAttribute('aria-checked', 'false')
  await userEvent.click(toggle)
  expect(backend.setPullRequestWatch).toHaveBeenCalledWith('pr-1', true)
  expect(await screen.findByText(/A cada 10 minutos a IA confere comentários novos/)).toBeInTheDocument()
  expect(screen.getByText('Vigia da revisão ligada')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Conferir agora' })).toBeEnabled()
})

it('offers the permission control when the watch needs Acesso total', async () => {
  const { backend } = createFakeBackend()
  backend.listPipelinePullRequests = async () => [pr({ watch: true, timeline: [{ at, kind: 'needs_access', summary: 'A vigia precisa de Acesso total' }] })]
  render(<PullRequestCards backend={backend} pipelineId="pipe" permissionProfile="ask" permissionControl={<span>MENU DE PERMISSÕES</span>} />)
  expect(await screen.findByText(/o projeto precisa de Acesso total/)).toBeInTheDocument()
  expect(screen.getByText('MENU DE PERMISSÕES')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Conferir agora' })).toBeDisabled()
})

it('shows a merged PR without the watch and refreshes when the backend says something changed', async () => {
  const { backend } = createFakeBackend()
  let current = pr({ watch: true })
  let notify: ((pipelineId: string) => void) | undefined
  backend.listPipelinePullRequests = async () => [current]
  backend.onPullRequestsChange = listener => { notify = listener; return () => undefined }
  render(<PullRequestCards backend={backend} pipelineId="pipe" permissionProfile="full_access" />)
  expect(await screen.findByRole('switch', { name: 'Vigiar revisão' })).toBeInTheDocument()
  current = pr({ state: 'merged', watch: false, timeline: [...pr().timeline, { at, kind: 'merged', summary: 'Corrigi os apontamentos e o PR foi mergeado' }] })
  notify?.('pipe')
  await waitFor(() => expect(screen.queryByRole('switch', { name: 'Vigiar revisão' })).not.toBeInTheDocument())
  expect(screen.getByText('Mergeado')).toBeInTheDocument()
  expect(screen.getByText('Corrigi os apontamentos e o PR foi mergeado')).toBeInTheDocument()
})
