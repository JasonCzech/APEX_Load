// @vitest-environment jsdom

import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it } from 'vitest'

import TelemetryAcceptanceVerdict from './TelemetryAcceptanceVerdict'
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
    expect(rows[0]).toEqual(['min_wall_time_coverage', 'fail', '70.0% (exact)', '>= 90.0%', '70,000 / 100,000'])
    expect(rows[1]).toEqual(['max_lost_records', 'inconclusive', '≥ 4 records (lower bound)', '<= 10 records', '—'])
    expect(text).toContain('wall-time coverage 70.0% is below the 90.0% floor')
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
})
