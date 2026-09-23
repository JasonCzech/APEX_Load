// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { captureScript } from './debuggerExperience'
import { MessageDetailFields } from './MessageDetailFields'
import ResourceSnapshotList from './ResourceSnapshotList'
import { runtimeSettingRows } from './importRuntimeSettingsModel'
import { runtimeDraftFromOverride, overrideFromRuntimeDraft } from './runtimeSettingsModel'
import { buildRuntimeSettingsDraft } from './scenarioDraftUtils'
import type { ScriptDefinition } from '../types/scripts'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

it('edits resource capture without collapsing false and retains limits in override round trips', () => {
 const container = document.createElement('div'); const root = createRoot(container); const onChange = vi.fn()
 try {
  act(() => root.render(<MessageDetailFields value={{ mode: 'sampled', sample_rate: 1, include_resources: true, max_items: 128 }} onChange={onChange} />))
  const select = [...container.querySelectorAll('label')].find(label => label.textContent?.startsWith('Include fetched resources'))!.querySelector('select')!
  act(() => { select.value = 'false'; select.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ include_resources: false, max_items: 128, mode: 'sampled' }))
  const original = { results: { message_detail: { mode: 'sampled' as const, sample_rate: 1, include_resources: false } } }
  expect(overrideFromRuntimeDraft(runtimeDraftFromOverride(original))?.results).toEqual(original.results)
  expect(buildRuntimeSettingsDraft(original).results).toEqual(original.results)
 } finally { act(() => root.unmount()) }
})

it('shows resource body state, cache evidence and omitted counts as text', () => {
 const container = document.createElement('div'); const root = createRoot(container)
 try {
  act(() => root.render(<ResourceSnapshotList snapshot={{ resources: [{ request_url: 'https://example.test/resource', response_status: 200, response_body: '<script>bad()</script>', response_body_state: 'captured', cache_hit: true, truncated: true }], omitted_resources: 3 }} />))
  expect(container.textContent).toContain('cache')
  expect(container.textContent).toContain('retention limit')
  expect(container.textContent).toContain('3 resources omitted')
  expect(container.querySelector('script')).toBeNull()
 } finally { act(() => root.unmount()) }
})

it('keeps old snapshot classifications unchanged and explains new dormant rows separately from mapped false', () => {
 const script: ScriptDefinition = { name: 'Fixture', version: '1.0', protocol: 'http', config: { base_url: '', http: { dns_cache: false } }, actions: [], metadata: { import: { preserved: {
  runtime_files: { 'default.cfg': { WEB: { EnableSnapshotsInReplay: '1', EnableIPCache: '0', JsForTransportRuntimeSize: '51200' } } },
  runtime_setting_dispositions: { 'default.cfg': { WEB: {
   EnableSnapshotsInReplay: { status: 'non_applicable', note: 'Legacy importer classification.' },
   EnableIPCache: { status: 'mapped', parity_id: 'http_dns_cache', fields: ['config.http.dns_cache'] },
   JsForTransportRuntimeSize: { status: 'non_applicable', reason: 'disabled_feature', note: 'EnableJsForTransport=0 disables the parent.' },
  } } },
  runtime_settings_parity: [{ id: 'http_dns_cache', area: 'HTTP', concept: 'DNS cache', status: 'executed', fields: ['config.http.dns_cache'] }],
 } } } }
 const rows = runtimeSettingRows(script)
 expect(rows['Mapped for replay'].find(row => row.path === 'config.http.dns_cache')?.importedSummary).toBe('Disabled')
 expect(rows['Not applicable'].find(row => row.label === 'EnableSnapshotsInReplay')?.note).toContain('Legacy importer')
 expect(rows['Not applicable'].find(row => row.label === 'JsForTransportRuntimeSize')?.note).toContain('disabled feature')
 expect(script.config.results).toBeUndefined()
})

 it('errors-only debugger capture preserves resource preferences and retention limits', () => {
  for (const include of [true, false, undefined]) {
   const policy = { mode: 'sampled' as const, sample_rate: 1, include_resources: include, max_items: 7, max_item_bytes: 1024, max_total_bytes: 4096 }
   const script: ScriptDefinition = { name: 'fixture', version: '1', protocol: 'http', config: { base_url: '', results: { message_detail: policy } }, actions: [] }
   expect(captureScript(script, 'errors').config.results?.message_detail).toEqual({ ...policy, mode: 'errors' })
   expect(script.config.results?.message_detail).toEqual(policy)
  }
 })
