import type { AgentEvent } from '../../lib/backend'
import { t } from '../../i18n'

export type StepStatus = 'pending' | 'in_progress' | 'completed'
export type ActionStatus = 'waiting' | 'running' | 'approval' | 'done' | 'failed'
export type ActionKind = 'read' | 'write' | 'search' | 'shell' | 'web' | 'tool' | 'agent'

export type PlanStep = { text: string; status: StepStatus }
export type Action = { id: string; kind: ActionKind; label: string; detail?: string; status: ActionStatus; note?: string; step: number }
export type Outcome = { status: 'completed' | 'failed' | 'cancelled' | 'interrupted'; label: string }

/** What the agent is doing for the latest request: the plan it published, the actions under each step and how it ended. */
export type ActivityFlow = {
  request?: string
  plan: PlanStep[]
  /** Where the plan came from, to say so in the panel. */
  planSource?: 'harflex' | 'codex' | 'claude' | 'checklist'
  explanation?: string
  actions: Action[]
  outcome?: Outcome
}

/** Actions before the first plan, or without one, hang from the request. */
export const beforePlan = -1

type Data = Record<string, unknown>
const record = (value: unknown): Data => value && typeof value === 'object' && !Array.isArray(value) ? value as Data : {}
const text = (value: unknown) => typeof value === 'string' ? value : ''
const list = (value: unknown): unknown[] => Array.isArray(value) ? value : []

const basename = (path: string) => path.split(/[\\/]/).filter(Boolean).pop() ?? path
function shorten(value: string, max = 140) {
  const flat = value.replace(/\s+/g, ' ').trim()
  return flat.length > max ? `${flat.slice(0, max - 1)}…` : flat
}

