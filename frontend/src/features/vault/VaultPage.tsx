import { useEffect, useRef, useState } from 'react'
import { KeyRound, LockKeyhole, RefreshCw } from 'lucide-react'
import type { Backend, MCPServer, ProviderProfile } from '../../lib/backend'
import { useT } from '../../i18n'

type Props = { backend: Backend; workspaceId?: string; onSettings: () => void; onMCP: () => void; onProjects: () => void }

export function VaultPage({ backend, workspaceId, onSettings, onMCP, onProjects }: Props) {
  const t = useT()
  const [providers, setProviders] = useState<ProviderProfile[]>([])
  const [servers, setServers] = useState<MCPServer[]>([])
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading')
  const generation = useRef(0)

  async function refresh() {
    const current = ++generation.current
    setState('loading')
    try {
      const [profiles, mcp] = await Promise.all([backend.listProviderProfiles(), workspaceId ? backend.listMCPServers(workspaceId) : Promise.resolve([])])
      if (current !== generation.current) return
      setProviders(profiles); setServers(mcp); setState('ready')
    } catch { if (current === generation.current) setState('error') }
  }
  useEffect(() => { void refresh(); return () => { generation.current++ } }, [backend, workspaceId])

  const total = providers.filter(item => item.hasCredential).length + servers.filter(item => item.hasCredential).length
  return <div className="vault-page">
    <div className="destination-heading"><div><h2>{t('Vault de referências')}</h2><p className="muted">{t('Inventário das credenciais usadas pelo Harflex; valores nunca são exibidos aqui.')}</p></div><button type="button" className="touch-target secondary-button" onClick={() => void refresh()} disabled={state === 'loading'}><RefreshCw aria-hidden="true" />{t('Atualizar')}</button></div>
    {state === 'loading' ? <p className="muted" role="status">{t('Lendo referências locais…')}</p> : state === 'error' ? <div className="inline-error" role="alert"><p>{t('Não foi possível ler as referências.')}</p><button type="button" className="touch-target secondary-button" onClick={() => void refresh()}>{t('Tentar novamente')}</button></div> : <>
      <div className="vault-summary"><LockKeyhole aria-hidden="true" /><div><strong>{t('{count} referência(s) de credencial cadastrada(s)', { count: total })}</strong><p className="muted">{t('A existência da referência não comprova que a chave ainda funciona. Para trocar o valor, abra a configuração do recurso.')}</p></div></div>
      <div className="vault-grid">
        <section className="vault-group"><div className="destination-heading"><div><h3>{t('Provedores de IA')}</h3><p className="muted">{t('Perfis globais usados em sessões nativas.')}</p></div><KeyRound aria-hidden="true" /></div>{providers.length === 0 ? <p className="muted">{t('Nenhum provedor configurado.')}</p> : <ul>{providers.map(item => <li key={item.id}><div><strong>{item.name}</strong><span className="muted">{item.model} · {item.id}</span></div><span className={`status-chip${item.hasCredential ? ' status-ready' : ''}`}>{item.hasCredential ? t('Referência salva') : t('Sem credencial')}</span></li>)}</ul>}<button type="button" className="touch-target secondary-button" onClick={onSettings}>{t('Gerenciar provedores')}</button></section>
        <section className="vault-group"><div className="destination-heading"><div><h3>{t('Servidores MCP')}</h3><p className="muted">{t('Tokens vinculados ao projeto atual.')}</p></div><KeyRound aria-hidden="true" /></div>{!workspaceId ? <div><p className="muted">{t('Abra um projeto para consultar tokens MCP.')}</p><button type="button" className="touch-target secondary-button" onClick={onProjects}>{t('Abrir projetos')}</button></div> : <>{servers.length === 0 ? <p className="muted">{t('Nenhum servidor neste projeto.')}</p> : <ul>{servers.map(item => <li key={item.id}><div><strong>{item.name}</strong><span className="muted">{item.transport === 'http' ? 'HTTP' : t('Processo local')}</span></div><span className={`status-chip${item.hasCredential ? ' status-ready' : ''}`}>{item.hasCredential ? t('Referência salva') : t('Sem token')}</span></li>)}</ul>}<button type="button" className="touch-target secondary-button" onClick={onMCP}>{t('Gerenciar MCP')}</button></>}</section>
      </div>
    </>}
  </div>
}
