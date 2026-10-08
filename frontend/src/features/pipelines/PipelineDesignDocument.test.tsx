import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { createFakeBackend } from '../../test/fakeBackend'
import { designReadback } from '../../test/pipelineDesignFixture'
import { PipelineDesignDocument } from './PipelineDesignDocument'

function setup() {
  const { backend } = createFakeBackend(), design = designReadback(undefined, true)
  const props = { backend, design, stage: 'spec' as const, disabled: false, readOnly: false, onStageChange: vi.fn(), onEditingChange: vi.fn(), onSave: vi.fn(async () => true), onRestore: vi.fn(async () => true) }
  return props
}

it('restores an unsaved manual edit and notifies the parent after navigation in the same session', async () => {
  const props = setup(), writes = vi.spyOn(Storage.prototype, 'setItem'), user = userEvent.setup()
  const view = render(<PipelineDesignDocument {...props} />)
  await user.click(screen.getByRole('button', { name: 'Editar SPEC' }))
  await user.clear(screen.getByLabelText('Texto de SPEC')); await user.type(screen.getByLabelText('Texto de SPEC'), 'Rascunho manual preservado.')
  view.unmount(); props.onEditingChange.mockClear()
  render(<PipelineDesignDocument {...props} />)
  expect(await screen.findByLabelText('Texto de SPEC')).toHaveValue('Rascunho manual preservado.')
  expect(props.onEditingChange).toHaveBeenCalledWith(true)
  expect(screen.getByRole('button', { name: 'Salvar SPEC' })).toBeEnabled()
  expect(writes).not.toHaveBeenCalled(); writes.mockRestore()
})

