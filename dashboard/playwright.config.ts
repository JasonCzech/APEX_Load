import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  testMatch: process.env.APEXLOAD_E2E_PROFILE === 'local-auth'
    ? ['**/tier0-local-auth.spec.ts', '**/workflow-local-auth.spec.ts']
    : '**/*.spec.ts',
  testIgnore: process.env.APEXLOAD_E2E_PROFILE === 'local-auth' ? [] : ['**/*local-auth.spec.ts'],
  fullyParallel: false,
  forbidOnly: Boolean(process.env.CI),
  // A retry hides the first signal the flake policy requires us to log.
  retries: 0,
  workers: 1,
  reporter: process.env.CI
    ? [['line'], ['junit', { outputFile: `test-results/${process.env.APEXLOAD_E2E_PROFILE || 'dev-admin'}/playwright-junit.xml` }]]
    : 'list',
  outputDir: `test-results/${process.env.APEXLOAD_E2E_PROFILE || 'dev-admin'}`,
  expect: { timeout: 15_000 },
  use: {
    actionTimeout: 15_000,
    navigationTimeout: 30_000,
    screenshot: 'only-on-failure',
    baseURL: process.env.APEXLOAD_E2E_BASE_URL || 'http://127.0.0.1:18081',
    browserName: 'chromium',
    // The dashboard formats numbers with `toLocaleString(undefined, …)`, i.e.
    // the browser's own locale, and Chromium takes that from the host's UI
    // language. Specs that assert on rendered numbers would otherwise pass on a
    // hosted en-US runner and fail on a German or French desktop, where a
    // healthy 1234.5 renders as "1.234,5". Pin it so the suite is hermetic.
    locale: 'en-US',
    trace: 'retain-on-failure',
  },
})
