import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { NO_CATALOG_TIME, errorMessage, type APIModelSelection, type AuthoringModelConsentBinding, type AuthoringStage as StageReadback, type Backend, type Pipeline } from '../../lib/backend'
import { AuthoringStageModelControls } from './AuthoringStageModelControls'
import { useT } from '../../i18n'

type Props = { backend: Backend; pipeline: Pipeline; stage: 'spec' | 'plan'; onPipelineChange: (pipeline: Pipeline) => void; onSettings?: () => void }
function stateLabels(t: (source: string) => string): Record<StageReadback['state'], string> { return { pending: t('Pendente'), ready: t('Pronto para gerar'), running: t('Gerando documento'), waiting_user: t('Aguardando sua decisão'), paused: t('Tentativa interrompida'), approved: t('Aprovado'), skipped: t('Pulado'), cancellation_pending: t('Cancelamento incerto') } }
function failureLabels(t: (source: string) => string): Record<string, string> { return { provider_failed: t('Falha do provedor'), timeout: t('Tempo limite atingido'), cancelled: t('Cancelada'), interrupted: t('Interrompida'), invalid_output: t('Documento inválido'), output_overflow: t('Saída excedeu o limite'), budget_overrun: t('Uso excedeu a reserva') } }
function artifactStatusLabels(t: (source: string) => string) { return { waiting_user: t('Rascunho'), approved: t('Aprovada'), rejected: t('Rejeitada'), bypassed: t('Pulada') } }
const legacyGenerationSelection: APIModelSelection = { executor: 'api', backendId: '', profileId: '', modelId: '', catalogRevision: '', source: '', destination: '', checkedAt: NO_CATALOG_TIME, credentialToken: '', reasoningEffort: '', confirmUnverifiedManual: false, confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 4096 }
type ModelConsent = { identity: string; confirmJitLoad: boolean; confirmUnfiltered: boolean; consentBinding?: AuthoringModelConsentBinding }