it.each(['save', 'cancel'] as const)('clears the cached editor only after explicit %s', async action => {
  const props = setup(), user = userEvent.setup(), view = render(<PipelineDesignDocument {...props} />)
  await user.click(screen.getByRole('button', { name: 'Editar SPEC' }))
  await user.clear(screen.getByLabelText('Texto de SPEC')); await user.type(screen.getByLabelText('Texto de SPEC'), 'Texto ainda local.')
  await user.click(screen.getByRole('button', { name: action === 'save' ? 'Salvar SPEC' : 'Cancelar edição' }))
  await waitFor(() => expect(screen.queryByLabelText('Texto de SPEC')).not.toBeInTheDocument())
  view.unmount(); render(<PipelineDesignDocument {...props} />)
  expect(screen.queryByLabelText('Texto de SPEC')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Editar SPEC' })).toBeInTheDocument()
})

it('preserves an old-base draft and blocks saving until the current version is compared and adopted explicitly', async () => {
  const props = setup(), user = userEvent.setup(), view = render(<PipelineDesignDocument {...props} />)
  await user.click(screen.getByRole('button', { name: 'Editar SPEC' }))
  await user.clear(screen.getByLabelText('Texto de SPEC')); await user.type(screen.getByLabelText('Texto de SPEC'), 'Meu texto local sem rebase.')
  view.unmount()
  const changed = { ...props.design, documents: { ...props.design.documents, spec: { ...props.design.documents.spec, version: 2, content: '# SPEC recebida\n\nMudança externa confirmada.', contentDigest: 'f'.repeat(64) } } }
  render(<PipelineDesignDocument {...props} design={changed} />)
  expect(await screen.findByLabelText('Texto de SPEC')).toHaveValue('Meu texto local sem rebase.')
  expect(screen.getByRole('button', { name: 'Salvar SPEC' })).toBeDisabled()
  expect(screen.getByRole('alert')).toHaveTextContent(/rascunho foi preservado/)
  await user.click(screen.getByText('Comparar com a versão atual'))
  expect(screen.getByText('Mudança externa confirmada.')).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Usar versão atual como base' }))
  expect(screen.getByLabelText('Texto de SPEC')).toHaveValue('Meu texto local sem rebase.')
  expect(screen.getByRole('button', { name: 'Salvar SPEC' })).toBeEnabled()
  await user.click(screen.getByRole('button', { name: 'Salvar SPEC' }))
  expect(props.onSave).toHaveBeenCalledWith('spec', 'Meu texto local sem rebase.')
})

it('requires an explicit source comparison when Discovery changes without changing the SPEC version', async () => {
  const props = setup(), user = userEvent.setup(), view = render(<PipelineDesignDocument {...props} />)
  await user.click(screen.getByRole('button', { name: 'Editar SPEC' }))
  await user.clear(screen.getByLabelText('Texto de SPEC')); await user.type(screen.getByLabelText('Texto de SPEC'), 'Requisito em revisão.')
  view.unmount()
  const changed = { ...props.design, documents: { ...props.design.documents, discovery: { ...props.design.documents.discovery, version: 2, content: '# Discovery\n\nNovo contexto da demanda.', contentDigest: 'e'.repeat(64) }, spec: { ...props.design.documents.spec, stale: true } } }
  render(<PipelineDesignDocument {...props} design={changed} />)
  expect(await screen.findByLabelText('Texto de SPEC')).toHaveValue('Requisito em revisão.')
  expect(screen.getByRole('button', { name: 'Salvar SPEC' })).toBeDisabled()
  await user.click(screen.getByText('Comparar com a versão atual'))
  expect(screen.getByText('Novo contexto da demanda.')).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Usar versão atual como base' }))
  expect(screen.getByRole('button', { name: 'Salvar SPEC' })).toBeEnabled()
})

it('isolates editor drafts by backend, workspace, pipeline and stage', async () => {
  const props = setup(), user = userEvent.setup(), view = render(<PipelineDesignDocument {...props} />)
  await user.click(screen.getByRole('button', { name: 'Editar SPEC' }))
  await user.clear(screen.getByLabelText('Texto de SPEC')); await user.type(screen.getByLabelText('Texto de SPEC'), 'Somente deste contexto.')
  view.unmount()
  const otherBackend = createFakeBackend().backend
  const other = render(<PipelineDesignDocument {...props} backend={otherBackend} />)
  expect(screen.queryByLabelText('Texto de SPEC')).not.toBeInTheDocument(); other.unmount()
  const otherWorkspace = render(<PipelineDesignDocument {...props} design={{ ...props.design, workspaceId: 'other-workspace' }} />)
  expect(screen.queryByLabelText('Texto de SPEC')).not.toBeInTheDocument(); otherWorkspace.unmount()
  const otherPipeline = render(<PipelineDesignDocument {...props} design={{ ...props.design, pipelineId: 'other-pipeline' }} />)
  expect(screen.queryByLabelText('Texto de SPEC')).not.toBeInTheDocument(); otherPipeline.unmount()
  const otherStage = render(<PipelineDesignDocument {...props} stage="plan" />)
  expect(screen.queryByLabelText('Texto de Plan')).not.toBeInTheDocument(); otherStage.unmount()
  render(<PipelineDesignDocument {...props} />)
  expect(screen.getByLabelText('Texto de SPEC')).toHaveValue('Somente deste contexto.')
})

it('does not let an old save response erase a newer draft after navigation', async () => {
  const props = setup(), user = userEvent.setup()
  let resolve!: (value: boolean) => void
  props.onSave = vi.fn(() => new Promise<boolean>(done => { resolve = done }))
  const view = render(<PipelineDesignDocument {...props} />)
  await user.click(screen.getByRole('button', { name: 'Editar SPEC' }))
  await user.clear(screen.getByLabelText('Texto de SPEC')); await user.type(screen.getByLabelText('Texto de SPEC'), 'Texto enviado para salvar.')
  await user.click(screen.getByRole('button', { name: 'Salvar SPEC' })); view.unmount()
  const next = render(<PipelineDesignDocument {...props} />)
  await user.clear(await screen.findByLabelText('Texto de SPEC')); await user.type(screen.getByLabelText('Texto de SPEC'), 'Nova edição após voltar.')
  await act(async () => resolve(true)); next.unmount()
  render(<PipelineDesignDocument {...props} />)
  expect(screen.getByLabelText('Texto de SPEC')).toHaveValue('Nova edição após voltar.')
})
