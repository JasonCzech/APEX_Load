# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: workflow-retained-resource-inspector.spec.ts >> retained resource inspector paginates beyond report previews and preserves native zero, false and independent clocks
- Location: e2e/workflow-retained-resource-inspector.spec.ts:56:1

# Error details

```
TimeoutError: locator.selectOption: Timeout 15000ms exceeded.
Call log:
  - waiting for getByRole('region', { name: 'Retained archive resource inspector', exact: true }).getByLabel('Resources per page', { exact: true })

```

# Page snapshot

```yaml
- generic [ref=e3]:
  - complementary [ref=e4]:
    - button "APEX Load dashboard" [ref=e5] [cursor=pointer]:
      - img "APEX Load" [ref=e6]
      - generic [ref=e11]: APEX Load
    - button "Workspace context. e2e-1791217414210-468212 domain / e2e-1791217414210-468212 project / e2e-1791217414210-468212 application / e2e-1791217414210-468212 1.0" [ref=e13] [cursor=pointer]:
      - generic [ref=e14]: ◈
      - generic [ref=e15]:
        - generic [ref=e16]: Workspace
        - generic [ref=e17]: e2e-1791217414210-468212 domain / e2e-1791217414210-468212 project / e2e-1791217414210-468212 application / e2e-1791217414210-468212 1.0
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
          - paragraph [ref=e140]: Run results · LOC-RUN-20261005-001
          - heading "Retained resource page inspection" [level=1] [ref=e141]
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
            - /url: "#/results?projectId=b81e7151-d31f-4570-b4cc-c245abb69ba1&applicationId=3990ec37-337a-47dc-81b2-48622d9be163&releaseId=3bcafbae-c517-4fa5-8c24-724e8c315198"
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
          - generic "Browser evidence" [ref=e187] [cursor=pointer]
          - paragraph [ref=e189]: archive not found
          - paragraph [ref=e191]: archive not found
        - group [ref=e192]:
          - generic "Execution path analysis" [ref=e193] [cursor=pointer]
```

# Test source

