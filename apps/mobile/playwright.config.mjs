import { accessSync, constants } from 'node:fs'
import { defineConfig } from '../admin/node_modules/@playwright/test/index.mjs'

const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH
if (executablePath) accessSync(executablePath, constants.X_OK)

export default defineConfig({
  testDir: './acceptance',
  testMatch: '**/*.spec.mjs',
  outputDir: './acceptance/test-results',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  forbidOnly: Boolean(process.env.CI),
  timeout: 30_000,
  expect: { timeout: 5_000 },
  reporter: 'list',
  use: {
    baseURL: 'http://127.0.0.1:4176',
    browserName: 'chromium',
    launchOptions: executablePath ? { executablePath } : {},
    viewport: { width: 430, height: 932 },
    serviceWorkers: 'block',
    trace: 'off', screenshot: 'off', video: 'off',
  },
  webServer: {
    command: 'node ../admin/node_modules/vite/bin/vite.js --config vite.acceptance.config.mjs --host 127.0.0.1 --port 4176 --strictPort',
    url: 'http://127.0.0.1:4176',
    reuseExistingServer: false,
    timeout: 30_000,
  },
})