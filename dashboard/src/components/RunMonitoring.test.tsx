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
