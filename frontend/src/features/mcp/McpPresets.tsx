import { useState, type FormEvent } from 'react'
import { GitPullRequest, KeyRound, PlugZap } from 'lucide-react'
import type { MCPServer } from '../../lib/backend'
import { credentialFor, mcpPresets, presetOf, type McpPreset } from './presets'

type Props = {
  servers: MCPServer[]
  pendingId?: string
  /** Saves the credential and connects; resolves true once the server is saved, connected or not. */
  onConnect: (preset: McpPreset, credential: string) => Promise<boolean>
  onUpdate: (server: MCPServer, action: 'connect' | 'disable') => void
}

export function McpPresets({ servers, pendingId, onConnect, onUpdate }: Props) {
  const [editing, setEditing] = useState<McpPreset['id']>()
  return <section className="mcp-presets" aria-labelledby="mcp-presets-title">
    <div className="destination-heading"><div><h3 id="mcp-presets-title">Servidores prontos</h3><p className="muted">GitHub e Bitbucket já vêm configurados: falta só a sua credencial. Com eles o agente abre pull requests sozinho.</p></div></div>
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
  const [user, setUser] = useState('')
  const [secret, setSecret] = useState('')
  const basic = preset.authScheme === 'basic'
  const busy = !!pendingId
  const ready = secret.trim() !== '' && (!basic || user.trim() !== '')
  const status = !server ? 'Não configurado' : server.enabled ? 'Ativo' : 'Desativado'

  function close() { setUser(''); setSecret(''); onEdit(false) }
  async function submit(event: FormEvent) {
    event.preventDefault()
    if (!ready || busy) return
    const credential = credentialFor(preset, user, secret)
    setUser(''); setSecret('')
    if (await onConnect(preset, credential)) onEdit(false)
  }

  return <li className="mcp-card mcp-preset" aria-label={preset.name}>
    <div className="destination-heading"><div><h3><GitPullRequest aria-hidden="true" /> {preset.name}</h3><p className="muted">{preset.summary}</p></div><span className={`status-chip${server?.enabled ? ' status-ready' : ''}`}>{status}</span></div>
    {server && <p className="muted">{server.enabled ? `${server.tools.length} ferramenta(s) disponíveis para novas conversas` : 'Credencial salva; nenhuma ferramenta ativa em novas conversas'}{server.hasCredential ? ' · Credencial no cofre' : ''}</p>}
    {server?.enabled && server.tools.length > 0 && <details className="mcp-preset-tools"><summary className="touch-target">Ver as {server.tools.length} ferramentas</summary><ul className="mcp-tools">{server.tools.map(tool => <li key={tool.agentName}><strong>{tool.name}</strong><span className="muted">{tool.description || 'Sem descrição fornecida pelo servidor'}</span></li>)}</ul></details>}
    {editing && <form className="mcp-preset-form" onSubmit={submit} aria-label={`Credencial do ${preset.name}`}>
      <ol className="mcp-preset-steps">{preset.steps.map(step => <li key={step}>{step}</li>)}</ol>
      {basic && <label className="field">E-mail da conta Atlassian<input type="email" autoComplete="off" value={user} onChange={event => setUser(event.target.value)} placeholder="voce@empresa.com" required /></label>}
      <label className="field">{basic ? 'Token de API' : 'Token de acesso pessoal'}<input type="password" autoComplete="new-password" value={secret} onChange={event => setSecret(event.target.value)} placeholder={server?.hasCredential ? 'Cole o novo token para trocar o salvo' : 'Guardado no cofre local'} required /></label>
      <p className="mcp-safety-note"><KeyRound aria-hidden="true" />Salvar valida a conexão e liga as ferramentas para novas conversas. Em cada chamada vale a permissão do projeto: ela pede a sua aprovação, exceto em Acesso total.</p>
      <div className="pipeline-actions"><button type="button" className="touch-target secondary-button" onClick={close} disabled={busy}>Cancelar</button><button type="submit" className="touch-target primary-button" disabled={!ready || busy}><PlugZap aria-hidden="true" />{busy ? 'Conectando…' : `Salvar e conectar ${preset.name}`}</button></div>
    </form>}
    {!editing && <div className="mcp-card-actions">
      {!server && <button type="button" className="touch-target primary-button" onClick={() => onEdit(true)} disabled={busy}><PlugZap aria-hidden="true" />Configurar {preset.name}</button>}
      {server && !server.enabled && <button type="button" className="touch-target primary-button" onClick={() => onUpdate(server, 'connect')} disabled={busy}><PlugZap aria-hidden="true" />Conectar {preset.name}</button>}
      {server?.enabled && <button type="button" className="touch-target secondary-button" onClick={() => onUpdate(server, 'disable')} disabled={busy}>Desativar {preset.name}</button>}
      {server && <button type="button" className="touch-target secondary-button" onClick={() => onEdit(true)} disabled={busy}>Trocar credencial do {preset.name}</button>}
    </div>}
  </li>
}
