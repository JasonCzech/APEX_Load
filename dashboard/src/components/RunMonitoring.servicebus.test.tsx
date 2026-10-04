// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { RunMonitorSources } from './RunMonitoring'
import { newServiceBusIdentity, newServiceBusScope, type ServiceBusMonitorSource as Source } from './serviceBusManagementSource'

const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLElement; let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); request.mockReset() })
afterEach(() => { act(() => root.unmount()); container.remove() })
const label = (text: string) => Array.from(container.querySelectorAll('label')).find(element => element.textContent?.startsWith(text))!
const changeInput = async (text: string, value: string) => act(async () => { const input = label(text).querySelector('input')!; Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })); input.dispatchEvent(new Event('change', { bubbles: true })) })
const changeSelect = async (text: string, value: string) => act(async () => { const select = label(text).querySelector('select')!; select.value = value; select.dispatchEvent(new Event('change', { bubbles: true })) })
const click = async (text: string) => act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === text)!.click())
const savedSource = (): Source => ({ id: 'managed', project_id: 'p', name: 'Approved counts', revision: 4, collector_id: 'collector', pack: 'servicebus-runtime', interval_seconds: 30, enabled: true, destination: 'management://namespace', collector_public_key: 'public-key', management_scope: { ...newServiceBusScope(), provider: 'example.servicebus.windows.net', entities: [{ service_bus: { queue: 'orders' } }] }, management_identity: { ...newServiceBusIdentity(), principal: 'ManageReader', policy_revision: 'v1', credential_ref: 'management-secret', credential_version: 'saved-version' } })
const mockSources = (sources: Source[], capability = { monitoring_enabled: true, messaging_management_enabled: true, messaging_management_packs: ['kafka-offsets', 'servicebus-runtime'] }) => request.mockImplementation(async (path: string, options?: { method?: string; body?: string }) => {
  if (path.includes('capabilities')) return capability
  if (path.includes('collectors?')) return { collectors: [{ id: 'collector', name: 'Collector', health: 'healthy' }] }
  if (path.includes('management-state')) return { state: 'unavailable' }
  if (options?.method === 'POST' || options?.method === 'PUT') return { ...JSON.parse(options.body!), id: 'managed', revision: 5 }
  return sources
})

it('offers each management reader only from the exact enabled executable inventory', async () => {
  for (const [packs, offered] of [
    [['kafka-offsets'], ['kafka-offsets']],
    [['servicebus-runtime'], ['servicebus-runtime']],
    [['kafka-offsets', 'servicebus-runtime', 'unimplemented'], ['kafka-offsets', 'servicebus-runtime']],
    [[], []]
  ] as [string[], string[]][]) {
    mockSources([], { monitoring_enabled: true, messaging_management_enabled: true, messaging_management_packs: packs })
    await act(async () => root.render(<RunMonitorSources projectId={JSON.stringify(packs)} canManage />))
    const values = Array.from(label('Pack').querySelector('select')!.options).map(option => option.value)
    for (const pack of ['kafka-offsets', 'servicebus-runtime']) expect(values.includes(pack)).toBe(offered.includes(pack))
    expect(values).not.toContain('unimplemented')
  }
  mockSources([], { monitoring_enabled: true, messaging_management_enabled: false, messaging_management_packs: ['servicebus-runtime'] })
  await act(async () => root.render(<RunMonitorSources projectId="disabled" canManage />))
  expect(Array.from(label('Pack').querySelector('select')!.options).map(option => option.value)).not.toContain('servicebus-runtime')
})

it('initializes Service Bus at 30 seconds and sends a separate shared-key queue approval', async () => {
  mockSources([])
  await act(async () => root.render(<RunMonitorSources projectId="p" canManage />))
  await changeInput('Name', 'Counts')
  await changeSelect('Collector', 'collector')
  await changeSelect('Pack', 'servicebus-runtime')
  expect(label('Collection interval (seconds)').querySelector('input')!.value).toBe('30')
  expect(label('Collection interval (seconds)').querySelector('input')!.min).toBe('30')
  expect(label('Service Bus management identity').querySelector('select')!.value).toBe('shared_key')
  expect(label('Approved destination').querySelector('input')!.type).toBe('text')
  await changeInput('Approved destination', 'management://namespace')
  await changeInput('Service Bus namespace hostname', 'example.servicebus.windows.net')
  await changeInput('Approved Service Bus Manage policy name', 'ManageReader')
  await changeInput('Identity policy revision', 'v1')
  await changeInput('Management credential project-secret reference', 'management-secret')
  await changeInput('Collector X25519 public key', 'public-key')
  await changeInput('Queue path 1', 'Orders/Nested')
  await click('Save monitor source')
  const mutation = request.mock.calls.find(([, options]) => options?.method === 'POST')!
  expect(mutation).toBeDefined()
  const payload = JSON.parse(mutation[1].body)
  expect(payload.interval_seconds).toBe(30)
  expect(payload.management_scope).toEqual({ version: 1, kind: 'servicebus_runtime', provider: 'example.servicebus.windows.net', entities: [{ service_bus: { queue: 'Orders/Nested' } }] })
  expect(payload.management_identity).toEqual({ mode: 'shared_key', principal: 'ManageReader', policy_revision: 'v1', credential_ref: 'management-secret' })
  expect(payload.credential_ref).toBe('')
  expect(payload.management_scope.isolation).toBeUndefined()
})

