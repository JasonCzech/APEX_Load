// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { TelemetryComparison, TelemetryRunProfile } from './model'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const request = vi.fn()
vi.mock('../../hooks/apiClient', () => ({ request }))
const { default: BaselineTelemetryCompatibility, POLL_RETRIES } = await import('./BaselineTelemetryCompatibility')

const profile = (run_id: string, cadence_ms: number): TelemetryRunProfile => ({
  run_id, policy_availability: 'available', policy_hash: 'a'.repeat(64), cadence_ms, cadence_scope: 'local-controller-ticker',
  sustained_breach_window_ms: 15000, budgets: [{ stage: 'controller_fanout', enabled: true, limit_ms: 250 }], source_capabilities: [],
})
const comparable: TelemetryComparison = { baseline_run_id: 'base-a', scope: 'whole_run', comparable: true, reasons: [], current: profile('current', 1000), baseline: profile('base-a', 1000), breached_interval_pct_delta: 8, health_coverage_pct_delta: 0 }
const cadence: TelemetryComparison = { baseline_run_id: 'base-b', scope: 'whole_run', comparable: false, reasons: ['cadence_differs'], current: profile('current', 1000), baseline: profile('base-b', 3000) }

const result = {
  schema_version: 'apexload.telemetry_reevaluation.v1', algorithm: 'telemetry-budget-reevaluation.v1', input_fingerprint: 'f'.repeat(64), all_answerable: false,
  policy: { id: 'common', name: 'Common', revision: 2, sha256: 'c'.repeat(64), sustained_breach_window_ms: 15000, budgets: [] },
  runs: [
    { run_id: 'current', manifest_sha256: 'm', answerable: true, original: { intervals: 5, healthy: 2, degraded: 3, breached: 0, unscored: 0, unknown: 0 }, reevaluated: { intervals: 5, healthy: 4, degraded: 1, breached: 0, unscored: 0, unknown: 0, breached_classified_pct: 0 } },
    { run_id: 'base-b', manifest_sha256: 'n', answerable: false, unanswerable: ['no_classifiable_intervals'], stage_unscored: { 'sample_age:clock_quality_not_retained': 5, 'interval:no_scored_stage': 1 }, original: { intervals: 1, healthy: 0, degraded: 0, breached: 0, unscored: 0, unknown: 1 } },
  ],
  notes: ['A derived result: each run’s archive and original classification are unchanged.'],
}

