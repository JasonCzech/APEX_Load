# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: command-center-details.spec.ts >> runtime workspace keeps essential metrics readable and settings in an opaque drawer
- Location: e2e/command-center-details.spec.ts:33:1

# Error details

```
Error: expect(received).toBeGreaterThanOrEqual(expected)

Expected: >= 363
Received:    269.78125
```

# Page snapshot

```yaml
- generic [ref=e1]:
  - generic [ref=e3]:
    - complementary [ref=e4]:
      - button "APEX Load dashboard" [ref=e5] [cursor=pointer]:
        - img "APEX Load" [ref=e6]
        - generic [ref=e11]: APEX Load
      - button "Workspace context. Default Domain / Unassigned / Legacy / Imported Legacy" [ref=e13] [cursor=pointer]:
        - generic [ref=e14]: ◈
        - generic [ref=e15]:
          - generic [ref=e16]: Workspace
          - generic [ref=e17]: Default Domain / Unassigned / Legacy / Imported Legacy
        - generic [ref=e18]: ⌄
      - navigation "Primary navigation" [ref=e19]:
        - button "New Test" [ref=e21] [cursor=pointer]:
          - img [ref=e23]
          - generic [ref=e26]: New Test
        - generic [ref=e27]:
          - paragraph [ref=e28]: Operate
          - button "Dashboard" [ref=e30] [cursor=pointer]:
            - img [ref=e32]
            - generic [ref=e37]: Dashboard
          - button "Command Center" [ref=e39] [cursor=pointer]:
            - img [ref=e41]
            - generic [ref=e45]: Command Center
          - button "Results" [ref=e47] [cursor=pointer]:
            - img [ref=e49]
            - generic [ref=e50]: Results
          - button "Monitoring" [ref=e52] [cursor=pointer]:
            - img [ref=e54]
            - generic [ref=e56]: Monitoring
          - button "Activity" [ref=e58] [cursor=pointer]:
            - img [ref=e60]
            - generic [ref=e62]: Activity
        - generic [ref=e63]:
          - paragraph [ref=e64]: Build
          - button "Scripts" [ref=e66] [cursor=pointer]:
            - img [ref=e68]
            - generic [ref=e71]: Scripts
          - button "Scenarios" [ref=e73] [cursor=pointer]:
            - img [ref=e75]
            - generic [ref=e77]: Scenarios
        - generic [ref=e78]:
          - paragraph [ref=e79]: Administer
          - button "Repository" [ref=e81] [cursor=pointer]:
            - img [ref=e83]
            - generic [ref=e86]: Repository
          - button "Access" [ref=e88] [cursor=pointer]:
            - img [ref=e90]
            - generic [ref=e94]: Access
          - button "Settings" [ref=e96] [cursor=pointer]:
            - img [ref=e98]
            - generic [ref=e101]: Settings
        - generic [ref=e102]:
          - paragraph [ref=e103]: Support
          - button "Help & documentation" [ref=e105] [cursor=pointer]:
            - img [ref=e107]
            - generic [ref=e110]: Help & documentation
      - generic [ref=e111]:
        - generic [ref=e112]:
          - 'button "Preview permissions. Current mode: admin" [ref=e114] [cursor=pointer]':
            - generic [ref=e115]: DEV
            - generic [ref=e116]: Preview permissions
          - generic [ref=e117]:
            - generic "Live" [ref=e118]:
              - generic [ref=e120]: Live
            - 'button "Build info: local · dev" [ref=e122] [cursor=pointer]':
              - generic [ref=e124]: Version Info
        - button "Collapse sidebar" [expanded] [ref=e126] [cursor=pointer]:
          - img [ref=e127]
    - main "Run Results" [active] [ref=e129]:
      - status [ref=e130]: Run Results
      - generic "Dashboard controls" [ref=e131]:
        - heading "Run Results" [level=1] [ref=e133]
        - generic [ref=e134]:
          - link "Help for this page" [ref=e135] [cursor=pointer]:
            - /url: "#/docs?guide=user-guide&article=results"
          - generic "Deployment identity" [ref=e136]:
            - generic [ref=e137]: Browser · Server
          - combobox "Dashboard color theme" [ref=e140]:
            - option "System (Auto)"
            - option "Dark Theme" [selected]
            - option "Light Theme"
            - option "Solarized Dark"
            - option "Solarized Light"
            - option "Monokai Dimmed"
      - generic [ref=e141]:
        - generic [ref=e142]:
          - generic [ref=e143]:
            - paragraph [ref=e144]: Run results · LOC-RUN-20261006-002
            - heading "command-clean-interface-run" [level=1] [ref=e145]
            - paragraph [ref=e146]:
              - generic [ref=e147]: ABORTED
              - text: · Configured 2 VUsers for 5m · Lifecycle elapsed 4s · Archived evidence elapsed 0m 4s
          - generic [ref=e148]:
            - button "Compare" [ref=e149] [cursor=pointer]
            - button "Export report" [ref=e150] [cursor=pointer]
            - button "Test notes" [ref=e151] [cursor=pointer]
        - region "Execution outcome" [ref=e152]:
          - heading "Execution aborted" [level=2] [ref=e153]
          - paragraph [ref=e154]: No termination detail was recorded.
          - paragraph [ref=e155]: Execution status is separate from sampled transaction success and performance acceptance.
        - group [ref=e156]:
          - generic "Run timeline annotations" [ref=e157] [cursor=pointer]
        - tablist "Result sections" [ref=e158]:
          - tab "Summary" [ref=e159] [cursor=pointer]
          - tab "Performance" [ref=e160] [cursor=pointer]
          - tab "Baseline Comparison" [ref=e161] [cursor=pointer]
          - tab "Diagnostics" [ref=e162] [cursor=pointer]
          - tab "Evidence" [ref=e163] [cursor=pointer]
          - tab "Execution" [selected] [ref=e164] [cursor=pointer]
        - tabpanel "Execution" [ref=e165]:
          - paragraph [ref=e166]: Read-only execution record. Original configuration is preserved. Execution completion and acceptance are assessed separately.
          - generic "Execution facts" [ref=e167]:
            - generic [ref=e168]:
              - term [ref=e169]: Configured duration
              - definition [ref=e170]: 5m
            - generic [ref=e171]:
              - term [ref=e172]: Lifecycle elapsed
              - definition [ref=e173]: 4s
            - generic [ref=e174]:
              - term [ref=e175]: Archived evidence elapsed
              - definition [ref=e176]: 0m 4s
            - generic [ref=e177]:
              - term [ref=e178]: Configured VUsers
              - definition [ref=e179]: "2"
            - generic [ref=e180]:
              - term [ref=e181]: Observed peak VUsers
              - definition [ref=e182]: "2"
          - region "Execution load timeline" [ref=e183]:
            - heading "Configured and observed load" [level=2] [ref=e184]
            - paragraph [ref=e185]: "Configured load: 2 VUsers. This reference is not a ramp schedule. Observed samples cover only the captured intervals."
            - generic "Captured VUsers with configured load reference" [ref=e186]:
              - 'figure "Configured and observed load. Only displayed series and measurements are exported; missing values remain empty. {\"run_id\":\"4dccbe74-081f-49a4-b0c0-6a12a5b1a26d\",\"configured_vusers\":2,\"configuration_immutable\":true}" [ref=e187]':
                - generic "Configured and observed load export actions" [ref=e189]:
                  - button "Export Configured and observed load data as CSV" [ref=e190] [cursor=pointer]: CSV
                  - button "Export Configured and observed load image as SVG" [ref=e191] [cursor=pointer]: SVG
                - paragraph [ref=e192]: "Configured and observed load. Only displayed series and measurements are exported; missing values remain empty. {\"run_id\":\"4dccbe74-081f-49a4-b0c0-6a12a5b1a26d\",\"configured_vusers\":2,\"configuration_immutable\":true}"
                - application [ref=e196]:
                  - generic [ref=e214]:
                    - generic [ref=e215]:
                      - generic [ref=e217]: 12:19:49 AM
                      - generic [ref=e219]: 12:19:50 AM
                      - generic [ref=e221]: 12:19:51 AM
                    - generic [ref=e222]:
                      - generic [ref=e224]: "0"
                      - generic [ref=e226]: "0.5"
                      - generic [ref=e228]: "1"
                      - generic [ref=e230]: "1.5"
                      - generic [ref=e232]: "2"
            - paragraph [ref=e233]: "Solid curve: observed VUsers. Dashed line: configured VUsers."
          - region "Execution groups" [ref=e234]:
            - heading "Groups and configuration" [level=2] [ref=e235]
            - paragraph [ref=e236]: Configuration preserved in the immutable run manifest.
            - table [ref=e238]:
              - rowgroup [ref=e239]:
                - row "Group Script Configured VUsers Configuration details" [ref=e240]:
                  - columnheader "Group" [ref=e241]
                  - columnheader "Script" [ref=e242]
                  - columnheader "Configured VUsers" [ref=e243]
                  - columnheader "Configuration details" [ref=e244]
              - rowgroup [ref=e245]:
                - row "smoke command-clean-interface-script 2" [ref=e246]:
                  - rowheader "smoke" [ref=e247]
                  - cell "command-clean-interface-script" [ref=e248]
                  - cell "2" [ref=e249]
                  - cell [ref=e250]:
                    - group [ref=e251]:
                      - generic "View configuration" [ref=e252] [cursor=pointer]
          - region "Execution events" [ref=e253]:
            - heading "Execution events" [level=2] [ref=e254]
            - generic [ref=e255]:
              - generic [ref=e256]:
                - term [ref=e257]: Created
                - definition [ref=e258]: 10/6/2026, 12:19:47 AM
              - generic [ref=e259]:
                - term [ref=e260]: Started
                - definition [ref=e261]: 10/6/2026, 12:19:47 AM
              - generic [ref=e262]:
                - term [ref=e263]: Ended
                - definition [ref=e264]: 10/6/2026, 12:19:51 AM
            - list [ref=e265]:
              - listitem [ref=e266]:
                - strong [ref=e267]: Run created
                - generic [ref=e268]: 10/6/2026, 12:19:47 AM · success
              - listitem [ref=e269]:
                - strong [ref=e270]: Run started
                - generic [ref=e271]: 10/6/2026, 12:19:47 AM · success
              - listitem [ref=e272]:
                - strong [ref=e273]: Abort requested
                - generic [ref=e274]: 10/6/2026, 12:19:51 AM · success
          - region "Telemetry budget policy" [ref=e275]:
            - heading "Telemetry budget policy" [level=3] [ref=e276]
            - paragraph [ref=e277]: Immutable receipt captured before runtime staging. Later system settings do not rewrite these budgets.
            - generic [ref=e278]:
              - generic [ref=e279]:
                - term [ref=e280]: Policy
                - definition [ref=e281]: system-telemetry-budgets · revision 1
              - generic [ref=e282]:
                - term [ref=e283]: Policy hash
                - definition [ref=e284]:
                  - code [ref=e285]: 640e27511083a7480f596f76292326e320162d8ec9a694cce68bb54dc19a2059
              - generic [ref=e286]:
                - term [ref=e287]: Captured at
                - definition [ref=e288]: 2026-10-06T04:19:47.656209Z
              - generic [ref=e289]:
                - term [ref=e290]: Controller publication interval
                - definition [ref=e291]: 1,000 ms · local controller ticker
              - generic [ref=e292]:
                - term [ref=e293]: Sustained warning window
                - definition [ref=e294]: 15,000 ms
              - generic [ref=e295]:
                - term [ref=e296]: Classification algorithm
                - definition [ref=e297]: legacy-mixed-diagnostic.v1
            - generic "Retained telemetry stage budgets" [ref=e298]:
              - generic [ref=e299]:
                - term [ref=e300]: Sample age
                - definition [ref=e301]: 2000 ms
              - generic [ref=e302]:
                - term [ref=e303]: Generator processing
                - definition [ref=e304]: 500 ms
              - generic [ref=e305]:
                - term [ref=e306]: Transport
                - definition [ref=e307]: 500 ms
              - generic [ref=e308]:
                - term [ref=e309]: Controller fan-out
                - definition [ref=e310]: 500 ms
              - generic [ref=e311]:
                - term [ref=e312]: Browser render
                - definition [ref=e313]: 500 ms
            - paragraph [ref=e314]: Policy availability describes retained settings. It does not establish synchronized clocks, wall-time coverage, exact loss totals, or a telemetry CI verdict. Missing intervals remain unknown.
          - group [ref=e315]:
            - generic "Configuration and infrastructure evidence" [ref=e316] [cursor=pointer]
  - generic [ref=e317]: "0"
```

