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
          encode_ms: { ...empty, count: 1, sum: 8 }, decode_ms: { ...empty, count: received ? 1 : 0, sum: received ? 8 : 0 },
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
  await expect(panel).toContainText('Client-call message rate: 100.00 sent/s / 0.00 received/s')
  await expect(panel).toContainText('Codec elapsed work / call wall time: unavailable')
  received = true
  await expect(panel).toContainText('First response: 0.0 ms (1 measured)')
  await expect(panel).toContainText('Status · OK: 1')
  await expect(panel).toContainText('Client-call message rate: 100.00 sent/s / 100.00 received/s')
  await expect(panel).toContainText('Mean protobuf message payload: 5.00 sent bytes / 0.00 received bytes')
  await expect(panel).toContainText('Codec elapsed work / call wall time: 160.00 % (codec timings can overlap)')
  await expect(panel).not.toContainText('Receive limit reached: 1')
  await page.setViewportSize({ width: 760, height: 900 })
  await expect(panel).toBeVisible()
  await panel.getByRole('heading', { name: 'gRPC diagnostics', exact: true }).hover()
  await panel.screenshot({ path: test.info().outputPath('grpc-diagnostics.png') })
})

test('WebSocket diagnostics preserves unavailable ping RTT and matched zero RTT', async ({ page, trackRun }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request; const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'WebSocket evidence', duration: '2m', groups: [{ name: 'socket', script_id: scriptId, vusers: 1 }] } })))
  trackRun(id)
  const empty = { count: 0, sum: 0, min: 0, max: 0, buckets: [] }; let matched = false
  await page.route(`**/api/v1/tests/${id}`, async route => {
    const response = await route.fetch(); const record = await response.json()
    await route.fulfill({ response, json: { ...record, status: 'RUNNING', live_metrics: { total_transactions: 1, total_errors: 0, active_vusers: 1, transactions: { socket: { name: 'Socket messages', count: 1, errors: 0, avg_ms: 10, dependency_type: 'websocket', server_telemetry: { samples: 1, websocket: { samples: 1, handshake_ms: empty, reconnect_ms: empty, send_ms: empty, receive_ms: { ...empty, count: 1, sum: 12, max: 12 }, write_wait_ms: empty, ping_rtt_ms: { ...empty, count: matched ? 1 : 0 }, text_receive_ms: empty, binary_receive_ms: empty, reconnects: 0, messages_sent: 0, messages_received: 1, bytes_sent: 0, bytes_received: 8, max_pending_writes: 0, normal_closes: 0, abnormal_closes: 0, timeouts: 0, cancellations: 0 } } } } } } })
  })
  await page.goto(`/#/test/${id}?tab=diagnostics&${scopeQuery(workspace)}`); await page.reload()
  const panel = page.getByRole('region', { name: 'WebSocket diagnostics', exact: true })
  await expect(panel).toContainText('Receive wait: 12.0 ms mean (1 measured)')
  await expect(panel).toContainText('Matched ping RTT: unavailable mean (0 measured)')
  matched = true
  await expect(panel).toContainText('Matched ping RTT: 0.0 ms mean (1 measured)')
  await page.setViewportSize({ width: 760, height: 900 })
  await panel.screenshot({ path: test.info().outputPath('websocket-diagnostics.png') })
})

