import { useMemo } from 'react'

import { LibraryCatalogItem, LibraryKind } from '../types/library'
import {
  formatLibraryDateTime,
  libraryStatusTone,
  shortLibraryHash,
} from './libraryFederationUtils'
import LibraryFederationControls from './LibraryFederationControls'
import { useLibraryFederation, type LibraryAssetSourceKind } from './useLibraryFederation'

type LibraryAssetSourcePanelProps = {
  kind: LibraryKind
  projectId?: string
  onRefreshLocal?: () => void | Promise<void>
}

type LibraryAssetRow = {
  source: LibraryAssetSourceKind
  item: LibraryCatalogItem
  syncStatus: string
}

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

export default function LibraryAssetSourcePanel({ kind, projectId, onRefreshLocal }: LibraryAssetSourcePanelProps) {
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

  return (
    <div className="library-source-panel">
      <LibraryFederationControls kind={kind} federation={federation} />

      <div className="script-parameter-table-wrap">
        <table className="script-parameter-table">
          <thead>
            <tr>
              <th>Source</th>
              <th>Name</th>
              <th>Status</th>
              <th>Metadata</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {rows.map(({ source, item, syncStatus }) => {
              const hostItem = source === 'Local' ? hostItemForLocal(item.id) : item
              const href = source === 'Remote' ? remoteItemHref(item) : ''
              const localItemID = item.local_item_id
              const transferBlockedByConflict = syncStatus === 'conflict'
              const transferBlockTitle = transferBlockedByConflict ? 'Compare this conflict before transferring.' : actionBlockedReason
              return (
                <tr key={`${source}:${item.kind}:${item.id}`}>
                  <td>{source}</td>
                  <td>
                    <strong>{item.name || item.id}</strong>
                    <div className="script-table-meta">
                      <span className="script-health-token">{item.kind}</span>
                      {item.protocol && <span className="script-health-token">{item.protocol}</span>}
                      {item.has_archive && <span className="script-health-token healthy">archive</span>}
                    </div>
                  </td>
                  <td><span className={`script-health-token ${libraryStatusTone(syncStatus)}`}>{syncStatus}</span></td>
                  <td>
                    <div className="script-table-usage">
                      {itemMetadata(item).map(piece => <span key={piece}>{piece}</span>)}
                    </div>
                  </td>
                  <td>
                    <div className="settings-inline-actions">
                      {source === 'Local' ? (
                        <>
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
                          {href && <a className="btn btn-sm btn-ghost" href={href} target="_blank" rel="noreferrer">Open in Remote Library</a>}
                        </>
                      )}
                    </div>
                  </td>
                </tr>
              )
            })}
            {rows.length === 0 && (
              <tr>
                <td colSpan={5}>{loading ? 'Loading library catalog...' : `No ${KIND_LABELS[kind].toLowerCase()} match this source.`}</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
