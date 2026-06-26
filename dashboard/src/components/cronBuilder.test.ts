import { describe, it, expect } from 'vitest'
import { cronModelToExpr, exprToCronModel, DEFAULT_CRON_MODEL, type CronModel } from './cronBuilder'

describe('cronBuilder', () => {
  it('emits expected expressions per frequency', () => {
    expect(cronModelToExpr({ ...DEFAULT_CRON_MODEL, frequency: 'minutes', everyN: 15 })).toBe('*/15 * * * *')
    expect(cronModelToExpr({ ...DEFAULT_CRON_MODEL, frequency: 'hourly', minute: 30, everyN: 2 })).toBe('30 */2 * * *')
    expect(cronModelToExpr({ ...DEFAULT_CRON_MODEL, frequency: 'daily', minute: 0, hour: 9 })).toBe('0 9 * * *')
    expect(cronModelToExpr({ ...DEFAULT_CRON_MODEL, frequency: 'weekly', minute: 0, hour: 9, daysOfWeek: [1, 3, 5] })).toBe('0 9 * * 1,3,5')
    expect(cronModelToExpr({ ...DEFAULT_CRON_MODEL, frequency: 'monthly', minute: 0, hour: 9, dayOfMonth: 15 })).toBe('0 9 15 * *')
  })

  it('canonicalizes Sunday to 0, never 7', () => {
    const expr = cronModelToExpr({ ...DEFAULT_CRON_MODEL, frequency: 'weekly', daysOfWeek: [7, 1] })
    expect(expr).toBe('0 9 * * 0,1')
    expect(expr).not.toContain('7')
  })

  it('round-trips model -> expr -> model stably', () => {
    const models: CronModel[] = [
      { ...DEFAULT_CRON_MODEL, frequency: 'minutes', everyN: 10 },
      { ...DEFAULT_CRON_MODEL, frequency: 'hourly', minute: 5, everyN: 3 },
      { ...DEFAULT_CRON_MODEL, frequency: 'daily', minute: 30, hour: 14 },
      { ...DEFAULT_CRON_MODEL, frequency: 'weekly', minute: 0, hour: 8, daysOfWeek: [1, 2, 3, 4, 5] },
      { ...DEFAULT_CRON_MODEL, frequency: 'monthly', minute: 45, hour: 23, dayOfMonth: 28 },
    ]
    for (const model of models) {
      const back = exprToCronModel(cronModelToExpr(model))
      expect(back).not.toBeNull()
      expect(cronModelToExpr(back as CronModel)).toBe(cronModelToExpr(model))
    }
  })

  it('parses macros into models', () => {
    expect(exprToCronModel('@daily')?.frequency).toBe('daily')
    expect(exprToCronModel('@weekly')?.daysOfWeek).toEqual([0])
    expect(exprToCronModel('@hourly')?.frequency).toBe('hourly')
    expect(exprToCronModel('@monthly')?.frequency).toBe('monthly')
  })

  it('expands a weekday range to a day list', () => {
    expect(exprToCronModel('0 9 * * 1-5')?.daysOfWeek).toEqual([1, 2, 3, 4, 5])
    expect(exprToCronModel('0 9 * * 5-7')?.daysOfWeek).toEqual([0, 5, 6])
  })

  it('returns null for expressions the builder cannot model', () => {
    expect(exprToCronModel('0 9 * 3 *')).toBeNull() // month restriction
    expect(exprToCronModel('0 9 L * *')).toBeNull() // Quartz last-day
    expect(exprToCronModel('0 9 * * 5#3')).toBeNull() // nth weekday
    expect(exprToCronModel('0 9 15 * 1')).toBeNull() // both dom and dow restricted
    expect(exprToCronModel('not a cron')).toBeNull()
    expect(exprToCronModel('0 9 * * MON')).toBeNull() // alias (raw input still handles it)
  })
})
