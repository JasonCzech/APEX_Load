import DialogLayer from './ux/DialogLayer'
import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

import { useApi } from '../hooks/useApi'
import { ApiError } from '../hooks/apiClient'
import { type ProjectSecretMetadata, type ProjectSecretStatus, type ProjectSecretUsage, type ScriptSecretBinding } from '../types/secrets'
import { type ScriptDefinition } from '../types/scripts'
import type { ScriptAuthoringBaseline } from '../types/storedContent'
import { getErrorMessage } from '../utils/errorUtils'
import { useModalDialog } from '../hooks/useModalDialog'
import { dialogBackdropProps } from './dialogBackdropProps'
import {
  collisionSafeSecretName,
  isValidSecretBindingName,
  MAX_SECRET_BINDING_NAME_LENGTH,
  normalizeNewSecretName,
  secretBindingDisplayNames,
  suggestedNewSecretName,
} from './secretBindingUtils'
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
  // Whether this session may create vault values (manage_scripts). Without it
  // the dialog offers only the existing-secret picker.
  canCreateSecrets?: boolean
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
type BindMode = 'existing' | 'create'

function omitKey<T>(record: Record<string, T>, key: string): Record<string, T> {
  if (!(key in record)) return record
  const next = { ...record }
  delete next[key]
  return next
}

function scopeArticle(scopeLabel: string): string {
  return /^[aeiou]/i.test(scopeLabel) ? 'An' : 'A'
}

function secretExistsMessage(name: string, scopeLabel: string): string {
  return `${scopeArticle(scopeLabel)} ${scopeLabel} secret named ${name} already exists. Choose another name, or select it under "Use existing secret".`
}

// A project secret an application-placed script would shadow by creating an
// application secret of the same name.
function projectSecretShadowMessage(name: string): string {
  return `A project secret named ${name} already exists, and an application secret of that name would shadow it. Choose another name, or select it under "Use existing secret".`
}

// The controller's internal runtime-override names: a rebind refuses them, so
// creating one here would leave an unreachable vault value.
const RESERVED_SECRET_PREFIX = 'APEX_RUNTIME_OVERRIDE_'

// Saved scripts other than this one that reference a name. The usage list may
// be truncated, so only the count is trusted.
function otherConsumerCount(usage: ProjectSecretUsage | undefined, scriptId: string): number {
  if (!usage) return 0
  const consumers = Array.isArray(usage.consumers) ? usage.consumers : []
  return Math.max(0, usage.consumer_count - (consumers.some(consumer => consumer.script_id === scriptId) ? 1 : 0))
}

// The reference's own name already has a value: the reloaded row shows it as
// configured, so there is nothing to pick.
function ownNameConfiguredMessage(name: string, scopeLabel: string): string {
  return `${name} already has a value in the ${scopeLabel} vault. The bindings are being refreshed, and this reference will show as configured.`
}

interface CreateFailureContext {
  // Whether the create request was sent: only then can a 503 hide a stored value.
  createSent: boolean
  // Whether the name is the reference's own, configured in place.
  ownName: boolean
}

