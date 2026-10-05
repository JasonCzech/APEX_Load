import { readFile } from 'node:fs/promises'
import type { Locator, Page } from '@playwright/test'
import { test, expect, seedScript, json, idOf, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'
test('browser resource evidence filters retained failures and inspects action attribution', async ({ page, trackRun }) => {
 if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
 const request=page.context().request;const workspace=await createWorkspace(request);const script=await seedScript(request,workspace)
 const id=idOf(await json(await request.post('/api/v1/tests',{data:{project_id:workspace.projectId,application_id:workspace.applicationId,release_id:workspace.releaseId,name:'Resource inspection run',duration:'1s',groups:[{name:'browser',script_id:script,vusers:1}]}})));trackRun(id)
 await page.route(`**/api/v1/tests/${id}`,async route=>{
 if(route.request().method()!=='GET')return route.continue()
 const response=await route.fetch();const body=await response.json()
 return route.fulfill({response,json:{...body,status:'COMPLETE',archive:{transaction_count:1},started_at:'2026-10-01T00:00:00Z',ended_at:'2026-10-01T00:00:01Z'}})
 })
 await page.route(`**/api/v1/tests/${id}/archive/report*`,route=>route.fulfill({json:{manifest:{test_id:id,test_name:'Resource inspection run',status:'COMPLETE',provenance:{started_at:'2026-10-01T00:00:00Z',duration:'1s',total_vusers:1}},overview:{transactions:1},by_action:{},browser_resources:[{timestamp:'2026-10-01T00:00:01Z',action_name:'Navigate',url:'https://example.test/ok.js',duration_ms:0,bytes_received:0,status_code:200},{timestamp:'2026-10-01T00:00:01Z',action_name:'Click',page_url:'https://example.test/page',url:'https://example.test/fail.js',failure_kind:'transport',method:'GET'},{timestamp:'2026-10-01T00:00:01Z',action_name:'Click',url:'https://example.test/service.js',status_code:503,failure_kind:'http',method:'GET'}]}}))
 await page.goto(`/#/report/${id}?section=diagnostics&${scopeQuery(workspace)}`);await page.reload()
 await page.getByRole('tab',{name:'Diagnostics',exact:true}).click()
 await page.getByText('Browser evidence',{exact:true}).click()
 const resources=page.getByRole('region',{name:'Browser resource evidence',exact:true})
 await expect(resources).toContainText('0.0 ms');await expect(resources).toContainText('0 B')
 await resources.getByLabel('Failed resources only').check();await expect(resources).toContainText('2 of 3 retained resources');await expect(resources).not.toContainText('ok.js');await expect(resources).toContainText('503 · http');await expect(resources).toContainText('transport')
 await resources.getByText('Inspect resource 1',{exact:true}).click();await expect(resources).toContainText('https://example.test/page');await expect(resources).toContainText('GET')
 await resources.getByLabel('Resource action',{exact:true}).selectOption('Navigate');await expect(resources).toContainText('No retained resources match')
 await resources.screenshot({path:test.info().outputPath('resource-evidence.png')})
})


// Polling route handlers must drain before the owned fixture context closes.
test.afterEach(async ({ page }) => { await page.unrouteAll({ behavior: 'wait' }) })

const explicitZeroSummary = { samples: 1, avg_lcp_ms: 0, avg_inp_ms: 0, avg_cls_score: 0, avg_long_task_count: 0, avg_blocking_resources: 0, avg_browser_process_cpu_pct: 0, avg_browser_process_mem_mb: 0 }


async function waitForFiniteLayout(page: Page) {
  await page.evaluate(async () => {
    await Promise.all(document.getAnimations().filter(animation => Number.isFinite(animation.effect?.getComputedTiming().endTime))
      .map(animation => animation.finished.catch(() => undefined)))
  })
}

async function captureReadableAssessment(page: Page, assessment: Locator, filename: string) {
  // Retain the original narrow viewport/whole-box checks. The region may be
  // taller than this viewport, so use a taller canvas only for its artifact.
  await page.setViewportSize({ width: 760, height: 900 })
  await waitForFiniteLayout(page)
  await assessment.scrollIntoViewIfNeeded()
  const geometry = await assessment.evaluate(node => { const rect = node.getBoundingClientRect(); return { left: rect.left, right: rect.right, width: window.innerWidth, scrollWidth: node.scrollWidth, clientWidth: node.clientWidth } })
  expect(geometry.left).toBeGreaterThanOrEqual(0)
  expect(geometry.right).toBeLessThanOrEqual(geometry.width)
  expect(geometry.scrollWidth).toBeLessThanOrEqual(geometry.clientWidth + 1)
  await page.setViewportSize({ width: 760, height: 1600 })
  await waitForFiniteLayout(page)
  const positioning = await assessment.evaluate(node => {
    const owner = node.closest('.main-content') as HTMLElement | null
    if (!owner) throw new Error('Browser assessment has no known main scroll owner')
    const header = document.querySelector('.main-topbar')
    const headerBottom = header?.getBoundingClientRect().bottom ?? 0
    const before = node.getBoundingClientRect()
    owner.scrollTop += before.top - headerBottom - 16
    return { beforeTop: before.top, headerBottom, scrollOwner: owner.className }
  })
  await test.info().attach(`${filename}-scroll-position`, { body: JSON.stringify(positioning), contentType: 'application/json' })
  const heading = assessment.getByRole('heading')
  const hit = async () => heading.evaluate(node => { const rect = node.getBoundingClientRect(); const hit = document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2); return { hit: hit === node || node.contains(hit), top: rect.top, bottom: rect.bottom } })
  await expect.poll(async () => (await hit()).hit).toBe(true)
  expect((await hit()).top).toBeGreaterThanOrEqual(positioning.headerBottom)
  const captureGeometry = () => assessment.evaluate(node => {
    const rect = node.getBoundingClientRect()
    const ancestors = []
    for (let parent = node.parentElement; parent; parent = parent.parentElement) {
      const box = parent.getBoundingClientRect()
      const style = getComputedStyle(parent)
      ancestors.push({ tag: parent.tagName, className: parent.className, left: box.left, right: box.right, scrollLeft: parent.scrollLeft, scrollWidth: parent.scrollWidth, clientWidth: parent.clientWidth, clientLeft: parent.clientLeft, overflowX: style.overflowX })
    }
    return { left: rect.left, right: rect.right, viewport: window.innerWidth, ancestors }
  })
  const capturedGeometry = await captureGeometry()
  await test.info().attach(`${filename}-capture-geometry`, { body: JSON.stringify(capturedGeometry), contentType: 'application/json' })
  expect(capturedGeometry.left).toBeGreaterThanOrEqual(0)
  expect(capturedGeometry.right).toBeLessThanOrEqual(capturedGeometry.viewport)
  for (const ancestor of capturedGeometry.ancestors.filter(ancestor => ancestor.overflowX !== 'visible')) {
    expect(capturedGeometry.left, `Card left edge clipped by ${ancestor.className}`).toBeGreaterThanOrEqual(ancestor.left + ancestor.clientLeft - 1)
    expect(capturedGeometry.right, `Card right edge clipped by ${ancestor.className}`).toBeLessThanOrEqual(ancestor.left + ancestor.clientLeft + ancestor.clientWidth + 1)
  }
  await assessment.screenshot({ path: test.info().outputPath(filename) })
  await expect.poll(async () => (await hit()).hit).toBe(true)
  const afterCapture = await captureGeometry()
  await test.info().attach(`${filename}-after-capture-geometry`, { body: JSON.stringify(afterCapture), contentType: 'application/json' })
  expect(afterCapture.left).toBeGreaterThanOrEqual(0)
  expect(afterCapture.right).toBeLessThanOrEqual(afterCapture.viewport)
  for (const ancestor of afterCapture.ancestors.filter(ancestor => ancestor.overflowX !== 'visible')) {
    expect(afterCapture.left, `Capture left edge clipped by ${ancestor.className}`).toBeGreaterThanOrEqual(ancestor.left + ancestor.clientLeft - 1)
    expect(afterCapture.right, `Capture right edge clipped by ${ancestor.className}`).toBeLessThanOrEqual(ancestor.left + ancestor.clientLeft + ancestor.clientWidth + 1)
  }
}

