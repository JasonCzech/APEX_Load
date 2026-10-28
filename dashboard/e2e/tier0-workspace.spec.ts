import { expect, test } from '@playwright/test'
import { createWorkspace, scopeQuery } from './support/workspace'

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

// Tier-0 journey step S2 (docs/qa/E2E_SUITES.md): the workspace hierarchy a
// user builds before anything else can happen.
//
// Two assertions here are regression guards rather than happy-path checks:
//
//   * The created project must actually be reachable and shown. A dashboard
//     that renders an empty repository for a project the API just created is
//     the failure this catches.
//   * An application must be created against the project the caller asked for.
//     A local-only project cursor previously produced both a spurious "Select a
//     project before creating an application" error and, worse, silent creation
//     against the wrong project — a wrong-project write that nothing surfaced.

test('the API-created workspace hierarchy is coherent and scoped', async ({ request }) => {
  const workspace = await createWorkspace(request)

  // Every level must be addressable on its own, not merely returned by the
  // creating call.
  const project = await request.get(`/api/v1/projects/${workspace.projectId}`)
  expect(project.ok(), 'created project must be retrievable').toBeTruthy()

  const application = await request.get(`/api/v1/applications/${workspace.applicationId}`)
  expect(application.ok(), 'created application must be retrievable').toBeTruthy()

  // The wrong-project guard: the application must belong to the project it was
  // created under, not to whatever project the server considered "active".
  const applicationBody = (await application.json()) as Record<string, unknown>
  const owner = (applicationBody.application ?? applicationBody) as Record<string, unknown>
  expect(owner.project_id, 'application must belong to the project it was created under')
    .toBe(workspace.projectId)

  // The release must be listed under its application, which is the relationship
  // the run-creation flow later depends on.
  const releases = await request.get(`/api/v1/applications/${workspace.applicationId}/releases`)
  expect(releases.ok(), 'application releases must be listable').toBeTruthy()
  const releaseText = await releases.text()
  expect(releaseText, 'the created release must appear under its application')
    .toContain(workspace.releaseId)
})

test('the repository view renders a freshly created project', async ({ page, request }) => {
  const workspace = await createWorkspace(request)

  await page.goto(`/#/repository?${scopeQuery(workspace)}`)

  // Assert on the project's own name rather than on chrome: a shell that renders
  // while the data call fails would satisfy a chrome-only assertion.
  await expect(
    page.locator('main').getByText(`${workspace.label} project`, { exact: false }).filter({ visible: true }).first(),
    'the repository view must show the project that was just created',
  ).toBeVisible({ timeout: 15_000 })
})

test('a link to a project created after the app loaded opens it', async ({ page, request }) => {
  const first = await createWorkspace(request)
  await page.goto(`/#/repository?${scopeQuery(first)}`)
  await expect(page.locator('main').getByText(`${first.label} project`, { exact: false }).filter({ visible: true }).first()).toBeVisible({ timeout: 15_000 })

  // The app listed projects before this one existed: following a link to it
  // must look again rather than call the project missing.
  const later = await createWorkspace(request)
  await page.evaluate(hash => { window.location.hash = hash }, `#/repository?${scopeQuery(later)}`)
  await expect(page.locator('main').getByText(`${later.label} project`, { exact: false }).filter({ visible: true }).first()).toBeVisible({ timeout: 15_000 })
  await expect(page.getByText('is not available to you or no longer exists', { exact: false })).toHaveCount(0)
})

test('the UI creates and scopes the complete workspace hierarchy', async ({ page, request }) => {
  test.setTimeout(120_000)
  const label = `tier0-ui-${Date.now()}`
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
  const expandSidebar = page.getByRole('button', { name: 'Expand sidebar', exact: true })
  if (await expandSidebar.isVisible()) await expandSidebar.click()
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
})
