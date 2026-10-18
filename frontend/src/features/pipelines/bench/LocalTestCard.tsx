import { useCallback, useEffect, useMemo, useState } from 'react'
import { ExternalLink, LoaderCircle, MessageSquare, Play, RotateCcw, SquareTerminal, TriangleAlert, Undo2 } from 'lucide-react'
import { IonPicker } from '../../../components/IonPicker'
import { errorCode, errorMessage, errorPaths, type Backend, type BackendOption, type Pipeline, type PipelineLocalTest } from '../../../lib/backend'
import { openExternal } from '../../../lib/desktop'
import { useT } from '../../../i18n'
import { StopRun } from '../CodeLiveRun'

export type LocalTestCardProps = {
  backend: Backend
  run: Pipeline
  backends: BackendOption[]
  defaultBackendId: string
  /** The executor the project chose for the Code phase, preferred when it can run the test. */
  configuredBackendId?: string
  /** The Code waits on its review: only then can a test start. An applied test stays in view either way. */
  canStart?: boolean
  /** The project is on Full access: only then does Claude Code run the test (over MCP nothing asks first). */
  fullAccess?: boolean
  /** The person kept their changes: a decision that was waiting on the undo is dropped. */
  onKeep?: () => void
  /** Starts the tester (createPipelineSession with the tester role). Rejects with the backend's error. */
  onStart: (backendId: string) => Promise<void>
  onOpenSession?: (sessionId: string, draft?: string) => Promise<void>
  /** Opens the Terminal panel, on a service's tab when one is given. */
  onOpenTerminal?: (terminalId?: string) => void
  onOpenPipeline?: (pipelineId: string) => void
  /** Bumped when a Code decision stopped on the test's conflict: the card reads the test again to show it. */
  refreshKey?: number
  /** A Code decision that waits on the test's undo: sent again once the person discards their changes. */
  onUndone?: () => void
}

const refreshEveryMs = 4000

