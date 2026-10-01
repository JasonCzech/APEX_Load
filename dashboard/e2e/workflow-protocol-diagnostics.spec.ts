import { test, expect, seedScript, json, idOf, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

// Polls may still be fulfilling a fetched response when assertions finish.
// Drain route handlers before Playwright disposes their request context.
test.afterEach(async ({ page }) => {
  await page.unrouteAll({ behavior: 'wait' })
})

// Real project authorization and run identity; diagnostic transport/aggregation
// is covered by the real gRPC integration tests, and polling/UI by this fixture.
test('gRPC diagnostics distinguishes unavailable and measured-zero responses in the active run view', async ({ page, trackRun }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: {
    project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId,
    name: 'gRPC diagnostic evidence', duration: '2m', groups: [{ name: 'rpc', script_id: scriptId, vusers: 1 }],
  } })))
  trackRun(id)
  const empty = { count: 0, sum: 0, min: 0, max: 0, buckets: [] }
  let received = false
  await page.route(`**/api/v1/tests/${id}`, async route => {
    const response = await route.fetch()
    const record = await response.json()
    await route.fulfill({ response, json: { ...record, status: 'RUNNING', live_metrics: {
      total_transactions: 1, total_errors: 0, active_vusers: 1,
      transactions: { lookup: { name: 'Lookup product', count: 1, errors: 0, avg_ms: 10,
        dependency_type: 'grpc', server_telemetry: { samples: 1, grpc: {
          samples: 1, call_ms: { ...empty, count: 1, sum: 10, max: 10 },
          first_message_ms: { ...empty, count: received ? 1 : 0 }, deadline_budget_ms: empty,
          status_counts: received ? [1] : [], unavailable_status: received ? 0 : 1,
          receive_limit_reached: 0, messages_sent: 1, messages_received: received ? 1 : 0,
          request_bytes: 5, response_bytes: 0,
        } },
      } },
    } } })
  })
  await page.goto(`/#/test/${id}?tab=diagnostics&${scopeQuery(workspace)}`)
  // Login bootstraps the project catalog before API fixture creation. Reload
  // so the authenticated route guard observes the newly authorized workspace.
  await page.reload()
  const panel = page.getByRole('region', { name: 'gRPC diagnostics', exact: true })
  await expect(panel).toBeVisible()
  await expect(panel).toContainText('First response: unavailable (0 measured)')
  await expect(panel).toContainText('Status · unavailable: 1')
  received = true
  await expect(panel).toContainText('First response: 0.0 ms (1 measured)')
  await expect(panel).toContainText('Status · OK: 1')
  await expect(panel).not.toContainText('Receive limit reached: 1')
  await page.setViewportSize({ width: 760, height: 900 })
  await expect(panel).toBeVisible()
  await panel.getByRole('heading', { name: 'gRPC diagnostics', exact: true }).hover()
  await panel.screenshot({ path: test.info().outputPath('grpc-diagnostics.png') })
})
