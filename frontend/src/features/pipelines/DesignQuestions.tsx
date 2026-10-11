import { Fragment, useMemo, useState, type ReactNode } from 'react'
import { Check, ChevronDown, ListChecks, PencilLine, Send, Sparkles, Undo2 } from 'lucide-react'
import type { Backend, PipelineDesign, PipelineDesignStage } from '../../lib/backend'
import { useT } from '../../i18n'
import { designLabels, designStageOrder } from './PipelineDesignDocument'
import { answered, answersRequest, designQuestions, type DesignAnswer, type DesignQuestion } from './designQuestionParser'

// Answers in progress live only in this renderer's memory, per work and per document version: a new version of the
// document starts clean, because its decisions may have changed.
const answerMemory = new WeakMap<Backend, Map<string, Record<string, DesignAnswer>>>()
function remembered(backend: Backend, key: string) { return answerMemory.get(backend)?.get(key) ?? {} }
function remember(backend: Backend, key: string, value: Record<string, DesignAnswer>) {
  let all = answerMemory.get(backend)
  if (!all) { all = new Map(); answerMemory.set(backend, all) }
  if (Object.keys(value).length) all.set(key, value); else all.delete(key)
}

/** `code` and **bold**, the inline marks the documents use in decisions. */
function Inline({ text }: { text: string }) {
  const parts = text.split(/(`[^`]+`)/g)
  return <>{parts.map((part, index) => part.startsWith('`') && part.endsWith('`') && part.length > 2 ? <code key={index}>{part.slice(1, -1)}</code> : <Fragment key={index}>{part}</Fragment>)}</>
}

type Props = { backend: Backend; design: PipelineDesign; disabled: boolean; onSend: (message: string, target: PipelineDesignStage | 'all') => void }

export function DesignQuestions({ backend, design, disabled, onSend }: Props) {
  const t = useT()
  const items = useMemo(() => designStageOrder.flatMap(stage => designQuestions(stage, design.documents[stage]?.content ?? '')), [design.documents.discovery?.contentDigest, design.documents.spec?.contentDigest, design.documents.plan?.contentDigest, design.documents.discovery?.content, design.documents.spec?.content, design.documents.plan?.content])
  const memoryKey = `${design.workspaceId}:${design.pipelineId}:${designStageOrder.map(stage => design.documents[stage]?.contentDigest || design.documents[stage]?.version || 0).join(':')}`
  const [state, setState] = useState(() => ({ key: memoryKey, answers: remembered(backend, memoryKey) }))
  const answers = state.key === memoryKey ? state.answers : remembered(backend, memoryKey)
  const stages = designStageOrder.filter(stage => items.some(item => item.stage === stage))
  const [chosenStage, setChosenStage] = useState<PipelineDesignStage>()
  const shownStage = chosenStage && stages.includes(chosenStage) ? chosenStage : stages.includes('spec') ? 'spec' : stages[0]
  const shown = items.filter(item => item.stage === shownStage)
  const done = items.filter(item => answered(item, answers[item.key]))
  const toSend = done.filter(item => answers[item.key]?.choice !== 'keep')

  function set(item: DesignQuestion, answer?: DesignAnswer) {
    const next = { ...answers }
    if (answer) next[item.key] = answer; else delete next[item.key]
    remember(backend, memoryKey, next)
    setState({ key: memoryKey, answers: next })
  }
  function send() {
    if (!done.length || disabled) return
    const touched = [...new Set(done.map(item => item.stage))]
    onSend(answersRequest(items, answers), touched.length === 1 ? touched[0] : 'all')
    remember(backend, memoryKey, {})
    setState({ key: memoryKey, answers: {} })
  }

  if (items.length === 0) return null
  const percent = Math.round(done.length / items.length * 100)
  return <section className="design-questions" aria-labelledby="design-questions-title">
    <header className="design-questions-head">
      <div className="design-questions-ring" style={{ ['--done' as string]: `${percent}%` }} aria-hidden="true"><span>{done.length}</span></div>
      <div>
        <h4 id="design-questions-title"><ListChecks aria-hidden="true" />{t('Decisões e perguntas')}</h4>
        <p className="muted">{t('{done} de {total} respondidas. Confirme, mude ou responda; a IA ajusta os documentos.', { done: done.length, total: items.length })}</p>
      </div>
    </header>
    {stages.length > 1 && <div className="design-questions-tabs" role="tablist" aria-label={t('Documento')}>{stages.map(stage => {
      const total = items.filter(item => item.stage === stage)
      const open = total.filter(item => !answered(item, answers[item.key])).length
      return <button key={stage} type="button" role="tab" aria-selected={stage === shownStage} className="design-questions-tab" onClick={() => setChosenStage(stage)}>{designLabels[stage]}<span className={open ? 'is-open' : 'is-done'}>{open || <Check aria-hidden="true" />}</span></button>
    })}</div>}
    <ol className="design-questions-list">{shown.map(item => <QuestionCard key={item.key} item={item} answer={answers[item.key]} disabled={disabled} onAnswer={answer => set(item, answer)} />)}</ol>
    <footer className="design-questions-send">
      <span className="muted">{toSend.length ? t('{count} para a IA ajustar', { count: toSend.length }) : done.length ? t('Tudo confirmado') : t('Nada respondido ainda')}</span>
      <button type="button" className="touch-target primary-button" disabled={!done.length || disabled} onClick={send}><Send aria-hidden="true" />{t('Enviar respostas para a IA')}</button>
    </footer>
  </section>
}

function QuestionCard({ item, answer, disabled, onAnswer }: { item: DesignQuestion; answer?: DesignAnswer; disabled: boolean; onAnswer: (answer?: DesignAnswer) => void }) {
  const t = useT()
  const [why, setWhy] = useState(false)
  const isDone = answered(item, answer)
  const tone = !isDone ? 'is-open' : answer?.choice === 'keep' ? 'is-kept' : 'is-changed'
  const choice = (value: DesignAnswer['choice'], label: string, icon: ReactNode, extra?: Partial<DesignAnswer>) =>
    <button type="button" className="design-choice" aria-pressed={answer?.choice === value && (value !== 'option' || answer.option === extra?.option)} disabled={disabled}
      onClick={() => onAnswer(answer?.choice === value && (value !== 'option' || answer.option === extra?.option) ? undefined : { choice: value, ...extra, text: value === 'change' || value === 'other' ? answer?.text ?? '' : undefined })}>{icon}<span>{label}</span></button>
  return <li className={`design-question ${tone}`}>
    <div className="design-question-top">
      <span className={`design-question-id is-${item.kind}`}>{item.id}</span>
      <span className="design-question-kind">{item.kind === 'decision' ? t('Decisão') : t('Pergunta')}</span>
      {isDone && <span className="design-question-state"><Check aria-hidden="true" />{answer?.choice === 'keep' ? t('Mantida') : t('Respondida')}</span>}
    </div>
    <p className="design-question-title"><Inline text={item.title} /></p>
    {item.decision && <div className="design-question-decision"><span className="design-question-decision-label">{t('A IA decidiu')}</span><p><Inline text={item.decision} /></p></div>}
    {item.rationale && <><button type="button" className="design-question-why" aria-expanded={why} onClick={() => setWhy(value => !value)}><ChevronDown aria-hidden="true" />{t('Por quê')}</button>{why && <p className="design-question-rationale muted"><Inline text={item.rationale} /></p>}</>}
    <div className="design-choices">
      {item.kind === 'decision' ? <>
        {choice('keep', t('Manter'), <Check aria-hidden="true" />)}
        {choice('change', t('Mudar'), <PencilLine aria-hidden="true" />)}
      </> : <>
        {item.options.map(option => <span key={option} className="design-choice-wrap">{choice('option', option, <span className="design-choice-dot" aria-hidden="true" />, { option })}</span>)}
        {choice('other', item.options.length ? t('Outra resposta') : t('Responder'), <PencilLine aria-hidden="true" />)}
        {choice('delegate', t('A IA decide'), <Sparkles aria-hidden="true" />)}
      </>}
      {isDone && <button type="button" className="design-question-undo" disabled={disabled} onClick={() => onAnswer(undefined)}><Undo2 aria-hidden="true" />{t('Desfazer')}</button>}
    </div>
    {(answer?.choice === 'change' || answer?.choice === 'other') && <label className="design-question-answer">
      <span className="visually-hidden">{answer.choice === 'change' ? t('Como deve ser a decisão {id}?', { id: item.id }) : t('Sua resposta para {id}', { id: item.id })}</span>
      <textarea rows={2} value={answer.text ?? ''} disabled={disabled} autoFocus placeholder={answer.choice === 'change' ? t('Como deve ser?') : t('Sua resposta')} onChange={event => onAnswer({ ...answer, text: event.target.value })} />
    </label>}
  </li>
}
