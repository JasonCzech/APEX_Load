# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: native-horizontal-diagnostic/workflow-analysis-preparation.spec.ts >> imported comparison preserves both exact identity selectors across save and reopen
- Location: e2e/native-horizontal-diagnostic/workflow-analysis-preparation.spec.ts:508:1

# Error details

```
Error: expect(received).toBe(expected) // Object.is equality

Expected: true
Received: false

Call Log:
- Timeout 15000ms exceeded while waiting on the predicate
```

# Page snapshot

```yaml
- generic [ref=e1]:
  - generic [ref=e3]:
    - complementary [ref=e4]:
      - button "APEX Load dashboard" [ref=e5] [cursor=pointer]:
        - img "APEX Load" [ref=e6]
      - button "Workspace context. e2e-1791190933458-982193 domain / e2e-1791190933458-982193 project / e2e-1791190933458-982193 application / e2e-1791190933458-982193 1.0" [ref=e12] [cursor=pointer]:
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
    - main "Analysis" [ref=e108]:
      - status [ref=e109]: Analysis
      - generic "Dashboard controls" [ref=e110]:
        - heading "Analysis" [level=1] [ref=e112]
        - generic [ref=e114]:
          - tablist "Monitor categories" [ref=e115]:
            - tab "Overview" [ref=e116] [cursor=pointer]:
              - generic [ref=e117]: Overview
            - tab "Fleet" [ref=e118] [cursor=pointer]:
              - generic [ref=e119]: Fleet
            - tab "Telemetry" [ref=e120] [cursor=pointer]:
              - generic [ref=e121]: Telemetry
            - tab "Alerts" [ref=e122] [cursor=pointer]:
              - generic [ref=e123]: Alerts
            - tab "Analysis" [selected] [ref=e124] [cursor=pointer]:
              - generic [ref=e125]: Analysis
            - tab "Schedules" [ref=e126] [cursor=pointer]:
              - generic [ref=e127]: Schedules
          - button "1 advisory warning. View system advisories" [ref=e128] [cursor=pointer]: 1 advisory warning
        - generic [ref=e129]:
          - generic "Deployment identity" [ref=e130]:
            - generic [ref=e131]: Browser · Server
          - combobox "Dashboard color theme" [ref=e134]:
            - option "System (Auto)"
            - option "Dark Theme" [selected]
            - option "Light Theme"
            - option "Solarized Dark"
            - option "Solarized Light"
            - option "Monokai Dimmed"
      - generic [ref=e135]:
        - status [ref=e136]: Readiness Degraded. Kubernetes API Not applicable. Metadata Store Ready. Cache Backend Ready.
        - generic [ref=e137]:
          - generic [ref=e138]: "Project: e2e-1791190933458-982193 project · Control-plane health is deployment-wide."
          - link "Controller settings" [ref=e139] [cursor=pointer]:
            - /url: "#/settings?setting=platform&returnTo=%23%2Fmonitoring%3Fcategory%3Danalysis%26projectId%3D9c45ac0d-3b60-4aaf-9428-bf13dc484286%26applicationId%3Df74020e8-dcc2-42cf-8f36-8f96f3a3d727%26releaseId%3D1aadab62-5faf-4844-acf8-34bbf4d244f6&projectId=9c45ac0d-3b60-4aaf-9428-bf13dc484286&applicationId=f74020e8-dcc2-42cf-8f36-8f96f3a3d727&releaseId=1aadab62-5faf-4844-acf8-34bbf4d244f6"
          - link "Authentication" [ref=e140] [cursor=pointer]:
            - /url: "#/access?setting=authentication&returnTo=%23%2Fmonitoring%3Fcategory%3Danalysis%26projectId%3D9c45ac0d-3b60-4aaf-9428-bf13dc484286%26applicationId%3Df74020e8-dcc2-42cf-8f36-8f96f3a3d727%26releaseId%3D1aadab62-5faf-4844-acf8-34bbf4d244f6&projectId=9c45ac0d-3b60-4aaf-9428-bf13dc484286&applicationId=f74020e8-dcc2-42cf-8f36-8f96f3a3d727&releaseId=1aadab62-5faf-4844-acf8-34bbf4d244f6"
        - tabpanel "Analysis monitors" [ref=e141]:
          - generic [ref=e143]:
            - generic [ref=e144]:
              - generic [ref=e145]: PT
              - generic [ref=e146]: Analysis workspace
            - region "Analysis workspace" [ref=e147]:
              - heading "Parquet analysis" [level=2] [ref=e148]
              - paragraph [ref=e149]: Import metric observations, inspect their schema, and compare measurements on a UTC timeline.
              - generic [ref=e150]:
                - complementary "Datasets" [ref=e151]:
                  - heading "Datasets and imports" [level=3] [ref=e152]
                  - button "New import" [ref=e153] [cursor=pointer]
                  - list [ref=e154]:
                    - listitem [ref=e155]:
                      - button "Identity A · ready" [ref=e156] [cursor=pointer]
                    - listitem [ref=e157]:
                      - button "Identity B · ready" [ref=e158] [cursor=pointer]
                - generic [ref=e159]:
                  - heading "Identity A" [level=3] [ref=e160]
                  - paragraph [ref=e161]: "State: ready · Accepted: 2 · Rejected: 0"
                  - group "Query dataset" [ref=e162]:
                    - generic [ref=e163]: Query dataset
                    - generic [ref=e164]:
                      - text: Metric
                      - combobox "Metric" [ref=e165]: latency
                    - generic [ref=e166]:
                      - text: From (UTC)
                      - textbox "From (UTC)" [ref=e167]
                    - generic [ref=e168]:
                      - text: To (UTC)
                      - textbox "To (UTC)" [ref=e169]
                    - generic [ref=e170]:
                      - text: Aggregation
                      - combobox "Aggregation" [ref=e171]:
                        - option "avg" [selected]
                        - option "min"
                        - option "max"
                        - option "sum"
                        - option "count"
                        - option "p50"
                        - option "p95"
                        - option "p99"
                        - option "rate"
                    - group "Dataset identity filters" [ref=e172]:
                      - generic [ref=e173]: Dataset identity filters
                      - paragraph [ref=e174]: Select one source and label combination when a metric has several series. An empty dimension value matches an explicit empty label.
                      - generic [ref=e175]:
                        - text: Source ID
                        - textbox "Source ID" [ref=e176]
                      - generic [ref=e177]:
                        - text: Host
                        - textbox "Host" [ref=e178]
                      - generic [ref=e179]:
                        - text: Service
                        - textbox "Service" [ref=e180]
                      - button "Add dimension filter" [ref=e181] [cursor=pointer]
                    - button "Show series" [disabled] [ref=e182] [cursor=pointer]
                  - region "Dataset comparison" [ref=e183]:
                    - heading "Compare and correlate" [level=3] [ref=e184]
                    - paragraph [ref=e185]: Correlation measures association. Missing evidence stays missing; it does not establish a cause.
                    - group "Evidence selection" [ref=e186]:
                      - generic [ref=e187]: Evidence selection
                      - generic [ref=e188]:
                        - text: Saved comparison
                        - combobox "Saved comparison" [ref=e189]:
                          - option "Choose a saved comparison…"
                          - option "Owned selectors · revision 1"
                          - option "Selector copy · revision 1" [selected]
                      - generic [ref=e190]:
                        - text: Dataset metric
                        - textbox "Dataset metric" [ref=e191]: latency
                      - group "Dataset identity filters" [ref=e192]:
                        - generic [ref=e193]: Dataset identity filters
                        - paragraph [ref=e194]: Select one source and label combination when a metric has several series. An empty dimension value matches an explicit empty label.
                        - generic [ref=e195]:
                          - text: Source ID
                          - textbox "Source ID" [ref=e196]: agent-a
                        - generic [ref=e197]:
                          - text: Host
                          - textbox "Host" [ref=e198]: host-a
                        - generic [ref=e199]:
                          - text: Service
                          - textbox "Service" [ref=e200]: service-a
                        - generic [ref=e201]:
                          - generic [ref=e202]:
                            - text: Filter 1 name
                            - textbox "Filter 1 name" [ref=e203]: a/b~c
                          - generic [ref=e204]:
                            - text: Filter 1 value
                            - textbox "Filter 1 value" [ref=e205]
                          - button "Remove filter 1" [ref=e206] [cursor=pointer]
                        - generic [ref=e207]:
                          - generic [ref=e208]:
                            - text: Filter 2 name
                            - textbox "Filter 2 name" [ref=e209]: transaction
                          - generic [ref=e210]:
                            - text: Filter 2 value
                            - textbox "Filter 2 value" [ref=e211]: Lookup
                          - button "Remove filter 2" [ref=e212] [cursor=pointer]
                        - generic [ref=e213]:
                          - generic [ref=e214]:
                            - text: Filter 3 name
                            - textbox "Filter 3 name" [ref=e215]: protocol
                          - generic [ref=e216]:
                            - text: Filter 3 value
                            - textbox "Filter 3 value" [ref=e217]: grpc
                          - button "Remove filter 3" [ref=e218] [cursor=pointer]
                        - generic [ref=e219]:
                          - generic [ref=e220]:
                            - text: Filter 4 name
                            - textbox "Filter 4 name" [ref=e221]: xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
                          - generic [ref=e222]:
                            - text: Filter 4 value
                            - textbox "Filter 4 value" [active] [ref=e223]: yyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyy
                          - button "Remove filter 4" [ref=e224] [cursor=pointer]
                        - button "Add dimension filter" [ref=e225] [cursor=pointer]
                      - generic [ref=e226]:
                        - text: Compare with
                        - combobox "Compare with" [ref=e227]:
                          - option "Select evidence…"
                          - option "Identity B" [selected]
                          - option "Managed run"
                      - group "Comparison identity filters" [ref=e228]:
                        - generic [ref=e229]: Comparison identity filters
                        - paragraph [ref=e230]: Select one source and label combination when a metric has several series. An empty dimension value matches an explicit empty label.
                        - generic [ref=e231]:
                          - text: Source ID
                          - textbox "Source ID" [ref=e232]: agent-b
                        - generic [ref=e233]:
                          - text: Host
                          - textbox "Host" [ref=e234]: host-b
                        - generic [ref=e235]:
                          - text: Service
                          - textbox "Service" [ref=e236]: service-b
                        - generic [ref=e237]:
                          - generic [ref=e238]:
                            - text: Filter 1 name
                            - textbox "Filter 1 name" [ref=e239]: __proto__
                          - generic [ref=e240]:
                            - text: Filter 1 value
                            - textbox "Filter 1 value" [ref=e241]
                          - button "Remove filter 1" [ref=e242] [cursor=pointer]
                        - generic [ref=e243]:
                          - generic [ref=e244]:
                            - text: Filter 2 name
                            - textbox "Filter 2 name" [ref=e245]: group
                          - generic [ref=e246]:
                            - text: Filter 2 value
                            - textbox "Filter 2 value" [ref=e247]: two
                          - button "Remove filter 2" [ref=e248] [cursor=pointer]
                        - button "Add dimension filter" [ref=e249] [cursor=pointer]
                      - generic [ref=e250]:
                        - text: Comparison metric
                        - textbox "Comparison metric" [ref=e251]: latency
                      - generic [ref=e252]:
                        - text: From (UTC)
                        - textbox "From (UTC)" [ref=e253]: 2026-01-01T00:00
                      - generic [ref=e254]:
                        - text: To (UTC)
                        - textbox "To (UTC)" [ref=e255]: 2026-01-01T00:01
                      - generic [ref=e256]:
                        - text: Bucket seconds
                        - spinbutton "Bucket seconds" [ref=e257]: "10"
                      - generic [ref=e258]:
                        - text: Dataset aggregation
                        - combobox "Dataset aggregation" [ref=e259]:
                          - option "avg"
                          - option "min"
                          - option "max"
                          - option "sum"
                          - option "count"
                          - option "rate"
                          - option "p50"
                          - option "p95" [selected]
                          - option "p99"
                      - generic [ref=e260]:
                        - text: Comparison aggregation
                        - combobox "Comparison aggregation" [ref=e261]:
                          - option "avg"
                          - option "min"
                          - option "max"
                          - option "sum"
                          - option "count"
                          - option "rate"
                          - option "p50"
                          - option "p95" [selected]
                          - option "p99"
                      - generic [ref=e262]:
                        - text: Analysis
                        - combobox "Analysis" [ref=e263]:
                          - option "Overlay compatible measurements" [selected]
                          - option "Lagged correlation"
                      - button "Analyze comparison" [ref=e264] [cursor=pointer]
                      - generic [ref=e265]:
                        - text: Saved comparison name
                        - textbox "Saved comparison name" [ref=e266]
                      - button "Save comparison" [disabled] [ref=e267] [cursor=pointer]
                    - 'figure "Dataset comparison. Only displayed series and measurements are exported; missing values remain empty. {\"selection\":{\"left\":{\"dataset_id\":\"00000000-0000-0000-0000-000000000011\",\"source_id\":\"agent-a\",\"host\":\"host-a\",\"service\":\"service-a\",\"dimensions\":{\"a/b~c\":\"\",\"transaction\":\"Lookup\",\"protocol\":\"grpc\"},\"metric\":\"latency\",\"aggregation\":\"p95\"},\"right\":{\"dataset_id\":\"00000000-0000-0000-0000-000000000012\",\"source_id\":\"agent-b\",\"host\":\"host-b\",\"service\":\"service-b\",\"dimensions\":{\"__proto__\":\"\",\"group\":\"two\"},\"metric\":\"latency\",\"aggregation\":\"p95\"},\"from\":\"2026-01-01T00:00:00.000Z\",\"to\":\"2026-01-01T00:01:00.000Z\",\"bucket_seconds\":10,\"max_lag_seconds\":0,\"mode\":\"overlay\"},\"left_coverage\":1,\"left_buckets\":1,\"right_coverage\":1,\"right_buckets\":1}" [ref=e269]':
                      - generic "Dataset comparison export actions" [ref=e271]:
                        - button "Export Dataset comparison data as CSV" [ref=e272] [cursor=pointer]: CSV
                        - button "Export Dataset comparison image as SVG" [ref=e273] [cursor=pointer]: SVG
                      - paragraph [ref=e274]: "Dataset comparison. Only displayed series and measurements are exported; missing values remain empty. {\"selection\":{\"left\":{\"dataset_id\":\"00000000-0000-0000-0000-000000000011\",\"source_id\":\"agent-a\",\"host\":\"host-a\",\"service\":\"service-a\",\"dimensions\":{\"a/b~c\":\"\",\"transaction\":\"Lookup\",\"protocol\":\"grpc\"},\"metric\":\"latency\",\"aggregation\":\"p95\"},\"right\":{\"dataset_id\":\"00000000-0000-0000-0000-000000000012\",\"source_id\":\"agent-b\",\"host\":\"host-b\",\"service\":\"service-b\",\"dimensions\":{\"__proto__\":\"\",\"group\":\"two\"},\"metric\":\"latency\",\"aggregation\":\"p95\"},\"from\":\"2026-01-01T00:00:00.000Z\",\"to\":\"2026-01-01T00:01:00.000Z\",\"bucket_seconds\":10,\"max_lag_seconds\":0,\"mode\":\"overlay\"},\"left_coverage\":1,\"left_buckets\":1,\"right_coverage\":1,\"right_buckets\":1}"
                      - application [ref=e278]:
                        - generic [ref=e289]:
                          - generic [ref=e292]: 01-01 00:00:00
                          - generic [ref=e293]:
                            - generic [ref=e295]: 0 ms
                            - generic [ref=e297]: 0.25 ms
                            - generic [ref=e299]: 0.5 ms
                            - generic [ref=e301]: 0.75 ms
                            - generic [ref=e303]: 1 ms
                  - button "Delete dataset" [ref=e304] [cursor=pointer]
        - status [ref=e305]:
          - generic [ref=e306]: Updated available data 5:02:28 AM
  - generic [ref=e307]: "0"
```