export function AuthoringStage({ backend, pipeline, stage, onPipelineChange, onSettings }: Props) {
  const t = useT()
  const labels = stateLabels(t)
  const failures = failureLabels(t)
  const artifactLabels = artifactStatusLabels(t)
  const identity = `${pipeline.workspaceId}:${pipeline.id}:${stage}:${pipeline.discoveryFrozenVersion}`
  const scope = useRef(identity)
  const epoch = useRef(0)
  const [run, setRun] = useState<StageReadback>()
  const [version, setVersion] = useState<number>()
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [cancelling, setCancelling] = useState(false)
  const [unknown, setUnknown] = useState(false)
  const [feedback, setFeedback] = useState('')
  const [reason, setReason] = useState('')
  const [modelReadiness, setModelReadiness] = useState({ identity: '', ready: false })
  const [modelConsent, setModelConsent] = useState<ModelConsent>({ identity: '', confirmJitLoad: false, confirmUnfiltered: false })
  const [consentResetEpoch, setConsentResetEpoch] = useState(0)
  const modelReady = modelReadiness.identity === identity && modelReadiness.ready
  const onModelReadinessChange = useCallback((target: string, ready: boolean) => setModelReadiness(previous => previous.identity === target && previous.ready === ready ? previous : { identity: target, ready }), [])
  const onModelConsentChange = useCallback((target: string, consent: Omit<ModelConsent, 'identity'>) => setModelConsent(previous => previous.identity === target && previous.confirmJitLoad === consent.confirmJitLoad && previous.confirmUnfiltered === consent.confirmUnfiltered && JSON.stringify(previous.consentBinding) === JSON.stringify(consent.consentBinding) ? previous : { identity: target, ...consent }), [])
  const actionEpoch = useRef(0)
  const actionLock = useRef(false)
  const cancelLock = useRef(false)
  const pendingRequest = useRef('')
  const failedRequest = useRef('')
  const pendingFence = useRef({ stageRevision: 0, pipelineRevision: 0 })
  const pipelineRefreshRequest = useRef('')
  const latest = useRef<StageReadback>()
  useLayoutEffect(() => {
    scope.current = identity; epoch.current++; actionEpoch.current++; actionLock.current = false; cancelLock.current = false; pendingRequest.current = ''; failedRequest.current = ''; pipelineRefreshRequest.current = ''; latest.current = undefined
    setRun(undefined); setVersion(undefined); setError(''); setBusy(false); setCancelling(false); setUnknown(false); setFeedback(''); setReason(''); setModelReadiness({ identity, ready: false }); setModelConsent({ identity, confirmJitLoad: false, confirmUnfiltered: false, consentBinding: undefined })
    return () => { epoch.current++; actionEpoch.current++ }
  }, [identity])
  function accept(result: StageReadback, reconcileRequest = '') {
    if (result.pipelineId !== pipeline.id || result.stage !== stage) throw new Error(t('Readback fora do contexto atual.'))
    if (latest.current && (result.revision < latest.current.revision || result.pipelineRevision < latest.current.pipelineRevision)) return
    latest.current = result; setRun(result)
    // An admitted attempt proves the command's outcome even while running.
    // Absence proves rejection only for a read begun AFTER the command failed:
    // an earlier in-flight poll may have captured the pre-admission snapshot.
    const currentFence = result.revision >= pendingFence.current.stageRevision && result.pipelineRevision >= pendingFence.current.pipelineRevision
    const decision = result.actions.find(item => item.requestId === pendingRequest.current)
    const receipt = result.attempts.some(item => item.requestId === pendingRequest.current) || !!decision
    if (pendingRequest.current && currentFence && (receipt || reconcileRequest === pendingRequest.current)) {
      if (failedRequest.current === pendingRequest.current && (decision?.action === 'approve' || decision?.action === 'skip')) pipelineRefreshRequest.current = pendingRequest.current
      pendingRequest.current = ''; failedRequest.current = ''; setUnknown(false)
    }
  }
  async function syncRecoveredPipeline(isCurrent: () => boolean) {
    const request = pipelineRefreshRequest.current
    if (!request) return
    const updated = await backend.getPipeline(pipeline.id)
    if (!isCurrent() || scope.current !== identity || pipelineRefreshRequest.current !== request) return
    if (updated.id !== pipeline.id || updated.workspaceId !== pipeline.workspaceId || updated.revision < (latest.current?.pipelineRevision ?? pipeline.revision)) throw new Error(t('Readback do pipeline desatualizado.'))
    pipelineRefreshRequest.current = ''; setUnknown(false); onPipelineChange(updated)
  }
  async function read() {
    const ticket = ++epoch.current
    const reconcileRequest = failedRequest.current
    setLoading(true)
    try {
      const result = await backend.getAuthoringStage({ pipelineId: pipeline.id, stage })
      if (ticket !== epoch.current || scope.current !== identity) return
      accept(result, reconcileRequest)
      await syncRecoveredPipeline(() => ticket === epoch.current)
      if (ticket === epoch.current && scope.current === identity) setError('')
    } catch (cause) { if (ticket === epoch.current && scope.current === identity) setError(errorMessage(cause)) }
    finally { if (ticket === epoch.current && scope.current === identity) setLoading(false) }
  }
  useEffect(() => { void read() }, [backend, identity]) // Readback only; never infer on mount.
  useEffect(() => {
    if (!busy && run?.state !== 'running' && !run?.cancellationPending) return
    let stopped = false
    const identityAtStart = identity
    const deadline = Date.now() + 90_000
    let timer: ReturnType<typeof setTimeout>
    const poll = async () => {
      const reconcileRequest = failedRequest.current
      const ticket = actionEpoch.current
      try {
        const result = await backend.getAuthoringStage({ pipelineId: pipeline.id, stage })
        if (!stopped && scope.current === identityAtStart) {
          accept(result, reconcileRequest)
          await syncRecoveredPipeline(() => !stopped && ticket === actionEpoch.current)
        }
      } catch { /* Keep last durable state; explicit refresh remains available. */ }
      if (!stopped && Date.now() < deadline) timer = setTimeout(poll, 1000)
    }
    timer = setTimeout(poll, 1000)
    return () => { stopped = true; clearTimeout(timer) }
  }, [backend, identity, busy, run?.state, run?.cancellationPending])
  const artifact = run?.artifacts.find(item => item.version === (version ?? run.artifactVersion))
  const attempt = run?.attempts.find(item => item.id === artifact?.attemptId)
  const uncertain = run?.cancellationPending || run?.state === 'cancellation_pending'
  const current = pipeline.currentStage === stage && (!artifact || artifact.version === run?.artifactVersion)
  const allowed = current && !!run && !uncertain && !unknown && !busy && !cancelling
  const canAttemptGeneration = allowed && ['ready', 'paused'].includes(run!.state) && run!.attemptCount < 6 && run!.artifactVersion < 3
  const canGenerate = canAttemptGeneration && modelReady
  const canReview = allowed && run!.state === 'waiting_user' && !!artifact && !!attempt
  const canSkip = allowed && ['ready', 'paused', 'waiting_user'].includes(run!.state)
  const title = stage === 'spec' ? 'SPEC' : 'Plan'
  function ref() {
    return { pipelineId: pipeline.id, stage, requestId: crypto.randomUUID(), pipelineRevision: run!.pipelineRevision, stageRevision: run!.revision, discoveryVersion: run!.discoveryVersion, artifactVersion: run!.artifactVersion }
  }
  async function command(action: 'generate' | 'revision' | 'approve' | 'skip' | 'cancel') {
    const cancel = action === 'cancel'
    if (!run || uncertain || unknown || (cancel ? cancelLock.current : actionLock.current)) return
    if (!cancel && !allowed) return
    if ((action === 'generate' || action === 'revision') && (!modelReady || (action === 'revision' && !feedback.trim()))) return
    if (action === 'skip' && !reason.trim()) return
    if (cancel) { cancelLock.current = true; setCancelling(true) } else { actionLock.current = true; setBusy(true) }
    const ticket = actionEpoch.current
    const activeAttempt = run.attempts.find(item => item.status === 'running')
    const generating = action === 'generate' || action === 'revision'
    const reference = ref()
    const generationConsent = generating && modelConsent.identity === identity ? modelConsent : { confirmJitLoad: false, confirmUnfiltered: false, consentBinding: undefined }
    if (generating) {
      setModelConsent({ identity, confirmJitLoad: false, confirmUnfiltered: false, consentBinding: undefined })
      setConsentResetEpoch(value => value + 1)
    }
    pendingRequest.current = reference.requestId; failedRequest.current = ''
    pendingFence.current = { stageRevision: reference.stageRevision, pipelineRevision: reference.pipelineRevision }
    setError('')
    try {
      const result = generating
        ? await (action === 'generate' ? backend.generateAuthoringStage : backend.requestAuthoringStageRevision)({ ref: reference, selection: legacyGenerationSelection, feedback: action === 'revision' ? feedback : '', confirmUnfiltered: generationConsent.confirmUnfiltered, confirmJitLoad: generationConsent.confirmJitLoad, consentBinding: generationConsent.consentBinding })
        : await (action === 'approve' ? backend.approveAuthoringStage : action === 'skip' ? backend.skipAuthoringStage : backend.cancelAuthoringStage)({ ref: reference, reason: action === 'skip' ? reason : '', attemptId: cancel ? activeAttempt?.id ?? '' : '' })
      if (ticket !== actionEpoch.current || scope.current !== identity) return
      accept(result); setVersion(undefined); setFeedback(''); setReason('')
      const updated = await backend.getPipeline(pipeline.id)
      if (ticket === actionEpoch.current && scope.current === identity && updated.id === pipeline.id && updated.workspaceId === pipeline.workspaceId) onPipelineChange(updated)
    } catch (cause) {
      if (ticket !== actionEpoch.current || scope.current !== identity) return
      setError(errorMessage(cause))
      pendingRequest.current = reference.requestId; failedRequest.current = reference.requestId; setUnknown(true)
      const reconcileRequest = failedRequest.current
      try {
        const result = await backend.getAuthoringStage({ pipelineId: pipeline.id, stage })
        if (ticket === actionEpoch.current && scope.current === identity) {
          accept(result, reconcileRequest)
          await syncRecoveredPipeline(() => ticket === actionEpoch.current)
        }
      } catch { if (ticket === actionEpoch.current && scope.current === identity) setUnknown(true) }
    } finally {
      if (ticket === actionEpoch.current && scope.current === identity) { if (cancel) { cancelLock.current = false; setCancelling(false) } else { actionLock.current = false; setBusy(false) } }
    }
  }
  const modelControls = run && <AuthoringStageModelControls backend={backend} pipeline={pipeline} stage={stage} disabled={!allowed} consentResetEpoch={consentResetEpoch} onReadinessChange={onModelReadinessChange} onGenerationConsentChange={onModelConsentChange} onSettings={onSettings} />
  const actions = (canAttemptGeneration || canReview || canSkip) && <div className="authoring-stage-actions">
      {canReview && <button type="button" className="primary" onClick={() => void command('approve')}>{t('Aprovar {title}', { title })}</button>}
      {(canAttemptGeneration || (canReview && run!.artifactVersion < 3 && run!.attemptCount < 6)) && <>
        <p>{t('A próxima tentativa usa a preferência salva. A validação final do modelo acontece ao gerar. Até 3 versões e 6 chamadas por fase; reservas não são reembolsadas.')}</p>
        {!modelReady && <p className="muted authoring-hint" role="note">{t('A geração libera quando o modelo da fase estiver confirmado: use “Consultar modelos” em “Modelo e esforço desta fase”.')}</p>}
        {canAttemptGeneration ? <button type="button" className="primary" disabled={!canGenerate} onClick={() => void command('generate')}>{t('Gerar {title}', { title })}</button> : <><label>{t('Feedback para revisão')}<textarea value={feedback} maxLength={16384} onChange={event => setFeedback(event.target.value)} /></label><button type="button" disabled={!feedback.trim() || !modelReady} onClick={() => void command('revision')}>{t('Pedir revisão e gerar')}</button></>}
      </>}
      <details className="authoring-history"><summary>{t('Pular esta fase')}</summary><label>{t('Justificativa do pulo')}<textarea value={reason} maxLength={16384} onChange={event => setReason(event.target.value)} /></label><button type="button" disabled={!reason.trim()} onClick={() => void command('skip')}>{t('Pular {title}', { title })}</button></details>
    </div>
  return <section className={`authoring-workbench authoring-stage${artifact ? ' has-artifact' : ''}`} aria-label={t('{label} por IA', { label: stage === 'spec' ? 'SPEC' : 'Plan' })}>
    <div className="section-heading"><h3>{stage === 'spec' ? 'SPEC' : 'Plan'}</h3>{run && <span className="status-badge">{labels[run.state]}</span>}</div>
    {error && <p role="alert">{error}</p>}
    {loading && !run && <p role="status">{t('Lendo documento…')}</p>}
    {uncertain && <p role="alert">{t('O cancelamento não foi confirmado localmente nem pelo provedor. A chamada pode continuar consumindo recursos. Novas ações estão bloqueadas; atualizar o estado não inicia outra tentativa.')}</p>}
    {unknown && !uncertain && <p role="alert">{t('Resultado do comando ainda não confirmado. Atualize o estado; nenhuma nova geração será iniciada automaticamente.')}</p>}
    {(busy || run?.state === 'running') && <p role="status">{t('Verificação ou geração em andamento. Uma tentativa tem limite de 90 segundos; não feche esta tela para tentar novamente.')}</p>}
    <div className="action-row"><button type="button" onClick={() => void read()} disabled={loading}>{t('Atualizar estado')}</button></div>
    {!uncertain && !unknown && current && run?.state === 'running' && <button type="button" disabled={cancelling} onClick={() => void command('cancel')}>{cancelling ? t('Confirmando cancelamento…') : t('Cancelar tentativa')}</button>}
    {artifact && <>
      <p className="authoring-provenance">{t('Versão {version} · {status} · Escrito por IA', { version: artifact.version, status: artifactLabels[artifact.status] })}</p>
      {attempt ? <details className="authoring-history"><summary>{t('Origem desta versão · {model}', { model: attempt.selection.modelId })}</summary>
        <dl className="authoring-provenance"><dt>{t('API / destino')}</dt><dd>{attempt.selection.backendId} · {attempt.selection.destination}</dd><dt>{t('Modelo / esforço')}</dt><dd>{attempt.selection.modelId} · {attempt.selection.reasoningEffort || t('Automático')}</dd><dt>{t('Tentativa / sessão')}</dt><dd>{attempt.id} · {artifact.sourceSessionId}</dd><dt>{t('Fontes')}</dt><dd>Discovery v{attempt.source.discoveryVersion}{attempt.source.specVersion > 0 && ` · SPEC v${attempt.source.specVersion}`}{attempt.source.synthesisVersion > 0 && ` · ${t('Síntese v{version}', { version: attempt.source.synthesisVersion })}`}</dd><dt>{t('Catálogo verificado')}</dt><dd>{attempt.selection.checkedAt}</dd><dt>{t('Limite de saída')}</dt><dd>{attempt.selection.maxOutputTokens} tokens</dd><dt>{t('Uso / custo')}</dt><dd>{attempt.usage == null ? t('Uso não informado; reserva integral consumida.') : t('Uso registrado na tentativa.')} {t('Custo não disponível.')}</dd>{attempt.source.feedback && <><dt>{t('Feedback da revisão')}</dt><dd>{attempt.source.feedback}</dd></>}</dl>
      </details> : <p role="alert">{t('Proveniência indisponível. Atualize o estado antes de decidir.')}</p>}
      <article className="authoring-document" aria-label={t('Documento {label} versão {version}', { label: stage.toUpperCase(), version: artifact.version })}>
        <h4>{t('Resumo')}</h4><p>{artifact.content.summary}</p>
        {'requirements' in artifact.content ? <>
          <h4>{t('Requisitos')}</h4><ul>{artifact.content.requirements.map((text, i) => <li key={i}>{text}</li>)}</ul>
          <h4>{t('Fora do escopo')}</h4>{artifact.content.nonGoals.length ? <ul>{artifact.content.nonGoals.map((text, i) => <li key={i}>{text}</li>)}</ul> : <p>{t('Nenhum item registrado.')}</p>}
          <h4>{t('Critérios de aceite')}</h4><dl>{artifact.content.acceptanceCriteria.map(item => <div key={item.id}><dt>{item.id}</dt><dd>{item.criterion}</dd></div>)}</dl>
        </> : <>
          <h4>{t('Tarefas')}</h4>{artifact.content.tasks.map(task => <section key={task.id}><h5>{task.id} · {task.title}</h5><h6>{t('Arquivos')}</h6><ul>{task.files.map((text, i) => <li key={i}>{text}</li>)}</ul><h6>{t('Passos')}</h6><ol>{task.steps.map((text, i) => <li key={i}>{text}</li>)}</ol><h6>{t('Testes')}</h6><ul>{task.tests.map((text, i) => <li key={i}>{text}</li>)}</ul><p>{t('Dependências: {list}', { list: task.dependsOn.join(', ') || t('Nenhuma') })}</p></section>)}
          <h4>{t('Riscos')}</h4>{artifact.content.risks.length ? <ul>{artifact.content.risks.map((text, i) => <li key={i}>{text}</li>)}</ul> : <p>{t('Nenhum risco registrado.')}</p>}
        </>}
      </article>
    </>}
    {run && !artifact && <p>{t('Nenhum documento publicado. A geração exige uma ação sua.')}</p>}
    {/* The DOM order never changes (the model controls keep their state while a document appears); with a draft
        to judge, CSS lifts the decision right under the document and lets the model settings follow it. */}
    {modelControls}
    {actions}
    {!!run?.artifacts.length && <details className="authoring-history authoring-tail"><summary>{t('Versões do documento ({count})', { count: run.artifacts.length })}</summary><div className="action-row">{run.artifacts.map(item => <button type="button" key={item.version} aria-pressed={artifact?.version === item.version} onClick={() => setVersion(item.version)}>{t('Versão {version} · {status}', { version: item.version, status: artifactLabels[item.status].toLowerCase() })}</button>)}</div></details>}
    {run && (run.attempts.length > 0 || run.actions.length > 0) && <details className="authoring-history authoring-tail"><summary>{t('Tentativas e decisões')}</summary>
      <p className="authoring-provenance">{t('{calls} de 6 chamadas · {versions} de 3 versões · reserva restante: {input} tokens de entrada / {output} de saída', { calls: run.attemptCount, versions: run.artifactVersion, input: run.inputBudgetRemaining, output: run.outputBudgetRemaining })}</p>
      <ol className="authoring-attempts">{run.attempts.map(item => <li key={item.id}><strong>{item.errorCode ? failures[item.errorCode] ?? t('Tentativa não concluída') : item.status === 'completed' ? t('Documento gerado') : item.status === 'running' ? t('Em andamento') : t('Interrompida')}</strong><p>{item.selection.modelId} · {item.selection.backendId} · {t('versão solicitada {version}', { version: item.artifactVersion })}</p><p>{item.id} · {t('sessão {session}', { session: item.sessionId || t('ainda não vinculada') })}</p><p>{item.createdAt} · {t('reserva: {input} entrada / {output} saída', { input: item.reservedInputTokens, output: item.reservedOutputTokens })}</p>{item.cancellationState === 'pending' && <p>{t('Cancelamento ainda não confirmado.')}</p>}</li>)}</ol>
      {run.actions.map(item => <div className="authoring-decision" key={item.requestId}><strong>{{ start: t('Geração solicitada'), revision: t('Revisão solicitada'), approve: t('Aprovação humana'), skip: t('Pulo justificado'), cancel: t('Cancelamento solicitado') }[item.action] ?? t('Decisão registrada')} · {t('versão {version}', { version: item.artifactVersion })}</strong><p>{item.createdAt}</p>{item.feedback && <p>{item.feedback}</p>}{item.reason && <p>{item.reason}</p>}</div>)}
    </details>}
  </section>
}
