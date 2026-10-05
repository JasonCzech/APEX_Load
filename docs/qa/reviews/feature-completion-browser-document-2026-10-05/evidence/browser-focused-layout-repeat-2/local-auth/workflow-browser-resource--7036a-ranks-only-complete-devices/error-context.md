# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: workflow-browser-resource-evidence.spec.ts >> archived browser legacy summary preserves mixed-cohort gaps in engine CSV and ranks only complete devices
- Location: e2e/workflow-browser-resource-evidence.spec.ts:132:1

# Error details

```
Error: expect(received).toBeGreaterThanOrEqual(expected)

Expected: >= 127
Received:    124.53125
```

# Page snapshot

```yaml
- generic [active] [ref=e1]:
  - generic [ref=e3]:
    - complementary [ref=e4]:
      - button "APEX Load dashboard" [ref=e5] [cursor=pointer]:
        - img "APEX Load" [ref=e6]
      - button "Workspace context. e2e-1791196398432-687912 domain / e2e-1791196398432-687912 project / e2e-1791196398432-687912 application / e2e-1791196398432-687912 1.0" [ref=e12] [cursor=pointer]:
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
        - generic "Local System Administrator • admin@apexload.local • System Admin" [ref=e96]:
          - generic [ref=e97]: LS
          - generic [ref=e98]:
            - generic [ref=e99]: Local System Administrator
            - generic [ref=e100]: System Admin
            - button "Sign out" [ref=e101] [cursor=pointer]
        - generic [ref=e102]:
          - generic "Live" [ref=e103]
          - 'button "Build info: local · dev" [ref=e106] [cursor=pointer]'
    - main "Run Results" [ref=e108]:
      - status [ref=e109]: Run Results
      - generic "Dashboard controls" [ref=e110]:
        - heading "Run Results" [level=1] [ref=e112]
        - generic [ref=e113]:
          - link "Help for this page" [ref=e114] [cursor=pointer]:
            - /url: "#/docs?guide=user-guide&article=results"
          - generic "Deployment identity" [ref=e115]:
            - generic [ref=e116]: Browser · Server
          - combobox "Dashboard color theme" [ref=e119]:
            - option "System (Auto)"
            - option "Dark Theme" [selected]
            - option "Light Theme"
            - option "Solarized Dark"
            - option "Solarized Light"
            - option "Monokai Dimmed"
      - generic [ref=e120]:
        - generic [ref=e121]:
          - generic [ref=e122]:
            - paragraph [ref=e123]: Run results · LOC-RUN-20261005-003
            - heading "Archived browser availability" [level=1] [ref=e124]
            - paragraph [ref=e125]:
              - generic [ref=e126]: ABORTED
              - text: · Configured 1 VUsers for 1s · Lifecycle elapsed 1s · Archived evidence elapsed Not recorded
          - generic [ref=e127]:
            - button "Compare" [ref=e128] [cursor=pointer]
            - button "Export report" [ref=e129] [cursor=pointer]
            - button "Test notes" [ref=e130] [cursor=pointer]
        - region "Execution outcome" [ref=e131]:
          - heading "Execution aborted" [level=2] [ref=e132]
          - paragraph [ref=e133]: No termination detail was recorded.
          - paragraph [ref=e134]: Execution status is separate from sampled transaction success and performance acceptance.
        - region "Analysis context" [ref=e135]:
          - generic [ref=e136]:
            - text: Metric population
            - combobox "Metric population" [ref=e137]:
              - option "Requests / actions" [selected]
              - option "Named transactions"
              - option "Unclassified legacy records"
          - paragraph [ref=e138]: Requests / actions · Unknown samples · Whole captured run. Legacy interval graphs have no identified named transaction population; their values are separate from these KPIs. Resource curves show the load generators.
        - group [ref=e139]:
          - generic "Run timeline annotations" [ref=e140] [cursor=pointer]
        - tablist "Result sections" [ref=e141]:
          - tab "Summary" [ref=e142] [cursor=pointer]
          - tab "Performance" [ref=e143] [cursor=pointer]
          - tab "Baseline Comparison" [ref=e144] [cursor=pointer]
          - tab "Diagnostics" [selected] [ref=e145] [cursor=pointer]
          - tab "Evidence" [ref=e146] [cursor=pointer]
          - tab "Execution" [ref=e147] [cursor=pointer]
        - tabpanel "Diagnostics" [ref=e148]:
          - region "Report unavailable" [ref=e149]:
            - heading "Report could not be loaded" [level=2] [ref=e150]
            - alert [ref=e151]: archive not found
            - paragraph [ref=e152]: Retry to load the archived findings. Execution details remain available.
            - link "Back to results" [ref=e153] [cursor=pointer]:
              - /url: "#/results?projectId=d2f44633-79df-4dcc-b894-2ab281fb5f71&applicationId=fc3d6cae-f7a7-44dd-8bb9-d7f6b1e03b21&releaseId=582fc5f0-36b4-4eae-974a-62cfe217091b"
            - button "Retry report" [ref=e154] [cursor=pointer]
            - button "View execution details" [ref=e155] [cursor=pointer]
          - generic [ref=e156]:
            - generic [ref=e158]:
              - heading "Diagnostics" [level=2] [ref=e159]
              - paragraph [ref=e160]: Network and generator resource evidence.
            - group [ref=e161]:
              - generic "Network phases" [ref=e162] [cursor=pointer]
            - group [ref=e163]:
              - generic "Resource peaks" [ref=e164] [cursor=pointer]
          - paragraph [ref=e166]: archive not found
          - paragraph [ref=e168]: archive not found
          - group [ref=e169]:
            - generic "Browser evidence" [ref=e170] [cursor=pointer]
            - paragraph [ref=e172]: archive not found
            - paragraph [ref=e174]: archive not found
          - group [ref=e175]:
            - generic "Execution path analysis" [ref=e176] [cursor=pointer]
  - generic [ref=e177]: (primitive)
```

