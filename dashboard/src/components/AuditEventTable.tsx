import './Access.css'
import { useEffect, useRef, type ReactNode } from 'react'
import type { DashboardAuditEvent } from '../types'
import { detailsSummary, eventResourceLabel, formatTimestamp } from './accessUtils'

/** Shared evidence presentation. Callers retain their authorized query/filter API. */
export function AuditEventTable({ events, header, footer, scopeLabel, onSelect, selectedId, emptyMessage = 'No activity found', paginated = false }: {
  paginated?: boolean
  events: DashboardAuditEvent[]
  header?: ReactNode
  /** Rendered at the end of the scrolling box, after the last row (e.g. LoadOlderEvents). */
  footer?: ReactNode
  scopeLabel?: (event: DashboardAuditEvent) => string
  onSelect?: (event: DashboardAuditEvent) => void
  selectedId?: string
  emptyMessage?: string
}) {
  // A server-batched list scrolls inside its box, so the box must be reachable by keyboard.
  return <div className="access-table-wrap" data-table-pagination={paginated ? 'off' : undefined} {...(paginated ? { tabIndex: 0, role: 'region', 'aria-label': 'Audit events' } : {})}>
    <table className="access-data-table">
      <caption className="sr-only">Audit events</caption>
      <thead><tr>{header || ['Time', 'Actor', 'Action', 'Resource', 'Scope', 'Status', 'Details'].map(label => <th key={label} scope="col">{label}</th>)}</tr></thead>
      <tbody>{events.map(event => <tr key={event.id} className={selectedId === event.id ? 'selected' : ''} onClick={onSelect ? () => onSelect(event) : undefined}>
        <td>{formatTimestamp(event.created_at)}</td>
        <td>{onSelect ? <button type="button" className="access-row-primary" onClick={click => { click.stopPropagation(); onSelect(event) }}>{event.actor_display_name || event.actor_subject || event.actor_type}</button> : event.actor_display_name || event.actor_subject || event.actor_type}<span className="access-row-meta">{event.actor_type}</span></td>
        <td>{event.action}</td><td>{eventResourceLabel(event)}</td><td>{scopeLabel?.(event) || event.project_id || 'Deployment'}</td>
        <td><span className={`access-status-badge ${event.status === 'success' ? 'healthy' : 'warning'}`}>{event.status}</span></td>
        <td>{onSelect ? <span className="access-table-stack">{detailsSummary(event.details)}</span> : <details><summary>Event details</summary><pre>{JSON.stringify(event.details || {}, null, 2)}</pre></details>}</td>
      </tr>)}</tbody>
    </table>
    {!events.length && <div className="access-empty-state"><strong>{emptyMessage}</strong><span>Adjust filters or select a broader timeframe.</span></div>}
    {footer}
  </div>
}

/**
 * End of a server-batched event list. Older events are appended to the same
 * scrolling table, so scrolling is the only way through the history; a failed
 * load leaves the rows in place and the button retries the same batch.
 */
export function LoadOlderEvents({ loaded, hasMore, loading, error, onLoad }: {
  loaded: number
  hasMore: boolean
  loading: boolean
  error?: string
  onLoad: () => void
}) {
  const buttonRef = useRef<HTMLButtonElement>(null)
  const endRef = useRef<HTMLSpanElement>(null)
  const hadFocusRef = useRef(false)
  // Keep keyboard focus through a load: the button stays focusable while busy
  // (aria-disabled, not disabled), and when the last batch arrives focus moves
  // to the "all loaded" line that replaces it.
  useEffect(() => {
    if (!hasMore && hadFocusRef.current) {
      hadFocusRef.current = false
      endRef.current?.focus()
    }
  }, [hasMore])
  // A batch that leaves more to load ends this click's focus hand-off, so a
  // later unrelated reload never moves focus.
  useEffect(() => {
    if (!loading && hasMore) hadFocusRef.current = false
  }, [hasMore, loading])
  if (!loaded) return null
  return <div className="access-load-older">
    {error && <span role="alert">{error}</span>}
    {hasMore
      ? <button ref={buttonRef} type="button" className="btn btn-secondary btn-sm" aria-disabled={loading || undefined} onClick={() => {
        if (loading) return
        hadFocusRef.current = document.activeElement === buttonRef.current
        onLoad()
      }}>{loading ? 'Loading older events…' : 'Load older events'}</button>
      : <span ref={endRef} tabIndex={-1} role="status">All {loaded} matching event{loaded === 1 ? '' : 's'} loaded</span>}
  </div>
}
