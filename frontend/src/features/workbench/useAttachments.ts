import { useCallback, useState } from 'react'
import type { ComposerAttachment } from '../../components/ChatComposer'
import { t } from '../../i18n'
import { errorMessage, type Backend } from '../../lib/backend'

type Attached = ComposerAttachment & { text?: string; path?: string }

// A text file goes in the message itself; past this size it is too long to read as part of a request.
const maxTextBytes = 256 * 1024

function looksBinary(text: string) {
  return text.includes('\u0000')
}

async function base64(file: File) {
  const bytes = new Uint8Array(await file.arrayBuffer())
  let binary = ''
  for (let index = 0; index < bytes.length; index += 0x8000) binary += String.fromCharCode(...bytes.subarray(index, index + 0x8000))
  return btoa(binary)
}

/**
 * Files the person attaches to a chat message. Text and code go in the message; an image is saved where the
 * conversation's tools read (the project or its private copy, never committed) and the message says where it is.
 */
export function useAttachments(backend: Backend, workspaceId: string | undefined, sessionId: string | undefined) {
  const [items, setItems] = useState<Attached[]>([])
  const [error, setError] = useState<string>()
  const attach = useCallback(async (files: File[]) => {
    setError(undefined)
    for (const file of files) {
      const id = crypto.randomUUID()
      try {
        if (file.type.startsWith('image/')) {
          if (!workspaceId) throw new Error(t('Abra um projeto para anexar imagens.'))
          const saved = await backend.saveChatAttachment({ sessionId: sessionId ?? '', workspaceId, name: file.name, data: await base64(file) })
          setItems(current => [...current, { id, name: file.name, kind: 'image', size: file.size, path: saved.path }])
          continue
        }
        if (file.size > maxTextBytes) { setError(t('{name} passa de 256 KB: anexe um trecho ou peça para a IA ler o arquivo no projeto.', { name: file.name })); continue }
        const text = await file.text()
        if (looksBinary(text)) { setError(t('{name} não é texto nem imagem: anexe arquivos de texto, código ou imagens.', { name: file.name })); continue }
        setItems(current => [...current, { id, name: file.name, kind: 'text', size: file.size, text }])
      } catch (failure) { setError(errorMessage(failure)) }
    }
  }, [backend, workspaceId, sessionId])
  const remove = useCallback((id: string) => setItems(current => current.filter(item => item.id !== id)), [])
  const clear = useCallback(() => { setItems([]); setError(undefined) }, [])
  /** What goes after the person's words: each text file in a fenced block, each image as the path to read. */
  const block = useCallback(() => {
    if (!items.length) return ''
    const parts = items.map(item => item.kind === 'image'
      ? t('Imagem anexada {name}: abra com a ferramenta de leitura em `{path}`.', { name: item.name, path: item.path ?? '' })
      : `${t('Arquivo anexado {name}:', { name: item.name })}\n\`\`\`\`\n${item.text ?? ''}\n\`\`\`\``)
    return `\n\n---\n${t('Anexos')}\n\n${parts.join('\n\n')}`
  }, [items])
  return { items: items as ComposerAttachment[], error, attach, remove, clear, block }
}
