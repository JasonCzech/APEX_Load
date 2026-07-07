// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { TestSummary } from '../types'
import RunReportPage from './RunReportPage'

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
    archiveReport: null,
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
vi.mock('./runReport/RunReportLensSwitcher', () => ({ default: ({ activeLens }: { activeLens: string }) => <div data-testid="lens">{activeLens}</div> }))
vi.mock('./runReport/RunReportVerdictBanner', () => ({ default: () => <section data-testid="verdict" /> }))
vi.mock('./runReport/RunReportKpiScorecard', () => ({ default: () => <section data-testid="kpis" /> }))
vi.mock('./runReport/SynchronizedTimeline', () => ({ default: () => <section data-testid="timeline" /> }))
vi.mock('./runReport/TransactionSlaScoreboard', () => ({ default: () => <section data-testid="scoreboard" /> }))
vi.mock('./runReport/RunReportDiagnostics', () => ({ default: () => <section data-testid="diagnostics" /> }))
vi.mock('./runReport/RunReportCompareBar', () => ({ default: () => <section data-testid="compare" /> }))
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
  let onOpenCommandCenter: ReturnType<typeof vi.fn>
  let onOpenLegacyArchive: ReturnType<typeof vi.fn>
  let onNavigateReport: ReturnType<typeof vi.fn>

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
    mocks.polling.test = null
    mocks.polling.error = ''
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    onOpenCommandCenter = vi.fn()
    onOpenLegacyArchive = vi.fn()
    onNavigateReport = vi.fn()
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
})
