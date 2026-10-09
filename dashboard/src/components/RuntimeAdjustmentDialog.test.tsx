// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { TestSummary } from '../types'
import RuntimeAdjustmentDialog from './RuntimeAdjustmentDialog'
import { proportionalVUserTargets, runtimeAdjustmentTargets } from './runtimeAdjustmentModel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const testRun: TestSummary = {
  id: 'run-1', name: 'Checkout', status: 'RUNNING', total_vusers: 12, duration: '10m', created_at: '',
  groups: [
    { name: 'Buyers', script_id: 's1', script_name: 'Buyers', protocol: 'http', vusers: 10 },
    { name: 'Observer', script_id: 's2', script_name: 'Observer', protocol: 'http', role: 'observer', vusers: 2 },
  ],
}

describe('RuntimeAdjustmentDialog', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    vi.restoreAllMocks()
    document.querySelector('.runtime-adjustment-backdrop')?.remove()
    container.remove()
  })

  async function click(label: string) {
    const button = Array.from(document.querySelectorAll('button')).find(candidate => candidate.textContent?.includes(label))
    if (!button) throw new Error(`button not found: ${label}`)
    await act(async () => { button.click(); await Promise.resolve(); await Promise.resolve() })
  }

  it('previews selected group changes before enabling apply', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({
      test_id: 'run-1', decision: 'allowed', revision: 'rev-1', conflicts: [],
      impact: { scenario_duration: '15m0s', scenario_end_at: '2026-08-18T15:00:00Z', peak_vusers: 12, recommended_generators: 1 },
      groups: [
        { name: 'Buyers', current_duration: '10m0s', target_duration: '15m0s', current_vusers: 10, target_vusers: 10, runtime_state: 'running', effective_end_at: '2026-08-18T15:00:00Z' },
        { name: 'Observer', current_duration: '10m0s', target_duration: '15m0s', current_vusers: 2, target_vusers: 2, runtime_state: 'running', effective_end_at: '2026-08-18T15:00:00Z' },
      ],
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))

    await act(async () => root.render(<RuntimeAdjustmentDialog test={testRun} onClose={() => {}} onApplied={() => {}} />))
    await click('Stage duration')
    expect((document.querySelector('[aria-label="Buyers target duration"]') as HTMLInputElement).value).toBe('15m')
    expect((document.querySelector('[aria-label="Observer target duration"]') as HTMLInputElement).value).toBe('15m')

    const applyButton = Array.from(document.querySelectorAll('button')).find(button => button.textContent === 'Apply changes') as HTMLButtonElement
    expect(applyButton.disabled).toBe(true)
    await click('Review changes')
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(document.body.textContent).toContain('Ready to apply')
    expect(applyButton.disabled).toBe(false)
  })

  it('does not bulk-scale observer groups', async () => {
    await act(async () => root.render(<RuntimeAdjustmentDialog test={testRun} onClose={() => {}} onApplied={() => {}} />))
    await click('Stage load')
    expect((document.querySelector('[aria-label="Buyers target VUsers"]') as HTMLInputElement).value).toBe('11')
    expect((document.querySelector('[aria-label="Observer target VUsers"]') as HTMLInputElement).value).toBe('2')
  })

  it('rounds scenario-wide load changes deterministically', () => {
    const targets = proportionalVUserTargets([
      { name: 'Bravo', selected: true, vusers: 3 },
      { name: 'Alpha', selected: true, vusers: 3 },
    ], 1.5)
    expect(targets.get('Alpha')).toBe(5)
    expect(targets.get('Bravo')).toBe(4)
  })

  it('does not allow preview with fractional or out-of-range VUser targets', async () => {
    await act(async () => root.render(<RuntimeAdjustmentDialog test={testRun} onClose={() => {}} onApplied={() => {}} />))
    const target = document.querySelector('[aria-label="Buyers target VUsers"]') as HTMLInputElement
    const review = Array.from(document.querySelectorAll('button')).find(button => button.textContent === 'Review changes') as HTMLButtonElement
    const setNativeValue = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set

    await act(async () => {
      setNativeValue?.call(target, '1.5')
      target.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(review.disabled).toBe(true)

    expect(document.body.textContent).toContain('Fix the highlighted group values before reviewing.')

    await act(async () => {
      setNativeValue?.call(target, '10')
      target.dispatchEvent(new Event('input', { bubbles: true }))
    })
    // 10 is the running target: nothing would change, and the dialog says so.
    expect(review.disabled).toBe(true)
    expect(document.body.textContent).toContain('Nothing to change yet')

    await act(async () => {
      setNativeValue?.call(target, '12')
      target.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(review.disabled).toBe(false)
  })

  it('explains an invalid staged change next to its field', async () => {
    await act(async () => root.render(<RuntimeAdjustmentDialog test={testRun} onClose={() => {}} onApplied={() => {}} />))
    const amount = document.querySelector('[aria-label="Duration change amount"]') as HTMLInputElement
    const setNativeValue = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
    await act(async () => {
      setNativeValue?.call(amount, 'soon')
      amount.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(amount.getAttribute('aria-invalid')).toBe('true')
    expect(document.getElementById(amount.getAttribute('aria-describedby') || '')?.textContent).toContain('Enter a duration')
    expect(Array.from(document.querySelectorAll('button')).find(button => button.textContent === 'Stage duration')?.disabled).toBe(true)
  })

  it('refreshes control targets from applied VUser and TPS results', () => {
    expect(runtimeAdjustmentTargets([
      { name: 'Buyers', target_vusers: 2 },
      { name: 'Goal', target_vusers: 8, target_tps: 12.5 },
    ])).toEqual({ Buyers: 2, Goal: 12.5 })
  })
})
