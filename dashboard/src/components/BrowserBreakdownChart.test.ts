import { describe, expect, it } from 'vitest'

import {
  buildBrowserBreakdownData,
  buildArchiveBrowserBreakdownSeries,
  buildComparisonBrowserBreakdownSeries,
  formatBrowserBreakdownTooltipLabel,
  formatBrowserBreakdownTooltipValue,
  getBrowserBreakdownChartCopy,
  getBrowserBreakdownSegmentLabel,
  getBrowserBreakdownTitle,
} from './browserBreakdownModel'

describe('BrowserBreakdownChart helpers', () => {
  it('formats tooltip values by metric type', () => {
    expect(formatBrowserBreakdownTooltipValue(240, 'ms')).toBe('240.0 ms')
    expect(formatBrowserBreakdownTooltipValue(0.0314, 'score')).toBe('0.031')
    expect(formatBrowserBreakdownTooltipValue(undefined, 'ms')).toBe('n/a')
    expect(formatBrowserBreakdownTooltipLabel('WebKit', 'Engine')).toBe('Engine: WebKit')
    expect(formatBrowserBreakdownTooltipLabel(undefined, 'Device')).toBe('Device: unknown')
  })

  it('derives consistent titles and copy from shared series builders', () => {
    expect(getBrowserBreakdownTitle('engine', 'Comparison', 'LCP / INP')).toBe('Browser Engine Comparison (LCP / INP)')
    expect(getBrowserBreakdownSegmentLabel('platform')).toBe('Platform')
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

  it('builds browser breakdown data from shared sources with device fallbacks', () => {
    expect(
      buildBrowserBreakdownData('engine', {
        devices: {
          iphone: {
            samples: 2,
            browser_engine: 'webkit',
            avg_lcp_ms: 240,
            avg_inp_ms: 56,
            avg_cls_score: 0.031,
          },
          pixel: {
            samples: 1,
            browser_engine: 'chromium',
            avg_lcp_ms: 180,
            avg_inp_ms: 41,
            avg_cls_score: 0.014,
          },
        },
      }),
    ).toEqual([
      { segment: 'Chromium', lcp: 180, inp: 41, cls: 0.014 },
      { segment: 'WebKit', lcp: 240, inp: 56, cls: 0.031 },
    ])

    expect(
      buildBrowserBreakdownData('overall', {
        summary: {
          avg_lcp_ms: 220,
          avg_inp_ms: 50,
          avg_cls_score: 0.02,
        },
      }),
    ).toEqual([
      { segment: 'Overall', lcp: 220, inp: 50, cls: 0.02 },
    ])
  })
})