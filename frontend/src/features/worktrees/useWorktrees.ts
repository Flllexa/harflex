import { useCallback, useEffect, useRef, useState } from 'react'
import { errorMessage, type Backend, type WorktreeList } from '../../lib/backend'

export type WorktreesState = 'idle' | 'loading' | 'ready' | 'error'

/** Reads the project's worktrees and keeps only the answer to the latest question. */
export function useWorktrees(backend: Backend, workspaceId: string | undefined, refreshKey = 0) {
  const [list, setList] = useState<WorktreeList>()
  const [state, setState] = useState<WorktreesState>('idle')
  const [error, setError] = useState<string>()
  const ticket = useRef(0)

  const refresh = useCallback(async () => {
    const mine = ++ticket.current
    if (!workspaceId) { setList(undefined); setState('idle'); setError(undefined); return }
    setState('loading')
    setError(undefined)
    try {
      const next = await backend.listWorktrees(workspaceId)
      if (mine !== ticket.current) return
      setList(next)
      setState('ready')
    } catch (failure) {
      if (mine !== ticket.current) return
      setError(errorMessage(failure))
      setState('error')
    }
  }, [backend, workspaceId])

  useEffect(() => {
    setList(undefined)
    void refresh()
    return () => { ticket.current++ }
  }, [refresh, refreshKey])

  /** An action already returned the fresh list; adopt it without another read. */
  const replace = useCallback((next: WorktreeList) => { ticket.current++; setList(next); setState('ready'); setError(undefined) }, [])
  return { list, state, error, refresh, replace }
}
