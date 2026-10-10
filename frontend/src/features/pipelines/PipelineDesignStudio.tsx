import { useEffect, useLayoutEffect, useRef, useState, type FormEvent } from 'react'
import { ArrowRight, FileText, MessageSquare, RefreshCw, Send, Square } from 'lucide-react'
import { errorMessage, type Backend, type Pipeline, type PipelineDesign, type PipelineDesignRef, type PipelineDesignStage, type PreparePipelineDesignInput, type SDDModelSelection, type StageExecutor } from '../../lib/backend'
import { IonPicker } from '../../components/IonPicker'
import { DesignMarkdown, PipelineDesignDocument, designLabels, designStageOrder, savedManualEditorStage } from './PipelineDesignDocument'
import { PipelineDesignModel } from './PipelineDesignModel'
import './pipelineDesign.css'
import { localeTag, useT } from '../../i18n'

type Props = { backend: Backend; pipeline: Pipeline; stageExecutors?: StageExecutor[]; onPipelineChange: (pipeline: Pipeline) => void; onSettings?: () => void; autoPrepareRequest?: string; onAutoPrepareConsumed?: (requestId: string) => void; requestedStage?: PipelineDesignStage; viewRequestId?: number }
type Command = 'prepare' | 'save' | 'restore' | 'approve' | 'derive' | 'cancel'
type Receipt = { requestId: string; message: string; clearComposer: boolean; admitted: boolean }
const running = (design?: PipelineDesign) => design?.state === 'running' || design?.state === 'cancellation_pending'
const initialPreparation = 'Prepare a SPEC e o Plan para o Discovery salvo.'
type ComposerDraft = { message: string; target: PipelineDesignStage | 'all' }
// Navigation drafts live only in this renderer's memory, scoped to a backend and work.
// No provider selection, credential, model token or draft is written to browser storage.
const composerMemory = new WeakMap<Backend, Map<string, ComposerDraft>>()
const readComposer = (backend: Backend, scope: string): ComposerDraft => composerMemory.get(backend)?.get(scope) ?? { message: '', target: 'all' }
function rememberComposer(backend: Backend, scope: string, draft: ComposerDraft) {
  let drafts = composerMemory.get(backend)
  if (!drafts) { drafts = new Map(); composerMemory.set(backend, drafts) }
  if (!draft.message && draft.target === 'all') drafts.delete(scope)
  else drafts.set(scope, draft)
}