// Codex wraps commands as `/bin/zsh -lc '…'`; the person wants to read the command itself.
export function unwrapCommand(command: string) {
  const match = /^\S*\/?(?:ba|z)?sh\s+-l?c\s+(['"])([\s\S]*)\1$/.exec(command.trim())
  return (match ? match[2] : command).replace(/^rtk (?:proxy )?/, '')
}

function describePath(path: string) { return path ? basename(path) : '' }

// The last folders are enough to tell files apart; the conversation keeps the full path.
export function shortPath(path: string) {
  const parts = path.split(/[\\/]/).filter(Boolean)
  return parts.length > 3 ? `…/${parts.slice(-3).join('/')}` : path
}
const pathDetail = (path: string) => path ? shortPath(path) : undefined

/** Turns a tool name and its arguments into a short sentence in Portuguese. */
export function describeTool(rawName: string, args: unknown): { kind: ActionKind; label: string; detail?: string } {
  // Claude Code names MCP tools mcp__<server>__<tool>; Harflex's own read like the built-in ones.
  const name = rawName.startsWith('mcp__harflex__') ? rawName.slice('mcp__harflex__'.length) : rawName
  const input = record(args)
  const path = text(input.path) || text(input.file_path) || text(input.notebook_path)
  switch (name) {
    case 'read': case 'Read': case 'NotebookRead':
      return { kind: 'read', label: path ? t('Lendo {file}', { file: describePath(path) }) : t('Lendo um arquivo'), detail: pathDetail(path) }
    case 'write': case 'Write':
      return { kind: 'write', label: path ? t('Escrevendo {file}', { file: describePath(path) }) : t('Escrevendo um arquivo'), detail: pathDetail(path) }
    case 'edit': case 'Edit': case 'MultiEdit': case 'NotebookEdit':
      return { kind: 'write', label: path ? t('Editando {file}', { file: describePath(path) }) : t('Editando um arquivo'), detail: pathDetail(path) }
    case 'ls': case 'LS':
      return { kind: 'search', label: path ? t('Listando {file}', { file: describePath(path) }) : t('Listando a pasta do projeto'), detail: pathDetail(path) }
    case 'find': case 'Glob': {
      const pattern = text(input.pattern) || text(input.glob) || text(input.name)
      return { kind: 'search', label: pattern ? t('Procurando {pattern}', { pattern: shorten(pattern, 60) }) : t('Procurando arquivos') }
    }
    case 'grep': case 'Grep': {
      const query = text(input.query) || text(input.pattern)
      return { kind: 'search', label: query ? t('Buscando “{query}”', { query: shorten(query, 60) }) : t('Buscando no código') }
    }
    case 'knowledge_search': {
      const query = text(input.query)
      return { kind: 'search', label: query ? t('Consultando a base: “{query}”', { query: shorten(query, 60) }) : t('Consultando a base de conhecimento') }
    }
    case 'bash': case 'powershell': case 'Bash': {
      const command = unwrapCommand(text(input.command))
      const description = text(input.description)
      return { kind: 'shell', label: description ? shorten(description, 90) : t('Rodando um comando'), detail: command ? shorten(command, 220) : undefined }
    }
    case 'WebFetch': case 'WebSearch': {
      const target = text(input.url) || text(input.query)
      return { kind: 'web', label: name === 'WebFetch' ? t('Lendo uma página') : t('Pesquisando na web'), detail: target ? shorten(target, 160) : undefined }
    }
    case 'harflex_create_pipeline': {
      const title = /^#\s*(.+)$/m.exec(text(input.discovery))?.[1]
      return { kind: 'agent', label: t('Criando um pipeline SDD'), detail: title ? shorten(title, 120) : undefined }
    }
    case 'harflex_list_pipelines':
      return { kind: 'search', label: t('Consultando os pipelines do projeto') }
    case 'harflex_get_pipeline':
      return { kind: 'search', label: input.includeDocuments === true ? t('Lendo os documentos do pipeline') : t('Consultando um pipeline') }
    case 'Task': case 'Agent':
      return { kind: 'agent', label: text(input.description) ? t('Delegando: {description}', { description: shorten(text(input.description), 80) }) : t('Delegando uma tarefa') }
    default: {
      const title = text(input.title) || text(input.description)
      return { kind: 'tool', label: title ? shorten(title, 90) : t('Usando {name}', { name }), detail: title ? name : undefined }
    }
  }
}

function harflexPlan(args: unknown): { steps: PlanStep[]; explanation?: string } | undefined {
  const input = record(args)
  const steps = list(input.plan).map(item => record(item)).map(item => ({ text: shorten(text(item.step), 300), status: stepStatus(item.status) })).filter(item => item.text)
  return steps.length ? { steps, explanation: text(input.explanation) || undefined } : undefined
}

function stepStatus(value: unknown): StepStatus {
  return value === 'completed' || value === 'in_progress' ? value : 'pending'
}

// Codex's todo list only says done or not; the first open item is the one being worked on.
function codexPlan(items: unknown[]): PlanStep[] {
  let active = false
  return items.map(item => record(item)).filter(item => text(item.text)).map(item => {
    if (item.completed === true) return { text: shorten(text(item.text), 300), status: 'completed' as const }
    const status: StepStatus = active ? 'pending' : 'in_progress'
    active = true
    return { text: shorten(text(item.text), 300), status }
  })
}

function claudePlan(todos: unknown[]): PlanStep[] {
  return todos.map(item => record(item)).filter(item => text(item.content)).map(item => ({ text: shorten(text(item.content), 300), status: stepStatus(item.status) }))
}

const checklistLine = /^\s*(?:[-*+]\s+)?(?:\d+[.)]\s+)?\[([ xX~-])\]\s+(.+?)\s*$/
const workingHint = /\s*\((?:em andamento|fazendo agora|in progress)\)\s*$/i

// Without a plan tool (Claude Code in print mode has none), the agent writes its plan as a Markdown checklist.
export function checklistPlan(content: string): PlanStep[] {
  const steps: PlanStep[] = []
  let working = false
  for (const line of content.split('\n')) {
    const match = checklistLine.exec(line)
    if (!match) continue
    const raw = match[2].replace(/^\d+[.)]\s+/, '')
    const hinted = workingHint.test(raw) || match[1] === '~' || match[1] === '-'
    const label = shorten(raw.replace(workingHint, '').replace(/[*_`]/g, ''), 300)
    if (!label) continue
    if (match[1].toLowerCase() === 'x') steps.push({ text: label, status: 'completed' })
    else { steps.push({ text: label, status: hinted ? 'in_progress' : 'pending' }); working ||= hinted }
  }
  if (steps.length < 2) return []
  if (!working) { const next = steps.find(step => step.status === 'pending'); if (next) next.status = 'in_progress' }
  return steps
}

// Meta tools that only look up other tools say nothing about the work.
const metaTools = new Set(['ToolSearch'])

function activeStep(plan: PlanStep[]) {
  const working = plan.findIndex(item => item.status === 'in_progress')
  if (working >= 0) return working
  const next = plan.findIndex(item => item.status === 'pending')
  return next >= 0 ? next : plan.length - 1
}

const terminal: Record<string, Outcome> = {
  'run.completed': { status: 'completed', label: 'Concluído' },
  'external.run.completed': { status: 'completed', label: 'Concluído' },
  'run.failed': { status: 'failed', label: 'A execução falhou' },
  'external.run.failed': { status: 'failed', label: 'A execução falhou' },
  'run.cancelled': { status: 'cancelled', label: 'Cancelado' },
  'external.run.cancelled': { status: 'cancelled', label: 'Cancelado' },
  'run.interrupted': { status: 'interrupted', label: 'Interrompido' },
  'external.run.interrupted': { status: 'interrupted', label: 'Interrompido' },
}

const fileName = /[\w.-]+\.[A-Za-z0-9]{1,8}/g

// A checklist is often written only at the start and the end, so every action lands on the first step. A file the
// action touched that only one started step names moves the action forward to that step.
function attachByFile(flow: ActivityFlow) {
  const steps = flow.plan.map(step => new Set((step.text.match(fileName) ?? []).map(name => name.toLowerCase())))
  for (const action of flow.actions) {
    const names = `${action.label} ${action.detail ?? ''}`.match(fileName)?.map(name => name.toLowerCase()) ?? []
    if (!names.length) continue
    const candidates = flow.plan.map((step, index) => index).filter(index => index >= action.step && flow.plan[index].status !== 'pending' && names.some(name => steps[index].has(name)))
    if (candidates.length === 1) action.step = candidates[0]
  }
}

/** Builds the flow of the latest request from the session's journal. */
export function buildActivityFlow(events: AgentEvent[]): ActivityFlow {
  let start = 0
  for (let index = events.length - 1; index >= 0; index--) if (events[index].type === 'message.user') { start = index; break }
  const flow: ActivityFlow = { plan: [], actions: [] }
  const actions = new Map<string, Action>()
  let step = beforePlan
  const setPlan = (steps: PlanStep[], source: ActivityFlow['planSource'], explanation?: string) => {
    flow.plan = steps
    flow.planSource = source
    flow.explanation = explanation
    step = activeStep(steps)
  }
  // A checklist in the agent's words counts only while no plan tool spoke.
  const setChecklist = (content: string) => {
    if (flow.planSource && flow.planSource !== 'checklist') return
    const steps = checklistPlan(content)
    if (steps.length) setPlan(steps, 'checklist')
  }
  const upsert = (id: string, make: () => Omit<Action, 'id' | 'step' | 'status'>, status: ActionStatus, note?: string) => {
    let action = actions.get(id)
    if (!action) {
      action = { id, ...make(), status, step }
      actions.set(id, action)
      flow.actions.push(action)
    } else action.status = status
    if (note !== undefined) action.note = note
    return action
  }
  const update = (id: string, status: ActionStatus, note?: string) => {
    const action = actions.get(id)
    if (!action) return
    // A denial stays the visible cause of the skipped call.
    if (action.status === 'failed' && status === 'failed' && action.note) return
    action.status = status
    if (note !== undefined) action.note = note
  }

  for (const event of events.slice(start)) {
    const data = record(event.data)
    switch (event.type) {
      case 'message.user':
        flow.request = shorten(text(data.content), 400)
        break
      case 'message.assistant':
        setChecklist(text(data.content))
        for (const raw of list(data.toolCalls)) {
          const call = record(raw)
          const id = text(call.id)
          const name = text(call.name)
          if (!id || !name) continue
          if (metaTools.has(name)) continue
          if (name === 'update_plan') {
            const plan = harflexPlan(call.arguments)
            if (plan) setPlan(plan.steps, 'harflex', plan.explanation)
            actions.set(id, { id, kind: 'tool', label: '', status: 'done', step })
            continue
          }
          upsert(id, () => describeTool(name, call.arguments), 'waiting')
        }
        break
      case 'tool.called': update(text(data.toolCallId), 'running'); break
      case 'approval.requested': update(text(data.toolCallId), 'approval', t('Aprovação solicitada')); break
      case 'approval.approved': update(text(data.toolCallId), 'waiting', ''); break
      case 'approval.denied': update(text(data.toolCallId), 'failed', t('Aprovação negada')); break
      case 'tool.completed': update(text(data.toolCallId), 'done'); break
      // Raw tool errors stay in the conversation and the audit export; the flow says what happened in plain words.
      case 'tool.failed': update(text(data.toolCallId), 'failed', data.errorCode === 'outcome_unknown' ? t('Resultado desconhecido') : t('Falhou')); break
      case 'tool.denied': update(text(data.toolCallId), 'failed', t('Negada')); break
      case 'tool.skipped': update(text(data.toolCallId), 'failed', t('Não executada')); break
      case 'external.event':
        if (data.type === 'assistant.message') setChecklist(text(data.text))
        externalEvent(record(data.raw))
        break
      default:
        if (terminal[event.type]) {
          flow.outcome = { status: terminal[event.type].status, label: t(terminal[event.type].label) }
          for (const action of flow.actions) if (action.status === 'running' || action.status === 'waiting' || action.status === 'approval') action.status = flow.outcome.status === 'completed' ? 'done' : 'failed'
        }
    }
  }
  if (flow.planSource === 'checklist') attachByFile(flow)
  // Steps that never received a plan entry keep their place.
  for (const action of flow.actions) if (action.step >= flow.plan.length) action.step = flow.plan.length ? flow.plan.length - 1 : beforePlan
  return flow

  function externalEvent(raw: Data) {
    const type = text(raw.type)
    // Codex: item.started / item.updated / item.completed.
    if (type === 'item.started' || type === 'item.updated' || type === 'item.completed') {
      const item = record(raw.item)
      const id = `codex:${text(item.id)}`
      const done = type === 'item.completed'
      switch (text(item.type)) {
        case 'todo_list': {
          const plan = codexPlan(list(item.items))
          if (plan.length) setPlan(plan, 'codex')
          return
        }
        case 'command_execution': {
          const command = unwrapCommand(text(item.command))
          const exit = typeof item.exit_code === 'number' ? item.exit_code : undefined
          const failed = text(item.status) === 'failed' || (done && exit !== undefined && exit !== 0)
          upsert(id, () => ({ kind: 'shell', label: t('Rodando um comando'), detail: shorten(command, 220) }), failed ? 'failed' : done ? 'done' : 'running', failed && exit !== undefined ? t('Saiu com código {code}', { code: exit }) : undefined)
          return
        }
        case 'file_change': {
          const changes = list(item.changes).map(change => record(change))
          const names = changes.map(change => basename(text(change.path))).filter(Boolean)
          const adding = changes.length > 0 && changes.every(change => text(change.kind) === 'add')
          const verb = adding ? t('Criando') : t('Alterando')
          const label = names.length === 1 ? t('{verb} {file}', { verb, file: names[0] }) : t('{verb} {count} arquivos', { verb, count: names.length })
          upsert(id, () => ({ kind: 'write', label, detail: names.length > 1 ? shorten(names.join(', '), 200) : pathDetail(text(changes[0]?.path)) }), text(item.status) === 'failed' ? 'failed' : done ? 'done' : 'running')
          return
        }
        case 'mcp_tool_call': {
          if (text(item.server) === 'harflex') {
            upsert(id, () => describeTool(text(item.tool), item.arguments), text(item.status) === 'failed' ? 'failed' : done ? 'done' : 'running')
            return
          }
          const args = record(item.arguments)
          const title = text(args.title)
          const tool = [text(item.server), text(item.tool)].filter(Boolean).join(' · ')
          upsert(id, () => ({ kind: 'tool', label: title ? shorten(title, 90) : t('Usando {tool}', { tool: tool || t('uma ferramenta') }), detail: title ? tool : undefined }), text(item.status) === 'failed' ? 'failed' : done ? 'done' : 'running')
          return
        }
        case 'web_search':
          upsert(id, () => ({ kind: 'web', label: t('Pesquisando na web'), detail: shorten(text(item.query), 160) || undefined }), done ? 'done' : 'running')
          return
        default:
          return
      }
    }
    // Claude Code stream-json: tool_use blocks in assistant messages, tool_result blocks in user messages.
    if (type === 'assistant' || type === 'user') {
      for (const block of list(record(raw.message).content).map(item => record(item))) {
        if (block.type === 'tool_use') {
          const id = `claude:${text(block.id)}`
          const name = text(block.name)
          if (name === 'TodoWrite') {
            const plan = claudePlan(list(record(block.input).todos))
            if (plan.length) setPlan(plan, 'claude')
            actions.set(id, { id, kind: 'tool', label: '', status: 'done', step })
            continue
          }
          if (name && !metaTools.has(name)) upsert(id, () => describeTool(name, block.input), 'running')
        } else if (block.type === 'tool_result') {
          const id = `claude:${text(block.tool_use_id)}`
          update(id, block.is_error === true ? 'failed' : 'done', block.is_error === true ? 'Falhou' : undefined)
        }
      }
    }
  }
}
