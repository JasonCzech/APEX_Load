import type { AppDynamicsConnection, AppDynamicsConnectionInput, AppDynamicsSelection, RunMonitorJob } from '../types/appdynamics'

export function appDynamicsDraft(connection?: AppDynamicsConnection): AppDynamicsConnectionInput {
  return connection ? { name: connection.name, description: connection.description || '', enabled: connection.enabled, collection_mode: connection.collection_mode, collector_agent_id: connection.collector_agent_id || '', config: { ...connection.config }, revision: connection.revision } : {
    name: '', description: '', enabled: false, collection_mode: 'direct', config: { endpoint: '', context_path: '/controller', account_name: '', auth_type: 'oauth', approved_cidrs: [], timeout_seconds: 10, requests_per_minute: 60, analytics_requests_per_minute: 30, max_series: 200, max_points: 10000, max_response_bytes: 4194304, max_requests_per_poll: 30, max_poll_seconds: 30 },
  }
}
export function appDynamicsMissing(draft: AppDynamicsConnectionInput, saved?: AppDynamicsConnection | null): string[] {
  const missing: string[] = []
  if (!draft.config.endpoint.trim()) missing.push('Controller HTTPS URL')
  if (!draft.config.account_name.trim()) missing.push('Account name')
  const identityUnchanged = Boolean(saved && ['endpoint', 'context_path', 'account_name', 'auth_type'].every(key => draft.config[key as keyof typeof draft.config] === saved.config[key as keyof typeof saved.config]))
  if (draft.config.auth_type === 'oauth') {
    if (!draft.client_id_secret?.trim() && !(identityUnchanged && saved?.has_client_id)) missing.push('OAuth client ID secret reference')
    if (!draft.client_secret_secret?.trim() && !(identityUnchanged && saved?.has_client_secret)) missing.push('OAuth client secret reference')
  } else {
    if (!draft.username_secret?.trim() && !(identityUnchanged && saved?.has_username)) missing.push('Username secret reference')
    if (!draft.password_secret?.trim() && !(identityUnchanged && saved?.has_password)) missing.push('Password secret reference')
  }
  if (draft.collection_mode === 'collector' && !draft.collector_agent_id?.trim()) missing.push('Assigned collector')
  const analyticsIdentityUnchanged = Boolean(saved && draft.config.events_endpoint === saved.config.events_endpoint && draft.config.events_account_name === saved.config.events_account_name)
  if (draft.config.events_endpoint && (!draft.config.events_account_name?.trim() || (!draft.events_api_key_secret?.trim() && !(analyticsIdentityUnchanged && saved?.has_events_api_key)))) missing.push('Analytics account and API key secret reference')
  return missing
}

export function appDynamicsCanEnable(draft: AppDynamicsConnectionInput, saved?: AppDynamicsConnection | null): boolean {
  if (!saved || saved.verified_revision !== saved.revision) return false
  if (draft.collection_mode !== saved.collection_mode || (draft.collector_agent_id || '') !== (saved.collector_agent_id || '')) return false
  if (JSON.stringify(draft.config) !== JSON.stringify(saved.config)) return false
  return ![draft.client_id_secret, draft.client_secret_secret, draft.username_secret, draft.password_secret, draft.events_api_key_secret].some(value => Boolean(value?.trim()))
}

export function monitorJobState(job: RunMonitorJob): string {
  if (job.state === 'draining' || job.state === 'reconciling') return 'Load finished; collecting delayed AppDynamics data'
  if (job.state === 'complete' || job.state === 'stopped') return 'Collection finished'
  if (job.state === 'incomplete') return 'Collection incomplete'
  return job.state
}
export const emptyAppDynamicsSelection = (): AppDynamicsSelection => ({ applications: [], metrics: [], events: true, violations: true, snapshots: false, analytics: [], settle_seconds: 120 })
