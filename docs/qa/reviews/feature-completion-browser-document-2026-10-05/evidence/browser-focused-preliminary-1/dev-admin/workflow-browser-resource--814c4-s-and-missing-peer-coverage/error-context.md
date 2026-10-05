# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: workflow-browser-resource-evidence.spec.ts >> live document populations preserve overlapping provisional history, actual cohorts and missing-peer coverage
- Location: e2e/workflow-browser-resource-evidence.spec.ts:318:1

# Error details

```
Error: expect(received).toBeGreaterThanOrEqual(expected)

Expected: >= 147
Received:    20.578125
```

# Page snapshot

```yaml
- generic [ref=e1]:
  - generic [ref=e3]:
    - complementary [ref=e4]:
      - button "APEX Load dashboard" [ref=e5] [cursor=pointer]:
        - img "APEX Load" [ref=e6]
      - button "Workspace context. e2e-1791195655309-121534 domain / e2e-1791195655309-121534 project / e2e-1791195655309-121534 application / e2e-1791195655309-121534 1.0" [ref=e12] [cursor=pointer]:
        - generic [ref=e13]: ◈
      - navigation "Primary navigation" [ref=e14]:
        - button "New Test" [ref=e16] [cursor=pointer]:
          - img [ref=e18]
        - generic [ref=e21]:
          - paragraph [ref=e22]: Operate
          - button "Dashboard" [ref=e24] [cursor=pointer]:
            - img [ref=e26]
          - button "Command Center" [ref=e32] [cursor=pointer]:
            - img [ref=e34]
          - button "Results" [ref=e39] [cursor=pointer]:
            - img [ref=e41]
          - button "Monitoring" [ref=e43] [cursor=pointer]:
            - img [ref=e45]
          - button "Activity" [ref=e48] [cursor=pointer]:
            - img [ref=e50]
        - generic [ref=e52]:
          - paragraph [ref=e53]: Build
          - button "Scripts" [ref=e55] [cursor=pointer]:
            - img [ref=e57]
          - button "Scenarios" [ref=e61] [cursor=pointer]:
            - img [ref=e63]
        - generic [ref=e65]:
          - paragraph [ref=e66]: Administer
          - button "Repository" [ref=e68] [cursor=pointer]:
            - img [ref=e70]
          - button "Access" [ref=e74] [cursor=pointer]:
            - img [ref=e76]
          - button "Settings" [ref=e81] [cursor=pointer]:
            - img [ref=e83]
        - generic [ref=e86]:
          - paragraph [ref=e87]: Support
          - button "Help & documentation" [ref=e89] [cursor=pointer]:
            - img [ref=e91]
      - generic [ref=e95]:
        - 'button "Preview permissions. Current mode: admin" [ref=e97] [cursor=pointer]':
          - generic [ref=e98]: DEV
          - generic [ref=e99]: Preview permissions
        - generic [ref=e100]:
          - generic "Live" [ref=e101]
          - 'button "Build info: local · dev" [ref=e104] [cursor=pointer]'
    - main "Run Results" [active] [ref=e106]:
      - status [ref=e107]: Run Results
      - generic "Dashboard controls" [ref=e108]:
        - heading "Run Results" [level=1] [ref=e110]
        - generic [ref=e111]:
          - link "Help for this page" [ref=e112] [cursor=pointer]:
            - /url: "#/docs?guide=user-guide&article=results"
          - generic "Deployment identity" [ref=e113]:
            - generic [ref=e114]: Browser · Server
          - combobox "Dashboard color theme" [ref=e117]:
            - option "System (Auto)"
            - option "Dark Theme" [selected]
            - option "Light Theme"
            - option "Solarized Dark"
            - option "Solarized Light"
            - option "Monokai Dimmed"
      - generic [ref=e118]:
        - generic [ref=e119]:
          - generic [ref=e120]:
            - paragraph [ref=e121]: Run results · LOC-RUN-20261005-006
            - heading "Independent live document evidence" [level=1] [ref=e122]
            - paragraph [ref=e123]:
              - generic [ref=e124]: ABORTED
              - text: · Configured 1 VUsers for 2m · Lifecycle elapsed Not recorded
          - generic [ref=e125]:
            - button "Compare" [disabled] [ref=e126]
            - button "Export report" [disabled] [ref=e127]
            - button "Test notes" [ref=e128] [cursor=pointer]
        - region "Execution outcome" [ref=e129]:
          - heading "Execution aborted" [level=2] [ref=e130]
          - paragraph [ref=e131]: test cancelled before start
          - paragraph [ref=e132]: Execution status is separate from sampled transaction success and performance acceptance.
        - region "Evidence availability" [ref=e133]:
          - generic [ref=e134]:
            - heading "Saved archive unavailable" [level=2] [ref=e135]
            - paragraph [ref=e136]: Retained controller records are available below. Archive publication progress is unknown; these records do not establish complete workload or performance evidence.
          - button "Check again" [ref=e137] [cursor=pointer]
        - group [ref=e138]:
          - generic "Run timeline annotations" [ref=e139] [cursor=pointer]
        - tablist "Result sections" [ref=e140]:
          - tab "Summary" [ref=e141] [cursor=pointer]
          - tab "Performance" [ref=e142] [cursor=pointer]
          - tab "Baseline Comparison" [ref=e143] [cursor=pointer]
          - tab "Diagnostics" [selected] [ref=e144] [cursor=pointer]
          - tab "Evidence" [ref=e145] [cursor=pointer]
          - tab "Execution" [ref=e146] [cursor=pointer]
        - tabpanel "Diagnostics" [ref=e147]:
          - region "Retained diagnostics" [ref=e148]:
            - heading "Retained diagnostics" [level=2] [ref=e149]
            - paragraph [ref=e150]: These records describe the last retained sample, not current system health or a successful run outcome.
            - generic [ref=e151]:
              - generic [ref=e152]:
                - term [ref=e153]: Metric capture
                - definition [ref=e154]: Not recorded
              - generic [ref=e155]:
                - term [ref=e156]: Generator publish
                - definition [ref=e157]: Not recorded
              - generic [ref=e158]:
                - term [ref=e159]: Controller receive
                - definition [ref=e160]: Not recorded
            - paragraph [ref=e161]: Archived transaction diagnostics are unavailable. Review the recorded execution reason and retained events for the available failure evidence.
          - region "Execution events" [ref=e162]:
            - heading "Execution events" [level=2] [ref=e163]
            - generic [ref=e164]:
              - generic [ref=e165]:
                - term [ref=e166]: Created
                - definition [ref=e167]: 10/5/2026, 6:20:55 AM
              - generic [ref=e168]:
                - term [ref=e169]: Started
                - definition [ref=e170]: Not recorded
              - generic [ref=e171]:
                - term [ref=e172]: Ended
                - definition [ref=e173]: 10/5/2026, 6:20:55 AM
            - list [ref=e174]:
              - listitem [ref=e175]:
                - strong [ref=e176]: Run created
                - generic [ref=e177]: 10/5/2026, 6:20:55 AM · success
              - listitem [ref=e178]:
                - strong [ref=e179]: Abort requested
                - generic [ref=e180]: 10/5/2026, 6:20:55 AM · success
          - group [ref=e181]:
            - generic "Configuration and infrastructure evidence" [ref=e182] [cursor=pointer]
  - generic [ref=e183]: "0"
```

