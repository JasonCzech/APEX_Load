// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import BrowserFindingsPanel from './BrowserFindingsPanel'
import { browserFindingBaselineLabel, browserFindingReasonText, browserScoreUnscoredText, formatBrowserMetric } from './browserFindingsModel'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../../hooks/apiClient', () => ({ request: mocks.request }))

const cohort = { engine: 'chromium', device_profile: 'desktop', frame_scope: 'main_frame', lifecycle_phase: 'navigation' }
const finding = (overrides: Record<string, unknown>) => ({
  id: 'f1', key: 'k1', rule_set: 'browser_findings.v2', rule_id: 'threshold', rule_version: 2, category: 'application_outcome', metric: 'lcp',
  cohort, window: { start: '2026-10-08T10:00:00Z', end: '2026-10-08T10:02:00Z' }, applicable: true, quality: { eligible_final: 7, unqualified_final: 0, unavailable_final: 0, censored: 0, provisional: 0 },
  baseline: {
    mode: 'latest_compatible', status: 'regressed', detail: 'level 10 VUsers: current 4700 ms vs baseline mean 1200 ms (z=4.50)',
    analysis_id: 'rca-1', baseline_run_id: 'base-run-1', baseline_archive_sha256: '0123456789abcdef', selection_receipt_sha256: 'fedcba9876543210',
    rejected_candidates: [{ test_id: 'cand-run-1', digest: '0123456789abcdef', reasons: ['policy_revision_mismatch', 'step_topology_mismatch'] }],
    policy_pins: [{ kind: 'browser_policy', id: 'browser', revision: 2, sha256: 'abc' }, { kind: 'journey_policy', id: 'checkout', revision: 1, sha256: 'def' }],
  },
  evidence: {}, state: 'active', summary: 'LCP p75 is high.', ...overrides,
})
const ready = {
  run_id: 'run-1', status: 'ready', job_id: 'job-1',
  result: {
    schema_version: 'apexload.browser_findings.v2', rule_set: 'browser_findings.v2', input_fingerprint: 'fp', run_id: 'run-1',
    policy: { id: 'browser', name: 'Web vitals', revision: 2, sha256: 'abc', source: 'pinned' }, window: { start: '', end: '' },
    score: {
      version: 'browser_score.v1', severity: 'unscored', unscored_reason: 'missing_required_metric', missing: ['inp'], coverage: { observed: 1, required: 2 },
      components: [
        { metric: 'lcp', weight: 3, required: true, good: 2500, poor: 4000, observed: 4700, samples: 7, score: 0, contribution: 0 },
        { metric: 'inp', weight: 1, required: true, good: 200, poor: 500, samples: 0, reason: 'no_qualified_final_observations' },
      ],
    },
    findings: [
      finding({ id: 's1', state: 'suppressed', reason: 'engine_not_applicable', cohort: { ...cohort, engine: 'webkit' }, baseline: { mode: 'none', status: 'not_selected', reason: 'policy_baseline_off' }, summary: 'outside' }),
      finding({ id: 'a1', severity: 'poor', evidence: { documents: [{ document_id: 'abcdef0123456789', source_generation: 'g', value: 4700, resources: '/api' }] } }),
      finding({ id: 'c1', rule_id: 'collection.incomplete', category: 'collection_health', metric: undefined, cohort: undefined, baseline: { mode: 'latest_compatible', status: 'not_applicable' }, severity: 'needs_improvement', summary: 'Document evidence is incomplete.' }),
    ],
    counts: { active: 2, suppressed: 1, unscored: 0 }, notes: ['Only qualified final observations of main-frame navigations are judged.'],
  },
}

async function flush() {
  await act(async () => { await Promise.resolve() })
  await act(async () => { await Promise.resolve() })
}

