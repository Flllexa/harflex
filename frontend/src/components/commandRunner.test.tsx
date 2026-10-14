import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Markdown } from './Markdown'
import { CommandRunnerContext, commandOf, isShellBlock, type CommandRunner } from './commandRunner'
import { commandResultPrompt } from '../features/workbench/useChatCommands'

afterEach(cleanup)

describe('the command a code block holds', () => {
  it('runs shell blocks only, and takes prompts and printed output out of a console session', () => {
    expect(isShellBlock('bash')).toBe(true)
    expect(isShellBlock('Zsh')).toBe(true)
    expect(isShellBlock('js')).toBe(false)
    expect(isShellBlock('')).toBe(false)
    expect(commandOf('bash', 'make update-report-apply DATE=2026-10-14 \\\n  CONFIRM=YES')).toBe('make update-report-apply DATE=2026-10-14 \\\n  CONFIRM=YES')
    expect(commandOf('console', '$ npm test\n> harflex@0.2.8 test\nok')).toBe('npm test')
    expect(commandOf('bash', '$ ls\n$ pwd')).toBe('ls\npwd')
  })
  it('tells the agent the command, the exit code and the output, cutting the middle of a long output', () => {
    const prompt = commandResultPrompt('make test', { exitCode: 2, timedOut: false, truncated: false }, 'linha\n'.repeat(5000))
    expect(prompt).toContain('```bash\nmake test\n```')
    expect(prompt).toContain('código de saída 2')
    expect(prompt).toContain('caracteres omitidos')
    expect(prompt.length).toBeLessThan(14000)
    expect(commandResultPrompt('ls', { exitCode: 0, timedOut: false, truncated: false }, '')).toContain('(sem saída)')
    expect(commandResultPrompt('cat x.md', { exitCode: 0, timedOut: false, truncated: false }, 'a\n```js\nb\n```')).toContain('````\na\n```js')
  })
})

function runner(overrides: Partial<CommandRunner> = {}): CommandRunner {
  return { canRun: true, runs: {}, run: vi.fn(), cancel: vi.fn(), ...overrides }
}
const text = 'Rode isto:\n\n```bash\nmake test\n```\n\nE veja:\n\n```js\nconsole.log(1)\n```'

describe('code blocks in the chat', () => {
  it('offers Run on shell blocks next to Copy, and runs the command', async () => {
    const value = runner()
    render(<CommandRunnerContext.Provider value={value}><Markdown text={text} /></CommandRunnerContext.Provider>)
    const run = screen.getAllByRole('button', { name: 'Executar o comando no projeto' })
    expect(run).toHaveLength(1)
    expect(screen.getAllByRole('button', { name: /Copiar código/ })).toHaveLength(2)
    await userEvent.click(run[0])
    expect(value.run).toHaveBeenCalledWith('make test')
  })
  it('shows the output while it runs, lets it stop, and says where the result went', async () => {
    const running = runner({ runs: { 'make test': { id: 'r1', command: 'make test', status: 'running', output: 'compilando…' } } })
    const view = render(<CommandRunnerContext.Provider value={running}><Markdown text={text} /></CommandRunnerContext.Provider>)
    expect(screen.getByRole('region', { name: 'Saída do comando' })).toHaveTextContent('compilando…')
    expect(screen.getByRole('status')).toHaveTextContent('Executando no projeto…')
    await userEvent.click(screen.getByRole('button', { name: 'Parar o comando' }))
    expect(running.cancel).toHaveBeenCalledWith('make test')
    view.rerender(<CommandRunnerContext.Provider value={runner({ runs: { 'make test': { id: 'r1', command: 'make test', status: 'done', exitCode: 2, output: 'FAIL', delivered: 'sent' } } })}><Markdown text={text} /></CommandRunnerContext.Provider>)
    expect(screen.getByRole('status')).toHaveTextContent('Terminou com código 2 · resultado enviado ao agente')
    expect(screen.getByRole('button', { name: 'Executar o comando no projeto' })).toHaveTextContent('Executar de novo')
  })
  it('waits while the agent works, and only copies outside the chat', () => {
    const view = render(<CommandRunnerContext.Provider value={runner({ canRun: false })}><Markdown text={text} /></CommandRunnerContext.Provider>)
    expect(screen.getByRole('button', { name: 'Executar o comando no projeto' })).toBeDisabled()
    view.unmount()
    render(<Markdown text={text} />)
    expect(screen.queryByRole('button', { name: 'Executar o comando no projeto' })).not.toBeInTheDocument()
  })
})
