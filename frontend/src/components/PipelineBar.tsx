import { useEffect, useRef } from 'react'
import { Check, Circle, CircleAlert, CircleDot } from 'lucide-react'
import { t, useT } from '../i18n'
import type { CoderStatus, Pipeline, PipelineStage, PipelineStageActivity } from '../lib/backend'
import { stageGroups, stageNames } from '../lib/pipelineStages'
import './pipelineBar.css'

const labels = { pending: 'Pendente', active: 'Em andamento', completed: 'Concluído', skipped: 'Pulada', failed: 'Falhou', paused: 'Pausada', waiting_user: 'Aguardando decisão' } as const

type Progress = { text: string; hint?: string; tone?: 'working' | 'ready' | 'failed' }
/** The next move once the stage's own session has ended; the stage stays "active" until someone verifies it. */
const afterRun: Partial<Record<PipelineStage, string>> = { code: 'verifique o código', eval: 'registre a avaliação', prs: 'leia o relatório e conclua os PRs' }
const short: Partial<Record<PipelineStage, string>> = { code: 'verificar', eval: 'registrar', prs: 'concluir' }
/** What comes after the current stage, for the card the wide bar has room for. */
const nextStep: Record<PipelineStage, string> = { discovery: 'preparar a SPEC', spec: 'desenhar o fluxo e as telas', brainstorm: 'aprovar o fluxo e o design', plan: 'aprovar e ir para o Code', code: 'verificar o código', eval: 'registrar a avaliação', prs: 'concluir os PRs' }
/** Why a stage was skipped, when the Harflex knows: only the update that added Brainstorm skips it. */
const skippedBecause: Partial<Record<PipelineStage, string>> = { brainstorm: 'a etapa não existia quando este trabalho foi preparado' }

/** What the session behind the current stage is doing, in the words the bar has room for. */
export function stageProgress(stage: PipelineStage, name: string, activity?: PipelineStageActivity, coder?: CoderStatus): Progress | undefined {
  // A Coder run that ended asking, or without saying it finished, waits for the person's answer, not for a check.
  if (stage === 'code' && activity?.status === 'completed' && coder?.sessionId === activity.sessionId) {
    if (coder.state === 'questions') return { text: t('Coder perguntou · responder'), tone: 'ready', hint: t('{name}: o Coder fez perguntas. Responda na bancada Code para ele continuar.', { name }) }
    if (coder.state === 'unfinished' || coder.state === 'stopped') return { text: t('Coder parou · responder'), tone: 'ready', hint: t('{name}: o Coder parou sem dizer que terminou. Responda ou peça para continuar na bancada Code.', { name }) }
  }
  switch (activity?.status) {
    case 'running': case 'cancellation_pending': return { text: t('IA trabalhando'), tone: 'working', hint: t('{name}: a IA está executando esta etapa.', { name }) }
    case 'awaiting_approval': return { text: t('Aguardando autorização'), tone: 'ready', hint: t('{name}: a IA está esperando a sua autorização.', { name }) }
    case 'completed': return short[stage] ? { text: t('Executado · {next}', { next: t(short[stage]!) }), tone: 'ready', hint: t('{name}: a execução terminou. Para seguir, {next} na página Pipelines.', { name, next: t(afterRun[stage]!) }) } : undefined
    case 'failed': return { text: t('Execução falhou'), tone: 'failed', hint: t('{name}: a execução parou por um erro. Veja a atividade da etapa.', { name }) }
    case 'cancelled': return { text: t('Execução cancelada'), tone: 'failed', hint: t('{name}: a execução foi cancelada.', { name }) }
  }
  return undefined
}

