import { useCallback, useEffect, useState } from 'react'
import type { Backend, ChatWatcher } from '../../lib/backend'

const refreshMs = 5000

/** A project's chat watchers, read again every few seconds, and a clock that ticks while one is watching. */
export function useChatWatchers(backend: Backend | undefined, workspaceId: string | undefined) {
  const [items, setItems] = useState<ChatWatcher[]>([])
  const [now, setNow] = useState(() => new Date())
  const refresh = useCallback(async () => {
    if (!backend || !workspaceId) { setItems([]); return }
    try { setItems(await backend.listChatWatchers(workspaceId)) } catch { /* the next read tries again */ }
  }, [backend, workspaceId])
  useEffect(() => {
    void refresh()
    if (!backend || !workspaceId) return
    const timer = setInterval(() => void refresh(), refreshMs)
    return () => clearInterval(timer)
  }, [refresh, backend, workspaceId])
  const watching = items.some(item => item.status === 'watching')
  useEffect(() => {
    if (!watching) return
    const timer = setInterval(() => setNow(new Date()), 1000)
    return () => clearInterval(timer)
  }, [watching])
  const stop = useCallback(async (watcherId: string) => {
    if (!backend) return
    try { await backend.stopChatWatcher(watcherId) } finally { await refresh() }
  }, [backend, refresh])
  return { items, now, stop }
}
