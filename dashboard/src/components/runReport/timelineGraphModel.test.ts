import { describe, expect, it } from 'vitest'
import type { MetricsHistoryPoint } from '../../types'
import { transactionChartSeriesKey } from '../transactionChartSeries'
import { DEFAULT_TIMELINE_GRAPHS, graphHasData, graphSupportsTransactions, TIMELINE_GRAPHS, timelineMetricKeys, timelineSeriesSegments } from './timelineGraphModel'

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

describe('timelineSeriesSegments', () => {
  const at = (second: number, tps: number, cpu?: number) => ({ time: String(second), timestampMs: second * 1000, tps, cpuProcess: cpu } as unknown as MetricsHistoryPoint)

  it('keeps one key across samples of another kind between a series\' own', () => {
    // Throughput every second, a resource-only sample (no throughput) between.
    const data = [at(0, 5, 1), at(0.5, Number.NaN, 2), at(1, 6, 3), at(1.5, Number.NaN, 4), at(2, 7, 5)]
    const result = timelineSeriesSegments(data, ['tps', 'cpuProcess'])
    expect(result.segments.get('tps')).toEqual(['tps'])
    expect(result.segments.get('cpuProcess')).toEqual(['cpuProcess'])
    expect(result.rows).toBe(data)
  })

  it('splits a series where its own samples stop for longer than a few intervals', () => {
    // CPU stops reporting from 3s to 10s while throughput carries on.
    const data = [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11].map(second => at(second, 5, second <= 3 || second >= 10 ? 50 : undefined))
    const result = timelineSeriesSegments(data, ['tps', 'cpuProcess'])
    expect(result.segments.get('tps')).toEqual(['tps'])
    const keys = result.segments.get('cpuProcess')!
    expect(keys).toHaveLength(2)
    const values = (key: string) => result.rows.map(row => (row as unknown as Record<string, unknown>)[key])
    expect(values(keys[0])).toEqual([50, 50, 50, 50, undefined, undefined, undefined, undefined, undefined, undefined, undefined, undefined])
    expect(values(keys[1])).toEqual([undefined, undefined, undefined, undefined, undefined, undefined, undefined, undefined, undefined, undefined, 50, 50])
    expect(result.points.size).toBe(0)
    // Tooltips and exports keep the series' own key.
    expect(values('cpuProcess')[0]).toBe(50)
  })

  it('marks a lone sample between two gaps as a point to draw', () => {
    const data = [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14].map(second => at(second, 5, second <= 3 || second === 8 || second >= 13 ? 50 : undefined))
    const result = timelineSeriesSegments(data, ['cpuProcess'])
    const keys = result.segments.get('cpuProcess')!
    expect(keys).toHaveLength(3)
    expect([...result.points]).toEqual([keys[1]])
  })
})