test('live browser legacy summary refuses unavailable values and distinguishes explicit zero without standards qualification', async ({ page, trackRun }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const script = await seedScript(request, workspace)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Browser summary availability', duration: '2m', groups: [{ name: 'browser', script_id: script, vusers: 1 }] } })))
  trackRun(id)
  let state: 'missing' | 'invalid' | 'zero' | 'empty' = 'missing'
  await page.route(`**/api/v1/tests/${id}`, async route => {
    if (route.request().method() !== 'GET') return route.continue()
    const response = await route.fetch()
    const record = await response.json()
    const browser = state === 'missing' ? { samples: 1 } : state === 'invalid' ? { ...explicitZeroSummary, avg_lcp_ms: -1, avg_inp_ms: null } : { ...explicitZeroSummary, samples: state === 'empty' ? 0 : 1 }
    await route.fulfill({ response, json: { ...record, status: 'RUNNING', live_metrics: { total_transactions: 1, total_errors: 0, active_vusers: 1, transactions: {}, browser, browser_by_device: { unavailable: { samples: 1 }, malformed: null, primitive: 5, complete_zero: explicitZeroSummary } } } })
  })
  await page.goto(`/#/test/${id}?tab=diagnostics&${scopeQuery(workspace)}`)
  await page.reload()
  const assessment = page.getByRole('region', { name: 'Browser legacy summary assessment', exact: true })
  await expect(assessment).toBeVisible()
  await expect(assessment).toContainText('Unscored')
  await expect(assessment).toContainText('0/7 required fields available')
  await expect(assessment).not.toContainText('100/100')
  await expect(assessment).toContainText('not a standards-qualified Core Web Vitals score')
  await expect(assessment).toHaveAttribute('data-model-version', 'legacy-summary-availability-v2')
  await expect(page.locator('.browser-metrics-card .browser-device-card').filter({ hasText: 'Malformed summary: expected an object' })).toHaveCount(2)
  const counter = page.locator('.browser-metrics-card > .goal-status-header .goal-status-badge')
  await expect(counter).toHaveClass(/neutral/)
  await expect(counter).not.toHaveClass(/healthy/)
  state = 'invalid'
  await expect(assessment).toContainText('5/7 required fields available')
  await expect(assessment.locator('[data-availability="invalid"]')).toHaveCount(2)
  await expect(assessment).toContainText('Value must be finite and nonnegative')
  state = 'zero'
  await expect(assessment).toContainText('Within legacy thresholds · 100/100 legacy estimate')
  await expect(assessment).toContainText('7/7 required fields available')
  await expect(assessment).toContainText('0.000')
  await expect(assessment).toContainText('Weakest complete summary: complete_zero')
  await expect(assessment).not.toContainText('Healthy')
  state = 'empty'
  await expect(assessment.getByRole('heading', { name: 'Unscored', exact: true })).toBeVisible()
  await expect(assessment).toContainText('Sample count must be a positive safe integer')
  await expect(assessment).not.toContainText('100/100 legacy estimate')
  await captureReadableAssessment(page, assessment, 'browser-live-unscored.png')
})

