import { expect, test } from '@playwright/test'
import { undersizedTargets } from './targets'

for (const width of [320, 768, 900, 1440]) {
  test(`shell remains usable at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/')
    // Outside the desktop runtime there is no backend: the workbench says so instead of faking data.
    await expect(page.getByRole('heading', { level: 1, name: 'Nenhum projeto aberto' })).toBeVisible()
    await expect(page.getByText('Backend local indisponível', { exact: true }).first()).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
    await expect(page.getByRole('navigation', { name: 'SDD Pipeline' }).locator('[aria-current="step"]')).toHaveCount(0)
    await page.screenshot({ path: testInfo.outputPath(`shell-${width}.png`), fullPage: true })
    const trigger = page.getByRole('button', { name: 'Atividade', exact: true })
    if (width >= 1024) await trigger.click()
    await trigger.click()
    await expect(page.getByRole('complementary', { name: 'Atividade do trabalho' })).toBeVisible()
    if (width < 1024) await expect(page.getByRole('dialog', { name: 'Atividade', exact: true })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(trigger).toBeFocused()
    await expect(trigger).toHaveAttribute('aria-expanded', 'false')
    if (width < 768) {
      const menu = page.getByRole('button', { name: 'Abrir navegação' })
      await menu.click()
      await expect(page.getByRole('dialog', { name: 'Navegação' })).toBeVisible()
      await page.getByRole('button', { name: 'Configurações', exact: true }).click()
      await expect(menu).toBeFocused()
      await expect(page.getByRole('heading', { name: 'Configurações' })).toBeVisible()
    }
    expect(await undersizedTargets(page)).toEqual([])
  })
}

test('mobile navigation becomes a rail after resizing to tablet', async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 900 })
  await page.goto('/')
  await page.getByRole('button', { name: 'Abrir navegação' }).click()
  await page.setViewportSize({ width: 900, height: 768 })
  await expect(page.getByRole('dialog', { name: 'Navegação' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Atividade', exact: true })).toBeEnabled()
  await page.getByRole('button', { name: 'Atividade', exact: true }).click()
  await expect(page.getByRole('dialog', { name: 'Atividade', exact: true })).toBeVisible()
})

test('an unstarted pipeline never invents a current stage after resize', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/')
  const pipeline = page.getByRole('navigation', { name: 'SDD Pipeline' })
  await expect(pipeline.locator('[aria-current="step"]')).toHaveCount(0)
  await expect(pipeline).toContainText('Nenhum pipeline ativo')
  await page.setViewportSize({ width: 320, height: 900 })
  await expect(pipeline.locator('[aria-current="step"]')).toHaveCount(0)
  await page.screenshot({ path: testInfo.outputPath('pipeline-resized-320.png'), fullPage: true })
})

for (const width of [900, 320]) {
  test(`activity restores focus when desktop shrinks to ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.goto('/')
    const close = page.getByRole('button', { name: 'Fechar atividade' })
    await close.focus()
    await expect(close).toBeFocused()
    await page.setViewportSize({ width, height: 900 })
    const trigger = page.getByRole('button', { name: 'Atividade', exact: true })
    await expect(trigger).toHaveAttribute('aria-expanded', 'false')
    await expect(trigger).toBeFocused()
    await page.screenshot({ path: testInfo.outputPath(`focus-resized-${width}.png`), fullPage: true })
  })

  test(`activity preserves outside focus when desktop shrinks to ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.goto('/')
    const pipeline = page.getByRole('navigation', { name: 'SDD Pipeline' })
    await pipeline.focus()
    await page.setViewportSize({ width, height: 900 })
    await expect(page.getByRole('button', { name: 'Atividade', exact: true })).toHaveAttribute('aria-expanded', 'false')
    await expect(pipeline).toBeFocused()
  })
}
