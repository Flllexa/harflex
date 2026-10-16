import { useMemo, type ReactNode } from 'react'
import { Check, ChevronRight, CircleAlert, CircleDashed, Coins, FileText, FilePen, LoaderCircle, Timer } from 'lucide-react'
import type { AgentEvent, Backend } from '../../lib/backend'
import { localeTag, useT } from '../../i18n'
import { shortPath, type Action } from './activityFlow'
import { buildRunProgress, formatCount, formatElapsed, type RunProgress as Progress } from './runProgress'
import { useNow, useSessionJournal } from './useSessionJournal'

type Translate = ReturnType<typeof useT>

function actionSummary(progress: Progress, t: Translate): string[] {
  const { counts } = progress
  const parts: string[] = []
  if (counts.read) parts.push(t(counts.read === 1 ? '{count} leitura' : '{count} leituras', { count: counts.read }))
  if (counts.search) parts.push(t(counts.search === 1 ? '{count} busca' : '{count} buscas', { count: counts.search }))
  if (counts.shell) parts.push(t(counts.shell === 1 ? '{count} comando' : '{count} comandos', { count: counts.shell }))
  if (counts.write) parts.push(t(counts.write === 1 ? '{count} alteração' : '{count} alterações', { count: counts.write }))
  const other = counts.web + counts.tool + counts.agent
  if (other) parts.push(t(other === 1 ? '{count} outra ação' : '{count} outras ações', { count: other }))
  if (counts.failed) parts.push(t(counts.failed === 1 ? '{count} falhou' : '{count} falharam', { count: counts.failed }))
  return parts
}

function tokenText(progress: Progress, t: Translate) {
  const locale = localeTag()
  if (progress.tokens) return t('{input} entrada · {output} saída', { input: formatCount(progress.tokens.input, locale), output: formatCount(progress.tokens.output, locale) })
  return progress.live ? t('informados ao final') : t('não informados pelo provedor')
}

function ActionIcon({ action }: { action: Action }) {
  if (action.status === 'running') return <LoaderCircle className="run-progress-spin" aria-hidden="true" />
  if (action.status === 'done') return <Check aria-hidden="true" />
  if (action.status === 'failed') return <CircleAlert aria-hidden="true" />
  return <CircleDashed aria-hidden="true" />
}

function FileList({ title, files, Icon }: { title: string; files: string[]; Icon: typeof FileText }) {
  const t = useT()
  if (!files.length) return null
  const shown = files.slice(-12)
  return <div className="run-progress-files">
    <span className="run-progress-label"><Icon aria-hidden="true" />{title} <span className="muted">({files.length})</span></span>
    <ul>{shown.map(path => <li key={path}><code className="mono" title={path}>{shortPath(path)}</code></li>)}</ul>
    {files.length > shown.length && <span className="muted">{t('e mais {count}', { count: files.length - shown.length })}</span>}
  </div>
}

type Props = {
  progress: Progress
  /** What the run is for, ahead of its step: "SPEC", "Leitura do projeto". */
  title?: string
  /** A finished run is shown only when asked; by default the strip shows while the run lives. */
  showFinished?: boolean
  /** Leaves the step out where the screen already tells it (Code, QA): only time, actions, files and tokens. */
  metricsOnly?: boolean
  className?: string
}

/**
 * Objective progress of an AI run: the step it is on, the elapsed time, what it has done, the files it read or
 * changed and the tokens the provider reported. It shows actions and lifecycle only, never the model's reasoning.
 */
