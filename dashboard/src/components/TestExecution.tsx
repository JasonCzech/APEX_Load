import { useState, useEffect, useMemo, useCallback, useReducer } from 'react'
import { ApiError, useApi } from '../hooks/useApi'
import {
  CapacityAdvisory,
  TestSummary,
  MetricsHistoryPoint,
  TxMetrics,
  GoalStatus,
} from '../types'
import TestExecutionArchive from './TestExecutionArchive'
import { useExecutionActionBridge } from './useExecutionActionBridge'
import { useExecutionArchiveModel } from './useExecutionArchiveModel'
import { useExecutionLiveControls } from './useExecutionLiveControls'
import { useExecutionMetricsStream } from './useExecutionMetricsStream'
import { useExecutionTestPolling } from './useExecutionTestPolling'
import { useExecutionViewState } from './useExecutionViewState'
import {
  executionChartZoomReducer,
  getExecutionChartRefArea,
  initialExecutionChartZoomState,
} from './testExecutionChartZoomModel'
import TestExecutionCharts from './TestExecutionCharts'
import TestExecutionHeader from './TestExecutionHeader'
import TestExecutionHeroMetrics from './TestExecutionHeroMetrics'
import TestExecutionLiveControls from './TestExecutionLiveControls'
import {
  buildExecutionChartData,
  filterExecutionChartData,
  mapExecutionHistory,
  summarizeExecutionChartData,
} from './testExecutionMetricsModel'
import TestExecutionScenarioPreview from './TestExecutionScenarioPreview'
import TestExecutionStatusPanels from './TestExecutionStatusPanels'
import TestExecutionSummary from './TestExecutionSummary'
import { buildQueuedScenarioPreview } from './testExecutionScenarioModel'
import { useExecutionScenarioPreviewModel } from './useExecutionScenarioPreviewModel'
import { formatTimeLong } from '../utils/timeUtils'
import { getErrorMessage } from '../utils/errorUtils'
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

function formatGoalScaleReason(reason?: string): string {
  if (!reason) return 'Tracking target'
  return reason.replace(/_/g, ' ')
}

function describeGoalController(status?: GoalStatus): string {
  if (!status) return 'Waiting for live metrics'
  if (status.stop_triggered) return `Stopping: ${status.stop_reason || 'SLA breached'}`
  if (status.breach_active) return `SLA breach active for ${status.breach_duration_sec || 0}s`
  if (status.last_scale_direction) {
    return `Last scale ${status.last_scale_direction} due to ${formatGoalScaleReason(status.last_scale_reason)}`
  }
  return 'Tracking target'
}

export default function TestExecution({ testId, history, onBack, onEditScript, onSelectTest }: Props) {
  const api = useApi()
  const { test, setTest, error } = useExecutionTestPolling(testId)
  const [chartZoomState, dispatchChartZoom] = useReducer(executionChartZoomReducer, initialExecutionChartZoomState)
  const {
    expandedChart,
    onExpandedChartChange,
    expandedScenarioScript,
    onExpandedScenarioScriptChange,
  } = useExecutionViewState()

  const { queuedScenarioRows, scriptDetailsLoading, scriptDetailsError } = useExecutionScenarioPreviewModel(test)
  const { localMetricsHistory } = useExecutionMetricsStream(testId)
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
  
  // Memoize the mapped backend history to avoid re-mapping it on every WebSocket update
  const mappedBackendHistory = useMemo(() => {
    return mapExecutionHistory(test?.history)
  }, [test?.history])

  // Combine WebSocket history with backend history for a continuous timeline
  const chartData = useMemo(() => {
    return buildExecutionChartData({
      mappedBackendHistory,
      localMetricsHistory,
      fallbackHistory: history,
      status: test?.status,
    })
  }, [history, localMetricsHistory, mappedBackendHistory, test?.status])

  const filteredData = useMemo(() => {
    return filterExecutionChartData(chartData, chartZoomState.timeRange, chartZoomState.zoomRange)
  }, [chartData, chartZoomState.timeRange, chartZoomState.zoomRange])

  const summary = useMemo(() => {
    return summarizeExecutionChartData(filteredData)
  }, [filteredData])

  const { showRefArea, refAreaEnd } = getExecutionChartRefArea(chartZoomState)

  const queuedScenarioPreview = useMemo(() => {
    return buildQueuedScenarioPreview(test)
  }, [test])

  const metrics = test?.live_metrics
  const goalStatus = metrics?.goal_status
  const transactions = metrics?.transactions ? Object.values(metrics.transactions) : []
  const groupMetrics = metrics?.groups ? Object.values(metrics.groups) : []
  const isMultiGroup = (test?.groups?.length || 0) > 1
  const groupNames = test?.groups?.map((g) => g.name) || []
  const isGoalOriented = test?.scenario_mode === 'goal_oriented'
  const shakeoutFailed = test?.status === 'FAILED' && Boolean(test.script_shakeout) && test.error?.toLowerCase().includes('shakeout')

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

  return (
    <div className="execution">
      <TestExecutionHeader
        test={test}
        isMultiGroup={isMultiGroup}
        timeRange={chartZoomState.timeRange}
        onTimeRangeChange={(value) => dispatchChartZoom({ type: 'setTimeRange', value })}
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

      {/* Group config summary */}
      {isMultiGroup && (
        <div className="group-config-bar">
          {test.groups.map((g, i) => (
            <div key={g.name} className="group-chip" style={{ borderLeftColor: GROUP_COLORS[i % GROUP_COLORS.length] }}>
              <span className="group-chip-name">{g.name}</span>
              <span className="group-chip-detail">{g.script_name} · {g.vusers} VUsers</span>
            </div>
          ))}
        </div>
      )}

      {shakeoutFailed && (
        <div className="scenario-alert-banner" role="alert">
          <div>
            <h3 className="scenario-alert-title">Script shakeout failed</h3>
            <p className="scenario-alert-message">{test.error}</p>
          </div>
        </div>
      )}

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
        onChartMouseDown={(activeLabel) => dispatchChartZoom({ type: 'beginSelection', activeLabel })}
        onChartMouseMove={(activeLabel) => dispatchChartZoom({ type: 'updateSelection', activeLabel })}
        onZoom={() => dispatchChartZoom({ type: 'applySelection' })}
        showRefArea={showRefArea}
        refAreaLeft={chartZoomState.refAreaLeft}
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