describe('BaselineTelemetryCompatibility', () => {
  let node: HTMLDivElement
  beforeEach(() => {
    vi.useFakeTimers()
    request.mockReset()
    node = document.createElement('div')
  })
  afterEach(() => { vi.useRealTimers() })

  it('shows deltas only for comparable runs and both profiles with the reason otherwise', async () => {
    request.mockResolvedValue([])
    const root = createRoot(node)
    await act(async () => root.render(<BaselineTelemetryCompatibility comparisons={[comparable, cadence]} projectId="proj" />))
    const sections = node.querySelectorAll('.bc-telemetry-compatibility')
    expect(sections[0].textContent).toContain('comparable')
    expect(sections[0].textContent).toContain('+8.00 pts')
    expect(sections[1].textContent).toContain('not comparable')
    expect(sections[1].textContent).toContain('different publication cadence')
    expect(sections[1].textContent).toContain('1000 ms (local-controller-ticker)')
    expect(sections[1].textContent).toContain('3000 ms (local-controller-ticker)')
    expect(sections[1].textContent).not.toContain('pts')
    expect(node.textContent).toContain('no telemetry policy yet')
    expect(request).toHaveBeenCalledWith('/analysis/policies?kind=telemetry_policy&project_id=proj', expect.anything())
    await act(async () => root.unmount())
  })

  it('shows each run\'s own telemetry verdict and dropped results side by side', async () => {
    request.mockResolvedValue([])
    const judged: TelemetryComparison = {
      ...cadence,
      current: { ...profile('current', 1000), acceptance: { outcome: 'fail', mode: 'fail_ci', policy: { kind: 'telemetry_acceptance_policy', id: 'gate', name: 'Gate', revision: 3, sha256: 'abc' }, criteria: [] }, loss: { dropped_results: { state: 'lower_bound', value: 4 } } },
    }
    const root = createRoot(node)
    await act(async () => root.render(<BaselineTelemetryCompatibility comparisons={[judged]} projectId="proj" />))
    const cells = [...node.querySelectorAll('.bc-telemetry-compatibility tbody tr')].map(row => [...row.querySelectorAll('td')].map(cell => cell.textContent))
    expect(cells[0].slice(-2)).toEqual(['fail (fail_ci, Gate r3)', '≥ 4 (lower bound)'])
    expect(cells[1].slice(-2)).toEqual(['No acceptance policy', 'Unavailable'])
    await act(async () => root.unmount())
  })

  it('queues a re-evaluation of every run, polls it and shows the derived result', async () => {
    request.mockImplementation(async (path: string, options?: { method?: string; body?: string }) => {
      if (path.startsWith('/analysis/policies')) return [{ id: 'common', name: 'Common', revision: 2 }, { id: 'old', name: 'Old', revision: 1, deleted: true }]
      if (options?.method === 'POST') return { id: 'job-1', state: 'queued', input_fingerprint: 'f' }
      return { id: 'job-1', state: 'ready', input_fingerprint: 'f', result_sha256: 'r'.repeat(64), result }
    })
    const root = createRoot(node)
    await act(async () => root.render(<BaselineTelemetryCompatibility comparisons={[comparable, cadence]} projectId="proj" />))
    const select = node.querySelector<HTMLSelectElement>('select')!
    expect([...select.options].map(option => option.value)).toEqual(['common'])
    await act(async () => { node.querySelector<HTMLButtonElement>('.bc-telemetry-reevaluation button')!.click() })
    const create = request.mock.calls.find(call => call[1]?.method === 'POST')!
    expect(create[0]).toBe('/analysis/evaluations?project_id=proj')
    expect(JSON.parse(create[1].body)).toEqual({ kind: 'telemetry_acceptance', request: { runs: ['current', 'base-a', 'base-b'], policy: { id: 'common', revision: 2 } } })
    expect(node.textContent).toContain('Re-evaluation queued')
    await act(async () => { vi.advanceTimersByTime(2000) })
    const region = node.querySelector('[aria-label="Telemetry re-evaluation result"]')!
    expect(region.textContent).toContain('Common revision 2')
    expect(region.querySelector('tr[data-run="current"]')!.textContent).toContain('4 healthy · 1 degraded')
    expect(region.querySelector('tr[data-run="base-b"]')!.textContent).toContain('No: no_classifiable_intervals (sample age:clock quality not retained ×5, interval:no scored stage ×1)')
    expect(region.textContent).toContain('original classification are unchanged')
    await act(async () => root.unmount())
  })

  it('reports a failed re-evaluation and renders nothing without comparisons', async () => {
    request.mockImplementation(async (path: string, options?: { method?: string }) => {
      if (path.startsWith('/analysis/policies')) return [{ id: 'common', name: 'Common', revision: 1 }]
      if (options?.method === 'POST') return { id: 'job-2', state: 'failed', input_fingerprint: 'f', failure: 'run base-b has no finalized archive' }
      throw new Error('not polled')
    })
    const root = createRoot(node)
    await act(async () => root.render(<BaselineTelemetryCompatibility comparisons={[cadence]} projectId="proj" />))
    await act(async () => { node.querySelector<HTMLButtonElement>('.bc-telemetry-reevaluation button')!.click() })
    expect(node.textContent).toContain('Re-evaluation failed: run base-b has no finalized archive')
    await act(async () => root.render(<BaselineTelemetryCompatibility comparisons={[]} projectId="proj" />))
    expect(node.innerHTML).toBe('')
    await act(async () => root.unmount())
  })

  it('retries a failed poll, then frees the panel, and starts over for another run set', async () => {
    let polls = 0
    request.mockImplementation(async (path: string, options?: { method?: string }) => {
      if (path.startsWith('/analysis/policies')) return [{ id: 'common', name: 'Common', revision: 1 }]
      if (options?.method === 'POST') return { id: 'job-3', state: 'queued', input_fingerprint: 'f' }
      polls++
      throw new Error('network down')
    })
    const root = createRoot(node)
    await act(async () => root.render(<BaselineTelemetryCompatibility comparisons={[comparable]} projectId="proj" />))
    await act(async () => { node.querySelector<HTMLButtonElement>('.bc-telemetry-reevaluation button')!.click() })
    await act(async () => { vi.advanceTimersByTime(2000) })
    expect(node.textContent).toContain('network down; retrying')
    expect(node.querySelector<HTMLButtonElement>('.bc-telemetry-reevaluation button')!.disabled).toBe(true)
    for (let attempt = 1; attempt <= POLL_RETRIES; attempt++) {
      await act(async () => { vi.advanceTimersByTime(2000 * 2 ** attempt) })
    }
    expect(polls).toBe(POLL_RETRIES + 1)
    expect(node.querySelector('[role="alert"]')!.textContent).toBe('network down')
    expect(node.querySelector<HTMLButtonElement>('.bc-telemetry-reevaluation button')!.disabled).toBe(false)

    // Another run set mounts a fresh panel: the old job's status is gone.
    await act(async () => { node.querySelector<HTMLButtonElement>('.bc-telemetry-reevaluation button')!.click() })
    expect(node.textContent).toContain('Re-evaluation queued')
    await act(async () => root.render(<BaselineTelemetryCompatibility comparisons={[cadence]} projectId="proj" />))
    expect(node.textContent).not.toContain('Re-evaluation queued')
    const before = polls
    await act(async () => { vi.advanceTimersByTime(60_000) })
    expect(polls).toBe(before)
    await act(async () => root.unmount())
  })
})
