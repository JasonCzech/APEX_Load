import path from 'node:path'
import { expect, test, type Response } from '@playwright/test'
import { createWorkspace, scopeQuery } from './support/workspace'

const fixturePath = path.join(process.cwd(), 'e2e', 'fixtures', 'tier0-orders.openapi.yaml')

function isImportResponse(response: Response, endpoint: string): boolean {
  const url = new URL(response.url())
  return response.request().method() === 'POST' && url.pathname === endpoint
}

function record(value: unknown, what: string): Record<string, unknown> {
  if (!value || typeof value !== 'object') {
    throw new Error(`${what} was not an object: ${JSON.stringify(value)}`)
  }
  return value as Record<string, unknown>
}

// Tier-0 journey step S3 (docs/qa/TIER0_JOURNEY.md): import a small,
// dependency-free OpenAPI contract, validate the generated script, commit it,
// and prove that the stored result is visible in the project Script Library.
test('[U05] OpenAPI validation stays dry-run until the reviewed script is committed to the library', async ({ page, request }) => {
  test.setTimeout(120_000)
  const workspace = await createWorkspace(request)

  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  await page.getByRole('button', { name: 'Import', exact: true }).click()
  await page.getByRole('button', { name: /OpenAPI \/ Swagger/ }).click()

  const importControls = page.getByRole('group', { name: 'Script import controls' })
  await expect(importControls.getByRole('heading', { level: 2, name: 'Import OpenAPI' })).toBeVisible()
  await importControls.locator('input[type="file"]').setInputFiles(fixturePath)
  await importControls.getByRole('textbox', { name: 'Script Name' }).fill('Tier-0 Orders API')

  const validationResponsePromise = page.waitForResponse(response => (
    isImportResponse(response, '/api/v1/scripts/import/openapi')
  ))
  await importControls.getByRole('button', { name: 'Validate Only' }).click()
  const validationResponse = await validationResponsePromise
  const validationText = await validationResponse.text()
  expect(validationResponse.ok(), validationText).toBeTruthy()
  const validationPayload = record(JSON.parse(validationText), 'OpenAPI validation response')
  const validationScript = record(validationPayload.script, 'validated OpenAPI script')
  expect(validationScript.actions).toEqual(expect.arrayContaining([
    expect.objectContaining({ name: 'listOrders', method: 'GET', target: '/orders' }),
  ]))

  const validationReview = importControls.getByRole('heading', { level: 3, name: 'Validation Review' })
  await expect(validationReview).toBeVisible({ timeout: 15_000 })
  await expect(importControls.getByText(/dry-run validation; nothing has been saved/i)).toBeVisible()
  await expect(importControls.getByText('Preflight').locator('..').getByText('Ready with notes', { exact: true })).toBeVisible()
  await expect(importControls.getByRole('cell', { name: 'GET /orders' })).toBeVisible()

  const conversionResponsePromise = page.waitForResponse(response => (
    isImportResponse(response, '/api/v1/scripts/import/openapi')
  ))
  await importControls.getByRole('button', { name: 'Convert OpenAPI' }).click()
  const conversionResponse = await conversionResponsePromise
  expect(conversionResponse.ok(), await conversionResponse.text()).toBeTruthy()
  await expect(importControls.getByRole('heading', { level: 3, name: 'Review Generated Script' })).toBeVisible({ timeout: 15_000 })
  await expect(importControls.getByText('1 action(s) generated from OpenAPI operations.', { exact: false })).toBeVisible()

  // Validation and conversion share the protected inspection endpoint. Check
  // after both modes so either one accidentally persisting is caught; only the
  // explicit commit endpoint below may create the library entry.
  const beforeCommit = await request.get(`/api/v1/scripts?project_id=${workspace.projectId}`)
  const beforeCommitText = await beforeCommit.text()
  expect(beforeCommit.ok(), beforeCommitText).toBeTruthy()
  expect(beforeCommitText).not.toContain('Tier_0_Orders_API')

  const commitResponsePromise = page.waitForResponse(response => (
    isImportResponse(response, '/api/v1/scripts/import/openapi/commit')
  ))
  await importControls.getByRole('button', { name: 'Save To Script Library' }).click()
  const commitResponse = await commitResponsePromise
  const commitText = await commitResponse.text()
  expect(commitResponse.ok(), commitText).toBeTruthy()

  const commitPayload = record(JSON.parse(commitText), 'OpenAPI commit response')
  const stored = record(commitPayload.stored_script, 'OpenAPI stored script')
  expect(stored.project_id).toBe(workspace.projectId)
  expect(stored.name).toBe('Tier_0_Orders_API')
  expect(typeof stored.id).toBe('string')

  // The import callback opens the new script for editing. Return through the
  // actual Scripts navigation so the final assertion covers the user-facing
  // library refresh, not only the commit response.
  // Clicking the already-active Scripts destination intentionally preserves
  // its editor state. Leave the section first, then return as a user would.
  await page.getByRole('button', { name: 'Dashboard', exact: true }).click()
  await page.getByRole('button', { name: 'Scripts', exact: true }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Script Library' })).toBeVisible({ timeout: 15_000 })
  await page.getByRole('searchbox', {name:'Search scripts',exact:true}).fill('Tier_0_Orders_API')
  await expect(page.getByRole('button', { name: 'Tier_0_Orders_API', exact: true })).toBeVisible()
})
