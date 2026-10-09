import type { AgentEvent, Backend, Pipeline, PullRequest, Session } from '../lib/backend'

// Demonstration data for the README screenshots: one project, one SDD work item at the stage the view asks for,
// a chat that created it and the live QA run. Nothing here talks to a real agent.
export type ShowcaseView = 'chat' | 'code' | 'qa-running' | 'qa-report' | 'prs'

const at = (minutes: number) => new Date(Date.UTC(2026, 9, 8, 14, minutes)).toISOString()
const workspaceId = 'workspace-1'

const diff = `diff --git a/index.html b/index.html
new file mode 100644
--- /dev/null
+++ b/index.html
@@ -0,0 +1,18 @@
+<!doctype html>
+<html lang="pt-BR">
+<head>
+  <meta charset="utf-8" />
+  <title>Tarefas</title>
+  <link rel="stylesheet" href="styles.css" />
+</head>
+<body>
+  <main class="todo">
+    <h1>Minhas tarefas</h1>
+    <form id="todo-form" class="todo-form">
+      <input id="task-input" aria-label="Nova tarefa" placeholder="O que precisa ser feito?" required />
+      <button type="submit">Adicionar</button>
+    </form>
+    <ul id="task-list" class="todo-list" aria-live="polite"></ul>
+  </main>
+  <script src="app.js" defer></script>
+</body>
diff --git a/app.js b/app.js
new file mode 100644
--- /dev/null
+++ b/app.js
@@ -0,0 +1,34 @@
+const KEY = 'todo.tasks'
+const form = document.querySelector('#todo-form')
+const input = document.querySelector('#task-input')
+const list = document.querySelector('#task-list')
+
+function loadTasks() {
+  try {
+    const raw = localStorage.getItem(KEY)
+    return raw === null ? [] : JSON.parse(raw)
+  } catch {
+    return []
+  }
+}
+
+let tasks = loadTasks()
+const saveTasks = () => localStorage.setItem(KEY, JSON.stringify(tasks))
+
+function render() {
+  list.replaceChildren(...tasks.map(task => {
+    const item = document.createElement('li')
+    item.className = task.done ? 'task is-done' : 'task'
+    item.textContent = task.text
+    item.addEventListener('click', () => { task.done = !task.done; saveTasks(); render() })
+    return item
+  }))
+}
+
+form.addEventListener('submit', event => {
+  event.preventDefault()
+  const text = input.value.trim()
+  if (!text) return
+  tasks.push({ id: crypto.randomUUID(), text, done: false })
+  input.value = ''
+  saveTasks(); render()
+})
diff --git a/styles.css b/styles.css
--- a/styles.css
+++ b/styles.css
@@ -1,6 +1,12 @@
 body { font-family: system-ui, sans-serif; margin: 0; }
-.todo { max-width: 480px; margin: 40px auto; }
+.todo { max-width: 520px; margin: 48px auto; padding: 0 16px; }
+.todo-form { display: flex; gap: 8px; }
+.todo-form input { flex: 1; padding: 10px 12px; border-radius: 8px; border: 1px solid #ccd; }
+.task { padding: 12px; border-bottom: 1px solid #eef; cursor: pointer; }
+.task.is-done { color: #889; text-decoration: line-through; }
diff --git a/tests/todo.test.js b/tests/todo.test.js
new file mode 100644
--- /dev/null
+++ b/tests/todo.test.js
@@ -0,0 +1,12 @@
+import { test } from 'node:test'
+import assert from 'node:assert/strict'
+import { loadTasks } from '../app.js'
+
+test('a lista começa vazia sem dados salvos', () => {
+  localStorage.clear()
+  assert.deepEqual(loadTasks(), [])
+})
+
+test('dados inválidos não quebram a página', () => {
+  localStorage.setItem('todo.tasks', '{quebrado')
+  assert.deepEqual(loadTasks(), [])
+})`

// The SPEC and Plan the preparation studio shows for the demonstration project.
export const showcaseDocuments = {
  spec: '# SPEC\n\n## Escopo\n\nUma lista de tarefas em uma página HTML, sem servidor, que guarda tudo no localStorage do navegador.\n\n## Critérios de aceite\n\n1. Ao adicionar uma descrição válida, a tarefa aparece como pendente e continua lá ao recarregar a página.\n2. Uma descrição vazia ou só com espaços não cria tarefa.\n3. Marcar uma tarefa como concluída risca o texto e sobrevive à recarga.\n4. Excluir a última tarefa mostra de novo a mensagem de lista vazia.\n5. Dados inválidos no localStorage não quebram a página.',
  plan: '# Plan\n\n## Tarefas\n\n1. **T1 · Página e formulário:** index.html com campo, botão e lista acessíveis.\n2. **T2 · Persistência:** app.js lê e grava em `todo.tasks`, tolerando dados inválidos.\n3. **T3 · Estilo:** styles.css com estados de pendente e concluída.\n4. **T4 · Testes:** unitários com node:test e E2E no Chromium com Playwright.',
}

