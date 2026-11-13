// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ScriptDefinition } from '../types/scripts'
import ImportRuntimeSettingsTable from './ImportRuntimeSettingsTable'
import { ImportSettingsProvider, rebaseOriginalSetting } from './ImportSettingsContext'
import { resetImportRuntimeFieldRequests } from './importRuntimeSettingsModel'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../hooks/apiClient', async importOriginal => ({ ...await importOriginal<typeof import('../hooks/apiClient')>(), request: mocks.request }))

const postCalls = () => mocks.request.mock.calls.filter(call => call[0] === '/scripts/import/settings')
const postedChanges = (index: number) => JSON.parse(postCalls()[index][1].body).changes

const withToken = (script: ScriptDefinition, token: string): ScriptDefinition => ({ ...script, metadata: { import: { source: 'loadrunner', preserved: { server_import_attestation_v1: token } } } })
const draftWith = (config: Partial<ScriptDefinition['config']>, token = 'tok-1'): ScriptDefinition => withToken({
 name: 'Imported', version: '1.0', protocol: 'http', actions: [],
 config: { base_url: 'https://example.test', ...config } as ScriptDefinition['config'],
}, token)
const applied = (script: ScriptDefinition) => ({ script, findings: [], validation_issues: [], readiness: { replay_ready: false, promotion_ready: false }, secret_binding_requirements: [] })

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

const button = (text: string) => Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(item => item.textContent === text)!
const byLabel = <T extends Element>(label: string) => container.querySelector<T>(`[aria-label="${label}"]`)
const allSettings = () => Array.from(container.querySelectorAll<HTMLButtonElement>('.import-provenance-filter-bar button')).find(item => item.textContent?.startsWith('All settings'))!
const provenanceCount = (option: string) => Number(Array.from(container.querySelectorAll('.import-provenance-filter-bar button')).find(item => item.textContent?.startsWith(option))?.textContent?.match(/\((\d+)\)/)?.[1])
const setInput = (label: string, value: string) => {
 const input = byLabel<HTMLInputElement>(label)!
 Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
 input.dispatchEvent(new Event('input', { bubbles: true }))
}
const setSelect = (label: string, value: string) => {
 const select = byLabel<HTMLSelectElement>(label)!
 select.value = value
 select.dispatchEvent(new Event('change', { bubbles: true }))
}

// Re-renders with each applied draft, as the import hosts do, and offers a
// non-settings rotation (a transport apply or guided repair) of the same import.
let rotate: (next: (draft: ScriptDefinition) => ScriptDefinition, identity?: string) => void = () => undefined
function Host({ initial, initialIdentity }: { initial: ScriptDefinition; initialIdentity?: string }) {
 const [draft, setDraft] = useState(initial)
 const [identity, setIdentity] = useState(initialIdentity)
 rotate = (next, nextIdentity) => { setDraft(next); if (nextIdentity !== undefined) setIdentity(nextIdentity) }
 return <ImportSettingsProvider script={draft} identity={identity} disabled={false} onApplied={result => setDraft(result.script)} onBusyChange={vi.fn()}>
  <ImportRuntimeSettingsTable script={draft} />
 </ImportSettingsProvider>
}

