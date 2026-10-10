import type { ComponentProps } from 'react'
import { act, cleanup, render, screen, waitFor, within } from '@testing-library/react'
import { useState } from 'react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { createFakeBackend } from '../../test/fakeBackend'
import type { Pipeline, PipelineStage } from '../../lib/backend'
import { PipelinesPage } from './PipelinesPage'
import { stagePipeline, stageReadback } from '../../test/authoringStage'
import { designPipeline, designReadback, installPipelineDesignFixture } from '../../test/pipelineDesignFixture'

afterEach(cleanup)

it('collapses approved preparation during Code and opens it only for an explicit document inspection', async () => {
  const { backend } = createFakeBackend(), base = designPipeline(), run: Pipeline = { ...base, currentStage: 'code', stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'active', eval: 'pending' } }
  installPipelineDesignFixture(backend, undefined, { pipeline: run, design: { ...designReadback(run, true), state: 'approved', needsDerivation: true } })
  backend.preparePipelineDesign = vi.fn()
  backend.getPipelineStageActivity = async (pipelineId, stage) => ({ pipelineId, workspaceId: run.workspaceId, stage, status: 'completed', phase: '', sessionId: '', modelId: '', updatedAt: run.updatedAt })
  const props = { backend, backends: [{ id: 'api-1', name: 'API de teste', kind: 'api' as const, available: true }], preferredBackendId: 'api-1', preferredModelBackendId: 'api-1', preferredModelId: 'test-model', workspaceId: run.workspaceId, selectedPipeline: run, onSelectPipeline: () => undefined, onProjects: () => undefined, onStartRole: async () => undefined }
  const view = render(<PipelinesPage {...props} />)
  // During Code the screen is Code's bench; the approved documents are a stage away in the top bar.
  expect(await screen.findByRole('region', { name: 'Bancada Code' })).toBeInTheDocument()
  await waitFor(() => expect(screen.getByRole('button', { name: 'Executar Coder' })).toBeEnabled())
  expect(screen.queryByText('Discovery, SPEC e Plan aprovados')).not.toBeInTheDocument()
  expect(screen.queryByRole('heading', { name: 'Critérios de aceite' })).not.toBeInTheDocument()
  view.rerender(<PipelinesPage {...props} viewStage="spec" viewRequestId={1} />)
  const summary = await screen.findByText('Discovery, SPEC e Plan aprovados')
  await waitFor(() => expect(summary.closest('details')).toHaveAttribute('open'))
  expect(await screen.findByRole('heading', { name: 'Critérios de aceite' })).toBeInTheDocument()
  expect(screen.queryByRole('region', { name: 'Bancada Code' })).not.toBeInTheDocument()
  expect(backend.preparePipelineDesign).not.toHaveBeenCalled()
})

it('consumes a Discovery seed once and keeps the next new work and project blank', async () => {
  const { backend } = createFakeBackend(), fixture = installPipelineDesignFixture(backend), consumed = vi.fn(), user = userEvent.setup()
  function Harness({ request, workspaceId = 'workspace-1' }: { request: number; workspaceId?: string }) {
    const [run, setRun] = useState<Pipeline>()
    return <PipelinesPage backend={backend} backends={[]} workspaceId={workspaceId} selectedPipeline={run} newWorkRequest={request} initialDiscovery="Texto do primeiro trabalho" initialDiscoveryRequestId="seed-once" onInitialDiscoveryConsumed={consumed} onSelectPipeline={setRun} onProjects={() => undefined} onStartRole={async () => undefined} />
  }
  const view = render(<Harness request={1} />)
  expect(await screen.findByLabelText('Discovery', { exact: true })).toHaveValue('Texto do primeiro trabalho')
  await user.click(screen.getByRole('button', { name: 'Criar pipeline' }))
  await waitFor(() => expect(fixture.prepareCount()).toBe(1))
  view.rerender(<Harness request={2} />)
  expect(await screen.findByLabelText('Discovery', { exact: true })).toHaveValue('')
  view.rerender(<Harness request={2} workspaceId="workspace-2" />)
  expect(await screen.findByLabelText('Discovery', { exact: true })).toHaveValue('')
  expect(consumed).toHaveBeenCalledTimes(1); expect(consumed).toHaveBeenCalledWith('seed-once')
})

it('opens SPEC as a readable AI document after Discovery, without manual editing or inference', async () => {
  const { backend } = createFakeBackend()
  backend.listPipelines = async () => [stagePipeline]
  backend.getPipeline = async () => stagePipeline
  backend.getAuthoringStage = async () => stageReadback
  backend.generateAuthoringStage = vi.fn()
  render(<PipelinesPage backend={backend} backends={[]} workspaceId="workspace-1" selectedPipeline={stagePipeline} onSelectPipeline={() => undefined} onProjects={() => undefined} onStartRole={async () => undefined} />)
  expect(await screen.findByRole('article', { name: 'Documento SPEC versão 1' })).toBeInTheDocument()
  expect(within(screen.getByLabelText('Fases do pipeline')).getByText('Aguardando decisão')).toBeInTheDocument()
  expect(screen.queryByLabelText('Artefato da fase')).not.toBeInTheDocument()
  expect(backend.generateAuthoringStage).not.toHaveBeenCalled()
})

it('explains that Professional SDD needs an API profile and routes setup to Settings', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const date = '2026-09-29T10:00:00Z'
  const run: Pipeline = { id: 'authoring-provider', workspaceId: 'workspace-1', kind: 'ai_authoring', derivedFromPipelineId: '', discoveryFrozenVersion: 0, title: 'TODO', objective: '', currentStage: 'discovery', stageStatus: { discovery: 'active', spec: 'pending', plan: 'pending', code: 'pending', eval: 'pending' }, revision: 1, artifacts: { discovery: { stage: 'discovery', version: 1, content: 'Criar TODO local', author: 'user', sourceSessionId: '', updatedAt: date } }, createdAt: date, updatedAt: date }
  backend.listPipelines = async () => [run]
  backend.getPipeline = async () => run
  backend.listProviderProfiles = async () => []
  const onSettings = vi.fn()
  render(<PipelinesPage backend={backend} backends={[]} workspaceId="workspace-1" selectedPipeline={run} onSelectPipeline={() => undefined} onProjects={() => undefined} onSettings={onSettings} onStartRole={async () => undefined} />)

  expect(await screen.findByText('Este pipeline precisa de um executor disponível')).toBeInTheDocument()
  expect(screen.queryByRole('combobox', { name: 'Provedor desta etapa' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Concluir Discovery e iniciar brainstorming' })).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Configurar provedor API' }))
  expect(onSettings).toHaveBeenCalledOnce()
})

it('bloqueia Codex como Evaluator no Professional SDD por falta de confinamento de leitura', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const date = '2026-09-27T10:00:00Z'
  const run: Pipeline = { id: 'pipeline-eval', workspaceId: 'workspace-1', title: 'Avaliar', objective: 'Validar', currentStage: 'eval', stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'completed', eval: 'active' }, revision: 5, artifacts: {}, createdAt: date, updatedAt: date }
  backend.listPipelines = async () => [run]
  backend.getPipeline = async () => run
  render(<PipelinesPage backend={backend} backends={[{ id: 'api', name: 'API', kind: 'api', available: true }, { id: 'codex', name: 'Codex', kind: 'cli', available: true }]} workspaceId="workspace-1" selectedPipeline={run} onSelectPipeline={() => undefined} onProjects={() => undefined} onStartRole={async () => undefined} />)
  expect(await screen.findByRole('button', { name: 'Rodar QA' })).toBeDisabled()
  await user.click(within(await screen.findByTestId('picker-qa-role-eval')).getByRole('button'))
  expect(await screen.findByText(/Codex CLI pode ler arquivos fora do projeto/i)).toBeInTheDocument()
  expect(screen.getByRole('option', { name: 'Codex · indisponível no SDD' })).toHaveAttribute('aria-disabled', 'true')
})

it.each(['code', 'eval'] as const)('mostra recuperação em Configurações quando %s não tem perfil API', async stage => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const date = '2026-09-30T10:00:00Z'
  const run: Pipeline = { id: `pipeline-${stage}-no-api`, workspaceId: 'workspace-1', title: 'Sem provedor', objective: 'Testar estado vazio', currentStage: stage,
    stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: stage === 'code' ? 'active' : 'completed', eval: stage === 'eval' ? 'active' : 'pending' }, revision: 6,
    artifacts: {}, createdAt: date, updatedAt: date }
  backend.listPipelines = async () => [run]
  backend.getPipeline = async () => run
  const onSettings = vi.fn()
  render(<PipelinesPage backend={backend} backends={[{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }]} preferredBackendId="codex"
    preferredModelBackendId="codex" preferredModelId="gpt-test" workspaceId="workspace-1" selectedPipeline={run} onSelectPipeline={() => undefined} onProjects={() => undefined} onSettings={onSettings} onStartRole={async () => undefined} />)

  expect(await screen.findByText('Code e QA precisam de um provedor API')).toBeInTheDocument()
  expect(screen.queryByRole('combobox', { name: 'Executor da fase' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Rodar QA' })).not.toBeInTheDocument()
  expect(screen.queryByRole('combobox', { name: `Modelo da fase ${stage === 'code' ? 'Code' : 'QA'}` })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: stage === 'code' ? 'Executar Coder' : 'Executar Evaluator' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: stage === 'code' ? 'Verificar código' : 'Registrar avaliação' })).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Configurar provedor API' }))
  expect(onSettings).toHaveBeenCalledOnce()
})

