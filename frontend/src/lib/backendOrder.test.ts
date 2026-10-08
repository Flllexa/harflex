import { expect, it } from 'vitest'
import type { BackendOption } from './backend'
import { preferredFirst } from './backendOrder'

const item = (id: string, kind: BackendOption['kind'], available = true): BackendOption => ({ id, name: id, kind, available })

it('puts the configured default, then the last used, before everything else', () => {
  const list = [item('codex', 'cli'), item('alpha', 'api'), item('beta', 'api'), item('gamma', 'api')]
  expect(preferredFirst(list, 'beta', 'gamma').map(entry => entry.id)).toEqual(['beta', 'gamma', 'alpha', 'codex'])
  expect(preferredFirst(list, '', 'gamma').map(entry => entry.id)).toEqual(['gamma', 'alpha', 'beta', 'codex'])
})

it('prefers API profiles over CLIs when nothing is preferred and keeps unavailable ones last', () => {
  const list = [item('opencode', 'cli', false), item('codex', 'cli'), item('local', 'api'), item('off', 'api', false)]
  expect(preferredFirst(list).map(entry => entry.id)).toEqual(['local', 'codex', 'off', 'opencode'])
  expect(preferredFirst(list, 'opencode').map(entry => entry.id)).toEqual(['local', 'codex', 'opencode', 'off'])
})

it('does not mutate its input and keeps the original order among equals', () => {
  const list = [item('b', 'api'), item('a', 'api')]
  const ordered = preferredFirst(list)
  expect(ordered.map(entry => entry.id)).toEqual(['b', 'a'])
  expect(ordered).not.toBe(list)
})
