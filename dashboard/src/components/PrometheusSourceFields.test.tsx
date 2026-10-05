// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { RunMonitorSources } from './RunMonitoring'
import PrometheusSourceFields from './PrometheusSourceFields'
import { emptyPrometheusQuery, emptyPrometheusSelection } from './prometheusConnectionModel'
const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLElement; let root: Root
beforeEach(() => { vi.clearAllMocks(); container = document.createElement('div'); root = createRoot(container); request.mockImplementation(async (path: string) => path.includes('capabilities') ? { monitoring_enabled: true } : path.includes('collectors?') ? { collectors: [{ id: 'collector', name: 'Private', health: 'healthy' }] } : path.includes('prometheus-connections') ? { connections: [{ id: 'c', name: 'Metrics', collection_mode: 'collector', collector_agent_id: 'collector', enabled: true, revision: 1, verified_revision: 1 }] } : []) })
afterEach(() => act(() => root.unmount()))
async function field(name: string, value: string) { const label = Array.from(container.querySelectorAll('label')).find(item => item.textContent?.startsWith(name))!; const input = label.querySelector('input,textarea,select')!; await act(async () => { const proto = input.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : input.tagName === 'SELECT' ? HTMLSelectElement.prototype : HTMLInputElement.prototype; Object.getOwnPropertyDescriptor(proto, 'value')!.set!.call(input, value); input.dispatchEvent(new Event(input.tagName === 'SELECT' ? 'change' : 'input', { bubbles: true })) }) }
it('requires typed queries and uses the saved assigned execution location', async () => {
  await act(async () => root.render(<RunMonitorSources projectId="p" canManage />)); await field('Pack', 'appdynamics')
  const interval = () => Array.from(container.querySelectorAll('label')).find(item => item.textContent?.startsWith('Collection interval (seconds)'))!.querySelector('input')!.value
  expect(interval()).toBe('60')
  await field('Pack', 'prometheus-query')
  expect(interval()).toBe('30')
  await field('Name', 'Target source'); await field('Prometheus connection', 'c')
  const save = Array.from(container.querySelectorAll('button')).find(item => item.textContent === 'Save monitor source')!; expect(save.disabled).toBe(true)
  await act(async () => Array.from(container.querySelectorAll('button')).find(item => item.textContent === 'Add Prometheus query')!.click())
  await field('Query 1 metric name', 'cpu'); await field('Query 1 expression', 'cpu_usage{job="checkout"}'); await field('Query 1 unit', '%'); await field('Query 1 allowed labels', 'job, instance'); expect(save.disabled).toBe(false)
  await act(async () => Array.from(container.querySelectorAll('button')).find(item => item.textContent === 'Add query 1 parameter')!.click())
  await field('Query 1 parameter 1 name', 'target'); await field('Query 1 parameter 1 value', 'checkout'); await field('Query 1 expression', 'cpu_usage{job={{param.target}}}')
  await act(async () => save.click())
  const payload = JSON.parse(request.mock.calls.find(([, options]) => options?.method === 'POST')![1].body)
  expect(payload).toMatchObject({ pack: 'prometheus-query', interval_seconds: 30, executor_mode: 'collector', collector_id: 'collector', connection_id: 'c', prometheus: { project_id: 'p', queries: [{ id: 'query_1', name: 'cpu', expression: 'cpu_usage{job={{param.target}}}', unit: '%', allowed_labels: ['job', 'instance'], parameters: { target: { type: 'label_value', value: 'checkout' } } }] } })
})
it('keeps populated query names stable and exposes helper text as descriptions', async () => {
  const query = { ...emptyPrometheusQuery(1), name: 'cpu', expression: 'cpu_usage{job="checkout"}', freshness_expression: 'timestamp(cpu_usage{job="checkout"})', terminal_expression: 'max_over_time(cpu_usage{job="checkout"}[{{window.duration}}])' }
  const source = { id: 'source', name: 'Target source', revision: 2, collector_id: 'collector', pack: 'prometheus-query', connection_id: 'c', interval_seconds: 30, enabled: true, prometheus: { ...emptyPrometheusSelection(), queries: [query], targets: true, evidence_selector: '{job="checkout"}' } }
  await act(async () => root.render(<PrometheusSourceFields projectId="p" source={source} onChange={vi.fn()} />))
  expect(container.querySelector('textarea[aria-label="Query 1 expression"]')?.textContent).toBe(query.expression)
  expect(container.querySelector('textarea[aria-label="Query 1 source freshness expression (optional)"]')?.textContent).toBe(query.freshness_expression)
  const terminal = container.querySelector('textarea[aria-label="Query 1 terminal expression (optional)"]')!
  expect(container.querySelector(`[id="${terminal.getAttribute('aria-describedby')}"]`)?.textContent).toContain('exact evaluation window')
  const evidence = container.querySelector('input[aria-label="Diagnostic evidence selector"]')!
  expect(evidence.getAttribute('value')).toBe('{job="checkout"}')
  expect(container.querySelector(`[id="${evidence.getAttribute('aria-describedby')}"]`)?.textContent).toContain('Scope target and alert summaries')
  await act(async () => root.render(<PrometheusSourceFields projectId="p" source={{ ...source, prometheus: { ...source.prometheus, queries: [{ ...query, expression: 'cpu_usage{job="other"}' }] } }} onChange={vi.fn()} />))
  expect(container.querySelector('textarea[aria-label="Query 1 expression"]')?.textContent).toBe('cpu_usage{job="other"}')
})
