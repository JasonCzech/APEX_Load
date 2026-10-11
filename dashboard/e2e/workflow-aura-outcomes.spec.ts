import { createServer } from 'node:http'
import type { Locator, Page } from '@playwright/test'
import { test, expect, login, mutation, json, idOf, expectDebugOutcome } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

// Capture the actual viewport, not the full height of a clipped nested pane.
async function captureReviewViewport(page: Page, region: Locator, path: string) {
  const heading = region.getByRole('heading').first()
  const anchor = await heading.count() ? heading : region
  await anchor.evaluate(element => {
    element.scrollIntoView({ block: 'center', inline: 'nearest' })
    for (let parent = element.parentElement; parent; parent = parent.parentElement) {
      parent.scrollLeft = 0
    }
    window.scrollTo({ left: 0, top: window.scrollY })
  })
  const box = await anchor.boundingBox()
  expect(box).not.toBeNull()
  expect(box!.x).toBeGreaterThanOrEqual(0)
  expect(box!.x + box!.width).toBeLessThanOrEqual(page.viewportSize()!.width + 1)
  await page.screenshot({ path })
}

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
  // The collapsed sidebar shows only an icon; the workspace path is the button's accessible name.
  await expect(page.getByRole('button', { name: new RegExp(`Workspace context\\..*${workspace.label} project`) })).toBeVisible()
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
  await page.getByRole('tab', { name: /^Review(,|$)/ }).click()
  await expect(outcomes).toBeVisible()
})

test('Aura parameter review applies one nested field without changing repeated sibling values', async ({ page }, testInfo) => {
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('button', { name: 'Import', exact: true }).click()
  await page.getByRole('searchbox', { name: 'Find a source format' }).fill('HAR Traffic')
  await page.getByRole('button', { name: /^HAR HAR Traffic/ }).click()
  const literal = '001D000000IqhSLIAZ'
  const nested = JSON.stringify({ records: [{ recordId: literal }, { recordId: literal }], note: literal })
  const body = new URLSearchParams({ message: JSON.stringify({ actions: [{ id: 'synthetic-action', descriptor: 'synthetic://Account/ACTION$save', params: { input: nested, recordId: literal } }] }) }).toString()
  const capture = { log: { version: '1.2', creator: { name: 'synthetic', version: '1' }, entries: [entry('/aura', body, JSON.stringify({ actions: [{ id: 'synthetic-action', state: 'SUCCESS', returnValue: {} }] }))] } }
  const controls = page.getByRole('group', { name: 'Script import controls' })
  await controls.locator('input[type=file]').first().setInputFiles({ name: 'aura-nested-parameters.har', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(capture)) })
  const imported = mutation(page, 'POST', '/api/v1/scripts/import/har')
  await controls.getByRole('button', { name: 'Inspect & review', exact: true }).click()
  const response = await imported
  expect(response.ok(), await response.text()).toBeTruthy()
  type Advice = { id: string; kind: string; var_name: string; replacement: string; locations?: { decoded_field_path?: string; encoding_layers?: string[] }[] }
  const result = await json(response)
  const suggestions = (result.parameterization as { suggestions: Advice[] }).suggestions.filter(item => item.kind === 'data_param')
  const selected = suggestions.find(item => item.locations?.some(location => location.decoded_field_path?.includes('records[0]')))
  expect(selected).toBeDefined()
  expect(suggestions.filter(item => item.locations?.some(location => location.decoded_field_path?.includes('records[1]')))).toHaveLength(1)
  await controls.getByRole('tab', { name: /^Data & parameters(,|$)/ }).click()
  const panel = controls.locator('.paramize-review')
  const row = panel.locator('.paramize-item').filter({ has: page.getByText(selected!.var_name, { exact: true }) })
  await expect(row).toHaveCount(1)
  await expect(row).toContainText('records[0]')
  await expect(row).toContainText('JSON')
  await row.getByRole('checkbox').check()
  await panel.getByRole('button', { name: 'Preview parameter changes', exact: true }).click()
  const preview = panel.getByRole('region', { name: 'Parameter suggestion preview', exact: true })
  await expect(preview).toContainText('records[0]')
  await expect(preview).not.toContainText('records[1]')
  await preview.screenshot({ path: testInfo.outputPath('aura-nested-parameter-preview.png') })
  const applied = mutation(page, 'POST', '/api/v1/scripts/import/parameterize/apply')
  await panel.getByRole('button', { name: 'Apply reviewed parameter changes', exact: true }).click()
  const appliedResponse = await applied
  expect(appliedResponse.ok(), await appliedResponse.text()).toBeTruthy()
  const outcome = await json(appliedResponse)
  expect(outcome.applied_count).toBe(1)
  const script = outcome.script as { actions: { body: string }[] }
  type Envelope = { actions: { params: { input: string; recordId: string } }[] }
  const message = JSON.parse(new URLSearchParams(script.actions[0].body).get('message') || '') as Envelope
  const payload = JSON.parse(message.actions[0].params.input) as { records: { recordId: string }[]; note: string }
  expect(payload.records[0].recordId).toBe(selected!.replacement)
  expect(payload.records[1].recordId).toBe(literal)
  expect(payload.note).toBe(literal)
  expect(message.actions[0].params.recordId).toBe(literal)
  await expect(panel).toContainText('Applied Changes')
  await page.setViewportSize({ width: 760, height: 1000 })
  expect(await panel.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  await panel.locator('.paramize-diffs').screenshot({ path: testInfo.outputPath('aura-nested-parameter-narrow.png') })
})

