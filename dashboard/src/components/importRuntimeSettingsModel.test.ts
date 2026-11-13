import { describe, expect, it } from 'vitest'
import { importRuntimeFields } from './importRuntimeSettingsModel'

describe('import runtime settings contract', () => {
  // SettingValue renders its own "Default" (unset) entry first, so a listed ""
  // option would render a second, unlabeled blank row.
  it('lists no empty select option', () => {
    const blank = importRuntimeFields.filter(field => field.options?.some(option => option.trim() === '')).map(field => field.path)
    expect(blank).toEqual([])
  })
})
