import { useEffect, useRef, useState, type FormEvent } from 'react'
import { errorMessage, type Backend, type BackendOption, type ModelCatalogResult, type OpenRouterManagementKeyStatus, type ProviderProfile, type ProviderType } from '../../lib/backend'
import { Modal } from '../../components/Modal'
import { IonPicker } from '../../components/IonPicker'

type Props = { backend: Backend; onSaved: (saved: BackendOption) => void; onClose: () => void; initial?: ProviderProfile }

const urlPlaceholder: Record<ProviderType, string> = {
  generic: 'https://seu-provedor.example/v1',
  openai: 'https://api.openai.com/v1',
  openrouter: 'https://openrouter.ai/api/v1',
  lm_studio: 'http://127.0.0.1:1234/v1',
  ollama: 'http://127.0.0.1:11434/v1',
}

export function profileID(name: string) {
  const slug = name.normalize('NFD').replace(/[̀-ͯ]/g, '').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 64)
  return slug && slug !== 'codex' && slug !== 'opencode' && slug !== 'claude' ? slug : `provider-${slug || 'local'}`
}

// Guidance only. Go validates the endpoint and canonical origin before saving.
function isLoopbackURL(value: string): boolean {
  try {
    const url = new URL(value)
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return false
    const authority = value.match(/^https?:\/\/([^/?#]+)/i)?.[1] ?? ''
    if (/^\[::1\](?::\d+)?$/i.test(authority)) return url.hostname === '[::1]'
    const match = authority.match(/^(127\.\d{1,3}\.\d{1,3}\.\d{1,3})(?::\d+)?$/)
    return !!match && url.hostname === match[1] && match[1].split('.').every(part => Number(part) <= 255)
  } catch { return false }
}

function profileOrigin(raw: string): string | null {
  try {
    new URL(raw) // Syntax check only: URL.origin applies IDNA folding that Go deliberately does not.
    const match = raw.match(/^(https?):\/\/([^/?#]+)/)
    if (!match || match[2].includes('@')) return null
    const [, scheme, authority] = match
    const parts = authority.startsWith('[')
      ? authority.match(/^\[([^\]%]+)\](?::(\d+))?$/)
      : authority.match(/^([^:%]+)(?::(\d+))?$/)
    if (!parts) return null
    const host = authority.startsWith('[') ? `[${parts[1]}]` : parts[1]
    const port = parts[2] ? Number(parts[2]) : scheme === 'https' ? 443 : 80
    if (!Number.isInteger(port) || port < 1 || port > 65535) return null
    // Match Go's ASCII-only DNS folding; preserve Unicode spelling as written.
    return `${scheme}://${host.replace(/[A-Z]/g, char => char.toLowerCase())}:${port}`
  } catch { return null }
}

function hasChangedOrigin(previous: string, current: string): boolean {
  const prior = profileOrigin(previous)
  return prior === null || prior !== profileOrigin(current)
}

function requiresHTTPS(type: ProviderType, raw: string): boolean {
  try {
    if (new URL(raw).protocol !== 'http:') return false
    return type === 'openai' || type === 'openrouter' || (type === 'generic' && !isLoopbackURL(raw))
  } catch { return false }
}

export function ProviderDialog({ backend, onSaved, onClose, initial }: Props) {
  const [name, setName] = useState(initial?.name ?? '')
  const [providerType, setProviderType] = useState<ProviderType>(initial?.providerType ?? 'generic')
  const [baseUrl, setBaseUrl] = useState(initial?.baseUrl ?? '')
  const [model, setModel] = useState(initial?.model ?? '')
  const [clearCredential, setClearCredential] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const [saved, setSaved] = useState<string>()
  const [catalog, setCatalog] = useState<ModelCatalogResult>()
  const [catalogState, setCatalogState] = useState<'idle' | 'loading' | 'ready' | 'interrupted' | 'error'>('idle')
  const [catalogError, setCatalogError] = useState<string>()
  const [managementStatus, setManagementStatus] = useState<OpenRouterManagementKeyStatus>()
  const [managementLoading, setManagementLoading] = useState(!!initial && initial.providerType === 'openrouter')
  const [managementBusy, setManagementBusy] = useState(false)
  const [managementNeedsReadback, setManagementNeedsReadback] = useState(false)
  const [managementError, setManagementError] = useState<string>()
  const [managementDraftPresent, setManagementDraftPresent] = useState(false)
  const [confirmManagementClear, setConfirmManagementClear] = useState(false)
  // The key lives only in the uncontrolled input: never in React, Zustand or storage.
  const keyInput = useRef<HTMLInputElement>(null)
  const managementKeyInput = useRef<HTMLInputElement>(null)
  const catalogController = useRef<AbortController>()
  const catalogVersion = useRef(0)
  const firstField = useRef<HTMLInputElement>(null)
  const mounted = useRef(false)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; catalogController.current?.abort() } }, [])
  useEffect(() => {
    if (!initial || initial.providerType !== 'openrouter') return
    let active = true
    setManagementLoading(true)
    void backend.getOpenRouterManagementKeyStatus(initial.id).then(status => {
      if (active) { setManagementStatus(status); setManagementNeedsReadback(false); setManagementError(undefined) }
    }).catch(() => {
      if (active) { setManagementNeedsReadback(true); setManagementError('Não foi possível ler o estado da chave do catálogo. Atualize antes de alterar.') }
    }).finally(() => { if (active) setManagementLoading(false) })
    return () => { active = false }
  }, [backend, initial?.id, initial?.providerType])

  function invalidateCatalog() {
    catalogVersion.current++
    catalogController.current?.abort()
    catalogController.current = undefined
    setCatalog(undefined)
    setCatalogError(undefined)
    setCatalogState('idle')
  }

  function closeDialog() {
    invalidateCatalog()
    if (keyInput.current) keyInput.current.value = ''
    if (managementKeyInput.current) managementKeyInput.current.value = ''
    onClose()
  }

  function cancelCatalog() {
    catalogVersion.current++
    catalogController.current?.abort()
    catalogController.current = undefined
    setCatalog(undefined)
    setCatalogState('interrupted')
  }

  const localWithoutKey = ['lm_studio', 'ollama', 'generic'].includes(providerType) && isLoopbackURL(baseUrl.trim())
  const localTypePending = !initial?.hasCredential && baseUrl.trim() === '' && (providerType === 'lm_studio' || providerType === 'ollama')
  const invalidLocalEndpoint = (providerType === 'lm_studio' || providerType === 'ollama') && baseUrl.trim() !== '' && !isLoopbackURL(baseUrl.trim())
  const httpsRequired = requiresHTTPS(providerType, baseUrl.trim())
  const effectiveClear = localWithoutKey && clearCredential
  const originChanged = !!initial?.hasCredential && hasChangedOrigin(initial.baseUrl, baseUrl.trim())
  const optionalKey = localWithoutKey || localTypePending
  const keyRequired = !effectiveClear && (originChanged || (!initial?.hasCredential && !optionalKey))
  const keyLabel = originChanged && !effectiveClear ? 'Novo token' : optionalKey ? 'Token opcional' : 'Chave de API'
  const savedRouterEndpoint = !!initial && initial.providerType === 'openrouter' && providerType === 'openrouter' && baseUrl.trim() === initial.baseUrl
  const listedModel = catalog?.models.find(item => item.id === model)
  const modelOptions = catalog ? [
    { value: '', label: 'Escolha um modelo' },
    ...(model && !listedModel ? [{ value: model, label: `${model} · não verificado` }] : []),
    ...catalog.models.map(item => ({ value: item.id, label: item.displayName || item.id })),
  ] : []
  const catalogTime = catalog?.destination && !Number.isNaN(Date.parse(catalog.checkedAt)) ? new Date(catalog.checkedAt).toLocaleString('pt-BR') : ''

  async function queryModels() {
    if (catalogState === 'loading') return
    invalidateCatalog()
    if (managementDraftPresent) {
      setCatalogError('Salve ou descarte a nova chave de gerenciamento antes de consultar. A prévia não mistura chaves antigas e novas.')
      setCatalogState('error')
      return
    }
    if (savedRouterEndpoint && managementNeedsReadback) {
      setCatalogError('O estado da chave do catálogo não foi confirmado. Atualize antes de consultar.')
      setCatalogState('error')
      return
    }
    const key = keyInput.current?.value ?? ''
    const savedProfileUnchanged = !!initial && providerType === initial.providerType && baseUrl.trim() === initial.baseUrl && key === '' && !clearCredential
    const remoteDraftNeedsKey = providerType === 'openai' || providerType === 'openrouter' || (providerType === 'generic' && !isLoopbackURL(baseUrl.trim()))
    if (!savedProfileUnchanged && remoteDraftNeedsKey && !key.trim()) {
      setCatalogError(initial ? 'Digite um novo token para consultar o destino editado. A credencial salva não é reutilizada nesta prévia.' : 'Informe uma chave de API para consultar modelos deste destino.')
      setCatalogState('error')
      return
    }
    const query = {
      profileId: initial?.id ?? profileID(name), searchTerm: '', refresh: true,
      ...(!savedProfileUnchanged ? { draft: { providerType, baseUrl: baseUrl.trim(), apiKey: key } } : {}),
    }
    const controller = new AbortController()
    catalogController.current = controller
    const version = ++catalogVersion.current
    setCatalogState('loading')
    try {
      const result = await backend.queryHTTPModelCatalog(query, controller.signal)
      if (!mounted.current || controller.signal.aborted || version !== catalogVersion.current) return
      setCatalog(result)
      setCatalogState('ready')
    } catch (failure) {
      if (!mounted.current || controller.signal.aborted || version !== catalogVersion.current) return
      setCatalogError(errorMessage(failure))
      setCatalogState('error')
    } finally {
      if (catalogController.current === controller) catalogController.current = undefined
    }
  }

  async function refreshManagementStatus() {
    if (!initial || initial.providerType !== 'openrouter' || managementBusy) return
    setManagementLoading(true)
    setManagementError(undefined)
    try {
      const status = await backend.getOpenRouterManagementKeyStatus(initial.id)
      if (mounted.current) { setManagementStatus(status); setManagementNeedsReadback(false); setConfirmManagementClear(false) }
    } catch {
      if (mounted.current) { setManagementNeedsReadback(true); setManagementError('Não foi possível confirmar o estado da chave do catálogo. Tente atualizar.') }
    } finally {
      if (mounted.current) setManagementLoading(false)
    }
  }

  async function saveManagementKey() {
    if (!initial || !savedRouterEndpoint || managementBusy || managementNeedsReadback) return
    const input = managementKeyInput.current
    const key = input?.value ?? ''
    if (!key.trim()) return
    if (input) input.value = ''
    setManagementDraftPresent(false)
    setConfirmManagementClear(false)
    invalidateCatalog()
    setManagementBusy(true)
    setManagementError(undefined)
    try {
      await backend.saveOpenRouterManagementKey(initial.id, key)
      const status = await backend.getOpenRouterManagementKeyStatus(initial.id)
      if (!mounted.current) return
      setManagementStatus(status)
      setManagementNeedsReadback(false)
    } catch {
      if (mounted.current) { setManagementNeedsReadback(true); setManagementError('O resultado do salvamento não foi confirmado. Atualize o estado antes de repetir.') }
    } finally {
      if (mounted.current) setManagementBusy(false)
    }
  }

  async function clearManagementKey() {
    if (!initial || !savedRouterEndpoint || managementBusy || managementNeedsReadback || !confirmManagementClear) return
    if (managementKeyInput.current) managementKeyInput.current.value = ''
    setManagementDraftPresent(false)
    invalidateCatalog()
    setManagementBusy(true)
    setManagementError(undefined)
    try {
      await backend.clearOpenRouterManagementKey(initial.id)
      const status = await backend.getOpenRouterManagementKeyStatus(initial.id)
      if (!mounted.current) return
      setManagementStatus(status)
      setManagementNeedsReadback(false)
      setConfirmManagementClear(false)
    } catch {
      if (mounted.current) { setManagementNeedsReadback(true); setManagementError('O resultado da remoção não foi confirmado. Atualize o estado antes de repetir.') }
    } finally {
      if (mounted.current) setManagementBusy(false)
    }
  }

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (invalidLocalEndpoint) return
    const input = keyInput.current
    if (!input || saving) return
    setSaving(true)
    setError(undefined)
    const apiKey = input.value
    input.value = ''
    try {
      const result = await backend.saveProviderProfile({ id: initial?.id ?? profileID(name), name: name.trim(), providerType, baseUrl: baseUrl.trim(), model: model.trim(), apiKey, clearCredential: effectiveClear })
      if (!mounted.current) return
      setSaved(`${result.name} · ${model.trim()}`)
      onSaved(result)
    } catch (failure) {
      if (mounted.current) setError(errorMessage(failure))
    } finally {
      if (mounted.current) setSaving(false)
    }
  }

  return <Modal title={initial ? 'Editar provedor' : 'Configurar provedor'} titleId="provider-title" initialFocus={firstField} onClose={closeDialog}>
      <p className="muted dialog-copy">API compatível com OpenAI. Credenciais ficam no cofre do sistema.</p>
      <p className="project-warning">Mensagens e resultados de ferramentas seguem para a URL base escolhida. Confira o destino antes de usar um provedor externo.</p>
      <form className="form-grid" onSubmit={submit}>
        <label>Nome<input ref={firstField} required value={name} onChange={event => { invalidateCatalog(); setName(event.target.value) }} autoComplete="off" /></label>
        <IonPicker id="provider-type" label="Tipo de provedor" value={providerType}
          onChange={next => { invalidateCatalog(); if (keyInput.current) keyInput.current.value = ''; if (managementKeyInput.current) managementKeyInput.current.value = ''; setManagementDraftPresent(false); setConfirmManagementClear(false); setProviderType(next as ProviderType); setClearCredential(false) }}
          options={[{ value: 'generic', label: 'API compatível' }, { value: 'openai', label: 'OpenAI' }, { value: 'openrouter', label: 'OpenRouter' }, { value: 'lm_studio', label: 'LM Studio local' }, { value: 'ollama', label: 'Ollama local' }]} />
        <label>URL base<input required type="url" inputMode="url" placeholder={urlPlaceholder[providerType]} value={baseUrl} onChange={event => { invalidateCatalog(); if (hasChangedOrigin(baseUrl.trim(), event.target.value.trim()) && keyInput.current) keyInput.current.value = ''; if (managementKeyInput.current) managementKeyInput.current.value = ''; setManagementDraftPresent(false); setConfirmManagementClear(false); setBaseUrl(event.target.value); setClearCredential(false) }} autoComplete="off" /></label>
        {httpsRequired && <p className="provider-warning" role="alert">HTTPS necessário: {initial?.endpointBlocked ? 'este endpoint legado' : 'este destino'} está bloqueado para novas execuções. Atualize a URL antes de usar o provedor.</p>}
        {invalidLocalEndpoint && <p className="provider-warning" role="alert">Este provedor local exige uma URL com IP literal de loopback (127.x.x.x ou [::1]).</p>}
        {originChanged && !effectiveClear && !invalidLocalEndpoint && <p className="provider-credential-note provider-key-required">Informe um novo token ou remova o token ativo antes de salvar neste destino.</p>}
        {initial?.hasCredential && !originChanged && !effectiveClear && !invalidLocalEndpoint && <p className="provider-credential-note">Deixe o campo vazio para reutilizar a credencial atual. Ela será enviada ao destino exibido na URL base.</p>}
        {!invalidLocalEndpoint && <label>{keyLabel}<input ref={keyInput} required={keyRequired} disabled={effectiveClear} type="password" autoComplete="off" spellCheck={false} onInput={invalidateCatalog} /></label>}
        {initial?.hasCredential && localWithoutKey && <label className="provider-clear-credential"><input type="checkbox" checked={clearCredential} onChange={event => { invalidateCatalog(); setClearCredential(event.target.checked); if (event.target.checked && keyInput.current) keyInput.current.value = '' }} /><span>Remover token ativo</span></label>}
        {effectiveClear && <p className="provider-credential-note">O token deixa de ser usado neste perfil. O histórico de versões permanece no cofre.</p>}
        {providerType === 'openrouter' && <section className="provider-management" aria-label="Catálogo da conta OpenRouter">
          <div className="provider-catalog-heading"><strong>Catálogo da conta</strong><span className="muted">Chave de gerenciamento opcional, separada da chave de inferência.</span></div>
          {!initial || initial.providerType !== 'openrouter' ? <p className="provider-catalog-note">Salve o perfil OpenRouter antes de configurar o catálogo filtrado da conta.</p> : <>
            {managementLoading && <p className="muted" role="status">Lendo estado da chave do catálogo…</p>}
            {!managementLoading && managementStatus && <p className="provider-catalog-note" role="status">
              {managementStatus.configured ? managementStatus.usable ? 'Chave configurada para esta origem. A permissão será verificada ao consultar modelos.' : 'Chave vinculada a outra origem ou a um perfil bloqueado. Rotacione antes de consultar a conta.' : 'Sem chave de gerenciamento. A consulta usa o catálogo geral não filtrado pela conta.'}
            </p>}
            {!savedRouterEndpoint && <p className="project-warning">Para esta URL editada, salve o novo destino e depois rotacione a chave do catálogo. A chave atual não será enviada a outra origem.</p>}
            {savedRouterEndpoint && <>
              <label>Chave de gerenciamento do catálogo<input ref={managementKeyInput} type="password" autoComplete="off" spellCheck={false} onInput={event => { invalidateCatalog(); setManagementDraftPresent(!!event.currentTarget.value); setManagementError(undefined) }} disabled={managementBusy || managementLoading || managementNeedsReadback} /></label>
              {managementDraftPresent && <p className="provider-catalog-note">Salve esta nova chave antes de consultar; a chave ativa não é substituída enquanto você digita.</p>}
              <div className="provider-catalog-actions">
                <button type="button" className="touch-target secondary-button" onClick={() => void saveManagementKey()} disabled={!managementDraftPresent || managementBusy || managementLoading || managementNeedsReadback}>Salvar chave do catálogo</button>
                {managementStatus?.configured && !confirmManagementClear && <button type="button" className="touch-target secondary-button" onClick={() => setConfirmManagementClear(true)} disabled={managementBusy || managementNeedsReadback}>Remover chave do catálogo</button>}
                <button type="button" className="touch-target text-button" onClick={() => void refreshManagementStatus()} disabled={managementBusy || managementLoading}>Atualizar estado</button>
              </div>
              {confirmManagementClear && <div className="provider-management-confirm"><p>Remover a chave ativa? O histórico permanece no cofre. Novas consultas voltarão ao catálogo geral não filtrado.</p><div className="provider-catalog-actions"><button type="button" className="touch-target secondary-button" onClick={() => setConfirmManagementClear(false)} disabled={managementBusy}>Voltar</button><button type="button" className="touch-target secondary-button" onClick={() => void clearManagementKey()} disabled={managementBusy || managementNeedsReadback}>Confirmar remoção</button></div></div>}
            </>}
          </>}
          {managementError && <p className="form-error" role="alert">{managementError}</p>}
        </section>}
        <section className="provider-catalog" aria-label="Catálogo de modelos">
          <div className="provider-catalog-heading"><strong>Modelos disponíveis</strong><span className="muted">Consulta explícita ao destino acima.</span></div>
          <div className="provider-catalog-actions">
            <button type="button" className="touch-target secondary-button" onClick={() => void queryModels()} disabled={catalogState === 'loading' || managementBusy || managementDraftPresent || !baseUrl.trim() || invalidLocalEndpoint || httpsRequired || (savedRouterEndpoint && (managementLoading || managementNeedsReadback || (!!managementStatus?.configured && !managementStatus.usable)))}>Consultar modelos</button>
            {catalogState === 'loading' && <button type="button" className="touch-target secondary-button" onClick={cancelCatalog}>Cancelar consulta</button>}
          </div>
          {catalogState === 'loading' && <p className="muted" role="status">Consultando modelos deste destino…</p>}
          {catalogState === 'interrupted' && <p className="muted" role="status">Consulta interrompida. O modelo informado foi preservado.</p>}
          {catalogState === 'error' && <p className="form-error" role="alert">{catalogError || 'Não foi possível consultar os modelos.'} O modelo informado foi preservado.</p>}
          {catalog && <>
            <p className={catalog.complete ? 'provider-catalog-note' : 'project-warning'} role="status">
              {catalog.status === 'complete' ? `${catalog.models.length} ${catalog.models.length === 1 ? 'modelo listado' : 'modelos listados'} neste destino.` :
                catalog.status === 'empty' ? 'Consulta completa: nenhum modelo listado neste destino.' :
                  catalog.status === 'partial' ? 'Consulta incompleta. Um modelo não localizado pode continuar disponível; confira ou consulte novamente.' :
                    catalog.status === 'interrupted' ? 'Consulta interrompida. Não há prova de ausência de modelos.' :
                      catalog.status === 'unsupported' ? 'Este provedor não oferece lista de modelos nesta URL. Informe um ID manual não verificado.' :
                        'A consulta falhou. Confira o destino e tente novamente; o ID manual permanece não verificado.'}
            </p>
            {providerType === 'openrouter' && <p className="provider-catalog-note">{catalog.accountFiltered ? 'Catálogo filtrado pelas preferências da conta.' : 'Catálogo geral, não filtrado pelas preferências da conta.'}</p>}
            {catalog.destination && <p className="provider-catalog-note mono">Destino consultado: {catalog.destination}</p>}
            {catalogTime && <p className="provider-catalog-note">Consulta em <time dateTime={catalog.checkedAt}>{catalogTime}</time>.</p>}
            {catalog.models.length > 0 && <IonPicker id="provider-model" label="Escolher modelo listado" value={model} onChange={setModel} searchable options={modelOptions} />}
          </>}
          {model && !listedModel && <p className="provider-catalog-note">ID atual informado manualmente · não verificado neste catálogo.</p>}
          {providerType === 'lm_studio' && listedModel?.loaded === false && <p className="project-warning">Modelo instalado, mas não carregado. A primeira inferência pode carregá-lo; confirme o efeito antes de executar.</p>}
        </section>
        <label>Modelo<input required value={model} onChange={event => setModel(event.target.value)} autoComplete="off" spellCheck={false} /></label>
        {error && <p className="form-error" role="alert">{error}</p>}
        {saved && <p className="form-success" role="status">Salvo: {saved}</p>}
        <div className="dialog-actions">
          <button type="button" className="touch-target secondary-button" onClick={closeDialog}>Fechar</button>
          <button type="submit" className="touch-target primary-button" disabled={saving || managementBusy || invalidLocalEndpoint}>{saving ? 'Salvando' : 'Salvar provedor'}</button>
        </div>
      </form>
  </Modal>
}
