// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ImportRuntimeOptions from './ImportRuntimeOptions'
import { type ScriptDefinition } from '../types/scripts'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function buildScript(): ScriptDefinition {
  return {
    name: 'Generated',
    version: '1.0',
    protocol: 'http',
    config: {
      base_url: 'https://api.example.test',
      think_time: { type: 'fixed', mean_ms: 1000 },
    },
    actions: [],
  }
}

function setNativeValue(input: HTMLInputElement | HTMLSelectElement, value: string) {
  const proto = input instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype
  const setter = Object.getOwnPropertyDescriptor(proto, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('change', { bubbles: true }))
}

describe('ImportRuntimeOptions', () => {
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

  function render(script: ScriptDefinition, onChange = vi.fn()) {
    act(() => {
      root.render(<ImportRuntimeOptions script={script} onChange={onChange} />)
    })
    return onChange
  }

  function inputByLabel(text: string): HTMLInputElement {
    const label = Array.from(container.querySelectorAll('label')).find(l => l.textContent?.includes(text))
    const input = label?.querySelector('input, select')
    expect(input, `input for label ${text}`).toBeTruthy()
    return input as HTMLInputElement
  }

  it('shows current config values', () => {
    render(buildScript())
    expect(inputByLabel('Think Time').value).toBe('1000')
    expect((inputByLabel('Pacing') as unknown as HTMLSelectElement).value).toBe('none')
    expect((inputByLabel('Authentication') as unknown as HTMLSelectElement).value).toBe('none')
  })

  it('lifts think time changes without mutating the original script', () => {
    const script = buildScript()
    const onChange = render(script)
    act(() => {
      setNativeValue(inputByLabel('Think Time'), '2500')
    })
    expect(onChange).toHaveBeenCalledTimes(1)
    const updated = onChange.mock.calls[0][0] as ScriptDefinition
    expect(updated.config.think_time?.mean_ms).toBe(2500)
    expect(script.config.think_time?.mean_ms).toBe(1000)
  })

  it('inserts env-placeholder auth when bearer is selected', () => {
    const onChange = render(buildScript())
    act(() => {
      setNativeValue(inputByLabel('Authentication'), 'bearer')
    })
    const updated = onChange.mock.calls[0][0] as ScriptDefinition
    expect(updated.config.http?.auth).toEqual({ type: 'bearer', token: '{{env.API_TOKEN}}' })
  })

  it('sets pacing delay when fixed_delay is chosen', () => {
    const script = buildScript()
    script.config.pacing = { type: 'fixed_delay', delay_ms: 100 }
    const onChange = render(script)
    act(() => {
      setNativeValue(inputByLabel('Pacing Delay'), '750')
    })
    const updated = onChange.mock.calls[0][0] as ScriptDefinition
    expect(updated.config.pacing).toEqual({ type: 'fixed_delay', delay_ms: 750 })
  })

  it('toggles continue on error', () => {
    const onChange = render(buildScript())
    const checkbox = inputByLabel('Continue on error')
    act(() => {
      checkbox.click()
    })
    const updated = onChange.mock.calls[0][0] as ScriptDefinition
    expect(updated.config.error_handling?.continue_on_error).toBe(true)
  })

  it('changes the JMS protocol and provider as one execution contract', () => {
    const script = buildScript()
    script.protocol = 'jms-solace'
    script.config.jms = {
      provider: 'solace',
      message_vpn: 'orders-vpn',
      destination: 'orders',
      subscription: 'orders-subscription',
      mock: true,
    }
    const onChange = render(script)

    act(() => {
      setNativeValue(inputByLabel('Provider'), 'servicebus')
    })

    const updated = onChange.mock.calls[0][0] as ScriptDefinition
    expect(updated.protocol).toBe('jms-servicebus')
    expect(updated.config.jms).toEqual(expect.objectContaining({
      provider: 'servicebus',
      destination: 'orders',
      subscription: 'orders-subscription',
      mock: true,
    }))
    expect(updated.config.jms).not.toHaveProperty('message_vpn')
    expect(script.protocol).toBe('jms-solace')
    expect(script.config.jms?.provider).toBe('solace')
  })
})
