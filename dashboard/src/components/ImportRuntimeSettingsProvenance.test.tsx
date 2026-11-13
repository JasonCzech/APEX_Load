// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ScriptDefinition } from '../types/scripts'
import { ApiError } from '../hooks/apiClient'
import ImportRuntimeSettingsTable from './ImportRuntimeSettingsTable'
import { ImportSettingsProvider, type SettingsResult } from './ImportSettingsContext'
import { resetImportRuntimeFieldRequests } from './importRuntimeSettingsModel'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../hooks/apiClient', async importOriginal => ({ ...await importOriginal<typeof import('../hooks/apiClient')>(), request: mocks.request }))

const FIELDS_ENDPOINT = '/scripts/import/settings/fields'
const postCalls = () => mocks.request.mock.calls.filter(call => call[0] === '/scripts/import/settings')

const loadRunnerDraft = (): ScriptDefinition => ({
 name: 'Imported',
 version: '1.0',
 protocol: 'http',
 config: { base_url: 'https://example.test', http: { request_timeout_ms: 3000 } },
 actions: [],
 metadata: { import: { source: 'loadrunner', preserved: {
  runtime_files: { 'default.cfg': { Web: { KeepAlive: 'Yes', CustomDecisionKey: 'needs-review', Superseded: '5' } } },
  runtime_setting_dispositions: { 'default.cfg': { Web: {
   KeepAlive: { status: 'mapped', parity_id: 'http_keepalive', fields: ['config.http.keep_alive'] },
   CustomDecisionKey: { status: 'partial', parity_id: 'http_decision', reason: 'manual_review' },
   Superseded: { status: 'non_applicable', note: 'Replaced by a newer setting.' },
  } } },
  runtime_settings_parity: [
   { id: 'http_keepalive', area: 'HTTP', concept: 'Keep-alive', status: 'executed', fields: ['config.http.keep_alive'] },
   { id: 'http_decision', area: 'HTTP', concept: 'Custom decision', status: 'partial' },
  ],
 } } },
})

let container: HTMLDivElement
let root: Root
beforeEach(() => {
 resetImportRuntimeFieldRequests()
 mocks.request.mockReset()
 container = document.createElement('div')
 document.body.append(container)
 root = createRoot(container)
})
afterEach(async () => {
 await act(async () => root.unmount())
 container.remove()
})

const buttons = (selector = 'button') => Array.from(container.querySelectorAll<HTMLButtonElement>(selector))
const button = (text: string) => buttons().find(item => item.textContent === text)!
const shownCount = () => Number(container.querySelector('.import-runtime-content > [role="status"]')?.textContent?.match(/(\d+) settings shown/)?.[1])
const content = () => container.querySelector('.import-runtime-content')?.textContent || ''
const setInput = (label: string, value: string) => {
 const input = container.querySelector<HTMLInputElement>(`[aria-label="${label}"]`)!
 Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
 input.dispatchEvent(new Event('input', { bubbles: true }))
}
const provenance = (option: string) => buttons('.import-provenance-filter-bar button').find(item => item.textContent?.startsWith(option))!
const provenanceCount = (option: string) => Number(provenance(option).textContent?.match(/\((\d+)\)/)?.[1])
const group = (name: string) => buttons('.import-canonical-group-nav button').find(item => item.textContent?.startsWith(name))!
const groupCount = (name: string) => Number(group(name).textContent?.slice(name.length).match(/^(\d+)/)?.[1])

// Re-renders with the applied draft, as the import hosts do.
function Host({ initial, onApplied }: { initial: ScriptDefinition; onApplied?: (result: SettingsResult) => void }) {
 const [draft, setDraft] = useState(initial)
 return <ImportSettingsProvider script={draft} disabled={false} onApplied={result => { onApplied?.(result); setDraft(result.script) }} onBusyChange={vi.fn()}>
  <ImportRuntimeSettingsTable script={draft} />
 </ImportSettingsProvider>
}

