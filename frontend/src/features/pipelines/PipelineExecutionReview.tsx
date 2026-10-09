import { Fragment, useEffect, useRef, useState } from 'react'
import { errorMessage, type Backend, type Pipeline } from '../../lib/backend'
import { localeTag, useT } from '../../i18n'

type ReviewStage = 'code' | 'eval'
type Props = { backend: Backend; run: Pipeline; stage: ReviewStage; onPipelineChange: (run: Pipeline) => void }
type EvaluationEvidence = {
  passed?: boolean
  findings?: string[]
  criteria?: { criterion: string; evidence: string }[]
  criteriaSource?: { sourceStage?: string; sourceVersion?: number; sourceDigest?: string; synthesisVersion?: number; synthesisDigest?: string; bypasses?: { stage: string; reason: string }[] }
}
const stageLabel: Record<ReviewStage, string> = { code: 'Code', eval: 'QA' }
const sourceStageLabel = (stage: string) => ({ discovery: 'Discovery', spec: 'SPEC', plan: 'Plan' }[stage] ?? stage)

function evaluationEvidence(content: string): EvaluationEvidence | undefined {
  try {
    const value: unknown = JSON.parse(content)
    return typeof value === 'object' && value !== null ? value as EvaluationEvidence : undefined
  } catch { return undefined }
}

