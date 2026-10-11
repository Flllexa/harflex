import type { PipelineDesignStage } from '../../lib/backend'

/**
 * The decisions and open questions a document records, read from its Markdown so the person can answer them as cards. The
 * documents keep the decisions in a "Decisões" table (# | Questão | Decisão | Justificativa) and the questions in a
 * "Perguntas em aberto" list, with their options as sub-items or as (a) … (b) …; anything else is left alone.
 */
export type DesignQuestion = {
  key: string
  stage: PipelineDesignStage
  kind: 'decision' | 'question'
  id: string
  title: string
  /** What the document decided (a decision), or nothing for an open question. */
  decision: string
  rationale: string
  options: string[]
}

const decisionHeading = /decis/i
const questionHeading = /pergunta|d[uú]vida|em aberto|open question|pend[eê]nci|a confirmar|to confirm|preguntas|abiertas/i
const listItem = /^(\s*)(?:[-*+]|\d+[.)])\s+(.*)$/
const leadingId = /^\**\s*\[?([A-Z]{1,2}\d{1,3})\]?\s*\**\s*[—–\-:.·)]*\s*/
const separatorRow = /^\|?\s*:?-{2,}/

function cells(row: string): string[] {
  const inner = row.trim().replace(/^\|/, '').replace(/\|$/, '')
  return inner.split(/(?<!\\)\|/).map(cell => cell.trim().replace(/\\\|/g, '|'))
}

function clean(text: string): string {
  return text.replace(/\*\*/g, '').replace(/\s+/g, ' ').trim()
}

/** Splits "Destino? (a) só produção (b) sandbox também" into the question and its options. */
function inlineOptions(text: string): { title: string; options: string[] } {
  const marker = /\(([a-eA-E])\)\s*/g
  const found = [...text.matchAll(marker)]
  if (found.length < 2) return { title: text, options: [] }
  const options = found.map((match, index) => {
    const start = (match.index ?? 0) + match[0].length
    const end = index + 1 < found.length ? found[index + 1].index ?? text.length : text.length
    return clean(text.slice(start, end).replace(/[;,]\s*(ou|or|o)?\s*$/i, ''))
  }).filter(Boolean)
  return { title: clean(text.slice(0, found[0].index)).replace(/[:—–-]\s*$/, ''), options }
}

function column(header: string[], pattern: RegExp): number {
  return header.findIndex(cell => pattern.test(cell))
}

