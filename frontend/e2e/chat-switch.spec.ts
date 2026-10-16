import { expect, test } from './locale'

// Switching chats must never blank the screen: the list stays, and the open conversation stays (dimmed) until the
// next one's history is in. A slow backend (latency) is where a flash would show.
for (const latency of [0, 350]) {
  test(`switching chats shows no blank list or empty conversation (latency ${latency} ms)`, async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.addInitScript(() => localStorage.setItem('harflex:view-mode', 'casual'))
    await page.goto(`/e2e/fixture.html?scenario=casual-history&latency=${latency}`)
    await page.getByLabel('Caminho da pasta').fill('/Users/dev/todo-app')
    await page.getByRole('button', { name: 'Abrir projeto' }).click()
    const list = page.getByRole('list', { name: 'Histórico de chats' })
    await list.getByRole('button', { name: /Criar uma lista TODO/ }).click()
    await expect(page.getByRole('list', { name: 'Conversa' })).toContainText('Vou organizar')
    await page.evaluate(() => {
      const seen = new Set<string>()
      Object.assign(window, { __flashes: seen })
      const visible = (selector: string) => [...document.querySelectorAll(selector)].some(node => !node.closest('.visually-hidden') && (node as HTMLElement).offsetParent !== null)
      new MutationObserver(() => {
        const text = [...document.querySelectorAll('.casual-history-state, .empty-state, .casual-history-notice')].filter(node => (node as HTMLElement).offsetParent !== null).map(node => node.textContent ?? '').join(' ')
        if (text.includes('Carregando chats')) seen.add('list replaced by loading')
        if (/Nova conversa|Nenhuma conversa iniciada/.test(text)) seen.add('empty conversation')
        if (!visible('[aria-label="Histórico de chats"]')) seen.add('list missing')
        if (text.includes('Sessão em execução')) seen.add('busy notice')
      }).observe(document.body, { subtree: true, childList: true, attributes: true, characterData: true })
    })
    await list.getByRole('button', { name: /Corrigir o bloqueio/ }).click()
    // The clicked chat is marked at once.
    await expect(list.getByRole('button', { name: /Corrigir o bloqueio/ })).toHaveAttribute('aria-current', 'true')
    await expect(page.getByRole('list', { name: 'Conversa' })).toContainText('Corrigir o bloqueio')
    await expect(page.locator('.conversation-log')).not.toHaveClass(/is-switching/)
    expect(await page.evaluate(() => [...(window as unknown as { __flashes: Set<string> }).__flashes])).toEqual([])
  })
}
