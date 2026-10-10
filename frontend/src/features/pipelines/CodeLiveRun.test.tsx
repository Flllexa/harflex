import { cleanup, render, renderHook, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import type { AgentEvent, Pipeline, PipelineStageActivity } from '../../lib/backend'
import { createFakeBackend } from '../../test/fakeBackend'
import { CodeLiveRun, codeIsRunning, useAutoVerifyCode } from './CodeLiveRun'

afterEach(cleanup)

const activity = (status: string): PipelineStageActivity => ({ pipelineId: 'p1', workspaceId: 'workspace-1', stage: 'code', status, phase: '', sessionId: 'coder-1', modelId: 'opus', updatedAt: '2026-10-09T10:00:00Z' })

function journal(): AgentEvent[] {
  let sequence = 0
  const event = (type: string, data: unknown): AgentEvent => ({ id: `e${++sequence}`, streamId: 'coder-1', sequence, type, data, createdAt: '2026-10-09T10:00:00Z' })
  const plan = (steps: [string, string][]) => ({ id: 'plan', name: 'update_plan', arguments: { plan: steps.map(([step, status]) => ({ step, status })) } })
  return [
    event('message.user', { content: 'Implemente este trabalho' }),
    event('message.assistant', { content: '', toolCalls: [{ id: 'r1', name: 'read', arguments: { path: 'app.js' } }] }),
    event('tool.called', { toolCallId: 'r1', name: 'read' }), event('tool.completed', { toolCallId: 'r1', name: 'read' }),
    event('message.assistant', { content: '', toolCalls: [plan([['Ler o projeto', 'completed'], ['Escrever o formulário', 'in_progress'], ['Rodar os testes', 'pending']])] }),
    event('message.assistant', { content: '', toolCalls: [{ id: 'w1', name: 'write', arguments: { path: 'index.html' } }] }),
    event('tool.called', { toolCallId: 'w1', name: 'write' }),
  ]
}

it('says when the Coder is at work, so the bench shows the run and not the start screen', () => {
  expect(codeIsRunning(activity('running'))).toBe(true)
  expect(codeIsRunning(activity('awaiting_approval'))).toBe(true)
  expect(codeIsRunning(activity('completed'))).toBe(false)
  expect(codeIsRunning(activity('ready'))).toBe(false)
  expect(codeIsRunning({ ...activity('running'), sessionId: '' })).toBe(false)
  expect(codeIsRunning(undefined)).toBe(false)
})

it('shows the plan the Coder declared, the step it is on, and what it is doing in that step', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  backend.listEvents = async () => journal()
  const onOpenSession = vi.fn()
  render(<CodeLiveRun backend={backend} activity={activity('running')} onOpenSession={onOpenSession} />)
  const plan = await screen.findByRole('list', { name: 'Plano do Coder' })
  const steps = within(plan).getAllByRole('listitem').filter(item => item.classList.contains('code-live-step'))
  expect(steps.map(step => step.textContent)).toEqual([expect.stringContaining('Ler o projeto'), expect.stringContaining('Escrever o formulário'), expect.stringContaining('Rodar os testes')])
  expect(steps[0]).toHaveClass('is-completed')
  expect(steps[1]).toHaveClass('is-in_progress')
  expect(steps[1]).toHaveAttribute('aria-current', 'step')
  expect(steps[2]).toHaveClass('is-pending')
  // The running step lists what the Coder does in it, and the header says what it is doing now.
  expect(steps[1]).toHaveTextContent('index.html')
  expect(steps[0]).not.toHaveTextContent('index.html')
  expect(screen.getByText(/Agora: /)).toHaveTextContent('index.html')
  expect(screen.getByText('O Coder está trabalhando')).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Ver a conversa' }))
  expect(onOpenSession).toHaveBeenCalledWith('coder-1')
})

it('says it is waiting for the person when an approval is pending', async () => {
  const { backend } = createFakeBackend()
  backend.listEvents = async () => journal()
  render(<CodeLiveRun backend={backend} activity={activity('awaiting_approval')} />)
  expect(await screen.findByText('O Coder espera a sua aprovação')).toBeInTheDocument()
  await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Aprove ou negue na conversa'))
})

