import type { BackendOption } from './backend'

/**
 * Orders backends so that pages which default to the first available one agree with the user's habits:
 * usable backends first, then the configured default and the one used last, then API profiles ahead of
 * CLIs (they run any number of turns). Everything else keeps its original order.
 */
export function preferredFirst(backends: readonly BackendOption[], ...preferred: string[]): BackendOption[] {
  const rank = (backend: BackendOption) => { const index = preferred.indexOf(backend.id); return index === -1 ? preferred.length : index }
  return [...backends].sort((a, b) => Number(!a.available) - Number(!b.available) || rank(a) - rank(b) || Number(a.kind !== 'api') - Number(b.kind !== 'api'))
}
