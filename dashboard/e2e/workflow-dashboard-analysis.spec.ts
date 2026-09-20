import { test, expect, seedScript, json, idOf, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

test('analysis preserves evidence gaps, saved baselines, filtered deltas and report context', async ({
  page,
}, testInfo) => {
  test.setTimeout(90_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace)
  const ids: string[] = []
  for (const name of ['Baseline checkout', 'Candidate checkout'])
    ids.push(
      idOf(
        await json(
          await request.post('/api/v1/tests', {
            data: {
              project_id: workspace.projectId,
              application_id: workspace.applicationId,
              release_id: workspace.releaseId,
              name,
              duration: '1m',
              groups: [{ name: 'orders', script_id: scriptId, vusers: 10 }],
            },
          }),
        ),
      ),
    )
  const dates = ['2026-09-19T10:00:00Z', '2026-09-20T10:00:00Z']
  const stats = (index: number) => ({
    transactions: 1000,
    errors: 0,
    error_pct: 0,
    avg_response_ms: 100 + index * 50,
    p50_ms: 100,
    p95_ms: 200 + index * 100,
    p99_ms: 400,
    min_response_ms: 10,
    max_response_ms: 500,
    average_rate_per_second: 10,
  })
  const project = (run: Record<string, unknown>) => {
    const index = ids.indexOf(String(run.id))
    return index < 0
      ? run
      : {
          ...run,
          status: 'COMPLETE',
          ended_at: dates[index],
          archive: { transaction_count: 1000 },
        }
  }
  // Analytic fixture only: real scoped run IDs and navigation, deterministic
  // retained evidence without launching load against an external target.
  await page.route('**/api/v1/tests**', async (route) => {
    const path = new URL(route.request().url()).pathname
    if (path === '/api/v1/tests/archive/reports')
      return route.fulfill({
        json: {
          reports: Object.fromEntries(
            ids.map((id, index) => [
              id,
              {
                overview: { ...stats(index), peak_active_vusers: 10 },
                metric_populations: {
                  action: stats(index),
                  transaction: stats(index),
                },
                by_transaction: { Checkout: stats(index) },
                by_request: { Checkout: stats(index) },
                completeness: { score_pct: 100 },
                verdict: { status: 'pass' },
              },
            ]),
          ),
          errors: {},
        },
      })
    const index = ids.findIndex((id) =>
      path.startsWith(`/api/v1/tests/${id}/archive/report`),
    )
    if (index >= 0)
      return route.fulfill({
        json: {
          overview: { ...stats(index), peak_active_vusers: 10 },
          metric_populations: {
            action: stats(index),
            transaction: stats(index),
          },
          by_transaction: { Checkout: stats(index) },
          by_request: { Checkout: stats(index) },
          completeness: { score_pct: 100 },
          verdict: { status: 'pass' },
        },
      })
    if (
      route.request().method() !== 'GET' ||
      (path !== '/api/v1/tests' &&
        !ids.some((id) => path === `/api/v1/tests/${id}`))
    )
      return route.continue()
    const response = await route.fetch()
    const payload = await response.json()
    await route.fulfill({
      response,
      json:
        path === '/api/v1/tests'
          ? { ...payload, tests: payload.tests.map(project) }
          : payload.test
            ? { ...payload, test: project(payload.test) }
            : project(payload),
    })
  })
  await page.route(
    `**/api/v1/applications/${workspace.applicationId}/load-comparison*`,
    async (route) => {
      const query = new URL(route.request().url()).searchParams
      const group = query.get('scope') === 'group'
      const levels = [10, 40, 100].map((n) => ({
        id: String(n),
        label: String(n),
        min_vusers: n,
        max_vusers: n,
        exact: true,
        sample_count: 2,
      }))
      const runSeries = ids.map((id, index) => ({
        release_id: workspace.releaseId,
        release_name: 'Release',
        run_id: id,
        run_name: index ? 'Candidate checkout' : 'Baseline checkout',
        run_count: 1,
        points: levels
          .filter((level) => index === 0 || level.id !== '40')
          .map((level) => ({
            load_level_id: level.id,
            transactions: group ? 0 : 1000,
            errors: group ? 77 : 0,
            error_pct: 0,
            avg_response_ms: 100,
            tps: 10,
            successful_rate: group ? undefined : 10,
            observed_seconds: 100,
            sample_count: 100,
            run_count: 1,
            run_ids: [id],
            evidence_status: group ? 'inconsistent_counts' : 'observed',
            latency: group
              ? undefined
              : {
                  samples: 1000,
                  average_ms: 100,
                  p50_ms: 100,
                  p95_ms: 200 + index * 100,
                  p99_ms: 400,
                },
          })),
      }))
      await route.fulfill({
        json: {
          application_id: workspace.applicationId,
          scope: group ? 'group' : 'transaction',
          selected_key: 'Checkout',
          options: [{ key: 'Checkout', label: 'Checkout', sample_count: 2000 }],
          load_levels: levels,
          run_series: runSeries,
          releases: runSeries,
          warnings: ['One fixture archive was excluded.'],
          skipped_runs: [
            { test_id: 'excluded-run', reason: 'Missing interval evidence' },
          ],
        },
      })
    },
  )
  await page.route(`**/api/v1/releases/${workspace.releaseId}/trend`, (route) =>
    route.fulfill({
      json: {
        release_id: workspace.releaseId,
        run_count: 2,
        points: ids.map((id, index) => ({
          run_id: id,
          run_name: index ? 'Candidate checkout' : 'Baseline checkout',
          status: 'COMPLETE',
          completed_at: dates[index],
          metric_population: 'transaction',
          ...stats(index),
          peak_active_vusers: 10,
        })),
        summary: {},
      },
    }),
  )
  await page.goto(`/#/dashboard?${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByRole('tab', { name: /^Load Comparison/ }).click()
  const panel = page.getByTestId('load-comparison-panel')
  await expect(
    panel.getByRole('region', { name: 'Load comparison data' }),
  ).toContainText('Not measured')
  await expect(panel).toContainText('excluded-run: Missing interval evidence')
  await expect(panel.locator('.recharts-wrapper')).toHaveCount(3)
  await panel
    .getByRole('combobox', { name: 'Baseline', exact: true })
    .selectOption(ids[0])
  await panel
    .getByRole('combobox', { name: 'Candidate', exact: true })
    .selectOption(ids[1])
  await panel.getByText('Saved comparisons', { exact: true }).click()
  await panel
    .getByLabel('Comparison name', { exact: true })
    .fill('Checkout steady state')
  await panel.getByRole('button', { name: 'Save current comparison' }).click()
  await panel.getByRole('button', { name: 'Groups', exact: true }).click()
  await expect(panel).toContainText(
    '77 errors; matching attempt count unavailable',
  )
  await panel
    .getByRole('combobox', { name: 'Saved comparison', exact: true })
    .selectOption('Checkout steady state')
  await panel.getByRole('button', { name: 'Restore comparison' }).click()
  await expect(
    panel.getByRole('combobox', { name: 'Metric population' }),
  ).toHaveValue('action')
  await expect(
    panel.getByRole('combobox', { name: 'Baseline', exact: true }),
  ).toHaveValue(ids[0])
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.screenshot({
    path: testInfo.outputPath('load-comparison-desktop.png'),
    fullPage: true,
  })
  await page.getByRole('tab', { name: /^Release Trend Analysis/ }).click()
  await page
    .getByRole('combobox', { name: 'Transaction', exact: true })
    .selectOption('Checkout')
  const candidate = page.getByTestId(`release-trend-row-${ids[1]}`)
  await expect(candidate).toContainText('+100.0 ms')
  await candidate.getByRole('button').click()
  await expect(page.getByTestId('release-trend-detail')).toContainText(
    'Baseline checkout',
  )
  await page.setViewportSize({ width: 850, height: 1000 })
  await page.screenshot({
    path: testInfo.outputPath('release-trend-narrow.png'),
    fullPage: true,
  })
  await expect
    .poll(() =>
      page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    )
    .toBe(true)
  await page.getByRole('button', { name: 'Full report', exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`compare=${ids[0]}`))
  await expect(page).toHaveURL(/transaction=Checkout/)
  await expect(page).toHaveURL(/population=transaction/)
})
