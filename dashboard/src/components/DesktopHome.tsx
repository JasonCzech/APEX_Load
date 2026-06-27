import type { DesktopStatusResponse } from '../types/library'
import type { RecentItem } from '../desktop/desktopRecents'
import './DesktopHome.css'

export type DesktopQuickAction = 'new-test' | 'import' | 'record' | 'scripts' | 'results'

interface DesktopHomeProps {
  status: DesktopStatusResponse | null
  startupError?: string
  recents: RecentItem[]
  onQuickAction: (action: DesktopQuickAction) => void
  onOpenRecent: (item: RecentItem) => void
  onClearRecents: () => void
  onReveal?: (which: 'logs' | 'appdata') => void
}

function storageLabel(status: DesktopStatusResponse | null): string {
  const type = (status?.storage as { type?: string } | undefined)?.type
  return type ? String(type) : 'unknown'
}

function remoteHealthLabel(status: DesktopStatusResponse | null): string {
  const count = status?.remote_libraries?.count || 0
  if (count === 0) return 'No remote workspaces'
  const healthy = status?.remote_libraries?.statuses?.healthy || 0
  return `${healthy}/${count} remote workspaces healthy`
}

const QUICK_ACTIONS: Array<{ id: DesktopQuickAction; label: string; hint: string }> = [
  { id: 'new-test', label: 'New Test', hint: 'Configure and queue a load test' },
  { id: 'import', label: 'Import', hint: 'Bring in a LoadRunner / JMeter / HAR script' },
  { id: 'record', label: 'Record', hint: 'Capture a browser session' },
  { id: 'scripts', label: 'Scripts', hint: 'Open the script library' },
  { id: 'results', label: 'Results', hint: 'Review past runs' },
]

// DesktopHome is the desktop-shell workspace hub: local engine health, storage
// and log locations, remote-workspace health, recent items, and quick actions.
// Presentational — all navigation/IO is delegated to the host via callbacks.
export default function DesktopHome({
  status,
  startupError,
  recents,
  onQuickAction,
  onOpenRecent,
  onClearRecents,
  onReveal,
}: DesktopHomeProps) {
  return (
    <section className="desktop-home" aria-label="Desktop workspace">
      {startupError && (
        <div className="desktop-home-error" role="alert">
          <strong>Local engine failed to start.</strong> {startupError}
        </div>
      )}

      <div className="desktop-home-quick">
        {QUICK_ACTIONS.map(action => (
          <button
            key={action.id}
            type="button"
            className="desktop-home-action"
            onClick={() => onQuickAction(action.id)}
            title={action.hint}
          >
            <span className="desktop-home-action-label">{action.label}</span>
            <span className="desktop-home-action-hint">{action.hint}</span>
          </button>
        ))}
      </div>

      <div className="desktop-home-grid">
        <div className="desktop-home-card">
          <h3>Local engine</h3>
          <dl>
            <dt>Status</dt>
            <dd>{startupError ? 'Unavailable' : (status?.desktop_mode ? 'Running' : 'Starting…')}</dd>
            <dt>API</dt>
            <dd>{status?.browser_url || status?.api_address || '—'}</dd>
            <dt>Storage</dt>
            <dd>{storageLabel(status)}</dd>
            <dt>Remote</dt>
            <dd>{remoteHealthLabel(status)}</dd>
          </dl>
          <div className="desktop-home-card-actions">
            {onReveal && status?.log_path && (
              <button type="button" className="btn btn-sm btn-ghost" onClick={() => onReveal('logs')}>Logs folder</button>
            )}
            {onReveal && status?.app_data_path && (
              <button type="button" className="btn btn-sm btn-ghost" onClick={() => onReveal('appdata')}>App data folder</button>
            )}
          </div>
        </div>

        <div className="desktop-home-card">
          <div className="desktop-home-card-head">
            <h3>Recent</h3>
            {recents.length > 0 && (
              <button type="button" className="btn btn-sm btn-ghost" onClick={onClearRecents}>Clear</button>
            )}
          </div>
          {recents.length === 0 ? (
            <p className="desktop-home-empty">Scripts, imports, and runs you open will appear here.</p>
          ) : (
            <ul className="desktop-home-recents">
              {recents.map(item => (
                <li key={`${item.kind}:${item.id}`}>
                  <button type="button" className="desktop-home-recent" onClick={() => onOpenRecent(item)}>
                    <span className="desktop-home-recent-name">{item.name || item.id}</span>
                    <span className="desktop-home-recent-kind">{item.kind}</span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </section>
  )
}
