// Settings domain model: default settings, generator/OIDC presets, and the
// pure validators + draft/normalize/derive builders for the settings form.
// Extracted from SettingsPage.tsx; no React or component-state dependencies.

import type {
  CapacityReservation,
  DashboardSystemSettings,
  GitHubProjectPolicy,
  PoolProjectQuota,
	ProjectSettings,
  RedactionRule,
  SystemInventoryResponse,
} from '../types'
import type { FleetPool } from '../types/fleet'
import {
  normalizeDashboardAuthMode,
  normalizeProjectRedactionRules,
  parseOptionalNumber,
  parseOptionalWholeNumber,
  validateAuthURL,
  type DraftRedactionRule,
} from './settingsPageFormatters'
import type { CapacitySettingsSubsectionId, SettingsSectionId } from './settingsSectionLayout'

export const DEFAULT_SETTINGS: DashboardSystemSettings = {
  general: {
    api_endpoint: ':8080',
    max_concurrent_tests: 10,
    metrics_interval_seconds: 1,
    usage_snapshot_interval_seconds: 0,
  },
  thresholds: {
    max_error_rate_pct: undefined,
    max_p99_latency_ms: undefined,
    p99_latency_stop_consecutive_samples: undefined,
    max_avg_server_processing_ms: undefined,
    max_avg_transfer_ms: undefined,
    max_telemetry_sample_age_ms: 2000,
    max_telemetry_generator_lag_ms: 500,
    max_telemetry_transport_lag_ms: 500,
    max_telemetry_controller_fanout_ms: 500,
    max_telemetry_browser_render_lag_ms: 500,
    fleet_constrained_available_slots: undefined,
    fleet_saturated_available_slots: undefined,
  },
  capacity_planning: {
    headroom_pct: 20,
    generator: {
      cpu_cores: 4,
      memory_mb: 8192,
      network_mbps: 1000,
      max_vusers: 10000,
    },
    availability: {
      source: 'configured',
      configured_available_generators: 1,
    },
    reservations: [],
  },
  archive_retention: {
    enabled: false,
    default_retention_days: 90,
    har_retention_days: 7,
    har_artifact_dir: '',
    policies: [],
  },
  schedule_defaults: {
    approval_policy: 'auto',
    missed_run_policy: 'skip',
    blackout_start_time: '',
    blackout_end_time: '',
    availability_start_time: '',
    availability_end_time: '',
  },
  notifications: {
    webhooks: [],
  },
  auth: {
    mode: 'dev',
    provider_name: '',
    login_url: '',
    logout_url: '',
    issuer_url: '',
    client_id: '',
    client_secret_env_var: 'APEXLOAD_OIDC_CLIENT_SECRET',
    redirect_url: '',
    scopes: ['openid', 'profile', 'email'],
    roles_claim: 'roles',
    project_roles_claim: 'project_roles',
    session_secret_env_var: 'APEXLOAD_AUTH_SESSION_SECRET',
    trusted_proxy_cidrs: [],
    trusted_proxy_secret_env_var: '',
    user_header: 'X-Auth-Request-User',
    name_header: 'X-Auth-Request-Name',
    email_header: 'X-Auth-Request-Email',
    roles_header: 'X-APEXLoad-Roles',
    project_roles_header: 'X-APEXLoad-Project-Roles',
  },
  github: {
    enabled: false,
    client_id: '',
    client_secret_env_var: 'APEXLOAD_GITHUB_CLIENT_SECRET',
    app_id: 0,
    app_slug: '',
    app_private_key_env_var: 'APEXLOAD_GITHUB_APP_PRIVATE_KEY',
    app_private_key_file: '',
    webhook_secret_env_var: 'APEXLOAD_GITHUB_WEBHOOK_SECRET',
    pat_env_var: '',
    automation_mode: 'app_oauth_pat',
    checks_enabled: false,
    api_base_url: 'https://api.github.com',
    web_base_url: 'https://github.com',
    ca_cert_file: '',
    tls_insecure_skip_verify: false,
    scopes: ['repo'],
    default_repo: '',
    default_branch: 'main',
    path_template: 'apexload/scripts/{project}/{ref_key}.json',
  },
  prometheus: {
    enabled: false,
    base_url: '',
    timeout_seconds: 5,
    bearer_token_env_var: '',
    allow_label_values: false,
  },
  grafana: {
    enabled: false,
    base_url: '',
    service_account_token_env_var: '',
    timeout_seconds: 10,
  },
  powerbi: {
    enabled: false,
    tenant_id: '',
    client_id: '',
    client_secret_env_var: '',
    workspace_id: '',
    dataset_name: '',
    auth_base_url: '',
    api_base_url: '',
    scope: '',
    timeout_seconds: 30,
  },
  client_app: {
    availability: 'disabled',
  },
  client_downloads: {
    enabled: true,
  },
  script_imports: {
    allow_source_secret_migration: false,
  },
}

export const GENERATOR_PRESETS = [
  { id: 'local', label: 'Local', cpu_cores: 2, memory_mb: 4096, network_mbps: 250, max_vusers: 1000 },
  { id: 'standard', label: 'Standard Pod', cpu_cores: 4, memory_mb: 8192, network_mbps: 1000, max_vusers: 10000 },
  { id: 'large', label: 'Large Pod', cpu_cores: 8, memory_mb: 16384, network_mbps: 2500, max_vusers: 20000 },
] as const

export const OIDC_PROVIDER_PRESETS = [
  {
    id: 'entra',
    label: 'Microsoft Entra ID OIDC',
    provider_name: 'Microsoft Entra ID',
    scopes: ['openid', 'profile', 'email'],
    roles_claim: 'roles',
    project_roles_claim: 'project_roles',
    client_secret_env_var: 'APEXLOAD_OIDC_CLIENT_SECRET',
    session_secret_env_var: 'APEXLOAD_AUTH_SESSION_SECRET',
    logout_url: '/',
    description: 'Uses Entra application roles or shaped group claims for APEX Load RBAC.',
  },
  {
    id: 'generic',
    label: 'Generic OIDC',
    provider_name: 'OIDC Provider',
    scopes: ['openid', 'profile', 'email'],
    roles_claim: 'roles',
    project_roles_claim: 'project_roles',
    client_secret_env_var: 'APEXLOAD_OIDC_CLIENT_SECRET',
    session_secret_env_var: 'APEXLOAD_AUTH_SESSION_SECRET',
    logout_url: '/',
    description: 'Keeps the standard OIDC scope and claim contract for any compliant issuer.',
  },
] as const

