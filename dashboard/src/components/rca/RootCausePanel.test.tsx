// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { RootCauseRouteState } from '../../appRouting'
import type { RunArchiveReport } from '../../types'
import type { ExemplarPage, RcaAnalysisResponse } from '../../types/rca'
import RootCausePanel from './RootCausePanel'
import { normalizeRcaAssessment } from './rcaModel'
import { rcaTestResponse } from './rcaTestFixture'
import type { RootCauseAnalysisModel } from './useRootCauseAnalysis'

const mocks = vi.hoisted(() => ({ api: { getTestArchiveExemplars: vi.fn(), downloadTestArchiveArtifact: vi.fn(), getRootCauseCritiques: vi.fn() } }))
vi.mock('../../hooks/useApi', () => ({ useApi: () => mocks.api }))

const exemplar = (timestamp: string, trace: string) => ({
  population: 'transaction', name: 'Checkout', interval_start: timestamp, interval_end: timestamp, reason: 'slowest', trace_id: trace,
  duration_ms: 3100, success: false, status_code: 503, timestamp,
  links: [{ provider: 'dynatrace', connection_id: 'dt-1', url: `https://dt.example/trace/${trace}` }],
})

const report = {
  by_transaction: { Checkout: { transactions: 1200, errors: 12, error_pct: 1, avg_response_ms: 900, min_response_ms: 100, max_response_ms: 4000, p95_ms: 3100 } },
  error_patterns: [
    { signature: 'checkout-503', sample: 'HTTP 503 from /cart/checkout', count: 12, first_seen: '2026-10-05T12:03:40Z', latest_seen: '2026-10-05T12:05:10Z', actions: ['Checkout'] },
    { signature: 'login-401', sample: 'HTTP 401 from /login', count: 3, first_seen: '2026-10-05T12:03:40Z', latest_seen: '2026-10-05T12:04:10Z', actions: ['Login'] },
  ],
} as unknown as RunArchiveReport

function model(response: RcaAnalysisResponse | null, overrides: Partial<RootCauseAnalysisModel> = {}): RootCauseAnalysisModel {
  return {
    response: response?.assessment ? { ...response, assessment: normalizeRcaAssessment(response.assessment) } : response,
    loading: false, error: '', requesting: false, requestError: '',
    request: vi.fn(), reload: vi.fn(), ...overrides,
  }
}

