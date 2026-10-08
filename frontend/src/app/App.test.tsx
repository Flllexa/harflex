import { act, render, screen, within, cleanup, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import App from './App'
import { createFakeBackend } from '../test/fakeBackend'
import { designPipeline, installPipelineDesignFixture } from '../test/pipelineDesignFixture'
import type { MCPServer, Pipeline, Session } from '../lib/backend'
import { StrictMode } from 'react'

afterEach(cleanup)

async function startFreeSession(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText('Motivo para pular SDD nesta sessão'), 'Teste de conversa livre')
  await user.click(screen.getByRole('button', { name: 'Iniciar sessão livre' }))
}

describe('Harflex control room', () => {
  it('opens a top stage into its document and activity without starting the model', async () => {
    const user = userEvent.setup(), {backend} = createFakeBackend()
    installPipelineDesignFixture(backend, undefined, {pipeline:designPipeline()})
    const prepare = vi.spyOn(backend,'preparePipelineDesign')
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'),'/Users/dev/api-faturas')
    await user.click(screen.getByRole('button',{name:'Abrir projeto'}))
    await waitFor(() => expect(screen.getByRole('button',{name:'Ver Plan e sua atividade'})).toBeEnabled())
    await user.click(screen.getByRole('button',{name:'Ver Plan e sua atividade'}))
    expect(await screen.findByRole('region',{name:'Atividade de Plan'})).toBeInTheDocument()
    expect(screen.getByRole('tab',{name:'Plan'})).toHaveAttribute('aria-selected','true')
    expect(prepare).not.toHaveBeenCalled()
  })
  it('gives conversational preparation room and keeps activity available on request', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    installPipelineDesignFixture(backend, undefined, { pipeline: designPipeline() })
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Pipelines' }))
    await screen.findByRole('heading', { name: 'Preparar trabalho' })
    await waitFor(() => expect(screen.getByRole('button', { name: 'Painel lateral' })).toHaveAttribute('aria-expanded', 'false'))
    await user.click(screen.getByRole('button', { name: 'Painel lateral' }))
    expect(screen.getByRole('complementary', { name: 'Atividade do trabalho' })).toBeInTheDocument()
  })
  it('makes SDD the primary start and audits an explicit free-session bypass reason', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const direct = vi.spyOn(backend, 'createDirectSession')
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    expect(screen.getByRole('button', { name: 'Iniciar trabalho SDD' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Iniciar sessão livre' })).toBeDisabled()
    await user.type(screen.getByLabelText('Motivo para pular SDD nesta sessão'), 'Pesquisa rápida sem edição')
    await user.click(screen.getByRole('button', { name: 'Iniciar sessão livre' }))
    expect(direct).toHaveBeenCalledWith({ workspaceId: 'workspace-1', backendId: 'local', reason: 'Pesquisa rápida sem edição' })
  })
  it('opens the SDD creation form from the primary setup action', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(screen.getByRole('button', { name: 'Iniciar trabalho SDD' }))
    expect(await screen.findByRole('heading', { name: 'Pipelines SDD' })).toBeInTheDocument()
    expect(screen.getByLabelText('Discovery')).toBeInTheDocument()
  })
  it('shows the SPEC linked to the active session in the workbench', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const date = '2026-09-25T10:00:00Z'
    backend.getPipelineForSession = async () => ({ id: 'pipeline-1', workspaceId: 'workspace-1', title: 'Exportar CSV', objective: 'Exportar faturas', currentStage: 'code', revision: 4,
      stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'active', eval: 'pending' },
      artifacts: { spec: { stage: 'spec', version: 1, content: 'SPEC da sessão ativa', updatedAt: date } }, createdAt: date, updatedAt: date })
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await startFreeSession(user)
    await user.click(await screen.findByRole('tab', { name: 'Artefatos' }))
    await user.click(screen.getByRole('button', { name: 'SPEC' }))
    expect(await screen.findByText('SPEC da sessão ativa')).toBeInTheDocument()
  })
  it('warns that OpenCode prompts can appear in local process arguments', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    backend.listBackends = async () => [{ id: 'local', name: 'Local', kind: 'api', available: true }, { id: 'opencode', name: 'OpenCode', kind: 'cli', available: true }]
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(screen.getByRole('radio', { name: /OpenCode/ }))
    expect(screen.getByText(/OpenCode recebe o prompt por argumento de processo/)).toBeInTheDocument()
  })
  it('registers an MCP server, connects only on explicit action and shows discovered tools', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const date = '2026-09-25T10:00:00Z'
    const configured: MCPServer = { id: 'mcp-12345678', workspaceId: 'workspace-1', name: 'Docs', transport: 'http', command: '', args: [], url: 'http://127.0.0.1:3333/mcp', tokenEnvVar: '', authScheme: 'bearer', enabled: false, tools: [], hasCredential: false, createdAt: date, updatedAt: date }
    const connected = { ...configured, enabled: true, tools: [{ name: 'search', agentName: 'mcp_12345678_search_1234', description: 'Search docs', schema: { type: 'object' } }] }
    const save = vi.fn(async () => configured)
    const connect = vi.fn(async () => connected)
    backend.listMCPServers = async () => []
    backend.saveMCPServer = save
    backend.connectMCPServer = connect
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'MCP Servers' }))
    expect(await screen.findByRole('heading', { name: 'Servidores MCP' })).toBeInTheDocument()
    await user.type(screen.getByLabelText('Nome do servidor'), 'Docs')
    await user.type(screen.getByLabelText('URL do servidor'), 'http://127.0.0.1:3333/mcp')
    await user.click(screen.getByRole('button', { name: 'Salvar servidor' }))
    expect(save).toHaveBeenCalled()
    expect(connect).not.toHaveBeenCalled()
    await user.click(await screen.findByRole('button', { name: 'Conectar Docs' }))
    expect(connect).toHaveBeenCalledWith('mcp-12345678')
    expect(await screen.findByText('Search docs')).toBeInTheDocument()
  })
  it('lets an API agent opt into tools discovered for the current project', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const date = '2026-09-25T10:00:00Z'
    const server: MCPServer = { id: 'mcp-12345678', workspaceId: 'workspace-1', name: 'Docs', transport: 'http', command: '', args: [], url: 'http://127.0.0.1:3333/mcp', tokenEnvVar: '', authScheme: 'bearer', enabled: true,
      tools: [{ name: 'search', agentName: 'mcp_12345678_search_1234', description: 'Search docs', schema: { type: 'object' } }], hasCredential: false, createdAt: date, updatedAt: date }
    backend.listMCPServers = async () => [server]
    const save = vi.spyOn(backend, 'saveAgent')
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Agentes' }))
    await user.type(screen.getByLabelText('Nome do agente'), 'Pesquisa')
    await user.type(screen.getByLabelText('Instruções do agente'), 'Busque a documentação')
    await user.click(await screen.findByLabelText('Docs · search'))
    await user.click(screen.getByRole('button', { name: 'Salvar agente' }))
    expect(save).toHaveBeenCalledWith(expect.objectContaining({ allowedTools: expect.arrayContaining([server.tools[0].agentName]) }))
  })
  it('saves and enables a workspace skill from its own screen', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const date = '2026-09-25T10:00:00Z'
    const saved = { id: 'skill-1', workspaceId: 'workspace-1', name: 'Revisão', description: 'Cobertura', content: 'Revise testes', enabled: true, revision: 1, createdAt: date, updatedAt: date }
    const saveSkill = vi.fn(async () => saved)
    backend.saveSkill = saveSkill
    backend.listSkills = async () => []
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Skills' }))
    expect(await screen.findByRole('heading', { name: 'Skills do projeto' })).toBeInTheDocument()
    await user.type(screen.getByLabelText('Nome da skill'), 'Revisão')
    await user.type(screen.getByLabelText('Instruções da skill'), 'Revise testes')
    await user.click(screen.getByRole('button', { name: 'Salvar skill' }))
    expect(saveSkill).toHaveBeenCalledWith({ id: undefined, workspaceId: 'workspace-1', name: 'Revisão', description: '', content: 'Revise testes', enabled: true })
    expect(await screen.findByText('Revise testes')).toBeInTheDocument()
  })
  it('imports SKILL.md inside the selected workspace', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const date = '2026-09-25T10:00:00Z'
    const imported = { id: 'skill-imported', workspaceId: 'workspace-1', name: 'review', description: '', content: 'Revise testes', enabled: true, revision: 1, createdAt: date, updatedAt: date }
    backend.listSkills = async () => []
    const pickSkillFile = vi.fn(async () => '.agents/skills/review/SKILL.md')
    backend.pickSkillFile = pickSkillFile
    const importSkill = vi.fn(async () => imported)
    backend.importSkill = importSkill
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Skills' }))
    await user.click(await screen.findByRole('button', { name: 'Escolher arquivo' }))
    expect(pickSkillFile).toHaveBeenCalledOnce()
    await user.click(screen.getByRole('button', { name: 'Importar skill' }))
    expect(importSkill).toHaveBeenCalledWith({ workspaceId: 'workspace-1', path: '.agents/skills/review/SKILL.md', enabled: true })
    expect(await screen.findByText('Revise testes')).toBeInTheDocument()
  })
  it('saves a workflow and runs its first step against the selected backend', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const date = '2026-09-25T10:00:00Z'
    const definitions: unknown[] = []
    const saveWorkflow = vi.fn(async (input: { workspaceId: string; name: string; steps: { name: string; prompt: string }[] }) => {
      const item = { ...input, id: 'workflow-1', revision: 1, createdAt: date, updatedAt: date }
      definitions.push(item)
      return item
    })
    const startWorkflow = vi.fn(async () => ({ id: 'run-1', workflowId: 'workflow-1', workspaceId: 'workspace-1', backendId: 'local', steps: [{ name: 'Revisar', prompt: 'Leia o código' }], currentStep: 0, status: 'ready', lastSessionId: '', createdAt: date, updatedAt: date }))
    const runWorkflowStep = vi.fn(async () => ({ id: 'run-1', workflowId: 'workflow-1', workspaceId: 'workspace-1', backendId: 'local', steps: [{ name: 'Revisar', prompt: 'Leia o código' }], currentStep: 1, status: 'completed', lastSessionId: 'session-1', createdAt: date, updatedAt: date }))
    Object.assign(backend, { listWorkflows: async () => definitions, listWorkflowRuns: async () => [], saveWorkflow, startWorkflow, runWorkflowStep })
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Workflows' }))
    expect(await screen.findByRole('heading', { name: 'Workflows locais' })).toBeInTheDocument()
    await user.type(screen.getByLabelText('Nome do workflow'), 'Revisar código')
    await user.type(screen.getByLabelText('Nome da etapa 1'), 'Revisar')
    await user.type(screen.getByLabelText('Prompt da etapa 1'), 'Leia o código')
    await user.click(screen.getByRole('button', { name: 'Salvar workflow' }))
    expect(saveWorkflow).toHaveBeenCalledWith({ id: undefined, workspaceId: 'workspace-1', name: 'Revisar código', steps: [{ name: 'Revisar', prompt: 'Leia o código' }] })
    await user.click(await screen.findByRole('button', { name: 'Iniciar Revisar código' }))
    expect(startWorkflow).toHaveBeenCalledWith({ workflowId: 'workflow-1', backendId: 'local' })
    await user.click(screen.getByRole('button', { name: 'Executar etapa' }))
    expect(runWorkflowStep).toHaveBeenCalledWith('run-1')
    expect(await screen.findByText('Concluído')).toBeInTheDocument()
  })
  it('keeps paused workflow recovery locked when session replay fails', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const date = '2026-09-26T10:00:00Z'
    backend.listWorkflows = async () => [{ id: 'workflow-1', workspaceId: 'workspace-1', name: 'Revisão', steps: [{ name: 'Checar', prompt: 'Cheque' }], revision: 1, createdAt: date, updatedAt: date }]
    backend.listWorkflowRuns = async () => [{ id: 'run-1', workflowId: 'workflow-1', workspaceId: 'workspace-1', backendId: 'local', steps: [{ name: 'Checar', prompt: 'Cheque' }], currentStep: 0, status: 'paused', lastSessionId: 'session-1', createdAt: date, updatedAt: date }]
    backend.openSession = async () => ({ id: 'session-1', workspaceId: 'workspace-1', backendId: 'local', status: 'failed', resumable: true, createdAt: date, updatedAt: date })
    let replayFails = true
    backend.listEvents = async () => {
      if (replayFails) throw new Error('replay unavailable')
      return [{ id: 'terminal-1', streamId: 'session-1', sequence: 1, type: 'run.failed', data: { reason: 'execution_failed' }, createdAt: date }]
    }
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    const navigation = within(screen.getByRole('navigation', { name: 'Navegação principal' }))
    await user.click(navigation.getByRole('button', { name: 'Workflows' }))
    expect(await screen.findByText('Pausado')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Abrir sessão da etapa' }))
    await user.click(navigation.getByRole('button', { name: 'Workflows' }))
    expect(await screen.findByRole('checkbox', { name: /Revisei o histórico da sessão/ })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Confirmar decisão' })).toBeDisabled()
    replayFails = false
    await user.click(screen.getByRole('button', { name: 'Abrir sessão da etapa' }))
    await user.click(navigation.getByRole('button', { name: 'Workflows' }))
    expect(await screen.findByRole('checkbox', { name: /Revisei o histórico da sessão/ })).toBeEnabled()
    replayFails = true
    await user.click(screen.getByRole('button', { name: 'Abrir sessão da etapa' }))
    await user.click(navigation.getByRole('button', { name: 'Workflows' }))
    expect(await screen.findByRole('checkbox', { name: /Revisei o histórico da sessão/ })).toBeDisabled()
  })
  it('delegates a task from an open session to a saved subagent', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const date = '2026-09-25T10:00:00Z'
    const agent = { id: 'agent-1', name: 'Revisor', description: 'Revisa código', instructions: 'Revise', backendId: 'local', allowedTools: ['read'], createdAt: date, updatedAt: date }
    backend.listAgents = async () => [agent]
    const delegateToAgent = vi.fn(async () => ({ session: { id: 'child-1', workspaceId: 'workspace-1', backendId: 'local', status: 'ready', resumable: true, createdAt: date, updatedAt: date }, prompt: 'Revise os testes', depth: 1, created: true }))
    backend.delegateToAgent = delegateToAgent
    const prompt = vi.spyOn(backend, 'prompt')
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await startFreeSession(user)
    await screen.findByLabelText('Mensagem')
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Agentes' }))
    await user.click(await screen.findByRole('button', { name: 'Delegar a Revisor' }))
    await user.type(screen.getByLabelText('Tarefa para Revisor'), 'Revise os testes')
    await user.click(screen.getByRole('button', { name: 'Iniciar subagente' }))
    expect(delegateToAgent).toHaveBeenCalledWith('session-1', 'agent-1', 'Revise os testes', expect.stringMatching(/^[a-f0-9]{32}$/))
    await waitFor(() => expect(prompt).toHaveBeenCalledWith('child-1', 'Revise os testes'))
  })
  it('opens an existing delegated child without silently running the task again', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const date = '2026-09-25T10:00:00Z'
    backend.listAgents = async () => [{ id: 'agent-1', name: 'Revisor', description: '', instructions: 'Revise', backendId: 'local', allowedTools: ['read'], createdAt: date, updatedAt: date }]
    backend.delegateToAgent = vi.fn(async () => ({ session: { id: 'child-1', workspaceId: 'workspace-1', backendId: 'local', status: 'ready', resumable: true, createdAt: date, updatedAt: date }, prompt: 'Revise os testes', depth: 1, created: false }))
    backend.openSession = vi.fn(async () => ({ id: 'child-1', workspaceId: 'workspace-1', backendId: 'local', status: 'ready', resumable: true, createdAt: date, updatedAt: date }))
    const prompt = vi.spyOn(backend, 'prompt')
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await startFreeSession(user)
    await screen.findByLabelText('Mensagem')
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Agentes' }))
    await user.click(await screen.findByRole('button', { name: 'Delegar a Revisor' }))
    await user.type(screen.getByLabelText('Tarefa para Revisor'), 'Revise os testes')
    await user.click(screen.getByRole('button', { name: 'Iniciar subagente' }))
    await waitFor(() => expect(backend.openSession).toHaveBeenCalledWith('child-1', 'workspace-1'))
    expect(prompt).not.toHaveBeenCalled()
    expect(await screen.findByDisplayValue('Revise os testes')).toBeInTheDocument()
    expect(screen.getByText(/tarefa já registrada/i)).toBeInTheDocument()
  })
  it('creates an agent profile and starts a session with its saved instructions', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const agents: unknown[] = []
    const saveAgent = vi.fn(async (input: { name: string; description: string; instructions: string; backendId: string; allowedTools: string[] }) => {
      const item = { ...input, id: 'agent-1', createdAt: '2026-09-25T10:00:00Z', updatedAt: '2026-09-25T10:00:00Z' }
      agents.push(item)
      return item
    })
    const createDirectSession = vi.spyOn(backend, 'createDirectSession')
    Object.assign(backend, { listAgents: async () => agents, saveAgent })
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Agentes' }))
    expect(await screen.findByRole('heading', { name: 'Agentes locais' })).toBeInTheDocument()
    await user.type(screen.getByLabelText('Nome do agente'), 'Analista')
    await user.type(screen.getByLabelText('Instruções do agente'), 'Leia antes de responder')
    await user.click(screen.getByRole('button', { name: 'Salvar agente' }))
    expect(saveAgent).toHaveBeenCalled()
    await user.click(await screen.findByRole('button', { name: 'Conversar com Analista' }))
    await user.type(screen.getByLabelText('Motivo para conversar sem SDD com Analista'), 'Pesquisa pontual')
    await user.click(screen.getByRole('button', { name: 'Iniciar sessão livre com Analista' }))
    expect(createDirectSession).toHaveBeenCalledWith({ workspaceId: 'workspace-1', backendId: 'local', agentId: 'agent-1', reason: 'Pesquisa pontual' })
    expect(await screen.findByLabelText('Mensagem')).toBeInTheDocument()
  })

  it('keeps the final evaluation and previous artifacts inspectable after completion', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const date = '2026-09-25T10:00:00Z'
    const run = { id: 'pipeline-done', workspaceId: 'workspace-1', title: 'CSV pronto', objective: 'Exportar CSV', currentStage: '', stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'completed', eval: 'completed' }, revision: 8, artifacts: { spec: { stage: 'spec', version: 1, content: 'Critério CSV correto', updatedAt: date }, eval: { stage: 'eval', version: 1, content: '{"passed":true,"findings":[]}', updatedAt: date } }, createdAt: date, updatedAt: date }
    Object.assign(backend, { listPipelines: async () => [run], getPipeline: async () => run })
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Pipelines' }))
    expect(await screen.findByRole('heading', { name: 'Artefatos salvos' })).toBeInTheDocument()
    expect(screen.getByText('{"passed":true,"findings":[]}')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Ver Spec' }))
    expect(screen.getByText('Critério CSV correto')).toBeInTheDocument()
  })

  it('starts the Coder through a linked agent session from the Code phase', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const run = { id: 'pipeline-1', workspaceId: 'workspace-1', title: 'Exportar faturas', objective: 'Adicionar CSV', currentStage: 'code', stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'active', eval: 'pending' }, revision: 4, artifacts: {}, createdAt: '2026-09-25T10:00:00Z', updatedAt: '2026-09-25T10:00:00Z' }
    const session = { id: 'session-1', workspaceId: 'workspace-1', backendId: 'local', status: 'ready', resumable: true, createdAt: '2026-09-25T10:00:00Z', updatedAt: '2026-09-25T10:00:00Z' }
    const createPipelineSession = vi.fn(async () => ({ session, prompt: 'Implementar CSV', role: 'coder' }))
    const prompt = vi.spyOn(backend, 'prompt')
    const checkedAt = new Date().toISOString()
    Object.assign(backend, {
      listPipelines: async () => [run], getPipeline: async () => run, createPipelineSession,
      getSettings: async () => ({ defaultBackendId: 'local', defaultModelBackendId: 'local', defaultModelId: 'api-model' }),
      listProviderProfiles: async () => [{ id: 'local', name: 'Local API', kind: 'openai_compatible', providerType: 'openai', baseUrl: 'https://example.test/v1', model: 'api-model', hasCredential: true, endpointBlocked: false, updatedAt: checkedAt }],
      queryHTTPModelCatalog: async query => ({ backendId: query.profileId, source: 'openai_models', destination: 'Local API', profileRevision: 'api-revision', credentialToken: 'a'.repeat(64), searchTerm: '',
        models: [{ id: 'api-model', displayName: 'Modelo API', backendId: 'local', source: 'openai_models', availability: 'available' }], nextCursor: '', checkedAt, status: 'complete' as const, complete: true, accountFiltered: true }),
    })
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Pipelines' }))
    await user.click(await screen.findByRole('button', { name: 'Executar Coder' }))
    expect(createPipelineSession).toHaveBeenCalledWith('pipeline-1', 'local', 'coder', expect.objectContaining({ executor: 'api', profileId: 'local', modelId: 'api-model', catalogRevision: 'api-revision' }), false)
    await waitFor(() => expect(prompt).toHaveBeenCalledWith('session-1', 'Implementar CSV'))
    expect(screen.getByRole('button', { name: 'Conversas' })).toHaveAttribute('aria-current', 'page')
  })

  it('creates an authoring pipeline from Discovery without manual stage advancement', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const date = '2026-09-25T10:00:00Z'
    const run: Pipeline = { id: 'pipeline-1', workspaceId: 'workspace-1', kind: 'ai_authoring', derivedFromPipelineId: '', discoveryFrozenVersion: 0, title: 'Exportar faturas', objective: '', currentStage: 'discovery', stageStatus: { discovery: 'active', spec: 'pending', plan: 'pending', code: 'pending', eval: 'pending' }, revision: 1, artifacts: { discovery: { stage: 'discovery', version: 1, content: 'Adicionar CSV', author: 'user', sourceSessionId: '', updatedAt: date } }, createdAt: date, updatedAt: date }
    const createAuthoringPipeline = vi.fn(async () => run)
    const advancePipeline = vi.fn()
    Object.assign(backend, {
      listPipelines: async () => [], createAuthoringPipeline, getPipeline: async () => run, advancePipeline,
    })
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Pipelines' }))
    expect(await screen.findByRole('heading', { name: 'Pipelines SDD' })).toBeInTheDocument()
    await user.type(screen.getByLabelText('Discovery'), 'Adicionar CSV')
    await user.click(screen.getByRole('button', { name: 'Criar pipeline' }))
    expect(createAuthoringPipeline).toHaveBeenCalledWith(expect.objectContaining({ workspaceId: 'workspace-1', discovery: 'Adicionar CSV' }))
    expect(within(screen.getByRole('navigation', { name: 'SDD Pipeline' })).getByText('Discovery').closest('[aria-current="step"]')).toBeInTheDocument()
    expect(await screen.findByText('Escrito por você')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Avançar fase' })).not.toBeInTheDocument()
    expect(advancePipeline).not.toHaveBeenCalled()
  })

  it('inspects real Git status with staged, unstaged and untracked files', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    Object.assign(backend, { inspectRepository: async () => ({ isRepository: true, root: '/Users/dev/api-faturas', branch: 'feature/export', files: ['M  staged.go', ' M unstaged.go', '?? new.go'], stagedDiff: 'diff --git a/staged.go b/staged.go\n+staged change', unstagedDiff: 'diff --git a/unstaged.go b/unstaged.go\n+unstaged change', truncated: false }) })
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Repositórios' }))
    expect(await screen.findByRole('heading', { name: 'Repositório Git' })).toBeInTheDocument()
    expect(screen.getByText('feature/export')).toBeInTheDocument()
    expect(screen.getByText('new.go')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Mudanças preparadas' }))
    expect(screen.getByText('+staged change')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Mudanças não preparadas' }))
    expect(screen.getByText('+unstaged change')).toBeInTheDocument()
  })

  it('shows real session activity instead of a sample timeline', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    render(<App backend={backend} />)
    expect(screen.queryByText('Linha do tempo de exemplo')).not.toBeInTheDocument()
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await startFreeSession(user)
    await user.type(await screen.findByLabelText('Mensagem'), 'Criar notas{Enter}')
    expect(await screen.findByRole('group', { name: 'Aprovação necessária' })).toBeInTheDocument()
    expect(screen.getByRole('complementary', { name: 'Atividade do trabalho' })).toHaveTextContent('Aprovação solicitada')
  })

  it('filters persisted execution logs without showing event payloads', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const listLogEvents = vi.fn(async ({ type }: { type: string }) => type === 'run.failed' ? [
      { cursor: 2, id: 'e2', sessionId: 'session-1', workspaceId: 'workspace-1', sequence: 2, type: 'run.failed', createdAt: '2026-09-25T10:00:00Z' },
    ] : [
      { cursor: 2, id: 'e2', sessionId: 'session-1', workspaceId: 'workspace-1', sequence: 2, type: 'run.failed', createdAt: '2026-09-25T10:00:00Z' },
      { cursor: 1, id: 'e1', sessionId: 'session-1', workspaceId: 'workspace-1', sequence: 1, type: 'run.started', createdAt: '2026-09-25T09:59:00Z' },
    ])
    Object.assign(backend, { listLogEvents })
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Logs' }))
    expect(await screen.findByRole('heading', { name: 'Logs e auditoria' })).toBeInTheDocument()
    expect(within(screen.getByRole('table', { name: 'Índice de eventos persistidos' })).getByText('run.started')).toBeInTheDocument()
    await user.type(within(screen.getByTestId('picker-logs-event-type')).getByRole('combobox'), 'run.failed')
    await user.click(screen.getByRole('option', { name: 'run.failed' }))
    await waitFor(() => expect(listLogEvents).toHaveBeenLastCalledWith({ workspaceId: 'workspace-1', type: 'run.failed', beforeId: 0, limit: 50 }))
    expect(screen.queryByText('secret-canary-log')).not.toBeInTheDocument()
  })

  it('shows saved providers and persists the default backend and configured model from Configurações', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const saveSettings = vi.fn(async (input: { defaultBackendId: string; defaultModelBackendId: string; defaultModelId: string }) => input)
    Object.assign(backend, {
      listProviderProfiles: async () => [{ id: 'local', name: 'Local', kind: 'openai_compatible', providerType: 'openai', baseUrl: 'https://api.openai.com/v1', model: 'modelo-local', hasCredential: true, endpointBlocked: false, updatedAt: '2026-09-25T10:00:00Z' }],
      getSettings: async () => ({ defaultBackendId: '', defaultModelBackendId: '', defaultModelId: '' }),
      saveSettings,
      setWorkspaceProfile: async (_workspaceId: string, _profile: string) => ({ id: 'workspace-1', path: '/Users/dev/api-faturas', profile: 'ask' }),
    })
    render(<App backend={backend} />)
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Configurações' }))
    expect(await screen.findByRole('heading', { name: 'Configurações do Harflex' })).toBeInTheDocument()
    expect(screen.getByText('modelo-local')).toBeInTheDocument()
    expect(screen.getByText('https://api.openai.com/v1')).toBeInTheDocument()
    expect(screen.queryByText('secret-canary-987')).not.toBeInTheDocument()
    await user.click(within(screen.getByTestId('picker-settings-default-backend')).getByRole('button'))
    await user.click(screen.getByRole('option', { name: 'Local' }))
    await user.click(screen.getByRole('button', { name: 'Salvar preferências' }))
    expect(saveSettings).toHaveBeenCalledWith({ defaultBackendId: 'local', defaultModelBackendId: 'local', defaultModelId: 'modelo-local' })
    expect(await screen.findByRole('status')).toHaveTextContent('Preferências salvas')
  })

  it('keeps a blocked legacy provider editable with its original HTTP URL', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    backend.listProviderProfiles = async () => [{ id: 'legacy', name: 'Legado', kind: 'openai_compatible', providerType: 'generic', baseUrl: 'http://legacy.example/v1', model: 'old', hasCredential: true, endpointBlocked: true, updatedAt: '2026-09-25T10:00:00Z' }]
    render(<App backend={backend} />)
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Configurações' }))
    expect(await screen.findByText('HTTPS necessário')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Editar Legado' }))
    expect(screen.getByRole('dialog', { name: 'Editar provedor' })).toBeInTheDocument()
    expect(screen.getByLabelText('URL base')).toHaveValue('http://legacy.example/v1')
    expect(screen.getByText(/HTTPS necessário: este endpoint legado/)).toBeInTheDocument()
  })

  it('uses the saved backend preference when setting up a new session', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    backend.listBackends = async () => [
      { id: 'alpha', name: 'Alpha', kind: 'api', available: true },
      { id: 'beta', name: 'Beta', kind: 'api', available: true },
    ]
    backend.getSettings = async () => ({ defaultBackendId: 'beta', defaultModelBackendId: '', defaultModelId: '' })
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    expect(await screen.findByRole('radio', { name: /Beta/ })).toBeChecked()
  })

  it('lists a saved local project and reopens it from Projetos', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const openWorkspace = vi.spyOn(backend, 'openWorkspace')
    Object.assign(backend, { listWorkspaces: async () => [{ id: 'workspace-1', path: '/Users/dev/api-faturas', profile: 'ask', available: true }] })
    render(<App backend={backend} />)
    // The saved project already reopened at startup; the catalog still lists it and can reopen it.
    await waitFor(() => expect(openWorkspace).toHaveBeenCalledTimes(1))
    openWorkspace.mockClear()
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Projetos' }))
    expect(await screen.findByRole('heading', { name: 'Projetos locais' })).toBeInTheDocument()
    expect(await screen.findByText('/Users/dev/api-faturas', { selector: '.project-card-path' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Abrir api-faturas' }))
    expect(openWorkspace).toHaveBeenCalledWith('/Users/dev/api-faturas')
    expect(await screen.findByRole('heading', { name: 'Começar pelo SDD' })).toBeInTheDocument()
  })

  it('archives a project, lists it under the archived toggle and reactivates it', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const openWorkspace = vi.spyOn(backend, 'openWorkspace')
    let catalog = [
      { id: 'old', path: '/Users/dev/legado', profile: 'ask', available: true, archived: true },
      { id: 'workspace-1', path: '/Users/dev/api-faturas', profile: 'ask', available: true, archived: false },
    ]
    backend.listWorkspaces = async () => catalog
    const setArchived = vi.fn(async (workspaceId: string, archived: boolean) => {
      catalog = catalog.map(item => item.id === workspaceId ? { ...item, archived } : item)
      return catalog.find(item => item.id === workspaceId)!
    })
    backend.setWorkspaceArchived = setArchived
    render(<App backend={backend} />)
    // The archived project was opened most recently, but startup skips it.
    await waitFor(() => expect(openWorkspace).toHaveBeenCalledWith('/Users/dev/api-faturas'))
    expect(openWorkspace).not.toHaveBeenCalledWith('/Users/dev/legado')
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Projetos' }))
    expect(await screen.findByRole('button', { name: 'Abrir api-faturas' })).toBeInTheDocument()
    expect(screen.queryByText('/Users/dev/legado')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Arquivar api-faturas' }))
    expect(setArchived).toHaveBeenCalledWith('workspace-1', true)
    expect(await screen.findByText('Nenhuma pasta ativa')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Mostrar arquivados (2)' }))
    const archived = screen.getByRole('region', { name: 'Arquivados' })
    expect(within(archived).getByText('/Users/dev/legado')).toBeInTheDocument()
    await user.click(within(archived).getByRole('button', { name: 'Reativar legado' }))
    expect(setArchived).toHaveBeenCalledWith('old', false)
    expect(await screen.findByRole('button', { name: 'Abrir legado' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Ocultar arquivados (1)' })).toHaveAttribute('aria-expanded', 'true')
  })

  it('explains a missing project directory and recovers a failed catalog load', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    backend.listWorkspaces = vi.fn()
      .mockResolvedValueOnce([]) // startup finds no project to reopen
      .mockRejectedValueOnce(new Error('private database path'))
      .mockResolvedValue([{ id: 'missing', path: '/Users/dev/moved-project', profile: 'ask', available: false }])
    render(<App backend={backend} />)
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Projetos' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Não foi possível carregar os projetos.')
    expect(screen.queryByText('private database path')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Tentar novamente' }))
    expect(await screen.findByText('/Users/dev/moved-project')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Abrir moved-project' })).toBeDisabled()
    expect(screen.getByText('A pasta mudou de lugar. Adicione o novo caminho acima.')).toBeInTheDocument()
  })

  it('reopens the most recent available project at startup', async () => {
    const { backend } = createFakeBackend()
    const openWorkspace = vi.spyOn(backend, 'openWorkspace')
    backend.listWorkspaces = async () => [
      { id: 'gone', path: '/Users/dev/moved-project', profile: 'ask', available: false },
      { id: 'workspace-1', path: '/Users/dev/api-faturas', profile: 'ask', available: true },
      { id: 'older', path: '/Users/dev/older', profile: 'ask', available: true },
    ]
    render(<App backend={backend} />)
    expect(await screen.findByRole('heading', { level: 1, name: 'api-faturas' })).toBeInTheDocument()
    expect(openWorkspace).toHaveBeenCalledTimes(1)
    expect(openWorkspace).toHaveBeenCalledWith('/Users/dev/api-faturas')
    expect(screen.getByRole('button', { name: 'Iniciar trabalho SDD' })).toBeInTheDocument()
    expect(screen.queryByLabelText('Caminho da pasta')).not.toBeInTheDocument()
  })

  it('keeps the open-project form when no saved project can be reopened', async () => {
    const { backend } = createFakeBackend()
    const openWorkspace = vi.spyOn(backend, 'openWorkspace')
    backend.listWorkspaces = async () => [{ id: 'gone', path: '/Users/dev/moved-project', profile: 'ask', available: false }]
    render(<App backend={backend} />)
    expect(await screen.findByLabelText('Caminho da pasta')).toBeInTheDocument()
    expect(openWorkspace).not.toHaveBeenCalled()
    expect(screen.queryByText('Continuar em um projeto recente')).not.toBeInTheDocument()
  })

  it('offers recent projects when the automatic reopen fails', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const openWorkspace = vi.spyOn(backend, 'openWorkspace').mockRejectedValueOnce(new Error('denied'))
    backend.listWorkspaces = async () => [{ id: 'workspace-1', path: '/Users/dev/api-faturas', profile: 'ask', available: true }]
    render(<App backend={backend} />)
    await user.click(await screen.findByRole('button', { name: 'Abrir api-faturas' }))
    expect(openWorkspace).toHaveBeenCalledTimes(2)
    expect(await screen.findByRole('heading', { level: 1, name: 'api-faturas' })).toBeInTheDocument()
  })

  it('keeps the current conversation draft while visiting another destination, and with the chat when switching projects', async () => {
    const user = userEvent.setup()
    window.localStorage.removeItem('harflex.chatDrafts')
    const { backend } = createFakeBackend()
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await startFreeSession(user)
    await user.type(await screen.findByLabelText('Mensagem'), 'Rascunho preservado')
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Projetos' }))
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Conversas' }))
    expect(screen.getByLabelText('Mensagem')).toHaveValue('Rascunho preservado')
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Projetos' }))
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/another-project')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    // Nothing blocks the switch: the draft waits with its chat.
    await waitFor(() => expect(window.localStorage.getItem('harflex.chatDrafts')).toContain('Rascunho preservado'))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    window.localStorage.removeItem('harflex.chatDrafts')
  })

  it('starts a new SDD pipeline from the global action without losing the workspace', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await startFreeSession(user)
    await screen.findByLabelText('Mensagem')
    await user.click(screen.getByRole('button', { name: 'Novo trabalho' }))
    expect(screen.getByRole('button', { name: 'Pipelines' })).toHaveAttribute('aria-current', 'page')
    expect(await screen.findByRole('heading', { name: 'Pipelines SDD' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 1, name: 'api-faturas' })).toBeInTheDocument()
    expect(screen.queryByLabelText('Mensagem')).not.toBeInTheDocument()
  })

  it('does not turn the Professional new-work click event into a recovered prompt', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    localStorage.setItem('harflex:view-mode', 'professional')
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(screen.getByRole('button', { name: 'Novo trabalho' }))
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Conversas' }))

    expect(screen.queryByLabelText('Pedido anterior · rascunho')).not.toBeInTheDocument()
  })

  it('asks for a project before starting the default SDD workflow', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    render(<App backend={backend} />)
    await user.click(screen.getByRole('button', { name: 'Novo trabalho' }))
    expect(await screen.findByRole('heading', { name: 'Projetos locais' })).toBeInTheDocument()
    await user.type(screen.getByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    expect(await screen.findByRole('heading', { name: 'Pipelines SDD' })).toBeInTheDocument()
  })

  it('keeps an unresolved approval visible when new work is requested', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await startFreeSession(user)
    await user.type(await screen.findByLabelText('Mensagem'), 'Criar notas{Enter}')
    expect(await screen.findByRole('group', { name: 'Aprovação necessária' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Novo trabalho' }))
    expect(screen.getByRole('group', { name: 'Aprovação necessária' })).toBeInTheDocument()
    expect(screen.getByText('Cancele ou conclua a execução atual antes de iniciar outro trabalho.')).toBeInTheDocument()
  })

  it('exposes all navigation destinations as keyboard controls and the new work action', () => {
    render(<App />)
    const navigation = screen.getByRole('navigation', { name: 'Navegação principal' })
    for (const name of ['Projetos', 'Conversas', 'Agentes', 'Skills', 'MCP Servers', 'Agendamentos', 'Conhecimento', 'Canais', 'Pipelines', 'Workflows', 'Repositórios', 'Diagnóstico', 'Logs', 'Custos e execução', 'Vault', 'Configurações']) {
      expect(within(navigation).getByRole('button', { name })).toHaveClass('touch-target')
    }
    expect(screen.getByRole('button', { name: 'Novo trabalho' })).toHaveClass('touch-target')
  })

  it('mounts a real surface for every destination with the desktop backend', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    render(<App backend={backend} />)
    const navigation = screen.getByRole('navigation', { name: 'Navegação principal' })
    for (const name of ['Projetos', 'Conversas', 'Agentes', 'Skills', 'MCP Servers', 'Agendamentos', 'Conhecimento', 'Canais', 'Pipelines', 'Workflows', 'Repositórios', 'Diagnóstico', 'Logs', 'Custos e execução', 'Vault', 'Configurações']) {
      await user.click(within(navigation).getByRole('button', { name }))
      expect(document.querySelector('.destination-failure')).toBeNull()
    }
  })

  it('shows six ordered pipeline stages without inventing progress', () => {
    render(<App />)
    const pipeline = screen.getByRole('navigation', { name: 'SDD Pipeline' })
    const stages = within(pipeline).getAllByRole('listitem')
    expect(stages).toHaveLength(6)
    for (const [index, name] of ['Discovery', 'SPEC', 'Plan', 'Code', 'QA', 'PRs'].entries()) {
      expect(stages[index]).toHaveTextContent(name)
    }
    for (const stage of stages) expect(stage).toHaveTextContent('Pendente')
    expect(pipeline.querySelector('[aria-current="step"]')).not.toBeInTheDocument()
    expect(pipeline).toHaveTextContent('Nenhum pipeline ativo')
  })

  it('provides named landmarks, a selected tab and honest example content', () => {
    render(<App />)
    expect(screen.getByRole('banner')).toBeInTheDocument()
    expect(screen.getByRole('main', { name: 'Área de trabalho' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'api-faturas' })).toBeInTheDocument()
    expect(screen.getByText('Local')).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'Conversa' })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByText('Projeto de exemplo')).toBeInTheDocument()
    expect(screen.queryByText(/Greet|Listening for Time|Wails \+/)).not.toBeInTheDocument()
  })

  it('opens activity, closes it with Escape and restores trigger focus', async () => {
    const user = userEvent.setup()
    render(<App initialActivityOpen={false} />)
    const trigger = screen.getByRole('button', { name: 'Painel lateral' })
    expect(trigger).toHaveAttribute('aria-expanded', 'false')
    expect(trigger).toHaveAttribute('aria-controls', 'side-panel')
    await user.click(trigger)
    expect(trigger).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByRole('complementary', { name: 'Atividade do trabalho' })).toBeInTheDocument()
    await user.keyboard('{Escape}')
    expect(trigger).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByRole('complementary', { name: 'Atividade do trabalho' })).not.toBeInTheDocument()
    expect(trigger).toHaveFocus()
  })

  it('switches between dark and light themes and remembers the choice', async () => {
    const user = userEvent.setup()
    window.localStorage.removeItem('harflex.theme')
    document.documentElement.dataset.theme = 'dark'
    render(<App initialActivityOpen={false} />)
    await user.click(screen.getByRole('button', { name: 'Mudar para o tema claro' }))
    expect(document.documentElement.dataset.theme).toBe('light')
    expect(window.localStorage.getItem('harflex.theme')).toBe('light')
    await user.click(screen.getByRole('button', { name: 'Mudar para o tema escuro' }))
    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(window.localStorage.getItem('harflex.theme')).toBe('dark')
    window.localStorage.removeItem('harflex.theme')
    delete document.documentElement.dataset.theme
  })

  it('resizes the side panel from its edge with the keyboard and remembers the width', async () => {
    const user = userEvent.setup()
    window.localStorage.removeItem('harflex.sidePanelWidth')
    const viewport = window.innerWidth
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1440 })
    render(<App initialActivityOpen />)
    const handle = screen.getByRole('separator', { name: 'Redimensionar painel lateral' })
    expect(handle).toHaveAttribute('aria-valuenow', '360')
    handle.focus()
    await user.keyboard('{ArrowLeft}{ArrowLeft}')
    expect(handle).toHaveAttribute('aria-valuenow', '408')
    expect(document.querySelector('.app-shell')).toHaveStyle({ '--side-panel-width': '408px' })
    expect(window.localStorage.getItem('harflex.sidePanelWidth')).toBe('408')
    await user.keyboard('{End}')
    expect(handle).toHaveAttribute('aria-valuenow', '280')
    await user.dblClick(handle)
    expect(handle).toHaveAttribute('aria-valuenow', '360')
    window.localStorage.removeItem('harflex.sidePanelWidth')
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: viewport })
  })

  it('switches tabs with the keyboard and exposes empty and loading states', async () => {
    const user = userEvent.setup()
    const { rerender } = render(<App />)
    screen.getByRole('tab', { name: 'Conversa' }).focus()
    await user.keyboard('{ArrowRight}')
    expect(screen.getByRole('tab', { name: 'Artefatos' })).toHaveFocus()
    expect(screen.getByRole('tabpanel')).toHaveTextContent('Nenhum artefato')
    await user.keyboard('{End}')
    expect(screen.getByRole('tabpanel')).toHaveTextContent('Nenhuma métrica')
    rerender(<App workState="loading" />)
    expect(screen.getByRole('status')).toHaveTextContent('Carregando área de trabalho')
  })

  it('opens the chat on Casual and restores the Professional document workspace', async () => {
    const user = userEvent.setup(), { backend } = createFakeBackend()
    installPipelineDesignFixture(backend, undefined, { pipeline: designPipeline() })
    const prepare = vi.spyOn(backend, 'preparePipelineDesign')
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Pipelines' }))
    await screen.findByRole('heading', { name: 'Preparar trabalho' })
    const modes = screen.getByRole('group', { name: 'Modo de uso' })
    await user.click(within(modes).getByRole('button', { name: 'Casual' }))
    expect(await screen.findByLabelText('Mensagem inicial')).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Preparar trabalho' })).not.toBeInTheDocument()
    await user.click(within(modes).getByRole('button', { name: 'Professional' }))
    expect(await screen.findByRole('heading', { name: 'Preparar trabalho' })).toBeInTheDocument()
    expect(prepare).not.toHaveBeenCalled()
  })

  it('preserves an unsent Discovery when switching between Professional and Casual', async () => {
    const user = userEvent.setup(), { backend } = createFakeBackend()
    installPipelineDesignFixture(backend, undefined, { pipeline: designPipeline() })
    const create = vi.spyOn(backend, 'createAuthoringPipeline')
    render(<StrictMode><App backend={backend} /></StrictMode>)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Pipelines' }))
    await user.click(await screen.findByRole('button', { name: 'Novo pipeline' }))
    await user.type(screen.getByLabelText('Discovery', { exact: true }), 'Meu Discovery ainda não enviado')
    const modes = screen.getByRole('group', { name: 'Modo de uso' })
    await user.click(within(modes).getByRole('button', { name: 'Casual' }))
    await screen.findByLabelText('Mensagem inicial')
    await user.click(within(modes).getByRole('button', { name: 'Professional' }))
    expect(await screen.findByLabelText('Discovery', { exact: true })).toHaveValue('Meu Discovery ainda não enviado')
    expect(create).not.toHaveBeenCalled()
  })

  it('reuses the creation request after switching modes while its response is pending', async () => {
    const user = userEvent.setup(), { backend } = createFakeBackend()
    installPipelineDesignFixture(backend, undefined, { pipeline: designPipeline() })
    const created: Pipeline = { ...designPipeline(), id: 'new-pipeline', preparationExperience: undefined }
    let finish!: (value: Pipeline) => void
    const create = vi.spyOn(backend, 'createAuthoringPipeline').mockImplementationOnce(() => new Promise(resolve => { finish = resolve })).mockResolvedValue(created)
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Pipelines' }))
    await user.click(await screen.findByRole('button', { name: 'Novo pipeline' }))
    await user.type(screen.getByLabelText('Discovery', { exact: true }), 'Discovery com resposta pendente')
    await user.click(screen.getByRole('button', { name: 'Criar pipeline' }))
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1))
    const intent = create.mock.calls[0][0]
    const modes = screen.getByRole('group', { name: 'Modo de uso' })
    await user.click(within(modes).getByRole('button', { name: 'Casual' }))
    await screen.findByLabelText('Mensagem inicial')
    await act(async () => finish(created))
    await user.click(within(modes).getByRole('button', { name: 'Professional' }))
    await screen.findByLabelText('Discovery', { exact: true })
    await user.click(screen.getByRole('button', { name: 'Criar pipeline' }))
    await waitFor(() => expect(create).toHaveBeenCalledTimes(2))
    expect(create.mock.calls[1][0]).toEqual(intent)
  })

  it('switches between Professional and Casual without replacing the active session', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const timestamp = '2026-09-29T10:00:00Z'
    const session: Session = { id: 'session-1', workspaceId: 'workspace-1', backendId: 'local', status: 'ready', resumable: true, createdAt: timestamp, updatedAt: timestamp }
    backend.listSessions = vi.fn(async () => [session])
    backend.listEvents = vi.fn(async id => [{ id: `${id}-user`, streamId: id, sequence: 1, type: 'message.user', data: { role: 'user', content: 'Corrigir a retomada do Codex' }, createdAt: timestamp }])
    localStorage.clear()
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/api-faturas')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await startFreeSession(user)
    expect(await screen.findByLabelText('Mensagem')).toBeInTheDocument()
    const topModes = screen.getByRole('group', { name: 'Modo de uso' })
    expect(within(topModes).getByRole('button', { name: 'Professional' })).toHaveAttribute('aria-pressed', 'true')

    await user.click(within(topModes).getByRole('button', { name: 'Casual' }))
    expect(await screen.findByRole('button', { name: /Corrigir a retomada do Codex/ })).toHaveAttribute('aria-current', 'true')
    expect(screen.getByLabelText('Mensagem')).toBeInTheDocument()
    expect(within(topModes).getByRole('button', { name: 'Casual' })).toHaveAttribute('aria-pressed', 'true')
    expect(localStorage.getItem('harflex:view-mode')).toBe('casual')

    await user.click(within(topModes).getByRole('button', { name: 'Professional' }))
    expect(screen.getByRole('navigation', { name: 'Navegação principal' })).toBeInTheDocument()
    expect(screen.getByLabelText('Mensagem')).toBeInTheDocument()
    expect(within(topModes).getByRole('button', { name: 'Professional' })).toHaveAttribute('aria-pressed', 'true')
    expect(localStorage.getItem('harflex:view-mode')).toBe('professional')
  })

  it('starts a Casual chat with the first message and records an explicit SDD bypass', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const createDirectSession = vi.spyOn(backend, 'createDirectSession')
    const prompt = vi.spyOn(backend, 'prompt')
    localStorage.setItem('harflex:view-mode', 'casual')
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/todo-app')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(await screen.findByRole('button', { name: 'Novo chat' }))
    await user.type(await screen.findByLabelText('Mensagem inicial'), 'Criar um TODO em TypeScript')
    await user.click(screen.getByRole('button', { name: 'Enviar' }))

    await waitFor(() => expect(createDirectSession).toHaveBeenCalledWith({ workspaceId: 'workspace-1', backendId: 'local', reason: 'Conversa livre no modo Casual' }))
    expect(await screen.findByLabelText('Mensagem')).toBeInTheDocument()
    await waitFor(() => expect(prompt).toHaveBeenCalledWith('session-1', 'Criar um TODO em TypeScript'))
    expect(await screen.findByText('Criar um TODO em TypeScript')).toBeInTheDocument()
  })

  it('recovers a failed read-only chat into a new Casual chat with its last request prefilled', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const timestamp = '2026-09-29T10:00:00Z'
    const failed: Session = { id: 'failed-codex', workspaceId: 'workspace-1', backendId: 'codex', status: 'failed', resumable: false, createdAt: timestamp, updatedAt: timestamp }
    backend.listBackends = async () => [{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }]
    backend.listSessions = vi.fn(async () => [failed])
    backend.openSession = vi.fn(async () => failed)
    backend.listEvents = vi.fn(async (id, after = 0) => [
      { id: 'run-start', streamId: id, sequence: 1, type: 'external.run.started', data: { adapter: 'codex' }, createdAt: timestamp },
      { id: 'user-request', streamId: id, sequence: 2, type: 'message.user', data: { role: 'user', content: 'Criar uma lista TODO com TypeScript' }, createdAt: timestamp },
      { id: 'run-failed', streamId: id, sequence: 3, type: 'external.run.failed', data: { reason: 'execution_failed' }, createdAt: timestamp },
    ].filter(event => event.sequence > after))
    const prompt = vi.spyOn(backend, 'prompt')
    localStorage.setItem('harflex:view-mode', 'casual')
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/todo-app')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(await screen.findByRole('button', { name: /Criar uma lista TODO com TypeScript/ }))
    expect(await screen.findByText(/Somente leitura: esta execução foi encerrada/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Continuar em novo chat' }))
    expect(await screen.findByLabelText('Mensagem inicial')).toHaveValue('Criar uma lista TODO com TypeScript')
    expect(prompt).not.toHaveBeenCalled()
  })

  it('does not carry a recovered prompt into a different Casual workspace', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const timestamp = '2026-09-29T10:00:00Z'
    const failed: Session = { id: 'failed-codex', workspaceId: 'workspace-1', backendId: 'codex', status: 'failed', resumable: false, createdAt: timestamp, updatedAt: timestamp }
    backend.listBackends = async () => [{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }]
    backend.openWorkspace = vi.fn(async path => ({ id: path.endsWith('other-project') ? 'workspace-2' : 'workspace-1', path, profile: 'ask', available: true }))
    backend.listSessions = vi.fn(async workspaceId => workspaceId === 'workspace-1' ? [failed] : [])
    backend.openSession = vi.fn(async () => failed)
    backend.listEvents = vi.fn(async (id, after = 0) => [
      { id: 'run-start', streamId: id, sequence: 1, type: 'external.run.started', data: { adapter: 'codex' }, createdAt: timestamp },
      { id: 'user-request', streamId: id, sequence: 2, type: 'message.user', data: { role: 'user', content: 'Recuperar pedido do projeto original' }, createdAt: timestamp },
      { id: 'run-failed', streamId: id, sequence: 3, type: 'external.run.failed', data: { reason: 'execution_failed' }, createdAt: timestamp },
    ].filter(event => event.sequence > after))
    const prompt = vi.spyOn(backend, 'prompt')
    localStorage.setItem('harflex:view-mode', 'casual')
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/todo-app')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(await screen.findByRole('button', { name: /Recuperar pedido do projeto original/ }))
    expect(await screen.findByText(/Somente leitura: esta execução foi encerrada/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Continuar em novo chat' }))
    expect(await screen.findByLabelText('Mensagem inicial')).toHaveValue('Recuperar pedido do projeto original')
    expect(prompt).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: 'Adicionar projeto' }))
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/other-project')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    expect(await screen.findByLabelText('Mensagem inicial')).toHaveValue('')
    expect(prompt).not.toHaveBeenCalled()
  })

  it('carries an unsent Casual draft into the next chat without executing it', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const timestamp = '2026-09-29T10:00:00Z'
    const session: Session = { id: 'session-1', workspaceId: 'workspace-1', backendId: 'local', status: 'completed', resumable: true, createdAt: timestamp, updatedAt: timestamp }
    backend.listSessions = async () => [session]
    backend.openSession = async () => session
    backend.listEvents = async id => [{ id: `${id}-message`, streamId: id, sequence: 1, type: 'message.user', data: { content: 'Pedido anterior' }, createdAt: timestamp }]
    const prompt = vi.spyOn(backend, 'prompt')
    localStorage.setItem('harflex:view-mode', 'casual')
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/todo-app')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(await screen.findByRole('button', { name: /Pedido anterior/ }))
    await user.type(await screen.findByLabelText('Mensagem'), 'Rascunho não enviado')
    await user.click(screen.getByRole('button', { name: 'Levar rascunho para novo chat' }))

    expect(await screen.findByLabelText('Mensagem inicial')).toHaveValue('Rascunho não enviado')
    expect(prompt).not.toHaveBeenCalled()
  })

  it('rejects a history readback that belongs to another workspace and restores the sidebar', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const timestamp = '2026-09-29T10:00:00Z'
    const listed: Session = { id: 'session-foreign', workspaceId: 'workspace-1', backendId: 'local', status: 'ready', resumable: true, createdAt: timestamp, updatedAt: timestamp }
    backend.listSessions = async () => [listed]
    backend.openSession = async () => ({ ...listed, workspaceId: 'workspace-other' })
    backend.listEvents = async id => [{ id: `${id}-user`, streamId: id, sequence: 1, type: 'message.user', data: { content: 'Conversa de outro projeto' }, createdAt: timestamp }]
    localStorage.setItem('harflex:view-mode', 'casual')
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/todo-app')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(await screen.findByRole('button', { name: /Conversa de outro projeto/ }))

    expect(await screen.findByRole('alert')).toHaveTextContent(/pertence a outro projeto/)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Novo chat' })).toBeEnabled())
  })

  it('replaces a pending Casual session open with the newer chat selection', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const timestamp = '2026-09-29T10:00:00Z'
    const first: Session = { id: 'session-first', workspaceId: 'workspace-1', backendId: 'local', status: 'completed', resumable: true, createdAt: timestamp, updatedAt: timestamp }
    const second: Session = { ...first, id: 'session-second' }
    backend.listSessions = async () => [first, second]
    let resolveFirst!: (session: Session) => void
    backend.openSession = vi.fn(id => id === first.id
      ? new Promise<Session>(resolve => { resolveFirst = resolve })
      : Promise.resolve(second))
    backend.listEvents = async id => {
      const prompt = id === first.id ? 'Primeiro pedido' : 'Segundo pedido'
      const answer = id === first.id ? 'Resposta da primeira conversa' : 'Resposta da segunda conversa'
      return [
        { id: `${id}-user`, streamId: id, sequence: 1, type: 'message.user', data: { content: prompt }, createdAt: timestamp },
        { id: `${id}-assistant`, streamId: id, sequence: 2, type: 'message.assistant', data: { content: answer }, createdAt: timestamp },
        { id: `${id}-done`, streamId: id, sequence: 3, type: 'run.completed', data: {}, createdAt: timestamp },
      ]
    }
    localStorage.setItem('harflex:view-mode', 'casual')
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/todo-app')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await user.click(await screen.findByRole('button', { name: /Primeiro pedido/ }))
    await waitFor(() => expect(backend.openSession).toHaveBeenCalledWith(first.id, 'workspace-1'))
    expect(await screen.findByText(/Carregando conversa/)).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /Segundo pedido/ }))
    expect(await screen.findByText('Resposta da segunda conversa')).toBeInTheDocument()
    await act(async () => { resolveFirst(first) })
    expect(screen.getByText('Resposta da segunda conversa')).toBeInTheDocument()
    expect(screen.queryByText('Resposta da primeira conversa')).not.toBeInTheDocument()
    expect(screen.getByLabelText('Mensagem')).toBeEnabled()
  })

  it('prevents the Casual pipeline shortcut from replacing a chat during replay', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const timestamp = '2026-09-29T10:00:00Z'
    const session: Session = { id: 'session-1', workspaceId: 'workspace-1', backendId: 'local', status: 'completed', resumable: true, createdAt: timestamp, updatedAt: timestamp }
    const pipeline: Pipeline = { id: 'pipeline-1', workspaceId: 'workspace-1', title: 'Exportar dados', objective: 'Criar CSV', currentStage: 'code', stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'active', eval: 'pending' }, revision: 4, artifacts: {}, createdAt: timestamp, updatedAt: timestamp }
    backend.listPipelines = async () => [pipeline]
    backend.getPipeline = async () => pipeline
    backend.getPipelineForSession = async () => pipeline
    backend.listSessions = async () => [session]
    backend.listEvents = async id => [{ id: `${id}-user`, streamId: id, sequence: 1, type: 'message.user', data: { content: 'Abrir a conversa' }, createdAt: timestamp }]
    let resolveOpen!: (value: Session) => void
    backend.openSession = () => new Promise(resolve => { resolveOpen = resolve })
    localStorage.setItem('harflex:view-mode', 'casual')
    render(<App backend={backend} />)
    await user.type(await screen.findByLabelText('Caminho da pasta'), '/Users/dev/todo-app')
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    expect(screen.queryByRole('button', { name: 'Abrir pipeline Exportar dados, fase Code' })).not.toBeInTheDocument()
    await user.click(await screen.findByRole('button', { name: /Abrir a conversa/ }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Abrir pipeline Exportar dados, fase Code' })).toBeDisabled())
    await act(async () => { resolveOpen(session) })
    expect(await within(await screen.findByRole('list', { name: 'Conversa' })).findByText('Abrir a conversa')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Abrir pipeline Exportar dados, fase Code' })).toBeEnabled())
  })
})
