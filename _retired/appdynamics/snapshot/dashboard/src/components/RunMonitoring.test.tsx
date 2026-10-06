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

it('lets operators select individual entity series instead of merging wildcard metric results', async () => {
  request.mockImplementation(async (path: string) => {
    if (path.includes('capabilities')) return { monitoring_enabled: true, diagnostics_enabled: false }
    if (path.endsWith('/monitors')) return [{ id: 'run:apm', source: { id: 'apm', name: 'APM', pack: 'appdynamics' }, state: 'stopped', next_sequence: 1 }]
    if (path.includes('monitor-evidence')) return { evidence: [], has_more: false, after_key: '' }
    if (path.includes('monitor-series')) return { points: [1, 2].map(node => ({ source: 'apm', metric_name: 'cpu', value: node * 5, unit: '%', quality: 'valid', timestamp_utc: '2026-10-03T12:00:00Z', dimensions: { node: String(node) } })), has_more: false }
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
})

it('requires at least one selected AppDynamics collection capability before saving', async () => {
  request.mockImplementation(async (path: string) => {
    if (path.includes('capabilities')) return { monitoring_enabled: true }
    if (path.includes('appdynamics-connections')) return { connections: [{ id: 'connection', name: 'APM', enabled: true, revision: 1, verified_revision: 1, collection_mode: 'direct' }] }
    if (path.includes('collectors?')) return { collectors: [] }
    return []
  })
  await act(async () => root.render(<RunMonitorSources projectId="p" canManage />))
  const pack = container.querySelectorAll('select')[1]
  await act(async () => { pack.value = 'appdynamics'; pack.dispatchEvent(new Event('change', { bubbles: true })) })
  const changeInput = async (label: string, value: string) => {
    const input = Array.from(container.querySelectorAll('label')).find(item => item.textContent?.startsWith(label))!.querySelector('input')!
    await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })) })
  }
  await changeInput('Name', 'APM source'); await changeInput('Application IDs', '123')
  const connection = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith('AppDynamics connection'))!.querySelector('select')!
  await act(async () => { connection.value = 'connection'; connection.dispatchEvent(new Event('change', { bubbles: true })) })
  const save = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save monitor source')!
  expect(save.disabled).toBe(false)
  for (const name of ['Capture events', 'Capture health rule violations']) {
    const input = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.trim() === name)!.querySelector('input')!
    await act(async () => input.click())
  }
  expect(save.disabled).toBe(true)
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
