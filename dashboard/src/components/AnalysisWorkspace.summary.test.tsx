// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import AnalysisWorkspace from './AnalysisWorkspace'

const request = vi.hoisted(() => vi.fn())
const apiFetch = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request, apiFetch }))
vi.mock('../utils/fileUtils', () => ({ downloadBlob: vi.fn() }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLElement; let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); request.mockReset(); apiFetch.mockReset() })
afterEach(() => { act(() => root.unmount()); container.remove() })

const statistics = ['avg', 'min', 'max', 'sum', 'count', 'p50', 'p75', 'p90', 'p95', 'p99', 'p999']
const caps = { native_parquet: true, enabled: true, imported_identity_version: 2, chunk_bytes: 4 * 1024 * 1024, summary_statistics: statistics, histogram_bound_limit: 128 }
const column = (name: string) => ({ path: name, path_components: [name], physical_type: 'DOUBLE', repeated: false })
const preview = { schema: { rows: 1, columns: [{ ...column('ts'), physical_type: 'INT64' }, column('p95'), column('n'), column('b10')] }, samples: [] }
const draft = { id: 'd', project_id: 'p', name: 'Summaries', revision: 1, state: 'uploading', files: [{ id: 'file', relative_path: 's.parquet', size: 1, uploaded: 1 }], accepted: 0, rejected: 0, mapping: { version: 1, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [{ value: ['p95'], name: 'latency', kind: 'gauge', unit: 'ms' }] } }
const button = (label: string) => [...container.querySelectorAll('button')].find(b => b.textContent === label)!
const labelled = <T extends HTMLElement>(text: string) => [...container.querySelectorAll('label')].find(l => l.textContent?.startsWith(text))?.querySelector<T>('select, input') ?? null
const choose = async (element: HTMLSelectElement | HTMLInputElement, value: string) => act(async () => {
  const prototype = element instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype
  Object.getOwnPropertyDescriptor(prototype, 'value')!.set!.call(element, value)
  element.dispatchEvent(new Event(element instanceof HTMLSelectElement ? 'change' : 'input', { bubbles: true }))
})

async function openDraft(capabilities: Record<string, unknown>, item: Record<string, unknown> = draft, onMapping?: (body: Record<string, unknown>) => void) {
  request.mockImplementation(async (path: string, options?: RequestInit) => {
    if (path.includes('capabilities')) return capabilities
    if (path.startsWith('/analysis/imports?')) return [item]
    if (path.endsWith('/preview')) return preview
    if (path.endsWith('/mapping')) { onMapping?.(JSON.parse(String(options?.body))); return { ...item, state: 'mapped', revision: 2 } }
    if (path.endsWith('/commit')) return { ...item, state: 'ready', revision: 3 }
    if (path.endsWith('/series')) return { points: [] }
    return []
  })
  await act(async () => root.render(<AnalysisWorkspace projectId="p" canEdit />))
  await act(async () => [...container.querySelectorAll('button')].find(b => b.textContent?.includes(String(item.name)))!.click())
}

it('declares the statistic, window and sample count of a summary column', async () => {
  let submitted: Record<string, unknown> | undefined
  await openDraft(caps, draft, body => { submitted = body })
  await choose(labelled<HTMLSelectElement>('Semantics')!, 'summary')
  // Nothing is guessed: the statistic and window must be declared.
  expect(labelled<HTMLSelectElement>('Statistic reported')!.value).toBe('')
  expect(labelled<HTMLInputElement>('Source window (seconds)')!.value).toBe('')
  expect(container.textContent).toContain('choose the statistic this summary reports')
  expect(button('Validate and import').disabled).toBe(true)
  // Leaving summary semantics drops every summary-only field.
  await choose(labelled<HTMLSelectElement>('Semantics')!, 'gauge')
  expect(labelled('Statistic reported')).toBeNull()
  await choose(labelled<HTMLSelectElement>('Semantics')!, 'summary')
  await choose(labelled<HTMLSelectElement>('Statistic reported')!, 'p95')
  await choose(labelled<HTMLInputElement>('Source window (seconds)')!, '')
  expect(container.textContent).toContain('the summary window must be 1-86400 seconds')
  expect(button('Validate and import').disabled).toBe(true)
  await choose(labelled<HTMLInputElement>('Source window (seconds)')!, '300')
  await choose(labelled<HTMLSelectElement>('Sample count column')!, JSON.stringify(['n']))
  expect(button('Validate and import').disabled).toBe(false)
  await act(async () => button('Validate and import').click())
  const measure = (submitted?.mapping as { measures: Record<string, unknown>[] }).measures[0]
  expect(measure).toMatchObject({ kind: 'summary', statistic: 'p95', window_seconds: 300, sample_count_column: ['n'] })
})

it('keeps display-only summaries on controllers without summary statistics', async () => {
  await openDraft({ ...caps, summary_statistics: undefined })
  await choose(labelled<HTMLSelectElement>('Semantics')!, 'summary')
  expect(labelled('Statistic reported')).toBeNull()
  expect(button('Validate and import').disabled).toBe(false)
})

