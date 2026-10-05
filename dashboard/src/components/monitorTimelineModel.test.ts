import { expect, it } from 'vitest'
import { buildMonitorTimelineRows, monitorSeriesIdentity, providerBucketRange, monitorEntityLabel, latestMonitorQueryAggregate } from './monitorTimelineModel'
import type { AnalysisPoint } from '../types/analysis'
import type { MetricsHistoryPoint } from '../types'

const point = (minute: number, value: number): AnalysisPoint => ({ source: 'source', metric_name: 'response_time', value, unit: 'ms', quality: 'valid', timestamp_utc: new Date(minute * 60000).toISOString(), provider: { name: 'appdynamics', metric_id: '9', bucket_start: new Date(minute * 60000).toISOString(), bucket_end: new Date((minute + 1) * 60000).toISOString(), resolution_seconds: 60, statistic: 'value', settled: true, aggregation_eligible: true } })
it('preserves explicit zero and inserts gaps for missing source buckets without interpolating', () => {
  expect(buildMonitorTimelineRows([point(3, 9), point(0, 0)], [], 'latency')).toEqual([{ time: 0, monitor: 0 }, { time: 60000, monitor: null }, { time: 180000, monitor: 9 }])
  expect(buildMonitorTimelineRows([point(0, 0), point(1, 9)], [], 'latency')).toHaveLength(2)
})
it('selects native load units and excludes observations outside the selected time window', () => {
  const history: MetricsHistoryPoint[] = [{ time: '0', timestampMs: 0, vusers: 25, tps: 10, avgMs: 100, errorPct: 2, p50: 80, p90: 90, p99: 120 }]
  expect(buildMonitorTimelineRows([], history, 'tps')).toEqual([{ time: 0, load: 10 }])
  expect(buildMonitorTimelineRows([], history, 'vusers')[0].load).toBe(25)
  expect(buildMonitorTimelineRows([], history, 'errors')[0].load).toBe(2)
  expect(buildMonitorTimelineRows([], history, 'latency', { from: new Date(1).toISOString(), to: new Date(60).toISOString() })).toEqual([])
})
it('labels partial boundaries and keeps statistic, entity and resolution identities distinct', () => {
  const first = point(0, 0)
  expect(providerBucketRange(first, new Date(1).toISOString())).toContain('partially overlaps')
  expect(monitorSeriesIdentity(first)).not.toBe(monitorSeriesIdentity({ ...first, provider: { ...first.provider!, statistic: 'sum' } }))
  expect(monitorSeriesIdentity({ ...first, provider: { ...first.provider!, entity_ids: { node: '1' } } })).not.toBe(monitorSeriesIdentity({ ...first, provider: { ...first.provider!, entity_ids: { node: '2' } } }))
  expect(monitorSeriesIdentity({ ...first, dimensions: { node: '1', tier: '2' } })).toBe(monitorSeriesIdentity({ ...first, dimensions: { tier: '2', node: '1' } }))
})

it('uses retained entity names only for the matching application and provider identity', () => {
  const metric = { ...point(0, 0), provider: { ...point(0, 0).provider!, entity_ids: { application: '123', node: '5' } } }
  const label = monitorEntityLabel(metric, [{ id: '5', type: 'node', name: 'Other application node', entity_ids: { application: '999' } }, { id: '5', type: 'node', name: 'Checkout node', entity_ids: { application: '123' } }])
  expect(label).toBe('application: 123, node: Checkout node (5)')
})

it('selects the most recent complete query aggregate without treating changing window sizes as separate series', () => {
  const first = { ...point(0, 0), provider: { ...point(0, 0).provider!, query_window: true } }
  const newest = { ...first, value: 100, provider: { ...first.provider, bucket_end: new Date(180000).toISOString(), resolution_seconds: 180 } }
  expect(latestMonitorQueryAggregate([newest, first])).toEqual(newest)
  expect(monitorSeriesIdentity(first)).toBe(monitorSeriesIdentity(newest))
  expect(monitorSeriesIdentity(point(0, 0))).not.toBe(monitorSeriesIdentity(first))
})

it('keeps Prometheus query identity and evaluation gaps separate from display labels', () => {
  const first: AnalysisPoint = { ...point(0, 0), dimensions: { job: 'checkout' }, provider: { ...point(0, 0).provider!, name: 'prometheus', mode: 'evaluation_point', query_id: 'cpu', definition_hash: 'hash', backend_profile: 'prometheus', evaluation_step_seconds: 15, entity_ids: { series: 'opaque-id' } } }
  const last = { ...first, timestamp_utc: new Date(45000).toISOString(), value: 42 }
  expect(buildMonitorTimelineRows([first, last], [], 'tps')).toEqual([{ time: 0, monitor: 0 }, { time: 15000, monitor: null }, { time: 45000, monitor: 42 }])
  expect(monitorEntityLabel(first)).toBe('job: checkout')
  expect(monitorSeriesIdentity(first)).not.toBe(monitorSeriesIdentity({ ...first, provider: { ...first.provider!, definition_hash: 'changed' } }))
  expect(monitorSeriesIdentity(first)).not.toBe(monitorSeriesIdentity({ ...first, provider: { ...first.provider!, evaluation_step_seconds: 30 } }))
  const azure = { ...first, dimensions: { job: 'checkoutapi' }, provider: { ...first.provider!, backend_profile: 'azure_managed', display_labels: { Job: 'CheckoutAPI' } } }
  expect(monitorEntityLabel(azure)).toBe('Job: CheckoutAPI')
  expect(monitorSeriesIdentity(azure)).toBe(monitorSeriesIdentity({ ...azure, provider: { ...azure.provider, display_labels: { JOB: 'CHECKOUTAPI' } } }))
})

it('keeps native provider names and Dynatrace backend identities separate from Prometheus definitions', () => {
  const appdynamics = point(0, 0)
  const dynatrace: AnalysisPoint = { ...appdynamics, provider: { ...appdynamics.provider!, name: 'dynatrace', backend: 'classic' } }
  expect(monitorSeriesIdentity(appdynamics)).not.toBe(monitorSeriesIdentity(dynatrace))
  expect(monitorSeriesIdentity(dynatrace)).not.toBe(monitorSeriesIdentity({ ...dynatrace, provider: { ...dynatrace.provider!, backend: 'grail' } }))
  expect(monitorSeriesIdentity(dynatrace)).not.toBe(monitorSeriesIdentity({ ...dynatrace, provider: { ...dynatrace.provider!, resolution_seconds: 120 } }))
})
