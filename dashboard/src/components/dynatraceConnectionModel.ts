import type { DynatraceConnectionInput } from '../hooks/api/useDynatraceConnectionsApi'
import type { DynatraceConnection } from '../types/monitoring'

const verifiedFields = ['environment_url', 'platform_url', 'metrics_enabled', 'problems_enabled', 'logs_enabled', 'events_enabled', 'spans_enabled', 'grail_metrics_enabled', 'grail_events_enabled', 'topology_mode', 'platform_auth_type', 'oauth_resource', 'oauth_scopes', 'metric_queries', 'problem_selector', 'problem_entity_selector', 'log_base_filter', 'log_test_id_field', 'log_timestamp_field', 'log_status_field', 'log_message_field', 'log_trace_id_field', 'collection_mode', 'collector_agent_id', 'requests_per_minute', 'platform_requests_per_minute', 'max_scan_bytes', 'max_run_scan_bytes', 'drain_reserve_bytes', 'poll_interval_seconds'] as const
const verificationDefaults: Partial<Record<typeof verifiedFields[number], unknown>> = {
  metrics_enabled: false, problems_enabled: false, logs_enabled: false, events_enabled: false, spans_enabled: false, grail_metrics_enabled: false, grail_events_enabled: false,
  oauth_scopes: [], metric_queries: [], collection_mode: 'direct', requests_per_minute: 60, platform_requests_per_minute: 60,
  max_scan_bytes: 1000000000, max_run_scan_bytes: 10000000000, drain_reserve_bytes: 2000000000, poll_interval_seconds: 15,
  log_timestamp_field: 'timestamp', log_status_field: 'status', log_message_field: 'content', log_trace_id_field: 'trace_id',
}
export function dynatraceCanEnable(draft: DynatraceConnectionInput, saved?: DynatraceConnection | null): boolean {
  if (!saved?.revision || saved.verified_revision !== saved.revision) return false
  if (verifiedFields.some(field => JSON.stringify(draft[field] ?? verificationDefaults[field] ?? '') !== JSON.stringify(saved[field] ?? verificationDefaults[field] ?? ''))) return false
  return ![draft.environment_token_secret, draft.platform_token_secret, draft.oauth_client_id_secret, draft.oauth_client_secret_secret].some(value => value?.trim())
}

export function dynatraceScopeHelp(): string {
  return 'Classic APIs use read-only Environment tokens. Grail logs, spans, events, metrics and Smartscape need independent Platform read scopes and available licensed data. Successful authentication alone does not verify these capabilities.'
}
