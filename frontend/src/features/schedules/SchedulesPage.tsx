import { useCallback, useEffect, useMemo, useState, type FormEvent } from 'react'
import { CreateToggle, useCreateForm } from '../../components/CreateForm'
import { CalendarClock, CirclePause, FolderOpen, Play, RefreshCw } from 'lucide-react'
import { IonPicker } from '../../components/IonPicker'
import { errorMessage, type Backend, type BackendOption, type Schedule, type ScheduleInput, type ScheduleJob, type Workflow } from '../../lib/backend'

type Props = { backend: Backend; backends: BackendOption[]; workspaceId?: string; onProjects: () => void; onSettings: () => void; onWorkflows: () => void; onOpenSession: (sessionId: string) => Promise<void> }
type FormState = Omit<ScheduleInput, 'workspaceId' | 'id' | 'revision'>
const localTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
const supportedTimezones = (Intl as typeof Intl & { supportedValuesOf?: (key: 'timeZone') => string[] }).supportedValuesOf?.('timeZone') ?? []
const timezoneOptions = Array.from(new Set(['UTC', localTimezone, 'America/Sao_Paulo', 'America/New_York', 'Europe/London', 'Europe/Berlin', 'Asia/Tokyo', ...supportedTimezones])).sort()
const initialForm = (): FormState => ({ name: '', targetKind: 'prompt', workflowId: '', backendId: '', prompt: '', frequency: 'daily', timezone: localTimezone, localDate: '', localTime: '09:00', missedPolicy: 'skip', enabled: true, allowCli: false })
const jobLabels: Record<ScheduleJob['status'], string> = { queued: 'Na fila', running: 'Executando', waiting_user: 'Aguardando aprovação', cancel_requested: 'Cancelamento solicitado', completed: 'Concluído', failed: 'Falhou', cancelled: 'Cancelado', skipped: 'Ignorado', interrupted: 'Interrompido' }
const failureLabels: Record<string, string> = { missed_execution: 'Horário perdido; política configurada para ignorar.', previous_job_active: 'Outro job deste agendamento ainda aguardava conclusão.', app_restart: 'O aplicativo foi fechado durante a execução. Confira a sessão antes de repetir.', workflow_paused: 'O workflow pausou; confira a sessão vinculada.', execution_failed: 'A execução falhou. Confira a sessão vinculada.', cancel_failed: 'Não foi possível confirmar o cancelamento. O job pode continuar; verifique o histórico.', cancel_unconfirmed: 'O resultado do cancelamento não foi confirmado.' }

function formatInstant(value: string, timezone: string) {
  return new Intl.DateTimeFormat('pt-BR', { timeZone: timezone, dateStyle: 'short', timeStyle: 'short' }).format(new Date(value))
}

