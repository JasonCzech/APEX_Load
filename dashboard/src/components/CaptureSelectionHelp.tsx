import { settingsHref } from './settingsNavigation'

export default function CaptureSelectionHelp({ provider, count, error, loading, retry, selected, capabilities, canManage = false, forbidden = false }: {
  provider: 'elk' | 'dynatrace'; count: number; error: boolean; loading: boolean; retry: () => void; selected?: string; capabilities?: string; canManage?: boolean
  /** The role cannot read connections: not a transient failure, so no retry. */
  forbidden?: boolean
}) {
  if (loading) return <p role="status">Loading capture connections…</p>
  if (forbidden) return <p>Your role cannot use capture connections, so capture stays off for this run.</p>
  if (error) return <p role="alert">Capture connections could not be loaded. Check the selection before launching. <button type="button" className="btn btn-secondary btn-sm" onClick={retry}>Retry connections</button></p>
  if (!count) return <p>No ready connections in this project. {canManage ? <a href={settingsHref(`${provider}-capture`)}>Configure connection</a> : 'Contact a project administrator to configure and enable a connection.'}</p>
  if (!selected) return null
  return <p>Capture selected: {selected} · {provider === 'elk' ? 'Log/error counts and configured numeric aggregations' : capabilities || 'Configured Dynatrace capabilities'}. The saved definition is snapshotted at launch. Correlation uses configured test/trace fields; time-window fallback may include unrelated logs. Evidence is bounded and redacted, not a full log export.</p>
}
