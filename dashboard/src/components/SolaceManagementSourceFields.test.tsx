// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it } from 'vitest'
import SolaceManagementSourceFields from './SolaceManagementSourceFields'
import { newSolaceIdentity, newSolaceScope, type SolaceMonitorSource as Source } from './solaceManagementSource'
const validSource = (): Source => ({ id: 'source', project_id: 'project', name: 'Depth', revision: 4, collector_id: 'collector', pack: 'solace-semp', interval_seconds: 30, enabled: true, destination: 'management://broker', collector_public_key: 'public-key', management_scope: { ...newSolaceScope(), provider: 'broker.example.com', entities: [{ solace: { vpn: 'primary', queue: 'Orders/Nested' } }] }, management_identity: { ...newSolaceIdentity(), principal: 'reader', policy_revision: 'p1', credential_ref: 'mgmt-secret', credential_version: 'saved-pin' } })
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLElement; let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(() => { act(() => root.unmount()); container.remove() })
const label = (text: string) => Array.from(container.querySelectorAll('label')).find(element => element.textContent?.startsWith(text))!
const changeInput = async (text: string, value: string) => act(async () => { const input = label(text).querySelector('input')!; Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })); input.dispatchEvent(new Event('change', { bubbles: true })) })
const changeSelect = async (text: string, value: string) => act(async () => { const select = label(text).querySelector('select')!; select.value = value; select.dispatchEvent(new Event('change', { bubbles: true })) })
const click = async (text: string) => act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === text)!.click())
it('keeps saved approval separate and never retains a changed credential pin', async () => {
  const saved = validSource(); let draft = validSource()
  const render = () => root.render(<SolaceManagementSourceFields source={draft} savedSource={saved} available onChange={source => { draft = source; render() }} />)
  await act(async () => render())
  await changeInput('Solace management hostname', 'changed.example.com')
  await changeInput('Management credential project-secret reference', 'rotated-secret')
  expect(draft.management_identity!.credential_version).toBeUndefined()
  expect(draft.management_identity!.credential_ref).toBe('rotated-secret')
  expect(draft.management_scope!.provider).toBe('changed.example.com')
  expect(container.querySelector('pre')!.textContent).toContain('broker.example.com')
  expect(container.querySelector('pre')!.textContent).toContain('saved-pin')
  expect(container.querySelector('pre')!.textContent).not.toContain('rotated-secret')
  expect(saved.management_identity!.credential_ref).toBe('mgmt-secret')
})
it('preserves the VPN while switching between explicit queue and topic endpoint tuples', async () => {
  const saved = validSource(); let draft = validSource()
  const render = () => root.render(<SolaceManagementSourceFields source={draft} savedSource={saved} available onChange={source => { draft = source; render() }} />)
  await act(async () => render())
  await changeSelect('Entity type 1', 'topic_endpoint')
  expect(label('Entity type 1').querySelector('select')!.value).toBe('topic_endpoint')
  expect(draft.management_scope!.entities[0].solace).toEqual({ vpn: 'primary', topic_endpoint: '' })
  await changeInput('Topic endpoint name 1', 'endpoint/☃')
  await changeInput('VPN name 1', 'New/VPN')
  expect(draft.management_scope!.entities[0].solace).toEqual({ vpn: 'New/VPN', topic_endpoint: 'endpoint/☃' })
  await changeSelect('Entity type 1', 'queue')
  expect(draft.management_scope!.entities[0].solace).toEqual({ vpn: 'New/VPN', queue: '' })
  expect(saved.management_scope!.entities[0].solace).toEqual({ vpn: 'primary', queue: 'Orders/Nested' })
})
it('keeps the last entity and caps inventory before rendering malformed or excessive populations', async () => {
  let draft = validSource()
  const render = () => root.render(<SolaceManagementSourceFields source={draft} available onChange={source => { draft = source; render() }} />)
  await act(async () => render())
  expect(Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Remove Solace entity 1')!.disabled).toBe(true)
  await click('Add Solace entity')
  expect(draft.management_scope!.entities).toHaveLength(2)
  await click('Remove Solace entity 2')
  expect(draft.management_scope!.entities).toHaveLength(1)
  for (const entities of [Array.from({ length: 257 }, (_, i) => ({ solace: { vpn: 'v', queue: `q${i}` } })), [null]]) {
    draft = { ...validSource(), management_scope: { ...newSolaceScope(), provider: 'broker.example.com', entities: entities as unknown as NonNullable<Source['management_scope']>['entities'] } }
    await act(async () => render())
    expect(container.querySelector('[role="alert"]')!.textContent).toContain('finite plain JSON')
    expect(container.querySelectorAll('select')).toHaveLength(0)
    expect(container.querySelector('button')).toBeNull()
  }
})
it('keeps a disabled saved approval readable without exposing password or session controls', async () => {
  const source = validSource()
  await act(async () => root.render(<SolaceManagementSourceFields source={source} savedSource={source} available={false} onChange={() => { throw new Error('disabled changed') }} />))
  expect(container.querySelector('fieldset')!.disabled).toBe(true)
  expect(container.querySelector('pre')!.textContent).toContain('saved-pin')
  expect(container.querySelectorAll('input[type="password"]')).toHaveLength(0)
  expect(container.textContent).toContain('fixed logout')
  expect(container.textContent).toContain('no automatic session-reset')
})
it('refuses to display another source, project or provider saved approval', async () => {
  const source = validSource()
  for (const savedSource of [{ ...source, id: 'other' }, { ...source, project_id: 'other' }, { ...source, pack: 'servicebus-runtime' }, { ...source, management_scope: { ...newSolaceScope(), kind: 'kafka_offsets' } }]) {
    await act(async () => root.render(<SolaceManagementSourceFields source={source} savedSource={savedSource} available onChange={() => {}} />))
    expect(container.querySelector('details')).toBeNull()
  }
})
it('refuses excessive or malformed saved approval before mounted disclosure', async () => {
  const source = validSource()
  const malformed = [
    { ...validSource(), management_scope: { ...newSolaceScope(), entities: Array.from({ length: 257 }, (_, i) => ({ solace: { vpn: 'v', queue: `q${i}` } })) } },
    { ...validSource(), management_identity: { ...newSolaceIdentity(), principal: 'x'.repeat(257) } },
    { ...validSource(), metadata: 'x'.repeat(16 * 1024 + 1) },
    { ...validSource(), revision: { small: 'object' } as unknown as number },
    { ...validSource(), id: { small: 'object' } as unknown as string },
    { ...validSource(), project_id: { small: 'object' } as unknown as string },
  ]
  for (const savedSource of malformed) {
    await act(async () => root.render(<SolaceManagementSourceFields source={source} savedSource={savedSource} available onChange={() => {}} />))
    expect(container.querySelector('pre')).toBeNull()
    expect(container.querySelector('[role="alert"]')!.textContent).toContain('saved collector approval')
  }
  const getter = validSource(); let invoked = false
  Object.defineProperty(getter, 'management_scope', { enumerable: true, get() { invoked = true; throw new Error('must not run') } })
  await act(async () => root.render(<SolaceManagementSourceFields source={source} savedSource={getter} available onChange={() => {}} />))
  expect(container.querySelector('pre')).toBeNull(); expect(invoked).toBe(false)
  const extended = { ...validSource(), management_identity: { ...validSource().management_identity!, password: 'not-public' }, metadata: 'not-public-metadata' }
  await act(async () => root.render(<SolaceManagementSourceFields source={source} savedSource={extended} available onChange={() => {}} />))
  expect(container.querySelector('pre')!.textContent).not.toContain('not-public')
  expect(container.querySelector('pre')!.textContent).toContain('saved-pin')
})

it('refuses malformed current owner/revision scalars before controlled inputs', async () => {
  for (const source of [{ ...validSource(), revision: { small: 'object' } as unknown as number }, { ...validSource(), revision: Number.MAX_SAFE_INTEGER + 1 }, { ...validSource(), id: [] as unknown as string }, { ...validSource(), project_id: null as unknown as string }]) {
    await act(async () => root.render(<SolaceManagementSourceFields source={source} savedSource={validSource()} available onChange={() => {}} />))
    expect(container.querySelector('[role="alert"]')!.textContent).toContain('finite plain JSON')
    expect(container.querySelector('input')).toBeNull(); expect(container.querySelector('pre')).toBeNull()
  }
})