describe('think time mode reset', () => {
 it('drops the companions a mode change queued when the mode is reset to imported', async () => {
  await act(async () => root.render(<Host initial={draftWith({ think_time: { type: 'fixed', mean_ms: 1000 } })} />))
  await act(async () => allSettings().click())
  await act(async () => button('Edit Think time').click())
  await act(async () => setSelect('Think time: Mode', 'random_percentage'))
  expect(byLabel<HTMLInputElement>('Think time: Minimum recorded delay (%)')!.value).toBe('50')
  await act(async () => byLabel<HTMLButtonElement>('Reset Think time: Mode to imported value')!.click())
  expect(byLabel<HTMLSelectElement>('Think time: Mode')!.value).toBe('fixed')
  expect(byLabel<HTMLInputElement>('Think time: Mean delay (ms)')!.value).toBe('1000')
  await act(async () => button('Apply Think time').click())
  // Nothing is left to send, so Apply closes the editor without a request.
  expect(postCalls()).toHaveLength(0)
  expect(button('Edit Think time')).toBeTruthy()
 })

 it('sends only the mode when it is reset to default, and keeps a companion the user typed', async () => {
  mocks.request.mockImplementation((endpoint: string) => endpoint === '/scripts/import/settings' ? Promise.resolve(applied(draftWith({ think_time: { type: 'fixed', mean_ms: 1000 } }, 'tok-2'))) : Promise.reject(new Error('offline')))
  await act(async () => root.render(<Host initial={draftWith({ think_time: { type: 'fixed', mean_ms: 1000 } })} />))
  await act(async () => allSettings().click())
  await act(async () => button('Edit Think time').click())
  await act(async () => setSelect('Think time: Mode', 'random_percentage'))
  await act(async () => setInput('Think time: Maximum recorded delay (%)', '200'))
  await act(async () => byLabel<HTMLButtonElement>('Reset Think time: Mode to default')!.click())
  await act(async () => button('Apply Think time').click())
  const byPath = (changes: { path: string }[]) => [...changes].sort((left, right) => left.path.localeCompare(right.path))
  expect(byPath(postedChanges(0))).toEqual([
   { path: 'config.think_time.max_percent', value: 200 },
   { path: 'config.think_time.type', value: null },
  ])
 })

 it('replaces the companions of an earlier mode change when the mode changes again', async () => {
  mocks.request.mockImplementation((endpoint: string) => endpoint === '/scripts/import/settings' ? Promise.resolve(applied(draftWith({ think_time: { type: 'gaussian', mean_ms: 1000 } }, 'tok-2'))) : Promise.reject(new Error('offline')))
  await act(async () => root.render(<Host initial={draftWith({ think_time: { type: 'fixed', mean_ms: 1000 } })} />))
  await act(async () => allSettings().click())
  await act(async () => button('Edit Think time').click())
  await act(async () => setSelect('Think time: Mode', 'random_percentage'))
  await act(async () => setSelect('Think time: Mode', 'gaussian'))
  await act(async () => button('Apply Think time').click())
  expect(postedChanges(0)).toEqual([{ path: 'config.think_time.type', value: 'gaussian' }])
 })
})

describe('think time mode reset after an applied mode change', () => {
 it('restores the imported companions instead of fresh defaults for the imported mode', async () => {
  mocks.request.mockImplementation((endpoint: string) => endpoint === '/scripts/import/settings'
   ? Promise.resolve(applied(draftWith({ think_time: { type: 'random_percentage', min_percent: 50, max_percent: 150 } }, `tok-${postCalls().length + 1}`)))
   : Promise.reject(new Error('offline')))
  await act(async () => root.render(<Host initial={draftWith({ think_time: { type: 'fixed', mean_ms: 1000 } })} />))
  await act(async () => allSettings().click())
  await act(async () => button('Edit Think time').click())
  await act(async () => setSelect('Think time: Mode', 'random_percentage'))
  await act(async () => button('Apply Think time').click())
  expect(postCalls()).toHaveLength(1)

  await act(async () => button('Edit Think time').click())
  await act(async () => byLabel<HTMLButtonElement>('Reset Think time: Mode to imported value')!.click())
  expect(byLabel<HTMLInputElement>('Think time: Mean delay (ms)')!.value).toBe('1000')
  await act(async () => button('Apply Think time').click())
  const byPath = (changes: { path: string }[]) => [...changes].sort((left, right) => left.path.localeCompare(right.path))
  expect(byPath(postedChanges(1))).toEqual([
   { path: 'config.think_time.max_percent', value: null },
   { path: 'config.think_time.mean_ms', value: 1000 },
   { path: 'config.think_time.min_percent', value: null },
   { path: 'config.think_time.type', value: 'fixed' },
  ])
 })
})