export function PipelineDesignStudio({ backend, pipeline, stageExecutors = [], onPipelineChange, onSettings, autoPrepareRequest, onAutoPrepareConsumed, requestedStage, viewRequestId }: Props) {
  const t = useT()
  const staleSpecMessage = t('Atualize a SPEC com o Discovery atual antes de preparar o Plan.')
  const scope = `${pipeline.workspaceId}:${pipeline.id}`, scopeRef = useRef(scope), epoch = useRef(0), readNumber = useRef(0)
  const [design, setDesign] = useState<PipelineDesign>(), designRef = useRef<PipelineDesign>()
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading'), [error, setError] = useState(''), [notice, setNotice] = useState('')
  const [pending, setPending] = useState<Command>(), pendingRef = useRef<Command>(), receipt = useRef<Receipt>()
  const preparationIntent = useRef<{ key: string; input: PreparePipelineDesignInput }>()
  const [confirmedPipeline, setConfirmedPipeline] = useState<Pipeline>(), confirmationNumber = useRef(0)
  const [uncertain, setUncertain] = useState(false), pollStarted = useRef(0)
  const [composer, setComposer] = useState(() => ({ backend, scope, draft: readComposer(backend, scope) })), [stage, setStage] = useState<PipelineDesignStage>('spec')
  const draft = composer.backend === backend && composer.scope === scope ? composer.draft : readComposer(backend, scope)
  const { message, target } = draft
  function setMessage(value: string | ((current: string) => string)) {
    setComposer(previous => {
      const current = previous.backend === backend && previous.scope === scope ? previous.draft : readComposer(backend, scope)
      const next = { ...current, message: typeof value === 'function' ? value(current.message) : value }
      rememberComposer(backend, scope, next); return { backend, scope, draft: next }
    })
  }
  function setTarget(value: ComposerDraft['target']) {
    setComposer(previous => {
      const current = previous.backend === backend && previous.scope === scope ? previous.draft : readComposer(backend, scope), next = { ...current, target: value }
      rememberComposer(backend, scope, next); return { backend, scope, draft: next }
    })
  }
  const [mobilePane, setMobilePane] = useState<'conversation' | 'documents'>('conversation'), [editing, setEditing] = useState(false)
  const [selections, setSelections] = useState<Partial<Record<PipelineDesignStage, SDDModelSelection>>>({}), [modelsReady, setModelsReady] = useState(true)
  const consumedRequests = useRef(new Set<string>()), latestPipeline = useRef(pipeline), log = useRef<HTMLDivElement>(null)
  latestPipeline.current = pipeline
  const validScope = (ticket: number) => ticket === epoch.current && scopeRef.current === scope
  useLayoutEffect(() => {
    scopeRef.current = scope; epoch.current++; readNumber.current++; confirmationNumber.current++; designRef.current = undefined; pendingRef.current = undefined; receipt.current = undefined; preparationIntent.current = undefined; pollStarted.current = 0
    setConfirmedPipeline(undefined)
    setDesign(undefined); setState('loading'); setError(''); setNotice(''); setPending(undefined); setUncertain(false); setComposer({ backend, scope, draft: readComposer(backend, scope) }); setStage('spec'); setEditing(false); setMobilePane('conversation'); setSelections({}); setModelsReady(true)
    return () => { epoch.current++; readNumber.current++ }
  }, [backend, scope])
  useEffect(() => {
    if (!requestedStage) return
    if (editing && requestedStage !== stage) { setNotice(t('Salve ou cancele a edição antes de trocar o documento.')); return }
    setStage(requestedStage); setMobilePane('documents')
  }, [scope, requestedStage, viewRequestId])

  function reconcileAdmission(value: PipelineDesign) {
    const expected = receipt.current
    if (!expected || expected.admitted) return
    const attempt = value.attempts.find(item => item.requestId === expected.requestId)
    if (!attempt || expected.message && !value.messages.some(item => item.role === 'user' && item.attemptId === attempt.id && item.content === expected.message)) return
    expected.admitted = true
    if (expected.clearComposer) setMessage(current => current === expected.message ? '' : current)
  }
  function accept(value: PipelineDesign, ticket: number) {
    if (!validScope(ticket)) return false
    if (value.pipelineId !== latestPipeline.current.id || value.workspaceId !== latestPipeline.current.workspaceId || value.currentPipelineRevision < latestPipeline.current.revision || designRef.current && value.revision < designRef.current.revision) return false
    const restoredStage = !designRef.current && !requestedStage ? savedManualEditorStage(backend, value) : undefined
    if (restoredStage) { setStage(restoredStage); setMobilePane('documents') }
    designRef.current = value; setDesign(value); setState('ready'); reconcileAdmission(value); setUncertain(false)
    const failed = value.attempts[value.attempts.length - 1]
    if (value.state === 'paused' && failed?.status === 'failed' && failed.errorCode) setError(errorMessage({ cause: { code: failed.errorCode } }))
    return true
  }
  async function confirmPublishedPipeline(value: PipelineDesign, ticket: number, snapshotNumber: number) {
    if (value.state !== 'approved' && !value.needsDerivation && value.currentPipelineRevision <= latestPipeline.current.revision) return
    const confirmation = ++confirmationNumber.current
    setConfirmedPipeline(undefined)
    try {
      const current = await backend.getPipeline(value.pipelineId)
      if (!validScope(ticket) || snapshotNumber !== readNumber.current || confirmation !== confirmationNumber.current) return
      if (current.id !== value.pipelineId || current.workspaceId !== value.workspaceId || current.revision < Math.max(value.currentPipelineRevision, latestPipeline.current.revision)) throw new Error(t('Pipeline incompatível com os documentos publicados'))
      setConfirmedPipeline(current); setError('')
      if (current.revision !== latestPipeline.current.revision || current.currentStage !== latestPipeline.current.currentStage) onPipelineChange(current)
      if (value.state === 'approved') setNotice(t('Documentos aprovados. Code pode começar.'))
    } catch {
      if (validScope(ticket) && snapshotNumber === readNumber.current && confirmation === confirmationNumber.current) setError(t('Não foi possível confirmar a pipeline publicada. Atualize o trabalho antes de continuar.'))
    }
  }
  async function refresh(showError = true) {
    const ticket = epoch.current, number = ++readNumber.current
    try {
      const value = await backend.openPipelineDesign(pipeline.id)
      if (number !== readNumber.current || !validScope(ticket)) return undefined
      if (!accept(value, ticket)) { if (showError) setError(t('O estado retornado não corresponde à versão atual deste trabalho. Atualize novamente.')); return undefined }
      await confirmPublishedPipeline(value, ticket, number)
      return value
    } catch (failure) {
      if (validScope(ticket) && number === readNumber.current && showError) { setError(errorMessage(failure)); if (!designRef.current) setState('error') }
      return undefined
    }
  }
  useEffect(() => { void refresh(); return () => { readNumber.current++ } }, [backend, scope, pipeline.revision])
  const needsPolling = running(design) || pending === 'prepare'
  useEffect(() => {
    if (!needsPolling) { pollStarted.current = 0; return }
    if (!pollStarted.current) pollStarted.current = Date.now()
    let active = true, timer: ReturnType<typeof setTimeout>
    const ticket = epoch.current
    const poll = async () => {
      if (!active || !validScope(ticket)) return
      await refresh(false)
      // Preparing has no time limit, so following it never stops; after the first minutes it checks less often.
      if (active && validScope(ticket)) timer = setTimeout(() => void poll(), Date.now() - pollStarted.current >= 180000 ? 5000 : 1000)
    }
    timer = setTimeout(() => void poll(), 1000)
    return () => { active = false; clearTimeout(timer) }
  }, [backend, scope, needsPolling])
  useEffect(() => {
    if (!log.current) return
    const reduced = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
    if (typeof log.current.scrollTo === 'function') log.current.scrollTo({ top: log.current.scrollHeight, behavior: reduced ? 'instant' : 'smooth' })
  }, [design?.messages.length])

  function reference(value: PipelineDesign, requestId: string = crypto.randomUUID()): PipelineDesignRef { return { pipelineId: value.pipelineId, requestId, pipelineRevision: value.currentPipelineRevision, designRevision: value.revision } }
  const needsPipelineConfirmation = !!design && (design.state === 'approved' || design.needsDerivation || design.currentPipelineRevision > pipeline.revision)
  const pipelineConfirmed = !needsPipelineConfirmation || !!confirmedPipeline && confirmedPipeline.id === pipeline.id && confirmedPipeline.workspaceId === pipeline.workspaceId && confirmedPipeline.revision >= Math.max(design?.currentPipelineRevision ?? 0, pipeline.revision)
  const fenced = !!pending || running(design) || uncertain || state !== 'ready' || !pipelineConfirmed
  // The reads that confirm a published pipeline can overtake one another while a preparation finishes, leaving an
  // older revision confirmed and every action fenced. Once nothing runs, confirm the latest revision again.
  useEffect(() => {
    if (!design || pipelineConfirmed || !needsPipelineConfirmation || running(design) || pending || state !== 'ready') return
    let live = true
    const wanted = Math.max(design.currentPipelineRevision, latestPipeline.current.revision)
    backend.getPipeline(design.pipelineId).then(current => {
      if (!live || current.id !== design.pipelineId || current.workspaceId !== design.workspaceId || current.revision < wanted) return
      setConfirmedPipeline(current)
      if (current.revision !== latestPipeline.current.revision || current.currentStage !== latestPipeline.current.currentStage) onPipelineChange(current)
    }, () => undefined)
    return () => { live = false }
  }, [backend, design?.pipelineId, design?.revision, design?.currentPipelineRevision, design?.state, confirmedPipeline?.revision, pipeline.revision, pending, state, pipelineConfirmed])
  const mutable = !fenced && !design?.needsDerivation
  const manualDraftStage = design ? savedManualEditorStage(backend, design) : undefined
  const canGenerate = mutable && !editing && !manualDraftStage && modelsReady
  const canSend = canGenerate && !(target === 'plan' && design?.documents.spec.stale)
  function setCommand(command?: Command) { pendingRef.current = command; setPending(command) }

  async function prepare(text: string, destination: PipelineDesignStage | 'all', requestId?: string, clearComposer = true) {
    const current = designRef.current
    if (!current || pendingRef.current || running(current) || current.needsDerivation || editing || savedManualEditorStage(backend, current) || !modelsReady) return
    if (destination === 'plan' && current.documents.spec.stale) { setError(staleSpecMessage); return }
    const ticket = epoch.current
    setCommand('prepare'); setError(''); setNotice(''); setUncertain(false); pollStarted.current = Date.now()
    const key = JSON.stringify([current.pipelineId, current.currentPipelineRevision, current.revision, text, destination, selections])
    const input: PreparePipelineDesignInput = preparationIntent.current?.key === key ? preparationIntent.current.input : { ref: reference(current, requestId), message: text, target: destination, ...(Object.keys(selections).length ? { selections } : {}) }
    preparationIntent.current = { key, input }
    receipt.current = { requestId: input.ref.requestId, message: text, clearComposer, admitted: false }
    try {
      const resultPromise = backend.preparePipelineDesign(input)
      void refresh(false)
      const result = await resultPromise
      if (!validScope(ticket)) return
      if (!accept(result, ticket)) { const readback = await refresh(); if (!readback) setUncertain(true) }
      if (validScope(ticket) && !running(designRef.current)) setNotice(designRef.current?.state === 'paused' ? t('A preparação foi interrompida. Os documentos anteriores foram preservados.') : t('Documentos atualizados para revisão.'))
    } catch (failure) {
      if (!validScope(ticket)) return
      setError(errorMessage(failure))
      const readback = await refresh(false)
      if (!validScope(ticket)) return
      if (!readback) setUncertain(true)
    } finally { if (validScope(ticket)) setCommand(undefined) }
  }
  useEffect(() => {
    if (!autoPrepareRequest || !design || state !== 'ready' || consumedRequests.current.has(autoPrepareRequest) || fenced || design.needsDerivation) return
    consumedRequests.current.add(autoPrepareRequest)
    onAutoPrepareConsumed?.(autoPrepareRequest)
    void prepare(initialPreparation, 'all', autoPrepareRequest, false)
  }, [autoPrepareRequest, design?.pipelineId, state, fenced])

  async function mutate(command: Command, action: (value: PipelineDesign) => Promise<PipelineDesign>, success: string) {
    const current = designRef.current
    if (!current || pendingRef.current || running(current) || uncertain || current.needsDerivation) return false
    const ticket = epoch.current
    setCommand(command); setError(''); setNotice('')
    try {
      const result = await action(current)
      if (!accept(result, ticket)) return false
      setNotice(success); return true
    } catch (failure) { if (validScope(ticket)) { setError(errorMessage(failure)); const readback = await refresh(false); if (!readback) setUncertain(true) }; return false }
    finally { if (validScope(ticket)) setCommand(undefined) }
  }
  async function cancel() {
    const current = designRef.current
    if (!current?.activeAttemptId || current.state !== 'running') return
    const ticket = epoch.current; setCommand('cancel'); setError(''); setNotice('')
    try { const result = await backend.cancelPipelineDesign({ pipelineId: current.pipelineId, attemptId: current.activeAttemptId }); accept(result, ticket) }
    catch (failure) { if (validScope(ticket)) { setError(errorMessage(failure)); const readback = await refresh(false); if (!readback) setUncertain(true) } }
    finally { if (validScope(ticket)) setCommand(undefined) }
  }
  async function approve() {
    const current = designRef.current
    if (!current || !mutable || editing || savedManualEditorStage(backend, current) || designStageOrder.some(item => !current.documents[item]?.content.trim() || current.documents[item].stale || !current.documents[item].contentDigest)) return
    const ticket = epoch.current; setCommand('approve'); setError(''); setNotice('')
    try {
      const result = await backend.approvePipelineDesign({ ref: reference(current), digests: { discovery: current.documents.discovery.contentDigest, spec: current.documents.spec.contentDigest, plan: current.documents.plan.contentDigest } })
      if (!validScope(ticket)) return
      if (result.id !== pipeline.id || result.workspaceId !== pipeline.workspaceId) throw new Error(t('A aprovação retornou para outro trabalho. Atualize o estado.'))
      const readback = await refresh()
      if (validScope(ticket) && !readback) setUncertain(true)
    } catch (failure) { if (validScope(ticket)) { setError(errorMessage(failure)); const readback = await refresh(false); if (validScope(ticket) && !readback) setUncertain(true) } }
    finally { if (validScope(ticket)) setCommand(undefined) }
  }
  async function derive() {
    const current = designRef.current
    if (!current?.needsDerivation || fenced || !current.documents.discovery.content.trim()) return
    const ticket = epoch.current; setCommand('derive'); setError(''); setNotice('')
    try {
      const created = await backend.deriveAuthoringPipeline({ parentPipelineId: current.pipelineId, requestId: crypto.randomUUID(), expectedRevision: confirmedPipeline!.revision, discovery: current.documents.discovery.content })
      if (!validScope(ticket)) return
      if (created.workspaceId !== pipeline.workspaceId || created.derivedFromPipelineId !== pipeline.id) throw new Error(t('A continuação retornou para outro trabalho. Atualize a lista.'))
      onPipelineChange(created)
    } catch (failure) { if (validScope(ticket)) setError(errorMessage(failure)) }
    finally { if (validScope(ticket)) setCommand(undefined) }
  }
  function send(event: FormEvent) { event.preventDefault(); if (message.trim() && canSend) void prepare(message, target) }
  const stale = designStageOrder.filter(item => design?.documents[item]?.stale), incomplete = designStageOrder.some(item => !design?.documents[item]?.content.trim()), latest = design && designStageOrder.map(item => design.documents[item]).filter(item => item.version).sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt))[0]
  const conversation = design?.messages.filter((item, index) => !(index === 0 && item.role === 'user' && item.target === 'discovery' && !item.attemptId && item.content === design.versions.discovery[0]?.content)) ?? []
  const lastAttempt = design?.attempts[design.attempts.length - 1]
  const failedMessage = design?.state === 'paused' && lastAttempt?.status === 'failed' ? design.messages.find(item => item.role === 'user' && item.attemptId === lastAttempt.id) : undefined
  const phase = design?.phase && designStageOrder.includes(design.phase as PipelineDesignStage) ? designLabels[design.phase as PipelineDesignStage] : t('SPEC e Plan')
  return <section className="pipeline-design-studio" aria-labelledby="design-studio-title" data-pane={mobilePane}>
    <header className="design-studio-header"><div><h3 id="design-studio-title">{t('Preparar trabalho')}</h3><p className="muted">{design?.needsDerivation ? t('Documentos aprovados para esta execução.') : t('Converse com a IA ou ajuste os documentos.')}</p></div><button type="button" className="touch-target secondary-button" onClick={() => { pollStarted.current = Date.now(); void refresh() }} disabled={state === 'loading'}><RefreshCw aria-hidden="true" />{t('Atualizar trabalho')}</button></header>
    {state === 'loading' && <div className="design-loading" role="status"><span>{t('Carregando conversa e documentos…')}</span><div /><div /></div>}
    {state === 'error' && <div className="inline-error"><p>{t('Não foi possível abrir os documentos deste trabalho.')}</p><button type="button" className="touch-target secondary-button" onClick={() => void refresh()}>{t('Tentar abrir novamente')}</button></div>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {uncertain && <p className="design-stale" role="status">{t('O resultado ainda não foi confirmado. Atualize o trabalho antes de enviar outro pedido.')}</p>}
    {design && <><PipelineDesignModel key={scope} backend={backend} design={design} stageExecutors={stageExecutors} disabled={fenced || editing || design.needsDerivation} onChange={(value, ready) => { setSelections(value); setModelsReady(ready) }} onSettings={onSettings} />
      {design.needsDerivation && <div className="design-continuation"><p>{t('Crie uma continuação para ajustar os documentos e preservar esta execução.')}</p><button type="button" className="touch-target secondary-button" onClick={() => void derive()} disabled={fenced}>{t('Criar continuação para editar')}<ArrowRight aria-hidden="true" /></button></div>}
      <nav className="design-mobile-tabs" aria-label={t('Área de preparação')}><button type="button" className="touch-target" aria-pressed={mobilePane === 'conversation'} onClick={() => setMobilePane('conversation')}><MessageSquare aria-hidden="true" />{t('Conversa')}</button><button type="button" className="touch-target" aria-pressed={mobilePane === 'documents'} onClick={() => setMobilePane('documents')}><FileText aria-hidden="true" />{t('Documentos')}{stale.length > 0 && <span className="design-count">{stale.length}</span>}</button></nav>
      <div className="design-workspace"><section className="design-conversation" aria-label={t('Conversa de preparação')}><div className="design-conversation-log" ref={log}>
        <div className="design-start-message"><span className="design-message-author">{t('Discovery salvo')}</span><p>{design.documents.discovery.content}</p></div>
        {conversation.map(item => <div key={item.id} className={`design-message design-message-${item.role}`}><div className="design-message-heading"><strong>{item.role === 'user' ? t('Você') : item.role === 'assistant' ? t('IA') : t('Trabalho')}</strong>{item.role === 'user' && <span className="muted">{item.target === 'all' ? t('Todos os documentos') : designLabels[item.target as PipelineDesignStage] || item.target}</span>}</div>{item.role === 'assistant' ? <DesignMarkdown content={item.content} /> : <p>{item.content}</p>}</div>)}
        {conversation.length === 0 && <p className="muted design-conversation-hint">{t('A IA prepara SPEC e Plan com base no Discovery. Depois, peça ajustes por aqui.')}</p>}
      </div>
      <div className="design-progress" aria-live="polite">{design.state === 'cancellation_pending' ? <p>{t('Aguardando a confirmação do cancelamento. Os documentos estão protegidos.')}</p> : design.state === 'running' || pending === 'prepare' ? <><p><RefreshCw className="design-preparing-icon" aria-hidden="true" />{design.state === 'running' ? t('Preparando {phase}…', { phase }) : t('Iniciando preparação…')}</p>{design.activeAttemptId && <button type="button" className="touch-target secondary-button" onClick={() => void cancel()} disabled={pending === 'cancel'}><Square aria-hidden="true" />{t('Cancelar preparação')}</button>}</> : latest ? <span className="muted">{t('Última atualização · {label} v{version} · {time}', { label: designLabels[latest.stage], version: latest.version, time: new Date(latest.updatedAt).toLocaleTimeString(localeTag(), { hour: '2-digit', minute: '2-digit' }) })}</span> : null}</div>
      {failedMessage && <div className="design-retry"><button type="button" className="touch-target secondary-button" onClick={() => void prepare(failedMessage.content, designStageOrder.includes(failedMessage.target as PipelineDesignStage) ? failedMessage.target as PipelineDesignStage : 'all', undefined, false)} disabled={!canGenerate || failedMessage.target === 'plan' && design.documents.spec.stale}>{t('Tentar preparação novamente')}</button></div>}
      {!design.needsDerivation && <form className="design-composer" onSubmit={send}><label className="field">{t('Pedido para a IA')}<textarea value={message} onChange={event => setMessage(event.target.value)} placeholder={t('O que você quer ajustar nos documentos?')} rows={3} maxLength={20000} /></label>{target === 'plan' && design.documents.spec.stale && <p className="design-stale" role="status">{staleSpecMessage}</p>}<div className="design-composer-actions"><IonPicker id="design-target" label={t('Alterar')} value={target} onChange={value => setTarget(value as PipelineDesignStage | 'all')} disabled={fenced || editing} options={[{ value: 'all', label: t('Todos') }, ...designStageOrder.map(item => ({ value: item, label: designLabels[item] }))]} /><button type="submit" className="touch-target primary-button" disabled={!message.trim() || !canSend}><Send aria-hidden="true" />{t('Enviar pedido')}</button></div></form>}
      </section><PipelineDesignDocument key={scope} backend={backend} design={design} stage={stage} disabled={fenced} readOnly={design.needsDerivation} onStageChange={setStage} onEditingChange={setEditing} onSave={(item, content) => mutate('save', current => backend.editPipelineDesignDocument({ ref: reference(current), stage: item, content }), t('{label} salvo e versionado.', { label: designLabels[item] }))} onRestore={(item, version) => mutate('restore', current => backend.restorePipelineDesignDocument({ ref: reference(current), stage: item, version }), t('Versão {version} restaurada em uma nova versão de {label}.', { version, label: designLabels[item] }))} />
      </div>
      {!design.needsDerivation && <footer className="design-studio-footer"><div>{stale.length > 0 ? <p>{stale.length > 1 ? t('{list} precisam refletir a última mudança.', { list: stale.map(item => designLabels[item]).join(t(' e ')) }) : t('{list} precisa refletir a última mudança.', { list: stale.map(item => designLabels[item]).join(t(' e ')) })}</p> : <p className="muted">{incomplete ? t('Prepare ou escreva os documentos para continuar.') : t('Revise os três documentos antes de continuar.')}</p>}{manualDraftStage && !editing && <p className="muted">{t('Há um rascunho manual de {label}. Abra esse documento para salvar ou cancelar a edição.', { label: designLabels[manualDraftStage] })}</p>}{notice && <p className="form-success" role="status">{notice}</p>}</div><div className="design-footer-actions">{incomplete && <button type="button" className="touch-target primary-button" onClick={() => void prepare(initialPreparation, 'all', undefined, false)} disabled={!canGenerate || !design.documents.discovery.content.trim()}>{t('Preparar SPEC e Plan')}</button>}{!incomplete && stale.length > 0 && <button type="button" className="touch-target secondary-button" onClick={() => void prepare('Atualize os documentos para refletir as últimas edições salvas.', stale.includes('spec') ? 'spec' : 'plan', undefined, false)} disabled={!canGenerate}>{t('Atualizar documentos')}</button>}<button type="button" className={`touch-target ${incomplete ? 'secondary-button' : 'primary-button'}`} onClick={() => void approve()} disabled={!mutable || editing || !!manualDraftStage || incomplete || stale.length > 0 || designStageOrder.some(item => !design.documents[item].contentDigest)}><ArrowRight aria-hidden="true" />{t('Aprovar documentos e continuar para Code')}</button></div></footer>}
    </>}
  </section>
}