// Native loops change request positions. Retained source references describe the
// captured import, and must never become current editor destinations after save.
test('native correlation review preserves exact original source navigation through save and reopen', async ({ page }, testInfo) => {
  test.setTimeout(90_000)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
  const workspace = await createWorkspace(page.context().request)
  await page.goto(`/#/scripts?mode=create&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('button', { name: 'Import', exact: true }).click()
  await page.getByRole('searchbox', { name: 'Find a source format' }).fill('HAR Traffic')
  await page.getByRole('button', { name: /^HAR HAR Traffic/ }).click()
  const clientId = 'synthetic-bayeux-client'
  const jobId = '7508c00000QRYA6AAL'
  const comet = (id: string, channel: string, extra: Record<string, unknown>, response: Record<string, unknown>) => entry('/cometd/60.0', new URLSearchParams({ message: JSON.stringify([{ id, channel, ...extra }]) }).toString(), JSON.stringify([{ id, channel, successful: true, ...response }]))
  const bulk = entry('/services/data/v60.0/jobs/query', JSON.stringify({ operation: 'query', query: 'SELECT Id FROM Account' }), JSON.stringify({ id: jobId, operation: 'query', state: 'UploadComplete' }))
  bulk.request.postData!.mimeType = 'application/json'
  const capture = { log: { version: '1.2', creator: { name: 'synthetic', version: '1' }, entries: [
    entry('/static.js', '', '/* filtered static asset */', 'application/javascript'),
    comet('1', '/meta/handshake', { supportedConnectionTypes: ['long-polling'], version: '1.0' }, { clientId }),
    comet('2', '/meta/subscribe', { clientId, subscription: '/event/Order__e' }, {}),
    comet('3', '/meta/connect', { clientId, connectionType: 'long-polling' }, {}),
    comet('4', '/meta/connect', { clientId, connectionType: 'long-polling' }, {}),
    bulk,
    entry(`/services/data/v60.0/jobs/query/${jobId}`, '', JSON.stringify({ id: jobId, state: 'JobComplete' })),
  ] } }
  const controls = page.getByRole('group', { name: 'Script import controls' })
  await controls.locator('input[type=file]').first().setInputFiles({ name: 'native-correlation-source.har', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(capture)) })
  const imported = mutation(page, 'POST', '/api/v1/scripts/import/har')
  await controls.getByRole('button', { name: 'Inspect & review', exact: true }).click()
  const response = await imported
  expect(response.ok(), await response.text()).toBeTruthy()
  type Reference = { har_entry_id: number; original_action_path?: string; source_mapping_id?: string; source_file?: string; native_action_path?: string }
  type Correlation = { rule_id?: string; producer_reference?: Reference; consumer_references?: Reference[] }
  type Review = { native_preview: { native_path_domain: string; correlations: Correlation[]; correlation_coverage: { discovered_correlations: number; native_unmapped_consumer_references: number; source_linked_consumer_references: number } } }
  type Script = { actions: { type: string }[]; metadata: { import: { preserved: { source_review: { target_domain: string; mappings: { id: string; source_file: string; target_path: string }[] }; salesforce_review: Review; source_mapping_review?: unknown } } } }
  const result = await json(response)
  const draft = result.script as Script
  expect(draft.actions.filter(action => action.type === 'control_loop')).toHaveLength(2)
  expect(draft.metadata.import.preserved.source_review.target_domain).toBe('original_import')
  const preview = (result.summary as { salesforce_review: Review }).salesforce_review.native_preview
  expect(preview.native_path_domain).toBe('final_native')
  expect(preview.correlation_coverage.discovered_correlations).toBeGreaterThan(0)
  expect(preview.correlation_coverage.source_linked_consumer_references).toBeGreaterThan(0)
  expect(preview.correlation_coverage.native_unmapped_consumer_references).toBeGreaterThan(0)
  const correlation = preview.correlations.find(item => item.rule_id === 'salesforce.cometd_client_id')
  expect(correlation).toBeDefined()
  const correlationLabel = `Captured correlation ${preview.correlations.indexOf(correlation!) + 1}`
  const producer = correlation!.producer_reference!
  const consumer = correlation!.consumer_references![0]
  for (const reference of [producer, consumer]) {
    expect(reference.source_mapping_id).toBeTruthy()
    expect(reference.source_file).toBeTruthy()
    expect(draft.metadata.import.preserved.source_review.mappings.filter(mapping => mapping.id === reference.source_mapping_id && mapping.source_file === reference.source_file && mapping.target_path === reference.original_action_path)).toHaveLength(1)
  }
  // This is evidence about the draft at import, not live session validation.
  const evidence = controls.getByRole('region', { name: 'Captured correlation evidence', exact: true })
  await expect(evidence).toBeVisible()
  await evidence.getByText(/^Review captured correlations \(\d+\)$/).click()
  const capturedRelationship = evidence.getByRole('article', { name: correlationLabel, exact: true })
  await expect(capturedRelationship).toContainText('Native path at import')
  await expect(evidence).not.toContainText('Session validated')
  await captureReviewViewport(page, evidence, testInfo.outputPath('correlation-review-desktop.png'))
  await page.setViewportSize({ width: 760, height: 1000 })
  expect(await evidence.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  await captureReviewViewport(page, evidence, testInfo.outputPath('correlation-review-narrow.png'))
  await page.setViewportSize({ width: 1280, height: 900 })
  await capturedRelationship.getByRole('button', { name: 'View producer source mapping', exact: true }).click()
  await expect(controls.getByRole('tab', { name: 'Source Mapping', exact: true })).toHaveAttribute('aria-selected', 'true')
  const original = controls.getByRole('region', { name: 'Original import source review', exact: true })
  await expect(original).toBeVisible()
  await expect(original.locator('tr[data-mapping-id].active')).toHaveAttribute('data-mapping-id', producer.source_mapping_id!)
  await expect(original.getByRole('combobox', { name: /^Captured source file/ })).toHaveValue(producer.source_file!)
  await expect(original.getByRole('region', { name: `${producer.source_file} source`, exact: true })).toBeVisible()
  await expect(original).toContainText('Original import path')
  await expect(original).not.toContainText('Destination identity')
  await expect(original).not.toContainText('Destination version')
  await expect(original.getByRole('button', { name: 'Record review decision', exact: true })).toHaveCount(0)
  await expect(original.getByRole('button', { name: 'Edit destination in Author', exact: true })).toHaveCount(0)
  await controls.getByRole('tab', { name: /^Review(,|$)/ }).click()
  if (await evidence.locator('details').first().getAttribute('open') === null) await evidence.getByText(/^Review captured correlations \(\d+\)$/).click()
  await capturedRelationship.getByRole('button', { name: 'View consumer 1 source mapping', exact: true }).click()
  await expect(original.locator('tr[data-mapping-id].active')).toHaveAttribute('data-mapping-id', consumer.source_mapping_id!)
  await expect(original.getByRole('combobox', { name: /^Captured source file/ })).toHaveValue(consumer.source_file!)
  await expect(original.getByRole('region', { name: `${consumer.source_file} source`, exact: true })).toBeVisible()
  await captureReviewViewport(page, original, testInfo.outputPath('correlation-original-source-desktop.png'))
  await page.setViewportSize({ width: 760, height: 1000 })
  expect(await original.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  await captureReviewViewport(page, original, testInfo.outputPath('correlation-original-source-narrow.png'))
  await page.setViewportSize({ width: 1280, height: 900 })
  await controls.getByRole('tab', { name: /^Review(,|$)/ }).click()
  const saved = mutation(page, 'POST', '/api/v1/scripts/import/commit-batch')
  await controls.getByRole('button', { name: 'Save draft', exact: true }).click()
  const savedResponse = await saved
  expect(savedResponse.ok(), await savedResponse.text()).toBeTruthy()
  const savedResult = await json(savedResponse)
  const stored = (savedResult.scripts as { id: string; script: Script }[])[0]
  expect(stored.script.metadata.import.preserved.source_review.target_domain).toBe('original_import')
  expect(stored.script.metadata.import.preserved.source_mapping_review).toBeUndefined()
  // A historical native draft may lack the new domain and carry annotations
  // produced by older clients. The narrow native provenance remains source-only.
  // Reopen must not treat them as current approval, and an editor save clears them.
  const legacy = { ...stored.script, metadata: { ...stored.script.metadata, import: { ...stored.script.metadata.import, preserved: { ...stored.script.metadata.import.preserved, source_review: { ...stored.script.metadata.import.preserved.source_review, target_domain: undefined }, source_mapping_review: { version: 1, importRevision: 'legacy', capturedAt: '2026-10-03T00:00:00Z', bindings: {}, decisions: { [producer.source_mapping_id!]: { outcome: 'acknowledged', reviewer: 'Synthetic reviewer', reviewedAt: '2026-10-03T00:00:00Z', importRevision: 'legacy', targetDigest: 'legacy', note: 'Historical annotation' } } } } } } }
  const seeded = await page.context().request.put(`/api/v1/scripts/${stored.id}`, { data: { script: legacy } })
  expect(seeded.ok(), await seeded.text()).toBeTruthy()
  await page.getByRole('button', { name: /^Open .* in Visual Editor$/ }).click()
  await expect(page.getByRole('region', { name: 'Author center workbench', exact: true })).toBeVisible()
  // Opening the editor uses the in-memory import. Navigate through the stored
  // edit route to inspect the injected legacy annotation after a fresh load.
  await page.goto(`/#/scripts?mode=edit&scriptId=${stored.id}&${scopeQuery(workspace)}`)
  if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
  await page.getByRole('tab', { name: 'Source Mapping', exact: true }).click()
  const reopened = page.getByRole('region', { name: 'Original import source review', exact: true })
  const reopenedEvidence = page.getByRole('region', { name: 'Captured correlation evidence', exact: true })
  await expect(reopenedEvidence).toBeVisible()
  await reopenedEvidence.getByText(/^Review captured correlations \(\d+\)$/).click()
  await reopenedEvidence.getByRole('article', { name: correlationLabel, exact: true }).getByRole('button', { name: 'View producer source mapping', exact: true }).click()
  await expect(reopened.locator('tr[data-mapping-id].active')).toHaveAttribute('data-mapping-id', producer.source_mapping_id!)
  await expect(reopened.getByRole('button', { name: 'Record review decision', exact: true })).toHaveCount(0)
  await expect(reopened.getByRole('button', { name: 'Edit destination in Author', exact: true })).toHaveCount(0)
  await expect(reopened).not.toContainText('Historical annotation')
  const authorPanes = page.getByRole('group', { name: 'Author panes', exact: true })
  if (await authorPanes.isVisible()) await authorPanes.getByRole('button', { name: 'Outline', exact: true }).click()
  await page.getByRole('textbox', { name: 'Script name', exact: true }).fill('Reviewed native correlation draft')
  const edited = mutation(page, 'PUT', `/api/v1/scripts/${stored.id}`)
  await page.getByRole('button', { name: 'Save script', exact: true }).click()
  const editedResponse = await edited
  expect(editedResponse.ok(), await editedResponse.text()).toBeTruthy()
  await page.reload()
  await page.getByRole('tab', { name: 'Source Mapping', exact: true }).click()
  await expect(reopened).toBeVisible()
  await expect(reopenedEvidence).toBeVisible()
  await expect(reopened).not.toContainText('Destination identity')
  const fetched = await json(await page.context().request.get(`/api/v1/scripts/${stored.id}`))
  expect((fetched.script as Script).metadata.import.preserved.source_mapping_review).toBeUndefined()
})

// The target is a loopback fixture. Both replay and HAR diagnosis use the real
// controller, so missing observations stay independent of a failed assertion.
test('correlation diagnosis retains missing-consumer evidence and independent HTTP200 failures', async ({ page }, testInfo) => {
  test.setTimeout(90_000)
  const oldId = '001D000000IqhSLIAZ'
  const freshId = '001E000000IqhSLIAZ'
  let observedRequests = 0
  const target = createServer((_request, response) => {
    observedRequests++
    response.setHeader('Content-Type', 'application/json')
    response.end(JSON.stringify({ recordId: freshId }))
  })
  await new Promise<void>(resolve => target.listen(0, '127.0.0.1', resolve))
  try {
    const address = target.address()
    if (!address || typeof address === 'string') throw new Error('Missing loopback fixture address')
    const targetURL = `http://127.0.0.1:${address.port}`
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
    const request = page.context().request
    const workspace = await createWorkspace(request)
    const recordedEntry = (path: string, responseBody: string) => {
      const observation = entry(path, '', responseBody)
      observation.request.url = `${targetURL}${path}`
      return observation
    }
    const recording = (entries: ReturnType<typeof entry>[]) => ({ log: { version: '1.2', creator: { name: 'synthetic', version: '1' }, entries } })
    const openReplay = async (failAssertion: boolean) => {
      const stored = await request.post('/api/v1/scripts', { data: { project_id: workspace.projectId, script: {
        name: failAssertion ? 'Correlation evidence with assertion failure' : 'Correlation evidence with no detected bindings', version: '1.0', protocol: 'http', config: { base_url: targetURL },
        actions: [
          { name: 'Produce record', type: 'request', method: 'GET', target: '/producer' },
          { name: 'Consume record', type: 'request', method: 'GET', target: `/consume?recordId=${oldId}`, ...(failAssertion ? { assertions: [{ type: 'status_code', expected: 201 }] } : {}) },
        ],
      } } })
      expect(stored.ok(), await stored.text()).toBeTruthy()
      const id = idOf(await json(stored))
      await page.goto(`/#/scripts?mode=edit&scriptId=${id}&${scopeQuery(workspace)}`)
      if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await page.reload()
      await page.getByRole('group', { name: 'Script workspace' }).getByRole('button', { name: 'Debug', exact: true }).click()
      await page.getByRole('button', { name: 'Debug options (0)', exact: true }).click()
      const options = page.getByRole('region', { name: 'Session Overrides' })
      await options.getByRole('checkbox', { name: 'Step through (start paused)' }).uncheck()
      await options.getByRole('checkbox', { name: 'Break on failure', exact: true }).uncheck()
      await options.getByRole('combobox', { name: 'Evidence capture', exact: true }).selectOption('diagnostic')
      await options.getByRole('button', { name: 'Done', exact: true }).click()
      await page.locator('.topbar-debug-action-run').click()
      await expectDebugOutcome(page, failAssertion ? 'failed' : 'passed')
      await page.getByText('Compare with a recording (optional)', { exact: true }).click()
      const panel = page.locator('.correlation-diagnose')
      await expect(panel.getByRole('button', { name: 'Diagnose from recording (HAR)…', exact: true })).toBeEnabled()
      return panel
    }
    type Diagnosis = { suggestions: { rule_id: string }[]; relationship_coverage: { complete: boolean; producer_count: number; consumer_count: number; missing_consumers: number; state_counts: Record<string, number> } }
    const upload = async (capture: ReturnType<typeof recording>) => {
      const diagnosed = mutation(page, 'POST', '/api/v1/correlations/diagnose')
      await page.locator('.correlation-diagnose-file-input').setInputFiles({ name: 'synthetic-correlation.har', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(capture)) })
      const response = await diagnosed
      expect(response.ok(), await response.text()).toBeTruthy()
      return await json(response) as unknown as Diagnosis
    }
    await page.setViewportSize({ width: 1800, height: 1100 })
    const panel = await openReplay(true)
    const result = await upload(recording([
      recordedEntry('/producer', JSON.stringify({ recordId: oldId })),
      recordedEntry(`/consume?recordId=${oldId}`, '{}'),
      recordedEntry(`/unreplayed?recordId=${oldId}`, '{}'),
    ]))
    expect(observedRequests).toBe(2)
    expect(result.relationship_coverage.complete).toBe(false)
    expect(result.relationship_coverage.consumer_count).toBe(2)
    expect(result.relationship_coverage.missing_consumers).toBe(1)
    expect(result.relationship_coverage.state_counts.stale_literal).toBe(1)
    expect(result.suggestions.some(item => item.rule_id === 'replay_assertion_failed_unexplained')).toBe(true)
    expect(JSON.stringify(result.relationship_coverage)).not.toContain(oldId)
    expect(JSON.stringify(result.relationship_coverage)).not.toContain(freshId)
    const evidence = page.getByRole('region', { name: 'Correlation relationship evidence', exact: true })
    await expect(evidence).toContainText('Relationship inspection incomplete.')
    await expect(evidence).toContainText('1 missing replay consumer')
    await expect(evidence).toContainText('Recorded literal remains in replay consumer: 1')
    await expect(panel).toContainText('Replay step failed without a detected cause')
    await evidence.getByText('Review observed relationships (2)', { exact: true }).click()
    await expect(evidence).toContainText('Producer step ID 0 → consumer step ID 2: Replay consumer missing')
    await captureReviewViewport(page, evidence, testInfo.outputPath('correlation-diagnosis-desktop.png'))
    await page.setViewportSize({ width: 760, height: 1000 })
    expect(await evidence.evaluate(element => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
    await captureReviewViewport(page, evidence, testInfo.outputPath('correlation-diagnosis-narrow.png'))
    await page.setViewportSize({ width: 1800, height: 1100 })
    const successfulPanel = await openReplay(false)
    const empty = await upload(recording([
      recordedEntry('/producer', '{"message":"no dynamic bindings"}'),
      recordedEntry('/consumer', '{}'),
      recordedEntry('/unreplayed', '{}'),
    ]))
    expect(observedRequests).toBe(4)
    expect(empty.suggestions).toHaveLength(0)
    expect(empty.relationship_coverage.complete).toBe(false)
    expect(empty.relationship_coverage.producer_count).toBe(0)
    await expect(successfulPanel).toContainText('Correlation relationship evidence is incomplete.')
    await expect(successfulPanel).toContainText('No producer bindings detected. This does not prove login or session bootstrap coverage.')
    await expect(successfulPanel).not.toContainText('No correlation issues detected')
  } finally {
    target.closeAllConnections()
    await new Promise<void>((resolve, reject) => target.close(error => error ? reject(error) : resolve()))
  }
})
