import { describe, expect, it } from 'vitest'
import type { BrowserDeviceLiveMetrics, BrowserLiveMetrics } from '../types'
import { buildChartCSV } from './exportableChartUtils'

import {
  buildBrowserBreakdownData,
  buildComparisonBrowserBreakdownData,
  formatBrowserEngineLabel,
  formatDevicePlatformLabel,
  buildArchiveBrowserBreakdownSeries,
  buildBrowserUXInsight,
  BROWSER_UX_MODEL_VERSION,
  reproduceLegacyBrowserUXScore,
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
    expect(formatBrowserBreakdownTooltipValue(undefined, 'ms')).toBe('—')
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
    const runSeries = buildComparisonBrowserBreakdownSeries([
      { id: 'run-a', name: 'Run A' },
      { id: 'run-b', name: 'Run B' },
      { id: 'run-c', name: 'Run C' },
    ])
    expect(new Set(runSeries.filter((_, index) => index % 2 === 0).map(series => series.stroke)).size).toBe(3)
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
      { segment: 'Chromium', cohortKey: JSON.stringify(['engine', 'chromium']), lcp: 180, inp: 41, cls: 0.014 },
      { segment: 'WebKit', cohortKey: JSON.stringify(['engine', 'webkit']), lcp: 240, inp: 56, cls: 0.031 },
    ])

    expect(
      buildBrowserBreakdownData('overall', {
        summary: {
          samples: 3,
          avg_lcp_ms: 220,
          avg_inp_ms: 50,
          avg_cls_score: 0.02,
        },
      }),
    ).toEqual([
      { segment: 'Overall', cohortKey: JSON.stringify(['overall']), lcp: 220, inp: 50, cls: 0.02 },
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
        avg_long_task_count: 3, avg_blocking_resources: 12, avg_browser_process_cpu_pct: 72, avg_browser_process_mem_mb: 260,
      },
      pixel_8: {
        samples: 3,
        avg_lcp_ms: 2100,
        avg_inp_ms: 120,
        avg_cls_score: 0.02,
        avg_long_task_count: 0, avg_blocking_resources: 0, avg_browser_process_cpu_pct: 20, avg_browser_process_mem_mb: 80,
      },
    })

    expect(insight?.severity).toBe('poor')
    expect(insight?.label).toBe('Poor')
    expect(insight?.worstDevice).toBe('iphone_se')
    expect(insight?.summary).toContain('observed components need attention')
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

const completeSummary: BrowserLiveMetrics = {
  samples: 2, avg_lcp_ms: 1000, avg_inp_ms: 100, avg_cls_score: 0.01,
  avg_long_task_count: 1, avg_blocking_resources: 2, avg_browser_process_cpu_pct: 10, avg_browser_process_mem_mb: 100,
}

