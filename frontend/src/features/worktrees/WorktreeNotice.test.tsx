import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { WorktreeCleanup } from './cleanup'
import { WorktreeNotice } from './WorktreeNotice'

afterEach(cleanup)

const cleanupOf = (overrides: Partial<WorktreeCleanup> = {}): WorktreeCleanup => ({
  workspaceId: 'workspace-1', path: '/synthetic/worktrees/relatorios', name: 'feature/relatorios', branch: 'feature/relatorios', base: 'main', sessionId: 'session-1',
  deleteWhenSaved: true, deleteBranch: true, acknowledgeIgnored: true, phase: 'saving', message: 'A IA está salvando feature/relatorios na conversa.', ...overrides,
})

function show(overrides: Partial<WorktreeCleanup> = {}, handlers: { open?: () => void; check?: () => void; dismiss?: () => void; showManage?: boolean } = {}) {
  return render(<WorktreeNotice cleanup={cleanupOf(overrides)} showManage={handlers.showManage ?? true} onOpenWorktrees={handlers.open ?? (() => undefined)}
    onCheckCleanup={handlers.check ?? (() => undefined)} onDismissCleanup={handlers.dismiss ?? (() => undefined)} />)
}

describe('Worktree save notice', () => {
  it('follows the assistant while it works and lets the user check early', async () => {
    const user = userEvent.setup()
    const check = vi.fn()
    show({}, { check })
    const notice = screen.getByRole('status', { name: 'Salvamento do worktree' })
    expect(notice).toHaveTextContent('A IA está salvando feature/relatorios na conversa.')
    await user.click(within(notice).getByRole('button', { name: 'Conferir agora' }))
    expect(check).toHaveBeenCalledTimes(1)
    expect(within(notice).queryByRole('button', { name: 'Fechar aviso' })).not.toBeInTheDocument()
    expect(within(notice).queryByRole('button', { name: 'Abrir Worktrees' })).not.toBeInTheDocument()
  })

  it('has no buttons while the app is checking', () => {
    show({ phase: 'checking', message: 'Conferindo se tudo foi salvo e mesclado…' })
    const notice = screen.getByRole('status', { name: 'Salvamento do worktree' })
    expect(notice).toHaveTextContent('Conferindo se tudo foi salvo e mesclado…')
    expect(within(notice).queryByRole('button')).not.toBeInTheDocument()
  })

  it('raises an alert with the way forward when something is still pending', async () => {
    const user = userEvent.setup()
    const check = vi.fn(), open = vi.fn(), dismiss = vi.fn()
    show({ phase: 'attention', message: 'Ainda não dá para excluir feature/relatorios: 3 alterações não salvas. Nada foi excluído.' }, { check, open, dismiss })
    const notice = screen.getByRole('alert', { name: 'Salvamento do worktree' })
    expect(notice).toHaveTextContent('Nada foi excluído.')
    await user.click(within(notice).getByRole('button', { name: 'Conferir agora' }))
    await user.click(within(notice).getByRole('button', { name: 'Abrir Worktrees' }))
    await user.click(within(notice).getByRole('button', { name: 'Fechar aviso' }))
    expect([check.mock.calls.length, open.mock.calls.length, dismiss.mock.calls.length]).toEqual([1, 1, 1])
  })

  it('offers the page once everything is saved but the worktree was kept', async () => {
    const user = userEvent.setup()
    const open = vi.fn()
    show({ phase: 'saved', message: 'Tudo salvo e mesclado em main. feature/relatorios já pode ser excluído na página Worktrees.' }, { open })
    const notice = screen.getByRole('status', { name: 'Salvamento do worktree' })
    expect(within(notice).queryByRole('button', { name: 'Conferir agora' })).not.toBeInTheDocument()
    await user.click(within(notice).getByRole('button', { name: 'Abrir Worktrees' }))
    expect(open).toHaveBeenCalledTimes(1)
  })

  it('does not point to the page while that page is already open', () => {
    show({ phase: 'attention', message: 'Ainda não dá para excluir.' }, { showManage: false })
    expect(screen.queryByRole('button', { name: 'Abrir Worktrees' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Conferir agora' })).toBeInTheDocument()
  })

  it('only offers to close a notice about a worktree that is gone', async () => {
    const user = userEvent.setup()
    const dismiss = vi.fn()
    show({ phase: 'deleted', message: 'Worktree feature/relatorios excluído.' }, { dismiss })
    const notice = screen.getByRole('status', { name: 'Salvamento do worktree' })
    expect(within(notice).getAllByRole('button').map(button => button.getAttribute('aria-label') ?? button.textContent)).toEqual(['Fechar aviso'])
    await user.click(within(notice).getByRole('button', { name: 'Fechar aviso' }))
    expect(dismiss).toHaveBeenCalledTimes(1)
  })
})
