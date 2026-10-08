import { useSyncExternalStore } from 'react'

export type Theme = 'light' | 'dark'

const storageKey = 'harflex.theme'
const listeners = new Set<() => void>()

/** The saved theme, or the system's when the person never chose one. */
export function readTheme(): Theme {
  try {
    const saved = window.localStorage.getItem(storageKey)
    if (saved === 'light' || saved === 'dark') return saved
  } catch { /* Storage may be unavailable. */ }
  return window.matchMedia?.('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
}

function currentTheme(): Theme {
  return document.documentElement.dataset.theme === 'light' ? 'light' : 'dark'
}

/** Applies the theme to the page; save remembers the choice on this computer. */
export function applyTheme(theme: Theme, save = true) {
  document.documentElement.dataset.theme = theme
  if (save) {
    try { window.localStorage.setItem(storageKey, theme) } catch { /* The choice still applies to this window. */ }
  }
  for (const listener of listeners) listener()
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => { listeners.delete(listener) }
}

export function useTheme(): [Theme, () => void] {
  const theme = useSyncExternalStore(subscribe, currentTheme, () => 'dark' as Theme)
  return [theme, () => applyTheme(theme === 'dark' ? 'light' : 'dark')]
}
