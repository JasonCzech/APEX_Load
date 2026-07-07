// @vitest-environment jsdom

import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { MetricsHistoryPoint } from '../../types'
import SynchronizedTimeline from './SynchronizedTimeline'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('recharts', () => {
  const Mock = ({ children }: { children?: ReactNode }) => <g>{children}</g>
  const MockChart = ({ children, data, syncId }: { children?: ReactNode; data?: unknown[]; syncId?: string }) => (
    <svg data-testid="mock-chart" data-sync-id={syncId || ''} data-length={String(data?.length ?? '')}>{children}</svg>
  )
  return {
    Area: ({ dataKey }: { dataKey?: string }) => <path data-testid="mock-area" data-key={dataKey || ''} />,
    Brush: ({ onChange }: { onChange?: (range: { startIndex?: number; endIndex?: number }) => void }) => (
      <button data-testid="mock-brush" type="button" onClick={() => onChange?.({ startIndex: 1, endIndex: 2 })}>brush</button>
    ),
    CartesianGrid: Mock,
    ComposedChart: MockChart,
    Line: ({ dataKey, strokeDasharray }: { dataKey?: string; strokeDasharray?: string }) => (
      <path data-testid="mock-line" data-key={dataKey || ''} data-dash={strokeDasharray || ''} />
    ),
    ReferenceArea: ({ x1, x2 }: { x1?: string; x2?: string }) => <path data-testid="mock-reference-area" data-x1={x1 || ''} data-x2={x2 || ''} />,
    ReferenceLine: ({ y }: { y?: number }) => <path data-testid="mock-reference-line" data-y={String(y ?? '')} />,
    ResponsiveContainer: ({ children }: { children?: ReactNode }) => <div data-testid="mock-responsive">{children}</div>,
    Tooltip: Mock,
    XAxis: Mock,
    YAxis: Mock,
  }
})

const samplePoint = (time: string, p95: number, errorPct: number): MetricsHistoryPoint => ({
  time,
  timestampMs: Date.parse(`2026-05-12T20:00:${time}Z`),
  vusers: 10,
  tps: 20,
  targetTps: 18,
  avgMs: 100,
  errorPct,
  p50: 80,
  p90: 95,
  p95,
  p99: 130,
  baselineTps: 19,
  baselineP95: 110,
  baselineErrorPct: 0,
})

describe('SynchronizedTimeline', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })

  it('renders four synced lanes with SLA lines, breach bands, and baseline overlays', async () => {
    await act(async () => {
      root.render(
        <SynchronizedTimeline
          data={[samplePoint('00', 100, 0), samplePoint('01', 700, 0), samplePoint('02', 100, 8)]}
          goalConfig={{ p95_latency_ms: 500, max_error_pct: 5 }}
          showBaseline
        />,
      )
    })

    const charts = Array.from(container.querySelectorAll('[data-testid="mock-chart"]'))
    expect(charts).toHaveLength(4)
    expect(charts.every(chart => chart.getAttribute('data-sync-id') === 'metrics')).toBe(true)
    expect(container.querySelectorAll('[data-testid="mock-reference-line"]').length).toBeGreaterThanOrEqual(2)
    expect(container.querySelectorAll('[data-testid="mock-reference-area"]')).toHaveLength(4)
    expect(container.querySelector('[data-key="baselineTps"]')).not.toBeNull()
  })

  it('applies the shared brush zoom to every lane', async () => {
    await act(async () => {
      root.render(
        <SynchronizedTimeline
          data={[samplePoint('00', 100, 0), samplePoint('01', 100, 0), samplePoint('02', 100, 0), samplePoint('03', 100, 0)]}
        />,
      )
    })

    await act(async () => {
      container.querySelector('[data-testid="mock-brush"]')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const charts = Array.from(container.querySelectorAll('[data-testid="mock-chart"]'))
    expect(charts.every(chart => chart.getAttribute('data-length') === '2')).toBe(true)
  })
})
