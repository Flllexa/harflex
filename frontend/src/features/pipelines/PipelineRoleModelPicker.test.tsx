import { act, cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Backend, BackendOption, ModelCatalogResult, PipelineRoleModelSelection } from '../../lib/backend'
import { createFakeBackend } from '../../test/fakeBackend'
import { PipelineRoleModelPicker } from './PipelineRoleModelPicker'

// Only the clock and the slow tick are faked: promises and timeouts stay real, so the picker's own requests resolve as usual.
beforeEach(() => { vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] }) })
afterEach(() => { cleanup(); vi.useRealTimers() })

const apiBackend: BackendOption = { id: 'api', name: 'API', kind: 'api', available: true }
const profile = { id: 'api', name: 'API', kind: 'openai_compatible', providerType: 'openai' as const, baseUrl: 'https://example.test/v1', model: 'profile-model', hasCredential: true, endpointBlocked: false, updatedAt: '2026-10-02T10:00:00Z' }

function catalogAt(checkedAt: Date): ModelCatalogResult {
  return { backendId: 'api', source: 'openai_models', destination: 'API', profileRevision: 'revision', credentialToken: 'a'.repeat(64), searchTerm: '',
    models: ['profile-model', 'large-model'].map(id => ({ id, displayName: id, backendId: 'api', source: 'openai_models', availability: 'available' as const })),
    nextCursor: '', checkedAt: checkedAt.toISOString(), status: 'complete', complete: true, accountFiltered: true }
}

function pickerWith(age = 0) {
  const { backend } = createFakeBackend()
  backend.listProviderProfiles = async () => [profile]
  const query = vi.fn(async () => catalogAt(new Date(Date.now() - age)))
  backend.queryHTTPModelCatalog = query as Backend['queryHTTPModelCatalog']
  const onSelectionChange = vi.fn<(selection?: PipelineRoleModelSelection) => void>()
  render(<PipelineRoleModelPicker backend={backend} workspaceId="workspace-1" stage="code" backendOption={apiBackend} defaultModelBackendId="" defaultModelId="" disabled={false} onSelectionChange={onSelectionChange} />)
  return { query, onSelectionChange }
}

const settle = () => act(async () => { await vi.advanceTimersByTimeAsync(0) })
const wait = (milliseconds: number) => act(async () => { await vi.advanceTimersByTimeAsync(milliseconds) })
const comboboxOf = () => within(screen.getByTestId('picker-pipeline-role-model-code')).getByRole('combobox', { name: 'Modelo da fase Code' })

describe('the catalog answer behind a pick', () => {
  it('stays a pick for a few minutes, then is read again by itself, keeping the model', async () => {
    const { query, onSelectionChange } = pickerWith()
    await settle()
    expect(onSelectionChange).toHaveBeenLastCalledWith(expect.objectContaining({ profileId: 'api', modelId: 'profile-model' }))
    expect(comboboxOf()).toHaveValue('profile-model')
    expect(comboboxOf()).toBeEnabled()

    await wait(240_000)
    expect(onSelectionChange).toHaveBeenLastCalledWith(expect.objectContaining({ modelId: 'profile-model' }))
    expect(screen.queryByText(/O catálogo de modelos ficou antigo/)).not.toBeInTheDocument()

    // The backend refuses an answer past five minutes; before that the picker reads the catalog again by itself.
    await wait(90_000)
    await settle()
    expect(onSelectionChange).toHaveBeenCalledWith(undefined)
    expect(query).toHaveBeenCalledTimes(2)
    expect(query).toHaveBeenLastCalledWith(expect.objectContaining({ profileId: 'api', refresh: true }), expect.anything())
    expect(onSelectionChange).toHaveBeenLastCalledWith(expect.objectContaining({ modelId: 'profile-model' }))
    expect(screen.queryByText(/O catálogo de modelos ficou antigo/)).not.toBeInTheDocument()
    expect(comboboxOf()).toBeEnabled()
  })

  it('is refused already on arrival when it is older than the picker lets a pick live', async () => {
    const { onSelectionChange } = pickerWith(280_000)
    await settle()
    expect(screen.getByText('Não foi possível confirmar o catálogo deste executor.')).toBeInTheDocument()
    expect(onSelectionChange).not.toHaveBeenCalledWith(expect.objectContaining({ modelId: expect.anything() }))
  })

  it('is taken while it is a little under the limit', async () => {
    const { onSelectionChange } = pickerWith(200_000)
    await settle()
    expect(onSelectionChange).toHaveBeenLastCalledWith(expect.objectContaining({ modelId: 'profile-model' }))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
