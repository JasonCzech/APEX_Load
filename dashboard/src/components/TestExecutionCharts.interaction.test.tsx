// @vitest-environment jsdom

import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { downloadTextFile } from '../utils/fileUtils'
import TestExecutionCharts from './TestExecutionCharts'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../utils/fileUtils', () => ({
  downloadTextFile: vi.fn(),
}))

vi.mock('recharts', () => {
  const Mock = ({ children }: { children?: ReactNode }) => <g>{children}</g>
  const labelText = (label?: string | { value?: string }) => (typeof label === 'string' ? label : label?.value ?? '')
  const labelPosition = (label?: string | { position?: string }) => (typeof label === 'string' ? '' : label?.position ?? '')
  const MockChart = ({
    children,
    data,
    syncId,
    onMouseDown,
    onMouseMove,
    onMouseUp,
  }: {
    children?: ReactNode
    data?: unknown[]
    syncId?: string
    onMouseDown?: (event: { activeLabel?: string; activeTooltipIndex?: number }) => void
    onMouseMove?: (event: { activeLabel?: string; activeTooltipIndex?: number }) => void
    onMouseUp?: () => void
  }) => (
    <svg
      data-testid="mock-chart"
      data-length={String(data?.length ?? '')}
      data-sync-id={syncId ?? ''}
      width="640"
      height="200"
      onMouseDown={() => onMouseDown?.({ activeLabel: '10:00:00', activeTooltipIndex: 0 })}
      onMouseMove={() => onMouseMove?.({ activeLabel: '10:00:01', activeTooltipIndex: 1 })}
      onMouseUp={() => onMouseUp?.()}
    >
      {children}
    </svg>
  )
  const MockTooltip = ({
    content,
    formatter,
    labelFormatter,
    labelStyle,
  }: {
    content?: () => ReactNode
    formatter?: (value: unknown, name: unknown) => ReactNode | [ReactNode, ReactNode]
    labelFormatter?: (value: string | number) => ReactNode
    labelStyle?: { display?: string }
  }) => {
    if (content) {
      return (
        <g
          data-testid="mock-tooltip"
          data-content-hidden="true"
          data-label-display="none"
          data-label-text=""
          data-formatted-value=""
        />
      )
    }
    const formatted = formatter?.(123.4567, 'TPS')
    const formattedValue = Array.isArray(formatted) ? formatted[0] : formatted
    return (
      <g
        data-testid="mock-tooltip"
        data-label-display={labelStyle?.display ?? ''}
        data-label-text={labelFormatter ? String(labelFormatter('10:00:01') ?? '') : ''}
        data-formatted-value={formattedValue === undefined ? '' : String(formattedValue)}
      />
    )
  }

  return {
    Area: ({ dataKey, name, yAxisId, fill }: { dataKey?: string; name?: string; yAxisId?: string | number; fill?: string }) => (
      <path data-testid="mock-area" data-key={dataKey} data-name={name} data-y-axis-id={String(yAxisId ?? '')} data-fill={fill ?? ''} />
    ),
    CartesianGrid: Mock,
    ComposedChart: MockChart,
    Legend: Mock,
    Line: ({
      dataKey,
      name,
      yAxisId,
      strokeWidth,
      strokeDasharray,
      strokeOpacity,
    }: {
      dataKey?: string
      name?: string
      yAxisId?: string | number
      strokeWidth?: number
      strokeDasharray?: string
      strokeOpacity?: number
    }) => (
      <path
        data-testid="mock-line"
        data-key={dataKey}
        data-name={name}
        data-y-axis-id={String(yAxisId ?? '')}
        data-stroke-width={String(strokeWidth ?? '')}
        data-stroke-dasharray={strokeDasharray ?? ''}
        data-stroke-opacity={String(strokeOpacity ?? '')}
      />
    ),
    LineChart: MockChart,
    ReferenceLine: ({ x, yAxisId }: { x?: string | number; yAxisId?: string | number }) => (
      <path data-testid="mock-reference-line" data-x={String(x ?? '')} data-y-axis-id={String(yAxisId ?? '')} />
    ),
    ReferenceArea: Mock,
    ResponsiveContainer: ({ children, height }: { children?: ReactNode; height?: number }) => <div data-testid="mock-responsive-container" data-height={String(height ?? '')}>{children}</div>,
    Tooltip: MockTooltip,
    XAxis: ({ dataKey, label }: { dataKey?: string; label?: string | { value?: string; position?: string } }) => (
      <g data-testid="mock-x-axis" data-key={dataKey ?? ''} data-label={labelText(label)} data-label-position={labelPosition(label)} />
    ),
    YAxis: ({
      yAxisId,
      label,
      orientation,
      domain,
      tick,
      ...props
    }: {
      yAxisId?: string | number
      label?: string | { value?: string; position?: string }
      orientation?: string
      domain?: Array<string | number>
      tick?: (props: { x: number; y: number; index: number; payload: { value: number } }) => ReactNode
      'aria-label'?: string
    }) => {
      const firstTick = typeof tick === 'function' ? tick({ x: 10, y: 20, index: 0, payload: { value: 0 } }) : null
      const secondTick = typeof tick === 'function' ? tick({ x: 10, y: 40, index: 1, payload: { value: 100 } }) : null
      const firstTickProps = (firstTick as { props?: Record<string, unknown> } | null)?.props
      return <g
        data-testid="mock-y-axis"
        data-y-axis-id={String(yAxisId ?? '')}
        data-orientation={orientation ?? 'left'}
        data-label={labelText(label)}
        data-label-position={labelPosition(label)}
        data-domain={Array.isArray(domain) ? domain.join('|') : ''}
        data-axis-aria-label={String(firstTickProps?.['aria-label'] ?? props['aria-label'] ?? '')}
      >
        {firstTick}
        {secondTick}
      </g>
    },
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
    vi.mocked(downloadTextFile).mockResolvedValue('saved')
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
          timeRange="3m"
          onTimeRangeChange={vi.fn()}
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

  it('opens graph actions from a keyboard-focusable button and restores focus on Escape', async () => {
    await renderCharts()
    const actionsButton = container.querySelector<HTMLButtonElement>('button[aria-label="Virtual Users actions"]')
    expect(actionsButton).not.toBeNull()
    if (!actionsButton) return
    actionsButton.focus()

    await act(async () => {
      actionsButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => {
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })

    const resetZoom = menuButton('Reset Zoom')
    expect(resetZoom).toBeDefined()
    expect(document.activeElement).toBe(resetZoom)
    expect(actionsButton.getAttribute('aria-expanded')).toBe('true')

    const menuItems = Array.from(container.querySelectorAll<HTMLButtonElement>('.chart-context-menu [role="menuitem"]'))
    await act(async () => {
      resetZoom?.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true }))
    })
    expect(document.activeElement).toBe(menuItems[1])
    await act(async () => {
      menuItems[1]?.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true, cancelable: true }))
    })
    expect(document.activeElement).toBe(resetZoom)

    await act(async () => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })

    expect(container.querySelector('.chart-context-menu')).toBeNull()
    expect(document.activeElement).toBe(actionsButton)
  })

  it('surfaces a native chart export failure from the graph action caller', async () => {
    vi.mocked(downloadTextFile).mockRejectedValueOnce(new Error('native save failed'))
    await renderCharts()

    const firstChart = container.querySelector('.chart-card')
    await act(async () => {
      firstChart?.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 100, clientY: 120 }))
    })
    await act(async () => {
      menuButton('Export Graph Data')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(downloadTextFile).toHaveBeenCalledTimes(1)
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('Could not save the chart file: native save failed')
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
    expect(tpsChart?.textContent).toContain('Active VUsers')
    expect(window.localStorage.getItem('apexload.execution.vuserOverlayCharts')).toBe('["tps"]')

    await act(async () => {
      tpsChart?.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 180, clientY: 160 }))
    })

    expect(menuActionTexts()).toContain('Hide VUsers Overlay')
  })

  it('toggles ART and TPS overlay lines from the graph context menu', async () => {
    await renderCharts()

    const vusersChart = container.querySelectorAll('.chart-card')[0]
    expect(vusersChart?.textContent).toContain('Virtual Users')
    expect(vusersChart?.querySelector('[data-testid="mock-x-axis"]')?.getAttribute('data-label')).toBe('')
    expect(axisLabels(vusersChart)).toEqual([])
    expect(axisAriaLabels(vusersChart)).toContain('Show VUsers axis label')
    const vuserAxis = vusersChart?.querySelector('[data-testid="mock-y-axis"]')
    expect(vuserAxis?.querySelectorAll('[role="button"][tabindex="0"]')).toHaveLength(1)

    await act(async () => {
      vuserAxis?.querySelector<SVGTextElement>('[role="button"]')?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    })

    expect(container.querySelector('.chart-axis-label-popover-title')?.textContent).toBe('Y-axis')
    expect(container.querySelector('.chart-axis-label-popover-value')?.textContent).toBe('VUsers')

    await act(async () => {
      vusersChart?.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 180, clientY: 160 }))
    })

    expect(menuActionTexts()).not.toContain('Overlay VUsers')
    expect(menuActionTexts()).toContain('Overlay ART')
    expect(menuActionTexts()).toContain('Overlay TPS')

    await act(async () => {
      menuButton('Overlay ART')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(vusersChart?.querySelector('path[data-testid="mock-line"][data-name="ART"]')).not.toBeNull()
    expect(vusersChart?.querySelector('.chart-title')?.textContent).toBe('Virtual Users + ART')
    expect(axisLabels(vusersChart)).toEqual([])
    expect(axisAriaLabels(vusersChart)).toEqual(expect.arrayContaining(['Show VUsers axis label', 'Show ART (ms) axis label']))
    expect(window.localStorage.getItem('apexload.execution.artOverlayCharts')).toBe('["vusers"]')

    await act(async () => {
      vusersChart?.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 180, clientY: 160 }))
    })

    expect(menuActionTexts()).toContain('Hide ART Overlay')
    expect(menuActionTexts()).toContain('Overlay TPS')
    expect(container.querySelector('.chart-context-menu-title')?.textContent).toBe('Virtual Users + ART')

    await act(async () => {
      menuButton('Overlay TPS')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(vusersChart?.querySelector('path[data-testid="mock-line"][data-name="TPS"]')).not.toBeNull()
    expect(vusersChart?.querySelector('.chart-title')?.textContent).toBe('Virtual Users + ART + TPS')
    expect(axisLabels(vusersChart)).toEqual([])
    expect(axisAriaLabels(vusersChart)).toContain('Show ART (ms) / TPS axis label')
    expect(window.localStorage.getItem('apexload.execution.tpsOverlayCharts')).toBe('["vusers"]')
  })

  it('keeps synced graph tooltips while only the hovered chart displays the time label', async () => {
    await renderCharts()

    const mockCharts = Array.from(container.querySelectorAll('[data-testid="mock-chart"]'))
    expect(mockCharts).toHaveLength(14)
    expect(mockCharts.map(chart => chart.getAttribute('data-sync-id'))).toEqual([...Array(11).fill('metrics'), '', '', ''])
    expect(Array.from(container.querySelectorAll('[data-testid="mock-tooltip"]')).map(tooltip => tooltip.getAttribute('data-label-display'))).toEqual(Array(14).fill('none'))

    const tpsChart = mockCharts[1]
    await act(async () => {
      tpsChart.dispatchEvent(new MouseEvent('mousemove', { bubbles: true }))
      await flushAnimationFrame()
    })

    const tooltips = Array.from(container.querySelectorAll('[data-testid="mock-tooltip"]'))
    expect(tooltips.map(tooltip => tooltip.getAttribute('data-label-display'))).toEqual([
      'none',
      '',
      ...Array(12).fill('none'),
    ])
    expect(tooltips.map(tooltip => tooltip.getAttribute('data-label-text'))).toEqual([
      '',
      '10:00:01',
      ...Array(12).fill(''),
    ])
    expect(tooltips.map(tooltip => tooltip.getAttribute('data-formatted-value'))).toEqual([
      ...Array(11).fill('123.46'),
      '123',
      '123',
      '123',
    ])
  })

  it('filters graph series from the metric table rows', async () => {
    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={[
            {
              time: '10:00:00',
              vusers: 2,
              tps: 3,
              avgMs: 100,
              errorPct: 0,
              p50: 80,
              p90: 100,
              p99: 130,
              Checkout_tps: 2,
              Search_tps: 1,
              Checkout_avgMs: 90,
              Search_avgMs: 120,
            },
            {
              time: '10:00:01',
              vusers: 4,
              tps: 6,
              avgMs: 110,
              errorPct: 0,
              p50: 85,
              p90: 105,
              p99: 135,
              Checkout_tps: 4,
              Search_tps: 2,
              Checkout_avgMs: 95,
              Search_avgMs: 125,
            },
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
          timeRange="3m"
          onTimeRangeChange={vi.fn()}
          isMultiGroup
          groupNames={['Checkout', 'Search']}
          groupColors={['#00a3ff', '#ffaa00']}
        />,
      )
    })

    const tpsChart = Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.textContent?.includes('Transactions Per Second'))
    expect(tpsChart?.querySelector('path[data-testid="mock-line"][data-key="Checkout_tps"]')).not.toBeNull()
    expect(tpsChart?.querySelector('path[data-testid="mock-line"][data-key="Search_tps"]')).not.toBeNull()
    expect(tpsChart?.textContent).toContain('Avg')
    expect(tpsChart?.textContent).toContain('Max')

    const responseChart = Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.textContent?.includes('Response Time (ms)'))
    expect(responseChart?.querySelector('path[data-testid="mock-line"][data-key="avgMs"][data-name="Total"]')).not.toBeNull()
    expect(responseChart?.querySelector('path[data-testid="mock-line"][data-key="Checkout_avgMs"][data-name="Checkout"]')).not.toBeNull()
    expect(responseChart?.querySelector('path[data-testid="mock-line"][data-key="Search_avgMs"][data-name="Search"]')).not.toBeNull()
    expect(responseChart?.textContent).toContain('Total')
    expect(responseChart?.textContent).toContain('Checkout')
    expect(responseChart?.textContent).toContain('Search')

    const checkoutHighlight = Array.from(tpsChart?.querySelectorAll<HTMLButtonElement>('button.chart-metric-name') ?? [])
      .find(button => button.textContent?.includes('Checkout'))
    expect(checkoutHighlight?.getAttribute('aria-pressed')).toBe('false')
    await act(async () => {
      checkoutHighlight?.click()
    })
    expect(checkoutHighlight?.getAttribute('aria-pressed')).toBe('true')

    const checkoutToggle = Array.from(tpsChart?.querySelectorAll<HTMLInputElement>('input[type="checkbox"]') ?? [])
      .find(input => input.getAttribute('aria-label') === 'Show Checkout')
    expect(checkoutToggle).not.toBeUndefined()

    await act(async () => {
      checkoutToggle?.click()
    })

    expect(tpsChart?.querySelector('path[data-testid="mock-line"][data-key="Checkout_tps"]')).toBeNull()
    expect(tpsChart?.querySelector('path[data-testid="mock-line"][data-key="Search_tps"]')).not.toBeNull()
    expect(window.localStorage.getItem('apexload.execution.chartMetricFilters')).toContain('"tps":["tps","Search_tps"]')
  })

  it('exposes chart labels, hidden measurement tables, and terminal status cues', async () => {
    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={[
            { time: '10:00:00', vusers: 2, tps: 3, avgMs: 100, errorPct: 0, p50: 80, p90: 100, p99: 130 },
            { time: '10:00:01', vusers: 4, tps: 6, avgMs: 110, errorPct: 0, p50: 85, p90: 105, p99: 135 },
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
          timeRange="3m"
          onTimeRangeChange={vi.fn()}
          isMultiGroup={false}
          groupNames={[]}
          groupColors={['#00a3ff']}
          terminalStatus="FAILED"
        />,
      )
    })

    expect(container.querySelector('.chart-terminal-status')?.textContent).toContain('Run ended with FAILED')
    const tpsChart = Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.textContent?.includes('Transactions Per Second'))
    const chartVisual = tpsChart?.querySelector('.chart-visual')
    const tableId = chartVisual?.getAttribute('aria-describedby')
    expect(chartVisual?.getAttribute('role')).toBe('img')
    expect(chartVisual?.getAttribute('aria-label')).toContain('Transactions Per Second chart. 2 samples.')
    expect(tableId).toBeTruthy()
    expect(tpsChart?.querySelector(`#${tableId}`)?.textContent).toContain('Transactions Per Second measurements')
    expect(tpsChart?.querySelector(`#${tableId}`)?.textContent).toContain('Latest')
    expect(tpsChart?.querySelector('button[aria-label="Expand Transactions Per Second"]')).not.toBeNull()
  })

  it('adds dash patterns when group colors repeat', async () => {
    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={[
            {
              time: '10:00:00',
              vusers: 2,
              tps: 6,
              avgMs: 100,
              errorPct: 0,
              p50: 80,
              p90: 100,
              p99: 130,
              Checkout_tps: 2,
              Search_tps: 1,
              Browse_tps: 3,
              Checkout_avgMs: 90,
              Search_avgMs: 120,
              Browse_avgMs: 105,
            },
            {
              time: '10:00:01',
              vusers: 4,
              tps: 9,
              avgMs: 110,
              errorPct: 0,
              p50: 85,
              p90: 105,
              p99: 135,
              Checkout_tps: 4,
              Search_tps: 2,
              Browse_tps: 3,
              Checkout_avgMs: 95,
              Search_avgMs: 125,
              Browse_avgMs: 112,
            },
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
          timeRange="3m"
          onTimeRangeChange={vi.fn()}
          isMultiGroup
          groupNames={['Checkout', 'Search', 'Browse']}
          groupColors={['#00a3ff', '#ffaa00']}
        />,
      )
    })

    const tpsChart = Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.textContent?.includes('Transactions Per Second'))
    expect(tpsChart?.querySelector('path[data-testid="mock-line"][data-key="Checkout_tps"]')?.getAttribute('data-stroke-dasharray')).toBe('')
    expect(tpsChart?.querySelector('path[data-testid="mock-line"][data-key="Browse_tps"]')?.getAttribute('data-stroke-dasharray')).toBe('5 3')
    const responseChart = Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.textContent?.includes('Response Time (ms)'))
    expect(responseChart?.querySelector('path[data-testid="mock-line"][data-key="Browse_avgMs"]')?.getAttribute('data-stroke-dasharray')).toBe('5 3')
    expect(responseChart?.querySelector('.chart-series-legend-item.dashed')?.textContent).toContain('Browse')
  })

  it('rescales the graph axis when a dominant visible metric is hidden', async () => {
    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={[
            { time: '10:00:00', vusers: 2, tps: 3, avgMs: 100, errorPct: 0, p50: 80, p90: 100, p95: 115, p99: 2200 },
            { time: '10:00:01', vusers: 4, tps: 6, avgMs: 110, errorPct: 0, p50: 85, p90: 105, p95: 118, p99: 2400 },
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
          timeRange="3m"
          onTimeRangeChange={vi.fn()}
          isMultiGroup={false}
          groupNames={[]}
          groupColors={['#00a3ff']}
        />,
      )
    })

    const responseChart = Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.textContent?.includes('Response Time (ms)'))
    expect(primaryAxisDomain(responseChart)).toBe('0|2592')
    expect(responseChart?.querySelector('path[data-testid="mock-line"][data-key="p99"]')).not.toBeNull()

    const p99Toggle = Array.from(responseChart?.querySelectorAll<HTMLInputElement>('input[type="checkbox"]') ?? [])
      .find(input => input.getAttribute('aria-label') === 'Show P99')
    expect(p99Toggle).not.toBeUndefined()

    await act(async () => {
      p99Toggle?.click()
    })

    expect(responseChart?.querySelector('path[data-testid="mock-line"][data-key="p99"]')).toBeNull()
    expect(primaryAxisDomain(responseChart)).toBe('0|127.44')
    expect(window.localStorage.getItem('apexload.execution.chartMetricFilters')).toContain('"latency":["avgMs","p50","p90","p95"]')
  })

  it('toggles and persists ART rolling standard-deviation bands', async () => {
    await renderCharts()

    let responseChart = Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.textContent?.includes('Response Time (ms)'))
    const bandToggleLabel = () => Array.from(responseChart?.querySelectorAll<HTMLLabelElement>('.chart-card-checkbox') ?? [])
      .find(label => label.textContent?.includes('σ Bands'))
    const bandToggle = () => bandToggleLabel()?.querySelector<HTMLInputElement>('input[type="checkbox"]')

    expect(bandToggle()?.checked).toBe(false)
    expect(responseChart?.querySelector('path[data-testid="mock-area"][data-key="avgMsStdDevBand"]')).toBeNull()

    await act(async () => {
      bandToggle()?.click()
    })

    expect(bandToggle()?.checked).toBe(true)
    expect(responseChart?.querySelector('path[data-testid="mock-area"][data-key="avgMsStdDevBand"][data-name="±1σ"]')).not.toBeNull()
    expect(window.localStorage.getItem('apexload.execution.artStdDevBands')).toBe('true')

    await act(async () => {
      root.unmount()
    })
    root = createRoot(container)
    await renderCharts()

    responseChart = Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.textContent?.includes('Response Time (ms)'))
    expect(bandToggle()?.checked).toBe(true)
    expect(responseChart?.querySelector('path[data-testid="mock-area"][data-key="avgMsStdDevBand"]')).not.toBeNull()
  })

  it('separates ART transaction and percentile legend groups', async () => {
    await renderCharts()

    const responseChart = Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.textContent?.includes('Response Time (ms)'))
    expect(responseChart?.querySelector('.chart-series-legend')?.textContent).toContain('Transactions')
    expect(responseChart?.querySelector('.chart-series-legend')?.textContent).toContain('Percentiles')
    expect(responseChart?.querySelector('path[data-testid="mock-line"][data-name="Total"]')?.getAttribute('data-stroke-width')).toBe('2.5')
    expect(responseChart?.querySelector('path[data-testid="mock-line"][data-name="Total"]')?.getAttribute('data-stroke-dasharray')).toBe('')
    expect(responseChart?.querySelector('path[data-testid="mock-line"][data-name="P95"]')?.getAttribute('data-stroke-width')).toBe('1.25')
    expect(responseChart?.querySelector('path[data-testid="mock-line"][data-name="P95"]')?.getAttribute('data-stroke-dasharray')).toBe('4 2')
    expect(responseChart?.querySelector('path[data-testid="mock-line"][data-name="P95"]')?.getAttribute('data-stroke-opacity')).toBe('0.68')
  })

  it('renders load-under graphs as selectable runtime graph cards', async () => {
    await renderCharts(null, false)

    const artUnderLoad = () => Array.from(container.querySelectorAll('.chart-card'))
      .find(chart => chart.textContent?.includes('ART Under Load'))

    expect(chartTitles()).toContain('ART Under Load')
    expect(chartTitles()).toContain('TPS Under Load')
    expect(chartTitles()).toContain('Error % Under Load')
    expect(chartTitles()).toContain('Load Impact Heatmap')
    expect(chartTitles()).not.toContain('P Values Under Load')
    expect(chartTitles()).not.toContain('P99 Under Load')
    expect(artUnderLoad()?.querySelector('[data-testid="mock-x-axis"]')?.getAttribute('data-label')).toBe('')
    expect(artUnderLoad()?.querySelector('path[data-testid="mock-line"][data-key="average"][data-name="Average"]')).not.toBeNull()
    expect(artUnderLoad()?.querySelector('path[data-testid="mock-line"][data-key="max"][data-name="Max"]')).toBeNull()

    const maxToggle = Array.from(artUnderLoad()?.querySelectorAll<HTMLInputElement>('input[type="checkbox"]') ?? [])
      .find(input => input.getAttribute('aria-label') === 'Show Max')
    expect(maxToggle).not.toBeUndefined()

    await act(async () => {
      maxToggle?.click()
    })

    expect(artUnderLoad()?.querySelector('path[data-testid="mock-line"][data-key="max"][data-name="Max"]')).not.toBeNull()
    expect(window.localStorage.getItem('apexload.execution.chartMetricFilters')).toContain('"load_impact_avgMs":["average","max"]')

    await act(async () => {
      buttonByText('Configure Graphs')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const p99Toggle = container.querySelector<HTMLInputElement>('input[aria-label="Show P99 Under Load"]')
    expect(p99Toggle?.checked).toBe(false)

    await act(async () => {
      p99Toggle?.click()
    })

    expect(chartTitles()).toContain('P99 Under Load')
    expect(window.localStorage.getItem('apexload.execution.visibleLoadImpactCharts')).toContain('load_impact_p99')

    const pValuesToggle = container.querySelector<HTMLInputElement>('input[aria-label="Show P Values Under Load"]')
    expect(pValuesToggle?.checked).toBe(false)

    await act(async () => {
      pValuesToggle?.click()
    })

    const pValuesUnderLoad = () => Array.from(container.querySelectorAll('.chart-card'))
      .find(chart => chart.textContent?.includes('P Values Under Load'))
    expect(chartTitles()).toContain('P Values Under Load')
    expect(pValuesUnderLoad()?.querySelector('path[data-testid="mock-line"][data-key="p50"][data-name="P50"]')).not.toBeNull()
    expect(pValuesUnderLoad()?.querySelector('path[data-testid="mock-line"][data-key="p90"][data-name="P90"]')).not.toBeNull()
    expect(pValuesUnderLoad()?.querySelector('path[data-testid="mock-line"][data-key="p95"][data-name="P95"]')).not.toBeNull()
    expect(pValuesUnderLoad()?.querySelector('path[data-testid="mock-line"][data-key="p99"][data-name="P99"]')).not.toBeNull()
    expect(window.localStorage.getItem('apexload.execution.visibleLoadImpactCharts')).toContain('load_impact_percentiles')

    const p90Toggle = Array.from(pValuesUnderLoad()?.querySelectorAll<HTMLInputElement>('input[type="checkbox"]') ?? [])
      .find(input => input.getAttribute('aria-label') === 'Show P90')
    expect(p90Toggle).not.toBeUndefined()

    await act(async () => {
      p90Toggle?.click()
    })

    expect(pValuesUnderLoad()?.querySelector('path[data-testid="mock-line"][data-key="p90"][data-name="P90"]')).toBeNull()
    expect(window.localStorage.getItem('apexload.execution.chartMetricFilters')).toContain('"load_impact_percentiles":["p50","p95","p99"]')
  })

  it('shows the load impact heatmap in Under Load by default and lets it be hidden', async () => {
    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={[
            { time: '10:00:00', vusers: 5, tps: 2.5, avgMs: 115, errorPct: 0, p50: 105, p90: 132, p95: 145, p99: 180 },
            { time: '10:01:00', vusers: 15, tps: 6.7, avgMs: 150, errorPct: 0.5, p50: 138, p90: 196, p95: 218, p99: 268 },
            { time: '10:02:00', vusers: 30, tps: 11.6, avgMs: 224, errorPct: 1.1, p50: 202, p90: 308, p95: 340, p99: 410 },
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
          timeRange="3m"
          onTimeRangeChange={vi.fn()}
          isMultiGroup={false}
          groupNames={[]}
          groupColors={['#00a3ff']}
          showLoadImpact={false}
          enableGraphGroupTabs
        />,
      )
    })

    const underLoadTab = Array.from(container.querySelectorAll('button[role="tab"]')).find(button => button.textContent?.includes('Under Load'))
    expect(underLoadTab?.textContent).toContain('5')

    await act(async () => {
      underLoadTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(chartTitles()).toContain('Load Impact Heatmap')
    const heatmapCard = container.querySelector('.load-impact-card.load-impact-card-graph')
    expect(heatmapCard).not.toBeNull()
    expect(heatmapCard?.className).toContain('load-impact-graph-card-span-2')

    await act(async () => {
      buttonByText('Configure Graphs')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const heatmapToggle = container.querySelector<HTMLInputElement>('input[aria-label="Show Load Impact Heatmap"]')
    expect(heatmapToggle?.checked).toBe(true)

    await act(async () => {
      heatmapToggle?.click()
    })

    expect(chartTitles()).not.toContain('Load Impact Heatmap')
    expect(window.localStorage.getItem('apexload.execution.hiddenCharts')).toContain('load_impact_heatmap')
  })

  it('shows Prometheus graphs as a graph group and supports metric row filtering', async () => {
    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={[
            { time: '10:00:00', timestampMs: 1781272800000, vusers: 2, tps: 1, avgMs: 100, errorPct: 0, p50: 80, p90: 100, p99: 130 },
            { time: '10:00:15', timestampMs: 1781272815000, vusers: 4, tps: 2, avgMs: 110, errorPct: 0, p50: 85, p90: 105, p99: 135 },
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
          timeRange="3m"
          onTimeRangeChange={vi.fn()}
          isMultiGroup={false}
          groupNames={[]}
          groupColors={['#00a3ff']}
          showLoadImpact={false}
          enableGraphGroupTabs
          prometheusGraphs={[
            {
              id: 'prometheus_scaling_vusers',
              title: 'Scaling VUsers',
              unit: 'VUsers',
              series: [
                {
                  key: 'prometheus_scaling_vusers_state_active',
                  label: 'active',
                  values: [
                    { timestamp: '2026-06-12T14:00:00Z', timestamp_ms: 1781272800000, value: 12 },
                    { timestamp: '2026-06-12T14:00:15Z', timestamp_ms: 1781272815000, value: 18 },
                  ],
                },
                {
                  key: 'prometheus_scaling_vusers_state_pending',
                  label: 'pending',
                  values: [
                    { timestamp: '2026-06-12T14:00:00Z', timestamp_ms: 1781272800000, value: 3 },
                    { timestamp: '2026-06-12T14:00:15Z', timestamp_ms: 1781272815000, value: 1 },
                  ],
                },
              ],
            },
          ]}
        />,
      )
    })

    const prometheusTab = Array.from(container.querySelectorAll('button[role="tab"]')).find(button => button.textContent?.includes('Prometheus'))
    expect(prometheusTab?.textContent).toContain('1')

    await act(async () => {
      prometheusTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(chartTitles()).toEqual(['Scaling VUsers'])
    const prometheusCard = Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.textContent?.includes('Scaling VUsers'))
    expect(prometheusCard?.querySelector('path[data-testid="mock-line"][data-key="prometheus_scaling_vusers_state_active"][data-name="active"]')).not.toBeNull()
    expect(prometheusCard?.querySelector('path[data-testid="mock-line"][data-key="prometheus_scaling_vusers_state_pending"][data-name="pending"]')).not.toBeNull()

    const pendingToggle = Array.from(prometheusCard?.querySelectorAll<HTMLInputElement>('input[type="checkbox"]') ?? [])
      .find(input => input.getAttribute('aria-label') === 'Show pending')
    expect(pendingToggle).not.toBeUndefined()

    await act(async () => {
      pendingToggle?.click()
    })

    expect(prometheusCard?.querySelector('path[data-testid="mock-line"][data-key="prometheus_scaling_vusers_state_active"][data-name="active"]')).not.toBeNull()
    expect(prometheusCard?.querySelector('path[data-testid="mock-line"][data-key="prometheus_scaling_vusers_state_pending"][data-name="pending"]')).toBeNull()
    expect(window.localStorage.getItem('apexload.execution.chartMetricFilters')).toContain('"prometheus_scaling_vusers":["prometheus_scaling_vusers_state_active"]')
  })

  it('uses dedicated Under Load heatmap mode with map controls and detail rows', async () => {
    const data = [
      { time: '10:00:00', vusers: 5, tps: 2.5, avgMs: 115, errorPct: 0, p50: 105, p90: 132, p95: 145, p99: 180 },
      { time: '10:01:00', vusers: 15, tps: 6.7, avgMs: 150, errorPct: 0.5, p50: 138, p90: 196, p95: 218, p99: 268 },
      { time: '10:02:00', vusers: 30, tps: 11.6, avgMs: 224, errorPct: 1.1, p50: 202, p90: 308, p95: 340, p99: 410 },
    ]

    const renderUnderLoadMode = async (underLoadDisplayMode: 'graphs' | 'heatmap') => {
      await act(async () => {
        root.render(
          <TestExecutionCharts
            filteredData={data}
            expandedChart={null}
            onExpandedChartChange={vi.fn()}
            onChartMouseDown={vi.fn()}
            onChartMouseMove={vi.fn()}
            onZoom={vi.fn()}
            onResetZoom={vi.fn()}
            showRefArea={false}
            refAreaLeft=""
            refAreaEnd=""
            timeRange="3m"
            onTimeRangeChange={vi.fn()}
            isMultiGroup={false}
            groupNames={[]}
            groupColors={['#00a3ff']}
            showLoadImpact={false}
            surface="command-center"
            storageScope="apexload.commandCenter.grafana"
            enableGraphGroupTabs
            activeGraphGroupTab="under_load"
            underLoadDisplayMode={underLoadDisplayMode}
          />,
        )
      })
    }

    await renderUnderLoadMode('graphs')

    expect(chartTitles()).toContain('ART Under Load')
    expect(chartTitles()).not.toContain('Load Impact Heatmap')
    expect(container.querySelector('.load-impact-card.load-impact-map-mode')).toBeNull()

    await renderUnderLoadMode('heatmap')

    expect(chartTitles()).toEqual(['Load Impact Heatmap'])
    const heatmapCard = container.querySelector('.load-impact-card.load-impact-map-mode')
    expect(heatmapCard).not.toBeNull()
    expect(heatmapCard?.className).toContain('load-impact-graph-card-span-3')
    expect(heatmapCard?.textContent).toContain('2D')
    expect(heatmapCard?.textContent).toContain('3D')

    const enabledCell = Array.from(heatmapCard!.querySelectorAll<HTMLButtonElement>('.load-impact-cell')).find(cell => !cell.disabled)
    expect(enabledCell).toBeDefined()

    await act(async () => {
      enabledCell?.click()
    })

    expect(heatmapCard?.querySelector('.load-impact-detail')).not.toBeNull()
    expect(heatmapCard?.textContent).toContain('data points')
  })

  it('applies default load-under visibility when old graph preferences are stored', async () => {
    window.localStorage.setItem('apexload.execution.hiddenCharts', '[]')

    await renderCharts(null, false)

    expect(chartTitles()).toContain('ART Under Load')
    expect(chartTitles()).toContain('TPS Under Load')
    expect(chartTitles()).not.toContain('P99 Under Load')
    expect(window.localStorage.getItem('apexload.execution.hiddenCharts')).toContain('load_impact_p99')
    expect(window.localStorage.getItem('apexload.execution.visibleLoadImpactCharts')).toBe('[]')
  })

  it('applies default load-under visibility when archive samples arrive after mount', async () => {
    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={[]}
          loadImpactData={[]}
          expandedChart={null}
          onExpandedChartChange={vi.fn()}
          onChartMouseDown={vi.fn()}
          onChartMouseMove={vi.fn()}
          onZoom={vi.fn()}
          onResetZoom={vi.fn()}
          showRefArea={false}
          refAreaLeft=""
          refAreaEnd=""
          timeRange="3m"
          onTimeRangeChange={vi.fn()}
          isMultiGroup={false}
          groupNames={[]}
          groupColors={['#00a3ff']}
          showLoadImpact={false}
        />,
      )
    })

    expect(container.querySelector('.chart-card')).toBeNull()

    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={[]}
          loadImpactData={[
            { time: '10:00:00', vusers: 5, tps: 2.5, avgMs: 115, errorPct: 0, p50: 105, p90: 132, p95: 145, p99: 180 },
            { time: '10:01:00', vusers: 15, tps: 6.7, avgMs: 150, errorPct: 0.5, p50: 138, p90: 196, p95: 218, p99: 268 },
            { time: '10:02:00', vusers: 30, tps: 11.6, avgMs: 224, errorPct: 1.1, p50: 202, p90: 308, p95: 340, p99: 410 },
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
          timeRange="3m"
          onTimeRangeChange={vi.fn()}
          isMultiGroup={false}
          groupNames={[]}
          groupColors={['#00a3ff']}
          showLoadImpact={false}
        />,
      )
    })

    expect(chartTitles()).toContain('ART Under Load')
    expect(chartTitles()).toContain('P95 Under Load')
    expect(chartTitles()).not.toContain('P99 Under Load')
  })

  it('moves the expanded graph to the top of the graph grid', async () => {
    await renderCharts('latency')

    let titles = chartTitles()
    expect(titles[0]).toBe('Response Time (ms)')
    expect(container.querySelector('.chart-card')?.className).toContain('expanded')

    await renderCharts(null)

    titles = chartTitles()
    expect(titles[0]).toBe('Virtual Users')
    expect(titles[1]).toBe('Transactions Per Second')
  })

  it('can suppress the chart-grid Load Impact card when the archive report owns it', async () => {
    await renderCharts(null, false)

    expect(container.querySelector('.load-impact-card:not(.load-impact-card-graph)')).toBeNull()
    expect(container.querySelector('.load-impact-card.load-impact-card-graph')).not.toBeNull()
    expect(chartTitles()[0]).toBe('Virtual Users')
  })

  it('rearranges graph cards by dragging a chart grabber', async () => {
    await renderCharts()

    expect(chartTitles().slice(0, 3)).toEqual([
      'Virtual Users',
      'Transactions Per Second',
      'Response Time (ms)',
    ])

    const cards = () => Array.from(container.querySelectorAll<HTMLElement>('.chart-card'))
    const responseCard = cards().find(card => card.textContent?.includes('Response Time (ms)'))
    const vusersCard = cards().find(card => card.textContent?.includes('Virtual Users'))
    const responseGrabber = responseCard?.querySelector<HTMLElement>('.chart-drag-handle')
    expect(responseGrabber).not.toBeNull()

    const dataTransfer = createDragDataTransfer()
    await act(async () => {
      dispatchDragEvent(responseGrabber!, 'dragstart', dataTransfer)
      dispatchDragEvent(vusersCard!, 'dragover', dataTransfer, { clientX: 0, clientY: 0 })
      dispatchDragEvent(vusersCard!, 'drop', dataTransfer, { clientX: 0, clientY: 0 })
      dispatchDragEvent(responseGrabber!, 'dragend', dataTransfer)
    })

    expect(chartTitles()[0]).toBe('Response Time (ms)')
    expect(window.localStorage.getItem('apexload.execution.chartOrder')).toContain('["latency","vusers","tps"')
  })

  it('rearranges graph cards from the grabber keyboard controls', async () => {
    await renderCharts()

    const tpsGrabber = Array.from(container.querySelectorAll<HTMLElement>('.chart-card'))
      .find(card => card.textContent?.includes('Transactions Per Second'))
      ?.querySelector<HTMLElement>('.chart-drag-handle')
    expect(tpsGrabber).not.toBeNull()

    await act(async () => {
      tpsGrabber?.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: 'Home' }))
    })

    expect(chartTitles()[0]).toBe('Transactions Per Second')
    expect(window.localStorage.getItem('apexload.execution.chartOrder')).toContain('["tps","vusers"')
  })

  it('opens final graph settings from a Configure Graphs drawer', async () => {
    await renderCharts(null, false)

    expect(container.querySelector('.charts-customize-bar')).toBeNull()
    expect(container.querySelector('.charts-customize-actions')).toBeNull()
    expect(container.querySelector('.monitor-preset-buttons')).toBeNull()
    expect(container.querySelector('.charts-customize-panel')).toBeNull()
    expect(container.querySelector('.charts-toolbar')?.textContent).toContain('Runtime Graphs')
    expect(container.querySelector('.charts-toolbar')?.textContent).toContain('15 graphs')
    expect(container.querySelector('.charts-config-drawer')).toBeNull()

    await act(async () => {
      buttonByText('Configure Graphs')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const drawer = container.querySelector('.charts-config-drawer')
    expect(drawer?.textContent).toContain('Graph Configuration')
    expect(drawer?.textContent).toContain('Graph Cards')
    expect(drawer?.textContent).toContain('Display Window')
    expect(drawer?.textContent).not.toContain('Max')
    expect(drawer?.textContent).not.toContain('Custom')
    const displayWindowSlider = drawer?.querySelector<HTMLInputElement>('input[aria-label="Graph display window"]')
    expect(displayWindowSlider?.min).toBe('1')
    expect(displayWindowSlider?.max).toBe('31')
    expect(displayWindowSlider?.step).toBe('1')
    expect(displayWindowSlider?.value).toBe('3')
    expect(displayWindowSlider?.getAttribute('aria-valuetext')).toBe('3m')
    const displayWindowControl = drawer?.querySelector<HTMLElement>('.chart-window-slider-control')
    expect(displayWindowControl?.style.getPropertyValue('--chart-window-slider-position')).toBe('6.6667%')
    expect(displayWindowControl?.querySelector('.chart-window-slider-value')?.textContent).toBe('3m')
    expect(displayWindowControl?.querySelector('.chart-window-slider-scale')).toBeNull()
    expect(drawer?.textContent).toContain('Zoom')
    expect(drawer?.textContent).toContain('6 Graphs')
    expect(drawer?.textContent).not.toContain('Role View')
    expect(drawer?.textContent).not.toContain('Graph preset')
    expect(drawer?.textContent).toContain('Display Time')
    expect(drawer?.textContent).toContain('Cursor Labels')
    expect(drawer?.textContent).not.toContain('Network latency')
    expect(drawer?.textContent).not.toContain('Removed')
    expect(container.querySelector('.btn-expand')?.hasAttribute('disabled')).toBe(false)
    expect(buttonByText('Clock Time')?.getAttribute('aria-pressed')).toBe('true')
    expect(buttonByText('Relative Time')?.getAttribute('aria-pressed')).toBe('false')
    expect(buttonByText('Standard')?.getAttribute('aria-pressed')).toBe('true')
    expect(buttonByText('6 Graphs')?.getAttribute('aria-pressed')).toBe('false')
    const cursorLabelToggle = container.querySelector<HTMLInputElement>('input[aria-label="Show cursor labels on graphs"]')
    expect(cursorLabelToggle?.checked).toBe(true)

    const roleSelect = container.querySelector<HTMLSelectElement>('select[aria-label="Graph role view"]')
    expect(roleSelect).toBeNull()

    const errorRateGraphToggle = container.querySelector<HTMLInputElement>('input[aria-label="Show Error Rate (%)"]')
    expect(errorRateGraphToggle?.checked).toBe(true)

    await act(async () => {
      errorRateGraphToggle?.click()
    })

    expect(container.querySelectorAll('.chart-card')).toHaveLength(14)
    expect(container.querySelector('.charts-toolbar')?.textContent).toContain('14 graphs')
    expect(window.localStorage.getItem('apexload.execution.hiddenCharts')).toContain('errors')

    await act(async () => {
      cursorLabelToggle?.click()
    })

    expect(cursorLabelToggle?.checked).toBe(false)
    expect(window.localStorage.getItem('apexload.execution.cursorLabels')).toBe('false')

    await act(async () => {
      container.querySelector<SVGElement>('[data-testid="mock-chart"]')?.dispatchEvent(new MouseEvent('mousemove', { bubbles: true }))
      await flushAnimationFrame()
    })

    const hiddenTooltips = Array.from(container.querySelectorAll('[data-testid="mock-tooltip"]'))
    expect(hiddenTooltips.length).toBeGreaterThan(0)
    expect(hiddenTooltips.every(tooltip => tooltip.getAttribute('data-content-hidden') === 'true')).toBe(true)

    await act(async () => {
      buttonByText('Relative Time')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(buttonByText('Clock Time')?.getAttribute('aria-pressed')).toBe('false')
    expect(buttonByText('Relative Time')?.getAttribute('aria-pressed')).toBe('true')
    expect(window.localStorage.getItem('apexload.execution.chartTimeAxisMode')).toBe('relative')

    await act(async () => {
      buttonByText('6 Graphs')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.charts-grid')?.className).toContain('six-up')
    expect(buttonByText('Standard')?.getAttribute('aria-pressed')).toBe('false')
    expect(buttonByText('6 Graphs')?.getAttribute('aria-pressed')).toBe('true')
    expect(window.localStorage.getItem('apexload.execution.chartDensity')).toBe('six_up')
    expect(Array.from(container.querySelectorAll('[data-testid="mock-responsive-container"]')).map(container => container.getAttribute('data-height'))).toEqual(Array(13).fill('132'))
    expect(container.querySelector('.chart-metrics-table')).toBeNull()

    await act(async () => {
      buttonByText('Standard')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.charts-grid')?.className).not.toContain('six-up')
    expect(window.localStorage.getItem('apexload.execution.chartDensity')).toBe('standard')
    expect(container.querySelector('.chart-metrics-table')).not.toBeNull()

    const responseChart = Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.textContent?.includes('Response Time (ms)'))
    const responseLatencyCheckbox = responseChart?.querySelector<HTMLInputElement>('.chart-card-checkbox input[type="checkbox"]')
    expect(responseChart?.textContent).toContain('Include network latency')
    expect(responseLatencyCheckbox?.checked).toBe(true)
    expect(responseChart?.textContent).toContain('110ms')

    await act(async () => {
      responseLatencyCheckbox?.click()
    })

    expect(responseLatencyCheckbox?.checked).toBe(false)
    expect(window.localStorage.getItem('apexload.execution.responseTimeNetworkMode')).toBe('application')
    expect(responseChart?.textContent).toContain('82.6ms')

    expect(container.querySelectorAll('.chart-card')).toHaveLength(14)

    const resetButton = buttonByText('Reset Graphs')
    expect(resetButton?.className).toContain('btn-chip')

    await act(async () => {
      resetButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelectorAll('.chart-card')).toHaveLength(15)
  })

  it('traps graph configuration focus, closes on Escape, and restores its trigger', async () => {
    await renderCharts(null, false)
    const trigger = buttonByText('Configure Graphs')
    trigger?.focus()
    await act(async () => trigger?.click())
    await act(async () => {
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })

    const drawer = container.querySelector<HTMLElement>('.charts-config-drawer')
    const closeButton = drawer?.querySelector<HTMLButtonElement>('.charts-config-drawer-header button')
    expect(drawer?.getAttribute('aria-modal')).toBe('true')
    expect(document.activeElement).toBe(closeButton)

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true }))
    })
    expect(drawer?.contains(document.activeElement)).toBe(true)
    expect(document.activeElement).not.toBe(closeButton)

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })
    expect(container.querySelector('.charts-config-drawer')).toBeNull()
    expect(document.activeElement).toBe(trigger)
  })

  it('changes the graph display window from the minute slider', async () => {
    const onTimeRangeChange = vi.fn()
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
          timeRange="3m"
          onTimeRangeChange={onTimeRangeChange}
          isMultiGroup={false}
          groupNames={[]}
          groupColors={['#00a3ff']}
        />,
      )
    })

    await act(async () => {
      buttonByText('Configure Graphs')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const displayWindowSlider = container.querySelector<HTMLInputElement>('input[aria-label="Graph display window"]')
    expect(displayWindowSlider).not.toBeNull()
    const setSliderValue = (value: string) => {
      const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
      valueSetter?.call(displayWindowSlider, value)
      displayWindowSlider!.dispatchEvent(new Event('input', { bubbles: true }))
      displayWindowSlider!.dispatchEvent(new Event('change', { bubbles: true }))
    }

    await act(async () => {
      setSliderValue('30')
    })
    expect(onTimeRangeChange).toHaveBeenLastCalledWith('30m')

    await act(async () => {
      setSliderValue('31')
    })
    expect(onTimeRangeChange).toHaveBeenLastCalledWith('all')
  })

  it('switches the route latency graph between total, phase, and stability diagnostics', async () => {
    await renderCharts()

    const routeChart = () => Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.textContent?.includes('Route Latency (ms)'))
    const routeButton = (label: string) => Array.from(routeChart()?.querySelectorAll('button') ?? []).find(button => button.textContent === label)

    expect(routeChart()?.querySelector('path[data-testid="mock-line"][data-key="latency_edge"][data-name="edge"]')).not.toBeNull()
    expect(routeChart()?.textContent).toContain('HTTP 204')
    expect(routeChart()?.textContent).toContain('192.0.2.10')
    expect(routeChart()?.textContent).toContain('91.7%')

    await act(async () => {
      routeButton('Phases')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(window.localStorage.getItem('apexload.execution.routeLatencyMode')).toBe('phases')
    expect(routeChart()?.querySelector('path[data-testid="mock-line"][data-key="latency_dns_edge"][data-name="DNS"]')).not.toBeNull()
    expect(routeChart()?.querySelector('path[data-testid="mock-line"][data-key="latency_connect_edge"][data-name="TCP Connect"]')).not.toBeNull()
    expect(routeChart()?.querySelector('path[data-testid="mock-line"][data-key="latency_ttfb_edge"][data-name="TTFB"]')).not.toBeNull()

    await act(async () => {
      routeButton('Stability')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(window.localStorage.getItem('apexload.execution.routeLatencyMode')).toBe('stability')
    expect(axisLabels(routeChart())).toEqual([])
    expect(axisAriaLabels(routeChart())).toEqual(expect.arrayContaining(['Show Jitter (ms) axis label', 'Show Success (%) axis label']))
    expect(routeChart()?.querySelector('path[data-testid="mock-line"][data-key="latency_jitter_edge"][data-name="Jitter"]')).not.toBeNull()
    expect(routeChart()?.querySelector('path[data-testid="mock-line"][data-key="latency_success_rate_edge"][data-name="Success Rate"][data-y-axis-id="successRate"]')).not.toBeNull()
  })

  it('renders generator impact metrics and per-generator verdict rows', async () => {
    await renderCharts()

    const impactChart = Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.textContent?.includes('Generator Impact'))

    expect(impactChart?.querySelector('path[data-testid="mock-line"][data-key="containerCPUUsagePct"][data-name="Container CPU"]')).not.toBeNull()
    expect(impactChart?.querySelector('path[data-testid="mock-line"][data-key="containerCPUThrottledSecondsRate"][data-name="CPU Throttle"]')).not.toBeNull()
    expect(impactChart?.textContent).toContain('Likely impact')
    expect(impactChart?.textContent).toContain('gen-a')
    expect(impactChart?.textContent).toContain('gen-b')
    expect(impactChart?.textContent).toContain('CPU saturation')
    expect(impactChart?.textContent).toContain('Generator imbalance')
    expect(impactChart?.textContent).toContain('n/a')
  })

  it('renders compact command-center graph cards with a shared cursor crosshair', async () => {
    const onActiveCursorChange = vi.fn()
    window.localStorage.setItem('apexload.commandCenter.ops.chartDensity', 'six_up')

    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={[
            { time: '10:00:00', timestampMs: 0, vusers: 2, tps: 1, targetTps: 3, avgMs: 100, errorPct: 0, p50: 80, p90: 100, p95: 115, p99: 130 },
            { time: '10:00:01', timestampMs: 1_000, vusers: 4, tps: 2, targetTps: 3, avgMs: 110, errorPct: 0.1, p50: 85, p90: 105, p95: 118, p99: 135 },
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
          timeRange="3m"
          onTimeRangeChange={vi.fn()}
          isMultiGroup={false}
          groupNames={[]}
          groupColors={['#00a3ff']}
          showLoadImpact={false}
          surface="command-center"
          storageScope="apexload.commandCenter.ops"
          defaultDensity="command_center"
          activeCursor={{ label: '10:00:01', timestampMs: 1_000 }}
          onActiveCursorChange={onActiveCursorChange}
        />,
      )
    })

    expect(container.querySelector('.charts-grid')?.className).toContain('command-center-compact')
    expect(window.localStorage.getItem('apexload.commandCenter.ops.chartDensity')).toBe('command_center')
    expect(container.querySelector('.chart-card')?.className).toContain('command-center-chart-card')
    const compactHeatmapCard = container.querySelector('.load-impact-card.load-impact-card-graph')
    expect(compactHeatmapCard?.className).toContain('load-impact-graph-card-span-3')
    expect(compactHeatmapCard?.className).toContain('command-center-chart-card')
    const compactHeights = Array.from(container.querySelectorAll('[data-testid="mock-responsive-container"]')).map(item => item.getAttribute('data-height'))
    expect(compactHeights.length).toBeGreaterThan(0)
    expect(compactHeights.every(height => height === '170')).toBe(true)

    const cursorLines = Array.from(container.querySelectorAll('[data-testid="mock-reference-line"]'))
    expect(cursorLines.length).toBeGreaterThan(1)
    const cursorXValues = cursorLines.map(line => line.getAttribute('data-x'))
    expect(cursorXValues).toContain('10:00:01')
    expect(cursorXValues.every(value => Boolean(value))).toBe(true)

    const tpsChart = Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.textContent?.includes('Transactions Per Second'))
    expect(tpsChart?.querySelector('path[data-testid="mock-line"][data-name="Total"]')?.getAttribute('data-key')).toBe('tps__smoothed')
    expect(tpsChart?.querySelector('path[data-testid="mock-line"][data-name="Target"]')?.getAttribute('data-key')).toBe('targetTps')

    await act(async () => {
      tpsChart?.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 180, clientY: 160 }))
    })

    expect(container.querySelector('.chart-context-menu-title')?.textContent).toBe('Transactions Per Second')
    expect(menuActionTexts()).toContain('Overlay VUsers')

    await act(async () => {
      menuButton('Overlay VUsers')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const tpsChartWithOverlay = Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.textContent?.includes('Transactions Per Second + VUsers'))
    expect(tpsChartWithOverlay).not.toBeUndefined()
    expect(tpsChartWithOverlay?.querySelector('path[data-testid="mock-line"][data-name="Active VUsers"]')?.getAttribute('data-key')).toBe('vusers')
    expect(tpsChartWithOverlay?.querySelector('path[data-testid="mock-line"][data-name="Total"]')?.getAttribute('data-key')).toBe('tps__smoothed')
    expect(tpsChartWithOverlay?.querySelector('path[data-testid="mock-reference-line"]')?.getAttribute('data-y-axis-id')).toBe('metric')

    await act(async () => {
      tpsChartWithOverlay?.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 200, clientY: 170 }))
    })

    expect(menuActionTexts()).toContain('Overlay ART')

    await act(async () => {
      menuButton('Overlay ART')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const tpsChartWithArtOverlay = Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.textContent?.includes('Transactions Per Second + VUsers + ART'))
    expect(tpsChartWithArtOverlay).not.toBeUndefined()
    expect(tpsChartWithArtOverlay?.querySelector('path[data-testid="mock-line"][data-name="ART"]')?.getAttribute('data-key')).toBe('avgMs__smoothed')

    await act(async () => {
      container.querySelector<SVGElement>('[data-testid="mock-chart"]')?.dispatchEvent(new MouseEvent('mousemove', { bubbles: true }))
      await flushAnimationFrame()
    })

    // The shared cursor resolves the hovered index against this chart's exact
    // rendered data, keeping differently-downsampled charts aligned by time.
    expect(onActiveCursorChange).toHaveBeenCalledWith({ label: '10:00:01', timestampMs: 1_000 })
  })

  it('cancels a pending mouse-move frame when the pointer leaves the chart', async () => {
    let pendingFrame: FrameRequestCallback | undefined
    const requestFrame = vi.spyOn(window, 'requestAnimationFrame').mockImplementation(callback => {
      pendingFrame = callback
      return 91
    })
    const cancelFrame = vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => undefined)
    const onActiveCursorChange = vi.fn()
    const onChartMouseMove = vi.fn()

    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={[
            { time: '10:00:00', timestampMs: 0, vusers: 2, tps: 1, avgMs: 0, errorPct: 0, p50: 0, p90: 0, p99: 0 },
            { time: '10:00:01', timestampMs: 1_000, vusers: 4, tps: 2, avgMs: 0, errorPct: 0, p50: 0, p90: 0, p99: 0 },
          ]}
          expandedChart={null}
          onExpandedChartChange={vi.fn()}
          onChartMouseDown={vi.fn()}
          onChartMouseMove={onChartMouseMove}
          onZoom={vi.fn()}
          onResetZoom={vi.fn()}
          showRefArea={false}
          refAreaLeft=""
          refAreaEnd=""
          timeRange="3m"
          onTimeRangeChange={vi.fn()}
          isMultiGroup={false}
          groupNames={[]}
          groupColors={[]}
          surface="command-center"
          onActiveCursorChange={onActiveCursorChange}
        />,
      )
    })
    const chart = container.querySelector<SVGElement>('[data-testid="mock-chart"]')
    const card = chart?.closest('.chart-card')
    await act(async () => chart?.dispatchEvent(new MouseEvent('mousemove', { bubbles: true })))
    expect(requestFrame).toHaveBeenCalled()
    await act(async () => card?.dispatchEvent(new MouseEvent('mouseout', { bubbles: true })))
    expect(cancelFrame).toHaveBeenCalledWith(91)
    pendingFrame?.(0)
    expect(onChartMouseMove).not.toHaveBeenCalled()
    expect(onActiveCursorChange).not.toHaveBeenCalled()
    requestFrame.mockRestore()
    cancelFrame.mockRestore()
  })

  it('flushes the pending drag anchor before applying a quick mouse-up zoom', async () => {
    let pendingFrame: FrameRequestCallback | undefined
    const requestFrame = vi.spyOn(window, 'requestAnimationFrame').mockImplementation(callback => {
      pendingFrame = callback
      return 92
    })
    const cancelFrame = vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => undefined)
    const calls: string[] = []
    const onChartMouseDown = vi.fn(() => calls.push('down'))
    const onChartMouseMove = vi.fn(() => calls.push('move'))
    const onZoom = vi.fn(() => calls.push('zoom'))

    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={[
            { time: '10:00:00', timestampMs: 0, vusers: 2, tps: 1, avgMs: 0, errorPct: 0, p50: 0, p90: 0, p99: 0 },
            { time: '10:00:01', timestampMs: 1_000, vusers: 4, tps: 2, avgMs: 0, errorPct: 0, p50: 0, p90: 0, p99: 0 },
          ]}
          expandedChart={null}
          onExpandedChartChange={vi.fn()}
          onChartMouseDown={onChartMouseDown}
          onChartMouseMove={onChartMouseMove}
          onZoom={onZoom}
          onResetZoom={vi.fn()}
          showRefArea={false}
          refAreaLeft=""
          refAreaEnd=""
          timeRange="all"
          onTimeRangeChange={vi.fn()}
          isMultiGroup={false}
          groupNames={[]}
          groupColors={[]}
        />,
      )
    })

    const chart = container.querySelector<SVGElement>('[data-testid="mock-chart"]')
    await act(async () => {
      chart?.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
      chart?.dispatchEvent(new MouseEvent('mousemove', { bubbles: true }))
      chart?.dispatchEvent(new MouseEvent('mouseup', { bubbles: true }))
    })

    expect(requestFrame).toHaveBeenCalled()
    expect(cancelFrame).toHaveBeenCalledWith(92)
    expect(onChartMouseDown).toHaveBeenCalledWith({ label: '10:00:00', timestampMs: 0 })
    expect(onChartMouseMove).toHaveBeenCalledWith({ label: '10:00:01', timestampMs: 1_000 })
    expect(calls).toEqual(['down', 'move', 'zoom'])
    pendingFrame?.(0)
    expect(onChartMouseMove).toHaveBeenCalledTimes(1)

    requestFrame.mockRestore()
    cancelFrame.mockRestore()
  })

  it('toggles gradient underlays and dense chart downsampling from graph options', async () => {
    const denseData = Array.from({ length: 600 }, (_, index) => ({
      time: `10:${String(Math.floor(index / 60)).padStart(2, '0')}:${String(index % 60).padStart(2, '0')}`,
      vusers: index % 40,
      tps: index === 320 ? 120 : index % 8,
      targetTps: 40,
      avgMs: index === 410 ? 1800 : 100 + (index % 13),
      errorPct: index % 25 === 0 ? 2 : 0,
      p50: 90,
      p90: 120,
      p95: 150,
      p99: 220,
    }))

    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={denseData}
          expandedChart={null}
          onExpandedChartChange={vi.fn()}
          onChartMouseDown={vi.fn()}
          onChartMouseMove={vi.fn()}
          onZoom={vi.fn()}
          onResetZoom={vi.fn()}
          showRefArea={false}
          refAreaLeft=""
          refAreaEnd=""
          timeRange="all"
          onTimeRangeChange={vi.fn()}
          isMultiGroup={false}
          groupNames={[]}
          groupColors={['#00a3ff']}
          showLoadImpact={false}
          surface="command-center"
          storageScope="apexload.commandCenter.ops"
          defaultDensity="command_center"
        />,
      )
    })

    const dataLengths = () => Array.from(container.querySelectorAll('[data-testid="mock-chart"]'))
      .map(chart => Number(chart.getAttribute('data-length')))
      .filter(value => Number.isFinite(value) && value > 0)
    const tpsChart = () => Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.textContent?.includes('Transactions Per Second'))
    const tpsGradientUnderlay = () => tpsChart()?.querySelector('path[data-testid="mock-area"][data-name="Total"]')

    expect(Math.max(...dataLengths())).toBeLessThan(denseData.length)
    expect(tpsGradientUnderlay()).not.toBeNull()

    await act(async () => {
      buttonByText('Configure Graphs')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const gradientCheckbox = container.querySelector<HTMLInputElement>('input[aria-label="Show gradient underlay on graphs"]')
    const downsampleCheckbox = container.querySelector<HTMLInputElement>('input[aria-label="Downsample dense charts"]')

    expect(gradientCheckbox?.checked).toBe(true)
    expect(downsampleCheckbox?.checked).toBe(true)

    await act(async () => {
      gradientCheckbox?.click()
    })

    expect(window.localStorage.getItem('apexload.commandCenter.ops.gradientUnderlays')).toBe('false')
    expect(tpsGradientUnderlay()).toBeNull()

    await act(async () => {
      downsampleCheckbox?.click()
    })

    expect(window.localStorage.getItem('apexload.commandCenter.ops.downsampleCharts')).toBe('false')
    expect(Math.max(...dataLengths())).toBe(denseData.length)
  })

  it('keeps selected graph granularity after an immediate remount', async () => {
    window.localStorage.setItem('apexload.execution.chartGranularity', '1')
    await renderCharts()

    // The granularity control lives in the "Configure Graphs" drawer.
    const openGranularityDrawer = async () => {
      const configureButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.trim() === 'Configure Graphs')
      expect(configureButton).toBeDefined()
      await act(async () => configureButton?.click())
      return container.querySelector<HTMLSelectElement>('select[aria-label="Graph granularity"]')
    }

    const granularitySelect = await openGranularityDrawer()
    expect(granularitySelect).not.toBeNull()
    if (!granularitySelect) return

    await act(async () => {
      granularitySelect.value = '15'
      granularitySelect.dispatchEvent(new Event('change', { bubbles: true }))
      // Simulate a rapid remount from surrounding workspace/live-refresh churn.
      root.render(<div />)
    })

    await renderCharts()

    const remountedSelect = await openGranularityDrawer()
    expect(remountedSelect?.value).toBe('15')
    expect(window.localStorage.getItem('apexload.execution.chartGranularity')).toBe('15')
  })

  function menuActionTexts() {
    return Array.from(container.querySelectorAll('.chart-context-menu button')).map(button => button.textContent)
  }

  function menuButton(label: string) {
    return Array.from(container.querySelectorAll('.chart-context-menu button')).find(button => button.textContent === label)
  }

  function buttonByText(label: string) {
    return Array.from(container.querySelectorAll('button')).find(button => button.textContent === label)
  }

  function chartTitles() {
    return Array.from(container.querySelectorAll('.chart-card .chart-title')).map(title => title.textContent)
  }

  function axisLabels(chart: Element | undefined) {
    return Array.from(chart?.querySelectorAll('[data-testid="mock-y-axis"]') ?? [])
      .map(axis => axis.getAttribute('data-label'))
      .filter(Boolean)
  }

  function axisAriaLabels(chart: Element | undefined) {
    return Array.from(chart?.querySelectorAll('[data-testid="mock-y-axis"]') ?? [])
      .map(axis => axis.getAttribute('data-axis-aria-label'))
      .filter(Boolean)
  }

  async function flushAnimationFrame() {
    await new Promise<void>(resolve => window.requestAnimationFrame(() => resolve()))
  }

  function primaryAxisDomain(chart: Element | undefined) {
    return Array.from(chart?.querySelectorAll('[data-testid="mock-y-axis"]') ?? [])
      .find(axis => axis.getAttribute('data-orientation') !== 'right')
      ?.getAttribute('data-domain')
  }

  function createDragDataTransfer() {
    const data = new Map<string, string>()
    return {
      dropEffect: 'move',
      effectAllowed: 'move',
      getData: vi.fn((type: string) => data.get(type) ?? ''),
      setData: vi.fn((type: string, value: string) => data.set(type, value)),
    } as unknown as DataTransfer
  }

  function dispatchDragEvent(element: HTMLElement, type: string, dataTransfer: DataTransfer, coordinates: { clientX?: number; clientY?: number } = {}) {
    const event = new Event(type, { bubbles: true, cancelable: true }) as DragEvent
    Object.defineProperty(event, 'dataTransfer', { value: dataTransfer })
    Object.defineProperty(event, 'clientX', { value: coordinates.clientX ?? 0 })
    Object.defineProperty(event, 'clientY', { value: coordinates.clientY ?? 0 })
    element.dispatchEvent(event)
  }

  async function renderCharts(expandedChart: string | null = null, showLoadImpact = true) {
    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={[
            {
              time: '10:00:00',
              vusers: 2,
              tps: 1,
              avgMs: 100,
              errorPct: 0,
              p50: 80,
              p90: 100,
              p99: 130,
              latency_edge: 20,
              latency_dns_edge: 2,
              latency_connect_edge: 8,
              latency_tls_edge: 3,
              latency_ttfb_edge: 18,
              latency_jitter_edge: 1,
              latency_success_rate_edge: 100,
              containerCPUUsagePct: 70,
              containerCPUThrottledSecondsRate: 0.1,
              containerMemoryUsagePct: 62,
              gcPauseMs: 8,
              netDropsPerSec: 0,
              netErrorsPerSec: 0,
              tcpRetransmitsPerSec: 0,
              diskIOTimePct: 18,
            },
            {
              time: '10:00:01',
              vusers: 4,
              tps: 2,
              avgMs: 110,
              errorPct: 0,
              p50: 85,
              p90: 105,
              p99: 135,
              latency_edge: 27.4,
              latency_dns_edge: 3,
              latency_connect_edge: 9,
              latency_tls_edge: 4,
              latency_ttfb_edge: 24,
              latency_jitter_edge: 2.2,
              latency_success_rate_edge: 91.7,
              containerCPUUsagePct: 96,
              containerCPUThrottledSecondsRate: 0.4,
              containerMemoryUsagePct: 91,
              gcPauseMs: 140,
              netDropsPerSec: 2,
              netErrorsPerSec: 1,
              tcpRetransmitsPerSec: 3,
              diskIOTimePct: 72,
            },
          ]}
          expandedChart={expandedChart}
          onExpandedChartChange={vi.fn()}
          onChartMouseDown={vi.fn()}
          onChartMouseMove={vi.fn()}
          onZoom={vi.fn()}
          onResetZoom={vi.fn()}
          showRefArea={false}
          refAreaLeft=""
          refAreaEnd=""
          timeRange="3m"
          onTimeRangeChange={vi.fn()}
          isMultiGroup={false}
          groupNames={[]}
          groupColors={['#00a3ff']}
          showLoadImpact={showLoadImpact}
          latestLatency={{
            targets: [{
              name: 'edge',
              endpoint: 'edge',
              latency_ms: 21.4,
              probe_total_ms: 27.4,
              dns_ms: 3,
              connect_ms: 9,
              tls_ms: 4,
              ttfb_ms: 24,
              http_status: 204,
              probe_method: 'HEAD',
              ip: '192.0.2.10',
              hops: 3,
              last_success: true,
              jitter_ms: 2.2,
              success_rate_pct: 91.7,
              sample_count: 12,
              route_status: 'ok',
              timestamp: '2026-05-12T20:05:05Z',
            }],
          }}
          latestMetrics={{
            timestamp: '2026-05-12T20:05:05Z',
            active_vusers: 20,
            tps: 4,
            avg_response_ms: 110,
            error_pct: 1,
            total_transactions: 4,
            total_errors: 1,
            p50_ms: 85,
            p90_ms: 105,
            p95_ms: 1300,
            p99_ms: 1500,
            resources: {
              cpu_process_pct: 96,
              cpu_free_pct: 4,
              mem_process_mb: 512,
              mem_free_mb: 1024,
              net_sent_kbps: 20,
              net_recv_kbps: 18,
              disk_read_kbps: 2,
              disk_write_kbps: 3,
              container_cpu_usage_pct: 96,
              container_cpu_throttled_seconds_rate: 0.4,
              container_memory_usage_pct: 91,
              gc_pause_ms: 140,
              net_drop_in_per_sec: 2,
              net_error_in_per_sec: 1,
              tcp_retransmits_per_sec: 3,
              disk_io_time_pct: 72,
            },
            generator_resources: {
              'gen-a': {
                generator_id: 'gen-a',
                target: '10.0.0.1',
                region: 'east',
                active_vusers: 16,
                tps: 3.5,
                p95_ms: 1300,
                p99_ms: 1500,
                error_pct: 1,
                resources: {
                  cpu_process_pct: 97,
                  cpu_free_pct: 3,
                  mem_process_mb: 512,
                  mem_free_mb: 1024,
                  net_sent_kbps: 20,
                  net_recv_kbps: 18,
                  disk_read_kbps: 2,
                  disk_write_kbps: 3,
                  container_cpu_usage_pct: 97,
                  container_cpu_throttled_seconds_rate: 0.4,
                  container_memory_usage_pct: 91,
                  gc_pause_ms: 140,
                  net_drop_in_per_sec: 2,
                  tcp_retransmits_per_sec: 3,
                  disk_io_time_pct: 72,
                },
                timestamp: '2026-05-12T20:05:05Z',
              },
              'gen-b': {
                generator_id: 'gen-b',
                target: '10.0.0.2',
                region: 'west',
                active_vusers: 4,
                tps: 0.5,
                p95_ms: 90,
                p99_ms: 120,
                error_pct: 0,
                resources: {
                  cpu_process_pct: 15,
                  cpu_free_pct: 70,
                  mem_process_mb: 128,
                  mem_free_mb: 2048,
                  net_sent_kbps: 5,
                  net_recv_kbps: 4,
                  disk_read_kbps: 0,
                  disk_write_kbps: 0,
                },
                timestamp: '2026-05-12T20:05:05Z',
              },
            },
          }}
        />,
      )
    })
  }
})
