// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import CreateTestRuntimeOverrides from './CreateTestRuntimeOverrides'
import { ScenarioDraft } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('CreateTestRuntimeOverrides', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('emits richer runtime override changes and reveals conditional fields', async () => {
    const onChangeSpy = vi.fn()

    function Harness() {
      const [runtimeSettings, setRuntimeSettings] = useState<ScenarioDraft['runtimeSettings']>({
        iterations: undefined,
        pacing: undefined,
        think_time: undefined,
        browser: undefined,
        log_level: '',
        auto_transactions: '',
        continue_on_error: false,
        snapshot_on_error: false,
      })

      return (
        <CreateTestRuntimeOverrides
          runtimeSettings={runtimeSettings}
          onRuntimeSettingChange={(field, value) => {
            onChangeSpy(field, value)
            setRuntimeSettings(prev => ({ ...prev, [field]: value }))
          }}
        />
      )
    }

    await act(async () => {
      root.render(<Harness />)
    })

    expect(container.textContent).toContain('Pacing')
    expect(container.textContent).toContain('Think Time Override (ms)')
    expect(container.textContent).toContain('Browser Device Profile')
    expect(container.textContent).toContain('Browser Headless')
    expect(container.textContent).toContain('Touch Mode')
    expect(container.textContent).toContain('Continue on error')

    const selects = Array.from(container.querySelectorAll('select'))
    const pacingSelect = selects[0]
    const deviceProfileSelect = selects.find(
      select => select.parentElement?.textContent?.includes('Browser Device Profile'),
    ) as HTMLSelectElement | undefined
    const orientationSelect = selects.find(
      select => select.parentElement?.textContent?.includes('Orientation'),
    ) as HTMLSelectElement | undefined
    const headlessSelect = selects.find(
      select => select.parentElement?.textContent?.includes('Browser Headless'),
    ) as HTMLSelectElement | undefined
    const touchModeSelect = selects.find(
      select => select.parentElement?.textContent?.includes('Touch Mode'),
    ) as HTMLSelectElement | undefined
    const thinkTimeInput = Array.from(container.querySelectorAll('input')).find(
      input => input.getAttribute('placeholder') === 'Script default'
        && input.getAttribute('type') === 'number'
        && input.parentElement?.textContent?.includes('Think Time Override'),
    ) as HTMLInputElement | undefined
    const continueOnErrorCheckbox = Array.from(container.querySelectorAll('input')).find(
      input => input.getAttribute('type') === 'checkbox' && input.parentElement?.textContent?.includes('Continue on error'),
    ) as HTMLInputElement | undefined
    const snapshotOnErrorCheckbox = Array.from(container.querySelectorAll('input')).find(
      input => input.getAttribute('type') === 'checkbox' && input.parentElement?.textContent?.includes('Snapshot on error'),
    ) as HTMLInputElement | undefined
    const ignoreThinkTimeCheckbox = Array.from(container.querySelectorAll('input')).find(
      input => input.getAttribute('type') === 'checkbox' && input.parentElement?.textContent?.includes('Ignore think time'),
    ) as HTMLInputElement | undefined

    expect(pacingSelect).toBeDefined()
    expect(deviceProfileSelect).toBeDefined()
    expect(orientationSelect).toBeDefined()
    expect(headlessSelect).toBeDefined()
    expect(touchModeSelect).toBeDefined()
    expect(thinkTimeInput).toBeDefined()
    expect(continueOnErrorCheckbox).toBeDefined()
    expect(snapshotOnErrorCheckbox).toBeDefined()
    expect(ignoreThinkTimeCheckbox).toBeDefined()

    await act(async () => {
      pacingSelect.value = 'fixed_delay'
      pacingSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(onChangeSpy).toHaveBeenCalledWith('pacing', { type: 'fixed_delay', delay_ms: 0 })
    expect(container.textContent).toContain('Pacing delay (seconds)')

    const delayInput = Array.from(container.querySelectorAll('input')).find(
      input => input.getAttribute('placeholder') === '0' && input.parentElement?.textContent?.includes('Pacing delay'),
    ) as HTMLInputElement | undefined

    expect(delayInput).toBeDefined()

    const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
    await act(async () => delayInput?.dispatchEvent(new FocusEvent('focusin', { bubbles: true })))
    await act(async () => {
      if (!delayInput) return
      delayInput.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: 'Backspace' }))
      valueSetter?.call(delayInput, '')
      delayInput.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'deleteContentBackward' }))
      delayInput.dispatchEvent(new KeyboardEvent('keyup', { bubbles: true, key: 'Backspace' }))
    })
    expect(delayInput?.value).toBe('')

    await act(async () => {
      if (!delayInput) return
      delayInput.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: '1' }))
      valueSetter?.call(delayInput, '1')
      delayInput.dispatchEvent(new InputEvent('input', { bubbles: true, data: '1', inputType: 'insertText' }))
      delayInput.dispatchEvent(new KeyboardEvent('keyup', { bubbles: true, key: '1' }))
    })
    expect(delayInput?.value).toBe('1')

    await act(async () => {
      if (!delayInput) return
      delayInput.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: '.' }))
      delayInput.dispatchEvent(new KeyboardEvent('keyup', { bubbles: true, key: '.' }))
      delayInput.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: '2' }))
      valueSetter?.call(delayInput, '1.2')
      delayInput.dispatchEvent(new InputEvent('input', { bubbles: true, data: '2', inputType: 'insertText' }))
      delayInput.dispatchEvent(new KeyboardEvent('keyup', { bubbles: true, key: '2' }))
      delayInput.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: '3' }))
      valueSetter?.call(delayInput, '1.23')
      delayInput.dispatchEvent(new InputEvent('input', { bubbles: true, data: '3', inputType: 'insertText' }))
      delayInput.dispatchEvent(new KeyboardEvent('keyup', { bubbles: true, key: '3' }))
      delayInput.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: '4' }))
      valueSetter?.call(delayInput, '1.234')
      delayInput.dispatchEvent(new InputEvent('input', { bubbles: true, data: '4', inputType: 'insertText' }))
      delayInput.dispatchEvent(new KeyboardEvent('keyup', { bubbles: true, key: '4' }))
    })

    expect(onChangeSpy).toHaveBeenCalledWith('pacing', { type: 'fixed_delay', delay_ms: 1234 })
    expect(delayInput?.value).toBe('1.234')

    await act(async () => delayInput?.dispatchEvent(new FocusEvent('focusout', { bubbles: true })))
    expect(delayInput?.value).toBe('1.234')

    await act(async () => {
      if (thinkTimeInput) {
        const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
        valueSetter?.call(thinkTimeInput, '500')
        thinkTimeInput.dispatchEvent(new Event('input', { bubbles: true }))
        thinkTimeInput.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })

    expect(onChangeSpy).toHaveBeenCalledWith('think_time', { type: 'fixed', mean_ms: 500, ignore: false })

    await act(async () => {
      if (ignoreThinkTimeCheckbox) {
        ignoreThinkTimeCheckbox.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      }
    })

    expect(onChangeSpy).toHaveBeenCalledWith('think_time', { type: 'fixed', mean_ms: 500, ignore: true })

    await act(async () => {
      if (ignoreThinkTimeCheckbox) {
        ignoreThinkTimeCheckbox.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      }
    })

    // This draft has an explicit 500 ms override, so disabling ignore keeps it.
    expect(onChangeSpy).toHaveBeenCalledWith('think_time', { type: 'fixed', mean_ms: 500, ignore: false })

    await act(async () => {
      if (deviceProfileSelect) {
        deviceProfileSelect.value = 'iphone_se'
        deviceProfileSelect.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })

    expect(onChangeSpy).toHaveBeenCalledWith('browser', { device_profile: 'iphone_se' })

    await act(async () => {
      if (orientationSelect) {
        orientationSelect.value = 'portrait'
        orientationSelect.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })

    expect(onChangeSpy).toHaveBeenCalledWith('browser', { device_profile: 'iphone_se', orientation: 'portrait' })

    await act(async () => {
      if (headlessSelect) {
        headlessSelect.value = 'false'
        headlessSelect.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })

    expect(onChangeSpy).toHaveBeenCalledWith('browser', {
      device_profile: 'iphone_se',
      orientation: 'portrait',
      headless: false,
    })

    await act(async () => {
      if (touchModeSelect) {
        touchModeSelect.value = 'true'
        touchModeSelect.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })

    expect(onChangeSpy).toHaveBeenCalledWith('browser', {
      device_profile: 'iphone_se',
      orientation: 'portrait',
      headless: false,
      touch_mode: true,
    })

    await act(async () => {
      if (continueOnErrorCheckbox) {
        continueOnErrorCheckbox.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      }
    })

    expect(onChangeSpy).toHaveBeenCalledWith('continue_on_error', true)

    await act(async () => {
      if (snapshotOnErrorCheckbox) {
        snapshotOnErrorCheckbox.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      }
    })

    expect(onChangeSpy).toHaveBeenCalledWith('snapshot_on_error', true)
  })

  it('returns to the script default after an ignore-only override is toggled off', async () => {
    const onChangeSpy = vi.fn()
    function Harness() {
      const [runtimeSettings, setRuntimeSettings] = useState<ScenarioDraft['runtimeSettings']>({
        think_time: undefined,
        log_level: '',
        auto_transactions: '',
        continue_on_error: false,
        snapshot_on_error: false,
      })
      return (
        <CreateTestRuntimeOverrides
          runtimeSettings={runtimeSettings}
          onRuntimeSettingChange={(field, value) => {
            onChangeSpy(field, value)
            setRuntimeSettings(prev => ({ ...prev, [field]: value }))
          }}
        />
      )
    }

    await act(async () => root.render(<Harness />))
    const ignore = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
      .find(input => input.parentElement?.textContent?.includes('Ignore think time'))!

    await act(async () => ignore.click())
    expect(onChangeSpy).toHaveBeenLastCalledWith('think_time', {
      type: 'fixed',
      mean_ms: 0,
      ignore: true,
    })

    await act(async () => ignore.click())
    expect(onChangeSpy).toHaveBeenLastCalledWith('think_time', undefined)
  })

  it('preserves recorded think-time settings while editing ignore, multiplier, and cap', async () => {
    const onChangeSpy = vi.fn()
    function Harness() {
      const [runtimeSettings, setRuntimeSettings] = useState<ScenarioDraft['runtimeSettings']>({
        think_time: { type: 'recorded', mean_ms: 0, multiplier: 2, limit_ms: 5000, ignore: false },
        log_level: '',
        auto_transactions: '',
        continue_on_error: false,
        snapshot_on_error: false,
      })
      return (
        <CreateTestRuntimeOverrides
          runtimeSettings={runtimeSettings}
          onRuntimeSettingChange={(field, value) => {
            onChangeSpy(field, value)
            setRuntimeSettings(prev => ({ ...prev, [field]: value }))
          }}
        />
      )
    }
    await act(async () => root.render(<Harness />))

    expect(container.textContent).not.toContain('Think Time Override (ms)')
    const ignore = Array.from(container.querySelectorAll<HTMLInputElement>('input')).find(
      input => input.type === 'checkbox' && input.parentElement?.textContent?.includes('Ignore think time'),
    )
    await act(async () => ignore?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(onChangeSpy).toHaveBeenCalledWith('think_time', {
      type: 'recorded', multiplier: 2, limit_ms: 5000, ignore: true,
    })

    const multiplier = Array.from(container.querySelectorAll<HTMLInputElement>('input')).find(
      input => input.parentElement?.textContent?.includes('Think time multiplier'),
    )
    expect(multiplier?.value).toBe('2')
    const cap = Array.from(container.querySelectorAll<HTMLInputElement>('input')).find(
      input => input.parentElement?.textContent?.includes('Think time cap'),
    )
    expect(cap?.value).toBe('5000')
  })

  it('edits uniform bounds without emitting an unused mean and round-trips gaussian fields', async () => {
    const onChangeSpy = vi.fn()
    function Harness() {
      const [runtimeSettings, setRuntimeSettings] = useState<ScenarioDraft['runtimeSettings']>({
        think_time: { type: 'fixed', mean_ms: 800 },
        log_level: '',
        auto_transactions: '',
        continue_on_error: false,
        snapshot_on_error: false,
      })
      return (
        <CreateTestRuntimeOverrides
          runtimeSettings={runtimeSettings}
          onRuntimeSettingChange={(field, value) => {
            onChangeSpy(field, value)
            setRuntimeSettings(prev => ({ ...prev, [field]: value }))
          }}
        />
      )
    }
    await act(async () => root.render(<Harness />))

    const field = (labelText: string) => {
      const label = Array.from(container.querySelectorAll('label')).find(
        candidate => candidate.querySelector('span')?.textContent === labelText,
      )
      return label?.querySelector('input, select') as HTMLInputElement | HTMLSelectElement | null
    }
    const setValue = async (element: HTMLInputElement | HTMLSelectElement | null, value: string) => {
      expect(element).not.toBeNull()
      await act(async () => {
        const prototype = element instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype
        Object.getOwnPropertyDescriptor(prototype, 'value')?.set?.call(element, value)
        element?.dispatchEvent(new Event('change', { bubbles: true }))
      })
    }

    await setValue(field('Think Time'), 'uniform')
    expect(onChangeSpy).toHaveBeenLastCalledWith('think_time', {
      type: 'uniform',
      min_ms: 800,
      max_ms: 800,
    })
    expect(container.textContent).not.toContain('Mean Think Time Override (ms)')
    expect((field('Minimum Think Time (ms)') as HTMLInputElement)?.value).toBe('800')

    await setValue(field('Minimum Think Time (ms)'), '250')
    expect(onChangeSpy).toHaveBeenLastCalledWith('think_time', {
      type: 'uniform',
      min_ms: 250,
      max_ms: 800,
    })
    await setValue(field('Maximum Think Time (ms)'), '1750')
    expect(onChangeSpy).toHaveBeenLastCalledWith('think_time', {
      type: 'uniform',
      min_ms: 250,
      max_ms: 1750,
    })
    expect(onChangeSpy.mock.calls[onChangeSpy.mock.calls.length - 1]?.[1]).not.toHaveProperty('mean_ms')

    await setValue(field('Think Time'), 'gaussian')
    expect(onChangeSpy).toHaveBeenLastCalledWith('think_time', {
      type: 'gaussian',
      mean_ms: 1000,
      min_ms: 250,
      max_ms: 1750,
    })
    await setValue(field('Think Time Std Dev (ms)'), '200')
    expect(onChangeSpy).toHaveBeenLastCalledWith('think_time', {
      type: 'gaussian',
      mean_ms: 1000,
      std_ms: 200,
      min_ms: 250,
      max_ms: 1750,
    })
  })

  it('reveals the IP-spoofing source pool editor and emits web_runtime.source_ips', async () => {
    const onChangeSpy = vi.fn()

    function Harness() {
      const [runtimeSettings, setRuntimeSettings] = useState<ScenarioDraft['runtimeSettings']>({
        log_level: '',
        auto_transactions: '',
        continue_on_error: false,
        snapshot_on_error: false,
      })

      return (
        <CreateTestRuntimeOverrides
          runtimeSettings={runtimeSettings}
          onRuntimeSettingChange={(field, value) => {
            onChangeSpy(field, value)
            setRuntimeSettings(prev => ({ ...prev, [field]: value }))
          }}
        />
      )
    }

    await act(async () => {
      root.render(<Harness />)
    })

    expect(container.textContent).toContain('Enable IP spoofing')

    const enableCheckbox = Array.from(container.querySelectorAll('input')).find(
      input => input.getAttribute('type') === 'checkbox' && input.parentElement?.textContent?.includes('Enable IP spoofing'),
    ) as HTMLInputElement | undefined
    expect(enableCheckbox).toBeDefined()

    // The row editor only appears once spoofing is enabled.
    expect(container.querySelector('input[aria-label="Source IP 1"]')).toBeNull()

    await act(async () => {
      enableCheckbox?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const ipInput = container.querySelector('input[aria-label="Source IP 1"]') as HTMLInputElement | null
    expect(ipInput).not.toBeNull()
    expect(container.textContent).toContain('HTTP/HTTPS traffic only')

    await act(async () => {
      if (ipInput) {
        const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
        valueSetter?.call(ipInput, '10.0.0.1')
        ipInput.dispatchEvent(new Event('input', { bubbles: true }))
        ipInput.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })

    expect(onChangeSpy).toHaveBeenCalledWith('web_runtime', { source_ips: ['10.0.0.1'] })

    // An obviously malformed address surfaces an inline hint (backend stays authoritative).
    await act(async () => {
      if (ipInput) {
        const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
        valueSetter?.call(ipInput, '10.0.0.300')
        ipInput.dispatchEvent(new Event('input', { bubbles: true }))
        ipInput.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })

    expect(container.textContent).toContain('Enter valid IPv4 or IPv6 addresses')

    // Disabling clears the pool from the draft.
    await act(async () => {
      enableCheckbox?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onChangeSpy).toHaveBeenLastCalledWith('web_runtime', undefined)
  })
})
