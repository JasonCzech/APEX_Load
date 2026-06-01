import { describe, expect, it } from 'vitest'

import { chartTooltipContentStyle, chartTooltipLabelStyle } from './chartTooltipTheme'

describe('chartTooltipTheme', () => {
  it('uses theme variables for chart tooltip surfaces and text', () => {
    expect(chartTooltipContentStyle).toMatchObject({
      background: 'var(--bg-elevated)',
      border: '1px solid var(--border)',
      color: 'var(--text-primary)',
    })
    expect(chartTooltipLabelStyle.color).toBe('var(--text-primary)')
  })
})
