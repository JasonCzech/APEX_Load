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