export function PipelineBar({ pipeline: currentPipeline, onViewStage, viewStage, activity, coder }: { pipeline?: Pipeline; onViewStage?: (stage:PipelineStage) => void; viewStage?:PipelineStage; /** What the current stage's own session is doing. */ activity?: PipelineStageActivity; /** Where the Coder stands, when Code is the stage. */ coder?: CoderStatus }) {
  const t = useT()
  const pipeline = useRef<HTMLElement>(null)
  const stages = useRef<HTMLOListElement>(null)
  const currentStage = useRef<HTMLLIElement>(null)
  // Narrow viewports scroll the pipeline; keep the current stage identifiable at a glance.
  useEffect(() => {
    const container = pipeline.current
    if (!container) return
    let frame: number | undefined
    const ensureCurrentVisible = () => {
      frame = undefined
      const current = currentStage.current
      if (!current || container.clientWidth === 0) return
      const viewport = container.getBoundingClientRect()
      const stage = current.getBoundingClientRect()
      const left = viewport.left + container.clientLeft
      const right = left + container.clientWidth
      if (stage.left < left || stage.right > right) {
        current.scrollIntoView?.({ block: 'nearest', inline: 'center' })
      }
    }
    const scheduleVisibilityCheck = () => {
      if (frame === undefined) frame = requestAnimationFrame(ensureCurrentVisible)
    }
    const observer = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(scheduleVisibilityCheck)
    observer?.observe(container)
    if (stages.current) observer?.observe(stages.current)
    if (!observer) window.addEventListener('resize', scheduleVisibilityCheck)
    scheduleVisibilityCheck()
    return () => {
      observer?.disconnect()
      window.removeEventListener('resize', scheduleVisibilityCheck)
      if (frame !== undefined) cancelAnimationFrame(frame)
    }
  }, [currentPipeline?.currentStage,viewStage])
  const current = currentPipeline?.currentStage as PipelineStage | undefined
  const currentStatus = current ? currentPipeline?.stageStatus[current] ?? 'pending' : undefined
  const currentProgress = current && currentStatus === 'active' && activity?.stage === current ? stageProgress(current, stageNames[current], activity, coder) : undefined
  // One list of stages that the bar's own width reshapes: a line with a "now" card when it has room, stages grouped as
  // markers when it does not (pipelineBar.css). Only one copy of each stage reaches screen readers.
  return <nav ref={pipeline} className="pipeline" aria-label="SDD Pipeline" tabIndex={0}>
    <div className="pipeline-heading">SDD Pipeline<span title={currentPipeline?.title}>{currentPipeline?.title ?? t('Nenhum pipeline ativo')}</span></div>
    <ol ref={stages} className="pipeline-line">
      {stageGroups.flatMap(group => group.stages.map((stage, index) => {
        const name = stageNames[stage]
        const status = currentPipeline?.stageStatus[stage] ?? 'pending'
        const isCurrent = stage === current
        const progress = isCurrent ? currentProgress : undefined
        const Icon = status === 'completed' ? Check : progress?.tone === 'failed' ? CircleAlert : isCurrent ? CircleDot : Circle
        const tone = progress ? ` stage-${progress.tone}` : ''
        const statusText = progress?.text ?? t(labels[status as keyof typeof labels] ?? 'Pendente')
        const reason = status === 'skipped' && skippedBecause[stage] ? t(skippedBecause[stage]!) : ''
        const label = reason ? t('Ver {name} e sua atividade · pulada: {reason}', { name, reason }) : t('Ver {name} e sua atividade', { name })
        const title = reason ? t('{name} · pulada: {reason}', { name, reason }) : progress?.hint ?? `${name} · ${statusText}`
        const state = status === 'completed' ? 'stage-completed' : status === 'skipped' ? 'stage-skipped' : status === 'failed' || progress?.tone === 'failed' ? 'stage-failed' : isCurrent ? 'stage-current' : 'stage-pending'
        return <li key={stage} data-group={group.id} ref={viewStage ? viewStage === stage ? currentStage : undefined : isCurrent ? currentStage : undefined} aria-current={isCurrent ? 'step' : undefined} className={`${state}${tone}${viewStage===stage ? ' stage-viewed' : ''}${index === 0 ? ' pipeline-group-start' : ''}`}>
          {index === 0 && <span className="pipeline-group-label" aria-hidden="true">{t(group.label)}</span>}
          <button type="button" className="touch-target pipeline-stage-button" aria-label={label} aria-pressed={viewStage===stage} disabled={!currentPipeline || !onViewStage} title={title} onClick={() => onViewStage?.(stage)}><Icon aria-hidden="true" /><span><strong>{name}</strong><span role={isCurrent && progress ? 'status' : undefined}>{statusText}</span></span></button>
        </li>
      }))}
    </ol>
    {current && currentStatus && <p className="pipeline-now" aria-hidden="true">{`${stageNames[current]} · ${currentProgress?.text ?? t(labels[currentStatus as keyof typeof labels] ?? 'Pendente')} · ${t('depois: {next}', { next: t(nextStep[current]) })}`}</p>}
  </nav>
}
