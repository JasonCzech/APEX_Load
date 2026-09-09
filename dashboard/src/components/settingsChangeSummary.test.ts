import { describe, expect, it } from 'vitest'
import { changedSettingsFields } from './settingsChangeSummary'
describe('pending field summary', () => {
 it('names only changed fields and excludes values and revision', () => {
  expect(changedSettingsFields({ revision: 'one', auth: { password: 'old-secret' }, unchanged: true }, { revision: 'two', auth: { password: 'new-secret' }, unchanged: true })).toEqual(['auth › password'])
 })
 it('summarizes arrays and removal without leaking payloads', () => {
  expect(changedSettingsFields({ routes: ['secret-url'], enabled: true }, { routes: [] })).toEqual(['routes', 'enabled'])
  expect(changedSettingsFields({}, {})).toEqual([])
 })
})
