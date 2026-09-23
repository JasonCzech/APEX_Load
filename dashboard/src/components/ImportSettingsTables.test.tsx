// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import type { ScriptDefinition } from '../types/scripts'
import { ApiError } from '../hooks/apiClient'
import ImportRuntimeSettingsTable from './ImportRuntimeSettingsTable'
import { runtimeSettingRows } from './importRuntimeSettingsModel'
import ImportParameterInventory from './ImportParameterInventory'
import { ImportSettingsProvider, formatSettingValue, useImportSettings, type SettingsResult } from './ImportSettingsContext'
const mocks = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../hooks/apiClient', async importOriginal => ({ ...await importOriginal<typeof import('../hooks/apiClient')>(), request: mocks.request }))
const script = (): ScriptDefinition => ({ name: 'Draft', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test', http: { request_timeout_ms: 3000, keep_alive: false } }, actions: [], parameters: [{ name: 'region', type: 'list', values: ['west'] }] })

it('shows idle timeout seconds beside editable milliseconds and keeps superseded pacing inactive', () => {
 const draft = script()
 draft.config.http = { keep_alive: true, idle_timeout_ms: 60000 }
 draft.config.pacing = { type: 'fixed_interval', interval_ms: 5000 }
 draft.metadata = { import: { preserved: {
  runtime_files: { 'default.cfg': { WEB: { keepalivetimeout: '60' }, Iterations: { StartEvery: '99' } }, 'default.usp': { RunLogicRunRoot: { RunLogicPaceConstTime: '5' } } },
  runtime_setting_dispositions: {
   'default.cfg': { WEB: { keepalivetimeout: { status: 'mapped', parity_id: 'http_idle_timeout', fields: ['config.http.idle_timeout_ms'] } }, Iterations: { StartEvery: { status: 'non_applicable', parity_id: 'pacing', note: 'Superseded by default.usp' } } },
   'default.usp': { RunLogicRunRoot: { RunLogicPaceConstTime: { status: 'mapped', parity_id: 'pacing', fields: ['config.pacing'] } } },
  },
  runtime_settings_parity: [
   { id: 'http_idle_timeout', area: 'HTTP', concept: 'Idle timeout', status: 'executed', fields: ['config.http.idle_timeout_ms'] },
   { id: 'pacing', area: 'Pacing', concept: 'Pacing', status: 'executed', fields: ['config.pacing'] },
  ],
 } } }
 const groups = runtimeSettingRows(draft)
 const idle = groups['Mapped for replay'].find(row => row.path === 'config.http.idle_timeout_ms')!
 expect(idle.imported).toContain('keepalivetimeout: 60')
 expect(idle.importedSummary).toBe('60000 ms')
 expect(idle.field?.type).toBe('number')
 expect(groups['Mapped for replay'].some(row => row.imported.includes('StartEvery'))).toBe(false)
 expect(groups['Not applicable'].find(row => row.label === 'StartEvery')?.note).toContain('Superseded by default.usp')
})

it('hides empty statuses and places details after current draft values', () => {
 const container = document.createElement('div'); const root = createRoot(container)
 try {
  act(() => root.render(<ImportRuntimeSettingsTable script={script()} />))
  for (const name of ['Requires decision', 'Mapped for replay', 'Preserved only', 'Not applicable']) expect(container.querySelector(`section[aria-label="${name}"]`)).toBeNull()
  const section = container.querySelector('.import-runtime-content > [aria-label="Additional APEX settings"]')!
  expect(section.querySelector(':scope > details')).toBeNull()
  expect(section.querySelector(':scope > p')?.textContent).toBe('Draft settings without a reported source mapping.')
  expect(container.querySelector('input[aria-label="Additional APEX settings"]')).not.toBeNull()
  expect(container.textContent).not.toContain('Current runtime settings')
  expect([...section.querySelectorAll('thead th')].map(th => th.textContent)).toEqual(['Setting', 'Imported value', 'Current draft value', 'Details'])
  const row = section.querySelector('tbody tr:not(.import-category)')!
  expect(row.querySelector('th details')).toBeNull()
  expect(row.children[3].querySelector('details > summary')?.textContent).toBe('Details')
  expect(section.querySelector('.import-category th')?.getAttribute('colspan')).toBe('4')
  expect(container.querySelectorAll('.import-runtime-summary > label')).toHaveLength(1)
  expect(container.textContent).toContain('3000')
  expect(container.textContent).toContain('Disabled')
  expect(formatSettingValue(0)).toBe('0')
  expect(formatSettingValue(undefined)).toBe('Default / not set')
 } finally { act(() => root.unmount()) }
})
it('shows source values and preserved settings without duplicating mapped destination fields', () => {
 const draft = script()
 draft.metadata = { import: { preserved: {
  runtime_files: { 'default.cfg': { HTTP: { ReceiveTimeout: '3', Other: 'keep' } } },
  runtime_setting_dispositions: { 'default.cfg': { HTTP: { ReceiveTimeout: { status: 'mapped', parity_id: 'transport', fields: ['config.http.request_timeout_ms'] }, Other: { status: 'compatibility_only', note: 'Retained only' } } } },
  runtime_settings_parity: [{ id: 'transport', area: 'HTTP', concept: 'Timeout', status: 'executed', fields: ['config.http.request_timeout_ms'], runtime_files: ['default.cfg'] }],
 } } }
 const groups = runtimeSettingRows(draft)
 expect(groups['Mapped for replay'][0].imported).toContain('ReceiveTimeout: 3')
 expect(groups['Preserved only'][0].imported).toContain('keep')
 expect(Object.values(groups).flat().filter(r => r.path === 'config.http.request_timeout_ms')).toHaveLength(1)
})
it('collapses category explanations independently from visible rows and edit actions', async () => {
 const container = document.createElement('div'); const root = createRoot(container)
 try {
  await act(async () => root.render(<ImportSettingsProvider script={script()} disabled={false} onApplied={vi.fn()} onBusyChange={vi.fn()}><ImportRuntimeSettingsTable script={script()} /></ImportSettingsProvider>))
  const toggle = container.querySelector<HTMLButtonElement>('[aria-label="HTTP details"]')!
  const explanation = container.querySelector<HTMLElement>(`[id="${toggle.getAttribute('aria-controls')}"]`)!
  expect(toggle.getAttribute('aria-expanded')).toBe('false')
  expect(explanation.hidden).toBe(true)
  const edit = [...container.querySelectorAll('button')].find(b => b.textContent === 'Edit HTTP')!
  expect(edit.closest('[hidden]')).toBeNull()
  await act(async () => toggle.click())
  expect(explanation.hidden).toBe(false)
  await act(async () => edit.click())
  const input = container.querySelector('[aria-label="HTTP: Request timeout (ms)"]')!
  await act(async () => toggle.click())
  expect(explanation.hidden).toBe(true)
  expect(input.closest('[hidden]')).toBeNull()
 } finally { await act(async () => root.unmount()) }
})
it('shows only the controls relevant to the current pacing and think-time modes', () => {
 const draft = script()
 draft.config.pacing = { type: 'random_delay', min_delay_ms: 0, max_delay_ms: 500 }
 draft.config.think_time = { type: 'uniform', min_ms: 0, max_ms: 250 }
 const paths = runtimeSettingRows(draft)['Additional APEX settings'].map(row => row.path)
 expect(paths).toContain('config.pacing.min_delay_ms')
 expect(paths).toContain('config.think_time.min_ms')
 expect(paths).not.toContain('config.pacing.interval_ms')
 expect(paths).not.toContain('config.think_time.std_ms')
})
it('retains invalid category edits, applies one atomic request, and supports cancel without mutation', async () => {
 const container = document.createElement('div'); document.body.append(container); const root = createRoot(container)
 const onApplied = vi.fn(); const onBusyChange = vi.fn(); const draft = script()
 const button = (name: string) => [...container.querySelectorAll('button')].find(b => b.textContent === name)!
 const inputValue = (label: string, value: string) => { const input = container.querySelector<HTMLInputElement>(`[aria-label="${label}"]`)!; Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })) }
 try {
  await act(async () => root.render(<ImportSettingsProvider script={draft} disabled={false} onApplied={onApplied} onBusyChange={onBusyChange}><ImportRuntimeSettingsTable script={draft} /></ImportSettingsProvider>))
  await act(async () => button('Edit HTTP').click())
  await act(async () => inputValue('HTTP: Request timeout (ms)', '2000'))
  mocks.request.mockRejectedValueOnce(new ApiError('Invalid setting', 422, { field_errors: { 'config.http.request_timeout_ms': 'Timeout is invalid' } }))
  await act(async () => button('Apply HTTP').click())
  expect(container.textContent).toContain('Timeout is invalid')
  expect(container.querySelector<HTMLInputElement>('[aria-label="HTTP: Request timeout (ms)"]')?.value).toBe('2000')
  expect(onApplied).not.toHaveBeenCalled()
  mocks.request.mockResolvedValueOnce({ script: { ...draft, config: { http: { request_timeout_ms: 2000 } } }, findings: [], readiness: {}, secret_binding_requirements: [] })
  await act(async () => button('Apply HTTP').click())
  expect(onApplied).toHaveBeenCalledOnce()
  expect(JSON.parse(mocks.request.mock.calls[mocks.request.mock.calls.length - 1][1].body).changes).toEqual([{ path: 'config.http.request_timeout_ms', value: 2000 }])
  expect(onBusyChange).toHaveBeenLastCalledWith(false)
  await act(async () => button('Edit HTTP').click())
  await act(async () => button('Cancel').click())
  expect(mocks.request).toHaveBeenCalledTimes(2)
 } finally { await act(async () => root.unmount()); container.remove(); mocks.request.mockReset() }
})
it('consolidates source mappings, shows safe quoted CSV samples, and keeps masked list values read-only', async () => {
 const draft = script()
 draft.parameters = [{ name: 'region', type: 'file', data_source: 'users', column: 'region' }, { name: 'password', type: 'list', values: ['[REDACTED]'] }]
 draft.data_sources = [{ name: 'users', type: 'csv', strategy: 'sequential', delimiter: 'comma', first_row_header: true, inline_data: 'region,code\n"west,\ncoast",1\n"east",2\n' }]
 draft.metadata = { import: { preserved: { parameter_file_sections: { 'users.prm': { region: { Table: 'users.csv' }, orphan: { Table: 'missing.csv' } } } } } }
 const container = document.createElement('div'); const root = createRoot(container)
 try {
  await act(async () => root.render(<ImportSettingsProvider script={draft} disabled={false} onApplied={vi.fn()} onBusyChange={vi.fn()}><ImportParameterInventory script={draft} /></ImportSettingsProvider>))
  expect(container.textContent).toContain('Sample: west,\ncoast, east')
  await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="Edit region"]')!.click())
  expect(container.textContent).toContain('2 available rows')
  expect(container.textContent).toContain('Unmapped parameter source sections')
  expect(container.querySelector('.import-row-details')?.textContent?.match(/users.prm · region/g)).toHaveLength(1)
  await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="Edit password"]')!.click())
  expect(container.querySelector('[aria-label="password: values"]')).toBeNull()
  expect(container.textContent).toContain('Protected — manage in Credentials')
 } finally { await act(async () => root.unmount()) }
})

