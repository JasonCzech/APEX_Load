// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { beforeEach, afterEach, expect, it, vi } from 'vitest'
import ArchivedNativePanel from './ArchivedNativePanel'
import type { DashboardDefinitionPanel } from '../hooks/api/useDashboardDefinitionsApi'
const request = vi.hoisted(() => vi.fn())
const exportProps = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request }))
vi.mock('./ExportableChart', () => ({ default: (props: { children: React.ReactNode }) => { exportProps(props); return <div>{props.children}</div> } }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLElement; let root: Root
const panel: DashboardDefinitionPanel = { id: 'p', title: 'Paid', kind: 'table', x: 0, y: 0, w: 24, h: 8, query: { metric: 'response_time', aggregation: 'p95', population: 'action', property_name: 'kind', property_value: 'paid', filters: { action: 'Checkout' }, end_seconds: 20, bucket_seconds: 10 } }
const response = { current: { run_id: 'run', identities: ['Checkout'], warnings: [], buckets: [{ from: '2026-10-01T00:00:00Z', to: '2026-10-01T00:00:10Z', offset_seconds: 0, count: 3, value: 30 }, { from: '2026-10-01T00:00:10Z', to: '2026-10-01T00:00:20Z', offset_seconds: 10, count: 0, value: null }] }, warnings: [], added: [], removed: [], comparable: false }
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); request.mockReset() })
afterEach(() => { act(() => root.unmount()); container.remove() })
it('keeps gaps and sends identical exact filters and half-open bounds to the inspector', async () => {
  request.mockImplementation(async (path: string) => path.endsWith('native-series') ? response : { transactions: [], total: 3 })
  await act(async () => root.render(<ArchivedNativePanel panel={panel} testId="run" />))
  expect(container.textContent).toContain('Missing')
  const button = [...container.querySelectorAll('button')].find(node => node.textContent?.startsWith('Inspect current bucket 0s'))!
  await act(async () => button.click())
  const call = request.mock.calls.find(([path]) => String(path).includes('/transactions?'))!
  const params = new URL(String(call[0]), 'http://local').searchParams
  expect(Object.fromEntries(params)).toMatchObject({ population: 'action', action_exact: 'Checkout', property_name: 'kind', property_value: 'paid', time_to_exclusive: 'true', time_to: '2026-10-01T00:00:10Z', offset: '0' })
  expect(container.textContent).toContain('3 matching observations')
})
it('exposes query limits rather than substituting totals', async () => {
  request.mockRejectedValue(new Error('raw observation limit exceeded'))
  await act(async () => root.render(<ArchivedNativePanel panel={panel} testId="run" />))
  expect(container.querySelector('[role=alert]')?.textContent).toContain('raw observation limit exceeded')
})
it('shows compatibility warnings and added/removed identities for comparison', async () => {
  request.mockResolvedValue({ ...response, baseline: response.current, warnings: ['Workload/version mismatch'], added: ['new'], removed: ['old'] })
  await act(async () => root.render(<ArchivedNativePanel panel={panel} testId="run" />))
  expect(container.textContent).toContain('Workload/version mismatch')
  expect(container.textContent).toContain('Added identities: new')
  expect(container.textContent).toContain('Removed identities: old')
})

it('authors only retained property categories and persists the selected category', async () => {
  const { NativePanelAuthoring } = await import('./NativeAnalyticsPanel')
  const save = vi.fn(async () => {})
  await act(async () => root.render(<NativePanelAuthoring properties={['kind=paid', 'kind=other']} onSave={save} />))
  const population = container.querySelector<HTMLSelectElement>('select[aria-label="Evidence population"]')!
  await act(async () => { population.value = 'action'; population.dispatchEvent(new Event('change', { bubbles: true })) })
  const property = container.querySelector<HTMLSelectElement>('select[aria-label="Property category"]')!
  expect([...property.options].map(option => option.value)).toEqual(['', 'kind=paid', 'kind=other'])
  await act(async () => { property.value = 'kind=paid'; property.dispatchEvent(new Event('change', { bubbles: true })) })
  await act(async () => { container.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) })
  expect(save).toHaveBeenCalledWith(expect.objectContaining({ tabs: [expect.objectContaining({ panels: [expect.objectContaining({ query: expect.objectContaining({ population: 'action', property_name: 'kind', property_value: 'paid' }) })] })] }))
})

