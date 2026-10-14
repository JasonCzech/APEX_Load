import {
  forwardRef,
  memo,
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
import {
  type ProjectSecretStatus,
  type ProjectSecretStatusesResponse,
  type ProjectSecretWrite,
} from '../types/secrets'
import { getErrorMessage } from '../utils/errorUtils'
import { collisionSafeSecretName, isValidSecretBindingName, MAX_SECRET_BINDING_NAME_LENGTH } from './secretBindingUtils'
import {
  formatSourceSecretSessionFailure,
  getSourceSecretSessionFailureMetadata,
  sourceSecretSessionIsExpired,
} from './sourceSecretSession'
import { createProjectSecretsInBatches } from './secretBindingBatches'
import SecretReferenceValueControl from './SecretReferenceValueControl'
import ImportCategoryHeading from './ImportCategoryHeading'
import { credentialNoun } from './secretBindingNoun'
import './ScriptImportReview.css'

interface Props {
  inlineEditing?: boolean
  onEditingChange?: (editing: boolean) => void
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
  onErrorChange?: (message: string) => void
  onSourceSessionSettled?: (
    sourceSecretSessionId: string,
    migrationSelections: SecretBindingSelection[],
  ) => void
  onPreparedChange?: (prepared: PreparedSecretBindings) => void
  onProjectSecretsConfigured?: (names: readonly string[]) => void
  onRequirementDestinationStateChange?: (
    state: Record<string, RequirementDestinationState>
  ) => void
  disabled?: boolean
}

interface RequirementGroup {
  key: string
  requirements: SecretBindingRequirement[]
  suggestedName: string
  // baseName is the importer's suggestion before per-scope de-duplication
  // renames it. The reuse control reads the credential kind out of it, which
  // the renamed suggestedName would no longer carry cleanly.
  baseName: string
  destinationClass: string
}

type DestinationChoice = 'preserve' | 'project-secret'
type PreservationUnavailableReason = NonNullable<SecretBindingRequirement['preservation_unavailable_reason']>

const preservationUnavailableMessages: Record<PreservationUnavailableReason, string> = {
  source_value_unavailable: 'The source did not provide one exact recoverable value. It contains an unresolved reference, a redacted placeholder, or conflicting values. Use a project secret and configure its value before execution.',
  existing_secret_reference: 'The source already uses a governed secret reference rather than a literal value. Configure that project secret; there is no imported value to preserve.',
  destination_mapping_incomplete: 'The importer found an exact source value but could not prove it can restore every generated destination occurrence. Use a project secret to avoid silently changing or dropping the credential.',
}

const genericPreservationUnavailableMessage = 'The server could not verify an exact imported value at every generated destination. Use a project secret for this credential.'

export interface PreparedSecretBindings {
  valid: boolean
  configureLaterCount: number
  selectedBindingCount: number
}

export interface RequirementDestinationState {
  readyForExecution?: boolean
  usesProjectSecret: boolean
  secretName: string
  guidance: string
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
  /** Checks the secrets again, keeping the statuses shown until it answers (after secrets changed elsewhere). */
  refreshStatuses: () => void
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
    // A preserved occurrence and an unresolved occurrence are different
    // destination decisions even when the importer suggests the same logical
    // name. Grouping them together would make the unresolved occurrence force
    // an otherwise runnable imported literal into Project Secrets.
    const destinationClass = requirement.destination_value_preserved === true
      ? 'preserved'
      : 'binding-required'
    const key = requirement.value_group_id && requirement.destination_value_preserved
      ? `${scope}:value:${requirement.value_group_id}`
      : requirement.destination_value_preserved
        ? `${scope}:requirement:${requirement.id}`
        : suggestedName
      ? `${scope}:name:${suggestedName}:destination:${destinationClass}`
      : `${scope}:requirement:${requirement.id}`
    const existing = groups.get(key)
    if (existing) {
      existing.requirements.push(requirement)
    } else {
      groups.set(key, { key, requirements: [requirement], suggestedName, baseName: suggestedName, destinationClass })
    }
  }
  const result = Array.from(groups.values())
  const usedNames = new Set<string>()
  const nextSuffixByBase = new Map<string, number>()
  for (const group of result) {
    const base = group.suggestedName
    if (!base || !usedNames.has(base)) {
      if (base) usedNames.add(base)
      continue
    }
    let suffix = nextSuffixByBase.get(base) ?? 2
    let candidate = base
    do {
      const suffixText = `_${suffix}`
      candidate = `${base.slice(0, MAX_SECRET_BINDING_NAME_LENGTH - suffixText.length)}${suffixText}`
      suffix += 1
    } while (usedNames.has(candidate))
    nextSuffixByBase.set(base, suffix)
    group.suggestedName = candidate
    usedNames.add(candidate)
  }
  return result
}

