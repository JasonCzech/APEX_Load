import { test, expect, login, seedScript, seedScenario } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

function gate() {
  let release!: () => void
  const promise = new Promise<void>(resolve => { release = resolve })
  return { promise, release }
}

test('recording selection and pending request saves stay with their original session', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  const now = new Date().toISOString()
  const recorded = (name: string) => ({ seq: 1, timestamp: now, method: 'GET', url: `https://example.test/${name}`, path: `/${name}`, host: 'example.test', status_code: 200, response_time_ms: 1, content_type: 'text/plain', body: '', think_time_ms: 0, is_static: false, step_name: name })
  const a = { id: 'ownership-a', name: 'Recording A', status: 'stopped', started_at: now, request_count: 1, filters: { exclude_static: true } }
  const b = { ...a, id: 'ownership-b', name: 'Recording B', requests: [recorded('B')] }
  const detail = gate()
  const save = gate()
  let detailStarted = false
  let saveStarted = false
  await page.route('**/api/v1/recorder**', async route => {
    const path = new URL(route.request().url()).pathname
    if (path === '/api/v1/recorder') return route.fulfill({ json: { sessions: [a, b], count: 2 } })
    if (path === `/api/v1/recorder/${a.id}`) {
      detailStarted = true
      await detail.promise
      return route.fulfill({ json: { ...a, requests: [recorded('A')] } })
    }
    if (path === `/api/v1/recorder/${a.id}/requests/1`) {
      saveStarted = true
      await save.promise
      return route.fulfill({ json: { ...recorded('A'), ...route.request().postDataJSON() } })
    }
    return route.fallback()
  })
  try {
    await page.goto(`/#/scripts?mode=record&method=proxy&${scopeQuery(workspace)}`)
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
    await page.getByRole('button', { name: 'Select recording session Recording A', exact: true }).click()
    await expect.poll(() => detailStarted).toBe(true)
    await page.getByRole('button', { name: 'Select recording session Recording B', exact: true }).click()
    const detailResponse = page.waitForResponse(response => new URL(response.url()).pathname === `/api/v1/recorder/${a.id}`)
    detail.release()
    await (await detailResponse).finished()
    // Let the response's state update render before checking that the older
    // selection was rejected; B was already visible before the response.
    await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
    await expect(page.getByRole('heading', { name: /^Request detail: Recording B\b/ })).toBeVisible()
    await page.getByRole('button', { name: 'Select recording session Recording A', exact: true }).click()
    await page.getByRole('button', { name: 'Select request 1', exact: true }).click()
    await page.getByLabel('Step name', { exact: true }).fill('A saved')
    await page.getByRole('button', { name: 'Save request', exact: true }).click()
    await expect.poll(() => saveStarted).toBe(true)
    await expect(page.getByRole('button', { name: 'Save request', exact: true })).toBeDisabled()
    await page.getByLabel('Step name', { exact: true }).fill('A newer unsaved edit')
    await page.getByRole('button', { name: 'Select recording session Recording B', exact: true }).click()
    await page.getByRole('button', { name: 'Select request 1', exact: true }).click()
    const savedResponse = page.waitForResponse(response => response.request().method() === 'PATCH')
    save.release()
    await savedResponse
    await expect(page.getByText('Request #1 updated', { exact: true })).toBeVisible()
    await expect(page.getByLabel('Step name', { exact: true })).toHaveValue('B')
  } finally {
    detail.release()
    save.release()
  }
})

