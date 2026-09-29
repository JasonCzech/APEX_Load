// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ScriptDefinition } from '../types/scripts'
import { ParameterValuePreview } from './ParameterValuePreview'
import ParameterRenameInput from './ParameterRenameInput'
import { RuntimeFieldEvidence } from './RuntimeFieldEvidence'
import { RuntimeCountLimit } from './RuntimeCountLimit'
import { ParameterDefinitionGuide } from './ParameterDefinitionGuide'
import { RuntimeContextSummary } from './RuntimeContextSummary'
import { formatRuntimeEvidenceValue } from './runtimeFieldEvidenceModel'
import { request } from '../hooks/apiClient'

vi.mock('../hooks/apiClient', () => ({ request: vi.fn() }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLDivElement
let root: Root
const script: ScriptDefinition = { name: 'Settings', protocol: 'http', version: '1', actions: [], config: { base_url: '', think_time: { type: 'fixed', mean_ms: 2000 }, http: { keep_alive: false, max_redirects: 0 } }, parameters: [{ name: 'counter', type: 'counter', start: 4 }] }
function button(name: string) { return Array.from(container.querySelectorAll('button')).find(item => item.textContent === name)! }
function change(input: HTMLInputElement | HTMLSelectElement, value: string) {
  act(() => { Object.getOwnPropertyDescriptor(input instanceof HTMLInputElement ? HTMLInputElement.prototype : HTMLSelectElement.prototype, 'value')!.set!.call(input, value); input.dispatchEvent(new Event(input instanceof HTMLInputElement ? 'input' : 'change', { bubbles: true })) })
}
beforeEach(() => { vi.resetAllMocks(); container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(() => { act(() => root.unmount()); container.remove() })

describe('settings workspace review regressions', () => {
  it('restores an explicit cap after No cap and leaves zero distinct from inheritance', () => {
    const onChange = vi.fn()
    act(() => root.render(<RuntimeCountLimit label="Iterations" value={7} onChange={onChange} help="Per user" />))
    act(() => (container.querySelector('[type="checkbox"]') as HTMLInputElement).click())
    expect(onChange).toHaveBeenLastCalledWith(0)
    act(() => root.render(<RuntimeCountLimit label="Iterations" value={0} onChange={onChange} help="Per user" />))
    act(() => (container.querySelector('[type="checkbox"]') as HTMLInputElement).click())
    expect(onChange).toHaveBeenLastCalledWith(7)
  })
  it('shows definition provenance in Debug without displaying inline credential values', () => {
    act(() => root.render(<ParameterDefinitionGuide script={{ ...script, parameters: [{ name: 'password', type: 'list', values: ['never-display'], update: 'once' }] }} />))
    expect(container.textContent).toContain('Once per virtual user')
    expect(container.textContent).toContain('Inline values')
    expect(container.textContent).not.toContain('never-display')
    expect(container.textContent).toContain('may reflect an earlier draft')
  })
  it('only commits parameter renames explicitly and lets Escape discard a draft', () => {
    const rename = vi.fn()
    act(() => root.render(<ParameterRenameInput name="customer" onRename={rename} />))
    change(container.querySelector('input')!, 'account')
    expect(rename).not.toHaveBeenCalled()
    act(() => button('Rename references').click())
    expect(rename).toHaveBeenCalledWith('account')
    act(() => container.querySelector('input')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
    expect(container.querySelector('input')!.value).toBe('customer')
  })
  it('discards a response when allocation changes while preview is in flight and masks current results', async () => {
    let resolve!: (value: unknown) => void
    vi.mocked(request).mockImplementationOnce(() => new Promise(done => { resolve = done }) as ReturnType<typeof request>)
    act(() => root.render(<ParameterValuePreview script={script} name="counter" />))
    act(() => button('Preview value').click())
    change(container.querySelector('[aria-label="Preview iteration"]')!, '2')
    await act(async () => { resolve({ resolved: true, masked: false, value: '4', note: 'engine' }); await Promise.resolve() })
    expect(container.querySelector('output')).toBeNull()
    vi.mocked(request).mockResolvedValueOnce({ resolved: true, masked: false, value: '5', note: 'engine' })
    await act(async () => { button('Preview value').click() })
    expect(container.querySelector('output')?.textContent).toBe('••••••••')
    act(() => button('Reveal value').click())
    expect(container.querySelector('output')?.textContent).toBe('5')
    change(container.querySelector('[aria-label="Preview iteration"]')!, '3')
    expect(container.querySelector('output')).toBeNull()
  })
  it('keeps engine-redacted preview values protected', async () => {
    vi.mocked(request).mockResolvedValueOnce({ resolved: true, masked: true, value: '[REDACTED]', note: 'engine' })
    act(() => root.render(<ParameterValuePreview script={script} name="counter" />))
    await act(async () => { button('Preview value').click() })
    expect(button('Reveal value')).toBeUndefined()
    expect(container.textContent).toContain('Credential-like values stay redacted')
  })
  it('reads backend support and defaults while preserving explicit false and zero', async () => {
    vi.mocked(request).mockImplementation(async path => path === '/runtime-settings/support-matrix'
      ? { support_matrix: [{ id: 'http', protocols: ['http'], fields: ['config.http.keep_alive', 'config.http.max_redirects', 'config.http.request_timeout_ms'], status: 'executed', concept: 'HTTP replay defaults', execution_scope: 'Script HTTP transport' }] }
      : { config: { http: { keep_alive: true, max_redirects: 10, request_timeout_ms: 30000 } } })
    await act(async () => { root.render(<RuntimeFieldEvidence script={script} baseline={script.config} fields={['http']} sectionId="http" sectionLabel="HTTP Transport" />) })
    const rows = container.querySelectorAll('tbody tr')
    expect(container.querySelector('.runtime-evidence-heading')?.textContent).toContain('HTTP Transport')
    expect(container.querySelector('[data-runtime-section-icon="http"]')).toBeTruthy()
    expect(rows[0].querySelector('.runtime-evidence-setting-name')?.textContent).toBe('Keep connections alive')
    expect(rows[0].querySelector('.runtime-evidence-setting-path')?.textContent).toBe('http.keep_alive')
    expect(rows[0].querySelectorAll('td')[0].textContent).toBe('Off')
    expect(rows[0].querySelector('.runtime-evidence-origin')?.textContent).toBe('Script value')
    expect(rows[1].querySelectorAll('td')[0].textContent).toBe('0')
    expect(rows[2].querySelectorAll('td')[0].textContent).toBe('30000')
    expect(rows[2].querySelector('.runtime-evidence-origin')?.textContent).toBe('Engine default')
    expect(rows[0].querySelector('.runtime-evidence-support')?.textContent).toBe('Executed')
    expect(rows[0].querySelector('.runtime-evidence-support')?.classList.contains('healthy')).toBe(true)
    expect(rows[0].querySelector('.runtime-evidence-row-details')?.textContent).toContain('Script HTTP transport')
    expect(rows[0].querySelector('.runtime-evidence-row-details strong')?.textContent).toBe('Execution scope:')
    expect(rows[0].querySelector('.runtime-evidence-row-details details')).toBeNull()
    expect(formatRuntimeEvidenceValue('secret value', 'config.http.proxy_password')).toBe('Protected value')
  })
  it('distinguishes edited and imported origins, protects values, and uses a readable fallback label', async () => {
    vi.mocked(request).mockImplementation(async path => path === '/runtime-settings/support-matrix'
      ? { support_matrix: [{ id: 'http', protocols: ['http'], fields: ['config.http.keep_alive', 'config.http.max_redirects', 'config.http.proxy.password', 'config.http.unlisted_policy'], status: 'partial' }] }
      : { config: { http: {} } })
    const importedScript: ScriptDefinition = {
      ...script,
      config: { ...script.config, http: { ...script.config.http, proxy: { password: 'never-display' } } },
      metadata: { import: { preserved: { runtime_setting_dispositions: { 'default.cfg': { WEB: { Redirects: { status: 'mapped', fields: ['config.http.max_redirects'] } } } } } } },
    }
    await act(async () => { root.render(<RuntimeFieldEvidence script={importedScript} baseline={{ ...script.config, http: { ...script.config.http, keep_alive: true } }} fields={['http']} />) })
    const rows = Array.from(container.querySelectorAll('tbody tr'))
    const byPath = (path: string) => rows.find(row => row.querySelector('.runtime-evidence-setting-path')?.textContent === path)!
    expect(byPath('http.keep_alive').querySelector('.runtime-evidence-origin')?.textContent).toBe('Edited')
    expect(byPath('http.max_redirects').querySelector('.runtime-evidence-origin')?.textContent).toBe('Imported')
    expect(byPath('http.proxy.password').textContent).toContain('Protected value')
    expect(container.textContent).not.toContain('never-display')
    expect(byPath('http.unlisted_policy').querySelector('.runtime-evidence-setting-name')?.textContent).toBe('Unlisted Policy')
    expect(byPath('http.keep_alive').querySelector('.runtime-evidence-support')?.textContent).toBe('Partial')
    expect(byPath('http.keep_alive').querySelector('.runtime-evidence-support')?.classList.contains('warning')).toBe(true)
  })
  it('keeps protocol applicability distinct from support status', async () => {
    vi.mocked(request).mockImplementation(async path => path === '/runtime-settings/support-matrix'
      ? { support_matrix: [{ id: 'unsupported-http', protocols: ['browser'], fields: ['config.http.keep_alive'], status: 'executed', concept: 'Browser session reuse', notes: 'Only supported during browser replay.' }] }
      : { config: { http: {} } })
    await act(async () => { root.render(<RuntimeFieldEvidence script={script} fields={['http']} />) })
    expect(container.querySelector('.runtime-evidence-other > summary')?.textContent).toBe('Other protocol contexts (1)')
    expect(container.querySelector('.runtime-evidence-other')?.hasAttribute('open')).toBe(false)
    expect(container.querySelector('.runtime-evidence-support')).toBeNull()
    expect(container.querySelector('.runtime-evidence-row-details')?.textContent).toContain('Only supported during browser replay.')
    expect(container.textContent).not.toContain('Check protocol applicability')
  })
  it('shows loading and request failures without inventing support status', async () => {
    let reject!: (error: Error) => void
    vi.mocked(request).mockImplementation(() => new Promise((_resolve, fail) => { reject = fail }) as ReturnType<typeof request>)
    act(() => root.render(<RuntimeFieldEvidence script={script} fields={['http']} />))
    expect(container.querySelector('[role="status"]')?.textContent).toContain('Loading engine defaults and support')
    expect(container.querySelector('.runtime-evidence-support')).toBeNull()
    await act(async () => { reject(new Error('offline')); await Promise.resolve() })
    expect(container.querySelector('[role="status"]')?.textContent).toContain('Execution support is unavailable')
    expect(container.querySelector('.runtime-evidence-support')).toBeNull()
  })
  it('shows the global percentage policy on recorded action waits', () => {
    const percentageScript = { ...script, config: { ...script.config, think_time: { type: 'random_percentage', min_percent: 25, max_percent: 75 } }, actions: [{ name: 'wait', type: 'source_runtime', target: '', think_time_after: true, think_time: { type: 'fixed', mean_ms: 4000 } }] }
    act(() => root.render(<RuntimeContextSummary script={percentageScript} />))
    expect(container.querySelector('li')?.textContent).toContain('25–75% of recorded action delays')
  })
  it('separates Debug scope from the editable script and applies the selected ignore flag', () => {
    act(() => root.render(<RuntimeContextSummary script={script} debug={{ ignoreThinkTime: true, logLevel: 'debug', breakOnFailure: false }} />))
    const details = container.querySelector<HTMLDetailsElement>('.runtime-context-details')!
    expect(details.open).toBe(false)
    expect(container.querySelector('.runtime-context-toolbar')?.textContent).toContain('Editing: Script defaults')
    act(() => details.querySelector('summary')!.click())
    expect(details.open).toBe(true)
    expect(container.textContent).toContain('fixed 2 seconds')
    change(container.querySelector('select')!, 'debug')
    expect(container.querySelector('.runtime-context-toolbar')?.textContent).toContain('Editing: Script defaults')
    expect(details.querySelector('summary')?.textContent).toContain('Debug skips think time')
    expect(container.textContent).toContain('No think-time delay — overridden for Debug')
    expect(container.textContent).toContain('Replay executes one virtual user')
    expect(script.config.think_time?.ignore).toBeUndefined()
  })
})
