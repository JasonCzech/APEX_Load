import type { APIRequestContext, Page } from '@playwright/test'
import { stat } from 'node:fs/promises'
import { basename } from 'node:path'

import { expect, test } from './support/liveRun'

// Tier-0 journey steps S8, S9 and S11 (docs/qa/E2E_SUITES.md): controlling a
// run mid-flight, stopping it, and proving the evidence it produced is intact.
//
// The archive assertion is the strongest one in the whole journey: it proves the
// run produced complete, checksum-verified evidence, which is the product's
// actual output. A run that executes but cannot produce a verifiable archive has
// not done its job.

const TERMINAL = new Set(['COMPLETE', 'FAILED', 'ABORTED'])

async function status(request: APIRequestContext, testId: string): Promise<string> {
  const response = await request.get(`/api/v1/tests/${testId}`)
  expect(response.ok(), `GET /api/v1/tests/${testId}`).toBeTruthy()
  const body = (await response.json()) as Record<string, unknown>
  const test = (body.test ?? body) as Record<string, unknown>
  return String(test.status ?? '')
}

async function openLiveControls(page: Page, testId: string): Promise<void> {
  await page.goto(`/#/command-center/${testId}`)
  await expect(page.getByRole('combobox', { name: 'Current run', exact: true })).toHaveValue(testId)
  await expect(page.getByRole('button', { name: 'Pause', exact: true })).toBeVisible()
}

/** Polls rather than sleeping a fixed interval: run transitions are not instant
 *  and a fixed wait either flakes under load or wastes the budget. */
async function waitForStatus(
  request: APIRequestContext,
  testId: string,
  predicate: (value: string) => boolean,
  what: string,
  timeoutMs = 60_000,
): Promise<string> {
  const deadline = Date.now() + timeoutMs
  let last = ''
  while (Date.now() < deadline) {
    last = await status(request, testId)
    if (predicate(last)) return last
    await new Promise(resolve => setTimeout(resolve, 500))
  }
  throw new Error(`${what}: status stayed ${last || '(empty)'} for ${timeoutMs}ms`)
}

