import { test, expect, seedScript, json, idOf, login, runStatus } from './support/workflow'
import { createWorkspace } from './support/workspace'

// A real run with a joint evidence v4 projection: the projection is checked
// at creation, frozen into the archive, the engine counts the script's
// samples under their exact tuples, a merged live joint query reads them,
// and the archive holds the joint interval rows.
test('a joint evidence projection is captured live and archived for a real run', async ({ page, target, trackRun }) => {
  test.setTimeout(180_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const script = await seedScript(request, workspace, `Joint_evidence_${Date.now()}`, target.url)
  const base = { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, groups: [{ name: 'joint', script_id: script, vusers: 1 }] }

  const invalid = await request.post('/api/v1/tests', { data: { ...base, name: 'Invalid projection', duration: '3s', joint_evidence: { population: 'action', dimensions: ['host'] } } })
  expect(invalid.status()).toBe(400)
  expect(await invalid.text()).toContain('joint_evidence')

  const projection = { population: 'action', dimensions: ['name', 'group'] }
  const id = idOf(await json(await request.post('/api/v1/tests', { data: { ...base, name: 'Joint evidence run', duration: '4s', joint_evidence: projection } })))
  trackRun(id)
  await json(await request.post(`/api/v1/tests/${id}/start`))
  await expect.poll(() => runStatus(request, id), { timeout: 90_000 }).toBe('COMPLETE')
  await expect.poll(() => target.count(), { timeout: 30_000 }).toBeGreaterThan(0)

  // A distribution metric in one-second buckets: counts need fully covered,
  // non-straddling buckets, and the bucket holding the run's terminal
  // state-only point (no native evidence) is a gap.
  const query = { evidence_mode: 'live', live_bucket_mode: 'merged', joint_evidence: true, population: 'action', metric: 'response_time', aggregation: 'p95', dimensions: ['name', 'group'], start_seconds: 0, end_seconds: 30, bucket_seconds: 1, max_series: 5 }
  const live = await json(await request.post(`/api/v1/tests/${id}/native-series`, { data: query })) as { series: Array<{ name: string; points: Array<{ count: number; value: number | null }> }> }
  const tuple = live.series.find(series => series.name === JSON.stringify(['orders', 'joint']))
  expect(tuple, `joint series ${JSON.stringify(live.series.map(series => series.name))}`).toBeTruthy()
  expect(tuple!.points.reduce((sum, point) => sum + (point.count || 0), 0)).toBeGreaterThan(0)
  expect(tuple!.points.some(point => point.value !== null)).toBe(true)
  const unprojected = await request.post(`/api/v1/tests/${id}/native-series`, { data: { ...query, dimensions: ['region'] } })
  expect(unprojected.status(), 'a dimension the run did not project').toBeGreaterThanOrEqual(400)

  await expect.poll(async () => (await request.get(`/api/v1/tests/${id}/archive/manifest`)).ok(), { timeout: 60_000 }).toBe(true)
  await expect.poll(async () => Number((await json(await request.get(`/api/v1/tests/${id}/archive/manifest`))).joint_interval_count || 0), { timeout: 60_000 }).toBeGreaterThan(0)
  const manifest = await json(await request.get(`/api/v1/tests/${id}/archive/manifest`)) as { joint_intervals_file?: string; joint_intervals_parquet_file?: string; provenance: { joint_evidence?: unknown } }
  expect(manifest.provenance.joint_evidence).toEqual(projection)
  expect(manifest.joint_intervals_file).toBe('joint_intervals_v4.jsonl')
  expect(manifest.joint_intervals_parquet_file).toBe('joint_intervals_v4.parquet')
})