it('clears both management profiles when switching packs and retains neither script credentials nor pins', async () => {
  const source = savedSource(); source.credential_ref = 'data-plane-secret'
  mockSources([source])
  await act(async () => root.render(<RunMonitorSources projectId="p" canManage />))
  await click('Edit Approved counts')
  await changeSelect('Pack', 'kafka-offsets')
  expect(container.textContent).not.toContain('saved-version')
  expect(label('SASL identity').querySelector('select')!.value).toBe('sasl_plain')
  expect(label('Management credential project-secret reference').querySelector('input')!.value).toBe('')
  await changeSelect('Pack', 'servicebus-runtime')
  expect(label('Service Bus namespace hostname').querySelector('input')!.value).toBe('')
  expect(label('Service Bus management identity').querySelector('select')!.value).toBe('shared_key')
  expect(label('Management credential project-secret reference').querySelector('input')!.value).toBe('')
  expect(label('Collector X25519 public key').querySelector('input')!.value).toBe('')
  await changeSelect('Pack', 'ssh-linux')
  expect(container.textContent).not.toContain('Service Bus management approval')
  expect(label('Project credential secret name').querySelector('input')!.value).toBe('')
  expect(label('Collector X25519 public key').querySelector('input')!.value).toBe('')
  await changeInput('Approved destination', 'ssh://host')
  await click('Save monitor source')
  const mutation = request.mock.calls.find(([, options]) => options?.method === 'PUT')!
  const payload = JSON.parse(mutation[1].body)
  expect(payload.management_scope).toBeUndefined()
  expect(payload.management_identity).toBeUndefined()
  expect(payload.credential_ref).toBe('')
})

it('preserves a saved Service Bus approval while changing its pinned identity through a revisioned update', async () => {
  const source = savedSource(); mockSources([source])
  await act(async () => root.render(<RunMonitorSources projectId="p" canManage />))
  await click('Edit Approved counts')
  await changeInput('Management credential project-secret reference', 'rotated-management-secret')
  expect(container.querySelector('.servicebus-management-approval pre')!.textContent).toContain('saved-version')
  expect(container.querySelector('.servicebus-management-approval pre')!.textContent).not.toContain('rotated-management-secret')
  await click('Save monitor source')
  const mutation = request.mock.calls.find(([, options]) => options?.method === 'PUT')!
  expect(mutation[0]).toBe('/projects/p/monitor-sources/managed')
  const payload = JSON.parse(mutation[1].body)
  expect(payload.revision).toBe(4)
  expect(payload.management_identity).toEqual({ mode: 'shared_key', principal: 'ManageReader', policy_revision: 'v1', credential_ref: 'rotated-management-secret' })
  expect(payload.management_scope).toEqual(source.management_scope)
  expect(source.management_identity!.credential_version).toBe('saved-version')
})

it('keeps a disabled saved pack readable and deletable while blocking its save', async () => {
  const source = savedSource()
  mockSources([source], { monitoring_enabled: true, messaging_management_enabled: false, messaging_management_packs: [] })
  await act(async () => root.render(<RunMonitorSources projectId="p" canManage />))
  await click('Edit Approved counts')
  expect(label('Pack').querySelector('select')!.value).toBe('servicebus-runtime')
  expect(container.querySelector<HTMLFieldSetElement>('.servicebus-management-approval')!.disabled).toBe(true)
  expect(container.querySelector('.servicebus-management-approval pre')!.textContent).toContain('saved-version')
  expect(Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save monitor source')!.disabled).toBe(true)
  await click('Delete source')
  expect(request).toHaveBeenCalledWith('/projects/p/monitor-sources/managed?revision=4', expect.objectContaining({ method: 'DELETE' }))
})
