import { useEffect, useMemo, useState } from 'react'
import { Check, CircleAlert, CircleDashed, FolderSearch, Hammer, LoaderCircle, MonitorPlay, Package, Play, ScanSearch, SquareTerminal, TestTube2 } from 'lucide-react'
import type { AgentEvent, Backend } from '../../lib/backend'
import { buildActivityFlow, type ActionStatus } from '../../components/activity/activityFlow'
import { useT } from '../../i18n'

type Translate = ReturnType<typeof useT>

type Kind = 'install' | 'inspect' | 'build' | 'unit' | 'e2e' | 'run' | 'lint' | 'other'

/** The labels of each kind of check, read when shown so the language on screen is the one used. */
function kindsFor(translate: Translate): Record<Kind, { label: string; Icon: typeof Hammer }> {
  return {
    install: { label: translate('Instalação de dependências'), Icon: Package },
    inspect: { label: translate('Inspecionando o projeto'), Icon: FolderSearch },
    build: { label: 'Build', Icon: Hammer },
    unit: { label: translate('Testes unitários'), Icon: TestTube2 },
    e2e: { label: 'E2E', Icon: MonitorPlay },
    run: { label: translate('App no ar'), Icon: Play },
    lint: { label: 'Lint', Icon: ScanSearch },
    other: { label: translate('Comando'), Icon: SquareTerminal },
  }
}
// The checks a QA run is expected to reach; the ones not seen yet wait at the end of the list.
const expected: Kind[] = ['build', 'unit', 'e2e', 'run', 'lint']
function statusTextFor(translate: Translate): Record<ActionStatus, string> {
  return { waiting: translate('Na fila'), running: translate('Rodando'), approval: translate('Aguardando'), done: translate('Concluiu'), failed: translate('Falhou') }
}

const inspection = /^(cd|pwd|ls|cat|nl|head|tail|find|grep|rg|sed -n|wc|tree|command -v|which|type|echo|printf|true|stat|file|ps|node (-v|--version)|npm (-v|--version)|git (status|log|diff|show)|mkdir)\b/

// What one part of a command does. Reading around (listing, printing, checking what is installed) is inspection,
// whatever names it mentions; otherwise the first match wins, so E2E comes before unit tests.
function partKind(part: string): Kind {
  if (inspection.test(part)) return 'inspect'
  if (/\b(npm|pnpm|yarn|bun)\s+(i|install|ci|add)\b|pip3? install|go mod download|bundle install|playwright install/.test(part)) return 'install'
  if (/playwright|cypress|puppeteer|e2e|chromium|webdriver/.test(part)) return 'e2e'
  if (/\b(lint|eslint|golangci-lint|go vet|ruff|flake8|stylelint)\b|prettier --check|tsc --noemit|node --check/.test(part)) return 'lint'
  if (/\b(test|tests|vitest|jest|pytest|mocha|go test|cargo test)\b|node --test/.test(part)) return 'unit'
  if (/serve\b|http-server|http\.server|createserver|\bcurl\b|\bwget\b|localhost|127\.0\.0\.1|npm (run )?(start|dev)|\bvite\b/.test(part)) return 'run'
  if (/\b(build|go build|cargo build|webpack|tsc)\b|^make\b/.test(part)) return 'build'
  return 'other'
}

