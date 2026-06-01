// @vitest-environment jsdom

import { describe, expect, it } from 'vitest'

import {
  mergeSettingsOrder,
  moveSettingsSection,
  readSettingsSectionArray,
  SETTINGS_SECTIONS,
  type SettingsSectionId,
} from './settingsSectionLayout'

describe('settingsSectionLayout', () => {
  it('merges persisted section order with new and missing sections', () => {
    expect(mergeSettingsOrder(['projects', 'general'])).toEqual([
      'projects',
      'general',
      'thresholds',
      'capacity',
      'applications',
      'data-pools',
      'integrations',
    ])
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
})
