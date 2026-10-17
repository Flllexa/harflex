import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { FileText, History, Pencil, Save, X } from 'lucide-react'
import type { Backend, PipelineDesign, PipelineDesignDocument as DesignDocument, PipelineDesignStage } from '../../lib/backend'
import { localeTag, useT } from '../../i18n'
import { designStageOrder as stageOrderOfDesign, stageNames } from '../../lib/pipelineStages'

export const designLabels: Record<PipelineDesignStage, string> = { discovery: stageNames.discovery, spec: stageNames.spec, brainstorm: stageNames.brainstorm, plan: stageNames.plan }
export const designStageOrder: PipelineDesignStage[] = [...stageOrderOfDesign]
/** The Brainstorm holds full screens: its editor takes the document's own limit (512 KB). */

function inline(text: string): ReactNode[] {
  return text.split(/(\*\*[^*]+\*\*|`[^`]+`)/g).map((part, index) => part.startsWith('**') && part.endsWith('**') ? <strong key={index}>{part.slice(2, -2)}</strong> : part.startsWith('`') && part.endsWith('`') ? <code key={index}>{part.slice(1, -1)}</code> : part)
}

// Raw HTML stays text; documents never execute markup from the provider.
export function DesignMarkdown({ content }: { content: string }) {
  const t = useT()
  const blocks: ReactNode[] = [], lines = content.replace(/\r\n/g, '\n').split('\n')
  for (let index = 0; index < lines.length;) {
    const line = lines[index]
    if (!line.trim()) { index++; continue }
    if (/^\s*```/.test(line)) {
      const code: string[] = []; index++
      while (index < lines.length && !/^\s*```/.test(lines[index])) code.push(lines[index++])
      index++; blocks.push(<pre key={index}><code>{code.join('\n')}</code></pre>); continue
    }
    // A table: a row of cells over a separator row (| --- | --- |).
    if (/^\s*\|/.test(line) && index + 1 < lines.length && /^\s*\|?\s*:?-{2,}/.test(lines[index + 1])) {
      const row = (text: string) => text.trim().replace(/^\|/, '').replace(/\|$/, '').split(/(?<!\\)\|/).map(cell => cell.trim())
      const head = row(line), rows: string[][] = [], key = index
      index += 2
      while (index < lines.length && /^\s*\|/.test(lines[index])) rows.push(row(lines[index++]))
      blocks.push(<div key={key} className="design-table" role="region" aria-label={t('Tabela')} tabIndex={0}><table><thead><tr>{head.map((cell, at) => <th key={at}>{inline(cell)}</th>)}</tr></thead><tbody>{rows.map((cells, at) => <tr key={at}>{head.map((_, column) => <td key={column}>{inline(cells[column] ?? '')}</td>)}</tr>)}</tbody></table></div>)
      continue
    }
    const heading = /^(#{1,6})\s+(.+)$/.exec(line)
    if (heading) { const text = inline(heading[2]), key = index++; blocks.push(heading[1].length <= 2 ? <h4 key={key}>{text}</h4> : <h5 key={key}>{text}</h5>); continue }
    if (/^\s*(?:[-*+]\s+|\d+[.)]\s+)\S/.test(line)) {
      const ordered = /^\s*\d/.test(line), items: ReactNode[] = [], key = index, indent = /^\s*/.exec(line)![0].length
      while (index < lines.length) {
        const item = (ordered ? /^(\s*)\d+[.)]\s+(.+)$/ : /^(\s*)[-*+]\s+(.+)$/).exec(lines[index])
        if (!item || item[1].length > indent) break
        const at = index++, children: ReactNode[] = []
        // Indented items under this one (its options or details) stay inside it, so the outer numbering goes on.
        while (index < lines.length && (/^\s+(?:[-*+]|\d+[.)])\s+\S/.test(lines[index]) && /^\s*/.exec(lines[index])![0].length > indent || !lines[index].trim() && index + 1 < lines.length && /^\s+(?:[-*+]|\d+[.)])\s+\S/.test(lines[index + 1]) && /^\s*/.exec(lines[index + 1])![0].length > indent)) {
          const child = /^\s*(?:[-*+]|\d+[.)])\s+(.+)$/.exec(lines[index++])
          if (child) children.push(<li key={index}>{inline(child[1])}</li>)
        }
        items.push(<li key={at}>{inline(item[2])}{children.length > 0 && <ul>{children}</ul>}</li>)
        while (index < lines.length && !lines[index].trim() && index + 1 < lines.length && (ordered ? /^\s*\d+[.)]\s/ : /^\s*[-*+]\s/).test(lines[index + 1])) index++
      }
      blocks.push(ordered ? <ol key={key}>{items}</ol> : <ul key={key}>{items}</ul>); continue
    }
    const paragraph: string[] = [line], key = index++
    while (index < lines.length && lines[index].trim() && !/^(#{1,6}\s|\s*```|\s*[-*+]\s|\s*\d+[.)]\s|\s*\|)/.test(lines[index])) paragraph.push(lines[index++])
    blocks.push(<p key={key}>{inline(paragraph.join('\n'))}</p>)
  }
  return <div className="design-markdown">{blocks}</div>
}

