import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { TerminalOutput } from '../../lib/backend'
import { createFakeBackend } from '../../test/fakeBackend'

const written: (string | Uint8Array)[] = []
let typeInto: ((data: string) => void) | undefined
vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    cols = 80; rows = 24
    loadAddon() { /* fit */ }
    open() { /* jsdom has no canvas */ }
    write(data: string | Uint8Array) { written.push(data) }
    onData(listener: (data: string) => void) { typeInto = listener; return { dispose() { typeInto = undefined } } }
    onResize() { return { dispose() { /* none */ } } }
    focus() { /* none */ }
    dispose() { /* none */ }
  },
}))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { fit() { /* none */ } } }))
vi.mock('@xterm/xterm/css/xterm.css', () => ({}))
globalThis.ResizeObserver ??= class { observe() { /* none */ } disconnect() { /* none */ } unobserve() { /* none */ } } as never

afterEach(() => { cleanup(); written.length = 0 })

describe('terminal panel', () => {
  it('opens the shell in the project, shows its output, sends what is typed and offers a new one when it ends', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    let emit: ((output: TerminalOutput) => void) | undefined
    backend.onTerminalOutput = listener => { emit = listener; return () => undefined }
    const start = vi.spyOn(backend, 'startTerminal'), write = vi.spyOn(backend, 'writeTerminal'), close = vi.spyOn(backend, 'closeTerminal')
    const { TerminalPanel } = await import('./TerminalPanel')
    render(<TerminalPanel backend={backend} workspace={{ id: 'workspace-9', path: '/Users/dev/cobranca', profile: 'ask' }} onClose={() => undefined} visible />)
    await waitFor(() => expect(start).toHaveBeenCalledWith('workspace-9', 80, 24))
    expect(await screen.findByText(/zsh · cobranca/)).toBeInTheDocument()
    emit?.({ id: 'term-1', data: btoa('ola do shell') })
    await waitFor(() => expect(written.some(chunk => chunk instanceof Uint8Array && new TextDecoder().decode(chunk) === 'ola do shell')).toBe(true))
    typeInto?.('ls\r')
    expect(write).toHaveBeenCalledWith('term-1', 'ls\r')
    emit?.({ id: 'term-1', exited: true, code: 0 })
    expect(await screen.findByText('O shell foi encerrado.')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Abrir um novo' }))
    await waitFor(() => expect(start).toHaveBeenCalledTimes(2))
    expect(close).not.toHaveBeenCalled()
  })

  it('asks for a project when none is open', async () => {
    const { backend } = createFakeBackend()
    const { TerminalPanel } = await import('./TerminalPanel')
    render(<TerminalPanel backend={backend} onClose={() => undefined} visible />)
    expect(screen.getByText('Abra um projeto para usar o terminal na pasta dele.')).toBeInTheDocument()
  })
})
