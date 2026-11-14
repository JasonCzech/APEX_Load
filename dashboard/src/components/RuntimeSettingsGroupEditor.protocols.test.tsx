// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import RuntimeSettingsGroupEditor, { type RuntimeEditorGroup } from './RuntimeSettingsGroupEditor'
import { type RuntimeSettingsOverride, type ScenarioRuntimeSettingsDraft } from '../types'
import { type ScriptConfig } from '../types/scripts'
import { PLAN_ENTRY_RUNTIME_PROTOCOLS, effectiveRuntimeValue, runtimeDraftFromOverride } from './runtimeSettingsModel'
import { buildRuntimeSettingsPayload } from './scenarioPayloadUtils'

// Phase 4B: the native protocol override tabs (kafka, jms, eventhubs, grpc,
// database, network, appium, java_http), the complete websocket section and
// the Java Env timeouts and delimiters, with the step-2 patterns: three-state
// booleans, provenance badges, Reset to inherited, masked secrets and
// override_fields partial writes.

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let container: HTMLDivElement
let root: Root
let latest: ScenarioRuntimeSettingsDraft | undefined

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  latest = undefined
})

afterEach(() => {
  act(() => root.unmount())
  container.remove()
})

interface HarnessProps {
  group: RuntimeEditorGroup
  initial?: ScenarioRuntimeSettingsDraft
  scenarioDefault?: RuntimeSettingsOverride
  engineDefaults?: ScriptConfig
  overrideLabel?: string
  scenarioDefaultLabel?: string
  applicableProtocols?: string[]
  scopeUsesLLMActions?: boolean
}

function Harness({ group, initial, scenarioDefault, engineDefaults, overrideLabel, scenarioDefaultLabel, applicableProtocols, scopeUsesLLMActions }: HarnessProps) {
  const [draft, setDraft] = useState<ScenarioRuntimeSettingsDraft | undefined>(initial)
  return (
    <RuntimeSettingsGroupEditor
      groups={[group]}
      focusedIndex={0}
      onFocusChange={() => {}}
      scenarioDefault={scenarioDefault}
      engineDefaults={engineDefaults}
      overrides={[draft]}
      overrideLabel={overrideLabel}
      scenarioDefaultLabel={scenarioDefaultLabel}
      applicableProtocols={applicableProtocols}
      scopeUsesLLMActions={scopeUsesLLMActions}
      onChange={(_, next) => {
        latest = next
        setDraft(next)
      }}
      onApplyToGroups={() => {}}
    />
  )
}

function mount(props: HarnessProps) {
  act(() => root.render(<Harness {...props} />))
}

function payload() {
  return latest ? buildRuntimeSettingsPayload(latest) : undefined
}

function clickTab(label: string) {
  // HTTP sub-tabs carry a count after their label span.
  const tab = Array.from(container.querySelectorAll('[role="tab"]')).find(b =>
    (b.textContent || '').trim() === label || (b.querySelector('span')?.textContent || '').trim() === label,
  ) as HTMLButtonElement | undefined
  if (!tab) throw new Error(`runtime tab not found: ${label}`)
  act(() => tab.dispatchEvent(new MouseEvent('click', { bubbles: true })))
}



function control<T extends HTMLElement>(labelText: string, selector: string): T {
  const label = Array.from(container.querySelectorAll('label')).find(candidate =>
    Array.from(candidate.querySelectorAll(':scope > span')).some(span => (span.textContent || '').trim() === labelText),
  )
  const found = label?.querySelector(selector) as T | null
  if (!found) throw new Error(`control not found for label: ${labelText}`)
  return found
}

function fieldFor(path: string): HTMLElement {
  const found = container.querySelector(`.runtime-override-field[data-override-path="${path}"]`) as HTMLElement | null
  if (!found) throw new Error(`override field not found: ${path}`)
  return found
}

function badgeText(path: string): string {
  return (fieldFor(path).querySelector('.runtime-field-provenance')?.textContent || '').trim()
}

function resetButton(path: string): HTMLButtonElement | null {
  return fieldFor(path).querySelector('.runtime-override-reset')
}

function setSelect(select: HTMLSelectElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')!.set!
  act(() => {
    setter.call(select, value)
    select.dispatchEvent(new Event('change', { bubbles: true }))
  })
}

