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

function setInputValue(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
  setter.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

function findLabeledControl<T extends HTMLElement>(labelText: string, selector: string): T {
  const label = Array.from(container.querySelectorAll('label')).find(candidate =>
    Array.from(candidate.querySelectorAll('span')).some(span => (span.textContent || '').trim() === labelText),
  )
  const control = label?.querySelector(selector) as T | null
  if (!control) throw new Error(`control not found for label: ${labelText}`)
  return control
}

/** The runtime editor now organizes its forms into tabs; fields live behind their tab. */
function tabLabels(): string[] {
  return Array.from(container.querySelectorAll('[role="tab"]')).map(b => (b.textContent || '').trim())
}

function clickTab(label: string) {
  const tab = Array.from(container.querySelectorAll('[role="tab"]')).find(b => (b.textContent || '').trim() === label) as HTMLButtonElement | undefined
  if (!tab) throw new Error(`runtime tab not found: ${label} (have: ${tabLabels().join(', ')})`)
  act(() => tab.dispatchEvent(new MouseEvent('click', { bubbles: true })))
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

    // The Summary tab is active by default and shows the effective-value table.
    const badges = Array.from(container.querySelectorAll('.runtime-source-badge')).map(b => (b.textContent || '').trim())
    // pacing + log_level are overridden on group 0; browser is not applicable for http.
    expect(badges.filter(b => b === 'Group override').length).toBe(2)
    expect(text).toContain('Not applicable for http protocol')
    // The overridden values are surfaced.
    expect(text).toContain('Fixed delay · 250ms')
    expect(text).toContain('extended')
  })

  it('shows the actual script-config value as the effective value when nothing is overridden', () => {
    const groups: RuntimeEditorGroup[] = [
      {
        name: 'Checkout',
        scriptId: 's1',
        protocol: 'http',
        scriptConfig: {
          base_url: 'https://example.test',
          pacing: { type: 'fixed_delay', delay_ms: 500 },
          think_time: { type: 'fixed', mean_ms: 750 },
          run_logic: { iterations: 9 },
        },
      },
    ]
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={groups}
          focusedIndex={0}
          onFocusChange={() => {}}
          overrides={[undefined]}
          onChange={() => {}}
          onApplyToGroups={() => {}}
        />,
      )
    })
    const text = container.textContent || ''
    // The real script values are surfaced in the Effective value column...
    expect(text).toContain('Fixed delay · 500ms')
    expect(text).toContain('fixed · 750ms')
    expect(text).toContain('9')
    // ...attributed to the Script default source.
    const badges = Array.from(container.querySelectorAll('.runtime-source-badge')).map(b => (b.textContent || '').trim())
    expect(badges).toContain('Script default')
    // Fields no layer sets show a descriptive engine-default value — never a blank '—'
    // placeholder, and never the redundant literal "Script default" text in the cell.
    const valueCells = Array.from(container.querySelectorAll('.runtime-override-row td:nth-child(2)')).map(td => (td.textContent || '').trim())
    expect(valueCells).not.toContain('Script default')
    expect(valueCells).not.toContain('—')
    expect(valueCells).toContain('Stop on error') // error_handling engine-default descriptive label
  })

  it('shows HTTP Transport + Web/HTTP tabs for http and browser groups, hidden for other protocols', () => {
    const groups: RuntimeEditorGroup[] = [
      { name: 'Api', scriptId: 's1', protocol: 'http' },
      { name: 'Experience', scriptId: 's2', protocol: 'browser' },
      { name: 'Stream', scriptId: 's3', protocol: 'grpc' },
    ]
    const render = (focusedIndex: number) =>
      act(() => {
        root.render(
          <RuntimeSettingsGroupEditor groups={groups} focusedIndex={focusedIndex} onFocusChange={() => {}} overrides={[undefined, undefined, undefined]} onChange={() => {}} onApplyToGroups={() => {}} />,
        )
      })

    // Universal tabs always present.
    render(0) // http
    expect(tabLabels()).toEqual(['Summary', 'Think Time', 'Iteration Pacing', 'Run Logic', 'Error Handling', 'HTTP Transport', 'Web/HTTP'])

    render(1) // browser — HTTP Transport (with browser emulation) + Web/HTTP still show
    expect(tabLabels()).toContain('HTTP Transport')
    expect(tabLabels()).toContain('Web/HTTP')

    render(2) // grpc — protocol-specific tabs hidden
    expect(tabLabels()).not.toContain('HTTP Transport')
    expect(tabLabels()).not.toContain('Web/HTTP')
    expect(tabLabels()).toEqual(['Summary', 'Think Time', 'Iteration Pacing', 'Run Logic', 'Error Handling'])
  })

  it('renders the browser device editor under HTTP Transport → Browser emulation for a browser group', () => {
    const browserOverride: ScenarioRuntimeSettingsDraft = {
      browser: { device_profile: 'pixel_8' },
      log_level: '',
      auto_transactions: '',
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
    // HTTP Transport tab shows for the browser group and folds in Browser emulation.
    clickTab('HTTP Transport')
    expect(container.textContent || '').toContain('Browser emulation')
    clickTab('Browser emulation')
    const deviceSelect = Array.from(container.querySelectorAll('select')).find(s =>
      Array.from(s.options).some(o => o.value === 'pixel_8'),
    )
    expect(deviceSelect).toBeTruthy()
  })

  it('shows matrix-gated rows in the Summary table (http applies, browser_config does not, for an http group)', () => {
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={0}
          onFocusChange={() => {}}
          overrides={[{ http: { version: '2' }, log: { level: 'extended' }, log_level: '', auto_transactions: '' }, undefined]}
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

  it('renders the full error_handling retry controls under the Error Handling tab', () => {
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
    clickTab('Error Handling')
    const text = container.textContent || ''
    expect(text).toContain('Log level')
    expect(text).toContain('Max retries')
    expect(text).toContain('Retry delay (ms)')
    const logLevel = Array.from(container.querySelectorAll('select')).find(s =>
      Array.from(s.options).some(o => o.value === 'extended') && Array.from(s.options).some(o => o.value === 'disabled'),
    )
    expect(logLevel).toBeTruthy()
    // retry_backoff select offers the backoff strategies.
    const backoff = Array.from(container.querySelectorAll('select')).find(s =>
      Array.from(s.options).some(o => o.value === 'exponential'),
    )
    expect(backoff).toBeTruthy()
    expect(Array.from(backoff?.options || []).map(option => option.value)).toEqual(['', 'fixed', 'exponential'])
    act(() => setSelectValue(backoff!, 'fixed'))
    expect(onChange).toHaveBeenCalledWith(0, expect.objectContaining({ retry_backoff: 'fixed' }))
  })

  it('renders and edits the HTTP transport form under the HTTP Transport tab', () => {
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
    clickTab('HTTP Transport')
    // HTTP version lives under the Connection sub-tab.
    clickTab('Connection')
    const versionSelect = Array.from(container.querySelectorAll('select')).find(s =>
      Array.from(s.options).some(o => o.value === '2') && Array.from(s.options).some(o => o.value === '1.1'),
    ) as HTMLSelectElement
    expect(versionSelect).toBeTruthy()
    act(() => setSelectValue(versionSelect, '2'))
    expect(onChange).toHaveBeenCalledWith(0, expect.objectContaining({ http: expect.objectContaining({ version: '2' }) }))
    // Auth fields live under the Authentication sub-tab.
    clickTab('Authentication')
    expect(container.textContent || '').toContain('Auth password')
  })

  it('renders and edits the Web runtime source IPs under the Web/HTTP tab', () => {
    const onChange = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor groups={GROUPS} focusedIndex={0} onFocusChange={() => {}} overrides={[undefined, undefined]} onChange={onChange} onApplyToGroups={() => {}} />,
      )
    })
    clickTab('Web/HTTP')
    const sourceInput = Array.from(container.querySelectorAll('input[type="text"]')).find(
      input => (input as HTMLInputElement).placeholder.includes('10.0.0.1'),
    ) as HTMLInputElement
    expect(sourceInput).toBeTruthy()
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    setter.call(sourceInput, '10.0.0.1, 10.0.0.2')
    act(() => sourceInput.dispatchEvent(new Event('input', { bubbles: true })))
    expect(onChange).toHaveBeenCalledWith(0, expect.objectContaining({ web_runtime: expect.objectContaining({ source_ips: ['10.0.0.1', '10.0.0.2'] }) }))
  })

  it('promotes the legacy log level into the log section on edit (Error Handling tab)', () => {
    const onChange = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={0}
          onFocusChange={() => {}}
          overrides={[{ log_level: 'extended', auto_transactions: '' }, undefined]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('Error Handling')
    const logSelect = Array.from(container.querySelectorAll('select')).find(s =>
      Array.from(s.options).some(o => o.value === 'extended') && Array.from(s.options).some(o => o.value === 'disabled'),
    ) as HTMLSelectElement
    expect(logSelect).toBeTruthy()
    expect(logSelect.value).toBe('extended') // the legacy value is bridged into the control
    act(() => setSelectValue(logSelect, 'standard'))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1][1]
    expect(last.log).toEqual({ level: 'standard' })
    expect(last.log_level).toBe('') // legacy cleared so the section is authoritative
  })

  it('Summary Clear removes a section-backed iterations override (not just the legacy field)', () => {
    const onChange = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={0}
          onFocusChange={() => {}}
          // Iterations override lives in the promoted run_logic section, as the
          // normal edit path writes it (not the legacy top-level field). A pacing
          // override is included so the draft is not emptied by the clear (which
          // would emit undefined) — letting us assert iterations specifically.
          overrides={[{ run_logic: { iterations: 9 }, pacing: { type: 'fixed_delay', delay_ms: 250 }, log_level: '', auto_transactions: '' }, undefined]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    const clearIterations = container.querySelector('button[aria-label="Clear Iterations override"]') as HTMLButtonElement
    expect(clearIterations).toBeTruthy()
    act(() => clearIterations.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1][1]
    expect(last).toBeTruthy() // pacing survives, so a non-empty override is emitted
    expect(last.iterations).toBeUndefined()
    // The section copy must be gone too; run_logic had no other field, so it drops.
    expect(last.run_logic?.iterations).toBeUndefined()
    expect(last.run_logic?.global_iterations).toBeUndefined()
    expect(last.pacing).toBeTruthy() // unrelated override preserved
  })

  it('Clear Iterations materializes the inherited value when run_logic keeps siblings', () => {
    const onChange = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={0}
          onFocusChange={() => {}}
          scenarioDefault={{ run_logic: { iterations: 7 } }}
          overrides={[{ run_logic: { iterations: 9, workload_model: 'open' }, log_level: '', auto_transactions: '' }, undefined]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    const clear = container.querySelector('button[aria-label="Clear Iterations override"]') as HTMLButtonElement
    act(() => clear.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1][1]
    expect(last.iterations).toBeUndefined()
    expect(last.run_logic).toEqual({ iterations: 7, workload_model: 'open' })
  })

  it('Clear Auto-transactions materializes the inherited value when run_logic keeps siblings', () => {
    const onChange = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={0}
          onFocusChange={() => {}}
          scenarioDefault={{ run_logic: { auto_transactions: 'per_action' } }}
          overrides={[{ run_logic: { auto_transactions: 'none', workload_model: 'open' }, log_level: '', auto_transactions: '' }, undefined]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    const clear = container.querySelector('button[aria-label="Clear Auto-transactions override"]') as HTMLButtonElement
    act(() => clear.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1][1]
    expect(last.auto_transactions).toBe('')
    expect(last.run_logic).toEqual({ auto_transactions: 'per_action', workload_model: 'open' })
  })

  it('Clear Log level materializes the inherited value when log keeps siblings', () => {
    const onChange = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={0}
          onFocusChange={() => {}}
          scenarioDefault={{ log: { level: 'standard' } }}
          overrides={[{ log: { level: 'extended', on_error_only: true }, log_level: '', auto_transactions: '' }, undefined]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    const clear = container.querySelector('button[aria-label="Clear Log level override"]') as HTMLButtonElement
    act(() => clear.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1][1]
    expect(last.log_level).toBe('')
    expect(last.log).toEqual({ level: 'standard', on_error_only: true })
  })

  it('editing the think-time distribution on an inheriting group keeps the inherited mean (not 0)', () => {
    const onChange = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={0}
          onFocusChange={() => {}}
          scenarioDefault={{ think_time: { type: 'fixed', mean_ms: 800 } }}
          overrides={[undefined, undefined]} // group inherits think time
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('Think Time')
    const distSelect = Array.from(container.querySelectorAll('select')).find(s =>
      Array.from(s.options).some(o => o.value === 'gaussian') && Array.from(s.options).some(o => o.value === 'uniform'),
    ) as HTMLSelectElement
    expect(distSelect).toBeTruthy()
    act(() => setSelectValue(distSelect, 'gaussian'))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1][1]
    expect(last.think_time.type).toBe('gaussian')
    // Must carry the inherited mean rather than silently forcing it to 0.
    expect(last.think_time.mean_ms).toBe(800)
  })

  it('folds the legacy iterations into the run_logic section when editing workload model (Run Logic tab)', () => {
    const onChange = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={0}
          onFocusChange={() => {}}
          overrides={[{ iterations: 5, log_level: '', auto_transactions: '' }, undefined]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('Run Logic')
    const workloadSelect = Array.from(container.querySelectorAll('select')).find(s =>
      Array.from(s.options).some(o => o.value === 'closed') && Array.from(s.options).some(o => o.value === 'open'),
    ) as HTMLSelectElement
    expect(workloadSelect).toBeTruthy()
    act(() => setSelectValue(workloadSelect, 'open'))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1][1]
    expect(last.run_logic).toEqual(expect.objectContaining({ iterations: 5, workload_model: 'open' }))
    expect(last.iterations).toBeUndefined() // legacy folded into the section
  })

  it('folds the legacy browser device into browser_config when editing an advanced field (HTTP Transport tab)', () => {
    const onChange = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={1}
          onFocusChange={() => {}}
          overrides={[undefined, { browser: { device_profile: 'pixel_8' }, log_level: '', auto_transactions: '' }]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('HTTP Transport')
    clickTab('Browser emulation')
    const engineSelect = Array.from(container.querySelectorAll('select')).find(s =>
      Array.from(s.options).some(o => o.value === 'webkit') && Array.from(s.options).some(o => o.value === 'chromium'),
    ) as HTMLSelectElement
    expect(engineSelect).toBeTruthy()
    act(() => setSelectValue(engineSelect, 'firefox'))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1][1]
    expect(last.browser_config).toEqual(expect.objectContaining({ device_profile: 'pixel_8', engine: 'firefox' }))
    expect(last.browser).toBeUndefined() // legacy browser folded into browser_config
  })

  it('falls back to the Summary tab when switching focus to a group that hides the active tab', () => {
    const groups: RuntimeEditorGroup[] = [
      { name: 'Api', scriptId: 's1', protocol: 'http' },
      { name: 'Stream', scriptId: 's2', protocol: 'grpc' },
    ]
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor groups={groups} focusedIndex={0} onFocusChange={() => {}} overrides={[undefined, undefined]} onChange={() => {}} onApplyToGroups={() => {}} />,
      )
    })
    clickTab('HTTP Transport')
    // Re-render focused on the grpc group, which has no HTTP Transport tab.
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor groups={groups} focusedIndex={1} onFocusChange={() => {}} overrides={[undefined, undefined]} onChange={() => {}} onApplyToGroups={() => {}} />,
      )
    })
    // The Summary table (effective-value rows) is shown, not a stranded HTTP Transport panel.
    expect(container.querySelector('.runtime-override-summary')).toBeTruthy()
    expect(container.textContent || '').not.toContain('Auth password')
  })

  it('exposes full Think Time distribution controls and merges on edit', () => {
    const onChange = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={0}
          onFocusChange={() => {}}
          overrides={[{ think_time: { type: 'gaussian', mean_ms: 500 }, log_level: '', auto_transactions: '' }, undefined]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('Think Time')
    // gaussian reveals std-dev; multiplier/limit are always present.
    expect(container.textContent || '').toContain('Std dev (ms)')
    expect(container.textContent || '').toContain('Multiplier')
    const distSelect = Array.from(container.querySelectorAll('select')).find(s =>
      Array.from(s.options).some(o => o.value === 'gaussian') && Array.from(s.options).some(o => o.value === 'uniform'),
    ) as HTMLSelectElement
    expect(distSelect.value).toBe('gaussian')
    // changing distribution preserves the existing mean (section merge, not replace).
    act(() => setSelectValue(distSelect, 'fixed'))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1][1]
    expect(last.think_time).toEqual(expect.objectContaining({ type: 'fixed', mean_ms: 500 }))
  })

  it('exposes random-delay pacing min/max fields', () => {
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={0}
          onFocusChange={() => {}}
          overrides={[{ pacing: { type: 'random_delay', min_delay_ms: 100 }, log_level: '', auto_transactions: '' }, undefined]}
          onChange={() => {}}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('Iteration Pacing')
    expect(container.textContent || '').toContain('Min delay (ms)')
    expect(container.textContent || '').toContain('Max delay (ms)')
    const pacingSelect = Array.from(container.querySelectorAll('select')).find(s =>
      Array.from(s.options).some(o => o.value === 'random_delay') && Array.from(s.options).some(o => o.value === 'random_interval'),
    ) as HTMLSelectElement
    expect(pacingSelect.value).toBe('random_delay')
  })

  it('edits the run_logic action sequence (comma-separated → array)', () => {
    const onChange = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor groups={GROUPS} focusedIndex={0} onFocusChange={() => {}} overrides={[undefined, undefined]} onChange={onChange} onApplyToGroups={() => {}} />,
      )
    })
    clickTab('Run Logic')
    const seqInput = Array.from(container.querySelectorAll('input[type="text"]')).find(
      input => (input as HTMLInputElement).placeholder.includes('login, browse, checkout'),
    ) as HTMLInputElement
    expect(seqInput).toBeTruthy()
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    setter.call(seqInput, 'login, browse, checkout')
    act(() => seqInput.dispatchEvent(new Event('input', { bubbles: true })))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1][1]
    expect(last.run_logic.action_sequence).toEqual(['login', 'browse', 'checkout'])
  })

  it('toggles a logging extended option into log.extended_options', () => {
    const onChange = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor groups={GROUPS} focusedIndex={0} onFocusChange={() => {}} overrides={[undefined, undefined]} onChange={onChange} onApplyToGroups={() => {}} />,
      )
    })
    clickTab('Error Handling')
    const card = Array.from(container.querySelectorAll('label')).find(l => (l.textContent || '').includes('Advanced trace'))
    expect(card).toBeTruthy()
    const checkbox = card!.querySelector('input[type="checkbox"]') as HTMLInputElement
    act(() => checkbox.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1][1]
    expect(last.log.extended_options).toContain('advanced_trace')
  })

  it('renders HTTP Transport nested sub-tabs and edits a proxy field', () => {
    const onChange = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor groups={GROUPS} focusedIndex={0} onFocusChange={() => {}} overrides={[undefined, undefined]} onChange={onChange} onApplyToGroups={() => {}} />,
      )
    })
    clickTab('HTTP Transport')
    // http (non-browser) group → 5 sub-tabs, no Browser emulation.
    const subTabs = Array.from(container.querySelectorAll('.http-transport-tab')).map(b => (b.textContent || '').trim())
    expect(subTabs).toEqual(['Timeouts', 'Connection', 'TLS / SSL', 'Proxy', 'Authentication'])
    clickTab('Proxy')
    const noProxyInput = Array.from(container.querySelectorAll('input[type="text"]')).find(
      input => (input as HTMLInputElement).placeholder.includes('localhost'),
    ) as HTMLInputElement
    expect(noProxyInput).toBeTruthy()
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    setter.call(noProxyInput, 'localhost, 10.0.0.0/8')
    act(() => noProxyInput.dispatchEvent(new Event('input', { bubbles: true })))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1][1]
    expect(last.http.proxy.no_proxy).toEqual(['localhost', '10.0.0.0/8'])
  })

  it('fills input placeholders with the inherited value, keeping secret fields generic', () => {
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={0}
          onFocusChange={() => {}}
          // HTTP is a whole-section replacement. Once the scenario supplies an HTTP
          // section, lower engine fields are no longer inherited.
          engineDefaults={{ base_url: '', http: { connect_timeout_ms: 30000, request_timeout_ms: 60000 } }}
          scenarioDefault={{ http: { request_timeout_ms: 45000 } }}
          overrides={[undefined, undefined]}
          onChange={() => {}}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('HTTP Transport') // Timeouts sub-tab is active by default
    const placeholders = (sel: string) =>
      Array.from(container.querySelectorAll(sel)).map(i => (i as HTMLInputElement).placeholder)
    // The scenario's section supplies request timeout; connect timeout no longer
    // leaks through from the replaced engine section.
    expect(placeholders('input[type="number"]')).not.toContain('30000')
    expect(placeholders('input[type="number"]')).toContain('45000')

    // Secret fields never echo an inherited value — they stay generic.
    clickTab('Authentication')
    const secretPlaceholders = placeholders('input[type="password"]')
    expect(secretPlaceholders.length).toBeGreaterThan(0)
    expect(secretPlaceholders.every(p => p === 'Inherit')).toBe(true)
  })

  it('materializes the highest-precedence pacing replacement before changing its type', () => {
    const onChange = vi.fn()
    const groups: RuntimeEditorGroup[] = [{
      name: 'Checkout',
      scriptId: 's1',
      protocol: 'http',
      scriptConfig: {
        base_url: '',
        pacing: { type: 'random_delay', min_delay_ms: 20, tps: 11 },
      },
    }]
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={groups}
          focusedIndex={0}
          onFocusChange={() => {}}
          engineDefaults={{ base_url: '', pacing: { type: 'random_delay', min_delay_ms: 10, max_delay_ms: 100, interval_ms: 500 } }}
          scenarioDefault={{ pacing: { type: 'random_delay', max_delay_ms: 70 } }}
          overrides={[undefined]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('Iteration Pacing')
    const pacing = findLabeledControl<HTMLSelectElement>('Pacing', 'select')
    act(() => setSelectValue(pacing, 'random_delay'))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1]
    expect(last.pacing).toEqual({
      type: 'random_delay',
      max_delay_ms: 70,
    })
  })

  it('materializes the highest-precedence think_time replacement and preserves false', () => {
    const onChange = vi.fn()
    const groups: RuntimeEditorGroup[] = [{
      name: 'Checkout',
      scriptId: 's1',
      protocol: 'http',
      scriptConfig: {
        base_url: '',
        think_time: { type: 'gaussian', mean_ms: 200, std_ms: 20, min_ms: 50 },
      },
    }]
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={groups}
          focusedIndex={0}
          onFocusChange={() => {}}
          engineDefaults={{ base_url: '', think_time: { type: 'fixed', mean_ms: 100, multiplier: 1.5, limit_ms: 900, ignore: true } }}
          scenarioDefault={{ think_time: { type: 'gaussian', mean_ms: 300, max_ms: 600, ignore: false } }}
          overrides={[undefined]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('Think Time')
    const distribution = findLabeledControl<HTMLSelectElement>('Distribution', 'select')
    act(() => setSelectValue(distribution, 'uniform'))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1]
    expect(last.think_time).toEqual({
      type: 'uniform',
      mean_ms: 300,
      max_ms: 600,
      ignore: false,
    })
  })

  it('materializes the highest-precedence error_handling replacement and keeps false', () => {
    const onChange = vi.fn()
    const groups: RuntimeEditorGroup[] = [{
      name: 'Checkout',
      scriptId: 's1',
      protocol: 'http',
      scriptConfig: {
        base_url: '',
        error_handling: { max_retries: 2 },
      },
    }]
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={groups}
          focusedIndex={0}
          onFocusChange={() => {}}
          engineDefaults={{
            base_url: '',
            error_handling: {
              continue_on_error: true,
              snapshot_on_error: true,
              max_retries: 1,
              retry_delay_ms: 100,
              retry_backoff: 'fixed',
            },
          }}
          scenarioDefault={{ error_handling: { continue_on_error: false, retry_delay_ms: 250 } }}
          overrides={[undefined]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('Error Handling')
    const continueOnError = Array.from(container.querySelectorAll('label')).find(label =>
      (label.textContent || '').includes('Continue on error'),
    )?.querySelector('input[type="checkbox"]') as HTMLInputElement
    expect(continueOnError.checked).toBe(false)
    act(() => setInputValue(findLabeledControl<HTMLInputElement>('Max retries', 'input'), '3'))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1]
    expect(last).toEqual(expect.objectContaining({
      continue_on_error: false,
      max_retries: 3,
      retry_delay_ms: 250,
    }))
    expect(last.snapshot_on_error).toBeUndefined()
    expect(last.retry_backoff).toBeUndefined()
  })

  it('materializes the highest-precedence log replacement when toggling one option', () => {
    const onChange = vi.fn()
    const groups: RuntimeEditorGroup[] = [{
      name: 'Checkout',
      scriptId: 's1',
      protocol: 'http',
      scriptConfig: { base_url: '', log: { level: 'extended' } },
    }]
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={groups}
          focusedIndex={0}
          onFocusChange={() => {}}
          engineDefaults={{ base_url: '', log: { level: 'standard', on_error_only: true, extended_options: ['parameter_substitution'] } }}
          scenarioDefault={{ log: { on_error_only: false } }}
          overrides={[undefined]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('Error Handling')
    const advancedTrace = Array.from(container.querySelectorAll('label')).find(label =>
      (label.textContent || '').includes('Advanced trace'),
    )?.querySelector('input[type="checkbox"]') as HTMLInputElement
    act(() => advancedTrace.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1]
    expect(last.log).toEqual({
      on_error_only: false,
      extended_options: ['advanced_trace'],
    })
  })

  it('materializes the highest-precedence run_logic replacement before editing one limit', () => {
    const onChange = vi.fn()
    const groups: RuntimeEditorGroup[] = [{
      name: 'Checkout',
      scriptId: 's1',
      protocol: 'http',
      scriptConfig: {
        base_url: '',
        run_logic: { iterations: 2, workload_model: 'open' },
      },
    }]
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={groups}
          focusedIndex={0}
          onFocusChange={() => {}}
          engineDefaults={{
            base_url: '',
            run_logic: {
              iterations: 1,
              global_iterations: 100,
              max_transactions: 1000,
              workload_model: 'closed',
              auto_transactions: 'per_action',
              block_scheduling: 'sequential',
              action_sequence: ['login'],
            },
          }}
          scenarioDefault={{ iterations: 3, run_logic: { global_iterations: 200, auto_transactions: 'none' } }}
          overrides={[undefined]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('Run Logic')
    act(() => setInputValue(findLabeledControl<HTMLInputElement>('Max transactions', 'input'), '500'))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1]
    expect(last.run_logic).toEqual({
      global_iterations: 200,
      max_transactions: 500,
      auto_transactions: 'none',
    })
    expect(last.iterations).toBeUndefined()
  })

  it('materializes only the highest-precedence HTTP replacement on the first edit', () => {
    const onChange = vi.fn()
    const groups: RuntimeEditorGroup[] = [{
      name: 'Checkout',
      scriptId: 's1',
      protocol: 'http',
      scriptConfig: {
        base_url: '',
        http: {
          request_timeout_ms: 300,
          tls: { client_cert_file: 'script.crt' },
          proxy: { username: 'script-user' },
          auth: { type: 'bearer' },
        },
      },
    }]
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={groups}
          focusedIndex={0}
          onFocusChange={() => {}}
          engineDefaults={{
            base_url: '',
            http: {
              connect_timeout_ms: 100,
              request_timeout_ms: 200,
              keep_alive: true,
              default_headers: { 'X-Engine': 'yes' },
              tls: { min_version: '1.2', insecure_skip_verify: true, client_key_file: 'engine.key' },
              proxy: { url: 'http://engine-proxy', password: 'engine-password', no_proxy: ['localhost'] },
              auth: { type: 'basic', username: 'engine-user', password: 'engine-auth-password', token: 'engine-token' },
            },
          }}
          scenarioDefault={{
            http: {
              idle_timeout_ms: 400,
              keep_alive: false,
              tls: { insecure_skip_verify: false, ca_cert_file: 'scenario-ca.crt' },
              proxy: { url: 'http://scenario-proxy' },
              auth: { token: 'scenario-token' },
            },
          }}
          overrides={[undefined]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('HTTP Transport')
    clickTab('Connection')
    const version = findLabeledControl<HTMLSelectElement>('HTTP version', 'select')
    act(() => setSelectValue(version, '2'))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1]
    expect(last.http).toEqual({
      idle_timeout_ms: 400,
      version: '2',
      keep_alive: false,
      tls: {
        insecure_skip_verify: false,
        ca_cert_file: 'scenario-ca.crt',
      },
      proxy: {
        url: 'http://scenario-proxy',
      },
      auth: {
        token: 'scenario-token',
      },
    })
  })

  it('materializes only the highest-precedence browser_config replacement', () => {
    const onChange = vi.fn()
    const groups: RuntimeEditorGroup[] = [{
      name: 'Experience',
      scriptId: 's1',
      protocol: 'browser',
      scriptConfig: {
        base_url: '',
        browser: { engine: 'firefox', device_profile: 'pixel_8', viewport: { width: 1440 } },
      },
    }]
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={groups}
          focusedIndex={0}
          onFocusChange={() => {}}
          engineDefaults={{
            base_url: '',
            browser: {
              engine: 'chromium',
              headless: true,
              touch_mode: true,
              orientation: 'landscape',
              client_metrics: true,
              cache_enabled: true,
              viewport: { width: 1280, height: 720 },
              review_policy: { allow_reviewed_javascript: true, allow_review_required: true },
            },
          }}
          scenarioDefault={{
            browser: { touch_mode: false },
            browser_config: {
              headless: false,
              viewport: { height: 900 },
              review_policy: { allow_reviewed_javascript: false },
            },
          }}
          overrides={[undefined]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('HTTP Transport')
    clickTab('Browser emulation')
    const reviewedJavaScript = Array.from(container.querySelectorAll('label')).find(label =>
      (label.textContent || '').includes('Allow reviewed JavaScript'),
    )?.querySelector('input[type="checkbox"]') as HTMLInputElement
    expect(reviewedJavaScript.checked).toBe(false)
    act(() => setInputValue(findLabeledControl<HTMLInputElement>('Slow-mo (ms)', 'input'), '55'))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1]
    expect(last.browser_config).toEqual({
      headless: false,
      slow_mo_ms: 55,
      viewport: { height: 900 },
      review_policy: { allow_reviewed_javascript: false },
    })
    expect(last.browser).toBeUndefined()
  })

  it('does not resurrect inherited HTTP nested siblings inside an existing group section', () => {
    const onChange = vi.fn()
    const groups: RuntimeEditorGroup[] = [{
      name: 'Checkout',
      scriptId: 's1',
      protocol: 'http',
      scriptConfig: {
        base_url: '',
        http: {
          tls: { min_version: '1.2', client_cert_file: 'inherited.crt' },
          proxy: { username: 'inherited-user', password: 'inherited-password' },
          auth: { type: 'basic', username: 'inherited-auth-user' },
        },
      },
    }]
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={groups}
          focusedIndex={0}
          onFocusChange={() => {}}
          overrides={[{ log_level: '', auto_transactions: '', http: { idle_timeout_ms: 400 } }]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })

    clickTab('HTTP Transport')
    clickTab('TLS / SSL')
    act(() => setInputValue(findLabeledControl<HTMLInputElement>('CA cert file', 'input'), 'group-ca.crt'))
    expect(onChange.mock.calls[onChange.mock.calls.length - 1]?.[1].http).toEqual({
      idle_timeout_ms: 400,
      tls: { ca_cert_file: 'group-ca.crt' },
    })

    clickTab('Proxy')
    act(() => setInputValue(findLabeledControl<HTMLInputElement>('Proxy URL', 'input'), 'http://group-proxy'))
    expect(onChange.mock.calls[onChange.mock.calls.length - 1]?.[1].http).toEqual({
      idle_timeout_ms: 400,
      proxy: { url: 'http://group-proxy' },
    })

    clickTab('Authentication')
    act(() => setInputValue(findLabeledControl<HTMLInputElement>('Auth token', 'input'), 'group-token'))
    expect(onChange.mock.calls[onChange.mock.calls.length - 1]?.[1].http).toEqual({
      idle_timeout_ms: 400,
      auth: { token: 'group-token' },
    })
  })

  it('does not resurrect inherited browser nested siblings inside an existing group section', () => {
    const onChange = vi.fn()
    const groups: RuntimeEditorGroup[] = [{
      name: 'Experience',
      scriptId: 's1',
      protocol: 'browser',
      scriptConfig: {
        base_url: '',
        browser: {
          viewport: { width: 1440 },
          review_policy: { allow_reviewed_javascript: false, allow_review_required: true },
        },
      },
    }]
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={groups}
          focusedIndex={0}
          onFocusChange={() => {}}
          overrides={[{ log_level: '', auto_transactions: '', browser_config: { headless: false } }]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })

    clickTab('HTTP Transport')
    clickTab('Browser emulation')
    act(() => setInputValue(findLabeledControl<HTMLInputElement>('Viewport height', 'input'), '900'))
    expect(onChange.mock.calls[onChange.mock.calls.length - 1]?.[1].browser_config).toEqual({
      headless: false,
      viewport: { height: 900 },
    })

    const reviewedJavaScript = Array.from(container.querySelectorAll('label')).find(label =>
      (label.textContent || '').includes('Allow reviewed JavaScript'),
    )?.querySelector('input[type="checkbox"]') as HTMLInputElement
    expect(reviewedJavaScript.checked).toBe(false)
    act(() => reviewedJavaScript.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    expect(onChange.mock.calls[onChange.mock.calls.length - 1]?.[1].browser_config).toEqual({
      headless: false,
      review_policy: { allow_reviewed_javascript: true },
    })
  })

  it('shows the Browser emulation sub-tab only for browser groups', () => {
    const subTabLabels = () => Array.from(container.querySelectorAll('.http-transport-tab')).map(b => (b.textContent || '').trim())
    // http group → no Browser emulation sub-tab.
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor groups={GROUPS} focusedIndex={0} onFocusChange={() => {}} overrides={[undefined, undefined]} onChange={() => {}} onApplyToGroups={() => {}} />,
      )
    })
    clickTab('HTTP Transport')
    expect(subTabLabels()).not.toContain('Browser emulation')
    // browser group → Browser emulation sub-tab present.
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor groups={GROUPS} focusedIndex={1} onFocusChange={() => {}} overrides={[undefined, undefined]} onChange={() => {}} onApplyToGroups={() => {}} />,
      )
    })
    clickTab('HTTP Transport')
    expect(subTabLabels()).toContain('Browser emulation')
  })
})