test('archived browser legacy summary preserves mixed-cohort gaps in engine CSV and ranks only complete devices', async ({ page, trackRun }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const script = await seedScript(request, workspace)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Archived browser availability', duration: '1s', groups: [{ name: 'browser', script_id: script, vusers: 1 }] } })))
  trackRun(id)
  await page.route(`**/api/v1/tests/${id}`, async route => {
    if (route.request().method() !== 'GET') return route.continue()
    const response = await route.fetch()
    const record = await response.json()
    await route.fulfill({ response, json: { ...record, status: 'COMPLETE', archive: { transaction_count: 1 }, started_at: '2026-10-05T00:00:00Z', ended_at: '2026-10-05T00:00:01Z' } })
  })
  await page.route(`**/api/v1/tests/${id}/archive/report*`, route => route.fulfill({ json: {
    manifest: { test_id: id, test_name: 'Archived browser availability', status: 'COMPLETE', provenance: { started_at: '2026-10-05T00:00:00Z', duration: '1s', total_vusers: 1 } },
    overview: { transactions: 1, browser: { samples: 100, avg_lcp_ms: 4200, avg_cls_score: 0 } }, by_action: {},
    browser_by_device: {
      complete_slow: { ...explicitZeroSummary, samples: 10, avg_lcp_ms: 4000, browser_engine: 'chromium', device_platform: 'desktop' },
      missing_lcp: { samples: 90, avg_inp_ms: 0, avg_cls_score: 0, browser_engine: 'chromium', device_platform: 'desktop' },
      malformed: null, primitive: 5,
    },
  } }))
  await page.goto(`/#/report/${id}?section=diagnostics&${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByRole('tab', { name: 'Diagnostics', exact: true }).click()
  await page.getByText('Browser evidence', { exact: true }).click()
  const assessment = page.getByRole('region', { name: 'Browser legacy summary assessment', exact: true })
  await expect(assessment).toContainText('Unscored')
  await expect(assessment).toContainText('2/7 required fields available')
  await expect(assessment).toContainText('Weakest complete summary: complete_slow')
  await assessment.getByText('3 device summaries unranked because required evidence is unavailable', { exact: true }).click()
  await expect(assessment).toContainText('missing_lcp: Required fields unavailable: LCP (missing)')
  await expect(assessment).toContainText('malformed: Malformed summary: expected an object')
  await expect(assessment).toContainText('primitive: Malformed summary: expected an object')
  await expect(assessment).toContainText('not a standards-qualified Core Web Vitals score')
  await page.getByRole('tab', { name: 'Engine', exact: true }).click()
  const exportButton = page.getByRole('button', { name: 'Export Browser Engine Breakdown (LCP / INP / CLS) data as CSV', exact: true })
  const downloadPromise = page.waitForEvent('download')
  await exportButton.click()
  const download = await downloadPromise
  const path = await download.path()
  expect(path).not.toBeNull()
  const csv = await readFile(path!, 'utf8')
  expect(csv).toContain('"Cohort identity"')
  expect(csv).toContain('"Chromium","[""engine"",""chromium""]",,"0","0",')
  expect(csv).toContain('"Engine unavailable (malformed)","[""engine"",""unavailable"",""malformed""]",,,,')
  expect(csv).toContain('"Engine unavailable (primitive)","[""engine"",""unavailable"",""primitive""]",,,,')
  expect(csv).not.toContain('"400"')
  expect(csv).toContain('missing values remain empty and gaps are not interpolated')
  await captureReadableAssessment(page, assessment, 'browser-archive-unscored.png')
})


test('browser results comparison cards preserve exact mixed cohorts and malformed summaries', async ({ page, trackRun }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const script = await seedScript(request, workspace)
  const runs: Array<{ id: string; name: string }> = []
  for (const name of ['Browser mixed comparison', 'Browser direct comparison']) {
    const id = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name, duration: '1m', groups: [{ name: 'browser', script_id: script, vusers: 1 }] } })))
    trackRun(id)
    runs.push({ id, name })
  }
  const devices = {
    captured: { samples: 10, browser_engine: 'chromium', device_platform: 'ios', avg_lcp_ms: 4000, avg_inp_ms: 0, avg_cls_score: 0 },
    missing: { samples: 90, browser_engine: 'chromium', device_platform: 'ios', avg_inp_ms: 0, avg_cls_score: 0 },
    alias: { samples: 1, browser_engine: 'Chromium', device_platform: 'iOS', avg_lcp_ms: 8000, avg_inp_ms: 0, avg_cls_score: 0 },
    malformed: null,
    primitive: 5,
    nested: { samples: 0, browser_engine: {}, device_platform: {}, device_tier: {}, avg_lcp_ms: 4000, avg_inp_ms: -1 },
  }
  await page.route('**/api/v1/tests**', async route => {
    const path = new URL(route.request().url()).pathname
    const index = runs.findIndex(run => path.startsWith(`/api/v1/tests/${run.id}/archive/`))
    if (index >= 0 && path.endsWith('/report')) return route.fulfill({ json: {
      manifest: { test_id: runs[index].id, test_name: runs[index].name, provenance: {} },
      overview: { transactions: 1, peak_active_vusers: 1, browser: { samples: 0, avg_lcp_ms: 4000, avg_inp_ms: -1 } },
      by_action: {}, resource_peaks: { cpu_process_pct: 0 },
      browser_by_device: devices,
      browser_by_engine: index === 0 ? {} : { malformed: null, nested: { samples: 1, browser_engine: {}, avg_lcp_ms: -1, avg_inp_ms: 0 } },
      browser_by_platform: index === 0 ? {} : { malformed: 5, nested: { samples: 1, device_platform: {}, browser_engine: {}, avg_lcp_ms: null, avg_inp_ms: 0 } },
    } })
    if (index >= 0 && path.endsWith('/summaries')) return route.fulfill({ json: { summaries: [] } })
    if (route.request().method() !== 'GET' || path !== '/api/v1/tests') return route.continue()
    const response = await route.fetch()
    const payload = await response.json()
    return route.fulfill({ response, json: { ...payload, tests: payload.tests.map((run: { id: string }) => runs.some(candidate => candidate.id === run.id) ? { ...run, status: 'COMPLETE', archive: { transaction_count: 1 } } : run) } })
  })
  await page.goto(`/#/results?${scopeQuery(workspace)}`)
  await page.reload()
  for (const run of runs) await page.getByRole('checkbox', { name: `Select ${run.name}`, exact: true }).check()
  await page.getByRole('button', { name: 'Run Comparison', exact: true }).click()
  const panel = (name: string) => page.locator('.results-compare-panel').filter({ has: page.getByRole('heading', { name, exact: true }) })
  const mixedPanel = panel(runs[0].name)
  const directPanel = panel(runs[1].name)
  await expect(mixedPanel.locator('.browser-device-card')).toHaveCount(6)
  // Exact tuple identities avoid alias display-name collisions in the DOM.
  const keyed = (owner: Locator, tuple: string[]) => owner.locator(`[data-cohort-key=${JSON.stringify(JSON.stringify(tuple))}]`)
  await expect(keyed(mixedPanel, ['device', 'malformed'])).toContainText('Malformed summary: expected an object')
  await expect(keyed(mixedPanel, ['device', 'primitive'])).toContainText('LCP n/a')
  await expect(keyed(mixedPanel, ['device', 'nested'])).toContainText('unknown · browser · unknown tier')
  await expect(keyed(mixedPanel, ['device', 'nested'])).toContainText('LCP n/a')
  await expect(mixedPanel.locator('.results-compare-metric').filter({ has: page.getByText('Avg LCP', { exact: true }) })).toContainText('n/a')
  for (const [mode, identity, alias] of [['Engine', 'chromium', 'Chromium'], ['Platform', 'ios', 'iOS']] as const) {
    await page.getByRole('tab', { name: mode, exact: true }).click()
    const kind = mode.toLowerCase()
    await expect(mixedPanel.locator('.browser-device-card')).toHaveCount(5)
    const mixed = keyed(mixedPanel, [kind, identity])
    await expect(mixed).toContainText('LCP n/a')
    await expect(mixed).toContainText('INP 0.0 ms')
    await expect(mixed).toContainText('Unavailable members are retained')
    await expect(mixed).not.toContainText('400.0 ms')
    await expect(keyed(mixedPanel, [kind, alias])).toContainText('LCP 8000.0 ms')
    for (const profile of ['malformed', 'primitive', 'nested']) await expect(keyed(mixedPanel, [kind, 'unavailable', profile])).toContainText('LCP n/a')
    await expect(keyed(directPanel, [kind, 'malformed'])).toContainText('Malformed summary: expected an object')
    await expect(keyed(directPanel, [kind, 'nested'])).toContainText('INP 0.0 ms')
    await expect(keyed(directPanel, [kind, 'nested'])).toContainText('LCP n/a')
  }
})

const documentAlgorithm = 'web-vitals/5.1.0'
const documentAlgorithmSHA = '8890ec954757ea8e4801f49958afb76e2b8837c47fec5b16598fc20cbd49c220'
const actualDocumentCohort = { version: 1, browser_version: '136.0.1', platform: 'linux', viewport_width: 800, viewport_height: 600,
  headless: true, mobile: false, touch: false, device_scale_factor: 0.5, cpu_throttle_rate: 1, network_latency_ms: 0,
  network_bandwidth_kbps: 0, shaping_applied: true, cache_enabled: false, cache_content_urls: false, check_newer_versions: false,
  new_user_policy: 'persistent_context', new_user_policy_applied: true }
const documentTuple = { metric: 'cls', algorithm: documentAlgorithm, algorithm_sha256: documentAlgorithmSHA, unit: 'score', engine: 'chromium',
  group: 'checkout', region: 'local', device_profile: 'desktop', cohort: actualDocumentCohort, frame_scope: 'main_frame', lifecycle_phase: 'navigation',
  eligible_final: 2, unavailable_final: 1, unqualified_final: 3, censored: 1, provisional: 2,
  excluded_reasons: { no_observation: 1, cohort_unavailable: 3, source_censored: 1 },
  distribution: { count: 2, sum: 0, min: 0, max: 0, buckets: [2, ...Array<number>(191).fill(0)] } }

async function captureDocumentPanel(page: Page, panel: Locator, filename: string) {
  await page.setViewportSize({ width: 760, height: 1600 })
  // Wait for the finite responsive shell transition before measuring its
  // scroll owner. Capturing must never advance a layout animation itself.
  await waitForFiniteLayout(page)
  await panel.scrollIntoViewIfNeeded()
  const heading = panel.getByRole('heading', { name: 'Browser document lifecycle evidence', exact: true })
  await panel.evaluate(node => {
    const owner = node.closest('.main-content') as HTMLElement | null
    if (!owner) throw new Error('Document evidence has no known scroll owner')
    owner.scrollTop += node.getBoundingClientRect().top - (document.querySelector('.main-topbar')?.getBoundingClientRect().bottom ?? 0) - 16
  })
  const geometry = () => panel.evaluate(node => {
    const box = node.getBoundingClientRect(), heading = node.querySelector('h3')!.getBoundingClientRect()
    const owner = node.closest('.main-content') as HTMLElement | null
    if (!owner) throw new Error('Document evidence has no known scroll owner')
    const ownerBox = owner.getBoundingClientRect()
    const hit = document.elementFromPoint(heading.left + heading.width / 2, heading.top + heading.height / 2)
    return { left: box.left, right: box.right, viewport: innerWidth, viewportHeight: innerHeight,
      scrollWidth: node.scrollWidth, clientWidth: node.clientWidth, ownerLeft: Math.max(0, ownerBox.left + owner.clientLeft),
      ownerRight: Math.min(innerWidth, ownerBox.left + owner.clientLeft + owner.clientWidth),
      topbarBottom: Math.max(0, document.querySelector('.main-topbar')?.getBoundingClientRect().bottom ?? 0),
      headingLeft: heading.left, headingRight: heading.right, headingTop: heading.top, headingBottom: heading.bottom,
      headingHit: hit === node.querySelector('h3') || node.querySelector('h3')!.contains(hit) }
  })
  const before = await geometry()
  // Preserve the initial diagnostic before assertions so an actual clipping
  // failure retains evidence instead of losing it during fixture teardown.
  await test.info().attach(`${filename}-before-geometry`, { body: JSON.stringify(before), contentType: 'application/json' })
  await expect(heading).toBeInViewport()
  await expect.poll(async () => (await geometry()).headingHit).toBe(true)
  expect(before.left).toBeGreaterThanOrEqual(0)
  expect(before.right).toBeLessThanOrEqual(before.viewport)
  expect(before.left).toBeGreaterThanOrEqual(before.ownerLeft)
  expect(before.right).toBeLessThanOrEqual(before.ownerRight)
  expect(before.headingTop).toBeGreaterThanOrEqual(before.topbarBottom)
  expect(before.headingBottom).toBeLessThanOrEqual(before.viewportHeight)
  expect(before.scrollWidth).toBeLessThanOrEqual(before.clientWidth + 1)
  const tableOwner = panel.getByLabel('Document metric populations', { exact: true })
  await expect(tableOwner).toBeVisible()
  expect(await tableOwner.evaluate(node => getComputedStyle(node).overflowX)).toBe('auto')
  const tableGeometry = await tableOwner.evaluate(node => {
    const table = node.querySelector(':scope > table')!, header = table.querySelector('th')!
    return { ownerWidth: node.clientWidth, tableWidth: table.getBoundingClientRect().width, headerWidth: header.getBoundingClientRect().width }
  })
  await test.info().attach(`${filename}-table-geometry`, { body: JSON.stringify(tableGeometry), contentType: 'application/json' })
  expect(tableGeometry.tableWidth).toBeGreaterThan(tableGeometry.ownerWidth)
  expect(tableGeometry.headerWidth).toBeGreaterThanOrEqual(60)
  // Element screenshots scroll oversized panels and can put their heading
  // beneath sticky controls. Preserve the actual visible viewport instead.
  await page.screenshot({ path: test.info().outputPath(filename), fullPage: false })
  const after = await geometry()
  await test.info().attach(`${filename}-after-geometry`, { body: JSON.stringify(after), contentType: 'application/json' })
  expect(after.left).toBeGreaterThanOrEqual(0)
  expect(after.right).toBeLessThanOrEqual(after.viewport)
  expect(after.left).toBeGreaterThanOrEqual(after.ownerLeft)
  expect(after.right).toBeLessThanOrEqual(after.ownerRight)
  expect(after.headingTop).toBeGreaterThanOrEqual(after.topbarBottom)
  expect(after.headingBottom).toBeLessThanOrEqual(after.viewportHeight)
  expect(after.headingLeft).toBeGreaterThanOrEqual(after.left)
  expect(after.headingRight).toBeLessThanOrEqual(after.right)
  expect(after.headingHit).toBe(true)
  await test.info().attach(`${filename}-geometry`, { body: JSON.stringify({ before, after }), contentType: 'application/json' })
}

async function proveDocumentKeyboardOverflow(panel: Locator, filename: string) {
  const owner = panel.getByLabel('Document metric populations', { exact: true })
  const geometry = () => owner.evaluate(node => {
    const main = node.closest('.main-content') as HTMLElement | null
    if (!main) throw new Error('Document table has no main scroll owner')
    return { scrollLeft: node.scrollLeft, scrollWidth: node.scrollWidth, clientWidth: node.clientWidth, mainScrollLeft: main.scrollLeft }
  })
  const before = await geometry()
  await test.info().attach(`${filename}-keyboard-before`, { body: JSON.stringify(before), contentType: 'application/json' })
  expect(before.scrollLeft).toBe(0)
  expect(before.scrollWidth).toBeGreaterThan(before.clientWidth)
  await owner.focus()
  await owner.press('ArrowRight')
  await expect.poll(async () => (await geometry()).scrollLeft).toBeGreaterThan(before.scrollLeft)
  const after = await geometry()
  await test.info().attach(`${filename}-keyboard-after`, { body: JSON.stringify(after), contentType: 'application/json' })
  expect(after.mainScrollLeft).toBe(before.mainScrollLeft)
}

test('live document populations preserve overlapping provisional history, actual cohorts and missing-peer coverage', async ({ page, trackRun }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request, workspace = await createWorkspace(request), script = await seedScript(request, workspace)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId,
    release_id: workspace.releaseId, name: 'Independent live document evidence', duration: '2m', groups: [{ name: 'browser', script_id: script, vusers: 1 }] } })))
  trackRun(id)
  let missingPeer = false
  await page.route(`**/api/v1/tests/${id}`, async route => {
    if (route.request().method() !== 'GET') return route.continue()
    const response = await route.fetch(), record = await response.json()
    await route.fulfill({ response, json: { ...record, status: 'RUNNING', live_metrics: { total_transactions: 999, total_errors: 0, active_vusers: 1, transactions: {},
      browser_documents: missingPeer ? { version: 2, metrics: null, incomplete: true, dropped_observations: 0 }
        : { version: 2, metrics: [documentTuple], incomplete: false, dropped_observations: 0 } } } })
  })
  await page.goto(`/#/test/${id}?tab=diagnostics&${scopeQuery(workspace)}`)
  await page.reload()
  const panel = page.getByRole('region', { name: 'Browser document evidence', exact: true })
  await expect(panel).toBeVisible()
  await expect(panel).toContainText('First seen provisional')
  await expect(panel).toContainText('overlap terminal history')
  await expect(panel).toContainText('No score is calculated')
  const cells = panel.getByLabel('Document metric populations', { exact: true }).locator(':scope > table > tbody > tr').first().locator(':scope > td')
  await expect(cells).toHaveCount(13)
  expect(await cells.allTextContents()).toEqual(['CLS', 'chromium · desktopGroup: checkout · Region: local', 'Main frame', 'Navigation', '2', '1', '3', '1', '2', '0 score', '0 score', '0 score', expect.any(String)])
  await captureDocumentPanel(page, panel, 'browser-document-live-overview.png')
  await proveDocumentKeyboardOverflow(panel, 'browser-document-live')
  await panel.getByText('Inspect CLS tuple 1', { exact: true }).click()
  await expect(panel).toContainText(documentAlgorithmSHA)
  await expect(panel).toContainText('0.5 / 1')
  await expect(panel).toContainText('No / No / No')
  await expect(panel.getByRole('table', { name: 'CLS qualified final histogram for tuple 1', exact: true })).toContainText('2')
  await expect(page.locator('.browser-metrics-card')).toHaveCount(0)
  await captureDocumentPanel(page, panel, 'browser-document-live.png')
  missingPeer = true
  await expect(panel).toContainText('Document evidence is incomplete. 0 observations were dropped')
  await expect(panel).toContainText('No document metric tuples were recorded')
  await expect(panel.locator('table')).toHaveCount(0)
  await expect(panel).not.toContainText('0 score')
})