export function designQuestions(stage: PipelineDesignStage, markdown: string): DesignQuestion[] {
  const items: DesignQuestion[] = []
  const lines = markdown.split('\n')
  let section: 'decision' | 'question' | '' = ''
  let table: string[][] = []
  let current: DesignQuestion | undefined
  let baseIndent = -1
  const flushTable = () => {
    if (table.length < 2) { table = []; return }
    const [header, ...rows] = table
    const idCol = column(header, /^#$|^id$|^n[º°o.]?$/i)
    const decisionCol = column(header, /decis/i)
    const questionCol = column(header, /quest|pergunta|tema|assunto|question|topic|ponto|item/i)
    const whyCol = column(header, /justific|motivo|por ?qu|rationale|why|raz[aã]o/i)
    rows.forEach((row, index) => {
      if (row.every(cell => !cell)) return
      const pick = (at: number, fallback: number) => row[at >= 0 ? at : fallback] ?? ''
      const id = clean(pick(idCol, -1)) || `D${index + 1}`
      const decision = clean(pick(decisionCol, header.length >= 3 ? 2 : 1))
      const title = clean(pick(questionCol, header.length >= 3 ? 1 : 0))
      if (!title && !decision) return
      items.push({ key: `${stage}:decision:${id}:${index}`, stage, kind: 'decision', id, title: title || decision, decision: title ? decision : '', rationale: clean(pick(whyCol, -1)), options: [] })
    })
    table = []
  }
  for (const line of lines) {
    const heading = /^#{1,6}\s+(.*)$/.exec(line)
    if (heading) {
      flushTable(); current = undefined; baseIndent = -1
      const text = heading[1]
      section = decisionHeading.test(text) ? 'decision' : questionHeading.test(text) ? 'question' : ''
      continue
    }
    if (!section) continue
    if (line.trim().startsWith('|')) {
      if (section === 'decision' && !separatorRow.test(line.trim())) table.push(cells(line))
      continue
    }
    flushTable()
    const item = listItem.exec(line)
    if (!item) {
      // A paragraph under a question continues it; a blank line keeps the item open for its options.
      if (current && line.trim() && !/^\s/.test(line) && baseIndent >= 0) current = undefined
      continue
    }
    const indent = item[1].replace(/\t/g, '  ').length
    const text = item[2]
    if (current && baseIndent >= 0 && indent > baseIndent) {
      if (current.kind === 'question') current.options.push(clean(text))
      continue
    }
    baseIndent = indent
    const idMatch = leadingId.exec(text)
    const body = idMatch ? text.slice(idMatch[0].length) : text
    if (section === 'question') {
      const { title, options } = inlineOptions(body)
      const id = idMatch?.[1] ?? `P${items.filter(other => other.stage === stage && other.kind === 'question').length + 1}`
      current = { key: `${stage}:question:${id}:${items.length}`, stage, kind: 'question', id, title: clean(title), decision: '', rationale: '', options }
    } else {
      const id = idMatch?.[1] ?? `D${items.filter(other => other.stage === stage && other.kind === 'decision').length + 1}`
      const [head, ...rest] = clean(body).split(/:\s+|\s+[—–]\s+/)
      current = { key: `${stage}:decision:${id}:${items.length}`, stage, kind: 'decision', id, title: rest.length ? head : clean(body), decision: rest.join(': '), rationale: '', options: [] }
    }
    if (current.title) items.push(current)
  }
  flushTable()
  return items
}

export type DesignAnswer = { choice: 'keep' | 'change' | 'option' | 'other' | 'delegate'; option?: string; text?: string }

export function answered(item: DesignQuestion, answer?: DesignAnswer): boolean {
  if (!answer) return false
  if (answer.choice === 'change' || answer.choice === 'other') return !!answer.text?.trim()
  if (answer.choice === 'option') return !!answer.option
  return true
}

const stageNames: Record<PipelineDesignStage, string> = { discovery: 'Discovery', spec: 'SPEC', plan: 'Plan' }

export const answersHeader = 'Respostas às decisões e perguntas dos documentos:'
const keepText = 'manter como está'
const lineFor = (item: DesignQuestion) => `- ${item.id} (${stageNames[item.stage]}: ${item.title}): `

/** What the person decided, as a request to the AI. It is the AI's prompt, so it stays in Portuguese like the others. Each
 *  answer is one line naming the item and its document, so the answers can be read back from the conversation later. */
export function answersRequest(items: DesignQuestion[], answers: Record<string, DesignAnswer>): string {
  const lines: string[] = []
  for (const item of items) {
    const answer = answers[item.key]
    if (!answered(item, answer)) continue
    const text = answer!.choice === 'keep' ? keepText
      : answer!.choice === 'change' ? `mudar a decisão para: ${answer!.text!.trim()}`
        : answer!.choice === 'option' ? `${answer!.option}${answer!.text?.trim() ? ` (${answer!.text.trim()})` : ''}`
          : answer!.choice === 'delegate' ? 'decida você, registre a decisão e a justificativa'
            : answer!.text!.trim()
    lines.push(`${lineFor(item)}${text.replace(/\s*\n\s*/g, ' ')}`)
  }
  return [answersHeader, lines.join('\n'), 'Atualize os documentos com essas respostas: ajuste as decisões, tire das perguntas em aberto o que foi respondido e mantenha como está o que foi marcado para manter.'].join('\n\n')
}

export type SentAnswer = { text: string; keep: boolean; messageId: string }

// A decision the AI wrote from the person's answer: "(resposta à P1)" in its title or "Decisão da pessoa" as its rationale.
const fromPerson = /resposta (?:à|a|da) P\d+|decis[aã]o da pessoa|answer to P\d+|the person'?s decision|respuesta a P\d+|decisi[oó]n de la persona/i

/** The answer a line gives to an item, when the line names it: same id and document, and a title that is the item's or the
 *  start of it (the AI often extends a decision's title when it rewrites it with the answer). */
function answerOn(line: string, item: DesignQuestion): string | undefined {
  const head = `- ${item.id} (${stageNames[item.stage]}: `
  if (!line.startsWith(head)) return undefined
  const rest = line.slice(head.length)
  for (let at = rest.indexOf('): '); at >= 0; at = rest.indexOf('): ', at + 1)) {
    const title = rest.slice(0, at).trim()
    if (title && (item.title === title || item.title.startsWith(title) || title.startsWith(item.title))) return rest.slice(at + 3).trim()
  }
  return undefined
}

/**
 * The answers the person already gave, read back from the preparation conversation (the latest answer to an item wins), and
 * the decisions the AI wrote from their answers. An item counts only when its id, document and title still match, so a
 * question the AI renumbered is not taken for another. Messages sent before titles were written ("Decisões confirmadas:
 * D1 (SPEC)") still count by id.
 */
export function sentAnswers(items: DesignQuestion[], messages: { id: string; role: string; content: string }[]): Record<string, SentAnswer> {
  const found: Record<string, SentAnswer> = {}
  for (const item of items) if (item.kind === 'decision' && (fromPerson.test(item.title) || fromPerson.test(item.rationale))) found[item.key] = { text: '', keep: true, messageId: '' }
  for (const message of messages) {
    if (message.role !== 'user' || !message.content.startsWith(answersHeader)) continue
    const lines = message.content.split('\n')
    const confirmed = lines.find(line => line.startsWith('Decisões confirmadas, mantenha como estão:')) ?? ''
    for (const item of items) {
      const text = lines.map(line => answerOn(line, item)).find(value => value !== undefined)
      if (text !== undefined) found[item.key] = { text, keep: text === keepText, messageId: message.id }
      else if (item.kind === 'decision' && new RegExp(`(^|[:,]\\s*)${item.id} \\(${stageNames[item.stage]}\\)`).test(confirmed)) found[item.key] = { text: keepText, keep: true, messageId: message.id }
    }
  }
  return found
}
