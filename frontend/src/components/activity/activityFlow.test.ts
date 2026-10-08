import { expect, it } from 'vitest'
import type { AgentEvent } from '../../lib/backend'
import { beforePlan, buildActivityFlow, describeTool, unwrapCommand } from './activityFlow'

let sequence = 0
const event = (type: string, data: unknown = {}): AgentEvent => ({ id: `e${++sequence}`, streamId: 's', sequence, type, data, createdAt: '2026-10-04T10:00:00Z' })
const call = (id: string, name: string, args: unknown) => event('message.assistant', { role: 'assistant', content: '', toolCalls: [{ id, name, arguments: args }] })
const raw = (value: unknown) => event('external.event', { type: 'external.raw', raw: value })

it('hangs each action under the step that was in progress when it ran, for the latest request only', () => {
  const flow = buildActivityFlow([
    event('message.user', { role: 'user', content: 'pedido antigo' }),
    call('old', 'read', { path: 'old.txt' }),
    event('message.user', { role: 'user', content: 'Corrija a validação' }),
    call('p1', 'update_plan', { explanation: 'Começando', plan: [{ step: 'Ler o código', status: 'in_progress' }, { step: 'Ajustar', status: 'pending' }] }),
    call('r1', 'read', { path: 'src/form/validate.ts' }),
    event('tool.called', { toolCallId: 'r1', name: 'read' }),
    event('tool.completed', { toolCallId: 'r1', name: 'read', content: {} }),
    call('p2', 'update_plan', { plan: [{ step: 'Ler o código', status: 'completed' }, { step: 'Ajustar', status: 'in_progress' }] }),
    call('w1', 'edit', { path: 'src/form/validate.ts' }),
    event('approval.requested', { approvalId: 'a', toolCallId: 'w1', name: 'edit', risk: 'write', arguments: {} }),
  ])
  expect(flow.request).toBe('Corrija a validação')
  expect(flow.planSource).toBe('harflex')
  expect(flow.plan.map(step => step.status)).toEqual(['completed', 'in_progress'])
  expect(flow.actions.map(action => [action.label, action.step, action.status])).toEqual([
    ['Lendo validate.ts', 0, 'done'],
    ['Editando validate.ts', 1, 'approval'],
  ])
  expect(flow.actions[1].note).toBe('Aprovação solicitada')
})

it('reads the Codex todo list, commands and file changes', () => {
  const flow = buildActivityFlow([
    event('message.user', { role: 'user', content: 'faça' }),
    raw({ type: 'item.started', item: { id: 'c1', type: 'command_execution', command: "/bin/zsh -lc 'npm test'", status: 'in_progress' } }),
    raw({ type: 'item.completed', item: { id: 'c1', type: 'command_execution', command: "/bin/zsh -lc 'npm test'", exit_code: 1, status: 'failed' } }),
    raw({ type: 'item.updated', item: { id: 't', type: 'todo_list', items: [{ text: 'Rodar testes', completed: true }, { text: 'Corrigir', completed: false }, { text: 'Revisar', completed: false }] } }),
    raw({ type: 'item.completed', item: { id: 'f', type: 'file_change', changes: [{ path: '/repo/a.ts', kind: 'update' }], status: 'completed' } }),
    event('external.run.completed', { adapter: 'codex' }),
  ])
  expect(flow.planSource).toBe('codex')
  expect(flow.plan.map(step => step.status)).toEqual(['completed', 'in_progress', 'pending'])
  expect(flow.actions.map(action => [action.label, action.detail, action.step, action.status])).toEqual([
    ['Rodando um comando', 'npm test', beforePlan, 'failed'],
    ['Alterando a.ts', '/repo/a.ts', 1, 'done'],
  ])
  expect(flow.actions[0].note).toBe('Saiu com código 1')
  expect(flow.outcome).toEqual({ status: 'completed', label: 'Concluído' })
})

