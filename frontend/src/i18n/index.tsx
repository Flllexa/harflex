import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { en, es } from './dictionaries'

export type Locale = 'en' | 'pt-BR' | 'es'

export const locales: readonly { value: Locale; label: string }[] = [
  { value: 'en', label: 'English' },
  { value: 'pt-BR', label: 'Português (Brasil)' },
  { value: 'es', label: 'Español' },
]

const storageKey = 'harflex:locale'
const tags: Record<Locale, string> = { en: 'en-US', 'pt-BR': 'pt-BR', es: 'es-ES' }
const dictionaries: Partial<Record<Locale, Record<string, string>>> = { en, es }
const listeners = new Set<() => void>()

function isLocale(value: string | null): value is Locale {
  return value === 'en' || value === 'pt-BR' || value === 'es'
}

function readStored(): Locale {
  try {
    const value = localStorage.getItem(storageKey)
    if (isLocale(value)) return value
  } catch { /* storage can be blocked: the default applies */ }
  return 'en'
}

// The Portuguese text is the key: pt-BR shows it as written, and a text with no translation yet shows Portuguese.
let current: Locale = readStored()

export function currentLocale(): Locale {
  return current
}

/** The BCP 47 tag for dates and numbers in the language on screen. */
export function localeTag(): string {
  return tags[current]
}

export function setLocale(next: Locale) {
  current = next
  try { localStorage.setItem(storageKey, next) } catch { /* the choice lasts for this session only */ }
  listeners.forEach(listener => listener())
}

/** Translates a text from the source (Portuguese). `{name}` marks are replaced by `values`. */
export function t(source: string, values?: Record<string, string | number>): string {
  const translated = current === 'pt-BR' ? source : dictionaries[current]?.[source] ?? source
  if (!values) return translated
  return translated.replace(/\{(\w+)\}/g, (mark, name: string) => (name in values ? String(values[name]) : mark))
}

const LocaleContext = createContext<Locale>(current)

/** Puts the language choice on screen: every component under it re-renders when the language changes. */
export function LocaleProvider({ children }: { children: ReactNode }) {
  const [locale, setState] = useState<Locale>(current)
  useEffect(() => {
    const update = () => setState(current)
    listeners.add(update)
    update()
    return () => { listeners.delete(update) }
  }, [])
  return <LocaleContext.Provider value={locale}>{children}</LocaleContext.Provider>
}

/** `t` for components: re-renders them when the language changes. */
export function useT() {
  useContext(LocaleContext)
  return t
}
