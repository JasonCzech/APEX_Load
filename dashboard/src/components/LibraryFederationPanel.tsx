import { confirmAction } from './ux/confirmAction'
import { useApplicationApi } from '../hooks/api/useApplicationApi'
import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'

import { useLibraryApi } from '../hooks/api/useLibraryApi'
import { useNavigationGuard, useNavigationGuardRunner, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { LibraryRemote } from '../types/library'
import { getErrorMessage } from '../utils/errorUtils'
import {
  formatLibraryDateTime,
  libraryStatusTone,
} from './libraryFederationUtils'

type LibraryFederationPanelProps = {
  projects?: { id: string; name: string }[]
  applications?: { id: string; name: string; project_id?: string }[]
  releases?: { id: string; name: string; application_id?: string }[]
  projectId?: string
  applicationId?: string
  applicationName?: string
  releaseId?: string
  releaseName?: string
}

type RemoteDraft = {
  name: string
  base_url: string
  local_project_id: string
  default_project_id: string
  project_mappings: Record<string, string>
  local_application_id: string
  remote_application_id: string
  application_mappings: Record<string, string>
  local_release_id: string
  remote_release_id: string
  release_mappings: Record<string, string>
  auth_method: string
  credential_ref: string
  api_key: string
}

function remoteToDraft(remote?: LibraryRemote, projectId?: string, applicationId?: string, releaseId?: string): RemoteDraft {
  const mappings = remote?.project_mappings || {}
  const applicationMappings = remote?.application_mappings || {}
  const releaseMappings = remote?.release_mappings || {}
  const preferredLocalProject = projectId?.trim() || ''
  const localProject = (preferredLocalProject && mappings[preferredLocalProject]
    ? preferredLocalProject
    : Object.keys(mappings)[0]) || preferredLocalProject
  const preferredLocalApplication = applicationId?.trim() || ''
  const localApplication = preferredLocalApplication || Object.keys(applicationMappings)[0] || ''
  const preferredLocalRelease = releaseId?.trim() || ''
  const localRelease = preferredLocalRelease || Object.keys(releaseMappings)[0] || ''
  return {
    name: remote?.name || '',
    base_url: remote?.base_url || '',
    local_project_id: localProject,
    default_project_id: mappings[localProject] || remote?.default_project_id || '',
    project_mappings: { ...mappings },
    local_application_id: localApplication,
    remote_application_id: applicationMappings[localApplication] || '',
    application_mappings: { ...applicationMappings },
    local_release_id: localRelease,
    remote_release_id: releaseMappings[localRelease] || '',
    release_mappings: { ...releaseMappings },
    auth_method: remote?.auth_method || 'service_account',
    credential_ref: remote?.credential_ref || '',
    api_key: '',
  }
}

function remoteCredentialLabel(remote: LibraryRemote) {
  if (remote.auth_method === 'none') return 'none'
  if (remote.credential_configured) return 'configured'
  return 'missing'
}

function sortedMappings(mappings?: Record<string, string>) {
  return Object.entries(mappings || {}).sort(([left], [right]) => left.localeCompare(right))
}

function remoteDraftFingerprint(draft: RemoteDraft) {
  return JSON.stringify({
    ...draft,
    project_mappings: sortedMappings(draft.project_mappings),
    application_mappings: sortedMappings(draft.application_mappings),
    release_mappings: sortedMappings(draft.release_mappings),
  })
}

function remoteConfigFingerprint(remote: LibraryRemote) {
  return JSON.stringify({
    name: remote.name || '',
    base_url: remote.base_url || '',
    default_project_id: remote.default_project_id || '',
    project_mappings: sortedMappings(remote.project_mappings),
    application_mappings: sortedMappings(remote.application_mappings),
    release_mappings: sortedMappings(remote.release_mappings),
    auth_method: remote.auth_method || 'service_account',
    credential_ref: remote.credential_ref || '',
  })
}

const EMPTY_PROJECTS: NonNullable<LibraryFederationPanelProps['projects']> = []
const EMPTY_APPLICATIONS: NonNullable<LibraryFederationPanelProps['applications']> = []
const EMPTY_RELEASES: NonNullable<LibraryFederationPanelProps['releases']> = []

export default function LibraryFederationPanel({ projects = EMPTY_PROJECTS, applications = EMPTY_APPLICATIONS, releases = EMPTY_RELEASES, projectId, applicationId, applicationName, releaseId, releaseName }: LibraryFederationPanelProps) {
  const libraryApi = useLibraryApi()
  const { runGuarded } = useNavigationGuardRunner()
  const [remotes, setRemotes] = useState<LibraryRemote[]>([])
  const [selectedRemoteId, setSelectedRemoteId] = useState('')
  const [draft, setDraft] = useState<RemoteDraft>(() => remoteToDraft(undefined, projectId, applicationId, releaseId))
  const [baselineDraft, setBaselineDraft] = useState<RemoteDraft>(() => remoteToDraft(undefined, projectId, applicationId, releaseId))
  const [actionLoading, setActionLoading] = useState(false)
  const [listLoading, setListLoading] = useState(false)
  const [actionMessage, setActionMessage] = useState('')
  const [actionError, setActionError] = useState('')
  const remotesRef = useRef(remotes)
  const selectedRemoteIdRef = useRef(selectedRemoteId)
  const draftRef = useRef(draft)
  const baselineDraftRef = useRef(baselineDraft)
  const draftDirtyRef = useRef(false)
  const listGenerationRef = useRef(0)
  const savedRemoteOverridesRef = useRef(new Map<string, LibraryRemote>())
  const deletedRemoteIdsRef = useRef(new Set<string>())
  remotesRef.current = remotes
  selectedRemoteIdRef.current = selectedRemoteId
  draftRef.current = draft
  baselineDraftRef.current = baselineDraft

  const { listApplications, listReleases } = useApplicationApi()
  const hintsId = useId()
  const [mappingApplications, setMappingApplications] = useState(applications)
  const [mappingReleases, setMappingReleases] = useState(releases)
  const [discovered, setDiscovered] = useState<{ remoteId: string; items: { id: string; name: string }[] } | null>(null)
  const [discoverBusy, setDiscoverBusy] = useState(false)
  const [discoverError, setDiscoverError] = useState('')
  const discoveryGeneration = useRef(0)
  useEffect(() => {
    discoveryGeneration.current += 1
    setDiscovered(null); setDiscoverError('')
  }, [selectedRemoteId, draft.base_url, draft.auth_method, draft.credential_ref, draft.api_key])
  useEffect(() => {
    let active = true
    setMappingApplications(applications.filter(item => item.project_id === draft.local_project_id))
    if (draft.local_project_id) void listApplications(draft.local_project_id).then(response => { if (active) setMappingApplications(response.applications || []) }).catch(() => {})
    return () => { active = false }
  }, [applications, draft.local_project_id, listApplications])
  useEffect(() => {
    let active = true
    setMappingReleases(releases.filter(item => item.application_id === draft.local_application_id))
    if (draft.local_application_id) void listReleases(draft.local_application_id).then(response => { if (active) setMappingReleases(response.releases || []) }).catch(() => {})
    return () => { active = false }
  }, [releases, draft.local_application_id, listReleases])
  async function discoverMappings() {
    const remoteId = selectedRemoteId
    if (!remoteId || discoverBusy) return
    const generation = discoveryGeneration.current
    setDiscoverBusy(true); setDiscoverError('')
    try {
      const result = await libraryApi.remoteWorkspaces(remoteId, 'projects')
      if (selectedRemoteIdRef.current === remoteId && generation === discoveryGeneration.current) setDiscovered({ remoteId, items: result.items })
    } catch { if (selectedRemoteIdRef.current === remoteId && generation === discoveryGeneration.current) setDiscoverError('Could not discover workspaces. Check the saved remote connection and workspace read permissions. Older controllers may not support workspace catalogs.') }
    finally { setDiscoverBusy(false) }
  }
  const [remoteApplications, setRemoteApplications] = useState<{ id: string; name: string }[]>([])
  const [remoteReleases, setRemoteReleases] = useState<{ id: string; name: string }[]>([])
  const [remoteWorkspaceStatus, setRemoteWorkspaceStatus] = useState('')
  useEffect(() => {
    const controller = new AbortController()
    setRemoteApplications([]); setRemoteReleases([]); setRemoteWorkspaceStatus('')
    if (discovered?.remoteId === selectedRemoteId && draft.default_project_id) {
      setRemoteWorkspaceStatus('Loading remote applications…')
      void libraryApi.remoteWorkspaces(selectedRemoteId, 'applications', draft.default_project_id, controller.signal).then(result => {
        if (!controller.signal.aborted) { setRemoteApplications(result.items); setRemoteWorkspaceStatus(`${result.items.length} remote applications available.`) }
      }).catch(() => { if (!controller.signal.aborted) setRemoteWorkspaceStatus('Unable to load remote applications. Check the project ID and remote read permissions.') })
    }
    return () => controller.abort()
  }, [discovered, selectedRemoteId, draft.default_project_id, libraryApi])
  useEffect(() => {
    const controller = new AbortController()
    setRemoteReleases([])
    if (discovered?.remoteId === selectedRemoteId && remoteApplications.some(item => item.id === draft.remote_application_id)) {
      void libraryApi.remoteWorkspaces(selectedRemoteId, 'releases', draft.remote_application_id, controller.signal).then(result => {
        if (!controller.signal.aborted) { setRemoteReleases(result.items); setRemoteWorkspaceStatus(`${result.items.length} remote releases available.`) }
      }).catch(() => { if (!controller.signal.aborted) setRemoteWorkspaceStatus('Unable to load remote releases. Check the application ID and remote read permissions.') })
    }
    return () => controller.abort()
  }, [discovered, selectedRemoteId, draft.remote_application_id, remoteApplications, libraryApi])
  const draftDirty = remoteDraftFingerprint(draft) !== remoteDraftFingerprint(baselineDraft)
  draftDirtyRef.current = draftDirty
  const loading = actionLoading || listLoading

  const replaceDraft = useCallback((nextDraft: RemoteDraft) => {
    draftRef.current = nextDraft
    baselineDraftRef.current = nextDraft
    draftDirtyRef.current = false
    setDraft(nextDraft)
    setBaselineDraft(nextDraft)
  }, [])

  const editDraft = useCallback((update: (current: RemoteDraft) => RemoteDraft) => {
    setDraft(current => {
      const next = update(current)
      draftRef.current = next
      return next
    })
  }, [])

  const applyRemoteSelection = useCallback((remoteId: string, candidates: LibraryRemote[]) => {
    const nextRemote = candidates.find(remote => remote.id === remoteId)
    selectedRemoteIdRef.current = nextRemote?.id || ''
    setSelectedRemoteId(nextRemote?.id || '')
    replaceDraft(remoteToDraft(nextRemote, projectId, applicationId, releaseId))
  }, [applicationId, projectId, releaseId, replaceDraft])

  const reconcileRemoteSnapshot = useCallback((serverRemotes: LibraryRemote[]) => {
    const serverById = new Map(serverRemotes.map(remote => [remote.id, remote]))
    const deletedAtRequestCompletion = new Set(deletedRemoteIdsRef.current)
    deletedAtRequestCompletion.forEach(remoteId => {
      if (!serverById.has(remoteId)) deletedRemoteIdsRef.current.delete(remoteId)
    })

    const reconciledById = new Map(
      serverRemotes
        .filter(remote => !deletedAtRequestCompletion.has(remote.id))
        .map(remote => [remote.id, remote]),
    )
    savedRemoteOverridesRef.current.forEach((savedRemote, remoteId) => {
      const serverRemote = serverById.get(remoteId)
      if (serverRemote && remoteConfigFingerprint(serverRemote) === remoteConfigFingerprint(savedRemote)) {
        savedRemoteOverridesRef.current.delete(remoteId)
        reconciledById.set(remoteId, serverRemote)
      } else {
        reconciledById.set(remoteId, savedRemote)
      }
    })
    return Array.from(reconciledById.values())
  }, [])

  const loadRemotes = useCallback(async (options?: { preferredRemoteId?: string; resetDraft?: boolean }) => {
    const generation = ++listGenerationRef.current
    setListLoading(true)
    try {
      const response = await libraryApi.listRemotes()
      if (generation !== listGenerationRef.current) return false
      const nextRemotes = reconcileRemoteSnapshot(response.remotes || [])
      remotesRef.current = nextRemotes
      setRemotes(nextRemotes)

      if (draftDirtyRef.current && !options?.resetDraft) return true
      const preferredRemoteId = options?.preferredRemoteId || selectedRemoteIdRef.current
      const nextRemoteId = nextRemotes.some(remote => remote.id === preferredRemoteId)
        ? preferredRemoteId
        : nextRemotes[0]?.id || ''
      applyRemoteSelection(nextRemoteId, nextRemotes)
      return true
    } finally {
      if (generation === listGenerationRef.current) setListLoading(false)
    }
  }, [applyRemoteSelection, libraryApi, reconcileRemoteSnapshot])

  const selectedRemote = useMemo(
    () => remotes.find(remote => remote.id === selectedRemoteId),
    [remotes, selectedRemoteId],
  )

  useEffect(() => {
    void loadRemotes().catch(error => {
      setActionError(getErrorMessage(error, 'Unable to load Remote Workspaces'))
    })
  }, [loadRemotes])

  const contextKey = `${projectId || ''}\u0000${applicationId || ''}\u0000${releaseId || ''}`
  const contextKeyRef = useRef(contextKey)
  useEffect(() => {
    if (contextKeyRef.current === contextKey) return
    contextKeyRef.current = contextKey
    replaceDraft(remoteToDraft(selectedRemote, projectId, applicationId, releaseId))
  }, [applicationId, contextKey, projectId, releaseId, replaceDraft, selectedRemote])

  async function saveRemote(): Promise<boolean> {
    const pendingDraft = draftRef.current
    const pendingRemoteId = selectedRemoteIdRef.current
    const pendingRemote = remotesRef.current.find(remote => remote.id === pendingRemoteId)
    if (!pendingDraft.name.trim() || !pendingDraft.base_url.trim() || !pendingDraft.local_project_id.trim() || !pendingDraft.default_project_id.trim()) {
      setActionError('Remote name, base URL, local project, and remote project are required.')
      return false
    }
    setActionMessage('')
    setActionError('')
    setActionLoading(true)
    ++listGenerationRef.current
    setListLoading(false)
    let saved: LibraryRemote
    try {
      const applicationMappings = { ...pendingDraft.application_mappings }
      const localApplicationId = pendingDraft.local_application_id.trim()
      const remoteApplicationId = pendingDraft.remote_application_id.trim()
      if (localApplicationId) {
        if (remoteApplicationId) applicationMappings[localApplicationId] = remoteApplicationId
        else delete applicationMappings[localApplicationId]
      }
      const releaseMappings = { ...pendingDraft.release_mappings }
      const localReleaseId = pendingDraft.local_release_id.trim()
      const remoteReleaseId = pendingDraft.remote_release_id.trim()
      if (localReleaseId) {
        if (remoteReleaseId) releaseMappings[localReleaseId] = remoteReleaseId
        else delete releaseMappings[localReleaseId]
      }
      const body = {
        name: pendingDraft.name.trim(),
        base_url: pendingDraft.base_url.trim(),
        default_project_id: pendingDraft.default_project_id.trim(),
        project_mappings: {
          ...pendingDraft.project_mappings,
          [pendingDraft.local_project_id.trim()]: pendingDraft.default_project_id.trim(),
        },
        application_mappings: applicationMappings,
        release_mappings: releaseMappings,
        auth_method: pendingDraft.auth_method,
        credential_ref: pendingDraft.credential_ref.trim(),
        api_key: pendingDraft.api_key.trim(),
      }
      saved = pendingRemote
        ? await libraryApi.updateRemote(pendingRemote.id, body)
        : await libraryApi.createRemote(body)
    } catch (error) {
      setActionError(getErrorMessage(error, pendingRemote ? 'Unable to update Remote Workspace' : 'Unable to add Remote Workspace'))
      setActionLoading(false)
      return false
    }

    deletedRemoteIdsRef.current.delete(saved.id)
    savedRemoteOverridesRef.current.set(saved.id, saved)
    const optimisticRemotes = [
      ...remotesRef.current.filter(remote => remote.id !== saved.id),
      saved,
    ]
    remotesRef.current = optimisticRemotes
    setRemotes(optimisticRemotes)
    applyRemoteSelection(saved.id, optimisticRemotes)
    setActionMessage(`${pendingRemote ? 'Updated' : 'Added'} Remote Workspace "${saved.name}".`)
    try {
      await loadRemotes({ preferredRemoteId: saved.id, resetDraft: true })
    } catch (error) {
      setActionError(`Remote Workspace was saved, but the list could not be refreshed: ${getErrorMessage(error, 'refresh failed')}`)
    } finally {
      setActionLoading(false)
    }
    return true
  }

  async function probeRemote(remoteId: string) {
    setActionMessage('')
    setActionError('')
    setActionLoading(true)
    ++listGenerationRef.current
    setListLoading(false)
    try {
      const response = await libraryApi.probeRemote(remoteId)
      await loadRemotes({ preferredRemoteId: remoteId, resetDraft: true })
      setActionMessage(response.status === 'healthy' ? 'Remote library is healthy.' : `Remote library is unreachable: ${response.error || 'probe failed'}`)
    } catch (error) {
      setActionError(getErrorMessage(error, 'Unable to probe Remote Workspace'))
    } finally {
      setActionLoading(false)
    }
  }

  async function deleteRemote(remote: LibraryRemote) {
    if (!(await confirmAction(`Delete remote workspace "${remote.name}"? Sync links for this host will also be removed.`, { title: 'Delete remote workspace', confirmLabel: 'Delete', destructive: true }))) return
    setActionMessage('')
    setActionError('')
    setActionLoading(true)
    ++listGenerationRef.current
    setListLoading(false)
    try {
      await libraryApi.deleteRemote(remote.id)
      setActionMessage(`Deleted Remote Workspace "${remote.name}".`)
    } catch (error) {
      setActionError(getErrorMessage(error, 'Unable to delete Remote Workspace'))
      setActionLoading(false)
      return
    }

    savedRemoteOverridesRef.current.delete(remote.id)
    deletedRemoteIdsRef.current.add(remote.id)
    const remaining = remotesRef.current.filter(candidate => candidate.id !== remote.id)
    remotesRef.current = remaining
    setRemotes(remaining)
    applyRemoteSelection(remaining[0]?.id || '', remaining)
    try {
      await loadRemotes({ resetDraft: true })
    } catch (error) {
      setActionError(`Remote Workspace was deleted, but the list could not be refreshed: ${getErrorMessage(error, 'refresh failed')}`)
    } finally {
      setActionLoading(false)
    }
  }

  function applyNewRemote() {
    selectedRemoteIdRef.current = ''
    setSelectedRemoteId('')
    replaceDraft(remoteToDraft(undefined, projectId, applicationId, releaseId))
    setActionMessage('')
    setActionError('')
  }

  function requestRefresh() {
    runGuarded(() => {
      setActionMessage('')
      setActionError('')
      void loadRemotes({ resetDraft: true }).catch(error => {
        setActionError(getErrorMessage(error, 'Unable to load Remote Workspaces'))
      })
    })
  }

  const saveRemoteRef = useRef(saveRemote)
  saveRemoteRef.current = saveRemote
  const discardDraftRef = useRef<() => void>(() => undefined)
  discardDraftRef.current = () => {
    replaceDraft(baselineDraftRef.current)
    setActionMessage('')
    setActionError('')
  }
  const navigationGuard = useMemo<NavigationGuard>(() => ({
    id: `library-federation-draft-${projectId || 'unscoped'}`,
    label: 'Remote workspace mapping and credential draft',
    isDirty: () => draftDirtyRef.current,
    save: () => saveRemoteRef.current(),
    discard: () => discardDraftRef.current(),
  }), [projectId])
  useNavigationGuard(navigationGuard, draftDirty)

  const canSave = Boolean(draft.name.trim() && draft.base_url.trim() && draft.local_project_id.trim() && draft.default_project_id.trim())

  return (
    <div className="settings-subsection library-federation-panel">
      <div className="settings-manager-summary">
        <div>
          <span className="setting-label">Remote Workspaces</span>
          <span className="setting-desc">Manage Kubernetes-hosted team libraries. API keys are write-only and resolved by the local controller.</span>
        </div>
        <strong>{remotes.length}</strong>
      </div>

      <div className="settings-inline-actions">
        <button className="btn btn-secondary" type="button" onClick={() => runGuarded(applyNewRemote)} disabled={loading}>
          New Remote
        </button>
        <button className="btn btn-secondary" type="button" onClick={requestRefresh} disabled={loading}>
          Refresh
        </button>
        {selectedRemote && (
          <>
            <button className="btn btn-secondary" type="button" onClick={() => runGuarded(() => { void probeRemote(selectedRemote.id) })} disabled={loading}>
              Probe Remote
            </button>
            <button
              className="btn btn-ghost"
              type="button"
              onClick={() => runGuarded(() => {
                const currentRemote = remotesRef.current.find(remote => remote.id === selectedRemote.id)
                if (currentRemote) void deleteRemote(currentRemote)
              })}
              disabled={loading}
              style={{ color: 'var(--danger-text)' }}
            >
              Delete
            </button>
          </>
        )}
      </div>

      {remotes.length > 0 && (
        <div className="settings-release-list">
          {remotes.map(remote => (
            <button
              key={remote.id}
              type="button"
              className={`settings-release-row ${remote.id === selectedRemoteId ? 'active' : ''}`}
              onClick={() => runGuarded(() => applyRemoteSelection(remote.id, remotesRef.current))}
              disabled={loading}
            >
              <div className="settings-release-main">
                <span>{remote.name}</span>
                <small>{remote.base_url}</small>
                <small>
                  Project mappings {Object.entries(remote.project_mappings || {}).map(([local, target]) => `${local} → ${target}`).join(', ') || 'unmapped'} / application mappings {Object.entries(remote.application_mappings || {}).map(([local, target]) => `${local} → ${target}`).join(', ') || 'unmapped'} / release mappings {Object.entries(remote.release_mappings || {}).map(([local, target]) => `${local} → ${target}`).join(', ') || 'unmapped'} / checked {formatLibraryDateTime(remote.last_health_check)} / credentials {remoteCredentialLabel(remote)}
                </small>
                {remote.last_error && <small>{remote.last_error}</small>}
              </div>
              <div className="settings-remote-status">
                <span className={`script-health-token ${libraryStatusTone(remote.status)}`}>{remote.status || 'unknown'}</span>
                <span className={`script-health-token ${remote.credential_configured || remote.auth_method === 'none' ? 'healthy' : 'danger'}`}>
                  {remoteCredentialLabel(remote)}
                </span>
              </div>
            </button>
          ))}
        </div>
      )}

      <div className="settings-subsection">
        <button className="btn btn-secondary" type="button" disabled={loading || draftDirty || !selectedRemoteId || discoverBusy} onClick={() => void discoverMappings()}>{discoverBusy ? 'Discovering workspaces…' : 'Discover remote workspaces'}</button>
        <p>Discover authorized projects, including empty workspaces. Choose a remote project to load its applications, then an application to load its releases. Save and probe the connection first.</p>
        {discoverError && <p role="alert">{discoverError}</p>}
        {discovered?.remoteId === selectedRemoteId && <p role="status">{discovered.items.length} remote projects available.</p>}
        <datalist id={`${hintsId}-project`}>{(discovered?.remoteId === selectedRemoteId ? discovered.items : []).map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</datalist>
        <datalist id={`${hintsId}-application`}>{remoteApplications.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</datalist>
        <datalist id={`${hintsId}-release`}>{remoteReleases.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</datalist>
        {remoteWorkspaceStatus && <p role="status">{remoteWorkspaceStatus}</p>}
      </div>
      <div className="project-redaction-rule-grid">
        <label className="project-redaction-rule-field">
          <span>Remote Name</span>
          <input className="settings-input" value={draft.name} onChange={event => editDraft(prev => ({ ...prev, name: event.target.value }))} placeholder="Team AKS" disabled={loading} />
        </label>
        <label className="project-redaction-rule-field">
          <span>Base URL</span>
          <input className="settings-input" value={draft.base_url} onChange={event => editDraft(prev => ({ ...prev, base_url: event.target.value }))} placeholder="https://apexload.example.com" disabled={loading} />
        </label>
        <label className="project-redaction-rule-field">
          <span>Local Project</span>
          <select className="settings-input" value={draft.local_project_id} onChange={event => editDraft(prev => ({ ...prev, local_project_id: event.target.value, default_project_id: prev.project_mappings[event.target.value] || '', local_application_id: '', remote_application_id: '', local_release_id: '', remote_release_id: '' }))} disabled={loading}><option value="">Select project</option>{projects.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}{draft.local_project_id && !projects.some(item => item.id === draft.local_project_id) && <option value={draft.local_project_id}>Unavailable project ({draft.local_project_id})</option>}</select>
        </label>
        <label className="project-redaction-rule-field">
          <span>Remote Project</span>
          <input className="settings-input" list={`${hintsId}-project`} value={draft.default_project_id} onChange={event => editDraft(prev => ({ ...prev, default_project_id: event.target.value, remote_application_id: '', remote_release_id: '' }))} placeholder="remote-project-id" disabled={loading} />
        </label>
        <label className="project-redaction-rule-field">
          <span>Local Application{applicationName ? ` (${applicationName})` : ''}</span>
          <select className="settings-input" value={draft.local_application_id} onChange={event => editDraft(prev => ({ ...prev, local_application_id: event.target.value, remote_application_id: prev.application_mappings[event.target.value.trim()] || '', local_release_id: '', remote_release_id: '' }))} disabled={loading}><option value="">Select application</option>{mappingApplications.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}{draft.local_application_id && !mappingApplications.some(item => item.id === draft.local_application_id) && <option value={draft.local_application_id}>Unavailable application ({draft.local_application_id})</option>}</select>
        </label>
        <label className="project-redaction-rule-field">
          <span>Remote Application</span>
          <input className="settings-input" list={`${hintsId}-application`} value={draft.remote_application_id} onChange={event => editDraft(prev => ({ ...prev, remote_application_id: event.target.value, remote_release_id: '' }))} placeholder="remote-application-id" disabled={loading} />
        </label>
        <label className="project-redaction-rule-field">
          <span>Local Release{releaseName ? ` (${releaseName})` : ''}</span>
          <select className="settings-input" value={draft.local_release_id} onChange={event => editDraft(prev => ({ ...prev, local_release_id: event.target.value, remote_release_id: prev.release_mappings[event.target.value.trim()] || '' }))} disabled={loading}><option value="">Select release</option>{mappingReleases.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}{draft.local_release_id && !mappingReleases.some(item => item.id === draft.local_release_id) && <option value={draft.local_release_id}>Unavailable release ({draft.local_release_id})</option>}</select>
        </label>
        <label className="project-redaction-rule-field">
          <span>Remote Release</span>
          <input className="settings-input" list={`${hintsId}-release`} value={draft.remote_release_id} onChange={event => editDraft(prev => ({ ...prev, remote_release_id: event.target.value }))} placeholder="remote-release-id" disabled={loading} />
        </label>
        <label className="project-redaction-rule-field">
          <span>Auth Method</span>
          <select className="settings-select" value={draft.auth_method} onChange={event => editDraft(prev => ({ ...prev, auth_method: event.target.value }))} disabled={loading}>
            <option value="service_account">Service Account</option>
            <option value="none">None</option>
          </select>
        </label>
        <label className="project-redaction-rule-field">
          <span>Credential Ref</span>
          <input className="settings-input" value={draft.credential_ref} onChange={event => editDraft(prev => ({ ...prev, credential_ref: event.target.value }))} placeholder="env:APEXLOAD_LIBRARY_REMOTE_TOKEN" disabled={loading} />
        </label>
        <label className="project-redaction-rule-field">
          <span>{selectedRemoteId ? 'Rotate API key' : 'API key'}</span>
          <input className="settings-input" type="password" value={draft.api_key} onChange={event => editDraft(prev => ({ ...prev, api_key: event.target.value }))} placeholder="Paste once; never returned" disabled={loading} />
        </label>
      </div>

      <div className="settings-inline-actions">
        <button className="btn btn-primary" type="button" onClick={() => void saveRemote()} disabled={!canSave || loading}>
          {selectedRemote ? 'Save Remote' : 'Add Remote'}
        </button>
      </div>

      {actionMessage && <p className="settings-notice">{actionMessage}</p>}
      {actionError && <p className="settings-error-inline">{actionError}</p>}
    </div>
  )
}
