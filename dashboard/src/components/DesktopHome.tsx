import { useRef } from 'react'

import type { DesktopStatusResponse } from '../types/library'
import type { RecentItem } from '../desktop/desktopRecents'
import type { TopbarRunSummary } from '../appTopbar'
import { isDesktopSurface } from '../desktop/desktopBridge'
import { filesFromDroppedPaths, nativeFileReadErrorMessage } from '../desktop/desktopFiles'
import { useDesktopFileDrop } from '../desktop/useDesktopFileDrop'
import { formatRelativeTime } from '../utils/relativeTime'
import './DesktopHome.css'

export type DesktopQuickAction = 'new-test' | 'import' | 'record' | 'scripts' | 'results'

interface DesktopHomeProps {
  status: DesktopStatusResponse | null
  startupError?: string
  recents: RecentItem[]
  runningTests?: TopbarRunSummary[]
  onQuickAction: (action: DesktopQuickAction) => void
  onOpenRecent: (item: RecentItem) => void
  onOpenRun?: (id: string) => void
  onTogglePin?: (item: RecentItem) => void
  onClearRecents: () => void
  onReveal?: (which: 'logs' | 'appdata') => void
  // Called when script/import files are dropped onto the home hub.
  onDropFiles?: (files: File[]) => void
  onDropError?: (message: string) => void
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
  runningTests = [],
  onQuickAction,
  onOpenRecent,
  onOpenRun,
  onTogglePin,
  onClearRecents,
  onReveal,
  onDropFiles,
  onDropError,
}: DesktopHomeProps) {
  // Pinned items first, preserving recency within each group.
  const orderedRecents = [...recents].sort((a, b) => Number(Boolean(b.pinned)) - Number(Boolean(a.pinned)))
  const dropRef = useRef<HTMLElement>(null)
  const desktop = isDesktopSurface()

  // Drop script/import files onto the hub → hand to the host (e.g. open import).
  useDesktopFileDrop(dropRef, paths => {
    if (!onDropFiles) return
    void filesFromDroppedPaths(paths).then(files => {
      if (files.length > 0) onDropFiles(files)
      else if (paths.length > 0) onDropError?.('The dropped selection did not contain a supported import file.')
    }).catch(error => onDropError?.(nativeFileReadErrorMessage(error)))
  }, desktop && typeof onDropFiles === 'function')

  return (
    <section
      ref={dropRef}
      className="desktop-home"
      aria-label="Desktop workspace"
      style={desktop && onDropFiles ? ({ '--wails-drop-target': 'drop' } as React.CSSProperties) : undefined}
    >
      {startupError && (
        <div className="desktop-home-error" role="alert">
          <strong>Local engine failed to start.</strong> {startupError}
        </div>
      )}

      {runningTests.length > 0 && (
        <div className="desktop-home-running" aria-label="Running tests">
          <h3>Running now</h3>
          <div className="desktop-home-running-cards">
            {runningTests.map(run => (
              <button
                key={run.id}
                type="button"
                className="desktop-home-run-card"
                onClick={() => onOpenRun?.(run.id)}
                disabled={!onOpenRun}
                title={`Open ${run.name || run.id}`}
              >
                <span className="desktop-home-run-name">{run.name || run.id}</span>
                <span className="desktop-home-run-status">{(run.status || '').replace(/_/g, ' ') || 'active'}</span>
              </button>
            ))}
          </div>
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
          {orderedRecents.length === 0 ? (
            <p className="desktop-home-empty">Scripts, imports, and runs you open will appear here.</p>
          ) : (
            <ul className="desktop-home-recents">
              {orderedRecents.map(item => (
                <li key={`${item.kind}:${item.id}`} className="desktop-home-recent-row">
                  <button type="button" className="desktop-home-recent" onClick={() => onOpenRecent(item)}>
                    <span className="desktop-home-recent-name">{item.name || item.id}</span>
                    <span className="desktop-home-recent-meta">
                      {formatRelativeTime(item.openedAt) && (
                        <span className="desktop-home-recent-when">{formatRelativeTime(item.openedAt)}</span>
                      )}
                      <span className="desktop-home-recent-kind">{item.kind}</span>
                    </span>
                  </button>
                  {onTogglePin && (
                    <button
                      type="button"
                      className={`desktop-home-pin ${item.pinned ? 'is-pinned' : ''}`}
                      aria-label={item.pinned ? `Unpin ${item.name || item.id}` : `Pin ${item.name || item.id}`}
                      aria-pressed={Boolean(item.pinned)}
                      title={item.pinned ? 'Unpin' : 'Pin'}
                      onClick={() => onTogglePin(item)}
                    >
                      {item.pinned ? '★' : '☆'}
                    </button>
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </section>
  )
}
