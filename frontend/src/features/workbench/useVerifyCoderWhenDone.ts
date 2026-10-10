import { useEffect, useRef } from 'react'
import type { Backend, Pipeline, Session } from '../../lib/backend'
import type { ActiveRun } from '../../state/session'

type Input = {
  backend: Backend
  session?: Pick<Session, 'id' | 'purpose'>
  activeRun: ActiveRun
  outcome?: string
  pipeline?: Pipeline
  onVerified: (run: Pipeline) => void
}

/**
 * The Coder of a work finishes while its conversation is open: the code is verified at once, and the person is taken to the
 * review. Only a run seen working in this view counts, so reopening an old conversation never verifies anything.
 */
export function useVerifyCoderWhenDone({ backend, session, activeRun, outcome, pipeline, onVerified }: Input) {
  const watch = useRef({ sessionId: '', working: false })
  const notify = useRef(onVerified)
  notify.current = onVerified
  useEffect(() => {
    if (!session || session.purpose !== 'code') return
    if (watch.current.sessionId !== session.id) watch.current = { sessionId: session.id, working: false }
    if (activeRun === 'running' || activeRun === 'awaiting_approval') { watch.current.working = true; return }
    if (!watch.current.working || outcome !== 'completed') return
    watch.current.working = false
    if (!pipeline || pipeline.currentStage !== 'code' || pipeline.stageStatus.code !== 'active') return
    // Without evidence yet the call is refused and the screen stays as it is: the Pipelines page still offers Check code.
    void backend.completePipelineCode(pipeline.id).then(updated => notify.current(updated), () => undefined)
  }, [backend, session?.id, session?.purpose, activeRun, outcome, pipeline?.id, pipeline?.currentStage, pipeline?.stageStatus.code])
}
