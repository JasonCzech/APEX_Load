// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import AppDynamicsMetricPresets from './AppDynamicsMetricPresets'
import { emptyAppDynamicsSelection } from './appDynamicsConnectionModel'
const api = vi.hoisted(() => ({ discoverAppDynamicsEntities: vi.fn(), getAppDynamicsOperation: vi.fn() }))
vi.mock('../hooks/api/useAppDynamicsConnectionsApi', () => ({ useAppDynamicsConnectionsApi: () => api }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
it('adds only available discovered preset paths and shows truncation', async () => {
  const container = document.createElement('div'); document.body.appendChild(container); const root = createRoot(container)
  const onChange = vi.fn()
  api.discoverAppDynamicsEntities.mockResolvedValue({ id: 'op', status: 'complete', result: { truncated: true, entities: [{ id: '8', name: 'Average Response Time (ms)', type: 'leaf', path: 'Overall Application Performance|Average Response Time (ms)' }, { name: 'Errors per Minute', type: 'leaf', path: 'not-a-response-metric' }] } })
  try {
    await act(async () => root.render(<AppDynamicsMetricPresets projectId="p" connectionId="conn" selection={{ ...emptyAppDynamicsSelection(), applications: ['123'] }} onChange={onChange} />))
    await act(async () => container.querySelector('button')!.click())
    expect(api.discoverAppDynamicsEntities).toHaveBeenCalledWith('p', 'conn', 'metrics', { application_id: '123', metric_path: 'Overall Application Performance' })
    expect(container.textContent).toContain('Preset results are truncated')
    expect(container.textContent).not.toContain('Add Errors per Minute')
    await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Add Average Response Time (ms)')!.click())
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ metrics: [{ application_id: '123', path: 'Overall Application Performance|Average Response Time (ms)', name: 'response_time', unit: 'ms', statistic: 'value' }] }))
  } finally { act(() => root.unmount()); container.remove() }
})
