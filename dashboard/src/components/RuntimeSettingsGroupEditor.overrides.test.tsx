// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import RuntimeSettingsGroupEditor, { type RuntimeEditorGroup } from './RuntimeSettingsGroupEditor'
import { type RuntimeSettingsOverride, type ScenarioRuntimeSettingsDraft } from '../types'
import { type ScriptConfig } from '../types/scripts'
import { runtimeDraftFromOverride } from './runtimeSettingsModel'
import { buildRuntimeSettingsPayload } from './scenarioPayloadUtils'

// The scenario/group override surface: canonical 7-group rail, the override
// fields Phase 3 adds (base URL, SOAP, TLS max version, auth domain/hosts,
// retries, pacing and run logic), three-state booleans, and per-field
// provenance with Reset to inherited.

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

function activeTabLabel(): string {
  return (container.querySelector('.runtime-override-tabs [role="tab"][aria-selected="true"]')?.textContent || '').trim()
}

function activeHttpSubTab(): string {
  return (container.querySelector('.http-transport-tabs [role="tab"][aria-selected="true"]')?.textContent || '').trim()
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

function click(element: Element) {
  act(() => element.dispatchEvent(new MouseEvent('click', { bubbles: true })))
}

function button(text: string): HTMLButtonElement {
  const found = Array.from(container.querySelectorAll('button')).find(b => (b.textContent || '').trim() === text)
  if (!found) throw new Error(`button not found: ${text}`)
  return found as HTMLButtonElement
}

const HTTP_GROUP: RuntimeEditorGroup = { name: 'Checkout', scriptId: 's1', protocol: 'http', scriptConfig: { base_url: 'https://script.example.com' } }
const SOAP_GROUP: RuntimeEditorGroup = {
  name: 'Orders',
  scriptId: 's2',
  protocol: 'soap',
  scriptConfig: {
    base_url: 'https://soap.example.com',
    soap: {
      version: '1.1',
      namespaces: { soapenv: 'http://schemas.xmlsoap.org/soap/envelope/', ord: 'urn:orders' },
      ws_security: { username_token: { username: 'svc', password_type: 'text' } },
    },
  },
}

describe('RuntimeSettingsGroupEditor canonical rail', () => {
  it('mounts the 7-group rail in scenario mode and opens the tab holding each group', () => {
    mount({ group: HTTP_GROUP })
    expect(container.querySelector('[data-testid="runtime-editor-mode-badge"]')?.textContent).toBe('Scenario VUsers runtime overrides')
    const pills = Array.from(container.querySelectorAll('[data-testid^="runtime-settings-canonical-group-"][data-canonical-group]')).map(pill => pill.getAttribute('data-canonical-group'))
    expect(pills).toEqual(['target_identity', 'run_logic_pacing', 'think_time', 'transport_connections', 'security_proxy_auth', 'protocol_behavior_session', 'errors_logging_diagnostics'])

    click(container.querySelector('[data-testid="runtime-settings-canonical-group-security_proxy_auth"]')!)
    expect(activeTabLabel()).toBe('HTTP Transport')
    expect(activeHttpSubTab()).toBe('TLS / SSL')
    expect(container.querySelector('[data-testid="runtime-settings-canonical-group-security_proxy_auth"]')?.getAttribute('aria-pressed')).toBe('true')

    click(container.querySelector('[data-testid="runtime-settings-canonical-group-errors_logging_diagnostics"]')!)
    expect(activeTabLabel()).toBe('Error Handling')
    click(container.querySelector('[data-testid="runtime-settings-canonical-group-target_identity"]')!)
    expect(activeTabLabel()).toBe('Target & identity')

    // The existing tabs still drive the editor, and the active pill follows them.
    clickTab('Think Time')
    expect(container.querySelector('[data-testid="runtime-settings-canonical-group-think_time"]')?.getAttribute('aria-pressed')).toBe('true')
  })

  it('counts this layer\'s overridden paths on the group pill', () => {
    mount({ group: HTTP_GROUP, initial: runtimeDraftFromOverride({ http: { request_timeout_ms: 5000, connect_timeout_ms: 1000 }, override_fields: ['http.request_timeout_ms', 'http.connect_timeout_ms'] }) })
    expect(container.querySelector('[data-testid="runtime-settings-canonical-group-overrides-transport_connections"]')?.textContent).toBe('2 overrides')
    click(container.querySelector('[data-testid="runtime-settings-canonical-group-transport_connections"]')!)
    expect(activeHttpSubTab()).toBe('Timeouts')
  })
})

describe('RuntimeSettingsGroupEditor override fields', () => {
  it('overrides and resets the base URL under Target & identity', () => {
    mount({ group: HTTP_GROUP })
    clickTab('Target & identity')
    const input = control<HTMLInputElement>('Base URL', 'input')
    expect(input.placeholder).toBe('https://script.example.com')
    expect(badgeText('base_url')).toBe('Script value')
    setInput(input, 'https://staging.example.com')
    expect(payload()).toEqual({ base_url: 'https://staging.example.com', override_fields: ['base_url'] })
    expect(badgeText('base_url')).toBe('Group override')
    click(resetButton('base_url')!)
    expect(latest).toBeUndefined()
  })

  it('shows the SOAP tab only where a member script uses SOAP', () => {
    mount({ group: HTTP_GROUP })
    expect(Array.from(container.querySelectorAll('[role="tab"]')).map(tab => tab.textContent)).not.toContain('SOAP')
    act(() => root.unmount())
    root = createRoot(container)
    mount({ group: { name: 'Scenario defaults', scriptId: 'defaults' }, applicableProtocols: ['http', 'soap'] })
    expect(Array.from(container.querySelectorAll('[role="tab"]')).map(tab => tab.textContent)).toContain('SOAP')
  })

  it('edits SOAP version, fault handling, namespaces and WS-Security as listed paths', () => {
    mount({ group: SOAP_GROUP })
    clickTab('SOAP')
    const version = control<HTMLSelectElement>('SOAP version', 'select')
    expect(version.options[0].textContent).toBe('Inherit (SOAP 1.1)')
    setSelect(version, '1.2')
    // The envelope namespace follows the version the soapenv prefix still had the default for.
    expect(payload()).toEqual({
      soap: { version: '1.2', namespaces: { soapenv: 'http://www.w3.org/2003/05/soap-envelope', ord: 'urn:orders' } },
      override_fields: ['soap.version', 'soap.namespaces'],
    })

    const fault = control<HTMLSelectElement>('Fail on SOAP fault', 'select')
    // An unset fail_on_fault is false.
    expect(fault.options[0].textContent).toBe('Inherit (Disabled)')
    setSelect(fault, 'true')
    expect(payload()?.soap?.fault_handling).toEqual({ fail_on_fault: true })

    setInput(container.querySelector('input[aria-label="New namespace prefix"]') as HTMLInputElement, 'xsi')
    setInput(container.querySelector('input[aria-label="New namespace URI"]') as HTMLInputElement, 'http://www.w3.org/2001/XMLSchema-instance')
    click(button('Add namespace'))
    expect(payload()?.soap?.namespaces).toEqual({
      soapenv: 'http://www.w3.org/2003/05/soap-envelope',
      ord: 'urn:orders',
      xsi: 'http://www.w3.org/2001/XMLSchema-instance',
    })

    const passwordType = control<HTMLSelectElement>('Password type', 'select')
    setSelect(passwordType, 'digest')
    setSelect(control<HTMLSelectElement>('Include nonce', 'select'), 'true')
    setInput(control<HTMLInputElement>('Timestamp TTL (seconds)', 'input'), '300')
    expect(payload()?.soap?.ws_security).toEqual({
      username_token: { password_type: 'digest', nonce: true },
      timestamp: { ttl_seconds: 300 },
    })
    expect(payload()?.override_fields).toEqual([
      'soap.version',
      'soap.namespaces',
      'soap.fault_handling.fail_on_fault',
      'soap.ws_security.username_token.password_type',
      'soap.ws_security.username_token.nonce',
      'soap.ws_security.timestamp.ttl_seconds',
    ])
    // The script's username keeps applying.
    expect(control<HTMLInputElement>('WS-Security username', 'input').placeholder).toBe('svc')
  })

  it('round-trips a stored WS-Security password as [REDACTED], clears it with "", and replaces it when typed', () => {
    const stored = runtimeDraftFromOverride({
      soap: { ws_security: { username_token: { username: '[REDACTED]', password: '[REDACTED]' } } },
      override_fields: ['soap.ws_security.username_token.username', 'soap.ws_security.username_token.password'],
    })
    mount({ group: SOAP_GROUP, initial: stored })
    clickTab('SOAP')
    const password = control<HTMLInputElement>('WS-Security password', 'input')
    expect(password.type).toBe('password')
    expect(password.value).toBe('')
    expect(password.placeholder).toBe('Stored secret (unchanged)')
    const username = control<HTMLInputElement>('WS-Security username', 'input')
    expect(username.value).toBe('')
    expect(username.placeholder).toBe('Stored username (unchanged)')

    // Editing a sibling sends the mask back at its listed path, so the server keeps the secret.
    setInput(control<HTMLInputElement>('Timestamp TTL (seconds)', 'input'), '60')
    expect(payload()).toEqual({
      soap: { ws_security: { username_token: { username: '[REDACTED]', password: '[REDACTED]' }, timestamp: { ttl_seconds: 60 } } },
      override_fields: [
        'soap.ws_security.username_token.username',
        'soap.ws_security.username_token.password',
        'soap.ws_security.timestamp.ttl_seconds',
      ],
    })

    click(button('Clear stored secret'))
    expect(payload()?.soap?.ws_security?.username_token?.password).toBe('')
    expect(payload()?.override_fields).toContain('soap.ws_security.username_token.password')

    setInput(control<HTMLInputElement>('WS-Security password', 'input'), 'n3w-pass')
    expect(payload()?.soap?.ws_security?.username_token?.password).toBe('n3w-pass')
  })

  it('keeps a stored WS-Security username and password when typed text over them is erased', () => {
    const stored = runtimeDraftFromOverride({
      soap: { ws_security: { username_token: { username: '[REDACTED]', password: '[REDACTED]' } } },
      override_fields: ['soap.ws_security.username_token.username', 'soap.ws_security.username_token.password'],
    })
    mount({ group: SOAP_GROUP, initial: stored })
    clickTab('SOAP')
    setInput(control<HTMLInputElement>('WS-Security password', 'input'), 'x')
    setInput(control<HTMLInputElement>('WS-Security password', 'input'), '')
    setInput(control<HTMLInputElement>('WS-Security username', 'input'), 'y')
    setInput(control<HTMLInputElement>('WS-Security username', 'input'), '')
    expect(payload()).toEqual({
      soap: { ws_security: { username_token: { username: '[REDACTED]', password: '[REDACTED]' } } },
      override_fields: ['soap.ws_security.username_token.username', 'soap.ws_security.username_token.password'],
    })
    expect(control<HTMLInputElement>('WS-Security password', 'input').placeholder).toBe('Stored secret (unchanged)')
    expect(control<HTMLInputElement>('WS-Security username', 'input').placeholder).toBe('Stored username (unchanged)')
  })

  it('undoes the soapenv namespace sync when the SOAP version is inherited again', () => {
    mount({ group: SOAP_GROUP })
    clickTab('SOAP')
    setSelect(control<HTMLSelectElement>('SOAP version', 'select'), '1.2')
    expect(payload()?.override_fields).toEqual(['soap.version', 'soap.namespaces'])
    setSelect(control<HTMLSelectElement>('SOAP version', 'select'), '')
    // Only the synced soapenv entry differed, so both overrides go.
    expect(latest).toBeUndefined()

    // The field's Reset to inherited does the same, and keeps a namespace the user added.
    setSelect(control<HTMLSelectElement>('SOAP version', 'select'), '1.2')
    setInput(container.querySelector('input[aria-label="New namespace prefix"]') as HTMLInputElement, 'xsi')
    setInput(container.querySelector('input[aria-label="New namespace URI"]') as HTMLInputElement, 'urn:xsi')
    click(button('Add namespace'))
    click(resetButton('soap.version')!)
    expect(payload()).toEqual({
      soap: { namespaces: { soapenv: 'http://schemas.xmlsoap.org/soap/envelope/', ord: 'urn:orders', xsi: 'urn:xsi' } },
      override_fields: ['soap.namespaces'],
    })
  })

  it('adds TLS max version with the HTTP/3 incompatibility warning, auth domain and hosts', () => {
    mount({ group: { ...HTTP_GROUP, scriptConfig: { base_url: '', http: { version: '3' } } } })
    clickTab('HTTP Transport')
    clickTab('TLS / SSL')
    expect(container.querySelector('[data-testid="tls-max-version-http3-warning"]')).toBeNull()
    setSelect(control<HTMLSelectElement>('TLS max version', 'select'), '1.2')
    expect(payload()).toEqual({ http: { tls: { max_version: '1.2' } }, override_fields: ['http.tls.max_version'] })
    expect(container.querySelector('[data-testid="tls-max-version-http3-warning"]')?.textContent).toContain('HTTP/3 (QUIC) requires TLS 1.3')

    clickTab('Authentication')
    setInput(control<HTMLInputElement>('Auth domain', 'input'), 'CORP')
    setInput(container.querySelector('input[aria-label="Auth hosts"]') as HTMLInputElement, 'shop.example.com, api.example.com:8443')
    expect(payload()?.http?.auth).toEqual({ domain: 'CORP', hosts: ['shop.example.com', 'api.example.com:8443'] })
    expect(payload()?.override_fields).toEqual(['http.tls.max_version', 'http.auth.domain', 'http.auth.hosts'])
  })

  it('adds retry, logging and cache-for-new-user overrides with catalog option values', () => {
    mount({ group: { ...HTTP_GROUP, scriptConfig: { base_url: '', error_handling: { retry_on: ['5xx'] } } } })
    clickTab('Error Handling')
    setSelect(control<HTMLSelectElement>('Retry backoff', 'select'), 'retry_after')
    setSelect(control<HTMLSelectElement>('Honor Retry-After', 'select'), 'true')
    setInput(control<HTMLInputElement>('Retry-After cap (ms)', 'input'), '8000')
    setSelect(control<HTMLSelectElement>('Fail transaction on error message', 'select'), 'false')
    const retryOn429 = Array.from(container.querySelectorAll('fieldset[aria-label="Retry on"] input')).find(input => input.parentElement?.textContent?.includes('429')) as HTMLInputElement
    expect(retryOn429).toBeTruthy()
    click(retryOn429)
    setInput(control<HTMLInputElement>('Max logged response (bytes)', 'input'), '99999999')
    expect(payload()).toEqual({
      error_handling: { retry_backoff: 'retry_after', honor_retry_after: true, retry_after_max_ms: 8000, fail_transaction_on_error_message: false, retry_on: ['5xx', '429'] },
      log: { max_response_bytes: 1048576 },
      override_fields: [
        'error_handling.retry_backoff',
        'error_handling.honor_retry_after',
        'error_handling.retry_after_max_ms',
        'error_handling.fail_transaction_on_error_message',
        'error_handling.retry_on',
        'log.max_response_bytes',
      ],
    })

    clickTab('Browser Emulation')
    setSelect(control<HTMLSelectElement>('Clear cache for a new user', 'select'), 'true')
    expect(payload()?.browser_emulation).toEqual({ cache: { clear_cache_for_new_user: true } })
  })

  const retryOnBox = (text: string) =>
    Array.from(container.querySelectorAll('fieldset[aria-label="Retry on"] input')).find(input => input.parentElement?.textContent?.includes(text)) as HTMLInputElement

  it('does not let the last inherited retry condition be unticked into a silent retry-on-everything', () => {
    mount({ group: { ...HTTP_GROUP, scriptConfig: { base_url: '', error_handling: { max_retries: 3, retry_on: ['5xx'] } } } })
    clickTab('Error Handling')
    const retryOn5xx = retryOnBox('5xx')
    const anyError = retryOnBox('Any error (no filter)')
    expect(retryOn5xx.checked).toBe(true)
    expect(anyError.checked).toBe(false)
    // An empty retry_on is no filter in the engine, so the only ticked condition cannot be unticked.
    expect(retryOn5xx.disabled).toBe(true)
    click(retryOn5xx)
    expect(latest).toBeUndefined()
    expect(container.querySelector('[data-testid="retry-on-hint"]')?.textContent).toContain('set Max retries to 0')

    // Retrying on every error is an explicit choice: an explicit empty list over the script's 5xx.
    click(anyError)
    expect(payload()).toEqual({ override_fields: ['error_handling.retry_on'] })
    expect(retryOnBox('Any error (no filter)').checked).toBe(true)
    expect(retryOnBox('5xx').checked).toBe(false)
    expect(retryOnBox('5xx').disabled).toBe(false)
  })

  it('shows no inherited filter as Any error and returns to it through Any error', () => {
    mount({ group: HTTP_GROUP })
    clickTab('Error Handling')
    expect(retryOnBox('Any error (no filter)').checked).toBe(true)
    click(retryOnBox('429'))
    expect(payload()).toEqual({ error_handling: { retry_on: ['429'] }, override_fields: ['error_handling.retry_on'] })
    expect(retryOnBox('Any error (no filter)').checked).toBe(false)
    expect(retryOnBox('429').disabled).toBe(true)
    // Nothing below filters retries, so Any error inherits again.
    click(retryOnBox('Any error (no filter)'))
    expect(latest).toBeUndefined()
  })
})

describe('RuntimeSettingsGroupEditor three-state booleans', () => {
  it('overrides a script true to false and Inherit returns it to the script', () => {
    mount({ group: { ...HTTP_GROUP, scriptConfig: { base_url: '', http: { keep_alive: true, cookies_enabled: false, tls: { insecure_skip_verify: true } } } } })
    clickTab('HTTP Transport')
    clickTab('Connection')
    const keepAlive = control<HTMLSelectElement>('Keep-alive', 'select')
    expect(keepAlive.options[0].textContent).toBe('Inherit (Enabled)')
    setSelect(keepAlive, 'false')
    expect(payload()).toEqual({ http: { keep_alive: false }, override_fields: ['http.keep_alive'] })
    // The explicit false shows as Disabled, not as Inherit.
    expect(control<HTMLSelectElement>('Keep-alive', 'select').value).toBe('false')
    setSelect(control<HTMLSelectElement>('Keep-alive', 'select'), '')
    expect(latest).toBeUndefined()

    clickTab('Session & Cache')
    const cookies = control<HTMLSelectElement>('Cookie jar override', 'select')
    expect(cookies.options[0].textContent).toBe('Inherit (Disabled)')
    setSelect(cookies, 'true')
    expect(payload()).toEqual({ http: { cookies_enabled: true }, override_fields: ['http.cookies_enabled'] })

    clickTab('TLS / SSL')
    const skip = control<HTMLSelectElement>('Skip TLS verification', 'select')
    expect(skip.options[0].textContent).toBe('Inherit (Enabled)')
    setSelect(skip, 'false')
    expect(payload()?.http).toEqual({ cookies_enabled: true, tls: { insecure_skip_verify: false } })

    clickTab('Request Defaults')
    setSelect(control<HTMLSelectElement>('Trace context override', 'select'), 'false')
    expect(payload()?.override_fields).toEqual(['http.cookies_enabled', 'http.tls.insecure_skip_verify', 'http.traceparent'])
  })

  it('keeps continue-on-error on by default and lets the group disable it or inherit again', () => {
    mount({ group: HTTP_GROUP, engineDefaults: { base_url: '', error_handling: { continue_on_error: false } } })
    clickTab('Error Handling')
    const select = control<HTMLSelectElement>('Continue on error', 'select')
    expect(select.options[0].textContent).toBe('Inherit (Enabled)')
    setSelect(select, 'false')
    expect(payload()).toEqual({ error_handling: { continue_on_error: false }, override_fields: ['error_handling.continue_on_error'] })
    setSelect(control<HTMLSelectElement>('Continue on error', 'select'), '')
    expect(latest).toBeUndefined()
  })
})

describe('RuntimeSettingsGroupEditor two-state HTTP controls', () => {
  function labelTexts(): string[] {
    return Array.from(container.querySelectorAll('label')).map(label => (label.querySelector('strong, :scope > span')?.textContent || '').trim())
  }

  it('shows only the three-state cookie, cache and trace context controls in override mode', () => {
    mount({ group: HTTP_GROUP })
    clickTab('HTTP Transport')
    clickTab('Session & Cache')
    const session = labelTexts()
    expect(session).not.toContain('Enable cookie jar')
    expect(session).not.toContain('Clear learned cookies each iteration')
    expect(session).not.toContain('Simulate HTTP cache')
    expect(session).toEqual(expect.arrayContaining(['Cookie jar override', 'Clear learned cookies override', 'HTTP cache override']))

    clickTab('Request Defaults')
    const request = labelTexts()
    expect(request).not.toContain('Trace context')
    expect(request).not.toContain('Generated trace sampling')
    expect(request).toEqual(expect.arrayContaining(['Trace context override', 'Trace sampling override']))
  })
})

describe('RuntimeSettingsGroupEditor provenance and reset', () => {
  it('labels the test-level mount as Test override over script or engine defaults', () => {
    // CreateTestRuntimeOverrides: one row for every group, no script of its own.
    mount({
      group: { name: 'Test overrides', scriptId: 'test-overrides' },
      engineDefaults: { base_url: '' },
      overrideLabel: 'Test override',
      applicableProtocols: ['http'],
    })
    clickTab('Target & identity')
    expect(badgeText('base_url')).toBe('Script or engine default')
    setInput(control<HTMLInputElement>('Base URL', 'input'), 'https://staging.example.com')
    expect(badgeText('base_url')).toBe('Test override')
  })

  it('labels the run mount\'s lower row with the layer the caller names', () => {
    // GroupRuntimeSettingsDialog: the test's runtime settings sit in the scenarioDefault row.
    mount({
      group: HTTP_GROUP,
      scenarioDefault: { base_url: 'https://test.example.com' },
      overrideLabel: 'Run override',
      scenarioDefaultLabel: 'Test override',
    })
    clickTab('Target & identity')
    expect(badgeText('base_url')).toBe('Test override')
    setInput(control<HTMLInputElement>('Base URL', 'input'), 'https://run.example.com')
    expect(badgeText('base_url')).toBe('Run override')
  })

  it('badges each field by the layer that supplies it and resets one field without touching siblings', () => {
    mount({
      group: { ...HTTP_GROUP, scriptConfig: { base_url: '', http: { connect_timeout_ms: 2000 } } },
      scenarioDefault: { http: { idle_timeout_ms: 9000 }, override_fields: ['http.idle_timeout_ms'] } as RuntimeSettingsOverride,
      engineDefaults: { base_url: '', http: { tls_timeout_ms: 10000 } },
      initial: runtimeDraftFromOverride({ http: { request_timeout_ms: 5000, max_redirects: 3 }, override_fields: ['http.request_timeout_ms', 'http.max_redirects'] }),
    })
    clickTab('HTTP Transport')
    clickTab('Timeouts')
    expect(badgeText('http.request_timeout_ms')).toBe('Group override')
    expect(badgeText('http.idle_timeout_ms')).toBe('Scenario default')
    expect(badgeText('http.connect_timeout_ms')).toBe('Script value')
    expect(badgeText('http.tls_timeout_ms')).toBe('Engine default')
    expect(resetButton('http.idle_timeout_ms')).toBeNull()
    const reset = resetButton('http.request_timeout_ms')
    expect(reset).toBeTruthy()
    expect(reset?.getAttribute('aria-label')).toBe('Reset Request timeout (ms) to inherited')
    click(reset!)
    expect(payload()).toEqual({ http: { max_redirects: 3 }, override_fields: ['http.max_redirects'] })
  })

  it('labels this layer Scenario default on the scenario-defaults mount', () => {
    mount({
      group: { name: 'Scenario defaults', scriptId: 'defaults' },
      overrideLabel: 'Scenario default',
      applicableProtocols: ['http'],
      initial: runtimeDraftFromOverride({ base_url: 'https://scenario.example.com', override_fields: ['base_url'] }),
    })
    clickTab('Target & identity')
    expect(badgeText('base_url')).toBe('Scenario default')
    expect(fieldFor('base_url').getAttribute('data-provenance')).toBe('scenario_default')
  })

  it('lists overridden fields a panel has no own control for, each with Reset to inherited', () => {
    mount({ group: HTTP_GROUP, initial: runtimeDraftFromOverride({ http: { page_timeout_ms: 30000, request_timeout_ms: 5000 }, override_fields: ['http.page_timeout_ms', 'http.request_timeout_ms'] }) })
    clickTab('HTTP Transport')
    clickTab('Timeouts')
    const list = container.querySelector('[data-testid="runtime-override-panel-list"]')
    expect(list).toBeTruthy()
    // The page timeout lives in a sub-editor; the request timeout has its own field.
    expect(Array.from(list!.querySelectorAll('li')).map(item => item.getAttribute('data-override-path'))).toEqual(['http.page_timeout_ms'])
    click(list!.querySelector('button[aria-label="Reset http.page_timeout_ms to inherited"]')!)
    expect(payload()).toEqual({ http: { request_timeout_ms: 5000 }, override_fields: ['http.request_timeout_ms'] })
  })
})

describe('RuntimeSettingsGroupEditor pacing and run logic', () => {
  it('offers open-workload arrival settings only on an open TPS workload', () => {
    mount({ group: { ...HTTP_GROUP, scriptConfig: { base_url: '', run_logic: { workload_model: 'open' }, pacing: { type: 'tps', tps: 5 } } } })
    clickTab('Iteration Pacing')
    expect(container.querySelector('[data-testid="pacing-open-workload-notice"]')).toBeTruthy()
    setSelect(control<HTMLSelectElement>('Arrival process', 'select'), 'gamma')
    // Gamma arrivals fail validation without a burstiness above 0.
    expect(payload()).toEqual({ pacing: { arrival_process: 'gamma', burstiness: 1 }, override_fields: ['pacing.arrival_process', 'pacing.burstiness'] })
    setInput(control<HTMLInputElement>('Arrival seed', 'input'), '0')
    click(button('Add rate stage'))
    expect(payload()?.pacing).toEqual({ arrival_process: 'gamma', burstiness: 1, seed: 0, rate_stages: [{ duration_seconds: 30, target_tps: 5 }] })

    clickTab('Run Logic')
    setInput(control<HTMLInputElement>('Maximum in-flight iterations', 'input'), '40')
    expect(payload()?.run_logic).toEqual({ max_in_flight: 40 })
  })

  it('offers to clear leftover arrival settings on a closed workload with explicit empties', () => {
    mount({ group: { ...HTTP_GROUP, scriptConfig: { base_url: '', pacing: { type: 'tps', tps: 2, arrival_process: 'poisson', seed: 7 } as ScriptConfig['pacing'] } } })
    clickTab('Iteration Pacing')
    expect(container.querySelector('select[aria-label="Arrival process"]')).toBeNull()
    expect(container.querySelector('[data-testid="pacing-open-only-settings-notice"]')).toBeTruthy()
    click(button('Clear open-workload settings'))
    expect(payload()?.override_fields).toEqual(['pacing.arrival_process', 'pacing.seed'])
  })

  it('choosing the open workload model also overrides pacing to TPS at a positive rate', () => {
    mount({ group: { ...HTTP_GROUP, scriptConfig: { base_url: '', pacing: { type: 'fixed_delay', delay_ms: 100 } } } })
    clickTab('Run Logic')
    expect(container.querySelector('input[aria-label="Maximum in-flight iterations"]')).toBeNull()
    setSelect(control<HTMLSelectElement>('Workload model', 'select'), 'open')
    expect(payload()).toEqual({
      run_logic: { workload_model: 'open' },
      pacing: { type: 'tps', tps: 1 },
      override_fields: ['run_logic.workload_model', 'pacing.type', 'pacing.tps'],
    })
  })

  it('edits termination mode and shows run logic blocks read-only', () => {
    mount({ group: { ...HTTP_GROUP, scriptConfig: { base_url: '', run_logic: { blocks: [{ name: 'Browse', mode: 'random', children: [{ action: 'a' }, { action: 'b' }] }] } } } })
    clickTab('Run Logic')
    const termination = control<HTMLSelectElement>('Termination mode', 'select')
    expect(Array.from(termination.options).map(option => option.value)).toEqual(['', 'duration', 'iterations'])
    setSelect(termination, 'iterations')
    expect(payload()).toEqual({ run_logic: { termination_mode: 'iterations' }, override_fields: ['run_logic.termination_mode'] })
    expect(container.querySelector('[data-testid="run-logic-termination-iterations-hint"]')).toBeTruthy()
    expect(container.querySelector('[data-testid="run-logic-blocks-summary"]')?.textContent).toContain('Browse · random · 2 steps')
    expect(badgeText('run_logic.blocks')).toBe('Script value')
  })

  it('shows the LLM quota editor for a group with LLM actions', () => {
    mount({ group: { ...HTTP_GROUP, scriptActions: [{ name: 'chat', type: 'llm_request' } as NonNullable<RuntimeEditorGroup['scriptActions']>[number]] } })
    clickTab('Iteration Pacing')
    expect(container.querySelector('[data-testid="pacing-quota-editor"]')).toBeTruthy()
    setInput(control<HTMLInputElement>('Tokens per minute (TPM)', 'input'), '60000')
    expect(payload()).toEqual({ pacing: { quota: { tpm: 60000 } }, override_fields: ['pacing.quota.tpm'] })
    act(() => root.unmount())
    root = createRoot(container)
    mount({ group: HTTP_GROUP })
    clickTab('Iteration Pacing')
    expect(container.querySelector('[data-testid="pacing-quota-editor"]')).toBeNull()
  })

  it('Pacing Inherit keeps a quota TPM override, which applies in every pacing mode', () => {
    mount({ group: { ...HTTP_GROUP, scriptActions: [{ name: 'chat', type: 'llm_request' } as NonNullable<RuntimeEditorGroup['scriptActions']>[number]] } })
    clickTab('Iteration Pacing')
    setSelect(control<HTMLSelectElement>('Pacing', 'select'), 'fixed_delay')
    setInput(control<HTMLInputElement>('Tokens per minute (TPM)', 'input'), '60000')
    expect(payload()?.override_fields).toEqual(['pacing.type', 'pacing.quota.tpm'])
    setSelect(control<HTMLSelectElement>('Pacing', 'select'), '')
    expect(payload()).toEqual({ pacing: { quota: { tpm: 60000 } }, override_fields: ['pacing.quota.tpm'] })
  })

  it('Pacing Inherit resets only the pacing-type leaves and keeps arrival overrides', () => {
    mount({
      group: HTTP_GROUP,
      initial: runtimeDraftFromOverride({
        pacing: { type: 'fixed_delay', delay_ms: 2000, tps: 4, arrival_process: 'poisson', burstiness: 2, seed: 7, rate_stages: [{ duration_seconds: 30, target_tps: 5 }] } as RuntimeSettingsOverride['pacing'],
        override_fields: ['pacing.type', 'pacing.delay_ms', 'pacing.tps', 'pacing.arrival_process', 'pacing.burstiness', 'pacing.seed', 'pacing.rate_stages'],
      }),
    })
    clickTab('Iteration Pacing')
    setSelect(control<HTMLSelectElement>('Pacing', 'select'), '')
    expect(payload()).toEqual({
      pacing: { arrival_process: 'poisson', burstiness: 2, seed: 7, rate_stages: [{ duration_seconds: 30, target_tps: 5 }] },
      override_fields: ['pacing.arrival_process', 'pacing.burstiness', 'pacing.seed', 'pacing.rate_stages'],
    })
  })

  it('offers exponential think time and the random distribution', () => {
    mount({ group: HTTP_GROUP })
    clickTab('Think Time')
    setSelect(control<HTMLSelectElement>('Distribution', 'select'), 'exponential')
    expect(payload()?.think_time).toMatchObject({ type: 'exponential', distribution: 'exponential' })
    setSelect(control<HTMLSelectElement>('Random distribution', 'select'), 'uniform')
    expect(payload()?.think_time).toMatchObject({ type: 'exponential', distribution: 'uniform' })
  })
})

describe('RuntimeSettingsGroupEditor review fixes', () => {
  it('drops the exponential distribution when think time moves to another type', () => {
    mount({ group: HTTP_GROUP })
    clickTab('Think Time')
    setSelect(control<HTMLSelectElement>('Distribution', 'select'), 'exponential')
    expect(payload()?.think_time).toMatchObject({ type: 'exponential', distribution: 'exponential' })
    setSelect(control<HTMLSelectElement>('Distribution', 'select'), 'fixed')
    // This layer set the distribution, so it returns to inherited (none).
    expect(payload()?.think_time?.type).toBe('fixed')
    expect(payload()?.think_time).not.toHaveProperty('distribution')
    expect(payload()?.override_fields).not.toContain('think_time.distribution')
  })

  it('writes an explicit empty distribution over an inherited exponential one', () => {
    mount({ group: { ...HTTP_GROUP, scriptConfig: { base_url: '', think_time: { type: 'exponential', distribution: 'exponential', mean_ms: 1000 } as NonNullable<ScriptConfig['think_time']> } } })
    clickTab('Think Time')
    setSelect(control<HTMLSelectElement>('Distribution', 'select'), 'gaussian')
    expect(payload()?.think_time?.type).toBe('gaussian')
    expect(payload()?.override_fields).toContain('think_time.distribution')
    expect((payload()?.think_time as { distribution?: string } | undefined)?.distribution ?? '').toBe('')
  })

  it('keeps a uniform distribution only for the uniform type', () => {
    mount({ group: { ...HTTP_GROUP, scriptConfig: { base_url: '', think_time: { type: 'uniform', distribution: 'uniform', min_ms: 10, max_ms: 20 } as NonNullable<ScriptConfig['think_time']> } } })
    clickTab('Think Time')
    setSelect(control<HTMLSelectElement>('Distribution', 'select'), 'fixed')
    expect(payload()?.override_fields).toContain('think_time.distribution')
    expect((payload()?.think_time as { distribution?: string } | undefined)?.distribution ?? '').toBe('')
  })

  it('shows clear cache for a new user as enabled when no layer sets it', () => {
    mount({ group: HTTP_GROUP })
    clickTab('Browser Emulation')
    expect(control<HTMLSelectElement>('Clear cache for a new user', 'select').options[0].textContent).toBe('Inherit (Enabled)')
  })

  it('resets both cache paths from the HTTP cache field reset', () => {
    mount({ group: { ...HTTP_GROUP, scriptConfig: { base_url: '', browser_emulation: { cache: { simulate_cache: true } } } as ScriptConfig } })
    clickTab('HTTP Transport')
    clickTab('Session & Cache')
    setSelect(control<HTMLSelectElement>('HTTP cache override', 'select'), 'false')
    expect(payload()?.override_fields).toEqual(expect.arrayContaining(['http.cache_enabled', 'browser_emulation.cache.simulate_cache']))
    click(resetButton('http.cache_enabled')!)
    expect(latest).toBeUndefined()
    expect(control<HTMLSelectElement>('HTTP cache override', 'select').value).toBe('')
    expect(resetButton('http.cache_enabled')).toBeNull()
  })

  it('marks the HTTP cache field overridden when only simulate_cache is overridden', () => {
    mount({
      group: { ...HTTP_GROUP, scriptConfig: { base_url: '', browser_emulation: { cache: { simulate_cache: true } } } as ScriptConfig },
      initial: runtimeDraftFromOverride({ browser_emulation: { cache: { simulate_cache: false } }, override_fields: ['browser_emulation.cache.simulate_cache'] } as RuntimeSettingsOverride),
    })
    clickTab('HTTP Transport')
    clickTab('Session & Cache')
    expect(badgeText('http.cache_enabled')).toBe('Group override')
    click(resetButton('http.cache_enabled')!)
    expect(latest).toBeUndefined()
  })

  it('does not drop a namespace when a prefix is edited into a duplicate or empty one', () => {
    mount({ group: SOAP_GROUP })
    clickTab('SOAP')
    const prefix2 = () => container.querySelector('input[aria-label="Namespace 2 prefix"]') as HTMLInputElement
    setInput(prefix2(), 'soapenv')
    expect(latest).toBeUndefined()
    expect(prefix2().value).toBe('soapenv')
    expect(container.querySelector('[data-testid="soap-namespace-prefix-error-1"]')?.textContent).toContain('already used')
    setInput(prefix2(), '')
    expect(latest).toBeUndefined()
    expect(container.querySelector('[data-testid="soap-namespace-prefix-error-1"]')?.textContent).toContain('required')
    setInput(prefix2(), 'ord2')
    expect(payload()?.soap?.namespaces).toEqual({ soapenv: 'http://schemas.xmlsoap.org/soap/envelope/', ord2: 'urn:orders' })
    expect(container.querySelector('[data-testid="soap-namespace-prefix-error-1"]')).toBeNull()
  })

  it('offers the LLM quota editor on scenario- and test-wide mounts', () => {
    mount({ group: { name: 'Scenario defaults', scriptId: 'defaults' }, applicableProtocols: ['http'] })
    clickTab('Iteration Pacing')
    expect(container.querySelector('[data-testid="pacing-quota-editor"]')).toBeTruthy()
    setInput(control<HTMLInputElement>('Tokens per minute (TPM)', 'input'), '60000')
    expect(payload()).toEqual({ pacing: { quota: { tpm: 60000 } }, override_fields: ['pacing.quota.tpm'] })
    act(() => root.unmount())
    root = createRoot(container)
    latest = undefined
    mount({ group: { name: 'Scenario defaults', scriptId: 'defaults' }, applicableProtocols: ['http'], scopeUsesLLMActions: false })
    clickTab('Iteration Pacing')
    expect(container.querySelector('[data-testid="pacing-quota-editor"]')).toBeNull()
  })
})
