import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Backend, BackendOption } from '../../lib/backend'
import { createFakeBackend } from '../../test/fakeBackend'
import { blocker, detachedWorktree, ignoredWorktree, inUseWorktree, installWorktrees, mainWorktree, pendingWorktree, worktreeItem, worktreeListOf, worktreeRoot } from '../../test/worktreeFixture'
import { useWorktrees } from './useWorktrees'
import { WorktreesPage } from './WorktreesPage'

afterEach(cleanup)

const api: BackendOption = { id: 'local', name: 'Local', kind: 'api', available: true }
const cli: BackendOption = { id: 'codex', name: 'Codex', kind: 'cli', available: true }

type HarnessProps = {
  backend: Backend
  workspaceId?: string
  backends?: BackendOption[]
  onSaveWithAI?: (...args: never[]) => Promise<void>
  onProjects?: () => void
  onSettings?: () => void
}

function Harness({ backend, workspaceId = 'workspace-1', backends = [api, cli], onSaveWithAI = async () => undefined, onProjects = () => undefined, onSettings = () => undefined }: HarnessProps) {
  const worktrees = useWorktrees(backend, workspaceId)
  return <WorktreesPage backend={backend} workspaceId={workspaceId} backends={backends} defaultBackendId="" worktrees={worktrees}
    onProjects={onProjects} onSettings={onSettings} onSaveWithAI={onSaveWithAI as never} />
}

const row = (name: string) => screen.getByRole('list', { name: 'Worktrees do repositório' }).querySelector(`li[class*="worktree-row"]:has(strong[title="${name}"])`) as HTMLElement

function setup(items = [mainWorktree(), worktreeItem(), pendingWorktree(), detachedWorktree(), ignoredWorktree()]) {
  const { backend } = createFakeBackend()
  const wt = installWorktrees(backend, items)
  return { backend, wt }
}