function SettingsProbe() {
 const context = useImportSettings()!
 return <>
  <button onClick={() => { context.begin('HTTP'); context.change('HTTP', { path: 'config.http.request_timeout_ms', value: 42 }) }}>Stage HTTP</button>
  <button onClick={() => context.begin('Pacing')}>Stage pacing</button>
  <button onClick={() => void context.apply('HTTP')}>Submit HTTP</button>
  <output>{Object.keys(context.editors).join('|')}:{String(context.busy)}</output>
 </>
}
it.each(['replacement', 'unmount'])('ignores a settings response after draft %s', async mode => {
 const container = document.createElement('div'); const root = createRoot(container)
 const onApplied = vi.fn(); const notify = vi.fn()
 let resolve!: (result: SettingsResult) => void
 mocks.request.mockImplementationOnce(() => new Promise<SettingsResult>(done => { resolve = done }))
 const render = (draft: ScriptDefinition) => root.render(<ImportSettingsProvider script={draft} disabled={false} onApplied={onApplied} onBusyChange={notify}><SettingsProbe /></ImportSettingsProvider>)
 try {
  await act(async () => render(script()))
  await act(async () => container.querySelectorAll('button')[0].click())
  await act(async () => [...container.querySelectorAll('button')].find(b => b.textContent === 'Submit HTTP')!.click())
  if (mode === 'replacement') {
   await act(async () => render(script()))
   expect(container.querySelector('output')?.textContent).toBe(':false')
  } else await act(async () => root.unmount())
  await act(async () => resolve({ script: script() } as SettingsResult))
  expect(onApplied).not.toHaveBeenCalled()
  expect(notify).toHaveBeenLastCalledWith(false)
 } finally { if (mode !== 'unmount') await act(async () => root.unmount()); mocks.request.mockReset() }
})
it('retains other categories after its own successful draft rotation', async () => {
 const container = document.createElement('div'); const root = createRoot(container)
 const result = { script: script() } as SettingsResult
 mocks.request.mockResolvedValueOnce(result)
 const render = (draft: ScriptDefinition) => root.render(<ImportSettingsProvider script={draft} disabled={false} onApplied={updated => render({ ...updated.script, metadata: {} })} onBusyChange={vi.fn()}><SettingsProbe /></ImportSettingsProvider>)
 const click = async (name: string) => act(async () => [...container.querySelectorAll('button')].find(b => b.textContent === name)!.click())
 try {
  await act(async () => render(script()))
  await click('Stage HTTP'); await click('Stage pacing'); await click('Submit HTTP')
  expect(container.querySelector('output')?.textContent).toBe('Pacing:false')
 } finally { await act(async () => root.unmount()); mocks.request.mockReset() }
})
it('updates file samples and row counts with pending source and column mappings', async () => {
 const draft = script()
 draft.parameters = [{ name: 'region', type: 'file', data_source: 'old', column: 'region' }]
 draft.data_sources = [
  { name: 'old', type: 'csv', strategy: 'sequential', first_row_header: true, inline_data: 'region\nwest' },
  { name: 'new', type: 'csv', strategy: 'sequential', first_row_header: true, inline_data: 'city\nBoston\nAustin' },
  { name: 'payload', type: 'file', strategy: 'sequential', inline_data: '{"region":"not a table"}' },
 ]
 const container = document.createElement('div'); const root = createRoot(container)
 try {
  await act(async () => root.render(<ImportSettingsProvider script={draft} disabled={false} onApplied={vi.fn()} onBusyChange={vi.fn()}><ImportParameterInventory script={draft} /></ImportSettingsProvider>))
  await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="Edit region"]')!.click())
  const change = async (label: string, value: string) => act(async () => {
   const select = container.querySelector<HTMLSelectElement>(`[aria-label="${label}"]`)!
   select.value = value; select.dispatchEvent(new Event('change', { bubbles: true }))
  })
  await change('region: data source', 'new'); await change('region: column', 'city')
  expect(container.textContent).toContain('Sample: Boston, Austin')
  expect(container.textContent).toContain('2 available rows')
  await act(async () => [...container.querySelectorAll('button')].find(b => b.textContent === 'Data sources (3)')!.click())
  const payload = [...container.querySelectorAll('tr')].find(row => row.textContent?.startsWith('payload'))!
  expect(payload.textContent).toContain('Unavailable')
  expect(payload.textContent).not.toContain('not a table')
 } finally { await act(async () => root.unmount()) }
})

