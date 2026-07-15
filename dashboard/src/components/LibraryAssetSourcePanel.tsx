import { Fragment, useEffect, useMemo, useState } from 'react'

import { LibraryCatalogItem, LibraryKind } from '../types/library'
import {
  formatLibraryDateTime,
  libraryStatusTone,
  shortLibraryHash,
} from './libraryFederationUtils'
import LibraryFederationControls from './LibraryFederationControls'
import { SortIndicator } from './SortIndicator'
import { useLibraryFederation, type LibraryAssetSourceKind } from './useLibraryFederation'

type LibraryAssetSourcePanelProps = {
  kind: LibraryKind
  projectId?: string
  onConfigureRemotes?: () => void
  onDeleteLocalItem?: (id: string) => void | Promise<void>
  onRefreshLocal?: () => void | Promise<void>
  onViewLocalItem?: (id: string) => void
}

type LibraryAssetRow = {
  source: LibraryAssetSourceKind
  item: LibraryCatalogItem
  syncStatus: string
}

type LibraryAssetSortField = 'source' | 'name' | 'status' | 'release' | 'execution_run_date' | 'metadata'

const KIND_LABELS: Record<LibraryKind, string> = {
  script: 'Scripts',
  scenario: 'Scenarios',
  result: 'Results',
}

function itemMetadata(item: LibraryCatalogItem) {
  return [
    `Project ${item.project_id || 'unmapped'}`,
    `Hash ${shortLibraryHash(item.content_hash)}`,
    `Updated ${formatLibraryDateTime(item.updated_at)}`,
    item.dependencies?.length ? `${item.dependencies.length} dependencies` : '',
  ].filter(Boolean)
}

function executionRunDate(item: LibraryCatalogItem) {
  return item.execution_run_date || item.updated_at
}

function displayStatus(row: LibraryAssetRow) {
  if (row.item.kind === 'result') return row.item.status || 'Unknown'
  return row.syncStatus
}

function statusTone(status?: string) {
  switch ((status || '').toLowerCase()) {
    case 'complete':
    case 'completed':
    case 'success':
    case 'succeeded':
      return 'healthy'
    case 'failed':
    case 'aborted':
      return 'danger'
    default:
      return libraryStatusTone(status)
  }
}

function releaseLabel(item: LibraryCatalogItem) {
  return item.release_name || item.release_id || 'Unassigned'
}

function releaseFilterValue(item: LibraryCatalogItem) {
  return item.release_id || item.release_name || '__unassigned__'
}

function rowKey(row: LibraryAssetRow) {
  return `${row.source}:${row.item.kind}:${row.item.id}`
}

function dependencyLabel(item: LibraryCatalogItem) {
  if (!item.dependencies?.length) return 'No dependencies'
  return item.dependencies.map(dependency => dependency.name || dependency.id).join(', ')
}

function detailRows(row: LibraryAssetRow) {
  const { source, item, syncStatus } = row
  return [
    { label: 'Source', value: source },
    { label: 'Kind', value: item.kind },
    { label: 'Status', value: syncStatus },
    { label: 'Project', value: item.project_id || 'unmapped' },
    { label: 'Release', value: releaseLabel(item) },
    { label: 'Protocol', value: item.protocol || 'n/a' },
    { label: 'Hash', value: shortLibraryHash(item.content_hash) },
    { label: 'Updated', value: formatLibraryDateTime(item.updated_at) },
    { label: 'Local ID', value: item.local_item_id || (source === 'Local' ? item.id : 'n/a') },
    { label: 'Remote ID', value: item.remote_item_id || (source === 'Remote' ? item.id : 'n/a') },
    { label: 'Dependencies', value: dependencyLabel(item) },
  ]
}

