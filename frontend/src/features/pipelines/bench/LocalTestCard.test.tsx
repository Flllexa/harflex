import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Backend, Pipeline, PipelineLocalTest } from '../../../lib/backend'
import { createFakeBackend } from '../../../test/fakeBackend'
import { LocalTestCard } from './LocalTestCard'

afterEach(cleanup)

const date = '2026-10-18T10:00:00Z'
const run: Pipeline = {
  id: 'pipeline-1', workspaceId: 'workspace-1', kind: 'ai_authoring', title: 'Exportar CSV', objective: 'CSV', currentStage: 'code', revision: 4,
  stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'waiting_user', eval: 'pending' },
  artifacts: { code: { stage: 'code', version: 2, content: 'diff', contentDigest: 'a'.repeat(64), author: 'ai', sourceSessionId: 'coder', updatedAt: date } }, createdAt: date, updatedAt: date,
}
const none: PipelineLocalTest = { pipelineId: 'pipeline-1', status: 'none', artifactVersion: 0, sessionId: '', testerStatus: '', services: [], servicesLost: false, backupMissing: false }
const applied: PipelineLocalTest = { ...none, status: 'applied', artifactVersion: 2, sessionId: 'tester', testerStatus: 'completed', services: [{ name: 'web', terminalId: 'term-9', running: true }],
  report: { urls: [{ label: 'App', url: 'http://localhost:5173' }], stop: 'Ctrl-C nos terminais', notes: 'Login: admin' } }
const backends = [{ id: 'api-work', name: 'API local', kind: 'api' as const, available: true }, { id: 'codex', name: 'Codex', kind: 'cli' as const, available: true }]

function setup(view: PipelineLocalTest, overrides: Partial<Backend> = {}, props: Record<string, unknown> = {}) {
  const { backend } = createFakeBackend()
  backend.getPipelineLocalTest = vi.fn(async () => view)
  Object.assign(backend, overrides)
  const handlers = { onStart: vi.fn(async () => undefined), onOpenSession: vi.fn(async () => undefined), onOpenTerminal: vi.fn(), onOpenPipeline: vi.fn(), onKeep: vi.fn() }
  render(<LocalTestCard backend={backend} run={run} backends={backends} defaultBackendId="api-work" canStart {...handlers} {...props} />)
  return { backend, ...handlers }
}
const card = () => screen.getByRole('region', { name: 'Teste local' })

