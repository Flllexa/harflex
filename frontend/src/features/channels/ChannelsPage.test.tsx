import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { createFakeBackend } from '../../test/fakeBackend'
import type { ChannelMessage, LocalChannel } from '../../lib/backend'
import { ChannelsPage } from './ChannelsPage'

afterEach(cleanup)

const date = '2026-09-26T10:00:00Z'
const channel = { id: 'channel-1', workspaceId: 'workspace-1', name: 'Equipe', folder: 'channels/equipe', status: 'ready' as const, lastError: '', createdAt: date, updatedAt: date }

it('configures a local folder, imports on command, sends on command and shows durable history', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  let storedChannels = [] as typeof channel[]
  const saveLocalChannel = vi.fn(async () => { storedChannels = [channel]; return channel })
  const importChannelInbox = vi.fn(async () => ({ channel, imported: 1, failed: 0 }))
  let history: ChannelMessage[] = [{ id: 'message-in', channelId: channel.id, direction: 'incoming', fileName: 'pedido.md', content: 'Preciso de uma resposta.', status: 'received', errorCode: '', createdAt: date, updatedAt: date }]
  const sendChannelMessage = vi.fn(async (input: { channelId: string; requestId: string; content: string }) => {
    const sent = { id: 'message-out', channelId: input.channelId, direction: 'outgoing' as const, fileName: `harflex-${input.requestId}.md`, content: input.content, status: 'sent' as const, errorCode: '', createdAt: date, updatedAt: date }
    history = [sent, ...history]
    return sent
  })
  const listChannelMessages = vi.fn(async () => history)
  Object.assign(backend, { listLocalChannels: async () => storedChannels, saveLocalChannel, importChannelInbox, sendChannelMessage, listChannelMessages })
  render(<ChannelsPage backend={backend} workspaceId="workspace-1" onProjects={() => undefined} />)
  await user.type(screen.getByLabelText('Nome do canal'), 'Equipe')
  await user.type(screen.getByLabelText('Pasta no projeto'), 'channels/equipe')
  await user.click(screen.getByRole('button', { name: 'Configurar canal' }))
  expect(saveLocalChannel).toHaveBeenCalledWith({ workspaceId: 'workspace-1', name: 'Equipe', folder: 'channels/equipe' })
  expect(await screen.findByText('Preciso de uma resposta.')).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Importar inbox' }))
  expect(importChannelInbox).toHaveBeenCalledWith('channel-1')
  await user.type(screen.getByLabelText('Resposta'), 'Resposta aprovada.')
  await user.click(screen.getByRole('button', { name: 'Enviar resposta' }))
  expect(sendChannelMessage).toHaveBeenCalledWith(expect.objectContaining({ channelId: 'channel-1', content: 'Resposta aprovada.', requestId: expect.stringMatching(/^[a-f0-9]{32}$/) }))
  expect(within(await screen.findByRole('listitem', { name: /Saída harflex-/ })).getByText('Resposta aprovada.')).toBeInTheDocument()
  expect(screen.getByText('Enviado')).toBeInTheDocument()
  await waitFor(() => expect(screen.getByLabelText('Resposta')).toHaveFocus())
})

it('reloads failed delivery and repeats only after explicit action', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const failed = { id: 'message-failed', channelId: channel.id, direction: 'outgoing' as const, fileName: 'harflex-fedcba9876543210fedcba9876543210.md', content: 'Resposta', status: 'failed' as const, errorCode: 'outbox_conflict', createdAt: date, updatedAt: date }
  let history: ChannelMessage[] = [failed]
  let currentChannel: LocalChannel = { ...channel, status: 'error', lastError: 'outbox_conflict' }
  const retryChannelMessage = vi.fn(async () => {
    const sent = { ...failed, status: 'sent' as const, errorCode: '' }
    history = [sent]
    currentChannel = { ...channel }
    return sent
  })
  Object.assign(backend, { listLocalChannels: async () => [currentChannel], listChannelMessages: async () => history, retryChannelMessage })
  render(<ChannelsPage backend={backend} workspaceId="workspace-1" onProjects={() => undefined} />)
  expect(await screen.findByText('Falha no envio')).toBeInTheDocument()
  expect(retryChannelMessage).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Repetir envio' }))
  expect(retryChannelMessage).toHaveBeenCalledWith({ channelId: channel.id, requestId: 'fedcba9876543210fedcba9876543210' })
  expect(await screen.findByText('Enviado')).toBeInTheDocument()
  await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
})

