import {
  forwardRef,
  useEffect,
  useId,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
} from 'react'

import { useApi } from '../hooks/useApi'
import {
  type SecretBindingRequirement,
  type SecretBindingSelection,
} from '../types/scriptImports'
import { type ProjectSecretStatus, type ProjectSecretWrite } from '../types/secrets'
import { getErrorMessage } from '../utils/errorUtils'
import { isValidSecretBindingName, MAX_SECRET_BINDING_NAME_LENGTH } from './secretBindingUtils'
import {
  formatSourceSecretSessionFailure,
  getSourceSecretSessionFailureMetadata,
  sourceSecretSessionIsExpired,
} from './sourceSecretSession'
import './ScriptImportReview.css'

interface Props {
  requirements: SecretBindingRequirement[]
  sourceMigrationRequirements?: SecretBindingRequirement[]
  preserveSourceSessionAfterApply?: boolean
  reviewGeneration?: string | number
  projectId?: string
  sourceSecretSessionId?: string
  sourceSecretSessionExpiresAt?: string
  sourceMigrationAllowed?: boolean
  externalSourceMigrationSelected?: boolean
  onExternalSourceMigrationChange?: (selected: boolean) => void
  onApply: (bindings: SecretBindingSelection[]) => void | Promise<void>
  onSourceSessionSettled?: (
    sourceSecretSessionId: string,
    migrationSelections: SecretBindingSelection[],
  ) => void
  onPreparedChange?: (prepared: PreparedSecretBindings) => void
  disabled?: boolean
}

interface RequirementGroup {
  key: string
  requirements: SecretBindingRequirement[]
  suggestedName: string
}

export interface PreparedSecretBindings {
  valid: boolean
  configureLaterCount: number
}

interface SecretBindingCommitPlan extends PreparedSecretBindings {
  bindings: SecretBindingSelection[]
  secrets: ProjectSecretWrite[]
  migrateSourceValues: boolean
  migrationSelections: SecretBindingSelection[]
}

type CredentialProbeState = 'idle' | 'loading' | 'ready' | 'unavailable'

export interface SecretBindingReviewHandle {
  commit: () => Promise<boolean>
  discardSourceSession: () => Promise<void>
  preserveEditsForNextReviewGeneration: () => void
}

function requirementScope(requirement: SecretBindingRequirement): string {
  if (typeof requirement.script_index === 'number') return `script-index:${requirement.script_index}`
  if (requirement.script_id) return `script-id:${requirement.script_id}`
  if (requirement.script_name) return `script-name:${requirement.script_name}`
  return 'unscoped'
}

function groupRequirements(requirements: SecretBindingRequirement[]): RequirementGroup[] {
  const groups = new Map<string, RequirementGroup>()
  for (const requirement of requirements) {
    const suggestedName = requirement.suggested_name?.trim() || ''
    const scope = requirementScope(requirement)
    const key = suggestedName ? `${scope}:name:${suggestedName}` : `${scope}:requirement:${requirement.id}`
    const existing = groups.get(key)
    if (existing) {
      existing.requirements.push(requirement)
    } else {
      groups.set(key, { key, requirements: [requirement], suggestedName })
    }
  }
  const result = Array.from(groups.values())
  const usedNames = new Set<string>()
  for (const group of result) {
    const base = group.suggestedName
    if (!base || !usedNames.has(base)) {
      if (base) usedNames.add(base)
      continue
    }
    let suffix = 2
    let candidate = base
    do {
      const suffixText = `_${suffix}`
      candidate = `${base.slice(0, MAX_SECRET_BINDING_NAME_LENGTH - suffixText.length)}${suffixText}`
      suffix += 1
    } while (usedNames.has(candidate))
    group.suggestedName = candidate
    usedNames.add(candidate)
  }
  return result
}

function semanticRequirementsIdentity(requirements: readonly SecretBindingRequirement[]): string {
  return JSON.stringify(requirements
    .map(requirement => ({
      id: requirement.id,
      path: requirement.path,
      occurrence: requirement.occurrence ?? null,
      label: requirement.label,
      context: requirement.context,
      suggested_name: requirement.suggested_name,
      replacement: requirement.replacement ?? '',
      script_index: requirement.script_index ?? null,
      script_id: requirement.script_id ?? '',
      script_name: requirement.script_name ?? '',
      source_value_available: requirement.source_value_available === true,
    }))
    .sort((left, right) => {
      const leftKey = `${left.id}\u0000${left.path}\u0000${left.occurrence ?? ''}`
      const rightKey = `${right.id}\u0000${right.path}\u0000${right.occurrence ?? ''}`
      return leftKey.localeCompare(rightKey)
    }))
}

