import { useEffect, useRef, useState } from 'react'
import { cliCatalogProblem, documentCLISources, errorMessage, isDocumentCLI, type Backend, type BackendOption, type ModelCatalogResult, type PipelineDesign, type PipelineDesignStage, type ProviderProfile, type SDDModelSelection, type Settings, type StageExecutor } from '../../lib/backend'
import { IonPicker } from '../../components/IonPicker'
import { designLabels, designStageOrder } from './PipelineDesignDocument'

type Props = { backend: Backend; design: PipelineDesign; /** What the project chose per phase: a document without a pick here follows it before the default of Settings. */ stageExecutors?: StageExecutor[]; disabled: boolean; onChange: (selections: Partial<Record<PipelineDesignStage, SDDModelSelection>>, ready: boolean) => void; onSettings?: () => void }
type Choice = { backendId: string; modelId: string; catalog?: ModelCatalogResult; confirmUnfiltered: boolean; confirmJitLoad: boolean; status: 'loading' | 'ready' | 'error'; error: string }
const inheritedChoice: Choice = { backendId: '', modelId: '', confirmUnfiltered: false, confirmJitLoad: false, status: 'ready', error: '' }
const usable = (profile: ProviderProfile) => !profile.endpointBlocked && profile.providerType !== 'generic' && (profile.hasCredential || ['lm_studio', 'ollama'].includes(profile.providerType))

