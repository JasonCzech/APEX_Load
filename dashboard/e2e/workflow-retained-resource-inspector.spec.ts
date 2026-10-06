import type { Page } from '@playwright/test'
import { test, expect, seedScript, json, idOf, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'
import { resourcePageFixture } from '../src/components/browserResourcePage.testFixtures'
import type { BrowserResourcePage, BrowserResourcePageItem } from '../src/types/browserResourcePage'

// These bounded transport fixtures qualify the real dashboard/client flow.
// Native acquisition and opened-object archive integrity are separate Go/runtime gates.
async function fixture(page: Page, trackRun: (id: string) => void) {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const script = await seedScript(request, workspace)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: {
    project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId,
    name: 'Retained resource page inspection', duration: '1s', groups: [{ name: 'resources', script_id: script, vusers: 1 }],
  } })))
  trackRun(id)
  await page.route(`**/api/v1/tests/${id}`, async route => {
    if (route.request().method() !== 'GET') return route.continue()
    const response = await route.fetch()
    return route.fulfill({ response, json: { ...await response.json(), status: 'COMPLETE',
      archive: { test_id: id, transaction_count: 2, created_at: '2026-10-05T12:00:00Z' },
      started_at: '2026-10-05T12:00:00Z', ended_at: '2026-10-05T12:00:01Z' } })
  })
  await page.route(`**/api/v1/tests/${id}/archive/report*`, route => route.fulfill({ json: {
    manifest: { test_id: id, test_name: 'Retained resource page inspection', status: 'COMPLETE',
      created_at: '2026-10-05T12:00:01Z', transaction_count: 2, transactions_file: 'transactions.jsonl',
      provenance: { project_id: workspace.projectId, started_at: '2026-10-05T12:00:00Z', duration: '1s', total_vusers: 1 } },
    overview: { transactions: 2 }, by_action: {}, browser_resources: [],
  } }))
  return { id, workspace }
}

function observations(): BrowserResourcePageItem[] {
  const base = resourcePageFixture().items[0]
  return Array.from({ length: 80 }, (_, index) => ({ ...structuredClone(base),
    locator: { transaction_ordinal: 1, resource_ordinal: index + 1, branch: 'network_entries' },
    url: `https://resource.example.test/retained-${index + 1}.js`, group: 'browser-group',
    status_code: index === 0 ? 0 : 200, duration_ms: index, bytes_received: index,
    browser_evidence: { ...structuredClone(base.browser_evidence!), request_id: `${base.browser_evidence!.source_generation}-request-${index + 1}` },
  }))
}

function pageFor(id: string, projectID: string, rows: BrowserResourcePageItem[], matched: number, next?: string): BrowserResourcePage {
  const value = resourcePageFixture()
  value.test_id = id; value.project_id = projectID; value.items = rows
  value.population.matched_retained_observations = matched
  delete value.next_cursor
  if (next) value.next_cursor = next
  return value
}

test.afterEach(async ({ page }) => { await page.unrouteAll({ behavior: 'wait' }) })

