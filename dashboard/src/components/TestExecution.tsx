import { useMemo, useRef, useState } from 'react'

import {
  MetricsHistoryPoint,
  TestSummary,
} from '../types'
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
import TestExecutionScenarioPreview from './TestExecutionScenarioPreview'
import TestExecutionStatusPanels from './TestExecutionStatusPanels'
import TestExecutionSummary from './TestExecutionSummary'
import ErrorDiagnostic from './ErrorDiagnostic'
import { mergeArchiveResourceHistory } from './testExecutionMetricsModel'
import { buildExecutionPhaseModel } from './testExecutionPhaseModel'
import { buildExecutionStatusModel, describeGoalController } from './testExecutionStatusModel'
import DashboardWorkspace, {
  type DashboardWorkspaceCard,
  type DashboardWorkspaceProfile,
} from './DashboardWorkspace'
import { buildWorkspaceContextTokens } from './dashboardWorkspaceModel'
import { useExecutionChartModel } from './useExecutionChartModel'
import { usePrometheusGraphs } from './usePrometheusGraphs'
import { useImportedDashboardGraphs } from './useImportedDashboardGraphs'
import { useExecutionScenarioPreviewModel } from './useExecutionScenarioPreviewModel'
import { useExecutionTestManifest } from './useExecutionTestManifest'
import './TestExecution.css'

const GROUP_COLORS = [
  'var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)',
  'var(--chart-4)', 'var(--chart-5)', 'var(--chart-6)',
]

const EXECUTION_WORKSPACE_DEFAULT_PROFILE: DashboardWorkspaceProfile = {
  id: 'default',
  name: 'Default Execution Layout',
  activeTabId: 'overview',
  tabs: [
    { id: 'overview', label: 'Overview', cardIds: ['scenario-overview', 'hero-metrics', 'test-manifest'] },
    { id: 'control', label: 'Control', cardIds: ['scenario-preview'] },
    { id: 'graphs', label: 'Graphs', cardIds: ['runtime-graphs'] },
    { id: 'diagnostics', label: 'Diagnostics', cardIds: ['status-panels', 'live-summary'] },
    { id: 'archive', label: 'Archive', cardIds: ['archive-report'] },
    { id: 'action-summary', label: 'Action Summary', cardIds: ['action-summary'] },
    { id: 'load-impact', label: 'Load Impact', cardIds: ['load-impact'] },
    { id: 'transaction-drilldown', label: 'Transaction Drill-Down', cardIds: ['transaction-drilldown'] },
  ],
  unassignedCardIds: [],
}

const EXECUTION_WORKSPACE_CARD_INSERTIONS = [
  {
    key: 'apexload.execution.testManifestCard.v1',
    profileId: 'default',
    tabId: 'overview',
    cardId: 'test-manifest',
    afterCardId: 'hero-metrics',
  },
]

function formatRunStartTime(startedAt?: string) {
  if (!startedAt) return 'Not started'
  const date = new Date(startedAt)
  if (!Number.isFinite(date.getTime())) return 'Unknown start'
  return date.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })
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
  testId: string
  history: MetricsHistoryPoint[]
  initialWorkspaceTabId?: string
  onBack: () => void
  onEditScript: (scriptId: string) => void
  onSelectTest: (testId: string) => void
  onOpenCommandCenter?: (testId: string) => void
}

