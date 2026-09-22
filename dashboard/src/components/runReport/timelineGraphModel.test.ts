import { describe, expect, it } from 'vitest'
import type { MetricsHistoryPoint } from '../../types'
import { transactionChartSeriesKey } from '../transactionChartSeries'
import { DEFAULT_TIMELINE_GRAPHS, graphHasData, graphSupportsTransactions, TIMELINE_GRAPHS, timelineMetricKeys } from './timelineGraphModel'

describe('timeline graph catalog', () => {
  it('has unique IDs and valid defaults', () => {
    expect(new Set(TIMELINE_GRAPHS.map(graph => graph.id)).size).toBe(TIMELINE_GRAPHS.length)
    expect(DEFAULT_TIMELINE_GRAPHS.every(id => TIMELINE_GRAPHS.some(graph => graph.id === id))).toBe(true)
  })

  it('recognizes observed zeroes but not missing, nonfinite, or nonnumeric metrics', () => {
    const keys = timelineMetricKeys([{ time: '00', cpuProcess: 0, memProcess: NaN, diskRead: Infinity, netSent: undefined } as MetricsHistoryPoint])
    expect([...keys]).toEqual(['cpuProcess'])
    expect(timelineMetricKeys([]).size).toBe(0)
  })

  it('checks selected transaction evidence independently of aggregates and unrelated transactions', () => {
    const graph = TIMELINE_GRAPHS.find(item => item.id === 'p99')!
    const keys = new Set(['p99', transactionChartSeriesKey('Checkout / ✓', 'p99')])
    expect(graphSupportsTransactions(graph)).toBe(true)
    expect(graphHasData(graph, keys)).toBe(true)
    expect(graphHasData(graph, keys, [])).toBe(false)
    expect(graphHasData(graph, keys, ['Search'])).toBe(false)
    expect(graphHasData(graph, keys, ['Checkout / ✓'])).toBe(true)
    const cpu = TIMELINE_GRAPHS.find(item => item.id === 'cpu')!
    expect(graphSupportsTransactions(cpu)).toBe(false)
    expect(graphHasData(cpu, new Set(['cpuProcess']), ['Search'])).toBe(true)
  })
})
