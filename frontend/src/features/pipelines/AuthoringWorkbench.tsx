import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import type { Backend, Pipeline } from '../../lib/backend'
import { AuthoringBrainstorm } from './AuthoringBrainstorm'
import { AuthoringStage } from './AuthoringStage'
import { useT } from '../../i18n'

export function AuthoringWorkbench({ backend, pipeline, onPipelineChange, onSettings }: { backend: Backend; pipeline: Pipeline; onPipelineChange: (pipeline: Pipeline) => void; onSettings?: () => void }) {
  const t = useT()
  const [inspected, setInspected] = useState<'discovery' | 'spec' | 'plan'>(pipeline.currentStage === 'discovery' ? 'discovery' : pipeline.currentStage === 'spec' ? 'spec' : 'plan')
  const scope = `${pipeline.workspaceId}:${pipeline.id}`
  const scopeRef = useRef(scope)
  const epoch = useRef(0)
  const [safety, setSafety] = useState<'checking' | 'unknown' | 'pending' | 'clear'>('checking')
  useLayoutEffect(() => {
    scopeRef.current = scope; epoch.current++; setSafety('checking')
    setInspected(pipeline.currentStage === 'discovery' ? 'discovery' : pipeline.currentStage === 'spec' ? 'spec' : 'plan')
    return () => { epoch.current++ }
  }, [scope])
  useLayoutEffect(() => { epoch.current++; setSafety('checking') }, [pipeline.revision, inspected])
  async function readDerivationSafety() {
    const ticket = ++epoch.current
    setSafety('checking')
    if (!pipeline.discoveryFrozenVersion) { setSafety('clear'); return }
    try {
      const stages = await Promise.all((['spec', 'plan'] as const).map(stage => backend.getAuthoringStage({ pipelineId: pipeline.id, stage })))
      if (ticket !== epoch.current || scopeRef.current !== scope) return
      if (stages.some((result, index) => result.pipelineId !== pipeline.id || result.stage !== (index === 0 ? 'spec' : 'plan') || result.pipelineRevision < pipeline.revision) || stages[0].pipelineRevision !== stages[1].pipelineRevision) throw new Error('Readback inconsistente')
      setSafety(stages.some(result => result.cancellationPending || result.state === 'cancellation_pending' || result.attempts.some(attempt => attempt.cancellationState === 'pending')) ? 'pending' : 'clear')
    } catch { if (ticket === epoch.current && scopeRef.current === scope) setSafety('unknown') }
  }
  useEffect(() => { void readDerivationSafety(); return () => { epoch.current++ } }, [backend, scope, pipeline.revision, inspected])
  return <>
    {pipeline.currentStage !== 'discovery' && <nav className="pipeline-archive-tabs" aria-label={t('Documentos do pipeline')}>
      <button type="button" className="touch-target secondary-button" aria-pressed={inspected === 'discovery'} onClick={() => setInspected('discovery')}>{t('Ver Discovery')}</button>
      <button type="button" className="touch-target secondary-button" aria-pressed={inspected === 'spec'} onClick={() => setInspected('spec')}>{t('Ver SPEC')}</button>
      <button type="button" className="touch-target secondary-button" aria-pressed={inspected === 'plan'} disabled={pipeline.stageStatus.plan === 'pending'} onClick={() => setInspected('plan')}>{t('Ver Plan')}</button>
    </nav>}
    {inspected === 'discovery' ? <AuthoringBrainstorm key={scope} backend={backend} pipeline={pipeline} onPipelineChange={onPipelineChange} derivationSafety={safety} onRefreshDerivationSafety={() => void readDerivationSafety()} onSettings={onSettings} /> : <AuthoringStage key={`${scope}:${inspected}`} backend={backend} pipeline={pipeline} stage={inspected} onPipelineChange={onPipelineChange} onSettings={onSettings} />}
    {['code', 'eval'].includes(pipeline.currentStage) && <p className="muted">{t('Discovery, SPEC e Plan permanecem disponíveis para consulta. Use os controles do pipeline abaixo para executar Code e QA com evidências ligadas a este trabalho.')}</p>}
  </>
}
