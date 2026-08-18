import { expect, test } from '@playwright/test'
import { createWorkspace, scopeQuery } from './support/workspace'

// Breadth guard for the Tier-0 journey: every top-level view a user can reach
// must render under a real workspace scope without throwing.
//
// The dashboard lazy-loads each page behind a Suspense boundary, so a module
// that throws on load degrades to a blank region rather than a hard failure —
// which is exactly the kind of break that reaches a user without any test
// noticing. Asserting "the app shell is still mounted AND nothing threw" is
// what distinguishes a rendered page from an empty one.
//
// This deliberately does not assert page-specific content. Per-view assertions
// belong in the per-step specs; this one exists so that a view which crashes
// outright can never pass unnoticed while those are still being written.

const ROUTES: Array<{ hash: string; name: string }> = [
  { hash: '#/dashboard', name: 'Dashboard' },
  { hash: '#/repository', name: 'Repository' },
  { hash: '#/tests?view=library', name: 'Scenarios (library)' },
  { hash: '#/tests?view=builder', name: 'Scenario builder' },
  { hash: '#/tests?view=plans', name: 'Execution plans' },
  { hash: '#/scripts?mode=list', name: 'Script library' },
  { hash: '#/scripts?mode=create', name: 'Script builder' },
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

test('every top-level view renders under a real workspace scope', async ({ page, request }) => {
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
      // The shell must still be mounted. A page module that throws inside its
      // Suspense boundary leaves the shell up but the region empty, which the
      // pageerror check below is what actually catches.
      await expect(page.locator('body')).toBeVisible({ timeout: 15_000 })
      // Give the lazy chunk a moment to evaluate; a throw during module
      // evaluation surfaces as a pageerror rather than a failed assertion.
      await page.waitForTimeout(400)
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
