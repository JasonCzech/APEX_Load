import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { type Folder, type StoredScript } from '../types'
import { type ScriptDefinition } from '../types/scripts'
import {
  type BrunoCollectionItem,
  type BrunoInspectResponse,
  type BrunoNativeDiagnostic,
  type BrunoNativeScriptCandidate,
  type BrunoProjectionPreview,
  type BrunoSourceRevision,
  type BrunoSupportMatrixResponse,
  type CommitBrunoCanonicalResponse,
  type CommitBrunoImportResponse,
  type CommitBrunoNativeBatchResponse,
  type SecretBindingRequirement,
  type SecretBindingSelection,
} from '../types/scriptImports'
import { useApi } from '../hooks/useApi'
import { getErrorMessage } from '../utils/errorUtils'
import DesktopApiResourceLink from './DesktopApiResourceLink'
import SecretBindingReview, {
  type PreparedSecretBindings,
  type SecretBindingReviewHandle,
} from './SecretBindingReview'
import ParameterizationReview from './ParameterizationReview'
import { formatBytes } from './settingsPageFormatters'
import { type ImportedSaveResult } from './useScriptsAuthoringState'
import {
  formatSourceSecretSessionFailure,
  getSourceSecretSessionFailureMetadata,
} from './sourceSecretSession'
import {
  filesContainNativeBrunoSource,
  isOpenCollectionFileName,
} from '../nativeImportRouting'
import { brunoDestinationFolderOptions } from './brunoFolderOptions'
import { mergeProtectedParameterizationSelectionIds } from './protectedImportSession'
import './BrunoImport.css'

interface Props {
  initialFiles?: File[] | null
  initialOutputMode?: OutputMode | null
  projectId?: string
  onImported?: (result: ImportedSaveResult) => void
  onConfigureSecrets?: (projectId: string) => void
  onClose?: () => void
}

type RunOptions = {
  selection: string
  paths: string
  includeTags: string
  excludeTags: string
  recursive: boolean
  execution: string
  delayMs: number
  streamTimeoutMs: number
  bail: boolean
  testsOnly: boolean
  persistSession: boolean
  defaultEnvironment: string
  defaultGlobalEnvironment: string
}

type OutputMode = 'canonical' | 'native_requests'

const DEFAULT_RUN_OPTIONS: RunOptions = {
  selection: 'collection',
  paths: '',
  includeTags: '',
  excludeTags: '',
  recursive: true,
  execution: 'sequential',
  delayMs: 0,
  streamTimeoutMs: 60000,
  bail: false,
  testsOnly: false,
  persistSession: false,
  defaultEnvironment: '',
  defaultGlobalEnvironment: '',
}

function splitList(value: string) {
  return value.split(',').map(item => item.trim()).filter(Boolean)
}

function isOpenCollectionYAML(files: File[]) {
  return files.some(isOpenCollectionFileName)
}

function needsBrunoContentModeDetection(files: File[]) {
  if (files.length === 0 || isOpenCollectionYAML(files)) return false
  return files.some(file => /\.(?:ya?ml|json)$/i.test(file.name))
}

function storedScriptFromDefinition(script: ScriptDefinition): StoredScript {
  return {
    id: script.id || '',
    ref_key: script.ref_key,
    name: script.name,
    protocol: script.protocol,
    version: script.version,
    project_id: script.project_id,
    folder_id: script.folder_id,
    created_at: script.created_at || new Date().toISOString(),
    script,
  }
}

function nativeCandidateIdentityError(candidates: BrunoNativeScriptCandidate[]) {
  const candidateIds = new Set<string>()
  const plannedScriptIds = new Set<string>()
  for (const candidate of candidates) {
    const candidateId = candidate.candidate_id?.trim()
    if (!candidateId) return `Native Bruno inspection candidate ${candidate.source_path || '(unknown source)'} is missing candidate_id.`
    if (candidateIds.has(candidateId)) return `Native Bruno inspection contains duplicate candidate_id ${candidateId}.`
    candidateIds.add(candidateId)

    const plannedScriptId = candidate.planned_script_id?.trim()
    if (!plannedScriptId) return `Native Bruno inspection candidate ${candidate.source_path || candidateId} is missing planned_script_id.`
    if (plannedScriptIds.has(plannedScriptId)) return `Native Bruno inspection contains duplicate planned_script_id ${plannedScriptId}.`
    plannedScriptIds.add(plannedScriptId)
  }
  return ''
}

function validatedNativeCommitScripts(
  response: CommitBrunoImportResponse,
  selectedCandidates: BrunoNativeScriptCandidate[],
) {
  const identityError = nativeCandidateIdentityError(selectedCandidates)
  if (identityError) throw new Error(identityError)
  if (!isNativeCommitResponse(response) || !response.batch_id.trim()) {
    throw new Error('Native Bruno commit response is missing batch_id.')
  }
  const receipt = response as unknown as Record<string, unknown>
  if (!Object.prototype.hasOwnProperty.call(receipt, 'created') ||
    !Object.prototype.hasOwnProperty.call(receipt, 'idempotent_replay') ||
    typeof receipt.created !== 'boolean' ||
    typeof receipt.idempotent_replay !== 'boolean') {
    throw new Error('Native Bruno commit response must include boolean created and idempotent_replay fields.')
  }
  if (receipt.created === receipt.idempotent_replay) {
    throw new Error('Native Bruno commit response must identify exactly one of creation or idempotent replay.')
  }
  if (response._http_status === 201 && !receipt.created) {
    throw new Error('Native Bruno HTTP 201 response must identify a newly created batch.')
  }
  if (response._http_status === 200 && !receipt.idempotent_replay) {
    throw new Error('Native Bruno HTTP 200 response must identify an idempotent replay.')
  }
  if (response._http_status !== 200 && response._http_status !== 201) {
    throw new Error('Native Bruno commit response has an unsupported HTTP status.')
  }
  if (response.scripts.length !== selectedCandidates.length) {
    throw new Error(`Native Bruno commit returned ${response.scripts.length} of ${selectedCandidates.length} selected scripts.`)
  }
  if (response.count !== response.scripts.length) {
    throw new Error('Native Bruno commit response count does not match its scripts.')
  }
  const expectedIds = new Set(selectedCandidates.map(candidate => (candidate.planned_script_id || '').trim()))
  const receivedIds = new Set<string>()
  for (const script of response.scripts) {
    const id = script.id?.trim()
    if (!id || receivedIds.has(id) || !expectedIds.has(id)) {
      throw new Error('Native Bruno commit response contains an unknown, duplicate, or missing script identity.')
    }
    receivedIds.add(id)
  }
  return response.scripts.map(storedScriptFromDefinition)
}

function isNativeCommitResponse(response: CommitBrunoImportResponse): response is CommitBrunoNativeBatchResponse {
  return 'batch_id' in response
}

function isCanonicalCommitResponse(response: CommitBrunoImportResponse): response is CommitBrunoCanonicalResponse {
  return 'script' in response
}

function nativeFindingID(diagnostic: BrunoNativeDiagnostic) {
  return diagnostic.id || diagnostic.finding_id || ''
}

function nativeCandidateReviewFindings(candidate: BrunoNativeScriptCandidate) {
  const ids = new Set<string>()
  ;(candidate.diagnostics || []).forEach(diagnostic => {
    if (!diagnostic.acknowledgement_required) return
    const id = nativeFindingID(diagnostic)
    if (id) ids.add(id)
  })
  return Array.from(ids).sort()
}

