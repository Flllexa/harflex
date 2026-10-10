import { useLayoutEffect, useRef, type FormEvent, type KeyboardEvent, type ReactNode } from 'react'
import { ChevronLeft, Send, Square } from 'lucide-react'
import { runIsLive, type SessionState } from '../../state/session'
import { ApprovalCard } from './ApprovalCard'
import { ToolCallCard, toolTarget } from './ToolCallCard'
import { Markdown } from '../../components/Markdown'
import type { Delegation } from '../../lib/backend'
import { delegationBudgetText } from '../../lib/delegationBudget'
import type { AppMode } from '../../state/appMode'
import { requestOf, splitContinuation } from './continuation'
import { splitPipelineChoice, type PipelineChoice } from './pipelineChoice'
import { PipelineChoiceCard } from './PipelineChoiceCard'
import { t, useT } from '../../i18n'

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
  state: Pick<SessionState, 'messages' | 'pendingApprovals' | 'activeRun' | 'outcome' | 'calling' | 'error' | 'draft' | 'connectionState' | 'readOnly'>
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
  /** Backend that runs this session, shown beside the session controls. */
  backendLabel?: string
  /** Provider, model and effort of the chat (Casual), in place of the backend label. */
  modelBar?: ReactNode
  /** The chat's project (Casual), beside the permission control. */
  projectControl?: ReactNode
  /** The bar names another provider, model or effort: the next message continues in a new chat with it. */
  switchModel?: { usable: boolean; onReset: () => void }
}

const composerMaxHeight = 200

