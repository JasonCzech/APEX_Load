// Pure formatters, parsers, validators, and the redaction-rule draft type used
// by SettingsPage. Extracted from SettingsPage.tsx to shrink that component and
// make these reusable/testable in isolation. No React or component-state deps.

import type {
  DashboardAuditEvent,
  DashboardAuthDiagnostics,
  DashboardAuthStatus,
  DashboardSystemSettings,
  Project,
  RedactionRule,
} from '../types'

export type DraftRedactionRule = {
  name: string
  pattern: string
  replacement: string
}

export const AUTH_ROLE_LABELS: Record<string, string> = {
  system_admin: 'System Admin',
  project_admin: 'Project Admin',
  editor: 'Editor',
  viewer: 'Viewer',
}

export function parseOptionalWholeNumber(value: string): number | undefined {
  const trimmed = value.trim()
  if (!trimmed) return undefined
  const parsed = Number(trimmed)
  if (!Number.isInteger(parsed) || parsed < 0) return undefined
  return parsed
}

// Convert a stored RFC3339 timestamp to the value a <input type="datetime-local">
// expects (local time, minute precision). Empty/invalid -> ''.
export function toDatetimeLocal(iso?: string): string {
  if (!iso) return ''
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

// Convert a datetime-local input value back to an RFC3339 string the API accepts.
export function fromDatetimeLocal(value: string): string {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return date.toISOString()
}

export function parseOptionalNumber(value: string): number | undefined {
  const trimmed = value.trim()
  if (!trimmed) return undefined
  const parsed = Number(trimmed)
  return Number.isFinite(parsed) ? parsed : undefined
}

export function sortProjects(projects: Project[]) {
  return [...projects].sort((left, right) => left.name.localeCompare(right.name))
}

export function toDraftRedactionRules(rules?: RedactionRule[]): DraftRedactionRule[] {
  return (rules || []).map(rule => ({
    name: rule.name || '',
    pattern: rule.pattern || '',
    replacement: rule.replacement || '',
  }))
}

export function normalizeProjectRedactionRules(rules: DraftRedactionRule[]): RedactionRule[] {
  return rules.flatMap(rule => {
    const name = rule.name.trim()
    const pattern = rule.pattern.trim()
    const replacement = rule.replacement.trim()
    if (!name && !pattern && !replacement) {
      return []
    }

    const normalized: RedactionRule = { pattern }
    if (name) normalized.name = name
    if (replacement) normalized.replacement = replacement
    return [normalized]
  })
}

export function formatAuthRole(role: string): string {
  return AUTH_ROLE_LABELS[role] || role
}

export function formatAuthRoleList(roles?: string[]): string {
  if (!roles || roles.length === 0) {
    return 'None'
  }
  return roles.map(formatAuthRole).join(', ')
}

export function formatProjectRoleAssignments(projectRoles?: Record<string, string[]>): string {
  if (!projectRoles) {
    return 'None'
  }

  const assignments = Object.entries(projectRoles)
    .filter(([, roles]) => roles.length > 0)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([projectID, roles]) => `${projectID}: ${roles.map(formatAuthRole).join(', ')}`)

  if (assignments.length === 0) {
    return 'None'
  }

  return assignments.join(' | ')
}

