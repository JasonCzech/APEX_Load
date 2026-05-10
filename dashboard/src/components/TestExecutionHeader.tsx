import StatusBadge from './StatusBadge'

import { TestSummary } from '../types'

type TimeRange = '3m' | '10m' | 'all' | 'custom'

interface Props {
  test: TestSummary
  isMultiGroup: boolean
  timeRange: TimeRange
  onTimeRangeChange: (value: TimeRange) => void
  onBack: () => void
  onOpenScenarioEditor: () => void
  onStart: () => void
  isRunning: boolean
  onStop: () => void
  onAbort: () => void
  hasArchive: boolean
  onDownloadArchive: () => void
}

export default function TestExecutionHeader({
  test,
  isMultiGroup,
  timeRange,
  onTimeRangeChange,
  onBack,
  onOpenScenarioEditor,
  onStart,
  isRunning,
  onStop,
  onAbort,
  hasArchive,
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
          <button className={`btn-chip ${timeRange === 'all' ? 'active' : ''}`} onClick={() => onTimeRangeChange('all')}>All</button>
          {timeRange === 'custom' && (
            <button className="btn-chip active zoom-reset" onClick={() => onTimeRangeChange('all')}>Reset Zoom</button>
          )}
        </div>
        {test.status === 'QUEUED' && (
          <>
            <button className="btn btn-secondary" onClick={onOpenScenarioEditor}>
              Edit Scenario
            </button>
            <button className="btn btn-success" onClick={onStart}>
              Start
            </button>
          </>
        )}
        {isRunning && (
          <>
            <button className="btn btn-secondary" onClick={onStop}>
              Stop
            </button>
            <button className="btn btn-danger" onClick={onAbort}>
              Abort
            </button>
          </>
        )}
        {!isRunning && hasArchive && (
          <button className="btn btn-secondary" onClick={onDownloadArchive}>
            Download Archive
          </button>
        )}
      </div>
    </div>
  )
}