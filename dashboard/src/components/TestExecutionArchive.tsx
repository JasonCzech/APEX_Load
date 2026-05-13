import { Fragment } from 'react'

import { RunArchiveActionStats, RunArchiveReport, TestSummary, TransactionEvent } from '../types'

interface Props {
  hasArchive: boolean
  test: TestSummary
  archiveError: string
  archiveLoading: boolean
  archiveReport: RunArchiveReport | null
  onDownloadArchiveArtifact: (artifactName: string) => void
  archiveActionEntries: Array<[string, RunArchiveActionStats]>
  selectedArchiveAction: string
  onSelectedArchiveActionChange: (value: string) => void
  archiveGroups: string[]
  selectedArchiveGroup: string
  onSelectedArchiveGroupChange: (value: string) => void
  archiveErrorsOnly: boolean
  onArchiveErrorsOnlyChange: (value: boolean) => void
  archiveTransactionsLoading: boolean
  archiveTransactionsTotal: number
  archivePageStart: number
  archivePageEnd: number
  archiveTransactionsOffset: number
  archivePageSize: number
  onPreviousPage: () => void
  onNextPage: () => void
  archiveTransactions: TransactionEvent[]
  expandedArchiveEvent: string | null
  onExpandedArchiveEventChange: (value: string | null) => void
}

function formatArchiveTimestamp(value?: string): string {
  if (!value) return 'n/a'
  return new Date(value).toLocaleString()
}

function formatHeaderPairs(headers?: Record<string, string>): string {
  if (!headers || Object.keys(headers).length === 0) return 'n/a'
  return Object.entries(headers)
    .map(([key, value]) => `${key}: ${value}`)
    .join('\n')
}

