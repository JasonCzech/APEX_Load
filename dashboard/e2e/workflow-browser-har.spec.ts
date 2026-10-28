import { readFile } from 'node:fs/promises'
import { createServer } from 'node:http'
import type { AddressInfo } from 'node:net'
import type { APIRequestContext, Locator, Page } from '@playwright/test'
import { test, expect, seedScript, json, idOf, login, runStatus, signOut, expectEditorSaved, mutation } from './support/workflow'
import { createWorkspace, scopeQuery, type Workspace } from './support/workspace'
import { resourcePageFixture } from '../src/components/browserResourcePage.testFixtures'
import type { BrowserResourcePage } from '../src/types/browserResourcePage'

const SYNTHETIC = 'browser_har_synthetic.har'

async function startManagedChainTarget() {
  const server = createServer((req, res) => {
    const url = new URL(req.url || '/', 'http://127.0.0.1')
    if (url.pathname === '/clicked') {
      res.setHeader('Content-Type', 'application/json')
      res.end('{"clicked":true}')
      return
    }
    res.setHeader('Content-Type', 'text/html; charset=utf-8')
    res.end('<!doctype html><html><head><title>Managed Chain Fixture</title></head><body>'
      + '<button id="go" type="button">Go</button><div id="status">idle</div>'
      + '<script>document.getElementById("go").addEventListener("click",function(){'
      + 'fetch("/clicked?token=secret-query-token").then(function(r){return r.text();}).then(function(){setTimeout(function(){document.getElementById("status").textContent="clicked";},100);});'
      + '});</script></body></html>')
  })
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  const origin = `http://127.0.0.1:${(server.address() as AddressInfo).port}`
  return {
    origin,
    close: async () => {
      server.closeAllConnections()
      await new Promise<void>((resolve, reject) => server.close(err => err ? reject(err) : resolve()))
    },
  }
}

async function openBrowserEmulationSettings(page: Page): Promise<Locator> {
  await page.getByRole('tab', { name: 'Runtime Settings', exact: true }).click()
  const runtime = page.getByRole('region', { name: 'Runtime Settings', exact: true })
  await runtime.getByRole('navigation', { name: 'Runtime setting categories' }).getByRole('tab', { name: 'HTTP Transport', exact: true }).click()
  await runtime.getByRole('tab', { name: /^Browser Emulation/ }).click()
  return runtime
}

