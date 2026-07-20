// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import CreateTestGoalSettings from './CreateTestGoalSettings'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('CreateTestGoalSettings', () => {
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
  })

  it('renders goal telemetry controls and dispatches updates', async () => {
    const onGoalConfigChange = vi.fn()

    await act(async () => {
      root.render(
        <CreateTestGoalSettings
          goalConfig={{
            target_average_tps: 300,
            max_error_count: 8,
            stop_on_max_errors: false,
            sla_breach_window: '30s',
          }}
          onGoalConfigChange={onGoalConfigChange}
        />,
      )
    })

    const maxErrorCountInput = container.querySelector('input[placeholder="e.g. 25"]') as HTMLInputElement | null
    const serverProcessingInput = container.querySelector('input[placeholder="e.g. 450"]') as HTMLInputElement | null
    const transferInput = container.querySelector('input[placeholder="e.g. 120"]') as HTMLInputElement | null
    expect(maxErrorCountInput).not.toBeNull()
    expect(serverProcessingInput).not.toBeNull()
    expect(transferInput).not.toBeNull()
    expect(maxErrorCountInput?.value).toBe('8')

    const checkboxes = Array.from(container.querySelectorAll('input[type="checkbox"]')) as HTMLInputElement[]
    expect(checkboxes).toHaveLength(2)
    expect(checkboxes[1]?.checked).toBe(false)

    if (!maxErrorCountInput || !serverProcessingInput || !transferInput || !checkboxes[1]) {
      return
    }

    await act(async () => {
      const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      valueSetter?.call(serverProcessingInput, '450')
      serverProcessingInput.dispatchEvent(new Event('input', { bubbles: true }))
      serverProcessingInput.dispatchEvent(new Event('change', { bubbles: true }))
      valueSetter?.call(transferInput, '120')
      transferInput.dispatchEvent(new Event('input', { bubbles: true }))
      transferInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    await act(async () => {
      const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      valueSetter?.call(maxErrorCountInput, '15')
      maxErrorCountInput.dispatchEvent(new Event('input', { bubbles: true }))
      maxErrorCountInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    await act(async () => {
      checkboxes[1].dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onGoalConfigChange).toHaveBeenCalledWith('max_avg_server_processing_ms', 450)
    expect(onGoalConfigChange).toHaveBeenCalledWith('max_avg_transfer_ms', 120)
    expect(onGoalConfigChange).toHaveBeenCalledWith('max_error_count', 15)
    expect(onGoalConfigChange).toHaveBeenCalledWith('stop_on_max_errors', true)
  })

  it('renders Web Vitals thresholds and dispatches updates', async () => {
    const onGoalConfigChange = vi.fn()

    await act(async () => {
      root.render(
        <CreateTestGoalSettings
          goalConfig={{ max_lcp_ms: 2500, max_cls: 0.1 }}
          onGoalConfigChange={onGoalConfigChange}
        />,
      )
    })

    const lcpInput = container.querySelector('input[placeholder="e.g. 2500"]') as HTMLInputElement | null
    const inpInput = container.querySelector('input[placeholder="e.g. 200"]') as HTMLInputElement | null
    const clsInput = container.querySelector('input[placeholder="e.g. 0.1"]') as HTMLInputElement | null
    expect(lcpInput).not.toBeNull()
    expect(inpInput).not.toBeNull()
    expect(clsInput).not.toBeNull()
    expect(lcpInput?.value).toBe('2500')
    expect(clsInput?.value).toBe('0.1')
    if (!lcpInput || !inpInput || !clsInput) return

    await act(async () => {
      const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      valueSetter?.call(inpInput, '200')
      inpInput.dispatchEvent(new Event('input', { bubbles: true }))
      inpInput.dispatchEvent(new Event('change', { bubbles: true }))
      valueSetter?.call(clsInput, '0.25')
      clsInput.dispatchEvent(new Event('input', { bubbles: true }))
      clsInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(onGoalConfigChange).toHaveBeenCalledWith('max_inp_ms', 200)
    expect(onGoalConfigChange).toHaveBeenCalledWith('max_cls', 0.25)

    // Clearing a vitals threshold removes it.
    await act(async () => {
      const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      valueSetter?.call(lcpInput, '')
      lcpInput.dispatchEvent(new Event('input', { bubbles: true }))
      lcpInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(onGoalConfigChange).toHaveBeenCalledWith('max_lcp_ms', undefined)
  })

  it('renders valid zero thresholds and edits all messaging guardrails', async () => {
    const onGoalConfigChange = vi.fn()

    await act(async () => {
      root.render(
        <CreateTestGoalSettings
          goalConfig={{
            max_error_pct: 0,
            max_throttled_requests: 0,
            max_partition_skew_pct: 0,
            max_redelivery_rate_pct: 0,
            max_dead_letter_rate_pct: 0,
          }}
          onGoalConfigChange={onGoalConfigChange}
        />,
      )
    })

    const inputFor = (label: string) => Array.from(container.querySelectorAll('label'))
      .find(candidate => candidate.querySelector('span')?.textContent === label)
      ?.querySelector('input[type="number"]') as HTMLInputElement | null

    expect(inputFor('Max Error %')?.value).toBe('0')
    expect(inputFor('Max Throttled Requests')?.value).toBe('0')
    expect(inputFor('Max Partition Skew %')?.value).toBe('0')
    expect(inputFor('Max Redelivery Rate %')?.value).toBe('0')
    expect(inputFor('Max Dead-Letter Rate %')?.value).toBe('0')
    expect(inputFor('Max Error Count')?.step).toBe('1')
    expect(inputFor('Min VUsers')?.step).toBe('1')
    expect(inputFor('Max Throttled Requests')?.step).toBe('1')
    expect(inputFor('Target Avg TPS')?.step).toBe('any')
    expect(inputFor('P95 Latency Ceiling (ms)')?.step).toBe('any')
    expect(inputFor('Max CLS')?.step).toBe('any')
    expect(inputFor('Max End-to-End Latency (ms)')?.step).toBe('any')

    const edits = [
      ['Max Throttled Requests', '2', 'max_throttled_requests', 2],
      ['Max End-to-End Latency (ms)', '500', 'max_end_to_end_latency_ms', 500],
      ['Max Partition Skew %', '10', 'max_partition_skew_pct', 10],
      ['Max Redelivery Rate %', '1.5', 'max_redelivery_rate_pct', 1.5],
      ['Max Dead-Letter Rate %', '0.5', 'max_dead_letter_rate_pct', 0.5],
    ] as const

    await act(async () => {
      const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      for (const [label, value] of edits) {
        const input = inputFor(label)
        expect(input).not.toBeNull()
        if (!input) continue
        valueSetter?.call(input, value)
        input.dispatchEvent(new Event('input', { bubbles: true }))
        input.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })

    for (const [, , field, value] of edits) {
      expect(onGoalConfigChange).toHaveBeenCalledWith(field, value)
    }
  })
})
