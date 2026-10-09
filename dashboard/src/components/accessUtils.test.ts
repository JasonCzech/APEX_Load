import { describe, expect, it } from 'vitest'

import { rotationDaysError, rotationDaysValue } from './accessUtils'

describe('service-account rotation interval', () => {
  it('reports invalid values instead of silently replacing them with 90 days', () => {
    expect(rotationDaysError('-5')).toContain('1 or more')
    expect(rotationDaysError('2.5')).toContain('whole number')
    expect(rotationDaysError('0')).toContain('1 or more')
    expect(rotationDaysError('')).toBe('')
    expect(rotationDaysError('30')).toBe('')
    expect(rotationDaysValue('')).toBe(90)
    expect(rotationDaysValue('30')).toBe(30)
  })

  it('lets an existing account turn rotation reminders off with 0, but not leave the field blank', () => {
    expect(rotationDaysError('0', { allowZero: true })).toBe('')
    expect(rotationDaysValue('0', { allowZero: true })).toBe(0)
    expect(rotationDaysError('', { allowZero: true })).toContain('0 turns rotation reminders off')
    expect(rotationDaysError('-1', { allowZero: true })).toContain('0 or more')
  })
})
