import { useEffect, useState } from 'react'
import type { AgentEvent, Backend } from '../../lib/backend'

/**
 * A session's journal as it grows: the stored pages first, then each event the backend pushes. keep picks the
 * events worth holding; streamed output can be left out where only the actions matter.
 */
export function useSessionJournal(backend: Backend, sessionId: string | undefined, keep: (event: AgentEvent) => boolean = () => true) {
  const [events, setEvents] = useState<AgentEvent[]>([])
  useEffect(() => {
    setEvents([])
    if (!sessionId) return
    let live = true
    const merge = (incoming: AgentEvent[]) => {
      const kept = incoming.filter(event => event.streamId === sessionId && keep(event))
      if (kept.length) setEvents(current => [...new Map([...current, ...kept].map(event => [event.id, event])).values()].sort((a, b) => a.sequence - b.sequence))
    }
    const unsubscribe = backend.onEvent(event => { if (live) merge([event]) })
    void (async () => {
      let after = 0
      for (let page = 0; page < 50 && live; page++) {
        const found = await backend.listEvents(sessionId, after, 1000).catch(() => [] as AgentEvent[])
        if (!live) return
        merge(found)
        if (found.length < 1000) return
        after = found[found.length - 1].sequence
      }
    })()
    return () => { live = false; unsubscribe() }
    // keep is a filter fixed by the caller; a new function each render must not reload the journal.
  }, [backend, sessionId])
  return events
}

/** The current time, ticking every second while active, so an elapsed time can run on screen. */
export function useNow(active: boolean) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    setNow(Date.now())
    if (!active) return
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [active])
  return now
}