it('retains evidence when multiple source concepts share a destination setting', () => {
 const draft = script()
 draft.metadata = { import: { preserved: {
  runtime_files: { 'default.cfg': { HTTP: { ReceiveTimeout: '3', StepDownloadTimeout: '5' } } },
  runtime_setting_dispositions: { 'default.cfg': { HTTP: {
   ReceiveTimeout: { status: 'mapped', parity_id: 'receive', fields: ['config.http.request_timeout_ms'] },
   StepDownloadTimeout: { status: 'mapped', parity_id: 'download', fields: ['config.http.request_timeout_ms'] },
  } } },
  runtime_settings_parity: ['receive', 'download'].map(id => ({ id, area: 'HTTP', concept: id, status: 'executed', fields: ['config.http.request_timeout_ms'], notes: `${id} evidence` })),
 } } }
 const rows = runtimeSettingRows(draft)['Mapped for replay']
 expect(rows).toHaveLength(1)
 expect(rows[0].imported).toContain('ReceiveTimeout: 3')
 expect(rows[0].imported).toContain('StepDownloadTimeout: 5')
 expect(rows[0].note).toContain('download evidence')
})

it('matches grouped and multi-destination source evidence without inventing absent settings', () => {
 const draft = script()
 draft.config.think_time = { type: 'random_percentage', min_percent: 50, max_percent: 150 }
 draft.metadata = { import: { preserved: {
  runtime_files: { 'default.cfg': { ThinkTime: { Options: 'RANDOM', ThinkTimeRandomLow: '50', ThinkTimeRandomHigh: '150' }, WEB: { SimulateCache: '1' }, Log: { AutoLog: '0' } } },
  runtime_setting_dispositions: { 'default.cfg': {
   ThinkTime: Object.fromEntries(['Options', 'ThinkTimeRandomLow', 'ThinkTimeRandomHigh'].map(key => [key, { status: 'mapped', parity_id: 'think_time', fields: ['config.think_time'] }])),
   WEB: { SimulateCache: { status: 'mapped', parity_id: 'cache', fields: ['config.browser_emulation.cache.simulate_cache', 'config.browser.cache_enabled'] } },
   Log: { AutoLog: { status: 'mapped', parity_id: 'logging', fields: ['config.log'] } },
  } },
  runtime_settings_parity: [
   { id: 'think_time', area: 'Think Time', concept: 'Think time', status: 'executed', fields: ['config.think_time'] },
   { id: 'cache', area: 'Cache', concept: 'Cache', status: 'executed', fields: ['config.browser_emulation.cache.simulate_cache', 'config.browser.cache_enabled'] },
   { id: 'logging', area: 'Logging', concept: 'Log', status: 'executed', fields: ['config.log'] },
  ],
 } } }
 const groups = runtimeSettingRows(draft)
 const mapped = groups['Mapped for replay']
 expect(mapped.find(r => r.path === 'config.think_time.min_percent')?.imported).toContain('ThinkTimeRandomLow: 50')
 expect(mapped.find(r => r.path === 'config.think_time.max_percent')?.imported).toContain('ThinkTimeRandomHigh: 150')
 expect(mapped.find(r => r.path === 'config.log.on_error_only')?.imported).toContain('AutoLog: 0')
 expect(mapped.filter(r => r.path?.includes('cache')).every(r => r.imported.includes('SimulateCache: 1'))).toBe(true)
 expect(mapped.some(r => r.path === 'config.think_time.mean_ms')).toBe(false)
 expect(JSON.stringify(groups)).not.toContain('Source value unavailable')
 expect(mapped.filter(r => !r.path)).toHaveLength(0)
})

