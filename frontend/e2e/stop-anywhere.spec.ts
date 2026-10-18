import { expect, test } from './locale'

// Whatever is running can be stopped from the Activity panel that follows it: the button sits in its heading.
test('the Activity panel stops the run it follows', async ({ page }, info) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.addInitScript(() => localStorage.setItem('harflex:view-mode', 'casual'))
  await page.goto('/e2e/fixture.html?scenario=activity-live')
  await page.getByLabel('Caminho da pasta').fill('/Users/dev/todo-app')
  await page.getByRole('button', { name: 'Abrir projeto' }).click()
  await page.getByRole('button', { name: /Implementar um arquivo de teste/ }).click()
  const activity = page.getByRole('complementary', { name: 'Atividade do trabalho' })
  if (!await activity.isVisible()) await page.getByRole('button', { name: 'Painel lateral', exact: true }).click()
  const stop = activity.getByRole('button', { name: 'Parar' })
  await expect(stop).toBeVisible()
  await page.screenshot({ path: info.outputPath('activity-stop.png') })
  // Its click is the chat box's own stop (ActivityPanel and Workbench unit tests); this scenario never confirms a cancel.
})
