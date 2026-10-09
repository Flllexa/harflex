import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { IonPicker } from '../../components/IonPicker'
import { errorCode, errorMessage, type Backend, type Brainstorm, type BrainstormRef, type ModelCatalogResult, type Pipeline, type ProviderProfile, type SDDModelSelection, type Settings } from '../../lib/backend'
import { localeTag, useT } from '../../i18n'

type Props = { backend: Backend; pipeline: Pipeline; onPipelineChange: (pipeline: Pipeline) => void; derivationSafety?: 'checking' | 'unknown' | 'pending' | 'clear'; onRefreshDerivationSafety?: () => void; onSettings?: () => void }
type SDDExecutor = 'api' | 'codex_cli'
type Intent = { key: string; requestId: string; input?: SDDModelSelection }
class CatalogChoiceError extends Error {}
const requestId = () => crypto.randomUUID()
const codexExecutorValue = 'executor:codex_cli'
const apiExecutorValue = (profileId: string) => `api:${profileId}`
const executorForSelection = (selection: Brainstorm['selection']): SDDExecutor => selection.executor === 'codex_cli' || (selection.executor === undefined && selection.backendId === 'codex' && selection.source === 'codex_app_server') ? 'codex_cli' : 'api'
const selectionOrigin = (selection: Brainstorm['selection'], local: string) => executorForSelection(selection) === 'codex_cli' ? local : selection.destination
const catalogChoiceKey = (catalog: ModelCatalogResult, modelId: string) => JSON.stringify([
  catalog.backendId, modelId, catalog.source, catalog.destination, catalog.profileRevision,
  catalog.models.find(item => item.id === modelId)?.loaded ?? null,
])
const isActive = (run: Brainstorm) => run.state === 'running_question' || run.state === 'running_synthesis'
function readableStates(t: (source: string) => string): Record<Brainstorm['state'], string> {
  return {
  ready: t('Pronto para a próxima pergunta'), running_question: t('Gerando pergunta'), waiting_answer: t('Aguardando resposta'), ready_for_synthesis: t('Perguntas concluídas'),
  running_synthesis: t('Gerando síntese'), waiting_user: t('Síntese pronta para revisão'), paused: t('Pausado'), invalidated: t('Discovery alterada'),
  skipped_waiting_confirmation: t('Aguardando confirmação do pulo'), approved: t('Discovery aprovada'),
  }
}

