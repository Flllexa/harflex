import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it } from 'vitest'
import type { Skill } from '../../lib/backend'
import { createFakeBackend } from '../../test/fakeBackend'
import { SkillsPage } from './SkillsPage'

afterEach(cleanup)

const date = '2026-09-26T10:00:00Z'
const skill: Skill = { id: 'skill-1', workspaceId: 'workspace-1', name: 'Revisão', description: 'Cobertura', content: 'Revise testes', enabled: true, revision: 1, createdAt: date, updatedAt: date }

it('leads with saved skills and opens import and creation together on demand', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  backend.listSkills = async () => [skill]
  render(<SkillsPage backend={backend} workspaceId="workspace-1" onProjects={() => undefined} />)
  expect(await screen.findByText('Revise testes')).toBeInTheDocument()
  expect(screen.queryByLabelText('Nome da skill')).not.toBeInTheDocument()
  expect(screen.queryByLabelText('Caminho da skill')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Nova skill' }))
  expect(await screen.findByLabelText('Nome da skill')).toBeVisible()
  expect(screen.getByLabelText('Caminho da skill')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Fechar formulário' }))
  expect(screen.queryByLabelText('Nome da skill')).not.toBeInTheDocument()
})

it('opens the form with the skill when editing and closes it after saving', async () => {
  const user = userEvent.setup()
  const { backend } = createFakeBackend()
  backend.listSkills = async () => [skill]
  backend.saveSkill = async input => ({ ...skill, content: input.content, revision: 2 })
  render(<SkillsPage backend={backend} workspaceId="workspace-1" onProjects={() => undefined} />)
  await user.click(await screen.findByRole('button', { name: 'Editar Revisão' }))
  expect(await screen.findByLabelText('Nome da skill')).toHaveValue('Revisão')
  await user.clear(screen.getByLabelText('Instruções da skill'))
  await user.type(screen.getByLabelText('Instruções da skill'), 'Revise testes e riscos')
  await user.click(screen.getByRole('button', { name: 'Salvar skill' }))
  expect(await screen.findByText('Revise testes e riscos')).toBeInTheDocument()
  expect(screen.queryByLabelText('Nome da skill')).not.toBeInTheDocument()
})

it('shows both forms straight away when there are no skills', async () => {
  const { backend } = createFakeBackend()
  backend.listSkills = async () => []
  render(<SkillsPage backend={backend} workspaceId="workspace-1" onProjects={() => undefined} />)
  expect(await screen.findByText('Nenhuma skill salva')).toBeInTheDocument()
  expect(screen.getByLabelText('Nome da skill')).toBeVisible()
  expect(screen.getByLabelText('Caminho da skill')).toBeVisible()
  expect(screen.queryByRole('button', { name: 'Nova skill' })).not.toBeInTheDocument()
})
