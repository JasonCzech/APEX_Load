// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import GroupRuntimeSettingsDialog from './GroupRuntimeSettingsDialog'
import {
  changedRuntimeFieldKeys,
  effectiveRuntimeFieldValue,
} from './groupRuntimeSettingsModel'
import type { RuntimeSettingsOverride } from '../types/runtimeSettings'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('changedRuntimeFieldKeys', () => {
  it('flags only fields whose effective value changed', () => {
    const before: RuntimeSettingsOverride = {
      pacing: { type: 'fixed_delay', delay_ms: 500 },
      think_time: { type: 'fixed', mean_ms: 100 },
    }
    const after: RuntimeSettingsOverride = {
      pacing: { type: 'fixed_delay', delay_ms: 2000 },
      think_time: { type: 'fixed', mean_ms: 100 },
    }
    expect(changedRuntimeFieldKeys(before, after)).toEqual(['pacing'])
  })

  it('resolves legacy aliases so a canonical section move is not a false change', () => {
    const before: RuntimeSettingsOverride = { log_level: 'debug' }
    const after: RuntimeSettingsOverride = { log: { level: 'debug' } }
    // Same effective log level; the log section itself appeared, which is a real
    // "log" change but must not flag log_level.
    const changed = changedRuntimeFieldKeys(before, after)
    expect(changed).not.toContain('log_level')
    expect(changed).toContain('log')
  })

  it('treats clearing an override as a change', () => {
    const before: RuntimeSettingsOverride = { base_url: 'https://a.example' }
    expect(changedRuntimeFieldKeys(before, {})).toEqual(['base_url'])
    expect(changedRuntimeFieldKeys(undefined, undefined)).toEqual([])
  })

  it('resolves iteration aliases with the section winning', () => {
    expect(effectiveRuntimeFieldValue({ iterations: 4 }, 'iterations')).toBe(4)
    expect(effectiveRuntimeFieldValue({ iterations: 4, run_logic: { iterations: 9 } }, 'iterations')).toBe(9)
    // A present section wins outright even when it leaves the aliased field
    // unset — execution ignores the legacy alias in that case.
    expect(effectiveRuntimeFieldValue({ iterations: 4, run_logic: { auto_transactions: 'tx' } }, 'iterations')).toBeUndefined()
    expect(effectiveRuntimeFieldValue({ log_level: 'debug', log: {} }, 'log_level')).toBeUndefined()
    expect(effectiveRuntimeFieldValue({ browser_config: { headless: true } }, 'browser')).toEqual({ headless: true })
  })
})

