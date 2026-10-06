// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { RunMonitorSources, ScenarioMonitorBindings, RunMonitorTimeline } from './RunMonitoring'
import type { AnalysisPoint } from '../types/analysis'

const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request }))
vi.mock('./MonitorTimelinePlot', () => ({ default: ({ points }: { points: AnalysisPoint[] }) => <div data-chart-node={points[0]?.dimensions?.node} /> }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLElement; let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); request.mockReset() })
afterEach(() => { act(() => root.unmount()); container.remove(); vi.useRealTimers() })

it('saves revisioned scenario bindings with required readiness', async () => {
  request.mockImplementation(async (path: string, opts?: { method?: string; body?: string }) => {
    if (path.includes('capabilities')) return { monitoring_enabled: true }
    if (path.includes('monitor-sources')) return [{ id: 'source', name: 'Linux host', pack: 'linux-hosts' }]
    if (opts?.method === 'PUT') return { ...JSON.parse(opts.body!), revision: 8 }
    return { revision: 7, sources: [{ source_id: 'source', required: false }] }
  })
  await act(async () => root.render(<ScenarioMonitorBindings projectId="p" scenarioId="scenario" canEdit />))
  const required = container.querySelectorAll<HTMLInputElement>('input[type=checkbox]')[1]
  await act(async () => required.click())
  await act(async () => container.querySelector('button')!.click())
  expect(request).toHaveBeenCalledWith('/scenarios/scenario/monitor-bindings', expect.objectContaining({ method: 'PUT', body: JSON.stringify({ revision: 7, sources: [{ source_id: 'source', required: true }] }) }))
  expect(container.textContent).toContain('Monitor bindings saved.')
})

