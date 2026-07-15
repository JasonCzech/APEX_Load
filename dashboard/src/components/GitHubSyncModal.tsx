import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'

import { useGitHubApi } from '../hooks/api/useGitHubApi'
import { useScriptApi } from '../hooks/api/useScriptApi'
import { useModalDialog } from '../hooks/useModalDialog'
import {
  GitHubConfigResponse,
  GitHubContentEntry,
  GitHubPushResponse,
  GitHubRepo,
  GitHubScriptStatus,
  StoredScript,
} from '../types'
import {
  type Parameterization,
  type SecretBindingRequirement,
  type SecretBindingSelection,
} from '../types/scriptImports'
import { ScriptDefinition } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'
import ParameterizationReview from './ParameterizationReview'
import SecretBindingReview from './SecretBindingReview'
import { refreshedSecretBindingRequirements } from './secretBindingUtils'

type GitHubSyncModalProps = {
  mode: 'push' | 'import'
  config: GitHubConfigResponse
  script?: StoredScript
  projectId?: string
  onClose: () => void
  onPushed?: (response: GitHubPushResponse) => void
  onImported?: (saved: StoredScript) => void
}

const STATUS_TONE: Record<string, string> = {
  synced: 'healthy',
  synced_local: 'healthy',
  not_linked: '',
  local_drift: 'warning',
  remote_drift: 'warning',
  diverged: 'danger',
  remote_missing: 'danger',
}

function statusLabel(status: string): string {
  return status.replace(/_/g, ' ')
}

