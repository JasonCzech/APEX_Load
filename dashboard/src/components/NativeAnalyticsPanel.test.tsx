// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { nativePanelRows, nativePopulationMetrics } from './nativeAnalyticsModel'
import type { DashboardDefinitionPanel } from '../hooks/api/useDashboardDefinitionsApi'
import type { MetricsHistoryPoint } from '../types'
const panel: DashboardDefinitionPanel = { id: 'p', title: 'Latency', kind: 'line', x: 0, y: 0, w: 24, h: 8, query: { metric: 'response_time', aggregation: 'p95' } }
const history = [{ time: 'first', p95: 10 }, { time: 'gap', p95: 0, isGapBoundary: 1 }, { time: 'last', p95: 50 }] as MetricsHistoryPoint[]
describe('native panel evidence', () => {
  it('retains source interval percentiles and missing observations', () => {
    expect(nativePanelRows(panel, history)).toEqual([{ time: 'first', value: 10 }, { time: 'gap', value: undefined }, { time: 'last', value: 50 }])
  })
  it.each([{ filters: { region: 'east' } }, { dimensions: ['region'] }, { aggregation: 'sum' }, { bucket_seconds: 30 }, { overlay: true }])('rejects unsupported saved query %j instead of showing totals', query => {
    expect(nativePanelRows({ ...panel, query: { ...panel.query!, ...query } }, history)).toBeNull()
  })
  it('preserves true zero and unavailable measurements', () => {
    expect(nativePanelRows(panel, [{ time: 'zero', p95: 0 }, { time: 'missing' }] as MetricsHistoryPoint[])).toEqual([{ time: 'zero', value: 0 }, { time: 'missing', value: undefined }])
  })
  it('retains previously saved metric aliases', () => {
    expect(nativePanelRows({ ...panel, query: { metric: 'transaction_duration', aggregation: 'p95' } }, history)).toEqual(nativePanelRows(panel, history))
    expect(nativePanelRows({ ...panel, query: { metric: 'throughput', aggregation: 'avg' } }, [{ time: 'first', tps: 2 }] as MetricsHistoryPoint[])).toEqual([{ time: 'first', value: 2 }])
  })

})

it('limits derived rates to retained population evidence and preserves their unit', () => {
 for (const metric of ['observation_rate', 'error_observation_rate', 'success_observation_rate']) {
 expect(nativePopulationMetrics[metric].aggregations).toEqual(['rate'])
 expect(nativePopulationMetrics[metric].unit).toBe('observations/s')
 expect(nativePanelRows({ ...panel, query: { metric, aggregation: 'rate' } }, history)).toBeNull()
 }
})
