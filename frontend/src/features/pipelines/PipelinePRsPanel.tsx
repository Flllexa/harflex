import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { Check, CircleAlert, GitPullRequest, MessageSquare } from 'lucide-react'
import { IonPicker } from '../../components/IonPicker'
import { Markdown } from '../../components/Markdown'
import { errorMessage, type Backend, type BackendOption, type MCPServer, type Pipeline, type PipelineRoleModelSelection, type ProviderProfile, type StageExecutor } from '../../lib/backend'
import { PipelineRoleModelPicker } from './PipelineRoleModelPicker'
import { useStageActivity } from './useStageActivity'
import { PullRequestCards, usePullRequests } from './PullRequestCards'

/** QA approved and the stage not yet ended. Pipelines saved before the stage existed finished at QA with no status for it. */
export function pullRequestsOpen(run: Pipeline): boolean {
  if (run.stageStatus.eval !== 'completed') return false
  if (run.currentStage === 'prs') return run.stageStatus.prs === 'active'
  return run.currentStage === '' && (!run.stageStatus.prs || run.stageStatus.prs === 'pending')
}

type Props = {
  backend: Backend
  backends: BackendOption[]
  run: Pipeline
  workspaceId?: string
  defaultBackendId: string
  pending: boolean
  /** The project's permission profile and the menu that changes it. */
  permissionProfile?: string
  permissionControl?: ReactNode
  /** What the project chose for this phase in "Provedor e modelo por fase". */
  configured?: StageExecutor
  /** With a selection when the project chose a model for this phase and it was confirmed against the catalog. */
  onStart: (backendId: string, selection?: PipelineRoleModelSelection) => void
  onFinish: (outcome: 'completed' | 'skipped', reason?: string) => void
  onOpenSession?: (sessionId: string, draft?: string) => Promise<void>
  /** Applies the approved patch to the project; the PRs need it there. Resolves false when it did not apply. */
  onApply?: () => Promise<boolean>
  /** Opens the screens that resolve what is missing: MCP Servers and Settings. */
  onOpenMCP?: () => void
  onSettings?: () => void
}

/** Prepared in the message box when the person comes back for the fixes a review asked for. It is read and sent by them. */
export const reviewFixesRequest = 'Leia os comentários de revisão dos pull requests que você abriu. Aplique as correções pedidas na mesma branch de cada PR, faça commit e push (sem push forçado) e responda ao comentário que cada correção resolve. Se algum pedido não estiver claro, pergunte antes de mudar.'