export function PipelineDesignModel({ backend, design, stageExecutors = [], disabled, onChange, onSettings }: Props) {
  const [open, setOpen] = useState(false), [settings, setSettings] = useState<Settings>(), [settingsError, setSettingsError] = useState(false)
  const [profiles, setProfiles] = useState<ProviderProfile[]>([]), [providers, setProviders] = useState<BackendOption[]>([]), [providersError, setProvidersError] = useState('')
  const [choices, setChoices] = useState<Record<PipelineDesignStage, Choice>>({ discovery: { ...inheritedChoice }, spec: { ...inheritedChoice }, plan: { ...inheritedChoice } })
  const requests = useRef<Record<PipelineDesignStage, number>>({ discovery: 0, spec: 0, plan: 0 }), alive = useRef(true), callback = useRef(onChange)
  callback.current = onChange
  useEffect(() => {
    alive.current = true; let active = true
    void backend.getSettings().then(value => { if (active) setSettings(value) }).catch(() => { if (active) setSettingsError(true) })
    return () => { active = false; alive.current = false; for (const stage of designStageOrder) requests.current[stage]++ }
  }, [backend, design.pipelineId])
  useEffect(() => {
    if (!open) return
    let active = true
    void Promise.all([backend.listProviderProfiles(), backend.listBackends()]).then(([foundProfiles, backends]) => {
      if (!active) return
      const eligible = foundProfiles.filter(usable)
      setProfiles(eligible); setProviders(backends.filter(item => item.available && (isDocumentCLI(item.id) && item.professionalAvailable === true || item.kind === 'api' && eligible.some(profile => profile.id === item.id)))); setProvidersError('')
    }).catch(failure => { if (active) setProvidersError(errorMessage(failure)) })
    return () => { active = false }
  }, [backend, open])

  function toSelection(choice: Choice): SDDModelSelection | undefined {
    const catalog = choice.catalog, profile = profiles.find(item => item.id === choice.backendId), model = catalog?.models.find(item => item.id === choice.modelId && item.backendId === choice.backendId && item.source === catalog.source)
    const age = catalog ? Date.now() - Date.parse(catalog.checkedAt) : Infinity
    if (!catalog || !model || model.availability === 'unavailable' || choice.status !== 'ready' || !catalog.complete || catalog.status !== 'complete' || !catalog.profileRevision || age < -60000 || age > 300000) return undefined
    const common = { modelId: model.id, catalogRevision: catalog.profileRevision, checkedAt: catalog.checkedAt, reasoningEffort: '', maxOutputTokens: 4096 }
    if (isDocumentCLI(choice.backendId)) return catalog.source === documentCLISources[choice.backendId] ? { ...common, executor: 'codex_cli', backendId: choice.backendId, profileId: '', source: documentCLISources[choice.backendId], destination: '', credentialToken: '', confirmUnverifiedManual: false, confirmUnfiltered: false, confirmJitLoad: false } : undefined
    if (!profile || !catalog.credentialToken || catalog.source === 'openrouter_general_unfiltered' && !choice.confirmUnfiltered || profile.providerType === 'lm_studio' && model.loaded === false && !choice.confirmJitLoad) return undefined
    return { ...common, executor: 'api', backendId: '', profileId: profile.id, source: catalog.source, destination: catalog.destination, credentialToken: catalog.credentialToken, confirmUnverifiedManual: false, confirmUnfiltered: choice.confirmUnfiltered, confirmJitLoad: choice.confirmJitLoad }
  }
  useEffect(() => {
    const selections: Partial<Record<PipelineDesignStage, SDDModelSelection>> = {}, overrides = designStageOrder.filter(stage => choices[stage].backendId)
    for (const stage of overrides) { const selected = toSelection(choices[stage]); if (selected) selections[stage] = selected }
    callback.current(selections, overrides.every(stage => !!selections[stage]))
  }, [choices, profiles])

  async function chooseProvider(stage: PipelineDesignStage, backendId: string) {
    const request = ++requests.current[stage]
    setChoices(previous => ({ ...previous, [stage]: backendId ? { ...inheritedChoice, backendId, status: 'loading' } : { ...inheritedChoice } }))
    if (!backendId) return
    try {
      const catalog = isDocumentCLI(backendId) ? await backend.queryCLIModelCatalog({ workspaceId: design.workspaceId, backendId }) : await backend.queryHTTPModelCatalog({ profileId: backendId, searchTerm: '', refresh: true })
      if (!alive.current || request !== requests.current[stage]) return
      const profile = profiles.find(item => item.id === backendId), preferred = settings?.defaultModelBackendId === backendId ? settings.defaultModelId : profile?.model ?? ''
      const modelId = catalog.models.some(item => item.id === preferred && item.backendId === backendId && item.source === catalog.source && item.availability !== 'unavailable') ? preferred : ''
      const age = Date.now() - Date.parse(catalog.checkedAt), valid = catalog.status === 'complete' && catalog.complete && !!catalog.profileRevision && (isDocumentCLI(backendId) ? catalog.source === documentCLISources[backendId] : !!catalog.credentialToken) && age >= -60000 && age <= 300000
      setChoices(previous => ({ ...previous, [stage]: { ...previous[stage], catalog, modelId, status: valid ? 'ready' : 'error', error: valid ? '' : catalog.errorCode === 'catalog_not_logged_in' ? cliCatalogProblem(catalog, providers.find(item => item.id === backendId)?.name ?? backendId) : 'Catálogo incompleto ou antigo. Atualize para escolher um modelo.' } }))
    } catch (failure) { if (alive.current && request === requests.current[stage]) setChoices(previous => ({ ...previous, [stage]: { ...previous[stage], status: 'error', error: errorMessage(failure) } })) }
  }
  const effective = designStageOrder.map(stage => design.documents[stage]?.selection?.modelId).filter(Boolean), defaultLabel = settings?.defaultModelId || (settings?.defaultBackendId === 'codex' ? 'Codex CLI' : settings?.defaultBackendId === 'claude' ? 'Claude Code' : settings?.defaultBackendId ? 'Modelo configurado' : '')
  const phaseChoice = (stage: PipelineDesignStage) => stageExecutors.find(item => item.stage === stage)
  const byPhase = designStageOrder.flatMap(stage => phaseChoice(stage) ? [`${designLabels[stage]}: ${phaseChoice(stage)!.modelId || 'modelo do perfil'}`] : [])
  return <details className="design-models" onToggle={event => setOpen(event.currentTarget.open)}>
    <summary>Modelos <span className="muted">{effective.length ? `Última versão · ${[...new Set(effective)].join(', ')}` : byPhase.length ? `Por fase · ${byPhase.join(' · ')}` : defaultLabel ? `Padrão · ${defaultLabel}` : 'Padrão das Configurações'}</span></summary>
    {settingsError && <p className="form-error" role="alert">Não foi possível ler o padrão. Atualize o trabalho ou confira Configurações.</p>}
    {open && <><p className="muted">Escolha um modelo só para este trabalho.</p>{providersError && <p className="form-error" role="alert">{providersError}</p>}<div className="design-model-grid">{designStageOrder.map(stage => {
      const choice = choices[stage], catalog = choice.catalog, model = catalog?.models.find(item => item.id === choice.modelId), profile = profiles.find(item => item.id === choice.backendId)
      return <div key={stage} className="design-model-choice"><h4>{designLabels[stage]}</h4><IonPicker id={`design-provider-${stage}`} label={`Provedor de ${designLabels[stage]}`} value={choice.backendId} onChange={value => void chooseProvider(stage, value)} disabled={disabled} options={[{ value: '', label: phaseChoice(stage) ? 'Usar a escolha da fase' : 'Usar padrão' }, ...providers.map(item => ({ value: item.id, label: item.name }))]} />{choice.backendId && <><IonPicker id={`design-model-${stage}`} label={`Modelo de ${designLabels[stage]}`} value={choice.modelId} onChange={value => setChoices(previous => ({ ...previous, [stage]: { ...previous[stage], modelId: value, confirmJitLoad: false } }))} searchable required disabled={disabled || choice.status !== 'ready'} options={[{ value: '', label: 'Escolha um modelo' }, ...(catalog?.models ?? []).filter(item => item.backendId === choice.backendId && item.source === catalog?.source).map(item => ({ value: item.id, label: item.displayName || item.id, disabled: item.availability === 'unavailable' }))]} />{choice.status === 'loading' && <p role="status" className="muted">Consultando modelos…</p>}{choice.error && <p role="alert" className="form-error">{choice.error}</p>}{catalog?.source === 'openrouter_general_unfiltered' && <label className="authoring-confirm"><input type="checkbox" checked={choice.confirmUnfiltered} disabled={disabled} onChange={event => setChoices(previous => ({ ...previous, [stage]: { ...previous[stage], confirmUnfiltered: event.target.checked } }))} />Confirmo a lista geral da OpenRouter.</label>}{profile?.providerType === 'lm_studio' && model?.loaded === false && <label className="authoring-confirm"><input type="checkbox" checked={choice.confirmJitLoad} disabled={disabled} onChange={event => setChoices(previous => ({ ...previous, [stage]: { ...previous[stage], confirmJitLoad: event.target.checked } }))} />Confirmo carregar este modelo.</label>}<button type="button" className="touch-target text-button" disabled={disabled} onClick={() => void chooseProvider(stage, choice.backendId)}>Atualizar catálogo de {designLabels[stage]}</button></>}</div>
    })}</div>{providers.length === 0 && <p className="muted">Nenhum executor disponível para substituição local. A preparação usa o padrão configurado.</p>}{onSettings && <button type="button" className="touch-target text-button" onClick={onSettings} disabled={disabled}>Abrir Configurações</button>}</>}
  </details>
}