export default function GitHubSyncModal({ mode, config, script, projectId, onClose, onPushed, onImported }: GitHubSyncModalProps) {
  const githubApi = useGitHubApi()
  const scriptApi = useScriptApi()

  const [repos, setRepos] = useState<GitHubRepo[]>([])
  const [repoFullName, setRepoFullName] = useState('')
  const [branch, setBranch] = useState(config.default_branch || 'main')
  const [branches, setBranches] = useState<string[]>([])
  const [path, setPath] = useState('')
  const [message, setMessage] = useState('')
  const [strategy, setStrategy] = useState<'commit' | 'pr'>('commit')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  // Import-only browse state.
  const [browsePath, setBrowsePath] = useState('')
  const [entries, setEntries] = useState<GitHubContentEntry[]>([])
  // Import-only review state: a fetched draft pending secret binding and/or parameterization review.
  const [importDraft, setImportDraft] = useState<ScriptDefinition | null>(null)
  const [importParameterization, setImportParameterization] = useState<Parameterization | null>(null)
  const [importSecretBindingRequirements, setImportSecretBindingRequirements] = useState<SecretBindingRequirement[]>([])

  // Push-only status.
  const [status, setStatus] = useState<GitHubScriptStatus | null>(null)
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const repositorySelectRef = useRef<HTMLSelectElement | null>(null)
  const titleId = useId()

  useModalDialog({ dialogRef, initialFocusRef: repositorySelectRef, onClose })

  const selectedRepo = useMemo(() => repos.find(repo => repo.full_name === repoFullName), [repos, repoFullName])

  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const response = await githubApi.listRepos()
        if (cancelled) return
        setRepos(response.repos)
        const initial = config.default_repo && response.repos.some(r => r.full_name === config.default_repo)
          ? config.default_repo
          : response.repos[0]?.full_name || ''
        setRepoFullName(initial)
      } catch (err) {
        if (!cancelled) setError(getErrorMessage(err, 'Unable to list repositories'))
      }
    })()
    return () => { cancelled = true }
  }, [githubApi, config.default_repo])

  // Load branches when the repo changes.
  useEffect(() => {
    if (!selectedRepo) return
    let cancelled = false
    void (async () => {
      try {
        const response = await githubApi.listBranches(selectedRepo.owner, selectedRepo.name)
        if (cancelled) return
        const names = response.branches.map(b => b.name)
        setBranches(names)
        setBranch(prev => (names.includes(prev) ? prev : (selectedRepo.default_branch || names[0] || 'main')))
      } catch (err) {
        if (!cancelled) setError(getErrorMessage(err, 'Unable to list branches'))
      }
    })()
    return () => { cancelled = true }
  }, [githubApi, selectedRepo])

  // Push: load the current sync status for the script.
  useEffect(() => {
    if (mode !== 'push' || !script) return
    let cancelled = false
    void (async () => {
      try {
        const response = await githubApi.scriptStatus(script.id)
        if (!cancelled) setStatus(response)
      } catch {
        // status is advisory; ignore failures
      }
    })()
    return () => { cancelled = true }
  }, [githubApi, mode, script])

  const browse = useCallback(async (nextPath: string) => {
    if (!selectedRepo) return
    setError('')
    try {
      const response = await githubApi.getContents(selectedRepo.owner, selectedRepo.name, nextPath, branch)
      if (response.type === 'dir') {
        setEntries(response.entries || [])
        setBrowsePath(nextPath)
      }
    } catch (err) {
      setError(getErrorMessage(err, 'Unable to browse repository'))
    }
  }, [githubApi, selectedRepo, branch])

  // Import: refresh the directory listing when repo/branch change.
  useEffect(() => {
    if (mode !== 'import' || !selectedRepo) return
    void browse('')
  }, [mode, selectedRepo, branch, browse])

  async function doPush() {
    if (!script || !selectedRepo) return
    setLoading(true)
    setError('')
    setNotice('')
    try {
      const response = await githubApi.pushScript(script.id, {
        owner: selectedRepo.owner,
        repo: selectedRepo.name,
        branch,
        path: path.trim() || undefined,
        message: message.trim() || undefined,
        strategy,
      })
      onPushed?.(response)
      onClose()
    } catch (err) {
      setError(getErrorMessage(err, 'Push failed'))
    } finally {
      setLoading(false)
    }
  }

  async function saveImportDraft(draft: ScriptDefinition) {
    const saved = await scriptApi.createScript(draft, projectId)
    onImported?.(saved)
    onClose()
  }

  async function doImport() {
    if (!selectedRepo || !path.trim()) {
      setError('Select a script file to import.')
      return
    }
    setLoading(true)
    setError('')
    try {
      const result = await githubApi.importScript({
        project_id: projectId || '',
        owner: selectedRepo.owner,
        repo: selectedRepo.name,
        path: path.trim(),
        ref: branch,
      })
      const draft = result.script as ScriptDefinition | undefined
      if (!draft) {
        setError('GitHub returned no importable script.')
        return
      }
      const parameterization = result.parameterization
      const secretBindingRequirements = result.secret_binding_requirements ?? []
      if (secretBindingRequirements.length > 0 || (parameterization?.suggestions.length ?? 0) > 0) {
        // Resolve redacted values before optional parameterization review and save.
        setImportDraft(draft)
        setImportSecretBindingRequirements(secretBindingRequirements)
        setImportParameterization(parameterization && parameterization.suggestions.length > 0 ? parameterization : null)
        return
      }
      await saveImportDraft(draft)
    } catch (err) {
      setError(getErrorMessage(err, 'Import failed'))
    } finally {
      setLoading(false)
    }
  }

  async function doSaveReviewedImport() {
    if (!importDraft) return
    if (importSecretBindingRequirements.length > 0) {
      setError('Resolve all redacted values before saving the script.')
      return
    }
    setLoading(true)
    setError('')
    try {
      await saveImportDraft(importDraft)
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to save imported script'))
    } finally {
      setLoading(false)
    }
  }

  async function doApplySecretBindings(bindings: SecretBindingSelection[]) {
    if (!importDraft) return
    setError('')
    try {
      const result = await scriptApi.applySecretBindings({ script: importDraft, bindings })
      setImportDraft(result.script)
      setImportSecretBindingRequirements(result.secret_binding_requirements ?? [])
    } catch (error: unknown) {
      const refreshed = refreshedSecretBindingRequirements(error)
      if (refreshed) setImportSecretBindingRequirements(refreshed)
      throw error
    }
  }

  const title = mode === 'push' ? `Push "${script?.name || 'script'}" to GitHub` : 'Import script from GitHub'

  return (
    <div
      className="modal-overlay"
      role="presentation"
      onPointerDown={event => {
        if (event.target === event.currentTarget) onClose()
      }}
    >
      <div ref={dialogRef} className="modal" role="dialog" aria-modal="true" aria-labelledby={titleId}>
        <div className="modal-header">
          <h3 id={titleId}>{title}</h3>
          <button type="button" className="btn btn-secondary btn-sm" onClick={onClose}>Cancel</button>
        </div>
        <div className="monitor-body">
          {mode === 'push' && status && (
            <p className="setting-desc">
              Sync status:{' '}
              <span className={`script-health-token ${STATUS_TONE[status.status] ?? ''}`}>{statusLabel(status.status)}</span>
              {status.html_url && (
                <> · <a href={status.html_url} target="_blank" rel="noreferrer">View on GitHub</a></>
              )}
            </p>
          )}

          <label className="project-redaction-rule-field">
            <span>Repository</span>
            <select ref={repositorySelectRef} className="settings-select" value={repoFullName} onChange={event => setRepoFullName(event.target.value)}>
              {repos.length === 0 && <option value="">No repositories available</option>}
              {repos.map(repo => <option key={repo.full_name} value={repo.full_name}>{repo.full_name}</option>)}
            </select>
          </label>

          <label className="project-redaction-rule-field">
            <span>Branch</span>
            <select className="settings-select" value={branch} onChange={event => setBranch(event.target.value)}>
              {!branches.includes(branch) && <option value={branch}>{branch}</option>}
              {branches.map(name => <option key={name} value={name}>{name}</option>)}
            </select>
          </label>

          {mode === 'push' ? (
            <>
              <label className="project-redaction-rule-field">
                <span>File Path</span>
                <input
                  className="settings-input"
                  value={path}
                  onChange={event => setPath(event.target.value)}
                  placeholder={config.path_template || 'apexload/scripts/{project}/{ref_key}.json'}
                />
              </label>
              <label className="project-redaction-rule-field">
                <span>Commit Message</span>
                <input
                  className="settings-input"
                  value={message}
                  onChange={event => setMessage(event.target.value)}
                  placeholder={`Update ${script?.name || 'script'} via APEX Load`}
                />
              </label>
              <div className="settings-inline-actions" role="radiogroup" aria-label="Write strategy">
                <label><input type="radio" name="gh-strategy" checked={strategy === 'commit'} onChange={() => setStrategy('commit')} /> Direct commit</label>
                <label><input type="radio" name="gh-strategy" checked={strategy === 'pr'} onChange={() => setStrategy('pr')} /> Pull request</label>
              </div>
            </>
          ) : importDraft && importSecretBindingRequirements.length > 0 ? (
            <SecretBindingReview
              requirements={importSecretBindingRequirements}
              onApply={doApplySecretBindings}
            />
          ) : importDraft && importParameterization ? (
            <ParameterizationReview
              parameterization={importParameterization}
              script={importDraft}
              onApplied={parameterizedScript => setImportDraft(parameterizedScript)}
            />
          ) : importDraft ? (
            <p className="setting-desc" role="status">
              All redacted values now use secret references. The imported script is ready to save.
            </p>
          ) : (
            <div className="settings-release-list" style={{ maxHeight: 240, overflow: 'auto' }}>
              {browsePath && (
                  <button type="button" className="settings-release-row" aria-label="Browse parent directory" onClick={() => void browse(browsePath.split('/').slice(0, -1).join(''))}>
                  <div className="settings-release-main"><span>⬆ ..</span><small>{browsePath}</small></div>
                </button>
              )}
              {entries.map(entry => (
                    <button
                      aria-label={`Browse ${entry.path}`}
                  key={entry.path}
                  type="button"
                  className={`settings-release-row ${path === entry.path ? 'active' : ''}`}
                  onClick={() => (entry.type === 'dir' ? void browse(entry.path) : setPath(entry.path))}
                >
                  <div className="settings-release-main">
                    <span>{entry.type === 'dir' ? '📁' : '📄'} {entry.name}</span>
                    <small>{entry.path}</small>
                  </div>
                </button>
              ))}
              {entries.length === 0 && <p className="setting-desc">No files in this folder.</p>}
            </div>
          )}

          {notice && <p className="settings-notice" role="status" aria-live="polite">{notice}</p>}
          {error && <p className="settings-error-inline" role="alert">{error}</p>}

          <div className="settings-inline-actions">
            <button className="btn btn-secondary" type="button" onClick={onClose}>Cancel</button>
            {mode === 'push' ? (
              <button className="btn btn-primary" type="button" onClick={() => void doPush()} disabled={loading || !selectedRepo}>
                {loading ? 'Pushing…' : strategy === 'pr' ? 'Open Pull Request' : 'Commit'}
              </button>
            ) : importDraft ? (
              <button
                className="btn btn-primary"
                type="button"
                onClick={() => void doSaveReviewedImport()}
                disabled={loading || importSecretBindingRequirements.length > 0}
              >
                {loading ? 'Saving…' : 'Save Script'}
              </button>
            ) : (
              <button className="btn btn-primary" type="button" onClick={() => void doImport()} disabled={loading || !path.trim()}>
                {loading ? 'Importing…' : 'Import & Save'}
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
