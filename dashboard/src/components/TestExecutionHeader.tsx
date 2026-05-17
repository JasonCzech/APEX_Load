import TestExecutionQueuedRunActions from './TestExecutionQueuedRunActions'
import { ExecutionChartTimeRange } from './testExecutionChartZoomModel'
import StatusBadge from './StatusBadge'

import { TestSummary } from '../types'

interface Props {
  test: TestSummary
  isMultiGroup: boolean
  timeRange: ExecutionChartTimeRange
  onTimeRangeChange: (value: ExecutionChartTimeRange) => void
  onBack: () => void
  onStart: () => void
  onSelectTest: (testId: string) => void
  isRunning: boolean
  isPaused: boolean
  isStopping: boolean
  onStop: () => void
  onAbort: () => void
  onPause: () => void
  onResume: () => void
  showArchiveDownload: boolean
  onDownloadArchive: () => void
}

export default function TestExecutionHeader({
  test,
  isMultiGroup,
  timeRange,
  onTimeRangeChange,
  onBack,
  onStart,
  onSelectTest,
  isRunning,
  isPaused,
  isStopping,
  onStop,
  onAbort,
  onPause,
  onResume,
  showArchiveDownload,
  onDownloadArchive,
}: Props) {
  return (
    <div className="exec-header">
      <button className="btn btn-secondary" onClick={onBack}>Back</button>
      <div className="exec-title-area">
        <h2>{test.name}</h2>
        <div className="exec-meta">
          <StatusBadge status={test.status} />
          <span className="meta-item">{test.total_vusers} VUsers</span>
          <span className="meta-item">{test.duration}</span>
          {isMultiGroup && (
            <span className="meta-item group-count">{test.groups.length} groups</span>
          )}
        </div>
      </div>
      <div className="exec-actions">
        <div className="time-range-selector">
          <button className={`btn-chip ${timeRange === '3m' ? 'active' : ''}`} onClick={() => onTimeRangeChange('3m')}>3m</button>
          <button className={`btn-chip ${timeRange === '10m' ? 'active' : ''}`} onClick={() => onTimeRangeChange('10m')}>10m</button>
          <button className={`btn-chip ${timeRange === 'custom' ? 'active' : ''}`} onClick={() => onTimeRangeChange('custom')}>Custom</button>
          <button className={`btn-chip ${timeRange === 'all' ? 'active' : ''}`} onClick={() => onTimeRangeChange('all')}>Max</button>
        </div>
        <TestExecutionQueuedRunActions
          test={test}
          onStart={onStart}
          onSelectTest={onSelectTest}
        />
        {isRunning && (
          <>
            <button className="btn btn-secondary" onClick={onPause}>
              Pause
            </button>
            <button className="btn btn-secondary" onClick={onStop}>
              Stop
            </button>
            <button className="btn btn-danger" onClick={onAbort}>
              Abort
            </button>
          </>
        )}
        {isPaused && (
          <>
            <button className="btn btn-success" onClick={onResume}>
              Resume
            </button>
            <button className="btn btn-secondary" onClick={onStop}>
              Stop
            </button>
            <button className="btn btn-danger" onClick={onAbort}>
              Abort
            </button>
          </>
        )}
        {showArchiveDownload && (
          <button className="btn btn-secondary" onClick={onDownloadArchive}>
            Download Archive
          </button>
        )}
        {isStopping && (
          <span className="meta-item">Stopping…</span>
        )}
      </div>
    </div>
  )
}