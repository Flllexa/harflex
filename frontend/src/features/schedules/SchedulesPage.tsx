import { useCallback, useEffect, useMemo, useState, type FormEvent } from 'react'
import { CreateToggle, useCreateForm } from '../../components/CreateForm'
import { CalendarClock, CirclePause, FolderOpen, Play, RefreshCw } from 'lucide-react'
import { IonPicker } from '../../components/IonPicker'
import { errorMessage, type Backend, type BackendOption, type Schedule, type ScheduleInput, type ScheduleJob, type Workflow } from '../../lib/backend'
import { localeTag, t, useT } from '../../i18n'

type Props = { backend: Backend; backends: BackendOption[]; workspaceId?: string; onProjects: () => void; onSettings: () => void; onWorkflows: () => void; onOpenSession: (sessionId: string) => Promise<void> }
type FormState = Omit<ScheduleInput, 'workspaceId' | 'id' | 'revision'>
const localTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
const supportedTimezones = (Intl as typeof Intl & { supportedValuesOf?: (key: 'timeZone') => string[] }).supportedValuesOf?.('timeZone') ?? []
const timezoneOptions = Array.from(new Set(['UTC', localTimezone, 'America/Sao_Paulo', 'America/New_York', 'Europe/London', 'Europe/Berlin', 'Asia/Tokyo', ...supportedTimezones])).sort()
const initialForm = (): FormState => ({ name: '', targetKind: 'prompt', workflowId: '', backendId: '', prompt: '', frequency: 'daily', timezone: localTimezone, localDate: '', localTime: '09:00', missedPolicy: 'skip', enabled: true, allowCli: false })
function jobLabels(): Record<ScheduleJob['status'], string> {
  return { queued: t('Na fila'), running: t('Executando'), waiting_user: t('Aguardando aprovação'), cancel_requested: t('Cancelamento solicitado'), completed: t('Concluído'), failed: t('Falhou'), cancelled: t('Cancelado'), skipped: t('Ignorado'), interrupted: t('Interrompido') }
}
function failureLabels(): Record<string, string> {
  return { missed_execution: t('Horário perdido; política configurada para ignorar.'), previous_job_active: t('Outro job deste agendamento ainda aguardava conclusão.'), app_restart: t('O aplicativo foi fechado durante a execução. Confira a sessão antes de repetir.'), workflow_paused: t('O workflow pausou; confira a sessão vinculada.'), execution_failed: t('A execução falhou. Confira a sessão vinculada.'), cancel_failed: t('Não foi possível confirmar o cancelamento. O job pode continuar; verifique o histórico.'), cancel_unconfirmed: t('O resultado do cancelamento não foi confirmado.') }
}

function formatInstant(value: string, timezone: string) {
  return new Intl.DateTimeFormat(localeTag(), { timeZone: timezone, dateStyle: 'short', timeStyle: 'short' }).format(new Date(value))
}