export function AuthoringBrainstorm({ backend, pipeline, onPipelineChange, derivationSafety = 'unknown', onRefreshDerivationSafety, onSettings }: Props) {
  const t = useT()
  const readableState = readableStates(t)
  const discovery = pipeline.artifacts.discovery
  const scope = `${pipeline.workspaceId}:${pipeline.id}:${discovery?.version ?? 0}`
  const [run, setRun] = useState<Brainstorm | null>(null)
  const [history, setHistory] = useState<Brainstorm[]>([])
  const [inspectedRunId, setInspectedRunId] = useState('')
  const [historyError, setHistoryError] = useState('')
  const [lookup, setLookup] = useState<'loading' | 'empty' | 'ready' | 'error'>('loading')
  const [profiles, setProfiles] = useState<ProviderProfile[]>([])
  const [profilesState, setProfilesState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [settingsState, setSettingsState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [executor, setExecutor] = useState<SDDExecutor>('api')
  const [profileId, setProfileId] = useState('')
  const [modelId, setModelId] = useState('')
  const [catalog, setCatalog] = useState<ModelCatalogResult>()
  const [catalogState, setCatalogState] = useState<'idle' | 'loading' | 'ready' | 'error'>('idle')
  const [confirmUnfiltered, setConfirmUnfiltered] = useState('')
  const [unfilteredDeclined, setUnfilteredDeclined] = useState(false)
  const [confirmJitLoad, setConfirmJitLoad] = useState('')
  const [answer, setAnswer] = useState('')
  const [feedback, setFeedback] = useState('')
  const [revisionChoice, setRevisionChoice] = useState<'more_questions' | 'new_synthesis'>('more_questions')
  const [skipReason, setSkipReason] = useState('')
  const [skipOpen, setSkipOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [editingDiscovery, setEditingDiscovery] = useState(false)
  const [discoveryDraft, setDiscoveryDraft] = useState(discovery?.content ?? '')
  const [discoveryConflict, setDiscoveryConflict] = useState(false)
  const [deriveOpen, setDeriveOpen] = useState(false)
  const [confirmDerivationRisk, setConfirmDerivationRisk] = useState(false)
  useLayoutEffect(() => { setConfirmDerivationRisk(false) }, [scope, pipeline.revision, derivationSafety, deriveOpen])
  const derivationAllowed = derivationSafety === 'clear' || (derivationSafety === 'pending' && confirmDerivationRisk)
  const [derivedDiscovery, setDerivedDiscovery] = useState(discovery?.content ?? '')
  const [cancelPending, setCancelPending] = useState(false)
  const [pendingAction, setPendingAction] = useState('')
  const conversation = useRef<HTMLElement>(null)
  const lastState = useRef<Brainstorm['state']>()
  const generation = useRef(0)
  const loadGeneration = useRef(0)
  const pollEpoch = useRef(0)
  const scopeRef = useRef(scope)
  const catalogAbort = useRef<AbortController>()
  const manualCatalogAbort = useRef<AbortController>()
  const intent = useRef<Intent>()
  const deriveIntent = useRef<Intent>()
  const cancelIntent = useRef<Intent>()
  const modelQuery = useRef(0)
  const executorTouched = useRef(false)
  const currentRun = useRef<Brainstorm | null>(null)
  const currentProfiles = useRef<ProviderProfile[]>([])
  const currentSettings = useRef<Settings>({ defaultBackendId: '', defaultModelBackendId: '', defaultModelId: '' })
  const settingsLoaded = useRef(false)
  const profilesLoaded = useRef(false)
  const settingsReadFailed = useRef(false)
  const profilesReadFailed = useRef(false)
  const manualRunQuery = useRef(0)
  const artifactIdentity = useRef(`${pipeline.id}:${pipeline.revision}:${discovery?.version ?? 0}`)
  const artifactScope = useRef(`${pipeline.workspaceId}:${pipeline.id}`)
  const currentScope = (token: number, identity: string) => generation.current === token && scopeRef.current === identity
  const currentLoad = (token: number, identity: string) => loadGeneration.current === token && scopeRef.current === identity
  function chooseInitialExecutor() {
    if (executorTouched.current) return
    const inherited = currentRun.current?.selection
    if (inherited) {
      const inheritedExecutor = executorForSelection(inherited)
      if (inheritedExecutor === 'codex_cli') {
        setExecutor('api')
        setProfileId('')
        setModelId('')
        return
      }
      setExecutor(inheritedExecutor)
      setProfileId(inheritedExecutor === 'api' ? inherited.backendId : '')
      setModelId(inherited.modelId)
      return
    }
    if (!profilesLoaded.current || !settingsLoaded.current) return
    const settings = currentSettings.current
    if (settingsReadFailed.current) {
      setExecutor('api')
      setProfileId('')
      setModelId('')
      return
    }
    if (settings.defaultBackendId === 'codex') {
      setExecutor('api')
      setProfileId('')
      setModelId('')
      return
    }
    const configuredProfile = currentProfiles.current.find(item => item.id === settings.defaultBackendId)
    if (configuredProfile) {
      setExecutor('api')
      setProfileId(configuredProfile.id)
      setModelId(settings.defaultModelBackendId === configuredProfile.id ? settings.defaultModelId : configuredProfile.model)
      return
    }
    if (settings.defaultBackendId && profilesReadFailed.current) {
      setExecutor('api')
      setProfileId(settings.defaultBackendId)
      setModelId(settings.defaultModelBackendId === settings.defaultBackendId ? settings.defaultModelId : '')
      return
    }
    if (!settings.defaultBackendId && currentProfiles.current.length === 0 && !profilesReadFailed.current) {
      setExecutor('api')
      setProfileId('')
      setModelId('')
      return
    }
    setExecutor('api')
    setProfileId('')
    setModelId('')
  }

  useLayoutEffect(() => {
    scopeRef.current = scope
    generation.current++
    loadGeneration.current++
    pollEpoch.current++
    modelQuery.current++
    catalogAbort.current?.abort()
    manualCatalogAbort.current?.abort()
    return () => {
      generation.current++
      loadGeneration.current++
      pollEpoch.current++
      modelQuery.current++
      catalogAbort.current?.abort()
      manualCatalogAbort.current?.abort()
    }
  }, [scope])

  useEffect(() => {
    const artifactOwner = `${pipeline.workspaceId}:${pipeline.id}`
    if (artifactScope.current !== artifactOwner) {
      setDiscoveryDraft(discovery?.content ?? '')
      setEditingDiscovery(false)
      setDiscoveryConflict(false)
      setDerivedDiscovery(discovery?.content ?? '')
      setDeriveOpen(false)
      artifactScope.current = artifactOwner
      artifactIdentity.current = `${pipeline.id}:${pipeline.revision}:${discovery?.version ?? 0}`
      return
    }
    const next = `${pipeline.id}:${pipeline.revision}:${discovery?.version ?? 0}`
    if (artifactIdentity.current !== next) {
      if (editingDiscovery && discoveryDraft !== discovery?.content) setDiscoveryConflict(true)
      else { setDiscoveryDraft(discovery?.content ?? ''); setEditingDiscovery(false); setDiscoveryConflict(false) }
      setDerivedDiscovery(discovery?.content ?? '')
      artifactIdentity.current = next
    }
  }, [pipeline.workspaceId, pipeline.id, pipeline.revision, discovery?.version, discovery?.content, editingDiscovery, discoveryDraft])

  useEffect(() => {
    const current = ++loadGeneration.current
    const identity = scope
    executorTouched.current = false
    currentRun.current = null
    currentProfiles.current = []
    currentSettings.current = { defaultBackendId: '', defaultModelBackendId: '', defaultModelId: '' }
    settingsLoaded.current = false
    profilesLoaded.current = false
    settingsReadFailed.current = false
    profilesReadFailed.current = false
    intent.current = undefined
    deriveIntent.current = undefined; cancelIntent.current = undefined
    setRun(null); setHistory([]); setHistoryError(''); setLookup('loading'); setError(''); setNotice(''); setBusy(false); setCancelPending(false); setPendingAction(''); setExecutor('api'); setProfileId(''); setModelId(''); setCatalog(undefined); setCatalogState('idle'); setProfiles([]); setProfilesState('loading'); setSettingsState('loading'); setInspectedRunId('')
    setAnswer(''); setFeedback(''); setRevisionChoice('more_questions'); setSkipReason(''); setSkipOpen(false); setConfirmUnfiltered(''); setUnfilteredDeclined(false); setConfirmJitLoad('')
    if (!discovery) { setLookup('error'); setError(t('Discovery não encontrada. Atualize o pipeline.')); return }
    void backend.getBrainstorming({ runId: '', pipelineId: pipeline.id, discoveryVersion: discovery.version }).then(found => {
      if (!currentLoad(current, identity)) return
      currentRun.current = found
      setRun(found); setLookup('ready'); chooseInitialExecutor()
    }).catch(failure => {
      if (!currentLoad(current, identity)) return
      if (errorCode(failure) === 'brainstorm_not_found') { currentRun.current = null; setLookup('empty'); chooseInitialExecutor() }
      else { setLookup('error'); setError(errorMessage(failure)) }
    })
    void backend.listProviderProfiles().then(found => {
      if (currentLoad(current, identity)) {
        const available = found.filter(profile => !profile.endpointBlocked && profile.providerType !== 'generic' && (profile.hasCredential || profile.providerType === 'lm_studio' || profile.providerType === 'ollama'))
        currentProfiles.current = available
        profilesLoaded.current = true
        profilesReadFailed.current = false
        setProfiles(available)
        setProfilesState('ready')
        chooseInitialExecutor()
      }
    }).catch(failure => {
      if (currentLoad(current, identity)) {
        currentProfiles.current = []
        profilesLoaded.current = true
        profilesReadFailed.current = true
        setProfilesState('error')
        chooseInitialExecutor()
      }
    })
    void backend.getSettings().then(found => {
      if (currentLoad(current, identity)) {
        currentSettings.current = found
        settingsLoaded.current = true
        settingsReadFailed.current = false
        setSettingsState('ready')
        chooseInitialExecutor()
      }
    }).catch(() => {
      if (currentLoad(current, identity)) {
        settingsLoaded.current = true
        settingsReadFailed.current = true
        setSettingsState('error')
        chooseInitialExecutor()
      }
    })
    void backend.listBrainstorming({ pipelineId: pipeline.id, workspaceId: pipeline.workspaceId, limit: 20 }).then(found => {
      if (currentLoad(current, identity)) { setHistory(found); setHistoryError('') }
    }).catch(failure => { if (currentLoad(current, identity)) setHistoryError(errorMessage(failure)) })
    return () => { loadGeneration.current++; generation.current++ }
  }, [backend, scope])

  useEffect(() => {
    const selectedRun = run?.selection && executorForSelection(run.selection) === executor ? run.selection : undefined
    const selectedProfileId = profileId || (executor === 'api' ? selectedRun?.backendId ?? '' : '')
    const selectedBackendId = executor === 'codex_cli' ? 'codex' : selectedProfileId
    if (!selectedBackendId) { setCatalog(undefined); setCatalogState('idle'); return }
    const current = ++modelQuery.current
    const identity = scope
    const controller = new AbortController()
    setCatalogState('loading'); setCatalog(undefined)
    const query = executor === 'codex_cli'
      ? backend.queryCLIModelCatalog({ workspaceId: pipeline.workspaceId, backendId: 'codex' }, controller.signal)
      : backend.queryHTTPModelCatalog({ profileId: selectedBackendId, searchTerm: '', refresh: true }, controller.signal)
    void query.then(found => {
      if (scopeRef.current !== identity || modelQuery.current !== current) return
      setCatalog(found); setCatalogState('ready')
    }).catch(failure => {
      if (scopeRef.current !== identity || modelQuery.current !== current || (failure instanceof DOMException && failure.name === 'AbortError')) return
      setCatalogState('error'); setError(errorMessage(failure))
    })
    return () => { modelQuery.current++; controller.abort() }
  }, [backend, scope, pipeline.workspaceId, executor, profileId, run?.selection.backendId, run?.selection.source])

  const selected = run?.selection
  const inherited = selected && executorForSelection(selected) === executor ? selected : undefined
  const chosenProfile = executor === 'api' ? profileId || inherited?.backendId || '' : ''
  const chosenBackendId = executor === 'codex_cli' ? 'codex' : chosenProfile
  const chosenModel = modelId || (chosenBackendId === inherited?.backendId ? inherited.modelId : '')
  const selectedProfile = profiles.find(item => item.id === chosenProfile)
  const selectedModel = catalog?.models.find(item => item.id === chosenModel)
  const selectedProfileConfirmed = !!selectedProfile || profilesState === 'error' && chosenBackendId === currentSettings.current.defaultBackendId
  const choiceKey = catalog ? catalogChoiceKey(catalog, chosenModel) : ''
  const confirmedUnfiltered = !!choiceKey && confirmUnfiltered === choiceKey
  const confirmedJitLoad = !!choiceKey && confirmJitLoad === choiceKey
  const sameSelection = !!inherited && inherited.backendId === chosenBackendId && inherited.modelId === chosenModel &&
    inherited.source === catalog?.source && inherited.destination === (executor === 'codex_cli' ? '' : catalog?.destination) && inherited.catalogRevision === catalog?.profileRevision
  const inheritedUnfiltered = sameSelection && !!run?.selection.confirmUnfiltered && !unfilteredDeclined
  // Loaded state is not part of the durable selection snapshot; a new unloaded
  // attempt always needs an explicit JIT confirmation.
  const inheritedJitLoad = false
  const needsUnfiltered = executor === 'api' && catalog?.source === 'openrouter_general_unfiltered'
  const needsLoad = executor === 'api' && (selectedProfile?.providerType === 'lm_studio' || catalog?.source.startsWith('lm_studio')) && selectedModel?.loaded === false
  const catalogAge = catalog ? Date.now() - new Date(catalog.checkedAt).getTime() : Infinity
  const catalogFresh = catalogAge >= -60_000 && catalogAge <= 300_000
  const catalogIdentityValid = catalogState === 'ready' && !!catalog && catalogFresh && catalog.status === 'complete' && catalog.complete && !!catalog.profileRevision &&
    catalog.backendId === chosenBackendId && (executor === 'codex_cli' ? catalog.source === 'codex_app_server' : selectedProfileConfirmed && !!catalog.credentialToken)
  const catalogOffersModels = catalogIdentityValid && !!catalog?.models.some(item => item.backendId === chosenBackendId && item.source === catalog.source && item.availability !== 'unavailable')
  const catalogUsable = catalogOffersModels && !!selectedModel && selectedModel.backendId === catalog?.backendId && selectedModel.source === catalog?.source &&
    (!needsUnfiltered || confirmedUnfiltered || inheritedUnfiltered) && (!needsLoad || confirmedJitLoad || inheritedJitLoad)

  function selectionFrom(fresh: ModelCatalogResult, chosenId: string, selectedExecutor: SDDExecutor, expectedBackendId: string): SDDModelSelection {
    const model = fresh.models.find(item => item.id === chosenId)
    const age = Date.now() - new Date(fresh.checkedAt).getTime()
    const freshChoiceKey = catalogChoiceKey(fresh, chosenId)
    if (!model || fresh.backendId !== expectedBackendId || model.backendId !== fresh.backendId || model.source !== fresh.source || model.availability === 'unavailable' ||
      fresh.status !== 'complete' || !fresh.complete || !fresh.profileRevision || age < -60_000 || age > 300_000) {
      throw new CatalogChoiceError(t('O catálogo ou o modelo mudou. Atualize e escolha novamente.'))
    }
    if (selectedExecutor === 'codex_cli') {
      if (fresh.backendId !== 'codex' || fresh.source !== 'codex_app_server') throw new CatalogChoiceError(t('O catálogo Codex CLI mudou. Atualize e escolha novamente.'))
      return { executor: 'codex_cli', backendId: 'codex', profileId: '', modelId: chosenId, catalogRevision: fresh.profileRevision, source: 'codex_app_server', destination: '',
        checkedAt: fresh.checkedAt, credentialToken: '', reasoningEffort: '', confirmUnverifiedManual: false, confirmUnfiltered: false, confirmJitLoad: false, maxOutputTokens: 256 }
    }
    const confirmedFreshUnfiltered = confirmUnfiltered === freshChoiceKey
    const confirmedFreshJit = confirmJitLoad === freshChoiceKey
    const inheritedFreshUnfiltered = !unfilteredDeclined && !!inherited?.confirmUnfiltered && inherited.backendId === fresh.backendId &&
      inherited.modelId === chosenId && inherited.source === fresh.source && inherited.destination === fresh.destination && inherited.catalogRevision === fresh.profileRevision
    if (!fresh.credentialToken ||
      (selectedProfile?.providerType === 'openrouter' && fresh.source === 'openrouter_general_unfiltered' && !confirmedFreshUnfiltered && !inheritedFreshUnfiltered) ||
      (selectedProfile?.providerType === 'lm_studio' && model.loaded === false && !confirmedFreshJit)) {
      throw new CatalogChoiceError(t('O catálogo ou o contexto do modelo mudou, ou falta uma confirmação. Atualize e escolha novamente.'))
    }
    return { executor: 'api', backendId: '', profileId: fresh.backendId, modelId: chosenId, catalogRevision: fresh.profileRevision, source: fresh.source, destination: fresh.destination,
      checkedAt: fresh.checkedAt, credentialToken: fresh.credentialToken, reasoningEffort: '', confirmUnverifiedManual: false,
      confirmUnfiltered: confirmedFreshUnfiltered || inheritedFreshUnfiltered, confirmJitLoad: confirmedFreshJit, maxOutputTokens: 256 }
  }

  async function currentSelection(selectedExecutor: SDDExecutor, expectedBackendId: string, chosenModel: string, token: number, identity: string) {
    const controller = new AbortController()
    catalogAbort.current = controller
    let fresh: ModelCatalogResult
    try {
      fresh = selectedExecutor === 'codex_cli'
        ? await backend.queryCLIModelCatalog({ workspaceId: pipeline.workspaceId, backendId: 'codex' }, controller.signal)
        : await backend.queryHTTPModelCatalog({ profileId: expectedBackendId, searchTerm: '', refresh: true }, controller.signal)
    }
    finally { if (catalogAbort.current === controller) catalogAbort.current = undefined }
    if (!currentScope(token, identity)) throw new DOMException(t('Seleção obsoleta.'), 'AbortError')
    setCatalog(fresh); setCatalogState('ready')
    return selectionFrom(fresh, chosenModel, selectedExecutor, expectedBackendId)
  }

  function refFor(current: Brainstorm, id: string): BrainstormRef {
    return { runId: current.id, requestId: id, pipelineRevision: current.pipelineRevision, runRevision: current.revision, discoveryVersion: current.discoveryVersion }
  }

  async function saveDiscovery() {
    if (!discovery || busy || discoveryConflict || !discoveryDraft.trim()) return
    const current = ++generation.current
    const identity = scopeRef.current
    setBusy(true); setError(''); setNotice('')
    try {
      const saved = await backend.reviseAuthoringDiscovery({ pipelineId: pipeline.id, expectedRevision: run?.pipelineRevision ?? pipeline.revision, expectedVersion: discovery.version, discovery: discoveryDraft.trim() })
      if (!currentScope(current, identity)) return
      setEditingDiscovery(false); setDiscoveryConflict(false); setRun(null); onPipelineChange(saved)
      setNotice(t('Nova versão da Discovery salva. A rodada anterior permanece no histórico.'))
    } catch (failure) { if (currentScope(current, identity)) setError(errorMessage(failure)) }
    finally { if (currentScope(current, identity)) setBusy(false) }
  }

  async function derivePipeline() {
    if (busy || !derivedDiscovery.trim() || !derivationAllowed) return
    const current = ++generation.current
    const identity = scopeRef.current
    const key = `${pipeline.id}:${pipeline.revision}:${derivedDiscovery.trim()}`
    const active = deriveIntent.current?.key === key ? deriveIntent.current : { key, requestId: requestId() }
    deriveIntent.current = active
    setBusy(true); setError('')
    try {
      const child = await backend.deriveAuthoringPipeline({ parentPipelineId: pipeline.id, expectedRevision: pipeline.revision, requestId: active.requestId, discovery: derivedDiscovery.trim() })
      if (!currentScope(current, identity)) return
      deriveIntent.current = undefined; setDeriveOpen(false); onPipelineChange(child)
    } catch (failure) { if (currentScope(current, identity)) setError(errorMessage(failure)) }
    finally { if (currentScope(current, identity)) setBusy(false) }
  }

  async function apply(key: string, callback: (id: string, selected?: SDDModelSelection) => Promise<Brainstorm>, model?: { executor: SDDExecutor; backendId: string; id: string }) {
    if (busy) return
    const current = ++generation.current
    const identity = scopeRef.current
    setBusy(true); setError(''); setNotice('')
    let poller: ReturnType<typeof setInterval> | undefined
    let pollToken: number | undefined
    try {
      let active = intent.current?.key === key ? intent.current : undefined
      if (!active) {
        const selected = model ? await currentSelection(model.executor, model.backendId, model.id, current, identity) : undefined
        if (!currentScope(current, identity)) return
        active = { key, requestId: requestId(), input: selected }
        intent.current = active
      }
      if (!currentScope(current, identity)) return
      const generationAction = /^(question|sufficient|synthesis):/.test(key)
      const operation = callback(active.requestId, active.input)
      if (generationAction && run) {
        pollToken = ++pollEpoch.current
        setPendingAction(key)
        let reads = 0
        let reading = false
        poller = setInterval(() => {
          if (!currentScope(current, identity) || pollEpoch.current !== pollToken || ++reads > 225) { if (poller) clearInterval(poller); return }
          if (reading) return
          reading = true
          void backend.getBrainstorming({ runId: run.id, pipelineId: '', discoveryVersion: 0 }).then(latest => {
            if (currentScope(current, identity) && pollEpoch.current === pollToken && latest.id === run.id) {
              setRun(previous => previous && (previous.id !== latest.id || previous.revision > latest.revision) ? previous : latest)
            }
          }).catch(() => { /* the command receipt remains authoritative; a manual read is available */ }).finally(() => { reading = false })
        }, 400)
      }
      const receipt = await operation
      if (!currentScope(current, identity)) return
      pollEpoch.current++
      setRun(receipt); setLookup('ready'); intent.current = undefined; setPendingAction('')
      setNotice(t('Ação confirmada no histórico do brainstorming.'))
      void backend.getPipeline(pipeline.id).then(latest => { if (currentScope(current, identity)) onPipelineChange(latest) }).catch(failure => { if (currentScope(current, identity)) setError(t('Ação registrada; atualize o pipeline: {error}', { error: errorMessage(failure) })) })
    } catch (failure) {
      if (currentScope(current, identity)) {
        pollEpoch.current++
        setPendingAction('')
        setError(failure instanceof CatalogChoiceError ? failure.message : errorMessage(failure))
        if (/^(question|sufficient|synthesis):/.test(key) && run) {
          try {
            const latest = await backend.getBrainstorming({ runId: run.id, pipelineId: '', discoveryVersion: 0 })
            if (!currentScope(current, identity)) return
            if (latest.id === run.id && latest.revision >= run.revision) {
              setRun(latest)
              setLookup('ready')
              if (!isActive(latest)) {
                intent.current = undefined
                const latestAttempt = latest.attempts[latest.attempts.length - 1]
                if (latest.state === 'paused' && latestAttempt?.status === 'failed' && latestAttempt.errorCode === 'budget_overrun') setError('')
              }
            }
          } catch { /* keep the specific failure visible; manual readback remains available */ }
        }
      }
    }
    finally { if (poller) clearInterval(poller); if (pollToken !== undefined && pollEpoch.current === pollToken) pollEpoch.current++; if (currentScope(current, identity)) setBusy(false) }
  }

  async function refreshRun() {
    const current = generation.current
    const identity = scopeRef.current
    const query = ++manualRunQuery.current
    setLookup('loading'); setError('')
    try {
      const found = await backend.getBrainstorming({ runId: run?.id ?? '', pipelineId: run ? '' : pipeline.id, discoveryVersion: run ? 0 : discovery?.version ?? 0 })
      if (!currentScope(current, identity) || manualRunQuery.current !== query) return
      setRun(found); setLookup('ready')
    } catch (failure) {
      if (!currentScope(current, identity) || manualRunQuery.current !== query) return
      if (errorCode(failure) === 'brainstorm_not_found') { setRun(null); setLookup('empty') }
      else { setLookup('error'); setError(errorMessage(failure)) }
    }
  }

  async function refreshCatalog() {
    if (!chosenBackendId) return
    const identity = scopeRef.current
    const query = ++modelQuery.current
    manualCatalogAbort.current?.abort()
    const controller = new AbortController()
    manualCatalogAbort.current = controller
    setCatalogState('loading'); setError('')
    try {
      const found = executor === 'codex_cli'
        ? await backend.queryCLIModelCatalog({ workspaceId: pipeline.workspaceId, backendId: 'codex' }, controller.signal)
        : await backend.queryHTTPModelCatalog({ profileId: chosenBackendId, searchTerm: '', refresh: true }, controller.signal)
      if (scopeRef.current !== identity || modelQuery.current !== query) return
      setCatalog(found); setCatalogState('ready')
    } catch (failure) {
      if (scopeRef.current !== identity || modelQuery.current !== query || (failure instanceof DOMException && failure.name === 'AbortError')) return
      setCatalogState('error'); setError(errorMessage(failure))
    } finally { if (manualCatalogAbort.current === controller) manualCatalogAbort.current = undefined }
  }

  async function cancelAttempt(currentRun: Brainstorm, attemptId: string) {
    if (cancelPending) return
    const current = generation.current
    const identity = scopeRef.current
    const key = `${currentRun.id}:${currentRun.revision}:${attemptId}`
    const active = cancelIntent.current?.key === key ? cancelIntent.current : { key, requestId: requestId() }
    cancelIntent.current = active
    setCancelPending(true); setError('')
    try {
      const receipt = await backend.cancelBrainstormAttempt({ ref: refFor(currentRun, active.requestId), attemptId })
      if (!currentScope(current, identity)) return
      generation.current++
      pollEpoch.current++
      cancelIntent.current = undefined; intent.current = undefined; setRun(receipt); setPendingAction(''); setBusy(false)
      setNotice(t('Geração cancelada. A tentativa ficou registrada.'))
    } catch (failure) {
      if (!currentScope(current, identity)) return
      setError(errorMessage(failure))
      try {
        const latest = await backend.getBrainstorming({ runId: currentRun.id, pipelineId: '', discoveryVersion: 0 })
        if (!currentScope(current, identity)) return
        setRun(latest)
        if (!isActive(latest) || !latest.attempts.some(item => item.id === attemptId && item.status === 'running')) {
          generation.current++
          pollEpoch.current++
          setBusy(false); setPendingAction(''); intent.current = undefined; cancelIntent.current = undefined
        }
      } catch { /* the original command remains active and will publish its receipt */ }
    } finally { if (scopeRef.current === identity) setCancelPending(false) }
  }

  // Each step ends below the fold of a long page; bring the next thing to do into view.
  useEffect(() => {
    const previous = lastState.current
    lastState.current = run?.state
    if (!run || !previous || previous === run.state) return
    const reduced = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
    conversation.current?.scrollIntoView?.({ block: 'nearest', behavior: reduced ? 'auto' : 'smooth' })
  }, [run?.state])

  const latestQuestion = run?.turns.find(turn => turn.questionId === run.currentQuestionId)
  const answeredCount = run?.turns.filter(turn => turn.status === 'answered').length ?? 0
  const latestSynthesis = run?.syntheses.find(item => item.version === run.synthesisVersion)
  const activeAttempt = [...(run?.attempts ?? [])].reverse().find(item => item.status === 'running')
  const latestAttempt = run?.attempts[run.attempts.length - 1]
  const pausedBudgetFailure = run?.state === 'paused' && latestAttempt?.status === 'failed' && latestAttempt.errorCode === 'budget_overrun'
  const shouldShowSelection = lookup === 'empty' || !!run && !['approved', 'invalidated'].includes(run.state)
  const historicalRuns = history.filter(item => item.id !== run?.id)
  const inspectedRun = history.find(item => item.id === inspectedRunId)
  const configuredDefaultBackend = currentSettings.current.defaultBackendId
  const configuredDefaultStatus = !configuredDefaultBackend ? 'none' : configuredDefaultBackend === 'codex' ? 'cli_unavailable_for_sdd' : profiles.some(item => item.id === configuredDefaultBackend)
    ? 'supported' : profilesState === 'error' ? 'unknown' : 'unsupported'
  const profilesKnownEmpty = profilesState === 'ready' && profiles.length === 0
  const canChooseExecutor = profilesState === 'error' || profilesState === 'ready' && profiles.length > 0
  const canStartBrainstorm = profilesState === 'error' || profilesState === 'ready' && profiles.length > 0
  const executorValue = executor === 'codex_cli' ? codexExecutorValue : chosenProfile ? apiExecutorValue(chosenProfile) : ''
  const executorOptions = [{ value: '', label: t('Escolha um executor') }, { value: codexExecutorValue, label: t('Codex CLI · indisponível no SDD'), disabled: true },
    ...(chosenProfile && !profiles.some(item => item.id === chosenProfile) ? [{ value: apiExecutorValue(chosenProfile), label: catalog?.destination || chosenProfile }] : []),
    ...profiles.map(item => ({ value: apiExecutorValue(item.id), label: item.name }))]
  const catalogStatusText = catalog?.status === 'empty'
    ? executor === 'codex_cli' ? t('O Codex CLI não informou modelos disponíveis. Confira a autenticação local e atualize o catálogo.') : t('Nenhum modelo disponível neste catálogo.')
    : executor === 'codex_cli' && catalog?.errorCode === 'catalog_cli_unavailable' ? t('Codex CLI indisponível. Instale e autentique no Codex CLI; depois atualize o catálogo.')
    : executor === 'codex_cli' && catalog?.errorCode === 'catalog_context_unverified' ? t('Não foi possível confirmar o contexto local do Codex. Atualize o catálogo antes de continuar.')
    : executor === 'codex_cli' && catalog?.errorCode === 'catalog_workspace_unavailable' ? t('A pasta deste projeto não está disponível. Reabra o projeto e atualize o catálogo.')
    : catalog?.status === 'partial' ? t('O catálogo veio incompleto; atualize antes de gerar.')
    : catalog?.status && catalog.status !== 'complete' ? t('Não foi possível confirmar o catálogo deste executor. Atualize antes de gerar.')
    : ''

  return <div className="authoring-workbench">
    <section className="authoring-discovery" aria-label={t('Discovery confirmada')}><div className="pipeline-editor-heading"><strong>Discovery</strong><span className="status-chip status-ready">{t('Escrito por você')}</span><span className="muted">{t('Versão {version}', { version: discovery?.version ?? '—' })}</span></div>
      {editingDiscovery && pipeline.discoveryFrozenVersion === 0 ? <><label className="field">{t('Texto da Discovery')}<textarea value={discoveryDraft} onChange={event => setDiscoveryDraft(event.target.value)} rows={8} maxLength={20000} /></label>{run && <p className="project-warning">{t('Salvar uma nova versão invalida esta rodada. As respostas e a síntese anteriores continuam consultáveis no histórico.')}</p>}{discoveryConflict && <p className="project-warning" role="alert">{t('A Discovery mudou em outra atualização. Copie seu texto antes de recarregar.')}</p>}<div className="pipeline-actions"><button type="button" className="touch-target secondary-button" onClick={() => { setEditingDiscovery(false); setDiscoveryDraft(discovery?.content ?? ''); setDiscoveryConflict(false) }}>{t('Cancelar edição')}</button><button type="button" className="touch-target primary-button" disabled={busy || discoveryConflict || !discoveryDraft.trim() || discoveryDraft.trim() === discovery?.content.trim()} onClick={() => void saveDiscovery()}>{t('Salvar nova versão')}</button></div></> : <pre className="mono diff-body">{discovery?.content ?? ''}</pre>}
      {pipeline.discoveryFrozenVersion === 0 && !editingDiscovery && <button type="button" className="touch-target text-button" disabled={busy} onClick={() => setEditingDiscovery(true)}>{t('Editar Discovery')}</button>}
      {(pipeline.discoveryFrozenVersion ?? 0) > 0 && !deriveOpen && <button type="button" className="touch-target text-button" disabled={busy} onClick={() => setDeriveOpen(true)}>{t('Criar nova versão derivada')}</button>}
      {(pipeline.discoveryFrozenVersion ?? 0) > 0 && derivationSafety === 'pending' && <p className="project-warning" role="alert">{t('O runner anterior ainda pode consumir recursos: seu cancelamento não foi confirmado. Derivar não cancela nem aguarda essa tentativa; uma geração futura no novo pipeline pode sobrepor chamadas e custos.')}</p>}
      {(pipeline.discoveryFrozenVersion ?? 0) > 0 && (derivationSafety === 'unknown' || derivationSafety === 'checking') && <div className="project-warning" role="status"><p>{derivationSafety === 'checking' ? t('Confirmando o estado de SPEC e Plan antes de permitir derivação…') : t('Não foi possível confirmar o cancelamento das fases. A derivação permanece bloqueada até uma leitura válida.')}</p><button type="button" className="touch-target secondary-button" disabled={derivationSafety === 'checking' || !onRefreshDerivationSafety} onClick={onRefreshDerivationSafety}>{t('Atualizar estado das fases')}</button></div>}
      {deriveOpen && <div className="authoring-answer"><p className="muted">{t('A Discovery aprovada permanece imutável. A nova versão será um pipeline separado.')}</p><label className="field">{t('Discovery da nova versão')}<textarea value={derivedDiscovery} onChange={event => setDerivedDiscovery(event.target.value)} rows={8} maxLength={20000} /></label>{derivationSafety === 'pending' && <label className="authoring-confirm"><input type="checkbox" checked={confirmDerivationRisk} onChange={event => setConfirmDerivationRisk(event.target.checked)} />{t('Entendo que derivar não cancela nem aguarda o runner anterior, que ainda pode gerar custos.')}</label>}<div className="pipeline-actions"><button type="button" className="touch-target secondary-button" onClick={() => setDeriveOpen(false)}>{t('Voltar')}</button><button type="button" className="touch-target primary-button" disabled={busy || !derivedDiscovery.trim() || !derivationAllowed} onClick={() => void derivePipeline()}>{t('Criar pipeline derivado')}</button></div></div>}
    </section>
    {historicalRuns.length > 0 && <details className="authoring-history">
      <summary>{t('Rodadas anteriores ({count})', { count: historicalRuns.length })}</summary>
      <div className="authoring-history-list">{historicalRuns.map(item => <button type="button" key={item.id} className="touch-target secondary-button" aria-pressed={inspectedRunId === item.id} onClick={() => setInspectedRunId(item.id)}>{t('Discovery v{version} · {state}', { version: item.discoveryVersion, state: readableState[item.state] })}</button>)}</div>
      {inspectedRun && <div className="authoring-history-detail">
        <strong>Discovery v{inspectedRun.discoveryVersion}</strong>
        <pre className="mono diff-body">{inspectedRun.discoveryContent}</pre>
        <span className="muted">{selectionOrigin(inspectedRun.selection, t('Codex CLI · local'))} · {inspectedRun.selection.modelId} · {readableState[inspectedRun.state]}</span>
        {inspectedRun.turns.map(turn => <p key={turn.questionId}><strong>{turn.question}</strong><br />{turn.answer || t('Sem resposta')}</p>)}
        {inspectedRun.syntheses.map(synthesis => <section key={synthesis.version} aria-label={t('Síntese v{version}', { version: synthesis.version })}>
          <strong>{t('Síntese v{version}', { version: synthesis.version })}</strong>
          <p>{synthesis.content.scope}</p>
          <strong>{t('Decisões')}</strong>
          <ul>{synthesis.content.decisions?.map((decision, index) => <li key={index}>{decision}</li>)}</ul>
          <strong>{t('Questões em aberto')}</strong>
          <ul>{synthesis.content.openQuestions?.map((question, index) => <li key={index}>{question}</li>)}</ul>
        </section>)}
      </div>}
    </details>}
    {historyError && <p className="project-warning" role="alert">{t('Histórico indisponível: {error}', { error: historyError })}</p>}
    {lookup === 'loading' && <p role="status" className="muted">{t('Consultando brainstorming…')}</p>}
    {lookup === 'error' && <button type="button" className="touch-target secondary-button" onClick={() => void refreshRun()}>{t('Tentar novamente')}</button>}
    {run && <div className="authoring-state"><span className="status-chip status-ready">{readableState[run.state]}</span><span className="muted">{t('{count}/5 perguntas', { count: run.questionCount })}</span><span className="muted">{t('{origin} · {model} · automático · limite {limit} tokens · {status} · conferido em {time}', { origin: selectionOrigin(run.selection, t('Codex CLI · local')), model: run.selection.modelId, limit: run.selection.maxOutputTokens, status: run.selection.status, time: new Date(run.selection.checkedAt).toLocaleString(localeTag()) })}</span></div>}
    {pausedBudgetFailure && <p className="form-error" role="status">{t('A última geração excedeu o orçamento reservado. A rodada foi pausada e nenhum conteúdo foi publicado.')}</p>}
    {shouldShowSelection && <section className="authoring-selection" aria-label={t('Modelo para a próxima ação')}><div className="pipeline-editor-heading"><strong>{profilesKnownEmpty ? t('Executor do SDD') : t('Modelo para SDD')}</strong>{!profilesKnownEmpty && <span className="muted">{t('Esforço automático')}</span>}</div>
      {canChooseExecutor && <div className="authoring-selectors"><IonPicker id="brainstorm-provider" label={t('Provedor desta etapa')} value={executorValue} onChange={value => {
        executorTouched.current = true
        if (value === codexExecutorValue) { setExecutor('codex_cli'); setProfileId('') }
        else if (value.startsWith('api:')) { setExecutor('api'); setProfileId(value.slice(4)) }
        else { setExecutor('api'); setProfileId('') }
        setModelId(''); setCatalog(undefined); setCatalogState('idle'); setConfirmUnfiltered(''); setUnfilteredDeclined(false); setConfirmJitLoad(''); intent.current = undefined
      }} options={executorOptions} searchable required disabled={busy || !!run && isActive(run)} />
      <IonPicker id="brainstorm-model" label={t('Modelo')} value={chosenModel} onChange={value => { setModelId(value); setConfirmUnfiltered(''); setUnfilteredDeclined(false); setConfirmJitLoad(''); intent.current = undefined }} options={[{ value: '', label: t('Escolha um modelo') }, ...(catalog?.models ?? []).map(item => ({ value: item.id, label: item.displayName || item.id, disabled: item.availability === 'unavailable' }))]} searchable required disabled={busy || !catalogOffersModels || !!run && isActive(run)} /></div>}
      {canChooseExecutor && <p className="muted authoring-provenance" role="status">{t('No Professional SDD, o Codex CLI fica indisponível porque pode ler arquivos fora do projeto. Use um perfil API; Codex CLI continua disponível em Conversas no modo Casual.')}</p>}
      {settingsState === 'error' && <p className="muted" role="status">{t('Não foi possível ler o provedor padrão salvo. Escolha o provedor desta etapa manualmente.')}</p>}
      {settingsState === 'ready' && configuredDefaultBackend && configuredDefaultStatus === 'supported' && <p className="muted authoring-provenance">{t('Padrão das Configurações: {provider}{model}. Esta escolha vale só para Discovery.', { provider: configuredDefaultBackend === 'codex' ? t('Codex CLI · local') : profiles.find(item => item.id === configuredDefaultBackend)?.name ?? configuredDefaultBackend, model: currentSettings.current.defaultModelBackendId === configuredDefaultBackend && currentSettings.current.defaultModelId ? ` · ${currentSettings.current.defaultModelId}` : '' })}</p>}
      {settingsState === 'ready' && configuredDefaultBackend && configuredDefaultStatus === 'unknown' && <p className="muted authoring-provenance" role="status">{t('A lista de perfis API falhou. Vou validar o provedor padrão salvo pelo catálogo antes de iniciar; as opções de troca podem estar incompletas.')}</p>}
      {settingsState === 'ready' && configuredDefaultBackend === 'codex' && !profilesKnownEmpty && <p className="project-warning authoring-provenance" role="alert">{t('O Codex CLI está configurado para Conversas, mas o Professional SDD exige leitura confinada ao projeto. Como o sandbox do CLI pode ler outros arquivos locais, selecione um perfil API para esta etapa.')}</p>}
      {settingsState === 'ready' && configuredDefaultBackend && configuredDefaultStatus === 'unsupported' && <p className="form-error authoring-provenance" role="alert">{t('O padrão global “{provider}” não é compatível com o SDD. Escolha um perfil API para esta etapa.', { provider: configuredDefaultBackend })}</p>}
      {profilesState === 'loading' && <p className="muted" role="status">{t('Consultando perfis API disponíveis…')}</p>}
      {profilesState === 'ready' && profiles.length === 0 && <div className="authoring-provider-empty" role="alert"><strong>{t('Este pipeline precisa de um executor disponível')}</strong><p>{t('Nenhum perfil API elegível foi encontrado. O Codex CLI está configurado para Conversas, mas não pode ser confinado à pasta neste fluxo Professional. Configure um provedor API ou mude para Casual no seletor do topo para continuar com Codex.')}</p>{onSettings && <button type="button" className="touch-target secondary-button" disabled={busy} onClick={onSettings}>{t('Configurar provedor API')}</button>}</div>}
      {profilesState === 'error' && <p className="muted" role="status">{t('Não foi possível consultar perfis API. O Codex CLI e o provedor padrão salvo ainda podem ser validados pelo catálogo.')}</p>}
      {catalogState === 'loading' && <span className="muted" role="status">{executor === 'codex_cli' ? t('Consultando modelos do Codex CLI…') : t('Atualizando catálogo…')}</span>}
      {catalogState === 'error' && <span className="form-error" role="alert">{executor === 'codex_cli' ? t('Não foi possível consultar o catálogo do Codex CLI. Atualize para tentar novamente.') : t('Não foi possível consultar o catálogo API. Atualize para tentar novamente.')}</span>}
      {catalogStatusText && <span className="form-error" role="alert">{catalogStatusText}</span>}
      {catalog && catalog.status === 'complete' && <p className="muted authoring-provenance">{!catalogFresh ? t('Catálogo desatualizado; atualize antes de continuar') : t('Catálogo completo')} · {executor === 'codex_cli' ? t('Codex CLI local') : catalog.source} · {t('conferido em {time}', { time: new Date(catalog.checkedAt).toLocaleString(localeTag()) })}{selectedModel?.contextLength ? ` · ${t('contexto {context}', { context: selectedModel.contextLength.toLocaleString(localeTag()) })}` : ''}</p>}
      {needsUnfiltered && <label className="authoring-confirm"><input type="checkbox" checked={confirmedUnfiltered || inheritedUnfiltered} onChange={event => { setConfirmUnfiltered(event.target.checked ? choiceKey : ''); setUnfilteredDeclined(!event.target.checked) }} /> {t('Confirmo usar a lista geral não filtrada da OpenRouter.')}</label>}
      {needsLoad && <label className="authoring-confirm"><input type="checkbox" checked={confirmedJitLoad || inheritedJitLoad} onChange={event => setConfirmJitLoad(event.target.checked ? choiceKey : '')} /> {t('Confirmo carregar este modelo do LM Studio antes da geração.')}</label>}
      {run && <p className="muted authoring-provenance">{t('Em uso: {origin} · {model} · esforço automático · limite {limit} tokens · conferido em {time}', { origin: selectionOrigin(run.selection, t('Codex CLI · local')), model: run.selection.modelId, limit: run.selection.maxOutputTokens, time: new Date(run.selection.checkedAt).toLocaleString(localeTag()) })}</p>}
      {chosenBackendId && <button type="button" className="touch-target text-button" disabled={busy} onClick={() => void refreshCatalog()}>{t('Atualizar catálogo')}</button>}
    </section>}
    {lookup === 'empty' && canStartBrainstorm && !catalogUsable && <p className="muted" role="note">{!chosenBackendId ? t('Escolha um provedor e um modelo para iniciar o brainstorming.') : !chosenModel ? t('Escolha um modelo para iniciar o brainstorming.') : catalogState === 'loading' ? t('Atualizando o catálogo de modelos…') : t('Atualize o catálogo e confirme o modelo para liberar a ação.')}</p>}
    {lookup === 'empty' && canStartBrainstorm && <div className="pipeline-actions"><button type="button" className="touch-target primary-button" disabled={busy || !catalogUsable} onClick={() => void apply(`start:${pipeline.id}:${pipeline.revision}:${discovery?.version}:${executor}:${chosenBackendId}:${chosenModel}`, (id, input) => backend.startBrainstorming({ pipelineId: pipeline.id, requestId: id, pipelineRevision: pipeline.revision, discoveryVersion: discovery!.version, selection: input! }), { executor, backendId: chosenBackendId, id: chosenModel })}>{t('Concluir Discovery e iniciar brainstorming')}</button></div>}
    {run && <section ref={conversation} className="authoring-conversation" aria-label={t('Conversa de brainstorming')}>
      <button type="button" className="touch-target text-button" disabled={busy} onClick={() => void refreshRun()}>{t('Atualizar brainstorming')}</button>
      {run.turns.length > 0 && <><ol className="authoring-turns">{run.turns.slice(-1).map(turn => <li key={turn.questionId}><span className="muted">{t('Pergunta {number} de 5', { number: turn.number })}</span><strong>{turn.question}</strong>{turn.answer && <p>{t('Resposta confirmada: {answer}', { answer: turn.answer })}</p>}</li>)}</ol>{run.turns.length > 1 && <details className="authoring-history"><summary>{t('Respostas anteriores ({count})', { count: run.turns.length - 1 })}</summary><ol className="authoring-turns">{run.turns.slice(0, -1).map(turn => <li key={turn.questionId}><strong>{turn.question}</strong><p>{t('Resposta confirmada: {answer}', { answer: turn.answer || t('Sem resposta') })}</p></li>)}</ol></details>}</>}
      {run.state === 'ready' && <div className="pipeline-actions"><button type="button" className="touch-target primary-button" disabled={busy || !catalogUsable || run.questionCount >= 5} onClick={() => void apply(`question:${run.id}:${run.revision}:${executor}:${chosenBackendId}:${chosenModel}`, (id, input) => backend.generateBrainstormQuestion({ ref: refFor(run, id), selection: input! }), { executor, backendId: chosenBackendId, id: chosenModel })}>{t('Gerar pergunta')}</button>{answeredCount > 0 && <button type="button" className="touch-target secondary-button" disabled={busy || !catalogUsable} onClick={() => void apply(`sufficient:${run.id}:${run.revision}:${executor}:${chosenBackendId}:${chosenModel}`, (id, input) => backend.questionsSufficient({ ref: refFor(run, id), selection: input! }), { executor, backendId: chosenBackendId, id: chosenModel })}>{t('Perguntas suficientes')}</button>}{answeredCount === 0 && <button type="button" className="touch-target secondary-button" onClick={() => setSkipOpen(true)} disabled={busy}>{t('Pular perguntas')}</button>}</div>}
      {run.state === 'waiting_answer' && latestQuestion && <div className="authoring-answer"><label className="field">{t('Sua resposta')}<textarea rows={4} maxLength={16384} value={answer} onChange={event => setAnswer(event.target.value)} /></label><div className="pipeline-actions"><button type="button" className="touch-target secondary-button" disabled={busy || !answer.trim()} onClick={() => void apply(`answer:${run.id}:${run.revision}:${latestQuestion.questionId}:${answer.trim()}`, id => backend.answerBrainstormQuestion({ ref: refFor(run, id), questionId: latestQuestion.questionId, answer: answer.trim() }).then(receipt => { setAnswer(''); return receipt }))}>{t('Confirmar resposta')}</button>{answeredCount > 0 && <button type="button" className="touch-target secondary-button" disabled={busy || !catalogUsable} onClick={() => void apply(`sufficient:${run.id}:${run.revision}:${executor}:${chosenBackendId}:${chosenModel}`, (id, input) => backend.questionsSufficient({ ref: refFor(run, id), selection: input! }), { executor, backendId: chosenBackendId, id: chosenModel })}>{t('Perguntas suficientes')}</button>}{answeredCount === 0 && <button type="button" className="touch-target secondary-button" disabled={busy} onClick={() => setSkipOpen(true)}>{t('Pular perguntas')}</button>}</div></div>}
      {run.state === 'ready_for_synthesis' && <div className="pipeline-actions"><button type="button" className="touch-target primary-button" disabled={busy || !catalogUsable} onClick={() => void apply(`synthesis:${run.id}:${run.revision}:${executor}:${chosenBackendId}:${chosenModel}`, (id, input) => backend.finishAndGenerateSynthesis({ ref: refFor(run, id), selection: input! }), { executor, backendId: chosenBackendId, id: chosenModel })}>{t('Gerar síntese')}</button></div>}
      {run.state === 'waiting_user' && latestSynthesis && <div className="authoring-synthesis"><h4>{t('Síntese · versão {version}', { version: latestSynthesis.version })}</h4><h5>{t('Escopo')}</h5><p>{latestSynthesis.content.scope}</p><h5>{t('Decisões')}</h5><ul>{latestSynthesis.content.decisions?.map((item, index) => <li key={index}>{item}</li>)}</ul><h5>{t('Questões em aberto')}</h5><ul>{latestSynthesis.content.openQuestions?.map((item, index) => <li key={index}>{item}</li>)}</ul><div className="pipeline-actions"><button type="button" className="touch-target primary-button" disabled={busy} onClick={() => void apply(`approve:${run.id}:${run.revision}:${run.synthesisVersion}`, id => backend.approveBrainstormSynthesis({ ref: refFor(run, id), synthesisVersion: run.synthesisVersion }))}>{t('Aprovar esta síntese')}</button></div><label className="field">{t('Feedback para revisão')}<textarea rows={3} value={feedback} onChange={event => setFeedback(event.target.value)} /></label><IonPicker id="revision-choice" label={t('Próximo passo')} value={revisionChoice} onChange={value => setRevisionChoice(value as 'more_questions' | 'new_synthesis')} options={[{ value: 'more_questions', label: t('Mais perguntas') }, { value: 'new_synthesis', label: t('Nova síntese') }]} /><button type="button" className="touch-target secondary-button" disabled={busy || !feedback.trim()} onClick={() => void apply(`revision:${run.id}:${run.revision}:${run.synthesisVersion}:${revisionChoice}:${feedback.trim()}`, id => backend.requestBrainstormRevision({ ref: refFor(run, id), synthesisVersion: run.synthesisVersion, choice: revisionChoice, feedback: feedback.trim() }).then(receipt => { setFeedback(''); return receipt }))}>{t('Solicitar revisão')}</button></div>}
      {skipOpen && answeredCount === 0 && <div className="pipeline-skip-confirm"><p>{t('O pulo ficará registrado e exigirá uma segunda confirmação da Discovery.')}</p><label className="field">{t('Motivo do pulo')}<textarea value={skipReason} onChange={event => setSkipReason(event.target.value)} rows={2} /></label><div className="pipeline-actions"><button type="button" className="touch-target secondary-button" onClick={() => setSkipOpen(false)}>{t('Voltar')}</button><button type="button" className="touch-target secondary-button" disabled={busy || !skipReason.trim()} onClick={() => void apply(`skip:${run.id}:${run.revision}:${skipReason.trim()}`, id => backend.skipBrainstormQuestions({ ref: refFor(run, id), reason: skipReason.trim() }).then(receipt => { setSkipOpen(false); return receipt }))}>{t('Registrar pulo')}</button></div></div>}
      {run.state === 'skipped_waiting_confirmation' && <button type="button" className="touch-target primary-button" disabled={busy} onClick={() => void apply(`confirm-skip:${run.id}:${run.revision}`, id => backend.confirmDiscoveryAfterSkip(refFor(run, id)))}>{t('Confirmar Discovery após pulo')}</button>}
      {run.state === 'paused' && <div className="pipeline-actions"><button type="button" className="touch-target secondary-button" disabled={busy} onClick={() => void apply(`resume:${run.id}:${run.revision}`, id => backend.resumePausedBrainstorm(refFor(run, id)))}>{t('Retomar brainstorming')}</button>{answeredCount === 0 && <button type="button" className="touch-target secondary-button" disabled={busy} onClick={() => setSkipOpen(true)}>{t('Pular perguntas')}</button>}</div>}
      {isActive(run) && <div className="pipeline-actions"><span className="muted" role="status">{pendingAction ? t('Aguardando geração…') : readableState[run.state]}</span>{activeAttempt && <button type="button" className="touch-target secondary-button" disabled={cancelPending} onClick={() => void cancelAttempt(run, activeAttempt.id)}>{cancelPending ? t('Cancelando…') : t('Cancelar geração')}</button>}</div>}
      {run.state === 'approved' && <p className="muted">{t('Discovery aprovada. A SPEC está pendente de geração; nenhuma edição manual está disponível nesta etapa.')}</p>}
    </section>}
    {error && <p className="form-error" role="alert">{error}</p>}{notice && <p className="form-success" role="status">{notice}</p>}
  </div>
}