function setInput(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
  act(() => {
    setter.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

/** Types `text` one character at a time, as a user does: each keystroke appends to what the control now shows. */
function typeInto(element: HTMLInputElement | HTMLTextAreaElement, text: string) {
  const proto = element instanceof HTMLTextAreaElement ? window.HTMLTextAreaElement.prototype : window.HTMLInputElement.prototype
  const setter = Object.getOwnPropertyDescriptor(proto, 'value')!.set!
  for (const char of text) {
    act(() => {
      setter.call(element, element.value + char)
      element.dispatchEvent(new Event('input', { bubbles: true }))
    })
  }
}

function blur(element: HTMLElement) {
  act(() => element.dispatchEvent(new FocusEvent('focusout', { bubbles: true })))
}

function click(element: Element) {
  act(() => element.dispatchEvent(new MouseEvent('click', { bubbles: true })))
}

function button(text: string): HTMLButtonElement {
  const found = Array.from(container.querySelectorAll('button')).find(b => (b.textContent || '').trim() === text)
  if (!found) throw new Error(`button not found: ${text}`)
  return found as HTMLButtonElement
}


const KAFKA_GROUP: RuntimeEditorGroup = {
  name: 'Orders stream',
  scriptId: 'k1',
  protocol: 'kafka',
  scriptConfig: {
    base_url: '',
    kafka: {
      brokers: ['k1.example.test:9092'],
      topic: 'orders',
      acks: 'all',
      sasl_mechanism: 'plain',
      sasl_password: 'script-literal',
      properties: { 'client.rack': 'east', 'request.timeout.ms': '30000' },
      mock: true,
    },
  },
}

const JMS_GROUP: RuntimeEditorGroup = {
  name: 'Solace orders',
  scriptId: 'j1',
  protocol: 'jms-solace',
  scriptConfig: { base_url: '', jms: { provider: 'solace', properties: { 'client.vpn': 'east', 'request.timeout': '30000' } } },
}

const GRPC_GROUP: RuntimeEditorGroup = {
  name: 'Orders RPC',
  scriptId: 'g1',
  protocol: 'grpc',
  scriptConfig: { base_url: '', grpc: { metadata: { authorization: 'Bearer script-token', 'x-trace': '1' } } },
}

function optionByText(select: HTMLSelectElement, text: string): HTMLOptionElement {
  const option = Array.from(select.options).find(candidate => (candidate.textContent || '').trim() === text)
  if (!option) throw new Error(`option not found: ${text}`)
  return option
}

function lastRow() {
  return payload()
}

describe('RuntimeSettingsGroupEditor protocol override tabs', () => {
  it('shows each protocol tab only for its protocol, and at scenario level when any group runs it', () => {
    mount({ group: KAFKA_GROUP })
    const tabs = () => Array.from(container.querySelectorAll('[role="tab"]')).map(tab => (tab.textContent || '').trim())
    expect(tabs()).toContain('Kafka')
    expect(tabs()).not.toContain('JMS')
    act(() => root.unmount())
    root = createRoot(container)
    mount({ group: { name: 'Solace', scriptId: 'j1', protocol: 'jms-solace' } })
    expect(tabs()).toContain('JMS')
    expect(tabs()).not.toContain('Kafka')
    act(() => root.unmount())
    root = createRoot(container)
    mount({ group: { name: 'Scenario defaults', scriptId: 'scenario' }, applicableProtocols: ['http', 'kafka', 'grpc', 'websocket'], overrideLabel: 'Scenario default' })
    expect(tabs()).toEqual(expect.arrayContaining(['Kafka', 'gRPC', 'WebSocket']))
    expect(tabs()).not.toContain('Database')
  })

  it('writes only the edited leaf, with provenance and Reset to inherited', () => {
    mount({ group: KAFKA_GROUP })
    clickTab('Kafka')
    expect(badgeText('kafka.topic')).toBe('Script value')
    expect(resetButton('kafka.topic')).toBeNull()
    expect(control<HTMLInputElement>('Default Kafka topic', 'input').placeholder).toBe('orders')
    setInput(control<HTMLInputElement>('Default Kafka topic', 'input'), 'payments')
    expect(lastRow()).toEqual({ kafka: { topic: 'payments' }, override_fields: ['kafka.topic'] })
    expect(badgeText('kafka.topic')).toBe('Group override')
    setSelect(control<HTMLSelectElement>('Producer acknowledgement level', 'select'), 'leader')
    expect(lastRow()).toEqual({ kafka: { topic: 'payments', acks: 'leader' }, override_fields: ['kafka.topic', 'kafka.acks'] })
    click(resetButton('kafka.topic')!)
    expect(lastRow()).toEqual({ kafka: { acks: 'leader' }, override_fields: ['kafka.acks'] })
  })

  it('offers Inherit, Enabled and Disabled for plain booleans', () => {
    mount({ group: KAFKA_GROUP })
    clickTab('Kafka')
    const mock = control<HTMLSelectElement>('Use in-memory mock Kafka broker', 'select')
    expect(mock.options[0].textContent).toBe('Inherit (Enabled)')
    setSelect(mock, 'false')
    // The server drops false (omitempty) and keeps the listed path.
    expect(lastRow()).toEqual({ kafka: { mock: false }, override_fields: ['kafka.mock'] })
    expect(control<HTMLSelectElement>('Use in-memory mock Kafka broker', 'select').value).toBe('false')
    // A stored row the server returned without the false value still reads as Disabled.
    act(() => root.unmount())
    root = createRoot(container)
    mount({ group: KAFKA_GROUP, initial: runtimeDraftFromOverride({ override_fields: ['kafka.mock'] }) })
    clickTab('Kafka')
    expect(control<HTMLSelectElement>('Use in-memory mock Kafka broker', 'select').value).toBe('false')
    setSelect(control<HTMLSelectElement>('Use in-memory mock Kafka broker', 'select'), '')
    expect(latest).toBeUndefined()
  })

  it('keeps a stored SASL password masked, sends it back unchanged and clears it on request', () => {
    const initial = runtimeDraftFromOverride({ kafka: { sasl_password: '[REDACTED]', topic: 'payments' }, override_fields: ['kafka.sasl_password', 'kafka.topic'] })
    mount({ group: KAFKA_GROUP, initial })
    clickTab('Kafka')
    const password = control<HTMLInputElement>('SASL password', 'input')
    expect(password.type).toBe('password')
    expect(password.value).toBe('')
    expect(password.placeholder).toBe('Stored secret (unchanged)')
    setInput(control<HTMLInputElement>('Default Kafka topic', 'input'), 'refunds')
    expect(lastRow()).toEqual({ kafka: { sasl_password: '[REDACTED]', topic: 'refunds' }, override_fields: ['kafka.sasl_password', 'kafka.topic'] })
    click(button('Clear stored secret'))
    expect(lastRow()).toEqual({ kafka: { sasl_password: '', topic: 'refunds' }, override_fields: ['kafka.sasl_password', 'kafka.topic'] })
  })

  it('never shows the script credential value, only that a lower layer sets one', () => {
    mount({ group: KAFKA_GROUP })
    clickTab('Kafka')
    expect(container.innerHTML).not.toContain('script-literal')
    expect(control<HTMLInputElement>('SASL password', 'input').placeholder).toBe('Inherit (set by a lower layer)')
  })

  it('flags a literal typed into a secret-name field', () => {
    mount({ group: KAFKA_GROUP })
    clickTab('Kafka')
    setInput(control<HTMLInputElement>('SASL password secret name', 'input'), 'hunter2 with spaces')
    expect(fieldFor('kafka.sasl_password_secret').querySelector('[role="alert"]')?.textContent).toContain('Name a secret')
    setInput(control<HTMLInputElement>('SASL password secret name', 'input'), 'KAFKA_PW')
    expect(fieldFor('kafka.sasl_password_secret').querySelector('[role="alert"]')).toBeNull()
    expect(lastRow()).toEqual({ kafka: { sasl_password_secret: 'KAFKA_PW' }, override_fields: ['kafka.sasl_password_secret'] })
  })

  it('edits a map key by key and removes an inherited key with an empty listed path', () => {
    mount({ group: JMS_GROUP })
    clickTab('JMS')
    const vpn = container.querySelector('input[aria-label="JMS connection properties client.vpn value"]') as HTMLInputElement
    setInput(vpn, 'west')
    expect(lastRow()).toEqual({ jms: { properties: { 'client.vpn': 'west' } }, override_fields: ['jms.properties.client.vpn'] })
    click(container.querySelector('button[aria-label="Remove JMS connection properties request.timeout"]')!)
    expect(lastRow()).toEqual({
      jms: { properties: { 'client.vpn': 'west' } },
      override_fields: ['jms.properties.client.vpn', 'jms.properties.request.timeout'],
    })
    // Per-key paths render inside the map control, not in the panel list.
    expect(container.querySelector('[data-testid="runtime-override-panel-list"]')).toBeNull()
    expect(container.querySelector('input[aria-label="JMS connection properties request.timeout value"]')).toBeNull()
  })

  it('lets the Kafka properties editor only remove inherited keys, never add or edit one', () => {
    mount({ group: KAFKA_GROUP })
    clickTab('Kafka')
    const editor = fieldFor('kafka.properties')
    // The engine refuses generic Kafka properties, so no control can add one.
    expect(Array.from(editor.querySelectorAll('button')).map(b => (b.textContent || '').trim())).not.toContain('Add entry')
    expect(editor.querySelector('input[aria-label="New Additional Kafka client properties key"]')).toBeNull()
    expect(editor.textContent).toContain('not executable')
    const rack = editor.querySelector('input[aria-label="Additional Kafka client properties client.rack value"]') as HTMLInputElement
    expect(rack.readOnly).toBe(true)
    setInput(rack, 'west')
    expect(latest).toBeUndefined()
    click(editor.querySelector('button[aria-label="Remove Additional Kafka client properties request.timeout.ms"]')!)
    expect(lastRow()).toEqual({ override_fields: ['kafka.properties.request.timeout.ms'] })
  })

  it('shows a whole v1 section in the panel list with its own reset', () => {
    mount({ group: KAFKA_GROUP, initial: runtimeDraftFromOverride({ kafka: { topic: 'v1-topic' } }) })
    clickTab('Kafka')
    // A v1 section replaces the script section whole, so the script's acks no
    // longer apply: the field reads as set by this layer with no value, not Inherit.
    expect(control<HTMLSelectElement>('Producer acknowledgement level', 'select').selectedOptions[0].textContent).toBe('Not set')
    expect(control<HTMLInputElement>('Default Kafka topic', 'input').value).toBe('v1-topic')
    const list = container.querySelector('[data-testid="runtime-override-panel-list"]')
    expect(list?.querySelector('[data-override-path="kafka"]')).not.toBeNull()
    click(list!.querySelector('button')!)
    expect(latest).toBeUndefined()
  })

  it('edits every websocket field as its own listed path', () => {
    mount({ group: { name: 'Socket', scriptId: 'w1', protocol: 'websocket', scriptConfig: { base_url: '', websocket: { read_timeout_ms: 15000, reconnect: true } } } })
    clickTab('WebSocket')
    expect(control<HTMLInputElement>('WebSocket read timeout (ms)', 'input').placeholder).toBe('15000')
    setInput(control<HTMLInputElement>('WebSocket write timeout (ms)', 'input'), '2000')
    setInput(control<HTMLInputElement>('Maximum WebSocket message size', 'input'), '4096')
    setInput(control<HTMLInputElement>('WebSocket subprotocol', 'input'), 'graphql-ws')
    setInput(control<HTMLInputElement>('WebSocket Origin header', 'input'), 'https://app.example.test')
    setSelect(control<HTMLSelectElement>('Enable per-message deflate compression', 'select'), 'true')
    setSelect(control<HTMLSelectElement>('Reconnect on unexpected close', 'select'), 'false')
    // The tab holds the whole section; the compact callback row beside the tabs is not repeated in it.
    expect(Array.from(container.querySelectorAll('label > span')).filter(span => /callback/i.test(span.textContent || '')).map(span => span.textContent)).toEqual(['Callback dispatch interval (ms)'])
    expect(lastRow()).toEqual({
      websocket: { write_timeout_ms: 2000, max_message_size: 4096, subprotocol: 'graphql-ws', origin: 'https://app.example.test', compression: true, reconnect: false },
      override_fields: ['websocket.write_timeout_ms', 'websocket.max_message_size', 'websocket.subprotocol', 'websocket.origin', 'websocket.compression', 'websocket.reconnect'],
    })
  })

  it('offers only the SQLite and PostgreSQL (pgx) database drivers', () => {
    mount({ group: { name: 'Ledger', scriptId: 'd1', protocol: 'database', scriptConfig: { base_url: '', database: { driver: 'sqlite' } } } })
    clickTab('Database')
    const driver = control<HTMLSelectElement>('Database driver', 'select')
    expect(Array.from(driver.options).map(option => option.value)).toEqual(['', 'sqlite', 'postgres'])
    expect(driver.options[0].textContent).toBe('Inherit (SQLite)')
    expect(control<HTMLInputElement>('Database connection string (DSN)', 'input').type).toBe('password')
  })

  it('pins the parameter delimiters together and resets both', () => {
    mount({ group: { name: 'Legacy Java', scriptId: 'jv', protocol: 'java_vuser', scriptConfig: { base_url: '', java_vuser: { action_timeout_ms: 60000 } } } })
    clickTab('Java Env')
    setInput(control<HTMLInputElement>('JVM startup timeout (ms)', 'input'), '45000')
    expect(lastRow()).toEqual({ java_vuser: { startup_timeout_ms: 45000 }, override_fields: ['java_vuser.startup_timeout_ms'] })
    expect(control<HTMLInputElement>('Java action timeout (ms)', 'input').placeholder).toBe('60000')
    setInput(control<HTMLInputElement>('Parameter left delimiter', 'input'), '<<')
    expect(lastRow()).toEqual({
      java_vuser: { startup_timeout_ms: 45000, param_left_brace: '<<', param_right_brace: '}' },
      override_fields: ['java_vuser.startup_timeout_ms', 'java_vuser.param_left_brace', 'java_vuser.param_right_brace'],
    })
    click(resetButton('java_vuser.param_right_brace')!)
    expect(lastRow()).toEqual({ java_vuser: { startup_timeout_ms: 45000 }, override_fields: ['java_vuser.startup_timeout_ms'] })
  })

  it('clears both parameter delimiters together with Override with no value', () => {
    mount({ group: { name: 'LR Java', scriptId: 'jv', protocol: 'java_vuser', scriptConfig: { base_url: '', java_vuser: { param_left_brace: '<<', param_right_brace: '>>' } } } })
    clickTab('Java Env')
    const clear = Array.from(fieldFor('java_vuser.param_left_brace').querySelectorAll('button')).find(b => (b.textContent || '').trim() === 'Override with no value')
    expect(clear).toBeDefined()
    click(clear!)
    const row = lastRow()
    expect(row?.override_fields).toEqual(['java_vuser.param_left_brace', 'java_vuser.param_right_brace'])
    expect(row?.java_vuser?.param_left_brace ?? '').toBe('')
    expect(row?.java_vuser?.param_right_brace ?? '').toBe('')
    // Typing a delimiter again re-pins the cleared partner at the script's value, never one empty and one set.
    typeInto(control<HTMLInputElement>('Parameter left delimiter', 'input'), '[')
    expect(lastRow()?.java_vuser).toEqual({ param_left_brace: '[', param_right_brace: '>>' })
  })

  it('edits an Appium capability with its JSON type and keeps a masked capability unchanged', () => {
    const group: RuntimeEditorGroup = { name: 'Device', scriptId: 'a1', protocol: 'appium', scriptConfig: { base_url: '', appium: { capabilities: { platformName: 'iOS' } } } }
    mount({ group, initial: runtimeDraftFromOverride({ appium: { capabilities: { 'browserstack.key': '[REDACTED]' } }, override_fields: ['appium.capabilities.browserstack.key'] }) })
    clickTab('Appium')
    const masked = container.querySelector('input[aria-label="Desired device capabilities browserstack.key value"]') as HTMLInputElement
    expect(masked.value).toBe('')
    expect(masked.placeholder).toBe('Stored secret (unchanged)')
    setInput(container.querySelector('input[aria-label="New Desired device capabilities key"]') as HTMLInputElement, 'appium:newCommandTimeout')
    setInput(container.querySelector('input[aria-label="New Desired device capabilities value"]') as HTMLInputElement, '120')
    setSelect(container.querySelector('select[aria-label="New Desired device capabilities value type"]') as HTMLSelectElement, 'json')
    click(button('Add entry'))
    expect(lastRow()).toEqual({
      appium: { capabilities: { 'browserstack.key': '[REDACTED]', 'appium:newCommandTimeout': 120 } },
      override_fields: ['appium.capabilities.browserstack.key', 'appium.capabilities.appium:newCommandTimeout'],
    })
  })

  it('says which targets Apply to groups skipped for a credential broker list', () => {
    const groups: RuntimeEditorGroup[] = [KAFKA_GROUP, { ...KAFKA_GROUP, name: 'Refunds stream', scriptId: 'k2' }]
    act(() => root.render(
      <RuntimeSettingsGroupEditor
        groups={groups}
        focusedIndex={0}
        onFocusChange={() => {}}
        overrides={[undefined, undefined]}
        onChange={() => {}}
        onApplyToGroups={() => ({ missingSOAPCredentialTargets: [], uncopyableCredentialTargets: [{ target: 1, paths: ['kafka.brokers'] }] })}
      />,
    ))
    click(button('Apply to groups…'))
    click(container.querySelector('.runtime-apply-target input')!)
    click(button('Apply'))
    const notice = container.querySelector('[data-testid="runtime-apply-notice"]')?.textContent ?? ''
    expect(notice).toContain('Not applied to Refunds stream')
    expect(notice).toContain('kafka.brokers')
  })

  it('shows a map key the scenario default sets key by key as inherited (Scenario default)', () => {
    const scenarioDefault = { kafka: { properties: { a: '1' } }, override_fields: ['kafka.properties.a'] } as RuntimeSettingsOverride
    mount({ group: KAFKA_GROUP, scenarioDefault })
    clickTab('Kafka')
    const a = container.querySelector('input[aria-label="Additional Kafka client properties a value"]') as HTMLInputElement | null
    expect(a?.value).toBe('1')
    // The script's keys still show beside it, and the map reads as the scenario default's.
    expect(container.querySelector('input[aria-label="Additional Kafka client properties client.rack value"]')).not.toBeNull()
    expect(badgeText('kafka.properties')).toBe('Scenario default')
    // Removing the inherited key lists it with no value, not a reset.
    click(container.querySelector('button[aria-label="Remove Additional Kafka client properties a"]')!)
    expect(lastRow()).toEqual({ override_fields: ['kafka.properties.a'] })
  })

  it('keeps each typed line of a list field, so a second broker can be typed', () => {
    mount({ group: KAFKA_GROUP })
    clickTab('Kafka')
    const brokers = control<HTMLTextAreaElement>('Bootstrap broker addresses', 'textarea')
    typeInto(brokers, 'k1:9092\n')
    expect(brokers.value).toBe('k1:9092\n')
    typeInto(brokers, 'k2:9092')
    expect(lastRow()).toEqual({ kafka: { brokers: ['k1:9092', 'k2:9092'] }, override_fields: ['kafka.brokers'] })
    blur(brokers)
    expect(brokers.value).toBe('k1:9092\nk2:9092')
  })

  it('keeps a capability typed like a number as a string (platformVersion 17.0)', () => {
    const group: RuntimeEditorGroup = { name: 'Device', scriptId: 'a1', protocol: 'appium', scriptConfig: { base_url: '', appium: { capabilities: { 'appium:platformVersion': '16.4', 'appium:newCommandTimeout': 60 } } } }
    mount({ group })
    clickTab('Appium')
    const version = container.querySelector('input[aria-label="Desired device capabilities appium:platformVersion value"]') as HTMLInputElement
    act(() => {
      Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!.call(version, '')
      version.dispatchEvent(new Event('input', { bubbles: true }))
    })
    typeInto(version, '17.0')
    expect(version.value).toBe('17.0')
    expect(lastRow()).toEqual({ appium: { capabilities: { 'appium:platformVersion': '17.0' } }, override_fields: ['appium.capabilities.appium:platformVersion'] })
    // A numeric capability keeps its number type.
    const timeout = container.querySelector('input[aria-label="Desired device capabilities appium:newCommandTimeout value"]') as HTMLInputElement
    typeInto(timeout, '0')
    expect(lastRow()?.appium?.capabilities?.['appium:newCommandTimeout']).toBe(600)
    // A new entry is text unless JSON is picked.
    setInput(container.querySelector('input[aria-label="New Desired device capabilities key"]') as HTMLInputElement, 'appium:udid')
    setInput(container.querySelector('input[aria-label="New Desired device capabilities value"]') as HTMLInputElement, '00008030')
    click(button('Add entry'))
    expect(lastRow()?.appium?.capabilities?.['appium:udid']).toBe('00008030')
  })

  it('shows a removed inherited map key with Reset to inherited', () => {
    mount({ group: KAFKA_GROUP })
    clickTab('Kafka')
    click(container.querySelector('button[aria-label="Remove Additional Kafka client properties request.timeout.ms"]')!)
    const removed = container.querySelector('[data-override-path="kafka.properties.request.timeout.ms"]')
    expect(removed?.textContent).toContain('Removed')
    click(container.querySelector('button[aria-label="Reset Additional Kafka client properties request.timeout.ms to inherited"]')!)
    expect(latest).toBeUndefined()
    expect((container.querySelector('input[aria-label="Additional Kafka client properties request.timeout.ms value"]') as HTMLInputElement).value).toBe('30000')
  })

  it('offers only client_credentials for the Kafka OAuth mode', () => {
    mount({ group: KAFKA_GROUP })
    clickTab('Kafka')
    const mode = control<HTMLSelectElement>('Kafka OAuth mode', 'select')
    expect(Array.from(mode.options).map(option => option.value)).toEqual(['', 'client_credentials'])
  })

  it('shows gRPC reflection as inherited Disabled, the engine default', () => {
    mount({ group: { name: 'Orders RPC', scriptId: 'g1', protocol: 'grpc', scriptConfig: { base_url: '' } } })
    clickTab('gRPC')
    expect(control<HTMLSelectElement>('Use gRPC server reflection', 'select').options[0].textContent).toBe('Inherit (Disabled)')
  })

  it('overrides an inherited Kafka profile back to none (plain Kafka), distinct from Inherit', () => {
    const group: RuntimeEditorGroup = { ...KAFKA_GROUP, scriptConfig: { base_url: '', kafka: { profile: 'eventhubs', serialization: 'avro', sasl_mechanism: 'oauthbearer' } } }
    mount({ group })
    clickTab('Kafka')
    const profile = control<HTMLSelectElement>('Kafka broker profile', 'select')
    expect(profile.options[0].textContent).toBe('Inherit (Azure Event Hubs)')
    setSelect(profile, optionByText(profile, 'None (plain Kafka)').value)
    expect(lastRow()?.override_fields).toEqual(['kafka.profile'])
    expect(lastRow()?.kafka?.profile ?? '').toBe('')
    const reread = control<HTMLSelectElement>('Kafka broker profile', 'select')
    expect(reread.selectedOptions[0].textContent).toBe('None (plain Kafka)')
    expect(badgeText('kafka.profile')).toBe('Group override')
    setSelect(control<HTMLSelectElement>('Record value serialization', 'select'), optionByText(control<HTMLSelectElement>('Record value serialization', 'select'), 'None (raw)').value)
    setSelect(control<HTMLSelectElement>('SASL authentication mechanism', 'select'), optionByText(control<HTMLSelectElement>('SASL authentication mechanism', 'select'), 'None (no SASL)').value)
    expect(lastRow()?.override_fields).toEqual(['kafka.profile', 'kafka.serialization', 'kafka.sasl_mechanism'])
    // Inherit still resets the path.
    setSelect(control<HTMLSelectElement>('Kafka broker profile', 'select'), '')
    expect(lastRow()?.override_fields).toEqual(['kafka.serialization', 'kafka.sasl_mechanism'])
  })

  it('reads a stored listed-with-no-value select as the explicit none choice', () => {
    mount({ group: KAFKA_GROUP, initial: runtimeDraftFromOverride({ override_fields: ['kafka.isolation_level'] }) })
    clickTab('Kafka')
    expect(control<HTMLSelectElement>('Consumer transaction isolation level', 'select').selectedOptions[0].textContent).toBe('Not set')
  })

  it('clears an inherited Kafka transactional ID with Override with no value', () => {
    const group: RuntimeEditorGroup = { ...KAFKA_GROUP, scriptConfig: { base_url: '', kafka: { transactional_id: 'orders-tx', schema: '{"type":"string"}' } } }
    mount({ group })
    clickTab('Kafka')
    const field = fieldFor('kafka.transactional_id')
    const clear = Array.from(field.querySelectorAll('button')).find(b => (b.textContent || '').trim() === 'Override with no value')
    expect(clear).toBeDefined()
    click(clear!)
    expect(lastRow()?.override_fields).toEqual(['kafka.transactional_id'])
    expect(lastRow()?.kafka?.transactional_id ?? '').toBe('')
    const input = control<HTMLInputElement>('Producer transactional ID', 'input')
    expect(input.value).toBe('')
    expect(input.placeholder).toBe('No value (overrides inherited)')
    // Typing and erasing returns to the chosen clear, not to inherit.
    typeInto(input, 'x')
    expect(lastRow()?.kafka?.transactional_id).toBe('x')
    setInput(control<HTMLInputElement>('Producer transactional ID', 'input'), '')
    expect(lastRow()?.override_fields).toEqual(['kafka.transactional_id'])
    expect(lastRow()?.kafka?.transactional_id ?? '').toBe('')
    // The textarea field offers the same explicit clear.
    const schemaClear = Array.from(fieldFor('kafka.schema').querySelectorAll('button')).find(b => (b.textContent || '').trim() === 'Override with no value')
    click(schemaClear!)
    expect(lastRow()?.override_fields).toEqual(['kafka.transactional_id', 'kafka.schema'])
  })

  it('erasing a typed value on an inheriting text field returns it to inherit', () => {
    mount({ group: KAFKA_GROUP })
    clickTab('Kafka')
    const topic = control<HTMLInputElement>('Default Kafka topic', 'input')
    typeInto(topic, 'p')
    expect(lastRow()).toEqual({ kafka: { topic: 'p' }, override_fields: ['kafka.topic'] })
    setInput(control<HTMLInputElement>('Default Kafka topic', 'input'), '')
    expect(latest).toBeUndefined()
  })

  it('types a credential-named map value in a password input with the mask lifecycle', () => {
    mount({ group: GRPC_GROUP })
    clickTab('gRPC')
    const auth = container.querySelector('input[aria-label="Default gRPC metadata authorization value"]') as HTMLInputElement
    expect(auth.type).toBe('password')
    expect((container.querySelector('input[aria-label="Default gRPC metadata x-trace value"]') as HTMLInputElement).type).toBe('text')
    // A new entry under a credential key is typed hidden too.
    const newValue = () => container.querySelector('input[aria-label="New Default gRPC metadata value"]') as HTMLInputElement
    expect(newValue().type).toBe('text')
    setInput(container.querySelector('input[aria-label="New Default gRPC metadata key"]') as HTMLInputElement, 'x-api-key')
    expect(newValue().type).toBe('password')
    act(() => root.unmount())
    root = createRoot(container)
    // A stored masked JMS property keeps its mask until replaced; erasing restores it.
    mount({ group: JMS_GROUP, initial: runtimeDraftFromOverride({ jms: { properties: { password: '[REDACTED]' } }, override_fields: ['jms.properties.password'] }) })
    clickTab('JMS')
    const password = container.querySelector('input[aria-label="JMS connection properties password value"]') as HTMLInputElement
    expect(password.type).toBe('password')
    expect(password.placeholder).toBe('Stored secret (unchanged)')
    typeInto(password, 'ab')
    expect(lastRow()?.jms?.properties?.password).toBe('ab')
    setInput(container.querySelector('input[aria-label="JMS connection properties password value"]') as HTMLInputElement, '')
    expect(lastRow()?.jms?.properties?.password).toBe('[REDACTED]')
    // An Appium device-cloud key capability is hidden as well.
    act(() => root.unmount())
    root = createRoot(container)
    mount({ group: { name: 'Device', scriptId: 'a1', protocol: 'appium', scriptConfig: { base_url: '', appium: { capabilities: { 'browserstack.key': 'k', platformName: 'iOS' } } } } })
    clickTab('Appium')
    expect((container.querySelector('input[aria-label="Desired device capabilities browserstack.key value"]') as HTMLInputElement).type).toBe('password')
    expect((container.querySelector('input[aria-label="Desired device capabilities platformName value"]') as HTMLInputElement).type).toBe('text')
  })

  it('offers the native protocol tabs in a plan entry editor', () => {
    mount({ group: { name: 'Entry overrides', scriptId: 'plan-entry-overrides' }, applicableProtocols: [...PLAN_ENTRY_RUNTIME_PROTOCOLS], overrideLabel: 'Entry override' })
    const tabs = Array.from(container.querySelectorAll('[role="tab"]')).map(tab => (tab.textContent || '').trim())
    expect(tabs).toEqual(expect.arrayContaining(['Kafka', 'JMS', 'Event Hubs', 'gRPC', 'Database', 'Network', 'Appium', 'Java over HTTP']))
  })

  it('reads the inherited protocol values through the shared resolver (guard)', () => {
    const scenarioDefault = { kafka: { topic: 'scenario-topic' }, override_fields: ['kafka.topic'] } as RuntimeSettingsOverride
    const engineDefaults = { base_url: '', kafka: { timeout_ms: 30000, client_id: 'engine-client' } }
    mount({ group: KAFKA_GROUP, scenarioDefault, engineDefaults })
    clickTab('Kafka')
    const layers = { scenario: scenarioDefault, script: KAFKA_GROUP.scriptConfig, engine: engineDefaults }
    const cases: Array<[string, string, string]> = [
      ['kafka.topic', 'Default Kafka topic', 'Scenario default'],
      ['kafka.client_id', 'Kafka client ID', 'Engine default'],
    ]
    for (const [path, label, badge] of cases) {
      const resolved = effectiveRuntimeValue(path, layers)
      expect([path, control<HTMLInputElement>(label, 'input').placeholder]).toEqual([path, String(resolved.value)])
      expect([path, badgeText(path)]).toEqual([path, badge])
    }
  })

  it('keeps the tab free of its own layer resolution (guard)', async () => {
    // @ts-expect-error Vitest runs in Node, but the dashboard app tsconfig intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
    const source = readFileSync(`${cwd}/src/components/runtimeTabs/ProtocolOverrideTab.tsx`, 'utf8') as string
    // The host hands the tab inherited/effective sections resolved by the shared
    // model (materializeRuntimeSettingsForApply + overlayRuntimeOverrideSection);
    // the tab never reads a layer itself.
    for (const forbidden of ['scriptConfig', 'engineDefaults', 'scenarioDefault', 'materializeRuntimeSettingsForApply', 'runtimeOverrideFromScriptConfig', 'overlayRuntimeOverrideSection', 'resolveRuntimeRowsValue']) {
      expect([forbidden, source.includes(forbidden)]).toEqual([forbidden, false])
    }
  })
})

describe('RuntimeSettingsGroupEditor protocol override tabs (round-4 fixes)', () => {
  it('keeps a lower-layer key cleared when removing a key of a map the row lists whole', () => {
    const group: RuntimeEditorGroup = { name: 'Orders RPC', scriptId: 'g1', protocol: 'grpc', scriptConfig: { base_url: '', grpc: { metadata: { b: '2' } } } }
    // A v1 row (or Apply to groups) lists grpc.metadata whole with {a}, clearing the script's b.
    mount({ group, initial: runtimeDraftFromOverride({ grpc: { metadata: { a: '1' } }, override_fields: ['grpc.metadata'] }) })
    clickTab('gRPC')
    click(container.querySelector('button[aria-label="Remove Default gRPC metadata a"]')!)
    expect(lastRow()).toEqual({ override_fields: ['grpc.metadata.b'] })
    expect(effectiveRuntimeValue('grpc.metadata', { group: latest, script: group.scriptConfig }).value ?? {}).toEqual({})
  })

  it('keeps the partner delimiter override while one delimiter is backspaced to empty and retyped', () => {
    mount({
      group: { name: 'LR Java', scriptId: 'jv', protocol: 'java_vuser', scriptConfig: { base_url: '', java_vuser: { param_left_brace: '{', param_right_brace: '}' } } },
      initial: runtimeDraftFromOverride({ java_vuser: { param_left_brace: '<%', param_right_brace: '%>' }, override_fields: ['java_vuser.param_left_brace', 'java_vuser.param_right_brace'] }),
    })
    clickTab('Java Env')
    const left = control<HTMLInputElement>('Parameter left delimiter', 'input')
    setInput(left, '<')
    setInput(left, '')
    expect(lastRow()?.java_vuser?.param_right_brace).toBe('%>')
    typeInto(control<HTMLInputElement>('Parameter left delimiter', 'input'), '[')
    expect(lastRow()?.java_vuser).toEqual({ param_left_brace: '[', param_right_brace: '%>' })
  })

  it('never pins an empty partner delimiter when a lower layer cleared the pair', () => {
    mount({
      group: { name: 'LR Java', scriptId: 'jv', protocol: 'java_vuser', scriptConfig: { base_url: '', java_vuser: { param_left_brace: '<<', param_right_brace: '>>' } } },
      scenarioDefault: { java_vuser: { param_left_brace: '', param_right_brace: '' }, override_fields: ['java_vuser.param_left_brace', 'java_vuser.param_right_brace'] } as RuntimeSettingsOverride,
    })
    clickTab('Java Env')
    typeInto(control<HTMLInputElement>('Parameter left delimiter', 'input'), '<')
    expect(lastRow()?.java_vuser).toEqual({ param_left_brace: '<', param_right_brace: '}' })
  })

  it('returns an inherited masked map credential to inheriting when typed text is erased', () => {
    mount({
      group: JMS_GROUP,
      scenarioDefault: { jms: { properties: { password: '[REDACTED]' } }, override_fields: ['jms.properties.password'] } as RuntimeSettingsOverride,
    })
    clickTab('JMS')
    const password = () => container.querySelector('input[aria-label="JMS connection properties password value"]') as HTMLInputElement
    expect(password().placeholder).toBe('Stored secret (unchanged)')
    typeInto(password(), 'a')
    expect(lastRow()?.jms?.properties?.password).toBe('a')
    setInput(password(), '')
    expect(lastRow()?.override_fields ?? []).not.toContain('jms.properties.password')
    expect(lastRow()?.jms?.properties?.password).toBeUndefined()
  })

  it('restores a masked secret name when typed text is erased', () => {
    mount({ group: KAFKA_GROUP, initial: runtimeDraftFromOverride({ kafka: { sasl_password_secret: '[REDACTED]' }, override_fields: ['kafka.sasl_password_secret'] }) })
    clickTab('Kafka')
    const input = () => control<HTMLInputElement>('SASL password secret name', 'input')
    expect(input().placeholder).toBe('Stored value (unchanged)')
    typeInto(input(), 'x')
    expect(lastRow()?.kafka?.sasl_password_secret).toBe('x')
    setInput(input(), '')
    expect(lastRow()).toEqual({ kafka: { sasl_password_secret: '[REDACTED]' }, override_fields: ['kafka.sasl_password_secret'] })
  })

  it('offers no java_http value the embedded validator rejects, but lets a stored one be cleared', () => {
    const group: RuntimeEditorGroup = { name: 'Java RMI', scriptId: 'jh', protocol: 'java_http', scriptConfig: { base_url: 'https://java.example.test', java_http: { request_mapping: { id: '$.id' } } } }
    mount({ group, initial: runtimeDraftFromOverride({ java_http: { object_schema: 'com.example.Order' }, override_fields: ['java_http.object_schema'] }) })
    clickTab('Java over HTTP')
    const schema = control<HTMLSelectElement>('Java object class or schema', 'select')
    expect(Array.from(schema.options).map(option => option.textContent)).toEqual([
      'Inherit (Default)', 'Override with no value', 'com.example.Order (stored value, not supported here)',
    ])
    expect(schema.value).toBe('com.example.Order')
    const extractor = control<HTMLSelectElement>('Default response extractor type', 'select')
    expect(Array.from(extractor.options).map(option => option.textContent)).toEqual(['Inherit (Default)', 'Override with no value'])
    // Request mapping only removes what a lower layer supplies.
    expect(container.querySelector('input[aria-label="New Request field mapping key"]')).toBeNull()
    expect((container.querySelector('input[aria-label="Request field mapping id value"]') as HTMLInputElement).readOnly).toBe(true)
    setSelect(schema, '')
    expect(lastRow()?.override_fields ?? []).not.toContain('java_http.object_schema')
  })

  it('drops the partner delimiter it pinned when the typed delimiter is erased back to inherit', () => {
    mount({ group: { name: 'LR Java', scriptId: 'jv', protocol: 'java_vuser', scriptConfig: { base_url: '', java_vuser: { param_left_brace: '{', param_right_brace: '}' } } } })
    clickTab('Java Env')
    const left = () => control<HTMLInputElement>('Parameter left delimiter', 'input')
    typeInto(left(), '[')
    expect(lastRow()?.java_vuser).toEqual({ param_left_brace: '[', param_right_brace: '}' })
    setInput(left(), '')
    expect((lastRow()?.override_fields ?? []).filter(path => path.startsWith('java_vuser.param_'))).toEqual([])
    expect(lastRow()?.java_vuser?.param_right_brace).toBeUndefined()
  })

  it('keeps a partner delimiter the user edited after the auto-pin when the typed one is erased', () => {
    mount({ group: { name: 'LR Java', scriptId: 'jv', protocol: 'java_vuser', scriptConfig: { base_url: '', java_vuser: { param_left_brace: '{', param_right_brace: '}' } } } })
    clickTab('Java Env')
    const left = () => control<HTMLInputElement>('Parameter left delimiter', 'input')
    const right = () => control<HTMLInputElement>('Parameter right delimiter', 'input')
    typeInto(left(), '[')
    blur(left())
    setInput(right(), ']')
    blur(right())
    setInput(left(), '')
    expect(lastRow()?.java_vuser?.param_right_brace).toBe(']')
  })

  it('shows the Event Hubs profile defaults as the inherited Kafka security protocol, SASL mechanism and username', () => {
    const group: RuntimeEditorGroup = { ...KAFKA_GROUP, scriptConfig: { base_url: '', kafka: { profile: 'eventhubs', topic: 'orders' } } }
    mount({ group })
    clickTab('Kafka')
    expect(control<HTMLSelectElement>('Kafka security protocol', 'select').options[0].textContent).toBe('Inherit (sasl_ssl)')
    expect(control<HTMLSelectElement>('SASL authentication mechanism', 'select').options[0].textContent).toBe('Inherit (plain)')
    expect(control<HTMLInputElement>('SASL username', 'input').placeholder).toBe('$ConnectionString')
    // Leaving the profile at this layer brings back the plain Kafka defaults.
    const profile = control<HTMLSelectElement>('Kafka broker profile', 'select')
    setSelect(profile, optionByText(profile, 'None (plain Kafka)').value)
    expect(control<HTMLSelectElement>('Kafka security protocol', 'select').options[0].textContent).toBe('Inherit (plaintext)')
    expect(control<HTMLSelectElement>('SASL authentication mechanism', 'select').options[0].textContent).toBe('Inherit (Default)')
    expect(control<HTMLInputElement>('SASL username', 'input').placeholder).not.toBe('$ConnectionString')
  })

  it('shows the Event Hubs profile defaults when this layer sets the profile', () => {
    const group: RuntimeEditorGroup = { ...KAFKA_GROUP, scriptConfig: { base_url: '', kafka: { topic: 'orders' } } }
    mount({ group, initial: runtimeDraftFromOverride({ kafka: { profile: 'eventhubs' }, override_fields: ['kafka.profile'] }) })
    clickTab('Kafka')
    expect(control<HTMLSelectElement>('Kafka security protocol', 'select').options[0].textContent).toBe('Inherit (sasl_ssl)')
    expect(control<HTMLSelectElement>('SASL authentication mechanism', 'select').options[0].textContent).toBe('Inherit (plain)')
  })

  it('drops an auto-pinned partner delimiter on erase even after a blur', () => {
    mount({ group: { name: 'LR Java', scriptId: 'jv', protocol: 'java_vuser', scriptConfig: { base_url: '', java_vuser: { param_left_brace: '{', param_right_brace: '}' } } } })
    clickTab('Java Env')
    const left = () => control<HTMLInputElement>('Parameter left delimiter', 'input')
    typeInto(left(), '[')
    blur(left())
    setInput(left(), '')
    expect((lastRow()?.override_fields ?? []).filter(path => path.startsWith('java_vuser.param_'))).toEqual([])
    expect(lastRow()?.java_vuser?.param_right_brace).toBeUndefined()
  })

  it('drops an auto-pinned partner delimiter on erase after a tab switch remounts the field', () => {
    mount({ group: { name: 'LR Java', scriptId: 'jv', protocol: 'java_vuser', scriptConfig: { base_url: '', java_vuser: { param_left_brace: '{', param_right_brace: '}' } } } })
    clickTab('Java Env')
    typeInto(control<HTMLInputElement>('Parameter left delimiter', 'input'), '[')
    clickTab('Think Time')
    clickTab('Java Env')
    setInput(control<HTMLInputElement>('Parameter left delimiter', 'input'), '')
    expect((lastRow()?.override_fields ?? []).filter(path => path.startsWith('java_vuser.param_'))).toEqual([])
  })

  it('drops a partner pinned at the default beside a pair a lower layer cleared', () => {
    mount({
      group: { name: 'LR Java', scriptId: 'jv', protocol: 'java_vuser', scriptConfig: { base_url: '', java_vuser: { param_left_brace: '<<', param_right_brace: '>>' } } },
      scenarioDefault: { java_vuser: { param_left_brace: '', param_right_brace: '' }, override_fields: ['java_vuser.param_left_brace', 'java_vuser.param_right_brace'] } as RuntimeSettingsOverride,
    })
    clickTab('Java Env')
    const left = () => control<HTMLInputElement>('Parameter left delimiter', 'input')
    typeInto(left(), '<')
    blur(left())
    setInput(left(), '')
    expect((lastRow()?.override_fields ?? []).filter(path => path.startsWith('java_vuser.param_'))).toEqual([])
  })

  it('reads the Event Hubs profile case-insensitively, as the validator and engine do', () => {
    const group: RuntimeEditorGroup = { ...KAFKA_GROUP, scriptConfig: { base_url: '', kafka: { profile: ' EventHubs', topic: 'orders' } } }
    mount({ group })
    clickTab('Kafka')
    expect(control<HTMLSelectElement>('Kafka security protocol', 'select').options[0].textContent).toBe('Inherit (sasl_ssl)')
    expect(control<HTMLSelectElement>('SASL authentication mechanism', 'select').options[0].textContent).toBe('Inherit (plain)')
    expect(control<HTMLInputElement>('SASL username', 'input').placeholder).toBe('$ConnectionString')
  })

  it('labels the explicit no-value choice with the Event Hubs profile default it runs with', () => {
    const group: RuntimeEditorGroup = { ...KAFKA_GROUP, scriptConfig: { base_url: '', kafka: { profile: 'eventhubs', topic: 'orders' } } }
    mount({ group, initial: runtimeDraftFromOverride({ kafka: { security_protocol: '' }, override_fields: ['kafka.security_protocol'] }) })
    clickTab('Kafka')
    const mechanism = control<HTMLSelectElement>('SASL authentication mechanism', 'select')
    const labels = Array.from(mechanism.options).map(option => option.textContent)
    expect(labels).not.toContain('None (no SASL)')
    expect(labels).toContain('No value (profile default: plain)')
    setSelect(mechanism, optionByText(mechanism, 'No value (profile default: plain)').value)
    expect(lastRow()?.override_fields ?? []).toContain('kafka.sasl_mechanism')
    const protocol = control<HTMLSelectElement>('Kafka security protocol', 'select')
    expect(protocol.options[protocol.selectedIndex].textContent).toBe('No value (profile default: sasl_ssl)')
  })
})
