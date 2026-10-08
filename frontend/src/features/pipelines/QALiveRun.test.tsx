import { cleanup, render, screen, within } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'
import type { AgentEvent } from '../../lib/backend'
import { createFakeBackend } from '../../test/fakeBackend'
import { commandKinds, QALiveRun } from './QALiveRun'

afterEach(cleanup)

it('reads what each command the QA runs checks, part by part', () => {
  expect(commandKinds('pwd; ls -la; ls -la todo; cat todo/package.json; command -v node npm chromium chromium-browser google-chrome || true')).toEqual(['inspect'])
  expect(commandKinds('cd todo && npm install --no-audit --no-fund && npm test && npm run lint')).toEqual(['install', 'unit', 'lint'])
  expect(commandKinds('cd todo && npm test 2>&1; echo EXIT_TEST=$?; npm run lint 2>&1; echo EXIT_LINT=$?')).toEqual(['unit', 'lint'])
  expect(commandKinds('cd todo && node --test tests/e2e.cjs')).toEqual(['e2e'])
  expect(commandKinds('cd todo && PLAYWRIGHT_BROWSERS_PATH="$PWD/.qa-browsers" npm run e2e')).toEqual(['e2e'])
  expect(commandKinds("cd todo && (python3 -m http.server 8765 --bind 127.0.0.1 > qa-server.log 2>&1 & pid=$!; curl -s http://127.0.0.1:8765/)")).toEqual(['run'])
  expect(commandKinds('cd todo && npm run build')).toEqual(['build'])
})

it('shows the QA run one command at a time, with the checks still to come', async () => {
  const { backend } = createFakeBackend()
  let sequence = 0
  const event = (type: string, data: unknown): AgentEvent => ({ id: `e${++sequence}`, streamId: 'qa-1', sequence, type, data, createdAt: '2026-10-08T10:00:00Z' })
  const call = (id: string, command: string) => ({ id, name: 'bash', arguments: { command } })
  const journal = [
    event('message.user', { content: 'Você é o QA desta entrega' }),
    event('message.assistant', { content: '', toolCalls: [call('t1', 'cd todo && npm test')] }),
    event('tool.called', { toolCallId: 't1', name: 'bash' }),
    event('tool.updated', { toolCallId: 't1', text: 'ok 1' }),
    event('tool.completed', { toolCallId: 't1', name: 'bash' }),
    event('message.assistant', { content: '', toolCalls: [call('t2', 'cd todo && node --test tests/e2e.cjs'), call('t3', 'cd todo && npm run lint')] }),
    event('tool.called', { toolCallId: 't2', name: 'bash' }),
  ]
  backend.listEvents = async () => journal
  render(<QALiveRun backend={backend} sessionId="qa-1" />)
  await screen.findByText('Rodando')
  const rows = screen.getAllByRole('listitem')
  expect(screen.getByText(/Agora:/)).toHaveTextContent('Agora: E2E')
  expect(within(rows[0]).getByText('Testes unitários', { selector: 'strong' })).toBeInTheDocument()
  expect(within(rows[0]).getByText('Concluiu')).toBeInTheDocument()
  expect(within(rows[1]).getByText('node --test tests/e2e.cjs', { exact: false })).toBeInTheDocument()
  expect(within(rows[1]).getByText('Rodando')).toBeInTheDocument()
  expect(within(rows[2]).getByText('Na fila')).toBeInTheDocument()
  // Build and the app were not reached yet.
  expect(rows.slice(3).map(row => within(row).getByText(/./, { selector: 'strong' }).textContent)).toEqual(['Build', 'App no ar'])
  expect(screen.getAllByText('Ainda não começou')).toHaveLength(2)
})

it('shows the next check at work while the model chooses the next command', async () => {
  const { backend } = createFakeBackend()
  let sequence = 0
  const event = (type: string, data: unknown): AgentEvent => ({ id: `e${++sequence}`, streamId: 'qa-2', sequence, type, data, createdAt: '2026-10-08T10:00:00Z' })
  backend.listEvents = async () => [
    event('message.user', { content: 'Você é o QA desta entrega' }),
    event('message.assistant', { content: '', toolCalls: [{ id: 't1', name: 'bash', arguments: { command: 'cd todo && npm install' } }] }),
    event('tool.called', { toolCallId: 't1', name: 'bash' }),
    event('tool.completed', { toolCallId: 't1', name: 'bash' }),
  ]
  render(<QALiveRun backend={backend} sessionId="qa-2" />)
  expect(await screen.findByText('Concluiu')).toBeInTheDocument()
  expect(screen.getByText(/Preparando:/)).toHaveTextContent('Preparando: Build')
  const rows = screen.getAllByRole('listitem')
  expect(within(rows[1]).getByText('Preparando…')).toBeInTheDocument()
  expect(screen.getAllByText('Ainda não começou')).toHaveLength(4)
})
