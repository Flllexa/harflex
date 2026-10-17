import { Graph, layout } from '@dagrejs/dagre'

export type FlowShape = 'step' | 'decision' | 'terminal'
export type FlowNode = { id: string; label: string; shape: FlowShape }
export type FlowEdge = { id: string; source: string; target: string; label: string; dashed: boolean }
export type FlowDirection = 'TB' | 'BT' | 'LR' | 'RL'
export type FlowGraph = { direction: FlowDirection; nodes: FlowNode[]; edges: FlowEdge[] }
export type PlacedFlowNode = FlowNode & { position: { x: number; y: number }; width: number; height: number }
export type PlacedFlow = { direction: FlowDirection; nodes: PlacedFlowNode[]; edges: FlowEdge[] }

type MermaidVertex = { id: string; text?: string; type?: string }
type MermaidEdge = { start: string; end: string; text?: string; stroke?: string }
type MermaidFlowDB = { getVertices(): Map<string, MermaidVertex>; getEdges(): MermaidEdge[]; getDirection?(): string }

const terminalShapes = new Set(['stadium', 'round', 'circle', 'doublecircle', 'ellipse'])

/** A Mermaid label as plain text: line breaks kept, any other markup dropped (React shows it as text anyway). */
function plainLabel(text: string | undefined, fallback: string): string {
  const label = (text ?? '').replace(/<br\s*\/?>/gi, '\n').replace(/<[^>]*>/g, '').trim()
  return label || fallback
}

function direction(value: string | undefined): FlowDirection {
  const upper = (value ?? 'TB').toUpperCase()
  return upper === 'TD' ? 'TB' : upper === 'LR' || upper === 'RL' || upper === 'BT' ? upper : 'TB'
}

/** Mermaid is large and only this screen needs it: it loads the first time a flow is shown, and here it only reads the
 * text. The drawing is the app's own (React Flow), so no SVG from Mermaid ever enters the page. */
async function loadMermaid() {
  const { default: mermaid } = await import('mermaid')
  mermaid.initialize({ startOnLoad: false, securityLevel: 'strict' })
  return mermaid
}

/** Reads a Mermaid flowchart into its steps and arrows. Anything else (another diagram, text Mermaid cannot read) fails
 * with the reason, which the screen shows and can send to the AI to fix. */
export async function readFlow(source: string): Promise<FlowGraph> {
  const mermaid = await loadMermaid()
  const diagram = await mermaid.mermaidAPI.getDiagramFromText(source)
  if (!diagram.type.startsWith('flowchart') && diagram.type !== 'graph') throw new Error(`The diagram is "${diagram.type}"; the Brainstorm flow must be a flowchart.`)
  const db = diagram.db as unknown as MermaidFlowDB
  const nodes = [...db.getVertices().values()].map(vertex => ({ id: vertex.id, label: plainLabel(vertex.text, vertex.id), shape: vertex.type === 'diamond' || vertex.type === 'hexagon' ? 'decision' as const : terminalShapes.has(vertex.type ?? '') ? 'terminal' as const : 'step' as const }))
  const edges = db.getEdges().map((edge, index) => ({ id: `e${index}`, source: edge.start, target: edge.end, label: plainLabel(edge.text, ''), dashed: edge.stroke === 'dotted' }))
  return { direction: direction(db.getDirection?.()), nodes, edges }
}

function size(node: FlowNode) {
  const lines = node.label.split('\n')
  const longest = Math.max(...lines.map(line => line.length))
  const width = Math.min(240, Math.max(120, longest * 7.2 + 36))
  const textLines = lines.reduce((total, line) => total + Math.max(1, Math.ceil(line.length * 7.2 / (width - 36))), 0)
  const height = 28 + textLines * 18
  // A decision is drawn as a diamond: it needs room around its text.
  return node.shape === 'decision' ? { width: width + 40, height: height + 36 } : { width, height }
}

/** Places the steps along the flow's direction (dagre's layered layout), each as React Flow's top-left position. */
export function layoutFlow(graph: FlowGraph): PlacedFlow {
  const layered = new Graph({ multigraph: true })
  layered.setGraph({ rankdir: graph.direction, nodesep: 36, ranksep: 56, edgesep: 16, marginx: 8, marginy: 8 })
  layered.setDefaultEdgeLabel(() => ({}))
  const sizes = new Map(graph.nodes.map(node => [node.id, size(node)]))
  for (const node of graph.nodes) layered.setNode(node.id, sizes.get(node.id)!)
  for (const edge of graph.edges) layered.setEdge(edge.source, edge.target, edge.label ? { width: edge.label.length * 7 + 12, height: 20, labelpos: 'c' } : {}, edge.id)
  layout(layered)
  const nodes = graph.nodes.map(node => {
    const placed = layered.node(node.id) as { x: number; y: number }
    const { width, height } = sizes.get(node.id)!
    return { ...node, width, height, position: { x: placed.x - width / 2, y: placed.y - height / 2 } }
  })
  return { direction: graph.direction, nodes, edges: graph.edges }
}
