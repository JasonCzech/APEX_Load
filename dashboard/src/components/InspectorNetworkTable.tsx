import { Fragment, useId, useMemo, useState } from 'react'
import type { BrowserReplayEvent } from '../types/replay'
import { browserResourceProtocolShortLabel } from './browserResourceEvidenceModel'
import InspectorHeaderTable from './InspectorHeaderTable'
import type { InspectorSourceContext } from './inspectorHeadersModel'
import {
  filterInspectorNetworkRows,
  inspectorNetworkBodySummary,
  inspectorNetworkFilters,
  inspectorNetworkKeptBody,
  inspectorNetworkPhases,
  inspectorNetworkRoleLabel,
  inspectorNetworkRows,
  INSPECTOR_NETWORK_TYPE_LABELS,
  type InspectorNetworkFilter,
  type InspectorNetworkRow,
} from './inspectorNetworkModel'
import { formatTimingMs } from './requestResponseInspectorModel'
import { formatReplayHeaders } from './scriptEditorUtils'
import SnapshotBodyPreview from './SnapshotBodyPreview'
import SnapshotTextPreview from './SnapshotTextPreview'
import './InspectorNetworkTable.css'

export interface InspectorNetworkTableProps {
  event: BrowserReplayEvent
  /** Hop phases are runtime evidence: withheld with it (as Timing does). */
  phasesUnavailable?: boolean
  /**
   * The Request tab's wire sources: the action's own request (and its
   * redirect hops) shows its headers through them, masked as there.
   */
  requestSources: InspectorSourceContext
  /** Prefix for copy keys, e.g. `${index}:network`. */
  copyKeyPrefix: string
  copiedKey: string | null
  onCopy: (key: string, value: string) => void | Promise<void>
}

type CopyProps = Pick<InspectorNetworkTableProps, 'copyKeyPrefix' | 'copiedKey' | 'onCopy'>

function statusText(row: InspectorNetworkRow): string {
  if (row.status !== null) return String(row.status)
  if (row.failureKind) return row.failureKind.replace(/_/g, ' ')
  return row.failed ? 'Failed' : 'Unknown'
}

function typeLabel(row: InspectorNetworkRow): string {
  const label = INSPECTOR_NETWORK_TYPE_LABELS[row.type]
  // The filter names a group; a row names one of it.
  return row.type === 'xhr' ? label : label.replace(/s$/, '').toLowerCase()
}

