import { useCallback, useEffect, useMemo, useState } from 'react'

import { useLibraryApi } from '../hooks/api/useLibraryApi'
import {
  LibraryCatalogItem,
  LibraryKind,
  LibraryRemote,
  LibrarySourceFilter,
} from '../types/library'
import { getErrorMessage } from '../utils/errorUtils'
import {
  formatLibraryDateTime,
  hostLibraryHref,
  libraryStatusTone,
  libraryTransferSummary,
  remoteCredentialReady,
  shortLibraryHash,
} from './libraryFederationUtils'

type LibraryAssetSourcePanelProps = {
  kind: LibraryKind
  projectId?: string
  onRefreshLocal?: () => void | Promise<void>
}

type LibraryAssetRow = {
  source: 'Local' | 'K8s Host'
  item: LibraryCatalogItem
  syncStatus: string
}

const SOURCE_OPTIONS: Array<{ value: LibrarySourceFilter; label: string }> = [
  { value: 'all', label: 'All' },
  { value: 'local', label: 'Local' },
  { value: 'host', label: 'K8s Host' },
]

const KIND_LABELS: Record<LibraryKind, string> = {
  script: 'Scripts',
  scenario: 'Scenarios',
  result: 'Results',
}

function sourceDescription(kind: LibraryKind) {
  if (kind === 'result') return 'Move completed result archives between this controller and a hosted team library.'
  if (kind === 'scenario') return 'Pull or promote reusable scenario definitions and referenced scripts.'
  return 'Pull or promote authored scripts between local and hosted libraries.'
}