it('aplica um patch somente após o QA concluído e confirmação explícita', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const date = '2026-09-30T10:00:00Z'
  const run: Pipeline = { id: 'authoring-applied', workspaceId: 'workspace-1', kind: 'ai_authoring', derivedFromPipelineId: '', discoveryFrozenVersion: 1,
    title: 'TODO', objective: 'Criar TODO', currentStage: '',
    stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'completed', eval: 'completed' }, revision: 10,
    artifacts: { discovery: { stage: 'discovery', version: 1, content: 'Criar TODO', author: 'user', sourceSessionId: '', updatedAt: date },
      spec: { stage: 'spec', version: 1, content: 'SPEC aprovada', author: 'ai', sourceSessionId: 'spec-session', updatedAt: date },
      plan: { stage: 'plan', version: 1, content: 'Plan aprovado', author: 'ai', sourceSessionId: 'plan-session', updatedAt: date },
      code: { stage: 'code', version: 1, content: '+function addTask() {}', author: 'ai', sourceSessionId: 'coder-session', updatedAt: date },
      eval: { stage: 'eval', version: 1, content: '{"passed":true}', author: 'ai', sourceSessionId: 'evaluator-session', updatedAt: date } },
    createdAt: date, updatedAt: date }
  const applied = { ...run, codeAppliedAt: date }
  backend.listPipelines = async () => [run]
  backend.getPipeline = async () => run
  backend.applyPipelineCode = vi.fn(async () => applied)
  const onSelectPipeline = vi.fn()
  function Harness() {
    const [selected, setSelected] = useState(run)
    return <PipelinesPage backend={backend} backends={[]} workspaceId="workspace-1" selectedPipeline={selected} onSelectPipeline={pipeline => { onSelectPipeline(pipeline); if (pipeline) setSelected(pipeline) }} onProjects={() => undefined} onStartRole={async () => undefined} />
  }
  render(<Harness />)

  const apply = await screen.findByRole('button', { name: 'Aplicar patch aprovado' })
  expect(apply).toBeDisabled()
  await user.click(screen.getByRole('checkbox', { name: /Confirmo aplicar ao projeto original/i }))
  await user.click(apply)
  expect(backend.applyPipelineCode).toHaveBeenCalledWith(run.id)
  expect(onSelectPipeline).toHaveBeenCalledWith(applied)
  expect(await screen.findByText(/Aplicado e conferido em/)).toBeInTheDocument()
})

it('mostra o Code como pendente de decisão humana e mantém o QA bloqueado', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const date = '2026-09-30T10:00:00Z'
  const run: Pipeline = { id: 'pipeline-code-review', workspaceId: 'workspace-1', kind: 'ai_authoring', title: 'Revisar Code', objective: 'Criar TODO', currentStage: 'code',
    stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'waiting_user', eval: 'pending' }, revision: 8,
    artifacts: { discovery: { stage: 'discovery', version: 1, content: 'Criar TODO', author: 'user', sourceSessionId: '', updatedAt: date },
      spec: { stage: 'spec', version: 1, content: 'SPEC aprovada', author: 'ai', sourceSessionId: 'spec-session', updatedAt: date },
      plan: { stage: 'plan', version: 1, content: 'Plan aprovado', author: 'ai', sourceSessionId: 'plan-session', updatedAt: date },
      code: { stage: 'code', version: 1, content: 'diff --git a/index.html b/index.html\n+<h1>TODO</h1>', author: 'ai', sourceSessionId: 'coder-session', contentDigest: 'a'.repeat(64), updatedAt: date } },
    createdAt: date, updatedAt: date }
  const approved: Pipeline = { ...run, currentStage: 'eval', revision: 9, stageStatus: { ...run.stageStatus, code: 'completed', eval: 'active' }, executionReviews: [{ stage: 'code', version: 1, content: run.artifacts.code!.content, contentDigest: 'a'.repeat(64), sourceSessionId: 'coder-session', decision: 'approve', actor: 'local_user', feedback: '', createdAt: date }] }
  backend.listPipelines = async () => [run]
  backend.getPipeline = async () => run
  backend.decidePipelineExecutionArtifact = vi.fn(async () => approved)
  const onSelectPipeline = vi.fn()
  render(<PipelinesPage backend={backend} backends={[]} workspaceId="workspace-1" selectedPipeline={run} onSelectPipeline={onSelectPipeline} onProjects={() => undefined} onStartRole={async () => undefined} />)

  expect(await screen.findByRole('group', { name: 'Decisão sobre o Code' })).toBeInTheDocument()
  expect(screen.getByRole('region', { name: 'Bancada Code' })).toBeInTheDocument()
  expect(screen.getByText(/SHA-256 do artefato/)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Aprovar Code e iniciar QA' })).toBeEnabled()
  expect(screen.queryByRole('button', { name: 'Executar Coder' })).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Pedir revisão' }))
  expect(screen.getByRole('button', { name: 'Pedir revisão do Code' })).toBeDisabled()
  await user.type(screen.getByLabelText('O que deve mudar no Code'), 'Adicionar estado vazio acessível')
  expect(screen.getByRole('button', { name: 'Pedir revisão do Code' })).toBeEnabled()
  await user.click(screen.getByRole('button', { name: 'Voltar' }))
  await user.click(screen.getByRole('button', { name: 'Aprovar Code e iniciar QA' }))
  await waitFor(() => expect(backend.decidePipelineExecutionArtifact).toHaveBeenCalledWith(expect.objectContaining({ pipelineId: run.id, stage: 'code', artifactVersion: 1, artifactDigest: 'a'.repeat(64), decision: 'approve', pipelineRevision: 8 })))
  expect(onSelectPipeline).toHaveBeenCalledWith(approved)
})

it('shows a failed QA as a lab report: failures come chosen, improvements wait, and it cannot be approved', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const date = '2026-09-30T10:00:00Z'
  const report = { passed: false, checks: [{ name: 'Testes unitários', kind: 'unit', command: 'npm test', status: 'failed', summary: '1 teste falhou' }, { name: 'Build', kind: 'build', command: 'npm run build', status: 'passed', summary: 'ok' }],
    findings: ['O formulário não tem rótulo acessível'], improvements: ['Adicionar atalho de teclado'], criteria: [{ criterion: 'título visível', evidence: 'TODO' }], criteriaSource: { sourceStage: 'discovery', sourceVersion: 1 } }
  const run: Pipeline = { id: 'pipeline-eval-review', workspaceId: 'workspace-1', kind: 'ai_authoring', title: 'Revisar Eval', objective: 'Validar TODO', currentStage: 'eval',
    stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'completed', eval: 'waiting_user' }, revision: 12,
    artifacts: { discovery: { stage: 'discovery', version: 1, content: 'Aceitar uma tarefa com título visível', author: 'user', sourceSessionId: '', updatedAt: date },
      code: { stage: 'code', version: 1, content: '+<h1>TODO</h1>', author: 'ai', sourceSessionId: 'coder-session', contentDigest: 'b'.repeat(64), updatedAt: date },
      eval: { stage: 'eval', version: 1, content: JSON.stringify(report), author: 'ai', sourceSessionId: 'eval-session', contentDigest: 'd'.repeat(64), updatedAt: date } },
    executionReviews: [{ stage: 'code', version: 1, content: '+<h1>TODO</h1>', contentDigest: 'b'.repeat(64), sourceSessionId: 'coder-session', decision: 'approve', actor: 'local_user', feedback: '', createdAt: date }],
    createdAt: date, updatedAt: date }
  backend.listPipelines = async () => [run]
  backend.getPipeline = async () => run
  render(<PipelinesPage backend={backend} backends={[{ id: 'api', name: 'API', kind: 'api', available: true }]} workspaceId="workspace-1" selectedPipeline={run} onSelectPipeline={() => undefined} onProjects={() => undefined} onStartRole={async () => undefined} />)

  expect(await screen.findByRole('region', { name: 'Laboratório de QA' })).toBeInTheDocument()
  expect(screen.getByText('Encontrou problemas')).toBeInTheDocument()
  expect(screen.getByText('npm test')).toBeInTheDocument()
  expect(screen.getByRole('checkbox', { name: 'O formulário não tem rótulo acessível' })).toBeChecked()
  expect(screen.getByRole('checkbox', { name: 'Adicionar atalho de teclado' })).not.toBeChecked()
  expect(screen.queryByRole('button', { name: 'Aprovar QA e seguir para PRs' })).not.toBeInTheDocument()
  await user.click(screen.getByRole('checkbox', { name: 'Adicionar atalho de teclado' }))
  expect(screen.getByRole('button', { name: 'Corrigir os selecionados (2)' })).toBeInTheDocument()
})

it('reabre Code legado sem autoaprovar e preserva Eval anterior como desatualizada', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const date = '2026-09-30T10:00:00Z'
  const run: Pipeline = { id: 'pipeline-legacy-recovery', workspaceId: 'workspace-1', kind: 'ai_authoring', title: 'Recuperar pipeline', objective: 'TODO', currentStage: '',
    stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'completed', eval: 'completed' }, revision: 18,
    codeReviewRecoveryStatus: 'available',
    artifacts: { discovery: { stage: 'discovery', version: 1, content: 'Criar TODO', author: 'user', sourceSessionId: '', updatedAt: date },
      spec: { stage: 'spec', version: 1, content: 'SPEC', author: 'ai', sourceSessionId: 'spec-session', updatedAt: date },
      plan: { stage: 'plan', version: 1, content: 'Plan', author: 'ai', sourceSessionId: 'plan-session', updatedAt: date },
      code: { stage: 'code', version: 1, content: '+<h1>TODO</h1>', author: 'ai', sourceSessionId: 'coder-session', contentDigest: 'e'.repeat(64), reviewStatus: 'legacy_unreviewed', updatedAt: date },
      eval: { stage: 'eval', version: 2, content: 'Eval antiga sem recibo', author: 'ai', sourceSessionId: 'evaluator-session', updatedAt: date } },
    archivedArtifacts: [{ stage: 'eval', version: 1, content: 'Avaliação arquivada com critério anterior', author: 'ai', sourceSessionId: 'evaluator-original', contentDigest: 'a'.repeat(64), reason: 'superseded', createdAt: date }],
    createdAt: date, updatedAt: date }
  const recovered: Pipeline = { ...run, currentStage: 'code', revision: 19, stageStatus: { ...run.stageStatus, code: 'waiting_user', eval: 'pending' },
    artifacts: { ...run.artifacts, eval: { ...run.artifacts.eval!, reviewStatus: 'stale' } } }
  backend.listPipelines = async () => [run]
  backend.getPipeline = async () => run
  backend.reopenPipelineCodeReview = vi.fn(async () => recovered)
  const onSelectPipeline = vi.fn()
  render(<PipelinesPage backend={backend} backends={[]} workspaceId="workspace-1" selectedPipeline={run} onSelectPipeline={onSelectPipeline} onProjects={() => undefined} onStartRole={async () => undefined} />)

  expect(await screen.findByRole('region', { name: 'Recuperar revisão de Code' })).toBeInTheDocument()
  expect(screen.getByText(/Revisar diff salvo de Code/)).toBeInTheDocument()
  expect(screen.getByText('Versões anteriores')).toBeInTheDocument()
  await user.click(screen.getByText('QA v1 · substituída'))
  expect(screen.getByText('Avaliação arquivada com critério anterior')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Aplicar patch aprovado' })).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Reabrir revisão de Code' }))
  await waitFor(() => expect(backend.reopenPipelineCodeReview).toHaveBeenCalledWith({ pipelineId: run.id, artifactVersion: 1, artifactDigest: 'e'.repeat(64), pipelineRevision: 18 }))
  expect(onSelectPipeline).toHaveBeenCalledWith(recovered)
})

