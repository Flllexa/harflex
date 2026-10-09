import { useEffect, useState, type FormEvent } from 'react'
import { KeyRound, Pencil, Plus, ShieldCheck, Server } from 'lucide-react'
import { cliCatalogProblem, errorMessage, isDocumentCLI, type Backend, type BackendOption, type ModelCatalogResult, type ProviderProfile, type Settings, type Workspace, type WorkspaceProfile } from '../../lib/backend'
import { IonPicker, type IonOption } from '../../components/IonPicker'
import type { ConnectionState } from '../../state/session'
import { ProviderDialog } from './ProviderDialog'
import { FullAccessDialog } from '../permissions/FullAccessDialog'
import { currentLocale, locales, setLocale, useT, type Locale } from '../../i18n'

type Props = {
  backend: Backend
  backends: BackendOption[]
  connectionState: ConnectionState
  workspace?: Workspace
  onWorkspaceUpdated: (workspace: Workspace) => void
  onBackendSaved: (backend: BackendOption) => void
  onDefaultSaved: (id: string) => void
}

export function SettingsPage({ backend, backends, connectionState, workspace, onWorkspaceUpdated, onBackendSaved, onDefaultSaved }: Props) {
  const t = useT()
  const [profiles, setProfiles] = useState<ProviderProfile[]>([])
  const [settings, setSettings] = useState<Settings>({ defaultBackendId: '', defaultModelBackendId: '', defaultModelId: '' })
  const [defaultId, setDefaultId] = useState('')
  const [defaultModelBackendId, setDefaultModelBackendId] = useState('')
  const [defaultModelId, setDefaultModelId] = useState('')
  const [modelCatalog, setModelCatalog] = useState<ModelCatalogResult>()
  const [catalogState, setCatalogState] = useState<'idle' | 'loading' | 'ready' | 'error'>('idle')
  const [catalogError, setCatalogError] = useState('')
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const [notice, setNotice] = useState<string>()
  const [confirmingFullAccess, setConfirmingFullAccess] = useState(false)
  const [editor, setEditor] = useState<ProviderProfile | null | undefined>()
  const [reloadRevision, setReloadRevision] = useState(0)

  useEffect(() => {
    if (connectionState === 'connecting') {
      setState('loading')
      return
    }
    let current = true
    setState('loading')
    void (async () => {
      try {
        const [nextProfiles, nextSettings] = await Promise.all([backend.listProviderProfiles(), backend.getSettings()])
        if (!current) return
        setProfiles(nextProfiles)
        setDefaultId(nextSettings.defaultBackendId)
        const profile = nextProfiles.find(item => item.id === nextSettings.defaultBackendId)
        const selectedBackend = backends.find(item => item.id === nextSettings.defaultBackendId)
        const providerCatalogAvailable = connectionState === 'ready' || connectionState === 'degraded'
        const supportsSDD = providerCatalogAvailable && selectedBackend?.available && (isDocumentCLI(nextSettings.defaultBackendId) || (selectedBackend.kind === 'api' && !!profile && !profile.endpointBlocked && profile.providerType !== 'generic' && (profile.hasCredential || profile.providerType === 'lm_studio' || profile.providerType === 'ollama')))
        const savedModelMatchesProvider = nextSettings.defaultModelBackendId === nextSettings.defaultBackendId
        const savedModel = savedModelMatchesProvider && (!providerCatalogAvailable || supportsSDD) ? nextSettings.defaultModelId : ''
        const configuredModel = supportsSDD ? profile?.model ?? '' : ''
        setSettings({ ...nextSettings, defaultModelBackendId: savedModel ? nextSettings.defaultModelBackendId : '', defaultModelId: savedModel })
        setDefaultModelBackendId(savedModel ? nextSettings.defaultModelBackendId : '')
        setDefaultModelId(savedModel || configuredModel)
        setModelCatalog(undefined)
        setCatalogState('idle')
        setCatalogError('')
        setState('ready')
      } catch {
        if (current) setState('error')
      }
    })()
    return () => { current = false }
  }, [backend, backends, connectionState, reloadRevision])

  function load() { setReloadRevision(revision => revision + 1) }

  async function save(event: FormEvent) {
    event.preventDefault()
    setSaving(true)
    setError(undefined)
    setNotice(undefined)
    try {
      const next = await backend.saveSettings({ defaultBackendId: defaultId, defaultModelBackendId: defaultModelId ? defaultId : '', defaultModelId })
      setSettings(next)
      setDefaultModelBackendId(next.defaultModelBackendId)
      setDefaultModelId(next.defaultModelId)
      onDefaultSaved(next.defaultBackendId)
      setNotice(t('Preferências salvas nesta máquina.'))
    } catch (failure) { setError(errorMessage(failure)) }
    finally { setSaving(false) }
  }

  async function refreshDefaultModelCatalog() {
    const selectedBackend = backends.find(item => item.id === defaultId)
    if (!selectedBackend) { setCatalogError(t('Escolha o provedor padrão antes de atualizar os modelos.')); return }
    if (!supportsSDDModelDefault) { setCatalogError(t('Este provedor não é compatível com modelos padrão do SDD. Escolha o Codex CLI, o Claude Code ou um perfil de API compatível.')); return }
    if (selectedBackend.kind === 'cli' && !workspace) { setCatalogError(t('Abra um projeto para consultar os modelos locais de {name}.', { name: selectedBackend.name })); return }
    setCatalogState('loading')
    setCatalogError('')
    try {
      const result = selectedBackend.kind === 'cli'
        ? await backend.queryCLIModelCatalog({ workspaceId: workspace!.id, backendId: selectedBackend.id })
        : await backend.queryHTTPModelCatalog({ profileId: selectedBackend.id, searchTerm: '', refresh: true })
      setModelCatalog(result)
      if (!result.complete || result.status !== 'complete') {
        setCatalogState('error')
        setCatalogError(selectedBackend.kind === 'cli' ? cliCatalogProblem(result, selectedBackend.name) : t('Não foi possível confirmar o catálogo deste provedor.'))
        return
      }
      setCatalogState('ready')
      if (defaultModelId && !result.models.some(item => item.id === defaultModelId && item.backendId === selectedBackend.id && item.availability !== 'unavailable')) {
        setCatalogError(t('O modelo padrão salvo não aparece neste catálogo. Escolha outro para evitar uma troca silenciosa.'))
      }
    } catch (failure) {
      setCatalogState('error')
      setCatalogError(errorMessage(failure))
    }
  }

  const selectedBackend = backends.find(item => item.id === defaultId)
  const selectedProfile = profiles.find(item => item.id === defaultId)
  const providerCatalogAvailable = connectionState === 'ready' || connectionState === 'degraded'
  const supportsSDDModelDefault = providerCatalogAvailable && !!selectedBackend?.available && (isDocumentCLI(defaultId) || (selectedBackend.kind === 'api' && !!selectedProfile && !selectedProfile.endpointBlocked && selectedProfile.providerType !== 'generic' && (selectedProfile.hasCredential || selectedProfile.providerType === 'lm_studio' || selectedProfile.providerType === 'ollama')))
  const catalogModels = catalogState === 'ready' && modelCatalog?.backendId === defaultId
    ? modelCatalog.models.filter(item => item.backendId === defaultId && item.availability !== 'unavailable').map(item => ({ value: item.id, label: item.displayName || item.id }))
    : []
  const modelOptions: IonOption[] = [{ value: '', label: selectedBackend ? t('Escolha um modelo') : t('Escolha o provedor primeiro') }, ...catalogModels]
  if (selectedProfile?.model && !modelOptions.some(item => item.value === selectedProfile.model)) modelOptions.push({ value: selectedProfile.model, label: t('{model} · configurado no provedor', { model: selectedProfile.model }) })
  if (defaultModelId && !modelOptions.some(item => item.value === defaultModelId)) modelOptions.push({ value: defaultModelId, label: t('{model} · atualize o catálogo para confirmar', { model: defaultModelId }), disabled: true })
  const settingsUnchanged = defaultId === settings.defaultBackendId && defaultModelId === settings.defaultModelId && (defaultModelId ? defaultModelBackendId === defaultId : !settings.defaultModelBackendId)

  async function changePolicy(profile: WorkspaceProfile, confirmFullAccess = false) {
    if (!workspace || saving) return
    setSaving(true)
    setError(undefined)
    setNotice(undefined)
    try {
      const updated = await backend.setWorkspaceProfile(workspace.id, profile, confirmFullAccess ? { confirmFullAccess } : undefined)
      onWorkspaceUpdated(updated)
      setConfirmingFullAccess(false)
      setNotice(t('Permissões do projeto salvas. Valem também para as conversas já abertas.'))
    } catch (failure) { setError(errorMessage(failure)) }
    finally { setSaving(false) }
  }

  return <div className="settings-page">
    <div className="destination-heading"><div><h2>{t('Configurações do Harflex')}</h2><p className="muted">{t('Provedores, preferência de execução e limites locais.')}</p></div></div>
    {state === 'loading' && <p role="status" className="muted">{t('Carregando configurações…')}</p>}
    {state === 'error' && <div className="inline-error" role="alert"><p>{t('Não foi possível carregar as configurações.')}</p><button type="button" className="touch-target secondary-button" onClick={() => void load()}>{t('Tentar novamente')}</button></div>}
    {state === 'ready' && <>
      <section className="settings-section" aria-labelledby="language-heading">
        <div className="destination-heading"><div><h3 id="language-heading">Idioma</h3><p className="muted">Vale para a interface inteira. O texto dos seus pedidos e das respostas da IA não muda.</p></div></div>
        <IonPicker id="app-language" label="Idioma da interface" value={currentLocale()} onChange={value => setLocale(value as Locale)} options={locales.map(item => ({ value: item.value, label: item.label }))} />
      </section>
      <section className="settings-section" aria-labelledby="providers-heading">
        <div className="destination-heading"><div><h3 id="providers-heading">{t('Provedores de IA')}</h3><p className="muted">{t('Chaves guardadas no cofre do sistema. Endpoints configurados por você.')}</p></div><button type="button" className="touch-target secondary-button" onClick={() => setEditor(null)}><Plus aria-hidden="true" />{t('Adicionar provedor')}</button></div>
        {profiles.length === 0 ? <div className="catalog-empty"><Server aria-hidden="true" /><strong>{t('Nenhum provedor de API')}</strong><span className="muted">{t('Adicione uma API compatível ou use um CLI detectado.')}</span></div>
          : <ul className="settings-provider-list">{profiles.map(profile => <li key={profile.id} className="settings-provider-row">
            <span className="settings-provider-icon"><KeyRound aria-hidden="true" /></span><div className="settings-provider-info"><strong>{profile.name}</strong><span className="mono">{profile.model}</span><span className="muted mono settings-provider-url">{profile.baseUrl}</span></div>
            <span className={`status-chip${profile.endpointBlocked ? ' status-unavailable' : profile.hasCredential ? ' status-ready' : ''}`}>{profile.endpointBlocked ? t('HTTPS necessário') : profile.hasCredential ? t('Credencial no cofre') : t('Sem chave')}</span>
            <button type="button" className="touch-target secondary-button" onClick={() => setEditor(profile)} aria-label={t('Editar {name}', { name: profile.name })}><Pencil aria-hidden="true" />{t('Editar')}</button>
          </li>)}</ul>}
      </section>
      <form className="settings-section" onSubmit={save} aria-labelledby="preferences-heading">
        <div className="destination-heading"><div><h3 id="preferences-heading">{t('Preferências')}</h3><p className="muted">{t('Usadas ao iniciar um novo trabalho.')}</p></div></div>
        <IonPicker id="settings-default-backend" label={t('Provedor padrão')} value={defaultId} onChange={value => {
          setDefaultId(value)
          const profile = profiles.find(item => item.id === value)
          const backend = backends.find(item => item.id === value)
          const supported = !!backend?.available && (isDocumentCLI(value) || (backend.kind === 'api' && !!profile && !profile.endpointBlocked && profile.providerType !== 'generic' && (profile.hasCredential || profile.providerType === 'lm_studio' || profile.providerType === 'ollama')))
          setDefaultModelBackendId('')
          setDefaultModelId(supported ? profile?.model ?? '' : '')
          setModelCatalog(undefined)
          setCatalogState('idle')
          setCatalogError('')
        }} options={[{ value: '', label: t('Escolher ao iniciar') }, ...backends.map(item => ({ value: item.id, label: item.available ? item.name : t('{name} · indisponível', { name: item.name }), disabled: !item.available }))]} searchable={backends.length > 6} />
        <div className="settings-default-model">
          <IonPicker id="settings-default-model" label={t('Modelo padrão do SDD')} value={defaultModelId} onChange={setDefaultModelId} options={modelOptions} searchable disabled={!supportsSDDModelDefault || catalogState === 'loading'} />
          <p className="muted">{!providerCatalogAvailable ? t('Não foi possível confirmar os provedores. O modelo salvo foi preservado até a conexão voltar.') : !defaultId ? t('Escolha um provedor para definir o modelo padrão.') : !supportsSDDModelDefault ? t('Este provedor padrão não é compatível com o SDD. Escolha Codex CLI ou um perfil de API compatível para definir este padrão.') : selectedBackend?.kind === 'cli' ? t('Selecione um modelo descoberto pelo Codex CLI local.') : t('Padrão atual do provedor: {model}.', { model: selectedProfile?.model || t('nenhum modelo configurado') })}</p>
          <button type="button" className="touch-target text-button" disabled={!supportsSDDModelDefault || catalogState === 'loading'} onClick={() => void refreshDefaultModelCatalog()}>{catalogState === 'loading' ? t('Consultando catálogo…') : t('Atualizar modelos')}</button>
          {catalogState === 'ready' && <span className="muted" role="status">{t('Catálogo confirmado')} · {selectedBackend?.kind === 'cli' ? t('{name} local', { name: selectedBackend.name }) : modelCatalog?.source}</span>}
          {catalogError && <p className="form-error" role="alert">{catalogError}</p>}
        </div>
        <div className="settings-actions"><span className="muted">{t('Atual:')} {settings.defaultBackendId || t('escolher ao iniciar')}{settings.defaultModelId && ` · ${settings.defaultModelId}`}</span><button type="submit" className="touch-target primary-button" disabled={saving || settingsUnchanged}>{t('Salvar preferências')}</button></div>
      </form>
      <section className="settings-section" aria-labelledby="security-heading">
        <div className="destination-heading"><div><h3 id="security-heading">{t('Permissões do projeto')}</h3><p className="muted">{t('Valem para todas as conversas deste projeto, inclusive as já abertas.')}</p></div><ShieldCheck aria-hidden="true" className="settings-section-icon" /></div>
        {!workspace ? <p className="muted">{t('Abra um projeto para definir suas permissões.')}</p> : <div className="policy-options">
          <label className="policy-option"><input type="radio" name="workspace-profile" checked={workspace.profile === 'ask'} onChange={() => void changePolicy('ask')} disabled={saving} /><span><strong>{t('Perguntar')}</strong><small>{t('Solicitar aprovação para escrita, shell e rede.')}</small></span></label>
          <label className="policy-option"><input type="radio" name="workspace-profile" checked={workspace.profile === 'trusted_workspace'} onChange={() => void changePolicy('trusted_workspace')} disabled={saving} /><span><strong>{t('Workspace confiável')}</strong><small>{t('Permitir escrita nesta pasta; shell e rede ainda pedem aprovação.')}</small></span></label>
          <label className="policy-option"><input type="radio" name="workspace-profile" checked={workspace.profile === 'full_access'} onChange={() => setConfirmingFullAccess(true)} disabled={saving} /><span><strong>{t('Acesso total')}</strong><small>{t('Escrever, executar comandos e usar ferramentas de rede sem pedir aprovação. Pede confirmação ao ativar.')}</small></span></label>
          <label className="policy-option is-disabled"><input type="radio" name="workspace-profile" checked={false} disabled /><span><strong>Sandbox</strong><small>{t('Indisponível até haver isolamento verificado nesta plataforma.')}</small></span></label>
        </div>}
      </section>
    </>}
    {error && !confirmingFullAccess && <p className="form-error" role="alert">{error}</p>}
    {notice && <p className="form-success" role="status">{notice}</p>}
    {confirmingFullAccess && <FullAccessDialog pending={saving} error={error} onConfirm={() => void changePolicy('full_access', true)} onClose={() => { setConfirmingFullAccess(false); setError(undefined) }} />}
    {editor !== undefined && <ProviderDialog key={editor?.id ?? 'new'} backend={backend} initial={editor ?? undefined} onClose={() => setEditor(undefined)} onSaved={saved => { onBackendSaved(saved); setEditor(undefined); load() }} />}
  </div>
}
