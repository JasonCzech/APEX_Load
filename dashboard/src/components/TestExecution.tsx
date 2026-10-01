import NativeAnalyticsPanel, { NativePanelAuthoring, NativeProjectPanels } from './NativeAnalyticsPanel'
import { OrchestrationReturn } from './OrchestrationReturn'
import RunOutcomeSummary from './RunOutcomeSummary'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import {
  MetricsHistoryPoint,
  TestSummary,
} from '../types'
import { isTestWorkspaceTab, type TestWorkspaceTabID } from '../appRouting'
import { topbarRunStatusLabel } from '../appTopbar'
import { useTopbarContribution, type TopbarContribution, type TopbarMetaTone } from '../hooks/TopbarContributionProvider'
import { formatTimeCompact, isValidDurationInput, parseDurationToSeconds } from '../utils/timeUtils'
import TestExecutionArchive from './TestExecutionArchive'
import { useExecutionActionBridge } from './useExecutionActionBridge'
import { useExecutionArchiveResourceHistory } from './useExecutionArchiveResourceHistory'
import { useExecutionArchiveSummaryHistory } from './useExecutionArchiveSummaryHistory'
import { useExecutionArchiveModel } from './useExecutionArchiveModel'
import { useExecutionLiveControls } from './useExecutionLiveControls'
import { useExecutionMetricsStream } from './useExecutionMetricsStream'
import { useRunActionConfirmation } from './useRunActionConfirmation'
import { useExecutionTestPolling } from './useExecutionTestPolling'
import { useExecutionViewState } from './useExecutionViewState'
import TestExecutionCharts from './TestExecutionCharts'
import TestExecutionHeroMetrics from './TestExecutionHeroMetrics'
import TestExecutionLiveControls from './TestExecutionLiveControls'
import TestExecutionManifestCard from './TestExecutionManifestCard'
import TestExecutionScenarioOverview from './TestExecutionScenarioOverview'
import TestExecutionScenarioEditor from './TestExecutionScenarioEditor'
import GenerateReportModal from './GenerateReportModal'
import type { ReportProfileId } from './reportTypeOptions'
import ReportComposer from './ReportComposer'
import TestNotesDialog from './TestNotesDialog'
import ConfirmRunActionModal from './ConfirmRunActionModal'
import TestExecutionScenarioPreview from './TestExecutionScenarioPreview'
import TestExecutionStatusPanels from './TestExecutionStatusPanels'
import TestExecutionSummary from './TestExecutionSummary'
import GRPCDiagnosticsPanel from './GRPCDiagnosticsPanel'
import WebSocketDiagnosticsPanel from './WebSocketDiagnosticsPanel'
import PreparationControls from './PreparationControls'
import { RunMonitorTimeline } from './RunMonitoring'
import TestExecutionRunState from './TestExecutionRunState'
import TestExecutionInfrastructure from './TestExecutionInfrastructure'
import ErrorDiagnostic from './ErrorDiagnostic'
import { mergeArchiveResourceHistory } from './testExecutionMetricsModel'
import { buildExecutionPhaseModel } from './testExecutionPhaseModel'
import { buildExecutionStatusModel, describeGoalController } from './testExecutionStatusModel'
import DashboardWorkspace, {
  type DashboardWorkspaceCard,
  type DashboardWorkspaceProfile,
  type DashboardWorkspaceTabChangeSource,
} from './DashboardWorkspace'
import { buildWorkspaceContextTokens } from './dashboardWorkspaceModel'
import { useExecutionChartModel } from './useExecutionChartModel'
import { usePrometheusGraphs } from './usePrometheusGraphs'
import { useImportedDashboardGraphs } from './useImportedDashboardGraphs'
import { useExecutionScenarioPreviewModel } from './useExecutionScenarioPreviewModel'
import { useExecutionTestManifest } from './useExecutionTestManifest'
import './TestExecution.css'
import { useOptionalApi } from '../hooks/useApi'
import type { DashboardDefinition, DashboardDefinitionPanel } from '../hooks/api/useDashboardDefinitionsApi'

const GROUP_COLORS = [
  'var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)',
  'var(--chart-4)', 'var(--chart-5)', 'var(--chart-6)',
]

const EXECUTION_WORKSPACE_DEFAULT_PROFILE: DashboardWorkspaceProfile = {
  id: 'default',
  name: 'Advanced run details',
  activeTabId: 'overview',
  tabs: [
    { id: 'overview', label: 'Run state', cardIds: ['run-state'] },
    { id: 'diagnostics', label: 'Diagnostics', cardIds: ['runtime-graphs', 'status-panels'] },
    { id: 'configuration', label: 'Configuration', cardIds: ['test-manifest'] },
    { id: 'infrastructure', label: 'Infrastructure', cardIds: ['infrastructure'] },
  ],
  unassignedCardIds: [],
}

const EXECUTION_WORKSPACE_CARD_INSERTIONS: never[] = []

function formatRunStartTime(startedAt?: string) {
  if (!startedAt) return 'Not started'
  const date = new Date(startedAt)
  if (!Number.isFinite(date.getTime())) return 'Unknown start'
  return date.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })
}

function sharedDashboardPanelCardId(definitionId: string, panelId: string) {
  return `shared-dashboard:${definitionId}:${panelId}`
}

