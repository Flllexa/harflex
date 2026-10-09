import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Background, BackgroundVariant, Controls, Handle, Panel, Position, ReactFlow, ReactFlowProvider, useReactFlow, type Edge, type Node, type NodeChange, type NodeProps } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { Bot, Check, Circle, CircleAlert, CircleCheck, CircleSlash, FileText, Globe, LoaderCircle, LocateFixed, MessageSquare, PencilLine, Search, ShieldQuestion, SquareTerminal, Wrench } from 'lucide-react'
import { beforePlan, type Action, type ActionKind, type ActivityFlow } from './activityFlow'
import './activityFlow.css'
import { useTheme } from '../../state/theme'
import { t, useT } from '../../i18n'

type RequestData = { text: string }
type StepData = { text: string; status: 'pending' | 'in_progress' | 'completed'; index: number; total: number; live: boolean }
type ActionData = { action: Action; live: boolean }
type MoreData = { count: number }
type OutcomeData = { status: string; label: string }

const kindIcons: Record<ActionKind, typeof FileText> = { read: FileText, write: PencilLine, search: Search, shell: SquareTerminal, web: Globe, tool: Wrench, agent: Bot }
const statusText: Record<Action['status'], string> = { waiting: 'Na fila', running: 'Em andamento', approval: 'Aguardando aprovação', done: 'Feito', failed: 'Falhou' }

const hidden = { opacity: 0, width: 1, height: 1, minWidth: 1, minHeight: 1, border: 0 }
const spine = { ...hidden, left: 14 }

const RequestNode = memo(({ data }: NodeProps<Node<RequestData>>) => {
  const t = useT()
  return <div className="flow-node flow-request">
  <span className="flow-node-eyebrow"><MessageSquare aria-hidden="true" />{t('Pedido')}</span>
  <p className="flow-node-text">{data.text}</p>
  <Handle type="source" position={Position.Bottom} style={spine} isConnectable={false} />
</div>
})

const StepNode = memo(({ data }: NodeProps<Node<StepData>>) => {
  const t = useT()
  const position = { number: data.index + 1, total: data.total }
  const Icon = data.status === 'completed' ? Check : data.status === 'in_progress' ? LoaderCircle : Circle
  return <div className={`flow-node flow-step is-${data.status}${data.live && data.status === 'in_progress' ? ' is-live' : ''}`}>
    <Handle type="target" position={Position.Top} style={spine} isConnectable={false} />
    <span className="flow-step-badge" aria-hidden="true"><Icon /></span>
    <div className="flow-step-body">
      <span className="flow-node-eyebrow">{data.status === 'in_progress' ? t('Etapa {number} de {total} · agora', position) : data.status === 'completed' ? t('Etapa {number} de {total} · feita', position) : t('Etapa {number} de {total}', position)}</span>
      <p className="flow-node-text">{data.text}</p>
    </div>
    <Handle type="source" position={Position.Bottom} style={spine} isConnectable={false} />
  </div>
})

const ActionNode = memo(({ data }: NodeProps<Node<ActionData>>) => {
  const t = useT()
  const { action } = data
  const Icon = kindIcons[action.kind]
  const StatusIcon = action.status === 'done' ? CircleCheck : action.status === 'failed' ? CircleAlert : action.status === 'approval' ? ShieldQuestion : action.status === 'running' ? LoaderCircle : Circle
  return <div className={`flow-node flow-action is-${action.status}${data.live && action.status === 'running' ? ' is-live' : ''}`}>
    <Handle type="target" position={Position.Left} style={hidden} isConnectable={false} />
    <span className="flow-action-icon" aria-hidden="true"><Icon /></span>
    <div className="flow-action-body">
      <p className="flow-action-label">{action.label}</p>
      {action.detail && <p className="flow-action-detail" title={action.detail}>{action.detail}</p>}
      {(action.note || action.status !== 'done') && <span className="flow-action-status"><StatusIcon aria-hidden="true" />{action.note || t(statusText[action.status])}</span>}
    </div>
  </div>
})

