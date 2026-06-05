import { useMemo } from 'react'

import {
  MetricsHistoryPoint,
  TestSummary,
} from '../types'
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
import TestExecutionHeader from './TestExecutionHeader'
import TestExecutionHeroMetrics from './TestExecutionHeroMetrics'
import TestExecutionLiveControls from './TestExecutionLiveControls'
import TestExecutionScenarioOverview from './TestExecutionScenarioOverview'
import TestExecutionScenarioPreview from './TestExecutionScenarioPreview'
import TestExecutionStatusPanels from './TestExecutionStatusPanels'
import TestExecutionSummary from './TestExecutionSummary'
import { mergeArchiveResourceHistory } from './testExecutionMetricsModel'
import { buildExecutionPhaseModel } from './testExecutionPhaseModel'
import { buildExecutionStatusModel, describeGoalController } from './testExecutionStatusModel'
import DashboardWorkspace, {
  buildWorkspaceContextTokens,
  type DashboardWorkspaceCard,
  type DashboardWorkspaceProfile,
} from './DashboardWorkspace'
import { useExecutionChartModel } from './useExecutionChartModel'
import { useExecutionScenarioPreviewModel } from './useExecutionScenarioPreviewModel'
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
}

interface Props {
  testId: string
  history: MetricsHistoryPoint[]
  onBack: () => void
  onEditScript: (scriptId: string) => void
  onSelectTest: (testId: string) => void
}

export default function TestExecution({ testId, history, onBack, onEditScript, onSelectTest }: Props) {
  const { test, setTest, error } = useExecutionTestPolling(testId)
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
    archiveResourceHistory,
  })
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
  } = useExecutionActionBridge(testId, test, setTest)
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

  if (error) {
    return (
      <div className="execution">
        <button className="btn btn-secondary" onClick={onBack}>Back</button>
        <p className="error-msg">{error}</p>
      </div>
    )
  }

  if (!test) {
    return <div className="execution"><p className="loading">Loading...</p></div>
  }

  const {
    isRunning,
    isPaused,
    isStopping,
    showLiveControls,
    showArchiveSection,
    showArchiveDownload,
  } = buildExecutionPhaseModel(test, hasArchive)

  const {
    metrics,
    goalStatus,
    transactions,
    groupMetrics,
    isMultiGroup,
    groupNames,
    isGoalOriented,
    shakeoutFailed,
  } = buildExecutionStatusModel(test)

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

  const renderArchiveWorkspaceView = (view: 'report' | 'load-impact' | 'action-summary' | 'transactions') => showArchiveSection ? (
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

  const executionWorkspaceCards: DashboardWorkspaceCard[] = [
    {
      id: 'scenario-overview',
      title: 'Scenario Overview',
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
      render: () => (
        <TestExecutionHeroMetrics
          archiveReport={archiveReport}
          summary={summary}
          metrics={metrics}
        />
      ),
    },
    {
      id: 'live-controls',
      title: 'Live Controls',
      render: () => showLiveControls ? (
        <TestExecutionLiveControls
          showControls={showControls}
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
      ) : (
        <div className="empty-card">
          <p>Live controls are unavailable for this run state.</p>
          <p className="empty-hint">Start or resume a run to scale VUsers, adjust duration, pause, stop, or abort from this tab.</p>
        </div>
      ),
    },
    {
      id: 'scenario-preview',
      title: 'Scenario Preview',
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
          latestLatency={latestMetrics?.latency ?? test?.live_metrics?.latency}
          latestMetrics={latestMetrics ?? test?.live_metrics}
        />
      ),
    },
    {
      id: 'status-panels',
      title: 'Status Panels',
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
      render: () => renderArchiveWorkspaceView('report'),
    },
    {
      id: 'action-summary',
      title: 'Action Summary',
      render: () => renderArchiveWorkspaceView('action-summary'),
    },
    {
      id: 'load-impact-metrics',
      title: 'Load Impact + Metrics Under Load',
      render: () => renderArchiveWorkspaceView('load-impact'),
    },
    {
      id: 'transaction-drilldown',
      title: 'Transaction Drill-Down',
      render: () => renderArchiveWorkspaceView('transactions'),
    },
  ]

  return (
    <div className="execution">
      <TestExecutionHeader
        test={test}
        isMultiGroup={isMultiGroup}
        onBack={onBack}
        onStart={onStart}
        onSelectTest={onSelectTest}
        isStopping={isStopping}
        showArchiveDownload={showArchiveDownload}
        onDownloadArchive={onDownloadArchive}
      />

      <DashboardWorkspace
        ariaLabel="Test execution workspace"
        storageKey="apexload.execution.workspaceProfiles.v1"
        activeStorageKey="apexload.execution.activeWorkspaceProfile.v1"
        defaultProfile={EXECUTION_WORKSPACE_DEFAULT_PROFILE}
        cards={executionWorkspaceCards}
        context={executionWorkspaceContext}
      />
    </div>
  )
}
