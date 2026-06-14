import { useCallback, useEffect, useMemo, useState } from 'react'

import { useLibraryApi } from '../hooks/api/useLibraryApi'
import { LibraryRemote } from '../types/library'
import { getErrorMessage } from '../utils/errorUtils'
import {
  formatLibraryDateTime,
  libraryStatusTone,
} from './libraryFederationUtils'

type LibraryFederationPanelProps = {
  projectId?: string
}

type RemoteDraft = {
  name: string
  base_url: string
  default_project_id: string
  auth_method: string
  credential_ref: string
  api_key: string
}

function remoteToDraft(remote?: LibraryRemote, projectId?: string): RemoteDraft {
  return {
    name: remote?.name || '',
    base_url: remote?.base_url || '',
    default_project_id: remote?.default_project_id || projectId || '',
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

export default function LibraryFederationPanel({ projectId }: LibraryFederationPanelProps) {
  const libraryApi = useLibraryApi()
  const [remotes, setRemotes] = useState<LibraryRemote[]>([])
  const [selectedRemoteId, setSelectedRemoteId] = useState('')
  const [draft, setDraft] = useState<RemoteDraft>(() => remoteToDraft(undefined, projectId))
  const [loading, setLoading] = useState(false)
  const [actionMessage, setActionMessage] = useState('')
  const [actionError, setActionError] = useState('')

  const selectedRemote = useMemo(
    () => remotes.find(remote => remote.id === selectedRemoteId),
    [remotes, selectedRemoteId],
  )

  const loadRemotes = useCallback(async () => {
    const response = await libraryApi.listRemotes()
    const nextRemotes = response.remotes || []
    setRemotes(nextRemotes)
    setSelectedRemoteId(current => {
      if (current && nextRemotes.some(remote => remote.id === current)) return current
      return nextRemotes[0]?.id || ''
    })
  }, [libraryApi])

  useEffect(() => {
    void loadRemotes().catch(error => {
      setActionError(getErrorMessage(error, 'Unable to load Remote Workspaces'))
    })
  }, [loadRemotes])

  useEffect(() => {
    setDraft(remoteToDraft(selectedRemote, projectId))
  }, [projectId, selectedRemote])

  async function saveRemote() {
    setActionMessage('')
    setActionError('')
    setLoading(true)
    try {
      const body = {
        name: draft.name.trim(),
        base_url: draft.base_url.trim(),
        default_project_id: draft.default_project_id.trim(),
        auth_method: draft.auth_method,
        credential_ref: draft.credential_ref.trim(),
        api_key: draft.api_key.trim(),
      }
      const saved = selectedRemote
        ? await libraryApi.updateRemote(selectedRemote.id, body)
        : await libraryApi.createRemote(body)
      setActionMessage(`${selectedRemote ? 'Updated' : 'Added'} Remote Workspace "${saved.name}".`)
      setDraft(remoteToDraft(saved, projectId))
      await loadRemotes()
      setSelectedRemoteId(saved.id)
    } catch (error) {
      setActionError(getErrorMessage(error, selectedRemote ? 'Unable to update Remote Workspace' : 'Unable to add Remote Workspace'))
    } finally {
      setLoading(false)
    }
  }

  async function probeRemote(remoteId: string) {
    setActionMessage('')
    setActionError('')
    setLoading(true)
    try {
      const response = await libraryApi.probeRemote(remoteId)
      await loadRemotes()
      setActionMessage(response.status === 'healthy' ? 'Remote library is healthy.' : `Remote library is unreachable: ${response.error || 'probe failed'}`)
    } catch (error) {
      setActionError(getErrorMessage(error, 'Unable to probe Remote Workspace'))
    } finally {
      setLoading(false)
    }
  }

  async function deleteRemote(remote: LibraryRemote) {
    if (!confirm(`Delete Remote Workspace "${remote.name}"? Sync links for this host will also be removed.`)) return
    setActionMessage('')
    setActionError('')
    setLoading(true)
    try {
      await libraryApi.deleteRemote(remote.id)
      setActionMessage(`Deleted Remote Workspace "${remote.name}".`)
      setSelectedRemoteId('')
      setDraft(remoteToDraft(undefined, projectId))
      await loadRemotes()
    } catch (error) {
      setActionError(getErrorMessage(error, 'Unable to delete Remote Workspace'))
    } finally {
      setLoading(false)
    }
  }

  function startNewRemote() {
    setSelectedRemoteId('')
    setDraft(remoteToDraft(undefined, projectId))
    setActionMessage('')
    setActionError('')
  }

  const canSave = draft.name.trim() && draft.base_url.trim()

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
        <button className="btn btn-secondary" type="button" onClick={startNewRemote}>
          New Remote
        </button>
        <button className="btn btn-secondary" type="button" onClick={() => void loadRemotes()} disabled={loading}>
          Refresh
        </button>
        {selectedRemote && (
          <>
            <button className="btn btn-secondary" type="button" onClick={() => void probeRemote(selectedRemote.id)} disabled={loading}>
              Probe Remote
            </button>
            <button className="btn btn-ghost" type="button" onClick={() => void deleteRemote(selectedRemote)} disabled={loading} style={{ color: 'var(--danger)' }}>
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
              onClick={() => setSelectedRemoteId(remote.id)}
            >
              <div className="settings-release-main">
                <span>{remote.name}</span>
                <small>{remote.base_url}</small>
                <small>
                  Project {remote.default_project_id || 'unmapped'} / checked {formatLibraryDateTime(remote.last_health_check)} / credentials {remoteCredentialLabel(remote)}
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

      <div className="project-redaction-rule-grid">
        <label className="project-redaction-rule-field">
          <span>Remote Name</span>
          <input className="settings-input" value={draft.name} onChange={event => setDraft(prev => ({ ...prev, name: event.target.value }))} placeholder="Team AKS" />
        </label>
        <label className="project-redaction-rule-field">
          <span>Base URL</span>
          <input className="settings-input" value={draft.base_url} onChange={event => setDraft(prev => ({ ...prev, base_url: event.target.value }))} placeholder="https://apexload.example.com" />
        </label>
        <label className="project-redaction-rule-field">
          <span>Default Project Mapping</span>
          <input className="settings-input" value={draft.default_project_id} onChange={event => setDraft(prev => ({ ...prev, default_project_id: event.target.value }))} placeholder="project-id" />
        </label>
        <label className="project-redaction-rule-field">
          <span>Auth Method</span>
          <select className="settings-select" value={draft.auth_method} onChange={event => setDraft(prev => ({ ...prev, auth_method: event.target.value }))}>
            <option value="service_account">Service Account</option>
            <option value="none">None</option>
          </select>
        </label>
        <label className="project-redaction-rule-field">
          <span>Credential Ref</span>
          <input className="settings-input" value={draft.credential_ref} onChange={event => setDraft(prev => ({ ...prev, credential_ref: event.target.value }))} placeholder="env:APEXLOAD_REMOTE_TOKEN" />
        </label>
        <label className="project-redaction-rule-field">
          <span>Rotate API Key</span>
          <input className="settings-input" type="password" value={draft.api_key} onChange={event => setDraft(prev => ({ ...prev, api_key: event.target.value }))} placeholder="Paste once; never returned" />
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
