import { useLayoutEffect, useRef, type FormEvent, type ReactNode, type RefObject } from 'react'
import { Compass, Layers, MessageSquarePlus, Search, Send, Settings2, Workflow } from 'lucide-react'
import type { BackendOption, ModelCatalogResult } from '../../lib/backend'
import { ChatModelBar } from './ChatModelBar'
import './casualStart.css'

type Props = {
  firstPrompt: string
  onFirstPrompt: (value: string) => void
  onSubmit: (event: FormEvent) => void
  backends: BackendOption[]
  selectedBackend: string
  onBackend: (value: string) => void
  apiModelName: string
  catalog?: ModelCatalogResult
  modelId: string
  onModel: (value: string) => void
  effort: string
  onEffort: (value: string) => void
  catalogLoading: boolean
  catalogError: string
  onQueryModels: () => void
  settingsStatus: 'loading' | 'ready' | 'error'
  pending: boolean
  canStart: boolean
  onConfigure: () => void
  providerTrigger: RefObject<HTMLButtonElement>
  onStartSDD: () => void
  projectName?: string
  /** The project's permission control, in the same place as in a conversation. */
  permissionControl?: ReactNode
  /** What went wrong starting or opening a chat. */
  error?: string
  /** The chat's project, beside the permission control. */
  projectControl?: ReactNode
}

const composerMaxHeight = 200

// Starting points that fill the box (nothing is sent until the person presses Enviar).
const suggestions = [
  { Icon: Compass, title: 'Entender o projeto', prompt: 'Resuma o que este projeto faz, como está organizado e por onde começar a ler o código.' },
  { Icon: Layers, title: 'Tecnologias e serviços', prompt: 'Quais tecnologias, domínios e serviços este projeto usa, e como eles se conectam?' },
  { Icon: Search, title: 'Encontrar no código', prompt: 'Onde fica no código a parte responsável por ' },
] as const

/** A new Casual chat, drawn like the conversation it becomes: a centered welcome and the same message card. */
export function CasualStartPanel({ firstPrompt, onFirstPrompt, onSubmit, backends, selectedBackend, onBackend, apiModelName, catalog, modelId, onModel, effort, onEffort, catalogLoading, catalogError, onQueryModels, settingsStatus, pending, canStart, onConfigure, providerTrigger, onStartSDD, projectName, permissionControl, error, projectControl }: Props) {
  const composer = useRef<HTMLTextAreaElement>(null)
  useLayoutEffect(() => {
    const element = composer.current
    if (!element) return
    element.style.height = 'auto'
    element.style.height = `${Math.min(element.scrollHeight, composerMaxHeight)}px`
  }, [firstPrompt])
  const option = backends.find(item => item.id === selectedBackend)
  const listed = catalog?.complete && catalog.status === 'complete' && catalog.backendId === selectedBackend ? catalog.models.filter(item => item.backendId === selectedBackend && item.source === catalog.source) : []
  const model = listed.find(item => item.id === modelId)
  function suggest(prompt: string) {
    onFirstPrompt(prompt)
    requestAnimationFrame(() => { const element = composer.current; if (element) { element.focus(); element.setSelectionRange(prompt.length, prompt.length) } })
  }

  return <section className="conversation conversation-casual casual-start" aria-labelledby="casual-start-title">
    <div className="conversation-actions casual-start-header">
      <h2 id="casual-start-title" className="casual-start-title"><MessageSquarePlus aria-hidden="true" />Novo chat</h2>
    </div>

    <div className="casual-welcome">
      <h3>O que vamos fazer{projectName ? <> em <span className="casual-welcome-project">{projectName}</span></> : ''}?</h3>
      <p className="muted">Pergunte sobre o projeto, peça uma mudança ou comece um trabalho completo de SDD.</p>
      <ul className="casual-suggestions" aria-label="Sugestões">
        {suggestions.map(({ Icon, title, prompt }) => <li key={title}><button type="button" className="casual-suggestion" onClick={() => suggest(prompt)} disabled={pending}>
          <Icon aria-hidden="true" /><strong>{title}</strong><span>{prompt}</span></button></li>)}
        <li><button type="button" className="casual-suggestion is-sdd" onClick={onStartSDD} disabled={pending}>
          <Workflow aria-hidden="true" /><strong>Iniciar trabalho SDD</strong><span>Discovery, SPEC, Plan, Code e QA, com revisão em cada fase.</span></button></li>
      </ul>
      {option?.id === 'opencode' && <p className="project-warning">A execução OpenCode aguarda prova de isolamento dos plugins nesta instalação. A lista pode ser consultada, mas não inicia uma sessão.</p>}
      {settingsStatus === 'loading' && <p className="muted casual-start-status" role="status">Lendo o provedor padrão…</p>}
      {settingsStatus === 'error' && <p className="project-warning" role="alert">Não foi possível ler o padrão salvo. Escolha o provedor abaixo; a seleção vale apenas para esta conversa.</p>}
      {catalogError && <div className="casual-start-error" role="alert"><span className="form-error">{catalogError}</span><button type="button" className="touch-target text-button" onClick={onQueryModels} disabled={catalogLoading}>Tentar de novo</button></div>}
      {error && <p className="form-error" role="alert">{error}</p>}
      <button ref={providerTrigger} type="button" className="text-button casual-configure" onClick={onConfigure} disabled={pending}><Settings2 aria-hidden="true" />Configurar provedor</button>
    </div>

    <form className="composer composer-card" onSubmit={onSubmit}>
      <label className="visually-hidden" htmlFor="casual-start-input">Mensagem inicial</label>
      <textarea ref={composer} id="casual-start-input" className="composer-input" rows={1} value={firstPrompt} maxLength={20000} required disabled={pending}
        placeholder="Descreva o trabalho · Enter envia, Shift+Enter quebra a linha" onChange={event => onFirstPrompt(event.target.value)}
        onKeyDown={event => { if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing && canStart && !pending) { event.preventDefault(); event.currentTarget.form?.requestSubmit() } }} />
      <div className="composer-card-actions">
        <ChatModelBar backends={backends} backendId={selectedBackend} onBackend={onBackend} fixedModel={apiModelName} loading={catalogLoading}
          modelOptions={listed.map(item => ({ value: item.id, label: item.displayName || item.id, disabled: item.availability === 'unavailable' }))} modelId={modelId} onModel={onModel}
          efforts={model?.supportedReasoningEfforts ?? []} effort={effort} onEffort={onEffort} disabled={pending} />
        {projectControl}
        {permissionControl && <div className="composer-card-permission">{permissionControl}</div>}
        <button type="submit" className="touch-target primary-button composer-card-send" disabled={!canStart || pending}><Send aria-hidden="true" />{pending ? 'Iniciando…' : 'Enviar'}</button>
      </div>
    </form>
  </section>
}
