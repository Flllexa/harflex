import { useEffect, useRef, useState, type FormEvent } from 'react'
import { FileSearch, FileText, FolderOpen, RefreshCw, Search, Trash2, Upload } from 'lucide-react'
import { errorCode, errorMessage, type Backend, type KnowledgeDocument, type KnowledgeEmbeddingProfile, type KnowledgeHit, type KnowledgeSearch } from '../../lib/backend'
import { IonPicker } from '../../components/IonPicker'
import { localeTag, t, useT } from '../../i18n'

type Props = { backend: Backend; workspaceId?: string; onProjects: () => void }

function formatDate(value: string) {
  return new Date(value).toLocaleString(localeTag(), { dateStyle: 'short', timeStyle: 'short' })
}

const localDefaults = { ollama: 'http://127.0.0.1:11434', lm_studio: 'http://127.0.0.1:1234' }
function searchModeNames(): Record<KnowledgeSearch['mode'], string> {
  return {
    textual: t('Busca textual'), textual_fallback: t('Busca textual · vetores indisponíveis'),
    semantic: t('Busca semântica local'), hybrid: t('Busca híbrida'),
  }
}

export function KnowledgePage({ backend, workspaceId, onProjects }: Props) {
  const t = useT()
  const [items, setItems] = useState<KnowledgeDocument[]>([])
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [path, setPath] = useState('')
  const [query, setQuery] = useState('')
  const [documentId, setDocumentId] = useState('')
  const [hits, setHits] = useState<KnowledgeHit[]>([])
  const [searchMode, setSearchMode] = useState<KnowledgeSearch['mode']>('textual')
  const [embeddingProfile, setEmbeddingProfile] = useState<KnowledgeEmbeddingProfile>()
  const [profileLoading, setProfileLoading] = useState(false)
  const [profileError, setProfileError] = useState(false)
  const [profileConflict, setProfileConflict] = useState(false)
  const [embeddingKind, setEmbeddingKind] = useState<'ollama' | 'lm_studio'>('ollama')
  const [embeddingURL, setEmbeddingURL] = useState(localDefaults.ollama)
  const [embeddingModel, setEmbeddingModel] = useState('')
  const [searchState, setSearchState] = useState<'idle' | 'loading' | 'ready'>('idle')
  const [pending, setPending] = useState('')
  const [stopRequested, setStopRequested] = useState(false)
  const [error, setError] = useState<string>()
  const [notice, setNotice] = useState<string>()
  const generation = useRef(0)
  const stopAfterBatch = useRef(false)
  const activeRequest = (current: number) => current === generation.current

  async function refresh() {
    if (!workspaceId) return
    const current = ++generation.current
    setState('loading')
    try {
      const documents = await backend.listKnowledge(workspaceId)
      if (current === generation.current) { setItems(documents); setState('ready') }
    } catch {
      if (current === generation.current) setState('error')
    }
    await refreshProfile(current)
  }

  async function refreshProfile(current = generation.current) {
    if (!workspaceId) return
    setProfileLoading(true); setProfileError(false)
    try {
      const profile = await backend.getKnowledgeEmbeddingProfile(workspaceId)
      if (current !== generation.current) return
      setEmbeddingProfile(profile)
      setProfileConflict(false)
      if (profile.configured) {
        setEmbeddingKind(profile.kind as 'ollama' | 'lm_studio')
        setEmbeddingURL(profile.baseUrl)
        setEmbeddingModel(profile.model)
      }
    } catch { if (current === generation.current) setProfileError(true) }
    finally { if (current === generation.current) setProfileLoading(false) }
  }

  useEffect(() => {
    setItems([])
    setHits([])
    setPath('')
    setQuery('')
    setDocumentId('')
    setSearchState('idle')
    setSearchMode('textual')
    setState('loading')
    setPending('')
    setStopRequested(false)
    setError(undefined)
    setNotice(undefined)
    setEmbeddingProfile(undefined)
    setProfileLoading(false)
    setProfileError(false)
    setProfileConflict(false)
    stopAfterBatch.current = true
    setEmbeddingKind('ollama'); setEmbeddingURL(localDefaults.ollama); setEmbeddingModel('')
    if (workspaceId) void refresh()
    return () => { stopAfterBatch.current = true; generation.current++ }
  }, [backend, workspaceId])

  async function chooseFile() {
    if (pending) return
    const current = generation.current
    setPending('pick'); setError(undefined)
    try {
      const selected = await backend.pickKnowledgeFile()
      if (selected && activeRequest(current)) setPath(selected)
    } catch (failure) { if (activeRequest(current)) setError(errorMessage(failure)) }
    finally { if (activeRequest(current)) setPending('') }
  }

  async function importFile(event: FormEvent) {
    event.preventDefault()
    if (!workspaceId || !path.trim() || pending) return
    const current = generation.current
    setPending('import'); setError(undefined); setNotice(undefined)
    try {
      const item = await backend.importKnowledge({ workspaceId, path: path.trim() })
      if (!activeRequest(current)) return
      setItems(current => [item, ...current.filter(existing => existing.id !== item.id)])
      setState('ready')
      setPath('')
      setNotice(`${t('{path} indexado.', { path: item.path })} ${item.chunkCount} ${item.chunkCount === 1 ? t('trecho disponível') : t('trechos disponíveis')} ${t('para busca textual.')}`)
      void refreshProfile()
    } catch (failure) { if (activeRequest(current)) setError(errorMessage(failure)) }
    finally { if (activeRequest(current)) setPending('') }
  }

  async function search(event: FormEvent) {
    event.preventDefault()
    if (!workspaceId || !query.trim() || pending) return
    const current = generation.current
    setPending('search'); setError(undefined); setSearchState('loading')
    try {
      const result = await backend.searchKnowledgeDetailed({ workspaceId, documentId, query: query.trim(), limit: 20 })
      if (!activeRequest(current)) return
      setHits(result.hits); setSearchMode(result.mode)
      setSearchState('ready')
    } catch (failure) { if (activeRequest(current)) { setHits([]); setSearchState('idle'); setError(errorMessage(failure)) } }
    finally { if (activeRequest(current)) setPending('') }
  }

  async function reindex(item: KnowledgeDocument) {
    if (!workspaceId || pending) return
    const current = generation.current
    setPending(item.id); setError(undefined); setNotice(undefined)
    try {
      const updated = await backend.reindexKnowledge({ workspaceId, documentId: item.id })
      if (!activeRequest(current)) return
      setItems(current => current.map(existing => existing.id === updated.id ? updated : existing))
      setHits([]); setSearchState('idle')
      setNotice(t('{path} relido da origem.', { path: updated.path }))
      void refreshProfile()
    } catch (failure) { if (activeRequest(current)) setError(errorMessage(failure)) }
    finally { if (activeRequest(current)) setPending('') }
  }

  async function remove(item: KnowledgeDocument) {
    if (!workspaceId || pending) return
    const current = generation.current
    setPending(item.id); setError(undefined); setNotice(undefined)
    try {
      await backend.removeKnowledge({ workspaceId, documentId: item.id })
      if (!activeRequest(current)) return
      setItems(current => current.filter(existing => existing.id !== item.id))
      if (documentId === item.id) setDocumentId('')
      setHits([]); setSearchState('idle')
      setNotice(t('{path} removido do índice. O arquivo original permanece no projeto.', { path: item.path }))
      void refreshProfile()
    } catch (failure) { if (activeRequest(current)) setError(errorMessage(failure)) }
    finally { if (activeRequest(current)) setPending('') }
  }

  async function saveEmbeddingProfile(event: FormEvent) {
    event.preventDefault()
    if (!workspaceId || !embeddingModel.trim() || pending) return
    const current = generation.current
    setPending('embedding-profile'); setError(undefined); setNotice(undefined)
    try {
      const profile = await backend.saveKnowledgeEmbeddingProfile({ workspaceId, kind: embeddingKind, baseUrl: embeddingURL.trim(), model: embeddingModel.trim() })
      if (!activeRequest(current)) return
      setEmbeddingProfile(profile); setProfileError(false)
      setProfileConflict(false)
      setEmbeddingURL(profile.baseUrl); setEmbeddingModel(profile.model)
      setHits([]); setSearchState('idle')
      setNotice(t('Perfil local salvo. Indexe os trechos quando o modelo estiver disponível.'))
    } catch (failure) { if (activeRequest(current)) setError(errorMessage(failure)) }
    finally { if (activeRequest(current)) setPending('') }
  }

  async function indexVectors(all: boolean) {
    if (!workspaceId || pending || !embeddingProfile?.configured || !profileDraftMatches || profileLoading || profileError || profileConflict) return
    const current = generation.current
    const expectedFingerprint = embeddingProfile.fingerprint
    let previous = embeddingProfile.progress.indexed
    const maxBatches = Math.max(1, embeddingProfile.progress.total - previous)
    let batches = 0
    stopAfterBatch.current = false
    setStopRequested(false)
    setPending(all ? 'vectors-all' : 'vectors'); setError(undefined); setNotice(undefined)
    try {
      while (true) {
        if (batches >= maxBatches) {
          setNotice(t('O limite de lotes desta ação foi atingido. O progresso está salvo; continue quando quiser.'))
          return
        }
        batches++
        const profile = await backend.indexKnowledgeVectors({ workspaceId, expectedFingerprint })
        if (!activeRequest(current)) return
        if (!profile.configured || profile.fingerprint !== expectedFingerprint) {
          setProfileConflict(true)
          setError(t('O perfil local mudou. Atualize e confirme o destino antes de indexar.'))
          return
        }
        setEmbeddingProfile(profile)
        if (!all || stopAfterBatch.current || profile.progress.indexed >= profile.progress.total) {
          setNotice(profile.progress.indexed >= profile.progress.total ? t('Vetores locais indexados.') : stopAfterBatch.current && all ? t('Sequência parada após o lote. O progresso está salvo.') : t('Lote salvo. Continue quando quiser; o progresso permanece após reiniciar.'))
          return
        }
        if (profile.progress.indexed <= previous) {
          setError(t('A indexação não avançou. Confira o modelo local antes de tentar novamente.'))
          return
        }
        previous = profile.progress.indexed
      }
    } catch (failure) { if (activeRequest(current)) { setError(errorMessage(failure)); if (errorCode(failure) === 'local_embedding_profile_changed') setProfileConflict(true) } }
    finally { if (activeRequest(current)) { setPending(''); setStopRequested(false) } }
  }

  const target = `${embeddingURL.replace(/\/+$/, '')}${embeddingKind === 'ollama' ? '/api/embed' : '/v1/embeddings'}`
  const activeTarget = embeddingProfile?.configured ? `${embeddingProfile.baseUrl}${embeddingProfile.kind === 'ollama' ? '/api/embed' : '/v1/embeddings'}` : ''
  const profileDraftMatches = !!embeddingProfile?.configured && embeddingKind === embeddingProfile.kind && embeddingURL.trim() === embeddingProfile.baseUrl && embeddingModel.trim() === embeddingProfile.model

  // Once documents exist, searching them is the job; the forms for adding sources and tuning embeddings follow.
  // The DOM order never changes (nothing remounts while the library loads); only the visual order does.
  return <div className={`knowledge-page${items.length > 0 ? ' is-library-first' : ''}`}>
    <div className="destination-heading knowledge-heading"><div><h2>{t('Conhecimento local')}</h2><p className="muted">{t('Fontes deste projeto, guardadas e pesquisadas nesta máquina.')}</p></div><span className="status-chip status-ready">{embeddingProfile?.progress.indexed ? t('Busca local') : t('Busca textual')}</span></div>
    {!workspaceId ? <div className="catalog-empty"><FolderOpen aria-hidden="true" /><strong>{t('Abra um projeto para indexar arquivos')}</strong><span className="muted">{t('A biblioteca pertence ao diretório autorizado que você escolher.')}</span><button type="button" className="touch-target primary-button" onClick={onProjects}>{t('Abrir projetos')}</button></div> : <>
      <form className="knowledge-import" onSubmit={importFile}>
        <div className="destination-heading"><div><h3>{t('Adicionar fonte')}</h3><p className="muted">{t('Arquivos .txt, .md e .markdown dentro do projeto, até 2 MB.')}</p></div><Upload aria-hidden="true" /></div>
        <label className="field">{t('Arquivo do projeto')}<input value={path} onChange={event => setPath(event.target.value)} placeholder="docs/guia.md" autoComplete="off" required /></label>
        <p className="project-warning">{t('Trechos encontrados podem ser enviados ao provedor de IA quando um agente usar a ferramenta de busca. Indexe somente fontes que você pode compartilhar com o backend escolhido.')}</p>
        <div className="knowledge-import-actions"><button type="button" className="touch-target secondary-button" onClick={() => void chooseFile()} disabled={!!pending}>{t('Escolher arquivo')}</button><button type="submit" className="touch-target primary-button" disabled={!!pending || !path.trim()}><Upload aria-hidden="true" />{pending === 'import' ? t('Indexando…') : t('Indexar arquivo')}</button></div>
      </form>
      <section className="knowledge-embedding" aria-labelledby="knowledge-embedding-title">
        <div className="destination-heading"><div><h3 id="knowledge-embedding-title">{t('Embeddings locais')}</h3><p className="muted">{t('Escolha um servidor e modelo já disponíveis nesta máquina.')}</p></div></div>
        <form className="knowledge-embedding-form" onSubmit={saveEmbeddingProfile}>
          <IonPicker id="knowledge-server" label={t('Servidor local')} value={embeddingKind} disabled={!!pending}
            onChange={next => { const kind = next as 'ollama' | 'lm_studio'; setEmbeddingURL(current => current === localDefaults[embeddingKind] ? localDefaults[kind] : current); setEmbeddingKind(kind) }}
            options={[{ value: 'ollama', label: 'Ollama' }, { value: 'lm_studio', label: 'LM Studio' }]} />
          <label className="field">{t('URL local')}<input value={embeddingURL} onChange={event => setEmbeddingURL(event.target.value)} placeholder={localDefaults[embeddingKind]} autoComplete="off" spellCheck={false} required disabled={!!pending} /></label>
          <label className="field">{t('Modelo de embeddings')}<input value={embeddingModel} onChange={event => setEmbeddingModel(event.target.value)} placeholder={t('Identificador do modelo carregado')} maxLength={128} autoComplete="off" spellCheck={false} required disabled={!!pending} /></label>
          <p className="knowledge-target">{t('Destino ao salvar:')} <code>{target}</code></p>
          <p className="project-warning">{t('Somente IP de loopback. Salvar não envia documentos nem baixa ou inicia modelos; escolha um lote ou todos os trechos para enviar ao destino ativo. Resultados da busca podem seguir ao provedor de IA escolhido na sessão.')}</p>
          <button type="submit" className="touch-target secondary-button" disabled={!!pending || !embeddingModel.trim() || !embeddingURL.trim()}>{pending === 'embedding-profile' ? t('Salvando…') : t('Salvar perfil local')}</button>
        </form>
        {profileLoading && <p className="muted" role="status">{t('Carregando perfil local…')}</p>}
        {profileError && <div className="inline-error" role="alert"><p>{t('Não foi possível carregar o perfil local.')}</p><button type="button" className="touch-target secondary-button" onClick={() => void refreshProfile()}>{t('Tentar novamente')}</button></div>}
        {embeddingProfile?.configured && <div className="knowledge-vector-progress"><div><p role="status">{t('{indexed} de {total} trechos com vetores', { indexed: embeddingProfile.progress.indexed, total: embeddingProfile.progress.total })}</p><p className="muted">{t('Destino ativo:')} <code>{activeTarget}</code> · {t('modelo')} <code>{embeddingProfile.model}</code></p>{embeddingProfile.progress.dimension > 0 && <p className="muted">{t('{count} dimensões', { count: embeddingProfile.progress.dimension })}</p>}{!profileDraftMatches && <p className="project-warning">{t('Salve as alterações do perfil antes de indexar novos trechos.')}</p>}{profileConflict && <p className="project-warning">{t('O perfil mudou em outra janela. Atualize e confirme o destino ativo.')}</p>}{pending === 'vectors-all' && <p className="muted">{t('A parada aguarda o lote atual, limitado a 30 segundos.')}</p>}</div><div className="knowledge-vector-actions"><button type="button" className="touch-target secondary-button" onClick={() => void refreshProfile()} disabled={!!pending || profileLoading}>{t('Atualizar perfil')}</button>{embeddingProfile.progress.indexed < embeddingProfile.progress.total && <><button type="button" className="touch-target secondary-button" onClick={() => void indexVectors(false)} disabled={!!pending || !profileDraftMatches || profileLoading || profileError || profileConflict}>{pending === 'vectors' ? t('Indexando lote…') : t('Indexar próximo lote')}</button>{pending === 'vectors-all' ? <button type="button" className="touch-target primary-button" onClick={() => { stopAfterBatch.current = true; setStopRequested(true) }} disabled={stopRequested}>{stopRequested ? t('Parando após este lote…') : t('Parar após este lote')}</button> : <button type="button" className="touch-target primary-button" onClick={() => void indexVectors(true)} disabled={!!pending || !profileDraftMatches || profileLoading || profileError || profileConflict}>{t('Indexar todos os trechos')}</button>}</>}</div></div>}
      </section>
      {state === 'loading' && <p className="muted" role="status">{t('Carregando biblioteca…')}</p>}
      {state === 'error' && <div className="inline-error" role="alert"><p>{t('Não foi possível carregar a biblioteca.')}</p><button type="button" className="touch-target secondary-button" onClick={() => void refresh()}>{t('Tentar novamente')}</button></div>}
      {state === 'ready' && <div className="knowledge-layout">
        <section className="knowledge-library" aria-labelledby="knowledge-library-title"><div className="destination-heading"><div><h3 id="knowledge-library-title">{t('Biblioteca')}</h3><p className="muted">{items.length} {items.length === 1 ? t('documento') : t('documentos')} {t('indexados')}</p></div><button type="button" className="touch-target secondary-button" onClick={() => void refresh()} disabled={!!pending} aria-label={t('Atualizar biblioteca')}><RefreshCw aria-hidden="true" />{t('Atualizar')}</button></div>
          {items.length === 0 ? <div className="catalog-empty"><FileText aria-hidden="true" /><strong>{t('Nenhum documento indexado')}</strong><span className="muted">{t('Escolha um arquivo do projeto para começar.')}</span></div> : <ul className="knowledge-documents">{items.map(item => <li key={item.id} aria-label={t('Documento {path}', { path: item.path })} className="knowledge-document"><div className="knowledge-document-title"><FileText aria-hidden="true" /><strong>{item.path}</strong></div><dl className="knowledge-metadata"><div><dt>{t('Fonte modificada')}</dt><dd>{formatDate(item.sourceModifiedAt)}</dd></div><div><dt>{t('Indexado')}</dt><dd>{formatDate(item.indexedAt)}</dd></div><div><dt>{t('Trechos')}</dt><dd>{item.chunkCount}</dd></div><div><dt>{t('Tamanho')}</dt><dd>{item.sourceSize.toLocaleString(localeTag())} {t('bytes')}</dd></div></dl><div className="knowledge-document-actions"><button type="button" className="touch-target secondary-button" onClick={() => void reindex(item)} disabled={!!pending} aria-label={t('Reindexar {path}', { path: item.path })}><RefreshCw aria-hidden="true" />{t('Reindexar')}</button><button type="button" className="touch-target secondary-button" onClick={() => void remove(item)} disabled={!!pending} aria-label={t('Remover {path} do índice', { path: item.path })}><Trash2 aria-hidden="true" />{t('Remover do índice')}</button></div></li>)}</ul>}
        </section>
        <section className="knowledge-search" aria-labelledby="knowledge-search-title"><div className="destination-heading"><div><h3 id="knowledge-search-title">{t('Buscar nos documentos')}</h3><p className="muted">{embeddingProfile?.progress.indexed ? t('Termos e vetores do modelo local configurado.') : t('Busca textual por termos. Vetores locais são opcionais.')}</p></div><FileSearch aria-hidden="true" /></div><form className="knowledge-search-form" onSubmit={search}><label className="field">{t('Buscar no conhecimento')}<input value={query} onChange={event => setQuery(event.target.value)} placeholder={t('Termo ou expressão')} maxLength={256} required /></label><IonPicker id="knowledge-document" label={t('Documento')} value={documentId} onChange={setDocumentId} searchable options={[{ value: '', label: t('Todos os documentos') }, ...items.map(item => ({ value: item.id, label: item.path }))]} /><button type="submit" className="touch-target primary-button" disabled={!!pending || !query.trim() || items.length === 0}><Search aria-hidden="true" />{t('Buscar')}</button></form>
          {searchState === 'loading' && <p role="status" className="muted">{t('Buscando trechos…')}</p>}
          {searchState === 'ready' && <p className="knowledge-search-mode" role="status">{searchModeNames()[searchMode]}</p>}
          {searchState === 'ready' && (hits.length === 0 ? <p className="muted knowledge-no-results">{t('Nenhum trecho encontrado para esta busca.')}</p> : <ol className="knowledge-results">{hits.map((hit, index) => <li key={`${hit.documentId}-${hit.lineStart}-${index}`} aria-label={t('Resultado em {path} linha {line}', { path: hit.path, line: hit.lineStart })}><div className="knowledge-result-source"><FileText aria-hidden="true" /><strong>{hit.path} · {t('linha')} {hit.lineStart}</strong></div><p className="knowledge-snippet">{hit.snippet}</p><p className="muted knowledge-result-time">{t('Fonte modificada {date}', { date: formatDate(hit.sourceModifiedAt) })} · {t('indexada {date}', { date: formatDate(hit.indexedAt) })}</p></li>)}</ol>)}
        </section>
      </div>}
    </>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {notice && <p className="form-success" role="status">{notice}</p>}
  </div>
}
