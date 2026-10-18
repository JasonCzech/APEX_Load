import { canonicalImportDraft } from './support/importResponse'
import type { ScriptDefinition } from '../src/types/scripts'
import { test, expect, json, login, mutation } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

// A captured HAR whose repeated status polling folds into a background
// conversation, so the app origin has a top-level request and a nested one
// (http_async_start params.request).
function capturedHAR(): Buffer {
  const start = Date.UTC(2026, 9, 10, 8, 0, 0)
  const entry = (offsetMs: number, url: string, version: string) => ({
    startedDateTime: new Date(start + offsetMs).toISOString(), time: 20,
    request: { method: 'GET', url, httpVersion: version, headers: [] },
    response: {
      status: 200, httpVersion: version,
      headers: [{ name: 'content-type', value: 'application/json' }, { name: 'alt-svc', value: 'h3=":443"; ma=86400' }],
      content: { mimeType: 'application/json', text: '{}' },
    },
  })
  const entries = [entry(0, 'https://app.example.test/', 'h3')]
  for (let index = 1; index <= 5; index++) entries.push(entry(index * 2000, 'https://app.example.test/api/status', 'h3'))
  entries.push(entry(13_000, 'https://api.example.test/v1/items', 'HTTP/2.0'))
  return Buffer.from(JSON.stringify({ log: { version: '1.2', creator: { name: 'WebInspector', version: '537.36' }, entries } }))
}

test('a Transport panel origin edit reaches nested requests and survives save and reopen', async ({ page }) => {
  test.setTimeout(120_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('tab', { name: 'Import', exact: true }).click()
  await page.getByRole('searchbox', { name: 'Find a source format' }).fill('HAR Traffic')
  await page.locator('.script-create-option').filter({ has: page.getByRole('heading', { name: 'HAR Traffic', exact: true }) }).click()
  const controls = page.getByRole('group', { name: 'Script import controls' })
  await controls.locator('input[type=file]').first().setInputFiles({ name: 'transport.har', mimeType: 'application/json', buffer: capturedHAR() })
  // Import without leaf policies, so the panel's edit is the only source.
  await controls.getByLabel('HTTP version in generated script').selectOption('inherit')
  const imported = mutation(page, 'POST', '/api/v1/scripts/import/har')
  await controls.getByRole('button', { name: 'Inspect & review', exact: true }).click()
  const importResponse = await imported
  expect(importResponse.ok(), await importResponse.text()).toBeTruthy()
  expect(new URL(importResponse.url()).searchParams.get('response_format')).toBe('canonical-v1')
  const wire = await json(importResponse)
  expect(wire.scripts).toHaveLength(1)
  const draft = canonicalImportDraft(wire)
  expect(draft.actions.some(action => action.type === 'http_async_start')).toBe(true)

  await controls.getByRole('tab', { name: /^Runtime settings/ }).click()
  const panel = controls.getByRole('region', { name: 'Transport', exact: true })
  await expect(panel).toBeVisible()
  const appOrigin = panel.getByLabel('HTTP version for https://app.example.test', { exact: true })
  await expect(appOrigin).toHaveValue('')
  await expect(panel.getByRole('row', { name: /https:\/\/app\.example\.test/ })).toContainText('h3 2')
  await expect(panel.getByRole('row', { name: /https:\/\/app\.example\.test/ })).toContainText('Yes (Alt-Svc)')

  // Narrow width: rows become labeled cards without horizontal overflow.
  await page.setViewportSize({ width: 390, height: 844 })
  const overflow = await panel.evaluate(element => {
    const limit = element.getBoundingClientRect().right + 1
    return Array.from(element.querySelectorAll('*')).filter(child => child.getBoundingClientRect().right > limit).map(child => `${child.tagName.toLowerCase()}.${child.className}`).slice(0, 5)
  })
  expect(overflow).toEqual([])
  expect(await panel.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  await page.setViewportSize({ width: 1280, height: 900 })

  // Keyboard: choose with the select, then apply from the keyboard.
  await appOrigin.focus()
  await appOrigin.selectOption('auto+h3')
  const preview = panel.getByRole('region', { name: 'Transport preview', exact: true })
  await expect(preview).toContainText('Preview: 2 requests change')
  await expect(preview).toContainText('.params.request')
  const applyButton = preview.getByRole('button', { name: 'Apply to 2 requests', exact: true })
  const applied = mutation(page, 'POST', '/api/v1/scripts/import/transport/apply')
  await applyButton.focus()
  await page.keyboard.press('Enter')
  const applyResponse = await applied
  expect(applyResponse.ok(), await applyResponse.text()).toBeTruthy()
  await expect(panel.getByRole('status')).toHaveText('Applied HTTP version choices to 2 requests.')
  await expect(appOrigin).toHaveValue('auto+h3')

  await controls.getByRole('tab', { name: /^Review(,|$)/ }).click()
  await controls.getByRole('button', { name: 'Save draft', exact: true }).click()
  await expect.poll(async () => {
    const listed = await json(await request.get(`/api/v1/scripts?project_id=${workspace.projectId}`)) as { scripts?: Array<{ id: string }> }
    return listed.scripts?.length ?? 0
  }, { timeout: 30_000 }).toBe(1)

  // Reopen from storage, after a fresh page load.
  await page.reload()
  const listed = await json(await request.get(`/api/v1/scripts?project_id=${workspace.projectId}`)) as { scripts: Array<{ id: string }> }
  const stored = await json(await request.get(`/api/v1/scripts/${listed.scripts[0].id}`))
  const script = (stored.script ?? stored) as ScriptDefinition
  expect(stored.project_id).toBe(workspace.projectId)
  expect(script.name).toBe(draft.name)
  const home = script.actions.find(action => action.target === '/')!
  const poll = script.actions.find(action => action.type === 'http_async_start')!
  const nested = poll.params!.request as { http_version?: string; metadata?: Record<string, unknown> }
  expect(home.http_version).toBe('auto+h3')
  expect(nested.http_version).toBe('auto+h3')
  expect(nested.metadata?.http_version_transport_edit).toMatchObject({ policy: 'auto+h3', previous_policy: 'inherit', origin: 'https://app.example.test' })
  // The other origin is untouched.
  expect(script.actions.find(action => action.target === 'https://api.example.test/v1/items')!.http_version).toBeUndefined()

  await page.goto(`/#/scripts?mode=edit&scriptId=${listed.scripts[0].id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await expect(page.getByText(script.name, { exact: true }).first()).toBeVisible()
})
