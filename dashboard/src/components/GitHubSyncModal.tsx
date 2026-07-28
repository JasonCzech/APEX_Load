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
import { importDraftExpiryStatus } from './protectedImportSession'
import SecretBindingReview, {
  type PreparedSecretBindings,
  type SecretBindingReviewHandle,
} from './SecretBindingReview'
import { refreshedSecretBindingRequirements } from './secretBindingUtils'
import { useSourceSecretSessionLifecycle } from './sourceSecretSessionLifecycle'
import { type ImportedSaveResult } from './useScriptsAuthoringState'

type GitHubSyncModalProps = {
  mode: 'push' | 'import'
  config: GitHubConfigResponse
  script?: StoredScript
  projectId?: string
  onClose: () => void
  onPushed?: (response: GitHubPushResponse) => void
  onImported?: (result: ImportedSaveResult) => void
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
  const sourceSecretSessions = useSourceSecretSessionLifecycle(scriptApi.discardImportSecretSession)

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
  const [importPreparedSecrets, setImportPreparedSecrets] = useState<PreparedSecretBindings | null>(null)
  const [sourceSecretSessionId, setSourceSecretSessionId] = useState('')
  const [sourceSecretSessionExpiresAt, setSourceSecretSessionExpiresAt] = useState('')
  const [sourceSecretMigrationAllowed, setSourceSecretMigrationAllowed] = useState(false)
  const [importDraftExpiresAt, setImportDraftExpiresAt] = useState('')
  const [importDraftExpiryNow, setImportDraftExpiryNow] = useState(() => Date.now())

  // Push-only status.
  const [status, setStatus] = useState<GitHubScriptStatus | null>(null)
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const repositorySelectRef = useRef<HTMLSelectElement | null>(null)
  const secretBindingReviewRef = useRef<SecretBindingReviewHandle | null>(null)
  const importDraftRef = useRef<ScriptDefinition | null>(null)
  const importDraftGenerationRef = useRef(0)
  const modalOpenRef = useRef(true)
  const [importDraftGeneration, setImportDraftGeneration] = useState(0)
  const advanceImportDraftGeneration = useCallback(() => {
    const next = importDraftGenerationRef.current + 1
    importDraftGenerationRef.current = next
    setImportDraftGeneration(next)
    return next
  }, [])
  const importProjectIdRef = useRef(projectId)
  importProjectIdRef.current = projectId
  const repoFullNameRef = useRef(repoFullName)
  repoFullNameRef.current = repoFullName
  const branchRef = useRef(branch)
  branchRef.current = branch
  const pathRef = useRef(path)
  pathRef.current = path
  const browseRequestRef = useRef(0)
  const [parameterizationApplying, setParameterizationApplying] = useState(false)
  const titleId = useId()

  const closeModal = useCallback(() => {
    if (!modalOpenRef.current) return
    sourceSecretSessions.clear('import')
    modalOpenRef.current = false
    importDraftGenerationRef.current += 1
    browseRequestRef.current += 1
    onClose()
  }, [onClose, sourceSecretSessions])

  useEffect(() => () => {
    modalOpenRef.current = false
    importDraftGenerationRef.current += 1
    browseRequestRef.current += 1
  }, [])

  useModalDialog({ dialogRef, initialFocusRef: repositorySelectRef, onClose: closeModal })

  const selectedRepo = useMemo(() => repos.find(repo => repo.full_name === repoFullName), [repos, repoFullName])
  const importDraftExpiry = importDraftExpiryStatus(importDraftExpiresAt, importDraftExpiryNow)

  const clearImportReview = useCallback(() => {
    setImportDraft(null)
    importDraftRef.current = null
    setImportParameterization(null)
    setImportSecretBindingRequirements([])
    setImportPreparedSecrets(null)
    setSourceSecretSessionId('')
    setSourceSecretSessionExpiresAt('')
    setSourceSecretMigrationAllowed(false)
    setImportDraftExpiresAt('')
    setParameterizationApplying(false)
  }, [])

  const invalidateImportSource = useCallback(() => {
    sourceSecretSessions.clear('import')
    advanceImportDraftGeneration()
    browseRequestRef.current += 1
    clearImportReview()
    setLoading(false)
    setError('')
  }, [advanceImportDraftGeneration, clearImportReview, sourceSecretSessions])

  useEffect(() => {
    setImportDraftExpiryNow(Date.now())
    const expiry = Date.parse(importDraftExpiresAt)
    if (!Number.isFinite(expiry)) return
    const remaining = expiry - Date.now()
    if (remaining <= 0) return
    const timeout = window.setTimeout(
      () => setImportDraftExpiryNow(Date.now()),
      Math.min(remaining, 2_147_483_647),
    )
    return () => window.clearTimeout(timeout)
  }, [importDraftExpiresAt])

  const previousProjectIdRef = useRef(projectId)
  useEffect(() => {
    if (previousProjectIdRef.current === projectId) return
    previousProjectIdRef.current = projectId
    invalidateImportSource()
  }, [invalidateImportSource, projectId])

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
        if (repoFullNameRef.current !== initial) {
          invalidateImportSource()
          setPath('')
          setRepoFullName(initial)
        }
      } catch (err) {
        if (!cancelled) setError(getErrorMessage(err, 'Unable to list repositories'))
      }
    })()
    return () => { cancelled = true }
  }, [githubApi, config.default_repo, invalidateImportSource])

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
        const currentBranch = branchRef.current
        const nextBranch = names.includes(currentBranch) ? currentBranch : (selectedRepo.default_branch || names[0] || 'main')
        if (nextBranch !== currentBranch) {
          invalidateImportSource()
          setPath('')
          setBranch(nextBranch)
        }
      } catch (err) {
        if (!cancelled) setError(getErrorMessage(err, 'Unable to list branches'))
      }
    })()
    return () => { cancelled = true }
  }, [githubApi, invalidateImportSource, selectedRepo])

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
    const requestID = ++browseRequestRef.current
    const sourceRepoFullName = selectedRepo.full_name
    const sourceBranch = branchRef.current
    const requestIsCurrent = () => browseRequestRef.current === requestID
      && repoFullNameRef.current === sourceRepoFullName
      && branchRef.current === sourceBranch
    setError('')
    try {
      const response = await githubApi.getContents(selectedRepo.owner, selectedRepo.name, nextPath, sourceBranch)
      if (requestIsCurrent() && response.type === 'dir') {
        setEntries(response.entries || [])
        setBrowsePath(nextPath)
      }
    } catch (err) {
      if (requestIsCurrent()) setError(getErrorMessage(err, 'Unable to browse repository'))
    }
  }, [githubApi, selectedRepo])

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
      if (!modalOpenRef.current) return
      onPushed?.(response)
      closeModal()
    } catch (err) {
      if (modalOpenRef.current) setError(getErrorMessage(err, 'Push failed'))
    } finally {
      if (modalOpenRef.current) setLoading(false)
    }
  }

  async function saveImportDraft(
    draft: ScriptDefinition,
    targetProjectId: string | undefined,
    requestIsCurrent: () => boolean,
  ) {
    const saved = await scriptApi.createScript(draft, targetProjectId)
    // createScript is the persistence commit point. If the modal is cancelled
    // or its source changes while that request is in flight, still publish the
    // returned script so the caller can track and refresh the created record.
    // Silently dropping this response leaves a real script orphaned in the
    // destination library.
    onImported?.({
      scripts: [saved],
      ...(!requestIsCurrent() ? { stayInImportReview: true } : {}),
    })
    if (!requestIsCurrent()) return
    closeModal()
  }

  async function doImport() {
    if (!selectedRepo || !path.trim()) {
      setError('Select a script file to import.')
      return
    }
    const sourceGeneration = advanceImportDraftGeneration()
    const sourceProjectId = importProjectIdRef.current
    const sourceRepoFullName = selectedRepo.full_name
    const sourceOwner = selectedRepo.owner
    const sourceRepo = selectedRepo.name
    const sourcePath = path.trim()
    const sourceBranch = branchRef.current
    const requestIsCurrent = () => modalOpenRef.current
      && importDraftGenerationRef.current === sourceGeneration
      && importProjectIdRef.current === sourceProjectId
      && repoFullNameRef.current === sourceRepoFullName
      && pathRef.current.trim() === sourcePath
      && branchRef.current === sourceBranch
    sourceSecretSessions.clear('import')
    clearImportReview()
    setLoading(true)
    setError('')
    try {
      const result = await githubApi.importScript({
        project_id: sourceProjectId || '',
        owner: sourceOwner,
        repo: sourceRepo,
        path: sourcePath,
        ref: sourceBranch,
      })
      if (!requestIsCurrent()) {
        sourceSecretSessions.discardUnaccepted(sourceProjectId, result.source_secret_session_id)
        return
      }
      sourceSecretSessions.replace('import', sourceProjectId, result.source_secret_session_id)
      const draft = result.script as ScriptDefinition | undefined
      if (!draft) {
        setError('GitHub returned no importable script.')
        return
      }
      const parameterization = result.parameterization
      const secretBindingRequirements = result.secret_binding_requirements ?? []
      setImportDraftExpiresAt(result.import_draft_expires_at || '')
      if (secretBindingRequirements.length > 0 || (parameterization?.suggestions.length ?? 0) > 0) {
        // Keep references and optional parameterization visible in one review step.
        setImportDraft(draft)
        importDraftRef.current = draft
        setImportSecretBindingRequirements(secretBindingRequirements)
        setImportParameterization(parameterization && parameterization.suggestions.length > 0 ? parameterization : null)
        setSourceSecretSessionId(result.source_secret_session_id || '')
        setSourceSecretSessionExpiresAt(result.source_secret_session_expires_at || '')
        setSourceSecretMigrationAllowed(result.source_secret_migration_allowed === true)
        return
      }
      await saveImportDraft(draft, sourceProjectId, requestIsCurrent)
    } catch (err) {
      if (requestIsCurrent()) setError(getErrorMessage(err, 'Import failed'))
    } finally {
      if (requestIsCurrent()) setLoading(false)
    }
  }

  async function doSaveReviewedImport() {
    if (!importDraft) return
    if (importDraftExpiry?.expired) {
      setError('This credential-preserving GitHub import draft has expired. Re-import the script before saving.')
      return
    }
    const sourceGeneration = importDraftGenerationRef.current
    const sourceProjectId = importProjectIdRef.current
    const sourceDraft = importDraftRef.current || importDraft
    const requestIsCurrent = () => modalOpenRef.current
      && importDraftGenerationRef.current === sourceGeneration
      && importProjectIdRef.current === sourceProjectId
    setLoading(true)
    setError('')
    try {
      if (importSecretBindingRequirements.length > 0) {
        if (!secretBindingReviewRef.current || importPreparedSecrets?.valid === false) return
        const committed = await secretBindingReviewRef.current.commit()
        if (!committed) return
      }
      if (!requestIsCurrent()) return
      await saveImportDraft(importDraftRef.current || sourceDraft, sourceProjectId, requestIsCurrent)
    } catch (err) {
      if (requestIsCurrent()) setError(getErrorMessage(err, 'Failed to save imported script'))
    } finally {
      if (requestIsCurrent()) setLoading(false)
    }
  }

  async function doApplySecretBindings(bindings: SecretBindingSelection[]) {
    if (!importDraft) return
    const sourceGeneration = importDraftGenerationRef.current
    const sourceProjectId = importProjectIdRef.current
    setError('')
    try {
      const result = await scriptApi.applySecretBindings({ script: importDraft, bindings })
      if (importDraftGenerationRef.current !== sourceGeneration || importProjectIdRef.current !== sourceProjectId) {
        throw new Error('The imported draft changed while credentials were being prepared. Review the current draft and try again.')
      }
      setImportDraft(result.script)
      importDraftRef.current = result.script
      setImportSecretBindingRequirements(result.secret_binding_requirements ?? [])
      if (result.import_draft_expires_at) {
        setImportDraftExpiresAt(result.import_draft_expires_at)
      }
    } catch (error: unknown) {
      if (importDraftGenerationRef.current !== sourceGeneration || importProjectIdRef.current !== sourceProjectId) throw error
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
        if (event.target === event.currentTarget) closeModal()
      }}
    >
      <div ref={dialogRef} className="modal" role="dialog" aria-modal="true" aria-labelledby={titleId}>
        <div className="modal-header">
          <h3 id={titleId}>{title}</h3>
          <button type="button" className="btn btn-secondary btn-sm" onClick={closeModal}>Cancel</button>
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
            <select
              ref={repositorySelectRef}
              className="settings-select"
              value={repoFullName}
              onChange={event => {
                if (mode === 'import') invalidateImportSource()
                setPath('')
                setRepoFullName(event.target.value)
              }}
            >
              {repos.length === 0 && <option value="">No repositories available</option>}
              {repos.map(repo => <option key={repo.full_name} value={repo.full_name}>{repo.full_name}</option>)}
            </select>
          </label>

          <label className="project-redaction-rule-field">
            <span>Branch</span>
            <select
              className="settings-select"
              value={branch}
              onChange={event => {
                if (mode === 'import') invalidateImportSource()
                setPath('')
                setBranch(event.target.value)
              }}
            >
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
          ) : importDraft ? (
            <>
              {importSecretBindingRequirements.length > 0 && (
                <SecretBindingReview
                  ref={secretBindingReviewRef}
                  requirements={importSecretBindingRequirements}
                  reviewGeneration={`${importDraftGeneration}:${projectId || ''}`}
                  projectId={projectId}
                  sourceSecretSessionId={sourceSecretSessionId}
                  sourceSecretSessionExpiresAt={sourceSecretSessionExpiresAt}
                  sourceMigrationAllowed={sourceSecretMigrationAllowed}
                  disabled={loading || parameterizationApplying || importDraftExpiry?.expired === true}
                  onApply={doApplySecretBindings}
                  onPreparedChange={setImportPreparedSecrets}
                />
              )}
              {importParameterization && (
                <ParameterizationReview
                  parameterization={importParameterization}
                  script={importDraft}
                  reviewGeneration={`${importDraftGeneration}:${projectId || ''}`}
                  disabled={loading || importDraftExpiry?.expired === true}
                  onApplyingChange={setParameterizationApplying}
                  onApplied={(parameterizedScript, _diffs, _selectedIds, nextImportDraftExpiresAt) => {
                    secretBindingReviewRef.current?.preserveEditsForNextReviewGeneration()
                    advanceImportDraftGeneration()
                    setImportDraft(parameterizedScript)
                    importDraftRef.current = parameterizedScript
                    if (nextImportDraftExpiresAt) {
                      setImportDraftExpiresAt(nextImportDraftExpiresAt)
                    }
                  }}
                />
              )}
              {importSecretBindingRequirements.length === 0 && !importParameterization && (
                <p className="setting-desc" role="status">
                  All redacted values now use secret references. The imported script is ready to save.
                </p>
              )}
              {importDraftExpiry && (
                <p
                  className={importDraftExpiry.expired ? 'settings-error-inline' : 'setting-desc'}
                  role={importDraftExpiry.expired ? 'alert' : 'status'}
                >
                  {importDraftExpiry.label}
                </p>
              )}
            </>
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
                  onClick={() => {
                    if (entry.type === 'dir') {
                      void browse(entry.path)
                      return
                    }
                    invalidateImportSource()
                    setPath(entry.path)
                  }}
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
            <button className="btn btn-secondary" type="button" onClick={closeModal}>Cancel</button>
            {mode === 'push' ? (
              <button className="btn btn-primary" type="button" onClick={() => void doPush()} disabled={loading || !selectedRepo}>
                {loading ? 'Pushing…' : strategy === 'pr' ? 'Open Pull Request' : 'Commit'}
              </button>
            ) : importDraft ? (
              <button
                className="btn btn-primary"
                type="button"
                onClick={() => void doSaveReviewedImport()}
                disabled={importDraftExpiry?.expired === true || loading || parameterizationApplying || importPreparedSecrets?.valid === false}
              >
                {loading
                  ? 'Saving…'
                  : importSecretBindingRequirements.length > 0 && (importPreparedSecrets?.configureLaterCount ?? 0) > 0
                    ? `Save with ${importPreparedSecrets?.configureLaterCount} secret${importPreparedSecrets?.configureLaterCount === 1 ? '' : 's'} to configure`
                    : 'Save Script'}
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
