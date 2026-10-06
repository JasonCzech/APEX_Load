// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useActiveTopbarContribution } from '../hooks/TopbarContributionProvider'
import { TopbarContributionProvider } from '../hooks/TopbarContributionRoot'
import type { TestSummary } from '../types'
import TestExecution from './TestExecution'

const hooks = vi.hoisted(() => ({
  polling: vi.fn(), viewState: vi.fn(), scenario: vi.fn(), metrics: vi.fn(), resourceHistory: vi.fn(), summaryHistory: vi.fn(),
  chart: vi.fn(), actionBridge: vi.fn(), liveControls: vi.fn(), archive: vi.fn(), manifest: vi.fn(), importedGraphs: vi.fn(),
}))
const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', async importOriginal => ({ ...await importOriginal<typeof import('../hooks/apiClient')>(), request }))

vi.mock('./TestExecutionRunState', () => ({ default: () => <div>Run State Content</div> }))
vi.mock('./TestExecutionInfrastructure', () => ({ default: () => <div>Infrastructure Content</div> }))
vi.mock('./TestExecutionManifestCard', () => ({ default: ({ compact }: { compact?: boolean }) => <div>Configuration Content {compact ? 'compact' : 'full'}</div> }))
vi.mock('./TestExecutionCharts', () => ({ default: () => <div>Diagnostics Graphs Content</div> }))
vi.mock('./TestExecutionStatusPanels', () => ({ default: () => <div>Telemetry Capacity Content</div> }))
vi.mock('./TestExecutionScenarioEditor', () => ({ default: () => null }))
vi.mock('./TestNotesDialog', () => ({ default: () => null }))
vi.mock('./GenerateReportModal', () => ({ default: () => null }))
vi.mock('./ReportComposer', () => ({ default: () => null }))
vi.mock('./ConfirmRunActionModal', () => ({ default: () => null }))
vi.mock('./RunOutcomeSummary', () => ({ default: () => null }))
vi.mock('./TestExecutionScenarioOverview', () => ({ default: () => null }))
vi.mock('./TestExecutionHeroMetrics', () => ({ default: () => null }))
vi.mock('./TestExecutionLiveControls', () => ({ default: () => <div>Live Controls Content</div> }))
vi.mock('./TestExecutionScenarioPreview', () => ({ default: () => null }))
vi.mock('./TestExecutionSummary', () => ({ default: () => null }))
vi.mock('./TestExecutionArchive', () => ({ default: () => null }))
vi.mock('./useExecutionTestPolling', () => ({ useExecutionTestPolling: hooks.polling }))
vi.mock('./useExecutionViewState', () => ({ useExecutionViewState: hooks.viewState }))
vi.mock('./useExecutionScenarioPreviewModel', () => ({ useExecutionScenarioPreviewModel: hooks.scenario }))
vi.mock('./useExecutionMetricsStream', () => ({ useExecutionMetricsStream: hooks.metrics }))
vi.mock('./useExecutionArchiveResourceHistory', () => ({ useExecutionArchiveResourceHistory: hooks.resourceHistory }))
vi.mock('./useExecutionArchiveSummaryHistory', () => ({ useExecutionArchiveSummaryHistory: hooks.summaryHistory }))
vi.mock('./useExecutionChartModel', () => ({ useExecutionChartModel: hooks.chart }))
vi.mock('./useExecutionActionBridge', () => ({ useExecutionActionBridge: hooks.actionBridge }))
vi.mock('./useExecutionLiveControls', () => ({ useExecutionLiveControls: hooks.liveControls }))
vi.mock('./useExecutionArchiveModel', () => ({ useExecutionArchiveModel: hooks.archive }))
vi.mock('./useExecutionTestManifest', () => ({ useExecutionTestManifest: hooks.manifest }))
vi.mock('./useImportedDashboardGraphs', () => ({ useImportedDashboardGraphs: hooks.importedGraphs }))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function Topbar() {
  const contribution = useActiveTopbarContribution()
  return <div data-testid="topbar">{contribution?.secondaryActions?.filter(action => !action.hidden).map(action => <button key={action.id} onClick={action.onClick}>{action.label}</button>)}{contribution?.primaryActions?.filter(action => !action.hidden).map(action => <button key={action.id}>{action.label}</button>)}</div>
}