test('messaging diagnostics preserves observed zero, missing acknowledgement and partial coverage', async ({ page, trackRun }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: {
    project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId,
    name: 'Messaging evidence', duration: '2m', groups: [{ name: 'consumer', script_id: scriptId, vusers: 1 }],
  } })))
  trackRun(id)
  const absent = { count: 0, sum: 0, max: 0 }
  const messaging = { incomplete: true, dimensions: [{ protocol: 'kafka', group: 'consumer', source: 'orders', partition: '0', subscription: 'checkout', operations: 2, sent: 0, received: 2, throttled: 1, redelivered: 1, age_ms: { count: 2, sum: 0, max: 0 }, offset_lag: absent, ack_ms: absent }] }
  await page.route(`**/api/v1/tests/${id}`, async route => {
    const response = await route.fetch()
    const record = await response.json()
    await route.fulfill({ response, json: { ...record, status: 'RUNNING', live_metrics: { total_transactions: 2, total_errors: 0, active_vusers: 1, messaging: { receive: messaging }, transactions: {} } } })
  })
  await page.goto(`/#/test/${id}?tab=diagnostics&${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByText('Messaging partition and subscription diagnostics', { exact: true }).click()
  const evidence = page.getByRole('region', { name: 'Messaging evidence receive' })
  await expect(evidence).toContainText('checkout')
  await expect(evidence.getByRole('cell', { name: 'kafka', exact: true })).toHaveCount(1)
  await expect(evidence.getByRole('cell', { name: 'consumer', exact: true })).toHaveCount(1)
  await expect(evidence).toContainText('0.000 ms (max 0.000 ms; 2 observed)')
  await expect(evidence).toContainText('Coverage incomplete; partition skew is unscored.')
  await expect(evidence.getByRole('cell', { name: 'Unavailable', exact: true })).toHaveCount(2)
  await page.setViewportSize({ width: 760, height: 900 })
  await expect(evidence).toBeVisible()
})


test('Event Hubs progress preserves exact cursors and unavailable lag during idle windows', async ({ page, trackRun }) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scriptId = await seedScript(request, workspace)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: {
    project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId,
    name: 'Event Hubs progress evidence', duration: '2m', groups: [{ name: 'workers', script_id: scriptId, vusers: 1 }],
  } })))
  trackRun(id)
  const now = '2026-10-03T00:00:00Z'
  const position = { state: 'observed', sequence: '9007199254740993', provider_offset: '2:-1:-1', observed_at: now, received_at: now, observation_age_ms: 10 }
  let incomplete = false
  await page.route(`**/api/v1/tests/${id}`, async route => {
    const response = await route.fetch(); const record = await response.json()
    await route.fulfill({ response, json: { ...record, status: 'RUNNING', live_metrics: {
      total_transactions: 0, total_errors: 0, active_vusers: 1, transactions: {},
      messaging_progress: { version: 1, lifecycle: 'running', captured_at: now, incomplete, rows: [{
        origin: 'generator', group: 'workers', protocol: 'eventhubs', mode: 'processor', source_id: 'source', source: 'orders', subscription: 'checkout', session_id: 'session', partition: '0', generation: 'ownership-1', revision: '1', capture_revision: '1', captured_at: now, updated_at: now, expected_partitions: 1, inventory_complete: true, ownership: 'owned', ownership_verified_at: now, ownership_verified_age_ms: 10, acquired: 1, lost: 0, delivered: 1, rejected: 0, redelivered: 0,
        delivered_position: position, checkpoint_position: { state: 'missing', observed_at: now, received_at: now }, tail_position: { ...position, sequence: '9007199254740995' },
        checkpoint: { attempts: 0, committed: 0, failed: 0, fenced: 0, cancelled_before_write: 0, unconfirmed: 0, retry_attempts: 0, in_flight: 0, duration: { metric: 'checkpoint', population: 'checkpoint_operation', eligible: 0, distribution_ms: { count: 0, sum: 0, min: 0, max: 0, buckets: [] } }, last_completed_at: now },
      }] },
    } } })
  })
  await page.goto(`/#/test/${id}?tab=diagnostics&${scopeQuery(workspace)}`); await page.reload()
  const summary = page.getByText('Event Hubs processor progress', { exact: true })
  await expect(summary).toBeVisible(); await summary.click()
  const panel = summary.locator('..')
  await expect(panel).toContainText('9007199254740993')
  await expect(panel).toContainText('9007199254740995')
  await expect(panel.getByRole('cell', { name: '2', exact: true })).toHaveCount(1)
  await expect(panel.getByRole('cell', { name: 'Unavailable', exact: true })).toHaveCount(1)
  await expect(panel).toContainText('0 attempts; 0 committed')
  incomplete = true
  await expect(panel).toContainText('Processor coverage incomplete; lag is unscored.')
  await expect(panel.getByRole('cell', { name: 'Unavailable', exact: true })).toHaveCount(2)
  await page.setViewportSize({ width: 760, height: 900 })
  await panel.screenshot({ path: test.info().outputPath('eventhubs-progress.png') })
})
