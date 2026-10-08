import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { FileText, History, Pencil, Save, X } from 'lucide-react'
import type { Backend, PipelineDesign, PipelineDesignDocument as DesignDocument, PipelineDesignStage } from '../../lib/backend'

export const designLabels: Record<PipelineDesignStage, string> = { discovery: 'Discovery', spec: 'SPEC', plan: 'Plan' }
export const designStageOrder: PipelineDesignStage[] = ['discovery', 'spec', 'plan']

function inline(text: string): ReactNode[] {
  return text.split(/(\*\*[^*]+\*\*|`[^`]+`)/g).map((part, index) => part.startsWith('**') && part.endsWith('**') ? <strong key={index}>{part.slice(2, -2)}</strong> : part.startsWith('`') && part.endsWith('`') ? <code key={index}>{part.slice(1, -1)}</code> : part)
}

// Raw HTML stays text; documents never execute markup from the provider.
export function DesignMarkdown({ content }: { content: string }) {
  const blocks: ReactNode[] = [], lines = content.replace(/\r\n/g, '\n').split('\n')
  for (let index = 0; index < lines.length;) {
    const line = lines[index]
    if (!line.trim()) { index++; continue }
    if (/^\s*```/.test(line)) {
      const code: string[] = []; index++
      while (index < lines.length && !/^\s*```/.test(lines[index])) code.push(lines[index++])
      index++; blocks.push(<pre key={index}><code>{code.join('\n')}</code></pre>); continue
    }
    const heading = /^(#{1,6})\s+(.+)$/.exec(line)
    if (heading) { const text = inline(heading[2]), key = index++; blocks.push(heading[1].length <= 2 ? <h4 key={key}>{text}</h4> : <h5 key={key}>{text}</h5>); continue }
    if (/^\s*(?:[-*+]\s+|\d+[.)]\s+)\S/.test(line)) {
      const ordered = /^\s*\d/.test(line), items: ReactNode[] = [], key = index
      while (index < lines.length) { const item = (ordered ? /^\s*\d+[.)]\s+(.+)$/ : /^\s*[-*+]\s+(.+)$/).exec(lines[index]); if (!item) break; items.push(<li key={index}>{inline(item[1])}</li>); index++ }
      blocks.push(ordered ? <ol key={key}>{items}</ol> : <ul key={key}>{items}</ul>); continue
    }
    const paragraph: string[] = [line], key = index++
    while (index < lines.length && lines[index].trim() && !/^(#{1,6}\s|\s*```|\s*[-*+]\s|\s*\d+[.)]\s)/.test(lines[index])) paragraph.push(lines[index++])
    blocks.push(<p key={key}>{inline(paragraph.join('\n'))}</p>)
  }
  return <div className="design-markdown">{blocks}</div>
}

function provenance(document: DesignDocument) {
  if (document.author !== 'ai') return 'Editado por você'
  return document.selection?.modelId ? `${document.selection.modelId}${document.selection.reasoningEffort ? ` · ${document.selection.reasoningEffort}` : ''}` : 'Preparado pela IA'
}
type EditorSource = { stage: PipelineDesignStage; version: number; contentDigest: string; content: string; stale: boolean }
type EditorDraft = { text: string; baseVersion: number; baseDigest: string; baseContent: string; baseSourceDigest: string; baseStale: boolean; sources: EditorSource[]; order: number }
const manualEditorDrafts = new WeakMap<Backend, Map<string, EditorDraft>>()
let draftOrder = 0
const draftKey = (design: PipelineDesign, stage: PipelineDesignStage) => JSON.stringify([design.workspaceId, design.pipelineId, stage])
function currentSources(design: PipelineDesign, stage: PipelineDesignStage): EditorSource[] {
  return designStageOrder.slice(0, designStageOrder.indexOf(stage)).map(source => ({ stage: source, version: design.documents[source].version, contentDigest: design.documents[source].contentDigest, content: design.documents[source].content, stale: design.documents[source].stale }))
}
function draftFromCurrent(design: PipelineDesign, stage: PipelineDesignStage, text: string): EditorDraft {
  const document = design.documents[stage]
  return { text, baseVersion: document.version, baseDigest: document.contentDigest, baseContent: document.content, baseSourceDigest: document.sourceDigest, baseStale: document.stale, sources: currentSources(design, stage), order: ++draftOrder }
}
function rememberDraft(backend: Backend, key: string, draft: EditorDraft) {
  let drafts = manualEditorDrafts.get(backend)
  if (!drafts) { drafts = new Map(); manualEditorDrafts.set(backend, drafts) }
  drafts.set(key, draft)
}
export function savedManualEditorStage(backend: Backend, design: PipelineDesign): PipelineDesignStage | undefined {
  return designStageOrder.map(stage => ({ stage, draft: manualEditorDrafts.get(backend)?.get(draftKey(design, stage)) })).filter(item => !!item.draft).sort((a, b) => b.draft!.order - a.draft!.order)[0]?.stage
}
type Props = { backend: Backend; design: PipelineDesign; stage: PipelineDesignStage; disabled: boolean; readOnly: boolean; onStageChange: (stage: PipelineDesignStage) => void; onEditingChange: (editing: boolean) => void; onSave: (stage: PipelineDesignStage, content: string) => Promise<boolean>; onRestore: (stage: PipelineDesignStage, version: number) => Promise<boolean> }
function HistoricalDocument({ version, content }: { version: number; content: string }) {
  const [open, setOpen] = useState(false)
  return <details onToggle={event => setOpen(event.currentTarget.open)}><summary>Ver texto da versão {version}</summary>{open && <DesignMarkdown content={content} />}</details>
}

