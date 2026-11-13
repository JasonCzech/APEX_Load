// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { HTTPTransportTabId, RuntimeSettingsSectionId } from './scriptEditorRuntimeSettingsModel'
import type { ScriptDefinition } from '../types/scripts'
import { ScriptRuntimeSettingsView } from './ScriptRuntimeSettingsView'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

// These tests pin Author controls to what the engine does at runtime. The
// harness keeps the script in state, as the Script Editor does, so each
// keystroke re-renders from the stored value.

let latest: ScriptDefinition | null = null

function Harness({ initial, section, tab = 'timeouts' }: { initial: ScriptDefinition; section: RuntimeSettingsSectionId; tab?: HTTPTransportTabId }) {
  const [script, setScript] = useState(initial)
  latest = script
  return (
    <ScriptRuntimeSettingsView
      activeHTTPTransportTab={tab}
      activeSection={section}
      canEditProtocol={false}
      onSave={vi.fn()}
      onSelectHTTPTransportTab={vi.fn()}
      onSelectSection={vi.fn()}
      onUpdateScript={updater => setScript(prev => updater(prev))}
      saveDisabled={false}
      saving={false}
      script={script}
    />
  )
}

function httpScript(config: Partial<ScriptDefinition['config']>, extra: Partial<ScriptDefinition> = {}): ScriptDefinition {
  return {
    name: 'Runtime semantics',
    version: '1.0.0',
    protocol: 'http',
    config: { base_url: 'https://example.test', ...config },
    actions: [{ name: 'Login', type: 'http_request', target: '/login' }],
    ...extra,
  }
}

function typeInto(input: HTMLInputElement, value: string) {
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
}

function chooseOption(select: HTMLSelectElement, value: string) {
  act(() => {
    select.value = value
    select.dispatchEvent(new Event('change', { bubbles: true }))
  })
}

function buttonByText(container: HTMLElement, text: string): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(btn => btn.textContent?.trim() === text)
}

