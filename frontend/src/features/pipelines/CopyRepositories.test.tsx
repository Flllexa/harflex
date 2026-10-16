import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { CopyRepositories } from './CopyRepositories'

const preview = { isGit: true, fileCount: 10, totalBytes: 10, excludedPaths: [], unsafePaths: [] }

it('lists the inner repositories in the copy and says how to bring in the ones left out', async () => {
  const refresh = vi.fn()
  render(<CopyRepositories preview={{ ...preview, repositories: [{ path: 'domains/settlement', branch: 'main' }], skippedRepositories: [{ path: 'domains/contracts', reason: 'uncommitted_changes' }] }} onRefresh={refresh} />)
  expect(screen.getByText('1 repositório interno entra na cópia')).toBeInTheDocument()
  expect(screen.getByText('domains/settlement')).toBeInTheDocument()
  expect(screen.getByRole('status')).toHaveTextContent('domains/contracts — tem alterações não commitadas')
  expect(screen.getByRole('status')).toHaveTextContent('Faça commit ou stash')
  await userEvent.click(screen.getByRole('button', { name: 'Atualizar prévia' }))
  expect(refresh).toHaveBeenCalledOnce()
})

it('shows nothing for a project with a single repository', () => {
  const { container } = render(<CopyRepositories preview={{ ...preview, repositories: [], skippedRepositories: [] }} onRefresh={() => undefined} />)
  expect(container).toBeEmptyDOMElement()
})
