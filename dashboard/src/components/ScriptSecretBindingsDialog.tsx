import DialogLayer from './ux/DialogLayer'
import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

import { useApi } from '../hooks/useApi'
import { type ProjectSecretMetadata, type ProjectSecretStatus, type ProjectSecretUsage, type ScriptSecretBinding } from '../types/secrets'
import { type ScriptDefinition } from '../types/scripts'
import type { ScriptAuthoringBaseline } from '../types/storedContent'
import { getErrorMessage } from '../utils/errorUtils'
import { useModalDialog } from '../hooks/useModalDialog'
import { dialogBackdropProps } from './dialogBackdropProps'
import { collisionSafeSecretName, MAX_SECRET_BINDING_NAME_LENGTH, secretBindingDisplayNames } from './secretBindingUtils'
import { scriptSecretReferenceSummaries } from './scriptSecretReferenceModel'
import SecretReferenceValueControl from './SecretReferenceValueControl'
import './ScriptSecretBindingsDialog.css'

interface Props {
  scriptId: string
  script: ScriptDefinition
  projectId: string
  applicationId?: string
  scriptName: string
  reconciliation?: ReactNode
  rebindDisabled?: boolean
  authoringBaseline?: ScriptAuthoringBaseline | null
  onClose: () => void
  onConfigured?: () => void | Promise<void>
  onRebound: (
    script: ScriptDefinition,
    changedLocations: string[],
    outcome?: { createdSecretName?: string },
  ) => void | Promise<void>
}

type BindingRow = ScriptSecretBinding & { usage?: ProjectSecretUsage }

function dedicatedBaseName(name: string, scriptName: string, scriptId: string): string {
  const scriptPart = scriptName.trim().toUpperCase().replace(/[^A-Z0-9]+/g, '_').replace(/^_+|_+$/g, '')
    || scriptId.toUpperCase().replace(/[^A-Z0-9]+/g, '_').slice(0, 12)
    || 'SCRIPT'
  const boundedScriptPart = scriptPart.slice(0, Math.max(1, MAX_SECRET_BINDING_NAME_LENGTH - 2))
  const suffix = `_${boundedScriptPart}`
  const prefix = name.slice(0, Math.max(1, MAX_SECRET_BINDING_NAME_LENGTH - suffix.length))
  return `${prefix}${suffix}`.slice(0, MAX_SECRET_BINDING_NAME_LENGTH)
}

