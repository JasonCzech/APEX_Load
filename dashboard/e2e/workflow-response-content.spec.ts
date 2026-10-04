import { createServer } from 'node:http'
import type { Socket } from 'node:net'
import { readFile } from 'node:fs/promises'
import { createGzip, constants, gzipSync } from 'node:zlib'
import type { Locator, Page, TestInfo } from '@playwright/test'
import type { ScriptDefinition } from '../src/types/scripts'
import { test, expect, idOf, json, login, mutation } from './support/workflow'
import { createWorkspace, scopeQuery, type Workspace } from './support/workspace'

const capturedToken = 'captured-response-token-abcdef123456'
const freshToken = 'fresh-loopback-token-fedcba654321'
const responseLabel = 'Résumé 日本語'

function harEntry(base: string, path: string, responseBody: string, options: { submit?: boolean; base64?: boolean; envelope?: Record<string, unknown> } = {}) {
  const wireBody = options.base64 ? gzipSync(responseBody) : Buffer.from(responseBody)
  return {
    startedDateTime: '2026-10-03T00:00:00.000Z', time: 0,
    request: { method: options.submit ? 'POST' : 'GET', url: `${base}${path}`, httpVersion: 'HTTP/1.1', headers: options.submit ? [{ name: 'Content-Type', value: 'application/x-www-form-urlencoded' }] : [{ name: 'Accept-Encoding', value: 'gzip' }], ...(options.submit ? { postData: { mimeType: 'application/x-www-form-urlencoded', text: `csrf_token=${capturedToken}` } } : {}) },
    response: { status: 200, httpVersion: 'HTTP/1.1', headers: [{ name: 'Content-Type', value: 'application/json' }, ...(options.base64 ? [{ name: 'Content-Encoding', value: 'gzip' }, { name: 'Content-Length', value: String(wireBody.length) }] : [])], bodySize: wireBody.length, content: { size: Buffer.byteLength(responseBody), mimeType: 'application/json', text: options.base64 ? Buffer.from(responseBody).toString('base64') : responseBody, ...(options.base64 ? { encoding: 'base64' } : {}) } },
    ...(options.envelope ? { _apexload_response_content: options.envelope } : {}),
  }
}

