import { describe, expect, it } from 'vitest'
import { matchesSettingsSearch, settingsSearchControl, SETTINGS_DESTINATIONS } from './settingsNavigation'
describe('Settings search', () => {
 it.each(['theme', 'DARK', 'word wrap', 'rows', 'motion', '  appearance  '])('finds personal controls for %s', query => {
  expect(SETTINGS_DESTINATIONS.filter(item => matchesSettingsSearch(item, query)).map(item => item.id)).toContain('preferences')
 })
 it('does not return unrelated sections or match impossible combinations', () => {
  expect(matchesSettingsSearch(SETTINGS_DESTINATIONS[0], 'theme')).toBe(false)
  expect(SETTINGS_DESTINATIONS.some(item => matchesSettingsSearch(item, 'theme kubernetes'))).toBe(false)
 })
})

it.each([['dark', 'personal-theme'], ['word wrap', 'personal-word-wrap'], ['rows', 'personal-table-rows'], ['motion', 'personal-motion']])('resolves %s to its control', (query, id) => {
 expect(settingsSearchControl('preferences', query)).toBe(id)
 expect(settingsSearchControl('authentication', query)).toBeUndefined()
})

describe('settings search vocabulary', () => {
  it('finds settings by the words admins and LRE engineers use', () => {
    const find = (query: string) => SETTINGS_DESTINATIONS.filter(item => matchesSettingsSearch(item, query)).map(item => item.id)
    expect(find('sla')).toContain('thresholds')
    expect(find('webhook')).toContain('notifications')
    expect(find('slack')).toContain('notifications')
    expect(find('licence')).toContain('entitlements')
    expect(find('ldap')).toContain('authentication')
    expect(find('load generator')).toContain('pools')
    expect(find('dark mode')).toContain('preferences')
  })
})
