import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { Backend, ChatCommandEvent } from '../../lib/backend'
import { errorMessage } from '../../lib/backend'
import type { CommandRun, CommandRunner } from '../../components/commandRunner'

const maxShownOutput = 1024 * 1024
const promptHead = 2000
const promptTail = 10000

/** What a finished command tells the agent. It is the agent's prompt, so it stays in Portuguese like the others. */
export function commandResultPrompt(command: string, event: Pick<ChatCommandEvent, 'exitCode' | 'timedOut' | 'truncated'>, output: string): string {
  const text = output.length > promptHead + promptTail ? `${output.slice(0, promptHead)}\n[… ${output.length - promptHead - promptTail} caracteres omitidos …]\n${output.slice(-promptTail)}` : output
  const notes = [`código de saída ${event.exitCode}`, event.timedOut ? 'tempo esgotado' : '', event.truncated ? 'saída cortada em 1 MiB' : ''].filter(Boolean).join(', ')
  const fence = text.includes('```') ? '````' : '```'
  return `Executei no projeto o comando que você sugeriu:\n${fence}bash\n${command}\n${fence}\nResultado: ${notes}.\nSaída:\n${fence}\n${text.trimEnd() || '(sem saída)'}\n${fence}\nContinue a partir desse resultado.`
}

/**
 * Runs the commands the person clicks in the chat's code blocks and follows their output. When one ends, its result goes
 * to the agent (or into the message box, when the agent is busy), so the conversation goes on from it.
 */
export function useChatCommands(backend: Backend, sessionId: string | undefined, canRun: boolean, deliver: (prompt: string) => 'sent' | 'draft'): CommandRunner {
  const [runs, setRuns] = useState<Record<string, CommandRun>>({})
  const byId = useRef(new Map<string, string>())
  // What each run printed so far, kept outside React state so the end can read it at once.
  const printed = useRef(new Map<string, string>())
  const deliverRef = useRef(deliver)
  deliverRef.current = deliver
  const session = useRef(sessionId)
  session.current = sessionId

  useEffect(() => { setRuns({}); byId.current.clear(); printed.current.clear(); early.current.clear() }, [sessionId])
  const update = useCallback((command: string, change: (run: CommandRun) => CommandRun) => setRuns(current => current[command] ? { ...current, [command]: change(current[command]) } : current), [])

  // A quick command can end before its id comes back from the backend: its events wait here until then.
  const early = useRef(new Map<string, ChatCommandEvent[]>())
  const handle = useRef<(event: ChatCommandEvent) => void>(() => undefined)
  useEffect(() => backend.onChatCommand(event => handle.current(event)), [backend])
  handle.current = event => {
    if (event.sessionId !== session.current) return
    const command = byId.current.get(event.id)
    if (command === undefined) {
      const waiting = early.current.get(event.id) ?? []
      if (waiting.length < 1000) early.current.set(event.id, [...waiting, event])
      return
    }
    if (!event.done) {
      const output = ((printed.current.get(event.id) ?? '') + (event.text ?? '')).slice(-maxShownOutput)
      printed.current.set(event.id, output)
      update(command, run => run.id !== event.id ? run : { ...run, status: 'running', output })
      return
    }
    byId.current.delete(event.id)
    const output = event.output ?? printed.current.get(event.id) ?? ''
    printed.current.delete(event.id)
    const delivered = event.cancelled ? undefined : deliverRef.current(commandResultPrompt(command, event, output))
    update(command, run => run.id !== event.id ? run : { ...run, status: event.cancelled ? 'cancelled' : 'done', exitCode: event.exitCode, output, delivered })
  }

  const run = useCallback((command: string) => {
    const id = sessionId
    if (!id || !canRun) return
    setRuns(current => ({ ...current, [command]: { id: '', command, status: 'starting', output: '' } }))
    backend.runChatCommand(id, command).then(runId => {
      if (session.current !== id) return
      byId.current.set(runId, command)
      update(command, existing => existing.status === 'starting' ? { ...existing, id: runId, status: 'running' } : existing)
      const waiting = early.current.get(runId) ?? []
      early.current.delete(runId)
      for (const event of waiting) handle.current(event)
    }, failure => update(command, existing => ({ ...existing, status: 'failed', error: errorMessage(failure) })))
  }, [backend, sessionId, canRun, update])

  const cancel = useCallback((command: string) => {
    const current = runs[command]
    if (current?.id) void backend.cancelChatCommand(current.id).catch(() => undefined)
  }, [backend, runs])

  return useMemo(() => ({ canRun: !!sessionId && canRun, runs, run, cancel }), [sessionId, canRun, runs, run, cancel])
}