it('não oferece reabrir Code sem snapshot e deriva uma nova execução do Discovery salvo', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const date = '2026-09-30T10:00:00Z'
  const run: Pipeline = { id: 'pipeline-old-snapshot', workspaceId: 'workspace-1', kind: 'legacy', title: 'TODO legado', objective: 'TODO', currentStage: '',
    stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'completed', eval: 'completed' }, revision: 8,
    codeReviewRecoveryStatus: 'snapshot_unavailable', codeReviewRecoveryReason: 'O snapshot isolado desta versão não está disponível.',
    artifacts: { discovery: { stage: 'discovery', version: 1, content: 'Criar TODO com persistência local', author: 'legacy/manual', sourceSessionId: '', updatedAt: date },
      code: { stage: 'code', version: 3, content: '+<h1>TODO legado</h1>', author: 'ai', sourceSessionId: 'old-coder', contentDigest: 'f'.repeat(64), updatedAt: date } },
    createdAt: date, updatedAt: date }
  const child: Pipeline = { ...run, id: 'pipeline-recovery-child', kind: 'ai_authoring', derivedFromPipelineId: run.id, currentStage: 'discovery', discoveryFrozenVersion: 0, revision: 1,
    stageStatus: { discovery: 'active', spec: 'pending', plan: 'pending', code: 'pending', eval: 'pending' },
    artifacts: { discovery: { stage: 'discovery', version: 1, content: 'Criar TODO com persistência local', author: 'user', sourceSessionId: '', updatedAt: date } } }
  backend.listPipelines = async () => [run]
  backend.getPipeline = async () => run
  backend.deriveAuthoringPipeline = vi.fn(async () => child)
  const onSelectPipeline = vi.fn()
  render(<PipelinesPage backend={backend} backends={[]} workspaceId="workspace-1" selectedPipeline={run} onSelectPipeline={onSelectPipeline} onProjects={() => undefined} onStartRole={async () => undefined} />)

  expect(await screen.findByText('O snapshot isolado desta versão não está disponível.')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Reabrir revisão de Code' })).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Criar nova execução a partir do Discovery' }))
  await waitFor(() => expect(backend.deriveAuthoringPipeline).toHaveBeenCalledWith(expect.objectContaining({ parentPipelineId: run.id, expectedRevision: run.revision, discovery: 'Criar TODO com persistência local' })))
  expect(onSelectPipeline).toHaveBeenCalledWith(child)
})

it('mostra a prévia da cópia não-Git e exige confirmação explícita antes do Coder', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const date = '2026-09-29T10:00:00Z'
  const run: Pipeline = { id: 'pipeline-code', workspaceId: 'workspace-1', title: 'Implementar', objective: 'Criar TODO', currentStage: 'code', stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'active', eval: 'pending' }, revision: 4, artifacts: {}, createdAt: date, updatedAt: date }
  backend.listPipelines = async () => [run]
  backend.getPipeline = async () => run
  backend.previewPipelineCodeWorkspace = vi.fn(async () => ({ isGit: false, fileCount: 4, totalBytes: 1200, excludedPaths: ['.git', 'node_modules/'], unsafePaths: [] }))
  const profile = { id: 'api-work', name: 'API local', kind: 'openai_compatible', providerType: 'openai' as const, baseUrl: 'https://example.test/v1', model: 'api-model', hasCredential: true, endpointBlocked: false, updatedAt: new Date().toISOString() }
  backend.listProviderProfiles = async () => [profile]
  backend.queryHTTPModelCatalog = async query => ({ backendId: query.profileId, source: 'openai_models', destination: profile.name, profileRevision: 'api-revision', credentialToken: 'a'.repeat(64), searchTerm: '',
    models: [{ id: 'api-model', displayName: 'Modelo API', backendId: query.profileId, source: 'openai_models', availability: 'available' }], nextCursor: '', checkedAt: new Date().toISOString(), status: 'complete', complete: true, accountFiltered: true })
  const onStartRole = vi.fn(async () => undefined)
  render(<PipelinesPage backend={backend} backends={[{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }, { id: 'api-work', name: 'API local', kind: 'api', available: true }]} workspaceId="workspace-1" selectedPipeline={run} onSelectPipeline={() => undefined} onProjects={() => undefined} onStartRole={onStartRole} />)

  expect(await screen.findByText(/4 arquivos · 0.0 MB/)).toBeInTheDocument()
  await user.click(screen.getByText('Itens excluídos (2)'))
  expect(screen.getByText('node_modules/')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Executar Coder' })).toBeDisabled()
  const modelPicker = await screen.findByTestId('picker-pipeline-role-model-code')
  await waitFor(() => expect(within(modelPicker).getByRole('combobox', { name: 'Modelo da fase Code' })).toHaveValue('Modelo API'))
  await user.click(screen.getByRole('checkbox', { name: /Confirmo copiar estes arquivos/i }))
  await waitFor(() => expect(screen.getByRole('button', { name: 'Executar Coder' })).toBeEnabled())
  await user.click(screen.getByRole('button', { name: 'Executar Coder' }))
  expect(onStartRole).toHaveBeenCalledWith(run.id, 'api-work', 'coder', expect.objectContaining({ executor: 'api', modelId: 'api-model' }), true)
})

it('bloqueia a cópia quando a prévia encontra links ou arquivos fora do perímetro', async () => {
  const { backend } = createFakeBackend()
  const date = '2026-09-29T10:00:00Z'
  const run: Pipeline = { id: 'pipeline-code-unsafe-copy', workspaceId: 'workspace-1', title: 'Implementar', objective: 'Criar TODO', currentStage: 'code', stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'active', eval: 'pending' }, revision: 4, artifacts: {}, createdAt: date, updatedAt: date }
  backend.listPipelines = async () => [run]
  backend.getPipeline = async () => run
  backend.previewPipelineCodeWorkspace = async () => ({ isGit: false, fileCount: 2, totalBytes: 100, excludedPaths: [], unsafePaths: ['linked-secret'] })
  render(<PipelinesPage backend={backend} backends={[{ id: 'api', name: 'API', kind: 'api', available: true }]} workspaceId="workspace-1" selectedPipeline={run} onSelectPipeline={() => undefined} onProjects={() => undefined} onStartRole={async () => undefined} />)

  expect(await screen.findByText('linked-secret')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Executar Coder' })).toBeDisabled()
})

it('bloqueia o Coder Codex no Professional SDD após aprovar Plan', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const date = '2026-09-29T10:00:00Z'
  const run: Pipeline = { id: 'authoring-code', workspaceId: 'workspace-1', kind: 'ai_authoring', derivedFromPipelineId: '', discoveryFrozenVersion: 1,
    title: 'TODO', objective: 'Criar TODO com localStorage', currentStage: 'code',
    stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'active', eval: 'pending' }, revision: 7,
    artifacts: { discovery: { stage: 'discovery', version: 1, content: 'Criar TODO local', author: 'user', sourceSessionId: '', updatedAt: date },
      spec: { stage: 'spec', version: 1, content: 'SPEC aprovada', author: 'ai', sourceSessionId: 'spec-session', updatedAt: date },
      plan: { stage: 'plan', version: 1, content: 'Plan aprovado', author: 'ai', sourceSessionId: 'plan-session', updatedAt: date } },
    createdAt: date, updatedAt: date }
  backend.listPipelines = async () => [run]
  backend.getPipeline = async () => run
  backend.inspectRepository = async () => ({ isRepository: true, root: '/workspace', branch: 'main', files: [], stagedDiff: '', unstagedDiff: '', truncated: false })
  const checkedAt = new Date().toISOString()
  backend.queryCLIModelCatalog = vi.fn(async query => ({ backendId: query.backendId, source: 'codex_app_server', destination: 'Codex CLI', profileRevision: 'codex-revision', searchTerm: '',
    models: [{ id: 'provider/model-exact', displayName: 'Modelo global', backendId: 'codex', source: 'codex_app_server', availability: 'listed' }], nextCursor: '', checkedAt, status: 'complete' as const, complete: true, accountFiltered: true }))
  const onStartRole = vi.fn(async () => undefined)
  render(<PipelinesPage backend={backend} backends={[{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }, { id: 'api', name: 'API', kind: 'api', available: true }]} preferredBackendId="codex" preferredModelBackendId="codex" preferredModelId="provider/model-exact" workspaceId="workspace-1" selectedPipeline={run} onSelectPipeline={() => undefined} onProjects={() => undefined} onStartRole={onStartRole} />)

  expect(await screen.findByText(/Codex CLI pode ler arquivos fora do projeto/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Executar Coder' })).toBeDisabled()
  const provider = await screen.findByTestId('picker-pipeline-backend')
  await user.click(within(provider).getByRole('button'))
  expect(await screen.findByRole('option', { name: 'Codex CLI · indisponível no SDD' })).toHaveAttribute('aria-disabled', 'true')
  expect(onStartRole).not.toHaveBeenCalled()
  expect(backend.queryCLIModelCatalog).not.toHaveBeenCalled()
})

it('bloqueia o Evaluator Codex no Professional SDD sem confinamento de leitura', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const date = '2026-09-29T10:00:00Z'
  const run: Pipeline = { id: 'authoring-eval', workspaceId: 'workspace-1', kind: 'ai_authoring', derivedFromPipelineId: '', discoveryFrozenVersion: 1,
    title: 'TODO', objective: 'Criar TODO com localStorage', currentStage: 'eval',
    stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'completed', eval: 'active' }, revision: 9,
    artifacts: { discovery: { stage: 'discovery', version: 1, content: 'Criar TODO local', author: 'user', sourceSessionId: '', updatedAt: date },
      spec: { stage: 'spec', version: 1, content: 'SPEC aprovada', author: 'ai', sourceSessionId: 'spec-session', updatedAt: date },
      plan: { stage: 'plan', version: 1, content: 'Plan aprovado', author: 'ai', sourceSessionId: 'plan-session', updatedAt: date },
      code: { stage: 'code', version: 1, content: '+function addTask() {}', author: 'ai', sourceSessionId: 'coder-session', updatedAt: date } },
    createdAt: date, updatedAt: date }
  backend.listPipelines = async () => [run]
  backend.getPipeline = async () => run
  const checkedAt = new Date().toISOString()
  backend.queryCLIModelCatalog = async query => ({ backendId: query.backendId, source: 'codex_app_server', destination: 'Codex CLI', profileRevision: 'codex-revision', searchTerm: '',
    models: [{ id: 'provider/model-exact', displayName: 'Modelo global', backendId: 'codex', source: 'codex_app_server', availability: 'listed' }], nextCursor: '', checkedAt, status: 'complete', complete: true, accountFiltered: true })
  const onStartRole = vi.fn(async () => undefined)
  render(<PipelinesPage backend={backend} backends={[{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }, { id: 'api', name: 'API', kind: 'api', available: true }]} preferredBackendId="codex" preferredModelBackendId="codex" preferredModelId="provider/model-exact" workspaceId="workspace-1" selectedPipeline={run} onSelectPipeline={() => undefined} onProjects={() => undefined} onStartRole={onStartRole} />)

  expect(await screen.findByText(/Codex CLI pode ler arquivos fora do projeto/i)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Rodar QA' })).toBeDisabled()
  const provider = await screen.findByTestId('picker-qa-role-eval')
  await user.click(within(provider).getByRole('button'))
  expect(await screen.findByRole('option', { name: 'Codex CLI · indisponível no SDD' })).toHaveAttribute('aria-disabled', 'true')
  expect(onStartRole).not.toHaveBeenCalled()
})

