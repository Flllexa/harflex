import { describe, expect, it } from 'vitest'
import { markPlatform } from './platform'

describe('markPlatform', () => {
  it('names the host so macOS can reserve room for the traffic lights', () => {
    const root = document.createElement('html')
    markPlatform(root, 'MacIntel')
    expect(root.dataset.platform).toBe('mac')
    markPlatform(root, 'Win32')
    expect(root.dataset.platform).toBe('windows')
    markPlatform(root, 'Linux x86_64')
    expect(root.dataset.platform).toBe('other')
  })
})
