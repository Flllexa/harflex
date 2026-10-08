import { afterEach, expect, it } from 'vitest'
import { saveChatDraft, takeChatDraft } from './chatDrafts'

afterEach(() => window.localStorage.removeItem('harflex.chatDrafts'))

it('keeps one draft per chat and hands it back once', () => {
  saveChatDraft('a', 'texto de a')
  saveChatDraft('b', 'texto de b')
  expect(takeChatDraft('a')).toBe('texto de a')
  expect(takeChatDraft('a')).toBe('')
  saveChatDraft('b', '   ')
  expect(takeChatDraft('b')).toBe('')
})
