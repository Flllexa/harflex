import { useEffect, useRef, useState, type FormEvent } from 'react'
import { FileSearch, FileText, FolderOpen, RefreshCw, Search, Trash2, Upload } from 'lucide-react'
import { errorCode, errorMessage, type Backend, type KnowledgeDocument, type KnowledgeEmbeddingProfile, type KnowledgeHit, type KnowledgeSearch } from '../../lib/backend'
import { IonPicker } from '../../components/IonPicker'

type Props = { backend: Backend; workspaceId?: string; onProjects: () => void }

function formatDate(value: string) {
  return new Date(value).toLocaleString('pt-BR', { dateStyle: 'short', timeStyle: 'short' })
}

const localDefaults = { ollama: 'http://127.0.0.1:11434', lm_studio: 'http://127.0.0.1:1234' }
const searchModeNames: Record<KnowledgeSearch['mode'], string> = {
  textual: 'Busca textual', textual_fallback: 'Busca textual · vetores indisponíveis',
  semantic: 'Busca semântica local', hybrid: 'Busca híbrida',
}

export function KnowledgePage({ backend, workspaceId, onProjects }: Props) {
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
      setNotice(`${item.path} indexado. ${item.chunkCount} ${item.chunkCount === 1 ? 'trecho disponível' : 'trechos disponíveis'} para busca textual.`)
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
      setNotice(`${updated.path} relido da origem.`)
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
      setNotice(`${item.path} removido do índice. O arquivo original permanece no projeto.`)
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
      setNotice('Perfil local salvo. Indexe os trechos quando o modelo estiver disponível.')
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
          setNotice('O limite de lotes desta ação foi atingido. O progresso está salvo; continue quando quiser.')
          return
        }
        batches++
        const profile = await backend.indexKnowledgeVectors({ workspaceId, expectedFingerprint })
        if (!activeRequest(current)) return
        if (!profile.configured || profile.fingerprint !== expectedFingerprint) {
          setProfileConflict(true)
          setError('O perfil local mudou. Atualize e confirme o destino antes de indexar.')
          return
        }
        setEmbeddingProfile(profile)
        if (!all || stopAfterBatch.current || profile.progress.indexed >= profile.progress.total) {
          setNotice(profile.progress.indexed >= profile.progress.total ? 'Vetores locais indexados.' : stopAfterBatch.current && all ? 'Sequência parada após o lote. O progresso está salvo.' : 'Lote salvo. Continue quando quiser; o progresso permanece após reiniciar.')
          return
        }
        if (profile.progress.indexed <= previous) {
          setError('A indexação não avançou. Confira o modelo local antes de tentar novamente.')
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
    <div className="destination-heading knowledge-heading"><div><h2>Conhecimento local</h2><p className="muted">Fontes deste projeto, guardadas e pesquisadas nesta máquina.</p></div><span className="status-chip status-ready">{embeddingProfile?.progress.indexed ? 'Busca local' : 'Busca textual'}</span></div>
    {!workspaceId ? <div className="catalog-empty"><FolderOpen aria-hidden="true" /><strong>Abra um projeto para indexar arquivos</strong><span className="muted">A biblioteca pertence ao diretório autorizado que você escolher.</span><button type="button" className="touch-target primary-button" onClick={onProjects}>Abrir projetos</button></div> : <>
      <form className="knowledge-import" onSubmit={importFile}>
        <div className="destination-heading"><div><h3>Adicionar fonte</h3><p className="muted">Arquivos .txt, .md e .markdown dentro do projeto, até 2 MB.</p></div><Upload aria-hidden="true" /></div>
        <label className="field">Arquivo do projeto<input value={path} onChange={event => setPath(event.target.value)} placeholder="docs/guia.md" autoComplete="off" required /></label>
        <p className="project-warning">Trechos encontrados podem ser enviados ao provedor de IA quando um agente usar a ferramenta de busca. Indexe somente fontes que você pode compartilhar com o backend escolhido.</p>
        <div className="knowledge-import-actions"><button type="button" className="touch-target secondary-button" onClick={() => void chooseFile()} disabled={!!pending}>Escolher arquivo</button><button type="submit" className="touch-target primary-button" disabled={!!pending || !path.trim()}><Upload aria-hidden="true" />{pending === 'import' ? 'Indexando…' : 'Indexar arquivo'}</button></div>
      </form>
      <section className="knowledge-embedding" aria-labelledby="knowledge-embedding-title">
        <div className="destination-heading"><div><h3 id="knowledge-embedding-title">Embeddings locais</h3><p className="muted">Escolha um servidor e modelo já disponíveis nesta máquina.</p></div></div>
        <form className="knowledge-embedding-form" onSubmit={saveEmbeddingProfile}>
          <IonPicker id="knowledge-server" label="Servidor local" value={embeddingKind} disabled={!!pending}
            onChange={next => { const kind = next as 'ollama' | 'lm_studio'; setEmbeddingURL(current => current === localDefaults[embeddingKind] ? localDefaults[kind] : current); setEmbeddingKind(kind) }}
            options={[{ value: 'ollama', label: 'Ollama' }, { value: 'lm_studio', label: 'LM Studio' }]} />
          <label className="field">URL local<input value={embeddingURL} onChange={event => setEmbeddingURL(event.target.value)} placeholder={localDefaults[embeddingKind]} autoComplete="off" spellCheck={false} required disabled={!!pending} /></label>
          <label className="field">Modelo de embeddings<input value={embeddingModel} onChange={event => setEmbeddingModel(event.target.value)} placeholder="Identificador do modelo carregado" maxLength={128} autoComplete="off" spellCheck={false} required disabled={!!pending} /></label>
          <p className="knowledge-target">Destino ao salvar: <code>{target}</code></p>
          <p className="project-warning">Somente IP de loopback. Salvar não envia documentos nem baixa ou inicia modelos; escolha um lote ou todos os trechos para enviar ao destino ativo. Resultados da busca podem seguir ao provedor de IA escolhido na sessão.</p>
          <button type="submit" className="touch-target secondary-button" disabled={!!pending || !embeddingModel.trim() || !embeddingURL.trim()}>{pending === 'embedding-profile' ? 'Salvando…' : 'Salvar perfil local'}</button>
        </form>
        {profileLoading && <p className="muted" role="status">Carregando perfil local…</p>}
        {profileError && <div className="inline-error" role="alert"><p>Não foi possível carregar o perfil local.</p><button type="button" className="touch-target secondary-button" onClick={() => void refreshProfile()}>Tentar novamente</button></div>}
        {embeddingProfile?.configured && <div className="knowledge-vector-progress"><div><p role="status">{embeddingProfile.progress.indexed} de {embeddingProfile.progress.total} trechos com vetores</p><p className="muted">Destino ativo: <code>{activeTarget}</code> · modelo <code>{embeddingProfile.model}</code></p>{embeddingProfile.progress.dimension > 0 && <p className="muted">{embeddingProfile.progress.dimension} dimensões</p>}{!profileDraftMatches && <p className="project-warning">Salve as alterações do perfil antes de indexar novos trechos.</p>}{profileConflict && <p className="project-warning">O perfil mudou em outra janela. Atualize e confirme o destino ativo.</p>}{pending === 'vectors-all' && <p className="muted">A parada aguarda o lote atual, limitado a 30 segundos.</p>}</div><div className="knowledge-vector-actions"><button type="button" className="touch-target secondary-button" onClick={() => void refreshProfile()} disabled={!!pending || profileLoading}>Atualizar perfil</button>{embeddingProfile.progress.indexed < embeddingProfile.progress.total && <><button type="button" className="touch-target secondary-button" onClick={() => void indexVectors(false)} disabled={!!pending || !profileDraftMatches || profileLoading || profileError || profileConflict}>{pending === 'vectors' ? 'Indexando lote…' : 'Indexar próximo lote'}</button>{pending === 'vectors-all' ? <button type="button" className="touch-target primary-button" onClick={() => { stopAfterBatch.current = true; setStopRequested(true) }} disabled={stopRequested}>{stopRequested ? 'Parando após este lote…' : 'Parar após este lote'}</button> : <button type="button" className="touch-target primary-button" onClick={() => void indexVectors(true)} disabled={!!pending || !profileDraftMatches || profileLoading || profileError || profileConflict}>Indexar todos os trechos</button>}</>}</div></div>}
      </section>
      {state === 'loading' && <p className="muted" role="status">Carregando biblioteca…</p>}
      {state === 'error' && <div className="inline-error" role="alert"><p>Não foi possível carregar a biblioteca.</p><button type="button" className="touch-target secondary-button" onClick={() => void refresh()}>Tentar novamente</button></div>}
      {state === 'ready' && <div className="knowledge-layout">
        <section className="knowledge-library" aria-labelledby="knowledge-library-title"><div className="destination-heading"><div><h3 id="knowledge-library-title">Biblioteca</h3><p className="muted">{items.length} {items.length === 1 ? 'documento' : 'documentos'} indexados</p></div><button type="button" className="touch-target secondary-button" onClick={() => void refresh()} disabled={!!pending} aria-label="Atualizar biblioteca"><RefreshCw aria-hidden="true" />Atualizar</button></div>
          {items.length === 0 ? <div className="catalog-empty"><FileText aria-hidden="true" /><strong>Nenhum documento indexado</strong><span className="muted">Escolha um arquivo do projeto para começar.</span></div> : <ul className="knowledge-documents">{items.map(item => <li key={item.id} aria-label={`Documento ${item.path}`} className="knowledge-document"><div className="knowledge-document-title"><FileText aria-hidden="true" /><strong>{item.path}</strong></div><dl className="knowledge-metadata"><div><dt>Fonte modificada</dt><dd>{formatDate(item.sourceModifiedAt)}</dd></div><div><dt>Indexado</dt><dd>{formatDate(item.indexedAt)}</dd></div><div><dt>Trechos</dt><dd>{item.chunkCount}</dd></div><div><dt>Tamanho</dt><dd>{item.sourceSize.toLocaleString('pt-BR')} bytes</dd></div></dl><div className="knowledge-document-actions"><button type="button" className="touch-target secondary-button" onClick={() => void reindex(item)} disabled={!!pending} aria-label={`Reindexar ${item.path}`}><RefreshCw aria-hidden="true" />Reindexar</button><button type="button" className="touch-target secondary-button" onClick={() => void remove(item)} disabled={!!pending} aria-label={`Remover ${item.path} do índice`}><Trash2 aria-hidden="true" />Remover do índice</button></div></li>)}</ul>}
        </section>
        <section className="knowledge-search" aria-labelledby="knowledge-search-title"><div className="destination-heading"><div><h3 id="knowledge-search-title">Buscar nos documentos</h3><p className="muted">{embeddingProfile?.progress.indexed ? 'Termos e vetores do modelo local configurado.' : 'Busca textual por termos. Vetores locais são opcionais.'}</p></div><FileSearch aria-hidden="true" /></div><form className="knowledge-search-form" onSubmit={search}><label className="field">Buscar no conhecimento<input value={query} onChange={event => setQuery(event.target.value)} placeholder="Termo ou expressão" maxLength={256} required /></label><IonPicker id="knowledge-document" label="Documento" value={documentId} onChange={setDocumentId} searchable options={[{ value: '', label: 'Todos os documentos' }, ...items.map(item => ({ value: item.id, label: item.path }))]} /><button type="submit" className="touch-target primary-button" disabled={!!pending || !query.trim() || items.length === 0}><Search aria-hidden="true" />Buscar</button></form>
          {searchState === 'loading' && <p role="status" className="muted">Buscando trechos…</p>}
          {searchState === 'ready' && <p className="knowledge-search-mode" role="status">{searchModeNames[searchMode]}</p>}
          {searchState === 'ready' && (hits.length === 0 ? <p className="muted knowledge-no-results">Nenhum trecho encontrado para esta busca.</p> : <ol className="knowledge-results">{hits.map((hit, index) => <li key={`${hit.documentId}-${hit.lineStart}-${index}`} aria-label={`Resultado em ${hit.path} linha ${hit.lineStart}`}><div className="knowledge-result-source"><FileText aria-hidden="true" /><strong>{hit.path} · linha {hit.lineStart}</strong></div><p className="knowledge-snippet">{hit.snippet}</p><p className="muted knowledge-result-time">Fonte modificada {formatDate(hit.sourceModifiedAt)} · indexada {formatDate(hit.indexedAt)}</p></li>)}</ol>)}
        </section>
      </div>}
    </>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {notice && <p className="form-success" role="status">{notice}</p>}
  </div>
}