export function formatDateTime(value?: string): string {
  if (!value) return 'Never'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

export function formatDurationSeconds(value: number): string {
  if (value < 60) return `${value}s`
  const hours = Math.floor(value / 3600)
  const minutes = Math.floor((value % 3600) / 60)
  if (hours > 0) return `${hours}h ${minutes}m`
  return `${minutes}m`
}

export function formatBytes(value?: number): string {
  const bytes = value || 0
  if (bytes >= 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`
  if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(2)} MB`
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(2)} KB`
  return `${bytes} B`
}

export function formatStatusCounts(counts?: Record<string, number>): string {
  const entries = Object.entries(counts || {}).sort(([left], [right]) => left.localeCompare(right))
  return entries.length ? entries.map(([status, count]) => `${status}=${count}`).join(', ') : 'none'
}

export function formatReadinessStatus(status?: string): string {
  switch (status) {
    case 'ready':
      return 'Ready'
    case 'degraded':
      return 'Degraded'
    case 'not_ready':
      return 'Not Ready'
    default:
      return status || 'Unknown'
  }
}

export function readinessTone(status?: string): string {
  switch (status) {
    case 'ready':
      return 'healthy'
    case 'degraded':
      return 'warning'
    case 'not_ready':
      return 'danger'
    default:
      return ''
  }
}

export function formatReadinessCheckName(name: string): string {
  const labels: Record<string, string> = {
    metadata_store: 'Metadata Store',
    archive_writer: 'Archive Writer',
    generator_rpc: 'Generator RPC',
    telemetry_fanout: 'Telemetry Fan-out',
    auth_mode: 'Auth Mode',
    scheduler_state: 'Scheduler State',
  }
  if (labels[name]) return labels[name]
  return name
    .split('_')
    .filter(Boolean)
    .map(part => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ')
}

export function formatReadinessDetails(details?: Record<string, unknown>): string {
  const entries = Object.entries(details || {})
    .filter(([, value]) => value !== undefined && value !== null && value !== '')
    .sort(([left], [right]) => left.localeCompare(right))

  if (entries.length === 0) return ''
  return entries.map(([key, value]) => {
    const formattedValue = Array.isArray(value) ? value.join(', ') : String(value)
    return `${key}: ${formattedValue}`
  }).join(' | ')
}

export function formatAuthStatusSummary(authStatus?: DashboardAuthStatus): string {
  if (!authStatus || !authStatus.enabled) {
    if (authStatus?.mode === 'dev') {
      return 'Development bypass'
    }
    return 'Disabled'
  }
  if (!authStatus.authenticated) {
    return authStatus.mode === 'oidc' ? 'Sign-in required' : 'Awaiting trusted headers'
  }
  return 'Authenticated'
}

export function formatSchedulePolicyLabel(value: string): string {
  return value
    .split('_')
    .filter(Boolean)
    .map(word => word.charAt(0).toUpperCase() + word.slice(1))
    .join(' ')
}

export function formatRoleScope(scope: string): string {
  switch (scope) {
    case 'global':
      return 'Global only'
    case 'global_or_project':
      return 'Global or project'
    default:
      return scope
  }
}

export function formatAccessOperation(operation: { global_roles?: string[]; project_roles?: string[] }): string {
  const segments: string[] = []
  if (operation.global_roles && operation.global_roles.length > 0) {
    segments.push(`Global: ${formatAuthRoleList(operation.global_roles)}`)
  }
  if (operation.project_roles && operation.project_roles.length > 0) {
    segments.push(`Project: ${formatAuthRoleList(operation.project_roles)}`)
  }
  return segments.join(' | ') || 'No roles'
}

export function formatGrantSource(grantSource?: string): string {
  switch (grantSource) {
  case 'dev_mode':
    return 'Allowed while development bypass is enabled'
  case 'disabled':
    return 'Allowed while auth is disabled'
  case 'global':
    return 'Allowed via global role'
  case 'project':
    return 'Allowed via project role'
  default:
    return 'Not granted'
  }
}

export function formatHeaderContractValue(value?: string, fallback = 'Not configured'): string {
  const normalized = value?.trim() || ''
  return normalized || fallback
}

export function formatAuthDiagnosticStatus(status?: string): string {
  switch (status) {
    case 'pass':
      return 'Pass'
    case 'warn':
      return 'Advisory'
    case 'fail':
      return 'Action Required'
    case 'info':
      return 'Info'
    default:
      return status || 'Unknown'
  }
}

export function formatAuthDiagnosticsHeadline(diagnostics: DashboardAuthDiagnostics | null): string {
  if (!diagnostics) {
    return 'Diagnostics unavailable'
  }
  return diagnostics.ready ? 'Ready' : 'Needs attention'
}

export function normalizeDashboardAuthMode(mode?: DashboardSystemSettings['auth']['mode']): DashboardSystemSettings['auth']['mode'] {
  if (mode === 'trusted_headers' || mode === 'oidc' || mode === 'disabled') {
    return mode
  }
  return 'dev'
}

export function formatAuthMode(mode: DashboardSystemSettings['auth']['mode']): string {
  switch (mode) {
    case 'oidc':
      return 'Native OIDC'
    case 'trusted_headers':
      return 'Trusted Headers'
    case 'disabled':
    case 'dev':
    default:
      return 'Dev Bypass'
  }
}

export function formatAuthScopes(scopes?: string[]): string {
  return (scopes || []).join(', ')
}

export function parseAuthScopes(value: string): string[] {
  return value
    .split(/[\s,]+/)
    .map(token => token.trim())
    .filter(Boolean)
}

export function validateAuthURL(label: string, value: string | undefined, allowAbsolutePath: boolean): string | null {
  const trimmed = value?.trim() || ''
  if (!trimmed) {
    return null
  }
  if (allowAbsolutePath && trimmed.startsWith('/')) {
    return null
  }
  try {
    const parsed = new URL(trimmed)
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
      return `${label} must use http${allowAbsolutePath ? ', https, or an absolute path.' : ' or https.'}`
    }
  } catch {
    return `${label} must use http${allowAbsolutePath ? ', https, or an absolute path.' : ' or https.'}`
  }
  return null
}

export function formatAuditActor(event: DashboardAuditEvent): string {
  return event.actor_display_name || event.actor_subject || event.actor_type
}

export function formatAuditTimestamp(value?: string): string {
  if (!value) {
    return 'Unknown'
  }
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString()
}