type NotificationWebhook = DashboardSystemSettings['notifications']['webhooks'][number]

export function updateNotificationWebhookDraft(
  webhook: NotificationWebhook,
  updates: Partial<NotificationWebhook>,
): NotificationWebhook {
  const endpointChanged = (
    ('url' in updates && updates.url !== webhook.url)
    || ('format' in updates && updates.format !== webhook.format)
  )
  const next = { ...webhook, ...updates }
  if (endpointChanged && webhook.has_api_token && !next.api_token?.trim()) {
    // A write-only token is scoped to the destination it was saved with. Once
    // that destination changes, retaining the opaque marker would falsely tell
    // validation that the new endpoint has credentials.
    next.has_api_token = false
  }
  return next
}



export function validateProjectDraft(name: string, rules: DraftRedactionRule[]): string {
  if (!name.trim()) {
    return 'Project name is required.'
  }

  for (let index = 0; index < rules.length; index += 1) {
    const rule = rules[index]
    const isBlank = !rule.name.trim() && !rule.pattern.trim() && !rule.replacement.trim()
    if (isBlank) {
      continue
    }
    if (!rule.pattern.trim()) {
      return `Redaction rule ${index + 1} needs a regex pattern.`
    }
  }

  return ''
}

export function buildProjectSettingsDraft(
  rules: DraftRedactionRule[],
  maxConcurrentTests: string,
  maxVUsers: string,
  maxVUsersPerTest: string,
  maxTestDurationMinutes: string,
  github?: GitHubProjectPolicy,
  // Per-pool quotas are edited via a dedicated card, not this governance form;
  // they are passed through here so saving the governance defaults never drops
  // quotas that were set elsewhere.
  poolQuotas?: Record<string, PoolProjectQuota>,
	features?: ProjectSettings['features'],
) {
  const settings: {
    redaction_rules?: RedactionRule[]
    max_concurrent_tests?: number
    max_vusers?: number
    max_vusers_per_test?: number
    max_test_duration_minutes?: number
    github?: GitHubProjectPolicy
    pool_quotas?: Record<string, PoolProjectQuota>
	features?: ProjectSettings['features']
  } = {
    redaction_rules: normalizeProjectRedactionRules(rules),
  }
  if (poolQuotas && Object.keys(poolQuotas).length > 0) {
    settings.pool_quotas = poolQuotas
  }
	if (features && Object.values(features).some(Boolean)) {
		settings.features = { ...features }
	}
  const parsedConcurrent = parseOptionalWholeNumber(maxConcurrentTests)
  if (parsedConcurrent !== undefined) {
    settings.max_concurrent_tests = parsedConcurrent
  }
  const parsedVUsers = parseOptionalWholeNumber(maxVUsers)
  if (parsedVUsers !== undefined) {
    settings.max_vusers = parsedVUsers
  }
  const parsedVUsersPerTest = parseOptionalWholeNumber(maxVUsersPerTest)
  if (parsedVUsersPerTest !== undefined) {
    settings.max_vusers_per_test = parsedVUsersPerTest
  }
  const parsedDuration = parseOptionalWholeNumber(maxTestDurationMinutes)
  if (parsedDuration !== undefined) {
    settings.max_test_duration_minutes = parsedDuration
  }
  if (github) {
    settings.github = github
  }
  return settings
}

export function splitListDraft(value: string): string[] {
  return value.split(',').map(item => item.trim()).filter(Boolean)
}

// projectGitHubDraftFromPolicy is the normalized GitHub policy the project form
// holds for a given saved policy — it must mirror loadProjectGitHubDraft +
// buildProjectGitHubDraft in SettingsPage. The unsaved-changes baseline uses it so
// the comparison is symmetric with the live draft (which always builds a full
// object); otherwise a project with no/partial GitHub policy reads as permanently
// dirty, leaving the governance Save button stuck enabled.
export function projectGitHubDraftFromPolicy(policy?: GitHubProjectPolicy): GitHubProjectPolicy {
  return {
    enabled: Boolean(policy?.enabled),
    allowed_repos: splitListDraft((policy?.allowed_repos || []).join(', ')),
    allowed_branches: splitListDraft((policy?.allowed_branches || []).join(', ')),
    path_template: (policy?.path_template || '').trim(),
    require_pull_request: policy?.require_pull_request ?? true,
    allow_direct_commit: Boolean(policy?.allow_direct_commit),
    allow_import: policy?.allow_import ?? true,
    allow_push: policy?.allow_push ?? true,
    bulk_sync_enabled: Boolean(policy?.bulk_sync_enabled),
    auto_sync_enabled: Boolean(policy?.auto_sync_enabled),
    checks_enabled: Boolean(policy?.checks_enabled),
    app_installation_id: policy?.app_installation_id ? Number(policy.app_installation_id) : undefined,
    default_repo: (policy?.default_repo || '').trim(),
    default_branch: (policy?.default_branch || '').trim(),
    default_sync_directory: (policy?.default_sync_directory || '').trim(),
    automation_identity: (policy?.automation_identity || '').trim(),
    // GitHub Actions authorization is not edited by this source-sync form, but
    // it is passed through by buildProjectGitHubDraft. Keep it in the baseline
    // too, otherwise a project with an Actions policy is permanently reported
    // as dirty as soon as Settings loads.
    actions: policy?.actions,
  }
}

export function validateProjectQuotaField(label: string, value: string): string {
  const trimmed = value.trim()
  if (!trimmed) return ''
  const parsed = Number(trimmed)
  if (!Number.isInteger(parsed) || parsed < 0) {
    return `${label} must be an integer greater than or equal to 0.`
  }
  return ''
}

