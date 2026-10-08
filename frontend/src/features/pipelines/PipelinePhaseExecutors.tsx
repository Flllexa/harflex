import { useEffect, useRef, useState } from 'react'
import { SlidersHorizontal } from 'lucide-react'
import { IonPicker, type IonOption } from '../../components/IonPicker'
import { cliCatalogProblem, errorMessage, isDocumentCLI, type Backend, type BackendOption, type PipelineStage, type ProviderProfile, type StageExecutor } from '../../lib/backend'

export const phaseOrder: readonly PipelineStage[] = ['discovery', 'spec', 'plan', 'code', 'eval', 'prs']
export const phaseText: Record<PipelineStage, { label: string; what: string }> = {
  discovery: { label: 'Discovery', what: 'Delimita o problema' },
  spec: { label: 'SPEC', what: 'Escreve a especificação' },
  plan: { label: 'Plan', what: 'Planeja a implementação' },
  code: { label: 'Code', what: 'Escreve o código numa cópia isolada' },
  eval: { label: 'QA', what: 'Confere o resultado, sem escrever' },
  prs: { label: 'PRs', what: 'Abre os pull requests, com terminal e MCP' },
}
/** The phases whose document the backend writes from the saved choice alone, with nobody there to confirm anything. */
const documentPhases = new Set<PipelineStage>(['discovery', 'spec', 'plan'])

/** An API profile the phases can call: a real catalog to choose from and a credential, or a local server that needs none. */
export const usableProfile = (profile: ProviderProfile) => !profile.endpointBlocked && profile.providerType !== 'generic' &&
  (profile.hasCredential || profile.providerType === 'lm_studio' || profile.providerType === 'ollama')

/**
 * Who can work on a phase. The documents and Code/QA take an API profile with a catalog, or Codex when the Harflex can
 * hold it to its contract; the pull requests need the Harflex tool loop (terminal and MCP), which only API profiles have.
 */
export function phaseCanUse(stage: PipelineStage, option: BackendOption, profile?: ProviderProfile): boolean {
  if (!option.available) return false
  if (option.kind === 'api') return stage === 'prs' || (!!profile && usableProfile(profile))
  return isDocumentCLI(option.id) && stage !== 'prs' && option.professionalAvailable === true
}

/** The list with the phase set to `value`, or back on the default when there is none, in pipeline order. */
const withPhase = (list: StageExecutor[], stage: PipelineStage, value?: StageExecutor) => [...list.filter(item => item.stage !== stage), ...(value ? [value] : [])]
  .sort((a, b) => phaseOrder.indexOf(a.stage) - phaseOrder.indexOf(b.stage))

type CatalogModel = IonOption & { unloaded: boolean }
type Catalog = { state: 'loading' | 'ready' | 'error'; models: CatalogModel[]; error: string; unfiltered: boolean }
type Draft = { backendId: string; modelId: string }

type Props = {
  backend: Backend
  backends: BackendOption[]
  /** The API profiles, read once by the page; undefined while they are being read. */
  profiles?: ProviderProfile[]
  workspaceId: string
  executors: StageExecutor[]
  /** Takes an update, not a list: two phases saved close together must not undo one another. */
  onExecutorsChange: (update: (current: StageExecutor[]) => StageExecutor[]) => void
  /** The default of Settings, which every phase without a choice of its own follows. */
  defaults: { backendId: string; modelBackendId: string; modelId: string }
  open: boolean
  onToggle: (open: boolean) => void
  /** The phase the open pipeline is in, so it is marked. */
  currentStage?: PipelineStage
  disabled?: boolean
  /** The saved choices are being read, or could not be: what is shown would be a guess, so nothing can be changed. */
  loading?: boolean
  loadFailed?: boolean
  onRetry?: () => void
}

