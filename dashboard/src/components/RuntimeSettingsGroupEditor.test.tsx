// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import CreateTestRuntimeOverrides from './CreateTestRuntimeOverrides'
import RuntimeSettingsGroupEditor, { type RuntimeEditorGroup } from './RuntimeSettingsGroupEditor'
import { type ScenarioRuntimeSettingsDraft } from '../types'
import { editReplacementSection, materializeRuntimeSettingsForApply } from './runtimeSettingsApply'
import { overrideFromRuntimeDraft, runtimeDraftFromOverride } from './runtimeSettingsModel'
import { buildRuntimeSettingsPayload } from './scenarioPayloadUtils'

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
  it('deletes inherited nested values when a replacement-section control is cleared', () => {
    const inherited = {
      default_headers: { Authorization: 'Bearer inherited' },
      cache: { enabled: true },
    }

    expect(editReplacementSection<typeof inherited>(undefined, { default_headers: undefined }, inherited)).toEqual({
      cache: { enabled: true },
    })
    expect(editReplacementSection<typeof inherited>(inherited, { cache: undefined }, inherited)).toEqual({
      default_headers: { Authorization: 'Bearer inherited' },
    })
  })

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
    expect(text).not.toContain('Not applicable for http protocol')
    // The overridden values are surfaced.
    expect(text).toContain('Fixed delay · 0.25 seconds')
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
    expect(text).toContain('Fixed delay · 0.5 seconds')
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

  it('attributes a v1 group row\'s whole {} error_handling and untyped pacing to the group, not the script', () => {
    const groups: RuntimeEditorGroup[] = [{
      name: 'Checkout',
      scriptId: 's1',
      protocol: 'http',
      scriptConfig: {
        base_url: 'https://example.test',
        pacing: { type: 'fixed_delay', delay_ms: 500 },
        error_handling: { continue_on_error: true },
      },
    }]
    const v1Row = { pacing: { delay_ms: 0 }, error_handling: {} } as unknown as ScenarioRuntimeSettingsDraft
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={groups}
          focusedIndex={0}
          onFocusChange={() => {}}
          overrides={[v1Row]}
          onChange={() => {}}
          onApplyToGroups={() => {}}
        />,
      )
    })
    const row = (label: string) => {
      const tr = Array.from(container.querySelectorAll('.runtime-override-row')).find(candidate => (candidate.querySelector('td')?.textContent || '').trim() === label)
      if (!tr) throw new Error(`summary row not found: ${label}`)
      const cells = Array.from(tr.querySelectorAll('td')).map(td => (td.textContent || '').trim())
      return { value: cells[1], source: cells[2] }
    }
    // Go v1 applies both sections whole: no pacing, and ContinueOnError=false.
    expect(row('Pacing')).toEqual({ value: 'None', source: 'Group override' })
    expect(row('Error handling')).toEqual({ value: 'Stop on error', source: 'Group override' })
    expect(container.textContent || '').not.toContain('Fixed delay · 0.5 seconds')
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
    expect(tabLabels()).toEqual(['Summary', 'Think Time', 'Iteration Pacing', 'Run Logic', 'Error Handling', 'HTTP Transport', 'Browser Emulation', 'Web/HTTP'])

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
    expect(container.textContent || '').toContain('Browser (Playwright)')
    clickTab('Browser (Playwright)')
    const deviceSelect = Array.from(container.querySelectorAll('select')).find(s =>
      Array.from(s.options).some(o => o.value === 'pixel_8'),
    )
    expect(deviceSelect).toBeTruthy()
  })

  it('shows the Java Env tab only for java_vuser groups and emits a java_vuser override', () => {
    const groups: RuntimeEditorGroup[] = [
      { name: 'JavaLoad', scriptId: 'j1', protocol: 'java_vuser', importedJavaVMParameters: ['-Xss2m'] },
      { name: 'Api', scriptId: 's2', protocol: 'http' },
    ]
    const onChange = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={groups}
          focusedIndex={0}
          onFocusChange={() => {}}
          overrides={[undefined, undefined]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    expect(tabLabels()).toContain('Java Env')

    clickTab('Java Env')
    // Imported VM parameters are listed together so they can be compared,
    // rather than hidden one at a time behind a single-select picker.
    const importedParameters = container.querySelector('[aria-label="Imported VM parameters"]')
    expect(importedParameters?.textContent).toContain('-Xss2m')

    // Toggling the skip-parameter-evaluation checkbox emits a java_vuser override.
    const skip = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')).find(input =>
      input.closest('label')?.textContent?.includes('Skip automatic parameter evaluation'),
    )
    expect(skip).toBeTruthy()
    act(() => skip!.click())
    expect(onChange).toHaveBeenCalledWith(0, expect.objectContaining({
      java_vuser: expect.objectContaining({ skip_parameter_evaluation: true }),
    }))
  })

  it('hides the Java Env tab for non-java protocols', () => {
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
    expect(tabLabels()).not.toContain('Java Env')
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
    expect(text).toContain('HTTP/2 required (no fallback)')
    expect(text).toContain('Logging')
    expect(text).not.toContain('Not applicable for http protocol') // Browser config row
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

  it('reports a target the apply left unchanged for missing SOAP credentials', () => {
    const onApply = vi.fn(() => ({ missingSOAPCredentialTargets: [1] }))
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
    const notice = () => container.querySelector('[data-testid="runtime-apply-notice"]')
    const applyTo = () => {
      act(() => findButton('Apply to groups…')!.dispatchEvent(new MouseEvent('click', { bubbles: true })))
      const targetCheckbox = container.querySelector('.runtime-apply-target input[type="checkbox"]') as HTMLInputElement
      act(() => targetCheckbox.dispatchEvent(new MouseEvent('click', { bubbles: true })))
      act(() => findButton('Apply')!.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    }
    applyTo()
    expect(onApply).toHaveBeenCalledWith(0, [1])
    expect(notice()?.textContent).toContain('no SOAP WS-Security credentials of its own')
    onApply.mockReturnValue({ missingSOAPCredentialTargets: [] })
    applyTo()
    expect(notice()).toBeNull()
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

  it('converts a legacy log level row to a partial row listing log.level on edit (Error Handling tab)', () => {
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
    expect(last.override_fields).toEqual(['log.level'])
    // The row is sent canonically: only log.level, no compact alias beside it.
    expect(buildRuntimeSettingsPayload(last)).toEqual({ log: { level: 'standard' }, override_fields: ['log.level'] })
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

  it('Clear Iterations returns only iterations to inherited and keeps run_logic siblings overridden', () => {
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
    // The v1 run_logic section is split into its other fields, so they keep
    // their whole-section meaning while iterations inherit the scenario's 7.
    expect(last.run_logic).toEqual({ workload_model: 'open' })
    expect(last.override_fields).toContain('run_logic.workload_model')
    expect(last.override_fields).toContain('run_logic.global_iterations')
    expect(last.override_fields).not.toContain('run_logic')
    expect(last.override_fields).not.toContain('run_logic.iterations')
  })

  it('Clear Auto-transactions returns only auto-transactions to inherited', () => {
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
    expect(last.run_logic).toEqual({ workload_model: 'open' })
    expect(last.override_fields).toContain('run_logic.workload_model')
    expect(last.override_fields).not.toContain('run_logic.auto_transactions')
  })

  it('Clear Log level returns only the level to inherited', () => {
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
    expect(last.log).toEqual({ on_error_only: true })
    expect(last.override_fields).toContain('log.on_error_only')
    expect(last.override_fields).not.toContain('log.level')
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
    // Only the distribution is overridden; the mean keeps inheriting 800 rather
    // than being pinned (or forced to 0).
    expect(last.think_time).toEqual({ type: 'gaussian' })
    expect(last.override_fields).toEqual(['think_time.type'])
  })

  it('keeps a legacy iterations override listed when editing workload model (Run Logic tab)', () => {
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
    expect(last.run_logic).toEqual({ iterations: 5, workload_model: 'open' })
    expect(last.override_fields).toEqual(['run_logic.iterations', 'run_logic.workload_model'])
    // The legacy alias is folded into the section on the wire.
    expect(buildRuntimeSettingsPayload(last)).toEqual({
      run_logic: { iterations: 5, workload_model: 'open' },
      override_fields: ['run_logic.iterations', 'run_logic.workload_model'],
    })
  })

  it('keeps a legacy browser device override listed when editing an advanced field (HTTP Transport tab)', () => {
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
    clickTab('Browser (Playwright)')
    const engineSelect = Array.from(container.querySelectorAll('select')).find(s =>
      Array.from(s.options).some(o => o.value === 'webkit') && Array.from(s.options).some(o => o.value === 'chromium'),
    ) as HTMLSelectElement
    expect(engineSelect).toBeTruthy()
    act(() => setSelectValue(engineSelect, 'firefox'))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1][1]
    expect(last.browser_config).toEqual({ device_profile: 'pixel_8', engine: 'firefox' })
    expect(last.override_fields).toEqual(['browser.device_profile', 'browser.engine'])
    // The legacy compact block is folded into browser_config on the wire.
    expect(buildRuntimeSettingsPayload(last)).toEqual({
      browser_config: { device_profile: 'pixel_8', engine: 'firefox' },
      override_fields: ['browser.device_profile', 'browser.engine'],
    })
  })

  it('overrides the browser QUIC mode for a group and can return to inherit', () => {
    const onChange = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={1}
          onFocusChange={() => {}}
          overrides={[undefined, { log_level: '', auto_transactions: '' }]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('HTTP Transport')
    clickTab('Browser (Playwright)')
    const quicSelect = Array.from(container.querySelectorAll('select')).find(s =>
      Array.from(s.options).some(o => o.textContent === 'Off (launch with QUIC disabled)'),
    ) as HTMLSelectElement
    expect(quicSelect).toBeTruthy()
    expect(quicSelect.value).toBe('')
    act(() => setSelectValue(quicSelect, 'off'))
    expect(onChange.mock.calls[onChange.mock.calls.length - 1][1].browser_config).toEqual({ quic: 'off' })
    expect(onChange.mock.calls[onChange.mock.calls.length - 1][1].override_fields).toEqual(['browser.quic'])
    act(() => setSelectValue(quicSelect, ''))
    // Inherit resets the path; nothing else was overridden, so the group inherits everything.
    expect(onChange.mock.calls[onChange.mock.calls.length - 1][1]).toBeUndefined()
  })

  it('warns when a group override pairs WebKit with QUIC off', () => {
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={1}
          onFocusChange={() => {}}
          overrides={[undefined, { log_level: '', auto_transactions: '', browser_config: { engine: 'webkit', quic: 'off' } }]}
          onChange={vi.fn()}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('HTTP Transport')
    clickTab('Browser (Playwright)')
    expect(container.textContent).toContain('WebKit cannot launch with QUIC disabled')
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

    act(() => setSelectValue(distSelect, 'recorded'))
    const recorded = onChange.mock.calls[onChange.mock.calls.length - 1][1]
    expect(recorded.think_time).toEqual({ type: 'recorded' })
  })

  it('shows inherited Gaussian and Uniform controls from the effective think-time type', () => {
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={0}
          onFocusChange={() => {}}
          scenarioDefault={{
            think_time: {
              type: 'gaussian',
              mean_ms: 500,
              std_ms: 125,
              min_ms: 100,
              max_ms: 900,
            },
          }}
          overrides={[undefined, undefined]}
          onChange={() => {}}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('Think Time')
    expect(findLabeledControl<HTMLInputElement>('Std dev (ms)', 'input').placeholder).toBe('125')
    expect(findLabeledControl<HTMLInputElement>('Min (ms)', 'input').placeholder).toBe('100')
    expect(findLabeledControl<HTMLInputElement>('Max (ms)', 'input').placeholder).toBe('900')

    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={0}
          onFocusChange={() => {}}
          scenarioDefault={{ think_time: { type: 'uniform', min_ms: 250, max_ms: 1750 } }}
          overrides={[undefined, undefined]}
          onChange={() => {}}
          onApplyToGroups={() => {}}
        />,
      )
    })
    expect(container.textContent).not.toContain('Std dev (ms)')
    expect(findLabeledControl<HTMLInputElement>('Min (ms)', 'input').placeholder).toBe('250')
    expect(findLabeledControl<HTMLInputElement>('Max (ms)', 'input').placeholder).toBe('1750')
  })

  it('does not author a zero think-time multiplier', () => {
    const onChange = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={0}
          onFocusChange={() => {}}
          overrides={[{ think_time: { type: 'recorded', multiplier: 1 }, log_level: '', auto_transactions: '' }, undefined]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('Think Time')
    const multiplier = findLabeledControl<HTMLInputElement>('Multiplier', 'input')
    expect(multiplier.min).toBe('0')
    expect(multiplier.max).toBe('100')
    expect(multiplier.step).toBe('any')
    act(() => multiplier.focus())
    act(() => setInputValue(multiplier, '0'))
    expect(multiplier.value).toBe('0')
    expect(onChange).not.toHaveBeenCalled()
    act(() => multiplier.blur())
    const last = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1]
    expect(last.think_time).toEqual({ type: 'recorded' })
    act(() => multiplier.focus())
    act(() => setInputValue(multiplier, '0.05'))
    const positive = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1]
    expect(positive.think_time).toEqual({ type: 'recorded', multiplier: 0.05 })
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
    expect(container.textContent || '').toContain('Min delay (seconds)')
    expect(container.textContent || '').toContain('Max delay (seconds)')
    const pacingSelect = Array.from(container.querySelectorAll('select')).find(s =>
      Array.from(s.options).some(o => o.value === 'random_delay') && Array.from(s.options).some(o => o.value === 'random_interval'),
    ) as HTMLSelectElement
    expect(pacingSelect.value).toBe('random_delay')
  })

  it('edits pacing in decimal seconds while retaining milliseconds in the override', () => {
    const onChange = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={0}
          onFocusChange={() => {}}
          overrides={[{ pacing: { type: 'fixed_interval', interval_ms: 5000 }, log_level: '', auto_transactions: '' }, undefined]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('Iteration Pacing')
    const interval = findLabeledControl<HTMLInputElement>('Pacing interval (seconds)', 'input')
    expect(interval.value).toBe('5')
    expect(interval.step).toBe('0.001')

    act(() => setInputValue(interval, '2.5'))
    const updated = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1]
    expect(updated.pacing).toEqual({ type: 'fixed_interval', interval_ms: 2500 })
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
    // HTTP groups expose request and session editors alongside the typed settings.
    const subTabs = Array.from(container.querySelectorAll('.http-transport-tab')).map(b => (b.textContent || '').trim())
    expect(subTabs).toEqual(['Request Defaults0', 'Session & Cache0', 'Connection', 'Timeouts', 'TLS / SSL', 'Proxy', 'Authentication'])
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
    clickTab('HTTP Transport')
    clickTab('Timeouts')
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

  it('overrides only the pacing type when an inheriting group changes it', () => {
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
    expect(last.pacing).toEqual({ type: 'random_delay' })
    expect(last.override_fields).toEqual(['pacing.type'])
  })

  it('normalizes a think-time distribution change and lists only the leaves it changes', () => {
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
    // Uniform seeds its minimum from the effective mean; the inherited maximum
    // and ignore=false keep inheriting from the scenario default.
    expect(last.think_time).toEqual({ type: 'uniform', min_ms: 300 })
    expect(last.override_fields).toEqual(['think_time.type', 'think_time.min_ms'])

    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={groups}
          focusedIndex={0}
          onFocusChange={() => {}}
          engineDefaults={{ base_url: '', think_time: { type: 'fixed', mean_ms: 100, multiplier: 1.5, limit_ms: 900, ignore: true } }}
          scenarioDefault={{ think_time: { type: 'gaussian', mean_ms: 300, max_ms: 600, ignore: false } }}
          overrides={[last]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    expect(container.textContent).not.toContain('Mean (ms)')

    const minimum = findLabeledControl<HTMLInputElement>('Min (ms)', 'input')
    act(() => setInputValue(minimum, '350'))
    const edited = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1]
    expect(edited.think_time).toEqual({ type: 'uniform', min_ms: 350 })
    expect(edited.override_fields).toEqual(['think_time.type', 'think_time.min_ms'])
  })

  it('lists one error_handling field and keeps continue_on_error inherited', () => {
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
    expect(last.max_retries).toBe(3)
    expect(buildRuntimeSettingsPayload(last)).toEqual({
      error_handling: { max_retries: 3 },
      override_fields: ['error_handling.max_retries'],
    })
    expect(last.override_fields).toEqual(['error_handling.max_retries'])
    // The scenario's continue_on_error=false and retry delay keep inheriting.
    expect(last.continue_on_error).toBeUndefined()
    expect(last.retry_delay_ms).toBeUndefined()
    expect(last.snapshot_on_error).toBeUndefined()
  })

  it('lists only log.extended_options when toggling one logging option', () => {
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
    expect(last.log).toEqual({ extended_options: ['advanced_trace'] })
    expect(last.override_fields).toEqual(['log.extended_options'])
  })

  it('shows run-evidence redaction as off by default and writes an explicit true when enabled', () => {
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
          scenarioDefault={{ log: { on_error_only: true } }}
          overrides={[undefined]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('Error Handling')
    const redact = Array.from(container.querySelectorAll('label')).find(label =>
      (label.textContent || '').includes('Redact credentials in run evidence'),
    ) as HTMLLabelElement
    const checkbox = redact.querySelector('input[type="checkbox"]') as HTMLInputElement
    // No layer sets the field, so it reads as the default OFF.
    expect(checkbox.checked).toBe(false)
    expect(redact.className).not.toContain('selected')

    act(() => checkbox.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1]
    expect(last.log).toEqual({ redact_run_evidence: true })
    expect(last.override_fields).toEqual(['log.redact_run_evidence'])
  })

  it('renders an inherited redact_run_evidence=false unchecked and can turn it back on', () => {
    const onChange = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={GROUPS}
          focusedIndex={0}
          onFocusChange={() => {}}
          scenarioDefault={{ log: { level: 'standard', redact_run_evidence: false } }}
          overrides={[undefined, undefined]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('Error Handling')
    const checkbox = Array.from(container.querySelectorAll('label')).find(label =>
      (label.textContent || '').includes('Redact credentials in run evidence'),
    )?.querySelector('input[type="checkbox"]') as HTMLInputElement
    expect(checkbox.checked).toBe(false)

    act(() => checkbox.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1]
    expect(last.log).toEqual({ redact_run_evidence: true })
    expect(last.override_fields).toEqual(['log.redact_run_evidence'])
  })

  it('lists only the run_logic limit an inheriting group edits', () => {
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
    expect(last.run_logic).toEqual({ max_transactions: 500 })
    expect(last.override_fields).toEqual(['run_logic.max_transactions'])
    expect(last.iterations).toBeUndefined()
  })

  it('edits the QUIC block with inherited values, field validation and a disabled reason', () => {
    const onChange = vi.fn()
    const groups: RuntimeEditorGroup[] = [{
      name: 'Checkout', scriptId: 's1', protocol: 'http',
      scriptConfig: { base_url: 'http://shop.example', http: { version: '3', quic: { broken_ttl_ms: 60000 } } },
    }]
    const render = (override?: ScenarioRuntimeSettingsDraft) => act(() => {
      root.render(<RuntimeSettingsGroupEditor groups={groups} focusedIndex={0} onFocusChange={() => {}} engineDefaults={{ base_url: '', http: {} }}
        overrides={[override]} onChange={onChange} onApplyToGroups={() => {}} />)
    })
    render()
    clickTab('HTTP Transport')
    clickTab('Connection')
    expect(container.querySelector('legend')?.textContent).toBe('QUIC (HTTP/3)')
    expect(findLabeledControl<HTMLInputElement>('Broken origin TTL (ms)', 'input').placeholder).toBe('Inherited: 60,000')
    // Exact HTTP/3 against the inherited http:// base URL is reported at the version field.
    expect(Array.from(container.querySelectorAll('.http3-policy-issue')).map(issue => issue.textContent)).toEqual([expect.stringContaining('only https origins')])
    act(() => setInputValue(findLabeledControl<HTMLInputElement>('Handshake timeout (ms)', 'input'), '500'))
    const edited = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1] as ScenarioRuntimeSettingsDraft
    // Only the edited QUIC leaf is overridden; the version and the rest of the
    // QUIC block keep inheriting from the script.
    expect(edited.http).toEqual({ quic: { handshake_timeout_ms: 500 } })
    expect(edited.override_fields).toEqual(['http.quic.handshake_timeout_ms'])
    render(edited)
    const handshake = findLabeledControl<HTMLInputElement>('Handshake timeout (ms)', 'input')
    expect(handshake.getAttribute('aria-invalid')).toBe('true')
    // Clearing a QUIC field of a stored v1 section resets just that leaf: the
    // section is split so its other fields keep their v1 meaning.
    render({ log_level: '', auto_transactions: '', http: { version: '3', quic: { handshake_timeout_ms: 2000 } } })
    act(() => setInputValue(findLabeledControl<HTMLInputElement>('Handshake timeout (ms)', 'input'), ''))
    const cleared = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1] as ScenarioRuntimeSettingsDraft
    expect(cleared.http).toEqual({ version: '3' })
    expect(cleared.override_fields).toContain('http.version')
    expect(cleared.override_fields).toContain('http.quic.broken_ttl_ms')
    expect(cleared.override_fields).not.toContain('http.quic.handshake_timeout_ms')
    expect(cleared.override_fields).not.toContain('http')
    // A custom proxy keeps requests on TCP, so the block is disabled with the reason.
    render({ log_level: '', auto_transactions: '', http: { version: 'auto+h3', proxy: { url: 'http://proxy.example:3128' } } })
    expect(container.querySelector<HTMLFieldSetElement>('fieldset.http-quic-settings')?.disabled).toBe(true)
    expect(container.querySelector('.http-quic-disabled')?.textContent).toContain('custom proxy')
  })

  it('lists only the HTTP version on an inheriting group, leaving script TLS/proxy/auth inherited', () => {
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
    expect(Array.from(version.options).map(option => option.textContent)).toEqual([
      'Inherit',
      'Require HTTP/1.1',
      'Require HTTP/2 (no fallback)',
      'Upgrade to HTTP/3 when advertised',
      'Require HTTP/3 (no fallback)',
    ])
    expect(version.querySelector('option[value="1.0"]')).toBeNull()
    expect(container.textContent).toContain('required HTTP/2 never falls back to HTTP/1.1')
    act(() => setSelectValue(version, 'auto+h3'))
    expect(onChange.mock.calls[onChange.mock.calls.length - 1]?.[1].http.version).toBe('auto+h3')
    act(() => setSelectValue(version, '2'))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1]
    // No inherited value (script, scenario or engine) is copied into the group.
    expect(last.http).toEqual({ version: '2' })
    expect(last.override_fields).toEqual(['http.version'])
  })

  it('lists only the browser field an inheriting group edits', () => {
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
    clickTab('Browser (Playwright)')
    const reviewedJavaScript = Array.from(container.querySelectorAll('label')).find(label =>
      (label.textContent || '').includes('Allow reviewed JavaScript'),
    )?.querySelector('input[type="checkbox"]') as HTMLInputElement
    expect(reviewedJavaScript.checked).toBe(false)
    act(() => setInputValue(findLabeledControl<HTMLInputElement>('Slow-mo (ms)', 'input'), '55'))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1]
    expect(last.browser_config).toEqual({ slow_mo_ms: 55 })
    expect(last.override_fields).toEqual(['browser.slow_mo_ms'])
    expect(last.browser).toBeUndefined()
  })

  // Page timings and interaction evidence are the group's to choose: the
  // choice is one listed leaf; the script's own browser settings keep applying.
  it('turns on page timings and interaction evidence for a browser group', () => {
    const onChange = vi.fn()
    const groups: RuntimeEditorGroup[] = [{
      name: 'Experience',
      scriptId: 's1',
      protocol: 'browser',
      scriptConfig: { base_url: '', browser: { engine: 'chromium', capture_interactions: true } },
    }]
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor groups={groups} focusedIndex={0} onFocusChange={() => {}} overrides={[undefined]} onChange={onChange} onApplyToGroups={() => {}} />,
      )
    })
    clickTab('HTTP Transport')
    clickTab('Browser (Playwright)')
    const checkbox = (text: string) => Array.from(container.querySelectorAll('label')).find(label =>
      (label.textContent || '').includes(text),
    )?.querySelector('input[type="checkbox"]') as HTMLInputElement
    expect(checkbox('Page timings and Web Vitals').checked).toBe(false)
    expect(checkbox('Interaction evidence').checked).toBe(true)
    act(() => checkbox('Page timings and Web Vitals').click())
    const last = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1]
    expect(last.browser_config).toEqual({ capture_document_metrics: true })
    expect(last.override_fields).toEqual(['browser.capture_document_metrics'])
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
    clickTab('Browser (Playwright)')
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

  it('offers the Browser Emulation tab for http groups and writes browser_emulation overrides', () => {
    const onChange = vi.fn()
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor groups={GROUPS} focusedIndex={0} onFocusChange={() => {}} overrides={[undefined, undefined]} onChange={onChange} onApplyToGroups={() => {}} />,
      )
    })
    clickTab('Browser Emulation')
    const browserSelect = container.querySelector<HTMLSelectElement>('select[aria-label="Browser type"]')
    expect(browserSelect).toBeTruthy()
    act(() => {
      browserSelect!.value = 'chrome'
      browserSelect!.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const written = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1]
    expect(written?.browser_emulation).toMatchObject({ mode: 'specific', browser_type: 'chrome' })

    clickTab('HTTP Transport')
    const sessionCacheTab = Array.from(container.querySelectorAll('[role="tab"]'))
      .find(tab => (tab.textContent || '').startsWith('Session & Cache'))
    expect(sessionCacheTab).toBeTruthy()
    act(() => sessionCacheTab!.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    const cacheCheckbox = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
      .find(cb => cb.closest('label')?.textContent?.includes('Simulate HTTP cache'))
    expect(cacheCheckbox).toBeTruthy()
    act(() => cacheCheckbox!.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    const cacheWritten = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1]
    expect(cacheWritten?.http).toMatchObject({ cache_enabled: true })
    expect(cacheWritten?.browser_emulation).toBeUndefined()
  })

  it('keeps this group cache and language edits when browser emulation mode returns to Inherit', () => {
    const onChange = vi.fn()
    const groupOverride: ScenarioRuntimeSettingsDraft = {
      log_level: '',
      auto_transactions: '',
      browser_emulation: {
        mode: 'specific',
        browser_type: 'chrome',
        browser_version: '126',
        language: 'de-DE',
        cache: { simulate_cache: true },
      },
    }
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor groups={GROUPS} focusedIndex={0} onFocusChange={() => {}} overrides={[groupOverride, undefined]} onChange={onChange} onApplyToGroups={() => {}} />,
      )
    })
    clickTab('Browser Emulation')
    const modeSelect = container.querySelector<HTMLSelectElement>('select[aria-label="Browser emulation mode"]')
    expect(modeSelect).toBeTruthy()
    act(() => {
      modeSelect!.value = ''
      modeSelect!.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const written = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1]
    // Identity goes back to the inherited layer...
    expect(written?.browser_emulation?.mode).toBeUndefined()
    expect(written?.browser_emulation?.browser_type).toBeUndefined()
    // ...but language and cache are not mode-dependent at replay, so the group's
    // own edits to them must survive rather than be silently discarded.
    expect(written?.browser_emulation?.language).toBe('de-DE')
    expect(written?.browser_emulation?.cache).toMatchObject({ simulate_cache: true })
  })

  describe('browser emulation identity over an inherited script section', () => {
    const renderInherited = (browserEmulation: NonNullable<RuntimeEditorGroup['scriptConfig']>['browser_emulation']) => {
      const onChange = vi.fn()
      const group: RuntimeEditorGroup = {
        name: 'Checkout',
        scriptId: 's1',
        protocol: 'http',
        scriptConfig: { base_url: '', browser_emulation: browserEmulation },
      }
      act(() => {
        root.render(
          <RuntimeSettingsGroupEditor groups={[group]} focusedIndex={0} onFocusChange={() => {}} overrides={[undefined]} onChange={onChange} onApplyToGroups={() => {}} />,
        )
      })
      clickTab('Browser Emulation')
      const written = () => onChange.mock.calls[onChange.mock.calls.length - 1]?.[1] as ScenarioRuntimeSettingsDraft | undefined
      const merged = () => materializeRuntimeSettingsForApply(undefined, group.scriptConfig, undefined, written())
      return { written, merged }
    }

    it('switching to Custom browser writes empty browser type, version and platform instead of inheriting them', () => {
      const { written, merged } = renderInherited({ mode: 'specific', browser_type: 'chrome', browser_version: '126', platform: 'windows' })
      act(() => setSelectValue(container.querySelector<HTMLSelectElement>('select[aria-label="Browser emulation mode"]')!, 'custom'))
      const draft = written()
      expect(draft?.override_fields).toEqual(expect.arrayContaining([
        'browser_emulation.mode',
        'browser_emulation.browser_type',
        'browser_emulation.browser_version',
        'browser_emulation.platform',
      ]))
      expect(draft?.browser_emulation).toMatchObject({ mode: 'custom', browser_type: '', browser_version: '', platform: '' })
      const effective = merged()?.browser_emulation
      expect(effective?.mode).toBe('custom')
      expect(effective?.browser_type || undefined).toBeUndefined()
      expect(effective?.browser_version || undefined).toBeUndefined()
      expect(effective?.platform || undefined).toBeUndefined()
    })

    it('switching to Specific browser writes an empty custom User-Agent instead of inheriting it', () => {
      const { written, merged } = renderInherited({ mode: 'custom', custom_user_agent: 'MyAgent/1.0' })
      act(() => setSelectValue(container.querySelector<HTMLSelectElement>('select[aria-label="Browser emulation mode"]')!, 'specific'))
      const draft = written()
      expect(draft?.override_fields).toEqual(expect.arrayContaining(['browser_emulation.mode', 'browser_emulation.custom_user_agent']))
      expect(draft?.browser_emulation).toMatchObject({ mode: 'specific', custom_user_agent: '' })
      expect(merged()?.browser_emulation?.custom_user_agent || undefined).toBeUndefined()
    })

    it('picking a browser writes empty version and platform instead of inheriting the previous browser\'s', () => {
      const { written, merged } = renderInherited({ mode: 'specific', browser_type: 'chrome', browser_version: '126', platform: 'android' })
      act(() => setSelectValue(container.querySelector<HTMLSelectElement>('select[aria-label="Browser type"]')!, 'safari'))
      const draft = written()
      expect(draft?.override_fields).toEqual(expect.arrayContaining([
        'browser_emulation.browser_type',
        'browser_emulation.browser_version',
        'browser_emulation.platform',
      ]))
      expect(draft?.browser_emulation).toMatchObject({ mode: 'specific', browser_type: 'safari', browser_version: '', platform: '' })
      const effective = merged()?.browser_emulation
      expect(effective?.browser_type).toBe('safari')
      expect(effective?.platform || undefined).toBeUndefined()
      expect(effective?.browser_version || undefined).toBeUndefined()
    })

    // Re-renders with each written draft, so a sequence of edits builds on the last.
    const renderStateful = (browserEmulation: NonNullable<RuntimeEditorGroup['scriptConfig']>['browser_emulation']) => {
      const group: RuntimeEditorGroup = {
        name: 'Checkout',
        scriptId: 's1',
        protocol: 'http',
        scriptConfig: { base_url: '', browser_emulation: browserEmulation },
      }
      let current: ScenarioRuntimeSettingsDraft | undefined
      const render = () => root.render(
        <RuntimeSettingsGroupEditor groups={[group]} focusedIndex={0} onFocusChange={() => {}} overrides={[current]} onChange={(_index, next) => { current = next; render() }} onApplyToGroups={() => {}} />,
      )
      act(() => render())
      clickTab('Browser Emulation')
      const select = (label: string) => container.querySelector<HTMLSelectElement>(`select[aria-label="${label}"]`)!
      return {
        pick: (label: string, value: string) => act(() => setSelectValue(select(label), value)),
        type: (label: string, value: string) => act(() => setInputValue(container.querySelector<HTMLInputElement>(`input[aria-label="${label}"]`)!, value)),
        written: () => current,
        merged: () => materializeRuntimeSettingsForApply(undefined, group.scriptConfig, undefined, current)?.browser_emulation,
        // The merged identity after the script (a lower layer) changes its own browser emulation.
        mergedOver: (browserEmulation: NonNullable<RuntimeEditorGroup['scriptConfig']>['browser_emulation']) =>
          materializeRuntimeSettingsForApply(undefined, { base_url: '', browser_emulation: browserEmulation }, undefined, current)?.browser_emulation,
      }
    }

    it('a Platform edit pins the inherited mode and browser with it, so a lower-layer browser change keeps the row valid', () => {
      const ui = renderStateful({ mode: 'specific', browser_type: 'chrome', browser_version: '126', platform: 'android' })
      ui.pick('Browser platform', 'windows')
      expect(ui.written()?.override_fields).toEqual(expect.arrayContaining([
        'browser_emulation.mode',
        'browser_emulation.browser_type',
        'browser_emulation.platform',
      ]))
      expect(ui.mergedOver({ mode: 'specific', browser_type: 'safari' })).toMatchObject({ mode: 'specific', browser_type: 'chrome', platform: 'windows' })
      expect(ui.mergedOver({ mode: 'custom', custom_user_agent: 'Other/1' })).toMatchObject({ mode: 'specific', browser_type: 'chrome', platform: 'windows' })
      expect(ui.mergedOver({ mode: 'custom', custom_user_agent: 'Other/1' })?.custom_user_agent || undefined).toBeUndefined()
    })

    it('a Version edit pins the inherited mode and browser with it', () => {
      const ui = renderStateful({ mode: 'specific', browser_type: 'chrome', browser_version: '126' })
      ui.pick('Browser version', '120')
      expect(ui.written()?.override_fields).toEqual(expect.arrayContaining(['browser_emulation.mode', 'browser_emulation.browser_type', 'browser_emulation.browser_version']))
      expect(ui.mergedOver({ mode: 'specific', browser_type: 'safari' })).toMatchObject({ browser_type: 'chrome', browser_version: '120' })
    })

    it('a Custom User-Agent edit pins the inherited custom mode with it', () => {
      const ui = renderStateful({ mode: 'custom', custom_user_agent: 'Script/1' })
      ui.type('Custom User-Agent', 'Group/2')
      expect(ui.written()?.override_fields).toEqual(expect.arrayContaining(['browser_emulation.mode', 'browser_emulation.custom_user_agent']))
      const over = ui.mergedOver({ mode: 'specific', browser_type: 'chrome', platform: 'android' })
      expect(over).toMatchObject({ mode: 'custom', custom_user_agent: 'Group/2' })
      expect(over?.browser_type || undefined).toBeUndefined()
      expect(over?.platform || undefined).toBeUndefined()
    })

    it('shows an effective platform or version the browser does not offer as an invalid option', () => {
      renderStateful({ mode: 'specific', browser_type: 'safari', browser_version: '999', platform: 'android' })
      const platform = container.querySelector<HTMLSelectElement>('select[aria-label="Browser platform"]')!
      expect(platform.value).toBe('android')
      expect(platform.selectedOptions[0]?.textContent).toMatch(/not available/i)
      const version = container.querySelector<HTMLSelectElement>('select[aria-label="Browser version"]')!
      expect(version.value).toBe('999')
      expect(version.selectedOptions[0]?.textContent).toMatch(/not available/i)
    })

    it('Mode > Custom after Specific inherits the custom User-Agent again rather than keeping an empty one', () => {
      const ui = renderStateful({ mode: 'custom', custom_user_agent: 'Script/1' })
      ui.pick('Browser emulation mode', 'specific')
      ui.pick('Browser emulation mode', 'custom')
      expect(ui.written()?.override_fields ?? []).not.toContain('browser_emulation.custom_user_agent')
      expect(ui.merged()).toMatchObject({ mode: 'custom', custom_user_agent: 'Script/1' })
    })

    it('clearing the Custom User-Agent inherits the lower layer\'s User-Agent again', () => {
      const ui = renderStateful({ mode: 'custom', custom_user_agent: 'Script/1' })
      ui.type('Custom User-Agent', 'Group/2')
      ui.type('Custom User-Agent', '')
      expect(ui.written()?.override_fields ?? []).not.toContain('browser_emulation.custom_user_agent')
      expect(ui.merged()).toMatchObject({ mode: 'custom', custom_user_agent: 'Script/1' })
    })

    it('Browser default writes an empty platform when this group overrides the browser', () => {
      const ui = renderStateful({ mode: 'specific', browser_type: 'chrome', browser_version: '126', platform: 'android' })
      ui.pick('Browser type', 'safari')
      ui.pick('Browser platform', 'macos')
      ui.pick('Browser platform', '')
      expect(ui.written()?.override_fields).toContain('browser_emulation.platform')
      expect(ui.written()?.browser_emulation?.platform).toBe('')
      expect(ui.merged()).toMatchObject({ browser_type: 'safari' })
      expect(ui.merged()?.platform || undefined).toBeUndefined()
    })

    it('Latest writes an empty version when this group overrides the browser', () => {
      const ui = renderStateful({ mode: 'specific', browser_type: 'chrome', browser_version: '126', platform: 'android' })
      ui.pick('Browser type', 'safari')
      ui.pick('Browser version', '17')
      ui.pick('Browser version', '')
      expect(ui.written()?.override_fields).toContain('browser_emulation.browser_version')
      expect(ui.written()?.browser_emulation?.browser_version).toBe('')
      expect(ui.merged()).toMatchObject({ browser_type: 'safari' })
      expect(ui.merged()?.browser_version || undefined).toBeUndefined()
    })

    it('Latest and Browser default override an inherited version and platform while the browser itself is inherited', () => {
      const ui = renderStateful({ mode: 'specific', browser_type: 'chrome', browser_version: '126', platform: 'android' })
      ui.pick('Browser version', '')
      expect(ui.written()?.override_fields).toContain('browser_emulation.browser_version')
      expect(ui.written()?.browser_emulation?.browser_version).toBe('')
      // The browser is pinned with the version, so a lower-layer browser change cannot pair
      // this group's version and platform with another browser.
      expect(ui.written()?.override_fields).toEqual(expect.arrayContaining(['browser_emulation.mode', 'browser_emulation.browser_type']))
      expect(ui.merged()).toMatchObject({ browser_type: 'chrome' })
      expect(ui.merged()?.browser_version || undefined).toBeUndefined()
      ui.pick('Browser platform', '')
      expect(ui.written()?.override_fields).toContain('browser_emulation.platform')
      expect(ui.written()?.browser_emulation?.platform).toBe('')
      expect(ui.merged()?.platform || undefined).toBeUndefined()
      expect(container.querySelector<HTMLSelectElement>('select[aria-label="Browser version"]')!.value).toBe('')
      expect(container.querySelector<HTMLSelectElement>('select[aria-label="Browser platform"]')!.value).toBe('')
    })

    it('Latest and Browser default inherit again when no lower layer sets them', () => {
      const ui = renderStateful({ mode: 'specific', browser_type: 'chrome' })
      ui.pick('Browser version', '120')
      ui.pick('Browser version', '')
      expect(ui.written()?.override_fields ?? []).not.toContain('browser_emulation.browser_version')
      ui.pick('Browser platform', 'windows')
      ui.pick('Browser platform', '')
      expect(ui.written()).toBeUndefined()
    })

    it('Language > Engine default overrides an inherited language to empty', () => {
      const ui = renderStateful({ mode: 'specific', browser_type: 'chrome', language: 'de-DE' })
      ui.pick('Browser language', '')
      expect(ui.written()?.override_fields).toEqual(['browser_emulation.language'])
      expect(ui.written()?.browser_emulation?.language).toBe('')
      expect(ui.merged()?.language || undefined).toBeUndefined()
      expect(container.querySelector<HTMLSelectElement>('select[aria-label="Browser language"]')!.value).toBe('')
    })

    it('Language > Engine default inherits again when no lower layer sets a language', () => {
      const ui = renderStateful({ mode: 'specific', browser_type: 'chrome' })
      ui.pick('Browser language', 'fr-FR')
      ui.pick('Browser language', '')
      expect(ui.written()).toBeUndefined()
    })

    it('Mode > Inherit can be picked after Browser > Engine default and inherits the identity again', () => {
      const ui = renderStateful({ mode: 'specific', browser_type: 'chrome', browser_version: '126', language: 'de-DE' })
      ui.pick('Browser language', 'fr-FR')
      ui.pick('Browser type', '')
      const modeSelect = container.querySelector<HTMLSelectElement>('select[aria-label="Browser emulation mode"]')!
      expect(modeSelect.value).not.toBe('')
      expect(modeSelect.selectedOptions[0]?.textContent).toBe('None (engine default)')
      ui.pick('Browser emulation mode', '')
      const identityPaths = ['mode', 'browser_type', 'browser_version', 'platform', 'custom_user_agent'].map(f => `browser_emulation.${f}`)
      expect((ui.written()?.override_fields ?? []).filter(path => identityPaths.includes(path))).toEqual([])
      expect(ui.written()?.override_fields).toEqual(['browser_emulation.language'])
      expect(ui.merged()).toMatchObject({ mode: 'specific', browser_type: 'chrome', browser_version: '126', language: 'fr-FR' })
      expect(container.querySelector<HTMLSelectElement>('select[aria-label="Browser emulation mode"]')!.value).toBe('')
    })

    it('Engine default overrides the script browser to the engine default identity', () => {
      const ui = renderStateful({ mode: 'specific', browser_type: 'chrome', browser_version: '126', platform: 'android', language: 'de-DE' })
      ui.pick('Browser type', '')
      expect(ui.written()?.override_fields).toEqual(expect.arrayContaining([
        'browser_emulation.mode',
        'browser_emulation.browser_type',
        'browser_emulation.browser_version',
        'browser_emulation.platform',
        'browser_emulation.custom_user_agent',
      ]))
      const effective = ui.merged()
      expect(effective?.mode || undefined).toBeUndefined()
      expect(effective?.browser_type || undefined).toBeUndefined()
      expect(effective?.browser_version || undefined).toBeUndefined()
      expect(effective?.platform || undefined).toBeUndefined()
      // Language is not part of the browser identity and keeps inheriting.
      expect(effective?.language).toBe('de-DE')
      expect(container.querySelector<HTMLSelectElement>('select[aria-label="Browser type"]')!.value).toBe('')
    })

    it('Inherit still resets the identity paths rather than writing empties', () => {
      const { written } = renderInherited({ mode: 'specific', browser_type: 'chrome' })
      const modeSelect = () => container.querySelector<HTMLSelectElement>('select[aria-label="Browser emulation mode"]')!
      act(() => setSelectValue(modeSelect(), 'custom'))
      act(() => setSelectValue(modeSelect(), ''))
      expect(written()).toBeUndefined()
    })
  })

  it('hides the Browser Emulation tab for browser-protocol groups', () => {
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor groups={GROUPS} focusedIndex={1} onFocusChange={() => {}} overrides={[undefined, undefined]} onChange={() => {}} onApplyToGroups={() => {}} />,
      )
    })
    expect(tabLabels()).not.toContain('Browser Emulation')
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
    expect(subTabLabels()).not.toContain('Browser (Playwright)')
    // browser group → Browser emulation sub-tab present.
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor groups={GROUPS} focusedIndex={1} onFocusChange={() => {}} overrides={[undefined, undefined]} onChange={() => {}} onApplyToGroups={() => {}} />,
      )
    })
    clickTab('HTTP Transport')
    expect(subTabLabels()).toContain('Browser (Playwright)')
  })

  it('previews the focused script action and its winning action headers', () => {
    const groups: RuntimeEditorGroup[] = [{
      name: 'Checkout',
      scriptId: 's1',
      protocol: 'http',
      scriptConfig: {
        base_url: '',
        http: {
          default_header_list: [{ name: 'X-Script', value: 'default' }],
        },
      },
      scriptActions: [{
        name: 'Create order',
        type: 'POST',
        target: 'https://orders.example.test/v1/orders',
        headers: { 'X-Action': 'create' },
      }],
    }]
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor groups={groups} focusedIndex={0} onFocusChange={() => {}} overrides={[undefined]} onChange={() => {}} onApplyToGroups={() => {}} />,
      )
    })

    clickTab('HTTP Transport')
    act(() => findButton('Show preview')?.dispatchEvent(new MouseEvent('click', { bubbles: true })))

    expect(container.querySelector<HTMLSelectElement>('.http-effective-preview select')?.value).toBe('0')
    expect(container.textContent).toContain('Create order')
    expect(container.textContent).toContain('https://orders.example.test/v1/orders')
    expect(container.textContent).toContain('X-Action')
    expect(container.textContent).toContain('Action override')
  })

  it('keeps continue_on_error undefined when editing sibling error_handling fields and preserves all 9 error_handling fields', () => {
    const onChange = vi.fn()
    const initialDraft = runtimeDraftFromOverride({
      error_handling: {
        fail_transaction_on_error_message: true,
        max_retries: 2,
        retry_delay_ms: 300,
        retry_backoff: 'exponential',
        retry_on: ['5xx', '429'],
        honor_retry_after: true,
        retry_after_max_ms: 8000,
      },
    })
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={[{ name: 'Checkout', scriptId: 's1', protocol: 'http', scriptConfig: { base_url: '' } }]}
          focusedIndex={0}
          onFocusChange={() => {}}
          overrides={[initialDraft]}
          engineDefaults={{
            base_url: '',
            error_handling: {
              continue_on_error: false,
              snapshot_on_error: false,
              max_retries: 0,
              retry_delay_ms: 1000,
              retry_backoff: 'fixed',
            },
          }}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('Error Handling')
    const continueCheckbox = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
      .find(cb => cb.closest('label')?.textContent?.includes('Continue on error'))
    expect(continueCheckbox?.checked).toBe(true)

    const snapshotCheckbox = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
      .find(cb => cb.closest('label')?.textContent?.includes('Snapshot on error'))
    expect(snapshotCheckbox).toBeTruthy()
    act(() => snapshotCheckbox!.dispatchEvent(new MouseEvent('click', { bubbles: true })))

    const written = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1]
    expect(written?.continue_on_error).toBeUndefined()
    expect(written?.snapshot_on_error).toBe(true)
    expect(overrideFromRuntimeDraft(written)?.error_handling).toEqual({
      snapshot_on_error: true,
      fail_transaction_on_error_message: true,
      max_retries: 2,
      retry_delay_ms: 300,
      retry_backoff: 'exponential',
      retry_on: ['5xx', '429'],
      honor_retry_after: true,
      retry_after_max_ms: 8000,
    })
  })

  it('Exchange capture > Default / inherit over a script value resets the path instead of listing an empty one', () => {
    const group: RuntimeEditorGroup = {
      name: 'Checkout',
      scriptId: 's1',
      protocol: 'http',
      scriptConfig: { base_url: '', results: { message_detail: { mode: 'sampled', sample_rate: 0.2 } } } as RuntimeEditorGroup['scriptConfig'],
    }
    let current: ScenarioRuntimeSettingsDraft | undefined
    const render = () => root.render(
      <RuntimeSettingsGroupEditor groups={[group]} focusedIndex={0} onFocusChange={() => {}} overrides={[current]} onChange={(_index, next) => { current = next; render() }} onApplyToGroups={() => {}} />,
    )
    act(() => render())
    clickTab('Error Handling')
    const capture = () => {
      const label = Array.from(container.querySelectorAll('label')).find(candidate => (candidate.textContent || '').startsWith('Exchange capture'))
      return label!.querySelector('select') as HTMLSelectElement
    }
    act(() => setSelectValue(capture(), 'errors'))
    expect(current?.override_fields).toContain('results.message_detail')
    act(() => setSelectValue(capture(), ''))
    expect(current?.override_fields ?? []).not.toContain('results.message_detail')
    expect(capture().value).toBe('sampled')
  })

  it('renders MessageDetailFields and PropertyAnalyticsFields when applicableProtocols is used without focusGroup.protocol', () => {
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={[{ name: 'Test overrides', scriptId: 'test-overrides' }]}
          focusedIndex={0}
          onFocusChange={() => {}}
          overrides={[undefined]}
          applicableProtocols={['http', 'browser', 'java_vuser']}
          onChange={() => {}}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('Error Handling')
    expect(container.textContent).toContain('Exchange capture')
    expect(container.textContent).toContain('Response property analytics')
  })

  it('aligns HTTP cache read precedence with simulate_cache ?? http.cache_enabled', () => {
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={[{
            name: 'Checkout',
            scriptId: 's1',
            protocol: 'http',
            scriptConfig: {
              base_url: '',
              browser_emulation: {
                mode: 'specific',
                browser_type: 'chrome',
                cache: { simulate_cache: true },
              },
            },
          }]}
          focusedIndex={0}
          onFocusChange={() => {}}
          overrides={[undefined]}
          engineDefaults={{ base_url: '', http: { cache_enabled: false } }}
          onChange={() => {}}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('HTTP Transport')
    const sessionCacheTab = Array.from(container.querySelectorAll('[role="tab"]'))
      .find(tab => (tab.textContent || '').startsWith('Session & Cache'))
    act(() => sessionCacheTab!.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    const cacheCheckbox = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
      .find(cb => cb.closest('label')?.textContent?.includes('Simulate HTTP cache'))
    expect(cacheCheckbox?.checked).toBe(true)
  })

  it('allows WebHttpTab content_checks.fail_if_found, case_insensitive, and data_formatting.auto_detect to be overridden from true to false', () => {
    const onChange = vi.fn()
    const initialDraft = runtimeDraftFromOverride({
      web_runtime: {
        content_checks: [{ text: 'error', case_insensitive: true }],
      },
    })
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          groups={[{
            name: 'Checkout',
            scriptId: 's1',
            protocol: 'http',
            scriptConfig: {
              base_url: '',
              web_runtime: {
                data_formatting: { auto_detect: true },
              },
            },
          }]}
          focusedIndex={0}
          onFocusChange={() => {}}
          overrides={[initialDraft]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    clickTab('Web/HTTP')
    const failIfFoundCheckbox = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
      .find(cb => cb.closest('label')?.textContent?.includes('Fail if found'))
    const caseInsensitiveCheckbox = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
      .find(cb => cb.closest('label')?.textContent?.includes('Case insensitive'))
    const autoDetectCheckbox = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
      .find(cb => cb.closest('label')?.textContent?.includes('Auto-detect formatting'))

    // Unset fail_if_found defaults to true (checked).
    expect(failIfFoundCheckbox?.checked).toBe(true)
    expect(caseInsensitiveCheckbox?.checked).toBe(true)
    expect(autoDetectCheckbox?.checked).toBe(true)

    act(() => failIfFoundCheckbox!.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    const afterFailIfFound = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1]
    expect(afterFailIfFound?.web_runtime?.content_checks?.[0]?.fail_if_found).toBe(false)

    act(() => caseInsensitiveCheckbox!.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    const afterCaseInsensitive = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1]
    expect(afterCaseInsensitive?.web_runtime?.content_checks?.[0]?.case_insensitive).toBe(false)

    act(() => autoDetectCheckbox!.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    const afterAutoDetect = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1]
    expect(afterAutoDetect?.web_runtime?.data_formatting?.auto_detect).toBe(false)
  })

  it('lists only the edited path at test level instead of writing an engine-default HTTP section', () => {
    const onReplace = vi.fn()
    act(() => {
      root.render(
        <CreateTestRuntimeOverrides
          runtimeSettings={{ log_level: '', auto_transactions: '' }}
          engineDefaults={{
            base_url: '',
            http: {
              version: '2',
              connect_timeout_ms: 5000,
              request_timeout_ms: 30000,
              max_conns_per_host: 100,
            },
          }}
          groupProtocols={['http']}
          onReplaceRuntimeSettings={onReplace}
        />,
      )
    })
    clickTab('HTTP Transport')
    clickTab('Timeouts')
    const connectInput = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="number"]'))
      .find(input => input.closest('label')?.textContent?.includes('Connect timeout'))
    expect(connectInput).toBeTruthy()
    act(() => setInputValue(connectInput!, '2000'))

    const next = onReplace.mock.calls[onReplace.mock.calls.length - 1]?.[0]
    // A whole engine-default http section here would replace every group's
    // script HTTP settings; the edit is one listed leaf instead.
    expect(next?.http).toEqual({ connect_timeout_ms: 2000 })
    expect(buildRuntimeSettingsPayload(next)).toEqual({
      http: { connect_timeout_ms: 2000 },
      override_fields: ['http.connect_timeout_ms'],
    })
    // The engine default still shows as the inherited placeholder.
    expect(Array.from(container.querySelectorAll<HTMLInputElement>('input[type="number"]'))
      .find(input => input.closest('label')?.textContent?.includes('Request timeout'))?.placeholder).toContain('30000')
  })

  it('emits one listed path per edit on the scenario-defaults mount, never an engine-default section', () => {
    const onChange = vi.fn()
    const engineDefaults = {
      base_url: '',
      websocket: { callback_interval_ms: 500, read_timeout_ms: 30000 },
      error_handling: { continue_on_error: false, max_retries: 0, retry_delay_ms: 1000 },
      http: { request_timeout_ms: 60000, connect_timeout_ms: 30000 },
    }
    const render = (override?: ScenarioRuntimeSettingsDraft) => act(() => {
      root.render(
        <RuntimeSettingsGroupEditor
          title="Scenario runtime defaults"
          overrideLabel="Scenario default"
          hideApplyPanel
          groups={[{ name: 'Scenario defaults', scriptId: 'scenario-defaults' }]}
          focusedIndex={0}
          onFocusChange={() => {}}
          engineDefaults={engineDefaults}
          applicableProtocols={['http', 'websocket']}
          overrides={[override]}
          onChange={onChange}
          onApplyToGroups={() => {}}
        />,
      )
    })
    render()
    clickTab('Error Handling')
    const callback = findLabeledControl<HTMLInputElement>('WebSocket callback interval (ms)', 'input')
    act(() => setInputValue(callback, '250'))
    let last = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1] as ScenarioRuntimeSettingsDraft
    expect(buildRuntimeSettingsPayload(last)).toEqual({ websocket: { callback_interval_ms: 250 }, override_fields: ['websocket.callback_interval_ms'] })

    render(last)
    clickTab('Error Handling')
    const continueOnError = Array.from(container.querySelectorAll('label')).find(label =>
      (label.textContent || '').includes('Continue on error'),
    )?.querySelector('input[type="checkbox"]') as HTMLInputElement
    act(() => continueOnError.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    last = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1] as ScenarioRuntimeSettingsDraft
    // The scripts' retries, delays and the rest of each section keep applying.
    expect(buildRuntimeSettingsPayload(last)).toEqual({
      websocket: { callback_interval_ms: 250 },
      error_handling: { continue_on_error: false },
      override_fields: ['websocket.callback_interval_ms', 'error_handling.continue_on_error'],
    })

    // Clear on the summary row resets just that row.
    render(last)
    clickTab('Summary')
    const clear = container.querySelector('button[aria-label="Clear Error handling override"]') as HTMLButtonElement
    act(() => clear.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    last = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1] as ScenarioRuntimeSettingsDraft
    expect(buildRuntimeSettingsPayload(last)).toEqual({ websocket: { callback_interval_ms: 250 }, override_fields: ['websocket.callback_interval_ms'] })
  })

  it('keeps a stored masked credential listed when a sibling HTTP field is edited', () => {
    const onChange = vi.fn()
    const stored = runtimeDraftFromOverride({
      http: { auth: { type: 'basic', username: '[REDACTED]', password: '[REDACTED]' } },
      override_fields: ['http.auth'],
    })
    act(() => {
      root.render(
        <RuntimeSettingsGroupEditor groups={GROUPS} focusedIndex={0} onFocusChange={() => {}} overrides={[stored, undefined]} onChange={onChange} onApplyToGroups={() => {}} />,
      )
    })
    clickTab('HTTP Transport')
    clickTab('Timeouts')
    act(() => setInputValue(findLabeledControl<HTMLInputElement>('Request timeout (ms)', 'input'), '5000'))
    const last = onChange.mock.calls[onChange.mock.calls.length - 1]?.[1] as ScenarioRuntimeSettingsDraft
    // "[REDACTED]" is sent at its listed path, so the backend restores the stored value.
    expect(buildRuntimeSettingsPayload(last)).toEqual({
      http: { auth: { type: 'basic', username: '[REDACTED]', password: '[REDACTED]' }, request_timeout_ms: 5000 },
      override_fields: ['http.auth', 'http.request_timeout_ms'],
    })
  })
})
