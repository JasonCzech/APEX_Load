import path from 'node:path'
import { readFile } from 'node:fs/promises'
import { test, expect, mutation, json, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

for (const scenario of [
  { file: 'transaction-labels.zip', text: 'Ready to save' },
  { file: 'credential-groups.zip', text: 'Ready to save' },
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
    const conversionOptions = page.locator('#migration-block-policy-options')
    const salesforceOptions = conversionOptions.locator('.lr-advanced-policy + .lr-har-panel')
    await expect(salesforceOptions.locator('summary')).toHaveText('Salesforce-specific options')
    await salesforceOptions.locator('summary').click()
    await expect(salesforceOptions.getByRole('checkbox', { name: 'Build Salesforce native preview' })).toBeVisible()
    await expect(salesforceOptions.getByRole('button', { name: 'Attach HAR' })).toBeVisible()
    await expect(page.locator('.lr-import-actions-card .lr-har-panel')).toHaveCount(0)
    await salesforceOptions.locator('summary').click()
    if (scenario.file === 'transaction-labels.zip') {
      const advancedPolicy = conversionOptions.locator('.lr-advanced-policy')
      await advancedPolicy.locator('summary').click()
      const expireDraft = advancedPolicy.getByRole('checkbox', { name: 'Expire converted drafts after 30 minutes' })
      await expect(expireDraft).not.toBeChecked()
      await expireDraft.check()
      await expect(expireDraft).toBeChecked()
    }
    await page.locator('input[type=file][accept=".zip"]').setInputFiles(path.join(process.cwd(), 'e2e/fixtures', scenario.file))
    await expect(page.getByRole('button', { name: 'Add archives', exact: true })).toBeVisible()
    const validated = mutation(page, 'POST', '/api/v1/scripts/import/enterprise-script-archive/validate')
    await page.getByRole('button', { name: 'Validate Script(s)', exact: true }).click()
    const validatedResponse = await validated
    const result = await json(validatedResponse)
    await expect(page.getByRole('tab', { name: 'Review', exact: true })).toBeVisible()
    await page.getByRole('tab', { name: 'DSL Preview', exact: true }).click()
    const dslPreview = page.locator('.script-import-review-preview')
    await expect(dslPreview).toHaveAttribute('open', '')
    const dslEditor = dslPreview.getByRole('region', { name: 'DSL Preview JSON' })
    // CodeMirror virtualizes off-screen lines, so assert the visible DSL opening
    // instead of relying on the full document being in the DOM like <pre> was.
    await expect(dslEditor.locator('.cm-content')).toContainText('"actions": [')
    await expect(dslEditor).not.toContainText('"script": {')
    await expect(dslEditor.locator('.apex-dsl-section-key').first()).toBeVisible()
    if (scenario.file === 'transaction-labels.zip') await page.screenshot({ path: testInfo.outputPath('tab-dsl.png'), fullPage: true })
    await dslPreview.getByRole('combobox', { name: 'Section', exact: true }).selectOption('config')
    await expect(dslEditor.locator('.cm-content')).toContainText('"config"')
    await dslPreview.getByRole('combobox', { name: 'Section', exact: true }).selectOption('actions')
    await page.getByRole('tab', { name: 'Data & parameters', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Imported parameters & data', exact: true })).toBeVisible()
    if (scenario.file === 'transaction-labels.zip') await page.screenshot({ path: testInfo.outputPath('tab-data.png'), fullPage: true })
    await page.getByRole('tab', { name: 'Runtime & source settings', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Runtime & source settings', exact: true })).toBeVisible()
    if (scenario.file === 'transaction-labels.zip') await page.screenshot({ path: testInfo.outputPath('tab-runtime.png'), fullPage: true })
    await page.getByRole('tab', { name: 'Source Mapping', exact: true }).click()
    await expect(page.getByRole('navigation', { name: 'Review mapping navigation' })).toBeVisible()
    const sourcePane = page.getByRole('region', { name: 'Generated DSL', exact: true })
    if (await sourcePane.count()) {
      await sourcePane.scrollIntoViewIfNeeded()
      await expect(sourcePane.locator('.cm-content')).toBeVisible()
      await expect(sourcePane.locator('.cm-content')).not.toBeEmpty()
      await page.getByRole('button', { name: 'Focus destination', exact: true }).click()
      await expect(sourcePane.locator('.cm-content')).toBeVisible()
      await page.getByRole('button', { name: 'Return to comparison', exact: true }).click()
      await expect(sourcePane.locator('.cm-content')).toBeVisible()
    }
    if (scenario.file === 'transaction-labels.zip') await page.screenshot({ path: testInfo.outputPath('tab-source.png'), fullPage: true })
    await page.getByRole('tab', { name: 'Review', exact: true }).click()
    await expect(page.getByRole('tab', { name: 'Conversion Complete', exact: true })).toHaveCount(0)
    await expect(page.getByText(scenario.text, { exact: true }).first()).toBeVisible()
    await expect(page.getByRole('region', { name: 'Guided import repair' })).toBeVisible()
    await expect(page.locator('.lr-completed-setup')).not.toHaveAttribute('open')
    await expect(page.getByText(/Imported —/).first()).toBeVisible()
    if (scenario.file === 'credential-groups.zip') {
      const requirements = result.secret_binding_requirements as { value_group_id?: string }[]
      expect(requirements.length).toBeGreaterThan(1)
      expect(new Set(requirements.map(requirement => requirement.value_group_id)).size).toBe(1)
      expect(requirements.every(requirement => requirement.value_group_id)).toBe(true)
      await page.getByRole('tab', { name: 'Credentials', exact: true }).click()
      await expect(page.getByRole('heading', { name: 'Credential groups (1)', exact: true })).toBeVisible()
      const control = page.locator('.secret-binding-review-control')
      await expect(control).not.toHaveAttribute('open')
      await page.getByRole('heading', { name: 'Credential groups (1)', exact: true }).scrollIntoViewIfNeeded()
      await page.screenshot({ path: testInfo.outputPath('credential-groups.png'), fullPage: true })
      await page.getByRole('searchbox', { name: 'Search credential locations' }).fill('no-match')
      await expect(page.getByText('No credential groups match this filter.')).toBeVisible()
      await page.getByRole('tab', { name: 'DSL Preview', exact: true }).click()
      await expect(page.getByRole('heading', { name: 'Credential groups (1)', exact: true })).not.toBeVisible()
      await expect(page.locator('.script-import-review-preview')).toHaveAttribute('open', '')
      const download = page.waitForEvent('download')
      await page.getByRole('button', { name: 'Download complete protected JSON' }).click()
      const savedDownload = await download
      const { readFile } = await import('node:fs/promises')
      const reviewText = await readFile((await savedDownload.path())!, 'utf8')
      const review = JSON.parse(reviewText)
      expect(review.script.actions.length).toBeGreaterThanOrEqual(2)
      expect(reviewText).not.toContain('test-only-cookie')
      expect(JSON.stringify(result.summary)).not.toContain('Anypoint Client ID Enforcement')
    }
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

// The real converter and store must persist every ready archive even when
// another ZIP fails validation. This runs under dev-admin and local-auth.
test('LoadRunner batch saves ready scripts, stays on import, and accepts another ZIP', async ({ page }, testInfo) => {
  test.setTimeout(120_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const folder = await json(await request.post(`/api/v1/projects/${workspace.projectId}/folders`, {
    data: { name: 'Imported batch', category: 'scripts' },
  }))
  const folderId = String(folder.id)
  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('button', { name: 'Import', exact: true }).click()
  await page.getByRole('button', { name: /Import Loadrunner Script Archive/ }).click()
  const destination = page.getByRole('combobox', { name: 'Destination folder for saved scripts' })
  await destination.selectOption(folderId)
  const policy = page.locator('.lr-advanced-policy input[type=checkbox]')
  await expect(policy).toHaveCount(3)
  for (let index = 0; index < 3; index += 1) await expect(policy.nth(index)).not.toBeChecked()
  const { readFile } = await import('node:fs/promises')
  const buffer = await readFile(path.join(process.cwd(), 'e2e/fixtures/transaction-labels.zip'))
  const archiveInput = page.locator('input[type=file][accept=".zip"]')
  await archiveInput.setInputFiles([
    { name: 'first.zip', mimeType: 'application/zip', buffer },
    { name: 'second.zip', mimeType: 'application/zip', buffer },
    { name: 'broken.zip', mimeType: 'application/zip', buffer: Buffer.from('not a ZIP') },
  ])
  await page.getByRole('button', { name: 'Validate 3 Scripts', exact: true }).click()
  await expect(page.locator('.lr-file-row.failed')).toHaveCount(1)
  const saved = mutation(page, 'POST', '/api/v1/scripts/import/enterprise-script-archive/save')
  if (await page.locator('.lr-completed-setup').getAttribute('open') !== '') await page.locator('.lr-completed-setup > summary').click()
  await page.locator('.lr-import-actions-card').getByRole('button', { name: 'Save reviewed drafts', exact: true }).click()
  const receipt = await json(await saved)
  const scripts = receipt.scripts as { id: string; name: string }[]
  expect(scripts).toHaveLength(2)
  expect(new Set(scripts.map(script => script.id)).size).toBe(2)
  await expect(page.getByRole('button', { name: 'Import More Files', exact: true })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Author center workbench' })).toHaveCount(0)
  await expect(destination).toHaveValue(folderId)
  const library = await json(await request.get(`/api/v1/scripts?project_id=${workspace.projectId}`))
  expect((library.scripts as { id: string; folder_id: string }[]).filter(script => script.folder_id === folderId).map(script => script.id).sort()).toEqual(scripts.map(script => script.id).sort())

  const picker = page.waitForEvent('filechooser')
  await page.getByRole('button', { name: 'Import More Files', exact: true }).click()
  await (await picker).setFiles({ name: 'third.zip', mimeType: 'application/zip', buffer })
  await expect(page.locator('.lr-file-row')).toHaveCount(4)
  if (!(await page.locator('.lr-completed-setup').getAttribute('open') === '')) await page.locator('.lr-completed-setup > summary').click()
  await page.getByRole('button', { name: 'Validate 4 Scripts', exact: true }).click()
  await expect(page.locator('.lr-file-row.validated')).toHaveCount(1)
  const nextSave = mutation(page, 'POST', '/api/v1/scripts/import/enterprise-script-archive/save')
  if (await page.locator('.lr-completed-setup').getAttribute('open') !== '') await page.locator('.lr-completed-setup > summary').click()
  await page.locator('.lr-import-actions-card').getByRole('button', { name: 'Save reviewed drafts', exact: true }).click()
  expect((await json(await nextSave)).scripts).toHaveLength(1)
  await expect(page.getByRole('button', { name: 'Import More Files', exact: true })).toBeVisible()
  expect((await json(await request.get(`/api/v1/scripts?project_id=${workspace.projectId}`))).scripts).toHaveLength(3)
  await page.getByRole('button', { name: 'Import More Files', exact: true }).scrollIntoViewIfNeeded()
  await page.screenshot({ path: testInfo.outputPath('batch-import-saved.png'), fullPage: true })
  await page.getByRole('button', { name: `Open ${scripts[0].name} in Visual Editor`, exact: true }).click()
  await expect(page.getByRole('region', { name: 'Author center workbench' })).toBeVisible()
  const authorPanes = page.getByRole('group', { name: 'Author panes' })
  if (await authorPanes.isVisible()) await authorPanes.getByRole('button', { name: 'Outline', exact: true }).click()
  await expect(page.getByRole('textbox', { name: 'Script Name', exact: true })).toHaveValue(scripts[0].name)
  await expect(page.getByRole('button', { name: 'Save script', exact: true })).toBeDisabled()
})

// The encrypted-session lifecycle is covered by API tests. This browser test
// isolates the review interaction with typed redacted repair responses.
test('LoadRunner guided repair previews redacted changes before applying', async ({ page }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  let draft: Record<string, any> = {}
  const findings = [{ code: 'restored_body_required', action_path: 'actions[0]', source_ref: 'Action.c:2', detail: 'Supply a restored request body.', gate: 'execution' }]
  const expiry = new Date(Date.now() + 20 * 60_000).toISOString()
  await page.route('**/api/v1/scripts/import/enterprise-script-archive/validate', async route => {
    const response = await page.context().request.post(`/api/v1/scripts/import/enterprise-script-archive/validate?project_id=${workspace.projectId}`, { multipart: { file: { name: 'guided-repair.zip', mimeType: 'application/zip', buffer: await readFile(path.join(process.cwd(), 'e2e/fixtures/guided-repair.zip')) } } })
    expect(response.ok(), await response.text()).toBeTruthy()
    const result = await response.json()
    draft = result.script
    draft.metadata.import.preserved.server_import_attestation_v1 = 'protected:browser-repair-one'
    if (result.scripts?.length) result.scripts[0] = draft
    result.import_draft_expires_at = expiry
    await route.fulfill({ response, json: result })
  })
  let applies = 0
  await page.route('**/api/v1/scripts/import/repairs/*', async route => {
    const req = route.request().postDataJSON()
    const inspect = req.operation.type === 'inspect'
    const apply = route.request().url().endsWith('/apply')
    if (apply) { applies++; expect(req.operation.body).toBe('restored test body') }
    const revised = JSON.parse(JSON.stringify(draft))
    if (!inspect) revised.actions[0].body = '[REDACTED]'
    if (apply || applies) revised.metadata.import.preserved.server_import_attestation_v1 = 'protected:browser-repair-two'
    await route.fulfill({ json: { candidate_id: apply ? 'protected:browser-repair-two' : req.candidate_id, revision: apply ? 'protected:browser-repair-two' : req.revision, before: draft, script: revised, findings: inspect && !applies ? findings : [], readiness: { replay_ready: !inspect || !!applies, promotion_ready: !inspect || !!applies, blocking_codes: inspect && !applies ? ['restored_body_required'] : [] }, secret_binding_requirements: [], fidelity_report: { promotion_status: inspect && !applies ? 'blocked' : 'promotable', blockers: inspect && !applies ? ['restored_body_required'] : [] }, expires_at: expiry } })
  })
  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('button', { name: 'Import', exact: true }).click()
  await page.getByRole('button', { name: /Import Loadrunner Script Archive/ }).click()
  await page.locator('input[type=file][accept=".zip"]').setInputFiles(path.join(process.cwd(), 'e2e/fixtures/guided-repair.zip'))
  await page.getByRole('button', { name: 'Validate Script(s)', exact: true }).click()
  const repair = page.getByRole('region', { name: 'Guided import repair' })
  await expect(repair).toBeVisible()
  await repair.getByRole('button', { name: /Restore protected request body/ }).click()
  await repair.getByLabel('Repair value').fill('restored test body')
  await expect(repair.getByRole('button', { name: 'Apply reviewed repair' })).toHaveCount(0)
  await repair.getByRole('button', { name: 'Preview repair' }).click()
  await repair.getByText('Redacted before / after', { exact: true }).click()
  await expect(repair.locator('pre')).toContainText('[REDACTED]')
  await expect(repair.locator('pre')).not.toContainText('restored test body')
  expect(applies).toBe(0)
  await repair.getByRole('button', { name: 'Apply reviewed repair' }).click()
  await expect(page.getByText('Repair applied. Requirements were refreshed from the protected draft.', { exact: true })).toBeVisible()
  expect(applies).toBe(1)
  await repair.getByRole('combobox', { name: 'Show', exact: true }).selectOption('resolved')
  await expect(repair.getByRole('button', { name: /Restore protected request body Resolved/ })).toBeVisible()
  await page.getByRole('tab', { name: 'Data & parameters', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Unique parameter allocation' })).toBeVisible()
  await expect(page.getByRole('combobox', { name: 'Run mode', exact: true })).toHaveValue('duration')
})

// Private acceptance archives stay local; CI uses the sanitized fixtures above.
test('LoadRunner private acceptance archive groups repeated findings without hiding source locations', async ({ page }, testInfo) => {
  const archive = process.env.APEXLOAD_LR_ACCEPTANCE_ARCHIVE
  test.skip(!archive, 'Set APEXLOAD_LR_ACCEPTANCE_ARCHIVE for local archive acceptance.')
  test.setTimeout(180_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('button', { name: 'Import', exact: true }).click()
  await page.getByRole('button', { name: /Import Loadrunner Script Archive/ }).click()
  await page.locator('input[type=file][accept=".zip"]').setInputFiles(archive!)
  const validated = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/scripts/import/enterprise-script-archive/validate', { timeout: 120_000 })
  await page.getByRole('button', { name: 'Validate Script(s)', exact: true }).click()
  expect((await validated).ok()).toBe(true)
  await expect(page.locator('.lr-stat').filter({ hasText: 'Raw Requests' })).toContainText('492')
  const repair = page.getByRole('region', { name: 'Guided import repair' })
  await expect(repair.getByRole('button', { name: /Missing value: c_transaction1/ })).toHaveCount(1)
  await repair.getByRole('button', { name: /Missing value: c_transaction1/ }).click()
  await expect(repair.getByLabel('Repair operation')).toHaveValue('bind_variable')
  await expect(repair.getByLabel('Repair operation').locator('option[value=restore_body]')).toHaveCount(0)
  await expect(repair.getByLabel('Repair finding').locator('option')).not.toHaveCount(1)
  await repair.getByText(/Affected requests and source evidence/).click()
  await expect(repair.getByRole('button', { name: 'View source', exact: true }).first()).toBeVisible()
  await repair.getByRole('button', { name: 'View source', exact: true }).first().click()
  await expect(page.getByRole('tab', { name: 'Source Mapping', exact: true })).toHaveAttribute('aria-selected', 'true')
  await page.getByRole('button', { name: 'Back to issue queue', exact: true }).click()
  await expect(repair.getByRole('button', { name: /Missing value: c_transaction1/ })).toHaveAttribute('aria-pressed', 'true')
  await repair.getByRole('searchbox', { name: 'Find an issue' }).fill('tstamp')
  await expect(repair.locator('.lr-issue-row')).toHaveCount(1)
  await repair.getByRole('button', { name: /Missing value: tstamp/ }).click()
  await expect(repair.getByLabel('Repair operation')).toHaveValue('bind_variable')
  await repair.getByRole('searchbox', { name: 'Find an issue' }).fill('')
  await page.getByRole('heading', { name: 'Resolve import issues', exact: true }).scrollIntoViewIfNeeded()
  await page.screenshot({ path: testInfo.outputPath('acceptance-grouped-review.png'), fullPage: true })
  await repair.evaluate(element => element.scrollIntoView({ block: 'start' }))
  await page.screenshot({ path: testInfo.outputPath('acceptance-issue-workspace.png'), fullPage: true })
  await page.setViewportSize({ width: 768, height: 1024 })
  await repair.getByRole('heading', { name: 'Missing value: tstamp', exact: true }).scrollIntoViewIfNeeded()
  await page.screenshot({ path: testInfo.outputPath('acceptance-tablet-review.png'), fullPage: true })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
})
