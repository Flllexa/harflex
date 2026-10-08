// What was typed in a chat's message box stays with that chat: switching chat or project keeps it, reopening brings
// it back. Kept on this computer only.
const storageKey = 'harflex.chatDrafts'
const maxDrafts = 50

type Drafts = Record<string, { text: string; at: number }>

function read(): Drafts {
  try {
    const value = JSON.parse(window.localStorage.getItem(storageKey) ?? '{}')
    return value && typeof value === 'object' && !Array.isArray(value) ? value as Drafts : {}
  } catch { return {} }
}

function write(drafts: Drafts) {
  // The oldest go first when there are too many.
  const kept = Object.entries(drafts).sort(([, a], [, b]) => b.at - a.at).slice(0, maxDrafts)
  try { window.localStorage.setItem(storageKey, JSON.stringify(Object.fromEntries(kept))) } catch { /* Only a convenience. */ }
}

export function saveChatDraft(sessionId: string, text: string) {
  const drafts = read()
  if (text.trim()) drafts[sessionId] = { text, at: Date.now() }
  else delete drafts[sessionId]
  write(drafts)
}

/** The chat's saved draft, removed from storage now that the box holds it again. */
export function takeChatDraft(sessionId: string): string {
  const drafts = read()
  const saved = drafts[sessionId]
  if (!saved) return ''
  delete drafts[sessionId]
  write(drafts)
  return typeof saved.text === 'string' ? saved.text : ''
}
