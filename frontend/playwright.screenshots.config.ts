import { defineConfig } from '@playwright/test'

// `make screenshots`: the README pictures, taken from the app with demonstration data (src/test/showcaseFixture.ts).
const port = Number(process.env.HARFLEX_SHOTS_PORT ?? '9446')
const baseURL = `http://127.0.0.1:${port}`

export default defineConfig({
  testDir: './screenshots',
  timeout: 60_000,
  workers: 1,
  reporter: [['list']],
  use: { baseURL, viewport: { width: 1440, height: 900 }, colorScheme: 'dark', deviceScaleFactor: 2 },
  webServer: { command: `npm run dev -- --host 127.0.0.1 --port ${port} --strictPort`, url: baseURL, reuseExistingServer: false },
})
