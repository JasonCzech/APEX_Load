import { describe, expect, it } from 'vitest'

import {
  browserDefinitionFromDraft, browserDraftFromDefinition, defaultBrowserPolicyDraft, defaultJourneyPolicyDraft,
  journeyDefinitionFromDraft, journeyDraftFromDefinition,
} from './policyEditorModel'

describe('browser policy editor model (BI-4)', () => {
  it('round-trips a stored definition through the form draft', () => {
    const stored = {
      version: 1,
      apply_by_default: true,
      description: 'Checkout vitals',
      metrics: [{ metric: 'lcp', good: 2000, poor: 3500, weight: 50 }, { metric: 'cls', good: 0.05, poor: 0.2, weight: 10 }],
      applicability: { engines: ['webkit'], device_profiles: ['iphone_15', 'pixel_8'] },
      effect_floors: { timing_ms: 120, cls: 0.05, timing_by_metric_ms: { lcp: 350, fcp: 75, inp: 50 } },
      baseline: { mode: 'pinned_run', run_id: 'run-7' },
    }
    const draft = browserDraftFromDefinition('Checkout', stored)
    expect(draft.metrics.fcp.include).toBe(false)
    expect(draft.deviceProfiles).toBe('iphone_15, pixel_8')
    expect(browserDefinitionFromDraft(draft)).toEqual({ definition: stored })
  })

  it('saves the defaults as a valid four-metric policy and omits empty optional sections', () => {
    const { definition } = browserDefinitionFromDraft({ ...defaultBrowserPolicyDraft(), name: 'Default vitals' })
    expect(definition).toEqual({
      version: 1,
      metrics: [
        { metric: 'lcp', good: 2500, poor: 4000, weight: 35 },
        { metric: 'fcp', good: 1800, poor: 3000, weight: 15 },
        { metric: 'inp', good: 200, poor: 500, weight: 30 },
        { metric: 'cls', good: 0.1, poor: 0.25, weight: 20 },
      ],
      baseline: { mode: 'latest_compatible' },
    })
  })

  it('keeps baseline floors when a metric is excluded from scoring and rejects invalid floors', () => {
    const draft = { ...defaultBrowserPolicyDraft(), name: 'Floors' }
    draft.metrics.lcp.include = false
    draft.floorTimingByMetricMs.lcp = '350'
    expect(browserDefinitionFromDraft(draft).definition?.effect_floors).toEqual({ timing_by_metric_ms: { lcp: 350 } })
    for (const value of ['49', '600001', 'NaN', 'Infinity', '-1']) {
      draft.floorTimingByMetricMs.lcp = value
      expect(browserDefinitionFromDraft(draft).problem).toContain('LCP floor must be between')
    }
    draft.floorTimingByMetricMs.lcp = ' '
    expect(browserDefinitionFromDraft(draft).definition?.effect_floors).toBeUndefined()
    expect(defaultBrowserPolicyDraft().floorTimingByMetricMs.lcp).toBe('')
  })

  it('explains a draft the controller would refuse', () => {
    const base = { ...defaultBrowserPolicyDraft(), name: 'P' }
    expect(browserDefinitionFromDraft({ ...base, name: ' ' }).problem).toBe('Name the policy.')
    expect(browserDefinitionFromDraft({ ...base, metrics: { ...base.metrics, lcp: { include: true, good: '4000', poor: '2500', weight: '1' } } }).problem).toContain("LCP's good bound")
    const none = Object.fromEntries(Object.entries(base.metrics).map(([key, value]) => [key, { ...value, include: false }])) as typeof base.metrics
    expect(browserDefinitionFromDraft({ ...base, metrics: none }).problem).toBe('Score at least one metric.')
    const zero = Object.fromEntries(Object.entries(base.metrics).map(([key, value]) => [key, { ...value, weight: '0' }])) as typeof base.metrics
    expect(browserDefinitionFromDraft({ ...base, metrics: zero }).problem).toBe('At least one weight must be above zero.')
    expect(browserDefinitionFromDraft({ ...base, baselineMode: 'pinned_run' }).problem).toContain('pinned baseline')
  })
})

describe('journey policy editor model (BI-4)', () => {
  it('round-trips transaction and action boundaries', () => {
    const stored = {
      version: 1,
      journeys: [
        { id: 'checkout', name: 'Checkout', start: { transaction: 'Cart' }, success: { action: 'Confirm order' }, weight: 3 },
        { id: 'search', name: 'Search', start: { action: 'Open search' }, success: { transaction: 'Results' }, weight: 1 },
      ],
      step_risk_enabled: true,
    }
    expect(journeyDefinitionFromDraft(journeyDraftFromDefinition('Shop', stored))).toEqual({ definition: stored })
  })

  it('refuses duplicate or malformed journey IDs and missing boundaries', () => {
    const base = { ...defaultJourneyPolicyDraft(), name: 'Shop' }
    const journey = { ...base.journeys[0], name: 'Checkout', start: 'Cart', success: 'Confirm', weight: '1' }
    expect(journeyDefinitionFromDraft({ ...base, journeys: [journey, journey] }).problem).toContain('unique lowercase slug')
    expect(journeyDefinitionFromDraft({ ...base, journeys: [{ ...journey, id: 'Check Out' }] }).problem).toContain('unique lowercase slug')
    expect(journeyDefinitionFromDraft({ ...base, journeys: [{ ...journey, success: '' }] }).problem).toContain('start and a success boundary')
    expect(journeyDefinitionFromDraft({ ...base, journeys: [] }).problem).toBe('Define at least one journey.')
  })
})
