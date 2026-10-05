# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: workflow-browser-resource-evidence.spec.ts >> archive document evidence retains latest pending, censored and unavailable measurements independently from actions
- Location: e2e/workflow-browser-resource-evidence.spec.ts:356:1

# Error details

```
Error: expect(locator).toHaveCount(expected) failed

Locator:  getByRole('region', { name: 'Browser document evidence', exact: true }).getByLabel('Document metric populations', { exact: true }).locator('tbody > tr')
Expected: 3
Received: 6
Timeout:  15000ms

Call log:
  - Expect "toHaveCount" with timeout 15000ms
  - waiting for getByRole('region', { name: 'Browser document evidence', exact: true }).getByLabel('Document metric populations', { exact: true }).locator('tbody > tr')
    34 × locator resolved to 6 elements
       - unexpected value "6"

```

# Page snapshot

```yaml
- generic [ref=e3]:
  - complementary [ref=e4]:
    - button "APEX Load dashboard" [ref=e5] [cursor=pointer]:
      - img "APEX Load" [ref=e6]
      - generic [ref=e11]: APEX Load
    - button "Workspace context. e2e-1791195656520-731878 domain / e2e-1791195656520-731878 project / e2e-1791195656520-731878 application / e2e-1791195656520-731878 1.0" [ref=e13] [cursor=pointer]:
      - generic [ref=e14]: ◈
      - generic [ref=e15]:
        - generic [ref=e16]: Workspace
        - generic [ref=e17]: e2e-1791195656520-731878 domain / e2e-1791195656520-731878 project / e2e-1791195656520-731878 application / e2e-1791195656520-731878 1.0
      - generic [ref=e18]: ⌄
    - navigation "Primary navigation" [ref=e19]:
      - button "New Test" [ref=e21] [cursor=pointer]:
        - img [ref=e23]
        - generic [ref=e26]: New Test
      - generic [ref=e27]:
        - button "Dashboard" [ref=e29] [cursor=pointer]:
          - img [ref=e31]
          - generic [ref=e36]: Dashboard
        - button "Command Center" [ref=e38] [cursor=pointer]:
          - img [ref=e40]
          - generic [ref=e44]: Command Center
        - button "Results" [ref=e46] [cursor=pointer]:
          - img [ref=e48]
          - generic [ref=e49]: Results
        - button "Monitoring" [ref=e51] [cursor=pointer]:
          - img [ref=e53]
          - generic [ref=e55]: Monitoring
        - button "Activity" [ref=e57] [cursor=pointer]:
          - img [ref=e59]
          - generic [ref=e61]: Activity
      - generic [ref=e62]:
        - button "Scripts" [ref=e64] [cursor=pointer]:
          - img [ref=e66]
          - generic [ref=e69]: Scripts
        - button "Scenarios" [ref=e71] [cursor=pointer]:
          - img [ref=e73]
          - generic [ref=e75]: Scenarios
      - generic [ref=e76]:
        - button "Repository" [ref=e78] [cursor=pointer]:
          - img [ref=e80]
          - generic [ref=e83]: Repository
        - button "Access" [ref=e85] [cursor=pointer]:
          - img [ref=e87]
          - generic [ref=e91]: Access
        - button "Settings" [ref=e93] [cursor=pointer]:
          - img [ref=e95]
          - generic [ref=e98]: Settings
      - button "Help & documentation" [ref=e101] [cursor=pointer]:
        - img [ref=e103]
        - generic [ref=e106]: Help & documentation
    - generic [ref=e107]:
      - generic [ref=e108]:
        - 'button "Preview permissions. Current mode: admin" [ref=e110] [cursor=pointer]':
          - generic [ref=e111]: DEV
          - generic [ref=e112]: Preview permissions
        - generic [ref=e113]:
          - generic "Live" [ref=e114]:
            - generic [ref=e116]: Live
          - 'button "Build info: local · dev" [ref=e118] [cursor=pointer]':
            - generic [ref=e120]: Version Info
      - button "Collapse sidebar" [expanded] [ref=e122] [cursor=pointer]:
        - img [ref=e123]
  - main "Run Results" [ref=e125]:
    - status [ref=e126]: Run Results
    - generic "Dashboard controls" [ref=e127]:
      - heading "Run Results" [level=1] [ref=e129]
      - generic [ref=e130]:
        - link "Help for this page" [ref=e131] [cursor=pointer]:
          - /url: "#/docs?guide=user-guide&article=results"
        - generic "Deployment identity" [ref=e132]:
          - generic [ref=e133]: Browser · Server
        - combobox "Dashboard color theme" [ref=e136]:
          - option "System (Auto)"
          - option "Dark Theme" [selected]
          - option "Light Theme"
          - option "Solarized Dark"
          - option "Solarized Light"
          - option "Monokai Dimmed"
    - generic [ref=e137]:
      - generic [ref=e138]:
        - generic [ref=e139]:
          - paragraph [ref=e140]: Run results · LOC-RUN-20261005-007
          - heading "Independent archive document evidence" [level=1] [ref=e141]
          - paragraph [ref=e142]:
            - generic [ref=e143]: ABORTED
            - text: · Configured 1 VUsers for 1s · Lifecycle elapsed 1s · Archived evidence elapsed Not recorded
        - generic [ref=e144]:
          - button "Compare" [ref=e145] [cursor=pointer]
          - button "Export report" [ref=e146] [cursor=pointer]
          - button "Test notes" [ref=e147] [cursor=pointer]
      - region "Execution outcome" [ref=e148]:
        - heading "Execution aborted" [level=2] [ref=e149]
        - paragraph [ref=e150]: No termination detail was recorded.
        - paragraph [ref=e151]: Execution status is separate from sampled transaction success and performance acceptance.
      - region "Analysis context" [ref=e152]:
        - generic [ref=e153]:
          - text: Metric population
          - combobox "Metric population" [ref=e154]:
            - option "Requests / actions" [selected]
            - option "Named transactions"
            - option "Unclassified legacy records"
        - paragraph [ref=e155]: Requests / actions · Unknown samples · Whole captured run. Legacy interval graphs have no identified named transaction population; their values are separate from these KPIs. Resource curves show the load generators.
      - group [ref=e156]:
        - generic "Run timeline annotations" [ref=e157] [cursor=pointer]
      - tablist "Result sections" [ref=e158]:
        - tab "Summary" [ref=e159] [cursor=pointer]
        - tab "Performance" [ref=e160] [cursor=pointer]
        - tab "Baseline Comparison" [ref=e161] [cursor=pointer]
        - tab "Diagnostics" [selected] [ref=e162] [cursor=pointer]
        - tab "Evidence" [ref=e163] [cursor=pointer]
        - tab "Execution" [ref=e164] [cursor=pointer]
      - tabpanel "Diagnostics" [ref=e165]:
        - region "Report unavailable" [ref=e166]:
          - heading "Report could not be loaded" [level=2] [ref=e167]
          - alert [ref=e168]: archive not found
          - paragraph [ref=e169]: Retry to load the archived findings. Execution details remain available.
          - link "Back to results" [ref=e170] [cursor=pointer]:
            - /url: "#/results?projectId=20b38b9f-1b5b-4eeb-a437-7886ce6e2352&applicationId=622a44a7-72b5-4263-9fad-19b0f751a991&releaseId=f93c9c94-fff2-43a1-8fd9-330bfcbe515f"
          - button "Retry report" [ref=e171] [cursor=pointer]
          - button "View execution details" [ref=e172] [cursor=pointer]
        - generic [ref=e173]:
          - generic [ref=e175]:
            - heading "Diagnostics" [level=2] [ref=e176]
            - paragraph [ref=e177]: Network and generator resource evidence.
          - group [ref=e178]:
            - generic "Network phases" [ref=e179] [cursor=pointer]
          - group [ref=e180]:
            - generic "Resource peaks" [ref=e181] [cursor=pointer]
        - paragraph [ref=e183]: archive not found
        - paragraph [ref=e185]: archive not found
        - group [ref=e186]:
          - generic "Browser evidence" [active] [ref=e187] [cursor=pointer]
          - paragraph [ref=e189]: archive not found
          - paragraph [ref=e191]: archive not found
        - group [ref=e192]:
          - generic "Execution path analysis" [ref=e193] [cursor=pointer]
```