export function validateOptionalPositiveInteger(label: string, value: string): string {
  const trimmed = value.trim()
  if (!trimmed) return ''
  const parsed = Number(trimmed)
  if (!Number.isInteger(parsed) || parsed <= 0) {
    return `${label} must be a positive integer.`
  }
  return ''
}

export function isClockTime(value: string): boolean {
  if (!value.trim()) return true
  return /^([01]\d|2[0-3]):[0-5]\d$/.test(value.trim())
}

export function validateScheduleWindowPair(label: string, start?: string, end?: string): string {
  const startValue = (start || '').trim()
  const endValue = (end || '').trim()
  if (Boolean(startValue) !== Boolean(endValue)) {
    return `${label} window requires both start and end times.`
  }
  if (!isClockTime(startValue) || !isClockTime(endValue)) {
    return `${label} window times must use HH:MM.`
  }
  return ''
}

export interface SettingsValidationIssue {
  section: SettingsSectionId
  subsection?: string
  field?: string
  message: string
}

// Mirrors trustedProxyGateConfigured() in pkg/api/auth_resolution.go, including
// its trim-per-CIDR semantics.
export function trustedProxyGateConfigured(auth: DashboardSystemSettings['auth']): boolean {
  return (auth.trusted_proxy_cidrs ?? []).some(cidr => cidr.trim() !== '')
    || Boolean(auth.trusted_proxy_secret_env_var?.trim())
}

// Posture the controller actually booted/serves with, latched from every server
// settings payload (see normalizeDashboardSystemSettings).
//
// The trusted-proxy-gate requirement is production-only on the server: it is a
// hard boot failure under APEXLOAD_ENV=production (pkg/api/server.go
// enforceAuthPosture) and only a slog.Warn plus "legacy behavior" pass-through
// otherwise (enforceTrustedProxyGate, pkg/api/auth_resolution.go). A controller
// that is serving settings while its *saved* auth is trusted_headers with no
// gate therefore proves it is not running in production. Grandfathering that
// saved posture keeps the page-global save block (canSave in SettingsPage is
// gated on the whole issue list) from stranding such an install on unrelated
// settings, whose only escape would be configuring a proxy gate that then
// rejects every request not matching it — a self-inflicted lockout.
let serverAuthPostureUngatedTrustedHeaders = false

export function noteServerAuthPosture(auth?: DashboardSystemSettings['auth']): void {
  serverAuthPostureUngatedTrustedHeaders = !!auth
    && normalizeDashboardAuthMode(auth.mode) === 'trusted_headers'
    && !trustedProxyGateConfigured(auth)
}

export type TelemetryBudgetKey =
  | 'max_telemetry_sample_age_ms'
  | 'max_telemetry_generator_lag_ms'
  | 'max_telemetry_transport_lag_ms'
  | 'max_telemetry_controller_fanout_ms'
  | 'max_telemetry_browser_render_lag_ms'

export const TELEMETRY_BUDGET_LABELS: Record<TelemetryBudgetKey, string> = {
  max_telemetry_sample_age_ms: 'Max telemetry sample age',
  max_telemetry_generator_lag_ms: 'Max generator lag',
  max_telemetry_transport_lag_ms: 'Max transport lag',
  max_telemetry_controller_fanout_ms: 'Max controller fan-out',
  max_telemetry_browser_render_lag_ms: 'Max browser render lag',
}

// Derived from DEFAULT_SETTINGS so reset affordances cannot drift from the
// backend defaults mirrored there.
export const TELEMETRY_BUDGET_DEFAULTS: Record<TelemetryBudgetKey, number> = {
  max_telemetry_sample_age_ms: DEFAULT_SETTINGS.thresholds.max_telemetry_sample_age_ms ?? 2000,
  max_telemetry_generator_lag_ms: DEFAULT_SETTINGS.thresholds.max_telemetry_generator_lag_ms ?? 500,
  max_telemetry_transport_lag_ms: DEFAULT_SETTINGS.thresholds.max_telemetry_transport_lag_ms ?? 500,
  max_telemetry_controller_fanout_ms: DEFAULT_SETTINGS.thresholds.max_telemetry_controller_fanout_ms ?? 500,
  max_telemetry_browser_render_lag_ms: DEFAULT_SETTINGS.thresholds.max_telemetry_browser_render_lag_ms ?? 500,
}

export function validateSettings(settings: DashboardSystemSettings): string[] {
  return validateSettingsDetailed(settings).map(issue => issue.message)
}

export function settingsIssuesBySection(issues: SettingsValidationIssue[]): Partial<Record<SettingsSectionId, SettingsValidationIssue[]>> {
  const bySection: Partial<Record<SettingsSectionId, SettingsValidationIssue[]>> = {}
  issues.forEach(issue => {
    const list = bySection[issue.section] ?? []
    list.push(issue)
    bySection[issue.section] = list
  })
  return bySection
}

/**
 * The issues that belong to one Settings destination: same section and, when
 * both say, the same subsection. Several destinations share a section, so
 * section alone would mark every one of them for a single bad field.
 * Browser-only destinations never hold deployment settings.
 */
export function settingsIssuesForDestination(issues: SettingsValidationIssue[], destination: { section: string; subsection?: string; scope?: string }): SettingsValidationIssue[] {
  if (destination.scope === 'browser') return []
  return issues.filter(issue => issue.section === destination.section
    && (!issue.subsection || !destination.subsection || issue.subsection === destination.subsection))
}

export function settingsIssuesForField(issues: SettingsValidationIssue[], field: string): string[] {
  return issues.filter(issue => issue.field === field).map(issue => issue.message)
}

export interface ReservationUsageSummary {
  reserved: number
  configured: number
  free: number
  overbooked: boolean
}

