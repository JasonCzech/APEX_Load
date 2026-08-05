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
vi.mock('./runReport/RunReportKpiScorecard', () => ({ default: () => <section data-testid="kpis" /> }))
vi.mock('./runReport/SynchronizedTimeline', () => ({ default: () => <section data-testid="timeline" /> }))
vi.mock('./runReport/TransactionSlaScoreboard', () => ({ default: () => <section data-testid="scoreboard" /> }))
vi.mock('./runReport/RunReportDiagnostics', () => ({ default: () => <section data-testid="diagnostics" /> }))
vi.mock('./runReport/RunReportCompareBar', () => ({
  default: ({ onPinBaseline }: { onPinBaseline: () => Promise<boolean> }) => (
    <section data-testid="compare"><button type="button" onClick={() => void onPinBaseline()}>Pin test baseline</button></section>
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
  let onOpenLegacyArchive: Mock<(id: string) => void>
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
    mocks.polling.test = null
    mocks.polling.error = ''
    mocks.archiveModel.archiveReport = null
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    onOpenCommandCenter = vi.fn<(id?: string) => void>()
    onOpenLegacyArchive = vi.fn<(id: string) => void>()
    onNavigateReport = vi.fn<(next: { lens?: 'executive' | 'engineer' | 'audit'; compare?: string }) => void>()
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    vi.clearAllMocks()
  })

  it('bounces live runs to the command center', async () => {
    mocks.polling.test = buildTest('RUNNING')

    await act(async () => {
      root.render(
        <RunReportPage
          testId="run-1"
          onOpenCommandCenter={onOpenCommandCenter}
          onOpenLegacyArchive={onOpenLegacyArchive}
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
          onOpenLegacyArchive={onOpenLegacyArchive}
          onNavigateReport={onNavigateReport}
        />,
      )
    })

    expect(container.textContent).toContain('No archive available')
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
        onOpenLegacyArchive={onOpenLegacyArchive}
        onNavigateReport={onNavigateReport}
      />)
    })
    await flush()
    await act(async () => buttonByText(container, 'Pin test baseline')?.click())
    await flush()

    expect(mocks.api.pinComparisonBaseline).toHaveBeenCalledWith('baseline-1')
    expect(mocks.api.getTest).toHaveBeenCalledWith('run-1')
    expect(mocks.polling.setTest).toHaveBeenCalledWith(refreshed)
  })

  it('ignores a delayed pin refresh after the comparison changes', async () => {
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
        onOpenLegacyArchive={onOpenLegacyArchive}
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

    expect(mocks.polling.setTest).not.toHaveBeenCalled()
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
        onOpenLegacyArchive={onOpenLegacyArchive}
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
            onOpenLegacyArchive={onOpenLegacyArchive}
            onNavigateReport={onNavigateReport}
          />
        </NavigationGuardProvider>,
      )
    })
    await flush()

    const explanation = container.querySelector<HTMLTextAreaElement>('textarea[aria-label="Verdict explanation"]')
    expect(explanation?.value).toBe('Automated verdict')
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
    expect(onNavigateReport).toHaveBeenCalledWith({ lens: 'engineer', compare: undefined })
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
          onOpenLegacyArchive={onOpenLegacyArchive}
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
            onOpenLegacyArchive={onOpenLegacyArchive}
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

    expect(onNavigateReport).toHaveBeenCalledWith({ lens: 'engineer', compare: undefined })
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
          onOpenLegacyArchive={onOpenLegacyArchive}
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
