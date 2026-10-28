// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { RootCauseRouteState } from '../../appRouting'
import type { RunArchiveReport } from '../../types'
import type { ExemplarPage, RcaAnalysisResponse } from '../../types/rca'
import RootCausePanel from './RootCausePanel'
import { formatRcaClock, formatRcaTime, normalizeRcaAssessment } from './rcaModel'
import { rcaTestResponse, rcaTestSummary } from './rcaTestFixture'
import type { RcaHypothesisActions } from './RootCausePanel'
import type { RootCauseAnalysisModel } from './useRootCauseAnalysis'

const mocks = vi.hoisted(() => ({ api: { getTestArchiveExemplars: vi.fn(), downloadTestArchiveArtifact: vi.fn(), getRootCauseCritiques: vi.fn(), getRootCauseWindowLinks: vi.fn() } }))
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
    mocks.api.getRootCauseWindowLinks.mockReset().mockResolvedValue({ links: [] })
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
    // The outcome counts the hypotheses and says how confident the leading one is.
    expect(text).toContain('2 supported hypotheses · #1 has medium confidence')
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
    expect(text).toContain(`Symptom: Checkout · p95 latency rose +300 ms from ${formatRcaTime('2026-10-05T12:03:20Z')} ± 10 s`)
    expect(text).toContain('Cause: builtin:service.cpu.time · Dynatrace rose +60 pts')
    // Without archived phase events, the load steps are inferred from the
    // detection levels: the first is before the window, the second inside.
    expect(text).toContain(`Load step to 20 VUsers at ${formatRcaTime('2026-10-05T12:02:00Z')}`)
    expect(text).not.toContain('Load step to 10 VUsers')
    expect(text).toContain('the load steps are inferred from its steady load levels')
    expect(mocks.api.downloadTestArchiveArtifact).not.toHaveBeenCalled()
    expect(text).toContain('Components: cart-service')
    expect(text).toContain(`Suspect run from ${formatRcaTime('2026-10-05T12:04:30Z')} to ${formatRcaTime('2026-10-05T12:04:40Z')}`)
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
    expect(text).toContain(`checkout stepped to 20 VUsers at ${formatRcaTime('2026-10-05T12:02:05Z')}`)
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

  it('offers no analysis where the project computes none', async () => {
    // Saved analyses stay readable; the controls say why none can be requested.
    await render(model(rcaTestResponse({ analysis_enabled: false })))
    expect(container.querySelector('[role="note"]')?.textContent).toContain('New analyses are not enabled for this project')
    expect(container.textContent).not.toContain('Re-analyze')
    await render(model(rcaTestResponse({ analysis_enabled: true })))
    expect(container.textContent).toContain('Re-analyze')
  })

  it('stays hidden where the project does not present v2', async () => {
    // An analyst is told the capability is off; others see nothing.
    await render(model(rcaTestResponse({ presented: false })))
    expect(container.querySelector('[role="note"]')?.textContent).toContain('not enabled for this project')
    await render(model(rcaTestResponse({ presented: false })), {}, false)
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

  it('says when the archive changed after the analysis read it', async () => {
    await render(model(rcaTestResponse()))
    expect(container.textContent).not.toContain('earlier revision')
    await render(model({ ...rcaTestResponse(), archive_changed: true }))
    expect(container.textContent).toContain("This analysis judged an earlier revision of the run's archive; evidence archived since is not in it. Re-analyze to include it.")
  })

  it('shows the run\'s largest changes on its timeline when no hypothesis is open', async () => {
    await render(model(rcaTestResponse()))
    const overview = container.querySelector('[aria-label="Largest changes"]')!
    expect(overview.getAttribute('data-report-focus')).toBe('rca-changes')
    expect(overview.textContent).toContain('Change: Checkout · p95 latency rose +300 ms')
    expect(overview.textContent).toContain(`Suspect run from ${formatRcaTime('2026-10-05T12:04:30Z')}`)
    await act(async () => Array.from(overview.querySelectorAll('button')).find(b => b.textContent === `Focus Checkout · p95 latency at ${formatRcaClock('2026-10-05T12:03:20Z')}`)!.click())
    expect(onRouteChange).toHaveBeenLastCalledWith({ analysis: 'rca-1', rev: '2', hypothesis: undefined, window: '2026-10-05T12:03:10.000Z~2026-10-05T12:06:00.000Z' })
    // A run whose hypotheses none support still shows what moved.
    await render(model(rcaTestResponse({ assessment: { ...rcaTestResponse().assessment!, outcome: 'no_supported_hypothesis', hypotheses: [] } })))
    expect(container.textContent).toContain('2 series changed, but no signature explains the symptoms')
    expect(container.querySelector('[aria-label="Largest changes"]')).not.toBeNull()
    // With a hypothesis open its own drill-down replaces the overview.
    await render(model(rcaTestResponse()), { analysis: 'rca-1', hypothesis: 'hypothesis-cpu' })
    expect(container.querySelector('[aria-label="Largest changes"]')).toBeNull()
  })

  it('states the analysis origin and the series it judged', async () => {
    await render(model(rcaTestResponse({ origin: 'automatic' })))
    const facts = container.querySelector('.rca-facts')!.textContent || ''
    expect(facts).toContain('AnalysisAutomatic · revision 2')
    expect(facts).toContain('Series judged12 of 12 · Complete')
    expect(container.textContent).toContain('automatic analysis, revision 2')
  })

  it('says an automatic analysis is on its way before any has completed', async () => {
    const latest = { analysis_id: 'rca-5', revision: 5, state: 'queued' as const, origin: 'automatic' as const, created_at: '2026-10-05T12:20:00Z' }
    await act(async () => {
      root.render(<RootCausePanel testId="run-1" model={model({ state: 'none', presented: true })} report={report} history={[]} canAnalyze route={{}} onRouteChange={onRouteChange}
        liveSummary={rcaTestSummary({ state: 'queued', analysis_id: undefined, outcome: undefined, top_hypothesis: undefined, latest })} />)
    })
    expect(container.textContent).toContain('Automatic analysis revision 5 is queued; it replaces this view when it completes.')
    expect(container.textContent).not.toContain('No root-cause analysis has completed')
    expect(button('Analyze')!.disabled).toBe(true)
    await act(async () => {
      root.render(<RootCausePanel testId="run-1" model={model({ state: 'none', presented: true })} report={report} history={[]} canAnalyze route={{}} onRouteChange={onRouteChange}
        liveSummary={rcaTestSummary({ state: 'failed', analysis_id: undefined, outcome: undefined, top_hypothesis: undefined, latest: { ...latest, state: 'failed' } })} />)
    })
    expect(container.querySelector('[role="alert"]')!.textContent).toBe("The run's automatic analysis failed. Analyze to try again.")
  })

  it('points an analyst at the project\'s analysis settings when root cause is off', async () => {
    await act(async () => {
      root.render(<RootCausePanel testId="run-1" model={model(rcaTestResponse({ presented: false }))} report={report} history={[]} canAnalyze route={{}} onRouteChange={onRouteChange} settingsHref="#/settings?setting=analysis" />)
    })
    const link = container.querySelector('[role="note"] a')!
    expect(link.textContent).toBe('Settings › Workspaces › Project analysis')
    expect(link.getAttribute('href')).toBe('#/settings?setting=analysis')
    await render(model(rcaTestResponse({ presented: false })))
    expect(container.textContent).toContain('A project administrator turns it on in Settings › Workspaces › Project analysis.')
  })

  it('names the monitor sources behind a coverage gap with links to their evidence', async () => {
    const assessment = rcaTestResponse().assessment!
    const coverage = [{ evidence: 'monitor_sources', status: 'partial' as const, detail: '2 sources, 1 incomplete', sources: [{ id: 'elastic-logs', pack: 'elastic-query', state: 'failed', reason: 'query_failed', required: true }, { id: 'prom', pack: 'prometheus', state: 'completed' }] }]
    await act(async () => {
      root.render(<RootCausePanel testId="run-1" model={model(rcaTestResponse({ assessment: { ...assessment, coverage } }))} report={report} history={[]} canAnalyze route={{}} onRouteChange={onRouteChange}
        monitorEvidenceHref="#/report/run-1?section=evidence&focus=monitor-evidence" monitorSourcesHref="#/monitoring?category=telemetry" />)
    })
    const gaps = container.querySelector('[aria-label="Monitor sources with gaps"]')!
    expect(gaps.textContent).toContain('elastic-query source elastic-logs (required): Query failed')
    expect(gaps.textContent).not.toContain('prom')
    expect(Array.from(gaps.querySelectorAll('a')).map(a => a.getAttribute('href'))).toEqual(['#/report/run-1?section=evidence&focus=monitor-evidence', '#/monitoring?category=telemetry'])
  })

  it('hands a hypothesis to the report\'s other tools for editors', async () => {
    const actions: RcaHypothesisActions = {
      recordFinding: vi.fn().mockResolvedValue('Added to the Executive report\'s findings.'),
      annotate: vi.fn().mockRejectedValue(new Error('This hypothesis has no onset to place on the timeline.')),
      addNote: vi.fn(),
    }
    await act(async () => {
      root.render(<RootCausePanel testId="run-1" model={model(rcaTestResponse())} report={report} history={[]} canAnalyze route={{}} onRouteChange={onRouteChange} hypothesisActions={actions} onCopyLink={vi.fn()} />)
    })
    const first = container.querySelectorAll('.rca-hypothesis')[0]
    const action = (name: string) => Array.from(first.querySelectorAll('button')).find(b => b.textContent === name)!
    await act(async () => action('Record as finding').click())
    expect(actions.recordFinding).toHaveBeenCalledWith(expect.objectContaining({ id: 'hypothesis-cpu' }), expect.objectContaining({ analysis_id: 'rca-1' }))
    expect(first.querySelector('[role="status"]')!.textContent).toBe("Added to the Executive report's findings.")
    await act(async () => action('Annotate timeline').click())
    expect(first.querySelector('[role="alert"]')!.textContent).toBe('This hypothesis has no onset to place on the timeline.')
    await act(async () => action('Add test note').click())
    expect(actions.addNote).toHaveBeenCalled()
    expect(button('Copy analysis link')).toBeDefined()
    // Viewers get none of them.
    await render(model(rcaTestResponse()))
    expect(container.querySelector('.rca-hypothesis-actions')).toBeNull()
  })

  it('opens the hypothesis span in the run\'s trace providers', async () => {
    mocks.api.getRootCauseWindowLinks.mockResolvedValue({ links: [{ provider: 'elasticsearch', connection_id: 'elk-1', url: 'https://kibana.example/app/discover#/?_g=(time:(from:a,to:b))' }] })
    await render(model(rcaTestResponse()), { analysis: 'rca-1', hypothesis: 'hypothesis-cpu' })
    expect(mocks.api.getRootCauseWindowLinks).toHaveBeenCalledWith('run-1', '2026-10-05T12:01:25.000Z', '2026-10-05T12:06:55.000Z', expect.objectContaining({ signal: expect.anything() }))
    const links = container.querySelector('.rca-window-links')!
    expect(links.textContent).toContain('Open the hypothesis span in Elasticsearch')
    expect(links.querySelector('a')!.getAttribute('target')).toBe('_blank')
  })

  it('shows the browser baseline selection receipt and rejected candidates even when no baseline run was selected', async () => {
    const assessment = rcaTestResponse().assessment!
    await render(model(rcaTestResponse({
      assessment: {
        ...assessment,
        inputs: { ...assessment.inputs, browser_selection: 'sha256:receipt-9999' },
        browser_baseline: undefined,
        browser_selection: {
          version: 'browser_selection.v1',
          status: 'not_selected',
          inventory_count: 2,
          policy: 'policy-1@3',
          reasons: ['no_compatible_candidate'],
          rejected: [
            { test_id: 'cand-run-1', digest: '0123456789abcdef', reasons: ['policy_revision_mismatch', 'step_topology_mismatch'] },
            { test_id: 'cand-run-2', reasons: ['clock_unqualified'] },
          ],
        },
      },
    })))
    const section = container.querySelector('.rca-browser-baseline')!
    expect(section).not.toBeNull()
    const text = section.textContent || ''
    expect(text).toContain('Browser baseline (selection: not selected)')
    expect(text).toContain('Selection receipt (browser_selection.v1): Not selected · policy policy-1@3 · 2 candidates inspected · receipt …ipt-9999.')
    expect(text).toContain('Selection reasons: No compatible candidate.')
    expect(text).toContain('Rejected cand-run-1: Policy revision mismatch, Step topology mismatch (archive …89abcdef)')
    expect(text).toContain('Rejected cand-run-2: Clock unqualified')
  })
})
