// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import { ScriptRuntimeSettingsView } from './ScriptRuntimeSettingsView'
import type { HTTPTransportTabId, RuntimeSettingsSectionId } from './scriptEditorRuntimeSettingsModel'
import AppiumRuntimeFields from './AppiumRuntimeFields'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let container: HTMLDivElement
let root: Root

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})

afterEach(() => {
  act(() => root.unmount())
  container.remove()
})

function script(protocol: string, config: Partial<ScriptDefinition['config']>): ScriptDefinition {
  return { name: `${protocol} script`, version: '1', protocol, actions: [], config: { base_url: '', ...config } as ScriptDefinition['config'] }
}

function render(value: ScriptDefinition, activeSection: RuntimeSettingsSectionId, activeHTTPTransportTab: HTTPTransportTabId = 'timeouts', onUpdateField = vi.fn()) {
  act(() => root.render(
    <ScriptRuntimeSettingsView
      activeHTTPTransportTab={activeHTTPTransportTab}
      activeSection={activeSection}
      onSelectHTTPTransportTab={vi.fn()}
      onSelectSection={vi.fn()}
      onUpdateField={onUpdateField}
      script={value}
    />,
  ))
  return onUpdateField
}

function field<T extends HTMLElement = HTMLInputElement>(path: string, selector = 'input'): T {
  const node = container.querySelector<T>(`[data-catalog-path="${path}"] ${selector}`) ?? container.querySelector<T>(`${selector}[data-catalog-path="${path}"]`)
  expect(node, path).not.toBeNull()
  return node!
}

