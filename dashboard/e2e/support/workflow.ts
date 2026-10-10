import { expect, test as base, type Page, type APIRequestContext, type APIResponse, type Response } from '@playwright/test'
import { createServer } from 'node:http'
import { type AddressInfo } from 'node:net'
import { type Workspace } from './workspace'

function record(payload: unknown, key?: string): Record<string, unknown> {
  if (!payload || typeof payload !== 'object') {
    throw new Error(`expected an object response, received ${JSON.stringify(payload)}`)
  }
  const root = payload as Record<string, unknown>
  if (!key) return root
  const nested = root[key]
  return nested && typeof nested === 'object' ? nested as Record<string, unknown> : root
}

function responseID(payload: unknown, what: string, key?: string): string {
  const id = record(payload, key).id
  if (typeof id !== 'string' || id === '') {
    throw new Error(`${what} response carried no id: ${JSON.stringify(payload).slice(0, 300)}`)
  }
  return id
}

function routeScope(url: string): URLSearchParams {
  const query = new URL(url).hash.split('?')[1] || ''
  return new URLSearchParams(query)
}

export async function createWorkspaceUI(page: Page, request: APIRequestContext, label: string): Promise<Workspace> {
  const domainName = `${label} domain`
  const ownerProjectName = `${label} owner`
  const decoyProjectName = `${label} decoy`
  const applicationName = `${label} application`
  const releaseName = `${label} 1.0`

  await page.goto('/#/settings')
  await page.getByRole('navigation', { name: 'Settings sections' })
    .getByRole('button', { name: 'Domains & placement defaults' })
    .click()

  await page.getByPlaceholder('Create domain, e.g. Retail').fill(domainName)
  const domainResponsePromise = page.waitForResponse(response => (
    response.request().method() === 'POST'
    && new URL(response.url()).pathname === '/api/v1/domains'
  ))
  await page.getByRole('button', { name: 'Add domain' }).click()
  const domainPayload = await (await domainResponsePromise).json()
  const domainId = responseID(domainPayload, 'create domain', 'domain')
  responseID(domainPayload, 'provision application repository', 'application_repository')
  const categoryId = responseID(domainPayload, 'default category', 'default_category')

  await expect(page.getByText(`Domain "${domainName}" created.`)).toBeVisible({ timeout: 15_000 })
  await expect(page.locator('button.settings-app-card').filter({ hasText: domainName })).toBeVisible()

  const settingsSections = page.getByRole('navigation', { name: 'Settings sections' })
  await settingsSections.getByRole('button', { name: 'Projects', exact: true }).click()
  const projectNameInput = page.getByPlaceholder('Create project, e.g. Customer Portal')

  const createProject = async (name: string) => {
    await projectNameInput.fill(name)
    const responsePromise = page.waitForResponse(response => (
      response.request().method() === 'POST'
      && new URL(response.url()).pathname === '/api/v1/projects'
    ))
    await page.getByRole('button', { name: 'Add Project' }).click()
    const payload = await (await responsePromise).json()
    const project = record(payload, 'project')
    expect(project.domain_id, `${name} must be created in the UI-selected domain`).toBe(domainId)
    await expect(page.locator('button.settings-app-card').filter({ hasText: name })).toBeVisible({ timeout: 15_000 })
    return responseID(payload, `create project ${name}`, 'project')
  }

  const ownerProjectId = await createProject(ownerProjectName)
  const decoyProjectId = await createProject(decoyProjectName)
  expect(routeScope(page.url()).get('projectId')).toBe(decoyProjectId)

  // Keep the decoy as the active workspace project, then explicitly target the
  // owner in the application picker. This is the wrong-project regression:
  // the picker must move real workspace scope, not only its local form cursor.
  await settingsSections.getByRole('button', { name: 'Applications & releases' }).click()
  await page.getByPlaceholder('Create application, e.g. ACC').fill(applicationName)
  const applicationProject = page.getByRole('combobox', { name: 'Project for the new application' })
  await applicationProject.selectOption(ownerProjectId)
  await expect(applicationProject).toHaveValue(ownerProjectId)

  const applicationResponsePromise = page.waitForResponse(response => (
    response.request().method() === 'POST'
    && new URL(response.url()).pathname === '/api/v1/applications'
  ))
  await page.getByRole('button', { name: 'Add Application' }).click()
  const applicationPayload = await (await applicationResponsePromise).json()
  const application = record(applicationPayload, 'application')
  const applicationId = responseID(applicationPayload, 'create application', 'application')
  expect(application.project_id, 'application must belong to the project chosen in the UI').toBe(ownerProjectId)

  await expect(page.locator('button.settings-app-card').filter({ hasText: applicationName })).toBeVisible({ timeout: 15_000 })
  await expect(page.getByRole('button', { name: new RegExp(`Workspace context\\..*${ownerProjectName}`) })).toBeVisible()
  await expect.poll(() => routeScope(page.url()).get('projectId')).toBe(ownerProjectId)
  await expect.poll(() => routeScope(page.url()).get('applicationId')).toBe(applicationId)

  const storedApplication = await request.get(`/api/v1/applications/${applicationId}`)
  expect(storedApplication.ok()).toBeTruthy()
  expect(record(await storedApplication.json(), 'application').project_id).toBe(ownerProjectId)

  // Release creation in the sidebar changes active scope. The repository tabs
  // below then prove that the same hierarchy is visible on the S2 route.
  await page.getByRole('button', { name: /^Workspace context\./ }).click()
  await page.getByRole('button', { name: /Select release\. Current release:/ }).click()
  await page.getByRole('button', { name: 'New Release' }).click()
  const releaseDialog = page.getByRole('dialog', { name: 'New Release' })
  await releaseDialog.getByRole('textbox', { name: 'Name' }).fill(releaseName)
  const releaseResponsePromise = page.waitForResponse(response => (
    response.request().method() === 'POST'
    && new URL(response.url()).pathname === `/api/v1/projects/${ownerProjectId}/releases`
  ))
  await releaseDialog.getByRole('button', { name: 'Create release' }).click()
  const releasePayload = await (await releaseResponsePromise).json()
  const release = record(releasePayload, 'release')
  const releaseId = typeof release.application_release_id === 'string'
    ? release.application_release_id
    : responseID(releasePayload, 'create release', 'release')

  await expect(page.getByRole('button', { name: new RegExp(`Workspace context\\..*${releaseName}`) })).toBeVisible({ timeout: 15_000 })
  await expect.poll(() => routeScope(page.url()).get('releaseId')).toBe(releaseId)

  await page.getByRole('button', { name: 'Repository', exact: true }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Repository' })).toBeVisible({ timeout: 15_000 })

  const repositoryTabs = page.getByRole('tablist', { name: 'Repository asset types' })
  await repositoryTabs.getByRole('tab', { name: /Projects/ }).click()
  await expect(page.getByRole('main').getByRole('button', { name: ownerProjectName, exact: true }).first()).toBeVisible()
  await repositoryTabs.getByRole('tab', { name: /Releases/ }).click()
  await expect(page.getByRole('main').getByRole('button', { name: releaseName, exact: true }).first()).toBeVisible()

  const finalScope = routeScope(page.url())
  expect(finalScope.get('projectId')).toBe(ownerProjectId)
  expect(finalScope.get('applicationId')).toBe(applicationId)
  expect(finalScope.get('releaseId')).toBe(releaseId)
  return { label, domainId, categoryId, projectId: ownerProjectId, applicationId, releaseId }
}

export async function json(response: APIResponse | Response): Promise<Record<string, unknown>> {
  expect(response.ok(), await response.text()).toBeTruthy()
  return response.json()
}
export function idOf(payload: Record<string, unknown>, key?: string): string {
  return responseID(payload, 'resource', key)
}
export function mutation(page: Page, method: string, path: string) {
  return page.waitForResponse(r => r.request().method() === method && new URL(r.url()).pathname === path)
}
export async function login(page: Page, username = 'admin') {
  // Workflow tests share a controller and source IP. Pace real UI logins below
  // its ten-per-minute limit rather than retrying failures or weakening auth.
  await new Promise(resolve => setTimeout(resolve, 6500))
  await page.goto('/#/dashboard')
  await page.getByLabel('Username').fill(username)
  await page.getByLabel('Password').fill(username)
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Dashboard' })).toBeVisible()
}
/** The collapsed sidebar keeps the account card, with Sign out, behind its avatar; open it. */
export async function showAccountCard(page: Page) {
  const account = page.getByRole('button', { name: /Open account details$/ })
  if (await account.isVisible()) await account.click()
}
export async function signOut(page: Page) {
  await showAccountCard(page)
  await page.getByRole('button', { name: 'Sign out' }).click()
}
export async function seedScript(request: APIRequestContext, workspace: Workspace, name = 'Workflow_HTTP', baseURL = 'http://127.0.0.1:1', path = '/orders') {
  return idOf(await json(await request.post('/api/v1/scripts', { data: {
    project_id: workspace.projectId,
    script: { name, version: '1.0.0', protocol: 'http', config: { base_url: baseURL, pacing: { type: 'fixed_interval', interval_ms: 500 } }, actions: [{ name: 'orders', type: 'request', method: 'GET', target: path }] },
  } })))
}
export async function seedScenario(request: APIRequestContext, workspace: Workspace, scriptId: string, name = 'Workflow scenario') {
  return idOf(await json(await request.post('/api/v1/scenarios', { data: {
    project_id: workspace.projectId, application_id: workspace.applicationId, name, duration: '2m', scenario_mode: 'vuser_driven', groups: [{ name: 'orders', script_id: scriptId, vusers: 1, ramp_up: '1s' }],
  } })))
}
export async function importOpenAPI(page: Page, name: string, target: string) {
  await page.getByRole('button', { name: 'Scripts', exact: true }).click()
  // Navigate through the actual creation entry rather than seeding the script.
  await page.getByRole('button', { name: 'Script Builder', exact: true }).first().click()
  await page.getByRole('button', { name: 'Import', exact: true }).click()
  await page.getByRole('button', { name: /OpenAPI \/ Swagger/ }).click()
  const controls = page.getByRole('group', { name: 'Script import controls' })
  const spec = { openapi: '3.0.3', info: { title: name, version: '1.0' }, servers: [{ url: target }], paths: { '/orders': { get: { operationId: 'listOrders', responses: { '200': { description: 'Orders' } } } } } }
  await controls.locator('input[type=file]').setInputFiles({ name: 'orders.openapi.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(spec)) })
  await controls.getByRole('textbox', { name: 'Script Name' }).fill(name)
  await controls.getByText('Advanced validation only', { exact: true }).click()
  await controls.getByRole('button', { name: 'Validate Only' }).click()
  await expect(controls.getByText(/dry-run validation; nothing has been saved/i)).toBeVisible()
  await controls.locator('.import-source-setup > summary').click()
  await controls.getByRole('button', { name: 'Inspect & review' }).click()
  await expect(controls.getByRole('heading', { name: 'Review Generated Script' })).toBeVisible()
  const saved = mutation(page, 'POST', '/api/v1/scripts/import/openapi/commit')
  await controls.getByRole('button', { name: 'Save draft' }).click()
  const scriptId = idOf(await json(await saved), 'stored_script')
  await controls.getByRole('button', { name: `Open ${name} in Visual Editor`, exact: true }).click()
  return scriptId
}
export async function buildScenarioUI(page: Page, scriptName: string, name: string) {
  await page.getByRole('button', { name: 'New Test', exact: true }).first().click()
  await page.getByRole('button', { name: 'Open Scenario Builder' }).click()
  const steps = page.locator('[aria-label="Scenario builder steps"]')
  await page.getByPlaceholder('Scenario name').fill(name)
  await page.getByRole('button', { name: 'Choose script for Group 1' }).click()
  await page.getByRole('row').filter({ hasText: scriptName }).getByRole('button', { name: 'Use for Group 1' }).click()
  await steps.getByRole('button', { name: /^Model/ }).click()
  await page.getByRole('button', { name: /^Fixed VUsers/ }).click()
  await page.getByRole('button', { name: 'Use Closed VUser', exact: true }).click()
  await steps.getByRole('button', { name: /^Run Settings/ }).click()
  const scenarioTabs = page.getByRole('tablist', { name: 'Scenario settings sections' })
  await scenarioTabs.getByRole('tab', { name: 'Run Phases' }).click()
  await page.getByLabel('Ramp-up', { exact: true }).fill('1')
  await page.getByLabel('Ramp-up Unit').selectOption('s')
  await page.getByLabel('Sustained load', { exact: true }).fill('1')
  await page.getByLabel('Sustained load Unit').selectOption('m')
  await scenarioTabs.getByRole('tab', { name: 'Target Load' }).click()
  await page.getByLabel('Peak VUsers', { exact: true }).fill('1')
  await steps.getByRole('button', { name: /^Data/ }).click()
  await page.getByRole('button', { name: 'Next', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Review And Validate' })).toBeVisible()
  const saved = mutation(page, 'POST', '/api/v1/scenarios')
  await page.getByRole('button', { name: 'Save and configure run' }).click()
  const payload = await json(await saved)
  return idOf(payload)
}
export async function runStatus(request: APIRequestContext, id: string) {
  return String((await json(await request.get(`/api/v1/tests/${id}`))).status)
}

interface WorkflowFixtures {
  target: { url: string; count: () => number }
  trackRun: (id: string) => void
}
export const test = base.extend<WorkflowFixtures>({
  // Playwright requires destructured fixture dependencies, even when empty.
  // eslint-disable-next-line no-empty-pattern
  target: async ({}, provide) => {
    let count = 0
    const server = createServer((req, res) => { count++; res.setHeader('Content-Type', 'application/json'); res.statusCode = req.url === '/unavailable' ? 503 : 200; res.end(res.statusCode === 503 ? '{"message":"Service unavailable"}' : '{"orders":[{"id":1}]}') })
    await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
    await provide({ url: `http://127.0.0.1:${(server.address() as AddressInfo).port}`, count: () => count })
    server.closeAllConnections()
    await new Promise<void>((resolve, reject) => server.close(err => err ? reject(err) : resolve()))
  },
  trackRun: [async ({ playwright, baseURL, page, target }, provide) => {
    // Depend on target so it remains available until run teardown completes.
    void target
    const ids = new Set<string>()
    const pending: Promise<void>[] = []
    const capture = (r: Response) => {
      if (r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/tests' && r.ok()) {
        pending.push(r.json().then(payload => { ids.add(idOf(payload)) }))
      }
    }
    page.on('response', capture)
    await provide(id => { ids.add(id) })
    page.off('response', capture)
    await Promise.all(pending)
    const cleanup = await playwright.request.newContext({ baseURL, storageState: await page.context().storageState() })
    try {
      for (const id of ids) {
        const response = await cleanup.get(`/api/v1/tests/${id}`)
        if (response.status() === 404) continue
        const terminal = ['COMPLETE', 'ABORTED', 'FAILED', 'CANCELLED']
        if (!terminal.includes(String((await json(response)).status))) {
          await json(await cleanup.post(`/api/v1/tests/${id}/abort`))
          await expect.poll(() => runStatus(cleanup, id), { timeout: 15_000 }).toMatch(/^(COMPLETE|ABORTED|FAILED|CANCELLED)$/)
        }
      }
    } finally { await cleanup.dispose() }
  }, { auto: true, timeout: 30_000 }],
})
export { expect } from '@playwright/test'