export function SchedulesPage({ backend, backends, workspaceId, onProjects, onSettings, onWorkflows, onOpenSession }: Props) {
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
      reset(); createForm.setOpen(false); setNotice('Agendamento salvo nesta máquina.'); await refresh()
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
    <div className="destination-heading"><div><h2>Agendamentos</h2><p className="muted">Trabalho recorrente ou único, registrado no histórico local.</p></div>{workspaceId && <div className="pipeline-actions">{createForm.collapsible && <CreateToggle open={createForm.open} onToggle={() => { if (createForm.open) reset(); createForm.setOpen(!createForm.open) }} label="Novo agendamento" />}<button type="button" className="touch-target secondary-button" onClick={() => void refresh(true)} disabled={state === 'loading'}><RefreshCw aria-hidden="true" />Atualizar</button></div>}</div>
    <p className="schedule-runtime-note"><CalendarClock aria-hidden="true" />Os agendamentos executam somente com o aplicativo aberto.</p>
    {!workspaceId ? <div className="catalog-empty"><FolderOpen aria-hidden="true" /><strong>Abra um projeto para agendar trabalho</strong><span className="muted">Agendamentos e jobs ficam vinculados à pasta local.</span><button type="button" className="touch-target primary-button" onClick={onProjects}>Abrir projetos</button></div> : <>
      {createForm.open && <form className="schedule-editor" onSubmit={save} aria-label="Editor de agendamento">
        <div className="destination-heading"><div><h3>{editing ? 'Editar agendamento' : 'Novo agendamento'}</h3><p className="muted">O fuso e o horário local determinam o próximo disparo.</p></div></div>
        <div className="schedule-form-grid">
          <label className="field">Nome do agendamento<input value={form.name} onChange={event => setField('name', event.target.value)} maxLength={200} required /></label>
          <IonPicker id="schedule-target-kind" label="Tipo de trabalho" value={form.targetKind}
            onChange={next => setForm(current => ({ ...current, targetKind: next as FormState['targetKind'], workflowId: '', prompt: '' }))}
            options={[{ value: 'prompt', label: 'Prompt' }, { value: 'workflow', label: 'Workflow' }]} />
          {form.targetKind === 'workflow' ? <IonPicker id="schedule-workflow" label="Workflow" value={form.workflowId}
            onChange={next => setField('workflowId', next)} required searchable
            options={[{ value: '', label: 'Escolha um workflow' }, ...workflows.map(item => ({ value: item.id, label: item.name }))]} />
            : <label className="field schedule-prompt">Prompt<textarea value={form.prompt} onChange={event => setField('prompt', event.target.value)} rows={3} maxLength={1024 * 1024} required /></label>}
          <IonPicker id="schedule-backend" label="Backend" value={selectedBackend} required
            onChange={next => setForm(current => ({ ...current, backendId: next, allowCli: false }))}
            options={[{ value: '', label: 'Escolha um backend' }, ...backends.map(item => ({ value: item.id, label: `${item.name}${item.available ? '' : ' · indisponível'}`, disabled: !item.available }))]} />
          <IonPicker id="schedule-frequency" label="Frequência" value={form.frequency}
            onChange={next => setField('frequency', next as FormState['frequency'])}
            options={[{ value: 'daily', label: 'Todos os dias' }, { value: 'once', label: 'Uma vez' }]} />
          {form.frequency === 'once' && <label className="field">Data local<input type="date" value={form.localDate} onChange={event => setField('localDate', event.target.value)} required /></label>}
          <label className="field">Horário local<input type="time" value={form.localTime} onChange={event => setField('localTime', event.target.value)} required /></label>
          <IonPicker id="schedule-timezone" label="Fuso horário" value={form.timezone} searchable
            onChange={next => setField('timezone', next)} options={timezoneOptions.map(zone => ({ value: zone, label: zone }))} />
          <IonPicker id="schedule-missed-policy" label="Se perder o horário" value={form.missedPolicy}
            onChange={next => setField('missedPolicy', next as FormState['missedPolicy'])}
            options={[{ value: 'skip', label: 'Ignorar' }, { value: 'run_once', label: 'Executar uma vez ao reabrir' }]} />
        </div>
        {form.targetKind === 'workflow' && workflows.length === 0 && <div className="schedule-inline-note"><span>Nenhum workflow neste projeto.</span><button type="button" className="touch-target secondary-button" onClick={onWorkflows}>Criar workflow</button></div>}
        {!backends.some(item => item.available) && <div className="schedule-inline-note"><span>Nenhum backend disponível.</span><button type="button" className="touch-target secondary-button" onClick={onSettings}>Configurar provedor</button></div>}
        {backendOption?.kind === 'cli' && <label className="schedule-cli-consent"><input type="checkbox" checked={form.allowCli} onChange={event => setField('allowCli', event.target.checked)} /><span>Entendo que o CLI executa com a autoridade do próprio processo; a política de ferramentas do Harflex não controla suas ações internas.</span></label>}
        <div className="schedule-actions">{editing && <button type="button" className="touch-target secondary-button" onClick={reset}>Cancelar edição</button>}<button type="submit" className="touch-target primary-button" disabled={pending || !form.name.trim() || !selectedBackend || (form.targetKind === 'prompt' ? !form.prompt.trim() : !form.workflowId) || (form.frequency === 'once' && !form.localDate) || (backendOption?.kind === 'cli' && !form.allowCli)}>Salvar agendamento</button></div>
      </form>}
      {state === 'loading' && <p role="status" className="muted">Carregando agendamentos…</p>}
      {state === 'error' && <div className="inline-error" role="alert"><p>Não foi possível carregar os agendamentos.</p><button type="button" className="touch-target secondary-button" onClick={() => void refresh(true)}>Tentar novamente</button></div>}
      {state === 'ready' && <>
        <section className="schedule-catalog" aria-labelledby="schedule-catalog-title"><div><h3 id="schedule-catalog-title">Agendados</h3><p className="muted">Pausar impede próximos disparos; jobs ativos podem ser cancelados no histórico.</p></div>{items.length === 0 ? <div className="catalog-empty"><CalendarClock aria-hidden="true" /><strong>Nenhum agendamento salvo</strong><span className="muted">Configure o primeiro trabalho acima.</span></div> : <ul className="schedule-list">{items.map(item => {
          const scheduledJob = jobs.find(job => job.scheduleId === item.id && job.trigger === 'scheduled')
          const endedOnce = item.frequency === 'once' && !item.enabled && scheduledJob
          return <li key={item.id}><div className="schedule-summary"><strong>{item.name}</strong><span className="muted">{item.targetKind === 'workflow' ? `Workflow · ${workflows.find(workflow => workflow.id === item.workflowId)?.name ?? 'definição indisponível'}` : 'Prompt'} · {item.frequency === 'daily' ? 'diário' : 'uma vez'} · {item.timezone}</span><span className="muted">{item.nextRunAt ? `Próximo disparo: ${formatInstant(item.nextRunAt, item.timezone)}` : endedOnce ? 'Execução única encerrada' : 'Sem próximo disparo'}</span></div><span className={`status-chip${item.enabled ? ' status-ready' : ' status-unavailable'}`}>{item.enabled ? 'Ativo' : endedOnce ? 'Encerrado' : 'Pausado'}</span><div className="schedule-item-actions"><button type="button" className="touch-target secondary-button" onClick={() => edit(item)} disabled={pending} aria-label={`Editar ${item.name}`}>Editar</button>{!endedOnce && <button type="button" className="touch-target secondary-button" onClick={() => void action(() => backend.setSchedulePaused(item.id, item.enabled, item.revision), item.enabled ? 'Agendamento pausado.' : 'Agendamento retomado.')} disabled={pending} aria-label={`${item.enabled ? 'Pausar' : 'Retomar'} ${item.name}`}>{item.enabled ? <CirclePause aria-hidden="true" /> : <Play aria-hidden="true" />}{item.enabled ? 'Pausar' : 'Retomar'}</button>}<button type="button" className="touch-target primary-button" onClick={() => void action(() => backend.runScheduleNow(item.id), 'Job enfileirado para execução local.')} disabled={pending} aria-label={`Executar agora ${item.name}`}><Play aria-hidden="true" />Executar agora</button></div></li>
        })}</ul>}</section>
        <section className="schedule-history" aria-labelledby="schedule-history-title">
          <h3 id="schedule-history-title">Histórico de jobs (até 200 recentes)</h3>
          {jobs.length === 0 ? <p className="muted">Nenhum job registrado.</p> : <ul>{jobs.map(job => <li key={job.id}>
            <div className="schedule-summary">
              <strong>{byId.get(job.scheduleId)?.name ?? 'Agendamento salvo'}</strong>
              <span className="muted">{job.trigger === 'manual' ? 'Manual' : 'Automático'} · <time dateTime={job.dueAt}>{formatInstant(job.dueAt, byId.get(job.scheduleId)?.timezone ?? localTimezone)}</time></span>
              {job.errorCode && <span className="schedule-job-error">{failureLabels[job.errorCode] ?? 'A execução não foi concluída. Confira a sessão vinculada.'}</span>}
            </div>
            <span className={`status-chip${job.status === 'completed' ? ' status-ready' : ['failed', 'interrupted'].includes(job.status) ? ' status-unavailable' : ''}`}>{jobLabels[job.status]}</span>
            <div className="schedule-item-actions">
              {job.sessionId && <button type="button" className="touch-target secondary-button" onClick={() => void openSession(job.sessionId)}>Abrir sessão</button>}
              {['queued', 'running', 'waiting_user', 'cancel_requested'].includes(job.status) && <button type="button" className="touch-target secondary-button" onClick={() => void action(() => backend.cancelScheduleJob(job.id), result => result.status === 'cancel_requested' ? 'Cancelamento solicitado; aguardando confirmação.' : result.status === 'cancelled' ? 'Job cancelado.' : `Job já ${jobLabels[result.status].toLowerCase()}.`)} disabled={pending}>{job.status === 'cancel_requested' ? 'Tentar cancelar' : 'Cancelar job'}</button>}
            </div>
          </li>)}</ul>}
        </section>
      </>}
    </>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {notice && <p className="form-success" role="status">{notice}</p>}
  </div>
}