// Counts only always-active global reservations, mirroring the backend rule:
// pool-scoped rows are bounded by their pool envelope, and time-windowed rows
// may legitimately oversubscribe across non-overlapping windows.
export function reservationUsageSummary(settings: DashboardSystemSettings): ReservationUsageSummary {
  const configured = settings.capacity_planning.availability.configured_available_generators
  const reserved = (settings.capacity_planning.reservations ?? []).reduce((total, reservation) => {
    if ((reservation.pool_id ?? '').trim()) return total
    if (!reservation.project_id.trim() || reservation.reserved_generators <= 0) return total
    if (reservation.starts_at || reservation.ends_at) return total
    return total + reservation.reserved_generators
  }, 0)
  return { reserved, configured, free: Math.max(0, configured - reserved), overbooked: reserved > configured }
}

// Pool existence can only be checked against a loaded fleet-pool list; pass
// null when the fleet API is unavailable to skip the check entirely.
export function reservationPoolIssues(reservations: CapacityReservation[] | undefined, pools: FleetPool[] | null): SettingsValidationIssue[] {
  if (!pools) return []
  const known = new Set(pools.map(pool => pool.id))
  const issues: SettingsValidationIssue[] = []
  ;(reservations ?? []).forEach((reservation, index) => {
    const poolID = (reservation.pool_id ?? '').trim()
    if (!poolID || known.has(poolID)) return
    issues.push({
      section: 'capacity',
      subsection: 'reservations',
      field: `capacity_planning.reservations.${index}.pool_id`,
      message: `Reservation ${index + 1} references an unknown fleet pool.`,
    })
  })
  return issues
}

const CAPACITY_DRAFT_FIELDS: Record<string, { label: string; subsection: CapacitySettingsSubsectionId }> = {
  'capacity_planning.headroom_pct': { label: 'Headroom (%)', subsection: 'generator' },
  'capacity_planning.generator.cpu_cores': { label: 'Generator CPU (cores)', subsection: 'generator' },
  'capacity_planning.generator.memory_mb': { label: 'Generator memory (MB)', subsection: 'generator' },
  'capacity_planning.generator.network_mbps': { label: 'Generator network (Mbps)', subsection: 'generator' },
  'capacity_planning.generator.max_vusers': { label: 'Generator max VUsers', subsection: 'generator' },
  'capacity_planning.availability.configured_available_generators': { label: 'Configured available generators', subsection: 'availability' },
  'archive_retention.default_retention_days': { label: 'Default retention days', subsection: 'retention' },
  'archive_retention.har_retention_days': { label: 'HAR retention days', subsection: 'retention' },
}

// Blank or non-numeric drafts for capacity number inputs block saving instead
// of silently snapping the stored value to 0 while the admin is mid-edit.
export function capacityDraftIssues(drafts: Record<string, string>): SettingsValidationIssue[] {
  const issues: SettingsValidationIssue[] = []
  Object.entries(drafts).forEach(([field, draft]) => {
    if (parseOptionalNumber(draft) !== undefined) return
    // Blank means the admin cleared the field; anything else is visible text
    // that failed to parse, which deserves the more accurate message.
    const problem = draft.trim() === '' ? 'is required.' : 'must be a number.'
    const known = CAPACITY_DRAFT_FIELDS[field]
    const reservationMatch = field.match(/^capacity_planning\.reservations\.(\d+)\.reserved_generators$/)
    if (known) {
      issues.push({ section: 'capacity', subsection: known.subsection, field, message: `${known.label} ${problem}` })
    } else if (reservationMatch) {
      issues.push({ section: 'capacity', subsection: 'reservations', field, message: `Reservation ${Number(reservationMatch[1]) + 1} reserved generators ${problem}` })
    } else {
      issues.push({ section: 'capacity', field, message: `A capacity value ${problem}` })
    }
  })
  return issues
}

export interface SettingsSaveErrorGuidance {
  message: string
  focusSection?: SettingsSectionId
  focusCapacitySubsection?: CapacitySettingsSubsectionId
  offerReload?: boolean
}

// Maps backend save rejections to actionable guidance and, where the rejection
// belongs to one tab, which section/subsection to focus.
export function describeSettingsSaveError(status: number | undefined, message: string): SettingsSaveErrorGuidance {
  if (status === 409 || status === 412 || status === 428) {
    return {
      message: 'Settings changed in another session. Reload settings and re-apply your edits.',
      offerReload: true,
    }
  }
  if (message.includes('reservations cannot exceed configured available generators')) {
    return {
      message: 'Save rejected: always-active reservations exceed the configured available generators. Lower a reservation or raise Configured Available Generators.',
      focusSection: 'capacity',
      focusCapacitySubsection: 'reservations',
    }
  }
  if (message.includes('references unknown fleet pool')) {
    return {
      message: `Save rejected: ${message}. Pick an existing fleet pool for the reservation or clear its pool to reserve from the global envelope.`,
      focusSection: 'capacity',
      focusCapacitySubsection: 'reservations',
    }
  }
  if (message.includes('capacity_planning update rejected')) {
    return {
      message: `Save rejected: ${message}. Approved capacity timeslots reserve part of the envelope, so adjust reservations or the configured generator count.`,
      focusSection: 'capacity',
      focusCapacitySubsection: 'reservations',
    }
  }
  return { message }
}

