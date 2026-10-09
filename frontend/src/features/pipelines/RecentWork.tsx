import { useEffect, useState } from 'react'
import type { Backend, Pipeline } from '../../lib/backend'
import { t } from '../../i18n'

const stageNames: Record<string, string> = { discovery: 'Discovery', spec: 'SPEC', plan: 'Plan', code: 'Code', eval: 'QA', prs: 'PRs' }
// Read at call time: the language is chosen on screen, not at startup.
function statusNames(): Record<string, string> { return { active: t('em andamento'), waiting_user: t('aguardando você'), failed: t('falhou'), paused: t('pausado'), pending: t('pendente'), completed: t('concluído'), skipped: t('pulado') } }

/** Where a work item is: its current stage and how it stands, or that it finished. */
export function workProgress(run: Pipeline) {
  if (!run.currentStage) return t('Concluído')
  const status = statusNames()[run.stageStatus[run.currentStage] ?? ''] ?? run.stageStatus[run.currentStage]
  return `${stageNames[run.currentStage] ?? run.currentStage}${status ? ` · ${status}` : ''}`
}

/** The project's SDD work, newest first, read again when a work item is created or the open one changes. */
export function useRecentWork(backend: Backend | undefined, workspaceId?: string, refreshKey?: unknown) {
  const [runs, setRuns] = useState<Pipeline[]>([])
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [reload, setReload] = useState(0)
  useEffect(() => {
    if (!backend || !workspaceId) { setRuns([]); setState('ready'); return }
    let live = true
    setState(current => current === 'ready' ? current : 'loading')
    backend.listPipelines(workspaceId).then(found => {
      if (!live) return
      setRuns(found.filter(run => run.workspaceId === workspaceId).sort((a, b) => b.updatedAt.localeCompare(a.updatedAt)))
      setState('ready')
    }, () => { if (live) setState('error') })
    return () => { live = false }
  }, [backend, workspaceId, refreshKey, reload])
  useEffect(() => backend?.onPipelineCreated(change => { if (change.workspaceId === workspaceId) setReload(value => value + 1) }), [backend, workspaceId])
  return { runs, state, reload: () => setReload(value => value + 1) }
}