const report = {
  passed: false,
  checks: [
    { name: 'Instalação de dependências', kind: 'other', command: 'npm ci', status: 'passed', summary: 'Dependências instaladas em 2,1 s.' },
    { name: 'Build', kind: 'build', command: 'npm run build', status: 'passed', summary: 'HTML, CSS e JavaScript empacotados sem avisos.' },
    { name: 'Testes unitários', kind: 'unit', command: 'npm test', status: 'passed', summary: '12 testes passaram; nenhum falhou.' },
    { name: 'E2E no Chromium', kind: 'e2e', command: 'npx playwright test', status: 'failed', summary: 'Excluir a última tarefa não mostra de novo a mensagem de lista vazia (tests/e2e/lista.spec.js:42).' },
    { name: 'App no ar', kind: 'run', command: 'npx serve . -l 4173 && curl -s localhost:4173', status: 'passed', summary: 'Página servida e acessível; formulário e lista presentes no DOM.' },
    { name: 'Lint', kind: 'lint', command: 'npm run lint', status: 'passed', summary: 'Nenhum problema encontrado.' },
  ],
  findings: ['Ao excluir a última tarefa, a mensagem "Nenhuma tarefa ainda" não volta a aparecer.'],
  improvements: ['Permitir editar o texto de uma tarefa com duplo clique.', 'Mostrar um contador de tarefas pendentes no rodapé.', 'Animar a entrada e a saída das tarefas da lista.'],
  criteria: [{ criterion: 'Ao adicionar uma descrição válida, a tarefa aparece como pendente', evidence: 'tasks.push({ id: crypto.randomUUID(), text, done: false })' }],
}

const artifact = (stage: 'discovery' | 'spec' | 'plan' | 'code' | 'eval', content: string, version = 1) => ({ stage, version, content, author: 'ai' as const, sourceSessionId: `${stage}-session`, contentDigest: stage.padEnd(64, '0'), updatedAt: at(10) })

function pipelineFor(view: ShowcaseView): Pipeline {
  const base: Pipeline = {
    id: 'todo-pipeline', workspaceId, kind: 'ai_authoring', preparationExperience: 'conversational', title: 'Lista de tarefas com localStorage', objective: 'Uma lista de tarefas em HTML que guarda tudo no navegador',
    currentStage: 'code', stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'waiting_user', eval: 'pending', prs: 'pending' }, revision: 12,
    artifacts: {
      discovery: artifact('discovery', '# Lista de tarefas\n\nQuero uma lista de tarefas simples, em HTML, que guarde tudo no navegador.'),
      spec: artifact('spec', '## Critérios\n1. Ao adicionar uma descrição válida, a tarefa aparece como pendente\n2. Excluir a última tarefa mostra a mensagem de lista vazia'),
      plan: artifact('plan', '## Tarefas\nT1 · Página e formulário\nT2 · Persistência no localStorage\nT3 · Testes unitários e E2E'),
      code: artifact('code', diff, 2),
    },
    createdAt: at(0), updatedAt: at(40),
  }
  // From QA on, the Code version that went to QA carries its approval, as the app records it.
  const code = base.artifacts.code!
  const approved = [{ stage: 'code' as const, version: code.version, content: code.content, contentDigest: code.contentDigest!, sourceSessionId: code.sourceSessionId ?? '', decision: 'approve' as const, actor: 'local_user', feedback: '', createdAt: at(40) }]
  if (view !== 'code') base.executionReviews = approved
  if (view === 'qa-running') return { ...base, currentStage: 'eval', stageStatus: { ...base.stageStatus, code: 'completed', eval: 'active' } }
  if (view === 'qa-report') return { ...base, currentStage: 'eval', stageStatus: { ...base.stageStatus, code: 'completed', eval: 'waiting_user' }, artifacts: { ...base.artifacts, eval: artifact('eval', JSON.stringify(report), 3) } }
  if (view === 'prs') return { ...base, currentStage: 'prs', stageStatus: { ...base.stageStatus, code: 'completed', eval: 'completed', prs: 'active' },
    artifacts: { ...base.artifacts, eval: artifact('eval', JSON.stringify({ ...report, passed: true, findings: [], checks: report.checks.map(check => ({ ...check, status: 'passed', summary: check.kind === 'e2e' ? '9 cenários passaram no Chromium.' : check.summary })) }), 4) } }
  return base
}

