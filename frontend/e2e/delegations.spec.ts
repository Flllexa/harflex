import { expect, test, type Page } from '@playwright/test'

async function openAgents(page: Page, width: number) {
  if (width < 768) await page.getByRole('button', { name: 'Abrir navegação' }).click()
  await page.getByRole('navigation', { name: 'Navegação principal' }).getByRole('button', { name: 'Agentes' }).click()
}

async function openConversation(page: Page, width: number) {
  if (width < 768) await page.getByRole('button', { name: 'Abrir navegação' }).click()
  await page.getByRole('navigation', { name: 'Navegação principal' }).getByRole('button', { name: 'Conversas' }).click()
}

for (const width of [320, 768, 900, 1440]) {
  test(`delegation hierarchy and contextual cancellation at ${width}px`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/e2e/fixture.html?scenario=delegations')
    await page.getByLabel('Caminho da pasta').fill('/synthetic/workspace/api-faturas')
    await page.getByRole('button', { name: 'Abrir projeto' }).click()
    await page.getByRole('button', { name: 'Abrir histórico' }).first().click()
    await openAgents(page, width)
    await expect(page.getByRole('heading', { name: 'Delegações da sessão' })).toBeVisible()
    await expect(page.getByText('Em execução', { exact: true })).toBeVisible()
    const expand = page.getByRole('button', { name: 'Ver subagentes de child-ru' })
    await expand.focus()
    await expect(expand).toBeFocused()
    await expand.click()
    await expect(page.getByText('Critérios revisados no journal.')).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
    await page.screenshot({ path: info.outputPath(`delegations-${width}.png`), fullPage: true })

    await page.getByRole('button', { name: 'Abrir conversa do subagente child-ru' }).click()
    await expect(page.getByLabel('Mensagem')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Voltar à sessão pai' })).toBeDisabled()
    await openAgents(page, width)
    await page.getByRole('button', { name: 'Cancelar subagente child-ru' }).click()
    await expect(page.getByText('Cancelado')).toBeVisible()
    await openConversation(page, width)
    await page.getByRole('button', { name: 'Voltar à sessão pai' }).click()
    await expect(page.getByLabel('Mensagem')).toBeVisible()
  })
}

test('recovers the parent link and cancellation readback after reloading', async ({ page }) => {
  await page.setViewportSize({ width: 900, height: 900 })
  await page.goto('/e2e/fixture.html?scenario=delegations')
  await page.getByLabel('Caminho da pasta').fill('/synthetic/workspace/api-faturas')
  await page.getByRole('button', { name: 'Abrir projeto' }).click()
  await page.getByRole('button', { name: 'Abrir histórico' }).nth(1).click()
  await openAgents(page, 900)
  await page.getByRole('button', { name: 'Cancelar subagente child-ru' }).click()
  await expect(page.getByText('Cancelado')).toBeVisible()
  await page.reload()
  await page.getByLabel('Caminho da pasta').fill('/synthetic/workspace/api-faturas')
  await page.getByRole('button', { name: 'Abrir projeto' }).click()
  await page.getByRole('button', { name: 'Abrir histórico' }).nth(1).click()
  await openAgents(page, 900)
  await expect(page.getByRole('button', { name: 'Voltar para sessão pai' })).toBeVisible()
  await expect(page.getByText('Cancelado')).toBeVisible()
  await page.getByRole('button', { name: 'Voltar para sessão pai' }).click()
  await expect(page.getByLabel('Mensagem')).toBeVisible()
})

for (const width of [320, 1440]) {
  test(`prepares a queued delegated task after reload without executing at ${width}px`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/e2e/fixture.html?scenario=delegation-queued')
    await page.reload()
    await page.getByLabel('Caminho da pasta').fill('/synthetic/workspace/api-faturas')
    await page.getByRole('button', { name: 'Abrir projeto' }).click()
    await page.getByRole('button', { name: 'Abrir histórico' }).click()
    await page.getByRole('button', { name: 'Preparar tarefa delegada' }).click()
    await expect(page.getByLabel('Mensagem')).toHaveValue('Revisar a SPEC recuperada')
    await expect(page.getByRole('status').last()).toContainText('Pronto')
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
    await page.screenshot({ path: info.outputPath(`delegation-queued-${width}.png`), fullPage: true })
  })
}