describe('Worktrees page', () => {
  it('judges every worktree and says plainly which can be deleted and why not', async () => {
    const { backend } = setup()
    render(<Harness backend={backend} />)
    expect(await screen.findByRole('heading', { name: 'Worktrees' })).toBeInTheDocument()
    expect(await screen.findByText('feature/exportacao')).toBeInTheDocument()

    const clear = row('feature/exportacao')
    expect(within(clear).getByText('Pode excluir')).toBeInTheDocument()
    expect(within(clear).getByText('Tudo está salvo e mesclado em main.')).toBeInTheDocument()
    expect(within(clear).getByText('Mesclado em main')).toBeInTheDocument()

    const held = row('feature/relatorios')
    expect(within(held).getByText('Não pode excluir')).toBeInTheDocument()
    expect(within(held).getByText('3 alterações não salvas (arquivos editados ou novos).')).toBeInTheDocument()
    expect(within(held).getByText('2 commits só desta branch, ainda não mesclados em main.')).toBeInTheDocument()
    expect(within(held).queryByText('Pode excluir')).not.toBeInTheDocument()

    const loose = row('HEAD destacado em 4d5e6f7a')
    expect(within(loose).getByText('1 commit solto (HEAD destacado) que nenhuma branch guarda.')).toBeInTheDocument()

    const main = row('main')
    expect(within(main).getByText('Worktree principal')).toBeInTheDocument()
    expect(within(main).getByText('Principal')).toBeInTheDocument()
    expect(within(main).getByText('Projeto aberto')).toBeInTheDocument()
    expect(within(main).queryByRole('button')).not.toBeInTheDocument()

    expect(screen.getByRole('status')).toHaveTextContent('4 worktrees além do principal · 2 podem ser excluídos · base main')
  })

  it('offers deleting only where it is safe and the assistant only where it can help', async () => {
    const { backend } = setup()
    render(<Harness backend={backend} />)
    await screen.findByText('feature/exportacao')
    expect(screen.getAllByRole('button', { name: 'Excluir…' })).toHaveLength(2)
    expect(screen.getAllByRole('button', { name: 'Salvar e mesclar com a IA…' })).toHaveLength(2)
    expect(within(row('feature/exportacao')).getByRole('button', { name: 'Excluir…' })).toBeInTheDocument()
    expect(within(row('feature/exportacao')).queryByRole('button', { name: 'Salvar e mesclar com a IA…' })).not.toBeInTheDocument()
    expect(within(row('feature/relatorios')).getByRole('button', { name: 'Salvar e mesclar com a IA…' })).toBeInTheDocument()
    expect(within(row('feature/relatorios')).queryByRole('button', { name: 'Excluir…' })).not.toBeInTheDocument()
  })

  it('offers committing a dirty main worktree, which is what unblocks merging the others', async () => {
    const user = userEvent.setup()
    const dirtyMain = mainWorktree({ changed: 13, unstaged: 13, changedFiles: ['README.md'], canSaveWithAi: true, saveBlockers: [] })
    const held = pendingWorktree({ canSaveWithAi: false, saveBlockers: [blocker('base_dirty', 13, 'main')] })
    const merged = worktreeItem({ canSaveWithAi: false, saveBlockers: [blocker('base_dirty', 13, 'main')] })
    const { backend, wt } = setup([dirtyMain, held, merged])
    const onSaveWithAI = vi.fn(async () => undefined)
    render(<Harness backend={backend} onSaveWithAI={onSaveWithAI} />)
    await screen.findByText('feature/relatorios')
    expect(within(row('feature/relatorios')).queryByRole('button', { name: 'Salvar e mesclar com a IA…' })).not.toBeInTheDocument()
    expect(within(row('main')).getByText('Commite as 13 alterações pendentes para os outros worktrees poderem ser mesclados aqui.')).toBeInTheDocument()
    // The held worktree offers the same way out, so it is found where the person is looking.
    await user.click(within(row('feature/relatorios')).getByRole('button', { name: 'Commitar o principal com a IA…' }))
    expect(within(row('feature/exportacao')).queryByRole('button', { name: 'Commitar o principal com a IA…' })).not.toBeInTheDocument()
    expect(await screen.findByRole('dialog', { name: 'Commitar com a IA' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Cancelar' }))
    await user.click(within(row('main')).getByRole('button', { name: 'Commitar com a IA…' }))
    const dialog = await screen.findByRole('dialog', { name: 'Commitar com a IA' })
    expect(within(dialog).queryByText(/Excluir o worktree/)).not.toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Começar' }))
    await waitFor(() => expect(onSaveWithAI).toHaveBeenCalled())
    expect(wt.calls.prepared).toEqual([dirtyMain.path])
    expect(onSaveWithAI).toHaveBeenCalledWith(expect.objectContaining({ path: dirtyMain.path }), { backendId: 'local', deleteWhenSaved: false, deleteBranch: false, acknowledgeIgnored: false })
  })

  it('shows the worktrees of every project grouped by repository and acts through each group\'s project', async () => {
    const user = userEvent.setup()
    const { backend, wt } = setup()
    const other = worktreeListOf([mainWorktree({ path: '/synthetic/workspace/painel', name: 'painel', isCurrent: false }), worktreeItem({ path: '/synthetic/worktrees/painel-tema', name: 'painel-tema', branch: 'feature/tema' })], { root: '/synthetic/workspace/painel' })
    backend.listAllWorktrees = vi.fn(async () => [
      { workspaceId: 'workspace-1', name: 'api-faturas', path: worktreeRoot, projects: ['api-faturas'], list: await backend.listWorktrees('workspace-1') },
      { workspaceId: 'workspace-2', name: 'painel', path: '/synthetic/workspace/painel', projects: ['painel', 'painel-tema'], list: other },
    ])
    render(<Harness backend={backend} />)
    await screen.findByText('feature/exportacao')
    expect(backend.listAllWorktrees).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Todos os projetos' }))
    const painel = await screen.findByRole('region', { name: 'painel' })
    expect(screen.getByRole('region', { name: 'api-faturas' })).toBeInTheDocument()
    expect(within(painel).getByText('Projetos: painel, painel-tema')).toBeInTheDocument()
    expect(screen.getByText(/2 repositórios/)).toBeInTheDocument()
    await user.click(within(painel).getByRole('button', { name: 'Excluir…' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: /^Excluir/ }))
    await waitFor(() => expect(wt.calls.deleted).toHaveLength(1))
    expect(wt.calls.deleted[0]).toMatchObject({ workspaceId: 'workspace-2', path: '/synthetic/worktrees/painel-tema' })
  })

  it('holds back a folder that programs are using even though Git holds all of its content', async () => {
    const { backend } = setup([mainWorktree(), inUseWorktree(), worktreeItem()])
    render(<Harness backend={backend} />)
    await screen.findByText('feature/aberto')
    const held = row('feature/aberto')
    expect(within(held).getByText('Não pode excluir')).toBeInTheDocument()
    expect(within(held).getByText('2 programas estão usando esta pasta (zsh (pid 4242), code (pid 4300)). Feche-os e confira de novo.')).toBeInTheDocument()
    expect(within(held).getByText('Mesclado em main')).toBeInTheDocument()
    expect(within(held).queryByRole('button', { name: 'Excluir…' })).not.toBeInTheDocument()
    expect(within(row('feature/exportacao')).getByRole('button', { name: 'Excluir…' })).toBeInTheDocument()
  })

  it('lists the pending files of a held worktree', async () => {
    const { backend } = setup()
    render(<Harness backend={backend} />)
    await screen.findByText('feature/relatorios')
    const held = row('feature/relatorios')
    await userEvent.setup().click(within(held).getByText('Ver 3 arquivos'))
    expect(within(held).getByText('?? docs/relatorios.md')).toBeVisible()
  })

  it('says when only the first files are listed', async () => {
    const { backend } = setup([mainWorktree(), pendingWorktree({ changed: 240, changesTruncated: true })])
    render(<Harness backend={backend} />)
    expect(await screen.findByText('Ver os primeiros 3 de 240')).toBeInTheDocument()
  })

  it('deletes a safe worktree and its merged branch after one confirmation', async () => {
    const user = userEvent.setup()
    const { backend, wt } = setup()
    render(<Harness backend={backend} />)
    await screen.findByText('feature/exportacao')
    await user.click(within(row('feature/exportacao')).getByRole('button', { name: 'Excluir…' }))
    const dialog = await screen.findByRole('dialog', { name: 'Excluir o worktree feature/exportacao?' })
    expect(within(dialog).getByText(/Tudo o que este worktree tinha foi salvo e mesclado em/)).toBeInTheDocument()
    expect(within(dialog).queryByRole('alert')).not.toBeInTheDocument()
    expect(within(dialog).getByRole('checkbox', { name: /Apagar também a branch feature\/exportacao/ })).toBeChecked()
    await user.click(within(dialog).getByRole('button', { name: 'Excluir worktree' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(wt.calls.deleted).toEqual([{ workspaceId: 'workspace-1', path: '/synthetic/worktrees/exportacao', deleteBranch: true, acknowledgeIgnored: false }])
    expect(screen.queryByText('feature/exportacao', { selector: 'strong' })).not.toBeInTheDocument()
    expect(screen.getByText('Worktree feature/exportacao excluído e a branch feature/exportacao também.')).toBeInTheDocument()
    expect(screen.getByText(/3 worktrees além do principal · 1 pode ser excluído/)).toBeInTheDocument()
  })

  it('keeps the branch when asked to', async () => {
    const user = userEvent.setup()
    const { backend, wt } = setup()
    render(<Harness backend={backend} />)
    await screen.findByText('feature/exportacao')
    await user.click(within(row('feature/exportacao')).getByRole('button', { name: 'Excluir…' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('checkbox', { name: /Apagar também a branch/ }))
    await user.click(within(dialog).getByRole('button', { name: 'Excluir worktree' }))
    await waitFor(() => expect(wt.calls.deleted).toHaveLength(1))
    expect(wt.calls.deleted[0]).toMatchObject({ deleteBranch: false })
    expect(await screen.findByText('Worktree feature/exportacao excluído.')).toBeInTheDocument()
  })

  it('does not delete when the confirmation is cancelled', async () => {
    const user = userEvent.setup()
    const { backend, wt } = setup()
    render(<Harness backend={backend} />)
    await screen.findByText('feature/exportacao')
    await user.click(within(row('feature/exportacao')).getByRole('button', { name: 'Excluir…' }))
    await user.click(await screen.findByRole('button', { name: 'Cancelar' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(wt.calls.deleted).toEqual([])
    expect(screen.getByText('feature/exportacao', { selector: 'strong' })).toBeInTheDocument()
  })

  it('makes the user acknowledge files Git does not hold before they are erased', async () => {
    const user = userEvent.setup()
    const { backend, wt } = setup()
    render(<Harness backend={backend} />)
    await screen.findByText('feature/cache')
    expect(within(row('feature/cache')).getByText(/Ao excluir, apaga 5 itens ignorados pelo Git: node_modules\/, \.env\.local, dist\/ e mais 2/)).toBeInTheDocument()
    await user.click(within(row('feature/cache')).getByRole('button', { name: 'Excluir…' }))
    const dialog = await screen.findByRole('dialog', { name: 'Excluir o worktree feature/cache?' })
    expect(within(dialog).getByRole('alert')).toHaveTextContent('Estes itens não estão no Git e serão apagados para sempre')
    const confirm = within(dialog).getByRole('button', { name: 'Excluir worktree' })
    expect(confirm).toBeDisabled()
    await user.click(within(dialog).getByRole('checkbox', { name: 'Entendo que serão apagados.' }))
    expect(confirm).toBeEnabled()
    await user.click(confirm)
    await waitFor(() => expect(wt.calls.deleted).toHaveLength(1))
    expect(wt.calls.deleted[0]).toMatchObject({ path: '/synthetic/worktrees/cache', acknowledgeIgnored: true })
  })

  it('explains and re-reads the list when the backend finds the worktree is no longer safe', async () => {
    const user = userEvent.setup()
    const { backend, wt } = setup()
    render(<Harness backend={backend} />)
    await screen.findByText('feature/exportacao')
    await user.click(within(row('feature/exportacao')).getByRole('button', { name: 'Excluir…' }))
    await screen.findByRole('dialog')
    // Someone edited a file after the list was read.
    wt.set(wt.items().map(item => item.branch === 'feature/exportacao' ? pendingWorktree({ path: item.path, name: item.name, branch: item.branch }) : item))
    const reads = wt.calls.list
    await user.click(screen.getByRole('button', { name: 'Excluir worktree' }))
    expect(await screen.findByText(/O worktree ainda guarda trabalho que seria perdido/)).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    await waitFor(() => expect(wt.calls.list).toBeGreaterThan(reads))
    await waitFor(() => expect(within(row('feature/exportacao')).getByText('Não pode excluir')).toBeInTheDocument())
  })

  it('shows an unexpected failure inside the dialog and lets the user try again', async () => {
    const user = userEvent.setup()
    const { backend } = setup()
    let attempts = 0
    const real = backend.deleteWorktree
    backend.deleteWorktree = async input => {
      attempts++
      if (attempts === 1) throw Object.assign(new Error('x'), { cause: { code: 'worktree_remove_failed' } })
      return real(input)
    }
    render(<Harness backend={backend} />)
    await screen.findByText('feature/exportacao')
    await user.click(within(row('feature/exportacao')).getByRole('button', { name: 'Excluir…' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: 'Excluir worktree' }))
    expect(await within(dialog).findByText(/O Git não conseguiu remover a pasta do worktree/)).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Excluir worktree' })).toBeEnabled()
    await user.click(within(dialog).getByRole('button', { name: 'Excluir worktree' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(attempts).toBe(2)
  })

  it('hands a held worktree to the assistant with the chosen provider and options', async () => {
    const user = userEvent.setup()
    const { backend, wt } = setup()
    const onSaveWithAI = vi.fn(async () => undefined)
    render(<Harness backend={backend} onSaveWithAI={onSaveWithAI as never} />)
    await screen.findByText('feature/relatorios')
    await user.click(within(row('feature/relatorios')).getByRole('button', { name: 'Salvar e mesclar com a IA…' }))
    const dialog = await screen.findByRole('dialog', { name: 'Salvar e mesclar com a IA' })
    expect(within(dialog).getByText(/ainda tem trabalho que só existe nele/)).toBeInTheDocument()
    expect(within(dialog).getByText(/no projeto principal/)).toBeInTheDocument()
    expect(within(dialog).getByRole('checkbox', { name: /Excluir o worktree quando tudo estiver salvo e mesclado/ })).toBeChecked()
    await user.click(within(dialog).getByRole('button', { name: 'Começar' }))
    await waitFor(() => expect(onSaveWithAI).toHaveBeenCalledTimes(1))
    expect(wt.calls.prepared).toEqual(['/synthetic/worktrees/relatorios'])
    const [plan, options] = onSaveWithAI.mock.calls[0] as unknown as [{ branch: string; prompt: string }, Record<string, unknown>]
    expect(plan).toMatchObject({ branch: 'feature/relatorios', base: 'main' })
    expect(options).toEqual({ backendId: 'local', deleteWhenSaved: true, deleteBranch: true, acknowledgeIgnored: true })
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('lets the user keep the worktree and its branch after the assistant saved everything', async () => {
    const user = userEvent.setup()
    const { backend } = setup()
    const onSaveWithAI = vi.fn(async () => undefined)
    render(<Harness backend={backend} onSaveWithAI={onSaveWithAI as never} />)
    await screen.findByText('feature/relatorios')
    await user.click(within(row('feature/relatorios')).getByRole('button', { name: 'Salvar e mesclar com a IA…' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByRole('checkbox', { name: /Apagar também a branch feature\/relatorios/ })).toBeInTheDocument()
    await user.click(within(dialog).getByRole('checkbox', { name: /Excluir o worktree quando tudo estiver salvo/ }))
    expect(within(dialog).queryByRole('checkbox', { name: /Apagar também a branch/ })).not.toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Começar' }))
    await waitFor(() => expect(onSaveWithAI).toHaveBeenCalled())
    expect((onSaveWithAI.mock.calls[0] as unknown[])[1]).toEqual({ backendId: 'local', deleteWhenSaved: false, deleteBranch: false, acknowledgeIgnored: false })
  })

  it('warns that ignored files go with the worktree when it is deleted after saving', async () => {
    const user = userEvent.setup()
    const { backend } = setup([mainWorktree(), pendingWorktree({ ignored: ['node_modules/'], ignoredMore: 0 })])
    render(<Harness backend={backend} />)
    await screen.findByText('feature/relatorios')
    await user.click(screen.getByRole('button', { name: 'Salvar e mesclar com a IA…' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByRole('note')).toHaveTextContent('Isso também apaga 1 item ignorado pelo Git: node_modules/.')
    await user.click(within(dialog).getByRole('checkbox', { name: /Excluir o worktree quando tudo estiver salvo/ }))
    expect(within(dialog).queryByRole('note')).not.toBeInTheDocument()
  })

  it('prefers an API provider and offers only the ones that are available', async () => {
    const user = userEvent.setup()
    const { backend } = setup()
    const offline: BackendOption = { id: 'opencode', name: 'opencode', kind: 'cli', available: false }
    render(<Harness backend={backend} backends={[offline, cli, api]} />)
    await screen.findByText('feature/relatorios')
    await user.click(within(row('feature/relatorios')).getByRole('button', { name: 'Salvar e mesclar com a IA…' }))
    const dialog = await screen.findByRole('dialog')
    const picker = within(dialog).getByTestId('picker-worktree-ai-backend')
    expect(within(picker).getByRole('button')).toHaveTextContent('Local · API')
    await user.click(within(picker).getByRole('button'))
    const options = screen.getAllByRole('option').map(option => option.textContent)
    expect(options).toEqual(['Codex · CLI', 'Local · API'])
  })

  it('shows why the assistant cannot start and keeps the dialog open', async () => {
    const user = userEvent.setup()
    const { backend, wt } = setup()
    const onSaveWithAI = vi.fn(async () => { throw Object.assign(new Error('x'), { cause: { code: 'worktree_backend_unusable' } }) })
    render(<Harness backend={backend} backends={[cli]} onSaveWithAI={onSaveWithAI as never} />)
    await screen.findByText('feature/relatorios')
    await user.click(within(row('feature/relatorios')).getByRole('button', { name: 'Salvar e mesclar com a IA…' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: 'Começar' }))
    expect(await within(dialog).findByText(/um CLI precisa de um modelo padrão salvo em Configurações/)).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Começar' })).toBeEnabled()
    expect(wt.calls.prepared).toHaveLength(1)
  })

  it('sends the user to Settings when no AI provider is available', async () => {
    const user = userEvent.setup()
    const { backend } = setup()
    const onSettings = vi.fn()
    render(<Harness backend={backend} backends={[{ ...api, available: false }]} onSettings={onSettings} />)
    await screen.findByText('feature/relatorios')
    await user.click(within(row('feature/relatorios')).getByRole('button', { name: 'Salvar e mesclar com a IA…' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByRole('alert')).toHaveTextContent('Nenhum provedor de IA disponível')
    expect(within(dialog).getByRole('button', { name: 'Começar' })).toBeDisabled()
    await user.click(within(dialog).getByRole('button', { name: 'Configurar provedor' }))
    expect(onSettings).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('refreshes and explains when the assistant is refused because the worktree changed', async () => {
    const user = userEvent.setup()
    const { backend, wt } = setup()
    render(<Harness backend={backend} />)
    await screen.findByText('feature/relatorios')
    await user.click(within(row('feature/relatorios')).getByRole('button', { name: 'Salvar e mesclar com a IA…' }))
    const dialog = await screen.findByRole('dialog')
    wt.set(wt.items().map(item => item.branch === 'feature/relatorios' ? { ...item, canSaveWithAi: false, saveBlockers: [{ code: 'base_dirty', detail: 'main', count: 2 }] } : item))
    const reads = wt.calls.list
    await user.click(within(dialog).getByRole('button', { name: 'Começar' }))
    expect(await screen.findByText(/Não dá para pedir à IA que salve este worktree agora/)).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    await waitFor(() => expect(wt.calls.list).toBeGreaterThan(reads))
    await waitFor(() => expect(within(row('feature/relatorios')).queryByRole('button', { name: 'Salvar e mesclar com a IA…' })).not.toBeInTheDocument())
    expect(within(row('feature/relatorios')).getByText(/A IA não pode ajudar agora: O worktree principal tem alterações \(2\)/)).toBeInTheDocument()
  })

  it('cleans the record of a folder that no longer exists', async () => {
    const user = userEvent.setup()
    const { backend, wt } = setup([mainWorktree(), worktreeItem({ path: '/synthetic/worktrees/sumiu', branch: 'feature/sumiu', missing: true, statusKnown: false, canDelete: false, blockers: [{ code: 'missing_directory', detail: '', count: 0 }], saveBlockers: [{ code: 'missing_directory', detail: '', count: 0 }] })])
    render(<Harness backend={backend} />)
    await screen.findByText('feature/sumiu')
    expect(within(row('feature/sumiu')).getByText('A pasta não existe mais; falta limpar o registro dela.')).toBeInTheDocument()
    expect(within(row('feature/sumiu')).getByText('Pasta ausente')).toBeInTheDocument()
    expect(within(row('feature/sumiu')).queryByRole('button', { name: 'Excluir…' })).not.toBeInTheDocument()
    await user.click(within(row('feature/sumiu')).getByRole('button', { name: 'Limpar registro' }))
    expect(await screen.findByText('Registros de pastas ausentes foram limpos.')).toBeInTheDocument()
    expect(wt.calls.pruned).toBe(1)
    expect(screen.queryByText('feature/sumiu')).not.toBeInTheDocument()
    expect(screen.getByText(/Este repositório só tem o worktree principal\./)).toBeInTheDocument()
  })

  it('keeps showing the last list while it is read again', async () => {
    const user = userEvent.setup()
    const { backend, wt } = setup()
    render(<Harness backend={backend} />)
    await screen.findByText('feature/exportacao')
    let finish: () => void = () => undefined
    const gate = new Promise<void>(resolve => { finish = resolve })
    const real = backend.listWorktrees
    backend.listWorktrees = async id => { await gate; return real(id) }
    wt.set([mainWorktree()])
    await user.click(screen.getByRole('button', { name: 'Atualizar' }))
    // Still the old answer, flagged as being refreshed, with no second refresh to click.
    expect(await screen.findByText(/atualizando…/)).toBeInTheDocument()
    expect(screen.getByText('feature/exportacao', { selector: 'strong' })).toBeInTheDocument()
    expect(screen.getByRole('list', { name: 'Worktrees do repositório' })).toHaveAttribute('aria-busy', 'true')
    expect(screen.getByRole('button', { name: 'Atualizar' })).toBeDisabled()
    finish()
    expect(await screen.findByText(/Este repositório só tem o worktree principal\./)).toBeInTheDocument()
    expect(screen.queryByText('feature/exportacao', { selector: 'strong' })).not.toBeInTheDocument()
  })

  it('reads the list again on request', async () => {
    const user = userEvent.setup()
    const { backend, wt } = setup()
    render(<Harness backend={backend} />)
    await screen.findByText('feature/exportacao')
    const before = wt.calls.list
    wt.set([mainWorktree()])
    await user.click(screen.getByRole('button', { name: 'Atualizar' }))
    await waitFor(() => expect(wt.calls.list).toBeGreaterThan(before))
    expect(await screen.findByText(/Este repositório só tem o worktree principal\./)).toBeInTheDocument()
  })

  it('asks to open a project when none is open', async () => {
    const user = userEvent.setup()
    const { backend } = setup()
    const onProjects = vi.fn()
    render(<Harness backend={backend} workspaceId="" onProjects={onProjects} />)
    // useWorktrees treats an empty id as no project; the page must not read anything.
    expect(await screen.findByText('Nenhum projeto aberto')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Atualizar' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Ir para Projetos' }))
    expect(onProjects).toHaveBeenCalledTimes(1)
  })

  it('says so when the project folder is not a Git repository', async () => {
    const { backend } = createFakeBackend()
    render(<Harness backend={backend} />)
    expect(await screen.findByText('Esta pasta não está dentro de um repositório Git')).toBeInTheDocument()
    expect(screen.queryByRole('list', { name: 'Worktrees do repositório' })).not.toBeInTheDocument()
  })

  it('shows a read failure with a way to retry', async () => {
    const user = userEvent.setup()
    const { backend } = setup()
    let failing = true
    const real = backend.listWorktrees
    backend.listWorktrees = async id => { if (failing) throw Object.assign(new Error('x'), { cause: { code: 'not_repository' } }); return real(id) }
    render(<Harness backend={backend} />)
    expect(await screen.findByRole('alert')).toHaveTextContent('Esta pasta não está dentro de um repositório Git.')
    failing = false
    await user.click(screen.getByRole('button', { name: 'Tentar novamente' }))
    expect(await screen.findByText('feature/exportacao')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('refuses to promise a delete when the base branch is unknown', async () => {
    const { backend, wt } = setup()
    const list = worktreeListOf(wt.items(), { base: '', baseKnown: false })
    backend.listWorktrees = async () => list
    render(<Harness backend={backend} />)
    expect(await screen.findByText(/Não foi possível identificar a branch base; por segurança, nenhum worktree pode ser excluído até lá/)).toBeInTheDocument()
  })
})
