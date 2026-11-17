import { accessSync, constants } from 'node:fs'
import { defineConfig } from '@playwright/test'

const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH
if (executablePath) accessSync(executablePath, constants.X_OK)

export default defineConfig({
  testDir: './acceptance',
  testMatch: '**/*.spec.mjs',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  forbidOnly: Boolean(process.env.CI),
  timeout: 30_000,
  expect: { timeout: 5_000 },
  reporter: 'list',
  outputDir: './test-results',
  use: {
    baseURL: 'http://127.0.0.1:4175',
    browserName: 'chromium',
    launchOptions: executablePath ? { executablePath } : {},
    viewport: { width: 1280, height: 900 },
    serviceWorkers: 'block',
    trace: 'off',
    screenshot: 'off',
    video: 'off',
  },
  webServer: {
    command: 'npm run build && npm run preview -- --host 127.0.0.1 --port 4175 --strictPort',
    url: 'http://127.0.0.1:4175',
    reuseExistingServer: false,
    timeout: 60_000,
    // Never bake a developer's real gateway into the acceptance artifact.
    env: { VITE_API_URL: '' },
  },
})