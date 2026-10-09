import { readFile } from 'node:fs/promises'
import type { APIRequestContext, Page } from '@playwright/test'
import { test, expect, seedScript, json, idOf, login, runStatus, signOut } from './support/workflow'
import { createWorkspace, scopeQuery, type Workspace } from './support/workspace'
import { resourcePageFixture } from '../src/components/browserResourcePage.testFixtures'
import type { BrowserResourcePage } from '../src/types/browserResourcePage'

const SYNTHETIC = 'browser_har_synthetic.har'

// A real one-second run produces a real archive, so the HAR list, the
// synthetic HAR and the HAR-inclusive archive come from the controller. Only
// the report and the retained resource page are bounded transport fixtures
// (as in the retained resource inspector workflow), so the inspector opens on
// any archive.
async function archivedRun(request: APIRequestContext, workspace: Workspace, targetURL: string, trackRun: (id: string) => void) {
  const script = await seedScript(request, workspace, `HAR_source_${Date.now()}`, targetURL)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: {
    project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId,
    name: 'Browser HAR downloads', duration: '1s', groups: [{ name: 'har', script_id: script, vusers: 1 }],
  } })))
  trackRun(id)
  await json(await request.post(`/api/v1/tests/${id}/start`))
  await expect.poll(() => runStatus(request, id), { timeout: 90_000 }).toBe('COMPLETE')
  await expect.poll(async () => (await request.get(`/api/v1/tests/${id}/archive/manifest`)).ok(), { timeout: 60_000 }).toBe(true)
  return id
}

async function openInspector(page: Page, id: string, workspace: Workspace) {
  await page.route(`**/api/v1/tests/${id}/archive/report*`, async route => {
    const response = await route.fetch()
    const body = await response.json()
    await route.fulfill({ response, json: { ...body, browser_resources: [] } })
  })
  await page.route(`**/api/v1/tests/${id}/archive/browser-resources*`, async route => {
    const value: BrowserResourcePage = resourcePageFixture()
    value.test_id = id; value.project_id = workspace.projectId
    await route.fulfill({ json: value })
  })
  await page.goto(`/#/report/${id}?section=diagnostics&${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByRole('tab', { name: 'Diagnostics', exact: true }).click()
  await page.getByText('Browser evidence', { exact: true }).click()
  await page.getByRole('button', { name: 'Inspect retained archive resources', exact: true }).click()
  return page.getByRole('region', { name: 'Browser HARs', exact: true })
}

test.afterEach(async ({ page }) => { await page.unrouteAll({ behavior: 'wait' }) })

test('an archived run leads from its retained resources to HAR and archive downloads', async ({ page, target, trackRun }) => {
  test.setTimeout(180_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const id = await archivedRun(request, workspace, target.url, trackRun)
  const list = await json(await request.get(`/api/v1/tests/${id}/archive/browser/har`))
  expect(list.can_download).toBe(true)
  expect((list.artifacts as Array<{ name: string; fidelity: string }>).map(item => [item.name, item.fidelity])).toContainEqual([SYNTHETIC, 'synthetic'])

  const panel = await openInspector(page, id, workspace)
  await expect(panel).toContainText(SYNTHETIC)
  const download = page.waitForEvent('download')
  await panel.getByRole('button', { name: 'Download synthetic HAR', exact: true }).click()
  const har = JSON.parse(await readFile((await (await download).path())!, 'utf8'))
  expect(har.log.version).toBe('1.2')
  expect(har.log._apexload.fidelity).toBe('synthetic')
  expect(har.log._apexload.body_policy).toBe('omitted')

  // A retained resource row offers the HAR holding it (no native HAR covers
  // the fixture's context, so the synthetic one).
  const inspector = page.getByRole('region', { name: 'Retained archive resource inspector', exact: true })
  await inspector.getByText('Inspect retained resource 1').click()
  const rowDownload = page.waitForEvent('download')
  await inspector.getByRole('button', { name: 'Download the synthetic HAR holding this request', exact: true }).click()
  expect((await rowDownload).suggestedFilename()).toContain(SYNTHETIC)

  const zip = await request.get(`/api/v1/tests/${id}/archive?include=har`)
  expect(zip.status()).toBe(200)
  expect(zip.headers()['content-type']).toBe('application/zip')
  expect((await request.get(`/api/v1/tests/${id}/archive/browser/har/${encodeURIComponent('apexload_browser_' + 'a'.repeat(32) + '_scrubbed.har')}`)).status()).toBe(404)
})

test('viewers list HARs but cannot download them, and other projects are denied', async ({ page, target, trackRun }) => {
  test.skip(process.env.APEXLOAD_E2E_PROFILE !== 'local-auth', 'requires the viewer persona')
  test.setTimeout(180_000)
  await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const id = await archivedRun(request, workspace, target.url, trackRun)
  const users = (await json(await request.get('/api/v1/auth/users'))).users as Array<{ id: string; subject: string }>
  const viewer = users.find(user => user.subject === 'local:user')!
  await signOut(page)
  await login(page, 'user')
  // Not bound to the run's project: denied outright.
  expect((await page.context().request.get(`/api/v1/tests/${id}/archive/browser/har`)).status()).toBe(403)
  await signOut(page)
  await login(page)
  await json(await page.context().request.post(`/api/v1/auth/users/${viewer.id}/bindings`, { data: { scope_type: 'domain', scope_id: workspace.domainId, role: 'viewer' } }))
  await signOut(page)
  await login(page, 'user')
  const viewerRequest = page.context().request
  const list = await json(await viewerRequest.get(`/api/v1/tests/${id}/archive/browser/har`))
  expect(list.can_download).toBe(false)
  expect((await viewerRequest.get(`/api/v1/tests/${id}/archive/browser/har/${SYNTHETIC}`)).status()).toBe(403)
  expect((await viewerRequest.get(`/api/v1/tests/${id}/archive?include=har`)).status()).toBe(403)
  expect((await viewerRequest.get(`/api/v1/tests/${id}/archive`)).status()).toBe(200)
  const panel = await openInspector(page, id, workspace)
  await expect(panel).toContainText('Downloading a HAR needs editor access')
  await expect(panel.getByRole('button', { name: 'Download synthetic HAR', exact: true })).toBeDisabled()
})
