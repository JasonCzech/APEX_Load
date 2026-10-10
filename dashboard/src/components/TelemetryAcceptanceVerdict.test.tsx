// @vitest-environment jsdom

import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it } from 'vitest'

import TelemetryAcceptanceVerdict from './TelemetryAcceptanceVerdict'
import { formatAcceptancePercent } from './telemetryAcceptanceFormat'
import type { TelemetryAcceptanceVerdict as Verdict } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const verdict: Verdict = {
  outcome: 'fail', mode: 'fail_ci',
  policy: { kind: 'telemetry_acceptance_policy', id: 'gate', name: 'Gate', revision: 3, sha256: 'abc' },
  criteria: [
    { criterion: 'min_wall_time_coverage', outcome: 'fail', comparator: '>=', threshold: 0.9, unit: 'ratio', observed: 0.7, bound: 'exact', numerator: 70000, denominator: 100000, reason: 'wall-time coverage 70.0% is below the 90.0% floor' },
    { criterion: 'max_lost_records', outcome: 'inconclusive', comparator: '<=', threshold: 10, unit: 'records', observed: 4, bound: 'lower_bound', reason: 'at least 4 records were lost' },
  ],
  reasons: ['wall-time coverage 70.0% is below the 90.0% floor', 'at least 4 records were lost'],
  exclusions: ['shedded_arrivals: a workload shortfall'],
}

describe('TelemetryAcceptanceVerdict', () => {
  it('shows the verdict apart from the application verdict, with each criterion and its bound', () => {
    const container = document.createElement('div')
    const root = createRoot(container)
    act(() => root.render(<TelemetryAcceptanceVerdict verdict={verdict} />))
    const text = container.textContent || ''
    expect(text).toContain('FAIL')
    expect(text).toContain('Gate revision 3')
    expect(text).toContain('fail_ci — can fail an otherwise passing CI run')
    expect(text).toContain('separate from the application verdict')
    const rows = [...container.querySelectorAll('tbody tr')].map(row => [...row.querySelectorAll('td')].map(cell => cell.textContent))
    expect(rows[0]).toEqual(['min_wall_time_coverage', 'fail', '70.00% (exact)', '>= 90.00%', '70,000 / 100,000'])
    expect(rows[1]).toEqual(['max_lost_records', 'inconclusive', '≥ 4 records (lower bound)', '<= 10 records', '—'])
    expect(text).toContain('wall-time coverage 70.0% is below the 90.0% floor')
    act(() => root.unmount())
  })

  it('shows an upper bound as at most and partial evidence as bounding nothing', () => {
    const bounded: Verdict = { ...verdict, criteria: [
      { criterion: 'stage_lag:transport/p99', outcome: 'pass', comparator: '<=', threshold: 500, unit: 'ms', observed: 300, bound: 'upper_bound' },
      { criterion: 'stage_lag:transport/p95', outcome: 'inconclusive', comparator: '<=', threshold: 500, unit: 'ms', observed: 450, bound: 'partial' },
    ] }
    const container = document.createElement('div')
    const root = createRoot(container)
    act(() => root.render(<TelemetryAcceptanceVerdict verdict={bounded} />))
    const observed = [...container.querySelectorAll('tbody tr')].map(row => row.querySelectorAll('td')[2]?.textContent)
    expect(observed).toEqual(['≤ 300 ms (upper bound)', '450 ms (partial evidence)'])
    act(() => root.unmount())
  })

  it('labels a live verdict provisional and an advisory one as never changing CI', () => {
    const container = document.createElement('div')
    const root = createRoot(container)
    act(() => root.render(<TelemetryAcceptanceVerdict verdict={{ ...verdict, mode: 'advisory', provisional: true }} />))
    expect(container.textContent).toContain('Telemetry acceptance (provisional)')
    expect(container.textContent).toContain('advisory — never changes a CI outcome')
    act(() => root.render(<TelemetryAcceptanceVerdict />))
    expect(container.textContent).toBe('')
    act(() => root.unmount())
  })

  it('marks an advisory criterion and keeps small ratios readable', () => {
    const advisory: Verdict = { ...verdict, outcome: 'pass', reasons: [], criteria: [
      { criterion: 'stage_lag:browser_render/p95', outcome: 'fail', comparator: '<=', threshold: 500, unit: 'ms', observed: 900, bound: 'exact', advisory: true },
      { criterion: 'max_loss_ratio', outcome: 'pass', comparator: '<=', threshold: 0.0003, unit: 'ratio', observed: 0.0002, bound: 'exact' },
    ] }
    const container = document.createElement('div')
    const root = createRoot(container)
    act(() => root.render(<TelemetryAcceptanceVerdict verdict={advisory} />))
    const rows = [...container.querySelectorAll('tbody tr')].map(row => row.textContent || '')
    expect(rows[0]).toContain('fail (advisory, never decides)')
    expect(rows[1]).toContain('0.020%')
    expect(rows[1]).toContain('0.030%')
    act(() => root.unmount())
    expect([0, 0.7, 0.0003, 0.000004].map(formatAcceptancePercent)).toEqual(['0.00%', '70.00%', '0.030%', '0.00040%'])
  })
})
