import { test, expect, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

// Browser presentation of the correlation contract (B6): each series keeps
// its own semantics, excluded evidence is counted, an unscored lag says why,
// and different units get separate labelled axes. The producer (shared
// alignment, exclusions, conversions, refusal of raw summaries) is qualified
// in the Go analysis and DuckDB API tests; these responses are fixtures.
test('a CPU-percent vs latency-ms correlation keeps both semantics on separate axes', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  const cpuId = '00000000-0000-0000-0000-0000000000b6'; const latencyId = '00000000-0000-0000-0000-0000000000b7'
  const cpuMapping = { version: 1, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [{ value: ['value'], name: 'cpu', kind: 'gauge', unit: 'percent' }] }
  const latencyMapping = { version: 1, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [{ value: ['p95'], name: 'latency', kind: 'summary', unit: 'ms', statistic: 'p95', window_seconds: 10 }] }
  const imports = [
    { id: cpuId, project_id: workspace.projectId, name: 'Host CPU', revision: 1, state: 'ready', files: [], accepted: 60, rejected: 0, mapping: cpuMapping },
    { id: latencyId, project_id: workspace.projectId, name: 'Service latency', revision: 1, state: 'ready', files: [], accepted: 60, rejected: 0, mapping: latencyMapping },
  ]
  const definition = {
    left: { dataset_id: cpuId, metric: 'cpu', aggregation: 'avg' },
    right: { dataset_id: latencyId, metric: 'latency', aggregation: 'p95' },
    from: '2026-01-01T00:00:00Z', to: '2026-01-01T00:10:00Z', bucket_seconds: 10, max_lag_seconds: 20, mode: 'correlation',
  }
  const point = (i: number, metric: string, unit: string, value: number, quality = 'valid') => ({ timestamp_utc: new Date(Date.UTC(2026, 0, 1, 0, 0, i * 10)).toISOString(), metric_name: metric, unit, kind: 'gauge', value: quality === 'valid' ? value : undefined, quality })
  let compared = 0
  await page.route('**/api/v1/analysis/**', async route => {
    const request = route.request(); const path = new URL(request.url()).pathname
    const send = (value: unknown) => route.fulfill({ json: value })
    if (path.endsWith('/capabilities')) return send({ native_parquet: true, enabled: true, imported_identity_version: 1, chunk_bytes: 4 * 1024 * 1024 })
    if (path.endsWith('/imports')) return send(imports)
    if (path.endsWith('/comparisons')) return send([{ id: 'cpu-latency', name: 'CPU vs latency', revision: 1, definition, deleted: false }])
    if (path.endsWith('/mappings')) return send([])
    if (path.endsWith('/compare')) {
      compared++
      const selected = request.postDataJSON()
      expect(selected.mode).toBe('correlation')
      const left = Array.from({ length: 6 }, (_, i) => point(i, 'cpu', 'percent', 40 + i * 5, i === 3 ? 'stale' : 'valid'))
      const right = Array.from({ length: 6 }, (_, i) => point(i, 'latency', 'ms', 180 + i * 20))
      return send({
        request: selected, left, right, advisory: true, interpretation: 'Correlation is association, not proof of causation.',
        correlation: [
          { lag_seconds: -20, pairs: 2, expected_buckets: 58, coverage: 0.03, unavailable: 'insufficient_pairs' },
          { lag_seconds: 0, pairs: 5, expected_buckets: 60, coverage: 0.083, coefficient: 0.42 },
          { lag_seconds: 20, pairs: 5, expected_buckets: 58, coverage: 0.086, coefficient: 0.91 },
        ],
        correlation_semantics: {
          left: { metric: 'cpu', unit: 'percent', kind: 'gauge', input_kind: 'gauge', statistic: 'avg', points: 6, valid: 5, excluded: { stale: 1 } },
          right: { metric: 'latency', unit: 'ms', kind: 'gauge', input_kind: 'summary', statistic: 'p95', conversion: 'declared_summary_statistic', points: 6, valid: 6 },
          same_units: false,
          lag_convention: 'lag_seconds > 0 pairs each left bucket with the right series lag seconds later: the right series follows the left.',
        },
      })
    }
    return route.fulfill({ status: 404, json: { error: 'unexpected bounded workflow request' } })
  })
  await page.goto(`/#/monitoring?${scopeQuery(workspace)}`); await page.reload()
  const area = page.getByRole('region', { name: 'Analysis workspace', exact: true })
  await area.getByRole('button', { name: 'Host CPU · ready', exact: true }).click()
  const comparison = area.getByRole('region', { name: 'Dataset comparison', exact: true })
  await comparison.getByRole('combobox', { name: 'Saved comparison', exact: true }).selectOption('cpu-latency')
  await comparison.getByRole('button', { name: 'Analyze comparison', exact: true }).click()
  await expect.poll(() => compared).toBe(1)
  const evidence = comparison.locator('[aria-label="Correlation evidence"]')
  const dataset = evidence.locator('[data-side="Dataset series"]')
  const other = evidence.locator('[data-side="Comparison series"]')
  await expect(dataset).toContainText('percent')
  await expect(dataset).toContainText('1 stale')
  await expect(other).toContainText('ms')
  await expect(other).toContainText('declared summary statistic (not raw observations)')
  await expect(evidence).toContainText('never compared value for value')
  await expect(evidence.getByRole('img')).toHaveAttribute('aria-label', 'Correlation investigation chart: percent on the left axis, ms on the right axis')
  const scores = evidence.getByRole('table', { name: 'Lagged correlation', exact: true })
  await expect(scores.locator('tbody tr').nth(0)).toContainText('Fewer than three paired buckets')
  await expect(scores.locator('tbody tr').nth(2)).toContainText('0.910')
  await expect(scores.locator('caption')).toContainText('the comparison series follows the dataset series')
  await evidence.screenshot({ path: test.info().outputPath('correlation-semantics.png') })
})

test.afterEach(async ({ page }) => { await page.unrouteAll({ behavior: 'wait' }) })