export function buildProjectDashboardWorkspace(definitions: DashboardDefinition[], history: MetricsHistoryPoint[]) {
  const panelByCardId = new Map<string, DashboardDefinitionPanel>()
  const cards: DashboardWorkspaceCard[] = []
  const profiles: DashboardWorkspaceProfile[] = definitions.map(definition => ({
    id: definition.id,
    name: definition.name,
    activeTabId: definition.tabs[0]?.id || 'overview',
    tabs: definition.tabs.map(tab => ({
      id: tab.id,
      label: tab.title,
      cardIds: tab.panels.map(panel => {
        if (panel.card_id) return panel.card_id
        if (!panel.query) return ''
        const cardId = sharedDashboardPanelCardId(definition.id, panel.id)
        panelByCardId.set(cardId, panel)
        cards.push({
          id: cardId,
          title: panel.title,
          category: 'Project analytics',
          description: `${panel.query.metric} · ${panel.query.aggregation}`,
          defaultWidth: panel.w >= 16 ? 'full' : panel.w >= 8 ? 'half' : 'natural',
          render: () => <NativeAnalyticsPanel panel={panel} history={history} />,
        })
        return cardId
      }).filter(Boolean),
    })),
    unassignedCardIds: [],
  }))
  return { cards, profiles, panelByCardId }
}

function computeRunRemainingSeconds(test: TestSummary) {
  const durationSeconds = parseDurationToSeconds(test.duration)
  const startedAt = test.started_at || test.start_time
  if (durationSeconds <= 0 || !startedAt) return 0
  const startedAtMs = Date.parse(startedAt)
  if (!Number.isFinite(startedAtMs)) return 0
  const elapsedSeconds = Math.max(0, Math.floor((Date.now() - startedAtMs) / 1000))
  return Math.max(durationSeconds - elapsedSeconds, 0)
}

interface Props {
  canManageTests?: boolean
  testId: string
  history: MetricsHistoryPoint[]
  initialWorkspaceTabId?: string
  onBack: () => void
  onEditScript: (scriptId: string) => void
  onWorkspaceTabChange?: (tabId: TestWorkspaceTabID | undefined, options: { replace: boolean }) => void
  onSelectTest: (testId: string) => void
  onOpenCommandCenter?: (testId: string) => void
}