it('renders a browser document panel in its own unit and without the transaction inspector', async () => {
  const documentPanel: DashboardDefinitionPanel = { ...panel, query: { metric: 'document_lcp', aggregation: 'p75', population: 'browser_document', end_seconds: 20, bucket_seconds: 10 } }
  request.mockResolvedValue(response)
  await act(async () => root.render(<ArchivedNativePanel panel={documentPanel} testId="run" />))
  expect(container.textContent).toContain('browser documents · document_lcp · p75 (ms)')
  expect(container.textContent).toContain('bucket estimates')
  expect(container.textContent).toContain('Final documents')
  expect([...container.querySelectorAll('button')].some(node => node.textContent?.startsWith('Inspect current bucket'))).toBe(false)
})

it('renders a derived series in its own unit with its unscored reasons and no raw baseline', async () => {
  const derivedPanel: DashboardDefinitionPanel = { ...panel, query: { ...panel.query!, comparison_run_id: 'base', derivation: { operation: 'baseline_percent_delta' } } as DashboardDefinitionPanel['query'] }
  const derived = { ...response, derivation: { operation: 'baseline_percent_delta' }, current: { ...response.current, unit: 'percent', buckets: [{ ...response.current.buckets[0], value: 12.5 }, { ...response.current.buckets[1], reason: 'missing_baseline_bucket' }] }, baseline: response.current }
  request.mockResolvedValue(derived)
  await act(async () => root.render(<ArchivedNativePanel panel={derivedPanel} testId="run" />))
  expect(container.textContent).toContain('baseline percent delta (percent)')
  expect(container.textContent).toContain('Missing (missing baseline bucket)')
  expect(container.textContent).not.toContain('Baseline (')
})

it('frames a message panel as received messages without the transaction inspector', async () => {
  const messagePanel: DashboardDefinitionPanel = { ...panel, query: { metric: 'message_count', aggregation: 'count', population: 'message', end_seconds: 20, bucket_seconds: 10 } as DashboardDefinitionPanel['query'] }
  request.mockResolvedValue(response)
  await act(async () => root.render(<ArchivedNativePanel panel={messagePanel} testId="run" />))
  expect(container.textContent).toContain('received messages · message_count')
  expect(container.textContent).not.toContain('nearest-rank')
  expect([...container.querySelectorAll('button')].some(node => node.textContent?.startsWith('Inspect current bucket'))).toBe(false)
})

it('keeps Java measured zero, missing reason and compression without a latency inspector', async () => {
  const javaPanel: DashboardDefinitionPanel = { ...panel, query: { metric: 'java_http_prepared_body_bytes', aggregation: 'avg', population: 'java_http_action', filters: { action: 'Read object' }, end_seconds: 20, bucket_seconds: 10 } }
  request.mockResolvedValue({ ...response, current: { ...response.current, unit: 'bytes', java_http_compression: 'gzip', buckets: [{ ...response.current.buckets[0], value: 0 }, { ...response.current.buckets[1] }] } })
  await act(async () => root.render(<ArchivedNativePanel panel={javaPanel} testId="run" />))
  expect(container.textContent).toContain('Java action representations')
  expect(container.textContent).toContain('Prepared-body compression: current gzip')
  expect(container.textContent).toContain('Missing (measurement unavailable)')
  expect(container.textContent).toContain('Measured observations')
  expect(container.textContent).toContain('Raw transaction drill-down is unavailable')
  expect(container.textContent).not.toContain('Latency percentiles')
  const cells = [...container.querySelectorAll('tbody tr:first-child td')].map(n => n.textContent)
  expect(cells).toEqual(['0', '0', '3'])
  expect([...container.querySelectorAll('button')].some(node => node.textContent?.startsWith('Inspect current bucket'))).toBe(false)
  expect(request).toHaveBeenCalledTimes(1)
})

