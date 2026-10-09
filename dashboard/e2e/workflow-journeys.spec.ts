import { test, expect, json, idOf, login, runStatus } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

interface JourneyAccounting { id: string; weight: number; completed: number; failed: number; aborted: number; censored: number; eligible: number; steps: number; step_failures: number }
interface JourneyReport { availability: string; policy: { id: string; revision: number; source: string }; journeys: JourneyAccounting[]; weighted_failure_index?: number; step_risk_enabled: boolean; step_risk_index?: number; step_risk_unscored?: string }

// A real run under a pinned journey policy: every archived row carries its
// VUser iteration, and the run's journeys are accounted from them. The
// payment step always fails (the target answers 503), so the payment journey
// fails and the checkout journey completes with a step failure.
test('a run pinned to a journey policy accounts its journeys from the archive', async ({ page, target, trackRun }) => {
  test.setTimeout(180_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scope = `project_id=${encodeURIComponent(workspace.projectId)}`
  const script = idOf(await json(await request.post('/api/v1/scripts', { data: {
    project_id: workspace.projectId,
    script: {
      name: `Journeys_${Date.now()}`, version: '1.0.0', protocol: 'http',
      config: { base_url: target.url, pacing: { type: 'fixed_interval', interval_ms: 300 }, error_handling: { continue_on_error: true } },
      actions: [
        { name: 'open_cart', type: 'request', method: 'GET', target: '/orders' },
        { name: 'pay', type: 'request', method: 'GET', target: '/unavailable' },
        { name: 'confirm', type: 'request', method: 'GET', target: '/orders' },
      ],
    },
  } })))
  const policy = await json(await request.post(`/api/v1/analysis/policies?${scope}`, { data: {
    kind: 'journey_policy', name: 'Checkout journeys', definition: {
      version: 1, apply_by_default: true, step_risk_enabled: true, journeys: [
        { id: 'payment', name: 'Payment', start: { action: 'open_cart' }, success: { action: 'pay' }, weight: 2 },
        { id: 'checkout', name: 'Checkout', start: { action: 'open_cart' }, success: { action: 'confirm' }, weight: 1 },
      ],
    },
  } }))

  const id = idOf(await json(await request.post('/api/v1/tests', { data: {
    project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId,
    name: 'Journey run', duration: '4s', groups: [{ name: 'shop', script_id: script, vusers: 1 }],
  } })))
  trackRun(id)
  await json(await request.post(`/api/v1/tests/${id}/start`))
  await expect.poll(() => runStatus(request, id), { timeout: 90_000 }).toBe('COMPLETE')
  await expect.poll(async () => (await request.get(`/api/v1/tests/${id}/archive/manifest`)).ok(), { timeout: 60_000 }).toBe(true)

  const rows = await json(await request.get(`/api/v1/tests/${id}/archive/transactions?limit=20`)) as unknown as { transactions: Array<{ action_name: string; iteration?: number; vuser_id?: number }> }
  const opened = rows.transactions.filter(row => row.action_name === 'open_cart')
  expect(opened.length).toBeGreaterThan(0)
  expect(opened.every(row => (row.iteration || 0) >= 1), 'archived rows carry their iteration').toBe(true)

  const report = await json(await request.get(`/api/v1/tests/${id}/archive/journeys`)) as unknown as JourneyReport
  expect(report.availability).toBe('available')
  expect(report.policy).toMatchObject({ id: String(policy.id), revision: Number(policy.revision), source: 'pinned' })
  const payment = report.journeys.find(journey => journey.id === 'payment')!
  const checkout = report.journeys.find(journey => journey.id === 'checkout')!
  expect(payment.failed, JSON.stringify(payment)).toBeGreaterThan(0)
  expect(payment.completed).toBe(0)
  expect(checkout.completed, JSON.stringify(checkout)).toBeGreaterThan(0)
  expect(checkout.step_failures).toBeGreaterThan(0)
  expect(checkout.failed).toBe(0)
  const weighted = (2 * payment.failed + checkout.failed) / (2 * payment.eligible + checkout.eligible)
  expect(report.weighted_failure_index).toBeCloseTo(weighted, 10)
  expect(report.step_risk_index).toBeCloseTo((2 * payment.step_failures + checkout.step_failures) / (2 * payment.steps + checkout.steps), 10)

  const unknown = await request.get(`/api/v1/tests/${id}/archive/journeys?policy=missing-policy`)
  expect(unknown.status()).toBe(404)

  await page.goto(`/#/report/${id}?section=performance&${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByRole('tab', { name: 'Performance', exact: true }).click()
  const panel = page.getByRole('region', { name: 'Journey risk' })
  await expect(panel).toBeVisible({ timeout: 30_000 })
  await expect(panel).toContainText('Weighted failure index')
  await expect(panel.getByRole('row', { name: /^Payment/ })).toBeVisible()
  await expect(panel).toContainText("the run's pinned policy")
})