export default function TestExecution({ testId, history, initialWorkspaceTabId, onBack, onEditScript, onSelectTest, onOpenCommandCenter }: Props) {
  const { test, setTest, error } = useExecutionTestPolling(testId)
  const [showScenarioEditor, setShowScenarioEditor] = useState(false)
  const [showGenerateReport, setShowGenerateReport] = useState(false)
  const [showTestNotes, setShowTestNotes] = useState(false)
  const [reportProfile, setReportProfile] = useState<ReportProfileId | null>(null)
  const {
    expandedChart,
    onExpandedChartChange,
    expandedScenarioScript,
    onExpandedScenarioScriptChange,
  } = useExecutionViewState()

  const {
    queuedScenarioPreview,
    queuedScenarioRows,
    scriptDetailsLoading,
    scriptDetailsError,
  } = useExecutionScenarioPreviewModel(test)
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
    onStopAllVUsers,
    onAdjustDuration,
    liveControlPreview,
  } = useExecutionLiveControls(testId, test, showControlFeedback)
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
  const {
    manifest,
    manifestError,
    manifestLoading,
    reloadManifest,
  } = useExecutionTestManifest(test)

  const topbarActionHandlers = useRef({
    onAbort,
    onBack,
    onDownloadArchive,
    onPause,
    onResume,
    onStart,
    onStop,
    onStopAllVUsers,
  })
  topbarActionHandlers.current = {
    onAbort,
    onBack,
    onDownloadArchive,
    onPause,
    onResume,
    onStart,
    onStop,
    onStopAllVUsers,
  }

  const executionPhase = test ? buildExecutionPhaseModel(test, hasArchive) : null
  const executionStatus = test ? buildExecutionStatusModel(test) : null
  const isRunning = executionPhase?.isRunning ?? false
  const isPaused = executionPhase?.isPaused ?? false
  const isStopping = executionPhase?.isStopping ?? false
  const showLiveControls = executionPhase?.showLiveControls ?? false
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
  const groupCount = (test?.groups || []).length

  const testTopbarContribution = useMemo<TopbarContribution | null>(() => test ? ({
    id: 'test-execution',
    priority: 90,
    title: 'Test Run',
    meta: [
      { id: 'status', label: topbarRunStatusLabel(test.status), tone: executionStatusTone },
      { id: 'name', label: test.name || 'Untitled test', tone: 'accent' },
      { id: 'planned-vusers', label: `Load ${test.total_vusers} VUsers`, tone: 'accent' },
      { id: 'duration', label: test.duration || 'No duration', tone: 'default' },
      { id: 'groups', label: `${groupCount} ${groupCount === 1 ? 'group' : 'groups'}`, tone: isMultiGroup ? 'accent' : 'default' },
      { id: 'started', label: `Started ${formatRunStartTime(test.started_at || test.start_time)}`, tone: 'default', hidden: !showLiveControls },
      { id: 'remaining', label: `Remaining ${formatTimeCompact(remainingSeconds)}`, tone: remainingSeconds <= 60 ? 'warning' : 'default', hidden: !showLiveControls },
      { id: 'current-vusers', label: `Current ${currentVUsers} VUsers`, tone: 'info', hidden: !showLiveControls },
      { id: 'target-vusers', label: `Target ${targetVUsers} VUsers`, tone: 'accent', hidden: !showLiveControls },
      { id: 'stopping', label: 'Stopping...', tone: 'danger', hidden: !isStopping },
    ],
    secondaryActions: [
      {
        id: 'back',
        label: 'Back',
        compactLabel: 'Back',
        tone: 'ghost',
        onClick: () => topbarActionHandlers.current.onBack(),
      },
      {
        id: 'revise',
        label: 'Revise Queued Run',
        compactLabel: 'Revise',
        tone: 'secondary',
        hidden: test.status !== 'QUEUED',
        onClick: () => setShowScenarioEditor(true),
      },
      {
        id: 'stop-vusers',
        label: 'Stop VUsers',
        compactLabel: 'Stop VU',
        tone: 'secondary',
        hidden: !showLiveControls,
        disabled: !hasActiveVUsers || rampInputMissing || isStopping,
        onClick: () => { void topbarActionHandlers.current.onStopAllVUsers() },
      },
      {
        id: 'test-notes',
        label: 'Test Notes',
        compactLabel: 'Notes',
        tone: 'secondary',
        onClick: () => setShowTestNotes(true),
      },
      {
        id: 'download-archive',
        label: 'Download Archive',
        compactLabel: 'Archive',
        tone: 'secondary',
        hidden: !showArchiveDownload,
        onClick: () => topbarActionHandlers.current.onDownloadArchive(),
      },
      {
        id: 'generate-report',
        label: 'Generate Report',
        compactLabel: 'Report',
        shortcut: 'r',
        shortcutGroup: 'Test Run',
        tone: 'secondary',
        hidden: !showArchiveDownload,
        onClick: () => setShowGenerateReport(true),
      },
    ],
    primaryActions: [
      {
        id: 'start',
        label: 'Start Test',
        compactLabel: 'Start',
        shortcut: 'Cmd/Ctrl+Enter',
        shortcutGroup: 'Test Run',
        tone: 'success',
        hidden: test.status !== 'QUEUED' && test.status !== 'RESERVED',
        onClick: () => { void topbarActionHandlers.current.onStart() },
      },
      {
        id: 'pause-resume',
        label: isPaused ? 'Resume' : 'Pause',
        compactLabel: isPaused ? 'Resume' : 'Pause',
        shortcut: 'p',
        shortcutGroup: 'Test Run',
        tone: isPaused ? 'success' : 'secondary',
        hidden: !showLiveControls,
        disabled: (!isRunning && !isPaused) || isStopping,
        onClick: () => {
          void (isPaused
            ? topbarActionHandlers.current.onResume()
            : topbarActionHandlers.current.onPause())
        },
      },
      {
        id: 'stop-test',
        label: 'Stop Test',
        compactLabel: 'Stop',
        shortcut: 'Shift+S',
        shortcutGroup: 'Test Run',
        tone: 'secondary',
        hidden: !showLiveControls,
        disabled: isStopping,
        onClick: () => { void topbarActionHandlers.current.onStop() },
      },
      {
        id: 'abort',
        label: 'Abort',
        compactLabel: 'Abort',
        tone: 'danger',
        hidden: !showLiveControls,
        // Abort stays enabled during STOPPING so a wedged graceful stop can be escalated.
        disabled: false,
        onClick: () => { void topbarActionHandlers.current.onAbort() },
      },
    ],
  }) : null, [
    currentVUsers,
    executionStatusTone,
    groupCount,
    hasActiveVUsers,
    isMultiGroup,
    isPaused,
    isRunning,
    isStopping,
    rampInputMissing,
    remainingSeconds,
    showArchiveDownload,
    showLiveControls,
    targetVUsers,
    test,
  ])

  useTopbarContribution(testTopbarContribution)

  if (error) {
    return (
      <div className="execution">
        <button className="btn btn-secondary" onClick={onBack}>Back</button>
        <ErrorDiagnostic error={error} fallback="Unable to load test execution details" className="execution-error-diagnostic" />
      </div>
    )
  }

  if (!test) {
    return <div className="execution"><p className="loading">Loading...</p></div>
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

  const pinnedLiveControls = showLiveControls ? (
    <TestExecutionLiveControls
      showControls={showControls}
      showRunActions={false}
      onToggleControls={onToggleControls}
      test={test}
      vuserTargets={vuserTargets}
      onVuserTargetChange={onVuserTargetChange}
      vuserGroupActions={vuserGroupActions}
      onVuserGroupActionChange={onVuserGroupActionChange}
      vuserRampEnabled={vuserRampEnabled}
      onVuserRampEnabledChange={onVuserRampEnabledChange}
      vuserRampDuration={vuserRampDuration}
      onVuserRampDurationChange={onVuserRampDurationChange}
      onScaleVUsers={onScaleVUsers}
      onStopAllVUsers={onStopAllVUsers}
      durationInput={durationInput}
      onDurationInputChange={onDurationInputChange}
      onAdjustDuration={onAdjustDuration}
      isRunning={isRunning}
      isPaused={isPaused}
      isStopping={isStopping}
      onPause={onPause}
      onResume={onResume}
      onStop={onStop}
      onAbort={onAbort}
      liveControlPreview={liveControlPreview}
      controlFeedback={controlFeedback}
      groupColors={GROUP_COLORS}
    />
  ) : null

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

  const executionWorkspaceCards: DashboardWorkspaceCard[] = [
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
          test={test}
          startCapacityConflict={startCapacityConflict}
          startShakeoutConflict={startShakeoutConflict}
          startFeedback={startFeedback}
          onDismissStartConflict={onDismissStartConflict}
          onConfirmStartAnyway={onConfirmStartAnyway}
          onContinueAfterShakeoutFailure={onContinueAfterShakeoutFailure}
          onCancelShakeoutRun={onAbort}
          queuedScenarioPreview={queuedScenarioPreview}
          queuedScenarioRows={queuedScenarioRows}
          scriptDetailsLoading={scriptDetailsLoading}
          scriptDetailsError={scriptDetailsError}
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
      title: 'Live Summary',
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

  return (
    <div className="execution">
      <TestExecutionScenarioEditor
        open={showScenarioEditor}
        test={test}
        onClose={() => setShowScenarioEditor(false)}
        onSelectTest={onSelectTest}
      />

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

      {pinnedLiveControls && (
        <section className="execution-live-controls-anchor" aria-label="Live controls">
          {pinnedLiveControls}
        </section>
      )}

      <DashboardWorkspace
        ariaLabel="Test execution workspace"
        storageKey="apexload.execution.workspaceProfiles.v1"
        activeStorageKey="apexload.execution.activeWorkspaceProfile.v1"
        cardLibraryStorageKey="apexload.execution.cardLibraryCollapsed.v1"
        defaultProfile={EXECUTION_WORKSPACE_DEFAULT_PROFILE}
        defaultCardInsertions={EXECUTION_WORKSPACE_CARD_INSERTIONS}
        cards={executionWorkspaceCards}
        context={executionWorkspaceContext}
        initialActiveTabId={initialWorkspaceTabId}
      />
    </div>
  )
}