async function compressedTarget() {
  const sockets = new Set<Socket>()
  const compressors = new Set<ReturnType<typeof createGzip>>()
  const stats = { totalRequests: 0, formCalls: 0, submitCalls: 0, accepted: 0, encodedBytes: 0, streamCalls: 0, streamConsumerCalls: 0, acceptedStreamConsumers: 0, streamEndCalls: 0 }
  const server = createServer((request, response) => {
    stats.totalRequests++
    response.setHeader('Content-Type', 'application/json')
    if (request.url === '/events') {
      stats.streamCalls++
      response.setHeader('Content-Type', 'text/event-stream'); response.setHeader('Content-Encoding', 'gzip')
      response.once('finish', () => { stats.streamEndCalls++ })
      const compressor = createGzip()
      compressors.add(compressor)
      response.once('close', () => { compressors.delete(compressor); compressor.destroy() })
      compressor.once('error', () => { compressors.delete(compressor); response.destroy() })
      compressor.pipe(response)
      compressor.write('event: token\ndata: synthetic-stream-value\n\nevent: done\ndata: sampled\n\n')
      compressor.flush(constants.Z_SYNC_FLUSH)
      // Do not emit a gzip trailer or response EOF. stop_on_event must finish
      // this collection window while the origin's stream remains incomplete.
      return
    }
    if (request.url?.startsWith('/stream-consume?')) {
      stats.streamConsumerCalls++
      const accepted = new URL(request.url, 'http://fixture').searchParams.get('value') === 'synthetic-stream-value'
      if (accepted) stats.acceptedStreamConsumers++
      response.statusCode = accepted ? 200 : 409; response.end(JSON.stringify({ accepted }))
      return
    }
    if (request.url === '/form') {
      stats.formCalls++
      const encoded = gzipSync(JSON.stringify({ csrf_token: freshToken, label: responseLabel }))
      stats.encodedBytes += encoded.length
      response.setHeader('Content-Encoding', 'gzip'); response.setHeader('Content-Length', encoded.length)
      response.end(encoded)
      return
    }
    if (request.url === '/submit' && request.method === 'POST') {
      stats.submitCalls++
      const chunks: Buffer[] = []
      let bodyBytes = 0, rejected = false
      request.on('data', chunk => {
        if (rejected) return
        const content = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk)
        bodyBytes += content.length
        if (bodyBytes > 64 * 1024) {
          rejected = true; chunks.length = 0
          response.statusCode = 413; response.end('{"error":"synthetic request body limit"}'); request.destroy()
          return
        }
        chunks.push(content)
      })
      request.once('aborted', () => { rejected = true; chunks.length = 0 })
      request.once('error', () => {
        rejected = true; chunks.length = 0
        if (!response.destroyed && !response.writableEnded) { response.statusCode = 400; response.end('{"error":"synthetic request failed"}') }
      })
      request.on('end', () => {
        if (rejected) return
        const accepted = new URLSearchParams(Buffer.concat(chunks).toString()).get('csrf_token') === freshToken
        if (accepted) stats.accepted++
        response.statusCode = accepted ? 200 : 409
        response.end(JSON.stringify({ accepted }))
      })
      return
    }
    response.statusCode = 404; response.end('{"error":"unknown synthetic route"}')
  })
  server.on('connection', socket => { sockets.add(socket); socket.once('close', () => sockets.delete(socket)) })
  try {
    await new Promise<void>((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', () => { server.removeListener('error', reject); resolve() }) })
    const address = server.address()
    if (!address || typeof address === 'string') throw new Error('Missing loopback fixture address')
    return {
      base: `http://127.0.0.1:${address.port}`, stats,
      async close() { for (const compressor of compressors) compressor.destroy(); for (const socket of sockets) socket.destroy(); await new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())) },
    }
  } catch (error) {
    for (const compressor of compressors) compressor.destroy()
    for (const socket of sockets) socket.destroy()
    if (server.listening) await new Promise<void>(resolve => server.close(() => resolve()))
    throw error
  }
}

async function inspectHAR(page: Page, workspace: Workspace, name: string, entries: unknown[]) {
  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('button', { name: 'Import', exact: true }).click()
  await page.getByRole('searchbox', { name: 'Find a source format' }).fill('HAR Traffic')
  await page.getByRole('button', { name: /^HAR HAR Traffic/ }).click()
  const controls = page.getByRole('group', { name: 'Script import controls' })
  await controls.locator('input[type=file]').first().setInputFiles({ name: `${name}.har`, mimeType: 'application/json', buffer: Buffer.from(JSON.stringify({ log: { version: '1.2', creator: { name: 'Synthetic HAR evidence', version: '1' }, entries } })) })
  await controls.getByRole('textbox', { name: 'Script Name', exact: true }).fill(name)
  const inspected = mutation(page, 'POST', '/api/v1/scripts/import/har')
  await controls.getByRole('button', { name: 'Inspect & review', exact: true }).click()
  const result = await json(await inspected)
  await expect(controls.getByRole('heading', { name: 'Review Generated Script', exact: true })).toBeVisible()
  return { controls, result, script: result.script as unknown as ScriptDefinition }
}

async function saveAndOpen(page: Page, controls: Locator) {
  const saved = mutation(page, 'POST', '/api/v1/scripts/import/commit-batch')
  await controls.getByRole('button', { name: 'Save draft', exact: true }).click()
  const result = await json(await saved)
  const scripts = result.scripts as Record<string, unknown>[]
  expect(scripts).toHaveLength(1)
  const id = idOf(scripts[0])
  await controls.getByRole('button', { name: /^Open .* in Visual Editor$/ }).click()
  await expect(page.getByRole('group', { name: 'Script workspace' })).toBeVisible()
  return id
}

async function reopenSavedScript(page: Page, workspace: Workspace, id: string) {
  // The import button opens an in-memory authoring session. The stored-script
  // route plus a fresh load verifies persisted content rather than that draft.
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  await page.reload()
  await expect(page.getByRole('group', { name: 'Script workspace' })).toBeVisible()
}

