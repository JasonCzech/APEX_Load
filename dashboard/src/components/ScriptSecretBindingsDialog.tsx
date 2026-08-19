import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'

import { useApi } from '../hooks/useApi'
import { type ProjectSecretMetadata, type ProjectSecretStatus, type ProjectSecretUsage, type ScriptSecretBinding } from '../types/secrets'
import { type ScriptDefinition } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'
import { useModalDialog } from '../hooks/useModalDialog'
import { dialogBackdropProps } from './dialogBackdropProps'
import { collisionSafeSecretName, MAX_SECRET_BINDING_NAME_LENGTH } from './secretBindingUtils'
import { scriptSecretReferenceSummaries } from './scriptSecretReferenceModel'
import SecretReferenceValueControl from './SecretReferenceValueControl'
import './ScriptSecretBindingsDialog.css'

interface Props {
  scriptId: string
  script: ScriptDefinition
  projectId: string
  scriptName: string
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
  scriptName,
  onClose,
  onConfigured,
  onRebound,
}: Props) {
  const api = useApi()
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const closeRef = useRef<HTMLButtonElement | null>(null)
  const titleId = useId()
  const descriptionId = useId()
  const requestGenerationRef = useRef(0)
  const [bindings, setBindings] = useState<BindingRow[]>([])
  const [vaultSecrets, setVaultSecrets] = useState<ProjectSecretMetadata[]>([])
  const [values, setValues] = useState<Record<string, string>>({})
  const [reuseNames, setReuseNames] = useState<Record<string, string>>({})
  const [shareAcknowledged, setShareAcknowledged] = useState<Record<string, boolean>>({})
  const [loading, setLoading] = useState(true)
  const [busyName, setBusyName] = useState('')
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  useModalDialog({ dialogRef, initialFocusRef: closeRef, onClose })

  const draftSummaries = useMemo(() => scriptSecretReferenceSummaries(script), [script])

  const load = useCallback(async () => {
    const requestGeneration = ++requestGenerationRef.current
    setLoading(true)
    setError('')
    try {
      const names = draftSummaries.map(binding => binding.name)
      const [savedStatus, projectStatuses, vault] = await Promise.all([
        api.getScriptSecretStatus(scriptId, { details: true }),
        api.getProjectSecretStatuses(projectId, names),
        api.listProjectSecrets(projectId),
      ])
      const usages = await Promise.all(names.map(name => api.getProjectSecretUsage(projectId, name, { limit: 50 })))
      if (requestGenerationRef.current !== requestGeneration) return
      const statusByName = new Map<string, ProjectSecretStatus>(projectStatuses.secrets.map(status => [status.name, status]))
      const savedBindingByName = new Map((savedStatus.bindings || []).map(binding => [binding.name, binding]))
      const usageByName = new Map(usages.map(usage => [usage.name, usage]))
      setBindings(draftSummaries.map(summary => {
        const status = statusByName.get(summary.name)
        const savedBinding = savedBindingByName.get(summary.name)
        return {
          name: summary.name,
          configured: status?.configured ?? savedBinding?.configured ?? false,
          provider: status?.provider || savedBinding?.provider,
          writable: status?.writable ?? savedBinding?.writable,
          occurrence_count: summary.occurrence_count,
          locations: summary.locations,
          usage: usageByName.get(summary.name),
        }
      }))
      setVaultSecrets((vault.secrets || []).slice().sort((left, right) => left.name.localeCompare(right.name)))
    } catch (cause: unknown) {
      if (requestGenerationRef.current !== requestGeneration) return
      setError(getErrorMessage(cause, 'Unable to load script secret bindings.'))
    } finally {
      if (requestGenerationRef.current === requestGeneration) setLoading(false)
    }
  }, [api, draftSummaries, projectId, scriptId])

  useEffect(() => {
    void load()
    return () => { requestGenerationRef.current += 1 }
  }, [load])

  const createExactValue = async (binding: BindingRow) => {
    const value = values[binding.name] || ''
    if (!value || busyName) return
    if ((binding.usage?.consumer_count || 0) > 1 && !shareAcknowledged[binding.name]) {
      setError('Confirm the shared impact before creating one value for every consuming script.')
      return
    }
    setBusyName(binding.name)
    setError('')
    setMessage('')
    try {
      await api.createProjectSecrets(projectId, [{
        name: binding.name,
        value,
        expected_usage: binding.usage ? {
          usage_revision: binding.usage.usage_revision,
          ...((binding.usage.consumer_count === 1 && binding.usage.consumers[0]?.script_id === scriptId)
            ? { exclusive_script_id: scriptId }
            : {}),
        } : undefined,
      }])
      setValues(current => ({ ...current, [binding.name]: '' }))
      setMessage(`${binding.name} is configured for the exact reference used by this script. The value cannot be viewed.`)
      await load()
      await onConfigured?.()
    } catch (cause: unknown) {
      setError(getErrorMessage(cause, 'Unable to create the project secret.'))
    } finally {
      setBusyName('')
    }
  }

  const applyRebind = async (binding: BindingRow, newName: string) => {
    if (!newName || newName === binding.name || busyName) return
    setBusyName(binding.name)
    setError('')
    setMessage('')
    try {
      const response = await api.rebindScriptSecretReference(script, binding.name, newName)
      await onRebound(response.script, response.changed_locations)
      setMessage(`Rebound ${response.changed_count} occurrence${response.changed_count === 1 ? '' : 's'} from ${binding.name} to ${newName}.`)
      onClose()
    } catch (cause: unknown) {
      setError(getErrorMessage(cause, 'Unable to rebind the script secret reference.'))
    } finally {
      setBusyName('')
    }
  }

  const allocateDedicatedName = async (binding: BindingRow): Promise<string> => {
    const base = dedicatedBaseName(binding.name, scriptName, scriptId)
    for (let ordinal = 1; ordinal <= 32; ordinal += 1) {
      const candidate = ordinal === 1 ? base : collisionSafeSecretName(base, ordinal)
      const [status, usage] = await Promise.all([
        api.getProjectSecretStatuses(projectId, [candidate]),
        api.getProjectSecretUsage(projectId, candidate, { limit: 1 }),
      ])
      if (!status.secrets[0]?.configured && usage.consumer_count === 0) return candidate
    }
    throw new Error('Unable to allocate a collision-safe script-specific secret name.')
  }

  const createDedicatedAndRebind = async (binding: BindingRow) => {
    const value = values[binding.name] || ''
    if (!value || busyName) return
    setBusyName(binding.name)
    setError('')
    setMessage('')
    let createdName = ''
    let valueCreated = false
    try {
      createdName = await allocateDedicatedName(binding)
      const usage = await api.getProjectSecretUsage(projectId, createdName, { limit: 1 })
      await api.createProjectSecrets(projectId, [{
        name: createdName,
        value,
        expected_usage: { usage_revision: usage.usage_revision },
      }])
      valueCreated = true
      const response = await api.rebindScriptSecretReference(script, binding.name, createdName)
      await onRebound(response.script, response.changed_locations, { createdSecretName: createdName })
      setValues(current => ({ ...current, [binding.name]: '' }))
      await onConfigured?.()
      onClose()
    } catch (cause: unknown) {
      const base = getErrorMessage(cause, 'Unable to create a dedicated script secret.')
      setError(valueCreated
        ? `${createdName} was created, but the script rebind/save did not complete. The value remains as an unused vault entry. ${base}`
        : base)
    } finally {
      setBusyName('')
    }
  }

  return createPortal((
    <div className="modal-overlay script-secret-bindings-overlay" {...dialogBackdropProps(onClose, 'Close secret bindings')}>
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
              Script references select write-only values from the project vault. Reuse a name only when scripts should share one credential.
            </p>
          </div>
          <button ref={closeRef} className="btn btn-sm btn-ghost" type="button" onClick={onClose}>Close</button>
        </div>

        {message && <p className="settings-notice" role="status">{message}</p>}
        {error && <p className="settings-error-inline" role="alert">{error}</p>}
        {loading && <p role="status">Loading secret bindings…</p>}
        {!loading && bindings.length === 0 && <p>This script does not reference any project secrets.</p>}

        <div className="script-secret-bindings-list">
          {bindings.map((binding, index) => {
            const shared = (binding.usage?.consumer_count || 0) > 1
            const reuseName = reuseNames[binding.name] || ''
            return (
              <section className="script-secret-binding-row" key={binding.name}>
                <div className="script-secret-binding-summary">
                  <div>
                    <strong>{binding.name}</strong>
                    <span className={`script-health-token ${binding.configured ? 'healthy' : 'warning'}`}>
                      {binding.configured ? 'Configured' : 'Missing value'}
                    </span>
                  </div>
                  <p>
                    {binding.occurrence_count} occurrence{binding.occurrence_count === 1 ? '' : 's'} in this draft ·{' '}
                    {binding.usage?.consumer_count ?? 0} consuming script{binding.usage?.consumer_count === 1 ? '' : 's'}
                    {binding.provider ? ` · ${binding.provider}` : ''}
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
                      nameLabel="Current script reference"
                      valueLabel="Secret value"
                      valueDisabled={busyName === binding.name}
                      valuePlaceholder="Enter once; it cannot be revealed later"
                    />
                    {shared && (
                      <label className="script-secret-impact-acknowledgement">
                        <input
                          type="checkbox"
                          checked={shareAcknowledged[binding.name] === true}
                          onChange={event => setShareAcknowledged(current => ({ ...current, [binding.name]: event.target.checked }))}
                        />
                        Use this value for all {binding.usage?.consumer_count} consuming scripts
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
                          disabled={!values[binding.name] || Boolean(busyName)}
                          onClick={() => { void createDedicatedAndRebind(binding) }}
                        >
                          Create dedicated value
                        </button>
                      )}
                    </div>
                  </div>
                )}

                <div className="script-secret-rebind-control">
                  <label htmlFor={`${titleId.replace(/:/g, '')}-reuse-${index}`}>Use existing project secret</label>
                  <div>
                    <select
                      id={`${titleId.replace(/:/g, '')}-reuse-${index}`}
                      value={reuseName}
                      disabled={Boolean(busyName)}
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
                      disabled={!reuseName || Boolean(busyName)}
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
    </div>
  ), document.body)
}