it('exposes credential enrollment only for remote adapters', async () => {
  request.mockImplementation(async (path: string) => path.includes('capabilities') ? { monitoring_enabled: true } : path.includes('collectors?') ? { collectors: [] } : [])
  await act(async () => root.render(<RunMonitorSources projectId="p" canManage />))
  const pack = container.querySelectorAll('select')[1]
  await act(async () => { pack.value = 'ssh-linux'; pack.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(container.textContent).toContain('Collector X25519 public key')
  expect(container.textContent).toContain('Project credential secret name')
  await act(async () => { pack.value = 'otel-metrics'; pack.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(container.textContent).not.toContain('Collector X25519 public key')
})


it('preserves an existing management pack and clears its identity when creating a new source or switching projects', async () => {
  const scope = { version: 1, kind: 'kafka_offsets', provider: 'broker', isolation: 'read_uncommitted', entities: [{ kafka: { topic: 'orders', partition: 0, group: 'workers' } }] }
  const source = { id: 'managed', project_id: 'a', name: 'Approved offsets', revision: 4, collector_id: 'collector', pack: 'kafka-offsets', interval_seconds: 10, enabled: true, destination: 'management://broker', management_scope: scope, management_identity: { mode: 'anonymous', principal: 'approved', policy_revision: 'v1' } }
  request.mockImplementation(async (path: string) => {
    if (path.includes('capabilities')) return { monitoring_enabled: true }
    if (path.includes('collectors?')) return { collectors: [] }
    if (path.includes('management-state')) return { state: 'unavailable' }
    return path.includes('/projects/a/') ? [source] : []
  })
  const click = async (label: string) => act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === label)!.click())
  await act(async () => root.render(<RunMonitorSources projectId="a" canManage />))
  await click('Edit Approved offsets')
  const pack = container.querySelectorAll('select')[1]
  expect(pack.value).toBe('kafka-offsets')
  expect(container.querySelector<HTMLInputElement>('label input[value="management://broker"]')?.value).toBe('management://broker')
  await click('New source')
  expect(pack.value).toBe('linux-hosts')
  expect(container.textContent).not.toContain('Management collection ownership')
  await click('Edit Approved offsets')
  await act(async () => root.render(<RunMonitorSources projectId="b" canManage />))
  expect(container.querySelector<HTMLInputElement>('input')?.value).toBe('')
  expect(container.querySelectorAll('select')[1].value).toBe('linux-hosts')
  expect(container.textContent).not.toContain('Approved offsets')
  expect(container.textContent).not.toContain('Management collection ownership')
})


it('offers only executable enabled management packs and sends the separate pinned identity', async () => {
  const scope = { version: 1, kind: 'kafka_offsets', provider: 'cluster-id', isolation: 'read_committed', entities: [{ kafka: { topic: 'orders', partition: 0, group: 'workers' } }] }
  const source = { id: 'managed', project_id: 'p', name: 'Approved offsets', revision: 4, collector_id: 'collector', pack: 'kafka-offsets', interval_seconds: 10, enabled: true, destination: 'management://broker', collector_public_key: 'public-key', management_scope: scope, management_identity: { mode: 'sasl_plain', principal: 'reader', policy_revision: 'v1', credential_ref: 'management-secret', credential_version: 'saved-version' } }
  request.mockImplementation(async (path: string, options?: { method?: string; body?: string }) => {
    if (path.includes('capabilities')) return { monitoring_enabled: true, messaging_management_enabled: true, messaging_management_packs: ['kafka-offsets', 'unimplemented-pack'] }
    if (path.includes('collectors?')) return { collectors: [{ id: 'collector', name: 'Collector', health: 'healthy' }] }
    if (path.includes('management-state')) return { state: 'unavailable' }
    if (options?.method === 'PUT') return { ...JSON.parse(options.body!), revision: 5 }
    return [source]
  })
  await act(async () => root.render(<RunMonitorSources projectId="p" canManage />))
  const pack = container.querySelectorAll('select')[1]
  expect(Array.from(pack.options).map(option => option.value)).toContain('kafka-offsets')
  expect(Array.from(pack.options).map(option => option.value)).not.toContain('unimplemented-pack')
  await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Edit Approved offsets')!.click())
  const label = Array.from(container.querySelectorAll('label')).find(element => element.textContent?.startsWith('Management credential project-secret reference'))!
  await act(async () => { const input = label.querySelector('input')!; Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'rotated-secret'); input.dispatchEvent(new Event('input', { bubbles: true })); input.dispatchEvent(new Event('change', { bubbles: true })) })
  await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save monitor source')!.click())
  const mutation = request.mock.calls.find(([, options]) => options?.method === 'PUT')!
  const payload = JSON.parse(mutation[1].body)
  expect(payload.management_identity).toEqual({ mode: 'sasl_plain', principal: 'reader', policy_revision: 'v1', credential_ref: 'rotated-secret' })
  expect(payload.credential_ref).toBeUndefined()
  expect(payload.management_scope).toEqual(scope)
})

it('does not offer management creation when the project flag or executable inventory is absent', async () => {
  for (const capability of [{ messaging_management_enabled: false, messaging_management_packs: ['kafka-offsets'] }, { messaging_management_enabled: true, messaging_management_packs: [] }]) {
    request.mockImplementation(async (path: string) => path.includes('capabilities') ? { monitoring_enabled: true, ...capability } : path.includes('collectors?') ? { collectors: [] } : [])
    await act(async () => root.render(<RunMonitorSources projectId={JSON.stringify(capability)} canManage />))
    expect(Array.from(container.querySelectorAll('select')[1].options).map(option => option.value)).not.toContain('kafka-offsets')
  }
})

it('lets operators select individual entity series instead of merging wildcard metric results', async () => {
  request.mockImplementation(async (path: string) => {
    if (path.includes('capabilities')) return { monitoring_enabled: true, diagnostics_enabled: false }
    if (path.endsWith('/monitors')) return [{ id: 'run:apm', source: { id: 'apm', name: 'APM', pack: 'appdynamics' }, state: 'stopped', next_sequence: 1 }]
    if (path.includes('monitor-evidence')) return { evidence: [], has_more: false, after_key: '' }
    if (path.includes('monitor-series')) return { points: [1, 2].map(node => ({ source: 'apm', metric_name: 'cpu', value: node * 5, unit: '%', quality: 'valid', timestamp_utc: '2026-10-03T12:00:00Z', dimensions: { node: String(node) }, provider: { name: 'appdynamics', statistic: 'value', resolution_seconds: 60, settled: true, bucket_start: '2026-10-03T12:00:00Z', bucket_end: '2026-10-03T12:01:00Z' } })), has_more: false }
    return []
  })
  await act(async () => root.render(<RunMonitorTimeline projectId="p" testId="run" history={[]} />))
  const source = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith('Monitor source'))!.querySelector('select')!
  await act(async () => { source.value = 'apm'; source.dispatchEvent(new Event('change', { bubbles: true })) })
  const metric = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith('Metric name'))!.querySelector('input')!
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(metric, 'cpu'); metric.dispatchEvent(new Event('input', { bubbles: true })) })
  await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Refresh monitor overlay')!.click())
  const series = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith('Observed monitor series'))!.querySelector('select')!
  expect(series.options).toHaveLength(2); expect(container.querySelector('[data-chart-node]')?.getAttribute('data-chart-node')).toBe('1')
  await act(async () => { series.value = series.options[1].value; series.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(container.querySelector('[data-chart-node]')?.getAttribute('data-chart-node')).toBe('2')
  expect(container.textContent).toContain('1 valid / 1 observed samples in the selected series')
  expect(container.textContent).toContain('resolution 60 seconds')
})


