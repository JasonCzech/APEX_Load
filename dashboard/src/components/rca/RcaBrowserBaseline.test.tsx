// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it } from 'vitest'

import type { RcaBrowserBaseline } from '../../types/rca'
import RcaBrowserBaselineSection from './RcaBrowserBaseline'
import { normalizeRcaAssessment } from './rcaModel'
import { rcaTestAssessment } from './rcaTestFixture'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const baseline: RcaBrowserBaseline = {
  version: 'browser_baseline.v1', status: 'complete',
  params: { min_baseline_buckets: 30, sigma: 3, timing_floor_ms: 50, timing_floor_fraction: 0.1, cls_floor: 0.02, level_tolerance: 0.05, max_results: 200 },
  baseline: { test_id: 'base-run', qualification: 'qualified' },
  results: [
    { id: 'r1', series: 'browser_document/lcp/a/p75', metric: 'lcp', unit: 'ms', level: 'vusers=10', state: 'anomaly', direction: 'increase', current: 1400, current_buckets: 20, current_samples: 60,
      baseline_mean: 1000, baseline_stddev: 28, baseline_buckets: 40, baseline_samples: 120, expected_low: 916, expected_high: 1084, z: 14.1, effect: 400, effect_floor: 100, baseline: { test_id: 'base-run' }, evidence: [] },
    { id: 'r2', series: 'browser_document/cls/b/p75', metric: 'cls', unit: 'score', level: 'vusers=10', state: 'unscored', reason: 'cohort_changed', detail: 'the baseline measured this document with a different shaping_applied',
      current: 0.05, current_buckets: 10, current_samples: 10, baseline_buckets: 0, baseline_samples: 0, baseline: { test_id: 'base-run' }, evidence: [] },
  ],
  excluded: { outside_steady_load: 3 },
  limitations: [],
}

describe('RcaBrowserBaselineSection', () => {
  it('lists each cohort with its result, expected range and counts', () => {
    const container = document.createElement('div')
    const root = createRoot(container)
    act(() => root.render(<RcaBrowserBaselineSection baseline={baseline} />))
    expect(container.querySelector('summary')?.textContent).toBe('Browser baseline (1 anomaly)')
    const rows = [...container.querySelectorAll('tbody tr')].map(row => row.textContent || '')
    expect(rows[0]).toContain('LCP')
    expect(rows[0]).toContain('Higher than baseline')
    expect(rows[0]).toContain('1,400 ms')
    expect(rows[0]).toContain('916 ms – 1,084 ms')
    expect(rows[0]).toContain('20 (60) vs 40 (120)')
    expect(rows[1]).toContain('Unscored: Cohort changed — the baseline measured this document with a different shaping_applied')
    expect(container.textContent).toContain('baseline run base-run')
    expect(container.textContent).toContain('3 outside steady load buckets')
    act(() => root.render(<RcaBrowserBaselineSection />))
    expect(container.textContent).toBe('')
    act(() => root.unmount())
  })

  it('normalizes a controller payload with null lists', () => {
    const payload = { ...rcaTestAssessment(), browser_baseline: { ...baseline, results: [{ ...baseline.results[0], evidence: null, caveats: null }], limitations: null } }
    const normalized = normalizeRcaAssessment(payload as never)
    expect(normalized.browser_baseline?.limitations).toEqual([])
    expect(normalized.browser_baseline?.results[0].evidence).toEqual([])
    expect(normalized.browser_baseline?.results[0].caveats).toEqual([])
  })
})