# Test source

```ts
  285 |       headingHit: hit === node.querySelector('h3') || node.querySelector('h3')!.contains(hit) }
  286 |   })
  287 |   const before = await geometry()
  288 |   // Preserve the initial diagnostic before assertions so an actual clipping
  289 |   // failure retains evidence instead of losing it during fixture teardown.
  290 |   await test.info().attach(`${filename}-before-geometry`, { body: JSON.stringify(before), contentType: 'application/json' })
  291 |   await expect(heading).toBeInViewport()
  292 |   await expect.poll(async () => (await geometry()).headingHit).toBe(true)
  293 |   expect(before.left).toBeGreaterThanOrEqual(0)
  294 |   expect(before.right).toBeLessThanOrEqual(before.viewport)
  295 |   expect(before.left).toBeGreaterThanOrEqual(before.ownerLeft)
  296 |   expect(before.right).toBeLessThanOrEqual(before.ownerRight)
  297 |   expect(before.headingTop).toBeGreaterThanOrEqual(before.topbarBottom)
  298 |   expect(before.headingBottom).toBeLessThanOrEqual(before.viewportHeight)
  299 |   expect(before.scrollWidth).toBeLessThanOrEqual(before.clientWidth + 1)
  300 |   const tableOwner = panel.getByLabel('Document metric populations', { exact: true })
  301 |   await expect(tableOwner).toBeVisible()
  302 |   expect(await tableOwner.evaluate(node => getComputedStyle(node).overflowX)).toBe('auto')
  303 |   await panel.screenshot({ path: test.info().outputPath(filename), animations: 'disabled' })
  304 |   const after = await geometry()
  305 |   await test.info().attach(`${filename}-after-geometry`, { body: JSON.stringify(after), contentType: 'application/json' })
  306 |   expect(after.left).toBeGreaterThanOrEqual(0)
  307 |   expect(after.right).toBeLessThanOrEqual(after.viewport)
  308 |   expect(after.left).toBeGreaterThanOrEqual(after.ownerLeft)
  309 |   expect(after.right).toBeLessThanOrEqual(after.ownerRight)
  310 |   expect(after.headingTop).toBeGreaterThanOrEqual(after.topbarBottom)
  311 |   expect(after.headingBottom).toBeLessThanOrEqual(after.viewportHeight)
  312 |   expect(after.headingLeft).toBeGreaterThanOrEqual(after.left)
  313 |   expect(after.headingRight).toBeLessThanOrEqual(after.right)
  314 |   expect(after.headingHit).toBe(true)
  315 |   await test.info().attach(`${filename}-geometry`, { body: JSON.stringify({ before, after }), contentType: 'application/json' })
  316 | }
  317 | 
  318 | test('live document populations preserve overlapping provisional history, actual cohorts and missing-peer coverage', async ({ page, trackRun }) => {
  319 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  320 |   const request = page.context().request, workspace = await createWorkspace(request), script = await seedScript(request, workspace)
  321 |   const id = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId,
  322 |     release_id: workspace.releaseId, name: 'Independent live document evidence', duration: '2m', groups: [{ name: 'browser', script_id: script, vusers: 1 }] } })))
  323 |   trackRun(id)
  324 |   let missingPeer = false
  325 |   await page.route(`**/api/v1/tests/${id}`, async route => {
  326 |     if (route.request().method() !== 'GET') return route.continue()
  327 |     const response = await route.fetch(), record = await response.json()
  328 |     await route.fulfill({ response, json: { ...record, status: 'RUNNING', live_metrics: { total_transactions: 999, total_errors: 0, active_vusers: 1, transactions: {},
  329 |       browser_documents: missingPeer ? { version: 2, metrics: null, incomplete: true, dropped_observations: 0 }
  330 |         : { version: 2, metrics: [documentTuple], incomplete: false, dropped_observations: 0 } } } })
  331 |   })
  332 |   await page.goto(`/#/test/${id}?tab=diagnostics&${scopeQuery(workspace)}`)
  333 |   await page.reload()
  334 |   const panel = page.getByRole('region', { name: 'Browser document evidence', exact: true })
  335 |   await expect(panel).toBeVisible()
  336 |   await expect(panel).toContainText('First seen provisional')
  337 |   await expect(panel).toContainText('overlap terminal history')
  338 |   await expect(panel).toContainText('No score is calculated')
  339 |   const cells = panel.getByLabel('Document metric populations', { exact: true }).locator('tbody > tr').first().locator(':scope > td')
  340 |   await expect(cells).toHaveCount(13)
  341 |   expect(await cells.allTextContents()).toEqual(['CLS', 'chromium · desktopGroup: checkout · Region: local', 'Main frame', 'Navigation', '2', '1', '3', '1', '2', '0 score', '0 score', '0 score', expect.any(String)])
  342 |   await panel.getByText('Inspect CLS tuple 1', { exact: true }).click()
  343 |   await expect(panel).toContainText(documentAlgorithmSHA)
  344 |   await expect(panel).toContainText('0.5 / 1')
  345 |   await expect(panel).toContainText('No / No / No')
  346 |   await expect(panel.getByRole('table', { name: 'CLS qualified final histogram for tuple 1', exact: true })).toContainText('2')
  347 |   await expect(page.locator('.browser-metrics-card')).toHaveCount(0)
  348 |   await captureDocumentPanel(page, panel, 'browser-document-live.png')
  349 |   missingPeer = true
  350 |   await expect(panel).toContainText('Document evidence is incomplete. 0 observations were dropped')
  351 |   await expect(panel).toContainText('No document metric tuples were recorded')
  352 |   await expect(panel.locator('table')).toHaveCount(0)
  353 |   await expect(panel).not.toContainText('0 score')
  354 | })
  355 | 
  356 | test('archive document evidence retains latest pending, censored and unavailable measurements independently from actions', async ({ page, trackRun }) => {
  357 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  358 |   const request = page.context().request, workspace = await createWorkspace(request), script = await seedScript(request, workspace)
  359 |   const id = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId,
  360 |     release_id: workspace.releaseId, name: 'Independent archive document evidence', duration: '1s', groups: [{ name: 'browser', script_id: script, vusers: 1 }] } })))
  361 |   trackRun(id)
  362 |   const source = 'a'.repeat(32)
  363 |   const baseObservation = (index: number) => ({ version: 2, source_generation: source, document_id: `${source}-document-${index}`, lifecycle_id: `${source}-lifecycle-${index}`,
  364 |     frame_id: `${source}-frame-${index}`, navigation_id: `${source}-navigation-${index}`, measurement_id: `${source}-document-${index}/${source}-lifecycle-${index}/cls`, revision: 2,
  365 |     metric: 'cls', algorithm: documentAlgorithm, algorithm_sha256: documentAlgorithmSHA, unit: 'score', engine: 'chromium', group: 'checkout', region: 'local',
  366 |     device_profile: 'desktop', cohort: actualDocumentCohort, frame_scope: 'main_frame', lifecycle_phase: 'navigation', state: 'final', boundary: 'pagehide',
  367 |     window_start: '2026-10-05T10:00:00.000000001Z', window_end: '2026-10-05T10:00:01.000000002Z', captured_at: '2026-10-05T10:00:01.000000003Z', available: true, qualified: true, value: 0 })
  368 |   const noInput = { ...baseObservation(4), metric: 'inp', unit: 'ms', measurement_id: `${source}-document-4/${source}-lifecycle-4/inp`, available: false, qualified: false, value: null, unavailable_reason: 'no_interaction' }
  369 |   await page.route(`**/api/v1/tests/${id}`, async route => { if (route.request().method() !== 'GET') return route.continue(); const response = await route.fetch(); const record = await response.json(); await route.fulfill({ response, json: { ...record, status: 'COMPLETE', archive: { transaction_count: 999 }, started_at: '2026-10-05T10:00:00Z', ended_at: '2026-10-05T10:00:01Z' } }) })
  370 |   await page.route(`**/api/v1/tests/${id}/archive/report*`, route => route.fulfill({ json: {
  371 |     manifest: { test_id: id, test_name: 'Independent archive document evidence', status: 'COMPLETE', provenance: { started_at: '2026-10-05T10:00:00Z', duration: '1s', total_vusers: 1 } },
  372 |     overview: { transactions: 999 }, by_action: {}, browser_documents: { version: 2, metrics: [documentTuple, { ...documentTuple, frame_scope: 'child_frame' }, { ...documentTuple, lifecycle_phase: 'bfcache_restore' }], incomplete: true, dropped_observations: 7 },
  373 |     browser_document_evidence: [baseObservation(1), { ...baseObservation(2), state: 'provisional', boundary: 'hidden', qualified: false },
  374 |       { ...baseObservation(3), state: 'censored', boundary: 'cancelled', qualified: false, available: false, value: null, unavailable_reason: 'source_censored' }, noInput], browser_document_evidence_truncated: true,
  375 |   } }))
  376 |   await page.goto(`/#/report/${id}?section=diagnostics&${scopeQuery(workspace)}`)
  377 |   await page.reload()
  378 |   await page.getByRole('tab', { name: 'Diagnostics', exact: true }).click()
  379 |   await page.getByText('Browser evidence', { exact: true }).click()
  380 |   const panel = page.getByRole('region', { name: 'Browser document evidence', exact: true })
  381 |   await expect(panel).toContainText('Retained pending')
  382 |   await expect(panel).not.toContainText('First seen provisional')
  383 |   await expect(panel).toContainText('Document evidence is incomplete. 7 observations were dropped')
  384 |   await expect(panel).toContainText('Additional source observations are excluded from this view')
> 385 |   await expect(panel.getByLabel('Document metric populations', { exact: true }).locator('tbody > tr')).toHaveCount(3)
      |                                                                                                        ^ Error: expect(locator).toHaveCount(expected) failed
  386 |   const observations = panel.getByLabel('Retained document measurements', { exact: true })
  387 |   await expect(observations.locator('tbody > tr')).toHaveCount(4)
  388 |   await expect(observations).toContainText('provisional · hidden')
  389 |   await expect(observations).toContainText('censored · cancelled')
  390 |   await expect(observations).toContainText('Unavailable · no_interaction')
  391 |   await observations.getByText('Inspect document observation 1', { exact: true }).click()
  392 |   await expect(observations).toContainText(`${source}-document-1`)
  393 |   await expect(observations).toContainText('2026-10-05T10:00:00.000000001Z')
  394 |   await expect(observations).toContainText(documentAlgorithmSHA)
  395 |   await expect(panel).not.toContainText('999 eligible')
  396 |   await panel.getByText('Inspect CLS tuple 1', { exact: true }).click()
  397 |   await captureDocumentPanel(page, panel, 'browser-document-archive.png')
  398 | })
  399 | 
```