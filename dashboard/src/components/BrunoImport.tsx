import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'

import { type Folder, type StoredScript } from '../types'
import { type ScriptDefinition } from '../types/scripts'
import {
  type BrunoCollectionItem,
  type BrunoInspectResponse,
  type BrunoNativeDiagnostic,
  type BrunoNativeScriptCandidate,
  type BrunoProjectionPreview,
  type BrunoSecretRequirement,
  type BrunoSupportMatrixResponse,
  type CommitBrunoCanonicalResponse,
  type CommitBrunoImportResponse,
  type CommitBrunoNativeBatchResponse,
  type ImportSourceRevision,
  type SecretBindingRequirement,
  type SecretBindingSelection,
} from '../types/scriptImports'
import { useApi } from '../hooks/useApi'
import { ApiError } from '../hooks/apiClient'
import { useModalDialog } from '../hooks/useModalDialog'
import { canCreateScriptFolders } from '../utils/authAccessUtils'
import { getErrorMessage } from '../utils/errorUtils'
import DesktopApiResourceLink from './DesktopApiResourceLink'
import SourceCodeViewer from './SourceCodeViewer'
import SecretBindingReview, {
  type PreparedSecretBindings,
  type RequirementDestinationState,
  type SecretBindingReviewHandle,
} from './SecretBindingReview'
import ParameterizationReview from './ParameterizationReview'
import SharedImportValues from './SharedImportValues'
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
import { nativeReviewGuidance } from './brunoNativeReviewGuidance'
import { brunoSecretPresentation } from './brunoSecretPresentation'
import { aggregateBrunoSecretRequirements } from './brunoSecretRequirements'
import { mergeProtectedParameterizationSelectionIds } from './protectedImportSession'
import { importFileLogicalPath } from '../importLogicalPath'
import './BrunoImport.css'

