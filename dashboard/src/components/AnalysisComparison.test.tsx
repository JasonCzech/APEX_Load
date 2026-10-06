// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import AnalysisComparison from './AnalysisComparison'
import type { AnalysisImport } from '../types/analysis'
import { downloadBlob } from '../utils/fileUtils'

const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request }))
vi.mock('../utils/fileUtils', () => ({ downloadBlob: vi.fn().mockResolvedValue('saved') }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLElement; let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); request.mockReset(); vi.mocked(downloadBlob).mockClear() })
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
  await act(async () => root.render(<AnalysisComparison importedIdentityVersion={2} projectId="p" dataset={dataset} datasets={[dataset, { ...dataset, id: 'b', name: 'B' }]} canEdit onAssociated={() => {}} />))
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
  await act(async () => root.render(<AnalysisComparison importedIdentityVersion={2} projectId="p" dataset={dataset} datasets={[dataset]} canEdit={false} onAssociated={() => {}} />))
  await change('Compare with', 'run')
  expect(container.textContent).not.toContain('Associate with run'); expect(container.textContent).not.toContain('Save comparison')
})

for (const selection of [
  { run_id: 'run', source_id: 'source', host: 'host', dimensions: { method: 'Lookup' }, metric: 'latency', aggregation: 'avg' },
  { run_id: 'run', transaction: 'Lookup', protocol: 'grpc', group: 'rpc', dimensions: { population: 'transaction' }, metric: 'transaction.duration', aggregation: 'p95' },
]) it(`retains the saved run evidence selectors for ${selection.source_id ?? selection.transaction}`, async () => {
  let executed: unknown
  request.mockImplementation(async (path: string, opts?: RequestInit) => {
    if (path.startsWith('/analysis/compare?')) { executed = JSON.parse(String(opts?.body)); return { request: executed, left: [], right: [] } }
    return [{ ...saved, definition: { ...definition, right: selection } }]
  })
  await act(async () => root.render(<AnalysisComparison importedIdentityVersion={2} projectId="p" dataset={dataset} datasets={[dataset]} canEdit onAssociated={() => {}} />))
  await change('Saved comparison', 'saved')
  await act(async () => [...container.querySelectorAll('button')].find(x => x.textContent === 'Analyze comparison')!.click())
  expect(executed).toMatchObject({ right: selection })
})

it('retains uncaptured saved run filters as unavailable evidence rather than broadening to all transactions', async () => {
  const selection = { run_id: 'run', source_id: '', transaction: 'Lookup', protocol: 'grpc', group: 'rpc', host: 'uncaptured-host', service: 'uncaptured-service', dimensions: { population: 'transaction', uncaptured: '' }, metric: 'transaction.duration', aggregation: 'p95' }
  let executed: unknown
  request.mockImplementation(async (path: string, options?: RequestInit) => {
    if (path.startsWith('/analysis/compare?')) {
      executed = JSON.parse(String(options?.body))
      return { request: executed, left: [{ timestamp_utc: '2026-01-01T00:00:00Z', metric_name: 'latency', unit: 'ms', quality: 'valid', value: 0 }], right: [] }
    }
    return [{ ...saved, definition: { ...definition, right: selection } }]
  })
  await act(async () => root.render(<AnalysisComparison projectId="p" dataset={dataset} datasets={[dataset]} canEdit onAssociated={() => {}} />))
  await change('Saved comparison', 'saved')
  expect(input('Host').value).toBe('uncaptured-host')
  expect(input('Service').value).toBe('uncaptured-service')
  expect(JSON.parse(input('Dimensions (JSON)').value)).toEqual(selection.dimensions)
  await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Analyze comparison')!.click())
  expect(executed).toMatchObject({ right: selection })
  expect(container.querySelector('[role=status]')?.textContent).toBe('No matching retained evidence for the comparison series. Missing or uncaptured labels remain unavailable.')
  expect(container.textContent).not.toContain('requires imported identity schema 2')
})