describe('legacy browser summary availability', () => {
  it('makes all absent fields unscored rather than healthy', () => {
    const insight = buildBrowserUXInsight({ samples: 3 })!
    expect(insight).toMatchObject({ version: BROWSER_UX_MODEL_VERSION, score: null, severity: 'unscored', label: 'Unscored', coverage: { observed: 0, required: 7 } })
    expect(insight.signals.every(signal => signal.status === 'missing' && signal.score === null && signal.contribution === null && signal.severity === 'unscored')).toBe(true)
    expect(insight.summary).toContain('0/7 required fields available')
    expect(insight.applicability).toContain('not a standards-qualified Core Web Vitals score')
  })

  it.each([undefined, null, '1', 0, -1, 1.5, NaN, Infinity, Number.MAX_SAFE_INTEGER + 1])('refuses invalid sample population %s while retaining observed fields', samples => {
    const insight = buildBrowserUXInsight({ ...completeSummary, samples } as BrowserLiveMetrics)!
    expect(insight.score).toBeNull()
    expect(insight.coverage).toEqual({ observed: 7, required: 7 })
    expect(insight.sampleReason).toContain('positive safe integer')
    expect(insight.signals.every(signal => signal.score === null && signal.severity === 'unscored')).toBe(true)
    expect(insight.signals[0].value).toBe('1000.0 ms')
  })

  it.each([undefined, null, -0.01, NaN, Infinity, '200'])('refuses a missing or invalid required value %s without renormalizing contributions', value => {
    const insight = buildBrowserUXInsight({ ...completeSummary, avg_inp_ms: value } as BrowserLiveMetrics)!
    expect(insight.score).toBeNull()
    expect(insight.coverage).toEqual({ observed: 6, required: 7 })
    expect(insight.signals[1]).toMatchObject({ value: '—', status: value === undefined ? 'missing' : 'invalid', score: null, contribution: null, severity: 'unscored' })
    expect(insight.signals[0].score).toBe(100)
    expect(insight.signals[0].weight).toBe(1)
    expect(insight.signals[0].contribution).toBeCloseTo(100 / 7)
  })

  it('retains explicit zero and fixed weights only for complete legacy fields', () => {
    const insight = buildBrowserUXInsight({ samples: 1, avg_lcp_ms: 0, avg_inp_ms: 0, avg_cls_score: 0, avg_long_task_count: 0, avg_blocking_resources: 0, avg_browser_process_cpu_pct: 0, avg_browser_process_mem_mb: 0 })!
    expect(insight).toMatchObject({ score: 100, label: 'Within legacy thresholds', coverage: { observed: 7, required: 7 } })
    expect(insight.signals.map(signal => signal.value)).toEqual(['0.0 ms', '0.0 ms', '0.000', '0.0', '0.0', '0.0%', '0.0 MB'])
    expect(insight.signals.reduce((sum, signal) => sum + signal.weight, 0)).toBe(7)
    expect(insight.signals.reduce((sum, signal) => sum + (signal.contribution ?? 0), 0)).toBeCloseTo(100)
    expect(insight.label).not.toBe('Healthy')
  })

  it('ranks only complete device populations and retains concrete exclusion reasons', () => {
    const insight = buildBrowserUXInsight(completeSummary, {
      absent: { samples: 3 }, invalid: { ...completeSummary, samples: 0 },
      missing: { ...completeSummary, avg_inp_ms: undefined },
      slow: { ...completeSummary, avg_lcp_ms: 4000 }, fast: completeSummary,
    })!
    expect(insight).toMatchObject({ worstDevice: 'slow', rankedDevices: 2, unrankedDevices: 3 })
    expect(insight.unrankedDeviceReasons.map(entry => entry.profile)).toEqual(['absent', 'invalid', 'missing'])
    expect(insight.unrankedDeviceReasons[0].reason).toContain('LCP (missing)')
    expect(insight.unrankedDeviceReasons[1].reason).toContain('positive safe integer')
    expect(insight.unrankedDeviceReasons[2].reason).toContain('INP (missing)')
    expect(buildBrowserUXInsight(completeSummary, { absent: { samples: 1 } })?.worstDevice).toBeUndefined()
  })

  it('keeps the original arithmetic behind an explicit historical version', () => {
    expect(reproduceLegacyBrowserUXScore({ samples: 1 }, 'legacy-canary-v1')).toEqual({ version: 'legacy-canary-v1', score: 100 })
    expect(buildBrowserUXInsight({ samples: 1 })?.score).toBeNull()
    expect(reproduceLegacyBrowserUXScore(completeSummary, 'legacy-canary-v1').score).toBe(buildBrowserUXInsight(completeSummary)?.score)
    expect(() => reproduceLegacyBrowserUXScore(completeSummary, 'unknown' as 'legacy-canary-v1')).toThrow('Unsupported legacy scoring version')
  })

  it.each(['engine', 'platform'] as const)('refuses missing cohort metrics in %s fallback rather than averaging zero or narrowing', breakdown => {
    const devices = {
      observed: { ...completeSummary, samples: 10, avg_lcp_ms: 4000, browser_engine: 'chromium', device_platform: 'desktop' },
      missing: { ...completeSummary, samples: 90, avg_lcp_ms: undefined as number | undefined, browser_engine: 'chromium', device_platform: 'desktop' },
    }
    const data = buildBrowserBreakdownData(breakdown, { devices })
    expect(data).toEqual([{ segment: breakdown === 'engine' ? 'Chromium' : 'Desktop', cohortKey: JSON.stringify([breakdown, breakdown === 'engine' ? 'chromium' : 'desktop']), lcp: undefined, inp: 100, cls: 0.01 }])
    const csv = buildChartCSV(data, [{ key: 'segment', label: 'Segment', value: row => row.segment }, { key: 'lcp', label: 'LCP', value: row => row.lcp }, { key: 'inp', label: 'INP', value: row => row.inp }])
    expect(csv).toContain(`"${data[0].segment}",,"100"`)
    devices.missing.avg_lcp_ms = 0
    expect(buildBrowserBreakdownData(breakdown, { devices })[0].lcp).toBe(400)
    devices.missing.samples = 0
    expect(buildBrowserBreakdownData(breakdown, { devices })[0]).toMatchObject({ lcp: undefined, inp: undefined, cls: undefined })
  })

  it('refuses overflowing sample cohorts and invalid direct measurements, while avoiding weighted-product overflow', () => {
    const large = { ...completeSummary, avg_lcp_ms: Number.MAX_VALUE, browser_engine: 'chromium' }
    expect(buildBrowserBreakdownData('engine', { devices: { a: large, b: large } })[0].lcp).toBe(Number.MAX_VALUE)
    expect(buildBrowserBreakdownData('engine', { devices: { a: { ...large, samples: Number.MAX_SAFE_INTEGER }, b: large } })[0].lcp).toBeUndefined()
    expect(buildBrowserBreakdownData('overall', { summary: { samples: 1, avg_lcp_ms: -1, avg_inp_ms: Infinity, avg_cls_score: 0 } })).toEqual([{ segment: 'Overall', cohortKey: JSON.stringify(['overall']), lcp: undefined, inp: undefined, cls: 0 }])
    expect(formatBrowserBreakdownTooltipValue(-1)).toBe('—')
  })

  it('weighs a cohort LCP by its page loads, the other measures by its samples', () => {
    const app = { ...completeSummary, browser_engine: 'chromium', samples: 10, document_loads: 1, avg_lcp_ms: 1000, avg_inp_ms: 100 }
    const site = { ...completeSummary, browser_engine: 'chromium', samples: 10, document_loads: 9, avg_lcp_ms: 2000, avg_inp_ms: 200 }
    const [chromium] = buildBrowserBreakdownData('engine', { devices: { app, site } })
    expect(chromium.lcp).toBeCloseTo(1900)
    expect(chromium.inp).toBeCloseTo(150)
    // A cohort that loaded no page adds nothing to the LCP.
    const inPage = { ...completeSummary, browser_engine: 'chromium', samples: 10, document_loads: 0, avg_lcp_ms: undefined, avg_inp_ms: 300 }
    const [mixed] = buildBrowserBreakdownData('engine', { devices: { site, inPage } })
    expect(mixed.lcp).toBeCloseTo(2000)
    expect(mixed.inp).toBeCloseTo(250)
    // An archived summary from before the count weighs its LCP by samples.
    const archived = { ...completeSummary, browser_engine: 'chromium', samples: 10, document_loads: 0, avg_lcp_ms: 1000 }
    expect(buildBrowserBreakdownData('engine', { devices: { site, archived } })[0].lcp).toBeCloseTo((2000 * 9 + 1000 * 10) / 19)
  })
})


