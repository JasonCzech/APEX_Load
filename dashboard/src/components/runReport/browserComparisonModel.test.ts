// @vitest-environment jsdom
import { act } from 'react'
import { createElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { browserVitalsAlgorithm, browserVitalsAlgorithmSHA256, type BrowserMetricDistribution } from '../../types/browserDocumentEvidence'
import BrowserComparisonTable from './BrowserComparisonTable'
import { browserDistributionQuantile, buildBrowserComparison } from './browserComparisonModel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

// A distribution filled as protocol.BrowserMetricDistribution.Add fills it.
function distributionOf(values: number[]): BrowserMetricDistribution {
  const buckets = Array<number>(192).fill(0)
  for (const v of values) buckets[v > 0.000001 ? Math.min(191, Math.ceil(Math.log(v / 0.000001) / Math.log(1.2))) : 0] += 1
  return { count: values.length, sum: values.reduce((a, b) => a + b, 0), min: Math.min(...values), max: Math.max(...values), buckets }
}

// An eligible actual runtime cohort (as BrowserDocumentEvidencePanel.test uses).
const cohort = { version: 1, browser_version: '136.0.1', platform: 'linux', viewport_width: 800, viewport_height: 600, headless: true, mobile: false, touch: false, device_scale_factor: 1, cpu_throttle_rate: 1, network_latency_ms: 0, network_bandwidth_kbps: 0, shaping_applied: true, cache_enabled: false, cache_content_urls: false, check_newer_versions: false, new_user_policy: 'persistent_context', new_user_policy_applied: true }
const tuple = (metric: string, values: number[], extra: Record<string, unknown> = {}) => ({
  metric, algorithm: browserVitalsAlgorithm, algorithm_sha256: browserVitalsAlgorithmSHA256, unit: metric === 'cls' ? 'score' : 'ms',
  engine: 'chromium', device_profile: 'desktop', cohort, frame_scope: 'main_frame', lifecycle_phase: 'navigation',
  eligible_final: values.length, unavailable_final: 0, unqualified_final: 0, censored: 0, provisional: 0,
  distribution: values.length ? distributionOf(values) : { count: 0, sum: 0, min: 0, max: 0, buckets: Array<number>(192).fill(0) },
  ...extra,
})
const summary = (metrics: unknown[]) => ({ version: 2, metrics, dropped_observations: 0, incomplete: false })

describe('browser comparison (BI-4)', () => {
  it('takes p75 as the controller does: the bucket upper bound, capped by the maximum', () => {
    const d = distributionOf([100, 200, 300, 400])
    const p75 = browserDistributionQuantile(d, 0.75)!
    expect(p75).toBeGreaterThanOrEqual(300)
    expect(p75).toBeLessThanOrEqual(360)
    expect(browserDistributionQuantile(d, 1)).toBe(400)
    expect(browserDistributionQuantile(distributionOf([0]), 0.75)).toBe(0)
    expect(browserDistributionQuantile({ count: 0, sum: 0, min: 0, max: 0, buckets: Array<number>(192).fill(0) }, 0.75)).toBeNull()
  })

  it('compares main-frame navigation documents by engine, device and group, and leaves other scopes out', () => {
    const comparison = buildBrowserComparison(summary([
      tuple('lcp', [1000, 1200, 1400, 1600]),
      tuple('cls', [0.01, 0.02]),
      tuple('lcp', [2500], { engine: 'webkit', device_profile: 'iphone_15', group: 'checkout' }),
      tuple('lcp', [900], { frame_scope: 'child_frame' }),
      tuple('lcp', [300], { lifecycle_phase: 'bfcache_restore' }),
      tuple('fcp', []),
    ]), 'archive-latest')
    expect(comparison.state).toBe('ready')
    expect(comparison.otherScopes).toBe(2)
    expect(comparison.rows.map(row => [row.engine, row.deviceProfile, row.group])).toEqual([['chromium', 'desktop', ''], ['webkit', 'iphone_15', 'checkout']])
    expect(comparison.rows[0].metrics.lcp).toMatchObject({ final: 4, unit: 'ms' })
    expect(comparison.rows[0].metrics.cls?.unit).toBe('score')
    expect(comparison.rows[0].metrics.fcp).toEqual({ p75: null, final: 0, unit: 'ms' })
    expect(comparison.rows[1].metrics.lcp?.p75).toBe(2500)
  })

  it('keeps runtime cohorts of one engine, device and group apart', () => {
    const comparison = buildBrowserComparison(summary([
      tuple('lcp', [1000]),
      tuple('lcp', [3000], { cohort: { ...cohort, network_latency_ms: 300 } }),
    ]), 'live-history')
    expect(comparison.rows.map(row => row.cohortLabel)).toEqual(['runtime cohort 1 of 2', 'runtime cohort 2 of 2'])
  })

  it('reports absent, unsupported and invalid summaries', () => {
    expect(buildBrowserComparison(undefined, 'archive-latest').state).toBe('absent')
    expect(buildBrowserComparison({ version: 1 }, 'archive-latest').state).toBe('unsupported')
    expect(buildBrowserComparison({ version: 2, metrics: 'x', dropped_observations: 0, incomplete: false }, 'archive-latest').state).toBe('invalid')
  })
})

describe('BrowserComparisonTable', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
  afterEach(async () => { await act(async () => root.unmount()); container.remove() })

  it('renders p75 cells with their document counts', async () => {
    await act(async () => root.render(createElement(BrowserComparisonTable, { summary: summary([tuple('lcp', [1000, 1200]), tuple('lcp', [900], { frame_scope: 'child_frame' })]), mode: 'archive-latest' })))
    const row = container.querySelector<HTMLTableRowElement>('tbody tr')!
    expect(row.cells[0].textContent).toBe('chromium')
    expect(row.cells[3].textContent).toContain('(2 documents)')
    expect(container.textContent).toContain('1 child-frame or back-forward cache metric tuple is left out')
  })

  it('says when a live run has published no documents yet', async () => {
    await act(async () => root.render(createElement(BrowserComparisonTable, { summary: undefined, mode: 'live-history' })))
    expect(container.textContent).toContain('No browser document evidence has been published for this run yet.')
  })
})
