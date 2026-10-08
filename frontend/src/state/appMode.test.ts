import { describe, expect, it } from 'vitest'
import { readAppMode, writeAppMode, type AppMode } from './appMode'

function memoryStorage() {
  const values = new Map<string, string>()
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => { values.set(key, value) },
  }
}

describe('app view preference', () => {
  it('defaults to Professional when storage is empty or invalid', () => {
    const storage = memoryStorage()
    expect(readAppMode(storage)).toBe('professional')
    storage.setItem('harflex:view-mode', 'unexpected')
    expect(readAppMode(storage)).toBe('professional')
  })

  it('persists and reloads Casual without changing the Professional default', () => {
    const storage = memoryStorage()
    const mode: AppMode = 'casual'
    writeAppMode(mode, storage)
    expect(readAppMode(storage)).toBe('casual')
    writeAppMode('professional', storage)
    expect(readAppMode(storage)).toBe('professional')
  })

  it('fails closed when local storage is unavailable', () => {
    const storage = {
      getItem: () => { throw new Error('storage unavailable') },
      setItem: () => { throw new Error('storage unavailable') },
    }
    expect(readAppMode(storage)).toBe('professional')
    expect(() => writeAppMode('casual', storage)).not.toThrow()
  })
})
