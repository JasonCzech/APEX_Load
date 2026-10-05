// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { RunMonitorSources } from './RunMonitoring'
import { newSolaceIdentity, newSolaceScope, type SolaceMonitorSource as Source } from './solaceManagementSource'

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
const savedSource = (): Source => ({ id: 'managed', project_id: 'p', name: 'Approved counts', revision: 4, collector_id: 'collector', pack: 'solace-semp', interval_seconds: 30, enabled: true, destination: 'management://namespace', collector_public_key: 'public-key', management_scope: { ...newSolaceScope(), provider: 'broker.example.com', entities: [{ solace: { vpn: 'primary', queue: 'orders' } }] }, management_identity: { ...newSolaceIdentity(), principal: 'reader', policy_revision: 'v1', credential_ref: 'management-secret', credential_version: 'saved-version' } })
const mockSources = (sources: Source[], capability = { monitoring_enabled: true, messaging_management_enabled: true, messaging_management_packs: ['kafka-offsets', 'servicebus-runtime', 'solace-semp'] }) => request.mockImplementation(async (path: string, options?: { method?: string; body?: string }) => {
  if (path.includes('capabilities')) return capability
  if (path.includes('collectors?')) return { collectors: [{ id: 'collector', name: 'Collector', health: 'healthy' }] }
  if (path.includes('management-state')) return { state: 'unavailable' }
  if (options?.method === 'POST' || options?.method === 'PUT') return { ...JSON.parse(options.body!), id: 'managed', revision: 5 }
  return sources
})

it('creates a finite separately pinned management source at the provider cadence', async () => {
  mockSources([])
  await act(async () => root.render(<RunMonitorSources projectId="p" canManage />))
  await changeInput('Name', 'Depth')
  await changeSelect('Collector', 'collector')
  await changeSelect('Pack', 'solace-semp')
  expect(label('Collection interval (seconds)').querySelector('input')!.value).toBe('30')
  expect(label('Collection interval (seconds)').querySelector('input')!.min).toBe('30')
  expect(label('Solace management identity').querySelector('select')!.value).toBe('basic')
  await changeInput('Approved destination', 'management://broker')
  await changeInput('Solace management hostname', 'broker.example.com')
  await changeInput('Approved Solace management username', 'reader')
  await changeInput('Identity policy revision', 'p1')
  await changeInput('Management credential project-secret reference', 'mgmt-secret')
  await changeInput('Collector X25519 public key', 'public-key')
  await changeInput('VPN name 1', 'primary')
  await changeInput('Queue name 1', 'Orders/Nested')
  await click('Save monitor source')
  const mutation = request.mock.calls.find(([, options]) => options?.method === 'POST')!
  expect(mutation).toBeDefined()
  const payload = JSON.parse(mutation[1].body)
  expect(payload.management_scope).toEqual({ version: 1, kind: 'solace_semp', provider: 'broker.example.com', entities: [{ solace: { vpn: 'primary', queue: 'Orders/Nested' } }] })
  expect(payload.management_identity).toEqual({ mode: 'basic', principal: 'reader', policy_revision: 'p1', credential_ref: 'mgmt-secret' })
  expect(payload.interval_seconds).toBe(30); expect(payload.credential_ref).toBe('')
  expect(payload.management_scope.isolation).toBeUndefined()
})
it('retains saved approval through revisioned identity edits and pack switching clears all pins', async () => {
  const source = savedSource(); mockSources([source])
  await act(async () => root.render(<RunMonitorSources projectId="p" canManage />))
  await click('Edit Approved counts')
  await changeInput('Management credential project-secret reference', 'rotation')
  expect(container.querySelector('.solace-management-approval pre')!.textContent).toContain('saved-version')
  expect(container.querySelector('.solace-management-approval pre')!.textContent).not.toContain('rotation')
  await click('Save monitor source')
  const mutation = request.mock.calls.find(([, options]) => options?.method === 'PUT')!
  expect(mutation[0]).toBe('/projects/p/monitor-sources/managed')
  expect(JSON.parse(mutation[1].body).revision).toBe(4)
  expect(JSON.parse(mutation[1].body).management_identity.credential_version).toBeUndefined()
  await click('Edit Approved counts')
  await changeSelect('Pack', 'servicebus-runtime')
  expect(label('Management credential project-secret reference').querySelector('input')!.value).toBe('')
  expect(label('Collector X25519 public key').querySelector('input')!.value).toBe('')
  expect(container.textContent).not.toContain('saved-version')
  await changeSelect('Pack', 'solace-semp')
  expect(label('VPN name 1').querySelector('input')!.value).toBe('')
  expect(label('Management credential project-secret reference').querySelector('input')!.value).toBe('')
})
it('offers only the executable pack and preserves disabled saved reads/deletion', async () => {
  const source = savedSource()
  mockSources([source], { monitoring_enabled: true, messaging_management_enabled: false, messaging_management_packs: [] })
  await act(async () => root.render(<RunMonitorSources projectId="p" canManage />))
  expect(Array.from(label('Pack').querySelector('select')!.options).map(option => option.value)).not.toContain('solace-semp')
  await click('Edit Approved counts')
  expect(container.querySelector<HTMLFieldSetElement>('.solace-management-approval')!.disabled).toBe(true)
  expect(container.querySelector('.solace-management-approval pre')!.textContent).toContain('saved-version')
  expect(Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save monitor source')!.disabled).toBe(true)
  await click('Delete source')
  expect(request).toHaveBeenCalledWith('/projects/p/monitor-sources/managed?revision=4', expect.objectContaining({ method: 'DELETE' }))
})