describe('malformed legacy browser cohorts', () => {
  it.each([null, 0, false, 'bad', []])('preserves malformed entry %j as direct/fallback gaps and an unranked reason', raw => {
    const invalid = raw as unknown as BrowserDeviceLiveMetrics
    const gap = { lcp: undefined, inp: undefined, cls: undefined }
    expect(buildBrowserBreakdownData('overall', { summary: invalid })).toEqual([{ segment: 'Overall', cohortKey: JSON.stringify(['overall']), ...gap }])
    expect(buildBrowserBreakdownData('device', { devices: { malformed: invalid } })).toEqual([{ segment: 'malformed', cohortKey: JSON.stringify(['device', 'malformed']), ...gap }])
    expect(buildBrowserBreakdownData('engine', { engines: { chromium: invalid } })).toEqual([{ segment: 'Chromium', cohortKey: JSON.stringify(['engine', 'chromium']), ...gap }])
    expect(buildBrowserBreakdownData('platform', { platforms: { desktop: invalid } })).toEqual([{ segment: 'Desktop', cohortKey: JSON.stringify(['platform', 'desktop']), ...gap }])
    expect(buildBrowserBreakdownData('engine', { devices: { malformed: invalid } })).toEqual([{ segment: 'Engine unavailable (malformed)', cohortKey: JSON.stringify(['engine', 'unavailable', 'malformed']), ...gap }])
    expect(buildBrowserBreakdownData('platform', { devices: { malformed: invalid } })).toEqual([{ segment: 'Platform unavailable (malformed)', cohortKey: JSON.stringify(['platform', 'unavailable', 'malformed']), ...gap }])
    const insight = buildBrowserUXInsight(completeSummary, { malformed: invalid, valid: completeSummary })!
    expect(insight).toMatchObject({ rankedDevices: 1, unrankedDevices: 1, worstDevice: 'valid' })
    expect(insight.unrankedDeviceReasons[0]).toMatchObject({ profile: 'malformed' })
    expect(insight.unrankedDeviceReasons[0].reason).toContain('Malformed summary: expected an object')
    expect(buildBrowserUXInsight(invalid)?.score).toBeNull()
  })

  it('keeps unavailable identities as individual gaps instead of merging unknown cohorts', () => {
    const data = buildBrowserBreakdownData('engine', { devices: { missing_a: completeSummary, missing_b: completeSummary, known: { ...completeSummary, browser_engine: 'chromium' } } })
    expect(data).toEqual([
      { segment: 'Chromium', cohortKey: JSON.stringify(['engine', 'chromium']), lcp: 1000, inp: 100, cls: 0.01 },
      { segment: 'Engine unavailable (missing_a)', cohortKey: JSON.stringify(['engine', 'unavailable', 'missing_a']), lcp: undefined, inp: undefined, cls: undefined },
      { segment: 'Engine unavailable (missing_b)', cohortKey: JSON.stringify(['engine', 'unavailable', 'missing_b']), lcp: undefined, inp: undefined, cls: undefined },
    ])
  })
})


