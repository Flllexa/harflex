import { expect, test } from './locale'

for (const width of [320, 1440]) {
  test(`repository inspection shows both diffs at ${width}px`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/e2e/fixture.html?scenario=repositories')
    await page.getByLabel('Caminho da pasta').fill('/synthetic/workspace/api-faturas')
    await page.getByRole('button', { name: 'Abrir projeto' }).click()
    if (width < 768) await page.getByRole('button', { name: 'Abrir navegação' }).click()
    await page.getByRole('navigation', { name: 'Navegação principal' }).getByRole('button', { name: 'Repositórios' }).click()
    await expect(page.getByRole('heading', { name: 'Repositório Git' })).toBeVisible()
    await expect(page.getByText('docs/exportacao.md')).toBeVisible()
    await page.getByRole('button', { name: 'Mudanças preparadas' }).click()
    await expect(page.getByText('+func Exportar() {}')).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
    await page.screenshot({ path: info.outputPath(`repository-${width}.png`), fullPage: true })
  })
}
