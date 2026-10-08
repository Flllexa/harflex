import { expect, test } from '@playwright/test'

for (const width of [320, 1440]) {
  test(`vault shows references and owner actions at ${width}px`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/e2e/fixture.html?scenario=vault')
    await page.getByLabel('Caminho da pasta').fill('/synthetic/workspace/api-faturas')
    await page.getByRole('button', { name: 'Abrir projeto' }).click()
    if (width < 768) await page.getByRole('button', { name: 'Abrir navegação' }).click()
    await page.getByRole('navigation', { name: 'Navegação principal' }).getByRole('button', { name: 'Vault' }).click()
    await expect(page.getByRole('heading', { name: 'Vault de referências' })).toBeVisible()
    await expect(page.getByText('2 referência(s) de credencial cadastrada(s)')).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
    await page.screenshot({ path: info.outputPath(`vault-${width}.png`), fullPage: true })
    await page.getByRole('button', { name: 'Gerenciar MCP' }).click()
    await expect(page.getByRole('heading', { name: 'Servidores MCP' })).toBeVisible()
  })
}