for (const stage of ['code', 'eval'] as const) {
  it(`requires an API override for ${stage} when the global default is Codex CLI`, async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const date = '2026-09-29T10:00:00Z'
    const artifacts = {
      discovery: { stage: 'discovery' as const, version: 1, content: 'Criar TODO local', author: 'user' as const, sourceSessionId: '', updatedAt: date },
      spec: { stage: 'spec' as const, version: 1, content: 'SPEC aprovada', author: 'ai' as const, sourceSessionId: 'spec-session', updatedAt: date },
      plan: { stage: 'plan' as const, version: 1, content: 'Plan aprovado', author: 'ai' as const, sourceSessionId: 'plan-session', updatedAt: date },
    }
    const run: Pipeline = { id: `authoring-${stage}-model`, workspaceId: 'workspace-1', kind: 'ai_authoring', derivedFromPipelineId: '', discoveryFrozenVersion: 1,
      title: 'TODO', objective: 'Criar TODO com localStorage', currentStage: stage,
      stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: stage === 'eval' ? 'completed' : 'active', eval: stage === 'eval' ? 'active' : 'pending' },
      revision: 7, artifacts: stage === 'eval' ? { ...artifacts, code: { stage: 'code', version: 1, content: '+function addTask() {}', author: 'ai', sourceSessionId: 'coder-session', updatedAt: date } } : artifacts,
      createdAt: date, updatedAt: date }
    const checkedAt = new Date().toISOString()
    const profile = { id: 'api-1', name: 'API local', kind: 'openai_compatible', providerType: 'openai' as const, baseUrl: 'https://example.test/v1', model: 'api-global-model', hasCredential: true, endpointBlocked: false, updatedAt: checkedAt }
    backend.listPipelines = async () => [run]
    backend.getPipeline = async () => run
    backend.getSettings = async () => ({ defaultBackendId: 'codex', defaultModelBackendId: 'codex', defaultModelId: 'global-model' })
    backend.listProviderProfiles = async () => [profile]
    backend.queryCLIModelCatalog = vi.fn()
    backend.queryHTTPModelCatalog = vi.fn(async query => ({ backendId: query.profileId, source: 'openai_models', destination: 'API local', profileRevision: 'api-revision', credentialToken: 'a'.repeat(64), searchTerm: '',
      models: [{ id: 'api-global-model', displayName: 'Modelo API global', backendId: 'api-1', source: 'openai_models', availability: 'available' }, { id: 'api-stage-model', displayName: 'Modelo API desta etapa', backendId: 'api-1', source: 'openai_models', availability: 'available' }],
      nextCursor: '', checkedAt, status: 'complete' as const, complete: true, accountFiltered: true }))
    const onStartRole = vi.fn(async () => undefined)
    render(<PipelinesPage backend={backend} backends={[{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }, { id: 'api-1', name: 'API local', kind: 'api', available: true }]}
      preferredBackendId="codex" preferredModelBackendId="codex" preferredModelId="global-model"
      workspaceId="workspace-1" selectedPipeline={run} onSelectPipeline={() => undefined} onProjects={() => undefined} onStartRole={onStartRole} />)

    expect(await screen.findByText(/Codex CLI pode ler arquivos fora do projeto/i)).toBeInTheDocument()
    if (stage === 'eval') backend.startPipelineQA = vi.fn(async pipelineId => ({ pipelineId, phase: 'qa' as const, round: 1, message: 'Preparando', updatedAt: date, running: true }))
    const providerPicker = await screen.findByTestId(stage === 'code' ? 'picker-pipeline-backend' : 'picker-qa-role-eval')
    await user.click(within(providerPicker).getByRole('button'))
    expect(await screen.findByRole('option', { name: 'Codex CLI · indisponível no SDD' })).toHaveAttribute('aria-disabled', 'true')
    await user.click(await screen.findByRole('option', { name: 'API local' }))
    const modelPicker = await screen.findByTestId(`picker-pipeline-role-model-${stage}`)
    expect(within(modelPicker).getByRole('combobox', { name: `Modelo da fase ${stage === 'code' ? 'Code' : 'QA'}` })).toHaveValue('Modelo API global')
    await user.click(within(modelPicker).getByRole('button'))
    await user.click(await screen.findByRole('option', { name: 'Modelo API desta etapa' }))
    await user.click(await screen.findByRole('button', { name: stage === 'code' ? 'Executar Coder' : 'Rodar QA' }))

    if (stage === 'code') expect(onStartRole).toHaveBeenCalledWith(run.id, 'api-1', 'coder', expect.objectContaining({ executor: 'api', profileId: 'api-1', modelId: 'api-stage-model', catalogRevision: 'api-revision' }), false)
    else expect(backend.startPipelineQA).toHaveBeenCalledWith(run.id, { backendId: 'api-1', selection: expect.objectContaining({ executor: 'api', profileId: 'api-1', modelId: 'api-stage-model', catalogRevision: 'api-revision' }) })
    expect(backend.queryCLIModelCatalog).not.toHaveBeenCalled()
  })
}

it('allows a Code API model override from the configured profile catalog', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const date = '2026-09-29T10:00:00Z'
  const run: Pipeline = { id: 'authoring-code-api-model', workspaceId: 'workspace-1', kind: 'ai_authoring', derivedFromPipelineId: '', discoveryFrozenVersion: 1,
    title: 'TODO', objective: 'Criar TODO com localStorage', currentStage: 'code', stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'active', eval: 'pending' }, revision: 7,
    artifacts: { discovery: { stage: 'discovery', version: 1, content: 'Criar TODO local', author: 'user', sourceSessionId: '', updatedAt: date },
      spec: { stage: 'spec', version: 1, content: 'SPEC aprovada', author: 'ai', sourceSessionId: 'spec-session', updatedAt: date },
      plan: { stage: 'plan', version: 1, content: 'Plan aprovado', author: 'ai', sourceSessionId: 'plan-session', updatedAt: date } },
    createdAt: date, updatedAt: date }
  const profile = { id: 'api-work', name: 'API trabalho', kind: 'openai_compatible', providerType: 'openai' as const, baseUrl: 'https://example.test/v1', model: 'api-global-model', hasCredential: true, endpointBlocked: false, updatedAt: date }
  backend.listPipelines = async () => [run]
  backend.getPipeline = async () => run
  backend.listProviderProfiles = async () => [profile]
  backend.saveSettings = vi.fn(async settings => settings)
  backend.queryHTTPModelCatalog = vi.fn(async query => ({ backendId: query.profileId, source: 'openai_models', destination: 'API trabalho', profileRevision: 'api-revision', credentialToken: 'a'.repeat(64), searchTerm: '',
    models: [{ id: 'api-global-model', displayName: 'Modelo API global', backendId: 'api-work', source: 'openai_models', availability: 'available' }, { id: 'api-stage-model', displayName: 'Modelo API desta etapa', backendId: 'api-work', source: 'openai_models', availability: 'available' }],
    nextCursor: '', checkedAt: new Date().toISOString(), status: 'complete' as const, complete: true, accountFiltered: true }))
  const onStartRole = vi.fn(async () => undefined)
  render(<PipelinesPage backend={backend} backends={[{ id: 'api-work', name: 'API trabalho', kind: 'api', available: true }]} preferredBackendId="api-work" preferredModelBackendId="api-work" preferredModelId="api-global-model" workspaceId="workspace-1" selectedPipeline={run} onSelectPipeline={() => undefined} onProjects={() => undefined} onStartRole={onStartRole} />)

  const picker = await screen.findByTestId('picker-pipeline-role-model-code')
  expect(within(picker).getByRole('combobox', { name: 'Modelo da fase Code' })).toHaveValue('Modelo API global')
  await user.click(within(picker).getByRole('button'))
  await user.click(await screen.findByRole('option', { name: 'Modelo API desta etapa' }))
  await user.click(await screen.findByRole('button', { name: 'Executar Coder' }))

  expect(onStartRole).toHaveBeenCalledWith(run.id, 'api-work', 'coder', expect.objectContaining({ executor: 'api', profileId: 'api-work', modelId: 'api-stage-model', catalogRevision: 'api-revision' }), false)
  expect(backend.saveSettings).not.toHaveBeenCalled()
})

it('blocks API Code on an incomplete catalog and recovers after refresh', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const date = '2026-09-29T10:00:00Z'
  const run: Pipeline = { id: 'authoring-code-catalog', workspaceId: 'workspace-1', kind: 'ai_authoring', derivedFromPipelineId: '', discoveryFrozenVersion: 1,
    title: 'TODO', objective: 'Criar TODO', currentStage: 'code', stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'active', eval: 'pending' }, revision: 7,
    artifacts: { discovery: { stage: 'discovery', version: 1, content: 'Criar TODO local', author: 'user', sourceSessionId: '', updatedAt: date },
      spec: { stage: 'spec', version: 1, content: 'SPEC aprovada', author: 'ai', sourceSessionId: 'spec-session', updatedAt: date },
      plan: { stage: 'plan', version: 1, content: 'Plan aprovado', author: 'ai', sourceSessionId: 'plan-session', updatedAt: date } },
    createdAt: date, updatedAt: date }
  let queryCount = 0
  const profile = { id: 'api-1', name: 'API local', kind: 'openai_compatible', providerType: 'openai' as const, baseUrl: 'https://example.test/v1', model: 'global-model', hasCredential: true, endpointBlocked: false, updatedAt: date }
  backend.listPipelines = async () => [run]
  backend.getPipeline = async () => run
  backend.getSettings = async () => ({ defaultBackendId: 'api-1', defaultModelBackendId: 'api-1', defaultModelId: 'global-model' })
  backend.listProviderProfiles = async () => [profile]
  backend.queryHTTPModelCatalog = vi.fn(async query => {
    queryCount++
    const complete = queryCount > 1
    return { backendId: query.profileId, source: 'openai_models', destination: 'API local', profileRevision: 'api-revision', credentialToken: 'a'.repeat(64), searchTerm: '',
      models: [{ id: 'global-model', displayName: 'Modelo global', backendId: query.profileId, source: 'openai_models', availability: 'available' }],
      nextCursor: '', checkedAt: new Date().toISOString(), status: complete ? 'complete' as const : 'partial' as const, complete, accountFiltered: true }
  })
  render(<PipelinesPage backend={backend} backends={[{ id: 'api-1', name: 'API local', kind: 'api', available: true }]} preferredBackendId="api-1" preferredModelBackendId="api-1" preferredModelId="global-model" workspaceId="workspace-1" selectedPipeline={run} onSelectPipeline={() => undefined} onProjects={() => undefined} onStartRole={async () => undefined} />)

  expect(await screen.findByText('O catálogo veio incompleto; atualize antes de iniciar a fase.')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Executar Coder' })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Atualizar catálogo' }))
  await waitFor(() => expect(screen.getByRole('button', { name: 'Executar Coder' })).toBeEnabled())
})