function transferProjectMessage(projectMapping: string, remote?: LibraryRemote) {
  if (projectMapping.trim() || remote?.default_project_id) return ''
  return 'Set a project mapping before transferring assets.'
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
  const libraryApi = useLibraryApi()
  const [remotes, setRemotes] = useState<LibraryRemote[]>([])
  const [selectedRemoteId, setSelectedRemoteId] = useState('')
  const [sourceFilter, setSourceFilter] = useState<LibrarySourceFilter>('all')
  const [projectMapping, setProjectMapping] = useState(projectId || '')
  const [includeDependencies, setIncludeDependencies] = useState(true)
  const [localItems, setLocalItems] = useState<LibraryCatalogItem[]>([])
  const [remoteItems, setRemoteItems] = useState<LibraryCatalogItem[]>([])
  const [loading, setLoading] = useState(false)
  const [actionMessage, setActionMessage] = useState('')
  const [actionError, setActionError] = useState('')
  const [diffMessage, setDiffMessage] = useState('')

  const selectedRemote = useMemo(
    () => remotes.find(remote => remote.id === selectedRemoteId),
    [remotes, selectedRemoteId],
  )

  const effectiveProjectID = projectMapping.trim() || selectedRemote?.default_project_id || ''
  const projectBlockMessage = transferProjectMessage(projectMapping, selectedRemote)
  const credentialsReady = remoteCredentialReady(selectedRemote)

  const loadRemotes = useCallback(async () => {
    const response = await libraryApi.listRemotes()
    const nextRemotes = response.remotes || []
    setRemotes(nextRemotes)
    setSelectedRemoteId(current => {
      if (current && nextRemotes.some(remote => remote.id === current)) return current
      return nextRemotes[0]?.id || ''
    })
  }, [libraryApi])

  const loadLocalCatalog = useCallback(async () => {
    const response = await libraryApi.localCatalog(kind, effectiveProjectID)
    setLocalItems(response.items || [])
  }, [effectiveProjectID, kind, libraryApi])

  const loadRemoteCatalog = useCallback(async () => {
    if (!selectedRemoteId) {
      setRemoteItems([])
      return
    }
    const response = await libraryApi.remoteCatalog(selectedRemoteId, kind, effectiveProjectID)
    setRemoteItems(response.catalog.items || [])
  }, [effectiveProjectID, kind, libraryApi, selectedRemoteId])

  const refreshCatalogs = useCallback(async () => {
    setLoading(true)
    setActionError('')
    try {
      await Promise.all([loadLocalCatalog(), loadRemoteCatalog()])
    } catch (error) {
      setActionError(getErrorMessage(error, 'Unable to load library catalogs'))
    } finally {
      setLoading(false)
    }
  }, [loadLocalCatalog, loadRemoteCatalog])

  useEffect(() => {
    void loadRemotes().catch(error => {
      setActionError(getErrorMessage(error, 'Unable to load remote libraries'))
    })
  }, [loadRemotes])

  useEffect(() => {
    void refreshCatalogs()
  }, [refreshCatalogs])

  useEffect(() => {
    if (projectId && !projectMapping) {
      setProjectMapping(projectId)
    }
  }, [projectId, projectMapping])

  const localStatusById = useMemo(() => {
    const map = new Map<string, string>()
    remoteItems.forEach(item => {
      if (item.local_item_id) {
        map.set(item.local_item_id, item.sync_status || 'synced')
      }
    })
    return map
  }, [remoteItems])

  const localRows = useMemo<LibraryAssetRow[]>(() => (
    sourceFilter === 'host' ? [] : localItems.map(item => ({
      source: 'Local',
      item,
      syncStatus: localStatusById.get(item.id) || 'local only',
    }))
  ), [localItems, localStatusById, sourceFilter])

  const remoteRows = useMemo<LibraryAssetRow[]>(() => (
    sourceFilter === 'local' ? [] : remoteItems.map(item => ({
      source: 'K8s Host',
      item,
      syncStatus: item.sync_status || 'host only',
    }))
  ), [remoteItems, sourceFilter])

  const hostByLocalID = useMemo(() => {
    const map = new Map<string, LibraryCatalogItem>()
    remoteItems.forEach(item => {
      if (item.local_item_id) {
        map.set(item.local_item_id, item)
      }
    })
    return map
  }, [remoteItems])

  const rows = useMemo(() => [...localRows, ...remoteRows], [localRows, remoteRows])

  const actionBlocked = !selectedRemoteId || !credentialsReady || Boolean(projectBlockMessage)
  const actionBlockedReason = !selectedRemoteId
    ? 'Select a remote library first.'
    : !credentialsReady
      ? 'Configure remote credentials in Settings first.'
      : projectBlockMessage

  async function refreshAfterTransfer() {
    await refreshCatalogs()
    await onRefreshLocal?.()
  }

  async function pullRemoteItem(item: LibraryCatalogItem) {
    if (actionBlocked) {
      setActionError(actionBlockedReason)
      return
    }
    setActionMessage('')
    setActionError('')
    setDiffMessage('')
    try {
      const response = await libraryApi.pullRemoteItem(selectedRemoteId, {
        kind: item.kind,
        id: item.remote_item_id || item.id,
        remote_id: item.remote_item_id || item.id,
        project_id: effectiveProjectID,
        include_dependencies: includeDependencies,
      })
      setActionMessage(libraryTransferSummary('pull', response))
      await refreshAfterTransfer()
    } catch (error) {
      setActionError(getErrorMessage(error, 'Unable to pull remote item'))
    }
  }

  async function pushLocalItem(item: LibraryCatalogItem) {
    if (actionBlocked) {
      setActionError(actionBlockedReason)
      return
    }
    setActionMessage('')
    setActionError('')
    setDiffMessage('')
    try {
      const response = await libraryApi.pushLocalItem(selectedRemoteId, {
        kind: item.kind,
        id: item.id,
        local_id: item.id,
        project_id: effectiveProjectID,
        include_dependencies: includeDependencies,
      })
      setActionMessage(libraryTransferSummary('push', response))
      await refreshAfterTransfer()
    } catch (error) {
      setActionError(getErrorMessage(error, 'Unable to push local item'))
    }
  }

  async function compareItems(localItemID: string, remoteItem: LibraryCatalogItem) {
    if (!selectedRemoteId) return
    setActionMessage('')
    setActionError('')
    setDiffMessage('')
    try {
      const diff = await libraryApi.compareItems(selectedRemoteId, {
        kind: remoteItem.kind,
        id: remoteItem.remote_item_id || remoteItem.id,
        local_id: localItemID,
        remote_id: remoteItem.remote_item_id || remoteItem.id,
        project_id: effectiveProjectID,
      })
      setDiffMessage(`${diff.kind} ${diff.status}: local ${shortLibraryHash(diff.local_hash)} / host ${shortLibraryHash(diff.remote_hash)}`)
    } catch (error) {
      setActionError(getErrorMessage(error, 'Unable to compare items'))
    }
  }

  return (
    <div className="library-source-panel">
      <div className="library-source-header">
        <div>
          <h3 className="section-title">Library Sources</h3>
          <p className="library-toolbar-subtitle">{sourceDescription(kind)}</p>
        </div>
        <div className="library-source-summary">
          <span className={`script-health-token ${libraryStatusTone(selectedRemote?.status)}`}>{selectedRemote?.status || 'no host'}</span>
          <span className={`script-health-token ${credentialsReady ? 'healthy' : 'danger'}`}>{credentialsReady ? 'credentials ready' : 'credentials missing'}</span>
        </div>
      </div>

      <div className="library-source-toolbar">
        <label className="project-redaction-rule-field">
          <span>Asset Type</span>
          <input className="settings-input" value={KIND_LABELS[kind]} readOnly />
        </label>
        <label className="project-redaction-rule-field">
          <span>Remote</span>
          <select className="settings-input library-source-select" value={selectedRemoteId} onChange={event => setSelectedRemoteId(event.target.value)}>
            {remotes.length === 0 && <option value="">No remote configured</option>}
            {remotes.map(remote => (
              <option key={remote.id} value={remote.id}>{remote.name}</option>
            ))}
          </select>
        </label>
        <label className="project-redaction-rule-field">
          <span>Source</span>
          <select className="settings-input library-source-select" value={sourceFilter} onChange={event => setSourceFilter(event.target.value as LibrarySourceFilter)}>
            {SOURCE_OPTIONS.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
          </select>
        </label>
        <label className="project-redaction-rule-field">
          <span>Project Mapping</span>
          <input className="settings-input" value={projectMapping} onChange={event => setProjectMapping(event.target.value)} placeholder="local or host project id" />
        </label>
        <label className="library-federation-checkbox">
          <input type="checkbox" checked={includeDependencies} onChange={event => setIncludeDependencies(event.target.checked)} />
          <span>Include dependencies</span>
        </label>
        <button className="btn btn-secondary" type="button" onClick={() => void refreshCatalogs()} disabled={loading}>
          Refresh
        </button>
      </div>

      {actionBlockedReason && <p className="settings-notice">{actionBlockedReason}</p>}
      {actionMessage && <p className="settings-notice">{actionMessage}</p>}
      {diffMessage && <p className="settings-notice">{diffMessage}</p>}
      {actionError && <p className="settings-error-inline">{actionError}</p>}

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
              const hostItem = source === 'Local' ? hostByLocalID.get(item.id) : item
              const href = source === 'K8s Host' ? hostLibraryHref(selectedRemote, item) : ''
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
                          {href && <a className="btn btn-sm btn-ghost" href={href} target="_blank" rel="noreferrer">Open on Host</a>}
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
