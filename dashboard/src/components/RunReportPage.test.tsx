// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest'

import type { RunArchiveReport, TestSummary } from '../types'
import RunReportPage from './RunReportPage'
import { NavigationGuardProvider } from '../hooks/NavigationGuardRoot'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const mocks = vi.hoisted(() => ({
  api: {
    getTestReportAnnotations: vi.fn(),
    getTest: vi.fn(),
    getTestManifest: vi.fn().mockResolvedValue(null),
    listTests: vi.fn().mockResolvedValue({ tests: [] }),
    getTestArchiveReport: vi.fn(),
    getTestArchiveAnalysis: vi.fn(),
    saveTestReportAnnotations: vi.fn(),
    pinComparisonBaseline: vi.fn(),
    downloadTestReport: vi.fn(),
    downloadTestReportFormat: vi.fn(),
  },
  polling: {
    test: null as TestSummary | null,
    setTest: vi.fn(),
    error: '',
    archiveCheckPending: false,
    archiveCheckError: '',
    checkArchiveAgain: vi.fn().mockResolvedValue(undefined),
  },
  compareBar: {
    pinResult: undefined as boolean | undefined,
    pinError: undefined as string | undefined,
  },
  archiveModel: {
    hasArchive: false,
    archiveError: '',
    archiveLoading: false,
    archiveReport: null as RunArchiveReport | null,
    archiveAnalysis: null,
    archiveAnalysisLoading: false,
    archiveAnalysisError: '',
    archiveActionEntries: [],
    selectedArchiveAction: '',
    onSelectedArchiveActionChange: vi.fn(),
    archiveSOAPOperationEntries: [],
    selectedArchiveSOAPOperation: '',
    onSelectedArchiveSOAPOperationChange: vi.fn(),
    archiveGroups: [],
    selectedArchiveGroup: '',
    onSelectedArchiveGroupChange: vi.fn(),
    archiveErrorsOnly: false,
    onArchiveErrorsOnlyChange: vi.fn(),
    archiveTransactionSort: null,
    onArchiveTransactionSortChange: vi.fn(),
    archiveTransactionFilters: {
      timeFrom: '',
      timeTo: '',
      status: '',
      minDurationMs: '',
      maxDurationMs: '',
      minBytes: '',
      maxBytes: '',
      errorContains: '',
    },
    archiveTransactionFiltersActive: false,
    onArchiveTransactionFilterChange: vi.fn(),
    onArchiveTransactionFiltersReset: vi.fn(),
    archiveTransactionsLoading: false,
    archiveTransactionsTotal: 0,
    archivePageStart: 0,
    archivePageEnd: 0,
    archiveTransactionsOffset: 0,
    archivePageSize: 10,
    onPageSizeChange: vi.fn(),
    onPreviousPage: vi.fn(),
    onNextPage: vi.fn(),
    archiveTransactions: [],
    expandedArchiveEvent: null,
    onExpandedArchiveEventChange: vi.fn(),
    onDownloadArchive: vi.fn(),
    onDownloadBrowserDiagnostics: vi.fn(),
    onDownloadArchiveArtifact: vi.fn(),
  },
}))

