// @vitest-environment jsdom

import { act, isValidElement } from 'react'
import type { ComponentProps, ReactNode } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ImportedDashboardResult, MetricsHistoryPoint } from '../types'
import { downloadTextFile } from '../utils/fileUtils'
import TestExecutionCharts, { BoundedChartTooltipContent, chartTimeAxisPresentation, formatChartTimeMetricValue, formatMetricTableValue } from './TestExecutionCharts'
import ContinuousStackedArea from './ContinuousStackedArea'
import { transactionChartSeriesKey } from './transactionChartSeries'

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
    const formatted = formatter?.(123.4567, 'TPS')
    const formattedValue = Array.isArray(formatted) ? formatted[0] : formatted
    if (content) {
      return (
        <g
          data-testid="mock-tooltip"
          data-content-hidden="true"
          data-label-display={labelStyle?.display ?? ''}
          data-label-text={labelFormatter ? String(labelFormatter('10:00:01') ?? '') : ''}
          data-formatted-value={formattedValue === undefined ? '' : String(formattedValue)}
        />
      )
    }
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
    Area: ({ dataKey, name, yAxisId, fill, connectNulls, shape }: { dataKey?: string; name?: string; yAxisId?: string | number; fill?: string; connectNulls?: boolean; shape?: unknown }) => {
      const continuousShape = isValidElement<{ stackKeys: string[] }>(shape) && shape.type === ContinuousStackedArea ? shape : undefined
      return <path data-testid="mock-area" data-key={dataKey} data-name={name} data-y-axis-id={String(yAxisId ?? '')} data-fill={fill ?? ''} data-connect-nulls={String(connectNulls ?? false)} data-gap-shape={continuousShape ? 'continuous-stacked' : undefined} data-gap-stack-keys={continuousShape?.props.stackKeys.join(',')} />
    },
    AreaRevealShape: Mock,
    CartesianGrid: Mock,
    ComposedChart: MockChart,
    Legend: Mock,
    Line: ({
      dataKey,
      name,
      yAxisId,
      stroke,
      strokeWidth,
      strokeDasharray,
      strokeOpacity,
      connectNulls,
      dot,
    }: {
      dataKey?: string
      name?: string
      yAxisId?: string | number
      stroke?: string
      strokeWidth?: number
      strokeDasharray?: string
      strokeOpacity?: number
      connectNulls?: boolean
      dot?: boolean | { r?: number }
    }) => (
      <path
        data-testid="mock-line"
        data-key={dataKey}
        data-name={name}
        data-y-axis-id={String(yAxisId ?? '')}
        data-stroke={stroke ?? ''}
        data-stroke-width={String(strokeWidth ?? '')}
        data-stroke-dasharray={strokeDasharray ?? ''}
        data-stroke-opacity={String(strokeOpacity ?? '')}
        data-connect-nulls={String(connectNulls ?? false)}
        data-dot-radius={typeof dot === 'object' ? String(dot.r ?? '') : ''}
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

describe('chartTimeAxisPresentation', () => {
  it('converts millisecond time axes to seconds without changing non-time axes', () => {
    const seconds = chartTimeAxisPresentation('Response time (ms)', 'seconds')
    expect(seconds.label).toBe('Response time (s)')
    expect(seconds.tickFormatter?.(2500)).toBe('2.5')
    expect(formatChartTimeMetricValue(2500, 'ms', 'seconds')).toBe('2.5 s')
    expect(formatChartTimeMetricValue(2500, 'ms', 'milliseconds')).toBe('2,500 ms')

    expect(chartTimeAxisPresentation('Fleet CPU (%)', 'seconds')).toEqual({
      label: 'Fleet CPU (%)',
      tickFormatter: undefined,
    })
    expect(chartTimeAxisPresentation('Route latency (ms)', 'milliseconds')).toEqual({
      label: 'Route latency (ms)',
      tickFormatter: undefined,
    })
  })

  it('converts response-time table values without rounding positive seconds to zero', () => {
    expect(formatMetricTableValue(0.7, 'ms', 'seconds')).toBe('0.0007 s')
    expect(formatMetricTableValue(110, 'ms', 'seconds')).toBe('0.11 s')
    expect(formatMetricTableValue(110, 'ms', 'milliseconds')).toBe('110ms')
    expect(formatMetricTableValue(12, '%', 'seconds')).toBe('12%')
  })
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

    expect(container.querySelector('.chart-context-menu-title')?.textContent).toBe('VUsers')
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
    const actionsButton = container.querySelector<HTMLButtonElement>('button[aria-label="VUsers actions"]')
    expect(actionsButton).toBeTruthy()
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

  it('includes the current run identity in displayed live data exports', async () => {
    await renderCharts(null, true, false, 'run-export-source')
    const card = container.querySelector('.chart-card')
    await act(async () => card?.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 100, clientY: 120 })))
    await act(async () => { menuButton('Export Graph Data')?.dispatchEvent(new MouseEvent('click', { bubbles: true })); await Promise.resolve(); await Promise.resolve() })
    expect(vi.mocked(downloadTextFile).mock.calls[0][0]).toContain('run-export-source')
    expect(vi.mocked(downloadTextFile).mock.calls[0][0]).toContain('displayed_samples')
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
    const vuserOverlay = container.querySelector('path[data-testid="mock-line"][data-name="Active VUsers"]')
    expect(vuserOverlay).not.toBeNull()
    expect(vuserOverlay?.getAttribute('data-connect-nulls')).toBe('true')
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
    expect(vusersChart?.textContent).toContain('VUsers')
    expect(vusersChart?.querySelector('[data-testid="mock-x-axis"]')?.getAttribute('data-label')).toBe('')
    expect(axisLabels(vusersChart)).toEqual([])
    expect(axisAriaLabels(vusersChart)).toContain('Show VUsers axis label')
    const vuserAxis = vusersChart?.querySelector('[data-testid="mock-y-axis"]')
    expect(vuserAxis?.querySelectorAll('[role="button"][tabindex="0"]')).toHaveLength(1)
    expect(vusersChart?.querySelector('path[data-testid="mock-area"][data-key="vusers"]')?.getAttribute('data-connect-nulls')).toBe('true')

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
    expect(vusersChart?.querySelector('.chart-title')?.textContent).toBe('VUsers + ART')
    expect(axisLabels(vusersChart)).toEqual([])
    expect(axisAriaLabels(vusersChart)).toEqual(expect.arrayContaining(['Show VUsers axis label', 'Show ART (ms) axis label']))
    expect(window.localStorage.getItem('apexload.execution.artOverlayCharts')).toBe('["vusers"]')

    await act(async () => {
      vusersChart?.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 180, clientY: 160 }))
    })

    expect(menuActionTexts()).toContain('Hide ART Overlay')
    expect(menuActionTexts()).toContain('Overlay TPS')
    expect(container.querySelector('.chart-context-menu-title')?.textContent).toBe('VUsers + ART')

    await act(async () => {
      menuButton('Overlay TPS')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(vusersChart?.querySelector('path[data-testid="mock-line"][data-name="TPS"]')).not.toBeNull()
    expect(vusersChart?.querySelector('.chart-title')?.textContent).toBe('VUsers + ART + TPS')
    expect(axisLabels(vusersChart)).toEqual([])
    expect(axisAriaLabels(vusersChart)).toContain('Show ART (ms) / TPS axis label')
    expect(window.localStorage.getItem('apexload.execution.tpsOverlayCharts')).toBe('["vusers"]')
  })

  it('keeps synced graph tooltips while only the hovered chart displays the time label', async () => {
    await renderCharts()

    const mockCharts = Array.from(container.querySelectorAll('[data-testid="mock-chart"]'))
    expect(mockCharts).toHaveLength(18)
    expect(mockCharts.map(chart => chart.getAttribute('data-sync-id'))).toEqual([...Array(15).fill('metrics'), '', '', ''])
    expect(Array.from(container.querySelectorAll('[data-testid="mock-tooltip"]')).map(tooltip => tooltip.getAttribute('data-label-display'))).toEqual(Array(18).fill('none'))

    const tpsChart = mockCharts[1]
    await act(async () => {
      tpsChart.dispatchEvent(new MouseEvent('mousemove', { bubbles: true }))
      await flushAnimationFrame()
    })

    const tooltips = Array.from(container.querySelectorAll('[data-testid="mock-tooltip"]'))
    expect(tooltips.map(tooltip => tooltip.getAttribute('data-label-display'))).toEqual([
      'none',
      '',
      ...Array(16).fill('none'),
    ])
    expect(tooltips.map(tooltip => tooltip.getAttribute('data-label-text'))).toEqual([
      '',
      '10:00:01',
      ...Array(16).fill(''),
    ])
    expect(tooltips.map(tooltip => tooltip.getAttribute('data-formatted-value'))).toEqual([
      ...Array(15).fill('123.46'),
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

  it('connects sparse network phases, emphasis, and overlays without inventing measurements', async () => {
    const base = { vusers: 10, tps: 5, avgMs: 100, errorPct: 0, p50: 80, p90: 120, p99: 160 }
    const samples: MetricsHistoryPoint[] = [
      { ...base, time: '10:00:00', txDnsMs: 2, txConnectMs: 4, txTlsMs: 6, txTtfbMs: 8, txWaitMs: 10 },
      { ...base, time: '10:00:01', tps: Number.NaN },
      { ...base, time: '10:00:02', txDnsMs: 6, txConnectMs: 8, txTlsMs: 10, txTtfbMs: 12, txWaitMs: 14 },
    ]
    await renderGraphSourceState({ filteredData: samples, activeGraphGroupTab: 'infrastructure', expandedChart: 'tx_network_phases' })
    const chart = container.querySelector<HTMLElement>('[data-chart-id="tx_network_phases"]')!
    const phaseKeys = ['txDnsMs', 'txConnectMs', 'txTlsMs', 'txTtfbMs', 'txWaitMs']
    for (const [index, key] of phaseKeys.entries()) {
      const area = chart.querySelector(`path[data-testid="mock-area"][data-key="${key}__smoothed"]`)
      expect(area, `${key} phase area`).not.toBeNull()
      // Keep missing stack inputs missing; only the drawing shape connects them.
      expect(area?.getAttribute('data-connect-nulls')).toBe('false')
      expect(area?.getAttribute('data-gap-shape')).toBe('continuous-stacked')
      expect(area?.getAttribute('data-gap-stack-keys')).toBe(phaseKeys.slice(0, index + 1).map(phase => `${phase}__smoothed`).join(','))
    }

    const dnsButton = Array.from(chart.querySelectorAll<HTMLButtonElement>('.chart-metric-name'))
      .find(button => button.textContent === 'DNS')!
    await act(async () => dnsButton.click())
    const emphasis = chart.querySelector('path[data-testid="mock-line"][data-key="txDnsMs__smoothed"]')
    expect(emphasis?.getAttribute('data-connect-nulls')).toBe('true')
    expect(Number(emphasis?.getAttribute('data-stroke-width'))).toBeGreaterThan(3)
    expect(dnsButton.closest('tr')?.querySelectorAll('td')[3].textContent?.replace(/\s/g, '')).toBe('4ms')

    await act(async () => chart.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true })))
    await act(async () => menuButton('Overlay TPS')!.click())
    const overlay = chart.querySelector('path[data-testid="mock-line"][data-name="TPS"]')
    expect(overlay?.getAttribute('data-connect-nulls')).toBe('true')

    await act(async () => chart.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true })))
    await act(async () => menuButton('Export Graph Data')!.click())
    const downloads = vi.mocked(downloadTextFile).mock.calls
    const csvRows = downloads[downloads.length - 1][0].split('\n')
    // All five phases and the TPS overlay are absent at the middle timestamp.
    // Connecting their paths must not export zero or a fabricated interpolation.
    expect(csvRows[2].split(',').slice(0, 7)).toEqual(['10:00:01', '', '', '', '', '', ''])
    expect(csvRows[1].split(',').slice(0, 6)).toEqual(['10:00:00', '2', '4', '6', '8', '10'])
    expect(csvRows[3].split(',').slice(0, 6)).toEqual(['10:00:02', '6', '8', '10', '12', '14'])
    expect(samples[1].txDnsMs).toBeUndefined()
    expect(samples[1].tps).toBeNaN()

    // A protocol with no TLS measurements must not suppress its later phases.
    const withoutTls = samples.map(point => {
      const next = { ...point }
      delete next.txTlsMs
      return next
    })
    await renderGraphSourceState({ filteredData: withoutTls, activeGraphGroupTab: 'infrastructure', expandedChart: 'tx_network_phases' })
    const ttfb = chart.querySelector('[data-testid="mock-area"][data-key="txTtfbMs__smoothed"]')
    const wait = chart.querySelector('[data-testid="mock-area"][data-key="txWaitMs__smoothed"]')
    expect(ttfb?.getAttribute('data-gap-stack-keys')).toBe('txDnsMs__smoothed,txConnectMs__smoothed,txTtfbMs__smoothed')
    expect(wait?.getAttribute('data-gap-stack-keys')).toBe('txDnsMs__smoothed,txConnectMs__smoothed,txTtfbMs__smoothed,txWaitMs__smoothed')
  })

  it('connects sparse group lines and their gradient underlays while exports retain gaps', async () => {
    const base = { vusers: 10, tps: 5, avgMs: 100, errorPct: 0, p50: 80, p90: 120, p99: 160, Search_tps: 5 }
    const samples: MetricsHistoryPoint[] = [
      { ...base, time: '10:00:00', Checkout_tps: 20 },
      { ...base, time: '10:00:01' },
      { ...base, time: '10:00:02', Checkout_tps: 40 },
    ]
    await renderGraphSourceState({
      filteredData: samples,
      isMultiGroup: true,
      groupNames: ['Checkout', 'Search'],
      groupColors: ['#00a3ff', '#ffaa00'],
      expandedChart: 'tps',
    })
    const chart = container.querySelector<HTMLElement>('[data-chart-id="tps"]')!
    const groupLine = chart.querySelector('path[data-testid="mock-line"][data-name="Checkout"]')
    const underlay = chart.querySelector('path[data-testid="mock-area"][data-name="Checkout"]')
    expect(groupLine?.getAttribute('data-connect-nulls')).toBe('true')
    expect(underlay?.getAttribute('data-connect-nulls')).toBe('true')

    const highlight = Array.from(chart.querySelectorAll<HTMLButtonElement>('.chart-metric-name'))
      .find(button => button.textContent === 'Checkout')!
    await act(async () => highlight.click())
    const groupPaths = chart.querySelectorAll('path[data-testid="mock-line"][data-key="Checkout_tps__smoothed"]')
    expect(groupPaths).toHaveLength(2)
    expect(Array.from(groupPaths).every(path => path.getAttribute('data-connect-nulls') === 'true')).toBe(true)
    expect(highlight.closest('tr')?.textContent).toContain('30')

    await act(async () => chart.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true })))
    await act(async () => menuButton('Export Graph Data')!.click())
    const downloads = vi.mocked(downloadTextFile).mock.calls
    const csvRows = downloads[downloads.length - 1][0].split('\n')
    const headers = csvRows[0].split(',')
    const groupColumn = headers.findIndex(header => header.includes('[Checkout_tps]'))
    const totalColumn = headers.findIndex(header => header.includes('[tps]'))
    expect(groupColumn).toBeGreaterThan(0)
    expect(totalColumn).toBeGreaterThan(0)
    expect(csvRows.slice(1).map(row => row.split(',')[groupColumn])).toEqual(['20', '', '40'])
    expect(csvRows[2].split(',')[totalColumn]).toBe('5')
    expect(samples[1].Checkout_tps).toBeUndefined()
  })

  it('renders every live transaction and gates sole-group duplicates after response carry-forward', async () => {
    const transactionValues = (loginTps: number, searchTps?: number) => ({
      [transactionChartSeriesKey('Login', 'tps')]: loginTps,
      [transactionChartSeriesKey('Login', 'avgMs')]: 90,
      [transactionChartSeriesKey('Login', 'errorPct')]: 0,
      ...(searchTps === undefined ? {} : {
        [transactionChartSeriesKey('Search', 'tps')]: searchTps,
        [transactionChartSeriesKey('Search', 'avgMs')]: 130,
        [transactionChartSeriesKey('Search', 'errorPct')]: 5,
      }),
    })

    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={[
            { time: '10:00:00', vusers: 2, tps: 3, avgMs: 100, errorPct: 2, p50: 80, p90: 120, p99: 160, Only_tps: 3, Only_avgMs: 100, Only_errorPct: 2, ...transactionValues(1, 2) },
            { time: '10:00:01', vusers: 4, tps: 6, avgMs: 110, errorPct: 3, p50: 85, p90: 125, p99: 170, Only_tps: 6, Only_avgMs: 110, Only_errorPct: 3, ...transactionValues(2) },
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
          groupNames={['Only']}
          groupColors={['#00a3ff', '#ffaa00']}
        />,
      )
    })

    const chartFor = (title: string) => Array.from(container.querySelectorAll('.chart-card'))
      .find(chart => chart.textContent?.includes(title))
    const expectTransactionLines = (chart: Element | undefined, metric: 'tps' | 'avgMs' | 'errorPct') => {
      const loginLine = chart?.querySelector(`path[data-testid="mock-line"][data-key="${transactionChartSeriesKey('Login', metric)}"]`)
      const searchLine = chart?.querySelector(`path[data-testid="mock-line"][data-key="${transactionChartSeriesKey('Search', metric)}"]`)
      expect(loginLine).not.toBeNull()
      expect(searchLine).not.toBeNull()
      expect(loginLine?.getAttribute('data-connect-nulls')).toBe('true')
      expect(searchLine?.getAttribute('data-connect-nulls')).toBe('true')
      expect(loginLine?.getAttribute('data-dot-radius')).toBe('')
      expect(searchLine?.getAttribute('data-dot-radius')).toBe(metric === 'avgMs' ? '' : '3')
      expect(chart?.querySelector(`path[data-testid="mock-line"][data-key="Only_${metric}"]`)).toBeNull()
      expect(chart?.textContent).toContain('Transaction · Login')
      expect(chart?.textContent).toContain('Transaction · Search')
    }

    expectTransactionLines(chartFor('Transactions Per Second'), 'tps')
    expectTransactionLines(chartFor('Response Time (ms)'), 'avgMs')
    expectTransactionLines(chartFor('Error Rate (%)'), 'errorPct')
  })

  it('shows group and transaction series on their own toggles, never both off', async () => {
    const point = (time: string, tps: number): MetricsHistoryPoint => ({
      time, vusers: 4, tps, avgMs: 100, errorPct: 0, p50: 80, p90: 120, p99: 160,
      Checkout_tps: tps / 2, Checkout_avgMs: 100, Checkout_errorPct: 0,
      Browse_tps: tps / 2, Browse_avgMs: 100, Browse_errorPct: 0,
      [transactionChartSeriesKey('Login', 'tps')]: tps, [transactionChartSeriesKey('Login', 'avgMs')]: 90, [transactionChartSeriesKey('Login', 'errorPct')]: 0,
    })
    const renderSeriesCharts = async () => {
      await act(async () => {
        root.render(
          <TestExecutionCharts
            filteredData={[point('10:00:00', 4), point('10:00:01', 6)]}
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
            groupNames={['Checkout', 'Browse']}
            groupColors={['#00a3ff', '#ffaa00']}
          />,
        )
      })
    }
    await renderSeriesCharts()
    await act(async () => {
      buttonByText('Configure Graphs')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const tpsChart = () => Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.querySelector('.chart-title')?.textContent === 'Transactions Per Second')
    const line = (key: string) => tpsChart()?.querySelector(`path[data-testid="mock-line"][data-key="${key}"]`) ?? null
    const loginKey = transactionChartSeriesKey('Login', 'tps')
    const groupsToggle = () => container.querySelector<HTMLInputElement>('input[aria-label="Show group series on graphs"]')!
    const transactionsToggle = () => container.querySelector<HTMLInputElement>('input[aria-label="Show transaction series on graphs"]')!

    expect(line('Checkout_tps')).not.toBeNull()
    expect(line(loginKey)).not.toBeNull()
    expect(groupsToggle().checked).toBe(true)
    expect(transactionsToggle().checked).toBe(true)
    expect(groupsToggle().getAttribute('aria-disabled')).toBe('false')

    // Transactions off: the groups and the total stay, and groups is the last one on.
    await act(async () => { transactionsToggle().click() })
    expect(line(loginKey)).toBeNull()
    expect(line('Checkout_tps')).not.toBeNull()
    expect(line('Browse_tps')).not.toBeNull()
    expect(line('tps')).not.toBeNull()
    expect(tpsChart()?.textContent).not.toContain('Transaction · Login')
    // It stays reachable and says why it cannot be turned off.
    expect(groupsToggle().disabled).toBe(false)
    expect(groupsToggle().getAttribute('aria-disabled')).toBe('true')
    const note = document.getElementById(groupsToggle().getAttribute('aria-describedby')!)
    expect(note?.textContent).toContain('At least one of groups and transactions stays shown')
    await act(async () => { groupsToggle().click() })
    expect(groupsToggle().checked).toBe(true)
    expect(line('Checkout_tps')).not.toBeNull()

    // Row choices made while transactions are hidden are their own: showing
    // transactions again brings back the choices made with both shown.
    await act(async () => { tpsChart()?.querySelector<HTMLInputElement>('input[aria-label="Show Browse"]')?.click() })
    expect(line('Browse_tps')).toBeNull()
    await act(async () => { transactionsToggle().click() })
    expect(line(loginKey)).not.toBeNull()
    expect(line('Checkout_tps')).not.toBeNull()
    expect(line('Browse_tps')).not.toBeNull()
    // And a choice made with both shown survives hiding a kind and back.
    await act(async () => { tpsChart()?.querySelector<HTMLInputElement>('input[aria-label="Show Checkout"]')?.click() })
    expect(line('Checkout_tps')).toBeNull()
    await act(async () => { transactionsToggle().click() })
    expect(line('Browse_tps')).toBeNull()
    expect(line('Checkout_tps')).not.toBeNull()
    await act(async () => { transactionsToggle().click() })
    expect(line('Checkout_tps')).toBeNull()
    expect(line(loginKey)).not.toBeNull()
    await act(async () => { tpsChart()?.querySelector<HTMLInputElement>('input[aria-label="Show Checkout"]')?.click() })
    await act(async () => { transactionsToggle().click() })
    await act(async () => { tpsChart()?.querySelector<HTMLInputElement>('input[aria-label="Show Browse"]')?.click() })

    // Groups off instead: the transactions and the total stay.
    await act(async () => { transactionsToggle().click() })
    await act(async () => { groupsToggle().click() })
    expect(line('Checkout_tps')).toBeNull()
    expect(line('Browse_tps')).toBeNull()
    expect(line(loginKey)).not.toBeNull()
    expect(line('tps')).not.toBeNull()
    expect(transactionsToggle().getAttribute('aria-disabled')).toBe('true')
    expect(window.localStorage.getItem('apexload.execution.groupSeries')).toBe('false')

    // The table under the graph follows the same choice.
    const table = () => tpsChart()?.querySelector('.chart-metrics-table')
    expect(table()).not.toBeNull()
    expect(table()?.textContent).toContain('Login')
    expect(table()?.textContent).not.toContain('Checkout')

    // The choice is remembered on the next visit.
    await act(async () => { root.unmount() })
    root = createRoot(container)
    await renderSeriesCharts()
    expect(line('Checkout_tps')).toBeNull()
    expect(line(loginKey)).not.toBeNull()

    // Reset Graphs shows both again.
    await act(async () => {
      buttonByText('Configure Graphs')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => { buttonByText('Reset Graphs')?.click() })
    expect(line('Checkout_tps')).not.toBeNull()
    expect(line(loginKey)).not.toBeNull()
    expect(window.localStorage.getItem('apexload.execution.groupSeries')).toBe('true')

    // A stored pair that hides both falls back to both.
    window.localStorage.setItem('apexload.execution.groupSeries', 'false')
    window.localStorage.setItem('apexload.execution.transactionSeries', 'false')
    await act(async () => { root.unmount() })
    root = createRoot(container)
    await renderSeriesCharts()
    expect(line('Checkout_tps')).not.toBeNull()
    expect(line(loginKey)).not.toBeNull()
  })

  it('shows an overlay in every series setting', async () => {
    const point = (time: string, tps: number): MetricsHistoryPoint => ({
      time, vusers: 4, tps, avgMs: 100, errorPct: 0, p50: 80, p90: 120, p99: 160,
      Checkout_tps: tps / 2, Checkout_avgMs: 100, Checkout_errorPct: 0,
      Browse_tps: tps / 2, Browse_avgMs: 100, Browse_errorPct: 0,
      [transactionChartSeriesKey('Login', 'tps')]: tps, [transactionChartSeriesKey('Login', 'avgMs')]: 90, [transactionChartSeriesKey('Login', 'errorPct')]: 0,
      [transactionChartSeriesKey('Search', 'tps')]: tps, [transactionChartSeriesKey('Search', 'avgMs')]: 90, [transactionChartSeriesKey('Search', 'errorPct')]: 0,
    })
    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={[point('10:00:00', 4), point('10:00:01', 6)]}
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
          groupNames={['Checkout', 'Browse']}
          groupColors={['#00a3ff', '#ffaa00']}
        />,
      )
    })
    await act(async () => {
      buttonByText('Configure Graphs')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    // The overlay adds itself to the title.
    const tpsChart = () => Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.querySelector('.chart-title')?.textContent?.startsWith('Transactions Per Second'))
    const groupsToggle = () => container.querySelector<HTMLInputElement>('input[aria-label="Show group series on graphs"]')!
    const vusersOverlay = () => tpsChart()?.querySelector('path[data-testid="mock-line"][data-name="Active VUsers"]') ?? null

    // Rows chosen with groups hidden, then the overlay turned on with both shown.
    await act(async () => { groupsToggle().click() })
    await act(async () => { tpsChart()?.querySelector<HTMLInputElement>('input[aria-label="Show Transaction · Search"]')?.click() })
    await act(async () => { groupsToggle().click() })
    await act(async () => {
      tpsChart()?.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 180, clientY: 160 }))
    })
    await act(async () => {
      menuButton('Overlay VUsers')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(vusersOverlay()).not.toBeNull()

    await act(async () => { groupsToggle().click() })
    expect(vusersOverlay()).not.toBeNull()
  })

  it('keeps row choices across a window without transactions', async () => {
    const point = (time: string, tps: number, withTransactions: boolean): MetricsHistoryPoint => ({
      time, vusers: 4, tps, avgMs: 100, errorPct: 0, p50: 80, p90: 120, p99: 160,
      Checkout_tps: tps / 2, Checkout_avgMs: 100, Checkout_errorPct: 0,
      Browse_tps: tps / 2, Browse_avgMs: 100, Browse_errorPct: 0,
      ...(withTransactions ? { [transactionChartSeriesKey('Login', 'tps')]: tps, [transactionChartSeriesKey('Login', 'avgMs')]: 90, [transactionChartSeriesKey('Login', 'errorPct')]: 0 } : {}),
    })
    const renderWindow = async (withTransactions: boolean) => {
      await act(async () => {
        root.render(
          <TestExecutionCharts
            runId="run-1"
            filteredData={[point('10:00:00', 4, withTransactions), point('10:00:01', 6, withTransactions)]}
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
            groupNames={['Checkout', 'Browse']}
            groupColors={['#00a3ff', '#ffaa00']}
          />,
        )
      })
    }
    await renderWindow(true)
    await act(async () => {
      buttonByText('Configure Graphs')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const tpsChart = () => Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.querySelector('.chart-title')?.textContent === 'Transactions Per Second')
    const browseLine = () => tpsChart()?.querySelector('path[data-testid="mock-line"][data-key="Browse_tps"]') ?? null

    await act(async () => { container.querySelector<HTMLInputElement>('input[aria-label="Show transaction series on graphs"]')!.click() })
    await act(async () => { tpsChart()?.querySelector<HTMLInputElement>('input[aria-label="Show Browse"]')?.click() })
    expect(browseLine()).toBeNull()

    // A zoom onto a stretch before the first transaction keeps the choice.
    await renderWindow(false)
    expect(browseLine()).toBeNull()
    await renderWindow(true)
    expect(browseLine()).toBeNull()
  })

  it('keeps row choices on graphs without group or transaction lines', async () => {
    const point = (time: string, tps: number): MetricsHistoryPoint => ({
      time, vusers: 4, tps, avgMs: 100, errorPct: 0, p50: 80, p90: 120, p99: 160,
      netSent: 10, netRecv: 20, diskRead: 30, diskWrite: 40,
      Checkout_tps: tps / 2, Checkout_avgMs: 100, Checkout_errorPct: 0,
      Browse_tps: tps / 2, Browse_avgMs: 100, Browse_errorPct: 0,
      [transactionChartSeriesKey('Login', 'tps')]: tps, [transactionChartSeriesKey('Login', 'avgMs')]: 90, [transactionChartSeriesKey('Login', 'errorPct')]: 0,
    })
    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={[point('10:00:00', 4), point('10:00:01', 6)]}
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
          groupNames={['Checkout', 'Browse']}
          groupColors={['#00a3ff', '#ffaa00']}
        />,
      )
    })
    await act(async () => {
      buttonByText('Configure Graphs')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const ioChart = () => Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.querySelector('.chart-title')?.textContent?.startsWith('Network & Disk I/O'))
    const diskWriteRow = () => ioChart()?.querySelector<HTMLInputElement>('input[aria-label="Show Disk Write"]') ?? null
    expect(diskWriteRow()?.checked).toBe(true)

    await act(async () => { diskWriteRow()?.click() })
    expect(diskWriteRow()?.checked).toBe(false)
    await act(async () => { container.querySelector<HTMLInputElement>('input[aria-label="Show transaction series on graphs"]')!.click() })
    expect(diskWriteRow()?.checked).toBe(false)
    await act(async () => { container.querySelector<HTMLInputElement>('input[aria-label="Show transaction series on graphs"]')!.click() })
    await act(async () => { container.querySelector<HTMLInputElement>('input[aria-label="Show group series on graphs"]')!.click() })
    expect(diskWriteRow()?.checked).toBe(false)
  })

  it('keeps row choices when a series toggle has nothing to hide', async () => {
    const point = (time: string, tps: number): MetricsHistoryPoint => ({
      time, vusers: 4, tps, avgMs: 100, errorPct: 0, p50: 80, p90: 120, p99: 160,
      [transactionChartSeriesKey('Login', 'tps')]: tps, [transactionChartSeriesKey('Login', 'avgMs')]: 90, [transactionChartSeriesKey('Login', 'errorPct')]: 0,
      [transactionChartSeriesKey('Search', 'tps')]: tps, [transactionChartSeriesKey('Search', 'avgMs')]: 90, [transactionChartSeriesKey('Search', 'errorPct')]: 0,
    })
    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={[point('10:00:00', 4), point('10:00:01', 6)]}
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
          groupNames={['Only']}
          groupColors={['#00a3ff']}
        />,
      )
    })
    await act(async () => {
      buttonByText('Configure Graphs')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const tpsChart = () => Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.querySelector('.chart-title')?.textContent === 'Transactions Per Second')
    const searchLine = () => tpsChart()?.querySelector(`path[data-testid="mock-line"][data-key="${transactionChartSeriesKey('Search', 'tps')}"]`) ?? null

    await act(async () => { tpsChart()?.querySelector<HTMLInputElement>('input[aria-label="Show Transaction · Search"]')?.click() })
    expect(searchLine()).toBeNull()
    // A single-group run draws no group lines, so hiding groups changes nothing.
    await act(async () => { container.querySelector<HTMLInputElement>('input[aria-label="Show group series on graphs"]')!.click() })
    expect(searchLine()).toBeNull()
  })

  it('bounds transaction legends with an explicit overflow count while retaining every line', async () => {
    const transactionNames = Array.from({ length: 30 }, (_, index) => `Transaction ${String(index).padStart(2, '0')}`)
    const point: MetricsHistoryPoint = {
      time: '10:00:00',
      vusers: 10,
      tps: 30,
      avgMs: 100,
      errorPct: 0,
      p50: 90,
      p90: 110,
      p99: 150,
    }
    transactionNames.forEach(name => {
      point[transactionChartSeriesKey(name, 'tps')] = 1
      point[transactionChartSeriesKey(name, 'avgMs')] = 100
      point[transactionChartSeriesKey(name, 'errorPct')] = 0
    })

    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={[point]}
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

    const tpsChart = Array.from(container.querySelectorAll('.chart-card'))
      .find(chart => chart.textContent?.includes('Transactions Per Second'))
    const responseTimeChart = Array.from(container.querySelectorAll('.chart-card'))
      .find(chart => chart.textContent?.includes('Response Time (ms)'))
    const transactionLines = tpsChart?.querySelectorAll('path[data-testid="mock-line"][data-key^="transaction:"]')

    expect(transactionLines).toHaveLength(transactionNames.length)
    expect(tpsChart?.querySelectorAll('.chart-series-legend-item')).toHaveLength(24)
    expect(tpsChart?.querySelector('.chart-series-legend-overflow')?.textContent).toBe('+7 more visible series')
    expect(responseTimeChart?.querySelector('.chart-series-legend-overflow')?.textContent).toBe('+7 more visible transaction series')
  })

  it('caps tooltip entries and reports how many visible series were omitted', async () => {
    const payload = Array.from({ length: 20 }, (_, index) => ({
      dataKey: `transaction-${index}`,
      name: `Transaction ${index}`,
      value: index,
      color: '#00a3ff',
      payload: {},
    }))

    await act(async () => {
      root.render(<BoundedChartTooltipContent active payload={payload} label="10:00:00" />)
    })

    expect(container.querySelectorAll('.bounded-chart-tooltip-item')).toHaveLength(12)
    expect(container.querySelector('.chart-tooltip-overflow')?.textContent).toBe('+8 more visible series')
    expect(container.textContent).toContain('10:00:00')
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
    // role=group: the Recharts surface inside is focusable, which role=img forbids.
    expect(chartVisual?.getAttribute('role')).toBe('group')
    expect(chartVisual?.getAttribute('aria-label')).toContain('Transactions Per Second chart. 2 samples.')
    expect(tableId).toBeTruthy()
    expect(tpsChart?.querySelector(`#${tableId}`)?.textContent).toContain('Transactions Per Second measurements')
    expect(tpsChart?.querySelector(`#${tableId}`)?.textContent).toContain('Latest')
    expect(tpsChart?.querySelector('button[aria-label="Expand Transactions Per Second"]')).not.toBeNull()
  })

  it('uses one summary source for visible measurements and screen-reader tables', async () => {
    const longFilteredData = Array.from({ length: 520 }, (_, index) => ({
      time: `10:${String(Math.floor(index / 60)).padStart(2, '0')}:${String(index % 60).padStart(2, '0')}`,
      vusers: index + 1,
      tps: index / 10,
      avgMs: index * 1.5 + 100,
      errorPct: index % 13 === 0 ? 0.4 : 0.0,
      p50: index * 1.5 + 110,
      p90: index * 1.5 + 130,
      p99: index * 1.5 + 150,
    }))

    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={longFilteredData}
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
          latestLatency={{ targets: [] }}
        />,
      )
    })

    const responseChart = Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.textContent?.includes('Response Time (ms)'))
    expect(responseChart).toBeTruthy()
    if (!responseChart) return

    const visibleRows = Array.from(responseChart.querySelectorAll('.chart-metrics-table tbody tr'))
    const a11yRows = Array.from(responseChart.querySelectorAll('table.chart-a11y-table tbody tr'))

    expect(visibleRows.length).toBeGreaterThan(0)
    expect(a11yRows.length).toBeGreaterThan(0)
    expect(visibleRows.length).toBe(a11yRows.length)

    const normalize = (rows: Element[]) => rows.map(row => row.textContent?.replace(/\s+/g, ' ').trim())
    expect(normalize(visibleRows)).toEqual(normalize(a11yRows))
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
            { time: '10:00:00', vusers: 2, tps: 3, avgMs: 100, errorPct: 0, p50: 80, p90: 100, p95: 115, p99: 2200, Checkout_p99: 2200, Browse_p99: 90 },
            { time: '10:00:01', vusers: 4, tps: 6, avgMs: 110, errorPct: 0, p50: 85, p90: 105, p95: 118, p99: 2400, Checkout_p99: 2400, Browse_p99: 95 },
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
          groupNames={['Checkout', 'Browse']}
          groupColors={['#00a3ff', '#ffaa00']}
        />,
      )
    })

    const p99Chart = Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.textContent?.includes('P99 Response Time (ms)'))
    expect(primaryAxisDomain(p99Chart)).toBe('0|2592')
    expect(p99Chart?.querySelector('path[data-testid="mock-line"][data-key="p99"]')).not.toBeNull()
    expect(p99Chart?.querySelector('path[data-testid="mock-line"][data-key="Checkout_p99"]')).not.toBeNull()
    expect(p99Chart?.querySelector('path[data-testid="mock-line"][data-key="Browse_p99"]')).not.toBeNull()

    const hideTotal = Array.from(p99Chart?.querySelectorAll<HTMLInputElement>('input[type="checkbox"]') ?? [])
      .find(input => input.getAttribute('aria-label') === 'Show Total')
    const hideCheckout = Array.from(p99Chart?.querySelectorAll<HTMLInputElement>('input[type="checkbox"]') ?? [])
      .find(input => input.getAttribute('aria-label') === 'Show Checkout')
    expect(hideTotal).not.toBeUndefined()
    expect(hideCheckout).not.toBeUndefined()

    await act(async () => {
      hideTotal?.click()
    })
    await act(async () => {
      hideCheckout?.click()
    })

    expect(p99Chart?.querySelector('path[data-testid="mock-line"][data-key="Checkout_p99"]')).toBeNull()
    expect(primaryAxisDomain(p99Chart)).toBe('0|102.6')
    expect(window.localStorage.getItem('apexload.execution.chartMetricFilters')).toContain('"latency_p99":["Browse_p99"]')
  })

  it('scales the graph axis to the smoothed line rather than the raw spike', async () => {
    const spikeIndex = 20
    const spikeData: MetricsHistoryPoint[] = Array.from({ length: 41 }, (_, index) => {
      const avgMs = index === spikeIndex ? 5000 : 100
      return {
        time: `10:00:${String(index).padStart(2, '0')}`,
        vusers: 2,
        tps: 3,
        avgMs,
        errorPct: 0,
        p50: avgMs,
        p90: avgMs,
        p95: avgMs,
        p99: avgMs,
      }
    })

    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={spikeData}
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
          surface="command-center"
        />,
      )
    })

    const responseChart = Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.textContent?.includes('Response Time (ms)'))
    // Command Center smooths the drawn series, so the spike never reaches 5000 on
    // screen. Scaling to the raw peak would flatten the 100ms baseline against the
    // x-axis; the axis must follow what is actually plotted.
    const [floor, ceiling] = (primaryAxisDomain(responseChart) ?? '').split('|').map(Number)
    expect(floor).toBe(0)
    expect(ceiling).toBeGreaterThan(100)
    expect(ceiling).toBeLessThan(5000)
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

  it('keeps percentiles off the response time graph and draws one graph per percentile', async () => {
    await renderCharts()

    const chartCard = (title: string) => Array.from(container.querySelectorAll('.chart-card')).find(chart => chart.textContent?.includes(title))
    const responseChart = chartCard('Response Time (ms)')
    expect(responseChart?.querySelector('.chart-series-legend')?.textContent).not.toContain('Percentiles')
    expect(responseChart?.querySelector('path[data-testid="mock-line"][data-name="Total"]')?.getAttribute('data-stroke-width')).toBe('2.5')
    expect(responseChart?.querySelector('path[data-testid="mock-line"][data-name="Total"]')?.getAttribute('data-stroke-dasharray')).toBe('')
    expect(responseChart?.querySelector('path[data-testid="mock-line"][data-key="p95"]')).toBeNull()
    expect(responseChart?.querySelector('path[data-testid="mock-line"][data-key="p99"]')).toBeNull()

    const titles = chartTitles()
    expect(titles).toContain('P50 Response Time (ms)')
    expect(titles).toContain('P90 Response Time (ms)')
    expect(titles).toContain('P95 Response Time (ms)')
    expect(titles).toContain('P99 Response Time (ms)')

    const p99Chart = chartCard('P99 Response Time (ms)')
    expect(p99Chart?.querySelector('path[data-testid="mock-line"][data-key="p99"]')?.getAttribute('data-stroke-width')).toBe('2')
    expect(p99Chart?.querySelector('path[data-testid="mock-line"][data-key="p50"]')).toBeNull()
  })

  it('renders load-under graphs as selectable runtime graph cards', async () => {
    await renderCharts(null, false)

    const artUnderLoad = () => Array.from(container.querySelectorAll('.chart-card'))
      .find(chart => chart.textContent?.includes('ART Under Load'))

    expect(chartTitles()).toContain('ART Under Load')
    expect(chartTitles()).toContain('TPS Under Load')
    expect(chartTitles()).toContain('Error rate Under Load')
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

  it('carries empty response intervals into explicit Under Load data', async () => {
    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={[]}
          loadImpactData={[
            { time: '10:00:00', vusers: 5, tps: 1, avgMs: 100, errorPct: 0, p50: 90, p90: 120, p95: 140, p99: 160 },
            { time: '10:00:01', vusers: 5, tps: 1, avgMs: 0, errorPct: 0, p50: 0, p90: 0, p95: 0, p99: 0 },
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
          timeRange="all"
          onTimeRangeChange={vi.fn()}
          isMultiGroup={false}
          groupNames={[]}
          groupColors={['#00a3ff']}
          showLoadImpact={false}
        />,
      )
    })

    const artUnderLoad = Array.from(container.querySelectorAll('.chart-card'))
      .find(chart => chart.textContent?.includes('ART Under Load'))
    const averageRow = Array.from(artUnderLoad?.querySelectorAll('tbody tr') ?? [])
      .find(row => row.querySelector('th')?.textContent === 'Average')
    expect(averageRow?.textContent).toContain('100ms100ms100ms100ms')
  })

  it('normalizes zero response-time sentinels at the chart boundary', async () => {
    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={[
            { time: '10:00:00', timestampMs: 0, vusers: 5, tps: 2, avgMs: 100, errorPct: 0, p50: 90, p90: 120, p95: 140, p99: 160 },
            { time: '10:00:01', timestampMs: 1_000, vusers: 5, tps: 2, avgMs: 0, errorPct: 0, p50: 0, p90: 0, p95: 0, p99: 0 },
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
          timeRange="all"
          onTimeRangeChange={vi.fn()}
          isMultiGroup={false}
          groupNames={[]}
          groupColors={['#00a3ff']}
          showLoadImpact={false}
        />,
      )
    })

    const responseTimeChart = Array.from(container.querySelectorAll('.chart-card'))
      .find(chart => chart.textContent?.includes('Response Time (ms)'))
    const totalRow = Array.from(responseTimeChart?.querySelectorAll('tbody tr') ?? [])
      .find(row => row.querySelector('th')?.textContent === 'Total')
    expect(totalRow?.textContent).toContain('100ms100ms100ms100ms')
  })

  it('scopes the Configure Graphs card list to the graph page in view', async () => {
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
          enableGraphGroupTabs
        />,
      )
    })

    await act(async () => {
      buttonByText('Configure Graphs')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const cardLabels = () => Array.from(container.querySelectorAll<HTMLInputElement>('.charts-config-option input[type="checkbox"]'))
      .map(input => input.getAttribute('aria-label'))

    // Live Metrics is in view: only its cards are listed.
    expect(cardLabels()).toContain('Show Response Time (ms)')
    expect(cardLabels()).toContain('Show P95 Response Time (ms)')
    expect(cardLabels()).not.toContain('Show ART Under Load')
    expect(cardLabels()).not.toContain('Show Load Impact Heatmap')
    expect(cardLabels()).not.toContain('Show CPU Usage — Fleet (%)')
    expect(container.querySelector('.charts-config-panel-section span')?.textContent).toBe('Add or remove live metrics graph cards')

    await act(async () => {
      Array.from(container.querySelectorAll('button[role="tab"]')).find(button => button.textContent?.includes('Under Load'))
        ?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    // Switching pages re-scopes the list, and hidden cards stay listed so they
    // can be switched back on.
    expect(cardLabels()).toContain('Show ART Under Load')
    expect(cardLabels()).toContain('Show Load Impact Heatmap')
    expect(cardLabels()).toContain('Show P Values Under Load')
    expect(cardLabels()).not.toContain('Show Response Time (ms)')
    expect(cardLabels()).not.toContain('Show P95 Response Time (ms)')

    await act(async () => {
      Array.from(container.querySelectorAll('button[role="tab"]')).find(button => button.textContent?.includes('Infrastructure'))
        ?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(cardLabels()).toEqual([
      'Show CPU Usage — Fleet (%)',
      'Show Memory — Fleet total (MB)',
      'Show Network & Disk I/O (KB/s)',
      'Show Network Phase Breakdown — Transaction Traffic (ms)',
      'Show Generator Impact',
      'Show Route Latency (ms)',
    ])
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

  it.each([undefined, false])('does not advertise unconfirmed graph sources (%s) during loading or errors', async available => {
    const availability = { prometheusAvailable: available, importedDashboardsAvailable: available }
    await renderGraphSourceState({ ...availability, prometheusLoading: true, importedDashboardsLoading: true })
    expect(container.querySelector('[role="tablist"][aria-label="Graph groups"]')?.textContent).not.toMatch(/Prometheus|Dashboards/)
    expect(container.textContent).not.toContain('Loading Prometheus')

    await renderGraphSourceState({ ...availability, prometheusError: 'Query failed', importedDashboardsError: 'Query failed' })
    expect(container.querySelector('[role="tablist"][aria-label="Graph groups"]')?.textContent).not.toMatch(/Prometheus|Dashboards/)
  })

  it('retains confirmed source tabs and the selected tab through empty, loading and failed refreshes', async () => {
    const availability = { prometheusAvailable: true, importedDashboardsAvailable: true, filteredData: [] }
    const sourceTab = (label: string) => Array.from(container.querySelectorAll<HTMLButtonElement>('[role="tab"]'))
      .find(tab => tab.textContent?.includes(label))
    await renderGraphSourceState(availability)
    expect(sourceTab('Prometheus')).toBeDefined()
    expect(sourceTab('Dashboards')).toBeDefined()
    await act(async () => sourceTab('Dashboards')!.click())

    await renderGraphSourceState({ ...availability, prometheusLoading: true, importedDashboardsLoading: true })
    expect(sourceTab('Prometheus')).toBeDefined()
    expect(sourceTab('Dashboards')?.getAttribute('aria-selected')).toBe('true')

    await renderGraphSourceState({ ...availability, prometheusError: 'Query failed', importedDashboardsError: 'Query failed' })
    expect(sourceTab('Prometheus')).toBeDefined()
    expect(sourceTab('Dashboards')?.getAttribute('aria-selected')).toBe('true')
    expect(container.textContent).toContain('Imported dashboards unavailable')

    await renderGraphSourceState({ prometheusAvailable: false, importedDashboardsAvailable: false })
    expect(sourceTab('Prometheus')).toBeUndefined()
    expect(sourceTab('Dashboards')).toBeUndefined()
    expect(sourceTab('Live Metrics')?.getAttribute('aria-selected')).toBe('true')
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
    expect(heatmapCard?.textContent).toContain('telemetry intervals')
  })

  it('draws one Under Load line per selected group instead of a single aggregate line', async () => {
    await act(async () => {
      root.render(
        <TestExecutionCharts
          filteredData={[
            { time: '10:00:00', vusers: 10, tps: 5, avgMs: 150, errorPct: 0, p50: 130, p90: 180, p95: 200, p99: 240, Checkout_vusers: 10, Search_vusers: 10, Checkout_avgMs: 100, Search_avgMs: 200 },
            { time: '10:01:00', vusers: 20, tps: 9, avgMs: 300, errorPct: 0, p50: 260, p90: 360, p95: 400, p99: 480, Checkout_vusers: 20, Search_vusers: 20, Checkout_avgMs: 200, Search_avgMs: 400 },
          ]}
          expandedChart="load_impact_avgMs"
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
          // The keyed mapping is authoritative on Graph Wall; the positional
          // fallback is deliberately reversed to catch color/order coupling.
          groupColors={['#ff8a00', '#00a3ff']}
          groupColorByName={{ Checkout: '#00a3ff', Search: '#ff8a00' }}
          showLoadImpact={false}
          surface="command-center"
          storageScope="apexload.commandCenter.grafana"
          enableGraphGroupTabs
          activeGraphGroupTab="under_load"
          underLoadDisplayMode="graphs"
        />,
      )
    })

    const artUnderLoad = () => Array.from(container.querySelectorAll('.chart-card'))
      .find(chart => chart.textContent?.includes('ART Under Load'))
    const lineFor = (key: string) => artUnderLoad()?.querySelector(`path[data-testid="mock-line"][data-key="${key}"]`)

    expect(lineFor('Checkout__loadImpactAverage')?.getAttribute('data-name')).toBe('Checkout')
    expect(lineFor('Checkout__loadImpactAverage')?.getAttribute('data-stroke')).toBe('#00a3ff')
    expect(lineFor('Checkout__loadImpactAverage')?.getAttribute('data-stroke-dasharray')).toBe('')
    expect(lineFor('Search__loadImpactAverage')?.getAttribute('data-name')).toBe('Search')
    expect(lineFor('Search__loadImpactAverage')?.getAttribute('data-stroke')).toBe('#ff8a00')
    expect(lineFor('average')).toBeNull()
    expect(artUnderLoad()?.querySelector('[data-testid="mock-x-axis"]')?.getAttribute('data-key')).toBe('vusers')
    expect(artUnderLoad()?.querySelector('.chart-series-legend')?.textContent).toContain('Checkout')
    expect(artUnderLoad()?.querySelector('.chart-series-legend')?.textContent).toContain('Search')

    const searchToggle = Array.from(artUnderLoad()?.querySelectorAll<HTMLInputElement>('input[type="checkbox"]') ?? [])
      .find(input => input.getAttribute('aria-label') === 'Show Search')
    expect(searchToggle?.checked).toBe(true)

    await act(async () => {
      searchToggle?.click()
    })

    expect(lineFor('Search__loadImpactAverage')).toBeNull()
    expect(lineFor('Checkout__loadImpactAverage')).not.toBeNull()
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
    expect(titles[0]).toBe('VUsers')
    expect(titles[1]).toBe('Transactions Per Second')
  })

  it('can suppress the chart-grid Load Impact card when the archive report owns it', async () => {
    await renderCharts(null, false)

    expect(container.querySelector('.load-impact-card:not(.load-impact-card-graph)')).toBeNull()
    expect(container.querySelector('.load-impact-card.load-impact-card-graph')).not.toBeNull()
    expect(chartTitles()[0]).toBe('VUsers')
  })

  it('rearranges graph cards by dragging a chart grabber', async () => {
    await renderCharts()

    expect(chartTitles().slice(0, 3)).toEqual([
      'VUsers',
      'Transactions Per Second',
      'Response Time (ms)',
    ])

    const cards = () => Array.from(container.querySelectorAll<HTMLElement>('.chart-card'))
    const responseCard = cards().find(card => card.textContent?.includes('Response Time (ms)'))
    const vusersCard = cards().find(card => card.textContent?.includes('VUsers'))
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
    expect(container.querySelector('.charts-toolbar')?.textContent).toContain('19 graphs')
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
    expect(displayWindowSlider?.max).toBe('61')
    expect(displayWindowSlider?.step).toBe('1')
    expect(displayWindowSlider?.value).toBe('3')
    expect(displayWindowSlider?.getAttribute('aria-valuetext')).toBe('3m')
    const displayWindowControl = drawer?.querySelector<HTMLElement>('.chart-window-slider-control')
    expect(displayWindowControl?.style.getPropertyValue('--chart-window-slider-position')).toBe('3.3333%')
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

    expect(container.querySelectorAll('.chart-card')).toHaveLength(18)
    expect(container.querySelector('.charts-toolbar')?.textContent).toContain('18 graphs')
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
    expect(Array.from(container.querySelectorAll('[data-testid="mock-responsive-container"]')).map(container => container.getAttribute('data-height'))).toEqual(Array(17).fill('132'))
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

    expect(container.querySelectorAll('.chart-card')).toHaveLength(18)

    const resetButton = buttonByText('Reset Graphs')
    expect(resetButton?.className).toContain('btn-chip')

    await act(async () => {
      resetButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelectorAll('.chart-card')).toHaveLength(19)
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
    expect(onTimeRangeChange).toHaveBeenLastCalledWith('31m')

    const presetGroup = container.querySelector('[aria-label="Graph display window presets"]')
    expect(presetGroup?.textContent).toContain('1m')
    expect(presetGroup?.textContent).toContain('3m')
    expect(presetGroup?.textContent).toContain('5m')
    expect(presetGroup?.textContent).toContain('10m')
    expect(presetGroup?.textContent).toContain('1h')
    await act(async () => {
      Array.from(presetGroup?.querySelectorAll<HTMLButtonElement>('button') ?? []).find(button => button.textContent === '10m')?.click()
    })
    expect(onTimeRangeChange).toHaveBeenLastCalledWith('10m')
    await act(async () => {
      presetGroup?.querySelector<HTMLButtonElement>('button:last-child')?.click()
    })
    expect(onTimeRangeChange).toHaveBeenLastCalledWith('60m')
  })

  it('runs the display window slider to the scheduled run length', async () => {
    const onTimeRangeChange = vi.fn()
    const renderWindow = async (timeRange: '10m' | '40m' | 'all', displayWindowMaxMinutes: number) => {
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
            timeRange={timeRange}
            onTimeRangeChange={onTimeRangeChange}
            displayWindowMaxMinutes={displayWindowMaxMinutes}
            isMultiGroup={false}
            groupNames={[]}
            groupColors={['#00a3ff']}
          />,
        )
      })
    }
    await renderWindow('10m', 25)
    await act(async () => {
      buttonByText('Configure Graphs')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const slider = () => container.querySelector<HTMLInputElement>('input[aria-label="Graph display window"]')!
    // A 25-minute schedule: minutes 1-25, then All.
    expect(slider().max).toBe('26')
    expect(slider().value).toBe('10')
    const presets = () => Array.from(container.querySelectorAll('[aria-label="Graph display window presets"] button')).map(button => button.textContent)
    expect(presets()).toEqual(['1m', '3m', '5m', '10m'])

    const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
    await act(async () => {
      valueSetter?.call(slider(), '25')
      slider().dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(onTimeRangeChange).toHaveBeenLastCalledWith('25m')
    await act(async () => {
      valueSetter?.call(slider(), '26')
      slider().dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(onTimeRangeChange).toHaveBeenLastCalledWith('all')

    // A window longer than the schedule becomes All, so a run that outlasts
    // its schedule is not trimmed while the slider reads All.
    onTimeRangeChange.mockClear()
    await renderWindow('40m', 25)
    expect(onTimeRangeChange).toHaveBeenCalledWith('all')
    expect(slider().value).toBe('26')
    expect(slider().getAttribute('aria-valuetext')).toBe('All')

    // A two-hour schedule reaches past the old one-hour limit.
    await renderWindow('all', 120)
    expect(slider().max).toBe('121')
    expect(presets()).toEqual(['1m', '3m', '5m', '10m', '1h'])
  })

  it('applies seconds to the live response-time graph title and axis', async () => {
    await renderCharts(null, true, true)
    const responseChart = () => Array.from(container.querySelectorAll<HTMLElement>('.chart-card'))
      .find(chart => chart.querySelector('.chart-title')?.textContent === 'Response Time (ms)')

    expect(responseChart()).not.toBeNull()
    await act(async () => {
      buttonByText('Configure Graphs')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const drawer = container.querySelector('.charts-config-drawer')
    const secondsButton = Array.from(drawer?.querySelectorAll('button') ?? [])
      .find(button => button.textContent?.trim() === 'Seconds')
    expect(secondsButton).toBeDefined()
    await act(async () => {
      secondsButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })

    const secondsResponseChart = Array.from(container.querySelectorAll<HTMLElement>('.chart-card'))
      .find(chart => chart.querySelector('.chart-title')?.textContent === 'Response Time (s)')
    expect(secondsResponseChart).not.toBeNull()
    expect(secondsResponseChart?.querySelector('[data-testid="mock-y-axis"]')?.textContent).toContain('0.1')
    expect(secondsResponseChart?.textContent).toContain('0.11 s')
    expect(secondsResponseChart?.textContent).not.toContain('110ms')

    const underLoadTab = Array.from(container.querySelectorAll('button'))
      .find(button => button.textContent?.includes('Under Load'))
    await act(async () => {
      underLoadTab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const secondsArtUnderLoad = Array.from(container.querySelectorAll<HTMLElement>('.chart-card'))
      .find(chart => chart.querySelector('.chart-title')?.textContent === 'ART Under Load')
    expect(secondsArtUnderLoad?.textContent).toContain('0.11 s')

    const millisecondsButton = Array.from(drawer?.querySelectorAll('button') ?? [])
      .find(button => button.textContent?.trim() === 'Milliseconds')
    await act(async () => {
      millisecondsButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })
    expect(secondsArtUnderLoad?.textContent).toContain('110ms')
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

  it('pages imported graphs by nine and resets or clamps pages as the dashboard changes', async () => {
    const dashboard = (id: string, count: number): ImportedDashboardResult => ({
      dashboard_id: id,
      dashboard_title: `Dashboard ${id}`,
      panels: Array.from({ length: count }, (_, panelId) => ({
        panel_id: panelId,
        title: `Panel ${panelId}`,
        kind: 'line',
        series: [],
      })),
    })
    const renderImported = async (dashboards: ImportedDashboardResult[], runId = 'run-a') => {
      await act(async () => {
        root.render(
          <TestExecutionCharts
            filteredData={[]}
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
            groupColors={[]}
            showLoadImpact={false}
            surface="command-center"
            storageScope="apexload.commandCenter.grafana"
            runId={runId}
            enableGraphGroupTabs
            activeGraphGroupTab="imported"
            importedDashboards={dashboards}
          />,
        )
      })
    }
    const ids = () => Array.from(container.querySelectorAll<HTMLElement>('.chart-card[data-chart-id]')).map(card => card.dataset.chartId)
    const pageSummary = () => container.querySelector('[aria-label="Graph pages"]')?.textContent
    const nextPage = async () => {
      await act(async () => container.querySelector<HTMLButtonElement>('[aria-label="Next graph page"]')!.click())
    }

    const dashboards = [dashboard('a', 12), dashboard('b', 11)]
    await renderImported(dashboards)
    expect(ids()).toEqual(Array.from({ length: 9 }, (_, id) => `gdash_a_${id}`))
    expect(pageSummary()).toContain('Graphs 1–9 of 12 · Page 1 of 2')

    await nextPage()
    expect(ids()).toEqual(['gdash_a_9', 'gdash_a_10', 'gdash_a_11'])

    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>('[aria-label="Imported dashboards"] button'))
      .find(button => button.textContent?.startsWith('Dashboard b'))!.click())
    expect(ids()).toEqual(Array.from({ length: 9 }, (_, id) => `gdash_b_${id}`))
    expect(pageSummary()).toContain('Page 1 of 2')

    await nextPage()
    expect(ids()).toEqual(['gdash_b_9', 'gdash_b_10'])

    await renderImported([dashboards[0], dashboard('b', 2)])
    expect(ids()).toEqual(['gdash_b_0', 'gdash_b_1'])
    expect(pageSummary()).toContain('Page 1 of 1')

    await renderImported(dashboards)
    expect(pageSummary()).toContain('Page 1 of 2')
    await nextPage()
    await renderImported(dashboards, 'run-b')
    expect(pageSummary()).toContain('Page 1 of 2')

    await nextPage()
    await act(async () => buttonByText('Configure Graphs')!.click())
    await act(async () => buttonByText('Reset Graphs')!.click())
    expect(pageSummary()).toContain('Page 1 of 2')
    expect(ids()).toHaveLength(9)
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
    expect(granularitySelect).toBeTruthy()
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
    return Array.from(container.querySelectorAll<HTMLButtonElement>('.chart-context-menu button')).find(button => button.textContent === label)
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

  async function renderGraphSourceState(overrides: Partial<ComponentProps<typeof TestExecutionCharts>>) {
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
          timeRange="all"
          onTimeRangeChange={vi.fn()}
          isMultiGroup={false}
          groupNames={[]}
          groupColors={[]}
          showLoadImpact={false}
          surface="command-center"
          enableGraphGroupTabs
          {...overrides}
        />,
      )
    })
  }

  async function renderCharts(expandedChart: string | null = null, showLoadImpact = true, commandCenter = false, runId?: string) {
    await act(async () => {
      root.render(
        <TestExecutionCharts
          runId={runId}
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
          surface={commandCenter ? 'command-center' : 'execution'}
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