describe('BrowserFindingsPanel', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    mocks.request.mockReset()
  })
  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it('offers no evaluation for a run that pinned no browser policy', async () => {
    mocks.request.mockResolvedValue({ run_id: 'run-1', status: 'not_evaluated' })
    act(() => root.render(<BrowserFindingsPanel testId="run-1" projectId="proj" canEvaluate browserPolicyPinned={false} />))
    await flush()
    expect(container.textContent).toContain('It pinned no browser policy, so the run report cannot evaluate it')
    expect(Array.from(container.querySelectorAll('button')).some(button => button.textContent === 'Evaluate with the pinned policy')).toBe(false)
  })

  it('shows the qualified score, its components and the findings by state', async () => {
    mocks.request.mockResolvedValue(ready)
    act(() => root.render(<BrowserFindingsPanel testId="run-1" projectId="proj" canEvaluate={false} />))
    await flush()
    expect(mocks.request).toHaveBeenCalledWith('/tests/run-1/archive/browser-findings', expect.objectContaining({ signal: expect.any(AbortSignal) }))
    const text = container.textContent || ''
    expect(text).toContain('No overall score: a required metric has no qualified final observation. Missing: INP.')
    expect(text).toContain("1/2 required metrics observed · browser_score.v1 · Web vitals revision 2 (the run's pinned policy) · sha256 …abc.")
    const rows = Array.from(container.querySelectorAll('tbody tr')).map(row => Array.from(row.querySelectorAll('td')).map(cell => cell.textContent))
    expect(rows[0]).toEqual(['LCP', '3', '4700 ms', '2500 ms / 4000 ms', '0.0', '0.0', '7'])
    expect(rows[1]).toEqual(['INP', '1', '—', '200 ms / 500 ms', 'Unscored', '—', '0'])
    const items = Array.from(container.querySelectorAll('.browser-findings-list li'))
    expect(items.map(item => item.getAttribute('data-state'))).toEqual(['active', 'active', 'suppressed'])
    expect(items.map(item => item.getAttribute('data-baseline-status'))).toEqual(['not_applicable', 'regressed', 'not_selected'])
    expect(text).toContain('LCP · chromium · desktop — poor. LCP p75 is high. Worst documents: 4700 ms (document …23456789).')
    expect(text).toContain('Baseline (latest_compatible): regressed — level 10 VUsers: current 4700 ms vs baseline mean 1200 ms (z=4.50).')
    expect(text).toContain('Baseline run base-run-1 (archive …89abcdef)')
    expect(text).toContain('RCA rca-1')
    expect(text).toContain('selection receipt …76543210')
    expect(text).toContain('rejected: cand-run-1 (policy revision mismatch, step topology mismatch) […89abcdef]')
    expect(text).toContain('pins: browser_policy:browser@2, journey_policy:checkout@1')
    expect(text).toContain('Baseline (none): not selected (policy baseline off).')
    expect(text).toContain('Document evidence incomplete — needs improvement (collection health).')
    expect(text).toContain('suppressed: outside the policy’s engines')
    expect(container.querySelector('button')).toBeNull()
    expect(browserFindingBaselineLabel({ mode: 'latest_compatible', status: 'not_applicable' })).toBe('')
  })

  it('lets an editor queue an evaluation of a run without one', async () => {
    mocks.request.mockResolvedValueOnce({ run_id: 'run-1', status: 'not_evaluated' })
    act(() => root.render(<BrowserFindingsPanel testId="run-1" projectId="proj" canEvaluate />))
    await flush()
    expect(container.textContent).toContain('This run has no browser evaluation.')
    mocks.request.mockResolvedValueOnce({ id: 'job-1', state: 'queued' }).mockResolvedValueOnce({ run_id: 'run-1', status: 'pending', pending: true })
    await act(async () => { container.querySelector('button')?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    await flush()
    expect(mocks.request).toHaveBeenCalledWith('/analysis/evaluations?project_id=proj', expect.objectContaining({ method: 'POST', body: JSON.stringify({ kind: 'browser_intelligence', request: { run: 'run-1' } }) }))
    expect(container.textContent).toContain('A browser evaluation is queued or running.')
    expect((container.querySelector('button') as HTMLButtonElement).disabled).toBe(true)
  })

  it('checks a pending evaluation again and shows a failed one', async () => {
    vi.useFakeTimers()
    try {
      mocks.request.mockResolvedValueOnce({ run_id: 'run-1', status: 'pending', pending: true }).mockResolvedValueOnce(ready)
      act(() => root.render(<BrowserFindingsPanel testId="run-1" projectId="proj" canEvaluate={false} />))
      await act(async () => { await Promise.resolve() })
      await act(async () => { await Promise.resolve() })
      expect(container.textContent).toContain('queued or running')
      await act(async () => { vi.advanceTimersByTime(3000) })
      await act(async () => { await Promise.resolve() })
      await act(async () => { await Promise.resolve() })
      expect(mocks.request).toHaveBeenCalledTimes(2)
      expect(container.textContent).toContain('Web vitals revision 2')
    } finally {
      vi.useRealTimers()
    }
    mocks.request.mockResolvedValue({ run_id: 'run-2', status: 'failed', failure: 'the pinned browser policy revision no longer matches its pin' })
    act(() => root.render(<BrowserFindingsPanel testId="run-2" projectId="proj" canEvaluate={false} />))
    await flush()
    expect(container.textContent).toContain('The browser evaluation failed: the pinned browser policy revision no longer matches its pin.')
  })

  it('reports load failures and formats values', async () => {
    mocks.request.mockRejectedValue(new Error('boom'))
    act(() => root.render(<BrowserFindingsPanel testId="run-1" projectId="proj" canEvaluate={false} />))
    await flush()
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('boom')
    expect(formatBrowserMetric('cls', 0.1234)).toBe('0.123')
    expect(formatBrowserMetric('lcp', undefined)).toBe('—')
    expect(browserScoreUnscoredText('no_document_evidence')).toContain('no document evidence')
    expect(browserFindingReasonText('other')).toBe('other')
  })
})