function provenance(document: DesignDocument, t: (source: string) => string) {
  if (document.author !== 'ai') return t('Editado por você')
  return document.selection?.modelId ? `${document.selection.modelId}${document.selection.reasoningEffort ? ` · ${document.selection.reasoningEffort}` : ''}` : t('Preparado pela IA')
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
type Props = { backend: Backend; design: PipelineDesign; stage: PipelineDesignStage; disabled: boolean; readOnly: boolean; onStageChange: (stage: PipelineDesignStage) => void; onEditingChange: (editing: boolean) => void; onSave: (stage: PipelineDesignStage, content: string) => Promise<boolean>; onRestore: (stage: PipelineDesignStage, version: number) => Promise<boolean>; /** Shown in place of the document's text when the stage has a view of its own (the Brainstorm). */ view?: ReactNode }
function HistoricalDocument({ version, content }: { version: number; content: string }) {
  const t = useT()
  const [open, setOpen] = useState(false)
  return <details onToggle={event => setOpen(event.currentTarget.open)}><summary>{t('Ver texto da versão {version}', { version })}</summary>{open && <DesignMarkdown content={content} />}</details>
}

export function PipelineDesignDocument({ backend, design, stage, disabled, readOnly, onStageChange, onEditingChange, onSave, onRestore, view: stageView }: Props) {
  const t = useT()
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
  return <section className="design-document" aria-label={t('Documentos preparados')}>
    <div className="design-document-tabs" role="tablist" aria-label={t('Documentos do trabalho')}>
      {designStageOrder.map((item, index) => <button key={item} ref={element => { tabRefs.current[index] = element }} type="button" role="tab" id={`design-tab-${item}`} aria-controls={`design-panel-${item}`} aria-selected={item === stage} tabIndex={item === stage ? 0 : -1} disabled={editing && item !== stage} onClick={() => onStageChange(item)} onKeyDown={event => {
        if (editing || !['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
        event.preventDefault(); const count = designStageOrder.length, selected = event.key === 'Home' ? 0 : event.key === 'End' ? count - 1 : (index + (event.key === 'ArrowRight' ? 1 : count - 1)) % count
        onStageChange(designStageOrder[selected]); tabRefs.current[selected]?.focus()
      }}><span>{designLabels[item]}</span>{design.documents[item]?.stale && <span className="design-tab-state">{t('Desatualizado')}</span>}</button>)}
    </div>
    <div className="design-document-content" role="tabpanel" id={`design-panel-${stage}`} aria-labelledby={`design-tab-${stage}`}>
      <div className="design-document-toolbar"><div><strong>{label}</strong><span className="muted">{document.version ? t('Versão {version} · {source}', { version: document.version, source: provenance(document, t) }) : t('Ainda não preparado')}</span></div>{!readOnly && !editing && <button type="button" className="touch-target secondary-button" onClick={startEdit} disabled={disabled}><Pencil aria-hidden="true" />{t('Editar {label}', { label })}</button>}</div>
      {document.stale && <p className="design-stale" role="status">{t('Este documento precisa ser atualizado.')}</p>}
      {draft ? <div className="design-manual-editor"><label className="field">{t('Texto de {label}', { label })}<textarea ref={editor} value={draft.text} onChange={event => setDraft({ ...draft, text: event.target.value, order: ++draftOrder })} rows={16} disabled={disabled || readOnly} /></label>{conflict && <><p className="form-error" role="alert">{t('O documento ou suas fontes mudaram. Seu rascunho foi preservado; compare as versões antes de salvar.')}</p><details onToggle={event => setComparing(event.currentTarget.open)}><summary>{t('Comparar com a versão atual')}</summary>{comparing && <div><p><strong>{t('Base do rascunho · versão {version}', { version: draft.baseVersion })}</strong></p><DesignMarkdown content={draft.baseContent} /><p><strong>{t('Versão atual · versão {version}', { version: document.version })}</strong></p><DesignMarkdown content={document.content} />{sources.map((source, index) => <section key={source.stage} aria-label={t('Comparar fonte {label}', { label: designLabels[source.stage] })}><p><strong>{t('Fonte {label} · versão {from} → {to}', { label: designLabels[source.stage], from: draft.sources[index]?.version ?? 0, to: source.version })}</strong></p><p className="muted">{t('Fonte usada no rascunho')}</p><DesignMarkdown content={draft.sources[index]?.content ?? ''} /><p className="muted">{t('Fonte atual')}</p><DesignMarkdown content={source.content} /></section>)}<button type="button" className="touch-target secondary-button" disabled={disabled || readOnly} onClick={() => { setDraft(draftFromCurrent(design, stage, draft.text)); setComparing(false) }}>{t('Usar versão atual como base')}</button></div>}</details></>}{readOnly && <p className="design-stale" role="status">{t('Esta execução já foi aprovada. Seu rascunho local foi preservado; crie uma continuação para editar.')}</p>}<div className="design-inline-actions"><button type="button" className="touch-target secondary-button" onClick={() => closeEditor()} disabled={disabled}><X aria-hidden="true" />{t('Cancelar edição')}</button><button type="button" className="touch-target primary-button" onClick={() => void save()} disabled={disabled || readOnly || conflict || !draft.text.trim()}><Save aria-hidden="true" />{t('Salvar {label}', { label })}</button></div></div>
        : stageView ? stageView
        : document.content ? <article aria-label={t('Documento {label} versão {version}', { label, version: document.version })}><DesignMarkdown content={document.content} /></article> : <div className="design-document-empty"><FileText aria-hidden="true" /><p>{stage === 'spec' ? t('A SPEC aparecerá aqui com escopo e critérios de aceite.') : stage === 'plan' ? t('O Plan aparecerá aqui com os passos de implementação, depois que o fluxo e o design forem aprovados.') : t('Registre o contexto e o resultado esperado no Discovery.')}</p></div>}
      {!editing && versions.length > 0 && <details className="design-history" onToggle={event => setHistoryOpen(event.currentTarget.open)}><summary><History aria-hidden="true" />{t('Histórico de {label} ({count})', { label, count: versions.length })}</summary>{historyOpen && <ol>{[...versions].reverse().map(version => <li key={version.version}><div><strong>{t('Versão {version}', { version: version.version })}{version.version === document.version ? t(' · atual') : ''}</strong><span className="muted">{version.reason === 'restored' ? t('Restaurada da versão {version}', { version: version.restoredFromVersion }) : version.author === 'ai' ? t('Preparada pela IA') : t('Editada por você')} · {new Date(version.createdAt).toLocaleString(localeTag())}</span></div><HistoricalDocument version={version.version} content={version.content} />{version.version !== document.version && !readOnly && <button type="button" className="touch-target secondary-button" onClick={() => void onRestore(stage, version.version)} disabled={disabled}>{t('Restaurar versão {version}', { version: version.version })}</button>}</li>)}</ol>}</details>}
    </div>
  </section>
}
