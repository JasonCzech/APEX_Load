import { readFile } from 'node:fs/promises'
import { createServer } from 'node:http'
import type { AddressInfo } from 'node:net'
import type { APIRequestContext } from '@playwright/test'
import { test, expect, seedScript, json, idOf, login, runStatus, signOut } from './support/workflow'
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
    schema_version: 'apexload.browser_findings.v2', rule_set: 'browser_findings.v2', input_fingerprint: 'fp', run_id: id,
    policy: { id: policyId, name: 'Checkout vitals', revision: 1, sha256: 'abc', source: 'pinned' }, window: { start: '', end: '' },
    score: { version: 'browser_score.v1', severity: 'poor', score: 0, coverage: { observed: 1, required: 1 },
      components: [{ metric: 'lcp', weight: 1, required: true, good: 2500, poor: 4000, observed: 4700, samples: 1, score: 0, contribution: 0 }] },
    findings: [{ id: 'a1', key: 'k1', rule_set: 'browser_findings.v2', rule_id: 'threshold', rule_version: 2, category: 'application_outcome', metric: 'lcp', severity: 'poor',
      cohort: { engine: 'chromium', device_profile: 'desktop', frame_scope: 'main_frame', lifecycle_phase: 'navigation' }, window: { start: '', end: '' }, applicable: true,
      quality: { eligible_final: 1, unqualified_final: 0, unavailable_final: 0, censored: 0, provisional: 0 }, baseline: { mode: 'none', status: 'not_selected', reason: 'policy_baseline_off' },
      evidence: { documents: [{ document_id: DOCUMENT_ID, source_generation: RESOURCE_GENERATION, value: 4700, resources: '/api' }] }, state: 'active', summary: 'LCP p75 is poor.' }],
    counts: { active: 1, suppressed: 0, unscored: 0 }, notes: [],
  } }
}

test.afterEach(async ({ page }) => { await page.unrouteAll({ behavior: 'wait' }) })

