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

describe('ScriptRuntimeSettingsView canonical pacing and exponential cap', () => {
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

  const render = (initial: ScriptDefinition, section: RuntimeSettingsSectionId) => {
    act(() => root.render(<Harness initial={initial} section={section} />))
  }

  it('treats a stored "TPS" pacing type as TPS pacing and shows the arrival controls', () => {
    render(httpScript({ run_logic: { workload_model: 'Open' }, pacing: { type: 'TPS', tps: 5 } }), 'pacing')
    const modeSelect = Array.from(container.querySelectorAll<HTMLSelectElement>('select'))
      .find(sel => sel.closest('label')?.textContent?.includes('Pacing Mode'))!
    expect(modeSelect.value).toBe('tps')
    expect(Array.from(container.querySelectorAll('label span')).some(span => span.textContent === 'Target TPS')).toBe(true)
    expect(container.querySelector('select[aria-label="Arrival process"]')).not.toBeNull()
    expect(container.querySelector('[data-testid="pacing-rate-stages-editor"]')).not.toBeNull()
    expect(latest?.config.pacing).toEqual({ type: 'TPS', tps: 5 })
  })

  it('reads a stored "Gamma" arrival process as gamma and shows Burstiness', () => {
    render(httpScript({ run_logic: { workload_model: 'open' }, pacing: { type: 'tps', tps: 5, arrival_process: ' Gamma ' } as ScriptDefinition['config']['pacing'] }), 'pacing')
    expect(container.querySelector<HTMLSelectElement>('select[aria-label="Arrival process"]')!.value).toBe('gamma')
    expect(container.querySelector('input[aria-label="Arrival burstiness"]')).not.toBeNull()
  })

  it('states the exponential stop after the think-time multiplier', () => {
    render(httpScript({ think_time: { type: 'exponential', mean_ms: 1000, max_ms: 2000, multiplier: 2 } }), 'think')
    const help = container.querySelector('[data-testid="exponential-think-cap-help"]')
    expect(help?.textContent).toContain('then the × 2 multiplier applies (16 s)')
    expect(help?.textContent).not.toContain('(8 s)')
  })

  it('keeps the plain 4x stop when the multiplier is the default', () => {
    render(httpScript({ think_time: { type: 'exponential', mean_ms: 1000, max_ms: 2000, multiplier: 1 } }), 'think')
    expect(container.querySelector('[data-testid="exponential-think-cap-help"]')?.textContent).toContain('4 × this value (8 s)')
  })
})
