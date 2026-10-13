import { createSecureServer, type Http2ServerResponse } from 'node:http2'
import { createServer } from 'node:https'
import type { ServerResponse } from 'node:http'
import { execFileSync } from 'node:child_process'
import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { readFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import type { Socket } from 'node:net'
import type { Page, TestInfo } from '@playwright/test'
import type { ScriptActionDefinition, ScriptDefinition } from '../src/types/scripts'
import { test, expect, idOf, json, login, expectEditorSaved, expectDebugOutcome } from './support/workflow'
import { createWorkspace, scopeQuery, type Workspace } from './support/workspace'

// Protocol provenance sits in a closed "Protocol evidence" disclosure under the response.
async function openProtocolEvidence(page: Page) {
  const disclosure = page.locator('details.replay-protocol-evidence')
  await expect(disclosure).toBeVisible()
  if (!await disclosure.evaluate(element => (element as HTMLDetailsElement).open)) await disclosure.locator('summary').click()
}

// Two real TLS targets distinguish exact selection from automatic negotiation.
// The certificate and key exist only inside this fixture's disposable directory.
async function versionTarget() {
  const directory = mkdtempSync(join(tmpdir(), 'apex-http-version-fixture-'))
  const sockets = new Set<Socket>()
  const timers = new Set<ReturnType<typeof setTimeout>>()
  const stats = { arrivals: 0, active: 0, maximumActive: 0, parentCookies: 0, versions: [] as { index: number; version: string }[], completed: [] as number[], autoCalls: 0, acceptedAuto: 0, consumerCalls: 0, acceptedConsumers: 0 }
  let first: ServerResponse | Http2ServerResponse | undefined
  let second: ServerResponse | Http2ServerResponse | undefined
  let firstVersion = '', secondVersion = ''
  const parentCookie = 'version_session=synthetic-version-parent'
  let mixed: ReturnType<typeof createSecureServer> | undefined
  let h1: ReturnType<typeof createServer> | undefined
  try {
    execFileSync('openssl', ['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', join(directory, 'key.pem'), '-out', join(directory, 'cert.pem'), '-subj', '/CN=localhost', '-days', '1'], { stdio: 'ignore' })
    const tls = { key: readFileSync(join(directory, 'key.pem')), cert: readFileSync(join(directory, 'cert.pem')) }
    mixed = createSecureServer({ ...tls, allowHTTP1: true }, (request, response) => {
      const url = new URL(request.url!, 'https://fixture')
      const version = request.httpVersion.startsWith('2') ? '2' : request.httpVersion
      response.setHeader('Content-Type', 'application/json')
      if (url.pathname === '/login') {
        response.setHeader('Set-Cookie', `${parentCookie}; Path=/; Secure; SameSite=Lax`)
        response.end('{"ready":true}')
        return
      }
      if (url.pathname === '/group/1' || url.pathname === '/group/2') {
        const index = url.pathname.endsWith('/1') ? 1 : 2
        stats.arrivals++; stats.active++; stats.maximumActive = Math.max(stats.maximumActive, stats.active)
        stats.versions.push({ index, version })
        if (request.headers.cookie?.includes(parentCookie)) stats.parentCookies++
        let settled = false
        const settle = () => { if (!settled) { settled = true; stats.active-- } }
        response.once('finish', settle); response.once('close', settle)
        if (index === 1) {
          first = response; firstVersion = version
          const timer = setTimeout(() => { timers.delete(timer); if (!response.writableEnded) { response.statusCode = 504; response.end('{"error":"unreleased fixture barrier"}') } }, 30_000)
          timer.unref(); timers.add(timer)
        } else { second = response; secondVersion = version }
        if (first && second && !second.writableEnded) {
          stats.completed.push(2)
          second.setHeader('Set-Cookie', 'version_second=yes; Path=/; Secure; SameSite=Lax')
          second.end(JSON.stringify({ version: secondVersion }))
        }
        return
      }
      if (url.pathname === '/consume') {
        stats.consumerCalls++
        const accepted = version === '2' && request.headers.cookie?.includes(parentCookie) && request.headers.cookie.includes('version_first=yes') && request.headers.cookie.includes('version_second=yes') && url.searchParams.get('first') === '1.1' && url.searchParams.get('second') === '2'
        if (accepted) stats.acceptedConsumers++
        response.statusCode = accepted ? 200 : 409
        response.end(JSON.stringify({ accepted: !!accepted }))
        return
      }
      response.statusCode = 404; response.end('{"error":"unknown fixture route"}')
    })
    h1 = createServer(tls, (request, response) => {
      stats.autoCalls++
      const accepted = request.url === '/auto' && request.httpVersion === '1.1' && request.headers.cookie?.includes(parentCookie) && request.headers.cookie.includes('version_first=yes') && request.headers.cookie.includes('version_second=yes')
      if (accepted) stats.acceptedAuto++
      response.setHeader('Content-Type', 'application/json'); response.statusCode = accepted ? 200 : 409
      response.end(JSON.stringify({ accepted: !!accepted }))
    })
    for (const server of [mixed, h1]) {
      server.on('connection', socket => { sockets.add(socket); socket.once('close', () => sockets.delete(socket)) })
      await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
    }
    const mixedAddress = mixed.address(), h1Address = h1.address()
    if (!mixedAddress || typeof mixedAddress === 'string' || !h1Address || typeof h1Address === 'string') throw new Error('Missing TLS loopback address')
    return {
      base: `https://127.0.0.1:${mixedAddress.port}`, auto: `https://127.0.0.1:${h1Address.port}/auto`, stats,
      releaseFirst() {
        expect(first, 'Both different-version siblings must reach the actual target').toBeDefined()
        expect(stats.arrivals).toBe(2); expect(first!.writableEnded).toBe(false)
        for (const timer of timers) clearTimeout(timer)
        timers.clear(); stats.completed.push(1)
        first!.setHeader('Set-Cookie', 'version_first=yes; Path=/; Secure; SameSite=Lax')
        first!.end(JSON.stringify({ version: firstVersion }))
      },
      async close() {
        for (const timer of timers) clearTimeout(timer)
        for (const socket of sockets) socket.destroy()
        await Promise.all([mixed!, h1!].map(server => new Promise<void>(resolve => server.close(() => resolve()))))
        rmSync(directory, { recursive: true, force: true })
      },
    }
  } catch (error) {
    for (const timer of timers) clearTimeout(timer)
    for (const socket of sockets) socket.destroy()
    mixed?.close(); h1?.close(); rmSync(directory, { recursive: true, force: true })
    throw error
  }
}
async function openEditor(page: Page, workspace: Workspace, id: string) {
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await expect(page.getByRole('group', { name: 'Script workspace' })).toBeVisible()
}
async function selectLeaf(page: Page, name: string) {
  await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Author', exact: true }).click()
  const outline = page.getByRole('navigation', { name: 'Script outline' })
  const leaf = outline.getByRole('treeitem', { name }).first()
  if (!(await leaf.isVisible())) await outline.getByRole('treeitem', { name: /Mixed versions/ }).press('ArrowRight')
  await leaf.click()
  await expect(page.getByLabel('Request HTTP version', { exact: true })).toBeVisible()
}
async function capturePolicy(page: Page, info: TestInfo, name: string) {
  const control = page.getByLabel('Request HTTP version', { exact: true })
  await control.evaluate(element => element.scrollIntoView({ block: 'center', inline: 'nearest' }))
  const box = await control.boundingBox()
  expect(box).not.toBeNull(); expect(box!.x).toBeGreaterThanOrEqual(0)
  expect(box!.x + box!.width).toBeLessThanOrEqual(page.viewportSize()!.width + 1)
  await page.screenshot({ path: info.outputPath(name) })
}

test('mixed HTTP versions retain request policy, share sessions and expose measured replay versions', async ({ page }, info) => {
  test.setTimeout(150_000)
  const target = await versionTarget()
  try {
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
    const request = page.context().request, workspace = await createWorkspace(request)
    const children: ScriptActionDefinition[] = [1, 2].map(index => ({ name: index === 1 ? 'Exact H1 sibling' : 'Exact H2 sibling', type: 'GET', target: `/group/${index}`, metadata: { fixture_tag: `version-child-${index}` }, extractors: [{ name: index === 1 ? 'version_first' : 'version_second', source: 'body', type: 'jsonpath', expression: '$.version' }], assertions: [{ type: 'status_code', expected: 200 }] }))
    const script: ScriptDefinition = { name: 'Mixed HTTP version session', version: '1', protocol: 'http', config: { base_url: target.base, http: { version: '2', max_conns_per_host: 2, tls: { insecure_skip_verify: true } } }, actions: [
      { name: 'Login', type: 'GET', target: '/login' },
      { name: 'Mixed versions', type: 'http_sequence', target: '', params: { requests: children, execution_mode: 'concurrent', max_concurrency: 2, future_note: { keep: true } } },
      { name: 'Auto on H1 only', type: 'GET', target: target.auto },
      { name: 'Consume joined versions', type: 'GET', target: '/consume?first={{var.version_first}}&second={{var.version_second}}', assertions: [{ type: 'status_code', expected: 200 }] },
    ] }
    const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script } })))
    const initial = (await json(await request.get(`/api/v1/scripts/${id}`))).script as unknown as ScriptDefinition
    await page.setViewportSize({ width: 1600, height: 1050 }); await openEditor(page, workspace, id)
    await selectLeaf(page, 'Exact H1 sibling')
    const control = page.getByLabel('Request HTTP version', { exact: true })
    await expect(control).toHaveValue('inherit')
    await expect(control.locator('option:checked')).toContainText('HTTP/2')
    await control.selectOption('1.1')
    await page.getByRole('button', { name: 'Save script', exact: true }).click()
    await expectEditorSaved(page)
    await control.selectOption('inherit')
    await page.getByRole('button', { name: 'Save script', exact: true }).click()
    await expectEditorSaved(page)
    const inherited = (await json(await request.get(`/api/v1/scripts/${id}`))).script as unknown as ScriptDefinition
    expect(inherited.actions[1].params).toEqual(initial.actions[1].params)
    await control.selectOption('1.1')
    await capturePolicy(page, info, 'http-version-desktop.png')
    await page.setViewportSize({ width: 760, height: 1000 }); await capturePolicy(page, info, 'http-version-narrow.png')
    await page.setViewportSize({ width: 1600, height: 1050 })
    await selectLeaf(page, 'Exact H2 sibling'); await control.selectOption('2')
    await selectLeaf(page, 'Auto on H1 only'); await control.selectOption('auto')
    await page.getByRole('button', { name: 'Save script', exact: true }).click()
    await expectEditorSaved(page)
    const saved = (await json(await request.get(`/api/v1/scripts/${id}`))).script as unknown as ScriptDefinition
    expect(saved.actions[1].params).toEqual({ ...initial.actions[1].params, requests: children.map((child, index) => ({ ...child, http_version: index === 0 ? '1.1' : '2' })) })
    expect(saved.actions[2]).toEqual({ ...initial.actions[2], http_version: 'auto' })
    await openEditor(page, workspace, id); await selectLeaf(page, 'Exact H1 sibling'); await expect(control).toHaveValue('1.1')
    await selectLeaf(page, 'Exact H2 sibling'); await expect(control).toHaveValue('2')
    await selectLeaf(page, 'Auto on H1 only'); await expect(control).toHaveValue('auto')
    await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
    await page.getByRole('button', { name: 'Debug options (0)', exact: true }).click()
    const options = page.getByRole('region', { name: 'Session Overrides' })
    await options.getByRole('checkbox', { name: 'Step through (start paused)' }).check()
    await options.getByRole('checkbox', { name: 'Break on failure', exact: true }).uncheck()
    await options.getByRole('combobox', { name: 'Evidence capture', exact: true }).selectOption('diagnostic')
    await options.getByRole('button', { name: 'Done', exact: true }).click()
    await page.locator('.topbar-debug-action-run').click()
    await expect(page.getByRole('status').filter({ hasText: 'Paused before: Login' })).toBeVisible()
    await page.getByRole('button', { name: 'Step', exact: true }).click()
    await expect(page.getByRole('status').filter({ hasText: 'Paused before: Mixed versions' })).toBeVisible()
    await page.getByRole('button', { name: 'Step', exact: true }).click()
    await expect.poll(() => target.stats.arrivals).toBe(2)
    await expect.poll(() => target.stats.completed).toEqual([2])
    expect(target.stats.versions.slice().sort((a, b) => a.index - b.index)).toEqual([{ index: 1, version: '1.1' }, { index: 2, version: '2' }])
    expect(target.stats.maximumActive).toBe(2); expect(target.stats.parentCookies).toBe(2)
    expect(target.stats.autoCalls).toBe(0); expect(target.stats.consumerCalls).toBe(0)
    target.releaseFirst()
    await expect(page.getByRole('status').filter({ hasText: 'Paused before: Auto on H1 only' })).toBeVisible()
    expect(target.stats.completed).toEqual([2, 1]); expect(target.stats.autoCalls).toBe(0)
    await page.getByRole('button', { name: 'Continue', exact: true }).click()
    await expectDebugOutcome(page, 'passed')
    expect(target.stats.autoCalls).toBe(1); expect(target.stats.acceptedAuto).toBe(1)
    expect(target.stats.consumerCalls).toBe(1); expect(target.stats.acceptedConsumers).toBe(1)
    const download = page.waitForEvent('download')
    await page.getByRole('button', { name: 'Export session', exact: true }).click()
    const path = await (await download).path(); expect(path).toBeTruthy()
    const exported = JSON.parse(await readFile(path!, 'utf8')) as { report: { success: boolean; events: { action_name: string; parent_action_name?: string; request_index?: number; protocol_evidence?: Record<string, unknown> }[] } }
    expect(exported.report.success).toBe(true)
    const siblings = exported.report.events.filter(event => event.parent_action_name === 'Mixed versions' && event.request_index !== undefined)
    expect(siblings.map(event => event.request_index)).toEqual([0, 1])
    expect(siblings.map(event => event.protocol_evidence?.http_version_policy)).toEqual(['1.1', '2'])
    expect(siblings.map(event => event.protocol_evidence?.http_version_negotiated)).toEqual(['1.1', '2'])
    expect(siblings.every(event => event.protocol_evidence?.http_version_source === 'wire')).toBe(true)
    const auto = exported.report.events.find(event => event.action_name === 'Auto on H1 only')
    expect(auto?.protocol_evidence).toMatchObject({ http_version_policy: 'auto', http_version_negotiated: '1.1', http_version_source: 'wire' })
    await page.locator('.debugger-action-row-main').filter({ hasText: 'Consume joined versions' }).click()
    await openProtocolEvidence(page)
    const evidence = page.getByRole('region', { name: 'HTTP version evidence', exact: true })
    const actual = evidence.locator('dt').filter({ hasText: 'Actual negotiated HTTP version' }).locator('xpath=following-sibling::dd[1]')
    await expect(actual).toHaveText('HTTP/2')
    await page.locator('.debugger-action-row-main').filter({ hasText: 'Auto on H1 only' }).click()
    await openProtocolEvidence(page)
    await expect(actual).toHaveText('HTTP/1.1')
    await expect(evidence.locator('dt').filter({ hasText: 'Replay policy' }).locator('xpath=following-sibling::dd[1]')).toContainText('Auto')
    await expect(evidence.locator('dt').filter({ hasText: 'Version evidence source' }).locator('xpath=following-sibling::dd[1]')).toHaveText('Wire response')
    await actual.scrollIntoViewIfNeeded()
    await page.screenshot({ path: info.outputPath('http-version-replay-desktop.png') })
    await info.attach('mixed-version-counts', { body: JSON.stringify(target.stats), contentType: 'application/json' })
  } finally { await target.close() }
})