describe('ScriptRuntimeSettingsView runtime semantics', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    latest = null
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  const render = (initial: ScriptDefinition, section: RuntimeSettingsSectionId, tab?: HTTPTransportTabId) => {
    act(() => root.render(<Harness initial={initial} section={section} tab={tab} />))
  }

  it('shows an imported WS-Security password as readable, editable text', () => {
    render({
      name: 'Order service',
      version: '1.0.0',
      protocol: 'soap',
      config: { base_url: 'https://soap.example.test', soap: { version: '1.1', ws_security: { username_token: { username: 'svc', password: 'imported-wsse-pass' } } } },
      actions: [],
    }, 'soap')
    const input = container.querySelector<HTMLInputElement>('input[aria-label="WS-Security password"]')!
    expect(input.type).toBe('text')
    expect(input.value).toBe('imported-wsse-pass')
    typeInto(input, '{{secret.SOAP_WSSE_PASSWORD}}')
    expect(latest?.config.soap?.ws_security?.username_token?.password).toBe('{{secret.SOAP_WSSE_PASSWORD}}')
  })

  it('keeps a typed comma in Retry on conditions so a second condition can be typed', () => {
    render(httpScript({}), 'errors')
    const input = container.querySelector<HTMLInputElement>('input[aria-label="Retry on conditions"]')!
    typeInto(input, '429')
    typeInto(input, '429,')
    expect(input.value).toBe('429,')
    typeInto(input, '429, ')
    expect(input.value).toBe('429, ')
    typeInto(input, '429, 5xx')
    expect(latest?.config.error_handling?.retry_on).toEqual(['429', '5xx'])
    act(() => {
      input.dispatchEvent(new FocusEvent('blur', { bubbles: false }))
      input.dispatchEvent(new FocusEvent('focusout', { bubbles: true }))
    })
    expect(input.value).toBe('429, 5xx')
  })

  it('keeps a typed comma or colon in a run block actions field', () => {
    render(httpScript({
      run_logic: { blocks: [{ name: 'Block 1', mode: 'sequential', iterations: 1, children: [{ action: 'Login' }] }] },
    }), 'runlogic')
    const input = container.querySelector<HTMLInputElement>('input[aria-label="Run block 1 actions"]')!
    typeInto(input, 'Login,')
    expect(input.value).toBe('Login,')
    typeInto(input, 'Login, Checkout')
    expect(latest?.config.run_logic?.blocks?.[0].children).toEqual([{ action: 'Login' }, { action: 'Checkout' }])
    typeInto(input, 'Login, Checkout:')
    expect(input.value).toBe('Login, Checkout:')
    typeInto(input, 'Login, Checkout:30')
    expect(latest?.config.run_logic?.blocks?.[0].children).toEqual([{ action: 'Login' }, { action: 'Checkout', weight: 30 }])
  })

  it('lets a percentage weight be typed after the colon', () => {
    render(httpScript({
      run_logic: { blocks: [{ name: 'Mix', mode: 'percentage', iterations: 1, children: [{ action: 'Browse', weight: 100 }] }] },
    }), 'runlogic')
    const input = container.querySelector<HTMLInputElement>('input[aria-label="Run block 1 actions"]')!
    expect(input.value).toBe('Browse:100')
    typeInto(input, 'Browse:')
    expect(input.value).toBe('Browse:')
    typeInto(input, 'Browse:7')
    typeInto(input, 'Browse:70')
    expect(latest?.config.run_logic?.blocks?.[0].children).toEqual([{ action: 'Browse', weight: 70 }])
    expect(input.value).toBe('Browse:70')
  })

  it('keeps a server-shaped 0% child (weight omitted) at 0% when another weight is edited', () => {
    render(httpScript({
      run_logic: { blocks: [{ name: 'Mix', mode: 'percentage', iterations: 1, children: [{ action: 'A' }, { action: 'B', weight: 50 }] }] },
    }), 'runlogic')
    const input = container.querySelector<HTMLInputElement>('input[aria-label="Run block 1 actions"]')!
    expect(input.value).toBe('A:0, B:50')
    typeInto(input, 'A:0, B:60')
    expect(latest?.config.run_logic?.blocks?.[0].children).toEqual([{ action: 'A', weight: 0 }, { action: 'B', weight: 60 }])
    expect(input.value).toBe('A:0, B:60')
  })

  it('shows the stored weight while a typed weight would not survive parsing', () => {
    render(httpScript({
      run_logic: { blocks: [{ name: 'Mix', mode: 'random', iterations: 1, children: [{ action: 'Login' }] }] },
    }), 'runlogic')
    const input = container.querySelector<HTMLInputElement>('input[aria-label="Run block 1 actions"]')!
    for (const typed of ['Login:0', 'Login:-3', 'Login:abc']) {
      typeInto(input, typed)
      expect(latest?.config.run_logic?.blocks?.[0].children).toEqual([{ action: 'Login', weight: 1 }])
      expect(input.value).toBe('Login:1')
    }
    typeInto(input, 'Login:2')
    expect(input.value).toBe('Login:2')
  })

  it('keeps a nested block in place when the action list is edited', () => {
    const nested = { name: 'Browse loop', mode: 'sequential' as const, iterations: 3, children: [{ action: 'Browse' }] }
    render(httpScript({
      run_logic: { blocks: [{ name: 'Main', mode: 'sequential', iterations: 1, children: [{ action: 'Login' }, { block: nested }, { action: 'Logot' }] }] },
    }), 'runlogic')
    const input = container.querySelector<HTMLInputElement>('input[aria-label="Run block 1 actions"]')!
    expect(input.value).toBe('Login, Logot')
    typeInto(input, 'Login, Logout')
    expect(latest?.config.run_logic?.blocks?.[0].children).toEqual([{ action: 'Login' }, { block: nested }, { action: 'Logout' }])
  })

  it('does not offer a script-level force control and lets an imported force be cleared', () => {
    render(httpScript({ think_time: { type: 'fixed', mean_ms: 1000, force: true } }), 'think')
    expect(container.querySelector('input[aria-label="Force exact think time"]')).toBeNull()
    const notice = container.querySelector('[data-testid="script-think-time-force-notice"]')
    expect(notice?.textContent).toContain("applies only to an action's own think time")
    act(() => buttonByText(container, 'Clear script-level force')!.click())
    expect(latest?.config.think_time).toEqual({ type: 'fixed', mean_ms: 1000 })
    expect(container.querySelector('[data-testid="script-think-time-force-notice"]')).toBeNull()
  })

  it('labels the exponential maximum as a cap base and states the 4x cap', () => {
    render(httpScript({ think_time: { type: 'exponential', mean_ms: 2000, max_ms: 5000 } }), 'think')
    const help = container.querySelector('[data-testid="exponential-think-cap-help"]')
    expect(help?.textContent).toContain('4 × this value (20 s)')
    // DSL validation rejects min_ms > max_ms, so the help must not imply a minimum may sit between max and 4 × max.
    expect(help?.textContent).toContain('Min (s) must not exceed this value')
    expect(Array.from(container.querySelectorAll('label span')).some(span => span.textContent === 'Max (s)')).toBe(false)
  })

  it('keeps a quota visible and editable after the pacing mode changes', () => {
    const llmScript = httpScript({ pacing: { type: 'none' } }, {
      actions: [{ name: 'Chat', type: 'http_request', target: '/v1/chat', params: { llm_dialect: 'openai' } }],
    })
    render(llmScript, 'pacing')
    const modeSelect = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(sel => sel.closest('label')?.textContent?.includes('Pacing Mode'))!
    expect(Array.from(modeSelect.options).map(opt => opt.value)).not.toContain('quota')
    typeInto(container.querySelector<HTMLInputElement>('input[aria-label="Quota TPM"]')!, '60000')
    // Editing the quota no longer switches pacing to the unpaced quota mode.
    expect(latest?.config.pacing).toEqual({ type: 'none', quota: { tpm: 60000 } })
    chooseOption(modeSelect, 'tps')
    expect(latest?.config.pacing?.type).toBe('tps')
    expect(container.querySelector<HTMLInputElement>('input[aria-label="Quota TPM"]')?.value).toBe('60000')
    expect(container.querySelector('[data-testid="pacing-quota-no-llm-notice"]')).toBeNull()
    act(() => buttonByText(container, 'Clear quota')!.click())
    expect((latest?.config.pacing as { quota?: unknown }).quota).toBeUndefined()
  })

  it('shows a stored quota on a script without LLM actions and says it throttles nothing', () => {
    render(httpScript({ pacing: { type: 'fixed_delay', delay_ms: 1000, quota: { tpm: 1000 } } as ScriptDefinition['config']['pacing'] }), 'pacing')
    expect(container.querySelector('[data-testid="pacing-quota-editor"]')).not.toBeNull()
    expect(container.querySelector('[data-testid="pacing-quota-no-llm-notice"]')?.textContent).toContain('throttles nothing')
  })

  it('hides the quota editor on a plain HTTP script without a quota', () => {
    render(httpScript({ pacing: { type: 'none' } }), 'pacing')
    expect(container.querySelector('[data-testid="pacing-quota-editor"]')).toBeNull()
  })

  it('explains the legacy quota mode as unpaced', () => {
    render(httpScript({ pacing: { type: 'quota' } }), 'pacing')
    const modeSelect = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(sel => sel.closest('label')?.textContent?.includes('Pacing Mode'))!
    expect(modeSelect.value).toBe('quota')
    expect(container.querySelector('[data-testid="pacing-quota-mode-notice"]')?.textContent).toContain('does not pace iterations')
  })

  it('offers arrival process, seed and rate stages only for open workloads', () => {
    render(httpScript({ pacing: { type: 'tps', tps: 10 } }), 'pacing')
    expect(container.querySelector('select[aria-label="Arrival process"]')).toBeNull()
    expect(container.querySelector('input[aria-label="Arrival seed"]')).toBeNull()
    expect(container.querySelector('[data-testid="pacing-rate-stages-editor"]')).toBeNull()
    expect(container.querySelector('[data-testid="pacing-open-only-settings-hint"]')).not.toBeNull()
  })

  it('flags and clears open-only arrival settings stored on a closed workload', () => {
    render(httpScript({
      pacing: { type: 'tps', tps: 10, arrival_process: 'poisson', seed: 4, rate_stages: [{ duration_seconds: 30, target_tps: 50 }] } as ScriptDefinition['config']['pacing'],
    }), 'pacing')
    expect(container.querySelector('[data-testid="pacing-open-only-settings-notice"]')?.textContent).toContain('apply only to open workloads')
    act(() => buttonByText(container, 'Clear open-workload settings')!.click())
    expect(latest?.config.pacing).toEqual({ type: 'tps', tps: 10 })
  })

  it('offers the clear action for leftover invalid arrival settings on a closed, non-TPS workload', () => {
    for (const type of ['fixed_delay', 'none', 'random_interval']) {
      act(() => root.unmount())
      root = createRoot(container)
      render(httpScript({
        pacing: { type, delay_ms: 1000, arrival_process: 'gamma', burstiness: 5000, rate_stages: [{ duration_seconds: 30, target_tps: 50 }] } as ScriptDefinition['config']['pacing'],
      }), 'pacing')
      const notice = container.querySelector('[data-testid="pacing-open-only-settings-notice"]')
      expect(notice?.textContent).toContain('apply only to open workloads')
      expect(notice?.textContent).not.toContain('constant Target TPS')
      act(() => buttonByText(container, 'Clear open-workload settings')!.click())
      expect(latest?.config.pacing).toEqual({ type, delay_ms: 1000 })
      expect(container.querySelector('[data-testid="pacing-open-only-settings-notice"]')).toBeNull()
    }
  })

  it('clamps an arrival seed beyond the safe-integer range so it saves as the int64 the engine reads', () => {
    render(httpScript({ run_logic: { workload_model: 'open' }, pacing: { type: 'tps', tps: 10 } }), 'pacing')
    const seed = container.querySelector<HTMLInputElement>('input[aria-label="Arrival seed"]')!
    typeInto(seed, '1e20')
    expect((latest?.config.pacing as { seed?: number }).seed).toBe(Number.MAX_SAFE_INTEGER)
    expect(JSON.stringify(latest?.config.pacing)).not.toContain('100000000000000000000')
    typeInto(seed, '-1e20')
    expect((latest?.config.pacing as { seed?: number }).seed).toBe(-Number.MAX_SAFE_INTEGER)
    typeInto(seed, '9007199254740993')
    expect((latest?.config.pacing as { seed?: number }).seed).toBe(Number.MAX_SAFE_INTEGER)
  })

  it('treats any typed seed as fixed, keeps 0 and negatives, and leaves an empty seed random', () => {
    render(httpScript({ run_logic: { workload_model: 'open' }, pacing: { type: 'tps', tps: 10 } }), 'pacing')
    const seed = container.querySelector<HTMLInputElement>('input[aria-label="Arrival seed"]')!
    expect(seed.placeholder).not.toContain('0 (random)')
    expect(seed.min).toBe('')
    typeInto(seed, '0')
    expect((latest?.config.pacing as { seed?: number }).seed).toBe(0)
    typeInto(seed, '-5')
    expect((latest?.config.pacing as { seed?: number }).seed).toBe(-5)
    typeInto(seed, '1.7')
    expect((latest?.config.pacing as { seed?: number }).seed).toBe(1)
    typeInto(seed, '')
    expect((latest?.config.pacing as { seed?: number }).seed).toBeUndefined()
  })

  it('gives gamma arrivals a valid burstiness when selected', () => {
    render(httpScript({ run_logic: { workload_model: 'open' }, pacing: { type: 'tps', tps: 10 } }), 'pacing')
    chooseOption(container.querySelector<HTMLSelectElement>('select[aria-label="Arrival process"]')!, 'gamma')
    expect(latest?.config.pacing).toMatchObject({ arrival_process: 'gamma', burstiness: 1 })
    const burstiness = container.querySelector<HTMLInputElement>('input[aria-label="Arrival burstiness"]')!
    expect(burstiness.value).toBe('1')
    typeInto(burstiness, '')
    expect(container.querySelector('.runtime-timing-issues')?.textContent).toContain('Gamma arrivals need a burstiness above 0')
  })

  it('shows imported block scheduling as preserved, not as an execution control', () => {
    render(httpScript({ run_logic: { block_scheduling: 'random' } }), 'runlogic')
    expect(container.querySelector('select[aria-label="Block scheduling"]')).toBeNull()
    const notice = container.querySelector('[data-testid="run-logic-block-scheduling-notice"]')
    expect(notice?.textContent).toContain('does not change execution order')
    expect(notice?.textContent).not.toContain('shared-JVM pooling')
    expect(container.textContent).not.toContain('read-only under Imported Source Runtime Settings')
  })

  it('warns that non-sequential block scheduling turns off shared-JVM pooling on Java VUser scripts', () => {
    render({
      name: 'Java pooled',
      version: '1.0.0',
      protocol: 'java_vuser',
      config: { base_url: '', run_logic: { block_scheduling: 'percentage' } },
      actions: [],
    }, 'runlogic')
    expect(container.querySelector('[data-testid="run-logic-block-scheduling-notice"]')?.textContent).toContain('turns off shared-JVM pooling')
  })

  it('resets Java VUser block scheduling to sequential so shared-JVM pooling can apply', () => {
    render({
      name: 'Java pooled',
      version: '1.0.0',
      protocol: 'java_vuser',
      config: { base_url: '', run_logic: { block_scheduling: 'random', iterations: 2 } },
      actions: [],
    }, 'runlogic')
    act(() => buttonByText(container, 'Reset to sequential')!.click())
    expect(latest?.config.run_logic).toEqual({ block_scheduling: 'sequential', iterations: 2 })
    const notice = container.querySelector('[data-testid="run-logic-block-scheduling-notice"]')
    expect(notice?.textContent).not.toContain('shared-JVM pooling')
    expect(buttonByText(container, 'Reset to sequential')).toBeUndefined()
  })

  it('treats a non-exact sequential spelling as pooling-off, as the engine gate does', () => {
    render({
      name: 'Java pooled',
      version: '1.0.0',
      protocol: 'java_vuser',
      config: { base_url: '', run_logic: { block_scheduling: 'Sequential' } },
      actions: [],
    }, 'runlogic')
    expect(buttonByText(container, 'Reset to sequential')).toBeDefined()
  })

  it('does not offer the Java reset on HTTP scripts', () => {
    render(httpScript({ run_logic: { block_scheduling: 'random' } }), 'runlogic')
    expect(buttonByText(container, 'Reset to sequential')).toBeUndefined()
  })

  it('treats a mixed-case or padded open workload model as open, as the engine does', () => {
    for (const model of ['Open', ' open ']) {
      act(() => root.unmount())
      root = createRoot(container)
      const pacing = { type: 'tps', tps: 10, arrival_process: 'gamma', burstiness: 2, seed: 4, rate_stages: [{ duration_seconds: 30, target_tps: 50 }] } as ScriptDefinition['config']['pacing']
      render(httpScript({ run_logic: { workload_model: model }, pacing }), 'pacing')
      expect(container.querySelector('[data-testid="pacing-open-only-settings-notice"]')).toBeNull()
      expect(buttonByText(container, 'Clear open-workload settings')).toBeUndefined()
      expect(container.querySelector('select[aria-label="Arrival process"]')).not.toBeNull()
      expect(container.querySelector('[data-testid="pacing-rate-stages-editor"]')).not.toBeNull()
      expect(latest?.config.pacing).toEqual(pacing)
    }
  })

  it('selects Open arrival-rate for a mixed-case stored workload model', () => {
    render(httpScript({ run_logic: { workload_model: 'Open' }, pacing: { type: 'tps', tps: 10 } }), 'runlogic')
    const select = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(sel => sel.closest('label')?.textContent?.includes('Workload Model'))!
    expect(select.value).toBe('open')
    expect(container.querySelector('input[aria-label="Arrival target"]')).not.toBeNull()
  })

  it('removes a deleted run block weight instead of keeping it hidden', () => {
    render(httpScript({
      run_logic: { blocks: [{ name: 'Mix', mode: 'random', iterations: 1, children: [{ action: 'Login', weight: 3 }] }] },
    }), 'runlogic')
    const input = container.querySelector<HTMLInputElement>('input[aria-label="Run block 1 actions"]')!
    expect(input.value).toBe('Login:3')
    typeInto(input, 'Login:')
    expect(input.value).toBe('Login:')
    typeInto(input, 'Login')
    expect(latest?.config.run_logic?.blocks?.[0].children).toEqual([{ action: 'Login' }])
    act(() => {
      input.dispatchEvent(new FocusEvent('blur', { bubbles: false }))
      input.dispatchEvent(new FocusEvent('focusout', { bubbles: true }))
    })
    expect(input.value).toBe('Login')
  })

  it('describes an empty max response bytes as the engine bound, not unlimited', () => {
    render(httpScript({}), 'log')
    const input = container.querySelector<HTMLInputElement>('input[aria-label="Max response bytes"]')!
    expect(input.placeholder).not.toMatch(/unlimited/i)
    expect(input.closest('label')?.textContent).toContain('4 MiB response-evidence bound')
  })

  it('warns about a TLS maximum below 1.3 when HTTP/3 is inherited from the protocol', () => {
    render({
      name: 'HTTP/3 protocol',
      version: '1.0.0',
      protocol: 'http/3',
      config: { base_url: 'https://example.test', http: { tls: { max_version: '1.2' } } },
      actions: [],
    }, 'http', 'tls')
    expect(container.querySelector('[data-testid="http-tls-max-version-h3-warning"]')?.textContent).toContain('HTTP/3 (QUIC) requires TLS 1.3')
  })

  it('shows the HTTP/3 execution note when HTTP/3 is inherited from the protocol', () => {
    render({
      name: 'HTTP/3 protocol',
      version: '1.0.0',
      protocol: 'http/3',
      config: { base_url: 'https://example.test' },
      actions: [],
    }, 'http', 'connection')
    expect(container.querySelector('.http3-execution-note')?.textContent).toContain('HTTP/3 execution')
  })

  it('shows no HTTP/3 execution note when the protocol defaults to auto-negotiation', () => {
    render(httpScript({}), 'http', 'connection')
    expect(container.querySelector('.http3-execution-note')).toBeNull()
  })

  it('does not warn about TLS 1.2 on an http/3 script whose version is explicitly auto', () => {
    render({
      name: 'HTTP/3 protocol, auto version',
      version: '1.0.0',
      protocol: 'http/3',
      config: { base_url: 'https://example.test', http: { version: 'auto', tls: { max_version: '1.2' } } },
      actions: [],
    }, 'http', 'tls')
    expect(container.querySelector('[data-testid="http-tls-max-version-h3-warning"]')).toBeNull()
  })

  it('does not warn about TLS 1.2 on an http/3 script whose version is invalid', () => {
    render({
      name: 'HTTP/3 protocol, invalid version',
      version: '1.0.0',
      protocol: 'http/3',
      config: { base_url: 'https://example.test', http: { version: 'bogus', tls: { max_version: '1.2' } } },
      actions: [],
    }, 'http', 'tls')
    expect(container.querySelector('[data-testid="http-tls-max-version-h3-warning"]')).toBeNull()
  })

  it('does not warn about TLS 1.2 when the protocol defaults to auto-negotiation', () => {
    render(httpScript({ http: { tls: { max_version: '1.2' } } }), 'http', 'tls')
    expect(container.querySelector('[data-testid="http-tls-max-version-h3-warning"]')).toBeNull()
  })
})
