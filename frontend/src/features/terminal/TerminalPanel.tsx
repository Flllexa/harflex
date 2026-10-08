import { useEffect, useRef, useState } from 'react'
import { RotateCcw, SquareTerminal, X } from 'lucide-react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { errorMessage, type Backend, type TerminalOutput, type Workspace } from '../../lib/backend'
import './terminal.css'

type Props = { backend: Backend; workspace?: Workspace; onClose: () => void; visible: boolean }

/** A project's shell lives on while the panel is closed; its recent output is kept to draw it again. */
type Session = { id: string; shell: string; chunks: Uint8Array[]; bytes: number; exitCode?: number }
const maxBufferedBytes = 2 * 1024 * 1024
const sessions = new Map<string, Session>()
const listeners = new Set<(output: TerminalOutput, bytes?: Uint8Array) => void>()
let subscribed: Backend | undefined

function decode(data: string): Uint8Array {
  const binary = atob(data)
  const bytes = new Uint8Array(binary.length)
  for (let index = 0; index < binary.length; index++) bytes[index] = binary.charCodeAt(index)
  return bytes
}

// One subscription for the whole app: output is kept per terminal even when no panel is showing it.
function subscribe(backend: Backend) {
  if (subscribed === backend) return
  subscribed = backend
  backend.onTerminalOutput(output => {
    const session = [...sessions.values()].find(item => item.id === output.id)
    let bytes: Uint8Array | undefined
    if (session && output.data) {
      bytes = decode(output.data)
      session.chunks.push(bytes)
      session.bytes += bytes.length
      while (session.bytes > maxBufferedBytes && session.chunks.length > 1) session.bytes -= session.chunks.shift()!.length
    }
    if (session && output.exited) session.exitCode = output.code ?? 0
    for (const listener of listeners) listener(output, bytes)
  })
}

const theme = {
  background: '#0a0d0f', foreground: '#e6edf0', cursor: '#7ef0b0', cursorAccent: '#0a0d0f', selectionBackground: '#2b4b5a',
  black: '#0f1418', red: '#ff7a7a', green: '#7ef0b0', yellow: '#f2c94c', blue: '#5ab0ff', magenta: '#c792ea', cyan: '#5fd7e6', white: '#d9e2e6',
  brightBlack: '#5c6b73', brightRed: '#ff9d9d', brightGreen: '#a8f5c8', brightYellow: '#f7dc85', brightBlue: '#8cc8ff', brightMagenta: '#dcb5f5', brightCyan: '#8ee6f0', brightWhite: '#ffffff',
}

export function TerminalPanel({ backend, workspace, onClose, visible }: Props) {
  const host = useRef<HTMLDivElement>(null)
  const [state, setState] = useState<'starting' | 'ready' | 'exited' | 'error'>('starting')
  const [error, setError] = useState('')
  const [restart, setRestart] = useState(0)
  const [shell, setShell] = useState('')
  const terminal = useRef<Terminal>()
  const fit = useRef<FitAddon>()

  useEffect(() => {
    if (!workspace || !host.current) return
    subscribe(backend)
    let live = true
    const term = new Terminal({ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', fontSize: 12, lineHeight: 1.25, cursorBlink: true, theme, allowProposedApi: false, scrollback: 5000 })
    const fitAddon = new FitAddon()
    term.loadAddon(fitAddon)
    term.open(host.current)
    terminal.current = term
    fit.current = fitAddon
    try { fitAddon.fit() } catch { /* The panel may not be laid out yet. */ }
    const key = workspace.id
    let session = sessions.get(key)
    const attach = (current: Session) => {
      for (const chunk of current.chunks) term.write(chunk)
      setShell(current.shell)
      setState(current.exitCode === undefined ? 'ready' : 'exited')
    }
    const listener = (output: TerminalOutput, bytes?: Uint8Array) => {
      const current = sessions.get(key)
      if (!current || output.id !== current.id) return
      if (bytes) term.write(bytes)
      if (output.exited) setState('exited')
    }
    listeners.add(listener)
    const typed = term.onData(data => { const current = sessions.get(key); if (current && current.exitCode === undefined) void backend.writeTerminal(current.id, data).catch(() => undefined) })
    const resized = term.onResize(({ cols, rows }) => { const current = sessions.get(key); if (current && current.exitCode === undefined) void backend.resizeTerminal(current.id, cols, rows).catch(() => undefined) })
    if (session && session.exitCode === undefined) attach(session)
    else {
      setState('starting'); setError('')
      void backend.startTerminal(workspace.id, term.cols, term.rows).then(info => {
        session = { id: info.id, shell: info.shell, chunks: [], bytes: 0 }
        sessions.set(key, session)
        if (live) attach(session)
      }, failure => { if (live) { setError(errorMessage(failure)); setState('error') } })
    }
    const observer = new ResizeObserver(() => { try { fitAddon.fit() } catch { /* Hidden. */ } })
    observer.observe(host.current)
    return () => { live = false; listeners.delete(listener); typed.dispose(); resized.dispose(); observer.disconnect(); term.dispose(); terminal.current = undefined }
  }, [backend, workspace?.id, restart])

  // Showing the tab again: fit to the panel and give the shell the keyboard.
  useEffect(() => {
    if (!visible) return
    const timer = window.setTimeout(() => { try { fit.current?.fit() } catch { /* Not laid out. */ } terminal.current?.focus() }, 30)
    return () => window.clearTimeout(timer)
  }, [visible])

  function newShell() {
    if (!workspace) return
    const current = sessions.get(workspace.id)
    if (current && current.exitCode === undefined) void backend.closeTerminal(current.id).catch(() => undefined)
    sessions.delete(workspace.id)
    setRestart(value => value + 1)
  }

  const name = workspace ? workspace.path.split(/[\\/]/).filter(Boolean).pop() : ''
  return <aside className="terminal-panel" aria-label="Terminal" hidden={!visible}>
    <div className="panel-heading terminal-heading">
      <div className="terminal-title"><SquareTerminal aria-hidden="true" /><div><h2>Terminal</h2>{workspace && <span className="muted mono" title={workspace.path}>{shell ? `${shell.split('/').pop()} · ` : ''}{name}</span>}</div></div>
      <div className="terminal-actions">
        {workspace && <button type="button" className="touch-target icon-button" aria-label="Novo terminal" title="Novo terminal" onClick={newShell}><RotateCcw aria-hidden="true" /></button>}
        <button type="button" className="touch-target icon-button" aria-label="Fechar painel" onClick={onClose}><X aria-hidden="true" /></button>
      </div>
    </div>
    {!workspace && <p className="muted terminal-note">Abra um projeto para usar o terminal na pasta dele.</p>}
    {state === 'starting' && workspace && <p className="muted terminal-note" role="status">Abrindo o shell…</p>}
    {state === 'error' && <div className="terminal-note" role="alert"><span className="form-error">{error}</span><button type="button" className="touch-target text-button" onClick={newShell}>Tentar de novo</button></div>}
    {state === 'exited' && <div className="terminal-note" role="status"><span className="muted">O shell foi encerrado.</span><button type="button" className="touch-target text-button" onClick={newShell}>Abrir um novo</button></div>}
    {workspace && <div ref={host} className="terminal-host" />}
  </aside>
}
