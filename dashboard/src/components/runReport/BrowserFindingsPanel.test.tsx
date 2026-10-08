// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import BrowserFindingsPanel from './BrowserFindingsPanel'
import { browserFindingReasonText, browserScoreUnscoredText, formatBrowserMetric } from './browserFindingsModel'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../../hooks/apiClient', () => ({ request: mocks.request }))

const cohort = { engine: 'chromium', device_profile: 'desktop', frame_scope: 'main_frame', lifecycle_phase: 'navigation' }
const finding = (overrides: Record<string, unknown>) => ({
  id: 'f1', key: 'k1', rule_set: 'browser_findings.v1', rule_id: 'threshold', rule_version: 1, category: 'application_outcome', metric: 'lcp',
  cohort, window: { start: '2026-10-08T10:00:00Z', end: '2026-10-08T10:02:00Z' }, applicable: true, quality: { eligible_final: 7, unqualified_final: 0, unavailable_final: 0, censored: 0, provisional: 0 },
  baseline: { mode: 'none', status: 'not_evaluated' }, evidence: {}, state: 'active', summary: 'LCP p75 is high.', ...overrides,
})
const ready = {
  run_id: 'run-1', status: 'ready', job_id: 'job-1',
  result: {
    schema_version: 'apexload.browser_findings.v1', rule_set: 'browser_findings.v1', input_fingerprint: 'fp', run_id: 'run-1',
    policy: { id: 'browser', name: 'Web vitals', revision: 2, sha256: 'abc', source: 'pinned' }, window: { start: '', end: '' },
    score: {
      version: 'browser_score.v1', severity: 'unscored', unscored_reason: 'missing_required_metric', missing: ['inp'], coverage: { observed: 1, required: 2 },
      components: [
        { metric: 'lcp', weight: 3, required: true, good: 2500, poor: 4000, observed: 4700, samples: 7, score: 0, contribution: 0 },
        { metric: 'inp', weight: 1, required: true, good: 200, poor: 500, samples: 0, reason: 'no_qualified_final_observations' },
      ],
    },
    findings: [
      finding({ id: 's1', state: 'suppressed', reason: 'engine_not_applicable', cohort: { ...cohort, engine: 'webkit' }, summary: 'outside' }),
      finding({ id: 'a1', severity: 'poor', evidence: { documents: [{ document_id: 'abcdef0123456789', source_generation: 'g', value: 4700, resources: '/api' }] } }),
      finding({ id: 'c1', rule_id: 'collection.incomplete', category: 'collection_health', metric: undefined, cohort: undefined, severity: 'needs_improvement', summary: 'Document evidence is incomplete.' }),
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

  it('shows the qualified score, its components and the findings by state', async () => {
    mocks.request.mockResolvedValue(ready)
    act(() => root.render(<BrowserFindingsPanel testId="run-1" projectId="proj" canEvaluate={false} />))
    await flush()
    expect(mocks.request).toHaveBeenCalledWith('/tests/run-1/archive/browser-findings', expect.objectContaining({ signal: expect.any(AbortSignal) }))
    const text = container.textContent || ''
    expect(text).toContain('No overall score: a required metric has no qualified final observation. Missing: INP.')
    expect(text).toContain("1/2 required metrics observed · browser_score.v1 · Web vitals revision 2 (the run's pinned policy)")
    const rows = Array.from(container.querySelectorAll('tbody tr')).map(row => Array.from(row.querySelectorAll('td')).map(cell => cell.textContent))
    expect(rows[0]).toEqual(['LCP', '3', '4700 ms', '2500 ms / 4000 ms', '0.0', '0.0', '7'])
    expect(rows[1]).toEqual(['INP', '1', '—', '200 ms / 500 ms', 'Unscored', '—', '0'])
    const items = Array.from(container.querySelectorAll('.browser-findings-list li'))
    expect(items.map(item => item.getAttribute('data-state'))).toEqual(['active', 'active', 'suppressed'])
    expect(text).toContain('LCP · chromium · desktop — poor. LCP p75 is high. Worst documents: 4700 ms (document …23456789).')
    expect(text).toContain('Document evidence incomplete — needs improvement (collection health).')
    expect(text).toContain('suppressed: outside the policy’s engines')
    expect(container.querySelector('button')).toBeNull()
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
