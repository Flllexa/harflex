import { useEffect, useRef, useState, type FormEvent } from 'react'
import { ArrowDownToLine, ArrowUpFromLine, FolderOpen, RefreshCw, Send } from 'lucide-react'
import { errorMessage, type Backend, type ChannelMessage, type LocalChannel } from '../../lib/backend'
import { localeTag, t, useT } from '../../i18n'

type Props = { backend: Backend; workspaceId?: string; onProjects: () => void }

function fileError(): Record<string, string> {
  return {
    folder_unavailable: t('A pasta do canal não está disponível.'),
    inbox_limit: t('A inbox tem mais de 500 entradas. Organize, mova ou remova arquivos manualmente para liberar novas importações; o Harflex não remove originais.'),
    source_unavailable: t('Um arquivo da inbox não está disponível ou saiu do projeto.'),
    source_changed: t('Um arquivo mudou durante a leitura.'),
    message_invalid: t('Um arquivo não é texto UTF-8 válido de até 64 KB.'),
    outbox_conflict: t('Há outro conteúdo nesse nome de arquivo na outbox.'),
    write_failed: t('Não foi possível escrever na outbox.'),
  }
}

function statusLabel(message: ChannelMessage) {
  if (message.status === 'received') return t('Recebido')
  if (message.status === 'sent') return t('Enviado')
  if (message.status === 'pending') return t('Pendente')
  return message.direction === 'incoming' ? t('Falha na importação') : t('Falha no envio')
}

function requestFromFile(name: string) {
  return /^harflex-([a-f0-9]{32})\.md$/.exec(name)?.[1] ?? ''
}

function newRequestID() {
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  return Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('')
}

function delivered(messages: ChannelMessage[] | undefined, requestId: string, content: string) {
  return messages?.some(item => item.direction === 'outgoing' && item.fileName === `harflex-${requestId}.md` && item.content === content && item.status === 'sent') ?? false
}

type ChannelDraft = { text: string; requestId?: string }

function formatDate(value: string) {
  return new Date(value).toLocaleString(localeTag(), { dateStyle: 'short', timeStyle: 'short' })
}

