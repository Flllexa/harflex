import { expect, test } from './locale'

for (const width of [320, 1440]) {
  test(`skill editor and activation are usable at ${width}px`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/e2e/fixture.html?scenario=skills')
    await page.getByLabel('Caminho da pasta').fill('/synthetic/workspace/api-faturas')
    await page.getByRole('button', { name: 'Abrir projeto' }).click()
    if (width < 768) await page.getByRole('button', { name: 'Abrir navegação' }).click()
    await page.getByRole('navigation', { name: 'Navegação principal' }).getByRole('button', { name: 'Skills' }).click()
    await page.getByLabel('Nome da skill').fill('Revisão de testes')
    await page.getByLabel('Instruções da skill').fill('Verifique cobertura e critérios de aceitação.')
    await page.getByRole('button', { name: 'Salvar skill' }).click()
    await expect(page.getByText('Verifique cobertura e critérios de aceitação.')).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
    await page.screenshot({ path: info.outputPath(`skills-${width}.png`), fullPage: true })
    await page.getByRole('button', { name: 'Desativar' }).click()
    await expect(page.getByRole('button', { name: 'Ativar' })).toBeVisible()
    // With a skill saved, the list leads and the forms open on demand.
    await expect(page.getByLabel('Caminho da skill')).toHaveCount(0)
    await page.getByRole('button', { name: 'Nova skill' }).click()
    await page.getByLabel('Caminho da skill').fill('.agents/skills/review/SKILL.md')
    await page.getByRole('button', { name: 'Importar skill' }).click()
    await expect(page.getByText('Revise testes importados')).toBeVisible()
  })
}