it('prefere um perfil API para Code/Eval quando não há provedor padrão configurado', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const date = '2026-09-29T10:00:00Z'
  const run: Pipeline = { id: 'authoring-code-default', workspaceId: 'workspace-1', kind: 'ai_authoring', derivedFromPipelineId: '', discoveryFrozenVersion: 1,
    title: 'TODO', objective: 'Criar TODO com localStorage', currentStage: 'code',
    stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'active', eval: 'pending' }, revision: 7,
    artifacts: { discovery: { stage: 'discovery', version: 1, content: 'Criar TODO local', author: 'user', sourceSessionId: '', updatedAt: date },
      spec: { stage: 'spec', version: 1, content: 'SPEC aprovada', author: 'ai', sourceSessionId: 'spec-session', updatedAt: date },
      plan: { stage: 'plan', version: 1, content: 'Plan aprovado', author: 'ai', sourceSessionId: 'plan-session', updatedAt: date } },
    createdAt: date, updatedAt: date }
  backend.listPipelines = async () => [run]
  backend.getPipeline = async () => run
  backend.inspectRepository = async () => ({ isRepository: true, root: '/workspace', branch: 'main', files: [], stagedDiff: '', unstagedDiff: '', truncated: false })
  render(<PipelinesPage backend={backend} backends={[{ id: 'aaa-api', name: 'API local', kind: 'api', available: true }, { id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }]} workspaceId="workspace-1" selectedPipeline={run} onSelectPipeline={() => undefined} onProjects={() => undefined} onStartRole={async () => undefined} />)

  await waitFor(() => expect(within(screen.getByTestId('picker-pipeline-backend')).getByRole('button')).toHaveTextContent('API local'))
  await user.click(within(screen.getByTestId('picker-pipeline-backend')).getByRole('button'))
  expect(await screen.findByRole('option', { name: 'Codex CLI · indisponível no SDD' })).toHaveAttribute('aria-disabled', 'true')
})

it('não presume um executor global após erro de Settings e mantém a escolha manual por fase', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const date = '2026-09-30T10:00:00Z'
  const run: Pipeline = { id: 'authoring-code-settings-error', workspaceId: 'workspace-1', kind: 'ai_authoring', derivedFromPipelineId: '', discoveryFrozenVersion: 1,
    title: 'TODO', objective: 'Criar TODO', currentStage: 'code',
    stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'active', eval: 'pending' }, revision: 8,
    artifacts: { discovery: { stage: 'discovery', version: 1, content: 'Criar TODO', author: 'user', sourceSessionId: '', updatedAt: date },
      spec: { stage: 'spec', version: 1, content: 'SPEC', author: 'ai', sourceSessionId: 'spec-session', updatedAt: date },
      plan: { stage: 'plan', version: 1, content: 'Plan', author: 'ai', sourceSessionId: 'plan-session', updatedAt: date } },
    createdAt: date, updatedAt: date }
  backend.listPipelines = async () => [run]
  backend.getPipeline = async () => run
  backend.listProviderProfiles = async () => [{ id: 'api', name: 'API local', kind: 'openai_compatible', providerType: 'openai', baseUrl: 'https://example.test/v1', model: '', hasCredential: true, endpointBlocked: false, updatedAt: date }]
  backend.queryCLIModelCatalog = vi.fn()
  backend.queryHTTPModelCatalog = vi.fn(async query => ({ backendId: query.profileId, source: 'openai_models', destination: 'API local', profileRevision: 'api-revision', credentialToken: 'a'.repeat(64), searchTerm: '',
    models: [{ id: 'manual-model', displayName: 'Modelo escolhido manualmente', backendId: 'api', source: 'openai_models', availability: 'available' }], nextCursor: '', checkedAt: new Date().toISOString(), status: 'complete' as const, complete: true, accountFiltered: true }))
  const onStartRole = vi.fn(async () => undefined)
  render(<PipelinesPage backend={backend} backends={[{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }, { id: 'api', name: 'API', kind: 'api', available: true }]}
    preferredBackendId="codex" preferredModelBackendId="codex" preferredModelId="stale-model"
    settingsStatus="error" workspaceId="workspace-1" selectedPipeline={run} onSelectPipeline={() => undefined} onProjects={() => undefined} onStartRole={onStartRole} />)

  // The controls wait for the project's phase choices to be read, so other alerts of the page may be there first.
  expect(await screen.findByText(/Não foi possível ler o padrão das Configurações/i)).toHaveAttribute('role', 'alert')
  expect(within(screen.getByTestId('picker-pipeline-backend')).getByRole('button')).toHaveTextContent('Escolha um executor')
  expect(backend.queryCLIModelCatalog).not.toHaveBeenCalled()
  await user.click(within(screen.getByTestId('picker-pipeline-backend')).getByRole('button'))
  expect(await screen.findByRole('option', { name: 'Codex CLI · indisponível no SDD' })).toHaveAttribute('aria-disabled', 'true')
  await user.click(await screen.findByRole('option', { name: 'API' }))
  const modelPicker = await screen.findByTestId('picker-pipeline-role-model-code')
  expect(within(modelPicker).getByRole('combobox', { name: 'Modelo da fase Code' })).toHaveValue('')
  expect(screen.queryByText(/Padrão das Configurações/)).not.toBeInTheDocument()
  await user.click(within(modelPicker).getByRole('button', { name: /Abrir opções de Modelo da fase Code/ }))
  await user.click(await screen.findByRole('option', { name: 'Modelo escolhido manualmente' }))
  await waitFor(() => expect(screen.getByRole('button', { name: 'Executar Coder' })).toBeEnabled())
  await user.click(screen.getByRole('button', { name: 'Executar Coder' }))
  expect(onStartRole).toHaveBeenCalledWith(run.id, 'api', 'coder', expect.objectContaining({ executor: 'api', profileId: 'api', modelId: 'manual-model' }), false)
})

// What the project chose for a phase in "Provedor e modelo por fase" is where Code and QA start from, ahead of the default of Settings.
function codeStageFixture(overrides: { stage?: 'code' | 'eval' | 'prs' } = {}) {
  const { backend } = createFakeBackend()
  const date = '2026-10-02T10:00:00Z'
  const stage = overrides.stage ?? 'code'
  const run: Pipeline = { id: 'authoring-phase-choice', workspaceId: 'workspace-1', kind: 'ai_authoring', derivedFromPipelineId: '', discoveryFrozenVersion: 1,
    title: 'TODO', objective: 'Criar TODO', currentStage: stage,
    stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: stage === 'code' ? 'active' : 'completed', eval: stage === 'eval' ? 'active' : stage === 'prs' ? 'completed' : 'pending', ...(stage === 'prs' ? { prs: 'active' as const } : {}) }, revision: 8,
    artifacts: { discovery: { stage: 'discovery', version: 1, content: 'Criar TODO', author: 'user', sourceSessionId: '', updatedAt: date },
      spec: { stage: 'spec', version: 1, content: 'SPEC', author: 'ai', sourceSessionId: 'spec-session', updatedAt: date },
      plan: { stage: 'plan', version: 1, content: 'Plan', author: 'ai', sourceSessionId: 'plan-session', updatedAt: date } },
    createdAt: date, updatedAt: date }
  const profile = (id: string, name: string, model: string) => ({ id, name, kind: 'openai_compatible', providerType: 'openai' as const, baseUrl: 'https://example.test/v1', model, hasCredential: true, endpointBlocked: false, updatedAt: date })
  backend.listPipelines = async () => [run]
  backend.getPipeline = async () => run
  backend.listProviderProfiles = async () => [profile('api', 'API', 'profile-model'), profile('other', 'Outra API', 'other-model')]
  backend.queryHTTPModelCatalog = vi.fn(async query => ({ backendId: query.profileId, source: 'openai_models', destination: query.profileId, profileRevision: 'api-revision', credentialToken: 'a'.repeat(64), searchTerm: '',
    models: ['api-model', 'profile-model', 'other-model', 'large-model'].map(id => ({ id, displayName: id, backendId: query.profileId, source: 'openai_models', availability: 'available' })), nextCursor: '', checkedAt: new Date().toISOString(), status: 'complete' as const, complete: true, accountFiltered: true }))
  const onStartRole = vi.fn(async () => undefined)
  const choice = (executorStage: 'code' | 'eval' | 'prs', backendId: string, modelId: string) => ({ workspaceId: 'workspace-1', stage: executorStage, backendId, modelId, updatedAt: date })
  const page = (extra: Partial<ComponentProps<typeof PipelinesPage>> = {}) => <PipelinesPage {...extra} backend={backend} backends={[{ id: 'api', name: 'API', kind: 'api', available: true }, { id: 'other', name: 'Outra API', kind: 'api', available: true }]}
    preferredBackendId="api" preferredModelBackendId="api" preferredModelId="api-model" settingsStatus="ready" workspaceId="workspace-1" selectedPipeline={run} onSelectPipeline={() => undefined} onProjects={() => undefined} onStartRole={onStartRole} />
  return { backend, run, onStartRole, choice, page }
}

it('starts Code from the executor and model the project chose for it, not from the default of Settings', async () => {
  const user = userEvent.setup()
  const { backend, run, onStartRole, choice, page } = codeStageFixture()
  backend.listStageExecutors = async () => [choice('code', 'other', 'large-model'), choice('eval', 'api', 'api-model')]
  render(page())
  await waitFor(() => expect(within(screen.getByTestId('picker-pipeline-backend')).getByRole('button')).toHaveTextContent('Outra API'))
  const modelPicker = await screen.findByTestId('picker-pipeline-role-model-code')
  await waitFor(() => expect(within(modelPicker).getByRole('combobox', { name: 'Modelo da fase Code' })).toHaveValue('large-model'))
  expect(screen.getByText('Escolhido para Code em Provedor e modelo por fase · Outra API')).toBeInTheDocument()
  await waitFor(() => expect(screen.getByRole('button', { name: 'Executar Coder' })).toBeEnabled())
  await user.click(screen.getByRole('button', { name: 'Executar Coder' }))
  expect(onStartRole).toHaveBeenCalledWith(run.id, 'other', 'coder', expect.objectContaining({ executor: 'api', profileId: 'other', modelId: 'large-model' }), false)
})