export function RunProgress({ progress, title, showFinished = false, metricsOnly = false, className = '' }: Props) {
  const t = useT()
  const now = useNow(progress.live)
  if (!progress.live && !showFinished) return null
  if (!progress.startedAt && !progress.live) return null
  const elapsed = progress.startedAt !== undefined ? formatElapsed((progress.live ? now : progress.endedAt ?? now) - progress.startedAt) : undefined
  const summary = actionSummary(progress, t)
  // Code and QA list their actions already; there the details keep only the files.
  const recent = metricsOnly ? [] : progress.recent
  const hasDetails = recent.length > 0 || progress.filesRead.length > 0 || progress.filesChanged.length > 0
  const state = progress.live ? progress.phase === 'approval' ? 'is-approval' : 'is-live' : progress.phase === 'completed' ? 'is-done' : 'is-failed'
  return <section className={`run-progress ${state}${metricsOnly ? ' is-metrics' : ''} ${className}`.trim()} aria-label={t('Progresso da IA')}>
    {!metricsOnly && <div className="run-progress-head">
      <span className="run-progress-indicator" aria-hidden="true" />
      {/* Only the step is announced; the ticking clock would speak every second. */}
      <span className="run-progress-step" role="status" aria-live="polite" aria-atomic="true">{title && <span className="run-progress-title">{title} · </span>}<strong>{progress.step}</strong></span>
      {elapsed && <span className="run-progress-elapsed" title={t('Tempo decorrido')}><Timer aria-hidden="true" /><span className="visually-hidden">{t('Tempo decorrido')}: </span>{elapsed}</span>}
    </div>}
    {!metricsOnly && progress.planStep && <p className="run-progress-plan">{t('Etapa {index} de {total}: {text}', { index: progress.planStep.index + 1, total: progress.planStep.total, text: progress.planStep.text })}</p>}
    <p className="run-progress-meta">
      {metricsOnly && elapsed && <span className="run-progress-elapsed" title={t('Tempo decorrido')}><Timer aria-hidden="true" /><span className="visually-hidden">{t('Tempo decorrido')}: </span>{elapsed}</span>}
      <span>{summary.length ? summary.join(' · ') : progress.live ? t('Nenhuma ação ainda') : t('Nenhuma ação')}</span>
      {progress.filesRead.length > 0 && <span>{t(progress.filesRead.length === 1 ? '{count} arquivo consultado' : '{count} arquivos consultados', { count: progress.filesRead.length })}</span>}
      {progress.live && !progress.tokens && progress.outputChars > 0 && <span>{t('{count} caracteres recebidos', { count: formatCount(progress.outputChars, localeTag()) })}</span>}
      <span className="run-progress-tokens"><Coins aria-hidden="true" />{t('Tokens: {value}', { value: tokenText(progress, t) })}</span>
    </p>
    {hasDetails && <details className="run-progress-details">
      <summary><ChevronRight aria-hidden="true" />{t('Detalhes da atividade')}</summary>
      {recent.length > 0 && <div className="run-progress-recent">
        <span className="run-progress-label">{t('Últimas ações')}</span>
        <ol>{recent.map(action => <li key={action.id} className={`is-${action.status}`}><ActionIcon action={action} /><span>{action.label}{action.kind === 'shell' && action.detail && <code className="mono">{action.detail}</code>}</span></li>)}</ol>
      </div>}
      <FileList title={t('Arquivos consultados')} files={progress.filesRead} Icon={FileText} />
      <FileList title={t('Arquivos alterados')} files={progress.filesChanged} Icon={FilePen} />
    </details>}
  </section>
}

/** The progress of a session's latest run, read from its journal; nothing while there is no session. */
export function SessionRunProgress({ backend, sessionId, title, showFinished, metricsOnly, className, fallback = null }: { backend: Backend; sessionId?: string; fallback?: ReactNode } & Omit<Props, 'progress'>) {
  const events = useSessionJournal(backend, sessionId)
  const progress = useMemo(() => buildRunProgress(events), [events])
  if (!sessionId || !events.length) return <>{fallback}</>
  return <RunProgress progress={progress} title={title} showFinished={showFinished} metricsOnly={metricsOnly} className={className} />
}

export function useRunProgress(events: AgentEvent[]) {
  return useMemo(() => buildRunProgress(events), [events])
}
