// @vitest-environment jsdom

import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import TestExecutionCharts from './TestExecutionCharts'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('recharts', () => {
  const Mock = ({ children }: { children?: ReactNode }) => <g>{children}</g>
  const labelText = (label?: string | { value?: string }) => (typeof label === 'string' ? label : label?.value ?? '')
  const labelPosition = (label?: string | { position?: string }) => (typeof label === 'string' ? '' : label?.position ?? '')
  const MockChart = ({
    children,
    syncId,
    onMouseMove,
  }: {
    children?: ReactNode
    syncId?: string
    onMouseMove?: (event: { activeLabel?: string }) => void
  }) => (
    <svg
      data-testid="mock-chart"
      data-sync-id={syncId ?? ''}
      width="640"
      height="200"
      onMouseMove={() => onMouseMove?.({ activeLabel: '10:00:01' })}
    >
      {children}
    </svg>
  )
  const MockTooltip = ({
    formatter,
    labelFormatter,
    labelStyle,
  }: {
    formatter?: (value: unknown, name: unknown) => ReactNode | [ReactNode, ReactNode]
    labelFormatter?: (value: string | number) => ReactNode
    labelStyle?: { display?: string }
  }) => {
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
    ResponsiveContainer: ({ children, height }: { children?: ReactNode; height?: number }) => <div data-testid="mock-responsive-container" data-height={String(height ?? '')}>{children}</div>,
    Tooltip: MockTooltip,
    XAxis: ({ dataKey, label }: { dataKey?: string; label?: string | { value?: string; position?: string } }) => (
      <g data-testid="mock-x-axis" data-key={dataKey ?? ''} data-label={labelText(label)} data-label-position={labelPosition(label)} />
    ),
    YAxis: ({ yAxisId, label, orientation }: { yAxisId?: string | number; label?: string | { value?: string; position?: string }; orientation?: string }) => (
      <g data-testid="mock-y-axis" data-y-axis-id={String(yAxisId ?? '')} data-orientation={orientation ?? 'left'} data-label={labelText(label)} data-label-position={labelPosition(label)} />
    ),
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
    expect(vusersChart?.querySelector('[data-testid="mock-x-axis"]')?.getAttribute('data-label')).toBe('Clock time')
    expect(axisLabels(vusersChart)).toContain('VUsers')
    expect(axisLabelPositions(vusersChart)).toContain('left')

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
    expect(axisLabels(vusersChart)).toEqual(expect.arrayContaining(['VUsers', 'ART (ms)']))
    expect(axisLabelPositions(vusersChart)).toEqual(expect.arrayContaining(['left', 'right']))
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
    expect(axisLabels(vusersChart)).toEqual(expect.arrayContaining(['VUsers', 'ART (ms) / TPS']))
    expect(window.localStorage.getItem('apexload.execution.tpsOverlayCharts')).toBe('["vusers"]')
  })

  it('keeps synced graph tooltips while only the hovered chart displays the time label', async () => {
    await renderCharts()

    const mockCharts = Array.from(container.querySelectorAll('[data-testid="mock-chart"]'))
    expect(mockCharts).toHaveLength(12)
    expect(mockCharts.map(chart => chart.getAttribute('data-sync-id'))).toEqual([...Array(9).fill('metrics'), '', '', ''])
    expect(Array.from(container.querySelectorAll('[data-testid="mock-tooltip"]')).map(tooltip => tooltip.getAttribute('data-label-display'))).toEqual(Array(12).fill('none'))

    const tpsChart = mockCharts[1]
    await act(async () => {
      tpsChart.dispatchEvent(new MouseEvent('mousemove', { bubbles: true }))
    })

    const tooltips = Array.from(container.querySelectorAll('[data-testid="mock-tooltip"]'))
    expect(tooltips.map(tooltip => tooltip.getAttribute('data-label-display'))).toEqual([
      'none',
      '',
      'none',
      'none',
      'none',
      'none',
      'none',
      'none',
      'none',
      'none',
      'none',
      'none',
    ])
    expect(tooltips.map(tooltip => tooltip.getAttribute('data-label-text'))).toEqual([
      '',
      '10:00:01',
      '',
      '',
      '',
      '',
      '',
      '',
      '',
      '',
      '',
      '',
    ])
    expect(tooltips.map(tooltip => tooltip.getAttribute('data-formatted-value'))).toEqual([
      ...Array(9).fill('123.46'),
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

  it('renders load-under graphs as selectable runtime graph cards', async () => {
    await renderCharts(null, false)

    const artUnderLoad = () => Array.from(container.querySelectorAll('.chart-card'))
      .find(chart => chart.textContent?.includes('ART Under Load'))

    expect(chartTitles()).toContain('ART Under Load')
    expect(chartTitles()).toContain('TPS Under Load')
    expect(chartTitles()).toContain('Error % Under Load')
    expect(chartTitles()).not.toContain('P99 Under Load')
    expect(artUnderLoad()?.querySelector('[data-testid="mock-x-axis"]')?.getAttribute('data-label')).toBe('VUser load')
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

    expect(container.querySelector('.load-impact-card')).toBeNull()
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
    expect(container.querySelector('.charts-toolbar')?.textContent).toContain('12 graphs')
    expect(container.querySelector('.charts-config-drawer')).toBeNull()

    await act(async () => {
      buttonByText('Configure Graphs')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const drawer = container.querySelector('.charts-config-drawer')
    expect(drawer?.textContent).toContain('Graph Configuration')
    expect(drawer?.textContent).toContain('Graph Cards')
    expect(drawer?.textContent).toContain('Display Window')
    expect(drawer?.textContent).toContain('3m')
    expect(drawer?.textContent).toContain('10m')
    expect(drawer?.textContent).toContain('Max')
    expect(drawer?.textContent).not.toContain('Custom')
    expect(drawer?.textContent).toContain('Zoom')
    expect(drawer?.textContent).toContain('6 Graphs')
    expect(drawer?.textContent).toContain('Role View')
    expect(drawer?.textContent).toContain('Display Time')
    expect(drawer?.textContent).not.toContain('Network latency')
    expect(drawer?.textContent).not.toContain('Removed')
    expect(container.querySelector('.btn-expand')?.hasAttribute('disabled')).toBe(false)
    expect(buttonByText('Clock Time')?.getAttribute('aria-pressed')).toBe('true')
    expect(buttonByText('Relative Time')?.getAttribute('aria-pressed')).toBe('false')
    expect(buttonByText('Standard')?.getAttribute('aria-pressed')).toBe('true')
    expect(buttonByText('6 Graphs')?.getAttribute('aria-pressed')).toBe('false')

    const roleSelect = container.querySelector<HTMLSelectElement>('select[aria-label="Graph role view"]')
    expect(roleSelect).not.toBeNull()
    expect(Array.from(roleSelect?.options ?? []).map(option => option.textContent)).toEqual([
      'Manual view',
      'Run Operator',
      'Performance Engineer',
      'Platform Admin',
      'Release Approver',
    ])

    const errorRateGraphToggle = container.querySelector<HTMLInputElement>('input[aria-label="Show Error Rate (%)"]')
    expect(errorRateGraphToggle?.checked).toBe(true)

    await act(async () => {
      errorRateGraphToggle?.click()
    })

    expect(container.querySelectorAll('.chart-card')).toHaveLength(11)
    expect(container.querySelector('.charts-toolbar')?.textContent).toContain('11 graphs')
    expect(window.localStorage.getItem('apexload.execution.hiddenCharts')).toContain('errors')

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
    expect(Array.from(container.querySelectorAll('[data-testid="mock-responsive-container"]')).map(container => container.getAttribute('data-height'))).toEqual(Array(11).fill('132'))
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

    await act(async () => {
      if (roleSelect) {
        roleSelect.value = 'release_approver'
        roleSelect.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })

    expect(container.querySelectorAll('.chart-card')).toHaveLength(3)
    expect(roleSelect?.value).toBe('release_approver')

    await act(async () => {
      if (roleSelect) {
        roleSelect.value = ''
        roleSelect.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })

    expect(container.querySelectorAll('.chart-card')).toHaveLength(12)
    expect(roleSelect?.value).toBe('')

    await act(async () => {
      if (roleSelect) {
        roleSelect.value = 'release_approver'
        roleSelect.dispatchEvent(new Event('change', { bubbles: true }))
      }
    })

    expect(container.querySelectorAll('.chart-card')).toHaveLength(3)

    const resetButton = buttonByText('Reset Graphs')
    expect(resetButton?.className).toContain('btn-chip')

    await act(async () => {
      resetButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelectorAll('.chart-card')).toHaveLength(12)
    expect(roleSelect?.value).toBe('')
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
    expect(axisLabels(routeChart())).toContain('Jitter (ms)')
    expect(axisLabels(routeChart())).toContain('Success (%)')
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

  function axisLabelPositions(chart: Element | undefined) {
    return Array.from(chart?.querySelectorAll('[data-testid="mock-y-axis"]') ?? [])
      .map(axis => axis.getAttribute('data-label-position'))
      .filter(Boolean)
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
