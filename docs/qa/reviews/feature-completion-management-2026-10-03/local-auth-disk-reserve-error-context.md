# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: workflow-local-auth.spec.ts >> [U01] sign in, build workspace, import, edit, launch, stop and retrieve the same run
- Location: e2e/workflow-local-auth.spec.ts:7:1

# Error details

```
TimeoutError: page.waitForEvent: Timeout 15000ms exceeded while waiting for event "download"
=========================== logs ===========================
waiting for event "download"
============================================================
```

# Page snapshot

```yaml
- generic [ref=e1]:
  - generic [ref=e3]:
    - complementary [ref=e4]:
      - button "APEX Load dashboard" [ref=e5] [cursor=pointer]:
        - img "APEX Load" [ref=e6]
        - generic [ref=e11]: APEX Load
      - button "Workspace context. arc-1791064638106 domain / arc-1791064638106 owner / arc-1791064638106 application / arc-1791064638106 1.0" [ref=e13] [cursor=pointer]:
        - generic [ref=e14]: ◈
        - generic [ref=e15]:
          - generic [ref=e16]: Workspace
          - generic [ref=e17]: arc-1791064638106 domain / arc-1791064638106 owner / arc-1791064638106 application / arc-1791064638106 1.0
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
        - generic [ref=e56]:
          - button "Scripts" [ref=e58] [cursor=pointer]:
            - img [ref=e60]
            - generic [ref=e63]: Scripts
          - button "Scenarios" [ref=e65] [cursor=pointer]:
            - img [ref=e67]
            - generic [ref=e69]: Scenarios
        - generic [ref=e70]:
          - button "Repository" [ref=e72] [cursor=pointer]:
            - img [ref=e74]
            - generic [ref=e77]: Repository
          - button "Access" [ref=e79] [cursor=pointer]:
            - img [ref=e81]
            - generic [ref=e85]: Access
          - button "Settings" [ref=e87] [cursor=pointer]:
            - img [ref=e89]
            - generic [ref=e92]: Settings
        - button "Help & documentation" [ref=e95] [cursor=pointer]:
          - img [ref=e97]
          - generic [ref=e100]: Help & documentation
      - generic [ref=e101]:
        - generic [ref=e102]:
          - generic "Local System Administrator • admin@apexload.local • System Admin" [ref=e103]:
            - generic [ref=e104]: LS
            - generic [ref=e105]:
              - generic [ref=e106]: Local System Administrator
              - generic [ref=e107]: System Admin
              - button "Sign out" [ref=e108] [cursor=pointer]
          - generic [ref=e109]:
            - generic "Live" [ref=e110]:
              - generic [ref=e112]: Live
            - 'button "Build info: local · dev" [ref=e114] [cursor=pointer]':
              - generic [ref=e116]: Version Info
        - button "Collapse sidebar" [expanded] [ref=e118] [cursor=pointer]:
          - img [ref=e119]
    - main "Run Results" [ref=e121]:
      - status [ref=e122]: Run Results
      - generic "Dashboard controls" [ref=e123]:
        - heading "Run Results" [level=1] [ref=e125]
        - generic [ref=e126]:
          - link "Help for this page" [ref=e127] [cursor=pointer]:
            - /url: "#/docs?guide=user-guide&article=results"
          - generic "Deployment identity" [ref=e128]:
            - generic [ref=e129]: Browser · Server
          - combobox "Dashboard color theme" [ref=e132]:
            - option "System (Auto)"
            - option "Dark Theme" [selected]
            - option "Light Theme"
            - option "Solarized Dark"
            - option "Solarized Light"
            - option "Monokai Dimmed"
      - generic [ref=e133]:
        - generic [ref=e134]:
          - generic [ref=e135]:
            - paragraph [ref=e136]: Run results · LOC-RUN-20261003-030
            - heading "arc-1791064638106 run" [level=1] [ref=e137]
            - paragraph [ref=e138]:
              - generic [ref=e139]: Stopped by operator
              - text: · Configured 1 VUsers for 1m1s · Lifecycle elapsed 3s · Archived evidence elapsed 0m 3s
          - generic [ref=e140]:
            - button "Compare" [ref=e141] [cursor=pointer]
            - button "Export report" [expanded] [ref=e142] [cursor=pointer]
            - button "Test notes" [ref=e143] [cursor=pointer]
        - region "Report export options" [ref=e144]:
          - heading "Export report" [level=2] [ref=e145]
          - paragraph [ref=e146]: Choose an audience. Outputs cover the whole run, independent of the population selector below. Any release trend in an output uses the prior release run and names that reference.
          - tablist "Report audience lens" [ref=e147]:
            - tab "Executive" [selected] [ref=e148] [cursor=pointer]
            - tab "Engineer" [ref=e149] [cursor=pointer]
            - tab "Audit" [ref=e150] [cursor=pointer]
          - button "Preview / edit commentary" [ref=e151] [cursor=pointer]
          - generic [ref=e152]:
            - generic [ref=e154]:
              - heading "Export" [level=2] [ref=e155]
              - paragraph [ref=e156]: Choose a file for how your audience will review this executive report.
            - generic [ref=e157]:
              - button "Navigable HTML Browse sections and charts offline." [ref=e158] [cursor=pointer]:
                - strong [ref=e159]: Navigable HTML
                - generic [ref=e160]: Browse sections and charts offline.
              - button "Print-ready PDF Share a paged review copy." [ref=e161] [cursor=pointer]:
                - strong [ref=e162]: Print-ready PDF
                - generic [ref=e163]: Share a paged review copy.
              - button "Editable Word Add review notes and sign-off." [ref=e164] [cursor=pointer]:
                - strong [ref=e165]: Editable Word
                - generic [ref=e166]: Add review notes and sign-off.
              - button "Data CSV Use section and metric values in other tools." [ref=e167] [cursor=pointer]:
                - strong [ref=e168]: Data CSV
                - generic [ref=e169]: Use section and metric values in other tools.
              - button "Analysis tables (ZIP) Download row-based CSV tables and a data guide." [ref=e170] [cursor=pointer]:
                - strong [ref=e171]: Analysis tables (ZIP)
                - generic [ref=e172]: Download row-based CSV tables and a data guide.
          - group [ref=e173]:
            - generic "Publish to Power BI" [ref=e174] [cursor=pointer]
          - group [ref=e175]:
            - generic "Publish to Confluence" [ref=e176] [cursor=pointer]
        - region "Analysis context" [ref=e177]:
          - generic [ref=e178]:
            - text: Metric population
            - combobox "Metric population" [ref=e179]:
              - option "Requests / actions" [selected]
              - option "Named transactions"
              - option "Unclassified legacy records"
          - paragraph [ref=e180]: Requests / actions · 5671 samples · Whole captured run. Interval graphs show named transaction metrics. Resource curves show the load generators.
        - group [ref=e181]:
          - generic "Run timeline annotations" [ref=e182] [cursor=pointer]
        - tablist "Result sections" [ref=e183]:
          - tab "Summary" [ref=e184] [cursor=pointer]
          - tab "Performance" [ref=e185] [cursor=pointer]
          - tab "Baseline Comparison" [ref=e186] [cursor=pointer]
          - tab "Diagnostics" [ref=e187] [cursor=pointer]
          - tab "Evidence" [selected] [ref=e188] [cursor=pointer]
          - tab "Execution" [ref=e189] [cursor=pointer]
        - tabpanel "Evidence" [ref=e190]:
          - region "Report unavailable" [ref=e191]:
            - heading "Report could not be loaded" [level=2] [ref=e192]
            - alert [ref=e193]: Controller reported a server error
            - paragraph [ref=e194]: Retry to load the archived findings. Execution details remain available.
            - link "Back to results" [ref=e195] [cursor=pointer]:
              - /url: "#/results?projectId=dd530a43-c7d4-4213-8faf-d3c236d656bf&applicationId=157501dc-c380-47af-bebd-5fc6220e7203&releaseId=b134dec0-01a5-455f-8702-f26b6369b30b"
            - button "Retry report" [ref=e196] [cursor=pointer]
            - button "View execution details" [ref=e197] [cursor=pointer]
          - generic [ref=e198]:
            - paragraph [ref=e199]: Controller reported a server error
            - generic [ref=e201]:
              - generic [ref=e202]:
                - heading "Completeness Score" [level=3] [ref=e203]
                - paragraph [ref=e204]: Archive coverage score for intervals, telemetry, writer health, and artifact availability.
              - generic [ref=e205]: 100% · complete
          - generic [ref=e206]:
            - heading "Metric populations" [level=2] [ref=e207]
            - generic "Metric populations" [ref=e208]:
              - generic [ref=e209]:
                - term [ref=e210]: Requests / actions
                - definition [ref=e211]: 5671 · 0 errors · p95 0.4 ms · 2149.088/s elapsed-run average
              - generic [ref=e212]:
                - term [ref=e213]: Named transactions
                - definition [ref=e214]: 5671 · 0 errors · p95 0.4 ms · 2149.088/s elapsed-run average
              - generic [ref=e215]:
                - term [ref=e216]: Unclassified legacy records
                - definition [ref=e217]: 0 records · no samples to evaluate
              - generic [ref=e218]:
                - term [ref=e219]: Archive records
                - definition [ref=e220]: "11342"
          - generic [ref=e221]:
            - paragraph [ref=e222]: Controller reported a server error
            - region "Archive metadata summary" [ref=e223]:
              - generic [ref=e224]:
                - generic [ref=e225]:
                  - generic [ref=e226]:
                    - heading "Telemetry Health" [level=3] [ref=e227]
                    - generic [ref=e228]: healthy
                  - generic [ref=e229]:
                    - generic [ref=e230]:
                      - term [ref=e231]: Samples
                      - definition [ref=e232]: "1"
                    - generic [ref=e233]:
                      - term [ref=e234]: Intervals
                      - definition [ref=e235]: 1 healthy · 0 degraded · 0 breached
                    - generic [ref=e236]:
                      - term [ref=e237]: Max Sample Age
                      - definition [ref=e238]: 53ms
                    - generic [ref=e239]:
                      - term [ref=e240]: Generator Lag
                      - definition [ref=e241]: n/a
                    - generic [ref=e242]:
                      - term [ref=e243]: Transport Lag
                      - definition [ref=e244]: n/a
                    - generic [ref=e245]:
                      - term [ref=e246]: Breach Window
                      - definition [ref=e247]: n/a
                - generic [ref=e248]:
                  - generic [ref=e249]:
                    - heading "Schema Compatibility" [level=3] [ref=e250]
                    - generic [ref=e251]: compatible
                  - generic [ref=e252]:
                    - generic [ref=e253]:
                      - term [ref=e254]: Report Schema
                      - definition [ref=e255]: apexload.archive_report.v1
                    - generic [ref=e256]:
                      - term [ref=e257]: Manifest
                      - definition [ref=e258]: apexload.archive.v4
                    - generic [ref=e259]:
                      - term [ref=e260]: Outputs
                      - definition [ref=e261]: "json: compatible, standalone html: compatible, transaction jsonl: compatible, resource jsonl: compatible, +7 more"
                    - generic [ref=e262]:
                      - term [ref=e263]: Issues
                      - definition [ref=e264]: None
                - generic [ref=e265]:
                  - generic [ref=e266]:
                    - heading "Evidence Tiers" [level=3] [ref=e267]
                    - generic [ref=e268]: 6 missing
                  - generic [ref=e269]:
                    - generic [ref=e270]:
                      - term [ref=e271]: Tiers
                      - definition [ref=e272]: "Summary: present · 11344, Intervals: present · 1, Sampled Diagnostics: missing · 0, Transaction Archive: present · 11342, Browser Artifacts: missing · 0, +5 more"
                    - generic [ref=e273]:
                      - term [ref=e274]: Present
                      - definition [ref=e275]: "4"
                    - generic [ref=e276]:
                      - term [ref=e277]: Missing
                      - definition [ref=e278]: "6"
                - generic [ref=e279]:
                  - generic [ref=e280]:
                    - heading "Run Provenance" [level=3] [ref=e281]
                    - generic [ref=e282]: captured
                  - generic [ref=e283]:
                    - generic [ref=e284]:
                      - term [ref=e285]: Execution Profile
                      - definition [ref=e286]: vuser_driven · 1 VUsers · 1m1s
                    - generic [ref=e287]:
                      - term [ref=e288]: Scope
                      - definition [ref=e289]: dd530a43-c7d4-4213-8faf-d3c236d656bf / b134dec0-01a5-455f-8702-f26b6369b30b / e2878e3f-8524-4d47-ada7-0a0609911d97
                    - generic [ref=e290]:
                      - term [ref=e291]: Runtime Flags
                      - definition [ref=e292]: Shakeout Disabled · Baseline Disabled
                    - generic [ref=e293]:
                      - term [ref=e294]: Browser Runtime
                      - definition [ref=e295]: n/a
                    - generic [ref=e296]:
                      - term [ref=e297]: Metrics Interval
                      - definition [ref=e298]: 1s
                    - generic [ref=e299]:
                      - term [ref=e300]: Pinned Baseline
                      - definition [ref=e301]: Not pinned
                    - generic [ref=e302]:
                      - term [ref=e303]: Scenario Groups
                      - definition [ref=e304]: Orders_1791064639559 · 1 VUsers
                    - generic [ref=e305]:
                      - term [ref=e306]: Controller Environment
                      - definition [ref=e307]: n/a
                    - generic [ref=e308]:
                      - term [ref=e309]: Generator Inventory
                      - definition [ref=e310]: n/a
                - generic [ref=e311]:
                  - generic [ref=e312]:
                    - heading "Change Attribution" [level=3] [ref=e313]
                    - generic [ref=e314]: captured
                  - generic [ref=e315]:
                    - generic [ref=e316]:
                      - term [ref=e317]: Scripts
                      - definition [ref=e318]: 5066221f-3d9b-4b1b-93de-df88e1fb65bd
                    - generic [ref=e319]:
                      - term [ref=e320]: Scenario
                      - definition [ref=e321]: e2878e3f-8524-4d47-ada7-0a0609911d97
                    - generic [ref=e322]:
                      - term [ref=e323]: Environment
                      - definition [ref=e324]: n/a
                    - generic [ref=e325]:
                      - term [ref=e326]: Generators
                      - definition [ref=e327]: n/a
                    - generic [ref=e328]:
                      - term [ref=e329]: Data
                      - definition [ref=e330]: 5066221f-3d9b-4b1b-93de-df88e1fb65bd:data
                    - generic [ref=e331]:
                      - term [ref=e332]: Runtime Overrides
                      - definition [ref=e333]: "No"
              - region "Archived monitoring capture" [ref=e334]:
                - heading "Monitoring capture evidence" [level=3] [ref=e335]
                - paragraph [ref=e336]: Samples are bounded, deduplicated, and redacted—not a complete log export. Empty evidence does not prove that no errors occurred.
                - paragraph [ref=e337]:
                  - strong [ref=e338]: "Elastic:"
                  - text: Capture not recorded for this run (disabled or older archive).
                - paragraph [ref=e339]:
                  - strong [ref=e340]: "Dynatrace:"
                  - text: Capture not recorded for this run (disabled or older archive).
          - group [ref=e341]:
            - generic "Raw evidence downloads" [ref=e342] [cursor=pointer]
            - button "Download evidence bundle" [ref=e343] [cursor=pointer]
            - generic [ref=e344]:
              - paragraph [ref=e345]: Controller reported a server error
              - generic [ref=e346]:
                - generic [ref=e347]:
                  - link "Download report data JSON" [ref=e348] [cursor=pointer]:
                    - /url: /api/v1/tests/903a11cc-d2d6-4f46-ab25-96b4c38fc970/archive/report?download=true
                  - generic [ref=e349]: JSONL
                  - button "Download Transactions JSONL" [active] [ref=e350] [cursor=pointer]
                  - button "Download Resources JSONL" [ref=e351] [cursor=pointer]
                  - button "Download Summaries JSONL" [ref=e352] [cursor=pointer]
                - generic [ref=e353]:
                  - generic [ref=e354]: Parquet
                  - button "Download Transactions Parquet" [ref=e355] [cursor=pointer]
                  - button "Download Resources Parquet" [ref=e356] [cursor=pointer]
                  - button "Download Summaries Parquet" [ref=e357] [cursor=pointer]
          - group [ref=e358]:
            - generic "Archived intervals" [ref=e359] [cursor=pointer]
  - generic [ref=e360]: "0"
```