// BI-4 (transport-fixture scope): the run report's Browser area compares
// documents and leads from a finding to its document's resources and one
// request's HAR entry. The run, its archive and policy pin are real, and the
// real HAR entry API answers 404 for a request the archive does not hold; the
// report's document summary, the findings result, the resource page and the
// HAR entry shown are bounded transport fixtures, as in the HAR and resource
// inspector workflows. This fixture-backed test does not qualify real browser
// capture, provisional live intelligence, two-run baseline comparison, saved
// v2 panels, or viewer RBAC; see the unstubbed full-chain test below.
test('the Browser area leads from a finding to its document resources and HAR entry, and exports its sections', async ({ page, target, trackRun }) => {
  test.setTimeout(180_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  await json(await request.put(`/api/v1/projects/${workspace.projectId}/analysis-settings`, {
    data: { expected_revision: 0, root_cause: { enabled: true, present: true, auto_analyze: false } },
  }))
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
    run_id: id, version: 'browser_interaction_patterns.v4', status: 'ready',
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
  await json(await request.put(`/api/v1/projects/${workspace.projectId}/analysis-settings`, {
    data: { expected_revision: 0, root_cause: { enabled: true, present: true, auto_analyze: false } },
  }))
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

// BI-4 / BI-B3 / BI-C5: an editor creates a journey policy using the structural
// StepPath picker from a project browser script, saves revisions, resolves an
// optimistic concurrency conflict by loading the current revision, views a past
// revision read-only, and deletes the policy at the revision it read.
test('journey policies pick structural step paths, save revisions, refuse stale edits and show past revisions', async ({ page }) => {
  test.setTimeout(120_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  await json(await request.put(`/api/v1/projects/${workspace.projectId}/analysis-settings`, {
    data: { expected_revision: 0, root_cause: { enabled: true, present: true, auto_analyze: false } },
  }))
  const scope = `project_id=${encodeURIComponent(workspace.projectId)}`
  const scriptId = idOf(await json(await request.post('/api/v1/scripts', {
    data: {
      project_id: workspace.projectId,
      script: {
        name: `Journey_Steps_${Date.now()}`,
        version: '1.0.0',
        protocol: 'browser',
        config: { browser: { engine: 'chromium', headless: true } },
        actions: [
          { name: 'Submit', type: 'browser_click', selector: '#start' },
          { name: 'Wait', type: 'browser_wait', wait_for: { type: 'duration', value: '50ms', timeout_ms: 1000 } },
          { name: 'Submit', type: 'browser_click', selector: '#finish' },
        ],
      },
    },
  })))

  await page.goto(`/#/settings?setting=analysis-policies&${scopeQuery(workspace)}`)
  await page.reload()
  const panel = page.getByRole('region', { name: 'Browser and journey policies', exact: true })
  await panel.getByRole('tab', { name: 'Journey policies', exact: true }).click()
  await expect(panel).toContainText('This project has no journey policies yet.')

  await panel.getByRole('button', { name: 'New journey policy', exact: true }).click()
  const editor = panel.getByRole('form', { name: 'New journey policy', exact: true })
  await editor.getByLabel('Name', { exact: true }).fill('Checkout journeys')
  await editor.getByLabel('Journey 1 name', { exact: true }).fill('Submit flow')
  await editor.getByLabel('Pick journey 1 start step from script', { exact: true }).selectOption(`${scriptId}\nactions[0]\naction`)
  await editor.getByLabel('Pick journey 1 success step from script', { exact: true }).selectOption(`${scriptId}\nactions[2]\naction`)
  await expect(editor.getByLabel('Journey 1 start', { exact: true })).toHaveValue('Submit')
  await expect(editor.getByLabel('Journey 1 start step path', { exact: true })).toHaveValue('actions[0]')
  await expect(editor.getByLabel('Journey 1 success', { exact: true })).toHaveValue('Submit')
  await expect(editor.getByLabel('Journey 1 success step path', { exact: true })).toHaveValue('actions[2]')
  await editor.getByRole('button', { name: 'Save policy', exact: true }).click()

  const table = panel.getByRole('table', { name: 'Journey policies', exact: true })
  await expect(table.getByRole('row', { name: /Checkout journeys/ })).toContainText('1')
  const [stored] = await json(await request.get(`/api/v1/analysis/policies?${scope}&kind=journey_policy`)) as unknown as Array<{
    id: string
    revision: number
    name: string
    definition: { journeys: Array<{ id: string; name: string; start: Record<string, unknown>; success: Record<string, unknown>; weight: number }> }
  }>
  expect(stored).toMatchObject({
    name: 'Checkout journeys',
    revision: 1,
    definition: {
      journeys: [{
        id: 'journey-1',
        name: 'Submit flow',
        start: { action: 'Submit', step_path: 'actions[0]' },
        success: { action: 'Submit', step_path: 'actions[2]' },
        weight: 1,
      }],
    },
  })

  // Concurrent update to revision 2 while the editor holds revision 1.
  await table.getByRole('button', { name: 'Edit', exact: true }).click()
  const edit = panel.getByRole('form', { name: 'Edit journey policy', exact: true })
  await expect(edit).toContainText('(revision 1)')
  await json(await request.put(`/api/v1/analysis/policies/${stored.id}?${scope}`, {
    data: {
      kind: 'journey_policy',
      name: 'Checkout journeys (API)',
      revision: 1,
      definition: {
        ...stored.definition,
        journeys: [{ ...stored.definition.journeys[0], weight: 3 }],
      },
    },
  }))
  await edit.getByLabel('Journey 1 weight', { exact: true }).fill('2')
  await edit.getByRole('button', { name: 'Save policy', exact: true }).click()
  await expect(edit).toContainText('This policy changed since you opened it')
  await edit.getByRole('button', { name: 'Load the current revision', exact: true }).click()
  await expect(edit).toContainText('(revision 2)')
  await expect(edit.getByLabel('Name', { exact: true })).toHaveValue('Checkout journeys (API)')
  await expect(edit.getByLabel('Journey 1 weight', { exact: true })).toHaveValue('3')
  await edit.getByLabel('Journey 1 weight', { exact: true }).fill('4')
  await edit.getByRole('button', { name: 'Save policy', exact: true }).click()
  await expect(table.getByRole('row', { name: /Checkout journeys \(API\)/ })).toContainText('3')

  const current = await json(await request.get(`/api/v1/analysis/policies/${stored.id}?${scope}`)) as unknown as {
    revision: number
    definition: { journeys: Array<{ weight: number; start: { step_path?: string }; success: { step_path?: string } }> }
  }
  expect(current.revision).toBe(3)
  expect(current.definition.journeys[0].weight).toBe(4)
  expect(current.definition.journeys[0].start.step_path).toBe('actions[0]')
  expect(current.definition.journeys[0].success.step_path).toBe('actions[2]')

  // Past revision 1 remains unchanged and read-only.
  await table.getByRole('button', { name: 'View a revision', exact: true }).click()
  const revision = panel.getByRole('region', { name: 'Policy revision', exact: true })
  await revision.getByLabel('Revision', { exact: true }).fill('1')
  await revision.getByRole('button', { name: 'Show', exact: true }).click()
  await expect(revision).toContainText('Revision 1')
  await expect(revision).toContainText('"step_path": "actions[0]"')
  await expect(revision).toContainText('"step_path": "actions[2]"')
  await expect(revision).toContainText('"weight": 1')

  await table.getByRole('button', { name: 'Delete', exact: true }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Delete policy', exact: true }).click()
  await expect(panel).toContainText('This project has no journey policies yet.')
})

// BI-C5: unstubbed full chain with two real managed Chromium runs, 30 s
// provisional live intelligence (BI-C1), finalized browser_findings.v2 with RCA
// latest_compatible selection provenance (BI-A3/BI-A4/BI-C4), finding ->
// document -> redacted resource -> native HAR/ZIP drill-down (BE-B6), two-run
// baseline comparison -> saved v2 panel + policy deletion refusal (BI-C3),
// report exports (BI-C4), and viewer RBAC + cross-project denial in local-auth.
test('real managed Chromium runs drive live provisional intelligence, finalized findings, HAR drill-down, comparison panels, exports and viewer access', async ({ page, trackRun }) => {
  test.setTimeout(300_000)
  await page.addInitScript(() => localStorage.setItem('apexload.featureFlags.runReport', 'true'))
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  await json(await request.put(`/api/v1/projects/${workspace.projectId}/analysis-settings`, {
    data: { expected_revision: 0, root_cause: { enabled: true, present: true, auto_analyze: false } },
  }))
  const scope = `project_id=${encodeURIComponent(workspace.projectId)}`

  const server = createServer((req, res) => {
    const url = new URL(req.url || '/', 'http://127.0.0.1')
    res.setHeader('Cache-Control', 'no-store')
    if (url.pathname === '/api/order') {
      res.setHeader('Content-Type', 'application/json')
      res.end('{"ordered":true}')
      return
    }
    res.setHeader('Content-Type', 'text/html; charset=utf-8')
    res.end('<!doctype html><html><head><title>Full Chain Store</title></head><body>'
      + '<h1>Full Chain Checkout</h1>'
      + '<button id="buy" type="button">Buy now</button>'
      + '<div id="receipt">pending</div>'
      + '<script src="/api/order?token=secret-full-chain-token"></script>'
      + '<script>document.getElementById("buy").addEventListener("click",function(){'
      + 'fetch("/api/order?token=secret-full-chain-token").then(function(){document.getElementById("receipt").textContent="confirmed";});'
      + '});</script></body></html>')
  })
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  const origin = `http://127.0.0.1:${(server.address() as AddressInfo).port}`

  try {
    const scriptId = idOf(await json(await request.post('/api/v1/scripts', {
      data: {
        project_id: workspace.projectId,
        script: {
          name: `Full_Chain_Browser_${Date.now()}`,
          version: '1.0.0',
          protocol: 'browser',
          config: {
            pacing: { type: 'fixed_interval', interval_ms: 4000 },
            browser: {
              engine: 'chromium',
              headless: true,
              capture_document_metrics: true,
              client_metrics: true,
              capture_interactions: true,
              har_capture: 'always',
            },
          },
          actions: [
            { name: 'Open store', type: 'browser_navigate', target: `${origin}/page` },
            { name: 'Place order', type: 'browser_click', selector: '#buy' },
            { name: 'Await receipt', type: 'browser_wait', selector: '#receipt', wait_for: { type: 'text', value: 'confirmed', timeout_ms: 5000 } },
          ],
        },
      },
    })))

    const browserPolicy = await json(await request.post(`/api/v1/analysis/policies?${scope}`, {
      data: {
        kind: 'browser_policy',
        name: 'Full chain vitals',
        definition: {
          version: 1,
          apply_by_default: true,
          baseline: { mode: 'latest_compatible' },
          metrics: [
            { metric: 'lcp', good: 0.1, poor: 0.5, weight: 2 },
            { metric: 'fcp', good: 0.1, poor: 0.5, weight: 1 },
          ],
        },
      },
    })) as { id: string; revision: number }

    const journeyPolicy = await json(await request.post(`/api/v1/analysis/policies?${scope}`, {
      data: {
        kind: 'journey_policy',
        name: 'Full chain checkout journey',
        definition: {
          version: 1,
          apply_by_default: true,
          step_risk_enabled: true,
          journeys: [{
            id: 'checkout',
            name: 'Checkout',
            start: { action: 'Open store', step_path: 'actions[0]' },
            success: { action: 'Await receipt', step_path: 'actions[2]' },
            weight: 1,
          }],
        },
      },
    })) as { id: string; revision: number }

    const scenarioId = idOf(await json(await request.post('/api/v1/scenarios', {
      data: {
        project_id: workspace.projectId,
        application_id: workspace.applicationId,
        name: 'Full chain browser scenario',
        termination_mode: 'duration',
        duration: '35s',
        groups: [{ name: 'checkout', script_id: scriptId, vusers: 1, ramp_up: '0s' }],
      },
    })))

    // Run 1: real Chromium baseline run, pinned for comparison.
    const baselineRunId = idOf(await json(await request.post('/api/v1/tests', {
      data: {
        project_id: workspace.projectId,
        application_id: workspace.applicationId,
        release_id: workspace.releaseId,
        scenario_id: scenarioId,
        name: 'Full chain baseline run',
        duration: '6s',
        groups: [{ name: 'checkout', script_id: scriptId, vusers: 1, ramp_up: '0s' }],
      },
    })))
    trackRun(baselineRunId)
    await json(await request.post(`/api/v1/tests/${baselineRunId}/start`))
    await expect.poll(() => runStatus(request, baselineRunId), { timeout: 90_000 }).toBe('COMPLETE')
    await expect.poll(async () => (await request.get(`/api/v1/tests/${baselineRunId}/archive/manifest`)).ok(), { timeout: 60_000 }).toBe(true)
    const pinResp = await request.post(`/api/v1/tests/${baselineRunId}/comparison-baseline/pin`)
    expect(pinResp.ok(), await pinResp.text()).toBeTruthy()

    // Run 2: real Chromium candidate run (35 s) to observe the 30 s provisional live sample (BI-C1).
    const candidateRunId = idOf(await json(await request.post('/api/v1/tests', {
      data: {
        project_id: workspace.projectId,
        application_id: workspace.applicationId,
        release_id: workspace.releaseId,
        scenario_id: scenarioId,
        name: 'Full chain candidate run',
        duration: '35s',
        groups: [{ name: 'checkout', script_id: scriptId, vusers: 1, ramp_up: '0s' }],
      },
    })))
    trackRun(candidateRunId)
    await json(await request.post(`/api/v1/tests/${candidateRunId}/start`))

    // Wait for first provisional live sample while the run is RUNNING.
    let firstSamples = 0
    await expect.poll(async () => {
      const fResp = await request.get(`/api/v1/tests/${candidateRunId}/archive/browser-findings`)
      const iResp = await request.get(`/api/v1/tests/${candidateRunId}/archive/browser-interactions`)
      const jResp = await request.get(`/api/v1/tests/${candidateRunId}/archive/journeys`)
      if (!fResp.ok() || !iResp.ok() || !jResp.ok()) return false
      const f = await fResp.json() as { provisional?: boolean; status?: string; result?: { provisional?: boolean; score?: { components?: Array<{ samples: number }> }; findings?: Array<{ baseline?: { status?: string } }> } }
      const i = await iResp.json() as { provisional?: boolean; status?: string; coverage?: { interactions?: number } }
      const j = await jResp.json() as { provisional?: boolean; availability?: string; journeys?: Array<{ completed: number; match_mode?: string }> }
      const samples = f.result?.score?.components?.[0]?.samples || 0
      if (
        f.provisional === true &&
        f.result?.provisional === true &&
        f.result?.findings?.some(item => item.baseline?.status === 'not_evaluated_live') &&
        i.provisional === true &&
        i.status === 'ready' &&
        (i.coverage?.interactions || 0) >= 1 &&
        j.provisional === true &&
        j.availability === 'available' &&
        (j.journeys?.[0]?.completed || 0) >= 1 &&
        j.journeys?.[0]?.match_mode === 'step_path' &&
        samples >= 1
      ) {
        firstSamples = samples
        return true
      }
      return false
    }, { timeout: 30_000 }).toBe(true)

    // Verify the Command Center renders the provisional live browser intelligence view and advances across the 30 s cadence.
    await page.goto(`/#/command-center/${candidateRunId}?${scopeQuery(workspace)}`)
    const browserGroups = page.getByRole('region', { name: 'Browser groups', exact: true })
    await expect(browserGroups).toContainText('Live browser intelligence (provisional — non-verdict)', { timeout: 30_000 })
    await expect(browserGroups.getByTestId('live-browser-findings-summary')).toContainText('baseline not evaluated live')
    await expect(browserGroups.getByTestId('live-browser-interactions-summary')).toContainText('browser_interaction_patterns.v4')
    await expect(browserGroups.getByTestId('live-browser-journeys-summary')).toContainText('Checkout:')

    // Wait for the 30 s ticker / later iterations to advance provisional sample count before completion.
    await expect.poll(async () => {
      const fResp = await request.get(`/api/v1/tests/${candidateRunId}/archive/browser-findings`)
      if (!fResp.ok()) return 0
      const f = await fResp.json() as { result?: { score?: { components?: Array<{ samples: number }> } } }
      return f.result?.score?.components?.[0]?.samples || 0
    }, { timeout: 35_000 }).toBeGreaterThan(firstSamples)

    // Wait for Run 2 to complete and finalize its archive.
    await expect.poll(() => runStatus(request, candidateRunId), { timeout: 90_000 }).toBe('COMPLETE')
    await expect.poll(async () => (await request.get(`/api/v1/tests/${candidateRunId}/archive/manifest`)).ok(), { timeout: 60_000 }).toBe(true)

    // Trigger RCA with latest_compatible so browser_selection.v1 inspects baselineRunId and refreshes browser_findings.v2.
    let rcaAnalysisId = ''
    await expect.poll(async () => {
      const res = await request.post(`/api/v1/tests/${candidateRunId}/archive/root-cause/analyses`, { data: {} })
      if (!res.ok()) return ''
      const body = await res.json() as { analysis_id?: string }
      rcaAnalysisId = String(body.analysis_id || '')
      return rcaAnalysisId
    }, { timeout: 30_000 }).not.toBe('')
    await expect.poll(async () => {
      const res = await request.get(`/api/v1/tests/${candidateRunId}/archive/root-cause/analyses/${rcaAnalysisId}`)
      if (!res.ok()) return ''
      const body = await res.json() as { state?: string }
      return String(body.state || '')
    }, { timeout: 60_000 }).toBe('completed')

    // Wait for finalized browser_findings.v2 to carry the RCA selection receipt and policy provenance (and no longer be provisional).
    let worstDocId = ''
    let selectionReceiptSha = ''
    await expect.poll(async () => {
      const res = await request.get(`/api/v1/tests/${candidateRunId}/archive/browser-findings`)
      if (!res.ok()) return false
      const body = await res.json() as {
        provisional?: boolean
        status?: string
        result?: {
          provisional?: boolean
          rule_set?: string
          policy?: { id: string; revision: number; sha256: string }
          findings?: Array<{
            metric?: string
            state?: string
            severity?: string
            evidence?: { documents?: Array<{ document_id: string }> }
            baseline?: {
              analysis_id?: string
              selection_receipt_sha256?: string
              selection_status?: string
              policy_pins?: Array<{ kind: string; id: string; revision: number }>
            }
          }>
        }
      }
      if (body.provisional || body.result?.provisional || body.status !== 'ready' || body.result?.rule_set !== 'browser_findings.v2') return false
      const lcpFinding = body.result.findings?.find(item => item.metric === 'lcp' && item.state === 'active')
      if (!lcpFinding) return false
      const docId = lcpFinding.evidence?.documents?.[0]?.document_id || ''
      const receiptSha = lcpFinding.baseline?.selection_receipt_sha256 || ''
      if (
        docId &&
        /^[a-f0-9]{64}$/.test(receiptSha) &&
        lcpFinding.baseline?.analysis_id === rcaAnalysisId &&
        Boolean(lcpFinding.baseline?.selection_status) &&
        (lcpFinding.baseline?.policy_pins?.length || 0) >= 2
      ) {
        worstDocId = docId
        selectionReceiptSha = receiptSha
        return true
      }
      return false
    }, { timeout: 60_000 }).toBe(true)

    // Inspect the finalized Browser report view (unstubbed): comparison, findings, baseline selection, interactions, journeys, and document -> resource -> native HAR/ZIP drill-down.
    await page.goto(`/#/report/${candidateRunId}?section=browser&${scopeQuery(workspace)}`)
    await page.reload()
    await expect(page.getByRole('tab', { name: 'Browser', exact: true })).toHaveAttribute('aria-selected', 'true')
    const docTable = page.getByRole('table', { name: 'Browser document comparison', exact: true })
    await expect(docTable).toContainText('chromium')
    await expect(docTable).toContainText('checkout')

    const findingsPanel = page.getByRole('region', { name: 'Browser findings', exact: true })
    await expect(findingsPanel).not.toHaveAttribute('data-provisional', 'true')
    await expect(findingsPanel).toContainText(`Full chain vitals revision ${browserPolicy.revision}`)
    await expect(findingsPanel).toContainText(`selection receipt …${selectionReceiptSha.slice(-8)}`)

    const baselinePanel = page.getByRole('region', { name: 'Browser baseline', exact: true })
    await expect(baselinePanel).toContainText('browser_selection.v1')
    await expect(baselinePanel).toContainText(selectionReceiptSha.slice(-8))

    const interactionsPanel = page.getByRole('region', { name: 'Interaction patterns', exact: true })
    await expect(interactionsPanel).toBeVisible()
    await expect(interactionsPanel).not.toHaveAttribute('data-provisional', 'true')

    const journeyPanel = page.getByRole('region', { name: 'Journey risk', exact: true })
    await expect(journeyPanel).toBeVisible()
    await expect(journeyPanel).not.toHaveAttribute('data-provisional', 'true')
    await expect(journeyPanel).toContainText(`Full chain checkout journey revision ${journeyPolicy.revision}`)

    // Drill down from the active finding's worst document to its retained resources and native HAR entry.
    await findingsPanel.getByRole('button', { name: `Inspect the resources of document …${worstDocId.slice(-8)}`, exact: true }).first().click()
    await expect(page.getByRole('status').filter({ hasText: `the retained resources of document …${worstDocId.slice(-8)}` })).toBeVisible()
    const inspector = page.getByRole('region', { name: 'Retained archive resource inspector', exact: true })
    const resourceTable = inspector.getByRole('region', { name: 'Retained archive resources table', exact: true })
    await expect(resourceTable).toContainText(`${origin}/api/order?token=[REDACTED]`)
    await expect(resourceTable).not.toContainText('secret-full-chain-token')
    const orderRow = resourceTable.getByRole('row').filter({ hasText: `${origin}/api/order?token=[REDACTED]` }).first()
    await orderRow.locator('summary').click()
    await orderRow.getByRole('button', { name: "Show this request's HAR entry", exact: true }).click()
    await expect(orderRow).toContainText("This request's browser context recorded a native HAR")
    const entryHarDownload = page.waitForEvent('download')
    await orderRow.getByRole('button', { name: 'Download that HAR', exact: true }).click()
    const entryHarRaw = await readFile((await (await entryHarDownload).path())!, 'utf8')
    expect(entryHarRaw).not.toContain('secret-full-chain-token')
    const entryHar = JSON.parse(entryHarRaw) as { log: { version: string; entries: Array<{ request: { url: string } }> } }
    expect(entryHar.log.version).toBe('1.2')
    expect(entryHar.log.entries.map(item => item.request.url)).toContain(`${origin}/api/order?token=[REDACTED]`)

    const harPanel = page.getByRole('region', { name: 'Browser HARs', exact: true })
    const zipDownload = page.waitForEvent('download')
    await harPanel.getByRole('button', { name: 'Download archive with native HARs', exact: true }).click()
    expect((await zipDownload).suggestedFilename()).toContain(candidateRunId)

    const harList = await json(await request.get(`/api/v1/tests/${candidateRunId}/archive/browser/har`)) as { artifacts: Array<{ name: string; fidelity: string }> }
    const nativeArtifact = harList.artifacts.find(item => item.fidelity === 'native')!
    expect(nativeArtifact).toBeTruthy()

    // Two-run baseline comparison -> Save to panel (query_version: 5, version: 2 BrowserPanelInputs with policy pins).
    await page.goto(`/#/report/${candidateRunId}?section=comparison&${scopeQuery(workspace)}`)
    await page.reload()
    await expect(page.getByRole('tab', { name: 'Baseline comparison', exact: true })).toHaveAttribute('aria-selected', 'true')
    await expect(page.getByText('1/5 baselines', { exact: true })).toBeVisible()
    await page.getByRole('button', { name: 'Compare selected baselines', exact: true }).click()
    await expect(page.getByRole('region', { name: 'Share comparison', exact: true })).toBeVisible({ timeout: 60_000 })
    const shareRegion = page.getByRole('region', { name: 'Share comparison', exact: true })
    await shareRegion.getByLabel('Panel title', { exact: true }).fill('Full chain v2 browser comparison panel')
    const createPanelResp = page.waitForResponse(res => res.request().method() === 'POST' && new URL(res.url()).pathname === '/api/v1/monitoring/dashboard-definitions')
    await shareRegion.getByRole('button', { name: 'Save to panel', exact: true }).click()
    const savedPanel = await json(await createPanelResp) as {
      id: string
      revision: number
      tabs: Array<{ panels: Array<{ query: Record<string, unknown> & { query_version: number; browser_inputs: { version: number; query_sha256: string; current: { run_id: string }; baseline?: { run_id: string }; policy_pins?: Array<{ kind: string; id: string; revision: number }> } } }> }>
    }
    const savedPanelQuery = savedPanel.tabs[0].panels[0].query
    expect(savedPanelQuery.query_version).toBe(5)
    expect(savedPanelQuery.browser_inputs.version).toBe(2)
    expect(savedPanelQuery.browser_inputs.current.run_id).toBe(candidateRunId)
    expect(savedPanelQuery.browser_inputs.baseline?.run_id).toBe(baselineRunId)
    expect(savedPanelQuery.browser_inputs.policy_pins).toEqual(expect.arrayContaining([
      expect.objectContaining({ kind: 'browser_policy', id: browserPolicy.id, revision: browserPolicy.revision }),
      expect.objectContaining({ kind: 'journey_policy', id: journeyPolicy.id, revision: journeyPolicy.revision }),
    ]))
    await expect(shareRegion).toContainText(`Saved panel: ${savedPanel.id}`)

    // Single-run query_version: 5 native-series preview also binds the exact RCA selection_receipt_sha256.
    const singleRunV5 = await json(await request.post(`/api/v1/tests/${candidateRunId}/archive/native-series`, {
      data: { ...savedPanelQuery, comparison_run_id: undefined, browser_inputs: undefined },
    })) as { browser_inputs?: { version: number; selection_receipt_sha256?: string } }
    expect(singleRunV5.browser_inputs?.version).toBe(2)
    expect(singleRunV5.browser_inputs?.selection_receipt_sha256).toBe(selectionReceiptSha)

    // Report exports (HTML, CSV, PDF, DOCX, plus interaction CSV) carry v2 provenance, interactions, and journeys.
    const csvResp = await request.get(`/api/v1/tests/${candidateRunId}/archive/report?format=csv&profile=engineer`)
    expect(csvResp.status()).toBe(200)
    const csvText = await csvResp.text()
    for (const token of ['browser_findings', 'interaction_map', 'journey_risk', selectionReceiptSha, browserPolicy.id, journeyPolicy.id]) {
      expect(csvText).toContain(token)
    }
    const htmlResp = await request.get(`/api/v1/tests/${candidateRunId}/report?profile=engineer`)
    expect(htmlResp.status()).toBe(200)
    const htmlText = await htmlResp.text()
    expect(htmlText).toContain(selectionReceiptSha.slice(-8))
    expect(htmlText).toContain(browserPolicy.id)
    for (const format of ['pdf', 'docx']) {
      const binResp = await request.get(`/api/v1/tests/${candidateRunId}/archive/report?format=${format}&profile=engineer`)
      expect(binResp.status()).toBe(200)
      expect((await binResp.body()).byteLength).toBeGreaterThan(0)
    }
    const interactionsCsvResp = await request.get(`/api/v1/tests/${candidateRunId}/archive/browser-interactions?format=csv`)
    expect(interactionsCsvResp.status()).toBe(200)
    expect(await interactionsCsvResp.text()).toContain('browser_interaction_patterns.v4')

    // Viewer RBAC & cross-project denial in local-auth.
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') {
      const otherWorkspace = await createWorkspace(request)
      const users = (await json(await request.get('/api/v1/auth/users'))).users as Array<{ id: string; subject: string }>
      const viewer = users.find(user => user.subject === 'local:user')!
      await json(await request.post(`/api/v1/auth/users/${viewer.id}/bindings`, {
        data: { scope_type: 'domain', scope_id: workspace.domainId, role: 'viewer' },
      }))

      await signOut(page)
      await login(page, 'user')
      const viewerRequest = page.context().request
      try {
        // Cross-project denial: viewer has no binding in otherWorkspace.
        expect((await viewerRequest.get(`/api/v1/analysis/policies?project_id=${encodeURIComponent(otherWorkspace.projectId)}`)).status()).toBe(403)
        expect((await viewerRequest.post(`/api/v1/analysis/evaluations?project_id=${encodeURIComponent(otherWorkspace.projectId)}`, {
          data: { kind: 'browser_intelligence', request: { run: candidateRunId } },
        })).status()).toBe(403)
        expect((await viewerRequest.post('/api/v1/tests', {
          data: {
            project_id: otherWorkspace.projectId,
            application_id: otherWorkspace.applicationId,
            release_id: otherWorkspace.releaseId,
            name: 'Cross-project denied run',
            duration: '1s',
            groups: [],
          },
        })).status()).toBe(403)

        // Viewer can inspect redacted Browser report, resources, HAR entry, and download report exports.
        await page.goto(`/#/report/${candidateRunId}?section=browser&${scopeQuery(workspace)}`)
        await page.reload()
        const viewerFindings = page.getByRole('region', { name: 'Browser findings', exact: true })
        await expect(viewerFindings).toContainText(`Full chain vitals revision ${browserPolicy.revision}`)
        await expect(viewerFindings.getByRole('button', { name: 'Evaluate with the pinned policy' })).toHaveCount(0)
        await viewerFindings.getByRole('button', { name: `Inspect the resources of document …${worstDocId.slice(-8)}`, exact: true }).first().click()
        const viewerInspector = page.getByRole('region', { name: 'Retained archive resource inspector', exact: true })
        await expect(viewerInspector.getByRole('region', { name: 'Retained archive resources table', exact: true })).toContainText(`${origin}/api/order?token=[REDACTED]`)
        const viewerHarPanel = page.getByRole('region', { name: 'Browser HARs', exact: true })
        await expect(viewerHarPanel).toContainText('Downloading a HAR needs editor access')
        await expect(viewerHarPanel.getByRole('button', { name: 'Download native HAR', exact: true })).toBeDisabled()

        expect((await viewerRequest.get(`/api/v1/tests/${candidateRunId}/archive/report?format=csv&profile=engineer`)).status()).toBe(200)
        expect((await viewerRequest.get(`/api/v1/tests/${candidateRunId}/report?profile=engineer`)).status()).toBe(200)
        expect((await viewerRequest.get(`/api/v1/tests/${candidateRunId}/archive/browser-interactions?format=csv`)).status()).toBe(200)

        // Raw native HAR and HAR-inclusive archive ZIP return 403 for Viewer.
        expect((await viewerRequest.get(`/api/v1/tests/${candidateRunId}/archive/browser/har/${encodeURIComponent(nativeArtifact.name)}`)).status()).toBe(403)
        expect((await viewerRequest.get(`/api/v1/tests/${candidateRunId}/archive?include=har`)).status()).toBe(403)

        // Viewer write attempts are denied (403) and policy editor controls are hidden.
        expect((await viewerRequest.post(`/api/v1/analysis/policies?${scope}`, {
          data: { kind: 'browser_policy', name: 'Denied viewer policy', definition: { version: 1, baseline: { mode: 'none' }, metrics: [{ metric: 'lcp', good: 2500, poor: 4000, weight: 1 }] } },
        })).status()).toBe(403)
        expect((await viewerRequest.delete(`/api/v1/analysis/policies/${browserPolicy.id}?${scope}&revision=${browserPolicy.revision}`)).status()).toBe(403)
        expect((await viewerRequest.post(`/api/v1/tests/${candidateRunId}/archive/root-cause/analyses`, { data: {} })).status()).toBe(403)
        expect((await viewerRequest.post('/api/v1/monitoring/dashboard-definitions', {
          data: { project_id: workspace.projectId, name: 'Denied viewer panel', visibility: 'project', tabs: savedPanel.tabs },
        })).status()).toBe(403)

        await page.goto(`/#/settings?setting=analysis-policies&${scopeQuery(workspace)}`)
        await page.reload()
        await expect(page.getByRole('main', { name: 'Settings', exact: true })).toContainText('The requested setting requires administrator access.')
        await expect(page.getByRole('region', { name: 'Browser and journey policies', exact: true })).toHaveCount(0)
      } finally {
        await signOut(page)
        await login(page)
      }
    }

    // Deleting the pinned browser policy makes the saved v2 panel refuse replay with 422 and show unavailable in UI.
    const delPolicy = await request.delete(`/api/v1/analysis/policies/${browserPolicy.id}?${scope}&revision=${browserPolicy.revision}`)
    expect(delPolicy.ok(), await delPolicy.text()).toBeTruthy()
    const tombstonedReplay = await request.post(`/api/v1/tests/${candidateRunId}/archive/native-series`, { data: savedPanelQuery })
    expect(tombstonedReplay.status()).toBe(422)
    expect(await tombstonedReplay.text()).toContain('pinned analysis policy has been deleted')

    await page.goto(`/#/report/${candidateRunId}?section=performance&${scopeQuery(workspace)}`)
    await page.reload()
    await page.getByRole('combobox', { name: 'Project dashboard', exact: true }).selectOption(savedPanel.id)
    await expect(page.getByRole('alert').filter({ hasText: 'Archive panel unavailable' })).toBeVisible()
  } finally {
    server.closeAllConnections()
    await new Promise<void>((resolve, reject) => server.close(err => err ? reject(err) : resolve()))
  }
})
