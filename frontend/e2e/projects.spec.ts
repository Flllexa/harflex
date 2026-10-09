import { expect, test } from './locale'

for (const width of [320, 768, 900, 1440]) {
  test(`saved project catalog remains usable at ${width}px`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/e2e/fixture.html?scenario=projects')
    if (width < 768) await page.getByRole('button', { name: 'Abrir navegação' }).click()
    await page.getByRole('navigation', { name: 'Navegação principal' }).getByRole('button', { name: 'Projetos' }).click()
    await expect(page.getByRole('heading', { name: 'Projetos locais' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Abrir api-faturas' })).toBeEnabled()
    await expect(page.getByRole('button', { name: 'Abrir pasta-movida' })).toBeDisabled()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
    await page.screenshot({ path: info.outputPath(`projects-${width}.png`), fullPage: true })
    await page.getByRole('button', { name: 'Abrir api-faturas' }).click()
    await expect(page.getByRole('heading', { name: 'Começar pelo SDD' })).toBeVisible()
  })
}