export function PipelinePRsPanel({ backend, backends, run, workspaceId, defaultBackendId, pending, configured, permissionProfile, permissionControl, onStart, onFinish, onOpenSession, onApply, onOpenMCP, onSettings }: Props) {
  // The stage is named: a pipeline saved before it existed has no current stage, but its agent still has to be followed.
  const activity = useStageActivity(backend, run, 'prs')
  const [servers, setServers] = useState<MCPServer[]>()
  const [serversFailed, setServersFailed] = useState(false)
  const [choice, setChoice] = useState('')
  const [profiles, setProfiles] = useState<ProviderProfile[]>([])
  const [selection, setSelection] = useState<PipelineRoleModelSelection>()
  const [profileModelOnly, setProfileModelOnly] = useState(false)
  const [skipping, setSkipping] = useState(false)
  const [reason, setReason] = useState('')
  const [openError, setOpenError] = useState('')
  // The agent needs the Harflex tool loop (shell, files, MCP), so only API profiles qualify.
  const eligible = useMemo(() => backends.filter(item => item.available && item.kind === 'api'), [backends])
  const configuredId = configured && eligible.some(item => item.id === configured.backendId) ? configured.backendId : ''
  const backendId = eligible.some(item => item.id === choice) ? choice : configuredId || eligible.find(item => item.id === defaultBackendId)?.id || eligible[0]?.id || ''
  // The project may have chosen a model for this phase: then the conversation is bound to it, confirmed against the catalog.
  const configuredModel = configured?.modelId && configured.backendId === backendId && !profileModelOnly ? configured.modelId : ''
  const backendOption = eligible.find(item => item.id === backendId)
  const configuredName = configured ? backends.find(item => item.id === configured.backendId)?.name ?? configured.backendId : ''
  const profileModel = profiles.find(item => item.id === backendId)?.model ?? ''
  const connected = (servers ?? []).filter(server => server.enabled)
  // The backend says whether an isolated Code run's approved patch is still to be applied; it is the one that refuses the agent otherwise.
  const applied = !run.codePatchPending
  const hasAgentRun = !!activity?.sessionId
  const finished = activity?.status === 'completed'
  const working = activity?.status === 'running' || activity?.status === 'awaiting_approval' || activity?.status === 'cancellation_pending'
  const full = permissionProfile === 'full_access'
  const pullRequests = usePullRequests(backend, run.id).items ?? []
  const recorded = pullRequests.length > 0
  const watching = pullRequests.some(item => item.watch && item.state === 'open')
  const merged = pullRequests.some(item => item.state === 'merged')
  // The road from the approved patch to a merged PR; each step lights up as it happens.
  const pushed = recorded || finished
  const steps: { label: string; state: 'done' | 'now' | 'next' }[] = [
    { label: 'Patch aprovado', state: applied ? 'done' : 'now' },
    { label: 'Branch e commit', state: pushed ? 'done' : applied && working ? 'now' : 'next' },
    { label: 'Push', state: pushed ? 'done' : 'next' },
    { label: 'PR aberto', state: recorded ? 'done' : finished ? 'now' : 'next' },
    { label: 'Em revisão', state: merged ? 'done' : watching ? 'now' : 'next' },
    { label: 'Mergeado', state: merged ? 'done' : 'next' },
  ]
  // A later step done means the earlier ones are too.
  const lastDone = steps.map(step => step.state).lastIndexOf('done')
  steps.forEach((step, index) => { if (index < lastDone) step.state = 'done' })

  useEffect(() => { setProfileModelOnly(false); setSelection(undefined) }, [backendId, run.id])
  useEffect(() => {
    let live = true
    backend.listProviderProfiles().then(found => { if (live) setProfiles(found) }).catch(() => { if (live) setProfiles([]) })
    return () => { live = false }
  }, [backend])
  useEffect(() => {
    if (!workspaceId) return
    let live = true
    setServers(undefined); setServersFailed(false)
    backend.listMCPServers(workspaceId).then(found => { if (live) setServers(found) }).catch(() => { if (live) setServersFailed(true) })
    return () => { live = false }
  }, [backend, workspaceId, run.id])

  const [applying, setApplying] = useState(false)
  // One click: the approved patch goes to the project, then the agent opens the pull requests.
  async function approveAndOpen() {
    if (!applied) {
      if (!onApply) return
      setApplying(true)
      const ok = await onApply().finally(() => setApplying(false))
      if (!ok) return
    }
    if (selection && configuredModel) onStart(backendId, selection)
    else onStart(backendId)
  }

  async function openConversation() {
    if (!activity?.sessionId || !onOpenSession) return
    setOpenError('')
    try { await onOpenSession(activity.sessionId) } catch (failure) { setOpenError(errorMessage(failure)) }
  }

  return <section className="pipeline-prs-panel" aria-label="Abrir pull requests">
    <div className="pipeline-prs-heading"><GitPullRequest aria-hidden="true" /><div><h3>Pull requests</h3><p className="muted">Último passo: a IA cria a branch, faz o commit, envia e abre o PR sozinha, pelo GitHub ou Bitbucket conectados em MCP Servers.</p></div></div>
    <ol className="pr-track" aria-label="Caminho até o merge">{steps.map((step, index) => <li key={step.label} className={`pr-track-step is-${step.state}`}><span className="pr-track-dot" aria-hidden="true">{step.state === 'done' ? <Check /> : index + 1}</span><span>{step.label}</span></li>)}</ol>
    <ul className="pipeline-prs-checks pr-pills" aria-label="Antes de começar">
      <li className={applied ? 'is-done' : 'is-pending'}>{applied ? <Check aria-hidden="true" /> : <CircleAlert aria-hidden="true" />}<span>{applied ? 'Patch aprovado aplicado ao projeto.' : 'O patch aprovado pelo QA vai para a pasta do projeto quando você clicar em Aprovar e abrir os PRs, conferido antes e depois da gravação.'}</span></li>
      <li className={connected.length > 0 ? 'is-done' : 'is-pending'}>{connected.length > 0 ? <Check aria-hidden="true" /> : <CircleAlert aria-hidden="true" />}<span>{servers === undefined && !serversFailed ? 'Conferindo os servidores MCP…' : serversFailed ? 'Não foi possível conferir os servidores MCP. Você ainda pode começar.' : connected.length > 0 ? `Servidor MCP conectado: ${connected.map(server => server.name).join(', ')}.` : 'Nenhum servidor MCP conectado. Conecte o GitHub ou o Bitbucket em MCP Servers; sem isso a IA só faz commit e push e entrega o link para você abrir o PR.'}</span>{connected.length === 0 && onOpenMCP && <button type="button" className="pr-pill-action" onClick={onOpenMCP}>Conectar GitHub ou Bitbucket</button>}</li>
      <li className={full ? 'is-done' : 'is-pending'}>{full ? <Check aria-hidden="true" /> : <CircleAlert aria-hidden="true" />}<span>{full ? 'Acesso total: a IA não pede aprovação.' : 'Hoje a IA pede a sua aprovação para cada comando e chamada MCP. Escolha Acesso total para ela abrir o PR sem interrupções.'}</span></li>
    </ul>
    {permissionControl && <div className="pipeline-prs-permissions"><span className="muted">Permissões do projeto</span>{permissionControl}</div>}
    {eligible.length === 0 ? <div className="authoring-provider-empty" role="alert"><strong>Os PRs precisam de um provedor API</strong><p>O terminal e as ferramentas MCP rodam no Harflex, que só as oferece a perfis de API. Configure um perfil em Configurações.</p>{onSettings && <button type="button" className="touch-target secondary-button" onClick={onSettings}>Configurar provedor</button>}</div> : <>
      <IonPicker id="pipeline-prs-backend" label="Executor da fase" value={backendId} onChange={setChoice} options={eligible.map(item => ({ value: item.id, label: item.name }))} disabled={pending || working} />
      {configuredModel && backendOption ? <>
        <PipelineRoleModelPicker backend={backend} workspaceId={workspaceId} stage="prs" backendOption={backendOption} defaultModelBackendId={configured!.backendId} defaultModelId={configuredModel} phaseConfigured disabled={pending || working} onSelectionChange={setSelection} />
        <button type="button" className="touch-target text-button" onClick={() => setProfileModelOnly(true)} disabled={pending || working}>Usar o modelo do perfil{profileModel ? ` (${profileModel})` : ''}</button>
      </> : profileModel && <p className="muted pipeline-prs-model">Modelo: <span className="mono">{profileModel}</span>, o configurado no perfil. Para usar outro neste projeto, escolha-o em Provedor e modelo por fase.</p>}
    </>}
    {hasAgentRun && <p className={`pipeline-prs-status${finished ? ' is-done' : ''}`} role="status">{finished ? 'A IA terminou. Leia o relatório na conversa e conclua a etapa.' : working ? 'A IA está trabalhando nos PRs. Acompanhe na conversa.' : activity?.status === 'failed' ? 'A última execução parou por um erro. Veja a conversa ou comece outra.' : activity?.status === 'cancelled' ? 'A última execução foi cancelada. Você pode começar outra.' : 'Há uma conversa dos PRs. Abra para continuar.'}</p>}
    {openError && <p className="form-error" role="alert">{openError}</p>}
    {configured && !configuredId && eligible.length > 0 && <p className="project-warning" role="status">O executor escolhido para PRs ({configuredName}) não está disponível agora; a fase parte do padrão. Ajuste em "Provedor e modelo por fase".</p>}
    <div className="pipeline-actions">
      {hasAgentRun && onOpenSession && <button type="button" className="touch-target secondary-button" onClick={() => void openConversation()} disabled={pending}><MessageSquare aria-hidden="true" />Abrir a conversa dos PRs</button>}
      <button type="button" className="touch-target primary-button" onClick={() => void approveAndOpen()} disabled={pending || working || applying || !backendId || (!!configuredModel && !selection)}><GitPullRequest aria-hidden="true" />{applying ? 'Aplicando o patch…' : hasAgentRun ? 'Pedir outra rodada à IA' : applied ? 'Abrir PRs com a IA' : 'Aprovar e abrir os PRs'}</button>
      {finished && <button type="button" className="touch-target primary-button" onClick={() => onFinish('completed')} disabled={pending}><Check aria-hidden="true" />Concluir PRs</button>}
    </div>
    <PullRequestCards backend={backend} pipelineId={run.id} permissionProfile={permissionProfile} permissionControl={permissionControl} />
    {!skipping ? <button type="button" className="touch-target text-button pipeline-skip" onClick={() => setSkipping(true)} disabled={pending || working}>Pular os PRs</button> : <div className="pipeline-skip-confirm">
      <p>Pular registra a etapa como pulada e encerra o pipeline, sem abrir nenhum pull request.</p>
      <label className="field">Motivo (opcional)<input value={reason} onChange={event => setReason(event.target.value)} maxLength={1000} /></label>
      <div className="pipeline-actions"><button type="button" className="touch-target secondary-button" onClick={() => setSkipping(false)}>Voltar</button><button type="button" className="touch-target secondary-button" onClick={() => onFinish('skipped', reason.trim())} disabled={pending}>Confirmar pulo</button></div>
    </div>}
  </section>
}

