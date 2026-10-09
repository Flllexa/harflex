import { useEffect, useState, type FormEvent } from 'react'
import { Bot, FolderOpen, Plus, RefreshCw } from 'lucide-react'
import { IonPicker } from '../../components/IonPicker'
import { errorMessage, type Agent, type Backend, type BackendOption, type MCPServer } from '../../lib/backend'
import { DelegationTree } from './DelegationTree'
import { CreateToggle, useCreateForm } from '../../components/CreateForm'
import { useT } from '../../i18n'

type Props = { backend: Backend; backends: BackendOption[]; workspaceId?: string; parentSessionId?: string; onStart: (agent: Agent, reason: string) => Promise<void>; onDelegate: (agent: Agent, prompt: string, requestId: string) => Promise<void>; onOpenSession: (id: string) => Promise<void>; onProjects: () => void; onSettings: () => void }
const toolOptions = [['read', 'Ler arquivos'], ['ls', 'Listar'], ['find', 'Encontrar'], ['grep', 'Buscar texto'], ['knowledge_search', 'Buscar no conhecimento local'], ['write', 'Criar arquivo'], ['edit', 'Editar arquivo'], ['bash', 'Terminal Unix'], ['powershell', 'PowerShell']] as const
const defaults = ['read', 'ls', 'find', 'grep']

function newRequestID() {
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  return Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('')
}

const pendingRequestKey = (parentSessionId: string, agentId: string) => `harflex:delegation-request:${parentSessionId}:${agentId}`

