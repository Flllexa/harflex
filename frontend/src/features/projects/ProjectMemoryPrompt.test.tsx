import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import App from '../../app/App'
import { createFakeBackend } from '../../test/fakeBackend'

afterEach(() => { cleanup(); localStorage.clear() })

async function openProject(backend: ReturnType<typeof createFakeBackend>['backend']) {
  const user = userEvent.setup()
  render(<App backend={backend} />)
  await user.type(await screen.findByLabelText('Caminho da pasta'), '/synthetic/cobranca')
  await user.click(screen.getByRole('button', { name: 'Abrir projeto' }))
  return user
}

const neverRead = (workspaceId: string) => ({ workspaceId, status: '' as const, content: '', sources: [], backendId: '', modelId: '', edited: false, updatedAt: '0001-01-01T00:00:00Z' })

describe('asking to read a new project', () => {
  it('asks when a project that was never read is opened, and starts the read on yes', async () => {
    const { backend } = createFakeBackend()
    backend.getProjectMemory = vi.fn(async workspaceId => neverRead(workspaceId))
    const refresh = vi.spyOn(backend, 'refreshProjectMemory')
    const user = await openProject(backend)
    const dialog = await screen.findByRole('dialog', { name: 'Ler o projeto com a IA?' })
    expect(within(dialog).getByText('cobranca')).toBeInTheDocument()
    expect(refresh).not.toHaveBeenCalled()
    await user.click(within(dialog).getByRole('button', { name: 'Ler o projeto' }))
    await waitFor(() => expect(refresh).toHaveBeenCalledWith('workspace-1'))
    expect(await screen.findByText(/A IA está lendo cobranca para a memória do projeto/)).toBeInTheDocument()
    expect(screen.queryByRole('dialog', { name: 'Ler o projeto com a IA?' })).not.toBeInTheDocument()
  })

  it('records "not now" and does not read', async () => {
    const { backend } = createFakeBackend()
    backend.getProjectMemory = vi.fn(async workspaceId => neverRead(workspaceId))
    const decline = vi.spyOn(backend, 'declineProjectMemory'), refresh = vi.spyOn(backend, 'refreshProjectMemory')
    const user = await openProject(backend)
    await user.click(within(await screen.findByRole('dialog', { name: 'Ler o projeto com a IA?' })).getByRole('button', { name: 'Agora não' }))
    await waitFor(() => expect(decline).toHaveBeenCalledWith('workspace-1'))
    expect(refresh).not.toHaveBeenCalled()
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Ler o projeto com a IA?' })).not.toBeInTheDocument())
  })

  it('does not ask for a project that was already read or answered', async () => {
    const { backend } = createFakeBackend()
    const read = vi.spyOn(backend, 'getProjectMemory')
    await openProject(backend)
    await waitFor(() => expect(read).toHaveBeenCalled())
    expect(screen.queryByRole('dialog', { name: 'Ler o projeto com a IA?' })).not.toBeInTheDocument()
  })
})