function NetworkRowDetail({ row, id, labelledBy, phasesUnavailable, requestSources, copyKeyPrefix, copiedKey, onCopy }: {
  row: InspectorNetworkRow
  id: string
  /** The row button's id: the detail is named by the request it shows. */
  labelledBy: string
  phasesUnavailable: boolean
  requestSources: InspectorSourceContext
} & CopyProps) {
  const phases = phasesUnavailable ? [] : inspectorNetworkPhases(row)
  const kept = inspectorNetworkKeptBody(row)
  const ownRequest = row.role === 'self' || row.role === 'page'
  const protocol = row.entry?.browser_evidence?.protocol ? browserResourceProtocolShortLabel(row.entry.browser_evidence) : ''
  const facts = [
    `${row.method} ${statusText(row)}`,
    row.durationMs !== null ? formatTimingMs(row.durationMs) : '',
    protocol,
    row.cacheHit ? 'from cache' : '',
    row.resource?.truncated ? 'headers or URL cut by retention' : '',
  ].filter(Boolean)
  const requestHeaders = row.resource?.request_headers || row.entry?.request_headers
  const responseHeaders = row.resource?.response_headers || row.entry?.response_headers
  return (
    <section id={id} className="rr-network-detail" aria-labelledby={labelledBy}>
      <h4 className="rr-network-detail-title"><code>{row.url || 'Unknown URL'}</code></h4>
      <p className="rr-network-detail-facts">{facts.join(' · ')}</p>
      {phases.length > 0 && (
        <dl className="rr-network-phases" aria-label="Timing phases">
          {phases.map((phase, index) => (
            <div key={`${phase.name}-${index}`}>
              <dt>{phase.label}</dt>
              <dd>{phase.durationMs !== null ? formatTimingMs(phase.durationMs) : `Unknown${phase.unavailableReason ? ` (${phase.unavailableReason.replace(/_/g, ' ')})` : ''}`}</dd>
            </div>
          ))}
        </dl>
      )}
      {requestHeaders && Object.keys(requestHeaders).length > 0 && (row.role !== null ? (
        // The action's own request (and a hop it was redirected through) carries
        // the authored headers: shown with the Request tab's sources and masks.
        <InspectorHeaderTable
          direction="request"
          headers={requestHeaders}
          sources={requestSources}
          label="Request headers of the selected request"
          copyKeyPrefix={`${copyKeyPrefix}:${row.id}:request-headers`}
          copiedKey={copiedKey}
          onCopy={onCopy}
        />
      ) : (
        <div className="replay-code-block">
          <span className="replay-detail-label">Request headers</span>
          <SnapshotTextPreview text={formatReplayHeaders(requestHeaders)} />
        </div>
      ))}
      {responseHeaders && Object.keys(responseHeaders).length > 0 && (
        <div className="replay-code-block">
          <span className="replay-detail-label">Response headers</span>
          <SnapshotTextPreview text={formatReplayHeaders(responseHeaders)} />
        </div>
      )}
      {ownRequest && !kept ? (
        <p className="rr-network-detail-note">This action&apos;s response body is on the Response tab.</p>
      ) : (
        <div className="replay-code-block">
          <span className="replay-detail-label">Response body · {row.body.label}</span>
          <SnapshotBodyPreview body={kept?.text} headers={kept?.headers} emptyMessage="No body kept for this request" />
        </div>
      )}
    </section>
  )
}

