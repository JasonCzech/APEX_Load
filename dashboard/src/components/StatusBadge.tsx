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
  return (
    <span className={`status-badge ${STATUS_STYLES[normalizedStatus] || ''}`}>
      {testOutcomeStatus(status, stoppedByOperator) === 'STOPPED' ? 'Stopped by operator' : normalizedStatus || 'UNKNOWN'}
    </span>
  )
}
