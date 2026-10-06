import { describe, expect, it } from 'vitest'
import type { AnalysisMeasure } from '../types/analysis'
import { analysisBucketSeconds, analysisHistogramDeclaration, analysisHistogramMeasureError, analysisSummaryDeclarations, analysisSummaryMeasureError, parseAnalysisHistogramBounds, withAnalysisMeasureKind } from './analysisSummary'

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

  it('adds editable defaults for summaries and strips them otherwise', () => {
    expect(withAnalysisMeasureKind({ value: ['v'], name: 'x', kind: 'gauge', unit: 'ms' }, 'summary', { summaryStatistics: statistics })).toMatchObject({ kind: 'summary', statistic: 'avg', window_seconds: 60 })
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
})