function stableSortedUniqueNonEmptyStrings(values: readonly string[]): readonly string[] {
  let previous = ''
  for (let index = 0; index < values.length; index += 1) {
    const value = values[index]
    if (!value || (index > 0 && value <= previous)) {
      return Array.from(new Set(values.filter(Boolean))).sort()
    }
    previous = value
  }
  return values
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
      source_variable: requirement.source_variable ?? '',
      unresolved_source_variable: requirement.unresolved_source_variable === true,
      affected_paths: stableSortedUniqueNonEmptyStrings(requirement.affected_paths || []),
      script_index: requirement.script_index ?? null,
      script_id: requirement.script_id ?? '',
      script_name: requirement.script_name ?? '',
      source_value_available: requirement.source_value_available === true,
      destination_value_preserved: requirement.destination_value_preserved === true,
      value_group_id: requirement.value_group_id ?? '',
      preservation_unavailable_reason: requirement.preservation_unavailable_reason ?? '',
      preservation_unavailable_description: requirement.preservation_unavailable_description ?? '',
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

function groupCanPreserveDestinationValue(group: RequirementGroup): boolean {
  return (
    group.requirements.length > 0
    && group.requirements.every(requirement => requirement.destination_value_preserved === true)
  )
}

function groupPreservationUnavailableMessages(
  group: RequirementGroup,
  unresolvedSourceNextStep = 'Enter the value now or configure it in Project Secrets before running the imported scripts.',
): string[] {
  const messages = new Set<string>()
  for (const requirement of group.requirements) {
    const reason = requirement.preservation_unavailable_reason
    if (reason && requirement.preservation_unavailable_description) {
      messages.add(`${requirement.preservation_unavailable_description} ${unresolvedSourceNextStep}`)
    } else if (reason === 'source_value_unavailable' && requirement.source_variable && requirement.unresolved_source_variable) {
      messages.add(`No value for {{${requirement.source_variable}}} was included in the imported collection. There is no imported value to preserve. ${unresolvedSourceNextStep}`)
    } else if (reason) {
      messages.add(preservationUnavailableMessages[reason] ?? genericPreservationUnavailableMessage)
    }
  }
  if (messages.size === 0) {
    messages.add(genericPreservationUnavailableMessage)
  }
  return Array.from(messages)
}

function groupAffectedPaths(group: RequirementGroup): readonly string[] {
  if (group.requirements.length === 1) {
    const requirement = group.requirements[0]
    const paths = requirement.affected_paths?.length
      ? requirement.affected_paths
      : requirement.path ? [requirement.path] : []
    return stableSortedUniqueNonEmptyStrings(paths)
  }
  return stableSortedUniqueNonEmptyStrings(group.requirements.flatMap(requirement => (
    requirement.affected_paths?.length ? requirement.affected_paths : [requirement.path]
  )))
}

function groupSourceVariables(group: RequirementGroup): string[] {
  return Array.from(new Set(group.requirements
    .map(requirement => requirement.source_variable?.trim() || '')
    .filter(Boolean))).sort()
}

function groupHasAffectedSourcePaths(group: RequirementGroup): boolean {
  return group.requirements.some(requirement => requirement.affected_paths?.some(path =>
    !/^\/(?:actions|init|teardown|config|parameters|data_sources)(?:\/|$)/.test(path)))
}

function initialDestinationChoices(groups: RequirementGroup[]): Record<string, DestinationChoice> {
  return Object.fromEntries(groups.map(group => [
    group.key,
    groupCanPreserveDestinationValue(group) ? 'preserve' : 'project-secret',
  ]))
}

function effectiveDestinationChoice(
  group: RequirementGroup,
  choices: Readonly<Record<string, DestinationChoice>>,
): DestinationChoice {
  if (!groupCanPreserveDestinationValue(group)) return 'project-secret'
  return choices[group.key] === 'project-secret' ? 'project-secret' : 'preserve'
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

const MAX_COLLISION_CANDIDATES = 256
const COLLISION_PROBE_BATCH_SIZE = 32
const PROJECT_SECRET_STATUS_BATCH_SIZE = 256
const PROJECT_SECRET_STATUS_CONCURRENCY = 4
const CREDENTIAL_GROUP_PAGE_SIZE = 100
const AFFECTED_PATH_PAGE_SIZE = 100
const EMPTY_COMMITTED_VALUE_NAMES: ReadonlySet<string> = new Set()
const EMPTY_COLLISION_RESOLVED_NAMES: ReadonlyMap<string, string> = new Map()
const EMPTY_PROJECT_SECRET_STATUSES: ReadonlyMap<string, ProjectSecretStatus> = new Map()

type ProjectSecretStatusSnapshot = {
  statuses: Map<string, ProjectSecretStatus>
  storeProvider: string
  storeWritable: boolean
}

function projectSecretStatusSnapshot(
  responses: readonly ProjectSecretStatusesResponse[],
  expectedProjectId?: string,
): ProjectSecretStatusSnapshot {
  const statuses = new Map<string, ProjectSecretStatus>()
  for (const response of responses) {
    for (const [name, status] of normalizeStatusMap(response)) statuses.set(name, status)
  }
  const firstCapabilities = responses[0]?.capabilities
  const provider = firstCapabilities?.provider || ''
  const capabilitiesComplete = responses.length > 0
    && provider !== ''
    && typeof firstCapabilities?.writable === 'boolean'
    && typeof firstCapabilities?.create_supported === 'boolean'
  const capabilitiesAgree = capabilitiesComplete && responses.every(response => {
    const capabilities = response.capabilities
    return (
      (!expectedProjectId || response.project_id === expectedProjectId)
      && capabilities?.provider === provider
      && capabilities?.writable === firstCapabilities?.writable
      && capabilities?.create_supported === firstCapabilities?.create_supported
      && capabilities?.replace_supported === firstCapabilities?.replace_supported
      && capabilities?.source_secret_migration_policy_enabled === firstCapabilities?.source_secret_migration_policy_enabled
    )
  })
  return {
    statuses,
    storeProvider: capabilitiesAgree ? provider : '',
    storeWritable: capabilitiesAgree
      && firstCapabilities?.writable === true
      && firstCapabilities?.create_supported === true,
  }
}

async function getProjectSecretStatusesBatched(
  getStatuses: (projectId: string, names: string[]) => Promise<ProjectSecretStatusesResponse>,
  projectId: string,
  names: readonly string[],
): Promise<ProjectSecretStatusSnapshot> {
  const batches = Array.from(
    { length: Math.ceil(names.length / PROJECT_SECRET_STATUS_BATCH_SIZE) },
    (_, index) => names.slice(
      index * PROJECT_SECRET_STATUS_BATCH_SIZE,
      (index + 1) * PROJECT_SECRET_STATUS_BATCH_SIZE,
    ),
  )
  const responses = new Array<ProjectSecretStatusesResponse>(batches.length)
  let nextBatch = 0
  await Promise.all(Array.from(
    { length: Math.min(PROJECT_SECRET_STATUS_CONCURRENCY, batches.length) },
    async () => {
      while (nextBatch < batches.length) {
        const batchIndex = nextBatch
        nextBatch += 1
        responses[batchIndex] = await getStatuses(projectId, [...batches[batchIndex]])
      }
    },
  ))

  return projectSecretStatusSnapshot(responses, projectId)
}

function destinationGuidance(
  configuredNameCollision: boolean,
  configured: boolean,
  willCreate: boolean,
): string {
  if (configuredNameCollision) {
    return 'A value will be stored under a new collision-safe project-secret name during import.'
  }
  if (configured) {
    return 'The selected project secret is already configured and will be used when the imported scripts run.'
  }
  if (willCreate) return 'A value will be stored in Project Secrets during import.'
  return 'Enter the value now or configure it in Project Secrets before running the imported scripts.'
}

const AffectedPathsDisclosure = memo(function AffectedPathsDisclosure({
  id,
  paths,
  hasAffectedSourcePaths,
}: {
  id: string
  paths: readonly string[]
  hasAffectedSourcePaths: boolean
}) {
  const [expanded, setExpanded] = useState(false)
  const [page, setPage] = useState(0)
  if (paths.length === 0) return null
  if (paths.length === 1) {
    return (
      <ul className="credentials-location-list">
        <li><code>{paths[0]}</code></li>
      </ul>
    )
  }

  const locationLabel = hasAffectedSourcePaths ? 'Affected request files' : 'Affected locations'
  const locationLabelLower = hasAffectedSourcePaths ? 'affected request files' : 'affected locations'
  const pageCount = Math.ceil(paths.length / AFFECTED_PATH_PAGE_SIZE)
  const safePage = Math.min(page, pageCount - 1)
  const firstIndex = safePage * AFFECTED_PATH_PAGE_SIZE
  const visiblePaths = expanded
    ? paths.slice(firstIndex, firstIndex + AFFECTED_PATH_PAGE_SIZE)
    : []
  const lastIndex = firstIndex + visiblePaths.length
  const summaryId = `${id}-summary`
  const regionId = `${id}-region`
  const statusId = `${id}-status`

  return (
    <details
      className="credentials-location-details"
      onToggle={event => {
        const nextExpanded = event.currentTarget.open
        setExpanded(nextExpanded)
        if (!nextExpanded) setPage(0)
      }}
    >
      <summary id={summaryId}>
        View {paths.length} {locationLabelLower}
      </summary>
      {expanded && (
        <div
          id={regionId}
          className="credentials-location-scroll-region"
          role="region"
          aria-labelledby={summaryId}
          aria-describedby={statusId}
          tabIndex={0}
        >
          <p id={statusId} className="credentials-location-page-status" aria-live="polite">
            Showing {firstIndex + 1}–{lastIndex} of {paths.length} {locationLabelLower}. Page {safePage + 1} of {pageCount}.
          </p>
          <ul
            className="credentials-location-list"
            aria-label={`${locationLabel}, items ${firstIndex + 1} through ${lastIndex} of ${paths.length}`}
          >
            {visiblePaths.map(affectedPath => <li key={affectedPath}><code>{affectedPath}</code></li>)}
          </ul>
          {pageCount > 1 && (
            <div
              className="credentials-location-pagination"
              role="group"
              aria-label={`${locationLabel} pages`}
            >
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                disabled={safePage === 0}
                onClick={() => setPage(Math.max(0, safePage - 1))}
              >
                Previous {AFFECTED_PATH_PAGE_SIZE}
              </button>
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                disabled={safePage >= pageCount - 1}
                onClick={() => setPage(Math.min(pageCount - 1, safePage + 1))}
              >
                Next {AFFECTED_PATH_PAGE_SIZE}
              </button>
            </div>
          )}
        </div>
      )}
    </details>
  )
})

const SecretBindingReview = forwardRef<SecretBindingReviewHandle, Props>(function SecretBindingReview({
  requirements,
  inlineEditing = false,
  onEditingChange,
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
  onErrorChange,
  onSourceSessionSettled,
  onPreparedChange,
  onProjectSecretsConfigured,
  onRequirementDestinationStateChange,
  disabled = false,
}, ref) {
  const api = useApi()
  const idPrefix = useId().replace(/:/g, '')
  const titleId = `${idPrefix}-secret-binding-review-title`
  const requirementsIdentity = useMemo(
    () => semanticRequirementsIdentity(requirements),
    [requirements],
  )
  const groupSnapshotRef = useRef<{ identity: string; groups: RequirementGroup[] } | null>(null)
  if (groupSnapshotRef.current?.identity !== requirementsIdentity) {
    groupSnapshotRef.current = {
      identity: requirementsIdentity,
      groups: groupRequirements(requirements),
    }
  }
  const groups = groupSnapshotRef.current.groups
  const groupPresentationByKey = useMemo(() => new Map(groups.map(group => [
    group.key,
    {
      affectedPaths: groupAffectedPaths(group),
      sourceVariables: groupSourceVariables(group),
      hasAffectedSourcePaths: groupHasAffectedSourcePaths(group),
    },
  ])), [groups])
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
  const [editing, setEditing] = useState(false)
  const editSnapshot = useRef<{ names: Record<string, string>; choices: Record<string, DestinationChoice>; migration: boolean } | null>(null)
  const editingCallbackRef = useRef(onEditingChange)
  editingCallbackRef.current = onEditingChange
  useEffect(() => { editingCallbackRef.current?.(inlineEditing && editing) }, [inlineEditing, editing])
  useEffect(() => () => editingCallbackRef.current?.(false), [])
  useEffect(() => {
    if (!inlineEditing || !editing) return
    const warn = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = '' }
    window.addEventListener('beforeunload', warn)
    return () => window.removeEventListener('beforeunload', warn)
  }, [inlineEditing, editing])
  const [names, setNames] = useState<Record<string, string>>(() => initialGroupValues(groups))
  const [destinationChoices, setDestinationChoices] = useState<Record<string, DestinationChoice>>(
    () => initialDestinationChoices(groups),
  )
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
  const [credentialPage, setCredentialPage] = useState(0)
  const [credentialSearch, setCredentialSearch] = useState('')
  const [credentialState, setCredentialState] = useState('all')
  const [attentionOnly, setAttentionOnly] = useState(false)
  const [pendingReuse, setPendingReuse] = useState<string | null>(null)
  const resetReviewIdentityRef = useRef(reviewIdentity)
  const credentialScopeIdentityRef = useRef(credentialScopeIdentity)
  const settledSourceSessionRef = useRef('')
  const notifiedSourceSessionRef = useRef('')
  // Only names whose create-only provider mutation was confirmed belong here.
  // Collision candidates remain speculative until create/migration completes.
  const committedValueNamesRef = useRef(new Set<string>())
  const committedValueNamesScopeRef = useRef(credentialScopeIdentity)
  const [committedValueNamesVersion, setCommittedValueNamesVersion] = useState(0)
  const collisionResolvedNamesRef = useRef(new Map<string, string>())
  const discardSourceSessionInFlightRef = useRef<{ id: string; promise: Promise<void> } | null>(null)
  const mountedRef = useRef(true)
  const activeSourceSessionIdentityRef = useRef(`${projectId || ''}\u0000${sourceSecretSessionId || ''}`)
  const sourceSessionPropIdentityRef = useRef(activeSourceSessionIdentityRef.current)
  activeSourceSessionIdentityRef.current = `${projectId || ''}\u0000${sourceSecretSessionId || ''}`
  const committedStateMatchesScope = committedValueNamesScopeRef.current === credentialScopeIdentity
  const committedValueNames = committedStateMatchesScope
    ? committedValueNamesRef.current
    : EMPTY_COMMITTED_VALUE_NAMES
  const committedValueNamesSnapshot = useMemo(() => ({
    names: committedValueNames,
    version: committedValueNamesVersion,
  }), [committedValueNames, committedValueNamesVersion])
  const collisionResolvedNames = committedStateMatchesScope
    ? collisionResolvedNamesRef.current
    : EMPTY_COLLISION_RESOLVED_NAMES
  const credentialEvidenceMatchesScope = credentialScopeIdentityRef.current === credentialScopeIdentity
  const scopedStatuses = credentialEvidenceMatchesScope ? statuses : EMPTY_PROJECT_SECRET_STATUSES
  const scopedStoreProvider = credentialEvidenceMatchesScope ? storeProvider : ''
  const scopedStoreWritable = credentialEvidenceMatchesScope ? storeWritable : false
  const scopedProbeState = credentialEvidenceMatchesScope ? probeState : 'idle'

  useEffect(() => {
    if (resetReviewIdentityRef.current !== reviewIdentity) {
      // Review-only actions (tab switches, runtime edits, selector repair, and
      // parameterization) legitimately advance the generation. Keep write-only
      // edits when the credential scope itself is unchanged.
      const preserveEdits = credentialScopeIdentityRef.current === credentialScopeIdentity
      resetReviewIdentityRef.current = reviewIdentity
      if (!preserveEdits) {
        setEditing(false)
        editSnapshot.current = null
        setNames(initialGroupValues(groups))
        setDestinationChoices(initialDestinationChoices(groups))
        valuesRef.current = {}
        setValues({})
        committedValueNamesScopeRef.current = credentialScopeIdentity
        committedValueNamesRef.current.clear()
        setCommittedValueNamesVersion(current => current + 1)
        collisionResolvedNamesRef.current.clear()
        setStatuses(new Map())
        setStoreProvider('')
        setStoreWritable(false)
        setProbeState('idle')
        setProbeError('')
        setMigrateSourceValues(false)
        setSourceSessionUnavailable(false)
        setSourceSessionNotice('')
        setCredentialPage(0)
        setPendingReuse(null)
        setCredentialSearch('')
        setAttentionOnly(false)
      }
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

  useEffect(() => {
    mountedRef.current = true
    const committedValueNamesForThisMount = committedValueNamesRef.current
    const collisionResolvedNamesForThisMount = collisionResolvedNamesRef.current
    return () => {
      mountedRef.current = false
      editSnapshot.current = null
      // Write-only values never leave this card. Credential-scope changes clear
      // the ref above; unmount always drops the final retained object.
      valuesRef.current = {}
      committedValueNamesForThisMount.clear()
      collisionResolvedNamesForThisMount.clear()
    }
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
      if (effectiveDestinationChoice(group, destinationChoices) !== 'project-secret') continue
      const name = (names[group.key] || '').trim()
      const value = values[group.key] || ''
      if (!name || !value) continue
      const seen = valuesByName.get(name) || new Set<string>()
      seen.add(value)
      valuesByName.set(name, seen)
    }
    return new Set(Array.from(valuesByName.entries()).filter(([, seen]) => seen.size > 1).map(([name]) => name))
  }, [destinationChoices, groups, names, values])

  const activeValidNames = useMemo(
    () => Array.from(new Set(groups
      .filter(group => effectiveDestinationChoice(group, destinationChoices) === 'project-secret')
      .map(group => (names[group.key] || '').trim())
      .filter(isValidSecretBindingName))).sort(),
    [destinationChoices, groups, names],
  )

  const visibleRequirementIDs = useMemo(
    () => new Set(requirements.map(requirement => requirement.id)),
    [requirements],
  )

  const sourceMigrationControlled = typeof onExternalSourceMigrationChange === 'function'
  const sourceMigrationSelected = sourceMigrationControlled ? externalSourceMigrationSelected : migrateSourceValues
  const effectiveSourceMigrationSelected = sourceMigrationSelected && !sourceSessionUnavailable

  const computedUniqueValidNames = useMemo(
    () => Array.from(new Set([
      ...activeValidNames,
      ...(effectiveSourceMigrationSelected
        ? migrationGroups
          .filter(group => group.requirements.some(requirement => !visibleRequirementIDs.has(requirement.id)))
          .map(group => group.suggestedName.trim())
        : []),
    ].filter(isValidSecretBindingName))).sort(),
    [activeValidNames, effectiveSourceMigrationSelected, migrationGroups, visibleRequirementIDs],
  )
  // Importers rebuild requirement arrays on every render, and unrelated polling
  // re-renders them constantly. Key the status probe on the names themselves so
  // an equal list keeps its identity instead of issuing another request.
  const uniqueValidNamesKey = computedUniqueValidNames.join('\u0000')
  const uniqueValidNames = useMemo(
    () => (uniqueValidNamesKey ? uniqueValidNamesKey.split('\u0000') : []),
    [uniqueValidNamesKey],
  )
  // Project secrets are usually configured on another page, tab, or window.
  // Re-check when the operator returns instead of on every re-render. A commit
  // in flight probes for itself, and a background re-check keeps showing the
  // last known statuses rather than flashing "checking" on every focus.
  const statusRefreshAtRef = useRef(0)
  const backgroundStatusRefreshRef = useRef(false)
  const applyingRef = useRef(applying)
  applyingRef.current = applying
  useEffect(() => {
    if (!projectId || !uniqueValidNamesKey) return
    const refresh = () => {
      if (document.visibilityState !== 'visible' || applyingRef.current) return
      // Returning to a tab fires both visibilitychange and focus.
      const now = Date.now()
      if (now - statusRefreshAtRef.current < 1000) return
      statusRefreshAtRef.current = now
      backgroundStatusRefreshRef.current = true
      setProbeRetryGeneration(current => current + 1)
    }
    document.addEventListener('visibilitychange', refresh)
    window.addEventListener('focus', refresh)
    return () => {
      document.removeEventListener('visibilitychange', refresh)
      window.removeEventListener('focus', refresh)
    }
  }, [projectId, uniqueValidNamesKey])

  useEffect(() => {
    if (!sourceSecretSessionExpiresAt) return
    const expiresAt = Date.parse(sourceSecretSessionExpiresAt)
    if (!Number.isFinite(expiresAt)) return
    const expireSession = () => {
      setSourceSessionUnavailable(true)
      setSourceSessionNotice('Optional source-value migration into Project Secrets has expired. Any preserved imported destination values remain runnable; credentials set to Use project secret can be configured manually, or you can re-run the import to migrate them.')
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
    const background = backgroundStatusRefreshRef.current
    backgroundStatusRefreshRef.current = false
    if (!background) {
      setProbeState('loading')
      setProbeError('')
    }
    const statusRequest = uniqueValidNames.length <= PROJECT_SECRET_STATUS_BATCH_SIZE
      ? api.getProjectSecretStatuses(projectId, uniqueValidNames)
        .then(response => projectSecretStatusSnapshot([response], projectId))
      : getProjectSecretStatusesBatched(api.getProjectSecretStatuses, projectId, uniqueValidNames)
    void statusRequest
      .then(response => {
        if (cancelled) return
        setStatuses(response.statuses)
        setStoreProvider(response.storeProvider)
        setStoreWritable(response.storeWritable)
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
    const selectedNames = new Set<string>()
    const activeNamesByRequirement = new Map<string, string>()
    let valid = groups.length > 0

    for (const group of groups) {
      if (effectiveDestinationChoice(group, destinationChoices) !== 'project-secret') continue
      const name = (names[group.key] || '').trim()
      const value = values[group.key] || ''
      const sourceValueAvailable = group.requirements.some(requirement => requirement.source_value_available)
      if (validationErrors[group.key] || conflictingValueNames.has(name)) valid = false
      if (name) selectedNames.add(name)
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
      if (name && value && !committedValueNamesSnapshot.names.has(name)) {
        writesByName.set(name, value)
      }
      if (name && sourceValueAvailable) {
        sourceAvailableNames.add(name)
      }
    }
    const migrationSelections = migrationGroups.flatMap(group => group.requirements.flatMap(requirement => {
      const activeName = activeNamesByRequirement.get(requirement.id)
      if (!activeName && (
        visibleRequirementIDs.has(requirement.id)
        || requirement.destination_value_preserved === true
      )) return []
      const migrationName = activeName || group.suggestedName.trim()
      return [{
        requirement_id: requirement.id,
        secret_name: collisionResolvedNames.get(migrationName) || migrationName,
      }]
    })).filter(selection => isValidSecretBindingName(selection.secret_name))

    const pendingNames = new Set<string>()
    for (const name of selectedNames) {
      const configured = scopedStatuses.get(name)?.configured === true
        || committedValueNamesSnapshot.names.has(name)
      const hasManualValue = writesByName.has(name)
      const willMigrate = effectiveSourceMigrationSelected && sourceAvailableNames.has(name)
      if (!configured && !hasManualValue && !willMigrate) pendingNames.add(name)
    }

    return {
      bindings,
      secrets: Array.from(writesByName, ([name, value]) => ({ name, value })),
      valid,
      configureLaterCount: pendingNames.size,
      selectedBindingCount: bindings.length,
      // Externally controlled migrations (for example Bruno commits) are
      // performed by that import's atomic server-side commit, not by the
      // generic source-session endpoint used by this component.
      migrateSourceValues: !sourceMigrationControlled && effectiveSourceMigrationSelected,
      migrationSelections,
    }
  }, [collisionResolvedNames, committedValueNamesSnapshot, conflictingValueNames, destinationChoices, effectiveSourceMigrationSelected, groups, migrationGroups, names, scopedStatuses, sourceMigrationControlled, validationErrors, values, visibleRequirementIDs])

  const preparedSummary = useMemo<PreparedSecretBindings>(() => ({
    valid: prepared.valid,
    configureLaterCount: prepared.configureLaterCount,
    selectedBindingCount: prepared.selectedBindingCount,
  }), [prepared.configureLaterCount, prepared.selectedBindingCount, prepared.valid])

  const destinationValueStateByName = useMemo(() => {
    const stateByName = new Map<string, {
      hasManualValue: boolean
      hasSourceValue: boolean
    }>()
    for (const group of groups) {
      if (effectiveDestinationChoice(group, destinationChoices) !== 'project-secret') continue
      const name = (names[group.key] || '').trim()
      const current = stateByName.get(name) || {
        hasManualValue: false,
        hasSourceValue: false,
      }
      current.hasManualValue ||= Boolean(values[group.key])
      current.hasSourceValue ||= group.requirements.some(requirement => requirement.source_value_available)
      stateByName.set(name, current)
    }
    return stateByName
  }, [destinationChoices, groups, names, values])

  const requirementDestinationState = useMemo(() => {
    const state: Record<string, RequirementDestinationState> = {}
    for (const group of groups) {
      const bindingSelected = effectiveDestinationChoice(group, destinationChoices) === 'project-secret'
      const name = (names[group.key] || '').trim()
      if (!bindingSelected) {
        for (const requirement of group.requirements) {
          state[requirement.id] = {
            usesProjectSecret: false,
            readyForExecution: true,
            secretName: '',
            guidance: 'The imported value remains preserved in the script.',
          }
        }
        continue
      }
      const configured = scopedStatuses.get(name)?.configured === true
        || committedValueNamesSnapshot.names.has(name)
      const destinationValueState = destinationValueStateByName.get(name)
      const finalNameHasManualValue = destinationValueState?.hasManualValue === true
      const finalNameHasSourceValue = destinationValueState?.hasSourceValue === true
      const willMigrate = effectiveSourceMigrationSelected && finalNameHasSourceValue
      const willCreate = !committedValueNamesSnapshot.names.has(name) && (
        (finalNameHasManualValue && scopedProbeState === 'ready' && scopedStoreWritable) || willMigrate
      )
      const configuredNameCollision = configured && willCreate
      const nextStep = destinationGuidance(configuredNameCollision, configured, willCreate)
      for (const requirement of group.requirements) {
        state[requirement.id] = {
          usesProjectSecret: true,
          readyForExecution: !validationErrors[group.key] && !conflictingValueNames.has(name) && !configuredNameCollision && (configured || willCreate),
          secretName: name,
          guidance: nextStep,
        }
      }
    }
    return state
  }, [committedValueNamesSnapshot, destinationChoices, destinationValueStateByName, effectiveSourceMigrationSelected, groups, names, scopedProbeState, scopedStatuses, scopedStoreWritable, validationErrors, conflictingValueNames])

  useEffect(() => {
    // Never lift write-only values, binding payloads, or migration intent into
    // a parent component. Parents only need safe CTA/validation metadata.
    onPreparedChange?.(preparedSummary)
  }, [onPreparedChange, preparedSummary])

  useEffect(() => {
    onRequirementDestinationStateChange?.(requirementDestinationState)
  }, [onRequirementDestinationStateChange, requirementDestinationState])

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
    const editingSession = editSnapshot.current
    if (committedValueNamesScopeRef.current !== credentialScopeIdentity) {
      committedValueNamesScopeRef.current = credentialScopeIdentity
      committedValueNamesRef.current.clear()
      collisionResolvedNamesRef.current.clear()
    }
    const generation = reviewGenerationRef.current
    const sourceIdentity = reviewIdentity
    const requestIsCurrent = () => (
      reviewGenerationRef.current === generation
      && activeReviewIdentityRef.current === sourceIdentity
    )
    const recordCommittedValueNames = (committedNames: Iterable<string>) => {
      if (
        !mountedRef.current
        || committedValueNamesScopeRef.current !== credentialScopeIdentity
      ) return
      let changed = false
      const newlyCommittedNames: string[] = []
      for (const name of committedNames) {
        if (!committedValueNamesRef.current.has(name)) changed = true
        if (!committedValueNamesRef.current.has(name)) newlyCommittedNames.push(name)
        committedValueNamesRef.current.add(name)
      }
      if (changed) {
        setCommittedValueNamesVersion(current => current + 1)
        // Only disclosure-safe logical names leave this write-only card. The
        // surrounding import review uses this signal to explain that a confirmed
        // project-secret mutation remains durable if the later script save fails.
        onProjectSecretsConfigured?.(newlyCommittedNames)
      }
    }
    setApplying(true)
    setError('')
    onErrorChange?.('')
    try {
      let currentStatuses = new Map(scopedStatuses)
      let currentStoreWritable = scopedStoreWritable
      const requestsGovernedMutation = (
        prepared.secrets.length > 0
        || (effectiveSourceMigrationSelected && prepared.migrationSelections.length > 0)
      )
      if (projectId && uniqueValidNames.length > 0 && typeof api.getProjectSecretStatuses === 'function') {
        setProbeState('loading')
        setProbeError('')
        try {
          const response = uniqueValidNames.length <= PROJECT_SECRET_STATUS_BATCH_SIZE
            ? projectSecretStatusSnapshot([
              await api.getProjectSecretStatuses(projectId, uniqueValidNames),
            ], projectId)
            : await getProjectSecretStatusesBatched(
              api.getProjectSecretStatuses,
              projectId,
              uniqueValidNames,
            )
          if (!requestIsCurrent()) return false
          currentStatuses = response.statuses
          currentStoreWritable = response.storeWritable
          setStatuses(currentStatuses)
          setStoreProvider(response.storeProvider)
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
      // Project-secret writes and protected source migrations are create-only.
      // A configured logical name might contain an unrelated value, and the
      // status API intentionally cannot compare that value with the imported
      // one. Resolve every such collision to a fresh name rather than silently
      // binding the imported script to an existing credential.
      const incomingValueNames = new Set(prepared.secrets.map(secret => secret.name))
      if (effectiveSourceMigrationSelected) {
        for (const selection of prepared.migrationSelections) {
          incomingValueNames.add(selection.secret_name)
        }
      }
      const reservedNames = new Set(incomingValueNames)
      const claimedNames = new Set<string>()
      const resolvedNames = new Map<string, string>()
      const sortedIncomingNames = Array.from(incomingValueNames).sort()
      let unresolvedCollisions: string[] = []
      for (const originalName of sortedIncomingNames) {
        if (
          !committedValueNamesRef.current.has(originalName)
          && currentStatuses.get(originalName)?.configured === true
        ) {
          unresolvedCollisions.push(originalName)
          continue
        }
        resolvedNames.set(originalName, originalName)
        claimedNames.add(originalName)
      }
      const reservedCandidates = new Set(reservedNames)
      const nextOrdinal = new Map(unresolvedCollisions.map(name => [name, 2]))
      while (unresolvedCollisions.length > 0) {
        const candidatesByOriginal = new Map<string, string[]>()
        const waveCandidates: string[] = []
        for (const originalName of unresolvedCollisions) {
          const candidates: string[] = []
          let ordinal = nextOrdinal.get(originalName) ?? 2
          while (candidates.length < COLLISION_PROBE_BATCH_SIZE && ordinal <= MAX_COLLISION_CANDIDATES + 1) {
            const candidate = collisionSafeSecretName(originalName, ordinal)
            ordinal += 1
            if (reservedCandidates.has(candidate) || claimedNames.has(candidate)) continue
            reservedCandidates.add(candidate)
            candidates.push(candidate)
            waveCandidates.push(candidate)
          }
          nextOrdinal.set(originalName, ordinal)
          candidatesByOriginal.set(originalName, candidates)
        }
        if (waveCandidates.length === 0) break
        const unknownNames = waveCandidates.filter(candidate => !currentStatuses.has(candidate))
        if (unknownNames.length > 0) {
          if (!projectId || typeof api.getProjectSecretStatuses !== 'function') {
            throw new Error('The project secret store cannot resolve a collision-safe credential name.')
          }
          const snapshot = unknownNames.length <= PROJECT_SECRET_STATUS_BATCH_SIZE
            ? projectSecretStatusSnapshot([
              await api.getProjectSecretStatuses(projectId, unknownNames),
            ], projectId)
            : await getProjectSecretStatusesBatched(
              api.getProjectSecretStatuses,
              projectId,
              unknownNames,
            )
          if (!requestIsCurrent()) return false
          if (!snapshot.storeWritable) {
            throw new Error('The project secret store cannot resolve a collision-safe credential name.')
          }
          const completedStatuses = unknownNames.map(name => [
            name,
            snapshot.statuses.get(name) || { name, configured: false },
          ] as const)
          currentStatuses = new Map([...currentStatuses, ...snapshot.statuses, ...completedStatuses])
        }
        const stillUnresolved: string[] = []
        for (const originalName of unresolvedCollisions) {
          const availableName = candidatesByOriginal.get(originalName)?.find(candidate => (
            currentStatuses.get(candidate)?.configured !== true && !claimedNames.has(candidate)
          ))
          if (!availableName) {
            stillUnresolved.push(originalName)
            continue
          }
          resolvedNames.set(originalName, availableName)
          collisionResolvedNamesRef.current.set(originalName, availableName)
          claimedNames.add(availableName)
        }
        unresolvedCollisions = stillUnresolved
      }
      if (unresolvedCollisions.length > 0) {
        throw new Error(`Unable to allocate a collision-safe project secret name for ${unresolvedCollisions[0]}.`)
      }
      if (Array.from(resolvedNames).some(([originalName, resolvedName]) => originalName !== resolvedName)) {
        setNames(current => Object.fromEntries(Object.entries(current).map(([key, name]) => [
          key,
          resolvedNames.get(name.trim()) || name,
        ])))
      }
      const resolveName = (name: string) => resolvedNames.get(name) || name
      const resolvedBindings = prepared.bindings.map(binding => {
        const resolvedBinding: SecretBindingSelection = {
          requirement_id: binding.requirement_id,
          secret_name: resolveName(binding.secret_name),
        }
        if (typeof binding.script_index === 'number') {
          Object.defineProperty(resolvedBinding, 'script_index', {
            configurable: false,
            enumerable: false,
            value: binding.script_index,
          })
        }
        return resolvedBinding
      })
      const resolvedSecrets = prepared.secrets.map(secret => ({
        ...secret,
        name: resolveName(secret.name),
      }))
      const resolvedMigrationSelections = prepared.migrationSelections.map(selection => ({
        ...selection,
        secret_name: resolveName(selection.secret_name),
      }))
      const secretsToCreate = resolvedSecrets.filter(secret => !committedValueNamesRef.current.has(secret.name))
      if (secretsToCreate.length > 0) {
        if (!projectId) throw new Error('Select a project before storing secret values.')
        if (!currentStoreWritable) throw new Error('The configured project secret provider is read-only.')
        const writesAreCurrent = await createProjectSecretsInBatches(
          api.createProjectSecrets,
          projectId,
          secretsToCreate,
          recordCommittedValueNames,
          requestIsCurrent,
        )
        if (!writesAreCurrent) return false
      }
      if (prepared.migrateSourceValues) {
        if (!projectId || !sourceSecretSessionId) {
          throw new Error('The protected source-secret session is unavailable.')
        }
        const namesWithManualValues = new Set(resolvedSecrets.map(secret => secret.name))
        const migrationSelections = resolvedMigrationSelections.filter(selection => {
          return (
            !namesWithManualValues.has(selection.secret_name)
            && !committedValueNamesRef.current.has(selection.secret_name)
          )
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
            const failureMetadata = getSourceSecretSessionFailureMetadata(cause)
            recordCommittedValueNames(failureMetadata.committedNames)
            if (!requestIsCurrent()) return false
            const failureMessage = formatSourceSecretSessionFailure(cause)
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
          recordCommittedValueNames(migrationSelections.map(selection => selection.secret_name))
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
            onSourceSessionSettled?.(sourceSecretSessionId, resolvedMigrationSelections)
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
      // A preservation-marked import with no explicit operator selection must
      // keep the reviewed runtime value byte-for-byte. Calling a generic
      // binding endpoint with an empty selection can still rebuild/mask that
      // draft, so zero selections are a deliberate local no-op.
      if (resolvedBindings.length > 0) {
        await onApply(resolvedBindings)
      }
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
      if (mountedRef.current && editSnapshot.current === editingSession) {
        setEditing(false)
        editSnapshot.current = null
      }
      return true
    } catch (err: unknown) {
      if (mountedRef.current && requestIsCurrent()) {
        const message = getErrorMessage(err, 'Failed to configure credentials')
        setError(message)
        onErrorChange?.(message)
      }
      return false
    } finally {
      if (requestIsCurrent()) setApplying(false)
    }
  }

  useImperativeHandle(ref, () => ({
    commit,
    discardSourceSession,
    refreshStatuses: () => {
      if (applyingRef.current) return
      backgroundStatusRefreshRef.current = true
      setProbeRetryGeneration(current => current + 1)
    },
  }))

  if (requirements.length === 0) return null

  const updateName = (key: string, name: string) => {
    setNames(current => ({ ...current, [key]: name.toUpperCase().replace(/[\s.-]+/g, '_') }))
    setError('')
  }

  // One credential shared by a whole collection is split into one requirement
  // per script on purpose, so a batch can never be attached to unrelated drafts
  // by accident. That leaves the operator naming the same password once per
  // request. Reusing one binding across them is an explicit choice, and
  // SecretBindingSelection exists to express it — so offer it as one click
  // instead of making them retype it N times.
  //
  // Grouping is by the credential noun the importer derived, not by the whole
  // suggested name: importers prefix the name with the request (LOGIN_PASSWORD,
  // ORDERS_PASSWORD), so whole-name grouping would never match a real import.
  // Nothing is merged automatically — the operator sees the count and the
  // affected requests before choosing.
  const reuseSets = new Map<string, RequirementGroup[]>()
  for (const group of groups) {
    const noun = credentialNoun(group.baseName)
    if (!noun || group.destinationClass === 'preserved') continue
    const key = `${noun}:${group.destinationClass}`
    const existing = reuseSets.get(key)
    if (existing) existing.push(group)
    else reuseSets.set(key, [group])
  }

  const reuseSetFor = (group: RequirementGroup): RequirementGroup[] => {
    const noun = credentialNoun(group.baseName)
    if (!noun) return []
    const members = reuseSets.get(`${noun}:${group.destinationClass}`) || []
    return members.length > 1 ? members : []
  }

  const applyNameToReuseSet = (group: RequirementGroup) => {
    const members = reuseSetFor(group)
    if (members.length === 0 || applying || disabled) return
    if (pendingReuse !== group.key) { setPendingReuse(group.key); return }
    setPendingReuse(null)
    const name = (names[group.key] ?? group.suggestedName).trim()
    if (!name) return
    const value = values[group.key] || ''
    setNames(current => {
      const next = { ...current }
      for (const member of members) next[member.key] = name
      return next
    })
    // Names reused across locations must carry the same optional value, so copy
    // the value too rather than leaving the set in a conflicting state.
    setValues(current => {
      const next = { ...current }
      for (const member of members) next[member.key] = value
      valuesRef.current = next
      return next
    })
    setError('')
  }

  const updateDestinationChoice = (group: RequirementGroup, choice: DestinationChoice) => {
    if (choice === 'preserve' && !groupCanPreserveDestinationValue(group)) return
    setDestinationChoices(current => ({ ...current, [group.key]: choice }))
    if (choice === 'preserve') {
      setValues(current => {
        if (!current[group.key]) return current
        const next = { ...current, [group.key]: '' }
        valuesRef.current = next
        return next
      })
      const anotherBoundSourceGroup = groups.some(candidate => (
        candidate.key !== group.key
        && effectiveDestinationChoice(candidate, destinationChoices) === 'project-secret'
        && candidate.requirements.some(requirement => requirement.source_value_available)
      ))
      if (!anotherBoundSourceGroup && effectiveSourceMigrationSelected) {
        if (sourceMigrationControlled) {
          onExternalSourceMigrationChange(false)
        } else {
          setMigrateSourceValues(false)
        }
      }
    }
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
  const hasBoundSourceValues = groups.some(group => (
    effectiveDestinationChoice(group, destinationChoices) === 'project-secret'
    && group.requirements.some(requirement => requirement.source_value_available)
  ))
  const sourceMigrationOffered = sourceMigrationAllowed && (sourceMigrationControlled || Boolean(sourceSecretSessionId))
  const sourceMigrationSelectable = sourceMigrationOffered && !sourceSessionUnavailable && hasBoundSourceValues
  const preservedGroupCount = groups.filter(group => (
    effectiveDestinationChoice(group, destinationChoices) === 'preserve'
  )).length
  const providerLabel = scopedStoreProvider.split(',').map(provider => ({ encrypted_database: 'Encrypted project store', environment_read_only: 'Environment bindings (read-only)' }[provider] || 'Project secret provider')).join(' · ') || 'Project secret store'
  // Each group needs setup, is configured (or has a value entered), or keeps
  // its imported value; the list leads with the groups that need setup. The
  // order reads the group's proposed name and what the store says, never a
  // name or value being typed, so a row does not move while it is filled in.
  const groupState = (group: RequirementGroup, typed: boolean): 'setup' | 'ready' | 'preserved' => {
    if (effectiveDestinationChoice(group, destinationChoices) === 'preserve') return 'preserved'
    const name = (typed ? names[group.key] || '' : group.suggestedName).trim()
    return scopedStatuses.get(name)?.configured === true || committedValueNamesSnapshot.names.has(name) || (typed && !!values[group.key])
      ? 'ready'
      : 'setup'
  }
  const groupStateOrder = { setup: 0, ready: 1, preserved: 2 } as const
  const filteredGroups = groups.filter(group => {
    const state = groupState(group, true)
    if (credentialState !== 'all' && credentialState !== state) return false
    if (attentionOnly && state === 'preserved') return false
    const search = credentialSearch.trim().toLowerCase()
    return !search || group.requirements.some(requirement =>
      [requirement.label, requirement.context, requirement.path, requirement.script_name, names[group.key], ...(requirement.affected_paths || [])].some(value => value?.toLowerCase().includes(search)))
  }).sort((left, right) => groupStateOrder[groupState(left, false)] - groupStateOrder[groupState(right, false)])
  const credentialPageCount = Math.max(1, Math.ceil(filteredGroups.length / CREDENTIAL_GROUP_PAGE_SIZE))
  const safeCredentialPage = Math.min(credentialPage, credentialPageCount - 1)
  const credentialPageStart = safeCredentialPage * CREDENTIAL_GROUP_PAGE_SIZE
  const visibleGroups = filteredGroups.slice(
    credentialPageStart,
    credentialPageStart + CREDENTIAL_GROUP_PAGE_SIZE,
  )

  const beginEditing = () => {
    editSnapshot.current = { names: { ...names }, choices: { ...destinationChoices }, migration: effectiveSourceMigrationSelected }
    setEditing(true)
    setError('')
  }
  const cancelEditing = () => {
    const snapshot = editSnapshot.current
    if (snapshot) {
      setNames(snapshot.names)
      setDestinationChoices(snapshot.choices)
      setMigrateSourceValues(snapshot.migration)
      if (sourceMigrationControlled) onExternalSourceMigrationChange?.(snapshot.migration)
    }
    valuesRef.current = {}
    setValues({})
    setPendingReuse(null)
    setError('')
    setEditing(false)
    editSnapshot.current = null
  }
  const ControlContainer = inlineEditing ? 'div' : 'details'

  return (
    <section className="secret-binding-review credentials-card" aria-labelledby={titleId}>
      <div className="secret-binding-review-header">
        <div>
          <h4 id={titleId}>Credential groups ({filteredGroups.length === groups.length ? groups.length : `${filteredGroups.length} of ${groups.length}`})</h4>
          <div hidden={inlineEditing}><p>{groups.length} unique credential groups across {requirements.length} credential requirements. Expand affected locations within each group to inspect their scope. Preserved imported values are grouped only when verified equal by the server. Unresolved bindings are grouped by their proposed reference name.</p>
          <p>
            Choose how each imported credential is stored in the destination. A server-confirmed imported value can remain
            runnable as-is, or you can replace it with a <code>{'{{secret.NAME}}'}</code> project-secret reference and
            configure it now or configure it later in Project Secrets.
          </p></div>
        </div>
        {inlineEditing && <span className="import-category-actions">
          {editing ? <>
            <button type="button" className="btn btn-primary btn-sm" disabled={!prepared.valid || applying || disabled} onClick={() => { void commit() }}>{applying ? 'Applying…' : 'Apply credentials'}</button>
            <button type="button" className="btn btn-secondary btn-sm" disabled={applying} onClick={cancelEditing}>Cancel credentials</button>
          </> : <button type="button" className="btn btn-secondary btn-sm" disabled={disabled || applying} onClick={beginEditing}>Edit credentials</button>}
        </span>}
        {!inlineEditing && !onPreparedChange && (
          <button className="btn btn-primary" type="button" onClick={() => { void commit() }} disabled={!prepared.valid || applying || disabled}>
            {applying ? 'Applying…' : 'Continue with credentials'}
          </button>
        )}
      </div>

      {inlineEditing && <ImportCategoryHeading label="Credential handling" details={<p>Preserve a server-confirmed imported value or use a project-secret reference. Entered values are write-only; existing secret values are never revealed or overwritten here. Apply updates this draft in place. You can also leave a reference to configure later in Project Secrets.</p>} />}
      {inlineEditing && editing && <p role="status">Apply or cancel credential edits before saving or changing this import. New secret values stay private in this editor.</p>}
      <div className="credentials-card-summary" role="status">
        <span>{prepared.configureLaterCount} {prepared.configureLaterCount === 1 ? 'binding needs' : 'bindings need'} setup</span>
        <span>{preservedGroupCount} imported value{preservedGroupCount === 1 ? '' : 's'} preserved</span>
        <span>{prepared.secrets.length} value{prepared.secrets.length === 1 ? '' : 's'} entered, not yet saved</span>
        <span>
          {scopedProbeState === 'loading'
            ? 'Checking project secret store…'
            : scopedProbeState === 'unavailable'
              ? 'Credential status unavailable'
              : `${providerLabel}${scopedStoreWritable ? '' : ' · read-only'}`}
        </span>
      </div>

      {hasSourceValues && sourceMigrationOffered && (!inlineEditing || editing) && (
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
            <small>
              Applies only to credentials set to Use project secret. Admin-only, create-only migration; existing project secrets are never overwritten.
              {!hasBoundSourceValues && ' Choose Use project secret below to enable migration.'}
            </small>
          </span>
        </label>
      )}

      {scopedProbeState === 'unavailable' && (
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

      <div className="import-review-toolbar">
        <label>Search credential locations<input type="search" disabled={applying || disabled} value={credentialSearch} onChange={event => { setCredentialSearch(event.target.value); setCredentialPage(0) }} /></label>
        <label>Credential state<select disabled={applying || disabled} value={credentialState} onChange={event => { setCredentialState(event.target.value); setCredentialPage(0) }}><option value="all">All groups</option><option value="setup">Needs setup</option><option value="preserved">Preserved</option><option value="ready">Configured or value entered</option></select></label>
        <label><input type="checkbox" disabled={applying || disabled} checked={attentionOnly} onChange={event => { setAttentionOnly(event.target.checked); setCredentialPage(0) }} /> Show bindings needing review</label>
      </div>
      {filteredGroups.length === 0 && <p role="status">No credential groups match this filter.</p>}
      <ol className="secret-binding-review-list" start={credentialPageStart + 1}>
        {visibleGroups.map((group) => {
          const index = groups.indexOf(group)
          const inputId = `${idPrefix}-secret-binding-${index}`
          const nameErrorId = `${inputId}-name-error`
          const valueConflictErrorId = `${inputId}-value-conflict-error`
          const preservationHelpId = `${inputId}-preservation-help`
          const preserveId = `${inputId}-preserve`
          const projectSecretId = `${inputId}-project-secret`
          const name = names[group.key] ?? ''
          const canPreserveDestinationValue = groupCanPreserveDestinationValue(group)
          const bindingSelected = effectiveDestinationChoice(group, destinationChoices) === 'project-secret'
          const nameError = bindingSelected ? validationErrors[group.key] : ''
          const configured = scopedStatuses.get(name.trim())?.configured === true
            || committedValueNamesSnapshot.names.has(name.trim())
          const sourceAvailable = group.requirements.some(requirement => requirement.source_value_available)
          const groupPresentation = groupPresentationByKey.get(group.key)!
          const { sourceVariables, affectedPaths, hasAffectedSourcePaths } = groupPresentation
          const scriptIdentities = Array.from(new Set(group.requirements.map(requirement => (
            requirement.script_name?.trim()
              || (typeof requirement.script_index === 'number' ? `Generated script ${requirement.script_index + 1}` : '')
          )).filter(Boolean)))
          const unresolvedSourceReference = group.requirements.some(requirement => requirement.unresolved_source_variable)
            && !sourceAvailable && !canPreserveDestinationValue
          const destinationValueState = destinationValueStateByName.get(name.trim())
          const finalNameHasManualValue = destinationValueState?.hasManualValue === true
          const finalNameHasSourceValue = destinationValueState?.hasSourceValue === true
          const sourceAvailableForMigration = sourceAvailable && sourceMigrationSelectable
          const willMigrate = effectiveSourceMigrationSelected && finalNameHasSourceValue
          const willCreate = !committedValueNamesSnapshot.names.has(name.trim()) && (
            (finalNameHasManualValue && scopedProbeState === 'ready' && scopedStoreWritable) || willMigrate
          )
          const configuredNameCollision = configured && willCreate
          const unresolvedSourceNextStep = destinationGuidance(configuredNameCollision, configured, willCreate)
          const preserveUnavailableMessages = canPreserveDestinationValue
            ? []
            : groupPreservationUnavailableMessages(group, unresolvedSourceNextStep)
          const status = !bindingSelected
            ? 'Preserved in script'
            : configuredNameCollision
            ? 'Will use a new name'
            : configured
            ? 'Configured'
            : willCreate
              ? 'Will be created'
              : sourceAvailableForMigration
                ? 'Available from source'
              : unresolvedSourceReference
                ? 'Value needed before run'
                : 'Configure later'
          const statusTone = !bindingSelected
            ? 'healthy'
            : configuredNameCollision
            ? 'warning'
            : configured || willCreate
              ? 'healthy'
              : sourceAvailableForMigration
                ? 'warning'
                : ''
          const valueConflict = conflictingValueNames.has(name.trim())
          const reuseSet = reuseSetFor(group)
          const reuseSetSize = reuseSet.length
          const reuseNoun = credentialNoun(group.baseName)
          const reuseSetLabels = reuseSet
            .slice(0, 8)
            .map(member => `${member.requirements[0]?.label || member.suggestedName} (${member.requirements.map(requirement => requirement.path).join(', ')})`)

          return (
            <li key={group.key} className="secret-binding-review-item">
              <div className="secret-binding-review-location">
                <div className="secret-binding-review-label-row">
                  <strong>{group.requirements[0]?.label || 'Credential'}</strong>
                  <span className={`script-health-token ${statusTone}`}>{status}</span>
                  {affectedPaths.length > 1 && (
                    <span className="secret-binding-review-reuse-badge">{affectedPaths.length} locations</span>
                  )}
                </div>
                {scriptIdentities.length > 0 && (
                  <div className="secret-binding-review-script">
                    <span>Generated script</span>
                    <strong>{scriptIdentities.join(', ')}</strong>
                  </div>
                )}
                {group.requirements[0]?.context && <p>{group.requirements[0].context}</p>}
                {sourceVariables.length > 0 && (
                  <div className="secret-binding-review-source">
                    <span>Source variable</span>
                    {sourceVariables.map(sourceVariable => <code key={sourceVariable}>{`{{${sourceVariable}}}`}</code>)}
                    {unresolvedSourceReference && (
                      <small>
                        APEX mapped {sourceVariables.length === 1 ? 'this authored source variable' : 'these authored source variables'}
                        {' '}to a governed project-secret reference.
                      </small>
                    )}
                  </div>
                )}
                <AffectedPathsDisclosure
                  id={`${inputId}-affected-paths`}
                  paths={affectedPaths}
                  hasAffectedSourcePaths={hasAffectedSourcePaths}
                />
              </div>
              {inlineEditing && !editing && <div className="credential-inline-current">
                <span>Destination</span><strong>{bindingSelected ? 'Project secret' : 'Preserve imported value'}</strong>
                {bindingSelected && <code>{`{{secret.${name}}}`}</code>}
                <span>Current value</span><strong>{!bindingSelected ? 'Preserved · hidden' : configured ? 'Configured · hidden' : 'Not configured'}</strong>
              </div>}
              <ControlContainer className="secret-binding-review-control" {...(!inlineEditing ? { open: bindingSelected ? true : undefined } : { hidden: !editing })}>
                {!inlineEditing && <summary>{bindingSelected ? 'Configure binding' : 'Preserved — review or change binding'}</summary>}
                {inlineEditing ? <div className="secret-binding-review-choice">
                  <label htmlFor={`${inputId}-destination`}>Destination handling</label>
                  <select id={`${inputId}-destination`} aria-label={`Destination for ${group.requirements[0]?.label || name}`} value={bindingSelected ? 'project-secret' : 'preserve'} disabled={applying || disabled} onChange={event => updateDestinationChoice(group, event.target.value as DestinationChoice)}>
                    <option value="preserve" disabled={!canPreserveDestinationValue}>Preserve imported value</option>
                    <option value="project-secret">Use project secret</option>
                  </select>
                  {preserveUnavailableMessages.length > 0 && <details><summary>Why preservation is unavailable</summary>{preserveUnavailableMessages.map(message => <small key={message}>{message}</small>)}</details>}
                </div> : <fieldset className="secret-binding-review-choice">
                  <legend>Destination handling</legend>
                  <label htmlFor={preserveId}>
                    <input
                      id={preserveId}
                      type="radio"
                      name={`${idPrefix}-destination-${index}`}
                      checked={!bindingSelected}
                      disabled={applying || disabled || !canPreserveDestinationValue}
                      aria-describedby={preserveUnavailableMessages.length > 0 ? preservationHelpId : undefined}
                      onChange={() => updateDestinationChoice(group, 'preserve')}
                    />
                    Preserve imported value
                  </label>
                  <label htmlFor={projectSecretId}>
                    <input
                      id={projectSecretId}
                      type="radio"
                      name={`${idPrefix}-destination-${index}`}
                      checked={bindingSelected}
                      disabled={applying || disabled}
                      onChange={() => updateDestinationChoice(group, 'project-secret')}
                    />
                    Use project secret
                  </label>
                  {preserveUnavailableMessages.length > 0 && (
                    <div id={preservationHelpId} className="secret-binding-review-preservation-reasons">
                      {preserveUnavailableMessages.map(message => <small key={message}>{message}</small>)}
                    </div>
                  )}
                </fieldset>}
                <SecretReferenceValueControl
                  id={inputId}
                  name={name}
                  value={values[group.key] || ''}
                  onNameChange={value => updateName(group.key, value)}
                  onValueChange={value => updateValue(group.key, value)}
                  nameDisabled={applying || disabled || !bindingSelected}
                  valueDisabled={applying || disabled || !bindingSelected || !projectId || scopedProbeState !== 'ready' || !scopedStoreWritable || configured}
                  nameDescribedBy={[
                    nameError ? nameErrorId : '',
                    valueConflict ? valueConflictErrorId : '',
                  ].filter(Boolean).join(' ')}
                  nameInvalid={Boolean(nameError || valueConflict)}
                  valueLabel="Secret value — supply now or later"
                  referenceKind={bindingSelected ? 'Script reference' : 'Optional project reference'}
                  valueAriaLabel={`Secret value for ${name || group.requirements[0]?.label || 'credential'}`}
                  valuePlaceholder={configured
                    ? 'Already configured'
                    : !projectId
                      ? 'Select a project to enter a value'
                      : scopedProbeState === 'loading'
                        ? 'Checking project secret store'
                        : scopedProbeState === 'unavailable'
                          ? 'Retry the credential check to enter a value'
                          : scopedStoreWritable
                            ? 'Enter now or leave blank'
                            : 'Provider is read-only'}
                />
                {bindingSelected && !configured && !values[group.key] && <p>Leaving this blank keeps the binding unresolved until a usable value is configured in Project Secrets. Saving a draft does not prove replay readiness.</p>}
                {pendingReuse === group.key && <div role="alert">
                  <p>This will replace the selected reference name and optional value in {reuseSetSize} groups. Inspect the affected locations below. Only continue if these locations intentionally share one credential.</p>
                  <AffectedPathsDisclosure id={`${inputId}-reuse-scope`} paths={reuseSet.map(member => `${member.requirements[0]?.label || member.suggestedName}: ${member.requirements.map(requirement => requirement.path).join(', ')}`)} hasAffectedSourcePaths={false} />
                  <button type="button" onClick={() => setPendingReuse(null)}>Cancel reuse</button>
                </div>}
                {reuseSetSize > 1 && (
                  <button
                    type="button"
                    className="btn btn-secondary btn-sm secret-binding-review-reuse-all"
                    disabled={applying || disabled || !name.trim() || Boolean(nameError)}
                    onClick={() => applyNameToReuseSet(group)}
                    title={`Point these at one project secret: ${reuseSetLabels.join(', ')}. Do this only when they really are the same credential.`}
                  >
                    {pendingReuse === group.key ? 'Confirm reuse across' : 'Review reuse across'} {reuseSetSize} {reuseNoun.toLowerCase()} groups
                  </button>
                )}
                {nameError && <span id={nameErrorId} className="secret-binding-review-field-error">{nameError}</span>}
                {valueConflict && <span id={valueConflictErrorId} className="secret-binding-review-field-error">Reused names must use the same optional value.</span>}
              </ControlContainer>
            </li>
          )
        })}
      </ol>
      {credentialPageCount > 1 && (
        <nav className="secret-binding-review-pagination" aria-label="Credential review pages">
          <span aria-live="polite">
            Showing credential groups {credentialPageStart + 1}–{credentialPageStart + visibleGroups.length} of {filteredGroups.length}. Page {safeCredentialPage + 1} of {credentialPageCount}.
          </span>
          <div role="group" aria-label="Credential page controls">
            <button
              type="button"
              className="btn btn-secondary btn-sm"
              disabled={safeCredentialPage === 0}
              onClick={() => setCredentialPage(Math.max(0, safeCredentialPage - 1))}
            >
              Previous {CREDENTIAL_GROUP_PAGE_SIZE}
            </button>
            <button
              type="button"
              className="btn btn-secondary btn-sm"
              disabled={safeCredentialPage >= credentialPageCount - 1}
              onClick={() => setCredentialPage(Math.min(credentialPageCount - 1, safeCredentialPage + 1))}
            >
              Next {CREDENTIAL_GROUP_PAGE_SIZE}
            </button>
          </div>
        </nav>
      )}
    </section>
  )
})

export default SecretBindingReview
