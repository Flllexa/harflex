import { act, cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { createFakeBackend } from '../../test/fakeBackend'
import { KnowledgePage } from './KnowledgePage'

afterEach(cleanup)

const date = '2026-09-26T10:00:00Z'
const document = { id: 'document-1', workspaceId: 'workspace-1', path: 'docs/guia.md', sourceSize: 128, sourceModifiedAt: date, indexedAt: date, chunkCount: 2 }

it('imports, searches with source citation, reindexes and removes local knowledge', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const pickKnowledgeFile = vi.fn(async () => '/workspace/docs/guia.md')
  const importKnowledge = vi.fn(async () => document)
  const searchKnowledgeDetailed = vi.fn(async () => ({ mode: 'textual', hits: [{ documentId: document.id, path: document.path, lineStart: 8, snippet: 'Contrato de integração local', sourceModifiedAt: date, indexedAt: date }] }))
  const reindexKnowledge = vi.fn(async () => document)
  const removeKnowledge = vi.fn(async () => undefined)
  Object.assign(backend, { listKnowledge: async () => [], pickKnowledgeFile, importKnowledge, searchKnowledgeDetailed, reindexKnowledge, removeKnowledge })
  render(<KnowledgePage backend={backend} workspaceId="workspace-1" onProjects={() => undefined} />)
  expect(await screen.findByRole('heading', { name: 'Conhecimento local' })).toBeInTheDocument()
  expect(screen.getByText('Busca textual')).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Escolher arquivo' }))
  expect(screen.getByLabelText('Arquivo do projeto')).toHaveValue('/workspace/docs/guia.md')
  await user.click(screen.getByRole('button', { name: 'Indexar arquivo' }))
  expect(importKnowledge).toHaveBeenCalledWith({ workspaceId: 'workspace-1', path: '/workspace/docs/guia.md' })
  expect(within(await screen.findByRole('listitem', { name: 'Documento docs/guia.md' })).getByText('docs/guia.md')).toBeInTheDocument()
  await user.type(screen.getByLabelText('Buscar no conhecimento'), 'integração')
  const documentPicker = screen.getByTestId('picker-knowledge-document')
  await user.type(within(documentPicker).getByRole('combobox'), 'guia')
  await user.click(screen.getByRole('option', { name: 'docs/guia.md' }))
  await user.click(screen.getByRole('button', { name: 'Buscar' }))
  expect(searchKnowledgeDetailed).toHaveBeenCalledWith({ workspaceId: 'workspace-1', documentId: 'document-1', query: 'integração', limit: 20 })
  const result = await screen.findByRole('listitem', { name: 'Resultado em docs/guia.md linha 8' })
  expect(within(result).getByText('Contrato de integração local')).toBeInTheDocument()
  expect(within(result).getByText(/docs\/guia.md · linha 8/)).toBeInTheDocument()
  expect(within(result).getByText(/Fonte modificada/)).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Reindexar docs/guia.md' }))
  expect(reindexKnowledge).toHaveBeenCalledWith({ workspaceId: 'workspace-1', documentId: 'document-1' })
  await user.click(screen.getByRole('button', { name: 'Remover docs/guia.md do índice' }))
  expect(removeKnowledge).toHaveBeenCalledWith({ workspaceId: 'workspace-1', documentId: 'document-1' })
  expect(await screen.findByText('Nenhum documento indexado')).toBeInTheDocument()
})

it('offers a project and recovers a failed library load without exposing details', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const onProjects = vi.fn()
  const listKnowledge = vi.fn().mockRejectedValueOnce(new Error('private sqlite path')).mockResolvedValue([])
  Object.assign(backend, { listKnowledge })
  const { rerender } = render(<KnowledgePage backend={backend} onProjects={onProjects} />)
  await user.click(screen.getByRole('button', { name: 'Abrir projetos' }))
  expect(onProjects).toHaveBeenCalledOnce()
  rerender(<KnowledgePage backend={backend} workspaceId="workspace-1" onProjects={onProjects} />)
  expect(await screen.findByRole('alert')).toHaveTextContent('Não foi possível carregar a biblioteca.')
  expect(screen.queryByText('private sqlite path')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Tentar novamente' }))
  expect(await screen.findByText('Nenhum documento indexado')).toBeInTheDocument()
})

