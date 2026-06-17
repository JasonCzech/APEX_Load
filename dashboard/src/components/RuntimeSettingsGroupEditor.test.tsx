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
    // Browser device is editable for a browser group; the HTTP transport section is
    // gated off by the applicability matrix.
    expect(container.textContent || '').toContain('Not applicable for browser protocol')
  })

  it('shows matrix-gated rows for the advanced sections (http on http, browser_config on browser)', () => {
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={0}
          onFocusChange={() => {}}
          overrides={[{ http: { version: '2' }, log: { level: 'extended' }, log_level: '', auto_transactions: '', continue_on_error: false, snapshot_on_error: false }, undefined]}
          onChange={() => {}}
          onApplyToGroups={() => {}}
        />,
      )
    })
    const text = container.textContent || ''
    // Group 0 is http: http applies (shows the override), browser_config does not.
    expect(text).toContain('HTTP transport')
    expect(text).toContain('HTTP/2')
    expect(text).toContain('Logging')
    expect(text).toContain('Not applicable for http protocol') // Browser config row
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

  it('renders the full error_handling retry controls', () => {
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={0}
          onFocusChange={() => {}}
          overrides={[undefined, undefined]}
          onChange={() => {}}
          onApplyToGroups={() => {}}
        />,
      )
    })
    const text = container.textContent || ''
    expect(text).toContain('Max retries')
    expect(text).toContain('Retry delay (ms)')
    // retry_backoff select offers the backoff strategies.
    const backoff = Array.from(container.querySelectorAll('select')).find(s =>
      Array.from(s.options).some(o => o.value === 'exponential'),
    )
    expect(backoff).toBeTruthy()
  })

  it('renders and edits the HTTP transport form for an http group', () => {
    const onChange = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={0}
          onFocusChange={() => {}}
          overrides={[undefined, undefined]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    const text = container.textContent || ''
    expect(text).toContain('HTTP transport')
    expect(text).toContain('Auth password')
    // The HTTP version select offers both 1.1 and 2 (distinguishes it from TLS).
    const versionSelect = Array.from(container.querySelectorAll('select')).find(s =>
      Array.from(s.options).some(o => o.value === '2') && Array.from(s.options).some(o => o.value === '1.1'),
    ) as HTMLSelectElement
    expect(versionSelect).toBeTruthy()
    act(() => setSelectValue(versionSelect, '2'))
    expect(onChange).toHaveBeenCalledWith(0, expect.objectContaining({ http: expect.objectContaining({ version: '2' }) }))
  })

  it('gates the HTTP transport edit form off for a non-http (browser) group', () => {
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor groups={GROUPS} focusedIndex={1} onFocusChange={() => {}} overrides={[undefined, undefined]} onChange={() => {}} onApplyToGroups={() => {}} />,
      )
    })
    // The HTTP edit subpanel (h4) does not render for a browser group; it shows as a
    // "Not applicable" summary row instead.
    const headings = Array.from(container.querySelectorAll('h4')).map(h => h.textContent)
    expect(headings).not.toContain('HTTP transport')
  })

  it('renders and edits the Web runtime source IPs', () => {
    const onChange = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor groups={GROUPS} focusedIndex={0} onFocusChange={() => {}} overrides={[undefined, undefined]} onChange={onChange} onApplyToGroups={() => {}} />,
      )
    })
    expect(container.textContent || '').toContain('Web runtime')
    const sourceInput = Array.from(container.querySelectorAll('input[type="text"]')).find(
      input => (input as HTMLInputElement).placeholder.includes('10.0.0.1'),
    ) as HTMLInputElement
    expect(sourceInput).toBeTruthy()
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    setter.call(sourceInput, '10.0.0.1, 10.0.0.2')
    act(() => sourceInput.dispatchEvent(new Event('input', { bubbles: true })))
    expect(onChange).toHaveBeenCalledWith(0, expect.objectContaining({ web_runtime: expect.objectContaining({ source_ips: ['10.0.0.1', '10.0.0.2'] }) }))
  })
})