export function PipelineDesignDocument({ backend, design, stage, disabled, readOnly, onStageChange, onEditingChange, onSave, onRestore }: Props) {
  const document = design.documents[stage], label = designLabels[stage], versions = design.versions[stage] ?? []
  const key = draftKey(design, stage)
  const [editorState, setEditorState] = useState<{ backend: Backend; key: string; draft?: EditorDraft }>(() => ({ backend, key, draft: manualEditorDrafts.get(backend)?.get(key) }))
  const draft = editorState.backend === backend && editorState.key === key ? editorState.draft : manualEditorDrafts.get(backend)?.get(key)
  const editing = !!draft
  const [historyOpen, setHistoryOpen] = useState(false)
  const [comparing, setComparing] = useState(false)
  const editor = useRef<HTMLTextAreaElement>(null), tabRefs = useRef<(HTMLButtonElement | null)[]>([])
  const view = useRef({ backend, key, active: true }), currentDraft = useRef(draft), editingCallback = useRef(onEditingChange)
  currentDraft.current = draft; editingCallback.current = onEditingChange
  useLayoutEffect(() => {
    view.current = { backend, key, active: true }
    const restored = manualEditorDrafts.get(backend)?.get(key)
    setEditorState({ backend, key, draft: restored }); setComparing(false)
    editingCallback.current(!!restored)
    return () => { view.current.active = false }
  }, [backend, key])
  useEffect(() => { if (editing) editor.current?.focus() }, [editing])
  const sources = currentSources(design, stage)
  const conflict = !!draft && (draft.baseVersion !== document.version || draft.baseDigest !== document.contentDigest || draft.baseSourceDigest !== document.sourceDigest || draft.baseStale !== document.stale || draft.sources.some((source, index) => source.version !== sources[index]?.version || source.contentDigest !== sources[index]?.contentDigest || source.stale !== sources[index]?.stale))
  function setDraft(next: EditorDraft) { rememberDraft(backend, key, next); setEditorState({ backend, key, draft: next }) }
  function startEdit() { setDraft(draftFromCurrent(design, stage, document.content)); onEditingChange(true) }
  function closeEditor(snapshot = draft) {
    if (snapshot && manualEditorDrafts.get(backend)?.get(key) === snapshot) manualEditorDrafts.get(backend)?.delete(key)
    if (view.current.active && view.current.backend === backend && view.current.key === key && currentDraft.current === snapshot) { setEditorState({ backend, key }); onEditingChange(false) }
  }
  async function save() { const snapshot = draft; if (snapshot && !conflict && !disabled && !readOnly && snapshot.text.trim() && await onSave(stage, snapshot.text)) closeEditor(snapshot) }
  return <section className="design-document" aria-label="Documentos preparados">
    <div className="design-document-tabs" role="tablist" aria-label="Documentos do trabalho">
      {designStageOrder.map((item, index) => <button key={item} ref={element => { tabRefs.current[index] = element }} type="button" role="tab" id={`design-tab-${item}`} aria-controls={`design-panel-${item}`} aria-selected={item === stage} tabIndex={item === stage ? 0 : -1} disabled={editing && item !== stage} onClick={() => onStageChange(item)} onKeyDown={event => {
        if (editing || !['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
        event.preventDefault(); const selected = event.key === 'Home' ? 0 : event.key === 'End' ? 2 : (index + (event.key === 'ArrowRight' ? 1 : 2)) % 3
        onStageChange(designStageOrder[selected]); tabRefs.current[selected]?.focus()
      }}><span>{designLabels[item]}</span>{design.documents[item]?.stale && <span className="design-tab-state">Desatualizado</span>}</button>)}
    </div>
    <div className="design-document-content" role="tabpanel" id={`design-panel-${stage}`} aria-labelledby={`design-tab-${stage}`}>
      <div className="design-document-toolbar"><div><strong>{label}</strong><span className="muted">{document.version ? `Versão ${document.version} · ${provenance(document)}` : 'Ainda não preparado'}</span></div>{!readOnly && !editing && <button type="button" className="touch-target secondary-button" onClick={startEdit} disabled={disabled}><Pencil aria-hidden="true" />Editar {label}</button>}</div>
      {document.stale && <p className="design-stale" role="status">Este documento precisa ser atualizado.</p>}
      {draft ? <div className="design-manual-editor"><label className="field">Texto de {label}<textarea ref={editor} value={draft.text} onChange={event => setDraft({ ...draft, text: event.target.value, order: ++draftOrder })} rows={16} maxLength={131072} disabled={disabled || readOnly} /></label>{conflict && <><p className="form-error" role="alert">O documento ou suas fontes mudaram. Seu rascunho foi preservado; compare as versões antes de salvar.</p><details onToggle={event => setComparing(event.currentTarget.open)}><summary>Comparar com a versão atual</summary>{comparing && <div><p><strong>Base do rascunho · versão {draft.baseVersion}</strong></p><DesignMarkdown content={draft.baseContent} /><p><strong>Versão atual · versão {document.version}</strong></p><DesignMarkdown content={document.content} />{sources.map((source, index) => <section key={source.stage} aria-label={`Comparar fonte ${designLabels[source.stage]}`}><p><strong>Fonte {designLabels[source.stage]} · versão {draft.sources[index]?.version ?? 0} → {source.version}</strong></p><p className="muted">Fonte usada no rascunho</p><DesignMarkdown content={draft.sources[index]?.content ?? ''} /><p className="muted">Fonte atual</p><DesignMarkdown content={source.content} /></section>)}<button type="button" className="touch-target secondary-button" disabled={disabled || readOnly} onClick={() => { setDraft(draftFromCurrent(design, stage, draft.text)); setComparing(false) }}>Usar versão atual como base</button></div>}</details></>}{readOnly && <p className="design-stale" role="status">Esta execução já foi aprovada. Seu rascunho local foi preservado; crie uma continuação para editar.</p>}<div className="design-inline-actions"><button type="button" className="touch-target secondary-button" onClick={() => closeEditor()} disabled={disabled}><X aria-hidden="true" />Cancelar edição</button><button type="button" className="touch-target primary-button" onClick={() => void save()} disabled={disabled || readOnly || conflict || !draft.text.trim()}><Save aria-hidden="true" />Salvar {label}</button></div></div>
        : document.content ? <article aria-label={`Documento ${label} versão ${document.version}`}><DesignMarkdown content={document.content} /></article> : <div className="design-document-empty"><FileText aria-hidden="true" /><p>{stage === 'spec' ? 'A SPEC aparecerá aqui com escopo e critérios de aceite.' : stage === 'plan' ? 'O Plan aparecerá aqui com os passos de implementação.' : 'Registre o contexto e o resultado esperado no Discovery.'}</p></div>}
      {!editing && versions.length > 0 && <details className="design-history" onToggle={event => setHistoryOpen(event.currentTarget.open)}><summary><History aria-hidden="true" />Histórico de {label} ({versions.length})</summary>{historyOpen && <ol>{[...versions].reverse().map(version => <li key={version.version}><div><strong>Versão {version.version}{version.version === document.version ? ' · atual' : ''}</strong><span className="muted">{version.reason === 'restored' ? `Restaurada da versão ${version.restoredFromVersion}` : version.author === 'ai' ? 'Preparada pela IA' : 'Editada por você'} · {new Date(version.createdAt).toLocaleString('pt-BR')}</span></div><HistoricalDocument version={version.version} content={version.content} />{version.version !== document.version && !readOnly && <button type="button" className="touch-target secondary-button" onClick={() => void onRestore(stage, version.version)} disabled={disabled}>Restaurar versão {version.version}</button>}</li>)}</ol>}</details>}
    </div>
  </section>
}