it('queries a summary metric only for its declared statistics over whole windows', async () => {
  const ready = { ...draft, state: 'ready', mapping: { ...draft.mapping, measures: [
    { value: ['p50'], name: 'latency', kind: 'summary', unit: 'ms', statistic: 'p50', window_seconds: 60 },
    { value: ['p95'], name: 'latency', kind: 'summary', unit: 'ms', statistic: 'p95', window_seconds: 60 },
  ] } }
  await openDraft(caps, ready)
  const query = [...container.querySelectorAll('fieldset')].find(f => f.querySelector('legend')?.textContent === 'Query dataset')!
  const aggregation = labelled<HTMLSelectElement>('Summary statistic')!
  expect([...aggregation.options].map(o => o.value)).toEqual(['p50', 'p95'])
  await choose(aggregation, 'p95')
  const dates = query.querySelectorAll<HTMLInputElement>('input[type="datetime-local"]')
  await choose(dates[0], '2026-01-01T00:00')
  await choose(dates[1], '2026-01-01T01:00')
  await act(async () => button('Show series').click())
  const call = request.mock.calls.find(([path]) => String(path).endsWith('/series'))!
  const body = JSON.parse(String(call[1].body))
  // 3600s / 500 rounds to 8s, widened to one 60-second source window.
  expect(body).toMatchObject({ metric: 'latency', aggregation: 'p95', bucket_seconds: 60 })
})

it('declares a wide histogram with a count column for every bound but the last', async () => {
  let submitted: Record<string, unknown> | undefined
  await openDraft(caps, draft, body => { submitted = body })
  await choose(labelled<HTMLSelectElement>('Semantics')!, 'histogram')
  const bounds = labelled<HTMLInputElement>('Upper bounds')!
  await choose(bounds, '10,')
  expect(bounds.value).toBe('10,')
  await choose(bounds, '10, 100, +Inf')
  expect(container.textContent).toContain('choose a count column for every bound but the last')
  expect(button('Validate and import').disabled).toBe(true)
  await choose(labelled<HTMLSelectElement>('Count for the bin ending at 10')!, JSON.stringify(['b10']))
  await choose(labelled<HTMLSelectElement>('Count for the bin ending at 100')!, JSON.stringify(['n']))
  await choose(labelled<HTMLSelectElement>('Bucket counts')!, 'le_cumulative')
  expect(labelled('Count for ≤ 10')).not.toBeNull()
  expect(button('Validate and import').disabled).toBe(false)
  await act(async () => button('Validate and import').click())
  const measure = (submitted?.mapping as { measures: Record<string, unknown>[] }).measures[0]
  expect(measure).toMatchObject({ kind: 'histogram', value: ['p95'], histogram: { bounds: ['10', '100', '+Inf'], bucket_counts: 'le_cumulative', temporality: 'delta', window_seconds: 60, count_columns: [['b10'], ['n']] } })
})

it('declares a long cumulative histogram by its bound column', async () => {
  let submitted: Record<string, unknown> | undefined
  await openDraft(caps, draft, body => { submitted = body })
  await choose(labelled<HTMLSelectElement>('Semantics')!, 'histogram')
  await choose(labelled<HTMLInputElement>('Upper bounds')!, '0.1, +Inf')
  await choose(labelled<HTMLSelectElement>('Counting')!, 'cumulative')
  expect(labelled('Window (seconds)')).toBeNull()
  await choose(labelled<HTMLSelectElement>('Table shape')!, 'long')
  // A long table starts with no bound column picked rather than a guessed one.
  expect(labelled<HTMLSelectElement>('Bound column')!.value).toBe('[]')
  await choose(labelled<HTMLSelectElement>('Bound column')!, JSON.stringify(['b10']))
  await act(async () => button('Validate and import').click())
  const histogram = (submitted?.mapping as { measures: { histogram: Record<string, unknown> }[] }).measures[0].histogram
  expect(histogram).toEqual({ bounds: ['0.1', '+Inf'], bucket_counts: 'per_bin', temporality: 'cumulative', bound_column: ['b10'] })
})

it('queries a histogram for counts and percentiles over whole windows', async () => {
  const ready = { ...draft, state: 'ready', mapping: { ...draft.mapping, measures: [
    { value: ['p95'], name: 'latency', kind: 'histogram', unit: 'ms', histogram: { bounds: ['10', '+Inf'], bucket_counts: 'per_bin', temporality: 'delta', window_seconds: 300, count_columns: [['b10']] } },
  ] } }
  await openDraft(caps, ready)
  const aggregation = labelled<HTMLSelectElement>('Histogram statistic')!
  expect([...aggregation.options].map(o => o.value)).toEqual(['count', 'p50', 'p75', 'p90', 'p95', 'p99', 'p999'])
  await choose(aggregation, 'p99')
  const query = [...container.querySelectorAll('fieldset')].find(f => f.querySelector('legend')?.textContent === 'Query dataset')!
  const dates = query.querySelectorAll<HTMLInputElement>('input[type="datetime-local"]')
  await choose(dates[0], '2026-01-01T00:00')
  await choose(dates[1], '2026-01-01T01:00')
  await act(async () => button('Show series').click())
  const body = JSON.parse(String(request.mock.calls.find(([path]) => String(path).endsWith('/series'))![1].body))
  expect(body).toMatchObject({ metric: 'latency', aggregation: 'p99', bucket_seconds: 300 })
})
