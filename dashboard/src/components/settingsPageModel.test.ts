import { describe, expect, it } from 'vitest'
import {
  DEFAULT_SETTINGS,
  normalizeDashboardSystemSettings,
  splitListDraft,
  usesDefaultGeneratorCapacity,
  validateOptionalPositiveInteger,
  validateProjectQuotaField,
  validateSettings,
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
})
