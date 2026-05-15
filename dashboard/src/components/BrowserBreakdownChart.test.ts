import { describe, expect, it } from 'vitest'

import {
  buildArchiveBrowserBreakdownSeries,
  buildComparisonBrowserBreakdownSeries,
  formatBrowserBreakdownTooltipValue,
  getBrowserBreakdownChartCopy,
  getBrowserBreakdownTitle,
} from './BrowserBreakdownChart'

describe('BrowserBreakdownChart helpers', () => {
  it('formats tooltip values by metric type', () => {
    expect(formatBrowserBreakdownTooltipValue(240, 'ms')).toBe('240.0 ms')
    expect(formatBrowserBreakdownTooltipValue(0.0314, 'score')).toBe('0.031')
    expect(formatBrowserBreakdownTooltipValue(undefined, 'ms')).toBe('n/a')
  })

  it('derives consistent titles and copy from shared series builders', () => {
    expect(getBrowserBreakdownTitle('engine', 'Comparison', 'LCP / INP')).toBe('Browser Engine Comparison (LCP / INP)')
    expect(getBrowserBreakdownChartCopy(buildArchiveBrowserBreakdownSeries())).toBe('Series: LCP and INP on the left axis, CLS on the right axis.')
    expect(
      getBrowserBreakdownChartCopy(
        buildComparisonBrowserBreakdownSeries([
          { id: 'run-a', name: 'Run A' },
          { id: 'run-b', name: 'Run B' },
        ]),
      ),
    ).toBe('Series: solid LCP and dashed INP for each selected run.')
  })
})