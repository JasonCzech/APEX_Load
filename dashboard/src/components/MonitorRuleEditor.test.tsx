// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import MonitorRuleEditor from './MonitorRuleEditor'
import type { MonitorRule } from '../types/runMonitoring'
const request = vi.hoisted(() => vi.fn())
const api = vi.hoisted(() => ({ createDynatraceOperation: vi.fn(), getDynatraceOperation: vi.fn() }))
vi.mock('../hooks/apiClient', () => ({ request }))
vi.mock('../hooks/api/useDynatraceConnectionsApi', () => ({ useDynatraceConnectionsApi: () => api }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
it('selects an exact native Dynatrace series from preview without combining entity/resolution identities', async () => {
  const rule: MonitorRule = { source_id: 'source', metric: 'cpu', unit: '%', statistic: 'avg', comparator: 'lte', threshold: 80, severity: 'fail', min_coverage_pct: 95, start_offset_seconds: 0, evaluation_operation: 'time_average' }
  const source = { id: 'source', name: 'CPU', pack: 'dynatrace', enabled: true, connection_id: 'connection', dynatrace: { metrics: [{ name: 'cpu', metric_selector: 'builtin:host.cpu.usage', aggregation: 'avg', unit: '%', semantics: 'gauge' }] } }
  request.mockResolvedValue([source])
  const meta = { name: 'dynatrace', backend: 'classic', metric_id: 'metrics:fixture', entity_ids: { host: 'HOST-0123456789ABCDEF' }, statistic: 'time_average', resolution_seconds: 60 }
  const point = { source: 'source', metric_name: 'cpu', unit: '%', value: 0, quality: 'valid', dimensions: meta.entity_ids, timestamp_utc: '2026-10-03T12:00:00Z', provider: meta }
  api.createDynatraceOperation.mockResolvedValue({ id: 'op', kind: 'preview', status: 'complete', result: { preview: { points: [point, { ...point, timestamp_utc: '2026-10-03T12:01:00Z' }] } } })
  let selected: MonitorRule[] = []
  function Wrapper() { const [rules, setRules] = useState([rule]); selected = rules; return <MonitorRuleEditor projectId="project" value={rules} onChange={setRules} /> }
  const container = document.createElement('div'); document.body.appendChild(container); const root = createRoot(container)
  try {
    await act(async () => root.render(<Wrapper />))
    expect(container.textContent).toContain('Monitoring performance criteria')
    await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Preview exact series for criterion 1')!.click())
    const buttons = [...container.querySelectorAll('button')].filter(button => button.textContent?.startsWith('Use cpu'))
    expect(buttons).toHaveLength(1)
    await act(async () => buttons[0].click())
    expect(selected[0]).toMatchObject({ provider: 'dynatrace', backend: 'classic', metric_id: 'metrics:fixture', source_statistic: 'time_average', resolution_seconds: 60, unit: '%', entity_ids: meta.entity_ids, dimensions: meta.entity_ids })
    expect(api.createDynatraceOperation).toHaveBeenCalledWith('project', 'connection', { kind: 'preview', source_id: source.id })
    const metric = [...container.querySelectorAll('label')].find(label => label.textContent?.startsWith('Criterion 1 metric'))!.querySelector('select')!
    await act(async () => { metric.value = ''; metric.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(selected[0].metric_id).toBeUndefined()
    expect(selected[0].entity_ids).toBeUndefined()
    expect(selected[0].dimensions).toBeUndefined()
    expect(selected[0].evaluation_operation).toBeUndefined()
  } finally { act(() => root.unmount()); container.remove(); vi.clearAllMocks() }
})

it('selects declared whole-window Grail catalog metadata without freezing preview resolution', async () => {
  const rule: MonitorRule = { source_id: 'source', metric: 'errors', unit: 'count', statistic: 'sum', comparator: 'lte', threshold: 1, severity: 'fail', min_coverage_pct: 95, start_offset_seconds: 0 }
  const selection = { metrics: [], grail: [{ name: 'errors', kind: 'metric', query: 'fetch logs | summarize value=count()', unit: 'count', statistic: 'sum', value_field: 'value', bucket_seconds: 0 }], problems: false, events: false, settle_seconds: 120 }
  request.mockResolvedValue([{ id: 'source', name: 'Errors', pack: 'dynatrace', enabled: true, connection_id: 'connection', dynatrace: selection }])
  api.createDynatraceOperation.mockResolvedValue({ id: 'op', kind: 'preview', status: 'complete', result: { preview: { points: [{ source: 'source', metric_name: 'errors', unit: 'count', value: 3, quality: 'valid', timestamp_utc: '2026-10-03T12:00:00Z', provider: { name: 'dynatrace', backend: 'grail', metric_id: 'grail:catalog', statistic: 'sum', resolution_seconds: 300, query_window: true } }] } } })
  let selected: MonitorRule[] = []
  function Wrapper() { const [rules, setRules] = useState([rule]); selected = rules; return <MonitorRuleEditor projectId="project" value={rules} onChange={setRules} /> }
  const container = document.createElement('div'); document.body.appendChild(container); const root = createRoot(container)
  try {
    await act(async () => root.render(<Wrapper />))
    await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Preview exact series for criterion 1')!.click())
    const choice = [...container.querySelectorAll('button')].find(button => button.textContent?.startsWith('Use errors'))!
    expect(choice.textContent).toContain('query window')
    await act(async () => choice.click())
    expect(selected[0]).toMatchObject({ provider: 'dynatrace', backend: 'grail', metric_id: 'grail:catalog', unit: 'count', source_statistic: 'sum' })
    expect(selected[0].resolution_seconds).toBeUndefined()
  } finally { act(() => root.unmount()); container.remove(); vi.clearAllMocks() }
})

it('fences outstanding preview creation when switching projects and does not retain the old catalog', async () => {
  const rule: MonitorRule = { source_id: 'source', metric: 'cpu', unit: '%', statistic: 'avg', comparator: 'lte', threshold: 80, severity: 'fail', min_coverage_pct: 95, start_offset_seconds: 0 }
  const source = { id: 'source', name: 'Old project CPU', pack: 'dynatrace', enabled: true, connection_id: 'connection', dynatrace: { metrics: [{ name: 'cpu', metric_selector: 'builtin:host.cpu.usage', aggregation: 'avg', unit: '%' }] } }
  let finish!: (operation: unknown) => void
  api.createDynatraceOperation.mockReturnValue(new Promise(resolve => { finish = resolve }))
  request.mockResolvedValueOnce([source]).mockRejectedValueOnce(new Error('New project sources unavailable'))
  const container = document.createElement('div'); document.body.appendChild(container); const root = createRoot(container)
  try {
    await act(async () => root.render(<MonitorRuleEditor projectId="old-project" value={[rule]} onChange={() => {}} />))
    const previewButton = [...container.querySelectorAll('button')].find(button => button.textContent === 'Preview exact series for criterion 1')!
    await act(async () => previewButton.click())
    expect(previewButton.disabled).toBe(true)
    await act(async () => root.render(<MonitorRuleEditor projectId="new-project" value={[rule]} onChange={() => {}} />))
    expect(container.textContent).not.toContain('Old project CPU')
    expect(container.textContent).toContain('Unavailable source — review required')
    await act(async () => finish({ id: 'old-op', status: 'complete', message: 'Old project preview', result: { preview: { points: [] } } }))
    expect(container.textContent).not.toContain('Old project preview')
    expect(container.querySelector('[role="status"]')).toBeNull()
    expect(api.getDynatraceOperation).not.toHaveBeenCalled()
  } finally { act(() => root.unmount()); container.remove(); vi.clearAllMocks() }
})

it('does not let an older project preview replace a newer preview or its error state', async () => {
  const rule: MonitorRule = { source_id: 'source', metric: 'cpu', unit: '%', statistic: 'avg', comparator: 'lte', threshold: 80, severity: 'fail', min_coverage_pct: 95, start_offset_seconds: 0 }
  const source = { id: 'source', name: 'CPU', pack: 'dynatrace', enabled: true, connection_id: 'connection', dynatrace: { metrics: [{ name: 'cpu', metric_selector: 'builtin:host.cpu.usage', aggregation: 'avg', unit: '%' }] } }
  let rejectOld!: (error: Error) => void
  api.createDynatraceOperation.mockReturnValueOnce(new Promise((_, reject) => { rejectOld = reject })).mockResolvedValueOnce({ id: 'new-op', status: 'complete', message: 'Current project preview' })
  request.mockResolvedValue([source])
  const container = document.createElement('div'); document.body.appendChild(container); const root = createRoot(container)
  const clickPreview = () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Preview exact series for criterion 1')!.click()
  try {
    await act(async () => root.render(<MonitorRuleEditor projectId="old-project" value={[rule]} onChange={() => {}} />))
    await act(async () => clickPreview())
    await act(async () => root.render(<MonitorRuleEditor projectId="new-project" value={[rule]} onChange={() => {}} />))
    await act(async () => clickPreview())
    await act(async () => rejectOld(new Error('Obsolete preview failure')))
    expect(container.textContent).toContain('Current project preview')
    expect(container.textContent).not.toContain('Obsolete preview failure')
    expect([...container.querySelectorAll('button')].find(button => button.textContent === 'Preview exact series for criterion 1')!.disabled).toBe(false)
  } finally { act(() => root.unmount()); container.remove(); vi.clearAllMocks() }
})

it.each(['success', 'error'])('ignores an obsolete operation poll %s after a new preview starts', async outcome => {
  vi.useFakeTimers()
  const rule: MonitorRule = { source_id: 'source', metric: 'cpu', unit: '%', statistic: 'avg', comparator: 'lte', threshold: 80, severity: 'fail', min_coverage_pct: 95, start_offset_seconds: 0 }
  const source = { id: 'source', name: 'CPU', pack: 'dynatrace', enabled: true, connection_id: 'connection', dynatrace: { metrics: [{ name: 'cpu', metric_selector: 'builtin:host.cpu.usage', aggregation: 'avg', unit: '%' }] } }
  let finishPoll!: () => void
  api.getDynatraceOperation.mockReturnValue(new Promise((resolve, reject) => { finishPoll = () => outcome === 'success' ? resolve({ id: 'old-op', status: 'complete', message: 'Obsolete poll result' }) : reject(new Error('Obsolete poll error')) }))
  api.createDynatraceOperation.mockResolvedValueOnce({ id: 'old-op', status: 'running' }).mockResolvedValueOnce({ id: 'new-op', status: 'complete', message: 'New preview result' })
  request.mockResolvedValue([source])
  const container = document.createElement('div'); document.body.appendChild(container); const root = createRoot(container)
  const clickPreview = () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Preview exact series for criterion 1')!.click()
  try {
    await act(async () => root.render(<MonitorRuleEditor projectId="project" value={[rule]} onChange={() => {}} />))
    await act(async () => clickPreview())
    await act(async () => { await vi.advanceTimersByTimeAsync(1500) })
    expect(api.getDynatraceOperation).toHaveBeenCalledWith('project', 'connection', 'old-op', expect.objectContaining({ signal: expect.any(AbortSignal) }))
    await act(async () => clickPreview())
    expect(api.getDynatraceOperation.mock.calls[0][3].signal.aborted).toBe(true)
    await act(async () => finishPoll())
    expect(container.textContent).toContain('New preview result')
    expect(container.textContent).not.toContain('Obsolete poll')
  } finally { act(() => root.unmount()); container.remove(); vi.clearAllMocks(); vi.useRealTimers() }
})

it('distinguishes grid quantile estimates from totals and disables raw counter rules', async () => {
  request.mockResolvedValue([{ id: 'prom', name: 'Target Prometheus', pack: 'prometheus-query', enabled: true, prometheus: { queries: [
    { id: 'quantile', name: 'request_p95', unit: 'seconds', semantics: 'quantile' },
    { id: 'counter', name: 'request_counter', unit: 'count', semantics: 'counter' },
    { id: 'total', name: 'request_total', unit: 'count', semantics: 'count', terminal_expression: 'sum(increase(requests_total[{{window.duration}}]))' },
    { id: 'cpu', name: 'cpu', unit: '%', semantics: 'gauge' },
  ] } }])
  const container = document.createElement('div'); document.body.appendChild(container); const root = createRoot(container)
  function Editor() { const [rules, setRules] = useState<MonitorRule[]>([]); return <><MonitorRuleEditor projectId="p" value={rules} onChange={setRules} /><output>{JSON.stringify(rules)}</output></> }
  async function select(name: string, value: string) { const input = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith(name))!.querySelector('select')!; await act(async () => { Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(input, value); input.dispatchEvent(new Event('change', { bubbles: true })) }) }
  try {
    await act(async () => root.render(<Editor />))
    await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Add monitoring performance criterion')!.click())
    await select('Criterion 1 source', 'prom')
    const counter = Array.from(container.querySelectorAll('option')).find(option => option.value === 'request_counter')!
    expect(counter.disabled).toBe(true)
    await select('Criterion 1 metric', 'request_p95')
    expect(container.textContent).toContain('Mean of grid quantile estimates')
    await select('Criterion 1 metric', 'request_total')
    await select('Criterion 1 evaluation mode', 'exact_window')
    expect(JSON.parse(container.querySelector('output')!.textContent!)[0]).toMatchObject({ query_id: 'total', statistic: 'sum', evaluation_mode: 'exact_window' })
    await select('Criterion 1 metric', 'cpu')
    expect(JSON.parse(container.querySelector('output')!.textContent!)[0]).toMatchObject({ query_id: 'cpu', statistic: 'avg', evaluation_mode: 'series_points' })
    expect(Array.from(container.querySelectorAll('option')).find(option => option.value === 'sum')!.disabled).toBe(true)
  } finally { act(() => root.unmount()); container.remove(); vi.clearAllMocks() }
})