# Test source

```ts
  1  | import { expect, test } from './support/liveRun'
  2  | 
  3  | test('advanced details returns to the same run without losing cockpit edits or graph mode', async ({ page, seedLiveRun }) => {
  4  |   test.setTimeout(150_000)
  5  |   const run = await seedLiveRun({ label: 'command-details-return' })
  6  |   await page.goto(`/#/command-center/${run.testId}`)
  7  |   const cockpit = page.locator('.command-center-page')
  8  |   const target = cockpit.getByRole('spinbutton', { name: 'smoke target VUsers', exact: true })
  9  |   await target.fill('5')
  10 |   await cockpit.getByRole('searchbox', { name: 'Filter command center table rows' }).fill('smoke')
  11 |   await cockpit.getByRole('button', { name: 'Advanced run details', exact: true }).click()
  12 |   await expect(page).toHaveURL(new RegExp(`/test/${run.testId}\\?`))
  13 |   await expect(page.getByRole('tab', { name: /^Run state/ })).toHaveAttribute('aria-selected', 'true')
  14 |   await expect(cockpit).toBeHidden()
  15 |   await page.getByRole('button', { name: 'Back to Command Center', exact: true }).click()
  16 |   await expect(page).toHaveURL(new RegExp(`/command-center/${run.testId}`))
  17 |   await expect(target).toHaveValue('5')
  18 |   await expect(cockpit.getByRole('searchbox')).toHaveValue('smoke')
  19 |   await expect(cockpit.getByRole('button', { name: 'Advanced run details', exact: true })).toBeFocused()
  20 |   await cockpit.getByRole('button', { name: 'Graph Wall', exact: true }).click()
  21 |   await cockpit.getByRole('button', { name: 'Advanced run details', exact: true }).click()
  22 |   await page.reload()
  23 |   await page.getByRole('button', { name: 'Back to Command Center', exact: true }).click()
  24 |   await expect(page).toHaveURL(new RegExp(`/command-center/${run.testId}`))
  25 |   // A reload has no in-memory draft to restore, but must still return to this run.
  26 |   await expect(cockpit).toBeVisible()
  27 |   await cockpit.getByRole('button', { name: 'Graph Wall', exact: true }).click()
  28 |   await cockpit.getByRole('button', { name: 'Advanced run details', exact: true }).click()
  29 |   await page.getByRole('button', { name: 'Back to Command Center', exact: true }).click()
  30 |   await expect(cockpit.getByRole('button', { name: 'Graph Wall', exact: true })).toHaveAttribute('aria-pressed', 'true')
  31 | })
  32 | 
  33 | test('runtime workspace keeps essential metrics readable and settings in an opaque drawer', async ({ page, seedLiveRun }, testInfo) => {
  34 |   const run = await seedLiveRun({ label: 'command-clean-interface' })
  35 |   await page.setViewportSize({ width: 1440, height: 1000 })
  36 |   await page.goto(`/#/command-center/${run.testId}`)
  37 |   const cockpit = page.locator('.command-center-page')
  38 |   const scenario = cockpit.getByRole('tabpanel', { name: 'Scenario run metrics' })
  39 |   await expect(scenario.getByRole('columnheader')).toHaveCount(7)
  40 |   await scenario.getByRole('checkbox', { name: 'Show all 17 metrics' }).check()
  41 |   await expect(scenario.getByRole('columnheader')).toHaveCount(17)
  42 |   await cockpit.getByRole('searchbox').fill('smoke')
  43 |   await cockpit.getByRole('button', { name: 'Graph Wall', exact: true }).click()
  44 |   await expect(cockpit.getByRole('button', { name: 'Change duration', exact: true })).toBeVisible()
  45 |   await expect(cockpit.locator('.command-run-stats')).toContainText('TPS')
  46 |   await cockpit.getByRole('button', { name: 'Configure Graphs', exact: true }).click()
  47 |   const drawer = page.getByRole('dialog', { name: 'Graph configuration', exact: true })
  48 |   await expect(drawer).toBeVisible()
  49 |   await expect(drawer).toContainText('Graph Cards')
  50 |   const graphDisplayOptions = cockpit.getByRole('region', { name: 'Graph display options', exact: true })
  51 |   await expect(graphDisplayOptions).toContainText('Display Window')
  52 |   await expect(graphDisplayOptions).toContainText('Reset Graphs')
  53 |   expect(await drawer.evaluate(element => getComputedStyle(element).backgroundColor)).toMatch(/^rgb\(/)
  54 |   await page.screenshot({ path: testInfo.outputPath('graph-configuration.png'), fullPage: true })
  55 |   await page.keyboard.press('Escape')
  56 |   await expect(drawer).toBeHidden()
  57 |   await expect(cockpit.getByRole('button', { name: 'Configure Graphs', exact: true })).toBeFocused()
  58 |   await cockpit.getByRole('button', { name: 'OPS View', exact: true }).click()
  59 |   await expect(cockpit.getByRole('searchbox')).toHaveValue('smoke')
  60 |   await expect(scenario.getByRole('checkbox', { name: 'Show all 17 metrics' })).toBeChecked()
  61 |   await scenario.getByRole('checkbox', { name: 'Show all 17 metrics' }).uncheck()
  62 |   await cockpit.getByRole('tab', { name: 'Error Details', exact: true }).click()
  63 |   await expect(cockpit.getByRole('tabpanel', { name: 'Error details', exact: true })).toContainText('No errors recorded.')
  64 |   await cockpit.getByRole('tab', { name: 'Scenario Run', exact: true }).click()
  65 |   await page.locator('.main-content').evaluate(element => { element.scrollTop = 400 })
  66 |   const header = await page.locator('.main-topbar').boundingBox()
  67 |   const shelf = await cockpit.locator('.command-control-shelf').boundingBox()
  68 |   expect(shelf!.y).toBeGreaterThanOrEqual(header!.y + header!.height - 1)
  69 |   await page.screenshot({ path: testInfo.outputPath('operations-desktop.png'), fullPage: true })
  70 |   await page.setViewportSize({ width: 900, height: 1000 })
  71 |   const currentLoad = cockpit.locator('.command-group-row [data-label="Current VU"]').first()
  72 |   expect(await currentLoad.evaluate(element => getComputedStyle(element, '::before').content)).toBe('"Current VU"')
  73 |   const groupActions = cockpit.getByRole('button', { name: 'smoke group actions', exact: true })
  74 |   await groupActions.evaluate(element => element.scrollIntoView({ block: 'start' }))
  75 |   await groupActions.focus()
  76 |   const narrowShelf = await cockpit.locator('.command-control-shelf').boundingBox()
  77 |   const actionBox = await groupActions.boundingBox()
> 78 |   expect(actionBox!.y).toBeGreaterThanOrEqual(narrowShelf!.y + narrowShelf!.height - 1)
     |                        ^ Error: expect(received).toBeGreaterThanOrEqual(expected)
  79 |   await page.screenshot({ path: testInfo.outputPath('operations-narrow.png'), fullPage: true })
  80 | })
  81 | 
```