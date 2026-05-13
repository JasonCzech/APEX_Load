import { useCallback } from 'react'
import { useApi } from '../hooks/useApi'
import {
  TestSummary,
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
  const api = useApi()
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
  const { localMetricsHistory } = useExecutionMetricsStream(testId)
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
    onPreviousPage,
    onNextPage,
    archiveTransactions,
    expandedArchiveEvent,
    onExpandedArchiveEventChange,
    onDownloadArchive,
    onDownloadArchiveArtifact,
  } = useExecutionArchiveModel(test)

  const isRunning = test?.status === 'RUNNING' || test?.status === 'INITIALIZING'

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
    metrics,
    goalStatus,
    transactions,
    groupMetrics,
    isMultiGroup,
    groupNames,
    isGoalOriented,
    shakeoutFailed,
  } = buildExecutionStatusModel(test)

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
        onStop={() => api.stopTest(testId)}
        onAbort={() => api.abortTest(testId)}
        onPause={onPause}
        onResume={onResume}
        hasArchive={hasArchive}
        onDownloadArchive={onDownloadArchive}
      />

      {/* Live Controls Panel */}
      {(isRunning || test.status === 'PAUSED') && (
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

      {!isRunning && (
        <TestExecutionArchive
          hasArchive={hasArchive}
          test={test}
          archiveError={archiveError}
          archiveLoading={archiveLoading}
          archiveReport={archiveReport}
          onDownloadArchiveArtifact={onDownloadArchiveArtifact}
          archiveActionEntries={archiveActionEntries}
          selectedArchiveAction={selectedArchiveAction}
          onSelectedArchiveActionChange={onSelectedArchiveActionChange}
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
        metrics={metrics}
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
        showRefArea={showRefArea}
        refAreaLeft={refAreaLeft}
        refAreaEnd={refAreaEnd}
        isMultiGroup={isMultiGroup}
        groupNames={groupNames}
        groupColors={GROUP_COLORS}
      />

      <TestExecutionSummary
        transactions={transactions}
        metrics={metrics}
      />
    </div>
  )
}
