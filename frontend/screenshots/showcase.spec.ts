import { expect, test, type Page } from '@playwright/test'

// Each picture opens the demonstration project at one stage and saves the screen to docs/screenshots.
const out = (name: string) => `../docs/screenshots/${name}.png`

async function openProject(page: Page, view: string) {
  await page.goto(`/e2e/fixture.html?scenario=showcase&view=${view}`)
  await page.getByLabel('Caminho da pasta').fill('/Users/demo/Projects/todo-app')
  await page.getByRole('button', { name: 'Abrir projeto' }).click()
  await expect(page.getByRole('heading', { name: 'Sessões recentes' })).toBeVisible()
  const close = page.getByRole('button', { name: 'Fechar atividade' })
  if (await close.isVisible()) await close.click()
}

async function openPipelines(page: Page) {
  await page.getByRole('navigation', { name: 'Navegação principal' }).getByRole('button', { name: 'Pipelines' }).click()
  await expect(page.getByRole('heading', { name: 'Pipelines SDD' })).toBeVisible()
}

// The stage's bench, scrolled so its header (or the part worth showing) sits under the stage bar.
async function shootBench(page: Page, name: string, bench: string, focus?: string) {
  const section = page.getByRole('region', { name: bench })
  await expect(section).toBeVisible()
  const target = focus ? section.locator(focus).first() : section
  await expect(target).toBeVisible()
  await target.evaluate(element => element.scrollIntoView({ block: 'start' }))
  await page.waitForTimeout(400)
  await page.screenshot({ path: out(name) })
}

test('chat: an agent creates the SDD work from a conversation', async ({ page }) => {
  await openProject(page, 'chat')
  await page.getByRole('button', { name: 'Casual', exact: true }).click()
  await page.getByRole('button', { name: /Criar uma lista de tarefas/ }).first().click()
  await expect(page.getByText('Pronto! Criei o trabalho')).toBeVisible()
  await page.waitForTimeout(400)
  await page.screenshot({ path: out('01-chat') })
})

test('design: Discovery, SPEC and Plan written with the AI', async ({ page }) => {
  await page.goto('/e2e/fixture.html?scenario=conversational-design&demo=todo')
  await page.getByLabel('Caminho da pasta').fill('/Users/demo/Projects/todo-app')
  await page.getByRole('button', { name: 'Abrir projeto' }).click()
  const close = page.getByRole('button', { name: 'Fechar atividade' })
  if (await close.isVisible()) await close.click()
  await openPipelines(page)
  await page.getByLabel('Discovery', { exact: true }).fill('Quero uma lista de tarefas em HTML que guarde tudo no navegador: adicionar, concluir e excluir tarefas, sem perder nada ao recarregar a página.')
  await page.getByRole('button', { name: 'Criar pipeline' }).click()
  await expect(page.getByRole('article', { name: 'Documento SPEC versão 1' })).toBeVisible()
  await page.locator('.pipeline-design-studio').first().evaluate(element => element.scrollIntoView({ block: 'start' }))
  await page.waitForTimeout(400)
  await page.screenshot({ path: out('02-design') })
})

test('code: the change file by file', async ({ page }) => {
  await openProject(page, 'code')
  await openPipelines(page)
  await shootBench(page, '03-code', 'Bancada Code')
})

test('qa running: each command as it happens', async ({ page }) => {
  await openProject(page, 'qa-running')
  await openPipelines(page)
  await expect(page.getByText('Agora:')).toBeVisible()
  await shootBench(page, '04-qa-running', 'Bancada QA', '.qa-lab-live')
})

test('qa report: failures and improvements to fix', async ({ page }) => {
  await openProject(page, 'qa-report')
  await openPipelines(page)
  await expect(page.getByText('Encontrou problemas', { exact: true })).toBeVisible()
  await shootBench(page, '05-qa-report', 'Bancada QA', '.qa-summary')
})

test('prs: the pull request and the review watch', async ({ page }) => {
  await openProject(page, 'prs')
  await openPipelines(page)
  await shootBench(page, '06-prs', 'Bancada PRs', '.pr-card')
})
