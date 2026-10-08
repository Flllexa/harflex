import { render,screen,within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect,it,vi } from 'vitest'
import { PipelineBar, stageProgress } from './PipelineBar'
import { designPipeline } from '../test/pipelineDesignFixture'
import type { Pipeline, PipelineStageActivity } from '../lib/backend'

it('opens a stage for inspection without changing or advancing the pipeline',async () => {
  const user = userEvent.setup(), onViewStage = vi.fn(), pipeline = designPipeline()
  render(<PipelineBar pipeline={pipeline} onViewStage={onViewStage} />)
  await user.click(screen.getByRole('button',{name:'Ver SPEC e sua atividade'}))
  expect(onViewStage).toHaveBeenCalledWith('spec')
  expect(pipeline.currentStage).toBe('discovery')
  await user.click(screen.getByRole('button',{name:'Ver Code e sua atividade'}))
  expect(onViewStage).toHaveBeenLastCalledWith('code')
})

const codeRun = (): Pipeline => ({ ...designPipeline(), currentStage: 'code', stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'active', eval: 'pending' } })
const activity = (status: string, stage: PipelineStageActivity['stage'] = 'code'): PipelineStageActivity => ({ pipelineId: 'design-1', workspaceId: 'workspace-1', stage, status, phase: '', sessionId: 'session-1', modelId: '', updatedAt: '2026-09-25T10:00:00Z' })
const stageItem = (name: string) => screen.getByRole('button', { name: `Ver ${name} e sua atividade` }).closest('li')!

it('calls the last stage QA while it keeps the stage identifier', () => {
  render(<PipelineBar pipeline={codeRun()} onViewStage={() => undefined} />)
  expect(screen.getByRole('button', { name: 'Ver QA e sua atividade' })).toBeInTheDocument()
  expect(screen.queryByText('Eval')).not.toBeInTheDocument()
})

it('says the run ended while the stage still waits for its verification', () => {
  render(<PipelineBar pipeline={codeRun()} activity={activity('completed')} onViewStage={() => undefined} />)
  const code = stageItem('Code')
  expect(within(code).getByRole('status')).toHaveTextContent('Executado · verificar')
  expect(within(code).getByRole('button')).toHaveAttribute('title', expect.stringContaining('a execução terminou'))
  expect(code).toHaveClass('stage-ready')
  expect(within(code).queryByText('Em andamento')).not.toBeInTheDocument()
  // Only the current stage speaks for the run.
  expect(within(stageItem('QA')).getByText('Pendente')).toBeInTheDocument()
})

it.each([
  ['running', 'IA trabalhando', 'stage-working'],
  ['awaiting_approval', 'Aguardando autorização', 'stage-ready'],
  ['failed', 'Execução falhou', 'stage-failed'],
  ['cancelled', 'Execução cancelada', 'stage-failed'],
])('shows %s as "%s"', (status, text, className) => {
  render(<PipelineBar pipeline={codeRun()} activity={activity(status)} onViewStage={() => undefined} />)
  const code = stageItem('Code')
  expect(within(code).getByRole('status')).toHaveTextContent(text)
  expect(code).toHaveClass(className)
})

it('keeps the stage status when the session has nothing to report', () => {
  render(<PipelineBar pipeline={codeRun()} activity={activity('ready')} onViewStage={() => undefined} />)
  expect(within(stageItem('Code')).getByText('Em andamento')).toBeInTheDocument()
  expect(within(stageItem('Code')).queryByRole('status')).not.toBeInTheDocument()
})

it('does not override a stage that is already waiting for a decision or finished', () => {
  const waiting: Pipeline = { ...codeRun(), stageStatus: { ...codeRun().stageStatus, code: 'waiting_user' } }
  render(<PipelineBar pipeline={waiting} activity={activity('completed')} onViewStage={() => undefined} />)
  expect(within(stageItem('Code')).getByText('Aguardando decisão')).toBeInTheDocument()
})

it('names the next move for QA and for the pull requests as well', () => {
  expect(stageProgress('eval', 'QA', activity('completed', 'eval'))).toMatchObject({ text: 'Executado · registrar', tone: 'ready' })
  expect(stageProgress('prs', 'PRs', activity('completed', 'prs'))).toMatchObject({ text: 'Executado · concluir', tone: 'ready' })
  expect(stageProgress('code', 'Code', undefined)).toBeUndefined()
})

it('ends the bar with the pull request stage and follows the agent that opens them', () => {
  const prs: Pipeline = { ...codeRun(), currentStage: 'prs', stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'completed', eval: 'completed', prs: 'active' } }
  render(<PipelineBar pipeline={prs} activity={activity('completed', 'prs')} onViewStage={() => undefined} />)
  const stages = screen.getAllByRole('listitem').map(item => within(item).getByRole('button').getAttribute('aria-label'))
  expect(stages).toEqual(['Ver Discovery e sua atividade', 'Ver SPEC e sua atividade', 'Ver Plan e sua atividade', 'Ver Code e sua atividade', 'Ver QA e sua atividade', 'Ver PRs e sua atividade'])
  expect(within(stageItem('PRs')).getByRole('status')).toHaveTextContent('Executado · concluir')
  expect(within(stageItem('QA')).getByText('Concluído')).toBeInTheDocument()
})

it('shows a pipeline saved before the pull request stage with it still pending', () => {
  const legacy: Pipeline = { ...codeRun(), currentStage: '', stageStatus: { discovery: 'completed', spec: 'completed', plan: 'completed', code: 'completed', eval: 'completed' } }
  render(<PipelineBar pipeline={legacy} onViewStage={() => undefined} />)
  expect(within(stageItem('PRs')).getByText('Pendente')).toBeInTheDocument()
})
