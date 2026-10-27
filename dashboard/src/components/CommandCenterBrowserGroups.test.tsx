// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import CommandCenterBrowserGroups from './CommandCenterBrowserGroups'
import type { CommandGroup } from './commandCenterScenarioRows'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../hooks/apiClient', () => ({ request: mocks.request }))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const sampleGroup: CommandGroup = {
  key: 'Shop',
  name: 'Shop',
  protocol: 'browser',
  color: '#2563eb',
  current: 2,
  target: 2,
  targetVusers: 2,
  targetMode: 'vusers',
  rate: 1.5,
  tps: 1.5,
  requestRate: 18,
  journeysPerMin: 12,
  art: 110,
  p99: 150,
  errorPct: 0,
  attempts: 10,
  errors: 0,
}

describe('CommandCenterBrowserGroups provisional live intelligence', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    mocks.request.mockReset()
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })

  it('displays provisional live findings, interactions, and journeys and updates when finalized', async () => {
    vi.useFakeTimers()
    try {
      mocks.request.mockImplementation((path: string) => {
        if (path.endsWith('/browser-findings')) {
          return Promise.resolve({
            run_id: 'run-live',
            status: 'ready',
            provisional: true,
            live_truncated: true,
            result: {
              schema_version: 'apexload.browser_findings.v2',
              rule_set: 'browser_findings.v2',
              input_fingerprint: 'fp',
              run_id: 'run-live',
              provisional: true,
              live_truncated: true,
              policy: { id: 'pol-b', name: 'Checkout Web Vitals', revision: 1, sha256: 'abc', source: 'pinned' },
              window: { start: '', end: '' },
              score: { version: 'browser_score.v1', score: 82.5, severity: 'needs_improvement', components: [], coverage: { observed: 2, required: 2 } },
              findings: [],
              counts: { active: 1, suppressed: 0, unscored: 0 },
              notes: [],
            },
          })
        }
        if (path.endsWith('/browser-interactions')) {
          return Promise.resolve({
            run_id: 'run-live',
            version: 'browser_interaction_patterns.v4',
            status: 'ready',
            provisional: true,
            thresholds: { repeat_min_gestures: 3, repeat_window_ms: 2000, repeat_radius_px: 32, heatmap_grid: 64 },
            coverage: { interactions: 14, records: 2, dropped: 0, incomplete: false, by_provenance: { scripted: 14 }, mapped: 14 },
            patterns: [{ id: 'p1', kind: 'dead_interaction', provenance: 'scripted', count: 1, start: '', end: '', detail: 'timeout', interactions: ['i1'] }],
            heatmaps: [{ key: 'h1', route: '/cart', layout_revision: 'a', viewport_width: 1280, viewport_height: 720, orientation: 'landscape', engine: 'chromium', device_profile: 'desktop', columns: 64, rows: 64, cells: [[1, 1, 2]], points: 2, by_kind: { click: 2 }, by_provenance: { scripted: 2 } }],
            notes: [],
          })
        }
        return Promise.resolve({
          schema_version: 'apexload.journey_risk.v1',
          run_id: 'run-live',
          provisional: true,
          policy: { id: 'pol-j', name: 'Checkout Journeys', revision: 1, sha256: 'def', source: 'pinned' },
          availability: 'available',
          journeys: [{ id: 'j1', name: 'Buy flow', weight: 1, start: { transaction: 'Browse' }, success: { transaction: 'Pay' }, completed: 4, failed: 1, aborted: 0, skipped: 0, censored: 1, eligible: 5, steps: 10, step_failures: 1, failure_rate: 0.2 }],
          weighted_failure_index: 0.2,
          step_risk_enabled: false,
          rows_outside_iterations: 0,
          notes: [],
        })
      })

      await act(async () => {
        root.render(
          <CommandCenterBrowserGroups
            testId="run-live"
            groups={[sampleGroup]}
            rateWindow="10 s avg"
            requestsCounted
            inRunRequestRate
          />,
        )
      })
      await act(async () => { await Promise.resolve() })
      await act(async () => { await Promise.resolve() })

      const liveBox = container.querySelector('.command-browser-live-intelligence')
      expect(liveBox?.getAttribute('data-provisional')).toBe('true')
      expect(liveBox?.textContent).toContain('Live browser intelligence (provisional — non-verdict)')
      expect(liveBox?.textContent).toContain('live_truncated')
      expect(container.querySelector('[data-testid="live-browser-findings-summary"]')?.textContent).toContain('Score 82.5/100 (needs improvement) · 1 active findings · baseline not evaluated live')
      expect(container.querySelector('[data-testid="live-browser-interactions-summary"]')?.textContent).toContain('14 gestures · 1 patterns · 1 heatmaps')
      expect(container.querySelector('[data-testid="live-browser-journeys-summary"]')?.textContent).toContain('Buy flow: 4 completed, 1 failed, 1 censored')

      // Transition to finalized archive responses on the next poll tick.
      mocks.request.mockImplementation((path: string) => {
        if (path.endsWith('/browser-findings')) {
          return Promise.resolve({
            run_id: 'run-live',
            status: 'ready',
            provisional: false,
            live_truncated: false,
            result: {
              schema_version: 'apexload.browser_findings.v2',
              rule_set: 'browser_findings.v2',
              input_fingerprint: 'fp-final',
              run_id: 'run-live',
              provisional: false,
              live_truncated: false,
              policy: { id: 'pol-b', name: 'Checkout Web Vitals', revision: 1, sha256: 'abc', source: 'pinned' },
              window: { start: '', end: '' },
              score: { version: 'browser_score.v1', score: 91.0, severity: 'good', components: [], coverage: { observed: 2, required: 2 } },
              findings: [],
              counts: { active: 0, suppressed: 0, unscored: 0 },
              notes: [],
            },
          })
        }
        if (path.endsWith('/browser-interactions')) {
          return Promise.resolve({
            run_id: 'run-live',
            version: 'browser_interaction_patterns.v4',
            status: 'ready',
            provisional: false,
            thresholds: { repeat_min_gestures: 3, repeat_window_ms: 2000, repeat_radius_px: 32, heatmap_grid: 64 },
            coverage: { interactions: 20, records: 3, dropped: 0, incomplete: false, by_provenance: { scripted: 20 }, mapped: 20 },
            patterns: [],
            heatmaps: [{ key: 'h1', route: '/cart', layout_revision: 'a', viewport_width: 1280, viewport_height: 720, orientation: 'landscape', engine: 'chromium', device_profile: 'desktop', columns: 64, rows: 64, cells: [[1, 1, 2]], points: 2, by_kind: { click: 2 }, by_provenance: { scripted: 2 } }],
            notes: [],
          })
        }
        return Promise.resolve({
          schema_version: 'apexload.journey_risk.v1',
          run_id: 'run-live',
          provisional: false,
          policy: { id: 'pol-j', name: 'Checkout Journeys', revision: 1, sha256: 'def', source: 'pinned' },
          availability: 'available',
          journeys: [{ id: 'j1', name: 'Buy flow', weight: 1, start: { transaction: 'Browse' }, success: { transaction: 'Pay' }, completed: 6, failed: 0, aborted: 0, skipped: 0, censored: 0, eligible: 6, steps: 12, step_failures: 0, failure_rate: 0 }],
          weighted_failure_index: 0,
          step_risk_enabled: false,
          rows_outside_iterations: 0,
          notes: [],
        })
      })

      await act(async () => {
        vi.advanceTimersByTime(5000)
      })
      await act(async () => { await Promise.resolve() })
      await act(async () => { await Promise.resolve() })

      const finalizedBox = container.querySelector('.command-browser-live-intelligence')
      expect(finalizedBox?.getAttribute('data-provisional')).toBe('false')
      expect(finalizedBox?.textContent).toContain('Browser intelligence (finalized archive)')
      expect(finalizedBox?.textContent).not.toContain('live_truncated')
      expect(finalizedBox?.textContent).not.toContain('baseline not evaluated live')
      expect(container.querySelector('[data-testid="live-browser-findings-summary"]')?.textContent).toContain('Score 91.0/100 (good) · 0 active findings')

      const callsAfterFinalize = mocks.request.mock.calls.length
      await act(async () => {
        vi.advanceTimersByTime(10000)
      })
      expect(mocks.request.mock.calls.length).toBe(callsAfterFinalize)
    } finally {
      vi.useRealTimers()
    }
  })
})
