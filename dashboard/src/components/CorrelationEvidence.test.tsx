// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it } from 'vitest'

import CorrelationEvidence, { type CorrelationSemantics } from './CorrelationEvidence'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const semantics: CorrelationSemantics = {
  left: { metric: 'cpu', unit: 'percent', kind: 'gauge', input_kind: 'gauge', statistic: 'avg', points: 10, valid: 8, excluded: { stale: 1, counter_reset: 1 } },
  right: { metric: 'transaction.duration', unit: 'ms', kind: 'gauge', input_kind: 'summary', population: 'transaction', statistic: 'p95', conversion: 'declared_summary_statistic', points: 10, valid: 10 },
  same_units: false,
  lag_convention: 'lag_seconds > 0 pairs each dataset bucket with the comparison series lag seconds later: the comparison series follows the dataset.',
}

describe('CorrelationEvidence', () => {
  it('keeps each series semantics and explains unavailable coefficients', async () => {
    const node = document.createElement('div')
    const root = createRoot(node)
    await act(async () => root.render(<CorrelationEvidence semantics={semantics} left={[]} right={[]} scores={[
      { lag_seconds: -5, pairs: 2, expected_buckets: 9, coverage: 0.22, unavailable: 'insufficient_pairs' },
      { lag_seconds: 0, pairs: 8, expected_buckets: 10, coverage: 0.8, unavailable: 'constant_series' },
      { lag_seconds: 5, pairs: 8, expected_buckets: 9, coverage: 0.89, coefficient: 0.734 },
    ]} />))
    const left = node.querySelector('[data-side="Dataset series"]')!.textContent!
    const right = node.querySelector('[data-side="Comparison series"]')!.textContent!
    expect(left).toContain('percent')
    expect(left).toContain('1 counter reset, 1 stale')
    expect(right).toContain('ms')
    expect(right).toContain('p95 · declared summary statistic (not raw observations)')
    expect(right).toContain('gauge from summary')
    expect(right).toContain('transaction')
    expect(node.textContent).toContain('never compared value for value')
    const rows = [...node.querySelectorAll('tbody tr')].map(row => row.textContent)
    expect(rows[0]).toContain('Fewer than three paired buckets')
    expect(rows[1]).toContain('A series did not vary')
    expect(rows[2]).toContain('0.734')
    expect(node.querySelector('caption')!.textContent).toContain('the comparison series follows the dataset')
    await act(async () => root.unmount())
  })

  it('labels a separate axis per unit for the investigation chart', async () => {
    const node = document.createElement('div')
    const root = createRoot(node)
    const point = (timestamp_utc: string, value: number, unit: string) => ({ timestamp_utc, value, unit, quality: 'valid', metric_name: 'm' })
    await act(async () => root.render(<CorrelationEvidence semantics={semantics} scores={[]}
      left={[point('2026-10-08T00:00:00Z', 50, 'percent'), point('2026-10-08T00:00:10Z', 60, 'percent')]}
      right={[point('2026-10-08T00:00:00Z', 120, 'ms'), point('2026-10-08T00:00:10Z', 180, 'ms')]} />))
    expect(node.querySelector('[role="img"]')!.getAttribute('aria-label')).toBe('Correlation investigation chart: percent on the left axis, ms on the right axis')
    await act(async () => root.unmount())
  })
})
