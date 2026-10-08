import { useEffect, useState, type FormEvent } from 'react'
import { CreateToggle, useCreateForm } from '../../components/CreateForm'
import { ArrowRight, CirclePause, FolderOpen, GitFork, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { IonPicker } from '../../components/IonPicker'
import { errorMessage, type Backend, type BackendOption, type Workflow, type WorkflowRun, type WorkflowStep } from '../../lib/backend'

type Props = { backend: Backend; backends: BackendOption[]; workspaceId?: string; reviewedSessions: ReadonlySet<string>; onProjects: () => void; onSettings: () => void; onOpenSession: (sessionId: string) => Promise<void> }
const blank = (): WorkflowStep => ({ name: '', prompt: '' })
const statusLabels: Record<WorkflowRun['status'], string> = { ready: 'Pronto', running: 'Executando', waiting_user: 'Aguardando aprovação', paused: 'Pausado', completed: 'Concluído', cancelled: 'Cancelado' }

export function WorkflowsPage({ backend, backends, workspaceId, reviewedSessions, onProjects, onSettings, onOpenSession }: Props) {
  const [definitions, setDefinitions] = useState<Workflow[]>([])
  const [runs, setRuns] = useState<WorkflowRun[]>([])
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [editingId, setEditingId] = useState<string>()
  const [name, setName] = useState('')
  const [steps, setSteps] = useState<WorkflowStep[]>([blank()])
  const [backendChoice, setBackendChoice] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string>()
  const [notice, setNotice] = useState<string>()
  const [recoveryChoices, setRecoveryChoices] = useState<Record<string, '' | 'retry' | 'skip'>>({})
  const [acknowledgedSessions, setAcknowledgedSessions] = useState<Record<string, string>>({})
  const form = useCreateForm(state === 'ready', definitions.length)
  const selectedBackend = backendChoice || backends.find(item => item.available)?.id || ''

  async function refresh() {
    if (!workspaceId) return
    setState('loading')
    try {
      const [saved, active] = await Promise.all([backend.listWorkflows(workspaceId), backend.listWorkflowRuns(workspaceId)])
      setDefinitions(saved); setRuns(active); setState('ready')
    } catch { setState('error') }
  }
  useEffect(() => { void refresh() }, [backend, workspaceId])

  function updateStep(index: number, field: keyof WorkflowStep, value: string) {
    setSteps(current => current.map((step, position) => position === index ? { ...step, [field]: value } : step))
  }
  function reset() { setEditingId(undefined); setName(''); setSteps([blank()]) }
  function edit(item: Workflow) { setEditingId(item.id); setName(item.name); setSteps(item.steps.map(step => ({ ...step }))); setError(undefined); form.setOpen(true) }

  async function save(event: FormEvent) {
    event.preventDefault()
    if (!workspaceId || pending) return
    setPending(true); setError(undefined); setNotice(undefined)
    try {
      const saved = await backend.saveWorkflow({ id: editingId, workspaceId, name: name.trim(), steps: steps.map(step => ({ name: step.name.trim(), prompt: step.prompt.trim() })) })
      setDefinitions(current => [saved, ...current.filter(item => item.id !== saved.id)])
      setNotice(`Workflow ${saved.name} salvo nesta máquina.`)
      reset(); form.setOpen(false)
    } catch (failure) { setError(errorMessage(failure)) }
    finally { setPending(false) }
  }

  async function start(item: Workflow) {
    if (pending || !selectedBackend) return
    setPending(true); setError(undefined); setNotice(undefined)
    try {
      const run = await backend.startWorkflow({ workflowId: item.id, backendId: selectedBackend })
      setRuns(current => [run, ...current]); setNotice('Execução criada. Inicie a primeira etapa quando estiver pronto.')
    } catch (failure) { setError(errorMessage(failure)) }
    finally { setPending(false) }
  }

  async function advance(run: WorkflowRun) {
    if (pending) return
    setPending(true); setError(undefined); setNotice(undefined)
    try {
      const updated = await backend.runWorkflowStep(run.id)
      setRuns(current => current.map(item => item.id === updated.id ? updated : item))
      setNotice(updated.status === 'waiting_user' ? 'Etapa aguardando sua aprovação na conversa.' : updated.status === 'completed' ? 'Workflow concluído com execução registrada.' : updated.status === 'paused' ? 'Etapa pausada. Confira o histórico antes de tentar novamente.' : 'Etapa concluída; próxima pronta.')
    } catch (failure) { setError(errorMessage(failure)) }
    finally { setPending(false) }
  }

  async function cancel(run: WorkflowRun) {
    if (pending) return
    setPending(true); setError(undefined)
    try { const updated = await backend.cancelWorkflowRun(run.id); setRuns(current => current.map(item => item.id === updated.id ? updated : item)); setNotice('Execução cancelada.') }
    catch (failure) { setError(errorMessage(failure)) }
    finally { setPending(false) }
  }

  async function recover(run: WorkflowRun) {
    const choice = recoveryChoices[run.id]
    if (pending || run.status !== 'paused' || !workspaceId || !run.lastSessionId || !choice || !reviewedSessions.has(`${workspaceId}:${run.lastSessionId}`) || acknowledgedSessions[run.id] !== run.lastSessionId) return
    setPending(true); setError(undefined); setNotice(undefined)
    try {
      const updated = await backend.resumeWorkflowRun({ runId: run.id, reviewedSessionId: run.lastSessionId, choice })
      setRuns(current => current.map(item => item.id === updated.id ? updated : item))
      setNotice(choice === 'retry' ? 'Etapa pronta para uma nova execução. Execute quando decidir continuar.' : 'Etapa pulada com sua decisão registrada.')
      setRecoveryChoices(current => ({ ...current, [run.id]: '' }))
      setAcknowledgedSessions(current => ({ ...current, [run.id]: '' }))
    } catch (failure) { setError(errorMessage(failure)) }
    finally { setPending(false) }
  }

  return <div className="workflows-page">
    <div className="destination-heading"><div><h2>Workflows locais</h2><p className="muted">Sequências de tarefas executadas por agentes, com histórico por etapa.</p></div>{workspaceId && <div className="pipeline-actions">{form.collapsible && <CreateToggle open={form.open} onToggle={() => { if (form.open) reset(); form.setOpen(!form.open) }} label="Novo workflow" />}<button type="button" className="touch-target secondary-button" onClick={() => void refresh()} disabled={state === 'loading'}><RefreshCw aria-hidden="true" />Atualizar</button></div>}</div>
    {!workspaceId ? <div className="catalog-empty"><FolderOpen aria-hidden="true" /><strong>Abra um projeto para criar workflows</strong><span className="muted">As definições e execuções ficam vinculadas à pasta local.</span><button type="button" className="touch-target primary-button" onClick={onProjects}>Abrir projetos</button></div> : <>
      {form.open && <form className="workflow-editor" onSubmit={save}><div className="destination-heading"><div><h3>{editingId ? 'Editar workflow' : 'Novo workflow'}</h3><p className="muted">Cada execução guarda uma cópia dos passos usados.</p></div><GitFork aria-hidden="true" /></div><label className="field">Nome do workflow<input value={name} onChange={event => setName(event.target.value)} maxLength={200} required /></label>
        <div className="workflow-step-editor"><div className="destination-heading"><h3>Etapas</h3><button type="button" className="touch-target secondary-button" onClick={() => setSteps(current => [...current, blank()])} disabled={steps.length >= 20}><Plus aria-hidden="true" />Adicionar etapa</button></div>
          {steps.map((step, index) => <div className="workflow-step-form" key={index}><span className="workflow-step-number">{index + 1}</span><div><label className="field">Nome da etapa {index + 1}<input value={step.name} onChange={event => updateStep(index, 'name', event.target.value)} maxLength={128} required /></label><label className="field">Prompt da etapa {index + 1}<textarea value={step.prompt} onChange={event => updateStep(index, 'prompt', event.target.value)} rows={3} maxLength={1024 * 1024} required /></label></div>{steps.length > 1 && <button type="button" className="touch-target icon-button" aria-label={`Remover etapa ${index + 1}`} onClick={() => setSteps(current => current.filter((_, position) => position !== index))}><Trash2 aria-hidden="true" /></button>}</div>)}
        </div><div className="pipeline-actions">{editingId && <button type="button" className="touch-target secondary-button" onClick={reset}>Cancelar edição</button>}<button type="submit" className="touch-target primary-button" disabled={pending || !name.trim() || steps.some(step => !step.name.trim() || !step.prompt.trim())}>Salvar workflow</button></div>
      </form>}
      {state === 'loading' && <p className="muted" role="status">Carregando workflows…</p>}
      {state === 'error' && <div className="inline-error" role="alert"><p>Não foi possível carregar os workflows.</p><button type="button" className="touch-target secondary-button" onClick={() => void refresh()}>Tentar novamente</button></div>}
      {state === 'ready' && <>
        <section className="workflow-catalog" aria-labelledby="workflow-catalog-title"><div className="destination-heading"><div><h3 id="workflow-catalog-title">Definições</h3><p className="muted">Selecione o backend antes de iniciar uma execução.</p></div></div><IonPicker id="workflow-backend" label="Backend das execuções" value={selectedBackend} onChange={setBackendChoice} options={[{ value: '', label: 'Escolha um backend' }, ...backends.map(item => ({ value: item.id, label: `${item.name}${item.available ? '' : ' · indisponível'}`, disabled: !item.available }))]} />{!backends.some(item => item.available) && <button type="button" className="touch-target secondary-button" onClick={onSettings}>Configurar provedor</button>}
          {definitions.length === 0 ? <div className="catalog-empty"><GitFork aria-hidden="true" /><strong>Nenhum workflow salvo</strong><span className="muted">Monte a primeira sequência no formulário acima.</span></div> : <ul className="workflow-definitions">{definitions.map(item => <li key={item.id}><div><strong>{item.name}</strong><span className="muted">{item.steps.length} {item.steps.length === 1 ? 'etapa' : 'etapas'} · {item.steps.map(step => step.name).join(' → ')}</span></div><div><button type="button" className="touch-target secondary-button" onClick={() => edit(item)} aria-label={`Editar ${item.name}`}>Editar</button><button type="button" className="touch-target primary-button" onClick={() => void start(item)} disabled={!selectedBackend || pending} aria-label={`Iniciar ${item.name}`}>Iniciar</button></div></li>)}</ul>}
        </section>
        <section className="workflow-runs" aria-labelledby="workflow-runs-title">
          <h3 id="workflow-runs-title">Execuções</h3>
          {runs.length === 0 ? <p className="muted">Nenhuma execução iniciada.</p> : <ul>{runs.map(run =>
            <li key={run.id} className="workflow-run">
              <div className="destination-heading"><div><strong>{definitions.find(item => item.id === run.workflowId)?.name ?? 'Workflow salvo'}</strong><p className="muted">{run.currentStep < run.steps.length ? `Etapa ${run.currentStep + 1} de ${run.steps.length}: ${run.steps[run.currentStep].name}` : `${run.steps.length} etapas concluídas`}</p></div><span className={`status-chip${run.status === 'completed' ? ' status-ready' : run.status === 'paused' ? ' status-unavailable' : ''}`}>{statusLabels[run.status]}</span></div>
              <div className="workflow-run-actions">
                {run.lastSessionId && <button type="button" className="touch-target secondary-button" onClick={() => void onOpenSession(run.lastSessionId)}>Abrir sessão da etapa</button>}
                {(run.status === 'ready' || run.status === 'waiting_user' || run.status === 'running') && <button type="button" className="touch-target primary-button" onClick={() => void advance(run)} disabled={pending}>{run.status === 'ready' ? <>Executar etapa<ArrowRight aria-hidden="true" /></> : 'Atualizar resultado'}</button>}
                {run.status !== 'completed' && run.status !== 'cancelled' && <button type="button" className="touch-target secondary-button" onClick={() => void cancel(run)} disabled={pending}><CirclePause aria-hidden="true" />Cancelar</button>}
              </div>
              {run.status === 'paused' && run.lastSessionId && <div className="workflow-recovery">
                <p className="muted">Abra a sessão da etapa e confira o histórico antes de decidir. Reexecutar pode repetir efeitos já produzidos.</p>
                <div className="workflow-recovery-controls">
                  <IonPicker id={`workflow-recovery-${run.id}`} label="Decisão para a etapa pausada" value={recoveryChoices[run.id] ?? ''}
                    onChange={next => setRecoveryChoices(current => ({ ...current, [run.id]: next as '' | 'retry' | 'skip' }))}
                    options={[{ value: '', label: 'Escolha uma ação' }, { value: 'retry', label: 'Preparar nova execução' }, { value: 'skip', label: 'Pular esta etapa' }]} />
                  <label className="workflow-review-confirmation"><input type="checkbox" checked={acknowledgedSessions[run.id] === run.lastSessionId} disabled={!workspaceId || !reviewedSessions.has(`${workspaceId}:${run.lastSessionId}`)} onChange={event => setAcknowledgedSessions(current => ({ ...current, [run.id]: event.target.checked ? run.lastSessionId : '' }))} />Revisei o histórico da sessão e escolhi como continuar.</label>
                  <button type="button" className="touch-target primary-button" onClick={() => void recover(run)} disabled={pending || !recoveryChoices[run.id] || !workspaceId || !reviewedSessions.has(`${workspaceId}:${run.lastSessionId}`) || acknowledgedSessions[run.id] !== run.lastSessionId}>Confirmar decisão</button>
                </div>
              </div>}
            </li>
          )}</ul>}
        </section>
      </>}
    </>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {notice && <p className="form-success" role="status">{notice}</p>}
  </div>
}
