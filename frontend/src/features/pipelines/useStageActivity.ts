import { useEffect, useState } from 'react'
import type { AgentEvent, Backend, Pipeline, PipelineStage, PipelineStageActivity } from '../../lib/backend'

/** Stages whose work is done by a session of its own, so the bar can say what that session is doing. */
const sessionStages = new Set<PipelineStage>(['code', 'eval', 'prs'])
const lifecycle = new Set(['run.started', 'run.completed', 'run.failed', 'run.cancelled', 'external.run.started', 'external.run.completed', 'external.run.failed', 'external.run.cancelled', 'approval.requested', 'approval.approved', 'approval.denied'])
const working = new Set(['running', 'awaiting_approval', 'cancellation_pending'])

/**
 * What the session of an execution stage is doing right now. By default that is the pipeline's current stage while it
 * is active: the stage stays "active" until a person verifies the result, so without this the bar looks the same
 * before, during and after the run. A caller that knows better can name the stage (the pull request stage of a
 * pipeline saved before it existed has no current stage). Events trigger a re-read and a slow poll covers a run that was
 * already going when the app opened; there is only ever one pending read, however many events arrive.
 */
export function useStageActivity(backend: Backend | undefined, pipeline: Pipeline | undefined, stageOverride?: PipelineStage): PipelineStageActivity | undefined {
  const stage = stageOverride ?? (pipeline?.currentStage || undefined)
  const stageStatus = stageOverride ? 'active' : stage && pipeline ? pipeline.stageStatus[stage] : undefined
  const watching = !!backend && !!pipeline && !!stage && sessionStages.has(stage) && stageStatus === 'active'
  const [activity, setActivity] = useState<PipelineStageActivity>()
  useEffect(() => {
    if (!watching || !backend || !pipeline || !stage) { setActivity(undefined); return }
    let live = true
    let timer: ReturnType<typeof setTimeout> | undefined
    const schedule = (delay: number) => {
      if (timer) clearTimeout(timer)
      timer = setTimeout(() => { timer = undefined; void read() }, delay)
    }
    const read = async () => {
      try {
        const value = await backend.getPipelineStageActivity(pipeline.id, stage)
        if (!live || value.pipelineId !== pipeline.id || value.stage !== stage) return
        setActivity(value)
        if (working.has(value.status)) schedule(3000)
      } catch { /* the bar keeps the stage's own status */ }
    }
    setActivity(undefined)
    void read()
    // The journal event can land a moment before the session record changes; a working status keeps the poll going until it has.
    const unsubscribe = backend.onEvent((event: AgentEvent) => { if (lifecycle.has(event.type)) schedule(250) })
    return () => { live = false; if (timer) clearTimeout(timer); unsubscribe() }
  }, [backend, watching, pipeline?.id, stage, pipeline?.revision])
  return watching ? activity : undefined
}
