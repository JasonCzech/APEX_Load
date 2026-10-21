// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { beforeEach, afterEach, expect, it, vi } from 'vitest'
import ArchivedNativePanel from './ArchivedNativePanel'
import { NativePanelAuthoring } from './NativeAnalyticsPanel'
import type { DashboardDefinitionPanel, DashboardDefinition } from '../hooks/api/useDashboardDefinitionsApi'
const request = vi.hoisted(() => vi.fn())
const exportProps = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request }))
vi.mock('./ExportableChart', () => ({ default: (props: { children: React.ReactNode }) => { exportProps(props); return <div>{props.children}</div> } }))
let container: HTMLElement; let root: Root
const panel: DashboardDefinitionPanel = { id: 'p', title: 'Documents', kind: 'table', x: 0, y: 0, w: 24, h: 8, query: { query_version: 3, browser_tuple_mode: 'separate', metric: 'document_lcp', aggregation: 'p75', population: 'browser_document', end_seconds: 20, bucket_seconds: 10, max_series: 20 } }
const tuple = (id: string, value: number | null) => ({ id, label: `Cohort ${id}`, cohort_digest: `digest-${id}`, measurement: { metric: 'lcp', unit: 'ms', algorithm_sha256: 'algorithm' }, buckets: [{ from: '2026-10-01T00:00:00Z', to: '2026-10-01T00:00:10Z', offset_seconds: 0, count: value == null ? 0 : 3, value }] })
const response = { current: { run_id: 'run', warnings: [], identities: [], buckets: [], browser_tuples: { version: 1, tuples: [tuple('a', 0), tuple('b', 900)] }, excluded: { provisional: 2 } }, baseline: { run_id: 'base', warnings: [], browser_tuples: { version: 1, tuples: [tuple('b', 200), tuple('a', null), tuple('c', 500)] } }, browser_tuple_comparisons: [{ tuple_id: 'a', status: 'unavailable', reason: 'incomplete_evidence' }, { tuple_id: 'b', status: 'comparable' }], warnings: [], added: [], removed: [], comparable: false }
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); request.mockReset(); exportProps.mockReset() })
afterEach(() => { act(() => root.unmount()); container.remove() })
it('joins only exact tuples, preserves measured zero, missing populations and export provenance', async () => {
 request.mockResolvedValue(response)
 await act(async () => root.render(<ArchivedNativePanel panel={panel} testId="run" />))
 expect(container.querySelectorAll('table')).toHaveLength(3)
 expect(container.textContent).toContain('Excluded current measurements: provisional (2)')
 expect(container.textContent).toContain('Baseline-only tuple: Cohort c')
 expect(container.textContent).toContain('Unavailable (incomplete evidence)')
 expect(container.textContent).toContain('not a baseline anomaly verdict')
 const cells = [...container.querySelectorAll('tbody tr')].map(row => [...row.querySelectorAll('td')].map(cell => cell.textContent))
 expect(cells).toEqual([['0', '0', '3', 'Missing', '0'], ['0', '900', '3', '200', '3'], ['0', 'Missing', 'Missing', '500', '3']])
 expect(exportProps).toHaveBeenCalledTimes(3)
 const props = exportProps.mock.calls[1][0] as { rows: Record<string, unknown>[]; columns: { key: string; value: (row: Record<string, unknown>) => unknown }[] }
 const exported = Object.fromEntries(props.columns.map(column => [column.key, column.value(props.rows[0])]))
 expect(exported).toMatchObject({ tuple: 'b', cohort: 'digest-b', run: 'run', baselineRun: 'base', value: 900, baselineValue: 200, compatibility: 'comparable', count: 3 })
 expect(container.textContent).not.toContain('Inspect current bucket')
})
it('refuses a legacy response for a separate-tuple query rather than displaying pooled buckets', async () => {
 request.mockResolvedValue({ ...response, current: { ...response.current, browser_tuples: undefined } })
 await act(async () => root.render(<ArchivedNativePanel panel={panel} testId="run" />))
 expect(container.querySelector('[role=alert]')?.textContent).toContain('unavailable from this server')
 expect(exportProps).not.toHaveBeenCalled()
})
it('shows empty evidence explicitly', async () => {
 request.mockResolvedValue({ ...response, baseline: undefined, current: { ...response.current, browser_tuples: { version: 1, tuples: [] } } })
 await act(async () => root.render(<ArchivedNativePanel panel={panel} testId="run" />))
 expect(container.textContent).toContain('No final eligible browser measurements')
 expect(container.querySelectorAll('table')).toHaveLength(0)
})
it('authors versioned separate browser tuples without changing other populations', async () => {
 const save = vi.fn<(definition: Pick<DashboardDefinition, "name" | "tabs">) => Promise<void>>(async () => {})
 await act(async () => root.render(<NativePanelAuthoring onSave={save} />))
 const population = container.querySelector<HTMLSelectElement>('select[aria-label="Evidence population"]')!
 await act(async () => { population.value = 'browser_document'; population.dispatchEvent(new Event('change', { bubbles: true })) })
 const check = [...container.querySelectorAll('label')].find(label => label.textContent?.includes('Show separate browser cohorts'))!.querySelector('input')!
 await act(async () => check.click())
 await act(async () => { container.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) })
 expect(save.mock.calls[0][0]).toMatchObject({ tabs: [{ panels: [{ query: { query_version: 3, browser_tuple_mode: 'separate', max_series: 10, population: 'browser_document' } }] }] })
 await act(async () => { population.value = 'action'; population.dispatchEvent(new Event('change', { bubbles: true })) })
 await act(async () => { container.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) })
 expect(save.mock.calls[1][0].tabs[0].panels[0].query?.browser_tuple_mode).toBeUndefined()
})

it('exports baseline-only measurements while preserving missing current values and counts', async () => {
 request.mockResolvedValue({ ...response, current: { ...response.current, browser_tuples: { version: 1, tuples: [] } }, baseline: { ...response.baseline, browser_tuples: { version: 1, tuples: [tuple('baseline-only', 0)] } }, browser_tuple_comparisons: [{ tuple_id: 'baseline-only', status: 'unavailable', reason: 'missing_current_tuple' }] })
 await act(async () => root.render(<ArchivedNativePanel panel={panel} testId="run" />))
 expect(container.textContent).toContain('No final eligible browser measurements match the current selection')
 expect(container.textContent).toContain('Unavailable (missing current tuple)')
 expect([...container.querySelectorAll('tbody td')].map(cell => cell.textContent)).toEqual(['0','Missing','Missing','0','3'])
 expect(exportProps).toHaveBeenCalledTimes(1)
 const props = exportProps.mock.calls[0][0] as { rows: Record<string, unknown>[]; columns: { key: string; value: (row: Record<string, unknown>) => unknown }[] }
 const exported = Object.fromEntries(props.columns.map(column => [column.key, column.value(props.rows[0])]))
 expect(exported).toMatchObject({tuple:'baseline-only',value:'',count:'',baselineValue:0,baselineCount:3,baselineRun:'base',compatibility:'missing_current_tuple'})
})
