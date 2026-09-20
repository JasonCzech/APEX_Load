import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './e2e-recording',
  testMatch: '**/*.spec.ts',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  forbidOnly: true,
  timeout: 180_000,
  expect: { timeout: 15_000 },
  outputDir: process.env.APEXLOAD_RECORDING_E2E_OUTPUT || '../output/playwright/recording/results',
  reporter: [['line'], ['json', { outputFile: `${process.env.APEXLOAD_RECORDING_E2E_OUTPUT || '../output/playwright/recording/results'}/report.json` }]],
  projects: (process.env.APEXLOAD_E2E_PROFILE ? [process.env.APEXLOAD_E2E_PROFILE] : ['dev-admin', 'local-auth']).map(name => ({ name })),
  use: { browserName: 'chromium', locale: 'en-US', viewport: { width: 1600, height: 1050 }, actionTimeout: 15_000, navigationTimeout: 30_000, trace: { mode: 'on', screenshots: false, snapshots: true, sources: true }, screenshot: 'only-on-failure' },
})
