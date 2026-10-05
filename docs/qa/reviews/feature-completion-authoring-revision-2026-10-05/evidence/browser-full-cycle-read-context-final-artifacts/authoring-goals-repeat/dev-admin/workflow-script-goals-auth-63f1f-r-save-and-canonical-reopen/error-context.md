# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: workflow-script-goals.spec.ts >> authored script goals survive an unrelated editor save and canonical reopen
- Location: e2e/workflow-script-goals.spec.ts:4:1

# Error details

```
Error: expect(locator).toContainText(expected) failed

Locator: locator('.script-code-pane').locator('.cm-content')
Expected substring: "goal_config"
Received string:    "{  \"script\": {    \"actions\": [      {        \"method\": \"GET\",        \"name\": \"Purchase\",        \"target\": \"/\",        \"think_time\": {          \"mean_ms\": 0,          \"type\": \"\"        },        \"type\": \"request\"      }    ],    \"config\": {      \"base_url\": \"http://127.0.0.1:2\",      \"browser\": {        \"geolocation\": {},        \"http_credentials\": {},        \"proxy\": {},        \"review_policy\": {},        \"viewport\": {}      },      \"bruno\": {        \"limits\": {}      },      \"database\": {},      \"error_handling\": {},      \"eventhubs\": {},      \"grpc\": {},      \"http\": {        \"auth\": {},        \"proxy\": {},"
Timeout: 15000ms

Call log:
  - Expect "toContainText" with timeout 15000ms
  - waiting for locator('.script-code-pane').locator('.cm-content')
    34 × locator resolved to <div translate="no" role="textbox" autocorrect="off" spellcheck="false" autocapitalize="off" data-language="json" aria-multiline="true" contenteditable="false" aria-autocomplete="list" writingsuggestions="false" class="cm-content cm-lineWrapping">…</div>
       - unexpected value "{  "script": {    "actions": [      {        "method": "GET",        "name": "Purchase",        "target": "/",        "think_time": {          "mean_ms": 0,          "type": ""        },        "type": "request"      }    ],    "config": {      "base_url": "http://127.0.0.1:2",      "browser": {        "geolocation": {},        "http_credentials": {},        "proxy": {},        "review_policy": {},        "viewport": {}      },      "bruno": {        "limits": {}      },      "database": {},      "error_handling": {},      "eventhubs": {},      "grpc": {},      "http": {        "auth": {},        "proxy": {},"

```

```yaml
- textbox
```

# Test source

```ts
  1  | import { test, expect, idOf, json, login, mutation } from './support/workflow'
  2  | import { createWorkspace, scopeQuery } from './support/workspace'
  3  | 
  4  | test('authored script goals survive an unrelated editor save and canonical reopen', async ({ page }, testInfo) => {
  5  |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  6  |   const request = page.context().request
  7  |   const workspace = await createWorkspace(request)
  8  |   const goals = {
  9  |     target_average_tps: 8.75, p95_latency_ms: 217.5, max_error_pct: 0,
  10 |     min_vusers: 1, max_vusers: 6, max_error_count: 17,
  11 |     stop_on_sla_breach: true, stop_on_max_errors: true, sla_breach_window: '11s',
  12 |     transaction_rules: [{ population: 'transaction', protocol: 'http', group: 'checkout', transaction: 'Purchase', metric: 'p95_duration_ms', comparator: 'lte', threshold: 215.25, severity: 'fail' }],
  13 |   }
  14 |   const id = idOf(await json(await request.post('/api/v1/scripts', { data: {
  15 |     project_id: workspace.projectId,
  16 |     script: { name: 'Durable script goals', version: '1.0', protocol: 'http', goal_config: goals,
  17 |       config: { base_url: 'http://127.0.0.1:1' }, actions: [{ name: 'Purchase', type: 'request', method: 'GET', target: '/' }] },
  18 |   } })))
  19 |   await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  20 |   if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  21 |   const workbench = page.getByRole('region', { name: 'Author center workbench' })
  22 |   await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
  23 |   const baseURL = workbench.getByRole('textbox', { name: 'Base URL' })
  24 |   await expect(baseURL).toHaveValue('http://127.0.0.1:1')
  25 |   await baseURL.fill('http://127.0.0.1:2')
  26 |   const saved = mutation(page, 'PUT', `/api/v1/scripts/${id}`)
  27 |   await page.getByRole('button', { name: 'Save script', exact: true }).click()
  28 |   await json(await saved)
  29 |   await expect(page.getByRole('button', { name: 'Save script', exact: true })).toBeDisabled()
  30 |   const canonical = await json(await request.get(`/api/v1/scripts/${id}`))
  31 |   expect(canonical.script).toMatchObject({ goal_config: goals, config: { base_url: 'http://127.0.0.1:2' } })
  32 |   await page.reload()
  33 |   await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
  34 |   await expect(baseURL).toHaveValue('http://127.0.0.1:2')
  35 |   await workbench.getByRole('tab', { name: 'Source' }).click()
  36 |   const code = page.locator('.script-code-pane')
  37 |   // With no action selected, Source already owns the complete canonical JSON.
  38 |   // Its virtualized code view must navigate to the authored goals to inspect
  39 |   // them; no conditional action-scope toggle is present on this entry path.
  40 |   await code.getByRole('button', { name: 'Find', exact: true }).click()
  41 |   const find = code.getByPlaceholder('Find', { exact: true })
  42 |   await find.fill('goal_config')
  43 |   await find.press('Enter')
> 44 |   await expect(code.locator('.cm-content')).toContainText('goal_config')
     |                                             ^ Error: expect(locator).toContainText(expected) failed
  45 |   await find.fill('217.5')
  46 |   await find.press('Enter')
  47 |   await expect(code.locator('.cm-content')).toContainText('217.5')
  48 |   await find.press('Escape')
  49 |   await page.screenshot({ path: testInfo.outputPath('canonical-goals-reopen.png'), fullPage: true })
  50 | })
  51 | 
```