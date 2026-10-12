import type { APIRequestContext } from '@playwright/test'
import type { DashboardSystemSettings } from '../src/types'
import { test, expect, seedScript, json, idOf, login, runStatus } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

async function writeSettings(request: APIRequestContext, value: DashboardSystemSettings) {
  const { revision, ...body } = value
  return await json(await request.put('/api/v1/system/settings', { data: body, headers: revision ? { 'If-Match': JSON.stringify(revision) } : {} })) as unknown as DashboardSystemSettings
}

// Real controller admission, controlled HTTP traffic, live publications and
// archive receipt. It does not qualify source-clock/time-coverage/CI semantics.
test('telemetry policy remains immutable in live, archive and manifest views after settings change', async ({ page, target, trackRun }) => {
  test.setTimeout(90_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const original = await json(await request.get('/api/v1/system/settings')) as unknown as DashboardSystemSettings
  try {
    await writeSettings(request, { ...original, general: { ...original.general, metrics_interval_seconds: 3 }, thresholds: { ...original.thresholds, max_telemetry_sample_age_ms: 17, max_telemetry_generator_lag_ms: 0 } })
    const workspace = await createWorkspace(request)
    const script = await seedScript(request, workspace, 'Telemetry policy HTTP fixture', target.url)
    const id = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Immutable telemetry policy run', duration: '30s', groups: [{ name: 'orders', script_id: script, vusers: 1 }] } })))
    trackRun(id)
    await json(await request.post(`/api/v1/tests/${id}/start`))
    await expect.poll(() => runStatus(request, id)).toBe('RUNNING')
    await expect.poll(target.count).toBeGreaterThan(0)
    const initial = await json(await request.get(`/api/v1/tests/${id}/manifest`))
    const evidence = initial.telemetry_policy as { availability: string; policy: { hash: string; budgets: { stage: string; limit_ms: number; enabled: boolean }[] } }
    expect(evidence.availability).toBe('available')
    expect(evidence.policy.hash).toMatch(/^[a-f0-9]{64}$/)
    expect(evidence.policy.budgets.find(budget => budget.stage === 'sample_age')).toMatchObject({ limit_ms: 17, enabled: true })
    const current = await json(await request.get('/api/v1/system/settings')) as unknown as DashboardSystemSettings
    await writeSettings(request, { ...current, general: { ...current.general, metrics_interval_seconds: 1 }, thresholds: { ...current.thresholds, max_telemetry_sample_age_ms: 999 } })
    await page.goto(`/#/test/${id}?tab=diagnostics&${scopeQuery(workspace)}`)
    await page.reload()
    const live = page.getByRole('region', { name: 'Telemetry budget policy', exact: true })
    await expect(live).toContainText(evidence.policy.hash)
    await expect(live).toContainText('17 ms')
    await expect(live).toContainText('3,000 ms')
    await expect(live).toContainText('Disabled (explicit zero)')
    await expect(live).not.toContainText('999 ms')
    const pipeline = page.locator('.telemetry-status-card')
    await expect(pipeline).toContainText('Telemetry observations')
    await expect(pipeline.locator('.goal-status-badge')).toHaveClass(/neutral/)
    await expect(pipeline.locator('.goal-status-badge')).toHaveText(/^(Age observed|Unknown|Observed drop evidence)$/)
    await expect(pipeline).not.toContainText('Healthy')
    await expect(pipeline).not.toContainText('Stale')
    await expect(pipeline).toContainText('not an exact run loss total')
    await page.setViewportSize({ width: 760, height: 1100 })
    await live.scrollIntoViewIfNeeded()
    const bounds = await live.evaluate(node => { const rect = node.getBoundingClientRect(); return { left: rect.left, right: rect.right, viewport: innerWidth, scrollWidth: node.scrollWidth, clientWidth: node.clientWidth } })
    await test.info().attach('telemetry-policy-live-geometry', { body: JSON.stringify({ viewport: page.viewportSize(), browser_version: page.context().browser()?.version(), geometry: bounds }), contentType: 'application/json' })
    expect(bounds.left).toBeGreaterThanOrEqual(0)
    expect(bounds.right).toBeLessThanOrEqual(bounds.viewport)
    expect(bounds.scrollWidth).toBeLessThanOrEqual(bounds.clientWidth + 1)
    await live.screenshot({ path: test.info().outputPath('telemetry-policy-live.png') })
    await json(await request.post(`/api/v1/tests/${id}/abort`))
    await expect.poll(() => runStatus(request, id)).toMatch(/^(COMPLETE|FAILED|ABORTED)$/)
    await expect.poll(async () => (await request.get(`/api/v1/tests/${id}/archive/report`)).ok(), { timeout: 30_000 }).toBe(true)
    const report = await json(await request.get(`/api/v1/tests/${id}/archive/report`))
    expect(report.telemetry_policy).toEqual(evidence)
    await test.info().attach('actual-immutable-policy-receipt', { body: JSON.stringify({ test_id: id, telemetry_policy: evidence, target_requests_observed: target.count() }), contentType: 'application/json' })
    const archivePage = await page.context().newPage()
    await archivePage.goto(`/#/report/${id}?section=evidence&${scopeQuery(workspace)}`)
    const archived = archivePage.getByRole('region', { name: 'Telemetry budget policy', exact: true })
    await expect(archived).toContainText(evidence.policy.hash)
    await expect(archived).toContainText('17 ms')
    await expect(archived).not.toContainText('999 ms')
    await expect(archived).toContainText('Missing intervals remain unknown')
    await expect(archivePage.getByRole('heading', { name: 'Legacy telemetry classification', exact: true })).toBeVisible()
    await expect(archivePage.locator('.archive-metadata-summary')).toContainText('Retained legacy mixed diagnostic classifications')
    await expect(archivePage.locator('.archive-metadata-summary')).toContainText('exact run loss totals, or a telemetry CI verdict')
    await archived.screenshot({ path: test.info().outputPath('telemetry-policy-archive.png') })
    await archivePage.getByRole('tab', { name: 'Execution', exact: true }).click()
    // On Execution the receipt waits behind the configuration evidence disclosure.
    await archivePage.getByText('Configuration and infrastructure evidence', { exact: true }).click()
    const manifest = archivePage.getByRole('region', { name: 'Telemetry budget policy', exact: true })
    await expect(manifest).toContainText(evidence.policy.hash)
    await expect(manifest).toContainText('17 ms')
  } finally {
    const latest = await json(await request.get('/api/v1/system/settings')) as unknown as DashboardSystemSettings
    await writeSettings(request, { ...original, revision: latest.revision })
  }
})

// These response overrides qualify disclosure-safe UI refusal, not the source
// producer. The first case above is the genuine immutable receipt qualification.
test('telemetry manifest shows legacy absence and refuses malformed policy without inferred budgets', async ({ page, trackRun }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const script = await seedScript(request, workspace)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Legacy policy UI fixture', duration: '1s', groups: [{ name: 'orders', script_id: script, vusers: 1 }] } })))
  trackRun(id)
  let override: 'legacy' | 'malformed' | 'tampered' | 'zero-time' = 'legacy'
  await page.route(`**/api/v1/tests/${id}/manifest`, async route => {
    const response = await route.fetch()
    const record = await response.json()
    delete record.telemetry_policy
    if (override === 'malformed') record.telemetry_policy = { availability: 'available', policy: { secret: 'never-render-this-secret' } }
    if (override === 'tampered' || override === 'zero-time') record.telemetry_policy = { availability: 'available', policy: {
      schema_version: 'apexload.telemetry_policy.v1', policy_id: 'system-telemetry-budgets', revision: 1, hash: 'e6fe795b839c0b8b2769d48105525f307343b41f20e1ca2e10dc19580fdfea25', captured_at: override === 'zero-time' ? '0001-01-01T00:00:00Z' : '2026-10-05T12:00:00Z', capture_boundary: 'execution-admission-before-staging', classification_algorithm: 'legacy-mixed-diagnostic.v1', expected_controller_publication_interval_ms: 3000, cadence_scope: 'local-controller-ticker', sustained_breach_window_ms: 15000, coverage_rule: 'retained-classifications-only;gaps-unknown',
      // Actual Go golden hash belongs to sample_age=11; only this value changes.
      budgets: [{ stage: 'sample_age', enabled: true, limit_ms: override === 'zero-time' ? 11 : 12 }, { stage: 'generator_processing', enabled: false, limit_ms: 0 }, { stage: 'transport', enabled: true, limit_ms: 750 }, { stage: 'controller_fanout', enabled: true, limit_ms: 250 }, { stage: 'browser_render', enabled: true, limit_ms: 1e-12 }],
    } }
    await route.fulfill({ response, json: record })
  })
  await page.goto(`/#/test/${id}?tab=configuration&${scopeQuery(workspace)}`)
  await page.reload()
  const policy = page.getByRole('region', { name: 'Telemetry budget policy', exact: true })
  await expect(policy).toContainText('No immutable policy receipt')
  await expect(policy).not.toContainText('Disabled')
  override = 'malformed'
  await page.reload()
  await expect(policy).toContainText('invalid or unsupported')
  await expect(policy).not.toContainText('never-render-this-secret')
  await expect(policy.locator('code')).toHaveCount(0)
  override = 'tampered'
  await page.reload()
  await expect(policy).toContainText('invalid or unsupported')
  await expect(policy).not.toContainText('12 ms')
  await expect(policy.locator('code')).toHaveCount(0)
  override = 'zero-time'
  await page.reload()
  await expect(policy).toContainText('invalid or unsupported')
  await expect(policy).not.toContainText('11 ms')
  await expect(policy.locator('code')).toHaveCount(0)
})

test.afterEach(async ({ page }) => { await page.unrouteAll({ behavior: 'wait' }) })
