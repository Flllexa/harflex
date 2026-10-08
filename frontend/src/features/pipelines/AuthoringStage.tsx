import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { NO_CATALOG_TIME, errorMessage, type APIModelSelection, type AuthoringModelConsentBinding, type AuthoringStage as StageReadback, type Backend, type Pipeline } from '../../lib/backend'
import { AuthoringStageModelControls } from './AuthoringStageModelControls'

type Props = { backend: Backend; pipeline: Pipeline; stage: 'spec' | 'plan'; onPipelineChange: (pipeline: Pipeline) => void; onSettings?: () => void }
const labels: Record<StageReadback['state'], string> = { pending: 'Pendente', ready: 'Pronto para gerar', running: 'Gerando documento', waiting_user: 'Aguardando sua decisão', paused: 'Tentativa interrompida', approved: 'Aprovado', skipped: 'Pulado', cancellation_pending: 'Cancelamento incerto' }
const failures: Record<string, string> = { provider_failed: 'Falha do provedor', timeout: 'Tempo limite atingido', cancelled: 'Cancelada', interrupted: 'Interrompida', invalid_output: 'Documento inválido', output_overflow: 'Saída excedeu o limite', budget_overrun: 'Uso excedeu a reserva' }
const artifactLabels = { waiting_user: 'Rascunho', approved: 'Aprovada', rejected: 'Rejeitada', bypassed: 'Pulada' }
const legacyGenerationSelection: APIModelSelection = { executor: 'api', backendId: '', profileId: '', modelId: '', catalogRevision: '', source: '', destination: '', checkedAt: NO_CATALOG_TIME, credentialToken: '', reasoningEffort: '', confirmUnverifiedManual: false, confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 4096 }
type ModelConsent = { identity: string; confirmJitLoad: boolean; confirmUnfiltered: boolean; consentBinding?: AuthoringModelConsentBinding }

