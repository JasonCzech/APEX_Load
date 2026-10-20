import { describe, expect, it } from 'vitest'

import type { RunArchiveReport } from '../../types'
import { groupBreakdown } from './groupBreakdownModel'

describe('groupBreakdown', () => {
  it('orders groups by name with ungrouped records last', () => {
    const report = { group_breakdown: { transaction: {
      '': { totals: { transactions: 1 }, items: { Orphan: { transactions: 1 } } },
      'Group 10': { totals: { transactions: 1 }, items: { b: { transactions: 1 } } },
      'Group 2': { totals: { transactions: 2 }, items: { z: { transactions: 1 }, a: { transactions: 1 } } },
    } } } as unknown as RunArchiveReport
    const result = groupBreakdown(report, 'transaction')
    expect(result.ungrouped).toBe(false)
    expect(result.groups.map(group => group.name)).toEqual(['Group 2', 'Group 10', ''])
    expect(result.groups[0].items.map(item => item.name)).toEqual(['a', 'z'])
  })

  it('lists an older report\'s names under one whole-run section', () => {
    const report = { metric_populations: { transaction: { transactions: 2 } }, by_transaction: { Login: { transactions: 2 } } } as unknown as RunArchiveReport
    const result = groupBreakdown(report, 'transaction')
    expect(result.ungrouped).toBe(true)
    expect(result.groups).toEqual([{ name: '', items: [{ name: 'Login', stats: { transactions: 2 } }] }])
    expect(result.overall?.transactions).toBe(2)
  })

  it('uses the mixed overview for a report from before metric populations', () => {
    const report = { overview: { transactions: 7, errors: 1 }, by_action: { Login: { transactions: 7 } } } as unknown as RunArchiveReport
    const result = groupBreakdown(report, 'unknown')
    expect(result.overall?.transactions).toBe(7)
    expect(result.groups[0].items.map(item => item.name)).toEqual(['Login'])
  })
})
