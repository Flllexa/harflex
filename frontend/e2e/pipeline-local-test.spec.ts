import { expect, test } from './locale'

// "Testar localmente" from the Code review: the AI applies the change to the folder and brings the project up; undo
// puts the folder back and the card returns to its start.
for (const width of [320, 1440]) {
  test(`a local test starts from the Code review and is undone at ${width}px`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/e2e/fixture.html?scenario=pipeline-code&stage=code-review')
    await page.getByLabel('Caminho da pasta').fill('/synthetic/workspace/api-faturas')
    await page.getByRole('button', { name: 'Abrir projeto' }).click()
    const pipelines = async () => {
      if (width < 768) await page.getByRole('button', { name: 'Abrir navegação' }).click()
      await page.getByRole('navigation', { name: 'Navegação principal' }).getByRole('button', { name: 'Pipelines' }).click()
    }
    await pipelines()
    const card = page.getByRole('region', { name: 'Teste local' })
    await card.scrollIntoViewIfNeeded()
    await expect(card.getByText(/aplica esta versão na sua pasta/)).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
    await card.getByRole('button', { name: 'Testar localmente' }).click()
    await expect(page.getByLabel('Mensagem')).toBeVisible()
    await pipelines()
    await expect(card.getByText('Aplicado na sua pasta para teste · versão 1')).toBeVisible()
    await expect(card.getByRole('link', { name: 'App' })).toHaveAttribute('href', 'http://localhost:5173')
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
    await page.screenshot({ path: info.outputPath(`local-test-${width}.png`), fullPage: true })
    await card.getByRole('button', { name: 'Desfazer teste' }).click()
    await expect(card.getByRole('button', { name: 'Testar localmente' })).toBeVisible()
  })
}
