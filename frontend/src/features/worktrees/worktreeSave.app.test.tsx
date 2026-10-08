import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import App from '../../app/App'
import type { Backend, ModelCatalogResult, Worktree } from '../../lib/backend'
import { createFakeBackend, sessionMetadata } from '../../test/fakeBackend'
import { installWorktrees, lockedWorktree, mainWorktree, pendingWorktree, worktreeItem, worktreeRoot } from '../../test/worktreeFixture'

afterEach(() => { cleanup(); localStorage.removeItem('harflex:view-mode') })

const relatorios = '/synthetic/worktrees/relatorios'

type Scenario = { items?: Worktree[]; openAt?: string; onPrompt?: (wt: ReturnType<typeof installWorktrees>) => void }

/** The app on the simulated Go facade, with the assistant's run standing in for the real commit and merge. */
function setup({ items = [mainWorktree(), pendingWorktree()], openAt = worktreeRoot, onPrompt }: Scenario = {}) {
  const { backend, respondReadOnly } = createFakeBackend()
  const wt = installWorktrees(backend, items, { followProject: true })
  const prompts: string[] = []
  backend.prompt = async (_session, text) => {
    prompts.push(text)
    onPrompt?.(wt)
    return respondReadOnly(text, 'Commitei o que estava pendente e mesclei em main.')
  }
  const direct = vi.spyOn(backend, 'createDirectSession')
  const opened = vi.spyOn(backend, 'openWorkspace')
  return { backend, wt, prompts, direct, opened, openAt }
}

async function openProject(user: ReturnType<typeof userEvent.setup>, path: string) {
  await user.type(await screen.findByLabelText('Caminho da pasta'), path)
  await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
}

async function openWorktreesPage(user: ReturnType<typeof userEvent.setup>) {
  await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Worktrees' }))
  await screen.findByRole('heading', { name: 'Worktrees' })
}

async function startSaving(user: ReturnType<typeof userEvent.setup>, options: { keep?: boolean } = {}) {
  await user.click(await screen.findByRole('button', { name: 'Salvar e mesclar com a IA…' }))
  const dialog = await screen.findByRole('dialog', { name: 'Salvar e mesclar com a IA' })
  if (options.keep) await user.click(within(dialog).getByRole('checkbox', { name: /Excluir o worktree quando tudo estiver salvo/ }))
  await user.click(within(dialog).getByRole('button', { name: 'Começar' }))
}

