// @vitest-environment jsdom
import { act, type ReactNode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ReleaseTrendPanel } from './ReleaseTrendPanel'
import type { Release, ReleaseTrend, RunArchiveReport, TestSummary } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('recharts', () => {
  const Mock = ({ children }: { children?: ReactNode }) => <g>{children}</g>
  return { ReferenceLine: Mock, CartesianGrid: Mock, LineChart: ({ children }: { children?: ReactNode }) => <svg>{children}</svg>, ResponsiveContainer: ({ children }: { children?: ReactNode }) => <div>{children}</div>, Tooltip: Mock, XAxis: Mock, YAxis: Mock, Line: Mock }
})

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

const flush = async () => { await act(async () => { await Promise.resolve() }) }

function trendFixture(): ReleaseTrend {
  return {
    release_id: 'rel-1',
    run_count: 2,
    points: [
      { metric_population: 'transaction', run_id: 'a', run_name: 'Run A', status: 'COMPLETE', completed_at: '2026-06-01T10:00:00Z', transactions: 1000, error_pct: 1, avg_response_ms: 100, peak_tps: 50, peak_active_vusers: 100, completeness_pct: 100, sla_breached: false },
      { metric_population: 'transaction', run_id: 'b', run_name: 'Run B', status: 'COMPLETE', completed_at: '2026-06-01T11:00:00Z', transactions: 1000, error_pct: 4, avg_response_ms: 140, peak_tps: 48, peak_active_vusers: 100, completeness_pct: 100, sla_breached: true, deltas: { error_pct_delta: 3, avg_response_ms_delta: 40, avg_response_pct_delta: 40, peak_tps_delta: -2, completeness_delta: 0 }, regressions: ['error rate regressed', 'SLA newly breached'] },
    ],
    summary: { latest_regressed: true, latest_regressions: ['error rate regressed', 'SLA newly breached'], avg_response_trend_pct: 40, error_pct_trend: 3 },
  }
}

function runFixture(id: string, scenarioId: string): TestSummary {
  return {
    id,
    name: `Run ${id}`,
    status: 'COMPLETE',
    scenario_id: scenarioId,
    release_id: 'rel-1',
    groups: [],
    total_vusers: 25,
    duration: '10m',
    created_at: '2026-06-01T09:00:00Z',
  } as TestSummary
}

function reportFixture(actions: Record<string, { transactions: number; avg: number; errorPct: number }>): RunArchiveReport {
  return {
    manifest: { test_id: 'x' },
    overview: { transactions: 1000, errors: 10, error_pct: 1, avg_response_ms: 100, min_response_ms: 10, max_response_ms: 900, p95_ms: 220, resource_samples: 4, interval_summaries: 4, peak_tps: 50, peak_active_vusers: 100 },
    metric_populations: { transaction: { transactions: 1000, errors: 10, error_pct: 1, avg_response_ms: 100, p95_ms: 200, average_rate_per_second: 10 } },
    by_transaction: Object.fromEntries(Object.entries(actions).map(([name, stats]) => [name, {
      transactions: stats.transactions,
      errors: 0,
      error_pct: stats.errorPct,
      avg_response_ms: stats.avg,
      min_response_ms: 5,
      max_response_ms: stats.avg * 3,
      p95_ms: stats.avg * 2,
    }])),
    resource_peaks: { cpu_process_pct: 41.5, mem_process_mb: 900, net_sent_kbps: 10, net_recv_kbps: 10, disk_read_kbps: 0, disk_write_kbps: 0 },
  } as unknown as RunArchiveReport
}

function clickRow(runId: string) {
  const row = container.querySelector(`[data-testid="release-trend-row-${runId}"]`) as HTMLElement
  expect(row).toBeTruthy()
  act(() => { row.querySelector('button')!.click() })
}

function selectByLabel(label: string): HTMLSelectElement {
  const field = Array.from(container.querySelectorAll('.release-trend-filter'))
    .find(node => node.querySelector('span')?.textContent === label)
  expect(field).toBeTruthy()
  return field!.querySelector('select') as HTMLSelectElement
}

