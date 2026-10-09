import { useEffect, useRef, useState } from 'react'
import { cliCatalogProblem, errorMessage, type Backend, type BackendOption, type ModelCatalogResult, type PipelineRoleModelSelection, type ProviderProfile } from '../../lib/backend'
import { IonPicker } from '../../components/IonPicker'
import { useT } from '../../i18n'

type Props = {
  backend: Backend
  workspaceId?: string
  stage: 'code' | 'eval' | 'prs'
  backendOption: BackendOption
  defaultModelBackendId: string
  defaultModelId: string
  /** The default above is what the project chose for this phase, not the default of Settings. */
  phaseConfigured?: boolean
  disabled: boolean
  onSelectionChange: (selection?: PipelineRoleModelSelection) => void
}

const labels = { code: 'Code', eval: 'QA', prs: 'PRs' } as const
/** The backend refuses a pick whose catalog answer is older than five minutes; the pick is withdrawn a little before. */
const catalogMaxAge = 270_000
const usableProfile = (profile: ProviderProfile) => !profile.endpointBlocked && profile.providerType !== 'generic' &&
  (profile.hasCredential || profile.providerType === 'lm_studio' || profile.providerType === 'ollama')

function selectedRoleModel(catalog: ModelCatalogResult, backend: BackendOption, modelId: string, profile: ProviderProfile | undefined, confirmUnfiltered: boolean, confirmJitLoad: boolean): PipelineRoleModelSelection | undefined {
  const model = catalog.models.find(item => item.id === modelId && item.backendId === backend.id && item.source === catalog.source)
  if (!model || model.availability === 'unavailable' || catalog.status !== 'complete' || !catalog.complete || !catalog.profileRevision) return undefined
  if (backend.kind === 'cli') {
    const choice = { backendId: backend.id, profileId: '' as const, modelId, catalogRevision: catalog.profileRevision, destination: '' as const,
      checkedAt: catalog.checkedAt, credentialToken: '' as const, reasoningEffort: '', confirmUnverifiedManual: false as const, confirmUnfiltered: false as const,
      confirmJitLoad: false as const, maxOutputTokens: 0 as const }
    if (backend.id === 'codex') return catalog.source === 'codex_app_server' ? { ...choice, executor: 'codex_cli' as const, backendId: 'codex' as const, source: 'codex_app_server' as const } : undefined
    return { ...choice, executor: 'cli' as const, source: catalog.source }
  }
  if (!profile || !usableProfile(profile) || !catalog.credentialToken) return undefined
  const needsUnfiltered = profile.providerType === 'openrouter' && catalog.source === 'openrouter_general_unfiltered'
  const needsJitLoad = profile.providerType === 'lm_studio' && model.loaded === false
  if (needsUnfiltered && !confirmUnfiltered || needsJitLoad && !confirmJitLoad) return undefined
  return { executor: 'api', backendId: '', profileId: backend.id, modelId, catalogRevision: catalog.profileRevision, source: catalog.source,
    destination: catalog.destination, checkedAt: catalog.checkedAt, credentialToken: catalog.credentialToken, reasoningEffort: '',
    confirmUnverifiedManual: false, confirmUnfiltered, confirmJitLoad, maxOutputTokens: 0 }
}

