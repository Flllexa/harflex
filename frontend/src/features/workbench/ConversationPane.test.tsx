import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { ConversationPane } from './ConversationPane'
import { continuationPrompt } from './continuation'
import type { ConversationItem } from '../../state/session'

function props() { return { state: { messages: [], pendingApprovals: [], activeRun: 'idle' as const, calling: false, draft: '', connectionState: 'ready' as const, readOnly: false }, loadingHistory: false, viewMode: 'casual' as const, onDraft: vi.fn(), onPrompt: vi.fn(), onResolve: vi.fn(), onCancel: vi.fn(), onRetry: vi.fn(), onNewWork: vi.fn() } }
it('keeps an empty Casual conversation focused on the composer', () => {
  render(<ConversationPane {...props()} />)
  expect(screen.getByLabelText('Mensagem')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Novo chat' })).not.toBeInTheDocument()
  expect(screen.queryByText('O trabalho começa com uma conversa.')).not.toBeInTheDocument()
  expect(screen.queryByText('Pronto')).not.toBeInTheDocument()
})
it('preserves draft migration without repeating the migration notice in Casual', async () => {
  const value = props(); value.state.draft = 'Pedido ainda não enviado.'
  render(<ConversationPane {...value} />)
  expect(screen.getByLabelText('Mensagem')).toHaveValue('Pedido ainda não enviado.')
  expect(screen.queryByText(/Se iniciar outro trabalho/)).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Levar rascunho para novo chat' }))
  expect(value.onNewWork).toHaveBeenCalledWith('Pedido ainda não enviado.')
  expect(value.onPrompt).not.toHaveBeenCalled()
})

it.each([
  ['tool_failed', 'Falhou: uma ferramenta falhou (veja o cartão dela acima)'],
  ['provider_failed', 'Falhou: o provedor de IA falhou'],
  ['policy_denied', 'Falhou: a política de segurança negou uma ferramenta'],
  ['tool_limit_exceeded', 'Falhou: limite de chamadas de ferramenta atingido'],
  ['algo_que_nao_existe', 'Falhou: erro durante a execução'],
])('says why a run ended as %s instead of a bare failure', (reason, text) => {
  const value = props()
  render(<ConversationPane {...value} state={{ ...value.state, outcome: { status: 'failed', reason } }} />)
  expect(screen.getByRole('status')).toHaveTextContent(text)
})

// A conversation that cannot be resumed does not lock the chat: the message box stays and carries the person on.
it('keeps the message box of a closed conversation and sends what is typed to onContinue, never to the closed run', async () => {
  const user = userEvent.setup()
  const value = props(); value.state.readOnly = true; value.state.draft = 'Agora ajuste o rodapé'
  const onContinue = vi.fn()
  render(<ConversationPane {...value} onContinue={onContinue} />)
  expect(screen.getByLabelText('Mensagem')).toBeEnabled()
  expect(screen.getByText(/Somente leitura: esta execução foi encerrada/)).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Enviar' }))
  expect(onContinue).toHaveBeenCalledWith('Agora ajuste o rodapé')
  expect(value.onPrompt).not.toHaveBeenCalled()
})
it('keeps the box shut while the new chat is being opened, so words typed meanwhile are not lost with the old one', () => {
  const value = props(); value.state.readOnly = true; value.state.draft = 'Siga daqui'; value.state.calling = true
  render(<ConversationPane {...value} onContinue={vi.fn()} />)
  expect(screen.getByLabelText('Mensagem')).toBeDisabled()
  cleanup()
  const open = props(); open.state.readOnly = true; open.state.draft = 'Siga daqui'
  render(<ConversationPane {...open} onContinue={vi.fn()} />)
  expect(screen.getByLabelText('Mensagem')).toBeEnabled()
})
it('sends on Enter from a closed conversation, and not an empty message', async () => {
  const user = userEvent.setup()
  const onContinue = vi.fn()
  const value = props(); value.state.readOnly = true
  const view = render(<ConversationPane {...value} onContinue={onContinue} />)
  expect(screen.getByRole('button', { name: 'Enviar' })).toBeDisabled()
  await user.type(screen.getByLabelText('Mensagem'), '{Enter}')
  expect(onContinue).not.toHaveBeenCalled()
  view.rerender(<ConversationPane {...value} state={{ ...value.state, draft: 'Siga daqui' }} onContinue={onContinue} />)
  await user.type(screen.getByLabelText('Mensagem'), '{Enter}')
  expect(onContinue).toHaveBeenCalledWith('Siga daqui')
})
it('does not send a second continuation while the first is being opened', async () => {
  const onContinue = vi.fn()
  const value = props(); value.state.readOnly = true; value.state.draft = 'Siga daqui'; value.state.calling = true
  render(<ConversationPane {...value} onContinue={onContinue} />)
  expect(screen.getByRole('button', { name: 'Enviar' })).toBeDisabled()
  await userEvent.type(screen.getByLabelText('Mensagem'), '{Enter}')
  expect(onContinue).not.toHaveBeenCalled()
})
it('stays read-only where nothing can carry the conversation on', () => {
  const value = props(); value.state.readOnly = true; value.state.draft = 'Siga daqui'
  render(<ConversationPane {...value} />)
  expect(screen.getByLabelText('Mensagem')).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Enviar' })).toBeDisabled()
})
it('shows what the person wrote in a continued conversation and folds the context that came with it', () => {
  const text = continuationPrompt('Ajuste o título do PR', { session: { purpose: 'chat', title: '' }, messages: [{ kind: 'user', id: 'u1', text: 'Crie o TODO' }, { kind: 'assistant', id: 'a1', text: 'Pronto, criei o TODO.', streaming: false }] })
  const value = props()
  render(<ConversationPane {...value} state={{ ...value.state, messages: [{ kind: 'user', id: 'u2', text }] }} />)
  expect(screen.getByText('Ajuste o título do PR')).toBeVisible()
  const folded = screen.getByText('Contexto levado da conversa anterior').closest('details')!
  expect(folded).not.toHaveAttribute('open')
  expect(folded).toHaveTextContent('Crie o TODO')
  expect(folded).toHaveTextContent('Pronto, criei o TODO.')
})
it('carries only the words, not the folded context, when a continued conversation is closed too', async () => {
  const text = continuationPrompt('Ajuste o título do PR', { session: { purpose: 'chat', title: '' }, messages: [{ kind: 'user', id: 'u1', text: 'Crie o TODO' }, { kind: 'assistant', id: 'a1', text: 'Pronto.', streaming: false }] })
  const value = props(); value.state.readOnly = true
  render(<ConversationPane {...value} viewMode="professional" onContinue={vi.fn()} state={{ ...value.state, messages: [{ kind: 'user', id: 'u2', text }] }} />)
  await userEvent.click(screen.getByRole('button', { name: 'Continuar em novo chat' }))
  expect(value.onNewWork).toHaveBeenCalledWith('Ajuste o título do PR')
})
it('does not hold a closed conversation hostage to an approval nobody can give any more', async () => {
  const user = userEvent.setup()
  const onContinue = vi.fn()
  const value = props()
  const approval = { approvalId: 'a1', toolCallId: 't1', name: 'write', risk: 'write', arguments: {} }
  render(<ConversationPane {...value} viewMode="professional" onContinue={onContinue}
    state={{ ...value.state, readOnly: true, activeRun: 'awaiting_approval', pendingApprovals: [approval], draft: 'Siga daqui' }} />)
  expect(screen.getByRole('button', { name: 'Continuar em novo chat' })).toBeEnabled()
  expect(screen.getByRole('button', { name: 'Aprovar' })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Enviar' }))
  expect(onContinue).toHaveBeenCalledWith('Siga daqui')
})
it('still holds a live conversation while it waits for its approval', () => {
  const value = props()
  const approval = { approvalId: 'a1', toolCallId: 't1', name: 'write', risk: 'write', arguments: {} }
  render(<ConversationPane {...value} viewMode="professional" onContinue={vi.fn()}
    state={{ ...value.state, activeRun: 'awaiting_approval', pendingApprovals: [approval], draft: 'Siga daqui' }} />)
  expect(screen.getByRole('button', { name: 'Enviar' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Levar rascunho para novo trabalho' })).toBeDisabled()
})
it('shows an ordinary message as it was written', () => {
  const value = props()
  render(<ConversationPane {...value} state={{ ...value.state, messages: [{ kind: 'user', id: 'u1', text: 'Crie o TODO' }] }} />)
  expect(screen.getByText('Crie o TODO')).toBeVisible()
  expect(screen.queryByText('Contexto levado da conversa anterior')).not.toBeInTheDocument()
})

const choiceBlock = '```harflex-choice\n{"kind":"pipeline"}\n```'
const askedState = (text: string, extra: ConversationItem[] = []) => {
  const value = props()
  const messages: ConversationItem[] = [{ kind: 'user', id: 'u1', text: 'Integre o arquivo da Nuclea' }, { kind: 'assistant', id: 'a1', text, streaming: false }, ...extra]
  return { ...value, state: { ...value.state, messages } }
}

it('offers the three ways to carry a large request in Casual, without showing the block', async () => {
  const user = userEvent.setup(), onPipelineChoice = vi.fn()
  const value = askedState(`Entendi: o arquivo precisa chegar na V2.\n\n${choiceBlock}`)
  render(<ConversationPane {...value} onPipelineChoice={onPipelineChoice} />)
  expect(screen.getByText('Entendi: o arquivo precisa chegar na V2.')).toBeInTheDocument()
  expect(screen.queryByText(/harflex-choice/)).not.toBeInTheDocument()
  const group = screen.getByRole('group', { name: 'Como você quer tocar isso?' })
  await user.click(within(group).getByRole('button', { name: /fazer tudo por aqui, no Casual/ }))
  await user.click(within(group).getByRole('button', { name: /ir para o modo Profissional/ }))
  await user.click(within(group).getByRole('button', { name: /Só resolver aqui na conversa/ }))
  expect(onPipelineChoice.mock.calls.map(call => call[0])).toEqual(['casual', 'professional', 'chat'])
})

it('offers two ways in Professional mode, where there is no other mode to go to', () => {
  const value = askedState(`Entendi.\n\n${choiceBlock}`)
  render(<ConversationPane {...value} viewMode="professional" onPipelineChoice={vi.fn()} />)
  const group = screen.getByRole('group', { name: 'Como você quer tocar isso?' })
  expect(within(group).getAllByRole('button').map(button => button.textContent)).toEqual(['Abrir a pipeline', 'Só resolver aqui na conversa, sem pipeline'])
})

it('closes the choice once the person has answered, while a reply is being written, or when the run is live', () => {
  const answered = askedState(`Entendi.\n\n${choiceBlock}`, [{ kind: 'user', id: 'u2', text: 'Resolva por aqui' }])
  const { unmount } = render(<ConversationPane {...answered} onPipelineChoice={vi.fn()} />)
  expect(screen.queryByRole('group', { name: 'Como você quer tocar isso?' })).not.toBeInTheDocument()
  expect(screen.queryByText(/harflex-choice/)).not.toBeInTheDocument()
  unmount()
  const writing = askedState(`Entendi.\n\n${choiceBlock}`)
  writing.state.messages[1] = { kind: 'assistant', id: 'a1', text: `Entendi.\n\n${choiceBlock}`, streaming: true }
  const second = render(<ConversationPane {...writing} onPipelineChoice={vi.fn()} />)
  expect(screen.queryByRole('group', { name: 'Como você quer tocar isso?' })).not.toBeInTheDocument()
  second.unmount()
  const busy = askedState(`Entendi.\n\n${choiceBlock}`)
  render(<ConversationPane {...busy} state={{ ...busy.state, activeRun: 'running' }} onPipelineChoice={vi.fn()} />)
  for (const button of within(screen.getByRole('group', { name: 'Como você quer tocar isso?' })).getAllByRole('button')) expect(button).toBeDisabled()
})