it('explains that the plan is not there yet instead of showing an empty list', async () => {
  const { backend } = createFakeBackend()
  backend.listEvents = async () => []
  render(<CodeLiveRun backend={backend} activity={activity('running')} />)
  expect(await screen.findByText(/ainda não publicou o plano de trabalho/)).toBeInTheDocument()
})

it('counts the actions of a finished step, in the singular when there is one', async () => {
  const { backend } = createFakeBackend()
  let sequence = 0
  const event = (type: string, data: unknown): AgentEvent => ({ id: `e${++sequence}`, streamId: 'coder-1', sequence, type, data, createdAt: '2026-10-09T10:00:00Z' })
  const plan = (first: string) => ({ id: `p${sequence}`, name: 'update_plan', arguments: { plan: [{ step: 'Ler o projeto', status: first }, { step: 'Escrever', status: first === 'completed' ? 'in_progress' : 'pending' }] } })
  backend.listEvents = async () => [
    event('message.user', { content: 'Implemente' }),
    event('message.assistant', { content: '', toolCalls: [plan('in_progress')] }),
    event('message.assistant', { content: '', toolCalls: [{ id: 'r1', name: 'read', arguments: { path: 'app.js' } }] }),
    event('tool.called', { toolCallId: 'r1', name: 'read' }), event('tool.completed', { toolCallId: 'r1', name: 'read' }),
    event('message.assistant', { content: '', toolCalls: [plan('completed')] }),
  ]
  render(<CodeLiveRun backend={backend} activity={activity('running')} />)
  const plan_ = await screen.findByRole('list', { name: 'Plano do Coder' })
  await waitFor(() => expect(within(plan_).getByText('1 ação')).toBeInTheDocument())
})

const codeRunPipeline: Pipeline = { id: 'p1', workspaceId: 'workspace-1', kind: 'ai_authoring', title: 'TODO', objective: 'TODO', currentStage: 'code',
  stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'active', eval: 'pending' }, revision: 5, artifacts: {}, createdAt: '2026-10-09T10:00:00Z', updatedAt: '2026-10-09T10:00:00Z' }

it('verifies the code once when a run seen working ends, and never verifies an old finished run', async () => {
  const { backend } = createFakeBackend()
  const verified = vi.fn()
  const complete = vi.fn(async () => ({ ...codeRunPipeline, stageStatus: { ...codeRunPipeline.stageStatus, code: 'waiting_user' as const }, revision: 6 }))
  backend.completePipelineCode = complete
  const { rerender } = renderHook(({ current }) => useAutoVerifyCode(backend, codeRunPipeline, current, verified), { initialProps: { current: activity('completed') } })
  // The run of a past round, already finished when the page opens, is left alone.
  expect(complete).not.toHaveBeenCalled()
  rerender({ current: activity('running') })
  rerender({ current: activity('completed') })
  await waitFor(() => expect(complete).toHaveBeenCalledTimes(1))
  await waitFor(() => expect(verified).toHaveBeenCalledWith(expect.objectContaining({ revision: 6 })))
  // A later read of the same finished run does not verify it again.
  rerender({ current: activity('completed') })
  expect(complete).toHaveBeenCalledTimes(1)
})

it('does not verify while the stage is no longer waiting for the Coder', async () => {
  const { backend } = createFakeBackend()
  const complete = vi.fn(async () => codeRunPipeline)
  backend.completePipelineCode = complete
  const waiting: Pipeline = { ...codeRunPipeline, stageStatus: { ...codeRunPipeline.stageStatus, code: 'waiting_user' } }
  const { rerender } = renderHook(({ current }) => useAutoVerifyCode(backend, waiting, current, vi.fn()), { initialProps: { current: activity('running') } })
  rerender({ current: activity('completed') })
  await new Promise(resolve => setTimeout(resolve, 50))
  expect(complete).not.toHaveBeenCalled()
})
