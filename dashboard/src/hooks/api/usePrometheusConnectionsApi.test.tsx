// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { usePrometheusConnectionsApi } from './usePrometheusConnectionsApi'

const request = vi.hoisted(() => vi.fn())
vi.mock('../apiClient', () => ({ request }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let root: Root
let api: ReturnType<typeof usePrometheusConnectionsApi>
function Capture() { api = usePrometheusConnectionsApi(); return null }
beforeEach(async () => { vi.clearAllMocks(); root = createRoot(document.createElement('div')); request.mockResolvedValue({ id: 'operation' }); await act(async () => root.render(<Capture />)) })
afterEach(() => act(() => root.unmount()))

it.each(['test', 'discover', 'preview'] as const)('dispatches %s to its concrete saved-connection POST route', async operation => {
  const controller = new AbortController()
  const input = { kind: 'metrics' as const, selector: '{job="checkout"}' }
  await api.inspectPrometheusConnection('project/name', 'connection name', operation, input, { signal: controller.signal })
  expect(request).toHaveBeenCalledWith(`/projects/project%2Fname/monitoring/prometheus-connections/connection%20name/${operation}`, { method: 'POST', body: JSON.stringify(input), signal: controller.signal })
})

it('encodes the operation identity on the saved-connection status route', async () => {
  await api.getPrometheusOperation('project/name', 'connection name', 'op/1')
  expect(request.mock.calls[0][0]).toBe('/projects/project%2Fname/monitoring/prometheus-connections/connection%20name/operations/op%2F1')
})
