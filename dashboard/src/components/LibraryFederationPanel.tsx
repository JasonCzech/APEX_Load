import { useCallback, useEffect, useMemo, useState } from 'react'
import { apiFetch, request } from '../hooks/apiClient'
import { getErrorMessage } from '../utils/errorUtils'

type LibraryKind = 'script' | 'scenario' | 'result'
type SourceFilter = 'all' | 'local' | 'host'

type LibraryDependency = {
  kind: string
  id: string
  name?: string
  content_hash?: string
}

type LibraryCatalogItem = {
  kind: LibraryKind
  id: string
  name: string
  project_id?: string
  protocol?: string
  version?: string
  status?: string
  content_hash?: string
  updated_at?: string
  has_archive?: boolean
  dependencies?: LibraryDependency[]
  sync_status?: string
  local_item_id?: string
  remote_item_id?: string
}

type LibraryCatalogResponse = {
  items?: LibraryCatalogItem[]
  count?: number
}

type LibraryCapabilitiesResponse = {
  schema_version?: string
  instance_id?: string
  item_kinds?: string[]
  operations?: string[]
}

type LibraryRemote = {
  id: string
  name: string
  base_url: string
  default_project_id?: string
  auth_method?: string
  status?: string
  last_health_check?: string
  last_error?: string
  capabilities?: LibraryCapabilitiesResponse
}

type LibraryRemoteListResponse = {
  remotes?: LibraryRemote[]
  count?: number
}

type LibraryRemoteCatalogResponse = {
  remote: LibraryRemote
  catalog: LibraryCatalogResponse
}

type LibraryTransferResponse = {
  remote?: LibraryRemote
  import?: {
    items?: Array<{ kind: string; local_item_id: string; action: string }>
    conflicts?: Array<{ kind: string; local_item_id?: string; reason: string }>
  }
}

type LibraryDiffResponse = {
  kind: string
  local_id: string
  remote_id: string
  local_hash: string
  remote_hash: string
  status: string
}

type LibraryFederationPanelProps = {
  projectId?: string
}

const KIND_OPTIONS: Array<{ value: LibraryKind; label: string }> = [
  { value: 'script', label: 'Scripts' },
  { value: 'scenario', label: 'Scenarios' },
  { value: 'result', label: 'Results' },
]

const SOURCE_OPTIONS: Array<{ value: SourceFilter; label: string }> = [
  { value: 'all', label: 'All' },
  { value: 'local', label: 'Local' },
  { value: 'host', label: 'K8s Host' },
]

function catalogPath(kind: LibraryKind, projectId?: string) {
  const params = new URLSearchParams({ kind })
  if (projectId?.trim()) {
    params.set('project_id', projectId.trim())
  }
  return `/library/catalog?${params.toString()}`
}

function remoteCatalogPath(remoteId: string, kind: LibraryKind, projectId?: string) {
  const params = new URLSearchParams({ kind })
  if (projectId?.trim()) {
    params.set('project_id', projectId.trim())
  }
  return `/library/remotes/${encodeURIComponent(remoteId)}/catalog?${params.toString()}`
}

function formatDateTime(value?: string) {
  if (!value) return 'Never'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return value
  return parsed.toLocaleString()
}

function shortHash(value?: string) {
  return value ? value.slice(0, 12) : '-'
}

function statusTone(status?: string) {
  switch ((status || '').toLowerCase()) {
    case 'synced':
    case 'healthy':
      return 'healthy'
    case 'conflict':
    case 'unreachable':
      return 'danger'
    case 'stale':
    case 'host only':
    case 'local only':
      return 'warning'
    default:
      return ''
  }
}

function normalizedBaseURL(remote?: LibraryRemote) {
  return (remote?.base_url || '').replace(/\/+$/, '')
}

function hostHref(remote: LibraryRemote | undefined, item: LibraryCatalogItem) {
  const base = normalizedBaseURL(remote)
  if (!base) return ''
  if (item.kind === 'script') {
    return `${base}/#/scripts?mode=edit&scriptId=${encodeURIComponent(item.remote_item_id || item.id)}`
  }
  if (item.kind === 'result') {
    return `${base}/#/test/${encodeURIComponent(item.remote_item_id || item.id)}`
  }
  return `${base}/#/tests`
}