it('saves an explicit LM Studio profile, indexes only on request and labels hybrid results', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const profile = { configured: true, kind: 'lm_studio', baseUrl: 'http://127.0.0.1:1234', model: 'local-embed', fingerprint: 'fingerprint', progress: { total: 2, indexed: 0, dimension: 0 } }
  const saveKnowledgeEmbeddingProfile = vi.fn(async () => profile)
  const indexKnowledgeVectors = vi.fn(async () => ({ ...profile, progress: { total: 2, indexed: 2, dimension: 2 } }))
  const searchKnowledgeDetailed = vi.fn(async () => ({ mode: 'hybrid', hits: [{ documentId: document.id, path: document.path, lineStart: 8, snippet: 'Contrato de integração local', sourceModifiedAt: date, indexedAt: date }] }))
  Object.assign(backend, {
    listKnowledge: async () => [document],
    getKnowledgeEmbeddingProfile: async () => ({ configured: false, kind: '', baseUrl: '', model: '', fingerprint: '', progress: { total: 0, indexed: 0, dimension: 0 } }),
    saveKnowledgeEmbeddingProfile, indexKnowledgeVectors, searchKnowledgeDetailed,
  })
  render(<KnowledgePage backend={backend} workspaceId="workspace-1" onProjects={() => undefined} />)
  await user.click(within(await screen.findByTestId('picker-knowledge-server')).getByRole('button'))
  await user.click(screen.getByRole('option', { name: 'LM Studio' }))
  await user.clear(screen.getByLabelText('URL local'))
  await user.type(screen.getByLabelText('URL local'), 'http://127.0.0.1:1234')
  await user.type(screen.getByLabelText('Modelo de embeddings'), 'local-embed')
  expect(screen.getByText('http://127.0.0.1:1234/v1/embeddings')).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Salvar perfil local' }))
  expect(saveKnowledgeEmbeddingProfile).toHaveBeenCalledWith({ workspaceId: 'workspace-1', kind: 'lm_studio', baseUrl: 'http://127.0.0.1:1234', model: 'local-embed' })
  expect(indexKnowledgeVectors).not.toHaveBeenCalled()
  expect(screen.getAllByText('http://127.0.0.1:1234/v1/embeddings', { selector: 'code' })).toHaveLength(2)
  await user.clear(screen.getByLabelText('URL local'))
  await user.type(screen.getByLabelText('URL local'), 'http://127.0.0.1:9999')
  expect(screen.getByRole('button', { name: 'Indexar próximo lote' })).toBeDisabled()
  expect(indexKnowledgeVectors).not.toHaveBeenCalled()
  await user.clear(screen.getByLabelText('URL local'))
  await user.type(screen.getByLabelText('URL local'), 'http://127.0.0.1:1234')
  await user.click(screen.getByRole('button', { name: 'Indexar próximo lote' }))
  expect(indexKnowledgeVectors).toHaveBeenCalledWith({ workspaceId: 'workspace-1', expectedFingerprint: 'fingerprint' })
  expect(await screen.findByText('2 de 2 trechos com vetores')).toBeInTheDocument()
  await user.type(screen.getByLabelText('Buscar no conhecimento'), 'integração')
  await user.click(screen.getByRole('button', { name: 'Buscar' }))
  expect(searchKnowledgeDetailed).toHaveBeenCalledWith({ workspaceId: 'workspace-1', documentId: '', query: 'integração', limit: 20 })
  expect(await screen.findByText('Busca híbrida')).toBeInTheDocument()
})

