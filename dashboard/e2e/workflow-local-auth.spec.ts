import { readFile, stat } from 'node:fs/promises'
import { createHash } from 'node:crypto'
import { test, expect, login, createWorkspaceUI, importOpenAPI, buildScenarioUI, mutation, json, idOf, runStatus, signOut } from './support/workflow'

test.beforeEach(() => { test.skip(process.env.APEXLOAD_E2E_PROFILE !== 'local-auth', 'local-auth profile') })

test('[U01] sign in, build workspace, import, edit, launch, stop and retrieve the same run', async ({ page, target, trackRun }) => {
  test.setTimeout(240_000)
  await page.addInitScript(() => localStorage.setItem('apexload.featureFlags.runReport', 'true'))
  await login(page)
  const request = page.context().request
  const label = `arc-${Date.now()}`
  const workspace = await createWorkspaceUI(page, request, label)
  const scriptName = `Orders_${Date.now()}`
  const scriptId = await importOpenAPI(page, scriptName, target.url)
  const workbench = page.getByRole('region', { name: 'Author center workbench' })
  await expect(workbench.getByRole('tab', { name: 'Action editor' })).toHaveAttribute('aria-selected', 'true')
  await workbench.getByRole('tab', { name: 'Runtime Settings' }).click()
  await workbench.getByRole('textbox', { name: 'Base URL' }).fill(target.url + '/')
  const updated = mutation(page, 'PUT', `/api/v1/scripts/${scriptId}`)
  await page.getByRole('button', { name: 'Save script', exact: true }).click()
  await json(await updated)
  await page.getByRole('button', { name: 'Close script editor' }).click()
  const scenarioId = await buildScenarioUI(page, scriptName, `${label} scenario`)
  await expect(page).toHaveURL(new RegExp(`scenarioId=${scenarioId}`))
  const runName = `${label} run`
  await page.getByLabel('Run Name', { exact: true }).fill(runName)
  await page.getByRole('combobox', { name: 'Release', exact: true }).selectOption(workspace.releaseId)
  await expect(page.getByRole('button', { name: 'Start Now' })).toBeEnabled({ timeout: 30_000 })
  const created = mutation(page, 'POST', '/api/v1/tests')
  await page.getByRole('button', { name: 'Start Now' }).click()
  const run = await json(await created)
  const id = idOf(run); trackRun(id)
  expect(run.project_id).toBe(workspace.projectId)
  expect(run.name).toBe(runName)
  await expect(page).toHaveURL(new RegExp(`(?:test|command-center)/${id}`))
  await expect.poll(() => target.count(), { timeout: 60_000 }).toBeGreaterThan(0)
  const first = target.count()
  await expect.poll(() => target.count()).toBeGreaterThan(first)
  await expect.poll(() => runStatus(request, id)).toBe('RUNNING')
  await page.getByRole('button', { name: 'Stop', exact: true }).click()
  await page.getByRole('dialog', { name: 'Stop this test run?' }).getByRole('button', { name: 'Stop Test' }).click()
  await expect.poll(() => runStatus(request, id), { timeout: 90_000 }).toBe('COMPLETE')
  await expect.poll(async () => (await request.get(`/api/v1/tests/${id}/archive/manifest`)).ok(), { timeout: 60_000 }).toBe(true)
  await page.getByRole('button', { name: 'Results', exact: true }).first().click()
  await page.getByRole('searchbox', { name: 'Search results', exact: true }).fill(runName)
  const row = page.locator('tbody tr').filter({ hasText: runName })
  await expect(row).toBeVisible()
  await row.getByRole('button', { name: 'View results' }).click()
  await expect(page).toHaveURL(new RegExp(`#/report/${id}`))
  await expect(page.getByText(runName, { exact: false }).first()).toBeVisible()
  await page.getByRole('button', { name: 'Export report', exact: true }).click()
  const exportOptions = page.getByRole('region', { name: 'Report export options' })
  await expect(exportOptions.getByRole('button', { name: /Print-ready PDF/ })).toBeVisible()
  const analysisDownload = page.waitForEvent('download')
  await exportOptions.getByRole('button', { name: /Analysis tables/ }).click()
  const analysisBundle = await analysisDownload
  expect(analysisBundle.suggestedFilename()).toMatch(/-executive\.zip$/)
  const analysisPath = await analysisBundle.path()
  expect(analysisPath).not.toBeNull()
  expect((await stat(analysisPath!)).size).toBeGreaterThan(0)
  // Follow the report's evidence link, preserving the same run identity.
  await page.getByRole('tab', { name: 'Evidence', exact: true }).click()
  await page.getByText('Raw evidence downloads', { exact: true }).click()
  const download = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Download Transactions JSONL' }).first().click()
  const downloaded = await download
  const path = await downloaded.path(); expect(path).not.toBeNull()
  expect((await stat(path!)).size).toBeGreaterThan(0)
  const manifest = await json(await request.get(`/api/v1/tests/${id}/archive/manifest`))
  const serverFile = await request.get(`/api/v1/tests/${id}/archive/files/${String(manifest.transactions_file).split('/').pop()}`)
  expect(serverFile.ok()).toBeTruthy()
  expect(createHash('sha256').update(await readFile(path!)).digest('hex')).toBe(createHash('sha256').update(await serverFile.body()).digest('hex'))
})

