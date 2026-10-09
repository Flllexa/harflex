import { useEffect, useState, type FormEvent } from 'react'
import { BookMarked, FileDown, FolderOpen, Pencil, Plus, RefreshCw } from 'lucide-react'
import { errorMessage, type Backend, type Skill } from '../../lib/backend'
import { CreateToggle, useCreateForm } from '../../components/CreateForm'
import { useT } from '../../i18n'

type Props = { backend: Backend; workspaceId?: string; onProjects: () => void }

export function SkillsPage({ backend, workspaceId, onProjects }: Props) {
  const t = useT()
  const [items, setItems] = useState<Skill[]>([])
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [editingId, setEditingId] = useState<string>()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [content, setContent] = useState('')
  const [enabled, setEnabled] = useState(true)
  const [importPath, setImportPath] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string>()
  const [notice, setNotice] = useState<string>()
  const form = useCreateForm(state === 'ready', items.length)

  async function refresh() {
    if (!workspaceId) return
    setState('loading')
    try { setItems(await backend.listSkills(workspaceId)); setState('ready') }
    catch { setState('error') }
  }
  useEffect(() => { void refresh() }, [backend, workspaceId])

  function edit(item: Skill) { setEditingId(item.id); setName(item.name); setDescription(item.description); setContent(item.content); setEnabled(item.enabled); form.setOpen(true) }
  function reset() { setEditingId(undefined); setName(''); setDescription(''); setContent(''); setEnabled(true) }

  async function save(event: FormEvent) {
    event.preventDefault()
    if (!workspaceId || pending) return
    setPending(true); setError(undefined); setNotice(undefined)
    try {
      const saved = await backend.saveSkill({ id: editingId, workspaceId, name: name.trim(), description: description.trim(), content: content.trim(), enabled })
      setItems(current => [saved, ...current.filter(item => item.id !== saved.id)])
      setNotice(`${t('Skill {name} salva.', { name: saved.name })} ${saved.enabled ? t('Disponível para novas sessões.') : t('Desativada para novas sessões.')}`)
      reset(); form.setOpen(false)
    } catch (failure) { setError(errorMessage(failure)) }
    finally { setPending(false) }
  }

  async function toggle(item: Skill) {
    if (pending) return
    setPending(true); setError(undefined); setNotice(undefined)
    try {
      const saved = await backend.saveSkill({ id: item.id, workspaceId: item.workspaceId, name: item.name, description: item.description, content: item.content, enabled: !item.enabled })
      setItems(current => current.map(existing => existing.id === saved.id ? saved : existing))
      setNotice(saved.enabled ? t('{name} ativada para novas sessões.', { name: saved.name }) : t('{name} desativada para novas sessões.', { name: saved.name }))
    } catch (failure) { setError(errorMessage(failure)) }
    finally { setPending(false) }
  }

  async function importFile(event: FormEvent) {
    event.preventDefault()
    if (!workspaceId || !importPath.trim() || pending) return
    setPending(true); setError(undefined); setNotice(undefined)
    try {
      const imported = await backend.importSkill({ workspaceId, path: importPath.trim(), enabled: true })
      setItems(current => [imported, ...current.filter(item => item.id !== imported.id)])
      setImportPath('')
      setNotice(t('{name} importada e ativada para novas sessões.', { name: imported.name }))
      form.setOpen(false)
    } catch (failure) { setError(errorMessage(failure)) }
    finally { setPending(false) }
  }
  async function chooseFile() {
    try { const selected = await backend.pickSkillFile(); if (selected) setImportPath(selected) }
    catch (failure) { setError(errorMessage(failure)) }
  }

  return <div className="skills-page">
    <div className="destination-heading"><div><h2>{t('Skills do projeto')}</h2><p className="muted">{t('Instruções reutilizáveis incluídas no contexto de novas sessões.')}</p></div>{workspaceId && <div className="pipeline-actions">{form.collapsible && <CreateToggle open={form.open} onToggle={() => { if (form.open) reset(); form.setOpen(!form.open) }} label={t('Nova skill')} />}<button type="button" className="touch-target secondary-button" onClick={() => void refresh()} disabled={state === 'loading'}><RefreshCw aria-hidden="true" />{t('Atualizar')}</button></div>}</div>
    {!workspaceId ? <div className="catalog-empty"><FolderOpen aria-hidden="true" /><strong>{t('Abra um projeto para usar skills')}</strong><span className="muted">{t('Cada skill fica guardada localmente no projeto escolhido.')}</span><button type="button" className="touch-target primary-button" onClick={onProjects}>{t('Abrir projetos')}</button></div> : <>
      {form.open && <><form className="skill-editor" onSubmit={importFile}><div className="destination-heading"><div><h3>{t('Importar SKILL.md')}</h3><p className="muted">{t('Escolha um arquivo de até 64 KB dentro do projeto. O conteúdo é copiado para o catálogo local.')}</p></div><FileDown aria-hidden="true" /></div><label className="field">{t('Caminho da skill')}<input value={importPath} onChange={event => setImportPath(event.target.value)} placeholder=".agents/skills/review/SKILL.md" required /></label><div className="pipeline-actions"><button type="button" className="touch-target secondary-button" onClick={() => void chooseFile()} disabled={pending}>{t('Escolher arquivo')}</button><button type="submit" className="touch-target secondary-button" disabled={pending || !importPath.trim()}>{t('Importar skill')}</button></div></form>
      <form className="skill-editor" onSubmit={save}><div className="destination-heading"><div><h3>{editingId ? t('Editar skill') : t('Nova skill')}</h3><p className="muted">{t('Mudanças valem para novas sessões; sessões anteriores mantêm a versão utilizada.')}</p></div><BookMarked aria-hidden="true" /></div><div className="skill-form-grid"><label className="field">{t('Nome da skill')}<input value={name} onChange={event => setName(event.target.value)} maxLength={128} required /></label><label className="field">{t('Descrição')}<input value={description} onChange={event => setDescription(event.target.value)} maxLength={500} /></label></div><label className="field">{t('Instruções da skill')}<textarea value={content} onChange={event => setContent(event.target.value)} rows={10} maxLength={64 * 1024} required placeholder={t('Descreva quando e como esta habilidade deve orientar o agente.')} /></label><label className="skill-enabled"><input type="checkbox" checked={enabled} onChange={event => setEnabled(event.target.checked)} /><span>{t('Ativar para novas sessões deste projeto')}</span></label><div className="pipeline-actions">{editingId && <button type="button" className="touch-target secondary-button" onClick={reset}>{t('Cancelar edição')}</button>}<button type="submit" className="touch-target primary-button" disabled={pending || !name.trim() || !content.trim()}><Plus aria-hidden="true" />{t('Salvar skill')}</button></div></form></>}
      {state === 'loading' && <p className="muted" role="status">{t('Carregando skills…')}</p>}
      {state === 'error' && <div className="inline-error" role="alert"><p>{t('Não foi possível carregar as skills.')}</p><button type="button" className="touch-target secondary-button" onClick={() => void refresh()}>{t('Tentar novamente')}</button></div>}
      {state === 'ready' && (items.length === 0 ? <div className="catalog-empty"><BookMarked aria-hidden="true" /><strong>{t('Nenhuma skill salva')}</strong><span className="muted">{t('Crie a primeira skill no formulário acima.')}</span></div> : <ul className="skill-list">{items.map(item => <li className="skill-card" key={item.id}><div className="destination-heading"><div><h3>{item.name}</h3><p className="muted">{item.description || t('Sem descrição')}</p></div><span className={`status-chip${item.enabled ? ' status-ready' : ''}`}>{item.enabled ? t('Ativa') : t('Desativada')}</span></div><pre className="mono skill-content">{item.content}</pre><div className="skill-card-footer"><span className="muted">{t('Versão {version}', { version: item.revision })}</span><div><button type="button" className="touch-target secondary-button" onClick={() => edit(item)} aria-label={t('Editar {name}', { name: item.name })}><Pencil aria-hidden="true" />{t('Editar')}</button><button type="button" className="touch-target secondary-button" onClick={() => void toggle(item)} disabled={pending}>{item.enabled ? t('Desativar') : t('Ativar')}</button></div></div></li>)}</ul>)}
    </>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {notice && <p className="form-success" role="status">{notice}</p>}
  </div>
}
