import { describe, expect, it } from 'vitest'
import { elasticScalarValue, elasticSourceReady, emptyElasticSelection, monitorSourceMetrics, safeElasticDashboardsLink } from './elasticMonitoringModel'
import { elkCanEnable, elkMissing, legacyELKCaptureReady } from './captureConnectionModel'
import type { ELKConnectionInput } from '../hooks/api/useELKConnectionsApi'
import type { ELKConnection } from '../types/monitoring'

const config = { product: 'elasticsearch' as const, endpoint: 'https://elastic.example.com', auth_type: 'api_key' as const }
const draft: ELKConnectionInput = { name: 'Elastic', description: '', enabled: false, endpoint: config.endpoint, index_pattern: '', timestamp_field: '', level_field: '', message_field: '', test_id_field: '', trace_id_field: '', base_filter: '', error_filter: '', auth_type: config.auth_type, product: config.product, config, metric_aggregations: [], collection_mode: 'direct' }
const saved = { ...draft, id: 'connection', project_id: 'p', status: 'ready', revision: 2, verified_revision: 2, has_api_key: true, has_basic_username: false, has_basic_password: false, created_at: '', updated_at: '', kibana_auth_type: undefined, logstash_auth_type: undefined } as ELKConnection

describe('Elastic source semantics', () => {
  it('uses the approved defaults and requires an assigned collector when selected', () => {
    const selection = emptyElasticSelection('p')
    expect(selection).toMatchObject({ settle_seconds: 60, overlap_seconds: 120, page_size: 100, duration_unit: 'ns' })
    const source = { id: '', name: 'Logs', revision: 0, pack: 'elastic', collector_id: '', interval_seconds: 10, enabled: true, connection_id: 'connection', elastic: { ...selection, indices: ['logs-*'] } }
    expect(elasticSourceReady(source)).toBe(true)
    expect(elasticSourceReady({ ...source, executor_mode: 'collector' })).toBe(false)
    expect(elasticSourceReady({ ...source, executor_mode: 'collector', collector_id: 'agent' })).toBe(true)
    expect(elasticSourceReady({ ...source, elastic: { ...selection, indices: [], logs: false, metrics: [] } })).toBe(false)
    expect(elasticSourceReady({ ...source, elastic: { ...selection, indices: [], logs: false, cluster_health: true } })).toBe(true)
    expect(elasticSourceReady({ ...source, elastic: { ...selection, indices: [], logs: true, cluster_health: true } })).toBe(false)
  })
  it('retains exact-window and source-statistic mappings for terminal rule selectors', () => {
    expect(monitorSourceMetrics({ id: 's', name: 'Metrics', revision: 1, collector_id: '', pack: 'elastic', interval_seconds: 10, enabled: true, elastic: { indices: ['metrics-*'], metrics: [{ name: 'count', statistic: 'count', bucket_seconds: 10 }], queries: [{ name: 'p99', kind: 'metric', language: 'esql', statistic: 'p99', unit: 'ms', bucket_seconds: 10, exact_window: true }] } })).toEqual([{ name: 'count', unit: 'count', statistic: 'count', bucket_seconds: 10, exact_window: undefined, final_window_supported: true }, { name: 'p99', unit: 'ms', statistic: 'p99', bucket_seconds: 10, exact_window: true, final_window_supported: true }])
  })
  it('distinguishes null, false, zero, and missing numeric projections', () => {
    expect(elasticScalarValue({ kind: 'null' })).toBe('null')
    expect(elasticScalarValue({ kind: 'boolean', bool_value: false })).toBe('false')
    expect(elasticScalarValue({ kind: 'number', number_value: 0 })).toBe('0')
    expect(elasticScalarValue({ kind: 'number' })).toBe('Unavailable')
  })
})

describe('saved connection verification', () => {
  it('does not require legacy document defaults for expanded connections', () => {
    expect(elkMissing(draft, saved)).toEqual([])
    expect(elkCanEnable(draft, saved)).toBe(true)
    expect(elkCanEnable(draft, { ...saved, verified_revision: 1 })).toBe(false)
  })
  it('requires replacement credentials and fresh verification after identity changes', () => {
    const moved = { ...draft, endpoint: 'https://other.example.com', config: { ...config, endpoint: 'https://other.example.com' } }
    expect(elkMissing(moved, saved)).toContain('API key secret reference')
    expect(elkCanEnable(moved, saved)).toBe(false)
    expect(elkCanEnable({ ...draft, api_key_secret_name: 'replacement' }, saved)).toBe(false)
    expect(elkCanEnable({ ...draft, config: { ...config, approved_cidrs: ['10.0.0.0/8'] } }, saved)).toBe(false)
  })
})

it('offers legacy capture only for connections with compatible document defaults', () => {
  expect(legacyELKCaptureReady({ ...saved, enabled: true, legacy_capture_ready: false })).toBe(false)
  expect(legacyELKCaptureReady({ ...saved, enabled: true, legacy_capture_ready: true })).toBe(true)
  expect(legacyELKCaptureReady({ ...saved, enabled: true, index_pattern: 'logs-*', timestamp_field: '@timestamp', message_field: 'message' })).toBe(true)
  expect(legacyELKCaptureReady({ ...saved, enabled: true })).toBe(false)
})

it('limits configured dashboard navigation to credential-free HTTPS without query or fragments', () => {
  expect(safeElasticDashboardsLink('https://dashboards.example.com/base')).toBe('https://dashboards.example.com/base')
  for (const value of ['javascript:alert(1)', 'http://dashboards.example.com', 'https://name:password@dashboards.example.com', 'https://dashboards.example.com?token=secret', 'https://dashboards.example.com/#/logs']) expect(safeElasticDashboardsLink(value)).toBeUndefined()
})