async function selectReplayAction(page: Page, name: string) {
  const panes = page.getByRole('group', { name: 'Debug panes', exact: true })
  if (await panes.isVisible()) await panes.getByRole('button', { name: 'Outline', exact: true }).click()
  const rail = page.getByRole('complementary', { name: 'Actions', exact: true })
  const actionList = rail.getByRole('button', { name: 'Action List', exact: true })
  if (await actionList.getAttribute('aria-expanded') === 'false') await actionList.click()
  const action = rail.locator('.debugger-action-row-main').filter({ hasText: name })
  await expect(action).toBeVisible()
  await action.click()
  if (await panes.isVisible()) await panes.getByRole('button', { name: 'Editor', exact: true }).click()
}

function responseEvidenceGeometry(element: Element) {
  const bounds = element.getBoundingClientRect()
  const main = element.closest('.main-content')
  const header = main?.querySelector(':scope > .main-topbar')
  let clipTop = Math.max(0, header?.getBoundingClientRect().bottom || 0) + 8
  let clipBottom = window.innerHeight - 8
  for (let parent = element.parentElement; parent; parent = parent.parentElement) {
    if (!/^(auto|scroll|hidden|clip)$/.test(getComputedStyle(parent).overflowY)) continue
    const port = parent.getBoundingClientRect()
    clipTop = Math.max(clipTop, port.top + parent.clientTop + 8)
    clipBottom = Math.min(clipBottom, port.top + parent.clientTop + parent.clientHeight - 8)
  }
  const unoccluded = [0.1, 0.5, 0.9].every(fraction => {
    const hit = document.elementFromPoint(bounds.left + bounds.width / 2, bounds.top + bounds.height * fraction)
    return hit !== null && element.contains(hit)
  })
  return {
    fully_visible: bounds.top >= clipTop && bounds.bottom <= clipBottom && unoccluded,
    focused_height: Math.ceil(bounds.height),
    available_height: Math.max(0, Math.floor(clipBottom - clipTop)),
    focus_top: Math.round(bounds.top), focus_bottom: Math.round(bounds.bottom),
    clip_top: Math.round(clipTop), clip_bottom: Math.round(clipBottom), unoccluded,
  }
}

async function alignEvidenceScrollports(focus: Locator) {
  await focus.evaluate(element => {
    const ancestry: HTMLElement[] = []
    for (let parent = element.parentElement; parent; parent = parent.parentElement) ancestry.push(parent)
    const clips = (node: HTMLElement) => /^(auto|scroll|hidden|clip)$/.test(getComputedStyle(node).overflowY)
    const scrollports = ancestry.filter(parent => /^(auto|scroll)$/.test(getComputedStyle(parent).overflowY) && parent.scrollHeight > parent.clientHeight)
    // Bring the next nested port into view before aligning the leaf within it.
    // A hidden main frame is not the debugger's scroll surface.
    for (let pass = 0; pass < 3; pass++) {
      for (const port of [...scrollports].reverse()) {
        const index = ancestry.indexOf(port)
        const target = ancestry.slice(0, index).reverse().find(clips) || element
        const main = element.closest('.main-content')
        const header = main?.querySelector(':scope > .main-topbar')
        let safeTop = Math.max(0, header?.getBoundingClientRect().bottom || 0)
        for (const parent of ancestry.slice(index)) {
          if (clips(parent)) safeTop = Math.max(safeTop, parent.getBoundingClientRect().top + parent.clientTop)
        }
        port.scrollTop += target.getBoundingClientRect().top - safeTop - 16
      }
    }
  })
}

async function positionEvidence(focus: Locator) {
  await expect.poll(async () => {
    await alignEvidenceScrollports(focus)
    return focus.evaluate(responseEvidenceGeometry)
  }, { message: 'Response evidence must be fully visible within all scrollports below the sticky page header' }).toMatchObject({ fully_visible: true })
}