# Test source

```ts
  210 |       by_action: {}, resource_peaks: { cpu_process_pct: 0 },
  211 |       browser_by_device: devices,
  212 |       browser_by_engine: index === 0 ? {} : { malformed: null, nested: { samples: 1, browser_engine: {}, avg_lcp_ms: -1, avg_inp_ms: 0 } },
  213 |       browser_by_platform: index === 0 ? {} : { malformed: 5, nested: { samples: 1, device_platform: {}, browser_engine: {}, avg_lcp_ms: null, avg_inp_ms: 0 } },
  214 |     } })
  215 |     if (index >= 0 && path.endsWith('/summaries')) return route.fulfill({ json: { summaries: [] } })
  216 |     if (route.request().method() !== 'GET' || path !== '/api/v1/tests') return route.continue()
  217 |     const response = await route.fetch()
  218 |     const payload = await response.json()
  219 |     return route.fulfill({ response, json: { ...payload, tests: payload.tests.map((run: { id: string }) => runs.some(candidate => candidate.id === run.id) ? { ...run, status: 'COMPLETE', archive: { transaction_count: 1 } } : run) } })
  220 |   })
  221 |   await page.goto(`/#/results?${scopeQuery(workspace)}`)
  222 |   await page.reload()
  223 |   for (const run of runs) await page.getByRole('checkbox', { name: `Select ${run.name}`, exact: true }).check()
  224 |   await page.getByRole('button', { name: 'Run Comparison', exact: true }).click()
  225 |   const panel = (name: string) => page.locator('.results-compare-panel').filter({ has: page.getByRole('heading', { name, exact: true }) })
  226 |   const mixedPanel = panel(runs[0].name)
  227 |   const directPanel = panel(runs[1].name)
  228 |   await expect(mixedPanel.locator('.browser-device-card')).toHaveCount(6)
  229 |   // Exact tuple identities avoid alias display-name collisions in the DOM.
  230 |   const keyed = (owner: Locator, tuple: string[]) => owner.locator(`[data-cohort-key=${JSON.stringify(JSON.stringify(tuple))}]`)
  231 |   await expect(keyed(mixedPanel, ['device', 'malformed'])).toContainText('Malformed summary: expected an object')
  232 |   await expect(keyed(mixedPanel, ['device', 'primitive'])).toContainText('LCP n/a')
  233 |   await expect(keyed(mixedPanel, ['device', 'nested'])).toContainText('unknown · browser · unknown tier')
  234 |   await expect(keyed(mixedPanel, ['device', 'nested'])).toContainText('LCP n/a')
  235 |   await expect(mixedPanel.locator('.results-compare-metric').filter({ has: page.getByText('Avg LCP', { exact: true }) })).toContainText('n/a')
  236 |   for (const [mode, identity, alias] of [['Engine', 'chromium', 'Chromium'], ['Platform', 'ios', 'iOS']] as const) {
  237 |     await page.getByRole('tab', { name: mode, exact: true }).click()
  238 |     const kind = mode.toLowerCase()
  239 |     await expect(mixedPanel.locator('.browser-device-card')).toHaveCount(5)
  240 |     const mixed = keyed(mixedPanel, [kind, identity])
  241 |     await expect(mixed).toContainText('LCP n/a')
  242 |     await expect(mixed).toContainText('INP 0.0 ms')
  243 |     await expect(mixed).toContainText('Unavailable members are retained')
  244 |     await expect(mixed).not.toContainText('400.0 ms')
  245 |     await expect(keyed(mixedPanel, [kind, alias])).toContainText('LCP 8000.0 ms')
  246 |     for (const profile of ['malformed', 'primitive', 'nested']) await expect(keyed(mixedPanel, [kind, 'unavailable', profile])).toContainText('LCP n/a')
  247 |     await expect(keyed(directPanel, [kind, 'malformed'])).toContainText('Malformed summary: expected an object')
  248 |     await expect(keyed(directPanel, [kind, 'nested'])).toContainText('INP 0.0 ms')
  249 |     await expect(keyed(directPanel, [kind, 'nested'])).toContainText('LCP n/a')
  250 |   }
  251 | })
  252 | 
  253 | const documentAlgorithm = 'web-vitals/5.1.0'
  254 | const documentAlgorithmSHA = '8890ec954757ea8e4801f49958afb76e2b8837c47fec5b16598fc20cbd49c220'
  255 | const actualDocumentCohort = { version: 1, browser_version: '136.0.1', platform: 'linux', viewport_width: 800, viewport_height: 600,
  256 |   headless: true, mobile: false, touch: false, device_scale_factor: 0.5, cpu_throttle_rate: 1, network_latency_ms: 0,
  257 |   network_bandwidth_kbps: 0, shaping_applied: true, cache_enabled: false, cache_content_urls: false, check_newer_versions: false,
  258 |   new_user_policy: 'persistent_context', new_user_policy_applied: true }
  259 | const documentTuple = { metric: 'cls', algorithm: documentAlgorithm, algorithm_sha256: documentAlgorithmSHA, unit: 'score', engine: 'chromium',
  260 |   group: 'checkout', region: 'local', device_profile: 'desktop', cohort: actualDocumentCohort, frame_scope: 'main_frame', lifecycle_phase: 'navigation',
  261 |   eligible_final: 2, unavailable_final: 1, unqualified_final: 3, censored: 1, provisional: 2,
  262 |   excluded_reasons: { no_observation: 1, cohort_unavailable: 3, source_censored: 1 },
  263 |   distribution: { count: 2, sum: 0, min: 0, max: 0, buckets: [2, ...Array<number>(191).fill(0)] } }
  264 | 
  265 | async function captureDocumentPanel(page: Page, panel: Locator, filename: string) {
  266 |   await page.setViewportSize({ width: 760, height: 1600 })
  267 |   await panel.scrollIntoViewIfNeeded()
  268 |   const heading = panel.getByRole('heading', { name: 'Browser document lifecycle evidence', exact: true })
  269 |   await panel.evaluate(node => {
  270 |     const owner = node.closest('.main-content') as HTMLElement | null
  271 |     if (!owner) throw new Error('Document evidence has no known scroll owner')
  272 |     owner.scrollTop += node.getBoundingClientRect().top - (document.querySelector('.main-topbar')?.getBoundingClientRect().bottom ?? 0) - 16
  273 |   })
  274 |   const geometry = () => panel.evaluate(node => {
  275 |     const box = node.getBoundingClientRect(), heading = node.querySelector('h3')!.getBoundingClientRect()
  276 |     const owner = node.closest('.main-content') as HTMLElement | null
  277 |     if (!owner) throw new Error('Document evidence has no known scroll owner')
  278 |     const ownerBox = owner.getBoundingClientRect()
  279 |     const hit = document.elementFromPoint(heading.left + heading.width / 2, heading.top + heading.height / 2)
  280 |     return { left: box.left, right: box.right, viewport: innerWidth, viewportHeight: innerHeight,
  281 |       scrollWidth: node.scrollWidth, clientWidth: node.clientWidth, ownerLeft: Math.max(0, ownerBox.left + owner.clientLeft),
  282 |       ownerRight: Math.min(innerWidth, ownerBox.left + owner.clientLeft + owner.clientWidth),
  283 |       topbarBottom: Math.max(0, document.querySelector('.main-topbar')?.getBoundingClientRect().bottom ?? 0),
  284 |       headingLeft: heading.left, headingRight: heading.right, headingTop: heading.top, headingBottom: heading.bottom,
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
> 310 |   expect(after.headingTop).toBeGreaterThanOrEqual(after.topbarBottom)
      |                            ^ Error: expect(received).toBeGreaterThanOrEqual(expected)
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
  385 |   await expect(panel.getByLabel('Document metric populations', { exact: true }).locator('tbody > tr')).toHaveCount(3)
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