it('runs QA from the choice for QA and fixes with the choice for Code', async () => {
  const user = userEvent.setup()
  const { backend, run, choice, page } = codeStageFixture({ stage: 'eval' })
  backend.listStageExecutors = async () => [choice('code', 'other', 'large-model'), choice('eval', 'api', '')]
  backend.startPipelineQA = vi.fn(async pipelineId => ({ pipelineId, phase: 'qa' as const, round: 1, message: 'Preparando', updatedAt: run.updatedAt, running: true }))
  render(page())
  await waitFor(() => expect(within(screen.getByTestId('picker-qa-role-eval')).getByRole('button')).toHaveTextContent('API'))
  await waitFor(() => expect(within(screen.getByTestId('picker-qa-role-code')).getByRole('button')).toHaveTextContent('Outra API'))
  // A choice with no model means the profile's own, not the model of Settings' default.
  const modelPicker = await screen.findByTestId('picker-pipeline-role-model-eval')
  await waitFor(() => expect(within(modelPicker).getByRole('combobox', { name: 'Modelo da fase QA' })).toHaveValue('profile-model'))
  await waitFor(() => expect(within(screen.getByTestId('picker-pipeline-role-model-code')).getByRole('combobox')).toHaveValue('large-model'))
  await waitFor(() => expect(screen.getByRole('button', { name: 'Rodar QA' })).toBeEnabled())
  await user.click(screen.getByRole('button', { name: 'Rodar QA' }))
  expect(backend.startPipelineQA).toHaveBeenCalledWith(run.id, { backendId: 'api', selection: expect.objectContaining({ profileId: 'api', modelId: 'profile-model' }) })
})

it('shows each session the QA loop starts in the activity panel, without leaving the screen', async () => {
  const { backend, run, page } = codeStageFixture({ stage: 'eval' })
  const onFollowSession = vi.fn()
  backend.getPipelineQALoop = vi.fn(async pipelineId => ({ pipelineId, phase: 'fixing' as const, round: 1, sessionId: 'coder-2', message: 'O Coder está aplicando as correções na mesma cópia', updatedAt: run.updatedAt, running: true }))
  render(page({ onFollowSession }))
  await waitFor(() => expect(onFollowSession).toHaveBeenCalledWith('coder-2'))
})

it('shows the Coder at work, with its plan, instead of the start screen, when Code is opened while it runs', async () => {
  const { backend, page } = codeStageFixture()
  let sequence = 0
  const event = (type: string, data: unknown) => ({ id: `e${++sequence}`, streamId: 'coder-1', sequence, type, data, createdAt: '2026-10-09T10:00:00Z' })
  const journal = [event('message.user', { content: 'Implemente' }),
    event('message.assistant', { content: '', toolCalls: [{ id: 'plan', name: 'update_plan', arguments: { plan: [{ step: 'Ler o projeto', status: 'completed' }, { step: 'Escrever o formulário', status: 'in_progress' }] } }] })]
  backend.getPipelineStageActivity = async (pipelineId, stage) => ({ pipelineId, workspaceId: 'workspace-1', stage, status: 'running', phase: '', sessionId: 'coder-1', modelId: 'api-model', updatedAt: '2026-10-09T10:00:00Z' })
  backend.listEvents = async () => journal
  render(page())
  expect(await screen.findByRole('list', { name: 'Plano do Coder' })).toBeInTheDocument()
  expect(screen.getByText('O Coder está trabalhando')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Executar Coder' })).not.toBeInTheDocument()
})

it('verifies the code by itself when the Coder finishes while the bench is open, so nobody has to find Check code', async () => {
  const { backend, run, page } = codeStageFixture()
  let reads = 0
  const activity = (status: string) => ({ pipelineId: run.id, workspaceId: 'workspace-1', stage: 'code' as const, status, phase: '', sessionId: 'coder-1', modelId: 'api-model', updatedAt: '2026-10-09T10:00:00Z' })
  backend.getPipelineStageActivity = async () => activity(++reads <= 2 ? 'running' : 'completed')
  backend.listEvents = async () => []
  const verify = vi.fn(async () => ({ ...run, stageStatus: { ...run.stageStatus, code: 'waiting_user' as const } }))
  backend.completePipelineCode = verify
  render(page())
  expect(await screen.findByText('O Coder está trabalhando')).toBeInTheDocument()
  await waitFor(() => expect(verify).toHaveBeenCalledTimes(1), { timeout: 8000 })
  expect(verify).toHaveBeenCalledWith(run.id)
})

it('falls back to the default of Settings when the executor chosen for the phase cannot run it, and the person can still pick', async () => {
  const { backend, choice, page } = codeStageFixture()
  backend.listStageExecutors = async () => [choice('code', 'gone', 'large-model')]
  render(page())
  await waitFor(() => expect(backend.queryHTTPModelCatalog).toHaveBeenCalledWith(expect.objectContaining({ profileId: 'api' }), expect.anything()))
  expect(within(screen.getByTestId('picker-pipeline-backend')).getByRole('button')).toHaveTextContent('API')
  await waitFor(() => expect(within(screen.getByTestId('picker-pipeline-role-model-code')).getByRole('combobox')).toHaveValue('api-model'))
})

it('starts the pull request chat from the executor and model the project chose for PRs', async () => {
  const user = userEvent.setup()
  const { backend, run, onStartRole, choice, page } = codeStageFixture({ stage: 'prs' })
  backend.listStageExecutors = async () => [choice('prs', 'other', 'large-model')]
  render(page())
  const modelPicker = await screen.findByTestId('picker-pipeline-role-model-prs')
  await waitFor(() => expect(within(modelPicker).getByRole('combobox', { name: 'Modelo da fase PRs' })).toHaveValue('large-model'))
  expect(within(screen.getByTestId('picker-pipeline-prs-backend')).getByRole('button')).toHaveTextContent('Outra API')
  const start = screen.getByRole('button', { name: 'Abrir PRs com a IA' })
  await waitFor(() => expect(start).toBeEnabled())
  await user.click(start)
  expect(onStartRole).toHaveBeenCalledWith(run.id, 'other', 'publisher', expect.objectContaining({ executor: 'api', profileId: 'other', modelId: 'large-model' }), false)
})

it('does not open the PRs panel on the default while the choices of the project are still being read', async () => {
  const { backend, choice, page } = codeStageFixture({ stage: 'prs' })
  let answer!: (items: Awaited<ReturnType<typeof backend.listStageExecutors>>) => void
  backend.listStageExecutors = () => new Promise(resolve => { answer = resolve })
  render(page())
  expect(await screen.findByText('Lendo as escolhas por fase…', { selector: 'p[role="status"]' })).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Abrir PRs com a IA' })).not.toBeInTheDocument()
  await act(async () => { answer([choice('prs', 'other', '')]) })
  await waitFor(() => expect(within(screen.getByTestId('picker-pipeline-prs-backend')).getByRole('button')).toHaveTextContent('Outra API'))
})

it('says why Code starts from the default when the executor chosen for it cannot run it, whichever way it cannot', async () => {
  const { backend, choice, run } = codeStageFixture()
  // Codex that the Harflex cannot hold to its contract, and a saved API profile that lost its credential, are both unusable.
  backend.listStageExecutors = async () => [choice('code', 'codex', 'gpt-5')]
  const view = render(<PipelinesPage backend={backend} backends={[{ id: 'api', name: 'API', kind: 'api', available: true }, { id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }]}
    preferredBackendId="api" preferredModelBackendId="api" preferredModelId="api-model" settingsStatus="ready" workspaceId="workspace-1" selectedPipeline={run} onSelectPipeline={() => undefined} onProjects={() => undefined} onStartRole={async () => undefined} />)
  expect(await screen.findByText(/O executor escolhido para Code \(Codex CLI\) não está disponível agora; esta fase parte do padrão/)).toBeInTheDocument()
  await waitFor(() => expect(within(screen.getByTestId('picker-pipeline-backend')).getByRole('button')).toHaveTextContent('API'))
  view.unmount()
  const second = codeStageFixture()
  second.backend.listStageExecutors = async () => [second.choice('code', 'other', 'large-model')]
  second.backend.listProviderProfiles = async () => [{ id: 'api', name: 'API', kind: 'openai_compatible', providerType: 'openai', baseUrl: 'https://example.test/v1', model: 'profile-model', hasCredential: true, endpointBlocked: false, updatedAt: '2026-10-02T10:00:00Z' },
    { id: 'other', name: 'Outra API', kind: 'openai_compatible', providerType: 'openai', baseUrl: 'https://example.test/v1', model: 'other-model', hasCredential: false, endpointBlocked: false, updatedAt: '2026-10-02T10:00:00Z' }]
  render(second.page())
  expect(await screen.findByText(/O executor escolhido para Code \(Outra API\) não está disponível agora/)).toBeInTheDocument()
  await waitFor(() => expect(within(screen.getByTestId('picker-pipeline-backend')).getByRole('button')).toHaveTextContent('API'))
  // The unusable profile was never asked for models: nothing failed with a message that says nothing.
  expect(second.backend.queryHTTPModelCatalog).not.toHaveBeenCalledWith(expect.objectContaining({ profileId: 'other' }), expect.anything())
})

it('does not start Code on the default while the choices of the project are still being read', async () => {
  const { backend, choice, page } = codeStageFixture()
  let answer!: (items: Awaited<ReturnType<typeof backend.listStageExecutors>>) => void
  backend.listStageExecutors = () => new Promise(resolve => { answer = resolve })
  render(page())
  expect(await screen.findByText('Lendo as escolhas por fase…', { selector: 'p[role="status"]' })).toBeInTheDocument()
  expect(screen.queryByTestId('picker-pipeline-backend')).not.toBeInTheDocument()
  expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalled()
  await act(async () => { answer([choice('code', 'other', 'large-model')]) })
  await waitFor(() => expect(within(screen.getByTestId('picker-pipeline-backend')).getByRole('button')).toHaveTextContent('Outra API'))
  // The catalog was asked about the provider the project chose, and never about the default it would have jumped from.
  await waitFor(() => expect(backend.queryHTTPModelCatalog).toHaveBeenCalledWith(expect.objectContaining({ profileId: 'other' }), expect.anything()))
  expect(backend.queryHTTPModelCatalog).not.toHaveBeenCalledWith(expect.objectContaining({ profileId: 'api' }), expect.anything())
})

it('treats profiles that cannot be read like choices that cannot: it says so, starts Code from the default, and reads both again on retry', async () => {
  const user = userEvent.setup()
  const { backend, choice, page } = codeStageFixture()
  backend.listStageExecutors = async () => [choice('code', 'other', 'large-model')]
  const readProfiles = backend.listProviderProfiles.bind(backend)
  let attempts = 0
  backend.listProviderProfiles = async () => {
    if (++attempts === 1) throw new Error('offline')
    return readProfiles()
  }
  render(page())
  const failure = await screen.findByText(/Não foi possível ler as escolhas por fase/)
  expect(failure.closest('details')).toBeNull()
  // Nothing is known about whether the saved executor works, so nothing is claimed about it and Code starts from the default.
  await waitFor(() => expect(within(screen.getByTestId('picker-pipeline-backend')).getByRole('button')).toHaveTextContent('API'))
  expect(screen.queryByText(/não está disponível agora; esta fase parte do padrão/)).not.toBeInTheDocument()
  await user.click(within(failure.parentElement!).getByRole('button', { name: 'Tentar de novo' }))
  await waitFor(() => expect(within(screen.getByTestId('picker-pipeline-backend')).getByRole('button')).toHaveTextContent('Outra API'))
  expect(screen.queryByText(/Não foi possível ler as escolhas por fase/)).not.toBeInTheDocument()
})

it('says that the choices could not be read even while the card is closed, and keeps what the person opened when it tries again', async () => {
  const user = userEvent.setup()
  const { backend, choice, page } = codeStageFixture()
  let attempts = 0
  backend.listStageExecutors = async () => {
    if (++attempts === 1) throw new Error('offline')
    return [choice('code', 'other', 'large-model')]
  }
  render(page())
  const failure = await screen.findByText(/Não foi possível ler as escolhas por fase\. Enquanto isso, Code, QA e PRs partem do padrão/)
  expect(failure.closest('details')).toBeNull()
  expect(screen.getByText('Escolhas indisponíveis')).toBeInTheDocument()
  // Code starts from the default, which the person is told.
  await waitFor(() => expect(within(screen.getByTestId('picker-pipeline-backend')).getByRole('button')).toHaveTextContent('API'))
  const card = screen.getByText('Provedor e modelo por fase').closest('details')!
  await user.click(within(card).getByText('Provedor e modelo por fase'))
  await waitFor(() => expect(card).toHaveAttribute('open'))
  await user.click(within(failure.parentElement!).getByRole('button', { name: 'Tentar de novo' }))
  await waitFor(() => expect(screen.getByText('1 de 6 com escolha própria')).toBeInTheDocument())
  expect(card).toHaveAttribute('open')
  expect(screen.queryByText(/Não foi possível ler as escolhas por fase/)).not.toBeInTheDocument()
  await waitFor(() => expect(within(screen.getByTestId('picker-pipeline-backend')).getByRole('button')).toHaveTextContent('Outra API'))
})

it('opens the card of phases while the project has no pipeline and keeps it closed once it has one', async () => {
  const user = userEvent.setup()
  const { backend, run, page } = codeStageFixture()
  backend.listPipelines = async () => []
  const first = render(<PipelinesPage backend={backend} backends={[]} workspaceId="workspace-1" onSelectPipeline={() => undefined} onProjects={() => undefined} onStartRole={async () => undefined} />)
  const empty = (await screen.findByText('Provedor e modelo por fase')).closest('details')!
  await waitFor(() => expect(empty).toHaveAttribute('open'))
  first.unmount()
  backend.listPipelines = async () => [run]
  render(page())
  const card = (await screen.findByText('Provedor e modelo por fase')).closest('details')!
  expect(card).not.toHaveAttribute('open')
  await user.click(within(card).getByText('Provedor e modelo por fase'))
  await waitFor(() => expect(card).toHaveAttribute('open'))
})

it('closes the card of phases by itself once the project has a pipeline, and leaves it as the person set it', async () => {
  const user = userEvent.setup()
  const { backend, run, page } = codeStageFixture()
  let pipelines: Pipeline[] = []
  backend.listPipelines = async () => pipelines
  render(page())
  const card = (await screen.findByText('Provedor e modelo por fase')).closest('details')!
  await waitFor(() => expect(card).toHaveAttribute('open'))
  // Nobody touched it: the first pipeline closes it, so the work is what is on the screen.
  pipelines = [run]
  await user.click(screen.getByRole('button', { name: 'Atualizar' }))
  await waitFor(() => expect(card).not.toHaveAttribute('open'))
  // Opened on purpose, it stays open through the next refresh.
  await user.click(within(card).getByText('Provedor e modelo por fase'))
  await waitFor(() => expect(card).toHaveAttribute('open'))
  await user.click(screen.getByRole('button', { name: 'Atualizar' }))
  await screen.findByText('Trabalhos deste projeto')
  expect(card).toHaveAttribute('open')
})

it('does not let the phases be changed on a guess when the saved choices cannot be read', async () => {
  const { backend, page } = codeStageFixture()
  backend.listStageExecutors = async () => { throw new Error('offline') }
  render(page())
  expect(await screen.findByText(/Não foi possível ler as escolhas por fase/)).toBeInTheDocument()
  // Code still starts from the default: the failure is said, not hidden behind a made-up state.
  await waitFor(() => expect(within(screen.getByTestId('picker-pipeline-backend')).getByRole('button')).toHaveTextContent('API'))
})

it('bloqueia Code quando o projeto Git está sujo', async () => {
  const { backend } = createFakeBackend()
  const date = '2026-09-29T10:00:00Z'
  const run: Pipeline = { id: 'pipeline-code', workspaceId: 'workspace-1', title: 'Implementar', objective: 'Criar TODO', currentStage: 'code', stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'active', eval: 'pending' }, revision: 4, artifacts: {}, createdAt: date, updatedAt: date }
  backend.listPipelines = async () => [run]
  backend.getPipeline = async () => run
  backend.previewPipelineCodeWorkspace = async () => { throw Object.assign(new Error('dirty'), { cause: { code: 'pipeline_git_dirty' } }) }
  render(<PipelinesPage backend={backend} backends={[{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }, { id: 'api', name: 'API', kind: 'api', available: true }]} workspaceId="workspace-1" selectedPipeline={run} onSelectPipeline={() => undefined} onProjects={() => undefined} onStartRole={async () => undefined} />)
  expect(await screen.findByText(/A pasta do projeto já tem alterações Git/)).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Executar Coder' })).toBeDisabled()
})