# Test source

```ts
  1   | import { readFile, stat } from 'node:fs/promises'
  2   | import { createHash } from 'node:crypto'
  3   | import { test, expect, login, createWorkspaceUI, importOpenAPI, buildScenarioUI, mutation, json, idOf, runStatus } from './support/workflow'
  4   |
  5   | test.beforeEach(() => { test.skip(process.env.APEXLOAD_E2E_PROFILE !== 'local-auth', 'local-auth profile') })
  6   |
  7   | test('[U01] sign in, build workspace, import, edit, launch, stop and retrieve the same run', async ({ page, target, trackRun }) => {
  8   |   test.setTimeout(240_000)
  9   |   await page.addInitScript(() => localStorage.setItem('apexload.featureFlags.runReport', 'true'))
  10  |   await login(page)
  11  |   const request = page.context().request
  12  |   const label = `arc-${Date.now()}`
  13  |   const workspace = await createWorkspaceUI(page, request, label)
  14  |   const scriptName = `Orders_${Date.now()}`
  15  |   const scriptId = await importOpenAPI(page, scriptName, target.url)
  16  |   const workbench = page.getByRole('region', { name: 'Author center workbench' })
  17  |   await expect(workbench.getByRole('tab', { name: 'Action editor' })).toHaveAttribute('aria-selected', 'true')
  18  |   await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
  19  |   await workbench.getByRole('textbox', { name: 'Base URL' }).fill(target.url + '/')
  20  |   const updated = mutation(page, 'PUT', `/api/v1/scripts/${scriptId}`)
  21  |   await page.getByRole('button', { name: 'Save script', exact: true }).click()
  22  |   await json(await updated)
  23  |   await page.getByRole('button', { name: 'Close script editor' }).click()
  24  |   const scenarioId = await buildScenarioUI(page, scriptName, `${label} scenario`)
  25  |   await expect(page).toHaveURL(new RegExp(`scenarioId=${scenarioId}`))
  26  |   const runName = `${label} run`
  27  |   await page.getByLabel('Run Name', { exact: true }).fill(runName)
  28  |   await page.getByRole('combobox', { name: 'Release', exact: true }).selectOption(workspace.releaseId)
  29  |   await expect(page.getByRole('button', { name: 'Start Now' })).toBeEnabled({ timeout: 30_000 })
  30  |   const created = mutation(page, 'POST', '/api/v1/tests')
  31  |   await page.getByRole('button', { name: 'Start Now' }).click()
  32  |   const run = await json(await created)
  33  |   const id = idOf(run); trackRun(id)
  34  |   expect(run.project_id).toBe(workspace.projectId)
  35  |   expect(run.name).toBe(runName)
  36  |   await expect(page).toHaveURL(new RegExp(`(?:test|command-center)/${id}`))
  37  |   await expect.poll(() => target.count(), { timeout: 60_000 }).toBeGreaterThan(0)
  38  |   const first = target.count()
  39  |   await expect.poll(() => target.count()).toBeGreaterThan(first)
  40  |   await expect.poll(() => runStatus(request, id)).toBe('RUNNING')
  41  |   await page.getByRole('button', { name: 'Stop', exact: true }).click()
  42  |   await page.getByRole('dialog', { name: 'Stop this test run?' }).getByRole('button', { name: 'Stop Test' }).click()
  43  |   await expect.poll(() => runStatus(request, id), { timeout: 90_000 }).toBe('COMPLETE')
  44  |   await expect.poll(async () => (await request.get(`/api/v1/tests/${id}/archive/manifest`)).ok(), { timeout: 60_000 }).toBe(true)
  45  |   await page.getByRole('button', { name: 'Results', exact: true }).first().click()
  46  |   await page.getByRole('searchbox', { name: 'Search results', exact: true }).fill(runName)
  47  |   const row = page.locator('tbody tr').filter({ hasText: runName })
  48  |   await expect(row).toBeVisible()
  49  |   await row.getByRole('button', { name: 'View results' }).click()
  50  |   await expect(page).toHaveURL(new RegExp(`#/report/${id}`))
  51  |   await expect(page.getByText(runName, { exact: false }).first()).toBeVisible()
  52  |   await page.getByRole('button', { name: 'Export report', exact: true }).click()
  53  |   const exportOptions = page.getByRole('region', { name: 'Report export options' })
  54  |   await expect(exportOptions.getByRole('button', { name: /Print-ready PDF/ })).toBeVisible()
  55  |   const analysisDownload = page.waitForEvent('download')
  56  |   await exportOptions.getByRole('button', { name: /Analysis tables/ }).click()
  57  |   const analysisBundle = await analysisDownload
  58  |   expect(analysisBundle.suggestedFilename()).toMatch(/-executive\.zip$/)
  59  |   const analysisPath = await analysisBundle.path()
  60  |   expect(analysisPath).not.toBeNull()
  61  |   expect((await stat(analysisPath!)).size).toBeGreaterThan(0)
  62  |   // Follow the report's evidence link, preserving the same run identity.
  63  |   await page.getByRole('tab', { name: 'Evidence', exact: true }).click()
  64  |   await page.getByText('Raw evidence downloads', { exact: true }).click()
