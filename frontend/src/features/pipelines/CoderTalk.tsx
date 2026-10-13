import { useEffect, useState } from 'react'
import { CircleHelp, MessageSquareReply, Play, TriangleAlert } from 'lucide-react'
import type { Backend, CoderStatus, Pipeline } from '../../lib/backend'
import { Markdown } from '../../components/Markdown'
import { useT } from '../../i18n'

/** Where the Coder of this work stands, read again whenever its run changes and every few seconds while Code is open. */
export function useCoderStatus(backend: Backend | undefined, run: Pipeline | undefined, runKey: string) {
  const [status, setStatus] = useState<CoderStatus>()
  const open = !!run && run.currentStage === 'code' && (run.stageStatus.code === 'active' || run.stageStatus.code === 'waiting_user')
  const pipelineId = run?.id
  useEffect(() => {
    if (!backend || !open || !pipelineId) { setStatus(undefined); return }
    let live = true
    const read = () => { backend.getPipelineCoderStatus(pipelineId).then(value => { if (live) setStatus(value) }, () => undefined) }
    read()
    const timer = setInterval(read, 5000)
    return () => { live = false; clearInterval(timer) }
  }, [backend, pipelineId, open, runKey, run?.revision])
  return status
}

/** The Coder needs the person (it asked, or stopped without saying it finished) */
export function coderNeedsPerson(status?: CoderStatus) {
  return !!status && (status.state === 'questions' || status.state === 'unfinished' || status.state === 'stopped')
}

const keepGoing = 'Continue implementando o plano até o fim. Se precisar de uma decisão ou informação minha, pergunte; quando terminar, diga com o status done.'

type Props = { status: CoderStatus; disabled: boolean; onReply: (text: string) => Promise<void> | void; /** The Code is waiting for review: the answer goes as a revision request. */ review?: boolean }

/** The Coder's questions, or where it stopped, with the person's answer: it goes on from there, and the review opens only
 *  when it says it finished. */
export function CoderTalk({ status, disabled, onReply, review = false }: Props) {
  const t = useT()
  const [answers, setAnswers] = useState<string[]>([])
  const [note, setNote] = useState('')
  const [sending, setSending] = useState(false)
  useEffect(() => { setAnswers([]); setNote('') }, [status.sessionId, status.message])
  const questions = status.questions
  const composed = [
    ...questions.map((question, index) => answers[index]?.trim() ? `${index + 1}. ${question}\n   ${answers[index].trim()}` : '').filter(Boolean),
    note.trim(),
  ].filter(Boolean).join('\n')
  async function send(text: string) {
    if (!text.trim() || disabled || sending) return
    setSending(true)
    try { await onReply(text) } finally { setSending(false) }
  }
  const asking = status.state === 'questions'
  const Icon = asking ? CircleHelp : TriangleAlert
  return <section className={`coder-talk is-${status.state}`} aria-labelledby="coder-talk-title">
    <header className="coder-talk-head">
      <span className="coder-talk-badge" aria-hidden="true"><Icon /></span>
      <div>
        <h4 id="coder-talk-title">{asking ? t('O Coder tem perguntas') : status.state === 'stopped' ? t('A execução do Coder parou') : t('O Coder parou sem dizer que terminou')}</h4>
        <p className="muted">{asking ? t('Responda para ele continuar de onde parou. A revisão do código abre quando ele disser que terminou.') : review ? t('Esta versão não foi dada como concluída pelo Coder. Responda ou peça para continuar: a resposta volta para o Code como pedido de revisão.') : t('Leia o que ele disse. Responda ou peça para continuar; a revisão do código abre quando ele disser que terminou.')}</p>
      </div>
    </header>
    {status.message && <details className="coder-talk-message" open={!asking}><summary>{t('Mensagem do Coder')}</summary><div className="coder-talk-markdown"><Markdown text={status.message} /></div></details>}
    {questions.length > 0 && <ol className="coder-talk-questions">{questions.map((question, index) => <li key={index}>
      <span className="coder-talk-number" aria-hidden="true">{index + 1}</span>
      <div><p>{question}</p>
        <label><span className="visually-hidden">{t('Resposta à pergunta {number}', { number: index + 1 })}</span>
          <textarea rows={2} value={answers[index] ?? ''} disabled={disabled || sending} placeholder={t('Sua resposta')} onChange={event => setAnswers(current => { const next = [...current]; next[index] = event.target.value; return next })} /></label>
      </div>
    </li>)}</ol>}
    <label className="coder-talk-note"><span>{questions.length ? t('Algo mais para o Coder (opcional)') : t('Sua resposta ao Coder')}</span>
      <textarea rows={2} value={note} disabled={disabled || sending} onChange={event => setNote(event.target.value)} placeholder={t('Uma decisão, um caminho, um detalhe que ele precisa saber')} /></label>
    <div className="coder-talk-actions">
      {!asking && <button type="button" className="touch-target secondary-button" disabled={disabled || sending} onClick={() => void send(keepGoing)}><Play aria-hidden="true" />{t('Pedir para continuar')}</button>}
      <button type="button" className="touch-target primary-button" disabled={disabled || sending || !composed} onClick={() => void send(composed)}><MessageSquareReply aria-hidden="true" />{review ? t('Pedir revisão com esta resposta') : t('Responder ao Coder')}</button>
    </div>
  </section>
}
