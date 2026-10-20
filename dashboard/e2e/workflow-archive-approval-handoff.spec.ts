import type { Page } from '@playwright/test'
import { test, expect, mutation, json, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'
import { storedZip } from './support/zip'
import { canonicalImportDraft } from './support/importResponse'

async function approvalHandoff(page: Page, laterEdits: boolean) {
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
    const request = page.context().request
    const workspace = await createWorkspace(request)
    // Model a legacy peer's plain import response. Current protected imports
    // keep their save-before-edit gate (covered in workflow-loadrunner-review).
    // Conversion and approval persistence still use the real local controller.
    await page.route('**/api/v1/scripts/import/enterprise-script-archive/validate', async route => {
      const response = await route.fetch()
      expect(response.ok()).toBe(true)
      const payload = await response.json()
      const draft = canonicalImportDraft(payload)
      expect(payload.secret_binding_requirements || []).toHaveLength(0)
      delete payload.import_draft_expires_at
      if (draft.metadata?.import?.preserved) delete draft.metadata.import.preserved.server_import_attestation_v1
      await route.fulfill({ response, json: payload })
    })
    const archive = storedZip([
      { name: 'Plain.usr', content: '[General]\nType=Web\n[Actions]\nAction1=Action.c\n' },
      { name: 'Action.c', content: 'Action()\n{\nweb_url("Home", "URL=https://example.invalid/home", LAST);\nreturn 0;\n}' },
    ])
    await page.goto(`/#/scripts?mode=import&${scopeQuery(workspace)}`)
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
    await page.locator('input[type=file][accept=".zip,.usz"]').setInputFiles([
      { name: 'first.zip', mimeType: 'application/zip', buffer: archive },
      { name: 'sibling.zip', mimeType: 'application/zip', buffer: archive },
    ])
    await page.getByRole('button', { name: 'Validate 2 Scripts', exact: true }).click()
    await page.getByRole('button', { name: 'Open draft in Visual Editor', exact: true }).click()
    await page.getByRole('region', { name: 'Author center workbench' }).getByRole('tab', { name: 'DSL', exact: true }).click()
    await page.getByRole('button', { name: 'Edit JSON', exact: true }).click()
    await page.locator('.script-code-pane .cm-content[contenteditable="true"]').fill(JSON.stringify({
      name: 'Queued Java draft', version: '1.0', protocol: 'java_vuser', config: { base_url: '' },
      actions: [{ name: 'Java action', type: 'java_vuser_run', target: '', params: { method: 'action' } }],
    }, null, 2))
    await page.getByRole('button', { name: 'Apply changes', exact: true }).click()
    let releaseResponse = () => {}
    let responseCreated = () => {}
    const created = new Promise<void>(resolve => { responseCreated = resolve })
    const release = new Promise<void>(resolve => { releaseResponse = resolve })
    if (laterEdits) {
      await page.route('**/api/v1/scripts/authoring/sessions', async route => {
        if (route.request().method() !== 'POST') return route.continue()
        const response = await route.fetch()
        responseCreated()
        await release
        await route.fulfill({ response })
      }, { times: 1 })
    }
    const queued = mutation(page, 'POST', '/api/v1/scripts/authoring/sessions')
    await page.getByRole('button', { name: 'Save script', exact: true }).click()
    if (laterEdits) {
      try {
        await created
        const name = page.getByRole('textbox', { name: 'Script name', exact: true })
        if (!await name.isVisible()) await page.getByRole('group', { name: 'Author panes' }).getByRole('button', { name: 'Outline', exact: true }).click()
        await name.fill('Later Java edits')
      } finally { releaseResponse() }
    }
    const session = await json(await queued)
    expect(session.id).toBeTruthy()
    const reopened = await json(await request.get(`/api/v1/scripts/authoring/sessions/${session.id}`))
    expect(reopened.draft).toMatchObject({ name: 'Queued Java draft', protocol: 'java_vuser' })
    if (laterEdits) {
      await expect(page.getByRole('region', { name: 'Author center workbench' })).toBeVisible()
      await expect(page.getByRole('textbox', { name: 'Script name', exact: true })).toHaveValue('Later Java edits')
      await expect(page.locator('.editor-toast.success')).toBeVisible()
      const latestQueued = mutation(page, 'POST', '/api/v1/scripts/authoring/sessions')
      await page.getByRole('button', { name: 'Save script', exact: true }).click()
      const latest = await json(await latestQueued)
      expect(latest.id).not.toBe(session.id)
      expect((await json(await request.get(`/api/v1/scripts/authoring/sessions/${latest.id}`))).draft).toMatchObject({ name: 'Later Java edits' })
    }
    await expect(page.getByRole('region', { name: 'Author center workbench' })).toHaveCount(0)
    await expect(page.getByText(/It is not in the script library yet/)).toBeVisible()
    await expect(page.getByRole('button', { name: 'Queued for approval', exact: true })).toBeDisabled()
    await expect(page.getByRole('status').filter({ hasText: 'approval session' })).toContainText('1 approval session queued')
    await expect(page.getByText('sibling.zip', { exact: true }).first()).toBeVisible()
    expect((await json(await request.get(`/api/v1/scripts?project_id=${workspace.projectId}`))).scripts).toHaveLength(0)
    await expect(page.getByRole('dialog')).toHaveCount(0)
    // Genuine navigation must still guard the pending sibling after the return.
    await page.getByRole('navigation', { name: 'Primary navigation' }).getByRole('button', { name: 'Dashboard', exact: true }).click()
    const leave = page.getByRole('dialog').filter({ has: page.getByRole('heading', { name: 'Unsaved Changes', exact: true }) })
    await expect(leave).toContainText('LoadRunner import review')
    await leave.getByRole('button', { name: 'Keep editing', exact: true }).click()
    // Saving the pending sibling must not save the original queued lane again.
    await page.getByRole('button', { name: /^sibling\.zip / }).click()
    const siblingSaved = mutation(page, 'POST', '/api/v1/scripts/import/enterprise-script-archive/save')
    await page.getByRole('button', { name: 'Save reviewed drafts', exact: true }).click()
    await json(await siblingSaved)
    const library = await json(await request.get(`/api/v1/scripts?project_id=${workspace.projectId}`))
    expect(library.scripts).toHaveLength(1)
    expect(library.scripts).toEqual([expect.objectContaining({ protocol: 'http' })])
    // Reload to prove Resume reads the durable session, not retained editor state.
    await page.goto(`/#/scripts?${scopeQuery(workspace)}`)
    await page.reload()
    await page.locator('.ux-review-queue > summary').click()
    const card = page.locator('.authoring-session-card').filter({ has: page.getByRole('heading', { name: String(session.name), exact: true }) })
    await card.getByRole('button', { name: 'Resume', exact: true }).click()
    await expect(page.getByRole('region', { name: 'Author center workbench' })).toBeVisible()
    const resumedName = page.getByRole('textbox', { name: 'Script name', exact: true })
    if (!await resumedName.isVisible()) await page.getByRole('group', { name: 'Author panes' }).getByRole('button', { name: 'Outline', exact: true }).click()
    await expect(resumedName).toHaveValue('Queued Java draft')
}

test('Legacy archive editor returns a durable approval receipt', async ({ page }) => {
  await approvalHandoff(page, false)
})

test('Legacy archive editor retains later edits while queueing approval', async ({ page }) => {
  await approvalHandoff(page, true)
})
