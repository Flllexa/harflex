import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { createFakeBackend } from '../../test/fakeBackend'
import { SettingsPage } from './SettingsPage'

afterEach(cleanup)

it('não oferece backend indisponível como preferência', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  backend.listProviderProfiles = async () => []
  backend.getSettings = async () => ({ defaultBackendId: '', defaultModelBackendId: '', defaultModelId: '' })
  render(<SettingsPage backend={backend} backends={[{ id: 'local', name: 'Local', kind: 'api', available: true }, { id: 'off', name: 'Offline', kind: 'api', available: false }]} connectionState="ready" onWorkspaceUpdated={() => undefined} onBackendSaved={() => undefined} onDefaultSaved={() => undefined} />)
  await user.click(within(await screen.findByTestId('picker-settings-default-backend')).getByRole('button'))
  expect(screen.getByRole('option', { name: 'Offline · indisponível' })).toHaveAttribute('aria-disabled', 'true')
})

it('salva um modelo padrão do Codex CLI associado ao provedor configurado', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  backend.listProviderProfiles = async () => []
  backend.getSettings = async () => ({ defaultBackendId: 'codex', defaultModelBackendId: '', defaultModelId: '' })
  backend.queryCLIModelCatalog = vi.fn(async query => ({ backendId: query.backendId, source: 'codex_app_server', destination: 'Codex CLI', profileRevision: 'codex-revision', searchTerm: '',
    models: [{ id: 'gpt-5-codex', displayName: 'GPT-5 Codex', backendId: 'codex', source: 'codex_app_server', availability: 'listed' }], nextCursor: '', checkedAt: new Date().toISOString(), status: 'complete' as const, complete: true, accountFiltered: true }))
  const save = vi.fn(async (input: { defaultBackendId: string; defaultModelBackendId: string; defaultModelId: string }) => input)
  backend.saveSettings = save
  render(<SettingsPage backend={backend} backends={[{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }]} connectionState="ready" workspace={{ id: 'workspace-1', path: '/tmp/project', profile: 'ask' }} onWorkspaceUpdated={() => undefined} onBackendSaved={() => undefined} onDefaultSaved={() => undefined} />)

  await user.click(await screen.findByRole('button', { name: 'Atualizar modelos' }))
  const model = await screen.findByTestId('picker-settings-default-model')
  await user.click(within(model).getByRole('button', { name: /Abrir opções de Modelo padrão do SDD/ }))
  await user.click(await screen.findByRole('option', { name: 'GPT-5 Codex' }))
  await user.click(screen.getByRole('button', { name: 'Salvar preferências' }))

  expect(save).toHaveBeenCalledWith({ defaultBackendId: 'codex', defaultModelBackendId: 'codex', defaultModelId: 'gpt-5-codex' })
})

it('não oferece modelo padrão do SDD para um CLI que o SDD ainda não suporta', async () => {
  const { backend } = createFakeBackend()
  backend.listProviderProfiles = async () => []
  backend.getSettings = async () => ({ defaultBackendId: 'opencode', defaultModelBackendId: '', defaultModelId: '' })
  render(<SettingsPage backend={backend} backends={[{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }, { id: 'opencode', name: 'OpenCode', kind: 'cli', available: true }]} connectionState="ready" workspace={{ id: 'workspace-1', path: '/tmp/project', profile: 'ask' }} onWorkspaceUpdated={() => undefined} onBackendSaved={() => undefined} onDefaultSaved={() => undefined} />)

  expect(await screen.findByText(/não é compatível com o SDD/i)).toBeInTheDocument()
  expect(screen.getByTestId('picker-settings-default-model').querySelector('input')).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Atualizar modelos' })).toBeDisabled()
})

it('rejeita um padrão de modelo SDD para um perfil API genérico', async () => {
  const { backend } = createFakeBackend()
  backend.listProviderProfiles = async () => [{ id: 'generic', name: 'API genérica', kind: 'openai_compatible', providerType: 'generic', baseUrl: 'https://example.test/v1', model: 'generic-model', hasCredential: true, endpointBlocked: false, updatedAt: new Date().toISOString() }]
  backend.getSettings = async () => ({ defaultBackendId: 'generic', defaultModelBackendId: '', defaultModelId: '' })
  render(<SettingsPage backend={backend} backends={[{ id: 'generic', name: 'API genérica', kind: 'api', available: true }]} connectionState="ready" onWorkspaceUpdated={() => undefined} onBackendSaved={() => undefined} onDefaultSaved={() => undefined} />)

  expect(await screen.findByText(/não é compatível com o SDD/i)).toBeInTheDocument()
  expect(screen.getByTestId('picker-settings-default-model').querySelector('input')).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Atualizar modelos' })).toBeDisabled()
})

it('preserva o modelo padrão salvo enquanto os provedores ainda estão carregando', async () => {
  const { backend } = createFakeBackend()
  backend.listProviderProfiles = async () => []
  backend.getSettings = async () => ({ defaultBackendId: 'codex', defaultModelBackendId: 'codex', defaultModelId: 'gpt-5-codex' })
  const props = {
    backend,
    backends: [],
    connectionState: 'connecting' as const,
    workspace: { id: 'workspace-1', path: '/tmp/project', profile: 'ask' as const },
    onWorkspaceUpdated: () => undefined,
    onBackendSaved: () => undefined,
    onDefaultSaved: () => undefined,
  }
  const { rerender } = render(<SettingsPage {...props} />)

  expect(await screen.findByRole('status')).toHaveTextContent('Carregando configurações')

  rerender(<SettingsPage {...props} connectionState="ready" backends={[{ id: 'codex', name: 'Codex CLI', kind: 'cli', available: true }]} />)
  const model = await screen.findByTestId('picker-settings-default-model')
  expect(model.querySelector('input')).toHaveValue('gpt-5-codex · atualize o catálogo para confirmar')
  expect(screen.getByRole('button', { name: 'Salvar preferências' })).toBeDisabled()
})
