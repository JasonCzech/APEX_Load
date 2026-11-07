import axe from 'axe-core'
import { test, expect, login, mutation, json } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'
import { canonicalImportDraft } from './support/importResponse'

function buildSyntheticSalesforceHar(options: {
  sessionSid: string
  recordId: string
  swapParallelOrder: boolean
  includeUnexportedBeacon: boolean
}) {
  const host = 'https://perf-synth.my.salesforce.com'
  const auraContext = JSON.stringify({
    mode: 'PROD',
    fwuid: 'synthFwuid2026Release1',
    app: 'one:one',
    loaded: { 'APPLICATION@markup://one:one': '1001_synth' },
  })
  const makeAuraPost = (r: number, actions: unknown[], responseActions: unknown[]) => {
    const body = new URLSearchParams({
      message: JSON.stringify({ actions }),
      'aura.context': auraContext,
      'aura.pageURI': '/lightning/page/home',
      'aura.token': `synth-jwt-${options.sessionSid}`,
    }).toString()
    const resp = JSON.stringify({ actions: responseActions })
    return {
      startedDateTime: `2026-11-01T12:00:0${r}.000Z`,
      time: 35,
      request: {
        method: 'POST',
        url: `${host}/aura?r=${r}&aura.ApexAction.execute=1`,
        httpVersion: 'HTTP/1.1',
        headers: [{ name: 'Content-Type', value: 'application/x-www-form-urlencoded; charset=UTF-8' }],
        queryString: [{ name: 'r', value: String(r) }],
        cookies: [],
        headersSize: -1,
        bodySize: body.length,
        postData: { mimeType: 'application/x-www-form-urlencoded; charset=UTF-8', text: body },
      },
      response: {
        status: 200,
        statusText: 'OK',
        httpVersion: 'HTTP/1.1',
        headers: [{ name: 'Content-Type', value: 'application/json' }],
        cookies: [],
        redirectURL: '',
        headersSize: -1,
        bodySize: resp.length,
        content: { size: resp.length, mimeType: 'application/json', text: resp },
      },
      cache: {},
      timings: { send: 1, wait: 30, receive: 4 },
    }
  }

  const loginPostBody = new URLSearchParams({
    un: 'perf.user@example.test',
    pw: 'SyntheticOnlyPassword!1',
    startURL: '/lightning/page/home',
  }).toString()
  const frontdoorUrl = `${host}/secur/frontdoor.jsp?sid=${options.sessionSid}&retURL=%2Fone%2Fone.app`
  const oneAppHtml = `<!doctype html><html><head><script>var auraConfig = {"eikoocnekot":"__Host-ERIC_PROD-999","context":${auraContext}};</script></head><body>Lightning</body></html>`

  const searchPost = makeAuraPost(
    options.swapParallelOrder ? 2 : 1,
    [{
      id: '101;a',
      descriptor: 'aura://ApexActionController/ACTION$execute',
      callingDescriptor: 'UNKNOWN',
      params: {
        namespace: '',
        classname: 'InteractionSearchController',
        method: 'findCustomer',
        params: { query: 'Acme' },
        cacheable: false,
        isContinuation: false,
      },
    }],
    [{
      id: '101;a',
      state: 'SUCCESS',
      returnValue: { returnValue: { customerId: options.recordId } },
    }],
  )

  const detailPost = makeAuraPost(
    options.swapParallelOrder ? 1 : 2,
    [{
      id: '102;a',
      descriptor: 'aura://ApexActionController/ACTION$execute',
      callingDescriptor: 'UNKNOWN',
      params: {
        namespace: '',
        classname: 'InteractionDetailController',
        method: 'loadInteraction',
        params: { customerId: options.recordId },
        cacheable: false,
        isContinuation: false,
      },
    }],
    [{
      id: '102;a',
      state: 'SUCCESS',
      returnValue: { returnValue: { status: 'OPEN' } },
    }],
  )

  const parallelBatch = options.swapParallelOrder
    ? [detailPost, searchPost]
    : [searchPost, detailPost]

  const entries: unknown[] = [
    {
      startedDateTime: '2026-11-01T12:00:00.000Z',
      time: 40,
      request: {
        method: 'POST',
        url: `${host}/`,
        httpVersion: 'HTTP/1.1',
        headers: [{ name: 'Content-Type', value: 'application/x-www-form-urlencoded' }],
        queryString: [],
        cookies: [],
        headersSize: -1,
        bodySize: loginPostBody.length,
        postData: { mimeType: 'application/x-www-form-urlencoded', text: loginPostBody },
      },
      response: {
        status: 302,
        statusText: 'Found',
        httpVersion: 'HTTP/1.1',
        headers: [{ name: 'Location', value: frontdoorUrl }],
        cookies: [],
        redirectURL: frontdoorUrl,
        headersSize: -1,
        bodySize: 0,
        content: { size: 0, mimeType: 'text/html', text: '' },
      },
      cache: {},
      timings: { send: 1, wait: 35, receive: 4 },
    },
    {
      startedDateTime: '2026-11-01T12:00:00.100Z',
      time: 25,
      request: {
        method: 'GET',
        url: frontdoorUrl,
        httpVersion: 'HTTP/1.1',
        headers: [],
        queryString: [{ name: 'sid', value: options.sessionSid }],
        cookies: [],
        headersSize: -1,
        bodySize: 0,
      },
      response: {
        status: 302,
        statusText: 'Found',
        httpVersion: 'HTTP/1.1',
        headers: [{ name: 'Location', value: `${host}/one/one.app` }],
        cookies: [],
        redirectURL: `${host}/one/one.app`,
        headersSize: -1,
        bodySize: 0,
        content: { size: 0, mimeType: 'text/html', text: '' },
      },
      cache: {},
      timings: { send: 1, wait: 20, receive: 4 },
    },
    {
      startedDateTime: '2026-11-01T12:00:00.200Z',
      time: 50,
      request: {
        method: 'GET',
        url: `${host}/one/one.app`,
        httpVersion: 'HTTP/1.1',
        headers: [],
        queryString: [],
        cookies: [],
        headersSize: -1,
        bodySize: 0,
      },
      response: {
        status: 200,
        statusText: 'OK',
        httpVersion: 'HTTP/1.1',
        headers: [{ name: 'Content-Type', value: 'text/html; charset=UTF-8' }],
        cookies: [],
        redirectURL: '',
        headersSize: -1,
        bodySize: oneAppHtml.length,
        content: { size: oneAppHtml.length, mimeType: 'text/html', text: oneAppHtml },
      },
      cache: {},
      timings: { send: 1, wait: 44, receive: 5 },
    },
    ...parallelBatch,
  ]

  if (options.includeUnexportedBeacon) {
    entries.push({
      startedDateTime: '2026-11-01T12:00:05.000Z',
      time: 12,
      request: {
        method: 'POST',
        url: `${host}/aura?r=94`,
        httpVersion: 'HTTP/1.1',
        headers: [
          { name: 'Content-Type', value: 'application/x-www-form-urlencoded; charset=UTF-8' },
          { name: 'Content-Length', value: '418' },
        ],
        queryString: [{ name: 'r', value: '94' }],
        cookies: [],
        headersSize: -1,
        bodySize: 418,
      },
      response: {
        status: 200,
        statusText: 'OK',
        httpVersion: 'HTTP/1.1',
        headers: [{ name: 'Content-Type', value: 'application/json' }],
        cookies: [],
        redirectURL: '',
        headersSize: -1,
        bodySize: 2,
        content: { size: 2, mimeType: 'application/json', text: '{}' },
      },
      cache: {},
      timings: { send: 1, wait: 10, receive: 1 },
    })
  }

  entries.push({
    startedDateTime: '2026-11-01T12:00:08.000Z',
    time: 20,
    request: {
      method: 'GET',
      url: `${host}/secur/logout.jsp`,
      httpVersion: 'HTTP/1.1',
      headers: [],
      queryString: [],
      cookies: [],
      headersSize: -1,
      bodySize: 0,
    },
    response: {
      status: 302,
      statusText: 'Found',
      httpVersion: 'HTTP/1.1',
      headers: [{ name: 'Location', value: `${host}/` }],
      cookies: [],
      redirectURL: `${host}/`,
      headersSize: -1,
      bodySize: 0,
      content: { size: 0, mimeType: 'text/html', text: '' },
    },
    cache: {},
    timings: { send: 1, wait: 18, receive: 1 },
  })

  return { log: { version: '1.2', creator: { name: 'synthetic-devtools', version: '130' }, entries } }
}