export function validateSettingsDetailed(settings: DashboardSystemSettings): SettingsValidationIssue[] {
  const issues: SettingsValidationIssue[] = []
  const push = (section: SettingsSectionId, subsection: string | undefined, field: string | undefined, message: string) => {
    issues.push({ section, subsection, field, message })
  }
  if (!settings.general.api_endpoint.trim()) {
    push('general', 'platform', 'general.api_endpoint', 'API endpoint is required.')
  }
  if (!Number.isInteger(settings.general.max_concurrent_tests) || settings.general.max_concurrent_tests < 1) {
    push('general', 'platform', 'general.max_concurrent_tests', 'Max concurrent tests must be an integer >= 1.')
  }
  if (!Number.isInteger(settings.general.metrics_interval_seconds) || settings.general.metrics_interval_seconds < 1) {
    push('general', 'platform', 'general.metrics_interval_seconds', 'Metrics interval must be an integer >= 1 second.')
  }
  if (settings.general.usage_snapshot_interval_seconds !== undefined && (!Number.isInteger(settings.general.usage_snapshot_interval_seconds) || settings.general.usage_snapshot_interval_seconds < 0)) {
    push('general', 'usage', 'general.usage_snapshot_interval_seconds', 'Usage snapshot interval must be an integer >= 0 seconds.')
  }
  if (settings.thresholds.max_error_rate_pct !== undefined) {
    if (settings.thresholds.max_error_rate_pct < 0 || settings.thresholds.max_error_rate_pct > 100) {
      push('thresholds', undefined, 'thresholds.max_error_rate_pct', 'Max error rate must be between 0 and 100%.')
    }
  }
  if (settings.thresholds.max_p99_latency_ms !== undefined && settings.thresholds.max_p99_latency_ms <= 0) {
    push('thresholds', undefined, 'thresholds.max_p99_latency_ms', 'Max P99 latency must be greater than 0 ms.')
  }
  if (settings.thresholds.p99_latency_stop_consecutive_samples !== undefined) {
    if (!Number.isInteger(settings.thresholds.p99_latency_stop_consecutive_samples) || settings.thresholds.p99_latency_stop_consecutive_samples < 0) {
      push('thresholds', undefined, 'thresholds.p99_latency_stop_consecutive_samples', 'P99 stop samples must be an integer >= 0.')
    } else if (settings.thresholds.p99_latency_stop_consecutive_samples > 0 && settings.thresholds.max_p99_latency_ms === undefined) {
      // Mirrors the backend write rule: the stop counter needs a P99 level.
      push('thresholds', undefined, 'thresholds.p99_latency_stop_consecutive_samples', 'P99 stop samples requires Max Response Time (P99) to be set.')
    }
  }
  if (settings.thresholds.max_avg_server_processing_ms !== undefined && settings.thresholds.max_avg_server_processing_ms <= 0) {
    push('thresholds', undefined, 'thresholds.max_avg_server_processing_ms', 'Max average server processing must be greater than 0 ms.')
  }
  if (settings.thresholds.max_avg_transfer_ms !== undefined && settings.thresholds.max_avg_transfer_ms <= 0) {
    push('thresholds', undefined, 'thresholds.max_avg_transfer_ms', 'Max average transfer time must be greater than 0 ms.')
  }
  // Telemetry budgets are always enforced server-side: a cleared input would
  // silently come back refilled with the default on save, so require an
  // explicit value. 0 is the documented explicit-disable.
  ;(Object.keys(TELEMETRY_BUDGET_DEFAULTS) as TelemetryBudgetKey[]).forEach(key => {
    const value = settings.thresholds[key]
    if (value === undefined) {
      push('thresholds', undefined, `thresholds.${key}`, `${TELEMETRY_BUDGET_LABELS[key]} is always enforced. Enter a value, 0 to disable, or reset to the default (${TELEMETRY_BUDGET_DEFAULTS[key]} ms).`)
      return
    }
    if (value < 0) {
      push('thresholds', undefined, `thresholds.${key}`, `${TELEMETRY_BUDGET_LABELS[key]} must be >= 0 ms (0 disables this budget).`)
    }
  })
  if (settings.thresholds.fleet_constrained_available_slots !== undefined && (!Number.isInteger(settings.thresholds.fleet_constrained_available_slots) || settings.thresholds.fleet_constrained_available_slots < 0)) {
    push('thresholds', undefined, 'thresholds.fleet_constrained_available_slots', 'Fleet constrained free slots must be an integer >= 0.')
  }
  if (settings.thresholds.fleet_saturated_available_slots !== undefined && (!Number.isInteger(settings.thresholds.fleet_saturated_available_slots) || settings.thresholds.fleet_saturated_available_slots < 0)) {
    push('thresholds', undefined, 'thresholds.fleet_saturated_available_slots', 'Fleet saturated free slots must be an integer >= 0.')
  }
  if (
    settings.thresholds.fleet_constrained_available_slots !== undefined
    && settings.thresholds.fleet_saturated_available_slots !== undefined
    && settings.thresholds.fleet_saturated_available_slots >= settings.thresholds.fleet_constrained_available_slots
  ) {
    // Mirrors the backend write rule: the saturated floor is evaluated first,
    // so an inverted pair makes the constrained tier unreachable.
    push('thresholds', undefined, 'thresholds.fleet_saturated_available_slots', 'Fleet saturated free slots must be less than fleet constrained free slots.')
  }
  if (settings.capacity_planning.headroom_pct < 0 || settings.capacity_planning.headroom_pct >= 100) {
    push('capacity', 'generator', 'capacity_planning.headroom_pct', 'Capacity planning headroom must be between 0 and 100%.')
  }
  if (settings.capacity_planning.generator.cpu_cores <= 0) {
    push('capacity', 'generator', 'capacity_planning.generator.cpu_cores', 'Generator CPU cores must be greater than 0.')
  }
  if (!Number.isInteger(settings.capacity_planning.generator.memory_mb) || settings.capacity_planning.generator.memory_mb < 1) {
    push('capacity', 'generator', 'capacity_planning.generator.memory_mb', 'Generator memory must be an integer >= 1 MB.')
  }
  if (settings.capacity_planning.generator.network_mbps <= 0) {
    push('capacity', 'generator', 'capacity_planning.generator.network_mbps', 'Generator network throughput must be greater than 0 Mbps.')
  }
  if (!Number.isInteger(settings.capacity_planning.generator.max_vusers) || settings.capacity_planning.generator.max_vusers < 1) {
    push('capacity', 'generator', 'capacity_planning.generator.max_vusers', 'Generator max VUsers must be an integer >= 1.')
  }
  if (!Number.isInteger(settings.capacity_planning.availability.configured_available_generators) || settings.capacity_planning.availability.configured_available_generators < 1) {
    push('capacity', 'availability', 'capacity_planning.availability.configured_available_generators', 'Configured available generators must be an integer >= 1.')
  }
  ;(settings.capacity_planning.reservations ?? []).forEach((reservation, index) => {
    if (!reservation.project_id.trim()) {
      push('capacity', 'reservations', `capacity_planning.reservations.${index}.project_id`, 'Each capacity reservation requires a project.')
    }
    if (!Number.isInteger(reservation.reserved_generators) || reservation.reserved_generators < 0) {
      push('capacity', 'reservations', `capacity_planning.reservations.${index}.reserved_generators`, 'Reserved generators must be an integer >= 0.')
    }
    if (reservation.starts_at && reservation.ends_at && new Date(reservation.ends_at) < new Date(reservation.starts_at)) {
      push('capacity', 'reservations', `capacity_planning.reservations.${index}.window`, 'Reservation end must not be before its start.')
    }
  })
  const reservationUsage = reservationUsageSummary(settings)
  if (reservationUsage.overbooked) {
    // Mirrors the backend global-envelope rule so overbooking surfaces before
    // the save round-trips into a 400.
    push('capacity', 'reservations', 'capacity_planning.reservations', `Always-active global reservations (${reservationUsage.reserved}) exceed configured available generators (${reservationUsage.configured}).`)
  }
  if (!Number.isInteger(settings.archive_retention.default_retention_days) || settings.archive_retention.default_retention_days < 1) {
    push('capacity', 'retention', 'archive_retention.default_retention_days', 'Archive retention default days must be an integer >= 1.')
  }
  if (settings.archive_retention.har_retention_days !== undefined &&
      (!Number.isInteger(settings.archive_retention.har_retention_days) || settings.archive_retention.har_retention_days < 1)) {
    push('capacity', 'retention', 'archive_retention.har_retention_days', 'HAR retention days must be an integer >= 1.')
  }
  const blackoutWindowError = validateScheduleWindowPair('Blackout', settings.schedule_defaults.blackout_start_time, settings.schedule_defaults.blackout_end_time)
  if (blackoutWindowError) push('general', 'governance', 'schedule_defaults.blackout', blackoutWindowError)
  const availabilityWindowError = validateScheduleWindowPair('Availability', settings.schedule_defaults.availability_start_time, settings.schedule_defaults.availability_end_time)
  if (availabilityWindowError) push('general', 'governance', 'schedule_defaults.availability', availabilityWindowError)
  if (settings.auth.mode !== 'dev' && settings.auth.mode !== 'disabled' && settings.auth.mode !== 'local' && settings.auth.mode !== 'trusted_headers' && settings.auth.mode !== 'oidc') {
    push('general', 'authentication', 'auth.mode', 'Access control mode must be Dev Bypass, Local Test Login, Trusted Headers, or Native OIDC.')
  }
  if (settings.auth.mode === 'trusted_headers') {
    if (!settings.auth.user_header.trim()) {
      push('general', 'authentication', 'auth.user_header', 'Trusted header auth requires a user header.')
    }
    if (!settings.auth.roles_header.trim() && !settings.auth.project_roles_header.trim()) {
      push('general', 'authentication', 'auth.roles_header', 'Trusted header auth requires a roles header or project roles header.')
    }
    // Mirrors trustedProxyGateConfigured/enforceAuthPosture in pkg/api: without a
    // trust boundary the controller refuses to boot under APEXLOAD_ENV=production,
    // so block the save rather than let the restart brick the platform. The rule
    // is production-only server-side, so an install whose saved posture is already
    // ungated trusted_headers (proof it is not production, see
    // noteServerAuthPosture) keeps saving unrelated settings.
    if (!trustedProxyGateConfigured(settings.auth) && !serverAuthPostureUngatedTrustedHeaders) {
      push('general', 'authentication', 'auth.trusted_proxy_cidrs', 'Trusted header auth requires a trusted proxy CIDR allowlist or a trusted proxy secret environment variable; without one the controller refuses to start in production. Use the source address the controller sees for the gateway — once a gate is set, requests from any other source are rejected.')
    }
    const authURLFields: Array<[string, string | undefined]> = [
      ['Login URL', settings.auth.login_url],
      ['Logout URL', settings.auth.logout_url],
    ]
    authURLFields.forEach(([label, value]) => {
      const error = validateAuthURL(label, value, true)
      if (error) {
        push('general', 'authentication', undefined, error)
      }
    })
  }
  if (settings.auth.mode === 'oidc') {
    if (!settings.auth.issuer_url?.trim()) {
      push('general', 'authentication', 'auth.issuer_url', 'OIDC auth requires an issuer URL.')
    }
    if (!settings.auth.client_id?.trim()) {
      push('general', 'authentication', 'auth.client_id', 'OIDC auth requires a client ID.')
    }
    if (!settings.auth.client_secret_env_var?.trim()) {
      push('general', 'authentication', 'auth.client_secret_env_var', 'OIDC auth requires a client secret environment variable name.')
    }
    if (!settings.auth.redirect_url?.trim()) {
      push('general', 'authentication', 'auth.redirect_url', 'OIDC auth requires a redirect URL.')
    }
    if (!settings.auth.session_secret_env_var?.trim()) {
      push('general', 'authentication', 'auth.session_secret_env_var', 'OIDC auth requires a session secret environment variable name.')
    }
    if (!settings.auth.scopes || settings.auth.scopes.length === 0) {
      push('general', 'authentication', 'auth.scopes', 'OIDC auth requires at least one scope.')
    }
    // Each URL error names its field, so it is shown on (and announced for) that input.
    const oidcURLFields: Array<[string, string, string | undefined, boolean]> = [
      ['Issuer URL', 'auth.issuer_url', settings.auth.issuer_url, false],
      ['Redirect URL', 'auth.redirect_url', settings.auth.redirect_url, false],
      ['Post-Logout Redirect URL', 'auth.logout_url', settings.auth.logout_url, true],
    ]
    oidcURLFields.forEach(([label, field, value, allowAbsolutePath]) => {
      const error = validateAuthURL(label, value, allowAbsolutePath)
      if (error) {
        push('general', 'authentication', field, error)
      }
    })
  }
  if (settings.prometheus.enabled && !settings.prometheus.base_url.trim()) {
    push('integrations', 'prometheus', 'prometheus.base_url', 'Prometheus server URL is required when Prometheus graphs are enabled.')
  }
  if (settings.prometheus.base_url.trim()) {
    try {
      const parsed = new URL(settings.prometheus.base_url)
      if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
        push('integrations', 'prometheus', 'prometheus.base_url', 'Prometheus server URL must use http or https.')
      }
    } catch {
      push('integrations', 'prometheus', 'prometheus.base_url', 'Prometheus server URL must be a valid absolute URL.')
    }
  }
  if (!Number.isInteger(settings.prometheus.timeout_seconds) || settings.prometheus.timeout_seconds < 1 || settings.prometheus.timeout_seconds > 60) {
    push('integrations', 'prometheus', 'prometheus.timeout_seconds', 'Prometheus timeout must be an integer between 1 and 60 seconds.')
  }
  if (settings.grafana?.enabled && (!settings.grafana.base_url.trim() || !settings.grafana.service_account_token_env_var.trim())) {
    push('integrations', 'dashboards', 'grafana.base_url', 'Grafana proxy requires a server URL and service-account token environment variable.')
  }
  if (settings.grafana && (!Number.isInteger(settings.grafana.timeout_seconds) || settings.grafana.timeout_seconds < 1 || settings.grafana.timeout_seconds > 60)) {
    push('integrations', 'dashboards', 'grafana.timeout_seconds', 'Grafana timeout must be an integer between 1 and 60 seconds.')
  }
  if (settings.powerbi?.enabled) {
    if (!settings.powerbi.tenant_id?.trim() || !settings.powerbi.client_id?.trim() || !settings.powerbi.client_secret_env_var?.trim() || !settings.powerbi.workspace_id?.trim()) {
      push('integrations', 'powerbi', 'powerbi.credentials', 'Power BI publishing requires a tenant ID, client ID, client secret environment variable, and workspace ID.')
    }
    const workspaceId = settings.powerbi.workspace_id?.trim() || ''
    if (workspaceId && !/^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/.test(workspaceId)) {
      push('integrations', 'powerbi', 'powerbi.workspace_id', 'Power BI workspace ID must be a GUID.')
    }
  }
  if (settings.powerbi) {
    const timeout = settings.powerbi.timeout_seconds ?? 30
    if (!Number.isInteger(timeout) || timeout < 1 || timeout > 60) {
      push('integrations', 'powerbi', 'powerbi.timeout_seconds', 'Power BI timeout must be an integer between 1 and 60 seconds.')
    }
  }
  const clientAppAvailability = settings.client_app?.availability || 'disabled'
  if (clientAppAvailability !== 'enabled' && clientAppAvailability !== 'disabled') {
    push('general', 'platform', 'client_app.availability', 'Desktop client availability must be enabled or disabled.')
  }
  settings.notifications.webhooks.forEach((webhook, index) => {
    const label = `Notification webhook ${index + 1}`
    if (!webhook.url.trim()) {
      push('integrations', 'notifications', `notifications.webhooks.${index}.url`, `${label} requires a destination URL.`)
    } else {
      try {
        const parsed = new URL(webhook.url)
        if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
          push('integrations', 'notifications', `notifications.webhooks.${index}.url`, `${label} URL must use http or https.`)
        }
      } catch {
        push('integrations', 'notifications', `notifications.webhooks.${index}.url`, `${label} URL must be a valid absolute URL.`)
      }
    }
    if (webhook.format !== 'slack' && webhook.format !== 'teams' && webhook.format !== 'dynatrace' && webhook.format !== 'grafana') {
      push('integrations', 'notifications', `notifications.webhooks.${index}.format`, `${label} format must be Slack, Microsoft Teams, Dynatrace or Grafana.`)
    }
    if (webhook.format === 'grafana' && !webhook.api_token?.trim() && !webhook.has_api_token) {
      push('integrations', 'notifications', `notifications.webhooks.${index}.api_token`, `${label} requires a Grafana service account token.`)
    }
    if (webhook.format === 'dynatrace') {
      if (!webhook.api_token?.trim() && !webhook.has_api_token) {
        push('integrations', 'notifications', `notifications.webhooks.${index}.api_token`, `${label} requires a Dynatrace API token.`)
      }
      if (webhook.timeout_minutes !== undefined && (!Number.isFinite(webhook.timeout_minutes) || webhook.timeout_minutes < 0 || webhook.timeout_minutes > 360)) {
        push('integrations', 'notifications', `notifications.webhooks.${index}.timeout_minutes`, `${label} Dynatrace timeout must be between 0 and 360 minutes.`)
      }
    }
  })
  return issues
}