describe('review-open baseline across rotations', () => {
 it('starts a new baseline when the host switches to another script with the same name', async () => {
  mocks.request.mockImplementation((endpoint: string) => endpoint === '/scripts/import/settings' ? Promise.resolve(applied(draftWith({ http: { request_timeout_ms: 4500 } }, 'tok-2'))) : Promise.reject(new Error('offline')))
  await act(async () => root.render(<Host initial={draftWith({ http: { request_timeout_ms: 3000 } })} initialIdentity="item-1:0" />))
  await act(async () => button('Edit HTTP').click())
  await act(async () => setInput('HTTP: Request timeout (ms)', '4500'))
  await act(async () => button('Apply HTTP').click())
  expect(provenanceCount('Modified')).toBe(1)
  // Script B shares the name, project, protocol and source, and its timeout equals A's draft.
  await act(async () => rotate(() => draftWith({ http: { request_timeout_ms: 4500 } }, 'tok-b'), 'item-1:1'))
  expect(provenanceCount('Modified')).toBe(0)
  await act(async () => button('Edit HTTP').click())
  expect(byLabel('Reset HTTP: Request timeout (ms) to imported value')).toBeNull()
 })

 it('keeps an applied edit Modified and resettable after a transport or repair rotation', async () => {
  const draft = draftWith({ http: { request_timeout_ms: 3000 } })
  mocks.request.mockImplementation((endpoint: string) => endpoint === '/scripts/import/settings' ? Promise.resolve(applied(draftWith({ http: { request_timeout_ms: 4500 } }, 'tok-2'))) : Promise.reject(new Error('offline')))
  await act(async () => root.render(<Host initial={draft} />))
  await act(async () => button('Edit HTTP').click())
  await act(async () => setInput('HTTP: Request timeout (ms)', '4500'))
  await act(async () => button('Apply HTTP').click())
  expect(provenanceCount('Modified')).toBe(1)

  // A transport apply changes keep-alive and rotates the attestation token.
  await act(async () => rotate(current => withToken({ ...current, config: { ...current.config, http: { ...current.config.http, keep_alive: false } } }, 'tok-3')))
  // The timeout edit still counts; the rotation's own keep-alive value is the new baseline.
  expect(provenanceCount('Modified')).toBe(1)
  await act(async () => button('Edit HTTP').click())
  expect(byLabel('Reset HTTP: Keep connections alive to imported value')).toBeNull()
  await act(async () => byLabel<HTMLButtonElement>('Reset HTTP: Request timeout (ms) to imported value')!.click())
  await act(async () => button('Apply HTTP').click())
  expect(postedChanges(1)).toEqual([{ path: 'config.http.request_timeout_ms', value: 3000 }])
 })

 it('starts a new baseline when the import itself is replaced', async () => {
  mocks.request.mockImplementation((endpoint: string) => endpoint === '/scripts/import/settings' ? Promise.resolve(applied(draftWith({ http: { request_timeout_ms: 4500 } }, 'tok-2'))) : Promise.reject(new Error('offline')))
  await act(async () => root.render(<Host initial={draftWith({ http: { request_timeout_ms: 3000 } })} />))
  await act(async () => button('Edit HTTP').click())
  await act(async () => setInput('HTTP: Request timeout (ms)', '4500'))
  await act(async () => button('Apply HTTP').click())
  expect(provenanceCount('Modified')).toBe(1)
  await act(async () => rotate(() => ({ ...draftWith({ http: { request_timeout_ms: 4500 } }, 'tok-9'), name: 'Another script' })))
  expect(provenanceCount('Modified')).toBe(0)
 })

 it('rebases only the settings a rotation changed', () => {
  expect(rebaseOriginalSetting(
   { http: { request_timeout_ms: 3000, keep_alive: true } },
   { http: { request_timeout_ms: 4500, keep_alive: true } },
   { http: { request_timeout_ms: 4500, keep_alive: false, version: '2' } },
  )).toEqual({ http: { request_timeout_ms: 3000, keep_alive: false, version: '2' } })
  // A section the rotation removed leaves only the settings it did not touch.
  expect(rebaseOriginalSetting({ proxy: { host: 'a' }, base: 'x' }, { proxy: { host: 'b' }, base: 'x' }, { base: 'x' })).toEqual({ base: 'x' })
 })
})

describe('reset to an unsupported imported value', () => {
 it('offers no reset to an imported legacy TLS version and says why', async () => {
  mocks.request.mockImplementation((endpoint: string) => endpoint === '/scripts/import/settings' ? Promise.resolve(applied(draftWith({ http: { tls: { min_version: '1.2' } } }, 'tok-2'))) : Promise.reject(new Error('offline')))
  await act(async () => root.render(<Host initial={draftWith({ http: { tls: { min_version: '1.0' } } })} />))
  await act(async () => button('Edit HTTP').click())
  // While the draft still holds 1.0, choosing it again simply drops the edit.
  await act(async () => setSelect('HTTP: Minimum TLS version', '1.3'))
  expect(byLabel('Reset HTTP: Minimum TLS version to imported value')).not.toBeNull()
  await act(async () => setSelect('HTTP: Minimum TLS version', '1.2'))
  await act(async () => button('Apply HTTP').click())
  expect(postedChanges(0)).toEqual([{ path: 'config.http.tls.min_version', value: '1.2' }])

  await act(async () => button('Edit HTTP').click())
  expect(byLabel('Reset HTTP: Minimum TLS version to imported value')).toBeNull()
  expect(byLabel('Reset HTTP: Minimum TLS version to default')).not.toBeNull()
  expect(byLabel('HTTP: Minimum TLS version')!.closest('label')!.textContent).toContain('The imported value 1.0 is not a supported option, so it cannot be restored. Legacy TLS versions stay migrated.')
 })
})