const detailProps = {
  runsById: {
    a: runFixture('a', 'scenario-checkout'),
    b: runFixture('b', 'scenario-search'),
  },
  reportsById: {
    a: reportFixture({ Checkout: { transactions: 600, avg: 90, errorPct: 0 }, Search: { transactions: 400, avg: 40, errorPct: 0 } }),
    b: reportFixture({ Checkout: { transactions: 500, avg: 150, errorPct: 2.5 } }),
  },
  scenarioNames: { 'scenario-checkout': 'Checkout Peak', 'scenario-search': 'Search Browse' },
}

describe('ReleaseTrendPanel', () => {
  it('explains missing named-transaction evidence and the need for another baseline run', async () => {
    const trend = trendFixture()
    trend.run_count = 1
    trend.points = [{ ...trend.points[0], metric_population: 'transaction', transactions: 0 }]
    await act(async () => { root.render(<ReleaseTrendPanel releaseId="rel-1" getReleaseTrend={vi.fn().mockResolvedValue(trend)} />) })
    await flush()
    expect(container.textContent).toContain('Request / action samples in Load Comparison do not establish named transaction attempts')
    expect(container.textContent).toContain('A trend needs at least two comparable runs')
    expect(container.textContent).toContain('No samples')
  })

  it('recomputes scenario-only rows, latest summary and detail against the previous visible run', async () => {
    const trend = trendFixture()
    trend.points = [trend.points[0], {...trend.points[1], avg_response_ms: 400}, {...trend.points[1], run_id: 'c', run_name: 'Run C', completed_at: '2026-06-01T12:00:00Z', avg_response_ms: 150}]
    trend.run_count = 3
    await act(async () => root.render(<ReleaseTrendPanel releaseId="rel-1" getReleaseTrend={vi.fn().mockResolvedValue(trend)} runsById={{a: runFixture('a', 'checkout'), b: runFixture('b', 'search'), c: runFixture('c', 'checkout')}} />))
    const metric = selectByLabel('Latency statistic')
    await act(async () => {metric.value = 'average_ms'; metric.dispatchEvent(new Event('change', {bubbles: true}))})
    const scenario = selectByLabel('Scenario')
    await act(async () => {scenario.value = 'checkout'; scenario.dispatchEvent(new Event('change', {bubbles: true}))})
    expect(container.querySelector('[data-testid="release-trend-row-b"]')).toBeNull()
    expect(container.querySelector('[data-testid="release-trend-row-c"]')?.textContent).toContain('+50.0 ms')
    expect(container.textContent).toContain('Latency +50.0% exceeds 10% rule')
    clickRow('c')
    const detail = container.querySelector('[data-testid="release-trend-detail"]')!
    expect(detail.textContent).toContain('Run A')
    expect(detail.textContent).not.toContain('Run B')
  })

  it('judges the latest run against its recorded baseline until the user picks a policy', async () => {
    const trend = trendFixture()
    const latest = { ...runFixture('b', 'checkout'), comparison_baseline: { source_test_id: 'a' } } as TestSummary
    await act(async () => root.render(<ReleaseTrendPanel releaseId="rel-1" getReleaseTrend={vi.fn().mockResolvedValue(trend)} runsById={{ a: runFixture('a', 'checkout'), b: latest }} />))
    await flush()
    const policy = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith('Baseline policy'))?.querySelector('select') as HTMLSelectElement
    expect(policy.value).toBe('recorded')
    await act(async () => { policy.value = 'previous'; policy.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(policy.value).toBe('previous')
  })

  it('does not silently replace a fixed baseline excluded by a scenario filter', async () => {
    const trend = trendFixture()
    trend.points.push({ ...trend.points[1], run_id: 'c', run_name: 'Run C', completed_at: '2026-06-01T12:00:00Z', avg_response_ms: 150 })
    trend.run_count = 3
    await act(async () => root.render(<ReleaseTrendPanel releaseId="rel-1" getReleaseTrend={vi.fn().mockResolvedValue(trend)} runsById={{ a: runFixture('a', 'checkout'), b: runFixture('b', 'search'), c: runFixture('c', 'checkout') }} />))
    const change = async (select: HTMLSelectElement, value: string) => {
      await act(async () => { select.value = value; select.dispatchEvent(new Event('change', { bubbles: true })) })
    }
    const control = (label: string) => Array.from(container.querySelectorAll('label')).find(node => node.textContent?.startsWith(label))!.querySelector('select')!
    await change(selectByLabel('Latency statistic'), 'average_ms')
    await change(control('Baseline policy'), 'pinned')
    await change(control('Fixed baseline'), 'b')
    await change(selectByLabel('Scenario'), 'checkout')
    expect(control('Fixed baseline').value).toBe('')
    expect(container.textContent).toContain('The fixed baseline has no matching evidence for these filters')
    const row = container.querySelector('[data-testid="release-trend-row-c"]')!
    expect(row.textContent).not.toContain('+50.0 ms')
    expect(row.textContent).not.toContain('Potential regression')
    clickRow('c')
    expect(container.querySelector('[data-testid="release-trend-detail"]')?.textContent).toContain('None')
    await change(selectByLabel('Scenario'), '')
    expect(control('Fixed baseline').value).toBe('b')
  })

  it('renders the run series with deltas and a regression flag', async () => {
    const getReleaseTrend = vi.fn().mockResolvedValue(trendFixture())
    await act(async () => {
      root.render(<ReleaseTrendPanel releaseId="rel-1" getReleaseTrend={getReleaseTrend} />)
    })
    await flush()

    expect(getReleaseTrend).toHaveBeenCalledWith('rel-1')
    expect(container.textContent).toContain('Release Trend Analysis')
    expect(container.textContent).toContain('Run A')
    expect(container.textContent).toContain('Run B')
    const select = selectByLabel('Latency statistic')
    await act(async () => { select.value = 'average_ms'; select.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(container.textContent).toContain('+40.0 ms')
    expect(container.textContent).toContain('Potential regression (unverified)')
    expect(container.textContent).toContain('Error rate +3.00 pp exceeds 1 pp rule')
  })

  it('renders an empty state when there are no runs', async () => {
    const getReleaseTrend = vi.fn().mockResolvedValue({ release_id: 'rel-1', run_count: 0, points: [], summary: { latest_regressed: false, avg_response_trend_pct: 0, error_pct_trend: 0 } })
    await act(async () => {
      root.render(<ReleaseTrendPanel releaseId="rel-1" getReleaseTrend={getReleaseTrend} />)
    })
    await flush()
    expect(container.textContent).toContain('No archived runs yet')
  })

  it('surfaces a load error', async () => {
    const getReleaseTrend = vi.fn().mockRejectedValue(new Error('boom'))
    await act(async () => {
      root.render(<ReleaseTrendPanel releaseId="rel-1" getReleaseTrend={getReleaseTrend} />)
    })
    await flush()
    expect(container.textContent).toContain('boom')
  })

  it('ignores a stale release response after the selected release changes', async () => {
    let resolveFirst: ((trend: ReleaseTrend) => void) | undefined
    let resolveSecond: ((trend: ReleaseTrend) => void) | undefined
    const first = new Promise<ReleaseTrend>(resolve => { resolveFirst = resolve })
    const second = new Promise<ReleaseTrend>(resolve => { resolveSecond = resolve })
    const getReleaseTrend = vi.fn((id: string) => id === 'rel-1' ? first : second)

    await act(async () => {
      root.render(<ReleaseTrendPanel releaseId="rel-1" getReleaseTrend={getReleaseTrend} />)
    })
    await act(async () => {
      root.render(<ReleaseTrendPanel releaseId="rel-2" getReleaseTrend={getReleaseTrend} />)
    })
    await act(async () => {
      resolveSecond?.({
        ...trendFixture(),
        release_id: 'rel-2',
        points: [{ ...trendFixture().points[0], run_id: 'current', run_name: 'Current Release Run' }],
        run_count: 1,
      })
    })
    await flush()
    await act(async () => {
      resolveFirst?.({
        ...trendFixture(),
        points: [{ ...trendFixture().points[0], run_id: 'stale', run_name: 'Stale Release Run' }],
        run_count: 1,
      })
    })
    await flush()

    expect(container.textContent).toContain('Current Release Run')
    expect(container.textContent).not.toContain('Stale Release Run')
  })

  it('filters the series by scenario', async () => {
    const getReleaseTrend = vi.fn().mockResolvedValue(trendFixture())
    await act(async () => {
      root.render(<ReleaseTrendPanel releaseId="rel-1" getReleaseTrend={getReleaseTrend} {...detailProps} />)
    })
    await flush()

    expect(container.textContent).toContain('Checkout Peak')
    expect(container.textContent).toContain('Search Browse')

    const scenarioSelect = selectByLabel('Scenario')
    await act(async () => {
      scenarioSelect.value = 'scenario-checkout'
      scenarioSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(container.querySelector('[data-testid="release-trend-row-a"]')).toBeTruthy()
    expect(container.querySelector('[data-testid="release-trend-row-b"]')).toBeNull()
    expect(container.textContent).toContain('Showing 1 of 2 runs')
  })

  it('scopes the table to a selected transaction and hides runs without it', async () => {
    const getReleaseTrend = vi.fn().mockResolvedValue(trendFixture())
    await act(async () => {
      root.render(<ReleaseTrendPanel releaseId="rel-1" getReleaseTrend={getReleaseTrend} {...detailProps} />)
    })
    await flush()

    const transactionSelect = selectByLabel('Transaction')
    await act(async () => {
      transactionSelect.value = 'Search'
      transactionSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })

    // Only run "a" recorded the Search transaction, and its metrics replace the
    // run-level ones.
    expect(container.querySelector('[data-testid="release-trend-row-a"]')).toBeTruthy()
    expect(container.querySelector('[data-testid="release-trend-row-b"]')).toBeNull()
    expect(container.querySelector('[data-testid="release-trend-scope-note"]')?.textContent).toContain('Search')
    expect(container.querySelector('[data-testid="release-trend-row-a"]')?.textContent).toContain('80.0')
  })

  it('populates the detail area from the selected run and collapses on reselect', async () => {
    const getReleaseTrend = vi.fn().mockResolvedValue(trendFixture())
    const onOpenRunArchive = vi.fn()
    await act(async () => {
      root.render(
        <ReleaseTrendPanel
          releaseId="rel-1"
          getReleaseTrend={getReleaseTrend}
          onOpenRunArchive={onOpenRunArchive}
          {...detailProps}
        />,
      )
    })
    await flush()

    expect(container.querySelector('[data-testid="release-trend-detail"]')?.textContent)
      .toContain('Select a run above')

    const baseline = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.startsWith('Baseline policy'))!.querySelector('select')!
    await act(async () => { baseline.value = 'pinned'; baseline.dispatchEvent(new Event('change', { bubbles: true })) })
    clickRow('b')
    const detail = container.querySelector('[data-testid="release-trend-detail"]') as HTMLElement
    expect(detail.textContent).toContain('Run B')
    expect(detail.textContent).toContain('Not comparable')
    // Run-over-run deltas, the transaction breakdown, and archived resource peaks.
    expect(detail.textContent).not.toContain('+40.0 ms')
    expect(detail.textContent).toContain('Checkout')
    expect(detail.textContent).toContain('41.5%')
    expect(detail.textContent).toContain('Test conditions differ')

    clickRow('b')
    expect(container.querySelector('[data-testid="release-trend-detail"]')?.textContent)
      .toContain('Select a run above')
  })

  it('switches releases from the release filter', async () => {
    const getReleaseTrend = vi.fn().mockResolvedValue(trendFixture())
    const onReleaseChange = vi.fn()
    const releaseOptions: Release[] = [
      { id: 'rel-1', application_id: 'app-1', name: 'Sprint 24', created_at: '', updated_at: '' },
      { id: 'rel-2', application_id: 'app-1', name: 'Sprint 25', created_at: '', updated_at: '' },
    ]
    await act(async () => {
      root.render(
        <ReleaseTrendPanel
          releaseId="rel-1"
          getReleaseTrend={getReleaseTrend}
          releaseOptions={releaseOptions}
          onReleaseChange={onReleaseChange}
        />,
      )
    })
    await flush()

    const releaseSelect = selectByLabel('Release')
    expect(releaseSelect.value).toBe('rel-1')
    await act(async () => {
      releaseSelect.value = 'rel-2'
      releaseSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(onReleaseChange).toHaveBeenCalledWith(releaseOptions[1])
  })
})
