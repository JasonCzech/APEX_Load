import { describe, expect, it } from 'vitest'
import {
  capacityDraftIssues,
  DEFAULT_SETTINGS,
  describeSettingsSaveError,
  normalizeDashboardSystemSettings,
  reservationPoolIssues,
  reservationUsageSummary,
  settingsIssuesBySection,
  settingsIssuesForField,
  splitListDraft,
  usesDefaultGeneratorCapacity,
  validateOptionalPositiveInteger,
  validateProjectQuotaField,
  validateSettings,
  validateSettingsDetailed,
} from './settingsPageModel'

describe('settingsPageModel', () => {
  it('splitListDraft trims comma-separated values and drops blanks', () => {
    expect(splitListDraft('a, b ,,  ,d')).toEqual(['a', 'b', 'd'])
    expect(splitListDraft('   ')).toEqual([])
  })

  it('validateOptionalPositiveInteger accepts blank and positives, rejects others', () => {
    expect(validateOptionalPositiveInteger('Limit', '')).toBe('')
    expect(validateOptionalPositiveInteger('Limit', '5')).toBe('')
    expect(validateOptionalPositiveInteger('Limit', '0')).not.toBe('')
    expect(validateOptionalPositiveInteger('Limit', '-2')).not.toBe('')
    expect(validateOptionalPositiveInteger('Limit', 'abc')).not.toBe('')
  })

  it('validateProjectQuotaField allows blank, rejects negative/non-numeric', () => {
    expect(validateProjectQuotaField('Quota', '')).toBe('')
    expect(validateProjectQuotaField('Quota', '10')).toBe('')
    expect(validateProjectQuotaField('Quota', '-1')).not.toBe('')
    expect(validateProjectQuotaField('Quota', 'x')).not.toBe('')
  })

  it('validateSettings returns no errors for the default settings', () => {
    expect(validateSettings(DEFAULT_SETTINGS)).toEqual([])
  })

  it('normalizes missing client app settings to disabled promotion', () => {
    const normalized = normalizeDashboardSystemSettings({
      ...DEFAULT_SETTINGS,
      client_app: undefined as unknown as typeof DEFAULT_SETTINGS.client_app,
    })
    expect(normalized.client_app.availability).toBe('disabled')
  })

  it('defaults client software downloads to enabled', () => {
    expect(DEFAULT_SETTINGS.client_downloads.enabled).toBe(true)
  })

  it('normalizes missing client downloads settings to enabled', () => {
    const normalized = normalizeDashboardSystemSettings({
      ...DEFAULT_SETTINGS,
      client_downloads: undefined as unknown as typeof DEFAULT_SETTINGS.client_downloads,
    })
    expect(normalized.client_downloads.enabled).toBe(true)
  })

  it('preserves an explicit client downloads disable', () => {
    const normalized = normalizeDashboardSystemSettings({
      ...DEFAULT_SETTINGS,
      client_downloads: { enabled: false },
    })
    expect(normalized.client_downloads.enabled).toBe(false)
  })

  it('usesDefaultGeneratorCapacity is true for the defaults', () => {
    expect(usesDefaultGeneratorCapacity(DEFAULT_SETTINGS)).toBe(true)
  })

  it('normalize preserves a configured powerbi section (dropping it would wipe the stored config on save)', () => {
    const normalized = normalizeDashboardSystemSettings({
      ...DEFAULT_SETTINGS,
      powerbi: {
        enabled: true,
        tenant_id: 'tenant-1',
        client_id: 'client-1',
        client_secret_env_var: 'APEXLOAD_POWERBI_CLIENT_SECRET',
        workspace_id: '11111111-2222-3333-4444-555555555555',
        dashboard_base_url: 'https://apexload.example.com',
      },
    })
    expect(normalized.powerbi?.enabled).toBe(true)
    expect(normalized.powerbi?.tenant_id).toBe('tenant-1')
    expect(normalized.powerbi?.workspace_id).toBe('11111111-2222-3333-4444-555555555555')
    expect(normalized.powerbi?.timeout_seconds).toBe(30)
    // Fields absent from DEFAULT_SETTINGS.powerbi must still round-trip; a
    // refactor to an explicit field-list normalize would wipe them silently.
    expect(normalized.powerbi?.dashboard_base_url).toBe('https://apexload.example.com')
  })

  it('normalize tolerates a missing powerbi section', () => {
    const normalized = normalizeDashboardSystemSettings({
      ...DEFAULT_SETTINGS,
      powerbi: undefined,
    })
    expect(normalized.powerbi?.enabled).toBe(false)
  })

  it('validates enabled powerbi settings', () => {
    const invalid = validateSettings({
      ...DEFAULT_SETTINGS,
      powerbi: { ...DEFAULT_SETTINGS.powerbi!, enabled: true, workspace_id: 'not-a-guid' },
    })
    expect(invalid.some(error => error.includes('Power BI publishing requires'))).toBe(true)
    expect(invalid.some(error => error.includes('workspace ID must be a GUID'))).toBe(true)

    const badTimeout = validateSettings({
      ...DEFAULT_SETTINGS,
      powerbi: { ...DEFAULT_SETTINGS.powerbi!, timeout_seconds: -5 },
    })
    expect(badTimeout.some(error => error.includes('Power BI timeout'))).toBe(true)

    const valid = validateSettings({
      ...DEFAULT_SETTINGS,
      powerbi: {
        ...DEFAULT_SETTINGS.powerbi!,
        enabled: true,
        tenant_id: 'tenant-1',
        client_id: 'client-1',
        client_secret_env_var: 'APEXLOAD_POWERBI_CLIENT_SECRET',
        workspace_id: '11111111-2222-3333-4444-555555555555',
      },
    })
    expect(valid).toEqual([])
  })

  it('validateSettingsDetailed maps threshold issues to the thresholds section with field keys', () => {
    const issues = validateSettingsDetailed({
      ...DEFAULT_SETTINGS,
      thresholds: { ...DEFAULT_SETTINGS.thresholds, max_p99_latency_ms: -5, max_error_rate_pct: 150 },
    })
    const thresholdIssues = issues.filter(issue => issue.section === 'thresholds')
    expect(thresholdIssues.map(issue => issue.field)).toEqual(
      expect.arrayContaining(['thresholds.max_p99_latency_ms', 'thresholds.max_error_rate_pct']),
    )
    expect(settingsIssuesForField(issues, 'thresholds.max_p99_latency_ms')).toEqual([
      'Max P99 latency must be greater than 0 ms.',
    ])
    expect(settingsIssuesBySection(issues).thresholds?.length).toBe(2)
  })

  it('validateSettingsDetailed maps capacity and retention issues to capacity subsections', () => {
    const issues = validateSettingsDetailed({
      ...DEFAULT_SETTINGS,
      capacity_planning: {
        ...DEFAULT_SETTINGS.capacity_planning,
        generator: { ...DEFAULT_SETTINGS.capacity_planning.generator, cpu_cores: 0 },
        availability: { source: 'configured', configured_available_generators: 0 },
      },
      archive_retention: { ...DEFAULT_SETTINGS.archive_retention, default_retention_days: 0 },
    })
    const capacityIssues = issues.filter(issue => issue.section === 'capacity')
    expect(capacityIssues.map(issue => issue.subsection)).toEqual(
      expect.arrayContaining(['generator', 'availability', 'retention']),
    )
  })

  it('validateSettings keeps the flat message list in sync with detailed issues', () => {
    const settings = {
      ...DEFAULT_SETTINGS,
      thresholds: { ...DEFAULT_SETTINGS.thresholds, max_p99_latency_ms: -5 },
    }
    expect(validateSettings(settings)).toEqual(validateSettingsDetailed(settings).map(issue => issue.message))
  })

  it('flags fleet saturated slots >= constrained slots', () => {
    const issues = validateSettingsDetailed({
      ...DEFAULT_SETTINGS,
      thresholds: {
        ...DEFAULT_SETTINGS.thresholds,
        fleet_constrained_available_slots: 5,
        fleet_saturated_available_slots: 5,
      },
    })
    expect(settingsIssuesForField(issues, 'thresholds.fleet_saturated_available_slots')).toEqual([
      'Fleet saturated free slots must be less than fleet constrained free slots.',
    ])
  })

  it('flags cleared telemetry budgets and accepts explicit 0 as disabled', () => {
    const cleared = validateSettingsDetailed({
      ...DEFAULT_SETTINGS,
      thresholds: { ...DEFAULT_SETTINGS.thresholds, max_telemetry_sample_age_ms: undefined },
    })
    expect(settingsIssuesForField(cleared, 'thresholds.max_telemetry_sample_age_ms')[0]).toContain('always enforced')

    const disabled = validateSettingsDetailed({
      ...DEFAULT_SETTINGS,
      thresholds: { ...DEFAULT_SETTINGS.thresholds, max_telemetry_sample_age_ms: 0 },
    })
    expect(settingsIssuesForField(disabled, 'thresholds.max_telemetry_sample_age_ms')).toEqual([])

    const negative = validateSettingsDetailed({
      ...DEFAULT_SETTINGS,
      thresholds: { ...DEFAULT_SETTINGS.thresholds, max_telemetry_sample_age_ms: -1 },
    })
    expect(settingsIssuesForField(negative, 'thresholds.max_telemetry_sample_age_ms')[0]).toContain('>= 0 ms')
  })

  it('validates the p99 stop sample count and its dependency on the p99 threshold', () => {
    const missingLevel = validateSettingsDetailed({
      ...DEFAULT_SETTINGS,
      thresholds: { ...DEFAULT_SETTINGS.thresholds, p99_latency_stop_consecutive_samples: 3 },
    })
    expect(settingsIssuesForField(missingLevel, 'thresholds.p99_latency_stop_consecutive_samples')[0]).toContain('requires Max Response Time (P99)')

    const valid = validateSettingsDetailed({
      ...DEFAULT_SETTINGS,
      thresholds: { ...DEFAULT_SETTINGS.thresholds, max_p99_latency_ms: 1500, p99_latency_stop_consecutive_samples: 3 },
    })
    expect(settingsIssuesForField(valid, 'thresholds.p99_latency_stop_consecutive_samples')).toEqual([])

    const fractional = validateSettingsDetailed({
      ...DEFAULT_SETTINGS,
      thresholds: { ...DEFAULT_SETTINGS.thresholds, max_p99_latency_ms: 1500, p99_latency_stop_consecutive_samples: 2.5 },
    })
    expect(settingsIssuesForField(fractional, 'thresholds.p99_latency_stop_consecutive_samples')[0]).toContain('integer')
  })

  it('capacityDraftIssues flags blank drafts as required and non-numeric drafts as not numbers', () => {
    const issues = capacityDraftIssues({
      'capacity_planning.generator.memory_mb': '',
      'capacity_planning.availability.configured_available_generators': 'abc',
      'capacity_planning.reservations.1.reserved_generators': '',
      'capacity_planning.headroom_pct': '15',
    })
    expect(issues.map(issue => issue.message)).toEqual(
      expect.arrayContaining([
        'Generator memory (MB) is required.',
        'Configured available generators must be a number.',
        'Reservation 2 reserved generators is required.',
      ]),
    )
    expect(issues.some(issue => issue.field === 'capacity_planning.headroom_pct')).toBe(false)
  })

  it('reservationUsageSummary counts only always-active global reservations', () => {
    const summary = reservationUsageSummary({
      ...DEFAULT_SETTINGS,
      capacity_planning: {
        ...DEFAULT_SETTINGS.capacity_planning,
        availability: { source: 'configured', configured_available_generators: 6 },
        reservations: [
          { project_id: 'checkout', reserved_generators: 2 },
          { project_id: 'payments', reserved_generators: 3, pool_id: 'pool-1' },
          { project_id: 'reports', reserved_generators: 4, starts_at: '2026-08-01T00:00:00Z' },
          { project_id: '', reserved_generators: 5 },
          { project_id: 'batch', reserved_generators: 0 },
        ],
      },
    })
    expect(summary).toEqual({ reserved: 2, configured: 6, free: 4, overbooked: false })
  })

  it('validateSettingsDetailed flags global reservation overbooking', () => {
    const issues = validateSettingsDetailed({
      ...DEFAULT_SETTINGS,
      capacity_planning: {
        ...DEFAULT_SETTINGS.capacity_planning,
        availability: { source: 'configured', configured_available_generators: 2 },
        reservations: [
          { project_id: 'checkout', reserved_generators: 2 },
          { project_id: 'payments', reserved_generators: 1 },
        ],
      },
    })
    expect(settingsIssuesForField(issues, 'capacity_planning.reservations')).toEqual([
      'Always-active global reservations (3) exceed configured available generators (2).',
    ])
  })

  it('reservationPoolIssues flags unknown pools and skips when the pool list is unavailable', () => {
    const reservations = [
      { project_id: 'checkout', reserved_generators: 1, pool_id: 'pool-known' },
      { project_id: 'payments', reserved_generators: 1, pool_id: 'pool-missing' },
      { project_id: 'reports', reserved_generators: 1 },
    ]
    const pools = [{ id: 'pool-known', name: 'Known', cloud_provider: 'aws', status: 'active', created_at: '2026-07-01T00:00:00Z', updated_at: '2026-07-01T00:00:00Z' }]
    const issues = reservationPoolIssues(reservations, pools)
    expect(issues).toEqual([
      expect.objectContaining({
        field: 'capacity_planning.reservations.1.pool_id',
        message: 'Reservation 2 references an unknown fleet pool.',
      }),
    ])
    expect(reservationPoolIssues(reservations, null)).toEqual([])
  })

  it('describeSettingsSaveError maps 409 conflicts and reservation rejections to actionable guidance', () => {
    const conflict = describeSettingsSaveError(409, 'system settings changed concurrently; refresh and retry')
    expect(conflict.offerReload).toBe(true)
    expect(conflict.message).toContain('Reload settings')
    expect(describeSettingsSaveError(412, 'precondition failed').offerReload).toBe(true)
    expect(describeSettingsSaveError(428, 'precondition required').offerReload).toBe(true)

    const overbooked = describeSettingsSaveError(400, 'capacity_planning.reservations cannot exceed configured available generators')
    expect(overbooked.focusSection).toBe('capacity')
    expect(overbooked.focusCapacitySubsection).toBe('reservations')

    const unknownPool = describeSettingsSaveError(400, 'capacity_planning.reservations references unknown fleet pool "pool-x"')
    expect(unknownPool.focusCapacitySubsection).toBe('reservations')

    const fence = describeSettingsSaveError(400, 'capacity_planning update rejected: approved timeslot demand exceeds envelope')
    expect(fence.focusCapacitySubsection).toBe('reservations')
    expect(fence.message).toContain('Approved capacity timeslots')

    const passthrough = describeSettingsSaveError(400, 'general.api_endpoint is required')
    expect(passthrough).toEqual({ message: 'general.api_endpoint is required' })
  })
})
