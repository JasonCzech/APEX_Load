import { createServer } from 'node:http'
import type { Page } from '@playwright/test'
import { test, expect, expectDebugOutcome, expectEditorSaved, idOf, json, login } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

// A loopback target: the cart page names its cart in the body, and the orders
// page records what each request sent for it.
async function cartTarget() {
  const seen: Array<{ cart: string | null; header: string | undefined }> = []
  const server = createServer((request, response) => {
    const url = new URL(request.url!, 'http://fixture')
    response.setHeader('Content-Type', 'application/json')
    if (url.pathname === '/cart/c-5571') { response.end('{"ok":true,"path":"/cart/c-5571"}'); return }
    if (url.pathname === '/orders') {
      seen.push({ cart: url.searchParams.get('cart'), header: request.headers['x-cart'] as string | undefined })
      response.end('{"orders":[]}')
      return
    }
    response.statusCode = 404; response.end('{"error":"unknown fixture route"}')
  })
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  const address = server.address()
  if (!address || typeof address === 'string') throw new Error('Missing loopback address')
  return { url: `http://127.0.0.1:${address.port}`, seen, close: () => new Promise<void>(resolve => server.close(() => resolve())) }
}

/** Selects `text` in the replayed response, as a reader would with the mouse. */
async function selectInResponse(page: Page, text: string) {
  const found = await page.evaluate(wanted => {
    const area = document.querySelector('.response-selection-content')
    if (!area) return false
    const walker = document.createTreeWalker(area, NodeFilter.SHOW_TEXT)
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      const at = node.textContent!.indexOf(wanted)
      if (at < 0 || node.parentElement!.closest('button')) continue
      const range = document.createRange()
      range.setStart(node, at)
      range.setEnd(node, at + wanted.length)
      getSelection()!.removeAllRanges()
      getSelection()!.addRange(range)
      node.parentElement!.dispatchEvent(new MouseEvent('mouseup', { bubbles: true }))
      return true
    }
    return false
  }, text)
  expect(found, `“${text}” is in the replayed response`).toBe(true)
  await expect(page.getByRole('group', { name: 'Use the selected text' })).toContainText(`Selected “${text}”`)
}

test('a status check, a text check and a saved value are made from a replayed response and checked without a new replay', async ({ page }) => {
  test.setTimeout(120_000)
  const target = await cartTarget()
  try {
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
    const request = page.context().request
    const workspace = await createWorkspace(request)
    const id = idOf(await json(await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: {
      name: 'Response checks', version: '1.0', protocol: 'http', config: { base_url: target.url },
      actions: [
        { name: 'Open cart', type: 'GET', target: '/cart/c-5571' },
        { name: 'List orders', type: 'GET', target: '/orders?cart=c-5571', header_list: [{ name: 'X-Cart', value: 'c-5571' }] },
      ],
    } } })))
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
    await page.reload()

    await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
    await page.locator('.topbar-debug-action-run').click()
    await expectDebugOutcome(page, 'passed')
    await page.getByRole('complementary', { name: 'Actions', exact: true }).getByRole('treeitem', { name: 'Open cart' }).click()

    // From the response: its status, text in it, and that text used by the later step.
    await page.getByRole('button', { name: 'Check that the status is 200', exact: true }).click()
    await expect(page.locator('.response-selection-done')).toHaveText('Added the check “Status is 200”.')
    await page.getByRole('button', { name: 'Check that the status is 200', exact: true }).click()
    await expect(page.locator('.response-selection-done')).toHaveText('The step already checks that the status is 200.')
    // A selection belongs to its event: another one starts afresh.
    const rail = page.getByRole('complementary', { name: 'Actions', exact: true })
    await selectInResponse(page, 'c-5571')
    await rail.getByRole('treeitem', { name: 'List orders' }).click()
    await expect(page.getByRole('group', { name: 'Use the selected text' })).toHaveCount(0)
    await expect(page.locator('.response-selection-done')).toHaveCount(0)
    await rail.getByRole('treeitem', { name: 'Open cart' }).click()
    await selectInResponse(page, 'c-5571')
    await page.getByRole('button', { name: 'Check that the response contains this', exact: true }).click()
    await expect(page.locator('.response-selection-done')).toHaveText('Added the check “Response contains c-5571”.')
    await selectInResponse(page, 'c-5571')
    await page.getByRole('button', { name: 'Save this as a value', exact: true }).click()
    await expect(page.locator('.response-selection-done')).toHaveText('Saved the value; it is in the step’s Saved values.')
    // Using it in later steps keeps the value already saved, under its name.
    await selectInResponse(page, 'c-5571')
    await page.getByRole('button', { name: 'Use this value in later steps (2)', exact: true }).click()
    const review = page.getByRole('dialog', { name: 'Use this value in later steps' })
    await expect(review.getByLabel('Save as', { exact: true })).toHaveValue('cart')
    await expect(review.getByRole('alert')).toHaveCount(0)
    await expect(review).toContainText('List orders · URL: /orders?cart=c-5571')
    await expect(review).toContainText('List orders · Header X-Cart: c-5571')
    await review.getByRole('button', { name: 'Replace 2 uses', exact: true }).click()
    await expect(review).toHaveCount(0)

    // Each shows its result against the response the replay captured.
    await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Author', exact: true }).click()
    await page.getByRole('treeitem', { name: /Open cart/ }).click()
    await page.getByRole('tab', { name: /^Correlation & checks/ }).click()
    await expect(page.getByRole('group', { name: 'Saved value cart' })).toContainText('Saves “c-5571”')
    await expect(page.getByRole('group', { name: 'Check 1: Status is …' })).toContainText('Passed')
    const contains = page.getByRole('group', { name: 'Check 2: Response contains …' })
    await expect(contains).toContainText('Passed')
    await page.getByLabel('Check 2 text', { exact: true }).fill('c-9999')
    await expect(contains).toContainText('Failed: “c-9999” is not in the response')
    await page.getByLabel('Check 2 text', { exact: true }).fill('c-5571')
    await expect(contains).toContainText('Passed')

    await page.getByRole('button', { name: 'Save script', exact: true }).click()
    await expectEditorSaved(page)
    const stored = (await json(await request.get(`/api/v1/scripts/${id}`))) as { script: { actions: Array<Record<string, unknown>> } }
    expect(stored.script.actions[0]).toMatchObject({
      assertions: [{ type: 'status_code', expected: 200 }, { type: 'text_contains', value: 'c-5571' }],
      extractors: [{ name: 'cart', source: 'body', type: 'boundary', expression: 't/|||"' }],
    })
    expect(stored.script.actions[1]).toMatchObject({ target: '/orders?cart={{var.cart}}', header_list: [{ name: 'X-Cart', value: '{{var.cart}}' }] })
    expect((stored.script.actions[0].extractors as unknown[]).length).toBe(1)

    // The next replay sends the saved value where the recorded one was.
    await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
    await page.locator('.topbar-debug-action-run').click()
    await expectDebugOutcome(page, 'passed')
    expect(target.seen.at(-1)).toEqual({ cart: 'c-5571', header: 'c-5571' })
  } finally {
    await target.close()
  }
})
