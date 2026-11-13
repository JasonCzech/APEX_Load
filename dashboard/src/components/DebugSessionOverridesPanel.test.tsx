// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import DebugSessionOverridesPanel from './DebugSessionOverridesPanel'
import { sessionOverrideValue, type DebugSessionOverrideDraft } from './debugSessionOverrides'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const SCRIPT: ScriptDefinition = {
  name: 'Checkout',
  version: '1.0',
  protocol: 'http',
  config: { base_url: 'https://shop.example.test', http: { proxy: { url: 'http://proxy.test', password: 'script-proxy-secret' } } },
  actions: [{ name: 'Home', type: 'request', method: 'GET', target: '/' }],
}

let container: HTMLDivElement
let root: Root
let latest: DebugSessionOverrideDraft | undefined
const onPromote = vi.fn(() => 2)

function Harness({ script = SCRIPT, storedUnmodified = true, ignoreThinkTime = false, disabled = false }: { script?: ScriptDefinition; storedUnmodified?: boolean; ignoreThinkTime?: boolean; disabled?: boolean }) {
  const [overrides, setOverrides] = useState<DebugSessionOverrideDraft | undefined>()
  latest = overrides
  return (
    <DebugSessionOverridesPanel
      script={script}
      overrides={overrides}
      onChange={next => setOverrides(next)}
      disabled={disabled}
      storedUnmodified={storedUnmodified}
      ignoreThinkTime={ignoreThinkTime}
      engineDefaults={{ base_url: '', http: { request_timeout_ms: 60000, connect_timeout_ms: 30000, keep_alive: true } }}
      onPromote={onPromote}
    />
  )
}

async function render(element: React.ReactElement) {
  await act(async () => { root.render(element) })
}

async function selectGroup(id: string) {
  const pill = container.querySelector<HTMLButtonElement>(`[data-testid="runtime-settings-canonical-group-${id}"]`)
  expect(pill).toBeTruthy()
  await act(async () => { pill!.click() })
}

async function typeInto(element: HTMLInputElement | HTMLSelectElement, value: string) {
  const prototype = element instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype
  await act(async () => {
    Object.getOwnPropertyDescriptor(prototype, 'value')!.set!.call(element, value)
    element.dispatchEvent(new Event(element instanceof HTMLSelectElement ? 'change' : 'input', { bubbles: true }))
  })
}

function field(path: string): HTMLElement | null {
  return container.querySelector<HTMLElement>(`[data-override-path="${path}"]`)
}

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  latest = undefined
  onPromote.mockClear()
})

afterEach(async () => {
  await act(async () => { root.unmount() })
  container.remove()
})

