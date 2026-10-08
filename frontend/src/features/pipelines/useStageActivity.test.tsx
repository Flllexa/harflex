import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { AgentEvent, Pipeline, PipelineStageActivity } from '../../lib/backend'
import { createFakeBackend } from '../../test/fakeBackend'
import { designPipeline } from '../../test/pipelineDesignFixture'
import { useStageActivity } from './useStageActivity'

const run = (overrides: Partial<Pipeline> = {}): Pipeline => ({ ...designPipeline(), currentStage: 'code', stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'active', eval: 'pending' }, ...overrides })
const report = (stage: PipelineStageActivity['stage'], status: string): PipelineStageActivity => ({ pipelineId: 'design-1', workspaceId: 'workspace-1', stage, status, phase: '', sessionId: 'session-1', modelId: '', updatedAt: '2026-09-25T10:00:00Z' })

beforeEach(() => { vi.useFakeTimers({ shouldAdvanceTime: true }) })
afterEach(() => { vi.useRealTimers() })

it('reads the session of the current execution stage and follows its events', async () => {
  const { backend } = createFakeBackend()
  const listeners: Array<(event: AgentEvent) => void> = []
  backend.onEvent = listener => { listeners.push(listener); return () => { listeners.splice(listeners.indexOf(listener), 1) } }
  let status = 'running'
  const read = vi.spyOn(backend, 'getPipelineStageActivity').mockImplementation(async (_id, stage) => report(stage, status))
  const pipeline = run()
  const { result } = renderHook(() => useStageActivity(backend, pipeline))
  await waitFor(() => expect(result.current?.status).toBe('running'))
  expect(read).toHaveBeenCalledWith(pipeline.id, 'code')

  status = 'completed'
  act(() => listeners.forEach(listener => listener({ id: 'e1', streamId: 'session-1', sequence: 9, type: 'run.completed', data: {}, createdAt: '2026-09-25T10:00:00Z' })))
  await act(async () => { await vi.advanceTimersByTimeAsync(300) })
  await waitFor(() => expect(result.current?.status).toBe('completed'))
})

it('also follows the agent that opens the pull requests', async () => {
  const { backend } = createFakeBackend()
  const read = vi.spyOn(backend, 'getPipelineStageActivity').mockImplementation(async (_id, stage) => report(stage, 'running'))
  const pipeline = run({ currentStage: 'prs', stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'completed', eval: 'completed', prs: 'active' } })
  const { result } = renderHook(() => useStageActivity(backend, pipeline))
  await waitFor(() => expect(result.current?.status).toBe('running'))
  expect(read).toHaveBeenCalledWith(pipeline.id, 'prs')
})

it('keeps a single pending read however many events arrive while the agent works', async () => {
  const { backend } = createFakeBackend()
  const listeners: Array<(event: AgentEvent) => void> = []
  backend.onEvent = listener => { listeners.push(listener); return () => undefined }
  const read = vi.spyOn(backend, 'getPipelineStageActivity').mockImplementation(async (_id, stage) => report(stage, 'awaiting_approval'))
  const { result } = renderHook(() => useStageActivity(backend, run()))
  await waitFor(() => expect(result.current?.status).toBe('awaiting_approval'))
  for (let index = 0; index < 20; index++) {
    act(() => listeners.forEach(listener => listener({ id: `a${index}`, streamId: 'session-1', sequence: index + 2, type: 'approval.requested', data: {}, createdAt: '2026-09-25T10:00:00Z' })))
    await act(async () => { await vi.advanceTimersByTimeAsync(200) })
  }
  const before = read.mock.calls.length
  await act(async () => { await vi.advanceTimersByTimeAsync(30_000) })
  // One poll every three seconds, not one chain per event.
  expect(read.mock.calls.length - before).toBeLessThanOrEqual(11)
  expect(read.mock.calls.length).toBeLessThan(40)
})

it('follows a stage the caller names, for a pipeline that has no current stage', async () => {
  const { backend } = createFakeBackend()
  const read = vi.spyOn(backend, 'getPipelineStageActivity').mockImplementation(async (_id, stage) => report(stage, 'completed'))
  const finishedBeforeTheStage = run({ currentStage: '', stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'completed', eval: 'completed' } })
  const unnamed = renderHook(() => useStageActivity(backend, finishedBeforeTheStage))
  const named = renderHook(() => useStageActivity(backend, finishedBeforeTheStage, 'prs'))
  await waitFor(() => expect(named.result.current?.stage).toBe('prs'))
  expect(named.result.current?.status).toBe('completed')
  expect(unnamed.result.current).toBeUndefined()
  expect(read).toHaveBeenCalledWith(finishedBeforeTheStage.id, 'prs')
  expect(read).toHaveBeenCalledTimes(1)
})

it('ignores chatter that is not a change in the run', async () => {
  const { backend } = createFakeBackend()
  const listeners: Array<(event: AgentEvent) => void> = []
  backend.onEvent = listener => { listeners.push(listener); return () => undefined }
  const read = vi.spyOn(backend, 'getPipelineStageActivity').mockImplementation(async (_id, stage) => report(stage, 'completed'))
  const { result } = renderHook(() => useStageActivity(backend, run()))
  await waitFor(() => expect(result.current?.status).toBe('completed'))
  const reads = read.mock.calls.length
  act(() => listeners.forEach(listener => listener({ id: 'e2', streamId: 'session-1', sequence: 3, type: 'assistant.delta', data: { delta: 'x' }, createdAt: '2026-09-25T10:00:00Z' })))
  await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
  expect(read.mock.calls.length).toBe(reads)
})

it('stays silent for stages without a session of their own and for finished stages', async () => {
  const { backend } = createFakeBackend()
  const read = vi.spyOn(backend, 'getPipelineStageActivity')
  const discovery = renderHook(() => useStageActivity(backend, designPipeline()))
  const waiting = renderHook(() => useStageActivity(backend, run({ stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'waiting_user', eval: 'pending' } })))
  const none = renderHook(() => useStageActivity(backend, undefined))
  await act(async () => { await vi.advanceTimersByTimeAsync(500) })
  expect(read).not.toHaveBeenCalled()
  expect(discovery.result.current).toBeUndefined()
  expect(waiting.result.current).toBeUndefined()
  expect(none.result.current).toBeUndefined()
})

it('drops a report when the pipeline moves to another stage', async () => {
  const { backend } = createFakeBackend()
  vi.spyOn(backend, 'getPipelineStageActivity').mockImplementation(async (_id, stage) => report(stage, 'completed'))
  const { result, rerender } = renderHook(({ pipeline }) => useStageActivity(backend, pipeline), { initialProps: { pipeline: run() } })
  await waitFor(() => expect(result.current?.stage).toBe('code'))
  rerender({ pipeline: run({ currentStage: 'eval', revision: 9, stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'completed', eval: 'active' } }) })
  await waitFor(() => expect(result.current?.stage).toBe('eval'))
})