/** Try the Code before approving it: the AI applies it to the project's own folder and brings the project up. */
export function LocalTestCard({ backend, run, backends, defaultBackendId, configuredBackendId, canStart = true, fullAccess = false, onKeep, onStart, onOpenSession, onOpenTerminal, onOpenPipeline, refreshKey = 0, onUndone }: LocalTestCardProps) {
  const t = useT()
  const [view, setView] = useState<PipelineLocalTest>()
  const [choice, setChoice] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<{ message: string; paths: string[]; dirty: boolean }>()
  const [conflictShown, setConflictShown] = useState(false)
  // The tester needs Harflex's tools and a conversation it can continue: API profiles and Claude Code.
  const eligible = useMemo(() => backends.filter(item => item.available && (item.kind === 'api' || item.id === 'claude' && fullAccess)), [backends, fullAccess])
  const claudeHeldBack = !fullAccess && backends.some(item => item.available && item.id === 'claude')
  const preferred = [choice, configuredBackendId, defaultBackendId].find(id => id && eligible.some(item => item.id === id))
  const backendId = preferred || eligible[0]?.id || ''

  const load = useCallback(async () => {
    try { setView(await backend.getPipelineLocalTest(run.id)) } catch (failure) { setError({ message: errorMessage(failure), paths: [], dirty: false }) }
  }, [backend, run.id])
  useEffect(() => { void load() }, [load, refreshKey, run.revision])
  useEffect(() => { if (refreshKey > 0) setConflictShown(true) }, [refreshKey])
  useEffect(() => {
    if (view?.status !== 'applied') return
    const timer = window.setInterval(() => void load(), refreshEveryMs)
    return () => window.clearInterval(timer)
  }, [view?.status, load])

  async function start() {
    if (!backendId || pending) return
    setPending(true); setError(undefined)
    try { await onStart(backendId); await load() }
    catch (failure) {
      const dirty = errorCode(failure) === 'local_test_source_dirty'
      setError({ message: dirty ? t('Sua pasta tem mudanças não salvas') : errorMessage(failure), paths: errorPaths(failure), dirty })
      await load()
    } finally { setPending(false) }
  }

  async function undo(discard: boolean) {
    setPending(true); setError(undefined)
    try {
      const next = await backend.undoPipelineLocalTest(run.id, discard)
      setView(next)
      setConflictShown(!!next.conflict)
      if (next.status === 'none') onUndone?.()
    } catch (failure) { setError({ message: errorMessage(failure), paths: [], dirty: false }) }
    finally { setPending(false) }
  }

  const version = run.artifacts.code?.version ?? 0
  const applied = view?.status === 'applied'
  const preparing = applied && (view.testerStatus === 'running' || view.testerStatus === 'awaiting_approval' || view.testerStatus === 'cancellation_pending')
  const conflict = conflictShown ? view?.conflict : undefined

  if (view && !applied && !canStart) return null
  return <section className="bench-slab local-test" aria-label={t('Teste local')}>
    {!view && <p className="muted" role="status">{t('Lendo o teste local…')}</p>}
    {view && !applied && view.heldBy && <div className="local-test-row">
      <span>{t('Outro teste local está aplicado neste projeto: {pipeline}', { pipeline: view.heldBy })}</span>
      {view.heldById && onOpenPipeline && <button type="button" className="touch-target secondary-button" onClick={() => onOpenPipeline(view.heldById!)}>{t('Abrir esse pipeline')}</button>}
    </div>}
    {view && !applied && !view.heldBy && <div className="local-test-start">
      <div className="local-test-text"><strong>{t('Testar antes de aprovar')}</strong><span className="muted">{t('A IA aplica esta versão na sua pasta e sobe o projeto para você testar antes de aprovar.')}</span></div>
      {eligible.length > 1 && <IonPicker id={`local-test-backend-${run.id}`} label={t('Executor do teste')} value={backendId} onChange={setChoice} options={eligible.map(item => ({ value: item.id, label: item.name }))} disabled={pending} compact />}
      {claudeHeldBack && <span className="muted">{t('O Claude Code só prepara o teste com Acesso total: por ele nada pede aprovação antes de rodar.')}</span>}
      {eligible.length === 0
        ? <p className="muted">{t('O teste local precisa de um perfil de API ou do Claude Code. Configure um em Configurações.')}</p>
        : <button type="button" className="touch-target secondary-button" onClick={() => void start()} disabled={pending || !backendId}>{pending ? <LoaderCircle aria-hidden="true" className="spin" /> : <Play aria-hidden="true" />}{t('Testar localmente')}</button>}
    </div>}
    {applied && <div className="local-test-applied">
      <div className="local-test-text">
        <strong>{t('Aplicado na sua pasta para teste · versão {version}', { version: view.artifactVersion })}</strong>
        {view.artifactVersion !== version && <span className="muted">{t('Em teste: versão {old}. Aprovar desfaz o teste antes.', { old: view.artifactVersion })}</span>}
        {preparing && <span className="muted" role="status"><LoaderCircle aria-hidden="true" className="spin" /> {t('Preparando o projeto…')}</span>}
        {preparing && <span><StopRun backend={backend} sessionId={view.sessionId} label={t('Parar a preparação')} /></span>}
      </div>
      {view.servicesLost && <div className="local-test-row" role="status">
        <span>{t('Os serviços pararam quando o app fechou. As mudanças continuam aplicadas.')}</span>
        {onOpenSession && <button type="button" className="touch-target secondary-button" onClick={() => void onOpenSession(view.sessionId, t('Suba os serviços de novo'))}><RotateCcw aria-hidden="true" />{t('Subir de novo')}</button>}
      </div>}
      {view.report && view.report.urls.length > 0 && <ul className="local-test-urls" aria-label={t('Endereços para testar')}>
        {view.report.urls.map(item => <li key={item.url}><ExternalLink aria-hidden="true" /><a href={item.url} onClick={event => { event.preventDefault(); void openExternal(item.url) }} title={item.url}>{item.label || item.url}</a><span className="muted mono">{item.url}</span></li>)}
      </ul>}
      {view.report?.stop && <p className="muted">{view.report.stop}</p>}
      {view.report?.notes && <p className="muted">{view.report.notes}</p>}
      {view.services.length > 0 && !view.servicesLost && <ul className="local-test-services" aria-label={t('Serviços')}>
        {view.services.map(service => <li key={service.terminalId}>
          <span className={`local-test-dot${service.running ? ' is-running' : ''}`} aria-hidden="true" /><span className="mono">{service.name}</span>
          <span className="muted">{service.running ? t('rodando') : t('encerrado')}</span>
          {onOpenTerminal && <button type="button" className="touch-target text-button" aria-label={t('Abrir {name} no Terminal', { name: service.name })} onClick={() => onOpenTerminal(service.terminalId)}><SquareTerminal aria-hidden="true" />{t('Terminal')}</button>}
        </li>)}
      </ul>}
      {view.backupMissing && <p className="muted" role="status">{t('O backup deste teste não foi encontrado: o desfazer restaura os arquivos a partir do commit em que a pasta estava.')}</p>}
      {conflict ? <div className="local-test-conflict" role="alert">
        <strong><TriangleAlert aria-hidden="true" />{t('Você mexeu em arquivos durante o teste')}</strong>
        {conflict.edited.length > 0 && <><span className="muted">{t('Alterados')}</span><ul className="mono">{conflict.edited.map(path => <li key={path}>{path}</li>)}</ul></>}
        {conflict.created.length > 0 && <><span className="muted">{t('Criados')}</span><ul className="mono">{conflict.created.map(path => <li key={path}>{path}</li>)}</ul></>}
        <div className="pipeline-actions">
          <button type="button" className="touch-target secondary-button" onClick={() => { setConflictShown(false); onKeep?.() }} disabled={pending}>{t('Manter minhas mudanças')}</button>
          <button type="button" className="touch-target secondary-button danger" onClick={() => void undo(true)} disabled={pending}>{t('Descartar e desfazer')}</button>
        </div>
      </div> : <div className="pipeline-actions">
        {onOpenSession && view.sessionId && <button type="button" className="touch-target secondary-button" onClick={() => void onOpenSession(view.sessionId)}><MessageSquare aria-hidden="true" />{t('Abrir conversa')}</button>}
        <button type="button" className="touch-target secondary-button" onClick={() => void undo(false)} disabled={pending}><Undo2 aria-hidden="true" />{t('Desfazer teste')}</button>
      </div>}
    </div>}
    {error && <div className="local-test-error" role="alert">
      <span className="form-error">{error.message}</span>
      {error.paths.length > 0 && <ul className="mono">{error.paths.map(path => <li key={path}>{path}</li>)}</ul>}
      {error.dirty && <div className="pipeline-actions">
        {onOpenTerminal && <button type="button" className="touch-target secondary-button" onClick={() => onOpenTerminal()}><SquareTerminal aria-hidden="true" />{t('Abrir no Terminal')}</button>}
        <button type="button" className="touch-target secondary-button" onClick={() => void start()} disabled={pending}>{t('Tentar de novo')}</button>
      </div>}
    </div>}
  </section>
}