it('keeps preserved values under a partial mapping in Requires decision', () => {
 const draft = script()
 draft.metadata = { import: { preserved: {
  runtime_files: { 'default.cfg': { ThinkTime: { ThinkTimeRandomLow: 'bad' } } },
  runtime_setting_dispositions: { 'default.cfg': { ThinkTime: { ThinkTimeRandomLow: { status: 'mapped', parity_id: 'think_time', fields: ['config.think_time'] } } } },
  runtime_settings_parity: [{ id: 'think_time', area: 'Think Time', concept: 'Think time', status: 'partial', fields: ['config.think_time'] }],
 } } }
 expect(runtimeSettingRows(draft)['Requires decision'].some(r => r.imported.includes('bad'))).toBe(true)
})

it('initializes percentage controls and clears obsolete milliseconds when changing mode', async () => {
 const draft = script(); draft.config.think_time = { type: 'fixed', mean_ms: 4000 }
 const container = document.createElement('div'); const root = createRoot(container)
 try {
  await act(async () => root.render(<ImportSettingsProvider script={draft} disabled={false} onApplied={vi.fn()} onBusyChange={vi.fn()}><ImportRuntimeSettingsTable script={draft} /></ImportSettingsProvider>))
  await act(async () => [...container.querySelectorAll<HTMLButtonElement>('.import-category-nav button')].find(b => b.textContent?.startsWith('Think time'))!.click())
  await act(async () => [...container.querySelectorAll('button')].find(b => b.textContent === 'Edit Think time')!.click())
  await act(async () => {
   const select = container.querySelector<HTMLSelectElement>('[aria-label="Think time: Mode"]')!
   select.value = 'random_percentage'; select.dispatchEvent(new Event('change', { bubbles: true }))
  })
  expect(container.querySelector<HTMLInputElement>('[aria-label="Think time: Minimum recorded delay (%)"]')?.value).toBe('50')
  expect(container.querySelector<HTMLInputElement>('[aria-label="Think time: Maximum recorded delay (%)"]')?.value).toBe('150')
  expect(container.querySelector('[aria-label="Think time: Mean delay (ms)"]')).toBeNull()
  mocks.request.mockResolvedValueOnce({ script: draft })
  await act(async () => [...container.querySelectorAll('button')].find(b => b.textContent === 'Apply Think time')!.click())
  expect(JSON.parse(mocks.request.mock.calls[0][1].body).changes).toEqual(expect.arrayContaining([
   { path: 'config.think_time.min_percent', value: 50 }, { path: 'config.think_time.max_percent', value: 150 }, { path: 'config.think_time.mean_ms', value: null },
  ]))
 } finally { await act(async () => root.unmount()); mocks.request.mockReset() }
})