/** Every request the action made, in one table with filters; a selected row opens its detail right under it. */
export default function InspectorNetworkTable({ event, phasesUnavailable = false, requestSources, copyKeyPrefix, copiedKey, onCopy }: InspectorNetworkTableProps) {
  const baseId = useId()
  const rows = useMemo(() => inspectorNetworkRows(event), [event])
  const filters = useMemo(() => inspectorNetworkFilters(rows), [rows])
  // The filter and the selection belong to the event they were made on (row
  // ids are positions): another event opens on All with nothing selected.
  const [chosenFilter, setChosenFilter] = useState<{ event: BrowserReplayEvent; filter: InspectorNetworkFilter } | null>(null)
  const [selection, setSelection] = useState<{ event: BrowserReplayEvent; id: string } | null>(null)
  const selectedId = selection?.event === event ? selection.id : null
  const eventFilter = chosenFilter?.event === event ? chosenFilter.filter : 'all'
  // A filter the event no longer has falls back to All.
  const filter = filters.some(option => option.id === eventFilter) ? eventFilter : 'all'
  const visible = useMemo(() => filterInspectorNetworkRows(rows, filter), [rows, filter])
  const selected = visible.find(row => row.id === selectedId) || null
  const summary = inspectorNetworkBodySummary(rows, event.snapshot?.omitted_resources)
  const showHop = rows.some(row => row.hop !== null)
  const columns = showHop ? 6 : 5
  const detailId = `${baseId}-detail`
  if (rows.length === 0 && !summary) return null
  const shownText = filter === 'all' ? `${rows.length} ${rows.length === 1 ? 'request' : 'requests'}` : `${visible.length} of ${rows.length} requests shown`
  return (
    <section className="rr-network" aria-label="Network requests">
      <div className="rr-network-toolbar">
        {filters.length > 1 && (
          <div className="rr-inspector-segmented" role="group" aria-label="Network filter">
            {filters.map(option => (
              <button key={option.id} type="button" aria-pressed={filter === option.id} onClick={() => setChosenFilter({ event, filter: option.id })}>
                {option.label} <span className="rr-network-filter-count">{option.count}</span>
              </button>
            ))}
          </div>
        )}
        {summary && <span className="rr-network-summary">{summary}</span>}
      </div>
      {/* Announces what a filter left (the caption itself stays plain table semantics). */}
      {rows.length > 0 && <span className="sr-only" role="status">{filter === 'all' ? '' : shownText}</span>}
      {rows.length > 0 && (
        <div className="rr-network-table-wrap">
          <table className="rr-network-table">
            <caption className="rr-network-caption">{shownText}</caption>
            {/* Columns by class: a narrow pane collapses Hop and Type to no
                width (not display: none), so every row keeps one column count
                and the detail row's span stays exact. */}
            <colgroup>
              {showHop && <col className="rr-network-col-hop" />}
              <col className="rr-network-col-request" />
              <col className="rr-network-col-type" />
              <col className="rr-network-col-status" />
              <col className="rr-network-col-time" />
              <col className="rr-network-col-body" />
            </colgroup>
            <thead>
              <tr>
                {showHop && <th scope="col" className="rr-network-col-hop">Hop</th>}
                <th scope="col" className="rr-network-col-request">Request</th>
                <th scope="col" className="rr-network-col-type">Type</th>
                <th scope="col" className="rr-network-col-status">Status</th>
                <th scope="col" className="rr-network-col-time">Time</th>
                <th scope="col" className="rr-network-col-body">Body</th>
              </tr>
            </thead>
            <tbody>
              {visible.map(row => {
                const isSelected = row.id === selected?.id
                const role = inspectorNetworkRoleLabel(row.role)
                const status = statusText(row)
                const buttonId = `${baseId}-row-${row.id}`
                return (
                  <Fragment key={row.id}>
                    <tr className={`${isSelected ? 'is-selected' : ''}${row.role ? ' is-own' : ''}`.trim() || undefined}>
                      {showHop && <td className="rr-network-col-hop">{row.hop ?? <span className="rr-network-unknown">{'—'}</span>}</td>}
                      <th scope="row" className="rr-network-request">
                        <button
                          id={buttonId}
                          type="button"
                          className="rr-network-row-button"
                          title={row.url}
                          aria-expanded={isSelected}
                          aria-controls={isSelected ? detailId : undefined}
                          onClick={() => setSelection(isSelected ? null : { event, id: row.id })}
                        >
                          <span className="rr-network-method">{row.method}</span>{' '}
                          <span className="rr-network-path">{row.display || 'Unknown URL'}</span>
                          {role && <span className="rr-network-role"> ({role})</span>}
                        </button>
                      </th>
                      <td className="rr-network-col-type">{typeLabel(row)}</td>
                      <td className={row.failed ? 'rr-network-status is-failed' : 'rr-network-status'} title={status}>
                        {row.status === null && !row.failed ? <span className="rr-network-unknown">Unknown</span> : status}
                      </td>
                      <td>{row.durationMs !== null ? formatTimingMs(row.durationMs) : <span className="rr-network-unknown">Unknown</span>}</td>
                      <td className={row.body.kind === 'unknown' ? 'rr-network-unknown' : undefined} title={row.body.label}>{row.body.label}</td>
                    </tr>
                    {isSelected && (
                      <tr className="rr-network-detail-row">
                        {/* No column header applies to the detail (headers=""): it is named by its request. */}
                        <td colSpan={columns} headers="">
                          <NetworkRowDetail
                            row={row}
                            id={detailId}
                            labelledBy={buttonId}
                            phasesUnavailable={phasesUnavailable}
                            requestSources={requestSources}
                            copyKeyPrefix={copyKeyPrefix}
                            copiedKey={copiedKey}
                            onCopy={onCopy}
                          />
                        </td>
                      </tr>
                    )}
                  </Fragment>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}