// The parts of a shell command: split on ; && || | and new lines outside quotes, with heredoc bodies left out.
function commandParts(command: string): string[] {
  const text = command.replace(/<<-?\s*(['"]?)(\w+)\1[^\n]*\n[\s\S]*?\n\s*\2\s*(?=\n|$)/g, '')
  const parts: string[] = []
  let current = '', quote = ''
  for (let index = 0; index < text.length; index++) {
    const char = text[index]
    if (quote) { if (char === quote) quote = ''; current += char; continue }
    if (char === "'" || char === '"') { quote = char; current += char; continue }
    if (char === ';' || char === '|' || char === '&' || char === '\n') { parts.push(current); current = ''; continue }
    current += char
  }
  parts.push(current)
  return parts.map(item => item.trim().replace(/^\(+/, '')).filter(Boolean)
}

/** What a shell command checks, part by part, in the order the parts run; inspection counts only when nothing else runs. */
export function commandKinds(command: string): Kind[] {
  const found: Kind[] = []
  for (const part of commandParts(command.toLowerCase())) {
    const kind = partKind(part)
    if (!found.includes(kind)) found.push(kind)
  }
  const checks = found.filter(kind => kind !== 'inspect' && kind !== 'other')
  return checks.length ? checks : found.includes('inspect') ? ['inspect'] : ['other']
}

const journalTypes = /^(message\.(user|assistant)|tool\.(called|completed|failed|denied|skipped)|approval\.|run\.)/

// The session's journal without streamed output, kept to the first request so a later reminder does not clear the list.
export function useRunJournal(backend: Backend, sessionId?: string) {
  const [events, setEvents] = useState<AgentEvent[]>([])
  useEffect(() => {
    setEvents([])
    if (!sessionId) return
    let live = true
    const merge = (incoming: AgentEvent[]) => {
      const kept = incoming.filter(event => event.streamId === sessionId && journalTypes.test(event.type))
      if (kept.length) setEvents(current => [...new Map([...current, ...kept].map(event => [event.id, event])).values()].sort((a, b) => a.sequence - b.sequence))
    }
    const unsubscribe = backend.onEvent(event => { if (live) merge([event]) })
    void (async () => {
      let after = 0
      for (let page = 0; page < 50 && live; page++) {
        const found = await backend.listEvents(sessionId, after, 1000).catch(() => [] as AgentEvent[])
        if (!live) return
        merge(found)
        if (found.length < 1000) return
        after = found[found.length - 1].sequence
      }
    })()
    return () => { live = false; unsubscribe() }
  }, [backend, sessionId])
  return useMemo(() => {
    const first = events.findIndex(event => event.type === 'message.user')
    return events.filter((event, index) => event.type !== 'message.user' || index === first)
  }, [events])
}

/** The QA run as it happens: each command the QA runs in the lab, one after another, and the checks still to come. */
export function QALiveRun({ backend, sessionId }: { backend: Backend; sessionId?: string }) {
  const t = useT()
  const kinds = kindsFor(t)
  const statusText = statusTextFor(t)
  const journal = useRunJournal(backend, sessionId)
  const commands = useMemo(() => buildActivityFlow(journal).actions.filter(action => action.kind === 'shell').map(action => ({ ...action, kinds: commandKinds(action.detail ?? '') })), [journal])
  const seen = new Set(commands.flatMap(item => item.kinds))
  const upcoming = expected.filter(kind => !seen.has(kind))
  const current = commands.find(item => item.status === 'running')
  // Between commands the model is choosing the next one: the next check on the list shows the lab is still at work.
  const next = current ? undefined : upcoming[0]

  return <section className="qa-live" aria-label={t('QA em andamento')} aria-busy="true">
    <p className="qa-live-summary" aria-live="polite">{current ? <>{t('Agora:')} <strong>{current.kinds.map(kind => kinds[kind].label).join(' · ')}</strong></> : next ? <>{t('Preparando:')} <strong>{kinds[next].label}</strong></> : commands.length ? t('Pensando no próximo passo…') : t('Preparando o laboratório…')}
      {commands.length > 0 && <span className="muted"> · {t(commands.length === 1 ? '{count} comando' : '{count} comandos', { count: commands.length })}</span>}</p>
    <ol className="qa-run qa-live-run">
      {commands.map(item => { const { Icon } = kinds[item.kinds[0]]; const label = item.kinds.map(kind => kinds[kind].label).join(' · '); return <li key={item.id} className={`qa-run-row is-live is-${item.status}`}>
        <div className="qa-run-head">
          <span className="qa-run-status" aria-hidden="true">{item.status === 'running' ? <LoaderCircle className="qa-spin" /> : item.status === 'done' ? <Check /> : item.status === 'failed' ? <CircleAlert /> : <CircleDashed />}</span>
          <span className="qa-live-name"><strong>{label}</strong>{item.detail && <code className="mono">{item.detail}</code>}</span>
          <span className="qa-run-kind"><Icon aria-hidden="true" />{item.kinds[0] === 'other' ? 'Terminal' : kinds[item.kinds[0]].label}</span>
          <span className="qa-run-result">{statusText[item.status]}</span>
        </div>
      </li> })}
      {upcoming.map(kind => { const { label, Icon } = kinds[kind]; return <li key={kind} className={`qa-run-row is-live ${kind === next ? 'is-next' : 'is-upcoming'}`}>
        <div className="qa-run-head">
          <span className="qa-run-status" aria-hidden="true">{kind === next ? <LoaderCircle className="qa-spin" /> : <CircleDashed />}</span>
          <span className="qa-live-name"><strong>{label}</strong></span>
          <span className="qa-run-kind"><Icon aria-hidden="true" />{label}</span>
          <span className="qa-run-result">{kind === next ? t('Preparando…') : t('Ainda não começou')}</span>
        </div>
      </li> })}
    </ol>
  </section>
}
