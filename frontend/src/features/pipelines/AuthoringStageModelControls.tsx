import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { IonPicker } from '../../components/IonPicker'
import { NO_CATALOG_TIME, errorCode, errorMessage, type APIModelSelection, type AuthoringModelConsentBinding, type AuthoringStageModelPreference, type Backend, type ModelCatalogResult, type Pipeline, type ProviderProfile, type SaveAuthoringStageModelPreferenceInput } from '../../lib/backend'

type Stage = 'spec' | 'plan'
type Draft = { modelMode: 'inherit' | 'override'; effortMode: 'inherit' | 'automatic' | 'explicit'; explicitEffort: string; profileId: string; modelId: string }
type GenerationConsent = { confirmJitLoad: boolean; confirmUnfiltered: boolean; consentBinding?: AuthoringModelConsentBinding }
type Props = { backend: Backend; pipeline: Pipeline; stage: Stage; disabled: boolean; consentResetEpoch: number; onReadinessChange: (identity: string, ready: boolean) => void; onGenerationConsentChange: (identity: string, consent: GenerationConsent) => void; onSettings?: () => void }
const supportedSources = new Set(['openai_models', 'openrouter_account', 'openrouter_general_unfiltered', 'lm_studio_native', 'ollama_tags'])
const emptySelection: APIModelSelection = { executor: 'api', backendId: '', profileId: '', modelId: '', catalogRevision: '', source: '', destination: '', checkedAt: NO_CATALOG_TIME, credentialToken: '', reasoningEffort: '', confirmUnverifiedManual: false, confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 4096 }
const sourceName = (source: string) => source === 'brainstorm' ? 'Brainstorm' : source === 'global_default' ? 'Configurações' : source === 'phase_override' ? 'esta fase' : source || 'Configurações'
const choiceKey = (catalog: ModelCatalogResult, modelId: string) => JSON.stringify([catalog.backendId, modelId, catalog.source, catalog.destination, catalog.profileRevision, catalog.models.find(model => model.id === modelId)?.loaded ?? null])
const freshAt = (time: string) => { const age = Date.now() - Date.parse(time); return Number.isFinite(age) && age <= 300_000 && age >= -60_000 }

