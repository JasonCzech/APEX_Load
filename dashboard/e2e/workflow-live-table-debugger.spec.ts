import { test, expect, expectDebugOutcome, idOf, json, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

test('debugger uses explicit isolated live-table seeds and resets them for every replay', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const request = page.context().request
  const workspace = await createWorkspace(request)
  const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: {
    name: 'Sandbox consumer', version: '1.0', protocol: 'http', config: { base_url: 'http://127.0.0.1:1' },
    actions: [{ name: 'Take sandbox work', type: 'live_table', params: { alias: 'work', operation: 'pop', result_prefix: 'taken' } }],
  } } })))
  const seeds = [{ alias: 'work', profile: 'native_queue', columns: ['id', 'note'], rows: [{ values: { id: 'sandbox-row', note: '' } }] }]
  // Unsupported replay is rejected before creating a session or touching targets.
  const missing = await request.post(`/api/v1/scripts/${id}/debug/sessions`, { data: {} })
  expect(missing.status()).toBe(400)
  expect(await missing.text()).toContain('configure isolated live table seeds')
  await page.setViewportSize({ width: 1440, height: 1100 })
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  await page.reload()
  await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
  await page.getByRole('button', { name: /^Debug options/ }).click()
  const options = page.getByRole('region', { name: 'Session Overrides' })
  const input = options.getByLabel('Sandbox seeds (JSON)')
  await input.fill('{')
  await expect(options.getByText('Enter sandbox seeds as a JSON array.', { exact: true }).last()).toBeVisible()
  await input.fill(JSON.stringify(seeds, null, 2))
  await expect(input).toHaveAttribute('aria-invalid', 'false')
  await input.scrollIntoViewIfNeeded()
  await page.screenshot({ path: testInfo.outputPath('live-table-debugger-seeds.png'), fullPage: true })
  await page.setViewportSize({ width: 600, height: 900 })
  await input.scrollIntoViewIfNeeded()
  await expect(input).toBeInViewport()
  expect(await input.evaluate(element => element.getBoundingClientRect().right)).toBeLessThanOrEqual(600)
  await page.screenshot({ path: testInfo.outputPath('live-table-debugger-seeds-narrow.png'), fullPage: true })
  await page.setViewportSize({ width: 1440, height: 1100 })
  await options.getByRole('button', { name: 'Done', exact: true }).click()
  const sessions: string[] = []
  for (let attempt = 0; attempt < 2; attempt++) {
    const response = page.waitForResponse(res => res.request().method() === 'POST' && new URL(res.url()).pathname === `/api/v1/scripts/${id}/debug/sessions`)
    await page.locator('.topbar-debug-action-run').click()
    const started = await response
    expect(started.ok(), await started.text()).toBe(true)
    expect(started.request().postDataJSON().live_table_seeds).toEqual(seeds)
    const sessionId = (await started.json()).replay_session_id as string
    sessions.push(sessionId)
    await expect.poll(async () => json(await request.get(`/api/v1/replay/sessions/${sessionId}`))).toMatchObject({ session: { status: 'completed' } })
    const session = (await json(await request.get(`/api/v1/replay/sessions/${sessionId}`))).session
    expect(session).toMatchObject({ report: { final_variables: { 'taken.values.id': 'sandbox-row', 'taken.values.note': '' } } })
    expect(session).not.toHaveProperty('live_table_seeds')
    await expectDebugOutcome(page, 'passed')
  }
  expect(sessions[0]).not.toBe(sessions[1])
  // Classic replay accepts the same explicit seed contract and also starts fresh.
  const classic = await json(await request.post(`/api/v1/scripts/${id}/replay`, { data: { live_table_seeds: seeds } }))
  expect(classic).toMatchObject({ report: { success: true, final_variables: { 'taken.values.id': 'sandbox-row' } } })
  const pools = await json(await request.get(`/api/v1/data-pools?project_id=${workspace.projectId}`))
  expect(pools.data_pools).toHaveLength(0)
})