it('includes source-only categories in navigation and filters every status consistently', () => {
 const draft = script()
 draft.metadata = { import: { preserved: {
  runtime_files: { 'default.cfg': { Legacy: { Setting: 'original', Retained: 'legacy-value' }, HTTP: { Other: 'http-value' } } },
  runtime_setting_dispositions: { 'default.cfg': { Legacy: { Setting: { status: 'non_applicable', note: 'Does not apply to this protocol.' }, Retained: { status: 'compatibility_only' } }, HTTP: { Other: { status: 'compatibility_only' } } } },
 } } }
 const container = document.createElement('div'); const root = createRoot(container)
 try {
  act(() => root.render(<ImportRuntimeSettingsTable script={draft} />))
  expect(container.querySelector('section[aria-label="Not applicable"]')).toBeNull()
  const summary = container.querySelector('.import-runtime-summary')!
  const overall = summary.textContent
  expect(overall).toContain('Preserved only2')
  expect(overall).toContain('Not applicable1')
  const legacy = [...container.querySelectorAll<HTMLButtonElement>('.import-category-nav button')].find(button => button.textContent === 'Legacy2')!
  expect(legacy).toBeDefined()
  act(() => legacy.click())
  const inactive = container.querySelector('section[aria-label="Not applicable"]')!
  expect(inactive.querySelector('h3')?.textContent).toBe('Not applicable (1)')
  expect(inactive.querySelector('table')?.textContent).toContain('original (default.cfg)')
  expect(inactive.querySelector('table')?.textContent).toContain('Does not apply to this protocol.')
  expect(container.querySelector('section[aria-label="Preserved only"]')?.textContent).toContain('legacy-value')
  expect(container.querySelector('section[aria-label="Preserved only"]')?.textContent).not.toContain('http-value')
  act(() => {
   for (const toggle of container.querySelectorAll<HTMLInputElement>('.import-runtime-summary input')) if (toggle.getAttribute('aria-label') !== 'Preserved only') toggle.click()
   ;[...container.querySelectorAll<HTMLButtonElement>('.import-category-nav button')].find(button => button.textContent?.startsWith('All categories'))!.click()
  })
  expect(container.querySelectorAll('.import-runtime-content > section')).toHaveLength(1)
  const navCounts = () => [...container.querySelectorAll('.import-category-nav button')].map(button => button.textContent)
  expect(navCounts()).toEqual(expect.arrayContaining(['All categories2', 'HTTP1', 'Legacy1']))
  expect(summary.textContent).toBe(overall)
  act(() => container.querySelector<HTMLInputElement>('.import-runtime-summary input[aria-label="Not applicable"]')!.click())
  expect(navCounts()).toEqual(expect.arrayContaining(['All categories3', 'HTTP1', 'Legacy2']))
  act(() => container.querySelector<HTMLInputElement>('.import-runtime-summary input[aria-label="Not applicable"]')!.click())
  expect(container.querySelector('section[aria-label="Preserved only"]')?.textContent).toContain('http-value')
  expect(container.querySelector('section[aria-label="Preserved only"]')?.textContent).toContain('legacy-value')
  const search = container.querySelector<HTMLInputElement>('input[type="search"]')!
  act(() => {
   Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(search, 'legacy-value')
   search.dispatchEvent(new Event('input', { bubbles: true }))
  })
  expect(container.querySelector('section[aria-label="Preserved only"]')?.textContent).toContain('(1)')
  expect(container.querySelector('section[aria-label="Preserved only"]')?.textContent).not.toContain('http-value')
  expect(summary.textContent).toBe(overall)
  act(() => {
   Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(search, 'no-such-setting')
   search.dispatchEvent(new Event('input', { bubbles: true }))
  })
  expect(container.querySelectorAll('.import-runtime-content > section')).toHaveLength(0)
  expect(container.querySelector('.import-review-empty[role="status"]')?.textContent).toContain('No settings match these filters')
  expect(summary.textContent).toBe(overall)
 } finally { act(() => root.unmount()) }
})

it('combines the run-logic family without losing section evidence or duplicate setting names', () => {
 const draft = script()
 draft.config.run_logic = { iterations: 1 }
 const sections = ['RunLogicEndRoot', 'RunLogicEndRoot:vuser_end', 'RunLogicInitRoot:vuser_init', 'RunLogicErrorHandlerRoot', 'RunLogicRunRoot:Action', 'Profile Actions']
 draft.metadata = { import: { preserved: { runtime_files: { 'default.usp': Object.fromEntries(sections.map(section => [section, { NumberOfEntries: '1' }])) } } } }
 const container = document.createElement('div'); const root = createRoot(container)
 try {
  act(() => root.render(<ImportRuntimeSettingsTable script={draft} />))
  const categories = [...container.querySelectorAll<HTMLButtonElement>('.import-category-nav button')]
  expect(categories.some(button => sections.some(section => button.textContent?.startsWith(section)))).toBe(false)
  const runLogic = categories.filter(button => button.textContent?.startsWith('Run logic'))
  expect(runLogic).toHaveLength(1)
  act(() => runLogic[0].click())
  expect(container.querySelector('section[aria-label="Additional APEX settings"]')?.textContent).toContain('Iterations per VUser')
  expect(container.querySelectorAll('[aria-label="Preserved only"] tbody > tr:not(.import-category)')).toHaveLength(sections.length)
  expect(container.querySelectorAll('[aria-label="Preserved only"] .import-category')).toHaveLength(1)
  for (const section of sections) expect(container.querySelector('section[aria-label="Preserved only"]')?.textContent).toContain(`Source section: ${section}.`)
  const search = container.querySelector<HTMLInputElement>('input[type="search"]')!
  act(() => {
   Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(search, 'RunLogicErrorHandlerRoot')
   search.dispatchEvent(new Event('input', { bubbles: true }))
  })
  expect(container.querySelectorAll('[aria-label="Preserved only"] tbody > tr:not(.import-category)')).toHaveLength(1)
  expect(container.querySelector('section[aria-label="Preserved only"]')?.textContent).toContain('RunLogicErrorHandlerRoot')
 } finally { act(() => root.unmount()) }
})

it('filters runtime categories without losing pending edits and searches immutable evidence across categories', async () => {
 const draft = script()
 draft.config.think_time = { type: 'fixed', mean_ms: 0 }
 const container = document.createElement('div'); const root = createRoot(container)
 const click = async (label: string) => act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === label)!.click())
 try {
  await act(async () => root.render(<ImportSettingsProvider script={draft} disabled={false} onApplied={vi.fn()} onBusyChange={vi.fn()}><ImportRuntimeSettingsTable script={draft} /></ImportSettingsProvider>))
  expect(container.textContent).not.toContain('Mean delay (ms)')
  await click('Edit HTTP')
  await act(async () => {
   const input = container.querySelector<HTMLInputElement>('[aria-label="HTTP: Request timeout (ms)"]')!
   Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, '8750')
   input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  await act(async () => [...container.querySelectorAll<HTMLButtonElement>('.import-category-nav button')].find(button => button.textContent?.startsWith('Think time'))!.click())
  expect(container.querySelector('[aria-label="HTTP: Request timeout (ms)"]')).toBeNull()
  expect(container.textContent).toContain('Apply HTTP')
  await act(async () => {
   const input = container.querySelector<HTMLInputElement>('input[type="search"]')!
   Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'Request timeout')
   input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  expect(container.querySelector<HTMLInputElement>('[aria-label="HTTP: Request timeout (ms)"]')?.value).toBe('8750')
  await click('Cancel')
  expect(container.textContent).toContain('3000')
 } finally { await act(async () => root.unmount()) }
})

