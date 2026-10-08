import { useEffect, useMemo, useRef, useState } from 'react'
import { Bug, Check, ChevronDown, CircleAlert, CircleDashed, FlaskConical, Hammer, LoaderCircle, MessageSquare, MonitorPlay, Play, RefreshCw, ScanSearch, Sparkles, TestTube2, Wrench } from 'lucide-react'
import { errorMessage, isDocumentCLI, type Backend, type BackendOption, type Pipeline, type PipelineRoleModelSelection, type QALoop } from '../../lib/backend'
import { IonPicker } from '../../components/IonPicker'
import { PipelineRoleModelPicker } from './PipelineRoleModelPicker'
import { StageActivityPane } from './StageActivityPane'
import { QALiveRun } from './QALiveRun'
import './qaLab.css'

type Check = { name: string; kind: string; command?: string; status: 'passed' | 'failed' | 'skipped'; summary?: string }
type Report = { passed?: boolean; checks?: Check[]; findings?: string[]; improvements?: string[]; criteria?: { criterion: string; evidence: string }[] }
export type RoleDefaults = { backendId: string; defaultModelBackendId: string; defaultModelId: string; phaseConfigured: boolean }

type Props = {
  backend: Backend
  backends: BackendOption[]
  run: Pipeline
  workspaceId?: string
  roleDefaults: (stage: 'code' | 'eval') => RoleDefaults
  onPipelineChange: (run: Pipeline) => void
  onOpenSession?: (sessionId: string) => void
  onLoopChange?: (loop: QALoop) => void
  onSettings?: () => void
  /** Saves who runs a role as the project's choice for that phase, so the next QA starts from it. */
  onRoleChosen?: (stage: 'code' | 'eval', backendId: string, modelId: string) => void
  /** QA's failures went back to Code and the fix loop stopped before its Coder round; the lab offers to go on. */
  resumable?: boolean
}

const kindIcons: Record<string, typeof TestTube2> = { build: Hammer, unit: TestTube2, e2e: MonitorPlay, run: Play, lint: ScanSearch, other: Wrench }
const kindLabels: Record<string, string> = { build: 'Build', unit: 'Testes unitários', e2e: 'E2E', run: 'Execução do app', lint: 'Lint', other: 'Verificação' }
const statusLabels = { passed: 'Passou', failed: 'Falhou', skipped: 'Não rodou' }

function readReport(content?: string): Report | undefined {
  if (!content) return undefined
  try { const value: unknown = JSON.parse(content); return value && typeof value === 'object' ? value as Report : undefined } catch { return undefined }
}

const eligible = (item: BackendOption) => item.available && (item.kind === 'api' || isDocumentCLI(item.id) && item.professionalAvailable === true)
const optionLabel = (item: BackendOption) => `${item.name}${!item.available ? ' · indisponível' : isDocumentCLI(item.id) && !item.professionalAvailable ? ' · indisponível no SDD' : ''}`

function RoleChoice({ backend, backends, workspaceId, stage, label, value, onChange, onSelection, onTouch, defaults, disabled }: {
  backend: Backend; backends: BackendOption[]; workspaceId?: string; stage: 'code' | 'eval'; label: string; disabled: boolean
  value: { backendId: string; selection?: PipelineRoleModelSelection }; onChange: (value: { backendId: string; selection?: PipelineRoleModelSelection }) => void; defaults: RoleDefaults
  onSelection: (backendId: string, selection?: PipelineRoleModelSelection) => void
  /** The person used this card (pointer or keyboard). */
  onTouch?: () => void
}) {
  const option = backends.find(item => item.id === value.backendId)
  const applies = defaults.phaseConfigured && value.backendId === defaults.backendId
  return <div className="qa-role" onPointerDownCapture={onTouch} onKeyDownCapture={onTouch}>
    <IonPicker id={`qa-role-${stage}`} label={label} value={value.backendId} disabled={disabled} onChange={backendId => onChange({ backendId })}
      options={[{ value: '', label: 'Escolha um executor' }, ...backends.map(item => ({ value: item.id, label: optionLabel(item), disabled: !eligible(item) }))]} />
    {option && <PipelineRoleModelPicker key={`${stage}:${value.backendId}`} backend={backend} workspaceId={workspaceId} stage={stage} backendOption={option}
      defaultModelBackendId={applies || !defaults.phaseConfigured ? defaults.defaultModelBackendId : ''} defaultModelId={applies || !defaults.phaseConfigured ? defaults.defaultModelId : ''} phaseConfigured={applies} disabled={disabled}
      onSelectionChange={selection => onSelection(value.backendId, selection)} />}
  </div>
}

