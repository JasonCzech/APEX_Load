// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import ElasticSourceFields from './ElasticSourceFields'
import { emptyElasticSelection } from './elasticMonitoringModel'
import type { MonitorSource } from '../types/runMonitoring'
const api = vi.hoisted(() => ({ listELKConnections: vi.fn(), inspectELKConnection: vi.fn(), getELKInspection: vi.fn(), cancelELKInspection: vi.fn() }))
vi.mock('../hooks/api/useELKConnectionsApi', () => ({ useELKConnectionsApi: () => api }))
let root: Root; let container: HTMLElement
beforeEach(() => { vi.clearAllMocks(); api.cancelELKInspection.mockResolvedValue(undefined); container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); api.listELKConnections.mockResolvedValue({ connections: [{ id: 'connection', enabled: true, name: 'Private OpenSearch', product: 'opensearch', revision: 2, verified_revision: 2, collection_mode: 'collector', collector_agent_id: 'agent', capabilities: { esql: false }, config: {} }] }) })
afterEach(() => { act(() => root.unmount()); container.remove() })
it('inherits collector execution, rejects unavailable ES|QL and enforces five advanced queries', async () => {
  const onChange = vi.fn()
  function Harness() { const [source, setSource] = useState<MonitorSource>({ id: '', name: 'Logs', revision: 0, collector_id: '', interval_seconds: 10, pack: 'elastic', enabled: true, elastic: emptyElasticSelection('p') }); return <ElasticSourceFields projectId="p" source={source} onChange={next => { setSource(next); onChange(next) }} /> }
  await act(async () => root.render(<Harness />))
  const connection = container.querySelector('select')!
  await act(async () => { connection.value = 'connection'; connection.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ executor_mode: 'collector', collector_id: 'agent' }))
  expect(container.textContent).toContain('Assigned collector agent')
  const addQuery = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Add Elastic query')!
  for (let i = 0; i < 5; i++) await act(async () => addQuery.click())
  expect(addQuery.disabled).toBe(true)
  const language = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith('Query 1 language'))!.querySelector('select')!
  expect(Array.from(language.options).find(option => option.value === 'esql')!.disabled).toBe(true)
})
it('previews the exact saved revision with explicit projection and UTC window', async () => {
  const source: MonitorSource = { id: 'source', name: 'Logs', revision: 1, collector_id: 'agent', executor_mode: 'collector', connection_id: 'connection', interval_seconds: 10, pack: 'elastic', enabled: true, elastic: { ...emptyElasticSelection('p'), indices: ['logs-*'], projected_fields: ['service.name'] } }
  api.inspectELKConnection.mockResolvedValue({ id: 'op', connection_id: 'connection', connection_revision: 2, status: 'complete', kind: 'preview' })
  await act(async () => root.render(<ElasticSourceFields projectId="p" source={source} onChange={vi.fn()} />))
  await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Preview Elastic source')!.click())
  expect(api.inspectELKConnection).toHaveBeenCalledWith('p', 'connection', expect.objectContaining({ expected_revision: 2, kind: 'preview', selection: expect.objectContaining({ projected_fields: ['service.name'], page_size: 100 }), from_utc: expect.stringMatching(/Z$/), to_utc: expect.stringMatching(/Z$/) }), expect.anything())
})

it('previews component health without document indices and requires indices when document capture is added', async () => {
  const source: MonitorSource = { id: 'health', name: 'Cluster health', revision: 1, collector_id: 'agent', executor_mode: 'collector', connection_id: 'connection', interval_seconds: 30, pack: 'elastic', enabled: true, elastic: { ...emptyElasticSelection('p'), logs: false, cluster_health: true } }
  api.inspectELKConnection.mockResolvedValue({ id: 'health-preview', status: 'complete', kind: 'preview' })
  const validation = vi.fn()
  await act(async () => root.render(<ElasticSourceFields projectId="p" source={source} onChange={vi.fn()} onValidationChange={validation} />))
  const preview = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Preview Elastic source')!
  expect(preview.disabled).toBe(false)
  await act(async () => preview.click())
  expect(api.inspectELKConnection).toHaveBeenCalledWith('p', 'connection', expect.objectContaining({ selection: expect.objectContaining({ indices: [], logs: false, cluster_health: true }), kind: 'preview' }), expect.anything())
  await act(async () => root.render(<ElasticSourceFields projectId="p" source={{ ...source, elastic: { ...source.elastic!, logs: true } }} onChange={vi.fn()} onValidationChange={validation} />))
  expect(validation).toHaveBeenLastCalledWith(expect.stringContaining('indices'))
  expect(preview.disabled).toBe(true)
})

