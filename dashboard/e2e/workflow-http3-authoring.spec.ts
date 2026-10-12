import { createSecureServer } from 'node:http2'
import { execFileSync } from 'node:child_process'
import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import type { Socket } from 'node:net'
import type { Locator, Page } from '@playwright/test'
import type { ScriptDefinition } from '../src/types/scripts'
import { test, expect, idOf, json, login, expectEditorSaved, expectDebugOutcome } from './support/workflow'
import { createWorkspace, scopeQuery, type Workspace } from './support/workspace'

// A TLS target that answers over HTTP/2 and advertises HTTP/3 (Alt-Svc). The
// advertisement is all replay needs to offer auto+h3, so no QUIC listener is
// started. The certificate exists only in this fixture's disposable directory.
async function advertisingTarget() {
  const directory = mkdtempSync(join(tmpdir(), 'apex-http3-authoring-'))
  const sockets = new Set<Socket>()
  try {
    execFileSync('openssl', ['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', join(directory, 'key.pem'), '-out', join(directory, 'cert.pem'), '-subj', '/CN=localhost', '-days', '1'], { stdio: 'ignore' })
    const server = createSecureServer({ key: readFileSync(join(directory, 'key.pem')), cert: readFileSync(join(directory, 'cert.pem')), allowHTTP1: true }, (_request, response) => {
      response.setHeader('Content-Type', 'application/json')
      response.setHeader('Alt-Svc', 'h3=":443"; ma=60')
      response.end('{"ok":true}')
    })
    server.on('connection', socket => { sockets.add(socket); socket.once('close', () => sockets.delete(socket)) })
    await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
    const address = server.address()
    if (!address || typeof address === 'string') throw new Error('Missing TLS loopback address')
    return {
      base: `https://127.0.0.1:${address.port}`,
      async close() {
        for (const socket of sockets) socket.destroy()
        await new Promise<void>(resolve => server.close(() => resolve()))
        rmSync(directory, { recursive: true, force: true })
      },
    }
  } catch (error) {
    rmSync(directory, { recursive: true, force: true })
    throw error
  }
}

async function createScript(page: Page, workspace: Workspace, script: ScriptDefinition): Promise<string> {
  return idOf(await json(await page.context().request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script } })))
}

async function openEditor(page: Page, workspace: Workspace, id: string) {
  await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await expect(page.getByRole('group', { name: 'Script workspace' })).toBeVisible()
}

async function openConnectionSettings(page: Page): Promise<Locator> {
  await page.getByRole('tab', { name: 'Runtime Settings', exact: true }).click()
  const runtime = page.getByRole('region', { name: 'Runtime Settings', exact: true })
  await runtime.getByRole('navigation', { name: 'Runtime setting categories' }).getByRole('tab', { name: 'HTTP Transport', exact: true }).click()
  await runtime.getByRole('tab', { name: /^Connection/ }).click()
  return runtime
}

const quicField = (runtime: Locator, label: string) => runtime.locator('fieldset.http-quic-settings label').filter({ has: runtime.page().locator('span', { hasText: label }) }).locator('input, select')

