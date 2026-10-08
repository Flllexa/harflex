import { expect, test, type Page } from '@playwright/test'
import { undersizedTargets } from './targets'

const root = '/synthetic/workspace/api-faturas'
const relatorios = '/synthetic/worktrees/relatorios'

async function openProject(page: Page, width: number, path = root, query = '') {
  await page.setViewportSize({ width, height: 900 })
  await page.goto(`/e2e/fixture.html?scenario=worktrees${query}`)
  await page.getByLabel('Caminho da pasta').fill(path)
  await page.getByRole('button', { name: 'Abrir projeto' }).click()
  await expect(page.getByRole('heading', { level: 1, name: path.split('/').pop()! })).toBeVisible()
}

async function goTo(page: Page, width: number, destination: string) {
  if (width < 768) await page.getByRole('button', { name: 'Abrir navegação' }).click()
  await page.getByRole('navigation', { name: 'Navegação principal' }).getByRole('button', { name: destination }).click()
}

async function openWorktrees(page: Page, width: number, path = root, query = '') {
  await openProject(page, width, path, query)
  await goTo(page, width, 'Worktrees')
  await expect(page.getByRole('heading', { level: 2, name: 'Worktrees' })).toBeVisible()
  await expect(page.getByText('feature/exportacao', { exact: true })).toBeVisible()
}

const row = (page: Page, branch: string) => page.getByRole('list', { name: 'Worktrees do repositório' }).locator(':scope > li').filter({ has: page.locator(`strong[title="${branch}"]`) })
const noOverflow = (page: Page, width: number) => page.evaluate(() => document.documentElement.scrollWidth).then(scroll => expect(scroll).toBe(width))

for (const width of [320, 768, 900, 1024, 1440]) {
  test(`the Worktrees page judges every worktree and fits the window at ${width}px`, async ({ page }, info) => {
    await openWorktrees(page, width)
    await expect(page.getByRole('list', { name: 'Worktrees do repositório' }).locator(':scope > li')).toHaveCount(7)
    await expect(page.getByRole('status').filter({ hasText: 'worktrees além do principal' })).toHaveText('6 worktrees além do principal · 2 podem ser excluídos · base main')

    await expect(row(page, 'main')).toContainText('Worktree principal')
    await expect(row(page, 'feature/exportacao')).toContainText('Pode excluir')
    await expect(row(page, 'feature/cache')).toContainText('Pode excluir')
    for (const held of ['feature/relatorios', 'feature/travado', 'feature/sumiu', 'HEAD destacado em 4d5e6f7a']) await expect(row(page, held)).toContainText('Não pode excluir')
    await expect(row(page, 'feature/relatorios')).toContainText('3 alterações não salvas (arquivos editados ou novos).')
    await expect(row(page, 'feature/relatorios')).toContainText('2 commits só desta branch, ainda não mesclados em main.')
    await expect(row(page, 'feature/travado')).toContainText('Está travado (em uso no CI)')
    await expect(row(page, 'feature/sumiu')).toContainText('A pasta não existe mais')

    // Deleting is offered only where it is safe; the assistant only where it can help.
    await expect(page.getByRole('button', { name: 'Excluir…' })).toHaveCount(2)
    await expect(page.getByRole('button', { name: 'Salvar e mesclar com a IA…' })).toHaveCount(2)
    await expect(page.getByRole('button', { name: 'Limpar registro' })).toHaveCount(1)
    await expect(row(page, 'main').getByRole('button')).toHaveCount(0)

    await noOverflow(page, width)
    expect(await undersizedTargets(page)).toEqual([])
    // A branch name is never chopped mid-word, whatever room the activity pane leaves the page.
    const broken = await page.locator('.worktree-title strong').evaluateAll(names => names.filter(name => {
      const title = name.getAttribute('title') ?? ''
      return !title.includes(' ') && name.getBoundingClientRect().height > 1.7 * parseFloat(getComputedStyle(name).lineHeight || '20')
    }).map(name => name.textContent))
    expect(broken).toEqual([])
    // Every action stays reachable inside the window, even on the narrowest one.
    const outside = await page.locator('.worktree-actions button').evaluateAll(buttons => buttons.filter(button => {
      const box = button.getBoundingClientRect()
      return box.left < 0 || box.right > window.innerWidth
    }).map(button => button.textContent))
    expect(outside).toEqual([])
    await page.screenshot({ path: info.outputPath(`worktrees-${width}.png`), fullPage: true })
  })
}

