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
    expect(container.textContent).toContain('Pacing delay (ms)')

    const delayInput = Array.from(container.querySelectorAll('input')).find(
      input => input.getAttribute('placeholder') === '0' && input.parentElement?.textContent?.includes('Pacing delay'),
    ) as HTMLInputElement | undefined

    expect(delayInput).toBeDefined()

    await act(async () => {
      if (delayInput) {
        const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
        valueSetter?.call(delayInput, '1234')
        delayInput.dispatchEvent(new Event('input', { bubbles: true }))
        delayInput.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })

    expect(onChangeSpy).toHaveBeenCalledWith('pacing', { type: 'fixed_delay', delay_ms: 1234 })

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
})