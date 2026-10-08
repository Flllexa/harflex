import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Backend, Workspace } from '../../lib/backend'
import { createFakeBackend } from '../../test/fakeBackend'
import { PermissionPicker } from './PermissionPicker'

afterEach(cleanup)

const workspace = (profile: string): Workspace => ({ id: 'workspace-1', path: '/synthetic/workspace', profile })

function show(profile: string, backend: Backend = createFakeBackend().backend) {
  const updated = vi.fn()
  render(<PermissionPicker backend={backend} workspace={workspace(profile)} onWorkspaceUpdated={updated} />)
  return { updated, backend }
}

async function choose(user: ReturnType<typeof userEvent.setup>, name: string) {
  await user.click(within(screen.getByTestId('picker-permission-profile')).getByRole('button'))
  await user.click(screen.getByRole('option', { name }))
}

describe('Permission picker', () => {
  it('names the project profile and changes it without asking when approvals stay on', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const save = vi.spyOn(backend, 'setWorkspaceProfile')
    const { updated } = show('ask', backend)
    const trigger = within(screen.getByTestId('picker-permission-profile')).getByRole('button')
    expect(trigger).toHaveAccessibleName(/Permissões do projeto/)
    expect(trigger).toHaveTextContent('Perguntar')
    await choose(user, 'Workspace confiável')
    expect(save).toHaveBeenCalledWith('workspace-1', 'trusted_workspace', undefined)
    expect(updated).toHaveBeenCalledWith({ id: 'workspace-1', path: '/synthetic/workspace', profile: 'trusted_workspace' })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('turns approvals off only after the person confirms in the dialog', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const save = vi.spyOn(backend, 'setWorkspaceProfile')
    const { updated } = show('trusted_workspace', backend)
    await choose(user, 'Acesso total')
    const dialog = await screen.findByRole('dialog', { name: 'Ativar acesso total neste projeto?' })
    expect(dialog).toHaveTextContent('sem pedir a sua aprovação')
    expect(save).not.toHaveBeenCalled()
    await user.click(within(dialog).getByRole('button', { name: 'Cancelar' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(save).not.toHaveBeenCalled()
    expect(updated).not.toHaveBeenCalled()

    await choose(user, 'Acesso total')
    await user.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Ativar acesso total' }))
    expect(save).toHaveBeenCalledWith('workspace-1', 'full_access', { confirmFullAccess: true })
    expect(updated).toHaveBeenCalledWith({ id: 'workspace-1', path: '/synthetic/workspace', profile: 'full_access' })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('keeps the dialog open and explains a refusal', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    backend.setWorkspaceProfile = async () => { throw { cause: { code: 'invalid_input' } } }
    const { updated } = show('ask', backend)
    await choose(user, 'Acesso total')
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: 'Ativar acesso total' }))
    expect(await within(dialog).findByRole('alert')).toBeInTheDocument()
    expect(updated).not.toHaveBeenCalled()
  })

  it('shows that nothing is asked while full access is on, and leaving it needs no confirmation', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    const save = vi.spyOn(backend, 'setWorkspaceProfile')
    show('full_access', backend)
    expect(screen.getByText('Sem pedir aprovação')).toBeInTheDocument()
    expect(within(screen.getByTestId('picker-permission-profile')).getByRole('button')).toHaveTextContent('Acesso total')
    await choose(user, 'Perguntar')
    expect(save).toHaveBeenCalledWith('workspace-1', 'ask', undefined)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('does not hide a profile the menu cannot choose', () => {
    show('sandbox')
    expect(within(screen.getByTestId('picker-permission-profile')).getByRole('button')).toHaveTextContent('Sandbox (indisponível)')
  })
})
