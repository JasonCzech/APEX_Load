import type { APIRequestContext } from '@playwright/test'
import { stat } from 'node:fs/promises'

import { expect, test } from './support/liveRun'

// Tier-0 journey steps S10 and S12 (docs/qa/E2E_SUITES.md): reading the
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

async function enableRunReport(page: import('@playwright/test').Page): Promise<void> {
  await page.addInitScript(() => window.localStorage.setItem('apexload.featureFlags.runReport', 'true'))
}

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
 * docs/qa/E2E_SUITES.md so the next caller does not repeat it.
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

test('[U16] a finished run produces a report and stays findable in results', async ({ request, page, seedLiveRun }, testInfo) => {
  test.setTimeout(TEST_TIMEOUT_MS)
  await enableRunReport(page)

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
  const filter = page.getByRole('searchbox', { name: 'Search results', exact: true })
  await expect(filter).toBeVisible({ timeout: 30_000 })
  await filter.fill(runName)
  const resultRow = page.locator('tbody tr').filter({ hasText: runName })
  await expect(resultRow, 'the filtered results view must list the finished run').toBeVisible()
  const openReport = resultRow.getByRole('button', { name: 'View results' })
  await expect(openReport, 'an archived result must expose its report action').toBeVisible({ timeout: 30_000 })
  await expect(resultRow.locator('td').last(), 'sticky actions must obscure scrolled table text').toHaveCSS('background-color', /^rgb\(\d+, \d+, \d+\)$/)
  const originalViewport = page.viewportSize()
  await page.setViewportSize({ width: 923, height: 987 })
  await expect(resultRow.locator('.results-compact-status')).toBeVisible()
  const nameBounds = await resultRow.locator('.cell-name > div').first().boundingBox()
  const actionBounds = await resultRow.locator('td').last().boundingBox()
  expect(nameBounds && actionBounds && nameBounds.x + nameBounds.width <= actionBounds.x, 'the run name must stay clear of sticky actions').toBeTruthy()
  if (originalViewport) await page.setViewportSize(originalViewport)
  await openReport.click()
  await expect(page).toHaveURL(new RegExp(`#/report/${run.testId}`))
  await expect(page.getByRole('tab', { name: 'Summary', exact: true })).toHaveAttribute('aria-selected', 'true')
  await expect(page.getByRole('region', { name: 'Run outcome' })).toBeVisible({ timeout: 30_000 })
  for (const area of ['Performance', 'Diagnostics', 'Evidence', 'Execution', 'Summary']) {
    await page.getByRole('tab', { name: area, exact: true }).click()
    await expect(page.getByRole('tabpanel', { name: area, exact: true })).toBeVisible()
    if (area === 'Performance') {
      const timeline = page.getByRole('region', { name: 'Synchronized run timeline' })
      await timeline.getByRole('button', { name: 'Graphs 4 / 6', exact: true }).click()
      const catalog = timeline.getByRole('region', { name: 'Graph catalog' })
      await expect(catalog.getByRole('checkbox')).toHaveCount(23)
      await catalog.getByRole('checkbox', { name: 'P50 Response Time', exact: true }).check()
      await catalog.getByRole('checkbox', { name: 'P99 Response Time', exact: true }).check()
      await expect(catalog.getByRole('checkbox', { name: 'P90 Response Time', exact: true })).toBeDisabled()
      await expect(timeline.locator('[data-lane]')).toHaveCount(6)
      await catalog.getByRole('checkbox', { name: 'VUsers', exact: true }).uncheck()
      await expect(catalog.getByRole('checkbox', { name: 'P90 Response Time', exact: true })).toBeEnabled()
      await catalog.getByRole('searchbox', { name: 'Search graphs', exact: true }).fill('CPU')
      await expect(catalog.getByRole('checkbox', { name: 'CPU Usage', exact: true })).toBeVisible()
      await expect(catalog.getByRole('checkbox', { name: 'P50 Response Time', exact: true })).toHaveCount(0)
      await catalog.getByRole('button', { name: 'Restore defaults' }).click()
      await expect(timeline.locator('[data-lane]')).toHaveCount(4)
      await timeline.getByRole('button', { name: 'Transactions Whole run', exact: true }).click()
      await expect(timeline.getByRole('region', { name: 'Transaction filter' })).toBeVisible()
      await expect(timeline.getByRole('combobox', { name: 'Display', exact: true })).toHaveValue('all')
    }
  }
  await page.getByRole('button', { name: 'Export report', exact: true }).click()
  const exportOptions = page.getByRole('region', { name: 'Report export options' })
  for (const label of ['Navigable HTML', 'Print-ready PDF', 'Editable Word', 'Data CSV', 'Analysis tables']) {
    await expect(exportOptions.getByRole('button', { name: new RegExp(label) })).toBeVisible()
  }
  const analysisDownload = page.waitForEvent('download')
  await exportOptions.getByRole('button', { name: /Analysis tables/ }).click()
  const bundle = await analysisDownload
  expect(bundle.suggestedFilename()).toMatch(/-executive\.zip$/)
  const bundlePath = await bundle.path()
  expect(bundlePath).not.toBeNull()
  expect((await stat(bundlePath!)).size).toBeGreaterThan(0)
  await page.getByRole('button', { name: 'Preview / edit commentary' }).click()
  const preview = page.locator('.report-composer-overlay')
  await expect(preview.getByLabel('Report title')).toBeDisabled()
  await preview.getByRole('button', { name: 'Edit commentary' }).click()
  await expect(preview.getByLabel('Report title')).toBeEnabled()
  await preview.getByRole('button', { name: 'Close', exact: true }).click()

  const exportedHTML = await page.context().newPage()
  await exportedHTML.goto(`/api/v1/tests/${run.testId}/report?profile=executive`)
  await expect(exportedHTML.getByRole('heading', { name: 'Review summary' })).toBeVisible()
  await expect(exportedHTML.getByRole('navigation', { name: 'Report contents' })).toBeVisible()
  await exportedHTML.screenshot({ path: testInfo.outputPath('executive-html-export.png') })
  await exportedHTML.locator('#report-contents').screenshot({ path: testInfo.outputPath('executive-html-contents.png') })
  await exportedHTML.locator('#report-contents a[href="#report-verdict"]').click()
  await expect(exportedHTML).toHaveURL(/#report-verdict$/)
  await exportedHTML.close()

  await expect(page.getByText(runName, { exact: false }).first()).toBeVisible({ timeout: 30_000 })
})

test('[U16] every report lens renders the same run', async ({ request, page, seedLiveRun }) => {
  test.setTimeout(TEST_TIMEOUT_MS)
  await enableRunReport(page)

  const label = 'tier0-lenses'
  const runName = `${label}-run`
  const run = await seedLiveRun({ label, readyTimeoutMs: READY_TIMEOUT_MS })
  expect((await request.post(`/api/v1/tests/${run.testId}/stop`)).ok(), 'stop must be accepted').toBeTruthy()
  await waitForTerminal(request, run.testId)
  await waitForReport(request, run.testId)

  // All three audience lenses are the same run told three ways. Each must render
  // without throwing and must be about the run it was opened for.
  const scorecards: string[] = []
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
      const scorecard = page.locator('[aria-label="Run KPI scorecard"]')
      await expect(scorecard, `the ${lens} lens must show shared headline metrics`).toBeVisible()
      scorecards.push((await scorecard.innerText()).replace(/\s+/g, ' ').trim())
      expect(pageErrors, `the ${lens} lens threw: ${pageErrors.map(e => e.message).join('; ')}`).toEqual([])
    } finally {
      page.off('pageerror', onPageError)
    }
  }
  expect(new Set(scorecards).size, 'all lenses must reconcile to the same headline values').toBe(1)
})