it('refreshes a loaded canonical series during reconciliation and stops after collection closes', async () => {
  vi.useFakeTimers()
  let state = 'reconciling'
  request.mockImplementation(async (path: string) => {
    if (path.includes('capabilities')) return { monitoring_enabled: true, diagnostics_enabled: false }
    if (path.endsWith('/monitors')) return [{ id: 'run:apm', source: { id: 'apm', name: 'APM', pack: 'appdynamics' }, state, next_sequence: 1 }]
    if (path.includes('monitor-evidence')) return { evidence: [], has_more: false, after_key: '' }
    if (path.includes('monitor-series')) return { points: [], has_more: false }
    return []
  })
  await act(async () => root.render(<RunMonitorTimeline projectId="p" testId="run" history={[]} />))
  const source = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith('Monitor source'))!.querySelector('select')!
  await act(async () => { source.value = 'apm'; source.dispatchEvent(new Event('change', { bubbles: true })) })
  const metric = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith('Metric name'))!.querySelector('input')!
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(metric, 'cpu'); metric.dispatchEvent(new Event('input', { bubbles: true })) })
  await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Refresh monitor overlay')!.click())
  const seriesRequests = () => request.mock.calls.filter(([path]) => path.includes('monitor-series')).length
  expect(seriesRequests()).toBe(1)
  await act(async () => vi.advanceTimersByTimeAsync(60000))
  expect(seriesRequests()).toBe(2)
  state = 'stopped'
  await act(async () => vi.advanceTimersByTimeAsync(10000))
  await act(async () => vi.advanceTimersByTimeAsync(60000))
  expect(seriesRequests()).toBe(2)
})