for (const width of [768, 1024, 1440]) {
  test(`no interactive control of the Worktrees page sits in the window-drag strip at ${width}px`, async ({ page }) => {
    await openWorktrees(page, width)
    await page.evaluate(() => { document.documentElement.dataset.platform = 'mac' })
    const inside = await page.locator('button, a[href], input, select, textarea, summary, [role="button"], [role="tab"], [tabindex]:not([tabindex="-1"])').evaluateAll(elements => elements.filter(element => {
      const box = element.getBoundingClientRect()
      return box.width > 0 && box.height > 0 && box.top < 50 && !element.closest('.skip-link')
    }).map(element => `${element.tagName.toLowerCase()}:${(element.getAttribute('aria-label') || element.textContent || '').trim().slice(0, 30)}`))
    expect(inside).toEqual([])
  })
}

for (const width of [320, 1440]) {
  test(`deletes a safe worktree and its merged branch after one confirmation at ${width}px`, async ({ page }, info) => {
    await openWorktrees(page, width)
    await row(page, 'feature/exportacao').getByRole('button', { name: 'Excluir…' }).click()
    const dialog = page.getByRole('dialog', { name: 'Excluir o worktree feature/exportacao?' })
    await expect(dialog).toBeVisible()
    await expect(dialog.getByRole('checkbox', { name: /Apagar também a branch feature\/exportacao/ })).toBeChecked()
    const box = await dialog.boundingBox()
    expect(box!.x).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width).toBeLessThanOrEqual(width)
    expect(await undersizedTargets(page)).toEqual([])
    await noOverflow(page, width)
    await page.screenshot({ path: info.outputPath(`worktree-delete-${width}.png`) })
    await dialog.getByRole('button', { name: 'Excluir worktree' }).click()
    await expect(dialog).toHaveCount(0)
    await expect(page.getByText('Worktree feature/exportacao excluído e a branch feature/exportacao também.')).toBeVisible()
    await expect(row(page, 'feature/exportacao')).toHaveCount(0)
    await expect(page.getByRole('status').filter({ hasText: 'worktrees além do principal' })).toContainText('5 worktrees além do principal · 1 pode ser excluído')
  })
}

test('asks for an explicit acknowledgement before erasing files Git does not hold', async ({ page }) => {
  await openWorktrees(page, 1440)
  await expect(row(page, 'feature/cache')).toContainText('Ao excluir, apaga 5 itens ignorados pelo Git: node_modules/, .env.local, dist/ e mais 2')
  await row(page, 'feature/cache').getByRole('button', { name: 'Excluir…' }).click()
  const dialog = page.getByRole('dialog', { name: 'Excluir o worktree feature/cache?' })
  await expect(dialog.getByRole('alert')).toContainText('Estes itens não estão no Git e serão apagados para sempre')
  const confirm = dialog.getByRole('button', { name: 'Excluir worktree' })
  await expect(confirm).toBeDisabled()
  await dialog.getByRole('checkbox', { name: 'Entendo que serão apagados.' }).check()
  await expect(confirm).toBeEnabled()
  await confirm.click()
  await expect(row(page, 'feature/cache')).toHaveCount(0)
})