export function normalizeDashboardSystemSettings(settings: DashboardSystemSettings): DashboardSystemSettings {
  // Only server payloads (initial load and save responses) reach this function,
  // so it is the one place that sees the persisted auth posture.
  noteServerAuthPosture(settings.auth)
  return {
    revision: typeof settings.revision === 'string' ? settings.revision : '',
    general: {
      ...DEFAULT_SETTINGS.general,
      ...settings.general,
    },
    thresholds: {
      ...DEFAULT_SETTINGS.thresholds,
      ...settings.thresholds,
    },
    capacity_planning: {
      ...DEFAULT_SETTINGS.capacity_planning,
      ...settings.capacity_planning,
      generator: {
        ...DEFAULT_SETTINGS.capacity_planning.generator,
        ...settings.capacity_planning.generator,
      },
      availability: {
        ...DEFAULT_SETTINGS.capacity_planning.availability,
        ...settings.capacity_planning.availability,
      },
      reservations: settings.capacity_planning.reservations ? [...settings.capacity_planning.reservations] : [],
    },
    archive_retention: {
      ...DEFAULT_SETTINGS.archive_retention,
      ...settings.archive_retention,
      policies: settings.archive_retention?.policies ? [...settings.archive_retention.policies] : [],
    },
    schedule_defaults: {
      ...DEFAULT_SETTINGS.schedule_defaults,
      ...settings.schedule_defaults,
    },
    notifications: {
	      webhooks: (settings.notifications?.webhooks || []).map(webhook => ({
	        ...webhook,
	        events: webhook.events ? [...webhook.events] : [],
	        has_api_token: Boolean(webhook.has_api_token),
	      })),
	    },
    auth: {
      ...DEFAULT_SETTINGS.auth,
      ...settings.auth,
	      scopes: settings.auth?.scopes ? [...settings.auth.scopes] : [...(DEFAULT_SETTINGS.auth.scopes || [])],
	      trusted_proxy_cidrs: settings.auth?.trusted_proxy_cidrs ? [...settings.auth.trusted_proxy_cidrs] : [],
      mode: normalizeDashboardAuthMode(settings.auth?.mode),
    },
    github: {
      ...DEFAULT_SETTINGS.github,
      ...settings.github,
      scopes: settings.github?.scopes ? [...settings.github.scopes] : [...DEFAULT_SETTINGS.github.scopes],
      app_id: Number(settings.github?.app_id || 0),
      checks_enabled: Boolean(settings.github?.checks_enabled),
    },
    prometheus: {
      ...DEFAULT_SETTINGS.prometheus,
      ...settings.prometheus,
      enabled: Boolean(settings.prometheus?.enabled),
      base_url: settings.prometheus?.base_url || '',
      timeout_seconds: Number(settings.prometheus?.timeout_seconds || DEFAULT_SETTINGS.prometheus.timeout_seconds),
      bearer_token_env_var: settings.prometheus?.bearer_token_env_var || '',
      allow_label_values: Boolean(settings.prometheus?.allow_label_values),
    },
    grafana: {
      ...DEFAULT_SETTINGS.grafana!,
      ...settings.grafana,
      enabled: Boolean(settings.grafana?.enabled),
      base_url: settings.grafana?.base_url || '',
      service_account_token_env_var: settings.grafana?.service_account_token_env_var || '',
      timeout_seconds: Number(settings.grafana?.timeout_seconds || DEFAULT_SETTINGS.grafana!.timeout_seconds),
    },
    // Dropping this key would silently wipe the stored Power BI configuration
    // on the next settings save (the backend zero value validates clean).
    powerbi: {
      ...DEFAULT_SETTINGS.powerbi!,
      ...settings.powerbi,
      enabled: Boolean(settings.powerbi?.enabled),
      timeout_seconds: Number(settings.powerbi?.timeout_seconds || DEFAULT_SETTINGS.powerbi!.timeout_seconds),
    },
    client_app: {
      ...DEFAULT_SETTINGS.client_app,
      ...settings.client_app,
      availability: settings.client_app?.availability === 'enabled' ? 'enabled' : 'disabled',
    },
    client_downloads: {
      ...DEFAULT_SETTINGS.client_downloads,
      ...settings.client_downloads,
      // Default to enabled; only an explicit false disables (matches the
      // server-side default and migration behaviour).
      enabled: settings.client_downloads?.enabled !== false,
    },
    script_imports: {
      ...DEFAULT_SETTINGS.script_imports,
      ...settings.script_imports,
      allow_source_secret_migration: settings.script_imports?.allow_source_secret_migration === true,
    },
  }
}