it('restores both imported identity selections and saves the executed receipt', async () => {
  const labelled = { ...dataset, mapping: { version: 2, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [] } }
  const selected = { ...definition, left: { ...definition.left, source_id: 'agent-a', host: 'host-a', service: 'service-a', dimensions: { 'a/b~c': '', group: 'one' } }, right: { ...definition.right, source_id: 'agent-b', host: 'host-b', service: 'service-b', dimensions: JSON.parse('{"__proto__":"","group":"two"}') } }
  let executed: unknown
  request.mockImplementation(async (path: string, options?: RequestInit) => {
    if (path.startsWith('/analysis/compare?')) { executed = JSON.parse(String(options?.body)); return { request: executed, left: [], right: [] } }
    return [{ ...saved, definition: selected }]
  })
  await act(async () => root.render(<AnalysisComparison importedIdentityVersion={2} projectId="p" dataset={labelled} datasets={[labelled, { ...labelled, id: 'b' }]} canEdit onAssociated={() => {}} />))
  await change('Saved comparison', 'saved')
  await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Analyze comparison')!.click())
  expect(executed).toMatchObject({ left: selected.left, right: selected.right })
  const source = container.querySelector('fieldset[aria-label="Dataset identity filters"] input')!
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(source, 'later-edit'); source.dispatchEvent(new Event('input', { bubbles: true })) })
  await change('Saved comparison name', 'Owned receipt')
  await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Save comparison')!.click())
  expect(request).toHaveBeenCalledWith('/analysis/comparisons?project_id=p', expect.objectContaining({ method: 'POST', body: JSON.stringify({ name: 'Owned receipt', definition: executed }) }))
})

for (const corrupt of [
  { from: 'not-a-timestamp' },
  { from: '2026-02-30T00:00:00Z', to: '2026-03-03T00:00:00Z' },
  { from: '2025-02-29T00:00:00Z', to: '2025-03-01T00:00:00Z' },
  { from: '2026-01-01T24:00:00Z', to: '2026-01-03T00:00:00Z' },
  { from: '2026-01-01T00:00:00+01:60' },
  { from: '2026-01-01T00:00:00.123456789Z' },
  { to: '2026-01-01T00:01:00.987000001Z' },
  { to: '2025-01-01T00:00:00Z' },
  { max_lag_seconds: 3601 },
  { bucket_seconds: Number.NaN },
  { right: { ...definition.right, dimensions: Object.fromEntries(Array.from({ length: 17 }, (_, i) => [`key${i}`, ''])) } },
  { right: { ...definition.right, aggregation: 'unsupported' } },
  { left: { ...definition.left, protocol: 'grpc', dimensions: { protocol: 'http' } } },
  { right: { ...definition.right, group: 'rpc', dimensions: { group: 'http' } } },
  { right: { ...definition.right, transaction: 'Lookup', dimensions: Object.fromEntries(Array.from({ length: 16 }, (_, i) => [`key${i}`, ''])) } },
  { left: { ...definition.left, transaction: 'x'.repeat(129) } },
]) it('admits an entire saved comparison before changing displayed state', async () => {
  const bad = { ...definition, left: { ...definition.left, metric: 'must-not-apply' }, ...corrupt }
  request.mockResolvedValue([saved, { ...saved, id: 'bad', name: 'Invalid', definition: bad }])
  await act(async () => root.render(<AnalysisComparison importedIdentityVersion={2} projectId="p" dataset={dataset} datasets={[dataset, { ...dataset, id: 'b' }]} canEdit onAssociated={() => {}} />))
  await change('Saved comparison', 'saved')
  await change('Dataset metric', 'owned-user-edit')
  const names = ['Dataset metric', 'Comparison metric', 'Compare with', 'From (UTC)', 'To (UTC)', 'Bucket seconds', 'Dataset aggregation', 'Comparison aggregation', 'Analysis', 'Maximum lag seconds']
  const before = names.map(name => input(name).value)
  await change('Saved comparison', 'bad')
  expect(names.map(name => input(name).value)).toEqual(before)
  expect(container.querySelector('[role=alert]')).not.toBeNull()
})


for (const bounds of [
  { from: '2026-01-01T00:00:00.123Z', to: '2026-01-01T00:01:00.987Z' },
  { from: '2026-01-01T00:00:00.123000000Z', to: '2026-01-01T00:01:00.987000000Z' },
  { from: '2025-12-31T19:00:00.123-05:00', to: '2025-12-31T19:01:00.987-05:00' },
]) it('restores exactly representable saved milliseconds and sends the same analytical bounds', async () => {
  let executed: unknown
  request.mockImplementation(async (path: string, options?: RequestInit) => {
    if (path.startsWith('/analysis/compare?')) { executed = JSON.parse(String(options?.body)); return { request: executed, left: [], right: [] } }
    return [{ ...saved, definition: { ...definition, ...bounds } }]
  })
  await act(async () => root.render(<AnalysisComparison importedIdentityVersion={2} projectId="p" dataset={dataset} datasets={[dataset, { ...dataset, id: 'b' }]} canEdit onAssociated={() => {}} />))
  await change('Saved comparison', 'saved')
  expect(input('From (UTC)').value).toBe('2026-01-01T00:00:00.123')
  expect(input('To (UTC)').value).toBe('2026-01-01T00:01:00.987')
  expect(input('From (UTC)').getAttribute('step')).toBe('0.001')
  await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Analyze comparison')!.click())
  expect(executed).toMatchObject({ from: '2026-01-01T00:00:00.123Z', to: '2026-01-01T00:01:00.987Z' })
})


