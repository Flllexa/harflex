import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { Browser, Clipboard } from '@wailsio/runtime'
import { copyText, openExternal } from './desktop'

vi.mock('@wailsio/runtime', () => ({ Browser: { OpenURL: vi.fn() }, Clipboard: { SetText: vi.fn() } }))

beforeEach(() => { vi.mocked(Browser.OpenURL).mockReset(); vi.mocked(Clipboard.SetText).mockReset() })
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('openExternal', () => {
  it('opens only safe web links, through the desktop shell', async () => {
    vi.mocked(Browser.OpenURL).mockResolvedValue(undefined)
    expect(await openExternal('https://example.com/a')).toBe(true)
    expect(Browser.OpenURL).toHaveBeenCalledWith('https://example.com/a')
    for (const unsafe of ['javascript:alert(1)', 'file:///etc/passwd', 'data:text/html,x', '/relative', '']) {
      expect(await openExternal(unsafe)).toBe(false)
    }
    expect(Browser.OpenURL).toHaveBeenCalledTimes(1)
  })

  it('falls back to the browser window outside the desktop shell', async () => {
    vi.mocked(Browser.OpenURL).mockRejectedValue(new Error('no runtime'))
    const open = vi.spyOn(window, 'open').mockReturnValue({} as Window)
    expect(await openExternal('https://example.com/b')).toBe(true)
    expect(open).toHaveBeenCalledWith('https://example.com/b', '_blank', 'noopener,noreferrer')
  })
})

describe('copyText', () => {
  it('prefers the async clipboard in a secure context', async () => {
    const writeText = vi.fn(async () => undefined)
    vi.stubGlobal('isSecureContext', true)
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    expect(await copyText('abc')).toBe(true)
    expect(writeText).toHaveBeenCalledWith('abc')
    expect(Clipboard.SetText).not.toHaveBeenCalled()
  })

  it('uses the desktop clipboard when the web one is unavailable', async () => {
    vi.stubGlobal('isSecureContext', false)
    vi.stubGlobal('navigator', {})
    vi.mocked(Clipboard.SetText).mockResolvedValue(undefined)
    expect(await copyText('abc')).toBe(true)
    expect(Clipboard.SetText).toHaveBeenCalledWith('abc')
  })

  it('falls back to a selection copy and reports failure honestly', async () => {
    vi.stubGlobal('isSecureContext', false)
    vi.stubGlobal('navigator', {})
    vi.mocked(Clipboard.SetText).mockRejectedValue(new Error('no runtime'))
    const exec = vi.fn(() => true)
    Object.defineProperty(document, 'execCommand', { value: exec, configurable: true })
    expect(await copyText('abc')).toBe(true)
    expect(exec).toHaveBeenCalledWith('copy')
    exec.mockReturnValue(false)
    expect(await copyText('abc')).toBe(false)
    expect(document.querySelector('textarea')).toBeNull()
  })
})
