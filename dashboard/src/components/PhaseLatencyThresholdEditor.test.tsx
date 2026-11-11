// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { DashboardPhaseLatencyThreshold } from '../types/settings'
import { thresholdAlertMetricLabel } from '../thresholdAlerts'
import PhaseLatencyThresholdEditor from './PhaseLatencyThresholdEditor'
import { validateGoalConfig } from './goalConfigValidation'
import { DEFAULT_SETTINGS, validatePhaseLatencyRules, validateSettingsDetailed } from './settingsPageModel'

describe('phase latency rule validation', () => {
  it('mirrors the backend rules', () => {
    expect(validatePhaseLatencyRules(undefined)).toEqual([])
    expect(validatePhaseLatencyRules([{ phase: 'connect', percentile: 95, max_ms: 500, stop_consecutive_samples: 60 }])).toEqual([])
    const issues = validatePhaseLatencyRules([
      { phase: 'connect', percentile: 95, max_ms: 0 },
      { phase: 'connect', percentile: 95, max_ms: 10, stop_consecutive_samples: 1.5 },
      { phase: 'ttfb' as DashboardPhaseLatencyThreshold['phase'], percentile: 75 as DashboardPhaseLatencyThreshold['percentile'], max_ms: Number.NaN },
    ])
    expect(issues.map(i => i.field)).toEqual([
      'thresholds.phase_latency.0.max_ms',
      'thresholds.phase_latency.1.stop_consecutive_samples',
      'thresholds.phase_latency.1.phase',
      'thresholds.phase_latency.2.phase',
      'thresholds.phase_latency.2.percentile',
      'thresholds.phase_latency.2.max_ms',
    ])
    const settings = { ...DEFAULT_SETTINGS, thresholds: { ...DEFAULT_SETTINGS.thresholds, phase_latency: [{ phase: 'tls' as const, percentile: 99 as const, max_ms: -1 }] } }
    expect(validateSettingsDetailed(settings).some(issue => issue.section === 'thresholds' && issue.field === 'thresholds.phase_latency.0.max_ms')).toBe(true)
  })

  it('validates goal-config phase rules like the server', () => {
    const rule = { transaction: 'Checkout', metric: 'p95_duration_ms' as const, comparator: 'lt' as const, threshold: 50 }
    expect(validateGoalConfig({ transaction_rules: [{ ...rule, phase: 'connect' }] })).toBeNull()
    expect(validateGoalConfig({ transaction_rules: [{ ...rule, phase: 'ttfb' as 'dns' }] })).toContain('only with a duration metric')
    expect(validateGoalConfig({ transaction_rules: [{ ...rule, metric: 'error_count', phase: 'tls' }] })).toContain('only with a duration metric')
  })

  it('labels phase latency alerts', () => {
    expect(thresholdAlertMetricLabel('phase_connect_p95_ms')).toBe('Connect P95 latency')
    expect(thresholdAlertMetricLabel('phase_tls_p99_ms')).toBe('TLS P99 latency')
    expect(thresholdAlertMetricLabel('phase_bogus_p95_ms')).toBe('phase_bogus_p95_ms')
  })
})

describe('PhaseLatencyThresholdEditor', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })

  it('adds, edits and removes rules', async () => {
    const onChange = vi.fn()
    await act(async () => root.render(<PhaseLatencyThresholdEditor rules={undefined} onChange={onChange} />))
    expect(container.textContent).toContain('No phase latency rules yet.')
    const add = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Add phase rule')!
    await act(async () => add.click())
    expect(onChange).toHaveBeenLastCalledWith([{ phase: 'dns', percentile: 95, max_ms: 500 }])

    const rules: DashboardPhaseLatencyThreshold[] = [{ phase: 'dns', percentile: 95, max_ms: 500 }]
    await act(async () => root.render(<PhaseLatencyThresholdEditor rules={rules} onChange={onChange} />))
    // The next rule picks an unused phase.
    await act(async () => Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Add phase rule')!.click())
    expect(onChange).toHaveBeenLastCalledWith([...rules, { phase: 'connect', percentile: 95, max_ms: 500 }])

    const phase = container.querySelector<HTMLSelectElement>('select[aria-label="Phase rule 1 phase"]')!
    const percentile = container.querySelector<HTMLSelectElement>('select[aria-label="Phase rule 1 percentile"]')!
    const maxMs = container.querySelector<HTMLInputElement>('input[aria-label="Phase rule 1 max (ms)"]')!
    expect(phase.classList.contains('settings-input')).toBe(true)
    expect(phase.classList.contains('settings-select')).toBe(true)
    expect(percentile.classList.contains('settings-input')).toBe(true)
    expect(percentile.classList.contains('settings-select')).toBe(true)
    for (const control of [phase, percentile, maxMs]) {
      expect(control.id).toBeTruthy()
      expect(container.querySelector(`label[for="${control.id}"]`)).toBe(control.closest('label'))
    }
    await act(async () => {
      phase.value = 'tls'
      phase.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(onChange).toHaveBeenLastCalledWith([{ phase: 'tls', percentile: 95, max_ms: 500 }])

    const stop = container.querySelector<HTMLInputElement>('input[aria-label="Phase rule 1 stop after breached samples"]')!
    expect(stop.id).toBeTruthy()
    expect(container.querySelector(`label[for="${stop.id}"]`)).toBe(stop.closest('label'))
    expect(stop.min).toBe('0')
    expect(stop.placeholder).toBe('Alert only')
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
    await act(async () => {
      setter.call(stop, '60')
      stop.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(onChange).toHaveBeenLastCalledWith([{ phase: 'dns', percentile: 95, max_ms: 500, stop_consecutive_samples: 60 }])

    await act(async () => container.querySelector<HTMLButtonElement>('button[aria-label="Remove phase rule 1"]')!.click())
    expect(onChange).toHaveBeenLastCalledWith([])
  })
})