it('refuses a saved comparison with a v2 side on an older peer before partial view mutation or query', async () => {
  const second = { ...dataset, id: 'b', mapping: { version: 2, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [] } }
  request.mockResolvedValue([saved])
  await act(async () => root.render(<AnalysisComparison projectId="p" dataset={dataset} datasets={[dataset, second]} canEdit onAssociated={() => {}} />))
  await change('Saved comparison', 'saved')
  expect(container.textContent).toContain('This comparison requires imported identity schema 2')
  expect(input('Dataset metric').value).toBe('')
  expect(input('Compare with').value).toBe('')
  await change('Compare with', 'b')
  expect([...container.querySelectorAll('button')].find(button => button.textContent === 'Analyze comparison')!.disabled).toBe(true)
  expect(request.mock.calls.some(([path]) => String(path).startsWith('/analysis/compare?'))).toBe(false)
})

for (const version of [undefined, 1]) it(`keeps v1 comparisons executable on old identity capability ${version}`, async () => {
  request.mockImplementation(async (path: string, options?: RequestInit) => path.startsWith('/analysis/compare?') ? { request: JSON.parse(String(options?.body)), left: [], right: [] } : [saved])
  await act(async () => root.render(<AnalysisComparison importedIdentityVersion={version} projectId="p" dataset={dataset} datasets={[dataset, { ...dataset, id: 'b' }]} canEdit onAssociated={() => {}} />))
  await change('Saved comparison', 'saved')
  const analyze = [...container.querySelectorAll('button')].find(button => button.textContent === 'Analyze comparison')!
  expect(analyze.disabled).toBe(false)
  await act(async () => analyze.click())
  expect(request.mock.calls.filter(([path]) => String(path).startsWith('/analysis/compare?'))).toHaveLength(1)
  expect(container.textContent).not.toContain('requires imported identity schema 2')
})


for (const side of ['left', 'right'] as const) for (const field of ['source_id', 'host', 'service', 'dimensions', 'transaction', 'protocol', 'group']) it(`refuses old-peer saved imported ${side} ${field} independently of mapping metadata`, async () => {
  const selected = { ...definition, [side]: { ...definition[side], [field]: field === 'dimensions' ? { label: '' } : 'exact-label', ...(side === 'right' ? { dataset_id: 'unknown-dataset' } : {}) } }
  request.mockResolvedValue([{ ...saved, definition: selected }])
  await act(async () => root.render(<AnalysisComparison projectId="p" dataset={dataset} datasets={[dataset]} canEdit onAssociated={() => {}} />))
  await change('Dataset metric', 'kept-draft')
  await change('Saved comparison', 'saved')
  expect(container.textContent).toContain('This comparison requires imported identity schema 2')
  expect(input('Dataset metric').value).toBe('kept-draft')
  expect(input('Compare with').value).toBe('')
  expect(request.mock.calls.some(([path]) => String(path).startsWith('/analysis/compare?'))).toBe(false)
})

