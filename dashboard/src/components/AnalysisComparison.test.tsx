// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import AnalysisComparison from './AnalysisComparison'
import type { AnalysisImport } from '../types/analysis'

const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLElement; let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); request.mockReset() })
afterEach(() => { act(() => root.unmount()); container.remove() })
const dataset: AnalysisImport = { id: 'a', project_id: 'p', name: 'A', state: 'ready', files: [], revision: 1, accepted: 30, rejected: 0 }
const definition = { left: { dataset_id: 'a', metric: 'requests', aggregation: 'rate' }, right: { dataset_id: 'b', metric: 'latency', aggregation: 'avg' }, from: '2026-01-01T00:00:00Z', to: '2026-01-01T00:01:00Z', bucket_seconds: 1, max_lag_seconds: 2, mode: 'correlation' }
const saved = { id: 'saved', name: 'Capacity and latency', revision: 2, definition }
const input = (name: string) => [...container.querySelectorAll('label')].find(x => x.textContent?.startsWith(name))!.querySelector('input,select') as HTMLInputElement | HTMLSelectElement
async function change(name: string, value: string) {
  await act(async () => {
    const element = input(name)
    const prototype = element.tagName === 'SELECT' ? HTMLSelectElement.prototype : HTMLInputElement.prototype
    Object.getOwnPropertyDescriptor(prototype, 'value')!.set!.call(element, value)
    element.dispatchEvent(new Event(element.tagName === 'SELECT' ? 'change' : 'input', { bubbles: true }))
  })
}

it('restores distinct aggregations and saves the actual executed definition', async () => {
  let executed: unknown
  request.mockImplementation(async (path: string, opts?: RequestInit) => {
    if (path.startsWith('/analysis/compare?')) { executed = JSON.parse(String(opts?.body)); return { request: executed, left: [], right: [], correlation: [{ lag_seconds: 0, pairs: 0, expected_buckets: 60, coverage: 0, coefficient: null }] } }
    return [saved]
  })
  await act(async () => root.render(<AnalysisComparison projectId="p" dataset={dataset} datasets={[dataset, { ...dataset, id: 'b', name: 'B' }]} canEdit onAssociated={() => {}} />))
  await change('Saved comparison', 'saved')
  expect(input('Dataset aggregation').value).toBe('rate'); expect(input('Comparison aggregation').value).toBe('avg')
  await act(async () => [...container.querySelectorAll('button')].find(x => x.textContent === 'Analyze comparison')!.click())
  expect(container.textContent).toContain('Insufficient variation or evidence')
  await change('Dataset metric', 'edited-after-query')
  await change('Saved comparison name', 'Copy')
  await act(async () => [...container.querySelectorAll('button')].find(x => x.textContent === 'Save comparison')!.click())
  expect(request).toHaveBeenCalledWith('/analysis/comparisons?project_id=p', expect.objectContaining({ method: 'POST', body: JSON.stringify({ name: 'Copy', definition: executed }) }))
})

it('does not expose association or save mutations to viewers', async () => {
  request.mockResolvedValue([])
  await act(async () => root.render(<AnalysisComparison projectId="p" dataset={dataset} datasets={[dataset]} canEdit={false} onAssociated={() => {}} />))
  await change('Compare with', 'run')
  expect(container.textContent).not.toContain('Associate with run'); expect(container.textContent).not.toContain('Save comparison')
})

for (const selection of [
  { run_id: 'run', source_id: 'source', host: 'host', dimensions: { method: 'Lookup' }, metric: 'latency', aggregation: 'avg' },
  { run_id: 'run', transaction: 'Lookup', protocol: 'grpc', group: 'rpc', metric: 'transaction.duration', aggregation: 'p95' },
]) it(`retains the saved run evidence selectors for ${selection.source_id ?? selection.transaction}`, async () => {
  let executed: unknown
  request.mockImplementation(async (path: string, opts?: RequestInit) => {
    if (path.startsWith('/analysis/compare?')) { executed = JSON.parse(String(opts?.body)); return { request: executed, left: [], right: [] } }
    return [{ ...saved, definition: { ...definition, right: selection } }]
  })
  await act(async () => root.render(<AnalysisComparison projectId="p" dataset={dataset} datasets={[dataset]} canEdit onAssociated={() => {}} />))
  await change('Saved comparison', 'saved')
  await act(async () => [...container.querySelectorAll('button')].find(x => x.textContent === 'Analyze comparison')!.click())
  expect(executed).toMatchObject({ right: selection })
})
