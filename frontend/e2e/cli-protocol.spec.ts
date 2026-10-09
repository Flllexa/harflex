import { expect, test } from './locale'
import codexEvents from '../../internal/externalagent/testdata/codex-events.json' with { type: 'json' }
import opencodeEvents from '../../internal/externalagent/testdata/opencode-events.json' with { type: 'json' }

// The same expected payloads are checked against the Go fake-process recorder.
for (const width of [320, 768, 900, 1440]) for (const [adapter, phase] of [['codex', 'complete'], ['opencode', 'shorter'], ['opencode', 'empty']]) {
  test(`replays normalized ${adapter} ${phase} responses once at ${width}px`, async ({ page }, info) => {
    const fixture = adapter === 'codex' ? codexEvents : phase === 'shorter' ? opencodeEvents.slice(0, 5) : opencodeEvents
    const journal = [
      ['external.run.started', { adapter }],
      ['message.user', { role: 'user', content: 'Pergunta sintética' }],
      ...fixture.map(data => ['external.event', data]),
      ['external.run.completed', { adapter }],
    ].map(([type, data], index) => ({ id: `protocol-${index + 1}`, streamId: 'session-1', sequence: index + 1,
      type, data, createdAt: '2026-09-26T12:00:00Z' }))
    await page.addInitScript(value => sessionStorage.setItem('harflex:synthetic-foundation-journal', JSON.stringify(value)), journal)
    await page.setViewportSize({ width, height: 900 })
    await page.goto('/e2e/fixture.html?scenario=foundation-readonly')
    await page.getByLabel('Caminho da pasta').fill('/synthetic/workspace/protocol')
    await page.getByRole('button', { name: 'Abrir projeto' }).click()
    await page.getByRole('button', { name: 'Abrir histórico' }).click()
    await expect(page.getByRole('list', { name: 'Conversa' }).getByText('Pergunta sintética', { exact: true })).toBeVisible()
    const answers = adapter === 'codex' ? ['Resposta do Codex.'] : phase === 'shorter' ? ['Olá'] : ['Segunda parte.']
    for (const answer of answers) {
      await expect(page.getByText(answer, { exact: true })).toHaveCount(1)
      await expect(page.getByText(answer, { exact: true })).toBeVisible()
    }
    await expect(page.getByText('Olá mundo', { exact: true })).toHaveCount(0)
    if (phase !== 'shorter') await expect(page.getByText('Olá', { exact: true })).toHaveCount(0)
    await expect(page.getByText('RAW_ONLY', { exact: true })).toHaveCount(0)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(width)
    await page.screenshot({ path: info.outputPath(`protocol-${adapter}-${phase}-${width}.png`), fullPage: true })
  })
}