function transferSummary(action: 'pull' | 'push', response: LibraryTransferResponse) {
  const imported = response.import
  const conflicts = imported?.conflicts || []
  if (conflicts.length > 0) {
    return `${action === 'pull' ? 'Pull' : 'Push'} found ${conflicts.length} conflict${conflicts.length === 1 ? '' : 's'}: ${conflicts.map(conflict => conflict.reason).join('; ')}`
  }
  const count = imported?.items?.length || 0
  return `${action === 'pull' ? 'Pulled' : 'Pushed'} ${count} item${count === 1 ? '' : 's'}.`
}

async function transferRequest(path: string, body: unknown): Promise<LibraryTransferResponse> {
  const response = await apiFetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  const payload = await response.json().catch(() => ({}))
  if (!response.ok && response.status !== 409) {
    const message = typeof payload === 'object' && payload !== null && 'error' in payload && typeof payload.error === 'string'
      ? payload.error
      : response.statusText
    throw new Error(message)
  }
  return payload as LibraryTransferResponse
}

export default function LibraryFederationPanel({ projectId }: LibraryFederationPanelProps) {
  const [remotes, setRemotes] = useState<LibraryRemote[]>([])
  const [selectedRemoteId, setSelectedRemoteId] = useState('')
  const [kind, setKind] = useState<LibraryKind>('script')
  const [sourceFilter, setSourceFilter] = useState<SourceFilter>('all')
  const [projectMapping, setProjectMapping] = useState(projectId || '')
  const [includeDependencies, setIncludeDependencies] = useState(true)
  const [localItems, setLocalItems] = useState<LibraryCatalogItem[]>([])
  const [remoteItems, setRemoteItems] = useState<LibraryCatalogItem[]>([])
  const [remoteName, setRemoteName] = useState('')
  const [remoteBaseURL, setRemoteBaseURL] = useState('')
  const [remoteDefaultProject, setRemoteDefaultProject] = useState(projectId || '')
  const [remoteAuthMethod, setRemoteAuthMethod] = useState('service_account')
  const [remoteAPIKey, setRemoteAPIKey] = useState('')
  const [loading, setLoading] = useState(false)
  const [actionMessage, setActionMessage] = useState('')
  const [actionError, setActionError] = useState('')
  const [diffMessage, setDiffMessage] = useState('')

  const selectedRemote = useMemo(
    () => remotes.find(remote => remote.id === selectedRemoteId),
    [remotes, selectedRemoteId],
  )

  const effectiveProjectID = projectMapping.trim() || selectedRemote?.default_project_id || ''

  const loadRemotes = useCallback(async () => {
    const response = await request<LibraryRemoteListResponse>('/library/remotes')
    const nextRemotes = response.remotes || []
    setRemotes(nextRemotes)
    setSelectedRemoteId(current => {
      if (current && nextRemotes.some(remote => remote.id === current)) return current
      return nextRemotes[0]?.id || ''
    })
  }, [])

  const loadLocalCatalog = useCallback(async () => {
    const response = await request<LibraryCatalogResponse>(catalogPath(kind, effectiveProjectID))
    setLocalItems(response.items || [])
  }, [effectiveProjectID, kind])

  const loadRemoteCatalog = useCallback(async () => {
    if (!selectedRemoteId) {
      setRemoteItems([])
      return
    }
    const response = await request<LibraryRemoteCatalogResponse>(remoteCatalogPath(selectedRemoteId, kind, effectiveProjectID))
    setRemoteItems(response.catalog.items || [])
  }, [effectiveProjectID, kind, selectedRemoteId])

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
    if (!projectMapping && projectId) {
      setProjectMapping(projectId)
    }
    if (!remoteDefaultProject && projectId) {
      setRemoteDefaultProject(projectId)
    }
  }, [projectId, projectMapping, remoteDefaultProject])

  const localStatusById = useMemo(() => {
    const map = new Map<string, string>()
    remoteItems.forEach(item => {
      if (item.local_item_id) {
        map.set(item.local_item_id, item.sync_status || 'synced')
      }
    })
    return map
  }, [remoteItems])

  const localRows = useMemo(() => (
    sourceFilter === 'host' ? [] : localItems.map(item => ({
      source: 'Local',
      item,
      syncStatus: localStatusById.get(item.id) || 'local only',
    }))
  ), [localItems, localStatusById, sourceFilter])

  const remoteRows = useMemo(() => (
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

  async function createRemote() {
    setActionMessage('')
    setActionError('')
    try {
      const created = await request<LibraryRemote>('/library/remotes', {
        method: 'POST',
        body: JSON.stringify({
          name: remoteName.trim(),
          base_url: remoteBaseURL.trim(),
          default_project_id: remoteDefaultProject.trim(),
          auth_method: remoteAuthMethod,
          api_key: remoteAPIKey.trim(),
        }),
      })
      setRemoteName('')
      setRemoteBaseURL('')
      setRemoteAPIKey('')
      setActionMessage(`Added remote library "${created.name}".`)
      await loadRemotes()
      setSelectedRemoteId(created.id)
    } catch (error) {
      setActionError(getErrorMessage(error, 'Unable to add remote library'))
    }
  }

  async function probeRemote(remoteId: string) {
    setActionMessage('')
    setActionError('')
    try {
      const response = await request<{ status: string; error?: string }>(`/library/remotes/${encodeURIComponent(remoteId)}/probe`, { method: 'POST' })
      await loadRemotes()
      setActionMessage(response.status === 'healthy' ? 'Remote library is healthy.' : `Remote library is unreachable: ${response.error || 'probe failed'}`)
    } catch (error) {
      setActionError(getErrorMessage(error, 'Unable to probe remote library'))
    }
  }

  async function pullRemoteItem(item: LibraryCatalogItem) {
    if (!selectedRemoteId) return
    setActionMessage('')
    setActionError('')
    setDiffMessage('')
    try {
      const response = await transferRequest(`/library/remotes/${encodeURIComponent(selectedRemoteId)}/pull`, {
          kind: item.kind,
          id: item.remote_item_id || item.id,
          remote_id: item.remote_item_id || item.id,
          project_id: effectiveProjectID,
          include_dependencies: includeDependencies,
      })
      setActionMessage(transferSummary('pull', response))
      await refreshCatalogs()
    } catch (error) {
      setActionError(getErrorMessage(error, 'Unable to pull remote item'))
    }
  }

  async function pushLocalItem(item: LibraryCatalogItem) {
    if (!selectedRemoteId) return
    setActionMessage('')
    setActionError('')
    setDiffMessage('')
    try {
      const response = await transferRequest(`/library/remotes/${encodeURIComponent(selectedRemoteId)}/push`, {
          kind: item.kind,
          id: item.id,
          local_id: item.id,
          project_id: effectiveProjectID,
          include_dependencies: includeDependencies,
      })
      setActionMessage(transferSummary('push', response))
      await refreshCatalogs()
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
      const diff = await request<LibraryDiffResponse>(`/library/remotes/${encodeURIComponent(selectedRemoteId)}/diff`, {
        method: 'POST',
        body: JSON.stringify({
          kind: remoteItem.kind,
          id: remoteItem.remote_item_id || remoteItem.id,
          local_id: localItemID,
          remote_id: remoteItem.remote_item_id || remoteItem.id,
          project_id: effectiveProjectID,
        }),
      })
      setDiffMessage(`${diff.kind} ${diff.status}: local ${shortHash(diff.local_hash)} / host ${shortHash(diff.remote_hash)}`)
    } catch (error) {
      setActionError(getErrorMessage(error, 'Unable to compare items'))
    }
  }

  const rows = [...localRows, ...remoteRows]

  return (
    <div className="settings-subsection library-federation-panel">
      <div className="settings-manager-summary">
        <div>
          <span className="setting-label">Remote Libraries</span>
          <span className="setting-desc">Browse local and Kubernetes-hosted scripts, scenarios, and completed results with explicit push/pull transfer.</span>
        </div>
        <strong>{remotes.length}</strong>
      </div>

      <div className="project-redaction-rule-grid">
        <label className="project-redaction-rule-field">
          <span>Remote Name</span>
          <input className="settings-input" value={remoteName} onChange={event => setRemoteName(event.target.value)} placeholder="Team AKS" />
        </label>
        <label className="project-redaction-rule-field">
          <span>Base URL</span>
          <input className="settings-input" value={remoteBaseURL} onChange={event => setRemoteBaseURL(event.target.value)} placeholder="https://apexload.example.com" />
        </label>
        <label className="project-redaction-rule-field">
          <span>Default Project Mapping</span>
          <input className="settings-input" value={remoteDefaultProject} onChange={event => setRemoteDefaultProject(event.target.value)} placeholder="project-id" />
        </label>
        <label className="project-redaction-rule-field">
          <span>Auth Method</span>
          <select className="settings-select" value={remoteAuthMethod} onChange={event => setRemoteAuthMethod(event.target.value)}>
            <option value="service_account">Service Account</option>
            <option value="none">None</option>
          </select>
        </label>
        <label className="project-redaction-rule-field project-redaction-rule-field-wide">
          <span>API Key</span>
          <input className="settings-input" type="password" value={remoteAPIKey} onChange={event => setRemoteAPIKey(event.target.value)} placeholder="Paste once; not returned to browser storage" />
        </label>
      </div>

      <div className="settings-inline-actions">
        <button className="btn btn-secondary" type="button" onClick={() => void createRemote()} disabled={!remoteName.trim() || !remoteBaseURL.trim()}>
          Add Remote
        </button>
        <button className="btn btn-secondary" type="button" onClick={() => void refreshCatalogs()} disabled={loading}>
          Refresh Catalogs
        </button>
        {selectedRemoteId && (
          <button className="btn btn-secondary" type="button" onClick={() => void probeRemote(selectedRemoteId)}>
            Probe Remote
          </button>
        )}
      </div>

      {remotes.length > 0 && (
        <div className="settings-release-list">
          {remotes.map(remote => (
            <button
              key={remote.id}
              type="button"
              className={`settings-release-row ${remote.id === selectedRemoteId ? 'active' : ''}`}
              onClick={() => setSelectedRemoteId(remote.id)}
            >
              <div className="settings-release-main">
                <span>{remote.name}</span>
                <small>{remote.base_url}</small>
                <small>Project {remote.default_project_id || 'unmapped'} / checked {formatDateTime(remote.last_health_check)}</small>
                {remote.last_error && <small>{remote.last_error}</small>}
              </div>
              <span className={`script-health-token ${statusTone(remote.status)}`}>{remote.status || 'unknown'}</span>
            </button>
          ))}
        </div>
      )}

      <div className="library-federation-toolbar">
        <label className="project-redaction-rule-field">
          <span>Kind</span>
          <select className="settings-select" value={kind} onChange={event => setKind(event.target.value as LibraryKind)}>
            {KIND_OPTIONS.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
          </select>
        </label>
        <label className="project-redaction-rule-field">
          <span>Source</span>
          <select className="settings-select" value={sourceFilter} onChange={event => setSourceFilter(event.target.value as SourceFilter)}>
            {SOURCE_OPTIONS.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
          </select>
        </label>
        <label className="project-redaction-rule-field">
          <span>Transfer Project Mapping</span>
          <input className="settings-input" value={projectMapping} onChange={event => setProjectMapping(event.target.value)} placeholder="local or host project id" />
        </label>
        <label className="library-federation-checkbox">
          <input type="checkbox" checked={includeDependencies} onChange={event => setIncludeDependencies(event.target.checked)} />
          <span>Include dependencies</span>
        </label>
      </div>

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
              const href = source === 'K8s Host' ? hostHref(selectedRemote, item) : ''
              const localItemID = item.local_item_id
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
                  <td><span className={`script-health-token ${statusTone(syncStatus)}`}>{syncStatus}</span></td>
                  <td>
                    <div className="script-table-usage">
                      <span>Project {item.project_id || 'unmapped'}</span>
                      <span>Hash {shortHash(item.content_hash)}</span>
                      <span>Updated {formatDateTime(item.updated_at)}</span>
                      {item.dependencies && item.dependencies.length > 0 && <span>{item.dependencies.length} dependencies</span>}
                    </div>
                  </td>
                  <td>
                    <div className="settings-inline-actions">
                      {source === 'Local' ? (
                        <>
                          <button className="btn btn-sm btn-secondary" type="button" onClick={() => void pushLocalItem(item)} disabled={!selectedRemoteId}>
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
                          <button className="btn btn-sm btn-secondary" type="button" onClick={() => void pullRemoteItem(item)} disabled={!selectedRemoteId}>
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
                <td colSpan={5}>{loading ? 'Loading library catalog...' : 'No library items match this source and kind.'}</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