describe('import provenance filter', () => {
 it('offers All, Modified, Imported and Default, each counting exactly the rows it shows', async () => {
  await act(async () => root.render(<Host initial={loadRunnerDraft()} />))
  expect(buttons('.import-provenance-filter-bar button').map(item => item.textContent?.replace(/\s*\(\d+\)$/, ''))).toEqual(['All settings', 'Modified', 'Imported', 'Default'])

  await act(async () => provenance('All settings').click())
  // Not applicable rows count toward All settings, so the filter shows them too.
  expect(container.querySelector('section[aria-label="Not applicable"]')).not.toBeNull()
  expect(shownCount()).toBe(provenanceCount('All settings'))

  await act(async () => provenance('Imported').click())
  expect(container.querySelector('section[aria-label="Additional APEX settings"]')).toBeNull()
  expect(container.querySelector('section[aria-label="Requires decision"]')).not.toBeNull()
  expect(container.querySelector('section[aria-label="Mapped for replay"]')).not.toBeNull()
  expect(provenanceCount('Imported')).toBe(3)
  expect(shownCount()).toBe(3)

  await act(async () => provenance('Default').click())
  expect(content()).toContain('HTTP version')
  expect(content()).not.toContain('Request timeout (ms)')
  expect(shownCount()).toBe(provenanceCount('Default'))

  expect(provenanceCount('Modified')).toBe(0)
  await act(async () => provenance('All settings').click())
  await act(async () => button('Edit HTTP').click())
  await act(async () => setInput('HTTP: Request timeout (ms)', '4500'))
  expect(provenanceCount('Modified')).toBe(1)
  await act(async () => provenance('Modified').click())
  expect(shownCount()).toBe(1)
  expect(content()).toContain('Request timeout (ms)')
 })

 it('counts each canonical group as the rows the group shows once selected', async () => {
  await act(async () => root.render(<Host initial={loadRunnerDraft()} />))
  // The not-applicable Web row files under Transport & connections but stays hidden there.
  for (const name of ['Transport & connections', 'Errors, logging & diagnostics', 'All groups']) {
   const expected = groupCount(name)
   await act(async () => group(name).click())
   expect(shownCount(), name).toBe(expected)
  }
 })
})

describe('reset actions', () => {
 it('resets a pending edit to the imported value or to the default', async () => {
  await act(async () => root.render(<Host initial={loadRunnerDraft()} />))
  await act(async () => provenance('All settings').click())
  await act(async () => button('Edit HTTP').click())
  const timeout = () => container.querySelector<HTMLInputElement>('[aria-label="HTTP: Request timeout (ms)"]')!
  expect(container.querySelector('[aria-label="Reset HTTP: Request timeout (ms) to imported value"]')).toBeNull()

  await act(async () => setInput('HTTP: Request timeout (ms)', '4500'))
  await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="Reset HTTP: Request timeout (ms) to imported value"]')!.click())
  expect(timeout().value).toBe('3000')

  await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="Reset HTTP: Request timeout (ms) to default"]')!.click())
  expect(timeout().value).toBe('')
  mocks.request.mockResolvedValueOnce({ script: { ...loadRunnerDraft(), config: { base_url: 'https://example.test' } }, findings: [], readiness: {}, secret_binding_requirements: [] })
  await act(async () => button('Apply HTTP').click())
  expect(JSON.parse(postCalls()[0][1].body).changes).toEqual([{ path: 'config.http.request_timeout_ms', value: null }])
 })

 it('marks an applied edit Modified and restores the imported value after the draft rotates', async () => {
  const draft = loadRunnerDraft()
  const applied = { ...draft, config: { ...draft.config, http: { request_timeout_ms: 4500 } } }
  await act(async () => root.render(<Host initial={draft} />))
  await act(async () => provenance('All settings').click())
  await act(async () => button('Edit HTTP').click())
  await act(async () => setInput('HTTP: Request timeout (ms)', '4500'))
  mocks.request.mockResolvedValueOnce({ script: applied, findings: [], readiness: {}, secret_binding_requirements: [] })
  await act(async () => button('Apply HTTP').click())
  expect(provenanceCount('Modified')).toBe(1)

  await act(async () => button('Edit HTTP').click())
  await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="Reset HTTP: Request timeout (ms) to imported value"]')!.click())
  mocks.request.mockResolvedValueOnce({ script: draft, findings: [], readiness: {}, secret_binding_requirements: [] })
  await act(async () => button('Apply HTTP').click())
  expect(JSON.parse(postCalls()[1][1].body).changes).toEqual([{ path: 'config.http.request_timeout_ms', value: 3000 }])
 })
})

