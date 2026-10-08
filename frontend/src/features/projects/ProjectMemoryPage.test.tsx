import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ProjectMemory } from '../../lib/backend'
import { createFakeBackend } from '../../test/fakeBackend'
import { itemName, memorySections, ProjectMemoryPage } from './ProjectMemoryPage'

afterEach(cleanup)

const content = `## Visão geral

- Workspace de coordenação dos domínios serverless.

## Tecnologias

- Backend: Go 1.25 com AWS SAM.
- Persistência: DynamoDB.

## Domínios

- \`billing-reporting\`: faturamento por cliente.
- \`company\`: cadastro de empresas.
- \`settlement\`: conciliação.

## Repositórios

- \`domains/company\` — acme-serverless-company.

## Serviços

- API pública V2.

## Convenções e documentos-chave

- AGENTS.md define as regras.`

const memory = (over: Partial<ProjectMemory> = {}): ProjectMemory => ({ workspaceId: 'workspace-1', status: 'ready', content, sources: ['README.md', 'AGENTS.md'], backendId: 'claude', modelId: 'sonnet',
  edited: false, updatedAt: '2026-10-02T12:00:00Z', ...over })

describe('project memory page', () => {
  it('splits the memory into sections and names list items', () => {
    const sections = memorySections(content)
    expect(sections.map(section => section.title)).toEqual(['Visão geral', 'Tecnologias', 'Domínios', 'Repositórios', 'Serviços', 'Convenções e documentos-chave'])
    expect(sections[2].items).toHaveLength(3)
    expect(itemName('`billing-reporting`: faturamento')).toBe('billing-reporting')
    expect(itemName('**Backend**: Go 1.25')).toBe('Backend')
    expect(itemName('API pública V2 — REST')).toBe('API pública V2')
    expect(itemName('`.` — o workspace')).toBe('raiz')
  })

  it('summarizes what the AI read: counts, overview, domain and repository chips, every section and the files', async () => {
    const user = userEvent.setup()
    const { backend } = createFakeBackend()
    backend.getProjectMemory = vi.fn(async () => memory())
    render(<ProjectMemoryPage backend={backend} workspaceId="workspace-1" projectName="workspace" onProjects={() => undefined} />)
    const tiles = await screen.findByRole('list', { name: 'Resumo' })
    expect(within(tiles).getByRole('link', { name: /2\s*Tecnologias/ })).toHaveAttribute('href', '#memory-tecnologias')
    expect(within(tiles).getByText('3')).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Visão geral' })).toHaveTextContent('Workspace de coordenação dos domínios serverless.')
    expect(screen.getByText('billing-reporting', { selector: '.memory-chips li' })).toBeInTheDocument()
    expect(screen.getByText('domains/company', { selector: '.memory-chips li' })).toBeInTheDocument()
    expect(screen.getByText('Convenções e documentos-chave')).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('Memória pronta · 2 arquivos lidos · claude · sonnet')
    await user.click(screen.getByRole('button', { name: 'Editar' }))
    expect(await screen.findByLabelText('Texto da memória')).toHaveValue(content)
  })

  it('says when the project is being read or was never read, and asks for a project when none is open', async () => {
    const { backend } = createFakeBackend()
    backend.getProjectMemory = vi.fn(async () => memory({ status: 'reading', content: '', sources: [] }))
    const { unmount } = render(<ProjectMemoryPage backend={backend} workspaceId="workspace-1" onProjects={() => undefined} />)
    expect(await screen.findByText('A IA está lendo o projeto…')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Ler/ })).not.toBeInTheDocument()
    unmount()
    render(<ProjectMemoryPage backend={backend} onProjects={() => undefined} />)
    expect(screen.getByText('Nenhum projeto aberto')).toBeInTheDocument()
  })
})
