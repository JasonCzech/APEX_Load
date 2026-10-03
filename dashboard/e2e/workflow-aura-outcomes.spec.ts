import { test, expect, login, mutation, json } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

const sourceID = 'private-source-action-id'
const descriptor = 'apex://PrivateCaptureController/ACTION$submit'
const requestMessage = (ids: string[]) => new URLSearchParams({ message: JSON.stringify({ actions: ids.map(id => ({ id, descriptor, params: {} })) }) }).toString()
const entry = (path: string, body: string, responseText?: string, mimeType = 'application/json') => ({
  startedDateTime: '2026-10-03T00:00:00Z', time: 10,
  request: { method: body ? 'POST' : 'GET', url: `https://capture.my.salesforce.com${path}`, httpVersion: 'HTTP/1.1', headers: [], queryString: [], cookies: [], headersSize: -1, bodySize: body.length, ...(body ? { postData: { mimeType: 'application/x-www-form-urlencoded', text: body } } : {}) },
  response: { status: 200, statusText: 'OK', httpVersion: 'HTTP/1.1', headers: [], cookies: [], redirectURL: '', headersSize: -1, bodySize: responseText?.length || 0, content: { size: responseText?.length || 0, mimeType, ...(responseText === undefined ? {} : { text: responseText }) } },
  cache: {}, timings: { send: 1, wait: 8, receive: 1 },
})

// Uses the real import API and a local, synthetic capture. No captured host is contacted.
test('Aura HAR review exposes HTTP200 failures and missing responses with safe source navigation', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await expect(page.getByRole('button', { name: /^Workspace context/ })).toContainText(`${workspace.label} project`)
  await page.getByRole('button', { name: 'Import', exact: true }).click()
  await page.getByRole('searchbox', { name: 'Find a source format' }).fill('HAR Traffic')
  await page.getByRole('button', { name: /^HAR HAR Traffic/ }).click()
  const capture = { log: { version: '1.2', creator: { name: 'synthetic', version: '1' }, entries: [
    entry('/app.js', '', '/* static asset before Aura */', 'application/javascript'),
    entry('/aura', requestMessage([`${sourceID}-1`, `${sourceID}-2`]), JSON.stringify({ actions: [{ id: `${sourceID}-2`, state: 'ERROR', error: [{ message: 'private-server-error-payload' }] }, { id: `${sourceID}-1`, state: 'SUCCESS', returnValue: {} }] })),
    entry('/aura', requestMessage([`${sourceID}-3`])),
  ] } }
  const controls = page.getByRole('group', { name: 'Script import controls' })
  await controls.locator('input[type=file]').first().setInputFiles({ name: 'aura-outcomes.har', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(capture)) })
  const converted = mutation(page, 'POST', '/api/v1/scripts/import/har')
  await controls.getByRole('button', { name: 'Inspect & review', exact: true }).click()
  const response = await converted
  expect(response.ok(), await response.text()).toBeTruthy()
  const result = await json(response)
  const review = (result.summary as { salesforce_review: { aura_outcomes: { state_counts: { ERROR: number }; missing_responses: number } } }).salesforce_review.aura_outcomes
  expect(review.state_counts.ERROR).toBe(1)
  expect(review.missing_responses).toBe(1)

  const outcomes = page.getByRole('region', { name: 'Aura outcomes', exact: true })
  await expect(outcomes).toBeVisible()
  await expect(outcomes).toContainText('Capture inspection incomplete.')
  await expect(outcomes).toContainText('1 captured action did not succeed.')
  await expect(outcomes).toContainText('HTTP 200 does not establish Aura action success')
  await outcomes.getByText('Review captured batches (2)', { exact: true }).click()
  await expect(outcomes).toContainText('HAR entry 2')
  await expect(outcomes).toContainText('HAR entry 3')
  await expect(outcomes).toContainText('Action 1: SUCCESS')
  await expect(outcomes).toContainText('Action 2: ERROR')
  await expect(outcomes).toContainText('No response captured')
  await expect(outcomes).not.toContainText(sourceID)
  await expect(outcomes).not.toContainText(descriptor)
  await expect(outcomes).not.toContainText('private-server-error-payload')
  await expect(outcomes).not.toContainText('Captured success')
  await page.screenshot({ path: testInfo.outputPath('aura-outcomes-desktop.png'), fullPage: true })
  await page.setViewportSize({ width: 760, height: 1000 })
  expect(await outcomes.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  await page.screenshot({ path: testInfo.outputPath('aura-outcomes-narrow.png'), fullPage: true })
  await page.setViewportSize({ width: 1280, height: 720 })

  // The exact existing mapping is used; the filtered asset makes HAR entry 2
  // deliberately different from the imported action index.
  const sourceLink = outcomes.getByRole('button', { name: /^View request \d+ source mapping$/ }).first()
  await expect(sourceLink).toBeVisible()
  await sourceLink.click()
  await expect(page.getByRole('tab', { name: 'Source Mapping', exact: true })).toHaveAttribute('aria-selected', 'true')
  await expect(page.getByRole('navigation', { name: 'Review mapping navigation' })).toBeVisible()
  await expect(page.locator('tr[data-mapping-id].active')).toContainText('actions[0]')
  await page.getByRole('tab', { name: 'Review', exact: true }).click()
  await expect(outcomes).toBeVisible()
})
