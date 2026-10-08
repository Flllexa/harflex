import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { createFakeBackend } from '../../test/fakeBackend'
import { SchedulesPage } from './SchedulesPage'

afterEach(cleanup)

it('conserva reset de trabalho, workflow obrigatório e consentimento CLI', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const save = vi.spyOn(backend, 'saveSchedule')
  backend.listWorkflows = async () => [{ id: 'workflow-fixture', workspaceId: 'workspace-1', name: 'Revisão', steps: [{ name: 'Passo', prompt: 'Revisar' }], revision: 1, createdAt: '2026-09-27T10:00:00Z', updatedAt: '2026-09-27T10:00:00Z' }]
  render(<SchedulesPage backend={backend} backends={[{ id: 'local', name: 'Local', kind: 'api', available: true }, { id: 'codex', name: 'Codex', kind: 'cli', available: true }]} workspaceId="workspace-1" onProjects={() => undefined} onSettings={() => undefined} onWorkflows={() => undefined} onOpenSession={async () => undefined} />)
  await screen.findByTestId('picker-schedule-target-kind')
  await user.type(screen.getByLabelText('Nome do agendamento'), 'Revisão diária')
  await user.type(screen.getByRole('textbox', { name: /^Prompt$/ }), 'Texto anterior')
  await user.click(within(screen.getByTestId('picker-schedule-target-kind')).getByRole('button'))
  await user.click(screen.getByRole('option', { name: 'Workflow' }))
  expect(screen.queryByRole('textbox', { name: /^Prompt$/ })).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Salvar agendamento' })).toBeDisabled()
  await user.type(within(screen.getByTestId('picker-schedule-workflow')).getByRole('combobox'), 'Revisão')
  await user.click(screen.getByRole('option', { name: 'Revisão' }))
  await user.click(within(screen.getByTestId('picker-schedule-backend')).getByRole('button'))
  await user.click(screen.getByRole('option', { name: 'Codex' }))
  expect(screen.getByRole('checkbox', { name: /Entendo que o CLI executa/ })).not.toBeChecked()
  expect(screen.getByRole('button', { name: 'Salvar agendamento' })).toBeDisabled()
  expect(save).not.toHaveBeenCalled()
})

it('leads with saved schedules and opens the editor on demand', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  const stamp = '2026-09-27T10:00:00Z'
  backend.listSchedules = async () => [{ id: 'schedule-1', workspaceId: 'workspace-1', name: 'Resumo matinal', targetKind: 'prompt', workflowId: '', backendId: 'local', prompt: 'Resuma', frequency: 'daily', timezone: 'America/Sao_Paulo', localDate: '', localTime: '09:00', missedPolicy: 'skip', enabled: true, allowCli: false, nextRunAt: '2026-09-28T12:00:00Z', revision: 1, createdAt: stamp, updatedAt: stamp }]
  render(<SchedulesPage backend={backend} backends={[{ id: 'local', name: 'Local', kind: 'api', available: true }]} workspaceId="workspace-1" onProjects={() => undefined} onSettings={() => undefined} onWorkflows={() => undefined} onOpenSession={async () => undefined} />)
  expect(await screen.findByText('Resumo matinal')).toBeInTheDocument()
  expect(screen.queryByLabelText('Nome do agendamento')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Novo agendamento' }))
  expect(await screen.findByLabelText('Nome do agendamento')).toHaveValue('')
  await user.click(screen.getByRole('button', { name: 'Fechar formulário' }))
  expect(screen.queryByLabelText('Nome do agendamento')).not.toBeInTheDocument()
})
