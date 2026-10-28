import axe from 'axe-core'
import type { BrowserContext, Locator } from '@playwright/test'
import { test as workflowTest, expect, login } from './support/workflow'

const localAuth = process.env.APEXLOAD_E2E_PROFILE === 'local-auth'
type HelpAuthState = Awaited<ReturnType<BrowserContext['storageState']>> | undefined
// Read-only Help tests reuse one authenticated session. Repeated persona logins
// would exhaust the controller's real per-user limit and affect later workflows.
const test = workflowTest.extend<Record<never, never>, { helpAuthState: HelpAuthState }>({
  helpAuthState: [async ({ browser }, provide) => {
    if (!localAuth) { await provide(undefined); return }
    const context = await browser.newContext({ baseURL: process.env.APEXLOAD_E2E_BASE_URL || 'http://127.0.0.1:18081' })
    try {
      await login(await context.newPage())
      await provide(await context.storageState())
    } finally { await context.close() }
  }, { scope: 'worker' }],
  storageState: async ({ helpAuthState }, provide) => { await provide(helpAuthState) },
})

// Help draws each code example in a read-only CodeMirror view and keeps its
// source in data-code, the text "Copy example" copies. CodeMirror renders only
// the lines within about a screen of the viewport, so with the example's top
// in view the rendered lines must be the source's lines from the first one on;
// the source is returned only then.
async function shownCodeExample(example: Locator) {
  await expect(example.locator('.cm-content')).toBeVisible()
  await example.evaluate((element: HTMLElement) => element.scrollIntoView({ block: 'start' }))
  let code = ''
  await expect(async () => {
    const rendered = await example.evaluate((element: HTMLElement) => ({
      code: element.dataset.code ?? '',
      shown: Array.from(element.querySelectorAll('.cm-line'), line => line.textContent ?? ''),
    }))
    expect(rendered.shown.length, 'rendered example lines').toBeGreaterThan(0)
    expect(rendered.shown, "the rendered lines start the example's source").toEqual(rendered.code.split('\n').slice(0, rendered.shown.length))
    code = rendered.code
  }).toPass({ timeout: 5_000 })
  return code
}
test.beforeEach(async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.addInitScript({ content: axe.source })
  await page.goto('/#/docs')
  await expect(page.getByRole('heading', { name: 'Your first test', exact: true })).toBeVisible()
})

