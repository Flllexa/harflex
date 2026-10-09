import { useEffect, useRef, useState } from 'react'
import { Activity, Database, KeyRound, Network, RefreshCw, TerminalSquare } from 'lucide-react'
import type { Backend, BackendOption, CredentialProbe, MCPServer, ProviderProfile } from '../../lib/backend'
import { useT } from '../../i18n'

type Props = { backend: Backend; workspaceId?: string; onSettings: () => void; onMCP: () => void; onProjects: () => void }
type Result<T> = { ok: true; value: T } | { ok: false }
const check = async <T,>(request: Promise<T>): Promise<Result<T>> => {
  try { return { ok: true, value: await request } }
  catch { return { ok: false } }
}

export function DiagnosticsPage({ backend, workspaceId, onSettings, onMCP, onProjects }: Props) {
  const t = useT()
  const [loading, setLoading] = useState(true)
  const [workspaces, setWorkspaces] = useState<Result<unknown[]>>()
  const [backends, setBackends] = useState<Result<BackendOption[]>>()
  const [profiles, setProfiles] = useState<Result<ProviderProfile[]>>()
  const [credentialProbe, setCredentialProbe] = useState<Result<CredentialProbe>>()
  const [mcp, setMCP] = useState<Result<MCPServer[]>>()
  const request = useRef(0)

  async function refresh() {
    const current = ++request.current
    setLoading(true)
    const [workspaceResult, backendResult, profileResult, credentialResult, mcpResult] = await Promise.all([
      check(backend.listWorkspaces()), check(backend.listBackends()), check(backend.listProviderProfiles()),
      check(backend.probeCredentialStore(workspaceId ?? '')),
      workspaceId ? check(backend.listMCPServers(workspaceId)) : Promise.resolve(undefined),
    ])
    if (current !== request.current) return
    setWorkspaces(workspaceResult); setBackends(backendResult); setProfiles(profileResult); setCredentialProbe(credentialResult); setMCP(mcpResult)
    setLoading(false)
  }
  useEffect(() => { void refresh(); return () => { request.current++ } }, [backend, workspaceId])

  const clis = backends?.ok ? backends.value.filter(item => item.kind === 'cli') : []
  const configuredProviders = profiles?.ok ? profiles.value : []
  const configuredMCP = mcp?.ok ? mcp.value : []
  const issueCount = [workspaces, backends, profiles, credentialProbe, workspaceId ? mcp : undefined].filter(value => value && !value.ok).length + (credentialProbe?.ok && ['degraded', 'unavailable'].includes(credentialProbe.value.status) ? 1 : 0)

  return <div className="diagnostics-page">
    <div className="destination-heading"><div><h2>{t('Diagnóstico do runtime')}</h2><p className="muted">{t('Leituras locais de disponibilidade. Nenhum teste de conexão externa é feito automaticamente.')}</p></div><button type="button" className="touch-target secondary-button" onClick={() => void refresh()} disabled={loading}><RefreshCw aria-hidden="true" />{t('Atualizar')}</button></div>
    {loading ? <p role="status" className="muted">{t('Verificando componentes locais…')}</p> : <>
      <div className="diagnostics-summary"><Activity aria-hidden="true" /><span>{issueCount === 0 ? t('Consultas locais concluídas') : t('{count} consulta(s) local(is) falharam', { count: issueCount })}</span></div>
      <div className="diagnostics-grid">
        <section className="diagnostic-card"><Database aria-hidden="true" /><div><h3>{t('Armazenamento local')}</h3><strong className={workspaces?.ok ? 'signal-ok' : 'signal-error'}>{workspaces?.ok ? t('Operacional') : t('Falha na leitura')}</strong><p className="muted">{workspaces?.ok ? t('{count} projeto(s) registrados no catálogo SQLite.', { count: workspaces.value.length }) : t('Não foi possível ler o catálogo. Reinicie o app e confira o armazenamento local.')}</p></div><button type="button" className="touch-target secondary-button" onClick={onProjects}>{t('Ver projetos')}</button></section>
        <section className="diagnostic-card"><TerminalSquare aria-hidden="true" /><div><h3>{t('Agentes CLI')}</h3>{backends?.ok ? <><strong>{t('{available} de {total} disponíveis', { available: clis.filter(item => item.available).length, total: clis.length })}</strong><ul className="diagnostic-items">{clis.map(item => <li key={item.id}><span>{item.name}</span><span className={item.available ? 'signal-ok' : 'muted'}>{item.available ? t('Detectado') : t('Não detectado')}</span></li>)}</ul></> : <p className="signal-error">{t('Não foi possível consultar os backends.')}</p>}<p className="muted">{t('Detecção do executável nesta máquina; não valida autenticação ou permissões do CLI.')}</p></div><button type="button" className="touch-target secondary-button" onClick={onSettings}>{t('Configurar IA')}</button></section>
        <section className="diagnostic-card"><KeyRound aria-hidden="true" /><div><h3>{t('Cofre local')}</h3>{credentialProbe?.ok ? <><strong className={['degraded', 'unavailable'].includes(credentialProbe.value.status) ? 'signal-error' : ''}>{credentialProbe.value.status === 'ready' ? t('{count} referência(s) legíveis', { count: credentialProbe.value.checked }) : credentialProbe.value.status === 'unconfigured' ? t('Nenhuma referência para testar') : credentialProbe.value.status === 'degraded' ? t('{count} referência(s) ausente(s)', { count: credentialProbe.value.missing }) : t('Cofre indisponível')}</strong><p className="muted">{t('Leitura das referências ativas de provedores e MCP. Valores não saem do backend; autenticação na API não é testada.')}</p></> : <p className="signal-error">{t('Não foi possível consultar o cofre.')}</p>}</div><button type="button" className="touch-target secondary-button" onClick={onSettings}>{t('Ver configurações')}</button></section>
        <section className="diagnostic-card"><KeyRound aria-hidden="true" /><div><h3>{t('Perfis de IA')}</h3>{profiles?.ok ? <><strong>{t('{count} perfil(is) configurado(s)', { count: configuredProviders.length })}</strong><p className="muted">{t('{count} com referência de credencial. Disponibilidade do modelo e acesso à API não foram testados.', { count: configuredProviders.filter(item => item.hasCredential).length })}</p></> : <p className="signal-error">{t('Não foi possível consultar os perfis.')}</p>}</div><button type="button" className="touch-target secondary-button" onClick={onSettings}>{t('Gerenciar perfis')}</button></section>
        <section className="diagnostic-card"><Network aria-hidden="true" /><div><h3>{t('Servidores MCP')}</h3>{!workspaceId ? <p className="muted">{t('Abra um projeto para ver os servidores cadastrados.')}</p> : mcp?.ok ? <><strong>{t('{active} de {total} ativos', { active: configuredMCP.filter(item => item.enabled).length, total: configuredMCP.length })}</strong><p className="muted">{t('Estado da última descoberta; a disponibilidade atual só é testada ao conectar ou chamar uma ferramenta.')}</p></> : <p className="signal-error">{t('Não foi possível ler os servidores deste projeto.')}</p>}</div><button type="button" className="touch-target secondary-button" onClick={workspaceId ? onMCP : onProjects}>{workspaceId ? t('Ver MCP') : t('Abrir projeto')}</button></section>
      </div>
    </>}
  </div>
}