// The QA run as it happens: the commands the QA already ran in its lab and the one running now.
function qaJournal(): AgentEvent[] {
  const events: AgentEvent[] = []
  const push = (type: string, data: unknown) => events.push({ id: `qa-${events.length + 1}`, streamId: 'qa-session', sequence: events.length + 1, type, data, createdAt: at(41 + events.length) })
  push('run.started', {})
  push('message.user', { role: 'user', content: 'Você é o QA desta entrega.' })
  const commands: [string, string, 'done' | 'running'][] = [
    ['t1', 'ls -la && cat package.json', 'done'],
    ['t2', 'npm ci --no-audit --no-fund', 'done'],
    ['t3', 'npm run build', 'done'],
    ['t4', 'npm test', 'done'],
    ['t5', 'npx playwright test --reporter=line', 'running'],
  ]
  for (const [id, command, state] of commands) {
    push('message.assistant', { role: 'assistant', content: '', toolCalls: [{ id, name: 'bash', arguments: { command } }] })
    push('tool.called', { toolCallId: id, name: 'bash' })
    if (state === 'done') push('tool.completed', { toolCallId: id, name: 'bash', content: { text: 'ok' } })
  }
  return events
}

const chatSession: Session = { id: 'chat-session', workspaceId, backendId: 'claude', status: 'completed', resumable: true, title: 'Criar uma lista de tarefas', createdAt: at(0), updatedAt: at(5) }
function chatJournal(): AgentEvent[] {
  const events: AgentEvent[] = []
  const push = (type: string, data: unknown) => events.push({ id: `chat-${events.length + 1}`, streamId: chatSession.id, sequence: events.length + 1, type, data, createdAt: at(events.length) })
  push('run.started', {})
  push('message.user', { role: 'user', content: 'Quero uma lista de tarefas em HTML que guarde tudo no navegador. Cria um trabalho SDD para isso?' })
  push('message.assistant', { role: 'assistant', content: 'Claro. Vou olhar o projeto e abrir um trabalho SDD com a Discovery que você descreveu.', toolCalls: [{ id: 'c1', name: 'ls', arguments: { path: '.' } }, { id: 'c2', name: 'harflex_create_pipeline', arguments: { discovery: '# Lista de tarefas com localStorage\n\nQuero uma lista de tarefas simples, em HTML, que guarde tudo no navegador.' } }] })
  push('tool.called', { toolCallId: 'c1', name: 'ls' })
  push('tool.completed', { toolCallId: 'c1', name: 'ls', content: { text: 'index.html\nstyles.css\npackage.json' } })
  push('tool.called', { toolCallId: 'c2', name: 'harflex_create_pipeline' })
  push('tool.completed', { toolCallId: 'c2', name: 'harflex_create_pipeline', content: { text: 'Pipeline criado: Lista de tarefas com localStorage' } })
  push('message.assistant', { role: 'assistant', content: 'Pronto! Criei o trabalho **Lista de tarefas com localStorage**. Ele já passou por Discovery, SPEC e Plan, e agora está no Code.\n\nVocê acompanha cada etapa em **Pipelines**, e eu sigo por aqui se quiser mudar alguma coisa.' })
  push('run.completed', {})
  return events
}

const pullRequest: PullRequest = {
  id: 'pr-1', pipelineId: 'todo-pipeline', sessionId: 'prs-session', url: 'https://github.com/demo/todo-app/pull/7', title: 'Lista de tarefas com localStorage', branch: 'harflex/lista-de-tarefas',
  state: 'open', watch: true, checking: false, lastCheckedAt: at(52), nextCheckAt: at(58),
  timeline: [
    { at: at(46), kind: 'opened', summary: 'PR aberto a partir da cópia aprovada pela QA.' },
    { at: at(47), kind: 'watch_on', summary: 'Vigia ligada: confere os comentários a cada 10 minutos.' },
    { at: at(51), kind: 'fixed', summary: 'Corrigi 2 comentários da revisão (nome da chave e contraste do botão) e enviei.' },
    { at: at(52), kind: 'quiet', summary: 'Nenhum comentário novo. Próxima conferência em 10 minutos.' },
  ],
}