export function PipelineExecutionReview({ backend, run, stage, onPipelineChange }: Props) {
  const t = useT()
  const reviewLabel = (decision: string) => decision === 'approve' ? t('Aprovado') : t('Revisão solicitada')
  const artifact = run.artifacts[stage]
  const [feedback, setFeedback] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const intent = useRef<{ key: string; requestId: string }>()
  const evidence = stage === 'eval' && artifact ? evaluationEvidence(artifact.content) : undefined
  const currentReviewReady = !!artifact && artifact.author === 'ai' && !!artifact.sourceSessionId && !!artifact.contentDigest && run.stageStatus[stage] === 'waiting_user'
  const canApprove = currentReviewReady && (stage === 'code' || evidence?.passed === true)
  const priorReviews = (run.executionReviews ?? []).filter(item => item.stage === stage)

  useEffect(() => {
    setFeedback('')
    setError('')
    intent.current = undefined
  }, [run.id, stage, artifact?.version, artifact?.contentDigest, run.revision])

  async function decide(decision: 'approve' | 'request_revision') {
    if (!artifact || pending || !currentReviewReady || (decision === 'approve' && !canApprove) || (decision === 'request_revision' && !feedback.trim())) return
    const normalizedFeedback = decision === 'approve' ? '' : feedback.trim()
    const key = `${run.id}:${stage}:${artifact.version}:${artifact.contentDigest}:${decision}:${normalizedFeedback}:${run.revision}`
    if (intent.current?.key !== key) intent.current = { key, requestId: crypto.randomUUID() }
    setPending(true)
    setError('')
    try {
      const updated = await backend.decidePipelineExecutionArtifact({
        pipelineId: run.id,
        requestId: intent.current.requestId,
        stage,
        artifactVersion: artifact.version,
        artifactDigest: artifact.contentDigest!,
        decision,
        feedback: normalizedFeedback,
        pipelineRevision: run.revision,
      })
      intent.current = undefined
      onPipelineChange(updated)
    } catch (failure) {
      setError(errorMessage(failure))
    } finally { setPending(false) }
  }

  return <section className="pipeline-review" aria-label={t('Revisão de {stage}', { stage: stageLabel[stage] })}>
    <div className="pipeline-review-heading"><div><h3>{t('Revise {stage} antes de continuar', { stage: stageLabel[stage] })}</h3><p className="muted">{t('A decisão fica registrada nesta versão e vinculada ao conteúdo exibido.')}</p></div><span className="status-chip">{t('Versão {version}', { version: artifact?.version ?? '—' })}</span></div>
    {!currentReviewReady && <p className="project-warning" role="alert">{t('A evidência ou o hash desta versão não está disponível. Atualize o pipeline antes de decidir.')}</p>}
    {artifact && <>
      {stage === 'code' ? <pre className="mono diff-body" aria-label={t('Diff de Code')}>{artifact.content}</pre> : <EvaluationSummary evidence={evidence} />}
      <details className="pipeline-review-provenance"><summary>{t('Proveniência e integridade')}</summary>
        <dl><dt>{t('Versão')}</dt><dd>{artifact.version}</dd><dt>{t('SHA-256 do artefato')}</dt><dd className="mono">{artifact.contentDigest || t('Indisponível')}</dd><dt>{t('Sessão de origem')}</dt><dd className="mono">{artifact.sourceSessionId || t('Indisponível')}</dd>
          {stage === 'eval' && evidence?.criteriaSource && <><dt>{t('Fonte dos critérios')}</dt><dd>{sourceStageLabel(evidence.criteriaSource.sourceStage ?? t('Indisponível'))} v{evidence.criteriaSource.sourceVersion ?? '—'}</dd><dt>{t('SHA-256 da fonte')}</dt><dd className="mono">{evidence.criteriaSource.sourceDigest ?? t('Indisponível')}</dd>
            {evidence.criteriaSource.synthesisVersion !== undefined && <><dt>{t('Síntese aprovada')}</dt><dd>v{evidence.criteriaSource.synthesisVersion}</dd><dt>{t('SHA-256 da síntese')}</dt><dd className="mono">{evidence.criteriaSource.synthesisDigest ?? t('Indisponível')}</dd></>}
            {evidence.criteriaSource.bypasses?.map(item => <Fragment key={`${item.stage}:${item.reason}`}><dt>{t('Etapa pulada · {stage}', { stage: sourceStageLabel(item.stage) })}</dt><dd>{item.reason || t('Sem justificativa registrada')}</dd></Fragment>)}
          </>}
        </dl>
      </details>
      {!canApprove && stage === 'eval' && currentReviewReady && <p className="project-warning" role="status">{t('Esta avaliação não passou. Corrija o Code e envie uma nova avaliação para seguir para os PRs.')}</p>}
      {currentReviewReady && <>
        <label className="field pipeline-review-feedback">{t('Feedback para revisão')}<textarea value={feedback} onChange={event => { setFeedback(event.target.value); intent.current = undefined }} rows={3} maxLength={8192} placeholder={t('Descreva o que deve mudar em {stage}.', { stage: stageLabel[stage] })} disabled={pending} /></label>
        <div className="pipeline-actions">
          {canApprove && <button type="button" className="touch-target primary-button" onClick={() => void decide('approve')} disabled={pending}>{pending ? t('Registrando decisão…') : stage === 'code' ? t('Aprovar Code e iniciar QA') : t('Aprovar QA e seguir para os PRs')}</button>}
          <button type="button" className="touch-target secondary-button" onClick={() => void decide('request_revision')} disabled={pending || !feedback.trim()}>{pending ? t('Registrando decisão…') : t('Pedir revisão do {stage}', { stage: stageLabel[stage] })}</button>
        </div>
      </>}
    </>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {priorReviews.length > 0 && <details className="pipeline-review-history"><summary>{t('Decisões anteriores ({count})', { count: priorReviews.length })}</summary><ol>{priorReviews.map((review, index) => <li key={`${review.stage}:${review.version}:${review.createdAt}:${index}`}>
      <strong>{t('{label} · versão {version}', { label: reviewLabel(review.decision), version: review.version })}</strong><p className="muted">{review.actor} · {new Date(review.createdAt).toLocaleString(localeTag())}</p>{review.feedback && <p>{review.feedback}</p>}
      <details><summary>{t('Ver conteúdo revisado · SHA-256 {digest}…', { digest: review.contentDigest.slice(0, 12) })}</summary><pre className="mono diff-body">{review.content}</pre><code className="mono">{review.contentDigest}</code></details>
    </li>)}</ol></details>}
  </section>
}

function EvaluationSummary({ evidence }: { evidence?: EvaluationEvidence }) {
  const t = useT()
  if (!evidence || typeof evidence.passed !== 'boolean') return <p className="project-warning" role="alert">{t('O resultado do QA não pôde ser interpretado. Atualize o pipeline para reler a evidência.')}</p>
  return <div className="pipeline-evaluation-summary">
    <p className={evidence.passed ? 'form-success' : 'form-error'} role="status">{evidence.passed ? t('Resultado: critérios atendidos') : t('Resultado: critérios não atendidos')}</p>
    {evidence.criteriaSource && <p>{t('Critérios com base em {stage} v{version}.', { stage: sourceStageLabel(evidence.criteriaSource.sourceStage ?? t('fonte indisponível')), version: evidence.criteriaSource.sourceVersion ?? '—' })}</p>}
    {!!evidence.findings?.length && <><h4>{t('Pontos encontrados')}</h4><ul>{evidence.findings.map((item, index) => <li key={index}>{item}</li>)}</ul></>}
    {!!evidence.criteria?.length && <><h4>{t('Matriz de evidências')}</h4><dl>{evidence.criteria.map((item, index) => <div key={`${item.criterion}:${index}`}><dt>{item.criterion}</dt><dd>{item.evidence}</dd></div>)}</dl></>}
  </div>
}