export function PipelineRoleModelPicker({ backend, workspaceId, stage, backendOption, defaultModelBackendId, defaultModelId, phaseConfigured = false, disabled, onSelectionChange }: Props) {
  const t = useT()
  const [catalog, setCatalog] = useState<ModelCatalogResult>()
  const [profiles, setProfiles] = useState<ProviderProfile[]>([])
  const [catalogState, setCatalogState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [catalogError, setCatalogError] = useState('')
  const [modelId, setModelId] = useState('')
  const [confirmUnfiltered, setConfirmUnfiltered] = useState(false)
  const [confirmJitLoad, setConfirmJitLoad] = useState(false)
  const [refreshRevision, setRefreshRevision] = useState(0)
  const selectionCallback = useRef(onSelectionChange)
  // The model to keep across a refresh of the catalog that grew old while the page stayed open.
  const keepModel = useRef('')
  const request = useRef(0)
  selectionCallback.current = onSelectionChange
  const profile = profiles.find(item => item.id === backendOption.id)
  const selectedModel = catalog?.models.find(item => item.id === modelId && item.backendId === backendOption.id && item.source === catalog.source)
  const needsUnfiltered = profile?.providerType === 'openrouter' && catalog?.source === 'openrouter_general_unfiltered'
  const needsJitLoad = profile?.providerType === 'lm_studio' && selectedModel?.loaded === false
  // Time moves on while the person reads: a slow tick lets an answer that has grown old be noticed without another event.
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 30_000)
    return () => clearInterval(timer)
  }, [])
  const catalogAge = catalog ? now - Date.parse(catalog.checkedAt) : Infinity
  const catalogFresh = catalogAge >= -60_000 && catalogAge <= catalogMaxAge
  const catalogUsable = catalogState === 'ready' && !!catalog && catalogFresh && catalog.status === 'complete' && catalog.complete && !!catalog.profileRevision
  const selectionReady = catalogUsable && !!selectedRoleModel(catalog, backendOption, modelId, profile, confirmUnfiltered, confirmJitLoad)

  useEffect(() => {
    const current = ++request.current
    const controller = new AbortController()
    setCatalog(undefined)
    setModelId('')
    setCatalogError('')
    setConfirmUnfiltered(false)
    setConfirmJitLoad(false)
    setCatalogState('loading')
    selectionCallback.current(undefined)
    if (!workspaceId || !backendOption.available) {
      setCatalogState('error')
      setCatalogError(t('Escolha um projeto e um executor disponível antes de consultar modelos.'))
      return () => { request.current++ }
    }
    void (async () => {
      let selectedProfile: ProviderProfile | undefined
      if (backendOption.kind === 'api') {
        const found = await backend.listProviderProfiles()
        if (request.current !== current) return
        setProfiles(found)
        selectedProfile = found.find(item => item.id === backendOption.id)
        if (!selectedProfile || !usableProfile(selectedProfile)) throw new Error(t('Este perfil API não está disponível para executar esta fase.'))
      } else {
        setProfiles([])
      }
      const result = backendOption.kind === 'cli'
        ? await backend.queryCLIModelCatalog({ workspaceId, backendId: backendOption.id }, controller.signal)
        : await backend.queryHTTPModelCatalog({ profileId: backendOption.id, searchTerm: '', refresh: true }, controller.signal)
      if (request.current !== current) return
      setCatalog(result)
      const age = Date.now() - Date.parse(result.checkedAt)
      if (result.status !== 'complete' || !result.complete || !result.profileRevision || age < -60_000 || age > catalogMaxAge) {
        setCatalogState('error')
        setCatalogError(result.status === 'empty'
          ? t('Nenhum modelo disponível em {name}.', { name: backendOption.name })
          : result.status === 'partial' ? t('O catálogo veio incompleto; atualize antes de iniciar a fase.') : backendOption.kind === 'cli' ? cliCatalogProblem(result, backendOption.name) : t('Não foi possível confirmar o catálogo deste executor.'))
        return
      }
      setCatalogState('ready')
      const kept = keepModel.current
      keepModel.current = ''
      const preferred = kept || (defaultModelBackendId === backendOption.id ? defaultModelId : selectedProfile?.model ?? '')
      const preferredIsListed = result.models.some(item => item.id === preferred && item.backendId === backendOption.id && item.source === result.source && item.availability !== 'unavailable')
      if (preferredIsListed) {
        setModelId(preferred)
        selectionCallback.current(selectedRoleModel(result, backendOption, preferred, selectedProfile, false, false))
      }
    })().catch(failure => {
      if (request.current !== current || controller.signal.aborted) return
      setCatalogState('error')
      setCatalogError(errorMessage(failure))
    })
    return () => { request.current++; controller.abort() }
  }, [backend, workspaceId, backendOption.id, backendOption.kind, backendOption.available, backendOption.name, defaultModelBackendId, defaultModelId, refreshRevision, t])

  // An answer that grew old is no longer a pick the backend would take: it is read again by itself, keeping the model.
  useEffect(() => {
    if (catalogState !== 'ready' || !catalog || catalogFresh) return
    selectionCallback.current(undefined)
    keepModel.current = modelId
    setRefreshRevision(revision => revision + 1)
  }, [catalogState, catalog, catalogFresh])

  function changeModel(value: string) {
    setModelId(value)
    if (!catalog || !value) { selectionCallback.current(undefined); return }
    selectionCallback.current(selectedRoleModel(catalog, backendOption, value, profile, confirmUnfiltered, confirmJitLoad))
  }

  function updateConfirmation(kind: 'unfiltered' | 'jit', checked: boolean) {
    const nextUnfiltered = kind === 'unfiltered' ? checked : confirmUnfiltered
    const nextJit = kind === 'jit' ? checked : confirmJitLoad
    if (kind === 'unfiltered') setConfirmUnfiltered(checked)
    else setConfirmJitLoad(checked)
    selectionCallback.current(catalog && modelId ? selectedRoleModel(catalog, backendOption, modelId, profile, nextUnfiltered, nextJit) : undefined)
  }

  const globalModelSelected = defaultModelBackendId === backendOption.id && defaultModelId === modelId
  const profileModelSelected = backendOption.kind === 'api' && defaultModelBackendId !== backendOption.id && !!profile?.model && profile.model === modelId
  const hasConfiguredModel = globalModelSelected || profileModelSelected || (defaultModelBackendId === backendOption.id && !!defaultModelId)
  const catalogStatus = catalog?.status === 'failed' && backendOption.kind === 'cli'
    ? t('{name} indisponível. Verifique o CLI local e atualize o catálogo.', { name: backendOption.name })
    : catalog?.errorCode === 'catalog_context_unverified' ? t('Não foi possível confirmar o contexto local. Atualize o catálogo.')
      : catalog?.errorCode === 'catalog_workspace_unavailable' ? t('A pasta deste projeto não está disponível. Reabra o projeto e atualize o catálogo.') : ''

  return <div className="pipeline-role-model-selection" data-testid={`pipeline-role-model-selection-${stage}`}>
    <IonPicker id={`pipeline-role-model-${stage}`} label={t('Modelo da fase {stage}', { stage: labels[stage] })} value={modelId}
      onChange={changeModel} searchable required disabled={disabled || catalogState !== 'ready' || !catalogUsable}
      options={[{ value: '', label: t('Escolha um modelo') }, ...(catalog?.models ?? []).filter(item => item.backendId === backendOption.id && item.source === catalog?.source).map(item => ({ value: item.id, label: item.displayName || item.id, disabled: item.availability === 'unavailable' }))]} />
    {catalogState === 'loading' && <p className="muted" role="status">{t('Consultando modelos de {name} para {stage}…', { name: backendOption.name, stage: labels[stage] })}</p>}
    {catalogState === 'error' && <div className="inline-error" role="alert"><p>{catalogStatus || catalogError}</p></div>}
    {catalogUsable && <p className="muted pipeline-role-model-provenance">{phaseConfigured && (globalModelSelected || profileModelSelected) ? t('Escolhido para {stage} em Provedor e modelo por fase · {name}', { stage: labels[stage], name: backendOption.name }) : globalModelSelected ? t('Padrão das Configurações · {name}', { name: backendOption.name }) : profileModelSelected ? t('Modelo configurado no perfil · {name}', { name: backendOption.name }) : hasConfiguredModel ? t('Override local de {stage} · {name}', { stage: labels[stage], name: backendOption.name }) : t('Modelo escolhido para {stage} · {name}', { stage: labels[stage], name: backendOption.name })}</p>}
    {needsUnfiltered && <label className="authoring-confirm"><input type="checkbox" checked={confirmUnfiltered} onChange={event => updateConfirmation('unfiltered', event.target.checked)} disabled={disabled} /> {t('Confirmo usar a lista geral não filtrada da OpenRouter.')}</label>}
    {needsJitLoad && <label className="authoring-confirm"><input type="checkbox" checked={confirmJitLoad} onChange={event => updateConfirmation('jit', event.target.checked)} disabled={disabled} /> {t('Confirmo carregar este modelo do LM Studio antes da geração.')}</label>}
    {catalogState === 'ready' && !!catalog && !catalogFresh && <p className="muted" role="status">{t('O catálogo de modelos ficou antigo; atualizando…')}</p>}
    {catalogUsable && !selectionReady && <p className="muted" role="status">{t('Escolha um modelo disponível para habilitar {stage}.', { stage: labels[stage] })}</p>}
    <button type="button" className="touch-target text-button" disabled={disabled} onClick={() => setRefreshRevision(value => value + 1)}>{t('Atualizar catálogo')}</button>
  </div>
}