export function PipelinePhaseExecutors({ backend, backends, profiles, workspaceId, executors, onExecutorsChange, defaults, open, onToggle, currentStage, disabled = false, loading = false, loadFailed = false, onRetry }: Props) {
  const [catalogs, setCatalogs] = useState<Record<string, Catalog>>({})
  const [drafts, setDrafts] = useState<Partial<Record<PipelineStage, Draft>>>({})
  const [busy, setBusy] = useState<Partial<Record<PipelineStage, boolean>>>({})
  const [errors, setErrors] = useState<Partial<Record<PipelineStage, string>>>({})
  // The model picker keeps what was typed or picked in it; after a choice that failed to save it is drawn again from what is saved.
  const [redrawn, setRedrawn] = useState<Partial<Record<PipelineStage, number>>>({})
  const requested = useRef(new Set<string>())
  const alive = useRef(true)
  useEffect(() => { alive.current = true; return () => { alive.current = false } }, [])

  const saved = (stage: PipelineStage) => executors.find(item => item.stage === stage)
  const chosen = (stage: PipelineStage): Draft | undefined => drafts[stage] ?? (saved(stage) && { backendId: saved(stage)!.backendId, modelId: saved(stage)!.modelId })
  const optionOf = (id: string) => backends.find(item => item.id === id)
  const profileOf = (id: string) => profiles?.find(item => item.id === id)
  const nameOf = (id: string) => optionOf(id)?.name ?? id
  // A generic server publishes no catalog the Harflex can confirm a model against: only the profile's own model is possible.
  const hasCatalog = (id: string) => profileOf(id)?.providerType !== 'generic'
  const eligible = (stage: PipelineStage) => backends.filter(item => phaseCanUse(stage, item, profileOf(item.id)))
  // Until the profiles are read there is nothing to say about whether an executor works.
  const works = (stage: PipelineStage, id: string) => !profiles || (!!optionOf(id) && phaseCanUse(stage, optionOf(id)!, profileOf(id)))
  const defaultBackend = optionOf(defaults.backendId)
  const defaultModel = defaults.modelBackendId === defaults.backendId ? defaults.modelId : profileOf(defaults.backendId)?.model ?? ''
  const defaultLabel = defaultBackend ? `${defaultBackend.name}${defaultModel ? ` · ${defaultModel}` : ''}` : ''

  async function loadCatalog(option: BackendOption, refresh = false) {
    if ((!refresh && requested.current.has(option.id)) || !hasCatalog(option.id)) return
    requested.current.add(option.id)
    setCatalogs(previous => ({ ...previous, [option.id]: { state: 'loading', models: previous[option.id]?.models ?? [], error: '', unfiltered: false } }))
    try {
      const result = option.kind === 'cli'
        ? await backend.queryCLIModelCatalog({ workspaceId, backendId: option.id })
        : await backend.queryHTTPModelCatalog({ profileId: option.id, searchTerm: '', refresh })
      if (!alive.current) return
      const models = result.models.filter(item => item.backendId === option.id && item.source === result.source)
        .map(item => ({ value: item.id, label: item.displayName || item.id, disabled: item.availability === 'unavailable', unloaded: item.loaded === false }))
      if (result.status === 'complete' && result.complete) setCatalogs(previous => ({ ...previous, [option.id]: { state: 'ready', models, error: '', unfiltered: result.source === 'openrouter_general_unfiltered' } }))
      else {
        requested.current.delete(option.id)
        setCatalogs(previous => ({ ...previous, [option.id]: { state: 'error', models: [], unfiltered: false, error: result.status === 'empty' ? `Nenhum modelo disponível em ${option.name}.` : result.status === 'unsupported' ? `${option.name} não publica um catálogo de modelos.` : option.kind === 'cli' ? cliCatalogProblem(result, option.name) : `Não foi possível confirmar o catálogo de ${option.name}.` } }))
      }
    } catch (failure) {
      if (!alive.current) return
      requested.current.delete(option.id)
      setCatalogs(previous => ({ ...previous, [option.id]: { state: 'error', models: [], unfiltered: false, error: errorMessage(failure) } }))
    }
  }

  // Models are read only for the executors the phases use, and only while the card is open.
  const used = [...new Set(phaseOrder.flatMap(stage => chosen(stage)?.backendId ?? []))].join(',')
  useEffect(() => {
    if (!open || !profiles) return
    for (const id of used ? used.split(',') : []) {
      const option = optionOf(id)
      if (option?.available) void loadCatalog(option)
    }
  }, [open, profiles, used, backends])

  const setStageError = (stage: PipelineStage, text: string) => setErrors(previous => ({ ...previous, [stage]: text }))
  const dropDraft = (stage: PipelineStage) => setDrafts(previous => { const { [stage]: _, ...rest } = previous; return rest })
  async function persist(stage: PipelineStage, action: () => Promise<StageExecutor | undefined>) {
    setBusy(previous => ({ ...previous, [stage]: true }))
    setStageError(stage, '')
    try {
      const value = await action()
      // The card of another project is not this one's to change.
      if (!alive.current) return
      onExecutorsChange(current => withPhase(current, stage, value))
      dropDraft(stage)
    } catch (failure) {
      if (alive.current) { setStageError(stage, errorMessage(failure)); setRedrawn(previous => ({ ...previous, [stage]: (previous[stage] ?? 0) + 1 })) }
    } finally { if (alive.current) setBusy(previous => ({ ...previous, [stage]: false })) }
  }

  function chooseExecutor(stage: PipelineStage, backendId: string) {
    if (!backendId) {
      void persist(stage, async () => { await backend.clearStageExecutor(workspaceId, stage); return undefined })
      return
    }
    const option = optionOf(backendId)
    if (option) void loadCatalog(option)
    // An API profile has a model of its own, so choosing it is a complete choice; a CLI needs a model before it is saved.
    if (option?.kind === 'api') void persist(stage, () => backend.saveStageExecutor({ workspaceId, stage, backendId, modelId: '' }))
    else setDrafts(previous => ({ ...previous, [stage]: { backendId, modelId: '' } }))
  }
  function chooseModel(stage: PipelineStage, modelId: string) {
    const current = chosen(stage)
    if (!current || (modelId === '' && optionOf(current.backendId)?.kind === 'cli')) return
    void persist(stage, () => backend.saveStageExecutor({ workspaceId, stage, backendId: current.backendId, modelId }))
  }

  const count = executors.length
  const unavailable = executors.filter(item => !works(item.stage, item.backendId)).length
  return <>
    <details className="phase-executors" open={open} onToggle={event => { if (event.currentTarget.open !== open) onToggle(event.currentTarget.open) }}>
      <summary><SlidersHorizontal aria-hidden="true" /><strong>Provedor e modelo por fase</strong>
        <span className="muted">{loadFailed ? 'Escolhas indisponíveis' : loading ? 'Lendo as escolhas…' : count === 0 ? 'Todas seguem o padrão das Configurações' : `${count} de ${phaseOrder.length} com escolha própria`}</span>
        {!loadFailed && !loading && unavailable > 0 && <span className="phase-warning">{unavailable === 1 ? '1 indisponível' : `${unavailable} indisponíveis`}</span>}</summary>
      <p className="muted phase-executors-intro">Cada fase pode usar um provedor de API, o Codex ou o Claude Code, com o modelo que você escolher. Vale para este projeto: o próximo trabalho e as fases que ainda não rodaram usam o que estiver aqui. Fase sem escolha segue o padrão das Configurações.</p>
      <ul className="phase-executors-list" aria-label="Executor de cada fase">{phaseOrder.map(stage => {
        const text = phaseText[stage], current = chosen(stage), option = current ? optionOf(current.backendId) : undefined
        const profile = current ? profileOf(current.backendId) : undefined
        const catalog = current ? catalogs[current.backendId] : undefined
        const stillCan = !current || works(stage, current.backendId)
        const executorOptions: IonOption[] = [{ value: '', label: 'Usar o padrão' }, ...eligible(stage).map(item => ({ value: item.id, label: `${item.name} · ${item.kind === 'cli' ? 'CLI' : 'API'}` }))]
        // What was saved stays visible even when it cannot be offered any more, so it can be seen and replaced.
        // While the profiles are still being read nothing is known about it, so it is not called unavailable yet.
        if (current && !executorOptions.some(item => item.value === current.backendId)) executorOptions.push({ value: current.backendId, label: profiles ? `${nameOf(current.backendId)} · indisponível` : nameOf(current.backendId), disabled: true })
        const isCLI = option?.kind === 'cli'
        const documents = documentPhases.has(stage)
        // The documents are written with nobody there to load a model into LM Studio, so one that is not loaded is not offered.
        const listed: IonOption[] = catalog?.state === 'ready' ? catalog.models.map(item => item.unloaded && documents ? { value: item.value, label: `${item.label} · não carregado`, disabled: true } : { value: item.value, label: item.label, disabled: item.disabled }) : []
        const modelOptions: IonOption[] = [{ value: '', label: isCLI ? 'Escolha um modelo' : `Modelo do perfil${profile?.model ? ` · ${profile.model}` : ''}` }, ...listed]
        const notListed = !!current?.modelId && catalog?.state === 'ready' && !listed.some(item => item.value === current.modelId)
        if (current?.modelId && !listed.some(item => item.value === current.modelId)) modelOptions.push({ value: current.modelId, label: notListed ? `${current.modelId} · fora do catálogo` : current.modelId })
        const locked = busy[stage] || disabled || loading || loadFailed || !profiles
        const defaultServes = !defaultBackend || !profiles || phaseCanUse(stage, defaultBackend, profileOf(defaultBackend.id))
        const redraw = redrawn[stage] ?? 0
        return <li key={stage} className={`phase-row${currentStage === stage ? ' is-current' : ''}`} aria-current={currentStage === stage ? 'step' : undefined}>
          <div className="phase-row-heading"><strong>{text.label}</strong><span>{text.what}</span></div>
          <IonPicker id={`phase-executor-${stage}`} label={`Provedor de ${text.label}`} value={current?.backendId ?? ''} onChange={value => chooseExecutor(stage, value)} options={executorOptions} disabled={locked} compact />
          {current && (!option || hasCatalog(current.backendId))
            ? <IonPicker key={`model-${redraw}`} id={`phase-model-${stage}`} label={`Modelo de ${text.label}`} value={current.modelId} onChange={value => chooseModel(stage, value)} options={modelOptions} required={isCLI} searchable disabled={locked || !stillCan} compact />
            : <span className="phase-row-spacer" aria-hidden="true" />}
          <div className="phase-row-status">
            {!current && !defaultBackend && <p className="phase-warning">Não há padrão definido nas Configurações. Escolha um executor para {text.label}.</p>}
            {!current && defaultBackend && defaultServes && <p className="muted">Segue o padrão: {defaultLabel}.</p>}
            {!current && defaultBackend && !defaultServes && <p className="phase-warning">O padrão ({defaultLabel}) não serve a {text.label}. Escolha um executor para esta fase.</p>}
            {current && drafts[stage] && <p className="muted" role="status">Escolha um modelo de {nameOf(current.backendId)} para salvar.</p>}
            {current && !drafts[stage] && stillCan && !notListed && <p className="muted">Salvo · {nameOf(current.backendId)} · {current.modelId || 'modelo do perfil'}</p>}
            {current && !stillCan && <p className="phase-warning" role="alert">{nameOf(current.backendId)} não está disponível para {text.label} agora. Escolha outro executor ou volte ao padrão.</p>}
            {notListed && <p className="phase-warning" role="alert">O catálogo de {nameOf(current!.backendId)} não lista mais {current!.modelId}. Escolha outro modelo.</p>}
            {documents && catalog?.state === 'ready' && catalog.unfiltered && <p className="phase-warning" role="alert">A lista geral da OpenRouter pede uma confirmação a cada uso, e a escolha por fase não pode dá-la. Para escrever {text.label} com este provedor, escolha o modelo na rodada, em Modelos.</p>}
            {catalog?.state === 'loading' && <p className="muted" role="status">Consultando os modelos de {nameOf(current!.backendId)}…</p>}
            {catalog?.state === 'error' && <div role="alert"><p className="phase-warning">{catalog.error}</p><button type="button" className="touch-target text-button" onClick={() => void loadCatalog(option!, true)}>Tentar de novo</button></div>}
            {errors[stage] && <p className="form-error" role="alert">{errors[stage]}</p>}
          </div>
        </li>
      })}</ul>
    </details>
    {loadFailed && <div className="phase-load-failure" role="alert"><p className="phase-warning">Não foi possível ler as escolhas por fase. Enquanto isso, Code, QA e PRs partem do padrão das Configurações, e os documentos seguem o que está salvo no servidor.</p>{onRetry && <button type="button" className="touch-target text-button" onClick={onRetry}>Tentar de novo</button>}</div>}
  </>
}