function initialGroupValues(groups: RequirementGroup[]): Record<string, string> {
  return Object.fromEntries(groups.map(group => [group.key, group.suggestedName]))
}

function bindingNameError(value: string): string {
  const name = value.trim()
  if (!name) return 'Enter a logical secret name.'
  if (!isValidSecretBindingName(name)) {
    return 'Use 1–249 uppercase letters, numbers, or underscores.'
  }
  return ''
}

function normalizeStatusMap(response: unknown): Map<string, ProjectSecretStatus> {
  if (!response || typeof response !== 'object') return new Map()
  const candidate = response as {
    statuses?: ProjectSecretStatus[]
    secrets?: ProjectSecretStatus[]
  }
  const items = Array.isArray(candidate.statuses)
    ? candidate.statuses
    : Array.isArray(candidate.secrets)
      ? candidate.secrets
      : []
  return new Map(items.map(status => [status.name, status]))
}

const SecretBindingReview = forwardRef<SecretBindingReviewHandle, Props>(function SecretBindingReview({
  requirements,
  sourceMigrationRequirements = requirements,
  preserveSourceSessionAfterApply = false,
  reviewGeneration,
  projectId,
  sourceSecretSessionId,
  sourceSecretSessionExpiresAt,
  sourceMigrationAllowed = false,
  externalSourceMigrationSelected = false,
  onExternalSourceMigrationChange,
  onApply,
  onSourceSessionSettled,
  onPreparedChange,
  disabled = false,
}, ref) {
  const api = useApi()
  const idPrefix = useId().replace(/:/g, '')
  const titleId = `${idPrefix}-secret-binding-review-title`
  const requirementsIdentity = semanticRequirementsIdentity(requirements)
  const groupSnapshotRef = useRef<{ identity: string; groups: RequirementGroup[] } | null>(null)
  if (groupSnapshotRef.current?.identity !== requirementsIdentity) {
    groupSnapshotRef.current = {
      identity: requirementsIdentity,
      groups: groupRequirements(requirements),
    }
  }
  const groups = groupSnapshotRef.current.groups
  const migrationGroups = useMemo(
    () => groupRequirements(sourceMigrationRequirements.filter(requirement => requirement.source_value_available)),
    [sourceMigrationRequirements],
  )
  const reviewIdentity = JSON.stringify({
    reviewGeneration: reviewGeneration ?? '',
    projectId: projectId || '',
    sourceSecretSessionId: sourceSecretSessionId || '',
    sourceSecretSessionExpiresAt: sourceSecretSessionExpiresAt || '',
    sourceMigrationAllowed,
    requirements: requirementsIdentity,
  })
  const credentialScopeIdentity = JSON.stringify({
    projectId: projectId || '',
    sourceSecretSessionId: sourceSecretSessionId || '',
    sourceSecretSessionExpiresAt: sourceSecretSessionExpiresAt || '',
    sourceMigrationAllowed,
    requirements: requirementsIdentity,
  })
  const activeReviewIdentityRef = useRef(reviewIdentity)
  const reviewGenerationRef = useRef(0)
  if (activeReviewIdentityRef.current !== reviewIdentity) {
    activeReviewIdentityRef.current = reviewIdentity
    reviewGenerationRef.current += 1
  }
  const [names, setNames] = useState<Record<string, string>>(() => initialGroupValues(groups))
  const [values, setValues] = useState<Record<string, string>>({})
  const valuesRef = useRef<Record<string, string>>({})
  const [statuses, setStatuses] = useState<Map<string, ProjectSecretStatus>>(new Map())
  const [storeProvider, setStoreProvider] = useState('')
  const [storeWritable, setStoreWritable] = useState(false)
  const [probeState, setProbeState] = useState<CredentialProbeState>('idle')
  const [probeError, setProbeError] = useState('')
  const [probeRetryGeneration, setProbeRetryGeneration] = useState(0)
  const [migrateSourceValues, setMigrateSourceValues] = useState(false)
  const [sourceSessionUnavailable, setSourceSessionUnavailable] = useState(false)
  const [sourceSessionNotice, setSourceSessionNotice] = useState('')
  const [applying, setApplying] = useState(false)
  const [error, setError] = useState('')
  const resetReviewIdentityRef = useRef(reviewIdentity)
  const credentialScopeIdentityRef = useRef(credentialScopeIdentity)
  const preserveEditsForNextReviewGenerationRef = useRef(false)
  const settledSourceSessionRef = useRef('')
  const notifiedSourceSessionRef = useRef('')
  const discardSourceSessionInFlightRef = useRef<{ id: string; promise: Promise<void> } | null>(null)
  const activeSourceSessionIdentityRef = useRef(`${projectId || ''}\u0000${sourceSecretSessionId || ''}`)
  const sourceSessionPropIdentityRef = useRef(activeSourceSessionIdentityRef.current)
  activeSourceSessionIdentityRef.current = `${projectId || ''}\u0000${sourceSecretSessionId || ''}`

  useEffect(() => {
    if (resetReviewIdentityRef.current !== reviewIdentity) {
      // Review-only actions (tab switches, runtime edits, selector repair, and
      // parameterization) legitimately advance the generation. Keep write-only
      // edits when the credential scope itself is unchanged.
      const preserveEdits = credentialScopeIdentityRef.current === credentialScopeIdentity
      resetReviewIdentityRef.current = reviewIdentity
      preserveEditsForNextReviewGenerationRef.current = false
      if (!preserveEdits) {
        setNames(initialGroupValues(groups))
        valuesRef.current = {}
        setValues({})
      }
      setStatuses(new Map())
      setStoreProvider('')
      setStoreWritable(false)
      setProbeState('idle')
      setProbeError('')
      setMigrateSourceValues(false)
      setSourceSessionUnavailable(false)
      setSourceSessionNotice('')
      setApplying(false)
      setError('')
    }
    credentialScopeIdentityRef.current = credentialScopeIdentity
  }, [credentialScopeIdentity, groups, reviewIdentity])

  useEffect(() => {
    const nextIdentity = `${projectId || ''}\u0000${sourceSecretSessionId || ''}`
    const previousIdentity = sourceSessionPropIdentityRef.current
    sourceSessionPropIdentityRef.current = nextIdentity
    const parentClearedSettledSession = (
      !sourceSecretSessionId
      && previousIdentity === `${projectId || ''}\u0000${settledSourceSessionRef.current}`
    )
    if (!parentClearedSettledSession) {
      settledSourceSessionRef.current = ''
      notifiedSourceSessionRef.current = ''
    }
    discardSourceSessionInFlightRef.current = null
  }, [projectId, sourceSecretSessionId])

  useEffect(() => () => {
    // Write-only values never leave this card. Identity changes clear the ref
    // above unless an explicitly marked same-scope parameterization transition
    // is preserving the edit; unmount always drops the final retained object.
    valuesRef.current = {}
  }, [])

  const validationErrors = useMemo(
    () => Object.fromEntries(groups.map(group => [
      group.key,
      bindingNameError(names[group.key] ?? ''),
    ])),
    [groups, names],
  )

  const conflictingValueNames = useMemo(() => {
    const valuesByName = new Map<string, Set<string>>()
    for (const group of groups) {
      const name = (names[group.key] || '').trim()
      const value = values[group.key] || ''
      if (!name || !value) continue
      const seen = valuesByName.get(name) || new Set<string>()
      seen.add(value)
      valuesByName.set(name, seen)
    }
    return new Set(Array.from(valuesByName.entries()).filter(([, seen]) => seen.size > 1).map(([name]) => name))
  }, [groups, names, values])

  const activeValidNames = useMemo(
    () => Array.from(new Set(groups
      .map(group => (names[group.key] || '').trim())
      .filter(isValidSecretBindingName))).sort(),
    [groups, names],
  )

  const uniqueValidNames = useMemo(
    () => Array.from(new Set([
      ...activeValidNames,
      ...migrationGroups.map(group => group.suggestedName.trim()),
    ]
      .filter(isValidSecretBindingName))).sort(),
    [activeValidNames, migrationGroups],
  )

  const sourceMigrationControlled = typeof onExternalSourceMigrationChange === 'function'
  const sourceMigrationSelected = sourceMigrationControlled ? externalSourceMigrationSelected : migrateSourceValues
  const effectiveSourceMigrationSelected = sourceMigrationSelected && !sourceSessionUnavailable

  useEffect(() => {
    if (!sourceSecretSessionExpiresAt) return
    const expiresAt = Date.parse(sourceSecretSessionExpiresAt)
    if (!Number.isFinite(expiresAt)) return
    const expireSession = () => {
      setSourceSessionUnavailable(true)
      setSourceSessionNotice('Detected source credential values expired. Enter values manually now, configure them later, or restart the import.')
      setMigrateSourceValues(false)
      if (sourceMigrationControlled) onExternalSourceMigrationChange(false)
    }
    if (sourceSecretSessionIsExpired(sourceSecretSessionExpiresAt)) {
      expireSession()
      return
    }
    const timeout = window.setTimeout(expireSession, Math.min(expiresAt - Date.now(), 2_147_483_647))
    return () => window.clearTimeout(timeout)
  }, [onExternalSourceMigrationChange, sourceMigrationControlled, sourceSecretSessionExpiresAt])

  useEffect(() => {
    if (!projectId || uniqueValidNames.length === 0 || typeof api.getProjectSecretStatuses !== 'function') {
      setStatuses(new Map())
      setStoreWritable(false)
      setStoreProvider('')
      setProbeError(projectId && uniqueValidNames.length > 0
        ? 'Credential status is unavailable because the project secret API is not supported by this controller.'
        : '')
      setProbeState(projectId && uniqueValidNames.length > 0 ? 'unavailable' : 'idle')
      return
    }
    let cancelled = false
    setProbeState('loading')
    setProbeError('')
    void api.getProjectSecretStatuses(projectId, uniqueValidNames)
      .then(response => {
        if (cancelled) return
        setStatuses(normalizeStatusMap(response))
        setStoreProvider(response.capabilities?.provider || '')
        setStoreWritable(response.capabilities?.create_supported ?? response.capabilities?.writable ?? false)
        setProbeState('ready')
        setProbeError('')
      })
      .catch((cause: unknown) => {
        if (cancelled) return
        setStatuses(new Map())
        setStoreProvider('')
        setStoreWritable(false)
        setProbeState('unavailable')
        setProbeError(getErrorMessage(cause, 'Unable to check project credential status.'))
      })
    return () => {
      cancelled = true
    }
  }, [api, probeRetryGeneration, projectId, uniqueValidNames])

  const prepared = useMemo<SecretBindingCommitPlan>(() => {
    const bindings: SecretBindingSelection[] = []
    const writesByName = new Map<string, string>()
    const sourceAvailableNames = new Set<string>()
    const activeNamesByRequirement = new Map<string, string>()
    let valid = groups.length > 0

    for (const group of groups) {
      const name = (names[group.key] || '').trim()
      if (validationErrors[group.key] || conflictingValueNames.has(name)) valid = false
      for (const requirement of group.requirements) {
        const binding: SecretBindingSelection = {
          requirement_id: requirement.id,
          secret_name: name,
        }
        if (typeof requirement.script_index === 'number') {
          // Keep routing metadata available to multi-script review handlers,
          // but make it non-enumerable so it can never leak into an API JSON
          // body when a single-script caller forwards the selection directly.
          Object.defineProperty(binding, 'script_index', {
            configurable: false,
            enumerable: false,
            value: requirement.script_index,
          })
        }
        bindings.push(binding)
        activeNamesByRequirement.set(requirement.id, name)
      }
      const value = values[group.key] || ''
      if (name && value) writesByName.set(name, value)
      if (name && group.requirements.some(requirement => requirement.source_value_available)) {
        sourceAvailableNames.add(name)
      }
    }
    const migrationSelections = migrationGroups.flatMap(group => group.requirements.map(requirement => ({
      requirement_id: requirement.id,
      secret_name: activeNamesByRequirement.get(requirement.id) || group.suggestedName.trim(),
    }))).filter(selection => isValidSecretBindingName(selection.secret_name))

    const pendingNames = new Set<string>()
    for (const name of activeValidNames) {
      const configured = statuses.get(name)?.configured === true
      const hasManualValue = writesByName.has(name)
      const willMigrate = effectiveSourceMigrationSelected && sourceAvailableNames.has(name)
      if (!configured && !hasManualValue && !willMigrate) pendingNames.add(name)
    }

    return {
      bindings,
      secrets: Array.from(writesByName, ([name, value]) => ({ name, value })),
      valid,
      configureLaterCount: pendingNames.size,
      // Externally controlled migrations (for example Bruno commits) are
      // performed by that import's atomic server-side commit, not by the
      // generic source-session endpoint used by this component.
      migrateSourceValues: !sourceMigrationControlled && effectiveSourceMigrationSelected,
      migrationSelections,
    }
  }, [activeValidNames, conflictingValueNames, effectiveSourceMigrationSelected, groups, migrationGroups, names, sourceMigrationControlled, statuses, validationErrors, values])

  const preparedSummary = useMemo<PreparedSecretBindings>(() => ({
    valid: prepared.valid,
    configureLaterCount: prepared.configureLaterCount,
  }), [prepared.configureLaterCount, prepared.valid])

  useEffect(() => {
    // Never lift write-only values, binding payloads, or migration intent into
    // a parent component. Parents only need safe CTA/validation metadata.
    onPreparedChange?.(preparedSummary)
  }, [onPreparedChange, preparedSummary])

  const discardSourceSession = async (): Promise<void> => {
    if (!projectId || !sourceSecretSessionId || settledSourceSessionRef.current === sourceSecretSessionId) return
    const inFlight = discardSourceSessionInFlightRef.current
    if (inFlight?.id === sourceSecretSessionId) return inFlight.promise
    const sessionId = sourceSecretSessionId
    const sourceSessionIdentity = activeSourceSessionIdentityRef.current
    const promise = (async () => {
      await api.discardImportSecretSession({
        project_id: projectId,
        source_secret_session_id: sessionId,
      })
      if (activeSourceSessionIdentityRef.current === sourceSessionIdentity) {
        settledSourceSessionRef.current = sessionId
        setSourceSessionUnavailable(true)
        setMigrateSourceValues(false)
        if (sourceMigrationControlled) onExternalSourceMigrationChange(false)
      }
    })()
    discardSourceSessionInFlightRef.current = { id: sessionId, promise }
    try {
      await promise
    } finally {
      if (discardSourceSessionInFlightRef.current?.promise === promise) {
        discardSourceSessionInFlightRef.current = null
      }
    }
  }

  const commit = async (): Promise<boolean> => {
    if (!prepared.valid || applying || disabled) return false
    const generation = reviewGenerationRef.current
    const sourceIdentity = reviewIdentity
    const requestIsCurrent = () => (
      reviewGenerationRef.current === generation
      && activeReviewIdentityRef.current === sourceIdentity
    )
    setApplying(true)
    setError('')
    try {
      let currentStatuses = statuses
      let currentStoreWritable = storeWritable
      const requestsGovernedMutation = prepared.secrets.length > 0 || effectiveSourceMigrationSelected
      if (projectId && uniqueValidNames.length > 0 && typeof api.getProjectSecretStatuses === 'function') {
        setProbeState('loading')
        setProbeError('')
        try {
          const response = await api.getProjectSecretStatuses(projectId, uniqueValidNames)
          if (!requestIsCurrent()) return false
          currentStatuses = normalizeStatusMap(response)
          currentStoreWritable = response.capabilities?.create_supported ?? response.capabilities?.writable ?? false
          setStatuses(currentStatuses)
          setStoreProvider(response.capabilities?.provider || '')
          setStoreWritable(currentStoreWritable)
          setProbeState('ready')
        } catch (cause: unknown) {
          if (!requestIsCurrent()) return false
          setStatuses(new Map())
          setStoreProvider('')
          setStoreWritable(false)
          setProbeState('unavailable')
          setProbeError(getErrorMessage(cause, 'Unable to check project credential status.'))
          // A read-only status probe is not a prerequisite for saving safe
          // references with all values deferred. Any requested write or source
          // migration still requires the fresh create-only conflict check.
          if (requestsGovernedMutation) {
            throw new Error('Unable to verify the project secret store before writing or migrating credentials.')
          }
        }
      } else if (requestsGovernedMutation) {
        throw new Error('The project secret store cannot be verified for this credential operation.')
      }
      if (!requestIsCurrent()) return false
      const secretsToCreate = prepared.secrets.filter(secret => currentStatuses.get(secret.name)?.configured !== true)
      if (secretsToCreate.length > 0) {
        if (!projectId) throw new Error('Select a project before storing secret values.')
        if (!currentStoreWritable) throw new Error('The configured project secret provider is read-only.')
        await api.createProjectSecrets(projectId, secretsToCreate)
        if (!requestIsCurrent()) return false
      }
      if (prepared.migrateSourceValues) {
        if (!projectId || !sourceSecretSessionId) {
          throw new Error('The protected source-secret session is unavailable.')
        }
        const namesWithManualValues = new Set(prepared.secrets.map(secret => secret.name))
        const migrationSelections = prepared.migrationSelections.filter(selection => {
          const configured = currentStatuses.get(selection.secret_name)?.configured === true
          return !namesWithManualValues.has(selection.secret_name) && !configured
        })
        if (migrationSelections.length > 0) {
          if (!requestIsCurrent()) return false
          try {
            await api.migrateImportSecrets({
              project_id: projectId,
              source_secret_session_id: sourceSecretSessionId,
              secrets: migrationSelections,
            })
          } catch (cause: unknown) {
            if (!requestIsCurrent()) return false
            const failureMessage = formatSourceSecretSessionFailure(cause)
            const failureMetadata = getSourceSecretSessionFailureMetadata(cause)
            if (failureMetadata.retained) {
              // The provider confirmed that the protected source session is
              // still fenced and retryable. Do not run the uncertainty-path
              // discard or disable migration: a fresh status probe will omit
              // any names already committed by a partial attempt.
              setSourceSessionUnavailable(false)
            } else if (failureMetadata.consumed) {
              settledSourceSessionRef.current = sourceSecretSessionId
              if (notifiedSourceSessionRef.current !== sourceSecretSessionId) {
                notifiedSourceSessionRef.current = sourceSecretSessionId
                onSourceSessionSettled?.(sourceSecretSessionId, [])
              }
            } else {
              try {
                await discardSourceSession()
              } catch {
                // The migration failure remains the actionable error. A failed
                // best-effort discard is still bounded by the server-side TTL.
              }
            }
            if (!failureMetadata.retained) {
              setSourceSessionUnavailable(true)
              setMigrateSourceValues(false)
            }
            setSourceSessionNotice(failureMessage)
            setProbeRetryGeneration(current => current + 1)
            return false
          }
          const migrationStillBelongsToCurrentReview = requestIsCurrent()
          // A successful source migration also consumes the single-use
          // session. Settle it before applying bindings, even when the user
          // changed generated-script tabs while the request was in flight or
          // the subsequent binding application fails. The archive owner also
          // receives every resolved logical name here so inactive scripts keep
          // collision-safe names chosen by the archive-wide migration.
          setSourceSessionUnavailable(true)
          settledSourceSessionRef.current = sourceSecretSessionId
          if (notifiedSourceSessionRef.current !== sourceSecretSessionId) {
            notifiedSourceSessionRef.current = sourceSecretSessionId
            onSourceSessionSettled?.(sourceSecretSessionId, prepared.migrationSelections)
          }
          setMigrateSourceValues(false)
          setProbeRetryGeneration(current => current + 1)
          if (!migrationStillBelongsToCurrentReview) return false
        }
      }
      // Clearing a consumed source session in the parent legitimately changes
      // this component's review identity. The active import handler performs
      // its own generation/script guard, so continue the binding application
      // after an in-scope successful migration.
      if (
        settledSourceSessionRef.current !== sourceSecretSessionId
        && !requestIsCurrent()
      ) return false
      await onApply(prepared.bindings)
      if (!sourceMigrationControlled && !preserveSourceSessionAfterApply && sourceSecretSessionId && settledSourceSessionRef.current !== sourceSecretSessionId) {
        try {
          await discardSourceSession()
        } catch (cause: unknown) {
          // Binding application already succeeded. Surface cleanup trouble but
          // do not make the safe reviewed script unusable; the server TTL still
          // bounds retention if this best-effort finalizer cannot be reached.
          setSourceSessionNotice(getErrorMessage(cause, 'The protected source credential session could not be discarded and will expire automatically.'))
        }
      }
      if (sourceSecretSessionId && settledSourceSessionRef.current === sourceSecretSessionId) {
        if (notifiedSourceSessionRef.current !== sourceSecretSessionId) {
          notifiedSourceSessionRef.current = sourceSecretSessionId
          onSourceSessionSettled?.(sourceSecretSessionId, [])
        }
      }
      // A successful binding application normally removes these requirements
      // and therefore changes this card's semantic identity. Parent handlers
      // are responsible for rejecting a genuinely stale source generation;
      // only mutate local write-only state when this exact card is still live.
      if (requestIsCurrent()) {
        valuesRef.current = {}
        setValues({})
      }
      return true
    } catch (err: unknown) {
      if (requestIsCurrent()) setError(getErrorMessage(err, 'Failed to configure credentials'))
      return false
    } finally {
      if (requestIsCurrent()) setApplying(false)
    }
  }

  useImperativeHandle(ref, () => ({
    commit,
    discardSourceSession,
    preserveEditsForNextReviewGeneration: () => {
      preserveEditsForNextReviewGenerationRef.current = true
    },
  }))

  if (requirements.length === 0) return null

  const updateName = (key: string, name: string) => {
    setNames(current => ({ ...current, [key]: name.toUpperCase().replace(/[\s.-]+/g, '_') }))
    setError('')
  }

  const updateValue = (key: string, value: string) => {
    setValues(current => {
      const next = { ...current, [key]: value }
      valuesRef.current = next
      return next
    })
    setError('')
  }

  const hasSourceValues = groups.some(group => group.requirements.some(requirement => requirement.source_value_available))
  const sourceMigrationOffered = sourceMigrationAllowed && (sourceMigrationControlled || Boolean(sourceSecretSessionId))
  const sourceMigrationSelectable = sourceMigrationOffered && !sourceSessionUnavailable
  const providerLabel = storeProvider || 'project secret store'

  return (
    <section className="secret-binding-review credentials-card" aria-labelledby={titleId}>
      <div className="secret-binding-review-header">
        <div>
          <h4 id={titleId}>Credentials ({groups.length})</h4>
          <p>
            Required by this script; stored in Project Secrets. Only scripts referencing this logical name are affected.
            Add values now if you want, or configure them later in Project Secrets; the script stores only <code>{'{{secret.NAME}}'}</code> references.
          </p>
        </div>
        {!onPreparedChange && (
          <button className="btn btn-primary" type="button" onClick={() => { void commit() }} disabled={!prepared.valid || applying || disabled}>
            {applying ? 'Applying...' : 'Continue with credentials'}
          </button>
        )}
      </div>

      <div className="credentials-card-summary" role="status">
        <span>{prepared.configureLaterCount} to configure later</span>
        <span>{prepared.secrets.length} value{prepared.secrets.length === 1 ? '' : 's'} ready to create</span>
        <span>
          {probeState === 'loading'
            ? 'Checking project secret store…'
            : probeState === 'unavailable'
              ? 'Credential status unavailable'
              : `${providerLabel}${storeWritable ? '' : ' · read-only'}`}
        </span>
      </div>

      {hasSourceValues && sourceMigrationOffered && (
        <label className="credentials-source-migration">
          <input
            type="checkbox"
            checked={effectiveSourceMigrationSelected}
            disabled={applying || disabled || !sourceMigrationSelectable}
            onChange={event => {
              if (sourceMigrationControlled) {
                onExternalSourceMigrationChange(event.target.checked)
              } else {
                setMigrateSourceValues(event.target.checked)
              }
            }}
          />
          <span>
            Import detected credential values
            <small>Admin-only, create-only migration. Existing project secrets are never overwritten.</small>
          </span>
        </label>
      )}

      {probeState === 'unavailable' && (
        <div className="secret-binding-review-error" role="alert">
          <span>{probeError || 'Unable to check project credential status.'} Values remain optional, and safe references can still be saved.</span>{' '}
          <button
            className="btn btn-secondary btn-sm"
            type="button"
            disabled={applying || disabled}
            onClick={() => setProbeRetryGeneration(current => current + 1)}
          >
            Retry credential check
          </button>
        </div>
      )}
      {sourceSessionNotice && <div className="secret-binding-review-error" role="alert">{sourceSessionNotice}</div>}
      {error && <div className="secret-binding-review-error" role="alert">{error}</div>}

      <ol className="secret-binding-review-list">
        {groups.map((group, index) => {
          const inputId = `${idPrefix}-secret-binding-${index}`
          const valueId = `${inputId}-value`
          const helpId = `${inputId}-help`
          const nameErrorId = `${inputId}-name-error`
          const valueConflictErrorId = `${inputId}-value-conflict-error`
          const name = names[group.key] ?? ''
          const nameError = validationErrors[group.key]
          const configured = statuses.get(name.trim())?.configured === true
          const sourceAvailable = group.requirements.some(requirement => requirement.source_value_available)
          const matchingGroups = groups.filter(candidate => (names[candidate.key] || '').trim() === name.trim())
          const finalNameHasManualValue = matchingGroups.some(candidate => Boolean(values[candidate.key]))
          const finalNameHasSourceValue = matchingGroups.some(candidate => candidate.requirements.some(requirement => requirement.source_value_available))
          const sourceAvailableForMigration = sourceAvailable && sourceMigrationSelectable
          const willMigrate = effectiveSourceMigrationSelected && finalNameHasSourceValue
          const willCreate = (finalNameHasManualValue && probeState === 'ready' && storeWritable) || willMigrate
          const status = configured
            ? 'Configured'
            : willCreate
              ? 'Will be created'
              : sourceAvailableForMigration
                ? 'Available from source'
                : 'Configure later'
          const statusTone = configured || willCreate ? 'healthy' : sourceAvailableForMigration ? 'warning' : ''
          const valueConflict = conflictingValueNames.has(name.trim())

          return (
            <li key={group.key} className="secret-binding-review-item">
              <div className="secret-binding-review-location">
                <div className="secret-binding-review-label-row">
                  <strong>{group.requirements[0]?.label || 'Credential'}</strong>
                  <span className={`script-health-token ${statusTone}`}>{status}</span>
                  {group.requirements.length > 1 && (
                    <span className="secret-binding-review-reuse-badge">{group.requirements.length} locations</span>
                  )}
                </div>
                {group.requirements[0]?.context && <p>{group.requirements[0].context}</p>}
                <ul className="credentials-location-list">
                  {group.requirements.map(requirement => <li key={requirement.id}><code>{requirement.path}</code></li>)}
                </ul>
              </div>
              <div className="secret-binding-review-control">
                <label htmlFor={inputId}>Secret reference name</label>
                <input
                  id={inputId}
                  type="text"
                  value={name}
                  onChange={event => updateName(group.key, event.target.value)}
                  disabled={applying || disabled}
                  aria-describedby={[
                    helpId,
                    nameError ? nameErrorId : '',
                    valueConflict ? valueConflictErrorId : '',
                  ].filter(Boolean).join(' ')}
                  aria-invalid={nameError || valueConflict ? 'true' : 'false'}
                  autoComplete="off"
                  spellCheck={false}
                />
                <span id={helpId} className="secret-binding-review-reference">
                  Script reference: <code>{name.trim() ? `{{secret.${name.trim()}}}` : '{{secret.NAME}}'}</code>
                </span>
                {nameError && <span id={nameErrorId} className="secret-binding-review-field-error">{nameError}</span>}
                {valueConflict && <span id={valueConflictErrorId} className="secret-binding-review-field-error">Reused names must use the same optional value.</span>}

                <label htmlFor={valueId}>Secret value (optional)</label>
                <input
                  id={valueId}
                  type="password"
                  value={values[group.key] || ''}
                  onChange={event => updateValue(group.key, event.target.value)}
                  disabled={applying || disabled || !projectId || probeState !== 'ready' || !storeWritable || configured}
                  autoComplete="new-password"
                  aria-label={`Secret value for ${name || group.requirements[0]?.label || 'credential'}`}
                  placeholder={configured
                    ? 'Already configured'
                    : !projectId
                      ? 'Select a project to enter a value'
                      : probeState === 'loading'
                        ? 'Checking project secret store'
                        : probeState === 'unavailable'
                          ? 'Retry the credential check to enter a value'
                          : storeWritable
                            ? 'Enter now or leave blank'
                            : 'Provider is read-only'}
                />
              </div>
            </li>
          )
        })}
      </ol>
    </section>
  )
})

export default SecretBindingReview
