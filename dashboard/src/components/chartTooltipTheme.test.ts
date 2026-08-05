import { describe, expect, it } from 'vitest'

import { preserveChartItemOrder } from './chartTooltipTheme'

describe('preserveChartItemOrder', () => {
  it('returns an equal sort key for every series item', () => {
    expect(preserveChartItemOrder()).toBe(0)
    expect([3, 1, 2].sort(() => preserveChartItemOrder() - preserveChartItemOrder())).toEqual([3, 1, 2])
  })
})
