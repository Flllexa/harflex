import { expect, test, type Page } from './locale'

// Each phase of a pipeline can have its own provider (or Codex) and model, chosen where the pipelines are.
async function openPipelines(page: Page, width: number, extra = '') {
  await page.setViewportSize({ width, height: 900 })
  await page.goto(`/e2e/fixture.html?scenario=phase-executors${extra}`)
  await page.getByLabel('Caminho da pasta').fill('/synthetic/workspace/api-faturas')
  await page.getByRole('button', { name: 'Abrir projeto' }).click()
  if (width < 768) await page.getByRole('button', { name: 'Abrir navegação' }).click()
  await page.getByRole('navigation', { name: 'Navegação principal' }).getByRole('button', { name: 'Pipelines' }).click()
  return page.getByRole('list', { name: 'Executor de cada fase' })
}

for (const width of [320, 768, 1440]) {
  test(`chooses a provider and a model for each phase at ${width}px`, async ({ page }, info) => {
    const phases = await openPipelines(page, width)
    // A project with no pipeline sees the card open, with every phase on the default.
    await expect(phases.getByRole('listitem')).toHaveCount(6)
    await expect(phases).toContainText('Segue o padrão: Local API · api-model.')
    await expect(page.getByText('Todas seguem o padrão das Configurações')).toBeVisible()

    // An API profile is a complete choice: it is saved at once with the model of the profile.
    const row = (name: string) => phases.getByRole('listitem').filter({ has: page.locator('strong', { hasText: new RegExp(`^${name}$`) }) })
    const pick = async (phase: string, picker: string, option: string) => {
      await row(phase).getByTestId(picker).getByRole('button').first().click()
      await page.getByRole('option', { name: option, exact: true }).click()
    }
    await pick('SPEC', 'picker-phase-executor-spec', 'API grande · API')
    await expect(row('SPEC')).toContainText('Salvo · API grande · modelo do perfil')
    // The model can be changed from the catalog of that provider.
    await row('SPEC').getByRole('button', { name: /Abrir opções de Modelo de SPEC/ }).click()
    await page.getByRole('option', { name: 'large-reasoning' }).click()
    await expect(row('SPEC')).toContainText('Salvo · API grande · large-reasoning')

    // A CLI is saved only with a model.
    await pick('Code', 'picker-phase-executor-code', 'Codex CLI · CLI')
    await expect(row('Code')).toContainText('Escolha um modelo de Codex CLI para salvar.')
    await row('Code').getByRole('button', { name: /Abrir opções de Modelo de Code/ }).click()
    await page.getByRole('option', { name: 'Modelo Codex' }).click()
    await expect(row('Code')).toContainText('Salvo · Codex CLI · codex-model')
    await expect(page.getByText('2 de 6 com escolha própria')).toBeVisible()

    // The pull requests need the tool loop, so Codex is not offered there.
    await row('PRs').getByTestId('picker-phase-executor-prs').getByRole('button').first().click()
    await expect(page.getByRole('option', { name: 'Codex CLI · CLI' })).toHaveCount(0)
    await expect(page.getByRole('option', { name: 'API grande · API' })).toBeVisible()
    await page.keyboard.press('Escape')

    // The way back to the default.
    await pick('SPEC', 'picker-phase-executor-spec', 'Usar o padrão')
    await expect(row('SPEC')).toContainText('Segue o padrão: Local API · api-model.')
    await expect(page.getByText('1 de 6 com escolha própria')).toBeVisible()

    // Nothing spills out of the card or the window.
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
    const card = await page.locator('.phase-executors').boundingBox()
    for (const picker of await page.locator('.phase-row .ion-picker').all()) {
      const box = await picker.boundingBox()
      expect(box && card && box.x >= card.x - 1 && box.x + box.width <= card.x + card.width + 1).toBe(true)
    }
    await page.screenshot({ path: info.outputPath(`phase-executors-${width}.png`), fullPage: true })
  })
}

for (const width of [320, 1440]) {
  test(`says when the saved choices cannot be read, even with the card closed, and reads them again on request at ${width}px`, async ({ page }, info) => {
    const phases = await openPipelines(page, width, '&unreadable=1')
    const failure = page.getByRole('alert').filter({ hasText: 'Não foi possível ler as escolhas por fase' })
    await expect(failure).toBeVisible()
    await expect(page.getByText('Escolhas indisponíveis')).toBeVisible()
    // What is shown would be a guess, so nothing can be changed.
    for (const stage of ['discovery', 'spec', 'plan', 'code', 'eval', 'prs']) await expect(phases.getByTestId(`picker-phase-executor-${stage}`).getByRole('button').first()).toBeDisabled()

    // The failure is not inside the card: closing the card does not hide it.
    const card = page.locator('.phase-executors')
    await card.locator('> summary').click()
    await expect(card).not.toHaveAttribute('open', '')
    await expect(failure).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
    const box = await failure.boundingBox()
    expect(box && box.x >= 0 && box.x + box.width <= width).toBe(true)
    await page.screenshot({ path: info.outputPath(`phase-executors-unreadable-${width}.png`), fullPage: true })

    // Trying again reads them, and keeps the card the way the person left it.
    await failure.getByRole('button', { name: 'Tentar de novo' }).click()
    await expect(failure).toHaveCount(0)
    await expect(page.getByText('Todas seguem o padrão das Configurações')).toBeVisible()
    await expect(card).not.toHaveAttribute('open', '')
    await card.locator('> summary').click()
    await expect(phases.getByTestId('picker-phase-executor-spec').getByRole('button').first()).toBeEnabled()
  })
}