it('keeps one request id with the same draft across a failed call and channel switch', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const other: LocalChannel = { ...channel, id: 'channel-2', name: 'Outro', folder: 'channels/outro' }
  let history: ChannelMessage[] = []
  const sendChannelMessage = vi.fn().mockRejectedValueOnce(new Error('transport lost')).mockImplementation(async (input: { channelId: string; requestId: string; content: string }) => {
    const sent: ChannelMessage = { id: 'outgoing-1', channelId: input.channelId, direction: 'outgoing', fileName: `harflex-${input.requestId}.md`, content: input.content, status: 'sent', errorCode: '', createdAt: date, updatedAt: date }
    history = [sent]
    return sent
  })
  Object.assign(backend, { listLocalChannels: async () => [channel, other], listChannelMessages: async (id: string) => history.filter(item => item.channelId === id), sendChannelMessage })
  const unsupported = vi.spyOn(crypto, 'randomUUID').mockImplementation(() => { throw new Error('randomUUID unavailable') })
  try {
    render(<ChannelsPage backend={backend} workspaceId="workspace-1" onProjects={() => undefined} />)
    await screen.findByRole('button', { name: /Equipe/ })
    await user.type(screen.getByLabelText('Resposta'), 'Minha resposta.')
    await user.click(screen.getByRole('button', { name: 'Enviar resposta' }))
    await waitFor(() => expect(sendChannelMessage).toHaveBeenCalledTimes(1))
    const firstID = sendChannelMessage.mock.calls[0][0].requestId
    await user.click(screen.getByRole('button', { name: /Outro/ }))
    expect(screen.getByLabelText('Resposta')).toHaveValue('')
    await user.type(screen.getByLabelText('Resposta'), 'Outro rascunho')
    await user.click(screen.getByRole('button', { name: /Equipe/ }))
    expect(screen.getByLabelText('Resposta')).toHaveValue('Minha resposta.')
    await user.click(screen.getByRole('button', { name: 'Enviar resposta' }))
    await waitFor(() => expect(sendChannelMessage).toHaveBeenCalledTimes(2))
    expect(sendChannelMessage.mock.calls[1][0].requestId).toBe(firstID)
    expect(firstID).toMatch(/^[a-f0-9]{32}$/)
    expect(await screen.findByText('Minha resposta.')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByLabelText('Resposta')).toHaveValue(''))
    await user.click(screen.getByRole('button', { name: /Outro/ }))
    expect(screen.getByLabelText('Resposta')).toHaveValue('Outro rascunho')
  } finally { unsupported.mockRestore() }
})

it('uses durable delivery readback when the Wails response is lost', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  let history: ChannelMessage[] = []
  const sendChannelMessage = vi.fn(async (input: { channelId: string; requestId: string; content: string }) => {
    history = [{ id: 'outgoing-1', channelId: input.channelId, direction: 'outgoing', fileName: `harflex-${input.requestId}.md`, content: input.content, status: 'sent', errorCode: '', createdAt: date, updatedAt: date }]
    throw new Error('Wails response lost')
  })
  Object.assign(backend, { listLocalChannels: async () => [channel], listChannelMessages: async () => history, sendChannelMessage })
  render(<ChannelsPage backend={backend} workspaceId="workspace-1" onProjects={() => undefined} />)
  await screen.findByRole('button', { name: /Equipe/ })
  await user.type(screen.getByLabelText('Resposta'), 'Entrega confirmada.')
  await user.click(screen.getByRole('button', { name: 'Enviar resposta' }))
  expect(await screen.findByText('Entrega confirmada.')).toBeInTheDocument()
  await waitFor(() => expect(screen.getByLabelText('Resposta')).toHaveValue(''))
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  expect(sendChannelMessage).toHaveBeenCalledTimes(1)
})
