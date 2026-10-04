import { expect, it } from 'vitest'
import { dynatraceCanEnable } from './dynatraceConnectionModel'
import { dynatraceMissing } from './captureConnectionModel'
import type { DynatraceConnectionInput } from '../hooks/api/useDynatraceConnectionsApi'
import type { DynatraceConnection } from '../types/monitoring'
const input: DynatraceConnectionInput = { name: 'SaaS', description: '', enabled: false, environment_url: 'https://tenant.live.dynatrace.com', platform_url: '', metrics_enabled: false, problems_enabled: true, logs_enabled: false, platform_auth_type: '', oauth_resource: '', oauth_scopes: [], metric_queries: [], problem_selector: '', problem_entity_selector: '', log_base_filter: '', log_test_id_field: '', log_timestamp_field: 'timestamp', log_status_field: 'status', log_message_field: 'content', log_trace_id_field: 'trace_id', poll_interval_seconds: 60, collection_mode: 'direct', requests_per_minute: 60 }
const saved: DynatraceConnection = { ...input, platform_auth_type: undefined, id: 'connection', project_id: 'project', status: 'disabled', revision: 2, verified_revision: 2, has_environment_token: true, has_platform_token: false, has_oauth_client_id: false, has_oauth_client_secret: false, created_at: '', updated_at: '' }
it('requires the saved verified revision and invalidates identity, execution and budget changes', () => {
  expect(dynatraceCanEnable(input, saved)).toBe(true)
  expect(dynatraceCanEnable(input, { ...saved, verified_revision: 1 })).toBe(false)
  for (const change of [{ environment_url: 'https://other.live.dynatrace.com' }, { collection_mode: 'collector' as const, collector_agent_id: 'collector' }, { requests_per_minute: 30 }, { environment_token_secret: 'replacement' }, { events_enabled: true }]) expect(dynatraceCanEnable({ ...input, ...change }, saved)).toBe(false)
  expect(dynatraceCanEnable({ ...input, name: 'New name' }, saved)).toBe(true)
  expect(dynatraceCanEnable({ ...input, grail_events_enabled: false, topology_mode: '', collector_agent_id: '', platform_requests_per_minute: 60, max_scan_bytes: 1000000000 }, saved)).toBe(true)
})
it('does not retain secret eligibility when identity changes and requires collector assignment', () => {
  expect(dynatraceMissing(input, saved)).toEqual([])
  expect(dynatraceMissing({ ...input, environment_url: 'https://other.live.dynatrace.com' }, saved)).toContain('Environment token secret reference')
  expect(dynatraceMissing({ ...input, collection_mode: 'collector' }, saved)).toContain('Assigned collector')
  expect(dynatraceMissing({ ...input, problems_enabled: false, spans_enabled: true })).toContain('Platform URL')
})
it('requires only Platform authentication for a Grail event capability', () => {
  const missing = dynatraceMissing({ ...input, problems_enabled: false, environment_url: '', grail_events_enabled: true })
  expect(missing).toContain('Platform URL')
  expect(missing).not.toContain('Environment URL')
  expect(missing).not.toContain('At least one capture capability')
  expect(dynatraceCanEnable({ ...input, grail_events_enabled: true }, saved)).toBe(false)
})
it('preserves a saved Platform token when the API omits unused OAuth scopes', () => {
  const draft: DynatraceConnectionInput = { ...input, problems_enabled: false, environment_url: '', logs_enabled: true, platform_url: 'https://tenant.apps.dynatrace.com', platform_auth_type: 'platform_token' }
  const apiSaved = JSON.parse(JSON.stringify({ ...saved, ...draft, has_platform_token: true, oauth_scopes: null }))
  expect(dynatraceMissing(draft, apiSaved)).toEqual([])
  expect(dynatraceCanEnable(draft, apiSaved)).toBe(true)
  expect(dynatraceMissing({ ...draft, platform_url: 'https://other.apps.dynatrace.com' }, apiSaved)).toContain('Platform token secret reference')
})