export default function ScriptSecretBindingsDialog({
  scriptId,
  script,
  projectId,
  applicationId,
  scriptName,
  reconciliation,
  rebindDisabled: rebindDisabledByOwner = false,
  authoringBaseline,
  onClose,
  onConfigured,
  onRebound,
}: Props) {
  const api = useApi()
  const rebindDisabled = rebindDisabledByOwner || !authoringBaseline
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const closeRef = useRef<HTMLButtonElement | null>(null)
  const titleId = useId()
  const descriptionId = useId()
  const requestGenerationRef = useRef(0)
  const mutationScopeRef = useRef({ scriptId, projectId, applicationId, script })
  if (mutationScopeRef.current.scriptId !== scriptId || mutationScopeRef.current.projectId !== projectId
    || mutationScopeRef.current.applicationId !== applicationId || mutationScopeRef.current.script !== script) {
    mutationScopeRef.current = { scriptId, projectId, applicationId, script }
  }
  const mountedRef = useRef(true)
  useEffect(() => { mountedRef.current = true; return () => { mountedRef.current = false } }, [])
  const [bindings, setBindings] = useState<BindingRow[]>([])
  const [vaultSecrets, setVaultSecrets] = useState<ProjectSecretMetadata[]>([])
  const [values, setValues] = useState<Record<string, string>>({})
  const [reuseNames, setReuseNames] = useState<Record<string, string>>({})
  const [shareAcknowledged, setShareAcknowledged] = useState<Record<string, boolean>>({})
  const [loading, setLoading] = useState(true)
  const [busyName, setBusyName] = useState('')
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  useLayoutEffect(() => {
    setValues({}); setReuseNames({}); setShareAcknowledged({}); setBusyName(''); setMessage('')
  }, [scriptId, projectId, applicationId])
  useLayoutEffect(() => { setBusyName('') }, [script])
  useModalDialog({ dialogRef, initialFocusRef: closeRef, onClose })

  const draftSummaries = useMemo(() => scriptSecretReferenceSummaries(script), [script])
  const displayNames = useMemo(
    () => secretBindingDisplayNames(bindings.map(binding => binding.name), script),
    [bindings, script],
  )
  const displayNameFor = (name: string) => displayNames.get(name) || name

  const load = useCallback(async () => {
    const requestGeneration = ++requestGenerationRef.current
    setLoading(true)
    setError('')
    try {
      const names = draftSummaries.map(binding => binding.name)
      const [savedStatus, projectStatuses, vault, projectVault] = await Promise.all([
        api.getScriptSecretStatus(scriptId, { details: true }),
        applicationId ? api.getApplicationSecretStatuses(applicationId, names) : api.getProjectSecretStatuses(projectId, names),
        applicationId ? api.listApplicationSecrets(applicationId) : api.listProjectSecrets(projectId),
        applicationId ? api.listProjectSecrets(projectId) : Promise.resolve(null),
      ])
      const usages = await Promise.all(names.map(name => applicationId
        ? api.getApplicationSecretUsage(applicationId, name, { limit: 50 })
        : api.getProjectSecretUsage(projectId, name, { limit: 50 })))
      if (requestGenerationRef.current !== requestGeneration) return
      const statusByName = new Map<string, ProjectSecretStatus>(projectStatuses.secrets.map(status => [status.name, status]))
      const savedBindingByName = new Map((savedStatus.bindings || []).map(binding => [binding.name, binding]))
      const usageByName = new Map(usages.map(usage => [usage.name, { ...usage, consumers: Array.isArray(usage.consumers) ? usage.consumers : [] }]))
      setBindings(draftSummaries.map(summary => {
        const status = statusByName.get(summary.name)
        const savedBinding = savedBindingByName.get(summary.name)
        return {
          name: summary.name,
          configured: status?.configured ?? savedBinding?.configured ?? false,
          provider: status?.provider || savedBinding?.provider,
          writable: status?.writable ?? savedBinding?.writable,
          source_scope: status?.source_scope,
          occurrence_count: summary.occurrence_count,
          locations: summary.locations,
          usage: usageByName.get(summary.name),
        }
      }))
      const available = [...(vault.secrets || [])]
      for (const secret of projectVault?.secrets || []) {
        if (!available.some(candidate => candidate.name === secret.name)) available.push(secret)
      }
      setVaultSecrets(available.sort((left, right) => left.name.localeCompare(right.name)))
    } catch (cause: unknown) {
      if (requestGenerationRef.current !== requestGeneration) return
      setError(getErrorMessage(cause, 'Unable to load script secret bindings.'))
    } finally {
      if (requestGenerationRef.current === requestGeneration) setLoading(false)
    }
  }, [api, applicationId, draftSummaries, projectId, scriptId])

  useEffect(() => {
    void load()
    return () => { requestGenerationRef.current += 1 }
  }, [load])

  const createExactValue = async (binding: BindingRow) => {
    const value = values[binding.name] || ''
    if (!value || busyName) return
    const scope = mutationScopeRef.current
    const current = () => mountedRef.current && mutationScopeRef.current === scope
    if ((binding.usage?.consumer_count || 0) > 1 && !shareAcknowledged[binding.name]) {
      setError('Confirm the shared impact before creating one value for every consuming script.')
      return
    }
    setBusyName(binding.name)
    setError('')
    setMessage('')
    try {
      const create = applicationId ? api.createApplicationSecrets : api.createProjectSecrets
      await create(applicationId || projectId, [{
        name: binding.name,
        value,
        expected_usage: binding.usage ? {
          usage_revision: binding.usage.usage_revision,
          ...((binding.usage.consumer_count === 1 && binding.usage.consumers[0]?.script_id === scriptId)
            ? { exclusive_script_id: scriptId }
            : {}),
        } : undefined,
      }])
      if (!current()) return
      setValues(current => ({ ...current, [binding.name]: '' }))
      setMessage(`${displayNameFor(binding.name)} is configured for the exact reference used by this script. The value cannot be viewed.`)
      await load()
      if (!current()) return
      await onConfigured?.()
    } catch (cause: unknown) {
      if (current()) setError(getErrorMessage(cause, `Unable to create the ${applicationId ? 'application' : 'project'} secret.`))
    } finally {
      if (current()) setBusyName('')
    }
  }

  const applyRebind = async (binding: BindingRow, newName: string) => {
    if (!newName || newName === binding.name || busyName || rebindDisabled) return
    const scope = mutationScopeRef.current
    const current = () => mountedRef.current && mutationScopeRef.current === scope
    setBusyName(binding.name)
    setError('')
    setMessage('')
    try {
      const response = await api.rebindScriptSecretReference(script, binding.name, newName, authoringBaseline!)
      if (!current()) return
      await onRebound(response.script, response.changed_locations)
      if (!current()) return
      setMessage(`Rebound ${response.changed_count} occurrence${response.changed_count === 1 ? '' : 's'} from ${displayNameFor(binding.name)} to ${newName}.`)
      onClose()
    } catch (cause: unknown) {
      if (current()) setError(getErrorMessage(cause, 'Unable to rebind the script secret reference.'))
    } finally {
      if (current()) setBusyName('')
    }
  }

  const allocateDedicatedName = async (binding: BindingRow, current: () => boolean): Promise<string> => {
    const base = dedicatedBaseName(binding.name, scriptName, scriptId)
    for (let ordinal = 1; ordinal <= 32; ordinal += 1) {
      if (!current()) throw new DOMException('Dialog ownership changed', 'AbortError')
      const candidate = ordinal === 1 ? base : collisionSafeSecretName(base, ordinal)
      const [status, usage] = await Promise.all([
        applicationId ? api.getApplicationSecretStatuses(applicationId, [candidate]) : api.getProjectSecretStatuses(projectId, [candidate]),
        applicationId ? api.getApplicationSecretUsage(applicationId, candidate, { limit: 1 }) : api.getProjectSecretUsage(projectId, candidate, { limit: 1 }),
      ])
      if (!status.secrets[0]?.configured && usage.consumer_count === 0) return candidate
    }
    throw new Error('Unable to allocate a collision-safe script-specific secret name.')
  }

  const createDedicatedAndRebind = async (binding: BindingRow) => {
    const value = values[binding.name] || ''
    if (!value || busyName || rebindDisabled) return
    const scope = mutationScopeRef.current
    const current = () => mountedRef.current && mutationScopeRef.current === scope
    setBusyName(binding.name)
    setError('')
    setMessage('')
    let createdName = ''
    let valueCreated = false
    try {
      createdName = await allocateDedicatedName(binding, current)
      if (!current()) return
      const usage = applicationId
        ? await api.getApplicationSecretUsage(applicationId, createdName, { limit: 1 })
        : await api.getProjectSecretUsage(projectId, createdName, { limit: 1 })
      if (!current()) return
      const create = applicationId ? api.createApplicationSecrets : api.createProjectSecrets
      await create(applicationId || projectId, [{
        name: createdName,
        value,
        expected_usage: { usage_revision: usage.usage_revision },
      }])
      valueCreated = true
      if (!current()) return
      const response = await api.rebindScriptSecretReference(script, binding.name, createdName, authoringBaseline!)
      if (!current()) return
      await onRebound(response.script, response.changed_locations, { createdSecretName: createdName })
      if (!current()) return
      setValues(current => ({ ...current, [binding.name]: '' }))
      await onConfigured?.()
      if (!current()) return
      onClose()
    } catch (cause: unknown) {
      if (!current()) return
      const base = getErrorMessage(cause, 'Unable to create a dedicated script secret.')
      setError(valueCreated
        ? `${createdName} was created, but the script rebind/save did not complete. The value remains as an unused vault entry. ${base}`
        : base)
    } finally {
      if (current()) setBusyName('')
    }
  }

  return createPortal((
    <DialogLayer><div className="modal-overlay script-secret-bindings-overlay" {...dialogBackdropProps(onClose, 'Close secret bindings')}>
      <div
        ref={dialogRef}
        className="modal modal-wide script-secret-bindings-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={descriptionId}
      >
        <div className="modal-header">
          <div>
            <h3 id={titleId}>Secret Bindings · {scriptName}</h3>
            <p id={descriptionId} className="page-subtitle">
              Script references select write-only values from the {applicationId ? 'application' : 'project'} vault. Reuse a name only when scripts should share one credential.
            </p>
          </div>
          <button ref={closeRef} className="btn btn-sm btn-ghost" type="button" onClick={onClose}>Close</button>
        </div>

        {message && <p className="settings-notice" role="status">{message}</p>}
        {error && <p className="settings-error-inline" role="alert">{error}</p>}
        {reconciliation}
        {loading && <p role="status">Loading secret bindings…</p>}
        {!loading && bindings.length === 0 && <p>This script does not reference any secrets.</p>}

        <div className="script-secret-bindings-list">
          {bindings.map((binding, index) => {
            const shared = (binding.usage?.consumer_count || 0) > 1
            const reuseName = reuseNames[binding.name] || ''
            const displayName = displayNameFor(binding.name)
            return (
              <section className="script-secret-binding-row" key={binding.name}>
                <div className="script-secret-binding-summary">
                  <div>
                    <strong>{displayName}</strong>
                    <span className={`script-health-token ${binding.configured ? 'healthy' : 'warning'}`}>
                      {binding.configured ? 'Configured' : 'Missing value'}
                    </span>
                  </div>
                  <p>
                    {binding.occurrence_count} occurrence{binding.occurrence_count === 1 ? '' : 's'} in this draft ·{' '}
                    {binding.usage?.consumer_count ?? 0} consuming script{binding.usage?.consumer_count === 1 ? '' : 's'}
                    {binding.provider ? ` · ${binding.provider}` : ''}
                    {binding.source_scope ? ` · ${binding.source_scope} scope` : ''}
                  </p>
                  {shared && (
                    <p className="script-secret-shared-warning">
                      This name is shared by {binding.usage?.consumer_count} scripts. Creating or rotating its value affects all of them.
                    </p>
                  )}
                  <details>
                    <summary>Affected locations</summary>
                    <ul>{binding.locations.map(location => <li key={location}><code>{location}</code></li>)}</ul>
                  </details>
                  {binding.usage && binding.usage.consumers.length > 0 && (
                    <details>
                      <summary>Consuming scripts</summary>
                      <ul>{binding.usage.consumers.map(consumer => (
                        <li key={consumer.script_id}>{consumer.script_name} · {consumer.reference_count} reference{consumer.reference_count === 1 ? '' : 's'}</li>
                      ))}</ul>
                    </details>
                  )}
                </div>

                {!binding.configured && (
                  <div className="secret-binding-review-control script-secret-value-control">
                    <SecretReferenceValueControl
                      id={`${titleId.replace(/:/g, '')}-binding-${index}`}
                      name={binding.name}
                      value={values[binding.name] || ''}
                      onValueChange={value => setValues(current => ({ ...current, [binding.name]: value }))}
                      nameReadOnly
                      nameLabel="Internal script reference"
                      valueLabel="Secret Value"
                      valueDisabled={busyName === binding.name}
                      valuePlaceholder="Enter once; it cannot be revealed later"
                      collapseReference
                    />
                    {shared && (
                      <label className="script-secret-impact-acknowledgement">
                        <input
                          type="checkbox"
                          checked={shareAcknowledged[binding.name] === true}
                          onChange={event => setShareAcknowledged(current => ({ ...current, [binding.name]: event.target.checked }))}
                        />
                        <span>Use this value for all {binding.usage?.consumer_count} consuming scripts</span>
                      </label>
                    )}
                    <div className="settings-inline-actions">
                      <button
                        className="btn btn-primary btn-sm"
                        type="button"
                        disabled={!values[binding.name] || Boolean(busyName) || (shared && !shareAcknowledged[binding.name])}
                        onClick={() => { void createExactValue(binding) }}
                      >
                        {busyName === binding.name ? 'Configuring…' : shared ? 'Configure shared value' : 'Configure exact reference'}
                      </button>
                      {shared && (
                        <button
                          className="btn btn-secondary btn-sm"
                          type="button"
                          disabled={!values[binding.name] || Boolean(busyName) || rebindDisabled}
                          onClick={() => { void createDedicatedAndRebind(binding) }}
                        >
                          Create dedicated value
                        </button>
                      )}
                    </div>
                  </div>
                )}

                <div className="script-secret-rebind-control">
                  <label htmlFor={`${titleId.replace(/:/g, '')}-reuse-${index}`}>Use existing {applicationId ? 'application or project' : 'project'} secret</label>
                  <div>
                    <select
                      id={`${titleId.replace(/:/g, '')}-reuse-${index}`}
                      value={reuseName}
                      disabled={Boolean(busyName) || rebindDisabled}
                      onChange={event => setReuseNames(current => ({ ...current, [binding.name]: event.target.value }))}
                    >
                      <option value="">Select an existing configured name</option>
                      {vaultSecrets.filter(secret => secret.name !== binding.name).map(secret => (
                        <option value={secret.name} key={secret.name}>{secret.name}</option>
                      ))}
                    </select>
                    <button
                      className="btn btn-secondary btn-sm"
                      type="button"
                      disabled={!reuseName || Boolean(busyName) || rebindDisabled}
                      onClick={() => { void applyRebind(binding, reuseName) }}
                    >
                      Rebind and share
                    </button>
                  </div>
                  <small>Rebinding changes every exact occurrence in the editor draft. Save the script to make the new reference authoritative.</small>
                </div>
              </section>
            )
          })}
        </div>
      </div>
    </div></DialogLayer>
  ), document.body)
}
