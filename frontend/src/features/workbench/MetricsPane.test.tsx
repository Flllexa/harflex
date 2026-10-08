import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'
import { parse } from '../../lib/backend'
import { MetricsPane } from './MetricsPane'

it('derives execution metrics from journal evidence without inventing a cost', () => {
  const createdAt = '2026-09-25T10:00:00Z'
  const events = parse.events([
    { id: '1', streamId: 's', sequence: 1, type: 'run.started', data: {}, createdAt },
    { id: '2', streamId: 's', sequence: 2, type: 'usage.recorded', data: { inputTokens: 120, outputTokens: 30 }, createdAt },
    { id: '3', streamId: 's', sequence: 3, type: 'tool.completed', data: { toolCallId: 't', name: 'read', content: { text: 'ok' } }, createdAt },
    { id: '4', streamId: 's', sequence: 4, type: 'run.completed', data: { reason: '' }, createdAt: '2026-09-25T10:02:00Z' },
  ])
  render(<MetricsPane events={events} />)
  expect(screen.getByText('120')).toBeInTheDocument()
  expect(screen.getByText('30')).toBeInTheDocument()
  expect(screen.getByText('2 min')).toBeInTheDocument()
  expect(screen.getByText('Custo não informado')).toBeInTheDocument()
})