test('scenario history retries failures and refreshes successful snapshots', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  const name = `History recovery ${Date.now()}`
  const scriptId = await seedScript(page.context().request, workspace, `${name} script`)
  const scenarioId = await seedScenario(page.context().request, workspace, scriptId, name)
  let reads = 0
  await page.route(`**/api/v1/scenarios/${scenarioId}/results**`, route => {
    reads++
    return reads === 1
      ? route.fulfill({ status: 503, json: { error: 'History temporarily unavailable' } })
      : route.fulfill({ json: { tests: reads === 2 ? [] : [{ id: 'recovered-history', name: reads === 3 ? 'Recovered history run' : 'Newest history run', status: 'COMPLETE', duration: '1m', created_at: new Date().toISOString(), groups: [], total_vusers: 1 }], count: reads === 2 ? 0 : 1 } })
  })
  await page.goto(`/#/tests?view=library&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  const row = page.locator('.scenario-library-table').getByRole('row').filter({ hasText: name })
  await row.getByRole('button', { name: /^More actions for/ }).click()
  await row.getByRole('button', { name: 'View runs', exact: true }).click()
  await expect(page.getByRole('alert').filter({ hasText: 'Could not load scenario runs' })).toBeVisible()
  await expect(page.getByText('No runs for this scenario yet.', { exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Retry loading runs', exact: true }).click()
  await expect(page.getByText('No runs for this scenario yet.', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Refresh runs', exact: true }).click()
  await expect(page.getByText('Recovered history run', { exact: true })).toBeVisible()
  await row.getByRole('button', { name: /^More actions for/ }).click()
  await row.getByRole('button', { name: 'Hide runs', exact: true }).click()
  await row.getByRole('button', { name: /^More actions for/ }).click()
  await row.getByRole('button', { name: 'View runs', exact: true }).click()
  await expect(page.getByText('Newest history run', { exact: true })).toBeVisible()
  await expect(page.getByText('Recovered history run', { exact: true })).toHaveCount(0)
  expect(reads).toBe(4)
})

// The agent is a fixture, but attestation, durable receipt recovery, and the
// script library are real controller operations in both authentication profiles.
test('device capture retries a committed response loss with the exact attested request', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  const name = `Device receipt recovery ${Date.now()}`
  const now = new Date().toISOString()
  const agentOrigin = 'http://127.0.0.1:27991'
  await page.addInitScript(({ origin, pairedAt }) => {
    localStorage.setItem(`apexload.browserRecorderBridge.${location.origin}`, JSON.stringify({ origin, token: 'device-fixture-token', session_id: 'device-fixture-session', paired_at: pairedAt }))
  }, { origin: agentOrigin, pairedAt: now })
  const script = { name, version: '1', protocol: 'http', project_id: workspace.projectId, config: { base_url: 'https://example.test' }, actions: [{ name: 'request', type: 'request', method: 'GET', target: '/private', header_list: [{ name: 'Authorization', value: 'Bearer device-regression-fixture-9a42' }] }] }
  await page.route(`${agentOrigin}/**`, async route => {
    const path = new URL(route.request().url()).pathname
    const headers = { 'access-control-allow-origin': '*', 'access-control-allow-headers': '*', 'access-control-allow-methods': '*' }
    if (route.request().method() === 'OPTIONS') return route.fulfill({ status: 204, headers })
    const response = path.endsWith('/device-capture')
      ? { interfaces: [{ name: 'fixture', address: '192.168.1.20' }], capture: { session_id: 'device-fixture', name, status: 'stopped', host: '192.168.1.20', port: 8888, username: 'apexload', password: '', setup_url: '', ca_url: '', started_at: now, request_count: 1 } }
      : path.endsWith('/convert/preview') ? { scripts: [script], warnings: [] }
        : path.endsWith('/bridge/status') ? { bridge: { paired: true, api_origin: agentOrigin, availability: { available: true }, active: null } }
          : { sessions: [], devices: [], states: [], count: 0, status: 'healthy' }
    await route.fulfill({ headers, json: response })
  })
  let attestations = 0
  page.on('request', request => { if (new URL(request.url()).pathname === '/api/v1/scripts/import/browser-recorder/attest') attestations++ })
  const payloads: string[] = []
  const savedIds: string[] = []
  await page.route('**/api/v1/scripts/import/commit-batch', async route => {
    payloads.push(route.request().postData() || '')
    const response = await route.fetch()
    expect(response.ok(), await response.text()).toBeTruthy()
    const result = await response.json()
    savedIds.push(result.scripts[0].id)
    if (payloads.length === 1) return route.abort('failed')
    return route.fulfill({ response })
  })
  await page.goto(`/#/scripts?mode=record&method=browser&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  const panel = page.getByTestId('device-capture-panel')
  await panel.getByRole('button', { name: 'Convert to script', exact: true }).click()
  await panel.getByRole('button', { name: 'Save to script library', exact: true }).click()
  await expect(panel.getByText(/The previous save has not been confirmed/)).toBeVisible()
  await expect(panel.getByRole('button', { name: 'Convert to script', exact: true })).toBeDisabled()
  await panel.getByRole('button', { name: 'Save to script library', exact: true }).click()
  await expect.poll(() => savedIds.length).toBe(2)
  expect(payloads[1]).toBe(payloads[0])
  expect(JSON.parse(payloads[0]).scripts[0].metadata.import.preserved.server_import_attestation_v1).toBeTruthy()
  expect(savedIds[1]).toBe(savedIds[0])
  expect(attestations).toBe(1)
  const library = await page.context().request.get(`/api/v1/scripts?project_id=${workspace.projectId}`)
  expect(library.ok(), await library.text()).toBeTruthy()
  const scripts = (await library.json()).scripts as Array<{ id: string; name: string }>
  expect(scripts.filter(item => item.name === name).map(item => item.id)).toEqual([savedIds[0]])
})
