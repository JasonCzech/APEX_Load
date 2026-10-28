// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import ArchivedNativePanel from './ArchivedNativePanel'
import type { DashboardDefinitionPanel } from '../hooks/api/useDashboardDefinitionsApi'
const { request, exports } = vi.hoisted(() => ({ request: vi.fn(), exports: vi.fn() }))
vi.mock('../hooks/apiClient', () => ({ request }))
vi.mock('./ExportableChart', () => ({ default: (props: { children: React.ReactNode }) => { exports(props); return <div>{props.children}</div> } }))
let container: HTMLDivElement
let root: Root
beforeEach(() => { request.mockReset(); exports.mockReset(); container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(async () => { await act(async () => root.unmount()); container.remove() })
const pin = { version: 1 as const, query_sha256: 'a'.repeat(64), current: { run_id: 'retained', manifest_sha256: 'b'.repeat(64), tuple_ids: ['c'.repeat(64)] } }
const panel: DashboardDefinitionPanel = { id: 'browser', title: 'Frozen browser', kind: 'table', x: 0, y: 0, w: 24, h: 8, query: { query_version: 4, browser_tuple_mode: 'separate', population: 'browser_document', metric: 'document_lcp', aggregation: 'p75', end_seconds: 20, bucket_seconds: 10, browser_inputs: pin } }
const result = { current: { run_id: 'retained', buckets: [], warnings: [], identities: ['c'.repeat(64)], browser_tuples: { version: 1, tuples: [{ id: 'c'.repeat(64), label: 'Desktop', cohort_digest: 'd'.repeat(16), measurement: { metric: 'lcp', unit: 'ms', algorithm: 'v1', algorithm_sha256: 'e'.repeat(64), engine: 'chromium', frame_scope: 'main_frame', lifecycle_phase: 'navigation' }, buckets: [{ from: '2026-10-01T00:00:00Z', to: '2026-10-01T00:00:10Z', offset_seconds: 0, count: 1, value: 100 }] }] } }, warnings: [], added: [], removed: [], comparable: false, browser_inputs: pin }
it('reopens the exact retained run from a different page and exports its pins', async () => {
 request.mockResolvedValue(result)
 await act(async () => root.render(<ArchivedNativePanel panel={panel} testId="different-page-run" />))
 expect(request.mock.calls[0][0]).toBe('/tests/retained/archive/native-series')
 expect(JSON.parse(request.mock.calls[0][1].body).browser_inputs).toEqual(pin)
 expect(container.textContent).toContain('Current run: retained')
 expect(container.textContent).toContain('Frozen archive inputs')
 const columns = exports.mock.calls[exports.mock.calls.length - 1][0].columns as { key: string; value: (row: unknown) => unknown }[]
 expect(columns.find(item => item.key === 'queryPin')?.value({})).toBe(pin.query_sha256)
 expect(columns.find(item => item.key === 'currentPin')?.value({})).toBe(pin.current.manifest_sha256)
})
it('renders and exports v2 policy pins and selection receipt SHA-256 on query_version 5 panels', async () => {
 const v2Pin = {
  version: 2 as const,
  query_sha256: 'a'.repeat(64),
  selection_receipt_sha256: 'f'.repeat(64),
  policy_pins: [{ kind: 'browser_policy' as const, id: 'pol-browser', revision: 2, sha256: '1'.repeat(64) }],
  current: {
   run_id: 'retained',
   manifest_sha256: 'b'.repeat(64),
   tuple_ids: ['c'.repeat(64)],
   policy_pins: [{ kind: 'browser_policy' as const, id: 'pol-browser', revision: 2, sha256: '1'.repeat(64) }],
  },
 }
 const v2Panel: DashboardDefinitionPanel = { ...panel, query: { ...panel.query!, query_version: 5, browser_inputs: v2Pin } }
 request.mockResolvedValue({ ...result, browser_inputs: v2Pin })
 await act(async () => root.render(<ArchivedNativePanel panel={v2Panel} testId="different-page-run" />))
 expect(container.textContent).toContain('Selection receipt:')
 expect(container.textContent).toContain('f'.repeat(64))
 expect(container.textContent).toContain('Policy pins: browser_policy pol-browser@2')
 const columns = exports.mock.calls[exports.mock.calls.length - 1][0].columns as { key: string; value: (row: unknown) => unknown }[]
 expect(columns.find(item => item.key === 'selectionReceiptPin')?.value({})).toBe('f'.repeat(64))
 expect(String(columns.find(item => item.key === 'policyPins')?.value({}))).toContain('browser_policy:pol-browser@2:')
})
it('keeps a stale publication unavailable rather than re-querying the page run', async () => {
 request.mockRejectedValue(new Error('saved browser inputs changed or are unavailable'))
 await act(async () => root.render(<ArchivedNativePanel panel={panel} testId="different-page-run" />))
 expect(container.querySelector('[role="alert"]')?.textContent).toContain('saved browser inputs changed')
 expect(exports).not.toHaveBeenCalled()
 const retry = container.querySelector('button')!
 await act(async () => retry.click())
 expect(request.mock.calls.map(call => call[0])).toEqual(['/tests/retained/archive/native-series', '/tests/retained/archive/native-series'])
})
