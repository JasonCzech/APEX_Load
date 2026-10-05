import { describe, expect, it } from 'vitest'
import { dynatraceMissing, elkMissing } from './captureConnectionModel'
import type { ELKConnectionInput } from '../hooks/api/useELKConnectionsApi'
import type { DynatraceConnectionInput } from '../hooks/api/useDynatraceConnectionsApi'

const elastic: ELKConnectionInput = { name: 'Logs', description: '', enabled: false, endpoint: 'https://elastic.example.com', index_pattern: 'logs-*', timestamp_field: '@timestamp', message_field: 'message', level_field: '', test_id_field: '', trace_id_field: '', base_filter: '', error_filter: '', auth_type: 'api_key', api_key_secret_name: 'key-ref', metric_aggregations: [] }
const dynatrace: DynatraceConnectionInput = { name: 'Monitoring', description: '', enabled: false, environment_url: '', platform_url: 'https://example.apps.dynatrace.com', metrics_enabled: false, problems_enabled: false, logs_enabled: true, platform_auth_type: 'oauth_client', oauth_client_id_secret: 'client-ref', oauth_client_secret_secret: 'secret-ref', oauth_resource: 'resource', oauth_scopes: ['storage:logs:read'], metric_queries: [], problem_selector: '', problem_entity_selector: '', log_base_filter: '', log_test_id_field: '', log_timestamp_field: 'timestamp', log_status_field: 'status', log_message_field: 'content', log_trace_id_field: '', poll_interval_seconds: 15 }

describe('capture connection readiness guidance', () => {
  it('treats completeness independently of enablement', () => {
    expect(elkMissing(elastic)).toEqual([])
    expect(dynatraceMissing(dynatrace)).toEqual([])
  })
  it.each(['endpoint', 'index_pattern', 'timestamp_field', 'message_field', 'api_key_secret_name'] as const)('identifies missing ELK %s', key => {
    expect(elkMissing({ ...elastic, [key]: '' }).length).toBeGreaterThan(0)
  })
  it('requires both basic-auth references and complete aggregation rows', () => {
    expect(elkMissing({ ...elastic, auth_type: 'basic', api_key_secret_name: undefined })).toEqual(['Username secret reference', 'Password secret reference'])
    expect(elkMissing({ ...elastic, metric_aggregations: [{ name: 'duration', field: '', operation: 'avg' }] })).toContain('Complete or remove unfinished aggregations')
  })
  it.each(['platform_url', 'oauth_client_id_secret', 'oauth_client_secret_secret', 'oauth_resource'] as const)('identifies missing Dynatrace %s', key => {
    expect(dynatraceMissing({ ...dynatrace, [key]: '' }).length).toBeGreaterThan(0)
  })
  it('only requires configuration for selected capabilities', () => {
    expect(dynatraceMissing({ ...dynatrace, logs_enabled: false })).toEqual(['At least one capture capability'])
    expect(dynatraceMissing({ ...dynatrace, logs_enabled: false, problems_enabled: true })).toEqual(['Environment URL', 'Environment token secret reference'])
    expect(dynatraceMissing({ ...dynatrace, logs_enabled: false, metrics_enabled: true, metric_queries: [{ name: '', metric_selector: 'builtin:host.cpu.usage', aggregation: 'avg' }] })).toContain('Complete metric selector')
    expect(dynatraceMissing({ ...dynatrace, platform_auth_type: 'platform_token' })).toEqual(['Platform token secret reference'])
    expect(dynatraceMissing({ ...dynatrace, oauth_scopes: [] })).toEqual(['OAuth scopes'])
  })
})
