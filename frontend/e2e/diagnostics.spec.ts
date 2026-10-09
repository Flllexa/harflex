import { expect, test } from './locale'

for (const width of [320, 1440]) {
  test(`diagnostics show real local checks at ${width}px`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/e2e/fixture.html?scenario=diagnostics')
    await page.getByLabel('Caminho da pasta').fill('/synthetic/workspace/api-faturas')
    await page.getByRole('button', { name: 'Abrir projeto' }).click()
    if (width < 768) await page.getByRole('button', { name: 'Abrir navegação' }).click()
    await page.getByRole('navigation', { name: 'Navegação principal' }).getByRole('button', { name: 'Diagnóstico' }).click()
    await expect(page.getByRole('heading', { name: 'Diagnóstico do runtime' })).toBeVisible()
    await expect(page.getByText('1 de 2 disponíveis')).toBeVisible()
    await expect(page.getByText('Nenhuma referência para testar')).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
    await page.screenshot({ path: info.outputPath(`diagnostics-${width}.png`), fullPage: true })
    await page.getByRole('button', { name: 'Ver configurações' }).click()
    await expect(page.getByRole('heading', { name: 'Configurações' })).toBeVisible()
  })
}