async function runManagedChainUI(page: Page, trackRun: (id: string) => void, engine: 'chromium' | 'webkit') {
  test.setTimeout(240_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const target = await startManagedChainTarget()
  try {
    const scriptId = idOf(await json(await request.post('/api/v1/scripts', {
      data: {
        project_id: workspace.projectId,
        script: {
          name: `Managed_Chain_${engine}_${Date.now()}`,
          version: '1.0.0',
          protocol: 'browser',
          config: { browser: { engine: 'chromium', headless: true } },
          actions: [
            { name: 'Open page', type: 'browser_navigate', target: `${target.origin}/page` },
            { name: 'Click action', type: 'browser_click', selector: '#go' },
            { name: 'Wait clicked', type: 'browser_wait', selector: '#status', wait_for: { type: 'text', value: 'clicked', timeout_ms: 5000 } },
          ],
        },
      },
    })))

    // 1. Authoring in the UI: configure Browser Engine, HAR capture = always, document metrics & interaction evidence.
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.goto(`/#/scripts?mode=edit&scriptId=${scriptId}&${scopeQuery(workspace)}`)
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
    await expect(page.getByRole('group', { name: 'Script workspace' })).toBeVisible()
    const runtime = await openBrowserEmulationSettings(page)
    await runtime.locator('label').filter({ has: page.locator('span', { hasText: /^Browser Engine$/ }) }).locator('select').selectOption(engine)
    await runtime.locator('label').filter({ has: page.locator('span', { hasText: /^HAR capture$/ }) }).locator('select').selectOption('always')
    await runtime.getByLabel('Capture page timings and Web Vitals').check()
    await runtime.getByLabel('Capture interaction evidence').check()
    await page.getByRole('button', { name: 'Save script', exact: true }).click()
    await expectEditorSaved(page)

    // 2. Replaying in the UI: run native browser replay and download the native replay HAR + evidence.
    await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
    const nativeReplay = page.getByRole('region', { name: 'Native browser replay', exact: true })
    const startReplay = mutation(page, 'POST', `/api/v1/scripts/${scriptId}/browser-replay/sessions`)
    await nativeReplay.getByRole('button', { name: 'Run native browser replay', exact: true }).click()
    const replaySessionId = String((await json(await startReplay)).replay_session_id)
    await expect(nativeReplay.getByTestId('replay-progress')).toContainText('Verified ✓', { timeout: 60_000 })

    const replayHarResponse = page.waitForResponse(res => res.url().includes(`/api/v1/browser-replay/sessions/${replaySessionId}/har`))
    const replayHarDownload = page.waitForEvent('download')
    await nativeReplay.getByRole('button', { name: 'Download replay HAR', exact: true }).click()
    expect((await replayHarResponse).headers()['x-apexload-har-fidelity']).toBe('native')
    const replayHarRaw = await readFile((await (await replayHarDownload).path())!, 'utf8')
    expect(replayHarRaw).not.toContain('secret-query-token')
    const replayHar = JSON.parse(replayHarRaw)
    expect(replayHar.log.version).toBe('1.2')
    expect(replayHar.log._apexload).toBeUndefined()
    expect(replayHar.log.entries.map((entry: { request: { url: string } }) => entry.request.url)).toContain(`${target.origin}/clicked?token=[REDACTED]`)

    const replayEvidenceDownload = page.waitForEvent('download')
    await nativeReplay.getByRole('button', { name: 'Download replay evidence', exact: true }).click()
    expect((await replayEvidenceDownload).suggestedFilename()).toContain(replaySessionId)

    // 3. Launching a 1-iteration managed run from the UI.
    const scenarioId = idOf(await json(await request.post('/api/v1/scenarios', {
      data: {
        project_id: workspace.projectId,
        application_id: workspace.applicationId,
        name: `Managed ${engine} chain scenario`,
        termination_mode: 'iterations',
        scenario_mode: 'vuser_driven',
        groups: [{ name: `browser-${engine}`, script_id: scriptId, vusers: 1, iterations: 1, ramp_up: '0s' }],
      },
    })))
    await page.goto(`/#/new-test?scenarioId=${scenarioId}&${scopeQuery(workspace)}`)
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
    await page.getByRole('combobox', { name: 'Release', exact: true }).selectOption(workspace.releaseId)
    const startNow = page.getByRole('button', { name: 'Start now' })
    await expect(startNow).toBeEnabled({ timeout: 30_000 })
    const createdRun = mutation(page, 'POST', '/api/v1/tests')
    await startNow.click()
    const runId = idOf(await json(await createdRun))
    trackRun(runId)
    await expect.poll(() => runStatus(request, runId), { timeout: 90_000 }).toBe('COMPLETE')
    await expect.poll(async () => (await request.get(`/api/v1/tests/${runId}/archive/manifest`)).ok(), { timeout: 60_000 }).toBe(true)

    // 4. Inspecting the real archived run in the UI without any page.route stubs.
    await page.goto(`/#/report/${runId}?section=diagnostics&${scopeQuery(workspace)}`)
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
    await page.getByRole('tab', { name: 'Diagnostics', exact: true }).click()
    await page.getByText('Browser evidence', { exact: true }).click()
    await page.getByRole('button', { name: 'Inspect retained archive resources', exact: true }).click()

    const harPanel = page.getByRole('region', { name: 'Browser HARs', exact: true })
    await expect(harPanel.getByRole('button', { name: 'Download native HAR', exact: true })).toBeVisible({ timeout: 30_000 })
    await expect(harPanel).toContainText('native')
    const nativeHarDownload = page.waitForEvent('download')
    await harPanel.getByRole('button', { name: 'Download native HAR', exact: true }).click()
    const archiveHarRaw = await readFile((await (await nativeHarDownload).path())!, 'utf8')
    expect(archiveHarRaw).not.toContain('secret-query-token')
    const archiveHar = JSON.parse(archiveHarRaw)
    expect(archiveHar.log.version).toBe('1.2')
    expect(archiveHar.log._apexload).toBeUndefined()
    expect(archiveHar.log.entries.map((entry: { request: { url: string } }) => entry.request.url)).toContain(`${target.origin}/clicked?token=[REDACTED]`)

    const archiveZipDownload = page.waitForEvent('download')
    await harPanel.getByRole('button', { name: 'Download archive with native HARs', exact: true }).click()
    expect((await archiveZipDownload).suggestedFilename()).toContain(runId)

    const inspector = page.getByRole('region', { name: 'Retained archive resource inspector', exact: true })
    const resourceTable = inspector.getByRole('region', { name: 'Retained archive resources table', exact: true })
    await expect(resourceTable).toContainText(`${target.origin}/clicked?token=[REDACTED]`)
    await expect(resourceTable).not.toContainText('secret-query-token')
    const clickedRow = resourceTable.getByRole('row').filter({ hasText: `${target.origin}/clicked?token=[REDACTED]` }).first()
    await clickedRow.locator('summary').click()
    await clickedRow.getByRole('button', { name: "Show this request's HAR entry", exact: true }).click()
    await expect(clickedRow).toContainText("This request's browser context recorded a native HAR")
    const entryHarDownload = page.waitForEvent('download')
    await clickedRow.getByRole('button', { name: 'Download that HAR', exact: true }).click()
    const entryHarRaw = await readFile((await (await entryHarDownload).path())!, 'utf8')
    expect(entryHarRaw).not.toContain('secret-query-token')
  } finally {
    await target.close()
  }
}

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

test('UI-driven Chromium managed chain authors, replays, runs and downloads native HARs without stubs', async ({ page, trackRun }) => {
  await runManagedChainUI(page, trackRun, 'chromium')
})

test('UI-driven WebKit managed chain authors, replays, runs and downloads native HARs without stubs', async ({ page, trackRun }) => {
  await runManagedChainUI(page, trackRun, 'webkit')
})

test('viewers list HARs but cannot download them, and other projects are denied', async ({ page, target, trackRun }) => {
  test.skip(process.env.APEXLOAD_E2E_PROFILE !== 'local-auth', 'requires the viewer persona')
  test.setTimeout(240_000)
  await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const id = await archivedRun(request, workspace, target.url, trackRun)
  const nativeScriptId = idOf(await json(await request.post('/api/v1/scripts', {
    data: {
      project_id: workspace.projectId,
      script: {
        name: `Native_HAR_RBAC_${Date.now()}`,
        version: '1.0.0',
        protocol: 'browser',
        config: { browser: { engine: 'chromium', headless: true, har_capture: 'always' } },
        actions: [{ name: 'Open target', type: 'browser_navigate', target: target.url }],
      },
    },
  })))
  const replayStart = await json(await request.post(`/api/v1/scripts/${nativeScriptId}/browser-replay/sessions`, {
    data: { project_id: workspace.projectId, record_har: true },
  }))
  const replayId = String(replayStart.replay_session_id)
  await expect.poll(async () => {
    const res = await request.get(`/api/v1/browser-replay/sessions/${replayId}?project_id=${workspace.projectId}`)
    if (!res.ok()) return ''
    const body = await res.json() as { session?: { status?: string } }
    return String(body.session?.status || '')
  }, { timeout: 60_000 }).toBe('completed')
  const nativeRunId = idOf(await json(await request.post('/api/v1/tests', {
    data: {
      project_id: workspace.projectId,
      application_id: workspace.applicationId,
      release_id: workspace.releaseId,
      name: 'Native Browser HAR RBAC',
      termination_mode: 'iterations',
      groups: [{ name: 'browser', script_id: nativeScriptId, vusers: 1, iterations: 1 }],
    },
  })))
  trackRun(nativeRunId)
  await json(await request.post(`/api/v1/tests/${nativeRunId}/start`))
  await expect.poll(() => runStatus(request, nativeRunId), { timeout: 90_000 }).toBe('COMPLETE')
  await expect.poll(async () => (await request.get(`/api/v1/tests/${nativeRunId}/archive/manifest`)).ok(), { timeout: 60_000 }).toBe(true)
  const adminNativeList = await json(await request.get(`/api/v1/tests/${nativeRunId}/archive/browser/har`))
  const nativeArtifact = (adminNativeList.artifacts as Array<{ name: string; fidelity: string }>).find(item => item.fidelity === 'native')!
  expect(nativeArtifact).toBeTruthy()

  const users = (await json(await request.get('/api/v1/auth/users'))).users as Array<{ id: string; subject: string }>
  const viewer = users.find(user => user.subject === 'local:user')!
  await signOut(page)
  await login(page, 'user')
  // Not bound to the run's project: denied outright.
  expect((await page.context().request.get(`/api/v1/tests/${id}/archive/browser/har`)).status()).toBe(403)
  expect((await page.context().request.get(`/api/v1/tests/${nativeRunId}/archive/browser/har`)).status()).toBe(403)
  expect((await page.context().request.get(`/api/v1/browser-replay/sessions/${replayId}/har`)).status()).toBe(403)
  expect((await page.context().request.get(`/api/v1/browser-replay/sessions/${replayId}/evidence`)).status()).toBe(403)
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
  expect((await viewerRequest.get(`/api/v1/tests/${nativeRunId}/archive/browser/har/${encodeURIComponent(nativeArtifact.name)}`)).status()).toBe(403)
  expect((await viewerRequest.get(`/api/v1/tests/${nativeRunId}/archive?include=har`)).status()).toBe(403)
  expect((await viewerRequest.get(`/api/v1/browser-replay/sessions/${replayId}/har`)).status()).toBe(403)
  expect((await viewerRequest.get(`/api/v1/browser-replay/sessions/${replayId}/evidence`)).status()).toBe(403)
  const panel = await openInspector(page, id, workspace)
  await expect(panel).toContainText('Downloading a HAR needs editor access')
  await expect(panel.getByRole('button', { name: 'Download synthetic HAR', exact: true })).toBeDisabled()
  await page.unrouteAll({ behavior: 'wait' })
  await page.goto(`/#/report/${nativeRunId}?section=diagnostics&${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByRole('tab', { name: 'Diagnostics', exact: true }).click()
  await page.getByText('Browser evidence', { exact: true }).click()
  await page.getByRole('button', { name: 'Inspect retained archive resources', exact: true }).click()
  const nativePanel = page.getByRole('region', { name: 'Browser HARs', exact: true })
  await expect(nativePanel).toContainText('Downloading a HAR needs editor access')
  await expect(nativePanel.getByRole('button', { name: 'Download native HAR', exact: true })).toBeDisabled()
})
