// @vitest-environment jsdom

import { Children, cloneElement, isValidElement, ReactNode, act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ReportCharts from './ReportCharts'
import { RunArchiveActionStats, RunArchiveReport } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

// The chart is exercised through a recharts stub that reproduces the two v3
// contracts this component depends on: BarChart hands its `data` to the Bar,
// and the Bar renders its `shape` once per datum with that datum as `payload`.
// A Bar without a `shape` falls back to its flat `fill`, which is exactly the
// "every bar paints the same colour" behaviour this test guards against.
vi.mock('recharts', () => {
  const Mock = ({ children }: { children?: ReactNode }) => <g>{children}</g>
  const MockChart = ({ children, data }: { children?: ReactNode; data?: unknown[] }) => (
    <svg data-testid="mock-chart">
      {Children.map(children, child => (isValidElement(child) ? cloneElement(child, { data } as never) : child))}
    </svg>
  )
  return {
    Area: Mock,
    AreaChart: MockChart,
    BarChart: MockChart,
    CartesianGrid: Mock,
    Line: Mock,
    LineChart: MockChart,
    Rectangle: ({ fill }: { fill?: string }) => <path data-testid="mock-bar-rect" data-fill={fill ?? ''} />,
    ResponsiveContainer: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
    Tooltip: Mock,
    XAxis: Mock,
    YAxis: Mock,
    Bar: ({ data = [], shape: Shape, fill }: { data?: { name: string; avg: number }[]; shape?: React.ComponentType<Record<string, unknown>>; fill?: string }) => (
      <g>
        {data.map((entry, index) => (
          <g key={entry.name} data-testid="mock-bar-cell" data-name={entry.name}>
            {Shape
              ? <Shape payload={entry} index={index} x={0} y={index * 12} width={Math.max(1, entry.avg)} height={10} />
              : <path data-testid="mock-bar-rect" data-fill={fill ?? ''} />}
          </g>
        ))}
      </g>
    ),
  }
})

const BASE_FILL = '#0f766e'
const HIGHLIGHT_FILL = '#b45309'

function actionStats(avgMs: number, transactions: number): RunArchiveActionStats {
  return {
    transactions,
    errors: 0,
    error_pct: 0,
    avg_response_ms: avgMs,
    min_response_ms: avgMs,
    max_response_ms: avgMs,
  }
}

function buildReport(runAverageMs: number): RunArchiveReport {
  return {
    overview: { avg_response_ms: runAverageMs },
    by_action: {
      search: actionStats(40, 30),
      login: actionStats(100, 20),
      checkout: actionStats(250, 10),
    },
  } as unknown as RunArchiveReport
}

function renderedBars() {
  return Array.from(document.querySelectorAll('[data-testid="mock-bar-cell"]')).map(cell => ({
    name: cell.getAttribute('data-name'),
    fill: cell.querySelector('[data-testid="mock-bar-rect"]')?.getAttribute('data-fill'),
  }))
}

describe('ReportCharts action bars', () => {
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

  it('paints each bar for its own datum instead of one colour for every bar', () => {
    act(() => {
      root.render(<ReportCharts report={buildReport(100)} />)
    })

    // Slowest-first, with the actions at or above the 100 ms run average
    // carrying the warning accent.
    expect(renderedBars()).toEqual([
      { name: 'checkout', fill: HIGHLIGHT_FILL },
      { name: 'login', fill: HIGHLIGHT_FILL },
      { name: 'search', fill: BASE_FILL },
    ])
    expect(new Set(renderedBars().map(bar => bar.fill)).size).toBeGreaterThan(1)
  })

  it('explains the highlight in the card so the encoding is not colour-alone', () => {
    act(() => {
      root.render(<ReportCharts report={buildReport(100)} />)
    })

    expect(document.querySelector('.report-chart-note')?.textContent)
      .toBe('Highlighted bars are at or above the run average of 100 ms.')
  })

  it('omits the highlight and its note when the run has no average to compare against', () => {
    act(() => {
      root.render(<ReportCharts report={buildReport(0)} />)
    })

    expect(renderedBars().map(bar => bar.fill)).toEqual([BASE_FILL, BASE_FILL, BASE_FILL])
    expect(document.querySelector('.report-chart-note')).toBeNull()
  })
})
