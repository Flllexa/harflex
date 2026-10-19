import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import type { Pipeline } from '../../../lib/backend'
import { createFakeBackend } from '../../../test/fakeBackend'
import { CodeBench } from './CodeBench'
import { PipelineBench } from './PipelineBench'

afterEach(cleanup)

const date = '2026-10-19T10:00:00Z'
const inReview: Pipeline = { id: 'p1', workspaceId: 'workspace-1', kind: 'ai_authoring', title: 'TODO', objective: 'TODO', currentStage: 'code', revision: 7,
  stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'waiting_user', eval: 'pending' },
  artifacts: { code: { stage: 'code', version: 5, content: 'diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-a\n+b', contentDigest: 'c'.repeat(64), author: 'ai', sourceSessionId: 'coder', updatedAt: date } }, createdAt: date, updatedAt: date }

it('says the Code was approved, so the page can take the person to QA and start it', async () => {
  const { backend } = createFakeBackend()
  const approvedRun = { ...inReview, currentStage: 'eval' as const, stageStatus: { ...inReview.stageStatus, code: 'completed' as const, eval: 'active' as const } }
  backend.decidePipelineExecutionArtifact = vi.fn(async () => approvedRun)
  const onApproved = vi.fn(), onPipelineChange = vi.fn()
  render(<CodeBench backend={backend} run={inReview} onPipelineChange={onPipelineChange} onApproved={onApproved} />)
  await userEvent.click(screen.getByRole('button', { name: 'Aprovar Code e iniciar QA' }))
  expect(onPipelineChange).toHaveBeenCalledWith(approvedRun)
  expect(onApproved).toHaveBeenCalledTimes(1)
})

it('opens the bench it is asked to show, such as QA right after the Code is approved', () => {
  const { rerender } = render(<PipelineBench run={inReview} viewStage="code" code={<p>CODE</p>} qa={<p>QA</p>} prs={<p>PRS</p>} />)
  expect(screen.getByText('CODE')).toBeInTheDocument()
  rerender(<PipelineBench run={inReview} viewStage="code" showRequest={{ stage: 'eval', id: 1 }} code={<p>CODE</p>} qa={<p>QA</p>} prs={<p>PRS</p>} />)
  expect(screen.getByText('QA')).toBeInTheDocument()
})