it('exports each Java comparison population and missing reason independently', async () => {
  const javaPanel: DashboardDefinitionPanel = { ...panel, query: { metric: 'java_http_prepared_body_bytes', aggregation: 'avg', population: 'java_http_action', filters: { action: 'Read object' }, comparison_run_id: 'baseline', end_seconds: 20, bucket_seconds: 10 } }
  request.mockResolvedValue({ ...response, current: { ...response.current, unit: 'bytes', java_http_compression: 'gzip' }, baseline: { ...response.current, run_id: 'baseline', java_http_compression: 'gzip', buckets: [{ ...response.current.buckets[0], count: 7, value: 0 }, { ...response.current.buckets[1], count: 2, reason: 'incomplete_representation_evidence' }] } })
  await act(async () => root.render(<ArchivedNativePanel panel={javaPanel} testId="run" />))
  const props = exportProps.mock.lastCall![0] as { rows: Record<string, unknown>[]; columns: { key: string; value: (row: Record<string, unknown>) => unknown }[] }
  const values = props.rows.map(row => Object.fromEntries(props.columns.map(column => [column.key, column.value(row)])))
  expect(values[0]).toMatchObject({ value: 30, count: 3, baseline: 0, baselineCount: 7, baselineReason: 'measured' })
  expect(values[1]).toMatchObject({ value: '', reason: 'measurement_unavailable', baseline: '', baselineCount: 2, baselineReason: 'incomplete_representation_evidence' })
  expect(container.textContent).toContain('Missing (incomplete representation evidence)')
  expect([...container.querySelectorAll('tbody tr:first-child td')].map(n => n.textContent)).toEqual(['0', '30', '0', '3', '7'])
})

it('states HTTP body bytes with their unit and unscored reasons, without a latency inspector (NA-C)', async () => {
  const bytePanel: DashboardDefinitionPanel = { ...panel, query: { metric: 'bytes_sent', aggregation: 'sum', population: 'byte', filters: { action: 'login' }, end_seconds: 20, bucket_seconds: 10 } }
  request.mockResolvedValue({ ...response, current: { ...response.current, unit: 'bytes', buckets: [{ ...response.current.buckets[0], value: 0 }, { ...response.current.buckets[1], count: 1, reason: 'byte_evidence_unavailable' }] } })
  await act(async () => root.render(<ArchivedNativePanel panel={bytePanel} testId="run" />))
  expect(container.textContent).toContain('HTTP requests · bytes_sent · sum (bytes)')
  expect(container.textContent).toContain('a response served from the cache receives nothing')
  expect(container.textContent).toContain('Missing (byte evidence unavailable)')
  expect(container.textContent).toContain('Measured observations')
  expect(container.textContent).not.toContain('Latency percentiles')
  const cells = [...container.querySelectorAll('tbody tr:first-child td')].map(n => n.textContent)
  expect(cells).toEqual(['0', '0', '3'])
  expect([...container.querySelectorAll('button')].some(node => node.textContent?.startsWith('Inspect current bucket'))).toBe(false)
})

it('says why a byte comparison bucket is unscored, in the table and the export (NA-C)', async () => {
  const bytePanel: DashboardDefinitionPanel = { ...panel, query: { metric: 'bytes_sent', aggregation: 'sum', population: 'byte', comparison_run_id: 'baseline', end_seconds: 20, bucket_seconds: 10 } }
  request.mockResolvedValue({ ...response, current: { ...response.current, unit: 'bytes' }, baseline: { ...response.current, run_id: 'baseline', buckets: [{ ...response.current.buckets[0], count: 2, value: 40 }, { ...response.current.buckets[1], count: 1, reason: 'byte_evidence_unavailable' }] } })
  exportProps.mockReset()
  await act(async () => root.render(<ArchivedNativePanel panel={bytePanel} testId="run" />))
  expect(container.textContent).toContain('Missing (byte evidence unavailable)')
  const columns = exportProps.mock.calls[exportProps.mock.calls.length - 1][0].columns.map((column: { key: string }) => column.key)
  expect(columns).toEqual(['offset', 'value', 'baseline', 'count', 'reason', 'baselineCount', 'baselineReason'])
})
