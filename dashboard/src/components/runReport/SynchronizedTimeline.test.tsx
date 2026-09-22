// @vitest-environment jsdom

import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { MetricsHistoryPoint } from '../../types'
import { transactionChartSeriesKey } from '../transactionChartSeries'
import SynchronizedTimeline from './SynchronizedTimeline'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('recharts', () => {
  const Mock = ({ children }: { children?: ReactNode }) => <g>{children}</g>
  const MockChart = ({ children, data, syncId }: { children?: ReactNode; data?: unknown[]; syncId?: string }) => (
    <svg data-testid="mock-chart" data-sync-id={syncId || ''} data-length={String(data?.length ?? '')}>{children}</svg>
  )
  return {
    Area: ({ dataKey }: { dataKey?: string }) => <path data-testid="mock-area" data-key={dataKey || ''} />,
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
  [transactionChartSeriesKey('Checkout', 'tps')]: 8,
  [transactionChartSeriesKey('Checkout', 'avgMs')]: 92,
  [transactionChartSeriesKey('Checkout', 'errorPct')]: 1,
  [transactionChartSeriesKey('Search', 'tps')]: 12,
  [transactionChartSeriesKey('Search', 'avgMs')]: 108,
  [transactionChartSeriesKey('Search', 'errorPct')]: 0,
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

  it('provides named bounded keyboard sliders and applies their window to every lane', async () => {
    await act(async () => {
      root.render(
        <SynchronizedTimeline
          data={[samplePoint('00', 100, 0), samplePoint('01', 100, 0), samplePoint('02', 100, 0), samplePoint('03', 100, 0)]}
        />,
      )
    })

    const start = container.querySelector('[aria-label="Timeline window start"]') as HTMLInputElement
    const end = container.querySelector('[aria-label="Timeline window end"]') as HTMLInputElement
    expect(start.min).toBe('0')
    expect(end.max).toBe('3')
    expect(start.getAttribute('aria-valuetext')).toBe('00')
    for (const [input, value] of [[start, '1'], [end, '2']] as const) {
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(input, value)
        input.dispatchEvent(new Event('input', { bubbles: true }))
      })
    }
    expect(start.getAttribute('aria-valuetext')).toBe('01')
    expect(end.min).toBe('1')

    const charts = Array.from(container.querySelectorAll('[data-testid="mock-chart"]'))
    expect(charts.every(chart => chart.getAttribute('data-length') === '2')).toBe(true)
  })

  it('lets operators add or remove lanes and filter transaction overlays without breaking synchronization', async () => {
    await act(async () => {
      root.render(
        <SynchronizedTimeline
          data={[samplePoint('00', 100, 0), samplePoint('01', 100, 0)]}
        />,
      )
    })

    const vusersButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'VUsers')
    expect(vusersButton?.getAttribute('aria-pressed')).toBe('true')
    await act(async () => vusersButton?.click())
    expect(container.querySelectorAll('[data-testid="mock-chart"]')).toHaveLength(3)
    expect(vusersButton?.getAttribute('aria-pressed')).toBe('false')

    const filter = container.querySelector<HTMLInputElement>('input[type="search"]')
    expect(filter).not.toBeNull()
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(filter, 'check')
      filter?.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(container.textContent).toContain('Checkout')
    expect(container.textContent).not.toContain('Search')

    const transactionTpsKey = transactionChartSeriesKey('Checkout', 'tps')
    expect(Array.from(container.querySelectorAll('[data-testid="mock-line"]')).some(line => line.getAttribute('data-key') === transactionTpsKey)).toBe(false)

    const showAll = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Show all')
    await act(async () => showAll?.click())
    expect(Array.from(container.querySelectorAll('[data-testid="mock-line"]')).some(line => line.getAttribute('data-key') === transactionTpsKey)).toBe(true)

    const hideAll = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Hide all')
    await act(async () => hideAll?.click())
    expect(Array.from(container.querySelectorAll('[data-testid="mock-line"]')).some(line => line.getAttribute('data-key') === transactionTpsKey)).toBe(false)

    const remainingCharts = Array.from(container.querySelectorAll('[data-testid="mock-chart"]'))
    expect(remainingCharts.every(chart => chart.getAttribute('data-sync-id') === 'metrics')).toBe(true)
  })
})
