import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import type { PipelineDesignDocument } from '../../../lib/backend'
import { BrainstormStage } from './BrainstormStage'

afterEach(cleanup)

const withDesign = JSON.stringify({ flow: 'flowchart TD\n  A[fluxo desenhado] --> B[fim]', design: { screens: [{ name: 'Lista', purpose: 'Ver contratos', html: '<html><body>Lista</body></html>' }] }, noDesignReason: '', notes: '## Decisões\n\nD1 manter CSV.' })
const withoutDesign = JSON.stringify({ flow: 'flowchart TD\n  A --> B', design: null, noDesignReason: 'Só backend: sem telas novas.', notes: '' })
const document = (content: string, over: Partial<PipelineDesignDocument> = {}): PipelineDesignDocument => ({ stage: 'brainstorm', version: content ? 1 : 0, content, contentDigest: content ? 'a'.repeat(64) : '', author: 'ai', sourceSessionId: 's1', sourceDigest: '', selection: null, stale: false, updatedAt: '2026-10-17T10:00:00Z', ...over })
const props = { conversation: <div>conversa de ajustes</div>, generating: false, canRefresh: true, onRefresh: () => undefined, onAskFix: () => undefined }

it('puts the design on the right and the flow with the conversation on the left', async () => {
  render(<BrainstormStage {...props} document={document(withDesign)} />)
  const flow = screen.getByRole('region', { name: 'Fluxo' }), design = screen.getByRole('region', { name: 'Design' })
  expect(await within(flow).findByText('fluxo desenhado')).toBeInTheDocument()
  expect(within(design).getByTitle('Lista')).toHaveAttribute('sandbox', '')
  expect(flow.closest('.brainstorm-left')).toContainElement(screen.getByText('conversa de ajustes'))
  expect(design.closest('.brainstorm-right')).not.toBeNull()
  expect(screen.getByText('D1 manter CSV.')).toBeInTheDocument()
})

it('drops the design box when the solution has no screens and says why', () => {
  const { container } = render(<BrainstormStage {...props} document={document(withoutDesign)} />)
  expect(screen.queryByRole('region', { name: 'Design' })).not.toBeInTheDocument()
  expect(screen.getByText('Só backend: sem telas novas.')).toBeInTheDocument()
  expect(container.querySelector('.brainstorm-stage')).toHaveClass('brainstorm-no-design')
})

it('asks to update a stale Brainstorm', async () => {
  const user = userEvent.setup(), onRefresh = vi.fn()
  render(<BrainstormStage {...props} onRefresh={onRefresh} document={document(withDesign, { stale: true })} />)
  expect(screen.getByText('Este Brainstorm precisa refletir a última mudança da SPEC.')).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Atualizar o Brainstorm' }))
  expect(onRefresh).toHaveBeenCalled()
})

it('shows the run in both boxes while the AI draws it, keeping the flow it already has', async () => {
  render(<BrainstormStage {...props} generating progress={<p>IA desenhando</p>} document={document(withDesign)} />)
  expect(within(screen.getByRole('region', { name: 'Fluxo' })).getByText('IA desenhando')).toBeInTheDocument()
  expect(within(screen.getByRole('region', { name: 'Design' })).getByText('IA desenhando')).toBeInTheDocument()
  expect(await screen.findByText('fluxo desenhado')).toBeInTheDocument()
  render(<BrainstormStage {...props} generating progress={<p>primeira vez</p>} document={document('')} />)
  expect(screen.getAllByText('primeira vez')).toHaveLength(2)
})

it('explains a Brainstorm skipped for work prepared before the stage, with nothing to do', () => {
  render(<BrainstormStage {...props} skipped document={document('')} />)
  expect(screen.getByText(/a etapa não existia quando este trabalho foi preparado/)).toBeInTheDocument()
  expect(screen.queryByRole('button')).not.toBeInTheDocument()
  expect(screen.queryByText('conversa de ajustes')).not.toBeInTheDocument()
})