it('refreshes the selected pipeline detail instead of retaining a stale revision', async () => {
  const { backend } = createFakeBackend()
  const date = '2026-09-25T10:00:00Z'
  const stale: Pipeline = { id: 'pipeline-1', workspaceId: 'workspace-1', title: 'Exportar', objective: 'CSV', currentStage: 'spec', stageStatus: { discovery: 'completed', spec: 'active', plan: 'pending', code: 'pending', eval: 'pending' }, revision: 2, artifacts: {}, createdAt: date, updatedAt: date }
  const fresh: Pipeline = { ...stale, currentStage: 'plan', stageStatus: { ...stale.stageStatus, spec: 'completed', plan: 'active' }, revision: 3 }
  backend.listPipelines = async () => [fresh]
  const getPipeline = vi.fn(async () => fresh)
  backend.getPipeline = getPipeline
  const onSelectPipeline = vi.fn()
  render(<PipelinesPage backend={backend} backends={[]} workspaceId="workspace-1" selectedPipeline={stale} onSelectPipeline={onSelectPipeline} onProjects={() => undefined} onStartRole={async () => undefined} />)
  await waitFor(() => expect(getPipeline).toHaveBeenCalledWith(stale.id))
  expect(onSelectPipeline).toHaveBeenCalledWith(fresh)
})

it('opens the SDD creation form when Novo trabalho is requested', async () => {
  const { backend } = createFakeBackend()
  const date = '2026-09-25T10:00:00Z'
  const active: Pipeline = { id: 'pipeline-1', workspaceId: 'workspace-1', title: 'Existente', objective: 'Continuar', currentStage: 'code', stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'active', eval: 'pending' }, revision: 4, artifacts: {}, createdAt: date, updatedAt: date }
  backend.listPipelines = async () => [active]
  backend.getPipeline = async () => active
  render(<PipelinesPage backend={backend} backends={[]} workspaceId="workspace-1" selectedPipeline={active} newWorkRequest={1} onSelectPipeline={() => undefined} onProjects={() => undefined} onStartRole={async () => undefined} />)
  expect(await screen.findByLabelText('Discovery')).toBeInTheDocument()
  expect(screen.queryByLabelText('Título do trabalho')).not.toBeInTheDocument()
})