async function screenshot(page: Page, info: TestInfo, name: string, focus: Locator, narrowName?: string, readableIssue = false) {
  await positionEvidence(focus)
  await page.screenshot({ path: info.outputPath(name) })
  if (!narrowName) return
  const originalViewport = page.viewportSize()
  if (!originalViewport) throw new Error('Narrow evidence screenshots require a managed viewport')
  const collapseSidebar = page.getByRole('button', { name: 'Collapse sidebar', exact: true })
  const collapsedForEvidence = await collapseSidebar.isVisible()
  try {
    // Use the operator's control before its responsive rail hides the toggle.
    if (collapsedForEvidence) await collapseSidebar.click()
    await expect(page.locator('.sidebar')).toHaveClass(/collapsed/)
    await page.setViewportSize({ width: 390, height: Math.max(originalViewport.height, 900) })
    await expect(focus).toBeVisible()
    const debugHeader = page.locator('.main-content:has(.editor-body.workspace-debug) > .main-topbar')
    if (await debugHeader.isVisible()) {
      const headerGeometry = () => debugHeader.evaluate(header => {
        const bounds = header.getBoundingClientRect()
        const controls = Array.from(header.querySelectorAll('button')).filter(button => button.getClientRects().length > 0 && getComputedStyle(button).visibility !== 'hidden')
        return {
          fits_viewport: bounds.left >= -1 && bounds.right <= window.innerWidth + 1 && header.scrollWidth <= header.clientWidth + 1,
          controls_fit_header: controls.length > 0 && controls.every(button => {
            const control = button.getBoundingClientRect()
            return control.left >= bounds.left - 1 && control.right <= bounds.right + 1 && control.top >= bounds.top - 1 && control.bottom <= bounds.bottom + 1
          }),
          header_width: Math.round(bounds.width), header_height: Math.round(bounds.height), control_count: controls.length,
        }
      })
      try {
        await expect.poll(headerGeometry, { message: 'Every narrow debugger header control must remain within the visible header' }).toMatchObject({ fits_viewport: true, controls_fit_header: true })
      } finally {
        await info.attach('narrow-debug-header-geometry', { body: JSON.stringify(await headerGeometry()), contentType: 'application/json' })
      }
    }
    await expect.poll(async () => focus.evaluate(element => {
      const bounds = element.getBoundingClientRect()
      return bounds.left >= -1 && bounds.right <= window.innerWidth + 1 && element.scrollWidth <= element.clientWidth + 1
    }), { message: 'Focused response evidence must fit the 390px viewport without horizontal clipping' }).toBe(true)
    if (readableIssue) {
      const issueGeometry = () => focus.evaluate(element => {
        const layout = element.closest('.shared-import-issue-layout')
        const paragraph = element.querySelector('.shared-import-issue-resolution p')
        const queue = layout?.querySelector('.shared-import-issue-list')
        if (!layout || !paragraph || !queue) return { readable: false }
        const detailBounds = element.getBoundingClientRect()
        const layoutBounds = layout.getBoundingClientRect()
        const proseWidth = paragraph.getBoundingClientRect().width
        return {
          readable: detailBounds.width >= 220 && proseWidth >= 180
            && detailBounds.width >= layoutBounds.width - 1 && queue.getBoundingClientRect().bottom <= detailBounds.top,
          article_width: Math.round(detailBounds.width), prose_width: Math.round(proseWidth), layout_width: Math.round(layoutBounds.width),
        }
      })
      try {
        await expect.poll(issueGeometry, { message: 'The selected issue must use the narrow pane width with readable prose below its queue' }).toMatchObject({ readable: true })
      } finally {
        await info.attach('narrow-selected-issue-geometry', { body: JSON.stringify(await issueGeometry()), contentType: 'application/json' })
      }
    }
    for (let pass = 0; pass < 4; pass++) {
      await alignEvidenceScrollports(focus)
      const geometry = await focus.evaluate(responseEvidenceGeometry)
      if (geometry.available_height >= geometry.focused_height + 16) break
      const height = page.viewportSize()!.height + geometry.focused_height - geometry.available_height + 32
      if (height > 1400) {
        await info.attach('narrow-response-evidence-geometry', { body: JSON.stringify({ viewport: page.viewportSize(), sidebar_collapsed: true, next_height: height, ...geometry }), contentType: 'application/json' })
      }
      expect(height, 'The actual evidence scrollport must fit a single receipt or issue within a bounded narrow screenshot').toBeLessThanOrEqual(1400)
      await page.setViewportSize({ width: 390, height })
    }
    await positionEvidence(focus)
    await info.attach('narrow-response-evidence-geometry', { body: JSON.stringify({ viewport: page.viewportSize(), sidebar_collapsed: true, ...(await focus.evaluate(responseEvidenceGeometry)) }), contentType: 'application/json' })
    await page.screenshot({ path: info.outputPath(narrowName) })
    await focus.screenshot({ path: info.outputPath(narrowName.replace('.png', '-focused.png')) })
  } finally {
    await page.setViewportSize(originalViewport)
    if (collapsedForEvidence) await page.getByRole('button', { name: 'Expand sidebar', exact: true }).click()
  }
}

