import { describe, expect, it } from 'vitest'

import {
  buildBrowserBreakdownData,
  buildArchiveBrowserBreakdownSeries,
  buildBrowserUXInsight,
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

  it('derives explainable UX intelligence from browser metric thresholds', () => {
    const insight = buildBrowserUXInsight({
      samples: 6,
      avg_lcp_ms: 4300,
      avg_inp_ms: 260,
      avg_cls_score: 0.08,
      avg_blocking_resources: 12,
      avg_long_task_count: 3,
      avg_browser_process_cpu_pct: 72,
      avg_browser_process_mem_mb: 260,
    }, {
      iphone_se: {
        samples: 3,
        avg_lcp_ms: 4400,
        avg_inp_ms: 520,
        avg_cls_score: 0.12,
      },
      pixel_8: {
        samples: 3,
        avg_lcp_ms: 2100,
        avg_inp_ms: 120,
        avg_cls_score: 0.02,
      },
    })

    expect(insight?.severity).toBe('poor')
    expect(insight?.label).toBe('Poor')
    expect(insight?.worstDevice).toBe('iphone_se')
    expect(insight?.summary).toContain('UX signals need attention')
    expect(insight?.signals.find(signal => signal.label === 'LCP')).toMatchObject({
      value: '4300.0 ms',
      severity: 'poor',
    })
    expect(insight?.signals.find(signal => signal.label === 'Browser RSS')).toMatchObject({
      value: '260.0 MB',
      severity: 'poor',
    })
  })
})
