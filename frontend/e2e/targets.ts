import type { Page } from '@playwright/test'

/**
 * Text of every `.touch-target` smaller than its minimum. Touch surfaces (coarse pointers and
 * windows under 768px) keep the 44px target; a mouse in a wide window may use compact 32px rows.
 */
export async function undersizedTargets(page: Page): Promise<(string | null)[]> {
  return page.locator('.touch-target').evaluateAll(elements => {
    const touch = window.matchMedia('(pointer: coarse)').matches || !window.matchMedia('(hover: hover)').matches || window.innerWidth < 768
    const minimum = touch ? 44 : 32
    return elements.filter(element => {
      const box = element.getBoundingClientRect()
      return box.width > 0 && box.height > 0 && (box.width < minimum || box.height < minimum)
    }).map(element => element.textContent)
  })
}

/**
 * Describes every visible checkbox or radio that has been stretched like a text field. A native one is
 * 13 to 20px; the old full-row, 44px-tall box squeezed its label down to a letter per line.
 */
export async function stretchedChecks(page: Page): Promise<string[]> {
  return page.locator('input[type="checkbox"], input[type="radio"]').evaluateAll(inputs => inputs.flatMap(input => {
    const box = input.getBoundingClientRect()
    if (box.width === 0 || box.height === 0 || (box.width <= 28 && box.height <= 28)) return []
    const label = input.closest('label')?.textContent?.trim().slice(0, 40) ?? input.getAttribute('name') ?? ''
    return [`${(input as HTMLInputElement).type} ${Math.round(box.width)}x${Math.round(box.height)} · ${label}`]
  }))
}

/** Width of the sentence next to a checkbox and how many lines it takes: squeezed text is narrow and tall. */
export async function checkboxSentence(page: Page, name: RegExp): Promise<{ width: number; lines: number }> {
  return page.getByRole('checkbox', { name }).evaluate(input => {
    const text = [...(input.closest('label')?.childNodes ?? [])].filter(node => node.nodeType === Node.TEXT_NODE).pop()
    if (!text) return { width: 0, lines: 0 }
    const range = document.createRange()
    range.selectNodeContents(text)
    const rects = [...range.getClientRects()].filter(rect => rect.width > 0)
    return { width: Math.round(Math.max(0, ...rects.map(rect => rect.width))), lines: new Set(rects.map(rect => Math.round(rect.top))).size }
  })
}