export function deriveLiveGeneratorPreset(inventory: SystemInventoryResponse) {
  if (inventory.execution_mode !== 'distributed') {
    throw new Error('Live generator capacity is only available when the controller is running in distributed mode.')
  }

  const connectedGenerators = inventory.generators.filter(generator => generator.status === 'connected')
  if (connectedGenerators.length === 0) {
    throw new Error('No connected load generator pods are reporting live capacity right now.')
  }

  const totalCapacities = connectedGenerators
    .map(generator => generator.max_vusers || (generator.available_capacity + generator.active_vusers))
    .filter(capacity => capacity > 0)
  const cpuLimits = connectedGenerators
    .map(generator => generator.cpu_limit_cores)
    .filter(limit => limit > 0)
  const memoryLimits = connectedGenerators
    .map(generator => generator.memory_limit_bytes)
    .filter(limit => limit > 0)

  if (totalCapacities.length === 0) {
    throw new Error('Connected generator pods did not report a usable VUser capacity snapshot.')
  }

  return {
    configuredAvailableGenerators: connectedGenerators.length,
    maxVUsers: Math.max(1, Math.floor(Math.min(...totalCapacities))),
    cpuCores: cpuLimits.length > 0 ? Math.min(...cpuLimits) : undefined,
    memoryMB: memoryLimits.length > 0 ? Math.max(1, Math.floor(Math.min(...memoryLimits) / (1024 * 1024))) : undefined,
    generatorCount: connectedGenerators.length,
  }
}