it('retains imported named selectors through reopening, execution, saving and CSV after later edits', async () => {
  const labelled = { ...dataset, mapping: { version: 2, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [] } }
  const selected = {
    ...definition, mode: 'overlay', max_lag_seconds: 0,
    left: { ...definition.left, transaction: 'Lookup', protocol: 'grpc', dimensions: { protocol: 'grpc', 'a/b~c': '' } },
    right: { ...definition.right, group: ' exact group ', dimensions: JSON.parse('{"__proto__":"","group":" exact group "}') },
  }
  const expected = {
    ...selected,
    left: { ...definition.left, dimensions: { protocol: 'grpc', 'a/b~c': '', transaction: 'Lookup' } },
    right: { ...definition.right, dimensions: selected.right.dimensions },
    from: '2026-01-01T00:00:00.000Z', to: '2026-01-01T00:01:00.000Z',
  }
  let executed: unknown
  request.mockImplementation(async (path: string, options?: RequestInit) => {
    if (path.startsWith('/analysis/compare?')) {
      executed = JSON.parse(String(options?.body))
      const point = { timestamp_utc: '2026-01-01T00:00:00Z', metric_name: 'latency', unit: 'ms', value: 1, quality: 'valid' }
      return { request: executed, left: [point], right: [point] }
    }
    return [{ ...saved, definition: selected }]
  })
  await act(async () => root.render(<AnalysisComparison importedIdentityVersion={2} projectId="p" dataset={labelled} datasets={[labelled, { ...labelled, id: 'b' }]} canEdit onAssociated={() => {}} />))
  await change('Saved comparison', 'saved')
  const filters = container.querySelector('fieldset[aria-label="Dataset identity filters"]')!
  expect(Array.from(filters.querySelectorAll('input')).map(element => element.value)).toContain('Lookup')
  await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Analyze comparison')!.click())
  expect(executed).toEqual(expected)
  const transaction = Array.from(filters.querySelectorAll('label')).find(label => label.textContent === 'Filter 3 value')!.querySelector('input')!
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(transaction, 'must-not-export'); transaction.dispatchEvent(new Event('input', { bubbles: true })) })
  await change('Saved comparison name', 'Owned aliases')
  await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Save comparison')!.click())
  // The executed object was checked against every expected selector above.
  // Saving retains that receipt's exact serialization, including key order.
  expect(request).toHaveBeenCalledWith('/analysis/comparisons?project_id=p', expect.objectContaining({ method: 'POST', body: JSON.stringify({ name: 'Owned aliases', definition: executed }) }))
  await act(async () => container.querySelector<HTMLButtonElement>('button[aria-label="Export Dataset comparison data as CSV"]')!.click())
  const csv = await new Promise<string>((resolve, reject) => { const reader = new FileReader(); reader.onload = () => resolve(String(reader.result)); reader.onerror = reject; reader.readAsText(vi.mocked(downloadBlob).mock.calls[0][0]) })
  expect(csv).toContain('Lookup')
  expect(csv).toContain('grpc')
  expect(csv).toContain(' exact group ')
  expect(csv).toContain('__proto__')
  expect(csv).not.toContain('must-not-export')
})

it('revokes retained imported selectors and result saves when the peer capability disappears', async () => {
  const selected = { ...definition, left: { ...definition.left, source_id: 'exact-source' } }
  request.mockImplementation(async (path: string, options?: RequestInit) => path.startsWith('/analysis/compare?') ? { request: JSON.parse(String(options?.body)), left: [], right: [] } : [{ ...saved, definition: selected }])
  const view = (version?: number) => <AnalysisComparison importedIdentityVersion={version} projectId="p" dataset={dataset} datasets={[dataset, { ...dataset, id: 'b' }]} canEdit onAssociated={() => {}} />
  await act(async () => root.render(view(2)))
  await change('Saved comparison', 'saved')
  await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Analyze comparison')!.click())
  await change('Saved comparison name', 'Retained result')
  await act(async () => root.render(view()))
  expect([...container.querySelectorAll('button')].find(button => button.textContent === 'Analyze comparison')!.disabled).toBe(true)
  expect([...container.querySelectorAll('button')].find(button => button.textContent === 'Save comparison')!.disabled).toBe(true)
  expect(request.mock.calls.filter(([path]) => String(path).startsWith('/analysis/compare?'))).toHaveLength(1)
  expect(request.mock.calls.some(([, options]) => options?.method === 'POST' && String(options.body).includes('Retained result'))).toBe(false)
})

it('keeps legacy managed-run monitor selectors available without imported identity capability', async () => {
  const selected = { ...definition, right: { run_id: 'run', source_id: 'monitor', transaction: '', host: 'host', service: 'svc', dimensions: { method: 'Get' }, metric: 'latency', aggregation: 'avg' } }
  request.mockImplementation(async (path: string, options?: RequestInit) => path.startsWith('/analysis/compare?') ? { request: JSON.parse(String(options?.body)), left: [], right: [] } : [{ ...saved, definition: selected }])
  await act(async () => root.render(<AnalysisComparison projectId="p" dataset={dataset} datasets={[dataset]} canEdit onAssociated={() => {}} />))
  await change('Saved comparison', 'saved')
  await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Analyze comparison')!.click())
  const query = request.mock.calls.find(([path]) => String(path).startsWith('/analysis/compare?'))!
  expect(JSON.parse(String(query[1].body)).right).toEqual(selected.right)
})