export default function TestExecution({
  canManageTests = false,
  testId,
  history,
  initialWorkspaceTabId,
  onBack,
  onEditScript,
  onWorkspaceTabChange,
  onSelectTest,
  onOpenCommandCenter,
}: Props) {
  const api = useOptionalApi()
  const [projectDashboardDefinitions, setProjectDashboardDefinitions] = useState<DashboardDefinition[]>([])
  const {
    test,
    setTest,
    error,
    archiveFinalizationTimedOut,
    archiveCheckPending,
    archiveCheckError,
    checkArchiveAgain,
  } = useExecutionTestPolling(testId)
  const [showScenarioEditor, setShowScenarioEditor] = useState(false)
  const [showGenerateReport, setShowGenerateReport] = useState(false)
  const [showTestNotes, setShowTestNotes] = useState(false)
  const [reportProfile, setReportProfile] = useState<ReportProfileId | null>(null)
  const handleWorkspaceActiveTabChange = useCallback((tabId: string, source: DashboardWorkspaceTabChangeSource) => {
    onWorkspaceTabChange?.(isTestWorkspaceTab(tabId) ? tabId : undefined, { replace: source === 'sync' })
  }, [onWorkspaceTabChange])
  const {
    expandedChart,
    onExpandedChartChange,
    expandedScenarioScript,
    onExpandedScenarioScriptChange,
  } = useExecutionViewState()

  const {
    manifest,
    manifestError,
    manifestLoading,
    reloadManifest,
  } = useExecutionTestManifest(test)
  const {
    scenarioSnapshot,
    snapshotSource,
    queuedScenarioPreview,
    queuedScenarioRows,
    snapshotLoading,
    snapshotError,
  } = useExecutionScenarioPreviewModel(test, manifest, manifestLoading, manifestError)
  const { localMetricsHistory, latestMetrics } = useExecutionMetricsStream(testId, test?.status)
  const {
    archiveResourceHistory,
    archiveResourceError,
    archiveResourcesLoading,
  } = useExecutionArchiveResourceHistory(test)
  const {
    archiveSummaryHistory,
    archiveSummaryError,
    archiveSummariesLoading,
  } = useExecutionArchiveSummaryHistory(test)
  const archiveLoadImpactData = useMemo(
    () => mergeArchiveResourceHistory(archiveSummaryHistory, archiveResourceHistory),
    [archiveResourceHistory, archiveSummaryHistory],
  )
  const archiveLoadImpactError = [archiveSummaryError, archiveResourceError].filter(Boolean).join(' ')
  const {
    filteredData,
    summary,
    timeRange,
    refAreaLeft,
    refAreaEnd,
    showRefArea,
    onTimeRangeChange,
    onChartMouseDown,
    onChartMouseMove,
    onZoom,
    onResetZoom,
  } = useExecutionChartModel({
    test,
    history,
    localMetricsHistory,
    archiveSummaryHistory,
    archiveResourceHistory,
  })
  const {
    prometheusGraphs,
    prometheusLoading,
    prometheusError,
  } = usePrometheusGraphs(test, filteredData)
  const {
    importedDashboards,
    importedDashboardsLoading,
    importedDashboardsError,
  } = useImportedDashboardGraphs(test, filteredData, test?.project_id)
  useEffect(() => {
    const projectId = test?.project_id
    if (!projectId || !api) {
      setProjectDashboardDefinitions([])
      return
    }
    let cancelled = false
    void api.listDashboardDefinitions(projectId).then(response => {
      if (cancelled) return
      setProjectDashboardDefinitions(response.dashboards)
    }).catch(() => {
      if (!cancelled) setProjectDashboardDefinitions([])
    })
    return () => { cancelled = true }
  }, [api, test?.project_id])
  const {
    controlFeedback,
    showControlFeedback,
    startCapacityConflict,
    startShakeoutConflict,
    startFeedback,
    onDismissStartConflict,
    onStart,
    onConfirmStartAnyway,
    onContinueAfterShakeoutFailure,
    onStop,
    onAbort,
    onPause,
    onResume,
  } = useExecutionActionBridge(testId, test, setTest, onOpenCommandCenter)
  const {
    showControls,
    onToggleControls,
    vuserTargets,
    onVuserTargetChange,
    vuserGroupActions,
    onVuserGroupActionChange,
    vuserRampEnabled,
    onVuserRampEnabledChange,
    vuserRampDuration,
    onVuserRampDurationChange,
    durationInput,
    onDurationInputChange,
    onScaleVUsers,
    onPauseAllVUsers,
    onAdjustDuration,
    liveControlPreview,
  } = useExecutionLiveControls(testId, test, showControlFeedback)
  // Legacy saved-layout migration still reads these values; this screen deliberately does not.
  void TestExecutionLiveControls
  void controlFeedback
  void showControls
  void onToggleControls
  void vuserTargets
  void onVuserTargetChange
  void vuserGroupActions
  void onVuserGroupActionChange
  void onVuserRampEnabledChange
  void onVuserRampDurationChange
  void durationInput
  void onDurationInputChange
  void onScaleVUsers
  void onAdjustDuration
  const {
    hasArchive,
    archiveError,
    archiveLoading,
    archiveReport,
    archiveAnalysis,
    archiveAnalysisLoading,
    archiveAnalysisError,
    archiveActionEntries,
    selectedArchiveAction,
    onSelectedArchiveActionChange,
    archiveSOAPOperationEntries,
    selectedArchiveSOAPOperation,
    onSelectedArchiveSOAPOperationChange,
    archiveGroups,
    selectedArchiveGroup,
    onSelectedArchiveGroupChange,
    archiveErrorsOnly,
    onArchiveErrorsOnlyChange,
    archiveTransactionSort,
    onArchiveTransactionSortChange,
    archiveTransactionFilters,
    archiveTransactionFiltersActive,
    onArchiveTransactionFilterChange,
    onArchiveTransactionFiltersReset,
    archiveTransactionsLoading,
    archiveTransactionsTotal,
    archivePageStart,
    archivePageEnd,
    archiveTransactionsOffset,
    archivePageSize,
    onPageSizeChange,
    onPreviousPage,
    onNextPage,
    archiveTransactions,
    expandedArchiveEvent,
    onExpandedArchiveEventChange,
    onDownloadArchive,
    onDownloadBrowserDiagnostics,
    onDownloadArchiveArtifact,
  } = useExecutionArchiveModel(test)
  // Every control that changes what the run is doing to the target environment
  // is confirmed first. The wrappers are applied here, above both the topbar and
  // the live-controls panel, so the two surfaces cannot drift apart. onStart is
  // wrapped only for the plain Start button; the capacity/shakeout override
  // flows are already an explicit confirmation of their own.
  const { pendingAction, confirmRunAction, onConfirmRunAction, onCancelRunAction } = useRunActionConfirmation()
  const onConfirmStart = confirmRunAction('start', onStart)
  const onConfirmPause = confirmRunAction('pause', onPause)
  const onConfirmResume = confirmRunAction('resume', onResume)
  const onConfirmStop = confirmRunAction('stop', onStop)
  const onConfirmAbort = confirmRunAction('abort', onAbort)

  const topbarActionHandlers = useRef({
    onAbort: onConfirmAbort,
    onBack: onOpenCommandCenter ? () => onOpenCommandCenter(testId) : onBack,
    onDownloadArchive,
    onPause: onConfirmPause,
    onResume: onConfirmResume,
    onStart: onConfirmStart,
    onStop: onConfirmStop,
    onPauseAllVUsers,
  })
  topbarActionHandlers.current = {
    onAbort: onConfirmAbort,
    onBack: onOpenCommandCenter ? () => onOpenCommandCenter(testId) : onBack,
    onDownloadArchive,
    onPause: onConfirmPause,
    onResume: onConfirmResume,
    onStart: onConfirmStart,
    onStop: onConfirmStop,
    onPauseAllVUsers,
  }

  const executionPhase = test ? buildExecutionPhaseModel(test, hasArchive) : null
  const executionStatus = test ? buildExecutionStatusModel(test) : null
  const isRunning = executionPhase?.isRunning ?? false
  const isPaused = executionPhase?.isPaused ?? false
  const isStopping = executionPhase?.isStopping ?? false
  const showLiveControls = executionPhase?.showLiveControls ?? false
  const showAbortControl = executionPhase?.showAbortControl ?? false
  const showArchiveSection = executionPhase?.showArchiveSection ?? false
  const showArchiveDownload = executionPhase?.showArchiveDownload ?? false
  const isMultiGroup = executionStatus?.isMultiGroup ?? false
  const hasActiveVUsers = (test?.groups || []).some((group) => {
    if (!test) return false
    const currentVUsers = test.live_metrics?.groups?.[group.name]?.vusers ?? group.vusers
    return currentVUsers > 0 || (test.vuser_ramps?.[group.name]?.target_vusers ?? 0) > 0
  })
  const rampInputMissing = vuserRampEnabled && !isValidDurationInput(vuserRampDuration)
  const executionStatusTone: TopbarMetaTone = isStopping
    ? 'danger'
    : isPaused
      ? 'warning'
      : isRunning
        ? 'success'
        : test?.status === 'FAILED' || test?.status === 'ABORTED'
          ? 'danger'
          : 'default'
  const livePreviewCurrentTotal = Number.isFinite(liveControlPreview?.currentTotal) ? liveControlPreview.currentTotal : undefined
  const livePreviewProjectedTotal = Number.isFinite(liveControlPreview?.projectedTotal) ? liveControlPreview.projectedTotal : undefined
  const currentVUsers = livePreviewCurrentTotal
    ?? test?.live_metrics?.active_vusers
    ?? (test?.groups || []).reduce((total, group) => total + (test?.live_metrics?.groups?.[group.name]?.vusers ?? group.vusers), 0)
  const targetVUsers = livePreviewProjectedTotal
    ?? (test?.groups || []).reduce((total, group) => total + (test?.vuser_ramps?.[group.name]?.target_vusers ?? group.vusers), 0)
    ?? test?.total_vusers
    ?? 0
  const remainingSeconds = Number.isFinite(liveControlPreview?.remainingSeconds)
    ? liveControlPreview.remainingSeconds
    : test ? computeRunRemainingSeconds(test) : 0
  void showAbortControl
  void showArchiveDownload
  void hasActiveVUsers
  void rampInputMissing
  void currentVUsers
  void targetVUsers
  const groupCount = (test?.groups || []).length

  const testTopbarContribution = useMemo<TopbarContribution | null>(() => test ? ({
    id: 'test-execution',
    priority: 90,
    title: 'Advanced Run Details',
    meta: [
      { id: 'status', label: test.status === 'COMPLETE' && test.stopped_by_subject ? 'Stopped by operator' : topbarRunStatusLabel(test.status), tone: executionStatusTone },
      { id: 'name', label: test.name || 'Untitled test', tone: 'accent' },
      { id: 'duration', label: test.termination_mode === 'iterations' ? 'Iteration completion' : test.duration || 'No duration', tone: 'default' },
      { id: 'groups', label: `${groupCount} ${groupCount === 1 ? 'group' : 'groups'}`, tone: isMultiGroup ? 'accent' : 'default' },
      { id: 'started', label: `Started ${formatRunStartTime(test.started_at || test.start_time)}`, tone: 'default', hidden: !showLiveControls },
      { id: 'remaining', label: test.termination_mode === 'iterations' ? `Iterations ${test.live_metrics?.completed_iterations ?? 0} / ${(test.groups || []).reduce((n,g) => n + g.vusers * (g.iterations || 0), 0)}` : `Remaining ${formatTimeCompact(remainingSeconds)}`, tone: remainingSeconds <= 60 ? 'warning' : 'default', hidden: !showLiveControls },
      { id: 'stopping', label: 'Stopping...', tone: 'danger', hidden: !isStopping },
    ],
    secondaryActions: [
      {
        id: 'back',
        label: onOpenCommandCenter ? 'Back to Command Center' : 'Back',
        compactLabel: onOpenCommandCenter ? 'Back to Command Center' : 'Back',
        tone: 'secondary',
        onClick: () => topbarActionHandlers.current.onBack(),
      },
      {
        id: 'test-notes',
        label: 'Test Notes',
        compactLabel: 'Notes',
        tone: 'secondary',
        onClick: () => setShowTestNotes(true),
      },
    ],
    primaryActions: [],
  }) : null, [
    onOpenCommandCenter,
    executionStatusTone,
    groupCount,
    isMultiGroup,
    isStopping,
    remainingSeconds,
    showLiveControls,
    test,
  ])

  useTopbarContribution(testTopbarContribution)

  if (error) {
    return (
      <div className="execution">
      <OrchestrationReturn testId={testId} />
        <button className="btn btn-secondary" onClick={() => onOpenCommandCenter ? onOpenCommandCenter(testId) : onBack()}>{onOpenCommandCenter ? 'Back to Command Center' : 'Back'}</button>
        <ErrorDiagnostic error={error} fallback="Unable to load test execution details" className="execution-error-diagnostic" />
      </div>
    )
  }

  if (!test) {
    return <div className="execution">
      <OrchestrationReturn testId={testId} /><p className="loading">Loading...</p></div>
  }

  const {
    metrics,
    goalStatus,
    transactions,
    groupMetrics,
    groupNames,
    isGoalOriented,
    shakeoutFailed,
  } = executionStatus!

  const statusMetrics = latestMetrics
    ? {
        ...metrics,
        ...latestMetrics,
        telemetry: {
          ...metrics?.telemetry,
          ...latestMetrics.telemetry,
        },
      }
    : metrics

  const summaryTransactions = statusMetrics?.transactions ? Object.values(statusMetrics.transactions) : transactions

  const executionWorkspaceContext = {
    tokens: buildWorkspaceContextTokens(
      test.id,
      test.name,
      test.status,
      test.application_id,
      test.release_id,
      test.scenario_id,
      test.project_id,
      test.folder_id,
      test.scenario_mode,
      (test as TestSummary & { tags?: unknown }).tags,
      (test as TestSummary & { metadata?: unknown }).metadata,
      test.groups?.map(group => ({
        name: group.name,
        protocol: group.protocol,
        script_id: group.script_id,
        script_name: group.script_name,
      })),
    ),
  }

  const renderArchiveWorkspaceView = (
    view:
      | 'report'
      | 'report-overview'
      | 'report-completeness'
      | 'report-metadata'
      | 'report-error-patterns'
      | 'report-browser-summary'
      | 'report-browser-resources'
      | 'report-assets'
      | 'report-profiles'
      | 'report-summary-timeline'
      | 'report-analysis'
      | 'load-impact'
      | 'action-summary'
      | 'transactions',
  ) => showArchiveSection ? (
    <TestExecutionArchive
      view={view}
      hasArchive={hasArchive}
      test={test}
      archiveError={archiveError}
      archiveLoading={archiveLoading}
      archiveReport={archiveReport}
      archiveAnalysis={archiveAnalysis}
      archiveAnalysisLoading={archiveAnalysisLoading}
      archiveAnalysisError={archiveAnalysisError}
      loadImpactData={archiveLoadImpactData}
      loadImpactLoading={archiveSummariesLoading || archiveResourcesLoading}
      loadImpactError={archiveLoadImpactError}
      onDownloadBrowserDiagnostics={onDownloadBrowserDiagnostics}
      onDownloadArchiveArtifact={onDownloadArchiveArtifact}
      archiveActionEntries={archiveActionEntries}
      selectedArchiveAction={selectedArchiveAction}
      onSelectedArchiveActionChange={onSelectedArchiveActionChange}
      archiveSOAPOperationEntries={archiveSOAPOperationEntries}
      selectedArchiveSOAPOperation={selectedArchiveSOAPOperation}
      onSelectedArchiveSOAPOperationChange={onSelectedArchiveSOAPOperationChange}
      archiveGroups={archiveGroups}
      selectedArchiveGroup={selectedArchiveGroup}
      onSelectedArchiveGroupChange={onSelectedArchiveGroupChange}
      archiveErrorsOnly={archiveErrorsOnly}
      onArchiveErrorsOnlyChange={onArchiveErrorsOnlyChange}
      archiveTransactionSort={archiveTransactionSort}
      onArchiveTransactionSortChange={onArchiveTransactionSortChange}
      archiveTransactionFilters={archiveTransactionFilters}
      archiveTransactionFiltersActive={archiveTransactionFiltersActive}
      onArchiveTransactionFilterChange={onArchiveTransactionFilterChange}
      onArchiveTransactionFiltersReset={onArchiveTransactionFiltersReset}
      archiveTransactionsLoading={archiveTransactionsLoading}
      archiveTransactionsTotal={archiveTransactionsTotal}
      archivePageStart={archivePageStart}
      archivePageEnd={archivePageEnd}
      archiveTransactionsOffset={archiveTransactionsOffset}
      archivePageSize={archivePageSize}
      onPageSizeChange={onPageSizeChange}
      onPreviousPage={onPreviousPage}
      onNextPage={onNextPage}
      archiveTransactions={archiveTransactions}
      expandedArchiveEvent={expandedArchiveEvent}
      onExpandedArchiveEventChange={onExpandedArchiveEventChange}
    />
  ) : (
    <div className="empty-card">
      <p>Archive details are unavailable while this run is active.</p>
      <p className="empty-hint">Completed runs with archives show reports, analysis, load impact, action summaries, and transaction drilldown here.</p>
    </div>
  )

  // Legacy card catalog is retained for client-side migration of saved layouts,
  // but is never mounted by the focused Advanced Details surface below.
  if (test.id === '__legacy-profile-migration__') {
  const liveControlsCard: DashboardWorkspaceCard = {
    id: 'live-controls',
    title: 'Live Controls',
    category: 'Run Control',
    description: 'Detailed VUser scaling and duration controls.',
    render: () => (
      <div className="empty-card live-controls-pinned-card">
        <p>Detailed live controls are pinned above this run dashboard while the run is active.</p>
        <p className="empty-hint">Use the banner for pause, stop, and abort actions; use the live controls panel for VUser scaling and duration changes.</p>
      </div>
    ),
  }

  const legacyExecutionWorkspaceCards: DashboardWorkspaceCard[] = [
    {
      id: 'scenario-overview',
      title: 'Scenario Overview',
      category: 'Run Overview',
      description: 'Scenario identity, groups, and run readiness.',
      defaultWidth: 'natural',
      render: () => (
        <TestExecutionScenarioOverview
          test={test}
          isMultiGroup={isMultiGroup}
          shakeoutFailed={shakeoutFailed}
          groupColors={GROUP_COLORS}
        />
      ),
    },
    {
      id: 'hero-metrics',
      title: 'Hero Metrics',
      category: 'Run Overview',
      description: 'Top-line execution and archive metrics.',
      defaultWidth: 'half',
      render: () => (
        <TestExecutionHeroMetrics
          archiveReport={archiveReport}
          summary={summary}
          metrics={metrics}
        />
      ),
    },
	{
		id: 'active-load-levels',
		title: 'Active Load Levels',
		category: 'Run Overview',
		description: 'Current stepped-ramp measurement intervals and load-level metrics.',
		defaultWidth: 'half',
		render: () => {
			const levels = statusMetrics?.load_levels || []
			return levels.length > 0 ? (
				<div className="execution-load-levels" aria-live="polite">
					<table>
						<thead><tr><th>Group</th><th>Level</th><th>VUsers</th><th>TPS</th><th>Error</th><th>P95</th></tr></thead>
						<tbody>{levels.map(level => {
							const group = level.group === '__total__' ? undefined : statusMetrics?.groups?.[level.group]
							return <tr key={`${level.group}-${level.stage_index}-${level.target_vusers}`}><td>{level.group === '__total__' ? 'Total' : level.group}</td><td>{level.name}</td><td>{level.target_vusers}</td><td>{(group?.tps ?? statusMetrics?.tps ?? 0).toFixed(1)}</td><td>{group ? (group.transactions > 0 ? `${((group.errors / group.transactions) * 100).toFixed(1)}%` : '0.0%') : `${(statusMetrics?.error_pct ?? 0).toFixed(1)}%`}</td><td>{(group?.p95_ms ?? statusMetrics?.p95_ms ?? 0).toFixed(1)} ms</td></tr>
						})}</tbody>
					</table>
				</div>
			) : <div className="empty-card"><p>No stepped-ramp interval is active.</p><p className="empty-hint">The current group and aggregate load levels appear here as each hold begins.</p></div>
		},
	},
    {
      id: 'test-manifest',
      title: 'Test Manifest',
      category: 'Run Overview',
      description: 'Canonical live and archived run configuration.',
      defaultWidth: 'full',
      render: () => (
        <TestExecutionManifestCard
          manifest={manifest}
          error={manifestError}
          loading={manifestLoading}
          onReload={reloadManifest}
        />
      ),
    },
    ...(showLiveControls ? [liveControlsCard] : []),
    {
      id: 'scenario-preview',
      title: 'Scenario Preview',
      category: 'Run Control',
      description: 'Queued scenario, script preview, and start blockers.',
      defaultWidth: 'full',
      render: () => (
        <TestExecutionScenarioPreview
          test={scenarioSnapshot || test}
          startCapacityConflict={startCapacityConflict}
          startShakeoutConflict={startShakeoutConflict}
          startFeedback={startFeedback}
          onDismissStartConflict={onDismissStartConflict}
          onConfirmStartAnyway={onConfirmStartAnyway}
          onContinueAfterShakeoutFailure={onContinueAfterShakeoutFailure}
          onCancelShakeoutRun={onConfirmAbort}
          queuedScenarioPreview={queuedScenarioPreview}
          queuedScenarioRows={queuedScenarioRows}
          snapshotSource={snapshotSource}
          snapshotLoading={snapshotLoading}
          snapshotError={snapshotError}
          expandedScenarioScript={expandedScenarioScript}
          onExpandedScenarioScriptChange={onExpandedScenarioScriptChange}
          onEditScript={onEditScript}
          groupColors={GROUP_COLORS}
        />
      ),
    },
    {
      id: 'runtime-graphs',
      title: 'Runtime Graphs',
      category: 'Runtime Monitoring',
      description: 'Live and archived execution charts.',
      render: () => (
        <TestExecutionCharts
          filteredData={filteredData}
          expandedChart={expandedChart}
          onExpandedChartChange={onExpandedChartChange}
          onChartMouseDown={onChartMouseDown}
          onChartMouseMove={onChartMouseMove}
          onZoom={onZoom}
          onResetZoom={onResetZoom}
          showRefArea={showRefArea}
          refAreaLeft={refAreaLeft}
          refAreaEnd={refAreaEnd}
          timeRange={timeRange}
          onTimeRangeChange={onTimeRangeChange}
          isMultiGroup={isMultiGroup}
          groupNames={groupNames}
          groupColors={GROUP_COLORS}
          showLoadImpact={!(showArchiveSection && archiveReport)}
          loadImpactData={archiveLoadImpactData.length > 0 ? archiveLoadImpactData : filteredData}
          loadImpactLoading={archiveSummariesLoading || archiveResourcesLoading}
          loadImpactError={archiveLoadImpactError}
          prometheusGraphs={prometheusGraphs}
          prometheusLoading={prometheusLoading}
          prometheusError={prometheusError}
          importedDashboards={importedDashboards}
          importedDashboardsLoading={importedDashboardsLoading}
          importedDashboardsError={importedDashboardsError}
          enableGraphGroupTabs={prometheusGraphs.length > 0 || prometheusLoading || Boolean(prometheusError) || importedDashboards.length > 0 || importedDashboardsLoading || Boolean(importedDashboardsError)}
          latestLatency={latestMetrics?.latency ?? test?.live_metrics?.latency}
          latestMetrics={latestMetrics ?? test?.live_metrics}
          terminalStatus={test?.status === 'FAILED' || test?.status === 'ABORTED' ? test.status : undefined}
        />
      ),
    },
    {
      id: 'status-panels',
      title: 'Status Panels',
      category: 'Runtime Monitoring',
      description: 'Goal, telemetry, and group status panels.',
      render: () => (
        <TestExecutionStatusPanels
          isGoalOriented={isGoalOriented}
          goalDescription={describeGoalController(goalStatus)}
          goalStatus={goalStatus}
          test={test}
          metrics={statusMetrics}
          isMultiGroup={isMultiGroup}
          groupMetrics={groupMetrics}
          groupColors={GROUP_COLORS}
        />
      ),
    },
    {
      id: 'live-summary',
      title: ['COMPLETE', 'FAILED', 'ABORTED'].includes(test.status) ? 'Captured Summary' : 'Live Summary',
      category: 'Run Overview',
      description: 'Current transaction and baseline summary.',
      defaultWidth: 'full',
      render: () => (
        <TestExecutionSummary
          transactions={summaryTransactions}
          metrics={statusMetrics}
          baseline={test.comparison_baseline}
          history={filteredData}
          goalConfig={test.goal_config}
        />
      ),
    },
    {
      id: 'archive-report',
      title: 'Archive Report',
      category: 'Archive Summary',
      description: 'Full consolidated post-run report.',
      defaultWidth: 'full',
      render: () => renderArchiveWorkspaceView('report'),
    },
    {
      id: 'archive-overview',
      title: 'Archive Overview',
      category: 'Archive Summary',
      description: 'Top-line archived run, goal, response, and resource metrics.',
      defaultWidth: 'full',
      render: () => renderArchiveWorkspaceView('report-overview'),
    },
    {
      id: 'archive-completeness',
      title: 'Completeness Score',
      category: 'Archive Summary',
      description: 'Archive coverage, quality findings, and retained evidence score.',
      defaultWidth: 'full',
      render: () => renderArchiveWorkspaceView('report-completeness'),
    },
    {
      id: 'archive-metadata',
      title: 'Run Metadata',
      category: 'Archive Summary',
      description: 'Telemetry health, schema compatibility, provenance, and attribution.',
      defaultWidth: 'full',
      render: () => renderArchiveWorkspaceView('report-metadata'),
    },
    {
      id: 'archive-error-patterns',
      title: 'Error Patterns',
      category: 'Archive Evidence',
      description: 'Grouped failure signatures and first retained snapshots.',
      defaultWidth: 'full',
      render: () => renderArchiveWorkspaceView('report-error-patterns'),
    },
    {
      id: 'archive-browser-summary',
      title: 'Browser Metrics',
      category: 'Archive Evidence',
      description: 'Browser UX, engine, platform, and device breakdowns.',
      defaultWidth: 'full',
      render: () => renderArchiveWorkspaceView('report-browser-summary'),
    },
    {
      id: 'archive-browser-resources',
      title: 'Browser Resource Waterfall',
      category: 'Archive Evidence',
      description: 'Slowest archived browser sub-resources and transfer timings.',
      defaultWidth: 'full',
      render: () => renderArchiveWorkspaceView('report-browser-resources'),
    },
    {
      id: 'archive-assets',
      title: 'Report Assets',
      category: 'Archive Exports',
      description: 'Downloadable reports, diagnostics, JSONL, and Parquet artifacts.',
      defaultWidth: 'full',
      render: () => renderArchiveWorkspaceView('report-assets'),
    },
    {
      id: 'archive-profiles',
      title: 'Report Profiles',
      category: 'Archive Exports',
      description: 'Profile-specific report packages and export options.',
      defaultWidth: 'full',
      render: () => renderArchiveWorkspaceView('report-profiles'),
    },
    {
      id: 'archive-summary-timeline',
      title: 'Summary Timeline',
      category: 'Archive Evidence',
      description: 'Archived interval snapshots for throughput, latency, and errors.',
      defaultWidth: 'full',
      render: () => renderArchiveWorkspaceView('report-summary-timeline'),
    },
    {
      id: 'archive-analysis',
      title: 'Archive Analysis',
      category: 'Analysis & Drilldown',
      description: 'Graph nodes, links, and transaction path evidence.',
      defaultWidth: 'full',
      render: () => renderArchiveWorkspaceView('report-analysis'),
    },
    {
      id: 'action-summary',
      title: 'Action Summary',
      category: 'Analysis & Drilldown',
      description: 'Archived totals by action and protocol grouping.',
      defaultWidth: 'full',
      render: () => renderArchiveWorkspaceView('action-summary'),
    },
    {
      id: 'load-impact',
      title: 'Load Impact',
      category: 'Analysis & Drilldown',
      description: 'Archive load impact heatmap grouped by active VUser load.',
      defaultWidth: 'full',
      render: () => renderArchiveWorkspaceView('load-impact'),
    },
    {
      id: 'transaction-drilldown',
      title: 'Transaction Drill-Down',
      category: 'Analysis & Drilldown',
      description: 'Archived event table, filters, and transaction evidence.',
      defaultWidth: 'full',
      render: () => renderArchiveWorkspaceView('transactions'),
    },
  ]
  // Retained only for legacy profile migration; the Advanced Details surface does not render it.
  void legacyExecutionWorkspaceCards

  }
  const executionWorkspaceCards: DashboardWorkspaceCard[] = [
    {
      id: 'run-state',
      title: 'Run state',
      category: 'Investigation',
      description: 'Observed workload, group health, and telemetry freshness.',
      render: () => (
        <TestExecutionRunState
          test={test}
          groupMetrics={groupMetrics}
          remainingSeconds={remainingSeconds}
        />
      ),
    },
    {
      id: 'runtime-graphs',
      title: 'Diagnostics',
      category: 'Investigation',
      description: 'Live execution charts and telemetry signals.',
      render: () => (
        <TestExecutionCharts
          filteredData={filteredData}
          expandedChart={expandedChart}
          onExpandedChartChange={onExpandedChartChange}
          onChartMouseDown={onChartMouseDown}
          onChartMouseMove={onChartMouseMove}
          onZoom={onZoom}
          onResetZoom={onResetZoom}
          showRefArea={showRefArea}
          refAreaLeft={refAreaLeft}
          refAreaEnd={refAreaEnd}
          timeRange={timeRange}
          onTimeRangeChange={onTimeRangeChange}
          isMultiGroup={isMultiGroup}
          groupNames={groupNames}
          groupColors={GROUP_COLORS}
          showLoadImpact={false}
          loadImpactData={[]}
          loadImpactLoading={false}
          loadImpactError=""
          prometheusGraphs={prometheusGraphs}
          prometheusLoading={prometheusLoading}
          prometheusError={prometheusError}
          importedDashboards={importedDashboards}
          importedDashboardsLoading={importedDashboardsLoading}
          importedDashboardsError={importedDashboardsError}
          enableGraphGroupTabs={prometheusGraphs.length > 0 || prometheusLoading || Boolean(prometheusError) || importedDashboards.length > 0 || importedDashboardsLoading || Boolean(importedDashboardsError)}
          latestLatency={latestMetrics?.latency ?? test?.live_metrics?.latency}
          latestMetrics={latestMetrics ?? test?.live_metrics}
          terminalStatus={test?.status === 'FAILED' || test?.status === 'ABORTED' ? test.status : undefined}
        />
      ),
    },
    {
      id: 'status-panels',
      title: 'Telemetry and capacity',
      category: 'Investigation',
      description: 'Health signals, throughput tracking, and capacity evidence.',
      render: () => (
        <>
        <TestExecutionStatusPanels
          isGoalOriented={isGoalOriented}
          goalDescription={describeGoalController(goalStatus)}
          goalStatus={goalStatus}
          test={test}
          metrics={statusMetrics}
          isMultiGroup={isMultiGroup}
          groupMetrics={groupMetrics}
          groupColors={GROUP_COLORS}
        />
        <GRPCDiagnosticsPanel transactions={summaryTransactions} />
        <WebSocketDiagnosticsPanel transactions={summaryTransactions} />
        </>
      ),
    },
    {
      id: 'test-manifest',
      title: 'Configuration changes',
      category: 'Evidence',
      description: 'Initial versus current configuration and runtime-change audit trail.',
      render: () => (
        <TestExecutionManifestCard
          manifest={manifest}
          error={manifestError}
          loading={manifestLoading}
          onReload={reloadManifest}
          compact
        />
      ),
    },
    {
      id: 'infrastructure',
      title: 'Infrastructure evidence',
      category: 'Evidence',
      description: 'Generator assignment and telemetry provenance.',
      render: () => (
        <TestExecutionInfrastructure
          manifest={manifest}
          manifestError={manifestError}
          manifestLoading={manifestLoading}
          test={test}
        />
      ),
    },
  ]

  const {
    cards: projectDashboardCards,
    profiles: projectDashboardProfiles,
    panelByCardId: sharedPanelByCardId,
  } = buildProjectDashboardWorkspace(projectDashboardDefinitions, filteredData)

  const allExecutionWorkspaceCards = [...executionWorkspaceCards, ...projectDashboardCards]

  const saveWorkspaceToProject = async (profile: DashboardWorkspaceProfile, mode: 'save' | 'import') => {
    const projectId = test?.project_id
    if (!projectId || !api) throw new Error('Project dashboard service is unavailable')
    const created = await api.createDashboardDefinition({
      project_id: projectId,
      name: mode === 'import' ? `${profile.name} (Imported)` : profile.name,
      description: 'Shared from the test execution workspace.',
      visibility: 'project',
      tabs: profile.tabs.map(tab => ({
        id: tab.id,
        title: tab.label,
        panels: tab.cardIds.map((cardId, index) => {
          const sharedPanel = sharedPanelByCardId.get(cardId)
          if (sharedPanel) {
            return { ...sharedPanel, id: `${tab.id}-${sharedPanel.id}`, y: index * 8 }
          }
          return {
            id: `${tab.id}-${cardId}`,
            title: allExecutionWorkspaceCards.find(card => card.id === cardId)?.title || cardId,
            card_id: cardId,
            kind: 'workspace_card',
            x: 0,
            y: index * 8,
            w: 24,
            h: 8,
          }
        }),
      })),
    })
    setProjectDashboardDefinitions(current => [...current.filter(item => item.id !== created.id), created])
  }

  return (
    <div className="execution">
      <OrchestrationReturn testId={testId} />
      <RunMonitorTimeline key={`monitor-${testId}`} projectId={test.project_id} testId={testId} history={filteredData} />
      <PreparationControls canEdit={canManageTests} key={testId} testId={testId} projectId={test.project_id} status={test.status} />
      <TestExecutionScenarioEditor
        open={showScenarioEditor}
        test={test}
        onClose={() => setShowScenarioEditor(false)}
        onSelectTest={onSelectTest}
      />

      {pendingAction && (
        <ConfirmRunActionModal
          action={pendingAction}
          runName={test?.name}
          runRef={test?.ref_key}
          onConfirm={onConfirmRunAction}
          onCancel={onCancelRunAction}
        />
      )}

      {showGenerateReport && (
        <GenerateReportModal
          profiles={archiveReport?.report_profiles}
          onClose={() => setShowGenerateReport(false)}
          onGenerate={(profile) => {
            setReportProfile(profile)
            setShowGenerateReport(false)
          }}
        />
      )}

      {reportProfile && (
        <ReportComposer
          testId={testId}
          testName={test?.name ?? testId}
          profile={reportProfile}
          onClose={() => setReportProfile(null)}
        />
      )}

      {showTestNotes && (
        <TestNotesDialog
          testId={testId}
          testName={test?.name ?? testId}
          onClose={() => setShowTestNotes(false)}
        />
      )}

      {archiveFinalizationTimedOut && !hasArchive && (
        <section className="execution-archive-retry" aria-label="Archive publication status" role="status">
          <div>
            <strong>Saved archive unavailable</strong>
            <p>This run has ended, but no saved archive is available. Publication progress is unknown. Check again or inspect retained details in this snapshot.</p>
            {archiveCheckError && <p className="execution-archive-retry-error" role="alert">{archiveCheckError}</p>}
          </div>
          <button
            className="btn btn-sm btn-secondary"
            type="button"
            onClick={() => { void checkArchiveAgain() }}
            disabled={archiveCheckPending}
          >
            {archiveCheckPending ? 'Checking…' : 'Check again'}
          </button>
        </section>
      )}

      {archiveReport && <RunOutcomeSummary report={archiveReport} />}
      {canManageTests && api && test.project_id && <NativePanelAuthoring properties={Object.keys(archiveReport?.by_property || {}).sort()} key={`panels-${testId}`} onSave={async definition => {
        const created = await api.createDashboardDefinition({ ...definition, project_id: test.project_id!, visibility: 'project' })
        setProjectDashboardDefinitions(current => [...current, created])
      }} />}
      <NativeProjectPanels testId={testId} key={`project-panels-${testId}`} definitions={projectDashboardDefinitions} history={filteredData} />
      <DashboardWorkspace
        key={testId}
        ariaLabel="Test execution workspace"
        storageKey="apexload.execution.workspaceProfiles.v1"
        activeStorageKey="apexload.execution.activeWorkspaceProfile.v1"
        cardLibraryStorageKey="apexload.execution.cardLibraryCollapsed.v1"
        defaultProfile={EXECUTION_WORKSPACE_DEFAULT_PROFILE}
        allowCustomization={false}
        defaultCardInsertions={EXECUTION_WORKSPACE_CARD_INSERTIONS}
        cards={allExecutionWorkspaceCards}
        projectProfiles={projectDashboardProfiles}
        onSaveToProject={canManageTests ? saveWorkspaceToProject : undefined}
        context={executionWorkspaceContext}
        initialActiveTabId={initialWorkspaceTabId}
        onActiveTabChange={handleWorkspaceActiveTabChange}
      />
    </div>
  )
}
