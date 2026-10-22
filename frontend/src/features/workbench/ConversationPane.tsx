import { useEffect, useLayoutEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { ChevronLeft, Info, Lock, Pause, RefreshCw } from 'lucide-react'
import { runIsLive, type SessionState } from '../../state/session'
import { ChatComposer, type ComposerAttachment } from '../../components/ChatComposer'
import { ContextGauge } from '../../components/ContextGauge'
import { ApprovalCard } from './ApprovalCard'
import { ToolCallCard, toolTarget } from './ToolCallCard'
import { Markdown } from '../../components/Markdown'
import type { Delegation } from '../../lib/backend'
import { delegationBudgetText } from '../../lib/delegationBudget'
import type { AppMode } from '../../state/appMode'
import { requestOf, splitContinuation } from './continuation'
import { splitPipelineChoice, type PipelineChoice } from './pipelineChoice'
import { PipelineChoiceCard } from './PipelineChoiceCard'
import { CommandRunnerContext, type CommandRunner } from '../../components/commandRunner'
import { localeTag, t, useT } from '../../i18n'
import { RunProgress as RunProgressView } from '../../components/activity/RunProgressView'
import { buildRunProgress, type RunProgress } from '../../components/activity/runProgress'

const failureCopy: Record<string, string> = {
  approval_denied: 'aprovação negada',
  turn_limit: 'limite de turnos atingido',
  execution_failed: 'erro durante a execução',
  backend_changed: 'a configuração do provedor mudou; inicie uma nova sessão',
  tool_failed: 'uma ferramenta falhou (veja o cartão dela acima)',
  invalid_tool_result: 'uma ferramenta devolveu um resultado inválido',
  unknown_tool: 'o modelo pediu uma ferramenta que não existe',
  policy_denied: 'a política de segurança negou uma ferramenta',
  tool_limit_exceeded: 'limite de chamadas de ferramenta atingido',
  output_limit_exceeded: 'o modelo ultrapassou o limite de saída',
  context_length_exceeded: 'a conversa não coube no contexto do modelo nem depois de compactada; reduza o pedido ou a leitura',
  provider_failed: 'o provedor de IA falhou',
  timeout: 'tempo esgotado',
}

function statusText(state: Pick<SessionState, 'calling' | 'activeRun' | 'outcome' | 'error'>): string {
  if (state.error) return state.error
  if (state.activeRun === 'awaiting_approval') return t('Aguardando aprovação')
  if (state.calling || state.activeRun === 'running') return t('Executando')
  switch (state.outcome?.status) {
    case 'completed': return t('Concluído')
    case 'cancelled': return t('Cancelado')
    case 'interrupted': return t('Interrompido')
    case 'failed': return t('Falhou: {reason}', { reason: t(failureCopy[state.outcome.code ?? ''] ?? failureCopy[state.outcome.reason ?? ''] ?? 'erro durante a execução') })
  }
  return t('Pronto')
}

function activeActionText(messages: SessionState['messages']): string {
  for (let index = messages.length - 1; index >= 0; index--) {
    const item = messages[index]
    if (item.kind !== 'tool' || item.call.status !== 'running') continue
    const known = ({
      read: 'Lendo arquivo', write: 'Escrevendo arquivo', edit: 'Editando arquivo',
      grep: 'Buscando no código', find: 'Localizando arquivos', ls: 'Listando arquivos',
      bash: 'Executando comando', powershell: 'Executando comando',
    } as Record<string, string>)[item.call.name]
    const action = known ? t(known) : t('Executando {name}', { name: item.call.name })
    // Shell arguments may contain values that do not belong in another live label.
    const target = ['bash', 'powershell'].includes(item.call.name) ? '' : toolTarget(item.call.arguments)
    return target ? t('{action} · {target}', { action, target }) : action
  }
  if (messages.some(item => item.kind === 'assistant' && item.streaming)) return t('Gerando resposta do agente')
  return t('Agente trabalhando')
}

// A run the session holds as ended stays ended on screen, even when the journal lost its terminal event (a restart).
function finished(progress: RunProgress, outcome: SessionState['outcome'], status: string, waiting = ''): RunProgress {
  const phase = outcome?.status === 'failed' || outcome?.status === 'cancelled' || outcome?.status === 'interrupted' ? outcome.status : waiting ? 'waiting' : 'completed'
  return { ...progress, live: false, phase, step: phase === 'waiting' ? waiting : status, endedAt: progress.endedAt ?? progress.lastAt }
}

/** What the person wrote. A message that opened a continued conversation has the old one's context after it, shown folded. */
function UserText({ text }: { text: string }) {
  const t = useT()
  const split = splitContinuation(text)
  if (!split) return <p className="turn-text">{text}</p>
  return <>
    <p className="turn-text">{split.request}</p>
    <details className="turn-context"><summary>{t('Contexto levado da conversa anterior')}</summary><p className="turn-text">{split.context}</p></details>
  </>
}

type Props = {
  state: Pick<SessionState, 'messages' | 'pendingApprovals' | 'activeRun' | 'outcome' | 'calling' | 'error' | 'draft' | 'connectionState' | 'readOnly'> & { events?: SessionState['events']; hasOlder?: boolean }
  /** Reads the turns before the first one on screen (a long conversation opens on its last turns). */
  onLoadOlder?: () => Promise<void>
  /** Watchers this chat's agent left running; the badge opens them in the side panel. */
  watching?: number
  onShowWatchers?: () => void
  onDraft: (text: string) => void
  onPrompt: (text: string) => void
  onResolve: (approvalId: string, allow: boolean) => void
  onCancel: () => void
  onRetry: () => void
  onNewWork: (recoveryPrompt?: string) => void
  /** Continues a conversation that cannot be resumed: opens a new one that carries its context and sends the message. */
  onContinue?: (text: string) => void
  /** The person's answer to "how do you want to carry this" when the agent offered a pipeline. */
  onPipelineChoice?: (choice: PipelineChoice) => void
  /** Runs the shell commands of the agent's code blocks, and sends their result back to it. */
  commands?: CommandRunner
  viewMode: AppMode
  cancelPending?: boolean
  delegationParentId?: string
  delegation?: Delegation
  queuedTask?: string
  onPrepareDelegatedTask?: () => void
  onOpenParent?: () => void
  loadingHistory: boolean
  /** The project's permission control, drawn above the message box. */
  permissionControl?: ReactNode
  /** How full the conversation's context is, for the gauge beside the model. */
  contextUse?: { used: number; window: number }
  onCompact?: () => void
  /** Messages waiting for the current answer to end, sent one by one after it. */
  queued?: string[]
  onQueue?: () => void
  onUnqueue?: (index: number) => void
  attachments?: ComposerAttachment[]
  onAttach?: (files: File[]) => void
  onRemoveAttachment?: (id: string) => void
  attachError?: string
  /** Backend that runs this session, shown beside the session controls. */
  backendLabel?: string
  /** Provider, model and effort of the chat (Casual), in place of the backend label. */
  modelBar?: ReactNode
  /** The chat's project (Casual), beside the permission control. */
  projectControl?: ReactNode
  /** The bar names another provider, model or effort: the next message continues in a new chat with it. */
  switchModel?: { usable: boolean; onReset: () => void }
  /** The open conversation; a new one enters with a short transition. */
  sessionKey?: string
}


export function ConversationPane({ sessionKey, state, loadingHistory, permissionControl, contextUse, onCompact, queued = [], onQueue, onUnqueue, attachments = [], onAttach, onRemoveAttachment, attachError, viewMode, cancelPending = false, delegationParentId, delegation, queuedTask, onPrepareDelegatedTask, onOpenParent, onDraft, onPrompt, onResolve, onCancel, onRetry, onNewWork, onContinue, onPipelineChoice, onLoadOlder, watching = 0, onShowWatchers, commands, backendLabel, modelBar, switchModel, projectControl }: Props) {
  const t = useT()
  const draft = state.draft
  const running = state.activeRun === 'running'
  const liveWork = !loadingHistory && !state.readOnly && !state.error && (running || state.calling || cancelPending)
  const liveStatus = state.activeRun === 'awaiting_approval' ? t('Aguardando aprovação') : activeActionText(state.messages)
  // The run's objective progress: step, elapsed time, actions, files and tokens from the journal.
  const progress = useMemo(() => buildRunProgress(state.events ?? []), [state.events])
  const showProgress = !!state.events?.length && !loadingHistory && !state.error && !cancelPending && (liveWork ? progress.live || state.activeRun === 'awaiting_approval' : !!progress.startedAt && !!state.outcome)
  const displayedStatus = loadingHistory ? t('Carregando histórico') : cancelPending ? t('Confirmando cancelamento…') : state.error ?? (liveWork ? liveStatus : statusText(state))
  const exhausted = !!delegation && delegation.promptLimit > 0 && delegation.promptCount >= delegation.promptLimit
  const lastUserRequest = requestOf([...state.messages].reverse().find(message => message.kind === 'user')?.text ?? '')
  const carriedRequest = draft.trim() || (state.readOnly ? lastUserRequest : '')
  const newWorkLabel = state.readOnly ? 'Continuar em novo chat'
    : draft.trim() ? viewMode === 'casual' ? 'Levar rascunho para novo chat' : 'Levar rascunho para novo trabalho'
      : viewMode === 'casual' ? 'Novo chat' : 'Sessões do projeto'
  // A conversation that cannot be resumed still takes a message: it goes to a new one that carries this one's context.
  const switching = !!switchModel && !!onContinue
  const continuing = state.readOnly && !!onContinue || switching
  const canSend = switching ? draft.trim() !== '' && switchModel.usable && !loadingHistory && !state.calling && !running && state.activeRun !== 'awaiting_approval' && state.connectionState === 'ready' : draft.trim() !== '' && (!state.readOnly || continuing) && !loadingHistory && !exhausted && !state.calling && (state.activeRun === 'idle' || state.activeRun === 'paused' || continuing && state.activeRun === 'awaiting_approval') && state.connectionState === 'ready'
  const emptyCasual = viewMode === 'casual' && state.messages.length === 0 && !state.readOnly && !loadingHistory && !state.error && !state.outcome && !state.calling && state.activeRun === 'idle' && !delegation && !delegationParentId && !queuedTask
  // While another conversation loads, the one on screen stays (dimmed) instead of flashing an empty page; the new one
  // replaces it whole once its history is in.
  const shown = useRef<{ key?: string; messages: SessionState['messages'] }>({ key: sessionKey, messages: state.messages })
  if (!loadingHistory || state.messages.length > 0 && shown.current.key === sessionKey) shown.current = { key: sessionKey, messages: state.messages }
  const changingChat = loadingHistory && shown.current.key !== sessionKey && shown.current.messages.length > 0
  const messages = changingChat ? shown.current.messages : state.messages
  // From the click on another chat until it is in, the one on screen dims: it is on its way out.
  const leaving = loadingHistory && messages.length > 0
  const log = useRef<HTMLOListElement>(null)
  const composer = useRef<HTMLTextAreaElement>(null)
  // Follow the newest message only while the reader is at the bottom; scrolling up pauses it.
  const following = useRef(true)
  // Older turns come in at the top; the reader stays on the message they were reading.
  const [loadingOlder, setLoadingOlder] = useState(false)
  const fromBottom = useRef<number | null>(null)
  const canLoadOlder = !!state.hasOlder && !!onLoadOlder && !loadingHistory
  function loadOlder() {
    if (!canLoadOlder || loadingOlder || !onLoadOlder) return
    const element = log.current
    fromBottom.current = element ? element.scrollHeight - element.scrollTop : null
    setLoadingOlder(true)
    void onLoadOlder().finally(() => setLoadingOlder(false))
  }
  useLayoutEffect(() => {
    const element = log.current
    if (state.messages.length === 0) following.current = true
    if (element && fromBottom.current !== null && !following.current) {
      element.scrollTop = element.scrollHeight - fromBottom.current
      fromBottom.current = null
      return
    }
    fromBottom.current = null
    if (element && following.current) element.scrollTop = element.scrollHeight
  }, [state.messages, loadingHistory])
  // The last turns fit on screen, so there is nothing to scroll: the older ones come in by themselves, once for each
  // new length of the conversation (a page that brings nothing new does not ask again).
  const autoLoadedAt = useRef(-1)
  useEffect(() => {
    const element = log.current
    if (!canLoadOlder || loadingOlder || autoLoadedAt.current === state.messages.length) return
    if (!element || element.scrollHeight <= element.clientHeight) {
      autoLoadedAt.current = state.messages.length
      loadOlder()
    }
  })
  function trackScroll() {
    const element = log.current
    if (!element) return
    following.current = element.scrollHeight - element.scrollTop - element.clientHeight < 64
    if (element.scrollTop < 160) loadOlder()
  }
  function submit(event?: FormEvent) {
    event?.preventDefault()
    if (!canSend) return
    following.current = true
    if (continuing) onContinue(draft)
    else onPrompt(draft)
  }
  return <div className={`conversation${viewMode === 'casual' ? ' conversation-casual' : ''}`}>
    {(!emptyCasual || !!draft.trim() || !!modelBar) && <div className="conversation-actions"><button type="button" className="touch-target secondary-button" onClick={() => onNewWork(carriedRequest || undefined)} disabled={state.readOnly && loadingHistory}>{newWorkLabel === 'Sessões do projeto' && <ChevronLeft aria-hidden="true" />}{t(newWorkLabel)}</button>{delegationParentId && <button type="button" className="touch-target secondary-button" onClick={onOpenParent} disabled={loadingHistory}>{t('Voltar à sessão pai')}</button>}{!modelBar && backendLabel && <span className="conversation-backend muted">{backendLabel}</span>}</div>}
    {queuedTask && <div className="continuity-note muted delegated-task-recovery"><p>{t('A tarefa delegada está salva no histórico. Prepare-a no rascunho e revise antes de enviar; nada é executado automaticamente.')}</p><button type="button" className="touch-target secondary-button" onClick={onPrepareDelegatedTask} disabled={loadingHistory || state.calling || state.readOnly || !!draft || state.activeRun !== 'idle'}>{t('Preparar tarefa delegada')}</button></div>}
    {delegation && <p className="continuity-note muted">{t('Subagente: {budget}. A espera por aprovação não conta; tentativas interrompidas contam. Não é limite de custo ou tokens.', { budget: delegationBudgetText(delegation) })}{exhausted ? t(' Limite atingido: revise o histórico antes de delegar novamente.') : ''}</p>}
    {messages.length === 0
      ? <div className="empty-state conversation-enter"><p>{viewMode === 'casual' ? t('Nova conversa') : t('Nenhuma conversa iniciada')}</p>{viewMode !== 'casual' && <span className="muted">{t('O trabalho começa com uma conversa.')}</span>}</div>
      : <CommandRunnerContext.Provider value={commands}><ol ref={log} key={changingChat ? shown.current.key : sessionKey} className={`conversation-log${leaving ? ' is-switching' : ' conversation-enter'}`} aria-label={t('Conversa')} aria-busy={leaving || undefined} onScroll={trackScroll}>
        {canLoadOlder && <li className="turn history-older">{loadingOlder ? <span className="muted" role="status">{t('Carregando mensagens anteriores…')}</span> : <button type="button" className="touch-target text-button" onClick={loadOlder}>{t('Ver mensagens anteriores')}</button>}</li>}
        {messages.map((item, index) => {
          const reply = item.kind === 'assistant' ? splitPipelineChoice(item.text) : undefined
          // The choice is open only while nothing came after it: once the person answers, the answer is in the conversation.
          const asking = !!reply?.ask && !!onPipelineChoice && index === messages.length - 1 && !(item.kind === 'assistant' && item.streaming) && !state.readOnly
          return <li key={item.id} className={`turn turn-${item.kind}`}>
            {item.kind === 'tool' ? <ToolCallCard call={item.call} /> : item.kind === 'compaction' ? <div className="compaction-divider" role="note">
              <span className="compaction-divider-line">{item.afterTokens > 0 ? t('Contexto compactado · {before} → {after} tokens', { before: item.beforeTokens.toLocaleString(localeTag()), after: item.afterTokens.toLocaleString(localeTag()) }) : t('O agente compactou a conversa · {before} tokens antes', { before: item.beforeTokens.toLocaleString(localeTag()) })}</span>
              {item.summary && <details><summary>{t('Ver resumo')}</summary><pre>{item.summary}</pre></details>}
            </div> : <>
              <span className="turn-author">{item.kind === 'user' ? t('Você') : t('Agente')}</span>
              {item.kind === 'assistant'
                ? <div className="turn-text turn-markdown"><Markdown text={reply?.text ?? item.text} />{item.streaming && <span className="assistant-streaming-dots" aria-hidden="true"><i /><i /><i /></span>}</div>
                : <UserText text={item.text} />}
              {asking && <PipelineChoiceCard viewMode={viewMode} disabled={loadingHistory || state.calling || runIsLive(state)} onChoose={onPipelineChoice!} />}
            </>}
          </li>
        })}
      </ol></CommandRunnerContext.Provider>}
    {state.pendingApprovals.length > 0 && <div className="approval-stack">{state.pendingApprovals.map(approval => <ApprovalCard key={approval.approvalId} approval={approval} disabled={state.readOnly || state.calling || state.connectionState === 'degraded'} onResolve={allow => onResolve(approval.approvalId, allow)} />)}</div>}
    <div className="composer-meta">
      {!emptyCasual && showProgress && <RunProgressView progress={liveWork ? progress : finished(progress, state.outcome, displayedStatus, watching > 0 ? (watching === 1 ? t('Aguardando 1 vigia') : t('Aguardando {count} vigias', { count: watching })) : '')} showFinished />}
      {!emptyCasual && !showProgress && <div className={`run-status${liveWork ? ' run-status-active' : ''}`} role="status" aria-live="polite" aria-atomic="true">
        {liveWork && <span className="run-status-indicator" aria-hidden="true" />}
        <span>{displayedStatus}</span>
        {liveWork && <span className="run-status-motion" aria-hidden="true"><i /><i /><i /></span>}
      </div>}
    </div>
    {watching > 0 && <button type="button" className="chat-watchers-badge" onClick={onShowWatchers}><span className="activity-state-indicator" aria-hidden="true" />{watching === 1 ? t('1 vigia ativa') : t('{count} vigias ativas', { count: watching })}</button>}
    {state.connectionState === 'degraded' && <button type="button" className="touch-target secondary-button" disabled={state.calling} onClick={onRetry}>{t('Atualizar eventos')}</button>}
    <ChatComposer id="composer-input" label={t('Mensagem')} value={draft} onChange={onDraft} onSend={() => submit()} canSend={canSend} inputRef={composer}
      disabled={state.readOnly && !continuing || continuing && state.calling || loadingHistory}
      placeholder={switching ? t('Enter envia e abre um novo chat com o modelo escolhido, levando o contexto') : continuing ? t('Continue de onde parou · Enter envia e abre um novo chat com o contexto') : undefined}
      running={running || state.calling && !loadingHistory && !continuing} stopping={cancelPending} onStop={onCancel}
      onQueue={!state.readOnly && !switching && onQueue ? onQueue : undefined} queued={queued} onUnqueue={onUnqueue}
      attachments={attachments} onAttach={!state.readOnly ? onAttach : undefined} onRemoveAttachment={onRemoveAttachment}
      notices={<>
        {/* What the box will do with the next message is said inside it, on one quiet line, never between the run and the box. */}
        {state.readOnly ? <p className="composer-notice"><Lock aria-hidden="true" /><span>{continuing
          ? t('Somente leitura: esta execução foi encerrada. O que você enviar abre um novo chat com o contexto desta conversa.')
          : t('Somente leitura: esta execução foi encerrada. Inicie um novo chat para continuar.')}</span></p>
          : state.activeRun === 'paused' && <p className="composer-notice"><Pause aria-hidden="true" /><span>{t('Envie uma mensagem para retomar o trabalho. Revise os resultados das ferramentas interrompidas.')}</span></p>}
        {!state.readOnly && draft.trim() && viewMode !== 'casual' && <p className="composer-notice"><Info aria-hidden="true" /><span>{t('Se iniciar outro trabalho, este rascunho será levado para revisão e permanecerá sem envio automático.')}</span></p>}
        {switchModel && <p className="composer-notice chat-model-note" role="status"><RefreshCw aria-hidden="true" /><span>{switchModel.usable ? t('A próxima mensagem abre um novo chat com esta escolha, levando o contexto desta conversa.') : t('Escolha um modelo disponível para continuar com este provedor.')}</span> <button type="button" className="text-button" onClick={switchModel.onReset}>{t('Manter o atual')}</button></p>}
        {attachError && <p className="composer-notice" role="alert"><Info aria-hidden="true" /><span>{attachError}</span></p>}
      </>}
      footerStart={permissionControl} footerEnd={<>{modelBar}{projectControl}{contextUse && contextUse.window > 0 && onCompact && <ContextGauge used={contextUse.used} window={contextUse.window} onCompact={onCompact} disabled={running || state.calling} />}</>} />
  </div>
}
