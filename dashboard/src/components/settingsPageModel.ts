// Settings domain model: default settings, generator/OIDC presets, and the
// pure validators + draft/normalize/derive builders for the settings form.
// Extracted from SettingsPage.tsx; no React or component-state dependencies.

import type {
  DashboardSystemSettings,
  GitHubProjectPolicy,
  PoolProjectQuota,
  RedactionRule,
  SystemInventoryResponse,
} from '../types'
import {
  normalizeDashboardAuthMode,
  normalizeProjectRedactionRules,
  parseOptionalWholeNumber,
  validateAuthURL,
  type DraftRedactionRule,
} from './settingsPageFormatters'

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
  },
  client_app: {
    availability: 'disabled',
  },
  client_downloads: {
    enabled: true,
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
) {
  const settings: {
    redaction_rules?: RedactionRule[]
    max_concurrent_tests?: number
    max_vusers?: number
    max_vusers_per_test?: number
    max_test_duration_minutes?: number
    github?: GitHubProjectPolicy
    pool_quotas?: Record<string, PoolProjectQuota>
  } = {
    redaction_rules: normalizeProjectRedactionRules(rules),
  }
  if (poolQuotas && Object.keys(poolQuotas).length > 0) {
    settings.pool_quotas = poolQuotas
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

export function validateSettings(settings: DashboardSystemSettings): string[] {
  const errors: string[] = []
  if (!settings.general.api_endpoint.trim()) {
    errors.push('API endpoint is required.')
  }
  if (!Number.isInteger(settings.general.max_concurrent_tests) || settings.general.max_concurrent_tests < 1) {
    errors.push('Max concurrent tests must be an integer >= 1.')
  }
  if (!Number.isInteger(settings.general.metrics_interval_seconds) || settings.general.metrics_interval_seconds < 1) {
    errors.push('Metrics interval must be an integer >= 1 second.')
  }
  if (settings.general.usage_snapshot_interval_seconds !== undefined && (!Number.isInteger(settings.general.usage_snapshot_interval_seconds) || settings.general.usage_snapshot_interval_seconds < 0)) {
    errors.push('Usage snapshot interval must be an integer >= 0 seconds.')
  }
  if (settings.thresholds.max_error_rate_pct !== undefined) {
    if (settings.thresholds.max_error_rate_pct < 0 || settings.thresholds.max_error_rate_pct > 100) {
      errors.push('Max error rate must be between 0 and 100%.')
    }
  }
  if (settings.thresholds.max_p99_latency_ms !== undefined && settings.thresholds.max_p99_latency_ms <= 0) {
    errors.push('Max P99 latency must be greater than 0 ms.')
  }
  if (settings.thresholds.max_avg_server_processing_ms !== undefined && settings.thresholds.max_avg_server_processing_ms <= 0) {
    errors.push('Max average server processing must be greater than 0 ms.')
  }
  if (settings.thresholds.max_avg_transfer_ms !== undefined && settings.thresholds.max_avg_transfer_ms <= 0) {
    errors.push('Max average transfer time must be greater than 0 ms.')
  }
  if (settings.thresholds.max_telemetry_sample_age_ms !== undefined && settings.thresholds.max_telemetry_sample_age_ms <= 0) {
    errors.push('Max telemetry sample age must be greater than 0 ms.')
  }
  if (settings.thresholds.max_telemetry_generator_lag_ms !== undefined && settings.thresholds.max_telemetry_generator_lag_ms <= 0) {
    errors.push('Max telemetry generator lag must be greater than 0 ms.')
  }
  if (settings.thresholds.max_telemetry_transport_lag_ms !== undefined && settings.thresholds.max_telemetry_transport_lag_ms <= 0) {
    errors.push('Max telemetry transport lag must be greater than 0 ms.')
  }
  if (settings.thresholds.max_telemetry_controller_fanout_ms !== undefined && settings.thresholds.max_telemetry_controller_fanout_ms <= 0) {
    errors.push('Max telemetry controller fan-out must be greater than 0 ms.')
  }
  if (settings.thresholds.max_telemetry_browser_render_lag_ms !== undefined && settings.thresholds.max_telemetry_browser_render_lag_ms <= 0) {
    errors.push('Max telemetry browser render lag must be greater than 0 ms.')
  }
  if (settings.thresholds.fleet_constrained_available_slots !== undefined && (!Number.isInteger(settings.thresholds.fleet_constrained_available_slots) || settings.thresholds.fleet_constrained_available_slots < 0)) {
    errors.push('Fleet constrained free slots must be an integer >= 0.')
  }
  if (settings.thresholds.fleet_saturated_available_slots !== undefined && (!Number.isInteger(settings.thresholds.fleet_saturated_available_slots) || settings.thresholds.fleet_saturated_available_slots < 0)) {
    errors.push('Fleet saturated free slots must be an integer >= 0.')
  }
  if (settings.capacity_planning.headroom_pct < 0 || settings.capacity_planning.headroom_pct >= 100) {
    errors.push('Capacity planning headroom must be between 0 and 100%.')
  }
  if (settings.capacity_planning.generator.cpu_cores <= 0) {
    errors.push('Generator CPU cores must be greater than 0.')
  }
  if (!Number.isInteger(settings.capacity_planning.generator.memory_mb) || settings.capacity_planning.generator.memory_mb < 1) {
    errors.push('Generator memory must be an integer >= 1 MB.')
  }
  if (settings.capacity_planning.generator.network_mbps <= 0) {
    errors.push('Generator network throughput must be greater than 0 Mbps.')
  }
  if (!Number.isInteger(settings.capacity_planning.generator.max_vusers) || settings.capacity_planning.generator.max_vusers < 1) {
    errors.push('Generator max VUsers must be an integer >= 1.')
  }
  if (!Number.isInteger(settings.capacity_planning.availability.configured_available_generators) || settings.capacity_planning.availability.configured_available_generators < 1) {
    errors.push('Configured available generators must be an integer >= 1.')
  }
  for (const reservation of settings.capacity_planning.reservations ?? []) {
    if (!reservation.project_id.trim()) {
      errors.push('Each capacity reservation requires a project.')
    }
    if (!Number.isInteger(reservation.reserved_generators) || reservation.reserved_generators < 0) {
      errors.push('Reserved generators must be an integer >= 0.')
    }
    if (reservation.starts_at && reservation.ends_at && new Date(reservation.ends_at) < new Date(reservation.starts_at)) {
      errors.push('Reservation end must not be before its start.')
    }
  }
  if (!Number.isInteger(settings.archive_retention.default_retention_days) || settings.archive_retention.default_retention_days < 1) {
    errors.push('Archive retention default days must be an integer >= 1.')
  }
  const blackoutWindowError = validateScheduleWindowPair('Blackout', settings.schedule_defaults.blackout_start_time, settings.schedule_defaults.blackout_end_time)
  if (blackoutWindowError) errors.push(blackoutWindowError)
  const availabilityWindowError = validateScheduleWindowPair('Availability', settings.schedule_defaults.availability_start_time, settings.schedule_defaults.availability_end_time)
  if (availabilityWindowError) errors.push(availabilityWindowError)
  if (settings.auth.mode !== 'dev' && settings.auth.mode !== 'disabled' && settings.auth.mode !== 'trusted_headers' && settings.auth.mode !== 'oidc') {
    errors.push('Access control mode must be Dev Bypass, Trusted Headers, or Native OIDC.')
  }
  if (settings.auth.mode === 'trusted_headers') {
    if (!settings.auth.user_header.trim()) {
      errors.push('Trusted header auth requires a user header.')
    }
    if (!settings.auth.roles_header.trim() && !settings.auth.project_roles_header.trim()) {
      errors.push('Trusted header auth requires a roles header or project roles header.')
    }
    const authURLFields: Array<[string, string | undefined]> = [
      ['Login URL', settings.auth.login_url],
      ['Logout URL', settings.auth.logout_url],
    ]
    authURLFields.forEach(([label, value]) => {
      const error = validateAuthURL(label, value, true)
      if (error) {
        errors.push(error)
      }
    })
  }
  if (settings.auth.mode === 'oidc') {
    if (!settings.auth.issuer_url?.trim()) {
      errors.push('OIDC auth requires an issuer URL.')
    }
    if (!settings.auth.client_id?.trim()) {
      errors.push('OIDC auth requires a client ID.')
    }
    if (!settings.auth.client_secret_env_var?.trim()) {
      errors.push('OIDC auth requires a client secret environment variable name.')
    }
    if (!settings.auth.redirect_url?.trim()) {
      errors.push('OIDC auth requires a redirect URL.')
    }
    if (!settings.auth.session_secret_env_var?.trim()) {
      errors.push('OIDC auth requires a session secret environment variable name.')
    }
    if (!settings.auth.scopes || settings.auth.scopes.length === 0) {
      errors.push('OIDC auth requires at least one scope.')
    }
    const oidcURLFields: Array<[string, string | undefined, boolean]> = [
      ['Issuer URL', settings.auth.issuer_url, false],
      ['Redirect URL', settings.auth.redirect_url, false],
      ['Post-Logout Redirect URL', settings.auth.logout_url, true],
    ]
    oidcURLFields.forEach(([label, value, allowAbsolutePath]) => {
      const error = validateAuthURL(label, value, allowAbsolutePath)
      if (error) {
        errors.push(error)
      }
    })
  }
  if (settings.prometheus.enabled && !settings.prometheus.base_url.trim()) {
    errors.push('Prometheus server URL is required when Prometheus graphs are enabled.')
  }
  if (settings.prometheus.base_url.trim()) {
    try {
      const parsed = new URL(settings.prometheus.base_url)
      if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
        errors.push('Prometheus server URL must use http or https.')
      }
    } catch {
      errors.push('Prometheus server URL must be a valid absolute URL.')
    }
  }
  if (!Number.isInteger(settings.prometheus.timeout_seconds) || settings.prometheus.timeout_seconds < 1 || settings.prometheus.timeout_seconds > 60) {
    errors.push('Prometheus timeout must be an integer between 1 and 60 seconds.')
  }
  const clientAppAvailability = settings.client_app?.availability || 'disabled'
  if (clientAppAvailability !== 'enabled' && clientAppAvailability !== 'disabled') {
    errors.push('Desktop client availability must be enabled or disabled.')
  }
  settings.notifications.webhooks.forEach((webhook, index) => {
    const label = `Notification webhook ${index + 1}`
    if (!webhook.url.trim()) {
      errors.push(`${label} requires a destination URL.`)
    } else {
      try {
        const parsed = new URL(webhook.url)
        if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
          errors.push(`${label} URL must use http or https.`)
        }
      } catch {
        errors.push(`${label} URL must be a valid absolute URL.`)
      }
    }
    if (webhook.format !== 'slack' && webhook.format !== 'teams' && webhook.format !== 'dynatrace') {
      errors.push(`${label} format must be Slack, Microsoft Teams, or Dynatrace.`)
    }
    if (webhook.format === 'dynatrace') {
      if (!webhook.api_token?.trim()) {
        errors.push(`${label} requires a Dynatrace API token.`)
      }
      if (webhook.timeout_minutes !== undefined && (!Number.isFinite(webhook.timeout_minutes) || webhook.timeout_minutes < 0 || webhook.timeout_minutes > 360)) {
        errors.push(`${label} Dynatrace timeout must be between 0 and 360 minutes.`)
      }
    }
  })
  return errors
}

export function normalizeDashboardSystemSettings(settings: DashboardSystemSettings): DashboardSystemSettings {
  return {
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
      })),
    },
    auth: {
      ...DEFAULT_SETTINGS.auth,
      ...settings.auth,
	      scopes: settings.auth?.scopes ? [...settings.auth.scopes] : [...(DEFAULT_SETTINGS.auth.scopes || [])],
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
