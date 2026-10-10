import type { SettingsSectionId } from './settingsSectionLayout'

export interface SettingsDestination {
  id: string
  group: string
  title: string
  section: SettingsSectionId
  subsection?: string
  governance?: string
  scope: 'deployment' | 'domain' | 'project' | 'browser'
  resource?: 'cloud' | 'clusters' | 'pools' | 'allocation' | 'generators'
}

export const SETTINGS_DESTINATIONS: readonly SettingsDestination[] = [
  { id: 'platform', group: 'Platform', title: 'Controller & runtime', section: 'general', subsection: 'platform', scope: 'deployment' },
  { id: 'preferences', group: 'Platform', title: 'Personal preferences', section: 'general', subsection: 'platform', scope: 'browser' },
  { id: 'entitlements', group: 'Platform', title: 'Edition & deployment limits', section: 'general', subsection: 'entitlements', scope: 'deployment' },
  { id: 'domains', group: 'Workspaces', title: 'Domains & placement defaults', section: 'domains', scope: 'domain' },
  { id: 'projects', group: 'Workspaces', title: 'Projects', section: 'projects', subsection: 'catalog', scope: 'domain' },
  { id: 'applications', group: 'Workspaces', title: 'Applications & releases', section: 'applications', scope: 'project' },
  { id: 'limits', group: 'Workspaces', title: 'Project limits & quotas', section: 'projects', subsection: 'limits', scope: 'project' },
  { id: 'usage', group: 'Workspaces', title: 'Usage accounting', section: 'general', subsection: 'usage', scope: 'project' },
  { id: 'github-policy', group: 'Workspaces', title: 'Project GitHub policy', section: 'projects', subsection: 'github', scope: 'project' },
  { id: 'analysis-policies', group: 'Workspaces', title: 'Browser & journey policies', section: 'projects', subsection: 'analysis-policies', scope: 'project' },
  { id: 'analysis', group: 'Workspaces', title: 'Project analysis', section: 'projects', subsection: 'analysis', scope: 'project' },
  { id: 'github', group: 'Connections', title: 'GitHub', section: 'integrations', subsection: 'github', scope: 'deployment' },
  { id: 'elk-capture', group: 'Connections', title: 'Elastic test capture', section: 'integrations', subsection: 'elk-capture', scope: 'project' },
  { id: 'dynatrace-capture', group: 'Connections', title: 'Dynatrace test capture', section: 'integrations', subsection: 'dynatrace-capture', scope: 'project' },
  { id: 'prometheus-capture', group: 'Connections', title: 'Prometheus test monitoring', section: 'integrations', subsection: 'prometheus-capture', scope: 'project' },
  { id: 'prometheus', group: 'Connections', title: 'APEX platform graphs', section: 'integrations', subsection: 'prometheus', scope: 'deployment' },
  { id: 'opensearch', group: 'Connections', title: 'OpenSearch', section: 'integrations', subsection: 'opensearch', scope: 'deployment' },
  { id: 'grafana', group: 'Connections', title: 'Grafana', section: 'integrations', subsection: 'grafana', scope: 'deployment' },
  { id: 'dashboards', group: 'Connections', title: 'Imported dashboards', section: 'integrations', subsection: 'dashboards', scope: 'project' },
  { id: 'powerbi', group: 'Connections', title: 'Power BI', section: 'integrations', subsection: 'powerbi', scope: 'deployment' },
  { id: 'confluence', group: 'Connections', title: 'Confluence', section: 'integrations', subsection: 'confluence', scope: 'project' },
  { id: 'federation', group: 'Connections', title: 'Library federation', section: 'integrations', subsection: 'library', scope: 'project' },
  { id: 'cloud', group: 'Infrastructure', title: 'Cloud connections & targets', section: 'capacity', scope: 'deployment', resource: 'cloud' },
  { id: 'clusters', group: 'Infrastructure', title: 'Kubernetes clusters', section: 'capacity', scope: 'deployment', resource: 'clusters' },
  { id: 'pools', group: 'Infrastructure', title: 'Generator pools', section: 'capacity', scope: 'deployment', resource: 'pools' },
  { id: 'allocation', group: 'Infrastructure', title: 'Load generator allocation', section: 'capacity', scope: 'deployment', resource: 'allocation' },
  { id: 'generators', group: 'Infrastructure', title: 'Generator metadata', section: 'capacity', scope: 'deployment', resource: 'generators' },
  { id: 'capacity', group: 'Infrastructure', title: 'Capacity source', section: 'capacity', subsection: 'availability', scope: 'deployment' },
  { id: 'planning', group: 'Infrastructure', title: 'Planning profile', section: 'capacity', subsection: 'generator', scope: 'deployment' },
  { id: 'allocations', group: 'Infrastructure', title: 'Standing allocations', section: 'capacity', subsection: 'reservations', scope: 'deployment' },
  { id: 'authentication', group: 'Access & Security', title: 'Authentication', section: 'general', subsection: 'authentication', scope: 'deployment' },
  { id: 'features', group: 'Access & Security', title: 'Domain feature policy', section: 'feature-access', scope: 'domain' },
  { id: 'imports', group: 'Access & Security', title: 'Import credential policy', section: 'general', subsection: 'governance', governance: 'script-imports', scope: 'deployment' },
  { id: 'redaction', group: 'Access & Security', title: 'Project redaction rules', section: 'projects', subsection: 'redaction', scope: 'project' },
  { id: 'secrets', group: 'Access & Security', title: 'Project secrets', section: 'projects', subsection: 'secrets', scope: 'project' },
  { id: 'audit', group: 'Access & Security', title: 'Governance audit', section: 'general', subsection: 'governance', governance: 'recent-audit-events', scope: 'deployment' },
  { id: 'thresholds', group: 'Alerts & Scheduling', title: 'Alert & stop policies', section: 'thresholds', scope: 'deployment' },
  { id: 'notifications', group: 'Alerts & Scheduling', title: 'Notification routes', section: 'integrations', subsection: 'notifications', scope: 'deployment' },
  { id: 'scheduling', group: 'Alerts & Scheduling', title: 'Schedule defaults', section: 'general', subsection: 'governance', governance: 'schedule-defaults', scope: 'deployment' },
  { id: 'retention', group: 'Data & Retention', title: 'Archive & HAR retention', section: 'capacity', subsection: 'retention', scope: 'deployment' },
  { id: 'test-data', group: 'Test Data', title: 'Shared data pools', section: 'data-pools', scope: 'project' },
]

