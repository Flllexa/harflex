import { expect, test, type Page } from './locale'

const fixtureKey = 'harflex:conversational-design'
async function openProject(page: Page) {
  await page.getByLabel('Caminho da pasta').fill('/synthetic/workspace/faturas')
  await page.getByRole('button', { name: 'Abrir projeto' }).click()
  const menu = page.getByRole('button', { name: 'Abrir navegação' })
  if (await menu.isVisible()) await menu.click()
  await page.getByRole('navigation', { name: 'Navegação principal' }).getByRole('button', { name: 'Pipelines' }).click()
  const activity = page.getByRole('button', { name: 'Painel lateral', exact: true })
  if (await activity.getAttribute('aria-expanded') === 'true') await activity.click()
}
async function pane(page: Page, name: 'Conversa' | 'Documentos') {
  const button = page.getByRole('navigation', { name: 'Área de preparação' }).getByRole('button', { name, exact: true })
  if (await button.isVisible()) await button.click()
}
async function saved(page: Page) { return page.evaluate(key => JSON.parse(sessionStorage.getItem(key)!), fixtureKey) }
async function start(page: Page) {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto('/e2e/fixture.html?scenario=conversational-design')
  await openProject(page)
  await page.getByLabel('Discovery', { exact: true }).fill('Exportar faturas em CSV com filtros por período, para a equipe financeira.')
  await page.getByRole('button', { name: 'Criar pipeline' }).click()
  await expect.poll(async () => (await saved(page)).prepareCount).toBe(1)
  await expect.poll(async () => (Object.values((await saved(page)).designs)[0] as { state: string }).state).toBe('ready')
  await pane(page, 'Documentos')
}

test('prepares SPEC and Brainstorm, approves the flow and design, then the Plan, and reaches Code', async ({ page }, info) => {
  await start(page)
  await expect(page.getByRole('article', { name: 'Documento SPEC versão 1' })).toContainText('Critérios de aceite')
  expect(Object.values((await saved(page)).designs as Record<string, { documents: Record<string, { content: string }> }>)[0].documents.plan.content).toBe('')
  await page.getByRole('tab', { name: /Brainstorm/ }).click()
  const flow = page.getByRole('region', { name: 'Fluxo' })
  await expect(flow.locator('.react-flow')).toContainText('Filtrar período')
  const design = page.getByRole('region', { name: 'Design' })
  await expect(design.getByRole('tab')).toHaveCount(2)
  await expect(design.locator('iframe')).toHaveAttribute('sandbox', '')
  await design.getByRole('tab', { name: 'Exportação pronta' }).click()
  await expect(design.frameLocator('iframe').getByRole('heading', { name: 'Exportação pronta' })).toBeVisible()
  // The policy written before <html> must be the frame's own: in its head, and enforced.
  const frame = page.frames().find(item => item.url() === 'about:srcdoc')!
  expect(await frame.evaluate(() => document.head.querySelector('meta[http-equiv="Content-Security-Policy"]')?.getAttribute('content'))).toContain("default-src 'none'")
  const outside: string[] = []
  page.on('requestfailed', request => { if (request.url().includes('example.invalid')) outside.push(request.failure()?.errorText ?? '') })
  page.on('requestfinished', request => { if (request.url().includes('example.invalid')) outside.push('loaded') })
  await frame.evaluate(() => { const image = document.createElement('img'); image.src = 'https://example.invalid/a.png'; document.body.append(image) })
  await expect.poll(() => outside).toEqual(['csp'])
  await expect(flow.locator('.react-flow__edge').first()).toBeAttached()
  await page.screenshot({ path: info.outputPath('brainstorm-1440.png'), fullPage: true })
  await page.getByRole('button', { name: 'Aprovar fluxo e design' }).click()
  await expect.poll(async () => (await saved(page)).pipelines[0].currentStage).toBe('plan')
  await page.getByRole('tab', { name: 'Plan', exact: true }).click()
  await expect(page.getByRole('article', { name: 'Documento Plan versão 1' })).toContainText('Implementação')
  await page.getByRole('button', { name: 'Aprovar documentos e continuar para Code' }).click()
  await expect.poll(async () => (await saved(page)).pipelines[0].currentStage).toBe('code')
  await expect(page.getByRole('button', { name: 'Executar Coder' })).toBeEnabled()
})

test('drops the design box when the solution has no screens', async ({ page }) => {
  await start(page)
  await page.evaluate(key => {
    const value = JSON.parse(sessionStorage.getItem(key)!), design = Object.values(value.designs)[0] as { documents: Record<string, { content: string }> }
    design.documents.brainstorm = { ...design.documents.brainstorm, content: JSON.stringify({ flow: 'flowchart TD\n  A[Agendar a exportação] --> B[Gravar o arquivo]', design: null, noDesignReason: 'Só muda o job noturno: não há telas novas.', notes: '' }) }
    sessionStorage.setItem(key, JSON.stringify(value))
  }, fixtureKey)
  await page.reload()
  await openProject(page)
  await pane(page, 'Documentos')
  await page.getByRole('tab', { name: /Brainstorm/ }).click()
  await expect(page.getByText('Só muda o job noturno: não há telas novas.')).toBeVisible()
  await expect(page.getByRole('region', { name: 'Design' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Aprovar fluxo', exact: true })).toBeEnabled()
})

test('fits a 320px screen without horizontal scroll', async ({ page }) => {
  await start(page)
  await page.setViewportSize({ width: 320, height: 900 })
  await pane(page, 'Documentos')
  await page.getByRole('tab', { name: /Brainstorm/ }).click()
  await expect(page.getByRole('region', { name: 'Fluxo' }).locator('.react-flow__node').first()).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320)
  await expect(page.getByLabel('Pedido para a IA')).toBeVisible()
})
