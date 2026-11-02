import './StatusBadge.css'
import { normalizeTestStatus, testOutcomeStatus } from '../utils/testStatusUtils'

const STATUS_STYLES: Record<string, string> = {
  QUEUED: 'status-queued',
  RESERVED: 'status-queued',
  PENDING: 'status-queued',
  SHAKEOUT: 'status-init',
  SHAKEOUT_REVIEW: 'status-paused',
  INITIALIZING: 'status-init',
  RUNNING: 'status-running',
  STOPPING: 'status-stopping',
  COMPLETE: 'status-complete',
  PASSED: 'status-complete',
  PASS: 'status-complete',
  FINISHED: 'status-complete',
  SUCCEEDED: 'status-complete',
  SUCCESS: 'status-complete',
  READY: 'status-complete',
  HEALTHY: 'status-complete',
  STOPPED: 'status-stopped',
  FAILED: 'status-failed',
  ABORTED: 'status-aborted',
  PAUSED: 'status-paused',
}

interface Props {
  status: string
  stoppedByOperator?: boolean
}

export default function StatusBadge({ status, stoppedByOperator }: Props) {
  const normalizedStatus = normalizeTestStatus(status)
  const outcomeStatus = testOutcomeStatus(status, stoppedByOperator)
  return (
    <span className={`status-badge ${STATUS_STYLES[outcomeStatus] || STATUS_STYLES[normalizedStatus] || ''}`}>
      <span className="status-badge-label">
        {outcomeStatus === 'STOPPED' ? 'Stopped by operator' : normalizedStatus || 'UNKNOWN'}
      </span>
    </span>
  )
}