export function settingsDestination(id?: string): SettingsDestination | undefined {
  return SETTINGS_DESTINATIONS.find(item => item.id === id)
}

/** A destination's one name, reused by both section lists and its page card. */
export function destinationTitle(id: string): string {
  return settingsDestination(id)?.title ?? id
}

export function settingsOwner(id: string): 'settings' | 'access' | 'repository' {
  if (settingsDestination(id)?.group === 'Access & Security') return 'access'
  return id === 'test-data' ? 'repository' : 'settings'
}

export function settingsHref(id: string, currentHash = typeof window === 'undefined' ? '' : window.location.hash, resourceId?: string): string {
  const current = new URLSearchParams(currentHash.split('?')[1] || '')
  const params = new URLSearchParams({ setting: settingsDestination(id)?.id || 'platform' })
  const origin = /^#\/(repository|monitoring|fleet|dashboard|schedules|command-center|new-test|tests|test\/[^?#]+)(\?|$)/.test(currentHash) && !current.has('setting') ? currentHash : current.get('returnTo') || ''
  if (/^#\/(repository|monitoring|fleet|dashboard|schedules|command-center|new-test|tests|test\/[^?#]+)(\?|$)/.test(origin)) params.set('returnTo', origin)
  if (resourceId) params.set('resourceId', resourceId)
  for (const key of ['projectId', 'applicationId', 'releaseId']) {
    const value = current.get(key)
    if (value) params.set(key, value)
  }
  return `#/${settingsOwner(id)}?${params}`
}

/** Carry a workspace into a related operational page without unrelated settings filters. */
export function workspaceHref(page: 'fleet' | 'schedules', currentHash = typeof window === 'undefined' ? '' : window.location.hash, projectId?: string): string {
  const current = new URLSearchParams(currentHash.split('?')[1] || '')
  const params = new URLSearchParams()
  const targetProject = projectId ?? current.get('projectId') ?? ''
  if (targetProject) params.set('projectId', targetProject)
  // A caller switching projects must not attach a child from the old project.
  if (projectId === undefined || projectId === current.get('projectId')) {
    for (const key of ['applicationId', 'releaseId']) {
      const value = current.get(key)
      if (value) params.set(key, value)
    }
  }
  return `#/${page}${params.size ? `?${params}` : ''}`
}

// Synonyms in the vocabulary admins and LoadRunner Enterprise engineers search with.
const SETTINGS_SEARCH_TERMS: Record<string, string> = {
  preferences: 'theme dark mode light system appearance word wrap editor rows table motion accessibility start page landing',
  platform: 'endpoint concurrency metrics interval runtime downloads desktop proxy',
  authentication: 'login sso oidc saml ldap idp identity provider password session users security',
  retention: 'archive har cleanup storage delete expiration',
  scheduling: 'timezone time zone recurring calendar cron',
  thresholds: 'sla p95 p99 response time error rate auto-stop autostop alert threshold',
  notifications: 'webhook slack teams email mail alert notify',
  entitlements: 'license licence edition limits seats',
  pools: 'load generator lg vuser capacity scale',
  planning: 'load generator lg vuser capacity sizing',
  generators: 'load generator lg host',
  limits: 'quota vuser cap',
  'test-data': 'data pool parameter csv dataset',
}

export function matchesSettingsSearch(item: SettingsDestination, query: string): boolean {
  const terms = `${item.group} ${item.title} ${SETTINGS_SEARCH_TERMS[item.id] || ''}`.toLowerCase()
  return query.toLowerCase().trim().split(/\s+/).every(word => terms.includes(word))
}

/** Resolve common personal-setting searches to the actual accessible control. */
export function settingsSearchControl(destinationId: string, query: string): string | undefined {
  if (destinationId !== 'preferences') return undefined
  const value = query.toLowerCase().trim()
  if (/\b(theme|dark|light|appearance)\b/.test(value)) return 'personal-theme'
  if (/\b(wrap|editor)\b/.test(value)) return 'personal-word-wrap'
  if (/\b(rows|table)\b/.test(value)) return 'personal-table-rows'
  if (/\b(motion|accessibility)\b/.test(value)) return 'personal-motion'
  return undefined
}
