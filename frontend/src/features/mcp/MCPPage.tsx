import { useEffect, useState, type FormEvent } from 'react'
import { Cable, FolderOpen, PlugZap, RefreshCw, ShieldCheck } from 'lucide-react'
import { IonPicker } from '../../components/IonPicker'
import { errorMessage, type Backend, type MCPServer } from '../../lib/backend'
import { CreateToggle, useCreateForm } from '../../components/CreateForm'
import { McpPresets } from './McpPresets'
import { presetOf, type McpPreset } from './presets'

type Props = { backend: Backend; workspaceId?: string; onProjects: () => void }

export function MCPPage({ backend, workspaceId, onProjects }: Props) {
  const [servers, setServers] = useState<MCPServer[]>([])
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [editingId, setEditingId] = useState<string>()
  const [name, setName] = useState('')
  const [transport, setTransport] = useState<'http' | 'stdio'>('http')
  const [url, setUrl] = useState('')
  const [command, setCommand] = useState('')
  const [args, setArgs] = useState('')
  const [tokenEnvVar, setTokenEnvVar] = useState('')
  const [authScheme, setAuthScheme] = useState<'bearer' | 'basic'>('bearer')
  const [token, setToken] = useState('')
  const [pendingId, setPendingId] = useState<string>()
  const [error, setError] = useState<string>()
  const [notice, setNotice] = useState<string>()
  const form = useCreateForm(state === 'ready', servers.length)
  // GitHub and Bitbucket are managed from their own cards; the list below holds every other server.
  const custom = servers.filter(server => !presetOf(server))

  async function refresh() {
    if (!workspaceId) return
    setState('loading')
    try { setServers(await backend.listMCPServers(workspaceId)); setState('ready') }
    catch { setState('error') }
  }
  useEffect(() => { void refresh() }, [backend, workspaceId])

  function reset() { setEditingId(undefined); setName(''); setTransport('http'); setUrl(''); setCommand(''); setArgs(''); setTokenEnvVar(''); setAuthScheme('bearer'); setToken('') }
  function edit(server: MCPServer) {
    setEditingId(server.id); setName(server.name); setTransport(server.transport); setUrl(server.url)
    setCommand(server.command); setArgs(server.args.join('\n')); setTokenEnvVar(server.tokenEnvVar); setAuthScheme(server.authScheme); setToken('')
    setError(undefined); setNotice(undefined); form.setOpen(true)
  }
  function upsert(server: MCPServer) { setServers(current => [server, ...current.filter(item => item.id !== server.id)]) }

  async function save(event: FormEvent) {
    event.preventDefault()
    if (!workspaceId || pendingId) return
    setPendingId('save'); setError(undefined); setNotice(undefined)
    try {
      const server = await backend.saveMCPServer({ id: editingId, workspaceId, name: name.trim(), transport,
        url: transport === 'http' ? url.trim() : '', command: transport === 'stdio' ? command.trim() : '',
        args: transport === 'stdio' ? args.split('\n').map(value => value.trim()).filter(Boolean) : [],
        tokenEnvVar: transport === 'stdio' ? tokenEnvVar.trim() : '', authScheme: transport === 'http' ? authScheme : 'bearer', token })
      upsert(server); reset(); form.setOpen(false)
      setNotice(`${server.name} salvo e desativado. Conecte explicitamente para descobrir as ferramentas.`)
    } catch (failure) { setError(errorMessage(failure)) }
    finally { setToken(''); setPendingId(undefined) }
  }

  async function update(server: MCPServer, action: 'connect' | 'disable') {
    if (pendingId) return
    setPendingId(server.id); setError(undefined); setNotice(undefined)
    try {
      const updated = action === 'connect' ? await backend.connectMCPServer(server.id) : await backend.disableMCPServer(server.id)
      upsert(updated)
      setNotice(action === 'connect' ? `${updated.name}: conexão validada e ${updated.tools.length} ferramenta(s) descoberta(s).` : `${updated.name} desativado para novas sessões.`)
    } catch (failure) { setError(errorMessage(failure)) }
    finally { setPendingId(undefined) }
  }

  /** Saves the preset's credential and connects in one go. The server stays saved when the connection fails, so a retry is one click; it resolves false only when nothing was saved. */
  async function connectPreset(preset: McpPreset, credential: string): Promise<boolean> {
    if (!workspaceId || pendingId) return false
    setPendingId(preset.id); setError(undefined); setNotice(undefined)
    const existing = servers.find(server => presetOf(server)?.id === preset.id)
    try {
      const saved = await backend.saveMCPServer({ id: existing?.id, workspaceId, name: existing?.name ?? preset.name, transport: 'http', url: preset.url, command: '', args: [], tokenEnvVar: '', authScheme: preset.authScheme, token: credential })
      upsert(saved)
      try {
        const connected = await backend.connectMCPServer(saved.id)
        upsert(connected)
        setNotice(`${connected.name}: conexão validada e ${connected.tools.length} ferramenta(s) descoberta(s). Valem para as próximas conversas.`)
      } catch (failure) {
        setError(`${preset.name} foi salvo, mas a conexão falhou. ${errorMessage(failure)} ${preset.trouble}`)
      }
      return true
    } catch (failure) { setError(errorMessage(failure)); return false }
    finally { setPendingId(undefined) }
  }

  const basic = transport === 'http' && authScheme === 'basic'
  return <div className="mcp-page">
    <div className="destination-heading"><div><h2>Servidores MCP</h2><p className="muted">Conectores locais ou HTTPS, ativados somente após sua confirmação.</p></div>{workspaceId && <div className="pipeline-actions">{form.collapsible && <CreateToggle open={form.open} onToggle={() => { if (form.open) reset(); form.setOpen(!form.open) }} label="Novo servidor" />}<button type="button" className="touch-target secondary-button" onClick={() => void refresh()} disabled={state === 'loading' || !!pendingId}><RefreshCw aria-hidden="true" />Atualizar</button></div>}</div>
    {!workspaceId ? <div className="catalog-empty"><FolderOpen aria-hidden="true" /><strong>Abra um projeto para conectar ferramentas</strong><span className="muted">Cada servidor MCP pertence ao projeto escolhido.</span><button type="button" className="touch-target primary-button" onClick={onProjects}>Abrir projetos</button></div> : <>
      {state === 'ready' && <McpPresets servers={servers} pendingId={pendingId} onConnect={connectPreset} onUpdate={(server, action) => void update(server, action)} />}
      {form.open && <form className="mcp-editor" onSubmit={save}>
        <div className="destination-heading"><div><h3>{editingId ? 'Editar servidor' : 'Novo servidor'}</h3><p className="muted">Salvar não inicia processos nem faz chamadas de rede.</p></div><Cable aria-hidden="true" /></div>
        <div className="mcp-form-grid"><label className="field">Nome do servidor<input value={name} onChange={event => setName(event.target.value)} maxLength={128} required /></label><IonPicker id="mcp-transport" label="Transporte" value={transport} onChange={next => setTransport(next as 'http' | 'stdio')} options={[{ value: 'http', label: 'HTTP (HTTPS ou loopback)' }, { value: 'stdio', label: 'Processo local (stdio)' }]} /></div>
        {transport === 'http' ? <><label className="field">URL do servidor<input type="url" value={url} onChange={event => setUrl(event.target.value)} placeholder="https://servidor.example/mcp" required /></label><IonPicker id="mcp-auth-scheme" label="Como enviar a credencial" value={authScheme} onChange={next => setAuthScheme(next as 'bearer' | 'basic')} options={[{ value: 'bearer', label: 'Bearer (o próprio token)' }, { value: 'basic', label: 'Basic (usuário:token)' }]} /></> : <><label className="field">Executável absoluto<input value={command} onChange={event => setCommand(event.target.value)} placeholder="/usr/local/bin/meu-servidor" required /></label><label className="field">Argumentos (um por linha)<textarea value={args} onChange={event => setArgs(event.target.value)} rows={3} placeholder="--modo-local" /></label><label className="field">Nome da variável de ambiente do token<input value={tokenEnvVar} onChange={event => setTokenEnvVar(event.target.value)} placeholder="MCP_TOKEN" pattern="[A-Za-z_][A-Za-z0-9_]*" maxLength={128} required={!!token || !!servers.find(server => server.id === editingId)?.hasCredential} /></label></>}
        <label className="field">{basic ? 'Credencial (usuário:token)' : 'Token de acesso (opcional)'}<input type="password" autoComplete="new-password" value={token} onChange={event => setToken(event.target.value)} placeholder={editingId ? 'Deixe vazio para manter o token atual' : basic ? 'ana@empresa.com:token-de-api' : 'Guardado no cofre local'} required={basic && !servers.find(server => server.id === editingId)?.hasCredential} /></label>
        <p className="mcp-safety-note"><ShieldCheck aria-hidden="true" />{transport === 'stdio' ? 'Cada chamada segue as permissões do projeto: pede a sua aprovação, exceto em Acesso total. O nome da variável fica salvo; o valor fica no cofre e é entregue ao processo MCP quando ele inicia. O processo local pode ler o token. Não inclua segredos nos argumentos.' : 'Conectar permite que o servidor receba chamadas de ferramenta do agente. Cada chamada segue as permissões do projeto: pede a sua aprovação, exceto em Acesso total. Não inclua segredos na URL; use o campo de token.'}</p>
        <div className="pipeline-actions">{editingId && <button type="button" className="touch-target secondary-button" onClick={reset}>Cancelar edição</button>}<button type="submit" className="touch-target primary-button" disabled={!!pendingId || !name.trim() || (transport === 'http' ? !url.trim() : !command.trim())}>Salvar servidor</button></div>
      </form>}
      {state === 'loading' && <p className="muted" role="status">Carregando servidores…</p>}
      {state === 'error' && <div className="inline-error" role="alert"><p>Não foi possível carregar os servidores.</p><button type="button" className="touch-target secondary-button" onClick={() => void refresh()}>Tentar novamente</button></div>}
      {state === 'ready' && (servers.length === 0 ? <div className="catalog-empty"><Cable aria-hidden="true" /><strong>Nenhum servidor cadastrado</strong><span className="muted">Adicione um servidor acima e conecte quando quiser descobrir suas ferramentas.</span></div> : custom.length > 0 && <ul className="mcp-list">{custom.map(server => <li className="mcp-card" key={server.id}><div className="destination-heading"><div><h3>{server.name}</h3><p className="muted mono">{server.transport === 'http' ? server.url : [server.command, ...server.args].join(' ')}</p></div><span className={`status-chip${server.enabled ? ' status-ready' : ''}`}>{server.enabled ? 'Ativo' : 'Desativado'}</span></div><p className="muted">{server.enabled ? `${server.tools.length} ferramenta(s) disponíveis para novas sessões · conexão refeita a cada chamada` : 'Nenhuma ferramenta ativa em novas sessões'}{server.hasCredential ? ' · Credencial no cofre' : ''}{server.transport === 'stdio' && server.tokenEnvVar ? ` · Token via ${server.tokenEnvVar} ao iniciar` : ''}{server.transport === 'http' && server.authScheme === 'basic' ? ' · Credencial enviada como Basic' : ''}</p>{server.enabled && server.tools.length > 0 && <ul className="mcp-tools">{server.tools.map(tool => <li key={tool.agentName}><strong>{tool.name}</strong><span className="muted">{tool.description || 'Sem descrição fornecida pelo servidor'}</span></li>)}</ul>}<div className="mcp-card-actions"><button type="button" className="touch-target secondary-button" onClick={() => edit(server)} disabled={!!pendingId}>Editar</button>{server.enabled ? <button type="button" className="touch-target secondary-button" onClick={() => void update(server, 'disable')} disabled={!!pendingId}>Desativar {server.name}</button> : <button type="button" className="touch-target primary-button" onClick={() => void update(server, 'connect')} disabled={!!pendingId}><PlugZap aria-hidden="true" />Conectar {server.name}</button>}</div></li>)}</ul>)}
    </>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {notice && <p className="form-success" role="status">{notice}</p>}
  </div>
}