function type(input: HTMLInputElement | HTMLTextAreaElement, value: string) {
  act(() => {
    const prototype = input instanceof HTMLTextAreaElement ? window.HTMLTextAreaElement.prototype : window.HTMLInputElement.prototype
    Object.getOwnPropertyDescriptor(prototype, 'value')?.set?.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

function choose(select: HTMLSelectElement, value: string) {
  act(() => {
    Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')?.set?.call(select, value)
    select.dispatchEvent(new Event('change', { bubbles: true }))
  })
}

function click(element: HTMLElement) {
  act(() => element.click())
}

function button(text: string): HTMLButtonElement {
  const found = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(candidate => candidate.textContent?.trim() === text)
  expect(found, text).toBeTruthy()
  return found!
}

describe('Kafka runtime section', () => {
  it('appears only for Kafka scripts and drops the Base URL field', () => {
    render(script('kafka', {}), 'kafka')
    expect(Array.from(container.querySelectorAll('[role="tab"]')).map(tab => tab.id)).toContain('runtime-settings-tab-kafka')
    expect(container.querySelector('[data-testid="kafka-runtime-section"]')).not.toBeNull()
    expect(container.querySelector('[data-author-field="base_url"]')).toBeNull()

    render(script('http', {}), 'kafka')
    expect(Array.from(container.querySelectorAll('[role="tab"]')).map(tab => tab.id)).not.toContain('runtime-settings-tab-kafka')
    expect(container.querySelector('[data-testid="kafka-runtime-section"]')).toBeNull()
  })

  it('edits brokers and producer batching without dropping the rest of config.kafka', () => {
    const onUpdateField = render(script('kafka', { kafka: { topic: 'orders', mock: true } }), 'kafka')
    type(field<HTMLTextAreaElement>('config.kafka.brokers', 'textarea'), 'b1:9092\nb2:9092')
    expect(onUpdateField).toHaveBeenLastCalledWith('config.kafka', { topic: 'orders', mock: true, brokers: ['b1:9092', 'b2:9092'] })
    type(field('config.kafka.producer.retries'), '0')
    expect(onUpdateField).toHaveBeenLastCalledWith('config.kafka', { topic: 'orders', mock: true, producer: { retries: 0 } })
  })

  it('turns OAuth on with the only accepted mode and removes the whole block when turned off', () => {
    const onUpdateField = render(script('kafka', { kafka: { sasl_mechanism: 'oauthbearer' } }), 'kafka')
    expect(container.querySelector('[data-testid="kafka-oauth-fields"]')).toBeNull()
    click(field('config.kafka.oauth.mode'))
    expect(onUpdateField).toHaveBeenLastCalledWith('config.kafka', { sasl_mechanism: 'oauthbearer', oauth: { mode: 'client_credentials' } })

    render(script('kafka', { kafka: { sasl_mechanism: 'oauthbearer', oauth: { mode: 'client_credentials', client_id: 'svc' } } }), 'kafka', 'timeouts', onUpdateField)
    expect(container.querySelector('[data-testid="kafka-oauth-fields"]')).not.toBeNull()
    click(field('config.kafka.oauth.mode'))
    expect(onUpdateField).toHaveBeenLastCalledWith('config.kafka', { sasl_mechanism: 'oauthbearer' })
  })

  it('keeps a script-sourced SASL password readable and labels a secret reference', () => {
    render(script('kafka', { kafka: { sasl_password: 'imported-literal', oauth: { mode: 'client_credentials', client_secret_ref: 'KAFKA_OAUTH' } as never } }), 'kafka')
    const password = field('config.kafka.sasl_password')
    expect(password.type).toBe('text')
    expect(password.value).toBe('imported-literal')
    expect(container.querySelector('[data-catalog-path="config.kafka.sasl_password"] [data-secret-reference-note]')).toBeNull()
    expect(container.querySelector('[data-catalog-path="config.kafka.oauth.client_secret_ref"] [data-secret-reference-note]')?.textContent).toContain('KAFKA_OAUTH')

    render(script('kafka', { kafka: { sasl_password: '{{secret.KAFKA_PASSWORD}}' } }), 'kafka')
    expect(field('config.kafka.sasl_password').value).toBe('{{secret.KAFKA_PASSWORD}}')
    expect(container.querySelector('[data-catalog-path="config.kafka.sasl_password"] [data-secret-reference-note]')?.textContent).toContain('KAFKA_PASSWORD')
  })

  it('shows backend defaults for unset selects and removes preserved non-executable properties', () => {
    const onUpdateField = render(script('kafka', { kafka: { topic: 'orders', properties: { 'linger.ms': '5' } } }), 'kafka')
    expect(field<HTMLSelectElement>('config.kafka.acks', 'select').value).toBe('all')
    expect(field<HTMLSelectElement>('config.kafka.compression', 'select').value).toBe('none')
    expect(field<HTMLSelectElement>('config.kafka.auto_offset_reset', 'select').value).toBe('latest')
    click(button('Remove preserved properties'))
    expect(onUpdateField).toHaveBeenLastCalledWith('config.kafka', { topic: 'orders' })
  })
})

describe('Database runtime fields', () => {
  it('offers only sqlite and postgres, shows an unset driver as unset and a stored unsupported one as stored', () => {
    render(script('database', { database: {} }), 'script_config')
    const driver = field<HTMLSelectElement>('config.database.driver', 'select')
    expect(driver.value).toBe('')
    expect(Array.from(driver.options).map(option => option.value)).toEqual(['', 'sqlite', 'postgres'])

    render(script('database', { database: { driver: 'mysql' } }), 'script_config')
    const stored = field<HTMLSelectElement>('config.database.driver', 'select')
    expect(stored.value).toBe('mysql')
    expect(Array.from(stored.options).map(option => option.value)).toEqual(['sqlite', 'postgres', 'mysql'])
  })

  it('shows a stored sqlite3 driver as SQLite, not as unsupported', () => {
    render(script('database', { database: { driver: 'sqlite3', dsn: ':memory:' } }), 'script_config')
    const driver = field<HTMLSelectElement>('config.database.driver', 'select')
    expect(driver.value).toBe('sqlite3')
    expect(driver.selectedOptions[0]?.textContent).toBe('SQLite (in-memory)')
    expect(Array.from(driver.options).some(option => option.textContent?.includes('not supported'))).toBe(false)
  })

  it('switches the DSN placeholder to the selected driver and keeps the DSN readable', () => {
    const onUpdateField = render(script('database', { database: { driver: 'postgres', dsn: 'postgres://app:pw@db.example.test/orders' } }), 'script_config')
    const dsn = field('config.database.dsn')
    expect(dsn.type).toBe('text')
    expect(dsn.value).toBe('postgres://app:pw@db.example.test/orders')
    expect(dsn.placeholder).toMatch(/^postgres:\/\//)
    choose(field<HTMLSelectElement>('config.database.driver', 'select'), 'sqlite')
    expect(onUpdateField).toHaveBeenLastCalledWith('config.database.driver', 'sqlite')
  })

  it('shows unset read-only and rollback guards as off, as the engine runs them', () => {
    const onUpdateField = render(script('database', { database: { driver: 'sqlite' } }), 'database')
    const checkbox = (text: string) => Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')).find(input => input.closest('label')?.textContent?.includes(text))!
    expect(checkbox('Read-only guard').checked).toBe(false)
    expect(checkbox('Rollback open transaction on teardown').checked).toBe(false)
    expect(field('config.database.max_open_conns').value).toBe('')
    type(field('config.database.conn_max_lifetime_ms'), '60')
    expect(onUpdateField).toHaveBeenLastCalledWith('config.database', { driver: 'sqlite', conn_max_lifetime_ms: 60000 })
  })
})

describe('JMS, Event Hubs, gRPC and network fields', () => {
  it('edits JMS properties, keeps the password readable and shows unset mock as off', () => {
    const onUpdateField = render(script('jms-solace', { jms: { provider: 'solace', password: 'imported-pw', properties: { 'Solace_JMS_VPN': 'perf' } } }), 'jms')
    const password = field('config.jms.password')
    expect(password.type).toBe('text')
    expect(password.value).toBe('imported-pw')
    const mock = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')).find(input => input.closest('label')?.textContent?.includes('local mock broker'))!
    expect(mock.checked).toBe(false)
    const group = container.querySelector('[data-catalog-path="config.jms.properties"]')!
    type(group.querySelector<HTMLInputElement>('[aria-label="New JMS connection properties key"]')!, 'jms_ClientAck')
    type(group.querySelector<HTMLInputElement>('[aria-label="New JMS connection properties value"]')!, 'true')
    click(button('Add property'))
    expect(onUpdateField).toHaveBeenLastCalledWith('config.jms', { provider: 'solace', password: 'imported-pw', properties: { Solace_JMS_VPN: 'perf', jms_ClientAck: 'true' } })
  })

  it('refuses a duplicate JMS property key instead of overwriting the entry', () => {
    const onUpdateField = render(script('jms-solace', { jms: { provider: 'solace', properties: { a: '1', b: '2' } } }), 'jms')
    const group = container.querySelector('[data-catalog-path="config.jms.properties"]')!
    type(group.querySelector<HTMLInputElement>('[aria-label="JMS connection properties key 2"]')!, 'a')
    expect(onUpdateField).not.toHaveBeenCalled()
    type(group.querySelector<HTMLInputElement>('[aria-label="New JMS connection properties key"]')!, 'b')
    expect(button('Add property').disabled).toBe(true)
  })

  it('never commits an empty or whitespace-variant JMS property key on rename', () => {
    const onUpdateField = render(script('jms-solace', { jms: { provider: 'solace', properties: { foo: '1', bar: '2' } } }), 'jms')
    const group = container.querySelector('[data-catalog-path="config.jms.properties"]')!
    const key2 = () => group.querySelector<HTMLInputElement>('[aria-label="JMS connection properties key 2"]')!
    type(key2(), '')
    expect(onUpdateField).not.toHaveBeenCalled()
    expect(key2().value).toBe('')
    expect(key2().getAttribute('aria-invalid')).toBe('true')
    expect(group.querySelector('[role="alert"]')?.textContent).toContain('Enter a key')
    type(key2(), '   ')
    expect(onUpdateField).not.toHaveBeenCalled()
    type(key2(), 'foo ')
    expect(onUpdateField).not.toHaveBeenCalled()
    expect(group.querySelector('[role="alert"]')?.textContent).toContain('foo is already defined')
    // A valid rename is stored trimmed.
    type(key2(), ' baz ')
    expect(onUpdateField).toHaveBeenLastCalledWith('config.jms', { provider: 'solace', properties: { foo: '1', baz: '2' } })
  })

  it('refuses an empty or whitespace-variant gRPC metadata key on rename', () => {
    const onUpdateField = render(script('grpc', { grpc: { metadata: { authorization: 'x', tenant: 'a' } } }), 'grpc')
    const group = container.querySelector('[data-catalog-path="config.grpc.metadata"]')!
    const key1 = () => group.querySelector<HTMLInputElement>('[aria-label="Default call metadata key 1"]')!
    type(key1(), '')
    type(key1(), 'tenant ')
    expect(onUpdateField).not.toHaveBeenCalled()
    // Leaving the field drops the draft and shows the stored key again.
    act(() => { key1().dispatchEvent(new FocusEvent('focusout', { bubbles: true })) })
    expect(key1().value).toBe('authorization')
    // Adding compares trimmed keys too.
    type(group.querySelector<HTMLInputElement>('[aria-label="New Default call metadata key"]')!, ' tenant')
    expect(button('Add metadata').disabled).toBe(true)
  })

  it('keeps renaming the same JMS property row when the new key is integer-like', () => {
    let current = script('jms-solace', { jms: { provider: 'solace', properties: { a: 'x', b: 'y' } } })
    const onUpdateField = vi.fn((path: string, value: unknown) => {
      if (path === 'config.jms') {
        current = { ...current, config: { ...current.config, jms: value as ScriptDefinition['config']['jms'] } }
        render(current, 'jms', 'timeouts', onUpdateField)
      }
    })
    render(current, 'jms', 'timeouts', onUpdateField)
    const group = () => container.querySelector('[data-catalog-path="config.jms.properties"]')!
    const key = (row: number) => group().querySelector<HTMLInputElement>(`[aria-label="JMS connection properties key ${row}"]`)!
    const val = (row: number) => group().querySelector<HTMLInputElement>(`[aria-label="JMS connection properties value ${row}"]`)!
    type(key(2), '')
    type(key(2), '1')
    expect(current.config.jms?.properties).toEqual({ a: 'x', 1: 'y' })
    // The row being edited stays in place, with no duplicate-key alert.
    expect(key(2).value).toBe('1')
    expect(val(2).value).toBe('y')
    expect(key(1).value).toBe('a')
    expect(group().querySelector('[role="alert"]')).toBeNull()
    type(key(2), '12')
    expect(current.config.jms?.properties).toEqual({ a: 'x', 12: 'y' })
    expect(key(1).value).toBe('a')
    expect(key(2).value).toBe('12')
    expect(val(2).value).toBe('y')
  })

  it('offers Event Hubs partition-tail settings only in processor mode or when already stored', () => {
    render(script('eventhubs', { eventhubs: {} }), 'eventhubs')
    expect(container.querySelector('[data-catalog-path="config.eventhubs.tail_poll_interval_ms"]')).toBeNull()
    expect(container.querySelector('[data-catalog-path="config.eventhubs.observe_partition_tail"]')).toBeNull()
    render(script('eventhubs', { eventhubs: { consumer_mode: 'exclusive' } }), 'eventhubs')
    expect(container.querySelector('[data-catalog-path="config.eventhubs.tail_poll_interval_ms"]')).toBeNull()
    expect(container.querySelector('[data-catalog-path="config.eventhubs.observe_partition_tail"]')).toBeNull()

    const onUpdateField = render(script('eventhubs', { eventhubs: { consumer_mode: 'processor' } }), 'eventhubs')
    expect(field('config.eventhubs.tail_poll_interval_ms').min).toBe('10000')
    type(field('config.eventhubs.tail_poll_interval_ms'), '15000')
    expect(onUpdateField).toHaveBeenLastCalledWith('config.eventhubs', { consumer_mode: 'processor', tail_poll_interval_ms: 15000 })
    expect(container.querySelector('[data-catalog-path="config.eventhubs.observe_partition_tail"]')).not.toBeNull()

    // A stored value the validator rejects in observer mode stays visible and clearable.
    const stored = render(script('eventhubs', { eventhubs: { tail_poll_interval_ms: 20000, observe_partition_tail: true } }), 'eventhubs')
    type(field('config.eventhubs.tail_poll_interval_ms'), '')
    expect(stored).toHaveBeenLastCalledWith('config.eventhubs', { tail_poll_interval_ms: undefined, observe_partition_tail: true })
    expect(container.querySelector('[data-catalog-path="config.eventhubs.observe_partition_tail"]')).not.toBeNull()
  })

  it('reveals Event Hubs processor fields and edits them without dropping the block', () => {
    const onUpdateField = render(script('eventhubs', { eventhubs: { namespace: 'ns.servicebus.windows.net', consumer_mode: 'processor' } }), 'eventhubs')
    type(field('config.eventhubs.checkpoint_container_url'), 'https://acct.blob.core.windows.net/cp')
    expect(onUpdateField).toHaveBeenLastCalledWith('config.eventhubs', { namespace: 'ns.servicebus.windows.net', consumer_mode: 'processor', checkpoint_container_url: 'https://acct.blob.core.windows.net/cp' })
    expect(container.querySelector('[data-catalog-path="config.eventhubs.owner_level"]')).toBeNull()

    render(script('eventhubs', { eventhubs: { consumer_mode: 'exclusive' } }), 'eventhubs', 'timeouts', onUpdateField)
    type(field('config.eventhubs.owner_level'), '3')
    expect(onUpdateField).toHaveBeenLastCalledWith('config.eventhubs', { consumer_mode: 'exclusive', owner_level: 3 })
  })

  it('edits gRPC metadata and shows unset reflection as off', () => {
    const onUpdateField = render(script('grpc', { base_url: 'dns:///api:443', grpc: { authority: 'api.example.test' } }), 'grpc')
    const reflection = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')).find(input => input.closest('label')?.textContent?.includes('Use reflection'))!
    expect(reflection.checked).toBe(false)
    const group = container.querySelector('[data-catalog-path="config.grpc.metadata"]')!
    type(group.querySelector<HTMLInputElement>('[aria-label="New Default call metadata key"]')!, 'authorization')
    type(group.querySelector<HTMLInputElement>('[aria-label="New Default call metadata value"]')!, 'Bearer {{secret.API_TOKEN}}')
    click(button('Add metadata'))
    expect(onUpdateField).toHaveBeenLastCalledWith('config.grpc', { authority: 'api.example.test', metadata: { authorization: 'Bearer {{secret.API_TOKEN}}' } })
  })

  it('edits SMTP sender and recipients for network scripts and keeps a typed new line', () => {
    const onUpdateField = render(script('network', { network: { subprotocol: 'smtp', host: 'mail' } }), 'network')
    type(field('config.network.from'), 'load@example.test')
    expect(onUpdateField).toHaveBeenLastCalledWith('config.network', { subprotocol: 'smtp', host: 'mail', from: 'load@example.test' })
    const to = field<HTMLTextAreaElement>('config.network.to', 'textarea')
    type(to, 'ops@example.test\n')
    expect(onUpdateField).toHaveBeenLastCalledWith('config.network', { subprotocol: 'smtp', host: 'mail', to: ['ops@example.test'] })
    // The parent echoes the stored list back; the typed new line survives.
    render(script('network', { network: { subprotocol: 'smtp', host: 'mail', to: ['ops@example.test'] } }), 'network', 'timeouts', onUpdateField)
    expect(field<HTMLTextAreaElement>('config.network.to', 'textarea').value).toBe('ops@example.test\n')
    expect(field('config.network.password').type).toBe('text')

    render(script('network', { network: { subprotocol: 'tcp' } }), 'network')
    expect(container.querySelector('[data-testid="network-smtp-defaults"]')).toBeNull()
  })

  it('treats a stored upper-case SMTP subprotocol as SMTP, as validation does', () => {
    render(script('network', { network: { subprotocol: 'SMTP', host: 'mail' } }), 'network')
    expect(container.querySelector('[data-testid="network-smtp-defaults"]')).not.toBeNull()
    render(script('network', { network: { subprotocol: 'SMTP', host: 'mail' } }), 'script_config')
    const subprotocol = field<HTMLSelectElement>('config.network.subprotocol', 'select')
    expect(subprotocol.value).toBe('SMTP')
    expect(subprotocol.selectedOptions[0]?.textContent).toBe('SMTP')
    expect(Array.from(subprotocol.options).some(option => option.textContent?.includes('not supported'))).toBe(false)
  })
})

describe('Java Vuser parameter delimiters', () => {
  it('edits both braces and flags a one-sided delimiter', () => {
    const onUpdateField = render(script('java_vuser', { java_vuser: { param_left_brace: '<<' } }), 'java_vuser')
    expect(container.querySelector('[data-testid="java-vuser-parameter-braces"] [role="alert"]')?.textContent).toContain('both')
    type(field('config.java_vuser.param_right_brace'), '>>')
    expect(onUpdateField).toHaveBeenLastCalledWith('config.java_vuser.param_right_brace', '>>')
    render(script('java_vuser', { java_vuser: { param_left_brace: '<<', param_right_brace: '>>' } }), 'java_vuser', 'timeouts', onUpdateField)
    expect(container.querySelector('[data-testid="java-vuser-parameter-braces"] [role="alert"]')).toBeNull()
  })
})

describe('Browser access fields', () => {
  it('writes the browser proxy and credentials to config.browser only', () => {
    const onUpdateField = render(script('browser', { browser: { engine: 'chromium' }, http: { proxy_mode: 'custom', proxy: { url: 'http://http-proxy:8080' } } }), 'http', 'browser')
    type(field('config.browser.proxy.server'), 'http://browser-proxy:3128')
    expect(onUpdateField).toHaveBeenLastCalledWith('config.browser', { engine: 'chromium', proxy: { server: 'http://browser-proxy:3128' } })
    expect(onUpdateField.mock.calls.every(([path]) => path === 'config.browser')).toBe(true)
    type(field('config.browser.http_credentials.password'), '{{secret.BASIC_PW}}')
    expect(onUpdateField).toHaveBeenLastCalledWith('config.browser', { engine: 'chromium', http_credentials: { password: '{{secret.BASIC_PW}}' } })
  })

  it('removes the proxy block when its last field is cleared and edits client certificates', () => {
    const onUpdateField = render(script('browser', { browser: { proxy: { server: 'http://p:1' }, locale: 'en-US' } }), 'http', 'browser')
    type(field('config.browser.proxy.server'), '')
    expect(onUpdateField).toHaveBeenLastCalledWith('config.browser', { locale: 'en-US' })
    click(button('Add client certificate'))
    expect(onUpdateField).toHaveBeenLastCalledWith('config.browser', { proxy: { server: 'http://p:1' }, locale: 'en-US', client_certificates: [{}] })

    render(script('browser', { browser: { client_certificates: [{ origin: 'https://mtls', passphrase: 'imported' }] } }), 'http', 'browser', onUpdateField)
    const passphrase = container.querySelector<HTMLInputElement>('input[aria-label="Certificate 1 passphrase"]')!
    expect(passphrase.type).toBe('text')
    expect(passphrase.value).toBe('imported')
    click(button('Remove certificate 1'))
    expect(onUpdateField).toHaveBeenLastCalledWith('config.browser', {})
  })

  it('is not rendered for HTTP scripts, which have no Browser sub-tab', () => {
    render(script('http', { browser: { locale: 'en-US' } }), 'http', 'browser')
    expect(container.querySelector('[data-testid="browser-access-fields"]')).toBeNull()
  })
})

describe('Appium secret references', () => {
  it('names the project secrets the server URL and capabilities reference', () => {
    act(() => root.render(<AppiumRuntimeFields config={{ server_url: 'https://{{secret.CLOUD_USER}}:{{secret.CLOUD_KEY}}@hub/wd/hub', capabilities: { 'appium:app': '{{secret.APP_URL}}' } }} onChange={vi.fn()} />))
    const notes = Array.from(container.querySelectorAll('[data-secret-reference-note]')).map(note => note.textContent)
    expect(notes[0]).toContain('CLOUD_USER, CLOUD_KEY')
    expect(notes[1]).toContain('APP_URL')
    // The reference text itself stays as written.
    expect(container.querySelector<HTMLInputElement>('input')!.value).toBe('https://{{secret.CLOUD_USER}}:{{secret.CLOUD_KEY}}@hub/wd/hub')
  })
})
