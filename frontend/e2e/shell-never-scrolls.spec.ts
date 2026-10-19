import { expect, test } from './locale'

// Something inside asking to be shown at the top (an activity pane that opens, a heading in a document) scrolls its
// own area; once that area has gone as far as it can, the browser scrolled the window frame too, and the whole app
// slid up with an empty band under it. The frame never scrolls.
test('the window frame never scrolls when something inside asks to be shown', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/e2e/fixture.html?scenario=pipeline-code&stage=code-review')
  await page.getByLabel('Caminho da pasta').fill('/synthetic/workspace/api-faturas')
  await page.getByRole('button', { name: 'Abrir projeto' }).click()
  await page.getByRole('navigation', { name: 'Navegação principal' }).getByRole('button', { name: 'Pipelines' }).click()
  await expect(page.getByRole('region', { name: 'Teste local' })).toBeVisible()
  const scrolled = await page.evaluate(() => {
    // Something placed below the frame's bottom (a popover, a tooltip) makes the frame taller than the window.
    const shell = document.querySelector('.app-shell') as HTMLElement
    const below = document.createElement('div')
    below.style.cssText = 'position:absolute;top:1300px;left:0;width:10px;height:10px'
    shell.appendChild(below)
    below.scrollIntoView({ block: 'start' })
    document.getElementById('work-area')!.querySelector('h3, h2')?.scrollIntoView({ block: 'start' })
    const moved = [...document.querySelectorAll('*')].filter(element => element.scrollTop > 0).map(element => `${element.tagName}.${element.className}#${element.id}:${element.scrollTop}`)
    return { shell: shell.scrollTop, page: document.scrollingElement?.scrollTop ?? 0, moved }
  })
  // Only the work area scrolls (to show the heading); the page and the frame stay put.
  expect(scrolled.moved.filter(item => !item.startsWith('MAIN.work-area'))).toEqual([])
  expect(scrolled.shell).toBe(0)
  expect(scrolled.page).toBe(0)
})
