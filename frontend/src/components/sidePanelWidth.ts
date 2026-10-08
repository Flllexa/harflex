import { useCallback, useEffect, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react'

const storageKey = 'harflex.sidePanelWidth'
export const defaultSidePanelWidth = 360
export const minSidePanelWidth = 280

// The work area keeps room for the sidebar and a readable conversation.
export function maxSidePanelWidth() {
  const viewport = typeof window === 'undefined' ? 1440 : window.innerWidth
  return Math.max(minSidePanelWidth, Math.min(Math.round(viewport * 0.7), viewport - 620))
}

export function clampSidePanelWidth(value: number) {
  return Math.round(Math.min(maxSidePanelWidth(), Math.max(minSidePanelWidth, value)))
}

function readWidth() {
  try {
    const value = Number(window.localStorage.getItem(storageKey))
    return Number.isFinite(value) && value > 0 ? Math.round(value) : defaultSidePanelWidth
  } catch { return defaultSidePanelWidth }
}

function saveWidth(value: number) {
  try { window.localStorage.setItem(storageKey, String(value)) } catch { /* The width is only a convenience. */ }
}

/** The side panel's width, dragged from its left edge or set with the arrow keys, remembered on this computer. */
export function useSidePanelWidth() {
  // The chosen width survives a smaller window: it is only limited while the window is small.
  const [preferred, setWidth] = useState(readWidth)
  const [, setViewport] = useState(() => window.innerWidth)
  const [dragging, setDragging] = useState(false)
  const width = clampSidePanelWidth(preferred)
  const latest = useRef(width)
  latest.current = width

  useEffect(() => {
    const fit = () => setViewport(window.innerWidth)
    window.addEventListener('resize', fit)
    return () => window.removeEventListener('resize', fit)
  }, [])

  const set = useCallback((value: number) => { const next = clampSidePanelWidth(value); setWidth(next); saveWidth(next) }, [])

  const onPointerDown = useCallback((event: PointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) return
    event.preventDefault()
    const handle = event.currentTarget
    handle.setPointerCapture?.(event.pointerId)
    const startX = event.clientX
    const startWidth = latest.current
    setDragging(true)
    const move = (moved: globalThis.PointerEvent) => setWidth(clampSidePanelWidth(startWidth + startX - moved.clientX))
    const stop = () => {
      setDragging(false)
      saveWidth(latest.current)
      handle.removeEventListener('pointermove', move)
      handle.removeEventListener('pointerup', stop)
      handle.removeEventListener('pointercancel', stop)
    }
    handle.addEventListener('pointermove', move)
    handle.addEventListener('pointerup', stop)
    handle.addEventListener('pointercancel', stop)
  }, [])

  const onKeyDown = useCallback((event: KeyboardEvent<HTMLDivElement>) => {
    const step = event.shiftKey ? 80 : 24
    const next = event.key === 'ArrowLeft' ? latest.current + step : event.key === 'ArrowRight' ? latest.current - step
      : event.key === 'Home' ? maxSidePanelWidth() : event.key === 'End' ? minSidePanelWidth : undefined
    if (next === undefined) return
    event.preventDefault()
    set(next)
  }, [set])

  return { width, dragging, reset: () => set(defaultSidePanelWidth), onPointerDown, onKeyDown }
}