# Test source

```ts
  1   | import { readFile } from 'node:fs/promises'
  2   | import type { Locator, Page } from '@playwright/test'
  3   | import { test, expect, seedScript, json, idOf, login } from './support/workflow'
  4   | import { createWorkspace, scopeQuery } from './support/workspace'
  5   | test('browser resource evidence filters retained failures and inspects action attribution', async ({ page, trackRun }) => {
  6   |  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  7   |  const request=page.context().request;const workspace=await createWorkspace(request);const script=await seedScript(request,workspace)
  8   |  const id=idOf(await json(await request.post('/api/v1/tests',{data:{project_id:workspace.projectId,application_id:workspace.applicationId,release_id:workspace.releaseId,name:'Resource inspection run',duration:'1s',groups:[{name:'browser',script_id:script,vusers:1}]}})));trackRun(id)
  9   |  await page.route(`**/api/v1/tests/${id}`,async route=>{
  10  |  if(route.request().method()!=='GET')return route.continue()
  11  |  const response=await route.fetch();const body=await response.json()
  12  |  return route.fulfill({response,json:{...body,status:'COMPLETE',archive:{transaction_count:1},started_at:'2026-10-01T00:00:00Z',ended_at:'2026-10-01T00:00:01Z'}})
  13  |  })
  14  |  await page.route(`**/api/v1/tests/${id}/archive/report*`,route=>route.fulfill({json:{manifest:{test_id:id,test_name:'Resource inspection run',status:'COMPLETE',provenance:{started_at:'2026-10-01T00:00:00Z',duration:'1s',total_vusers:1}},overview:{transactions:1},by_action:{},browser_resources:[{timestamp:'2026-10-01T00:00:01Z',action_name:'Navigate',url:'https://example.test/ok.js',duration_ms:0,bytes_received:0,status_code:200},{timestamp:'2026-10-01T00:00:01Z',action_name:'Click',page_url:'https://example.test/page',url:'https://example.test/fail.js',failure_kind:'transport',method:'GET'},{timestamp:'2026-10-01T00:00:01Z',action_name:'Click',url:'https://example.test/service.js',status_code:503,failure_kind:'http',method:'GET'}]}}))
  15  |  await page.goto(`/#/report/${id}?section=diagnostics&${scopeQuery(workspace)}`);await page.reload()
  16  |  await page.getByRole('tab',{name:'Diagnostics',exact:true}).click()
  17  |  await page.getByText('Browser evidence',{exact:true}).click()
  18  |  const resources=page.getByRole('region',{name:'Browser resource evidence',exact:true})
  19  |  await expect(resources).toContainText('0.0 ms');await expect(resources).toContainText('0 B')
  20  |  await resources.getByLabel('Failed resources only').check();await expect(resources).toContainText('2 of 3 retained resources');await expect(resources).not.toContainText('ok.js');await expect(resources).toContainText('503 · http');await expect(resources).toContainText('transport')
  21  |  await resources.getByText('Inspect resource 1',{exact:true}).click();await expect(resources).toContainText('https://example.test/page');await expect(resources).toContainText('GET')
  22  |  await resources.getByLabel('Resource action',{exact:true}).selectOption('Navigate');await expect(resources).toContainText('No retained resources match')
  23  |  await resources.screenshot({path:test.info().outputPath('resource-evidence.png')})
  24  | })
  25  | 
  26  | 
  27  | // Polling route handlers must drain before the owned fixture context closes.
  28  | test.afterEach(async ({ page }) => { await page.unrouteAll({ behavior: 'wait' }) })
  29  | 
  30  | const explicitZeroSummary = { samples: 1, avg_lcp_ms: 0, avg_inp_ms: 0, avg_cls_score: 0, avg_long_task_count: 0, avg_blocking_resources: 0, avg_browser_process_cpu_pct: 0, avg_browser_process_mem_mb: 0 }
  31  | 
  32  | 
  33  | async function captureReadableAssessment(page: Page, assessment: Locator, filename: string) {
  34  |   // Retain the original narrow viewport/whole-box checks. The region may be
  35  |   // taller than this viewport, so use a taller canvas only for its artifact.
  36  |   await page.setViewportSize({ width: 760, height: 900 })
  37  |   await assessment.scrollIntoViewIfNeeded()
  38  |   const geometry = await assessment.evaluate(node => { const rect = node.getBoundingClientRect(); return { left: rect.left, right: rect.right, width: window.innerWidth, scrollWidth: node.scrollWidth, clientWidth: node.clientWidth } })
  39  |   expect(geometry.left).toBeGreaterThanOrEqual(0)
  40  |   expect(geometry.right).toBeLessThanOrEqual(geometry.width)
  41  |   expect(geometry.scrollWidth).toBeLessThanOrEqual(geometry.clientWidth + 1)
  42  |   await page.setViewportSize({ width: 760, height: 1600 })
  43  |   const positioning = await assessment.evaluate(node => {
  44  |     const owner = node.closest('.main-content') as HTMLElement | null
  45  |     if (!owner) throw new Error('Browser assessment has no known main scroll owner')
  46  |     const header = document.querySelector('.main-topbar')
  47  |     const headerBottom = header?.getBoundingClientRect().bottom ?? 0
  48  |     const before = node.getBoundingClientRect()
  49  |     owner.scrollTop += before.top - headerBottom - 16
  50  |     return { beforeTop: before.top, headerBottom, scrollOwner: owner.className }
  51  |   })
  52  |   const heading = assessment.getByRole('heading')
  53  |   const hit = async () => heading.evaluate(node => { const rect = node.getBoundingClientRect(); const hit = document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2); return { hit: hit === node || node.contains(hit), top: rect.top, bottom: rect.bottom } })
  54  |   await expect.poll(async () => (await hit()).hit).toBe(true)