const MoreNode = memo(({ data }: NodeProps<Node<MoreData>>) => {
  const t = useT()
  return <div className="flow-node flow-more">
  <Handle type="target" position={Position.Left} style={hidden} isConnectable={false} />
  {data.count === 1 ? t('+{count} ação anterior', { count: data.count }) : t('+{count} ações anteriores', { count: data.count })}
</div>
})

const OutcomeNode = memo(({ data }: NodeProps<Node<OutcomeData>>) => {
  const Icon = data.status === 'completed' ? CircleCheck : data.status === 'failed' ? CircleAlert : CircleSlash
  return <div className={`flow-node flow-outcome is-${data.status}`}>
    <Handle type="target" position={Position.Top} style={spine} isConnectable={false} />
    <Icon aria-hidden="true" />{data.label}
  </div>
})

const nodeTypes = { request: RequestNode, step: StepNode, action: ActionNode, more: MoreNode, outcome: OutcomeNode }

const indent = 30
const gap = { spine: 14, branch: 8 }
// Before the browser measures a node, a rough height keeps the first frame from overlapping.
function estimate(type: string, text: string, width: number, extra = 0) {
  const perLine = Math.max(18, Math.floor((width - 56) / 6.8))
  const lines = Math.min(type === 'action' ? 2 : 5, Math.max(1, Math.ceil(text.length / perLine)))
  const base = type === 'step' ? 46 : type === 'request' ? 44 : type === 'action' ? 30 : 30
  return base + lines * 17 + extra
}

type Layout = { nodes: Node[]; edges: Edge[]; focus?: string }

/** A tree read top to bottom: the request, the plan's steps on the spine and each step's actions branching off it. */
function layout(flow: ActivityFlow, width: number, heights: Map<string, number>, live: boolean): Layout {
  const nodes: Node[] = []
  const edges: Edge[] = []
  let y = 0
  let focus: string | undefined
  let previousSpine: string | undefined
  const height = (id: string, fallback: number) => heights.get(id) ?? fallback
  const place = (node: Node, h: number, spacing: number) => { nodes.push(node); y += h + spacing }
  const link = (source: string, target: string, kind: 'spine' | 'branch', active = false) => edges.push({
    id: `${source}->${target}`, source, target, type: 'smoothstep', className: `flow-edge is-${kind}${active ? ' is-active' : ''}`,
    animated: active, pathOptions: { borderRadius: 10 },
  } as Edge)
  const spineNode = (node: Node, h: number, active = false) => {
    if (previousSpine) link(previousSpine, node.id, 'spine', active)
    previousSpine = node.id
    place(node, h, gap.spine)
  }
  const branch = (parent: string, actions: Action[], keep: number) => {
    const shown = actions.slice(-keep)
    const hiddenCount = actions.length - shown.length
    const actionWidth = width - indent
    if (hiddenCount > 0) {
      const id = `more:${parent}`
      place({ id, type: 'more', position: { x: indent, y }, data: { count: hiddenCount }, style: { width: actionWidth }, draggable: false, selectable: false }, height(id, 30), gap.branch)
      link(parent, id, 'branch')
    }
    for (const action of shown) {
      const id = `action:${action.id}`
      const extra = (action.detail ? 17 : 0) + (action.note || action.status !== 'done' ? 18 : 0)
      place({ id, type: 'action', position: { x: indent, y }, data: { action, live }, style: { width: actionWidth }, draggable: false, selectable: false }, height(id, estimate('action', action.label, actionWidth, extra)), gap.branch)
      link(parent, id, 'branch', live && action.status === 'running')
      if (action.status === 'running' || action.status === 'approval') focus = id
    }
    if (shown.length || hiddenCount) y += gap.spine - gap.branch
  }

  const requestText = flow.request || t('Trabalho em andamento')
  spineNode({ id: 'request', type: 'request', position: { x: 0, y }, data: { text: requestText }, style: { width }, draggable: false, selectable: false }, height('request', estimate('request', requestText, width)))
  const loose = flow.actions.filter(action => action.step === beforePlan)
  const activeIndex = flow.plan.findIndex(step => step.status === 'in_progress')
  branch('request', loose, flow.plan.length ? 3 : 10)
  flow.plan.forEach((step, index) => {
    const id = `step:${index}`
    spineNode({ id, type: 'step', position: { x: 0, y }, data: { text: step.text, status: step.status, index, total: flow.plan.length, live }, style: { width }, draggable: false, selectable: false },
      height(id, estimate('step', step.text, width)), live && index === activeIndex)
    if (step.status === 'in_progress' && !focus) focus = id
    branch(id, flow.actions.filter(action => action.step === index), step.status === 'in_progress' ? 8 : 4)
  })
  if (flow.outcome) {
    spineNode({ id: 'outcome', type: 'outcome', position: { x: 0, y }, data: flow.outcome, style: { width }, draggable: false, selectable: false }, height('outcome', 40))
    focus = 'outcome'
  }
  if (!focus && live) focus = nodes[nodes.length - 1]?.id
  return { nodes, edges, focus }
}

