// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useActiveTopbarContribution } from '../hooks/TopbarContributionProvider'
import { TopbarContributionProvider } from '../hooks/TopbarContributionRoot'
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
  useExecutionTestManifest: vi.fn(),
}))

vi.mock('./TestExecutionHeader', () => ({ default: () => <div>Execution Header</div> }))
vi.mock('./TestExecutionScenarioEditor', () => ({ default: ({ open }: { open: boolean }) => (open ? <div>Scenario Editor Content</div> : null) }))
vi.mock('./TestNotesDialog', () => ({ default: ({ testId }: { testId: string }) => <div>Test Notes Dialog {testId}</div> }))
vi.mock('./TestExecutionScenarioOverview', () => ({ default: () => <div>Scenario Overview Content</div> }))
vi.mock('./TestExecutionHeroMetrics', () => ({ default: () => <div>Hero Metrics Content</div> }))
vi.mock('./TestExecutionLiveControls', () => ({ default: () => <div>Live Controls Content</div> }))
vi.mock('./TestExecutionManifestCard', () => ({ default: () => <div>Test Manifest Content</div> }))
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
vi.mock('./useExecutionTestManifest', () => ({ useExecutionTestManifest: hookMocks.useExecutionTestManifest }))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function TopbarContributionHarness() {
  const contribution = useActiveTopbarContribution()
  return (
    <div data-testid="execution-topbar">
      <strong>{contribution?.title}</strong>
      {contribution?.meta?.filter(chip => !chip.hidden).map(chip => (
        <span key={chip.id}>{chip.label}</span>
      ))}
      {contribution?.secondaryActions?.filter(action => !action.hidden).map(action => (
        <button key={action.id} disabled={action.disabled} onClick={action.onClick}>
          {action.label}
        </button>
      ))}
      {contribution?.primaryActions?.filter(action => !action.hidden).map(action => (
        <button key={action.id} disabled={action.disabled} onClick={action.onClick}>
          {action.label}
        </button>
      ))}
    </div>
  )
}

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
    expect(container.textContent).toContain('Test Manifest Content')
    expect(container.textContent).toContain('Card Library')
    expect(container.textContent).toContain('Run Overview')
    expect(container.textContent).toContain('Run Control')
    expect(container.textContent).toContain('Runtime Monitoring')
    expect(container.textContent).toContain('Archive Summary')
    expect(container.textContent).toContain('Archive Evidence')
    expect(container.textContent).toContain('Archive Exports')
    expect(container.textContent).toContain('Analysis & Drilldown')
    const archiveEvidenceHeader = libraryGroupHeader('Archive Evidence')
    const archiveEvidenceList = libraryGroupList(archiveEvidenceHeader)
    expect(archiveEvidenceHeader.textContent).toContain('0/4')
    expect(archiveEvidenceList.hidden).toBe(false)
    await click(archiveEvidenceHeader)
    expect(archiveEvidenceHeader.getAttribute('aria-expanded')).toBe('false')
    expect(archiveEvidenceList.hidden).toBe(true)
    await click(archiveEvidenceHeader)
    expect(archiveEvidenceHeader.getAttribute('aria-expanded')).toBe('true')
    expect(archiveEvidenceList.hidden).toBe(false)
    expect(libraryItem('archive-overview').textContent).toContain('Archive Overview')
    expect(libraryItem('test-manifest').textContent).toContain('Test Manifest')
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
    expect(container.textContent).toContain('Scenario Preview Content')
    expect(queryLibraryItem('live-controls')).toBeNull()

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

  it('feeds archived summary points into the runtime chart model', async () => {
    const archivedPoint = {
      time: '8:00:00 PM',
      timestampMs: Date.parse('2026-05-12T20:00:00Z'),
      vusers: 10,
      tps: 23,
      avgMs: 145,
      errorPct: 1.5,
      p50: 100,
      p90: 170,
      p95: 190,
      p99: 240,
    }
    hookMocks.useExecutionArchiveSummaryHistory.mockReturnValue({
      archiveSummaryHistory: [archivedPoint],
      archiveSummaryError: '',
      archiveSummariesLoading: false,
    })

    await renderExecution()

    expect(hookMocks.useExecutionChartModel).toHaveBeenLastCalledWith(expect.objectContaining({
      archiveSummaryHistory: [archivedPoint],
    }))
  })

  it('opens the requested archive workspace tab without changing the default layout', async () => {
    await renderExecution({ initialWorkspaceTabId: 'archive' })

    expect(selectedTabLabel()).toBe('Archive')
    expect(container.textContent).toContain('Archive Empty Content report')
  })

  it('publishes queued and active run controls to the contextual topbar', async () => {
    await renderExecution()

    expect(topbarText()).toContain('Test Run')
    expect(topbarText()).toContain('Checkout Browser')
    expect(topbarText()).not.toContain('Live Controls')
    expect(topbarText()).toContain('Revise Queued Run')
    expect(topbarText()).toContain('Test Notes')
    expect(topbarText()).toContain('Start Test')
    expect(topbarText()).toContain('Queued')
    expect(topbarText()).toContain('Load 10 VUsers')
    expect(topbarText()).toContain('5m')
    expect(topbarText()).toContain('1 group')
    expect(topbarText()).not.toContain('Started')
    expect(container.querySelector('.execution-run-summary')).toBeNull()

    const queuedActions = latestActionBridge()
    await clickTopbarButton('Start Test')
    expect(queuedActions.onStart).toHaveBeenCalledTimes(1)
    await clickTopbarButton('Test Notes')
    expect(container.textContent).toContain('Test Notes Dialog test-1')

    configureHooks(makeTest('RUNNING', {
      name: 'Checkout Live Run',
      started_at: '2026-06-08T04:00:00Z',
      live_metrics: {
        timestamp: '2026-06-08T04:01:00Z',
        active_vusers: 6,
        tps: 1,
        avg_response_ms: 100,
        error_pct: 0,
        total_transactions: 10,
        total_errors: 0,
        p50_ms: 90,
        p90_ms: 120,
        p95_ms: 130,
        p99_ms: 150,
      },
    }))
    await renderExecution()

    expect(topbarText()).toContain('Test Run')
    expect(topbarText()).toContain('Checkout Live Run')
    expect(topbarText()).not.toContain('Live Controls')
    expect(topbarText()).toContain('Running')
    expect(topbarText()).toContain('Started')
    expect(topbarText()).toContain('Remaining 3m')
    expect(topbarText()).toContain('Current 6 VUsers')
    expect(topbarText()).toContain('Target 10 VUsers')
    expect(topbarText()).toContain('Pause')
    expect(topbarText()).toContain('Stop Test')
    expect(topbarText()).toContain('Abort')
    expect(container.querySelector('.execution-run-summary')).toBeNull()

    const runningActions = latestActionBridge()
    await clickTopbarButton('Pause')
    expect(runningActions.onPause).toHaveBeenCalledTimes(1)
  })

  it('keeps the topbar registration stable while invoking the latest action callbacks', async () => {
    const queuedTest = makeTest('QUEUED')
    configureHooks(queuedTest)
    await renderExecution()
    const firstActions = latestActionBridge()

    configureHooks(queuedTest)
    await renderExecution()
    const latestActions = latestActionBridge()

    await clickTopbarButton('Start Test')

    expect(firstActions.onStart).not.toHaveBeenCalled()
    expect(latestActions.onStart).toHaveBeenCalledTimes(1)
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
    expect(cardElement('test-manifest').dataset.cardWidth).toBe('full')

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

  it('pins active run controls above the workspace for a running run', async () => {
    configureHooks(makeTest('RUNNING'))
    await renderExecution()

    expect(container.textContent).toContain('Live Controls Content')
    expect(container.textContent).toContain('Scenario Overview Content')
    expect((container.textContent?.match(/Live Controls Content/g) ?? []).length).toBe(1)

    await clickWorkspaceTab('Control')

    expect(container.textContent).toContain('Live Controls Content')
    expect(container.textContent).toContain('Scenario Preview Content')
    expect(container.textContent).toContain('Card Library')
    expect(libraryItem('archive-report').textContent).toContain('Archive Report')
    expect((container.textContent?.match(/Live Controls Content/g) ?? []).length).toBe(1)
  })

  it('uses a pinned-controls note for saved layouts that still include the live controls card', async () => {
    window.localStorage.setItem('apexload.execution.workspaceProfiles.v1', JSON.stringify([
      {
        id: 'default',
        name: 'Default Execution Layout',
        activeTabId: 'control',
        tabs: [
          { id: 'overview', label: 'Overview', cardIds: ['scenario-overview', 'hero-metrics'] },
          { id: 'control', label: 'Control', cardIds: ['live-controls', 'scenario-preview'] },
          { id: 'graphs', label: 'Graphs', cardIds: ['runtime-graphs'] },
        ],
        unassignedCardIds: [],
      },
    ]))
    configureHooks(makeTest('RUNNING'))

    await renderExecution()

    expect(container.textContent).toContain('Live Controls Content')
    expect(container.textContent).toContain('Detailed live controls are pinned above this run dashboard while the run is active.')
    expect(container.textContent).toContain('Scenario Preview Content')
    expect((container.textContent?.match(/Live Controls Content/g) ?? []).length).toBe(1)
  })

  it('migrates existing default layouts to include the test manifest once', async () => {
    window.localStorage.setItem('apexload.execution.workspaceProfiles.v1', JSON.stringify([
      {
        id: 'default',
        name: 'Default Execution Layout',
        activeTabId: 'overview',
        tabs: [
          { id: 'overview', label: 'Overview', cardIds: ['scenario-overview', 'hero-metrics'] },
          { id: 'control', label: 'Control', cardIds: ['scenario-preview'] },
        ],
        unassignedCardIds: [],
      },
    ]))

    await renderExecution()

    expect(container.textContent).toContain('Test Manifest Content')
    let stored = readStoredDefaultProfile()
    expect(stored.tabs.find(tab => tab.id === 'overview')?.cardIds).toEqual(['scenario-overview', 'hero-metrics', 'test-manifest'])
    expect(window.localStorage.getItem('apexload.execution.testManifestCard.v1')).toBe('true')

    await clickWorkspaceTab('Overview')
    await clickRemoveCard('Test Manifest')
    expect(container.textContent).not.toContain('Test Manifest Content')

    await act(async () => {
      root.unmount()
    })
    root = createRoot(container)
    configureHooks(makeTest('QUEUED'))
    await renderExecution()

    stored = readStoredDefaultProfile()
    expect(stored.tabs.find(tab => tab.id === 'overview')?.cardIds).toEqual(['scenario-overview', 'hero-metrics'])
    expect(container.textContent).not.toContain('Test Manifest Content')
  })

  it('removes live controls from saved layouts after a run has finished', async () => {
    window.localStorage.setItem('apexload.execution.workspaceProfiles.v1', JSON.stringify([
      {
        id: 'default',
        name: 'Default Execution Layout',
        activeTabId: 'control',
        tabs: [
          { id: 'overview', label: 'Overview', cardIds: ['scenario-overview', 'hero-metrics'] },
          { id: 'control', label: 'Control', cardIds: ['live-controls', 'scenario-preview'] },
          { id: 'graphs', label: 'Graphs', cardIds: ['runtime-graphs'] },
        ],
        unassignedCardIds: ['live-controls'],
      },
    ]))
    configureHooks(makeTest('COMPLETE'))

    await renderExecution()

    expect(selectedTabLabel()).toBe('Control')
    expect(container.textContent).toContain('Scenario Preview Content')
    expect(container.textContent).not.toContain('Live Controls Content')
    expect(container.textContent).not.toContain('Detailed live controls are pinned above this run dashboard while the run is active.')
    expect(container.textContent).not.toContain('Live controls are unavailable for this run state.')
    expect(queryLibraryItem('live-controls')).toBeNull()
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

  async function renderExecution(props: Partial<Parameters<typeof TestExecution>[0]> = {}) {
    await act(async () => {
      root.render(
        <TopbarContributionProvider>
          <TopbarContributionHarness />
          <TestExecution
            testId="test-1"
            history={[]}
            onBack={vi.fn()}
            onEditScript={vi.fn()}
            onSelectTest={vi.fn()}
            {...props}
          />
        </TopbarContributionProvider>,
      )
    })
  }

  function topbarText() {
    return container.querySelector('[data-testid="execution-topbar"]')?.textContent || ''
  }

  function latestActionBridge() {
    const results = hookMocks.useExecutionActionBridge.mock.results
    return results[results.length - 1]?.value
  }

  async function clickTopbarButton(label: string) {
    const button = Array.from(container.querySelectorAll('[data-testid="execution-topbar"] button'))
      .find(candidate => candidate.textContent?.trim() === label)
    expect(button).toBeDefined()
    if (!button) return
    await act(async () => {
      button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
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
      liveControlPreview: {
        currentTotal: 6,
        projectedTotal: 10,
        remainingSeconds: 180,
      },
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
    hookMocks.useExecutionTestManifest.mockReturnValue({
      manifest: null,
      manifestError: '',
      manifestLoading: false,
      reloadManifest: vi.fn(),
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
    const item = queryLibraryItem(cardId)
    expect(item).toBeTruthy()
    return item as HTMLElement
  }

  function queryLibraryItem(cardId: string) {
    return container.querySelector(`[data-library-card-id="${cardId}"]`) as HTMLElement | null
  }

  function libraryGroupHeader(category: string) {
    const button = Array.from(container.querySelectorAll('.dashboard-workspace-library-group-header'))
      .find(button => button.textContent?.includes(category))
    expect(button).toBeTruthy()
    return button as HTMLButtonElement
  }

  function libraryGroupList(header: HTMLButtonElement) {
    const listId = header.getAttribute('aria-controls')
    expect(listId).toBeTruthy()
    const list = listId ? document.getElementById(listId) : null
    expect(list).toBeTruthy()
    return list as HTMLDivElement
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

  function readStoredDefaultProfile() {
    const stored = JSON.parse(window.localStorage.getItem('apexload.execution.workspaceProfiles.v1') || '[]') as Array<{ id: string; tabs: Array<{ id: string; cardIds: string[] }> }>
    const profile = stored.find(profile => profile.id === 'default')
    expect(profile).toBeTruthy()
    return profile as { tabs: Array<{ id: string; cardIds: string[] }> }
  }

  async function clickRemoveCard(title: string) {
    const button = container.querySelector(`button[aria-label="Remove card ${title}"]`) as HTMLButtonElement | null
    expect(button).toBeTruthy()
    await act(async () => {
      button?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  async function clickWorkspaceTab(label: string) {
    const tab = Array.from(container.querySelectorAll('[role="tab"]'))
      .find(button => button.textContent?.includes(label)) as HTMLButtonElement | undefined
    expect(tab).toBeTruthy()
    await act(async () => {
      tab?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }

  async function click(element: HTMLElement) {
    await act(async () => {
      element.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
  }
})