export function ConversationPane({ state, loadingHistory, permissionControl, viewMode, cancelPending = false, delegationParentId, delegation, queuedTask, onPrepareDelegatedTask, onOpenParent, onDraft, onPrompt, onResolve, onCancel, onRetry, onNewWork, onContinue, onPipelineChoice, backendLabel, modelBar, switchModel, projectControl }: Props) {
  const t = useT()
  const draft = state.draft
  const running = state.activeRun === 'running'
  const liveWork = !loadingHistory && !state.readOnly && !state.error && (running || state.calling || cancelPending)
  const liveStatus = state.activeRun === 'awaiting_approval' ? t('Aguardando aprovação') : activeActionText(state.messages)
  const displayedStatus = loadingHistory ? t('Carregando histórico') : cancelPending ? t('Confirmando cancelamento…') : state.error ?? (liveWork ? liveStatus : statusText(state))
  const exhausted = !!delegation && delegation.promptCount >= delegation.promptLimit
  const lastUserRequest = requestOf([...state.messages].reverse().find(message => message.kind === 'user')?.text ?? '')
  const carriedRequest = draft.trim() || (state.readOnly ? lastUserRequest : '')
  const executionBusy = runIsLive(state) || state.calling && !loadingHistory
  const newWorkLabel = state.readOnly ? 'Continuar em novo chat'
    : draft.trim() ? viewMode === 'casual' ? 'Levar rascunho para novo chat' : 'Levar rascunho para novo trabalho'
      : viewMode === 'casual' ? 'Novo chat' : 'Sessões do projeto'
  // A conversation that cannot be resumed still takes a message: it goes to a new one that carries this one's context.
  const switching = !!switchModel && !!onContinue
  const continuing = state.readOnly && !!onContinue || switching
  const canSend = switching ? draft.trim() !== '' && switchModel.usable && !loadingHistory && !state.calling && !running && state.activeRun !== 'awaiting_approval' && state.connectionState === 'ready' : draft.trim() !== '' && (!state.readOnly || continuing) && !loadingHistory && !exhausted && !state.calling && (state.activeRun === 'idle' || state.activeRun === 'paused' || continuing && state.activeRun === 'awaiting_approval') && state.connectionState === 'ready'
  const emptyCasual = viewMode === 'casual' && state.messages.length === 0 && !state.readOnly && !loadingHistory && !state.error && !state.outcome && !state.calling && state.activeRun === 'idle' && !delegation && !delegationParentId && !queuedTask
  const log = useRef<HTMLOListElement>(null)
  const composer = useRef<HTMLTextAreaElement>(null)
  // Follow the newest message only while the reader is at the bottom; scrolling up pauses it.
  const following = useRef(true)
  useLayoutEffect(() => {
    const element = log.current
    if (state.messages.length === 0) following.current = true
    if (element && following.current) element.scrollTop = element.scrollHeight
  }, [state.messages, loadingHistory])
  useLayoutEffect(() => {
    const element = composer.current
    if (!element) return
    element.style.height = 'auto'
    element.style.height = `${Math.min(element.scrollHeight, composerMaxHeight)}px`
  }, [draft])
  function trackScroll() {
    const element = log.current
    if (element) following.current = element.scrollHeight - element.scrollTop - element.clientHeight < 64
  }
  function submit(event?: FormEvent) {
    event?.preventDefault()
    if (!canSend) return
    following.current = true
    if (continuing) onContinue(draft)
    else onPrompt(draft)
  }
  function composerKey(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) submit(event)
  }
  return <div className={`conversation${viewMode === 'casual' ? ' conversation-casual' : ''}`}>
    {(!emptyCasual || !!draft.trim() || !!modelBar) && <div className="conversation-actions"><button type="button" className="touch-target secondary-button" onClick={() => onNewWork(carriedRequest || undefined)} disabled={state.readOnly && loadingHistory || executionBusy}>{newWorkLabel === 'Sessões do projeto' && <ChevronLeft aria-hidden="true" />}{t(newWorkLabel)}</button>{delegationParentId && <button type="button" className="touch-target secondary-button" onClick={onOpenParent} disabled={loadingHistory || state.calling || runIsLive(state)}>{t('Voltar à sessão pai')}</button>}{!modelBar && backendLabel && <span className="conversation-backend muted">{backendLabel}</span>}</div>}
    {queuedTask && <div className="continuity-note muted delegated-task-recovery"><p>{t('A tarefa delegada está salva no histórico. Prepare-a no rascunho e revise antes de enviar; nada é executado automaticamente.')}</p><button type="button" className="touch-target secondary-button" onClick={onPrepareDelegatedTask} disabled={loadingHistory || state.calling || state.readOnly || !!draft || state.activeRun !== 'idle'}>{t('Preparar tarefa delegada')}</button></div>}
    {delegation && <p className="continuity-note muted">{t('Subagente: {budget}. A espera por aprovação não conta; tentativas interrompidas contam. Não é limite de custo ou tokens.', { budget: delegationBudgetText(delegation) })}{exhausted ? t(' Limite atingido: revise o histórico antes de delegar novamente.') : ''}</p>}
    {state.messages.length === 0
      ? <div className="empty-state"><p>{viewMode === 'casual' ? t('Nova conversa') : t('Nenhuma conversa iniciada')}</p>{viewMode !== 'casual' && <span className="muted">{t('O trabalho começa com uma conversa.')}</span>}</div>
      : <ol ref={log} className="conversation-log" aria-label={t('Conversa')} onScroll={trackScroll}>
        {state.messages.map((item, index) => {
          const reply = item.kind === 'assistant' ? splitPipelineChoice(item.text) : undefined
          // The choice is open only while nothing came after it: once the person answers, the answer is in the conversation.
          const asking = !!reply?.ask && !!onPipelineChoice && index === state.messages.length - 1 && !(item.kind === 'assistant' && item.streaming) && !state.readOnly
          return <li key={item.id} className={`turn turn-${item.kind}`}>
            {item.kind === 'tool' ? <ToolCallCard call={item.call} /> : <>
              <span className="turn-author">{item.kind === 'user' ? t('Você') : t('Agente')}</span>
              {item.kind === 'assistant'
                ? <div className="turn-text turn-markdown"><Markdown text={reply?.text ?? item.text} />{item.streaming && <span className="assistant-streaming-dots" aria-hidden="true"><i /><i /><i /></span>}</div>
                : <UserText text={item.text} />}
              {asking && <PipelineChoiceCard viewMode={viewMode} disabled={loadingHistory || state.calling || runIsLive(state)} onChoose={onPipelineChoice!} />}
            </>}
          </li>
        })}
      </ol>}
    {state.pendingApprovals.length > 0 && <div className="approval-stack">{state.pendingApprovals.map(approval => <ApprovalCard key={approval.approvalId} approval={approval} disabled={state.readOnly || state.calling || state.connectionState === 'degraded'} onResolve={allow => onResolve(approval.approvalId, allow)} />)}</div>}
    <div className="composer-meta">
      {!emptyCasual && <div className={`run-status${liveWork ? ' run-status-active' : ''}`} role="status" aria-live="polite" aria-atomic="true">
        {liveWork && <span className="run-status-indicator" aria-hidden="true" />}
        <span>{displayedStatus}</span>
        {liveWork && <span className="run-status-motion" aria-hidden="true"><i /><i /><i /></span>}
      </div>}
      {permissionControl && !modelBar && <div className="composer-toolbar">{permissionControl}</div>}
    </div>
    {state.connectionState === 'degraded' && <button type="button" className="touch-target secondary-button" disabled={state.calling} onClick={onRetry}>{t('Atualizar eventos')}</button>}
    {state.readOnly ? <p className="continuity-note muted">{continuing
      ? <>{t('Somente leitura: esta execução foi encerrada. Escreva abaixo e o Harflex continua em um novo chat, levando o contexto desta conversa.')}{carriedRequest ? t(' Continuar em novo chat só prepara o último pedido para você revisar antes de enviar.') : ''}</>
      : <>{t('Somente leitura: esta execução foi encerrada. Inicie um novo chat para continuar.')} {carriedRequest ? t('O último pedido será preparado para revisão, sem reenvio automático.') : t('Você pode descrever um novo pedido.')}</>}</p> : state.activeRun === 'paused' && <p className="continuity-note muted">{t('Envie uma mensagem para retomar o trabalho. Revise os resultados das ferramentas interrompidas.')}</p>}
    {!state.readOnly && draft.trim() && viewMode !== 'casual' && <p className="continuity-note muted">{t('Se iniciar outro trabalho, este rascunho será levado para revisão e permanecerá sem envio automático.')}</p>}
    {switchModel && <p className="chat-model-note" role="status">{switchModel.usable ? t('A próxima mensagem abre um novo chat com esta escolha, levando o contexto desta conversa.') : t('Escolha um modelo disponível para continuar com este provedor.')} <button type="button" className="text-button" onClick={switchModel.onReset}>{t('Manter o atual')}</button></p>}
    <form className={`composer${modelBar ? ' composer-card' : ''}`} onSubmit={submit}>
      <label className="visually-hidden" htmlFor="composer-input">{t('Mensagem')}</label>
      <textarea ref={composer} id="composer-input" className="composer-input" rows={1} value={draft} disabled={state.readOnly && !continuing || continuing && state.calling || loadingHistory} placeholder={switching ? t('Enter envia e abre um novo chat com o modelo escolhido, levando o contexto') : continuing ? t('Continue de onde parou · Enter envia e abre um novo chat com o contexto') : t('Descreva o trabalho · Enter envia, Shift+Enter quebra a linha')} onChange={event => onDraft(event.target.value)} onKeyDown={composerKey} />
      {modelBar ? <div className="composer-card-actions">
        {modelBar}
        {projectControl}
        {permissionControl && <div className="composer-card-permission">{permissionControl}</div>}
        {running || cancelPending
          ? <button type="button" className="touch-target secondary-button composer-card-send" disabled={cancelPending} onClick={event => { event.preventDefault(); event.stopPropagation(); onCancel() }}><Square aria-hidden="true" />{cancelPending ? t('Cancelando…') : t('Cancelar execução')}</button>
          : <button type="submit" className="touch-target primary-button composer-card-send" disabled={!canSend}><Send aria-hidden="true" />{t('Enviar')}</button>}
      </div> : running || cancelPending
        ? <button type="button" className="touch-target secondary-button" disabled={cancelPending} onClick={event => { event.preventDefault(); event.stopPropagation(); onCancel() }}><Square aria-hidden="true" />{cancelPending ? t('Cancelando…') : t('Cancelar execução')}</button>
        : <button type="submit" className="touch-target primary-button" disabled={!canSend}><Send aria-hidden="true" />{t('Enviar')}</button>}
    </form>
  </div>
}