# Test source

```ts
  1   | import { readFile } from "node:fs/promises"
  2   | import type { Locator } from '@playwright/test'
  3   | import { test, expect, seedScript, json, idOf, login } from '../support/workflow'
  4   | import { createWorkspace, scopeQuery } from '../support/workspace'
  5   | 
  6   | // Project authorization and navigation use the real controller. Bounded
  7   | // transport fixtures exercise the complete UI; native normalization and the
  8   | // independent zero-traffic counters are covered by Go integration tests.
  9   | test.afterEach(async ({ page }) => { await page.unrouteAll({ behavior: 'wait' }) })
  10  | 
  11  | // Playwright follows an option's enclosing label to its select when checking
  12  | // enabled/disabled state. Check the native option itself. Its programmatic
  13  | // selectOption can bypass disabled choices, so exercise malformed-draft
  14  | // admission separately from the native disabled contract.
  15  | async function assertProtectedColumnsUnavailable(column: Locator, area: Locator) {
  16  |   await expect(column).toBeEnabled()
  17  |   for (const name of ['credentials.password (protected)', 'api.key (protected)']) {
  18  |     await expect(column.getByRole('option', { name, exact: true })).toHaveJSProperty('disabled', true)
  19  |   }
  20  |   const previous = await column.inputValue()
  21  |   const validate = area.getByRole('button', { name: 'Validate and import', exact: true })
  22  |   const save = area.getByRole('button', { name: 'Save mapping', exact: true })
  23  |   await expect(validate).toBeEnabled(); await expect(save).toBeEnabled()
  24  |   for (const protectedValue of ['["credentials","password"]', '["api","key"]']) {
  25  |     await column.selectOption(protectedValue)
  26  |     await expect(column).toHaveValue(protectedValue)
  27  |     const disclosure = area.getByRole('alert').filter({ hasText: 'Protected credential columns cannot become analytical timestamps, measurement values, metric names or identity labels.' })
  28  |     await expect(disclosure).toHaveCount(1); await expect(disclosure).toBeVisible()
  29  |     await expect(validate).toBeDisabled(); await expect(save).toBeDisabled()
  30  |     // Native disabled-button clicks cannot submit the retained invalid draft.
  31  |     await validate.evaluate(button => (button as HTMLButtonElement).click())
  32  |     await save.evaluate(button => (button as HTMLButtonElement).click())
  33  |     await column.selectOption(previous)
  34  |     await expect(column).toHaveValue(previous)
  35  |     await expect(area.getByRole('alert')).toHaveCount(0)
  36  |     await expect(validate).toBeEnabled(); await expect(save).toBeEnabled()
  37  |   }
  38  | }
  39  | 
  40  | // Capture a bounded editor inside its actual scroll owner. A screenshot of a
  41  | // whole comparison taller than the viewport contains clipped black areas and
  42  | // sticky-toolbar overlap; center hit-testing proves the actual controls remain
  43  | // exposed before any visual evidence is saved.
  44  | async function captureIdentityFilters(filters: Locator, path: string) {
  45  |   await filters.evaluate(element => {
  46  |     const container = element.closest('.main-content')
  47  |     if (!(container instanceof HTMLElement)) throw new Error('Identity editor has no owned scroll container')
  48  |     const toolbar = container.querySelector(':scope > .main-topbar')
  49  |     const top = Math.max(container.getBoundingClientRect().top, toolbar?.getBoundingClientRect().bottom ?? 0) + 16
  50  |     container.scrollTop += element.getBoundingClientRect().top - top
  51  |   })
  52  |   try {
  53  |     await expect.poll(() => filters.evaluate(element => {
  54  |       const box = element.getBoundingClientRect()
  55  |       const container = element.closest('.main-content')
  56  |       if (!(container instanceof HTMLElement)) return false
  57  |       const toolbar = container.querySelector(':scope > .main-topbar')
  58  |       const top = Math.max(container.getBoundingClientRect().top, toolbar?.getBoundingClientRect().bottom ?? 0)
  59  |       const bottom = Math.min(window.innerHeight, container.getBoundingClientRect().bottom)
  60  |       return box.top >= top && box.bottom <= bottom && box.left >= 0 && box.right <= window.innerWidth && element.scrollWidth <= element.clientWidth + 1 && Array.from(element.querySelectorAll('input, button')).every(control => {
  61  |         const bounds = control.getBoundingClientRect()
  62  |         const x = (bounds.left + bounds.right) / 2; const y = (bounds.top + bounds.bottom) / 2
  63  |         const hit = document.elementFromPoint(x, y)
  64  |         return bounds.width > 0 && bounds.height > 0 && bounds.left >= box.left && bounds.right <= box.right && hit !== null && (control === hit || control.contains(hit))
  65  |       })
> 66  |     })).toBe(true)
      |         ^ Error: expect(received).toBe(expected) // Object.is equality
  67  |   } catch (error) {
  68  |     const geometry = await filters.evaluate(element => {
  69  |       const bounds = (node: Element) => {
  70  |         const box = node.getBoundingClientRect(); const style = getComputedStyle(node)
  71  |         return { tag: node.tagName, class: node.className, left: box.left, right: box.right, top: box.top, bottom: box.bottom, width: box.width, scrollWidth: node.scrollWidth, clientWidth: node.clientWidth, scrollLeft: node.scrollLeft, minWidth: style.minWidth, gridTemplateColumns: style.gridTemplateColumns, display: style.display }
  72  |       }
  73  |       const ancestors = []; let parent: Element | null = element
  74  |       while (parent) { ancestors.push(bounds(parent)); parent = parent.parentElement }
  75  |       const card = element.closest('.monitor-card')
  76  |       const cardBounds = card?.getBoundingClientRect()
  77  |       const overflow = card && cardBounds ? Array.from(card.querySelectorAll('*')).filter(node => {
  78  |         const box = node.getBoundingClientRect()
  79  |         return box.width > cardBounds.width + 1 || box.right > cardBounds.right + 1 || node.scrollWidth > node.clientWidth + 1
  80  |       }).map(node => ({ ...bounds(node), text: node.textContent?.slice(0, 180), position: getComputedStyle(node).position, whiteSpace: getComputedStyle(node).whiteSpace, overflowX: getComputedStyle(node).overflowX })) : []
  81  |       const descriptions = card ? Array.from(card.querySelectorAll('.sr-only')).map(node => ({ ...bounds(node), text: node.textContent?.slice(0, 180), position: getComputedStyle(node).position, clip: getComputedStyle(node).clip, overflowX: getComputedStyle(node).overflowX })) : []
  82  |       return { viewport: { width: innerWidth, height: innerHeight }, ancestors, controls: Array.from(element.querySelectorAll('input, button')).map(bounds), overflow, descriptions }
  83  |     })
  84  |     await test.info().attach('identity-filter-geometry', { body: JSON.stringify(geometry, null, 2), contentType: 'application/json' })
  85  |     throw error
  86  |   }
  87  |   const add = filters.getByRole('button', { name: 'Add dimension filter', exact: true })
  88  |   const buttonBounds = await add.boundingBox()
  89  |   const inputBounds = await filters.getByLabel('Filter 1 value', { exact: true }).boundingBox()
  90  |   expect(buttonBounds).not.toBeNull(); expect(inputBounds).not.toBeNull()
  91  |   expect(buttonBounds!.height).toBeLessThanOrEqual(inputBounds!.height + 8)
  92  |   await filters.screenshot({ path })
  93  | }
  94  | 
  95  | 
  96  | test('Parquet upload, mapping, query and deletion remain in project scope', async ({ page }) => {
  97  |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  98  |   const workspace = await createWorkspace(page.context().request)
  99  |   const id = '00000000-0000-0000-0000-000000000001'
  100 |   let state = 'uploading'; let revision = 1; let uploaded = false; let deleted = false
  101 |   let mappingRequests = 0; let savedMappingRequests = 0; let commitRequests = 0
  102 |   let mapping: unknown
  103 |   const record = () => ({ id, project_id: workspace.projectId, name: 'External latency', revision, state, files: [{ id: 'source', relative_path: 'sample.parquet', size: 16, uploaded: uploaded ? 16 : 0 }], accepted: state === 'ready' ? 3 : 0, rejected: 0, mapping })
  104 |   const point = (value: number, second: number) => ({ timestamp_utc: `2026-01-01T00:00:0${second}Z`, metric_name: 'latency', unit: 'ms', value, quality: 'valid' })
  105 |   await page.route('**/api/v1/analysis/**', async route => {
  106 |     const req = route.request(); const url = new URL(req.url()); const method = req.method()
  107 |     const send = (value: unknown, status = 200) => route.fulfill({ status, json: value })
  108 |     if (url.pathname.endsWith('/capabilities')) return send({ native_parquet: true, enabled: true, imported_identity_version: 2, chunk_bytes: 4 * 1024 * 1024 })
  109 |     if (url.pathname.endsWith('/mappings') || url.pathname.endsWith('/comparisons')) { if (url.pathname.endsWith('/mappings') && method === 'POST') savedMappingRequests++; return send([]) }
  110 |     if (url.pathname.endsWith('/imports')) {
  111 |       expect(url.searchParams.get('project_id')).toBe(workspace.projectId)
  112 |       if (method === 'POST') { expect(req.postDataJSON().files[0].relative_path).toBe('sample.parquet'); return send(record(), 201) }
  113 |       return send(deleted ? [] : [record()])
  114 |     }
  115 |     if (url.pathname.endsWith('/files/source') && method === 'PUT') { expect(req.postDataBuffer()?.length).toBe(16); uploaded = true; revision++; return send(record()) }
  116 |     if (url.pathname.endsWith('/preview')) return send({ schema: { rows: 3, columns: [{ path: 'ts', path_components: ['ts'], physical_type: 'INT64' }, { path: 'latency', path_components: ['latency'], physical_type: 'DOUBLE' }, { path: 'labels.source', path_components: ['labels', 'source'], physical_type: 'BYTE_ARRAY' }, { path: 'host', path_components: ['host'], physical_type: 'BYTE_ARRAY' }, { path: 'service', path_components: ['service'], physical_type: 'BYTE_ARRAY' }, { path: 'group', path_components: ['group'], physical_type: 'BYTE_ARRAY' }, { path: 'credentials.password', path_components: ['credentials', 'password'], physical_type: 'DOUBLE' }, { path: 'api.key', path_components: ['api', 'key'], physical_type: 'BYTE_ARRAY' }] }, samples: [{ ts: 1767225600000, latency: 20 }] })
  117 |     if (url.pathname.endsWith('/mapping')) { mappingRequests++; mapping = req.postDataJSON().mapping; expect(mapping).toMatchObject({ version: 2, identity: { source: ['labels', 'source'], host: ['host'], service: ['service'], dimensions: [{ name: 'a/b~c', value: ['group'] }] } }); expect(req.postDataJSON().revision).toBe(revision); state = 'mapped'; revision++; return send(record()) }
  118 |     if (url.pathname.endsWith('/commit')) { commitRequests++; state = 'ready'; revision++; return send(record(), 202) }
  119 |     if (url.pathname.endsWith('/series')) { expect(req.postDataJSON()).toMatchObject({ source_id: 'agent', host: 'host-a', service: 'api', dimensions: { 'a/b~c': '' } }); expect(req.postDataJSON().from).toBe('2026-01-01T00:00:00.000Z'); return send({ points: [point(20, 0), point(40, 1)] }) }
  120 |     if (method === 'DELETE') { deleted = true; return route.fulfill({ status: 204 }) }
  121 |     return send(record())
  122 |   })
  123 |   await page.goto(`/#/monitoring?category=analysis&${scopeQuery(workspace)}`); await page.reload()
  124 |   const area = page.getByRole('region', { name: 'Analysis workspace' })
  125 |   await expect(area).toBeVisible()
  126 |   await area.getByRole('button', { name: 'New import', exact: true }).click()
  127 |   await area.getByLabel('Dataset name', { exact: true }).fill('External latency')
  128 |   await area.getByLabel('Files', { exact: true }).setInputFiles({ name: 'sample.parquet', mimeType: 'application/octet-stream', buffer: Buffer.alloc(16) })
  129 |   await area.getByRole('button', { name: 'Upload files', exact: true }).click()
  130 |   await expect(area).toContainText('Upload complete')
  131 |   await area.getByLabel('Timestamp format').selectOption('unix_ms')
  132 |   await area.getByRole('checkbox', { name: 'latency', exact: true }).check()
  133 |   await area.getByLabel('Semantics').selectOption('observation')
  134 |   await area.getByRole('combobox', { name: 'Unit', exact: true }).selectOption('ms')
  135 |   await area.getByLabel('Mapping name', { exact: true }).fill('Private-selection admission probe')
  136 |   for (const column of [area.getByRole('combobox', { name: 'Timestamp column', exact: true }), area.getByRole('combobox', { name: 'Metric name column (long table)', exact: true })]) {
  137 |     await assertProtectedColumnsUnavailable(column, area)
  138 |   }
  139 |   await expect(area.getByRole('checkbox', { name: 'credentials.password (protected)', exact: true })).toBeDisabled()
  140 |   await expect(area.getByRole('checkbox', { name: 'api.key (protected)', exact: true })).toBeDisabled()
  141 |   const identityMapping = area.getByRole('group', { name: 'Imported measurement identities', exact: true })
  142 |   for (const label of ['Source column', 'Host column', 'Service column']) {
  143 |     const column = identityMapping.getByRole('combobox', { name: label, exact: true })
  144 |     await assertProtectedColumnsUnavailable(column, area)
  145 |   }
  146 |   expect(mappingRequests).toBe(0); expect(savedMappingRequests).toBe(0); expect(commitRequests).toBe(0)
  147 |   await area.getByLabel('Mapping name', { exact: true }).fill('')
  148 |   await identityMapping.getByRole('combobox', { name: 'Source column', exact: true }).selectOption('["labels","source"]')
  149 |   await identityMapping.getByRole('combobox', { name: 'Host column', exact: true }).selectOption('["host"]')
  150 |   await identityMapping.getByRole('combobox', { name: 'Service column', exact: true }).selectOption('["service"]')
  151 |   await identityMapping.getByRole('button', { name: 'Add dimension', exact: true }).click()
  152 |   await expect(area.getByRole('button', { name: 'Validate and import', exact: true })).toBeDisabled()
  153 |   await identityMapping.getByLabel('Dimension 1 name', { exact: true }).fill('a/b~c')
  154 |   await identityMapping.getByRole('combobox', { name: 'Dimension 1 column', exact: true }).selectOption('["group"]')
  155 |   await area.getByRole('button', { name: 'Validate and import', exact: true }).click()
  156 |   await expect(area).toContainText('State: ready')
  157 |   expect(mappingRequests).toBe(1); expect(savedMappingRequests).toBe(0); expect(commitRequests).toBe(1)
  158 |   const query = area.getByRole('group', { name: 'Query dataset', exact: true })
  159 |   await query.getByLabel('Metric', { exact: true }).fill('latency')
  160 |   await query.getByLabel('From (UTC)', { exact: true }).fill('2026-01-01T00:00')
  161 |   await query.getByLabel('To (UTC)', { exact: true }).fill('2026-01-01T00:01')
  162 |   const identityFilters = query.getByRole('group', { name: 'Dataset identity filters', exact: true })
  163 |   await identityFilters.getByLabel('Source ID', { exact: true }).fill('agent')
  164 |   await identityFilters.getByLabel('Host', { exact: true }).fill('host-a')
  165 |   await identityFilters.getByLabel('Service', { exact: true }).fill('api')
  166 |   await identityFilters.getByRole('button', { name: 'Add dimension filter', exact: true }).click()
```