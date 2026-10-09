import { expect, test } from './locale'

for (const width of [320, 1440]) {
  test(`agent profile can be saved and used at ${width}px`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/e2e/fixture.html?scenario=agents')
    await page.getByLabel('Caminho da pasta').fill('/synthetic/workspace/api-faturas')
    await page.getByRole('button', { name: 'Abrir projeto' }).click()
    if (width < 768) await page.getByRole('button', { name: 'Abrir navegação' }).click()
    await page.getByRole('navigation', { name: 'Navegação principal' }).getByRole('button', { name: 'Agentes' }).click()
    await page.getByLabel('Nome do agente').fill('Analista de código')
    await page.getByLabel('Instruções do agente').fill('Leia o código antes de responder.')
    await page.getByRole('button', { name: 'Salvar agente' }).click()
    await expect(page.getByRole('button', { name: 'Conversar com Analista de código' })).toBeEnabled()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
    await page.screenshot({ path: info.outputPath(`agents-${width}.png`), fullPage: true })
    await page.getByRole('button', { name: 'Conversar com Analista de código' }).click()
    await page.getByLabel('Motivo para conversar sem SDD com Analista de código').fill('Pesquisa pontual')
    await page.getByRole('button', { name: 'Iniciar sessão livre com Analista de código' }).click()
    await expect(page.getByLabel('Mensagem')).toBeVisible()
  })
}
