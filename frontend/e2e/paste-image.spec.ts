import { expect, test } from './locale'

// A screenshot copied to the clipboard and pasted into the chat box is attached, with a small picture of itself.
for (const width of [320, 1440]) {
  test(`a pasted image is attached with its picture at ${width}px`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await page.addInitScript(() => localStorage.setItem('harflex:view-mode', 'casual'))
    await page.goto('/e2e/fixture.html?scenario=casual-history')
    await page.getByLabel('Caminho da pasta').fill('/Users/dev/todo-app')
    await page.getByRole('button', { name: 'Abrir projeto' }).click()
    if (width < 768) await page.getByRole('button', { name: 'Abrir histórico de chats' }).click()
    await page.getByRole('button', { name: /Criar uma lista TODO em TypeScript/ }).click()
    const box = page.getByLabel('Mensagem')
    await expect(box).toBeEnabled()
    await box.fill('Veja este erro')
    await box.evaluate(async element => {
      // A 2x2 red PNG, as the system clipboard hands a screenshot to the page.
      const png = Uint8Array.from(atob('iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFklEQVR4nGP8z8DwnwEJMDGgAQYGBgAOxgEDYm6P1QAAAABJRU5ErkJggg=='), character => character.charCodeAt(0))
      const clipboard = new DataTransfer()
      clipboard.items.add(new File([png], 'image.png', { type: 'image/png' }))
      element.dispatchEvent(new ClipboardEvent('paste', { clipboardData: clipboard, bubbles: true, cancelable: true }))
    })
    const chips = page.getByRole('list', { name: 'Anexos' })
    await expect(chips.getByText(/^imagem-colada-\d{8}-\d{6}\.png$/)).toBeVisible()
    await expect(chips.locator('img.chat-composer-thumb')).toHaveAttribute('src', /^blob:/)
    await expect(box).toHaveValue('Veja este erro')
    await expect(page.getByRole('button', { name: 'Enviar' })).toBeEnabled()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
    await page.screenshot({ path: info.outputPath(`paste-image-${width}.png`) })
  })
}
