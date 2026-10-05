// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import MonitorRuleEditor from './MonitorRuleEditor'
import type { MonitorRule } from '../types/appdynamics'
const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let root: Root; let container: HTMLElement
beforeEach(() => { container = document.createElement('div'); root = createRoot(container); request.mockResolvedValue([{ id: 'prom', name: 'Target Prometheus', pack: 'prometheus-query', enabled: true, prometheus: { queries: [
  { id: 'quantile', name: 'request_p95', unit: 'seconds', semantics: 'quantile' },
  { id: 'counter', name: 'request_counter', unit: 'count', semantics: 'counter' },
  { id: 'total', name: 'request_total', unit: 'count', semantics: 'count', terminal_expression: 'sum(increase(requests_total[{{window.duration}}]))' },
  { id: 'cpu', name: 'cpu', unit: '%', semantics: 'gauge' },
] } }]) })
afterEach(() => act(() => root.unmount()))
function Editor() { const [rules, setRules] = useState<MonitorRule[]>([]); return <><MonitorRuleEditor projectId="p" value={rules} onChange={setRules} /><output>{JSON.stringify(rules)}</output></> }
async function select(name: string, value: string) { const input = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith(name))!.querySelector('select')!; await act(async () => { Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(input, value); input.dispatchEvent(new Event('change', { bubbles: true })) }) }
it('distinguishes grid quantile estimates from totals and disables raw counter rules', async () => {
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
})