export function AgentsPage({ backend, backends, workspaceId, parentSessionId, onStart, onDelegate, onOpenSession, onProjects, onSettings }: Props) {
  const t = useT()
  const [items, setItems] = useState<Agent[]>([])
  const [mcpServers, setMCPServers] = useState<MCPServer[]>([])
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [editingId, setEditingId] = useState<string>()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [instructions, setInstructions] = useState('')
  const [choice, setChoice] = useState('')
  const [allowed, setAllowed] = useState<string[]>(defaults)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string>()
  const [notice, setNotice] = useState<string>()
  const [delegateId, setDelegateId] = useState<string>()
  const [delegateRequestId, setDelegateRequestId] = useState('')
  const [startId, setStartId] = useState<string>()
  const [startReason, setStartReason] = useState('')
  const [task, setTask] = useState('')
  const selectedBackend = choice || backends.find(item => item.available)?.id || ''
  const selectedKind = backends.find(item => item.id === selectedBackend)?.kind
  const form = useCreateForm(state === 'ready', items.length)

  async function refresh() {
    setState('loading')
    try {
      const [agents, servers] = await Promise.all([backend.listAgents(), workspaceId ? backend.listMCPServers(workspaceId) : Promise.resolve([])])
      setItems(agents); setMCPServers(servers); setState('ready')
    }
    catch { setState('error') }
  }
  useEffect(() => { void refresh() }, [backend, workspaceId])

  function edit(item: Agent) {
    setEditingId(item.id); setName(item.name); setDescription(item.description); setInstructions(item.instructions); setChoice(item.backendId); setAllowed(item.allowedTools); setError(undefined); form.setOpen(true)
  }
  function reset() { setEditingId(undefined); setName(''); setDescription(''); setInstructions(''); setChoice(''); setAllowed(defaults) }
  function toggleTool(tool: string) { setAllowed(current => current.includes(tool) ? current.filter(item => item !== tool) : [...current, tool]) }

  async function save(event: FormEvent) {
    event.preventDefault()
    if (!selectedBackend || pending) return
    setPending(true); setError(undefined); setNotice(undefined)
    try {
      const saved = await backend.saveAgent({ id: editingId, name: name.trim(), description: description.trim(), instructions: instructions.trim(), backendId: selectedBackend, allowedTools: selectedKind === 'cli' ? [] : allowed })
      setItems(current => [saved, ...current.filter(item => item.id !== saved.id)])
      setNotice(t('Agente {name} salvo localmente.', { name: saved.name }))
      reset(); form.setOpen(false)
    } catch (failure) { setError(errorMessage(failure)) }
    finally { setPending(false) }
  }

  async function start(item: Agent) {
    if (!workspaceId || pending || !startReason.trim()) return
    setPending(true); setError(undefined)
    try { await onStart(item, startReason.trim()) }
    catch (failure) { setError(errorMessage(failure)); setPending(false) }
  }

  function openDelegate(item: Agent) {
    if (!parentSessionId) return
    try {
      setDelegateRequestId(localStorage.getItem(pendingRequestKey(parentSessionId, item.id)) ?? '')
      setDelegateId(item.id); setTask(''); setError(undefined)
    } catch { setError(t('Não foi possível ler a chave local de retomada. Verifique o armazenamento do aplicativo.')); }
  }

  function clearPendingRequest(item: Agent) {
    if (!parentSessionId) return
    try {
      localStorage.removeItem(pendingRequestKey(parentSessionId, item.id))
      setDelegateRequestId('')
      setError(undefined)
    } catch { setError(t('Não foi possível descartar a tentativa local. Verifique o armazenamento do aplicativo.')); }
  }

  async function delegate(item: Agent) {
    if (!parentSessionId || !task.trim() || pending) return
    let requestId = delegateRequestId
    try {
      if (!requestId) {
        requestId = newRequestID()
        localStorage.setItem(pendingRequestKey(parentSessionId, item.id), requestId)
        setDelegateRequestId(requestId)
      }
    } catch { setError(t('Não foi possível guardar a chave local de retomada; a delegação não foi iniciada.')); return }
    setPending(true); setError(undefined)
    try {
      await onDelegate(item, task.trim(), requestId)
      localStorage.removeItem(pendingRequestKey(parentSessionId, item.id))
      setTask(''); setDelegateId(undefined); setDelegateRequestId('')
    }
    catch (failure) { setError(errorMessage(failure)); setPending(false) }
  }

  return <div className="agents-page">
    <div className="destination-heading"><div><h2>{t('Agentes locais')}</h2><p className="muted">{t('Instruções, backend e ferramentas escolhidas por você.')}</p></div><div className="pipeline-actions">{form.collapsible && <CreateToggle open={form.open} onToggle={() => { if (form.open) reset(); form.setOpen(!form.open) }} label={t('Novo agente')} />}<button type="button" className="touch-target secondary-button" onClick={() => void refresh()} disabled={state === 'loading'}><RefreshCw aria-hidden="true" />{t('Atualizar')}</button></div></div>
    {parentSessionId && <DelegationTree key={parentSessionId} backend={backend} sessionId={parentSessionId} agents={items} onOpenSession={onOpenSession} />}
    {form.open && <form className="agent-editor" onSubmit={save}><div className="destination-heading"><div><h3>{editingId ? t('Editar agente') : t('Novo agente')}</h3><p className="muted">{t('Cada sessão guarda uma cópia da configuração usada.')}</p></div><Bot aria-hidden="true" /></div>
      <div className="agent-form-grid"><label className="field">{t('Nome do agente')}<input value={name} onChange={event => setName(event.target.value)} maxLength={128} required /></label><label className="field">{t('Descrição')}<input value={description} onChange={event => setDescription(event.target.value)} maxLength={500} /></label></div>
      <IonPicker id="agent-backend" label="Backend" value={selectedBackend} onChange={setChoice} required
        options={[{ value: '', label: t('Escolha um backend') }, ...backends.map(item => ({ value: item.id, label: item.available ? item.name : t('{name} · indisponível', { name: item.name }), disabled: !item.available }))]} />
      {!backends.some(item => item.available) && <button type="button" className="touch-target secondary-button" onClick={onSettings}>{t('Configurar provedor')}</button>}
      <label className="field">{t('Instruções do agente')}<textarea value={instructions} onChange={event => setInstructions(event.target.value)} rows={6} maxLength={64 * 1024} required placeholder={t('Defina o papel, limites e resultado esperado.')} /></label>
      {selectedKind === 'cli' ? <p className="project-warning">{t('Este CLI usa suas próprias permissões. O Harflex registra a sessão, mas não restringe as ferramentas internas do processo.')}{selectedBackend === 'opencode' ? t(' O OpenCode recebe o prompt por argumento de processo, que pode ficar visível temporariamente a ferramentas locais de inspeção.') : ''}</p> : <><fieldset className="agent-tools"><legend>{t('Ferramentas disponíveis')}</legend><div>{toolOptions.map(([id, label]) => <label key={id}><input type="checkbox" checked={allowed.includes(id)} onChange={() => toggleTool(id)} /><span>{t(label)}</span></label>)}</div></fieldset>{mcpServers.some(server => server.enabled && server.tools.length > 0) && <fieldset className="agent-tools"><legend>{t('Ferramentas MCP deste projeto')}</legend><div>{mcpServers.filter(server => server.enabled).flatMap(server => server.tools.map(tool => <label key={tool.agentName}><input type="checkbox" checked={allowed.includes(tool.agentName)} onChange={() => toggleTool(tool.agentName)} /><span>{server.name} · {tool.name}</span></label>))}</div><p className="muted">{t('A disponibilidade é conferida novamente ao iniciar cada sessão.')}</p></fieldset>}</>}
      <div className="pipeline-actions">{editingId && <button type="button" className="touch-target secondary-button" onClick={reset}>{t('Cancelar edição')}</button>}<button type="submit" className="touch-target primary-button" disabled={pending || !name.trim() || !instructions.trim() || !selectedBackend}><Plus aria-hidden="true" />{t('Salvar agente')}</button></div>
    </form>}
    {state === 'loading' && <p className="muted" role="status">{t('Carregando agentes…')}</p>}
    {state === 'error' && <div className="inline-error" role="alert"><p>{t('Não foi possível carregar os agentes.')}</p><button type="button" className="touch-target secondary-button" onClick={() => void refresh()}>{t('Tentar novamente')}</button></div>}
    {state === 'ready' && (items.length === 0 ? <div className="catalog-empty"><Bot aria-hidden="true" /><strong>{t('Nenhum agente salvo')}</strong><span className="muted">{t('Crie um perfil acima para usar instruções reutilizáveis.')}</span></div> : <ul className="agent-list">{items.map(item => {
      const backendOption = backends.find(option => option.id === item.backendId)
      return <li key={item.id} className="agent-card"><div className="agent-card-top"><span className="agent-avatar"><Bot aria-hidden="true" /></span><div><strong>{item.name}</strong><span className="muted">{item.description || t('Sem descrição')}</span></div><span className={`status-chip${backendOption?.available ? ' status-ready' : ' status-unavailable'}`}>{backendOption?.available ? backendOption.name : t('Backend indisponível')}</span></div><p className="agent-instructions">{item.instructions}</p><div className="agent-card-footer"><span className="muted">{backendOption?.kind === 'cli' ? t('Permissões do CLI') : t('{count} ferramentas', { count: item.allowedTools.length })}</span><div><button type="button" className="touch-target secondary-button" onClick={() => edit(item)} aria-label={t('Editar {name}', { name: item.name })}>{t('Editar')}</button>{parentSessionId && <button type="button" className="touch-target secondary-button" onClick={() => openDelegate(item)} disabled={!backendOption?.available || pending} aria-label={t('Delegar a {name}', { name: item.name })}>{t('Delegar')}</button>}<button type="button" className="touch-target primary-button" onClick={() => { setStartId(item.id); setStartReason('') }} disabled={!workspaceId || !backendOption?.available || pending} aria-label={t('Conversar com {name}', { name: item.name })}>{t('Conversar')}</button></div></div>{startId === item.id && <form className="agent-delegate" onSubmit={event => { event.preventDefault(); void start(item) }}><p className="project-warning">{t('Esta conversa livre pula o SDD. Para desenvolvimento orientado a fases, use “Novo trabalho”.')}</p><label className="field">{t('Motivo para conversar sem SDD com {name}', { name: item.name })}<textarea value={startReason} onChange={event => setStartReason(event.target.value)} rows={2} maxLength={1000} required /></label><div className="pipeline-actions"><button type="button" className="touch-target secondary-button" onClick={() => setStartId(undefined)}>{t('Cancelar')}</button><button type="submit" className="touch-target primary-button" disabled={!startReason.trim() || pending}>{t('Iniciar sessão livre com {name}', { name: item.name })}</button></div></form>}{delegateId === item.id && <form className="agent-delegate" onSubmit={event => { event.preventDefault(); void delegate(item) }}><p className="muted">{t('Até 3 chamadas por subagente, 5 min por execução. Cada trecho ativo recebe até 5 min; a espera humana por aprovação não conta. Tentativas interrompidas contam; não são limites de custo ou tokens.')}</p>{delegateRequestId && <p className="project-warning">{t('Existe uma tentativa pendente nesta máquina. Reenvie a mesma tarefa para consultar o filho existente, ou inicie uma nova tentativa conscientemente.')}</p>}<label className="field">{t('Tarefa para {name}', { name: item.name })}<textarea value={task} onChange={event => setTask(event.target.value)} rows={3} maxLength={1024 * 1024} required /></label><div className="pipeline-actions">{delegateRequestId && <button type="button" className="touch-target secondary-button" onClick={() => clearPendingRequest(item)} disabled={pending}>{t('Iniciar nova tentativa')}</button>}<button type="button" className="touch-target secondary-button" onClick={() => setDelegateId(undefined)}>{t('Cancelar')}</button><button type="submit" className="touch-target primary-button" disabled={!task.trim() || pending}>{t('Iniciar subagente')}</button></div></form>}</li>
    })}</ul>)}
    {!workspaceId && items.length > 0 && <div className="agent-workspace-note"><FolderOpen aria-hidden="true" /><span>{t('Abra um projeto para conversar com um agente.')}</span><button type="button" className="touch-target secondary-button" onClick={onProjects}>{t('Abrir projetos')}</button></div>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {notice && <p className="form-success" role="status">{notice}</p>}
  </div>
}
