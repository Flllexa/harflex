import type { AgentEvent } from '../../lib/backend'
import { t } from '../../i18n'
import { buildActivityFlow, type Action, type ActionKind } from './activityFlow'

export type RunPhase = 'waiting' | 'acting' | 'writing' | 'approval' | 'completed' | 'failed' | 'cancelled' | 'interrupted'
export type TokenUsage = { input: number; output: number }

/**
 * What an AI run is doing, told from the journal's objective events only: the step it is on, what it has done, the
 * files it read or changed, when it started and the tokens the provider reported. The model's own text (and any
 * reasoning it may carry) never feeds the step; only tool calls, lifecycle events and the size of the answer do.
 */
export type RunProgress = {
  live: boolean
  phase: RunPhase
  /** One objective sentence: "Lendo session.go", "Aguardando o modelo", "Concluído". */
  step: string
  /** The plan step in progress, when the agent published a plan. */
  planStep?: { index: number; total: number; text: string }
  startedAt?: number
  endedAt?: number
  /** When the run's latest event was recorded, the end of a run whose terminal event was lost. */
  lastAt?: number
  counts: Record<ActionKind, number> & { failed: number; total: number }
  /** The latest actions, newest last. */
  recent: Action[]
  filesRead: string[]
  filesChanged: string[]
  /** Summed over the run's model calls; undefined while the provider has reported nothing. */
  tokens?: TokenUsage
  /** Characters of answer received so far, the only live signal of a run without tools. */
  outputChars: number
}

type Data = Record<string, unknown>
const record = (value: unknown): Data => value && typeof value === 'object' && !Array.isArray(value) ? value as Data : {}
const text = (value: unknown) => typeof value === 'string' ? value : ''
const count = (value: unknown) => typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : undefined

const starts = new Set(['run.started', 'external.run.started'])
const terminals: Record<string, RunPhase> = {
  'run.completed': 'completed', 'external.run.completed': 'completed',
  'run.failed': 'failed', 'external.run.failed': 'failed',
  'run.cancelled': 'cancelled', 'external.run.cancelled': 'cancelled',
  'run.interrupted': 'interrupted', 'external.run.interrupted': 'interrupted',
}
const outcomeText: Record<string, string> = { completed: 'Concluído', failed: 'Falhou', cancelled: 'Cancelado', interrupted: 'Interrompido' }

const time = (event: AgentEvent) => { const value = Date.parse(event.createdAt); return Number.isFinite(value) ? value : undefined }

/** The events of the latest request: from the run start just before its message, or the message itself. */
function latestRun(events: AgentEvent[]): AgentEvent[] {
  let user = -1
  for (let index = events.length - 1; index >= 0; index--) if (events[index].type === 'message.user') { user = index; break }
  if (user < 0) {
    let start = -1
    for (let index = events.length - 1; index >= 0; index--) if (starts.has(events[index].type)) { start = index; break }
    return start < 0 ? events : events.slice(start)
  }
  // Runs record their start, then the message; anything between belongs to the same request.
  for (let index = user - 1; index >= 0 && index >= user - 3; index--) if (starts.has(events[index].type)) return events.slice(index)
  return events.slice(user)
}

/**
 * Tokens of a run. A provider may repeat its cumulative usage during one response, so each model call counts once,
 * with its last snapshot; the call ends at its assistant message or at the end of the run.
 */
export function usageTotals(events: AgentEvent[]): TokenUsage | undefined {
  let total: TokenUsage | undefined
  let pending: TokenUsage | undefined
  const commit = () => {
    if (!pending) return
    total = { input: (total?.input ?? 0) + pending.input, output: (total?.output ?? 0) + pending.output }
    pending = undefined
  }
  for (const event of events) {
    if (event.type === 'usage.recorded') {
      const data = record(event.data)
      const input = count(data.inputTokens), output = count(data.outputTokens)
      if (input !== undefined && output !== undefined) pending = { input, output }
    } else if (event.type === 'message.assistant' || event.type === 'message.user' || starts.has(event.type) || terminals[event.type]) commit()
  }
  commit()
  return total
}

const emptyCounts = (): RunProgress['counts'] => ({ read: 0, write: 0, search: 0, shell: 0, web: 0, tool: 0, agent: 0, failed: 0, total: 0 })

