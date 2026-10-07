import { describe, expect, it } from 'vitest'
import type { AnalysisMeasure } from '../types/analysis'
import { analysisBucketSeconds, analysisHistogramDeclaration, analysisHistogramMeasureError, analysisSummaryDeclarations, analysisSummaryMeasureError, parseAnalysisHistogramBounds, withAnalysisMeasureKind, analysisWindowAlignedRange, analysisWindowAnchor, analysisCommonWindow, analysisCommonWindowAnchor, analysisMetricNames, analysisMetricAggregations } from './analysisSummary'

const statistics = ['avg', 'count', 'p95']
const summary: AnalysisMeasure = { value: ['v'], name: 'latency', kind: 'summary', unit: 'ms', statistic: 'p95', window_seconds: 60 }

describe('analysis summary helpers', () => {
  it('validates declarations only when the controller supports them', () => {
    expect(analysisSummaryMeasureError(summary, statistics)).toBeUndefined()
    expect(analysisSummaryMeasureError({ ...summary, statistic: undefined }, statistics)).toContain('statistic')
    expect(analysisSummaryMeasureError({ ...summary, window_seconds: 0 }, statistics)).toContain('window')
    expect(analysisSummaryMeasureError({ ...summary, sample_sum_column: ['s'] }, statistics)).toContain('sample count')
    expect(analysisSummaryMeasureError({ ...summary, statistic: 'count' }, statistics)).toContain('count unit')
    expect(analysisSummaryMeasureError({ ...summary, statistic: undefined }, undefined)).toBeUndefined()
  })

  it('never guesses a summary statistic or window and strips them otherwise', () => {
    const fresh = withAnalysisMeasureKind({ value: ['v'], name: 'x', kind: 'gauge', unit: 'ms' }, 'summary', { summaryStatistics: statistics })
    expect(fresh).toMatchObject({ kind: 'summary' })
    expect(fresh).not.toHaveProperty('statistic')
    expect(fresh).not.toHaveProperty('window_seconds')
    expect(analysisSummaryMeasureError(fresh, statistics)).toContain('choose the statistic')
    expect(withAnalysisMeasureKind(summary, 'summary', { summaryStatistics: statistics })).toMatchObject({ statistic: summary.statistic, window_seconds: summary.window_seconds })
    const gauge = withAnalysisMeasureKind({ ...summary, sample_count_column: ['n'] }, 'gauge', { summaryStatistics: statistics })
    expect(gauge).toEqual({ value: ['v'], name: 'latency', kind: 'gauge', unit: 'ms' })
    expect(withAnalysisMeasureKind(summary, 'summary')).toEqual({ value: ['v'], name: 'latency', kind: 'summary', unit: 'ms' })
    const histogram = withAnalysisMeasureKind(summary, 'histogram', { histogramBoundLimit: 128 })
    expect(histogram).toMatchObject({ kind: 'histogram', histogram: { bounds: ['+Inf'], temporality: 'delta', window_seconds: 60 } })
    expect(histogram).not.toHaveProperty('statistic')
    expect(withAnalysisMeasureKind(histogram, 'gauge', { histogramBoundLimit: 128 })).not.toHaveProperty('histogram')
  })

  it('finds fixed-name declarations and widens buckets to whole windows', () => {
    const mapping = { version: 1, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [summary, { ...summary, value: ['w'], statistic: 'avg' }, { ...summary, name: '', name_column: ['metric'] }] }
    expect(analysisSummaryDeclarations(mapping, 'latency')).toEqual([{ statistic: 'p95', window_seconds: 60 }, { statistic: 'avg', window_seconds: 60 }])
    expect(analysisBucketSeconds(3600)).toBe(8)
    expect(analysisBucketSeconds(3600, 60)).toBe(60)
    expect(analysisBucketSeconds(86400, 60)).toBe(180)
    expect(analysisBucketSeconds(10, 300)).toBe(300)
  })

  it('validates histogram declarations', () => {
    const histogram: AnalysisMeasure = { value: ['inf'], name: 'latency', kind: 'histogram', unit: 'ms', histogram: { bounds: ['10', '100', '+Inf'], bucket_counts: 'per_bin', temporality: 'delta', window_seconds: 60, count_columns: [['a'], ['b']] } }
    expect(analysisHistogramMeasureError(histogram, 128)).toBeUndefined()
    expect(analysisHistogramMeasureError(histogram, undefined)).toBeUndefined()
    const change = (values: Partial<NonNullable<AnalysisMeasure['histogram']>>) => ({ ...histogram, histogram: { ...histogram.histogram!, ...values } })
    expect(analysisHistogramMeasureError(change({ bounds: ['100', '10', '+Inf'] }), 128)).toContain('increasing')
    expect(analysisHistogramMeasureError(change({ bounds: ['+Inf', '10', '100'] }), 128)).toContain('increasing')
    expect(analysisHistogramMeasureError(change({ bounds: ['0x10', '100', '+Inf'] }), 128)).toContain('increasing')
    expect(analysisHistogramMeasureError(change({ window_seconds: undefined }), 128)).toContain('window')
    expect(analysisHistogramMeasureError(change({ count_columns: [['a'], []] }), 128)).toContain('count column')
    expect(analysisHistogramMeasureError(change({ count_columns: undefined, bound_column: ['le'] }), 128)).toBeUndefined()
    expect(analysisHistogramMeasureError(change({ bounds: ['1', '2', '3'] }), 2)).toContain('1-2')
    expect(parseAnalysisHistogramBounds(' 0.1, 1 ,, +Inf ')).toEqual(['0.1', '1', '+Inf'])
    const mapping = { version: 1, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [histogram] }
    expect(analysisHistogramDeclaration(mapping, 'latency')?.window_seconds).toBe(60)
    expect(analysisHistogramDeclaration(mapping, 'other')).toBeUndefined()
  })

  it('snaps ranges to the source window grid and lists each metric name once', () => {
    // The grid passes through the dataset's own window starts, not the epoch.
    const anchor = Date.parse('2026-01-01T09:02:00Z')
    expect(analysisWindowAlignedRange(Date.parse('2026-01-01T10:01:00Z'), Date.parse('2026-01-01T11:01:00Z'), 300, anchor)).toEqual({ fromMs: Date.parse('2026-01-01T09:57:00Z'), toMs: Date.parse('2026-01-01T11:02:00Z') })
    // Without a known grid the range is the user's.
    expect(analysisWindowAlignedRange(Date.parse('2026-01-01T10:01:00Z'), Date.parse('2026-01-01T11:01:00Z'), 300)).toEqual({ fromMs: Date.parse('2026-01-01T10:01:00Z'), toMs: Date.parse('2026-01-01T11:01:00Z') })
    expect(analysisWindowAlignedRange(5, 7)).toEqual({ fromMs: 5, toMs: 7 })
    const mapping = { version: 1, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [summary, { ...summary, value: ['w'], statistic: 'avg' }, { ...summary, value: ['x'], statistic: 'p50' }] }
    expect(analysisMetricNames(mapping)).toEqual(['latency'])
    expect(analysisMetricAggregations(mapping, 'latency').options).toEqual([summary.statistic, 'avg', 'p50'])
    const named = { ...mapping, measures: [{ value: ['c'], name: '', name_column: ['metric'], kind: 'histogram', unit: 'ms', histogram: { bounds: ['1', '+Inf'], bucket_counts: 'per_bin', temporality: 'delta', window_seconds: 30, bound_column: ['le'] } }] }
    const aggregations = analysisMetricAggregations(named, 'anything')
    expect(aggregations.options).toEqual(expect.arrayContaining(['p75', 'p90', 'p999', 'avg']))
    expect(aggregations.windowSeconds?.('p90')).toBe(30)
    expect(aggregations.windowSeconds?.('avg')).toBeUndefined()
    // A fixed-name metric beside name-column histograms keeps its own aggregations.
    const mixed = { ...named, measures: [...named.measures, { value: ['rt'], name: 'response', kind: 'observation', unit: 'ms' }] }
    expect(analysisMetricAggregations(mixed, 'response').options).not.toContain('p999')
    // Name-column summaries answer only their declared statistics, over their declared window.
    const summaryRow = { value: ['v'], name: '', name_column: ['metric'], kind: 'summary', unit: 'ms', statistic: 'p90', window_seconds: 60 }
    const namedSummaries = { ...mapping, measures: [summaryRow, { ...summaryRow, value: ['a'], statistic: 'avg' }, { ...summaryRow, value: ['b'], statistic: 'avg', window_seconds: 300 }] }
    const summaryAggregations = analysisMetricAggregations(namedSummaries, 'anything')
    expect(summaryAggregations.options).toEqual(['p90', 'avg'])
    expect(summaryAggregations.windowSeconds?.('p90')).toBe(60)
    // Two declared windows for one statistic leave the window unknown.
    expect(summaryAggregations.windowSeconds?.('avg')).toBeUndefined()
  })

  it('finds each metric\'s window grid from its partitions and a grid both sides share', () => {
    const at = (iso: string) => ({ stats: { kind: 'summary', metric: 'latency', min_time: iso } })
    const dataset = { id: 'd', project_id: 'p', name: 'd', revision: 1, state: 'ready', files: [], accepted: 1, rejected: 0, partition_manifest: { partitions: [at('2026-01-01T09:02:00Z'), at('2026-01-01T09:12:00Z')] } }
    expect(analysisWindowAnchor(dataset, 'latency', 300)).toBe(Date.parse('2026-01-01T09:02:00Z'))
    // Partitions on different grids leave the grid unknown.
    const skewed = { ...dataset, partition_manifest: { partitions: [at('2026-01-01T09:02:00Z'), at('2026-01-01T09:13:00Z')] } }
    expect(analysisWindowAnchor(skewed, 'latency', 300)).toBeUndefined()
    expect(analysisWindowAnchor(dataset, 'other', 300)).toBeUndefined()
    // Windows of 120 and 300 seconds share 600-second buckets where both grids start.
    expect(analysisCommonWindow([120, 300])).toBe(600)
    expect(analysisCommonWindow([86400, 7])).toBeUndefined()
    const left = Date.parse('2026-01-01T00:00:00Z')
    const right = Date.parse('2026-01-01T00:01:00Z')
    const shared = analysisCommonWindowAnchor([{ windowSeconds: 120, anchorMs: left }, { windowSeconds: 300, anchorMs: right }], 600)
    expect(shared).toBeDefined()
    expect((shared! - left) % 120000).toBe(0)
    expect((((shared! - right) % 300000) + 300000) % 300000).toBe(0)
    expect(analysisCommonWindowAnchor([{ windowSeconds: 120, anchorMs: left }, { windowSeconds: 300 }], 600)).toBeUndefined()
    // Two 60-second grids half a minute apart never share a start.
    expect(analysisCommonWindowAnchor([{ windowSeconds: 60, anchorMs: left }, { windowSeconds: 60, anchorMs: left + 30000 }], 60)).toBeUndefined()
  })

  it('reads the grid of the selected series', () => {
    const series = (host: string, iso: string) => ({ stats: { kind: 'summary', metric: 'latency', min_time: iso, identity: { source: 's', host, service: '', dimensions: {} } } })
    const dataset = { id: 'd', project_id: 'p', name: 'd', revision: 1, state: 'ready', files: [], accepted: 1, rejected: 0, partition_manifest: { partitions: [series('a', '2026-01-01T00:00:00Z'), series('b', '2026-01-01T00:00:30Z')] } }
    // Together the hosts disagree; each on its own has a grid.
    expect(analysisWindowAnchor(dataset, 'latency', 60)).toBeUndefined()
    expect(analysisWindowAnchor(dataset, 'latency', 60, { host: 'b' })).toBe(Date.parse('2026-01-01T00:00:30Z'))
    // A partition mixing series cannot tell one series' grid.
    const mixed = { ...dataset, partition_manifest: { partitions: [{ stats: { kind: 'summary', metric: 'latency', min_time: '2026-01-01T00:00:00Z' } }] } }
    expect(analysisWindowAnchor(mixed, 'latency', 60)).toBe(Date.parse('2026-01-01T00:00:00Z'))
    expect(analysisWindowAnchor(mixed, 'latency', 60, { host: 'b' })).toBeUndefined()
  })
})
