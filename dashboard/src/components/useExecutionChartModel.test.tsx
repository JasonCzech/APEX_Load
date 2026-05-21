// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { MetricsHistoryPoint } from '../types'
import { useExecutionChartModel } from './useExecutionChartModel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function ChartHarness({
  test,
  history,
  localMetricsHistory,
}: {
  test: unknown
  history: MetricsHistoryPoint[]
  localMetricsHistory: MetricsHistoryPoint[]
}) {
  const model = useExecutionChartModel({
    test: test as never,
    history,
    localMetricsHistory,
  })

  return (
    <div>
      <span data-testid="time-range">{model.timeRange}</span>
      <span data-testid="filtered-count">{model.filteredData.length}</span>
      <span data-testid="avg-tps">{model.summary?.avgTps ?? 0}</span>
      <span data-testid="show-ref-area">{model.showRefArea ? 'yes' : 'no'}</span>
      <span data-testid="ref-left">{String(model.refAreaLeft)}</span>
      <span data-testid="ref-end">{String(model.refAreaEnd)}</span>
      <button data-testid="set-3m" onClick={() => model.onTimeRangeChange('3m')}>set-3m</button>
      <button data-testid="begin" onClick={() => model.onChartMouseDown('10:00:00')}>begin</button>
      <button data-testid="move" onClick={() => model.onChartMouseMove('10:00:01')}>move</button>
      <button data-testid="zoom" onClick={() => model.onZoom()}>zoom</button>
      <button data-testid="reset" onClick={() => model.onResetZoom()}>reset</button>
    </div>
  )
}

describe('useExecutionChartModel', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  it('builds chart summary from backend and local history', async () => {
    await act(async () => {
      root.render(
        <ChartHarness
          test={{
            status: 'RUNNING',
            history: [
              {
                timestamp: '2026-05-12T20:00:00Z',
                active_vusers: 10,
                tps: 5,
                avg_response_ms: 100,
                error_pct: 1,
                p50_ms: 90,
                p90_ms: 120,
                p95_ms: 140,
                p99_ms: 180,
              },
            ],
          }}
          history={[]}
          localMetricsHistory={[
            { time: '8:00:01 PM', vusers: 12, tps: 7, avgMs: 120, errorPct: 2, p50: 100, p90: 140, p99: 190 },
          ]}
        />,
      )
    })

    expect(readTestId('filtered-count')).toBe('2')
    expect(Number(readTestId('avg-tps'))).toBe(6)
  })

  it('keeps backend history visible after local websocket history grows', async () => {
    await act(async () => {
      root.render(
        <ChartHarness
          test={{
            status: 'RUNNING',
            history: [
              {
                timestamp: '2026-05-12T20:00:00Z',
                active_vusers: 10,
                tps: 5,
                avg_response_ms: 100,
                error_pct: 1,
                p50_ms: 90,
                p90_ms: 120,
                p95_ms: 140,
                p99_ms: 180,
              },
            ],
          }}
          history={[]}
          localMetricsHistory={Array.from({ length: 12 }, (_, index) => ({
            time: `8:00:${String(index + 1).padStart(2, '0')} PM`,
            timestampMs: Date.UTC(2026, 4, 12, 20, 0, index + 1),
            vusers: 12,
            tps: 7,
            avgMs: 120,
            errorPct: 2,
            p50: 100,
            p90: 140,
            p99: 190,
          }))}
        />,
      )
    })

    expect(readTestId('filtered-count')).toBe('13')
  })

  it('tracks drag selection and applies custom zoom', async () => {
    await act(async () => {
      root.render(
        <ChartHarness
          test={{ status: 'RUNNING', history: [] }}
          history={[
            { time: '10:00:00', vusers: 2, tps: 1, avgMs: 100, errorPct: 0, p50: 90, p90: 120, p99: 150 },
            { time: '10:00:01', vusers: 4, tps: 2, avgMs: 120, errorPct: 1, p50: 100, p90: 140, p99: 180 },
            { time: '10:00:02', vusers: 6, tps: 3, avgMs: 140, errorPct: 2, p50: 110, p90: 150, p99: 190 },
          ]}
          localMetricsHistory={[]}
        />,
      )
    })

    await click('begin')
    await click('move')

    expect(readTestId('show-ref-area')).toBe('yes')
    expect(readTestId('ref-left')).toBe('10:00:00')
    expect(readTestId('ref-end')).toBe('10:00:01')

    await click('zoom')

    expect(readTestId('time-range')).toBe('custom')
    expect(readTestId('filtered-count')).toBe('2')
    expect(readTestId('show-ref-area')).toBe('no')

    await click('reset')

    expect(readTestId('time-range')).toBe('all')
    expect(readTestId('filtered-count')).toBe('3')
  })

  it('filters by elapsed time instead of raw point count for 10m view', async () => {
    const history = Array.from({ length: 240 }, (_, index) => ({
      time: `10:${String(Math.floor(index / 12)).padStart(2, '0')}:${String((index % 12) * 5).padStart(2, '0')}`,
      timestampMs: Date.UTC(2026, 4, 14, 10, 0, index * 5),
      vusers: 2,
      tps: index,
      avgMs: 100,
      errorPct: 0,
      p50: 90,
      p90: 120,
      p99: 150,
    }))

    await act(async () => {
      root.render(
        <ChartHarness
          test={{ status: 'RUNNING', history: [] }}
          history={history}
          localMetricsHistory={[]}
        />,
      )
    })

    expect(readTestId('filtered-count')).toBe('240')

    await act(async () => {
      container.querySelector('[data-testid="set-3m"]')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(readTestId('filtered-count')).toBe('37')
  })

  async function click(testId: string) {
    await act(async () => {
      container.querySelector(`[data-testid="${testId}"]`)?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  function readTestId(testId: string) {
    return container.querySelector(`[data-testid="${testId}"]`)?.textContent
  }
})