export function installShowcase(backend: Backend, view: ShowcaseView) {
  const pipeline = pipelineFor(view)
  const checkedAt = new Date().toISOString()
  const openWorkspace = backend.openWorkspace.bind(backend)
  backend.openWorkspace = async path => ({ ...await openWorkspace(path), profile: 'full_access' })
  backend.listBackends = async () => [
    { id: 'claude', name: 'Claude Code', kind: 'cli', available: true, professionalAvailable: true },
    { id: 'codex', name: 'Codex', kind: 'cli', available: true, professionalAvailable: true },
    { id: 'openrouter', name: 'OpenRouter', kind: 'api', available: true },
  ]
  backend.getSettings = async () => ({ defaultBackendId: 'claude', defaultModelBackendId: 'claude', defaultModelId: 'opus' })
  // The PRs run on an API profile: the terminal and the MCP tools they use are Harflex's own.
  backend.listProviderProfiles = async () => [{ id: 'openrouter', name: 'OpenRouter', kind: 'openai_compatible', providerType: 'openrouter', baseUrl: 'https://openrouter.ai/api/v1', model: 'anthropic/claude-opus-5.5', hasCredential: true, endpointBlocked: false, updatedAt: checkedAt }]
  backend.queryHTTPModelCatalog = async query => ({ backendId: query.profileId, source: 'openai_models', destination: 'OpenRouter', profileRevision: 'openrouter-revision', credentialToken: 'a'.repeat(64), searchTerm: '',
    models: [{ id: 'anthropic/claude-opus-5.5', displayName: 'Claude Opus 5.5', backendId: query.profileId, source: 'openai_models', availability: 'available' as const }], nextCursor: '', checkedAt, status: 'complete', complete: true, accountFiltered: true })
  backend.queryCLIModelCatalog = async query => ({ backendId: query.backendId, source: query.backendId === 'claude' ? 'claude_cli' : 'codex_app_server', destination: query.backendId === 'claude' ? 'Claude Code' : 'Codex', profileRevision: `${query.backendId}-revision`, searchTerm: '',
    models: query.backendId === 'claude'
      ? [{ id: 'opus', displayName: 'Opus (mais recente)', backendId: 'claude', source: 'claude_cli', availability: 'listed' as const }, { id: 'sonnet', displayName: 'Sonnet (mais recente)', backendId: 'claude', source: 'claude_cli', availability: 'listed' as const }]
      : [{ id: 'gpt-6', displayName: 'GPT-6', backendId: 'codex', source: 'codex_app_server', availability: 'listed' as const }],
    nextCursor: '', checkedAt, status: 'complete', complete: true, accountFiltered: true })
  backend.listStageExecutors = async () => [
    ...(['code', 'eval'] as const).map(stage => ({ workspaceId, stage, backendId: 'claude', modelId: 'opus', updatedAt: checkedAt })),
    { workspaceId, stage: 'prs' as const, backendId: 'openrouter', modelId: 'anthropic/claude-opus-5.5', updatedAt: checkedAt },
  ]
  backend.listPipelines = async () => [pipeline]
  backend.getPipeline = async () => pipeline
  backend.getPipelineForSession = async () => pipeline
  backend.getPipelineQALoop = async pipelineId => view === 'qa-running'
    ? { pipelineId, phase: 'qa', round: 1, sessionId: 'qa-session', message: 'O QA está rodando build, testes e o app no laboratório', updatedAt: at(46), running: true }
    : { pipelineId, phase: '', round: 0, message: '', updatedAt: at(46), running: false }
  backend.listPipelinePullRequests = async () => view === 'prs' ? [pullRequest] : []
  backend.listMCPServers = async () => [{ id: 'mcp-github', workspaceId, name: 'GitHub', transport: 'http', command: '', args: [], url: 'https://api.githubcopilot.com/mcp/', tokenEnvVar: '', authScheme: 'bearer', enabled: true, tools: [], hasCredential: true, createdAt: checkedAt, updatedAt: checkedAt }] as never
  const qa = qaJournal(), chat = chatJournal()
  backend.listSessions = async id => id === workspaceId ? [chatSession] : []
  backend.openSession = async () => chatSession
  backend.listEvents = async (id, after = 0) => (id === 'qa-session' ? qa : id === chatSession.id ? chat : []).filter(event => event.sequence > after)
  backend.decidePipelineExecutionArtifact = async () => pipeline
}
