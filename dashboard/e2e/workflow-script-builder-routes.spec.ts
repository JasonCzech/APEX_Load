import { test, expect, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

// The Script builder's steps are routes of one page: Back returns from
// Recording Studio or a draft to the builder tab it came from, a draft asks
// before Back discards it, and a reload reopens what the address names.
test('Back and reload move between the Script builder, Recording Studio and drafts', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  await page.setViewportSize({ width: 1600, height: 1000 })
  const workspace = await createWorkspace(page.context().request)
  const route = () => new URL(page.url()).hash
  await page.goto(`/#/scripts?mode=list&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()

  await page.getByRole('button', { name: 'Script builder', exact: true }).first().click()
  const workflows = page.getByRole('tablist', { name: 'Script creation workflows' })
  await expect(workflows.getByRole('tab', { name: 'Create', exact: true })).toHaveAttribute('aria-selected', 'true')
  await page.getByRole('tab', { name: /Starter Templates/ }).click()
  await expect.poll(route).toContain('category=templates')

  // Recording Studio is a step: Back returns to the builder's tab.
  await page.getByRole('button', { name: 'Record', exact: true }).click()
  await expect.poll(route).toContain('mode=record')
  await page.goBack()
  await expect.poll(route).toContain('mode=create')
  await expect(page.getByRole('tab', { name: /Starter Templates/ })).toHaveAttribute('aria-selected', 'true')

  // An importer is a step too: Back returns to the builder's tab.
  await page.getByRole('tab', { name: 'Import', exact: true }).click()
  await expect.poll(route).toContain('workflow=source')
  await page.getByRole('button', { name: /OpenAPI \/ Swagger/ }).click()
  await expect.poll(route).toContain('launcher=openapi')
  await page.goBack()
  await expect.poll(route).toContain('workflow=source')
  await expect(page.getByRole('tab', { name: 'Import', exact: true })).toHaveAttribute('aria-selected', 'true')
  // The importer's own Back goes back to that same entry.
  await page.getByRole('button', { name: /OpenAPI \/ Swagger/ }).click()
  await page.getByRole('button', { name: 'Back', exact: true }).click()
  await expect.poll(route).toContain('workflow=source')
  // A tab replaces the builder's entry rather than adding one.
  await page.getByRole('tab', { name: 'Create', exact: true }).click()
  await expect.poll(route).toContain('category=templates')

  // A template draft is unsaved content: Back asks before discarding it.
  await page.getByRole('button', { name: 'Use HTTP API Smoke' }).click()
  await expect.poll(route).toContain('mode=draft')
  await expect(page.getByRole('textbox', { name: 'Script name', exact: true })).toHaveValue('HTTP API Smoke')
  await page.goBack()
  const leave = page.getByRole('dialog', { name: /Unsaved changes/i })
  await expect(leave).toBeVisible()
  await leave.getByRole('button', { name: 'Keep editing', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Close script editor' })).toBeVisible()
  await expect.poll(route).toContain('mode=draft')
  // Close goes back to the builder entry, not on top of it.
  await page.getByRole('button', { name: 'Close script editor' }).click()
  await page.getByRole('button', { name: 'Discard changes', exact: true }).click()
  await expect.poll(route).toContain('category=templates')
  await page.goBack()
  await expect.poll(route).toContain('mode=list')

  // Forward to the closed draft opens the builder, and so does a reload of one.
  await page.goForward()
  await page.goForward()
  await expect.poll(route).toContain('mode=create')
  await expect(page.getByRole('button', { name: 'Close script editor' })).toHaveCount(0)

  // A reload reopens Recording Studio with its method.
  await page.goto(`/#/scripts?mode=record&method=browser&${scopeQuery(workspace)}`)
  await page.reload()
  await expect(page.getByRole('heading', { name: 'Record Browser Session' })).toBeVisible()
})