function QARing({ passed, total }: { passed: number; total: number }) {
  const radius = 26, circumference = 2 * Math.PI * radius, share = total ? passed / total : 0
  return <div className="qa-ring" role="img" aria-label={`${passed} de ${total} verificações passaram`}>
    <svg viewBox="0 0 64 64" aria-hidden="true"><circle cx="32" cy="32" r={radius} className="qa-ring-track" /><circle cx="32" cy="32" r={radius} className="qa-ring-value" strokeDasharray={`${circumference * share} ${circumference}`} /></svg>
    <span><strong>{passed}</strong>/{total}</span>
  </div>
}

/** QA as a lab: it runs the project's checks, lists failures and improvements, fixes the chosen ones and runs again. */
export function PipelineQALab({ backend, backends, run, workspaceId, roleDefaults, onPipelineChange, onOpenSession, onLoopChange, onSettings, onRoleChosen, resumable = false }: Props) {
  const [loop, setLoop] = useState<QALoop>()
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const qaDefaults = roleDefaults('eval'), codeDefaults = roleDefaults('code')
  const [qaRole, setQARole] = useState<{ backendId: string; selection?: PipelineRoleModelSelection }>({ backendId: qaDefaults.backendId })
  const [codeRole, setCodeRole] = useState<{ backendId: string; selection?: PipelineRoleModelSelection }>({ backendId: codeDefaults.backendId })
  const report = useMemo(() => readReport(run.artifacts.eval?.content), [run.artifacts.eval?.content])
  const evalArtifact = run.artifacts.eval
  const waiting = run.currentStage === 'eval' && run.stageStatus.eval === 'waiting_user' && !!report
  // A report left from before the latest Code: QA is due again, the old findings are only for reading.
  const stale = run.currentStage === 'eval' && run.stageStatus.eval === 'active' && !!report
  const [chosenFindings, setChosenFindings] = useState<Set<number>>(new Set())
  const [chosenImprovements, setChosenImprovements] = useState<Set<number>>(new Set())
  const [note, setNote] = useState('')
  const lastLoopKey = useRef('')
  // Failed checks come open, with their command and output; the others open on a click.
  const [expanded, setExpanded] = useState<Set<number>>(new Set())
  useEffect(() => { setExpanded(new Set((report?.checks ?? []).flatMap((item, index) => item.status === 'failed' ? [index] : []))) }, [evalArtifact?.version, evalArtifact?.contentDigest])

  // Until the person picks, each role follows what the project chose for its phase (which may load after the first render).
  const touched = useRef({ qa: false, code: false })
  useEffect(() => { if (!touched.current.qa && qaDefaults.backendId !== qaRole.backendId) setQARole({ backendId: qaDefaults.backendId }) }, [qaDefaults.backendId])
  useEffect(() => { if (!touched.current.code && codeDefaults.backendId !== codeRole.backendId) setCodeRole({ backendId: codeDefaults.backendId }) }, [codeDefaults.backendId])
  const picked = useRef({ code: false, eval: false })
  // Picking the same executor again keeps its model; another one waits for its own model.
  const pickQA = (value: { backendId: string }) => { touched.current.qa = true; setQARole(current => current.backendId === value.backendId ? current : { backendId: value.backendId }) }
  const pickCode = (value: { backendId: string }) => { touched.current.code = true; setCodeRole(current => current.backendId === value.backendId ? current : { backendId: value.backendId }) }
  // A model picker answers late; its choice only counts for the executor it was made for. A choice that differs from
  // the project's for the phase becomes the project's, so the next QA starts from it.
  const modelFor = (stage: 'code' | 'eval', set: typeof setQARole) => (backendId: string, selection?: PipelineRoleModelSelection) => {
    set(current => current.backendId === backendId ? { backendId, selection } : current)
    const defaults = stage === 'eval' ? qaDefaults : codeDefaults
    // Only a pick the person made counts: answers that arrive while the defaults load are not choices.
    if (picked.current[stage] && selection?.modelId && (backendId !== defaults.backendId || selection.modelId !== defaults.defaultModelId)) onRoleChosen?.(stage, backendId, selection.modelId)
  }
  // Failures come chosen; improvements wait for the person.
  useEffect(() => {
    setChosenFindings(new Set((report?.findings ?? []).map((_, index) => index)))
    setChosenImprovements(new Set())
    setNote('')
  }, [evalArtifact?.version, evalArtifact?.contentDigest])

  // Follow the background loop; each change of phase refreshes the pipeline.
  useEffect(() => {
    let live = true
    let timer: ReturnType<typeof setTimeout>
    const poll = async () => {
      try {
        const current = await backend.getPipelineQALoop(run.id)
        if (!live) return
        setLoop(current)
        onLoopChange?.(current)
        const key = `${current.phase}:${current.round}:${current.updatedAt}`
        if (key !== lastLoopKey.current) {
          lastLoopKey.current = key
          if (current.phase) void backend.getPipeline(run.id).then(value => { if (live) onPipelineChange(value) }, () => undefined)
        }
        timer = setTimeout(() => void poll(), current.running ? 1500 : 5000)
      } catch { if (live) timer = setTimeout(() => void poll(), 5000) }
    }
    void poll()
    return () => { live = false; clearTimeout(timer) }
  }, [backend, run.id])

  const running = !!loop?.running
  // While QA tests, the lab shows the run as it happens instead of the previous report.
  const testing = running && loop?.phase === 'qa'
  const findings = report?.findings ?? [], improvements = report?.improvements ?? []
  const checks = report?.checks ?? []
  const counts = { passed: checks.filter(item => item.status === 'passed').length, failed: checks.filter(item => item.status === 'failed').length, skipped: checks.filter(item => item.status === 'skipped').length }
  const canRun = run.currentStage === 'eval' && run.stageStatus.eval === 'active' && !running && !!qaRole.backendId && !!qaRole.selection
  const chosenCount = chosenFindings.size + chosenImprovements.size + (note.trim() ? 1 : 0)
  const canFix = waiting && !running && chosenCount > 0 && !!codeRole.backendId && !!codeRole.selection && !!qaRole.backendId && !!qaRole.selection
  const canResume = resumable && !running && !!codeRole.backendId && !!codeRole.selection && !!qaRole.backendId && !!qaRole.selection
  const canApprove = waiting && report?.passed === true && !running && !!evalArtifact?.contentDigest

  async function start() {
    if (!qaRole.selection) { setError('Escolha um modelo confirmado para o QA.'); return }
    setPending(true); setError('')
    try { const state = await backend.startPipelineQA(run.id, { backendId: qaRole.backendId, selection: qaRole.selection }); setLoop(state); onLoopChange?.(state) }
    catch (failure) { setError(errorMessage(failure)) } finally { setPending(false) }
  }

  async function fix() {
    if (!codeRole.selection || !qaRole.selection) { setError('Escolha os modelos do Code e do QA para as correções.'); return }
    setPending(true); setError('')
    try {
      const state = await backend.fixPipelineFindings({ pipelineId: run.id, findings: findings.filter((_, index) => chosenFindings.has(index)), improvements: improvements.filter((_, index) => chosenImprovements.has(index)), note: note.trim(),
        coder: { backendId: codeRole.backendId, selection: codeRole.selection }, evaluator: { backendId: qaRole.backendId, selection: qaRole.selection } })
      setLoop(state); onLoopChange?.(state)
    } catch (failure) { setError(errorMessage(failure)) } finally { setPending(false) }
  }

  async function resume() {
    if (!codeRole.selection || !qaRole.selection) { setError('Escolha os modelos do Code e do QA para as correções.'); return }
    setPending(true); setError('')
    try { const state = await backend.resumePipelineFixes({ pipelineId: run.id, coder: { backendId: codeRole.backendId, selection: codeRole.selection }, evaluator: { backendId: qaRole.backendId, selection: qaRole.selection } }); setLoop(state); onLoopChange?.(state) }
    catch (failure) { setError(errorMessage(failure)) } finally { setPending(false) }
  }

  // The evaluator may have answered with a valid report that an earlier reading refused; read it again without a new run.
  async function readAgain() {
    setPending(true); setError('')
    try { onPipelineChange(await backend.completePipelineEvaluation(run.id)) }
    catch (failure) { setError(errorMessage(failure)) } finally { setPending(false) }
  }

  async function approve() {
    if (!evalArtifact?.contentDigest) return
    setPending(true); setError('')
    try {
      onPipelineChange(await backend.decidePipelineExecutionArtifact({ pipelineId: run.id, requestId: crypto.randomUUID(), stage: 'eval', artifactVersion: evalArtifact.version, artifactDigest: evalArtifact.contentDigest, decision: 'approve', feedback: '', pipelineRevision: run.revision }))
    } catch (failure) { setError(errorMessage(failure)) } finally { setPending(false) }
  }

  const toggle = (set: Set<number>, index: number, update: (next: Set<number>) => void) => { const next = new Set(set); if (next.has(index)) next.delete(index); else next.add(index); update(next) }
  const verdict = running ? 'running' : !report ? 'idle' : report.passed ? 'passed' : 'failed'
  const verdictText = { running: loop?.phase === 'fixing' ? `Corrigindo · rodada ${loop.round}` : `QA rodando · rodada ${loop?.round ?? 1}`, idle: 'Pronto para rodar', passed: 'Passou', failed: 'Encontrou problemas' }[verdict]

  return <section className="qa-lab" aria-labelledby="qa-lab-title">
    <header className="qa-lab-header">
      <span className={`qa-lab-badge is-${verdict}`} aria-hidden="true">{verdict === 'running' ? <LoaderCircle /> : verdict === 'passed' ? <Check /> : verdict === 'failed' ? <CircleAlert /> : <FlaskConical />}</span>
      <div><h3 id="qa-lab-title" className="visually-hidden">Laboratório de QA</h3><strong className="qa-lab-verdict">{report && !testing ? 'Relatório do laboratório' : running ? 'O laboratório está trabalhando' : 'Laboratório pronto'}</strong><p className="muted">{report && !testing ? `${stale ? 'Rodada anterior · ' : ''}${checks.filter(item => item.status === 'passed').length} de ${checks.length} verificações passaram · ${findings.length} ${findings.length === 1 ? 'falha' : 'falhas'} · ${improvements.length} ${improvements.length === 1 ? 'melhoria sugerida' : 'melhorias sugeridas'}` : 'Nada do que o QA instala ou gera vai para o seu projeto.'}</p></div>
      <span className={`qa-lab-status is-${verdict}`} role="status">{verdictText}</span>
    </header>

    {!backends.some(eligible) ? <div className="authoring-provider-empty" role="alert"><strong>Code e QA precisam de um provedor API</strong><p>Configure um perfil API, ou habilite o Codex ou o Claude Code para o SDD, para o QA rodar as verificações.</p>{onSettings && <button type="button" className="touch-target secondary-button" onClick={onSettings}>Configurar provedor API</button>}</div>
    : (run.currentStage === 'eval' || running || resumable) && <>{backends.some(item => item.available && isDocumentCLI(item.id) && !item.professionalAvailable) && <p className="project-warning" role="status">O Codex CLI pode ler arquivos fora do projeto mesmo em sandbox somente leitura; aqui ele fica disponível só quando o Harflex controla as ferramentas dele.</p>}
    <div className="qa-lab-roles">
      <RoleChoice backend={backend} backends={backends} workspaceId={workspaceId} stage="eval" label="Quem testa (QA)" value={qaRole} onChange={pickQA} onSelection={modelFor('eval', setQARole)} onTouch={() => { picked.current.eval = true }} defaults={qaDefaults} disabled={running || pending} />
      <RoleChoice backend={backend} backends={backends} workspaceId={workspaceId} stage="code" label="Quem corrige (Code)" value={codeRole} onChange={pickCode} onSelection={modelFor('code', setCodeRole)} onTouch={() => { picked.current.code = true }} defaults={codeDefaults} disabled={running || pending} />
    </div></>}

    {running && loop && <div className="qa-lab-live" aria-live="polite">
      <div className="qa-lab-live-row"><LoaderCircle className="qa-spin" aria-hidden="true" /><strong>{loop.message}</strong>
        {loop.sessionId && onOpenSession && <button type="button" className="touch-target text-button" onClick={() => onOpenSession(loop.sessionId!)}><MessageSquare aria-hidden="true" />Ver a conversa</button>}</div>
      {loop.phase === 'fixing' && <StageActivityPane key={`${run.id}:${loop.round}:${loop.sessionId ?? ''}`} backend={backend} pipeline={run} stage="code" />}
    </div>}
    {testing && <QALiveRun key={loop?.sessionId ?? 'starting'} backend={backend} sessionId={loop?.sessionId} />}
    {!running && loop?.phase === 'failed' && run.stageStatus[run.currentStage] !== 'waiting_user' && <div className="qa-lab-alert" role="alert"><CircleAlert aria-hidden="true" /><p>{loop.message}</p>
      {loop.sessionId && run.currentStage === 'eval' && run.stageStatus.eval === 'active' && <button type="button" className="touch-target secondary-button" onClick={() => void readAgain()} disabled={pending}>{pending ? 'Lendo…' : 'Ler o relatório de novo'}</button>}
      {loop.sessionId && onOpenSession && <button type="button" className="touch-target secondary-button" onClick={() => onOpenSession(loop.sessionId!)}>Ver a conversa</button>}</div>}

    {!report && !running && backends.some(eligible) && <div className="qa-lab-empty">
      <FlaskConical aria-hidden="true" />
      <p>Quando você rodar, o QA descobre os comandos do projeto, executa as verificações e traz um relatório com falhas e melhorias.</p>
      <button type="button" className="touch-target primary-button" onClick={() => void start()} disabled={pending || !canRun}><Play aria-hidden="true" />{pending ? 'Iniciando…' : 'Rodar QA'}</button>
    </div>}

    {resumable && !running && backends.some(eligible) && <div className="qa-lab-rerun" role="status">
      <RefreshCw aria-hidden="true" />
      <p><strong>As correções pararam no meio.</strong> As falhas do QA já voltaram para o Code, mas a rodada do Coder não começou. Continue de onde parou.</p>
      <button type="button" className="touch-target primary-button" onClick={() => void resume()} disabled={pending || !canResume}><Wrench aria-hidden="true" />{pending ? 'Retomando…' : 'Continuar as correções'}</button>
    </div>}

    {stale && !running && backends.some(eligible) && <div className="qa-lab-rerun" role="status">
      <RefreshCw aria-hidden="true" />
      <p><strong>O Code mudou depois deste relatório.</strong> Rode o QA de novo para testar a versão nova; o relatório abaixo é da rodada anterior.</p>
      <button type="button" className="touch-target primary-button" onClick={() => void start()} disabled={pending || !canRun}><Play aria-hidden="true" />{pending ? 'Iniciando…' : 'Rodar QA de novo'}</button>
    </div>}

    {report && !testing && <>
      {checks.length === 0 ? <p className="muted">Este relatório não traz verificações executadas.</p> : <>
        <div className="qa-summary">
          <QARing passed={counts.passed} total={checks.length} />
          <div className="qa-summary-body">
            <div className="qa-summary-bar" role="img" aria-label={`${counts.passed} passaram, ${counts.failed} falharam, ${counts.skipped} não rodaram`}>
              {counts.passed > 0 && <i className="is-passed" style={{ flexGrow: counts.passed }} />}{counts.failed > 0 && <i className="is-failed" style={{ flexGrow: counts.failed }} />}{counts.skipped > 0 && <i className="is-skipped" style={{ flexGrow: counts.skipped }} />}
            </div>
            <div className="qa-summary-legend"><span className="is-passed">{counts.passed} passaram</span><span className="is-failed">{counts.failed} falharam</span><span className="is-skipped">{counts.skipped} não rodaram</span></div>
          </div>
        </div>
        <ol className="qa-run" aria-label="Verificações">{checks.map((check, index) => { const Icon = kindIcons[check.kind] ?? Wrench; const open = expanded.has(index); return <li key={index} className={`qa-run-row is-${check.status}${open ? ' is-open' : ''}`}>
          <button type="button" className="qa-run-head" aria-expanded={open} onClick={() => setExpanded(current => { const next = new Set(current); if (next.has(index)) next.delete(index); else next.add(index); return next })}>
            <span className="qa-run-status" aria-hidden="true">{check.status === 'passed' ? <Check /> : check.status === 'failed' ? <CircleAlert /> : <CircleDashed />}</span>
            <strong>{check.name}</strong>
            <span className="qa-run-kind"><Icon aria-hidden="true" />{kindLabels[check.kind] ?? check.kind}</span>
            <span className="qa-run-result">{statusLabels[check.status]}</span>
            <ChevronDown className="qa-run-chevron" aria-hidden="true" />
          </button>
          {open && <div className="qa-run-output">
            {check.command && <code className="mono"><span aria-hidden="true">$ </span>{check.command}</code>}
            {check.summary && <p>{check.summary}</p>}
          </div>}
        </li> })}</ol>
      </>}

      <div className="qa-lab-lists">
        <section className="qa-list is-findings" aria-labelledby="qa-findings-title">
          <h4 id="qa-findings-title"><Bug aria-hidden="true" />Falhas <span>{findings.length}</span></h4>
          {findings.length === 0 ? <p className="muted">Nenhuma falha encontrada.</p> : <ul>{findings.map((item, index) => <li key={index}>{waiting ? <label><input type="checkbox" checked={chosenFindings.has(index)} disabled={running} onChange={() => toggle(chosenFindings, index, setChosenFindings)} /><span>{item}</span></label> : <p className="qa-list-item">{item}</p>}</li>)}</ul>}
        </section>
        <section className="qa-list is-improvements" aria-labelledby="qa-improvements-title">
          <h4 id="qa-improvements-title"><Sparkles aria-hidden="true" />Melhorias sugeridas <span>{improvements.length}</span></h4>
          {improvements.length === 0 ? <p className="muted">Nenhuma melhoria sugerida.</p> : <ul>{improvements.map((item, index) => <li key={index}>{waiting ? <label><input type="checkbox" checked={chosenImprovements.has(index)} disabled={running} onChange={() => toggle(chosenImprovements, index, setChosenImprovements)} /><span>{item}</span></label> : <p className="qa-list-item">{item}</p>}</li>)}</ul>}
        </section>
      </div>

      {!!report.criteria?.length && <details className="qa-lab-criteria"><summary>Critérios de aceite e evidências ({report.criteria.length})</summary><dl>{report.criteria.map((item, index) => <div key={index}><dt>{item.criterion}</dt><dd>{item.evidence}</dd></div>)}</dl></details>}

      {waiting && <div className="qa-lab-decide">
        <label className="field">Algo mais para corrigir (opcional)<textarea value={note} onChange={event => setNote(event.target.value)} rows={2} maxLength={4000} placeholder="Ex.: deixe a mensagem de erro mais clara" disabled={running || pending} /></label>
        <div className="pipeline-actions">
          <button type="button" className={`touch-target ${report.passed ? 'secondary-button' : 'primary-button'}`} onClick={() => void fix()} disabled={pending || !canFix}><Wrench aria-hidden="true" />{chosenCount ? `Corrigir os selecionados (${chosenCount})` : 'Corrigir os selecionados'}</button>
          {report.passed && <button type="button" className="touch-target primary-button" onClick={() => void approve()} disabled={pending || !canApprove}><Check aria-hidden="true" />Aprovar QA e seguir para PRs</button>}
        </div>
        {chosenCount === 0 && <p className="muted qa-lab-hint" role="status">Marque as falhas ou melhorias que quer corrigir, ou escreva o que mudar.</p>}
        {chosenCount > 0 && (!codeRole.selection || !qaRole.selection) && <p className="muted qa-lab-hint" role="status">Confirmando os modelos de quem corrige e de quem testa…</p>}
        <p className="muted qa-lab-hint">As correções voltam ao Code na mesma cópia e o QA roda de novo sozinho, até passar. Só para e chama você se as mesmas falhas voltarem.</p>
      </div>}
    </>}
    {error && <p className="form-error" role="alert">{error}</p>}
  </section>
}
