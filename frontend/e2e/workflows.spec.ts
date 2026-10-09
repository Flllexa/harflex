import { expect, test } from './locale'

for (const width of [320, 1440]) {
  test(`workflow editor and execution remain usable at ${width}px`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/e2e/fixture.html?scenario=workflows')
    await page.getByLabel('Caminho da pasta').fill('/synthetic/workspace/api-faturas')
    await page.getByRole('button', { name: 'Abrir projeto' }).click()
    if (width < 768) await page.getByRole('button', { name: 'Abrir navegação' }).click()
    await page.getByRole('navigation', { name: 'Navegação principal' }).getByRole('button', { name: 'Workflows' }).click()
    await page.getByLabel('Nome do workflow').fill('Revisar e documentar')
    await page.getByLabel('Nome da etapa 1').fill('Revisar')
    await page.getByLabel('Prompt da etapa 1').fill('Leia o código')
    await page.getByRole('button', { name: 'Adicionar etapa' }).click()
    await page.getByLabel('Nome da etapa 2').fill('Documentar')
    await page.getByLabel('Prompt da etapa 2').fill('Escreva a documentação')
    await page.getByRole('button', { name: 'Salvar workflow' }).click()
    await expect(page.getByRole('button', { name: 'Iniciar Revisar e documentar' })).toBeEnabled()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
    await page.screenshot({ path: info.outputPath(`workflow-${width}.png`), fullPage: true })
    await page.getByRole('button', { name: 'Iniciar Revisar e documentar' }).click()
    await page.getByRole('button', { name: 'Executar etapa' }).click()
    await expect(page.getByText('Etapa 2 de 2: Documentar')).toBeVisible()
  })
}
