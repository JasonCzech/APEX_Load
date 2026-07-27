// @vitest-environment jsdom

import { act, useState } from 'react'
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
    expect(inputByLabel('Think Time (ms)').value).toBe('1000')
    expect((inputByLabel('Pacing') as unknown as HTMLSelectElement).value).toBe('none')
    expect((inputByLabel('Authentication') as unknown as HTMLSelectElement).value).toBe('none')
  })

  it('lifts think time changes without mutating the original script', () => {
    const script = buildScript()
    const onChange = render(script)
    act(() => {
      setNativeValue(inputByLabel('Think Time (ms)'), '2500')
    })
    expect(onChange).toHaveBeenCalledTimes(1)
    const updated = onChange.mock.calls[0][0] as ScriptDefinition
    expect(updated.config.think_time?.mean_ms).toBe(2500)
    expect(script.config.think_time?.mean_ms).toBe(1000)
  })

  it('normalizes out-of-range think-time multipliers instead of authoring zero or values above 100', () => {
    const script = buildScript()
    script.config.think_time = { type: 'recorded', multiplier: 0 }
    const onChange = render(script)
    const multiplier = inputByLabel('Think Time Multiplier')
    expect(multiplier.value).toBe('1')
    expect(multiplier.min).toBe('0')
    expect(multiplier.max).toBe('100')
    expect(multiplier.step).toBe('any')

    act(() => multiplier.focus())
    act(() => setNativeValue(multiplier, '0'))
    expect(multiplier.value).toBe('0')
    expect(onChange).not.toHaveBeenCalled()
    act(() => multiplier.blur())
    expect(onChange.mock.calls[0][0].config.think_time?.multiplier).toBeUndefined()
    act(() => multiplier.focus())
    act(() => setNativeValue(multiplier, '101'))
    act(() => multiplier.blur())
    expect(onChange.mock.calls[1][0].config.think_time?.multiplier).toBeUndefined()
    act(() => multiplier.focus())
    act(() => setNativeValue(multiplier, '0.05'))
    expect(onChange.mock.calls[2][0].config.think_time?.multiplier).toBe(0.05)
  })

  it('round-trips recorded think time and a five-second fixed interval', () => {
    const script = buildScript()
    script.config.think_time = { type: 'recorded', mean_ms: 0, multiplier: 1 }
    script.config.pacing = { type: 'fixed_interval', interval_ms: 5000 }
    render(script)
    expect(inputByLabel('Think Time Mode').value).toBe('recorded')
    expect(container.textContent).toContain('Replay think time as recorded')
    expect(container.textContent).not.toContain('Think Time (ms)')
    expect(inputByLabel('Pacing').value).toBe('fixed_interval')
    expect(container.textContent).toContain('Pacing Interval (seconds)')
    expect(inputByLabel('Pacing Interval').value).toBe('5')
  })

  it('clears hidden distribution fields when recorded replay is selected', () => {
    const script = buildScript()
    script.config.think_time = {
      type: 'gaussian',
      mean_ms: 1000,
      std_ms: 200,
      min_ms: 100,
      max_ms: 3000,
      multiplier: 2,
    }
    const onChange = render(script)

    act(() => {
      setNativeValue(inputByLabel('Think Time Mode'), 'recorded')
    })

    expect(onChange.mock.calls[0][0].config.think_time).toEqual({
      type: 'recorded',
      multiplier: 2,
    })
  })

  it('round-trips disabled think time without authoring a hidden delay', () => {
    const script = buildScript()
    script.config.think_time = {
      type: 'gaussian',
      mean_ms: 1000,
      std_ms: 200,
      min_ms: 100,
      max_ms: 3000,
      multiplier: 2,
    }
    const onChange = render(script)

    act(() => {
      setNativeValue(inputByLabel('Think Time Mode'), 'none')
    })

    const updated = onChange.mock.calls[0][0] as ScriptDefinition
    expect(updated.config.think_time).toEqual({
      type: 'none',
      multiplier: 2,
    })
    render(updated)
    expect(container.textContent).not.toContain('Think Time (ms)')
  })

  it('initializes uniform bounds from fixed think time instead of producing a zero-delay config', () => {
    const onChange = render(buildScript())

    act(() => {
      setNativeValue(inputByLabel('Think Time Mode'), 'uniform')
    })

    expect(onChange.mock.calls[0][0].config.think_time).toEqual({
      type: 'uniform',
      min_ms: 1000,
      max_ms: 1000,
    })
  })

  it('edits and round-trips the fields consumed by uniform and gaussian modes', () => {
    const uniformScript = buildScript()
    uniformScript.config.think_time = { type: 'uniform', min_ms: 250, max_ms: 1750, multiplier: 2 }
    const uniformChange = render(uniformScript)

    expect(container.textContent).not.toContain('Mean Think Time (ms)')
    expect(inputByLabel('Minimum Think Time').value).toBe('250')
    expect(inputByLabel('Maximum Think Time').value).toBe('1750')
    act(() => {
      setNativeValue(inputByLabel('Maximum Think Time'), '2250')
    })
    expect(uniformChange.mock.calls[0][0].config.think_time).toEqual({
      type: 'uniform',
      min_ms: 250,
      max_ms: 2250,
      multiplier: 2,
    })

    const gaussianScript = buildScript()
    gaussianScript.config.think_time = {
      type: 'gaussian',
      mean_ms: 1000,
      std_ms: 200,
      min_ms: 100,
      max_ms: 2500,
    }
    const gaussianChange = vi.fn()
    render(gaussianScript, gaussianChange)
    expect(inputByLabel('Mean Think Time').value).toBe('1000')
    expect(inputByLabel('Think Time Std Dev').value).toBe('200')
    act(() => {
      setNativeValue(inputByLabel('Think Time Std Dev'), '300')
    })
    expect(gaussianChange.mock.calls[0][0].config.think_time).toEqual({
      type: 'gaussian',
      mean_ms: 1000,
      std_ms: 300,
      min_ms: 100,
      max_ms: 2500,
    })
  })

  it('inserts env-placeholder auth when bearer is selected', () => {
    const onChange = render(buildScript())
    act(() => {
      setNativeValue(inputByLabel('Authentication'), 'bearer')
    })
    const updated = onChange.mock.calls[0][0] as ScriptDefinition
    expect(updated.config.http?.auth).toEqual({ type: 'bearer', token: '{{env.API_TOKEN}}' })
  })

  it('keeps a sequential decimal pacing draft while persisting milliseconds', () => {
    const initialScript = buildScript()
    initialScript.config.pacing = { type: 'fixed_delay', delay_ms: 100 }
    const onChange = vi.fn()

    function Harness() {
      const [script, setScript] = useState(initialScript)
      return (
        <ImportRuntimeOptions
          script={script}
          onChange={next => {
            onChange(next)
            setScript(next)
          }}
        />
      )
    }

    act(() => root.render(<Harness />))
    const delayInput = inputByLabel('Pacing Delay')
    act(() => delayInput.dispatchEvent(new FocusEvent('focusin', { bubbles: true })))

    act(() => setNativeValue(delayInput, ''))
    expect(delayInput.value).toBe('')

    act(() => setNativeValue(delayInput, '0'))
    expect(delayInput.value).toBe('0')

    act(() => delayInput.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: '.' })))
    act(() => setNativeValue(delayInput, '0.7'))
    act(() => setNativeValue(delayInput, '0.75'))
    expect(delayInput.value).toBe('0.75')

    const updated = onChange.mock.calls[onChange.mock.calls.length - 1][0] as ScriptDefinition
    expect(updated.config.pacing).toEqual({ type: 'fixed_delay', delay_ms: 750 })

    act(() => delayInput.dispatchEvent(new FocusEvent('focusout', { bubbles: true })))
    expect(delayInput.value).toBe('0.75')
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