it('does not apply a late vector response from another project', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const profile = (workspaceId: string) => ({ configured: true, kind: 'ollama', baseUrl: workspaceId === 'A' ? 'http://127.0.0.1:11434' : 'http://127.0.0.1:11435', model: 'local-embed', fingerprint: workspaceId, progress: { total: 2, indexed: 0, dimension: 0 } })
  let finishA: (value: ReturnType<typeof profile>) => void = () => undefined
  backend.listKnowledge = async () => []
  backend.getKnowledgeEmbeddingProfile = async id => profile(id)
  backend.indexKnowledgeVectors = vi.fn(async input => input.workspaceId === 'A'
    ? new Promise<ReturnType<typeof profile>>(resolve => { finishA = resolve })
    : profile(input.workspaceId))
  const { rerender } = render(<KnowledgePage backend={backend} workspaceId="A" onProjects={() => undefined} />)
  await waitFor(() => expect(screen.getAllByText('http://127.0.0.1:11434/api/embed', { selector: 'code' })).toHaveLength(2))
  await user.click(screen.getByRole('button', { name: 'Indexar próximo lote' }))
  rerender(<KnowledgePage backend={backend} workspaceId="B" onProjects={() => undefined} />)
  await waitFor(() => expect(screen.getAllByText('http://127.0.0.1:11435/api/embed', { selector: 'code' })).toHaveLength(2))
  await act(async () => { finishA({ ...profile('A'), progress: { total: 2, indexed: 2, dimension: 2 } }) })
  await waitFor(() => expect(screen.getByText('0 de 2 trechos com vetores')).toBeInTheDocument())
  expect(screen.queryByText('Vetores locais indexados.')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Indexar próximo lote' })).toBeEnabled()
})

it('indexes every bounded batch after one explicit action', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const profile = { configured: true, kind: 'ollama', baseUrl: 'http://127.0.0.1:11434', model: 'local-embed', fingerprint: 'profile-1', progress: { total: 17, indexed: 0, dimension: 0 } }
  let indexed = 0
  backend.listKnowledge = async () => []
  backend.getKnowledgeEmbeddingProfile = async () => profile
  backend.indexKnowledgeVectors = vi.fn(async () => {
    indexed = Math.min(17, indexed + 8)
    return { ...profile, progress: { total: 17, indexed, dimension: 2 } }
  })
  render(<KnowledgePage backend={backend} workspaceId="workspace-1" onProjects={() => undefined} />)
  await user.click(await screen.findByRole('button', { name: 'Indexar todos os trechos' }))
  await screen.findByText('17 de 17 trechos com vetores')
  expect(backend.indexKnowledgeVectors).toHaveBeenCalledTimes(3)
  expect(backend.indexKnowledgeVectors).toHaveBeenNthCalledWith(1, { workspaceId: 'workspace-1', expectedFingerprint: 'profile-1' })
})

it('stops a sequence after the active batch and keeps its checkpoint', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const profile = { configured: true, kind: 'ollama', baseUrl: 'http://127.0.0.1:11434', model: 'local-embed', fingerprint: 'profile-1', progress: { total: 17, indexed: 0, dimension: 0 } }
  let finish: (value: typeof profile) => void = () => undefined
  backend.listKnowledge = async () => []
  backend.getKnowledgeEmbeddingProfile = async () => profile
  backend.indexKnowledgeVectors = vi.fn(async () => new Promise<typeof profile>(resolve => { finish = resolve }))
  render(<KnowledgePage backend={backend} workspaceId="workspace-1" onProjects={() => undefined} />)
  await user.click(await screen.findByRole('button', { name: 'Indexar todos os trechos' }))
  await user.click(screen.getByRole('button', { name: 'Parar após este lote' }))
  expect(screen.getByRole('button', { name: 'Parando após este lote…' })).toBeDisabled()
  await act(async () => { finish({ ...profile, progress: { total: 17, indexed: 8, dimension: 2 } }) })
  await screen.findByText('8 de 17 trechos com vetores')
  expect(backend.indexKnowledgeVectors).toHaveBeenCalledTimes(1)
  expect(screen.getByRole('button', { name: 'Indexar todos os trechos' })).toBeEnabled()
})

it('describes vector fallback without guessing its cause', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  Object.assign(backend, {
    listKnowledge: async () => [document],
    searchKnowledgeDetailed: async () => ({ mode: 'textual_fallback', hits: [] }),
  })
  render(<KnowledgePage backend={backend} workspaceId="workspace-1" onProjects={() => undefined} />)
  await user.type(await screen.findByLabelText('Buscar no conhecimento'), 'integração')
  await user.click(screen.getByRole('button', { name: 'Buscar' }))
  expect(await screen.findByText('Busca textual · vetores indisponíveis')).toBeInTheDocument()
})