interface Props {
  initialFiles?: File[] | null
  initialOutputMode?: OutputMode | null
  projectId?: string
  folderId?: string | null
  onFolderChange?: (folderId: string) => void
  /** The authoring shell renders and owns the only visible destination control. */
  destinationFolderManagedExternally?: boolean
  onImported?: (result: ImportedSaveResult) => void
  onConfigureSecrets?: (projectId: string) => void
  onClose?: () => void
  onOperationBusyChange?: (busy: boolean) => void
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
type BrunoInspectionTab = 'summary' | 'source_apis' | 'credentials' | 'data' | 'import'
type NativeCandidateSortKey = 'source' | 'script' | 'request' | 'status' | 'details'
type FolderCreationAccess = 'checking' | 'allowed' | 'denied' | 'unavailable'

const FOLDER_CREATION_DENIED_MESSAGE = 'Your current role can import scripts but cannot create project folders. Select an existing script folder, or ask a Project Administrator to create one for this project.'

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

const NATIVE_CANDIDATE_PAGE_SIZE = 100
const NATIVE_FINDING_PAGE_SIZE = 100
const COLLECTION_TREE_PAGE_SIZE = 100
const MAX_DETAILED_REVIEW_FINDING_BLOCKERS = 10
const BRUNO_INSPECTION_TABS: ReadonlyArray<{ id: BrunoInspectionTab; label: string }> = [
  { id: 'summary', label: 'Summary' },
  { id: 'source_apis', label: 'Source APIs' },
  { id: 'credentials', label: 'Credentials' },
  { id: 'data', label: 'Data' },
  { id: 'import', label: 'Import APIs' },
]

function splitList(value: string) {
  return value.split(',').map(item => item.trim()).filter(Boolean)
}

function isOpenCollectionYAML(files: File[]) {
  return files.some(isOpenCollectionFileName)
}

function needsBrunoContentModeDetection(files: File[]) {
  if (files.length === 0 || isOpenCollectionYAML(files)) return false
  return files.some(file => /\.(?:ya?ml|json|zip)$/i.test(file.name))
}

function suggestedBrunoFolderName(files: File[]) {
  if (files.length === 0) return 'Imported Bruno APIs'
  const sourcePath = importFileLogicalPath(files[0])
  const firstSegment = sourcePath.split('/').filter(Boolean)[0] || files[0].name
  const withoutSuffix = firstSegment
    .replace(/\.bruno\.(?:tar\.gz|tar|zip)$/i, '')
    .replace(/\.(?:tar\.gz|tgz|tar|zip|bru|ya?ml|json)$/i, '')
  const readable = withoutSuffix.replace(/[_-]+/g, ' ').replace(/\s+/g, ' ').trim()
  return readable || 'Imported Bruno APIs'
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

function nativeCandidateSortValue(candidate: BrunoNativeScriptCandidate, key: NativeCandidateSortKey) {
  switch (key) {
    case 'source':
      return `${candidate.source_path || ''}\u0000${candidate.sequence ?? Number.MAX_SAFE_INTEGER}`
    case 'script':
      return candidate.script.name || ''
    case 'request':
      return `${nativeCandidateMethod(candidate)}\u0000${nativeCandidateTarget(candidate)}`
    case 'status':
      return nativeCandidateReviewFindings(candidate).length > 0
        ? 'review'
        : (candidate.missing_variables || []).length > 0
          ? 'blocked'
          : (candidate.secret_requirements || []).length > 0
            ? 'configure'
            : (candidate.diagnostics || []).length > 0 ? 'warning' : 'ready'
    case 'details':
      return `${nativeCandidateReviewFindings(candidate).length}\u0000${(candidate.diagnostics || []).length}`
  }
}

function nativeSecretRequirementNoImportedValueMessage(
  requirement: BrunoSecretRequirement,
  destinationSecretName: string,
): string {
  if (
    requirement.unresolved_source_variable !== true
    || requirement.source_value_available === true
    || requirement.destination_value_preserved === true
    || requirement.preservation_unavailable_reason !== 'source_value_unavailable'
  ) return ''
  const sourceReference = requirement.source_variable
    ? ` for {{${requirement.source_variable}}}`
    : ''
  const destinationReference = destinationSecretName
    ? `{{secret.${destinationSecretName}}}`
    : 'a project-secret reference whose name is still required'
  return `No value${sourceReference} was included in the collection, so there is no imported value to preserve. The generated script uses ${destinationReference}; execution requires a configured value for that project secret.`
}

function BrunoNativeReviewDialog({
  candidate,
  candidateSelected,
  requirementDestinationState,
  onClose,
}: {
  candidate: BrunoNativeScriptCandidate
  candidateSelected: boolean
  requirementDestinationState: Readonly<Record<string, RequirementDestinationState>>
  onClose: () => void
}) {
  const dialogRef = useRef<HTMLElement | null>(null)
  const closeButtonRef = useRef<HTMLButtonElement | null>(null)
  useModalDialog({ dialogRef, initialFocusRef: closeButtonRef, onClose })

  return createPortal(
    <div
      className="modal-overlay bruno-native-review-overlay"
      role="presentation"
      onClick={event => {
        if (event.target === event.currentTarget) onClose()
      }}
    >
      <section
        ref={dialogRef}
        className="modal-content bruno-native-review-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="bruno-native-review-dialog-title"
        aria-describedby="bruno-native-review-dialog-source"
      >
        <header className="bruno-native-review-dialog-header">
          <div>
            <h3 id="bruno-native-review-dialog-title">
              {candidateSelected ? 'Review imported script' : 'Preview detected script'}
            </h3>
            <p id="bruno-native-review-dialog-source">{candidate.source_path}</p>
          </div>
          <button
            ref={closeButtonRef}
            type="button"
            className="btn btn-ghost bruno-native-review-dialog-close"
            aria-label={`Close review for ${candidate.source_path}`}
            onClick={onClose}
          >
            ×
          </button>
        </header>
        <div className="bruno-native-review-dialog-body">
          {!candidateSelected && (
            <div className="bruno-native-review-selection-note" role="status">
              <strong>Preview only — not selected for import.</strong>
              <span>Select this request in the scripts table before relying on credential destination choices.</span>
            </div>
          )}
          <div className="bruno-native-review-summary" aria-label="Imported request summary">
            <span><small>Method</small><code>{nativeCandidateMethod(candidate)}</code></span>
            <span><small>Target</small><code>{nativeCandidateTarget(candidate)}</code></span>
            {candidate.sequence !== undefined && <span><small>Sequence</small><strong>{candidate.sequence}</strong></span>}
          </div>
          {(candidate.missing_variables || []).length > 0 && (
            <section className="bruno-native-review-section">
              <h4>Missing variables</h4>
              <p>{candidate.missing_variables?.join(', ')}</p>
            </section>
          )}
          {(candidate.secret_requirements || []).length > 0 && (
            <section className="bruno-native-review-section">
              <h4>Secrets</h4>
              <ul>
                {candidate.secret_requirements?.map(requirement => {
                  const presentation = brunoSecretPresentation(requirement)
                  const liveDestination = candidateSelected
                    ? requirementDestinationState[requirement.id]
                    : undefined
                  const usesProjectSecret = liveDestination?.usesProjectSecret
                    ?? (requirement.destination_value_preserved !== true)
                  const destinationSecretName = liveDestination
                    ? liveDestination.secretName
                    : requirement.suggested_name
                  const noImportedValueMessage = presentation.preservationUnavailableDescription
                    ? ''
                    : nativeSecretRequirementNoImportedValueMessage(requirement, destinationSecretName)
                  const currentDestinationGuidance = liveDestination?.guidance || ''
                  return (
                    <li key={requirement.id}>
                      <strong>{presentation.label}</strong>
                      {presentation.origin ? (
                        <>
                          <span>Import context: {presentation.context}</span>
                          <span>Runtime purpose: {presentation.purpose}</span>
                        </>
                      ) : requirement.context ? <span>{requirement.context}</span> : null}
                      {requirement.source_variable && (
                        <span>Source variable: <code>{`{{${requirement.source_variable}}}`}</code></span>
                      )}
                      {usesProjectSecret ? (
                        <span>
                          {candidateSelected ? 'Project secret reference' : 'Detected project-secret suggestion'}:{' '}
                          <code>{destinationSecretName ? `{{secret.${destinationSecretName}}}` : '{{secret.NAME}}'}</code>
                        </span>
                      ) : (
                        <span>
                          Optional project-secret suggestion: <code>{`{{secret.${requirement.suggested_name}}}`}</code>
                        </span>
                      )}
                      {noImportedValueMessage && (
                        <span>{noImportedValueMessage}</span>
                      )}
                      {currentDestinationGuidance && (
                        <span>Current destination: {currentDestinationGuidance}</span>
                      )}
                    </li>
                  )
                })}
              </ul>
            </section>
          )}
          {(candidate.diagnostics || []).length > 0 && (
            <section className="bruno-native-review-section bruno-native-diagnostics">
              <h4>Diagnostics</h4>
              {candidate.diagnostics?.map((diagnostic, index) => (
                <p key={nativeFindingID(diagnostic) || `${diagnostic.code}-${index}`}>
                  <code>{diagnostic.code || 'finding'}</code> {diagnostic.message}
                </p>
              ))}
            </section>
          )}
          {(candidate.source_mappings || []).length > 0 && (
            <section className="bruno-native-review-section">
              <h4>Source mappings</h4>
              <SourceCodeViewer
                ariaLabel="Source mappings JSON"
                className="bruno-native-review-code-viewer"
                sourcePath="source-mappings.json"
                value={JSON.stringify(candidate.source_mappings, null, 2)}
              />
            </section>
          )}
          <section className="bruno-native-review-section">
            <h4>Masked APEX Load DSL script</h4>
            <BrunoImportPreviewScript script={candidate.script} />
          </section>
        </div>
        <footer className="bruno-native-review-dialog-footer">
          <button type="button" className="btn btn-secondary" onClick={onClose}>Close</button>
        </footer>
      </section>
    </div>,
    document.body,
  )
}

function BrunoNativeSourceDialog({
  candidate,
  importSessionId,
  projectId,
  loadSource,
  onClose,
}: {
  candidate: BrunoNativeScriptCandidate
  importSessionId: string
  projectId: string
  loadSource: (importSessionId: string, projectId: string, sourcePath: string) => Promise<string>
  onClose: () => void
}) {
  const dialogRef = useRef<HTMLElement | null>(null)
  const closeButtonRef = useRef<HTMLButtonElement | null>(null)
  const [source, setSource] = useState<string | null>(null)
  const [error, setError] = useState('')
  useModalDialog({ dialogRef, initialFocusRef: closeButtonRef, onClose })

  useEffect(() => {
    let cancelled = false
    setSource(null)
    setError('')
    void loadSource(importSessionId, projectId, candidate.source_path).then(content => {
      if (!cancelled) setSource(content)
    }).catch((sourceError: unknown) => {
      if (!cancelled) {
        setError(sourceError instanceof Error ? sourceError.message : 'Unable to load the sanitized source API.')
      }
    })
    return () => { cancelled = true }
  }, [candidate.candidate_id, candidate.source_path, importSessionId, loadSource, projectId])

  return createPortal(
    <div className="modal-overlay bruno-native-review-overlay" role="presentation" onClick={event => {
      if (event.target === event.currentTarget) onClose()
    }}>
      <section ref={dialogRef} className="modal-content bruno-native-review-dialog bruno-native-source-dialog" role="dialog" aria-modal="true" aria-labelledby="bruno-native-source-dialog-title" aria-describedby="bruno-native-source-dialog-source">
        <header className="bruno-native-review-dialog-header">
          <div>
            <h3 id="bruno-native-source-dialog-title">Inspect source API</h3>
            <p id="bruno-native-source-dialog-source">{candidate.source_path}</p>
          </div>
          <button ref={closeButtonRef} type="button" className="btn btn-ghost bruno-native-review-dialog-close" aria-label={'Close source inspection for ' + candidate.source_path} onClick={onClose}>×</button>
        </header>
        <div className="bruno-native-review-dialog-body bruno-native-source-dialog-body">
          <section className="bruno-native-review-section bruno-native-source-section">
            <h4>Source API</h4>
            {error
              ? <p role="alert">{error}</p>
              : source == null
                ? <p className="bruno-native-source-loading">Loading source…</p>
                : <SourceCodeViewer
                    className="bruno-native-source-viewer"
                    sourcePath={candidate.source_path}
                    value={source}
                  />}
          </section>
        </div>
        <footer className="bruno-native-review-dialog-footer">
          <button type="button" className="btn btn-secondary" onClick={onClose}>Close</button>
        </footer>
      </section>
    </div>,
    document.body,
  )
}

function CollectionTreeItem({ item }: { item: BrunoCollectionItem }) {
  const [expanded, setExpanded] = useState(false)
  const hasChildren = Boolean(item.children?.length)
  return (
    <li>
      <div className="bruno-tree-row">
        {hasChildren && (
          <button
            type="button"
            className="btn btn-ghost bruno-tree-toggle"
            aria-expanded={expanded}
            aria-label={`${expanded ? 'Collapse' : 'Expand'} ${item.name}`}
            onClick={() => setExpanded(value => !value)}
          >
            {expanded ? 'Collapse' : 'Expand'}
          </button>
        )}
        <span className={`bruno-type bruno-type-${item.type}`}>{item.type}</span>
        <span>{item.name}</span>
        {!item.executable && <small>preserved only</small>}
        {item.has_scripts && <small>scripts</small>}
        {item.has_tests && <small>tests</small>}
      </div>
      {expanded && item.children && <CollectionTree items={item.children} />}
    </li>
  )
}

function CollectionTree({ items }: { items: BrunoCollectionItem[] }) {
  const [page, setPage] = useState(0)
  const pageCount = Math.max(1, Math.ceil(items.length / COLLECTION_TREE_PAGE_SIZE))
  const safePage = Math.min(page, pageCount - 1)
  const start = safePage * COLLECTION_TREE_PAGE_SIZE
  const visibleItems = items.slice(start, start + COLLECTION_TREE_PAGE_SIZE)
  useEffect(() => setPage(0), [items])
  if (items.length === 0) return <p className="bruno-empty">No collection items were detected.</p>
  return (
    <>
      <ul className="bruno-tree">
        {visibleItems.map(item => <CollectionTreeItem key={`${item.uid}-${item.path}`} item={item} />)}
      </ul>
      {pageCount > 1 && (
        <nav className="bruno-native-table-pagination" aria-label="Collection tree pages">
          <span>Items {start + 1}–{Math.min(start + COLLECTION_TREE_PAGE_SIZE, items.length)} of {items.length}</span>
          <button type="button" className="btn btn-ghost" disabled={safePage === 0} onClick={() => setPage(value => Math.max(0, value - 1))}>Previous {COLLECTION_TREE_PAGE_SIZE}</button>
          <button type="button" className="btn btn-ghost" disabled={safePage + 1 >= pageCount} onClick={() => setPage(value => Math.min(pageCount - 1, value + 1))}>Next {COLLECTION_TREE_PAGE_SIZE}</button>
        </nav>
      )}
    </>
  )
}

export default function BrunoImport({ initialFiles, initialOutputMode, projectId, folderId, onFolderChange, destinationFolderManagedExternally = false, onImported, onConfigureSecrets, onClose, onOperationBusyChange }: Props) {
  const api = useApi()
  const fileInputRef = useRef<HTMLInputElement>(null)
  const directoryInputRef = useRef<HTMLInputElement>(null)
  const folderCreateRequestRef = useRef(0)
  const [files, setFiles] = useState<File[]>(initialFiles || [])
  const [outputMode, setOutputMode] = useState<OutputMode>(() => initialOutputMode || (isOpenCollectionYAML(initialFiles || []) ? 'native_requests' : 'canonical'))
  const initiallyDetectingMode = !initialOutputMode && needsBrunoContentModeDetection(initialFiles || [])
  const [modeDetectionPending, setModeDetectionPending] = useState(initiallyDetectingMode)
  const modeDetectionPendingRef = useRef(initiallyDetectingMode)
  const modeDetectionRequestRef = useRef(0)
  const [recommendedOutputMode, setRecommendedOutputMode] = useState<OutputMode | null>(() => {
    if (initialOutputMode) return initialOutputMode
    return isOpenCollectionYAML(initialFiles || []) ? 'native_requests' : null
  })
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
  const [protectedSessionNow, setProtectedSessionNow] = useState(() => Date.now())
  const [support, setSupport] = useState<BrunoSupportMatrixResponse | null>(null)
  const [scripts, setScripts] = useState<StoredScript[]>([])
  const [folders, setFolders] = useState<Folder[]>([])
  const [folderLoadError, setFolderLoadError] = useState('')
  const [folderLoading, setFolderLoading] = useState(() => Boolean(projectId && !destinationFolderManagedExternally))
  const [folderSelectionWarning, setFolderSelectionWarning] = useState('')
  const [folderReloadToken, setFolderReloadToken] = useState(0)
  const [localDestinationFolderId, setLocalDestinationFolderId] = useState(folderId ?? '')
  const destinationFolderId = folderId === undefined ? localDestinationFolderId : folderId || ''
  const selectDestinationFolder = useCallback((nextFolderId: string) => {
    setFolderSelectionWarning('')
    setLocalDestinationFolderId(nextFolderId)
    onFolderChange?.(nextFolderId)
  }, [onFolderChange])
  const destinationFolderIdRef = useRef(destinationFolderId)
  destinationFolderIdRef.current = destinationFolderId
  const selectDestinationFolderRef = useRef(selectDestinationFolder)
  selectDestinationFolderRef.current = selectDestinationFolder
  const [newFolderName, setNewFolderName] = useState(() => suggestedBrunoFolderName(initialFiles || []))
  const [folderCreateError, setFolderCreateError] = useState('')
  const [creatingFolder, setCreatingFolder] = useState(false)
  const [folderCreationAccess, setFolderCreationAccess] = useState<FolderCreationAccess>(projectId ? 'checking' : 'denied')
  const [selectedCandidateIds, setSelectedCandidateIds] = useState<string[]>([])
  const [nativeCandidatePage, setNativeCandidatePage] = useState(0)
  const [nativeCandidateSort, setNativeCandidateSort] = useState<{ key: NativeCandidateSortKey; direction: 'asc' | 'desc' }>({ key: 'source', direction: 'asc' })
  const [nativeFindingPage, setNativeFindingPage] = useState(0)
  const [nativeNameOverrides, setNativeNameOverrides] = useState<Record<string, string>>({})
  const [acknowledgedFindingIds, setAcknowledgedFindingIds] = useState<string[]>([])
  const [nativeReviewCandidateId, setNativeReviewCandidateId] = useState<string | null>(null)
  const [nativeSourceCandidateId, setNativeSourceCandidateId] = useState<string | null>(null)
  const [activeInspectionTab, setActiveInspectionTab] = useState<BrunoInspectionTab>('summary')
  const [migrateSecrets, setMigrateSecrets] = useState(false)
  const [preparedSecrets, setPreparedSecrets] = useState<PreparedSecretBindings | null>(null)
  const [credentialDestinationState, setCredentialDestinationState] = useState<
    Record<string, RequirementDestinationState>
  >({})
  const [configuredCredentialReviewIdentity, setConfiguredCredentialReviewIdentity] = useState('')
  const secretBindingsRef = useRef<Record<string, string>>({})
  const credentialReviewRef = useRef<SecretBindingReviewHandle | null>(null)
  const [conflictResolutions, setConflictResolutions] = useState<Record<string, string>>({})
  const [committed, setCommitted] = useState<CommitBrunoImportResponse | null>(null)
  const [committedDeferredSecretCount, setCommittedDeferredSecretCount] = useState(0)
  const [parameterizationSelectedIds, setParameterizationSelectedIds] = useState<string[]>([])
  const [parameterizationApplying, setParameterizationApplying] = useState(false)
  // Bruno artifacts never leave the server, so collection values are collected
  // here and applied server-side by the native commit rather than through the
  // generic shared-values apply endpoint.
  const [sharedValueSelections, setSharedValueSelections] = useState<Record<string, string>>({})
  const [revisions, setRevisions] = useState<ImportSourceRevision[]>([])
  const [projection, setProjection] = useState<BrunoProjectionPreview | null>(null)
  const [loading, setLoading] = useState(false)
  const [rollbackLoading, setRollbackLoading] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const operationBusy = loading || folderLoading || creatingFolder || parameterizationApplying
  useEffect(() => {
    onOperationBusyChange?.(operationBusy)
    return () => onOperationBusyChange?.(false)
  }, [onOperationBusyChange, operationBusy])
  const sourceGenerationRef = useRef(0)
  const mountedRef = useRef(true)
  const [sourceGeneration, setSourceGeneration] = useState(0)
  const credentialReviewIdentity = `${sourceGeneration}:${projectId || ''}`
  const projectSecretsConfiguredForReview = configuredCredentialReviewIdentity === credentialReviewIdentity
  const advanceSourceGeneration = useCallback(() => {
    const next = sourceGenerationRef.current + 1
    sourceGenerationRef.current = next
    setSourceGeneration(next)
    setNativeReviewCandidateId(null)
    setCredentialDestinationState({})
    setNativeCandidatePage(0)
    setNativeFindingPage(0)
    return next
  }, [])
  const selectSynchronizationTarget = useCallback((nextScriptId: string) => {
    advanceSourceGeneration()
    setSyncScriptId(nextScriptId)
    setInspection(null)
    setCommitted(null)
    const target = scripts.find(script => script.id === nextScriptId)
    if (target) selectDestinationFolder(target.folder_id || '')
  }, [advanceSourceGeneration, scripts, selectDestinationFolder])
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
    if (!inspection?.import_session_id || outputMode !== 'native_requests') return
    setActiveInspectionTab('summary')
  }, [inspection?.import_session_id, outputMode])

