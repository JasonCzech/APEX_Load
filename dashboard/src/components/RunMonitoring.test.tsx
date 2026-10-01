// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { RunMonitorSources, ScenarioMonitorBindings } from './RunMonitoring'

const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLElement; let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); request.mockReset() })
afterEach(() => { act(() => root.unmount()); container.remove() })

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
