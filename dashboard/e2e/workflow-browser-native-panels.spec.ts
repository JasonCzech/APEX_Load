import { createServer } from 'node:http'
import type { AddressInfo } from 'node:net'
import { createHash } from 'node:crypto'
import { test, expect, json, idOf, login, runStatus, signOut } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

// Real local Chromium desktop and Pixel emulation, with no response routes or
// seeded document measurements. This does not qualify another host or device.
test('native replay and two browser cohorts survive archive, HAR and saved panel inspection', async ({ page, trackRun }, testInfo) => {
  test.setTimeout(240_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request, workspace = await createWorkspace(request)
  const counts = new Map<string, number>()
  const target = createServer((req, res) => {
    counts.set(req.url || '', (counts.get(req.url || '') || 0) + 1)
    res.setHeader('Cache-Control', 'no-store')
    if (req.url === '/') { res.setHeader('Content-Type', 'text/html'); res.end('<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="icon" href="data:,"><link rel="stylesheet" href="/style.css"><h1>Native panel fixture</h1><button id="go" onclick="fetch(\'/clicked\').then(r=>r.text()).then(()=>document.querySelector(\'#done\').hidden=false)">Go</button><div id="done" hidden>Done</div>') }
    else if (req.url === '/style.css') { res.setHeader('Content-Type', 'text/css'); res.end('button{width:140px;height:60px}h1{font-size:32px}') }
    else if (req.url === '/clicked') res.end('ok')
    else { res.statusCode = 404; res.end() }
  })
  await new Promise<void>(resolve => target.listen(0, '127.0.0.1', resolve))
  const url = `http://127.0.0.1:${(target.address() as AddressInfo).port}`
  let runId = '', replayId = '', extraRunId = ''
  try {
    const ids: string[] = []
    for (const profile of ['', 'pixel_8']) {
      const script = { name: `Native ${profile || 'desktop'}`, protocol: 'browser', version: '1.0.0', project_id: workspace.projectId,
        config: { browser: { engine: 'chromium', device_profile: profile, headless: true, capture_document_metrics: true, client_metrics: true, capture_interactions: true, har_capture: 'always' } },
        actions: [{ name: 'open', type: 'browser_navigate', target: `${url}/` }, { name: 'activate', type: 'browser_click', selector: '#go', sync: { type: 'element_visible', value: '#done', timeout_ms: 2000 } }, { name: 'paint', type: 'browser_wait', wait_for: { type: 'duration', value: '300ms', timeout_ms: 2000 } }],
      }
      ids.push(idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script } }))))
    }
    const replay = await json(await request.post(`/api/v1/scripts/${ids[0]}/browser-replay/sessions`, { data: { project_id: workspace.projectId, record_har: true } }))
    replayId = String(replay.replay_session_id)
    await expect.poll(async () => {
      const payload = await json(await request.get(`/api/v1/browser-replay/sessions/${replayId}`))
      return (payload.session as { status: string }).status
    }, { timeout: 90_000 }).toBe('completed')
    const replayHAR = await request.get(`/api/v1/browser-replay/sessions/${replayId}/har`)
    expect(replayHAR.ok(), await replayHAR.text()).toBeTruthy()
    expect(replayHAR.headers()['x-apexload-har-fidelity']).toBe('native')
    expect((await replayHAR.json()).log.entries).toHaveLength(3)
    const policyDefinition = { version: 1, baseline: { mode: 'none' }, metrics: [{ metric: 'fcp', good: 1800, poor: 3000, weight: 1 }] }
    const policyScope = `project_id=${encodeURIComponent(workspace.projectId)}`
    const pinPolicy = await json(await request.post(`/api/v1/analysis/policies?${policyScope}`, { data: { kind: 'browser_policy', name: 'Frozen browser policy', definition: policyDefinition } }))
    await json(await request.put(`/api/v1/analysis/policies/${pinPolicy.id}?${policyScope}`, { data: { kind: 'browser_policy', revision: 1, name: 'Later policy revision', definition: policyDefinition } }))
    runId = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Native browser tuple panels', termination_mode: 'iterations', analysis_policy_selections: { browser: { id: pinPolicy.id, revision: 1 } }, groups: ids.map((script_id, index) => ({ name: index ? 'mobile' : 'desktop', script_id, vusers: 1, iterations: 1 })) } })))
    trackRun(runId)
    await json(await request.post(`/api/v1/tests/${runId}/start`))
    await expect.poll(() => runStatus(request, runId), { timeout: 90_000 }).toBe('COMPLETE')
    await expect.poll(async () => (await request.get(`/api/v1/tests/${runId}/archive/manifest`)).ok(), { timeout: 60_000 }).toBe(true)
    const listing = await json(await request.get(`/api/v1/tests/${runId}/archive/browser/har`))
    const artifacts = (listing.artifacts as { name: string; sha256: string; bytes: number; fidelity: string; available: boolean }[]).filter(item => item.fidelity === 'native' && item.available)
    expect(artifacts).toHaveLength(2)
    const query = { query_version: 3, browser_tuple_mode: 'separate', population: 'browser_document', metric: 'document_fcp', aggregation: 'p75', max_series: 20, start_seconds: 0, end_seconds: 30, bucket_seconds: 5, filters: { frame_scope: 'main_frame', lifecycle_phase: 'navigation' } }
    const result = await json(await request.post(`/api/v1/tests/${runId}/archive/native-series`, { data: query })) as unknown as { current: { buckets: unknown[]; browser_tuples: { version: number; tuples: { id: string; label: string; cohort_digest: string; buckets: { count: number; value: number | null }[] }[] } } }
    expect(result.current.buckets).toEqual([])
    expect(result.current.browser_tuples.version).toBe(1)
    const tuples = result.current.browser_tuples.tuples
    expect(tuples).toHaveLength(2)
    expect(new Set(tuples.map(tuple => tuple.cohort_digest)).size).toBe(2)
    for (const tuple of tuples) expect(tuple.buckets.reduce((sum, bucket) => sum + bucket.count, 0)).toBe(1)
    await page.goto(`/#/report/${runId}?section=diagnostics&${scopeQuery(workspace)}`); await page.reload()
    await page.getByRole('tab', { name: 'Diagnostics', exact: true }).click()
    await page.getByText('Browser evidence', { exact: true }).click()
    await page.getByRole('button', { name: 'Inspect retained archive resources', exact: true }).click()
    const authoredPages = page.getByRole('region', { name: 'Authored browser pages', exact: true })
    await expect(authoredPages).toBeVisible()
    await expect(authoredPages).toContainText('2 retained receipts · 2 distinct document bindings')
    await expect(authoredPages.getByRole('button', { name: 'Export Authored browser pages image as SVG', exact: true })).toHaveCount(0)
    await authoredPages.locator('summary').first().click()
    await expect(authoredPages).toContainText(/cdp_loader_join|native_navigation_commit/)
    const pageCSV = page.waitForEvent('download')
    await authoredPages.getByRole('button', { name: 'Export Authored browser pages data as CSV', exact: true }).click()
    const pageStream = await (await pageCSV).createReadStream(), pageChunks: Buffer[] = []
    for await (const chunk of pageStream!) pageChunks.push(Buffer.from(chunk))
    const pageData = Buffer.concat(pageChunks).toString('utf8')
    expect(pageData).toContain('Original source SHA-256')
    expect(pageData).toContain('actions[0]')
    await page.setViewportSize({ width: 760, height: 1000 })
    await authoredPages.scrollIntoViewIfNeeded()
    expect(await authoredPages.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath('native-authored-pages.png'), animations: 'disabled' })
    const downloads = page.getByRole('region', { name: 'Browser HARs', exact: true })
    await expect(downloads.getByRole('button', { name: 'Download native HAR', exact: true })).toHaveCount(2)
    const downloadEvent = page.waitForEvent('download')
    await downloads.getByRole('row').filter({ hasText: artifacts[0].name }).getByRole('button', { name: 'Download native HAR', exact: true }).click()
    const download = await downloadEvent, stream = await download.createReadStream(), chunks: Buffer[] = []
    for await (const chunk of stream!) chunks.push(Buffer.from(chunk))
    const body = Buffer.concat(chunks)
    expect(body.length).toBe(artifacts[0].bytes)
    expect(createHash('sha256').update(body).digest('hex')).toBe(artifacts[0].sha256)
    expect(JSON.parse(body.toString()).log.entries).toHaveLength(3)
    await page.goto(`/#/report/${runId}?section=performance&${scopeQuery(workspace)}`); await page.reload()
    await page.getByText('Create project analytics panel', { exact: true }).click()
    await page.getByLabel('Panel title', { exact: true }).fill('Separate native browser cohorts')
    await page.getByRole('combobox', { name: 'Evidence population', exact: true }).selectOption('browser_document')
    await page.getByLabel('Show separate browser cohorts', { exact: true }).check()
    await page.getByRole('combobox', { name: 'Metric', exact: true }).selectOption('document_fcp')
    await page.getByLabel('End elapsed seconds', { exact: true }).fill('30')
    await page.getByLabel('Bucket seconds', { exact: true }).fill('5')
    await page.getByRole('combobox', { name: 'Visualization', exact: true }).selectOption('table')
    await page.getByRole('button', { name: 'Save project panel', exact: true }).click()
    const panel = page.getByRole('region', { name: 'Separate browser cohort panel', exact: true })
    await expect(panel).toBeVisible(); await page.reload(); await expect(panel).toBeVisible()
    for (const tuple of tuples) await expect(panel).toContainText(tuple.id)
    expect(await panel.locator('table').count()).toBe(2)
    await panel.getByText('Inspect tuple time buckets (6)', { exact: true }).first().click()
    const csvEvent = page.waitForEvent('download')
    await panel.getByRole('button', { name: /Export Separate native browser cohorts.*data as CSV/ }).first().click()
    const csvDownload = await csvEvent, csvStream = await csvDownload.createReadStream(), csvChunks: Buffer[] = []
    for await (const chunk of csvStream!) csvChunks.push(Buffer.from(chunk))
    const csv = Buffer.concat(csvChunks).toString('utf8')
    expect(csv).toContain('Tuple'); expect(csv).toContain('Cohort'); expect(csv).toContain(runId)
    await page.setViewportSize({ width: 760, height: 1000 }); await panel.scrollIntoViewIfNeeded()
    expect(await panel.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath('native-browser-tuples.png'), animations: 'disabled' })
    expect(Object.fromEntries(counts)).toEqual({ '/': 3, '/style.css': 3, '/clicked': 3 })
    extraRunId = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Frozen browser comparison input', termination_mode: 'iterations', analysis_policy_selections: { browser: { id: pinPolicy.id, revision: 1 } }, groups: [...ids.map((script_id, index) => ({ name: index ? 'mobile' : 'desktop', script_id, vusers: 1, iterations: 1 })), { name: 'baseline-only', script_id: ids[0], vusers: 1, iterations: 1 }] } })))
    trackRun(extraRunId)
    await json(await request.post(`/api/v1/tests/${extraRunId}/start`))
    await expect.poll(() => runStatus(request, extraRunId), { timeout: 90_000 }).toBe('COMPLETE')
    await expect.poll(async () => (await request.get(`/api/v1/tests/${extraRunId}/archive/manifest`)).ok(), { timeout: 60_000 }).toBe(true)
    for (const id of [runId, extraRunId]) {
      const retained = await json(await request.get(`/api/v1/tests/${id}/archive/manifest`)) as { provenance: { policy_pins: { id: string; revision: number; source: string }[] } }
      expect(retained.provenance.policy_pins).toEqual(expect.arrayContaining([expect.objectContaining({ id: pinPolicy.id, revision: 1, source: 'run_selection' })]))
    }
    await page.goto(`/#/report/${runId}?section=performance&${scopeQuery(workspace)}`); await page.reload()
    await page.getByText('Create project analytics panel', { exact: true }).click()
    await page.getByLabel('Panel title', { exact: true }).fill('Frozen native browser comparison')
    await page.getByRole('combobox', { name: 'Evidence population', exact: true }).selectOption('browser_document')
    await page.getByLabel('Show separate browser cohorts', { exact: true }).check()
    await page.getByLabel('Freeze archive inputs for this panel', { exact: true }).check()
    await page.getByRole('combobox', { name: 'Metric', exact: true }).selectOption('document_fcp')
    await page.getByLabel('End elapsed seconds', { exact: true }).fill('30')
    await page.getByLabel('Bucket seconds', { exact: true }).fill('5')
    await page.getByLabel('Comparison run ID (optional)', { exact: true }).fill(extraRunId)
    await page.getByRole('combobox', { name: 'Visualization', exact: true }).selectOption('table')
    const frozenSave = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/monitoring/dashboard-definitions')
    await page.getByRole('button', { name: 'Save project panel', exact: true }).click()
    const frozenResponse = await frozenSave
    expect(frozenResponse.ok(), await frozenResponse.text()).toBeTruthy()
    const frozenDefinition = await frozenResponse.json()
    const frozenQuery = frozenDefinition.tabs[0].panels[0].query
    expect(frozenQuery.query_version).toBe(4)
    expect(frozenQuery.browser_inputs.current.run_id).toBe(runId)
    expect(frozenQuery.browser_inputs.baseline.run_id).toBe(extraRunId)
    expect(frozenQuery.browser_inputs.current.tuple_ids).toHaveLength(2)
    expect(frozenQuery.browser_inputs.baseline.tuple_ids).toHaveLength(3)
    for (const input of [frozenQuery.browser_inputs.current, frozenQuery.browser_inputs.baseline]) expect(input.manifest_sha256).toMatch(/^[a-f0-9]{64}$/)
    await page.goto(`/#/report/${extraRunId}?section=performance&${scopeQuery(workspace)}`); await page.reload()
    await page.getByRole('combobox', { name: 'Project dashboard', exact: true }).selectOption(frozenDefinition.id)
    await expect(panel).toContainText(`Current run: ${runId}`)
    await expect(panel).toContainText(`Baseline run: ${extraRunId}`)
    await panel.getByText('Frozen archive inputs', { exact: true }).click()
    await expect(panel).toContainText(frozenQuery.browser_inputs.current.manifest_sha256)
    await expect(panel).toContainText(frozenQuery.browser_inputs.baseline.manifest_sha256)
    const frozenCSVEvent = page.waitForEvent('download')
    await panel.getByRole('button', { name: /Export Frozen native browser comparison.*data as CSV/ }).first().click()
    const frozenCSVStream = await (await frozenCSVEvent).createReadStream(), frozenChunks: Buffer[] = []
    for await (const chunk of frozenCSVStream!) frozenChunks.push(Buffer.from(chunk))
    const frozenCSV = Buffer.concat(frozenChunks).toString('utf8')
    for (const expected of ['Query SHA-256', frozenQuery.browser_inputs.query_sha256, frozenQuery.browser_inputs.current.manifest_sha256, frozenQuery.browser_inputs.baseline.manifest_sha256, runId, extraRunId]) expect(frozenCSV).toContain(expected)
    type FrozenNativeTuple = { id: string; measurement: { group: string }; buckets: { offset_seconds: number; value: number | null; count: number }[] }
    const frozenSeries = await json(await request.post(`/api/v1/tests/${runId}/archive/native-series`, { data: frozenQuery })) as { baseline: { browser_tuples: { tuples: FrozenNativeTuple[] } } }
    const baselineOnlyTuple = frozenSeries.baseline.browser_tuples.tuples.find((tuple: { measurement: { group: string } }) => tuple.measurement.group === 'baseline-only')!
    expect(baselineOnlyTuple).toBeTruthy()
    expect(frozenQuery.browser_inputs.current.tuple_ids).not.toContain(baselineOnlyTuple.id)
    const baselineOnlyPanel = panel.getByRole('region', { name: /group=baseline-only/ })
    await baselineOnlyPanel.locator('summary').click()
    await expect(baselineOnlyPanel.locator('table')).toBeVisible()
    const baselineRows = await baselineOnlyPanel.locator('tbody tr').evaluateAll(rows => rows.map(row => Array.from(row.querySelectorAll('td')).map(cell => cell.textContent)))
    expect(baselineRows).toEqual(baselineOnlyTuple.buckets.map((bucket: { offset_seconds: number; value: number | null; count: number }) => [String(bucket.offset_seconds), 'Missing', 'Missing', bucket.value == null ? 'Missing' : String(bucket.value), String(bucket.count)]))
    const baselineCSVEvent = page.waitForEvent('download')
    await baselineOnlyPanel.getByRole('button', { name: /Export .*data as CSV/ }).click()
    const baselineCSVStream = await (await baselineCSVEvent).createReadStream(), baselineCSVChunks: Buffer[] = []
    for await (const chunk of baselineCSVStream!) baselineCSVChunks.push(Buffer.from(chunk))
    const baselineCSV = Buffer.concat(baselineCSVChunks).toString('utf8')
    for (const expected of [baselineOnlyTuple.id, 'Baseline final documents', 'missing_current_tuple', frozenQuery.browser_inputs.baseline.manifest_sha256]) expect(baselineCSV).toContain(expected)
    await page.setViewportSize({ width: 760, height: 1800 })
    await panel.scrollIntoViewIfNeeded()
    expect(await panel.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath('native-frozen-browser-panel.png'), animations: 'disabled' })
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') {
      const users = (await json(await request.get('/api/v1/auth/users'))).users as { id: string; subject: string }[]
      const viewer = users.find(user => user.subject === 'local:user')!
      await signOut(page); await login(page, 'user')
      expect((await request.post(`/api/v1/tests/${runId}/archive/native-series`, { data: frozenQuery })).status()).toBe(403)
      expect((await request.get(`/api/v1/monitoring/dashboard-definitions/${frozenDefinition.id}`)).status()).toBe(403)
      await signOut(page); await login(page)
      await json(await request.post(`/api/v1/auth/users/${viewer.id}/bindings`, { data: { scope_type: 'domain', scope_id: workspace.domainId, role: 'viewer' } }))
      await signOut(page); await login(page, 'user')
      try {
        await page.goto(`/#/report/${extraRunId}?section=performance&${scopeQuery(workspace)}`); await page.reload()
        await page.getByRole('combobox', { name: 'Project dashboard', exact: true }).selectOption(frozenDefinition.id)
        await expect(panel).toContainText(`Current run: ${runId}`)
        await expect(page.getByText('Create project analytics panel', { exact: true })).toHaveCount(0)
        expect((await request.post('/api/v1/monitoring/dashboard-definitions', { data: { ...frozenDefinition, name: 'Denied viewer write' } })).status()).toBe(403)
        expect((await request.get(`/api/v1/tests/${runId}/archive/browser/har/${encodeURIComponent(artifacts[0].name)}`)).status()).toBe(403)
        const viewerCSVEvent = page.waitForEvent('download')
        await panel.getByRole('button', { name: /Export Frozen native browser comparison.*data as CSV/ }).first().click()
        const viewerStream = await (await viewerCSVEvent).createReadStream(), viewerChunks: Buffer[] = []
        for await (const chunk of viewerStream!) viewerChunks.push(Buffer.from(chunk))
        expect(Buffer.concat(viewerChunks).toString('utf8')).toContain(frozenQuery.browser_inputs.query_sha256)
      } finally { await signOut(page); await login(page) }
      await page.goto(`/#/report/${extraRunId}?section=performance&${scopeQuery(workspace)}`); await page.reload()
      await page.getByRole('combobox', { name: 'Project dashboard', exact: true }).selectOption(frozenDefinition.id)
    }
    const removed = await request.delete(`/api/v1/tests/${runId}`)
    expect(removed.ok(), await removed.text()).toBeTruthy()
    await page.reload()
    await page.getByRole('combobox', { name: 'Project dashboard', exact: true }).selectOption(frozenDefinition.id)
    await expect(page.getByRole('alert').filter({ hasText: 'Archive panel unavailable' })).toBeVisible()
    await expect(panel).toHaveCount(0)
    const savedAgain = await json(await request.get(`/api/v1/monitoring/dashboard-definitions/${frozenDefinition.id}`))
    expect((savedAgain.tabs as typeof frozenDefinition.tabs)[0].panels[0].query.browser_inputs).toEqual(frozenQuery.browser_inputs)
    const unavailable = page.getByRole('alert').filter({ hasText: 'Archive panel unavailable' })
    await unavailable.scrollIntoViewIfNeeded()
    await expect(unavailable).toBeInViewport()
    await page.screenshot({ path: testInfo.outputPath('native-frozen-browser-panel-unavailable.png'), animations: 'disabled' })
    expect(Object.fromEntries(counts)).toEqual({ '/': 6, '/style.css': 6, '/clicked': 6 })
    await testInfo.attach('frozen-browser-inputs', { body: JSON.stringify({ query: frozenQuery, policy_id: pinPolicy.id, selected_policy_revision: 1, counts: Object.fromEntries(counts), deleted_current_input: true }, null, 2), contentType: 'application/json' })

  } finally {
    if (extraRunId) await request.post(`/api/v1/tests/${extraRunId}/stop`).catch(() => {})
    if (runId) await request.post(`/api/v1/tests/${runId}/stop`).catch(() => {})
    if (replayId) await request.post(`/api/v1/browser-replay/sessions/${replayId}/stop`).catch(() => {})
    target.closeAllConnections()
    await new Promise<void>((resolve, reject) => target.close(error => error ? reject(error) : resolve()))
  }
})
