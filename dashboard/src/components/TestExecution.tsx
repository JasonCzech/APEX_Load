import {
  MetricsHistoryPoint,
} from '../types'
import TestExecutionArchive from './TestExecutionArchive'
import { useExecutionActionBridge } from './useExecutionActionBridge'
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
import { buildExecutionPhaseModel } from './testExecutionPhaseModel'
import { buildExecutionStatusModel, describeGoalController } from './testExecutionStatusModel'
import { useExecutionChartModel } from './useExecutionChartModel'
import { useExecutionScenarioPreviewModel } from './useExecutionScenarioPreviewModel'
import './TestExecution.css'

const GROUP_COLORS = [
  'var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)',
  'var(--chart-4)', 'var(--chart-5)', 'var(--chart-6)',
]

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
    onChartScrollWheel,
  } = useExecutionChartModel({
    test,
    history,
    localMetricsHistory,
  })
  const {
    controlFeedback,
    showControlFeedback,
    startCapacityConflict,
    startFeedback,
    onDismissStartConflict,
    onStart,
    onConfirmStartAnyway,
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
    durationInput,
    onDurationInputChange,
    onScaleVUsers,
    onAdjustDuration,
    liveControlPreview,
  } = useExecutionLiveControls(testId, test, showControlFeedback)
  const {
    hasArchive,
    archiveError,
    archiveLoading,
    archiveReport,
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

  return (
    <div className="execution">
      <TestExecutionHeader
        test={test}
        isMultiGroup={isMultiGroup}
        timeRange={timeRange}
        onTimeRangeChange={onTimeRangeChange}
        onBack={onBack}
        onStart={onStart}
        onSelectTest={onSelectTest}
        isRunning={isRunning}
        isPaused={isPaused}
        isStopping={isStopping}
        onStop={onStop}
        onAbort={onAbort}
        onPause={onPause}
        onResume={onResume}
        showArchiveDownload={showArchiveDownload}
        onDownloadArchive={onDownloadArchive}
      />

      {/* Live Controls Panel */}
      {showLiveControls && (
        <TestExecutionLiveControls
          showControls={showControls}
          onToggleControls={onToggleControls}
          test={test}
          vuserTargets={vuserTargets}
          onVuserTargetChange={onVuserTargetChange}
          onScaleVUsers={onScaleVUsers}
          durationInput={durationInput}
          onDurationInputChange={onDurationInputChange}
          onAdjustDuration={onAdjustDuration}
          liveControlPreview={liveControlPreview}
          controlFeedback={controlFeedback}
          groupColors={GROUP_COLORS}
        />
      )}

      <TestExecutionScenarioOverview
        test={test}
        isMultiGroup={isMultiGroup}
        shakeoutFailed={shakeoutFailed}
        groupColors={GROUP_COLORS}
      />

      <TestExecutionScenarioPreview
        test={test}
        startCapacityConflict={startCapacityConflict}
        startFeedback={startFeedback}
        onDismissStartConflict={onDismissStartConflict}
        onConfirmStartAnyway={onConfirmStartAnyway}
        queuedScenarioPreview={queuedScenarioPreview}
        queuedScenarioRows={queuedScenarioRows}
        scriptDetailsLoading={scriptDetailsLoading}
        scriptDetailsError={scriptDetailsError}
        expandedScenarioScript={expandedScenarioScript}
        onExpandedScenarioScriptChange={onExpandedScenarioScriptChange}
        onEditScript={onEditScript}
        groupColors={GROUP_COLORS}
      />

      {/* Hero metrics */}
      <TestExecutionHeroMetrics
        archiveReport={archiveReport}
        summary={summary}
        metrics={metrics}
      />

      {showArchiveSection && (
        <TestExecutionArchive
          hasArchive={hasArchive}
          test={test}
          archiveError={archiveError}
          archiveLoading={archiveLoading}
          archiveReport={archiveReport}
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
      )}

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

      {/* Charts */}
      <TestExecutionCharts
        filteredData={filteredData}
        expandedChart={expandedChart}
        onExpandedChartChange={onExpandedChartChange}
        onChartMouseDown={onChartMouseDown}
        onChartMouseMove={onChartMouseMove}
        onZoom={onZoom}
        onChartScrollWheel={onChartScrollWheel}
        showRefArea={showRefArea}
        refAreaLeft={refAreaLeft}
        refAreaEnd={refAreaEnd}
        isMultiGroup={isMultiGroup}
        groupNames={groupNames}
        groupColors={GROUP_COLORS}
      />

      <TestExecutionSummary
        transactions={summaryTransactions}
        metrics={statusMetrics}
        baseline={test.comparison_baseline}
        history={filteredData}
        goalConfig={test.goal_config}
      />
    </div>
  )
}
