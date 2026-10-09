import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { CircleAlert, CircleCheck, CircleDot, Eye, EyeOff, GitMerge, GitPullRequest, GitPullRequestClosed, LoaderCircle, MessageSquareReply, RefreshCw, ShieldAlert, Wrench } from 'lucide-react'
import { errorMessage, type Backend, type PullRequest } from '../../lib/backend'
import { localeTag, useT } from '../../i18n'
import { openExternal } from '../../lib/desktop'
import './pullRequests.css'

type Props = {
  backend: Backend
  pipelineId: string
  /** The project's permission profile: the watch fixes and pushes by itself only with full access. */
  permissionProfile?: string
  permissionControl?: ReactNode
}

const stateIcons = { open: GitPullRequest, merged: GitMerge, closed: GitPullRequestClosed }
const eventIcons: Record<string, typeof CircleDot> = { opened: GitPullRequest, watch_on: Eye, watch_off: EyeOff, checking: RefreshCw, fixed: Wrench, quiet: CircleCheck, merged: GitMerge, closed: GitPullRequestClosed, needs_access: ShieldAlert, error: CircleAlert }
const time = (value?: string) => value ? new Date(value).toLocaleTimeString(localeTag(), { hour: '2-digit', minute: '2-digit' }) : ''

/** The pull requests of a pipeline, kept fresh by the backend's change event and a slow poll. */
export function usePullRequests(backend: Backend, pipelineId: string) {
  const [items, setItems] = useState<PullRequest[]>()
  const [error, setError] = useState('')
  const refresh = useCallback(async () => {
    try { setItems(await backend.listPipelinePullRequests(pipelineId)); setError('') } catch (failure) { setError(errorMessage(failure)) }
  }, [backend, pipelineId])
  useEffect(() => {
    void refresh()
    const stop = backend.onPullRequestsChange(changed => { if (changed === pipelineId) void refresh() })
    const timer = setInterval(() => void refresh(), 15000)
    return () => { stop(); clearInterval(timer) }
  }, [backend, pipelineId, refresh])
  return { items, setItems, error, setError, refresh }
}

/** The pull requests the PRs conversation opened, each with its review watch and what it has done. */
export function PullRequestCards({ backend, pipelineId, permissionProfile, permissionControl }: Props) {
  const t = useT()
  const stateLabels = { open: t('Aberto'), merged: t('Mergeado'), closed: t('Fechado') }
  const { items, setItems, error, setError } = usePullRequests(backend, pipelineId)
  const [busy, setBusy] = useState<string>()

  async function act(id: string, action: () => Promise<PullRequest>) {
    setBusy(id); setError('')
    try { const updated = await action(); setItems(current => current?.map(item => item.id === updated.id ? updated : item)) }
    catch (failure) { setError(errorMessage(failure)) } finally { setBusy(undefined) }
  }

  if (!items || items.length === 0) return null
  const full = permissionProfile === 'full_access'
  return <section className="pr-cards" aria-label={t('Pull requests deste trabalho')}>
    {items.map(pr => {
      const StateIcon = stateIcons[pr.state]
      const open = pr.state === 'open'
      const recent = [...pr.timeline].reverse().slice(0, 6)
      return <article key={pr.id} className={`pr-card is-${pr.state}`}>
        <header className="pr-card-header">
          <span className="pr-card-state" aria-hidden="true"><StateIcon /></span>
          <div className="pr-card-title">
            <a href={pr.url} onClick={event => { event.preventDefault(); void openExternal(pr.url) }} title={pr.url}>{pr.title || pr.url}</a>
            <span className="muted">{pr.branch && <code className="mono">{pr.branch}</code>}<span className={`pr-chip is-${pr.state}`}>{stateLabels[pr.state]}</span></span>
          </div>
          {open && <button type="button" role="switch" aria-checked={pr.watch} className={`pr-watch${pr.watch ? ' is-on' : ''}`} disabled={busy === pr.id} onClick={() => void act(pr.id, () => backend.setPullRequestWatch(pr.id, !pr.watch))}>
            <span className="pr-watch-track" aria-hidden="true"><span /></span>{t('Vigiar revisão')}</button>}
        </header>
        {open && pr.watch && <div className={`pr-watch-status${pr.checking ? ' is-checking' : ''}`}>
          {pr.checking ? <><LoaderCircle className="pr-spin" aria-hidden="true" /><span>{t('A IA está lendo os comentários e corrigindo o que for pedido…')}</span></>
            : <><Eye aria-hidden="true" /><span>{pr.nextCheckAt ? t('A cada 10 minutos a IA confere comentários novos, corrige no mesmo branch, roda os testes, faz push e responde. Nunca faz merge. Próxima conferência às {time}.', { time: time(pr.nextCheckAt) }) : t('A cada 10 minutos a IA confere comentários novos, corrige no mesmo branch, roda os testes, faz push e responde. Nunca faz merge. Conferindo em instantes.')}</span></>}
          <button type="button" className="touch-target text-button" disabled={busy === pr.id || pr.checking || !full} onClick={() => void act(pr.id, () => backend.checkPullRequestNow(pr.id))}><MessageSquareReply aria-hidden="true" />{t('Conferir agora')}</button>
        </div>}
        {open && pr.watch && !full && <div className="pr-watch-access" role="alert"><ShieldAlert aria-hidden="true" /><p>{t('Para a IA corrigir e fazer push sozinha, o projeto precisa de Acesso total. Ative aqui:')}</p>{permissionControl}</div>}
        {recent.length > 0 && <ol className="pr-notes" aria-label={t('O que aconteceu neste PR')}>{recent.map((event, index) => { const Icon = eventIcons[event.kind] ?? CircleDot; return <li key={index} className={`pr-note is-${event.kind}`}><header><Icon aria-hidden="true" /><time dateTime={event.at}>{time(event.at)}</time></header><span>{event.summary}</span></li> })}</ol>}
      </article>
    })}
    {error && <p className="form-error" role="alert">{error}</p>}
  </section>
}
