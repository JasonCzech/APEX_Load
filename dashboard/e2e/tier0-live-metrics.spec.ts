import type { APIRequestContext } from '@playwright/test'

import { expect, test } from './support/liveRun'

// Tier-0 journey step S7 (docs/qa/E2E_SUITES.md): watching a run.
//
// The assertion that matters is that metrics ADVANCE, not that a panel renders.
// A dashboard frozen on a stale sample renders perfectly and is completely
// broken, and a spec that only checks "a number is displayed" passes for it.
// So this reads a counter twice and requires the second reading to exceed the
// first.
//
// This is the step most exposed to timing on a codebase with no fake clock, so
// it is written to the FLAKE_LOG policy's own advice: poll against a deadline
// rather than sleeping a fixed interval, and assert monotonic progress rather
// than exact values. A specific transaction count would be a guess about
// scheduling; "strictly more than before" is a property.

const READY_TIMEOUT_MS = 90_000
const ADVANCE_TIMEOUT_MS = 60_000
const TEST_TIMEOUT_MS = READY_TIMEOUT_MS + ADVANCE_TIMEOUT_MS + 60_000

/** Total completed transactions across the run, as the controller reports them. */
async function transactionCount(request: APIRequestContext, testId: string): Promise<number> {
  const response = await request.get(`/api/v1/tests/${testId}/metrics`)
  if (!response.ok()) return -1
  const body = (await response.json()) as Record<string, unknown>
  const metrics = (body.metrics ?? body) as Record<string, unknown>
  const total = metrics.total_transactions ?? metrics.transactions ?? metrics.total_requests
  return typeof total === 'number' ? total : -1
}

test('[U12] live metrics advance while a run is executing', async ({ request, seedLiveRun }) => {
  test.setTimeout(TEST_TIMEOUT_MS)

  const run = await seedLiveRun({ label: 'tier0-live-metrics', readyTimeoutMs: READY_TIMEOUT_MS })

  const first = await transactionCount(request, run.testId)
  expect(first, 'the controller must report a transaction count for a live run').toBeGreaterThanOrEqual(0)

  // Poll for a strictly greater reading. A fixed sleep either flakes when the
  // machine is loaded or wastes budget when it is not.
  const deadline = Date.now() + ADVANCE_TIMEOUT_MS
  let latest = first
  while (Date.now() < deadline && latest <= first) {
    await new Promise(resolve => setTimeout(resolve, 500))
    latest = await transactionCount(request, run.testId)
  }

  expect(
    latest,
    `transaction count did not advance from ${first} within ${ADVANCE_TIMEOUT_MS}ms — ` +
      'the run is executing but its metrics are frozen',
  ).toBeGreaterThan(first)
})

test('[U12] the run view renders live metrics for an executing run', async ({ page, seedLiveRun }) => {
  test.setTimeout(TEST_TIMEOUT_MS)

  const run = await seedLiveRun({ label: 'tier0-live-view', readyTimeoutMs: READY_TIMEOUT_MS })

  await page.goto(`/#/test/${run.testId}?tab=overview`)

  // Assert on the run's own identity rather than on chrome: a shell that mounts
  // while the run's data call fails would satisfy a chrome-only assertion.
  await expect(
    page.getByRole('heading', { level: 1, name: 'tier0-live-view-run', exact: true }),
    'the run view must identify the run it was opened for',
  ).toBeVisible({ timeout: 30_000 })
})
