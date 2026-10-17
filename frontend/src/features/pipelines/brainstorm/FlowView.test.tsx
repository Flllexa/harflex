import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

const mermaid = vi.hoisted(() => ({ initialize: vi.fn(), parse: vi.fn(), render: vi.fn() }))
vi.mock('mermaid', () => ({ default: mermaid }))

import { FlowView } from './FlowView'

afterEach(cleanup)
beforeEach(() => {
  mermaid.initialize.mockReset(); mermaid.parse.mockReset(); mermaid.render.mockReset()
  mermaid.parse.mockResolvedValue({ diagramType: 'flowchart-v2' })
  mermaid.render.mockImplementation(async (_id: string, source: string) => ({ svg: `<svg role="img"><text>${source.includes('Contrato venceu?') ? 'Contrato venceu?' : 'fluxo'}</text></svg>` }))
})

it('renders a flow with accented labels', async () => {
  const source = 'flowchart TD\n  A[Cliente pede a baixa] --> B{"Contrato venceu?"}'
  render(<FlowView source={source} onAskFix={() => undefined} />)
  expect(await screen.findByText('Contrato venceu?')).toBeInTheDocument()
  expect(mermaid.initialize).toHaveBeenCalledWith(expect.objectContaining({ startOnLoad: false, securityLevel: 'strict' }))
  expect(mermaid.render).toHaveBeenCalledWith(expect.any(String), source)
})

it('shows the parser error of an invalid flow and asks the AI to fix it', async () => {
  const user = userEvent.setup(), onAskFix = vi.fn()
  mermaid.parse.mockRejectedValue(new Error('Parse error on line 2: Expecting SQE'))
  render(<FlowView source={'flowchart TD\n  A[sem fim'} onAskFix={onAskFix} />)
  expect(await screen.findByText(/Parse error on line 2: Expecting SQE/)).toBeInTheDocument()
  expect(mermaid.render).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Pedir correção à IA' }))
  expect(onAskFix).toHaveBeenCalledWith('Parse error on line 2: Expecting SQE')
})

it('shows the source on request and zooms the flow', async () => {
  const user = userEvent.setup()
  render(<FlowView source={'flowchart TD\n  A --> B'} onAskFix={() => undefined} />)
  await screen.findByText('fluxo')
  await user.click(screen.getByRole('button', { name: 'Ver código' }))
  expect(screen.getByText(/A --> B/)).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Ver fluxo' }))
  await user.click(screen.getByRole('button', { name: 'Aproximar' }))
  await waitFor(() => expect(document.querySelector('.flow-canvas')).toHaveAttribute('data-zoom', '1.25'))
  await user.click(screen.getByRole('button', { name: 'Ampliar' }))
  expect(screen.getByRole('dialog', { name: 'Fluxo' })).toBeInTheDocument()
})
