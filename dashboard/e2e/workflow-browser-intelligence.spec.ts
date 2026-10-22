import type { APIRequestContext } from '@playwright/test'
import { test, expect, seedScript, json, idOf, login, runStatus } from './support/workflow'
import { createWorkspace, scopeQuery, type Workspace } from './support/workspace'
import { RESOURCE_GENERATION, resourcePageFixture } from '../src/components/browserResourcePage.testFixtures'
import { browserVitalsAlgorithm, browserVitalsAlgorithmSHA256 } from '../src/types/browserDocumentEvidence'

const DOCUMENT_ID = `${RESOURCE_GENERATION}-document-7`
const REQUEST_ID = `${RESOURCE_GENERATION}-request-1`

async function archivedRun(request: APIRequestContext, workspace: Workspace, targetURL: string, trackRun: (id: string) => void) {
  const script = await seedScript(request, workspace, `Browser_area_${Date.now()}`, targetURL)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: {
    project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId,
    name: 'Browser intelligence area', duration: '1s', groups: [{ name: 'shop', script_id: script, vusers: 1 }],
  } })))
  trackRun(id)
  await json(await request.post(`/api/v1/tests/${id}/start`))
  await expect.poll(() => runStatus(request, id), { timeout: 90_000 }).toBe('COMPLETE')
  await expect.poll(async () => (await request.get(`/api/v1/tests/${id}/archive/manifest`)).ok(), { timeout: 60_000 }).toBe(true)
  return id
}

// A distribution of one value, bucketed as protocol.BrowserMetricDistribution.Add buckets it.
function oneValue(value: number) {
  const buckets = Array<number>(192).fill(0)
  buckets[Math.min(191, Math.ceil(Math.log(value / 0.000001) / Math.log(1.2)))] = 1
  return { count: 1, sum: value, min: value, max: value, buckets }
}

const cohort = { version: 1, browser_version: '136.0.1', platform: 'linux', viewport_width: 800, viewport_height: 600, headless: true, mobile: false, touch: false, device_scale_factor: 1, cpu_throttle_rate: 1, network_latency_ms: 0, network_bandwidth_kbps: 0, shaping_applied: true, cache_enabled: false, cache_content_urls: false, check_newer_versions: false, new_user_policy: 'persistent_context', new_user_policy_applied: true }
const documentsSummary = { version: 2, dropped_observations: 0, incomplete: false, metrics: [
  { metric: 'lcp', algorithm: browserVitalsAlgorithm, algorithm_sha256: browserVitalsAlgorithmSHA256, unit: 'ms', engine: 'chromium', device_profile: 'desktop', group: 'shop', cohort,
    frame_scope: 'main_frame', lifecycle_phase: 'navigation', eligible_final: 1, unavailable_final: 0, unqualified_final: 0, censored: 0, provisional: 0, distribution: oneValue(4700) },
] }

function findingsFixture(id: string, policyId: string) {
  return { run_id: id, status: 'ready', job_id: 'job-1', result: {
    schema_version: 'apexload.browser_findings.v1', rule_set: 'browser_findings.v1', input_fingerprint: 'fp', run_id: id,
    policy: { id: policyId, name: 'Checkout vitals', revision: 1, sha256: 'abc', source: 'pinned' }, window: { start: '', end: '' },
    score: { version: 'browser_score.v1', severity: 'poor', score: 0, coverage: { observed: 1, required: 1 },
      components: [{ metric: 'lcp', weight: 1, required: true, good: 2500, poor: 4000, observed: 4700, samples: 1, score: 0, contribution: 0 }] },
    findings: [{ id: 'a1', key: 'k1', rule_set: 'browser_findings.v1', rule_id: 'threshold', rule_version: 1, category: 'application_outcome', metric: 'lcp', severity: 'poor',
      cohort: { engine: 'chromium', device_profile: 'desktop', frame_scope: 'main_frame', lifecycle_phase: 'navigation' }, window: { start: '', end: '' }, applicable: true,
      quality: { eligible_final: 1, unqualified_final: 0, unavailable_final: 0, censored: 0, provisional: 0 }, baseline: { mode: 'none', status: 'not_evaluated' },
      evidence: { documents: [{ document_id: DOCUMENT_ID, source_generation: RESOURCE_GENERATION, value: 4700, resources: '/api' }] }, state: 'active', summary: 'LCP p75 is poor.' }],
    counts: { active: 1, suppressed: 0, unscored: 0 }, notes: [],
  } }
}