  useEffect(() => {
    const nextFiles = initialFiles || []
    advanceSourceGeneration()
    const modeDetectionRequest = modeDetectionRequestRef.current + 1
    modeDetectionRequestRef.current = modeDetectionRequest
    const shouldDetectMode = !initialOutputMode && needsBrunoContentModeDetection(nextFiles)
    updateModeDetectionPending(shouldDetectMode)
    setFiles(nextFiles)
    const immediateMode = initialOutputMode || (isOpenCollectionYAML(nextFiles) ? 'native_requests' : 'canonical')
    setOutputMode(immediateMode)
    setRecommendedOutputMode(initialOutputMode || (isOpenCollectionYAML(nextFiles) ? 'native_requests' : null))
    setNewFolderName(suggestedBrunoFolderName(nextFiles))
    setFolderCreateError('')
    setCreatingFolder(false)
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
        const detectedMode = isNative ? 'native_requests' : 'canonical'
        setRecommendedOutputMode(detectedMode)
        setOutputMode(detectedMode)
        updateModeDetectionPending(false)
      })
    }
  }, [advanceSourceGeneration, initialFiles, initialOutputMode, updateModeDetectionPending])

  useEffect(() => {
    if (resetProjectIdRef.current === projectId) return
    folderCreateRequestRef.current += 1
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
    selectDestinationFolder('')
    setFolderCreateError('')
    setCreatingFolder(false)
    setSyncScriptId('')
  }, [advanceSourceGeneration, projectId, selectDestinationFolder])

  useEffect(() => {
    let cancelled = false
    const shouldLoadFolders = Boolean(projectId && !destinationFolderManagedExternally)
    setFolderLoading(shouldLoadFolders)
    setFolderLoadError('')
    if (shouldLoadFolders) setFolders([])
    void Promise.all([
      api.getBrunoSupportMatrix().catch(() => null),
      projectId ? api.listScripts(projectId).catch(() => ({ scripts: [] as StoredScript[], count: 0 })) : Promise.resolve({ scripts: [] as StoredScript[], count: 0 }),
      projectId && !destinationFolderManagedExternally
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
      const loadedFolders = folderResponse.folders || []
      setFolders(loadedFolders)
      setFolderLoadError(folderResponse.loadError)
      setFolderLoading(false)
      const selectedFolderID = destinationFolderIdRef.current
      if (shouldLoadFolders && selectedFolderID && (
        folderResponse.loadError || !loadedFolders.some(folder => folder.id === selectedFolderID)
      )) {
        selectDestinationFolderRef.current('')
        setFolderSelectionWarning(folderResponse.loadError
          ? 'The selected destination was cleared because its folder could not be revalidated. Retry loading folders before saving.'
          : 'The previously selected destination folder no longer exists in this project. Select another script folder before saving.')
      }
    })
    return () => { cancelled = true }
  }, [api, destinationFolderManagedExternally, folderReloadToken, projectId])

  useEffect(() => {
    if (!projectId || destinationFolderManagedExternally) {
      setFolderCreationAccess('denied')
      return
    }
    let cancelled = false
    setFolderCreationAccess('checking')
    void api.getAuthEffectiveAccess(projectId)
      .then(response => {
        if (cancelled) return
        setFolderCreationAccess(canCreateScriptFolders(response.resources || [])
          ? 'allowed'
          : 'denied')
      })
      .catch(() => {
        if (!cancelled) setFolderCreationAccess('unavailable')
      })
    return () => { cancelled = true }
  }, [api, destinationFolderManagedExternally, projectId])

  const nativeCandidates = useMemo(
    () => inspection?.result.output_mode === 'native_requests' ? inspection.result.scripts : [],
    [inspection],
  )
  const sortedNativeCandidates = useMemo(() => {
    const direction = nativeCandidateSort.direction === 'asc' ? 1 : -1
    return [...nativeCandidates].sort((left, right) => {
      const comparison = nativeCandidateSortValue(left, nativeCandidateSort.key)
        .localeCompare(nativeCandidateSortValue(right, nativeCandidateSort.key), undefined, { numeric: true, sensitivity: 'base' })
      return comparison === 0 ? left.candidate_id.localeCompare(right.candidate_id) : comparison * direction
    })
  }, [nativeCandidateSort, nativeCandidates])
  const selectedCandidateIdSet = useMemo(
    () => new Set(selectedCandidateIds),
    [selectedCandidateIds],
  )
  const nativeCandidatePageCount = Math.max(
    1,
    Math.ceil(nativeCandidates.length / NATIVE_CANDIDATE_PAGE_SIZE),
  )
  const safeNativeCandidatePage = Math.min(nativeCandidatePage, nativeCandidatePageCount - 1)
  const nativeCandidatePageStart = safeNativeCandidatePage * NATIVE_CANDIDATE_PAGE_SIZE
  const visibleNativeCandidates = useMemo(
    () => sortedNativeCandidates.slice(
      nativeCandidatePageStart,
      nativeCandidatePageStart + NATIVE_CANDIDATE_PAGE_SIZE,
    ),
    [nativeCandidatePageStart, sortedNativeCandidates],
  )
  const nativeReviewCandidate = useMemo(
    () => nativeCandidates.find(candidate => candidate.candidate_id === nativeReviewCandidateId) || null,
    [nativeCandidates, nativeReviewCandidateId],
  )
  const nativeSourceCandidate = useMemo(
    () => nativeCandidates.find(candidate => candidate.candidate_id === nativeSourceCandidateId) || null,
    [nativeCandidates, nativeSourceCandidateId],
  )
  const destinationFolderOptions = useMemo(
    () => brunoDestinationFolderOptions(folders),
    [folders],
  )
  const selectedDestinationFolderLabel = destinationFolderOptions
    .find(option => option.folder.id === destinationFolderId)?.label || ''
  const selectedNativeCandidates = useMemo(
    () => nativeCandidates.filter(candidate => selectedCandidateIdSet.has(candidate.candidate_id)),
    [nativeCandidates, selectedCandidateIdSet],
  )
  const requirements = useMemo(() => {
    const selectedRequirements = selectedNativeCandidates
      .flatMap(candidate => candidate.secret_requirements || [])
    const all = outputMode === 'native_requests' && nativeCandidates.length > 0
      ? selectedRequirements
      : inspection?.result.secret_requirements || []
    return aggregateBrunoSecretRequirements(all)
  }, [inspection, nativeCandidates.length, outputMode, selectedNativeCandidates])
  const credentialRequirements = useMemo<SecretBindingRequirement[]>(() => requirements.map(requirement => {
    const presentation = brunoSecretPresentation(requirement)
    return {
      id: requirement.id,
      path: requirement.path,
      label: presentation.label,
      context: presentation.cardContext,
      suggested_name: requirement.suggested_name,
      replacement: requirement.replacement,
      source_variable: requirement.source_variable,
      unresolved_source_variable: requirement.unresolved_source_variable,
      affected_paths: requirement.affected_paths,
      source_value_available: requirement.source_value_available,
      destination_value_preserved: requirement.destination_value_preserved,
      preservation_unavailable_reason: requirement.preservation_unavailable_reason,
      preservation_unavailable_description: presentation.preservationUnavailableDescription,
    }
  }), [requirements])
  const sourceMigrationAvailable = Boolean(
    support?.secret_store?.source_secret_migration_policy_enabled
    && support.secret_store.writable
    && requirements.some(requirement => requirement.source_value_available),
  )
  const conflicts = inspection?.sync_preview?.conflicts || []
  const unresolvedConflicts = conflicts.filter(conflict => conflictResolutions[conflict.id] !== conflict.required_resolution)
  const selectedBytes = useMemo(() => files.reduce((total, file) => total + file.size, 0), [files])
  const importDisabled = support?.import_enabled === false
  // Bruno import publishes encrypted source artifacts during inspection, so it
  // cannot run without an envelope key — and the server masks that failure as a
  // generic error. An unconfigured controller now provisions a local key for
  // itself, leaving only a production posture unable to retain source. Report
  // that before the source bundle, destination folder, and profile have all
  // been filled in for nothing.
  const sourceRetentionUnavailable = support?.retention?.available === false
  const sourceRetentionMessage = typeof support?.retention?.unavailable_reason === 'string'
    ? support.retention.unavailable_reason
    : 'Bruno import requires protected source retention, which needs an envelope key. Ask an operator to configure the wrapping master key and restart the controller.'
  // Key custody is an operator advisory, not a blocker: import works, and the
  // note says who holds the key rather than letting the UI imply managed custody.
  const sourceRetentionAdvisory = !sourceRetentionUnavailable && typeof support?.retention?.advisory === 'string'
    ? support.retention.advisory
    : ''
  const canInspect = Boolean(projectId && files.length > 0 && !loading && !creatingFolder && !modeDetectionPending && !importDisabled && !sourceRetentionUnavailable)
  const selectionMissing = outputMode === 'canonical'
    && (runOptions.selection === 'folder' || runOptions.selection === 'request')
    && splitList(runOptions.paths).length === 0
  const allNativeCandidatesSelected = nativeCandidates.length > 0
    && selectedNativeCandidates.length === nativeCandidates.length
  const someNativeCandidatesSelected = selectedNativeCandidates.length > 0 && !allNativeCandidatesSelected
  const nativeIdentityError = useMemo(
    () => outputMode === 'native_requests'
      ? nativeCandidateIdentityError(nativeCandidates)
      : '',
    [nativeCandidates, outputMode],
  )
  const nativeReviewFindings = useMemo(() => Array.from(new Map(selectedNativeCandidates.flatMap(candidate => (
    candidate.diagnostics || []
  ).flatMap(diagnostic => {
    const id = nativeFindingID(diagnostic)
    if (!diagnostic.acknowledgement_required || !id) return []
    return [[id, {
      id,
      code: diagnostic.code || '',
      sourcePath: candidate.source_path || candidate.script.name,
      message: diagnostic.message || diagnostic.code || 'Imported behavior requires review.',
    }] as const]
  }))).values()).sort((left, right) => left.id.localeCompare(right.id)), [selectedNativeCandidates])
  const nativeReviewFindingIds = useMemo(
    () => nativeReviewFindings.map(finding => finding.id),
    [nativeReviewFindings],
  )
  const acknowledgedFindingIdSet = useMemo(
    () => new Set(acknowledgedFindingIds),
    [acknowledgedFindingIds],
  )
  const unacknowledgedReviewFindings = useMemo(
    () => nativeReviewFindings.filter(finding => !acknowledgedFindingIdSet.has(finding.id)),
    [acknowledgedFindingIdSet, nativeReviewFindings],
  )
  const unacknowledgedFindingIds = useMemo(
    () => unacknowledgedReviewFindings.map(finding => finding.id),
    [unacknowledgedReviewFindings],
  )
  const nativeFindingPageCount = Math.max(
    1,
    Math.ceil(nativeReviewFindings.length / NATIVE_FINDING_PAGE_SIZE),
  )
  const safeNativeFindingPage = Math.min(nativeFindingPage, nativeFindingPageCount - 1)
  const nativeFindingPageStart = safeNativeFindingPage * NATIVE_FINDING_PAGE_SIZE
  const visibleNativeReviewFindings = useMemo(
    () => nativeReviewFindings.slice(
      nativeFindingPageStart,
      nativeFindingPageStart + NATIVE_FINDING_PAGE_SIZE,
    ),
    [nativeFindingPageStart, nativeReviewFindings],
  )
  const selectedNativeMissingVariables = Array.from(new Set(
    selectedNativeCandidates.flatMap(candidate => candidate.missing_variables || []),
  )).sort()
  const selectedNativeNames = selectedNativeCandidates.map(candidate => (nativeNameOverrides[candidate.candidate_id] ?? candidate.script.name).trim())
  const duplicateNativeNames = new Set(selectedNativeNames.map(value => value.toLowerCase())).size !== selectedNativeNames.length
  const credentialsValid = requirements.length === 0 || preparedSecrets?.valid === true
  const deferredSecretCount = preparedSecrets?.configureLaterCount ?? 0
  const credentialsReadyForExecution = credentialsValid && deferredSecretCount === 0
  const protectedSessionExpiry = inspection?.expires_at
    ? Date.parse(inspection.expires_at)
    : Number.NaN
  const protectedSessionExpired = Number.isFinite(protectedSessionExpiry)
    && protectedSessionExpiry <= protectedSessionNow
  const protectedSessionLabel = Number.isFinite(protectedSessionExpiry)
    ? protectedSessionExpired
      ? 'This Bruno import session has expired. Re-inspect the source before saving.'
      : `Bruno import session available until ${new Date(protectedSessionExpiry).toLocaleString()}.`
    : ''
  const saveBlockers: string[] = []
  if (protectedSessionExpired) {
    saveBlockers.push('This import session expired. Re-inspect the source to create a fresh review session.')
  }
  if (loading) saveBlockers.push('Wait for the current import operation to finish.')
  if (!destinationFolderManagedExternally && folderLoading) saveBlockers.push('Wait for destination folders to finish loading before saving.')
  if (!destinationFolderManagedExternally && folderLoadError) saveBlockers.push(`Destination folders could not be loaded: ${folderLoadError} Retry loading folders before saving.`)
  if (creatingFolder) saveBlockers.push('Wait for destination folder creation to finish. The current destination cannot be used while its replacement is pending.')
  if (parameterizationApplying) saveBlockers.push('Wait for the selected parameterization changes to finish applying.')
  if (requirements.length > 0 && preparedSecrets === null) {
    saveBlockers.push('Wait for the credential review to finish preparing the selected credential choices.')
  } else if (!credentialsValid) {
    saveBlockers.push('Correct the invalid credential destination choices or secret reference names in Credentials.')
  }
  if (selectionMissing) saveBlockers.push('Enter at least one source path for the selected folder or request import scope.')
  if (outputMode === 'native_requests') {
    if (!destinationFolderId) {
      if (destinationFolderManagedExternally) {
        saveBlockers.push('Select an existing script folder in Save destination; native batches cannot be saved to Project root.')
      } else if (!folderLoading && !folderLoadError && destinationFolderOptions.length > 0) {
        saveBlockers.push('Select an existing destination folder in Import and execution profile.')
      } else if (!folderLoading && !folderLoadError && folderCreationAccess === 'allowed') {
        saveBlockers.push('Create and select a destination folder in Import and execution profile.')
      } else if (!folderLoading && !folderLoadError && folderCreationAccess === 'denied') {
        saveBlockers.push('No destination folder is available, and your current role cannot create project folders. Ask a Project Administrator to create a script folder, then reload this page and select it.')
      } else if (!folderLoading && !folderLoadError && folderCreationAccess === 'checking') {
        saveBlockers.push('No destination folder is available. Waiting for the folder-creation permission check to finish.')
      } else if (!folderLoading && !folderLoadError) {
        saveBlockers.push('No destination folder is available, and folder-creation permission could not be verified. Refresh the page or ask a Project Administrator to create a script folder.')
      }
    }
    if (selectedNativeCandidates.length === 0) saveBlockers.push('Select at least one API request to import.')
    if (selectedNativeNames.some(name => name === '')) saveBlockers.push('Enter a non-empty script name for every selected API request.')
    if (duplicateNativeNames) saveBlockers.push('Give every selected API request a unique script name.')
    for (const finding of unacknowledgedReviewFindings.slice(0, MAX_DETAILED_REVIEW_FINDING_BLOCKERS)) {
      saveBlockers.push(`Review ${finding.sourcePath}: ${finding.message} Acknowledge this finding under Review-gated conversions.`)
    }
    if (unacknowledgedReviewFindings.length > MAX_DETAILED_REVIEW_FINDING_BLOCKERS) {
      saveBlockers.push(`${unacknowledgedReviewFindings.length - MAX_DETAILED_REVIEW_FINDING_BLOCKERS} additional review-gated conversion findings require acknowledgement. Use the paginated Review-gated conversions section for each finding's context and resolution.`)
    }
    if (nativeIdentityError) saveBlockers.push(nativeIdentityError)
  } else {
    if (unresolvedConflicts.length > 0) saveBlockers.push(`Resolve ${unresolvedConflicts.length} synchronization conflict${unresolvedConflicts.length === 1 ? '' : 's'}.`)
    if (syncScriptId && !inspection?.sync_preview) saveBlockers.push('Re-inspect the source to prepare the selected synchronization target.')
  }
  const canCommit = Boolean(inspection) && saveBlockers.length === 0
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
    // Candidate IDs are derived from one specific inspection. Carrying a
    // selection into a new session would send IDs the server cannot re-derive.
    setSharedValueSelections({})
  }, [inspection?.import_session_id])

  useEffect(() => {
    setProtectedSessionNow(Date.now())
    if (!Number.isFinite(protectedSessionExpiry)) return
    const remaining = protectedSessionExpiry - Date.now()
    if (remaining <= 0) return
    const timeout = window.setTimeout(
      () => setProtectedSessionNow(Date.now()),
      Math.min(remaining, 2_147_483_647),
    )
    return () => window.clearTimeout(timeout)
  }, [inspection?.import_session_id, protectedSessionExpiry])

  const selectFiles = (next: File[]) => {
    advanceSourceGeneration()
    const modeDetectionRequest = modeDetectionRequestRef.current + 1
    modeDetectionRequestRef.current = modeDetectionRequest
    const shouldDetectMode = needsBrunoContentModeDetection(next)
    updateModeDetectionPending(shouldDetectMode)
    setFiles(next)
    const immediateMode: OutputMode = isOpenCollectionYAML(next) ? 'native_requests' : 'canonical'
    setOutputMode(immediateMode)
    setRecommendedOutputMode(isOpenCollectionYAML(next) ? 'native_requests' : null)
    setNewFolderName(suggestedBrunoFolderName(next))
    setFolderCreateError('')
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
        const detectedMode = isNative ? 'native_requests' : 'canonical'
        setRecommendedOutputMode(detectedMode)
        setOutputMode(detectedMode)
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

  const createDestinationFolder = async () => {
    const folderName = newFolderName.trim()
    const sourceProjectId = sourceProjectIdRef.current
    if (!sourceProjectId || !folderName || creatingFolder || folderCreationAccess !== 'allowed') return
    const requestID = folderCreateRequestRef.current + 1
    folderCreateRequestRef.current = requestID
    setCreatingFolder(true)
    setFolderCreateError('')
    try {
      const created = await api.createFolder(sourceProjectId, {
        name: folderName,
        category: 'scripts',
      })
      if (!mountedRef.current || sourceProjectIdRef.current !== sourceProjectId || folderCreateRequestRef.current !== requestID) return
      setFolders(previous => previous.some(folder => folder.id === created.id)
        ? previous
        : [...previous, created])
      selectDestinationFolder(created.id)
      setNotice(`Created and selected script folder “${created.name}”.`)
    } catch (cause) {
      if (mountedRef.current && sourceProjectIdRef.current === sourceProjectId && folderCreateRequestRef.current === requestID) {
        if (cause instanceof ApiError && cause.status === 403) {
          setFolderCreationAccess('denied')
          setFolderCreateError(FOLDER_CREATION_DENIED_MESSAGE)
        } else {
          setFolderCreateError(getErrorMessage(cause, 'Unable to create the destination folder.'))
        }
      }
    } finally {
      if (mountedRef.current && sourceProjectIdRef.current === sourceProjectId && folderCreateRequestRef.current === requestID) {
        setCreatingFolder(false)
      }
    }
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
      // The protected Bruno session already contains the operator-authored
      // runtime values. A suggested logical name is review information, not an
      // implicit instruction to replace that value with an unconfigured secret
      // reference. Only the credential review's explicit selections populate
      // this map before commit.
      secretBindingsRef.current = {}
      setConflictResolutions({})
      const candidates = response.result.output_mode === 'native_requests' ? response.result.scripts : []
      setSelectedCandidateIds(candidates.map(candidate => candidate.candidate_id))
      // Keep overrides sparse. Initial names already live on each candidate;
      // copying all of them would make every edit clone an object as large as
      // the imported collection.
      setNativeNameOverrides({})
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
          ...(Object.keys(sharedValueSelections).length > 0
            ? { shared_values: sharedValueSelections }
            : {}),
        })
        : await api.commitBrunoImport({
          project_id: projectId,
          import_session_id: inspection.import_session_id,
          mode: syncScriptId ? 'sync' : 'create',
          folder_id: destinationFolderId || null,
          sync_script_id: syncScriptId || undefined,
          name: name.trim() || undefined,
          expected_updated_at: inspection.sync_preview?.expected_updated_at,
          expected_active_revision_id: inspection.sync_preview?.active_revision,
          migrate_secrets: migrateSecrets,
          parameterization_selected_ids: parameterizationSelectedIds,
          secret_bindings: secretBindingsRef.current,
          conflict_resolutions: conflictResolutions,
        })
      const importedScripts = outputMode === 'native_requests'
        ? validatedNativeCommitScripts(response, selectedNativeCandidates)
        : null
      if (outputMode === 'canonical' && !isCanonicalCommitResponse(response)) {
        throw new Error('Canonical Bruno commit response is missing its script and revision.')
      }
      if (!requestIsCurrent()) {
        // The commit endpoint is the durable boundary. Back/project changes
        // must prevent navigation, but cannot make the confirmed batch or
        // canonical script disappear from the library until a manual refresh.
        onImported?.({
          scripts: importedScripts || (isCanonicalCommitResponse(response)
            ? [storedScriptFromDefinition(response.script)]
            : []),
          stayInImportReview: true,
        })
        return
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

  const rollback = async (revision: ImportSourceRevision) => {
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

  const handleProjectSecretsConfigured = useCallback((names: readonly string[]) => {
    if (names.length > 0) setConfiguredCredentialReviewIdentity(credentialReviewIdentity)
  }, [credentialReviewIdentity])

  const applyParameterization = (parameterizedScript: ScriptDefinition, _diffs: unknown[], selectedIds: string[]) => {
    const importSessionId = inspection?.import_session_id
    if (!importSessionId) return
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
  const isNativeInspectionMode = outputMode === 'native_requests'
  const sortNativeCandidates = (key: NativeCandidateSortKey) => {
    setNativeCandidatePage(0)
    setNativeCandidateSort(previous => previous.key === key
      ? { key, direction: previous.direction === 'asc' ? 'desc' : 'asc' }
      : { key, direction: 'asc' })
  }
  const sortableHeader = (label: string, key: NativeCandidateSortKey) => {
    const active = nativeCandidateSort.key === key
    const indicator = active ? (nativeCandidateSort.direction === 'asc' ? ' ▲' : ' ▼') : ''
    return (
      <button
        type="button"
        className="bruno-sort-button"
        aria-label={`Sort by ${label}`}
        aria-pressed={active}
        onClick={() => sortNativeCandidates(key)}
      >
        {label}{indicator}
      </button>
    )
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

      {!isNativeInspectionMode && requirements.length > 0 && !committed && (
        <SecretBindingReview
          ref={credentialReviewRef}
          requirements={credentialRequirements}
          reviewGeneration={credentialReviewIdentity}
          disabled={loading || creatingFolder || parameterizationApplying || protectedSessionExpired}
          projectId={projectId}
          sourceSecretSessionExpiresAt={inspection?.expires_at}
          sourceMigrationAllowed={sourceMigrationAvailable}
          externalSourceMigrationSelected={migrateSecrets}
          onExternalSourceMigrationChange={setMigrateSecrets}
          onApply={applyCredentialBindings}
          onPreparedChange={setPreparedSecrets}
          onRequirementDestinationStateChange={setCredentialDestinationState}
          onProjectSecretsConfigured={handleProjectSecretsConfigured}
        />
      )}

      {!isNativeInspectionMode && projectSecretsConfiguredForReview && !committed && (
        <div className="bruno-preflight" role="status">
          Project-secret values configured during this review remain in Project Secrets independently of script saving.
          {' '}If script saving fails, retrying this save reuses those configured values. Any atomic-save promise on this page applies to scripts, not external secret-store writes.
        </div>
      )}

      {protectedSessionLabel && !committed && (
        <div
          className={`bruno-preflight ${protectedSessionExpired ? 'warning' : ''}`}
          role={protectedSessionExpired ? 'alert' : 'status'}
        >
          {protectedSessionLabel}
        </div>
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

      {error && (!inspection || committed) && <div className="settings-error" role="alert">{error}</div>}
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
              <span key={`${importFileLogicalPath(file)}-${index}`}>{importFileLogicalPath(file)}</span>
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
        {recommendedOutputMode && !modeDetectionPending && (
          <div className={`bruno-source-recommendation ${recommendedOutputMode === outputMode ? 'ready' : 'warning'}`} role="status">
            <div>
              <strong>Source recommendation: {recommendedOutputMode === 'native_requests' ? 'Native scripts per API' : 'Canonical Bruno collection'}</strong>
              <span>{recommendedOutputMode === 'native_requests'
                ? 'OpenCollection metadata was detected in the selected source. Native mode keeps each HTTP API independently editable and runnable.'
                : 'No OpenCollection marker was detected. Canonical mode is the safer starting point for classic Bruno runtime behavior.'}</span>
            </div>
            {recommendedOutputMode !== outputMode && (
              <button className="btn btn-secondary" disabled={loading} onClick={() => selectOutputMode(recommendedOutputMode)}>
                Use recommended mode
              </button>
            )}
          </div>
        )}
        {outputMode === 'canonical' ? (
          <>
        <div className="bruno-options-grid">
          <label>
            <span>Script name</span>
            <input disabled={loading} value={name} onChange={event => { advanceSourceGeneration(); setName(event.target.value); setInspection(null) }} placeholder="Use collection name" />
          </label>
          <label>
            <span>Synchronization target</span>
            <select disabled={loading} value={syncScriptId} onChange={event => selectSynchronizationTarget(event.target.value)}>
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
        ) : null}
        {!destinationFolderManagedExternally && (
          <>
            <div className="bruno-options-grid">
              <label>
                <span>Destination folder</span>
                <select
                  aria-describedby={outputMode === 'native_requests' && destinationFolderOptions.length === 0 ? 'bruno-folder-empty-guidance' : undefined}
                  aria-busy={folderLoading || undefined}
                  disabled={loading || folderLoading || Boolean(folderLoadError) || creatingFolder || (outputMode === 'native_requests' && destinationFolderOptions.length === 0)}
                  value={destinationFolderId}
                  onChange={event => selectDestinationFolder(event.target.value)}
                >
                  <option value="" disabled={outputMode === 'native_requests'}>
                    {outputMode === 'native_requests' ? 'Select a script folder…' : 'Project root'}
                  </option>
                  {destinationFolderOptions.map(({ folder, label }) => (
                    <option key={folder.id} value={folder.id}>{label}</option>
                  ))}
                </select>
              </label>
            </div>
            {folderLoading && (
              <div className="bruno-preflight" role="status">Loading destination folders…</div>
            )}
            {folderSelectionWarning && (
              <div className="bruno-preflight warning" role="alert">{folderSelectionWarning}</div>
            )}
            {outputMode === 'native_requests' && destinationFolderOptions.length === 0 && !folderLoading && !folderLoadError && (
              <div id="bruno-folder-empty-guidance" className="bruno-preflight warning" role="status">
                No script folders exist in this project. Native import creates one script per API and requires an existing destination folder before saving.
              </div>
            )}
            {folderCreationAccess === 'allowed' ? (
              <div className="bruno-folder-create">
                <label>
                  <span>New script folder name</span>
                  <input
                    value={newFolderName}
                    disabled={loading || creatingFolder || !projectId}
                    onChange={event => {
                      setNewFolderName(event.target.value)
                      setFolderCreateError('')
                    }}
                    placeholder="Imported Bruno APIs"
                  />
                </label>
                <button
                  className="btn btn-secondary"
                  disabled={loading || creatingFolder || !projectId || newFolderName.trim() === ''}
                  onClick={() => void createDestinationFolder()}
                >
                  {creatingFolder ? 'Creating folder…' : 'Create and select folder'}
                </button>
              </div>
            ) : outputMode === 'native_requests' && destinationFolderOptions.length === 0 && !folderCreateError ? (
              <div className="bruno-folder-permission" role="status">
                {folderCreationAccess === 'checking'
                  ? 'Checking whether this session can create project folders…'
                  : folderCreationAccess === 'denied'
                    ? FOLDER_CREATION_DENIED_MESSAGE
                    : 'Folder-creation permission could not be verified. Refresh the page, select an existing folder, or ask a Project Administrator to create one.'}
              </div>
            ) : null}
            {folderCreateError && <div className="bruno-preflight warning" role="alert">{folderCreateError}</div>}
            {folderLoadError && (
              <div className="bruno-preflight warning" role="alert">
                {folderLoadError}{' '}
                <button
                  className="btn btn-ghost"
                  disabled={loading || folderLoading}
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
        {sourceRetentionUnavailable && (
          <div className="bruno-preflight warning" role="alert">
            {sourceRetentionMessage}
          </div>
        )}
        {sourceRetentionAdvisory && (
          <div className="bruno-preflight" role="status">
            {sourceRetentionAdvisory}
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
          {outputMode === 'native_requests' ? (
            <>
              <div className="bruno-tabs" role="tablist" aria-label="Bruno native inspection panels">
                {BRUNO_INSPECTION_TABS.map((tab, index) => (
                  <button
                    key={tab.id}
                    type="button"
                    id={`bruno-inspection-tab-${tab.id}`}
                    role="tab"
                    className="bruno-tab"
                    aria-selected={activeInspectionTab === tab.id}
                    aria-controls={`bruno-inspection-panel-${tab.id}`}
                    tabIndex={activeInspectionTab === tab.id ? 0 : -1}
                    onClick={() => setActiveInspectionTab(tab.id)}
                    onKeyDown={event => {
                      let nextIndex: number
                      switch (event.key) {
                        case 'ArrowRight':
                          nextIndex = (index + 1) % BRUNO_INSPECTION_TABS.length
                          break
                        case 'ArrowLeft':
                          nextIndex = (index - 1 + BRUNO_INSPECTION_TABS.length) % BRUNO_INSPECTION_TABS.length
                          break
                        case 'Home':
                          nextIndex = 0
                          break
                        case 'End':
                          nextIndex = BRUNO_INSPECTION_TABS.length - 1
                          break
                        default:
                          return
                      }
                      event.preventDefault()
                      const nextTab = BRUNO_INSPECTION_TABS[nextIndex]
                      setActiveInspectionTab(nextTab.id)
                      document.getElementById(`bruno-inspection-tab-${nextTab.id}`)?.focus()
                    }}
                  >
                    {tab.label}
                  </button>
                ))}
              </div>

              <section
                  className="bruno-tab-panel"
                  role="tabpanel"
                  id="bruno-inspection-panel-summary"
                  aria-labelledby="bruno-inspection-tab-summary"
                  hidden={activeInspectionTab !== 'summary'}
                >
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
                  <section className="bruno-card bruno-validation-guidance" aria-labelledby="bruno-validation-guidance-title">
                    <div className="bruno-card-heading">
                      <div>
                        <h3 id="bruno-validation-guidance-title">Recommended configuration</h3>
                        <p>Based on the validated source, complete these choices in order.</p>
                      </div>
                    </div>
                    <ol>
                      <li className={selectedNativeCandidates.length > 0 ? 'ready' : 'warning'}>
                        <strong>Script selection</strong>
                        <span>Keep all {nativeCandidates.length} APIs selected unless you intentionally want a partial import.</span>
                      </li>
                      <li className={destinationFolderId ? 'ready' : 'warning'}>
                        <strong>Destination</strong>
                        <span>{destinationFolderId
                          ? `Save the batch in ${selectedDestinationFolderLabel || 'the selected script folder'}.`
                          : 'Create or select a script folder; native batches cannot be saved to the project root.'}</span>
                      </li>
                      <li className={credentialsReadyForExecution ? 'ready' : 'warning'}>
                        <strong>Credentials</strong>
                        <span>{requirements.length === 0
                          ? 'No credential bindings are required.'
                          : !credentialsValid
                            ? `Review ${requirements.length} detected credential requirement${requirements.length === 1 ? '' : 's'}. Keep preserved source values for exact replay, or explicitly bind them to project secrets.`
                            : deferredSecretCount > 0
                              ? `Credential review is complete, but ${deferredSecretCount} project secret${deferredSecretCount === 1 ? '' : 's'} must still be configured before execution. Saving now is allowed.`
                              : `Credential handling is complete for ${requirements.length} detected requirement${requirements.length === 1 ? '' : 's'}.`}</span>
                      </li>
                      <li className={selectedNativeMissingVariables.length === 0 ? 'ready' : 'warning'}>
                        <strong>Runtime variables</strong>
                        <span>{selectedNativeMissingVariables.length === 0
                          ? 'All referenced variables resolve from the imported source.'
                          : `Configure ${selectedNativeMissingVariables.length} unresolved variable${selectedNativeMissingVariables.length === 1 ? '' : 's'} before execution. Saving is allowed, but replay remains blocked.`}</span>
                      </li>
                      <li className={unacknowledgedFindingIds.length === 0 ? 'ready' : 'warning'}>
                        <strong>Review findings</strong>
                        <span>{unacknowledgedFindingIds.length === 0
                          ? 'No outstanding acknowledgement is required.'
                          : `Read and acknowledge ${unacknowledgedFindingIds.length} finding${unacknowledgedFindingIds.length === 1 ? '' : 's'} before saving.`}</span>
                      </li>
                    </ol>
                  </section>

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
              </section>

              <section
                  className="bruno-tab-panel"
                  role="tabpanel"
                  id="bruno-inspection-panel-source_apis"
                  aria-labelledby="bruno-inspection-tab-source_apis"
                  hidden={activeInspectionTab !== 'source_apis'}
                >
                  <article className="bruno-card bruno-native-review">
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
                          onChange={event => {
                            setCredentialDestinationState({})
                            setNativeFindingPage(0)
                            setSelectedCandidateIds(event.target.checked
                              ? nativeCandidates.map(candidate => candidate.candidate_id)
                              : [])
                          }}
                        />
                        Select all
                      </label>
                    </div>
                    <div className="bruno-native-table-wrap">
                      <table className="bruno-native-table">
                        <thead>
                          <tr>
                            <th aria-label="Selected" />
                            <th aria-sort={nativeCandidateSort.key === 'source' ? (nativeCandidateSort.direction === 'asc' ? 'ascending' : 'descending') : 'none'}>{sortableHeader('Source', 'source')}</th>
                            <th aria-sort={nativeCandidateSort.key === 'script' ? (nativeCandidateSort.direction === 'asc' ? 'ascending' : 'descending') : 'none'}>{sortableHeader('Script name', 'script')}</th>
                            <th aria-sort={nativeCandidateSort.key === 'request' ? (nativeCandidateSort.direction === 'asc' ? 'ascending' : 'descending') : 'none'}>{sortableHeader('Request', 'request')}</th>
                            <th aria-sort={nativeCandidateSort.key === 'status' ? (nativeCandidateSort.direction === 'asc' ? 'ascending' : 'descending') : 'none'}>{sortableHeader('Status', 'status')}</th>
                            <th aria-sort={nativeCandidateSort.key === 'details' ? (nativeCandidateSort.direction === 'asc' ? 'ascending' : 'descending') : 'none'}>{sortableHeader('Details', 'details')}</th>
                          </tr>
                        </thead>
                        <tbody>
                          {visibleNativeCandidates.map(candidate => {
                            const selected = selectedCandidateIdSet.has(candidate.candidate_id)
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
                                    onChange={event => {
                                      setCredentialDestinationState({})
                                      setNativeFindingPage(0)
                                      setSelectedCandidateIds(previous => event.target.checked
                                        ? [...previous, candidate.candidate_id]
                                        : previous.filter(id => id !== candidate.candidate_id))
                                    }}
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
                                <td>
                                  <button
                                    type="button"
                                    className="btn btn-ghost bruno-native-inspect-button"
                                    aria-haspopup="dialog"
                                    aria-label={`Inspect source API ${candidate.source_path}`}
                                    onClick={() => setNativeSourceCandidateId(candidate.candidate_id)}
                                  >
                                    Inspect
                                  </button>
                                </td>
                                <td><span className={`bruno-native-status ${status}`}>{statusLabel}</span></td>
                                <td>
                                  <button
                                    type="button"
                                    className="btn btn-ghost bruno-native-review-button"
                                    aria-haspopup="dialog"
                                    aria-label={selected
                                      ? `Review ${candidate.source_path}`
                                      : `Preview ${candidate.source_path}; not selected for import`}
                                    onClick={() => setNativeReviewCandidateId(candidate.candidate_id)}
                                  >
                                    Review
                                  </button>
                                </td>
                              </tr>
                            )
                          })}
                        </tbody>
                      </table>
                    </div>
                    {nativeCandidatePageCount > 1 && (
                      <nav className="bruno-native-table-pagination" aria-label="Native scripts table pages">
                        <span aria-live="polite">
                          Showing {nativeCandidatePageStart + 1}–{Math.min(
                            nativeCandidates.length,
                            nativeCandidatePageStart + visibleNativeCandidates.length,
                          )} of {nativeCandidates.length} scripts. Page {safeNativeCandidatePage + 1} of {nativeCandidatePageCount}.
                        </span>
                        <div role="group" aria-label="Native scripts page controls">
                          <button
                            type="button"
                            className="btn btn-secondary btn-sm"
                            disabled={safeNativeCandidatePage === 0}
                            onClick={() => setNativeCandidatePage(Math.max(0, safeNativeCandidatePage - 1))}
                          >
                            Previous {NATIVE_CANDIDATE_PAGE_SIZE}
                          </button>
                          <button
                            type="button"
                            className="btn btn-secondary btn-sm"
                            disabled={safeNativeCandidatePage >= nativeCandidatePageCount - 1}
                            onClick={() => setNativeCandidatePage(Math.min(nativeCandidatePageCount - 1, safeNativeCandidatePage + 1))}
                          >
                            Next {NATIVE_CANDIDATE_PAGE_SIZE}
                          </button>
                        </div>
                      </nav>
                    )}
                  </article>
              </section>

              <section
                  className="bruno-tab-panel"
                  role="tabpanel"
                  id="bruno-inspection-panel-credentials"
                  aria-labelledby="bruno-inspection-tab-credentials"
                  hidden={activeInspectionTab !== 'credentials'}
                >
                  {!committed && (
                    <>
                      <h3>Credentials</h3>
                      <SecretBindingReview
                        ref={credentialReviewRef}
                        requirements={credentialRequirements}
                        reviewGeneration={credentialReviewIdentity}
                        disabled={loading || creatingFolder || parameterizationApplying || protectedSessionExpired}
                        projectId={projectId}
                        sourceSecretSessionExpiresAt={inspection?.expires_at}
                        sourceMigrationAllowed={sourceMigrationAvailable}
                        externalSourceMigrationSelected={migrateSecrets}
                        onExternalSourceMigrationChange={setMigrateSecrets}
                        onApply={applyCredentialBindings}
                        onPreparedChange={setPreparedSecrets}
                        onRequirementDestinationStateChange={setCredentialDestinationState}
                        onProjectSecretsConfigured={handleProjectSecretsConfigured}
                      />
                      {projectSecretsConfiguredForReview && (
                        <div className="bruno-preflight" role="status">
                          Project-secret values configured during this review remain in Project Secrets independently of script saving.
                          {' '}If script saving fails, retrying this save reuses those configured values. Any atomic-save promise on this page applies to scripts, not external secret-store writes.
                        </div>
                      )}
                    </>
                  )}
              </section>

              <section
                  className="bruno-tab-panel"
                  role="tabpanel"
                  id="bruno-inspection-panel-data"
                  aria-labelledby="bruno-inspection-tab-data"
                  hidden={activeInspectionTab !== 'data'}
                >
                  {inspection.shared_values && (inspection.shared_values.candidates?.length ?? 0) > 0 && !committed && (
                    <section className="bruno-card">
                      <h3>Collection values</h3>
                      <SharedImportValues
                        sharedValues={inspection.shared_values}
                        collectOnly
                        scriptCount={selectedNativeCandidates.length}
                        onValuesChange={setSharedValueSelections}
                        reviewGeneration={`${sourceGeneration}:${projectId || ''}`}
                        disabled={loading || protectedSessionExpired}
                      />
                    </section>
                  )}
                  {(inspection.result.missing_variables || []).length > 0 && (
                    <section className="bruno-card">
                      <h3>Preflight requirements</h3>
                      <p>These values were referenced but not defined in the uploaded source. Supply them above under Collection Values, through Bruno environments, or through APEX runtime bindings before execution.</p>
                      <div className="bruno-capability-list">
                        {(inspection.result.missing_variables || []).map(variable => <span key={variable}>{variable}</span>)}
                      </div>
                    </section>
                  )}
              </section>

              <section
                  className="bruno-tab-panel"
                  role="tabpanel"
                  id="bruno-inspection-panel-import"
                  aria-labelledby="bruno-inspection-tab-import"
                  hidden={activeInspectionTab !== 'import'}
                >
                  {nativeReviewFindingIds.length > 0 && (
                    <section className="bruno-card bruno-native-acknowledgements">
                      <h4>Review-gated conversions</h4>
                      <p>Each item below is a separate finding in an affected script. To save these incomplete drafts, acknowledge that you understand each limitation. Acknowledgement records awareness only; it does not approve replay or remove the safety gate.</p>
                      <div className="bruno-native-acknowledgement-list">
                        {visibleNativeReviewFindings.map((finding, pageIndex) => {
                          const index = nativeFindingPageStart + pageIndex
                          const guidance = nativeReviewGuidance(finding.code)
                          const detailsId = `bruno-review-finding-${index + 1}`
                          return (
                            <article key={finding.id} className="bruno-native-acknowledgement-card">
                              <div className="bruno-native-acknowledgement-heading">
                                <strong>{guidance.title}</strong>
                                <span>{finding.sourcePath}</span>
                              </div>
                              <dl id={detailsId}>
                                <div>
                                  <dt>What was found</dt>
                                  <dd>{finding.message}</dd>
                                </div>
                                <div>
                                  <dt>Why replay is blocked</dt>
                                  <dd>{guidance.impact}</dd>
                                </div>
                                <div>
                                  <dt>Required before replay</dt>
                                  <dd>{guidance.resolution}</dd>
                                </div>
                              </dl>
                              <label>
                                <input
                                  type="checkbox"
                                  disabled={loading}
                                  checked={acknowledgedFindingIdSet.has(finding.id)}
                                  aria-describedby={detailsId}
                                  aria-label={`Acknowledge ${guidance.title} for ${finding.sourcePath}`}
                                  onChange={event => setAcknowledgedFindingIds(previous => event.target.checked
                                    ? [...previous, finding.id]
                                    : previous.filter(id => id !== finding.id))}
                                />
                                I understand this draft remains blocked from replay until this finding is resolved.
                              </label>
                            </article>
                          )
                        })}
                      </div>
                      {nativeFindingPageCount > 1 && (
                        <nav className="bruno-native-table-pagination" aria-label="Review-gated conversion pages">
                          <span aria-live="polite">
                            Showing findings {nativeFindingPageStart + 1}–{nativeFindingPageStart + visibleNativeReviewFindings.length} of {nativeReviewFindings.length}. Page {safeNativeFindingPage + 1} of {nativeFindingPageCount}.
                          </span>
                          <div role="group" aria-label="Review finding page controls">
                            <button
                              type="button"
                              className="btn btn-secondary btn-sm"
                              disabled={safeNativeFindingPage === 0}
                              onClick={() => setNativeFindingPage(Math.max(0, safeNativeFindingPage - 1))}
                            >
                              Previous {NATIVE_FINDING_PAGE_SIZE}
                            </button>
                            <button
                              type="button"
                              className="btn btn-secondary btn-sm"
                              disabled={safeNativeFindingPage >= nativeFindingPageCount - 1}
                              onClick={() => setNativeFindingPage(Math.min(nativeFindingPageCount - 1, safeNativeFindingPage + 1))}
                            >
                              Next {NATIVE_FINDING_PAGE_SIZE}
                            </button>
                          </div>
                        </nav>
                      )}
                    </section>
                  )}

                  {!committed && (
                    <section className="bruno-commit-bar">
                      <div>
                        <strong>{`Save ${selectedNativeCandidates.length} native script${selectedNativeCandidates.length === 1 ? '' : 's'}`}</strong>
                        <span>{destinationFolderId
                          ? 'All selected scripts will be saved atomically to the selected folder.'
                          : 'Select a destination folder before saving.'}</span>
                        {saveBlockers.length > 0 && (
                          <div id="bruno-save-blockers" className="bruno-save-blockers" role="status">
                            <strong>Save is unavailable for these reasons:</strong>
                            <ul>{saveBlockers.map(blocker => <li key={blocker}>{blocker}</li>)}</ul>
                            {deferredSecretCount > 0 && <p>{deferredSecretCount} project secret{deferredSecretCount === 1 ? ' is' : 's are'} still unconfigured, but that does not prevent saving.</p>}
                            {selectedNativeMissingVariables.length > 0 && <p>{selectedNativeMissingVariables.length} unresolved runtime variable{selectedNativeMissingVariables.length === 1 ? ' does' : 's do'} not prevent saving, but must be configured before execution.</p>}
                          </div>
                        )}
                        {error && (
                          <div className="settings-error" role="alert">
                            Save failed: {error}
                          </div>
                        )}
                      </div>
                      <button
                        className="btn btn-primary"
                        disabled={!canCommit}
                        aria-describedby={!canCommit && saveBlockers.length > 0 ? 'bruno-save-blockers' : undefined}
                        onClick={() => void commit()}
                      >
                        {commitButtonLabel}
                      </button>
                    </section>
                  )}
              </section>
            </>
          ) : (
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
                  <p>These values were referenced but not defined in the uploaded source. Supply them above under Collection Values, through Bruno environments, or through APEX runtime bindings before execution.</p>
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
                    <strong>{syncScriptId ? 'Create synchronized revision' : 'Commit canonical Bruno script'}</strong>
                    <span>Raw and sanitized source artifacts are immutable, encrypted, and content-addressed.</span>
                    {saveBlockers.length > 0 && (
                      <div id="bruno-save-blockers" className="bruno-save-blockers" role="status">
                        <strong>Save is unavailable for these reasons:</strong>
                        <ul>{saveBlockers.map(blocker => <li key={blocker}>{blocker}</li>)}</ul>
                        {deferredSecretCount > 0 && <p>{deferredSecretCount} project secret{deferredSecretCount === 1 ? ' is' : 's are'} still unconfigured, but that does not prevent saving.</p>}
                        {selectedNativeMissingVariables.length > 0 && <p>{selectedNativeMissingVariables.length} unresolved runtime variable{selectedNativeMissingVariables.length === 1 ? ' does' : 's do'} not prevent saving, but must be configured before execution.</p>}
                      </div>
                    )}
                    {error && (
                      <div className="settings-error" role="alert">
                        Save failed: {error}
                      </div>
                    )}
                  </div>
                  <button
                    className="btn btn-primary"
                    disabled={!canCommit}
                    aria-describedby={!canCommit && saveBlockers.length > 0 ? 'bruno-save-blockers' : undefined}
                    onClick={() => void commit()}
                  >
                    {commitButtonLabel}
                  </button>
                </section>
              )}
            </>
          )}

          {nativeReviewCandidate && (
            <BrunoNativeReviewDialog
              candidate={nativeReviewCandidate}
              candidateSelected={selectedCandidateIdSet.has(nativeReviewCandidate.candidate_id)}
              requirementDestinationState={credentialDestinationState}
              onClose={() => setNativeReviewCandidateId(null)}
            />
          )}
          {nativeSourceCandidate && (
            <BrunoNativeSourceDialog
              candidate={nativeSourceCandidate}
              importSessionId={inspection.import_session_id || ''}
              projectId={projectId || ''}
              loadSource={api.getBrunoImportSessionSource}
              onClose={() => setNativeSourceCandidateId(null)}
            />
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
            <DesktopApiResourceLink className="btn btn-secondary" href={`/api/v1/scripts/${committed.script.id}/imports/source`} filename={`${committed.script.name || committed.script.id}-sanitized-source.zip`} onError={setError}>Download sanitized source</DesktopApiResourceLink>
            <DesktopApiResourceLink className="btn btn-secondary" href={`/api/v1/scripts/${committed.script.id}/imports/source?raw=true`} filename={`${committed.script.name || committed.script.id}-raw-source.zip`} onError={setError}>Download restricted raw source</DesktopApiResourceLink>
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
  return (
    <SourceCodeViewer
      ariaLabel="Masked APEX Load DSL script JSON"
      className="bruno-native-review-code-viewer"
      semanticMode="apex-dsl"
      sourcePath="apex-load-script.json"
      value={JSON.stringify(script, null, 2)}
    />
  )
}
