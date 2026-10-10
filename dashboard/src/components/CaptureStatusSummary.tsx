import type { ELKCaptureStatus, DynatraceCaptureStatus } from '../types/monitoring'
import { formatDateTime } from '../utils/dateTime'

export function CaptureStatusSummary({ capture }: { capture: ELKCaptureStatus | DynatraceCaptureStatus }) {
  const labels: Record<string, string> = { pending: 'Waiting for first collection', connected: 'Connected', partial: 'Partial capture', unavailable: 'Capture unavailable' }
  return <div>
    <p role="status">{labels[capture.status] || capture.status}{capture.message ? ` — ${capture.message}` : ''}</p>
    <p>Last poll attempt: {capture.last_polled_at ? formatDateTime(capture.last_polled_at, { seconds: true }) : 'Not yet polled'}. Last fully successful collection: {capture.last_successful_at ? formatDateTime(capture.last_successful_at, { seconds: true }) : 'Not recorded'}.</p>
    {(capture.status === 'partial' || capture.status === 'unavailable') && <p>Some displayed values may be from an earlier successful collection. Polling retries automatically while the test runs; ask a project administrator to check connection permissions and diagnostics. Test execution is unaffected.</p>}
    {'capabilities' in capture && capture.capabilities && <ul aria-label="Capture capability diagnostics">{Object.entries(capture.capabilities).map(([name, health]) => <li key={name}>{name}: {health.status}{health.message ? ` — ${health.message}` : ''}</li>)}</ul>}
  </div>
}

export function CaptureMetricValues({ metrics }: { metrics?: Record<string, number> }) {
  const builtins: Record<string, string> = { logs_total: 'Log count', errors_total: 'Error count', 'logs.total': 'Log count', 'logs.errors': 'Error count', 'problems.total': 'Problem count', 'problems.open': 'Open problem count' }
  return metrics && <dl className="advanced-fact-grid">{Object.entries(metrics).map(([name, value]) => <div key={name}><dt title={name}>{builtins[name] || name.replace(/[._]/g, ' ')}</dt><dd>{value.toLocaleString(undefined, builtins[name] ? { maximumFractionDigits: 0 } : { maximumSignificantDigits: 8 })} <small>{builtins[name] ? 'count' : 'source units'}</small></dd></div>)}</dl>
}

export function CaptureEmptyEvidence({ status }: { status?: string }) {
  return <p>{status === 'pending' ? 'Waiting for capture evidence.' : status === 'connected' ? 'No matching evidence returned yet.' : 'No evidence available; collection may be incomplete.'}</p>
}