test('retained resource inspector paginates beyond report previews and preserves native zero, false and independent clocks', async ({ page, trackRun }) => {
  const { id, workspace } = await fixture(page, trackRun)
  const rows = observations()
  const cursor = `page-two.${'A'.repeat(43)}`
  const queries: Array<Record<string, string>> = []
  await page.route(`**/api/v1/tests/${id}/archive/browser-resources*`, async route => {
    const q = new URL(route.request().url()).searchParams
    queries.push(Object.fromEntries(q))
    expect(q.get('project_id')).toBe(workspace.projectId)
    expect(route.request().method()).toBe('GET')
    const matched = q.get('action_name') === 'Absent action' ? [] : rows
    const limit = Number(q.get('limit') || 50)
    const offset = q.has('cursor') ? 50 : 0
    if (q.has('cursor')) expect(q.get('cursor')).toBe(cursor)
    await route.fulfill({ json: pageFor(id, workspace.projectId, matched.slice(offset, offset + limit), matched.length,
      offset + limit < matched.length ? cursor : undefined) })
  })
  await page.goto(`/#/report/${id}?section=diagnostics&${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByRole('tab', { name: 'Diagnostics', exact: true }).click()
  await page.getByText('Browser evidence', { exact: true }).click()
  await page.getByRole('button', { name: 'Inspect retained archive resources', exact: true }).click()
  const inspector = page.getByRole('region', { name: 'Retained archive resource inspector', exact: true })
  await expect(inspector).toContainText('Page 1: 50 retained observations')
  await expect(inspector).toContainText('80 retained entry observations')
  await expect(inspector).toContainText('0 ms / 0 B')
  await expect(inspector).toContainText('capture coverage are unavailable')
  const table = inspector.getByRole('region', { name: 'Retained archive resources table', exact: true })
  await expect(table.locator('tbody tr')).toHaveCount(50)
  await inspector.getByRole('button', { name: 'Next resource page', exact: true }).click()
  await expect(inspector).toContainText('Page 2: 30 retained observations')
  await expect(table.locator('tbody tr')).toHaveCount(30)
  await expect(table).toContainText('retained-80.js')
  await expect(inspector.getByRole('button', { name: 'Next resource page', exact: true })).toBeDisabled()
  await inspector.getByRole('button', { name: 'First page / retry', exact: true }).click()
  await expect(inspector).toContainText('Page 1: 50 retained observations')
  await inspector.getByLabel('Capturing action', { exact: true }).fill('Absent action')
  await inspector.getByRole('button', { name: 'Apply resource filters', exact: true }).click()
  await expect(inspector).toContainText('0 match these filters out of 80')
  await expect(inspector).toContainText('No retained observations match these filters')
  expect(queries.at(-1)).not.toHaveProperty('cursor')
  await inspector.getByLabel('Capturing action', { exact: true }).fill('')
  await inspector.getByLabel('Resources per page', { exact: true }).selectOption('25')
  await inspector.getByRole('button', { name: 'Apply resource filters', exact: true }).click()
  await expect(table.locator('tbody tr')).toHaveCount(25)
  await page.setViewportSize({ width: 760, height: 1600 })
  await inspector.getByRole('heading', { name: 'Retained resource observations', exact: true }).scrollIntoViewIfNeeded()
  const geometry = () => inspector.evaluate(node => {
    const box = node.getBoundingClientRect()
    const heading = node.querySelector('h3')!
    const title = heading.getBoundingClientRect()
    const owner = node.closest('.main-content')
    if (!owner) throw new Error('Resource inspector has no main scroll owner')
    const visible = owner.getBoundingClientRect()
    const topbar = document.querySelector('.main-topbar')?.getBoundingClientRect().bottom ?? 0
    const hit = document.elementFromPoint(title.left + title.width / 2, title.top + title.height / 2)
    return { left: box.left, right: box.right, width: window.innerWidth, height: window.innerHeight,
      ownerLeft: visible.left, ownerRight: visible.right, scrollWidth: node.scrollWidth, clientWidth: node.clientWidth,
      headingTop: title.top, headingBottom: title.bottom, topbar, headingHit: hit === heading || heading.contains(hit) }
  })
  const assertGeometry = (value: Awaited<ReturnType<typeof geometry>>) => {
    expect(value.left).toBeGreaterThanOrEqual(Math.max(0, value.ownerLeft))
    expect(value.right).toBeLessThanOrEqual(Math.min(value.width, value.ownerRight))
    expect(value.scrollWidth).toBeLessThanOrEqual(value.clientWidth + 1)
    expect(value.headingTop).toBeGreaterThanOrEqual(value.topbar)
    expect(value.headingBottom).toBeLessThanOrEqual(value.height)
    expect(value.headingHit).toBe(true)
  }
  const beforeCapture = await geometry()
  await test.info().attach('resource-inspector-overview-before', { body: JSON.stringify(beforeCapture), contentType: 'application/json' })
  assertGeometry(beforeCapture)
  await page.screenshot({ path: test.info().outputPath('retained-resource-inspector-overview.png'), animations: 'disabled' })
  const afterCapture = await geometry()
  await test.info().attach('resource-inspector-overview-after', { body: JSON.stringify(afterCapture), contentType: 'application/json' })
  assertGeometry(afterCapture)
  await table.focus()
  const before = await table.evaluate(node => ({ left: node.scrollLeft, mainLeft: node.closest('.main-content')?.scrollLeft, width: node.clientWidth, scrollWidth: node.scrollWidth }))
  expect(before.scrollWidth).toBeGreaterThan(before.width)
  await page.keyboard.press('ArrowRight')
  await expect.poll(() => table.evaluate(node => node.scrollLeft)).toBeGreaterThan(before.left)
  const after = await table.evaluate(node => ({ left: node.scrollLeft, mainLeft: node.closest('.main-content')?.scrollLeft }))
  expect(after.mainLeft).toBe(before.mainLeft)
  await test.info().attach('resource-inspector-keyboard-scroll', { body: JSON.stringify({ before, after }), contentType: 'application/json' })
  await table.getByText('Inspect retained resource 1', { exact: true }).click()
  const native = table.locator('.browser-native-request-evidence').first()
  await expect(native).toContainText('false (observed)')
  await expect(native).toContainText('2026-10-05T12:00:00.123456789Z')
  await expect(native).toContainText('0 ms · clock unverified')
  await expect(native).toContainText('may overlap')
  await expect(native).toContainText('0 B')
  await expect(native.locator('.measured-point')).toHaveCount(1)
  await native.scrollIntoViewIfNeeded()
  const point = await native.locator('.measured-point').evaluate(node => {
    const box = node.getBoundingClientRect(); const track = node.parentElement!.getBoundingClientRect()
    const style = getComputedStyle(node)
    const owner = node.closest('.main-content')!.getBoundingClientRect()
    const topbar = document.querySelector('.main-topbar')?.getBoundingClientRect().bottom ?? 0
    const hit = document.elementFromPoint(box.left + box.width / 2, box.top + box.height / 2)
    return { left: box.left, right: box.right, top: box.top, bottom: box.bottom, width: box.width, height: box.height,
      trackLeft: track.left, trackRight: track.right, ownerLeft: owner.left, ownerRight: owner.right, topbar,
      viewportWidth: window.innerWidth, viewportHeight: window.innerHeight, hitOwned: hit === node || node.parentElement!.contains(hit),
      display: style.display, visibility: style.visibility, opacity: style.opacity, borderWidth: style.borderLeftWidth }
  })
  await test.info().attach('resource-inspector-measured-zero-point', { body: JSON.stringify(point), contentType: 'application/json' })
  expect(point.width).toBeGreaterThan(0); expect(point.height).toBeGreaterThan(0)
  expect(point.left).toBeGreaterThanOrEqual(point.trackLeft); expect(point.right).toBeLessThanOrEqual(point.trackRight + 1)
  expect(point.display).not.toBe('none'); expect(point.visibility).toBe('visible'); expect(Number(point.opacity)).toBeGreaterThan(0)
  expect(parseFloat(point.borderWidth)).toBeGreaterThan(0)
  expect(point.left).toBeGreaterThanOrEqual(Math.max(0, point.ownerLeft))
  expect(point.right).toBeLessThanOrEqual(Math.min(point.viewportWidth, point.ownerRight))
  expect(point.top).toBeGreaterThanOrEqual(point.topbar); expect(point.bottom).toBeLessThanOrEqual(point.viewportHeight)
  expect(point.hitOwned).toBe(true)
  await expect(native.locator('.browser-resource-phases li').filter({ hasText: 'request wait' }).locator('.browser-resource-phase-bar')).toHaveCount(0)
  await native.screenshot({ path: test.info().outputPath('retained-resource-native-detail.png') })
  await test.info().attach('resource-inspector-transport-queries', { body: JSON.stringify(queries), contentType: 'application/json' })
})

test('retained resource inspector refuses a mismatched page and retries without publishing unavailable evidence as zero', async ({ page, trackRun }) => {
  const { id, workspace } = await fixture(page, trackRun)
  let state: 'foreign' | 'unavailable' | 'zero' = 'foreign'
  await page.route(`**/api/v1/tests/${id}/archive/browser-resources*`, async route => {
    const body = pageFor(id, workspace.projectId, [], 0)
    if (state === 'foreign') body.test_id = 'foreign-private-run'
    if (state === 'unavailable') body.population = { availability: 'unavailable', transaction_records: 2, retained_entry_observations: null, matched_retained_observations: null }
    body.source_integrity = 'legacy_scan_fingerprint'
    await route.fulfill({ json: body })
  })
  await page.goto(`/#/report/${id}?section=diagnostics&${scopeQuery(workspace)}`)
  // Login initializes workspace context before this externally seeded project exists.
  // A fresh application read admits the actual project instead of a stale list.
  await page.reload()
  await page.getByRole('tab', { name: 'Diagnostics', exact: true }).click()
  await page.getByText('Browser evidence', { exact: true }).click()
  await page.getByRole('button', { name: 'Inspect retained archive resources', exact: true }).click()
  const inspector = page.getByRole('region', { name: 'Retained archive resource inspector', exact: true })
  await expect(inspector.getByRole('alert')).toContainText('Unable to load')
  await expect(inspector).not.toContainText('foreign-private-run')
  await expect(inspector.getByRole('button', { name: 'Next resource page', exact: true })).toBeDisabled()
  state = 'unavailable'
  await inspector.getByRole('button', { name: 'First page / retry', exact: true }).click()
  await expect(inspector).toContainText('The archive resource projection is unavailable')
  await expect(inspector).not.toContainText('0 retained observations')
  await expect(inspector).not.toContainText('0 retained entry observations')
  await expect(inspector).toContainText('producer integrity is unverified')
  state = 'zero'
  await inspector.getByRole('button', { name: 'First page / retry', exact: true }).click()
  await expect(inspector).toContainText('0 match these filters out of 80')
  await expect(inspector).toContainText('No retained observations match these filters')
  await expect(inspector).not.toContainText('Healthy')
})