describe('worktree save with the assistant', () => {
  it('lists the worktrees under their own destination in the menu', async () => {
    const user = userEvent.setup()
    const { backend } = setup({ items: [mainWorktree(), worktreeItem(), pendingWorktree(), lockedWorktree()] })
    render(<App backend={backend} />)
    await openProject(user, worktreeRoot)
    await openWorktreesPage(user)
    expect(await screen.findByText('feature/relatorios')).toBeInTheDocument()
    const list = screen.getByRole('list', { name: 'Worktrees do repositório' })
    expect(within(list).getAllByText(/Pode excluir|Não pode excluir|Worktree principal/).map(node => node.textContent)).toEqual(['Worktree principal', 'Pode excluir', 'Não pode excluir', 'Não pode excluir'])
    // The task header carries the same control on every destination.
    expect(screen.getByRole('region', { name: 'Worktree da tarefa' })).toHaveTextContent('3 outros worktrees · 1 pode ser excluído')
    expect(within(screen.getByRole('region', { name: 'Worktree da tarefa' })).queryByRole('button', { name: 'Gerenciar worktrees' })).not.toBeInTheDocument()
  })

  it('opens the worktree page from the task header', async () => {
    const user = userEvent.setup()
    const { backend } = setup()
    render(<App backend={backend} />)
    await openProject(user, worktreeRoot)
    const region = await screen.findByRole('region', { name: 'Worktree da tarefa' })
    await user.click(within(region).getByRole('button', { name: 'Gerenciar worktrees' }))
    expect(await screen.findByRole('heading', { name: 'Worktrees' })).toBeInTheDocument()
  })

  it('has the assistant commit and merge, then deletes the worktree once the app has checked it', async () => {
    const user = userEvent.setup()
    const { backend, wt, prompts, direct } = setup({ onPrompt: wt => wt.settle(relatorios) })
    render(<App backend={backend} />)
    await openProject(user, worktreeRoot)
    await openWorktreesPage(user)
    await startSaving(user)

    expect(await screen.findByText('Worktree feature/relatorios excluído. Tudo estava salvo e mesclado em main; a branch feature/relatorios também foi apagada.')).toBeInTheDocument()
    expect(direct).toHaveBeenCalledWith({ workspaceId: 'workspace-1', backendId: 'local', reason: 'Salvar e mesclar o worktree feature/relatorios' })
    expect(prompts).toEqual(['Commite o que está pendente em feature/relatorios e mescle em main.'])
    expect(wt.calls.prepared).toEqual([relatorios])
    // The assistant never deletes: the app judged again and removed it, acknowledging what the user agreed to.
    expect(wt.calls.deleted).toEqual([{ workspaceId: 'workspace-1', path: relatorios, deleteBranch: true, acknowledgeIgnored: true }])
    expect(wt.items().map(item => item.branch)).toEqual(['main'])
    expect(screen.getByRole('status', { name: 'Salvamento do worktree' })).toHaveTextContent('excluído')
  })

  it('judges again after the run and deletes nothing when work is still pending', async () => {
    const user = userEvent.setup()
    const { backend, wt, prompts } = setup()
    render(<App backend={backend} />)
    await openProject(user, worktreeRoot)
    await openWorktreesPage(user)
    await startSaving(user)

    const alert = await screen.findByRole('alert', { name: 'Salvamento do worktree' })
    expect(alert).toHaveTextContent('Ainda não dá para excluir feature/relatorios:')
    expect(alert).toHaveTextContent('3 alterações não salvas (arquivos editados ou novos).')
    expect(alert).toHaveTextContent('Nada foi excluído.')
    expect(prompts).toHaveLength(1)
    expect(wt.calls.deleted).toEqual([])
    expect(wt.items().map(item => item.branch)).toEqual(['main', 'feature/relatorios'])
  })

  it('checks again when the user continues the conversation and the assistant finishes the job', async () => {
    const user = userEvent.setup()
    let runs = 0
    // The first run leaves the work half done; the follow-up completes it.
    const { backend, wt, prompts } = setup({ onPrompt: wt => { runs++; if (runs === 2) wt.settle(relatorios) } })
    render(<App backend={backend} />)
    await openProject(user, worktreeRoot)
    await openWorktreesPage(user)
    await startSaving(user)
    const alert = await screen.findByRole('alert', { name: 'Salvamento do worktree' })
    expect(alert).toHaveTextContent('Se a conversa continuar, o Harflex confere de novo quando a IA terminar.')
    expect(wt.calls.deleted).toEqual([])

    await user.type(screen.getByLabelText('Mensagem'), 'O merge ficou faltando, conclua')
    await user.keyboard('{Enter}')
    expect(await screen.findByText(/Worktree feature\/relatorios excluído\./)).toBeInTheDocument()
    expect(prompts).toHaveLength(2)
    expect(wt.calls.deleted).toHaveLength(1)
    expect(screen.queryByRole('alert', { name: 'Salvamento do worktree' })).not.toBeInTheDocument()
  })

  it('does not delete while a program is using the folder, and deletes on the next check once it is closed', async () => {
    const user = userEvent.setup()
    const { backend, wt } = setup({ onPrompt: wt => {
      wt.settle(relatorios)
      // The terminal the user left open in the task worktree.
      wt.set(wt.items().map(item => item.path === relatorios ? { ...item, canDelete: false, blockers: [{ code: 'in_use', detail: 'zsh (pid 4242)', count: 1 }] } : item))
    } })
    render(<App backend={backend} />)
    await openProject(user, worktreeRoot)
    await openWorktreesPage(user)
    await startSaving(user)
    const alert = await screen.findByRole('alert', { name: 'Salvamento do worktree' })
    expect(alert).toHaveTextContent('1 programa está usando esta pasta (zsh (pid 4242)). Feche-o e confira de novo.')
    expect(alert).toHaveTextContent('Nada foi excluído. Depois de fechar, use Conferir agora.')
    expect(alert).not.toHaveTextContent('Se a conversa continuar')
    expect(wt.calls.deleted).toEqual([])

    // The terminal is closed; the next check finds the worktree free and removes it.
    wt.settle(relatorios)
    await user.click(within(alert).getByRole('button', { name: 'Conferir agora' }))
    expect(await screen.findByText(/Worktree feature\/relatorios excluído\./)).toBeInTheDocument()
    expect(wt.calls.deleted).toHaveLength(1)
  })

  it('keeps the worktree when the user chose to, and says it can go from the page', async () => {
    const user = userEvent.setup()
    const { backend, wt } = setup({ onPrompt: wt => wt.settle(relatorios) })
    render(<App backend={backend} />)
    await openProject(user, worktreeRoot)
    await openWorktreesPage(user)
    await startSaving(user, { keep: true })

    expect(await screen.findByText('Tudo salvo e mesclado em main. feature/relatorios já pode ser excluído na página Worktrees.')).toBeInTheDocument()
    expect(wt.calls.deleted).toEqual([])
    const notice = screen.getByRole('status', { name: 'Salvamento do worktree' })
    await user.click(within(notice).getByRole('button', { name: 'Abrir Worktrees' }))
    const row = (await screen.findByRole('list', { name: 'Worktrees do repositório' })).querySelector('li:has(strong[title="feature/relatorios"])') as HTMLElement
    expect(within(row).getByText('Pode excluir')).toBeInTheDocument()
  })

  it('lets the user check by hand when the automatic check cannot run', async () => {
    const user = userEvent.setup()
    const { backend, wt } = setup({ onPrompt: wt => wt.settle(relatorios) })
    let failing = true
    const real = backend.listWorktrees
    // The automatic check right after the run fails (say the disk is busy); the user asks again.
    backend.listWorktrees = async id => {
      if (failing && wt.calls.prepared.length > 0 && wt.calls.list >= 2) { failing = false; throw new Error('transient') }
      return real(id)
    }
    render(<App backend={backend} />)
    await openProject(user, worktreeRoot)
    await openWorktreesPage(user)
    await startSaving(user)
    const alert = await screen.findByRole('alert', { name: 'Salvamento do worktree' })
    expect(alert).toHaveTextContent('Não foi possível conferir feature/relatorios:')
    expect(alert).toHaveTextContent('Nada foi excluído.')
    expect(wt.calls.deleted).toEqual([])
    await user.click(within(alert).getByRole('button', { name: 'Conferir agora' }))
    expect(await screen.findByText(/Worktree feature\/relatorios excluído\./)).toBeInTheDocument()
    expect(wt.calls.deleted).toHaveLength(1)
  })

  it('moves the project to the main checkout where the merge happens', async () => {
    const user = userEvent.setup()
    const { backend, wt, opened } = setup({ onPrompt: wt => wt.settle(relatorios) })
    render(<App backend={backend} />)
    await openProject(user, relatorios)
    const region = await screen.findByRole('region', { name: 'Worktree da tarefa' })
    expect(region).toHaveTextContent('feature/relatorios')
    // Being the open project holds the worktree, but the header does not count it among the reasons.
    expect(region).toHaveTextContent('Não pode ser excluído: 3 alterações não salvas')
    await user.click(within(region).getByRole('button', { name: 'Salvar com a IA…' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText(/no projeto principal/)).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Começar' }))
    expect(await screen.findByText(/Worktree feature\/relatorios excluído\./)).toBeInTheDocument()
    expect(opened.mock.calls.map(call => call[0])).toEqual([relatorios, worktreeRoot])
    expect(wt.calls.deleted).toEqual([{ workspaceId: 'workspace-1', path: relatorios, deleteBranch: true, acknowledgeIgnored: true }])
  })

  it('does not delete the worktree the user is still working in', async () => {
    const user = userEvent.setup()
    const { backend, wt } = setup({ items: [mainWorktree(), worktreeItem()] })
    render(<App backend={backend} />)
    await openProject(user, '/synthetic/worktrees/exportacao')
    const region = await screen.findByRole('region', { name: 'Worktree da tarefa' })
    expect(region).toHaveTextContent('Tudo salvo e mesclado em main: pode ser excluído ao sair deste projeto.')
    await openWorktreesPage(user)
    const row = (await screen.findByRole('list', { name: 'Worktrees do repositório' })).querySelector('li:has(strong[title="feature/exportacao"])') as HTMLElement
    expect(within(row).getByText('Projeto aberto')).toBeInTheDocument()
    expect(within(row).getByText('Não pode excluir')).toBeInTheDocument()
    expect(within(row).getByText('É o projeto aberto agora. Abra outro projeto para poder excluí-lo.')).toBeInTheDocument()
    expect(within(row).queryByRole('button', { name: 'Excluir…' })).not.toBeInTheDocument()
    expect(wt.calls.deleted).toEqual([])
  })

  it('keeps the save notice in view in the Casual conversation, where the project header is hidden', async () => {
    localStorage.setItem('harflex:view-mode', 'casual')
    const user = userEvent.setup()
    const { backend, wt } = setup({ onPrompt: wt => wt.settle(relatorios) })
    render(<App backend={backend} />)
    await openProject(user, worktreeRoot)
    await user.click(await screen.findByText('Ferramentas', { exact: true }))
    await user.click(within(screen.getByRole('navigation', { name: 'Ferramentas' })).getByRole('button', { name: 'Worktrees' }))
    await screen.findByRole('heading', { name: 'Worktrees' })
    await startSaving(user)

    const notice = await screen.findByRole('status', { name: 'Salvamento do worktree' })
    await waitFor(() => expect(notice).toHaveTextContent('Worktree feature/relatorios excluído.'))
    expect(screen.getByRole('heading', { level: 1, name: /Conversa no projeto/ })).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Worktree da tarefa' })).not.toBeInTheDocument()
    expect(wt.calls.deleted).toHaveLength(1)
  })

  it('shows what is still pending in the Casual conversation instead of staying silent', async () => {
    localStorage.setItem('harflex:view-mode', 'casual')
    const user = userEvent.setup()
    const { backend, wt } = setup()
    render(<App backend={backend} />)
    await openProject(user, worktreeRoot)
    await user.click(await screen.findByText('Ferramentas', { exact: true }))
    await user.click(within(screen.getByRole('navigation', { name: 'Ferramentas' })).getByRole('button', { name: 'Worktrees' }))
    await screen.findByRole('heading', { name: 'Worktrees' })
    await startSaving(user)
    const alert = await screen.findByRole('alert', { name: 'Salvamento do worktree' })
    expect(alert).toHaveTextContent('Ainda não dá para excluir feature/relatorios:')
    expect(alert).toHaveTextContent('Nada foi excluído.')
    expect(wt.calls.deleted).toEqual([])
  })

  it('keeps the follow-up with the project it started in', async () => {
    const user = userEvent.setup()
    const { backend, wt } = setup()
    const elsewhere = '/synthetic/workspace/outro'
    const open = backend.openWorkspace
    backend.openWorkspace = async path => { const opened = await open(path); return path === elsewhere ? { ...opened, id: 'workspace-2' } : opened }
    const list = backend.listWorktrees
    backend.listWorktrees = async id => id === 'workspace-2' ? { isRepository: false, root: '', base: '', baseKnown: false, currentPath: '', items: [] } : list(id)
    render(<App backend={backend} />)
    await openProject(user, worktreeRoot)
    await openWorktreesPage(user)
    await startSaving(user)
    expect(await screen.findByRole('alert', { name: 'Salvamento do worktree' })).toHaveTextContent('Nada foi excluído.')

    // Another project is open: the notice is about a different repository, so it is not shown there.
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Projetos' }))
    await user.clear(await screen.findByLabelText('Caminho da pasta'))
    await user.type(screen.getByLabelText('Caminho da pasta'), elsewhere)
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    await screen.findByRole('heading', { level: 1, name: 'outro' })
    expect(screen.queryByRole('alert', { name: 'Salvamento do worktree' })).not.toBeInTheDocument()
    expect(screen.queryByRole('status', { name: 'Salvamento do worktree' })).not.toBeInTheDocument()

    // Back in the first one it is still there, and nothing was ever deleted.
    await user.click(within(screen.getByRole('navigation', { name: 'Navegação principal' })).getByRole('button', { name: 'Projetos' }))
    await user.clear(await screen.findByLabelText('Caminho da pasta'))
    await user.type(screen.getByLabelText('Caminho da pasta'), worktreeRoot)
    await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
    expect(await screen.findByRole('alert', { name: 'Salvamento do worktree' })).toHaveTextContent('Nada foi excluído.')
    expect(wt.calls.deleted).toEqual([])
  })

  describe('with a CLI assistant', () => {
    const catalog = (overrides: Partial<ModelCatalogResult> = {}): ModelCatalogResult => ({
      backendId: 'codex', source: 'codex_app_server', destination: 'Codex CLI', profileRevision: 'rev-1', searchTerm: '',
      models: [{ id: 'runtime-model', displayName: 'Modelo dinâmico', backendId: 'codex', source: 'codex_app_server', availability: 'listed' }],
      nextCursor: '', checkedAt: sessionMetadata.createdAt, status: 'complete', complete: true, accountFiltered: true, ...overrides,
    })
    const withCLI = (backend: Backend, settings: { defaultModelBackendId: string; defaultModelId: string }) => {
      backend.listBackends = async () => [{ id: 'codex', name: 'Codex', kind: 'cli', available: true }]
      backend.getSettings = async () => ({ defaultBackendId: 'codex', ...settings })
    }

    it('refuses a CLI that has no saved default model, before creating any session', async () => {
      const user = userEvent.setup()
      const { backend, wt, direct } = setup()
      withCLI(backend, { defaultModelBackendId: '', defaultModelId: '' })
      render(<App backend={backend} />)
      await openProject(user, worktreeRoot)
      await openWorktreesPage(user)
      await startSaving(user)
      const dialog = await screen.findByRole('dialog')
      expect(await within(dialog).findByText(/um CLI precisa de um modelo padrão salvo em Configurações/)).toBeInTheDocument()
      expect(direct).not.toHaveBeenCalled()
      expect(wt.calls.deleted).toEqual([])
    })

    it('confirms the default model in the CLI catalog and carries it into the session', async () => {
      const user = userEvent.setup()
      const { backend, direct } = setup({ onPrompt: wt => wt.settle(relatorios) })
      withCLI(backend, { defaultModelBackendId: 'codex', defaultModelId: 'runtime-model' })
      const query = vi.fn(async () => catalog())
      backend.queryCLIModelCatalog = query
      render(<App backend={backend} />)
      await openProject(user, worktreeRoot)
      await openWorktreesPage(user)
      await startSaving(user)
      expect(await screen.findByText(/Worktree feature\/relatorios excluído\./)).toBeInTheDocument()
      expect(query).toHaveBeenCalledWith({ workspaceId: 'workspace-1', backendId: 'codex' })
      expect(direct).toHaveBeenCalledWith({ workspaceId: 'workspace-1', backendId: 'codex', reason: 'Salvar e mesclar o worktree feature/relatorios', modelId: 'runtime-model', reasoningEffort: '', catalogRevision: 'rev-1' })
    })

    it('does not start when the catalog cannot confirm the saved model', async () => {
      const user = userEvent.setup()
      const { backend, direct, prompts } = setup()
      withCLI(backend, { defaultModelBackendId: 'codex', defaultModelId: 'runtime-model' })
      backend.queryCLIModelCatalog = async () => catalog({ models: [] , status: 'empty' })
      render(<App backend={backend} />)
      await openProject(user, worktreeRoot)
      await openWorktreesPage(user)
      await startSaving(user)
      const dialog = await screen.findByRole('dialog')
      expect(await within(dialog).findByText(/O catálogo do CLI não confirmou o modelo padrão/)).toBeInTheDocument()
      expect(direct).not.toHaveBeenCalled()
      expect(prompts).toEqual([])
    })
  })

  it('does not start a second conversation while one is running', async () => {
    const user = userEvent.setup()
    const { backend, wt } = setup({ onPrompt: wt => wt.settle(relatorios) })
    let release: () => void = () => undefined
    const gate = new Promise<void>(resolve => { release = resolve })
    const respond = backend.prompt
    backend.prompt = async (session, text) => { await gate; return respond(session, text) }
    render(<App backend={backend} />)
    await openProject(user, worktreeRoot)
    await openWorktreesPage(user)
    await startSaving(user)
    // Still running: the page, once opened, must not let a second save in.
    await waitFor(() => expect(screen.getByRole('status', { name: 'Salvamento do worktree' })).toHaveTextContent('A IA está salvando feature/relatorios na conversa.'))
    await openWorktreesPage(user)
    await user.click(await screen.findByRole('button', { name: 'Salvar e mesclar com a IA…' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: 'Começar' }))
    expect(await within(dialog).findByRole('alert')).toBeInTheDocument()
    expect(wt.calls.deleted).toEqual([])
    release()
    expect(await screen.findByText(/Worktree feature\/relatorios excluído\./, undefined, { timeout: 3000 })).toBeInTheDocument()
  })
})