it('keeps only one parameter editor open and retains edits across filters and data views', async () => {
 const draft = script()
 draft.parameters!.push({ name: 'market', type: 'list', values: ['east'] })
 const container = document.createElement('div'); document.body.append(container); const root = createRoot(container)
 const click = async (label: string) => act(async () => [...container.querySelectorAll('button')].find(button => button.getAttribute('aria-label') === label || button.textContent === label)!.click())
 try {
  await act(async () => root.render(<ImportSettingsProvider script={draft} disabled={false} onApplied={vi.fn()} onBusyChange={vi.fn()}><ImportParameterInventory script={draft} /></ImportSettingsProvider>))
  expect(container.querySelectorAll('textarea')).toHaveLength(0)
  await click('Edit region')
  await act(async () => {
   const input = container.querySelector<HTMLTextAreaElement>('[aria-label="region: values"]')!
   Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(input, 'north\nsouth')
   input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  await click('Edit market')
  expect(container.querySelectorAll('textarea')).toHaveLength(1)
  expect(container.textContent).toContain('north, south')
  await click('Data sources (0)')
  expect(container.querySelectorAll('.import-parameters-table')).toHaveLength(0)
  expect(container.textContent).toContain('Apply list')
  await click('Allocation preview')
  expect(container.textContent).toContain('Row 1')
  await click('Parameters (2)')
  await click('Edit region')
  expect(container.querySelector<HTMLTextAreaElement>('[aria-label="region: values"]')?.value).toBe('north\nsouth')
  mocks.request.mockRejectedValueOnce(new ApiError('Invalid values', 422, { field_errors: { 'region.values': 'Choose a value' } }))
  await click('Apply list')
  await click('Edit market')
  await click('Review list edits')
  expect(container.textContent).toContain('Choose a value')
  expect(container.querySelector<HTMLTextAreaElement>('[aria-label="region: values"]')?.value).toBe('north\nsouth')
  await click('Cancel')
  expect(container.querySelectorAll('textarea')).toHaveLength(0)
  expect(container.textContent).not.toContain('north, south')
 } finally { await act(async () => root.unmount()); container.remove(); mocks.request.mockReset() }
})

it('normalizes known timeout units while preserving exact source evidence', () => {
 const draft = script()
 draft.metadata = { import: { preserved: {
  runtime_files: { 'default.cfg': { HTTP: { ReceiveTimeout: '3' } } },
  runtime_setting_dispositions: { 'default.cfg': { HTTP: { ReceiveTimeout: { status: 'mapped', parity_id: 'transport', fields: ['config.http.request_timeout_ms'] } } } },
  runtime_settings_parity: [{ id: 'transport', area: 'HTTP', concept: 'Timeout', status: 'executed', fields: ['config.http.request_timeout_ms'] }],
 } } }
 const row = runtimeSettingRows(draft)['Mapped for replay'][0]
 expect(row.importedSummary).toBe('3000 ms')
 expect(row.imported).toBe('ReceiveTimeout: 3 (default.cfg / HTTP)')
})

it('keeps complete ordinary lists inspectable without exposing credential values', async () => {
 const draft = script()
 draft.parameters = [{ name: 'regions', type: 'list', values: ['west', 'east', 'north'] }, { name: 'password', type: 'list', values: ['private-one', 'private-two', 'private-three'] }]
 const container = document.createElement('div'); const root = createRoot(container)
 try {
  await act(async () => root.render(<ImportParameterInventory script={draft} />))
  const details = [...container.querySelectorAll('details')].filter(element => element.querySelector('summary')?.textContent === 'All values')
  expect(details).toHaveLength(1)
  expect(details[0].textContent).toContain('west\neast\nnorth')
  expect(container.textContent).not.toContain('private-one')
  expect(container.textContent).toContain('Protected — manage in Credentials')
 } finally { await act(async () => root.unmount()) }
})

it.each([
 { header: false, columns: undefined, data: '\nwest,1\neast,2\n', column: 'col_1', sample: 'west, east' },
 { header: undefined, columns: [' market ', 'id'], data: 'west,1\neast,2\n', column: 'market', sample: 'west, east' },
 { header: true, columns: ['incorrect', 'id'], data: ' market ,id\nwest,1\neast,2\n', column: 'market', sample: 'west, east' },
 { header: false, columns: undefined, data: '""\neast\n', column: 'col_1', sample: '(empty), east' },
])('matches replay CSV column and header semantics: $column / $header', async ({ header, columns, data, column, sample }) => {
 const draft = script()
 draft.parameters = [{ name: 'region', type: 'file', data_source: 'input', column }]
 draft.data_sources = [{ name: 'input', type: 'csv', strategy: 'sequential', first_row_header: header, columns, inline_data: data }]
 const container = document.createElement('div'); const root = createRoot(container)
 try {
  await act(async () => root.render(<ImportSettingsProvider script={draft} disabled={false} onApplied={vi.fn()} onBusyChange={vi.fn()}><ImportParameterInventory script={draft} /></ImportSettingsProvider>))
  expect(container.textContent).toContain(`Sample: ${sample}`)
  expect(container.textContent).toContain('Definition issues (0)')
  await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="Edit region"]')!.click())
  expect(container.textContent).toContain('2 available rows')
  expect([...container.querySelectorAll('[aria-label="region: column"] option')].map(option => option.textContent)).toContain(column.replace(/_/g, ' '))
 } finally { await act(async () => root.unmount()) }
})

it('does not mark a valid remote mapping broken merely because columns are not loaded', () => {
 const draft = script()
 draft.parameters = [{ name: 'region', type: 'file', data_source: 'remote', column: 'region' }]
 draft.data_sources = [{ name: 'remote', type: 'csv', path: 'regions.csv', strategy: 'sequential', first_row_header: true }]
 const container = document.createElement('div'); const root = createRoot(container)
 try {
  act(() => root.render(<ImportParameterInventory script={draft} />))
  expect(container.textContent).toContain('Definition issues (0)')
 } finally { act(() => root.unmount()) }
})

it('protects API keys and token samples, editors, and evidence using the shared credential policy', async () => {
 const draft = script()
 draft.parameters = [{ name: 'api_key', type: 'list', values: ['never-render-list'] }, { name: 'session_token', type: 'file', data_source: 'input', column: 'token' }, { name: 'secretary', type: 'list', values: ['ordinary-value'] }]
 draft.data_sources = [{ name: 'input', type: 'csv', first_row_header: true, strategy: 'sequential', inline_data: 'token\nnever-render-file' }]
 draft.metadata = { import: { preserved: { parameter_file_sections: { 'input.prm': { api_key: { Value: 'never-render-mapped-evidence' }, refresh_token: { Value: 'never-render-unmapped-evidence' } } }, runtime_files: { 'default.cfg': { HTTP: { APIKey: 'never-render-runtime' } } } } } }
 const container = document.createElement('div'); const root = createRoot(container)
 try {
  await act(async () => root.render(<ImportSettingsProvider script={draft} disabled={false} onApplied={vi.fn()} onBusyChange={vi.fn()}><ImportParameterInventory script={draft} /><ImportRuntimeSettingsTable script={draft} /></ImportSettingsProvider>))
  expect(container.innerHTML).not.toContain('never-render')
  await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="Edit api_key"]')!.click())
  expect(container.querySelector('[aria-label="api_key: values"]')).toBeNull()
  await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="Edit session_token"]')!.click())
  expect(container.innerHTML).not.toContain('never-render')
  await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="Edit secretary"]')!.click())
  expect(container.querySelector<HTMLTextAreaElement>('[aria-label="secretary: values"]')?.value).toBe('ordinary-value')
 } finally { await act(async () => root.unmount()) }
})

