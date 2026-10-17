import { memo, useEffect, useMemo, useRef, useState } from 'react'
import { Background, BackgroundVariant, Controls, Handle, MarkerType, Position, ReactFlow, ReactFlowProvider, type Edge, type Node, type NodeProps } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { Code2, Maximize2, Sparkles, Workflow, X } from 'lucide-react'
import { useT } from '../../../i18n'
import { useTheme } from '../../../state/theme'
import { layoutFlow, readFlow, type FlowDirection, type PlacedFlow, type PlacedFlowNode } from './flowGraph'

type Read = { state: 'loading' } | { state: 'ready'; flow: PlacedFlow } | { state: 'invalid'; error: string }
type StepData = { label: string; shape: PlacedFlowNode['shape']; direction: FlowDirection }

const hidden = { opacity: 0, width: 1, height: 1, minWidth: 1, minHeight: 1, border: 0 }
const entry: Record<FlowDirection, Position> = { TB: Position.Top, BT: Position.Bottom, LR: Position.Left, RL: Position.Right }
const exit: Record<FlowDirection, Position> = { TB: Position.Bottom, BT: Position.Top, LR: Position.Right, RL: Position.Left }

/** One step of the flow. The label is React text, so whatever the Mermaid source holds, nothing in it becomes markup. */
const StepNode = memo(({ data }: NodeProps<Node<StepData>>) => <div className={`flow-shape flow-shape-${data.shape}`}>
  <Handle type="target" position={entry[data.direction]} style={hidden} isConnectable={false} />
  <span className="flow-shape-label">{data.label}</span>
  <Handle type="source" position={exit[data.direction]} style={hidden} isConnectable={false} />
</div>)
const nodeTypes = { step: StepNode }

function useFlow(source: string) {
  const [read, setRead] = useState<Read>({ state: 'loading' })
  useEffect(() => {
    let live = true
    setRead({ state: 'loading' })
    readFlow(source).then(graph => { if (live) setRead({ state: 'ready', flow: layoutFlow(graph) }) }, failure => {
      if (live) setRead({ state: 'invalid', error: failure instanceof Error ? failure.message : String(failure) })
    })
    return () => { live = false }
  }, [source])
  return read
}

/** Where the flow starts on screen: as wide as the box allows (never smaller than readable, never above 100%), centered
 * across and from its first step down, so a long flow reads from the top and the rest is a drag away. */
export function initialViewport(flow: PlacedFlow, box: { width: number }) {
  const left = Math.min(...flow.nodes.map(node => node.position.x)), right = Math.max(...flow.nodes.map(node => node.position.x + node.width))
  const top = Math.min(...flow.nodes.map(node => node.position.y))
  const width = Math.max(1, right - left)
  const zoom = Math.min(1, Math.max(0.6, (box.width - 32) / width))
  return { zoom, x: Math.max(16, (box.width - width * zoom) / 2) - left * zoom, y: 16 - top * zoom }
}

function Canvas({ flow, label }: { flow: PlacedFlow; label: string }) {
  const [theme] = useTheme()
  const host = useRef<HTMLDivElement>(null)
  const nodes = useMemo<Node<StepData>[]>(() => flow.nodes.map(node => ({ id: node.id, type: 'step', position: node.position, data: { label: node.label, shape: node.shape, direction: flow.direction }, style: { width: node.width, height: node.height }, draggable: false, selectable: false })), [flow])
  const edges = useMemo<Edge[]>(() => flow.edges.map(edge => ({ id: edge.id, source: edge.source, target: edge.target, label: edge.label || undefined, type: 'smoothstep', markerEnd: { type: MarkerType.ArrowClosed }, className: edge.dashed ? 'flow-edge-dashed' : undefined, labelBgPadding: [6, 3] as [number, number], labelBgBorderRadius: 4 })), [flow])
  return <div ref={host} className="flow-canvas">
    <ReactFlow nodes={nodes} edges={edges} nodeTypes={nodeTypes} onInit={instance => { if (flow.nodes.length) void instance.setViewport(initialViewport(flow, { width: host.current?.clientWidth || 360 })) }} minZoom={0.2} maxZoom={2}
      nodesDraggable={false} nodesConnectable={false} elementsSelectable={false} zoomOnDoubleClick={false} proOptions={{ hideAttribution: true }} colorMode={theme} aria-label={label}>
      <Background variant={BackgroundVariant.Dots} gap={18} size={1} />
      <Controls showInteractive={false} position="bottom-right" />
    </ReactFlow>
  </div>
}

/** The application flow: the Mermaid text drawn as the app's own diagram (zoom and drag), or the reason it cannot be
 * drawn and a way to have the AI fix it. */
export function FlowView({ source, onAskFix }: { source: string; onAskFix(error: string): void }) {
  const t = useT()
  const read = useFlow(source)
  const [code, setCode] = useState(false), [large, setLarge] = useState(false)
  return <div className="flow-view">
    <div className="flow-view-tools">
      {read.state === 'ready' && !code && <button type="button" className="touch-target" onClick={() => setLarge(true)}><Maximize2 aria-hidden="true" />{t('Ampliar')}</button>}
      <button type="button" className="touch-target" onClick={() => setCode(value => !value)}>{code ? <><Workflow aria-hidden="true" />{t('Ver fluxo')}</> : <><Code2 aria-hidden="true" />{t('Ver código')}</>}</button>
    </div>
    {code ? <pre className="mono flow-source">{source}</pre>
      : read.state === 'loading' ? <p className="muted flow-loading" role="status">{t('Desenhando o fluxo…')}</p>
      : read.state === 'invalid' ? <div className="flow-invalid" role="alert"><p>{t('O fluxo não pôde ser desenhado: {error}', { error: read.error })}</p><button type="button" className="touch-target secondary-button" onClick={() => onAskFix(read.error)}><Sparkles aria-hidden="true" />{t('Pedir correção à IA')}</button></div>
      : <ReactFlowProvider><Canvas flow={read.flow} label={t('Fluxo')} /></ReactFlowProvider>}
    {large && read.state === 'ready' && <div className="brainstorm-dialog" role="dialog" aria-modal="true" aria-label={t('Fluxo')} onKeyDown={event => { if (event.key === 'Escape') setLarge(false) }}>
      <header><strong>{t('Fluxo')}</strong><span className="muted">{t('Arraste para mover.')}</span><button type="button" className="touch-target secondary-button" onClick={() => setLarge(false)} autoFocus><X aria-hidden="true" />{t('Fechar')}</button></header>
      <ReactFlowProvider><Canvas flow={read.flow} label={t('Fluxo ampliado')} /></ReactFlowProvider>
    </div>}
  </div>
}
