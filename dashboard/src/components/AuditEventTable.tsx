import './Access.css'
import type { ReactNode } from 'react'
import type { DashboardAuditEvent } from '../types'
import { detailsSummary, eventResourceLabel, formatTimestamp } from './accessUtils'

/** Shared evidence presentation. Callers retain their authorized query/filter API. */
export function AuditEventTable({ events, header, scopeLabel, onSelect, selectedId, emptyMessage = 'No activity found', paginated = false }: {
  paginated?: boolean
  events: DashboardAuditEvent[]
  header?: ReactNode
  scopeLabel?: (event: DashboardAuditEvent) => string
  onSelect?: (event: DashboardAuditEvent) => void
  selectedId?: string
  emptyMessage?: string
}) {
  return <div className="access-table-wrap" data-table-pagination={paginated ? 'off' : undefined}>
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
  </div>
}