it('preserves other parameter errors when one field is corrected', async () => {
 const draft = script(); draft.parameters!.push({ name: 'market', type: 'list', values: ['east'] })
 const container = document.createElement('div'); document.body.append(container); const root = createRoot(container)
 const click = async (label: string) => act(async () => [...container.querySelectorAll('button')].find(button => button.getAttribute('aria-label') === label || button.textContent === label)!.click())
 const change = async (label: string, value: string) => act(async () => { const input = container.querySelector<HTMLTextAreaElement>(`[aria-label="${label}"]`)!; Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })) })
 try {
  await act(async () => root.render(<ImportSettingsProvider script={draft} disabled={false} onApplied={vi.fn()} onBusyChange={vi.fn()}><ImportParameterInventory script={draft} /></ImportSettingsProvider>))
  await click('Edit region'); await change('region: values', 'invalid')
  await click('Edit market'); await change('market: values', 'invalid')
  mocks.request.mockRejectedValueOnce(new ApiError('Invalid', 422, { field_errors: { 'region.values': 'Invalid region', 'market.values': 'Invalid market' } }))
  await click('Apply list'); await click('Edit region'); await change('region: values', 'valid')
  expect(container.textContent).toContain('Definition issues (1)')
  await click('Review list edits')
  expect(container.textContent).toContain('Invalid market')
  expect(container.querySelector('[aria-label="market: values"]')).not.toBeNull()
 } finally { await act(async () => root.unmount()); container.remove(); mocks.request.mockReset() }
})

it('resets filters for a replacement draft while retaining them during its own apply rotation', async () => {
 const container = document.createElement('div'); document.body.append(container); const root = createRoot(container)
 const draft = script()
 const render = (current: ScriptDefinition) => root.render(<ImportSettingsProvider script={current} disabled={false} onApplied={result => render(result.script)} onBusyChange={vi.fn()}><ImportParameterInventory script={current} /><ImportRuntimeSettingsTable script={current} /></ImportSettingsProvider>)
 const fill = async (selector: string, value: string) => act(async () => { const input = container.querySelector<HTMLInputElement>(selector)!; Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })) })
 try {
  await act(async () => render(draft))
  await fill('.lr-parameter-inventory input[type="search"]', 'region')
  await fill('.import-runtime-workspace input[type="search"]', 'Request timeout')
  await act(async () => [...container.querySelectorAll('button')].find(b => b.textContent === 'Edit HTTP')!.click())
  await fill('[aria-label="HTTP: Request timeout (ms)"]', '4000')
  mocks.request.mockResolvedValueOnce({ script: { ...draft, config: { http: { request_timeout_ms: 4000 } } } })
  await act(async () => [...container.querySelectorAll('button')].find(b => b.textContent === 'Apply HTTP')!.click())
  expect(container.querySelector<HTMLInputElement>('.lr-parameter-inventory input[type="search"]')?.value).toBe('region')
  expect(container.querySelector<HTMLInputElement>('.import-runtime-workspace input[type="search"]')?.value).toBe('Request timeout')
  const replacement = script(); replacement.parameters = [{ name: 'new-parameter', type: 'list', values: ['new-value'] }]
  await act(async () => render(replacement))
  expect(container.querySelector<HTMLInputElement>('.lr-parameter-inventory input[type="search"]')?.value).toBe('')
  expect(container.querySelector<HTMLInputElement>('.import-runtime-workspace input[type="search"]')?.value).toBe('')
  expect(container.textContent).toContain('new-parameter')
 } finally { await act(async () => root.unmount()); container.remove(); mocks.request.mockReset() }
})

