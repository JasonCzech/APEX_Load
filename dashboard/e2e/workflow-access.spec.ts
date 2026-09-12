import { test, expect, json } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

test('Access policy navigation keeps the requested body through links and reloads', async ({
  page,
  request,
}) => {
  const workspace = await createWorkspace(request)
  await page.goto(`/#/access?accessView=users&${scopeQuery(workspace)}`)
  for (const label of [
    'Authentication',
    'Import policy',
    'Redaction',
    'Project secrets',
    'Governance audit',
  ]) {
    await page
      .getByRole('navigation', { name: 'Access administration' })
      .getByRole('link', { name: label, exact: true })
      .click()
    await page
      .getByRole('navigation', { name: 'Access administration' })
      .getByRole('link', { name: 'Feature policy', exact: true })
      .click()
    await expect(page.locator('#settings-section-feature-access')).toBeVisible()
    await expect(page.locator('#settings-section-general')).toBeHidden()
    await expect(page.getByText('Loading feature policy permissions…')).toBeHidden()
    await expect(page.getByRole('navigation', { name: 'Settings sections' })).toBeHidden()
  }
  await page.reload()
  await expect(page.locator('#settings-section-feature-access')).toBeVisible()
  await page
    .getByRole('navigation', { name: 'Access administration' })
    .getByRole('link', { name: 'Users & service accounts' })
    .click()
  await page.getByRole('tab', { name: /Service Accounts/ }).click()
  await page.getByRole('tab', { name: 'Create service account', exact: true }).click()
  await expect(page).toHaveURL(/accessView=create-account/)
  await expect(page.locator('.access-inspector')).toHaveCount(0)
  await expect(page.getByRole('button', { pressed: true }).first()).toBeVisible()
  await expect(page.getByLabel(`Role for ${workspace.label} project`)).toBeVisible()
  await page.reload()
  await expect(page.getByRole('tab', { name: 'Create service account', exact: true })).toHaveAttribute(
    'aria-selected',
    'true',
  )
  await page.setViewportSize({ width: 720, height: 1000 })
  await page.getByLabel(`Role for ${workspace.label} project`).scrollIntoViewIfNeeded()
  await page.screenshot({ path: 'test-results/access-create-narrow.png', animations:'disabled' })
  await page.getByRole('button', { name: 'Create Service Account', exact: true }).click()
  const issued = page.getByRole('dialog', { name: 'Issued API key', exact: true })
  await expect(issued).toBeVisible()
  await expect(issued.locator('.access-issued-examples')).toContainText(new URL(page.url()).origin)
  await issued.getByRole('button', { name: 'Close', exact: true }).click()
  await expect(page.getByRole('tab', { name: 'Existing Accounts', exact: true })).toHaveAttribute('aria-selected', 'true')
})

test('Access history pages beyond 50 and downloads all matching events', async ({ page, request }) => {
  test.setTimeout(90_000)
  const workspace = await createWorkspace(request)
  const user = await json(
    await request.post('/api/v1/auth/users', {
      data: { subject: `access-history-${Date.now()}`, display_name: 'History fixture' },
    }),
  )
  const id = String(user.id)
  for (let i = 0; i < 56; i++)
    await json(
      await request.post(`/api/v1/auth/users/${id}/bindings`, {
        data: { scope_type: 'project', scope_id: workspace.projectId, role: i % 2 ? 'viewer' : 'editor' },
      }),
    )
  await page.goto(`/#/access?accessView=events&${scopeQuery(workspace)}`)
  await page.getByPlaceholder('auth.user.create').fill('auth.user.binding.create')
  await page.getByRole('button', { name: 'Apply Filters', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Next events', exact: true })).toBeEnabled()
  const firstIds = await page
    .getByRole('table', { name: 'Audit events', exact: true })
    .locator('tbody tr')
    .count()
  // Use the server pagination controls, not the generic DOM-table paginator.
  await page.getByRole('button', { name: 'Next events', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Previous events', exact: true })).toBeEnabled()
  await expect(page.getByRole('button', { name: 'Next events', exact: true })).toBeDisabled()
  const downloadPromise = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Export all matching events CSV', exact: true }).click()
  const download = await downloadPromise
  const stream = await download.createReadStream()
  const chunks: Buffer[] = []
  if (stream) for await (const chunk of stream) chunks.push(Buffer.from(chunk))
  const csv = Buffer.concat(chunks).toString('utf8')
  expect(csv.split('\n').filter((line) => line.includes('auth.user.binding.create')).length).toBeGreaterThan(
    50,
  )
  expect(firstIds).toBe(50)
  await page.getByRole('tab', { name: 'Usage By User', exact: true }).click()
  await expect(page).toHaveURL(/accessView=usage/)
  await expect(page.getByRole('button', { name: 'Export usage CSV', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Export all matching events CSV', exact: true })).toHaveCount(
    0,
  )
  await page.goBack()
  await expect(page.getByRole('tab', { name: 'Events', exact: true })).toHaveAttribute(
    'aria-selected',
    'true',
  )
  await expect(page.getByPlaceholder('auth.user.create')).toHaveValue('auth.user.binding.create')
})

test('Access review opens remediation and renders long identities at narrow widths', async ({
  page,
  request,
}) => {
  const workspace = await createWorkspace(request)
  const name = `Access review ${Date.now()} long identity requiring entitlement review`
  const subject = `access-review-${Date.now()}`
  await json(
    await request.post('/api/v1/auth/users', {
      data: { subject, display_name: name },
    }),
  )
  await page.goto(`/#/access?accessView=review&${scopeQuery(workspace)}`)
  await expect(page.getByRole('heading', { name: 'Role permissions preview' })).toBeVisible()
  const row = page.locator('.access-review tbody tr').filter({ hasText: name })
  await expect(row).toContainText('Review recommended')
  await page.setViewportSize({ width: 720, height: 1000 })
  await page.screenshot({ path: 'test-results/access-review-narrow.png', animations:'disabled' })
  await row.getByRole('button', { name: 'Inspect user', exact: true }).click()
  await expect(page).toHaveURL(/accessView=users/)
  await expect(page.locator('.access-inspector')).toContainText(name)
  await expect(page.getByPlaceholder('name, email, role, scope')).toHaveValue(subject)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.getByRole('button', { name: 'Collapse user details', exact: true }).click()
  const table = page.locator('.access-users-table')
  await expect.poll(() => table.evaluate(element => {
    const wrapper = element.closest('.access-table-wrap')!
    return wrapper.scrollWidth <= wrapper.clientWidth
  })).toBe(true)
  await page.screenshot({ path: 'test-results/access-users-desktop.png', animations: 'disabled' })
  await page.getByRole('tab', { name: /Activity/ }).click()
  await page.getByRole('tab', { name: 'Access Review', exact: true }).click()
  const accountName = `Later review account ${Date.now()}`
  await json(await request.post('/api/v1/auth/service-accounts', { data: {
    name: accountName, global_roles: ['viewer'], scopes: ['results:read'],
  } }))
  await page.locator('.access-review').getByRole('button', { name: 'Refresh', exact: true }).click()
  const accountRow = page.locator('.access-review tbody tr').filter({ hasText: accountName })
  await accountRow.getByRole('button', { name: 'Inspect account', exact: true }).click()
  await expect(page.locator('.access-inspector')).toContainText(accountName)
})
