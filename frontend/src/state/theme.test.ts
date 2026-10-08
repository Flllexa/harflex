import { afterEach, expect, it, vi } from 'vitest'
import { readTheme } from './theme'

afterEach(() => { window.localStorage.removeItem('harflex.theme'); vi.unstubAllGlobals() })

it('uses the saved theme, or the system preference when none was chosen', () => {
  window.localStorage.setItem('harflex.theme', 'light')
  expect(readTheme()).toBe('light')
  window.localStorage.removeItem('harflex.theme')
  vi.stubGlobal('matchMedia', (query: string) => ({ matches: query === '(prefers-color-scheme: light)' }))
  expect(readTheme()).toBe('light')
  vi.stubGlobal('matchMedia', () => ({ matches: false }))
  expect(readTheme()).toBe('dark')
})