// THESIS: One local folder is a visible handoff point, never an invisible transport.
// OWN-WORLD: Ion Void, Forge panels, mint commands, cyan file provenance.
// STORY: Choose a folder, inspect imported files, then deliberately write a reply.
// FIRST VIEWPORT: Folder configuration and channel rail beside the message ledger.
// FORM: An extension of the approved operational workbench, with no new visual world.
export function ChannelsPage({ backend, workspaceId, onProjects }: Props) {
  const t = useT()
  const [channels, setChannels] = useState<LocalChannel[]>([])
  const [selectedId, setSelectedId] = useState('')
  const [messages, setMessages] = useState<ChannelMessage[]>([])
  const [listState, setListState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [messageState, setMessageState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [name, setName] = useState('')
  const [folder, setFolder] = useState('')
  const [drafts, setDrafts] = useState<Record<string, ChannelDraft>>({})
  const [pending, setPending] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const listGeneration = useRef(0)
  const messageGeneration = useRef(0)
  const replyRef = useRef<HTMLTextAreaElement>(null)
  const selected = channels.find(item => item.id === selectedId)
  const reply = drafts[selectedId]?.text ?? ''

  function updateReply(text: string) {
    setDrafts(current => {
      const previous = current[selectedId]
      return { ...current, [selectedId]: { text, requestId: previous?.text === text ? previous.requestId : undefined } }
    })
  }

  function clearDeliveredDraft(channelId: string, requestId: string, content: string) {
    setDrafts(current => {
      const draft = current[channelId]
      if (draft?.text !== content || draft.requestId !== requestId) return current
      return { ...current, [channelId]: { text: '' } }
    })
  }

  async function refreshChannels() {
    if (!workspaceId) return
    const generation = ++listGeneration.current
    setListState(current => current === 'ready' ? 'ready' : 'loading')
    try {
      const found = await backend.listLocalChannels(workspaceId)
      if (generation !== listGeneration.current) return
      setChannels(found)
      setSelectedId(current => found.some(item => item.id === current) ? current : found[0]?.id ?? '')
      setListState('ready')
    } catch {
      if (generation === listGeneration.current) setListState('error')
    }
  }

  async function refreshMessages(channelId: string) {
    const generation = ++messageGeneration.current
    setMessageState('loading')
    try {
      const found = await backend.listChannelMessages(channelId)
      if (generation !== messageGeneration.current) return undefined
      setMessages(found)
      setMessageState('ready')
      return found
    } catch {
      if (generation === messageGeneration.current) setMessageState('error')
      return undefined
    }
  }

  useEffect(() => {
    setChannels([]); setSelectedId(''); setMessages([])
    if (workspaceId) void refreshChannels()
    return () => { listGeneration.current++; messageGeneration.current++ }
  }, [backend, workspaceId])

  useEffect(() => {
    setMessages([])
    if (selectedId) void refreshMessages(selectedId)
    return () => { messageGeneration.current++ }
  }, [backend, selectedId])

  async function configure(event: FormEvent) {
    event.preventDefault()
    if (!workspaceId || !name.trim() || !folder.trim() || pending) return
    setPending('configure'); setError(''); setNotice('')
    try {
      const saved = await backend.saveLocalChannel({ workspaceId, name: name.trim(), folder: folder.trim() })
      setChannels(current => [saved, ...current.filter(item => item.id !== saved.id)])
      setSelectedId(saved.id)
      setName(''); setFolder('')
      setListState('ready')
      setNotice(t('Canal configurado. A inbox será lida apenas quando você importar.'))
    } catch (failure) { setError(errorMessage(failure)) }
    finally { setPending('') }
  }

  async function importInbox() {
    if (!selected || pending) return
    setPending('import'); setError(''); setNotice('')
    try {
      const result = await backend.importChannelInbox(selected.id)
      setChannels(current => current.map(item => item.id === selected.id ? result.channel : item))
      await refreshMessages(selected.id)
      setNotice(`${result.imported} ${result.imported === 1 ? t('mensagem nova importada') : t('mensagens novas importadas')}.${result.failed ? ` ${result.failed} ${result.failed === 1 ? t('arquivo com erro') : t('arquivos com erro')}.` : ''}`)
    } catch (failure) { setError(errorMessage(failure)); await refreshChannels() }
    finally { setPending('') }
  }

  async function send(event: FormEvent) {
    event.preventDefault()
    if (!selected || !reply.trim() || pending) return
    const channelId = selected.id
    const content = reply
    setPending('send'); setError(''); setNotice('')
    let requestId = drafts[channelId]?.requestId ?? ''
    let failure: unknown
    try {
      if (!requestId) requestId = newRequestID()
      setDrafts(current => ({ ...current, [channelId]: { text: content, requestId } }))
      await backend.sendChannelMessage({ channelId, requestId, content })
    } catch (error) { failure = error }
    const readback = await refreshMessages(channelId)
    await refreshChannels()
    if (requestId && delivered(readback, requestId, content)) {
      clearDeliveredDraft(channelId, requestId, content)
      setNotice(t('Resposta confirmada na outbox.'))
      replyRef.current?.focus()
    } else if (failure) setError(errorMessage(failure))
    else setError(t('Não foi possível confirmar a entrega. Atualize o histórico e repita com o mesmo envio.'))
    setPending('')
  }

  async function retry(message: ChannelMessage) {
    if (!selected || pending) return
    const requestId = requestFromFile(message.fileName)
    if (!requestId) return
    setPending(message.id); setError(''); setNotice('')
    let failure: unknown
    try { await backend.retryChannelMessage({ channelId: selected.id, requestId }) }
    catch (error) { failure = error }
    const readback = await refreshMessages(selected.id)
    await refreshChannels()
    if (delivered(readback, requestId, message.content)) {
      clearDeliveredDraft(selected.id, requestId, message.content)
      setNotice(t('Entrega confirmada na outbox.'))
    } else if (failure) setError(errorMessage(failure))
    else setError(t('Não foi possível confirmar a entrega. Atualize o histórico e repita o envio.'))
    setPending('')
  }

  return <div className="channels-page">
    <div className="destination-heading"><div><h2>{t('Canais locais')}</h2><p className="muted">{t('Troca de arquivos dentro do projeto, sempre por comando seu.')}</p></div><span className="status-chip status-ready">{t('Somente arquivos locais')}</span></div>
    {!workspaceId ? <div className="catalog-empty"><FolderOpen aria-hidden="true" /><strong>{t('Abra um projeto para configurar canais')}</strong><span className="muted">{t('As pastas ficam dentro do diretório autorizado.')}</span><button type="button" className="touch-target primary-button" onClick={onProjects}>{t('Abrir projetos')}</button></div> : <>
      <form className="channels-config" onSubmit={configure}>
        <div className="destination-heading"><div><h3>{t('Configurar pasta')}</h3><p className="muted">{t('O Harflex cria inbox e outbox na pasta escolhida. Arquivos originais permanecem lá.')}</p></div><FolderOpen aria-hidden="true" /></div>
        <div className="channels-config-fields"><label className="field">{t('Nome do canal')}<input value={name} onChange={event => setName(event.target.value)} maxLength={80} placeholder={t('Equipe')} required /></label><label className="field">{t('Pasta no projeto')}<input value={folder} onChange={event => setFolder(event.target.value)} maxLength={240} placeholder="channels/equipe" required /></label></div>
        <button className="touch-target primary-button" type="submit" disabled={!!pending || !name.trim() || !folder.trim()}>{t('Configurar canal')}</button>
      </form>
      {listState === 'loading' && <p role="status" className="muted">{t('Carregando canais…')}</p>}
      {listState === 'error' && <div className="inline-error" role="alert"><p>{t('Não foi possível carregar os canais.')}</p><button className="touch-target secondary-button" type="button" onClick={() => void refreshChannels()}>{t('Tentar novamente')}</button></div>}
      {listState === 'ready' && channels.length === 0 && <div className="catalog-empty"><FolderOpen aria-hidden="true" /><strong>{t('Nenhum canal configurado')}</strong><span className="muted">{t('Escolha uma pasta do projeto para começar.')}</span></div>}
      {listState === 'ready' && channels.length > 0 && <div className="channels-layout">
        <section className="channels-list" aria-label={t('Canais configurados')}><div className="destination-heading"><h3>{t('Pastas')}</h3><button type="button" className="touch-target secondary-button" onClick={() => void refreshChannels()} disabled={!!pending}><RefreshCw aria-hidden="true" />{t('Atualizar')}</button></div><ul>{channels.map(item => <li key={item.id}><button type="button" className={`touch-target channels-list-item${item.id === selectedId ? ' is-selected' : ''}`} aria-current={item.id === selectedId ? 'true' : undefined} onClick={() => setSelectedId(item.id)} disabled={!!pending}><strong>{item.name}</strong><span className="muted mono-path">{item.folder}</span><span className={item.status === 'error' ? 'status-chip status-unavailable' : 'status-chip status-ready'}>{item.status === 'error' ? t('Atenção') : t('Pronto')}</span></button></li>)}</ul></section>
        {selected && <section className="channels-thread" aria-label={t('Canal {name}', { name: selected.name })}>
          <div className="channels-thread-head"><div><h3>{selected.name}</h3><p className="muted mono-path">{selected.folder}/inbox → {selected.folder}/outbox</p></div><button className="touch-target secondary-button" type="button" onClick={() => void importInbox()} disabled={!!pending}><ArrowDownToLine aria-hidden="true" />{pending === 'import' ? t('Importando…') : t('Importar inbox')}</button></div>
          {selected.status === 'error' && <p className="channels-warning" role="alert">{fileError()[selected.lastError] ?? t('O canal precisa de atenção.')} {t('Corrija a pasta e importe ou repita o envio.')}</p>}
          {messageState === 'loading' && <p role="status" className="muted">{t('Carregando histórico…')}</p>}
          {messageState === 'error' && <div className="inline-error" role="alert"><p>{t('Não foi possível carregar o histórico.')}</p><button className="touch-target secondary-button" type="button" onClick={() => void refreshMessages(selected.id)}>{t('Tentar novamente')}</button></div>}
          {messageState === 'ready' && (messages.length === 0 ? <div className="catalog-empty"><ArrowDownToLine aria-hidden="true" /><strong>{t('Nenhuma mensagem no histórico')}</strong><span className="muted">{t('Coloque um .txt ou .md na inbox e escolha “Importar inbox”.')}</span></div> : <ol className="channels-messages">{messages.map(message => <li key={message.id} aria-label={`${message.direction === 'incoming' ? t('Entrada') : t('Saída')} ${message.fileName}`} className={`channels-message ${message.direction}`}><div className="channels-message-meta"><span>{message.direction === 'incoming' ? <ArrowDownToLine aria-hidden="true" /> : <ArrowUpFromLine aria-hidden="true" />}{message.fileName}</span><span className={message.status === 'failed' ? 'status-chip status-unavailable' : 'status-chip status-ready'}>{statusLabel(message)}</span></div>{message.content && <p>{message.content}</p>}<div className="channels-message-foot"><time dateTime={message.updatedAt}>{formatDate(message.updatedAt)}</time>{message.status === 'failed' && <span>{fileError()[message.errorCode] ?? t('Entrega pendente de verificação.')}</span>}{message.direction === 'outgoing' && message.status !== 'sent' && <button type="button" className="touch-target secondary-button" onClick={() => void retry(message)} disabled={!!pending || !requestFromFile(message.fileName)}>{t('Repetir envio')}</button>}</div></li>)}</ol>)}
          <form className="channels-compose" onSubmit={send}><label className="field">{t('Resposta')}<textarea ref={replyRef} value={reply} onChange={event => updateReply(event.target.value)} rows={4} maxLength={65536} placeholder={t('Escreva uma resposta para gerar um arquivo na outbox')} required /></label><div className="channels-compose-actions"><span className="muted">{t('Até 64 KB · .md · envio manual')}</span><button type="submit" className="touch-target primary-button" disabled={!!pending || !reply.trim()}><Send aria-hidden="true" />{pending === 'send' ? t('Enviando…') : t('Enviar resposta')}</button></div></form>
        </section>}
      </div>}
    </>}
    {error && <p role="alert" className="form-error">{error}</p>}
    {notice && <p role="status" className="form-success">{notice}</p>}
  </div>
}