function nativeCandidateMethod(candidate: BrunoNativeScriptCandidate) {
  return candidate.method || candidate.script.actions[0]?.method || candidate.script.actions[0]?.type || 'HTTP'
}

function nativeCandidateTarget(candidate: BrunoNativeScriptCandidate) {
  return candidate.target || candidate.script.actions[0]?.target || candidate.script.config?.base_url || ''
}

function CollectionTree({ items }: { items: BrunoCollectionItem[] }) {
  if (items.length === 0) return <p className="bruno-empty">No collection items were detected.</p>
  return (
    <ul className="bruno-tree">
      {items.map(item => (
        <li key={`${item.uid}-${item.path}`}>
          <div className="bruno-tree-row">
            <span className={`bruno-type bruno-type-${item.type}`}>{item.type}</span>
            <span>{item.name}</span>
            {!item.executable && <small>preserved only</small>}
            {item.has_scripts && <small>scripts</small>}
            {item.has_tests && <small>tests</small>}
          </div>
          {item.children && item.children.length > 0 && <CollectionTree items={item.children} />}
        </li>
      ))}
    </ul>
  )
}

export default function BrunoImport({ initialFiles, initialOutputMode, projectId, onImported, onConfigureSecrets, onClose }: Props) {
  const api = useApi()
  const fileInputRef = useRef<HTMLInputElement>(null)
  const directoryInputRef = useRef<HTMLInputElement>(null)
  const [files, setFiles] = useState<File[]>(initialFiles || [])
  const [outputMode, setOutputMode] = useState<OutputMode>(() => initialOutputMode || (isOpenCollectionYAML(initialFiles || []) ? 'native_requests' : 'canonical'))
  const initiallyDetectingMode = !initialOutputMode && needsBrunoContentModeDetection(initialFiles || [])
  const [modeDetectionPending, setModeDetectionPending] = useState(initiallyDetectingMode)
  const modeDetectionPendingRef = useRef(initiallyDetectingMode)
  const modeDetectionRequestRef = useRef(0)
  const updateModeDetectionPending = useCallback((pending: boolean) => {
    modeDetectionPendingRef.current = pending
    setModeDetectionPending(pending)
  }, [])
  const [name, setName] = useState('')
  const [sandbox, setSandbox] = useState<'safe' | 'developer'>('safe')
  const [syncScriptId, setSyncScriptId] = useState('')
  const [runOptions, setRunOptions] = useState<RunOptions>(DEFAULT_RUN_OPTIONS)
  const [inspection, setInspection] = useState<BrunoInspectResponse | null>(null)
  const inspectionRef = useRef<BrunoInspectResponse | null>(inspection)
  inspectionRef.current = inspection
  const [support, setSupport] = useState<BrunoSupportMatrixResponse | null>(null)
  const [scripts, setScripts] = useState<StoredScript[]>([])
  const [folders, setFolders] = useState<Folder[]>([])
  const [folderLoadError, setFolderLoadError] = useState('')
  const [folderReloadToken, setFolderReloadToken] = useState(0)
  const [destinationFolderId, setDestinationFolderId] = useState('')
  const [selectedCandidateIds, setSelectedCandidateIds] = useState<string[]>([])
  const [nativeNameOverrides, setNativeNameOverrides] = useState<Record<string, string>>({})
  const [acknowledgedFindingIds, setAcknowledgedFindingIds] = useState<string[]>([])
  const [migrateSecrets, setMigrateSecrets] = useState(false)
  const [preparedSecrets, setPreparedSecrets] = useState<PreparedSecretBindings | null>(null)
  const secretBindingsRef = useRef<Record<string, string>>({})
  const credentialReviewRef = useRef<SecretBindingReviewHandle | null>(null)
  const [conflictResolutions, setConflictResolutions] = useState<Record<string, string>>({})
  const [committed, setCommitted] = useState<CommitBrunoImportResponse | null>(null)
  const [committedDeferredSecretCount, setCommittedDeferredSecretCount] = useState(0)
  const [parameterizationSelectedIds, setParameterizationSelectedIds] = useState<string[]>([])
  const [parameterizationApplying, setParameterizationApplying] = useState(false)
  const [revisions, setRevisions] = useState<BrunoSourceRevision[]>([])
  const [projection, setProjection] = useState<BrunoProjectionPreview | null>(null)
  const [loading, setLoading] = useState(false)
  const [rollbackLoading, setRollbackLoading] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const sourceGenerationRef = useRef(0)
  const mountedRef = useRef(true)
  const [sourceGeneration, setSourceGeneration] = useState(0)
  const advanceSourceGeneration = useCallback(() => {
    const next = sourceGenerationRef.current + 1
    sourceGenerationRef.current = next
    setSourceGeneration(next)
    return next
  }, [])
  const sourceProjectIdRef = useRef(projectId)
  sourceProjectIdRef.current = projectId
  const resetProjectIdRef = useRef(projectId)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      // Invalidate inspection and commit continuations before they can invoke
      // callbacks or update state after this import view has been left.
      sourceGenerationRef.current += 1
      modeDetectionRequestRef.current += 1
      modeDetectionPendingRef.current = false
    }
  }, [])

  useEffect(() => {
    directoryInputRef.current?.setAttribute('webkitdirectory', '')
    directoryInputRef.current?.setAttribute('directory', '')
  }, [])

  useEffect(() => {
    const nextFiles = initialFiles || []
    advanceSourceGeneration()
    const modeDetectionRequest = modeDetectionRequestRef.current + 1
    modeDetectionRequestRef.current = modeDetectionRequest
    const shouldDetectMode = !initialOutputMode && needsBrunoContentModeDetection(nextFiles)
    updateModeDetectionPending(shouldDetectMode)
    setFiles(nextFiles)
    setOutputMode(initialOutputMode || (isOpenCollectionYAML(nextFiles) ? 'native_requests' : 'canonical'))
    setInspection(null)
    setCommitted(null)
    setCommittedDeferredSecretCount(0)
    setPreparedSecrets(null)
    setMigrateSecrets(false)
    secretBindingsRef.current = {}
    setLoading(false)
    setError('')
    setNotice('')
    setSelectedCandidateIds([])
    setNativeNameOverrides({})
    setAcknowledgedFindingIds([])
    if (shouldDetectMode) {
      void filesContainNativeBrunoSource(nextFiles).then(isNative => {
        if (!mountedRef.current ||
          modeDetectionRequestRef.current !== modeDetectionRequest) return
        setOutputMode(isNative ? 'native_requests' : 'canonical')
        updateModeDetectionPending(false)
      })
    }
  }, [advanceSourceGeneration, initialFiles, initialOutputMode, updateModeDetectionPending])

  useEffect(() => {
    if (resetProjectIdRef.current === projectId) return
    resetProjectIdRef.current = projectId
    advanceSourceGeneration()
    setInspection(null)
    setCommitted(null)
    setCommittedDeferredSecretCount(0)
    setPreparedSecrets(null)
    setMigrateSecrets(false)
    secretBindingsRef.current = {}
    setLoading(false)
    setError('')
    setNotice('')
    setSelectedCandidateIds([])
    setNativeNameOverrides({})
    setAcknowledgedFindingIds([])
    setDestinationFolderId('')
    setSyncScriptId('')
  }, [advanceSourceGeneration, projectId])

  useEffect(() => {
    let cancelled = false
    void Promise.all([
      api.getBrunoSupportMatrix().catch(() => null),
      projectId ? api.listScripts(projectId).catch(() => ({ scripts: [] as StoredScript[], count: 0 })) : Promise.resolve({ scripts: [] as StoredScript[], count: 0 }),
      projectId
        ? api.listFolders(projectId, 'scripts')
          .then(response => ({ ...response, loadError: '' }))
          .catch(cause => ({
            folders: [] as Folder[],
            count: 0,
            loadError: getErrorMessage(cause, 'Unable to load destination folders.'),
          }))
        : Promise.resolve({ folders: [] as Folder[], count: 0, loadError: '' }),
    ]).then(([matrix, response, folderResponse]) => {
      if (cancelled) return
      setSupport(matrix)
      setScripts((response.scripts || []).filter(script => script.protocol === 'bruno'))
      setFolders(folderResponse.folders || [])
      setFolderLoadError(folderResponse.loadError)
    })
    return () => { cancelled = true }
  }, [api, folderReloadToken, projectId])

  const nativeCandidates = useMemo(
    () => inspection?.result.output_mode === 'native_requests' ? inspection.result.scripts : [],
    [inspection],
  )
  const destinationFolderOptions = useMemo(
    () => brunoDestinationFolderOptions(folders),
    [folders],
  )
  const requirements = useMemo(() => {
    const selectedRequirements = nativeCandidates
      .filter(candidate => selectedCandidateIds.includes(candidate.candidate_id))
      .flatMap(candidate => candidate.secret_requirements || [])
    const all = outputMode === 'native_requests' && nativeCandidates.length > 0
      ? selectedRequirements
      : inspection?.result.secret_requirements || []
    return Array.from(new Map(all.map(requirement => [requirement.id, requirement])).values())
  }, [inspection, nativeCandidates, outputMode, selectedCandidateIds])
  const credentialRequirements = useMemo<SecretBindingRequirement[]>(() => requirements.map(requirement => ({
    id: requirement.id,
    path: requirement.path,
    label: requirement.suggested_name,
    context: requirement.context,
    suggested_name: requirement.suggested_name,
    replacement: requirement.replacement,
    source_value_available: requirement.source_value_available,
  })), [requirements])
  const sourceMigrationAvailable = Boolean(
    support?.secret_store?.source_secret_migration_policy_enabled
    && support.secret_store.writable
    && requirements.some(requirement => requirement.source_value_available),
  )
  const conflicts = inspection?.sync_preview?.conflicts || []
  const unresolvedConflicts = conflicts.filter(conflict => conflictResolutions[conflict.id] !== conflict.required_resolution)
  const selectedBytes = useMemo(() => files.reduce((total, file) => total + file.size, 0), [files])
  const importDisabled = support?.import_enabled === false
  const canInspect = Boolean(projectId && files.length > 0 && !loading && !modeDetectionPending && !importDisabled)
  const selectionMissing = outputMode === 'canonical'
    && (runOptions.selection === 'folder' || runOptions.selection === 'request')
    && splitList(runOptions.paths).length === 0
  const selectedNativeCandidates = nativeCandidates.filter(candidate => selectedCandidateIds.includes(candidate.candidate_id))
  const allNativeCandidatesSelected = nativeCandidates.length > 0
    && selectedNativeCandidates.length === nativeCandidates.length
  const someNativeCandidatesSelected = selectedNativeCandidates.length > 0 && !allNativeCandidatesSelected
  const nativeIdentityError = outputMode === 'native_requests'
    ? nativeCandidateIdentityError(nativeCandidates)
    : ''
  const nativeReviewFindingIds = Array.from(new Set(selectedNativeCandidates.flatMap(nativeCandidateReviewFindings))).sort()
  const unacknowledgedFindingIds = nativeReviewFindingIds.filter(id => !acknowledgedFindingIds.includes(id))
  const selectedNativeNames = selectedNativeCandidates.map(candidate => (nativeNameOverrides[candidate.candidate_id] ?? candidate.script.name).trim())
  const duplicateNativeNames = new Set(selectedNativeNames.map(value => value.toLowerCase())).size !== selectedNativeNames.length
  const nativeNamesValid = selectedNativeNames.every(Boolean) && !duplicateNativeNames
  const credentialsValid = requirements.length === 0 || preparedSecrets?.valid === true
  const canCommit = Boolean(
    inspection &&
    !loading &&
    !parameterizationApplying &&
    credentialsValid &&
    !selectionMissing &&
    (outputMode === 'native_requests'
      ? destinationFolderId !== '' && selectedNativeCandidates.length > 0 && nativeNamesValid && unacknowledgedFindingIds.length === 0
        && nativeIdentityError === ''
      : unresolvedConflicts.length === 0 && (!syncScriptId || inspection.sync_preview)),
  )
  const deferredSecretCount = preparedSecrets?.configureLaterCount ?? 0
  const commitActionLabel = outputMode === 'native_requests'
    ? `Save ${selectedNativeCandidates.length} native script${selectedNativeCandidates.length === 1 ? '' : 's'}`
    : syncScriptId ? 'Synchronize Bruno source' : 'Import Bruno collection'
  const commitButtonLabel = loading
    ? 'Committing…'
    : deferredSecretCount > 0
      ? `${commitActionLabel} with ${deferredSecretCount} ${deferredSecretCount === 1 ? 'secret' : 'secrets'} to configure`
      : commitActionLabel
  const developerBlocked = outputMode === 'canonical' && sandbox === 'developer' && support && !support.developer_execution_enabled

  useEffect(() => {
    if (!sourceMigrationAvailable) setMigrateSecrets(false)
  }, [sourceMigrationAvailable])

  useEffect(() => {
    setParameterizationSelectedIds([])
    setParameterizationApplying(false)
  }, [inspection?.import_session_id])

  const selectFiles = (next: File[]) => {
    advanceSourceGeneration()
    const modeDetectionRequest = modeDetectionRequestRef.current + 1
    modeDetectionRequestRef.current = modeDetectionRequest
    const shouldDetectMode = needsBrunoContentModeDetection(next)
    updateModeDetectionPending(shouldDetectMode)
    setFiles(next)
    setOutputMode(isOpenCollectionYAML(next) ? 'native_requests' : 'canonical')
    setInspection(null)
    setCommitted(null)
    setCommittedDeferredSecretCount(0)
    setPreparedSecrets(null)
    setMigrateSecrets(false)
    secretBindingsRef.current = {}
    setConflictResolutions({})
    setError('')
    setNotice('')
    setSelectedCandidateIds([])
    setNativeNameOverrides({})
    setAcknowledgedFindingIds([])
    if (shouldDetectMode) {
      void filesContainNativeBrunoSource(next).then(isNative => {
        if (!mountedRef.current ||
          modeDetectionRequestRef.current !== modeDetectionRequest) return
        setOutputMode(isNative ? 'native_requests' : 'canonical')
        updateModeDetectionPending(false)
      })
    }
  }

  const selectOutputMode = (mode: OutputMode) => {
    if (mode === outputMode && !modeDetectionPendingRef.current) return
    modeDetectionRequestRef.current += 1
    updateModeDetectionPending(false)
    advanceSourceGeneration()
    setOutputMode(mode)
    setInspection(null)
    setCommitted(null)
    setPreparedSecrets(null)
    setMigrateSecrets(false)
    secretBindingsRef.current = {}
    setSelectedCandidateIds([])
    setNativeNameOverrides({})
    setAcknowledgedFindingIds([])
    setConflictResolutions({})
    setError('')
    setNotice('')
  }

  const confirmPendingOutputMode = (mode: OutputMode) => {
    if (mode === outputMode && modeDetectionPendingRef.current) {
      selectOutputMode(mode)
    }
  }

  const inspect = async () => {
    if (!projectId || files.length === 0 || modeDetectionPendingRef.current) return
    const sourceRequestGeneration = advanceSourceGeneration()
    const sourceProjectId = sourceProjectIdRef.current
    const requestIsCurrent = () => mountedRef.current
      && sourceGenerationRef.current === sourceRequestGeneration
      && sourceProjectIdRef.current === sourceProjectId
    setLoading(true)
    setError('')
    setNotice('')
    setCommitted(null)
    setPreparedSecrets(null)
    setMigrateSecrets(false)
    try {
      const response = await api.inspectBrunoImport(files, {
        project_id: projectId,
        output_mode: outputMode,
        name: outputMode === 'canonical' ? name.trim() || undefined : undefined,
        sandbox: outputMode === 'canonical' ? sandbox : undefined,
        sync_script_id: outputMode === 'canonical' ? syncScriptId || undefined : undefined,
        run_options: outputMode === 'canonical' ? {
          default_environment: runOptions.defaultEnvironment || undefined,
          default_global_environment: runOptions.defaultGlobalEnvironment || undefined,
          selection: runOptions.selection,
          paths: splitList(runOptions.paths),
          include_tags: splitList(runOptions.includeTags),
          exclude_tags: splitList(runOptions.excludeTags),
          recursive: runOptions.recursive,
          execution: runOptions.execution,
          delay_ms: runOptions.delayMs,
          stream_timeout_ms: runOptions.streamTimeoutMs,
          bail: runOptions.bail,
          tests_only: runOptions.testsOnly,
          persist_session: runOptions.persistSession,
        } : undefined,
      })
      if (!requestIsCurrent()) return
      setInspection(response)
      const nextBindings: Record<string, string> = {}
      const inspectedRequirements = [
        ...(response.result.secret_requirements || []),
        ...(response.result.output_mode === 'native_requests'
          ? response.result.scripts.flatMap(candidate => candidate.secret_requirements || [])
          : []),
      ]
      ;inspectedRequirements.forEach(requirement => {
        nextBindings[requirement.id] = secretBindingsRef.current[requirement.id] || requirement.suggested_name
      })
      secretBindingsRef.current = nextBindings
      setConflictResolutions({})
      const candidates = response.result.output_mode === 'native_requests' ? response.result.scripts : []
      setSelectedCandidateIds(candidates.map(candidate => candidate.candidate_id))
      setNativeNameOverrides(Object.fromEntries(candidates.map(candidate => [candidate.candidate_id, candidate.script.name])))
      setAcknowledgedFindingIds([])
      const identityError = outputMode === 'native_requests'
        ? nativeCandidateIdentityError(candidates)
        : ''
      if (identityError) {
        setError(identityError)
        setNotice('')
      } else {
        setNotice(outputMode === 'native_requests'
          ? `${candidates.length} API request${candidates.length === 1 ? '' : 's'} found → ${candidates.length} native script${candidates.length === 1 ? '' : 's'}.`
          : `Inspection preserved ${response.result.fidelity.preserved_files} source artifacts with ${response.result.fidelity.dropped_items} dropped items.`)
      }
    } catch (cause) {
      if (requestIsCurrent()) setError(getErrorMessage(cause, 'Unable to inspect the Bruno source.'))
    } finally {
      if (requestIsCurrent()) setLoading(false)
    }
  }

  const refreshRevisionState = async (scriptId: string, requestIsCurrent: () => boolean = () => true) => {
    const [revisionResponse, projectionResponse] = await Promise.all([
      api.listBrunoRevisions(scriptId).catch(() => ({ revisions: [], count: 0 })),
      api.previewBrunoProjection(scriptId).catch(() => null),
    ])
    if (!requestIsCurrent()) return
    setRevisions(revisionResponse.revisions || [])
    setProjection(projectionResponse)
  }

  const commit = async () => {
    if (!inspection || !projectId || !canCommit) return
    const sourceRequestGeneration = sourceGenerationRef.current
    const sourceProjectId = sourceProjectIdRef.current
    const sourceImportSessionId = inspection.import_session_id
    const requestIsCurrent = () => mountedRef.current
      && sourceGenerationRef.current === sourceRequestGeneration
      && sourceProjectIdRef.current === sourceProjectId
      && inspectionRef.current?.import_session_id === sourceImportSessionId
    setLoading(true)
    setError('')
    try {
      if (requirements.length > 0) {
        if (!credentialReviewRef.current) throw new Error('Credential preparation is unavailable.')
        const committedCredentials = await credentialReviewRef.current.commit()
        if (!committedCredentials) return
      }
      if (!requestIsCurrent()) return
      const response = outputMode === 'native_requests'
        ? await api.commitBrunoImport({
          project_id: projectId,
          import_session_id: inspection.import_session_id,
          mode: 'create_native_batch',
          folder_id: destinationFolderId,
          candidate_ids: selectedNativeCandidates.map(candidate => candidate.candidate_id),
          name_overrides: Object.fromEntries(selectedNativeCandidates.map(candidate => [
            candidate.candidate_id,
            (nativeNameOverrides[candidate.candidate_id] ?? candidate.script.name).trim(),
          ])),
          migrate_secrets: migrateSecrets,
          secret_bindings: Object.fromEntries(requirements
            .filter(requirement => secretBindingsRef.current[requirement.id])
            .map(requirement => [requirement.id, secretBindingsRef.current[requirement.id]])),
          acknowledged_finding_ids: nativeReviewFindingIds,
        })
        : await api.commitBrunoImport({
          project_id: projectId,
          import_session_id: inspection.import_session_id,
          mode: syncScriptId ? 'sync' : 'create',
          sync_script_id: syncScriptId || undefined,
          name: name.trim() || undefined,
          expected_updated_at: inspection.sync_preview?.expected_updated_at,
          expected_active_revision_id: inspection.sync_preview?.active_revision,
          migrate_secrets: migrateSecrets,
          parameterization_selected_ids: parameterizationSelectedIds,
          secret_bindings: secretBindingsRef.current,
          conflict_resolutions: conflictResolutions,
        })
      if (!requestIsCurrent()) return
      const importedScripts = outputMode === 'native_requests'
        ? validatedNativeCommitScripts(response, selectedNativeCandidates)
        : null
      if (outputMode === 'canonical' && !isCanonicalCommitResponse(response)) {
        throw new Error('Canonical Bruno commit response is missing its script and revision.')
      }
      setMigrateSecrets(false)
      setPreparedSecrets(null)
      secretBindingsRef.current = {}
      setCommitted(response)
      setCommittedDeferredSecretCount(deferredSecretCount)
      if (outputMode === 'native_requests') {
        setNotice(`${importedScripts!.length} native APEX script${importedScripts!.length === 1 ? '' : 's'} saved atomically.`)
        onImported?.({ scripts: importedScripts! })
      } else {
        setNotice(syncScriptId ? 'Bruno source synchronized as a new immutable revision.' : 'Bruno collection imported as a canonical APEX script.')
      }
      if (outputMode === 'canonical' && isCanonicalCommitResponse(response) && response.script.id) {
        await refreshRevisionState(response.script.id, requestIsCurrent)
        try {
          const status = await api.getScriptSecretStatus(response.script.id)
          if (requestIsCurrent()) setCommittedDeferredSecretCount(status.missing?.length || 0)
        } catch {
          // The pre-commit write-only summary is a safe fallback when the status
          // endpoint is temporarily unavailable.
        }
      }
    } catch (cause) {
      if (requestIsCurrent()) {
        const sourceSessionFailure = getSourceSecretSessionFailureMetadata(cause)
        if (migrateSecrets && sourceSessionFailure.retained) {
          // Native Bruno migration and script persistence share an import session.
          // Provider writes can survive while the server restores that session
          // specifically so the identical reviewed batch can be retried.
          setError(formatSourceSecretSessionFailure(cause))
        } else if (migrateSecrets && (sourceSessionFailure.consumed || sourceSessionFailure.manualConfigurationRequired)) {
          // The server reports only logical names here. Clear the consumed
          // import session and require a fresh inspection; never retain or
          // reconstruct any source credential material in the browser.
          setMigrateSecrets(false)
          setInspection(null)
          setPreparedSecrets(null)
          secretBindingsRef.current = {}
          setError(`${formatSourceSecretSessionFailure(cause)} Re-inspect the Bruno source to continue the script import.`)
        } else {
          setError(getErrorMessage(cause, 'Unable to commit the Bruno import.'))
        }
      }
    } finally {
      if (requestIsCurrent()) setLoading(false)
    }
  }

  const rollback = async (revision: BrunoSourceRevision) => {
    const scriptId = committed && isCanonicalCommitResponse(committed) ? committed.script.id : ''
    const activeRevision = revisions.find(candidate => candidate.active)
    if (!scriptId || revision.active) return
    setRollbackLoading(revision.id)
    setError('')
    try {
      await api.rollbackBrunoRevision(scriptId, revision.id, activeRevision?.id)
      await refreshRevisionState(scriptId)
      setNotice(`Activated Bruno source revision ${revision.id}.`)
    } catch (cause) {
      setError(getErrorMessage(cause, 'Unable to roll back the Bruno source revision.'))
    } finally {
      setRollbackLoading('')
    }
  }

  const applyCredentialBindings = (bindings: SecretBindingSelection[]) => {
    const next = Object.fromEntries(bindings.map(binding => [binding.requirement_id, binding.secret_name]))
    secretBindingsRef.current = next
  }

  const applyParameterization = (parameterizedScript: ScriptDefinition, _diffs: unknown[], selectedIds: string[]) => {
    const importSessionId = inspection?.import_session_id
    if (!importSessionId) return
    credentialReviewRef.current?.preserveEditsForNextReviewGeneration()
    advanceSourceGeneration()
    setParameterizationSelectedIds(previous => mergeProtectedParameterizationSelectionIds(previous, selectedIds))
    setInspection(current => current?.import_session_id === importSessionId
      ? { ...current, result: { ...current.result, script: parameterizedScript } }
      : current)
    setNotice(`Applied ${selectedIds.length} parameterization suggestion${selectedIds.length === 1 ? '' : 's'} to the pending Bruno script.`)
  }

  const commitProjection = async () => {
    if (!committed || !isCanonicalCommitResponse(committed) || !committed.script.id || !projection?.eligible) return
    setLoading(true)
    setError('')
    try {
      await api.commitBrunoProjection(committed.script.id)
      setNotice('Native projection committed with its equivalence proof.')
    } catch (cause) {
      setError(getErrorMessage(cause, 'Unable to commit the Bruno native projection.'))
    } finally {
      setLoading(false)
    }
  }

  const openCommitted = () => {
    if (!committed || !isCanonicalCommitResponse(committed)) return
    onImported?.({ scripts: [storedScriptFromDefinition(committed.script)] })
  }

  const close = () => {
    advanceSourceGeneration()
    setLoading(false)
    onClose?.()
  }

  const updateRunOption = <K extends keyof RunOptions>(key: K, value: RunOptions[K]) => {
    advanceSourceGeneration()
    setRunOptions(previous => ({ ...previous, [key]: value }))
    setInspection(null)
  }

  return (
    <div className="bruno-import">
      <header className="bruno-import-header">
        <div>
          <span className="bruno-kicker">{outputMode === 'native_requests' ? 'Native API conversion' : 'Full-fidelity source adapter'}</span>
          <h2>Import Bruno Collection</h2>
          <p>{outputMode === 'native_requests'
            ? 'Convert every HTTP API in an OpenCollection YAML folder into its own native APEX script.'
            : <>Preserve and execute classic <code>.bru</code>, OpenCollection YAML, archives, environments, scripts, dependencies, certificates, fixtures, and Bruno Apps without translating behavior away.</>}</p>
        </div>
        {onClose && <button className="btn btn-secondary" onClick={close}>Back</button>}
      </header>

      {requirements.length > 0 && !committed && (
        <SecretBindingReview
          ref={credentialReviewRef}
          requirements={credentialRequirements}
          reviewGeneration={`${sourceGeneration}:${projectId || ''}`}
          disabled={loading || parameterizationApplying}
          projectId={projectId}
          sourceSecretSessionExpiresAt={inspection?.expires_at}
          sourceMigrationAllowed={sourceMigrationAvailable}
          externalSourceMigrationSelected={migrateSecrets}
          onExternalSourceMigrationChange={setMigrateSecrets}
          onApply={applyCredentialBindings}
          onPreparedChange={setPreparedSecrets}
        />
      )}

      {outputMode === 'canonical' && inspection?.result.output_mode === 'canonical' && inspection.parameterization && inspection.parameterization.suggestions.length > 0 && !committed && (
        <ParameterizationReview
          parameterization={inspection.parameterization}
          script={inspection.result.script}
          reviewGeneration={`${sourceGeneration}:${projectId || ''}`}
          disabled={loading}
          onApplyingChange={setParameterizationApplying}
          onApplied={applyParameterization}
        />
      )}

      {error && <div className="settings-error" role="alert">{error}</div>}
      {notice && <div className="settings-notice" role="status">{notice}</div>}
      {!projectId && <div className="settings-error">Select a project before importing Bruno source.</div>}

      <section className="bruno-card">
        <div className="bruno-card-heading">
          <div>
            <h3>1. Source bundle</h3>
            <p>Choose a complete collection directory, deterministic archive, or standalone request file.</p>
          </div>
          {files.length > 0 && <span className="bruno-stat">{files.length} file{files.length === 1 ? '' : 's'} · {formatBytes(selectedBytes)}</span>}
        </div>
        <div className="bruno-source-actions">
          <button className="btn btn-secondary" disabled={loading} onClick={() => fileInputRef.current?.click()}>Choose files or archive</button>
          <button className="btn btn-secondary" disabled={loading} onClick={() => directoryInputRef.current?.click()}>Choose collection directory</button>
          {files.length > 0 && <button className="btn btn-ghost" disabled={loading} onClick={() => selectFiles([])}>Clear</button>}
        </div>
        <input
          ref={fileInputRef}
          hidden
          disabled={loading}
          multiple
          type="file"
          accept=".bru,.yml,.yaml,.json,.env,.js,.ts,.zip,.tar,.gz,.tgz,application/zip,application/gzip,text/yaml,application/yaml,application/json,text/plain"
          onChange={event => selectFiles(Array.from(event.target.files || []))}
        />
        <input
          ref={directoryInputRef}
          hidden
          disabled={loading}
          multiple
          type="file"
          onChange={event => selectFiles(Array.from(event.target.files || []))}
        />
        {files.length > 0 && (
          <div className="bruno-file-list">
            {files.slice(0, 12).map((file, index) => (
              <span key={`${file.webkitRelativePath || file.name}-${index}`}>{file.webkitRelativePath || file.name}</span>
            ))}
            {files.length > 12 && <span>+ {files.length - 12} more</span>}
          </div>
        )}
      </section>

      <section className="bruno-card">
        <div className="bruno-card-heading">
          <div>
            <h3>2. Import and execution profile</h3>
            <p>{outputMode === 'native_requests'
              ? 'Create one native HTTP script per API and save the selected batch to one destination.'
              : <>These settings become the canonical <code>bruno_run</code> selection. Operator limits can only reduce them.</>}</p>
          </div>
        </div>
        <div className="bruno-mode-selector" role="radiogroup" aria-label="Bruno import mode">
          <label className={outputMode === 'native_requests' ? 'active' : ''}>
            <input type="radio" name="bruno-output-mode" checked={outputMode === 'native_requests'} disabled={loading} onClick={() => confirmPendingOutputMode('native_requests')} onChange={() => selectOutputMode('native_requests')} />
            <span><strong>Native scripts per API</strong><small>Recommended for OpenCollection YAML HTTP requests.</small></span>
          </label>
          <label className={outputMode === 'canonical' ? 'active' : ''}>
            <input type="radio" name="bruno-output-mode" checked={outputMode === 'canonical'} disabled={loading} onClick={() => confirmPendingOutputMode('canonical')} onChange={() => selectOutputMode('canonical')} />
            <span><strong>Canonical Bruno collection</strong><small>Required for classic .bru and full Bruno runtime behavior.</small></span>
          </label>
        </div>
        {outputMode === 'canonical' ? (
          <>
        <div className="bruno-options-grid">
          <label>
            <span>Script name</span>
            <input disabled={loading} value={name} onChange={event => { advanceSourceGeneration(); setName(event.target.value); setInspection(null) }} placeholder="Use collection name" />
          </label>
          <label>
            <span>Synchronization target</span>
            <select disabled={loading} value={syncScriptId} onChange={event => { advanceSourceGeneration(); setSyncScriptId(event.target.value); setInspection(null); setCommitted(null) }}>
              <option value="">Create a new canonical script</option>
              {scripts.map(script => <option key={script.id} value={script.id}>{script.name} ({script.ref_key || script.id})</option>)}
            </select>
          </label>
          <label>
            <span>Sandbox</span>
            <select disabled={loading} value={sandbox} onChange={event => { advanceSourceGeneration(); setSandbox(event.target.value as 'safe' | 'developer'); setInspection(null) }}>
              <option value="safe">Safe (default)</option>
              <option value="developer">Developer isolated worker</option>
            </select>
          </label>
          <label>
            <span>Selection</span>
            <select disabled={loading} value={runOptions.selection} onChange={event => updateRunOption('selection', event.target.value)}>
              <option value="collection">Entire collection</option>
              <option value="folder">Folder path</option>
              <option value="request">Request path</option>
              <option value="tag">Tag selection</option>
            </select>
          </label>
          <label>
            <span>Paths (comma separated)</span>
            <input disabled={loading} value={runOptions.paths} onChange={event => updateRunOption('paths', event.target.value)} placeholder="orders/create.yml, smoke/" />
          </label>
          <label>
            <span>Execution</span>
            <select disabled={loading} value={runOptions.execution} onChange={event => updateRunOption('execution', event.target.value)}>
              <option value="sequential">Sequential, source order</option>
              <option value="parallel">Bounded parallel</option>
            </select>
          </label>
          <label>
            <span>Include tags</span>
            <input disabled={loading} value={runOptions.includeTags} onChange={event => updateRunOption('includeTags', event.target.value)} placeholder="smoke, checkout" />
          </label>
          <label>
            <span>Exclude tags</span>
            <input disabled={loading} value={runOptions.excludeTags} onChange={event => updateRunOption('excludeTags', event.target.value)} placeholder="manual, destructive" />
          </label>
          <label>
            <span>Delay between requests (ms)</span>
            <input disabled={loading} type="number" min={0} value={runOptions.delayMs} onChange={event => updateRunOption('delayMs', Math.max(0, Number(event.target.value) || 0))} />
          </label>
          <label>
            <span>Stream deadline (ms)</span>
            <input disabled={loading} type="number" min={1} value={runOptions.streamTimeoutMs} onChange={event => updateRunOption('streamTimeoutMs', Math.max(1, Number(event.target.value) || 1))} />
          </label>
          {inspection?.result.environments && inspection.result.environments.length > 0 && (
            <>
              <label>
                <span>Collection environment</span>
                <select disabled={loading} value={runOptions.defaultEnvironment} onChange={event => updateRunOption('defaultEnvironment', event.target.value)}>
                  <option value="">None</option>
                  {inspection.result.environments.map(environment => <option key={environment.path} value={environment.name}>{environment.name}</option>)}
                </select>
              </label>
              <label>
                <span>Global environment</span>
                <select disabled={loading} value={runOptions.defaultGlobalEnvironment} onChange={event => updateRunOption('defaultGlobalEnvironment', event.target.value)}>
                  <option value="">None</option>
                  {inspection.result.environments.map(environment => <option key={environment.path} value={environment.name}>{environment.name}</option>)}
                </select>
              </label>
            </>
          )}
        </div>
        <div className="bruno-checks">
          <label><input type="checkbox" disabled={loading} checked={runOptions.recursive} onChange={event => updateRunOption('recursive', event.target.checked)} /> Recursive folders</label>
          <label><input type="checkbox" disabled={loading} checked={runOptions.bail} onChange={event => updateRunOption('bail', event.target.checked)} /> Bail on failure</label>
          <label><input type="checkbox" disabled={loading} checked={runOptions.testsOnly} onChange={event => updateRunOption('testsOnly', event.target.checked)} /> Tests only</label>
          <label><input type="checkbox" disabled={loading} checked={runOptions.persistSession} onChange={event => updateRunOption('persistSession', event.target.checked)} /> Persist VUser session across APEX iterations</label>
        </div>
          </>
        ) : (
          <>
          <div className="bruno-options-grid">
            <label>
              <span>Destination folder</span>
              <select disabled={loading} value={destinationFolderId} onChange={event => setDestinationFolderId(event.target.value)}>
                <option value="">Select a script folder…</option>
                {destinationFolderOptions.map(({ folder, label }) => (
                  <option key={folder.id} value={folder.id}>{label}</option>
                ))}
              </select>
            </label>
          </div>
          {folderLoadError && (
            <div className="bruno-preflight warning" role="alert">
              {folderLoadError}{' '}
              <button
                className="btn btn-ghost"
                disabled={loading}
                onClick={() => setFolderReloadToken(value => value + 1)}
              >
                Retry
              </button>
            </div>
          )}
          </>
        )}
        {developerBlocked && (
          <div className="bruno-preflight warning">
            Developer execution is disabled or no isolated worker class is available. Import remains available, but execution will fail preflight until an operator enables it.
          </div>
        )}
        {importDisabled && (
          <div className="bruno-preflight warning" role="alert">
            Bruno import is disabled by an operator.
          </div>
        )}
		{selectionMissing && <div className="bruno-preflight warning">Choose at least one path for {runOptions.selection} selection.</div>}
		{outputMode === 'canonical' && inspection?.execution_preflight && !inspection.execution_preflight.ready && Array.isArray(inspection.execution_preflight.blockers) && (
		  <div className="bruno-preflight warning" role="status">
			Execution preflight is blocked: {inspection.execution_preflight.blockers.join(', ')}. The source can be preserved, but it cannot run until these operator prerequisites are resolved.
		  </div>
        )}
        <button className="btn btn-primary" disabled={!canInspect} onClick={() => void inspect()}>
          {modeDetectionPending ? 'Detecting source type…' : loading ? 'Inspecting…' : inspection ? 'Re-inspect source' : 'Inspect source'}
        </button>
      </section>

      {inspection && (
        <>
          <section className="bruno-summary-grid">
            <article className="bruno-card">
              <h3>Collection</h3>
              <strong>{inspection.result.collection.name}</strong>
              <p>{inspection.result.fidelity.executable_items} executable items · {inspection.result.fidelity.preserved_apps} Bruno Apps preserved</p>
              <CollectionTree items={inspection.result.collection.items} />
            </article>
            <article className="bruno-card">
              <h3>Fidelity and capabilities</h3>
              <dl className="bruno-details">
                <div><dt>Fidelity</dt><dd>{inspection.result.fidelity.status}</dd></div>
                <div><dt>Compatibility</dt><dd>{inspection.result.fidelity.compatibility}</dd></div>
                <div><dt>Bundle</dt><dd>{formatBytes(inspection.result.manifest.total_bytes)}</dd></div>
                <div><dt>Requests</dt><dd>{inspection.result.capabilities.request_types.join(', ') || 'None'}</dd></div>
                <div><dt>Required sandbox</dt><dd>{inspection.result.capabilities.required_sandbox}</dd></div>
              </dl>
              <div className="bruno-capability-list">
                {inspection.result.capabilities.has_javascript && <span>JavaScript</span>}
                {inspection.result.capabilities.has_npm_dependencies && <span>npm dependencies</span>}
                {inspection.result.capabilities.has_local_modules && <span>local modules</span>}
                {inspection.result.capabilities.has_file_bodies && <span>file bodies</span>}
                {inspection.result.capabilities.has_certificates && <span>certificates</span>}
                {inspection.result.capabilities.has_bruno_apps && <span>Bruno Apps</span>}
              </div>
            </article>
          </section>

          {outputMode === 'native_requests' && (
            <section className="bruno-card bruno-native-review">
              <div className="bruno-card-heading">
                <div>
                  <h3>{nativeCandidates.length} API request{nativeCandidates.length === 1 ? '' : 's'} found → {nativeCandidates.length} native script{nativeCandidates.length === 1 ? '' : 's'}</h3>
                  <p>Scripts stay in deterministic Bruno sequence/path order and will be committed as one atomic batch.</p>
                </div>
                <label className="bruno-select-all">
                  <input
                    type="checkbox"
                    disabled={loading || nativeCandidates.length === 0}
                    ref={node => {
                      if (node) node.indeterminate = someNativeCandidatesSelected
                    }}
                    aria-checked={someNativeCandidatesSelected ? 'mixed' : undefined}
                    checked={allNativeCandidatesSelected}
                    onChange={event => setSelectedCandidateIds(event.target.checked ? nativeCandidates.map(candidate => candidate.candidate_id) : [])}
                  />
                  Select all
                </label>
              </div>
              <div className="bruno-native-table-wrap">
                <table className="bruno-native-table">
                  <thead>
                    <tr>
                      <th aria-label="Selected" />
                      <th>Source</th>
                      <th>Script name</th>
                      <th>Request</th>
                      <th>Status</th>
                      <th>Details</th>
                    </tr>
                  </thead>
                  <tbody>
                    {nativeCandidates.map(candidate => {
                      const selected = selectedCandidateIds.includes(candidate.candidate_id)
                      const reviewFindings = nativeCandidateReviewFindings(candidate)
                      const diagnosticCount = (candidate.diagnostics || []).length
                      const missingVariableCount = (candidate.missing_variables || []).length
                      const secretCount = (candidate.secret_requirements || []).length
                      const status = reviewFindings.length > 0
                        ? 'review'
                        : missingVariableCount > 0
                          ? 'blocked'
                          : secretCount > 0
                            ? 'configure'
                            : diagnosticCount > 0 ? 'warning' : 'ready'
                      const statusLabel = status === 'review'
                        ? 'Review required'
                        : status === 'blocked'
                          ? 'Execution blocked'
                          : status === 'configure'
                            ? 'Configure secrets'
                            : status
                      const inheritedSources = (candidate.source_mappings || [])
                        .filter(mapping => mapping.role !== 'request')
                        .map(mapping => `${mapping.role || 'source'}: ${mapping.path || ''}`)
                      const inheritedBadges = inheritedSources
                      return (
                        <tr key={candidate.candidate_id}>
                          <td>
                            <input
                              type="checkbox"
                              checked={selected}
                              disabled={loading}
                              aria-label={`Select ${candidate.script.name}`}
                              onChange={event => setSelectedCandidateIds(previous => event.target.checked
                                ? [...previous, candidate.candidate_id]
                                : previous.filter(id => id !== candidate.candidate_id))}
                            />
                          </td>
                          <td>
                            <strong>{candidate.source_path}</strong>
                            {candidate.sequence !== undefined && <small>Sequence {candidate.sequence}</small>}
                            {inheritedBadges.length > 0 && (
                              <span className="bruno-native-badges">
                                {inheritedBadges.map(field => <em key={field}>inherited {field}</em>)}
                              </span>
                            )}
                          </td>
                          <td>
                            <input
                              aria-label={`Name for ${candidate.source_path}`}
                              disabled={loading || !selected}
                              value={nativeNameOverrides[candidate.candidate_id] ?? candidate.script.name}
                              onChange={event => setNativeNameOverrides(previous => ({ ...previous, [candidate.candidate_id]: event.target.value }))}
                            />
                          </td>
                          <td><code>{nativeCandidateMethod(candidate)}</code><small>{nativeCandidateTarget(candidate)}</small></td>
                          <td><span className={`bruno-native-status ${status}`}>{statusLabel}</span></td>
                          <td>
                            <details>
                              <summary>Review</summary>
                              {(candidate.missing_variables || []).length > 0 && (
                                <div><strong>Missing variables</strong><p>{candidate.missing_variables?.join(', ')}</p></div>
                              )}
                              {(candidate.secret_requirements || []).length > 0 && (
                                <div><strong>Secrets</strong><p>{candidate.secret_requirements?.map(requirement => requirement.suggested_name).join(', ')}</p></div>
                              )}
                              {(candidate.diagnostics || []).length > 0 && (
                                <div className="bruno-native-diagnostics">
                                  <strong>Diagnostics</strong>
                                  {candidate.diagnostics?.map((diagnostic, index) => (
                                    <p key={nativeFindingID(diagnostic) || `${diagnostic.code}-${index}`}>
                                      <code>{diagnostic.code || diagnostic.id || 'finding'}</code> {diagnostic.message}
                                    </p>
                                  ))}
                                </div>
                              )}
                              {(candidate.source_mappings || []).length > 0 && (
                                <div><strong>Source mappings</strong><pre className="bruno-json">{JSON.stringify(candidate.source_mappings, null, 2)}</pre></div>
                              )}
                              <strong>Masked native script</strong>
                              <BrunoImportPreviewScript script={candidate.script} />
                            </details>
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
              {nativeReviewFindingIds.length > 0 && (
                <div className="bruno-native-acknowledgements">
                  <h4>Review-gated conversions</h4>
                  <p>These scripts can be saved as drafts, but replay remains blocked until the unsupported behavior is translated and its gate removed.</p>
                  {nativeReviewFindingIds.map(findingId => (
                    <label key={findingId}>
                      <input
                        type="checkbox"
                        disabled={loading}
                        checked={acknowledgedFindingIds.includes(findingId)}
                        onChange={event => setAcknowledgedFindingIds(previous => event.target.checked
                          ? [...previous, findingId]
                          : previous.filter(id => id !== findingId))}
                      />
                      I reviewed and acknowledge <code>{findingId}</code>
                    </label>
                  ))}
                </div>
              )}
            </section>
          )}

          {(inspection.result.security_findings || []).length > 0 && (
            <section className="bruno-card">
              <h3>Security findings</h3>
              <div className="bruno-findings">
                {(inspection.result.security_findings || []).map(finding => (
                  <div key={`${finding.code}-${finding.path || ''}`} className={`bruno-finding ${finding.severity}`}>
                    <strong>{finding.code}</strong>
                    <span>{finding.summary}</span>
                    {finding.path && <small>{finding.path}</small>}
                  </div>
                ))}
              </div>
            </section>
          )}

          {(inspection.result.missing_variables || []).length > 0 && (
            <section className="bruno-card">
              <h3>Preflight requirements</h3>
              <p>These values were referenced but not defined in the uploaded source. Provide them through Bruno environments or APEX runtime bindings before execution.</p>
              <div className="bruno-capability-list">
                {(inspection.result.missing_variables || []).map(variable => <span key={variable}>{variable}</span>)}
              </div>
            </section>
          )}

          {inspection.sync_preview && (
            <section className="bruno-card">
              <h3>Synchronization preview</h3>
              <pre className="bruno-json">{JSON.stringify(inspection.sync_preview.changes, null, 2)}</pre>
              {conflicts.length > 0 && (
                <div className="bruno-conflicts">
                  {conflicts.map(conflict => (
                    <label key={conflict.id}>
                      <input
                        type="checkbox"
                        disabled={loading}
                        checked={conflictResolutions[conflict.id] === conflict.required_resolution}
                        onChange={event => setConflictResolutions(previous => ({
                          ...previous,
                          [conflict.id]: event.target.checked ? conflict.required_resolution : '',
                        }))}
                      />
                      <span><strong>{conflict.field}</strong>{conflict.summary}<small>Resolve with: {conflict.required_resolution}</small></span>
                    </label>
                  ))}
                </div>
              )}
            </section>
          )}

          {!committed && (
            <section className="bruno-commit-bar">
              <div>
                <strong>{outputMode === 'native_requests'
                  ? `Save ${selectedNativeCandidates.length} native script${selectedNativeCandidates.length === 1 ? '' : 's'}`
                  : syncScriptId ? 'Create synchronized revision' : 'Commit canonical Bruno script'}</strong>
                <span>{outputMode === 'native_requests'
                  ? destinationFolderId
                    ? 'All selected scripts will be saved atomically to the selected folder.'
                    : 'Select a destination folder before saving.'
                  : 'Raw and sanitized source artifacts are immutable, encrypted, and content-addressed.'}</span>
                {unacknowledgedFindingIds.length > 0 && <span>{unacknowledgedFindingIds.length} review finding{unacknowledgedFindingIds.length === 1 ? '' : 's'} must be acknowledged before saving.</span>}
                {duplicateNativeNames && <span>Selected script names must be unique.</span>}
              </div>
              <button className="btn btn-primary" disabled={!canCommit} onClick={() => void commit()}>
                {commitButtonLabel}
              </button>
            </section>
          )}
        </>
      )}

      {committed && outputMode === 'canonical' && isCanonicalCommitResponse(committed) && (
        <section className="bruno-card bruno-committed">
          <div className="bruno-card-heading">
            <div>
              <span className="bruno-kicker">Canonical source active</span>
              <h3>{committed.script.name}</h3>
              <p>Revision {revisions.find(revision => revision.active)?.id || committed.revision.id}</p>
              {committedDeferredSecretCount > 0 && (
                <p>{committedDeferredSecretCount} project secret{committedDeferredSecretCount === 1 ? ' still needs' : 's still need'} configuration.</p>
              )}
            </div>
            <div className="settings-inline-actions">
              {committedDeferredSecretCount > 0 && projectId && onConfigureSecrets && (
                <button className="btn btn-secondary" type="button" onClick={() => onConfigureSecrets(projectId)}>Configure secrets</button>
              )}
              <button className="btn btn-primary" onClick={openCommitted}>Open imported script</button>
            </div>
          </div>
          <div className="bruno-source-links">
            <DesktopApiResourceLink className="btn btn-secondary" href={`/api/v1/scripts/${committed.script.id}/bruno-imports/source`} filename={`${committed.script.name || committed.script.id}-sanitized-source.zip`} onError={setError}>Download sanitized source</DesktopApiResourceLink>
            <DesktopApiResourceLink className="btn btn-secondary" href={`/api/v1/scripts/${committed.script.id}/bruno-imports/source?raw=true`} filename={`${committed.script.name || committed.script.id}-raw-source.zip`} onError={setError}>Download restricted raw source</DesktopApiResourceLink>
          </div>
          {revisions.length > 0 && (
            <div className="bruno-revisions">
              <h4>Source revisions</h4>
              {revisions.map(revision => (
                <div key={revision.id} className={revision.active ? 'active' : ''}>
                  <span><strong>{revision.id}</strong><small>{new Date(revision.created_at).toLocaleString()} · {revision.source_format}</small></span>
                  {revision.active
                    ? <span className="bruno-active-pill">Active</span>
                    : <button className="btn btn-sm btn-secondary" disabled={rollbackLoading === revision.id} onClick={() => void rollback(revision)}>{rollbackLoading === revision.id ? 'Activating…' : 'Roll back'}</button>}
                </div>
              ))}
            </div>
          )}
          {projection && (
            <div className="bruno-projection">
              <div>
                <h4>Native APEX projection</h4>
                <p>{projection.eligible ? 'Differential analysis found an eligible native subset.' : 'Canonical Bruno execution remains authoritative; no equivalence proof is available.'}</p>
              </div>
              <button
                className="btn btn-secondary"
                disabled={!projection.eligible || !support?.native_projection_enabled || loading}
                onClick={() => void commitProjection()}
              >
                Commit proven projection
              </button>
            </div>
          )}
        </section>
      )}

      {committed && outputMode === 'native_requests' && isNativeCommitResponse(committed) && (
        <section className="bruno-card bruno-committed">
          <div className="bruno-card-heading">
            <div>
              <span className="bruno-kicker">Native batch saved</span>
              <h3>{committed.count ?? committed.scripts?.length ?? 0} API script{(committed.count ?? committed.scripts?.length ?? 0) === 1 ? '' : 's'} created</h3>
              <p>{committed.idempotent_replay ? 'The existing idempotent batch receipt was returned.' : 'The complete selected batch was committed atomically.'}</p>
            </div>
          </div>
          <div className="bruno-native-saved-list">
            {(committed.scripts || []).map(script => (
              <span key={script.id || script.name}><strong>{script.name}</strong><small>{script.ref_key || script.id}</small></span>
            ))}
          </div>
        </section>
      )}
    </div>
  )
}

export function BrunoImportPreviewScript({ script }: { script: ScriptDefinition }) {
  return <pre className="bruno-json">{JSON.stringify(script, null, 2)}</pre>
}
