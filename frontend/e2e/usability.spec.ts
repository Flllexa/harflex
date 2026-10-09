import { expect, test, type Page } from './locale'

async function openProjectAndSession(page: Page) {
  await page.goto('/e2e/fixture.html')
  await page.getByLabel('Caminho da pasta').fill('/Users/dev/api-faturas')
  await page.getByRole('button', { name: 'Abrir projeto' }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'api-faturas' })).toBeVisible()
  await page.getByLabel('Motivo para pular SDD nesta sessão').fill('Teste de conversa livre')
  await page.getByRole('button', { name: 'Iniciar sessão livre' }).click()
}

test('reopens the last project at startup without asking for a folder', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/e2e/fixture.html?scenario=restore-project')
  await expect(page.getByRole('heading', { level: 1, name: 'api-faturas' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Iniciar trabalho SDD' })).toBeVisible()
  await expect(page.getByLabel('Caminho da pasta')).toHaveCount(0)
})

test('all eighteen destinations fit in a 900px window without scrolling the sidebar', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/e2e/fixture.html')
  const navigation = page.getByRole('navigation', { name: 'Navegação principal' })
  await expect(navigation.getByRole('button')).toHaveCount(18)
  expect(await page.locator('#sidebar').evaluate(element => element.scrollHeight <= element.clientHeight)).toBe(true)
  for (const name of ['Projetos', 'Configurações']) await expect(navigation.getByRole('button', { name })).toBeInViewport({ ratio: 1 })
})

test.describe('touch devices', () => {
  test.use({ hasTouch: true, isMobile: true })
  test('keep 44px navigation targets even on a wide screen', async ({ page }) => {
    await page.setViewportSize({ width: 1024, height: 768 })
    await page.goto('/e2e/fixture.html')
    const heights = await page.getByRole('navigation', { name: 'Navegação principal' }).getByRole('button').evaluateAll(buttons => buttons.map(button => Math.round(button.getBoundingClientRect().height)))
    expect(heights.length).toBe(18)
    expect(Math.min(...heights)).toBeGreaterThanOrEqual(44)
  })
})

test('a new destination starts at its top instead of inheriting the old scroll', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 600 })
  await page.goto('/e2e/fixture.html')
  const navigation = page.getByRole('navigation', { name: 'Navegação principal' })
  await navigation.getByRole('button', { name: 'Configurações' }).click()
  await expect(page.getByRole('heading', { name: 'Configurações do Harflex' })).toBeVisible()
  const area = page.locator('#work-area')
  await area.evaluate(element => { element.scrollTop = element.scrollHeight })
  expect(await area.evaluate(element => element.scrollTop)).toBeGreaterThan(0)
  await navigation.getByRole('button', { name: 'Logs' }).click()
  await expect(page.getByRole('heading', { name: 'Logs e auditoria' })).toBeVisible()
  expect(await area.evaluate(element => element.scrollTop)).toBe(0)
})

for (const viewport of [{ width: 1280, height: 720 }, { width: 1440, height: 900 }, { width: 900, height: 640 }]) {
  test(`the approval, its content and the composer stay in view at ${viewport.width}x${viewport.height}`, async ({ page }) => {
    await page.setViewportSize(viewport)
    await openProjectAndSession(page)
    await page.getByLabel('Mensagem').fill('Crie notas')
    await page.keyboard.press('Enter')
    const approval = page.getByRole('group', { name: 'Aprovação necessária' })
    // At the app's minimum height the card's border may scroll under its own edge; the decision itself never does.
    await expect(approval).toBeInViewport({ ratio: viewport.height < 700 ? 0.85 : 1 })
    await expect(approval.getByRole('button', { name: 'Aprovar' })).toBeInViewport({ ratio: 1 })
    await expect(approval.getByRole('button', { name: 'Negar' })).toBeInViewport({ ratio: 1 })
    await expect(page.getByLabel('Mensagem')).toBeInViewport({ ratio: 1 })
    // Approving is informed: the content about to be written is on the card.
    await expect(approval.getByText('Conteúdo a gravar')).toBeVisible()
    await expect(approval.getByLabel('Conteúdo a gravar')).toContainText('olá')
    // The window does not scroll; the conversation owns its own scrolling.
    expect(await page.evaluate(() => document.documentElement.scrollHeight <= window.innerHeight)).toBe(true)
  })
}

// The desktop window hides its title bar and starts a window drag on any click in the top 50px,
// so a button there would never receive its click. Nothing interactive may sit in that strip.
for (const width of [768, 1024, 1440]) {
  test(`no interactive control sits in the window-drag strip at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await openProjectAndSession(page)
    await page.evaluate(() => { document.documentElement.dataset.platform = 'mac' })
    const inside = await page.locator('button, a[href], input, select, textarea, summary, [role="button"], [role="tab"], [tabindex]:not([tabindex="-1"])').evaluateAll(elements => elements.filter(element => {
      const box = element.getBoundingClientRect()
      // React Aria keeps a visually hidden native <select> (aria-hidden, not focusable) beside each picker for forms; nobody can click it.
      return box.width > 0 && box.height > 0 && box.top < 50 && !element.closest('.skip-link') && !element.closest('[aria-hidden="true"]')
    }).map(element => `${element.tagName.toLowerCase()}:${(element.getAttribute('aria-label') || element.textContent || '').trim().slice(0, 30)}`))
    expect(inside).toEqual([])
    // On macOS the sidebar brand clears the traffic lights, and the sidebar still fits.
    const brand = await page.locator('.brand-word').boundingBox()
    expect(brand!.y).toBeGreaterThanOrEqual(38)
    if (width >= 1024) expect(await page.locator('#sidebar').evaluate(element => element.scrollHeight <= element.clientHeight)).toBe(true)
  })
}