vi.mock('../hooks/useApi', () => ({ useApi: () => mocks.api }))
vi.mock('./useExecutionTestPolling', () => ({ useExecutionTestPolling: () => mocks.polling }))
vi.mock('./useExecutionArchiveModel', () => ({ useExecutionArchiveModel: () => mocks.archiveModel }))
vi.mock('./useExecutionArchiveSummaryHistory', () => ({
  useExecutionArchiveSummaryHistory: () => ({ archiveSummaryHistory: [], archiveSummaryError: '', archiveSummariesLoading: false }),
}))
vi.mock('./useExecutionArchiveResourceHistory', () => ({
  useExecutionArchiveResourceHistory: () => ({ archiveResourceHistory: [], archiveResourceError: '', archiveResourcesLoading: false }),
}))
vi.mock('./runReport/RunReportLensSwitcher', () => ({
  default: ({ activeLens, onLensChange }: { activeLens: string; onLensChange: (lens: 'executive' | 'engineer' | 'audit') => void }) => (
    <div data-testid="lens">
      <span>{activeLens}</span>
      <button type="button" onClick={() => onLensChange('engineer')}>Engineer lens</button>
    </div>
  ),
}))
vi.mock('./runReport/RunReportVerdictBanner', () => ({
  default: ({
    status,
    explanation,
    onStatusChange,
    onExplanationChange,
    onSaveOverride,
    saving,
    disabled,
  }: {
    status: string
    explanation: string
    onStatusChange: (value: string) => void
    onExplanationChange: (value: string) => void
    onSaveOverride: (status: string, explanation: string) => Promise<boolean>
    saving: boolean
    disabled?: boolean
  }) => (
    <section data-testid="verdict">
      <input aria-label="Verdict status" value={status} onChange={event => onStatusChange(event.currentTarget.value)} disabled={saving || disabled} />
      <textarea aria-label="Verdict explanation" value={explanation} onChange={event => onExplanationChange(event.currentTarget.value)} disabled={saving || disabled} />
      <button type="button" onClick={() => void onSaveOverride(status, explanation)} disabled={saving || disabled}>Save verdict</button>
    </section>
  ),
}))
vi.mock('./RunOutcomeSummary', () => ({ default: () => <section data-testid="outcome" /> }))
vi.mock('./runReport/RunReportKpiScorecard', () => ({ default: () => <section data-testid="kpis" /> }))
vi.mock('./runReport/SynchronizedTimeline', () => ({ default: () => <section data-testid="timeline" /> }))
vi.mock('./runReport/TransactionSlaScoreboard', () => ({ default: () => <section data-testid="scoreboard" /> }))
vi.mock('./runReport/RunReportDiagnostics', () => ({ default: () => <section data-testid="diagnostics" /> }))
vi.mock('./runReport/RunReportCompareBar', () => ({
  default: ({ onPinBaseline, options = [] }: { onPinBaseline: () => Promise<boolean>; options?: TestSummary[] }) => (
    <section data-testid="compare">{options.map(run => <span key={run.id}>{run.name}</span>)}
      <button
        type="button"
        onClick={async () => {
          try {
            mocks.compareBar.pinResult = await onPinBaseline()
          } catch (error: unknown) {
            mocks.compareBar.pinError = error instanceof Error ? error.message : 'Unable to pin baseline'
          }
        }}
      >
        Pin test baseline
      </button>
    </section>
  ),
}))
vi.mock('./runReport/RunReportExportActions', () => ({ default: () => <section data-testid="exports" /> }))

function buildTest(status: TestSummary['status'], archive = true): TestSummary {
  return {
    id: 'run-1',
    name: 'Checkout Run',
    status,
    duration: '5m',
    total_vusers: 10,
    created_at: '2026-05-12T20:00:00Z',
    groups: [],
    archive: archive ? {
      test_id: 'run-1',
      schema_version: '1',
      archive_path: '/tmp/archive',
      manifest_path: '/tmp/archive/manifest.json',
      transaction_count: 1,
      resource_sample_count: 1,
      interval_summary_count: 1,
      created_at: '2026-05-12T20:00:00Z',
      completed_at: '2026-05-12T20:05:00Z',
    } : undefined,
  }
}