test.afterEach(async ({ page }) => { await page.unrouteAll({ behavior: 'wait' }) })

// BI-4: the run report's Browser area compares documents and leads from a
// finding to its document's resources and one request's HAR entry. The run,
// its archive and policy pin are real, and the real HAR entry API answers 404
// for a request the archive does not hold; the report's document summary,
// the findings result, the resource page and the HAR entry shown are bounded
// transport fixtures, as in the HAR and resource inspector workflows.
test('the Browser area leads from a finding to its document resources and HAR entry, and exports its sections', async ({ page, target, trackRun }) => {
  test.setTimeout(180_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scope = `project_id=${encodeURIComponent(workspace.projectId)}`
  const policy = await json(await request.post(`/api/v1/analysis/policies?${scope}`, { data: {
    kind: 'browser_policy', name: 'Checkout vitals', definition: { version: 1, apply_by_default: true, baseline: { mode: 'none' }, metrics: [{ metric: 'lcp', good: 2500, poor: 4000, weight: 1 }] },
  } }))
  const id = await archivedRun(request, workspace, target.url, trackRun)
  // A request identity the archive does not hold is not found.
  expect((await request.get(`/api/v1/tests/${id}/archive/browser/har-entry?request_id=${encodeURIComponent(REQUEST_ID)}`)).status()).toBe(404)

  await page.route(`**/api/v1/tests/${id}/archive/report*`, async route => {
    const response = await route.fetch()
    await route.fulfill({ response, json: { ...(await response.json()), browser_documents: documentsSummary, browser_resources: [] } })
  })
  await page.route(`**/api/v1/tests/${id}/archive/browser-findings*`, route => route.fulfill({ json: findingsFixture(id, String(policy.id)) }))
  // Transport fixture for v3 pattern eligibility and mixed map rendering; API archive tests independently
  // qualify cohort separation. This HTTP run does not qualify native capture.
  await page.route(`**/api/v1/tests/${id}/archive/browser-interactions`, route => route.fulfill({ json: {
    run_id: id, version: 'browser_interaction_patterns.v3', status: 'ready',
    thresholds: { repeat_min_gestures: 3, repeat_window_ms: 2000, repeat_radius_px: 32, heatmap_grid: 64 },
    coverage: { pattern_eligible: 1, pattern_excluded: { legacy_identity_unavailable: 1, dispatch_unavailable: 2 }, interactions: 4, records: 1, dropped: 0, incomplete: false, by_provenance: { scripted: 4 }, mapped: 2, excluded: { engine_profile_unknown: 1, layout_unknown: 1 } },
    patterns: [], notes: ['Native dispatch and historical post-action maps are separate populations.'],
    heatmaps: [0, 1200].map((y, index) => ({ key: `cohort-${index}`, observation: index === 0 ? 'native_dispatch' : 'post_action', route: '/cart', layout_revision: 'aabb',
      viewport_width: 400, viewport_height: 800, orientation: 'portrait', engine: 'webkit', device_profile: 'iphone_15_pro',
      coordinate_space: 'viewport_css_px', device_scale_factor: 2, scroll: { min_x: 0, max_x: 0, min_y: y, max_y: y },
      columns: 64, rows: 64, cells: [[1, 1, 1]], points: 1, by_kind: { tap: 1 }, sources: { scripted: 1 },
    })),
  } }))
  const resourceQueries: string[] = []
  await page.route(`**/api/v1/tests/${id}/archive/browser-resources*`, async route => {
    resourceQueries.push(new URL(route.request().url()).searchParams.get('document_id') || '')
    const value = resourcePageFixture()
    value.test_id = id; value.project_id = workspace.projectId
    await route.fulfill({ json: value })
  })
  await page.route(`**/api/v1/tests/${id}/archive/browser/har-entry*`, route => route.fulfill({ json: {
    request_id: REQUEST_ID, source_generation: RESOURCE_GENERATION, fidelity: 'synthetic', headers_omitted: true,
    entry: { request: { method: 'GET', url: '/resource-zero' }, response: { status: 0 }, _apexload: { request_id: REQUEST_ID } },
  } }))

  await page.goto(`/#/report/${id}?section=browser&${scopeQuery(workspace)}`)
  await page.reload()
  await expect(page.getByRole('tab', { name: 'Browser', exact: true })).toHaveAttribute('aria-selected', 'true')
  const comparison = page.getByRole('table', { name: 'Browser document comparison', exact: true })
  await expect(comparison.getByRole('row').nth(1)).toContainText('chromium')
  await expect(comparison.getByRole('row').nth(1)).toContainText('shop')
  await expect(comparison.getByRole('row').nth(1)).toContainText('(1 document)')
  const interactions = page.getByRole('region', { name: 'Interaction patterns', exact: true })
  await expect(interactions.locator('figure')).toHaveCount(2)
  await expect(interactions).toContainText('recorded scroll offsets x 0 px, y 0 px')
  await expect(interactions).toContainText('recorded scroll offsets x 0 px, y 1200 px')
  await expect(interactions).toContainText('Not mapped: 1 engine profile unknown, 1 layout unknown.')
  await expect(interactions).toContainText('1 gesture qualifies for patterns.')
  await expect(interactions).toContainText('Excluded from patterns: 1 legacy identity unavailable, 2 dispatch unavailable.')
  await expect(interactions).toContainText('native dispatch geometry')
  await expect(interactions).toContainText('historical post-action geometry')
  await expect(interactions).not.toContainText('one action name')
  const findings = page.getByRole('region', { name: 'Browser findings', exact: true })
  await expect(findings).toContainText('LCP p75 is poor.')
  await expect(page.getByRole('region', { name: 'Browser baseline', exact: true })).toContainText('has not been run for this run')

  await findings.getByRole('button', { name: `Inspect the resources of document …${DOCUMENT_ID.slice(-8)}`, exact: true }).click()
  await expect(page.getByRole('status').filter({ hasText: `the retained resources of document …${DOCUMENT_ID.slice(-8)}` })).toBeVisible()
  const inspector = page.getByRole('region', { name: 'Retained archive resource inspector', exact: true })
  await expect(inspector).toBeVisible()
  await expect.poll(() => resourceQueries.at(-1)).toBe(DOCUMENT_ID)
  await inspector.getByText('Inspect retained resource 1').click()
  await inspector.getByRole('button', { name: "Show this request's HAR entry", exact: true }).click()
  await expect(inspector.getByLabel('HAR entry', { exact: true })).toContainText(REQUEST_ID)
  await expect(inspector).toContainText('this archive keeps no headers')

  // Exports: the engineer report's CSV (unrouted: the real archive) carries
  // the browser intelligence sections, each saying why its result is missing
  // (the browser baseline is part of the rca section).
  const exported = await request.get(`/api/v1/tests/${id}/archive/report?format=csv&profile=engineer`)
  expect(exported.status()).toBe(200)
  const csv = await exported.text()
  for (const section of ['browser_findings', 'interaction_map', 'journey_risk']) expect(csv).toContain(section)
  expect(csv).toContain('This run has no browser evaluation.')
  expect(csv).toContain('This run captured no interaction evidence.')
  expect(csv).toContain('The run pinned no journey policy.')
})

// BI-4: an editor creates and edits a browser policy; a save based on a
// revision someone else replaced is refused and offers the current one; a
// past revision is shown read-only; a delete names the revision it read.
test('browser policies save revisions, refuse stale edits and show past revisions', async ({ page }, testInfo) => {
  test.setTimeout(120_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scope = `project_id=${encodeURIComponent(workspace.projectId)}`
  await page.goto(`/#/settings?setting=analysis-policies&${scopeQuery(workspace)}`)
  // The workspace was created after sign-in: reload so the page knows it.
  await page.reload()
  const panel = page.getByRole('region', { name: 'Browser and journey policies', exact: true })
  await expect(panel).toContainText('This project has no browser policies yet.')

  await panel.getByRole('button', { name: 'New browser policy', exact: true }).click()
  const editor = panel.getByRole('form', { name: 'New browser policy', exact: true })
  await editor.getByLabel('Name', { exact: true }).fill('Checkout vitals')
  await editor.getByLabel('LCP floor (ms)', { exact: true }).fill('350')
  await editor.getByLabel('FCP floor (ms)', { exact: true }).fill('75')
  await editor.getByLabel('INP floor (ms)', { exact: true }).fill('50')
  await page.setViewportSize({ width: 760, height: 900 })
  await editor.getByLabel('LCP floor (ms)', { exact: true }).scrollIntoViewIfNeeded()
  await expect(editor.getByLabel('LCP floor (ms)', { exact: true })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('browser-metric-floors.png'), animations: 'disabled' })
  await editor.getByRole('button', { name: 'Save policy', exact: true }).click()
  const table = panel.getByRole('table', { name: 'Browser policies', exact: true })
  await expect(table.getByRole('row', { name: /Checkout vitals/ })).toContainText('1')
  const [stored] = await json(await request.get(`/api/v1/analysis/policies?${scope}&kind=browser_policy`)) as unknown as Array<{ id: string; revision: number; name: string; definition: Record<string, unknown> }>
  expect(stored).toMatchObject({ name: 'Checkout vitals', revision: 1, definition: { effect_floors: { timing_by_metric_ms: { lcp: 350, fcp: 75, inp: 50 } } } })

  // Someone else saves revision 2 while this editor holds revision 1.
  await table.getByRole('button', { name: 'Edit', exact: true }).click()
  const edit = panel.getByRole('form', { name: 'Edit browser policy', exact: true })
  await expect(edit).toContainText('(revision 1)')
  await expect(edit.getByLabel('LCP floor (ms)', { exact: true })).toHaveValue('350')
  await json(await request.put(`/api/v1/analysis/policies/${stored.id}?${scope}`, { data: { kind: 'browser_policy', name: 'Checkout vitals (API)', definition: { ...stored.definition, effect_floors: { timing_by_metric_ms: { lcp: 500, fcp: 75, inp: 50 } } }, revision: 1 } }))
  await edit.getByLabel('LCP good bound', { exact: true }).fill('2000')
  await edit.getByRole('button', { name: 'Save policy', exact: true }).click()
  await expect(edit).toContainText('This policy changed since you opened it')
  await edit.getByRole('button', { name: 'Load the current revision', exact: true }).click()
  await expect(edit).toContainText('(revision 2)')
  await expect(edit.getByLabel('LCP floor (ms)', { exact: true })).toHaveValue('500')
  await expect(edit.getByLabel('Name', { exact: true })).toHaveValue('Checkout vitals (API)')
  await edit.getByLabel('LCP good bound', { exact: true }).fill('2000')
  await edit.getByRole('button', { name: 'Save policy', exact: true }).click()
  await expect(table.getByRole('row', { name: /Checkout vitals \(API\)/ })).toContainText('3')
  const current = await json(await request.get(`/api/v1/analysis/policies/${stored.id}?${scope}`)) as unknown as { revision: number; definition: { metrics: Array<{ metric: string; good: number }> } }
  expect(current.revision).toBe(3)
  expect(current.definition).toMatchObject({ effect_floors: { timing_by_metric_ms: { lcp: 500, fcp: 75, inp: 50 } } })
  expect(current.definition.metrics.find(metric => metric.metric === 'lcp')?.good).toBe(2000)

  // Revision 1 is unchanged and read-only.
  await table.getByRole('button', { name: 'View a revision', exact: true }).click()
  const revision = panel.getByRole('region', { name: 'Policy revision', exact: true })
  await revision.getByLabel('Revision', { exact: true }).fill('1')
  await revision.getByRole('button', { name: 'Show', exact: true }).click()
  await expect(revision).toContainText('Revision 1')
  await expect(revision).toContainText('"good": 2500')
  await expect(revision).toContainText('"lcp": 350')

  await table.getByRole('button', { name: 'Delete', exact: true }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Delete policy', exact: true }).click()
  await expect(panel).toContainText('This project has no browser policies yet.')
})
