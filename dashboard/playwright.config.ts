import { defineConfig } from '@playwright/test'

// Isolate artifacts when multiple local validation jobs share this checkout.
const outputRoot = process.env.APEXLOAD_E2E_OUTPUT_ROOT || 'test-results'

export default defineConfig({
  testDir: './e2e',
  testMatch: process.env.APEXLOAD_E2E_PROFILE === 'local-auth'
    ? ['**/workflow-archive-references.spec.ts', '**/workflow-object-activity.spec.ts', '**/workflow-browser-resource-evidence.spec.ts', '**/workflow-analysis-preparation.spec.ts', '**/workflow-protocol-diagnostics.spec.ts', '**/workflow-crosscut.spec.ts', '**/workflow-theme-controls.spec.ts', '**/workflow-application-secrets.spec.ts', '**/workflow-scenario-composition.spec.ts', '**/workflow-scenario-data.spec.ts', '**/workflow-local-archive-failure.spec.ts', '**/workflow-baseline-comparison.spec.ts', '**/workflow-jmeter-fidelity.spec.ts', '**/tier0-bruno-import.spec.ts', '**/workflow-library-assignments.spec.ts', '**/workflow-script-folders.spec.ts', '**/tier0-local-auth.spec.ts', '**/workflow-local-auth.spec.ts', '**/workflow-loadrunner-review.spec.ts', '**/workflow-loadrunner-folder.spec.ts', '**/workflow-loadrunner-collection.spec.ts', '**/workflow-import-source.spec.ts', '**/workflow-help.spec.ts', '**/workflow-monitoring.spec.ts', '**/workflow-dynatrace.spec.ts', '**/workflow-dashboard.spec.ts', '**/workflow-dashboard-analysis.spec.ts', '**/workflow-command-center.spec.ts', '**/workflow-new-test.spec.ts', '**/workflow-script-settings.spec.ts', '**/workflow-script-sidebars.spec.ts', '**/workflow-debugger-experience.spec.ts', '**/workflow-author-experience.spec.ts']
    : '**/*.spec.ts',
  testIgnore: process.env.APEXLOAD_E2E_PROFILE === 'local-auth' ? [] : ['**/*local-auth.spec.ts'],
  fullyParallel: false,
  forbidOnly: Boolean(process.env.CI),
  // A retry hides the first signal the flake policy requires us to log.
  retries: 0,
  workers: 1,
  reporter: process.env.CI
    ? [['line'], ['junit', { outputFile: `${outputRoot}/${process.env.APEXLOAD_E2E_PROFILE || 'dev-admin'}/playwright-junit.xml` }]]
    : 'list',
  outputDir: `${outputRoot}/${process.env.APEXLOAD_E2E_PROFILE || 'dev-admin'}`,
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
