// @vitest-environment jsdom

import { act } from 'react'
import type { ReactNode } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import TestExecutionCharts from './TestExecutionCharts'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('recharts', () => {
  const Mock = ({ children }: { children?: ReactNode }) => <g>{children}</g>
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
    labelFormatter,
    labelStyle,
  }: {
    labelFormatter?: (value: string | number) => ReactNode
    labelStyle?: { display?: string }
  }) => (
    <g
      data-testid="mock-tooltip"
      data-label-display={labelStyle?.display ?? ''}
      data-label-text={labelFormatter ? String(labelFormatter('10:00:01') ?? '') : ''}
    />
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
    ResponsiveContainer: ({ children, height }: { children?: ReactNode; height?: number }) => <div data-testid="mock-responsive-container" data-height={String(height ?? '')}>{children}</div>,
    Tooltip: MockTooltip,
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
    expect(vusersChart?.textContent).toContain('ART')
    expect(window.localStorage.getItem('apexload.execution.artOverlayCharts')).toBe('["vusers"]')

    await act(async () => {
      vusersChart?.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: 180, clientY: 160 }))
    })

    expect(menuActionTexts()).toContain('Hide ART Overlay')
    expect(menuActionTexts()).toContain('Overlay TPS')

    await act(async () => {
      menuButton('Overlay TPS')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(vusersChart?.querySelector('path[data-testid="mock-line"][data-name="TPS"]')).not.toBeNull()
    expect(vusersChart?.textContent).toContain('TPS')
    expect(window.localStorage.getItem('apexload.execution.tpsOverlayCharts')).toBe('["vusers"]')
  })

  it('keeps synced graph tooltips while only the hovered chart displays the time label', async () => {
    await renderCharts()

    const mockCharts = Array.from(container.querySelectorAll('[data-testid="mock-chart"]'))
    expect(mockCharts).toHaveLength(8)
    expect(mockCharts.map(chart => chart.getAttribute('data-sync-id'))).toEqual(Array(8).fill('metrics'))
    expect(Array.from(container.querySelectorAll('[data-testid="mock-tooltip"]')).map(tooltip => tooltip.getAttribute('data-label-display'))).toEqual(Array(8).fill('none'))

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

  it('always shows graph settings controls without expanding a customize panel', async () => {
    await renderCharts()

    expect(buttonByText('Customize Graphs')).toBeUndefined()
    expect(buttonByText('Done')).toBeUndefined()
    expect(container.querySelector('.charts-customize-bar')).toBeNull()
    expect(container.querySelector('.charts-customize-actions')).toBeNull()
    expect(container.querySelector('.monitor-preset-buttons')).toBeNull()
    expect(container.querySelector('.charts-customize-panel')?.textContent).toContain('Display Window')
    expect(container.querySelector('.charts-customize-panel')?.textContent).toContain('3m')
    expect(container.querySelector('.charts-customize-panel')?.textContent).toContain('10m')
    expect(container.querySelector('.charts-customize-panel')?.textContent).toContain('Max')
    expect(container.querySelector('.charts-customize-panel')?.textContent).not.toContain('Custom')
    expect(container.querySelector('.charts-customize-panel')?.textContent).toContain('Zoom')
    expect(container.querySelector('.charts-customize-panel')?.textContent).toContain('6 Graphs')
    expect(container.querySelector('.charts-customize-panel')?.textContent).toContain('Role View')
    expect(container.querySelector('.charts-customize-panel')?.textContent).toContain('Display Time')
    expect(container.querySelector('.charts-customize-panel')?.textContent).not.toContain('Network latency')
    expect(container.querySelector('.charts-customize-panel')?.textContent).not.toContain('Removed')
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
    expect(Array.from(container.querySelectorAll('[data-testid="mock-responsive-container"]')).map(container => container.getAttribute('data-height'))).toEqual(Array(8).fill('132'))
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

    expect(container.querySelectorAll('.chart-card')).toHaveLength(8)
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

    expect(container.querySelectorAll('.chart-card')).toHaveLength(8)
    expect(roleSelect?.value).toBe('')
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
            { time: '10:00:00', vusers: 2, tps: 1, avgMs: 100, errorPct: 0, p50: 80, p90: 100, p99: 130, latency_edge: 20 },
            { time: '10:00:01', vusers: 4, tps: 2, avgMs: 110, errorPct: 0, p50: 85, p90: 105, p99: 135, latency_edge: 27.4 },
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
        />,
      )
    })
  }
})
