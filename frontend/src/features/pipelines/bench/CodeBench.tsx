import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { Check, FileMinus2, FilePlus2, FileCode2, MessageSquareWarning, Undo2 } from 'lucide-react'
import { errorMessage, type Backend, type Pipeline } from '../../../lib/backend'
import { parseUnifiedDiff, type DiffFile } from './diff'
import { localeTag, useT } from '../../../i18n'

type Props = {
  backend: Backend
  run: Pipeline
  onPipelineChange: (run: Pipeline) => void
  /** The executor and model choice that starts the Coder, shown while Code has nothing to review. */
  runControls?: ReactNode
}

const fileIcon = { added: FilePlus2, removed: FileMinus2, modified: FileCode2 }
const basename = (path: string) => path.split('/').pop() ?? path
const folder = (path: string) => path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : ''

function ChangeBar({ file }: { file: DiffFile }) {
  const total = Math.max(1, file.added + file.removed)
  const blocks = 5, added = Math.round(file.added / total * blocks)
  return <span className="code-bench-bar" aria-hidden="true">{Array.from({ length: blocks }, (_, index) => <i key={index} className={index < added ? 'is-add' : file.removed ? 'is-remove' : ''} />)}</span>
}

/** Code's own bench: the change spotlighted file by file, and the decision that sends it to QA. */
export function CodeBench({ backend, run, onPipelineChange, runControls }: Props) {
  const t = useT()
  const artifact = run.artifacts.code
  const files = useMemo(() => artifact ? parseUnifiedDiff(artifact.content) : [], [artifact?.content])
  const [selected, setSelected] = useState(0)
  const [feedback, setFeedback] = useState('')
  const [asking, setAsking] = useState(false)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const waiting = run.currentStage === 'code' && run.stageStatus.code === 'waiting_user' && !!artifact?.contentDigest
  const active = run.currentStage === 'code' && run.stageStatus.code === 'active'
  const reviews = (run.executionReviews ?? []).filter(item => item.stage === 'code')
  const totals = files.reduce((sum, file) => ({ added: sum.added + file.added, removed: sum.removed + file.removed }), { added: 0, removed: 0 })
  const file = files[Math.min(selected, Math.max(0, files.length - 1))]

  useEffect(() => { setSelected(0); setFeedback(''); setAsking(false) }, [artifact?.version])

  async function decide(decision: 'approve' | 'request_revision') {
    if (!artifact?.contentDigest) return
    setPending(true); setError('')
    try {
      onPipelineChange(await backend.decidePipelineExecutionArtifact({ pipelineId: run.id, requestId: crypto.randomUUID(), stage: 'code', artifactVersion: artifact.version, artifactDigest: artifact.contentDigest, decision, feedback: decision === 'approve' ? '' : feedback.trim(), pipelineRevision: run.revision }))
    } catch (failure) { setError(errorMessage(failure)) } finally { setPending(false) }
  }

  return <div className="code-bench">
    {active && runControls && <div className="bench-slab code-bench-start">{runControls}</div>}
    {files.length > 0 && <section className="code-bench-desk" aria-label={t('Mudanças do Code')}>
      <aside className="code-bench-files" aria-label={t('Arquivos alterados')}>
        <div className="code-bench-files-head"><strong>{files.length} {files.length === 1 ? t('arquivo') : t('arquivos')}</strong><span><b className="is-add">+{totals.added}</b> <b className="is-remove">−{totals.removed}</b></span></div>
        <ul>{files.map((item, index) => { const Icon = fileIcon[item.status]; return <li key={item.path}>
          <button type="button" className={`code-bench-file is-${item.status}${index === selected ? ' is-selected' : ''}`} aria-pressed={index === selected} onClick={() => setSelected(index)}>
            <Icon aria-hidden="true" /><span className="code-bench-file-name"><strong>{basename(item.path)}</strong>{folder(item.path) && <small>{folder(item.path)}</small>}</span>
            <span className="code-bench-file-count"><b className="is-add">+{item.added}</b><b className="is-remove">−{item.removed}</b></span><ChangeBar file={item} />
          </button></li> })}</ul>
      </aside>
      {file && <div className="code-bench-diff" aria-label={t('Diff de {path}', { path: file.path })}>
        <header><span className={`code-bench-status is-${file.status}`}>{file.status === 'added' ? t('Novo') : file.status === 'removed' ? t('Removido') : t('Alterado')}</span><code className="mono">{file.path}</code></header>
        <div className="code-bench-lines mono" role="table">
          {file.lines.map((line, index) => line.kind === 'hunk'
            ? <div key={index} className="code-line is-hunk" role="row"><span role="cell" className="code-line-number" /><span role="cell" className="code-line-number" /><span role="cell" className="code-line-text">{line.text || '…'}</span></div>
            : <div key={index} className={`code-line is-${line.kind}`} role="row"><span role="cell" className="code-line-number">{line.oldNumber ?? ''}</span><span role="cell" className="code-line-number">{line.newNumber ?? ''}</span><span role="cell" className="code-line-text"><i aria-hidden="true">{line.kind === 'add' ? '+' : line.kind === 'remove' ? '−' : ' '}</i>{line.text}</span></div>)}
        </div>
      </div>}
    </section>}
    {artifact && files.length === 0 && <pre className="mono diff-body" aria-label={t('Diff de Code')}>{artifact.content}</pre>}
    {waiting && <div className="bench-actions" role="group" aria-label={t('Decisão sobre o Code')}>
      <div className="bench-actions-text"><strong>{t('Revise a mudança')}</strong><span className="muted">{t('Aprovar leva esta versão ao laboratório da QA. A decisão fica ligada a esta versão.')}</span></div>
      {asking ? <div className="bench-ask"><label className="field">{t('O que deve mudar no Code')}<textarea value={feedback} onChange={event => setFeedback(event.target.value)} rows={2} maxLength={8192} autoFocus /></label>
        <div className="pipeline-actions"><button type="button" className="touch-target secondary-button" onClick={() => setAsking(false)} disabled={pending}><Undo2 aria-hidden="true" />{t('Voltar')}</button><button type="button" className="touch-target secondary-button" onClick={() => void decide('request_revision')} disabled={pending || !feedback.trim()}><MessageSquareWarning aria-hidden="true" />{t('Pedir revisão do Code')}</button></div></div>
        : <div className="pipeline-actions"><button type="button" className="touch-target secondary-button" onClick={() => setAsking(true)} disabled={pending}><MessageSquareWarning aria-hidden="true" />{t('Pedir revisão')}</button><button type="button" className="touch-target primary-button" onClick={() => void decide('approve')} disabled={pending}><Check aria-hidden="true" />{pending ? t('Registrando…') : t('Aprovar Code e iniciar QA')}</button></div>}
    </div>}
    {artifact && <details className="bench-history"><summary>{t('Proveniência e integridade')}</summary><ol><li><strong>{t('Versão {version}', { version: artifact.version })}</strong><span className="mono">{t('SHA-256 do artefato: {digest}', { digest: artifact.contentDigest || t('indisponível') })}</span><span className="mono">{t('Sessão de origem: {session}', { session: artifact.sourceSessionId || t('indisponível') })}</span></li></ol></details>}
    {reviews.length > 0 && <details className="bench-history"><summary>{t('Decisões anteriores do Code ({count})', { count: reviews.length })}</summary><ol>{reviews.map((review, index) => <li key={index}><strong>{review.decision === 'approve' ? t('Aprovado') : t('Revisão pedida')} · {t('versão {version}', { version: review.version })}</strong><span className="muted">{review.actor} · {new Date(review.createdAt).toLocaleString(localeTag())}</span>{review.feedback && <p>{review.feedback}</p>}</li>)}</ol></details>}
    {error && <p className="form-error" role="alert">{error}</p>}
  </div>
}