/**
 * What the stage left behind once it ended: the agent's report, or the note that it was skipped. A finished stage does
 * not close the work: the conversation of the PRs stays open, with its context and tools, for the fixes a review asks for.
 */
export function PipelinePRsOutcome({ backend, run, onOpenSession }: { backend?: Backend; run: Pipeline; onOpenSession?: (sessionId: string, draft?: string) => Promise<void> }) {
  const completed = run.stageStatus.prs === 'completed'
  // Only a pipeline that ended this stage has a conversation to come back to; the others are not read.
  const activity = useStageActivity(backend, completed ? run : undefined, 'prs')
  const [openError, setOpenError] = useState('')
  const report = run.artifacts.prs?.content
  async function open(draft?: string) {
    if (!activity?.sessionId || !onOpenSession) return
    setOpenError('')
    try { await onOpenSession(activity.sessionId, draft) } catch (failure) { setOpenError(errorMessage(failure)) }
  }
  if (run.stageStatus.prs === 'skipped') return <section className="pipeline-prs-panel" aria-label="Pull requests"><div className="pipeline-prs-heading"><GitPullRequest aria-hidden="true" /><div><h3>Pull requests</h3><p className="muted">Etapa pulada: nenhum pull request foi aberto por este pipeline.</p></div></div></section>
  if (!completed || !report) return null
  return <section className="pipeline-prs-panel" aria-label="Pull requests abertos"><div className="pipeline-prs-heading"><GitPullRequest aria-hidden="true" /><div><h3>Pull requests abertos</h3><p className="muted">Relatório final da IA, guardado como evidência desta etapa.</p></div></div><div className="pipeline-prs-report"><Markdown text={report} /></div>
    {activity?.sessionId && onOpenSession && <>
      <p className="muted pipeline-prs-followup">Se o revisor pedir ajustes, continue na conversa dos PRs: a IA mantém o contexto, o terminal e as ferramentas MCP. Nada é enviado sozinho.</p>
      {openError && <p className="form-error" role="alert">{openError}</p>}
      {backend && <PullRequestCards backend={backend} pipelineId={run.id} />}
      <div className="pipeline-actions">
        <button type="button" className="touch-target primary-button" onClick={() => void open(reviewFixesRequest)}><GitPullRequest aria-hidden="true" />Corrigir os comentários da revisão</button>
        <button type="button" className="touch-target secondary-button" onClick={() => void open()}><MessageSquare aria-hidden="true" />Continuar a conversa dos PRs</button>
      </div>
    </>}
  </section>
}
