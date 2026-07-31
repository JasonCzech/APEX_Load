import { formatThresholdAlertSummary, type ThresholdAlertEntry } from '../thresholdAlerts'

interface ThresholdAlertStripProps {
  alerts: ThresholdAlertEntry[]
  onAcknowledge?: (key: string) => void
}

const MAX_STRIP_ROWS = 3

// Compact live banner for global SLA threshold breaches on the run in view.
// Renders nothing when there are no alerts; acknowledged entries stay visible
// but subdued (the breach may still be active server-side).
export default function ThresholdAlertStrip({ alerts, onAcknowledge }: ThresholdAlertStripProps) {
  const activeAlerts = alerts.filter(alert => alert.active)
  if (activeAlerts.length === 0) return null
  const visible = activeAlerts.slice(0, MAX_STRIP_ROWS)
  const overflow = activeAlerts.length - visible.length

  return (
    <div className="monitor-warning-list threshold-alert-strip" role="region" aria-label="Active SLA threshold alerts">
      {visible.map(alert => (
        <div
          key={alert.key}
          className={`monitor-warning-row threshold-alert-strip-row ${alert.action === 'stop_requested' ? 'danger' : 'warning'} ${alert.acknowledged ? 'acknowledged' : ''}`}
        >
          <span className={`threshold-alert-strip-summary ${alert.action === 'stop_requested' ? 'text-danger' : ''}`}>
            {formatThresholdAlertSummary(alert)}
          </span>
          {onAcknowledge && !alert.acknowledged ? (
            <button type="button" className="btn btn-ghost btn-sm" onClick={() => onAcknowledge(alert.key)}>
              Ack
            </button>
          ) : null}
        </div>
      ))}
      {overflow > 0 && <p className="threshold-alert-strip-more">+{overflow} more threshold alert{overflow === 1 ? '' : 's'}</p>}
    </div>
  )
}
