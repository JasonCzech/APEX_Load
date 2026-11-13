// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ScriptDefinition } from '../types/scripts'
import ImportIssueRuntimeEditor from './ImportIssueRuntimeEditor'
import { CategoryActions, ImportSettingsProvider, SettingValue } from './ImportSettingsContext'
import { readSetting, resetImportRuntimeFieldRequests } from './importRuntimeSettingsModel'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../hooks/apiClient', async importOriginal => ({ ...await importOriginal<typeof import('../hooks/apiClient')>(), request: mocks.request }))

const draftWith = (config: Partial<ScriptDefinition['config']>): ScriptDefinition => ({
 name: 'Imported', version: '1.0', protocol: 'http', actions: [],
 config: { base_url: 'https://example.test', ...config } as ScriptDefinition['config'],
 metadata: { import: { source: 'loadrunner', preserved: { server_import_attestation_v1: 'tok-1' } } },
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

const byLabel = <T extends Element>(label: string) => container.querySelector<T>(`[aria-label="${label}"]`)
const button = (text: string) => Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(item => item.textContent === text)

function Field({ initial, path, label, credentialReference }: { initial: ScriptDefinition; path: string; label: string; credentialReference?: boolean }) {
 const [draft, update] = useState(initial)
 return <ImportSettingsProvider script={draft} disabled={false} onApplied={result => update(result.script)} onBusyChange={vi.fn()}>
  <CategoryActions category="HTTP" />
  <SettingValue category="HTTP" path={path} label={label} value={readSetting(draft, path)} credentialReference={credentialReference} />
 </ImportSettingsProvider>
}

describe('import setting reset targets', () => {
 it('offers no Reset to default on Base URL, which the import refuses to clear', async () => {
  await act(async () => root.render(<Field initial={draftWith({})} path="config.base_url" label="Base URL" />))
  await act(async () => button('Edit HTTP')!.click())
  expect(byLabel('Reset Base URL to default')).toBeNull()
 })

 it('offers one reset when the imported value is unset, since both targets are the default', async () => {
  await act(async () => root.render(<Field initial={draftWith({})} path="config.http.request_timeout_ms" label="Request timeout" />))
  await act(async () => button('Edit HTTP')!.click())
  const input = byLabel<HTMLInputElement>('Request timeout')!
  await act(async () => {
   Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, '1200')
   input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  expect(byLabel('Reset Request timeout to default')).not.toBeNull()
  expect(byLabel('Reset Request timeout to imported value')).toBeNull()
 })

 it('does not offer to resend an imported proxy username after an apply replaced it', async () => {
  const imported = draftWith({ http: { proxy: { username: 'alice' } } } as Partial<ScriptDefinition['config']>)
  await act(async () => root.render(<Field initial={imported} path="config.http.proxy.username" label="Proxy username" credentialReference />))
  const bound: ScriptDefinition = { ...imported, config: { ...imported.config, http: { proxy: { username: '{{secret.PROXY_USER}}' } } } as ScriptDefinition['config'], metadata: { import: { source: 'loadrunner', preserved: { server_import_attestation_v1: 'tok-2' } } } }
  mocks.request.mockResolvedValueOnce({ script: bound, findings: [], validation_issues: [], readiness: { replay_ready: false, promotion_ready: false }, secret_binding_requirements: [] })
  await act(async () => button('Edit HTTP')!.click())
  const input = byLabel<HTMLInputElement>('Proxy username')!
  await act(async () => {
   Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, '{{secret.PROXY_USER}}')
   input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  await act(async () => button('Apply HTTP')!.click())
  // The applied draft now binds a reference; the imported literal would read as a new typed username.
  await act(async () => button('Edit HTTP')!.click())
  expect(byLabel<HTMLInputElement>('Proxy username')!.value).toBe('{{secret.PROXY_USER}}')
  expect(byLabel('Reset Proxy username to imported value')).toBeNull()
  expect(byLabel('Reset Proxy username to default')).not.toBeNull()
  expect(container.textContent).toContain('The imported value cannot be restored here')
 })

 it('still lets a pending edit of the imported proxy username be dropped', async () => {
  const imported = draftWith({ http: { proxy: { username: 'alice' } } } as Partial<ScriptDefinition['config']>)
  await act(async () => root.render(<Field initial={imported} path="config.http.proxy.username" label="Proxy username" credentialReference />))
  await act(async () => button('Edit HTTP')!.click())
  const input = byLabel<HTMLInputElement>('Proxy username')!
  await act(async () => {
   Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, '{{secret.PROXY_USER}}')
   input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  await act(async () => byLabel<HTMLButtonElement>('Reset Proxy username to imported value')!.click())
  expect(byLabel<HTMLInputElement>('Proxy username')!.value).toBe('alice')
 })
})

describe('import setting reset targets for credential-bearing URLs', () => {
 const applyTo = async (imported: ScriptDefinition, path: string, label: string, typed: string, applied: ScriptDefinition) => {
  await act(async () => root.render(<Field initial={imported} path={path} label={label} />))
  mocks.request.mockResolvedValueOnce({ script: applied, findings: [], validation_issues: [], readiness: { replay_ready: false, promotion_ready: false }, secret_binding_requirements: [] })
  await act(async () => button('Edit HTTP')!.click())
  const input = byLabel<HTMLInputElement>(label)!
  await act(async () => {
   Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, typed)
   input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  await act(async () => button('Apply HTTP')!.click())
  await act(async () => button('Edit HTTP')!.click())
 }
 const reattested = (draft: ScriptDefinition): ScriptDefinition => ({ ...draft, metadata: { import: { source: 'loadrunner', preserved: { server_import_attestation_v1: 'tok-2' } } } })

 it('does not offer to resend an imported base URL whose sig= parameter the apply dropped', async () => {
  const imported = draftWith({ base_url: 'https://acct.blob.example.test/c?sv=2020&sig=abc123' })
  await applyTo(imported, 'config.base_url', 'Base URL', 'https://other.example.test', reattested(draftWith({ base_url: 'https://other.example.test' })))
  expect(byLabel('Reset Base URL to imported value')).toBeNull()
  expect(container.textContent).toContain('The imported value cannot be restored here')
 })

 it('offers to resend an imported base URL when the draft still carries its sig= parameter', async () => {
  const imported = draftWith({ base_url: 'https://acct.blob.example.test/c?sv=2020&sig=abc123' })
  await applyTo(imported, 'config.base_url', 'Base URL', 'https://other.blob.example.test/c?sv=2020&sig=abc123', reattested(draftWith({ base_url: 'https://other.blob.example.test/c?sv=2020&sig=abc123' })))
  expect(byLabel('Reset Base URL to imported value')).not.toBeNull()
 })

 it('offers to resend an imported proxy URL while the draft keeps its Credentials reference userinfo', async () => {
  const proxy = (url: string) => ({ http: { proxy: { url } } } as Partial<ScriptDefinition['config']>)
  const imported = draftWith(proxy('http://alice:{{secret.PROXY_PW}}@proxy.example.test:8080'))
  await applyTo(imported, 'config.http.proxy.url', 'Proxy URL', 'http://alice:{{secret.PROXY_PW}}@proxy.example.test:9090', reattested(draftWith(proxy('http://alice:{{secret.PROXY_PW}}@proxy.example.test:9090'))))
  expect(byLabel('Reset Proxy URL to imported value')).not.toBeNull()
  expect(container.textContent).not.toContain('The imported value cannot be restored here')
 })

 it('does not offer to resend an imported proxy URL whose masked userinfo the apply dropped', async () => {
  // The import masks the whole proxy URL userinfo (an empty password too),
  // and refuses userinfo the draft no longer carries.
  const proxy = (url: string) => ({ http: { proxy: { url } } } as Partial<ScriptDefinition['config']>)
  await applyTo(draftWith(proxy('http://svc:@proxy.example.test:8080')), 'config.http.proxy.url', 'Proxy URL', 'http://proxy.example.test:8080', reattested(draftWith(proxy('http://proxy.example.test:8080'))))
  expect(byLabel('Reset Proxy URL to imported value')).toBeNull()
  expect(container.textContent).toContain('The imported value cannot be restored here')
 })

 it('offers to resend an imported proxy URL whose reference-only userinfo the apply dropped', async () => {
  // A {{secret.*}} reference password is not material: the import accepts it.
  const proxy = (url: string) => ({ http: { proxy: { url } } } as Partial<ScriptDefinition['config']>)
  await applyTo(draftWith(proxy('http://alice:{{secret.PROXY_PW}}@proxy.example.test:8080')), 'config.http.proxy.url', 'Proxy URL', 'http://proxy.example.test:8080', reattested(draftWith(proxy('http://proxy.example.test:8080'))))
  expect(byLabel('Reset Proxy URL to imported value')).not.toBeNull()
  expect(container.textContent).not.toContain('The imported value cannot be restored here')
 })

 it('does not offer to resend an imported base URL whose short token the apply dropped beside kept userinfo', async () => {
  // The kept userinfo password does not account for the dropped token=ab, which the import still refuses.
  const imported = draftWith({ base_url: 'https://user:pass1234@a.example.test/c?token=ab' })
  await applyTo(imported, 'config.base_url', 'Base URL', 'https://user:pass1234@a.example.test/c', reattested(draftWith({ base_url: 'https://user:pass1234@a.example.test/c' })))
  expect(byLabel('Reset Base URL to imported value')).toBeNull()
  expect(container.textContent).toContain('The imported value cannot be restored here')
 })

 it('does not offer to resend an imported base URL whose short token the apply dropped', async () => {
  const imported = draftWith({ base_url: 'https://a.example.test/c?token=ab' })
  await applyTo(imported, 'config.base_url', 'Base URL', 'https://a.example.test/c', reattested(draftWith({ base_url: 'https://a.example.test/c' })))
  expect(byLabel('Reset Base URL to imported value')).toBeNull()
  expect(container.textContent).toContain('The imported value cannot be restored here')
 })

 it('offers to resend an imported base URL while the draft keeps its short token', async () => {
  const imported = draftWith({ base_url: 'https://a.example.test/c?token=ab' })
  await applyTo(imported, 'config.base_url', 'Base URL', 'https://b.example.test/c?token=ab', reattested(draftWith({ base_url: 'https://b.example.test/c?token=ab' })))
  expect(byLabel('Reset Base URL to imported value')).not.toBeNull()
 })

 it('does not offer to resend an imported proxy URL whose literal password the apply dropped', async () => {
  const proxy = (url: string) => ({ http: { proxy: { url } } } as Partial<ScriptDefinition['config']>)
  const imported = draftWith(proxy('http://alice:pw@proxy.example.test:8080'))
  await applyTo(imported, 'config.http.proxy.url', 'Proxy URL', 'http://proxy.example.test:8080', reattested(draftWith(proxy('http://proxy.example.test:8080'))))
  expect(byLabel('Reset Proxy URL to imported value')).toBeNull()
 })
})

describe('guided TLS repair resets', () => {
 it('shows no reset before an edit when the draft leaves the TLS range unset', async () => {
  const draft = draftWith({})
  await act(async () => root.render(<ImportSettingsProvider script={draft} disabled={false} onApplied={vi.fn()} onBusyChange={vi.fn()}>
   <ImportIssueRuntimeEditor script={draft} migrationPending={false} tlsOnly />
  </ImportSettingsProvider>))
  const edit = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(item => item.textContent?.startsWith('Edit ') && item.textContent.endsWith(' here'))!
  await act(async () => edit.click())
  const selects = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
  expect(selects.map(select => select.value)).toEqual(['1.2', '1.3'])
  expect(container.querySelectorAll('.import-setting-reset-actions button')).toHaveLength(0)
 })
})