```ts
  1   | import type { Page } from '@playwright/test'
  2   | import { test, expect, seedScript, json, idOf, login } from './support/workflow'
  3   | import { createWorkspace, scopeQuery } from './support/workspace'
  4   | import { resourcePageFixture } from '../src/components/browserResourcePage.testFixtures'
  5   | import type { BrowserResourcePage, BrowserResourcePageItem } from '../src/types/browserResourcePage'
  6   | 
  7   | // These bounded transport fixtures qualify the real dashboard/client flow.
  8   | // Native acquisition and opened-object archive integrity are separate Go/runtime gates.
  9   | async function fixture(page: Page, trackRun: (id: string) => void) {
  10  |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  11  |   const request = page.context().request
  12  |   const workspace = await createWorkspace(request)
  13  |   const script = await seedScript(request, workspace)
  14  |   const id = idOf(await json(await request.post('/api/v1/tests', { data: {
  15  |     project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId,
  16  |     name: 'Retained resource page inspection', duration: '1s', groups: [{ name: 'resources', script_id: script, vusers: 1 }],
  17  |   } })))
  18  |   trackRun(id)
  19  |   await page.route(`**/api/v1/tests/${id}`, async route => {
  20  |     if (route.request().method() !== 'GET') return route.continue()
  21  |     const response = await route.fetch()
  22  |     return route.fulfill({ response, json: { ...await response.json(), status: 'COMPLETE',
  23  |       archive: { test_id: id, transaction_count: 2, created_at: '2026-10-05T12:00:00Z' },
  24  |       started_at: '2026-10-05T12:00:00Z', ended_at: '2026-10-05T12:00:01Z' } })
  25  |   })
  26  |   await page.route(`**/api/v1/tests/${id}/archive/report*`, route => route.fulfill({ json: {
  27  |     manifest: { test_id: id, test_name: 'Retained resource page inspection', status: 'COMPLETE',
  28  |       created_at: '2026-10-05T12:00:01Z', transaction_count: 2, transactions_file: 'transactions.jsonl',
  29  |       provenance: { project_id: workspace.projectId, started_at: '2026-10-05T12:00:00Z', duration: '1s', total_vusers: 1 } },
  30  |     overview: { transactions: 2 }, by_action: {}, browser_resources: [],
  31  |   } }))
  32  |   return { id, workspace }
  33  | }
  34  | 
  35  | function observations(): BrowserResourcePageItem[] {
  36  |   const base = resourcePageFixture().items[0]
  37  |   return Array.from({ length: 80 }, (_, index) => ({ ...structuredClone(base),
  38  |     locator: { transaction_ordinal: 1, resource_ordinal: index + 1, branch: 'network_entries' },
  39  |     url: `https://resource.example.test/retained-${index + 1}.js`, group: 'browser-group',
  40  |     status_code: index === 0 ? 0 : 200, duration_ms: index, bytes_received: index,
  41  |     browser_evidence: { ...structuredClone(base.browser_evidence!), request_id: `${base.browser_evidence!.source_generation}-request-${index + 1}` },
  42  |   }))
  43  | }
  44  | 
  45  | function pageFor(id: string, projectID: string, rows: BrowserResourcePageItem[], matched: number, next?: string): BrowserResourcePage {
  46  |   const value = resourcePageFixture()
  47  |   value.test_id = id; value.project_id = projectID; value.items = rows
  48  |   value.population.matched_retained_observations = matched
  49  |   delete value.next_cursor
  50  |   if (next) value.next_cursor = next
  51  |   return value
  52  | }
  53  | 
  54  | test.afterEach(async ({ page }) => { await page.unrouteAll({ behavior: 'wait' }) })
  55  | 
  56  | test('retained resource inspector paginates beyond report previews and preserves native zero, false and independent clocks', async ({ page, trackRun }) => {
  57  |   const { id, workspace } = await fixture(page, trackRun)
  58  |   const rows = observations()
  59  |   const cursor = `page-two.${'A'.repeat(43)}`
  60  |   const queries: Array<Record<string, string>> = []
  61  |   await page.route(`**/api/v1/tests/${id}/archive/browser-resources*`, async route => {
  62  |     const q = new URL(route.request().url()).searchParams
  63  |     queries.push(Object.fromEntries(q))
  64  |     expect(q.get('project_id')).toBe(workspace.projectId)
  65  |     expect(route.request().method()).toBe('GET')
  66  |     const matched = q.get('action_name') === 'Absent action' ? [] : rows
  67  |     const limit = Number(q.get('limit') || 50)
  68  |     const offset = q.has('cursor') ? 50 : 0
  69  |     if (q.has('cursor')) expect(q.get('cursor')).toBe(cursor)
  70  |     await route.fulfill({ json: pageFor(id, workspace.projectId, matched.slice(offset, offset + limit), matched.length,
  71  |       offset + limit < matched.length ? cursor : undefined) })
  72  |   })
  73  |   await page.goto(`/#/report/${id}?section=diagnostics&${scopeQuery(workspace)}`)
  74  |   await page.reload()
  75  |   await page.getByRole('tab', { name: 'Diagnostics', exact: true }).click()
  76  |   await page.getByText('Browser evidence', { exact: true }).click()
  77  |   await page.getByRole('button', { name: 'Inspect retained archive resources', exact: true }).click()
  78  |   const inspector = page.getByRole('region', { name: 'Retained archive resource inspector', exact: true })
  79  |   await expect(inspector).toContainText('Page 1: 50 retained observations')
  80  |   await expect(inspector).toContainText('80 retained entry observations')
  81  |   await expect(inspector).toContainText('0 ms / 0 B')
  82  |   await expect(inspector).toContainText('capture coverage are unavailable')
  83  |   const table = inspector.getByRole('region', { name: 'Retained archive resources table', exact: true })
  84  |   await expect(table.locator('tbody tr')).toHaveCount(50)
  85  |   await inspector.getByRole('button', { name: 'Next resource page', exact: true }).click()
  86  |   await expect(inspector).toContainText('Page 2: 30 retained observations')
  87  |   await expect(table.locator('tbody tr')).toHaveCount(30)
  88  |   await expect(table).toContainText('retained-80.js')
  89  |   await expect(inspector.getByRole('button', { name: 'Next resource page', exact: true })).toBeDisabled()
  90  |   await inspector.getByRole('button', { name: 'First page / retry', exact: true }).click()
  91  |   await expect(inspector).toContainText('Page 1: 50 retained observations')
  92  |   await inspector.getByLabel('Capturing action', { exact: true }).fill('Absent action')
  93  |   await inspector.getByRole('button', { name: 'Apply resource filters', exact: true }).click()
  94  |   await expect(inspector).toContainText('0 match these filters out of 80')
  95  |   await expect(inspector).toContainText('No retained observations match these filters')
  96  |   expect(queries.at(-1)).not.toHaveProperty('cursor')
  97  |   await inspector.getByLabel('Capturing action', { exact: true }).fill('')
> 98  |   await inspector.getByLabel('Resources per page', { exact: true }).selectOption('25')
      |                                                                     ^ TimeoutError: locator.selectOption: Timeout 15000ms exceeded.
  99  |   await inspector.getByRole('button', { name: 'Apply resource filters', exact: true }).click()
  100 |   await expect(table.locator('tbody tr')).toHaveCount(25)
  101 |   await page.setViewportSize({ width: 760, height: 1600 })
  102 |   await inspector.getByRole('heading', { name: 'Retained resource observations', exact: true }).scrollIntoViewIfNeeded()
  103 |   const geometry = () => inspector.evaluate(node => {
  104 |     const box = node.getBoundingClientRect()
  105 |     const heading = node.querySelector('h3')!
  106 |     const title = heading.getBoundingClientRect()
  107 |     const owner = node.closest('.main-content')
  108 |     if (!owner) throw new Error('Resource inspector has no main scroll owner')
  109 |     const visible = owner.getBoundingClientRect()
  110 |     const topbar = document.querySelector('.main-topbar')?.getBoundingClientRect().bottom ?? 0
  111 |     const hit = document.elementFromPoint(title.left + title.width / 2, title.top + title.height / 2)
  112 |     return { left: box.left, right: box.right, width: window.innerWidth, height: window.innerHeight,
  113 |       ownerLeft: visible.left, ownerRight: visible.right, scrollWidth: node.scrollWidth, clientWidth: node.clientWidth,
  114 |       headingTop: title.top, headingBottom: title.bottom, topbar, headingHit: hit === heading || heading.contains(hit) }
  115 |   })
  116 |   const assertGeometry = (value: Awaited<ReturnType<typeof geometry>>) => {
  117 |     expect(value.left).toBeGreaterThanOrEqual(Math.max(0, value.ownerLeft))
  118 |     expect(value.right).toBeLessThanOrEqual(Math.min(value.width, value.ownerRight))
  119 |     expect(value.scrollWidth).toBeLessThanOrEqual(value.clientWidth + 1)
  120 |     expect(value.headingTop).toBeGreaterThanOrEqual(value.topbar)
  121 |     expect(value.headingBottom).toBeLessThanOrEqual(value.height)
  122 |     expect(value.headingHit).toBe(true)
  123 |   }
  124 |   const beforeCapture = await geometry()
  125 |   await test.info().attach('resource-inspector-overview-before', { body: JSON.stringify(beforeCapture), contentType: 'application/json' })
  126 |   assertGeometry(beforeCapture)
  127 |   await page.screenshot({ path: test.info().outputPath('retained-resource-inspector-overview.png'), animations: 'disabled' })
  128 |   const afterCapture = await geometry()
  129 |   await test.info().attach('resource-inspector-overview-after', { body: JSON.stringify(afterCapture), contentType: 'application/json' })
  130 |   assertGeometry(afterCapture)
  131 |   await table.focus()
  132 |   const before = await table.evaluate(node => ({ left: node.scrollLeft, mainLeft: node.closest('.main-content')?.scrollLeft, width: node.clientWidth, scrollWidth: node.scrollWidth }))
  133 |   expect(before.scrollWidth).toBeGreaterThan(before.width)
  134 |   await page.keyboard.press('ArrowRight')
  135 |   await expect.poll(() => table.evaluate(node => node.scrollLeft)).toBeGreaterThan(before.left)
  136 |   const after = await table.evaluate(node => ({ left: node.scrollLeft, mainLeft: node.closest('.main-content')?.scrollLeft }))
  137 |   expect(after.mainLeft).toBe(before.mainLeft)
  138 |   await test.info().attach('resource-inspector-keyboard-scroll', { body: JSON.stringify({ before, after }), contentType: 'application/json' })
  139 |   await table.getByText('Inspect retained resource 1', { exact: true }).click()
  140 |   const native = table.locator('.browser-native-request-evidence').first()
  141 |   await expect(native).toContainText('false (observed)')
  142 |   await expect(native).toContainText('2026-10-05T12:00:00.123456789Z')
  143 |   await expect(native).toContainText('0 ms · clock unverified')
  144 |   await expect(native).toContainText('may overlap')
  145 |   await expect(native).toContainText('0 B')
  146 |   await expect(native.locator('.measured-point')).toHaveCount(1)
  147 |   await native.scrollIntoViewIfNeeded()
  148 |   const point = await native.locator('.measured-point').evaluate(node => {
  149 |     const box = node.getBoundingClientRect(); const track = node.parentElement!.getBoundingClientRect()
  150 |     const style = getComputedStyle(node)
  151 |     return { left: box.left, right: box.right, width: box.width, height: box.height, trackLeft: track.left, trackRight: track.right,
  152 |       display: style.display, visibility: style.visibility, opacity: style.opacity, borderWidth: style.borderLeftWidth }
  153 |   })
  154 |   await test.info().attach('resource-inspector-measured-zero-point', { body: JSON.stringify(point), contentType: 'application/json' })
  155 |   expect(point.width).toBeGreaterThan(0); expect(point.height).toBeGreaterThan(0)
  156 |   expect(point.left).toBeGreaterThanOrEqual(point.trackLeft); expect(point.right).toBeLessThanOrEqual(point.trackRight + 1)
  157 |   expect(point.display).not.toBe('none'); expect(point.visibility).toBe('visible'); expect(Number(point.opacity)).toBeGreaterThan(0)
  158 |   expect(parseFloat(point.borderWidth)).toBeGreaterThan(0)
  159 |   await expect(native.locator('.browser-resource-phases li').filter({ hasText: 'request wait' }).locator('.browser-resource-phase-bar')).toHaveCount(0)
  160 |   await native.screenshot({ path: test.info().outputPath('retained-resource-native-detail.png') })
  161 |   await test.info().attach('resource-inspector-transport-queries', { body: JSON.stringify(queries), contentType: 'application/json' })
  162 | })
  163 | 
  164 | test('retained resource inspector refuses a mismatched page and retries without publishing unavailable evidence as zero', async ({ page, trackRun }) => {
  165 |   const { id, workspace } = await fixture(page, trackRun)
  166 |   let state: 'foreign' | 'unavailable' | 'zero' = 'foreign'
  167 |   await page.route(`**/api/v1/tests/${id}/archive/browser-resources*`, async route => {
  168 |     const body = pageFor(id, workspace.projectId, [], 0)
  169 |     if (state === 'foreign') body.test_id = 'foreign-private-run'
  170 |     if (state === 'unavailable') body.population = { availability: 'unavailable', transaction_records: 2, retained_entry_observations: null, matched_retained_observations: null }
  171 |     body.source_integrity = 'legacy_scan_fingerprint'
  172 |     await route.fulfill({ json: body })
  173 |   })
  174 |   await page.goto(`/#/report/${id}?section=diagnostics&${scopeQuery(workspace)}`)
  175 |   await page.getByRole('tab', { name: 'Diagnostics', exact: true }).click()
  176 |   await page.getByText('Browser evidence', { exact: true }).click()
  177 |   await page.getByRole('button', { name: 'Inspect retained archive resources', exact: true }).click()
  178 |   const inspector = page.getByRole('region', { name: 'Retained archive resource inspector', exact: true })
  179 |   await expect(inspector.getByRole('alert')).toContainText('Unable to load')
  180 |   await expect(inspector).not.toContainText('foreign-private-run')
  181 |   await expect(inspector.getByRole('button', { name: 'Next resource page', exact: true })).toBeDisabled()
  182 |   state = 'unavailable'
  183 |   await inspector.getByRole('button', { name: 'First page / retry', exact: true }).click()
  184 |   await expect(inspector).toContainText('The archive resource projection is unavailable')
  185 |   await expect(inspector).not.toContainText('0 retained observations')
  186 |   await expect(inspector).not.toContainText('0 retained entry observations')
  187 |   await expect(inspector).toContainText('producer integrity is unverified')
  188 |   state = 'zero'
  189 |   await inspector.getByRole('button', { name: 'First page / retry', exact: true }).click()
  190 |   await expect(inspector).toContainText('0 match these filters out of 80')
  191 |   await expect(inspector).toContainText('No retained observations match these filters')
  192 |   await expect(inspector).not.toContainText('Healthy')
  193 | })
  194 | 
```