test('Help tasks, search and section links survive navigation and validate the first-test example', async ({ page }) => {
  const areas = page.getByRole('navigation', { name: 'Help areas' })
  await expect(areas.getByRole('link')).toHaveCount(5)
  await expect(areas.getByRole('link', { name: 'Getting Started', exact: true })).toHaveAttribute('aria-current', 'page')
  const example = await shownCodeExample(page.locator('.help-code-example .markdown-code-editor[data-language="json"]'))
  // Validate the copied example, checked against what CodeMirror rendered, against the real controller contract.
  const response = await page.request.post('/api/v1/scripts/validate', { data: JSON.parse(example) })
  expect(response.ok()).toBe(true)
  const validation = await response.json()
  expect(validation.valid, JSON.stringify(validation)).toBe(true)

  await page.getByRole('searchbox', { name: 'Search all help articles' }).fill('pacing 250')
  const result = page.locator('.help-search-results').getByRole('link', { name: 'Model load', exact: true })
  await expect(result).toBeVisible()
  await result.click()
  await expect(page.getByRole('heading', { name: 'Model load', exact: true })).toBeAttached()
  await expect(page).toHaveURL(/article=scenarios/)
  const section = new URLSearchParams(new URL(page.url()).hash.split('?')[1]).get('section')!
  const heading = page.locator(`[id="${section}"]`)
  await expect(heading).toBeFocused()
  const assertBelowToolbar = async () => {
    const target = await heading.boundingBox()
    const toolbar = await page.locator('.main-topbar').boundingBox()
    expect(target!.y).toBeGreaterThanOrEqual(toolbar!.y + toolbar!.height)
  }
  await assertBelowToolbar()
  await page.reload()
  await expect(heading).toBeFocused()
  await assertBelowToolbar()
  await areas.getByRole('link', { name: 'Getting Started', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Your first test', exact: true })).toBeVisible()
  await expect.poll(() => page.locator('.main-content').evaluate(el => el.scrollTop)).toBe(0)
  await page.goBack()
  await expect(heading).toBeFocused()

  await page.goto('/#/docs?guide=user-guide&article=imports')
  await expect(page.locator('.markdown-body tbody tr').last()).toBeAttached()
  await expect(page.locator('.markdown-body table').first().locator('tbody tr')).toHaveCount(17)
  await expect(page.locator('.markdown-body table').first()).toContainText('Oracle E-Business / Siebel')
  await expect(page.locator('.guide-markdown-article [aria-label*="pagination" i]')).toHaveCount(0)
})

test('DSL operators can discover the guide, validate its example, and follow reference links', async ({ page }, testInfo) => {
  await page.getByRole('searchbox', { name: 'Search all help articles' }).fill('lightweight goroutine')
  await page.locator('.help-search-results').getByRole('link', { name: 'DSL scripting for LoadRunner & JMeter operators', exact: true }).click()
  await expect(page).toHaveURL(/article=dsl-operator-guide/)
  await expect(page.locator('#understand-how-one-user-runs')).toBeFocused()
  await page.reload()
  await expect(page.locator('#understand-how-one-user-runs')).toBeFocused()

  await page.getByRole('navigation', { name: 'On this page' }).getByRole('link', { name: 'Walk through a complete HTTP example', exact: true }).click()
  const examples = page.locator('.help-code-example .markdown-code-editor[data-language="json"]')
  const script = JSON.parse(await shownCodeExample(examples.first()))
  const response = await page.request.post('/api/v1/scripts/validate', { data: { script } })
  expect(response.ok()).toBe(true)
  const validation = await response.json()
  expect(validation.valid, JSON.stringify(validation)).toBe(true)
  // Validate the documented control-flow fragment in its stated execution context.
  const loop = JSON.parse(await shownCodeExample(examples.last()))
  const loopResponse = await page.request.post('/api/v1/scripts/validate', { data: { script: { ...script, actions: [...script.actions, loop] } } })
  expect(loopResponse.ok()).toBe(true)
  const loopValidation = await loopResponse.json()
  expect(loopValidation.valid, JSON.stringify(loopValidation)).toBe(true)

  await page.getByRole('navigation', { name: 'Help articles' }).getByRole('link', { name: 'DSL action reference', exact: true }).click()
  await page.locator('.help-reading-pane').getByRole('link', { name: 'DSL scripting for LoadRunner & JMeter operators', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'DSL scripting for LoadRunner & JMeter operators', exact: true })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('dsl-operator-guide-wide.png'), animations: 'disabled' })
  await page.setViewportSize({ width: 760, height: 900 })
  const codeRegion = page.getByRole('region', { name: 'json code example', exact: true }).first()
  await expect(codeRegion).toHaveAttribute('tabindex', '0')
  await codeRegion.focus()
  await expect(codeRegion).toBeFocused()
  await codeRegion.press('ArrowRight')
  await expect.poll(() => codeRegion.evaluate(el => el.scrollLeft)).toBeGreaterThan(0)
  await page.getByRole('navigation', { name: 'On this page' }).getByRole('link', { name: 'Translate familiar concepts', exact: true }).click()
  await expect(page.locator('#translate-familiar-concepts')).toBeFocused()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true)
  const violations = await page.evaluate(async () => (await (window as typeof window & { axe: typeof axe }).axe.run('.help-workspace', { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa'] } })).violations)
  expect(violations).toEqual([])
  await page.screenshot({ path: testInfo.outputPath('dsl-operator-guide-narrow.png'), animations: 'disabled' })
})

test('evidence guides are searchable, readable and retain their scope after reload', async ({ page }, testInfo) => {
  const guides = [
    { query: 'Decimal precision review', id: 'native-analytics', title: 'Native analytics', body: 'Original source bytes remain unchanged.' },
    { query: 'provisional history', id: 'browser-document-evidence', title: 'Browser document evidence', body: 'No-interaction INP is unavailable.' },
    { query: 'paint opportunity', id: 'telemetry-policy-receipts', title: 'Telemetry budget policy receipts', body: 'explicit zero disables a stage' },
    { query: 'sub-millisecond thresholds', id: 'authoring-scenario-fidelity', title: 'Scenario and queued-run authoring fidelity', body: 'A present empty section is a whole-section replacement.' },
  ]
  for (const guide of guides) {
    await page.getByRole('searchbox', { name: 'Search all help articles' }).fill(guide.query)
    await page.locator('.help-search-results').getByRole('link', { name: guide.title, exact: true }).click()
    await expect(page).toHaveURL(new RegExp(`article=${guide.id}`))
    await expect(page.getByRole('heading', { name: guide.title, exact: true })).toBeVisible()
    await expect(page.locator('.guide-markdown-article')).toBeVisible()
    await expect(page.locator('.guide-markdown-article')).toContainText(guide.body)
    if (guide.id === 'telemetry-policy-receipts') {
      await expect(page.locator('.guide-markdown-article')).toContainText('not proof that a frame was visibly presented')
      await expect(page.locator('.guide-markdown-article')).not.toContainText('to the first paint after it rendered')
      await expect(page.locator('.guide-markdown-article')).toContainText('Durable acceptance notifications')
      await expect(page.locator('.guide-markdown-article')).toContainText('capacity_exhausted')
      await expect(page.locator('.guide-markdown-article')).toContainText('at least once')
      await page.goto('/#/docs?guide=user-guide&article=telemetry-policy-receipts&section=durable-acceptance-notifications')
      await expect(page.locator('#durable-acceptance-notifications')).toBeFocused()
    }
    await expect(page.locator('.guide-markdown-article')).toContainText('remain open')
    await page.reload()
    await expect(page.locator('.guide-markdown-article')).toContainText(guide.body)
    await page.setViewportSize({ width: 760, height: 900 })
    await expect(page.locator('.help-workspace')).toBeVisible()
    expect(await page.locator('.help-workspace').evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath(`${guide.id}-narrow.png`), animations: 'disabled' })
  }
})

test('HAR ownership help distinguishes caller waits from capture finalization and retains its limits', async ({ page }, testInfo) => {
  await page.getByRole('searchbox', { name: 'Search all help articles' }).fill('har_finalization_budget_exhausted')
  await page.locator('.help-search-results').getByRole('link', { name: 'Native HAR working-file ownership', exact: true }).click()
  await expect(page).toHaveURL(/article=browser-har-ownership/)
  const article = page.locator('.guide-markdown-article')
  await expect(article).toContainText('30-second capture finalization deadline')
  await expect(article).toContainText('two-second caller wait is separate')
  await expect(article).toContainText('does not forcibly terminate a callback')
  await expect(article).toContainText('1,024 outstanding issued requests')
  await expect(article).toContainText('har_in_flight')
  await expect(article).toContainText('document observations censored')
  await expect(article).toContainText('native_capture_failed')
  await expect(article).toContainText('native_owner_unretired')
  await expect(article).toContainText('native_omission_count_limit')
  await expect(article).toContainText('X-APEXLoad-Replay-Durability')
  await expect(article).toContainText('never falls back to synthetic evidence')
  await expect(article).toContainText('Cache eviction does not free a durable replay ID')
  await expect(article).toContainText('does not release a writer')
  await page.goto('/#/docs?guide=user-guide&article=browser-har-ownership&section=outstanding-request-limit')
  await expect(article.locator('#outstanding-request-limit')).toBeFocused()
  await page.reload()
  await expect(article).toContainText('har_finalization_exhausted')
  await page.setViewportSize({ width: 760, height: 900 })
  expect(await page.locator('.help-workspace').evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  await page.screenshot({ path: testInfo.outputPath('har-finalization-budget-help.png'), animations: 'disabled' })
  await article.getByText('native_owner_unretired', { exact: true }).scrollIntoViewIfNeeded()
  await expect(article.getByText('native_owner_unretired', { exact: true })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('har-capture-omissions-help.png'), animations: 'disabled' })
  const durability = article.getByText('X-APEXLoad-Replay-Durability', { exact: true })
  await durability.scrollIntoViewIfNeeded()
  await expect(durability).toBeInViewport()
  await page.screenshot({ path: testInfo.outputPath('har-replay-durability-help.png'), animations: 'disabled' })
})

test('legacy URLs, generated API search and contextual guidance reach the right articles', async ({ page }) => {
  for (const [hash, title] of [['#/docs?guide=quick-start', 'Overview & first API calls'], ['#/docs?guide=mcp', 'MCP setup'], ['#/docs?guide=authentication', 'API authentication'], ['#/repository-guide', 'API workflows']]) {
    await page.goto(`/${hash}`)
    await expect(page.getByRole('heading', { name: title, exact: true })).toBeVisible()
    await expect(page.getByRole('navigation', { name: 'Help areas' }).getByRole('link', { name: 'API & Automation', exact: true })).toHaveAttribute('aria-current', 'page')
  }
  await page.goto('/#/docs?guide=user-guide&article=scenarios')
  await page.getByRole('searchbox', { name: 'Search all help articles' }).fill('scripts/validate')
  await page.getByRole('link', { name: 'Search API operations for “scripts/validate”' }).click()
  await expect(page.getByRole('heading', { name: 'API Reference (OpenAPI)', exact: true })).toBeVisible()
  await expect(page.locator('.swagger-operation-card')).toHaveCount(1)
  await expect(page.locator('.swagger-operation-card')).toContainText('/api/v1/scripts/validate')
  await page.goto('/#/results')
  await page.getByRole('link', { name: 'Help for this page', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Analyze results', exact: true })).toBeVisible()
})

test('Help is readable at narrow widths and exposes accessible navigation', async ({ page }, testInfo) => {
  await page.screenshot({ path: testInfo.outputPath('help-desktop.png'), animations: 'disabled' })
  await page.setViewportSize({ width: 800, height: 900 })
  await page.goto('/#/docs?guide=user-guide&article=capabilities')
  // The article title is for screen readers; readers see the open article marked in the list and its contents in view.
  await expect(page.getByRole('heading', { name: 'Capabilities & limitations', exact: true })).toBeAttached()
  // Capabilities is a More help utility, listed once, under Help utilities.
  await expect(page.getByRole('navigation', { name: 'Help utilities' }).getByRole('link', { name: 'Capabilities & limitations', exact: true })).toHaveAttribute('aria-current', 'page')
  await expect(page.getByRole('navigation', { name: 'On this page' })).toBeInViewport()
  for (const link of await page.getByRole('navigation', { name: 'Help areas' }).getByRole('link').all()) {
    const box = await link.boundingBox()
    expect(box!.x + box!.width).toBeLessThanOrEqual(800)
  }
  // The article list is always shown (no collapsible "Browse articles" toggle), as on Settings and Access.
  await expect(page.getByRole('navigation', { name: 'Help articles' })).toBeVisible()
  await page.getByRole('navigation', { name: 'Help utilities' }).getByRole('link', { name: 'Capabilities & limitations', exact: true }).click()
  expect(await page.locator('.help-workspace').evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true)
  const violations = await page.evaluate(async () => (await (window as typeof window & { axe: typeof axe }).axe.run('.help-workspace, .help-primary-nav', { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa'] } })).violations)
  expect(violations).toEqual([])
  await page.getByRole('navigation', { name: 'On this page' }).scrollIntoViewIfNeeded()
  await page.screenshot({ path: testInfo.outputPath('help-narrow.png'), animations: 'disabled' })
})

test('non-administrators cannot open or search Administration articles', async ({ page, browser }) => {
  test.skip(!localAuth, 'Requires the restricted local-auth persona')
  const context = await browser.newContext({ baseURL: new URL(page.url()).origin, storageState: { cookies: [], origins: [] } })
  try {
    const userPage = await context.newPage()
    await login(userPage, 'user')
    await userPage.goto('/#/docs?guide=admin-guide&article=administration')
    await expect(userPage.getByRole('status').filter({ hasText: 'Administration requires administrative access' })).toBeVisible()
    await expect(userPage.getByRole('navigation', { name: 'Help areas' }).getByRole('link')).toHaveCount(4)
    await userPage.getByRole('searchbox', { name: 'Search all help articles' }).fill('Administrator tasks')
    await expect(userPage.locator('.help-search-results [role="status"]')).toHaveText('0 matching articles')
  } finally { await context.close() }
})


test('Help maintains readable contrast across the supported themes', async ({ page }) => {
  for (const theme of ['dark', 'light', 'solarized-dark', 'solarized-light', 'monokai-dimmed']) {
    await page.goto('/#/settings?setting=preferences')
    await page.getByRole('combobox', { name: 'Dashboard Theme', exact: true }).selectOption(theme)
    await page.goto('/#/docs?guide=general&article=api-overview')
    await expect(page.getByRole('heading', { name: 'Overview & first API calls', exact: true })).toBeVisible()
    // The page keeps its theme colour transitions; audit the settled colours, not a frame mid-switch.
    await page.waitForFunction(() => document.getAnimations().every(animation => animation.playState !== 'running'))
    const violations = await page.evaluate(async () => (await (window as typeof window & { axe: typeof axe }).axe.run('.help-workspace, .help-primary-nav', { runOnly: ['color-contrast'] })).violations)
    expect(violations, theme).toEqual([])
  }
})

test('administrators can read the data model as drawn, accessible diagrams in every theme', async ({ page }, testInfo) => {
  // A deep link below a dozen lazily drawn diagrams (their renderer not yet
  // loaded) lands on its section once they have drawn, not where it stood
  // while they still showed their source.
  await page.goto('/#/docs?guide=admin-guide&article=data-model&section=github-integration')
  const destination = page.locator('#github-integration')
  await expect(destination).toBeFocused()
  await expect(page.locator('pre code[data-language="mermaid"]')).toHaveCount(0)
  // scroll-margin-top places it 32 px below the toolbar; allow for rounding.
  const belowToolbar = async () => {
    const target = await destination.boundingBox()
    const toolbar = await page.locator('.main-topbar').boundingBox()
    return target && toolbar ? Math.round(target.y - (toolbar.y + toolbar.height)) : Number.NaN
  }
  await expect.poll(belowToolbar).toBeLessThanOrEqual(48)
  expect(await belowToolbar()).toBeGreaterThanOrEqual(0)
  await page.goto('/#/docs?guide=admin-guide&article=data-model')
  await expect(page.getByRole('navigation', { name: 'Help articles' }).getByRole('link', { name: 'Data model', exact: true })).toHaveAttribute('aria-current', 'page')
  const figures = page.locator('figure.entity-diagram')
  await expect(figures.first().getByRole('img')).toHaveAttribute('aria-label', /^Overview: entity relationship diagram of \d+ tables and \d+ relationships$/)
  expect(await figures.count()).toBeGreaterThan(10)
  // Every erDiagram fence draws; none is left as Mermaid source.
  await expect(page.locator('pre code[data-language="mermaid"]')).toHaveCount(0)
  await figures.first().locator('.entity-diagram-node').filter({ has: page.locator('.entity-diagram-name', { hasText: /^tests$/ }) }).hover()
  await expect(figures.first().locator('.entity-diagram-edges path.is-active')).toHaveCount(9)
  await page.screenshot({ path: testInfo.outputPath('help-data-model.png'), animations: 'disabled' })
  const audit = () => page.evaluate(async () => (await (window as typeof window & { axe: typeof axe }).axe.run('.help-workspace', { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa'] } })).violations)
  expect(await audit()).toEqual([])
  // Theme preferences persist per user, so switch the document theme only.
  for (const theme of ['dark', 'light', 'solarized-dark', 'solarized-light', 'monokai-dimmed']) {
    await page.evaluate(value => { document.documentElement.dataset.theme = value }, theme)
    await page.waitForFunction(() => document.getAnimations().every(animation => animation.playState !== 'running'))
    const violations = await page.evaluate(async () => (await (window as typeof window & { axe: typeof axe }).axe.run('figure.entity-diagram', { runOnly: ['color-contrast'] })).violations)
    expect(violations, theme).toEqual([])
  }
  // At phone width the diagrams keep legible text and scroll inside their figure, not the page.
  await page.setViewportSize({ width: 390, height: 844 })
  await expect.poll(() => figures.nth(1).getAttribute('role')).toBe('region')
  expect(await page.locator('.help-workspace').evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true)
  expect(await audit()).toEqual([])
  await page.screenshot({ path: testInfo.outputPath('help-data-model-narrow.png'), animations: 'disabled' })
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.getByRole('searchbox', { name: 'Search all help articles' }).fill('run_archive_replica_holds')
  await expect(page.locator('.help-search-results').getByRole('link', { name: /Data model/ }).first()).toBeVisible()
})

test('retained resource guide is searchable and remains selected after reload and narrowing', async ({ page }, testInfo) => {
  await page.getByRole('searchbox', { name: 'Search all help articles' }).fill('opaque continuation')
  await page.locator('.help-search-results').getByRole('link', { name: 'Retained resource inspection', exact: true }).click()
  await expect(page).toHaveURL(/article=retained-resource-inspection/)
  await expect(page.getByRole('heading', { name: 'Retained resource inspection', exact: true })).toBeAttached()
  const section = page.locator('#filter-and-page')
  await expect(section).toBeFocused()
  const reading = page.locator('.help-reading-pane')
  await expect(reading).toContainText('A zero filter match differs from an unavailable projection')
  await page.reload()
  await expect(page).toHaveURL(/article=retained-resource-inspection/)
  await expect(section).toBeFocused()
  await expect(reading).toContainText('not a complete browser network census')
  await page.setViewportSize({ width: 760, height: 1000 })
  await expect(section).toBeVisible()
  const geometry = async () => {
    const target = await section.boundingBox()
    const toolbar = await page.locator('.main-topbar').boundingBox()
    const owner = await page.locator('.main-content').boundingBox()
    if (!target || !toolbar || !owner) throw new Error('Help heading or its scroll owner is unavailable')
    return { target, toolbar, owner, viewport: page.viewportSize()! }
  }
  const assertVisible = (value: Awaited<ReturnType<typeof geometry>>) => {
    expect(value.target.y).toBeGreaterThanOrEqual(value.toolbar.y + value.toolbar.height)
    expect(value.target.y + value.target.height).toBeLessThanOrEqual(value.viewport.height)
    expect(value.target.x).toBeGreaterThanOrEqual(value.owner.x)
    expect(value.target.x + value.target.width).toBeLessThanOrEqual(Math.min(value.viewport.width, value.owner.x + value.owner.width))
  }
  // ResizeObserver re-aligns the focused anchor after the toolbar wraps.
  // Wait for that real layout update; never scroll the heading in the test.
  await expect(async () => assertVisible(await geometry())).toPass({ timeout: 5000 })
  const before = await geometry()
  await testInfo.attach('resource-help-heading-before', { body: JSON.stringify(before), contentType: 'application/json' })
  assertVisible(before)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true)
  await page.screenshot({ path: testInfo.outputPath('retained-resource-help-narrow.png'), animations: 'disabled' })
  const after = await geometry()
  await testInfo.attach('resource-help-heading-after', { body: JSON.stringify(after), contentType: 'application/json' })
  assertVisible(after)
  await page.locator('.guide-markdown-article').getByRole('link', { name: 'browser document evidence guide', exact: true }).click()
  await expect(page).toHaveURL(/article=browser-document-evidence/)
  await expect(page.getByRole('heading', { name: 'Browser document evidence', exact: true })).toBeAttached()
  await expect(page.locator('.guide-markdown-article')).toContainText('No-interaction INP is unavailable.')
  await page.reload()
  await expect(page.locator('.guide-markdown-article')).toContainText('No-interaction INP is unavailable.')
})


test('browser evidence links reach consolidated interpretation and capture contracts', async ({ page }) => {
  await page.goto('/#/docs?guide=user-guide&article=browser-document-evidence')
  const reading = page.locator('.guide-markdown-article')
  await reading.getByRole('link', { name: 'Browser intelligence workflows', exact: true }).click()
  await expect(page).toHaveURL(/article=browser-intelligence/)
  await reading.getByRole('link', { name: 'Findings contract', exact: true }).click()
  await expect(page).toHaveURL(/section=browser-findings-and-qualified-scores/)
  await expect(reading).toContainText('remaining metrics are never renormalized')
  await page.getByRole('searchbox', { name: 'Search all help articles' }).fill('weighted test-journey risk')
  await page.locator('.help-search-results').getByRole('link', { name: 'Browser intelligence workflows', exact: true }).click()
  await expect(page).toHaveURL(/article=browser-intelligence/)
  await expect(reading).toContainText('never revenue or conversion impact')
  await page.reload()
  await expect(reading).toContainText('Journey accounting')
  await page.getByRole('searchbox', { name: 'Search all help articles' }).fill('')
  await reading.getByRole('link', { name: 'Browser interaction evidence', exact: true }).click()
  await expect(page).toHaveURL(/article=browser-interaction-evidence/)
  await reading.getByRole('link', { name: 'browser intelligence guide', exact: true }).click()
  await expect(page).toHaveURL(/article=browser-intelligence.*section=patterns-and-heatmaps/)
  await reading.getByRole('link', { name: 'HAR working-file ownership', exact: true }).click()
  await expect(page).toHaveURL(/article=browser-har-ownership/)
  await expect(reading).toContainText('Run archive transfer and receipts')
  await page.setViewportSize({ width: 760, height: 900 })
  expect(await page.locator('.help-workspace').evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
})

test('monitoring help follows provider contracts and retains legacy capture limits', async ({ page }) => {
  await page.goto('/#/docs?guide=user-guide&article=dynatrace')
  const reading = page.locator('.guide-markdown-article')
  await reading.locator('a[href*="article=dynatrace-monitoring"]').filter({ hasText: 'Dynatrace lifecycle webhooks' }).click()
  await expect(page).toHaveURL(/article=dynatrace-monitoring.*section=dynatrace-lifecycle-webhooks/)
  await expect(reading).toContainText('bounded exponential backoff')
  await page.reload()
  await expect(reading).toContainText('apexload.idempotency_key')
  await page.goto('/#/docs?guide=user-guide&article=dynatrace')
  await reading.locator('a[href*="article=elastic-monitoring"]').filter({ hasText: 'Optional Elastic (ELK) in-flight monitoring' }).click()
  await expect(page).toHaveURL(/article=elastic-monitoring.*section=optional-elastic-elk-in-flight-monitoring/)
  await expect(reading).toContainText('at most 50 deduplicated samples')
  await reading.getByRole('link', { name: 'bucketed legacy-connection adapter', exact: true }).click()
  await expect(page).toHaveURL(/section=bucketed-elasticsearch-capture/)
  await expect(reading).toContainText('never reinterpreted as intervals')
  await page.getByRole('searchbox', { name: 'Search all help articles' }).fill('Shared HTTP Import Safety Contract')
  await page.locator('.help-search-results').getByRole('link', { name: 'Import fidelity and repairs', exact: true }).click()
  await expect(page).toHaveURL(/article=import-fidelity/)
  await expect(reading).toContainText('executable_bounded')
  await page.setViewportSize({ width: 760, height: 900 })
  expect(await page.locator('.help-workspace').evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
})

test('consolidated references select their owning help article and focus repository anchors', async ({ page }) => {
  const reading = page.locator('.guide-markdown-article')
  await page.goto('/#/docs?guide=user-guide&article=import-fidelity')
  await reading.getByRole('link', { name: 'Web/API Execution Support Matrix', exact: true }).click()
  await expect(page).toHaveURL(/article=web-api-support.*section=tested-adopter-dispositions/)
  await expect(reading.locator('#tested-adopter-dispositions')).toBeFocused()
  await page.goto('/#/docs?guide=user-guide&article=dynatrace')
  await reading.getByRole('link', { name: 'Power BI', exact: true }).click()
  await expect(page).toHaveURL(/article=powerbi.*section=power-bi-publishing-workflow/)
  await expect(reading.locator('#power-bi-publishing-workflow')).toBeFocused()
  // Preserve existing repository bookmarks despite the renderer's different punctuation rules.
  await page.goto('/#/docs?guide=user-guide&article=messaging&section=webapi-execution-support-matrix')
  await expect(page).toHaveURL(/article=web-api-support.*section=web-api-execution-support-matrix/)
  await expect(reading.locator('#web-api-execution-support-matrix')).toBeFocused()
  await page.reload()
  await expect(reading.locator('#web-api-execution-support-matrix')).toBeFocused()
  await page.goto('/#/docs?guide=user-guide&article=elastic-monitoring')
  await reading.locator('a[href*="section=governed-provider-registry"]').click()
  await expect(page).toHaveURL(/article=monitoring-governance.*section=governed-provider-registry/)
  await expect(reading.locator('#governed-provider-registry')).toBeFocused()
  await expect(reading).toContainText('Provider qualification')
  await page.setViewportSize({ width: 760, height: 900 })
  expect(await page.locator('.help-workspace').evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
})


test('evacuation intervention help retains its proof requirements across reload and narrow layout', async ({ page }, testInfo) => {
  await page.goto('/#/docs?guide=admin-guide&article=platform&section=resolving-an-unproven-generator-evacuation')
  const reading = page.locator('.guide-markdown-article')
  const heading = reading.locator('#resolving-an-unproven-generator-evacuation')
  await expect(heading).toBeFocused()
  await expect(reading).toContainText('automatic evacuation stop retries exhausted')
  await expect(reading).toContainText('all-generator-processes-stopped')
  await expect(reading).toContainText('A canceled controller context')
  // Repository-only evidence references stay readable text in bundled Help.
  await expect(reading).toContainText('same-leader intervention review records the local failure, persistence and authorization regressions')
  await expect(reading.getByRole('link', { name: 'same-leader intervention review', exact: true })).toHaveCount(0)
  await page.screenshot({ path: testInfo.outputPath('evacuation-intervention-wide.png'), animations: 'disabled' })
  let releaseAccess!: () => void
  const accessReady = new Promise<void>(resolve => { releaseAccess = resolve })
  const accessRoute = '**/api/v1/auth/effective-access**'
  await page.route(accessRoute, async route => { await accessReady; await route.continue() })
  try {
    await page.reload({ waitUntil: 'domcontentloaded' })
    await expect(page.getByRole('status').filter({ hasText: 'Loading help access…' })).toBeVisible()
    await expect(page).toHaveURL(/section=resolving-an-unproven-generator-evacuation/)
    await expect(heading).toHaveCount(0)
  } finally { releaseAccess() }
  await expect(heading).toBeFocused()
  await page.unroute(accessRoute)
  await page.setViewportSize({ width: 760, height: 900 })
  expect(await page.locator('.help-workspace').evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  await page.screenshot({ path: testInfo.outputPath('evacuation-intervention-narrow.png'), animations: 'disabled' })
})

// The browser map contract is an embedded Help source, not an external page.
test('browser intelligence Help preserves the map cohort contract', async ({ page }) => {
  await page.getByRole('searchbox', { name: 'Search all help articles' }).fill('fractional offsets')
  await page.locator('.help-search-results').getByRole('link', { name: 'Browser intelligence workflows', exact: true }).click()
  await expect(page).toHaveURL(/article=browser-intelligence/)
  await expect(page.locator('.guide-markdown-article')).toContainText('browser_interaction_patterns.v2')
  await expect(page.locator('.guide-markdown-article')).toContainText('dispatch-time geometry')
  await page.reload()
  await expect(page.locator('.guide-markdown-article')).toContainText('engine_profile_unknown')
})


test('browser Help distinguishes qualified native dispatch from historical evidence', async ({ page }) => {
  await page.getByRole('searchbox', { name: 'Search all help articles' }).fill('native dispatch')
  await page.locator('.help-search-results').getByRole('link', { name: 'Browser intelligence workflows', exact: true }).click()
  const article = page.locator('.guide-markdown-article')
  await expect(article).toContainText('browser_interaction_patterns.v3')
  await expect(article).toContainText('pattern_eligible')
  await expect(article).toContainText('logical invocation')
  await page.reload()
  await expect(article).toContainText('historical maps')
  await page.setViewportSize({ width: 760, height: 900 })
  expect(await page.locator('.help-workspace').evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
})


test('browser promotion Help preserves atomic evidence dependencies', async ({ page }, testInfo) => {
  await page.getByRole('searchbox', { name: 'Search all help articles' }).fill('Promoting browser intelligence support')
  await page.locator('.help-search-results').getByRole('link', { name: 'Browser intelligence workflows', exact: true }).click()
  const article = page.locator('.guide-markdown-article')
  await expect(article).toContainText('writer fence')
  await expect(article).toContainText('cohort_sha256')
  await expect(article).toContainText('record_sha256')
  await page.reload()
  const heading = article.getByRole('heading', { name: 'Promoting browser intelligence support', exact: true })
  await heading.scrollIntoViewIfNeeded()
  await expect(heading).toBeVisible()
  await page.setViewportSize({ width: 760, height: 900 })
  expect(await page.locator('.help-workspace').evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  await page.screenshot({ path: testInfo.outputPath('browser-promotion-help.png'), animations: 'disabled' })
})


test('browser metric-floor Help retains pinned revision and threshold rules', async ({ page }, testInfo) => {
  await page.getByRole('searchbox', { name: 'Search all help articles' }).fill('Per-metric baseline effect floors')
  await page.locator('.help-search-results').getByRole('link', { name: 'Browser intelligence workflows', exact: true }).click()
  const article = page.locator('.guide-markdown-article')
  await expect(article).toContainText('timing_by_metric_ms')
  await expect(article).toContainText('largest of its configured')
  await page.reload()
  const heading = article.getByRole('heading', { name: 'Per-metric baseline effect floors', exact: true })
  await heading.scrollIntoViewIfNeeded()
  await expect(heading).toBeVisible()
  await expect(article).toContainText('pinned immutable')
  await page.setViewportSize({ width: 760, height: 900 })
  expect(await page.locator('.help-workspace').evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  await page.screenshot({ path: testInfo.outputPath('browser-metric-floors-help.png'), animations: 'disabled' })
})


test('original browser source Help distinguishes source proof from runtime equivalence', async ({ page }, testInfo) => {
  await page.getByRole('searchbox', { name: 'Search all help articles' }).fill('Original authored source identity')
  await page.locator('.help-search-results').getByRole('link', { name: 'Browser intelligence workflows', exact: true }).click()
  const article = page.locator('.guide-markdown-article')
  await expect(article).toContainText('browser_original_source_v1')
  await expect(article).toContainText('original-source identity unavailable')
  await page.reload()
  const heading = article.getByRole('heading', { name: 'Original authored source identity', exact: true })
  await heading.scrollIntoViewIfNeeded()
  await expect(heading).toBeVisible()
  await expect(article).toContainText('Source equality alone')
  await page.setViewportSize({ width: 760, height: 900 })
  await heading.scrollIntoViewIfNeeded()
  await expect(heading).toBeVisible()
  expect(await page.locator('.help-workspace').evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  await page.screenshot({ path: testInfo.outputPath('browser-original-source-help.png'), animations: 'disabled' })
})


test('run policy selection Help preserves revision and admission boundaries', async ({ page }, testInfo) => {
  await page.getByRole('searchbox', { name: 'Search all help articles' }).fill('Choose policy revisions for a run')
  await page.locator('.help-search-results').getByRole('link', { name: 'Browser intelligence workflows', exact: true }).click()
  const article = page.locator('.guide-markdown-article')
  await expect(article).toContainText('source: run_selection')
  await page.reload()
  const heading = article.getByRole('heading', { name: 'Choose policy revisions for a run or recurring schedule', exact: true })
  await heading.scrollIntoViewIfNeeded()
  await expect(heading).toBeVisible()
  await expect(article).toContainText('recurring scenario schedules (scenario_schedules.analysis_policy_selections_json, schema v85) retain the selection')
  await page.setViewportSize({ width: 760, height: 900 })
  await heading.scrollIntoViewIfNeeded()
  await expect(heading).toBeVisible()
  expect(await page.locator('.help-workspace').evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  await page.screenshot({ path: testInfo.outputPath('browser-run-policy-help.png'), animations: 'disabled' })
})


test('observed browser wait Help keeps raw and analysis versions distinct', async ({ page }, testInfo) => {
  await page.getByRole('searchbox', { name: 'Search all help articles' }).fill('Observed post-action wait clocks')
  await page.locator('.help-search-results').getByRole('link', { name: 'Browser intelligence workflows', exact: true }).click()
  const article = page.locator('.guide-markdown-article')
  await expect(article).toContainText('browser_interaction_evidence_v3')
  await page.reload()
  await page.setViewportSize({ width: 760, height: 900 })
  const heading = article.getByRole('heading', { name: 'Observed post-action wait clocks', exact: true })
  await heading.scrollIntoViewIfNeeded()
  await expect(heading).toBeVisible()
  await expect(article).toContainText('Historical version-1/version-2 records never receive invented clocks')
  expect(await page.locator('.help-workspace').evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  await page.screenshot({ path: testInfo.outputPath('browser-wait-clocks-help.png'), animations: 'disabled' })
})


test('separate browser panel Help preserves cohort and comparison limits', async ({ page }, testInfo) => {
  await page.getByRole('searchbox', { name: 'Search all help articles' }).fill('Separate browser cohorts in saved panels')
  await page.locator('.help-search-results').getByRole('link', { name: 'Browser intelligence workflows', exact: true }).click()
  const article = page.locator('.guide-markdown-article')
  await expect(article).toContainText('browser_tuple_mode: separate')
  await page.reload()
  await page.setViewportSize({ width: 760, height: 900 })
  const heading = article.getByRole('heading', { name: 'Separate browser cohorts in saved panels', exact: true })
  await heading.scrollIntoViewIfNeeded()
  await expect(heading).toBeVisible()
  await expect(article).toContainText('immutable comparison-to-panel input pinning remains separate work')
  expect(await page.locator('.help-workspace').evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  await page.screenshot({ path: testInfo.outputPath('browser-tuple-panel-help.png'), animations: 'disabled' })
})


test('authored navigation Help preserves supported and unavailable identities', async ({ page }, testInfo) => {
  await page.getByRole('searchbox', { name: 'Search all help articles' }).fill('Supported authored navigation page receipts')
  await page.locator('.help-search-results').getByRole('link', { name: 'Browser intelligence workflows', exact: true }).click()
  const article = page.locator('.guide-markdown-article')
  await expect(article).toContainText('Final document measurements are not rewritten')
  await page.reload()
  await page.setViewportSize({ width: 760, height: 900 })
  const heading = article.getByRole('heading', { name: 'Supported authored navigation page receipts', exact: true })
  await heading.scrollIntoViewIfNeeded()
  await expect(heading).toBeVisible()
  await expect(article).toContainText('no page ID is guessed')
  await page.screenshot({ path: testInfo.outputPath('browser-authored-pages-help.png'), animations: 'disabled' })
})


test('native waterfall Help preserves independent timing and cache availability', async ({ page }, testInfo) => {
  await page.getByRole('searchbox', { name: 'Search all help articles' }).fill('Reading native waterfall and cache evidence')
  await page.locator('.help-search-results').getByRole('link', { name: 'Browser intelligence workflows', exact: true }).click()
  const article = page.locator('.guide-markdown-article')
  await expect(article).toContainText('A missing join leaves the waterfall unavailable')
  await page.reload()
  await page.setViewportSize({ width: 760, height: 900 })
  const heading = article.getByRole('heading', { name: 'Reading native waterfall and cache evidence', exact: true })
  await heading.scrollIntoViewIfNeeded()
  await expect(heading).toBeVisible()
  await expect(article).toContainText('a cached response must not claim a negotiated network protocol')
  await page.screenshot({ path: testInfo.outputPath('native-waterfall-help.png'), animations: 'disabled' })
})


test('frozen browser panel Help preserves stale-input and authorization boundaries', async ({ page }, testInfo) => {
  await page.getByRole('searchbox', { name: 'Search all help articles' }).fill('Frozen browser comparison inputs')
  await page.locator('.help-search-results').getByRole('link', { name: 'Browser intelligence workflows', exact: true }).click()
  const article = page.locator('.guide-markdown-article')
  await expect(article).toContainText('Query version 4 is explicit')
  await page.reload()
  await page.setViewportSize({ width: 760, height: 1000 })
  const heading = article.getByRole('heading', { name: 'Frozen browser comparison inputs', exact: true })
  await heading.scrollIntoViewIfNeeded()
  await expect(heading).toBeVisible()
  await expect(article).toContainText('native HAR downloads retain their separate authorization')
  expect(await page.locator('.help-workspace').evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  await page.screenshot({ path: testInfo.outputPath('browser-panel-pins-help.png'), animations: 'disabled' })
})

test('left sidebar index extends towards the visible window bottom or fits its content', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 1280, height: 800 })

  // 1. Long list (User Guide, 45 articles): extends down to near the edge of the visible window
  await page.goto('/#/docs?guide=user-guide')
  const sidebar = page.locator('.help-sidebar')
  await expect(sidebar).toBeVisible()
  await expect(sidebar.getByRole('link', { name: 'Concepts & navigation' })).toBeVisible()

  // At scroll 0:
  await expect.poll(async () => {
    const box = await sidebar.boundingBox()
    return box ? box.y + box.height : 0
  }).toBeGreaterThanOrEqual(760)
  let box = (await sidebar.boundingBox())!
  expect(box.y + box.height).toBeLessThanOrEqual(800)

  // When scrolling down:
  await page.locator('.main-content').evaluate(el => { el.scrollTop = 300 })
  await expect.poll(async () => {
    const b = await sidebar.boundingBox()
    return b ? b.y + b.height : 0
  }).toBeGreaterThanOrEqual(760)
  box = (await sidebar.boundingBox())!
  expect(box.y + box.height).toBeLessThanOrEqual(800)

  // 2. Short list (Getting Started, 2 articles): fits contents without stretching to the bottom
  await page.goto('/#/docs?guide=getting-started')
  await expect(sidebar.getByRole('link', { name: 'Your first test' })).toBeVisible()
  await expect.poll(async () => (await sidebar.boundingBox())?.height).toBeLessThan(350)
  const boxGS = (await sidebar.boundingBox())!
  expect(boxGS.y + boxGS.height).toBeLessThan(550)

  await page.screenshot({ path: testInfo.outputPath('help-sidebar-height.png'), animations: 'disabled' })
})

test('API reference examples match real handlers and remain usable at narrow widths', async ({ page }, testInfo) => {
  await page.goto('/#/swagger?search=/api/v1/scripts/validate')
  const card = page.locator('.swagger-operation-card')
  await expect(card).toHaveCount(1)
  await card.locator(':scope > summary').click()
  await expect(card.getByRole('heading', { name: 'Request body (required)', exact: true })).toBeVisible()
  const bodyCode = card.locator('pre[aria-label="Example body"]').first()
  const payload = JSON.parse(await bodyCode.innerText())
  const validation = await page.request.post('/api/v1/scripts/validate', { data: payload })
  expect(validation.ok()).toBe(true)
  expect((await validation.json()).valid).toBe(true)
  await card.locator('.swagger-curl > summary').click()
  const command = await card.locator('pre[aria-label="cURL request"]').innerText()
  expect(command).toContain('/api/v1/scripts/validate')
  expect(command).toContain('Authorization: Bearer <token>')
  expect(command).toContain('Validate an HTTP flow')
  // Exercise the clipboard action, independently of operating-system permission prompts.
  await page.evaluate(() => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async (text: string) => { document.documentElement.dataset.referenceCopy = text } } })
    document.execCommand = () => false
  })
  await card.getByRole('button', { name: 'Copy curl request', exact: true }).click()
  await expect(card.getByText('Copied to clipboard.', { exact: true })).toBeVisible()
  expect(await page.locator('html').getAttribute('data-reference-copy')).toBe(command)
  await card.getByRole('combobox', { name: 'Response status', exact: true }).selectOption('401')
  await expect(card).toContainText('Authentication required or invalid.')
  await expect(card.locator('pre[aria-label="Example body"]').last()).toContainText('"error"')
  await page.screenshot({ path: testInfo.outputPath('api-reference-desktop.png'), animations: 'disabled' })
  await page.setViewportSize({ width: 760, height: 1000 })
  await expect(card.getByRole('combobox', { name: 'Request format', exact: true })).toBeVisible()
  expect(await card.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  expect(await page.locator('.help-workspace').evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  const results = await page.evaluate(async () => {
    const axeWindow = window as typeof window & { axe: typeof axe }
    return axeWindow.axe.run(document.querySelector('.swagger-docs-page')!, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa'] } })
  })
  expect(results.violations.map(violation => ({ id: violation.id, nodes: violation.nodes.map(node => node.target) }))).toEqual([])
  await page.screenshot({ path: testInfo.outputPath('api-reference-narrow.png'), animations: 'disabled' })
  const downloadEvent = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Download OpenAPI', exact: true }).click()
  expect((await downloadEvent).suggestedFilename()).toBe('apexload-openapi.yaml')

  await page.goto('/#/swagger?search=handleListScripts')
  const listCard = page.locator('.swagger-operation-card')
  await listCard.locator(':scope > summary').click()
  await expect(listCard).toContainText('values above 1000 are capped')
  await expect(listCard).toContainText('No request body is declared')
  await expect(listCard.locator('pre[aria-label="Example body"]')).toContainText('"scripts": []')
})