it('allows source retention ceilings to be lowered without exposing continuation state', async () => {
  const onChange = vi.fn()
  const source: MonitorSource = { id: 'source', name: 'Logs', revision: 1, collector_id: 'agent', executor_mode: 'collector', connection_id: 'connection', interval_seconds: 10, pack: 'elastic', enabled: true, elastic: { ...emptyElasticSelection('p'), indices: ['logs-*'] } }
  await act(async () => root.render(<ElasticSourceFields projectId="p" source={source} onChange={onChange} />))
  const budget = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith('Maximum retained records'))!.querySelector('input')!
  expect(budget.max).toBe('10000')
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(budget, '500'); budget.dispatchEvent(new Event('input', { bubbles: true })) })
  expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ elastic: expect.objectContaining({ max_retained_records: 500 }) }))
  expect(container.textContent).toContain('coverage as truncated')
})

it('keeps invalid query JSON blocking after another query is removed or the connection changes', async () => {
  const validation = vi.fn()
  function Harness() { const [source, setSource] = useState<MonitorSource>({ id: '', name: 'Logs', revision: 0, collector_id: 'agent', connection_id: 'connection', interval_seconds: 10, pack: 'elastic', enabled: true, elastic: { ...emptyElasticSelection('p'), indices: ['logs-*'], queries: [{ name: 'first', language: 'dsl', kind: 'log', dsl: { match_all: {} } }, { name: 'second', language: 'dsl', kind: 'log', dsl: { match_all: {} } }] } }); return <ElasticSourceFields projectId="p" source={source} onChange={setSource} onValidationChange={validation} /> }
  await act(async () => root.render(<Harness />))
  const input = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith('Query 1 DSL'))!.querySelector('textarea')!
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(input, '{'); input.dispatchEvent(new Event('input', { bubbles: true })) })
  await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Remove Elastic query 2')!.click())
  expect(container.textContent).toContain('Enter one valid JSON query clause')
  expect(validation).toHaveBeenLastCalledWith(expect.stringContaining('valid JSON'))
  const connection = container.querySelector('select')!
  await act(async () => { connection.value = ''; connection.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(validation).toHaveBeenLastCalledWith(expect.stringContaining('valid JSON'))
})

it('retains the correct query draft when an earlier query is removed', async () => {
  const validation = vi.fn()
  function Harness() { const [source, setSource] = useState<MonitorSource>({ id: 'source', name: 'Logs', revision: 1, collector_id: 'agent', connection_id: 'connection', interval_seconds: 10, pack: 'elastic', enabled: true, elastic: { ...emptyElasticSelection('p'), indices: ['logs-*'], queries: [{ name: 'first', language: 'dsl', kind: 'log', dsl: { match_all: {} } }, { name: 'second', language: 'dsl', kind: 'log', dsl: { term: { service: 'checkout' } } }] } }); return <ElasticSourceFields projectId="p" source={source} onChange={setSource} onValidationChange={validation} /> }
  await act(async () => root.render(<Harness />))
  const input = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith('Query 2 DSL'))!.querySelector('textarea')!
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(input, '{"retained":'); input.dispatchEvent(new Event('input', { bubbles: true })) })
  await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Remove Elastic query 1')!.click())
  const retained = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith('Query 1 DSL'))!.querySelector('textarea')!
  expect(retained.value).toBe('{"retained":')
  expect(validation).toHaveBeenLastCalledWith(expect.stringContaining('valid JSON'))
})

