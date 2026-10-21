import { createServer } from 'node:http'
import { writeFile } from 'node:fs/promises'
import type { AddressInfo } from 'node:net'
import { test, expect, json, idOf, login, runStatus } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'
import type { BrowserResourcePage, BrowserResourcePageItem } from '../src/types/browserResourcePage'

type Attempt = { path: string; method?: string; userAgent?: string; referer?: string; received: number; finished?: number }
type Entry = { startedDateTime: string; time: number; request: { url: string }; response: { status: number }; timings: Record<string, number> }

for (const [engine, profile] of [['chromium', ''], ['chromium', 'pixel_8'], ['webkit', ''], ['webkit', 'iphone_15_pro']] as const) {
  test(`native ${engine}/${profile || 'desktop'} waterfall and cache evidence reconcile with target and HAR`, async ({ page, trackRun }, testInfo) => {
    test.setTimeout(180_000)
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
    const request = page.context().request, workspace = await createWorkspace(request)
    const attempts: Attempt[] = [], timers = new Set<ReturnType<typeof setTimeout>>()
    const target = createServer((req, res) => {
      const attempt: Attempt = { path: req.url || '', method: req.method, userAgent: req.headers['user-agent'], referer: req.headers.referer, received: Date.now() }; attempts.push(attempt)
      res.on('finish', () => { attempt.finished = Date.now() })
      if (req.url === '/first' || req.url === '/second') {
        res.setHeader('Cache-Control', 'no-store'); res.setHeader('Content-Type', 'text/html')
        const timer = setTimeout(() => { timers.delete(timer); res.end('<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="icon" href="data:,"><link rel="stylesheet" href="/cached.css"><h1>Independent waterfall fixture</h1>') }, 40); timers.add(timer)
      } else if (req.url === '/cached.css') {
        res.setHeader('Cache-Control', 'public, max-age=600'); res.setHeader('Content-Type', 'text/css')
        const timer = setTimeout(() => { timers.delete(timer); res.end('h1{font-size:30px;color:navy}') }, 90); timers.add(timer)
      } else { res.statusCode = 404; res.end() }
    })
    await new Promise<void>(resolve => target.listen(0, '127.0.0.1', resolve))
    const origin = `http://127.0.0.1:${(target.address() as AddressInfo).port}`
    let runID = ''
    try {
      // Absolute navigation with no base_url leaves the controller's separate
      // HTTP latency probe unconfigured; this fixture measures capture traffic.
      const script = { name: 'Independent waterfall fixture', version: '1.0.0', protocol: 'browser', project_id: workspace.projectId,
        config: { browser: { engine, device_profile: profile, headless: true, cache_enabled: true, cache_content_urls: true, check_newer_versions: false, capture_document_metrics: true, client_metrics: true, har_capture: 'always' } },
        actions: [{ name: 'first document', type: 'browser_navigate', target: `${origin}/first` }, { name: 'settle first', type: 'browser_wait', wait_for: { type: 'duration', value: '250ms', timeout_ms: 2000 } }, { name: 'second document', type: 'browser_navigate', target: `${origin}/second` }, { name: 'settle second', type: 'browser_wait', wait_for: { type: 'duration', value: '250ms', timeout_ms: 2000 } }],
      }
      const scriptID = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script } })))
      runID = idOf(await json(await request.post('/api/v1/tests', { data: { project_id: workspace.projectId, application_id: workspace.applicationId, release_id: workspace.releaseId, name: 'Native waterfall qualification', termination_mode: 'iterations', groups: [{ name: 'browser', script_id: scriptID, vusers: 1, iterations: 1 }] } })))
      trackRun(runID); await json(await request.post(`/api/v1/tests/${runID}/start`))
      await expect.poll(() => runStatus(request, runID), { timeout: 90_000 }).toBe('COMPLETE')
      await expect.poll(async () => (await request.get(`/api/v1/tests/${runID}/archive/manifest`)).ok(), { timeout: 60_000 }).toBe(true)
      const listing = await json(await request.get(`/api/v1/tests/${runID}/archive/browser/har`))
      const artifacts = (listing.artifacts as { name: string; fidelity: string; available: boolean }[]).filter(item => item.fidelity === 'native' && item.available); expect(artifacts).toHaveLength(1)
      const harResponse = await request.get(`/api/v1/tests/${runID}/archive/browser/har/${encodeURIComponent(artifacts[0].name)}`)
      expect(harResponse.ok(), await harResponse.text()).toBe(true)
      const entries: Entry[] = (await harResponse.json()).log.entries
      expect(entries).toHaveLength(4)
      const retained = await json(await request.get(`/api/v1/tests/${runID}/archive/browser-resources?project_id=${workspace.projectId}&limit=100&sort_by=native_start_ms&sort_dir=asc`)) as unknown as BrowserResourcePage
      expect(retained.next_cursor).toBeFalsy()
      const unique = new Map<string, BrowserResourcePageItem>()
      for (const row of retained.items) if (row.browser_evidence) unique.set(row.browser_evidence.request_id, row)
      expect(unique.size).toBe(4)
      const css = [...unique.values()].filter(row => row.url === `${origin}/cached.css`).sort((a, b) => a.browser_evidence!.epoch_start_ms! - b.browser_evidence!.epoch_start_ms!)
      expect(css).toHaveLength(2)
      // The target counts are independent of archive/HAR projections. Capture
      // must issue no probes. Chromium must actually cache the revisit; WebKit
      // retains its independent request count without inventing cache provenance.
      await writeFile(testInfo.outputPath('target-request-ledger.json'), JSON.stringify(attempts, null, 2))
      await testInfo.attach('target-request-ledger', { path: testInfo.outputPath('target-request-ledger.json'), contentType: 'application/json' })
      expect(attempts.filter(attempt => attempt.path === '/first')).toHaveLength(1)
      expect(attempts.filter(attempt => attempt.path === '/second')).toHaveLength(1)
      const cssNetworkRequests = attempts.filter(attempt => attempt.path === '/cached.css').length
      if (engine === 'chromium') expect(cssNetworkRequests).toBe(1)
      else expect([1, 2]).toContain(cssNetworkRequests) // WebKit does not expose cache provenance; retain the independent count.
      expect(attempts).toHaveLength(2 + cssNetworkRequests)
      if (engine === 'chromium') {
        expect(css[0].browser_evidence!.cache).toBe(false); expect(css[1].browser_evidence!.cache).toBe(true)
        expect(css[1].browser_evidence!.protocol).toBeFalsy(); expect(css[1].browser_evidence!.protocol_unavailable_reason).toBe('cached')
      } else {
        for (const row of css) { expect(row.browser_evidence!.cache).toBeUndefined(); expect(row.browser_evidence!.cache_unavailable_reason).toBe('not_exposed') }
      }
      const networkCSS = css[0].browser_evidence!, targetCSS = attempts.find(attempt => attempt.path === '/cached.css')!
      const harCSS = entries.filter(entry => entry.request.url === `${origin}/cached.css`).sort((a, b) => Date.parse(a.startedDateTime) - Date.parse(b.startedDateTime))[0]
      expect(targetCSS.finished! - targetCSS.received).toBeGreaterThanOrEqual(85)
      expect(networkCSS.epoch_start_ms).toBeGreaterThanOrEqual(targetCSS.received - 100)
      expect(networkCSS.epoch_start_ms).toBeLessThanOrEqual(targetCSS.received + 5)
      expect(Math.abs(networkCSS.epoch_start_ms! - Date.parse(harCSS.startedDateTime))).toBeLessThan(5)
      const responseEnd = networkCSS.phases!.find(phase => phase.name === 'response')!.end_ms!
      expect(responseEnd).toBeGreaterThanOrEqual(targetCSS.finished! - targetCSS.received - 10)
      // HAR's total includes all known phases; native SDK clocks retain finer
      // precision. The tolerance is only for this same-host controlled fixture.
      expect(Math.abs(responseEnd - harCSS.time)).toBeLessThan(20)
      if (engine === 'chromium') { expect(networkCSS.document_id).toBeTruthy(); expect(typeof networkCSS.document_offset_ms).toBe('number') }
      const expectedStart = networkCSS.document_offset_ms!, expectedEnd = expectedStart + responseEnd
      await page.goto(`/#/report/${runID}?section=diagnostics&${scopeQuery(workspace)}`); await page.reload()
      await page.getByRole('tab', { name: 'Diagnostics', exact: true }).click()
      await page.getByText('Browser evidence', { exact: true }).click()
      await page.getByRole('button', { name: 'Inspect retained archive resources', exact: true }).click()
      const inspector = page.getByRole('region', { name: 'Retained archive resource inspector', exact: true })
      if (!networkCSS.document_id || typeof networkCSS.document_offset_ms !== 'number') {
        expect(engine).toBe('webkit'); expect(networkCSS.identity_unavailable_reason).toBeTruthy()
        await expect(inspector.getByRole('region', { name: 'Document request waterfall', exact: true })).toHaveCount(0)
        const cssRow = inspector.getByRole('row').filter({ hasText: `${origin}/cached.css` }).first()
        await cssRow.locator('summary').first().click()
        await expect(cssRow.locator('dt').filter({ hasText: /^Document identity$/ }).locator('+ dd')).toContainText('Unavailable')
        await expect(cssRow.locator('dt').filter({ hasText: /^Cache$/ }).locator('+ dd')).toHaveText('Unavailable: not_exposed')
        await page.setViewportSize({ width: 760, height: 1000 })
        const nativeDetails = cssRow.locator('.browser-native-request-evidence')
        await nativeDetails.scrollIntoViewIfNeeded()
        const visible = await nativeDetails.evaluate(node => { const box = node.getBoundingClientRect(), owner = node.closest('.browser-resource-table-scroll')!.getBoundingClientRect(); return box.left >= owner.left && box.right <= owner.right + 1 })
        expect(visible).toBe(true)
        // Keep the same narrow width but fit the inspection in the capture so
        // the sticky toolbar cannot cover it during an element screenshot.
        await page.setViewportSize({ width: 760, height: 2400 })
        await nativeDetails.evaluate(node => { const owner = node.closest('.main-content') as HTMLElement; const header = document.querySelector('.main-topbar')!.getBoundingClientRect(); owner.scrollTop += node.getBoundingClientRect().top - header.bottom - 16 })
        await expect.poll(async () => nativeDetails.evaluate(node => { const box = node.getBoundingClientRect(), header = document.querySelector('.main-topbar')!.getBoundingClientRect(); return box.top >= header.bottom && box.bottom <= window.innerHeight })).toBe(true)
        await nativeDetails.screenshot({ path: testInfo.outputPath('native-waterfall-unavailable.png'), animations: 'disabled' })
        await writeFile(testInfo.outputPath('independent-waterfall-evidence.json'), JSON.stringify({ engine, profile, attempts, entries, retained, waterfall: 'unavailable' }, null, 2))
        await testInfo.attach('independent-waterfall-evidence', { path: testInfo.outputPath('independent-waterfall-evidence.json'), contentType: 'application/json' })
        expect(attempts).toHaveLength(2 + cssNetworkRequests)
        return
      }
      await inspector.getByText('Exact identity and cohort filters', { exact: true }).click()
      await inspector.getByLabel('Document identity', { exact: true }).fill(networkCSS.document_id!)
      await inspector.getByRole('button', { name: 'Apply resource filters', exact: true }).click()
      const waterfall = inspector.getByRole('region', { name: 'Document request waterfall', exact: true })
      await expect(waterfall).toBeVisible()
      await expect(waterfall.locator('.browser-document-waterfall-track').filter({ hasText: '' }).first()).toBeVisible()
      const bar = waterfall.locator('.browser-document-waterfall-track').filter({ has: page.locator('.browser-document-waterfall-phase') })
      await expect.poll(async () => (await bar.evaluateAll(nodes => nodes.map(node => node.getAttribute('aria-label')))).some(label => label?.startsWith(`${origin}/cached.css:`))).toBe(true)
      const labels = await bar.evaluateAll(nodes => nodes.map(node => node.getAttribute('aria-label')))
      expect(labels.find(label => label?.startsWith(`${origin}/cached.css:`))).toContain(`starts ${expectedStart.toFixed(1)} ms, ends ${expectedEnd.toFixed(1)} ms`)
      const firstCSSRow = inspector.getByRole('row').filter({ hasText: `${origin}/cached.css` }).first()
      await firstCSSRow.locator('summary').first().click()
      await expect(firstCSSRow.locator('dt').filter({ hasText: /^Cache$/ }).locator('+ dd')).toHaveText(engine === 'chromium' ? 'false (observed)' : 'Unavailable: not_exposed')
      await page.setViewportSize({ width: 760, height: 1000 }); await waterfall.scrollIntoViewIfNeeded()
      expect(await waterfall.evaluate(node => node.scrollWidth <= node.clientWidth + 1)).toBe(true)
      await page.screenshot({ path: testInfo.outputPath('native-waterfall.png'), animations: 'disabled' })
      await writeFile(testInfo.outputPath('independent-waterfall-evidence.json'), JSON.stringify({ engine, profile, attempts, entries, retained, waterfall: 'proved_native_join' }, null, 2))
      await testInfo.attach('independent-waterfall-evidence', { path: testInfo.outputPath('independent-waterfall-evidence.json'), contentType: 'application/json' })
      if (engine === 'chromium') {
        await inspector.getByLabel('Document identity', { exact: true }).fill(css[1].browser_evidence!.document_id!)
        await inspector.getByRole('button', { name: 'Apply resource filters', exact: true }).click()
        await expect(waterfall).toContainText(css[1].browser_evidence!.document_id!)
        const cachedRow = inspector.getByRole('row').filter({ hasText: `${origin}/cached.css` }).first()
        await cachedRow.locator('summary').first().click()
        await expect(cachedRow.locator('dt').filter({ hasText: /^Cache$/ }).locator('+ dd')).toHaveText('true (observed)')
        await expect(cachedRow.locator('dt').filter({ hasText: /^Negotiated protocol$/ }).locator('+ dd')).toHaveText('Unavailable: served from cache')
      }
      expect(attempts).toHaveLength(2 + cssNetworkRequests)
    } finally {
      if (runID) await request.post(`/api/v1/tests/${runID}/stop`).catch(() => {})
      for (const timer of timers) clearTimeout(timer)
      target.closeAllConnections(); await new Promise<void>((resolve, reject) => target.close(error => error ? reject(error) : resolve()))
    }
  })
}