test('HTTP/3 authoring: QUIC settings, field validation, capability note and the switch to auto+h3', async ({ page }, info) => {
  test.setTimeout(180_000)
  const target = await advertisingTarget()
  try {
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
    const request = page.context().request
    const workspace = await createWorkspace(request)
    const capabilities = await json(await request.get('/api/v1/scripts/authoring/capabilities')) as { http_runtime: { http3: { exact: boolean } } }
    const id = await createScript(page, workspace, {
      name: 'HTTP3 authoring', version: '1', protocol: 'http',
      config: { base_url: target.base, http: { version: '2', tls: { insecure_skip_verify: true } } },
      actions: [{ name: 'Home', type: 'GET', target: '/' }],
    })
    await page.setViewportSize({ width: 1440, height: 1000 })
    await openEditor(page, workspace, id)
    const runtime = await openConnectionSettings(page)

    // Exact HTTP/3: the capability note follows what this controller reports.
    await runtime.locator('label').filter({ has: page.locator('span', { hasText: /^HTTP Version$/ }) }).locator('select').selectOption('3')
    await expect(runtime.locator('.http3-execution-note')).toContainText(capabilities.http_runtime.http3.exact ? 'enabled on this controller' : 'not enabled on this installation')
    const quic = runtime.getByRole('group', { name: 'QUIC (HTTP/3)' })
    await expect(quic).toBeVisible()

    // Out-of-range values are reported at the field, then corrected.
    const handshake = quicField(runtime, 'Handshake timeout (ms)')
    await handshake.fill('500')
    await expect(handshake).toHaveAttribute('aria-invalid', 'true')
    await expect(quic.getByRole('alert')).toHaveText('Enter 1,000 to 60,000 ms.')
    await handshake.fill('2500')
    await expect(quic.getByRole('alert')).toHaveCount(0)
    await quicField(runtime, 'Alt-Svc cache').selectOption('per_iteration')
    await page.getByRole('button', { name: 'Save script', exact: true }).click()
    await expectEditorSaved(page)
    const saved = (await json(await request.get(`/api/v1/scripts/${id}`))).script as unknown as ScriptDefinition
    expect(saved.config.http).toMatchObject({ version: '3', quic: { handshake_timeout_ms: 2500, alt_svc_cache: 'per_iteration' } })

    // Themes and widths: the panel stays inside the editor without overflow.
    for (const theme of ['dark', 'light', 'solarized-dark', 'solarized-light', 'monokai-dimmed']) {
      for (const width of [923, 1440]) {
        await page.setViewportSize({ width, height: 1000 })
        await page.evaluate(value => { localStorage.setItem('apexload.theme', value); document.documentElement.dataset.theme = value }, theme)
        await quic.scrollIntoViewIfNeeded()
        const layout = await quic.evaluate(element => {
          const box = element.getBoundingClientRect()
          const legend = getComputedStyle(element.querySelector('legend')!)
          const page = getComputedStyle(document.body)
          return { right: box.right, overflow: element.scrollWidth - element.clientWidth, legend: legend.color, background: page.backgroundColor }
        })
        expect(layout.right, `${theme} at ${width}px`).toBeLessThanOrEqual(width + 1)
        expect(layout.overflow, `${theme} at ${width}px`).toBeLessThanOrEqual(1)
        expect(layout.legend, `${theme} legend contrast`).not.toBe(layout.background)
        await quic.screenshot({ path: info.outputPath(`quic-${theme}-${width}.png`), animations: 'disabled' })
      }
    }
    await page.setViewportSize({ width: 1440, height: 1000 })

    // A custom proxy keeps requests on TCP: the panel is disabled with the
    // reason, and exact HTTP/3 is refused at the version field.
    await runtime.getByRole('tab', { name: /^Proxy/ }).click()
    await runtime.locator('label').filter({ has: page.locator('span', { hasText: /^Proxy Mode$/ }) }).locator('select').selectOption('custom')
    await runtime.locator('label').filter({ has: page.locator('span', { hasText: /^Proxy URL$/ }) }).locator('input').fill('http://proxy.example:3128')
    await runtime.getByRole('tab', { name: /^Connection/ }).click()
    // A disabled fieldset disables every control in it.
    await expect(quic).toHaveAttribute('disabled', '')
    await expect(quicField(runtime, 'Handshake timeout (ms)')).toBeDisabled()
    await expect(quic.getByRole('note')).toContainText('custom proxy')
    await expect(runtime.locator('.http3-policy-issue')).toContainText('A proxied route stays on TCP')

    // Replay on HTTP/2 against an origin that advertises HTTP/3, then switch
    // the request to auto+h3 from the evidence.
    const replayId = await createScript(page, workspace, {
      name: 'HTTP3 switch', version: '1', protocol: 'http',
      config: { base_url: target.base, http: { version: '2', tls: { insecure_skip_verify: true } } },
      actions: [{ name: 'Home', type: 'GET', target: '/' }],
    })
    // Leaving discards the unsaved proxy edit.
    await page.goto(`/#/scripts?mode=edit&scriptId=${replayId}&${scopeQuery(workspace)}`)
    const unsaved = page.getByRole('dialog', { name: 'Unsaved Changes' })
    await expect(unsaved).toBeVisible()
    await unsaved.getByRole('button', { name: 'Discard changes', exact: true }).click()
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
    await expect(page.getByRole('group', { name: 'Script workspace' })).toBeVisible()
    expect((await json(await request.get(`/api/v1/scripts/${id}`))).script).toMatchObject({ config: { http: { version: '3' } } })
    await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
    await page.locator('.topbar-debug-action-run').click()
    await expectDebugOutcome(page, 'passed')
    await page.locator('.debugger-action-row-main').filter({ hasText: 'Home' }).click()
    const disclosure = page.locator('details.replay-protocol-evidence')
    if (!await disclosure.evaluate(element => (element as HTMLDetailsElement).open)) await disclosure.locator('summary').click()
    const offer = page.getByRole('group', { name: 'Switch to auto+h3' })
    await expect(offer).toContainText('The origin advertises HTTP/3; this request used HTTP/2. Switch to auto+h3?')
    const validated = page.waitForRequest(candidate => candidate.method() === 'POST' && /\/api\/v1\/scripts\/(validate|[^/]+\/validate)/.test(candidate.url()))
    await offer.getByRole('button', { name: 'Switch this request to auto+h3', exact: true }).click()
    await validated
    await expect(page.getByRole('status').filter({ hasText: 'Switched this request to auto+h3' })).toBeVisible()
    await page.screenshot({ path: info.outputPath('http3-switch.png') })
    await page.getByRole('button', { name: 'Save script', exact: true }).click()
    await expectEditorSaved(page)
    const switched = (await json(await request.get(`/api/v1/scripts/${replayId}`))).script as unknown as ScriptDefinition
    expect(switched.actions[0].http_version).toBe('auto+h3')
    expect(switched.config.http?.version).toBe('2')
  } finally { await target.close() }
})
