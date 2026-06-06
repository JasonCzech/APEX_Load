// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { TestSummary } from '../types'
import TestExecution from './TestExecution'

const hookMocks = vi.hoisted(() => ({
  useExecutionTestPolling: vi.fn(),
  useExecutionViewState: vi.fn(),
  useExecutionScenarioPreviewModel: vi.fn(),
  useExecutionMetricsStream: vi.fn(),
  useExecutionArchiveResourceHistory: vi.fn(),
  useExecutionArchiveSummaryHistory: vi.fn(),
  useExecutionChartModel: vi.fn(),
  useExecutionActionBridge: vi.fn(),
  useExecutionLiveControls: vi.fn(),
  useExecutionArchiveModel: vi.fn(),
}))

vi.mock('./TestExecutionHeader', () => ({ default: () => <div>Execution Header</div> }))
vi.mock('./TestExecutionScenarioOverview', () => ({ default: () => <div>Scenario Overview Content</div> }))
vi.mock('./TestExecutionHeroMetrics', () => ({ default: () => <div>Hero Metrics Content</div> }))
vi.mock('./TestExecutionLiveControls', () => ({ default: () => <div>Live Controls Content</div> }))
vi.mock('./TestExecutionScenarioPreview', () => ({ default: () => <div>Scenario Preview Content</div> }))
vi.mock('./TestExecutionCharts', () => ({ default: () => <div>Runtime Graphs Content</div> }))
vi.mock('./TestExecutionStatusPanels', () => ({ default: () => <div>Status Panels Content</div> }))
vi.mock('./TestExecutionSummary', () => ({ default: () => <div>Live Summary Content</div> }))
vi.mock('./TestExecutionArchive', () => ({
  default: ({ hasArchive, view = 'report' }: { hasArchive: boolean; view?: string }) => {
    const viewContent: Record<string, string> = {
      report: 'Archive Report Content',
      'report-overview': 'Archive Overview Content',
      'report-completeness': 'Completeness Score Content',
      'report-metadata': 'Run Metadata Content',
      'report-error-patterns': 'Error Patterns Content',
      'report-browser-summary': 'Browser Metrics Content',
      'report-browser-resources': 'Browser Resource Waterfall Content',
      'report-assets': 'Report Assets Content',
      'report-profiles': 'Report Profiles Content',
      'report-summary-timeline': 'Summary Timeline Content',
      'report-analysis': 'Archive Analysis Content',
      'action-summary': 'Action Summary Content',
      'load-impact': 'Load Impact Content',
      transactions: 'Transaction Drill-Down Content',
    }
    return <div>{hasArchive ? viewContent[view] : `Archive Empty Content ${view}`}</div>
  },
}))