for(const side of ['left','right']) it(`refuses protected ${side} dataset metadata before restoring a saved comparison`, async()=>{
  const mapping = {version:2,timestamp:['ts'],timestamp_encoding:'unix_s',timezone:'UTC',measures:[],identity:{host:['credentials','password']}}
  const left = side==='left'?{...dataset,mapping}:dataset
  const right = side==='right'?{...dataset,id:'b',mapping}:{...dataset,id:'b'}
  request.mockResolvedValue([saved])
  await act(async()=>root.render(<AnalysisComparison importedIdentityVersion={2} projectId="p" dataset={left} datasets={[left,right]} canEdit onAssociated={()=>{}} />))
  await change('Saved comparison','saved')
  expect(container.textContent).toContain('Protected credential columns')
  expect(input('Dataset metric').value).toBe('')
  expect(input('Compare with').value).toBe('')
  await change('Compare with','b')
  expect([...container.querySelectorAll('button')].find(button=>button.textContent==='Analyze comparison')!.disabled).toBe(true)
  expect(request.mock.calls.some(([path])=>String(path).startsWith('/analysis/compare?'))).toBe(false)
  expect(mapping.identity.host).toEqual(['credentials','password'])
})


for (const side of ['left', 'right']) for (const source of ['timestamp', 'value', 'name_column']) it(`refuses protected v1 ${side} ${source} metadata before saved restoration on a legacy peer`, async () => {
  const mapping = { version: 1, timestamp: source === 'timestamp' ? ['password'] : ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [{ value: source === 'value' ? ['password'] : ['v'], name: source === 'name_column' ? '' : 'latency', name_column: source === 'name_column' ? ['password'] : [], kind: 'gauge', unit: 'ms' }] }
  const left = side === 'left' ? { ...dataset, mapping } : dataset
  const right = side === 'right' ? { ...dataset, id: 'b', mapping } : { ...dataset, id: 'b' }
  request.mockResolvedValue([saved])
  await act(async () => root.render(<AnalysisComparison projectId="p" dataset={left} datasets={[left, right]} canEdit onAssociated={() => {}} />))
  await change('Dataset metric', 'retained-draft')
  await change('Saved comparison', 'saved')
  expect(container.textContent).toContain('Protected credential columns')
  expect(input('Dataset metric').value).toBe('retained-draft')
  expect(input('Compare with').value).toBe('')
  await change('Compare with', 'b')
  expect([...container.querySelectorAll('button')].find(button => button.textContent === 'Analyze comparison')!.disabled).toBe(true)
  expect(request.mock.calls.some(([path]) => String(path).startsWith('/analysis/compare?'))).toBe(false)
})

it('restores a saved histogram statistic and widens buckets to whole source windows', async () => {
  let executed: { bucket_seconds: number; from: string; to: string; left: { aggregation: string } } | undefined
  // Its windows start two minutes past each five-minute mark (a test-start anchored grid).
  const gridStart = Date.parse('2026-01-01T00:02:00Z')
  const summaryDataset: AnalysisImport = { ...dataset, mapping: { version: 1, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [{ value: ['p90'], name: 'latency', kind: 'summary', unit: 'ms', statistic: 'p90', window_seconds: 300 }] }, partition_manifest: { partitions: [{ stats: { kind: 'summary', metric: 'latency', min_time: '2026-01-01T00:02:00Z' } }] } }
  const p90 = { ...saved, definition: { ...definition, left: { dataset_id: 'a', metric: 'latency', aggregation: 'p90' }, mode: 'overlay', max_lag_seconds: 0 } }
  request.mockImplementation(async (path: string, opts?: RequestInit) => {
    if (path.startsWith('/analysis/compare?')) { executed = JSON.parse(String(opts?.body)); return { request: executed, left: [], right: [] } }
    return [p90]
  })
  await act(async () => root.render(<AnalysisComparison importedIdentityVersion={2} projectId="p" dataset={summaryDataset} datasets={[summaryDataset, { ...dataset, id: 'b', name: 'B' }]} canEdit onAssociated={() => {}} />))
  await change('Saved comparison', 'saved')
  expect(container.textContent).not.toContain('invalid measurement semantics')
  expect(input('Dataset aggregation').value).toBe('p90')
  expect(container.textContent).toContain('Buckets widen to 300 seconds')
  await act(async () => [...container.querySelectorAll('button')].find(x => x.textContent === 'Analyze comparison')!.click())
  expect(executed?.bucket_seconds).toBe(300)
  expect(executed?.left.aggregation).toBe('p90')
  const onGrid = (iso: string) => (((Date.parse(iso) - gridStart) % 300000) + 300000) % 300000
  expect(onGrid(executed!.from)).toBe(0)
  expect(onGrid(executed!.to)).toBe(0)
})