// Teardown deliberately lives in the `seedLiveRun` fixture rather than a
// `finally` in this body. Playwright stops awaiting the body when a test times
// out and begins disposing fixtures, so a body `finally` runs against an
// already-closed request context and every teardown call throws — leaving the
// seeded run executing against the controller the next spec asserts on. The
// repository verified this directly: a body `finally` after a timeout reached
// the server zero times.
test('[U13 U14] a run dismisses confirmation, pauses, resumes, scales, extends, stops, and downloads evidence', async ({ request, page, seedLiveRun }) => {
  test.setTimeout(240_000)

  {
    const run = await seedLiveRun({ label: 'tier0-lifecycle', vusers: 2 })
    const testId = run.testId
    await openLiveControls(page, testId)

    // S8 — dismissing confirmation must be inert, then the confirmed action must
    // be observable in both the API and the controls a user is looking at.
    await page.getByRole('button', { name: 'Pause', exact: true }).click()
    const pauseDialog = page.getByRole('dialog', { name: 'Pause this test run?' })
    await expect(pauseDialog).toBeVisible()
    await pauseDialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(pauseDialog).toBeHidden()
    expect(await status(request, testId), 'cancelled confirmation must not pause the run').toBe('RUNNING')

    await page.getByRole('button', { name: 'Pause', exact: true }).click()
    await page.getByRole('dialog', { name: 'Pause this test run?' }).getByRole('button', { name: 'Pause Test' }).click()
    await waitForStatus(request, testId, value => value === 'PAUSED', 'pause')
    await expect(page.getByRole('button', { name: 'Resume', exact: true })).toBeVisible({ timeout: 15_000 })

    await page.getByRole('button', { name: 'Resume', exact: true }).click()
    await page.getByRole('dialog', { name: 'Resume this test run?' }).getByRole('button', { name: 'Resume Test' }).click()
    await waitForStatus(request, testId, value => value === 'RUNNING', 'resume')

    const target = page.getByRole('spinbutton', { name: 'smoke target VUsers', exact: true })
    const vuserRow = page.getByRole('row').filter({ has: target })
    await target.fill('3')
    await vuserRow.getByRole('button', { name: 'Apply', exact: true }).click()
    await expect.poll(async () => (await (await request.get(`/api/v1/tests/${testId}`)).json()).groups[0].vusers).toBe(3)

    // The current command-center dialog previews and applies the runtime change.
    await page.getByRole('button', { name: 'Change duration', exact: true }).click()
    const durationDialog = page.getByRole('dialog', { name: 'Change duration', exact: true })
    await durationDialog.getByLabel('smoke target duration', { exact: true }).fill('6m')
    await durationDialog.getByRole('button', { name: 'Review changes', exact: true }).click()
    await expect(durationDialog).toContainText('Ready to apply')
    await durationDialog.getByRole('button', { name: 'Apply changes', exact: true }).click()
    await expect(durationDialog).toBeHidden()
    await expect.poll(async () => (await (await request.get(`/api/v1/tests/${testId}`)).json()).duration).toBe('6m0s')

    // S9 — stop must reach a terminal status. The specific status is asserted
    // below rather than accepted as "whatever happened": a prior review recorded
    // stop being upgraded to ABORTED in some paths, so the value carries meaning.
    await page.getByRole('button', { name: 'Stop', exact: true }).click()
    await page.getByRole('dialog', { name: 'Stop this test run?' }).getByRole('button', { name: 'Stop Test' }).click()
    const terminal = await waitForStatus(request, testId, value => TERMINAL.has(value), 'stop', 120_000)
    expect(terminal, 'a graceful stop must preserve successful terminal semantics').toBe('COMPLETE')

    // S11 — the archive is the run's real output. Finalization is asynchronous,
    // so poll for the manifest rather than assuming it exists the moment the run
    // reaches a terminal status.
    const manifestDeadline = Date.now() + 90_000
    let manifestOk = false
    let transactionsFile = ''
    while (Date.now() < manifestDeadline) {
      const manifestResponse = await request.get(`/api/v1/tests/${testId}/archive/manifest`)
      if (manifestResponse.ok()) {
        const manifest = (await manifestResponse.json()) as Record<string, unknown>
        transactionsFile = String(manifest.transactions_file ?? '')
        manifestOk = true
        break
      }
      await new Promise(resolve => setTimeout(resolve, 1_000))
    }
    expect(manifestOk, 'a terminated run must publish an archive manifest').toBeTruthy()
    expect(transactionsFile, 'the manifest must list the transactions artifact').not.toBe('')

    // The integrity check is what makes the archive evidence rather than output.
    // Assert on the report's own vocabulary rather than substring-matching the
    // body: the payload carries a `"mismatched": 0` COUNT field, so a naive
    // search for "mismatch" flags a perfectly healthy archive as tampered.
    const verify = await request.get(`/api/v1/tests/${testId}/archive/verify`)
    expect(verify.ok(), `archive verify: ${verify.status()} ${await verify.text()}`).toBeTruthy()
    const report = (await verify.json()) as Record<string, unknown>

    // 'verified' = every artifact recomputed to its recorded SHA-256.
    // 'verified_partial' = all hashed artifacts match, some carried no hash.
    // Anything else — tampered, corrupt, unverifiable — means the run's evidence
    // cannot be trusted, which is the failure this whole step exists to catch.
    expect(['verified', 'verified_partial'], `archive integrity status was ${String(report.status)}`)
      .toContain(String(report.status))
    expect(report.mismatched, 'no artifact may fail its recorded checksum').toBe(0)

    await page.goto(`/#/test/${testId}?tab=archive`)
    await expect(page).toHaveURL(new RegExp(`/report/${testId}\\?section=evidence`))
    await page.getByText('Raw evidence downloads', { exact: true }).click()
    const downloadButton = page.getByRole('button', { name: 'Download Transactions JSONL' }).first()
    await expect(downloadButton).toBeVisible({ timeout: 30_000 })
    const downloadPromise = page.waitForEvent('download')
    await downloadButton.click()
    const download = await downloadPromise
    expect(download.suggestedFilename()).toContain(basename(transactionsFile))
    const downloadedPath = await download.path()
    expect(downloadedPath, 'the listed artifact must be materialized as a browser download').not.toBeNull()
    expect((await stat(downloadedPath!)).size, 'the downloaded artifact must not be empty').toBeGreaterThan(0)
  }
})

test('[U13 U14] abort force-cancels a live run with exact terminal semantics', async ({ request, page, seedLiveRun }) => {
  test.setTimeout(180_000)

  const run = await seedLiveRun({ label: 'tier0-abort' })
  await openLiveControls(page, run.testId)
  await page.getByRole('button', { name: 'Abort', exact: true }).click()
  await page.getByRole('dialog', { name: 'Abort this test run?' }).getByRole('button', { name: 'Abort Test' }).click()
  const terminal = await waitForStatus(request, run.testId, value => TERMINAL.has(value), 'abort', 120_000)
  expect(terminal, 'force abort must not be reported as a graceful completion').toBe('ABORTED')
})
