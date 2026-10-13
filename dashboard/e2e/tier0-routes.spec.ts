import { expect, test } from '@playwright/test'
import { createWorkspace, scopeQuery } from './support/workspace'

// A visible body cannot prove a lazy page loaded. Require rendered page content
// and a settled loading boundary, with page-specific cases covering interactions.
const ROUTES: Array<{ hash: string; name: string }> = [
  { hash: '#/dashboard', name: 'Dashboard' },
  { hash: '#/repository', name: 'Repository' },
  { hash: '#/tests?view=library', name: 'Scenarios (library)' },
  { hash: '#/tests?view=builder', name: 'Scenario builder' },
  { hash: '#/tests?view=plans', name: 'Execution plans' },
  { hash: '#/scripts?mode=list', name: 'Script library' },
  { hash: '#/scripts?mode=create', name: 'Script builder' },
  { hash: '#/shared-assets', name: 'Shared assets' },
  { hash: '#/shared-assets?section=secrets', name: 'Shared assets (secrets)' },
  { hash: '#/results', name: 'Results' },
  { hash: '#/monitoring?category=overview', name: 'Monitoring' },
  { hash: '#/fleet', name: 'Fleet' },
  { hash: '#/schedules', name: 'Schedules' },
  { hash: '#/command-center', name: 'Command Center' },
  { hash: '#/access', name: 'Access' },
  { hash: '#/settings', name: 'Settings' },
  { hash: '#/docs', name: 'API guide' },
  { hash: '#/repository-guide', name: 'Repository guide' },
]

test('[U22] every top-level view renders under a real workspace scope', async ({ page, request }) => {
  test.setTimeout(120_000)
  const workspace = await createWorkspace(request)
  const scope = scopeQuery(workspace)

  const failures: string[] = []

  for (const route of ROUTES) {
    const pageErrors: Error[] = []
    const onPageError = (error: Error) => pageErrors.push(error)
    page.on('pageerror', onPageError)

    const separator = route.hash.includes('?') ? '&' : '?'
    await page.goto(`/${route.hash}${separator}${scope}`)

    try {
      await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
      await expect(page.getByText('Loading page…', { exact: true })).toBeHidden()
      await expect(page.locator('main').locator('.page, .command-center-page, .dashboard-workspace').first()).toBeVisible()
      await expect(page.locator('main').getByText('This dashboard page could not be loaded.', { exact: true })).toBeHidden()
      await expect.poll(() => page.locator('main').locator('table, section, [role="tabpanel"], .page > :not(.page-header)').count()).toBeGreaterThan(0)
      if (pageErrors.length > 0) {
        failures.push(`${route.name} (${route.hash}): ${pageErrors.map(e => e.message).join('; ')}`)
      }
    } catch (error) {
      failures.push(`${route.name} (${route.hash}): ${(error as Error).message}`)
    } finally {
      page.off('pageerror', onPageError)
    }
  }

  // Report every broken view at once. Failing on the first would hide the rest
  // and turn a systemic break into a one-view-at-a-time investigation.
  expect(failures, `views that failed to render:\n${failures.join('\n')}`).toEqual([])
})