test('HAR decoded base64 content retains its correlations and real gzip replay extracts a fresh value', async ({ page }, info) => {
  test.setTimeout(120_000)
  const target = await compressedTarget()
  try {
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
    const workspace = await createWorkspace(page.context().request)
    const body = JSON.stringify({ csrf_token: capturedToken, label: responseLabel })
    const { controls, script } = await inspectHAR(page, workspace, 'Decoded_response_content', [harEntry(target.base, '/form', body, { base64: true }), harEntry(target.base, '/submit', '{"accepted":true}', { submit: true })])
    expect(script.actions).toHaveLength(2)
    expect(script.actions[0].metadata?.response_content_capture).toMatchObject({ version: 1, captured: 'har_decoded', stored: 'decoded', state: 'complete', decoded_size_bytes: Buffer.byteLength(body) })
    expect(script.actions.every(action => !action.metadata?.execution_blocker)).toBe(true)
    expect(script.actions[0].extractors).toEqual(expect.arrayContaining([expect.objectContaining({ name: 'corr_csrf_token', type: 'jsonpath', source: 'body', expression: '$.csrf_token' })]))
    expect(script.actions[1].body).toBe('csrf_token={{var.corr_csrf_token}}')
    await expect(controls.getByText('Import review has blockers. Resolve them before replay.', { exact: false })).toHaveCount(0)
    await screenshot(page, info, 'response-content-har-review.png', controls.getByRole('tablist', { name: 'Import review sections' }))
    const id = await saveAndOpen(page, controls)
    const stored = (await json(await page.context().request.get(`/api/v1/scripts/${id}`))).script as unknown as ScriptDefinition
    expect(stored.actions[0].metadata?.response_content_capture).toEqual(script.actions[0].metadata?.response_content_capture)
    expect(stored.actions[0].extractors).toEqual(script.actions[0].extractors)
    await reopenSavedScript(page, workspace, id)
    await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
    await page.locator('.topbar-debug-action-run').click()
    await expect(page.getByLabel('Script evidence states')).toContainText('Debugged: passed')
    expect(target.stats).toMatchObject({ formCalls: 1, submitCalls: 1, accepted: 1 })
    expect(target.stats.encodedBytes).toBeGreaterThan(0)
    const downloaded = page.waitForEvent('download')
    await page.getByRole('button', { name: 'Export session', exact: true }).click()
    const path = await (await downloaded).path(); expect(path).toBeTruthy()
    const report = JSON.parse(await readFile(path!, 'utf8')) as { report: { success: boolean; events: { success: boolean; error_message?: string; protocol_evidence?: Record<string, unknown> }[] } }
    expect(report.report.success).toBe(true)
    expect(report.report.events).toHaveLength(2)
    expect(report.report.events.every(event => event.success && !event.error_message)).toBe(true)
    expect(report.report.events[0].protocol_evidence).toMatchObject({ response_content_state: 'complete', response_content_basis: 'decoded', response_content_source: 'wire' })
    await selectReplayAction(page, script.actions[0].name)
    const evidence = page.getByRole('region', { name: 'HTTP response content evidence', exact: true })
    await expect(evidence).toContainText('Complete response')
    await expect(evidence).toContainText('Decoded response content')
    await screenshot(page, info, 'response-content-gzip-replay.png', evidence, 'response-content-gzip-replay-narrow.png')
    await info.attach('compressed-target-counts', { body: JSON.stringify(target.stats), contentType: 'application/json' })
  } finally { await target.close() }
})

