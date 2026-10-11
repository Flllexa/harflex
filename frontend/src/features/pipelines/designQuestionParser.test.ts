import { describe, expect, it } from 'vitest'
import { answered, answersRequest, designQuestions } from './designQuestionParser'

const spec = `# SPEC

## 1. Escopo

Texto com | barra que não é tabela.

## 2. Decisões

| # | Questão do Discovery | Decisão | Justificativa |
|---|---|---|---|
| D1 | Correlação da consulta | O campo é o \`IdentdCtrlReqSolicte\` **[H]**. | Quem envia a requisição conhece o id. |
| D2 | ARRC018 sem consulta correlacionável | **Não publica e não falha.** | Mandar para a DLQ geraria ruído. |

## 3. Perguntas em aberto

1. **P1 — Destino:** qual ambiente recebe primeiro?
   - Só platform-production
   - Production e sandbox
2. Backlog: (a) trazer tudo (b) só de julho em diante (c) nada
- Quem aprova a publicação da agenda?

## 4. Requisitos

- R1: não é pergunta
`

describe('decisions and open questions of a document', () => {
  const items = designQuestions('spec', spec)
  it('reads the decisions table with its id, question, decision and rationale', () => {
    const decisions = items.filter(item => item.kind === 'decision')
    expect(decisions).toHaveLength(2)
    expect(decisions[0]).toMatchObject({ id: 'D1', title: 'Correlação da consulta', decision: 'O campo é o `IdentdCtrlReqSolicte` [H].', rationale: 'Quem envia a requisição conhece o id.' })
    expect(decisions[1]).toMatchObject({ id: 'D2', decision: 'Não publica e não falha.' })
  })
  it('reads the open questions with options from sub-items or (a) (b) and numbers the ones without an id', () => {
    const questions = items.filter(item => item.kind === 'question')
    expect(questions.map(item => item.id)).toEqual(['P1', 'P2', 'P3'])
    expect(questions[0]).toMatchObject({ title: 'Destino: qual ambiente recebe primeiro?', options: ['Só platform-production', 'Production e sandbox'] })
    expect(questions[1]).toMatchObject({ title: 'Backlog', options: ['trazer tudo', 'só de julho em diante', 'nada'] })
    expect(questions[2]).toMatchObject({ title: 'Quem aprova a publicação da agenda?', options: [] })
  })
  it('leaves other sections alone', () => {
    expect(items.some(item => item.title.includes('R1'))).toBe(false)
    expect(designQuestions('plan', '# Plan\n\n1. Criar exportação.\n2. Verificar filtros.')).toEqual([])
  })
  it('understands English and Spanish headings and decisions written as a list', () => {
    const english = designQuestions('plan', '## Decisions\n\n- **D1 — Storage:** use DynamoDB\n\n## Open questions\n\n- Which region?')
    expect(english.map(item => [item.kind, item.id, item.title, item.decision])).toEqual([['decision', 'D1', 'Storage', 'use DynamoDB'], ['question', 'P1', 'Which region?', '']])
    expect(designQuestions('discovery', '## Preguntas abiertas\n\n1. ¿Quién valida?')[0]).toMatchObject({ kind: 'question', title: '¿Quién valida?' })
  })
})

describe('answers sent to the AI', () => {
  const items = designQuestions('spec', spec)
  const [d1, d2, p1, p2, p3] = items
  it('counts only complete answers', () => {
    expect(answered(d2, { choice: 'change', text: '  ' })).toBe(false)
    expect(answered(d2, { choice: 'change', text: 'Publicar como externo' })).toBe(true)
    expect(answered(p1, { choice: 'option' })).toBe(false)
  })
  it('says what changed and what was confirmed, with the document of each item', () => {
    const request = answersRequest(items, {
      [d1.key]: { choice: 'keep' },
      [d2.key]: { choice: 'change', text: 'Enviar para a DLQ com alarme' },
      [p1.key]: { choice: 'option', option: 'Production e sandbox' },
      [p2.key]: { choice: 'delegate' },
      [p3.key]: { choice: 'other', text: 'O time de operações' },
    })
    expect(request).toContain('- D2 (SPEC: ARRC018 sem consulta correlacionável): mudar a decisão para: Enviar para a DLQ com alarme')
    expect(request).toContain('- P1 (SPEC: Destino: qual ambiente recebe primeiro?): Production e sandbox')
    expect(request).toContain('- P2 (SPEC: Backlog): decida você')
    expect(request).toContain('- P3 (SPEC: Quem aprova a publicação da agenda?): O time de operações')
    expect(request).toContain('Decisões confirmadas, mantenha como estão: D1 (SPEC).')
  })
})
