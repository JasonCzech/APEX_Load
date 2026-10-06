import { describe, expect, it } from 'vitest'
import type { AnalysisMeasure } from '../types/analysis'
import { analysisBucketSeconds, analysisSummaryDeclarations, analysisSummaryMeasureError, withAnalysisMeasureKind } from './analysisSummary'

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
    expect(withAnalysisMeasureKind({ value: ['v'], name: 'x', kind: 'gauge', unit: 'ms' }, 'summary', statistics)).toMatchObject({ kind: 'summary', statistic: 'avg', window_seconds: 60 })
    const gauge = withAnalysisMeasureKind({ ...summary, sample_count_column: ['n'] }, 'gauge', statistics)
    expect(gauge).toEqual({ value: ['v'], name: 'latency', kind: 'gauge', unit: 'ms' })
    expect(withAnalysisMeasureKind(summary, 'summary', undefined)).toEqual({ value: ['v'], name: 'latency', kind: 'summary', unit: 'ms' })
  })

  it('finds fixed-name declarations and widens buckets to whole windows', () => {
    const mapping = { version: 1, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [summary, { ...summary, value: ['w'], statistic: 'avg' }, { ...summary, name: '', name_column: ['metric'] }] }
    expect(analysisSummaryDeclarations(mapping, 'latency')).toEqual([{ statistic: 'p95', window_seconds: 60 }, { statistic: 'avg', window_seconds: 60 }])
    expect(analysisBucketSeconds(3600)).toBe(8)
    expect(analysisBucketSeconds(3600, 60)).toBe(60)
    expect(analysisBucketSeconds(86400, 60)).toBe(180)
    expect(analysisBucketSeconds(10, 300)).toBe(300)
  })
})