test('cancelling the confirmation, or pressing Escape, deletes nothing', async ({ page }) => {
  await openWorktrees(page, 1440)
  await row(page, 'feature/exportacao').getByRole('button', { name: 'Excluir…' }).click()
  await page.getByRole('button', { name: 'Cancelar' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await row(page, 'feature/exportacao').getByRole('button', { name: 'Excluir…' }).click()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(row(page, 'feature/exportacao')).toHaveCount(1)
})

test('cleans the record of a folder that no longer exists', async ({ page }) => {
  await openWorktrees(page, 1440)
  await row(page, 'feature/sumiu').getByRole('button', { name: 'Limpar registro' }).click()
  await expect(page.getByText('Registros de pastas ausentes foram limpos.')).toBeVisible()
  await expect(row(page, 'feature/sumiu')).toHaveCount(0)
})

for (const width of [320, 1440]) {
  test(`the assistant saves and merges a held worktree, then the app deletes it at ${width}px`, async ({ page }, info) => {
    await openWorktrees(page, width)
    await row(page, 'feature/relatorios').getByRole('button', { name: 'Salvar e mesclar com a IA…' }).click()
    const dialog = page.getByRole('dialog', { name: 'Salvar e mesclar com a IA' })
    await expect(dialog).toBeVisible()
    await expect(dialog.getByText(/no projeto principal/)).toBeVisible()
    await expect(dialog.getByRole('checkbox', { name: /Excluir o worktree quando tudo estiver salvo e mesclado/ })).toBeChecked()
    const box = await dialog.boundingBox()
    expect(box!.x).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width).toBeLessThanOrEqual(width)
    expect(await undersizedTargets(page)).toEqual([])
    await page.screenshot({ path: info.outputPath(`worktree-save-dialog-${width}.png`) })
    await dialog.getByRole('button', { name: 'Começar' }).click()

    const notice = page.getByRole('status', { name: 'Salvamento do worktree' })
    await expect(notice).toContainText('Worktree feature/relatorios excluído. Tudo estava salvo e mesclado em main; a branch feature/relatorios também foi apagada.')
    await expect(page.getByText('Commitei o que estava pendente e mesclei em main.')).toBeVisible()
    await noOverflow(page, width)
    expect(await undersizedTargets(page)).toEqual([])
    await page.screenshot({ path: info.outputPath(`worktree-saved-${width}.png`), fullPage: true })

    await goTo(page, width, 'Worktrees')
    await expect(page.getByRole('heading', { level: 2, name: 'Worktrees' })).toBeVisible()
    await expect(row(page, 'feature/relatorios')).toHaveCount(0)
    await expect(row(page, 'feature/exportacao')).toHaveCount(1)
  })
}

test('judges again after the assistant and deletes nothing when work is still pending', async ({ page }, info) => {
  await openWorktrees(page, 1440, root, '&assistant=stalls')
  await row(page, 'feature/relatorios').getByRole('button', { name: 'Salvar e mesclar com a IA…' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Começar' }).click()
  const alert = page.getByRole('alert', { name: 'Salvamento do worktree' })
  await expect(alert).toContainText('Ainda não dá para excluir feature/relatorios: 3 alterações não salvas (arquivos editados ou novos).')
  await expect(alert).toContainText('Nada foi excluído.')
  await page.screenshot({ path: info.outputPath('worktree-save-pending.png'), fullPage: true })
  await alert.getByRole('button', { name: 'Abrir Worktrees' }).click()
  await expect(row(page, 'feature/relatorios')).toContainText('Não pode excluir')
})

test('keeps the worktree when the user chose not to delete it after saving', async ({ page }) => {
  await openWorktrees(page, 1440)
  await row(page, 'feature/relatorios').getByRole('button', { name: 'Salvar e mesclar com a IA…' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('checkbox', { name: /Excluir o worktree quando tudo estiver salvo/ }).uncheck()
  await dialog.getByRole('button', { name: 'Começar' }).click()
  const notice = page.getByRole('status', { name: 'Salvamento do worktree' })
  await expect(notice).toContainText('Tudo salvo e mesclado em main. feature/relatorios já pode ser excluído na página Worktrees.')
  await notice.getByRole('button', { name: 'Abrir Worktrees' }).click()
  await expect(row(page, 'feature/relatorios')).toContainText('Pode excluir')
})

test('says so when no AI provider is available', async ({ page }) => {
  await openWorktrees(page, 1440, root, '&backends=none')
  await row(page, 'feature/relatorios').getByRole('button', { name: 'Salvar e mesclar com a IA…' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('alert')).toContainText('Nenhum provedor de IA disponível')
  await expect(dialog.getByRole('button', { name: 'Começar' })).toBeDisabled()
  await expect(dialog.getByRole('button', { name: 'Configurar provedor' })).toBeVisible()
})

for (const width of [320, 768, 1440]) {
  test(`the task header carries the worktree control on every destination at ${width}px`, async ({ page }, info) => {
    await openProject(page, width, relatorios)
    const region = page.getByRole('region', { name: 'Worktree da tarefa' })
    await expect(region).toBeVisible()
    await expect(region).toContainText('feature/relatorios')
    await expect(region).toContainText('3 alterações não salvas · 2 commits à frente de main')
    await expect(region).toContainText('Não pode ser excluído: 3 alterações não salvas (arquivos editados ou novos).')
    await expect(region.getByRole('button', { name: 'Salvar com a IA…' })).toBeVisible()
    await expect(region.getByRole('button', { name: 'Gerenciar worktrees' })).toBeVisible()
    await noOverflow(page, width)
    expect(await undersizedTargets(page)).toEqual([])
    await page.screenshot({ path: info.outputPath(`worktree-bar-${width}.png`) })

    // The same control follows the user to another destination.
    await goTo(page, width, 'Logs')
    await expect(page.getByRole('heading', { name: 'Logs e auditoria' })).toBeVisible()
    await expect(page.getByRole('region', { name: 'Worktree da tarefa' })).toBeVisible()

    await region.getByRole('button', { name: 'Gerenciar worktrees' }).click()
    await expect(page.getByRole('heading', { level: 2, name: 'Worktrees' })).toBeVisible()
    // On its own page the shortcut to itself is gone.
    await expect(page.getByRole('region', { name: 'Worktree da tarefa' }).getByRole('button', { name: 'Gerenciar worktrees' })).toHaveCount(0)
    await expect(row(page, 'feature/relatorios')).toContainText('Projeto aberto')
    await expect(row(page, 'feature/relatorios')).toContainText('É o projeto aberto agora.')
  })
}

test('the task worktree is saved from its own header, which moves the project to the main checkout', async ({ page }) => {
  await openProject(page, 1440, relatorios)
  await page.getByRole('region', { name: 'Worktree da tarefa' }).getByRole('button', { name: 'Salvar com a IA…' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Começar' }).click()
  await expect(page.getByRole('status', { name: 'Salvamento do worktree' })).toContainText('Worktree feature/relatorios excluído.')
  await expect(page.getByRole('heading', { level: 1, name: 'api-faturas' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Worktree da tarefa' })).toContainText('main')
})

for (const width of [320, 1440]) {
  test(`keeps the save notice in view in the Casual conversation at ${width}px`, async ({ page }, info) => {
    await page.addInitScript(() => localStorage.setItem('harflex:view-mode', 'casual'))
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/e2e/fixture.html?scenario=worktrees')
    await page.getByLabel('Caminho da pasta').fill(root)
    await page.getByRole('button', { name: 'Abrir projeto' }).click()
    if (width < 768) await page.getByRole('button', { name: 'Abrir histórico de chats' }).click()
    await page.getByText('Ferramentas', { exact: true }).click()
    await page.getByRole('navigation', { name: 'Ferramentas' }).getByRole('button', { name: 'Worktrees' }).click()
    await expect(page.getByRole('heading', { level: 2, name: 'Worktrees' })).toBeVisible()
    await row(page, 'feature/relatorios').getByRole('button', { name: 'Salvar e mesclar com a IA…' }).click()
    await page.getByRole('dialog').getByRole('button', { name: 'Começar' }).click()

    // The project header is hidden in this conversation; the notice still says what became of the save.
    const notice = page.getByRole('status', { name: 'Salvamento do worktree' })
    await expect(notice).toContainText('Worktree feature/relatorios excluído.')
    await expect(page.getByRole('region', { name: 'Worktree da tarefa' })).toHaveCount(0)
    await expect(page.getByText('Commitei o que estava pendente e mesclei em main.')).toBeVisible()
    await noOverflow(page, width)
    expect(await undersizedTargets(page)).toEqual([])
    await page.screenshot({ path: info.outputPath(`worktree-casual-${width}.png`), fullPage: true })
  })
}
