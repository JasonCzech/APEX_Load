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
    Line: ({ dataKey, strokeDasharray, connectNulls, dot }: { dataKey?: string; strokeDasharray?: string; connectNulls?: boolean; dot?: unknown }) => (
      <path data-testid="mock-line" data-key={dataKey || ''} data-dash={strokeDasharray || ''} data-connect-nulls={String(Boolean(connectNulls))} data-dot={String(Boolean(dot))} />
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

  it('marks root-cause spans on every lane and opens them from the legend', async () => {
    const onOverlaySelect = vi.fn()
    // Samples end at :00, :01 and :02, one second apart, so they start a
    // second earlier.
    const at = (second: string) => Date.parse(`2026-05-12T20:00:${second}Z`)
    await act(async () => {
      root.render(
        <SynchronizedTimeline
          data={[samplePoint('00', 100, 0), samplePoint('01', 700, 0), samplePoint('02', 100, 0)]}
          overlays={[
            { id: 'sym', kind: 'anomaly', start: at('00'), end: at('01'), label: 'Symptom: Checkout · p95 latency +600 ms (#1 Pool exhaustion)', tone: 'danger', ref: 'hypothesis:h-1' },
            { id: 'step', kind: 'phase', start: at('00'), label: 'Load step to 20 VUsers', tone: 'muted' },
            { id: 'suspect', kind: 'validity', start: at('00'), end: at('01'), label: 'Suspect run', tone: 'muted' },
          ]}
          onOverlaySelect={onOverlaySelect}
        />,
      )
    })
    const spans = Array.from(container.querySelectorAll('[data-testid="mock-reference-area"]')).map(area => [area.getAttribute('data-x1'), area.getAttribute('data-x2')])
    // One span per lane, from the sample starting at :00 to the one at :01.
    expect(spans).toEqual(Array(8).fill(['01', '02']))
    const legend = container.querySelector('[aria-label="Root-cause markers"]')!
    expect(legend.textContent).toContain('Load step to 20 VUsers')
    await act(async () => (legend.querySelector('button') as HTMLButtonElement).click())
    expect(onOverlaySelect).toHaveBeenCalledWith(expect.objectContaining({ id: 'sym', ref: 'hypothesis:h-1' }))
    // A load step or a suspect span has nothing to open.
    expect(legend.querySelectorAll('button')).toHaveLength(1)
    expect(legend.textContent).toContain('Suspect run')
  })

  it('draws continuous lines across resource-only samples', async () => {
    // A resource sample between two intervals has no throughput or latency.
    const resourceOnly = { ...samplePoint('01', Number.NaN, 0), avgMs: Number.NaN, p95: Number.NaN, errorPct: Number.NaN, resourceSampleOnly: 1 }
    await act(async () => {
      root.render(<SynchronizedTimeline data={[samplePoint('00', 100, 0), resourceOnly, samplePoint('02', 120, 0)]} />)
    })
    const lines = Array.from(container.querySelectorAll('[data-testid="mock-line"]'))
    expect(lines.length).toBeGreaterThan(0)
    expect(lines.every(line => line.getAttribute('data-connect-nulls') === 'true')).toBe(true)
  })

  it('breaks a line where its own samples stop for several intervals', async () => {
    const points = Array.from({ length: 12 }, (_, i) => ({ ...samplePoint(String(i).padStart(2, '0'), 100, 0), tps: i <= 3 || i >= 10 ? 20 : Number.NaN }))
    await act(async () => {
      root.render(<SynchronizedTimeline data={points} />)
    })
    const throughput = Array.from(container.querySelectorAll('[data-lane="throughput"] [data-testid="mock-line"]')).map(line => line.getAttribute('data-key'))
    expect(throughput.filter(key => key?.startsWith('tps'))).toEqual(['tps\u00010', 'tps\u00011'])
  })

  it('draws a lone sample between two gaps as a dot', async () => {
    const points = Array.from({ length: 15 }, (_, i) => ({ ...samplePoint(String(i).padStart(2, '0'), 100, 0), tps: i <= 3 || i === 8 || i >= 13 ? 20 : Number.NaN }))
    await act(async () => {
      root.render(<SynchronizedTimeline data={points} />)
    })
    const lines = Array.from(container.querySelectorAll('[data-lane="throughput"] [data-testid="mock-line"]')).filter(line => line.getAttribute('data-key')?.startsWith('tps'))
    expect(lines.map(line => line.getAttribute('data-dot'))).toEqual(['false', 'true', 'false'])
  })

  it('raises the start thumb where the thumbs meet in the right half', async () => {
    await act(async () => {
      root.render(<SynchronizedTimeline data={[0, 1, 2, 3, 4].map(i => samplePoint(String(i).padStart(2, '0'), 100, 0))} />)
    })
    const start = container.querySelector('[aria-label="Timeline window start"]') as HTMLInputElement
    const end = container.querySelector('[aria-label="Timeline window end"]') as HTMLInputElement
    for (const [input, value] of [[start, '3'], [end, '3']] as const) {
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(input, value)
        input.dispatchEvent(new Event('input', { bubbles: true }))
      })
    }
    expect(start.hasAttribute('data-raised')).toBe(true)
    for (const [input, value] of [[start, '1'], [end, '1']] as const) {
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(input, value)
        input.dispatchEvent(new Event('input', { bubbles: true }))
      })
    }
    expect(start.hasAttribute('data-raised')).toBe(false)
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
    // Both thumbs share one track; a thumb stops at the other.
    expect(start.closest('.run-report-timeline-window-range')).toBe(end.closest('.run-report-timeline-window-range'))
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(start, '3')
      start.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(start.value).toBe('2')
    expect(end.value).toBe('2')
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(start, '1')
      start.dispatchEvent(new Event('input', { bubbles: true }))
    })

    const charts = Array.from(container.querySelectorAll('[data-testid="mock-chart"]'))
    expect(charts.every(chart => chart.getAttribute('data-length') === '2')).toBe(true)
    await act(async () => button('Graphs').click())
    await act(async () => checkbox('P99 Response Time').click())
    const addedChart = container.querySelector('[data-lane="p99"] [data-testid="mock-chart"]')
    expect(addedChart?.getAttribute('data-length')).toBe('2')
    expect(addedChart?.getAttribute('data-sync-id')).toBe('metrics')
  })

  const button = (text: string) => Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(item => item.textContent?.startsWith(text))!
  const checkbox = (label: string) => container.querySelector<HTMLInputElement>(`input[aria-label="${label}"]`)!
  const inputValue = async (input: HTMLInputElement, value: string) => {
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(input, value)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
  }
  const selectTransactions = async () => {
    await act(async () => button('Transactions').click())
    await act(async () => {
      const select = container.querySelector('select')!
      select.value = 'selected'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
  }

  it('offers a searchable graph catalog, enforces six graphs, and permits removal at the limit', async () => {
    await act(async () => {
      root.render(<SynchronizedTimeline data={[samplePoint('00', 100, 0), samplePoint('01', 100, 0)]} />)
    })
    await act(async () => button('Graphs').click())
    expect(container.querySelectorAll('input[type="checkbox"]')).toHaveLength(23)
    expect(checkbox('CPU Usage').disabled).toBe(true)
    await act(async () => checkbox('P50 Response Time').click())
    await act(async () => checkbox('P99 Response Time').click())
    expect(container.querySelectorAll('[data-testid="mock-chart"]')).toHaveLength(6)
    expect(checkbox('P90 Response Time').disabled).toBe(true)
    expect(checkbox('VUsers').disabled).toBe(false)
    expect(container.textContent).toContain('Remove a graph to add another.')
    await act(async () => checkbox('VUsers').click())
    expect(checkbox('P90 Response Time').disabled).toBe(false)
    await inputValue(container.querySelector('input[type="search"]')!, 'p90')
    expect(container.querySelectorAll('input[type="checkbox"]')).toHaveLength(1)
    await act(async () => checkbox('P90 Response Time').click())
    expect(container.querySelector('[data-lane="p90"] [data-key="p90"]')).not.toBeNull()
    await act(async () => button('Restore defaults').click())
    expect(container.querySelectorAll('[data-testid="mock-chart"]')).toHaveLength(4)
    await inputValue(container.querySelector('input[type="search"]')!, 'not-a-graph')
    expect(container.textContent).toContain('No graphs match your search.')
  })

  it('filters performance to selected transactions, preserving choices across search and leaving load whole-run', async () => {
    await act(async () => root.render(<SynchronizedTimeline data={[samplePoint('00', 700, 8), samplePoint('01', 100, 0)]} goalConfig={{ p95_latency_ms: 500, max_error_pct: 5 }} showBaseline />))
    const transactionLines = () => Array.from(container.querySelectorAll('[data-testid="mock-line"]')).filter(line => line.getAttribute('data-key')?.startsWith('transaction:'))
    expect(transactionLines()).toHaveLength(0)
    await selectTransactions()
    expect(container.textContent).toContain('No transactions selected.')
    await inputValue(container.querySelector('input[type="search"]')!, 'check')
    const choices = container.querySelector('[aria-label="Transaction selection"]')!
    expect(choices.textContent).toContain('Checkout')
    expect(choices.textContent).not.toContain('Search')
    await act(async () => choices.querySelector<HTMLInputElement>('input')!.click())
    expect(transactionLines()).toHaveLength(3)
    expect(transactionLines().every(line => line.getAttribute('data-key')?.includes('Checkout'))).toBe(true)
    expect(container.querySelector('[data-key="tps"]')).toBeNull()
    expect(container.querySelector('[data-key="avgMs"]')).toBeNull()
    expect(container.querySelector('[data-key="baselineTps"]')).toBeNull()
    expect(container.querySelectorAll('[data-testid="mock-reference-area"], [data-testid="mock-reference-line"]')).toHaveLength(0)
    expect(container.querySelector('[data-key="vusers"]')).not.toBeNull()
    await inputValue(container.querySelector('input[type="search"]')!, 'search')
    expect(transactionLines()).toHaveLength(3) // Searching options does not change the applied filter.
    await act(async () => button('Transactions').click())
    expect(container.textContent).toContain('Showing only: Checkout.')
    expect(Array.from(container.querySelectorAll('[data-testid="mock-chart"]')).every(chart => chart.getAttribute('data-sync-id') === 'metrics')).toBe(true)
    await act(async () => button('Transactions').click())
    await act(async () => button('Clear selection').click())
    expect(transactionLines()).toHaveLength(0)
    await act(async () => {
      const select = container.querySelector('select')!
      select.value = 'all'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(container.querySelector('[data-key="tps"]')).not.toBeNull()
    expect(container.querySelector('[data-key="baselineTps"]')).not.toBeNull()
  })

  it('limits transaction series, while keeping checked transactions removable', async () => {
    const point = samplePoint('00', 100, 0)
    for (let index = 0; index < 20; index++) point[transactionChartSeriesKey(`Tx ${index}`, 'tps')] = index
    await act(async () => root.render(<SynchronizedTimeline data={[point]} />))
    await selectTransactions()
    const inputs = Array.from(container.querySelectorAll<HTMLInputElement>('[aria-label="Transaction selection"] input'))
    for (const input of inputs.slice(0, 12)) await act(async () => input.click())
    expect(inputs[12].disabled).toBe(true)
    expect(inputs[0].disabled).toBe(false)
    await act(async () => inputs[0].click())
    expect(inputs[12].disabled).toBe(false)
  })

  it('uses transaction-specific percentile availability instead of the whole-run percentile', async () => {
    const point = samplePoint('00', 100, 0)
    point[transactionChartSeriesKey('Search', 'p99')] = 140
    await act(async () => root.render(<SynchronizedTimeline data={[point]} />))
    await selectTransactions()
    const choices = container.querySelector('[aria-label="Transaction selection"]')!
    await act(async () => choices.querySelector<HTMLInputElement>('input')!.click()) // Checkout has no P99.
    await act(async () => button('Graphs').click())
    expect(checkbox('P99 Response Time').disabled).toBe(true)
    await act(async () => button('Transactions').click())
    const searchChoice = Array.from(container.querySelectorAll<HTMLLabelElement>('[aria-label="Transaction selection"] label')).find(label => label.textContent === 'Search')!
    await act(async () => searchChoice.querySelector<HTMLInputElement>('input')!.click())
    await act(async () => button('Graphs').click())
    expect(checkbox('P99 Response Time').disabled).toBe(false)
    await act(async () => checkbox('P99 Response Time').click())
    const lines = Array.from(container.querySelectorAll('[data-lane="p99"] [data-testid="mock-line"]'))
    expect(lines.map(line => line.getAttribute('data-key'))).toEqual([transactionChartSeriesKey('Search', 'p99')])
  })

  it('explains unavailable transaction evidence and resource metrics without inventing values', async () => {
    const point = { time: '00', vusers: 0, tps: 0, avgMs: 0, errorPct: 0, p50: 0, p90: 0, p99: 0, cpuProcess: 0, memProcess: Number.NaN }
    await act(async () => root.render(<SynchronizedTimeline data={[point]} />))
    await act(async () => button('Transactions').click())
    expect(container.textContent).toContain('no recorded transaction-level timeline data')
    expect(container.querySelector<HTMLOptionElement>('option[value="selected"]')?.disabled).toBe(true)
    await act(async () => button('Graphs').click())
    expect(checkbox('CPU Usage').disabled).toBe(false)
    expect(checkbox('Memory').disabled).toBe(true)
    await act(async () => checkbox('CPU Usage').click())
    expect(container.querySelector('[data-key="cpuProcess"]')).not.toBeNull()
    expect(container.querySelector('[data-key="cpuFree"]')).toBeNull()
    for (const input of Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]:checked'))) await act(async () => input.click())
    expect(container.textContent).toContain('No graphs selected.')
  })
})