vi.mock('./useExecutionTestPolling', () => ({ useExecutionTestPolling: hookMocks.useExecutionTestPolling }))
vi.mock('./useExecutionViewState', () => ({ useExecutionViewState: hookMocks.useExecutionViewState }))
vi.mock('./useExecutionScenarioPreviewModel', () => ({ useExecutionScenarioPreviewModel: hookMocks.useExecutionScenarioPreviewModel }))
vi.mock('./useExecutionMetricsStream', () => ({ useExecutionMetricsStream: hookMocks.useExecutionMetricsStream }))
vi.mock('./useExecutionArchiveResourceHistory', () => ({ useExecutionArchiveResourceHistory: hookMocks.useExecutionArchiveResourceHistory }))
vi.mock('./useExecutionArchiveSummaryHistory', () => ({ useExecutionArchiveSummaryHistory: hookMocks.useExecutionArchiveSummaryHistory }))
vi.mock('./useExecutionChartModel', () => ({ useExecutionChartModel: hookMocks.useExecutionChartModel }))
vi.mock('./useExecutionActionBridge', () => ({ useExecutionActionBridge: hookMocks.useExecutionActionBridge }))
vi.mock('./useExecutionLiveControls', () => ({ useExecutionLiveControls: hookMocks.useExecutionLiveControls }))
vi.mock('./useExecutionArchiveModel', () => ({ useExecutionArchiveModel: hookMocks.useExecutionArchiveModel }))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('TestExecution workspace layout', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    window.localStorage.clear()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    configureHooks(makeTest('QUEUED'))
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    window.localStorage.clear()
    vi.clearAllMocks()
  })

  it('renders default execution tabs with grouped cards for a queued run', async () => {
    await renderExecution()

    expect(tabLabels()).toEqual([
      'Overview',
      'Control',
      'Graphs',
      'Diagnostics',
      'Archive',
      'Action Summary',
      'Load Impact',
      'Transaction Drill-Down',
    ])
    expect(selectedTabLabel()).toBe('Overview')
    expect(container.textContent).toContain('Scenario Overview Content')
    expect(container.textContent).toContain('Hero Metrics Content')
    expect(container.textContent).toContain('Card Library')
    expect(container.textContent).toContain('Overview')
    expect(container.textContent).toContain('Control')
    expect(container.textContent).toContain('Graphs')
    expect(container.textContent).toContain('Diagnostics')
    expect(container.textContent).toContain('Archive Report')
    expect(libraryItem('archive-overview').textContent).toContain('Archive Overview')
    expect(libraryItem('archive-completeness').textContent).toContain('Completeness Score')
    expect(libraryItem('archive-metadata').textContent).toContain('Run Metadata')
    expect(libraryItem('archive-error-patterns').textContent).toContain('Error Patterns')
    expect(libraryItem('archive-browser-summary').textContent).toContain('Browser Metrics')
    expect(libraryItem('archive-browser-resources').textContent).toContain('Browser Resource Waterfall')
    expect(libraryItem('archive-assets').textContent).toContain('Report Assets')
    expect(libraryItem('archive-profiles').textContent).toContain('Report Profiles')
    expect(libraryItem('archive-summary-timeline').textContent).toContain('Summary Timeline')
    expect(libraryItem('archive-analysis').textContent).toContain('Archive Analysis')
    expect(libraryItem('load-impact').textContent).toContain('Load Impact')
    expect(libraryItem('transaction-drilldown').textContent).toContain('Transaction Drill-Down')
    expect(container.querySelector('select[aria-label^="Add card to"]')).toBeNull()

    await clickWorkspaceTab('Control')
    expect(container.textContent).toContain('Live controls are unavailable for this run state.')
    expect(container.textContent).toContain('Scenario Preview Content')

    await clickWorkspaceTab('Graphs')
    expect(container.textContent).toContain('Runtime Graphs Content')

    await clickWorkspaceTab('Diagnostics')
    expect(container.textContent).toContain('Status Panels Content')
    expect(container.textContent).toContain('Live Summary Content')

    await clickWorkspaceTab('Archive')
    expect(container.textContent).toContain('Archive Empty Content report')

    await clickWorkspaceTab('Action Summary')
    expect(container.textContent).toContain('Archive Empty Content action-summary')

    await clickWorkspaceTab('Load Impact')
    expect(container.textContent).toContain('Archive Empty Content load-impact')

    await clickWorkspaceTab('Transaction Drill-Down')
    expect(container.textContent).toContain('Archive Empty Content transactions')
  })

  it('routes completed archive tabs to their dedicated archive views', async () => {
    configureHooks(makeTest('COMPLETE', {
      archive: {
        test_id: 'test-1',
        schema_version: '1',
        archive_path: '/archives/test-1',
        manifest_path: '/archives/test-1/manifest.json',
        transaction_count: 100,
        resource_sample_count: 20,
        interval_summary_count: 10,
        created_at: '2026-06-05T12:00:00Z',
        completed_at: '2026-06-05T12:05:00Z',
      },
    }))
    await renderExecution()

    await clickWorkspaceTab('Archive')
    expect(container.textContent).toContain('Card Library')
    expect(libraryItem('action-summary').textContent).toContain('Action Summary')
    expect(libraryItem('archive-overview').textContent).toContain('Archive Overview')
    expect(container.textContent).toContain('Archive Report Content')

    await clickLibraryAdd('archive-overview')
    expect(container.textContent).toContain('Archive Overview Content')

    await clickWorkspaceTab('Action Summary')
    expect(container.textContent).toContain('Action Summary Content')

    await clickWorkspaceTab('Load Impact')
    expect(container.textContent).toContain('Load Impact Content')

    await clickWorkspaceTab('Transaction Drill-Down')
    expect(container.textContent).toContain('Transaction Drill-Down Content')
  })

  it('defaults cards to full width except compact and natural overview cards', async () => {
    configureHooks(makeTest('COMPLETE', {
      archive: {
        test_id: 'test-1',
        schema_version: '1',
        archive_path: '/archives/test-1',
        manifest_path: '/archives/test-1/manifest.json',
        transaction_count: 100,
        resource_sample_count: 20,
        interval_summary_count: 10,
        created_at: '2026-06-05T12:00:00Z',
        completed_at: '2026-06-05T12:05:00Z',
      },
    }))
    await renderExecution()

    expect(cardElement('scenario-overview').dataset.cardWidth).toBe('natural')
    expect(cardElement('hero-metrics').dataset.cardWidth).toBe('half')

    await clickWorkspaceTab('Graphs')
    expect(cardElement('runtime-graphs').dataset.cardWidth).toBe('full')

    await clickWorkspaceTab('Action Summary')
    expect(cardElement('action-summary').dataset.cardWidth).toBe('full')

    await clickWorkspaceTab('Load Impact')
    expect(cardElement('load-impact').dataset.cardWidth).toBe('full')
  })

  it('migrates legacy combined load impact layouts into load impact and runtime graphs', async () => {
    window.localStorage.setItem('apexload.execution.workspaceProfiles.v1', JSON.stringify([
      {
        id: 'default',
        name: 'Default Execution Layout',
        activeTabId: 'load-impact',
        tabs: [
          { id: 'overview', label: 'Overview', cardIds: ['scenario-overview', 'hero-metrics'] },
          { id: 'control', label: 'Control', cardIds: ['live-controls', 'scenario-preview'] },
          { id: 'graphs', label: 'Graphs', cardIds: ['runtime-graphs'] },
          { id: 'diagnostics', label: 'Diagnostics', cardIds: ['status-panels', 'live-summary'] },
          { id: 'archive', label: 'Archive', cardIds: ['archive-report'] },
          { id: 'action-summary', label: 'Action Summary', cardIds: ['action-summary'] },
          { id: 'load-impact', label: 'Load Impact + Metrics Under Load', cardIds: ['load-impact-metrics'] },
          { id: 'transaction-drilldown', label: 'Transaction Drill-Down', cardIds: ['transaction-drilldown'] },
        ],
        unassignedCardIds: [],
      },
    ]))

    await renderExecution()

    expect(tabLabels()).toContain('Load Impact')
    expect(tabLabels()).toContain('Graphs')
    expect(tabLabels()).not.toContain('Load Impact + Metrics Under Load')
    expect(cardElement('load-impact').dataset.cardWidth).toBe('full')

    await clickWorkspaceTab('Graphs')
    expect(container.textContent).toContain('Runtime Graphs Content')
  })

  it('removes legacy metrics-under-load tabs and routes the active view to graphs', async () => {
    window.localStorage.setItem('apexload.execution.workspaceProfiles.v1', JSON.stringify([
      {
        id: 'default',
        name: 'Default Execution Layout',
        activeTabId: 'metrics-under-load',
        tabs: [
          { id: 'overview', label: 'Overview', cardIds: ['scenario-overview', 'hero-metrics'] },
          { id: 'graphs', label: 'Graphs', cardIds: ['runtime-graphs'] },
          { id: 'metrics-under-load', label: 'Metrics Under Load', cardIds: ['metrics-under-load'] },
          { id: 'load-impact', label: 'Load Impact', cardIds: ['load-impact'] },
        ],
        unassignedCardIds: [],
      },
    ]))

    await renderExecution()

    expect(tabLabels()).toContain('Graphs')
    expect(tabLabels()).not.toContain('Metrics Under Load')
    expect(selectedTabLabel()).toBe('Graphs')
    expect(container.textContent).toContain('Runtime Graphs Content')
  })

  it('places active run controls inside the Control tab for a running run', async () => {
    configureHooks(makeTest('RUNNING'))
    await renderExecution()

    await clickWorkspaceTab('Control')

    expect(container.textContent).toContain('Live Controls Content')
    expect(container.textContent).toContain('Scenario Preview Content')
    expect(container.textContent).toContain('Card Library')
    expect(libraryItem('archive-report').textContent).toContain('Archive Report')
  })

  it('auto-applies a matching execution profile from run context', async () => {
    window.localStorage.setItem('apexload.execution.workspaceProfiles.v1', JSON.stringify([
      {
        id: 'default',
        name: 'Default Execution Layout',
        activeTabId: 'overview',
        tabs: [
          { id: 'overview', label: 'Overview', cardIds: ['scenario-overview', 'hero-metrics'] },
          { id: 'control', label: 'Control', cardIds: ['live-controls', 'scenario-preview'] },
          { id: 'graphs', label: 'Graphs', cardIds: ['runtime-graphs'] },
          { id: 'diagnostics', label: 'Diagnostics', cardIds: ['status-panels', 'live-summary'] },
          { id: 'archive', label: 'Archive', cardIds: ['archive-report'] },
          { id: 'action-summary', label: 'Action Summary', cardIds: ['action-summary'] },
          { id: 'load-impact', label: 'Load Impact', cardIds: ['load-impact'] },
          { id: 'transaction-drilldown', label: 'Transaction Drill-Down', cardIds: ['transaction-drilldown'] },
        ],
        unassignedCardIds: [],
      },
      {
        id: 'browser-diagnostics',
        name: 'Browser Diagnostics',
        activeTabId: 'diagnostics',
        matchEnabled: true,
        matchText: 'checkout browser',
        priority: 20,
        tabs: [
          { id: 'overview', label: 'Overview', cardIds: ['scenario-overview', 'hero-metrics'] },
          { id: 'control', label: 'Control', cardIds: ['live-controls', 'scenario-preview'] },
          { id: 'graphs', label: 'Graphs', cardIds: ['runtime-graphs'] },
          { id: 'diagnostics', label: 'Diagnostics', cardIds: ['status-panels', 'live-summary'] },
          { id: 'archive', label: 'Archive', cardIds: ['archive-report'] },
          { id: 'action-summary', label: 'Action Summary', cardIds: ['action-summary'] },
          { id: 'load-impact', label: 'Load Impact', cardIds: ['load-impact'] },
          { id: 'transaction-drilldown', label: 'Transaction Drill-Down', cardIds: ['transaction-drilldown'] },
        ],
        unassignedCardIds: [],
      },
    ]))
    window.localStorage.setItem('apexload.execution.activeWorkspaceProfile.v1', JSON.stringify({ autoApply: true, profileId: 'default' }))
    configureHooks(makeTest('RUNNING', { name: 'Checkout Browser Smoke' }))

    await renderExecution()

    expect(selectedTabLabel()).toBe('Diagnostics')
    expect(container.textContent).toContain('Status Panels Content')
    expect(container.textContent).toContain('Live Summary Content')
  })

  async function renderExecution() {
    await act(async () => {
      root.render(
        <TestExecution
          testId="test-1"
          history={[]}
          onBack={vi.fn()}
          onEditScript={vi.fn()}
          onSelectTest={vi.fn()}
        />,
      )
    })
  }

  function configureHooks(test: TestSummary) {
    hookMocks.useExecutionTestPolling.mockReturnValue({ test, setTest: vi.fn(), error: '' })
    hookMocks.useExecutionViewState.mockReturnValue({
      expandedChart: null,
      onExpandedChartChange: vi.fn(),
      expandedScenarioScript: null,
      onExpandedScenarioScriptChange: vi.fn(),
    })
    hookMocks.useExecutionScenarioPreviewModel.mockReturnValue({
      queuedScenarioPreview: {},
      queuedScenarioRows: [],
      scriptDetailsLoading: false,
      scriptDetailsError: '',
    })
    hookMocks.useExecutionMetricsStream.mockReturnValue({ localMetricsHistory: [], latestMetrics: null })
    hookMocks.useExecutionArchiveResourceHistory.mockReturnValue({
      archiveResourceHistory: [],
      archiveResourceError: '',
      archiveResourcesLoading: false,
    })
    hookMocks.useExecutionArchiveSummaryHistory.mockReturnValue({
      archiveSummaryHistory: [],
      archiveSummaryError: '',
      archiveSummariesLoading: false,
    })
    hookMocks.useExecutionChartModel.mockReturnValue({
      filteredData: [],
      summary: {},
      timeRange: '3m',
      refAreaLeft: '',
      refAreaEnd: '',
      showRefArea: false,
      onTimeRangeChange: vi.fn(),
      onChartMouseDown: vi.fn(),
      onChartMouseMove: vi.fn(),
      onZoom: vi.fn(),
      onResetZoom: vi.fn(),
    })
    hookMocks.useExecutionActionBridge.mockReturnValue({
      controlFeedback: '',
      showControlFeedback: false,
      startCapacityConflict: null,
      startShakeoutConflict: null,
      startFeedback: '',
      onDismissStartConflict: vi.fn(),
      onStart: vi.fn(),
      onConfirmStartAnyway: vi.fn(),
      onContinueAfterShakeoutFailure: vi.fn(),
      onStop: vi.fn(),
      onAbort: vi.fn(),
      onPause: vi.fn(),
      onResume: vi.fn(),
    })
    hookMocks.useExecutionLiveControls.mockReturnValue({
      showControls: true,
      onToggleControls: vi.fn(),
      vuserTargets: {},
      onVuserTargetChange: vi.fn(),
      vuserGroupActions: {},
      onVuserGroupActionChange: vi.fn(),
      vuserRampEnabled: false,
      onVuserRampEnabledChange: vi.fn(),
      vuserRampDuration: '1m',
      onVuserRampDurationChange: vi.fn(),
      durationInput: '5m',
      onDurationInputChange: vi.fn(),
      onScaleVUsers: vi.fn(),
      onStopAllVUsers: vi.fn(),
      onAdjustDuration: vi.fn(),
      liveControlPreview: {},
    })
    hookMocks.useExecutionArchiveModel.mockReturnValue({
      hasArchive: Boolean(test.archive),
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
      archiveTransactionSort: { key: 'started_at', direction: 'asc' },
      onArchiveTransactionSortChange: vi.fn(),
      archiveTransactionFilters: {},
      archiveTransactionFiltersActive: false,
      onArchiveTransactionFilterChange: vi.fn(),
      onArchiveTransactionFiltersReset: vi.fn(),
      archiveTransactionsLoading: false,
      archiveTransactionsTotal: 0,
      archivePageStart: 0,
      archivePageEnd: 0,
      archiveTransactionsOffset: 0,
      archivePageSize: 25,
      onPageSizeChange: vi.fn(),
      onPreviousPage: vi.fn(),
      onNextPage: vi.fn(),
      archiveTransactions: [],
      expandedArchiveEvent: null,
      onExpandedArchiveEventChange: vi.fn(),
      onDownloadArchive: vi.fn(),
      onDownloadBrowserDiagnostics: vi.fn(),
      onDownloadArchiveArtifact: vi.fn(),
    })
  }

  function makeTest(status: TestSummary['status'], overrides: Partial<TestSummary> = {}): TestSummary {
    return {
      id: 'test-1',
      name: 'Checkout Browser',
      status,
      application_id: 'app-1',
      release_id: 'rel-1',
      scenario_id: 'scenario-1',
      groups: [
        { name: 'buyers', script_id: 'script-1', script_name: 'Checkout Flow', protocol: 'browser', vusers: 10 },
      ],
      total_vusers: 10,
      duration: '5m',
      created_at: '2026-06-05T12:00:00Z',
      ...overrides,
    }
  }

  function tabLabels() {
    return Array.from(container.querySelectorAll('.dashboard-workspace-tab > span'))
      .map(node => node.textContent || '')
  }

  function selectedTabLabel() {
    const tab = container.querySelector('.dashboard-workspace-tab[aria-selected="true"] > span')
    return tab?.textContent || ''
  }

  function libraryItem(cardId: string) {
    const item = container.querySelector(`[data-library-card-id="${cardId}"]`)
    expect(item).toBeTruthy()
    return item as HTMLElement
  }

  async function clickLibraryAdd(cardId: string) {
    const button = libraryItem(cardId).querySelector('button') as HTMLButtonElement | null
    expect(button).toBeTruthy()
    await act(async () => {
      button?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  function cardElement(cardId: string) {
    const element = container.querySelector(`[data-card-id="${cardId}"]`)
    expect(element).toBeTruthy()
    return element as HTMLElement
  }

  async function clickWorkspaceTab(label: string) {
    const tab = Array.from(container.querySelectorAll('[role="tab"]'))
      .find(button => button.textContent?.includes(label)) as HTMLButtonElement | undefined
    expect(tab).toBeTruthy()
    await act(async () => {
      tab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }
})
