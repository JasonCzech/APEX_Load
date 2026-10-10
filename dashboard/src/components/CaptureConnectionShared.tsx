import { settingsHref } from './settingsNavigation'
import './CaptureConnections.css'
import { formatDateTime } from '../utils/dateTime'

export function CaptureSetupHelp({ missing, credentials, editing }: { missing: string[]; credentials: string[]; editing: boolean }) {
  return <aside className="capture-setup-help">
    <p>Save a draft with just a name; add connectivity details later. Only enabled, complete connections are selectable for new tests.</p>
    <p><strong>{missing.length ? 'Setup incomplete' : 'Required details supplied — save, then test the connection'}</strong></p>
    {missing.length > 0 && <ul aria-label="Missing connection requirements">{missing.map(item => <li key={item}>{item}</li>)}</ul>}
    <p>Credentials: enter a project-secret reference, not the token or password. Values and reference names are never shown after saving. {editing && 'Leave replacement fields blank to keep existing credentials.'}</p>
    {credentials.length > 0 && <p>Configured credentials: {credentials.join(', ')}.</p>}
    <a href={settingsHref('secrets')}>Manage project secrets</a>
  </aside>
}

export function CaptureCorrelationHelp() {
  return <p className="capture-help">Correlation is optional. A test-ID field restricts results to this run; trace correlation uses observed trace IDs. Without usable correlation, capture uses the run time window and may include unrelated matching logs. Field names must match your source; leave unavailable mappings blank. Samples are bounded and redacted, not a complete log export.</p>
}

export function CaptureConnectionHealth({ enabled, missing, checkedAt, updatedAt, status, message, capabilities }: {
  enabled: boolean; missing: string[]; checkedAt?: string; updatedAt?: string; status?: string; message?: string;
  capabilities?: Record<string, { status: string; message?: string }>
}) {
  return <div className="capture-connection-health">
    <p>Configuration: {missing.length ? 'Incomplete draft' : 'Complete'} · New tests: {enabled ? 'Enabled' : 'Disabled'}</p>
    <p>Last check: {checkedAt ? `${status || 'Unknown'} · ${formatDateTime(checkedAt, { seconds: true })}` : 'Not tested'}</p>
    {checkedAt && updatedAt && Date.parse(updatedAt) > Date.parse(checkedAt) && <p>Configuration changed since this check. Test saved connection again to verify the current settings.</p>}
    {message && <p>{message}</p>}
    {capabilities && <ul>{Object.entries(capabilities).map(([name, health]) => <li key={name}>{name}: {health.status}{health.message ? ` — ${health.message}` : ''}</li>)}</ul>}
  </div>
}
