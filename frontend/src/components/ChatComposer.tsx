import { useEffect, useRef, type ClipboardEvent, type KeyboardEvent, type ReactNode, type Ref } from 'react'
import { ArrowUp, FileText, ImageIcon, ListPlus, Plus, X } from 'lucide-react'
import { useT } from '../i18n'
import './chatComposer.css'

/** A file attached to the message; an image carries a small picture of itself (an object URL) for its chip. */
export type ComposerAttachment = { id: string; name: string; kind: 'text' | 'image'; size: number; preview?: string }

export type ComposerProps = {
  id: string
  label: string
  value: string
  onChange: (value: string) => void
  onSend: () => void
  canSend: boolean
  placeholder?: string
  disabled?: boolean
  maxLength?: number
  /** The AI is answering: the button stops it, and Cmd+Enter queues the message. */
  running?: boolean
  stopping?: boolean
  onStop?: () => void
  onQueue?: () => void
  queued?: string[]
  onUnqueue?: (index: number) => void
  attachments?: ComposerAttachment[]
  onAttach?: (files: File[]) => void
  onRemoveAttachment?: (id: string) => void
  /** Short lines about what the next message will do, inside the box above the text. */
  notices?: ReactNode
  footerStart?: ReactNode
  footerEnd?: ReactNode
  inputRef?: Ref<HTMLTextAreaElement>
  sendLabel?: string
}

const maxHeight = 240

/** The chat box, as in Claude Code: attach on the left, the text, stop or send on the right; settings quietly underneath. */
export function ChatComposer({ id, label, value, onChange, onSend, canSend, placeholder, disabled = false, maxLength, running = false, stopping = false, onStop, onQueue, queued = [], onUnqueue, attachments = [], onAttach, onRemoveAttachment, notices, footerStart, footerEnd, inputRef, sendLabel }: ComposerProps) {
  const t = useT()
  const box = useRef<HTMLTextAreaElement | null>(null)
  const picker = useRef<HTMLInputElement>(null)
  // The box grows with what is typed, up to a few lines, then scrolls.
  useEffect(() => {
    const element = box.current
    if (!element) return
    element.style.height = 'auto'
    element.style.height = `${Math.min(element.scrollHeight, maxHeight)}px`
  }, [value])
  function setRefs(element: HTMLTextAreaElement | null) {
    box.current = element
    if (typeof inputRef === 'function') inputRef(element)
    else if (inputRef) (inputRef as { current: HTMLTextAreaElement | null }).current = element
  }
  function keyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key !== 'Enter' || event.shiftKey || event.nativeEvent.isComposing) return
    event.preventDefault()
    if ((event.metaKey || event.ctrlKey) && running && onQueue && value.trim()) { onQueue(); return }
    if (canSend) { onSend(); return }
    if (running && onQueue && value.trim()) onQueue()
  }
  // An image pasted from the clipboard (a screenshot) is attached like a picked file; text pastes into the box as usual.
  function pasted(event: ClipboardEvent<HTMLTextAreaElement>) {
    if (!onAttach || disabled) return
    const data = event.clipboardData
    let files = [...(data.files ?? [])]
    if (!files.length) files = [...(data.items ?? [])].flatMap(item => item.kind === 'file' ? [item.getAsFile()].filter((file): file is File => !!file) : [])
    if (!files.length) return
    if (!data.getData('text/plain')) event.preventDefault()
    onAttach(files.map((file, index) => file.type.startsWith('image/') ? new File([file], pastedImageName(file, index, files.length), { type: file.type }) : file))
  }
  const stoppable = (running || stopping) && !!onStop
  const hint = placeholder ?? (onQueue ? t('↵ para enviar, ⌘↵ para adicionar à fila') : t('↵ para enviar, ⇧↵ quebra a linha'))
  return <div className="chat-composer">
    {queued.length > 0 && <ol className="chat-composer-queue" aria-label={t('Na fila · {count}', { count: queued.length })}>
      {queued.map((text, index) => <li key={`${index}:${text}`}><ListPlus aria-hidden="true" /><span>{text}</span>
        {onUnqueue && <button type="button" className="chat-composer-chip-remove" aria-label={t('Tirar da fila: {text}', { text })} onClick={() => onUnqueue(index)}><X aria-hidden="true" /></button>}
      </li>)}
    </ol>}
    <form className="chat-composer-box" onSubmit={event => { event.preventDefault(); if (canSend) onSend() }}>
      {notices && <div className="chat-composer-notices">{notices}</div>}
      {attachments.length > 0 && <ul className="chat-composer-attachments" aria-label={t('Anexos')}>
        {attachments.map(item => <li key={item.id} className="chat-composer-chip">
          {item.kind === 'image' ? item.preview ? <img className="chat-composer-thumb" src={item.preview} alt="" /> : <ImageIcon aria-hidden="true" /> : <FileText aria-hidden="true" />}<span>{item.name}</span>
          {onRemoveAttachment && <button type="button" className="chat-composer-chip-remove" aria-label={t('Remover {name}', { name: item.name })} onClick={() => onRemoveAttachment(item.id)}><X aria-hidden="true" /></button>}
        </li>)}
      </ul>}
      <div className="chat-composer-row">
        {onAttach && <>
          <button type="button" className="chat-composer-icon" aria-label={t('Anexar arquivos')} title={t('Anexar arquivos')} disabled={disabled} onClick={() => picker.current?.click()}><Plus aria-hidden="true" /></button>
          <input ref={picker} type="file" multiple hidden aria-label={t('Arquivos para anexar')} onChange={event => { const files = [...event.target.files ?? []]; event.target.value = ''; if (files.length) onAttach(files) }} />
        </>}
        <label className="visually-hidden" htmlFor={id}>{label}</label>
        <textarea ref={setRefs} id={id} className="chat-composer-input" rows={1} value={value} maxLength={maxLength} disabled={disabled} placeholder={hint}
          onChange={event => onChange(event.target.value)} onKeyDown={keyDown} onPaste={pasted} />
        {stoppable
          ? <button type="button" className="chat-composer-icon chat-composer-stop" aria-label={stopping ? t('Cancelando…') : t('Cancelar execução')} title={t('Cancelar execução')} disabled={stopping} onClick={event => { event.preventDefault(); event.stopPropagation(); onStop?.() }}><span aria-hidden="true" /></button>
          : <button type="submit" className="chat-composer-icon chat-composer-send" aria-label={sendLabel ?? t('Enviar')} title={sendLabel ?? t('Enviar')} disabled={!canSend}><ArrowUp aria-hidden="true" /></button>}
      </div>
    </form>
    {(footerStart || footerEnd) && <div className="chat-composer-footer">
      <div className="chat-composer-footer-start">{footerStart}</div>
      <div className="chat-composer-footer-end">{footerEnd}</div>
    </div>}
  </div>
}

/** A pasted screenshot arrives as "image.png": it gets a name of its own, by when it was pasted. */
function pastedImageName(file: File, index: number, count: number) {
  const now = new Date()
  const two = (value: number) => String(value).padStart(2, '0')
  const stamp = `${now.getFullYear()}${two(now.getMonth() + 1)}${two(now.getDate())}-${two(now.getHours())}${two(now.getMinutes())}${two(now.getSeconds())}`
  const extension = file.type.split('/')[1]?.replace('jpeg', 'jpg').replace(/[^a-z0-9]/g, '') || 'png'
  return `imagem-colada-${stamp}${count > 1 ? `-${index + 1}` : ''}.${extension}`
}
