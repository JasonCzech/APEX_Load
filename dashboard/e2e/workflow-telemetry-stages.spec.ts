import { test, expect, seedScript, json, idOf, login, runStatus } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

type StageDistribution = { stage: string; samples: number; availability: string; advisory?: boolean }

// A real live run watched by a real browser tab: the tab measures its own
// monotonic receive-to-callback intervals and reports them once the project opts in, and the
// run's live snapshot carries them as the advisory browser render stage.
test('an opted-in project collects visible render evidence as an advisory stage', async ({ page, target, trackRun }) => {
  test.setTimeout(150_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const script = await seedScript(request, workspace, `Render_evidence_${Date.now()}`, target.url)
  const id = idOf(await json(await request.post('/api/v1/tests', { data: {
    project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId,
    name: 'Client render evidence', duration: '90s', groups: [{ name: 'render', script_id: script, vusers: 1 }],
  } })))
  trackRun(id)
  await json(await request.post(`/api/v1/tests/${id}/start`))
  await expect.poll(() => runStatus(request, id), { timeout: 60_000 }).toBe('RUNNING')

  const sample = { receive_to_render_ms: 5, state: 'visible', observed_at: new Date().toISOString() }
  const refused = await request.post(`/api/v1/tests/${id}/telemetry/client-render`, { data: { client_id: 'e2e-before-opt-in', samples: [sample] } })
  expect(refused.status(), 'render evidence before the project opts in').toBe(409)

  const project = await json(await request.put(`/api/v1/projects/${workspace.projectId}/telemetry-policy`, { data: { client_render_evidence: true } }))
  expect((project.settings as { telemetry?: { client_render_evidence?: boolean } }).telemetry?.client_render_evidence).toBe(true)

  const reported = page.waitForResponse(response => response.url().includes(`/api/v1/tests/${id}/telemetry/client-render`) && response.request().method() === 'POST', { timeout: 60_000 })
  await page.goto(`/#/test/${id}?tab=diagnostics&${scopeQuery(workspace)}`)
  await page.reload()
  const response = await reported
  expect(response.status()).toBe(202)
  const result = await response.json() as { accepted: number }
  expect(result.accepted, 'the visible tab measured its own renders').toBeGreaterThan(0)
  const body = JSON.parse(response.request().postData() || '{}') as { samples: Array<{ state: string; receive_to_render_ms: number }> }
  expect(body.samples.every(item => item.state === 'visible' && item.receive_to_render_ms >= 0 && item.receive_to_render_ms < 60_000)).toBe(true)

  // Inject callback wall-clock drift without changing performance.now().
  // This is a clock-regression fixture, not proof of visible presentation.
  const shiftedAfter = Date.now() + 60_000
  const shiftedReport = page.waitForResponse(response => {
    if (!response.url().includes(`/api/v1/tests/${id}/telemetry/client-render`) || response.request().method() !== 'POST') return false
    const payload = response.request().postDataJSON() as { samples?: Array<{ observed_at: string }> }
    return Boolean(payload.samples?.some(item => Date.parse(item.observed_at) > shiftedAfter))
  }, { timeout: 30_000 })
  await page.evaluate(() => {
    const actualNow = Date.now.bind(Date)
    Date.now = () => actualNow() + 120_000
  })
  const shifted = await shiftedReport
  expect(shifted.status()).toBe(202)
  expect((await shifted.json() as { accepted: number }).accepted).toBeGreaterThan(0)
  const shiftedBody = shifted.request().postDataJSON() as { samples: Array<{ receive_to_render_ms: number }> }
  expect(shiftedBody.samples.every(item => item.receive_to_render_ms >= 0 && item.receive_to_render_ms < 60_000)).toBe(true)

  const stage = async (name: string) => {
    const run = await json(await request.get(`/api/v1/tests/${id}`)) as { live_metrics?: { telemetry?: { stage_distributions?: StageDistribution[] } } }
    return run.live_metrics?.telemetry?.stage_distributions?.find(item => item.stage === name)
  }
  await expect.poll(async () => (await stage('browser_render'))?.samples ?? 0, { timeout: 30_000 }).toBeGreaterThan(0)
  expect(await stage('browser_render')).toMatchObject({ availability: 'measured', advisory: true })
  expect((await stage('controller_fanout'))?.availability).toBe('measured')

  const table = page.getByRole('table', { name: 'Telemetry stage distributions', exact: true })
  // The table shows the tab's own renders once the next publication carries
  // them: a measured, advisory row with a nonzero sample count.
  const renderRow = table.locator('tr[data-stage="browser_render"]')
  await expect(renderRow).toContainText('advisory, not run health', { timeout: 30_000 })
  await expect(renderRow).not.toContainText('not sampled', { timeout: 30_000 })
  await expect(renderRow.locator('td').nth(1)).not.toHaveText('0')
  await expect(table.locator('tr[data-stage="controller_fanout"]')).not.toContainText('not sampled')
  await table.screenshot({ path: test.info().outputPath('telemetry-stage-distributions.png') })

  await json(await request.post(`/api/v1/tests/${id}/abort`))
  await expect.poll(() => runStatus(request, id), { timeout: 60_000 }).toMatch(/^(COMPLETE|FAILED|ABORTED)$/)
  await expect.poll(async () => (await request.get(`/api/v1/tests/${id}/archive/manifest`)).ok(), { timeout: 60_000 }).toBe(true)
  const manifest = await json(await request.get(`/api/v1/tests/${id}/archive/manifest`)) as { telemetry_stage_distributions?: StageDistribution[]; telemetry_stage_digests?: Record<string, { count: number }> }
  expect(manifest.telemetry_stage_distributions?.find(item => item.stage === 'browser_render')?.samples).toBeGreaterThan(0)
  expect(manifest.telemetry_stage_digests?.browser_render?.count).toBeGreaterThan(0)
})