export default function TestExecutionArchive({
  hasArchive,
  test,
  archiveError,
  archiveLoading,
  archiveReport,
  onDownloadArchiveArtifact,
  archiveActionEntries,
  selectedArchiveAction,
  onSelectedArchiveActionChange,
  archiveGroups,
  selectedArchiveGroup,
  onSelectedArchiveGroupChange,
  archiveErrorsOnly,
  onArchiveErrorsOnlyChange,
  archiveTransactionsLoading,
  archiveTransactionsTotal,
  archivePageStart,
  archivePageEnd,
  archiveTransactionsOffset,
  archivePageSize,
  onPreviousPage,
  onNextPage,
  archiveTransactions,
  expandedArchiveEvent,
  onExpandedArchiveEventChange,
}: Props) {
  if (!hasArchive) {
    return null
  }

  return (
    <div className="archive-report-card">
      <div className="archive-report-header">
        <div>
          <h3 className="archive-report-title">Archive Report</h3>
          <p className="archive-report-subtitle">
            Post-run summary derived from the saved JSONL bundle. Completed {formatArchiveTimestamp(test.archive?.completed_at)}.
          </p>
        </div>
        <div className="archive-report-badges">
          <span className="archive-chip">{test.archive?.transaction_count || 0} transactions</span>
          <span className="archive-chip">{test.archive?.resource_sample_count || 0} resource samples</span>
          <span className="archive-chip">{test.archive?.interval_summary_count || 0} summaries</span>
        </div>
      </div>

      {archiveError && <p className="scenario-script-status error">{archiveError}</p>}
      {archiveLoading && <p className="scenario-script-status">Loading archive report…</p>}

      {archiveReport && (
        <>
          <div className="archive-overview-grid">
            <div className="scenario-script-detail-card">
              <span className="scenario-script-detail-label">Peak TPS</span>
              <span className="scenario-script-detail-value">{archiveReport.overview.peak_tps.toFixed(1)}</span>
            </div>
            <div className="scenario-script-detail-card">
              <span className="scenario-script-detail-label">Peak Active VUsers</span>
              <span className="scenario-script-detail-value">{archiveReport.overview.peak_active_vusers}</span>
            </div>
            <div className="scenario-script-detail-card">
              <span className="scenario-script-detail-label">Response Range</span>
              <span className="scenario-script-detail-value">{archiveReport.overview.min_response_ms.toFixed(1)} - {archiveReport.overview.max_response_ms.toFixed(1)} ms</span>
            </div>
            <div className="scenario-script-detail-card">
              <span className="scenario-script-detail-label">CPU Peak</span>
              <span className="scenario-script-detail-value">{archiveReport.resource_peaks.cpu_process_pct.toFixed(1)}%</span>
            </div>
            <div className="scenario-script-detail-card">
              <span className="scenario-script-detail-label">Memory Peak</span>
              <span className="scenario-script-detail-value">{archiveReport.resource_peaks.mem_process_mb.toFixed(1)} MB</span>
            </div>
            <div className="scenario-script-detail-card">
              <span className="scenario-script-detail-label">Network Peak</span>
              <span className="scenario-script-detail-value">{archiveReport.resource_peaks.net_sent_kbps.toFixed(1)} / {archiveReport.resource_peaks.net_recv_kbps.toFixed(1)} KB/s</span>
            </div>
          </div>

          <div className="archive-artifact-row">
            <div className="archive-artifact-group">
              <span className="archive-artifact-label">JSONL</span>
              <button className="btn btn-secondary btn-sm" onClick={() => onDownloadArchiveArtifact(archiveReport.manifest.transactions_file || 'transactions.jsonl')}>
                Download Transactions JSONL
              </button>
              <button className="btn btn-secondary btn-sm" onClick={() => onDownloadArchiveArtifact(archiveReport.manifest.resource_samples_file || 'resources.jsonl')}>
                Download Resources JSONL
              </button>
              <button className="btn btn-secondary btn-sm" onClick={() => onDownloadArchiveArtifact(archiveReport.manifest.interval_summaries_file || 'interval_summaries.jsonl')}>
                Download Summaries JSONL
              </button>
            </div>
            <div className="archive-artifact-group">
              <span className="archive-artifact-label">Parquet</span>
              <button className="btn btn-secondary btn-sm" onClick={() => onDownloadArchiveArtifact(archiveReport.manifest.transactions_parquet_file || 'transactions.parquet')}>
                Download Transactions Parquet
              </button>
              <button className="btn btn-secondary btn-sm" onClick={() => onDownloadArchiveArtifact(archiveReport.manifest.resource_samples_parquet_file || 'resources.parquet')}>
                Download Resources Parquet
              </button>
              <button className="btn btn-secondary btn-sm" onClick={() => onDownloadArchiveArtifact(archiveReport.manifest.interval_summaries_parquet_file || 'interval_summaries.parquet')}>
                Download Summaries Parquet
              </button>
            </div>
          </div>

          <div className="archive-report-layout">
            <div className="tx-section archive-action-section">
              <div className="archive-section-header">
                <div>
                  <h3 className="chart-title">Action Summary</h3>
                  <p className="archive-section-copy">Archived totals by transaction name.</p>
                </div>
              </div>
              <table className="tx-table">
                <thead>
                  <tr>
                    <th>Action</th>
                    <th>Count</th>
                    <th>Errors</th>
                    <th>Error %</th>
                    <th>Avg (ms)</th>
                    <th>Max (ms)</th>
                  </tr>
                </thead>
                <tbody>
                  {archiveActionEntries.slice(0, 8).map(([name, stats]) => (
                    <tr key={name}>
                      <td className="tx-name">{name}</td>
                      <td>{stats.transactions}</td>
                      <td className={stats.errors > 0 ? 'error-cell' : ''}>{stats.errors}</td>
                      <td>{stats.error_pct.toFixed(2)}%</td>
                      <td>{stats.avg_response_ms.toFixed(1)}</td>
                      <td>{stats.max_response_ms.toFixed(1)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            <div className="tx-section archive-drilldown-section">
              <div className="archive-section-header archive-section-header-stack">
                <div>
                  <h3 className="chart-title">Transaction Drill-Down</h3>
                  <p className="archive-section-copy">Sample archived events for a selected action.</p>
                </div>
                <div className="archive-filter-bar">
                  <select
                    className="archive-action-select"
                    value={selectedArchiveAction}
                    onChange={(event) => onSelectedArchiveActionChange(event.target.value)}
                  >
                    {archiveActionEntries.map(([name]) => (
                      <option key={name} value={name}>{name}</option>
                    ))}
                  </select>
                  <select
                    className="archive-action-select archive-filter-select"
                    value={selectedArchiveGroup}
                    onChange={(event) => onSelectedArchiveGroupChange(event.target.value)}
                  >
                    <option value="">All groups</option>
                    {archiveGroups.map((group) => (
                      <option key={group} value={group}>{group}</option>
                    ))}
                  </select>
                  <label className="archive-checkbox">
                    <input
                      type="checkbox"
                      checked={archiveErrorsOnly}
                      onChange={(event) => onArchiveErrorsOnlyChange(event.target.checked)}
                    />
                    Errors only
                  </label>
                </div>
              </div>
              <div className="archive-pagination-bar">
                <span className="archive-pagination-copy">
                  {archiveTransactionsLoading ? 'Loading events…' : archiveTransactionsTotal > 0 ? `${archivePageStart}-${archivePageEnd} of ${archiveTransactionsTotal}` : 'No archived events'}
                </span>
                <div className="archive-pagination-actions">
                  <button
                    className="btn btn-sm btn-secondary"
                    disabled={archiveTransactionsOffset === 0 || archiveTransactionsLoading}
                    onClick={onPreviousPage}
                  >
                    Prev
                  </button>
                  <button
                    className="btn btn-sm btn-secondary"
                    disabled={archiveTransactionsOffset + archivePageSize >= archiveTransactionsTotal || archiveTransactionsLoading}
                    onClick={onNextPage}
                  >
                    Next
                  </button>
                </div>
              </div>
              {!archiveTransactionsLoading && archiveTransactions.length === 0 ? (
                <p className="scenario-script-status">No archived transactions loaded for this action.</p>
              ) : (
                <table className="tx-table archive-events-table">
                  <thead>
                    <tr>
                      <th>Time</th>
                      <th>Group</th>
                      <th>Status</th>
                      <th>Duration</th>
                      <th>Bytes</th>
                      <th>Error</th>
                      <th>Details</th>
                    </tr>
                  </thead>
                  <tbody>
                    {archiveTransactions.map((event, index) => {
                      const rowKey = `${event.timestamp}-${index}`
                      const hasSnapshot = Boolean(event.snapshot)
                      const isExpanded = expandedArchiveEvent === rowKey

                      return (
                        <Fragment key={rowKey}>
                          <tr>
                            <td>{formatArchiveTimestamp(event.timestamp)}</td>
                            <td>{event.group || 'n/a'}</td>
                            <td className={event.success ? '' : 'error-cell'}>{event.status_code || (event.success ? 'OK' : 'ERR')}</td>
                            <td>{event.duration_ms.toFixed(1)} ms</td>
                            <td>{event.bytes_received || 0}</td>
                            <td className={!event.success ? 'error-cell' : ''}>{event.error_message || 'None'}</td>
                            <td>
                              {hasSnapshot ? (
                                <button
                                  className="btn btn-sm btn-secondary archive-detail-toggle"
                                  onClick={() => onExpandedArchiveEventChange(isExpanded ? null : rowKey)}
                                >
                                  {isExpanded ? 'Hide' : 'Inspect'}
                                </button>
                              ) : (
                                <span className="cell-muted">n/a</span>
                              )}
                            </td>
                          </tr>
                          {isExpanded && hasSnapshot && (
                            <tr className="archive-event-detail-row">
                              <td colSpan={7}>
                                <div className="archive-event-detail-grid">
                                  <div className="archive-event-detail-card">
                                    <span className="scenario-script-detail-label">Request</span>
                                    <span className="scenario-script-detail-value">{event.snapshot?.request_method || 'n/a'} {event.snapshot?.request_url || ''}</span>
                                    <pre className="archive-event-detail-pre">{formatHeaderPairs(event.snapshot?.request_headers)}</pre>
                                    <pre className="archive-event-detail-pre">{event.snapshot?.request_body || 'No request body captured'}</pre>
                                  </div>
                                  <div className="archive-event-detail-card">
                                    <span className="scenario-script-detail-label">Response</span>
                                    <span className="scenario-script-detail-value">{event.snapshot?.response_status || event.status_code || 'n/a'}</span>
                                    <pre className="archive-event-detail-pre">{formatHeaderPairs(event.snapshot?.response_headers)}</pre>
                                    <pre className="archive-event-detail-pre">{event.snapshot?.response_body || 'No response body captured'}</pre>
                                  </div>
                                </div>
                              </td>
                            </tr>
                          )}
                        </Fragment>
                      )
                    })}
                  </tbody>
                </table>
              )}
            </div>
          </div>
        </>
      )}
    </div>
  )
}