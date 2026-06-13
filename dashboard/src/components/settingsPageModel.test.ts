import { describe, expect, it } from 'vitest'
import {
  DEFAULT_SETTINGS,
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

  it('usesDefaultGeneratorCapacity is true for the defaults', () => {
    expect(usesDefaultGeneratorCapacity(DEFAULT_SETTINGS)).toBe(true)
  })
})
