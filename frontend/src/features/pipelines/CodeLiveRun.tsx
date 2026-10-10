import { useEffect, useMemo, useRef, useState } from 'react'
import { Bot, Check, CircleAlert, CircleDashed, FileText, Globe, LoaderCircle, MessageSquare, PencilLine, Search, SquareTerminal, Wrench } from 'lucide-react'
import type { Backend, Pipeline, PipelineStageActivity } from '../../lib/backend'
import { beforePlan, buildActivityFlow, type Action, type ActionKind } from '../../components/activity/activityFlow'
import { useT } from '../../i18n'
import { useRunJournal } from './QALiveRun'

const busyStatuses = ['running', 'awaiting_approval', 'cancellation_pending']
const kindIcons: Record<ActionKind, typeof FileText> = { read: FileText, write: PencilLine, search: Search, shell: SquareTerminal, web: Globe, tool: Wrench, agent: Bot }

/** What the Coder of this work is doing: the stage's activity, read while a run is on. Undefined until the first read. */
export function useCodeRun(backend: Backend, run?: Pipeline) {
  const [activity, setActivity] = useState<PipelineStageActivity>()
  const active = !!run && run.currentStage === 'code' && run.stageStatus.code === 'active'
  const pipelineId = run?.id
  useEffect(() => {
    if (!active || !pipelineId) { setActivity(undefined); return }
    let live = true
    let timer: ReturnType<typeof setTimeout> | undefined
    const read = async () => {
      try {
        const value = await backend.getPipelineStageActivity(pipelineId, 'code')
        if (!live) return
        setActivity(value)
        timer = setTimeout(() => void read(), busyStatuses.includes(value.status) || value.phase !== '' ? 1500 : 5000)
      } catch { if (live) timer = setTimeout(() => void read(), 5000) }
    }
    void read()
    return () => { live = false; if (timer) clearTimeout(timer) }
  }, [backend, pipelineId, active])
  return activity
}

/** True while the Coder is at work (or waiting for an approval), so the bench shows the run instead of the start screen. */
export function codeIsRunning(activity?: PipelineStageActivity) {
  return !!activity && !!activity.sessionId && busyStatuses.includes(activity.status)
}

/**
 * When the Coder's run ends while the person watches the bench, its code is verified at once and the review opens, so
 * nobody has to find the Check code button. Only a run seen working counts: an old finished one is never verified again.
 */
export function useAutoVerifyCode(backend: Backend, run: Pipeline | undefined, activity: PipelineStageActivity | undefined, onVerified: (run: Pipeline) => void) {
  const seenWorking = useRef('')
  const notify = useRef(onVerified)
  notify.current = onVerified
  useEffect(() => {
    if (!run || !activity?.sessionId) return
    if (codeIsRunning(activity)) { seenWorking.current = activity.sessionId; return }
    if (activity.status !== 'completed' || seenWorking.current !== activity.sessionId) return
    seenWorking.current = ''
    if (run.currentStage !== 'code' || run.stageStatus.code !== 'active') return
    // Without evidence yet, or already verified, nothing changes: the start screen still offers Check code.
    void backend.completePipelineCode(run.id).then(updated => notify.current(updated), () => undefined)
  }, [backend, run?.id, run?.currentStage, run?.stageStatus.code, activity?.status, activity?.sessionId])
}

function ActionRow({ action }: { action: Action }) {
  const Icon = kindIcons[action.kind] ?? Wrench
  return <li className={`code-live-action is-${action.status}`}>
    <span className="code-live-action-status" aria-hidden="true">{action.status === 'running' || action.status === 'approval' ? <LoaderCircle className="qa-spin" /> : action.status === 'done' ? <Check /> : action.status === 'failed' ? <CircleAlert /> : <CircleDashed />}</span>
    <Icon className="code-live-action-kind" aria-hidden="true" />
    <span className="code-live-action-text"><span>{action.label}</span>{action.detail && <code className="mono">{action.detail}</code>}</span>
  </li>
}

/** The Coder's run as it happens: the plan it declared, the step it is on, and what it does in each step. */
export function CodeLiveRun({ backend, activity, onOpenSession }: { backend: Backend; activity: PipelineStageActivity; onOpenSession?: (sessionId: string) => void }) {
  const t = useT()
  const journal = useRunJournal(backend, activity.sessionId)
  const flow = useMemo(() => buildActivityFlow(journal), [journal])
  const waiting = activity.status === 'awaiting_approval'
  const current = [...flow.actions].reverse().find(action => action.status === 'running' || action.status === 'approval')
  const done = flow.plan.filter(step => step.status === 'completed').length
  const before = flow.actions.filter(action => action.step === beforePlan)

  return <section className="code-live" aria-label={t('Code em andamento')} aria-busy="true">
    <header className="code-live-head">
      <span className="code-live-badge" aria-hidden="true"><LoaderCircle className="qa-spin" /></span>
      <div>
        <strong>{waiting ? t('O Coder espera a sua aprovação') : t('O Coder está trabalhando')}</strong>
        <p className="muted" aria-live="polite">{current ? t('Agora: {action}', { action: current.label }) : flow.plan.length > 0 ? t('{done} de {total} etapas do plano', { done, total: flow.plan.length }) : t('Preparando o trabalho…')}{activity.modelId ? ` · ${activity.modelId}` : ''}</p>
      </div>
      {onOpenSession && <button type="button" className="touch-target secondary-button" onClick={() => onOpenSession(activity.sessionId)}><MessageSquare aria-hidden="true" />{t('Ver a conversa')}</button>}
    </header>
    {waiting && <p className="project-warning" role="status">{t('Há uma ação esperando a sua aprovação. Aprove ou negue na conversa para o Coder continuar.')}</p>}

    {before.length > 0 && <ul className="code-live-actions" aria-label={t('Primeiros passos')}>{before.map(action => <ActionRow key={action.id} action={action} />)}</ul>}
    {flow.plan.length > 0
      ? <ol className="code-live-plan" aria-label={t('Plano do Coder')}>{flow.plan.map((step, index) => {
        const actions = flow.actions.filter(action => action.step === index)
        const shown = step.status === 'in_progress' ? actions.slice(-6) : []
        return <li key={index} className={`code-live-step is-${step.status}`} aria-current={step.status === 'in_progress' ? 'step' : undefined}>
          <span className="code-live-step-mark" aria-hidden="true">{step.status === 'completed' ? <Check /> : step.status === 'in_progress' ? <LoaderCircle className="qa-spin" /> : <CircleDashed />}</span>
          <div className="code-live-step-body">
            <strong>{step.text}</strong>
            {step.status === 'completed' && actions.length > 0 && <span className="muted">{actions.length === 1 ? t('1 ação') : t('{count} ações', { count: actions.length })}</span>}
            {shown.length > 0 && <ul className="code-live-actions">{shown.map(action => <ActionRow key={action.id} action={action} />)}</ul>}
          </div>
        </li>
      })}</ol>
      : before.length === 0 && <p className="muted code-live-empty">{t('O Coder ainda não publicou o plano de trabalho. Quando publicar, as etapas e as ações de cada uma aparecem aqui.')}</p>}
  </section>
}
