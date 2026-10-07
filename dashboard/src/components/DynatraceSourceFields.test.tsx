// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { beforeEach, afterEach, expect, it, vi } from 'vitest'
import DynatraceSourceFields from './DynatraceSourceFields'
import { emptyDynatraceSelection } from '../types/dynatrace'
import type { MonitorSource } from '../types/runMonitoring'
const api = vi.hoisted(() => ({ listDynatraceConnections: vi.fn(), createDynatraceOperation: vi.fn(), getDynatraceOperation: vi.fn() }))
vi.mock('../hooks/api/useDynatraceConnectionsApi', () => ({ useDynatraceConnectionsApi: () => api }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLElement; let root: Root; let observed: MonitorSource
const initial: MonitorSource = { id: '', name: 'DT', revision: 0, pack: 'dynatrace', enabled: true, interval_seconds: 60, collector_id: '', dynatrace: { ...emptyDynatraceSelection(), problem_selector: 'status("OPEN")', event_selector: 'eventType("CUSTOM_ALERT")' } }
function Wrapper() { const [source, setSource] = useState(initial); observed = source; return <DynatraceSourceFields projectId="project" source={source} onChange={setSource} /> }
beforeEach(() => { api.listDynatraceConnections.mockResolvedValue({ connections: [{ id: 'direct', name: 'Direct', enabled: true, collection_mode: 'direct', revision: 1, verified_revision: 1 }, { id: 'agent', name: 'Collector', enabled: true, collection_mode: 'collector', collector_agent_id: 'approved-agent', revision: 1, verified_revision: 1 }] }); container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(() => { act(() => root.unmount()); container.remove(); vi.clearAllMocks() })
const select = async (name: string, value: string) => { const input = [...container.querySelectorAll('label')].find(label => label.textContent?.startsWith(name))!.querySelector('select')!; await act(async () => { input.value = value; input.dispatchEvent(new Event('change', { bubbles: true })) }) }
const click = async (name: string) => { await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === name)!.click()) }
it('takes the execution location from saved connection and preserves complete Grail/source selection', async () => {
  await act(async () => root.render(<Wrapper />)); await select('Dynatrace connection', 'direct')
  expect(observed.executor_mode).toBe('direct'); expect(observed.collector_id).toBe('')
  await click('Add Dynatrace metric'); await click('Add Grail query'); await select('Grail 1 result kind', 'trace'); await select('Topology discovery', 'smartscape')
  await select('Dynatrace connection', 'agent')
  expect(observed.executor_mode).toBe('collector'); expect(observed.collector_id).toBe('approved-agent')
  expect(observed.dynatrace?.metrics).toHaveLength(1); expect(observed.dynatrace?.grail).toHaveLength(1); expect(observed.dynatrace?.topology_mode).toBe('smartscape')
  expect(observed.dynatrace?.grail[0].kind).toBe('trace')
  expect(observed.dynatrace?.problem_selector).toBe('status("OPEN")')
  expect(observed.dynatrace?.event_selector).toBe('eventType("CUSTOM_ALERT")')
  expect(container.textContent).toContain('Grail 1 entity ID field'); expect(container.textContent).toContain('Grail 1 run ID field')
  expect(container.textContent).toContain('Problems/events/topology entity selector')
  expect(container.textContent).toContain("Set each metric's entity selector independently")
  await select('Grail 1 result kind', 'metric')
  expect(observed.dynatrace?.grail[0]).toMatchObject({ value_field: 'value', statistic: 'average', bucket_seconds: 60 })
})
it('keeps an enabled legacy connection unavailable until its revision is verified', async () => {
  api.listDynatraceConnections.mockResolvedValue({ connections: [{ id: 'legacy', name: 'Legacy', enabled: true, revision: 2, verified_revision: 1 }] })
  await act(async () => root.render(<Wrapper />))
  const option = container.querySelector<HTMLOptionElement>('option[value="legacy"]')!
  expect(option.disabled).toBe(true)
  expect(option.textContent).toContain('unverified revision')
})
it('uses native async preview and shows explicit incomplete coverage', async () => {
  api.createDynatraceOperation.mockResolvedValue({ id: 'preview', kind: 'preview', status: 'complete', result: { preview: { points: [], evidence: [], coverage: { logs: { status: 'partial', truncated: true, message: 'Scan budget exhausted' } } } } })
  await act(async () => root.render(<Wrapper />)); await select('Dynatrace connection', 'direct'); await click('Preview Dynatrace selection')
  expect(api.createDynatraceOperation).toHaveBeenCalledWith('project', 'direct', { kind: 'preview', selection: expect.objectContaining({ project_id: 'project', problems: true, metrics: [], grail: [] }) })
  expect(container.textContent).toContain('logs: partial · truncated · Scan budget exhausted')
  expect(container.textContent).toContain('Preview does not create test traffic')
})
it('preserves omitted false capabilities when editing a metrics-only API selection', async () => {
  const source = { ...initial, connection_id: 'direct', dynatrace: JSON.parse('{"metrics":[{"name":"cpu","metric_selector":"builtin:host.cpu.usage","aggregation":"avg"}]}') }
  let changed: MonitorSource | undefined
  await act(async () => root.render(<DynatraceSourceFields projectId="project" source={source} onChange={next => { changed = next }} />))
  const problems = [...container.querySelectorAll('label')].find(label => label.textContent?.includes('Capture problems'))!.querySelector<HTMLInputElement>('input')!
  expect(problems.checked).toBe(false)
  await click('Add Grail query')
  expect(changed?.dynatrace).toMatchObject({ problems: false, events: false, settle_seconds: 120, grail: [{ kind: 'log' }] })
})
it('holds the connection selection during inspection creation and discards a result after source replacement', async () => {
  let finish!: (value: unknown) => void
  api.createDynatraceOperation.mockReturnValue(new Promise(resolve => { finish = resolve }))
  const source = { ...initial, connection_id: 'direct' }
  await act(async () => root.render(<DynatraceSourceFields projectId="project" source={source} onChange={() => {}} />))
  await click('Preview Dynatrace selection')
  const connection = container.querySelector<HTMLSelectElement>('select')!
  expect(connection.disabled).toBe(true)
  await act(async () => root.render(<DynatraceSourceFields projectId="project" source={{ ...source, connection_id: 'agent' }} onChange={() => {}} />))
  expect(connection.disabled).toBe(false)
  await act(async () => finish({ id: 'old-connection-result', kind: 'preview', status: 'complete', result: { truncated: true } }))
  expect(container.querySelector('[aria-label="Dynatrace source inspection"]')).toBeNull()
  expect(connection.value).toBe('agent')
})
it.each(['classic', ''])('clears hidden Smartscape scope when changing topology to %s and preserves metric scope', async mode => {
  api.createDynatraceOperation.mockResolvedValue({ id: 'preview', kind: 'preview', status: 'complete' })
  function ScopedSource() {
    const [source, setSource] = useState<MonitorSource>({ ...initial, connection_id: 'direct', dynatrace: { ...emptyDynatraceSelection(), topology_mode: 'smartscape', smartscape_node_type: 'dt.entity.service', smartscape_entity_ids: ['opaque-service'], metrics: [{ name: 'cpu', metric_selector: 'builtin:host.cpu.usage', aggregation: 'avg', entity_selector: 'type(HOST)' }] } })
    observed = source
    return <DynatraceSourceFields projectId="project" source={source} onChange={setSource} />
  }
  await act(async () => root.render(<ScopedSource />))
  await select('Topology discovery', mode)
  await click('Preview Dynatrace selection')
  const selection = JSON.parse(JSON.stringify(api.createDynatraceOperation.mock.calls[0][2].selection))
  expect(selection.topology_mode).toBe(mode)
  expect(selection).not.toHaveProperty('smartscape_node_type')
  expect(selection).not.toHaveProperty('smartscape_entity_ids')
  expect(selection.metrics[0].entity_selector).toBe('type(HOST)')
  expect(container.textContent).not.toContain('Smartscape entity IDs')
})
