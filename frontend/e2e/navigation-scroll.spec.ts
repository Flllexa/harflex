import { expect, test } from '@playwright/test'

for (const width of [768, 1440]) {
  test(`all desktop navigation remains reachable in a short window at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 640 })
    await page.goto('/e2e/fixture.html')
    const navigation = page.getByRole('navigation', { name: 'Navegação principal' })
    await navigation.getByRole('button', { name: 'Configurações' }).click()
    await expect(page.getByRole('heading', { name: 'Configurações' })).toBeVisible()
    expect(await page.locator('#sidebar').evaluate(element => element.scrollTop)).toBeGreaterThan(0)
    expect(Math.abs(await page.locator('#sidebar').evaluate(element => element.getBoundingClientRect().top))).toBeLessThan(1)
  })
}