/** Builds the progress of the session's latest run from its journal. */
export function buildRunProgress(events: AgentEvent[]): RunProgress {
  const run = latestRun(events)
  const flow = buildActivityFlow(run)
  let startedAt: number | undefined
  let endedAt: number | undefined
  let ended: RunPhase | undefined
  let last: 'tool' | 'text' | undefined
  let outputChars = 0
  const external = new Map<string, number>()
  let lastAt: number | undefined
  for (const event of run) {
    const data = record(event.data)
    lastAt = time(event) ?? lastAt
    if (starts.has(event.type)) {
      startedAt ??= time(event)
      ended = undefined
      endedAt = undefined
    } else if (event.type === 'message.user') startedAt ??= time(event)
    else if (terminals[event.type]) { ended = terminals[event.type]; endedAt = time(event) }
    else if (event.type === 'assistant.delta') { outputChars += text(data.delta).length; last = 'text' }
    else if (event.type === 'message.assistant') last = Array.isArray(data.toolCalls) && data.toolCalls.length ? 'tool' : last
    else if (event.type.startsWith('tool.') || event.type.startsWith('approval.')) last = 'tool'
    else if (event.type === 'external.event') {
      if (data.type === 'assistant.message') {
        const id = text(data.messageId) || event.id
        const size = text(data.text).length
        const before = external.get(id) ?? 0
        const next = data.mode === 'delta' ? before + size : size
        external.set(id, next)
        outputChars += next - before
        last = 'text'
      } else {
        const raw = record(data.raw)
        const item = record(raw.item)
        if (text(item.type) && !['agent_message', 'reasoning', 'todo_list'].includes(text(item.type))) last = 'tool'
        if (raw.type === 'assistant' || raw.type === 'user') {
          const blocks = Array.isArray(record(raw.message).content) ? record(raw.message).content as unknown[] : []
          if (blocks.some(block => ['tool_use', 'tool_result'].includes(text(record(block).type)))) last = 'tool'
        }
      }
    }
  }

  const counts = emptyCounts()
  const filesRead: string[] = []
  const filesChanged: string[] = []
  const add = (list: string[], path: string) => { if (path && !list.includes(path)) list.push(path) }
  for (const action of flow.actions) {
    if (!action.label) continue
    counts[action.kind]++
    counts.total++
    if (action.status === 'failed') counts.failed++
    for (const path of action.paths ?? []) add(action.kind === 'write' ? filesChanged : filesRead, path)
  }
  const visible = flow.actions.filter(action => action.label)
  const live = run.length > 0 && !ended
  const plan = flow.plan.findIndex(step => step.status === 'in_progress')
  const planStep = plan >= 0 ? { index: plan, total: flow.plan.length, text: flow.plan[plan].text } : undefined

  let phase: RunPhase
  let step: string
  const asking = visible.find(action => action.status === 'approval')
  const running = [...visible].reverse().find(action => action.status === 'running')
  if (!live) {
    phase = ended ?? 'completed'
    step = t(outcomeText[phase] ?? 'Concluído')
  } else if (asking) {
    phase = 'approval'
    step = t('Aguardando sua aprovação: {action}', { action: asking.label })
  } else if (running) {
    phase = 'acting'
    step = running.label
  } else if (last === 'text') {
    phase = 'writing'
    step = t('Escrevendo a resposta')
  } else {
    phase = 'waiting'
    step = visible.length ? t('Aguardando o modelo') : t('Aguardando a primeira resposta do modelo')
  }

  return { live, phase, step, planStep, startedAt, endedAt, lastAt, counts, recent: visible.slice(-5), filesRead, filesChanged, tokens: usageTotals(run), outputChars }
}

/** "4 s", "2 min 05 s", "1 h 03 min". */
export function formatElapsed(ms: number): string {
  const seconds = Math.max(0, Math.floor(ms / 1000))
  if (seconds < 60) return `${seconds} s`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} min ${String(seconds % 60).padStart(2, '0')} s`
  return `${Math.floor(minutes / 60)} h ${String(minutes % 60).padStart(2, '0')} min`
}

/** "812", "12,4 mil" — short counts for tokens and characters. */
export function formatCount(value: number, locale: string): string {
  if (value < 10_000) return value.toLocaleString(locale)
  return new Intl.NumberFormat(locale, { notation: 'compact', maximumFractionDigits: 1 }).format(value)
}
