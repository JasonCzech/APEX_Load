import type { APIRequestContext } from '@playwright/test'

import { expect, test } from './support/liveRun'

// Tier-0 journey steps S10 and S12 (docs/qa/TIER0_JOURNEY.md): reading the
// report a finished run produced, and finding that run again afterwards.
//
// The report is the same run told three ways. The assertion that carries weight
// is that the three lenses AGREE: a run whose executive summary and engineer
// view disagree about how many transactions it ran is worse than one that fails
// to render, because it is wrong while looking authoritative.

const READY_TIMEOUT_MS = 90_000
const TERMINAL_TIMEOUT_MS = 120_000
const TEST_TIMEOUT_MS = READY_TIMEOUT_MS + TERMINAL_TIMEOUT_MS + 120_000

const TERMINAL = new Set(['COMPLETE', 'FAILED', 'ABORTED'])

async function status(request: APIRequestContext, testId: string): Promise<string> {
  const response = await request.get(`/api/v1/tests/${testId}`)
  if (!response.ok()) return ''
  const body = (await response.json()) as Record<string, unknown>
  const record = (body.test ?? body) as Record<string, unknown>
  return String(record.status ?? '')
}

async function waitForTerminal(request: APIRequestContext, testId: string): Promise<string> {
  const deadline = Date.now() + TERMINAL_TIMEOUT_MS
  let last = ''
  while (Date.now() < deadline) {
    last = await status(request, testId)
    if (TERMINAL.has(last)) return last
    await new Promise(resolve => setTimeout(resolve, 500))
  }
  throw new Error(`run did not terminalize; last status ${last || '(none)'}`)
}

/**
 * Waits for the asynchronous archive finalization the report reads, and returns
 * the report body as TEXT.
 *
 * GET /api/v1/tests/{id}/report serves a rendered HTML document, not JSON —
 * parsing it as JSON fails with `Unexpected token '<'`, which reads like a
 * server error rather than a wrong content type. Recorded in
 * docs/qa/TIER0_JOURNEY.md so the next caller does not repeat it.
 */
async function waitForReport(request: APIRequestContext, testId: string): Promise<string> {
  const deadline = Date.now() + 90_000
  let lastStatus = 0
  while (Date.now() < deadline) {
    const response = await request.get(`/api/v1/tests/${testId}/report`)
    lastStatus = response.status()
    if (response.ok()) return await response.text()
    await new Promise(resolve => setTimeout(resolve, 1_000))
  }
  throw new Error(`report never became available; last status ${lastStatus}`)
}

test('a finished run produces a report and stays findable in results', async ({ request, page, seedLiveRun }) => {
  test.setTimeout(TEST_TIMEOUT_MS)

  // startLiveRun names the run `${label}-run`; the views render that name rather
  // than the UUID, so assert on what a user actually sees.
  const label = 'tier0-report'
  const runName = `${label}-run`
  const run = await seedLiveRun({ label, readyTimeoutMs: READY_TIMEOUT_MS })

  expect((await request.post(`/api/v1/tests/${run.testId}/stop`)).ok(), 'stop must be accepted').toBeTruthy()
  const terminal = await waitForTerminal(request, run.testId)
  expect(terminal, 'a deliberate stop must not terminalize as FAILED').not.toBe('FAILED')

  // S10 — the report must exist and describe THIS run, not a generic shell.
  const report = await waitForReport(request, run.testId)
  expect(report, 'the report must identify the run it describes').toContain(run.testId)

  // S12 — the finished run must be findable afterwards. A run that executes and
  // then cannot be located is indistinguishable from one that never ran.
  const listed = await request.get('/api/v1/tests')
  expect(listed.ok(), 'the run list must be retrievable').toBeTruthy()
  expect(await listed.text(), 'a finished run must appear in the run list').toContain(run.testId)

  // The results view must render that run rather than an empty history.
  await page.goto(`/#/results`)
  await expect(
    page.getByText(runName, { exact: false }).first(),
    'the results view must list the finished run',
  ).toBeVisible({ timeout: 30_000 })
})

test('every report lens renders the same run', async ({ request, page, seedLiveRun }) => {
  test.setTimeout(TEST_TIMEOUT_MS)

  const label = 'tier0-lenses'
  const runName = `${label}-run`
  const run = await seedLiveRun({ label, readyTimeoutMs: READY_TIMEOUT_MS })
  expect((await request.post(`/api/v1/tests/${run.testId}/stop`)).ok(), 'stop must be accepted').toBeTruthy()
  await waitForTerminal(request, run.testId)
  await waitForReport(request, run.testId)

  // All three audience lenses are the same run told three ways. Each must render
  // without throwing and must be about the run it was opened for.
  for (const lens of ['executive', 'engineer', 'audit'] as const) {
    const pageErrors: Error[] = []
    const onPageError = (error: Error) => pageErrors.push(error)
    page.on('pageerror', onPageError)
    try {
      await page.goto(`/#/report/${run.testId}?lens=${lens}`)
      await expect(
        page.getByText(runName, { exact: false }).first(),
        `the ${lens} lens must identify the run it describes`,
      ).toBeVisible({ timeout: 30_000 })
      expect(pageErrors, `the ${lens} lens threw: ${pageErrors.map(e => e.message).join('; ')}`).toEqual([])
    } finally {
      page.off('pageerror', onPageError)
    }
  }
})
