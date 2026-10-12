// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { MetricsHistoryPoint, TestSummary } from '../types'

// The page averages the samples its live metrics stream delivers; stand in
// for the stream with a recent sample so the page's rates read "10 s avg".
const streamHistory: MetricsHistoryPoint[] = []
vi.mock('./useExecutionMetricsStream', () => ({
  useExecutionMetricsStream: () => ({ localMetricsHistory: streamHistory, latestMetrics: undefined }),
}))

import CommandCenterPrototype from './CommandCenterPrototype'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('CommandCenterPrototype goal window', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    window.localStorage.clear()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    streamHistory.splice(0, streamHistory.length, {
      time: '09:10:00', timestampMs: Date.now() - 1000, vusers: 4, tps: 11, avgMs: 40, errorPct: 0, p50: 30, p90: 60, p95: 70, p99: 90, 'Store_Browse_tps': 11,
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    vi.restoreAllMocks()
    container.remove()
  })

  // Once the page averages its own samples, a goal run is shown as the engine
  // judges it: the goal window's TPS against the targets in force over that
  // window, and the gap between them — so a target change does not read as a gap.
  it('shows a goal run by the engine\'s goal window once the page averages', async () => {
    const startedAt = new Date(Date.now() - 60_000).toISOString()
    const liveTest: TestSummary = {
      id: 'live-goal-window',
      name: 'Goal window',
      status: 'RUNNING',
      duration: '5m',
      total_vusers: 4,
      created_at: startedAt,
      started_at: startedAt,
      scenario_mode: 'goal_oriented',
      goal_config: { target_average_tps: 8 },
      groups: [{ name: 'Store_Browse', protocol: 'http', script_id: 'script-browse', script_name: 'Store_Browse', vusers: 4 }],
      live_metrics: {
        timestamp: new Date().toISOString(),
        active_vusers: 4,
        tps: 12,
        target_tps: 8,
        goal_window_tps: 9.5,
        goal_window_target_tps: 10,
        goal_gap_pct: 5,
        avg_response_ms: 40,
        error_pct: 0,
        total_transactions: 120,
        total_errors: 0,
        p50_ms: 30,
        p90_ms: 60,
        p95_ms: 70,
        p99_ms: 90,
        groups: {
          Store_Browse: { name: 'Store_Browse', vusers: 4, transactions: 120, errors: 0, tps: 12, target_tps: 8, goal_window_tps: 9.5, goal_window_target_tps: 10, goal_gap_pct: 5, avg_response_ms: 40, p50_ms: 30, p90_ms: 60, p99_ms: 90 },
        },
      },
    }
    await act(async () => {
      root.render(<CommandCenterPrototype testId="live-goal-window" initialTest={liveTest} history={[]} />)
    })

    const goalRow = container.querySelector('.command-group-row.goal-target')!
    expect(goalRow.querySelector('.command-tps-stack strong')?.textContent).toBe('9.5')
    expect(goalRow.querySelector('.command-tps-stack small')?.textContent).toBe('10 s target 10.0 TPS / 5.0% under')
    const throughputPill = Array.from(container.querySelectorAll<HTMLElement>('.command-metric-pill'))
      .find(metric => metric.querySelector('span')?.textContent?.endsWith('/ target'))
    expect(throughputPill?.querySelector('span')?.textContent).toBe('TPS (10 s avg) / target')
    expect(throughputPill?.querySelector('strong')?.textContent).toBe('9.5 of 10.0 · 5.0% under')
  })

})