export function AuthoringStage({ backend, pipeline, stage, onPipelineChange, onSettings }: Props) {
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
    if (result.pipelineId !== pipeline.id || result.stage !== stage) throw new Error('Readback fora do contexto atual.')
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
    if (updated.id !== pipeline.id || updated.workspaceId !== pipeline.workspaceId || updated.revision < (latest.current?.pipelineRevision ?? pipeline.revision)) throw new Error('Readback do pipeline desatualizado.')
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
      {canReview && <button type="button" className="primary" onClick={() => void command('approve')}>Aprovar {title}</button>}
      {(canAttemptGeneration || (canReview && run!.artifactVersion < 3 && run!.attemptCount < 6)) && <>
        <p>A próxima tentativa usa a preferência salva. A validação final do modelo acontece ao gerar. Até 3 versões e 6 chamadas por fase; reservas não são reembolsadas.</p>
        {!modelReady && <p className="muted authoring-hint" role="note">A geração libera quando o modelo da fase estiver confirmado: use “Consultar modelos” em “Modelo e esforço desta fase”.</p>}
        {canAttemptGeneration ? <button type="button" className="primary" disabled={!canGenerate} onClick={() => void command('generate')}>Gerar {title}</button> : <><label>Feedback para revisão<textarea value={feedback} maxLength={16384} onChange={event => setFeedback(event.target.value)} /></label><button type="button" disabled={!feedback.trim() || !modelReady} onClick={() => void command('revision')}>Pedir revisão e gerar</button></>}
      </>}
      <details className="authoring-history"><summary>Pular esta fase</summary><label>Justificativa do pulo<textarea value={reason} maxLength={16384} onChange={event => setReason(event.target.value)} /></label><button type="button" disabled={!reason.trim()} onClick={() => void command('skip')}>Pular {title}</button></details>
    </div>
  return <section className={`authoring-workbench authoring-stage${artifact ? ' has-artifact' : ''}`} aria-label={`${stage === 'spec' ? 'SPEC' : 'Plan'} por IA`}>
    <div className="section-heading"><h3>{stage === 'spec' ? 'SPEC' : 'Plan'}</h3>{run && <span className="status-badge">{labels[run.state]}</span>}</div>
    {error && <p role="alert">{error}</p>}
    {loading && !run && <p role="status">Lendo documento…</p>}
    {uncertain && <p role="alert">O cancelamento não foi confirmado localmente nem pelo provedor. A chamada pode continuar consumindo recursos. Novas ações estão bloqueadas; atualizar o estado não inicia outra tentativa.</p>}
    {unknown && !uncertain && <p role="alert">Resultado do comando ainda não confirmado. Atualize o estado; nenhuma nova geração será iniciada automaticamente.</p>}
    {(busy || run?.state === 'running') && <p role="status">Verificação ou geração em andamento. Uma tentativa tem limite de 90 segundos; não feche esta tela para tentar novamente.</p>}
    <div className="action-row"><button type="button" onClick={() => void read()} disabled={loading}>Atualizar estado</button></div>
    {!uncertain && !unknown && current && run?.state === 'running' && <button type="button" disabled={cancelling} onClick={() => void command('cancel')}>{cancelling ? 'Confirmando cancelamento…' : 'Cancelar tentativa'}</button>}
    {artifact && <>
      <p className="authoring-provenance">Versão {artifact.version} · {artifactLabels[artifact.status]} · Escrito por IA</p>
      {attempt ? <details className="authoring-history"><summary>Origem desta versão · {attempt.selection.modelId}</summary>
        <dl className="authoring-provenance"><dt>API / destino</dt><dd>{attempt.selection.backendId} · {attempt.selection.destination}</dd><dt>Modelo / esforço</dt><dd>{attempt.selection.modelId} · {attempt.selection.reasoningEffort || 'Automático'}</dd><dt>Tentativa / sessão</dt><dd>{attempt.id} · {artifact.sourceSessionId}</dd><dt>Fontes</dt><dd>Discovery v{attempt.source.discoveryVersion}{attempt.source.specVersion > 0 && ` · SPEC v${attempt.source.specVersion}`}{attempt.source.synthesisVersion > 0 && ` · Síntese v${attempt.source.synthesisVersion}`}</dd><dt>Catálogo verificado</dt><dd>{attempt.selection.checkedAt}</dd><dt>Limite de saída</dt><dd>{attempt.selection.maxOutputTokens} tokens</dd><dt>Uso / custo</dt><dd>{attempt.usage == null ? 'Uso não informado; reserva integral consumida.' : 'Uso registrado na tentativa.'} Custo não disponível.</dd>{attempt.source.feedback && <><dt>Feedback da revisão</dt><dd>{attempt.source.feedback}</dd></>}</dl>
      </details> : <p role="alert">Proveniência indisponível. Atualize o estado antes de decidir.</p>}
      <article className="authoring-document" aria-label={`Documento ${stage.toUpperCase()} versão ${artifact.version}`}>
        <h4>Resumo</h4><p>{artifact.content.summary}</p>
        {'requirements' in artifact.content ? <>
          <h4>Requisitos</h4><ul>{artifact.content.requirements.map((text, i) => <li key={i}>{text}</li>)}</ul>
          <h4>Fora do escopo</h4>{artifact.content.nonGoals.length ? <ul>{artifact.content.nonGoals.map((text, i) => <li key={i}>{text}</li>)}</ul> : <p>Nenhum item registrado.</p>}
          <h4>Critérios de aceite</h4><dl>{artifact.content.acceptanceCriteria.map(item => <div key={item.id}><dt>{item.id}</dt><dd>{item.criterion}</dd></div>)}</dl>
        </> : <>
          <h4>Tarefas</h4>{artifact.content.tasks.map(task => <section key={task.id}><h5>{task.id} · {task.title}</h5><h6>Arquivos</h6><ul>{task.files.map((text, i) => <li key={i}>{text}</li>)}</ul><h6>Passos</h6><ol>{task.steps.map((text, i) => <li key={i}>{text}</li>)}</ol><h6>Testes</h6><ul>{task.tests.map((text, i) => <li key={i}>{text}</li>)}</ul><p>Dependências: {task.dependsOn.join(', ') || 'Nenhuma'}</p></section>)}
          <h4>Riscos</h4>{artifact.content.risks.length ? <ul>{artifact.content.risks.map((text, i) => <li key={i}>{text}</li>)}</ul> : <p>Nenhum risco registrado.</p>}
        </>}
      </article>
    </>}
    {run && !artifact && <p>Nenhum documento publicado. A geração exige uma ação sua.</p>}
    {/* The DOM order never changes (the model controls keep their state while a document appears); with a draft
        to judge, CSS lifts the decision right under the document and lets the model settings follow it. */}
    {modelControls}
    {actions}
    {!!run?.artifacts.length && <details className="authoring-history authoring-tail"><summary>Versões do documento ({run.artifacts.length})</summary><div className="action-row">{run.artifacts.map(item => <button type="button" key={item.version} aria-pressed={artifact?.version === item.version} onClick={() => setVersion(item.version)}>Versão {item.version} · {artifactLabels[item.status].toLowerCase()}</button>)}</div></details>}
    {run && (run.attempts.length > 0 || run.actions.length > 0) && <details className="authoring-history authoring-tail"><summary>Tentativas e decisões</summary>
      <p className="authoring-provenance">{run.attemptCount} de 6 chamadas · {run.artifactVersion} de 3 versões · reserva restante: {run.inputBudgetRemaining} tokens de entrada / {run.outputBudgetRemaining} de saída</p>
      <ol className="authoring-attempts">{run.attempts.map(item => <li key={item.id}><strong>{item.errorCode ? failures[item.errorCode] ?? 'Tentativa não concluída' : item.status === 'completed' ? 'Documento gerado' : item.status === 'running' ? 'Em andamento' : 'Interrompida'}</strong><p>{item.selection.modelId} · {item.selection.backendId} · versão solicitada {item.artifactVersion}</p><p>{item.id} · sessão {item.sessionId || 'ainda não vinculada'}</p><p>{item.createdAt} · reserva: {item.reservedInputTokens} entrada / {item.reservedOutputTokens} saída</p>{item.cancellationState === 'pending' && <p>Cancelamento ainda não confirmado.</p>}</li>)}</ol>
      {run.actions.map(item => <div className="authoring-decision" key={item.requestId}><strong>{{ start: 'Geração solicitada', revision: 'Revisão solicitada', approve: 'Aprovação humana', skip: 'Pulo justificado', cancel: 'Cancelamento solicitado' }[item.action] ?? 'Decisão registrada'} · versão {item.artifactVersion}</strong><p>{item.createdAt}</p>{item.feedback && <p>{item.feedback}</p>}{item.reason && <p>{item.reason}</p>}</div>)}
    </details>}
  </section>
}
