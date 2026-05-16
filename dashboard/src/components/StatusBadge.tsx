import './StatusBadge.css'

const STATUS_STYLES: Record<string, string> = {
  QUEUED: 'status-queued',
  PENDING: 'status-queued',
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
}

export default function StatusBadge({ status }: Props) {
  return (
    <span className={`status-badge ${STATUS_STYLES[status] || ''}`}>
      {status}
    </span>
  )
}
