import path from 'node:path'
import { test, expect, mutation, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

test('imports an unpacked VuGen project folder', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('button', { name: 'Import', exact: true }).click()
  await page.getByRole('button', { name: /LoadRunner Script Archive/ }).click()

  const folder = path.join(process.cwd(), '../pkg/convert/enterprisearchive/testdata/golden_web_http_html_vugen24')
  await page.locator('input[webkitdirectory]').setInputFiles(folder)
  await expect(page.getByText('1 source queued').first()).toBeVisible()
  await expect(page.getByRole('button', { name: 'Choose VuGen folder' })).toBeVisible()

  const validated = mutation(page, 'POST', '/api/v1/scripts/import/enterprise-script-archive/validate')
  await page.getByRole('button', { name: 'Validate Script(s)', exact: true }).click()
  const response = await validated
  expect(response.ok()).toBeTruthy()
  const result = await response.json()
  expect(result.script?.name).toBe('VuGen24Real')
  await expect(page.getByRole('tab', { name: /^Review(,|$)/ })).toBeVisible()
})
