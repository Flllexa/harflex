import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { FlowView, initialViewport } from './FlowView'
import { layoutFlow, readFlow } from './flowGraph'

afterEach(cleanup)

it('renders a flow with accented labels', async () => {
  const { container } = render(<FlowView source={'flowchart TD\n  A[Cliente pede a baixa] --> B{"Contrato venceu?"}\n  B -->|não| C([Encerrar])'} onAskFix={() => undefined} />)
  expect(await screen.findByText('Contrato venceu?')).toBeInTheDocument()
  expect(screen.getByText('Cliente pede a baixa')).toBeInTheDocument()
  expect(container.querySelector('.react-flow')).not.toBeNull()
  expect(container.querySelector('.flow-shape-decision')).toHaveTextContent('Contrato venceu?')
  expect(container.querySelector('.flow-shape-terminal')).toHaveTextContent('Encerrar')
})

it('shows a label as text, never as markup', async () => {
  const { container } = render(<FlowView source={'flowchart TD\n  A["<img src=x onerror=alert(1)> abrir"] --> B[fim]'} onAskFix={() => undefined} />)
  expect(await screen.findByText(/abrir/)).toBeInTheDocument()
  expect(container.querySelector('.flow-canvas img')).toBeNull()
})

it('shows the parser error of an invalid flow and asks the AI to fix it', async () => {
  const user = userEvent.setup(), onAskFix = vi.fn()
  render(<FlowView source={'flowchart TD\n  A[sem fim'} onAskFix={onAskFix} />)
  const alert = await screen.findByRole('alert')
  expect(alert).toHaveTextContent('O fluxo não pôde ser desenhado')
  await user.click(screen.getByRole('button', { name: 'Pedir correção à IA' }))
  expect(onAskFix).toHaveBeenCalledWith(expect.stringMatching(/\S/))
})

it('asks for a flowchart when the diagram is of another kind', async () => {
  render(<FlowView source={'sequenceDiagram\n  A->>B: oi'} onAskFix={() => undefined} />)
  expect(await screen.findByRole('alert')).toHaveTextContent('flowchart')
})

it('shows the source on request and enlarges the flow', async () => {
  const user = userEvent.setup()
  render(<FlowView source={'flowchart TD\n  A[Abrir] --> B[Filtrar]'} onAskFix={() => undefined} />)
  await screen.findByText('Filtrar')
  await user.click(screen.getByRole('button', { name: 'Ver código' }))
  expect(screen.getByText(/A\[Abrir\] --> B\[Filtrar\]/)).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Ver fluxo' }))
  await user.click(await screen.findByRole('button', { name: 'Ampliar' }))
  const dialog = screen.getByRole('dialog', { name: 'Fluxo' })
  expect(await within(dialog).findByText('Filtrar')).toBeInTheDocument()
  await user.click(within(dialog).getByRole('button', { name: 'Fechar' }))
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

it('starts a long flow readable from its first step instead of shrinking it to fit', async () => {
  const steps = Array.from({ length: 14 }, (_, index) => `  S${index}[Passo ${index}]`).join('\n')
  const arrows = Array.from({ length: 13 }, (_, index) => `  S${index} --> S${index + 1}`).join('\n')
  const flow = layoutFlow(await readFlow(`flowchart TD\n${steps}\n${arrows}`))
  const viewport = initialViewport(flow, { width: 560 })
  expect(viewport.zoom).toBe(1)
  const first = flow.nodes[0]
  expect(first.position.y * viewport.zoom + viewport.y).toBe(16)
  const wide = layoutFlow(await readFlow('flowchart LR\n' + Array.from({ length: 12 }, (_, index) => `  W${index}[Etapa ${index}] --> W${index + 1}[Etapa ${index + 1}]`).join('\n')))
  expect(initialViewport(wide, { width: 360 }).zoom).toBe(0.6)
})