export function AuthoringStageModelControls({ backend, pipeline, stage, disabled, consentResetEpoch, onReadinessChange, onGenerationConsentChange, onSettings }: Props) {
  const identity = `${pipeline.workspaceId}:${pipeline.id}:${stage}:${pipeline.discoveryFrozenVersion}`
  const scope = useRef(identity)
  const preferenceEpoch = useRef(0)
  const catalogEpoch = useRef(0)
  const catalogAbort = useRef<AbortController>()
  const [preference, setPreference] = useState<AuthoringStageModelPreference>()
  const [preferenceLoading, setPreferenceLoading] = useState(true)
  const [preferenceError, setPreferenceError] = useState('')
  const [draft, setDraft] = useState<Draft>()
  const [profiles, setProfiles] = useState<ProviderProfile[]>([])
  const [profilesLoading, setProfilesLoading] = useState(true)
  const [profilesError, setProfilesError] = useState('')
  const [catalog, setCatalog] = useState<ModelCatalogResult>()
  const [catalogTokenValidated, setCatalogTokenValidated] = useState(false)
  const [inheritedCatalogBlocked, setInheritedCatalogBlocked] = useState(false)
  const [catalogState, setCatalogState] = useState<'idle' | 'loading' | 'ready' | 'error'>('idle')
  const [catalogMessage, setCatalogMessage] = useState('')
  const [confirmUnfiltered, setConfirmUnfiltered] = useState('')
  const [confirmJit, setConfirmJit] = useState('')
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState('')
  const [reconfirmTick, setReconfirmTick] = useState(0)
  const [saveAfterConsult, setSaveAfterConsult] = useState(false)

  useLayoutEffect(() => {
    scope.current = identity
    preferenceEpoch.current++; catalogEpoch.current++; catalogAbort.current?.abort()
    setPreference(undefined); setPreferenceLoading(true); setPreferenceError(''); setDraft(undefined)
    setProfiles([]); setProfilesLoading(true); setProfilesError('')
    setCatalog(undefined); setCatalogTokenValidated(false); setInheritedCatalogBlocked(false); setCatalogState('idle'); setCatalogMessage(''); setConfirmUnfiltered(''); setConfirmJit(''); setSaving(false); setSaveError('')
    onReadinessChange(identity, false)
    return () => { preferenceEpoch.current++; catalogEpoch.current++; catalogAbort.current?.abort() }
  }, [identity, onReadinessChange])

  useEffect(() => {
    const ticket = ++preferenceEpoch.current
    const current = () => ticket === preferenceEpoch.current && scope.current === identity
    void backend.getAuthoringStageModelPreference({ pipelineId: pipeline.id, stage }).then(value => {
      if (!current()) return
      if (value.pipelineId !== pipeline.id || value.stage !== stage) throw new Error('Preferência fora do contexto da fase atual.')
      setPreference(value)
      setDraft({ modelMode: value.modelMode, effortMode: value.effortMode, explicitEffort: value.explicitEffort, profileId: value.modelMode === 'override' ? value.selection.backendId : '', modelId: value.modelMode === 'override' ? value.selection.modelId : '' })
      setPreferenceError(''); setSaveError('')
    }).catch(cause => { if (current()) setPreferenceError(errorMessage(cause)) }).finally(() => { if (current()) setPreferenceLoading(false) })
    void backend.listProviderProfiles().then(value => {
      if (!current()) return
      setProfiles(value.filter(profile => profile.kind === 'openai_compatible' && profile.providerType !== 'generic'))
      setProfilesError('')
    }).catch(cause => { if (current()) setProfilesError(errorMessage(cause)) }).finally(() => { if (current()) setProfilesLoading(false) })
    return () => { if (ticket === preferenceEpoch.current) preferenceEpoch.current++ }
  }, [backend, identity])

  const selectedProfileId = draft?.modelMode === 'override' ? draft.profileId : preference?.selection.backendId ?? ''
  const selectedModelId = draft?.modelMode === 'override' ? draft.modelId : preference?.selection.modelId ?? ''
  const activeModel = useMemo(() => catalog?.models.find(model => model.id === selectedModelId && model.backendId === selectedProfileId && model.source === catalog.source), [catalog, selectedModelId, selectedProfileId])
  const catalogHasTransientCredential = !!catalog?.credentialToken && /^[a-f0-9]{64}$/.test(catalog.credentialToken)
  const catalogFresh = !!catalog && catalog.complete && catalog.status === 'complete' && catalog.nextCursor === '' && freshAt(catalog.checkedAt) && (catalogTokenValidated || catalogHasTransientCredential) && catalog.backendId === selectedProfileId && supportedSources.has(catalog.source) && !!catalog.destination && !!catalog.profileRevision
  const activeChoiceKey = catalog && activeModel ? choiceKey(catalog, activeModel.id) : ''
  const inheritedConsentMatches = !!preference && !!catalog && !!activeModel && preference.selection.backendId === catalog.backendId && preference.selection.modelId === activeModel.id && preference.selection.catalogRevision === catalog.profileRevision && preference.selection.source === catalog.source && preference.selection.destination === catalog.destination
  const needsUnfilteredConsent = !!catalog && catalog.source === 'openrouter_general_unfiltered' && !((confirmUnfiltered === activeChoiceKey) || (inheritedConsentMatches && !!preference?.selection.confirmUnfiltered))
  const needsJitConsent = !!catalogFresh && catalog.source === 'lm_studio_native' && activeModel?.loaded === false && confirmJit !== activeChoiceKey
  const inheritedEffort = preference?.effortMode === 'inherit' ? preference.selection.reasoningEffort : ''
  const unsupportedInheritedEffort = !!draft && draft.effortMode === 'inherit' && !!inheritedEffort && !!catalogFresh && !!activeModel && !activeModel.supportedReasoningEfforts?.includes(inheritedEffort)
  const selectedEffortAllowed = !!draft && draft.effortMode !== 'explicit' || (catalogFresh && !!activeModel && !!draft?.explicitEffort && !!activeModel.supportedReasoningEfforts?.includes(draft.explicitEffort))
  const catalogRequiredForStoredPreference = preference?.modelMode === 'override' || preference?.effortMode === 'explicit'
  const selectedProfile = profiles.find(profile => profile.id === selectedProfileId)
  const catalogRequiredForGeneration = catalogRequiredForStoredPreference || selectedProfile?.providerType === 'openrouter' || selectedProfile?.providerType === 'lm_studio'
  const draftMatchesPreference = !!preference && !!draft && draft.modelMode === preference.modelMode && draft.effortMode === preference.effortMode && draft.explicitEffort === preference.explicitEffort && (draft.modelMode !== 'override' || (draft.profileId === preference.selection.backendId && draft.modelId === preference.selection.modelId))
  const canGenerate = !!preference && !!draft && preference.resolution === 'ready' && !preferenceLoading && !profilesLoading && !profilesError && !preferenceError && !saveError && !saving && !disabled && draftMatchesPreference && selectedEffortAllowed && !unsupportedInheritedEffort && !inheritedCatalogBlocked && (!catalogRequiredForGeneration || (catalogFresh && !!activeModel && !needsUnfilteredConsent && !needsJitConsent))

  useEffect(() => { onReadinessChange(identity, canGenerate) }, [identity, canGenerate, onReadinessChange])
  useEffect(() => {
    const confirmJitLoad = !!catalogFresh && catalog?.source === 'lm_studio_native' && activeModel?.loaded === false && confirmJit === activeChoiceKey
    const confirmUnfilteredForAttempt = !!catalogFresh && catalog?.source === 'openrouter_general_unfiltered' && (confirmUnfiltered === activeChoiceKey || (inheritedConsentMatches && !!preference?.selection.confirmUnfiltered))
    const consentBinding = (confirmJitLoad || confirmUnfilteredForAttempt) && catalog && activeModel ? {
      backendId: catalog.backendId, modelId: activeModel.id, catalogRevision: catalog.profileRevision,
      source: catalog.source, destination: catalog.destination,
    } : undefined
    onGenerationConsentChange(identity, {
      confirmJitLoad,
      confirmUnfiltered: confirmUnfilteredForAttempt,
      consentBinding,
    })
  }, [identity, catalogFresh, catalog?.source, catalog?.backendId, catalog?.profileRevision, catalog?.destination, activeModel?.id, activeModel?.loaded, confirmJit, confirmUnfiltered, activeChoiceKey, inheritedConsentMatches, preference?.selection.confirmUnfiltered, onGenerationConsentChange])
  useEffect(() => {
    setConfirmUnfiltered(''); setConfirmJit('')
    onGenerationConsentChange(identity, { confirmJitLoad: false, confirmUnfiltered: false, consentBinding: undefined })
  }, [consentResetEpoch, identity, onGenerationConsentChange])

  useEffect(() => { if (reconfirmTick > 0) void consultModels() }, [reconfirmTick])
  const invalidateCatalog = () => { catalogEpoch.current++; catalogAbort.current?.abort(); setCatalog(undefined); setCatalogTokenValidated(false); setInheritedCatalogBlocked(false); setCatalogState('idle'); setCatalogMessage(''); setConfirmUnfiltered(''); setConfirmJit('') }
  function updateDraft(update: Partial<Draft>) { setDraft(current => current ? { ...current, ...update } : current) }

  async function consultModels() {
    if (!preference || !draft || disabled || profilesLoading) return
    const profileId = draft.modelMode === 'override' ? draft.profileId : preference.selection.backendId
    if (!profileId || !profiles.some(profile => profile.id === profileId)) {
      setCatalog(undefined); setCatalogState('error'); setCatalogMessage('Selecione um provedor de API configurado antes de consultar modelos.')
      return
    }
    const ticket = ++catalogEpoch.current
    const current = () => ticket === catalogEpoch.current && scope.current === identity
    catalogAbort.current?.abort()
    const controller = new AbortController(); catalogAbort.current = controller
    setCatalog(undefined); setCatalogState('loading'); setCatalogMessage('Consultando o catálogo completo…'); setConfirmUnfiltered(''); setConfirmJit('')
    try {
      const result = await backend.queryHTTPModelCatalog({ profileId, searchTerm: '', refresh: true }, controller.signal)
      if (!current()) return
      let failure = ''
      if (!result.complete || result.status !== 'complete' || result.nextCursor !== '') failure = 'O catálogo está parcial ou incompleto. Atualize-o antes de escolher modelo ou esforço.'
      else if (!freshAt(result.checkedAt)) failure = 'O catálogo ficou desatualizado. Consulte os modelos novamente.'
      else if (result.backendId !== profileId || !result.destination || !result.profileRevision || !supportedSources.has(result.source) || !result.credentialToken || !/^[a-f0-9]{64}$/.test(result.credentialToken)) failure = 'O perfil ou a credencial mudou; nenhum modelo foi substituído.'
      if (failure) {
        if (draft.modelMode === 'inherit' && result.complete && result.status === 'complete' && result.nextCursor === '' && freshAt(result.checkedAt)) setInheritedCatalogBlocked(true)
        setCatalogState('error'); setCatalogMessage(failure); return
      }
      const exactModel = result.models.find(model => model.id === selectedModelId && model.backendId === result.backendId && model.source === result.source)
      if (draft.modelMode === 'inherit' && !exactModel) { setInheritedCatalogBlocked(true); setCatalogState('error'); setCatalogMessage('O modelo herdado não está neste catálogo completo. Nenhum substituto foi escolhido.'); return }
      if (draft.modelMode === 'inherit' && preference.selection.source !== 'global_default' && (preference.selection.catalogRevision !== result.profileRevision || preference.selection.destination !== result.destination || preference.selection.source !== result.source)) {
        setInheritedCatalogBlocked(true); setCatalogState('error'); setCatalogMessage('O perfil do modelo herdado mudou desde a preferência salva. Revise a configuração antes de continuar.'); return
      }
      if (draft.modelMode === 'inherit') setInheritedCatalogBlocked(false)
      if (draft.modelMode === 'override' && draft.modelId && !exactModel) {
        updateDraft({ modelId: '' })
        setCatalogMessage('O modelo salvo saiu do catálogo. Nenhum substituto foi escolhido; selecione outro modelo explicitamente.')
      } else setCatalogMessage(`Catálogo completo consultado · ${result.models.length} ${result.models.length === 1 ? 'modelo' : 'modelos'}`)
      setCatalog(result); setCatalogTokenValidated(true); setCatalogState('ready')
    } catch (cause) {
      if (current() && !controller.signal.aborted) { setCatalogState('error'); setCatalogMessage(errorMessage(cause)) }
    } finally { if (current()) catalogAbort.current = undefined }
  }

  const currentPreference = preference
  const currentDraft = draft
  // An inherited choice expires with its 5-minute catalog snapshot; the same provider and model can be reconfirmed in two clicks.
  const canReconfirm = currentPreference?.resolution === 'stale' && currentDraft?.modelMode === 'inherit' && !!currentPreference.selection.backendId && !!currentPreference.selection.modelId
    && profiles.some(profile => profile.id === currentPreference.selection.backendId && !profile.endpointBlocked) && !disabled && !profilesLoading
  function reconfirmInherited() {
    if (!currentPreference || !canReconfirm) return
    invalidateCatalog()
    updateDraft({ modelMode: 'override', profileId: currentPreference.selection.backendId, modelId: currentPreference.selection.modelId })
    setSaveAfterConsult(true)
    setReconfirmTick(tick => tick + 1)
  }
  const catalogSelectionReady = !!catalogFresh && !!activeModel && selectedEffortAllowed && !unsupportedInheritedEffort && !needsUnfilteredConsent && catalogState === 'ready'
  const isDirty = !!currentPreference && !!currentDraft && (
    currentDraft.modelMode !== currentPreference.modelMode || currentDraft.effortMode !== currentPreference.effortMode || currentDraft.explicitEffort !== currentPreference.explicitEffort ||
    (currentDraft.modelMode === 'override' && (currentDraft.profileId !== currentPreference.selection.backendId || currentDraft.modelId !== currentPreference.selection.modelId || (catalog?.profileRevision && catalog.profileRevision !== currentPreference.selection.catalogRevision)))
  )
  const needsStaleOverrideRevalidation = currentPreference?.resolution === 'stale' && currentDraft?.modelMode === 'override' && catalogSelectionReady
  const showSaveButton = isDirty || needsStaleOverrideRevalidation
  const canSave = !!currentPreference && !!currentDraft && showSaveButton && !disabled && !saving && !preferenceLoading && !preferenceError && catalogState !== 'loading' && (
    (currentDraft.modelMode === 'inherit' && currentDraft.effortMode !== 'explicit') || (catalogSelectionReady && (currentDraft.modelMode !== 'override' || catalogHasTransientCredential))
  )

  async function savePreference() {
    if (!canSave || !currentPreference || !currentDraft) return
    const selected = activeModel
    if (currentDraft.modelMode === 'override' && (!catalog || !selected || !catalogFresh || !catalog.credentialToken)) return
    if (currentDraft.effortMode === 'explicit' && (!selected || !selected.supportedReasoningEfforts?.includes(currentDraft.explicitEffort))) return
    if (unsupportedInheritedEffort) return
    const selection: APIModelSelection = currentDraft.modelMode === 'override' && selected && catalog ? {
      executor: 'api', backendId: '', profileId: catalog.backendId, modelId: selected.id, catalogRevision: catalog.profileRevision, source: catalog.source, destination: catalog.destination,
      checkedAt: catalog.checkedAt, credentialToken: catalog.credentialToken ?? '', reasoningEffort: '', confirmUnverifiedManual: false,
      confirmUnfiltered: catalog.source === 'openrouter_general_unfiltered' && !needsUnfilteredConsent,
      confirmJitLoad: false, maxOutputTokens: 4096,
    } : emptySelection
    const input: SaveAuthoringStageModelPreferenceInput = { pipelineId: pipeline.id, stage, expectedRevision: currentPreference.preferenceRevision, modelMode: currentDraft.modelMode, effortMode: currentDraft.effortMode, explicitEffort: currentDraft.effortMode === 'explicit' ? currentDraft.explicitEffort : '', selection }
    const ticket = preferenceEpoch.current
    setSaving(true); setSaveError('')
    try {
      const saved = await backend.saveAuthoringStageModelPreference(input)
      if (ticket !== preferenceEpoch.current || scope.current !== identity) return
      if (saved.pipelineId !== pipeline.id || saved.stage !== stage || saved.preferenceRevision < input.expectedRevision + 1) throw new Error('Readback da preferência não confirmou a revisão salva.')
      setPreference(saved)
      setDraft({ modelMode: saved.modelMode, effortMode: saved.effortMode, explicitEffort: saved.explicitEffort, profileId: saved.modelMode === 'override' ? saved.selection.backendId : '', modelId: saved.modelMode === 'override' ? saved.selection.modelId : '' })
      setCatalog(current => current ? { ...current, credentialToken: undefined } : current)
      setSaveError(''); setPreferenceError('')
    } catch (cause) {
      if (ticket !== preferenceEpoch.current || scope.current !== identity) return
      const conflict = errorCode(cause) === 'pipeline_conflict'
      setSaveError(conflict ? 'A preferência mudou em outra ação. A revisão atual foi recarregada; revise antes de salvar novamente.' : errorMessage(cause))
      if (conflict) {
        const nextTicket = ticket
        try {
          const latest = await backend.getAuthoringStageModelPreference({ pipelineId: pipeline.id, stage })
          if (nextTicket === preferenceEpoch.current && scope.current === identity) {
            setPreference(latest); setDraft({ modelMode: latest.modelMode, effortMode: latest.effortMode, explicitEffort: latest.explicitEffort, profileId: latest.modelMode === 'override' ? latest.selection.backendId : '', modelId: latest.modelMode === 'override' ? latest.selection.modelId : '' })
            invalidateCatalog()
          }
        } catch { /* The conflict remains visible; explicit refresh is available. */ }
      }
    } finally { if (ticket === preferenceEpoch.current && scope.current === identity) setSaving(false) }
  }

  // Reconfirming is the user's explicit choice of this exact provider and model, so a clean catalog read
  // saves it. Anything that still needs consent (unfiltered list, model load) stops at its checkbox.
  useEffect(() => {
    if (!saveAfterConsult) return
    if (catalogState === 'error') { setSaveAfterConsult(false); return }
    if (catalogState !== 'ready') return
    setSaveAfterConsult(false)
    if (canSave) void savePreference()
  }, [saveAfterConsult, catalogState, canSave])

  async function refreshPreference() {
    const ticket = ++preferenceEpoch.current
    setPreferenceLoading(true); setPreferenceError(''); setSaveError(''); invalidateCatalog()
    try {
      const value = await backend.getAuthoringStageModelPreference({ pipelineId: pipeline.id, stage })
      if (ticket !== preferenceEpoch.current || scope.current !== identity) return
      if (value.pipelineId !== pipeline.id || value.stage !== stage) throw new Error('Preferência fora do contexto da fase atual.')
      setPreference(value); setDraft({ modelMode: value.modelMode, effortMode: value.effortMode, explicitEffort: value.explicitEffort, profileId: value.modelMode === 'override' ? value.selection.backendId : '', modelId: value.modelMode === 'override' ? value.selection.modelId : '' })
    } catch (cause) { if (ticket === preferenceEpoch.current && scope.current === identity) setPreferenceError(errorMessage(cause)) }
    finally { if (ticket === preferenceEpoch.current && scope.current === identity) setPreferenceLoading(false) }
  }

  const source = currentPreference?.inheritedFrom || currentPreference?.modelSource || 'global_default'
  const sourceLabel = sourceName(source)
  const globalDefaultProfile = currentPreference?.modelSource === 'global_default' ? profiles.find(profile => profile.id === currentPreference.selection.backendId) : undefined
  const globalDefaultModel = globalDefaultProfile?.model || currentPreference?.selection.modelId || 'Modelo configurado'
  const controlsDisabled = disabled || preferenceLoading || profilesLoading || !currentPreference
  const defaultNeedsUnfilteredCatalog = selectedProfile?.providerType === 'openrouter' && currentPreference?.modelSource === 'global_default'
  const defaultNeedsLoadedState = selectedProfile?.providerType === 'lm_studio' && currentPreference?.modelSource === 'global_default'
  const previewModel = currentDraft?.modelMode === 'override' ? activeModel?.displayName || currentDraft.modelId || 'Modelo ainda não selecionado' : currentPreference?.selection.modelId || globalDefaultModel
  const previewEffort = currentDraft?.effortMode === 'explicit' ? currentDraft.explicitEffort : currentDraft?.effortMode === 'automatic' ? 'Automático' : currentPreference?.selection.reasoningEffort || 'Automático'
  const previewDestination = catalogFresh && catalog?.backendId === selectedProfileId ? catalog.destination : currentPreference?.resolution === 'ready' && currentPreference.selection.backendId === selectedProfileId ? currentPreference.selection.destination : ''
  const outputCap = Math.min(4096, currentPreference?.selection.maxOutputTokens || 4096)
  const modelOptions = catalogState === 'ready' && catalogFresh && draft?.modelMode === 'override'
    ? [{ value: '', label: 'Escolha um modelo' }, ...(catalog?.models.filter(model => model.backendId === catalog.backendId && model.source === catalog.source).map(model => ({ value: model.id, label: model.displayName || model.id })) ?? [])]
    : [{ value: '', label: 'Consulte o catálogo completo' }]
  const supportedEfforts = catalogFresh && activeModel ? [...new Set(activeModel.supportedReasoningEfforts ?? [])].filter(Boolean) : []
  const effortOptions = [
    { value: 'inherit', label: 'Herdar' }, { value: 'automatic', label: 'Automático' },
    ...(supportedEfforts.length ? supportedEfforts.map(value => ({ value: `explicit:${value}`, label: value })) : []),
    ...(draft?.effortMode === 'explicit' && !supportedEfforts.includes(draft.explicitEffort) ? [{ value: `explicit:${draft.explicitEffort}`, label: `${draft.explicitEffort} · salvo; consulte o catálogo`, disabled: true }] : []),
  ]
  const effortValue = draft?.effortMode === 'explicit' ? `explicit:${draft.explicitEffort}` : draft?.effortMode ?? 'inherit'
  const openRouterNeedsConsent = !!catalog && catalog.source === 'openrouter_general_unfiltered' && !!activeModel
  const jitNeedsConsent = !!catalog && catalog.source === 'lm_studio_native' && activeModel?.loaded === false

  return <section className="authoring-stage-model-preferences" aria-labelledby={`authoring-model-heading-${stage}`} aria-busy={preferenceLoading || profilesLoading || saving || catalogState === 'loading'}>
    <div className="authoring-model-heading"><h4 id={`authoring-model-heading-${stage}`}>Modelo e esforço desta fase</h4>{currentPreference && <span>Preferência v{currentPreference.preferenceRevision}</span>}</div>
    {preferenceLoading && !currentPreference && <p role="status">Lendo preferência salva…</p>}
    {preferenceError && <p role="alert">Não foi possível confirmar a preferência desta fase: {preferenceError}</p>}
    {currentPreference?.resolution === 'stale' && <div role="alert" className="authoring-model-help"><p>A seleção salva está desatualizada: o catálogo de modelos vale por 5 minutos. Consulte o catálogo e salve uma seleção válida antes de gerar.</p>
      {canReconfirm && <button type="button" onClick={reconfirmInherited}>Reconfirmar {currentPreference.selection.modelId} · {profiles.find(profile => profile.id === currentPreference.selection.backendId)?.name}</button>}</div>}
    {currentPreference?.resolution === 'unconfigured' && <div role="alert" className="authoring-model-help"><p>Esta fase não tem um modelo configurado. Escolha um modelo de API ou ajuste Configurações. O SDD aceita provedores OpenAI, OpenRouter, LM Studio e Ollama; “API compatível” não é aceita nesta etapa.</p>{onSettings && <button type="button" onClick={onSettings}>Abrir Configurações</button>}</div>}
    {currentPreference?.catalogValidationRequired && currentPreference.modelSource === 'global_default' && <p className="authoring-model-status" role="status">Modelo herdado de Configurações: {globalDefaultProfile?.name || 'Provedor padrão'} · {globalDefaultModel}. {defaultNeedsUnfilteredCatalog ? 'Consulte o catálogo completo antes de gerar; catálogos OpenRouter gerais exigem confirmação explícita.' : defaultNeedsLoadedState ? 'Consulte o catálogo atual antes de gerar para validar o estado carregado; qualquer carregamento JIT exige autorização desta tentativa.' : 'A validação do catálogo acontecerá no servidor ao gerar; níveis específicos só aparecem após consulta explícita.'}</p>}
    {saveError && <p role="alert">{saveError}</p>}
    <div className="authoring-model-grid">
      <IonPicker id={`authoring-stage-model-${stage}`} label="Modelo" value={draft?.modelMode ?? 'inherit'} onChange={value => {
        if (value !== 'inherit' && value !== 'override') return
        invalidateCatalog()
        updateDraft({ modelMode: value, profileId: value === 'override' ? '' : '', modelId: '' })
      }} options={[{ value: 'inherit', label: `Herdar · ${sourceLabel}` }, { value: 'override', label: 'Escolher para esta fase' }]} disabled={controlsDisabled} />
      <IonPicker id={`authoring-stage-effort-${stage}`} label="Esforço" value={effortValue} onChange={value => {
        if (value === 'inherit' || value === 'automatic') { updateDraft({ effortMode: value, explicitEffort: '' }); return }
        if (value.startsWith('explicit:')) updateDraft({ effortMode: 'explicit', explicitEffort: value.slice('explicit:'.length) })
      }} options={effortOptions} disabled={controlsDisabled} />
      {draft?.modelMode === 'inherit' && <p className="authoring-model-origin">Modelo herdado de <span>{sourceLabel}</span>{currentPreference?.selection.status === 'unverified_default' && !currentPreference.catalogValidationRequired ? ' · modelo padrão sem snapshot de catálogo' : ''}</p>}
      {draft?.effortMode === 'inherit' && currentPreference && <p className="authoring-model-origin">Esforço herdado: <span>{currentPreference.selection.reasoningEffort || 'Automático'} · Herdado de {sourceName(currentPreference.effortSource || source)}</span></p>}
      {draft?.modelMode === 'override' && <div className="authoring-provider-model-grid">
        <IonPicker id={`authoring-stage-provider-${stage}`} label="Provedor de API" value={draft.profileId} onChange={profileId => { updateDraft({ profileId, modelId: '' }); invalidateCatalog() }} options={[{ value: '', label: profilesLoading ? 'Lendo provedores…' : 'Escolha um provedor' }, ...profiles.map(profile => ({ value: profile.id, label: profile.name, disabled: profile.endpointBlocked }))]} disabled={controlsDisabled} searchable />
        <IonPicker id={`authoring-stage-override-model-${stage}`} label="Modelo da fase" value={draft.modelId} onChange={modelId => { updateDraft({ modelId }); setConfirmUnfiltered(''); setConfirmJit('') }} options={modelOptions} disabled={controlsDisabled || catalogState !== 'ready' || !catalogFresh} searchable />
      </div>}
    </div>
    {draft?.modelMode === 'override' && currentPreference?.modelMode === 'override' && <p className="authoring-model-origin">Modelo salvo nesta fase{currentPreference.effortMode === 'explicit' ? ` · esforço salvo: ${currentPreference.explicitEffort}` : ''}. Consulte o catálogo para confirmar a disponibilidade atual.</p>}
    {profilesError && draft?.modelMode === 'override' && <p role="alert">Não foi possível carregar os provedores de API: {profilesError}</p>}
    {!profilesLoading && !profilesError && profiles.length === 0 && draft?.modelMode === 'override' && <div className="authoring-model-help" role="status"><p className="authoring-model-status">Nenhum provedor elegível. Configure OpenAI, OpenRouter, LM Studio ou Ollama; “API compatível” não é aceita nesta etapa.</p>{onSettings && <button type="button" onClick={onSettings}>Abrir Configurações</button>}</div>}
    {catalogMessage && <p className={catalogState === 'error' ? 'authoring-model-error' : 'authoring-model-status'} role={catalogState === 'error' ? 'alert' : 'status'}>{catalogMessage}</p>}
    {catalogState === 'loading' && <p role="status">Consultando modelos…</p>}
    <div className="authoring-model-actions">
      <button type="button" onClick={() => void consultModels()} disabled={controlsDisabled || catalogState === 'loading' || (draft?.modelMode === 'override' && !draft.profileId)}>Consultar modelos</button>
      {showSaveButton && <button type="button" className="primary" onClick={() => void savePreference()} disabled={!canSave}>{saving ? 'Salvando preferência…' : 'Salvar preferência'}</button>}
      {(preferenceError || saveError) && <button type="button" onClick={() => void refreshPreference()} disabled={preferenceLoading || saving}>Atualizar preferência</button>}
    </div>
    {unsupportedInheritedEffort && <p className="authoring-model-error" role="alert">O modelo escolhido não oferece o esforço herdado “{inheritedEffort}”. Escolha Automático ou um nível compatível; nenhum fallback foi aplicado.</p>}
    {openRouterNeedsConsent && <label className="authoring-model-confirm"><input type="checkbox" checked={confirmUnfiltered === activeChoiceKey || (inheritedConsentMatches && !!preference?.selection.confirmUnfiltered)} onChange={event => setConfirmUnfiltered(event.target.checked ? activeChoiceKey : '')} />Confirmo que este catálogo geral não está filtrado pela minha conta e que o modelo pode gerar custos.</label>}
    {jitNeedsConsent && <label className="authoring-model-confirm"><input type="checkbox" checked={confirmJit === activeChoiceKey} onChange={event => setConfirmJit(event.target.checked ? activeChoiceKey : '')} />Autorizo carregar este modelo no LM Studio para esta tentativa.</label>}
    {currentPreference && <dl className="authoring-model-preview" aria-label="Prévia de destino e permissões desta fase">
      <div><dt>{previewDestination ? 'Destino seguro da API' : 'Destino não validado'}</dt><dd>{previewDestination || 'Atualize a preferência ou consulte o catálogo antes de gerar.'}</dd></div>
      <div><dt>Modelo e esforço</dt><dd>{previewModel} · {previewEffort}</dd></div>
      <div><dt>Limite de saída</dt><dd>{outputCap} tokens</dd></div>
      <div className="authoring-model-permissions"><dt>Permissões desta fase</dt><dd>A fase acessa somente a API selecionada; não acessa arquivos do workspace, shell, rede geral ou MCP.</dd></div>
    </dl>}
  </section>
}
