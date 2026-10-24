import { canonicalImportDraft } from './support/importResponse'
import path from 'node:path'
import { test, expect, json, mutation } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

// Long mixed-case business identifiers previously triggered the generic token
// heuristic inside transaction arguments and nested request metadata.
test('transaction names survive archive import without requiring secret bindings', async ({ page, request }, testInfo) => {
  test.setTimeout(120_000)
  const label = 'B2B_Checkout_ApexActionController_ServiceAddress_GetSearchResults_2026'
  const workspace = await createWorkspace(request)
  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  await page.getByRole('tab', { name: 'Import', exact: true }).click()
  await page.getByRole('button', { name: /LoadRunner Script Archive/ }).click()
  await page.locator('input[type=file][accept=".zip,.usz"]').setInputFiles(path.join(process.cwd(), 'e2e/fixtures/transaction-labels.zip'))
  const validated = mutation(page, 'POST', '/api/v1/scripts/import/enterprise-script-archive/validate')
  await page.getByRole('button', { name: 'Validate Script', exact: true }).click()
  const result = await json(await validated)
  expect(result.secret_binding_requirements || []).toEqual([])
  const script = canonicalImportDraft(result)
  const markers = script.actions.filter(action => action.type === 'source_transaction')
  expect(markers).toHaveLength(2)
  expect(markers.every(action => (action.params?.args as string[] | undefined)?.[0] === label)).toBe(true)
  await expect(page.locator('.lr-tab-readiness')).toContainText('0 block replay')
  await expect(page.locator('.secret-binding-review')).toHaveCount(0)
  await page.getByRole('tab', { name: 'DSL Preview', exact: true }).click()
  await expect(page.getByRole('tabpanel', { name: 'DSL Preview' })).toContainText(label)
  const saved = mutation(page, 'POST', '/api/v1/scripts/import/enterprise-script-archive/save')
  await page.locator('.script-import-review-actions').getByRole('button', { name: 'Save draft', exact: true }).click()
  const savedResult = await json(await saved)
  const scriptId = (savedResult.scripts as { id: string }[])[0].id
  await expect(page.getByRole('button', { name: 'Import More Files', exact: true })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Author center workbench' })).toHaveCount(0)
  await page.goto(`/#/scripts?mode=edit&scriptId=${scriptId}&${scopeQuery(workspace)}`)
  await expect(page.getByRole('region', { name: 'Author center workbench' })).toContainText(label)
  await page.screenshot({ path: testInfo.outputPath('imported-transaction-names.png'), fullPage: true })
})
