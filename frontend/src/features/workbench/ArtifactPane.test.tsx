import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it } from 'vitest'
import { createFakeBackend } from '../../test/fakeBackend'
import type { Pipeline } from '../../lib/backend'
import { ArtifactPane } from './ArtifactPane'

afterEach(cleanup)

it('shows durable SPEC, plan, code and evaluator evidence in the workbench', async () => {
  const { backend } = createFakeBackend()
  const date = '2026-09-25T10:00:00Z'
  const pipeline: Pipeline = { id: 'pipeline-1', workspaceId: 'workspace-1', title: 'Exportar CSV', objective: 'Exportar faturas', currentStage: 'eval',
    stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'completed', eval: 'active' }, revision: 5,
    artifacts: { spec: { stage: 'spec', version: 2, content: 'Critério CSV correto', updatedAt: date }, plan: { stage: 'plan', version: 1, content: 'Criar endpoint e testar', updatedAt: date }, code: { stage: 'code', version: 1, content: 'diff --git a/export.go b/export.go\n+func ExportCSV() {}', updatedAt: date }, eval: { stage: 'eval', version: 1, content: '{"passed":true}', updatedAt: date } }, createdAt: date, updatedAt: date }
  render(<ArtifactPane messages={[]} events={[]} backend={backend} pipeline={pipeline} />)
  await userEvent.click(screen.getByRole('button', { name: 'SPEC' }))
  expect(screen.getByText('Critério CSV correto')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Plano' }))
  expect(screen.getByText('Criar endpoint e testar')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Diff' }))
  expect(screen.getByText(/ExportCSV/)).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Avaliação' }))
  expect(screen.getByText('{"passed":true}')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'PRs' }))
  expect(screen.getByText('Nenhum Relatório dos PRs salvo')).toBeInTheDocument()
})

it('shows the report the agent left when it opened the pull requests', async () => {
  const { backend } = createFakeBackend()
  const date = '2026-10-02T10:00:00Z'
  const pipeline: Pipeline = { id: 'pipeline-1', workspaceId: 'workspace-1', title: 'Exportar CSV', objective: 'Exportar faturas', currentStage: '',
    stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'completed', eval: 'completed', prs: 'completed' }, revision: 9,
    artifacts: { prs: { stage: 'prs', version: 1, content: 'PR https://github.com/acme/app/pull/3', author: 'ai', sourceSessionId: 'pr-chat', updatedAt: date } }, createdAt: date, updatedAt: date }
  render(<ArtifactPane messages={[]} events={[]} backend={backend} pipeline={pipeline} />)
  await userEvent.click(screen.getByRole('button', { name: 'PRs' }))
  expect(screen.getByText(/Relatório dos PRs · versão 1/)).toBeInTheDocument()
  expect(screen.getByText('PR https://github.com/acme/app/pull/3')).toBeInTheDocument()
})