describe('the local test card', () => {
  it('shows the start button while Code waits, with only the executors that can run it', async () => {
    const user = userEvent.setup()
    const { onStart } = setup(none)
    const start = await within(card()).findByRole('button', { name: 'Testar localmente' })
    expect(within(card()).getByText(/aplica esta versão na sua pasta/)).toBeInTheDocument()
    expect(within(card()).queryByRole('option', { name: 'Codex' })).not.toBeInTheDocument()
    await user.click(start)
    expect(onStart).toHaveBeenCalledWith('api-work')
  })

  it('shows the URLs and services once applied', async () => {
    const user = userEvent.setup()
    const { onOpenTerminal, onOpenSession } = setup(applied)
    expect(await within(card()).findByText('Aplicado na sua pasta para teste · versão 2')).toBeInTheDocument()
    expect(within(card()).getByRole('link', { name: 'App' })).toHaveAttribute('href', 'http://localhost:5173')
    expect(within(card()).getByText('Ctrl-C nos terminais')).toBeInTheDocument()
    await user.click(within(card()).getByRole('button', { name: 'Abrir web no Terminal' }))
    expect(onOpenTerminal).toHaveBeenCalledWith('term-9')
    await user.click(within(card()).getByRole('button', { name: 'Abrir conversa' }))
    expect(onOpenSession).toHaveBeenCalledWith('tester')
  })

  it('undo with a conflict offers keep and discard', async () => {
    const user = userEvent.setup()
    const conflicted: PipelineLocalTest = { ...applied, conflict: { edited: ['README.md'], created: ['server.log'] } }
    const undo = vi.fn(async (_id: string, discard?: boolean) => discard ? none : conflicted)
    const { onKeep } = setup(applied, { undoPipelineLocalTest: undo })
    await user.click(await within(card()).findByRole('button', { name: 'Desfazer teste' }))
    expect(await within(card()).findByText('Você mexeu em arquivos durante o teste')).toBeInTheDocument()
    expect(within(card()).getByText('README.md')).toBeInTheDocument()
    expect(within(card()).getByText('server.log')).toBeInTheDocument()
    await user.click(within(card()).getByRole('button', { name: 'Manter minhas mudanças' }))
    // Keeping the changes also drops a decision that was waiting on this undo: it is not sent later by itself.
    expect(onKeep).toHaveBeenCalled()
    expect(within(card()).queryByText('Você mexeu em arquivos durante o teste')).not.toBeInTheDocument()
    await user.click(within(card()).getByRole('button', { name: 'Desfazer teste' }))
    await user.click(await within(card()).findByRole('button', { name: 'Descartar e desfazer' }))
    expect(undo).toHaveBeenLastCalledWith('pipeline-1', true)
    expect(await within(card()).findByRole('button', { name: 'Testar localmente' })).toBeInTheDocument()
  })

  it('a dirty folder lists the files with a way to fix it', async () => {
    const user = userEvent.setup()
    const { onStart, onOpenTerminal } = setup(none)
    onStart.mockRejectedValueOnce({ cause: { code: 'local_test_source_dirty', paths: ['notas.md', 'src/x.ts'] } })
    await user.click(await within(card()).findByRole('button', { name: 'Testar localmente' }))
    expect(await within(card()).findByText('Sua pasta tem mudanças não salvas')).toBeInTheDocument()
    expect(within(card()).getByText('notas.md')).toBeInTheDocument()
    await user.click(within(card()).getByRole('button', { name: 'Abrir no Terminal' }))
    expect(onOpenTerminal).toHaveBeenCalledWith()
    await user.click(within(card()).getByRole('button', { name: 'Tentar de novo' }))
    await waitFor(() => expect(onStart).toHaveBeenCalledTimes(2))
  })

  it('services lost after a restart offers to bring them up again', async () => {
    const user = userEvent.setup()
    const { onOpenSession } = setup({ ...applied, servicesLost: true, services: [{ name: 'web', terminalId: 'term-gone', running: false }] })
    expect(await within(card()).findByText('Os serviços pararam quando o app fechou. As mudanças continuam aplicadas.')).toBeInTheDocument()
    await user.click(within(card()).getByRole('button', { name: 'Subir de novo' }))
    expect(onOpenSession).toHaveBeenCalledWith('tester', 'Suba os serviços de novo')
  })

  it('another pipeline holding the project says which, with a way to it', async () => {
    const user = userEvent.setup()
    const { onOpenPipeline } = setup({ ...none, heldBy: 'Outro trabalho', heldById: 'pipeline-2' })
    expect(await within(card()).findByText('Outro teste local está aplicado neste projeto: Outro trabalho')).toBeInTheDocument()
    await user.click(within(card()).getByRole('button', { name: 'Abrir esse pipeline' }))
    expect(onOpenPipeline).toHaveBeenCalledWith('pipeline-2')
  })

  it('says when the version under test is older than the one in review', async () => {
    setup({ ...applied, artifactVersion: 1 })
    expect(await within(card()).findByText('Em teste: versão 1. Aprovar desfaz o teste antes.')).toBeInTheDocument()
  })

  it('offers Claude Code only when the project is on Full access', async () => {
    const withClaude = [...backends, { id: 'claude', name: 'Claude Code', kind: 'cli' as const, available: true }]
    setup(none, {}, { backends: withClaude })
    await within(card()).findByRole('button', { name: 'Testar localmente' })
    expect(within(card()).queryByLabelText('Executor do teste')).not.toBeInTheDocument()
    expect(within(card()).getByText(/Claude Code só prepara o teste com Acesso total/)).toBeInTheDocument()
    cleanup()
    setup(none, {}, { backends: withClaude, fullAccess: true })
    expect(await within(card()).findByLabelText('Executor do teste')).toBeInTheDocument()
  })

  it('undoes from the commit when the backup is gone', async () => {
    setup({ ...applied, backupMissing: true })
    expect(await within(card()).findByText(/o desfazer restaura os arquivos a partir do commit/)).toBeInTheDocument()
    expect(within(card()).getByRole('button', { name: 'Desfazer teste' })).toBeEnabled()
  })

  it('does not offer a new test while the Code is not in review, but keeps an applied one in view', async () => {
    setup(none, {}, { canStart: false })
    await waitFor(() => expect(screen.queryByText('Lendo o teste local…')).not.toBeInTheDocument())
    expect(screen.queryByRole('button', { name: 'Testar localmente' })).not.toBeInTheDocument()
    cleanup()
    setup(applied, {}, { canStart: false })
    expect(await within(card()).findByRole('button', { name: 'Desfazer teste' })).toBeInTheDocument()
  })
})