// What a refused vault create tells the operator. Messages name the secret and
// the reason, never the value.
function createSecretFailureMessage(cause: unknown, name: string, scopeLabel: string, context: CreateFailureContext): string {
  if (cause instanceof ApiError) {
    const reference = cause.ref ? ` Reference: ${cause.ref}.` : ''
    if (cause.status === 403) {
      return `You do not have permission to create ${scopeLabel} secrets. Select an existing secret instead, or ask a project editor to create ${name}.`
    }
    if (cause.status === 409 && /already exists/i.test(cause.message)) {
      return context.ownName ? ownNameConfiguredMessage(name, scopeLabel) : secretExistsMessage(name, scopeLabel)
    }
    if (cause.status === 503 && !context.createSent) {
      return `The ${scopeLabel} secret store is unavailable, so nothing was created. Try again later.${reference}`
    }
    // A 503 after the create was sent can follow a committed write (an audit
    // or invalidation step failed), and redacted detail cannot say which, so
    // never claim the value was not stored. The dialog reloads the list after
    // it; a missing store shows the configuration note instead.
    if (cause.status === 503) {
      const check = context.ownName
        ? `if this reference then shows as configured, the value was stored.`
        : `if ${name} then appears under "Use existing secret", select it instead of creating it again.`
      return `The ${scopeLabel} secret store did not confirm that ${name} was created. It may be unavailable, or the value may have been stored. The bindings are being refreshed: ${check}${reference}`
    }
  }
  return getErrorMessage(cause, `Unable to create the ${scopeLabel} secret.`)
}

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
  canCreateSecrets = true,
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
  // For an application-placed script: names only the project vault holds.
  const [projectOnlyNames, setProjectOnlyNames] = useState<Set<string>>(() => new Set())
  // Names a usage check found other saved scripts referencing: not offered
  // again as a new secret's name.
  const [referencedNames, setReferencedNames] = useState<Set<string>>(() => new Set())
  // Values this dialog created, so a later rebind to one still reports it to
  // the owner (which warns when the script save then fails).
  const createdNamesRef = useRef(new Set<string>())
  const [values, setValues] = useState<Record<string, string>>({})
  const [reuseNames, setReuseNames] = useState<Record<string, string>>({})
  const [shareAcknowledged, setShareAcknowledged] = useState<Record<string, boolean>>({})
  const [loading, setLoading] = useState(true)
  const [busyName, setBusyName] = useState('')
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [bindModes, setBindModes] = useState<Record<string, BindMode>>({})
  const [newNames, setNewNames] = useState<Record<string, string>>({})
  // Write-only: a typed value lives here only until its create succeeds.
  const [newValues, setNewValues] = useState<Record<string, string>>({})
  const [createSupported, setCreateSupported] = useState<boolean | null>(null)
  const [createForbidden, setCreateForbidden] = useState(false)
  useLayoutEffect(() => {
    setValues({}); setReuseNames({}); setShareAcknowledged({}); setBusyName(''); setMessage('')
    setBindModes({}); setNewNames({}); setNewValues({}); setCreateSupported(null); setCreateForbidden(false)
    setReferencedNames(new Set()); createdNamesRef.current = new Set()
  }, [scriptId, projectId, applicationId])
  useLayoutEffect(() => { setBusyName('') }, [script])
  useModalDialog({ dialogRef, initialFocusRef: closeRef, onClose })

  const draftSummaries = useMemo(() => scriptSecretReferenceSummaries(script), [script])
  const displayNames = useMemo(
    () => secretBindingDisplayNames([...bindings.map(binding => binding.name), ...draftSummaries.map(summary => summary.name)], script),
    [bindings, draftSummaries, script],
  )
  const displayNameFor = (name: string) => displayNames.get(name) || name
  const scopeLabel = applicationId ? 'application' : 'project'
  const canCreateNew = canCreateSecrets && !createForbidden && createSupported === true
  const takenNames = useMemo(
    () => new Set([...vaultSecrets.map(secret => secret.name), ...bindings.map(binding => binding.name), ...referencedNames]),
    [bindings, referencedNames, vaultSecrets],
  )
  // A reference with no value and no other consuming script may keep its own
  // name; any other taken name is numbered past.
  const suggestionFor = (binding: BindingRow) => {
    const ownNameFree = !binding.configured && otherConsumerCount(binding.usage, scriptId) === 0
    const taken = ownNameFree ? new Set([...takenNames].filter(name => name !== binding.name)) : takenNames
    return suggestedNewSecretName(binding.name, script, taken)
  }
  const newNameProblem = (binding: BindingRow, name: string): string => {
    if (!name) return 'Enter a name for the new secret.'
    if (!isValidSecretBindingName(name)) return 'Use 1–249 letters, numbers, or underscores.'
    if (name.startsWith(RESERVED_SECRET_PREFIX)) return `Names starting with ${RESERVED_SECRET_PREFIX} are reserved for runtime overrides. Choose a different name.`
    if (projectOnlyNames.has(name)) return projectSecretShadowMessage(name)
    if (vaultSecrets.some(secret => secret.name === name) || (name === binding.name && binding.configured)) {
      return secretExistsMessage(name, scopeLabel)
    }
    if (name !== binding.name && bindings.some(other => other.name === name)) {
      return `This script already uses ${name} for another reference. Choose a different name.`
    }
    if (name !== binding.name && referencedNames.has(name)) {
      return `${name} is already referenced by another saved script. Choose a different name so this value is not shared by accident.`
    }
    return ''
  }

  // preserveError keeps a failure on screen while capabilities refresh after it.
  // Resolves false when this refresh failed; a superseded refresh leaves the
  // outcome to the newer one.
  const load = useCallback(async (options?: { preserveError?: boolean }): Promise<boolean> => {
    const requestGeneration = ++requestGenerationRef.current
    setLoading(true)
    if (!options?.preserveError) setError('')
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
      if (requestGenerationRef.current !== requestGeneration) return true
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
      const projectOnly = new Set<string>()
      for (const secret of projectVault?.secrets || []) {
        if (!available.some(candidate => candidate.name === secret.name)) {
          available.push(secret)
          projectOnly.add(secret.name)
        }
      }
      setVaultSecrets(available.sort((left, right) => left.name.localeCompare(right.name)))
      setProjectOnlyNames(projectOnly)
      setCreateSupported(Boolean(vault.capabilities?.writable && vault.capabilities?.create_supported))
      return true
    } catch (cause: unknown) {
      if (requestGenerationRef.current !== requestGeneration) return true
      // A refresh after a failure keeps that failure, which says what to do.
      if (!options?.preserveError) setError(getErrorMessage(cause, 'Unable to load script secret bindings.'))
      return false
    } finally {
      if (requestGenerationRef.current === requestGeneration) setLoading(false)
    }
  }, [api, applicationId, draftSummaries, projectId, scriptId])

  useEffect(() => {
    void load()
    return () => { requestGenerationRef.current += 1 }
  }, [load])

  // A value now exists for this reference although the reload that would show
  // it failed: mark the row so a second value is not offered.
  const markConfigured = (name: string) => {
    setBindings(rows => rows.map(row => row.name === name ? { ...row, configured: true } : row))
  }

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
    let createSent = false
    try {
      const create = applicationId ? api.createApplicationSecrets : api.createProjectSecrets
      createSent = true
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
      const refreshed = await load()
      if (!current()) return
      if (!refreshed) {
        markConfigured(binding.name)
        setError(`${displayNameFor(binding.name)} was configured, but the bindings could not refresh. Close and reopen this dialog to see the current state.`)
      }
      await onConfigured?.()
    } catch (cause: unknown) {
      if (!current()) return
      setError(createSecretFailureMessage(cause, binding.name, scopeLabel, { createSent, ownName: true }))
      if (cause instanceof ApiError && cause.status === 403) {
        setCreateForbidden(true)
        setValues({})
        setNewValues({})
      }
      if (cause instanceof ApiError && (cause.status === 409 || cause.status === 503)) void load({ preserveError: true })
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
      if (createdNamesRef.current.has(newName)) await onRebound(response.script, response.changed_locations, { createdSecretName: newName })
      else await onRebound(response.script, response.changed_locations)
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
    let createSent = false
    let valueCreated = false
    try {
      createdName = await allocateDedicatedName(binding, current)
      if (!current()) return
      const usage = applicationId
        ? await api.getApplicationSecretUsage(applicationId, createdName, { limit: 1 })
        : await api.getProjectSecretUsage(projectId, createdName, { limit: 1 })
      if (!current()) return
      const create = applicationId ? api.createApplicationSecrets : api.createProjectSecrets
      createSent = true
      await create(applicationId || projectId, [{
        name: createdName,
        value,
        expected_usage: { usage_revision: usage.usage_revision },
      }])
      valueCreated = true
      createdNamesRef.current.add(createdName)
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
      setError(valueCreated
        ? `${createdName} was created, but the script rebind/save did not complete. The value remains as an unused vault entry. ${getErrorMessage(cause, 'Unable to create a dedicated script secret.')}`
        : createdName ? createSecretFailureMessage(cause, createdName, scopeLabel, { createSent, ownName: false }) : getErrorMessage(cause, 'Unable to create a dedicated script secret.'))
      if (!valueCreated && cause instanceof ApiError && cause.status === 403) {
        setCreateForbidden(true)
        setValues({})
        setNewValues({})
      }
      if (valueCreated || (cause instanceof ApiError && (cause.status === 409 || cause.status === 503))) void load({ preserveError: true })
    } finally {
      if (current()) setBusyName('')
    }
  }

  // Create a vault value under a name the operator chose, then point every
  // exact occurrence of this reference at it. Keeping the reference's own name
  // configures it in place, as "Configure exact reference" does.
  const createNewAndBind = async (binding: BindingRow) => {
    const name = normalizeNewSecretName(newNames[binding.name] ?? suggestionFor(binding))
    const value = newValues[binding.name] || ''
    const rebind = name !== binding.name
    if (!value || busyName || !canCreateNew || newNameProblem(binding, name) || (rebind && rebindDisabled)) return
    const scope = mutationScopeRef.current
    const current = () => mountedRef.current && mutationScopeRef.current === scope
    setBusyName(binding.name)
    setError('')
    setMessage('')
    let createSent = false
    let valueCreated = false
    try {
      const [status, usage] = await Promise.all([
        applicationId ? api.getApplicationSecretStatuses(applicationId, [name]) : api.getProjectSecretStatuses(projectId, [name]),
        applicationId ? api.getApplicationSecretUsage(applicationId, name, { limit: 50 }) : api.getProjectSecretUsage(projectId, name, { limit: 50 }),
      ])
      if (!current()) return
      if (status.secrets.find(candidate => candidate.name === name)?.configured) {
        setError(applicationId && status.secrets.find(candidate => candidate.name === name)?.source_scope === 'project'
          ? projectSecretShadowMessage(name)
          : rebind ? secretExistsMessage(name, scopeLabel) : ownNameConfiguredMessage(name, scopeLabel))
        void load({ preserveError: true })
        return
      }
      const consumers = Array.isArray(usage.consumers) ? usage.consumers : []
      const thisScriptConsumes = consumers.some(consumer => consumer.script_id === scriptId)
      if (rebind && usage.consumer_count > 0) {
        setError(`${name} is already referenced by ${usage.consumer_count} saved script${usage.consumer_count === 1 ? '' : 's'}. Choose a different name so this value is not shared by accident.`)
        setReferencedNames(current => new Set([...current, name]))
        return
      }
      if (!rebind && usage.consumer_count - (thisScriptConsumes ? 1 : 0) > 0 && !shareAcknowledged[binding.name]) {
        // Another script started using the name after the dialog loaded: the
        // reload shows its consumers and the acknowledgement to confirm.
        setError('Another saved script now references this name. Review its consumers and confirm the shared impact before creating one value for every consuming script.')
        // Keep the chosen name: the reloaded usage would otherwise number the
        // suggestion past it and hide the acknowledgement this asks for.
        setNewNames(current => ({ ...current, [binding.name]: name }))
        void load({ preserveError: true })
        return
      }
      const create = applicationId ? api.createApplicationSecrets : api.createProjectSecrets
      createSent = true
      await create(applicationId || projectId, [{
        name,
        value,
        expected_usage: {
          usage_revision: usage.usage_revision,
          ...(!rebind && usage.consumer_count === 1 && thisScriptConsumes ? { exclusive_script_id: scriptId } : {}),
        },
      }])
      valueCreated = true
      createdNamesRef.current.add(name)
      setNewValues(current => omitKey(current, binding.name))
      setNewNames(current => omitKey(current, binding.name))
      if (!current()) return
      if (rebind) {
        const response = await api.rebindScriptSecretReference(script, binding.name, name, authoringBaseline!)
        if (!current()) return
        await onRebound(response.script, response.changed_locations, { createdSecretName: name })
        if (!current()) return
        await onConfigured?.()
        if (!current()) return
        onClose()
        return
      }
      setBindModes(current => omitKey(current, binding.name))
      setMessage(`${name} was created in the ${scopeLabel} vault and this script's reference uses it. The value cannot be viewed.`)
      const refreshed = await load()
      if (!current()) return
      if (!refreshed) {
        markConfigured(binding.name)
        setError(`${name} was created and this script's reference uses it, but the bindings could not refresh. Close and reopen this dialog to see the current state.`)
      }
      await onConfigured?.()
    } catch (cause: unknown) {
      if (!current()) return
      if (valueCreated) {
        if (rebind) {
          // The value exists but nothing references it yet: offer it in the
          // existing-secret picker so binding it needs no second create.
          setError(`${name} was created, but the script rebind did not complete. It is selected under "Use existing secret" so you can bind it without creating it again. ${getErrorMessage(cause, 'Unable to bind the new secret.')}`)
          setBindModes(current => omitKey(current, binding.name))
          // Listed at once, so the preselection holds even if the reload fails.
          setVaultSecrets(current => current.some(secret => secret.name === name)
            ? current
            : [...current, { name, provider: '', writable: true }].sort((left, right) => left.name.localeCompare(right.name)))
          setReuseNames(current => ({ ...current, [binding.name]: name }))
        } else {
          // Only the owner's status refresh runs after an in-place create.
          markConfigured(binding.name)
          setError(`${name} was created and this script's reference uses it, but refreshing the script's secret status failed. ${getErrorMessage(cause, 'Unable to refresh the secret status.')}`)
        }
        void load({ preserveError: true })
        return
      }
      setError(createSecretFailureMessage(cause, name, scopeLabel, { createSent, ownName: !rebind }))
      if (cause instanceof ApiError && cause.status === 403) {
        setCreateForbidden(true)
        setValues({})
        setNewValues({})
      }
      if (cause instanceof ApiError && (cause.status === 409 || cause.status === 503)) void load({ preserveError: true })
    } finally {
      if (current()) setBusyName('')
    }
  }

  return createPortal((
    <DialogLayer><div className="modal-overlay script-secret-bindings-overlay" {...dialogBackdropProps(onClose)}>
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
        {canCreateSecrets && createSupported === false && bindings.length > 0 && (
          <p className="new-test-inline-note script-secret-create-unavailable">
            New secrets cannot be created here: no writable {scopeLabel} secret store is configured. A deployment administrator must configure APEXLOAD_WRAPPING_MASTER_KEY or Key Vault. You can still bind existing secrets.
          </p>
        )}
        {loading && <p role="status">Loading secret bindings…</p>}
        {!loading && bindings.length === 0 && <p>This script does not reference any secrets.</p>}

        <div className="script-secret-bindings-list">
          {bindings.map((binding, index) => {
            const shared = (binding.usage?.consumer_count || 0) > 1
            const reuseName = reuseNames[binding.name] || ''
            const displayName = displayNameFor(binding.name)
            const rowId = titleId.replace(/:/g, '')
            const mode: BindMode = canCreateNew && bindModes[binding.name] === 'create' ? 'create' : 'existing'
            const newNameInput = newNames[binding.name] ?? suggestionFor(binding)
            const newName = normalizeNewSecretName(newNameInput)
            const nameProblem = newNameProblem(binding, newName)
            const newNameRebinds = newName !== binding.name
            // Creating the reference's own name sets the value every other
            // consuming script reads, as "Configure shared value" does.
            const createSharesValue = !newNameRebinds && otherConsumerCount(binding.usage, scriptId) > 0
            const busy = busyName === binding.name
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

                {!binding.configured && canCreateSecrets && !createForbidden && createSupported !== false && (
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

                {!binding.configured && !(canCreateSecrets && !createForbidden && createSupported !== false) && (
                  // No value can be entered here, but the exact reference still
                  // says which vault name the script reads.
                  <details className="secret-binding-internal-reference">
                    <summary>Internal script reference</summary>
                    <code>{`{{secret.${binding.name}}}`}</code>
                  </details>
                )}

                <div className="script-secret-rebind-control">
                  {canCreateNew && (
                    <div className="script-secret-bind-mode" role="radiogroup" aria-label={`Bind ${displayName} to`}>
                      <label>
                        <input
                          type="radio"
                          name={`${rowId}-mode-${index}`}
                          checked={mode === 'existing'}
                          disabled={Boolean(busyName)}
                          onChange={() => {
                            setBindModes(current => omitKey(current, binding.name))
                            setNewValues(current => omitKey(current, binding.name))
                          }}
                        />
                        <span>Use existing secret</span>
                      </label>
                      <label>
                        <input
                          type="radio"
                          name={`${rowId}-mode-${index}`}
                          checked={mode === 'create'}
                          disabled={Boolean(busyName)}
                          onChange={() => setBindModes(current => ({ ...current, [binding.name]: 'create' }))}
                        />
                        <span>Create new secret</span>
                      </label>
                    </div>
                  )}
                  {mode === 'existing' ? (
                    <>
                      <label htmlFor={`${rowId}-reuse-${index}`}>Use existing {applicationId ? 'application or project' : 'project'} secret</label>
                      <div>
                        <select
                          id={`${rowId}-reuse-${index}`}
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
                    </>
                  ) : (
                    <div className="script-secret-create-control">
                      <SecretReferenceValueControl
                        id={`${rowId}-new-${index}`}
                        name={newNameInput}
                        value={newValues[binding.name] || ''}
                        onNameChange={name => setNewNames(current => ({ ...current, [binding.name]: name }))}
                        onValueChange={value => setNewValues(current => ({ ...current, [binding.name]: value }))}
                        nameLabel={`New ${scopeLabel} secret name for ${displayName}`}
                        valueLabel={`Secret value for ${displayName}`}
                        nameDisabled={busy}
                        valueDisabled={busy}
                        nameInvalid={Boolean(nameProblem)}
                        nameDescribedBy={nameProblem ? `${rowId}-new-${index}-problem` : undefined}
                        valuePlaceholder="Enter once; it cannot be revealed later"
                        referenceKind="Script reference after binding"
                        referenceName={newName}
                      />
                      {nameProblem && <p id={`${rowId}-new-${index}-problem`} className="settings-error-inline">{nameProblem}</p>}
                      {createSharesValue && (
                        <label className="script-secret-impact-acknowledgement">
                          <input
                            type="checkbox"
                            checked={shareAcknowledged[binding.name] === true}
                            onChange={event => setShareAcknowledged(current => ({ ...current, [binding.name]: event.target.checked }))}
                          />
                          <span>Use this value for every script that references {newName}</span>
                        </label>
                      )}
                      <div className="settings-inline-actions">
                        <button
                          className="btn btn-primary btn-sm"
                          type="button"
                          disabled={!newValues[binding.name] || Boolean(nameProblem) || Boolean(busyName) || (newNameRebinds && rebindDisabled) || (createSharesValue && !shareAcknowledged[binding.name])}
                          onClick={() => { void createNewAndBind(binding) }}
                        >
                          {busy ? 'Creating…' : newNameRebinds ? 'Create and bind' : 'Create value'}
                        </button>
                      </div>
                      <small>
                        {newNameRebinds
                          ? `Creates ${newName || 'the secret'} in the ${scopeLabel} vault and rebinds every exact occurrence in the editor draft. Save the script to make the new reference authoritative.`
                          : `Creates the value for this script's existing reference in the ${scopeLabel} vault.`}
                      </small>
                    </div>
                  )}
                </div>
              </section>
            )
          })}
        </div>
      </div>
    </div></DialogLayer>
  ), document.body)
}