export function SchedulesPage({ backend, backends, workspaceId, onProjects, onSettings, onWorkflows, onOpenSession }: Props) {
  const t = useT()
  const [items, setItems] = useState<Schedule[]>([])
  const [jobs, setJobs] = useState<ScheduleJob[]>([])
  const [workflows, setWorkflows] = useState<Workflow[]>([])
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [form, setForm] = useState<FormState>(initialForm)
  const [editing, setEditing] = useState<Schedule>()
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string>()
  const [notice, setNotice] = useState<string>()
  const createForm = useCreateForm(state === 'ready', items.length)
  const selectedBackend = form.backendId || backends.find(item => item.available)?.id || ''
  const backendOption = backends.find(item => item.id === selectedBackend)
  const byId = useMemo(() => new Map(items.map(item => [item.id, item])), [items])

  const refresh = useCallback(async (loading = false) => {
    if (!workspaceId) return
    if (loading) setState('loading')
    try {
      const [schedules, history, definitions] = await Promise.all([backend.listSchedules(workspaceId), backend.listScheduleJobs(workspaceId), backend.listWorkflows(workspaceId)])
      setItems(schedules); setJobs(history); setWorkflows(definitions); setState('ready')
    } catch { setState('error') }
  }, [backend, workspaceId])
  useEffect(() => {
    void refresh(true)
    return backend.onScheduleChange(changedWorkspace => { if (changedWorkspace === workspaceId) void refresh() })
  }, [backend, workspaceId, refresh])

  function setField<K extends keyof FormState>(key: K, value: FormState[K]) { setForm(current => ({ ...current, [key]: value })) }
  function reset() { setEditing(undefined); setForm(initialForm()); setError(undefined) }
  function edit(item: Schedule) {
    setEditing(item)
    setForm({ name: item.name, targetKind: item.targetKind, workflowId: item.workflowId, backendId: item.backendId, prompt: item.prompt, frequency: item.frequency, timezone: item.timezone, localDate: item.localDate, localTime: item.localTime, missedPolicy: item.missedPolicy, enabled: item.enabled, allowCli: item.allowCli })
    setError(undefined); createForm.setOpen(true)
  }
  async function save(event: FormEvent) {
    event.preventDefault()
    if (!workspaceId || pending) return
    setPending(true); setError(undefined); setNotice(undefined)
    try {
      await backend.saveSchedule({ ...form, workspaceId, id: editing?.id, revision: editing?.revision, backendId: selectedBackend, name: form.name.trim(), prompt: form.targetKind === 'prompt' ? form.prompt.trim() : '', workflowId: form.targetKind === 'workflow' ? form.workflowId : '', localDate: form.frequency === 'once' ? form.localDate : '', allowCli: backendOption?.kind === 'cli' && form.allowCli })
      reset(); createForm.setOpen(false); setNotice(t('Agendamento salvo nesta máquina.')); await refresh()
    } catch (failure) { setError(errorMessage(failure)) }
    finally { setPending(false) }
  }
  async function action<T>(work: () => Promise<T>, success: string | ((result: T) => string)) {
    if (pending) return
    setPending(true); setError(undefined); setNotice(undefined)
    try { const result = await work(); setNotice(typeof success === 'string' ? success : success(result)); await refresh() }
    catch (failure) { setError(errorMessage(failure)); await refresh() }
    finally { setPending(false) }
  }
  async function openSession(sessionId: string) {
    setError(undefined)
    try { await onOpenSession(sessionId) }
    catch (failure) { setError(errorMessage(failure)) }
  }

  return <div className="schedules-page">
    <div className="destination-heading"><div><h2>{t('Agendamentos')}</h2><p className="muted">{t('Trabalho recorrente ou único, registrado no histórico local.')}</p></div>{workspaceId && <div className="pipeline-actions">{createForm.collapsible && <CreateToggle open={createForm.open} onToggle={() => { if (createForm.open) reset(); createForm.setOpen(!createForm.open) }} label={t('Novo agendamento')} />}<button type="button" className="touch-target secondary-button" onClick={() => void refresh(true)} disabled={state === 'loading'}><RefreshCw aria-hidden="true" />{t('Atualizar')}</button></div>}</div>
    <p className="schedule-runtime-note"><CalendarClock aria-hidden="true" />{t('Os agendamentos executam somente com o aplicativo aberto.')}</p>
    {!workspaceId ? <div className="catalog-empty"><FolderOpen aria-hidden="true" /><strong>{t('Abra um projeto para agendar trabalho')}</strong><span className="muted">{t('Agendamentos e jobs ficam vinculados à pasta local.')}</span><button type="button" className="touch-target primary-button" onClick={onProjects}>{t('Abrir projetos')}</button></div> : <>
      {createForm.open && <form className="schedule-editor" onSubmit={save} aria-label={t('Editor de agendamento')}>
        <div className="destination-heading"><div><h3>{editing ? t('Editar agendamento') : t('Novo agendamento')}</h3><p className="muted">{t('O fuso e o horário local determinam o próximo disparo.')}</p></div></div>
        <div className="schedule-form-grid">
          <label className="field">{t('Nome do agendamento')}<input value={form.name} onChange={event => setField('name', event.target.value)} maxLength={200} required /></label>
          <IonPicker id="schedule-target-kind" label={t('Tipo de trabalho')} value={form.targetKind}
            onChange={next => setForm(current => ({ ...current, targetKind: next as FormState['targetKind'], workflowId: '', prompt: '' }))}
            options={[{ value: 'prompt', label: t('Prompt') }, { value: 'workflow', label: t('Workflow') }]} />
          {form.targetKind === 'workflow' ? <IonPicker id="schedule-workflow" label={t('Workflow')} value={form.workflowId}
            onChange={next => setField('workflowId', next)} required searchable
            options={[{ value: '', label: t('Escolha um workflow') }, ...workflows.map(item => ({ value: item.id, label: item.name }))]} />
            : <label className="field schedule-prompt">{t('Prompt')}<textarea value={form.prompt} onChange={event => setField('prompt', event.target.value)} rows={3} maxLength={1024 * 1024} required /></label>}
          <IonPicker id="schedule-backend" label={t('Backend')} value={selectedBackend} required
            onChange={next => setForm(current => ({ ...current, backendId: next, allowCli: false }))}
            options={[{ value: '', label: t('Escolha um backend') }, ...backends.map(item => ({ value: item.id, label: item.available ? item.name : t('{name} · indisponível', { name: item.name }), disabled: !item.available }))]} />
          <IonPicker id="schedule-frequency" label={t('Frequência')} value={form.frequency}
            onChange={next => setField('frequency', next as FormState['frequency'])}
            options={[{ value: 'daily', label: t('Todos os dias') }, { value: 'once', label: t('Uma vez') }]} />
          {form.frequency === 'once' && <label className="field">{t('Data local')}<input type="date" value={form.localDate} onChange={event => setField('localDate', event.target.value)} required /></label>}
          <label className="field">{t('Horário local')}<input type="time" value={form.localTime} onChange={event => setField('localTime', event.target.value)} required /></label>
          <IonPicker id="schedule-timezone" label={t('Fuso horário')} value={form.timezone} searchable
            onChange={next => setField('timezone', next)} options={timezoneOptions.map(zone => ({ value: zone, label: zone }))} />
          <IonPicker id="schedule-missed-policy" label={t('Se perder o horário')} value={form.missedPolicy}
            onChange={next => setField('missedPolicy', next as FormState['missedPolicy'])}
            options={[{ value: 'skip', label: t('Ignorar') }, { value: 'run_once', label: t('Executar uma vez ao reabrir') }]} />
        </div>
        {form.targetKind === 'workflow' && workflows.length === 0 && <div className="schedule-inline-note"><span>{t('Nenhum workflow neste projeto.')}</span><button type="button" className="touch-target secondary-button" onClick={onWorkflows}>{t('Criar workflow')}</button></div>}
        {!backends.some(item => item.available) && <div className="schedule-inline-note"><span>{t('Nenhum backend disponível.')}</span><button type="button" className="touch-target secondary-button" onClick={onSettings}>{t('Configurar provedor')}</button></div>}
        {backendOption?.kind === 'cli' && <label className="schedule-cli-consent"><input type="checkbox" checked={form.allowCli} onChange={event => setField('allowCli', event.target.checked)} /><span>{t('Entendo que o CLI executa com a autoridade do próprio processo; a política de ferramentas do Harflex não controla suas ações internas.')}</span></label>}
        <div className="schedule-actions">{editing && <button type="button" className="touch-target secondary-button" onClick={reset}>{t('Cancelar edição')}</button>}<button type="submit" className="touch-target primary-button" disabled={pending || !form.name.trim() || !selectedBackend || (form.targetKind === 'prompt' ? !form.prompt.trim() : !form.workflowId) || (form.frequency === 'once' && !form.localDate) || (backendOption?.kind === 'cli' && !form.allowCli)}>{t('Salvar agendamento')}</button></div>
      </form>}
      {state === 'loading' && <p role="status" className="muted">{t('Carregando agendamentos…')}</p>}
      {state === 'error' && <div className="inline-error" role="alert"><p>{t('Não foi possível carregar os agendamentos.')}</p><button type="button" className="touch-target secondary-button" onClick={() => void refresh(true)}>{t('Tentar novamente')}</button></div>}
      {state === 'ready' && <>
        <section className="schedule-catalog" aria-labelledby="schedule-catalog-title"><div><h3 id="schedule-catalog-title">{t('Agendados')}</h3><p className="muted">{t('Pausar impede próximos disparos; jobs ativos podem ser cancelados no histórico.')}</p></div>{items.length === 0 ? <div className="catalog-empty"><CalendarClock aria-hidden="true" /><strong>{t('Nenhum agendamento salvo')}</strong><span className="muted">{t('Configure o primeiro trabalho acima.')}</span></div> : <ul className="schedule-list">{items.map(item => {
          const scheduledJob = jobs.find(job => job.scheduleId === item.id && job.trigger === 'scheduled')
          const endedOnce = item.frequency === 'once' && !item.enabled && scheduledJob
          return <li key={item.id}><div className="schedule-summary"><strong>{item.name}</strong><span className="muted">{item.targetKind === 'workflow' ? t('Workflow · {name}', { name: workflows.find(workflow => workflow.id === item.workflowId)?.name ?? t('definição indisponível') }) : t('Prompt')} · {item.frequency === 'daily' ? t('diário') : t('uma vez')} · {item.timezone}</span><span className="muted">{item.nextRunAt ? t('Próximo disparo: {date}', { date: formatInstant(item.nextRunAt, item.timezone) }) : endedOnce ? t('Execução única encerrada') : t('Sem próximo disparo')}</span></div><span className={`status-chip${item.enabled ? ' status-ready' : ' status-unavailable'}`}>{item.enabled ? t('Ativo') : endedOnce ? t('Encerrado') : t('Pausado')}</span><div className="schedule-item-actions"><button type="button" className="touch-target secondary-button" onClick={() => edit(item)} disabled={pending} aria-label={t('Editar {name}', { name: item.name })}>{t('Editar')}</button>{!endedOnce && <button type="button" className="touch-target secondary-button" onClick={() => void action(() => backend.setSchedulePaused(item.id, item.enabled, item.revision), item.enabled ? t('Agendamento pausado.') : t('Agendamento retomado.'))} disabled={pending} aria-label={`${item.enabled ? t('Pausar') : t('Retomar')} ${item.name}`}>{item.enabled ? <CirclePause aria-hidden="true" /> : <Play aria-hidden="true" />}{item.enabled ? t('Pausar') : t('Retomar')}</button>}<button type="button" className="touch-target primary-button" onClick={() => void action(() => backend.runScheduleNow(item.id), t('Job enfileirado para execução local.'))} disabled={pending} aria-label={t('Executar agora {name}', { name: item.name })}><Play aria-hidden="true" />{t('Executar agora')}</button></div></li>
        })}</ul>}</section>
        <section className="schedule-history" aria-labelledby="schedule-history-title">
          <h3 id="schedule-history-title">{t('Histórico de jobs (até 200 recentes)')}</h3>
          {jobs.length === 0 ? <p className="muted">{t('Nenhum job registrado.')}</p> : <ul>{jobs.map(job => <li key={job.id}>
            <div className="schedule-summary">
              <strong>{byId.get(job.scheduleId)?.name ?? t('Agendamento salvo')}</strong>
              <span className="muted">{job.trigger === 'manual' ? t('Manual') : t('Automático')} · <time dateTime={job.dueAt}>{formatInstant(job.dueAt, byId.get(job.scheduleId)?.timezone ?? localTimezone)}</time></span>
              {job.errorCode && <span className="schedule-job-error">{failureLabels()[job.errorCode] ?? t('A execução não foi concluída. Confira a sessão vinculada.')}</span>}
            </div>
            <span className={`status-chip${job.status === 'completed' ? ' status-ready' : ['failed', 'interrupted'].includes(job.status) ? ' status-unavailable' : ''}`}>{jobLabels()[job.status]}</span>
            <div className="schedule-item-actions">
              {job.sessionId && <button type="button" className="touch-target secondary-button" onClick={() => void openSession(job.sessionId)}>{t('Abrir sessão')}</button>}
              {['queued', 'running', 'waiting_user', 'cancel_requested'].includes(job.status) && <button type="button" className="touch-target secondary-button" onClick={() => void action(() => backend.cancelScheduleJob(job.id), result => result.status === 'cancel_requested' ? t('Cancelamento solicitado; aguardando confirmação.') : result.status === 'cancelled' ? t('Job cancelado.') : t('Job já {status}.', { status: jobLabels()[result.status].toLowerCase() }))} disabled={pending}>{job.status === 'cancel_requested' ? t('Tentar cancelar') : t('Cancelar job')}</button>}
            </div>
          </li>)}</ul>}
        </section>
      </>}
    </>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {notice && <p className="form-success" role="status">{notice}</p>}
  </div>
}