test('incomplete recorder response source remains a visible execution blocker through save and reopen', async ({ page }, info) => {
  test.setTimeout(120_000)
  const target = await compressedTarget()
  try {
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
    const workspace = await createWorkspace(page.context().request)
    const canary = 'synthetic-partial-response-canary'
    const envelope = { version: 1, captured: 'wire', stored: 'unavailable', state: 'unavailable', reason_code: 'response_content_truncated' }
    const { controls, script } = await inspectHAR(page, workspace, 'Incomplete_response_content', [harEntry(target.base, '/partial', JSON.stringify({ marker: canary }), { envelope })])
    expect(script.actions).toHaveLength(1)
    const action = script.actions[0]
    expect(action.metadata?.response_content_capture).toEqual(envelope)
    expect(action.metadata?.execution_blocker).toBe('import_review_required')
    expect(action.extractors || []).toEqual([])
    expect(JSON.stringify(action)).not.toContain(canary)
    const issues = controls.getByRole('region', { name: 'Resolve import issues', exact: true })
    await issues.getByRole('searchbox', { name: 'Find an issue', exact: true }).fill('response_content_truncated')
    await issues.getByRole('button', { name: /Review unavailable response content/ }).click()
    const selected = issues.getByRole('article', { name: 'Selected issue', exact: true })
    await expect(selected.getByRole('region', { name: 'Available resolution', exact: true })).toContainText('cannot clear this execution blocker')
    await expect(selected.getByRole('button', { name: /Mark reviewed/ })).toHaveCount(0)
    await screenshot(page, info, 'response-content-blocker.png', selected, 'response-content-blocker-narrow.png', true)
    await selected.getByRole('button', { name: 'Inspect source evidence', exact: true }).click()
    await expect(controls.getByRole('tab', { name: 'Source Mapping', exact: true })).toHaveAttribute('aria-selected', 'true')
    await expect(controls.getByRole('navigation', { name: 'Review mapping navigation', exact: true })).toBeVisible()
    await screenshot(page, info, 'response-content-retained-source.png', controls.getByRole('navigation', { name: 'Review mapping navigation', exact: true }))
    const id = await saveAndOpen(page, controls)
    await reopenSavedScript(page, workspace, id)
    const reopened = (await json(await page.context().request.get(`/api/v1/scripts/${id}`))).script as unknown as ScriptDefinition
    expect(reopened.actions[0].metadata?.response_content_capture).toEqual(envelope)
    expect(reopened.actions[0].metadata?.execution_blocker).toBe('import_review_required')
    expect(reopened.actions[0].extractors || []).toEqual([])
    await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
    const readiness = page.locator('details.script-health-summary')
    await readiness.locator('summary').first().click()
    const rechecked = mutation(page, 'POST', `/api/v1/scripts/${id}/validate`)
    await readiness.getByRole('button', { name: 'Recheck', exact: true }).click()
    const validation = await json(await rechecked)
    // The DSL is structurally valid; replay readiness independently retains
    // the imported source blocker and must reject execution.
    expect(validation.valid).toBe(true)
    expect(validation.preflight).toMatchObject({ valid: false, checks: expect.arrayContaining([
      expect.objectContaining({ id: 'review_gates', status: 'fail', detail: expect.stringContaining('import_review_required') }),
    ]) })
    const failedCheck = readiness.locator('.replay-preflight-check.fail').filter({ hasText: 'import_review_required' })
    await expect(failedCheck).toBeVisible()
    await screenshot(page, info, 'response-content-reopened-blocker.png', failedCheck)
    await readiness.locator('summary').first().click()
    const attemptedRun = mutation(page, 'POST', `/api/v1/scripts/${id}/debug/sessions`)
    await page.locator('.topbar-debug-action-run').click()
    const rejectedRun = await attemptedRun
    expect(rejectedRun.status()).toBe(400)
    expect((await rejectedRun.json()).error).toContain('import_review_required')
    await expect(page.locator('.editor-toast.error').filter({ hasText: 'import_review_required' }).first()).toBeVisible()
    const rejectedStoredReplay = await page.context().request.post(`/api/v1/scripts/${id}/replay`, { data: { project_id: workspace.projectId } })
    expect(rejectedStoredReplay.status()).toBe(400)
    expect((await rejectedStoredReplay.json()).error).toContain('import_review_required')
    expect(target.stats.totalRequests).toBe(0)
    await info.attach('blocked-response-target-counts', { body: JSON.stringify(target.stats), contentType: 'application/json' })
  } finally { await target.close() }
})

