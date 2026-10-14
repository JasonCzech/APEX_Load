import { test, expect } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

test('[U22] creation capability failure has an actionable retry that recovers', async ({ page, request }) => {
  const workspace = await createWorkspace(request)
  const pattern = '**/api/v1/scripts/authoring/capabilities**'
  await page.route(pattern, route => route.fulfill({ status: 503, json: { error: 'Workflow capabilities temporarily unavailable' } }))
  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  await expect(page.getByRole('alert')).toContainText('Workflow capabilities temporarily unavailable')
  await page.unroute(pattern)
  await page.getByRole('button', { name: 'Retry', exact: true }).click()
  await expect(page.getByRole('tab', { name: 'Import', exact: true })).toBeVisible()
  await expect(page.getByText('Workflow capabilities temporarily unavailable')).toBeHidden()
})

test('[U19] unavailable readiness is explicit and real monitoring recovers', async ({ page, request }) => {
  const workspace = await createWorkspace(request)
  const pattern = '**/api/v1/system/readiness**'
  await page.route(pattern, route => route.fulfill({ status: 503, json: { error: 'Readiness unavailable' } }))
  await page.goto(`/#/monitoring?category=overview&${scopeQuery(workspace)}`)
  await expect(page.getByText('Unable to load /api/v1/system/readiness. Retrying automatically.')).toBeVisible()
  await page.unroute(pattern)
  await page.reload()
  await expect(page.getByText('Unable to load /api/v1/system/readiness. Retrying automatically.')).toBeHidden()
  await expect(page.getByRole('list', { name: 'Infrastructure readiness' })).toContainText('metadata store is reachable')
  await expect(page.getByRole('tabpanel', { name: 'Overview monitors' })).toContainText(/WebSocket\s*Connected/)
})

for (const viewport of [{ width: 1440, height: 900 }, { width: 932, height: 1096 }, { width: 1280, height: 720 }]) {
  test(`[U20] keyboard draft guard and wizard remain reachable at ${viewport.width}x${viewport.height}`, async ({ page, request }) => {
    await page.setViewportSize(viewport)
    const workspace = await createWorkspace(request)
    await page.goto(`/#/tests?view=builder&${scopeQuery(workspace)}`)
    await page.locator('[aria-label="Scenario builder steps"]').getByRole('button', { name: /^Start/ }).click()
    await page.getByPlaceholder('Scenario name').fill('Keyboard draft')
    const destination = page.locator('nav.sidebar-nav').getByRole('button', { name: 'Scripts', exact: true })
    await destination.focus(); await page.keyboard.press('Enter')
    const dialog = page.getByRole('dialog', { name: 'Unsaved Changes' })
    const keep = dialog.getByRole('button', { name: 'Keep editing' })
    await expect(keep).toBeFocused()
    for (let i = 0; i < 6; i++) {
      await page.keyboard.press('Tab')
      expect(await dialog.evaluate(node => node.contains(document.activeElement))).toBe(true)
    }
    await keep.focus(); await page.keyboard.press('Enter')
    await expect(destination).toBeFocused()
    await expect(page.getByPlaceholder('Scenario name')).toHaveValue('Keyboard draft')
    const review = page.locator('[aria-label="Scenario builder steps"]').getByRole('button', { name: /^Review/ })
    await review.focus(); await page.keyboard.press('Enter')
    const save = page.getByRole('button', { name: 'Save scenario', exact: true })
    await save.focus(); await page.keyboard.press('Enter')
    await expect(page.locator('.scenario-save-notice')).toContainText('Saved “Keyboard draft” as a draft in this project')
    const scenarios = await request.get(`/api/v1/scenarios?project_id=${workspace.projectId}`)
    expect(scenarios.ok()).toBeTruthy()
    expect((await scenarios.json()).scenarios || []).toHaveLength(0)
    await save.scrollIntoViewIfNeeded()
    const box = await save.boundingBox(); expect(box).not.toBeNull()
    expect(box!.x).toBeGreaterThanOrEqual(0); expect(box!.x + box!.width).toBeLessThanOrEqual(viewport.width)
    expect(box!.y + box!.height).toBeLessThanOrEqual(viewport.height)
  })
}

test('[U24] recorder startup failure preserves the target and exposes recovery', async ({ page, request }) => {
  const workspace = await createWorkspace(request)
  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  await page.getByRole('button', { name: 'Record', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'HTTP proxy recorder' })).toBeHidden()
  await page.getByRole('button', { name: 'HTTP proxy (advanced)', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'HTTP proxy recorder' })).toBeVisible()
  await page.getByRole('button', { name: 'Browser actions', exact: true }).click()
  const input = page.getByRole('textbox', { name: 'Start URL address' })
  await page.getByRole('button', { name: 'Start browser recording', exact: true }).click()
  await expect(input).toBeFocused()
  await expect(input).toHaveAttribute('aria-invalid', 'true')
  await input.fill('127.0.0.1:1')
  await page.route('**/api/v1/**', async route => {
    if (route.request().method() === 'POST' && new URL(route.request().url()).pathname === '/api/v1/browser-recorder/sessions')
      await route.fulfill({ status: 503, json: { error: 'Recording service unavailable. Reconnect and retry.' } })
    else await route.continue()
  })
  await page.getByRole('button', { name: 'Start browser recording', exact: true }).click()
  await expect(page.getByText('Recording service unavailable. Reconnect and retry.', { exact: false }).first()).toBeVisible()
  await expect(input).toHaveValue('127.0.0.1:1')
  await expect(page.getByRole('button', { name: 'Start browser recording', exact: true })).toBeEnabled()
})