test('[U02] viewer cannot launch and sign-out does not reopen a protected workspace', async ({ page }) => {
  await login(page, 'user')
  const auth = await json(await page.context().request.get('/api/v1/auth/me'))
  expect((auth.user as { subject: string }).subject).toBe('local:user')
  await page.goto('/#/new-test')
  await expect(page.getByRole('button', { name: 'Start Now' })).toBeDisabled()
  const denied = await page.context().request.post('/api/v1/tests', { data: { name: 'viewer must not create', duration: '1m', groups: [] } })
  expect(denied.status()).toBe(403)
  await signOut(page)
  await expect(page.getByRole('heading', { name: 'Choose a test persona' })).toBeVisible()
  await page.goBack()
  await expect(page.getByRole('heading', { name: 'Choose a test persona' })).toBeVisible()
  await expect(page.locator('nav.sidebar-nav')).toBeHidden()
})

test('editor and viewer monitoring, schedules and new-test pages ask only for what the role may read', async ({ page }) => {
  test.setTimeout(120_000)
  // A request the controller refuses is console noise and an empty panel;
  // these pages check the session's access first and say what is not shown.
  const refused: string[] = []
  page.on('response', response => {
    if (response.status() === 403 && new URL(response.url()).pathname.startsWith('/api/')) refused.push(`${response.request().method()} ${new URL(response.url()).pathname}`)
  })
  for (const persona of ['dev', 'user']) {
    await login(page, persona)
    for (const route of ['/#/monitoring', '/#/monitoring?category=telemetry', '/#/monitoring?category=alerts', '/#/fleet', '/#/schedules', '/#/new-test']) {
      await page.goto(route)
      await expect(page.locator('main')).toBeVisible()
      await page.waitForLoadState('networkidle')
    }
    await signOut(page)
    await expect(page.getByRole('heading', { name: 'Choose a test persona' })).toBeVisible()
  }
  expect(refused).toEqual([])
})

test('[U03] viewer personal settings persist without exposing administrative settings', async ({ page }) => {
  await login(page, 'user')
  const adminSettingsRequests: string[] = []
  page.on('request', request => { if (/\/api\/v1\/system\/settings(?:\?|$)/.test(request.url())) adminSettingsRequests.push(request.url()) })
  await page.locator('button.nav-item[aria-label="Settings"]').click()
  await expect(page.getByRole('navigation', { name: 'Personal settings sections' })).toBeVisible()
  await page.getByLabel('Dashboard Theme', { exact: true }).selectOption('light')
  await page.getByLabel('Motion', { exact: true }).selectOption('reduce')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  await expect(page.locator('html')).toHaveAttribute('data-motion', 'reduce')
  await page.getByRole('button', { name: 'Tables & editor', exact: true }).click()
  await page.getByLabel('Default rows per table', { exact: true }).selectOption('25')
  await page.getByLabel('Editor word wrap', { exact: true }).selectOption('off')
  await page.reload()
  await expect(page.getByLabel('Default rows per table', { exact: true })).toHaveValue('25')
  await expect(page.getByLabel('Editor word wrap', { exact: true })).toHaveValue('off')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  await expect(page.locator('html')).toHaveAttribute('data-motion', 'reduce')
  await page.getByRole('button', { name: 'Account', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Your account' })).toContainText('Local Viewer')
  await page.goto('/#/settings?setting=authentication')
  await expect(page.getByLabel('Dashboard Theme', { exact: true })).toBeVisible()
  await expect(page.getByRole('navigation', { name: 'Personal settings sections' })).toBeVisible()
  expect(adminSettingsRequests).toEqual([])
  const denied = await page.context().request.put('/api/v1/system/settings', { data: {} })
  expect(denied.status()).toBe(403)
})
