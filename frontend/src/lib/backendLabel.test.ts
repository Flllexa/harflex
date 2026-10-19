import { describe, expect, it } from 'vitest'
import { backendLabel } from './backendLabel'

describe('backendLabel', () => {
  it('says why a CLI is unavailable', () => {
    expect(backendLabel({ id: 'claude', name: 'Claude Code', kind: 'cli', available: false, unavailableReason: 'not_found' })).toBe('Claude Code · não encontrado')
    expect(backendLabel({ id: 'codex', name: 'codex', kind: 'cli', available: false, unavailableReason: 'timeout' })).toBe('codex · demorou para responder')
    expect(backendLabel({ id: 'codex', name: 'codex', kind: 'cli', available: false, unavailableReason: 'failed', unavailableDetail: 'requires git-bash' })).toBe('codex · não iniciou: requires git-bash')
  })

  it('keeps the plain labels', () => {
    expect(backendLabel({ id: 'claude', name: 'Claude Code', kind: 'cli', available: true })).toBe('Claude Code')
    expect(backendLabel({ id: 'x', name: 'API', kind: 'api', available: false })).toBe('API · indisponível')
  })
})
