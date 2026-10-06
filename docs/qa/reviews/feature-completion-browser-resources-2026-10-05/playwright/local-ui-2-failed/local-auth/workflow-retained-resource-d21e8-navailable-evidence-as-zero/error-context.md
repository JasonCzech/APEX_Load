# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: workflow-retained-resource-inspector.spec.ts >> retained resource inspector refuses a mismatched page and retries without publishing unavailable evidence as zero
- Location: e2e/workflow-retained-resource-inspector.spec.ts:173:1

# Error details

```
TimeoutError: locator.click: Timeout 15000ms exceeded.
Call log:
  - waiting for getByRole('tab', { name: 'Diagnostics', exact: true })

```

# Page snapshot

```yaml
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
        - generic "Local System Administrator • admin@apexload.local • System Admin" [ref=e109]:
          - generic [ref=e110]: LS
          - generic [ref=e111]:
            - generic [ref=e112]: Local System Administrator
            - generic [ref=e113]: System Admin
            - button "Sign out" [ref=e114] [cursor=pointer]
        - generic [ref=e115]:
          - generic "Live" [ref=e116]:
            - generic [ref=e118]: Live
          - 'button "Build info: local · dev" [ref=e120] [cursor=pointer]':
            - generic [ref=e122]: Version Info
      - button "Collapse sidebar" [expanded] [ref=e124] [cursor=pointer]:
        - img [ref=e125]
  - main "Run Results" [active] [ref=e127]:
    - status [ref=e128]: Run Results
    - generic "Dashboard controls" [ref=e129]:
      - heading "Run Results" [level=1] [ref=e131]
      - generic [ref=e132]:
        - link "Help for this page" [ref=e133] [cursor=pointer]:
          - /url: "#/docs?guide=user-guide&article=results"
        - generic "Deployment identity" [ref=e134]:
          - generic [ref=e135]: Browser · Server
        - combobox "Dashboard color theme" [ref=e138]:
          - option "System (Auto)"
          - option "Dark Theme" [selected]
          - option "Light Theme"
          - option "Solarized Dark"
          - option "Solarized Light"
          - option "Monokai Dimmed"
    - alert [ref=e139]: Project 338afa4e-e715-449a-8abc-f5b298a5cbf3 is unavailable. Select an accessible project to continue.
```

# Test source

```ts
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
  98  |   await inspector.getByLabel('Resources per page', { exact: true }).selectOption('25')
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
  151 |     const owner = node.closest('.main-content')!.getBoundingClientRect()
  152 |     const topbar = document.querySelector('.main-topbar')?.getBoundingClientRect().bottom ?? 0
  153 |     const hit = document.elementFromPoint(box.left + box.width / 2, box.top + box.height / 2)
  154 |     return { left: box.left, right: box.right, top: box.top, bottom: box.bottom, width: box.width, height: box.height,
  155 |       trackLeft: track.left, trackRight: track.right, ownerLeft: owner.left, ownerRight: owner.right, topbar,
  156 |       viewportWidth: window.innerWidth, viewportHeight: window.innerHeight, hitOwned: hit === node || node.parentElement!.contains(hit),
  157 |       display: style.display, visibility: style.visibility, opacity: style.opacity, borderWidth: style.borderLeftWidth }
  158 |   })
  159 |   await test.info().attach('resource-inspector-measured-zero-point', { body: JSON.stringify(point), contentType: 'application/json' })
  160 |   expect(point.width).toBeGreaterThan(0); expect(point.height).toBeGreaterThan(0)
  161 |   expect(point.left).toBeGreaterThanOrEqual(point.trackLeft); expect(point.right).toBeLessThanOrEqual(point.trackRight + 1)
  162 |   expect(point.display).not.toBe('none'); expect(point.visibility).toBe('visible'); expect(Number(point.opacity)).toBeGreaterThan(0)
  163 |   expect(parseFloat(point.borderWidth)).toBeGreaterThan(0)
  164 |   expect(point.left).toBeGreaterThanOrEqual(Math.max(0, point.ownerLeft))
  165 |   expect(point.right).toBeLessThanOrEqual(Math.min(point.viewportWidth, point.ownerRight))
  166 |   expect(point.top).toBeGreaterThanOrEqual(point.topbar); expect(point.bottom).toBeLessThanOrEqual(point.viewportHeight)
  167 |   expect(point.hitOwned).toBe(true)
  168 |   await expect(native.locator('.browser-resource-phases li').filter({ hasText: 'request wait' }).locator('.browser-resource-phase-bar')).toHaveCount(0)
  169 |   await native.screenshot({ path: test.info().outputPath('retained-resource-native-detail.png') })
  170 |   await test.info().attach('resource-inspector-transport-queries', { body: JSON.stringify(queries), contentType: 'application/json' })
  171 | })
  172 | 
  173 | test('retained resource inspector refuses a mismatched page and retries without publishing unavailable evidence as zero', async ({ page, trackRun }) => {
  174 |   const { id, workspace } = await fixture(page, trackRun)
  175 |   let state: 'foreign' | 'unavailable' | 'zero' = 'foreign'
  176 |   await page.route(`**/api/v1/tests/${id}/archive/browser-resources*`, async route => {
  177 |     const body = pageFor(id, workspace.projectId, [], 0)
  178 |     if (state === 'foreign') body.test_id = 'foreign-private-run'
  179 |     if (state === 'unavailable') body.population = { availability: 'unavailable', transaction_records: 2, retained_entry_observations: null, matched_retained_observations: null }
  180 |     body.source_integrity = 'legacy_scan_fingerprint'
  181 |     await route.fulfill({ json: body })
  182 |   })
  183 |   await page.goto(`/#/report/${id}?section=diagnostics&${scopeQuery(workspace)}`)
> 184 |   await page.getByRole('tab', { name: 'Diagnostics', exact: true }).click()
      |                                                                     ^ TimeoutError: locator.click: Timeout 15000ms exceeded.
  185 |   await page.getByText('Browser evidence', { exact: true }).click()
  186 |   await page.getByRole('button', { name: 'Inspect retained archive resources', exact: true }).click()
  187 |   const inspector = page.getByRole('region', { name: 'Retained archive resource inspector', exact: true })
  188 |   await expect(inspector.getByRole('alert')).toContainText('Unable to load')
  189 |   await expect(inspector).not.toContainText('foreign-private-run')
  190 |   await expect(inspector.getByRole('button', { name: 'Next resource page', exact: true })).toBeDisabled()
  191 |   state = 'unavailable'
  192 |   await inspector.getByRole('button', { name: 'First page / retry', exact: true }).click()
  193 |   await expect(inspector).toContainText('The archive resource projection is unavailable')
  194 |   await expect(inspector).not.toContainText('0 retained observations')
  195 |   await expect(inspector).not.toContainText('0 retained entry observations')
  196 |   await expect(inspector).toContainText('producer integrity is unverified')
  197 |   state = 'zero'
  198 |   await inspector.getByRole('button', { name: 'First page / retry', exact: true }).click()
  199 |   await expect(inspector).toContainText('0 match these filters out of 80')
  200 |   await expect(inspector).toContainText('No retained observations match these filters')
  201 |   await expect(inspector).not.toContainText('Healthy')
  202 | })
  203 | 
```