it('authors several indices without losing separators and produces bounded weighted templates', async () => {
  api.listELKConnections.mockResolvedValue({ connections: [{ id: 'connection', enabled: true, name: 'Elastic', product: 'elasticsearch', revision: 2, verified_revision: 2, collection_mode: 'direct', capabilities: { esql: true, async_esql: false }, config: {} }] })
  const onChange = vi.fn()
  function Harness() { const [source, setSource] = useState<MonitorSource>({ id: '', name: 'Metrics', revision: 0, collector_id: '', interval_seconds: 10, pack: 'elastic', enabled: true, connection_id: 'connection', elastic: emptyElasticSelection('p') }); return <ElasticSourceFields projectId="p" source={source} onChange={next => { setSource(next); onChange(next) }} /> }
  await act(async () => root.render(<Harness />))
  const input = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith('Index patterns'))!.querySelector('input')!
  const fill = async (value: string) => act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })) })
  await fill('logs-*, ')
  expect(input.value).toBe('logs-*, ')
  await fill('logs-*, traces-*')
  expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ elastic: expect.objectContaining({ indices: ['logs-*', 'traces-*'] }) }))
  await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Add ES|QL weighted duration average template')!.click())
  expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ elastic: expect.objectContaining({ queries: [expect.objectContaining({ name: 'duration_average', language: 'esql', statistic: 'average', sum_column: 'duration_sum', count_column: 'observations', timestamp_column: 'bucket', bucket_seconds: 10, esql: expect.stringContaining('FROM logs-*,traces-*') })] }) }))
  await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Add ES|QL exact-window p99 template')!.click())
  expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ elastic: expect.objectContaining({ queries: [expect.anything(), expect.objectContaining({ exact_window: true, approximate: true, statistic: 'p99' })] }) }))
})

it.each(['connection', 'indices', 'mapping', 'query', 'window'])('invalidates a completed preview when its %s scope changes', async scope => {
  const source: MonitorSource = { id: 'source', name: 'Logs', revision: 1, collector_id: 'agent', connection_id: 'connection', interval_seconds: 10, pack: 'elastic', enabled: true, elastic: { ...emptyElasticSelection('p'), indices: ['logs-*'], queries: [{ name: 'q', language: 'dsl', kind: 'log', dsl: { match_all: {} } }] } }
  api.inspectELKConnection.mockResolvedValue({ id: 'op', connection_id: 'connection', status: 'complete', kind: 'preview' })
  await act(async () => root.render(<ElasticSourceFields projectId="p" source={source} onChange={vi.fn()} />))
  await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Preview Elastic source')!.click())
  expect(container.querySelector('[aria-label="Elastic inspection result"]')).not.toBeNull()
  if (scope === 'window') {
    const input = Array.from(container.querySelectorAll('label')).find(label => label.textContent === 'Elastic preview from')!.querySelector('input')!
    await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, '2026-01-01T00:00'); input.dispatchEvent(new Event('input', { bubbles: true })) })
  } else {
    const next = scope === 'connection' ? { ...source, connection_id: '' } : { ...source, elastic: { ...source.elastic!, ...(scope === 'indices' ? { indices: ['other-*'] } : scope === 'mapping' ? { timestamp_field: 'event.time' } : { queries: [{ name: 'changed', language: 'dsl' as const, kind: 'log', dsl: { term: { service: 'checkout' } } }] }) } }
    await act(async () => root.render(<ElasticSourceFields projectId="p" source={next} onChange={vi.fn()} />))
  }
  expect(container.querySelector('[aria-label="Elastic inspection result"]')).toBeNull()
})

it('cancels obsolete preview work and preserves index discovery while selecting an index', async () => {
  const source: MonitorSource = { id: 'source', name: 'Logs', revision: 1, collector_id: 'agent', connection_id: 'connection', interval_seconds: 10, pack: 'elastic', enabled: true, elastic: { ...emptyElasticSelection('p'), indices: ['logs-*'] } }
  api.inspectELKConnection.mockResolvedValueOnce({ id: 'op', connection_id: 'connection', status: 'pending', kind: 'preview' }).mockResolvedValueOnce({ id: 'indices', connection_id: 'connection', status: 'complete', kind: 'discover_indices', result: { indices: [{ name: 'another', type: 'index' }] } })
  await act(async () => root.render(<ElasticSourceFields projectId="p" source={source} onChange={vi.fn()} />))
  await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Preview Elastic source')!.click())
  await act(async () => root.render(<ElasticSourceFields projectId="p" source={{ ...source, elastic: { ...source.elastic!, indices: ['next-*'] } }} onChange={vi.fn()} />))
  expect(api.cancelELKInspection).toHaveBeenCalledWith('p', 'connection', 'op')
  await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Discover Elastic indices')!.click())
  await act(async () => root.render(<ElasticSourceFields projectId="p" source={{ ...source, elastic: { ...source.elastic!, indices: ['another'] } }} onChange={vi.fn()} />))
  expect(container.querySelector('[aria-label="Discovered indices"]')?.textContent).toContain('another')
})
