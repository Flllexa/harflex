import { expect, test, type Page } from '@playwright/test'
import { undersizedTargets } from './targets'

async function openConversation(page: Page, width: number) {
  await page.setViewportSize({ width, height: 900 })
  await page.goto('/e2e/fixture.html')
  await page.getByLabel('Caminho da pasta').fill('/Users/dev/api-faturas')
  await page.getByRole('button', { name: 'Abrir projeto' }).click()
  await page.getByLabel('Motivo para pular SDD nesta sessão').fill('Teste de conversa livre')
  await page.getByRole('button', { name: 'Iniciar sessão livre' }).click()
  await expect(page.getByLabel('Mensagem')).toBeEnabled()
}

// The person decides, from the conversation itself, whether the agent has to ask. Turning the questions off
// takes one deliberate confirmation and leaves a visible reminder.
for (const width of [320, 768, 1440]) {
  test(`permissions are one menu away and full access needs a confirmation at ${width}px`, async ({ page }, info) => {
    await openConversation(page, width)
    const picker = page.getByTestId('picker-permission-profile')
    await expect(picker.getByRole('button')).toHaveText(/Perguntar/)
    await expect(page.getByText('Sem pedir aprovação')).toHaveCount(0)

    await picker.getByRole('button').click()
    await expect(page.getByRole('option')).toHaveText(['Perguntar', 'Workspace confiável', 'Acesso total'])
    await page.getByRole('option', { name: 'Workspace confiável' }).click()
    await expect(picker.getByRole('button')).toHaveText(/Workspace confiável/)

    await picker.getByRole('button').click()
    await page.getByRole('option', { name: 'Acesso total' }).click()
    const dialog = page.getByRole('dialog', { name: 'Ativar acesso total neste projeto?' })
    await expect(dialog).toBeVisible()
    await page.screenshot({ path: info.outputPath(`permissions-confirm-${width}.png`) })
    // Cancelling changes nothing.
    await dialog.getByRole('button', { name: 'Cancelar' }).click()
    await expect(dialog).toHaveCount(0)
    await expect(picker.getByRole('button')).toHaveText(/Workspace confiável/)

    await picker.getByRole('button').click()
    await page.getByRole('option', { name: 'Acesso total' }).click()
    await dialog.getByRole('button', { name: 'Ativar acesso total' }).click()
    await expect(dialog).toHaveCount(0)
    await expect(picker.getByRole('button')).toHaveText(/Acesso total/)
    await expect(page.getByText('Sem pedir aprovação')).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
    expect(await undersizedTargets(page)).toEqual([])
    await page.screenshot({ path: info.outputPath(`permissions-full-${width}.png`) })

    // Going back needs no confirmation.
    await picker.getByRole('button').click()
    await page.getByRole('option', { name: 'Perguntar' }).click()
    await expect(picker.getByRole('button')).toHaveText(/Perguntar/)
    await expect(page.getByText('Sem pedir aprovação')).toHaveCount(0)
  })
}

test('settings offers the same full access choice behind the same confirmation', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.goto('/e2e/fixture.html?scenario=settings')
  await page.getByLabel('Caminho da pasta').fill('/synthetic/workspace/api-faturas')
  await page.getByRole('button', { name: 'Abrir projeto' }).click()
  await page.getByRole('navigation', { name: 'Navegação principal' }).getByRole('button', { name: 'Configurações' }).click()
  const full = page.getByRole('radio', { name: /Acesso total/ })
  await expect(full).not.toBeChecked()
  await full.click()
  const dialog = page.getByRole('dialog', { name: 'Ativar acesso total neste projeto?' })
  await dialog.getByRole('button', { name: 'Cancelar' }).click()
  await expect(full).not.toBeChecked()
  await full.click()
  await dialog.getByRole('button', { name: 'Ativar acesso total' }).click()
  await expect(full).toBeChecked()
  await expect(page.getByRole('status')).toContainText('Permissões do projeto salvas')
  await page.getByRole('radio', { name: /Perguntar/ }).click()
  await expect(page.getByRole('radio', { name: /Perguntar/ })).toBeChecked()
})
