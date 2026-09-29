import { test, expect, json, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'
import { abortLiveRun, startLiveRun } from './support/liveRun'

test.beforeEach(async ({ page }) => {
  test.setTimeout(60000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
})

test('Confluence connection setup follows saved project connection workflow', async ({ page }) => {
  const workspace = await createWorkspace(page.context().request)
  await page.goto(`/#/settings?setting=confluence&${scopeQuery(workspace)}`)
  await page.reload()
  const card = page.getByLabel('Confluence connections')
  await expect(card).toBeVisible()
  await card.getByRole('button', { name: 'Add Confluence connection' }).click()
  const editor = card.getByLabel('Confluence connection editor')
  await editor.getByLabel('Name', { exact: true }).fill('Workflow wiki')
  await editor.getByLabel('Confluence Cloud site URL').fill('https://example.atlassian.net')
  await editor.getByLabel('Atlassian account email').fill('writer@example.com')
  await editor.getByLabel('Space ID').fill('42')
  await editor.getByLabel('Parent page ID').fill('100')
  await editor.getByLabel(/API token project-secret reference/).fill('workflow-wiki-token')
  await editor.getByRole('checkbox', { name: 'Enable connection' }).check()
  const created = page.waitForResponse(response => response.request().method() === 'POST'
    && new URL(response.url()).pathname === `/api/v1/projects/${workspace.projectId}/integrations/confluence-connections`)
  await editor.getByRole('button', { name: 'Save connection' }).click()
  const response = await created
  expect(response.ok()).toBeTruthy()
  expect(await response.text()).not.toContain('workflow-wiki-token')
  await expect(card).toContainText('Workflow wiki')
  await card.getByRole('button', { name: 'Edit' }).click()
  await expect(editor.getByLabel(/API token project-secret reference/)).toHaveValue('')
  await editor.getByRole('button', { name: 'Discard draft' }).click()
  await card.getByRole('button', { name: 'Delete' }).click()
  await card.getByRole('button', { name: 'Confirm delete' }).click()
  await expect(card).toContainText('No Confluence connections are configured')
})

test('archived run report publishes to a selected Confluence connection', async ({ page }) => {
  test.setTimeout(120000)
  const request=page.context().request
  await page.addInitScript(() => localStorage.setItem('apexload.featureFlags.runReport','true'))
  const run=await startLiveRun(request,process.env.APEXLOAD_E2E_BASE_URL,{label:'confluence-report',duration:'2m'})
  try {
    expect((await request.post(`/api/v1/tests/${run.testId}/stop`)).ok()).toBeTruthy()
    await expect.poll(async () => {
      const response=await request.get(`/api/v1/tests/${run.testId}`)
      const payload=await response.json() as {archive?:unknown}
      return Boolean(payload.archive)
    },{timeout:60000}).toBe(true)
    const publication={test_id:run.testId,project_id:'default',connection_id:'wiki',status:'complete',page_id:'200',page_url:'https://example.atlassian.net/wiki/spaces/42/pages/200',attempts:1,created_at:'2026-09-25T00:00:00Z',updated_at:'2026-09-25T00:00:00Z'}
    let published=false
    await page.route(`**/api/v1/tests/${run.testId}/publish/confluence`,route => {
      if (route.request().method()==='POST') {published=true;return route.fulfill({json:{publication}})}
      return route.fulfill({json:{connections:[{id:'wiki',name:'Workflow wiki',enabled:true}],publications:published?[publication]:[]}})
    })
    await page.goto(`/#/report/${run.testId}`)
    await page.getByRole('button',{name:'Export report'}).click()
    await page.getByText('Publish to Confluence',{exact:true}).first().click()
    await expect(page.getByRole('combobox',{name:'Confluence connection'})).toHaveValue('wiki')
    await page.getByRole('button',{name:'Publish to Confluence'}).click()
    await expect(page.getByRole('link',{name:'Open page'})).toHaveAttribute('href',publication.page_url)
    await expect(page.getByRole('button',{name:'Update Confluence page'})).toBeVisible()
  } finally {await abortLiveRun(request,run)}
})

test('Monitoring preserves repeated keyboard navigation and calendar context', async ({ page }) => {
  const request = page.context().request
  const workspace = await createWorkspace(request)
  await page.goto(`/#/monitoring?${scopeQuery(workspace)}`)
  // Refresh the workspace inventory after API fixture creation in an already signed-in page.
  await page.reload()
  const controlPlane = page.locator('[data-card-id="system-health"]')
  await expect(controlPlane.locator('.monitor-build-heading')).toBeVisible()
  await expect(controlPlane.locator('.monitor-build-row')).toHaveCount(3)
  await expect(controlPlane.locator('.monitor-build-date')).toHaveCount(3)
  await expect(controlPlane).not.toContainText('0.0.0.0:8080')
  const tabs = page.getByRole('tablist', { name: 'Monitor categories' })
  await tabs.getByRole('tab', { name: 'Overview', exact: true }).focus()
  for (const name of ['Fleet', 'Telemetry', 'Alerts', 'Schedules']) {
    await page.keyboard.press('ArrowRight')
    await expect(tabs.getByRole('tab', { name, exact: true })).toBeFocused()
  }
  await page.getByRole('button', { name: 'Day', exact: true }).click()
  await page.getByRole('button', { name: 'Cron', exact: true }).click()
  await page.getByRole('checkbox', { name: 'Show queued runs', exact: true }).uncheck()
  await tabs.getByRole('tab', { name: 'Alerts', exact: true }).click()
  const incidentHistory = page.locator('.monitor-incident-history')
  await expect(incidentHistory.getByRole('heading', { name: 'Shared SLA incident history' })).toBeVisible()
  await expect(incidentHistory.getByRole('searchbox', { name: 'Search incidents' })).toBeVisible()
  await tabs.getByRole('tab', { name: 'Telemetry', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Datasource evidence' })).toBeVisible()
  await tabs.getByRole('tab', { name: 'Schedules', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Day', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByRole('button', { name: 'Cron', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByRole('checkbox', { name: 'Show queued runs', exact: true })).not.toBeChecked()
  await page.reload()
  await expect(page.getByRole('button', { name: 'Day', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByRole('button', { name: 'Schedule run', exact: true })).toBeVisible()
})

test('Collectors receive a heartbeat while Monitoring remains open', async ({ page }) => {
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const created = await json(await request.post('/api/v1/monitoring/collectors', { data: { project_id: workspace.projectId, name: 'Monitoring refresh acceptance', allowed_metric_packs: ['linux-hosts'] } }))
  const collector = created.collector as { id: string }
  await page.route('**/api/v1/integrations/apm/profiles', async route => {
    const response = await route.fetch()
    const profiles = await response.json()
    profiles.profiles.find((profile: { id: string }) => profile.id === 'opentelemetry').status = 'unavailable'
    await route.fulfill({ response, json: profiles })
  })
  await page.goto(`/#/monitoring?category=telemetry&${scopeQuery(workspace)}`)
  await page.reload()
  await expect(page.locator('[data-card-id="apm-integrations"] .monitor-header-badge.danger')).toContainText('1 unavailable')
  const row = page.locator('.pp-row').filter({ hasText: 'Monitoring refresh acceptance' })
  await expect(row).toBeVisible()
  await json(await request.post(`/api/v1/monitoring/collectors/${collector.id}/heartbeat`, { headers: { Authorization: `Bearer ${created.token}` }, data: { packs: [{ pack: 'linux-hosts', samples: { cpu_pct: 17 } }] } }))
  await expect(row).toContainText('healthy', { timeout: 15000 })
  await row.getByRole('button', { name: 'Metrics', exact: true }).click()
  const metrics = page.getByRole('dialog', { name: /Latest captured metrics/ })
  await expect(metrics).toContainText('cpu_pct')
  await expect(metrics.getByRole('button', { name: 'Refresh metrics', exact: true })).toBeVisible()
})

test('ELK connection plumbing is manageable without exposing secret references', async ({ page }) => {
  const request = page.context().request
  const workspace = await createWorkspace(request)
  await page.goto(`/#/monitoring?category=telemetry&${scopeQuery(workspace)}`)
  await page.reload()

  await page.getByRole('link', { name: 'Elastic capture settings', exact: true }).click()
  const card = page.locator('[aria-label="ELK connections"]')
  const form = card.locator('[aria-label="ELK connection editor"]')
  await card.getByRole('button', { name: 'Add ELK connection', exact: true }).click()
  await expect(form).toBeVisible()
  await form.getByLabel('Name (required)', { exact: true }).fill('Workflow Elastic logs')
  await form.getByLabel('Elasticsearch endpoint', { exact: true }).fill('https://127.0.0.1:1')
  await form.getByLabel('Index pattern', { exact: true }).fill('workflow-logs-*')
  await expect(form.getByLabel('Name (required)', { exact: true })).toHaveCSS('min-height', '40px')
  await form.getByRole('button', { name: 'Add aggregation', exact: true }).click()
  const metricName = form.getByRole('textbox', { name: 'Metric 1 name', exact: true })
  await metricName.pressSequentially('duration_avg')
  await expect(metricName).toBeFocused()
  await expect(metricName).toHaveValue('duration_avg')
  await form.getByRole('textbox', { name: 'Metric 1 field', exact: true }).fill('duration_ms')
  await form.getByRole('combobox', { name: 'Authentication', exact: true }).selectOption('api_key')
  await form.getByLabel('API key project-secret reference', { exact: true }).fill('workflow-elastic-api-key')
  await form.getByRole('checkbox', { name: 'Enable for new tests', exact: true }).check()

  const created = page.waitForResponse(response => response.request().method() === 'POST'
    && new URL(response.url()).pathname === `/api/v1/projects/${workspace.projectId}/monitoring/elk-connections`)
  await form.getByRole('button', { name: 'Save connection', exact: true }).click()
  const createdResponse = await created
  expect(createdResponse.ok()).toBeTruthy()
  const safeResponse = await createdResponse.text()
  expect(safeResponse).not.toContain('workflow-elastic-api-key')
  await expect(card).toContainText('Workflow Elastic logs')
  await expect(card).toContainText('ready')

  await card.getByRole('button', { name: 'Edit', exact: true }).click()
  await form.getByLabel('Name (required)', { exact: true }).fill('Workflow Elastic logs updated')
  const updated = page.waitForResponse(response => response.request().method() === 'PUT'
    && new URL(response.url()).pathname.includes('/monitoring/elk-connections/'))
  await form.getByRole('button', { name: 'Save connection', exact: true }).click()
  expect((await updated).ok()).toBeTruthy()
  await expect(card).toContainText('Workflow Elastic logs updated')

  const tested = page.waitForResponse(response => response.request().method() === 'POST'
    && new URL(response.url()).pathname.endsWith('/test'))
  await card.getByRole('button', { name: 'Test saved connection', exact: true }).click()
  expect((await tested).status()).toBe(502)
  await expect(card).toContainText('ELK connection test failed')

  const deleted = page.waitForResponse(response => response.request().method() === 'DELETE'
    && new URL(response.url()).pathname.includes('/monitoring/elk-connections/'))
  await card.getByRole('button', { name: 'Delete', exact: true }).click()
  await expect(card.getByRole('group', { name: 'Confirm connection deletion' })).toBeVisible()
  await card.getByRole('button', { name: 'Confirm delete', exact: true }).click()
  expect((await deleted).status()).toBe(204)
  await expect(card).not.toContainText('Workflow Elastic logs updated')
})

test('Dynatrace SaaS connection plumbing is manageable without exposing secret references', async ({ page }) => {
  const request = page.context().request
  const workspace = await createWorkspace(request)
  await page.goto(`/#/monitoring?category=telemetry&${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByRole('link', { name: 'Dynatrace capture settings', exact: true }).click()
  const card = page.locator('[aria-label="Dynatrace connections"]')
  const form = card.locator('[aria-label="Dynatrace connection editor"]')
  await card.getByRole('button', { name: 'Add Dynatrace connection', exact: true }).click()
  await expect(form).toBeVisible()
  await form.getByLabel('Name (required)', { exact: true }).fill('Workflow Dynatrace')
  await expect(form.getByLabel('Environment URL', { exact: true })).toHaveCount(0)
  await expect(form.getByLabel('Log timestamp field', { exact: true })).toHaveCount(0)
  await form.getByRole('checkbox', { name: 'Problems API', exact: true }).check()
  await form.getByLabel('Environment URL', { exact: true }).fill('https://workflow.live.dynatrace.com')
  await form.getByLabel('Environment token project-secret reference', { exact: true }).fill('workflow-dynatrace-token')
  await form.getByRole('checkbox', { name: 'Problems API', exact: true }).check()
  await form.getByRole('checkbox', { name: 'Enable for new tests', exact: true }).check()
  const created = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === `/api/v1/projects/${workspace.projectId}/monitoring/dynatrace-connections`)
  await form.getByRole('button', { name: 'Save connection', exact: true }).click()
  const createdResponse = await created
  expect(createdResponse.ok()).toBeTruthy()
  expect(await createdResponse.text()).not.toContain('workflow-dynatrace-token')
  await expect(card).toContainText('Workflow Dynatrace')
  await expect(card).toContainText('ready')
  await card.getByRole('button', { name: 'Edit', exact: true }).click()
  await form.getByLabel('Name (required)', { exact: true }).fill('Workflow Dynatrace updated')
  const updated = page.waitForResponse(response => response.request().method() === 'PUT' && new URL(response.url()).pathname.includes('/monitoring/dynatrace-connections/'))
  await form.getByRole('button', { name: 'Save connection', exact: true }).click()
  expect((await updated).ok()).toBeTruthy()
  await expect(card).toContainText('Workflow Dynatrace updated')
  const deleted = page.waitForResponse(response => response.request().method() === 'DELETE' && new URL(response.url()).pathname.includes('/monitoring/dynatrace-connections/'))
  await card.getByRole('button', { name: 'Delete', exact: true }).click()
  await expect(card.getByRole('group', { name: 'Confirm connection deletion' })).toBeVisible()
  await card.getByRole('button', { name: 'Confirm delete', exact: true }).click()
  expect((await deleted).status()).toBe(204)
  await expect(card).not.toContainText('Workflow Dynatrace updated')
})
