// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import AnalysisWorkspace from './AnalysisWorkspace'
import type { AnalysisImport, AnalysisPrecisionReview } from '../types/analysis'

const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request, apiFetch: vi.fn() }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLElement; let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); request.mockReset() })
afterEach(() => { act(() => root.unmount()); container.remove() })
const draft: AnalysisImport = { id: 'draft', project_id: 'p', name: 'Decimal evidence', revision: 7, state: 'mapped', files: [{ id: 'one', relative_path: 'one.parquet', size: 100, uploaded: 100 }, { id: 'two', relative_path: 'two.parquet', size: 100, uploaded: 100 }], accepted: 0, rejected: 0, mapping: { version: 2, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [{ value: ['amount'], name: 'amount', kind: 'gauge', unit: 'count' }] } }
const preview = { schema: { rows: 1, columns: ['ts', 'amount'].map(path => ({ path, path_components: [path], physical_type: 'INT64', repeated: false })) }, samples: [] }
const caps = { native_parquet: true, enabled: true, imported_identity_version: 2, precision_consent_version: 1 }
const digest = 'a'.repeat(64)
const review: AnalysisPrecisionReview = { version: 1, import_id: 'draft', revision: 7, acknowledgement_sha256: 'b'.repeat(64), review: { version: 1, conversion: 'float64', mapping_sha256: digest, source_set_sha256: digest, source_count: 2, sha256: 'c'.repeat(64), risks: [{ file_sha256: digest, column: ['amount'], precision: 38, scale: 2, reason: 'fractional_decimal_may_round_to_float64' }] } }
const button = (text: string) => [...container.querySelectorAll('button')].find(item => item.textContent === text)!
const select = (text: string) => [...container.querySelectorAll('label')].find(item => item.firstChild?.textContent === text)!.querySelector('select')!
async function choose(element: HTMLSelectElement, value: string) {
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(element, value); element.dispatchEvent(new Event('change', { bubbles: true })) })
}
async function open(options: { capabilities?: object; item?: AnalysisImport; response?: AnalysisPrecisionReview; reviewPromise?: Promise<AnalysisPrecisionReview>; canEdit?: boolean } = {}) {
  const item = options.item ?? draft
  request.mockImplementation(async (path: string, init?: RequestInit) => {
    if (path.includes('/capabilities')) return options.capabilities ?? caps
    if (path.includes('/mappings')) return []
    if (path.endsWith('/precision-review')) return options.reviewPromise ?? options.response ?? review
    if (path.endsWith('/preview')) return preview
    if (path.endsWith('/mapping')) return { ...item, revision: 8, mapping: JSON.parse(String(init?.body)).mapping }
    if (path.endsWith('/commit')) return { ...item, revision: 9, state: 'normalizing' }
    return [item]
  })
  await act(async () => root.render(<AnalysisWorkspace projectId="p" canEdit={options.canEdit ?? true} />))
  await act(async () => [...container.querySelectorAll('button')].find(item => item.textContent?.includes('Decimal evidence'))!.click())
}

it('requires all-source review and explicit acknowledgement before a lossy import', async () => {
  await open()
  expect(button('Validate and import').disabled).toBe(true)
  await act(async () => button('Review precision').click())
  expect(container.textContent).toContain('Reviewed 2 source files')
  expect(container.textContent).toContain('does not count rows whose values actually change')
  expect(button('Validate and import').disabled).toBe(true)
  await choose(select('Decimal conversion'), 'allow_float64_rounding')
  expect(container.textContent).not.toContain('Reviewed 2 source files')
  await act(async () => button('Review precision').click())
  expect(button('Validate and import').disabled).toBe(true)
  await act(async () => [...container.querySelectorAll('label')].find(item => item.textContent?.includes('I accept potential rounding'))!.querySelector('input')!.click())
  expect(button('Validate and import').disabled).toBe(false)
  await act(async () => button('Validate and import').click())
  const call = request.mock.calls.find(([path]) => String(path).endsWith('/mapping'))!
  expect(JSON.parse(String(call[1].body))).toMatchObject({ revision: 7, mapping: { version: 3, precision_policy: 'allow_float64_rounding' }, precision_review_sha256: review.review.sha256, precision_acknowledgement_sha256: review.acknowledgement_sha256 })
  expect(request.mock.calls.filter(([path]) => String(path).endsWith('/commit'))).toHaveLength(1)
})

it('invalidates consent after identity edits without downgrading the precision policy', async () => {
  await open()
  await choose(select('Decimal conversion'), 'allow_float64_rounding')
  await act(async () => button('Review precision').click())
  await act(async () => [...container.querySelectorAll('label')].find(item => item.textContent?.includes('I accept potential rounding'))!.querySelector('input')!.click())
  await choose(select('Host column'), JSON.stringify(['amount']))
  expect(button('Validate and import').disabled).toBe(true)
  expect(container.textContent).not.toContain('I accept potential rounding')
  await act(async () => button('Review precision').click())
  const calls = request.mock.calls.filter(([path]) => String(path).endsWith('/precision-review'))
  const call = calls[calls.length - 1]!
  expect(JSON.parse(String(call[1].body)).mapping).toMatchObject({ version: 3, precision_policy: 'allow_float64_rounding', identity: { host: ['amount'] } })
})

it('permits risk-free legacy mappings after review without manufacturing acknowledgement', async () => {
  await open({ response: { ...review, review: { ...review.review, risks: [] } } })
  await act(async () => button('Review precision').click())
  expect(button('Validate and import').disabled).toBe(false)
  await act(async () => button('Validate and import').click())
  const body = JSON.parse(String(request.mock.calls.find(([path]) => String(path).endsWith('/mapping'))![1].body))
  expect(body.mapping.version).toBe(2)
  expect(body.precision_acknowledgement_sha256).toBeUndefined()
})

it('refuses stale reviews and old controllers for precision mappings', async () => {
  await open({ response: { ...review, revision: 6 } })
  await act(async () => button('Review precision').click())
  expect(container.textContent).toContain('invalid or stale precision review')
  expect(button('Validate and import').disabled).toBe(true)
  await open({ capabilities: { ...caps, precision_consent_version: undefined }, item: { ...draft, mapping: { ...draft.mapping!, version: 3, precision_policy: 'allow_float64_rounding' } } })
  // Remount to fetch the changed peer's capabilities.
  await act(async () => root.render(<AnalysisWorkspace key="old-peer" projectId="p" canEdit />))
  await act(async () => [...container.querySelectorAll('button')].find(item => item.textContent?.includes('Decimal evidence'))!.click())
  expect(container.textContent).toContain('requires precision consent support')
  expect(button('Validate and import').disabled).toBe(true)
  expect(request.mock.calls.some(([path]) => String(path).endsWith('/mapping'))).toBe(false)
})

it('drops a late precision review after switching projects', async () => {
  let resolve!: (value: AnalysisPrecisionReview) => void
  const pending = new Promise<AnalysisPrecisionReview>(done => { resolve = done })
  await open({ reviewPromise: pending })
  await act(async () => button('Review precision').click())
  const call = request.mock.calls.find(([path]) => String(path).endsWith('/precision-review'))!
  await act(async () => root.render(<AnalysisWorkspace projectId="other" canEdit />))
  expect(call[1].signal.aborted).toBe(true)
  await act(async () => resolve(review))
  expect(container.textContent).not.toContain('Reviewed 2 source files')
  expect(request.mock.calls.some(([path]) => String(path).endsWith('/mapping'))).toBe(false)
})
