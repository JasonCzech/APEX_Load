import path from 'node:path'
import { test, expect, mutation, json, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

for (const scenario of [
  { file: 'transaction-labels.zip', text: 'Ready to save' },
  { file: 'java-lifecycle.zip', text: 'Needs runner / approval' },
  { file: 'sap-review.zip', text: 'Review only' },
  { file: 'truclient-review.zip', text: 'TruClient 2 Compatibility' },
]) {
  test(`LoadRunner review renders real ${scenario.file} response`, async ({ page }, testInfo) => {
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
    const request = page.context().request
    const workspace = await createWorkspace(request)
    await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
    // API seeding happens after login; reload the cached workspace hierarchy.
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
    await page.getByRole('button', { name: 'Import', exact: true }).click()
    await page.getByRole('button', { name: /Import Loadrunner Script Archive/ }).click()
    await page.locator('input[type=file][accept=".zip"]').setInputFiles(path.join(process.cwd(), 'e2e/fixtures', scenario.file))
    await expect(page.getByRole('button', { name: 'Add archives', exact: true })).toBeVisible()
    const validated = mutation(page, 'POST', '/api/v1/scripts/import/enterprise-script-archive/validate')
    await page.getByRole('button', { name: 'Validate Script(s)', exact: true }).click()
    const result = await json(await validated)
    await expect(page.getByRole('tab', { name: 'Review', exact: true })).toBeVisible()
    await expect(page.getByRole('tab', { name: 'Conversion Complete', exact: true })).toHaveCount(0)
    await expect(page.getByText(scenario.text, { exact: true }).first()).toBeVisible()
    const checks = page.locator('.lr-result .replay-preflight-checks')
    await expect(checks).toHaveCSS('display', 'grid')
    await expect(page.locator('.lr-result .replay-preflight-check').filter({ hasText: 'Runtime prerequisites' })).toContainText('Not verified')
    if (scenario.file === 'truclient-review.zip') {
      await expect(page.getByRole('button', { name: 'Continue to browser replay', exact: true })).toBeVisible()
      expect(typeof (result.trueclient2_compat_report as Record<string, unknown>).fixture_contract_status).toBe('object')
      await expect(page.getByText('Authoring tools could not be loaded.')).toHaveCount(0)
      await page.getByText('Compatibility evidence', { exact: true }).click()
      await expect(page.getByText(/web2 spa checkout:/)).toBeVisible()
    }
    if (scenario.file === 'sap-review.zip') {
      const requirements = JSON.stringify(result.secret_binding_requirements || [])
      expect(requirements).not.toContain('/config/sapgui/session_behavior')
      await expect(page.getByText('Execution blocked', { exact: true })).toBeVisible()
    }
    if (scenario.file === 'java-lifecycle.zip') {
      await expect(page.getByRole('button', { name: 'Save Combined Java Draft for Approval', exact: true })).toBeVisible()
      await expect(page.getByText('Execution blocked', { exact: true })).toBeVisible()
    }
    await page.screenshot({ path: testInfo.outputPath('loadrunner-review.png'), fullPage: true })
  })
}
