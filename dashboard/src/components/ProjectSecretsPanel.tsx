import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'

import { useApi } from '../hooks/useApi'
import { ApiError } from '../hooks/apiClient'
import { useNavigationGuard, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { type ProjectSecretMetadata } from '../types/secrets'
import { getErrorMessage } from '../utils/errorUtils'
import { isValidSecretBindingName } from './secretBindingUtils'
import './ProjectSecretsPanel.css'

interface Props {
  projectId: string
  projectName: string
  canCreate: boolean
  canRotate: boolean
  onCountChange?: (count: number) => void
}

type SecretReconciliation = {
  operation: 'create' | 'rotate'
  name: string
  previousUpdatedAt?: string
  failureMessage: string
}

function formatTimestamp(value?: string): string {
  if (!value) return 'Not recorded'
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString()
}

function metadataCaughtUp(listed: ProjectSecretMetadata, committed: ProjectSecretMetadata): boolean {
  if (JSON.stringify(listed) === JSON.stringify(committed)) return true
  const listedTimestamp = Date.parse(listed.updated_at || '')
  const committedTimestamp = Date.parse(committed.updated_at || '')
  if (Number.isFinite(listedTimestamp) && Number.isFinite(committedTimestamp)) {
    return listedTimestamp > committedTimestamp
  }
  return false
}

export default function ProjectSecretsPanel({
  projectId,
  projectName,
  canCreate,
  canRotate,
  onCountChange,
}: Props) {
  const api = useApi()
  const idPrefix = useId().replace(/:/g, '')
  const newNameId = `${idPrefix}-project-secret-name`
  const newValueId = `${idPrefix}-project-secret-value`
  const nameErrorId = `${idPrefix}-project-secret-name-error`
  const activeProjectIdRef = useRef(projectId)
  activeProjectIdRef.current = projectId
  const loadRequestRef = useRef(0)
  const mutationSequenceRef = useRef(0)
  const activeMutationRef = useRef<{ id: number; projectId: string } | null>(null)
  const committedSecretsRef = useRef(new Map<string, ProjectSecretMetadata>())
  const [secrets, setSecrets] = useState<ProjectSecretMetadata[]>([])
  const [provider, setProvider] = useState('')
  const [writable, setWritable] = useState(false)
  const [createSupported, setCreateSupported] = useState(false)
  const [replaceSupported, setReplaceSupported] = useState(false)
  const [loading, setLoading] = useState(true)
  const [busyName, setBusyName] = useState('')
  const [newName, setNewName] = useState('')
  const [newValue, setNewValue] = useState('')
  const [rotateName, setRotateName] = useState('')
  const [rotateValue, setRotateValue] = useState('')
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const [loadError, setLoadError] = useState(false)
  const [reconciliation, setReconciliation] = useState<SecretReconciliation | null>(null)

  const load = useCallback(async (): Promise<ProjectSecretMetadata[] | null> => {
    const targetProjectId = projectId
    const requestId = ++loadRequestRef.current
    const requestIsCurrent = () => (
      activeProjectIdRef.current === targetProjectId
      && loadRequestRef.current === requestId
    )
    setLoading(true)
    setError('')
    setLoadError(false)
    try {
      const response = await api.listProjectSecrets(targetProjectId)
      if (!requestIsCurrent()) return null
      const listed = Array.isArray(response.secrets) ? response.secrets : []
      const next = [...listed]
      committedSecretsRef.current.forEach((secret, name) => {
        const listedSecret = listed.find(candidate => candidate.name === name)
        if (listedSecret && metadataCaughtUp(listedSecret, secret)) {
          committedSecretsRef.current.delete(name)
          return
        }
        const index = next.findIndex(candidate => candidate.name === name)
        if (index >= 0) next[index] = secret
        else next.push(secret)
      })
      next.sort((left, right) => left.name.localeCompare(right.name))
      setSecrets(next)
      setProvider(response.capabilities?.provider || '')
      setWritable(response.capabilities?.writable ?? false)
      setCreateSupported(response.capabilities?.create_supported ?? response.capabilities?.writable ?? false)
      setReplaceSupported(response.capabilities?.replace_supported ?? response.capabilities?.writable ?? false)
      onCountChange?.(next.length)
      return next
    } catch (err: unknown) {
      if (!requestIsCurrent()) return null
      // A transient metadata failure does not invalidate a catalog and provider
      // capability snapshot that already loaded successfully for this project.
      setError(getErrorMessage(err, 'Unable to load project secrets'))
      setLoadError(true)
      return null
    } finally {
      if (requestIsCurrent()) setLoading(false)
    }
  }, [api, onCountChange, projectId])

  useEffect(() => {
    loadRequestRef.current += 1
    setSecrets([])
    setProvider('')
    setWritable(false)
    setCreateSupported(false)
    setReplaceSupported(false)
    setLoading(true)
    activeMutationRef.current = null
    committedSecretsRef.current.clear()
    setBusyName('')
    setNewName('')
    setNewValue('')
    setRotateName('')
    setRotateValue('')
    setMessage('')
    setError('')
    setLoadError(false)
    setReconciliation(null)
  }, [projectId])

  useEffect(() => {
    void load()
  }, [load])

  const normalizedNewName = newName.trim().toUpperCase().replace(/\s+/g, '_')
  const nameError = useMemo(() => {
    if (!newName.trim()) return ''
    if (!isValidSecretBindingName(normalizedNewName)) return 'Use 1–249 uppercase letters, numbers, or underscores.'
    if (secrets.some(secret => secret.name === normalizedNewName)) return 'This secret already exists. Use Rotate to replace its value.'
    return ''
  }, [newName, normalizedNewName, secrets])

  const reconcileSecretOutcome = async (pending: SecretReconciliation): Promise<boolean> => {
    const refreshed = await load()
    if (activeProjectIdRef.current !== projectId || !refreshed) {
      setReconciliation(pending)
      setError(`${pending.failureMessage} The write outcome is uncertain and secret metadata could not be refreshed. Reconcile status before retrying.`)
      return false
    }
    const observed = refreshed.find(secret => secret.name === pending.name)
    const confirmed = pending.operation === 'create'
      ? Boolean(observed)
      : Boolean(observed && observed.updated_at && observed.updated_at !== pending.previousUpdatedAt)
    if (!confirmed) {
      setReconciliation(pending)
      setError(`${pending.failureMessage} Metadata was refreshed, but the ${pending.operation} outcome is still uncertain. Verify status before retrying.`)
      return false
    }
    setReconciliation(null)
    setError('')
    if (pending.operation === 'create') {
      setNewName('')
      setNewValue('')
    } else {
      setRotateName('')
      setRotateValue('')
    }
    setMessage(`${pending.name} is present with updated metadata after an uncertain response. The operation may have committed; do not retry it blindly.`)
    return true
  }

  const createSecret = async (): Promise<boolean> => {
    if (activeMutationRef.current || !normalizedNewName || !newValue || nameError || !canCreate || !writable || !createSupported) return false
    const targetProjectId = projectId
    const operation = { id: ++mutationSequenceRef.current, projectId: targetProjectId }
    activeMutationRef.current = operation
    setBusyName(normalizedNewName)
    setMessage('')
    setError('')
    setLoadError(false)
    try {
      const response = await api.createProjectSecrets(targetProjectId, [{ name: normalizedNewName, value: newValue }])
      if (activeProjectIdRef.current !== targetProjectId || activeMutationRef.current !== operation) return false
      const committed = response.secrets || (response.secret ? [response.secret] : [])
      if (committed.length > 0) {
        committed.forEach(secret => committedSecretsRef.current.set(secret.name, secret))
        setSecrets(current => {
          const ids = new Set(committed.map(secret => secret.name))
          const next = [...current.filter(secret => !ids.has(secret.name)), ...committed]
            .sort((left, right) => left.name.localeCompare(right.name))
          onCountChange?.(next.length)
          return next
        })
      }
      setNewName('')
      setNewValue('')
      setMessage(`${normalizedNewName} was created. Its value cannot be viewed from the dashboard.`)
      await load()
      return true
    } catch (err: unknown) {
      if (activeProjectIdRef.current !== targetProjectId || activeMutationRef.current !== operation) return false
      const failureMessage = getErrorMessage(err, 'Unable to create project secret')
      const payload = err instanceof ApiError ? err.payload as { created?: ProjectSecretMetadata[] } | undefined : undefined
      const committed = payload?.created?.find(secret => secret.name === normalizedNewName)
      if (committed) {
        committedSecretsRef.current.set(committed.name, committed)
        setSecrets(current => [...current.filter(secret => secret.name !== committed.name), committed].sort((left, right) => left.name.localeCompare(right.name)))
        setNewName('')
        setNewValue('')
        setReconciliation(null)
        setError('')
        setMessage(`${normalizedNewName} was created, but the server reported an audit persistence warning. Its metadata is shown; do not retry creation.`)
        await load()
        return true
      }
      if (!(err instanceof ApiError) || err.status === 408 || err.status >= 500) {
        return await reconcileSecretOutcome({ operation: 'create', name: normalizedNewName, failureMessage })
      }
      setError(failureMessage)
      return false
    } finally {
      if (activeMutationRef.current === operation) {
        activeMutationRef.current = null
        if (activeProjectIdRef.current === targetProjectId) setBusyName('')
      }
    }
  }

  const rotateSecret = async (name: string): Promise<boolean> => {
    const secret = secrets.find(candidate => candidate.name === name)
    if (activeMutationRef.current || !secret?.writable || !rotateValue || !canRotate || !writable || !replaceSupported) return false
    const targetProjectId = projectId
    const operation = { id: ++mutationSequenceRef.current, projectId: targetProjectId }
    activeMutationRef.current = operation
    setBusyName(name)
    setMessage('')
    setError('')
    setLoadError(false)
    try {
      const response = await api.rotateProjectSecret(targetProjectId, name, rotateValue)
      if (activeProjectIdRef.current !== targetProjectId || activeMutationRef.current !== operation) return false
      const committed = response.secret || response.secrets?.find(secret => secret.name === name)
      if (committed) {
        committedSecretsRef.current.set(committed.name, committed)
        setSecrets(current => current.map(secret => secret.name === name ? committed : secret))
      }
      setRotateName('')
      setRotateValue('')
      setMessage(`${name} was rotated. Its previous value is no longer available to APEX Load.`)
      await load()
      return true
    } catch (err: unknown) {
      if (activeProjectIdRef.current !== targetProjectId || activeMutationRef.current !== operation) return false
      const failureMessage = getErrorMessage(err, 'Unable to rotate project secret')
      if (!(err instanceof ApiError) || err.status === 408 || err.status >= 500) {
        return await reconcileSecretOutcome({ operation: 'rotate', name, previousUpdatedAt: secret.updated_at, failureMessage })
      }
      setError(failureMessage)
      return false
    } finally {
      if (activeMutationRef.current === operation) {
        activeMutationRef.current = null
        if (activeProjectIdRef.current === targetProjectId) setBusyName('')
      }
    }
  }

  const draftDirty = Boolean(newName.trim() || newValue || rotateValue)
  const draftDirtyRef = useRef(draftDirty)
  draftDirtyRef.current = draftDirty
  const saveDraftRef = useRef<() => Promise<boolean>>(async () => false)
  saveDraftRef.current = async () => {
    if ((newName.trim() || newValue) && !(await createSecret())) return false
    if (rotateName && rotateValue && !(await rotateSecret(rotateName))) return false
    return true
  }
  const discardDraftRef = useRef<() => void>(() => undefined)
  discardDraftRef.current = () => {
    setNewName('')
    setNewValue('')
    setRotateName('')
    setRotateValue('')
    setReconciliation(null)
    setError('')
  }
  const navigationGuard = useMemo<NavigationGuard>(() => ({
    id: `project-secrets-${projectId}`,
    label: 'Project secret draft',
    isDirty: () => draftDirtyRef.current,
    save: () => saveDraftRef.current(),
    discard: () => discardDraftRef.current(),
  }), [projectId])
  useNavigationGuard(navigationGuard, draftDirty)

  return (
    <div className="settings-group project-secrets-panel">
      <div className="settings-subsection">
        <div className="settings-manager-summary">
          <div>
            <span className="setting-label">Project Secrets</span>
            <span className="setting-desc">
              Write-only credentials for {projectName}. Values are encrypted by the configured provider and are never returned to the browser.
            </span>
          </div>
          <span className={`script-health-token ${writable ? 'healthy' : 'warning'}`}>
            {loading
              ? 'Checking provider…'
              : provider
                ? `${provider}${writable ? '' : ' · read-only'}`
                : 'provider unavailable'}
          </span>
        </div>

        {message && <p className="settings-notice" role="status">{message}</p>}
        {error && (
          <div>
            <p className="settings-error-inline" role="alert">{error}</p>
            {error.toLowerCase().includes('timed out') && (
              <p className="new-test-inline-note" role="status">
                Secret operation timed out. The request was canceled to avoid hanging on Creating....
                Confirm the controller is healthy, then retry.
              </p>
            )}
            {loadError && (
              <button
                className="btn btn-secondary btn-sm"
                type="button"
                disabled={loading || Boolean(busyName)}
                onClick={() => { void load() }}
              >
                Retry secret metadata
              </button>
            )}
            {reconciliation && (
              <button
                className="btn btn-secondary btn-sm"
                type="button"
                disabled={loading || Boolean(busyName)}
                onClick={() => { void reconcileSecretOutcome(reconciliation) }}
              >
                Reconcile secret status
              </button>
            )}
          </div>
        )}
        {loading && <p className="new-test-inline-note" role="status">Loading secret metadata...</p>}

        {!loading && secrets.length > 0 ? (
          <div className="project-secret-list">
            {secrets.map(secret => (
              <div className="project-secret-row" key={secret.name}>
                <div>
                  <strong>{secret.name}</strong>
                  <span>
                    {secret.provider || provider || 'project provider'}{secret.writable ? '' : ' · read-only'} · Updated {formatTimestamp(secret.updated_at)}
                  </span>
                  {secret.created_by && <span>Created by {secret.created_by}</span>}
                </div>
                {rotateName === secret.name ? (
                  <div className="project-secret-rotate">
                    <label htmlFor={`rotate-secret-${secret.name}`}>New value</label>
                    <input
                      id={`rotate-secret-${secret.name}`}
                      className="settings-input"
                      type="password"
                      value={rotateValue}
                      onChange={event => setRotateValue(event.target.value)}
                      disabled={Boolean(busyName)}
                      autoComplete="new-password"
                      placeholder="Enter replacement value"
                    />
                    <button
                      className="btn btn-primary btn-sm"
                      type="button"
                      onClick={() => { void rotateSecret(secret.name) }}
                      disabled={!secret.writable || !canRotate || !writable || !replaceSupported || !rotateValue || Boolean(busyName)}
                    >
                      {busyName === secret.name ? 'Rotating...' : 'Rotate'}
                    </button>
                    <button
                      className="btn btn-ghost btn-sm"
                      type="button"
                      disabled={Boolean(busyName)}
                      onClick={() => {
                        if (activeMutationRef.current) return
                        setRotateName('')
                        setRotateValue('')
                      }}
                    >
                      Cancel
                    </button>
                  </div>
                ) : (
                  <button
                    className="btn btn-secondary btn-sm"
                    type="button"
                    onClick={() => {
                      if (activeMutationRef.current) return
                      setRotateName(secret.name)
                      setRotateValue('')
                    }}
                    disabled={!secret.writable || !canRotate || !writable || !replaceSupported || Boolean(busyName)}
                    title={!secret.writable ? 'This secret is managed by a read-only provider and cannot be rotated here.' : undefined}
                  >
                    Rotate
                  </button>
                )}
              </div>
            ))}
          </div>
        ) : !loading ? (
          <p className="new-test-inline-note">No managed secrets yet. Scripts can still be saved with secret references and configured here later.</p>
        ) : null}
      </div>

      <div className="settings-subsection">
        <div className="settings-manager-summary">
          <div>
            <span className="setting-label">Create Secret</span>
            <span className="setting-desc">Creating is safe for editors because existing names cannot be overwritten. Rotation remains an administrator action.</span>
          </div>
        </div>
        <div className="project-secret-create">
          <label htmlFor={newNameId}>
            <span>Secret name</span>
            <input
              id={newNameId}
              className="settings-input"
              value={newName}
              onChange={event => setNewName(event.target.value)}
              disabled={Boolean(busyName)}
              aria-invalid={nameError ? 'true' : 'false'}
              aria-describedby={nameError ? nameErrorId : undefined}
              autoComplete="off"
              spellCheck={false}
              placeholder="ORDER_API_TOKEN"
            />
          </label>
          <label htmlFor={newValueId}>
            <span>Secret value</span>
            <input
              id={newValueId}
              className="settings-input"
              type="password"
              value={newValue}
              onChange={event => setNewValue(event.target.value)}
              disabled={Boolean(busyName)}
              autoComplete="new-password"
              placeholder="Enter once; it cannot be revealed later"
            />
          </label>
          <button
            className="btn btn-primary"
            type="button"
            onClick={() => { void createSecret() }}
            disabled={!canCreate || !writable || !createSupported || !normalizedNewName || !newValue || Boolean(nameError) || Boolean(busyName)}
          >
            {busyName && busyName === normalizedNewName ? 'Creating...' : 'Create Secret'}
          </button>
        </div>
        {nameError && <p id={nameErrorId} className="settings-error-inline" role="alert">{nameError}</p>}
        {!canCreate && <p className="new-test-inline-note">Current session can view secret metadata but cannot create values.</p>}
        {canCreate && writable && !createSupported && <p className="new-test-inline-note">The configured provider does not support creating secrets.</p>}
        {canRotate && writable && !replaceSupported && <p className="new-test-inline-note">The configured provider does not support rotating secrets.</p>}
        {canCreate && !canRotate && <p className="new-test-inline-note">Editors may create missing secrets. Rotation requires a project or system administrator.</p>}
      </div>
    </div>
  )
}
