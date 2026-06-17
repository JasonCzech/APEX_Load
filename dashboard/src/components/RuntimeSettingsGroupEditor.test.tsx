// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import RuntimeSettingsGroupEditor, { type RuntimeEditorGroup } from './RuntimeSettingsGroupEditor'
import { type ScenarioRuntimeSettingsDraft } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const GROUPS: RuntimeEditorGroup[] = [
  { name: 'Checkout', scriptId: 's1', protocol: 'http' },
  { name: 'Experience', scriptId: 's2', protocol: 'browser' },
]

const GROUP0_OVERRIDE: ScenarioRuntimeSettingsDraft = {
  pacing: { type: 'fixed_delay', delay_ms: 250 },
  log_level: 'extended',
  auto_transactions: '',
  continue_on_error: false,
  snapshot_on_error: false,
}

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

function findButton(text: string): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll('button')).find(b => (b.textContent || '').trim() === text) as HTMLButtonElement | undefined
}

function setSelectValue(select: HTMLSelectElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')!.set!
  setter.call(select, value)
  select.dispatchEvent(new Event('change', { bubbles: true }))
}

describe('RuntimeSettingsGroupEditor', () => {
  it('shows the effective value, source labels, and protocol applicability for the focused group', () => {
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={0}
          onFocusChange={() => {}}
          scenarioDefault={{ log_level: 'standard' }}
          overrides={[GROUP0_OVERRIDE, undefined]}
          onChange={() => {}}
          onApplyToGroups={() => {}}
        />,
      )
    })
    const text = container.textContent || ''
    expect(text).toContain('Per-group runtime overrides')

    const badges = Array.from(container.querySelectorAll('.runtime-source-badge')).map(b => (b.textContent || '').trim())
    // pacing + log_level are overridden on group 0; browser is not applicable for http.
    expect(badges.filter(b => b === 'Group override').length).toBe(2)
    expect(text).toContain('Not applicable for http protocol')
    // The overridden values are surfaced.
    expect(text).toContain('Fixed delay · 250ms')
    expect(text).toContain('extended')
  })

  it('renders the browser editor and a Group override for a browser-protocol group', () => {
    const browserOverride: ScenarioRuntimeSettingsDraft = {
      browser: { device_profile: 'pixel_8' },
      log_level: '',
      auto_transactions: '',
      continue_on_error: false,
      snapshot_on_error: false,
    }
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={1}
          onFocusChange={() => {}}
          overrides={[undefined, browserOverride]}
          onChange={() => {}}
          onApplyToGroups={() => {}}
        />,
      )
    })
    // The browser device select is present for the browser group.
    const deviceSelect = Array.from(container.querySelectorAll('select')).find(s =>
      Array.from(s.options).some(o => o.value === 'pixel_8'),
    )
    expect(deviceSelect).toBeTruthy()
    expect((container.textContent || '')).not.toContain('Not applicable')
  })

  it('clears a group override field via the Clear control', () => {
    const onChange = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={0}
          onFocusChange={() => {}}
          overrides={[GROUP0_OVERRIDE, undefined]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    const clear = findButton('Clear')
    expect(clear).toBeTruthy()
    act(() => clear!.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(onChange).toHaveBeenCalledWith(0, expect.anything())
  })

  it('applies the focused group profile to selected target groups', () => {
    const onApply = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={0}
          onFocusChange={() => {}}
          overrides={[GROUP0_OVERRIDE, undefined]}
          onChange={() => {}}
          onApplyToGroups={onApply}
        />,
      )
    })
    act(() => findButton('Apply to groups…')!.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    const targetCheckbox = container.querySelector('.runtime-apply-target input[type="checkbox"]') as HTMLInputElement
    expect(targetCheckbox).toBeTruthy()
    act(() => targetCheckbox.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    act(() => findButton('Apply')!.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(onApply).toHaveBeenCalledWith(0, [1])
  })

  it('notifies on focus-group change', () => {
    const onFocus = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={0}
          onFocusChange={onFocus}
          overrides={[undefined, undefined]}
          onChange={() => {}}
          onApplyToGroups={() => {}}
        />,
      )
    })
    const select = container.querySelector('select[aria-label="Focus group"]') as HTMLSelectElement
    act(() => setSelectValue(select, '1'))
    expect(onFocus).toHaveBeenCalledWith(1)
  })
})
