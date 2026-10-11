import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { createFakeBackend } from '../../test/fakeBackend'
import { designPipeline, designReadback } from '../../test/pipelineDesignFixture'
import type { PipelineDesign } from '../../lib/backend'
import { DesignQuestions } from './DesignQuestions'
import { DesignMarkdown } from './PipelineDesignDocument'

afterEach(cleanup)

const spec = `# SPEC

## Decisões

| # | Questão | Decisão | Justificativa |
|---|---|---|---|
| D1 | Correlação | Usar o \`IdentdCtrlReqSolicte\` | Quem envia conhece o id |
| D2 | Sem correlação | Não publica e não falha | Evita ruído na DLQ |

## Perguntas em aberto

1. P1 — Destino?
   - Só produção
   - Produção e sandbox
`

function designWith(content: Partial<Record<'discovery' | 'spec' | 'plan', string>>): PipelineDesign {
  const base = designReadback(designPipeline(), true)
  for (const [stage, text] of Object.entries(content)) base.documents[stage as 'spec'] = { ...base.documents[stage as 'spec'], content: text!, contentDigest: `digest-${stage}-${text!.length}` }
  return base
}
const card = (id: string) => screen.getByText(id).closest('li')!

it('shows the decisions and questions of the documents as cards and sends the answers to the AI', async () => {
  const user = userEvent.setup(), onSend = vi.fn()
  const { backend } = createFakeBackend()
  render(<DesignQuestions backend={backend} design={designWith({ spec })} disabled={false} onSend={onSend} />)
  expect(screen.getByRole('heading', { name: 'Decisões e perguntas' })).toBeInTheDocument()
  expect(screen.getByText('0 de 3 respondidas. Confirme, mude ou responda; a IA ajusta os documentos.')).toBeInTheDocument()
  expect(within(card('D1')).getByText('IdentdCtrlReqSolicte').tagName).toBe('CODE')
  await user.click(within(card('D1')).getByRole('button', { name: 'Por quê' }))
  expect(within(card('D1')).getByText('Quem envia conhece o id')).toBeInTheDocument()

  const send = screen.getByRole('button', { name: 'Enviar respostas para a IA' })
  expect(send).toBeDisabled()
  await user.click(within(card('D1')).getByRole('button', { name: 'Manter' }))
  expect(within(card('D1')).getByText('Mantida')).toBeInTheDocument()
  await user.click(within(card('D2')).getByRole('button', { name: 'Mudar' }))
  await user.type(within(card('D2')).getByPlaceholderText('Como deve ser?'), 'Mandar para a DLQ com alarme')
  await user.click(within(card('P1')).getByRole('button', { name: 'Produção e sandbox' }))
  expect(within(card('P1')).getByRole('button', { name: 'Produção e sandbox' })).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByText('3 de 3 respondidas. Confirme, mude ou responda; a IA ajusta os documentos.')).toBeInTheDocument()
  expect(screen.getByText('3 respostas para enviar')).toBeInTheDocument()

  await user.click(send)
  expect(onSend).toHaveBeenCalledTimes(1)
  const [message, target] = onSend.mock.calls[0]
  expect(target).toBe('spec')
  expect(message).toContain('D2 (SPEC: Sem correlação): mudar a decisão para: Mandar para a DLQ com alarme')
  expect(message).toContain('P1 (SPEC: Destino?): Produção e sandbox')
  expect(message).toContain('D1 (SPEC: Correlação): manter como está')
  // Sent answers start over: the AI rewrites the document with them.
  expect(screen.getByText('0 de 3 respondidas. Confirme, mude ou responda; a IA ajusta os documentos.')).toBeInTheDocument()
})