type Props = { flow: ActivityFlow; live: boolean }

function FlowCanvas({ flow, live }: Props) {
  const t = useT()
  const host = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(280)
  const [heights, setHeights] = useState(() => new Map<string, number>())
  const [follow, setFollow] = useState(true)
  const { setCenter, getZoom, getNode } = useReactFlow()
  const [theme] = useTheme()

  useEffect(() => {
    const element = host.current
    if (!element || typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(([entry]) => {
      const next = Math.round(Math.min(560, Math.max(220, entry.contentRect.width - 40)))
      setWidth(current => Math.abs(current - next) > 4 ? next : current)
    })
    observer.observe(element)
    return () => observer.disconnect()
  }, [])

  const { nodes, edges, focus } = useMemo(() => layout(flow, width, heights, live), [flow, width, heights, live])

  // The browser's measurements replace the estimates; a changed height moves everything below it.
  const onNodesChange = useCallback((changes: NodeChange[]) => {
    setHeights(current => {
      let next: Map<string, number> | undefined
      for (const change of changes) {
        if (change.type !== 'dimensions' || !change.dimensions) continue
        const value = Math.round(change.dimensions.height)
        if (value > 0 && current.get(change.id) !== value) { next ??= new Map(current); next.set(change.id, value) }
      }
      return next ?? current
    })
  }, [])

  // The camera follows what is happening now, until the person moves the canvas.
  useEffect(() => {
    if (!follow || !focus) return
    const timer = window.setTimeout(() => {
      const node = getNode(focus)
      if (!node) return
      const h = node.measured?.height ?? heights.get(focus) ?? 60
      const zoom = getZoom() || 1
      const element = host.current
      const visibleHeight = element ? element.clientHeight / zoom : 400
      // Keep the focus in the lower third so the steps above stay in view.
      const centerY = node.position.y + h - visibleHeight * 0.18
      void setCenter(width / 2, Math.max(centerY, visibleHeight / 2 - 16), { zoom, duration: 280 })
    }, 60)
    return () => window.clearTimeout(timer)
  }, [follow, focus, nodes, width, getNode, getZoom, setCenter, heights])

  return <div ref={host} className="activity-flow-canvas">
    <ReactFlow nodes={nodes} edges={edges} nodeTypes={nodeTypes} onNodesChange={onNodesChange} defaultViewport={{ x: 20, y: 16, zoom: 1 }}
      minZoom={0.35} maxZoom={1.6} nodesDraggable={false} nodesConnectable={false} elementsSelectable={false} proOptions={{ hideAttribution: true }}
      onMoveStart={event => { if (event) setFollow(false) }} zoomOnDoubleClick={false} colorMode={theme} aria-label={t('Fluxo do plano de execução')}>
      <Background variant={BackgroundVariant.Dots} gap={18} size={1} className="activity-flow-bg" />
      <Controls showInteractive={false} position="bottom-right" className="activity-flow-controls" />
      {!follow && <Panel position="top-right"><button type="button" className="activity-flow-follow" onClick={() => setFollow(true)}><LocateFixed aria-hidden="true" />{t('Acompanhar')}</button></Panel>}
    </ReactFlow>
  </div>
}

export function ActivityFlowView(props: Props) {
  return <ReactFlowProvider><FlowCanvas {...props} /></ReactFlowProvider>
}
