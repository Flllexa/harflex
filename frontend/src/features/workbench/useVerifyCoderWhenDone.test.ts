import { renderHook, waitFor } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import type { Pipeline } from '../../lib/backend'
import type { ActiveRun } from '../../state/session'
import { createFakeBackend } from '../../test/fakeBackend'
import { useVerifyCoderWhenDone } from './useVerifyCoderWhenDone'

const pipeline = (code: Pipeline['stageStatus'][string]): Pipeline => ({ id: 'p1', workspaceId: 'workspace-1', kind: 'ai_authoring', title: 'TODO', objective: 'TODO', currentStage: 'code',
  stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code, eval: 'pending' }, revision: 5, artifacts: {}, createdAt: '2026-10-09T10:00:00Z', updatedAt: '2026-10-09T10:00:00Z' })

function setup(initial: { activeRun: ActiveRun; outcome?: string; purpose?: 'code' | 'chat' }) {
  const { backend } = createFakeBackend()
  const complete = vi.fn(async () => pipeline('waiting_user'))
  backend.completePipelineCode = complete
  const onVerified = vi.fn()
  const view = renderHook((props: { activeRun: ActiveRun; outcome?: string; purpose?: 'code' | 'chat' }) => useVerifyCoderWhenDone({ backend, session: { id: 'coder-1', purpose: props.purpose ?? 'code' }, activeRun: props.activeRun, outcome: props.outcome, pipeline: pipeline('active'), onVerified }), { initialProps: initial })
  return { ...view, complete, onVerified }
}

it('verifies the code and hands the verified work over when a run seen working ends', async () => {
  const { rerender, complete, onVerified } = setup({ activeRun: 'running' })
  rerender({ activeRun: 'idle', outcome: 'completed' })
  await waitFor(() => expect(complete).toHaveBeenCalledWith('p1'))
  await waitFor(() => expect(onVerified).toHaveBeenCalledWith(expect.objectContaining({ id: 'p1' })))
})

it('waits through an approval and verifies only when the run completes', async () => {
  const { rerender, complete } = setup({ activeRun: 'running' })
  rerender({ activeRun: 'awaiting_approval' })
  expect(complete).not.toHaveBeenCalled()
  rerender({ activeRun: 'running' })
  rerender({ activeRun: 'idle', outcome: 'completed' })
  await waitFor(() => expect(complete).toHaveBeenCalledTimes(1))
})

it('leaves an old conversation alone: a run that was never seen working is not verified', async () => {
  const { complete } = setup({ activeRun: 'idle', outcome: 'completed' })
  await new Promise(resolve => setTimeout(resolve, 50))
  expect(complete).not.toHaveBeenCalled()
})

it('does not verify a failed or cancelled run, nor the conversation of anything but the Coder', async () => {
  const failed = setup({ activeRun: 'running' })
  failed.rerender({ activeRun: 'idle', outcome: 'failed' })
  const chat = setup({ activeRun: 'running', purpose: 'chat' })
  chat.rerender({ activeRun: 'idle', outcome: 'completed', purpose: 'chat' })
  await new Promise(resolve => setTimeout(resolve, 50))
  expect(failed.complete).not.toHaveBeenCalled()
  expect(chat.complete).not.toHaveBeenCalled()
})
