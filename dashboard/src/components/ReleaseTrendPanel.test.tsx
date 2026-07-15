// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ReleaseTrendPanel } from './ReleaseTrendPanel'
import type { ReleaseTrend } from '../types'

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
})