it('switches between documents, keeps answers across remounts, and sends to every document when more than one was answered', async () => {
  const user = userEvent.setup(), onSend = vi.fn()
  const { backend } = createFakeBackend()
  const design = designWith({ spec, plan: '# Plan\n\n## Perguntas em aberto\n\n- Quem faz o deploy?' })
  const first = render(<DesignQuestions backend={backend} design={design} disabled={false} onSend={onSend} />)
  await user.click(within(card('D1')).getByRole('button', { name: 'Manter' }))
  first.unmount()
  render(<DesignQuestions backend={backend} design={design} disabled={false} onSend={onSend} />)
  expect(within(card('D1')).getByText('Mantida')).toBeInTheDocument()
  await user.click(screen.getByRole('tab', { name: /Plan/ }))
  await user.click(within(card('P1')).getByRole('button', { name: 'A IA decide' }))
  await user.click(screen.getByRole('button', { name: 'Enviar respostas para a IA' }))
  expect(onSend.mock.calls[0][1]).toBe('all')
  expect(onSend.mock.calls[0][0]).toContain('P1 (Plan: Quem faz o deploy?): decida você')
})

it('shows nothing when the documents record no decisions or questions, and blocks answers while the AI works', async () => {
  const { backend } = createFakeBackend()
  const empty = render(<DesignQuestions backend={backend} design={designWith({})} disabled={false} onSend={vi.fn()} />)
  expect(empty.container).toBeEmptyDOMElement()
  empty.unmount()
  render(<DesignQuestions backend={backend} design={designWith({ spec })} disabled onSend={vi.fn()} />)
  expect(within(card('D1')).getByRole('button', { name: 'Manter' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Enviar respostas para a IA' })).toBeDisabled()
})

it('draws Markdown tables in the documents instead of raw pipes', () => {
  render(<DesignMarkdown content={'Antes\n\n| # | Decisão |\n|---|---|\n| D1 | Usar **DynamoDB** |\n\nDepois'} />)
  const table = screen.getByRole('table')
  expect(within(table).getByRole('columnheader', { name: 'Decisão' })).toBeInTheDocument()
  expect(within(table).getByRole('cell', { name: 'Usar DynamoDB' })).toBeInTheDocument()
  expect(screen.queryByText(/\|---/)).not.toBeInTheDocument()
  expect(screen.getByText('Depois')).toBeInTheDocument()
})

it('counts the answers already sent, shows them on their cards, and says they were sent while the AI works', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const sentText = 'Respostas às decisões e perguntas dos documentos:\n\n- P1 (SPEC: Destino?): Produção e sandbox\n- D1 (SPEC: Correlação): manter como está\n\nAtualize os documentos.'
  const design = { ...designWith({ spec }), state: 'running', messages: [{ id: 'm1', role: 'user' as const, content: sentText, target: 'spec', attemptId: 'a1', createdAt: '2026-10-11T10:00:00Z' }] }
  const { rerender } = render(<DesignQuestions backend={backend} design={design} disabled onSend={vi.fn()} />)
  expect(screen.getByText('2 de 3 respondidas. Confirme, mude ou responda; a IA ajusta os documentos.')).toBeInTheDocument()
  expect(within(card('P1')).getByText('Enviada')).toBeInTheDocument()
  expect(within(card('P1')).getByText('Produção e sandbox', { selector: 'p *, p' })).toBeInTheDocument()
  expect(screen.getByText('Respostas enviadas; a IA está ajustando os documentos')).toBeInTheDocument()
  rerender(<DesignQuestions backend={backend} design={{ ...design, state: 'ready' }} disabled={false} onSend={vi.fn()} />)
  expect(within(card('P1')).getByText('Respondida')).toBeInTheDocument()
  expect(within(card('D1')).getByText('Mantida')).toBeInTheDocument()
  // The person can still answer again; the new answer is what goes next.
  await user.click(within(card('P1')).getByRole('button', { name: 'Só produção' }))
  expect(screen.getByText('1 resposta para enviar')).toBeInTheDocument()
})

it('keeps every decision still open in one click', async () => {
  const user = userEvent.setup(), onSend = vi.fn()
  const { backend } = createFakeBackend()
  render(<DesignQuestions backend={backend} design={designWith({ spec })} disabled={false} onSend={onSend} />)
  await user.click(screen.getByRole('button', { name: 'Manter as 2 decisões ainda abertas' }))
  expect(within(card('D1')).getByText('Mantida')).toBeInTheDocument()
  expect(within(card('D2')).getByText('Mantida')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /Manter as/ })).not.toBeInTheDocument()
})
