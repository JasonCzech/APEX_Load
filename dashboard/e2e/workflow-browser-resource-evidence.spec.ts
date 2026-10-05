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


async function captureReadableAssessment(page: Page, assessment: Locator, filename: string) {
  // Retain the original narrow viewport/whole-box checks. The region may be
  // taller than this viewport, so use a taller canvas only for its artifact.
  await page.setViewportSize({ width: 760, height: 900 })
  await assessment.scrollIntoViewIfNeeded()
  const geometry = await assessment.evaluate(node => { const rect = node.getBoundingClientRect(); return { left: rect.left, right: rect.right, width: window.innerWidth, scrollWidth: node.scrollWidth, clientWidth: node.clientWidth } })
  expect(geometry.left).toBeGreaterThanOrEqual(0)
  expect(geometry.right).toBeLessThanOrEqual(geometry.width)
  expect(geometry.scrollWidth).toBeLessThanOrEqual(geometry.clientWidth + 1)
  await page.setViewportSize({ width: 760, height: 1600 })
  const positioning = await assessment.evaluate(node => {
    const owner = node.closest('.main-content') as HTMLElement | null
    if (!owner) throw new Error('Browser assessment has no known main scroll owner')
    const header = document.querySelector('.main-topbar')
    const headerBottom = header?.getBoundingClientRect().bottom ?? 0
    const before = node.getBoundingClientRect()
    owner.scrollTop += before.top - headerBottom - 16
    return { beforeTop: before.top, headerBottom, scrollOwner: owner.className }
  })
  const heading = assessment.getByRole('heading')
  const hit = async () => heading.evaluate(node => { const rect = node.getBoundingClientRect(); const hit = document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2); return { hit: hit === node || node.contains(hit), top: rect.top, bottom: rect.bottom } })
  await expect.poll(async () => (await hit()).hit).toBe(true)
  expect((await hit()).top).toBeGreaterThanOrEqual(positioning.headerBottom)
  await test.info().attach(`${filename}-scroll-position`, { body: JSON.stringify(positioning), contentType: 'application/json' })
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
