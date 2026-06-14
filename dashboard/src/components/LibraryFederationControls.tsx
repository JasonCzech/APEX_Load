import { type ReactNode } from 'react'
import { LibraryKind, LibrarySourceFilter } from '../types/library'
import { libraryStatusTone } from './libraryFederationUtils'
import { LIBRARY_SOURCE_OPTIONS, type UseLibraryFederationResult } from './useLibraryFederation'

function sourceDescription(kind: LibraryKind) {
  if (kind === 'result') return 'Move completed result archives between this controller and a remote team library.'
  if (kind === 'scenario') return 'Pull or promote reusable scenario definitions and referenced scripts between local and Remote Workspaces.'
  return 'Pull or promote authored scripts between local storage and Remote Workspaces.'
}

function remoteStatusLabel(status?: string) {
  return status || 'No Remote Workspace'
}

function credentialsLabel(ready: boolean) {
  return ready ? 'Credentials Ready' : 'Credentials Missing'
}

type LibraryFederationControlsProps = {
  kind: LibraryKind
  federation: UseLibraryFederationResult
  filterControls?: ReactNode
  onConfigureRemotes?: () => void
  summaryText?: string
}

export default function LibraryFederationControls({ kind, federation, filterControls, onConfigureRemotes, summaryText }: LibraryFederationControlsProps) {
  const {
    remotes,
    selectedRemoteId,
    setSelectedRemoteId,
    sourceFilter,
    setSourceFilter,
    projectMapping,
    setProjectMapping,
    includeDependencies,
    setIncludeDependencies,
    loading,
    actionMessage,
    actionError,
    diffMessage,
    selectedRemote,
    credentialsReady,
    actionBlockedReason,
    refreshCatalogs,
  } = federation
  const generalBlockedReason = selectedRemoteId ? actionBlockedReason : ''

  return (
    <div className="library-source-controls">
      <div className="library-source-header">
        <div>
          <h3 className="section-title">Library Sources</h3>
          <p className="library-toolbar-subtitle">{summaryText || sourceDescription(kind)}</p>
        </div>
        <div className="library-source-summary">
          {onConfigureRemotes ? (
            <button
              type="button"
              className={`script-health-token library-source-pill-link ${libraryStatusTone(selectedRemote?.status)}`}
              onClick={onConfigureRemotes}
              title="Manage Remote Workspaces in Settings > Integrations"
            >{remoteStatusLabel(selectedRemote?.status)}</button>
          ) : (
            <span className={`script-health-token ${libraryStatusTone(selectedRemote?.status)}`}>{remoteStatusLabel(selectedRemote?.status)}</span>
          )}
          {onConfigureRemotes ? (
            <button
              type="button"
              className={`script-health-token library-source-pill-link ${credentialsReady ? 'healthy' : 'danger'}`}
              onClick={onConfigureRemotes}
              title="Configure remote credentials in Settings > Integrations"
            >{credentialsLabel(credentialsReady)}</button>
          ) : (
            <span className={`script-health-token ${credentialsReady ? 'healthy' : 'danger'}`}>{credentialsLabel(credentialsReady)}</span>
          )}
        </div>
      </div>

      <div className="library-source-toolbar">
        {filterControls}
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
            {LIBRARY_SOURCE_OPTIONS.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
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

      {generalBlockedReason && <p className="settings-notice">{generalBlockedReason}</p>}
      {actionMessage && <p className="settings-notice">{actionMessage}</p>}
      {diffMessage && <p className="settings-notice">{diffMessage}</p>}
      {actionError && <p className="settings-error-inline">{actionError}</p>}
    </div>
  )
}
