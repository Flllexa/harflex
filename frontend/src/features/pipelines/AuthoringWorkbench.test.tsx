import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { createFakeBackend } from '../../test/fakeBackend'
import { stagePipeline, stageReadback } from '../../test/authoringStage'
import { AuthoringWorkbench } from './AuthoringWorkbench'

it('resets overlap consent when navigating away, returning, or switching to another pending pipeline', async () => {
  const { backend } = createFakeBackend()
  backend.getAuthoringStage = async input => ({ ...stageReadback, pipelineId: input.pipelineId, stage: input.stage, state: 'cancellation_pending', cancellationPending: true })
  const view = render(<AuthoringWorkbench backend={backend} pipeline={stagePipeline} onPipelineChange={() => undefined} />)
  const consent = async () => {
    await userEvent.click(screen.getByRole('button', { name: 'Ver Discovery' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Criar nova versão derivada' }))
    const checkbox = await screen.findByRole('checkbox', { name: /Entendo que derivar/ })
    expect(checkbox).not.toBeChecked()
    expect(screen.getByRole('button', { name: 'Criar pipeline derivado' })).toBeDisabled()
    await userEvent.click(checkbox)
    expect(screen.getByRole('button', { name: 'Criar pipeline derivado' })).toBeEnabled()
  }
  await consent()
  await userEvent.click(screen.getByRole('button', { name: 'Ver SPEC' }))
  await consent()
  view.rerender(<AuthoringWorkbench backend={backend} pipeline={{ ...stagePipeline, id: 'pipeline-B', workspaceId: 'workspace-B' }} onPipelineChange={() => undefined} />)
  await consent()
})

it('requires explicit overlap acknowledgment before derivation from Discovery while a stage cancellation is pending', async () => {
  const { backend } = createFakeBackend()
  backend.getAuthoringStage = async input => ({ ...stageReadback, stage: input.stage, state: input.stage === 'spec' ? 'cancellation_pending' : 'pending', cancellationPending: input.stage === 'spec', cancellationAttemptId: input.stage === 'spec' ? 'attempt-1' : '' })
  backend.deriveAuthoringPipeline = vi.fn(async () => ({ ...stagePipeline, id: 'child', currentStage: 'discovery' as const, discoveryFrozenVersion: 0 }))
  backend.generateAuthoringStage = vi.fn()
  render(<AuthoringWorkbench backend={backend} pipeline={stagePipeline} onPipelineChange={() => undefined} />)
  await userEvent.click(screen.getByRole('button', { name: 'Ver Discovery' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Criar nova versão derivada' }))
  expect(await screen.findByText(/runner anterior ainda pode consumir recursos/)).toBeInTheDocument()
  const create = screen.getByRole('button', { name: 'Criar pipeline derivado' })
  expect(create).toBeDisabled()
  await userEvent.click(screen.getByRole('checkbox', { name: /Entendo que derivar não cancela nem aguarda/ }))
  await userEvent.click(create)
  expect(backend.deriveAuthoringPipeline).toHaveBeenCalledTimes(1)
  expect(backend.generateAuthoringStage).not.toHaveBeenCalled()
})

it('fails closed on readback error and rechecks on return to Discovery or pipeline revision', async () => {
  const { backend } = createFakeBackend()
  let readable = false
  backend.getAuthoringStage = vi.fn(async input => { if (!readable) throw new Error('Unavailable'); return { ...stageReadback, stage: input.stage, state: 'ready' as const, cancellationPending: false } })
  const view = render(<AuthoringWorkbench backend={backend} pipeline={stagePipeline} onPipelineChange={() => undefined} />)
  await userEvent.click(screen.getByRole('button', { name: 'Ver Discovery' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Criar nova versão derivada' }))
  expect(await screen.findByText(/Não foi possível confirmar o cancelamento das fases/)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Criar pipeline derivado' })).toBeDisabled()
  readable = true
  await userEvent.click(screen.getByRole('button', { name: 'Atualizar estado das fases' }))
  await waitFor(() => expect(screen.getByRole('button', { name: 'Criar pipeline derivado' })).toBeEnabled())
  const calls = vi.mocked(backend.getAuthoringStage).mock.calls.length
  await userEvent.click(screen.getByRole('button', { name: 'Ver SPEC' }))
  await userEvent.click(screen.getByRole('button', { name: 'Ver Discovery' }))
  await waitFor(() => expect(vi.mocked(backend.getAuthoringStage).mock.calls.length).toBeGreaterThan(calls))
  readable = false
  view.rerender(<AuthoringWorkbench backend={backend} pipeline={{ ...stagePipeline, revision: 6 }} onPipelineChange={() => undefined} />)
  expect(await screen.findByText(/Não foi possível confirmar o cancelamento das fases/)).toBeInTheDocument()
})

it('does not carry a pending warning or confirmation from pipeline A into B', async () => {
  const { backend } = createFakeBackend()
  let resolveA!: (value: typeof stageReadback) => void
  backend.getAuthoringStage = vi.fn(async input => input.pipelineId === 'pipeline-1' ? await new Promise<typeof stageReadback>(resolve => { resolveA = resolve }) : { ...stageReadback, pipelineId: 'pipeline-B', stage: input.stage, cancellationPending: false, state: 'ready' as const })
  const view = render(<AuthoringWorkbench backend={backend} pipeline={stagePipeline} onPipelineChange={() => undefined} />)
  await userEvent.click(screen.getByRole('button', { name: 'Ver Discovery' }))
  view.rerender(<AuthoringWorkbench backend={backend} pipeline={{ ...stagePipeline, id: 'pipeline-B', workspaceId: 'workspace-B' }} onPipelineChange={() => undefined} />)
  await act(async () => resolveA({ ...stageReadback, state: 'cancellation_pending', cancellationPending: true }))
  await userEvent.click(screen.getByRole('button', { name: 'Ver Discovery' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Criar nova versão derivada' }))
  await waitFor(() => expect(screen.getByRole('button', { name: 'Criar pipeline derivado' })).toBeEnabled())
  expect(screen.queryByText(/runner anterior ainda pode consumir recursos/)).not.toBeInTheDocument()
  expect(screen.queryByRole('checkbox', { name: /Entendo que derivar/ })).not.toBeInTheDocument()
})