describe('RootCausePanel', () => {
  let container: HTMLDivElement
  let root: Root
  const onRouteChange = vi.fn()

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    onRouteChange.mockReset()
    mocks.api.getRootCauseCritiques.mockReset().mockResolvedValue({ critiques: [] })
    mocks.api.getTestArchiveExemplars.mockReset().mockResolvedValue({
      exemplars: [exemplar('2026-10-05T12:04:00Z', 'trace-in-1'), exemplar('2026-10-05T12:05:30Z', 'trace-in-2')],
      total: 2,
    } satisfies ExemplarPage)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  const render = async (m: RootCauseAnalysisModel, route: RootCauseRouteState = {}, canAnalyze = true) => {
    await act(async () => {
      root.render(<RootCausePanel testId="run-1" model={m} report={report} history={[]} canAnalyze={canAnalyze} route={route} onRouteChange={onRouteChange} />)
    })
  }
  const button = (name: string) => Array.from(container.querySelectorAll('button')).find(b => b.textContent === name)

  it('presents the ranked hypotheses with what would disprove each', async () => {
    await render(model(rcaTestResponse()))
    const text = container.textContent || ''
    expect(text).toContain('Supported hypotheses')
    expect(text).toContain('Validity unknown')
    expect(text).toContain('no load generator statistics were archived')
    const cards = container.querySelectorAll('.rca-hypothesis')
    expect(cards).toHaveLength(2)
    expect(cards[0].getAttribute('aria-label')).toBe('Hypothesis 1: Resource pressure')
    expect(cards[0].textContent).toContain('Medium confidence')
    expect(cards[0].textContent).toContain('builtin:service.cpu.time · Dynatrace (+60 pts) began before Checkout · p95 latency')
    expect(cards[0].textContent).toContain('Would disprove it: CPU stays below saturation while latency rises')
    expect(cards[0].textContent).toContain('Contradicting evidence: None found.')
    expect(cards[0].textContent).toContain('Also supported: Garbage collection')
    expect(cards[1].textContent).toContain('jvm_gc_seconds_rate')
    expect(text).toContain('Evidence coverage (1 gaps)')
    expect(text).toContain('Validity caps confidence')
  })

  it('pins the analysis, hypothesis and window in the route when investigating', async () => {
    await render(model(rcaTestResponse()))
    await act(async () => button('Investigate')!.click())
    expect(onRouteChange).toHaveBeenCalledWith({ analysis: 'rca-1', rev: '2', hypothesis: 'hypothesis-cpu', window: '2026-10-05T12:01:25.000Z~2026-10-05T12:06:55.000Z' })
  })

  it('drills into the routed hypothesis within its window', async () => {
    await render(model(rcaTestResponse()), { analysis: 'rca-1', rev: '2', hypothesis: 'hypothesis-cpu', window: '2026-10-05T12:01:25.000Z~2026-10-05T12:06:55.000Z' })
    expect(mocks.api.getTestArchiveExemplars).toHaveBeenCalledWith('run-1', { name: 'Checkout', population: 'transaction', from: '2026-10-05T12:01:25.000Z', to: '2026-10-05T12:06:55.000Z', limit: 1000 },
      expect.objectContaining({ signal: expect.anything() }))
    const investigation = container.querySelector('.rca-investigation')!
    const text = investigation.textContent || ''
    expect(text).toContain('Symptom: Checkout · p95 latency rose +300 ms from 2026-10-05 12:03:20 UTC ± 10 s')
    expect(text).toContain('Cause: builtin:service.cpu.time · Dynatrace rose +60 pts')
    // Without archived phase events, the load steps are inferred from the
    // detection levels: the first is before the window, the second inside.
    expect(text).toContain('Load step to 20 VUsers at 2026-10-05 12:02:00 UTC')
    expect(text).not.toContain('Load step to 10 VUsers')
    expect(text).toContain('the load steps are inferred from its steady load levels')
    expect(mocks.api.downloadTestArchiveArtifact).not.toHaveBeenCalled()
    expect(text).toContain('Components: cart-service')
    expect(text).toContain('Suspect run from 2026-10-05 12:04:30 UTC to 2026-10-05 12:04:40 UTC')
    const traces = Array.from(investigation.querySelectorAll('a.rca-trace-link')).map(a => a.getAttribute('href'))
    expect(traces).toEqual(['https://dt.example/trace/trace-in-1', 'https://dt.example/trace/trace-in-2'])
    expect(investigation.querySelector('a.rca-trace-link')!.getAttribute('rel')).toBe('noopener noreferrer')
    expect(text).toContain('HTTP 503 from /cart/checkout')
    expect(text).not.toContain('HTTP 401 from /login')
    expect(text).toContain('1,200')

    await act(async () => button('Whole run')!.click())
    expect(onRouteChange).toHaveBeenLastCalledWith({ analysis: 'rca-1', rev: '2', hypothesis: 'hypothesis-cpu', window: undefined })
    await render(model(rcaTestResponse()), { analysis: 'rca-1', rev: '2', hypothesis: 'hypothesis-cpu' })
    expect(mocks.api.getTestArchiveExemplars).toHaveBeenLastCalledWith('run-1', { name: 'Checkout', population: 'transaction', from: undefined, to: undefined, limit: 1000 }, expect.anything())
    await act(async () => button('Close investigation')!.click())
    expect(onRouteChange).toHaveBeenLastCalledWith({ analysis: 'rca-1', rev: '2', hypothesis: undefined, window: undefined })
  })

  it('marks the archived load-phase events on the timeline', async () => {
    let release!: (blob: Blob) => void
    mocks.api.downloadTestArchiveArtifact.mockReturnValue(new Promise<Blob>(resolve => { release = resolve }))
    await act(async () => {
      root.render(<RootCausePanel testId="run-1" model={model(rcaTestResponse())} report={{ ...report, manifest: { phase_events_file: 'phase_events.jsonl', phase_events_truncated: true } } as unknown as RunArchiveReport}
        history={[]} canAnalyze route={{ analysis: 'rca-1', rev: '2', hypothesis: 'hypothesis-cpu', window: '2026-10-05T12:01:25.000Z~2026-10-05T12:06:55.000Z' }} onRouteChange={onRouteChange} />)
    })
    expect(mocks.api.downloadTestArchiveArtifact).toHaveBeenCalledWith('run-1', 'phase_events.jsonl', expect.objectContaining({ signal: expect.anything() }))
    // Nothing is inferred while the archived events load.
    expect(container.querySelector('.rca-investigation')!.textContent).not.toContain('Load step')
    await act(async () => release(new Blob(['{"kind":"level_change","level":20,"target":"checkout","at":"2026-10-05T12:02:05Z"}\n'])))
    const text = container.querySelector('.rca-investigation')!.textContent || ''
    expect(text).toContain('checkout stepped to 20 VUsers at 2026-10-05 12:02:05 UTC')
    expect(text).not.toContain('Load step to 20 VUsers')
    expect(text).not.toContain('inferred')
    expect(text).toContain('later ones are missing')
  })

  it('says when a link names a hypothesis or revision it does not have', async () => {
    await render(model(rcaTestResponse({ revision: 3 })), { analysis: 'rca-1', rev: '2', hypothesis: 'hypothesis-gone' })
    expect(container.textContent).toContain('The link named revision 2; showing revision 3.')
    expect(container.textContent).toContain('The linked hypothesis is not in this analysis.')
    expect(container.querySelector('.rca-investigation')).toBeNull()
  })

  it('lists the analysis\'s critiques apart from the assessment', async () => {
    mocks.api.getRootCauseCritiques.mockResolvedValue({ critiques: [
      { id: 'c1', analysis_id: 'rca-1', revision: 2, created_at: '2026-10-05T13:00:00Z', created_by: 'service-account:sa-1', author_kind: 'agent', model: 'deterministic-local', narrative: 'CPU rose 60 points before Checkout slowed.' },
    ] })
    await render(model(rcaTestResponse()))
    expect(mocks.api.getRootCauseCritiques).toHaveBeenCalledWith('run-1', 'rca-1', expect.anything())
    const critiques = container.querySelector('.rca-critiques')!
    expect(critiques.textContent).toContain('Critiques (1)')
    expect(critiques.textContent).toContain('Agent (deterministic-local) · service-account:sa-1')
    expect(critiques.textContent).toContain('CPU rose 60 points before Checkout slowed.')
  })

  it('stays hidden where the project does not present v2', async () => {
    await render(model(rcaTestResponse({ presented: false })))
    expect(container.innerHTML).toBe('')
    // Before any response, a load or a failure shows only for a link that
    // names an analysis.
    await render(model(null, { loading: true, error: 'root-cause analysis storage unavailable' }))
    expect(container.innerHTML).toBe('')
    await render(model(null, { error: 'root-cause analysis not found' }), { analysis: 'rca-9' })
    expect(container.querySelector('[role="alert"]')!.textContent).toContain('root-cause analysis not found')
  })

  it('queues a new analysis and routes to it, or reloads the one already shown', async () => {
    const request = vi.fn().mockResolvedValueOnce({ analysis_id: 'rca-2', revision: 3, state: 'queued', presented: true })
    const reload = vi.fn()
    await act(async () => {
      root.render(<RootCausePanel testId="run-1" model={model(rcaTestResponse(), { request, reload })} report={report} history={[]} canAnalyze route={{ analysis: 'rca-1', rev: '2' }}
        baselineTestId="run-0" baselineName="Release 2 soak" onRouteChange={onRouteChange} />)
    })
    expect(container.textContent).toContain('Against baseline Release 2 soak')
    await act(async () => { (container.querySelector('.rca-enrich input') as HTMLInputElement).click() })
    await act(async () => button('Re-analyze')!.click())
    expect(request).toHaveBeenCalledWith({ enrich: true, baseline_test_id: 'run-0' })
    expect(onRouteChange).toHaveBeenLastCalledWith({ analysis: 'rca-2', rev: '3', hypothesis: undefined, window: undefined })

    request.mockResolvedValueOnce({ analysis_id: 'rca-1', revision: 2, state: 'completed', presented: true })
    await act(async () => button('Re-analyze')!.click())
    expect(reload).toHaveBeenCalled()
  })

  it('reports pending, empty and failed analyses and hides analysis for viewers', async () => {
    await render(model({ state: 'none', presented: true }), {}, false)
    expect(container.textContent).toContain('No root-cause analysis has completed for this run.')
    expect(button('Analyze')).toBeUndefined()
    await render(model({ analysis_id: 'rca-2', revision: 3, state: 'running', presented: true }))
    expect(container.querySelector('[role="status"]')!.textContent).toBe('Analysis revision 3 is running…')
    expect(button('Re-analyze')!.disabled).toBe(true)
    await render(model({ analysis_id: 'rca-2', revision: 3, state: 'failed', error: 'budget exhausted', presented: true }))
    expect(container.querySelector('[role="alert"]')!.textContent).toBe('The analysis failed: budget exhausted.')
  })
})
