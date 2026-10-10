import { test, expect, seedScript, json, idOf, login, runStatus } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

interface Findings { run_id: string; status: string; job_id?: string; result?: { policy: { id: string; revision: number; source: string }; score: { score?: number; severity: string; unscored_reason?: string }; findings: unknown[]; rule_set: string } }

// A real run pinned to a browser policy: a run that requested no document
// evidence is not evaluated automatically; an on-demand evaluation (from the
// API and from the report panel) is a real browser_intelligence job whose
// result never claims a score without document evidence.
test('browser findings are evaluated on demand and never score absent evidence', async ({ page, target, trackRun }) => {
  test.setTimeout(180_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const scope = `project_id=${encodeURIComponent(workspace.projectId)}`
  const script = await seedScript(request, workspace, `Browser_findings_${Date.now()}`, target.url)
  const policy = await json(await request.post(`/api/v1/analysis/policies?${scope}`, { data: {
    kind: 'browser_policy', name: 'Web vitals', definition: {
      version: 1, apply_by_default: true, baseline: { mode: 'none' },
      metrics: [{ metric: 'lcp', good: 2500, poor: 4000, weight: 3 }, { metric: 'cls', good: 0.1, poor: 0.25, weight: 1 }],
    },
  } }))
  const id = idOf(await json(await request.post('/api/v1/tests', { data: {
    project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId,
    name: 'Browser findings run', duration: '3s', groups: [{ name: 'orders', script_id: script, vusers: 1 }],
  } })))
  trackRun(id)
  await json(await request.post(`/api/v1/tests/${id}/start`))
  await expect.poll(() => runStatus(request, id), { timeout: 90_000 }).toBe('COMPLETE')
  await expect.poll(async () => (await request.get(`/api/v1/tests/${id}/archive/manifest`)).ok(), { timeout: 60_000 }).toBe(true)
  const manifest = await json(await request.get(`/api/v1/tests/${id}/archive/manifest`)) as { provenance: { policy_pins?: Array<{ kind: string; id: string }> } }
  expect(manifest.provenance.policy_pins?.some(pin => pin.kind === 'browser_policy' && pin.id === String(policy.id))).toBe(true)
  const before = await json(await request.get(`/api/v1/tests/${id}/archive/browser-findings`)) as unknown as Findings
  expect(before.status, 'a run without document evidence is not evaluated automatically').toBe('not_evaluated')

  const queued = await json(await request.post(`/api/v1/analysis/evaluations?${scope}`, { data: { kind: 'browser_intelligence', request: { run: id } } }))
  await expect.poll(async () => String((await json(await request.get(`/api/v1/analysis/evaluations/${queued.id}?${scope}`))).state), { timeout: 60_000 }).toBe('ready')
  const ready = await json(await request.get(`/api/v1/tests/${id}/archive/browser-findings`)) as unknown as Findings
  expect(ready.status).toBe('ready')
  expect(ready.job_id).toBe(String(queued.id))
  expect(ready.result!.rule_set).toBe('browser_findings.v1')
  expect(ready.result!.policy).toMatchObject({ id: String(policy.id), revision: Number(policy.revision), source: 'pinned' })
  expect(ready.result!.score.score, 'no score without document evidence').toBeUndefined()
  expect(ready.result!.score).toMatchObject({ severity: 'unscored', unscored_reason: 'no_document_evidence' })
  expect(ready.result!.findings).toEqual([])

  const unknown = await request.post(`/api/v1/analysis/evaluations?${scope}`, { data: { kind: 'browser_intelligence', request: { run: id, policy: { id: 'missing', revision: 1 } } } })
  // A policy revision that does not exist is not found (a request fault would be 400).
  expect(unknown.status()).toBe(404)

  // The pinned browser policy offers the Browser area although the run kept
  // no browser evidence.
  await page.goto(`/#/report/${id}?section=browser&${scopeQuery(workspace)}`)
  await page.reload()
  await expect(page.getByRole('tab', { name: 'Browser', exact: true })).toHaveAttribute('aria-selected', 'true')
  const panel = page.getByRole('region', { name: 'Browser findings' })
  await expect(panel).toContainText('No overall score: the run retained no document evidence.', { timeout: 30_000 })
  await expect(panel).toContainText("Web vitals revision")
  const evaluations = page.waitForResponse(response => response.request().method() === 'POST' && response.url().includes('/api/v1/analysis/evaluations'))
  await panel.getByRole('button', { name: 'Evaluate with the pinned policy' }).click()
  expect((await evaluations).status()).toBe(202)
})
