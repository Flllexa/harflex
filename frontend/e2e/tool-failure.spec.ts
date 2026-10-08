import { expect, test } from '@playwright/test'
import { undersizedTargets } from './targets'

// A tool that cannot do what the model asked (a file that does not exist yet) tells the model and the run
// goes on. The person sees why in plain Portuguese, not "tool execution failed".
for (const width of [320, 768, 1440]) {
  test(`a missing file is explained and the run completes at ${width}px`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/e2e/fixture.html')
    await page.getByLabel('Caminho da pasta').fill('/Users/dev/api-faturas')
    await page.getByRole('button', { name: 'Abrir projeto' }).click()
    await page.getByLabel('Motivo para pular SDD nesta sessão').fill('Teste de conversa livre')
    await page.getByRole('button', { name: 'Iniciar sessão livre' }).click()
    await page.getByLabel('Mensagem').fill('Leia o arquivo inexistente e crie-o')
    await page.keyboard.press('Enter')

    const card = page.getByRole('article', { name: 'Ferramenta read' })
    await expect(card).toBeVisible()
    await expect(card).toContainText('Falhou')
    await expect(card).toContainText('Não encontrado.')
    await expect(card).toContainText('"index.html" does not exist')
    await expect(card).toContainText('A execução continuou e o modelo foi avisado.')
    await expect(card).not.toContainText('tool execution failed')
    // The run did not stop: the assistant went on and the conversation is complete.
    await expect(page.getByText('Como ele não existe, vou criá-lo.')).toBeVisible()
    await expect(page.getByRole('status').filter({ hasText: 'Concluído' })).toBeVisible()
    await expect(page.getByLabel('Mensagem')).toBeEnabled()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
    expect(await undersizedTargets(page)).toEqual([])
    await page.screenshot({ path: info.outputPath(`tool-failure-${width}.png`) })
  })
}
