import { createContext, useContext } from 'react'

/** A command the person ran from a code block of the chat, and what it printed. */
export type CommandRun = {
  id: string
  command: string
  status: 'starting' | 'running' | 'done' | 'cancelled' | 'failed'
  output: string
  exitCode?: number
  error?: string
  /** Where the result went: straight to the agent, or into the message box when the agent was busy. */
  delivered?: 'sent' | 'draft'
}

export type CommandRunner = {
  /** Whether a command can run now (a chat is open and its agent is not working). */
  canRun: boolean
  runs: Record<string, CommandRun>
  run: (command: string) => void
  cancel: (command: string) => void
}

/** Present only where code blocks can be run: the chat. Elsewhere the blocks only copy. */
export const CommandRunnerContext = createContext<CommandRunner | undefined>(undefined)
export const useCommandRunner = () => useContext(CommandRunnerContext)

const shells = new Set(['bash', 'sh', 'shell', 'zsh', 'console', 'terminal', 'shellsession', 'sh-session', 'fish', 'powershell', 'ps1', 'pwsh'])
export const isShellBlock = (lang: string) => shells.has(lang.trim().toLowerCase())

/** The command a shell block holds: prompts ("$ ", "% ") taken off, output lines of a console session left out. */
export function commandOf(lang: string, value: string): string {
  const lines = value.replace(/\r\n/g, '\n').split('\n')
  const prompted = lines.filter(line => /^\s*[$%]\s/.test(line))
  const session = ['console', 'shellsession', 'sh-session', 'terminal'].includes(lang.trim().toLowerCase()) || prompted.length > 0 && prompted.length < lines.filter(line => line.trim()).length
  const kept = session ? prompted : lines
  return kept.map(line => line.replace(/^\s*[$%]\s/, '')).join('\n').trim()
}
