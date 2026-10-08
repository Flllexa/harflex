import { Fragment, useEffect, useRef, useState } from 'react'
import { errorMessage, type Backend, type Pipeline } from '../../lib/backend'

type ReviewStage = 'code' | 'eval'
type Props = { backend: Backend; run: Pipeline; stage: ReviewStage; onPipelineChange: (run: Pipeline) => void }
type EvaluationEvidence = {
  passed?: boolean
  findings?: string[]
  criteria?: { criterion: string; evidence: string }[]
  criteriaSource?: { sourceStage?: string; sourceVersion?: number; sourceDigest?: string; synthesisVersion?: number; synthesisDigest?: string; bypasses?: { stage: string; reason: string }[] }
}
const stageLabel: Record<ReviewStage, string> = { code: 'Code', eval: 'QA' }
const reviewLabel = (decision: string) => decision === 'approve' ? 'Aprovado' : 'Revisão solicitada'
const sourceStageLabel = (stage: string) => ({ discovery: 'Discovery', spec: 'SPEC', plan: 'Plan' }[stage] ?? stage)

function evaluationEvidence(content: string): EvaluationEvidence | undefined {
  try {
    const value: unknown = JSON.parse(content)
    return typeof value === 'object' && value !== null ? value as EvaluationEvidence : undefined
  } catch { return undefined }
}

export function PipelineExecutionReview({ backend, run, stage, onPipelineChange }: Props) {
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

  return <section className="pipeline-review" aria-label={`Revisão de ${stageLabel[stage]}`}>
    <div className="pipeline-review-heading"><div><h3>Revise {stageLabel[stage]} antes de continuar</h3><p className="muted">A decisão fica registrada nesta versão e vinculada ao conteúdo exibido.</p></div><span className="status-chip">Versão {artifact?.version ?? '—'}</span></div>
    {!currentReviewReady && <p className="project-warning" role="alert">A evidência ou o hash desta versão não está disponível. Atualize o pipeline antes de decidir.</p>}
    {artifact && <>
      {stage === 'code' ? <pre className="mono diff-body" aria-label="Diff de Code">{artifact.content}</pre> : <EvaluationSummary evidence={evidence} />}
      <details className="pipeline-review-provenance"><summary>Proveniência e integridade</summary>
        <dl><dt>Versão</dt><dd>{artifact.version}</dd><dt>SHA-256 do artefato</dt><dd className="mono">{artifact.contentDigest || 'Indisponível'}</dd><dt>Sessão de origem</dt><dd className="mono">{artifact.sourceSessionId || 'Indisponível'}</dd>
          {stage === 'eval' && evidence?.criteriaSource && <><dt>Fonte dos critérios</dt><dd>{sourceStageLabel(evidence.criteriaSource.sourceStage ?? 'Indisponível')} v{evidence.criteriaSource.sourceVersion ?? '—'}</dd><dt>SHA-256 da fonte</dt><dd className="mono">{evidence.criteriaSource.sourceDigest ?? 'Indisponível'}</dd>
            {evidence.criteriaSource.synthesisVersion !== undefined && <><dt>Síntese aprovada</dt><dd>v{evidence.criteriaSource.synthesisVersion}</dd><dt>SHA-256 da síntese</dt><dd className="mono">{evidence.criteriaSource.synthesisDigest ?? 'Indisponível'}</dd></>}
            {evidence.criteriaSource.bypasses?.map(item => <Fragment key={`${item.stage}:${item.reason}`}><dt>Etapa pulada · {sourceStageLabel(item.stage)}</dt><dd>{item.reason || 'Sem justificativa registrada'}</dd></Fragment>)}
          </>}
        </dl>
      </details>
      {!canApprove && stage === 'eval' && currentReviewReady && <p className="project-warning" role="status">Esta avaliação não passou. Corrija o Code e envie uma nova avaliação para seguir para os PRs.</p>}
      {currentReviewReady && <>
        <label className="field pipeline-review-feedback">Feedback para revisão<textarea value={feedback} onChange={event => { setFeedback(event.target.value); intent.current = undefined }} rows={3} maxLength={8192} placeholder={`Descreva o que deve mudar em ${stageLabel[stage]}.`} disabled={pending} /></label>
        <div className="pipeline-actions">
          {canApprove && <button type="button" className="touch-target primary-button" onClick={() => void decide('approve')} disabled={pending}>{pending ? 'Registrando decisão…' : stage === 'code' ? 'Aprovar Code e iniciar QA' : 'Aprovar QA e seguir para os PRs'}</button>}
          <button type="button" className="touch-target secondary-button" onClick={() => void decide('request_revision')} disabled={pending || !feedback.trim()}>{pending ? 'Registrando decisão…' : `Pedir revisão do ${stageLabel[stage]}`}</button>
        </div>
      </>}
    </>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {priorReviews.length > 0 && <details className="pipeline-review-history"><summary>Decisões anteriores ({priorReviews.length})</summary><ol>{priorReviews.map((review, index) => <li key={`${review.stage}:${review.version}:${review.createdAt}:${index}`}>
      <strong>{reviewLabel(review.decision)} · versão {review.version}</strong><p className="muted">{review.actor} · {new Date(review.createdAt).toLocaleString('pt-BR')}</p>{review.feedback && <p>{review.feedback}</p>}
      <details><summary>Ver conteúdo revisado · SHA-256 {review.contentDigest.slice(0, 12)}…</summary><pre className="mono diff-body">{review.content}</pre><code className="mono">{review.contentDigest}</code></details>
    </li>)}</ol></details>}
  </section>
}

function EvaluationSummary({ evidence }: { evidence?: EvaluationEvidence }) {
  if (!evidence || typeof evidence.passed !== 'boolean') return <p className="project-warning" role="alert">O resultado do QA não pôde ser interpretado. Atualize o pipeline para reler a evidência.</p>
  return <div className="pipeline-evaluation-summary">
    <p className={evidence.passed ? 'form-success' : 'form-error'} role="status">{evidence.passed ? 'Resultado: critérios atendidos' : 'Resultado: critérios não atendidos'}</p>
    {evidence.criteriaSource && <p>Critérios com base em {sourceStageLabel(evidence.criteriaSource.sourceStage ?? 'fonte indisponível')} v{evidence.criteriaSource.sourceVersion ?? '—'}.</p>}
    {!!evidence.findings?.length && <><h4>Pontos encontrados</h4><ul>{evidence.findings.map((item, index) => <li key={index}>{item}</li>)}</ul></>}
    {!!evidence.criteria?.length && <><h4>Matriz de evidências</h4><dl>{evidence.criteria.map((item, index) => <div key={`${item.criterion}:${index}`}><dt>{item.criterion}</dt><dd>{item.evidence}</dd></div>)}</dl></>}
  </div>
}
