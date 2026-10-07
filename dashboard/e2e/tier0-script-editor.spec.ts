import { expect, test, type APIRequestContext, type Page, type Response } from '@playwright/test'
import { createWorkspace, scopeQuery, type Workspace } from './support/workspace'

function record(value: unknown, what: string): Record<string, unknown> {
  if (!value || typeof value !== 'object') {
    throw new Error(`${what} was not an object: ${JSON.stringify(value)}`)
  }
  return value as Record<string, unknown>
}

async function createScript(
  request: APIRequestContext,
  workspace: Workspace,
  script: Record<string, unknown>,
): Promise<string> {
  const response = await request.post('/api/v1/scripts', {
    data: { project_id: workspace.projectId, script },
  })
  const text = await response.text()
  expect(response.ok(), text).toBeTruthy()
  const payload = record(JSON.parse(text), 'create script response')
  const stored = payload.script && typeof payload.script === 'object'
    ? record(payload.script, 'created script')
    : payload
  if (typeof stored.id !== 'string' || stored.id === '') {
    throw new Error(`create script response carried no id: ${text.slice(0, 300)}`)
  }
  return stored.id
}

async function openEditor(page: Page, workspace: Workspace, scriptId: string): Promise<void> {
  await page.goto(`/#/scripts?mode=edit&scriptId=${scriptId}&${scopeQuery(workspace)}`)
  await expect(page.getByRole('group', { name: 'Script workspace' })).toBeVisible({ timeout: 15_000 })
}

function isScriptUpdate(response: Response, scriptId: string): boolean {
  return response.request().method() === 'PUT'
    && new URL(response.url()).pathname === `/api/v1/scripts/${scriptId}`
}