test('gzip SSE collection extracts sampled events and visibly qualifies the partial response', async ({ page }, info) => {
  test.setTimeout(120_000)
  const target = await compressedTarget()
  try {
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
    const workspace = await createWorkspace(page.context().request)
    const script: ScriptDefinition = { name: 'Partial_compressed_stream', version: '1', protocol: 'http', config: { base_url: target.base, results: { message_detail: { mode: 'sampled', sample_rate: 1, max_items: 4, max_item_bytes: 65536, max_total_bytes: 131072 } } }, actions: [
      { name: 'Collect compressed events', type: 'GET', target: '/events', headers: { 'Accept-Encoding': 'gzip' }, params: { stream: true, stop_on_event: 'done', stream_timeout_ms: 5000 }, extractors: [{ name: 'sampled_stream_value', source: 'body', type: 'sse_event', expression: 'token', on_miss: 'fail' }], assertions: [{ type: 'status_code', expected: 200 }] },
      { name: 'Consume sampled value', type: 'GET', target: '/stream-consume?value={{var.sampled_stream_value}}', assertions: [{ type: 'status_code', expected: 200 }] },
    ] }
    const id = idOf(await json(await page.context().request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script } })))
    await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
    await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
    await page.getByRole('button', { name: 'Debug options (0)', exact: true }).click()
    const options = page.getByRole('region', { name: 'Session Overrides', exact: true })
    await options.getByRole('combobox', { name: 'Evidence capture', exact: true }).selectOption('diagnostic')
    await options.getByRole('button', { name: 'Done', exact: true }).click()
    await page.locator('.topbar-debug-action-run').click()
    await expect(page.getByLabel('Script evidence states')).toContainText('Debugged: passed')
    expect(target.stats).toMatchObject({ streamCalls: 1, streamConsumerCalls: 1, acceptedStreamConsumers: 1, streamEndCalls: 0 })
    await selectReplayAction(page, 'Collect compressed events')
    const evidence = page.getByRole('region', { name: 'HTTP response content evidence', exact: true })
    await expect(evidence).toContainText('Collection window (partial response)')
    await expect(evidence).toContainText('Decoded response content')
    await expect(evidence).toContainText('Full-response completion was not established')
    await screenshot(page, info, 'response-content-partial-stream.png', evidence, 'response-content-partial-stream-narrow.png')
    const downloaded = page.waitForEvent('download')
    await page.getByRole('button', { name: 'Export session', exact: true }).click()
    const path = await (await downloaded).path(); expect(path).toBeTruthy()
    const exported = JSON.parse(await readFile(path!, 'utf8')) as { report: { success: boolean; events: { action_name: string; protocol_evidence?: Record<string, unknown>; snapshot?: { response_body_state?: string; response_body?: string } }[] } }
    expect(exported.report.success).toBe(true)
    const event = exported.report.events.find(item => item.action_name === 'Collect compressed events')
    expect(event?.protocol_evidence).toMatchObject({ response_content_state: 'collection_window', response_content_basis: 'decoded' })
    expect(event?.snapshot?.response_body_state).toBe('partial')
    expect(event?.snapshot?.response_body).toContain('event: done')
    await info.attach('partial-stream-counts', { body: JSON.stringify(target.stats), contentType: 'application/json' })
  } finally { await target.close() }
})