it('shows edit actions only for categories in the selected runtime status', async () => {
 const container = document.createElement('div'); const root = createRoot(container); const draft = script()
 try {
  await act(async () => root.render(<ImportSettingsProvider script={draft} disabled={false} onApplied={vi.fn()} onBusyChange={vi.fn()}><ImportRuntimeSettingsTable script={draft} /></ImportSettingsProvider>))
  await act(async () => container.querySelector<HTMLInputElement>('.import-runtime-summary input')!.click())
  expect([...container.querySelectorAll('button')].some(button => button.textContent === 'Edit HTTP')).toBe(false)
  expect(container.querySelectorAll('.import-runtime-content > section')).toHaveLength(0)
  expect(container.querySelector('.import-review-empty[role="status"]')?.textContent).toContain('Enable a mapping status')
  for (const count of container.querySelectorAll('.import-category-nav small')) expect(count.textContent).toBe('0')
  await act(async () => container.querySelector<HTMLInputElement>('.import-runtime-summary input')!.click())
  const edit = [...container.querySelectorAll('button')].find(button => button.textContent === 'Edit HTTP')!
  expect(edit).toBeDefined()
  await act(async () => edit.click())
  await act(async () => container.querySelector<HTMLInputElement>('.import-runtime-summary input')!.click())
  const review = [...container.querySelectorAll('button')].find(button => button.textContent === 'Review HTTP edits')!
  expect(review).toBeDefined()
  await act(async () => review.click())
  expect(container.querySelector<HTMLInputElement>('.import-runtime-summary input')?.checked).toBe(true)
  expect(container.querySelector('[aria-label="HTTP: Request timeout (ms)"]')).not.toBeNull()
 } finally { await act(async () => root.unmount()) }
})

it('keeps allocation controls available when changing an auto-allocated parameter away from unique', async () => {
 const draft = script(); draft.parameters![0] = { ...draft.parameters![0], selection: 'unique', auto_allocate: true }
 const container = document.createElement('div'); const root = createRoot(container)
 try {
  await act(async () => root.render(<ImportSettingsProvider script={draft} disabled={false} onApplied={vi.fn()} onBusyChange={vi.fn()}><ImportParameterInventory script={draft} /></ImportSettingsProvider>))
  await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="Edit region"]')!.click())
  await act(async () => { const select = container.querySelector<HTMLSelectElement>('[aria-label="region: selection"]')!; select.value = 'random'; select.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(container.textContent).toContain('Definition issues (1)')
  expect(container.querySelector('[aria-label="region: auto allocate"]')).not.toBeNull()
  await act(async () => { const select = container.querySelector<HTMLSelectElement>('[aria-label="region: auto allocate"]')!; select.value = 'false'; select.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(container.textContent).toContain('Definition issues (0)')
 } finally { await act(async () => root.unmount()) }
})

it('exposes replay policies once, with explicit false values and source evidence', () => {
 const draft = script()
 draft.config.http = { ...draft.config.http, legacy_html_checks: false, dns_cache: false }
 draft.metadata = { import: { preserved: {
  runtime_files: { 'default.cfg': { WEB: { EnableChecks: '0' } } },
  runtime_setting_dispositions: { 'default.cfg': { WEB: { EnableChecks: { status: 'mapped', parity_id: 'http_legacy_checks', fields: ['config.http.legacy_html_checks'] } } } },
  runtime_settings_parity: [{ id: 'http_legacy_checks', area: 'HTTP', concept: 'Legacy HTML checks', status: 'executed', fields: ['config.http.legacy_html_checks'] }],
 } } }
 const groups = runtimeSettingRows(draft)
 const rows = Object.values(groups).flat().filter(row => row.path === 'config.http.legacy_html_checks')
 expect(rows).toHaveLength(1)
 expect(rows[0].importedSummary).toBe('Disabled')
 expect(rows[0].field?.type).toBe('boolean')
 expect(groups['Additional APEX settings'].some(row => row.path === 'config.http.dns_cache')).toBe(true)
})

it('routes unresolved TLS migration to guided repair and re-enables settings after resolution', async () => {
 const draft = script(); draft.config.http = { ...draft.config.http, tls: { min_version: '1.2', max_version: '1.3' } }
 const container = document.createElement('div'); const root = createRoot(container); const onReview = vi.fn()
 const render = (migration?: () => void) => root.render(<ImportSettingsProvider script={draft} disabled={false} onApplied={vi.fn()} onBusyChange={vi.fn()}><ImportRuntimeSettingsTable script={draft} onReviewTLSMigration={migration} /></ImportSettingsProvider>)
 const click = async (name: string) => act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === name)!.click())
 try {
  await act(async () => render(onReview))
  await click('Edit HTTP')
  expect(container.querySelector('[aria-label="HTTP: Minimum TLS version"]')).toBeNull()
  await click('Review TLS migration')
  expect(onReview).toHaveBeenCalledOnce()
  const count = container.querySelector<HTMLInputElement>('.import-runtime-summary input')!
  const description = container.querySelector(`[id="${count.getAttribute('aria-describedby')}"]`)
  expect(description?.textContent).toContain('settings across all categories')
  await act(async () => render())
  expect(container.querySelector('[aria-label="HTTP: Minimum TLS version"]')).not.toBeNull()
  expect(container.textContent).not.toContain('Review TLS migration')
 } finally { await act(async () => root.unmount()) }
})