describe('RunReportPage', () => {
  let container: HTMLDivElement
  let root: Root
  let onOpenCommandCenter: Mock<(id?: string) => void>
  let onNavigateReport: Mock<(next: { lens?: 'executive' | 'engineer' | 'audit'; compare?: string }) => void>

  beforeEach(() => {
    mocks.api.getTestReportAnnotations.mockResolvedValue({
      annotations: {
        test_id: 'run-1',
        profile: 'executive',
        title: '',
        prepared_by: '',
        executive_narrative: '',
        section_comments: {},
        verdict_override: '',
        verdict_explanation: '',
        findings: [],
        updated_at: '',
        updated_by: '',
      },
      sections: [],
    })
    mocks.api.saveTestReportAnnotations.mockResolvedValue({
      annotations: {
        test_id: 'run-1',
        profile: 'executive',
        title: '',
        prepared_by: '',
        executive_narrative: '',
        section_comments: {},
        verdict_override: 'Pass',
        verdict_explanation: 'Saved explanation',
        findings: [],
        updated_at: '',
        updated_by: '',
      },
      sections: [],
    })
    mocks.api.getTestManifest.mockResolvedValue({ runtime_changes: [] })
    mocks.polling.test = null
    mocks.polling.error = ''
    mocks.archiveModel.archiveReport = null
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    onOpenCommandCenter = vi.fn<(id?: string) => void>()
    onNavigateReport = vi.fn<(next: { lens?: 'executive' | 'engineer' | 'audit'; compare?: string }) => void>()
    mocks.compareBar.pinResult = undefined
    mocks.compareBar.pinError = undefined
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    vi.clearAllMocks()
  })

  it.each(['FAILED', 'COMPLETE', 'ABORTED'] as const)('keeps an unarchived %s run in the shared Results shell', async status => {
    mocks.polling.test = { ...buildTest(status, false), error: status === 'FAILED' ? 'Controller restarted before completion' : undefined }
    const render = (section: 'summary' | 'performance' | 'execution' | 'evidence') => <RunReportPage testId="run-1" initialSection={section} onOpenCommandCenter={onOpenCommandCenter} onNavigateReport={onNavigateReport} />
    await act(async () => root.render(render('summary')))
    expect(container.querySelector('h1')?.textContent).toBe('Checkout Run')
    expect(container.querySelectorAll('[role="tab"]')).toHaveLength(5)
    expect(container.textContent).toContain('Saved archive unavailable')
    if (status === 'FAILED') expect(container.textContent).toContain('Controller restarted before completion')
    expect(container.textContent).not.toContain('Customize view')
    await act(async () => { Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Check again')!.click() })
    expect(mocks.polling.checkArchiveAgain).toHaveBeenCalled()
    await act(async () => root.render(render('performance')))
    expect(container.textContent).toContain('No controller metric sample was retained')
    expect(container.querySelector('[data-testid="kpis"]')).toBeNull()
    await act(async () => root.render(render('execution')))
    expect(container.textContent).toContain('No interval history was retained')
    expect(container.textContent).toContain('No group configuration was retained')
    await act(async () => root.render(render('evidence')))
    expect(container.textContent).toContain('No lifecycle event history was retained')
    expect(onOpenCommandCenter).not.toHaveBeenCalled()
  })

  it('does not label controller timing as archived evidence while the archive report is unavailable', async () => {
    mocks.polling.test = { ...buildTest('COMPLETE'), started_at: '2026-05-12T20:00:00Z', ended_at: '2026-05-12T20:05:00Z' }
    mocks.archiveModel.archiveReport = null
    await act(async () => root.render(<RunReportPage testId="run-1" onOpenCommandCenter={onOpenCommandCenter} onNavigateReport={onNavigateReport} />))
    expect(container.textContent).toContain('Lifecycle elapsed 5m')
    expect(container.textContent).toContain('Archived evidence elapsed Not recorded')
  })

  it('discovers eligible archived baselines beyond the first catalog page', async () => {
    mocks.polling.test = { ...buildTest('COMPLETE'), project_id: 'project', application_id: 'app', release_id: 'release' }
    const firstPage = Array.from({ length: 100 }, (_, i) => ({ ...buildTest('COMPLETE', false), id: `unarchived-${i}` }))
    mocks.api.listTests.mockResolvedValueOnce({ tests: firstPage, total: 101, count: 100 }).mockResolvedValueOnce({ tests: [{ ...buildTest('COMPLETE'), id: 'older', name: 'Older archived baseline' }], total: 101, count: 1 })
    await act(async () => root.render(<RunReportPage testId="run-1" onOpenCommandCenter={onOpenCommandCenter} onNavigateReport={onNavigateReport} />))
    await act(async () => buttonByText(container, 'Compare')?.click())
    expect(mocks.api.listTests).toHaveBeenCalledWith(expect.objectContaining({ offset: 100, project_id: 'project', application_id: 'app', release_id: 'release' }))
    expect(container.textContent).toContain('Older archived baseline')
    expect(container.querySelector('[data-testid="compare"]')?.querySelectorAll('span')).toHaveLength(1)
  })

  it('distinguishes an event retrieval error from an empty retained history and retries', async () => {
    mocks.polling.test = buildTest('FAILED', false)
    mocks.api.getTestManifest.mockRejectedValueOnce(new Error('Manifest service unavailable'))
    await act(async () => root.render(<RunReportPage testId="run-1" initialSection="execution" onOpenCommandCenter={onOpenCommandCenter} onNavigateReport={onNavigateReport} />))
    expect(container.textContent).toContain('Manifest service unavailable')
    expect(container.textContent).not.toContain('No lifecycle event history was retained')
    await act(async () => { Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Retry records')!.click() })
    expect(container.textContent).not.toContain('Manifest service unavailable')
    expect(container.textContent).toContain('No lifecycle event history was retained')
  })

  it('keeps Execution selected when a missing archive becomes available', async () => {
    mocks.polling.test = buildTest('FAILED', false)
    const render = () => <RunReportPage testId="run-1" initialSection="execution" onOpenCommandCenter={onOpenCommandCenter} onNavigateReport={onNavigateReport} />
    await act(async () => root.render(render()))
    mocks.polling.test = buildTest('FAILED', true)
    await act(async () => root.render(render()))
    expect(container.querySelector('[aria-selected="true"]')?.textContent).toBe('Execution')
    expect(container.textContent).not.toContain('Saved archive unavailable')
    expect(container.querySelector('h1')?.textContent).toBe('Checkout Run')
  })

  it('includes Execution in keyboard navigation without navigating away', async () => {
    mocks.polling.test = buildTest('COMPLETE')
    await act(async () => root.render(<RunReportPage testId="run-1" onOpenCommandCenter={onOpenCommandCenter} onNavigateReport={onNavigateReport} />))
    await act(async () => container.querySelector('#result-tab-Summary')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true })))
    expect(onNavigateReport).toHaveBeenCalledWith(expect.objectContaining({ section: 'execution' }))
    expect(document.activeElement?.id).toBe('result-tab-Execution')
    await act(async () => document.activeElement!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true })))
    expect(onNavigateReport).toHaveBeenLastCalledWith(expect.objectContaining({ section: 'summary' }))
    expect(onOpenCommandCenter).not.toHaveBeenCalled()
  })

  it('restores the requested report section when navigation changes', async () => {
    mocks.polling.test = buildTest('COMPLETE')
    const render = (section: 'diagnostics' | 'evidence') => <NavigationGuardProvider><RunReportPage testId="run-1" initialSection={section} onOpenCommandCenter={onOpenCommandCenter} onNavigateReport={onNavigateReport} /></NavigationGuardProvider>
    await act(async () => { root.render(render('diagnostics')) })
    expect(container.querySelector('[role="tab"][aria-selected="true"]')?.textContent).toBe('Diagnostics')
    await act(async () => { root.render(render('evidence')) })
    expect(container.querySelector('[role="tab"][aria-selected="true"]')?.textContent).toBe('Evidence')
  })

  it('keeps the current section until guarded navigation commits', async () => {
    mocks.polling.test = buildTest('COMPLETE')
    const render = (section: 'summary' | 'evidence') => <NavigationGuardProvider><RunReportPage testId="run-1" initialSection={section} onOpenCommandCenter={onOpenCommandCenter} onNavigateReport={onNavigateReport} /></NavigationGuardProvider>
    await act(async () => { root.render(render('summary')) })
    await act(async () => { (container.querySelector('#result-tab-Evidence') as HTMLButtonElement).click() })
    expect(onNavigateReport).toHaveBeenCalledWith(expect.objectContaining({ section: 'evidence' }))
    // A canceled guard leaves the route prop unchanged.
    expect(container.querySelector('[role="tab"][aria-selected="true"]')?.textContent).toBe('Summary')
    await act(async () => { root.render(render('evidence')) })
    expect(container.querySelector('[role="tab"][aria-selected="true"]')?.textContent).toBe('Evidence')
  })

  it('bounces live runs to the command center', async () => {
    mocks.polling.test = buildTest('RUNNING')

    await act(async () => {
      root.render(
        <RunReportPage
          testId="run-1"
          onOpenCommandCenter={onOpenCommandCenter}

          onNavigateReport={onNavigateReport}
        />,
      )
    })

    expect(onOpenCommandCenter).toHaveBeenCalledWith('run-1')
  })

  it('shows an archive empty state for completed runs without archives', async () => {
    mocks.polling.test = buildTest('COMPLETE', false)

    await act(async () => {
      root.render(
        <RunReportPage
          testId="run-1"
          onOpenCommandCenter={onOpenCommandCenter}

          onNavigateReport={onNavigateReport}
        />,
      )
    })

    expect(container.textContent).toContain('Saved archive unavailable')
    expect(onOpenCommandCenter).not.toHaveBeenCalled()
  })

  it('refreshes the current run after pinning a comparison baseline', async () => {
    const current = { ...buildTest('COMPLETE'), comparison_baseline: { source_test_id: 'baseline-1', source_test_name: 'Baseline' } }
    const baseline = { ...buildTest('COMPLETE'), id: 'baseline-1', name: 'Baseline' }
    const refreshed = { ...current, comparison_baseline: { ...current.comparison_baseline, pinned_at: '2026-08-05T12:00:00Z' } }
    mocks.polling.test = current
    mocks.archiveModel.archiveReport = {} as RunArchiveReport
    mocks.api.getTest.mockImplementation(async (id: string) => id === 'run-1' ? refreshed : baseline)
    mocks.api.getTestArchiveReport.mockResolvedValue({})
    mocks.api.getTestArchiveAnalysis.mockResolvedValue({ nodes: [], edges: [], stats: {} })
    mocks.api.pinComparisonBaseline.mockResolvedValue(baseline)

    await act(async () => {
      root.render(<RunReportPage
        testId="run-1"
        initialCompare="baseline-1"
        onOpenCommandCenter={onOpenCommandCenter}

        onNavigateReport={onNavigateReport}
      />)
    })
    await flush()
    await act(async () => buttonByText(container, 'Pin test baseline')?.click())
    await flush()

    expect(mocks.api.pinComparisonBaseline).toHaveBeenCalledWith('baseline-1')
    expect(mocks.api.getTest).toHaveBeenCalledWith('run-1')
    expect(mocks.polling.setTest).toHaveBeenCalledWith(refreshed)
    await act(async () => buttonByText(container, 'Compare')?.click())
    expect(container.textContent).toContain('Comparing with Baseline. Open Compare to change or clear the baseline.')
  })

  it('returns a successful pin result when the comparison changes before delayed refresh', async () => {
    const current = { ...buildTest('COMPLETE'), comparison_baseline: { source_test_id: 'baseline-1', source_test_name: 'Baseline 1' } }
    const baseline = (id: string) => ({ ...buildTest('COMPLETE'), id, name: id })
    let resolveRefresh!: (value: TestSummary) => void
    const pendingRefresh = new Promise<TestSummary>(resolve => { resolveRefresh = resolve })
    mocks.polling.test = current
    mocks.archiveModel.archiveReport = {} as RunArchiveReport
    mocks.api.getTest.mockImplementation((id: string) => id === 'run-1' ? pendingRefresh : Promise.resolve(baseline(id)))
    mocks.api.getTestArchiveReport.mockResolvedValue({})
    mocks.api.getTestArchiveAnalysis.mockResolvedValue({ nodes: [], edges: [], stats: {} })
    mocks.api.pinComparisonBaseline.mockResolvedValue(baseline('baseline-1'))

    const render = (initialCompare: string) => (
      <RunReportPage
        testId="run-1"
        initialCompare={initialCompare}
        onOpenCommandCenter={onOpenCommandCenter}

        onNavigateReport={onNavigateReport}
      />
    )
    await act(async () => root.render(render('baseline-1')))
    await flush()
    await act(async () => buttonByText(container, 'Pin test baseline')?.click())
    await vi.waitFor(() => expect(mocks.api.getTest).toHaveBeenCalledWith('run-1'))

    await act(async () => root.render(render('baseline-2')))
    await flush()
    await act(async () => {
      resolveRefresh(current)
      await pendingRefresh
    })

    expect(mocks.compareBar.pinError).toBeUndefined()
    expect(mocks.compareBar.pinResult).toBe(true)
    expect(mocks.polling.setTest).toHaveBeenCalled()
  })

  it('returns a successful pin result even when the comparison changes before the refresh', async () => {
    const current = { ...buildTest('COMPLETE'), comparison_baseline: { source_test_id: 'baseline-1', source_test_name: 'Baseline 1' } }
    let releaseRefresh: (() => void) | undefined
    const pendingRefresh = new Promise<void>(resolve => {
      releaseRefresh = resolve
    })
    let getTestCalled = false
    mocks.polling.test = current
    mocks.archiveModel.archiveReport = {} as RunArchiveReport
    mocks.api.getTest.mockImplementation(async (id: string) => {
      if (id === 'run-1') {
        getTestCalled = true
        await pendingRefresh
        return { ...current, comparison_baseline: { ...current.comparison_baseline, pinned_at: '2026-08-05T12:00:00Z' } }
      }
      return { ...buildTest('COMPLETE'), id, name: id }
    })
    mocks.api.pinComparisonBaseline.mockResolvedValue({} as any)
    mocks.api.getTestArchiveReport.mockResolvedValue({})
    mocks.api.getTestArchiveAnalysis.mockResolvedValue({ nodes: [], edges: [], stats: {} })

    const render = (initialCompare: string) => (
      <RunReportPage
        testId="run-1"
        initialCompare={initialCompare}
        onOpenCommandCenter={onOpenCommandCenter}

        onNavigateReport={onNavigateReport}
      />
    )
    await act(async () => root.render(render('baseline-1')))
    await flush()
    await act(async () => buttonByText(container, 'Pin test baseline')?.click())
    await act(async () => root.render(render('baseline-2')))
    await act(async () => releaseRefresh?.())

    expect(getTestCalled).toBe(true)
    expect(mocks.compareBar.pinError).toBeUndefined()
    expect(mocks.compareBar.pinResult).toBe(true)
    expect(mocks.polling.setTest).toHaveBeenCalled()
  })

  it('disables verdict saving when report annotations cannot be loaded', async () => {
    mocks.polling.test = buildTest('COMPLETE')
    mocks.archiveModel.archiveReport = {
      verdict: { status: 'Pass', failed: false, conditional: false, explanation: 'Automated verdict' },
    } as RunArchiveReport
    mocks.api.getTestReportAnnotations.mockRejectedValue(new Error('annotations unavailable'))

    await act(async () => {
      root.render(<RunReportPage
        testId="run-1"
        onOpenCommandCenter={onOpenCommandCenter}

        onNavigateReport={onNavigateReport}
      />)
    })
    await flush()

    expect(buttonByText(container, 'Save verdict')?.disabled).toBe(true)
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('annotations unavailable')
    expect(mocks.api.saveTestReportAnnotations).not.toHaveBeenCalled()
  })

  it('preserves a verdict draft across a slow annotation load and guards lens changes', async () => {
    mocks.polling.test = buildTest('COMPLETE')
    mocks.archiveModel.archiveReport = {
      verdict: { status: 'Pass', failed: false, conditional: false, explanation: 'Automated verdict' },
    } as RunArchiveReport
    let resolveAnnotations: ((value: Awaited<ReturnType<typeof mocks.api.getTestReportAnnotations>>) => void) | undefined
    const pendingAnnotations = new Promise<Awaited<ReturnType<typeof mocks.api.getTestReportAnnotations>>>(resolve => {
      resolveAnnotations = resolve
    })
    mocks.api.getTestReportAnnotations
      .mockReturnValueOnce(pendingAnnotations)
      .mockResolvedValue({
        annotations: {
          test_id: 'run-1',
          profile: 'engineer',
          title: '',
          prepared_by: '',
          executive_narrative: '',
          section_comments: {},
          verdict_override: 'Conditional Pass',
          verdict_explanation: 'Engineer annotation',
          findings: [],
          updated_at: '',
          updated_by: '',
        },
        sections: [],
      })

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <RunReportPage
            testId="run-1"
            onOpenCommandCenter={onOpenCommandCenter}

            onNavigateReport={onNavigateReport}
          />
        </NavigationGuardProvider>,
      )
    })
    await flush()

    const explanation = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="Verdict explanation"]')
    expect(explanation?.value).toBe('')
    await changeTextarea(explanation, 'Analyst draft')
    await act(async () => {
      resolveAnnotations?.({
        annotations: {
          test_id: 'run-1',
          profile: 'executive',
          title: '',
          prepared_by: '',
          executive_narrative: '',
          section_comments: {},
          verdict_override: 'Fail',
          verdict_explanation: 'Server response',
          findings: [],
          updated_at: '',
          updated_by: '',
        },
        sections: [],
      })
    })
    await flush()
    expect(container.querySelector<HTMLTextAreaElement>('textarea[aria-label="Verdict explanation"]')?.value).toBe('Analyst draft')

    await act(async () => buttonByText(container, 'Export report')?.click())
    await act(async () => {
      buttonByText(container, 'Engineer lens')?.click()
    })
    expect(container.textContent).toContain('Unsaved Changes')
    expect(container.querySelector('[data-testid="lens"]')?.textContent).toContain('executive')

    await act(async () => {
      buttonByText(container, 'Discard changes')?.click()
    })
    await flush()
    expect(container.querySelector('[data-testid="lens"]')?.textContent).toContain('engineer')
    expect(onNavigateReport).toHaveBeenCalledWith({ lens: 'engineer', compare: undefined, section: 'summary' })
  })

  it('does not apply an old-lens verdict save after the report lens changes', async () => {
    mocks.polling.test = buildTest('COMPLETE')
    mocks.archiveModel.archiveReport = {
      verdict: { status: 'Pass', failed: false, conditional: false, explanation: 'Automated verdict' },
    } as RunArchiveReport
    mocks.api.getTestReportAnnotations.mockImplementation(async (_testID: string, lens: string) => ({
      annotations: {
        test_id: 'run-1',
        profile: lens,
        title: '',
        prepared_by: '',
        executive_narrative: '',
        section_comments: {},
        verdict_override: lens === 'engineer' ? 'Conditional Pass' : 'Pass',
        verdict_explanation: lens === 'engineer' ? 'Engineer annotation' : 'Executive annotation',
        findings: [],
        updated_at: '',
        updated_by: '',
      },
      sections: [],
    }))
    let resolveSave: ((value: Awaited<ReturnType<typeof mocks.api.saveTestReportAnnotations>>) => void) | undefined
    mocks.api.saveTestReportAnnotations.mockReturnValue(new Promise(resolve => { resolveSave = resolve }))
    const render = (initialLens: 'executive' | 'engineer') => (
      <NavigationGuardProvider>
        <RunReportPage
          testId="run-1"
          initialLens={initialLens}
          onOpenCommandCenter={onOpenCommandCenter}

          onNavigateReport={onNavigateReport}
        />
      </NavigationGuardProvider>
    )

    await act(async () => root.render(render('executive')))
    await flush()
    await changeTextarea(container.querySelector<HTMLTextAreaElement>('textarea[aria-label="Verdict explanation"]'), 'Saving executive draft')
    await act(async () => buttonByText(container, 'Save verdict')?.click())
    await act(async () => root.render(render('engineer')))
    await flush()
    await act(async () => buttonByText(container, 'Export report')?.click())
    expect(container.querySelector('[data-testid="lens"]')?.textContent).toContain('engineer')
    expect(container.querySelector<HTMLTextAreaElement>('textarea[aria-label="Verdict explanation"]')?.value).toBe('Engineer annotation')

    await act(async () => {
      resolveSave?.({
        annotations: {
          test_id: 'run-1',
          profile: 'executive',
          title: '',
          prepared_by: '',
          executive_narrative: '',
          section_comments: {},
          verdict_override: 'Fail',
          verdict_explanation: 'Old executive save',
          findings: [],
          updated_at: '',
          updated_by: '',
        },
        sections: [],
      })
    })
    await flush()

    expect(container.querySelector<HTMLTextAreaElement>('textarea[aria-label="Verdict explanation"]')?.value).toBe('Engineer annotation')
  })

  it('reuses an in-flight verdict save when guarded navigation saves the same draft', async () => {
    mocks.polling.test = buildTest('COMPLETE')
    mocks.archiveModel.archiveReport = {
      verdict: { status: 'Pass', failed: false, conditional: false, explanation: 'Automated verdict' },
    } as RunArchiveReport
    let resolveSave: ((value: Awaited<ReturnType<typeof mocks.api.saveTestReportAnnotations>>) => void) | undefined
    mocks.api.saveTestReportAnnotations.mockReturnValue(new Promise(resolve => { resolveSave = resolve }))

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <RunReportPage
            testId="run-1"
            onOpenCommandCenter={onOpenCommandCenter}

            onNavigateReport={onNavigateReport}
          />
        </NavigationGuardProvider>,
      )
    })
    await flush()
    await changeTextarea(container.querySelector<HTMLTextAreaElement>('textarea[aria-label="Verdict explanation"]'), 'One durable draft')
    await act(async () => buttonByText(container, 'Save verdict')?.click())

    expect(mocks.api.saveTestReportAnnotations).toHaveBeenCalledTimes(1)
    expect(container.querySelector<HTMLTextAreaElement>('textarea[aria-label="Verdict explanation"]')?.disabled).toBe(true)

    await act(async () => buttonByText(container, 'Export report')?.click())
    await act(async () => buttonByText(container, 'Engineer lens')?.click())
    expect(container.textContent).toContain('Unsaved Changes')
    await act(async () => buttonByText(container, 'Save changes')?.click())
    expect(mocks.api.saveTestReportAnnotations).toHaveBeenCalledTimes(1)

    await act(async () => {
      resolveSave?.({
        annotations: {
          test_id: 'run-1',
          profile: 'executive',
          title: '',
          prepared_by: '',
          executive_narrative: '',
          section_comments: {},
          verdict_override: 'Pass',
          verdict_explanation: 'One durable draft',
          findings: [],
          updated_at: '2026-08-03T10:00:00Z',
          updated_by: 'analyst',
        },
        sections: [],
      })
    })
    await flush()

    expect(onNavigateReport).toHaveBeenCalledWith({ lens: 'engineer', compare: undefined, section: 'summary' })
  })

  it('reloads and merges the latest commentary after a verdict revision conflict', async () => {
    mocks.polling.test = buildTest('COMPLETE')
    mocks.archiveModel.archiveReport = {
      verdict: { status: 'Pass', failed: false, conditional: false, explanation: 'Automated verdict' },
    } as RunArchiveReport
    mocks.api.getTestReportAnnotations
      .mockResolvedValueOnce({
        annotations: {
          test_id: 'run-1', profile: 'executive', title: 'Initial', prepared_by: '', executive_narrative: 'old narrative',
          section_comments: {}, verdict_override: '', verdict_explanation: '', findings: [], updated_at: 'rev-1', updated_by: '',
        },
        sections: [],
      })
      .mockResolvedValueOnce({
        annotations: {
          test_id: 'run-1', profile: 'executive', title: 'Composer title', prepared_by: 'Other analyst', executive_narrative: 'newer narrative',
          section_comments: { decision: 'preserve me' }, verdict_override: '', verdict_explanation: '', findings: [], updated_at: 'rev-2', updated_by: 'Other analyst',
        },
        sections: [],
      })
    mocks.api.saveTestReportAnnotations
      .mockRejectedValueOnce(Object.assign(new Error('revision conflict'), { status: 409 }))
      .mockResolvedValueOnce({
        annotations: {
          test_id: 'run-1', profile: 'executive', title: 'Composer title', prepared_by: 'Other analyst', executive_narrative: 'newer narrative',
          section_comments: { decision: 'preserve me' }, verdict_override: 'Fail', verdict_explanation: 'Analyst verdict', findings: [], updated_at: 'rev-3', updated_by: 'analyst',
        },
        sections: [],
      })

    await act(async () => {
      root.render(
        <RunReportPage
          testId="run-1"
          onOpenCommandCenter={onOpenCommandCenter}

          onNavigateReport={onNavigateReport}
        />,
      )
    })
    await flush()
    await changeTextarea(container.querySelector<HTMLTextAreaElement>('textarea[aria-label="Verdict explanation"]'), 'Analyst verdict')
    await act(async () => buttonByText(container, 'Save verdict')?.click())
    await vi.waitFor(() => expect(mocks.api.saveTestReportAnnotations).toHaveBeenCalledTimes(2))

    expect(mocks.api.saveTestReportAnnotations.mock.calls[1][2]).toMatchObject({
      expected_updated_at: 'rev-2',
      title: 'Composer title',
      prepared_by: 'Other analyst',
      executive_narrative: 'newer narrative',
      section_comments: { decision: 'preserve me' },
      verdict_explanation: 'Analyst verdict',
    })
  })
})

async function flush() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
    await Promise.resolve()
  })
}

function buttonByText(root: HTMLElement, text: string) {
  return Array.from(root.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes(text))
}

async function changeTextarea(textarea: HTMLTextAreaElement | null, value: string) {
  expect(textarea).toBeTruthy()
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
    if (textarea) setter?.call(textarea, value)
    textarea?.dispatchEvent(new Event('input', { bubbles: true }))
    textarea?.dispatchEvent(new Event('change', { bubbles: true }))
  })
}