> 65  |   const download = page.waitForEvent('download')
      |                         ^ TimeoutError: page.waitForEvent: Timeout 15000ms exceeded while waiting for event "download"
  66  |   await page.getByRole('button', { name: 'Download Transactions JSONL' }).first().click()
  67  |   const downloaded = await download
  68  |   const path = await downloaded.path(); expect(path).not.toBeNull()
  69  |   expect((await stat(path!)).size).toBeGreaterThan(0)
  70  |   const manifest = await json(await request.get(`/api/v1/tests/${id}/archive/manifest`))
  71  |   const serverFile = await request.get(`/api/v1/tests/${id}/archive/files/${String(manifest.transactions_file).split('/').pop()}`)
  72  |   expect(serverFile.ok()).toBeTruthy()
  73  |   expect(createHash('sha256').update(await readFile(path!)).digest('hex')).toBe(createHash('sha256').update(await serverFile.body()).digest('hex'))
  74  | })
  75  |
  76  | test('[U02] viewer cannot launch and sign-out does not reopen a protected workspace', async ({ page }) => {
  77  |   await login(page, 'user')
  78  |   const auth = await json(await page.context().request.get('/api/v1/auth/me'))
  79  |   expect((auth.user as { subject: string }).subject).toBe('local:user')
  80  |   await page.goto('/#/new-test')
  81  |   await expect(page.getByRole('button', { name: 'Start Now' })).toBeDisabled()
  82  |   const denied = await page.context().request.post('/api/v1/tests', { data: { name: 'viewer must not create', duration: '1m', groups: [] } })
  83  |   expect(denied.status()).toBe(403)
  84  |   await page.getByRole('button', { name: 'Sign out' }).click()
  85  |   await expect(page.getByRole('heading', { name: 'Choose a test persona' })).toBeVisible()
  86  |   await page.goBack()
  87  |   await expect(page.getByRole('heading', { name: 'Choose a test persona' })).toBeVisible()
  88  |   await expect(page.locator('nav.sidebar-nav')).toBeHidden()
  89  | })
  90  |
  91  | test('[U03] viewer personal settings persist without exposing administrative settings', async ({ page }) => {
  92  |   await login(page, 'user')
  93  |   const adminSettingsRequests: string[] = []
  94  |   page.on('request', request => { if (/\/api\/v1\/system\/settings(?:\?|$)/.test(request.url())) adminSettingsRequests.push(request.url()) })
  95  |   await page.locator('button.nav-item[aria-label="Settings"]').click()
  96  |   await expect(page.getByRole('navigation', { name: 'Personal settings sections' })).toBeVisible()
  97  |   await page.getByLabel('Dashboard Theme', { exact: true }).selectOption('light')
  98  |   await page.getByLabel('Motion', { exact: true }).selectOption('reduce')
  99  |   await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  100 |   await expect(page.locator('html')).toHaveAttribute('data-motion', 'reduce')
  101 |   await page.getByRole('button', { name: 'Tables & editor', exact: true }).click()
  102 |   await page.getByLabel('Default rows per table', { exact: true }).selectOption('25')
  103 |   await page.getByLabel('Editor word wrap', { exact: true }).selectOption('off')
  104 |   await page.reload()
  105 |   await expect(page.getByLabel('Default rows per table', { exact: true })).toHaveValue('25')
  106 |   await expect(page.getByLabel('Editor word wrap', { exact: true })).toHaveValue('off')
  107 |   await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  108 |   await expect(page.locator('html')).toHaveAttribute('data-motion', 'reduce')
  109 |   await page.getByRole('button', { name: 'Account', exact: true }).click()
  110 |   await expect(page.getByRole('region', { name: 'Your account' })).toContainText('Local Viewer')
  111 |   await page.goto('/#/settings?setting=authentication')
  112 |   await expect(page.getByLabel('Dashboard Theme', { exact: true })).toBeVisible()
  113 |   await expect(page.getByRole('navigation', { name: 'Personal settings sections' })).toBeVisible()
  114 |   expect(adminSettingsRequests).toEqual([])
  115 |   const denied = await page.context().request.put('/api/v1/system/settings', { data: {} })
  116 |   expect(denied.status()).toBe(403)
  117 | })
  118 |
```
