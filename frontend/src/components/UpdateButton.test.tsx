import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Backend, UpdateInfo, UpdateState } from '../lib/backend'
import { createFakeBackend } from '../test/fakeBackend'
import { UpdateButton } from './UpdateButton'

const desktop = vi.hoisted(() => ({ openExternal: vi.fn(async () => true), copyText: vi.fn(async () => true) }))
vi.mock('../lib/desktop', () => desktop)

const info = (patch: Partial<UpdateInfo> = {}): UpdateInfo => ({ currentVersion: '0.2.2', available: true, version: '0.3.0', pageUrl: 'https://github.com/Flllexa/harflex/releases/tag/v0.3.0', canInstall: true, ...patch })

function fixture(found: UpdateInfo | Error) {
  const { backend } = createFakeBackend()
  let push: (state: UpdateState) => void = () => undefined
  backend.checkForUpdate = vi.fn(async () => { if (found instanceof Error) throw found; return found })
  backend.installUpdate = vi.fn(async () => undefined)
  backend.onUpdateState = vi.fn(listener => { push = listener; return () => undefined })
  return { backend: backend as Backend, send: (state: UpdateState) => act(() => push(state)) }
}

beforeEach(() => desktop.openExternal.mockClear())

describe('update button beside the logo', () => {
  it('shows nothing while there is no newer version, or when the check cannot run', async () => {
    const current = fixture(info({ available: false, version: '' }))
    const { container } = render(<UpdateButton backend={current.backend} />)
    await vi.waitFor(() => expect(current.backend.checkForUpdate).toHaveBeenCalled())
    expect(container).toBeEmptyDOMElement()
    const offline = fixture(new Error('offline'))
    const second = render(<UpdateButton backend={offline.backend} />)
    await vi.waitFor(() => expect(offline.backend.checkForUpdate).toHaveBeenCalled())
    expect(second.container).toBeEmptyDOMElement()
  })

  it('updates with one click and follows the download until the app restarts', async () => {
    const user = userEvent.setup()
    const { backend, send } = fixture(info())
    render(<UpdateButton backend={backend} />)
    await user.click(await screen.findByRole('button', { name: 'Atualizar para v0.3.0' }))
    expect(backend.installUpdate).toHaveBeenCalledTimes(1)
    expect(await screen.findByRole('status')).toHaveTextContent('Baixando 0%')
    send({ phase: 'downloading', percent: 42, errorCode: '' })
    expect(screen.getByRole('status')).toHaveTextContent('Baixando 42%')
    send({ phase: 'installing', percent: 100, errorCode: '' })
    expect(screen.getByRole('status')).toHaveTextContent('Instalando…')
    send({ phase: 'restarting', percent: 100, errorCode: '' })
    expect(screen.getByRole('status')).toHaveTextContent('Reiniciando…')
    expect(screen.queryByRole('button', { name: /Atualizar para/ })).not.toBeInTheDocument()
  })

  it('says why it failed and offers the manual download, then lets the person try again', async () => {
    const user = userEvent.setup()
    const { backend, send } = fixture(info())
    render(<UpdateButton backend={backend} />)
    await user.click(await screen.findByRole('button', { name: 'Atualizar para v0.3.0' }))
    send({ phase: 'failed', percent: 0, errorCode: 'update_not_writable' })
    expect(await screen.findByRole('alert')).toHaveTextContent('O app está numa pasta que você não pode alterar')
    await user.click(screen.getByRole('button', { name: 'Baixar a versão' }))
    expect(desktop.openExternal).toHaveBeenCalledWith('https://github.com/Flllexa/harflex/releases/tag/v0.3.0')
    await user.click(screen.getByRole('button', { name: 'Atualizar para v0.3.0' }))
    expect(backend.installUpdate).toHaveBeenCalledTimes(2)
    send({ phase: 'failed', percent: 0, errorCode: 'update_checksum' })
    expect(await screen.findByRole('alert')).toHaveTextContent('não confere com o publicado')
  })

  it('opens the release page when this copy cannot install by itself', async () => {
    const user = userEvent.setup()
    const { backend } = fixture(info({ canInstall: false }))
    render(<UpdateButton backend={backend} />)
    await user.click(await screen.findByRole('button', { name: 'Baixar v0.3.0' }))
    expect(desktop.openExternal).toHaveBeenCalledWith('https://github.com/Flllexa/harflex/releases/tag/v0.3.0')
    expect(backend.installUpdate).not.toHaveBeenCalled()
  })

  it('does not ask GitHub again when the sidebar is mounted again soon after', async () => {
    const { backend } = fixture(info())
    const first = render(<UpdateButton backend={backend} />)
    await screen.findByRole('button', { name: 'Atualizar para v0.3.0' })
    first.unmount()
    render(<UpdateButton backend={backend} />)
    expect(await screen.findByRole('button', { name: 'Atualizar para v0.3.0' })).toBeInTheDocument()
    expect(backend.checkForUpdate).toHaveBeenCalledTimes(1)
  })
})