describe('exact browser cohort identity', () => {
  it.each(['engine', 'platform'] as const)('keeps display aliases and unavailable-row labels separate in %s regardless of input order', breakdown => {
    const field = breakdown === 'engine' ? 'browser_engine' : 'device_platform'
    const rawIdentity = breakdown === 'engine' ? 'chromium' : 'desktop'
    const aliasIdentity = breakdown === 'engine' ? 'Chromium' : 'Desktop'
    const display = aliasIdentity
    const unavailableLabel = `${breakdown === 'engine' ? 'Engine' : 'Platform'} unavailable (bad)`
    const entries = [
      ['a', { ...completeSummary, [field]: rawIdentity, avg_lcp_ms: 100 }],
      ['b', { ...completeSummary, [field]: aliasIdentity, avg_lcp_ms: 900 }],
      ['bad', null],
      ['named_like_gap', { ...completeSummary, [field]: unavailableLabel, avg_lcp_ms: 2000 }],
    ] as const
    const devices = Object.fromEntries(entries) as unknown as Record<string, BrowserDeviceLiveMetrics>
    const forward = buildBrowserBreakdownData(breakdown, { devices })
    const reverse = buildBrowserBreakdownData(breakdown, { devices: Object.fromEntries([...entries].reverse()) as unknown as Record<string, BrowserDeviceLiveMetrics> })
    expect(forward).toEqual(reverse)
    expect(forward).toHaveLength(4)
    expect(new Set(forward.map(row => row.cohortKey)).size).toBe(4)
    expect(forward.filter(row => row.segment === display).map(row => row.lcp).sort()).toEqual([100, 900])
    expect(forward.filter(row => row.segment === unavailableLabel).map(row => row.lcp)).toContain(undefined)
    expect(forward.filter(row => row.segment === unavailableLabel).map(row => row.lcp)).toContain(2000)
    expect(forward.find(row => row.cohortKey === JSON.stringify([breakdown, rawIdentity]))?.lcp).toBe(100)
    expect(forward.find(row => row.cohortKey === JSON.stringify([breakdown, aliasIdentity]))?.lcp).toBe(900)
  })

  it('aligns comparisons by exact cohort identity instead of an equal presentation label', () => {
    const data = buildComparisonBrowserBreakdownData('engine', [
      { id: 'run-a', source: { devices: { lower: { ...completeSummary, browser_engine: 'chromium', avg_lcp_ms: 100 } } } },
      { id: 'run-b', source: { devices: { upper: { ...completeSummary, browser_engine: 'Chromium', avg_lcp_ms: 900 } } } },
    ])
    expect(data).toEqual([
      { segment: 'Chromium', cohortKey: JSON.stringify(['engine', 'Chromium']), 'run-b': 900, 'run-b_inp': 100 },
      { segment: 'Chromium', cohortKey: JSON.stringify(['engine', 'chromium']), 'run-a': 100, 'run-a_inp': 100 },
    ])
    expect(data.every(row => !(Object.prototype.hasOwnProperty.call(row, 'run-a') && Object.prototype.hasOwnProperty.call(row, 'run-b')))).toBe(true)
  })

  it.each([{}, [], true, 42])('refuses non-string label metadata %j without creating non-string display values', raw => {
    expect(formatBrowserEngineLabel(raw)).toBe('browser')
    expect(formatDevicePlatformLabel(raw)).toBe('unknown')
    const malformedIdentity = { ...completeSummary, browser_engine: raw, device_platform: raw } as unknown as BrowserDeviceLiveMetrics
    expect(buildBrowserBreakdownData('engine', { engines: { native: malformedIdentity } })[0].segment).toBe('browser')
    expect(buildBrowserBreakdownData('platform', { platforms: { native: malformedIdentity } })[0].segment).toBe('unknown')
    expect(buildBrowserBreakdownData('engine', { devices: { native: malformedIdentity } })[0].lcp).toBeUndefined()
  })
})
