import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ProjectMemory } from '../../lib/backend'
import { createFakeBackend } from '../../test/fakeBackend'
import { ProjectMemoryLine } from './ProjectMemory'

afterEach(() => { cleanup(); vi.useRealTimers() })

const memory = (over: Partial<ProjectMemory> = {}): ProjectMemory => ({ workspaceId: 'workspace-1', status: 'ready', content: '## Tecnologias\n- Go 1.26\n- Java 21', sources: ['README.md', 'go.mod'],
  backendId: 'claude', modelId: 'sonnet', edited: false, updatedAt: '2026-10-02T12:00:00Z', ...over })

describe('project memory on the card', () => {
  it('shows a ready memory, opens it and saves an edit', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    backend.getProjectMemory = vi.fn(async () => memory())
    const save = vi.spyOn(backend, 'saveProjectMemory')
    render(<ProjectMemoryLine backend={backend} workspaceId="workspace-1" name="cobranca" />)
    expect(await screen.findByText('Memória pronta · 2 arquivos lidos')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Ver a memória de cobranca' }))
    const dialog = await screen.findByRole('dialog', { name: 'Memória de cobranca' })
    expect(within(dialog).getByText('Java 21')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Editar' }))
    const text = within(dialog).getByLabelText('Texto da memória')
    await user.clear(text)
    await user.type(text, '## Tecnologias{enter}- Kotlin')
    await user.click(within(dialog).getByRole('button', { name: 'Salvar memória' }))
    await waitFor(() => expect(save).toHaveBeenCalledWith('workspace-1', '## Tecnologias\n- Kotlin'))
    expect(await screen.findByText('Memória pronta · editada por você')).toBeInTheDocument()
  })

  it('follows a background read until it ends', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const { backend } = createFakeBackend()
    backend.getProjectMemory = vi.fn().mockResolvedValueOnce(memory({ status: 'reading', content: '', sources: [] })).mockResolvedValue(memory())
    render(<ProjectMemoryLine backend={backend} workspaceId="workspace-1" name="cobranca" />)
    expect(await screen.findByText('A IA está lendo o projeto…')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Ler/ })).not.toBeInTheDocument()
    await vi.advanceTimersByTimeAsync(3100)
    expect(await screen.findByText('Memória pronta · 2 arquivos lidos')).toBeInTheDocument()
  })

  it('asks for an AI when none was chosen and starts a read on request', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    backend.getProjectMemory = vi.fn(async () => memory({ status: 'needs_model', content: '', sources: [] }))
    const refresh = vi.spyOn(backend, 'refreshProjectMemory')
    render(<ProjectMemoryLine backend={backend} workspaceId="workspace-1" name="cobranca" />)
    expect(await screen.findByText(/Escolha uma IA para a fase Discovery/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Ler o projeto cobranca com a IA' }))
    expect(refresh).toHaveBeenCalledWith('workspace-1')
    expect(await screen.findByText('A IA está lendo o projeto…')).toBeInTheDocument()
  })
})
