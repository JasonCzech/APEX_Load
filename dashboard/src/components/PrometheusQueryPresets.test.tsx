// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import PrometheusQueryPresets from './PrometheusQueryPresets'
const api = vi.hoisted(() => ({ inspectPrometheusConnection: vi.fn(), getPrometheusOperation: vi.fn() }))
vi.mock('../hooks/api/usePrometheusConnectionsApi', () => ({ usePrometheusConnectionsApi: () => api }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let root: Root; let container: HTMLElement
beforeEach(() => { vi.clearAllMocks(); container = document.createElement('div'); root = createRoot(container); api.inspectPrometheusConnection.mockResolvedValue({ id: 'metrics', status: 'complete', result: { entities: [{ name: 'node_cpu_seconds_total' }], truncated: true } }) })
afterEach(() => act(() => root.unmount()))
async function click(name: string) { await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === name)!.click()) }
it('only adds discovered scoped queries and preserves catalogue truncation', async () => {
  const onChange = vi.fn()
  await act(async () => root.render(<PrometheusQueryPresets projectId="p" connectionID="c" queries={[]} onChange={onChange} />))
  const add = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Add discovered preset query')!
  expect(add.disabled).toBe(true)
  await click('Discover preset metrics')
  expect(api.inspectPrometheusConnection).toHaveBeenCalledWith('p', 'c', 'discover', { kind: 'metrics' }, expect.objectContaining({ signal: expect.any(AbortSignal) }))
  expect(container.textContent).toContain('catalogue truncated')
  expect(add.disabled).toBe(true)
  const input = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith('Preset target job'))!.querySelector('input')!
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'checkout'); input.dispatchEvent(new Event('input', { bubbles: true })) })
  expect(add.disabled).toBe(false)
  await click('Add discovered preset query')
  expect(onChange).toHaveBeenCalledWith([expect.objectContaining({ id: 'host_cpu_percent', unit: '%', allowed_labels: ['job', 'instance'] })])
  expect(onChange.mock.calls[0][0][0].expression).toContain('job="checkout"')
  expect(Array.from(container.querySelectorAll('option')).find(option => option.value === 'host_memory_percent')!.disabled).toBe(true)
})