test('HAR capture guide, 2-HAR Salesforce Correlation Studio, draft exclusion, and Aura envelope editor work end-to-end across themes', async ({ page }) => {
  test.setTimeout(90_000)
  await page.addInitScript({ content: axe.source })
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()

  await page.getByRole('tab', { name: 'Import', exact: true }).click()
  await page.getByRole('searchbox', { name: 'Find a source format' }).fill('HAR Traffic')
  await page.getByRole('button', { name: /^HAR HAR Traffic/ }).click()

  const controls = page.getByRole('group', { name: 'Script import controls' })

  // 1. Open and audit the embedded HAR capture guide popup
  await controls.getByRole('button', { name: 'HAR capture guide', exact: true }).click()
  const guideDialog = page.getByRole('dialog', { name: /Optimal browser HAR capture settings/i })
  await expect(guideDialog).toBeVisible()
  await expect(guideDialog).toContainText('Preserve log')
  await expect(guideDialog).toContainText('Allow to generate HAR (with sensitive data)')
  await expect(guideDialog).toContainText('Copy all listed as HAR')
  const dialogViolations = await page.evaluate(async () => (
    await (window as typeof window & { axe: typeof axe }).axe.run('[role="dialog"]', {
      runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa'] },
    })
  ).violations)
  expect(dialogViolations).toEqual([])
  await guideDialog.getByRole('button', { name: /Close/i }).first().click()
  await expect(guideDialog).toBeHidden()

  // 2. Drop both matched Salesforce HAR files at once into the primary file input
  const har1 = buildSyntheticSalesforceHar({
    sessionSid: '00DSynthSessionA1111',
    recordId: '001D000000SynthAA1',
    swapParallelOrder: false,
    includeUnexportedBeacon: true,
  })
  const har2 = buildSyntheticSalesforceHar({
    sessionSid: '00DSynthSessionB2222',
    recordId: '001D000000SynthBB2',
    swapParallelOrder: true,
    includeUnexportedBeacon: false,
  })

  await controls.locator('input[type=file]').first().setInputFiles([
    { name: 'salesforce-session-1.har', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(har1)) },
    { name: 'salesforce-session-2.har', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(har2)) },
  ])
  await expect(controls).toContainText('salesforce-session-1.har')
  await expect(controls).toContainText('salesforce-session-2.har')

  // 3. Run 2-HAR analysis & verify Correlation Studio + unexported-body blocker exclusion
  const imported = mutation(page, 'POST', '/api/v1/scripts/import/har')
  await controls.getByRole('button', { name: 'Inspect & review', exact: true }).click()
  const importResp = await imported
  expect(importResp.ok(), await importResp.text()).toBeTruthy()
  const importPayload = await json(importResp)
  expect((importPayload.summary as { har_diff?: { compared?: boolean } })?.har_diff?.compared).toBe(true)

  // Desktop sign-in is hoisted into init[] and logout into teardown[]; the
  // funnel reports those counts, so check the numbers, not the labels.
  const importedScript = canonicalImportDraft(importPayload)
  expect(importedScript.init?.length ?? 0).toBeGreaterThan(0)
  expect(importedScript.teardown?.length ?? 0).toBeGreaterThan(0)
  const studio = controls.getByRole('region', { name: 'Correlation studio & capture funnel', exact: true })
  await expect(studio).toBeVisible()
  const funnel = studio.getByRole('group', { name: 'HAR capture conversion funnel', exact: true })
  await expect(funnel).toContainText(/Init [1-9]\d* · Main \d+ · Teardown [1-9]\d*/)

  const blockerBanner = studio.getByRole('region', { name: 'Requests missing exported bodies', exact: true })
  await expect(blockerBanner).toBeVisible()
  const excluded = mutation(page, 'POST', '/api/v1/scripts/import/exclude-actions')
  await blockerBanner.getByRole('button', { name: /^Exclude action from draft: / }).click()
  const excludeResp = await excluded
  expect(excludeResp.ok(), await excludeResp.text()).toBeTruthy()
  await expect(blockerBanner).toBeHidden()

  // 4. Verify WCAG AA axe-core compliance across all 5 themes & 200% zoom reflow (640px CSS width)
  for (const theme of ['dark', 'light', 'solarized-dark', 'solarized-light', 'monokai-dimmed']) {
    await page.evaluate(nextTheme => {
      document.documentElement.dataset.theme = nextTheme
    }, theme)
    await page.waitForFunction(() => document.getAnimations().every(
      animation => animation.playState !== 'running' || animation.effect?.getComputedTiming().iterations === Infinity,
    ))
    const violations = await page.evaluate(async () => (
      await (window as typeof window & { axe: typeof axe }).axe.run('.har-correlation-studio', {
        runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa'] },
      })
    ).violations)
    expect(violations, `axe violations in theme ${theme}`).toEqual([])
  }
  await page.evaluate(() => { document.documentElement.dataset.theme = 'dark' })

  await page.setViewportSize({ width: 640, height: 800 })
  expect(await studio.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true)
  await page.setViewportSize({ width: 1280, height: 900 })

  // 5. Save to Script Library, open in Editor, verify decoded Aura envelope & ready Debug gate
  const saved = mutation(page, 'POST', '/api/v1/scripts/import/commit-batch')
  // The review's own Save step; the sticky toolbar repeats it.
  await controls.locator('.script-import-review-actions').getByRole('button', { name: /^(Save to script library|Save draft)$/ }).click()
  const savedResp = await saved
  expect(savedResp.ok(), await savedResp.text()).toBeTruthy()

  await page.getByRole('button', { name: /^Open .* in Visual Editor$/ }).click()
  await expect(page.getByRole('region', { name: 'Author center workbench', exact: true })).toBeVisible()

  // Verify empty transaction markers around the excluded beacon were pruned,
  // and verify the decoded Aura envelope editor renders sub-actions cleanly
  await expect(page.getByRole('treeitem', { name: /04_aura/ })).toHaveCount(0)
  await page.getByRole('treeitem', { name: /04 - POST aura/ }).click()
  await page.getByRole('button', { name: 'Edit request body in center', exact: true }).click()
  const auraEditor = page.getByRole('region', { name: 'Aura envelope', exact: true })
  await expect(auraEditor).toBeVisible()
  await expect(auraEditor).toContainText('InteractionSearchController')
  await expect(auraEditor).toContainText('findCustomer')

  // Verify zero authoring problems and enabled Debug gate
  await expect(page.getByRole('button', { name: 'Author Problems (0)', exact: true })).toBeVisible()
  await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
  await expect(page.locator('.topbar-debug-action-run')).toBeEnabled()

  // 6. Verify User Guide documentation section in Help Center
  await page.goto('/#/docs?guide=user-guide&article=imports&section=capturing-optimal-har-files-for-comparison-and-dsl-conversion')
  const article = page.locator('.guide-markdown-article')
  await expect(article).toContainText('Capturing Optimal HAR Files For Comparison And DSL Conversion')
  await expect(article).toContainText('Comparing Two Salesforce HAR Captures')
})
