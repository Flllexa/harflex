import { Browser, Clipboard } from '@wailsio/runtime'
import { safeHref } from './markdown'

// Thin wrappers over the desktop shell with web fallbacks. Runtime calls fail outside the
// desktop shell, so every channel falls back to the plain web API.

/** Opens an http(s)/mailto link in the system browser; anything else is ignored. */
export async function openExternal(url: string): Promise<boolean> {
  const href = safeHref(url)
  if (!href) return false
  try {
    await Browser.OpenURL(href)
    return true
  } catch {
    // Not running inside the desktop shell.
  }
  return window.open(href, '_blank', 'noopener,noreferrer') !== null
}

/** Copies text to the clipboard through the best channel available; resolves to success. */
export async function copyText(text: string): Promise<boolean> {
  if (window.isSecureContext && navigator.clipboard?.writeText) {
    try { await navigator.clipboard.writeText(text); return true } catch { /* try the next channel */ }
  }
  try {
    await Clipboard.SetText(text)
    return true
  } catch { /* not in the desktop shell */ }
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.style.cssText = 'position:fixed;top:0;left:0;opacity:0;pointer-events:none'
  document.body.append(area)
  area.select()
  try { return document.execCommand('copy') } catch { return false } finally { area.remove() }
}
