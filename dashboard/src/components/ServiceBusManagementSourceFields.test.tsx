// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it } from 'vitest'
import ServiceBusManagementSourceFields from './ServiceBusManagementSourceFields'
import { newServiceBusIdentity, newServiceBusScope, type ServiceBusMonitorSource as Source } from './serviceBusManagementSource'

const validSource = (): Source => ({ id: 'source', project_id: 'project', revision: 4, name: 'Runtime counts', collector_id: 'collector', pack: 'servicebus-runtime', enabled: true, interval_seconds: 30, destination: 'management://namespace', collector_public_key: 'public-key', management_scope: { ...newServiceBusScope(), provider: 'example.servicebus.windows.net', entities: [{ service_bus: { queue: 'orders' } }] }, management_identity: { ...newServiceBusIdentity(), principal: 'ManageReader', policy_revision: 'v1', credential_ref: 'management-secret', credential_version: 'saved-version' } })
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLElement; let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(() => { act(() => root.unmount()); container.remove() })
const label = (text: string) => Array.from(container.querySelectorAll('label')).find(element => element.textContent?.startsWith(text))!
const changeInput = async (text: string, value: string) => act(async () => { const input = label(text).querySelector('input')!; Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })); input.dispatchEvent(new Event('change', { bubbles: true })) })
const changeSelect = async (text: string, value: string) => act(async () => { const select = label(text).querySelector('select')!; select.value = value; select.dispatchEvent(new Event('change', { bubbles: true })) })
const click = async (text: string) => act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === text)!.click())

it('keeps saved collector approval separate from an unsaved draft and clears a changed identity pin', async () => {
  const saved = validSource(); let draft = validSource(); draft.management_scope!.provider = 'unsaved.servicebus.windows.net'
  const render = () => root.render(<ServiceBusManagementSourceFields source={draft} savedSource={saved} available onChange={source => { draft = source; render() }} />)
  await act(async () => render())
  expect(container.querySelector('pre')!.textContent).toContain('"provider": "example.servicebus.windows.net"')
  expect(container.querySelector('pre')!.textContent).not.toContain('unsaved.servicebus.windows.net')
  expect(container.querySelector('pre')!.textContent).toContain('saved-version')
  await changeInput('Management credential project-secret reference', 'rotated-secret')
  expect(draft.management_identity!.credential_ref).toBe('rotated-secret')
  expect(draft.management_identity!.credential_version).toBeUndefined()
  expect(saved.management_identity!.credential_ref).toBe('management-secret')
  expect(saved.management_identity!.credential_version).toBe('saved-version')
  expect(container.querySelector('pre')!.textContent).not.toContain('rotated-secret')
  expect(draft.management_scope!.provider).toBe('unsaved.servicebus.windows.net')
  expect(draft.credential_ref).toBeUndefined()
})

it('retains an empty subscription choice and immutably edits the explicit tuple', async () => {
  const saved = validSource(); let draft = validSource()
  draft.management_scope!.entities.push({ service_bus: { queue: 'retained' } })
  const render = () => root.render(<ServiceBusManagementSourceFields source={draft} savedSource={saved} available onChange={source => { draft = source; render() }} />)
  await act(async () => render())
  await changeSelect('Entity type 1', 'subscription')
  expect(label('Entity type 1').querySelector('select')!.value).toBe('subscription')
  expect(label('Topic path 1').querySelector('input')!.value).toBe('')
  expect(label('Subscription name 1').querySelector('input')!.value).toBe('')
  expect(draft.management_scope!.entities[0].service_bus).toEqual({ topic: '', subscription: '' })
  await changeInput('Topic path 1', 'Events/Nested')
  await changeInput('Subscription name 1', 'Workers')
  expect(draft.management_scope!.entities[0].service_bus).toEqual({ topic: 'Events/Nested', subscription: 'Workers' })
  expect(draft.management_scope!.entities[1].service_bus).toEqual({ queue: 'retained' })
  expect(saved.management_scope!.entities[0].service_bus).toEqual({ queue: 'orders' })
  await changeSelect('Entity type 1', 'queue')
  expect(draft.management_scope!.entities[0].service_bus).toEqual({ queue: '' })
  expect(Array.from(container.querySelectorAll('label')).some(element => element.textContent?.startsWith('Topic path'))).toBe(false)
  await changeInput('Queue path 1', 'New/Queue')
  expect(draft.management_scope!.entities[0].service_bus).toEqual({ queue: 'New/Queue' })
})