it('creates authoring work from Discovery only and preserves the request on uncertain retry', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const create = vi.fn().mockRejectedValueOnce(new Error('network uncertain')).mockResolvedValueOnce({
    id: 'authoring-1', workspaceId: 'workspace-1', kind: 'ai_authoring', derivedFromPipelineId: '', discoveryFrozenVersion: 0,
    title: 'Discovery', objective: '', currentStage: 'discovery', stageStatus: { discovery: 'active', spec: 'pending', plan: 'pending', code: 'pending', eval: 'pending' }, revision: 1,
    artifacts: { discovery: { stage: 'discovery', version: 1, content: 'Meu problema', author: 'user', sourceSessionId: '', updatedAt: '2026-09-25T10:00:00Z' } },
    createdAt: '2026-09-25T10:00:00Z', updatedAt: '2026-09-25T10:00:00Z',
  })
  backend.createAuthoringPipeline = create
  backend.listPipelines = async () => []
  const onSelectPipeline = vi.fn()
  render(<PipelinesPage backend={backend} backends={[]} workspaceId="workspace-1" onSelectPipeline={onSelectPipeline} onProjects={() => undefined} onStartRole={async () => undefined} />)
  await user.type(await screen.findByLabelText('Discovery'), 'Meu problema')
  await user.click(screen.getByRole('button', { name: 'Criar pipeline' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('A operação falhou')
  await user.click(screen.getByRole('button', { name: 'Criar pipeline' }))
  expect(create).toHaveBeenCalledTimes(2)
  expect(create.mock.calls[0][0]).toEqual(create.mock.calls[1][0])
  expect(create.mock.calls[0][0]).toMatchObject({ workspaceId: 'workspace-1', discovery: 'Meu problema' })
  expect(onSelectPipeline).toHaveBeenCalledWith(expect.objectContaining({ kind: 'ai_authoring' }))
})

it('reloads a clean phase draft when a newer artifact revision is read back', async () => {
  const { backend } = createFakeBackend()
  const date = '2026-09-25T10:00:00Z'
  const stale: Pipeline = { id: 'pipeline-1', workspaceId: 'workspace-1', title: 'Existente', objective: 'Atualizar SPEC', currentStage: 'spec', stageStatus: { discovery: 'completed', spec: 'active', plan: 'pending', code: 'pending', eval: 'pending' }, revision: 2, artifacts: { spec: { stage: 'spec', version: 1, content: 'Versão antiga', updatedAt: date } }, createdAt: date, updatedAt: date }
  const fresh: Pipeline = { ...stale, revision: 3, artifacts: { spec: { ...stale.artifacts.spec, version: 2, content: 'Versão nova' } } }
  backend.listPipelines = async () => [fresh]
  backend.getPipeline = async () => fresh
  function Harness() {
    const [selected, setSelected] = useState(stale)
    return <PipelinesPage backend={backend} backends={[]} workspaceId="workspace-1" selectedPipeline={selected} onSelectPipeline={item => { if (item) setSelected(item) }} onProjects={() => undefined} onStartRole={async () => undefined} />
  }
  render(<Harness />)
  await waitFor(() => expect(screen.getByLabelText('Artefato da fase')).toHaveValue('Versão nova'))
})

it('keeps an unsaved draft and blocks overwrite when the phase changes externally', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const date = '2026-09-25T10:00:00Z'
  const old: Pipeline = { id: 'pipeline-1', workspaceId: 'workspace-1', title: 'Existente', objective: 'Atualizar SPEC', currentStage: 'spec', stageStatus: { discovery: 'completed', spec: 'active', plan: 'pending', code: 'pending', eval: 'pending' }, revision: 2, artifacts: { spec: { stage: 'spec', version: 1, content: 'Versão antiga', updatedAt: date } }, createdAt: date, updatedAt: date }
  const fresh: Pipeline = { ...old, revision: 3, artifacts: { spec: { ...old.artifacts.spec, version: 2, content: 'Versão externa' } } }
  let external = false
  backend.listPipelines = async () => [external ? fresh : old]
  backend.getPipeline = async () => external ? fresh : old
  function Harness() {
    const [selected, setSelected] = useState(old)
    return <PipelinesPage backend={backend} backends={[]} workspaceId="workspace-1" selectedPipeline={selected} onSelectPipeline={item => { if (item) setSelected(item) }} onProjects={() => undefined} onStartRole={async () => undefined} />
  }
  render(<Harness />)
  await screen.findByLabelText('Artefato da fase')
  await user.type(screen.getByLabelText('Artefato da fase'), ' rascunho')
  external = true
  await user.click(screen.getByRole('button', { name: 'Atualizar' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('A fase mudou enquanto você editava')
  expect(screen.getByLabelText('Artefato da fase')).toHaveValue('Versão antiga rascunho')
  expect(screen.getByRole('button', { name: 'Salvar artefato' })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Descartar rascunho e carregar atualização' }))
  expect(screen.getByLabelText('Artefato da fase')).toHaveValue('Versão externa')
})

it('retains a dirty authoring Discovery draft when an external version arrives', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const date = '2026-09-28T10:00:00Z'
  const old: Pipeline = { id: 'authoring-1', workspaceId: 'workspace-1', kind: 'ai_authoring', derivedFromPipelineId: '', discoveryFrozenVersion: 0, title: 'Novo trabalho', objective: '', currentStage: 'discovery', stageStatus: { discovery: 'active', spec: 'pending', plan: 'pending', code: 'pending', eval: 'pending' }, revision: 1, artifacts: { discovery: { stage: 'discovery', version: 1, content: 'Versão inicial', author: 'user', sourceSessionId: '', updatedAt: date } }, createdAt: date, updatedAt: date }
  const fresh: Pipeline = { ...old, revision: 2, artifacts: { discovery: { ...old.artifacts.discovery!, version: 2, content: 'Versão externa' } } }
  let external = false
  backend.listPipelines = async () => [external ? fresh : old]
  backend.getPipeline = async () => external ? fresh : old
  const revise = vi.fn(async () => fresh)
  backend.reviseAuthoringDiscovery = revise
  function Harness() {
    const [selected, setSelected] = useState(old)
    return <PipelinesPage backend={backend} backends={[]} workspaceId="workspace-1" selectedPipeline={selected} onSelectPipeline={item => { if (item) setSelected(item) }} onProjects={() => undefined} onStartRole={async () => undefined} />
  }
  render(<Harness />)
  await user.click(await screen.findByRole('button', { name: 'Editar Discovery' }))
  await user.type(screen.getByLabelText('Texto da Discovery'), ' rascunho')
  external = true
  await user.click(screen.getByRole('button', { name: 'Atualizar' }))
  expect(await screen.findByText('A Discovery mudou em outra atualização. Copie seu texto antes de recarregar.')).toBeInTheDocument()
  expect(screen.getByLabelText('Texto da Discovery')).toHaveValue('Versão inicial rascunho')
  expect(screen.getByRole('button', { name: 'Salvar nova versão' })).toBeDisabled()
  expect(revise).not.toHaveBeenCalled()
})

it('does not select a created pipeline from workspace A after switching to B', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const date = '2026-09-28T10:00:00Z'
  const created: Pipeline = { id: 'created-a', workspaceId: 'workspace-a', kind: 'ai_authoring', derivedFromPipelineId: '', discoveryFrozenVersion: 0, title: 'A', objective: '', currentStage: 'discovery', stageStatus: { discovery: 'active', spec: 'pending', plan: 'pending', code: 'pending', eval: 'pending' }, revision: 1, artifacts: { discovery: { stage: 'discovery', version: 1, content: 'De A', author: 'user', sourceSessionId: '', updatedAt: date } }, createdAt: date, updatedAt: date }
  let finish!: (value: Pipeline) => void
  backend.listPipelines = async () => []
  backend.createAuthoringPipeline = () => new Promise(resolve => { finish = resolve })
  const onSelectPipeline = vi.fn()
  const props = { backend, backends: [], onSelectPipeline, onProjects: () => undefined, onStartRole: async () => undefined }
  const view = render(<PipelinesPage {...props} workspaceId="workspace-a" />)
  await user.type(await screen.findByLabelText('Discovery'), 'De A')
  await user.click(screen.getByRole('button', { name: 'Criar pipeline' }))
  view.rerender(<PipelinesPage {...props} workspaceId="workspace-b" />)
  await act(async () => { finish(created) })
  expect(screen.getByLabelText('Discovery')).toBeInTheDocument()
  expect(onSelectPipeline).not.toHaveBeenCalledWith(created)
  expect(screen.queryByText('created-a')).not.toBeInTheDocument()
})

it('does not call selection after an in-flight create unmounts', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  backend.listPipelines = async () => []
  let finish!: (value: Pipeline) => void
  backend.createAuthoringPipeline = () => new Promise(resolve => { finish = resolve })
  const onSelectPipeline = vi.fn()
  const view = render(<PipelinesPage backend={backend} backends={[]} workspaceId="workspace-a" onSelectPipeline={onSelectPipeline} onProjects={() => undefined} onStartRole={async () => undefined} />)
  await user.type(await screen.findByLabelText('Discovery'), 'De A')
  await user.click(screen.getByRole('button', { name: 'Criar pipeline' }))
  view.unmount()
  const created = { id: 'created-a', workspaceId: 'workspace-a' } as Pipeline
  await act(async () => { finish(created) })
  expect(onSelectPipeline).not.toHaveBeenCalledWith(created)
})

it('opens the Code, QA or PRs screen chosen in the stage bar, without tabs of its own', async () => {
  const { backend, run } = codeStageFixture({ stage: 'prs' })
  const finished: Pipeline = { ...run, artifacts: { ...run.artifacts, code: { stage: 'code', version: 1, content: 'diff --git a/index.html b/index.html\nnew file mode 100644\n--- /dev/null\n+++ b/index.html\n@@ -0,0 +1 @@\n+<h1>TODO</h1>', author: 'ai', sourceSessionId: 'coder', contentDigest: 'c'.repeat(64), updatedAt: run.updatedAt } } }
  backend.getPipeline = async () => finished
  const page = (viewStage?: PipelineStage) => <PipelinesPage backend={backend} backends={[{ id: 'api', name: 'API', kind: 'api', available: true }]} workspaceId="workspace-1" selectedPipeline={finished} viewStage={viewStage} viewRequestId={viewStage ? 1 : undefined} onSelectPipeline={() => undefined} onProjects={() => undefined} onStartRole={async () => undefined} />
  const view = render(page())
  expect(await screen.findByRole('region', { name: 'Bancada PRs' })).toBeInTheDocument()
  expect(screen.queryByRole('navigation', { name: 'Bancadas' })).not.toBeInTheDocument()
  view.rerender(page('code'))
  expect(await screen.findByRole('region', { name: 'Bancada Code' })).toBeInTheDocument()
  expect(screen.getByRole('button', { name: /index\.html/ })).toBeInTheDocument()
  expect(screen.getByText('<h1>TODO</h1>')).toBeInTheDocument()
})

it('tells the stage bar which screen is showing, also when the work moves on by itself', async () => {
  const { backend, run } = codeStageFixture({ stage: 'code' })
  const shown = vi.fn()
  const page = (pipeline: Pipeline, viewStage?: PipelineStage) => <PipelinesPage backend={backend} backends={[{ id: 'api', name: 'API', kind: 'api', available: true }]} workspaceId="workspace-1" selectedPipeline={pipeline} viewStage={viewStage} viewRequestId={viewStage ? 1 : undefined} onSelectPipeline={() => undefined} onProjects={() => undefined} onStartRole={async () => undefined} onShownStage={shown} />
  const view = render(page(run, 'code'))
  await waitFor(() => expect(shown).toHaveBeenLastCalledWith('code'))
  // Code approved: the pipeline is now at QA, and the screen and the bar both follow it.
  const atQA: Pipeline = { ...run, currentStage: 'eval', revision: run.revision + 1, stageStatus: { ...run.stageStatus, code: 'completed', eval: 'active' } }
  backend.getPipeline = async () => atQA
  view.rerender(page(atQA, 'code'))
  expect(await screen.findByRole('region', { name: 'Bancada QA' })).toBeInTheDocument()
  await waitFor(() => expect(shown).toHaveBeenLastCalledWith('eval'))
  view.rerender(page(atQA, 'spec'))
  await waitFor(() => expect(shown).toHaveBeenLastCalledWith('spec'))
})
