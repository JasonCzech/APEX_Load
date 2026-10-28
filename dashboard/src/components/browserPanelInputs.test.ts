import { describe, expect, it, vi, beforeEach } from 'vitest'
import { freezeBrowserPanelDefinition, verifyBrowserPanelInputs } from './browserPanelInputs'
import type { BrowserPanelInputs, DashboardAnalyticsQuery } from '../hooks/api/useDashboardDefinitionsApi'

const { request } = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../hooks/apiClient', () => ({ request }))

describe('verifyBrowserPanelInputs and freezeBrowserPanelDefinition', () => {
  beforeEach(() => {
    request.mockReset()
  })

  it('verifies query_version 4 (version 1) and rejects v2 fields on v1 inputs', () => {
    const queryV4: DashboardAnalyticsQuery = {
      query_version: 4,
      browser_tuple_mode: 'separate',
      population: 'browser_document',
      metric: 'document_lcp',
      aggregation: 'p75',
      comparison_run_id: 'base-run',
    }
    const validV1: BrowserPanelInputs = {
      version: 1,
      query_sha256: 'a'.repeat(64),
      current: { run_id: 'cur-run', manifest_sha256: 'b'.repeat(64), tuple_ids: ['c'.repeat(64)] },
      baseline: { run_id: 'base-run', manifest_sha256: 'd'.repeat(64), tuple_ids: ['c'.repeat(64)] },
    }
    expect(verifyBrowserPanelInputs('cur-run', queryV4, validV1)).toBe(true)
    expect(verifyBrowserPanelInputs('cur-run', queryV4, { ...validV1, selection_receipt_sha256: 'e'.repeat(64) })).toBe(false)
    expect(verifyBrowserPanelInputs('cur-run', queryV4, {
      ...validV1,
      policy_pins: [{ kind: 'browser_policy', id: 'pol-1', revision: 1, sha256: 'f'.repeat(64) }],
    })).toBe(false)
  })

  it('verifies query_version 5 (version 2) policy pins and selection receipt digests', () => {
    const queryV5: DashboardAnalyticsQuery = {
      query_version: 5,
      browser_tuple_mode: 'separate',
      population: 'browser_document',
      metric: 'document_lcp',
      aggregation: 'p75',
      comparison_run_id: 'base-run',
    }
    const validV2: BrowserPanelInputs = {
      version: 2,
      query_sha256: 'a'.repeat(64),
      selection_receipt_sha256: 'e'.repeat(64),
      policy_pins: [
        { kind: 'browser_policy', id: 'pol-browser', revision: 2, sha256: '1'.repeat(64) },
        { kind: 'journey_policy', id: 'pol-journey', revision: 1, sha256: '2'.repeat(64) },
      ],
      current: {
        run_id: 'cur-run',
        manifest_sha256: 'b'.repeat(64),
        tuple_ids: ['c'.repeat(64)],
        policy_pins: [
          { kind: 'browser_policy', id: 'pol-browser', revision: 2, sha256: '1'.repeat(64) },
          { kind: 'journey_policy', id: 'pol-journey', revision: 1, sha256: '2'.repeat(64) },
        ],
      },
      baseline: {
        run_id: 'base-run',
        manifest_sha256: 'd'.repeat(64),
        tuple_ids: ['c'.repeat(64)],
        policy_pins: [{ kind: 'browser_policy', id: 'pol-browser', revision: 1, sha256: '3'.repeat(64) }],
      },
    }
    expect(verifyBrowserPanelInputs('cur-run', queryV5, validV2)).toBe(true)
    expect(verifyBrowserPanelInputs('cur-run', queryV5, { ...validV2, version: 1 })).toBe(false)
    expect(verifyBrowserPanelInputs('cur-run', queryV5, { ...validV2, selection_receipt_sha256: 'not-hex' })).toBe(false)
    expect(verifyBrowserPanelInputs('cur-run', queryV5, {
      ...validV2,
      policy_pins: [{ kind: 'browser_policy', id: 'pol-browser', revision: 0, sha256: '1'.repeat(64) }],
    })).toBe(false)
    expect(verifyBrowserPanelInputs('cur-run', queryV5, {
      ...validV2,
      current: { ...validV2.current, policy_pins: [{ kind: 'browser_policy', id: 'pol-browser', revision: 2, sha256: '1'.repeat(64) }] },
    })).toBe(false)
    expect(verifyBrowserPanelInputs('cur-run', queryV5, {
      ...validV2,
      policy_pins: [{ kind: 'browser_policy', id: ' pol-browser ', revision: 2, sha256: '1'.repeat(64) }],
      current: { ...validV2.current, policy_pins: [{ kind: 'browser_policy', id: ' pol-browser ', revision: 2, sha256: '1'.repeat(64) }] },
    })).toBe(false)
    expect(verifyBrowserPanelInputs('cur-run', queryV5, {
      ...validV2,
      current: { ...validV2.current, tuple_ids: ['d'.repeat(64), 'c'.repeat(64)] },
    })).toBe(false)
  })

  it('freezes query_version 5 panels with returned v2 browser_inputs', async () => {
    const returnedPins: BrowserPanelInputs = {
      version: 2,
      query_sha256: 'a'.repeat(64),
      selection_receipt_sha256: 'b'.repeat(64),
      policy_pins: [{ kind: 'browser_policy', id: 'pol-1', revision: 1, sha256: 'c'.repeat(64) }],
      current: {
        run_id: 'run-1',
        manifest_sha256: 'd'.repeat(64),
        tuple_ids: ['e'.repeat(64)],
        policy_pins: [{ kind: 'browser_policy', id: 'pol-1', revision: 1, sha256: 'c'.repeat(64) }],
      },
    }
    request.mockResolvedValue({ browser_inputs: returnedPins })
    const frozen = await freezeBrowserPanelDefinition('run-1', {
      name: 'Comparison panel',
      tabs: [{
        id: 'main',
        title: 'Main',
        panels: [{
          id: 'p1',
          title: 'LCP',
          kind: 'line',
          x: 0,
          y: 0,
          w: 24,
          h: 8,
          query: {
            query_version: 5,
            browser_tuple_mode: 'separate',
            population: 'browser_document',
            metric: 'document_lcp',
            aggregation: 'p75',
          },
        }],
      }],
    })
    expect(frozen.tabs[0].panels[0].query?.browser_inputs).toEqual(returnedPins)
  })
})