it('reads Claude Code TodoWrite and its tool results', () => {
  const flow = buildActivityFlow([
    event('message.user', { role: 'user', content: 'faça' }),
    raw({ type: 'assistant', message: { content: [{ type: 'tool_use', id: 'todo', name: 'TodoWrite', input: { todos: [{ content: 'Entender', status: 'in_progress', activeForm: 'Entendendo' }] } }] } }),
    raw({ type: 'assistant', message: { content: [{ type: 'tool_use', id: 'b', name: 'Bash', input: { command: 'go test ./...', description: 'Rodar os testes Go' } }] } }),
    raw({ type: 'user', message: { content: [{ type: 'tool_result', tool_use_id: 'b', is_error: true }] } }),
  ])
  expect(flow.planSource).toBe('claude')
  expect(flow.plan).toEqual([{ text: 'Entender', status: 'in_progress' }])
  expect(flow.actions).toMatchObject([{ label: 'Rodar os testes Go', detail: 'go test ./...', status: 'failed', step: 0 }])
})

it('describes tools in plain words', () => {
  expect(describeTool('grep', { query: 'TODO' }).label).toBe('Buscando “TODO”')
  expect(describeTool('bash', { command: 'make build' })).toMatchObject({ kind: 'shell', label: 'Rodando um comando', detail: 'make build' })
  expect(describeTool('mcp_github_search', {}).label).toBe('Usando mcp_github_search')
  expect(unwrapCommand("/bin/zsh -lc 'rtk git status'")).toBe('git status')
})

it('reads the plan from a Markdown checklist when no plan tool is available, until a plan tool speaks', () => {
  const flow = buildActivityFlow([
    event('message.user', { role: 'user', content: 'faça' }),
    event('external.event', { type: 'assistant.message', text: 'Plano\n\n- [x] 1. Ler o README\n- [ ] 2. Criar `index.html` (em andamento)\n- [ ] 3. Testar', messageId: 'm', mode: 'replace' }),
    raw({ type: 'assistant', message: { content: [{ type: 'tool_use', id: 's', name: 'ToolSearch', input: { query: 'select:TodoWrite' } }, { type: 'tool_use', id: 'w', name: 'Write', input: { file_path: '/repo/index.html' } }] } }),
  ])
  expect(flow.planSource).toBe('checklist')
  expect(flow.plan).toEqual([{ text: 'Ler o README', status: 'completed' }, { text: 'Criar index.html', status: 'in_progress' }, { text: 'Testar', status: 'pending' }])
  expect(flow.actions).toMatchObject([{ label: 'Escrevendo index.html', step: 1 }])

  const tool = buildActivityFlow([
    event('message.user', { role: 'user', content: 'faça' }),
    call('p', 'update_plan', { plan: [{ step: 'Único passo', status: 'in_progress' }] }),
    event('message.assistant', { role: 'assistant', content: '- [ ] a\n- [ ] b' }),
  ])
  expect(tool.planSource).toBe('harflex')
  expect(tool.plan).toHaveLength(1)
})

it('moves an action forward to the started checklist step that names its file, and shortens long paths', () => {
  const flow = buildActivityFlow([
    event('message.user', { role: 'user', content: 'faça' }),
    event('message.assistant', { role: 'assistant', content: '- [ ] Revisar app.js\n- [ ] Criar style.css\n- [ ] Publicar' }),
    call('w', 'write', { path: '/Users/dev/projetos/todo-app/src/style.css' }),
    call('r', 'read', { path: 'app.js' }),
    event('message.assistant', { role: 'assistant', content: '- [x] Revisar app.js\n- [x] Criar style.css\n- [ ] Publicar' }),
  ])
  expect(flow.actions.map(action => [action.label, action.detail, action.step])).toEqual([
    ['Escrevendo style.css', '…/todo-app/src/style.css', 1],
    ['Lendo app.js', 'app.js', 0],
  ])
})

it('names the Harflex tools the same way from an API agent, Codex and Claude Code', () => {
  expect(describeTool('harflex_create_pipeline', { discovery: '# Baixa parcial\n\nContexto' })).toMatchObject({ label: 'Criando um pipeline SDD', detail: 'Baixa parcial' })
  expect(describeTool('mcp__harflex__harflex_list_pipelines', {}).label).toBe('Consultando os pipelines do projeto')
  const flow = buildActivityFlow([
    event('message.user', { role: 'user', content: 'crie o pipeline' }),
    raw({ type: 'item.completed', item: { id: 'm', type: 'mcp_tool_call', server: 'harflex', tool: 'harflex_get_pipeline', arguments: { pipelineId: 'p', includeDocuments: true }, status: 'completed' } }),
  ])
  expect(flow.actions).toMatchObject([{ label: 'Lendo os documentos do pipeline', status: 'done' }])
})