export function usesDefaultGeneratorCapacity(settings: DashboardSystemSettings) {
  const generator = settings.capacity_planning.generator
  const availability = settings.capacity_planning.availability
  return (
    generator.cpu_cores === DEFAULT_SETTINGS.capacity_planning.generator.cpu_cores &&
    generator.memory_mb === DEFAULT_SETTINGS.capacity_planning.generator.memory_mb &&
    generator.network_mbps === DEFAULT_SETTINGS.capacity_planning.generator.network_mbps &&
    generator.max_vusers === DEFAULT_SETTINGS.capacity_planning.generator.max_vusers &&
    availability.configured_available_generators === DEFAULT_SETTINGS.capacity_planning.availability.configured_available_generators
  )
}

export function applyLiveGeneratorPresetToSettings(settings: DashboardSystemSettings, preset: ReturnType<typeof deriveLiveGeneratorPreset>): DashboardSystemSettings {
  return {
    ...settings,
    capacity_planning: {
      ...settings.capacity_planning,
      generator: {
        ...settings.capacity_planning.generator,
        cpu_cores: preset.cpuCores ?? settings.capacity_planning.generator.cpu_cores,
        memory_mb: preset.memoryMB ?? settings.capacity_planning.generator.memory_mb,
        max_vusers: preset.maxVUsers,
      },
      availability: {
        ...settings.capacity_planning.availability,
        configured_available_generators: preset.configuredAvailableGenerators,
      },
    },
  }
}
