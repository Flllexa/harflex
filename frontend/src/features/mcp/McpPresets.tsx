import { useState, type FormEvent } from 'react'
import { GitPullRequest, KeyRound, PlugZap } from 'lucide-react'
import type { MCPServer } from '../../lib/backend'
import { useT } from '../../i18n'
import { credentialFor, mcpPresets, presetOf, type McpPreset } from './presets'

type Props = {
  servers: MCPServer[]
  pendingId?: string
  /** Saves the credential and connects; resolves true once the server is saved, connected or not. */
  onConnect: (preset: McpPreset, credential: string) => Promise<boolean>
  onUpdate: (server: MCPServer, action: 'connect' | 'disable') => void
}

export function McpPresets({ servers, pendingId, onConnect, onUpdate }: Props) {
  const t = useT()
  const [editing, setEditing] = useState<McpPreset['id']>()
  return <section className="mcp-presets" aria-labelledby="mcp-presets-title">
    <div className="destination-heading"><div><h3 id="mcp-presets-title">{t('Servidores prontos')}</h3><p className="muted">{t('GitHub e Bitbucket já vêm configurados: falta só a sua credencial. Com eles o agente abre pull requests sozinho.')}</p></div></div>
    <ul className="mcp-list">{mcpPresets.map(preset => {
      const server = servers.find(item => presetOf(item)?.id === preset.id)
      return <PresetCard key={preset.id} preset={preset} server={server} pendingId={pendingId} editing={editing === preset.id}
        onEdit={open => setEditing(open ? preset.id : undefined)} onConnect={onConnect} onUpdate={onUpdate} />
    })}</ul>
  </section>
}

function PresetCard({ preset, server, pendingId, editing, onEdit, onConnect, onUpdate }: {
  preset: McpPreset; server?: MCPServer; pendingId?: string; editing: boolean; onEdit: (open: boolean) => void
  onConnect: Props['onConnect']; onUpdate: Props['onUpdate']
}) {
  const t = useT()
  const [user, setUser] = useState('')
  const [secret, setSecret] = useState('')
  const basic = preset.authScheme === 'basic'
  const busy = !!pendingId
  const ready = secret.trim() !== '' && (!basic || user.trim() !== '')
  const status = !server ? t('Não configurado') : server.enabled ? t('Ativo') : t('Desativado')

  function close() { setUser(''); setSecret(''); onEdit(false) }
  async function submit(event: FormEvent) {
    event.preventDefault()
    if (!ready || busy) return
    const credential = credentialFor(preset, user, secret)
    setUser(''); setSecret('')
    if (await onConnect(preset, credential)) onEdit(false)
  }

  return <li className="mcp-card mcp-preset" aria-label={preset.name}>
    <div className="destination-heading"><div><h3><GitPullRequest aria-hidden="true" /> {preset.name}</h3><p className="muted">{t(preset.summary)}</p></div><span className={`status-chip${server?.enabled ? ' status-ready' : ''}`}>{status}</span></div>
    {server && <p className="muted">{server.enabled ? t('{count} ferramenta(s) disponíveis para novas conversas', { count: server.tools.length }) : t('Credencial salva; nenhuma ferramenta ativa em novas conversas')}{server.hasCredential ? ` · ${t('Credencial no cofre')}` : ''}</p>}
    {server?.enabled && server.tools.length > 0 && <details className="mcp-preset-tools"><summary className="touch-target">{t('Ver as {count} ferramentas', { count: server.tools.length })}</summary><ul className="mcp-tools">{server.tools.map(tool => <li key={tool.agentName}><strong>{tool.name}</strong><span className="muted">{tool.description || t('Sem descrição fornecida pelo servidor')}</span></li>)}</ul></details>}
    {editing && <form className="mcp-preset-form" onSubmit={submit} aria-label={t('Credencial do {name}', { name: preset.name })}>
      <ol className="mcp-preset-steps">{preset.steps.map(step => <li key={step}>{t(step)}</li>)}</ol>
      {basic && <label className="field">{t('E-mail da conta Atlassian')}<input type="email" autoComplete="off" value={user} onChange={event => setUser(event.target.value)} placeholder="voce@empresa.com" required /></label>}
      <label className="field">{basic ? t('Token de API') : t('Token de acesso pessoal')}<input type="password" autoComplete="new-password" value={secret} onChange={event => setSecret(event.target.value)} placeholder={server?.hasCredential ? t('Cole o novo token para trocar o salvo') : t('Guardado no cofre local')} required /></label>
      <p className="mcp-safety-note"><KeyRound aria-hidden="true" />{t('Salvar valida a conexão e liga as ferramentas para novas conversas. Em cada chamada vale a permissão do projeto: ela pede a sua aprovação, exceto em Acesso total.')}</p>
      <div className="pipeline-actions"><button type="button" className="touch-target secondary-button" onClick={close} disabled={busy}>{t('Cancelar')}</button><button type="submit" className="touch-target primary-button" disabled={!ready || busy}><PlugZap aria-hidden="true" />{busy ? t('Conectando…') : t('Salvar e conectar {name}', { name: preset.name })}</button></div>
    </form>}
    {!editing && <div className="mcp-card-actions">
      {!server && <button type="button" className="touch-target primary-button" onClick={() => onEdit(true)} disabled={busy}><PlugZap aria-hidden="true" />{t('Configurar {name}', { name: preset.name })}</button>}
      {server && !server.enabled && <button type="button" className="touch-target primary-button" onClick={() => onUpdate(server, 'connect')} disabled={busy}><PlugZap aria-hidden="true" />{t('Conectar {name}', { name: preset.name })}</button>}
      {server?.enabled && <button type="button" className="touch-target secondary-button" onClick={() => onUpdate(server, 'disable')} disabled={busy}>{t('Desativar {name}', { name: preset.name })}</button>}
      {server && <button type="button" className="touch-target secondary-button" onClick={() => onEdit(true)} disabled={busy}>{t('Trocar credencial do {name}', { name: preset.name })}</button>}
    </div>}
  </li>
}
