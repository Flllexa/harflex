import { act, renderHook } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { createFakeBackend } from '../../test/fakeBackend'
import { useAttachments } from './useAttachments'

it('inlines text files and saves images where the AI reads them', async () => {
  const { backend } = createFakeBackend()
  const save = vi.spyOn(backend, 'saveChatAttachment')
  const { result } = renderHook(() => useAttachments(backend, 'workspace-1', 'session-1'))
  await act(async () => { await result.current.attach([new File(['linha 1'], 'notas.md', { type: 'text/markdown' }), new File([new Uint8Array([137, 80, 78, 71])], 'tela.png', { type: 'image/png' })]) })
  expect(result.current.items.map(item => [item.name, item.kind])).toEqual([['notas.md', 'text'], ['tela.png', 'image']])
  expect(save).toHaveBeenCalledWith(expect.objectContaining({ sessionId: 'session-1', workspaceId: 'workspace-1', name: 'tela.png' }))
  const block = result.current.block()
  expect(block).toContain('notas.md')
  expect(block).toContain('linha 1')
  expect(block).toContain('.harflex/attachments/test-tela.png')
  act(() => result.current.remove(result.current.items[0].id))
  expect(result.current.block()).not.toContain('linha 1')
})

it('refuses a binary file that is not an image, saying why', async () => {
  const { backend } = createFakeBackend()
  const { result } = renderHook(() => useAttachments(backend, 'workspace-1', ''))
  await act(async () => { await result.current.attach([new File([new Uint8Array([0, 1, 2, 0])], 'app.bin', { type: 'application/octet-stream' })]) })
  expect(result.current.items).toEqual([])
  expect(result.current.error).toBe('app.bin não é texto nem imagem: anexe arquivos de texto, código ou imagens.')
})