test('archive document evidence retains latest pending, censored and unavailable measurements independently from actions', async ({ page, trackRun }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request, workspace = await createWorkspace(request), script = await seedScript(request, workspace)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId,
    release_id: workspace.releaseId, name: 'Independent archive document evidence', duration: '1s', groups: [{ name: 'browser', script_id: script, vusers: 1 }] } })))
  trackRun(id)
  const source = 'a'.repeat(32)
  const baseObservation = (index: number) => ({ version: 2, source_generation: source, document_id: `${source}-document-${index}`, lifecycle_id: `${source}-lifecycle-${index}`,
    frame_id: `${source}-frame-${index}`, navigation_id: `${source}-navigation-${index}`, measurement_id: `${source}-document-${index}/${source}-lifecycle-${index}/cls`, revision: 2,
    metric: 'cls', algorithm: documentAlgorithm, algorithm_sha256: documentAlgorithmSHA, unit: 'score', engine: 'chromium', group: 'checkout', region: 'local',
    device_profile: 'desktop', cohort: actualDocumentCohort, frame_scope: 'main_frame', lifecycle_phase: 'navigation', state: 'final', boundary: 'pagehide',
    window_start: '2026-10-05T10:00:00.000000001Z', window_end: '2026-10-05T10:00:01.000000002Z', captured_at: '2026-10-05T10:00:01.000000003Z', available: true, qualified: true, value: 0 })
  const noInput = { ...baseObservation(4), metric: 'inp', unit: 'ms', measurement_id: `${source}-document-4/${source}-lifecycle-4/inp`, available: false, qualified: false, value: null, unavailable_reason: 'no_interaction' }
  await page.route(`**/api/v1/tests/${id}`, async route => { if (route.request().method() !== 'GET') return route.continue(); const response = await route.fetch(); const record = await response.json(); await route.fulfill({ response, json: { ...record, status: 'COMPLETE', archive: { transaction_count: 999 }, started_at: '2026-10-05T10:00:00Z', ended_at: '2026-10-05T10:00:01Z' } }) })
  await page.route(`**/api/v1/tests/${id}/archive/report*`, route => route.fulfill({ json: {
    manifest: { test_id: id, test_name: 'Independent archive document evidence', status: 'COMPLETE', provenance: { started_at: '2026-10-05T10:00:00Z', duration: '1s', total_vusers: 1 } },
    overview: { transactions: 999 }, by_action: {}, browser_documents: { version: 2, metrics: [documentTuple, { ...documentTuple, frame_scope: 'child_frame' }, { ...documentTuple, lifecycle_phase: 'bfcache_restore' }], incomplete: true, dropped_observations: 7 },
    browser_document_evidence: [baseObservation(1), { ...baseObservation(2), state: 'provisional', boundary: 'hidden', qualified: false },
      { ...baseObservation(3), state: 'censored', boundary: 'cancelled', qualified: false, available: false, value: null, unavailable_reason: 'source_censored' }, noInput], browser_document_evidence_truncated: true,
  } }))
  await page.goto(`/#/report/${id}?section=diagnostics&${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByRole('tab', { name: 'Diagnostics', exact: true }).click()
  await page.getByText('Browser evidence', { exact: true }).click()
  const panel = page.getByRole('region', { name: 'Browser document evidence', exact: true })
  await expect(panel).toContainText('Retained pending')
  await expect(panel).not.toContainText('First seen provisional')
  await expect(panel).toContainText('Document evidence is incomplete. 7 observations were dropped')
  await expect(panel).toContainText('Additional source observations are excluded from this view')
  await expect(panel.getByLabel('Document metric populations', { exact: true }).locator(':scope > table > tbody > tr')).toHaveCount(3)
  const observations = panel.getByLabel('Retained document measurements', { exact: true })
  await expect(observations.locator(':scope > table > tbody > tr')).toHaveCount(4)
  await expect(observations).toContainText('provisional · hidden')
  await expect(observations).toContainText('censored · cancelled')
  await expect(observations).toContainText('Unavailable · no_interaction')
  await observations.getByText('Inspect document observation 1', { exact: true }).click()
  await expect(observations).toContainText(`${source}-document-1`)
  await expect(observations).toContainText('2026-10-05T10:00:00.000000001Z')
  await expect(observations).toContainText(documentAlgorithmSHA)
  await expect(panel).not.toContainText('999 eligible')
  await captureDocumentPanel(page, panel, 'browser-document-archive-overview.png')
  await proveDocumentKeyboardOverflow(panel, 'browser-document-archive')
  await panel.getByText('Inspect CLS tuple 1', { exact: true }).click()
  await captureDocumentPanel(page, panel, 'browser-document-archive.png')
})