describe('apply errors', () => {
 it('shows a category issue that names another setting on that setting', async () => {
  const draft = loadRunnerDraft()
  await act(async () => root.render(<Host initial={draft} />))
  await act(async () => provenance('All settings').click())
  await act(async () => button('Edit Diagnostics').click())
  await act(async () => setInput('Diagnostics: Retained exchange limit', '50'))
  const issue = 'config.results.message_detail.mode must be off, errors, sampled, or adaptive'
  mocks.request.mockRejectedValueOnce(new ApiError('Check the highlighted settings', 422, { field_errors: { category: issue } }))
  await act(async () => button('Apply Diagnostics').click())
  const modeControl = container.querySelector('[aria-label="Diagnostics: Exchange capture"]')!.closest('label')!
  expect(modeControl.querySelector('[role="alert"]')?.textContent).toBe(issue)
 })
})

describe('served field list', () => {
 it('loads the field list for the script protocol and uses its fields and options', async () => {
  mocks.request.mockImplementation((endpoint: string) => Promise.resolve(endpoint.startsWith(FIELDS_ENDPOINT) ? {
   groups: [],
   fields: [
    { path: 'config.http.future_limit', category: 'HTTP', group: 'transport_connections', label: 'Future limit', type: 'int' },
    { path: 'config.think_time.type', category: 'Think time', group: 'think_time', type: 'select', options: ['none', 'fixed'] },
   ],
  } : undefined))
  const draft = loadRunnerDraft()
  await act(async () => root.render(<Host initial={draft} />))
  expect(mocks.request).toHaveBeenCalledWith(`${FIELDS_ENDPOINT}?protocol=http`)
  await act(async () => provenance('All settings').click())
  expect(content()).toContain('Future limit')
  await act(async () => button('Edit Think time').click())
  const mode = container.querySelector<HTMLSelectElement>('[aria-label="Think time: Mode"]')!
  expect(Array.from(mode.options).map(option => option.value)).toEqual(['', 'none', 'fixed'])
 })

 it('keeps the offline catalog when the field list cannot load', async () => {
  mocks.request.mockRejectedValue(new Error('offline'))
  await act(async () => root.render(<Host initial={{ ...loadRunnerDraft(), protocol: 'soap' }} />))
  expect(mocks.request).toHaveBeenCalledWith(`${FIELDS_ENDPOINT}?protocol=soap`)
  await act(async () => provenance('All settings').click())
  expect(content()).toContain('Fail action on SOAP Fault')
  expect(content()).toContain('HTTP version')
 })
})

describe('credential reference fields', () => {
 it('keeps an imported proxy username readable and says a new value needs a Credentials reference', async () => {
  const draft: ScriptDefinition = { ...loadRunnerDraft(), metadata: undefined }
  draft.config.http = { ...draft.config.http, proxy: { username: 'svc-proxy' } }
  await act(async () => root.render(<Host initial={draft} />))
  await act(async () => button('Edit HTTP').click())
  const input = container.querySelector<HTMLInputElement>('[aria-label="HTTP: Proxy username"]')!
  expect(input.type).toBe('text')
  expect(input.value).toBe('svc-proxy')
  expect(input.closest('label')?.textContent).toContain('{{secret.PROXY_USER}}')
 })
})
