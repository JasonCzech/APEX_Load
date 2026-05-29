// @vitest-environment jsdom

import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import BrowserBreakdownChart from './BrowserBreakdownChart'
import type { BrowserBreakdownDatum, BrowserBreakdownSeries } from './browserBreakdownModel'

vi.mock('recharts', () => ({
  CartesianGrid: () => <div data-testid="cartesian-grid" />,
  Legend: () => <div data-testid="legend" />,
  Line: ({ name }: { name?: string }) => <div data-testid="line-series">{name}</div>,
  LineChart: ({ children }: { children?: ReactNode }) => <div data-testid="line-chart">{children}</div>,
  ResponsiveContainer: ({ children }: { children?: ReactNode }) => <div data-testid="responsive-container">{children}</div>,
  Tooltip: () => <div data-testid="tooltip" />,
  XAxis: ({ label }: { label?: string | { value?: string; position?: string } }) => (
    <div
      data-testid="x-axis"
      data-label={typeof label === 'string' ? label : label?.value ?? ''}
      data-position={typeof label === 'string' ? '' : label?.position ?? ''}
    />
  ),
  YAxis: ({ label, orientation }: { label?: string | { value?: string; position?: string }; orientation?: string }) => (
    <div
      data-testid="y-axis"
      data-label={typeof label === 'string' ? label : label?.value ?? ''}
      data-position={typeof label === 'string' ? '' : label?.position ?? ''}
      data-orientation={orientation ?? 'left'}
    />
  ),
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('BrowserBreakdownChart', () => {
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

  it('labels segment, timing, and CLS axes', async () => {
    const data: BrowserBreakdownDatum[] = [
      { segment: 'Chromium', lcp: 180, inp: 42, cls: 0.01 },
      { segment: 'WebKit', lcp: 260, inp: 58, cls: 0.03 },
    ]
    const series: BrowserBreakdownSeries[] = [
      { dataKey: 'lcp', name: 'LCP', stroke: '#00a3ff', valueFormat: 'ms', yAxisId: 'timings' },
      { dataKey: 'inp', name: 'INP', stroke: '#00ff94', valueFormat: 'ms', yAxisId: 'timings' },
      { dataKey: 'cls', name: 'CLS', stroke: '#ffbe0b', valueFormat: 'score', yAxisId: 'cls' },
    ]

    await act(async () => {
      root.render(
        <BrowserBreakdownChart
          title="Browser Engine Comparison"
          data={data}
          series={series}
          segmentLabel="Engine"
        />,
      )
    })

    expect(container.querySelector('[data-testid="x-axis"]')?.getAttribute('data-label')).toBe('Engine')
    expect(Array.from(container.querySelectorAll('[data-testid="y-axis"]')).map(axis => axis.getAttribute('data-label'))).toEqual([
      'Timing (ms)',
      'CLS score',
    ])
    expect(Array.from(container.querySelectorAll('[data-testid="y-axis"]')).map(axis => axis.getAttribute('data-position'))).toEqual([
      'left',
      'right',
    ])
  })
})