it('preserves existing cloud polling minimums and retained intervals when switching packs', async () => {
  request.mockImplementation(async (path: string) => path.includes('capabilities') ? { monitoring_enabled: true } : path.includes('collectors?') ? { collectors: [] } : [])
  await act(async () => root.render(<RunMonitorSources projectId="p" canManage />))
  const pack = container.querySelectorAll('select')[1]
  const interval = container.querySelector<HTMLInputElement>('input[type=number]')!
  await act(async () => { pack.value = 'cloudwatch'; pack.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(interval.value).toBe('60')
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(interval, '120'); interval.dispatchEvent(new Event('input', { bubbles: true })) })
  for (const value of ['azure-monitor', 'linux-hosts']) {
    await act(async () => { pack.value = value; pack.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(interval.value).toBe('120')
  }
})

it('keeps provider sources available beside management packs and clears management approval on a provider switch', async () => {
  const source = { id: 'managed', project_id: 'p', name: 'Combined monitoring', revision: 4, collector_id: 'collector', pack: 'kafka-offsets', interval_seconds: 10, enabled: true, destination: 'management://broker', collector_public_key: 'public-key', management_scope: { version: 1, kind: 'kafka_offsets', provider: 'cluster-id', isolation: 'read_committed', entities: [{ kafka: { topic: 'orders', partition: 0, group: 'workers' } }] }, management_identity: { mode: 'sasl_plain', principal: 'reader', policy_revision: 'v1', credential_ref: 'management-secret' } }
  request.mockImplementation(async (path: string, options?: { method?: string; body?: string }) => {
    if (path.includes('capabilities')) return { monitoring_enabled: true, messaging_management_enabled: true, messaging_management_packs: ['kafka-offsets'] }
    if (path.includes('appdynamics-connections')) return { connections: [{ id: 'connection', name: 'APM', enabled: true, revision: 1, verified_revision: 1, collection_mode: 'direct' }] }
    if (path.includes('collectors?')) return { collectors: [{ id: 'collector', name: 'Collector', health: 'healthy' }] }
    if (path.includes('management-state')) return { state: 'unavailable' }
    if (options?.method === 'PUT') return { ...JSON.parse(options.body!), revision: 5 }
    return [source]
  })
  await act(async () => root.render(<RunMonitorSources projectId="p" canManage />))
  const pack = container.querySelectorAll('select')[1]
  expect(Array.from(pack.options).map(option => option.value)).toEqual(expect.arrayContaining(['dynatrace', 'prometheus-query', 'kafka-offsets']))
  await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Edit Combined monitoring')!.click())
  await act(async () => { pack.value = 'dynatrace'; pack.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(container.querySelectorAll('select')[0].disabled).toBe(true)
  expect(container.textContent).not.toContain('Management collection ownership')
  expect(container.textContent).not.toContain('Approved destination')
  expect(Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith('Collection interval (seconds)'))!.querySelector<HTMLInputElement>('input[type=number]')!.value).toBe('60')
  expect(Array.from(pack.options).map(option => option.value)).not.toContain('appdynamics')
})

it('clears provider connection fields when switching back to a host collection pack', async () => {
  const source = { id: 'provider', project_id: 'p', name: 'Prometheus source', revision: 3, collector_id: 'collector', pack: 'prometheus-query', interval_seconds: 30, enabled: true, connection_id: 'connection', executor_mode: 'collector', prometheus: { queries: [], settle_seconds: 30 } }
  request.mockImplementation(async (path: string, options?: { method?: string; body?: string }) => {
    if (path.includes('capabilities')) return { monitoring_enabled: true }
    if (path.includes('prometheus-connections')) return { connections: [] }
    if (path.includes('collectors?')) return { collectors: [{ id: 'collector', name: 'Collector', health: 'healthy' }] }
    if (options?.method === 'PUT') return { ...JSON.parse(options.body!), revision: 4 }
    return [source]
  })
  await act(async () => root.render(<RunMonitorSources projectId="p" canManage />))
  await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Edit Prometheus source')!.click())
  const pack = container.querySelectorAll('select')[1]
  await act(async () => { pack.value = 'linux-hosts'; pack.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(container.querySelectorAll('select')[0].disabled).toBe(false)
  const save = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save monitor source')!
  expect(save.disabled).toBe(false)
  await act(async () => save.click())
  const payload = JSON.parse(request.mock.calls.find(([, options]) => options?.method === 'PUT')![1].body)
  expect(payload.pack).toBe('linux-hosts')
  for (const field of ['connection_id', 'executor_mode', 'prometheus', 'appdynamics', 'management_scope', 'management_identity']) expect(payload[field]).toBeUndefined()
})

it('keeps exact management evidence available when generic run monitoring is disabled', async () => {
  request.mockImplementation(async (path: string) => path.includes('capabilities') ? { monitoring_enabled: false, messaging_management_version: 1 } : [])
  await act(async () => root.render(<RunMonitorTimeline projectId="p" testId="run" history={[]} />))
  expect(container.querySelector('[aria-label="Messaging management evidence"]')).not.toBeNull()
  expect(container.textContent).toContain('Read management evidence')
  expect(request.mock.calls.some(([path]) => path.endsWith('/monitors'))).toBe(false)
})
