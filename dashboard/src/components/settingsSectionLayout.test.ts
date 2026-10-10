// @vitest-environment jsdom

import { describe, expect, it } from 'vitest'

import {
  CAPACITY_SETTINGS_SUBSECTIONS,
  GENERAL_SETTINGS_SUBSECTIONS,
  GOVERNANCE_SETTINGS_SUBSECTIONS,
  INTEGRATION_SETTINGS_SUBSECTIONS,
  isGeneralSettingsSubsectionId,
  isCapacitySettingsSubsectionId,
  isGovernanceSettingsSubsectionId,
  isIntegrationSettingsSubsectionId,
  isProjectSettingsSubsectionId,
  mergeSettingsOrder,
  moveSettingsSection,
  PROJECT_SETTINGS_SUBSECTIONS,
  readSettingsSectionArray,
  readSettingsSubsectionId,
  SETTINGS_SECTIONS,
  type SettingsSectionId,
} from './settingsSectionLayout'

describe('settingsSectionLayout', () => {
  it('merges persisted section order with new and missing sections', () => {
    expect(mergeSettingsOrder(['projects', 'general'])).toEqual([
      'general',
      'thresholds',
      'capacity',
      'domains',
      'feature-access',
      'projects',
      'applications',
      'data-pools',
      'integrations',
    ])
  })

  it('inserts the Domains section before Applications in existing persisted orders', () => {
    expect(mergeSettingsOrder(['general', 'thresholds', 'capacity', 'applications', 'projects', 'data-pools', 'integrations'])).toEqual([
      'general',
      'thresholds',
      'capacity',
      'domains',
      'feature-access',
      'projects',
      'applications',
      'data-pools',
      'integrations',
    ])
  })

  it('keeps Projects immediately before Applications in the canonical and persisted orders', () => {
    const canonical = SETTINGS_SECTIONS.map(section => section.id)
    expect(canonical.indexOf('projects')).toBe(canonical.indexOf('applications') - 1)

    const merged = mergeSettingsOrder(['integrations', 'applications', 'general', 'projects'])
    expect(merged.indexOf('projects')).toBe(merged.indexOf('applications') - 1)
  })

  it('moves a dragged section before the target section', () => {
    const ids = SETTINGS_SECTIONS.map(section => section.id)

    expect(moveSettingsSection(ids, 'integrations', 'general').slice(0, 2)).toEqual(['integrations', 'general'])
    expect(moveSettingsSection(ids, 'general', 'general')).toBe(ids)
  })

  it('reads only known settings section ids from localStorage', () => {
    window.localStorage.setItem('settings.order', JSON.stringify(['projects', 'bogus', 'general']))

    expect(readSettingsSectionArray('settings.order')).toEqual(['projects', 'general'] satisfies SettingsSectionId[])
  })

  it('validates the general settings subsection ids', () => {
    expect(GENERAL_SETTINGS_SUBSECTIONS.map(section => section.id)).toEqual([
      'platform',
      'usage',
      'entitlements',
      'readiness',
      'authentication',
      'governance',
    ])
    expect(isGeneralSettingsSubsectionId('usage')).toBe(true)
    expect(isGeneralSettingsSubsectionId('bogus')).toBe(false)
  })

  it('reads a persisted general subsection with a safe fallback', () => {
    window.localStorage.setItem('settings.generalSubsection', JSON.stringify('governance'))

    expect(readSettingsSubsectionId('settings.generalSubsection')).toBe('governance')

    window.localStorage.setItem('settings.generalSubsection', JSON.stringify('bogus'))
    expect(readSettingsSubsectionId('settings.generalSubsection')).toBe('platform')
    expect(readSettingsSubsectionId('settings.generalSubsection', 'usage')).toBe('usage')
  })

  it('validates subsection ids for dense settings tabs', () => {
    expect(GOVERNANCE_SETTINGS_SUBSECTIONS.map(section => section.id)).toEqual([
      'schedule-defaults',
      'script-imports',
      'service-account-apis',
      'recent-audit-events',
    ])
    expect(CAPACITY_SETTINGS_SUBSECTIONS.map(section => section.id)).toEqual([
      'availability',
      'generator',
      'reservations',
      'retention',
    ])
    expect(PROJECT_SETTINGS_SUBSECTIONS.map(section => section.id)).toEqual([
      'catalog',
      'limits',
      'github',
      'analysis',
      'redaction',
      'secrets',
      'analysis-policies',
    ])
    expect(INTEGRATION_SETTINGS_SUBSECTIONS.map(section => section.id)).toEqual([
      'library',
      'github',
      'elk-capture',
      'dynatrace-capture',
      'prometheus-capture',
      'prometheus',
      'opensearch',
      'grafana',
      'dashboards',
      'powerbi',
      'confluence',
      'notifications',
    ])
    expect(isGovernanceSettingsSubsectionId('service-account-apis')).toBe(true)
    expect(isCapacitySettingsSubsectionId('generator')).toBe(true)
    expect(isProjectSettingsSubsectionId('redaction')).toBe(true)
    expect(isIntegrationSettingsSubsectionId('notifications')).toBe(true)
    expect(isGovernanceSettingsSubsectionId('bogus')).toBe(false)
    expect(isCapacitySettingsSubsectionId('bogus')).toBe(false)
    expect(isProjectSettingsSubsectionId('bogus')).toBe(false)
    expect(isIntegrationSettingsSubsectionId('bogus')).toBe(false)
  })

  it('reads persisted subsections using the requested tab validator', () => {
    window.localStorage.setItem('settings.capacitySubsection', JSON.stringify('retention'))
    window.localStorage.setItem('settings.governanceSubsection', JSON.stringify('recent-audit-events'))
    window.localStorage.setItem('settings.projectSubsection', JSON.stringify('github'))
    window.localStorage.setItem('settings.integrationSubsection', JSON.stringify('notifications'))

    expect(readSettingsSubsectionId('settings.capacitySubsection', 'availability', isCapacitySettingsSubsectionId)).toBe('retention')
    expect(readSettingsSubsectionId('settings.governanceSubsection', 'schedule-defaults', isGovernanceSettingsSubsectionId)).toBe('recent-audit-events')
    expect(readSettingsSubsectionId('settings.projectSubsection', 'catalog', isProjectSettingsSubsectionId)).toBe('github')
    expect(readSettingsSubsectionId('settings.integrationSubsection', 'library', isIntegrationSettingsSubsectionId)).toBe('notifications')

    window.localStorage.setItem('settings.capacitySubsection', JSON.stringify('not-real'))
    expect(readSettingsSubsectionId('settings.capacitySubsection', 'generator', isCapacitySettingsSubsectionId)).toBe('generator')
    window.localStorage.setItem('settings.governanceSubsection', JSON.stringify('not-real'))
    expect(readSettingsSubsectionId('settings.governanceSubsection', 'service-account-apis', isGovernanceSettingsSubsectionId)).toBe('service-account-apis')
  })
})