describe('GroupRuntimeSettingsDialog', () => {
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
    vi.restoreAllMocks()
    container.remove()
  })

  function jsonResponse(body: unknown) {
    return new Response(JSON.stringify(body), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }

  function requestUrl(input: RequestInfo | URL) {
    if (typeof input === 'string') return input
    if (input instanceof URL) return input.toString()
    return input.url
  }

  async function flush(times = 5) {
    for (let i = 0; i < times; i++) {
      await act(async () => {
        await Promise.resolve()
      })
    }
  }

  it('renders inherited script Results and submits an explicit off/false/zero replacement with untouched result siblings', async () => {
    const inherited = { message_detail: { mode: 'errors', include_resources: true, max_items: 128 }, property_analytics: [{ name: 'category', extraction: 'category', values: ['one'] }], csv_exports: [{ id: 'csv', file_name: 'detail.csv', action_name: 'Home', columns: [{ name: 'latency', metric: 'latency_ms' }] }] }
    let patchBody: { runtime_settings: RuntimeSettingsOverride; fields: string[]; expected_revision: string } | undefined
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = requestUrl(input)
      if (url.includes('/runtime-settings') && init?.method === 'PATCH') { patchBody = JSON.parse(String(init.body)); return jsonResponse({}) }
      if (url.includes('/runtime-settings')) return jsonResponse({ test_id: 'results-run', group: 'HTTP', group_index: 0, protocol: 'http', status: 'QUEUED', editable: true, revision: 'sha256:results-observed', editable_fields: ['results'], script_settings: { results: inherited }, runtime_settings: {}, effective_settings: { results: inherited } })
      if (url.includes('/dsl/config-defaults')) return jsonResponse({ config: {} })
      return jsonResponse({})
    })
    await act(async () => root.render(<GroupRuntimeSettingsDialog testId="results-run" groupName="HTTP" onClose={() => {}} onSaved={() => {}} />))
    await flush()
    const dialog = document.querySelector('.group-run-settings-dialog') as HTMLElement
    const errorTab = Array.from(dialog.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Error Handling') as HTMLButtonElement
    await act(async () => errorTab.click())
    const field = (name: string) => Array.from(dialog.querySelectorAll('label')).find(label => label.textContent?.includes(name))!
    const capture = field('Exchange capture').querySelector('select')!
    expect(capture.value).toBe('errors')
    await act(async () => { capture.value = 'off'; capture.dispatchEvent(new Event('change', { bubbles: true })) })
    const resources = field('Include fetched resources').querySelector('select')!
    await act(async () => { resources.value = 'false'; resources.dispatchEvent(new Event('change', { bubbles: true })) })
    const limit = field('Retained exchange limit').querySelector('input')!
    await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(limit, '0'); limit.dispatchEvent(new Event('input', { bubbles: true })); limit.dispatchEvent(new Event('change', { bubbles: true })) })
    const save = Array.from(dialog.querySelectorAll('button')).find(button => button.textContent?.includes('Save to run'))!
    await act(async () => save.click())
    await flush()
    expect(patchBody?.expected_revision).toBe('sha256:results-observed')
    expect(patchBody?.fields).toEqual(['results'])
    expect(patchBody?.runtime_settings.results).toEqual({ ...inherited, message_detail: { mode: 'off', include_resources: false, max_items: 0 } })
    expect(inherited.message_detail).toEqual({ mode: 'errors', include_resources: true, max_items: 128 })
  })

  it.each(['zero replacement', 'explicit clear'])('submits WebSocket %s from the real group dialog without losing false or sibling settings', async operation => {
    const original = { callback_interval_ms: 500, read_timeout_ms: 100, compression: false, reconnect: false }
    let patchBody: { runtime_settings: RuntimeSettingsOverride; fields: string[] } | undefined
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = requestUrl(input)
      if (url.includes('/runtime-settings') && init?.method === 'PATCH') { patchBody = JSON.parse(String(init.body)); return jsonResponse({}) }
      if (url.includes('/runtime-settings')) return jsonResponse({ test_id: 'ws-run', group: 'WS', group_index: 0, protocol: 'websocket', status: 'QUEUED', editable: true, revision: 'sha256:ws-observed', editable_fields: ['websocket'], script_settings: { websocket: { callback_interval_ms: 0 } }, runtime_settings: { websocket: original }, effective_settings: { websocket: original } })
      if (url.includes('/dsl/config-defaults')) return jsonResponse({ config: {} })
      return jsonResponse({})
    })
    await act(async () => root.render(<GroupRuntimeSettingsDialog testId="ws-run" groupName="WS" onClose={() => {}} onSaved={() => {}} />))
    await flush()
    const dialog = document.querySelector('.group-run-settings-dialog') as HTMLElement
    const callback = Array.from(dialog.querySelectorAll('label')).find(label => label.textContent?.includes('WebSocket callback interval (ms)'))!.querySelector('input')!
    expect(callback.placeholder).toBe('0')
    if (operation === 'explicit clear') {
      const clear = dialog.querySelector<HTMLButtonElement>('button[aria-label="Clear WebSocket override"]')!
      expect(clear).not.toBeNull()
      await act(async () => clear.click())
    } else {
      const input = Array.from(dialog.querySelectorAll('label')).find(label => label.textContent?.includes('WebSocket callback interval (ms)'))!.querySelector('input')!
      expect(input.value).toBe('500')
      await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, '0'); input.dispatchEvent(new Event('input', { bubbles: true })); input.dispatchEvent(new Event('change', { bubbles: true })) })
      expect(input.validity.valid).toBe(true)
    }
    const save = Array.from(dialog.querySelectorAll('button')).find(button => button.textContent?.includes('Save to run'))!
    await act(async () => save.click())
    await flush()
    expect(patchBody?.fields).toEqual(['websocket'])
    expect(patchBody?.runtime_settings.websocket).toEqual(operation === 'explicit clear' ? undefined : { ...original, callback_interval_ms: 0 })
    expect(original.callback_interval_ms).toBe(500)
    expect(JSON.stringify(patchBody)).not.toContain('null')
  })

  it('renders the editable group editor for a queued run and skips saving when nothing changed', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = requestUrl(input)
      if (url.includes('/runtime-settings')) {
        return jsonResponse({
          test_id: 'queued-1',
          group: 'Buyers',
          group_index: 0,
          script_name: 'Checkout',
          protocol: 'http',
          status: 'QUEUED',
          editable: true,
          revision: 'sha256:runtime-v1',
          editable_fields: ['pacing', 'think_time', 'java_vuser'],
          runtime_settings: { pacing: { type: 'fixed_delay', delay_ms: 500 } },
          test_runtime_settings: { think_time: { type: 'fixed', mean_ms: 250 } },
          effective_settings: { pacing: { type: 'fixed_delay', delay_ms: 500 } },
        })
      }
      if (url.includes('/dsl/config-defaults')) return jsonResponse({ config: {} })
      return jsonResponse({})
    })
    const onSaved = vi.fn()
    await act(async () => {
      root.render(
        <GroupRuntimeSettingsDialog testId="queued-1" groupName="Buyers" onClose={() => {}} onSaved={onSaved} />,
      )
    })
    await flush()

    const dialog = document.querySelector('.group-run-settings-dialog') as HTMLElement
    expect(dialog.textContent).toContain('Runtime Settings — Buyers')
    expect(dialog.textContent).not.toContain('Read-only')
    // The per-group runtime editor is mounted in editable mode with the
    // single-target framing: no cross-group apply affordance, no focus picker.
    expect(dialog.querySelector('.runtime-override-tabs')).not.toBeNull()
    expect(dialog.textContent).toContain('Run overrides')
    expect(dialog.querySelector('.runtime-apply-row')).toBeNull()
    expect(dialog.querySelector('select[aria-label="Focus group"]')).toBeNull()

    const saveButton = Array.from(dialog.querySelectorAll('button')).find(button => button.textContent?.includes('Save to run')) as HTMLButtonElement
    expect(saveButton).toBeDefined()
    const callsBeforeSave = fetchMock.mock.calls.length
    await act(async () => {
      saveButton.click()
    })
    await flush()

    // No field changed: the dialog reports it and never issues the PATCH.
    expect(dialog.textContent).toContain('No changes to save.')
    expect(fetchMock.mock.calls.length).toBe(callsBeforeSave)
    expect(onSaved).not.toHaveBeenCalled()

    // The view carries no script_id, so no script fetch happens and the
    // imported-source panel is absent.
    expect(dialog.querySelector('.group-run-settings-imported')).toBeNull()
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes('/scripts/'))).toBe(false)
  })

  it('does not flag phantom changes from draft materialization on an untouched save', async () => {
    // A partial error-handling section (only max_retries) materializes explicit
    // continue_on_error/snapshot_on_error=false in the draft; the baseline must
    // round-trip through the same transform so an untouched save stays a no-op.
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = requestUrl(input)
      if (url.includes('/runtime-settings')) {
        return jsonResponse({
          test_id: 'queued-2',
          group: 'Buyers',
          group_index: 0,
          protocol: 'http',
          status: 'QUEUED',
          editable: true,
          revision: 'sha256:runtime-v1',
          editable_fields: ['error_handling', 'iterations'],
          runtime_settings: { error_handling: { max_retries: 3 }, iterations: 4, run_logic: { auto_transactions: 'tx' } },
          effective_settings: { error_handling: { max_retries: 3 } },
        })
      }
      if (url.includes('/dsl/config-defaults')) return jsonResponse({ config: {} })
      return jsonResponse({})
    })
    const onSaved = vi.fn()
    await act(async () => {
      root.render(
        <GroupRuntimeSettingsDialog testId="queued-2" groupName="Buyers" onClose={() => {}} onSaved={onSaved} />,
      )
    })
    await flush()

    const dialog = document.querySelector('.group-run-settings-dialog') as HTMLElement
    const saveButton = Array.from(dialog.querySelectorAll('button')).find(button => button.textContent?.includes('Save to run')) as HTMLButtonElement
    const callsBeforeSave = fetchMock.mock.calls.length
    await act(async () => {
      saveButton.click()
    })
    await flush()

    expect(dialog.textContent).toContain('No changes to save.')
    expect(fetchMock.mock.calls.length).toBe(callsBeforeSave)
    expect(onSaved).not.toHaveBeenCalled()
  })

  it('keeps the draft and error visible when a save is rejected', async () => {
    let patchCount = 0
    let patchBody: Record<string, unknown> | undefined
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = requestUrl(input)
      if (url.includes('/runtime-settings') && init?.method === 'PATCH') {
        patchCount++
        patchBody = JSON.parse(String(init.body))
        return new Response(JSON.stringify({ error: 'runtime settings blocked: open workload requires TPS pacing' }), {
          status: 400,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      if (url.includes('/runtime-settings')) {
        return jsonResponse({
          test_id: 'queued-3',
          group: 'Buyers',
          group_index: 0,
          protocol: 'http',
          status: 'QUEUED',
          editable: true,
          revision: 'sha256:runtime-v1',
          editable_fields: ['pacing'],
          runtime_settings: { pacing: { type: 'fixed_delay', delay_ms: 500 } },
          effective_settings: { pacing: { type: 'fixed_delay', delay_ms: 500 } },
        })
      }
      if (url.includes('/dsl/config-defaults')) return jsonResponse({ config: {} })
      return jsonResponse({})
    })
    await act(async () => {
      root.render(
        <GroupRuntimeSettingsDialog testId="queued-3" groupName="Buyers" onClose={() => {}} onSaved={() => {}} />,
      )
    })
    await flush()

    const dialog = document.querySelector('.group-run-settings-dialog') as HTMLElement
    // Edit pacing through the editor's Iteration Pacing tab so the draft dirties.
    const pacingTab = Array.from(dialog.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Iteration Pacing') as HTMLButtonElement
    await act(async () => {
      pacingTab.click()
    })
    const delayInput = Array.from(dialog.querySelectorAll('label'))
      .find(label => label.textContent?.includes('Pacing delay (seconds)'))
      ?.querySelector('input') as HTMLInputElement | undefined
    expect(delayInput).toBeDefined()
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      setter?.call(delayInput, '9')
      delayInput?.dispatchEvent(new Event('input', { bubbles: true }))
      delayInput?.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const saveButton = Array.from(dialog.querySelectorAll('button')).find(button => button.textContent?.includes('Save to run')) as HTMLButtonElement
    await act(async () => {
      saveButton.click()
    })
    await flush()

    expect(patchCount).toBe(1)
    expect(patchBody?.expected_revision).toBe('sha256:runtime-v1')
    // The rejection is shown, the editor stays mounted, and the edited value survives.
    expect(dialog.textContent).toContain('runtime settings blocked')
    expect(dialog.querySelector('.runtime-override-tabs')).not.toBeNull()
    const survivingInput = Array.from(dialog.querySelectorAll('label'))
      .find(label => label.textContent?.includes('Pacing delay (seconds)'))
      ?.querySelector('input') as HTMLInputElement | undefined
    expect(survivingInput?.value).toBe('9')
  })

  it('turns a newer post-error refresh revision into an explicit conflict', async () => {
    let getCount = 0
    const patchBodies: Array<{ expected_revision?: string }> = []
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = requestUrl(input)
      if (url.includes('/runtime-settings') && init?.method === 'PATCH') {
        patchBodies.push(JSON.parse(String(init.body)))
        return new Response(JSON.stringify({ error: 'runtime settings blocked: revise pacing' }), {
          status: 400,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      if (url.includes('/runtime-settings')) {
        getCount++
        return jsonResponse({
          test_id: 'queued-refresh-conflict',
          group: 'Buyers',
          group_index: 0,
          protocol: 'http',
          status: 'QUEUED',
          editable: true,
          revision: getCount === 1 ? 'sha256:runtime-old' : 'sha256:runtime-new',
          editable_fields: ['pacing'],
          runtime_settings: { pacing: { type: 'fixed_delay', delay_ms: getCount === 1 ? 500 : 700 } },
          effective_settings: { pacing: { type: 'fixed_delay', delay_ms: getCount === 1 ? 500 : 700 } },
        })
      }
      if (url.includes('/dsl/config-defaults')) return jsonResponse({ config: {} })
      return jsonResponse({})
    })
    await act(async () => {
      root.render(
        <GroupRuntimeSettingsDialog testId="queued-refresh-conflict" groupName="Buyers" onClose={() => {}} onSaved={() => {}} />,
      )
    })
    await flush()

    const dialog = document.querySelector('.group-run-settings-dialog') as HTMLElement
    const pacingTab = Array.from(dialog.querySelectorAll('button'))
      .find(button => button.textContent?.trim() === 'Iteration Pacing') as HTMLButtonElement
    await act(async () => {
      pacingTab.click()
    })
    const delayInput = Array.from(dialog.querySelectorAll('label'))
      .find(label => label.textContent?.includes('Pacing delay (seconds)'))
      ?.querySelector('input') as HTMLInputElement
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      setter?.call(delayInput, '9')
      delayInput.dispatchEvent(new Event('input', { bubbles: true }))
      delayInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const saveButton = Array.from(dialog.querySelectorAll('button'))
      .find(button => button.textContent?.includes('Save to run')) as HTMLButtonElement
    await act(async () => {
      saveButton.click()
    })
    await flush()

    expect(patchBodies).toEqual([expect.objectContaining({ expected_revision: 'sha256:runtime-old' })])
    expect(getCount).toBe(2)
    expect(dialog.textContent).toContain('Runtime settings changed in another session')
    expect(saveButton.disabled).toBe(true)
    const survivingInput = Array.from(dialog.querySelectorAll('label'))
      .find(label => label.textContent?.includes('Pacing delay (seconds)'))
      ?.querySelector('input') as HTMLInputElement
    expect(survivingInput.value).toBe('9')
  })

  it('sends the shared revision and requires an explicit reload after a 409', async () => {
    let getCount = 0
    const patchBodies: Array<{ expected_revision?: string }> = []
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = requestUrl(input)
      if (url.includes('/runtime-settings') && init?.method === 'PATCH') {
        patchBodies.push(JSON.parse(String(init.body)))
        return new Response(JSON.stringify({ error: 'group settings changed since they were loaded' }), {
          status: 409,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      if (url.includes('/runtime-settings')) {
        getCount++
        return jsonResponse({
          test_id: 'queued-conflict',
          group: 'Buyers',
          group_index: 0,
          protocol: 'http',
          status: 'QUEUED',
          editable: true,
          revision: getCount === 1 ? 'sha256:stale' : 'sha256:fresh',
          editable_fields: ['pacing'],
          runtime_settings: { pacing: { type: 'fixed_delay', delay_ms: getCount === 1 ? 500 : 700 } },
          effective_settings: { pacing: { type: 'fixed_delay', delay_ms: getCount === 1 ? 500 : 700 } },
        })
      }
      if (url.includes('/dsl/config-defaults')) return jsonResponse({ config: {} })
      return jsonResponse({})
    })
    await act(async () => {
      root.render(
        <GroupRuntimeSettingsDialog testId="queued-conflict" groupName="Buyers" onClose={() => {}} onSaved={() => {}} />,
      )
    })
    await flush()

    const dialog = document.querySelector('.group-run-settings-dialog') as HTMLElement
    const pacingTab = Array.from(dialog.querySelectorAll('button'))
      .find(button => button.textContent?.trim() === 'Iteration Pacing') as HTMLButtonElement
    await act(async () => {
      pacingTab.click()
    })
    const delayInput = Array.from(dialog.querySelectorAll('label'))
      .find(label => label.textContent?.includes('Pacing delay (seconds)'))
      ?.querySelector('input') as HTMLInputElement
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      setter?.call(delayInput, '9')
      delayInput.dispatchEvent(new Event('input', { bubbles: true }))
      delayInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const saveButton = Array.from(dialog.querySelectorAll('button'))
      .find(button => button.textContent?.includes('Save to run')) as HTMLButtonElement
    await act(async () => {
      saveButton.click()
    })
    await flush()

    expect(patchBodies[0]?.expected_revision).toBe('sha256:stale')
    expect(getCount).toBe(1)
    expect(dialog.textContent).toContain('Runtime settings changed in another session')
    expect(saveButton.disabled).toBe(true)

    const reloadButton = Array.from(dialog.querySelectorAll('button'))
      .find(button => button.textContent?.includes('Reload settings')) as HTMLButtonElement
    await act(async () => {
      reloadButton.click()
    })
    await flush()
    expect(getCount).toBe(2)
    expect(dialog.textContent).not.toContain('Runtime settings changed in another session')
  })

  it('renders the read-only summary with layer sources once a run has started', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = requestUrl(input)
      if (url.includes('/runtime-settings')) {
        return jsonResponse({
          test_id: 'run-1',
          group: 'Buyers',
          group_index: 0,
          script_id: 'script-imported',
          protocol: 'http',
          status: 'RUNNING',
          editable: false,
          revision: 'sha256:runtime-readonly',
          editable_reason: 'run is RUNNING; settings were locked when the run left the pre-start queue',
          editable_fields: ['pacing'],
          runtime_settings: {
            pacing: { type: 'fixed_delay', delay_ms: 500 },
            run_logic: { auto_transactions: 'actions' },
          },
          test_runtime_settings: { think_time: { type: 'fixed', mean_ms: 250 }, iterations: 8 },
          effective_settings: {
            pacing: { type: 'fixed_delay', delay_ms: 500 },
            think_time: { type: 'fixed', mean_ms: 250 },
            run_logic: { auto_transactions: 'actions' },
            browser_config: { headless: false },
          },
          imported_runtime_source: {
            files: {
              'default.cfg': {
                ThinkTime: { Options: 'NOTHINK', ThinkTimeRandomLow: '50' },
              },
            },
            dispositions: {
              'default.cfg': {
                ThinkTime: {
                  Options: { status: 'mapped', parity_id: 'think_time', fields: ['config.think_time.ignore'] },
                  ThinkTimeRandomLow: { status: 'non_applicable', note: 'random bounds unused for NOTHINK' },
                },
              },
            },
          },
        })
      }
      if (url.includes('/dsl/config-defaults')) return jsonResponse({ config: {} })
      return jsonResponse({})
    })
    await act(async () => {
      root.render(
        <GroupRuntimeSettingsDialog testId="run-1" groupName="Buyers" onClose={() => {}} onSaved={() => {}} />,
      )
    })
    await flush()

    const dialog = document.querySelector('.group-run-settings-dialog') as HTMLElement
    expect(dialog.textContent).toContain('Read-only')
    expect(dialog.querySelector('.runtime-override-tabs')).toBeNull()
    const rows = Array.from(dialog.querySelectorAll('.group-run-settings-summary-row'))
    expect(rows.length).toBeGreaterThan(2)
    const pacingRow = rows.find(row => row.textContent?.includes('Pacing'))
    expect(pacingRow?.textContent).toContain('fixed_delay')
    expect(pacingRow?.textContent).toContain('Group override')
    const thinkTimeRow = rows.find(row => row.textContent?.includes('Think time'))
    expect(thinkTimeRow?.textContent).toContain('Scenario default')
    const iterationsRow = rows.find(row => row.textContent?.includes('Iterations'))
    expect(iterationsRow?.textContent).toContain('—')
    expect(iterationsRow?.textContent).toContain('Group override')
    const browserConfigRow = rows.find(row => row.textContent?.includes('Browser config'))
    expect(browserConfigRow?.textContent).toContain('headless: false')

    // The imported-source context panel renders the immutable run projection;
    // it must not fetch mutable library-script provenance.
    const imported = dialog.querySelector('.group-run-settings-imported') as HTMLElement
    expect(imported).not.toBeNull()
    expect(imported.textContent).toContain('default.cfg')
    expect(imported.textContent).toContain('NOTHINK')
    expect(imported.textContent).toContain('Mapped → config.think_time.ignore')
    expect(imported.textContent).toContain('Not applicable')
    expect(imported.textContent).toContain('1 mapped')
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes('/scripts/'))).toBe(false)
  })

  it('bounds the imported-source panel to the section cap with an overflow note', async () => {
    const manySections: Record<string, Record<string, string>> = {}
    for (let index = 0; index < 15; index++) {
      manySections[`Section${String(index).padStart(2, '0')}`] = { SomeKey: `value-${index}` }
    }
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = requestUrl(input)
      if (url.includes('/runtime-settings')) {
        return jsonResponse({
          test_id: 'run-2',
          group: 'Buyers',
          group_index: 0,
          script_id: 'script-wide',
          protocol: 'http',
          status: 'RUNNING',
          editable: false,
          revision: 'sha256:runtime-wide',
          editable_reason: 'run is RUNNING; settings were locked when the run left the pre-start queue',
          editable_fields: ['pacing'],
          effective_settings: {},
          imported_runtime_source: {
            files: { 'default.cfg': Object.fromEntries(Object.entries(manySections).slice(0, 12)) },
            dispositions: {},
            truncated: true,
          },
        })
      }
      if (url.includes('/dsl/config-defaults')) return jsonResponse({ config: {} })
      return jsonResponse({})
    })
    await act(async () => {
      root.render(
        <GroupRuntimeSettingsDialog testId="run-2" groupName="Buyers" onClose={() => {}} onSaved={() => {}} />,
      )
    })
    await flush()

    const imported = document.querySelector('.group-run-settings-imported') as HTMLElement
    expect(imported).not.toBeNull()
    expect(imported.querySelectorAll('table').length).toBe(12)
    expect(imported.textContent).toContain('immutable run snapshot is truncated')
  })
})
