import { expect, it } from 'vitest'
import { layoutFlow, readFlow } from './flowGraph'

it('reads a Mermaid flowchart into steps, decisions and labelled arrows, accents and quotes kept', async () => {
  const graph = await readFlow('flowchart LR\n  A[Cliente pede a baixa] --> B{"Contrato venceu?"}\n  B -- sim --> C([Fim])\n  B -->|não| D[Linha 1<br/>Linha 2]')
  expect(graph.direction).toBe('LR')
  expect(graph.nodes).toEqual([
    { id: 'A', label: 'Cliente pede a baixa', shape: 'step' },
    { id: 'B', label: 'Contrato venceu?', shape: 'decision' },
    { id: 'C', label: 'Fim', shape: 'terminal' },
    { id: 'D', label: 'Linha 1\nLinha 2', shape: 'step' },
  ])
  expect(graph.edges.map(edge => [edge.source, edge.target, edge.label])).toEqual([['A', 'B', ''], ['B', 'C', 'sim'], ['B', 'D', 'não']])
})

it('refuses what is not a flowchart, and Mermaid it cannot read, with the reason', async () => {
  await expect(readFlow('sequenceDiagram\n  A->>B: oi')).rejects.toThrow(/flowchart/)
  await expect(readFlow('flowchart TD\n  A[sem fim')).rejects.toThrow()
})

it('lays the flow out along its direction without overlapping steps', async () => {
  const down = layoutFlow(await readFlow('flowchart TD\n  A[Abrir] --> B[Filtrar] --> C[Exportar]'))
  const y = Object.fromEntries(down.nodes.map(node => [node.id, node.position.y]))
  expect(y.A).toBeLessThan(y.B); expect(y.B).toBeLessThan(y.C)
  const across = layoutFlow(await readFlow('flowchart LR\n  A[Abrir] --> B[Filtrar]'))
  const [a, b] = across.nodes
  expect(a.position.x + (a.width ?? 0)).toBeLessThanOrEqual(b.position.x)
  const branches = layoutFlow(await readFlow('flowchart TD\n  A{Válido?} -->|sim| B[Mostrar]\n  A -->|não| C[Avisar]'))
  const [, yes, no] = branches.nodes
  expect(Math.abs(yes.position.x - no.position.x)).toBeGreaterThanOrEqual(yes.width ?? 0)
  expect(branches.edges.find(edge => edge.target === 'C')?.label).toBe('não')
})
