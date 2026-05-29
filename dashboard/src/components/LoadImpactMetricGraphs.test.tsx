// @vitest-environment jsdom

import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { MetricsHistoryPoint } from '../types'
import LoadImpactMetricGraphs, { buildMetricGraphRows } from './LoadImpactMetricGraphs'

vi.mock('recharts', () => ({
  CartesianGrid: () => <div data-testid="cartesian-grid" />,
  Line: ({ name }: { name: string }) => <div data-testid="line-series">{name}</div>,
  LineChart: ({ children, data }: { children: ReactNode; data: unknown[] }) => (
    <div data-testid="line-chart" data-points={data.length}>{children}</div>
  ),
  ResponsiveContainer: ({ children }: { children: ReactNode }) => (
    <div data-testid="responsive-container">{children}</div>
  ),
  Tooltip: () => <div data-testid="tooltip" />,
  XAxis: ({ label }: { label?: string | { value?: string; position?: string } }) => (
    <div
      data-testid="x-axis"
      data-label={typeof label === 'string' ? label : label?.value ?? ''}
      data-position={typeof label === 'string' ? '' : label?.position ?? ''}
    />
  ),
  YAxis: ({ label }: { label?: string | { value?: string; position?: string } }) => (
    <div
      data-testid="y-axis"
      data-label={typeof label === 'string' ? label : label?.value ?? ''}
      data-position={typeof label === 'string' ? '' : label?.position ?? ''}
    />
  ),
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('LoadImpactMetricGraphs', () => {
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

  it('builds graph rows from selected metrics only', () => {
    const rows = buildMetricGraphRows([
      point('10:00:00', 5, { avgMs: 100, tps: 3, p95: 130, cpuProcess: 20 }),
      point('10:00:01', 10, { avgMs: 140, tps: 4, p95: 180, cpuProcess: 30 }),
    ], ['avgMs', 'cpuProcess'])

    expect(rows.map(row => row.metric.id)).toEqual(['avgMs', 'cpuProcess'])
    expect(rows[0].cells.map(cell => cell.average)).toEqual([100, 140])
    expect(rows[1].cells.map(cell => cell.average)).toEqual([20, 30])
  })

  it('renders default metric graphs and opens the right-side selector', async () => {
    await act(async () => {
      root.render(
        <LoadImpactMetricGraphs
          points={[
            point('10:00:00', 5, { avgMs: 100, tps: 3, p95: 130, errorPct: 1, cpuProcess: 20 }),
            point('10:00:01', 10, { avgMs: 140, tps: 4, p95: 180, errorPct: 0, cpuProcess: 30 }),
          ]}
        />,
      )
    })

    expect(container.textContent).toContain('Metrics Under Load')
    expect(container.textContent).toContain('ART Under Load')
    expect(container.textContent).toContain('P95 Under Load')
    expect(container.textContent).toContain('TPS Under Load')
    expect(container.textContent).toContain('Error % Under Load')
    expect(container.textContent).toContain('CPU VUsers Under Load')
    expect(container.querySelectorAll('[data-testid="line-chart"]').length).toBe(5)
    expect(Array.from(container.querySelectorAll('[data-testid="x-axis"]')).map(axis => axis.getAttribute('data-label'))).toContain('VUser load')
    expect(Array.from(container.querySelectorAll('[data-testid="y-axis"]')).map(axis => axis.getAttribute('data-label'))).toContain('ART (ms)')
    expect(Array.from(container.querySelectorAll('[data-testid="y-axis"]')).map(axis => axis.getAttribute('data-position'))).not.toContain('insideLeft')

    await act(async () => {
      buttonByText('Configure Graphs')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.load-impact-graph-drawer')).not.toBeNull()
    expect(container.textContent).toContain('Graph Selection')
    expect(container.textContent).toContain('Performance')
    expect(container.textContent).toContain('Resources')
  })

  it('adds and removes graphs from the drawer', async () => {
    await act(async () => {
      root.render(
        <LoadImpactMetricGraphs
          points={[
            point('10:00:00', 5, { avgMs: 100, p95: 130, p99: 160, tps: 3 }),
            point('10:00:01', 10, { avgMs: 140, p95: 180, p99: 220, tps: 4 }),
          ]}
        />,
      )
    })

    await act(async () => {
      buttonByText('Configure Graphs')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const p99 = container.querySelector<HTMLInputElement>('input[value="p99"]')
    const art = container.querySelector<HTMLInputElement>('input[value="avgMs"]')
    expect(p99?.checked).toBe(false)
    expect(art?.checked).toBe(true)

    await act(async () => {
      p99?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      art?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('P99 Under Load')
    expect(container.textContent).not.toContain('ART Under Load')
  })

  it('shows loading and empty states without rendering charts', async () => {
    await act(async () => {
      root.render(<LoadImpactMetricGraphs points={[]} loading />)
    })
    expect(container.textContent).toContain('Loading load metric graphs')
    expect(container.querySelector('[data-testid="line-chart"]')).toBeNull()

    await act(async () => {
      root.render(<LoadImpactMetricGraphs points={[]} emptyMessage="No graph data." />)
    })
    expect(container.textContent).toContain('No graph data.')
  })

  function buttonByText(text: string) {
    return Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.trim() === text) ?? null
  }
})

function point(time: string, vusers: number, values: Partial<MetricsHistoryPoint> = {}): MetricsHistoryPoint {
  return {
    time,
    vusers,
    tps: undefined as unknown as number,
    avgMs: undefined as unknown as number,
    errorPct: undefined as unknown as number,
    p50: undefined as unknown as number,
    p90: undefined as unknown as number,
    p99: undefined as unknown as number,
    ...values,
  }
}
