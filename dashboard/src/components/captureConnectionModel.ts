import type { ELKConnectionInput } from '../hooks/api/useELKConnectionsApi'
import type { DynatraceConnectionInput } from '../hooks/api/useDynatraceConnectionsApi'
import type { ELKConnection, DynatraceConnection } from '../types/monitoring'

export function captureSelectionNeedsReview(id: string, connections: { id: string }[], loading: boolean, error: boolean): boolean {
  return Boolean(id) && (loading || error || !connections.some(c => c.id === id))
}

export function dynatraceCapabilities(c?: Pick<DynatraceConnection, 'metrics_enabled' | 'problems_enabled' | 'logs_enabled' | 'events_enabled' | 'spans_enabled' | 'grail_metrics_enabled' | 'grail_events_enabled' | 'topology_mode'>): string {
  return c ? [c.metrics_enabled && 'Metrics', c.problems_enabled && 'Problems', c.events_enabled && 'Events', c.logs_enabled && 'Grail logs', c.spans_enabled && 'Grail spans', c.grail_metrics_enabled && 'Grail numeric queries', c.grail_events_enabled && 'Grail events', c.topology_mode && 'Topology'].filter(Boolean).join(', ') : ''
}

export function elkMissing(d: ELKConnectionInput, saved?: ELKConnection | null): string[] {
  const missing: string[] = []
  const savedIdentity = Boolean(saved && d.endpoint === (saved.config?.endpoint || saved.endpoint) && (d.product || d.config?.product || 'elasticsearch') === (saved.product || saved.config?.product || 'elasticsearch') && d.auth_type === (saved.config?.auth_type || saved.auth_type) && (d.collection_mode || 'direct') === (saved.collection_mode || 'direct') && (d.collector_agent_id || '') === (saved.collector_agent_id || ''))
  const retained = savedIdentity ? saved : undefined
  for (const [key, label] of (d.config ? [['endpoint', 'HTTPS endpoint']] as const : [['endpoint', 'HTTPS endpoint'], ['index_pattern', 'Index pattern'], ['timestamp_field', 'Timestamp field'], ['message_field', 'Message field']] as const)) {
    if (!d[key].trim()) missing.push(label)
  }
  if (!d.auth_type) missing.push('Authentication method')
  if (d.auth_type === 'api_key' && !d.api_key_secret_name?.trim() && !retained?.has_api_key) missing.push('API key secret reference')
  if (d.auth_type === 'basic') {
    if (!d.username_secret_name?.trim() && !retained?.has_basic_username) missing.push('Username secret reference')
    if (!d.password_secret_name?.trim() && !(retained?.has_basic_password && !d.username_secret_name?.trim())) missing.push('Password secret reference')
  }
  if (d.auth_type === 'bearer' && !d.bearer_token_secret_name?.trim() && !retained?.has_bearer_token) missing.push('Bearer token secret reference')
  if (d.auth_type === 'mtls') {
    if (!d.client_certificate_secret_name?.trim() && !retained?.has_client_certificate) missing.push('Client certificate secret reference')
    if (!d.client_key_secret_name?.trim() && !retained?.has_client_key) missing.push('Client key secret reference')
  }
  if (d.collection_mode === 'collector' && !d.collector_agent_id) missing.push('Assigned collector')
  if (d.metric_aggregations.some(m => !m.name.trim() || !m.field.trim())) missing.push('Complete or remove unfinished aggregations')
  return missing
}

export function elkCanEnable(d: ELKConnectionInput, saved?: ELKConnection | null): boolean {
  if (!d.config) return elkMissing(d, saved).length === 0
  if (!saved?.revision || saved.verified_revision !== saved.revision || elkMissing(d, saved).length) return false
  if (d.endpoint !== saved.config?.endpoint || d.product !== saved.product || d.auth_type !== saved.auth_type || (d.collection_mode || 'direct') !== (saved.collection_mode || 'direct') || (d.collector_agent_id || '') !== (saved.collector_agent_id || '')) return false
  if (Object.entries(d).some(([name, value]) => name.endsWith('_secret_name') && Boolean(value))) return false
  const compare = (config: typeof d.config) => { const value = { ...config }; delete value.ca_pem; delete value.approved_cidrs; delete value.kibana_ca_pem; delete value.kibana_approved_cidrs; delete value.logstash_ca_pem; delete value.logstash_approved_cidrs; return JSON.stringify(value) }
  return compare(d.config) === compare(saved.config) && !d.config.ca_pem && !d.config.approved_cidrs?.length && !d.config.kibana_ca_pem && !d.config.kibana_approved_cidrs?.length && !d.config.logstash_ca_pem && !d.config.logstash_approved_cidrs?.length && d.kibana_auth_type === saved.kibana_auth_type && d.logstash_auth_type === saved.logstash_auth_type
}

export function dynatraceMissing(d: DynatraceConnectionInput, saved?: DynatraceConnection | null): string[] {
  const missing: string[] = []
  if (!d.metrics_enabled && !d.problems_enabled && !d.logs_enabled && !d.events_enabled && !d.spans_enabled && !d.grail_metrics_enabled && !d.grail_events_enabled && !d.topology_mode) missing.push('At least one capture capability')
  const environmentUnchanged = !saved || d.environment_url === saved.environment_url
  const platformUnchanged = !saved || d.platform_url === saved.platform_url && d.platform_auth_type === (saved.platform_auth_type || '') && d.oauth_resource === (saved.oauth_resource || '') && JSON.stringify(d.oauth_scopes) === JSON.stringify(saved.oauth_scopes || [])
  if (d.metrics_enabled || d.problems_enabled || d.events_enabled || d.topology_mode === 'classic') {
    if (!d.environment_url.trim()) missing.push('Environment URL')
    if (!d.environment_token_secret?.trim() && !(environmentUnchanged && saved?.has_environment_token)) missing.push('Environment token secret reference')
  }
  if (d.metrics_enabled && d.metric_queries.some(m => !m.name.trim() || !m.metric_selector.trim())) missing.push('Complete metric selector')
  if (d.logs_enabled || d.spans_enabled || d.grail_metrics_enabled || d.grail_events_enabled || d.topology_mode === 'smartscape') {
    if (!d.platform_url.trim()) missing.push('Platform URL')
    if (!d.platform_auth_type) missing.push('Platform authentication method')
    if (d.platform_auth_type === 'platform_token' && !d.platform_token_secret?.trim() && !(platformUnchanged && saved?.has_platform_token)) missing.push('Platform token secret reference')
    if (d.platform_auth_type === 'oauth_client') {
      if (!d.oauth_client_id_secret?.trim() && !(platformUnchanged && saved?.has_oauth_client_id)) missing.push('OAuth client ID secret reference')
      if (!d.oauth_client_secret_secret?.trim() && !(platformUnchanged && saved?.has_oauth_client_secret)) missing.push('OAuth client secret reference')
      if (!d.oauth_resource.trim()) missing.push('OAuth resource')
      if (!d.oauth_scopes.length) missing.push('OAuth scopes')
    }
  }
  if (d.collection_mode === 'collector' && !d.collector_agent_id?.trim()) missing.push('Assigned collector')
  return missing
}

export function legacyELKCaptureReady(connection: ELKConnection): boolean {
  if (!connection.enabled || connection.status !== 'ready') return false
  if (connection.legacy_capture_ready !== undefined) return connection.legacy_capture_ready
  return !connection.config || Boolean(connection.index_pattern && connection.timestamp_field && connection.message_field && ['api_key', 'basic'].includes(connection.auth_type || ''))
}
