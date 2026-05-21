// @vitest-environment jsdom

import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import TestExecutionCharts from './TestExecutionCharts'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('recharts', () => {
  const Mock = ({ children }: { children?: ReactNode }) => <g>{children}</g>
  const MockChart = ({ children }: { children?: ReactNode }) => (
    <svg data-testid="mock-chart" width="640" height="200">
      {children}
    </svg>
  )

  return {
    Area: ({ dataKey, name, yAxisId }: { dataKey?: string; name?: string; yAxisId?: string | number }) => (
      <path data-testid="mock-area" data-key={dataKey} data-name={name} data-y-axis-id={String(yAxisId ?? '')} />
    ),
    CartesianGrid: Mock,
    ComposedChart: MockChart,
    Legend: Mock,
    Line: ({ dataKey, name, yAxisId }: { dataKey?: string; name?: string; yAxisId?: string | number }) => (
      <path data-testid="mock-line" data-key={dataKey} data-name={name} data-y-axis-id={String(yAxisId ?? '')} />
    ),
    LineChart: MockChart,
    ReferenceArea: Mock,
    ResponsiveContainer: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
    Tooltip: Mock,
    XAxis: Mock,
    YAxis: Mock,
  }
})

describe('TestExecutionCharts context menu', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    window.localStorage.clear()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('opens graph actions from right click and resets zoom', async () => {
    const onResetZoom = vi.fn()

    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={[
            { time: '10:00:00', vusers: 2, tps: 1, avgMs: 100, errorPct: 0, p50: 80, p90: 100, p99: 130 },
            { time: '10:00:01', vusers: 4, tps: 2, avgMs: 110, errorPct: 0, p50: 85, p90: 105, p99: 135 },
          ]}
          expandedChart={null}
          onExpandedChartChange={vi.fn()}
          onChartMouseDown={vi.fn()}
          onChartMouseMove={vi.fn()}
          onZoom={vi.fn()}
          onResetZoom={onResetZoom}
          showRefArea={false}
          refAreaLeft=""
          refAreaEnd=""
          isMultiGroup={false}
          groupNames={[]}
          groupColors={['#00a3ff']}
        />,
      )
    })

    const firstChart = container.querySelector('.chart-card')
    expect(firstChart).not.toBeNull()

    await act(async () => {
      firstChart?.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 100, clientY: 120 }))
    })

    expect(container.querySelector('.chart-context-menu-title')?.textContent).toBe('Virtual Users')
    expect(menuActionTexts()).toContain('Reset Zoom')
    expect(menuActionTexts()).toContain('Download Snapshot')
    expect(menuActionTexts()).toContain('Export Graph Data')

    await act(async () => {
      firstChart?.dispatchEvent(new MouseEvent('mousemove', { bubbles: true, clientX: 130, clientY: 150 }))
      window.dispatchEvent(new Event('scroll'))
    })

    expect(container.querySelector('.chart-context-menu')).not.toBeNull()

    await act(async () => {
      menuButton('Reset Zoom')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onResetZoom).toHaveBeenCalledTimes(1)
    expect(container.querySelector('.chart-context-menu')).toBeNull()
  })

  it('toggles a VUsers overlay line from the graph context menu', async () => {
    await renderCharts()

    const tpsChart = container.querySelectorAll('.chart-card')[1]
    expect(tpsChart?.textContent).toContain('Transactions Per Second')

    await act(async () => {
      tpsChart?.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 180, clientY: 160 }))
    })

    expect(container.querySelector('.chart-context-menu-title')?.textContent).toBe('Transactions Per Second')
    expect(menuActionTexts()).toContain('Overlay VUsers')

    await act(async () => {
      menuButton('Overlay VUsers')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.chart-context-menu')).toBeNull()
    expect(container.querySelector('path[data-testid="mock-line"][data-name="Active VUsers"]')).not.toBeNull()
    expect(container.querySelector('path[data-testid="mock-area"][data-name="Active VUsers"]')).toBeNull()
    expect(tpsChart?.textContent).toContain('VUsers:')
    expect(window.localStorage.getItem('apexload.execution.vuserOverlayCharts')).toBe('["tps"]')

    await act(async () => {
      tpsChart?.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 180, clientY: 160 }))
    })

    expect(menuActionTexts()).toContain('Hide VUsers Overlay')
  })

  function menuActionTexts() {
    return Array.from(container.querySelectorAll('.chart-context-menu button')).map(button => button.textContent)
  }

  function menuButton(label: string) {
    return Array.from(container.querySelectorAll('.chart-context-menu button')).find(button => button.textContent === label)
  }

  async function renderCharts() {
    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={[
            { time: '10:00:00', vusers: 2, tps: 1, avgMs: 100, errorPct: 0, p50: 80, p90: 100, p99: 130 },
            { time: '10:00:01', vusers: 4, tps: 2, avgMs: 110, errorPct: 0, p50: 85, p90: 105, p99: 135 },
          ]}
          expandedChart={null}
          onExpandedChartChange={vi.fn()}
          onChartMouseDown={vi.fn()}
          onChartMouseMove={vi.fn()}
          onZoom={vi.fn()}
          onResetZoom={vi.fn()}
          showRefArea={false}
          refAreaLeft=""
          refAreaEnd=""
          isMultiGroup={false}
          groupNames={[]}
          groupColors={['#00a3ff']}
        />,
      )
    })
  }
})