// Tier-0 journey step S4 (docs/qa/E2E_SUITES.md): an imported script opens
// action-list-first, keeps unsaved edits across workbench tabs, persists them,
// and can be reopened from the Script Library without losing its actions.
test('[U21] the imported script editor preserves tab state and saved changes across reopen', async ({ page, request }) => {
  test.setTimeout(120_000)
  const workspace = await createWorkspace(request)
  const scriptId = await createScript(request, workspace, {
    name: 'Tier_0_Orders_API',
    version: '1.0.0',
    protocol: 'http',
    config: { base_url: 'https://api.tier0.invalid' },
    actions: [{
      name: 'listOrders',
      type: 'request',
      method: 'GET',
      target: '/orders',
      assertions: [{ type: 'status_code', expected: 200 }],
    }],
    metadata: {
      import: {
        source: 'openapi',
        preserved: {
          source_review: {
            source_files: [{
              path: 'tier0-orders.openapi.yaml',
              language: 'yaml',
              content: 'openapi: 3.0.3',
            }],
            mappings: [{
              id: 'tier0-orders.openapi.yaml:9-9:get-orders:actions[0]:converted',
              source_file: 'tier0-orders.openapi.yaml',
              start_line: 9,
              end_line: 9,
              function: 'GET /orders',
              target_kind: 'action',
              target_name: 'listOrders',
              target_path: 'actions[0]',
              status: 'converted',
            }],
          },
        },
      },
    },
  })

  await page.setViewportSize({ width: 1600, height: 1000 })
  await openEditor(page, workspace, scriptId)

  const workbench = page.getByRole('region', { name: 'Author center workbench' })
  const actionListTab = workbench.getByRole('tab', { name: 'Action editor' })
  await expect(actionListTab, 'the primary editor surface must be the action editor').toHaveAttribute('aria-selected', 'true')
  await page.getByRole('navigation', { name: 'Script outline' }).getByRole('button', { name: /listOrders.*GET/ }).click()
  await expect(workbench.getByLabel('Name', { exact: true })).toHaveValue('listOrders')

  const sourceMappingTab = workbench.getByRole('tab', { name: 'Source Mapping' })
  await expect(sourceMappingTab, 'an imported script must retain its source-review surface').toBeVisible()
  await sourceMappingTab.click()
  await workbench.getByRole('button', { name: /^Show mappings/ }).click()
  await expect(workbench.getByRole('cell', { name: 'GET /orders' })).toBeVisible()

  const runtimeTab = workbench.getByRole('tab', { name: 'Runtime Settings' })
  await runtimeTab.click()
  const editedBaseURL = 'https://api.edited.tier0.invalid'
  await workbench.getByRole('textbox', { name: 'Base URL' }).fill(editedBaseURL)

  // The Source pane is intentionally read-only. Switching through it and back
  // must not discard the editable runtime draft.
  await workbench.getByRole('tab', { name: 'Source', exact: true }).click()
  await expect(workbench.getByRole('tabpanel', { name: 'Source', exact: true })).toBeVisible()
  await runtimeTab.click()
  await expect(workbench.getByRole('textbox', { name: 'Base URL' })).toHaveValue(editedBaseURL)
  await actionListTab.click()
  await expect(workbench.getByLabel('Name', { exact: true })).toHaveValue('listOrders')

  const updateResponsePromise = page.waitForResponse(response => isScriptUpdate(response, scriptId))
  await page.getByRole('button', { name: 'Save script' }).click()
  const updateResponse = await updateResponsePromise
  expect(updateResponse.ok(), await updateResponse.text()).toBeTruthy()

  const storedResponse = await request.get(`/api/v1/scripts/${scriptId}`)
  const storedText = await storedResponse.text()
  expect(storedResponse.ok(), storedText).toBeTruthy()
  const storedPayload = record(JSON.parse(storedText), 'saved script response')
  const storedScript = storedPayload.script && typeof storedPayload.script === 'object'
    ? record(storedPayload.script, 'saved script')
    : storedPayload
  expect(record(storedScript.config, 'saved script config').base_url).toBe(editedBaseURL)

  await page.getByRole('button', { name: 'Close script editor' }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Scripts' })).toBeVisible({ timeout: 15_000 })
  await page.getByRole('searchbox', {name:'Search scripts',exact:true}).fill('Tier_0_Orders_API')
  const scriptRow = page.getByRole('row').filter({ hasText: 'Tier_0_Orders_API' })
  await scriptRow.getByRole('button', { name: 'Edit', exact: true }).click()

  await expect(page.getByRole('group', { name: 'Script workspace' })).toBeVisible({ timeout: 15_000 })
  const reopenedWorkbench = page.getByRole('region', { name: 'Author center workbench' })
  await expect(reopenedWorkbench.getByRole('tab', { name: 'Action editor' })).toHaveAttribute('aria-selected', 'true')
  await page.getByRole('navigation', { name: 'Script outline' }).getByRole('button', { name: /listOrders.*GET/ }).click()
  await expect(reopenedWorkbench.getByLabel('Name', { exact: true })).toHaveValue('listOrders')
  await reopenedWorkbench.getByRole('tab', { name: 'Runtime Settings' }).click()
  await expect(reopenedWorkbench.getByRole('textbox', { name: 'Base URL' })).toHaveValue(editedBaseURL)
})

test('[U21] a large script renders a usable virtualized action list within a bounded interval', async ({ page, request }) => {
  test.setTimeout(30_000)
  const workspace = await createWorkspace(request)
  const actionCount = 400
  const scriptId = await createScript(request, workspace, {
    name: 'Tier_0_Large_Import',
    version: '1.0.0',
    protocol: 'http',
    config: { base_url: 'https://api.tier0.invalid' },
    actions: Array.from({ length: actionCount }, (_, index) => ({
      name: `largeAction${index.toString().padStart(3, '0')}`,
      type: 'request',
      method: 'GET',
      target: `/items/${index}`,
    })),
  })

  await openEditor(page, workspace, scriptId)
  await page.getByRole('button', { name: 'Overview & bulk', exact: true }).click()
  await expect(
    page.getByRole('button', { name: `Action List ${actionCount} executables` }),
    'the large editor must become usable rather than hanging during full-list rendering',
  ).toBeVisible({ timeout: 10_000 })

  const renderedOutlineRows = page.locator('.action-list-virtual > .action-item')
  await expect.poll(() => renderedOutlineRows.count()).toBeGreaterThan(0)
  expect(await renderedOutlineRows.count(), 'the outline must mount a window rather than every source action').toBeLessThan(actionCount)

  const filter = page.getByRole('searchbox', { name: 'Filter the action list by name, type, or target' })
  await filter.fill('largeAction399')
  await expect(page.getByRole('button', { name: 'Select largeAction399' })).toBeVisible()
  await expect(page.getByText(`1/${actionCount}`, { exact: true })).toBeVisible()
})