describe('DebugSessionOverridesPanel', () => {
  it('mounts the shared runtime settings editor in debug mode', async () => {
    await render(<Harness />)
    const editor = container.querySelector('[data-testid="runtime-settings-editor"]')
    expect(editor?.getAttribute('data-runtime-editor-mode')).toBe('debug')
    expect(container.querySelector('[data-testid="runtime-editor-mode-badge"]')?.textContent).toBe('Debug VUsers session overrides')
  })

  it('says how a saved script and an edited draft combine with the overrides', async () => {
    await render(<Harness />)
    expect(container.querySelector('[data-testid="debug-session-overrides-source"]')?.textContent).toContain('The script itself is not sent')
    await render(<Harness storedUnmodified={false} />)
    expect(container.querySelector('[data-testid="debug-session-overrides-source"]')?.textContent).toContain('The run sends the editor’s copy of the script (it has unsaved edits, or its saved revision is not loaded), and these overrides apply on top of it')
  })

  it('tells a browser script that native browser replay does not apply the session overrides', async () => {
    await render(<Harness />)
    expect(container.querySelector('[data-testid="debug-session-overrides-native-replay"]')).toBeNull()
    await render(<Harness script={{ ...SCRIPT, protocol: 'browser' }} />)
    expect(container.querySelector('[data-testid="debug-session-overrides-native-replay"]')?.textContent).toBe('Session runtime overrides apply to debug runs only. Native browser replay uses the script’s runtime settings.')
  })

  it('starts timeout and max connection overrides at 1, since 0 means the engine default', async () => {
    await render(<Harness />)
    await selectGroup('transport_connections')
    for (const path of ['http.request_timeout_ms', 'http.connect_timeout_ms', 'http.tls_timeout_ms', 'http.idle_timeout_ms', 'http.max_conns_per_host']) {
      const input = field(path)?.querySelector<HTMLInputElement>('input')
      expect(input?.min).toBe('1')
      await typeInto(input!, '0')
    }
    for (const path of ['http.request_timeout_ms', 'http.connect_timeout_ms', 'http.tls_timeout_ms', 'http.idle_timeout_ms', 'http.max_conns_per_host']) {
      expect(sessionOverrideValue(latest, path)).toBe(1)
    }
  })

  it('overrides one timeout as a partial row with session provenance, then resets it', async () => {
    await render(<Harness />)
    await selectGroup('transport_connections')
    const input = field('http.request_timeout_ms')?.querySelector<HTMLInputElement>('input')
    expect(input).toBeTruthy()
    expect(input!.placeholder).toBe('Inherit (60000)')
    expect(field('http.request_timeout_ms')?.getAttribute('data-provenance')).toBe('engine_default')
    await typeInto(input!, '5000')
    expect(latest?.override_fields).toEqual(['http.request_timeout_ms'])
    expect(field('http.request_timeout_ms')?.getAttribute('data-provenance')).toBe('session_override')
    expect(container.querySelector('[data-testid="runtime-settings-canonical-group-overrides-transport_connections"]')?.textContent).toBe('1 override')
    const reset = field('http.request_timeout_ms')?.querySelector<HTMLButtonElement>('.runtime-override-reset')
    expect(reset).toBeTruthy()
    await act(async () => { reset!.click() })
    expect(latest).toBeUndefined()
  })

  it('offers a three-state keep-alive whose Inherit names the effective value', async () => {
    await render(<Harness />)
    await selectGroup('transport_connections')
    const select = field('http.keep_alive')?.querySelector<HTMLSelectElement>('select')
    expect(select).toBeTruthy()
    expect(select!.options[0].textContent).toBe('Inherit (Enabled)')
    await typeInto(select!, 'false')
    expect(latest?.http?.keep_alive).toBe(false)
    expect(latest?.override_fields).toEqual(['http.keep_alive'])
  })

  it('never shows the script proxy password and sends a typed one as an override', async () => {
    await render(<Harness />)
    await selectGroup('security_proxy_auth')
    const password = field('http.proxy.password')?.querySelector<HTMLInputElement>('input[type="password"]')
    expect(password).toBeTruthy()
    expect(password!.value).toBe('')
    expect(container.innerHTML).not.toContain('script-proxy-secret')
    await typeInto(password!, 'session-secret')
    expect(latest?.http?.proxy?.password).toBe('session-secret')
    expect(latest?.override_fields).toEqual(['http.proxy.password'])
  })

  it('leaves the Debug-option-owned settings to Debug options', async () => {
    await render(<Harness />)
    await selectGroup('errors_logging_diagnostics')
    expect(field('error_handling.max_retries')).toBeTruthy()
    expect(field('log.level')).toBeNull()
    expect(field('error_handling.continue_on_error')).toBeNull()
    expect(container.textContent).toContain('Log level and what follows a failure are Debug options')
    await selectGroup('run_logic_pacing')
    expect(container.textContent).toContain('Skipped in single-VUser debug replay')
    expect(container.querySelector('[data-override-path^="pacing"], [data-override-path^="run_logic"]')).toBeNull()
  })

  it('warns that think-time overrides wait for Ignore think time to turn off', async () => {
    await render(<Harness ignoreThinkTime />)
    await selectGroup('think_time')
    expect(container.textContent).toContain('Ignore think time is on in Debug options')
    expect(field('think_time.ignore')).toBeNull()
    await render(<Harness ignoreThinkTime={false} />)
    expect(container.textContent).not.toContain('Ignore think time is on in Debug options')
  })

  it('offers SOAP version and WS-Security only to SOAP scripts', async () => {
    await render(<Harness script={{ ...SCRIPT, protocol: 'soap' }} />)
    await selectGroup('protocol_behavior_session')
    expect(field('soap.version')).toBeTruthy()
    expect(field('soap.fault_handling.fail_on_fault')).toBeTruthy()
    await selectGroup('security_proxy_auth')
    expect(field('soap.ws_security.username_token.password')).toBeTruthy()
    await render(<Harness />)
    await selectGroup('protocol_behavior_session')
    expect(field('soap.version')).toBeNull()
  })

  it('adds a default header override by name', async () => {
    await render(<Harness />)
    await selectGroup('protocol_behavior_session')
    const name = container.querySelector<HTMLInputElement>('input[aria-label="Header name"]')
    const value = container.querySelector<HTMLInputElement>('input[aria-label="Header value"]')
    expect(name).toBeTruthy()
    expect(value).toBeTruthy()
    await typeInto(name!, 'X-Debug')
    await typeInto(value!, 'trace')
    const add = [...container.querySelectorAll('button')].find(button => button.textContent === 'Add header')
    expect(add).toBeTruthy()
    await act(async () => { add!.click() })
    expect(latest?.override_fields).toEqual(['http.default_headers.X-Debug'])
    expect(latest?.http?.default_headers).toEqual({ 'X-Debug': 'trace' })
  })

  it('promotes only when there is something to promote, then reports it', async () => {
    await render(<Harness />)
    const promote = () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Promote session overrides to script')
    expect(promote()?.disabled).toBe(true)
    const baseURL = field('base_url')?.querySelector<HTMLInputElement>('input')
    expect(baseURL).toBeTruthy()
    await typeInto(baseURL!, 'https://staging.example.test')
    expect(promote()?.disabled).toBe(false)
    await act(async () => { promote()!.click() })
    expect(onPromote).toHaveBeenCalledTimes(1)
    expect(container.querySelector('[role="status"]')?.textContent).toContain('Promoted 2 settings')
  })

  it('clears every session override at once', async () => {
    await render(<Harness />)
    const baseURL = field('base_url')?.querySelector<HTMLInputElement>('input')
    expect(baseURL).toBeTruthy()
    await typeInto(baseURL!, 'https://staging.example.test')
    const clear = [...container.querySelectorAll('button')].find(button => button.textContent === 'Clear session runtime overrides')
    expect(clear).toBeTruthy()
    await act(async () => { clear!.click() })
    expect(latest).toBeUndefined()
  })

  it('locks the fields while a run is live', async () => {
    await render(<Harness disabled />)
    expect(container.querySelector<HTMLFieldSetElement>('fieldset.debug-session-override-fields')?.disabled).toBe(true)
  })
})