describe('TestExecution advanced details', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    window.localStorage.clear()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    configure(makeTest('RUNNING'))
    request.mockImplementation((path: string) => Promise.resolve(path.includes('/capabilities') ? { preparation_enabled: true, monitoring_enabled: false } : path.endsWith('/preparation') ? { id: 'prepared', state: 'none', revision: 1 } : path.endsWith('/analysis/annotations') ? { revision: 1, annotations: [] } : []))
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    vi.clearAllMocks()
  })

  it('uses a compact four-section investigation hierarchy', async () => {
    await render()
    expect(tabLabels()).toEqual(['Run state', 'Diagnostics', 'Configuration', 'Infrastructure'])
    expect(container.textContent).toContain('Run State Content')
    expect(container.textContent).not.toContain('Live Controls Content')
    expect(container.textContent).not.toContain('Card Library')
    expect(container.textContent).not.toContain('Customize view')
    expect(container.textContent).not.toContain('Prometheus connection')
  })

  it('keeps all run-changing actions in Command Center', async () => {
    await render()
    const text = container.querySelector('[data-testid="topbar"]')?.textContent || ''
    expect(text).toContain('Back to Command Center')
    expect(text).toContain('Test Notes')
    expect(text).not.toMatch(/Pause|Resume|Stop|Abort|Start Test|Pause VUsers|Revise/)
  })

  it('shows investigation evidence in the appropriate sections', async () => {
    await render()
    await clickTab('Diagnostics')
    expect(container.textContent).toContain('Diagnostics Graphs Content')
    expect(container.textContent).toContain('Telemetry Capacity Content')
    await clickTab('Configuration')
    expect(container.textContent).toContain('Configuration Content compact')
    await clickTab('Infrastructure')
    expect(container.textContent).toContain('Infrastructure Content')
  })

  it('accepts the new deep-link section and reports it to routing', async () => {
    const onWorkspaceTabChange = vi.fn()
    await render({ initialWorkspaceTabId: 'infrastructure', onWorkspaceTabChange })
    expect(selectedTab()).toBe('Infrastructure')
    expect(onWorkspaceTabChange).toHaveBeenLastCalledWith('infrastructure', { replace: true })
  })

  it('retains monitoring warnings as readable text during a running test', async () => {
    configure({ ...makeTest('RUNNING'), monitor_warnings: ['Optional Elastic: forbidden', '<img src=x onerror=alert(1)>'] })
    await render()
    const warnings = container.querySelector('[aria-label="Run monitoring warnings"]')!
    expect(warnings.textContent).toContain('Optional Elastic: forbidden')
    expect(warnings.textContent).toContain('<img src=x onerror=alert(1)>')
    expect(warnings.querySelector('img')).toBeNull()
  })

  it('retains one preparation control when monitoring warnings appear and disappear', async () => {
    const queued = { ...makeTest('QUEUED'), project_id: 'p' }
    configure(queued)
    await render({ canManageTests: true })
    expect(container.querySelectorAll('[aria-label="Run preparation"]')).toHaveLength(1)
    configure({ ...queued, monitor_warnings: ['Optional Elastic unavailable'] })
    await render({ canManageTests: true })
    expect(container.querySelectorAll('[aria-label="Run preparation"]')).toHaveLength(1)
    configure(queued)
    await render({ canManageTests: true })
    expect(container.querySelectorAll('[aria-label="Run preparation"]')).toHaveLength(1)
  })

  async function render(props: Partial<Parameters<typeof TestExecution>[0]> = {}) {
    await act(async () => root.render(<TopbarContributionProvider><Topbar /><TestExecution testId="run-1" history={[]} onBack={vi.fn()} onEditScript={vi.fn()} onSelectTest={vi.fn()} onOpenCommandCenter={vi.fn()} {...props} /></TopbarContributionProvider>))
  }

  async function clickTab(label: string) {
    const tab = Array.from(container.querySelectorAll<HTMLButtonElement>('[role="tab"]')).find(button => button.textContent?.includes(label))
    expect(tab).toBeTruthy()
    await act(async () => tab?.click())
  }

  function tabLabels() { return Array.from(container.querySelectorAll('.dashboard-workspace-tab > span')).map(node => node.textContent) }
  function selectedTab() { return container.querySelector('.dashboard-workspace-tab[aria-selected="true"] > span')?.textContent }

  function configure(test: TestSummary) {
    hooks.polling.mockReturnValue({ test, setTest: vi.fn(), error: '', archiveFinalizationTimedOut: false, archiveCheckPending: false, archiveCheckError: '', checkArchiveAgain: vi.fn() })
    hooks.viewState.mockReturnValue({ expandedChart: null, onExpandedChartChange: vi.fn(), expandedScenarioScript: null, onExpandedScenarioScriptChange: vi.fn() })
    hooks.scenario.mockReturnValue({ scenarioSnapshot: test, snapshotSource: 'manifest', queuedScenarioPreview: {}, queuedScenarioRows: [], snapshotLoading: false, snapshotError: '' })
    hooks.metrics.mockReturnValue({ localMetricsHistory: [], latestMetrics: null })
    hooks.resourceHistory.mockReturnValue({ archiveResourceHistory: [], archiveResourceError: '', archiveResourcesLoading: false })
    hooks.summaryHistory.mockReturnValue({ archiveSummaryHistory: [], archiveSummaryError: '', archiveSummariesLoading: false })
    hooks.chart.mockReturnValue({ filteredData: [], summary: {}, timeRange: '3m', refAreaLeft: '', refAreaEnd: '', showRefArea: false, onTimeRangeChange: vi.fn(), onChartMouseDown: vi.fn(), onChartMouseMove: vi.fn(), onZoom: vi.fn(), onResetZoom: vi.fn() })
    hooks.actionBridge.mockReturnValue({ controlFeedback: '', showControlFeedback: false, startCapacityConflict: null, startShakeoutConflict: null, startFeedback: '', onDismissStartConflict: vi.fn(), onStart: vi.fn(), onConfirmStartAnyway: vi.fn(), onContinueAfterShakeoutFailure: vi.fn(), onStop: vi.fn(), onAbort: vi.fn(), onPause: vi.fn(), onResume: vi.fn() })
    hooks.liveControls.mockReturnValue({ showControls: true, onToggleControls: vi.fn(), vuserTargets: {}, onVuserTargetChange: vi.fn(), vuserGroupActions: {}, onVuserGroupActionChange: vi.fn(), vuserRampEnabled: false, onVuserRampEnabledChange: vi.fn(), vuserRampDuration: '1m', onVuserRampDurationChange: vi.fn(), durationInput: '5m', onDurationInputChange: vi.fn(), onScaleVUsers: vi.fn(), onPauseAllVUsers: vi.fn(), onAdjustDuration: vi.fn(), liveControlPreview: { currentTotal: 1, projectedTotal: 1, remainingSeconds: 60 } })
    hooks.archive.mockReturnValue({ hasArchive: false, archiveError: '', archiveLoading: false, archiveReport: null, archiveAnalysis: null, archiveAnalysisLoading: false, archiveAnalysisError: '', archiveActionEntries: [], selectedArchiveAction: '', onSelectedArchiveActionChange: vi.fn(), archiveSOAPOperationEntries: [], selectedArchiveSOAPOperation: '', onSelectedArchiveSOAPOperationChange: vi.fn(), archiveGroups: [], selectedArchiveGroup: '', onSelectedArchiveGroupChange: vi.fn(), archiveErrorsOnly: false, onArchiveErrorsOnlyChange: vi.fn(), archiveTransactionSort: {}, onArchiveTransactionSortChange: vi.fn(), archiveTransactionFilters: {}, archiveTransactionFiltersActive: false, onArchiveTransactionFilterChange: vi.fn(), onArchiveTransactionFiltersReset: vi.fn(), archiveTransactionsLoading: false, archiveTransactionsTotal: 0, archivePageStart: 0, archivePageEnd: 0, archiveTransactionsOffset: 0, archivePageSize: 25, onPageSizeChange: vi.fn(), onPreviousPage: vi.fn(), onNextPage: vi.fn(), archiveTransactions: [], expandedArchiveEvent: null, onExpandedArchiveEventChange: vi.fn(), onDownloadArchive: vi.fn(), onDownloadBrowserDiagnostics: vi.fn(), onDownloadArchiveArtifact: vi.fn() })
    hooks.manifest.mockReturnValue({ manifest: null, manifestError: '', manifestLoading: false, reloadManifest: vi.fn() })
    hooks.importedGraphs.mockReturnValue({ importedDashboards: [], importedDashboardsLoading: false, importedDashboardsError: '' })
  }

  function makeTest(status: TestSummary['status']): TestSummary {
    return { id: 'run-1', name: 'Checkout flow', status, application_id: 'app', release_id: 'release', scenario_id: 'scenario', groups: [{ name: 'buyers', script_id: 'script', script_name: 'Checkout', protocol: 'http', vusers: 10 }], total_vusers: 10, duration: '5m', created_at: '2026-01-01T00:00:00Z', started_at: '2026-01-01T00:00:00Z' }
  }
})
