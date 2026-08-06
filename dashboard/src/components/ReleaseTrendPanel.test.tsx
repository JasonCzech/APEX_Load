// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ReleaseTrendPanel } from './ReleaseTrendPanel'
import type { Release, ReleaseTrend, RunArchiveReport, TestSummary } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

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
      { run_id: 'a', run_name: 'Run A', status: 'COMPLETE', completed_at: '2026-06-01T10:00:00Z', transactions: 1000, error_pct: 1, avg_response_ms: 100, peak_tps: 50, peak_active_vusers: 100, completeness_pct: 100, sla_breached: false },
      { run_id: 'b', run_name: 'Run B', status: 'COMPLETE', completed_at: '2026-06-01T11:00:00Z', transactions: 1000, error_pct: 4, avg_response_ms: 140, peak_tps: 48, peak_active_vusers: 100, completeness_pct: 100, sla_breached: true, deltas: { error_pct_delta: 3, avg_response_ms_delta: 40, avg_response_pct_delta: 40, peak_tps_delta: -2, completeness_delta: 0 }, regressions: ['error rate regressed', 'SLA newly breached'] },
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
    by_action: Object.fromEntries(Object.entries(actions).map(([name, stats]) => [name, {
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
  act(() => { row.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
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
    expect(container.textContent).toContain('+40ms')
    expect(container.textContent).toContain('error rate regressed')
    expect(container.querySelector('[data-testid="release-trend-latest-regressed"]')).toBeTruthy()
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
    expect(container.querySelector('[data-testid="release-trend-row-a"]')?.textContent).toContain('40.0')
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

    clickRow('b')
    const detail = container.querySelector('[data-testid="release-trend-detail"]') as HTMLElement
    expect(detail.textContent).toContain('Run B')
    expect(detail.textContent).toContain('Search Browse')
    // Run-over-run deltas, the transaction breakdown, and archived resource peaks.
    expect(detail.textContent).toContain('+40ms')
    expect(detail.textContent).toContain('Checkout')
    expect(detail.textContent).toContain('41.5%')
    expect(detail.textContent).toContain('SLA newly breached')

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
