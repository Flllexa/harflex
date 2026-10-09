import { useEffect, useRef } from 'react'
import { Check, Circle, CircleAlert, CircleDot } from 'lucide-react'
import { t, useT } from '../i18n'
import type { Pipeline, PipelineStage, PipelineStageActivity } from '../lib/backend'
import './pipelineBar.css'

const stageDefinitions = [['discovery', 'Discovery'], ['spec', 'SPEC'], ['plan', 'Plan'], ['code', 'Code'], ['eval', 'QA'], ['prs', 'PRs']] as const
const labels = { pending: 'Pendente', active: 'Em andamento', completed: 'Concluído', skipped: 'Pulada', failed: 'Falhou', paused: 'Pausada', waiting_user: 'Aguardando decisão' } as const

type Progress = { text: string; hint?: string; tone?: 'working' | 'ready' | 'failed' }
/** The next move once the stage's own session has ended; the stage stays "active" until someone verifies it. */
const afterRun: Partial<Record<PipelineStage, string>> = { code: 'verifique o código', eval: 'registre a avaliação', prs: 'leia o relatório e conclua os PRs' }
const short: Partial<Record<PipelineStage, string>> = { code: 'verificar', eval: 'registrar', prs: 'concluir' }

/** What the session behind the current stage is doing, in the words the bar has room for. */
export function stageProgress(stage: PipelineStage, name: string, activity?: PipelineStageActivity): Progress | undefined {
  switch (activity?.status) {
    case 'running': case 'cancellation_pending': return { text: t('IA trabalhando'), tone: 'working', hint: t('{name}: a IA está executando esta etapa.', { name }) }
    case 'awaiting_approval': return { text: t('Aguardando autorização'), tone: 'ready', hint: t('{name}: a IA está esperando a sua autorização.', { name }) }
    case 'completed': return short[stage] ? { text: t('Executado · {next}', { next: t(short[stage]!) }), tone: 'ready', hint: t('{name}: a execução terminou. Para seguir, {next} na página Pipelines.', { name, next: t(afterRun[stage]!) }) } : undefined
    case 'failed': return { text: t('Execução falhou'), tone: 'failed', hint: t('{name}: a execução parou por um erro. Veja a atividade da etapa.', { name }) }
    case 'cancelled': return { text: t('Execução cancelada'), tone: 'failed', hint: t('{name}: a execução foi cancelada.', { name }) }
  }
  return undefined
}

export function PipelineBar({ pipeline: currentPipeline, onViewStage, viewStage, activity }: { pipeline?: Pipeline; onViewStage?: (stage:PipelineStage) => void; viewStage?:PipelineStage; /** What the current stage's own session is doing. */ activity?: PipelineStageActivity }) {
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
  return <nav ref={pipeline} className="pipeline" aria-label="SDD Pipeline" tabIndex={0}>
    <div className="pipeline-heading">SDD Pipeline<span title={currentPipeline?.title}>{currentPipeline?.title ?? t('Nenhum pipeline ativo')}</span></div>
    <ol ref={stages}>
      {stageDefinitions.map(([stage, name]) => {
        const status = currentPipeline?.stageStatus[stage] ?? 'pending'
        const current = stage === currentPipeline?.currentStage
        const progress = current && status === 'active' && activity?.stage === stage ? stageProgress(stage, name, activity) : undefined
        const Icon = status === 'completed' ? Check : progress?.tone === 'failed' ? CircleAlert : current ? CircleDot : Circle
        const tone = progress ? ` stage-${progress.tone}` : ''
        return <li key={name} ref={viewStage ? viewStage === stage ? currentStage : undefined : current ? currentStage : undefined} aria-current={current ? 'step' : undefined} className={`${status === 'completed' ? 'stage-completed' : status === 'failed' || progress?.tone === 'failed' ? 'stage-failed' : current ? 'stage-current' : 'stage-pending'}${tone}${viewStage===stage ? ' stage-viewed' : ''}`}>
          <button type="button" className="touch-target pipeline-stage-button" aria-label={t('Ver {name} e sua atividade', { name })} aria-pressed={viewStage===stage} disabled={!currentPipeline || !onViewStage} title={progress?.hint} onClick={() => onViewStage?.(stage)}><Icon aria-hidden="true" /><span><strong>{name}</strong><span role={current && progress ? 'status' : undefined}>{progress?.text ?? t(labels[status] ?? 'Pendente')}</span></span></button>
        </li>
      })}
    </ol>
  </nav>
}