function sortValue(row: LibraryAssetRow, field: LibraryAssetSortField): string | number {
  switch (field) {
    case 'source':
      return row.source
    case 'status':
      return displayStatus(row).toLowerCase()
    case 'release':
      return releaseLabel(row.item).toLowerCase()
    case 'execution_run_date':
      return Date.parse(executionRunDate(row.item) || '') || 0
    case 'metadata':
      return Date.parse(row.item.updated_at || '') || 0
    case 'name':
    default:
      return (row.item.name || row.item.id || '').toLowerCase()
  }
}

export default function LibraryAssetSourcePanel({
  kind,
  projectId,
  onConfigureRemotes,
  onDeleteLocalItem,
  onRefreshLocal,
  onViewLocalItem,
}: LibraryAssetSourcePanelProps) {
  const federation = useLibraryFederation({ kind, projectId, onRefreshLocal })
  const {
    sourceFilter,
    localItems,
    remoteItems,
    loading,
    actionBlocked,
    actionBlockedReason,
    pullRemoteItem,
    pushLocalItem,
    compareItems,
    hostItemForLocal,
    syncStatusForLocal,
    remoteItemHref,
  } = federation

  const localRows = useMemo<LibraryAssetRow[]>(() => (
    sourceFilter === 'host' ? [] : localItems.map(item => ({
      source: 'Local',
      item,
      syncStatus: syncStatusForLocal(item.id),
    }))
  ), [localItems, sourceFilter, syncStatusForLocal])

  const remoteRows = useMemo<LibraryAssetRow[]>(() => (
    sourceFilter === 'local' ? [] : remoteItems.map(item => ({
      source: 'Remote',
      item,
      syncStatus: item.sync_status || 'host only',
    }))
  ), [remoteItems, sourceFilter])

  const rows = useMemo(() => [...localRows, ...remoteRows], [localRows, remoteRows])
  const showReleaseControls = kind === 'result'
  const [releaseFilter, setReleaseFilter] = useState('all')
  const [sortField, setSortField] = useState<LibraryAssetSortField>(showReleaseControls ? 'execution_run_date' : 'metadata')
  const [sortDirection, setSortDirection] = useState<'asc' | 'desc'>('desc')
  const [selectedRowKey, setSelectedRowKey] = useState<string | null>(null)

  useEffect(() => {
    setReleaseFilter('all')
    setSortField(showReleaseControls ? 'execution_run_date' : 'metadata')
    setSortDirection('desc')
  }, [showReleaseControls])

  const releaseOptions = useMemo(() => {
    const options = new Map<string, string>()
    rows.forEach(row => {
      if (row.item.kind !== 'result') return
      options.set(releaseFilterValue(row.item), releaseLabel(row.item))
    })
    return Array.from(options.entries())
      .map(([value, label]) => ({ value, label }))
      .sort((left, right) => left.label.localeCompare(right.label))
  }, [rows])

  const visibleRows = useMemo(() => {
    const filtered = showReleaseControls && releaseFilter !== 'all'
      ? rows.filter(row => releaseFilterValue(row.item) === releaseFilter)
      : rows
    return [...filtered].sort((left, right) => {
      const leftValue = sortValue(left, sortField)
      const rightValue = sortValue(right, sortField)
      if (leftValue < rightValue) return sortDirection === 'asc' ? -1 : 1
      if (leftValue > rightValue) return sortDirection === 'asc' ? 1 : -1
      return 0
    })
  }, [releaseFilter, rows, showReleaseControls, sortDirection, sortField])

  const columnCount = showReleaseControls ? 7 : 5

  useEffect(() => {
    if (selectedRowKey && !visibleRows.some(row => rowKey(row) === selectedRowKey)) {
      setSelectedRowKey(null)
    }
  }, [selectedRowKey, visibleRows])

  const toggleRow = (key: string) => {
    setSelectedRowKey(current => (current === key ? null : key))
  }

  const handleSort = (field: LibraryAssetSortField) => {
    if (sortField === field) {
      setSortDirection(current => (current === 'asc' ? 'desc' : 'asc'))
      return
    }
    setSortField(field)
    setSortDirection(field === 'metadata' || field === 'execution_run_date' ? 'desc' : 'asc')
  }

  const ariaSort = (field: LibraryAssetSortField): 'ascending' | 'descending' | 'none' => (
    sortField === field ? (sortDirection === 'asc' ? 'ascending' : 'descending') : 'none'
  )

  return (
    <div className="library-source-panel">
      <LibraryFederationControls
        kind={kind}
        federation={federation}
        onConfigureRemotes={onConfigureRemotes}
        summaryText={showReleaseControls && (releaseFilter !== 'all' || visibleRows.length !== rows.length)
          ? `${visibleRows.length} matching result${visibleRows.length === 1 ? '' : 's'}`
          : undefined}
        filterControls={showReleaseControls ? (
          <label className="project-redaction-rule-field">
            <span>Release</span>
            <select
              className="settings-input library-source-select library-release-filter"
              value={releaseFilter}
              onChange={event => setReleaseFilter(event.target.value)}
            >
              <option value="all">All releases</option>
              {releaseOptions.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
            </select>
          </label>
        ) : undefined}
      />

      <div className="data-table library-source-table-wrap">
        <table className="library-source-table">
          <thead>
            <tr>
              <th scope="col" aria-sort={ariaSort('source')} className="sortable"><button type="button" onClick={() => handleSort('source')}>Source <SortIndicator active={sortField === 'source'} direction={sortDirection} /></button></th>
              <th scope="col" aria-sort={ariaSort('name')} className="sortable"><button type="button" onClick={() => handleSort('name')}>Name <SortIndicator active={sortField === 'name'} direction={sortDirection} /></button></th>
              <th scope="col" aria-sort={ariaSort('status')} className="sortable"><button type="button" onClick={() => handleSort('status')}>Status <SortIndicator active={sortField === 'status'} direction={sortDirection} /></button></th>
              {showReleaseControls && (
                <th scope="col" aria-sort={ariaSort('release')} className="sortable"><button type="button" onClick={() => handleSort('release')}>Release <SortIndicator active={sortField === 'release'} direction={sortDirection} /></button></th>
              )}
              {showReleaseControls && (
                <th scope="col" aria-sort={ariaSort('execution_run_date')} className="sortable"><button type="button" onClick={() => handleSort('execution_run_date')}>Execution Run Date <SortIndicator active={sortField === 'execution_run_date'} direction={sortDirection} /></button></th>
              )}
              <th scope="col" aria-sort={ariaSort('metadata')} className="sortable"><button type="button" onClick={() => handleSort('metadata')}>Metadata <SortIndicator active={sortField === 'metadata'} direction={sortDirection} /></button></th>
              <th scope="col">Actions</th>
            </tr>
          </thead>
          <tbody>
            {visibleRows.map((row) => {
              const { source, item, syncStatus } = row
              const key = rowKey(row)
              const selected = key === selectedRowKey
              const rowStatus = displayStatus(row)
              const canExpandRow = !showReleaseControls
              const hostItem = source === 'Local' ? hostItemForLocal(item.id) : item
              const href = source === 'Remote' ? remoteItemHref(item) : ''
              const localItemID = item.local_item_id
              const transferBlockedByConflict = syncStatus === 'conflict'
              const transferBlockTitle = transferBlockedByConflict ? 'Compare this conflict before transferring.' : actionBlockedReason
              return (
                <Fragment key={key}>
                  <tr
                    className={`${canExpandRow ? 'clickable-row' : 'no-hover'} ${selected ? 'selected' : ''}`}
                    tabIndex={canExpandRow ? 0 : undefined}
                    aria-selected={canExpandRow ? selected : undefined}
                    aria-expanded={canExpandRow ? selected : undefined}
                    onClick={canExpandRow ? () => toggleRow(key) : undefined}
                    onKeyDown={(event) => {
                      if (!canExpandRow) return
                      if (event.key !== 'Enter' && event.key !== ' ') return
                      event.preventDefault()
                      toggleRow(key)
                    }}
                  >
                    <td>
                      <span className="script-health-token">{source}</span>
                    </td>
                    <td className="cell-name">
                      <strong>{item.name || item.id}</strong>
                      <div className="script-table-meta">
                        {!showReleaseControls && <span className="script-health-token">{item.kind}</span>}
                        {!showReleaseControls && item.protocol && <span className="script-health-token">{item.protocol}</span>}
                        {!showReleaseControls && item.has_archive && <span className="script-health-token healthy">archive</span>}
                      </div>
                    </td>
                    <td><span className={`script-health-token ${statusTone(rowStatus)}`}>{rowStatus}</span></td>
                    {showReleaseControls && (
                      <td>
                        <div className="script-table-usage">
                          <strong>{releaseLabel(item)}</strong>
                          {item.release_id && <span className="cell-mono">{item.release_id}</span>}
                        </div>
                      </td>
                    )}
                    {showReleaseControls && <td className="cell-muted">{formatLibraryDateTime(executionRunDate(item))}</td>}
                    <td>
                      <div className="script-table-usage">
                        {itemMetadata(item).map(piece => <span key={piece}>{piece}</span>)}
                      </div>
                    </td>
                    <td style={{ textAlign: 'right' }}>
                      <div className="settings-inline-actions">
                        {source === 'Local' ? (
                          <>
                            {onViewLocalItem && (
                              <button className="btn btn-sm btn-ghost" type="button" onClick={() => onViewLocalItem(item.id)}>
                                View
                              </button>
                            )}
                            {onDeleteLocalItem && (
                              <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} type="button" onClick={() => void onDeleteLocalItem(item.id)}>
                                Delete
                              </button>
                            )}
                            <button className="btn btn-sm btn-secondary" type="button" onClick={() => void pushLocalItem(item)} disabled={actionBlocked || transferBlockedByConflict} title={transferBlockTitle}>
                              Push
                            </button>
                            {hostItem?.local_item_id && (
                              <button className="btn btn-sm btn-ghost" type="button" onClick={() => void compareItems(item.id, hostItem)}>
                                Compare
                              </button>
                            )}
                          </>
                        ) : (
                          <>
                            <button className="btn btn-sm btn-secondary" type="button" onClick={() => void pullRemoteItem(item)} disabled={actionBlocked || transferBlockedByConflict} title={transferBlockTitle}>
                              Pull
                            </button>
                            {localItemID && (
                              <button className="btn btn-sm btn-ghost" type="button" onClick={() => void compareItems(localItemID, item)}>
                                Compare
                              </button>
                            )}
                            {href && <a className="btn btn-sm btn-ghost" href={href} target="_blank" rel="noreferrer" onClick={event => event.stopPropagation()}>Open in Remote Workspace</a>}
                          </>
                        )}
                      </div>
                    </td>
                  </tr>
                  {canExpandRow && selected && (
                    <tr className="no-hover library-source-detail-row">
                  <td colSpan={columnCount} aria-label="Library asset details">
                        <div className="library-source-detail-card">
                          <div className="library-source-detail-header">
                            <strong>{item.name || item.id}</strong>
                            <span className={`script-health-token ${libraryStatusTone(syncStatus)}`}>{syncStatus}</span>
                          </div>
                          <div className="library-source-detail-grid">
                            {detailRows(row).map(detail => (
                              <div key={detail.label}>
                                <span>{detail.label}</span>
                                <strong>{detail.value}</strong>
                              </div>
                            ))}
                          </div>
                        </div>
                      </td>
                    </tr>
                  )}
                </Fragment>
              )
            })}
            {visibleRows.length === 0 && (
              <tr>
                <td colSpan={columnCount}>{loading ? 'Loading library catalog...' : `No ${KIND_LABELS[kind].toLowerCase()} match this source or release.`}</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
