import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Backend, BackendOption } from '../../lib/backend'
import { createFakeBackend } from '../../test/fakeBackend'
import { blocker, installWorktrees, lockedWorktree, mainWorktree, pendingWorktree, worktreeItem } from '../../test/worktreeFixture'
import { useWorktrees } from './useWorktrees'
import { WorktreeBar } from './WorktreeBar'

afterEach(cleanup)

const api: BackendOption = { id: 'local', name: 'Local', kind: 'api', available: true }

type HarnessProps = {
  backend: Backend
  workspaceId?: string
  showManage?: boolean
  onOpenWorktrees?: () => void
  onSaveWithAI?: () => Promise<void>
}

function Harness({ backend, workspaceId = 'workspace-1', showManage = true, onOpenWorktrees = () => undefined, onSaveWithAI = async () => undefined }: HarnessProps) {
  const worktrees = useWorktrees(backend, workspaceId)
  return <WorktreeBar backend={backend} workspaceId={workspaceId} worktrees={worktrees} backends={[api]} defaultBackendId="" showManage={showManage}
    onOpenWorktrees={onOpenWorktrees} onSettings={() => undefined} onSaveWithAI={onSaveWithAI as never} />
}

/** The open project lives inside a linked worktree: the backend also lists `current_project`, which the bar leaves out. */
function inside(item = pendingWorktree()) {
  const { backend } = createFakeBackend()
  const current = { ...item, isCurrent: true, blockers: [...item.blockers, blocker('current_project')] }
  const wt = installWorktrees(backend, [mainWorktree({ isCurrent: false }), current])
  return { backend, wt }
}

describe('Worktree bar in the task header', () => {
  it('stays out of the way when the project is not a Git repository', async () => {
    const { backend } = createFakeBackend()
    const { container } = render(<Harness backend={backend} />)
    await waitFor(() => expect(container).toBeEmptyDOMElement())
    expect(screen.queryByRole('region', { name: 'Worktree da tarefa' })).not.toBeInTheDocument()
  })

  it('reads nothing without an open project', () => {
    const { backend } = createFakeBackend()
    const list = vi.spyOn(backend, 'listWorktrees')
    const { container } = render(<Harness backend={backend} workspaceId="" />)
    expect(container).toBeEmptyDOMElement()
    expect(list).not.toHaveBeenCalled()
  })

  it('shows the main checkout and how many other worktrees could go', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    installWorktrees(backend, [mainWorktree(), worktreeItem(), pendingWorktree(), lockedWorktree()])
    const open = vi.fn()
    render(<Harness backend={backend} onOpenWorktrees={open} />)
    const region = await screen.findByRole('region', { name: 'Worktree da tarefa' })
    expect(within(region).getByText('main')).toBeInTheDocument()
    expect(within(region).getByText('Principal')).toBeInTheDocument()
    expect(within(region).getByText('Sem alterações pendentes')).toBeInTheDocument()
    expect(within(region).getByText('3 outros worktrees · 1 pode ser excluído')).toBeInTheDocument()
    expect(within(region).queryByRole('button', { name: 'Salvar com a IA…' })).not.toBeInTheDocument()
    await user.click(within(region).getByRole('button', { name: 'Gerenciar worktrees' }))
    expect(open).toHaveBeenCalledTimes(1)
  })

  it('says when the repository has only the main worktree', async () => {
    const { backend } = createFakeBackend()
    installWorktrees(backend, [mainWorktree()])
    render(<Harness backend={backend} />)
    expect(await screen.findByText('Sem outros worktrees.')).toBeInTheDocument()
  })

  it('hides the shortcut to the page while that page is open', async () => {
    const { backend } = createFakeBackend()
    installWorktrees(backend, [mainWorktree(), worktreeItem()])
    render(<Harness backend={backend} showManage={false} />)
    await screen.findByRole('region', { name: 'Worktree da tarefa' })
    expect(screen.queryByRole('button', { name: 'Gerenciar worktrees' })).not.toBeInTheDocument()
  })

  it('tells the task its worktree is safe once everything is saved and merged', async () => {
    const { backend } = inside(worktreeItem())
    render(<Harness backend={backend} />)
    const region = await screen.findByRole('region', { name: 'Worktree da tarefa' })
    expect(within(region).getByText('feature/exportacao')).toBeInTheDocument()
    expect(within(region).getByText('Worktree')).toBeInTheDocument()
    expect(within(region).getByText('Tudo salvo e mesclado em main: pode ser excluído ao sair deste projeto.')).toBeInTheDocument()
    expect(within(region).queryByRole('button', { name: 'Salvar com a IA…' })).not.toBeInTheDocument()
    expect(within(region).getByRole('button', { name: 'Gerenciar worktrees' })).toBeInTheDocument()
  })

  it('names the first thing that holds the task worktree and counts the rest', async () => {
    const { backend } = inside()
    render(<Harness backend={backend} />)
    const region = await screen.findByRole('region', { name: 'Worktree da tarefa' })
    expect(within(region).getByText('3 alterações não salvas · 2 commits à frente de main')).toBeInTheDocument()
    expect(within(region).getByText(/Não pode ser excluído: 3 alterações não salvas \(arquivos editados ou novos\)\. \+1 motivo$/)).toBeInTheDocument()
    // Being the open project is not a reason the user can fix here, so it is not listed.
    expect(within(region).queryByText(/projeto aberto agora/)).not.toBeInTheDocument()
  })

  it('starts the assistant for the task worktree from the header', async () => {
    const user = userEvent.setup()
    const { backend, wt } = inside()
    const onSaveWithAI = vi.fn(async () => undefined)
    render(<Harness backend={backend} onSaveWithAI={onSaveWithAI} />)
    const region = await screen.findByRole('region', { name: 'Worktree da tarefa' })
    await user.click(within(region).getByRole('button', { name: 'Salvar com a IA…' }))
    const dialog = await screen.findByRole('dialog', { name: 'Salvar e mesclar com a IA' })
    expect(within(dialog).getByText(/ainda tem trabalho que só existe nele/)).toBeInTheDocument()
    expect(within(dialog).getAllByText('feature/relatorios', { selector: 'strong' }).length).toBeGreaterThan(0)
    await user.click(within(dialog).getByRole('button', { name: 'Começar' }))
    await waitFor(() => expect(onSaveWithAI).toHaveBeenCalledTimes(1))
    expect(wt.calls.prepared).toEqual(['/synthetic/worktrees/relatorios'])
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('explains a refusal in the header and reads the worktree again', async () => {
    const user = userEvent.setup()
    const { backend, wt } = inside()
    render(<Harness backend={backend} />)
    const region = await screen.findByRole('region', { name: 'Worktree da tarefa' })
    await user.click(within(region).getByRole('button', { name: 'Salvar com a IA…' }))
    const dialog = await screen.findByRole('dialog')
    wt.set(wt.items().map(item => item.isCurrent ? { ...item, canSaveWithAi: false, saveBlockers: [blocker('base_dirty', 2, 'main')] } : item))
    const reads = wt.calls.list
    await user.click(within(dialog).getByRole('button', { name: 'Começar' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Não dá para pedir à IA que salve este worktree agora')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    await waitFor(() => expect(wt.calls.list).toBeGreaterThan(reads))
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Salvar com a IA…' })).not.toBeInTheDocument())
    await user.click(within(alert).getByRole('button', { name: 'Fechar aviso' }))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
