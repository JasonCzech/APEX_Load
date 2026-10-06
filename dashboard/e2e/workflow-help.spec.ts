import axe from 'axe-core'
import type { BrowserContext } from '@playwright/test'
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
  const example = await page.locator('.help-code-example').filter({ has: page.locator('code[data-language="json"]') }).locator('code').textContent()
  // Validate the exact text shown to the reader against the real controller contract.
  const response = await page.request.post('/api/v1/scripts/validate', { data: JSON.parse(example!) })
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
  const examples = page.locator('.help-code-example code[data-language="json"]')
  const script = JSON.parse((await examples.first().textContent())!)
  const response = await page.request.post('/api/v1/scripts/validate', { data: { script } })
  expect(response.ok()).toBe(true)
  const validation = await response.json()
  expect(validation.valid, JSON.stringify(validation)).toBe(true)
  // Validate the documented control-flow fragment in its stated execution context.
  const loop = JSON.parse((await examples.last().textContent())!)
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
    { query: 'provisional history', id: 'browser-document-evidence', title: 'Browser document evidence', body: 'No-interaction INP is unavailable.' },
    { query: 'semantic SHA-256', id: 'telemetry-policy-receipts', title: 'Telemetry budget policy receipts', body: 'explicit zero disables a stage' },
    { query: 'sub-millisecond thresholds', id: 'authoring-scenario-fidelity', title: 'Scenario and queued-run authoring fidelity', body: 'A present empty section is a whole-section replacement.' },
  ]
  for (const guide of guides) {
    await page.getByRole('searchbox', { name: 'Search all help articles' }).fill(guide.query)
    await page.locator('.help-search-results').getByRole('link', { name: guide.title, exact: true }).click()
    await expect(page).toHaveURL(new RegExp(`article=${guide.id}`))
    await expect(page.getByRole('heading', { name: guide.title, exact: true })).toBeVisible()
    await expect(page.locator('.guide-markdown-article')).toBeVisible()
    await expect(page.locator('.guide-markdown-article')).toContainText(guide.body)
    await expect(page.locator('.guide-markdown-article')).toContainText('remain open')
    await page.reload()
    await expect(page.locator('.guide-markdown-article')).toContainText(guide.body)
    await page.setViewportSize({ width: 760, height: 900 })
    await expect(page.locator('.help-workspace')).toBeVisible()
    expect(await page.locator('.help-workspace').evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath(`${guide.id}-narrow.png`), animations: 'disabled' })
  }
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
  await expect(page.getByRole('heading', { name: 'Capabilities & limitations', exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Capabilities & limitations', exact: true })).toBeInViewport()
  for (const link of await page.getByRole('navigation', { name: 'Help areas' }).getByRole('link').all()) {
    const box = await link.boundingBox()
    expect(box!.x + box!.width).toBeLessThanOrEqual(800)
  }
  await page.getByText('Browse articles', { exact: true }).click()
  await expect(page.getByRole('navigation', { name: 'Help articles' })).toBeVisible()
  await page.getByRole('navigation', { name: 'Help articles' }).getByRole('link', { name: 'Capabilities & limitations', exact: true }).click()
  await page.getByText('Browse articles', { exact: true }).click()
  expect(await page.locator('.help-workspace').evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true)
  const violations = await page.evaluate(async () => (await (window as typeof window & { axe: typeof axe }).axe.run('.help-workspace, .help-primary-nav', { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa'] } })).violations)
  expect(violations).toEqual([])
  await page.getByRole('heading', { name: 'Capabilities & limitations', exact: true }).scrollIntoViewIfNeeded()
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
    const violations = await page.evaluate(async () => (await (window as typeof window & { axe: typeof axe }).axe.run('.help-workspace, .help-primary-nav', { runOnly: ['color-contrast'] })).violations)
    expect(violations, theme).toEqual([])
  }
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