it('adds and removes inventory within finite bounds while keeping the last entity', async () => {
  const saved = validSource(); let draft = validSource()
  const render = () => root.render(<ServiceBusManagementSourceFields source={draft} savedSource={saved} available onChange={source => { draft = source; render() }} />)
  await act(async () => render())
  expect(Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Remove Service Bus entity 1')!.disabled).toBe(true)
  await click('Add Service Bus entity')
  expect(draft.management_scope!.entities).toHaveLength(2)
  expect(draft.management_scope!.entities[0].service_bus).toEqual({ queue: 'orders' })
  await click('Remove Service Bus entity 1')
  expect(draft.management_scope!.entities).toEqual([{ service_bus: { queue: '' } }])
  expect(saved.management_scope!.entities).toEqual([{ service_bus: { queue: 'orders' } }])
  draft = validSource(); draft.management_scope!.entities = Array.from({ length: 256 }, (_, index) => ({ service_bus: { queue: `q${index}` } }))
  await act(async () => render())
  expect(Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Add Service Bus entity')!.disabled).toBe(true)
})

it('keeps saved approval readable when the project cannot configure the pack', async () => {
  const saved = validSource()
  await act(async () => root.render(<ServiceBusManagementSourceFields source={saved} savedSource={saved} available={false} onChange={() => { throw new Error('disabled approval changed') }} />))
  expect(container.querySelector('fieldset')!.disabled).toBe(true)
  expect(container.textContent).toContain('cannot configure')
  expect(container.querySelector('pre')!.textContent).toContain('saved-version')
  expect(container.querySelectorAll('input[type="password"]')).toHaveLength(0)
  expect(container.querySelector('input[type="url"]')).toBeNull()
})


it('does not present a different source, project or provider approval as the current draft', async () => {
  const source = validSource()
  const otherApprovals = [
    { ...validSource(), id: 'another-source' },
    { ...validSource(), project_id: 'another-project' },
    { ...validSource(), pack: 'kafka-offsets' },
    { ...validSource(), management_scope: { ...newServiceBusScope(), kind: 'kafka_offsets' } },
  ]
  for (const savedSource of otherApprovals) {
    await act(async () => root.render(<ServiceBusManagementSourceFields source={source} savedSource={savedSource} available onChange={() => { throw new Error('readonly approval mutated') }} />))
    expect(container.querySelector('details')).toBeNull()
    expect(container.textContent).not.toContain('saved-version')
  }
  const unsaved = { ...source, id: '' }
  await act(async () => root.render(<ServiceBusManagementSourceFields source={unsaved} savedSource={unsaved} available onChange={() => {}} />))
  expect(container.querySelector('details')).toBeNull()
})

it('keeps help separate from accessible control names with unique descriptions', async () => {
  await act(async () => root.render(<><ServiceBusManagementSourceFields source={validSource()} available onChange={() => {}} /><ServiceBusManagementSourceFields source={validSource()} available onChange={() => {}} /></>))
  const described = Array.from(container.querySelectorAll<HTMLInputElement>('input[aria-describedby]'))
  expect(described).toHaveLength(6)
  expect(new Set(described.map(input => input.getAttribute('aria-describedby'))).size).toBe(6)
  for (const input of described) {
    expect(['Service Bus namespace hostname', 'Approved Service Bus Manage policy name', 'Management credential project-secret reference']).toContain(input.getAttribute('aria-label'))
    const description = document.getElementById(input.getAttribute('aria-describedby')!)
    expect(description?.textContent).toMatch(/canonical|sealed|purpose-bound/)
    expect(input.closest('label')!.contains(description)).toBe(true)
  }
})