> 55  |   expect((await hit()).top).toBeGreaterThanOrEqual(positioning.headerBottom)
      |                             ^ Error: expect(received).toBeGreaterThanOrEqual(expected)
  56  |   await test.info().attach(`${filename}-scroll-position`, { body: JSON.stringify(positioning), contentType: 'application/json' })
  57  |   const captureGeometry = () => assessment.evaluate(node => {
  58  |     const rect = node.getBoundingClientRect()
  59  |     const ancestors = []
  60  |     for (let parent = node.parentElement; parent; parent = parent.parentElement) {
  61  |       const box = parent.getBoundingClientRect()
  62  |       const style = getComputedStyle(parent)
  63  |       ancestors.push({ tag: parent.tagName, className: parent.className, left: box.left, right: box.right, scrollLeft: parent.scrollLeft, scrollWidth: parent.scrollWidth, clientWidth: parent.clientWidth, clientLeft: parent.clientLeft, overflowX: style.overflowX })
  64  |     }
  65  |     return { left: rect.left, right: rect.right, viewport: window.innerWidth, ancestors }
  66  |   })
  67  |   const capturedGeometry = await captureGeometry()
  68  |   await test.info().attach(`${filename}-capture-geometry`, { body: JSON.stringify(capturedGeometry), contentType: 'application/json' })
  69  |   expect(capturedGeometry.left).toBeGreaterThanOrEqual(0)
  70  |   expect(capturedGeometry.right).toBeLessThanOrEqual(capturedGeometry.viewport)
  71  |   for (const ancestor of capturedGeometry.ancestors.filter(ancestor => ancestor.overflowX !== 'visible')) {
  72  |     expect(capturedGeometry.left, `Card left edge clipped by ${ancestor.className}`).toBeGreaterThanOrEqual(ancestor.left + ancestor.clientLeft - 1)
  73  |     expect(capturedGeometry.right, `Card right edge clipped by ${ancestor.className}`).toBeLessThanOrEqual(ancestor.left + ancestor.clientLeft + ancestor.clientWidth + 1)
  74  |   }
  75  |   await assessment.screenshot({ path: test.info().outputPath(filename) })
  76  |   await expect.poll(async () => (await hit()).hit).toBe(true)
  77  |   const afterCapture = await captureGeometry()
  78  |   await test.info().attach(`${filename}-after-capture-geometry`, { body: JSON.stringify(afterCapture), contentType: 'application/json' })
  79  |   expect(afterCapture.left).toBeGreaterThanOrEqual(0)
  80  |   expect(afterCapture.right).toBeLessThanOrEqual(afterCapture.viewport)
  81  |   for (const ancestor of afterCapture.ancestors.filter(ancestor => ancestor.overflowX !== 'visible')) {
  82  |     expect(afterCapture.left, `Capture left edge clipped by ${ancestor.className}`).toBeGreaterThanOrEqual(ancestor.left + ancestor.clientLeft - 1)
  83  |     expect(afterCapture.right, `Capture right edge clipped by ${ancestor.className}`).toBeLessThanOrEqual(ancestor.left + ancestor.clientLeft + ancestor.clientWidth + 1)
  84  |   }
  85  | }
  86  | 
  87  | test('live browser legacy summary refuses unavailable values and distinguishes explicit zero without standards qualification', async ({ page, trackRun }) => {
  88  |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  89  |   const request = page.context().request
  90  |   const workspace = await createWorkspace(request)
  91  |   const script = await seedScript(request, workspace)
  92  |   const id = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Browser summary availability', duration: '2m', groups: [{ name: 'browser', script_id: script, vusers: 1 }] } })))
  93  |   trackRun(id)
  94  |   let state: 'missing' | 'invalid' | 'zero' | 'empty' = 'missing'
  95  |   await page.route(`**/api/v1/tests/${id}`, async route => {
  96  |     if (route.request().method() !== 'GET') return route.continue()
  97  |     const response = await route.fetch()
  98  |     const record = await response.json()
  99  |     const browser = state === 'missing' ? { samples: 1 } : state === 'invalid' ? { ...explicitZeroSummary, avg_lcp_ms: -1, avg_inp_ms: null } : { ...explicitZeroSummary, samples: state === 'empty' ? 0 : 1 }
  100 |     await route.fulfill({ response, json: { ...record, status: 'RUNNING', live_metrics: { total_transactions: 1, total_errors: 0, active_vusers: 1, transactions: {}, browser, browser_by_device: { unavailable: { samples: 1 }, malformed: null, primitive: 5, complete_zero: explicitZeroSummary } } } })
  101 |   })
  102 |   await page.goto(`/#/test/${id}?tab=diagnostics&${scopeQuery(workspace)}`)
  103 |   await page.reload()
  104 |   const assessment = page.getByRole('region', { name: 'Browser legacy summary assessment', exact: true })
  105 |   await expect(assessment).toBeVisible()
  106 |   await expect(assessment).toContainText('Unscored')
  107 |   await expect(assessment).toContainText('0/7 required fields available')
  108 |   await expect(assessment).not.toContainText('100/100')
  109 |   await expect(assessment).toContainText('not a standards-qualified Core Web Vitals score')
  110 |   await expect(assessment).toHaveAttribute('data-model-version', 'legacy-summary-availability-v2')
  111 |   await expect(page.locator('.browser-metrics-card .browser-device-card').filter({ hasText: 'Malformed summary: expected an object' })).toHaveCount(2)
  112 |   const counter = page.locator('.browser-metrics-card > .goal-status-header .goal-status-badge')
  113 |   await expect(counter).toHaveClass(/neutral/)
  114 |   await expect(counter).not.toHaveClass(/healthy/)
  115 |   state = 'invalid'
  116 |   await expect(assessment).toContainText('5/7 required fields available')
  117 |   await expect(assessment.locator('[data-availability="invalid"]')).toHaveCount(2)
  118 |   await expect(assessment).toContainText('Value must be finite and nonnegative')
  119 |   state = 'zero'
  120 |   await expect(assessment).toContainText('Within legacy thresholds · 100/100 legacy estimate')
  121 |   await expect(assessment).toContainText('7/7 required fields available')
  122 |   await expect(assessment).toContainText('0.000')
  123 |   await expect(assessment).toContainText('Weakest complete summary: complete_zero')
  124 |   await expect(assessment).not.toContainText('Healthy')
  125 |   state = 'empty'
  126 |   await expect(assessment.getByRole('heading', { name: 'Unscored', exact: true })).toBeVisible()
  127 |   await expect(assessment).toContainText('Sample count must be a positive safe integer')
  128 |   await expect(assessment).not.toContainText('100/100 legacy estimate')
  129 |   await captureReadableAssessment(page, assessment, 'browser-live-unscored.png')
  130 | })
  131 | 
  132 | test('archived browser legacy summary preserves mixed-cohort gaps in engine CSV and ranks only complete devices', async ({ page, trackRun }) => {
  133 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  134 |   const request = page.context().request
  135 |   const workspace = await createWorkspace(request)
  136 |   const script = await seedScript(request, workspace)
  137 |   const id = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Archived browser availability', duration: '1s', groups: [{ name: 'browser', script_id: script, vusers: 1 }] } })))
  138 |   trackRun(id)
  139 |   await page.route(`**/api/v1/tests/${id}`, async route => {
  140 |     if (route.request().method() !== 'GET') return route.continue()
  141 |     const response = await route.fetch()
  142 |     const record = await response.json()
  143 |     await route.fulfill({ response, json: { ...record, status: 'COMPLETE', archive: { transaction_count: 1 }, started_at: '2026-10-05T00:00:00Z', ended_at: '2026-10-05T00:00:01Z' } })
  144 |   })
  145 |   await page.route(`**/api/v1/tests/${id}/archive/report*`, route => route.fulfill({ json: {
  146 |     manifest: { test_id: id, test_name: 'Archived browser availability', status: 'COMPLETE', provenance: { started_at: '2026-10-05T00:00:00Z', duration: '1s', total_vusers: 1 } },
  147 |     overview: { transactions: 1, browser: { samples: 100, avg_lcp_ms: 4200, avg_cls_score: 0 } }, by_action: {},
  148 |     browser_by_device: {
  149 |       complete_slow: { ...explicitZeroSummary, samples: 10, avg_lcp_ms: 4000, browser_engine: 'chromium', device_platform: 'desktop' },
  150 |       missing_lcp: { samples: 90, avg_inp_ms: 0, avg_cls_score: 0, browser_engine: 'chromium', device_platform: 'desktop' },
  151 |       malformed: null, primitive: 5,
  152 |     },
  153 |   } }))
  154 |   await page.goto(`/#/report/${id}?section=diagnostics&${scopeQuery(workspace)}`)
  155 |   await page.reload()
```