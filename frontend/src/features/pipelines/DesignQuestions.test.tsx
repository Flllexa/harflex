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
  expect(screen.getByText('2 para a IA ajustar')).toBeInTheDocument()

  await user.click(send)
  expect(onSend).toHaveBeenCalledTimes(1)
  const [message, target] = onSend.mock.calls[0]
  expect(target).toBe('spec')
  expect(message).toContain('D2 (SPEC: Sem correlação): mudar a decisão para: Mandar para a DLQ com alarme')
  expect(message).toContain('P1 (SPEC: Destino?): Produção e sandbox')
  expect(message).toContain('Decisões confirmadas, mantenha como estão: D1 (SPEC).')
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
