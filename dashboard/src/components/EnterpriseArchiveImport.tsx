import ImportReviewSectionHeader from './ImportReviewSectionHeader'
import { importSourceName } from '../appTopbar'
import { useNavigationGuardRunner, useUnsavedWorkGuard } from '../hooks/NavigationGuardProvider'
import ImportRuntimeSettingsTable from './ImportRuntimeSettingsTable'
import { ImportConversionDetails, ImportEvidenceFacts } from './ImportConversionDetails'
import { ImportGuidedRepair, type RepairResult } from './ImportGuidedRepair'
import ImportSaveConfirmation from './ImportSaveConfirmation'
import { type ImportSaveDestination } from './useImportSaveOutcome'
import TabList from './ux/TabList'
import { handleTabKey } from './tabKeyboard'
import DialogLayer from './ux/DialogLayer'
import { useCallback, useEffect, useMemo, useState, useRef, type ReactNode, type CSSProperties } from 'react'
import { useApi } from '../hooks/useApi'
import { ApiError } from '../hooks/apiClient'
import { isDesktopSurface } from '../desktop/desktopBridge'
import { canUseNativeDialogs, filesFromDroppedPaths, nativeFileReadErrorMessage, pickFilesViaBridge, shouldUseBrowserFileFallback } from '../desktop/desktopFiles'
import { useDesktopFileDrop } from '../desktop/useDesktopFileDrop'
import { type DetectedSourceProtocol, type ImportRepairSuggestion, type EnterpriseArchiveImportResult, type EnterpriseArchiveImportSummary, type EnterpriseArchiveJavaSupportEntry, type EnterpriseArchiveMigrationReadiness, type EnterpriseArchiveProtocolSupportEntry, type EnterpriseArchiveReviewArtifact, type EnterpriseArchiveSupportMatrixEntry, type EnterpriseArchiveSupportMatrixExample, type EnterpriseArchiveSupportMatrixResponse, type SalesforceReview, type SecretBindingSelection, type SharedValues, type TrueClient2CompatReport } from '../types/scriptImports'
import { type BrowserReplayEvent, type BrowserReplaySession, type StoredScript } from '../types'
import { type JavaClasspathAsset, type ReplayPreflight, type SourceArchiveReview, type ScriptActionDefinition, type ScriptDefinition } from '../types/scripts'
import { isJavaClasspathUploadEnabled } from '../utils/featureFlags'
import DropIngestOverlay from './DropIngestOverlay'
import { describeAccepted, describeReading, useDropIngestFeedback } from './dropIngestFeedback'
import ImportCoverageButton from './ImportCoverageButton'
import { JavaClasspathResolver } from './JavaClasspathResolver'
import { JavaDependencyInventory } from './JavaDependencyInventory'
import { javaDependencyInventory } from './javaDependencyInventoryModel'
import { bindJavaClasspathAssets, javaClasspathReviewKey } from './javaClasspathBindings'
import { getErrorMessage } from '../utils/errorUtils'
import { useModalDialog } from '../hooks/useModalDialog'
import { buildReplayStartPayload, createReplaySessionId, isDefiniteReplayStartRejection, summarizeInlineData } from './scriptEditorUtils'
import { getImportSummary } from './scriptEditorImportModel'
import { getImportedRuntimeSectionId } from './scriptEditorRuntimeSettingsModel'
import ScriptImportReview from './ScriptImportReview'
import ArchiveReviewBoundary from './ArchiveReviewBoundary'
import { ENTERPRISE_ARCHIVE_REVIEW_GUARD_ID, archiveReviewOutcome } from './enterpriseArchiveReviewOutcome'
import ImportIssueSeverityTag from './ImportIssueSeverityTag'
import { groupImportFindings, draftImportFindings, reviewSectionFindings, type ImportFinding, type IssueCategory } from './importReviewIssues'
import { prepareProjectSecretsSettingsNavigation } from './projectSecretsNavigation'
import { type SecretBindingReviewHandle } from './SecretBindingReview'
import AuraOutcomeReview from './AuraOutcomeReview'
import SalesforceCorrelationReview from './SalesforceCorrelationReview'
import AuraParameterLocationDetails from './AuraParameterLocationDetails'
import SourceMappingReview from './SourceMappingReview'
import { withFallbackPersistedScript } from './importSaveUtils'
import { salesforceApiSurfaceSummary } from './salesforceReviewSummary'
import {
  refreshedSecretBindingRequirements,
  replaceSecretBindingRequirementsForScript,
  requirementsPreserveDestinationValues,
  secretBindingRequirementsForScript,
  updateSecretBindingRequirementNames,
} from './secretBindingUtils'
import { type DraftEditorFocus, type ImportedSaveResult } from './useScriptsAuthoringState'
import { credentialPreservingImportDraftRequiresSave, earliestImportDraftExpiry } from './protectedImportSession'
import './EnterpriseArchiveImport.css'

interface ImportMetadata {
  source?: string
  source_script_name?: string
  preserved?: Record<string, unknown>
}

function getImportMetadata(script?: ScriptDefinition): ImportMetadata | null {
  return script?.metadata?.import || null
}

function getSourceReviewFromMetadata(metadata?: ImportMetadata | null): SourceArchiveReview | null {
  const sourceReview = metadata?.preserved?.source_review as SourceArchiveReview | undefined
  if (!sourceReview) return null
  if (!Array.isArray(sourceReview.source_files) && !Array.isArray(sourceReview.mappings)) return null
  return sourceReview
}

function getSummaryList(values?: string[]) {
  return Array.isArray(values) ? values : []
}

function isBrowserImport(script: ScriptDefinition | null): boolean {
  return script?.protocol === 'browser'
}

function isEnterpriseArchiveZipFile(file: File) {
  const lowerName = file.name.toLowerCase()
  return lowerName.endsWith('.zip') || lowerName.endsWith('.usz') || file.type === 'application/zip' || file.type === 'application/x-zip-compressed'
}

function isHARFile(file: File) {
  const lowerName = file.name.toLowerCase()
  return lowerName.endsWith('.har') || lowerName.endsWith('.json') || file.type === 'application/json'
}

// A definite draft-attestation rejection is fundamentally different from a
// lost save response. The latter must retain the exact request and operation
// identity so the durable receipt can be reconciled. An expired attestation is
// a one-shot server capability, though, and resubmitting it can never work.
// Drop that retained request so the next Convert action obtains a fresh draft.
function enterpriseArchiveDraftNeedsRefresh(error: unknown): boolean {
  return error instanceof ApiError
    && error.status === 409
    && (error.code === 'import_draft_attestation_invalid'
      // Older controllers did not return the structured code. Retain a narrow
      // message fallback so a newer dashboard can still recover against them.
      || /enterprise archive import draft (?:is invalid or expired|is missing its server capability)/i
        .test(getErrorMessage(error, '')))
}

function javaSimpleClassName(className: string) {
  const normalized = className.trim().replace(/\$/g, '.')
  const parts = normalized.split('.')
  return parts[parts.length - 1] || normalized
}

function javaClassToSuggestedSourcePath(className: string) {
  return `${className.trim().replace(/\./g, '/').replace(/\$/g, '/')}.java`
}

// Matches maxJavaSourceSniffBytes in pkg/convert/enterprisearchive. A package,
// import, or type declaration always appears well within this prefix, and the
// cap is load-bearing rather than cosmetic: the block-comment pass below is
// quadratic on input containing many `/*` with no closing `*/`, so an uncapped
// 4 MB attachment blocks the main thread for ~47s. Supplemental uploads are
// allowed up to 64 MiB, and a plain text file can hit that shape without being
// malicious.
const MAX_JAVA_SOURCE_SNIFF_BYTES = 256 * 1024

// A type declaration only counts when a Java ACCESS/CLASS MODIFIER precedes it.
// A bare `class <Ident> {` is not evidence of Java: it matches English prose
// ("the class Actions {see listing}"), a C enum, a JavaScript class, and CSS.
// Keep this in step with javaSourceTypeRe in pkg/convert/enterprisearchive.
const JAVA_MODIFIER = '(?:public|protected|private|abstract|final|static|sealed|strictfp)'
const JAVA_TYPE_DECLARATION_RE = new RegExp(
  `\\b${JAVA_MODIFIER}(?:[ \\t]+${JAVA_MODIFIER})*[ \\t]+(?:(?:class|interface|enum|record)|@[ \\t]*interface)[ \\t]+[A-Za-z_$][\\w$]*`,
)
// The shapes a compilable Java file can legally open with — mirrors
// javaSourceFileStartRe in pkg/convert/enterprisearchive.
const JAVA_FILE_START_RE = new RegExp(
  `^(?:package[ \\t]|import[ \\t]|@[A-Za-z_$]|${JAVA_MODIFIER}[ \\t]|class[ \\t]|interface[ \\t]|enum[ \\t]|record[ \\t])`,
)

// Mirrors looksLikeJavaSourceText in pkg/convert/enterprisearchive. Comments and
// string literals are masked first so a recording log that merely quotes Java is
// not renamed.
//
// The first-meaningful-line gate below is NOT optional mirroring: because this
// function renames the file to .java BEFORE upload, and the server trusts any
// .java name without re-sniffing, any input this accepts and the server would
// reject is smuggled straight past the server's protections. A prose note that
// quotes a full class listing is exactly such an input — the quoted declaration
// matches JAVA_TYPE_DECLARATION_RE, and only the file-must-begin-like-Java rule
// stops it.
function looksLikeJavaSource(text: string): boolean {
  const code = (text.length > MAX_JAVA_SOURCE_SNIFF_BYTES ? text.slice(0, MAX_JAVA_SOURCE_SNIFF_BYTES) : text)
    .replace(/\/\*[^]*?\*\//g, ' ')
    .replace(/\/\/.*$/gm, ' ')
    .replace(/"(?:\\.|[^"\\])*"/g, '""')
    .replace(/'(?:\\.|[^'\\])*'/g, "''")
  const firstMeaningfulLine = code.split('\n').map(line => line.trim()).find(line => line !== '') ?? ''
  if (!JAVA_FILE_START_RE.test(firstMeaningfulLine)) return false
  if (JAVA_TYPE_DECLARATION_RE.test(code)) return true
  const hasDeclaration = /^[ \t]*package[ \t]+[A-Za-z_$][\w$]*(?:[ \t]*\.[ \t]*[A-Za-z_$][\w$]*)*[ \t]*;/m.test(code)
    || /^[ \t]*import[ \t]+(?:static[ \t]+)?[A-Za-z_$][\w$]*(?:[ \t]*\.[ \t]*(?:[A-Za-z_$][\w$]*|\*))*[ \t]*;/m.test(code)
  return hasDeclaration && code.includes('{')
}

// Java sources are routinely renamed to .txt so corporate mail gateways let them
// through. The archive importer performs the authoritative rename server-side,
// so API clients get the same behavior; doing it here as well keeps the required
// files list showing the resolved .java name while the user is still attaching.
//
// javaContext is REQUIRED and must reflect the archive actually being a Java
// import, mirroring archiveHasJavaSourceSignal on the server. Renaming
// unconditionally defeats that server gate: the file arrives already named
// .java, which by itself satisfies the gate, and a non-Java archive is then
// diverted to the Java converter with its recorded workload discarded. It also
// lets a client-renamed name slip past the server's rename-collision guard.
async function normalizeJavaSourceSupplementalFile(file: File, javaContext: boolean): Promise<File> {
  if (!javaContext) return file
  const name = file.name.trim()
  const lowerName = name.toLowerCase()
  if (lowerName.endsWith('.java') || !lowerName.endsWith('.txt')) return file

  const sourceText = await file.text()
  if (!looksLikeJavaSource(sourceText)) return file

  const javaName = lowerName.endsWith('.java.txt')
    ? name.replace(/\.txt$/i, '')
    : name.replace(/\.txt$/i, '.java')
  // Reject names whose whole base is the extension; renaming those yields a
  // dotfile, not a class.
  if (javaName.split(/[\\/]/).pop()!.length <= '.java'.length) return file

  // Rebuild from the ORIGINAL blob, never from sourceText: file.text() decodes
  // as UTF-8 with U+FFFD replacement, so re-encoding that string silently
  // corrupts every non-UTF-8 byte. Windows-authored LoadRunner Java sources are
  // routinely windows-1252, where a literal like "café" would come back as
  // "caf�" — wrong bytes in the source manifest and the compiled runner,
  // with no error anywhere. Only the NAME changes here; the bytes must not.
  return new File([file], javaName, {
    type: file.type || 'text/x-java-source',
    lastModified: file.lastModified,
  })
}

type RequiredFileCandidate = {
  className: string
  suggestedPath: string
  reason: string
}

// Java fidelity blockers name the exact dependency that could not be resolved.
// Turning those names into concrete source-file candidates lets reviewers attach
// the missing sources without hand-parsing blocker prose.
function parseRequiredJavaFilesFromFidelityBlockers(blockers: string[]): RequiredFileCandidate[] {
  if (!Array.isArray(blockers) || blockers.length === 0) return []

  const patterns: RegExp[] = [
    /extends unresolved superclass\s+([A-Za-z_][\w.$]*)/i,
    /unresolved superclass\s+([A-Za-z_][\w.$]*)/i,
    /Archived superclass\s+([A-Za-z_][\w.$]*)\s+has no resolvable zero-argument constructor/i,
  ]
  const candidates = new Map<string, RequiredFileCandidate>()

  blockers.forEach((blocker) => {
    for (const pattern of patterns) {
      const match = blocker.match(pattern)
      if (!match) continue
      const className = match[1]?.trim()
      if (!className) continue

      const key = className.toLowerCase()
      if (!candidates.has(key)) {
        candidates.set(key, {
          className,
          suggestedPath: javaClassToSuggestedSourcePath(className),
          reason: blocker,
        })
      }
      break
    }
  })

  return Array.from(candidates.values())
}

function formatEnterpriseArchiveFileSize(file: File) {
  return `${(file.size / 1024).toFixed(1)} KB`
}

function formatSalesforceCoverage(review?: SalesforceReview | null) {
  const coverage = review?.har_match_coverage
  if (!coverage?.har_provided) return 'No HAR matched'
  return `${coverage.matched_requests}/${coverage.imported_requests} matched (${coverage.coverage_pct.toFixed(1)}%)`
}

function salesforceDomainSummary(review?: SalesforceReview | null) {
  const domains = review?.domains || []
  if (domains.length === 0) return 'No Salesforce domains detected'
  return domains.slice(0, 3).map(domain => domain.host).join(', ')
}

function salesforceTransactionCount(review?: SalesforceReview | null) {
  return review?.transactions?.length || 0
}

function scriptActionCount(script?: ScriptDefinition | null) {
  if (!script) return 0
  const phaseActions = (phase: 'init' | 'actions' | 'teardown') => {
    const value = (script as ScriptDefinition & Record<string, unknown>)[phase]
    return Array.isArray(value) ? value.length : 0
  }
  return phaseActions('init') + phaseActions('actions') + phaseActions('teardown')
}

function orderedImportScriptActions(script?: ScriptDefinition | null): ScriptActionDefinition[] {
  if (!script) return []
  const phasedScript = script as ScriptDefinition & {
    init?: ScriptActionDefinition[]
    teardown?: ScriptActionDefinition[]
  }
  return [
    ...(phasedScript.init || []),
    ...(script.actions || []),
    ...(phasedScript.teardown || []),
  ]
}

function flattenImportScriptActions(actions: ScriptActionDefinition[]): ScriptActionDefinition[] {
  const flattened: ScriptActionDefinition[] = []
  actions.forEach(action => {
    flattened.push(action)
    if (action.control?.steps?.length) {
      flattened.push(...flattenImportScriptActions(action.control.steps))
    }
    if (action.control?.else?.length) {
      flattened.push(...flattenImportScriptActions(action.control.else))
    }
	;(action.control?.cases || []).forEach(switchCase => {
		if (switchCase.steps?.length) flattened.push(...flattenImportScriptActions(switchCase.steps))
	})
	if (action.control?.default?.length) {
		flattened.push(...flattenImportScriptActions(action.control.default))
	}
  })
  return flattened
}

function importScriptActionsDeep(script?: ScriptDefinition | null): ScriptActionDefinition[] {
  return flattenImportScriptActions(orderedImportScriptActions(script))
}

function getTrueClient2CompatReport(result: EnterpriseArchiveImportResult | null, metadata?: ImportMetadata | null): TrueClient2CompatReport | null {
  const topLevel = result?.trueclient2_compat_report
  if (topLevel) return topLevel
  const summaryReport = result?.summary?.trueclient2_compat_report
  if (summaryReport) return summaryReport
  const preservedReport = metadata?.preserved?.trueclient2_compat_report
  if (preservedReport && typeof preservedReport === 'object' && !Array.isArray(preservedReport)) {
    return preservedReport as TrueClient2CompatReport
  }
  return null
}

function getEnterpriseArchiveResultScripts(result: EnterpriseArchiveImportResult | null): ScriptDefinition[] {
  if (!result) return []
  if (Array.isArray(result.scripts) && result.scripts.length > 0) {
    return result.scripts
  }
  return result.script ? [result.script] : []
}

type EnterpriseArchiveScriptNameRegistry = Map<string, number>

function normalizedEnterpriseArchiveScriptName(name: string): string {
  return name.trim().toLowerCase()
}

function reserveEnterpriseArchiveScriptName(
  name: string,
  registry: EnterpriseArchiveScriptNameRegistry,
): string {
  const identity = normalizedEnterpriseArchiveScriptName(name)
  if (!identity) return name
  let count = registry.get(identity) || 0
  if (count === 0) {
    registry.set(identity, 1)
    return name
  }
  for (;;) {
    count += 1
    const candidate = `${name}_${count}`
    const candidateIdentity = normalizedEnterpriseArchiveScriptName(candidate)
    if (!registry.has(candidateIdentity)) {
      registry.set(identity, count)
      registry.set(candidateIdentity, 1)
      return candidate
    }
  }
}

/**
 * The server converts one enterprise archive at a time, but the dashboard can
 * commit several conversion results in one batch. Allocate names across that
 * complete UI batch before credentials are scoped or scripts are saved so two
 * archives with the same human name cannot become an ambiguous requirement or
 * collide at the atomic save boundary.
 */
function uniquifyEnterpriseArchiveResultScriptNames(
  result: EnterpriseArchiveImportResult,
  registry: EnterpriseArchiveScriptNameRegistry,
  retainedNames?: string[],
): EnterpriseArchiveImportResult {
  const sourceScripts = getEnterpriseArchiveResultScripts(result)
  if (sourceScripts.length === 0) return result
  const scripts = sourceScripts.map((script, index) => ({
    ...script,
    name: retainedNames?.[index] ?? reserveEnterpriseArchiveScriptName(script.name || '', registry),
  }))
  const renamedByIndex = scripts.map((script, index) => ({
    prior: sourceScripts[index]?.name || '',
    current: script.name || '',
  }))
  const scriptNameForSideRecord = (scriptIndex?: number, scriptName?: string) => {
    if (typeof scriptIndex === 'number' && renamedByIndex[scriptIndex]) {
      return renamedByIndex[scriptIndex].current
    }
    if (!scriptName) return scriptName
    return renamedByIndex.find(rename => rename.prior === scriptName)?.current || scriptName
  }
  return {
    ...result,
    scripts,
    ...(result.script ? { script: scripts[0] } : {}),
    ...(result.secret_binding_requirements ? {
      secret_binding_requirements: result.secret_binding_requirements.map(requirement => ({
        ...requirement,
        script_name: scriptNameForSideRecord(requirement.script_index, requirement.script_name),
      })),
    } : {}),
    ...(result.parameterization ? {
      parameterization: {
        ...result.parameterization,
        suggestions: result.parameterization.suggestions.map(suggestion => ({
          ...suggestion,
          script_name: scriptNameForSideRecord(undefined, suggestion.script_name),
        })),
      },
    } : {}),
  }
}

function getEnterpriseArchiveReplayPreflight(
  script: ScriptDefinition | null,
  result: EnterpriseArchiveImportResult | null,
): ReplayPreflight | null {
  const lanePreflight = script?.metadata?.import?.preserved?.replay_preflight
  if (lanePreflight && typeof lanePreflight === 'object' && !Array.isArray(lanePreflight)) {
    return lanePreflight as ReplayPreflight
  }
  return result?.preflight || null
}

function enterpriseArchiveResultScriptIsSaved(item: EnterpriseArchiveImportQueueItem, scriptIndex: number): boolean {
  return Object.prototype.hasOwnProperty.call(item.savedResultScripts, scriptIndex)
}

function firstUnsavedEnterpriseArchiveResultScriptIndex(item: EnterpriseArchiveImportQueueItem): number {
  const scripts = getEnterpriseArchiveResultScripts(item.result)
  const pendingIndex = scripts.findIndex((_script, index) => !enterpriseArchiveResultScriptIsSaved(item, index))
  return pendingIndex >= 0 ? pendingIndex : 0
}

function findFirstPendingEnterpriseArchiveSelection(
  items: readonly EnterpriseArchiveImportQueueItem[],
): { itemId: string; scriptIndex: number } | null {
  for (const item of items) {
    const scripts = getEnterpriseArchiveResultScripts(item.result)
    const pendingIndex = scripts.findIndex((_script, index) => !enterpriseArchiveResultScriptIsSaved(item, index))
    if (pendingIndex >= 0) {
      return { itemId: item.id, scriptIndex: pendingIndex }
    }
    if (
      item.status === 'queued'
      || item.status === 'validating'
      || item.status === 'validated'
      || item.status === 'converting'
      || item.status === 'failed'
      || (item.status === 'converted' && scripts.length === 0)
    ) {
      return { itemId: item.id, scriptIndex: 0 }
    }
  }
  return null
}

function collectSavedEnterpriseArchiveScripts(items: readonly EnterpriseArchiveImportQueueItem[]): StoredScript[] {
  return items.flatMap(item => Object.entries(item.savedResultScripts)
    .sort(([left], [right]) => Number(left) - Number(right))
    .map(([, script]) => script))
}

function enterpriseArchiveImportStatusLabel(status: EnterpriseArchiveImportQueueStatus) {
  switch (status) {
    case 'validating':
      return 'Validating'
    case 'validated':
      return 'Validated'
    case 'converting':
      return 'Converting'
    case 'converted':
      return 'Converted'
    case 'failed':
      return 'Failed'
    default:
      return 'Queued'
  }
}


function enterpriseArchiveImportStatusIsRunning(status: EnterpriseArchiveImportQueueStatus) {
  return status === 'validating' || status === 'converting'
}

function nextRequestToken(tokens: Map<string, number>, key: string): number {
  const next = (tokens.get(key) || 0) + 1
  tokens.set(key, next)
  return next
}

function importOptionsIdentity(options: ImportOptions): string {
  return JSON.stringify(options)
}

function buildEnterpriseArchiveImportQueueItem(file: File, id: string, folderFiles?: File[]): EnterpriseArchiveImportQueueItem {
  return {
    id,
    file,
    folderFiles,
    harFile: null,
    supplementalFiles: [],
    lastRequiredFileCandidates: [],
    status: 'queued',
    result: null,
    reviewMode: null,
    error: '',
    notice: '',
    replayValidationLoading: false,
    replayValidationError: '',
    replayValidationWarnings: [],
    replayValidationSession: null,
    replayReviewGeneration: -1,
    replayScriptIndex: -1,
    replayProjectId: '',
    reviewGeneration: 0,
    reviewOptionsIdentity: '',
    savedResultScripts: {},
    sourceSecretProjectId: '',
  }
}

function buildReviewArtifactDraft(
  artifact: EnterpriseArchiveReviewArtifact,
  detectedProtocol: DetectedSourceProtocol | null,
  projectId?: string,
  requestedName?: string,
  sourceReview?: SourceArchiveReview | null,
): ScriptDefinition {
  const protocol = detectedProtocol?.apexload_protocol || detectedProtocol?.canonical_source_protocol || 'review'
  const name = requestedName?.trim() || artifact.title || detectedProtocol?.display_name || 'Enterprise Script Archive Protocol Review'
  return {
    name,
    version: '1.0.0',
    protocol,
    project_id: projectId,
    generated_by: 'apexload-enterprise-script-archive-importer',
    generated_from: 'enterprise_script_archive',
    config: { base_url: '' },
    actions: [],
    metadata: {
      import: {
        source: 'enterprise_script_archive',
        source_script_name: name,
        preserved: {
          detected_protocol: detectedProtocol,
          review_artifact: artifact,
          ...(sourceReview ? { source_review: sourceReview } : {}),
        },
      },
    },
  } as ScriptDefinition
}

interface Props {
  destinationControl?: ReactNode
  initialFiles?: File[] | null
  projectId?: string
  // folderId is the destination folder for scripts saved from this import,
  // matching ScriptEditor's prop. The server binds it on create and re-validates
  // it when a review session is approved, so omitting it saves to project root.
  folderId?: string | null
  onImported?: (result: ImportedSaveResult) => void
  /** Opens a generated script in the script editor. onSaved tells the review the editor saved it to the library. */
  onEdit?: (script: ScriptDefinition, focus?: DraftEditorFocus, onSaved?: (saved: StoredScript) => void) => void
  onClose?: () => void
  onOperationBusyChange?: (busy: boolean) => void
}

type ReviewMode = 'validation' | 'conversion'
type EnterpriseArchiveImportQueueStatus = 'queued' | 'validating' | 'validated' | 'converting' | 'converted' | 'failed'

interface EnterpriseArchiveImportQueueItem {
  id: string
  file: File
  folderFiles?: File[]
  archiveFile?: File
  archiveMember?: string
  sourceSize?: number
  harFile: File | null
  supplementalFiles: File[]
  lastRequiredFileCandidates: RequiredFileCandidate[]
  status: EnterpriseArchiveImportQueueStatus
  result: EnterpriseArchiveImportResult | null
  reviewMode: ReviewMode | null
  error: string
  notice: string
  replayValidationLoading: boolean
  replayValidationError: string
  replayValidationWarnings: string[]
  replayValidationSession: BrowserReplaySession | null
  replayReviewGeneration: number
  replayScriptIndex: number
  replayProjectId: string
  reviewGeneration: number
  reviewOptionsIdentity: string
  savedResultScripts: Record<number, StoredScript>
  sourceSecretProjectId: string
}

type EnterpriseArchiveAtomicSaveEntry = {
  itemId: string
  file: File
  harFile: File | null
  supplementalFiles: File[]
  result: EnterpriseArchiveImportResult
  scripts: ScriptDefinition[]
}

type RetainedEnterpriseArchiveAtomicSave = {
  operationKey: string
  operationId: string
  projectId: string | undefined
  folderId: string | null
  optionsIdentity: string
  entries: EnterpriseArchiveAtomicSaveEntry[]
  scriptsToPersist: ScriptDefinition[]
}

type EnterpriseArchiveSupportMatrixSection = {
  key: string
  title: string
  items: EnterpriseArchiveSupportMatrixEntry[]
  preview: string
}

type EnterpriseArchiveProtocolCoverageSection = {
  key: string
  title: string
  items: EnterpriseArchiveProtocolSupportEntry[]
  preview: string
  hint: string
}

type EditorActionButton = {
  key: string
  label: string
  onClick: () => void
  // False for a button that only reviews or refreshes, so it does not wait
  // for the editor to be available.
  opensEditor?: boolean
  // Why the button is unavailable for a reason of its own.
  disabledReason?: string
}

type ImportOptions = {
  name: string
  base_url: string
  include_assertions: boolean
  preserve_transactions: boolean
  salesforce_native: boolean
  block_partial: boolean
  block_compatibility_only: boolean
  block_non_applicable: boolean
  expire_draft: boolean
}

type RelaxableMigrationBlockStatus = 'partial' | 'compatibility_only' | 'non_applicable'

let enterpriseArchiveSaveOperationSequence = 0

function createEnterpriseArchiveSaveOperationID() {
  const randomID = createReplaySessionId().trim()
  if (randomID) return `enterprise-archive-${randomID}`
  enterpriseArchiveSaveOperationSequence += 1
  return `enterprise-archive-${Date.now().toString(36)}-${enterpriseArchiveSaveOperationSequence.toString(36)}`
}

const DEFAULT_IMPORT_OPTIONS: ImportOptions = {
  name: '',
  base_url: '',
  include_assertions: true,
  preserve_transactions: true,
  salesforce_native: false,
  block_partial: false,
  block_compatibility_only: false,
  block_non_applicable: false,
  expire_draft: false,
}
const EMPTY_REPLAY_VALIDATION_WARNINGS: string[] = []
const EMPTY_SUPPLEMENTAL_FILES: File[] = []

interface BrowserReplayEvidenceSummary {
  events: number
  passed: number
  failed: number
  screenshots: number
  domHtmlCaptures: number
  consoleEntries: number
  networkEntries: number
  sourceSteps: number
  sourceTransactions: number
}

interface PromotionReviewInputs {
  compatReport?: TrueClient2CompatReport | null
  replaySession?: BrowserReplaySession | null
  replayWarnings?: string[]
  importWarnings?: string[]
}

function formatRuntimeSettingStatusLabel(status?: string | null) {
  const normalized = status?.trim().replace(/[_-]+/g, ' ')
  if (!normalized) return 'Unknown'
  return normalized.replace(/\b\w/g, char => char.toUpperCase())
}

function formatEvidenceCount(count: number, singular: string, plural = `${singular}s`) {
  return `${count} ${count === 1 ? singular : plural}`
}

function getReplaySessionEvents(session?: BrowserReplaySession | null): BrowserReplayEvent[] {
  if (Array.isArray(session?.events) && session.events.length > 0) return session.events
  if (Array.isArray(session?.report?.events) && session.report.events.length > 0) return session.report.events
  return session?.current_event ? [session.current_event] : []
}

function summarizeBrowserReplayEvidence(session?: BrowserReplaySession | null): BrowserReplayEvidenceSummary {
  const events = getReplaySessionEvents(session)
  const manifest = session?.report?.artifact_manifest
  const sourceSteps = new Set<string>()
  const sourceTransactions = new Set<string>()
  let screenshots = 0
  let domHtmlCaptures = 0
  let consoleEntries = 0
  let networkEntries = 0
  let passed = 0
  let failed = 0

  events.forEach((event) => {
    const snapshot = event.snapshot
    if (event.success) {
      passed += 1
    } else {
      failed += 1
    }
    if (event.screenshot_base64 || snapshot?.screenshot_base64) screenshots += 1
    if (event.html || event.dom_summary || snapshot?.html || snapshot?.dom_summary) domHtmlCaptures += 1
    consoleEntries += (event.console_entries || snapshot?.console_entries || []).length
    networkEntries += (event.network_entries || snapshot?.network_entries || []).length
    const sourceStep = event.source_step_id || event.source_action || event.source_action_name || (
      typeof event.source_step_index === 'number' ? `source-step-${event.source_step_index}` : ''
    )
    if (sourceStep) sourceSteps.add(sourceStep)
    if (event.source_transaction) sourceTransactions.add(event.source_transaction)
  })

  return {
    events: events.length,
    passed,
    failed,
    screenshots: Math.max(screenshots, manifest?.screenshots || 0),
    domHtmlCaptures: Math.max(domHtmlCaptures, manifest?.snapshots || 0, manifest?.html_captured ? 1 : 0),
    consoleEntries: Math.max(consoleEntries, manifest?.console_entries || 0),
    networkEntries: Math.max(networkEntries, manifest?.network_entries || 0),
    sourceSteps: sourceSteps.size,
    sourceTransactions: sourceTransactions.size,
  }
}

function buildTrueClientPromotionReview(script: ScriptDefinition, inputs: PromotionReviewInputs): Record<string, unknown> {
  const metadata = getImportMetadata(script)
  const actions = importScriptActionsDeep(script)
  const replayEvidence = summarizeBrowserReplayEvidence(inputs.replaySession)
  const reviewRequiredActions = actions.filter(action => Boolean(action.metadata?.review_required)).length
  const javascriptReviewActions = actions.filter(action => action.type === 'browser_evaluate_js' || action.metadata?.review_gate === 'javascript').length
  const selectorFallbackCandidates = actions.reduce((count, action) => {
    const fallbacks = action.metadata?.source_selector_fallbacks
    return count + (Array.isArray(fallbacks) ? fallbacks.length : 0)
  }, 0)
  const replayAttached = Boolean(inputs.replaySession?.id && inputs.replaySession.status === 'completed' && replayEvidence.failed === 0)

  return {
    schema_version: 1,
    status: replayAttached ? 'replay_evidence_attached' : 'needs_replay_evidence',
    generated_at: new Date().toISOString(),
    source: metadata?.source || 'enterprise_script_archive',
    source_script_name: metadata?.source_script_name || '',
    compatibility: {
      source_profile: inputs.compatReport?.source_profile,
      import_mode: inputs.compatReport?.import_mode,
      promotion_status: inputs.compatReport?.promotion_status,
      promotion_allowed: inputs.compatReport?.promotion_allowed ?? false,
      runtime_parity_status: inputs.compatReport?.runtime_parity_status,
      replay_status: replayAttached ? 'attached' : inputs.compatReport?.replay_status,
      javascript_review_gate: inputs.compatReport?.javascript_review_gate,
      http_conversion_readiness: inputs.compatReport?.http_conversion_readiness,
      selector_confidence_pct: inputs.compatReport?.selector_confidence_pct,
      frame_window_blockers: inputs.compatReport?.frame_window_blockers ?? 0,
    },
    gates: {
      review_required_actions: reviewRequiredActions,
      javascript_review_actions: javascriptReviewActions,
      selector_fallback_candidates: selectorFallbackCandidates,
      replay_required: !replayAttached,
      replay_warnings: inputs.replayWarnings || [],
      import_warnings: inputs.importWarnings || [],
    },
    replay_evidence: {
      session_id: inputs.replaySession?.id || '',
      status: inputs.replaySession?.status || 'not_run',
      started_at: inputs.replaySession?.started_at || '',
      completed_at: inputs.replaySession?.completed_at || inputs.replaySession?.report?.completed_at || '',
      events: replayEvidence.events,
      passed: replayEvidence.passed,
      failed: replayEvidence.failed,
      screenshots: replayEvidence.screenshots,
      dom_html_captures: replayEvidence.domHtmlCaptures,
      console_entries: replayEvidence.consoleEntries,
      network_entries: replayEvidence.networkEntries,
      source_steps: replayEvidence.sourceSteps,
      source_transactions: replayEvidence.sourceTransactions,
    },
    next_actions: replayAttached
      ? ['Review selector fallbacks and JavaScript approvals before production promotion.']
      : ['Run draft browser replay and attach evidence before production promotion.'],
  }
}

function attachTrueClientPromotionReview(script: ScriptDefinition, inputs: PromotionReviewInputs): ScriptDefinition {
  const promotionReview = buildTrueClientPromotionReview(script, inputs)
  const existingMetadata = script.metadata || {}
  const existingImport = existingMetadata.import || {}
  return {
    ...script,
    metadata: {
      ...existingMetadata,
      import: {
        ...existingImport,
        preserved: {
          ...(existingImport.preserved || {}),
          trueclient2_promotion_review: promotionReview,
        },
      },
    },
  }
}

function normalizeReplaySuggestionCopy(value?: string | null) {
  if (!value) return ''

  return value
    .replace(/\bbrowser replay\b/gi, (match) => (
      match[0] === 'B' ? 'Draft Browser Replay' : 'draft browser replay'
    ))
    .replace(/before saving as production-ready/gi, 'before saving it to the library')
}

function formatMigrationBlockPolicySummary(options: Pick<ImportOptions, 'block_partial' | 'block_compatibility_only' | 'block_non_applicable'>) {
  return buildMigrationBlockStatuses(options)
    .map((status) => formatRuntimeSettingStatusLabel(status))
    .join(', ')
}

function hasCustomMigrationBlockPolicy(options: Pick<ImportOptions, 'block_partial' | 'block_compatibility_only' | 'block_non_applicable'>) {
  return options.block_partial !== DEFAULT_IMPORT_OPTIONS.block_partial
    || options.block_compatibility_only !== DEFAULT_IMPORT_OPTIONS.block_compatibility_only
    || options.block_non_applicable !== DEFAULT_IMPORT_OPTIONS.block_non_applicable
}

function buildMigrationPolicyOptions(statuses?: string[] | null): Pick<ImportOptions, 'block_partial' | 'block_compatibility_only' | 'block_non_applicable'> {
  const normalizedStatuses = new Set((statuses || []).map((status) => status.trim().toLowerCase()).filter(Boolean))
  return {
    block_partial: normalizedStatuses.has('partial'),
    block_compatibility_only: normalizedStatuses.has('compatibility_only'),
    block_non_applicable: normalizedStatuses.has('non_applicable'),
  }
}

function sameMigrationBlockPolicy(
  left: Pick<ImportOptions, 'block_partial' | 'block_compatibility_only' | 'block_non_applicable'>,
  right: Pick<ImportOptions, 'block_partial' | 'block_compatibility_only' | 'block_non_applicable'>,
) {
  return left.block_partial === right.block_partial
    && left.block_compatibility_only === right.block_compatibility_only
    && left.block_non_applicable === right.block_non_applicable
}

function buildMigrationPolicyRefreshNotice(mode: 'convert' | 'validate', options: Pick<ImportOptions, 'block_partial' | 'block_compatibility_only' | 'block_non_applicable'>) {
  const prefix = mode === 'validate' ? 'Validation refreshed' : 'Conversion refreshed'
  return `${prefix} with migration block policy: ${formatMigrationBlockPolicySummary(options)}.`
}

function findFirstImportedRuntimeArea(entries?: Array<{ area?: string | null }> | null): string | null {
  if (!Array.isArray(entries)) return null

  for (const entry of entries) {
    const area = entry?.area?.trim()
    if (area && getImportedRuntimeSectionId(area)) {
      return area
    }
  }

  return null
}

function findUnsupportedRepairSuggestion(repairSuggestions: ImportRepairSuggestion[], unsupportedFunction: string): ImportRepairSuggestion | null {
  const normalizedFunction = unsupportedFunction.trim().toLowerCase()
  if (!normalizedFunction) return null

  return repairSuggestions.find((suggestion) => {
    if (suggestion.category !== 'unsupported' || !suggestion.target_action?.trim()) {
      return false
    }
    return suggestion.title.toLowerCase().includes(normalizedFunction)
  }) || null
}

const validationReviewTabs = [
  { id: 'overview', label: 'Review' },
  { id: 'credentials', label: 'Credentials' },
  { id: 'data', label: 'Data & parameters' },
  { id: 'preserved', label: 'Runtime settings' },
  { id: 'dsl', label: 'DSL Preview' },
  { id: 'source', label: 'Source Mapping' },
] as const

type ValidationReviewTab = typeof validationReviewTabs[number]['id']

const ENTERPRISE_QUEUE_PAGE_SIZE = 50
const ENTERPRISE_RESULT_PAGE_SIZE = 50

export default function EnterpriseArchiveImport({ destinationControl, initialFiles, projectId, folderId, onImported, onEdit, onClose, onOperationBusyChange }: Props) {
  const api = useApi()
  const apiRef = useRef(api)
  apiRef.current = api
  const fileRef = useRef<HTMLInputElement>(null)
  const folderRef = useRef<HTMLInputElement>(null)
  const harFileRef = useRef<HTMLInputElement>(null)
  const supplementalFileRef = useRef<HTMLInputElement>(null)
  const dropzoneRef = useRef<HTMLDivElement>(null)
  const itemCounterRef = useRef(0)
  const desktop = isDesktopSurface()
  const [browserPickerFallback, setBrowserPickerFallback] = useState(false)
  const nativeDialogs = desktop && canUseNativeDialogs() && !browserPickerFallback
  const [importItems, setImportItems] = useState<EnterpriseArchiveImportQueueItem[]>([])
  const [selectedItemId, setSelectedItemId] = useState('')
  const queueItemButtonRefs = useRef(new Map<string, HTMLButtonElement>())
  const pendingQueueItemFocusRef = useRef<string | null>(null)
  // User-uploaded classpath JAR bindings, keyed by project/item/script so
  // each imported Java script keeps its own attachments. Merged into the script
  // config at save time. A ref mirrors it for the async save handler.
  const [classpathAssetsByKey, setClasspathAssetsByKey] = useState<Record<string, JavaClasspathAsset[]>>({})
  const classpathAssetsByKeyRef = useRef(classpathAssetsByKey)
  classpathAssetsByKeyRef.current = classpathAssetsByKey
  const [classpathUploadInProgress, setClasspathUploadInProgress] = useState(false)
  const classpathUploadInFlightRef = useRef<object | null>(null)
  const [dragOver, setDragOver] = useState(false)
  // Destructured for stable callback identity — the native drop handler is a
  // useDesktopFileDrop dependency and must not change every render.
  const { phase: ingestPhase, label: ingestLabel, hostClassName: ingestHostClass, begin: beginIngest, succeed: succeedIngest, fail: failIngest } = useDropIngestFeedback()
  const [workingMode, setWorkingMode] = useState<'convert' | 'validate' | null>(null)
  // An archive to convert again after the editor saved one of its validated
  // drafts, and the archive whose remaining drafts are being converted again
  // (nothing is saved meanwhile).
  const [convertAfterEditorSave, setConvertAfterEditorSave] = useState<{ itemId: string; notice: string; archiveName: string } | null>(null)
  const [refreshingRemainingDraftsOf, setRefreshingRemainingDraftsOf] = useState<{ itemId: string; archiveName: string } | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [supportMatrix, setSupportMatrix] = useState<EnterpriseArchiveSupportMatrixEntry[]>([])
  const [supportMatrixNotes, setSupportMatrixNotes] = useState<string[]>([])
  const [supportMatrixExamples, setSupportMatrixExamples] = useState<EnterpriseArchiveSupportMatrixExample[]>([])
  const [protocolSupportMatrix, setProtocolSupportMatrix] = useState<EnterpriseArchiveProtocolSupportEntry[]>([])
  const [javaImportSurfaceMatrix, setJavaImportSurfaceMatrix] = useState<EnterpriseArchiveJavaSupportEntry[]>([])
  const [javaHTTPIdiomMatrix, setJavaHTTPIdiomMatrix] = useState<EnterpriseArchiveJavaSupportEntry[]>([])
  const [javaJMSIdiomMatrix, setJavaJMSIdiomMatrix] = useState<EnterpriseArchiveJavaSupportEntry[]>([])
  const [javaJDBCIdiomMatrix, setJavaJDBCIdiomMatrix] = useState<EnterpriseArchiveJavaSupportEntry[]>([])
  const [runtimeCoverageOpen, setRuntimeCoverageOpen] = useState(false)
  const runtimeCoverageDialogRef = useRef<HTMLDivElement | null>(null)
  const runtimeCoverageCloseButtonRef = useRef<HTMLButtonElement | null>(null)
  const [reviewArtifactSaving, setReviewArtifactSaving] = useState(false)
  const [reviewArtifactPersistenceInProgress, setReviewArtifactPersistenceInProgress] = useState(false)
  const [savedReviewSessions, setSavedReviewSessions] = useState<Record<string, number>>({})
  const [selectedResultScriptIndex, setSelectedResultScriptIndex] = useState(0)
  const resultScriptButtonRefs = useRef(new Map<number, HTMLButtonElement>())
  const pendingResultScriptFocusRef = useRef<number | null>(null)
  const [validationReviewTab, setValidationReviewTab] = useState<ValidationReviewTab>('overview')
  const [validationReviewPanelMinHeight, setValidationReviewPanelMinHeight] = useState(0)
  const [repairSourceFocus, setRepairSourceFocus] = useState({ id: '', file: '', revision: 0 })
  const credentialReviewRef = useRef<SecretBindingReviewHandle | null>(null)
  const editorActionInFlightRef = useRef(false)
  const [editorActionPreparing, setEditorActionPreparing] = useState(false)
  const [settingsEditing, setSettingsEditing] = useState(false)
  const [internalReviewBusy, setInternalReviewBusy] = useState(false)
  const [guidedRepairBusy, setGuidedRepairBusy] = useState(false)
  type ImportSaveScope = {
    itemId: string
    generation: number
    operationGeneration: number
    scriptIndex: number
    projectId: string | undefined
    optionsIdentity: string
    script: ScriptDefinition
    operationKey?: string
    operationId?: string
  }
  const scriptSaveInFlightRef = useRef<ImportSaveScope | null>(null)
  const reviewArtifactSaveInFlightRef = useRef<ImportSaveScope | null>(null)
  const atomicSaveInFlightRef = useRef<{ operationKey: string; operationId: string } | null>(null)
  const [atomicSaveInProgress, setAtomicSaveInProgress] = useState(false)
  const [scriptSaveInProgress, setScriptSaveInProgress] = useState(false)
  const operationBusy = classpathUploadInProgress || workingMode !== null
    || reviewArtifactSaving
    || reviewArtifactPersistenceInProgress
    || editorActionPreparing
    || internalReviewBusy
    || guidedRepairBusy
    || scriptSaveInProgress
    || atomicSaveInProgress
  // Batches and saves record drafts too, so no draft opens in the editor while
  // one runs; the review's own busy states only disable its buttons.
  const editorBlockedByBatch = workingMode !== null || atomicSaveInProgress || scriptSaveInProgress
    || reviewArtifactSaving || reviewArtifactPersistenceInProgress || classpathUploadInProgress
  // The archive whose remaining drafts are being converted again, while that
  // run is the one in progress.
  const refreshingRemainingDrafts = refreshingRemainingDraftsOf && workingMode === 'convert'
    && importItems.find(item => item.id === refreshingRemainingDraftsOf.itemId)?.status === 'converting'
    ? refreshingRemainingDraftsOf.archiveName
    : null
  const sourceMutationLocked = classpathUploadInProgress || settingsEditing || reviewArtifactPersistenceInProgress || scriptSaveInProgress || atomicSaveInProgress
    // Options stay put while an archive's remaining drafts are converted again.
    || refreshingRemainingDrafts !== null
  const sourceMutationLockedRef = useRef(sourceMutationLocked)
  sourceMutationLockedRef.current = sourceMutationLocked
  useEffect(() => {
    onOperationBusyChange?.(operationBusy)
    return () => onOperationBusyChange?.(false)
  }, [onOperationBusyChange, operationBusy])
  const operationGenerationRef = useRef(0)
  // The converted-draft Save/Edit actions of the latest render. See
  // runValidatedReviewAction for why queued validation actions read them.
  const convertedReviewActionsRef = useRef<{
    save: () => void | Promise<void>
    edit: () => void
    convertValidated: (itemIds: string[]) => Promise<void>
  }>({ save: () => {}, edit: () => {}, convertValidated: async () => {} })
  // Reuse the same server idempotency identity after a lost response. A retry
  // must reconcile the original atomic save, never create another copy.
  const saveOperationIDsRef = useRef(new Map<string, string>())
  // Keep the exact post-conversion payload as well as its operation identity.
  // Attestation tokens are one-shot server capabilities, so reconverting after
  // an ambiguous response produces different tokens and therefore a different
  // receipt digest. A retry must submit these same scripts unchanged.
  const retainedAtomicSaveRef = useRef<RetainedEnterpriseArchiveAtomicSave | null>(null)
  // The archives an unconfirmed atomic save covers: none opens in the editor
  // until Convert again reconciles that save.
  const [retainedAtomicSaveItemIds, setRetainedAtomicSaveItemIds] = useState<readonly string[]>([])
  const retainAtomicSave = useCallback((retained: RetainedEnterpriseArchiveAtomicSave | null) => {
    retainedAtomicSaveRef.current = retained
    setRetainedAtomicSaveItemIds(retained ? retained.entries.map(entry => entry.itemId) : [])
  }, [])
  const clearRetainedAtomicSave = useCallback((itemIds?: readonly string[]) => {
    const retained = retainedAtomicSaveRef.current
    if (!retained) return
    if (itemIds) {
      const invalidatedIds = new Set(itemIds)
      if (!retained.entries.some(entry => invalidatedIds.has(entry.itemId))) return
    }
    saveOperationIDsRef.current.delete(retained.operationKey)
    retainAtomicSave(null)
  }, [retainAtomicSave])
  const itemRequestTokensRef = useRef<Map<string, number>>(new Map())
  const replayRequestTokensRef = useRef<Map<string, number>>(new Map())
  const replayNotFoundCountsRef = useRef<Map<string, number>>(new Map())
  const replayStartScopeRef = useRef<{
    itemId: string
    generation: number
    scriptIndex: number
    projectId: string | undefined
    optionsIdentity: string
    script: ScriptDefinition
  } | null>(null)

  const [options, setOptions] = useState<ImportOptions>(DEFAULT_IMPORT_OPTIONS)
  const selectedItem = useMemo(
    () => importItems.find(item => item.id === selectedItemId) || importItems[0] || null,
    [importItems, selectedItemId],
  )
  const selectedImportItemIndex = Math.max(0, importItems.findIndex(item => item.id === selectedItem?.id))
  const queuePageStart = Math.floor(selectedImportItemIndex / ENTERPRISE_QUEUE_PAGE_SIZE) * ENTERPRISE_QUEUE_PAGE_SIZE
  const visibleImportItems = importItems.slice(queuePageStart, queuePageStart + ENTERPRISE_QUEUE_PAGE_SIZE)
  const importItemsRef = useRef(importItems)
  importItemsRef.current = importItems
  const settledSourceSecretSelectionsRef = useRef(new Map<string, SecretBindingSelection[]>())
  const credentialBatchReviewKeysRef = useRef(new Set<string>())
  const discardedSourceSecretSessionsRef = useRef(new Set<string>())
  const sourceSecretSessionDiscardsRef = useRef(new Map<string, Promise<void>>())
  const selectedItemIdRef = useRef(selectedItem?.id || '')
  selectedItemIdRef.current = selectedItem?.id || ''
  const selectedResultScriptIndexRef = useRef(selectedResultScriptIndex)
  selectedResultScriptIndexRef.current = selectedResultScriptIndex
  const reviewProjectIdRef = useRef(projectId)
  reviewProjectIdRef.current = projectId
  const destinationFolderIdRef = useRef(folderId ?? null)
  destinationFolderIdRef.current = folderId ?? null
  const optionsRef = useRef(options)
  optionsRef.current = options
  const sourceSecretSessionKey = useCallback(
    (sourceProjectId: string, sessionId: string) => `${sourceProjectId}\u0000${sessionId}`,
    [],
  )
  const markSourceSecretSessionSettled = useCallback((sourceProjectId: string, sessionId: string) => {
    if (!sourceProjectId || !sessionId) return
    discardedSourceSecretSessionsRef.current.add(sourceSecretSessionKey(sourceProjectId, sessionId))
  }, [sourceSecretSessionKey])
  const discardSourceSecretSession = useCallback((
    sourceProjectId: string,
    sessionId: string,
  ): Promise<void> => {
    if (!sourceProjectId || !sessionId) return Promise.resolve()
    const key = sourceSecretSessionKey(sourceProjectId, sessionId)
    if (discardedSourceSecretSessionsRef.current.has(key)) return Promise.resolve()
    const existing = sourceSecretSessionDiscardsRef.current.get(key)
    if (existing) return existing

    const discard = apiRef.current.discardImportSecretSession({
      project_id: sourceProjectId,
      source_secret_session_id: sessionId,
    }).then(() => {
      discardedSourceSecretSessionsRef.current.add(key)
    }).catch(() => {
      // Source sessions are protected and expire server-side. Keep a failed
      // discard retryable so a later lifecycle boundary can try again.
    }).finally(() => {
      sourceSecretSessionDiscardsRef.current.delete(key)
    })
    sourceSecretSessionDiscardsRef.current.set(key, discard)
    return discard
  }, [sourceSecretSessionKey])
  const discardQueuedSourceSecretSessions = useCallback((
    items: readonly EnterpriseArchiveImportQueueItem[] = importItemsRef.current,
    itemIds?: readonly string[],
  ): Promise<void> => {
    const selectedIds = itemIds ? new Set(itemIds) : null
    const seen = new Set<string>()
    const discards: Promise<void>[] = []
    for (const item of items) {
      if (selectedIds && !selectedIds.has(item.id)) continue
      const sessionId = item.result?.source_secret_session_id || ''
      const sourceProjectId = item.sourceSecretProjectId || reviewProjectIdRef.current || ''
      if (!sessionId || !sourceProjectId) continue
      const key = sourceSecretSessionKey(sourceProjectId, sessionId)
      if (seen.has(key)) continue
      seen.add(key)
      discards.push(discardSourceSecretSession(sourceProjectId, sessionId))
    }
    return Promise.all(discards).then(() => undefined)
  }, [discardSourceSecretSession, sourceSecretSessionKey])
  const fenceOperationScopes = useCallback((itemIds?: readonly string[]) => {
    // A queue-item fence must not invalidate unrelated imports that are already
    // running. Reserve the global generation for whole-surface resets/close.
    if (!itemIds) operationGenerationRef.current += 1
    const ids = itemIds || importItemsRef.current.map(item => item.id)
    ids.forEach(id => {
      nextRequestToken(itemRequestTokensRef.current, id)
      nextRequestToken(replayRequestTokensRef.current, id)
    })
    replayNotFoundCountsRef.current.clear()
    const activeReviewArtifactSave = reviewArtifactSaveInFlightRef.current
    const invalidatesActiveReviewArtifactSave = Boolean(
      activeReviewArtifactSave &&
      (!itemIds || itemIds.includes(activeReviewArtifactSave.itemId)),
    )
    // Fencing a logical review does not cancel an authoring-session POST that
    // already crossed the wire. Leave its physical ref intact until finally.
    return invalidatesActiveReviewArtifactSave
  }, [])
  const invalidateOperationScopes = useCallback((itemIds?: readonly string[]) => {
    const activeAtomicSave = atomicSaveInFlightRef.current
    const retainedAtomicSave = retainedAtomicSaveRef.current
    const invalidatesActiveAtomicSave = Boolean(
      activeAtomicSave
      && retainedAtomicSave
      && (!itemIds || retainedAtomicSave.entries.some(entry => itemIds.includes(entry.itemId))),
    )
    if (!invalidatesActiveAtomicSave) clearRetainedAtomicSave(itemIds)
    void discardQueuedSourceSecretSessions(importItemsRef.current, itemIds)
    fenceOperationScopes(itemIds)
    // A fenced re-conversion of an archive's remaining drafts no longer
    // counts as one, even while its request is still out.
    setRefreshingRemainingDraftsOf(current => current && (!itemIds || itemIds.includes(current.itemId)) ? null : current)
    // A queue-item fence does not stop a running validate/convert batch, and
    // clearing workingMode would re-enable Validate so the operator could fence
    // their own batch from under it. Only a whole-surface fence resets it.
    if (!itemIds && !invalidatesActiveAtomicSave) setWorkingMode(null)
    // Physical save flags remain raised until their corresponding promise
    // settles, even when Back/project/source invalidates the visible review.
    if (!itemIds) {
      const protectedKeys = new Set([
        activeAtomicSave?.operationKey,
        scriptSaveInFlightRef.current?.operationKey,
      ].filter((key): key is string => Boolean(key)))
      for (const key of saveOperationIDsRef.current.keys()) {
        if (!protectedKeys.has(key)) saveOperationIDsRef.current.delete(key)
      }
    } else {
      const invalidatedIDs = new Set(itemIds)
      for (const key of saveOperationIDsRef.current.keys()) {
        const itemID = key.split('\u0000', 1)[0]
        const protectedKey = activeAtomicSave?.operationKey === key
          || scriptSaveInFlightRef.current?.operationKey === key
        if (invalidatedIDs.has(itemID) && !protectedKey) saveOperationIDsRef.current.delete(key)
      }
    }
  }, [clearRetainedAtomicSave, discardQueuedSourceSecretSessions, fenceOperationScopes])
  useEffect(() => () => {
    // Back is not the only way this surface can disappear: project and route
    // transitions may unmount it directly. Fence the refs synchronously so an
    // already-started batch cannot issue its next durable write.
    fenceOperationScopes()
    void discardQueuedSourceSecretSessions(importItemsRef.current)
  }, [discardQueuedSourceSecretSessions, fenceOperationScopes])
  const previousProjectIdRef = useRef(projectId)
  useEffect(() => {
    if (previousProjectIdRef.current === projectId) return
    previousProjectIdRef.current = projectId
    invalidateOperationScopes()
    classpathAssetsByKeyRef.current = {}
    setClasspathAssetsByKey({})
    setSelectedResultScriptIndex(0)
    setImportItems(items => items.map(item => ({
      ...item,
      status: 'queued',
      result: null,
      lastRequiredFileCandidates: [],
      reviewMode: null,
      reviewOptionsIdentity: '',
      error: '',
      notice: '',
      replayValidationLoading: false,
      replayValidationError: '',
      replayValidationWarnings: [],
      replayValidationSession: null,
      replayReviewGeneration: -1,
      replayScriptIndex: -1,
      replayProjectId: '',
      reviewGeneration: item.reviewGeneration + 1,
      savedResultScripts: {},
      sourceSecretProjectId: '',
    })))
  }, [invalidateOperationScopes, projectId])
  const previousFolderIdRef = useRef(folderId ?? null)
  useEffect(() => {
    const nextFolderId = folderId ?? null
    if (previousFolderIdRef.current === nextFolderId) return
    previousFolderIdRef.current = nextFolderId
    if (!atomicSaveInFlightRef.current) clearRetainedAtomicSave()
  }, [clearRetainedAtomicSave, folderId])
  const selectedHARFile = selectedItem?.harFile ?? null
  const selectedSupplementalFiles = selectedItem?.supplementalFiles || EMPTY_SUPPLEMENTAL_FILES
  const result = selectedItem?.result ?? null
  const reviewMode = selectedItem?.reviewMode ?? null
  const reviewMatchesCurrentOptions = !result || selectedItem?.reviewOptionsIdentity === importOptionsIdentity(options)
  const rawReplayValidationLoading = selectedItem?.replayValidationLoading ?? false
  const rawReplayValidationError = selectedItem?.replayValidationError ?? ''
  const rawReplayValidationWarnings = selectedItem?.replayValidationWarnings ?? EMPTY_REPLAY_VALIDATION_WARNINGS
  const rawReplayValidationSession = selectedItem?.replayValidationSession ?? null
  const displayError = error || selectedItem?.error || ''
  const displayNotice = notice || selectedItem?.notice || ''
  const queuedFileCount = importItems.length
  const runningFileCount = importItems.filter(item => enterpriseArchiveImportStatusIsRunning(item.status)).length
  const failedFileCount = importItems.filter(item => item.status === 'failed').length
  const validatedItems = importItems.filter(item => item.status === 'validated')
  const validatedFileCount = validatedItems.length
  const validatedItemIds = validatedItems.map(item => item.id)
  const convertedFileCount = importItems.filter(item => item.status === 'converted').length
  const savedArchiveScripts = collectSavedEnterpriseArchiveScripts(importItems)
  const reviewSessionIsSaved = (item: EnterpriseArchiveImportQueueItem, index: number) =>
    savedReviewSessions[`${item.id}:${index}`] === item.reviewGeneration
  const pendingItemReviewCount = (item: EnterpriseArchiveImportQueueItem) => {
    const partlySavedRunning = (item.status === 'converting' || item.status === 'validating') && Object.keys(item.savedResultScripts || {}).length > 0
    if (item.status !== 'converted' && item.status !== 'validated' && !partlySavedRunning) return 0
    const scripts = getEnterpriseArchiveResultScripts(item.result)
    if (!scripts.length) {
      const artifact = item.result?.review_artifact ?? item.result?.summary?.review_artifact
      return artifact && !reviewSessionIsSaved(item, 0) ? 1 : 0
    }
    return scripts.filter((_script, index) => !enterpriseArchiveResultScriptIsSaved(item, index) && !reviewSessionIsSaved(item, index)).length
  }
  const savedReviewSessionCount = importItems.reduce((count, item) => count + Array.from({ length: Math.max(getEnterpriseArchiveResultScripts(item.result).length, 1) }, (_, index) => index)
    .filter(index => reviewSessionIsSaved(item, index)).length, 0)
  const pendingReviewScriptCount = importItems.reduce((count, item) => count + pendingItemReviewCount(item), 0)
  const validatedAwaitingSaveCount = validatedItems.filter(item => pendingItemReviewCount(item) > 0).length
  // Validated drafts and review decisions live only in this page until saved.
  const pendingItemReviewCountRef = useRef(pendingItemReviewCount)
  pendingItemReviewCountRef.current = pendingItemReviewCount
  const reviewHasPendingDrafts = (items: readonly EnterpriseArchiveImportQueueItem[]) =>
    items.some(item => pendingItemReviewCountRef.current(item) > 0)
  const { markClean: markNavigationGuardClean } = useNavigationGuardRunner()
  useUnsavedWorkGuard(ENTERPRISE_ARCHIVE_REVIEW_GUARD_ID, 'LoadRunner import review', pendingReviewScriptCount > 0, () => {
    invalidateOperationScopes()
    setImportItems([])
    setSelectedItemId('')
  }, () => reviewHasPendingDrafts(importItemsRef.current))
  const pendingValidationFileCount = Math.max(queuedFileCount - validatedFileCount - convertedFileCount, 0)
  const allValidatedReviewsMatchOptions = validatedItems.every(item => item.reviewOptionsIdentity === importOptionsIdentity(options))
  const selectedItemValidated = selectedItem?.status === 'validated'
  const activeImportFileCount = runningFileCount || queuedFileCount
  const importProgressMessage = workingMode === 'validate'
    ? `Validating ${activeImportFileCount} enterprise script archive${activeImportFileCount === 1 ? '' : 's'}. Review will appear when the dry run finishes.`
    : refreshingRemainingDrafts
      ? `Converting the remaining drafts of ${refreshingRemainingDrafts} again. The saved drafts are kept; nothing else is saved.`
    : workingMode === 'convert'
      ? `Processing ${activeImportFileCount} reviewed archive${activeImportFileCount === 1 ? '' : 's'}. Saving eligible scripts and retaining drafts that need attention.`
      : ''
  const failedImportMessage = failedFileCount > 0
    ? `${failedFileCount} enterprise script archive${failedFileCount === 1 ? '' : 's'} failed during import review. Failed queue rows show the error details.`
    : ''
  const validateButtonLabel = queuedFileCount > 1 ? `Validate ${queuedFileCount} Scripts` : 'Validate Script(s)'
  const convertButtonLabel = queuedFileCount > 1 ? 'Save reviewed drafts' : 'Save draft'
  const conversionGateMessage = refreshingRemainingDrafts
    ? 'Converting the remaining drafts again. Nothing is saved until you save them.'
    : workingMode === 'convert'
    ? 'Saving eligible scripts. Archives that need repair, replay, or approval remain in this queue.'
    : queuedFileCount === 0
      ? 'Add an enterprise script archive before validating or converting.'
      : (validatedFileCount > 0)
        ? (allValidatedReviewsMatchOptions
            ? 'Import checks complete. This action saves eligible scripts; archives needing repair, replay, or approval stay in the queue for the next action shown in their review.'
            : 'Conversion options changed. Validate the queued archive again before conversion.')
        : convertedFileCount === queuedFileCount
          ? 'Review the save results below. Converted drafts still require saving to appear in the library.'
          : `${pendingValidationFileCount} archive${pendingValidationFileCount === 1 ? '' : 's'} must pass validation before conversion.`
  const resultScripts = useMemo(() => getEnterpriseArchiveResultScripts(result), [result])
  const activeResultScriptIndex = resultScripts.length > 0
    ? Math.min(selectedResultScriptIndex, resultScripts.length - 1)
    : 0
  selectedResultScriptIndexRef.current = activeResultScriptIndex
  const resultScriptPageStart = Math.floor(activeResultScriptIndex / ENTERPRISE_RESULT_PAGE_SIZE) * ENTERPRISE_RESULT_PAGE_SIZE
  const visibleResultScripts = resultScripts.slice(resultScriptPageStart, resultScriptPageStart + ENTERPRISE_RESULT_PAGE_SIZE)
  useEffect(() => {
    const pendingItemID = pendingQueueItemFocusRef.current
    if (!pendingItemID || pendingItemID !== selectedItem?.id) return
    pendingQueueItemFocusRef.current = null
    queueItemButtonRefs.current.get(pendingItemID)?.focus()
  }, [selectedItem?.id])
  useEffect(() => {
    const pendingIndex = pendingResultScriptFocusRef.current
    if (pendingIndex !== activeResultScriptIndex) return
    pendingResultScriptFocusRef.current = null
    resultScriptButtonRefs.current.get(pendingIndex)?.focus()
  }, [activeResultScriptIndex])
  const replayScopeMatches = Boolean(selectedItem)
    && selectedItem.replayReviewGeneration === selectedItem.reviewGeneration
    && selectedItem.replayScriptIndex === activeResultScriptIndex
    && selectedItem.replayProjectId === (projectId || '')
  const replayValidationLoading = replayScopeMatches ? rawReplayValidationLoading : false
  const replayValidationError = replayScopeMatches ? rawReplayValidationError : ''
  const replayValidationWarnings = replayScopeMatches ? rawReplayValidationWarnings : EMPTY_REPLAY_VALIDATION_WARNINGS
  const replayValidationSession = replayScopeMatches ? rawReplayValidationSession : null
  const reviewScope = `${selectedItem?.id || ''}:${selectedItem?.reviewGeneration ?? -1}:${activeResultScriptIndex}:${projectId || ''}`
  useEffect(() => {
    setValidationReviewTab('overview')
  }, [projectId, selectedItem?.id])
  useEffect(() => {
    const activeSave = reviewArtifactSaveInFlightRef.current
    setReviewArtifactSaving(Boolean(
      activeSave
      && activeSave.itemId === selectedItem?.id
      && activeSave.generation === selectedItem?.reviewGeneration
      && activeSave.scriptIndex === activeResultScriptIndex
      && activeSave.projectId === projectId,
    ))
  }, [activeResultScriptIndex, projectId, reviewScope, selectedItem?.id, selectedItem?.reviewGeneration])
  const importedScript = resultScripts[activeResultScriptIndex] ?? null
  const [protectedReview, setProtectedReview] = useState<{ project: string; result: RepairResult } | null>(null)
  const [issueFocus, setIssueFocus] = useState({ path: '', revision: 0 })
  const [credentialTarget, setCredentialTarget] = useState<HTMLElement | null>(null)
  const [returnToIssue, setReturnToIssue] = useState(false)
  useEffect(() => setReturnToIssue(false), [selectedItem?.id, selectedResultScriptIndex, projectId])
  const archiveSetupRef = useRef<HTMLDetailsElement>(null)
  const [categoryFocus, setCategoryFocus] = useState<{ category: IssueCategory | 'all'; revision: number }>({ category: 'all', revision: 0 })
  const currentRepairReview = protectedReview?.project === (projectId || '') && protectedReview.result.candidate_id === importedScript?.metadata?.import?.preserved?.server_import_attestation_v1 ? protectedReview.result : null
  const replayPreflight = getEnterpriseArchiveReplayPreflight(importedScript, result)
  const selectedResultScriptSaved = Boolean(
    selectedItem
    && enterpriseArchiveResultScriptIsSaved(selectedItem, activeResultScriptIndex),
  )
  // Saves, conversions, and draft mutations consume the selected protected
  // candidate. The guided-repair inspection waits for them instead of racing
  // the consumer (and then reporting a stale candidate beside a good save).
  // Work on other queued archives never touches this candidate.
  const guidedRepairInspectionPaused = selectedResultScriptSaved
    || Boolean(selectedItem && enterpriseArchiveImportStatusIsRunning(selectedItem.status))
    || scriptSaveInProgress
    || atomicSaveInProgress
    || editorActionPreparing
    || internalReviewBusy
    || reviewArtifactSaving
    || reviewArtifactPersistenceInProgress
  // Source-secret sessions may be archive-wide and single-use, but requirements
  // and editor/save readiness are script-specific. A credential needed by one
  // generated lane must not block an unrelated lane.
  const archiveSecretBindingRequirements = result?.secret_binding_requirements || []
  const secretBindingRequirements = importedScript
    ? secretBindingRequirementsForScript(archiveSecretBindingRequirements, activeResultScriptIndex, importedScript)
    : []
  const credentialBatchReviewKey = `${selectedItem?.id || ''}:${selectedItem?.reviewGeneration ?? -1}`
  if (
    selectedItem
    && resultScripts.length > 1
    && archiveSecretBindingRequirements.some(requirement => requirement.destination_value_preserved !== true)
  ) {
    credentialBatchReviewKeysRef.current.add(credentialBatchReviewKey)
  }
  const pendingResultScriptIndices = resultScripts.flatMap((_script, index) => (
    selectedItem && enterpriseArchiveResultScriptIsSaved(selectedItem, index) ? [] : [index]
  ))
  const selectedNeedsCredentialPreparation = secretBindingRequirements.some(
    requirement => requirement.destination_value_preserved !== true,
  )
  const otherPendingCredentialRequirements = pendingResultScriptIndices
    .filter(index => index !== activeResultScriptIndex)
    .flatMap(index => secretBindingRequirementsForScript(
      archiveSecretBindingRequirements,
      index,
      resultScripts[index],
    ))
    .filter(requirement => requirement.destination_value_preserved !== true)
  const enterpriseCredentialBatchFlow = credentialBatchReviewKeysRef.current.has(credentialBatchReviewKey)
    && pendingResultScriptIndices.length > 1
  const enterprisePrepareSelectedForBatch = enterpriseCredentialBatchFlow
    && otherPendingCredentialRequirements.length > 0
  const editRequiresCredentialPreservingSave = credentialPreservingImportDraftRequiresSave(
    result,
    importedScript,
    secretBindingRequirements,
  )
  const latestPreparedReviewRef = useRef({
    itemId: selectedItem?.id || '',
    generation: selectedItem?.reviewGeneration ?? -1,
    scriptIndex: activeResultScriptIndex,
    projectId,
    optionsIdentity: selectedItem?.reviewOptionsIdentity || '',
    importedScript,
    result,
    secretBindingRequirements,
  })
  latestPreparedReviewRef.current = {
    itemId: selectedItem?.id || '',
    generation: selectedItem?.reviewGeneration ?? -1,
    scriptIndex: activeResultScriptIndex,
    projectId,
    optionsIdentity: selectedItem?.reviewOptionsIdentity || '',
    importedScript,
    result,
    secretBindingRequirements,
  }
  const detectedProtocol = result?.detected_protocol ?? result?.summary?.detected_protocol ?? null
  const reviewArtifact = result?.review_artifact ?? result?.summary?.review_artifact ?? null
  const dependencyBundle = javaDependencyInventory(reviewArtifact?.metadata)?.classification === 'java_dependency_bundle'
  const fidelityReport = result?.fidelity_report ?? result?.summary?.fidelity_report ?? null
  const currentRequiredFileCandidates = useMemo(
    () => parseRequiredJavaFilesFromFidelityBlockers(fidelityReport?.blockers || []),
    [fidelityReport?.blockers],
  )
  const requiredFileCandidates = useMemo(
    () => result ? currentRequiredFileCandidates : (selectedItem?.lastRequiredFileCandidates || []),
    [currentRequiredFileCandidates, result, selectedItem?.lastRequiredFileCandidates],
  )
  const requiredFileReviewIsStale = !result && requiredFileCandidates.length > 0
  const matchedRequiredFileKeys = useMemo(() => {
    const availableNames = new Set(
      selectedSupplementalFiles.map(file => file.name.trim().toLowerCase()).filter(Boolean),
    )
    return new Set(
      requiredFileCandidates
        .filter((candidate) => {
          const expectedFileName = `${javaSimpleClassName(candidate.className)}.java`.toLowerCase()
          const expectedPath = candidate.suggestedPath.toLowerCase()
          return availableNames.has(expectedFileName) || availableNames.has(expectedPath)
        })
        .map(candidate => candidate.className.toLowerCase()),
    )
  }, [requiredFileCandidates, selectedSupplementalFiles])
  // Scope for the client-side .txt -> .java rename, mirroring the server's
  // archiveHasJavaSourceSignal. Renaming outside a Java import would hand the
  // server a .java file that satisfies its own scope gate, diverting a non-Java
  // archive into the Java converter.
  const supplementalRenameHasJavaContext = Boolean(
    detectedProtocol?.apexload_protocol === 'java_vuser' ||
    detectedProtocol?.canonical_source_protocol === 'java_vuser' ||
    requiredFileCandidates.length > 0,
  )
  const executableImport = !detectedProtocol || detectedProtocol.import_mode === 'executable'
  const importMetadata = getImportMetadata(importedScript || undefined)
  const trueClient2CompatReport = getTrueClient2CompatReport(result, importMetadata)
  // Re-normalizing the whole preserved-import blob on every keystroke in the
  // review form is pure waste: it only changes when the imported script does.
  const importSummary = useMemo(() => getImportSummary(importMetadata), [importMetadata])
  const reviewSummary: EnterpriseArchiveImportSummary | null = result?.summary || null
  const salesforceReview = reviewSummary?.salesforce_review || null
  const salesforceNativeScript = salesforceReview?.native_preview?.script || null
  const sourceReview = reviewSummary?.source_review || getSourceReviewFromMetadata(importMetadata)
  const runtimeFiles = getSummaryList(reviewSummary?.runtime_files).length > 0
    ? getSummaryList(reviewSummary?.runtime_files)
    : importSummary.runtimeFiles
  const parameterFiles = getSummaryList(reviewSummary?.parameter_files).length > 0
    ? getSummaryList(reviewSummary?.parameter_files)
    : importSummary.parameterFiles
  const unsupportedFunctions = result?.unsupported_elements.length
    ? result.unsupported_elements
    : importSummary.unsupportedFunctions
  const reviewSections = useMemo(() => result?.review_sections || [], [result?.review_sections])
  const repairSuggestions = result?.repair_suggestions || []
  const needsExecutionRepair = currentRepairReview ? !currentRepairReview.readiness.replay_ready : Boolean((detectedProtocol && detectedProtocol.import_mode !== 'executable') || fidelityReport?.promotion_status === 'blocked'
    || result?.summary?.migration_readiness?.blocked
    || (replayPreflight?.valid === false
    || replayPreflight?.checks?.some(check => check.status === 'fail')
    || replayPreflight?.missing_secrets?.length)
    || reviewSections.some(section => section.severity === 'blocker'))
  const readinessMessage = needsExecutionRepair
    ? 'Imported — repairs needed before replay.'
    : 'Imported — ready for review. Target replay is not verified.'
  const additionalRepairFindings = useMemo<ImportFinding[]>(() => {
    const canonical = currentRepairReview?.findings || (importedScript ? draftImportFindings(importedScript) : [])
    const codes = new Set(canonical.map(f => f.code))
    const extra: ImportFinding[] = []
    // Older/unprotected imports still expose archive-level gates. Keep them visible
    // without rendering another copy of action-level findings.
    for (const [index, detail] of (currentRepairReview?.fidelity_report.blockers || fidelityReport?.blockers || []).entries()) {
      if ([...codes].some(code => detail.includes(code)) || (codes.has('restored_body_required') && /lr_unmask|secret.gated/i.test(detail))) continue
      extra.push({ code: `diagnostic:archive:${index}`, detail, gate: 'execution' })
    }
    const diagnosticSections = currentRepairReview ? reviewSections.filter(section => ['warnings', 'conversion_warnings'].includes(section.key)) : reviewSections
    const warningFallback = result?.warnings?.length ? [{ key: 'warnings', title: 'Conversion warnings', severity: 'warning', summary: '', items: result.warnings }] : []
    extra.push(...reviewSectionFindings([...diagnosticSections, ...warningFallback], sourceReview || undefined).filter(finding => finding.gate !== 'execution' || !canonical.length))
    // Replay sends a missing variable as placeholder text unless the variable-readiness check fails.
    const variablesBlocked = replayPreflight?.checks?.some(check => check.id === 'variables' && check.status === 'fail')
    if (!currentRepairReview) for (const variable of replayPreflight?.missing_variables || []) {
      if (!codes.has(`missing_producer:${variable}`)) extra.push({ code: `missing_producer:${variable}`, detail: `Missing variable: ${variable}`, gate: variablesBlocked ? 'execution' : 'promotion' })
    }
    const bindings = currentRepairReview?.readiness.unresolved_bindings ?? replayPreflight?.missing_secrets?.length ?? 0
    if (bindings) extra.push({ code: 'diagnostic:credential_bindings', detail: `${bindings} credential requirements remain unresolved. Review project secret bindings and protected body requirements. Counts may include several requirements for one request.`, gate: 'execution' })
    if (currentRepairReview?.readiness.validation_issues?.length) extra.push({ code: 'diagnostic:validation', detail: currentRepairReview.readiness.validation_issues.join('\n'), gate: 'execution' })
    if (needsExecutionRepair && !canonical.some(f => f.gate === 'execution') && !extra.some(f => f.gate === 'execution')) extra.push({ code: 'diagnostic:execution', detail: 'Additional protocol or execution checks need attention. Inspect conversion details and the related review sections before replay.', gate: 'execution' })
    return extra
  }, [currentRepairReview, importedScript, fidelityReport, reviewSections, sourceReview, replayPreflight, needsExecutionRepair, result?.warnings])
  const allTabIssues = useMemo(() => importedScript ? groupImportFindings([...(currentRepairReview?.findings || draftImportFindings(importedScript)), ...additionalRepairFindings], importedScript) : [], [currentRepairReview, importedScript, additionalRepairFindings])
  // Review marks live in the guided repair panel; notes marked reviewed there
  // stop counting as open review work in the tab badges and readiness line.
  const guidedRepairScope = `${selectedItem?.id}:${selectedResultScriptIndex}:${projectId}`
  const [reviewedRepairIssues, setReviewedRepairIssues] = useState<{ scope: string; ids: string[] }>({ scope: '', ids: [] })
  const reviewedRepairIssueIds = useMemo(() => new Set(reviewedRepairIssues.scope === guidedRepairScope ? reviewedRepairIssues.ids : []), [reviewedRepairIssues, guidedRepairScope])
  const tabIssues = useMemo(() => allTabIssues.filter(issue => issue.blocking || !reviewedRepairIssueIds.has(issue.id)), [allTabIssues, reviewedRepairIssueIds])
  const reviewedTabIssueCount = allTabIssues.length - tabIssues.length
  const selectReviewSection = (tab: ValidationReviewTab) => {
    setValidationReviewTab(tab)
    document.getElementById(`lr-validation-review-tab-${tab}`)?.focus()
    document.getElementById(`lr-validation-review-tab-${tab}`)?.scrollIntoView({ block: 'center' })
  }
  const selectValidationReviewTab = (tab: ValidationReviewTab) => {
    const pageScroller = document.querySelector<HTMLElement>('.main-content')
    const scrollingElement = document.scrollingElement
    const scrollLeft = pageScroller?.scrollLeft ?? scrollingElement?.scrollLeft ?? window.scrollX
    const scrollTop = pageScroller?.scrollTop ?? scrollingElement?.scrollTop ?? window.scrollY
    const reviewPanel = document.getElementById('lr-validation-review-panel')
    if (reviewPanel && pageScroller) {
      const panelTop = reviewPanel.getBoundingClientRect().top - pageScroller.getBoundingClientRect().top + pageScroller.scrollTop
      const minimumPanelHeight = Math.max(
        reviewPanel.offsetHeight,
        pageScroller.scrollTop + pageScroller.clientHeight - panelTop,
      )
      setValidationReviewPanelMinHeight(currentHeight => Math.max(currentHeight, Math.ceil(minimumPanelHeight)))
    }
    setValidationReviewTab(tab)
    // Credentials can be shorter than the issue review, which otherwise
    // clamps the panel scroller before its position can be restored.
    const restoreScroll = () => {
      if (pageScroller) pageScroller.scrollTo({ left: scrollLeft, top: scrollTop, behavior: 'auto' })
      else window.scrollTo({ left: scrollLeft, top: scrollTop, behavior: 'auto' })
    }
    requestAnimationFrame(() => requestAnimationFrame(restoreScroll))
  }
  const reviewCategory: IssueCategory | 'all' = validationReviewTab === 'credentials' ? 'credentials' : validationReviewTab === 'data' ? 'values' : validationReviewTab === 'preserved' ? 'runtime' : 'all'
  const sectionIssueCount = tabIssues.filter(issue => reviewCategory === 'all' || issue.category === reviewCategory).length
  const openReviewIssues = () => {
    setCategoryFocus(previous => ({ category: reviewCategory, revision: previous.revision + 1 }))
    selectReviewSection('overview')
  }
  const tlsRepairIssue = allTabIssues.find(issue => issue.findings.some(finding => finding.code === 'legacy_tls_replacement'))
  const supportMatrixSections = summarizeEnterpriseArchiveSupportMatrix(supportMatrix)
  const hasRuntimeCoverage = supportMatrix.length > 0
  useModalDialog({
    dialogRef: runtimeCoverageDialogRef,
    enabled: runtimeCoverageOpen && hasRuntimeCoverage,
    initialFocusRef: runtimeCoverageCloseButtonRef,
    onClose: () => setRuntimeCoverageOpen(false),
  })
  const protocolCoverageSections = summarizeEnterpriseArchiveProtocolSupport(protocolSupportMatrix)
  const javaIdiomSections = [
    { key: 'http', title: 'HTTP Idioms', items: javaHTTPIdiomMatrix },
    { key: 'jms', title: 'JMS Idioms', items: javaJMSIdiomMatrix },
    { key: 'jdbc', title: 'JDBC Idioms', items: javaJDBCIdiomMatrix },
  ].filter(section => section.items.length > 0)
  const migrationReadiness: EnterpriseArchiveMigrationReadiness | null = reviewSummary?.migration_readiness || null
  const reviewedMigrationPolicy = migrationReadiness?.policy?.block_statuses
    ? buildMigrationPolicyOptions(migrationReadiness.policy.block_statuses)
    : null
  const hasPendingMigrationPolicyChanges = reviewedMigrationPolicy !== null && !sameMigrationBlockPolicy(options, reviewedMigrationPolicy)
  const saveBlockedByMigrationPolicy = Boolean(migrationReadiness?.blocked)
  const saveBlockedByProtocolGate = Boolean(detectedProtocol && detectedProtocol.import_mode !== 'executable')
  const externalRunnerGate = detectedProtocol?.import_mode === 'external_runner_required'
  const combinedJavaVuserGate = externalRunnerGate && (
    detectedProtocol?.apexload_protocol === 'java_vuser' ||
    detectedProtocol?.canonical_source_protocol === 'java_vuser' ||
    importedScript?.protocol === 'java_vuser'
  )
  const combinedJavaVuserApprovalReady = Boolean(
    combinedJavaVuserGate &&
    fidelityReport?.promotion_status === 'promotable' &&
    (fidelityReport.blockers?.length || 0) === 0,
  )
  const javaClasspathUploadEnabled = isJavaClasspathUploadEnabled()
  const currentClasspathKey = javaClasspathReviewKey(projectId, selectedItem?.id || '', activeResultScriptIndex)
  const currentClasspathEntries = importedScript?.protocol === 'java_vuser'
    ? (importedScript.config?.java_vuser?.classpath ?? [])
    : []
  const currentClasspathAssets = classpathAssetsByKey[currentClasspathKey]
    ?? importedScript?.config?.java_vuser?.classpath_assets
    ?? []
  const currentAttachedCount = currentClasspathAssets.filter(asset => currentClasspathEntries.includes(asset.entry)).length
  const showClasspathResolver = javaClasspathUploadEnabled
    && importedScript?.protocol === 'java_vuser'
    && currentClasspathEntries.length > 0
  const classpathReviewIsCurrent = () => {
    const currentItem = importItemsRef.current.find(item => item.id === selectedItem?.id)
    return Boolean(selectedItem && importedScript)
      && reviewProjectIdRef.current === projectId
      && selectedItemIdRef.current === selectedItem?.id
      && selectedResultScriptIndexRef.current === activeResultScriptIndex
      && currentItem?.reviewGeneration === selectedItem?.reviewGeneration
      && currentItem?.reviewOptionsIdentity === selectedItem?.reviewOptionsIdentity
      && getEnterpriseArchiveResultScripts(currentItem?.result || null)[activeResultScriptIndex] === importedScript
  }
  const setCurrentClasspathAssets = (assets: JavaClasspathAsset[]) => {
    if (!classpathReviewIsCurrent() || !selectedItem
      || classpathUploadInFlightRef.current || reviewArtifactSaveInFlightRef.current
      || scriptSaveInFlightRef.current || atomicSaveInFlightRef.current) return
    const filtered = assets.filter(asset => currentClasspathEntries.includes(asset.entry))
    const nextAssets = { ...classpathAssetsByKeyRef.current, [currentClasspathKey]: filtered }
    classpathAssetsByKeyRef.current = nextAssets
    setClasspathAssetsByKey(nextAssets)
    // A changed dependency produces a different governed draft. Advance the
    // review identity so a prior authoring-session receipt cannot hide the
    // change or collide with the next idempotent save.
    const nextGeneration = selectedItem.reviewGeneration + 1
    latestPreparedReviewRef.current = { ...latestPreparedReviewRef.current, generation: nextGeneration }
    setImportItems(items => items.map(item => item.id === selectedItem.id ? {
      ...item,
      reviewGeneration: nextGeneration,
      notice: 'Java dependency bindings changed. Save the updated draft for approval.',
    } : item))
  }
  const handleUploadClasspathJar = async (file: File, entry: string): Promise<JavaClasspathAsset> => {
    if (!projectId || !classpathReviewIsCurrent() || sourceMutationLockedRef.current
      || workingMode !== null || classpathUploadInFlightRef.current) {
      throw new Error('Finish the current import operation before attaching a JAR.')
    }
    const scope = {}
    const operationGeneration = operationGenerationRef.current
    classpathUploadInFlightRef.current = scope
    setClasspathUploadInProgress(true)
    try {
      const response = await api.uploadJavaClasspathJar(selectedItem!.id, file, { project_id: projectId, entry })
      if (operationGenerationRef.current !== operationGeneration || !classpathReviewIsCurrent()) {
        throw new Error('The import review changed during upload. Attach the JAR again in the current review.')
      }
      return {
        entry,
        file_name: response.file_name,
        sha256: response.sha256,
        size_bytes: response.size_bytes,
        content_type: response.content_type,
        storage_ref: response.storage_ref,
      }
    } finally {
      if (classpathUploadInFlightRef.current === scope) {
        classpathUploadInFlightRef.current = null
        setClasspathUploadInProgress(false)
      }
    }
  }
  const importedEditorAvailable = Boolean(onEdit) && !combinedJavaVuserGate
  const protocolGateName = detectedProtocol?.display_name || detectedProtocol?.canonical_source_protocol || 'This protocol'
  const protocolGateRunnerRequirement = detectedProtocol?.runner_requirement || reviewArtifact?.protocol?.runner_requirement || ''
  const protocolGateRequiredEvidence = Array.from(new Set([
    ...(reviewArtifact?.required_evidence || []),
    ...(detectedProtocol?.required_evidence || []),
  ]))
  const protocolGateDetails = [
    protocolGateRunnerRequirement ? `Runner: ${protocolGateRunnerRequirement}` : '',
    reviewArtifact?.title ? `Review artifact: ${reviewArtifact.title}` : '',
    ...protocolGateRequiredEvidence.map(evidence => `Required evidence: ${evidence}`),
    ...(combinedJavaVuserGate && !combinedJavaVuserApprovalReady
      ? (fidelityReport?.blockers || []).map(blocker => `Fidelity blocker: ${blocker}`)
      : []),
    ...(detectedProtocol?.signals || []).slice(0, 3).map(signal => `Signal: ${signal}`),
  ].filter(Boolean)
  const browserImport = isBrowserImport(importedScript)
  const browserReplayStatus = replayValidationSession?.status || null
  const browserReplayEvidence = useMemo(
    () => summarizeBrowserReplayEvidence(replayValidationSession),
    [replayValidationSession],
  )
  const browserReplayStarting = browserImport && replayValidationLoading
  const browserReplayStatusLabel = browserReplayStarting
    ? 'Starting'
    : formatRuntimeSettingStatusLabel(browserReplayStatus || 'not run')
  const browserReplayWasStarted = browserImport && replayValidationSession !== null
  const browserReplayInProgress = browserImport && (browserReplayStarting || browserReplayStatus === 'queued' || browserReplayStatus === 'running' || browserReplayStatus === 'paused')
  const browserReplayNeedsRerun = browserImport && !browserReplayInProgress && (replayValidationSession === null || browserReplayStatus === 'failed' || browserReplayStatus === 'stopped')
  const saveRequiresBrowserReplay = executableImport && browserReplayNeedsRerun
  const primarySaveDisabled = saveBlockedByProtocolGate
    ? (!reviewArtifact || reviewArtifactSaving || !reviewMatchesCurrentOptions)
    : (
        selectedResultScriptSaved
        || saveBlockedByMigrationPolicy
        || replayValidationLoading
        || browserReplayInProgress
        || !reviewMatchesCurrentOptions
      )
  const reviewSaveDisabled = classpathUploadInProgress || (reviewMode === 'validation'
    ? (!selectedItemValidated || !reviewMatchesCurrentOptions || workingMode !== null || (saveBlockedByProtocolGate && (!reviewArtifact || reviewArtifactSaving)))
    : (primarySaveDisabled || workingMode !== null))
  const savedReviewScript = reviewMode !== 'validation' && !saveBlockedByProtocolGate && selectedResultScriptSaved && selectedItem
    ? selectedItem.savedResultScripts[activeResultScriptIndex]
    : undefined
  const reviewSaveDisabledReason = !reviewSaveDisabled
    ? undefined
    : classpathUploadInProgress
      ? 'Wait for the Java dependency upload to finish before saving.'
      : workingMode !== null
      ? 'Wait for the current archive operation to finish before starting another save or conversion.'
      : !reviewMatchesCurrentOptions
        ? 'Conversion options changed after this review was generated. Validate the archive again to refresh the review.'
        : reviewMode === 'validation' && !selectedItemValidated
          ? 'The selected archive must pass validation before it can be converted.'
          : saveBlockedByProtocolGate && reviewArtifactSaving
            ? 'Wait for the protocol review artifact to finish saving.'
            : saveBlockedByProtocolGate && !reviewArtifact
              ? 'Generate the required protocol review artifact before saving this governed import.'
              : selectedResultScriptSaved
                ? 'This generated script has already been saved. Select an unsaved archive result to continue.'
                : saveBlockedByMigrationPolicy
                  ? 'Resolve the migration readiness blockers or relax the Migration Block Policy in Conversion Options before saving.'
                  : replayValidationLoading || browserReplayInProgress
                    ? 'Wait for the draft browser replay to finish before saving this script.'
                    : 'Complete the required archive review choices before saving.'
  const saveActionLabel = saveBlockedByProtocolGate
    ? (reviewArtifactSaving
      ? (combinedJavaVuserApprovalReady ? 'Saving combined Java draft…' : 'Saving review artifact…')
      : (combinedJavaVuserApprovalReady ? 'Save combined Java draft for approval' : 'Save review artifact instead'))
    : (reviewMode === 'validation'
      ? (queuedFileCount > 1 ? 'Save reviewed drafts' : browserImport ? 'Continue to browser replay' : 'Save draft')
      : (selectedResultScriptSaved
      ? 'Saved to Script Library'
      : enterprisePrepareSelectedForBatch
        ? (selectedNeedsCredentialPreparation
            ? `Prepare Credentials for Script ${activeResultScriptIndex + 1} of ${resultScripts.length}`
            : 'Review Required Credentials Before Atomic Save')
      : enterpriseCredentialBatchFlow
        ? `Save remaining ${pendingResultScriptIndices.length} scripts atomically`
      : (browserReplayInProgress
      ? 'Draft Browser Replay In Progress'
      : (saveRequiresBrowserReplay
          ? (browserReplayWasStarted ? 'Rerun Draft Browser Replay Before Saving' : 'Start Draft Browser Replay Before Saving')
          : needsExecutionRepair ? 'Save draft — execution blocked' : 'Save to Script Library'))))
  const replayActionLabel = replayValidationLoading
    ? 'Starting Draft Browser Replay…'
    : (browserReplayInProgress
        ? 'Draft Browser Replay In Progress'
      : (browserReplayWasStarted ? 'Rerun Draft Browser Replay' : 'Start Draft Browser Replay'))
  const replayPanelDescription = browserReplayInProgress
    ? 'Wait for the draft browser replay to finish before saving it to the library.'
    : (saveRequiresBrowserReplay
      ? `${browserReplayWasStarted ? 'Rerun' : 'Run'} a draft browser replay against the converted browser script before saving it to the library.`
      : 'The latest draft browser replay completed. Review the draft browser replay results before saving it to the library.')
  const reviewSubtitle = reviewMode === 'validation'
    ? (saveBlockedByMigrationPolicy
        ? 'This preview came from the dry-run validator. Conversion can continue, but saving will remain blocked until migration readiness blockers are resolved or the Migration Block Policy in Conversion Options is relaxed.'
        : (browserReplayInProgress
        ? 'This preview came from the dry-run validator. Wait for the draft browser replay to finish before saving it to the library.'
            : (saveBlockedByProtocolGate
          ? (externalRunnerGate
              ? (combinedJavaVuserGate && !combinedJavaVuserApprovalReady
                ? `This preview came from the dry-run validator. Save the combined Java Vuser review artifact and resolve its fidelity blockers before authoring approval.`
                : `This preview came from the dry-run validator. ${protocolGateName} execution requires ${protocolGateRunnerRequirement || 'an approved external runner'}; save ${combinedJavaVuserApprovalReady ? 'the combined draft' : 'the review artifact'} for approval.`)
              : 'This preview came from the dry-run validator. Save the review artifact before script promotion.')
              : browserImport ? 'This preview came from the dry-run validator. Continue to conversion and required draft browser replay before saving.' : 'This preview came from the dry-run validator. Convert the validated archive to save the generated script.')))
    : (saveBlockedByMigrationPolicy
        ? 'Review the generated script, preserved metadata, and warnings. Saving is disabled until the migration readiness blockers are resolved or the Migration Block Policy in Conversion Options is relaxed.'
        : (browserReplayInProgress
        ? 'Review the generated script, preserved metadata, and warnings. Wait for the draft browser replay to finish before saving it to the library.'
            : (saveBlockedByProtocolGate
          ? (externalRunnerGate
              ? (combinedJavaVuserGate && !combinedJavaVuserApprovalReady
                ? 'Review the combined Java Vuser draft, save it as a review artifact, and resolve every fidelity blocker before authoring approval.'
                : `Review the generated ${protocolGateName} draft and save ${combinedJavaVuserApprovalReady ? 'the combined source-order draft' : 'the review artifact'} for approval; execution requires ${protocolGateRunnerRequirement || 'an approved external runner'}.`)
              : 'Review the protocol classification and save the review artifact; this import is not executable yet.')
              : (saveRequiresBrowserReplay
          ? `Review the generated script, preserved metadata, and warnings. ${browserReplayWasStarted ? 'Rerun' : 'Run'} a draft browser replay before saving it to the library.`
                : 'Review the generated script, preserved metadata, and warnings before saving.'))))
  const runtimeBlockingSettings = importSummary.runtimeSettingReadiness?.blocking_settings || []
  // summarizeInlineData parses every inline data source, so keep it keyed to the
  // imported script rather than re-running it on unrelated review-form state.
  const generatedDataSources = useMemo(() => importedScript
    ? (importedScript.data_sources || []).map((source, index) => {
      const summary = summarizeInlineData(source)
      const columns = source.first_row_header === false
        ? source.columns || []
        : []
      return {
        name: source.name || `source_${index + 1}`,
        type: source.type || 'csv',
        rows: summary.rows,
        columns: columns.length > 0 ? columns : summary.columns,
      }
    })
    : [], [importedScript])
  // These scans only need the first hit, so stop at it instead of walking every
  // correlation suggestion (and every action behind it) on each render.
  const firstRuntimeBlockingArea = findFirstImportedRuntimeArea(runtimeBlockingSettings)
  const firstRuntimeParityArea = findFirstImportedRuntimeArea(importSummary.runtimeSettingsParity)
  const firstMappedParameterDataSourceName = useMemo(() => {
    for (const section of importSummary.parameterFileSections) {
      const sourceName = findImportedParameterMappingDataSourceName(generatedDataSources, section.values)
      if (sourceName) return sourceName
    }
    return null
  }, [generatedDataSources, importSummary.parameterFileSections])
  const firstGeneratedDataSourceName = generatedDataSources[0]?.name || null
  const firstUnsupportedTargetAction = repairSuggestions
    .find((suggestion) => suggestion.category === 'unsupported' && Boolean(suggestion.target_action?.trim()))
    ?.target_action?.trim() || null
  const actionWrapStyle: CSSProperties = { marginTop: 12, justifyContent: 'flex-start', flexWrap: 'wrap' }
  const sectionActionStyle: CSSProperties = { marginBottom: 12, justifyContent: 'flex-start', flexWrap: 'wrap' }

  const updateImportItem = (itemId: string, patch: Partial<EnterpriseArchiveImportQueueItem>) => {
    setImportItems(items => items.map(item => item.id === itemId ? { ...item, ...patch } : item))
  }

  const publishSavedEnterpriseArchiveScripts = async (
    nextItems: EnterpriseArchiveImportQueueItem[],
    newlySavedScripts: StoredScript[],
    pendingNotice: string,
    completedNotice: string,
    requestIsCurrent: () => boolean,
    destination: ImportSaveDestination = 'stay',
  ) => {
    // Keep the ref in lockstep with the committed snapshot because the caller
    // may immediately choose the next pending generated lane before React
    // renders this state update.
    importItemsRef.current = nextItems
    setImportItems(nextItems)
    const pending = findFirstPendingEnterpriseArchiveSelection(nextItems)
    if (pending) {
      setSelectedItemId(pending.itemId)
      setSelectedResultScriptIndex(pending.scriptIndex)
      setNotice(pendingNotice)
      if (newlySavedScripts.length > 0) {
        onImported?.({
          scripts: newlySavedScripts,
          stayInImportReview: true,
        })
      }
      return
    }

    const allSavedScripts = collectSavedEnterpriseArchiveScripts(nextItems)
    if (allSavedScripts.length > 0 && onImported) {
      // The durable script commit is the user-visible outcome. Publish it
      // before best-effort source-session cleanup so navigation or a slow
      // cleanup endpoint cannot hide a confirmed save from the library.
      onImported({
        scripts: allSavedScripts,
        ...(destination === 'stay' || allSavedScripts.length > 1 || !requestIsCurrent() ? { stayInImportReview: true } : {}),
      })
      if (requestIsCurrent()) setNotice(completedNotice)
      void discardQueuedSourceSecretSessions(nextItems)
      return
    }
    void discardQueuedSourceSecretSessions(nextItems)
    if (requestIsCurrent()) setNotice(completedNotice)
  }

  const replaceOptionsAndInvalidate = (nextOptions: ImportOptions) => {
    if (sourceMutationLockedRef.current) return
    invalidateOperationScopes()
    optionsRef.current = nextOptions
    setOptions(nextOptions)
    setImportItems(items => items.map(item => enterpriseArchiveImportStatusIsRunning(item.status)
      ? {
          ...item,
          status: 'queued',
          result: null,
          reviewMode: null,
          reviewOptionsIdentity: '',
          error: '',
          notice: '',
          replayValidationLoading: false,
          replayValidationError: '',
          replayValidationWarnings: [],
          replayValidationSession: null,
          replayReviewGeneration: -1,
          replayScriptIndex: -1,
          replayProjectId: '',
          reviewGeneration: item.reviewGeneration + 1,
          savedResultScripts: {},
          sourceSecretProjectId: '',
        }
      : { ...item, replayValidationLoading: false }))
  }

  const prepareImportedScriptForEditor = async (): Promise<ScriptDefinition | null> => {
    const preparedReview = latestPreparedReviewRef.current
    if (!preparedReview.importedScript) return null
    if (preparedReview.secretBindingRequirements.length === 0) {
      return preparedReview.importedScript
    }
    if (editorActionInFlightRef.current) return null

    const sourceItemId = selectedItem?.id || ''
    const sourceGeneration = selectedItem?.reviewGeneration
    const sourceScriptIndex = activeResultScriptIndex
    const sourceProjectId = reviewProjectIdRef.current
    const requestIsCurrent = () => {
      const current = importItemsRef.current.find(item => item.id === sourceItemId)
      return Boolean(sourceItemId)
        && current?.reviewGeneration === sourceGeneration
        && selectedItemIdRef.current === sourceItemId
        && selectedResultScriptIndexRef.current === sourceScriptIndex
        && reviewProjectIdRef.current === sourceProjectId
    }

    editorActionInFlightRef.current = true
    setEditorActionPreparing(true)
    setError('')
    try {
      if (!credentialReviewRef.current) {
        setError('Credential preparation is unavailable for this imported draft.')
        return null
      }
      const committed = await credentialReviewRef.current.commit()
      if (!committed || !requestIsCurrent()) return null
      const currentReview = latestPreparedReviewRef.current
      if (
        currentReview.secretBindingRequirements.length > 0
        && !requirementsPreserveDestinationValues(currentReview.secretBindingRequirements)
      ) return null
      return currentReview.importedScript
    } finally {
      editorActionInFlightRef.current = false
      setEditorActionPreparing(false)
    }
  }

  const editorBlockedByUnconfirmedSave = Boolean(selectedItem && retainedAtomicSaveItemIds.includes(selectedItem.id))
  const editorBlocked = editorBlockedByBatch || editorBlockedByUnconfirmedSave
  const editorBlockedReason = classpathUploadInProgress
    ? 'Wait for the Java dependency upload to finish before editing.'
    : refreshingRemainingDrafts
      ? 'Wait for the remaining drafts to be converted again before editing.'
      : editorBlockedByBatch
        ? 'Wait for the current validation, conversion or save to finish before editing.'
        : 'An earlier save of this archive is unconfirmed. Convert again to reconcile it before editing.'
  const openImportedScriptInEditor = (focus?: DraftEditorFocus) => {
    // A draft saved from the editor is recorded here, so none opens while a
    // validate or save batch is running and could overwrite that record.
    if (!onEdit || combinedJavaVuserGate || editorBlocked) return
    if (editRequiresCredentialPreservingSave) {
      if (!onImported) {
        setError('Save this credential-preserving import before opening it in the Visual Editor.')
        return
      }
      if (reviewMode === 'validation') {
        void convertValidatedItems(validatedItemIds, 'editor')
      } else {
        void handleSaveConverted('editor')
      }
      return
    }
    const sourceItemId = selectedItem?.id || ''
    const sourceGeneration = selectedItem?.reviewGeneration
    const sourceScriptIndex = activeResultScriptIndex
    void (async () => {
      const currentScript = await prepareImportedScriptForEditor()
      if (currentScript) {
        void discardQueuedSourceSecretSessions(
          importItemsRef.current,
          sourceItemId ? [sourceItemId] : [],
        )
        onEdit(currentScript, focus, saved => recordEditorSave(sourceItemId, sourceGeneration, sourceScriptIndex, saved))
      }
    })()
  }

  // recordEditorSave marks a generated script that the script editor saved to
  // the library, as this review's own save does, so it is not offered or saved
  // again. A review refreshed since the script was opened is left alone.
  const recordEditorSave = (itemId: string, generation: number | undefined, scriptIndex: number, saved: StoredScript) => {
    const item = importItemsRef.current.find(candidate => candidate.id === itemId)
    if (!item || item.reviewGeneration !== generation || enterpriseArchiveResultScriptIsSaved(item, scriptIndex)) return
    // A validated archive with a saved draft is treated as any partly saved
    // archive is: it becomes a conversion review, and its remaining drafts are
    // converted again (runReview), so validation's save cannot repeat this one.
    const validated = item.status === 'validated'
    const nextItems = importItemsRef.current.map(candidate => candidate.id !== itemId
      ? candidate
      : {
          ...candidate,
          savedResultScripts: { ...candidate.savedResultScripts, [scriptIndex]: saved },
          ...(validated ? { status: 'converted' as const, reviewMode: 'conversion' as const } : {}),
          notice: `Saved ${saved.name} to the script library.`,
        })
    const updated = nextItems.find(candidate => candidate.id === itemId)
    if (validated && updated && getEnterpriseArchiveResultScripts(updated.result).some((_script, index) => !enterpriseArchiveResultScriptIsSaved(updated, index))) {
      setConvertAfterEditorSave({ itemId, notice: `Saved ${saved.name} to the script library. Continue reviewing the remaining generated scripts before leaving this import.`, archiveName: item.file.name })
    }
    // The guard's rendered state catches up only on the next render; a save
    // made on the way to another page must not prompt for this review.
    if (!reviewHasPendingDrafts(nextItems)) markNavigationGuardClean(ENTERPRISE_ARCHIVE_REVIEW_GUARD_ID)
    void publishSavedEnterpriseArchiveScripts(
      nextItems,
      [saved],
      `Saved ${saved.name} to the script library. Continue reviewing the remaining generated scripts before leaving this import.`,
      `Saved ${saved.name} to the script library.`,
      () => true,
    )
  }

  const openSalesforceNativePreviewInEditor = () => {
    if (!salesforceNativeScript || !onEdit || combinedJavaVuserGate) return
    // native_preview is currently embedded in summary evidence. The API
    // disclosure-sanitizes that evidence and does not attach the executable
    // draft attestation used by top-level candidates. Opening it could put a
    // literal [REDACTED] value into an otherwise runnable script. Keep the
    // preview review-only until the backend exposes it as its own attested
    // top-level candidate; the exact-replay candidate remains available.
    setError('Salesforce Native Preview is review-only until it is returned as a separately attested import candidate. Open Exact Replay to keep imported credential values runnable without re-entry.')
  }

  const openImportedRuntimeSettingsInEditor = (area: string) => {
    openImportedScriptInEditor({ targetSection: 'runtime', targetRuntimeArea: area })
  }

  const openImportedDataSourceInEditor = (sourceName?: string | null) => {
    const normalizedSourceName = sourceName?.trim()
    if (!normalizedSourceName) return
    openImportedScriptInEditor({ targetSection: 'parameter-data', targetDataSource: normalizedSourceName })
  }

  const openImportedParameterDataInEditor = (sourceName?: string | null) => {
    const normalizedSourceName = sourceName?.trim()
    openImportedScriptInEditor({
      targetSection: 'parameter-data',
      ...(normalizedSourceName ? { targetDataSource: normalizedSourceName } : {}),
    })
  }

  const buildMigrationPolicyRelaxationButtons = (keyPrefix: string): EditorActionButton[] => {
    if (!migrationReadiness?.blocked || !reviewMode) return []

    return getRelaxableMigrationBlockStatuses(migrationReadiness, options).map((status) => ({
      key: `${keyPrefix}:${status}`,
      label: `Allow ${formatRuntimeSettingStatusLabel(status)} Runtime Settings And Refresh Review`,
      opensEditor: false,
      // It changes the options, which stay put while they are locked.
      disabledReason: settingsEditing
        ? 'Apply or cancel the open setting or credential edits before changing the options.'
        : sourceMutationLocked ? 'Wait for the current archive operation to finish before changing the options.' : undefined,
      onClick: () => {
        if (sourceMutationLockedRef.current) return
        const nextOptions = getRelaxedMigrationBlockOptions(options, status)
        if (!nextOptions) return

        replaceOptionsAndInvalidate(nextOptions)
        const nextMode = reviewMode === 'validation' ? 'validate' : 'convert'
        void runReview(nextMode, nextOptions, buildMigrationPolicyRefreshNotice(nextMode, nextOptions), selectedItem ? [selectedItem.id] : undefined)
      },
    }))
  }

  const restoreDefaultMigrationPolicy = () => {
    const nextOptions = {
      ...options,
      block_partial: DEFAULT_IMPORT_OPTIONS.block_partial,
      block_compatibility_only: DEFAULT_IMPORT_OPTIONS.block_compatibility_only,
      block_non_applicable: DEFAULT_IMPORT_OPTIONS.block_non_applicable,
    }

    replaceOptionsAndInvalidate(nextOptions)
    if (reviewMode) {
      const nextMode = reviewMode === 'validation' ? 'validate' : 'convert'
      void runReview(nextMode, nextOptions, buildMigrationPolicyRefreshNotice(nextMode, nextOptions), selectedItem ? [selectedItem.id] : undefined)
    }
  }

  const renderEditorActionButtons = (buttons: EditorActionButton[], style: CSSProperties = actionWrapStyle) => {
    const seenKeys = new Set<string>()
    const availableButtons = buttons.filter((button) => {
      if (seenKeys.has(button.key)) return false
      seenKeys.add(button.key)
      return true
    })
    if (availableButtons.length === 0) return null

    return (
      <div className="replay-panel-actions" style={style}>
        {availableButtons.map((button) => {
          const blockedReason = button.disabledReason ?? (button.opensEditor !== false && editorBlocked ? editorBlockedReason : undefined)
          return (
            <button
              key={button.key}
              className="btn btn-secondary btn-sm"
              onClick={button.onClick}
              disabled={editorActionPreparing || scriptSaveInProgress || Boolean(blockedReason)}
              title={blockedReason}
            >
              {editorActionPreparing ? 'Preparing credentials…' : button.label}
            </button>
          )
        })}
      </div>
    )
  }

  const getReviewSectionActions = (sectionKey: string): EditorActionButton[] => {
    const buttons: EditorActionButton[] = []
    switch (sectionKey) {
      case 'unsupported_elements':
        if (importedEditorAvailable && firstUnsupportedTargetAction) {
          buttons.push({
            key: 'review-unsupported-action',
            label: 'Open First Unsupported Action In Editor',
            onClick: () => openImportedScriptInEditor({ targetAction: firstUnsupportedTargetAction }),
          })
        }
        if (importedEditorAvailable) {
          buttons.push({
            key: 'review-unsupported-script',
            label: 'Open Imported Script In Editor',
            onClick: () => openImportedScriptInEditor(),
          })
        }
        break
      case 'preserved_metadata':
        if (importedEditorAvailable && (runtimeFiles.length > 0 || reviewSummary?.mapped_runtime_file)) {
          buttons.push({
            key: 'review-preserved-runtime',
            label: 'Open Runtime Settings In Editor',
            onClick: () => openImportedScriptInEditor({ targetSection: 'runtime' }),
          })
        }
        if (importedEditorAvailable && parameterFiles.length > 0) {
          buttons.push({
            key: 'review-preserved-parameter-files',
            label: 'Open Parameter Files In Editor',
            onClick: () => openImportedParameterDataInEditor(firstMappedParameterDataSourceName || firstGeneratedDataSourceName),
          })
        }
        if (importedEditorAvailable && firstGeneratedDataSourceName) {
          buttons.push({
            key: 'review-preserved-source',
            label: 'Open First Data Source In Editor',
            onClick: () => openImportedDataSourceInEditor(firstGeneratedDataSourceName),
          })
        }
        break
      case 'runtime_settings_parity':
        if (importedEditorAvailable) {
          buttons.push({
            key: 'review-runtime-parity',
            label: 'Open Runtime Review In Editor',
            onClick: () => openImportedScriptInEditor({ targetSection: 'runtime' }),
          })
          if (firstRuntimeParityArea) {
            buttons.push({
              key: 'review-runtime-parity-area',
              label: 'Open First Runtime Setting In Editor',
              onClick: () => openImportedRuntimeSettingsInEditor(firstRuntimeParityArea),
            })
          }
        }
        break
      case 'runtime_settings_migration_readiness':
        if (importedEditorAvailable) {
          buttons.push({
            key: 'review-runtime-readiness',
            label: 'Open Runtime Review In Editor',
            onClick: () => openImportedScriptInEditor({ targetSection: 'runtime' }),
          })
          if (firstRuntimeBlockingArea) {
            buttons.push({
              key: 'review-runtime-blocking-area',
              label: 'Open First Blocking Setting In Editor',
              onClick: () => openImportedRuntimeSettingsInEditor(firstRuntimeBlockingArea),
            })
          }
        }
        buttons.push(...buildMigrationPolicyRelaxationButtons('review-runtime-relax'))
        break
      case 'browser_import_assumptions':
        if (importedEditorAvailable) {
          buttons.push({
            key: 'review-browser-import-script',
            label: 'Open Imported Script In Editor',
            onClick: () => openImportedScriptInEditor(),
          })
        }
        break
      case 'salesforce_review':
        if (importedEditorAvailable && salesforceNativeScript) {
          buttons.push({
            key: 'review-salesforce-native-preview',
            label: 'Open Native Preview',
            opensEditor: false,
            onClick: openSalesforceNativePreviewInEditor,
          })
        }
        if (importedEditorAvailable) {
          buttons.push({
            key: 'review-salesforce-exact-replay',
            label: 'Open Exact Replay',
            onClick: () => openImportedScriptInEditor(),
          })
        }
        break
      default:
        break
    }

    return buttons
  }

  const setBrowserReplayStartedNotice = (wasStarted: boolean) => {
    if (!selectedItem) return
    updateImportItem(selectedItem.id, {
      notice: wasStarted
        ? 'Draft browser replay restarted. Review the refreshed draft browser replay session before saving it to the library.'
        : 'Draft browser replay started. Review the draft browser replay session before saving it to the library.',
    })
  }

  useEffect(() => {
    let cancelled = false

    api.getEnterpriseArchiveSupportMatrix()
      .then((response: EnterpriseArchiveSupportMatrixResponse) => {
        if (!cancelled) {
          setSupportMatrix(Array.isArray(response.support_matrix) ? response.support_matrix : [])
          setSupportMatrixNotes(Array.isArray(response.notes) ? response.notes : [])
          setSupportMatrixExamples(Array.isArray(response.tested_examples) ? response.tested_examples : [])
          setProtocolSupportMatrix(Array.isArray(response.protocol_support_matrix) ? response.protocol_support_matrix : [])
          setJavaImportSurfaceMatrix(Array.isArray(response.java_import_surface_matrix) ? response.java_import_surface_matrix : [])
          setJavaHTTPIdiomMatrix(Array.isArray(response.java_http_idiom_matrix) ? response.java_http_idiom_matrix : [])
          setJavaJMSIdiomMatrix(Array.isArray(response.java_jms_idiom_matrix) ? response.java_jms_idiom_matrix : [])
          setJavaJDBCIdiomMatrix(Array.isArray(response.java_jdbc_idiom_matrix) ? response.java_jdbc_idiom_matrix : [])
        }
      })
      .catch(() => {
        if (!cancelled) {
          setSupportMatrix([])
          setSupportMatrixNotes([])
          setSupportMatrixExamples([])
          setProtocolSupportMatrix([])
          setJavaImportSurfaceMatrix([])
          setJavaHTTPIdiomMatrix([])
          setJavaJMSIdiomMatrix([])
          setJavaJDBCIdiomMatrix([])
        }
      })

    return () => {
      cancelled = true
    }
  }, [api])

  useEffect(() => {
    const item = importItemsRef.current.find(candidate => candidate.id === selectedItemId)
    setSelectedResultScriptIndex(item ? firstUnsavedEnterpriseArchiveResultScriptIndex(item) : 0)
    if (supplementalFileRef.current) {
      supplementalFileRef.current.value = ''
    }
  }, [selectedItemId])

  useEffect(() => {
    const sessionId = replayValidationSession?.id
    const replayItemId = selectedItem?.id
    const replayGeneration = selectedItem?.reviewGeneration
    const replayScriptIndex = activeResultScriptIndex
    const replayProjectId = projectId || ''
    if (!sessionId || !replayItemId || replayGeneration === undefined || !browserReplayInProgress || replayValidationLoading) {
      return
    }

    let cancelled = false
    const replayScopeIsCurrent = () => {
      const current = importItemsRef.current.find(item => item.id === replayItemId)
      return !cancelled
        && current?.reviewGeneration === replayGeneration
        && current.replayReviewGeneration === replayGeneration
        && current.replayScriptIndex === replayScriptIndex
        && current.replayProjectId === replayProjectId
        && reviewProjectIdRef.current === projectId
    }
    const replayLookupKey = `${replayItemId}\u0000${replayGeneration}\u0000${replayScriptIndex}\u0000${replayProjectId}\u0000${sessionId}`

    const refreshSession = () => {
      void api.getBrowserReplaySession(sessionId)
        .then((response) => {
          if (!replayScopeIsCurrent() || !response?.session) {
            return
          }
          replayNotFoundCountsRef.current.delete(replayLookupKey)
          updateImportItem(replayItemId, {
            replayValidationSession: response.session,
            replayValidationWarnings: Array.isArray(response.warnings) ? response.warnings : replayValidationWarnings,
            replayValidationError: response.session.status === 'failed' && response.session.error ? response.session.error : replayValidationError,
          })
        })
        .catch((error: unknown) => {
          if (!replayScopeIsCurrent()) return
          if (error instanceof ApiError && error.status === 404) {
            const notFoundCount = (replayNotFoundCountsRef.current.get(replayLookupKey) || 0) + 1
            replayNotFoundCountsRef.current.set(replayLookupKey, notFoundCount)
            if (notFoundCount <= 4) return
            replayNotFoundCountsRef.current.delete(replayLookupKey)
            const current = importItemsRef.current.find(item => item.id === replayItemId)?.replayValidationSession
            if (!current || current.id !== sessionId) return
            updateImportItem(replayItemId, {
              replayValidationSession: {
                ...current,
                status: 'failed',
                error: 'Replay session could not be confirmed. Start draft browser replay again.',
              },
              replayValidationError: 'Replay session could not be confirmed. Start draft browser replay again.',
            })
            return
          }
          updateImportItem(replayItemId, {
            replayValidationError: getErrorMessage(error, 'Draft browser replay status refresh failed'),
          })
        })
    }

    const intervalId = window.setInterval(refreshSession, 2000)
    return () => {
      cancelled = true
      window.clearInterval(intervalId)
    }
  }, [activeResultScriptIndex, api, browserReplayInProgress, projectId, replayValidationError, replayValidationLoading, replayValidationSession?.id, replayValidationWarnings, selectedItem?.id, selectedItem?.reviewGeneration])

  // Returns the number of archives actually queued, so drop handlers can tell
  // the drop animation whether the drop was accepted.
  const addFilesToQueue = useCallback((fileList?: FileList | File[] | null): number => {
    if (sourceMutationLockedRef.current) return 0
    const incomingFiles = Array.from(fileList || [])
    if (incomingFiles.length === 0) return 0

    const zipFiles = incomingFiles.filter(isEnterpriseArchiveZipFile)
    if (zipFiles.length === 0) {
      setError('Please upload ZIP or USZ archives containing VuGen projects.')
      return 0
    }

    // A retained receipt retry describes the complete source batch that was
    // converted. Adding another source changes that batch, so it must start a
    // fresh operation rather than silently reconciling the old one.
    const retainedItemIds = retainedAtomicSaveRef.current?.entries.map(entry => entry.itemId) || []
    if (retainedItemIds.length > 0) {
      invalidateOperationScopes(retainedItemIds)
    }

    const nextItems = zipFiles.map((nextFile) => {
      itemCounterRef.current += 1
      const randomItemId = createReplaySessionId()
      return buildEnterpriseArchiveImportQueueItem(
        nextFile,
        `lr-import-${randomItemId || `${Date.now()}-${itemCounterRef.current}`}`,
      )
    })
    setImportItems(items => [...items, ...nextItems])
    setSelectedItemId(nextItems[0].id)
    setError(zipFiles.length === incomingFiles.length ? '' : `${incomingFiles.length - zipFiles.length} unsupported file${incomingFiles.length - zipFiles.length === 1 ? '' : 's'} skipped.`)
    setNotice('')
    return zipFiles.length
  }, [invalidateOperationScopes])

  const addFolderToQueue = useCallback((fileList?: FileList | null) => {
    if (sourceMutationLockedRef.current) return
    const files = Array.from(fileList || [])
    if (files.length === 0) return
    const roots = new Set(files.map(file => (file.webkitRelativePath || '').split('/')[0]))
    if (roots.size !== 1 || files.some(file => !file.webkitRelativePath) ||
        files.filter(file => file.name.toLowerCase().endsWith('.usr')).length !== 1) {
      setError('Choose one VuGen script folder containing exactly one .usr project file.')
      return
    }
    const retainedItemIds = retainedAtomicSaveRef.current?.entries.map(entry => entry.itemId) || []
    if (retainedItemIds.length > 0) invalidateOperationScopes(retainedItemIds)
    itemCounterRef.current += 1
    const folderName = Array.from(roots)[0]
    const displayFile = new File([], folderName, { type: 'application/x-directory' })
    const id = `lr-import-${createReplaySessionId() || `${Date.now()}-${itemCounterRef.current}`}`
    const item = buildEnterpriseArchiveImportQueueItem(displayFile, id, files)
    setImportItems(items => [...items, item])
    setSelectedItemId(id)
    setError('')
    setNotice('')
  }, [invalidateOperationScopes])

  useEffect(() => {
    folderRef.current?.setAttribute('webkitdirectory', '')
    folderRef.current?.setAttribute('directory', '')
  }, [])

  useEffect(() => {
    if (initialFiles && initialFiles.length > 0) {
      addFilesToQueue(initialFiles)
    }
  }, [addFilesToQueue, initialFiles])

  // Desktop shell: prefer the native multi-file dialog and accept native file
  // drops (absolute paths via Wails OnFileDrop), reconstructing File objects for
  // the existing queue. Browser keeps the <input type=file> + HTML5 drop path.
  const handleNativeArchivePaths = useCallback((paths: string[]) => {
    beginIngest(describeReading(paths.length))
    void filesFromDroppedPaths(paths).then(files => {
      setDragOver(false)
      const queued = addFilesToQueue(files)
      if (queued > 0) succeedIngest(describeAccepted(queued))
      else failIngest()
    }).catch(error => {
      setDragOver(false)
      failIngest()
      setError(nativeFileReadErrorMessage(error))
      if (shouldUseBrowserFileFallback(error)) setBrowserPickerFallback(true)
    })
  }, [addFilesToQueue, beginIngest, succeedIngest, failIngest])
  useDesktopFileDrop(dropzoneRef, handleNativeArchivePaths, desktop)

  const handleBrowseArchives = useCallback(() => {
    if (sourceMutationLockedRef.current) return
    if (nativeDialogs) {
      void pickFilesViaBridge('.zip,.usz', true).then(files => {
        if (files.length > 0) {
          addFilesToQueue(files)
        }
      }).catch(error => {
        setError(nativeFileReadErrorMessage(error))
        if (shouldUseBrowserFileFallback(error)) {
          setBrowserPickerFallback(true)
          fileRef.current?.click()
        }
      })
      return
    }
    fileRef.current?.click()
  }, [nativeDialogs, addFilesToQueue])

  const removeQueuedFile = (itemId: string) => {
    if (sourceMutationLockedRef.current) return
    invalidateOperationScopes([itemId])
    const removingSelectedItem = selectedItemId === itemId
    const nextSelectedItem = removingSelectedItem
      ? importItems.find(item => item.id !== itemId)?.id || ''
      : selectedItemId
    setImportItems(items => {
      const nextItems = items.filter(item => item.id !== itemId)
      return nextItems
    })
    setSelectedItemId(nextSelectedItem)
    setNotice('')
  }

  const clearQueuedFiles = () => {
    if (sourceMutationLockedRef.current) return
    invalidateOperationScopes()
    setImportItems([])
    setSelectedItemId('')
    setError('')
    setNotice('')
    if (fileRef.current) {
      fileRef.current.value = ''
    }
    if (harFileRef.current) {
      harFileRef.current.value = ''
    }
    if (supplementalFileRef.current) {
      supplementalFileRef.current.value = ''
    }
  }

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault()
    setDragOver(false)
    // Desktop file drops arrive via the native OnFileDrop router with real
    // paths; the webview's dataTransfer has none, so ignore HTML5 drop there.
    if (desktop) return
    const dropped = e.dataTransfer.files
    if (!dropped || dropped.length === 0) return
    beginIngest(describeReading(dropped.length))
    const queued = addFilesToQueue(dropped)
    if (queued > 0) succeedIngest(describeAccepted(queued))
    else failIngest()
  }

  const handleFileSelect = (e: React.ChangeEvent<HTMLInputElement>) => {
    addFilesToQueue(e.target.files)
    e.target.value = ''
  }

  const handleHARFileSelect = (e: React.ChangeEvent<HTMLInputElement>) => {
    const nextFile = Array.from(e.target.files || [])[0]
    e.target.value = ''
    if (!selectedItem || !nextFile) return
    if (!isHARFile(nextFile)) {
      updateImportItem(selectedItem.id, {
        error: 'Please attach a HAR or JSON file captured from the same Salesforce recording.',
      })
      return
    }
    invalidateOperationScopes([selectedItem.id])
    updateImportItem(selectedItem.id, {
      harFile: nextFile,
      status: 'queued',
      result: null,
      reviewMode: null,
      reviewOptionsIdentity: '',
      reviewGeneration: selectedItem.reviewGeneration + 1,
      replayValidationLoading: false,
      replayValidationError: '',
      replayValidationWarnings: [],
      replayValidationSession: null,
      replayReviewGeneration: -1,
      replayScriptIndex: -1,
      replayProjectId: '',
      savedResultScripts: {},
      sourceSecretProjectId: '',
      error: '',
      notice: '',
    })
  }

  const clearSelectedHARFile = () => {
    if (!selectedItem) return
    invalidateOperationScopes([selectedItem.id])
    updateImportItem(selectedItem.id, {
      harFile: null,
      status: 'queued',
      result: null,
      reviewMode: null,
      reviewOptionsIdentity: '',
      reviewGeneration: selectedItem.reviewGeneration + 1,
      replayValidationLoading: false,
      replayValidationError: '',
      replayValidationWarnings: [],
      replayValidationSession: null,
      replayReviewGeneration: -1,
      replayScriptIndex: -1,
      replayProjectId: '',
      savedResultScripts: {},
      sourceSecretProjectId: '',
      notice: '',
      error: '',
    })
    if (harFileRef.current) {
      harFileRef.current.value = ''
    }
  }

  // Attaching, removing, or clearing supplemental sources changes what the
  // backend will convert, so each handler invalidates the current review and
  // returns the item to 'queued' for a fresh validate/convert run.
  // The patch may be derived from the item so a caller that had to await (for
  // example reading an attached file's text) merges against the item as it
  // stands when the update runs, not against a stale render-time snapshot.
  const resetSelectedItemForSupplementalChange = (
    patch:
      | Partial<EnterpriseArchiveImportQueueItem>
      | ((item: EnterpriseArchiveImportQueueItem) => Partial<EnterpriseArchiveImportQueueItem>),
  ) => {
    if (!selectedItem || sourceMutationLockedRef.current) return
    const itemId = selectedItem.id
    invalidateOperationScopes([itemId])
    setImportItems(items => items.map(item => item.id !== itemId ? item : {
      ...item,
      ...(typeof patch === 'function' ? patch(item) : patch),
      lastRequiredFileCandidates: item.result
        ? parseRequiredJavaFilesFromFidelityBlockers(
            (item.result.fidelity_report ?? item.result.summary?.fidelity_report)?.blockers || [],
          )
        : item.lastRequiredFileCandidates,
      status: 'queued',
      result: null,
      reviewMode: null,
      reviewOptionsIdentity: '',
      reviewGeneration: item.reviewGeneration + 1,
      replayValidationLoading: false,
      replayValidationError: '',
      replayValidationWarnings: [],
      replayValidationSession: null,
      replayReviewGeneration: -1,
      replayScriptIndex: -1,
      replayProjectId: '',
      savedResultScripts: {},
      sourceSecretProjectId: '',
      error: '',
      notice: '',
    }))
  }

  const handleSupplementalFileSelect = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const incomingFiles = Array.from(e.target.files || [])
    e.target.value = ''
    if (!selectedItem || incomingFiles.length === 0) return

    // Java sources renamed to .txt to survive a mail gateway are restored to
    // their .java name here, matching the authoritative rename the archive
    // importer performs server-side.
    //
    // Reading a file can reject (permissions, a file removed or truncated after
    // the picker closed). Renaming is a convenience, so one unreadable entry
    // must not reject the batch: attach it under its original name and let the
    // server decide, rather than dropping every file with an unhandled rejection
    // and no visible error.
    const normalizedFiles = await Promise.all(incomingFiles.map(async (file) => {
      try {
        return await normalizeJavaSourceSupplementalFile(file, supplementalRenameHasJavaContext)
      } catch {
        return file
      }
    }))

    resetSelectedItemForSupplementalChange(item => {
      // Same-named re-attachments replace the previous file rather than duplicate it.
      const deduped = new Map<string, File>()
      ;(item.supplementalFiles || []).forEach((file) => {
        deduped.set(file.name.toLowerCase(), file)
      })
      normalizedFiles.forEach((file) => {
        deduped.set(file.name.toLowerCase(), file)
      })
      return { supplementalFiles: Array.from(deduped.values()) }
    })
  }

  const removeSelectedSupplementalFile = (fileName: string) => {
    if (!selectedItem) return
    const remaining = (selectedItem.supplementalFiles || []).filter(file => file.name !== fileName)
    resetSelectedItemForSupplementalChange({ supplementalFiles: remaining })
  }

  const clearSelectedSupplementalFiles = () => {
    if (!selectedItem) return
    if ((selectedItem.supplementalFiles || []).length === 0) return
    resetSelectedItemForSupplementalChange({ supplementalFiles: [] })
    if (supplementalFileRef.current) {
      supplementalFileRef.current.value = ''
    }
  }

  const runReview = async (
    mode: 'convert' | 'validate',
    reviewOptions: ImportOptions = options,
    completionNotice?: string,
    itemIds?: string[],
  ) => {
    const eligibleItems = importItems.filter(item => (!itemIds || itemIds.includes(item.id))
      && (Object.keys(item.savedResultScripts || {}).length === 0
        || getEnterpriseArchiveResultScripts(item.result).some((_, index) => !enterpriseArchiveResultScriptIsSaved(item, index))))
    const addingFiles = eligibleItems.some(item => item.status === 'queued')
    // Adding files preserves existing reviews; an explicit refresh with no new work
    // renews the selected review, including an expired protected draft.
    const targetItems = eligibleItems.filter(item => itemIds || item.status !== 'converted'
      || item.reviewOptionsIdentity !== importOptionsIdentity(reviewOptions)
      || (!addingFiles && item.id === selectedItemId))
    if (targetItems.length === 0) return
    clearRetainedAtomicSave(targetItems.map(item => item.id))
    void discardQueuedSourceSecretSessions(targetItems)
    const operationGeneration = ++operationGenerationRef.current
    const sourceProjectId = reviewProjectIdRef.current
    const sourceOptionsIdentity = importOptionsIdentity(reviewOptions)
    const requestTokens = new Map(targetItems.map(item => [
      item.id,
      nextRequestToken(itemRequestTokensRef.current, item.id),
    ]))
    const operationIsCurrent = () => operationGenerationRef.current === operationGeneration
      && reviewProjectIdRef.current === sourceProjectId
      && importOptionsIdentity(optionsRef.current) === sourceOptionsIdentity
    const requestIsCurrent = (item: EnterpriseArchiveImportQueueItem) => {
      const current = importItemsRef.current.find(candidate => candidate.id === item.id)
      return operationIsCurrent()
        && itemRequestTokensRef.current.get(item.id) === requestTokens.get(item.id)
        && current?.file === item.file
        && current.harFile === item.harFile
        && current.supplementalFiles === item.supplementalFiles
    }

    const runningStatus = mode === 'validate' ? 'validating' : 'converting'
    const completeStatus = mode === 'validate' ? 'validated' : 'converted'
    const targetIds = new Set(targetItems.map(item => item.id))
    const scriptNameRegistry: EnterpriseArchiveScriptNameRegistry = new Map()
    importItems
      .filter(item => !targetIds.has(item.id) || Object.keys(item.savedResultScripts || {}).length > 0)
      .flatMap(item => getEnterpriseArchiveResultScripts(item.result))
      .forEach(script => { reserveEnterpriseArchiveScriptName(script.name || '', scriptNameRegistry) })

    setWorkingMode(mode)
    setError('')
    setNotice('')
    setSelectedResultScriptIndex(0)
    setSelectedItemId(prev => targetIds.has(prev) ? prev : targetItems[0].id)
    setImportItems(items => items.map(item => targetIds.has(item.id)
      ? {
          ...item,
          status: runningStatus,
          result: Object.keys(item.savedResultScripts || {}).length > 0 ? item.result : null,
          reviewMode: null,
          error: '',
          notice: '',
          replayValidationLoading: false,
          replayValidationError: '',
          replayValidationWarnings: [],
          replayValidationSession: null,
          replayReviewGeneration: -1,
          replayScriptIndex: -1,
          replayProjectId: '',
          reviewGeneration: item.reviewGeneration + 1,
          savedResultScripts: item.savedResultScripts,
          sourceSecretProjectId: '',
        }
      : item))

    let successCount = 0
    let failureCount = 0
    const useNameOverride = targetItems.length === 1

    for (const item of targetItems) {
      if (!operationIsCurrent()) break
      if (!requestIsCurrent(item)) continue
      const partiallySaved = Object.keys(item.savedResultScripts || {}).length > 0
      const request = mode === 'validate' && !partiallySaved ? api.validateEnterpriseArchiveImport : api.importEnterpriseArchive
      try {
        const rawResult = await request(item.archiveFile || item.file, {
          ...(item.archiveMember ? { archive_member: item.archiveMember } : {}),
          name: useNameOverride ? reviewOptions.name || undefined : undefined,
          project_id: sourceProjectId,
          base_url: reviewOptions.base_url || undefined,
          include_assertions: reviewOptions.include_assertions,
          preserve_transactions: reviewOptions.preserve_transactions,
          migration_block_statuses: buildMigrationBlockStatuses(reviewOptions),
          ...(reviewOptions.expire_draft ? { expire_draft: true } : {}),
          ...(item.harFile ? { har_file: item.harFile } : {}),
          ...(item.folderFiles ? { folder_files: item.folderFiles } : {}),
          ...((item.supplementalFiles || []).length > 0 ? { supplemental_files: item.supplementalFiles || [] } : {}),
          ...(reviewOptions.salesforce_native ? { salesforce_native: true } : {}),
        })
        if (!requestIsCurrent(item)) {
          if (sourceProjectId) {
            const sessions = [rawResult.source_secret_session_id, ...(rawResult.archives || []).map(archive => archive.response?.source_secret_session_id)]
            await Promise.all(sessions.filter((id): id is string => Boolean(id)).map(id => discardSourceSecretSession(sourceProjectId, id)))
          }
          continue
        }
        if (rawResult.archive_collection && rawResult.archives?.length) {
          const children = rawResult.archives.map<EnterpriseArchiveImportQueueItem>((archive, index) => {
            const child = buildEnterpriseArchiveImportQueueItem(
              new File([], archive.source_name, { type: 'application/zip' }),
              `${item.id}:member:${index}`,
            )
            return {
              ...child,
              archiveFile: item.archiveFile || item.file,
              archiveMember: archive.source_name,
              sourceSize: archive.source_size,
              status: archive.response ? completeStatus : 'failed' as const,
              result: archive.response ? uniquifyEnterpriseArchiveResultScriptNames(archive.response, scriptNameRegistry) : null,
              reviewMode: archive.response ? mode === 'validate' ? 'validation' as const : 'conversion' as const : null,
              reviewOptionsIdentity: sourceOptionsIdentity,
              reviewGeneration: item.reviewGeneration + 1,
              sourceSecretProjectId: sourceProjectId || '',
              error: archive.error || '',
              notice: archive.response ? 'Import checks complete. Review this project independently before saving.' : '',
            }
          })
          successCount += children.filter(child => child.result).length
          failureCount += children.filter(child => !child.result).length
          const nextItems = importItemsRef.current.flatMap(current => current.id === item.id ? children : [current])
          importItemsRef.current = nextItems
          setImportItems(nextItems)
          if (selectedItemIdRef.current === item.id) setSelectedItemId(children[0].id)
          setNotice(`Opened ${children.length} projects from ${item.file.name}. Each project has its own settings, review, and save status.`)
          continue
        }
        const previousScripts = getEnterpriseArchiveResultScripts(item.result)
        const refreshedScripts = getEnterpriseArchiveResultScripts(rawResult)
        if (partiallySaved && (previousScripts.length !== refreshedScripts.length
          || previousScripts.some((script, index) => script.protocol !== refreshedScripts[index].protocol))) {
          if (sourceProjectId && rawResult.source_secret_session_id) {
            await discardSourceSecretSession(sourceProjectId, rawResult.source_secret_session_id)
          }
          throw new Error('The generated script lanes changed. Keep the saved scripts and import the changed archive separately to review its new lanes.')
        }
        const res = uniquifyEnterpriseArchiveResultScriptNames(rawResult, scriptNameRegistry,
          partiallySaved ? previousScripts.map(script => script.name || '') : undefined)
        if (partiallySaved && item.id === (targetIds.has(selectedItemId) ? selectedItemId : targetItems[0].id)) {
          setSelectedResultScriptIndex(refreshedScripts.findIndex((_, index) => !enterpriseArchiveResultScriptIsSaved(item, index)))
        }
        successCount += 1
        updateImportItem(item.id, {
          status: partiallySaved ? 'converted' : completeStatus,
          result: res,
          reviewMode: mode === 'validate' && !partiallySaved ? 'validation' : 'conversion',
          reviewOptionsIdentity: sourceOptionsIdentity,
          savedResultScripts: item.savedResultScripts,
          sourceSecretProjectId: sourceProjectId || '',
          error: '',
          notice: completionNotice || (mode === 'validate'
            ? 'Validation finished. Review execution readiness and required repairs below.'
            : 'Conversion complete. Review the generated script, warnings, and preserved settings before saving.'),
        })
      } catch (error: unknown) {
        if (!requestIsCurrent(item)) continue
        failureCount += 1
        updateImportItem(item.id, {
          status: 'failed',
          error: getErrorMessage(error, mode === 'validate' ? 'Validation failed' : 'Import failed'),
          notice: '',
        })
      }
    }

    if (operationGenerationRef.current !== operationGeneration || reviewProjectIdRef.current !== sourceProjectId) return
    setWorkingMode(null)
    if (targetItems.length > 1) {
      const action = mode === 'validate' ? 'Validated' : 'Converted'
      const reviewedCount = successCount + failureCount
      setNotice(`${action} ${successCount} of ${reviewedCount} enterprise script archive${reviewedCount === 1 ? '' : 's'}${failureCount > 0 ? `; ${failureCount} failed` : ''}.`)
    }
  }
  const runReviewRef = useRef(runReview)
  runReviewRef.current = runReview
  useEffect(() => {
    if (!convertAfterEditorSave) return
    const { itemId, notice, archiveName } = convertAfterEditorSave
    setConvertAfterEditorSave(null)
    const refreshing = { itemId, archiveName }
    setRefreshingRemainingDraftsOf(refreshing)
    void runReviewRef.current('convert', optionsRef.current, notice, [itemId])
      .finally(() => setRefreshingRemainingDraftsOf(current => current === refreshing ? null : current))
  }, [convertAfterEditorSave])

  const convertValidatedItems = async (itemIds?: string[], destination: ImportSaveDestination = 'stay') => {
    const targetItems = importItems.filter(item => (!itemIds || itemIds.includes(item.id)) && item.status === 'validated')
    if (targetItems.length === 0) {
      setError('Validate the queued enterprise script archive before converting it.')
      return
    }
    if (targetItems.some(item => item.reviewOptionsIdentity !== importOptionsIdentity(options))) {
      setError('Conversion options changed after validation. Validate the queued archive again before converting it.')
      return
    }
    const sourceProjectId = reviewProjectIdRef.current
    const sourceOptionsIdentity = importOptionsIdentity(options)
    const destinationFolderId = folderId ?? null
    const retainedCandidate = retainedAtomicSaveRef.current
    const retainedAtomicSave = retainedCandidate
      && retainedCandidate.projectId === sourceProjectId
      && retainedCandidate.folderId === destinationFolderId
      && retainedCandidate.optionsIdentity === sourceOptionsIdentity
      && retainedCandidate.entries.length === targetItems.length
      && retainedCandidate.entries.every((entry, index) => {
        const item = targetItems[index]
        return item?.id === entry.itemId
          && item.file === entry.file
          && item.harFile === entry.harFile
          && item.supplementalFiles === entry.supplementalFiles
          && item.result === entry.result
      })
      ? retainedCandidate
      : null
    if (retainedCandidate && !retainedAtomicSave) {
      clearRetainedAtomicSave()
    }
    if (!retainedAtomicSave) {
      void discardQueuedSourceSecretSessions(targetItems)
    }
    const operationGeneration = ++operationGenerationRef.current
    const requestTokens = new Map(targetItems.map(item => [
      item.id,
      nextRequestToken(itemRequestTokensRef.current, item.id),
    ]))
    const operationIsCurrent = () => operationGenerationRef.current === operationGeneration
      && reviewProjectIdRef.current === sourceProjectId
      && destinationFolderIdRef.current === destinationFolderId
      && importOptionsIdentity(optionsRef.current) === sourceOptionsIdentity
    const requestIsCurrent = (item: EnterpriseArchiveImportQueueItem) => {
      const current = importItemsRef.current.find(candidate => candidate.id === item.id)
      return operationIsCurrent()
        && itemRequestTokensRef.current.get(item.id) === requestTokens.get(item.id)
        && current?.file === item.file
        && current.harFile === item.harFile
        && current.supplementalFiles === item.supplementalFiles
    }

    const targetIds = new Set(targetItems.map(item => item.id))
    const scriptNameRegistry: EnterpriseArchiveScriptNameRegistry = new Map()
    importItems
      .filter(item => !targetIds.has(item.id))
      .flatMap(item => getEnterpriseArchiveResultScripts(item.result))
      .forEach(script => { reserveEnterpriseArchiveScriptName(script.name || '', scriptNameRegistry) })
    const useNameOverride = targetItems.length === 1
    const savedScripts: StoredScript[] = []
    const readyAtomicSaves: Array<{
      item: EnterpriseArchiveImportQueueItem
      result: EnterpriseArchiveImportResult
      scripts: ScriptDefinition[]
    }> = retainedAtomicSave
      ? retainedAtomicSave.entries.map((entry, index) => ({
          item: targetItems[index],
          result: entry.result,
          scripts: entry.scripts,
        }))
      : []
    const conversionItemPatches = new Map<string, Partial<EnterpriseArchiveImportQueueItem>>()
    let successCount = 0
    let failureCount = 0
    let skippedCount = 0
    let atomicSaveUnconfirmed = false
    let atomicSaveNeedsFreshDraft = false

    setWorkingMode('convert')
    setError('')
    setNotice('')
    setSelectedItemId(prev => targetIds.has(prev) ? prev : targetItems[0].id)
    setImportItems(items => items.map(item => targetIds.has(item.id)
      ? {
          ...item,
          status: 'converting',
          error: '',
          notice: '',
          replayValidationLoading: false,
          replayValidationError: '',
          replayValidationWarnings: [],
          replayValidationSession: null,
          replayReviewGeneration: -1,
          replayScriptIndex: -1,
          replayProjectId: '',
          reviewGeneration: item.reviewGeneration + 1,
        }
      : item))

    const conversionTargetItems = retainedAtomicSave ? [] : targetItems
    for (const item of conversionTargetItems) {
      // A whole-operation fence (unmount, project switch, a newly started run)
      // abandons the batch, but a single fenced queue row must only skip its own
      // archive so the archives queued behind it still convert.
      if (!operationIsCurrent()) break
      if (!requestIsCurrent(item)) {
        skippedCount += 1
        continue
      }
      let conversionResult: EnterpriseArchiveImportResult | null = null
      try {
        conversionResult = await api.importEnterpriseArchive(item.archiveFile || item.file, {
          ...(item.archiveMember ? { archive_member: item.archiveMember } : {}),
          name: useNameOverride ? options.name || undefined : undefined,
          project_id: sourceProjectId,
          base_url: options.base_url || undefined,
          include_assertions: options.include_assertions,
          preserve_transactions: options.preserve_transactions,
          migration_block_statuses: buildMigrationBlockStatuses(options),
          ...(options.expire_draft ? { expire_draft: true } : {}),
          ...(item.harFile ? { har_file: item.harFile } : {}),
          ...(item.folderFiles ? { folder_files: item.folderFiles } : {}),
          ...((item.supplementalFiles || []).length > 0 ? { supplemental_files: item.supplementalFiles || [] } : {}),
          ...(options.salesforce_native ? { salesforce_native: true } : {}),
        })
        if (!requestIsCurrent(item)) {
          skippedCount += 1
          if (sourceProjectId && conversionResult.source_secret_session_id) {
            await discardSourceSecretSession(sourceProjectId, conversionResult.source_secret_session_id)
          }
          continue
        }
        conversionResult = uniquifyEnterpriseArchiveResultScriptNames(conversionResult, scriptNameRegistry)
        const scriptsToSave = getEnterpriseArchiveResultScripts(conversionResult)
        const conversionDetectedProtocol = conversionResult.detected_protocol ?? conversionResult.summary?.detected_protocol ?? null
        const conversionBlockedByProtocol = Boolean(conversionDetectedProtocol && conversionDetectedProtocol.import_mode !== 'executable')
        const conversionBlockedByMigration = Boolean(conversionResult.summary?.migration_readiness?.blocked)
        const conversionRequiresBrowserReplay = scriptsToSave.some(script => isBrowserImport(script))
        const conversionSecretBindingRequirements = conversionResult.secret_binding_requirements ?? []
        const conversionRequiresSecretBindings = conversionSecretBindingRequirements.length > 0
          && !requirementsPreserveDestinationValues(conversionSecretBindingRequirements)
        const conversionRequiresParameterizationReview = (conversionResult.parameterization?.suggestions || [])
          .some(suggestion => !suggestion.pre_applied)
        if (scriptsToSave.length === 0) {
          const patch: Partial<EnterpriseArchiveImportQueueItem> = {
            status: 'converted',
            result: conversionResult,
            reviewMode: 'conversion',
            reviewOptionsIdentity: sourceOptionsIdentity,
            savedResultScripts: {},
            sourceSecretProjectId: sourceProjectId || '',
            error: 'Conversion completed, but no executable script was produced. Review the generated artifact before saving.',
            notice: '',
          }
          conversionItemPatches.set(item.id, patch)
          updateImportItem(item.id, patch)
          failureCount += 1
          continue
        }
        if (conversionBlockedByProtocol || conversionBlockedByMigration || conversionRequiresBrowserReplay || conversionRequiresSecretBindings || conversionRequiresParameterizationReview) {
          const patch: Partial<EnterpriseArchiveImportQueueItem> = {
            status: 'converted',
            result: conversionResult,
            reviewMode: 'conversion',
            reviewOptionsIdentity: sourceOptionsIdentity,
            savedResultScripts: {},
            sourceSecretProjectId: sourceProjectId || '',
            error: '',
            notice: conversionBlockedByProtocol
              ? 'Conversion complete. Review the protocol gate before saving or promoting this archive.'
              : conversionBlockedByMigration
                ? 'Conversion complete. Resolve migration readiness blockers before saving.'
                : conversionRequiresBrowserReplay
                  ? 'Conversion complete. Run draft browser replay before saving this browser script.'
                  : conversionRequiresSecretBindings
                  ? 'Conversion complete. Resolve the redacted credential bindings before saving.'
                    : 'Conversion complete. Review parameterization suggestions before saving.',
          }
          conversionItemPatches.set(item.id, patch)
          updateImportItem(item.id, patch)
          successCount += 1
          continue
        }

        // Hold every immediately-runnable lane until all queued archives have
        // converted. One server transaction then saves the complete ready set;
        // a later failure can no longer leave earlier archives in the library.
        readyAtomicSaves.push({ item, result: conversionResult, scripts: scriptsToSave })
        const patch: Partial<EnterpriseArchiveImportQueueItem> = {
          status: 'converting',
          result: conversionResult,
          reviewMode: 'conversion',
          reviewOptionsIdentity: sourceOptionsIdentity,
          savedResultScripts: {},
          sourceSecretProjectId: sourceProjectId || '',
          error: '',
          notice: `Converted ${scriptsToSave.length} script${scriptsToSave.length === 1 ? '' : 's'}; waiting for the atomic batch save.`,
        }
        conversionItemPatches.set(item.id, patch)
        updateImportItem(item.id, patch)
      } catch (error: unknown) {
        if (!requestIsCurrent(item)) {
          skippedCount += 1
          if (sourceProjectId && conversionResult?.source_secret_session_id) {
            await discardSourceSecretSession(sourceProjectId, conversionResult.source_secret_session_id)
          }
          continue
        }
        failureCount += 1
        const patch: Partial<EnterpriseArchiveImportQueueItem> = {
          status: 'failed',
          ...(conversionResult ? {
            result: conversionResult,
            reviewMode: 'conversion' as const,
            reviewOptionsIdentity: sourceOptionsIdentity,
            savedResultScripts: {},
            sourceSecretProjectId: sourceProjectId || '',
          } : {}),
          error: getErrorMessage(error, 'Import failed'),
          notice: '',
        }
        conversionItemPatches.set(item.id, patch)
        updateImportItem(item.id, patch)
      }
    }

    const currentReadySaves = readyAtomicSaves.filter(({ item }) => requestIsCurrent(item))
    skippedCount += readyAtomicSaves.length - currentReadySaves.length
    if (operationIsCurrent() && currentReadySaves.length > 0) {
      const operationKey = retainedAtomicSave?.operationKey ?? [
          currentReadySaves[0].item.id,
          sourceProjectId || '',
          destinationFolderId || '',
          sourceOptionsIdentity,
          ...currentReadySaves.flatMap(({ item, scripts }) => [
            item.id,
            ...scripts.map(script => script.name || ''),
          ]),
        ].join('\u0000')
      let operationID = retainedAtomicSave?.operationId ?? saveOperationIDsRef.current.get(operationKey)
      if (!operationID) {
        operationID = createEnterpriseArchiveSaveOperationID()
        saveOperationIDsRef.current.set(operationKey, operationID)
      }
      const scriptsToPersist = retainedAtomicSave?.scriptsToPersist ?? currentReadySaves
        .flatMap(entry => entry.scripts)
        .map(script => sourceProjectId
          ? { ...script, project_id: sourceProjectId }
          : script)
      // Publish the full retry scope before the request yields. Source/options
      // are locked for the physical request, and route invalidation preserves
      // this exact payload/identity until the promise settles.
      retainAtomicSave(retainedAtomicSave ?? {
        operationKey,
        operationId: operationID,
        projectId: sourceProjectId,
        folderId: destinationFolderId,
        optionsIdentity: sourceOptionsIdentity,
        entries: currentReadySaves.map(entry => ({
          itemId: entry.item.id,
          file: entry.item.file,
          harFile: entry.item.harFile,
          supplementalFiles: entry.item.supplementalFiles,
          result: entry.result,
          scripts: entry.scripts,
        })),
        scriptsToPersist,
      })
      const atomicSaveScope = { operationKey, operationId: operationID }
      atomicSaveInFlightRef.current = atomicSaveScope
      setAtomicSaveInProgress(true)
      try {
        const save = () => api.saveEnterpriseArchiveImport({
          scripts: scriptsToPersist,
          folder_id: folderId ?? null,
          operation_id: operationID,
        })
        let response
        try {
          response = await save()
        } catch (saveError: unknown) {
          // Reconcile an interrupted/5xx response once with the same operation
          // identity. The server receipt either returns the committed batch or
          // safely starts the whole transaction again.
          if (saveError instanceof ApiError && saveError.status < 500) throw saveError
          response = await save()
        }
        if (response.scripts.length !== scriptsToPersist.length) {
          throw new Error(`Atomic save returned ${response.scripts.length} of ${scriptsToPersist.length} scripts.`)
        }
        if (!operationIsCurrent() || currentReadySaves.some(({ item }) => !requestIsCurrent(item))) {
          saveOperationIDsRef.current.delete(operationKey)
          if (retainedAtomicSaveRef.current?.operationId === operationID) {
            retainAtomicSave(null)
          }
          onImported?.({ scripts: response.scripts, stayInImportReview: true })
          currentReadySaves.forEach(entry => {
            if (sourceProjectId && entry.result.source_secret_session_id) {
              void discardSourceSecretSession(sourceProjectId, entry.result.source_secret_session_id)
            }
          })
          if (operationGenerationRef.current === operationGeneration) {
            setWorkingMode(null)
          }
          return
        }
        let savedOffset = 0
        for (const entry of currentReadySaves) {
          const entryScripts = response.scripts
            .slice(savedOffset, savedOffset + entry.scripts.length)
            .map((script, index) => withFallbackPersistedScript(script, entry.scripts[index]))
          savedOffset += entry.scripts.length
          savedScripts.push(...entryScripts)
          successCount += 1
          const patch: Partial<EnterpriseArchiveImportQueueItem> = {
            status: 'converted',
            result: entry.result,
            reviewMode: 'conversion',
            reviewOptionsIdentity: sourceOptionsIdentity,
            savedResultScripts: Object.fromEntries(entryScripts.map((script, index) => [index, script])),
            sourceSecretProjectId: sourceProjectId || '',
            error: '',
            notice: `Converted and saved ${entryScripts.length} script${entryScripts.length === 1 ? '' : 's'} as part of one atomic batch.`,
          }
          conversionItemPatches.set(entry.item.id, patch)
          updateImportItem(entry.item.id, patch)
        }
        saveOperationIDsRef.current.delete(operationKey)
        if (retainedAtomicSaveRef.current?.operationId === operationID) {
          retainAtomicSave(null)
        }
      } catch (saveError: unknown) {
        if (!operationIsCurrent() || currentReadySaves.some(({ item }) => !requestIsCurrent(item))) {
          skippedCount += currentReadySaves.length
        } else {
          atomicSaveNeedsFreshDraft = enterpriseArchiveDraftNeedsRefresh(saveError)
          if (atomicSaveNeedsFreshDraft) {
            // Do not leave the next Convert action pinned to a capability the
            // server has explicitly said can no longer authorize a save.
            saveOperationIDsRef.current.delete(operationKey)
            if (retainedAtomicSaveRef.current?.operationId === operationID) {
              retainAtomicSave(null)
            }
          } else {
            atomicSaveUnconfirmed = true
          }
          failureCount += currentReadySaves.length
          for (const entry of currentReadySaves) {
            const patch: Partial<EnterpriseArchiveImportQueueItem> = {
              // Preserve a retry path only for ambiguous outcomes. A definitive
              // expired-draft response needs a new conversion and attestation.
              status: 'validated',
              result: entry.result,
              reviewMode: 'validation',
              reviewOptionsIdentity: sourceOptionsIdentity,
              savedResultScripts: {},
              sourceSecretProjectId: sourceProjectId || '',
              error: getErrorMessage(saveError, 'Atomic batch save failed'),
              notice: atomicSaveNeedsFreshDraft
                ? 'The import draft is invalid or expired and could not be saved. Choose Convert again to generate a fresh draft; no individual script save will be attempted.'
                : 'APEX could not confirm the atomic batch save. Choose Convert again to reconcile the same operation; no individual script save will be attempted.',
            }
            conversionItemPatches.set(entry.item.id, patch)
            updateImportItem(entry.item.id, patch)
          }
        }
      } finally {
        if (atomicSaveInFlightRef.current === atomicSaveScope) {
          atomicSaveInFlightRef.current = null
          setAtomicSaveInProgress(false)
          // Logical invalidation may have advanced the generation, but it did
          // not cancel this POST. Lower the busy fence only now that it settled.
          setWorkingMode(null)
        }
      }
    }

    if (operationGenerationRef.current !== operationGeneration || reviewProjectIdRef.current !== sourceProjectId) return
    setWorkingMode(null)

    // Archives fenced mid-batch (a removed queue row, a re-attached source file)
    // are neither converted nor failed, so name them instead of quietly dropping
    // them out of the completion count.
    const skippedSuffix = skippedCount > 0 ? `; ${skippedCount} archive${skippedCount === 1 ? '' : 's'} skipped` : ''
    if (savedScripts.length > 0) {
      const projectedItems = importItemsRef.current.map(item => {
        const patch = conversionItemPatches.get(item.id)
        return patch ? { ...item, ...patch } : item
      })
      const savedNotice = `Converted and saved ${savedScripts.length} script${savedScripts.length === 1 ? '' : 's'} to the library${failureCount > 0 ? `; ${failureCount} archive${failureCount === 1 ? '' : 's'} failed` : ''}${skippedSuffix}.`
      await publishSavedEnterpriseArchiveScripts(
        projectedItems,
        savedScripts,
        `${savedNotice} Continue reviewing the remaining archive drafts before leaving this import.`,
        savedNotice,
        () => operationGenerationRef.current === operationGeneration
          && reviewProjectIdRef.current === sourceProjectId,
        destination,
      )
      currentReadySaves.forEach(entry => {
        if (sourceProjectId && entry.result.source_secret_session_id) {
          void discardSourceSecretSession(sourceProjectId, entry.result.source_secret_session_id)
        }
      })
      return
    }

    if (atomicSaveUnconfirmed) {
      setNotice('APEX could not confirm the atomic batch save. Choose Convert again to reconcile the same operation; no individual script save will be attempted.')
      return
    }

    if (atomicSaveNeedsFreshDraft) {
      setNotice('The import draft is invalid or expired and could not be saved. Choose Convert again to generate a fresh draft; no individual script save will be attempted.')
      return
    }

    if (targetItems.length > 1) {
      setNotice(`Converted ${successCount} of ${targetItems.length} enterprise script archive${targetItems.length === 1 ? '' : 's'}${failureCount > 0 ? `; ${failureCount} failed` : ''}${skippedSuffix}.`)
    }
  }

  const startBrowserReplayValidation = async () => {
    if (!importedScript || !selectedItem) return
    const currentOptionsIdentity = importOptionsIdentity(optionsRef.current)
    if (selectedItem.reviewOptionsIdentity !== currentOptionsIdentity) {
      setError('Conversion options changed after this review. Refresh the review before starting draft browser replay.')
      return false
    }
    const currentItem = importItemsRef.current.find(item => item.id === selectedItem.id)
    if (
      selectedItemIdRef.current !== selectedItem.id
      || selectedResultScriptIndexRef.current !== activeResultScriptIndex
      || currentItem?.reviewGeneration !== selectedItem.reviewGeneration
      || currentItem.reviewOptionsIdentity !== currentOptionsIdentity
      || getEnterpriseArchiveResultScripts(currentItem.result)[activeResultScriptIndex] !== importedScript
    ) return false
    const script = importedScript
    const sourceItemId = selectedItem.id
    const sourceGeneration = selectedItem.reviewGeneration
    const sourceScriptIndex = activeResultScriptIndex
    const sourceProjectId = reviewProjectIdRef.current
    const sourceOptionsIdentity = selectedItem?.reviewOptionsIdentity || ''
    const activeStart = replayStartScopeRef.current
    if (
      activeStart?.itemId === sourceItemId
      && activeStart.generation === sourceGeneration
      && activeStart.scriptIndex === sourceScriptIndex
      && activeStart.projectId === sourceProjectId
      && activeStart.optionsIdentity === sourceOptionsIdentity
      && activeStart.script === script
    ) return false
    const startScope = {
      itemId: sourceItemId,
      generation: sourceGeneration,
      scriptIndex: sourceScriptIndex,
      projectId: sourceProjectId,
      optionsIdentity: sourceOptionsIdentity,
      script,
    }
    replayStartScopeRef.current = startScope
    const previousReplaySession = selectedItem.replayValidationSession
    const previousReplayWarnings = selectedItem.replayValidationWarnings
    const clientReplaySessionId = createReplaySessionId()
    const requestToken = nextRequestToken(replayRequestTokensRef.current, sourceItemId)
    const requestOwnsSourceItem = () => {
      const current = importItemsRef.current.find(item => item.id === sourceItemId)
      const currentScripts = getEnterpriseArchiveResultScripts(current?.result || null)
      return replayRequestTokensRef.current.get(sourceItemId) === requestToken
        && reviewProjectIdRef.current === sourceProjectId
        && current?.reviewOptionsIdentity === sourceOptionsIdentity
        && importOptionsIdentity(optionsRef.current) === sourceOptionsIdentity
        && current?.reviewGeneration === sourceGeneration
        && currentScripts[sourceScriptIndex] === script
    }
    const requestIsCurrent = () => requestOwnsSourceItem()
      && selectedItemIdRef.current === sourceItemId
      && selectedResultScriptIndexRef.current === sourceScriptIndex

    updateImportItem(sourceItemId, {
      replayValidationLoading: true,
      replayValidationError: '',
      replayValidationSession: clientReplaySessionId
        ? {
            id: clientReplaySessionId,
            status: 'queued',
            group: `${script.name || 'script'}-replay`,
            protocol: 'browser',
            current_event_index: -1,
            events: [],
          }
        : previousReplaySession,
      replayValidationWarnings: [],
      replayReviewGeneration: sourceGeneration,
      replayScriptIndex: sourceScriptIndex,
      replayProjectId: sourceProjectId || '',
    })

    try {
      const res = await api.startDraftBrowserReplaySession(buildReplayStartPayload(script, {
        projectId: sourceProjectId,
        ignoreThinkTime: true,
        replaySessionId: clientReplaySessionId || undefined,
      }))
      if (!requestIsCurrent()) return false
      updateImportItem(sourceItemId, {
        replayValidationSession: res.session,
        replayValidationWarnings: Array.isArray(res.warnings) ? res.warnings : [],
      })
      return true
    } catch (error: unknown) {
      if (!requestIsCurrent()) return false
      if (clientReplaySessionId && !isDefiniteReplayStartRejection(error)) {
        updateImportItem(sourceItemId, {
          replayValidationError: '',
          replayValidationWarnings: ['The replay start response was interrupted. Confirming the accepted replay session…'],
        })
        return true
      }
      updateImportItem(sourceItemId, {
        replayValidationSession: previousReplaySession,
        replayValidationWarnings: previousReplayWarnings,
        replayValidationError: getErrorMessage(error, 'Draft browser replay failed to start'),
      })
      return false
    } finally {
      if (requestOwnsSourceItem()) updateImportItem(sourceItemId, { replayValidationLoading: false })
      if (replayStartScopeRef.current === startScope) replayStartScopeRef.current = null
    }
  }

  const handleSaveConverted = async (destination: ImportSaveDestination = 'stay') => {
    const preparedReview = latestPreparedReviewRef.current
    const currentImportedScript = preparedReview.importedScript
    if (!currentImportedScript || !selectedItem) return
    if (enterpriseArchiveResultScriptIsSaved(selectedItem, activeResultScriptIndex)) {
      setNotice('This generated script is already saved. Continue with the next unsaved script.')
      return
    }
    if (!reviewMatchesCurrentOptions) {
      setError('Conversion options changed after this review. Refresh the review before saving the script.')
      return
    }
    let requestIsCurrent = () => false
    let saveScope: typeof scriptSaveInFlightRef.current = null
    try {
      const sourceItemId = selectedItem.id
      const sourceGeneration = selectedItem.reviewGeneration
      const sourceOperationGeneration = operationGenerationRef.current
      const sourceScriptIndex = activeResultScriptIndex
      const sourceProjectId = reviewProjectIdRef.current
      const sourceOptionsIdentity = selectedItem.reviewOptionsIdentity || ''
      const activeSave = scriptSaveInFlightRef.current
      if (
        activeSave?.itemId === sourceItemId
        && activeSave.generation === sourceGeneration
        && activeSave.scriptIndex === sourceScriptIndex
        && activeSave.projectId === sourceProjectId
        && activeSave.optionsIdentity === sourceOptionsIdentity
        && activeSave.script === currentImportedScript
      ) return
      saveScope = {
        itemId: sourceItemId,
        generation: sourceGeneration,
        operationGeneration: sourceOperationGeneration,
        scriptIndex: sourceScriptIndex,
        projectId: sourceProjectId,
        optionsIdentity: sourceOptionsIdentity,
        script: currentImportedScript,
      }
      scriptSaveInFlightRef.current = saveScope
      requestIsCurrent = () => {
        const current = importItemsRef.current.find(item => item.id === sourceItemId)
        return Boolean(sourceItemId)
          && operationGenerationRef.current === sourceOperationGeneration
          && current?.reviewGeneration === sourceGeneration
          && getEnterpriseArchiveResultScripts(current?.result || null)[sourceScriptIndex] === currentImportedScript
          && selectedItemIdRef.current === sourceItemId
          && selectedResultScriptIndexRef.current === sourceScriptIndex
          && reviewProjectIdRef.current === sourceProjectId
          && current?.reviewOptionsIdentity === sourceOptionsIdentity
          && importOptionsIdentity(optionsRef.current) === sourceOptionsIdentity
      }
      if (
        preparedReview.secretBindingRequirements.length > 0
        && !requirementsPreserveDestinationValues(preparedReview.secretBindingRequirements)
      ) {
        setError('Resolve every redacted credential binding before saving this script.')
        return
      }
      if (saveBlockedByProtocolGate) {
        setNotice('This import is gated by protocol classification. Save the review artifact instead of promoting it as an executable script.')
        return
      }
      if (saveBlockedByMigrationPolicy) {
        setError('Saving is disabled by the Migration Block Policy configured in Conversion Options. Relax that policy or resolve the blocking runtime settings first.')
        return
      }
      if (browserReplayInProgress) {
        setNotice('Draft browser replay is still running. Wait for it to finish before saving it to the library.')
        return
      }
      if (browserReplayNeedsRerun) {
        const wasStarted = browserReplayWasStarted
        const started = await startBrowserReplayValidation()
        if (started) {
          setBrowserReplayStartedNotice(wasStarted)
        }
        return
      }
      if (!requestIsCurrent()) return
      let scriptToSave = browserImport
        ? attachTrueClientPromotionReview(currentImportedScript, {
          compatReport: trueClient2CompatReport,
          replaySession: replayValidationSession,
          replayWarnings: replayValidationWarnings,
          importWarnings: preparedReview.result?.warnings || [],
        })
        : currentImportedScript
      // Persist any user-attached classpath JARs into the saved script config so
      // the runner can materialize them at launch. Only when the feature flag is
      // on (parity: nothing emitted when off), and only bindings whose entry is
      // still in the saved script's classpath (drop stale/orphaned attachments
      // left by a re-review that changed the classpath).
      if (javaClasspathUploadEnabled) {
        const savedClasspath = scriptToSave.config?.java_vuser?.classpath ?? []
        const attachedClasspathAssets = (classpathAssetsByKeyRef.current[javaClasspathReviewKey(sourceProjectId, sourceItemId, sourceScriptIndex)] ?? [])
          .filter(asset => savedClasspath.includes(asset.entry))
        if (attachedClasspathAssets.length > 0) {
          scriptToSave = {
            ...scriptToSave,
            config: {
              ...scriptToSave.config,
              java_vuser: {
                ...(scriptToSave.config?.java_vuser ?? {}),
                classpath_assets: attachedClasspathAssets,
              },
            },
          }
        }
      }
      setScriptSaveInProgress(true)
      const operationKey = [
        sourceItemId,
        String(sourceGeneration),
        String(sourceScriptIndex),
        sourceProjectId || '',
        folderId || '',
        sourceOptionsIdentity,
      ].join('\u0000')
      let operationID = saveOperationIDsRef.current.get(operationKey)
      if (!operationID) {
        operationID = createEnterpriseArchiveSaveOperationID()
        saveOperationIDsRef.current.set(operationKey, operationID)
      }
      saveScope.operationKey = operationKey
      saveScope.operationId = operationID
      const response = await api.saveEnterpriseArchiveImport({
        scripts: [sourceProjectId ? { ...scriptToSave, project_id: sourceProjectId } : scriptToSave],
        folder_id: folderId ?? null,
        operation_id: operationID,
      })
      const savedScript = response.scripts[0]
      if (!savedScript || response.scripts.length !== 1) {
        throw new Error('Atomic save did not return the generated script.')
      }
      const persistedScript = withFallbackPersistedScript(savedScript, scriptToSave)
      if (!requestIsCurrent()) {
        onImported?.({ scripts: [persistedScript], stayInImportReview: true })
        setNotice(`Saved ${persistedScript.name} before the import review changed. Refresh the script library before retrying this lane.`)
        return
      }
      saveOperationIDsRef.current.delete(operationKey)
      const nextItems = importItemsRef.current.map(item => item.id !== sourceItemId
        ? item
        : {
            ...item,
            savedResultScripts: {
              ...item.savedResultScripts,
              [sourceScriptIndex]: persistedScript,
            },
            notice: `Saved ${persistedScript.name} to the script library.`,
          })
      await publishSavedEnterpriseArchiveScripts(
        nextItems,
        [persistedScript],
        `Saved ${persistedScript.name} to the script library. Continue reviewing the remaining generated scripts before leaving this import.`,
        `Saved ${persistedScript.name} to the script library.`,
        requestIsCurrent,
        destination,
      )
      const updatedSourceItem = nextItems.find(item => item.id === sourceItemId)
      const sourceScriptCount = getEnterpriseArchiveResultScripts(updatedSourceItem?.result || null).length
      const everySourceScriptSaved = sourceScriptCount > 0
        && Array.from({ length: sourceScriptCount }, (_unused, index) => (
          Boolean(updatedSourceItem?.savedResultScripts?.[index])
        )).every(Boolean)
      if (
        everySourceScriptSaved
        && sourceProjectId
        && preparedReview.result?.source_secret_session_id
      ) {
        void discardSourceSecretSession(sourceProjectId, preparedReview.result.source_secret_session_id)
      }
    } catch (error: unknown) {
      if (requestIsCurrent()) setError(getErrorMessage(error, 'Failed to save script'))
    } finally {
      if (scriptSaveInFlightRef.current === saveScope) {
        scriptSaveInFlightRef.current = null
        setScriptSaveInProgress(false)
      }
    }
  }

  const handleSaveCredentialBatch = async () => {
    const preparedReview = latestPreparedReviewRef.current
    const sourceItemId = preparedReview.itemId
    const sourceGeneration = preparedReview.generation
    const sourceProjectId = preparedReview.projectId
    const sourceOptionsIdentity = preparedReview.optionsIdentity
    const sourceResult = preparedReview.result
    const sourceItem = importItemsRef.current.find(item => item.id === sourceItemId)
    if (!sourceItem || !sourceResult || sourceItem.reviewGeneration !== sourceGeneration) return
    const sourceScripts = getEnterpriseArchiveResultScripts(sourceResult)
    const pendingIndices = sourceScripts.flatMap((_script, index) => (
      enterpriseArchiveResultScriptIsSaved(sourceItem, index) ? [] : [index]
    ))
    if (pendingIndices.length <= 1) {
      await handleSaveConverted()
      return
    }
    const unresolvedRequirements = pendingIndices.flatMap(index => (
      secretBindingRequirementsForScript(sourceResult.secret_binding_requirements, index, sourceScripts[index])
        .filter(requirement => requirement.destination_value_preserved !== true)
    ))
    if (unresolvedRequirements.length > 0) {
      setError(`Prepare the remaining ${unresolvedRequirements.length} credential ${unresolvedRequirements.length === 1 ? 'choice' : 'choices'} before the atomic script save.`)
      return
    }
    if (saveBlockedByProtocolGate || saveBlockedByMigrationPolicy) {
      setError('The atomic script save is unavailable until the active protocol and migration gates are resolved.')
      return
    }
    if ((sourceResult.parameterization?.suggestions || []).length > 0) {
      setError('Apply or dismiss the archive parameterization review before saving the generated scripts atomically.')
      return
    }
    if (pendingIndices.some(index => isBrowserImport(sourceScripts[index]))) {
      setError('Browser scripts require lane-specific replay evidence. Review and save each browser lane after replay validation.')
      return
    }

    const sourceOperationGeneration = operationGenerationRef.current
    const saveScope: NonNullable<typeof scriptSaveInFlightRef.current> = {
      itemId: sourceItemId,
      generation: sourceGeneration,
      operationGeneration: sourceOperationGeneration,
      scriptIndex: preparedReview.scriptIndex,
      projectId: sourceProjectId,
      optionsIdentity: sourceOptionsIdentity,
      script: preparedReview.importedScript || sourceScripts[pendingIndices[0]],
    }
    if (scriptSaveInFlightRef.current) return
    scriptSaveInFlightRef.current = saveScope
    setScriptSaveInProgress(true)
    setError('')

    // Secret preparation mutates the review synchronously, before React can
    // publish the updated queue item. Keep the queue ref aligned so the batch
    // response is fenced against the exact credential-resolved draft.
    const preparedItems = importItemsRef.current.map(item => item.id === sourceItemId
      ? { ...item, result: sourceResult }
      : item)
    importItemsRef.current = preparedItems
    setImportItems(preparedItems)
    const requestOwnsSourceItem = () => {
      const current = importItemsRef.current.find(item => item.id === sourceItemId)
      return operationGenerationRef.current === sourceOperationGeneration
        && reviewProjectIdRef.current === sourceProjectId
        && current?.reviewGeneration === sourceGeneration
        && current?.result === sourceResult
        && current?.reviewOptionsIdentity === sourceOptionsIdentity
        && importOptionsIdentity(optionsRef.current) === sourceOptionsIdentity
    }

    const scriptsToSave = pendingIndices.map(index => {
      let script = sourceScripts[index]
      if (javaClasspathUploadEnabled && script.config?.java_vuser?.classpath?.length) {
        const savedClasspath = script.config.java_vuser.classpath
        const classpathAssets = (classpathAssetsByKeyRef.current[javaClasspathReviewKey(sourceProjectId, sourceItemId, index)] ?? [])
          .filter(asset => savedClasspath.includes(asset.entry))
        if (classpathAssets.length > 0) {
          script = {
            ...script,
            config: {
              ...script.config,
              java_vuser: {
                ...script.config.java_vuser,
                classpath_assets: classpathAssets,
              },
            },
          }
        }
      }
      return sourceProjectId ? { ...script, project_id: sourceProjectId } : script
    })
    const operationKey = [
      sourceItemId,
      String(sourceGeneration),
      'credential-batch',
      sourceProjectId || '',
      folderId || '',
      sourceOptionsIdentity,
      ...pendingIndices.map(index => String(index)),
    ].join('\u0000')
    let operationID = saveOperationIDsRef.current.get(operationKey)
    if (!operationID) {
      operationID = createEnterpriseArchiveSaveOperationID()
      saveOperationIDsRef.current.set(operationKey, operationID)
    }
    saveScope.operationKey = operationKey
    saveScope.operationId = operationID

    try {
      const response = await api.saveEnterpriseArchiveImport({
        scripts: scriptsToSave,
        folder_id: folderId ?? null,
        operation_id: operationID,
      })
      if (response.scripts.length !== scriptsToSave.length) {
        throw new Error(`Atomic save returned ${response.scripts.length} of ${scriptsToSave.length} scripts.`)
      }
      const persistedScripts = response.scripts.map((script, index) => (
        withFallbackPersistedScript(script, scriptsToSave[index])
      ))
      if (!requestOwnsSourceItem()) {
        onImported?.({ scripts: persistedScripts, stayInImportReview: true })
        return
      }
      saveOperationIDsRef.current.delete(operationKey)
      const savedResultScripts = { ...sourceItem.savedResultScripts }
      pendingIndices.forEach((scriptIndex, index) => {
        savedResultScripts[scriptIndex] = persistedScripts[index]
      })
      const nextItems = importItemsRef.current.map(item => item.id === sourceItemId
        ? {
            ...item,
            result: sourceResult,
            savedResultScripts,
            notice: `Saved ${persistedScripts.length} generated scripts as one atomic batch.`,
          }
        : item)
      await publishSavedEnterpriseArchiveScripts(
        nextItems,
        persistedScripts,
        `Saved ${persistedScripts.length} generated scripts atomically. Continue with the remaining archives.`,
        `Saved ${persistedScripts.length} generated scripts atomically.`,
        requestOwnsSourceItem,
      )
      if (sourceProjectId && sourceResult.source_secret_session_id) {
        void discardSourceSecretSession(sourceProjectId, sourceResult.source_secret_session_id)
      }
    } catch (error: unknown) {
      if (requestOwnsSourceItem()) {
        setError(`${getErrorMessage(error, 'Failed to save the generated script batch')} No script was reported complete; retrying reuses the same atomic save operation.`)
      }
    } finally {
      if (scriptSaveInFlightRef.current === saveScope) {
        scriptSaveInFlightRef.current = null
        setScriptSaveInProgress(false)
      }
    }
  }

  const handleSecretBindingsApply = async (bindings: SecretBindingSelection[]) => {
    if (!selectedItem || !result || !importedScript) return
    const sourceItemId = selectedItem.id
    const sourceGeneration = selectedItem.reviewGeneration
    const sourceProjectId = reviewProjectIdRef.current
    const sourceScriptIndex = selectedResultScriptIndexRef.current
    const requestIsCurrent = () => {
      const currentItem = importItemsRef.current.find(item => item.id === sourceItemId)
      return selectedItemIdRef.current === sourceItemId
        && currentItem?.reviewGeneration === sourceGeneration
        && reviewProjectIdRef.current === sourceProjectId
        && selectedResultScriptIndexRef.current === sourceScriptIndex
    }
    const assertCurrent = () => {
      if (!requestIsCurrent()) throw new Error('The import result changed while credentials were being prepared. Review the current draft and try again.')
    }
    let resolvedResult = {
      ...result,
      secret_binding_requirements: updateSecretBindingRequirementNames(
        result.secret_binding_requirements,
        result.source_secret_session_id
          ? (settledSourceSecretSelectionsRef.current.get(result.source_secret_session_id) ?? [])
          : [],
      ),
    }
    for (let scriptIndex = 0; scriptIndex < resultScripts.length; scriptIndex += 1) {
      const script = resolvedResult.scripts?.[scriptIndex] || (scriptIndex === 0 ? resolvedResult.script : undefined)
      if (!script) continue
      const requirements = secretBindingRequirementsForScript(resolvedResult.secret_binding_requirements, scriptIndex, script)
      const requirementIDs = new Set(requirements.map(requirement => requirement.id))
      const scriptBindings = bindings
        .filter(binding => (
          typeof binding.script_index === 'number'
            ? binding.script_index === scriptIndex
            : requirementIDs.has(binding.requirement_id)
        ))
        .map(({ requirement_id, secret_name }) => ({ requirement_id, secret_name }))
      if (scriptBindings.length === 0) continue
      try {
        assertCurrent()
        const resolved = await api.applySecretBindings({ script, script_index: scriptIndex, bindings: scriptBindings })
        assertCurrent()
        const currentScripts = getEnterpriseArchiveResultScripts(resolvedResult)
        const nextScripts = currentScripts.map((entry, index) => index === scriptIndex ? resolved.script : entry)
        resolvedResult = {
          ...resolvedResult,
          script: nextScripts[0],
          scripts: nextScripts,
          ...(nextScripts.length === 1
            ? { import_draft_expires_at: resolved.import_draft_expires_at }
            : {}),
          secret_binding_requirements: replaceSecretBindingRequirementsForScript(
            resolvedResult.secret_binding_requirements,
            scriptIndex,
            script,
            resolved.secret_binding_requirements,
          ),
        }
      } catch (error: unknown) {
        if (!requestIsCurrent()) throw error
        const refreshed = refreshedSecretBindingRequirements(error)
        if (refreshed) {
          resolvedResult = {
            ...resolvedResult,
            secret_binding_requirements: replaceSecretBindingRequirementsForScript(
              resolvedResult.secret_binding_requirements,
              scriptIndex,
              script,
              refreshed,
            ),
          }
          updateImportItem(selectedItem.id, { result: resolvedResult })
        }
        throw error
      }
    }
    assertCurrent()
    const nextScripts = getEnterpriseArchiveResultScripts(resolvedResult)
    const previewIndex = Math.min(selectedResultScriptIndexRef.current, Math.max(nextScripts.length - 1, 0))
    latestPreparedReviewRef.current = {
      itemId: sourceItemId,
      generation: sourceGeneration,
      scriptIndex: previewIndex,
      projectId: sourceProjectId,
      optionsIdentity: selectedItem.reviewOptionsIdentity || '',
      importedScript: nextScripts[previewIndex] || null,
      result: resolvedResult,
      secretBindingRequirements: nextScripts[previewIndex]
        ? secretBindingRequirementsForScript(
            resolvedResult.secret_binding_requirements,
            previewIndex,
            nextScripts[previewIndex],
          )
        : [],
    }
    nextRequestToken(replayRequestTokensRef.current, selectedItem.id)
    updateImportItem(selectedItem.id, {
      result: resolvedResult,
      reviewMode: 'conversion',
      status: 'converted',
      replayValidationLoading: false,
      replayValidationError: '',
      replayValidationWarnings: [],
      replayValidationSession: null,
      replayReviewGeneration: -1,
      replayScriptIndex: -1,
      replayProjectId: '',
      error: '',
      notice: `Secret bindings applied to ${nextScripts[previewIndex]?.name || 'the selected script'}. Other generated scripts remain independent.`,
    })
  }

  const handleSourceSecretSessionSettled = (
    sessionId: string,
    migrationSelections: SecretBindingSelection[],
  ) => {
    if (!selectedItem) return
    settledSourceSecretSelectionsRef.current.set(sessionId, migrationSelections)
    const current = importItemsRef.current.find(item => item.id === selectedItem.id)
    if (current?.result?.source_secret_session_id !== sessionId) return
    markSourceSecretSessionSettled(
      current.sourceSecretProjectId || reviewProjectIdRef.current || '',
      sessionId,
    )
    updateImportItem(selectedItem.id, {
      result: {
        ...current.result,
        secret_binding_requirements: updateSecretBindingRequirementNames(
          current.result.secret_binding_requirements,
          migrationSelections,
        ),
        source_secret_session_id: undefined,
        source_secret_session_expires_at: undefined,
        source_secret_migration_allowed: false,
      },
    })
  }

  const handlePrepareEnterpriseCredentialsForBatch = () => {
    const preparedReview = latestPreparedReviewRef.current
    const preparedResult = preparedReview.result
    const currentItem = importItemsRef.current.find(item => item.id === preparedReview.itemId)
    if (!preparedResult || !currentItem) return
    const scripts = getEnterpriseArchiveResultScripts(preparedResult)
    const pendingIndices = scripts.flatMap((_script, index) => (
      enterpriseArchiveResultScriptIsSaved(currentItem, index) ? [] : [index]
    ))
    const nextCredentialIndex = pendingIndices.find(index => (
      index !== preparedReview.scriptIndex
      && secretBindingRequirementsForScript(
        preparedResult.secret_binding_requirements,
        index,
        scripts[index],
      ).some(requirement => requirement.destination_value_preserved !== true)
    ))
    if (nextCredentialIndex !== undefined) {
      selectEnterpriseResultScript(nextCredentialIndex, true)
      return
    }
    const unresolvedCurrent = secretBindingRequirementsForScript(
      preparedResult.secret_binding_requirements,
      preparedReview.scriptIndex,
      scripts[preparedReview.scriptIndex],
    ).some(requirement => requirement.destination_value_preserved !== true)
    if (unresolvedCurrent) {
      setError('Finish the selected script credential choices before continuing to the atomic save.')
      return
    }
    void handleSaveCredentialBatch()
  }

  const handleParameterizationApplied = (
    parameterizedScript: ScriptDefinition,
    _diffs: unknown[],
    _selectedIds: string[],
    importDraftExpiresAt?: string,
    refreshedRequirements?: EnterpriseArchiveImportResult['secret_binding_requirements'],
  ) => {
    if (!selectedItem || !result || !importedScript) return
    const nextScripts = resultScripts.map((script, index) => index === activeResultScriptIndex ? parameterizedScript : script)
    const nextResult = {
      ...result,
      ...(refreshedRequirements ? { secret_binding_requirements: replaceSecretBindingRequirementsForScript(result.secret_binding_requirements, activeResultScriptIndex, parameterizedScript, refreshedRequirements.map(requirement => ({ ...requirement, script_index: activeResultScriptIndex }))) } : {}),
      script: nextScripts[0],
      scripts: nextScripts,
      ...(nextScripts.length === 1 ? { import_draft_expires_at: importDraftExpiresAt } : {}),
    }
    latestPreparedReviewRef.current = {
      itemId: selectedItem.id,
      generation: selectedItem.reviewGeneration + 1,
      scriptIndex: activeResultScriptIndex,
      projectId: reviewProjectIdRef.current,
      optionsIdentity: selectedItem.reviewOptionsIdentity || '',
      importedScript: parameterizedScript,
      result: nextResult,
      secretBindingRequirements: secretBindingRequirementsForScript(nextResult.secret_binding_requirements, activeResultScriptIndex, parameterizedScript),
    }
    nextRequestToken(replayRequestTokensRef.current, selectedItem.id)
    updateImportItem(selectedItem.id, {
      result: nextResult,
      reviewGeneration: selectedItem.reviewGeneration + 1,
      replayValidationLoading: false,
      replayValidationError: '',
      replayValidationWarnings: [],
      replayValidationSession: null,
      replayReviewGeneration: -1,
      replayScriptIndex: -1,
      replayProjectId: '',
      error: '',
      notice: 'Parameterization applied to the reviewed script.',
    })
  }

  const handleSharedValuesApplied = (
    updatedScripts: ScriptDefinition[],
    _diffs: unknown[],
    refreshed?: SharedValues,
    importDraftExpiresAt?: (string | null)[],
  ) => {
    if (!selectedItem || !result || updatedScripts.length === 0) return
    const previewIndex = Math.min(activeResultScriptIndex, updatedScripts.length - 1)
    const rotatedExpiry = earliestImportDraftExpiry(importDraftExpiresAt)
    const nextResult: EnterpriseArchiveImportResult = {
      ...result,
      script: updatedScripts[0],
      scripts: updatedScripts,
      ...(refreshed ? { shared_values: refreshed } : {}),
      ...(rotatedExpiry ? { import_draft_expires_at: rotatedExpiry } : {}),
    }
    latestPreparedReviewRef.current = {
      itemId: selectedItem.id,
      generation: selectedItem.reviewGeneration + 1,
      scriptIndex: previewIndex,
      projectId: reviewProjectIdRef.current,
      optionsIdentity: selectedItem.reviewOptionsIdentity || '',
      importedScript: updatedScripts[previewIndex],
      result: nextResult,
      secretBindingRequirements: secretBindingRequirementsForScript(
        nextResult.secret_binding_requirements,
        previewIndex,
        updatedScripts[previewIndex],
      ),
    }
    nextRequestToken(replayRequestTokensRef.current, selectedItem.id)
    updateImportItem(selectedItem.id, {
      result: nextResult,
      reviewGeneration: selectedItem.reviewGeneration + 1,
      replayValidationLoading: false,
      replayValidationError: '',
      replayValidationWarnings: [],
      replayValidationSession: null,
      replayReviewGeneration: -1,
      replayScriptIndex: -1,
      replayProjectId: '',
      error: '',
      notice: 'Collection values updated every generated script.',
    })
  }

  const handleSaveReviewArtifact = async () => {
    const preparedReview = latestPreparedReviewRef.current
    const currentResult = preparedReview.result
    const currentReviewArtifact = currentResult?.review_artifact ?? currentResult?.summary?.review_artifact ?? null
    const currentDetectedProtocol = currentResult?.detected_protocol ?? currentResult?.summary?.detected_protocol ?? null
    if (!currentReviewArtifact || !preparedReview.itemId || classpathUploadInFlightRef.current) return
    const preparedImportedScript = preparedReview.importedScript
    const sourceDraft = preparedImportedScript || buildReviewArtifactDraft(
      currentReviewArtifact,
      currentDetectedProtocol,
      preparedReview.projectId,
      optionsRef.current.name,
      currentResult?.summary?.source_review || null,
    )
    const preparedDraft = bindJavaClasspathAssets(
      sourceDraft,
      javaClasspathUploadEnabled
        ? classpathAssetsByKeyRef.current[javaClasspathReviewKey(preparedReview.projectId, preparedReview.itemId, preparedReview.scriptIndex)]
        : undefined,
    )

    const saveScope: ImportSaveScope = {
      itemId: preparedReview.itemId,
      generation: preparedReview.generation,
      operationGeneration: operationGenerationRef.current,
      scriptIndex: preparedReview.scriptIndex,
      projectId: preparedReview.projectId,
      optionsIdentity: preparedReview.optionsIdentity,
      script: preparedDraft,
    }
    const activeSave = reviewArtifactSaveInFlightRef.current
    if (activeSave) return

    const requestIsCurrent = () => {
      const currentItem = importItemsRef.current.find(item => item.id === saveScope.itemId)
      const latestReview = latestPreparedReviewRef.current
      return operationGenerationRef.current === saveScope.operationGeneration
        && selectedItemIdRef.current === saveScope.itemId
        && selectedResultScriptIndexRef.current === saveScope.scriptIndex
        && reviewProjectIdRef.current === saveScope.projectId
        && currentItem?.reviewGeneration === saveScope.generation
        && currentItem?.reviewOptionsIdentity === saveScope.optionsIdentity
        && importOptionsIdentity(optionsRef.current) === saveScope.optionsIdentity
        && latestReview.itemId === saveScope.itemId
        && latestReview.generation === saveScope.generation
        && latestReview.scriptIndex === saveScope.scriptIndex
        && latestReview.projectId === saveScope.projectId
        && latestReview.optionsIdentity === saveScope.optionsIdentity
        && latestReview.importedScript === preparedImportedScript
        && latestReview.result === currentResult
    }
    if (!requestIsCurrent()) return
    if (
      preparedReview.secretBindingRequirements.length > 0
      && !requirementsPreserveDestinationValues(preparedReview.secretBindingRequirements)
    ) {
      setError('Resolve every redacted credential binding before saving this review artifact.')
      return
    }

    reviewArtifactSaveInFlightRef.current = saveScope
    setReviewArtifactSaving(true)
    setReviewArtifactPersistenceInProgress(true)
    setError('')
    try {
      const currentFidelityReport = currentResult?.fidelity_report ?? currentResult?.summary?.fidelity_report ?? null
      const currentCombinedJavaVuser = currentDetectedProtocol?.import_mode === 'external_runner_required' && (
        currentDetectedProtocol?.apexload_protocol === 'java_vuser' ||
        currentDetectedProtocol?.canonical_source_protocol === 'java_vuser' ||
        preparedDraft.protocol === 'java_vuser'
      )
      const currentCombinedJavaVuserApprovalReady = Boolean(
        currentCombinedJavaVuser &&
        currentFidelityReport?.promotion_status === 'promotable' &&
        (currentFidelityReport.blockers?.length || 0) === 0,
      )
      const session = await api.createAuthoringSession({
        project_id: saveScope.projectId,
        folder_id: folderId,
        source: 'source_review_artifact',
        name: preparedDraft.name || currentReviewArtifact.title || currentDetectedProtocol?.display_name || 'enterprise script archive protocol review',
        status: 'reviewing',
        idempotency_key: `enterprise-archive-review:${saveScope.itemId}:${saveScope.generation}:${saveScope.scriptIndex}`,
        draft: preparedDraft,
        review_state: {
          review_artifact: currentReviewArtifact,
          detected_protocol: currentDetectedProtocol,
          import_summary: currentResult?.summary || {},
          warnings: currentResult?.warnings || [],
          required_evidence: currentReviewArtifact.required_evidence || currentDetectedProtocol?.required_evidence || [],
        },
      })
      // Authoring sessions are durable even if the operator leaves while the
      // request is in flight. Refresh the parent-owned session list without
      // replacing whichever authoring surface is now active.
      onImported?.({ scripts: [], stayInImportReview: true })
      if (!requestIsCurrent()) return
      setSavedReviewSessions(previous => ({ ...previous, [`${saveScope.itemId}:${saveScope.scriptIndex}`]: saveScope.generation }))
      const dependencyReview = javaDependencyInventory(currentReviewArtifact.metadata)?.classification === 'java_dependency_bundle'
      setNotice(`${dependencyReview ? 'Dependency review' : currentCombinedJavaVuserApprovalReady ? 'Combined Java Vuser draft' : currentCombinedJavaVuser ? 'Combined Java Vuser review artifact' : 'Review artifact'} saved as authoring session ${session.name || session.id}.`)
    } catch (error: unknown) {
      if (requestIsCurrent()) setError(getErrorMessage(error, 'Failed to save review artifact'))
    } finally {
      if (reviewArtifactSaveInFlightRef.current === saveScope) {
        reviewArtifactSaveInFlightRef.current = null
        setReviewArtifactSaving(false)
        setReviewArtifactPersistenceInProgress(false)
      }
    }
  }

  const handleValidateBrowserReplay = async () => {
    const wasStarted = browserReplayWasStarted
    const started = await startBrowserReplayValidation()
    if (started) {
      setBrowserReplayStartedNotice(wasStarted)
    }
  }

  const selectEnterpriseQueueItem = (itemID: string, focusButton = false) => {
    if (settingsEditing || !importItems.some(item => item.id === itemID)) return
    if (focusButton) pendingQueueItemFocusRef.current = itemID
    setSelectedItemId(itemID)
    setError('')
    setNotice('')
  }

  const handleEnterpriseQueueItemKeyDown = (event: React.KeyboardEvent<HTMLButtonElement>, itemID: string) => {
    const index = importItems.findIndex(item => item.id === itemID)
    if (index < 0) return
    let nextIndex: number | null = null
    if (event.key === 'ArrowDown' || event.key === 'ArrowRight') nextIndex = Math.min(importItems.length - 1, index + 1)
    if (event.key === 'ArrowUp' || event.key === 'ArrowLeft') nextIndex = Math.max(0, index - 1)
    if (event.key === 'Home') nextIndex = 0
    if (event.key === 'End') nextIndex = importItems.length - 1
    if (nextIndex === null || nextIndex === index) return
    event.preventDefault()
    event.stopPropagation()
    selectEnterpriseQueueItem(importItems[nextIndex].id, true)
  }

  const selectEnterpriseResultScript = (index: number, focusButton = false) => {
    if (settingsEditing) return
    if (resultScripts.length === 0) return
    const nextIndex = Math.max(0, Math.min(resultScripts.length - 1, index))
    if (nextIndex === activeResultScriptIndex) return
    if (selectedItem) {
      nextRequestToken(replayRequestTokensRef.current, selectedItem.id)
      updateImportItem(selectedItem.id, {
        replayValidationLoading: false,
        replayValidationError: '',
        replayValidationWarnings: [],
        replayValidationSession: null,
        replayReviewGeneration: -1,
        replayScriptIndex: -1,
        replayProjectId: '',
      })
    }
    if (focusButton) pendingResultScriptFocusRef.current = nextIndex
    selectedResultScriptIndexRef.current = nextIndex
    setSelectedResultScriptIndex(nextIndex)
  }

  const handleEnterpriseResultScriptKeyDown = (event: React.KeyboardEvent<HTMLButtonElement>, index: number) => {
    let nextIndex: number | null = null
    if (event.key === 'ArrowDown' || event.key === 'ArrowRight') nextIndex = Math.min(resultScripts.length - 1, index + 1)
    if (event.key === 'ArrowUp' || event.key === 'ArrowLeft') nextIndex = Math.max(0, index - 1)
    if (event.key === 'Home') nextIndex = 0
    if (event.key === 'End') nextIndex = resultScripts.length - 1
    if (nextIndex === null || nextIndex === index) return
    event.preventDefault()
    selectEnterpriseResultScript(nextIndex, true)
  }

  // The review's Save for a converted draft; a validated draft delegates to it
  // once credential preparation has promoted the draft (runValidatedReviewAction).
  const convertedReviewSave = saveBlockedByProtocolGate && reviewArtifact
    ? handleSaveReviewArtifact
    : enterprisePrepareSelectedForBatch
      ? handlePrepareEnterpriseCredentialsForBatch
      : enterpriseCredentialBatchFlow
        ? handleSaveCredentialBatch
        : handleSaveConverted
  convertedReviewActionsRef.current = {
    save: () => convertedReviewSave(),
    edit: () => openImportedScriptInEditor(),
    convertValidated: itemIds => convertValidatedItems(itemIds),
  }
  // ScriptImportReview queues Save/Edit before it prepares credentials, so the
  // queued handler is the one from the click. Applying the bindings promotes a
  // validated draft to a converted draft in place (validation and conversion
  // return the same draft), and the click-time validation handler would then
  // re-run the whole conversion, discard the bindings it just applied, and
  // leave the operator to click Save a second time. Once that promotion has
  // happened, run the converted draft's action from the latest render instead.
  // "Save reviewed drafts" still covers the other validated archives: they
  // follow once the promoted draft is saved.
  const runValidatedReviewAction = (
    itemId: string,
    generation: number,
    action: 'save' | 'edit',
    validatedIds: readonly string[],
    fallback: () => void,
  ) => {
    const current = importItemsRef.current.find(item => item.id === itemId)
    if (current?.reviewMode !== 'conversion' || current.reviewGeneration !== generation) {
      fallback()
      return
    }
    if (action === 'edit') {
      convertedReviewActionsRef.current.edit()
      return
    }
    const scriptIndex = selectedResultScriptIndexRef.current
    const remainingIds = validatedIds.filter(id => id !== itemId)
    const sourceOperationGeneration = operationGenerationRef.current
    const sourceProjectId = reviewProjectIdRef.current
    void (async () => {
      await convertedReviewActionsRef.current.save()
      if (remainingIds.length === 0) return
      // Closing the import, switching project, or starting another operation
      // fences the batch: no further durable write may start after that.
      if (operationGenerationRef.current !== sourceOperationGeneration || reviewProjectIdRef.current !== sourceProjectId) return
      const promoted = importItemsRef.current.find(item => item.id === itemId)
      // A failed or non-saving action (such as preparing the next script's
      // credentials) keeps its message visible and leaves the rest for the
      // operator's next Save.
      if (!promoted || !enterpriseArchiveResultScriptIsSaved(promoted, scriptIndex)) return
      await convertedReviewActionsRef.current.convertValidated(remainingIds)
    })()
  }

  const reviewProjectPicker = importItems.length > 1 && <label className="lr-review-project-picker">
    <span>Review imported project</span>
    <select value={selectedItem?.id || ''} disabled={sourceMutationLocked} onChange={event => selectEnterpriseQueueItem(event.target.value)}>
      {importItems.map(item => <option key={item.id} value={item.id}>
        {item.file.name} — {item.status === 'validated' || item.status === 'converted'
          ? archiveReviewOutcome(item, importOptionsIdentity(options), projectId || '')
          : enterpriseArchiveImportStatusLabel(item.status)}
      </option>)}
    </select>
  </label>

  return (
    <div className={`lr-import ${result ? 'lr-import-reviewed' : ''}`}>
      <div className="lr-import-header">
        <div>
          <h2>{importSourceName('enterprise_archive')}</h2>
          <p className="lr-subtitle">Choose an uncompressed VuGen script folder or a LoadRunner ZIP or USZ archive, including source, runtime settings, parameter files, and dependencies. We detect the protocol and show what can be saved, what needs repair, and whether a runner is required. Import checks do not execute your script.</p>
        </div>
        {onClose && <button type="button" className="btn btn-secondary" onClick={() => {
          invalidateOperationScopes()
          onClose()
        }}>Back</button>}
      </div>

      <ol className="lr-import-steps" aria-label="Import workflow">
        <li aria-current={!result ? 'step' : undefined}>1. Upload archives</li>
        <li aria-current={result && workingMode !== 'convert' ? 'step' : undefined}>2. Review and resolve</li>
        <li aria-current={workingMode === 'convert' ? 'step' : undefined}>3. Save script or review draft</li>
        <li>4. Debug one iteration</li>
      </ol>
      {/* Drop zone */}
      <div ref={dropzoneRef} className={`lr-dropzone ${ingestHostClass} ${dragOver ? 'drag-over' : ''} ${queuedFileCount > 0 ? 'has-file' : ''}`}
        style={desktop ? ({ '--wails-drop-target': 'drop' } as CSSProperties) : undefined}
        onDragOver={e => { e.preventDefault(); if (!sourceMutationLocked) setDragOver(true) }}
        onDragLeave={() => setDragOver(false)}
        onDrop={handleDrop}
        onClick={sourceMutationLocked || queuedFileCount > 0 ? undefined : handleBrowseArchives}
        role={queuedFileCount > 0 ? "group" : "button"}
        tabIndex={sourceMutationLocked || queuedFileCount > 0 ? -1 : 0}
        aria-disabled={sourceMutationLocked}
        aria-label={queuedFileCount > 0 ? `Add enterprise script archives. ${queuedFileCount} currently queued` : 'Choose one or more enterprise script archives'}
        onKeyDown={event => {
          if (sourceMutationLocked || queuedFileCount > 0 || event.currentTarget !== event.target) return
          if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault()
            handleBrowseArchives()
          }
        }}>
        <DropIngestOverlay phase={ingestPhase} label={ingestLabel} />
        <input ref={fileRef} type="file" accept=".zip,.usz" multiple disabled={sourceMutationLocked} onClick={event => event.stopPropagation()} onChange={handleFileSelect} hidden />
        <input ref={folderRef} type="file" disabled={sourceMutationLocked} onClick={event => event.stopPropagation()} onChange={event => { addFolderToQueue(event.target.files); event.target.value = '' }} hidden />
        {queuedFileCount > 0 ? (
          <div className="lr-file-queue">
            <div className="lr-file-info">
              <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="var(--success)" strokeWidth="2" aria-hidden="true"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
              <div>
                <span className="lr-filename">{importItems.some(item => item.folderFiles) ? `${queuedFileCount} source${queuedFileCount === 1 ? '' : 's'}` : `${queuedFileCount} archive${queuedFileCount === 1 ? '' : 's'}`} queued</span>
                <span className="lr-filesize">Select a source to review its result.</span>
                <button type="button" className="btn btn-secondary btn-sm" disabled={sourceMutationLocked} onClick={event => { event.stopPropagation(); handleBrowseArchives() }}>Add archives</button>
              </div>
              <button className="btn btn-sm btn-ghost" disabled={sourceMutationLocked} onClick={e => { e.stopPropagation(); clearQueuedFiles() }}>Clear All</button>
            </div>
            {importItems.length > ENTERPRISE_QUEUE_PAGE_SIZE && (
              <nav className="lr-pagination" aria-label="Queued enterprise archive pages">
                <button
                  type="button"
                  className="btn btn-secondary btn-sm"
                  disabled={queuePageStart === 0}
                  onClick={event => {
                    event.stopPropagation()
                    selectEnterpriseQueueItem(importItems[Math.max(0, queuePageStart - ENTERPRISE_QUEUE_PAGE_SIZE)].id, true)
                  }}
                >
                  Previous archives
                </button>
                <span role="status" aria-live="polite">Showing {queuePageStart + 1}–{Math.min(queuePageStart + ENTERPRISE_QUEUE_PAGE_SIZE, importItems.length)} of {importItems.length}</span>
                <button
                  type="button"
                  className="btn btn-secondary btn-sm"
                  disabled={queuePageStart + ENTERPRISE_QUEUE_PAGE_SIZE >= importItems.length}
                  onClick={event => {
                    event.stopPropagation()
                    selectEnterpriseQueueItem(importItems[queuePageStart + ENTERPRISE_QUEUE_PAGE_SIZE].id, true)
                  }}
                >
                  Next archives
                </button>
              </nav>
            )}
            <div className="lr-file-list" aria-label="Queued enterprise script archives">
              {visibleImportItems.map(item => {
                const reviewOutcome = item.status === 'validated' || item.status === 'converted'
                  ? archiveReviewOutcome(item, importOptionsIdentity(options), projectId || '')
                  : ''
                return (
                <div
                  key={item.id}
                  className={`lr-file-row ${item.status} ${selectedItem?.id === item.id ? 'active' : ''}`}
                >
                  <button
                    ref={element => {
                      if (element) queueItemButtonRefs.current.set(item.id, element)
                      else queueItemButtonRefs.current.delete(item.id)
                    }}
                    type="button"
                    className="lr-file-row-select"
                    aria-pressed={selectedItem?.id === item.id}
                    onClick={event => {
                      event.stopPropagation()
                      selectEnterpriseQueueItem(item.id)
                    }}
                    onKeyDown={event => handleEnterpriseQueueItemKeyDown(event, item.id)}
                  >
                    <span className="lr-file-row-main">
                      <strong>{item.file.name}</strong>
                      <small className="lr-file-row-meta">
                        {item.folderFiles ? `${item.folderFiles.length} files` : item.sourceSize !== undefined ? `${(item.sourceSize / 1024).toFixed(1)} KB` : formatEnterpriseArchiveFileSize(item.file)} ·{' '}
                        {reviewOutcome === 'Blocker'
                          ? <ImportIssueSeverityTag severity="blocker" />
                          : <span className={`lr-file-status-pill ${item.status}`}>
                            {enterpriseArchiveImportStatusIsRunning(item.status) && <span className="lr-status-dot" aria-hidden="true" />}
                            {reviewOutcome || enterpriseArchiveImportStatusLabel(item.status)}
                          </span>}
                        {item.archiveFile ? ` · from ${item.archiveFile.name}` : ''}
                        {item.harFile ? ` · HAR: ${item.harFile.name}` : ''}
                        {(item.supplementalFiles || []).length > 0 ? ` · Required files: ${(item.supplementalFiles || []).length}` : ''}
                      </small>
                      {item.error && <small className="lr-file-row-message" role="alert">{item.error}</small>}
                    </span>
                  </button>
                  <button
                    type="button"
                    className="btn btn-sm btn-ghost lr-file-remove"
                    disabled={sourceMutationLocked}
                    aria-label={`Remove ${item.file.name}`}
                    onClick={event => {
                      event.stopPropagation()
                      removeQueuedFile(item.id)
                    }}
                  >
                    Remove
                  </button>
                </div>
              )})}
            </div>
          </div>
        ) : (
          <div className="lr-drop-content">
            <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" strokeWidth="1.5" aria-hidden="true"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/></svg>
            <p>Drop one or more enterprise script archive <code>.zip</code> or <code>.usz</code> files here</p>
            <span className="lr-drop-hint">or click to browse</span>
          </div>
        )}
      </div>

      <div className="lr-import-actions" style={actionWrapStyle}>
        <button type="button" className="btn btn-secondary" disabled={sourceMutationLocked} onClick={() => folderRef.current?.click()}>Choose VuGen folder</button>
      </div>

      {queuedFileCount > 0 && (
        <p role="status">{savedArchiveScripts.length} script{savedArchiveScripts.length === 1 ? '' : 's'} saved · {savedReviewSessionCount} review session{savedReviewSessionCount === 1 ? '' : 's'} saved · {pendingReviewScriptCount} draft{pendingReviewScriptCount === 1 ? '' : 's'} awaiting save · {validatedAwaitingSaveCount} archive{validatedAwaitingSaveCount === 1 ? '' : 's'} validated, awaiting save · {failedFileCount} archive{failedFileCount === 1 ? '' : 's'} failed</p>
      )}
      <ImportSaveConfirmation
        result={savedArchiveScripts.length ? { scripts: savedArchiveScripts } : null}
        onImportMore={handleBrowseArchives}
        onOpen={saved => onImported?.(saved)}
        disabled={sourceMutationLocked}
      />

      <details ref={archiveSetupRef} className="lr-completed-setup" open={!result}>
        <summary>{result ? 'Archive setup & save destination — edit or revalidate' : 'Archive setup & save destination'}</summary>
      {destinationControl}

      {/* Options */}
      <div className="lr-conversion-row">
        <div className="lr-options" id="migration-block-policy-options">
          <div className="lr-options-header">
            <h3 className="lr-section-title">Conversion Options</h3>
            {hasRuntimeCoverage && (
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setRuntimeCoverageOpen(true)}>
                Runtime Settings Coverage
              </button>
            )}
          </div>
          <div className="lr-options-grid">
            <label><span>Script Name (optional)</span>
              <input value={options.name} disabled={sourceMutationLocked || queuedFileCount > 1} onChange={e => replaceOptionsAndInvalidate({ ...options, name: e.target.value })} placeholder="Auto-detected from .usr file" />
            </label>
            <label><span>Base URL Override (optional)</span>
              <input value={options.base_url} disabled={sourceMutationLocked} onChange={e => replaceOptionsAndInvalidate({ ...options, base_url: e.target.value })} placeholder="Auto-detected from first URL" />
            </label>
            <label className="lr-checkbox">
              <input type="checkbox" checked={options.include_assertions} disabled={sourceMutationLocked} onChange={e => replaceOptionsAndInvalidate({ ...options, include_assertions: e.target.checked })} />
              <span>Add default status_code assertions</span>
            </label>
            <label className="lr-checkbox">
              <input type="checkbox" checked={options.preserve_transactions} disabled={sourceMutationLocked} onChange={e => replaceOptionsAndInvalidate({ ...options, preserve_transactions: e.target.checked })} />
              <span>Preserve transaction names in generated actions</span>
            </label>

            <details className="lr-advanced-policy"><summary>Advanced migration policy</summary><div className="lr-policy-heading">
              <strong>Migration Block Policy</strong>
              <span>These settings control which migration-readiness results prevent saving.</span>
            </div>
            <label className="lr-checkbox">
              <input type="checkbox" checked={options.block_partial} disabled={sourceMutationLocked} onChange={e => replaceOptionsAndInvalidate({ ...options, block_partial: e.target.checked })} />
              <span>Treat partial runtime settings as blockers</span>
            </label>
            <label className="lr-checkbox">
              <input type="checkbox" checked={options.block_compatibility_only} disabled={sourceMutationLocked} onChange={e => replaceOptionsAndInvalidate({ ...options, block_compatibility_only: e.target.checked })} />
              <span>Treat preserved-only runtime settings as blockers</span>
            </label>
            <label className="lr-checkbox">
              <input type="checkbox" checked={options.block_non_applicable} disabled={sourceMutationLocked} onChange={e => replaceOptionsAndInvalidate({ ...options, block_non_applicable: e.target.checked })} />
              <span>Treat non-applicable runtime settings as blockers</span>
            </label>
            <div className="lr-policy-heading">
              <strong>Draft Lifecycle</strong>
              <span>Drafts remain valid unless expiration is enabled.</span>
            </div>
            <label className="lr-checkbox">
              <input type="checkbox" checked={options.expire_draft} disabled={sourceMutationLocked} onChange={e => replaceOptionsAndInvalidate({ ...options, expire_draft: e.target.checked })} />
              <span>Expire converted drafts after 30 minutes</span>
            </label></details>
            <details className="lr-har-panel" open={Boolean(options.salesforce_native || selectedHARFile || salesforceReview)}><summary>Salesforce-specific options</summary>
              <label className="lr-checkbox">
                <input type="checkbox" checked={options.salesforce_native} disabled={sourceMutationLocked} onChange={e => replaceOptionsAndInvalidate({ ...options, salesforce_native: e.target.checked })} />
                <span>Build Salesforce native preview</span>
              </label>
              <div>
                <h4>Salesforce HAR (optional)</h4>
                <p>{selectedHARFile ? selectedHARFile.name : 'No HAR attached to the selected script'}</p>
              </div>
              <div className="lr-har-actions">
                <button type="button" className="btn btn-secondary btn-sm" onClick={() => harFileRef.current?.click()} disabled={!selectedItem || sourceMutationLocked}>
                  {selectedHARFile ? 'Replace HAR' : 'Attach HAR'}
                </button>
                {selectedHARFile && (
                  <button type="button" className="btn btn-ghost btn-sm" disabled={sourceMutationLocked} onClick={clearSelectedHARFile}>
                    Remove
                  </button>
                )}
              </div>
            </details>
          </div>
          {queuedFileCount > 1 && (
            <p className="lr-subtitle">Each archive keeps its detected script name in a batch. Rename individual scripts after import.</p>
          )}
          <p className="lr-subtitle">Selected migration block policy: {formatMigrationBlockPolicySummary(options)}</p>
          {reviewedMigrationPolicy && (
            <p className="lr-subtitle">Reviewed migration block policy: {formatMigrationBlockPolicySummary(reviewedMigrationPolicy)}</p>
          )}
          {hasPendingMigrationPolicyChanges && (
            <p className="lr-subtitle">Refresh review to apply the selected migration block policy.</p>
          )}
          {result && !reviewMatchesCurrentOptions && !hasPendingMigrationPolicyChanges && (
            <p className="lr-subtitle">Conversion options changed. Refresh the review before saving or editing this draft.</p>
          )}
          {hasCustomMigrationBlockPolicy(options) && (
            <div className="replay-panel-actions" style={actionWrapStyle}>
              <button className="btn btn-secondary btn-sm" disabled={sourceMutationLocked} onClick={restoreDefaultMigrationPolicy}>
                Restore Default Policy
              </button>
            </div>
          )}
        </div>

        <div className="lr-import-actions-card">
          <div>
            <h3 className="lr-section-title">Validation / Conversion</h3>
            <p className="lr-subtitle">{importItems.some(item => item.folderFiles) ? `${queuedFileCount} source${queuedFileCount === 1 ? '' : 's'}` : `${queuedFileCount} archive${queuedFileCount === 1 ? '' : 's'}`} queued</p>
          </div>
          <input ref={harFileRef} type="file" accept=".har,.json,application/json" disabled={sourceMutationLocked} onChange={handleHARFileSelect} hidden />
          <input ref={supplementalFileRef} type="file" accept=".java,.zip,.jar,.json,.txt" multiple disabled={sourceMutationLocked} onChange={handleSupplementalFileSelect} hidden />
          <div className="lr-required-files-panel">
            <div>
              <h4>Required Files</h4>
              <p>
                {requiredFileCandidates.length > 0
                  ? `${matchedRequiredFileKeys.size}/${requiredFileCandidates.length} detected dependency file${requiredFileCandidates.length === 1 ? '' : 's'} attached.`
                  : (result ? 'No unresolved required files detected in this review.' : 'Validate the archive to check for missing dependencies.')}
              </p>
              {requiredFileReviewIsStale && (
                <p role="status">Review is stale; re-validate to refresh unresolved required files.</p>
              )}
            </div>
            <div className="lr-required-files-actions">
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => supplementalFileRef.current?.click()} disabled={!selectedItem || sourceMutationLocked}>
                Attach Required File(s)
              </button>
              {selectedSupplementalFiles.length > 0 && (
                <button type="button" className="btn btn-ghost btn-sm" disabled={sourceMutationLocked} onClick={clearSelectedSupplementalFiles}>
                  Clear
                </button>
              )}
            </div>
          </div>
          {(requiredFileCandidates.length > 0 || selectedSupplementalFiles.length > 0) && (
            <div className="lr-warnings lr-required-files-review">
              {requiredFileCandidates.length > 0 && (
                <>
                  <h4>Required Files To Resolve Fidelity Blockers</h4>
                  <ul>
                    {requiredFileCandidates.map((candidate) => {
                      const attached = matchedRequiredFileKeys.has(candidate.className.toLowerCase())
                      return (
                        <li key={candidate.className.toLowerCase()}>
                          {candidate.className} ({candidate.suggestedPath}) - {attached ? 'attached' : 'missing'}
                        </li>
                      )
                    })}
                  </ul>
                </>
              )}
              {selectedSupplementalFiles.length > 0 && (
                <>
                  <h4>Attached Supplemental Files ({selectedSupplementalFiles.length})</h4>
                  <ul>
                    {selectedSupplementalFiles.map((file) => (
                      <li key={file.name}>
                        {file.name}{' '}
                        <button
                          type="button"
                          className="btn btn-ghost btn-sm"
                          disabled={sourceMutationLocked}
                          onClick={() => removeSelectedSupplementalFile(file.name)}
                        >
                          Remove
                        </button>
                      </li>
                    ))}
                  </ul>
                  <p className="lr-subtitle">Attached files are merged into the uploaded ZIP the next time you validate or convert this archive.</p>
                </>
              )}
            </div>
          )}
          <div className="lr-import-action-buttons">
            <button className="btn btn-secondary btn-lg" onClick={() => runReview('validate')} disabled={queuedFileCount === 0 || operationBusy}>
              {workingMode === 'validate' ? <><span className="spinner" /> Validating…</> : validateButtonLabel}
            </button>
            {(queuedFileCount > 1 || reviewMode !== 'conversion') && <button className="btn btn-primary btn-lg" onClick={() => convertValidatedItems()} disabled={validatedFileCount === 0 || !allValidatedReviewsMatchOptions || operationBusy}>
              {workingMode === 'convert' ? <><span className="spinner" /> {refreshingRemainingDrafts ? 'Converting remaining drafts…' : 'Converting and saving…'}</> : convertButtonLabel}
            </button>}
          </div>
          <p className="lr-subtitle">{conversionGateMessage}</p>
        </div>
      </div>

      </details>

      {importProgressMessage && (
        <div className="lr-import-progress" role="status" aria-live="polite">
          <span className="spinner" aria-hidden="true" />
          <span>{importProgressMessage}</span>
        </div>
      )}

      {runtimeCoverageOpen && hasRuntimeCoverage && (
        <DialogLayer><div
          className="lr-modal-backdrop"
          role="presentation"
          onPointerDown={event => {
            if (event.target === event.currentTarget) setRuntimeCoverageOpen(false)
          }}
        >
          <div
            ref={runtimeCoverageDialogRef}
            className="lr-modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="lr-runtime-coverage-title"
          >
            <div className="lr-modal-header">
              <div>
                <h3 id="lr-runtime-coverage-title">Runtime Settings Coverage</h3>
                <p>Code-owned runtime compatibility support matrix</p>
              </div>
              <button ref={runtimeCoverageCloseButtonRef} type="button" className="btn btn-ghost" onClick={() => setRuntimeCoverageOpen(false)}>Close</button>
            </div>
            <div className="lr-modal-body">
              <div className="lr-warnings">
                <h4>Coverage Details</h4>
                {supportMatrixSections.map(section => (
                  <details key={section.key}>
                    <summary>{section.title} ({section.items.length})</summary>
                    <ul>
                      {section.items.map(entry => (
                        <li key={entry.id}>{entry.area}: {entry.concept}</li>
                      ))}
                    </ul>
                  </details>
                ))}
              </div>

              {supportMatrixExamples.length > 0 && (
                <div className="lr-warnings">
                  <h4>Tested Examples</h4>
                  {supportMatrixExamples.map(example => (
                    <div key={example.test} className="lr-review-section">
                      <div className="lr-review-section-header">
                        <strong>{example.title}</strong>
                        <span>tested</span>
                      </div>
                      <p>{example.coverage}</p>
                      <ul><li>{example.test}</li></ul>
                    </div>
                  ))}
                </div>
              )}

              {supportMatrixNotes.length > 0 && (
                <div className="lr-warnings">
                  <h4>Known Caveats</h4>
                  <ul>{supportMatrixNotes.map((note, index) => <li key={index}>{note}</li>)}</ul>
                </div>
              )}
            </div>
          </div>
        </div></DialogLayer>
      )}

      {!result && <div className="settings-notice">
        <p>Protocol-aware detection uses .usr metadata plus source signatures. Conflicting evidence is gated for review instead of being saved as an executable script.</p>
      </div>}

      {protocolCoverageSections.length > 0 && workingMode === null && reviewMode === null && (
        <ImportCoverageButton
          title="Import Coverage"
          subtitle="Executable, review-only, external-runner, and unsupported protocols"
          triggerAriaLabel="enterprise protocol import coverage"
        >
          <div className="lr-preserved-grid">
            {protocolCoverageSections.map(section => (
              <div className={`lr-preserved-card coverage-${section.key}`} key={section.key}>
                <span className="lr-preserved-label">{section.title}</span>
                <strong>{section.items.length} {section.items.length === 1 ? 'protocol' : 'protocols'}</strong>
                <small>{section.preview}</small>
              </div>
            ))}
          </div>
          <div className="lr-warnings">
            <h4>Coverage Details</h4>
            {protocolCoverageSections.map(section => (
              <details key={section.key} data-coverage={section.key}>
                <summary>{section.title} ({section.items.length})</summary>
                <p className="coverage-hint">{section.hint}</p>
                <ul>
                  {section.items.map(item => {
                    const detail = enterpriseArchiveProtocolSupportDetail(item)
                    return (
                      <li key={item.id}>
                        <strong>{enterpriseArchiveProtocolName(item)}</strong>
                        {detail ? <> — {detail}</> : null}
                      </li>
                    )
                  })}
                </ul>
              </details>
            ))}
          </div>
          {javaImportSurfaceMatrix.length > 0 && (
            <div className="lr-warnings" aria-label="java vuser import coverage">
              <h4>Java Vuser Import Coverage</h4>
              <p className="coverage-hint">Recognized Java idioms are source-review evidence only. HTTP, JDBC, JMS, SOAP, and custom Java remain together in one source-order-preserving draft. Launch requires a governed JVM runner advertising java_vuser_persistent_lifecycle_v1 and java_vuser_session_frame_token_v1; each VUser keeps one JVM and Actions instance across init, action iterations, and end.</p>
              <ul>
                {javaImportSurfaceMatrix.map(entry => (
                  <li key={entry.id}>
                    <strong>{entry.area}</strong>: {entry.construct} — {formatJavaCoverageStatus(entry)}
                    {entry.notes ? <>. {entry.notes}</> : null}
                  </li>
                ))}
              </ul>
              {javaIdiomSections.map(section => (
                <details key={section.key} data-coverage={`java_${section.key}`}>
                  <summary>{section.title} ({section.items.length})</summary>
                  <ul>
                    {section.items.map(entry => (
                      <li key={entry.id}>
                        <strong>{entry.construct}</strong> — {formatJavaCoverageStatus(entry)}
                        {entry.fields?.length ? <> ({entry.fields.join(', ')})</> : null}
                        {entry.notes ? <>. {entry.notes}</> : null}
                      </li>
                    ))}
                  </ul>
                </details>
              ))}
            </div>
          )}
        </ImportCoverageButton>
      )}

      {failedImportMessage && <div className="lr-error" role="alert">{failedImportMessage}</div>}
      {displayError && <div className="lr-error" role="alert">{displayError}</div>}
      {displayNotice && <div className="settings-notice">{displayNotice}</div>}
      {!importedScript && importItems.some(item => ['validated', 'converted', 'failed'].includes(item.status)) && (
        <div className="lr-validation-review-navigation">{reviewProjectPicker}</div>
      )}

      <ArchiveReviewBoundary key={`${selectedItem?.id}:${projectId}`} resetKey={reviewScope} archiveName={selectedItem?.file.name || 'Selected archive'} retryDisabled={sourceMutationLocked || workingMode !== null} onRetry={() => { if (selectedItem) void runReview('validate', options, undefined, [selectedItem.id]) }}>
      {/* Results */}
      {result && !importedScript && reviewArtifact && (
        <div className="lr-result">
          <div className="lr-result-header">
            <div>
              <h3>{reviewMode === 'validation' ? 'Validation Review' : 'Import Review Artifact'}</h3>
              <p className="lr-subtitle">{dependencyBundle
                ? 'This archive contains Java dependencies without a script project. Attach this ZIP under Required Files in the corresponding Java project import, then validate that project. You can save this dependency review as evidence.'
                : 'This archive was classified without creating an executable APEX Load script.'}</p>
            </div>
            <button className="btn btn-primary" onClick={handleSaveReviewArtifact} disabled={reviewArtifactSaving || classpathUploadInProgress}>
              {reviewArtifactSaving ? <><span className="spinner" /> Saving review artifact…</> : dependencyBundle ? 'Save dependency review' : 'Save review artifact'}
            </button>
          </div>

          <div className="lr-warnings">
            <div className="lr-review-section">
              <div className="lr-review-section-header">
                <strong>{reviewArtifact.title}</strong>
                <span>{reviewArtifact.status}</span>
              </div>
              <p>{reviewArtifact.summary}</p>
              {reviewArtifact.required_evidence?.length ? (
                <ul>{reviewArtifact.required_evidence.map((evidence, index) => <li key={`artifact-evidence-${index}`}>{evidence}</li>)}</ul>
              ) : null}
            </div>
            {reviewSections.map(section => (
              <div key={section.key} className="lr-review-section">
                <div className="lr-review-section-header">
                  <strong>{section.title}</strong>
                  <span>{section.severity}</span>
                </div>
                {section.summary && <p>{section.summary}</p>}
                {section.items.length > 0 && <ul>{section.items.slice(0, 8).map((item, index) => <li key={`${section.key}-${index}`}>{item}</li>)}</ul>}
              </div>
            ))}
          </div>
          <JavaDependencyInventory metadata={reviewArtifact.metadata} />
          {sourceReview && (
            <SourceMappingReview sourceReview={sourceReview} script={null} />
          )}
        </div>
      )}

      {result && importedScript && (
        <div className="lr-result">
          {resultScripts.length > 1 && (
            <div className="lr-output-selector" aria-label="Generated scripts">
              <div className="lr-output-selector-header">
                <strong>Generated Scripts</strong>
                <span>{resultScripts.length} scripts</span>
              </div>
              {resultScripts.length > ENTERPRISE_RESULT_PAGE_SIZE && (
                <nav className="lr-pagination" aria-label="Generated enterprise script pages">
                  <button
                    type="button"
                    className="btn btn-secondary btn-sm"
                    disabled={resultScriptPageStart === 0}
                    onClick={() => selectEnterpriseResultScript(Math.max(0, resultScriptPageStart - ENTERPRISE_RESULT_PAGE_SIZE), true)}
                  >
                    Previous scripts
                  </button>
                  <span role="status" aria-live="polite">Showing {resultScriptPageStart + 1}–{Math.min(resultScriptPageStart + ENTERPRISE_RESULT_PAGE_SIZE, resultScripts.length)} of {resultScripts.length}</span>
                  <button
                    type="button"
                    className="btn btn-secondary btn-sm"
                    disabled={resultScriptPageStart + ENTERPRISE_RESULT_PAGE_SIZE >= resultScripts.length}
                    onClick={() => selectEnterpriseResultScript(resultScriptPageStart + ENTERPRISE_RESULT_PAGE_SIZE, true)}
                  >
                    Next scripts
                  </button>
                </nav>
              )}
              <div className="lr-output-list">
                {visibleResultScripts.map((script, visibleIndex) => {
                  const index = resultScriptPageStart + visibleIndex
                  return (
                  <button
                    key={`${script.name || 'script'}:${script.protocol || 'unknown'}:${index}`}
                    ref={element => {
                      if (element) resultScriptButtonRefs.current.set(index, element)
                      else resultScriptButtonRefs.current.delete(index)
                    }}
                    type="button"
                    className={`lr-output-row ${index === activeResultScriptIndex ? 'active' : ''}`}
                    aria-pressed={index === activeResultScriptIndex}
                    aria-label={`Review generated script ${script.name || `Script ${index + 1}`}`}
                    onClick={() => selectEnterpriseResultScript(index)}
                    onKeyDown={event => handleEnterpriseResultScriptKeyDown(event, index)}
                  >
                    <span className="lr-output-main">
                      <strong>{script.name || `Generated Script ${index + 1}`}</strong>
                      <small>
                        {script.protocol || 'unknown'} · {scriptActionCount(script)} actions
                        {selectedItem && enterpriseArchiveResultScriptIsSaved(selectedItem, index) ? ' · Saved' : ''}
                      </small>
                    </span>
                  </button>
                  )
                })}
              </div>
            </div>
          )}
          <div className="lr-tab-readiness" role="status">{importedScript && (() => { const issues = tabIssues; return `${issues.filter(i => i.blocking).length} grouped issues block replay · ${issues.filter(i => !i.blocking).length} need review${reviewedTabIssueCount ? ` · ${reviewedTabIssueCount} reviewed` : ''}`; })()}</div>
          <div className="lr-validation-review-navigation">
          {reviewProjectPicker}
          <TabList className="lr-validation-review-tabs"
 role="tablist" aria-label="Script archive review sections" onKeyDown={handleTabKey}>
              {validationReviewTabs.map((tab) => {
                // Unresolved grouped issues for this tab, named once in the tab's accessible name.
                const issueCount = importedScript && ['overview', 'credentials', 'data', 'preserved'].includes(tab.id)
                  ? tabIssues.filter(i => tab.id === 'overview' || (tab.id === 'credentials' && i.category === 'credentials') || (tab.id === 'data' && i.category === 'values') || (tab.id === 'preserved' && i.category === 'runtime')).length
                  : 0
                return (
                <button
                  key={tab.id}
                  id={`lr-validation-review-tab-${tab.id}`}
                  type="button"
                  role="tab"
                  aria-label={issueCount > 0 ? `${tab.label}, ${issueCount} unresolved grouped issue${issueCount === 1 ? '' : 's'}` : tab.label}
                  aria-selected={validationReviewTab === tab.id}
                  aria-controls="lr-validation-review-panel"
                  tabIndex={validationReviewTab === tab.id ? 0 : -1}
                  className={validationReviewTab === tab.id ? 'active' : ''}
                  onClick={() => selectValidationReviewTab(tab.id)}
                  onKeyDown={event => {
                    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
                    event.preventDefault()
                    const currentIndex = validationReviewTabs.findIndex(item => item.id === validationReviewTab)
                    const nextIndex = event.key === 'Home'
                      ? 0
                      : event.key === 'End'
                        ? validationReviewTabs.length - 1
                        : event.key === 'ArrowRight'
                          ? (currentIndex + 1) % validationReviewTabs.length
                          : (currentIndex - 1 + validationReviewTabs.length) % validationReviewTabs.length
                    const nextTab = validationReviewTabs[nextIndex]
                    selectValidationReviewTab(nextTab.id)
                    event.currentTarget.parentElement
                      ?.querySelector<HTMLButtonElement>(`#lr-validation-review-tab-${nextTab.id}`)
                      ?.focus()
                  }}
                >
                  {tab.label}{issueCount > 0 && <span className="lr-tab-count" aria-hidden="true">{issueCount}</span>}
                </button>
                )
              })}
          </TabList>
          </div>
          <div
            id="lr-validation-review-panel"
            role="tabpanel"
            aria-labelledby={`lr-validation-review-tab-${validationReviewTab}`}
            className="lr-validation-review-panel"
            style={validationReviewPanelMinHeight ? { minHeight: `${validationReviewPanelMinHeight}px` } : undefined}
          >
            <ScriptImportReview
            title="Review and resolve"
            compactReview
            showOriginalDiagnostics={false}
            reviewGeneration={reviewScope}
            subtitle={needsExecutionRepair && !saveBlockedByMigrationPolicy && !saveBlockedByProtocolGate && !browserImport ? 'Save a draft to continue later. Resolve execution blockers before debugging one iteration.' : reviewSubtitle}
            readinessMessage={readinessMessage}
            showCredentials={validationReviewTab === 'credentials'}
            showDataReview={validationReviewTab === 'data'}
            reviewNavigation={validationReviewTab !== 'overview' && <nav className="import-review-navigation" aria-label="Related replay issues">
              <button type="button" className="btn btn-secondary btn-sm" hidden={!returnToIssue} onClick={() => { selectReviewSection('overview'); setReturnToIssue(false) }}>Back to this issue</button>
              <p>{sectionIssueCount} unresolved grouped replay {sectionIssueCount === 1 ? 'issue' : 'issues'}{reviewCategory !== 'all' ? ` in ${reviewCategory === 'values' ? 'missing values & correlations' : reviewCategory === 'runtime' ? 'transport & runtime' : 'credentials'}` : ''}.</p>
              <button type="button" className="btn btn-secondary btn-sm" onClick={openReviewIssues}>{reviewCategory === 'values' ? 'Review missing values & correlations' : reviewCategory === 'runtime' ? 'Review runtime issues' : reviewCategory === 'credentials' ? 'Review credential issues' : 'Review all issues'}</button>
            </nav>}
            credentialTarget={credentialTarget}
            credentialIntroduction={<div className="import-credentials-introduction">
              <ImportReviewSectionHeader title="Credential setup" description={secretBindingRequirements.length ? "Manage project bindings and preserved values. The inventory below counts unique groups; replay issues are tracked separately above." : "No credential bindings were reported for this draft. Runtime credentials are verified when debugging starts."} />
              {!currentRepairReview && (replayPreflight?.missing_secrets?.length || 0) > 0 && <>
                <ul>{replayPreflight?.missing_secrets?.map(name => <li key={name}><code>{name}</code></li>)}</ul>
                <p>Configure these exact names in the project secret provider, then validate again to refresh readiness.</p>
                <a className="btn btn-secondary btn-sm" href="#/settings" target="_blank" rel="noopener noreferrer" onClick={() => prepareProjectSecretsSettingsNavigation(projectId)}>Open Project Secrets in a new tab</a>
              </>}
            </div>}
            script={importedScript}
            warnings={result.warnings}
            unsupportedElements={unsupportedFunctions}
            warningsTitle={`Conversion Warnings (${result.warnings.length})`}
            parameterization={result.parameterization}
            onParameterizationApplied={handleParameterizationApplied}
            onSettingsBusyChange={setSettingsEditing}
            onSettingsApplied={updated => {
              handleParameterizationApplied(updated.script, [], [], updated.import_draft_expires_at, updated.secret_binding_requirements || [])
              setProtectedReview(null)
              if (selectedItem) updateImportItem(selectedItem.id, { reviewMode: 'conversion', status: 'converted', notice: 'Settings applied to the reviewed draft.' })
            }}
            settingsDisabled={selectedResultScriptSaved || !reviewMatchesCurrentOptions}
            runtimeContent={<div className="lr-validation-review-preserved" hidden={validationReviewTab !== 'preserved'}><ImportRuntimeSettingsTable script={importedScript} onReviewTLSMigration={tlsRepairIssue ? () => {
              const path = tlsRepairIssue.findings.find(finding => finding.code === 'legacy_tls_replacement')?.action_path
              if (path) setIssueFocus(previous => ({ path, revision: previous.revision + 1 }))
              else setCategoryFocus(previous => ({ category: 'runtime', revision: previous.revision + 1 }))
              selectReviewSection('overview')
            } : undefined} /></div>}
            secretBindingRequirements={secretBindingRequirements}
            sourceMigrationRequirements={archiveSecretBindingRequirements}
            preserveSourceSessionAfterApply={archiveSecretBindingRequirements.length > secretBindingRequirements.length}
            onSecretBindingsApply={handleSecretBindingsApply}
            onSecretBindingsErrorChange={setError}
            sharedValues={result.shared_values}
            sharedValueScripts={resultScripts}
            onSharedValuesApplied={handleSharedValuesApplied}
            projectId={projectId}
            sourceSecretSessionId={result.source_secret_session_id}
            sourceSecretSessionExpiresAt={result.source_secret_session_expires_at}
            sourceSecretMigrationAllowed={result.source_secret_migration_allowed}
            importDraftExpiresAt={result.import_draft_expires_at}
            onSourceSecretSessionSettled={handleSourceSecretSessionSettled}
            credentialReviewRef={credentialReviewRef}
            saving={saveBlockedByProtocolGate ? reviewArtifactSaving : workingMode === 'convert' || scriptSaveInProgress}
            reviewBusy={classpathUploadInProgress || guidedRepairBusy || !reviewMatchesCurrentOptions || editorActionPreparing || scriptSaveInProgress || selectedResultScriptSaved}
            onReviewBusyChange={setInternalReviewBusy}
            saveDisabled={reviewSaveDisabled}
            saveDisabledReason={reviewSaveDisabledReason}
            saveLabel={saveActionLabel}
            savedAction={savedReviewScript && onImported ? { label: 'Open saved script in Visual Editor', onClick: () => onImported({ scripts: [savedReviewScript] }) } : undefined}
            onSave={!(saveBlockedByProtocolGate && reviewArtifact) && reviewMode === 'validation' && selectedItem
              ? () => runValidatedReviewAction(selectedItem.id, selectedItem.reviewGeneration, 'save', validatedItemIds, () => { void convertValidatedItems(validatedItemIds) })
              : convertedReviewSave}
            editDisabledReason={editorBlocked ? editorBlockedReason : undefined}
            onEdit={importedEditorAvailable && !selectedResultScriptSaved && (!editRequiresCredentialPreservingSave || (queuedFileCount === 1 && resultScripts.length === 1))
              ? (reviewMode === 'validation' && selectedItem
                  ? () => runValidatedReviewAction(selectedItem.id, selectedItem.reviewGeneration, 'edit', validatedItemIds, () => openImportedScriptInEditor())
                  : () => { openImportedScriptInEditor() })
              : undefined}
            editLabel={editRequiresCredentialPreservingSave ? 'Save draft and open in Visual Editor' : 'Open draft in Visual Editor'}
            previewTitle="DSL Preview"
            previewData={importedScript}
            previewInitiallyOpen
            previewAside={sourceReview
              ? <SourceMappingReview sourceReview={sourceReview} script={importedScript} repairPaths={allTabIssues.flatMap(issue => issue.findings.flatMap(f => f.action_path ? [f.action_path] : []))} onRepairTarget={path => { setIssueFocus(previous => ({ path, revision: previous.revision + 1 })); setValidationReviewTab('overview') }} focusedSourceFile={repairSourceFocus.file} focusedMappingId={repairSourceFocus.id} focusRequestId={repairSourceFocus.revision} />
              : <div className="lr-review-tab-empty">No source mapping was generated for this archive.</div>}
            showPreview={validationReviewTab === 'dsl'}
            showPreviewAside={validationReviewTab === 'source'}
          >
            {importedScript && <ImportGuidedRepair onCredentialTarget={setCredentialTarget} onReimport={() => { if (archiveSetupRef.current) { archiveSetupRef.current.open = true; archiveSetupRef.current.querySelector('summary')?.focus(); archiveSetupRef.current.scrollIntoView({ block: 'start' }) } }} categoryFocus={categoryFocus} onBusyChange={setGuidedRepairBusy} focusPath={issueFocus.path} focusRequest={issueFocus.revision} key={guidedRepairScope} onReviewedChange={ids => setReviewedRepairIssues({ scope: guidedRepairScope, ids })} view={validationReviewTab === 'overview' ? 'overview' : validationReviewTab === 'data' ? 'data' : 'hidden'} additionalFindings={additionalRepairFindings} onSection={tab => { setReturnToIssue(true); selectReviewSection(tab) }} onReview={review => setProtectedReview({ project: projectId || '', result: review })} inspectionPaused={guidedRepairInspectionPaused} script={importedScript} projectId={projectId || ''} disabled={internalReviewBusy || scriptSaveInProgress || selectedResultScriptSaved || !reviewMatchesCurrentOptions} disabledReason={selectedResultScriptSaved ? 'This draft has already been saved. Re-import the archive to preview allocation for a new draft.' : !reviewMatchesCurrentOptions ? 'Import options have changed. Validate the archive again before previewing allocation.' : 'Finish pending edits or wait for the current import operation to complete before previewing allocation.'} onViewSource={(path, finding) => {
              const mapping = sourceReview?.mappings?.find(entry => finding?.source_mapping_id ? entry.id === finding.source_mapping_id : path && entry.target_path === path)
              setReturnToIssue(true)
              setRepairSourceFocus(previous => ({ id: mapping?.id || '', file: finding?.source_file || '', revision: previous.revision + 1 }))
              setValidationReviewTab('source')
            }} onApplied={repaired => {
              if (!selectedItem || !result) return
              setProtectedReview({ project: projectId || '', result: repaired })
              const nextScripts = resultScripts.map((entry, index) => index === selectedResultScriptIndex ? repaired.script : entry)
              nextRequestToken(replayRequestTokensRef.current, selectedItem.id)
              updateImportItem(selectedItem.id, { result: { ...result, script: nextScripts[0], scripts: nextScripts, import_draft_expires_at: repaired.expires_at, fidelity_report: repaired.fidelity_report, secret_binding_requirements: replaceSecretBindingRequirementsForScript(result.secret_binding_requirements, selectedResultScriptIndex, importedScript, repaired.secret_binding_requirements) }, replayValidationLoading: false, replayValidationError: '', replayValidationWarnings: [], replayValidationSession: null, replayReviewGeneration: -1, replayScriptIndex: -1, replayProjectId: '', notice: 'Repair applied. Requirements were refreshed from the protected draft.' })
            }} />}
            <div
              className="lr-validation-review-overview"
              hidden={validationReviewTab !== 'overview'}
            >
            {saveBlockedByProtocolGate && <div className="lr-warnings" role="status"><strong>Execution blocked</strong><p>{combinedJavaVuserGate ? 'Save the combined draft for approval. A compatible governed Java runner is also required before execution.' : 'Save a reference artifact. This protocol is not available for executable promotion in this import.'}</p></div>}
            {saveBlockedByProtocolGate && detectedProtocol && (
              <div className="lr-warnings lr-protocol-gate">
                <div className="lr-review-section">
                  <div className="lr-review-section-header">
                    <strong>{externalRunnerGate ? 'External Runner Required' : 'Protocol Promotion Gate'}</strong>
                    <span>{detectedProtocol.import_mode}</span>
                  </div>
                  <p>
                    {externalRunnerGate
                      ? (combinedJavaVuserGate
                        ? (combinedJavaVuserApprovalReady
                          ? `${protocolGateName} remains one combined init/action/end source-order draft. It can be approved into the script library, but execution is blocked until the governed runner advertises java_vuser_persistent_lifecycle_v1 and java_vuser_session_frame_token_v1. Each VUser keeps one JVM and Actions instance across init, repeated action iterations, and end.`
                          : `${protocolGateName} remains one combined Java Vuser review artifact, but fidelity blockers must be resolved before authoring approval. The source is preserved without splitting it into native protocol lanes.`)
                        : `${protocolGateName} draft conversion is visible for review, but execution is blocked until the required runner is approved and configured.`)
                      : `${protocolGateName} draft conversion is visible for review, but executable promotion is blocked by the protocol classification.`}
                  </p>
                  {combinedJavaVuserGate && (
                    <p>
                      Save this combined draft as an authoring session, then resume that session to edit and approve it. Direct visual-editor and script-library saves are disabled so the governed approval evidence stays attached to the draft.
                    </p>
                  )}
                  {protocolGateDetails.length > 0 && (
                    <ul>
                      {protocolGateDetails.map((detail, index) => <li key={`protocol-gate-${index}`}>{detail}</li>)}
                    </ul>
                  )}
                </div>
              </div>
            )}

            <JavaDependencyInventory metadata={reviewArtifact?.metadata} />

            {showClasspathResolver && (
              <div className="lr-warnings lr-java-classpath-resolver-section">
                <div className="lr-review-section">
                  <div className="lr-review-section-header">
                    <strong>Resolve Java Dependencies</strong>
                    <span>{currentAttachedCount}/{currentClasspathEntries.length} attached</span>
                  </div>
                  {!projectId && (
                    <p className="cell-error">Select a project before attaching classpath JARs.</p>
                  )}
                  <JavaClasspathResolver
                    key={reviewScope}
                    classpath={currentClasspathEntries}
                    assets={currentClasspathAssets}
                    onChange={setCurrentClasspathAssets}
                    onUpload={handleUploadClasspathJar}
                    disabled={!projectId || sourceMutationLocked || workingMode !== null || internalReviewBusy || guidedRepairBusy}
                  />
                </div>
              </div>
            )}

            {trueClient2CompatReport && (
              <div className="lr-warnings lr-trueclient2-compat">
                <div className="lr-review-section">
                  <div className="lr-review-section-header">
                    <strong>TruClient 2 Compatibility</strong>
                    <span>{formatRuntimeSettingStatusLabel(trueClient2CompatReport.promotion_status || 'review')}</span>
                  </div>
                  <p>
                    {trueClient2CompatReport.source_script_type || 'TruClient source'} · {trueClient2CompatReport.import_mode || 'review'} · {trueClient2CompatReport.promotion_allowed ? 'Promotion allowed' : 'Promotion blocked'}
                  </p>
                  <ImportEvidenceFacts facts={[
                    { label: 'Steps', value: `${trueClient2CompatReport.converted_actions ?? 0}/${trueClient2CompatReport.imported_steps ?? 0}`, detail: `${trueClient2CompatReport.skipped_review_steps ?? 0} skipped/review` },
                    { label: 'Selectors', value: `${trueClient2CompatReport.selector_confidence_pct ?? 0}%`, detail: `${trueClient2CompatReport.selector_candidate_count ?? 0} candidates` },
                    { label: 'Runtime', value: <>{formatRuntimeSettingStatusLabel(trueClient2CompatReport.runtime_parity_status || 'unknown')}
                      <details>
                        <summary>Compatibility evidence</summary>
                        {typeof trueClient2CompatReport.fixture_contract_status === 'string'
                          ? <p>{formatRuntimeSettingStatusLabel(trueClient2CompatReport.fixture_contract_status)}</p>
                          : <ul>{Object.entries(trueClient2CompatReport.fixture_contract_status || {}).sort(([a], [b]) => a.localeCompare(b)).map(([name, status]) => (
                            <li key={name}>{name.replace(/_/g, ' ')}: {formatRuntimeSettingStatusLabel(status)}</li>
                          ))}</ul>}
                        {!trueClient2CompatReport.fixture_contract_status && <p>Compatibility evidence not available.</p>}
                      </details>
                    </> },
                    { label: 'Replay', value: formatRuntimeSettingStatusLabel(trueClient2CompatReport.replay_status || 'required'), detail: `${trueClient2CompatReport.frame_window_blockers ?? 0} frame/window blockers` },
                    { label: 'JavaScript review gate', value: formatRuntimeSettingStatusLabel(trueClient2CompatReport.javascript_review_gate || 'none') },
                    { label: 'HTTP conversion', value: formatRuntimeSettingStatusLabel(trueClient2CompatReport.http_conversion_readiness || 'not ready'), detail: 'Scale path' },
                  ]} />
                  {trueClient2CompatReport.supported_step_families?.length ? (
                    <p>Supported steps: {trueClient2CompatReport.supported_step_families.join(', ')}</p>
                  ) : null}
                  {trueClient2CompatReport.review_only_step_families?.length ? (
                    <p>Review-only steps: {trueClient2CompatReport.review_only_step_families.join(', ')}</p>
                  ) : null}
                  {trueClient2CompatReport.unsupported_step_families?.length ? (
                    <p>Unsupported steps: {trueClient2CompatReport.unsupported_step_families.join(', ')}</p>
                  ) : null}
                  {trueClient2CompatReport.findings?.length ? (
                    <ul>{trueClient2CompatReport.findings.slice(0, 6).map((finding, index) => <li key={`tc2-finding-${index}`}>{finding}</li>)}</ul>
                  ) : null}
                  {trueClient2CompatReport.next_actions?.length ? (
                    <>
                      <p>Next actions</p>
                      <ul>{trueClient2CompatReport.next_actions.slice(0, 6).map((action, index) => <li key={`tc2-next-${index}`}>{action}</li>)}</ul>
                    </>
                  ) : null}
                  {importedEditorAvailable && renderEditorActionButtons([
                    {
                      key: 'trueclient2-open-draft',
                      label: 'Open Imported Draft In Editor',
                      onClick: () => openImportedScriptInEditor(),
                    },
                    {
                      key: 'trueclient2-runtime-review',
                      label: 'Open Runtime Review In Editor',
                      onClick: () => openImportedScriptInEditor({ targetSection: 'runtime' }),
                    },
                  ], sectionActionStyle)}
                </div>
              </div>
            )}

            {browserImport && (
              <div className="lr-warnings">
                <div className="lr-review-section">
                  <div className="lr-review-section-header">
                    <strong>Draft Browser Replay Validation</strong>
                    <span>{browserReplayStatusLabel}</span>
                  </div>
                  <p>{replayPanelDescription}</p>
                  <div className="replay-panel-actions" style={{ marginTop: 12, justifyContent: 'flex-start' }}>
                    <button className="btn btn-secondary" onClick={handleValidateBrowserReplay} disabled={replayValidationLoading || browserReplayInProgress || !reviewMatchesCurrentOptions}>
                      {replayValidationLoading ? <><span className="spinner" /> {replayActionLabel}</> : replayActionLabel}
                    </button>
                  </div>
                  {replayValidationSession && !replayValidationLoading && (
                    <>
                      <ul>
                        <li>Replay Session: {replayValidationSession.id}</li>
                        <li>Replay Status: {browserReplayStatusLabel}</li>
                        <li>Replay Group: {replayValidationSession.group}</li>
                      </ul>
                      <div className="lr-replay-evidence">
                        <div className="lr-review-section-header">
                          <strong>Replay Evidence</strong>
                          <span>{browserReplayEvidence.events > 0 ? 'Captured' : 'Pending'}</span>
                        </div>
                        <ImportEvidenceFacts facts={[
                          { label: 'Actions', value: formatEvidenceCount(browserReplayEvidence.events, 'action'), detail: `${browserReplayEvidence.passed} passed / ${browserReplayEvidence.failed} failed` },
                          { label: 'Captures', value: formatEvidenceCount(browserReplayEvidence.screenshots, 'screenshot'), detail: formatEvidenceCount(browserReplayEvidence.domHtmlCaptures, 'DOM/HTML capture') },
                          { label: 'Telemetry', value: formatEvidenceCount(browserReplayEvidence.consoleEntries, 'console entry', 'console entries'), detail: formatEvidenceCount(browserReplayEvidence.networkEntries, 'network entry', 'network entries') },
                          { label: 'Source links', value: formatEvidenceCount(browserReplayEvidence.sourceSteps, 'source step'), detail: formatEvidenceCount(browserReplayEvidence.sourceTransactions, 'transaction') },
                        ]} />
                      </div>
                    </>
                  )}
                  {replayValidationWarnings.length > 0 && (
                    <ul>{replayValidationWarnings.map((warning, index) => <li key={index}>{warning}</li>)}</ul>
                  )}
                  {replayValidationError && <div className="lr-error" role="alert">{replayValidationError}</div>}
                </div>
              </div>
            )}

            <SalesforceCorrelationReview preview={salesforceReview?.native_preview} sourceReview={sourceReview || undefined} onViewSource={mapping => { setRepairSourceFocus(previous => ({ id: mapping.id, file: mapping.source_file, revision: previous.revision + 1 })); setValidationReviewTab('source') }} />
            <AuraOutcomeReview report={salesforceReview?.aura_outcomes} sourceReview={sourceReview || undefined} onViewSource={mapping => { setRepairSourceFocus(previous => ({ id: mapping.id, file: mapping.source_file, revision: previous.revision + 1 })); setValidationReviewTab('source') }} />

            {salesforceReview && (
              <div className="lr-salesforce-lanes">
                <div className="lr-salesforce-lane">
                  <div className="lr-salesforce-lane-header">
                    <div>
                      <strong>Exact Replay</strong>
                      <span>Primary Output</span>
                    </div>
                    <small>{scriptActionCount(importedScript)} actions</small>
                  </div>
                  <dl>
                    <div>
                      <dt>Domains</dt>
                      <dd>{salesforceDomainSummary(salesforceReview)}</dd>
                    </div>
                    <div>
                      <dt>HAR Coverage</dt>
                      <dd>{formatSalesforceCoverage(salesforceReview)}</dd>
                    </div>
                  </dl>
                  {importedEditorAvailable && renderEditorActionButtons([
                    {
                      key: 'salesforce-exact-open',
                      label: 'Open Exact Replay',
                      onClick: () => openImportedScriptInEditor(),
                    },
                  ], sectionActionStyle)}
                </div>

                <div className={`lr-salesforce-lane ${salesforceNativeScript ? '' : 'blocked'}`}>
                  <div className="lr-salesforce-lane-header">
                    <div>
                      <strong>Salesforce Native Preview</strong>
                      <span>{salesforceNativeScript ? 'Review Only' : 'Needs Evidence'}</span>
                    </div>
                    <small>{salesforceTransactionCount(salesforceReview)} transactions</small>
                  </div>
                  <dl>
                    <div>
                      <dt>Correlation</dt>
                      <dd>{salesforceReview.correlation_confidence || 'unknown'}</dd>
                    </div>
                    <div>
                      <dt>Correlations</dt>
                      <dd>{salesforceReview.native_preview?.correlations?.length || 0}</dd>
                    </div>
                    <div>
                      <dt>API surface</dt>
                      <dd>{salesforceApiSurfaceSummary(salesforceReview)}</dd>
                    </div>
                    <div>
                      <dt>Aura params</dt>
                      <dd>{salesforceReview.parameterizations?.length || 0}</dd>
                    </div>
                  </dl>
                  {salesforceReview.blockers?.length ? (
                    <ul className="lr-salesforce-blockers">
                      {salesforceReview.blockers.slice(0, 4).map((blocker, index) => <li key={index}>{blocker}</li>)}
                    </ul>
                  ) : null}
                  {salesforceReview.parameterizations?.length ? (
                    <ul className="lr-salesforce-blockers">
                      {salesforceReview.parameterizations.slice(0, 4).map((param, index) => (
                        <li key={index}>{param.param_key} ({param.kind}) → {param.template}
                          {param.decoded_field_path && <AuraParameterLocationDetails location={param} />}
                        </li>
                      ))}
                    </ul>
                  ) : null}
                  {importedEditorAvailable && salesforceNativeScript && renderEditorActionButtons([
                    {
                      key: 'salesforce-native-open',
                      label: 'Review Native Preview Limitation',
                      opensEditor: false,
                      onClick: openSalesforceNativePreviewInEditor,
                    },
                  ], sectionActionStyle)}
                </div>
              </div>
            )}

            <ImportConversionDetails
              description="Original archive conversion counts and reports. These do not update after draft repairs or verify replay; use the issue queue above for current requirements."
              facts={[
                { label: 'Logical actions', value: result.stats.logical_actions ?? result.stats.converted_actions },
                { label: importedScript.protocol === 'java_vuser' ? 'Lifecycle actions' : 'Raw requests', value: result.stats.raw_request_actions ?? result.stats.converted_actions },
                { label: 'Collapsed requests', value: result.stats.collapsed_requests ?? 0 },
                { label: 'Extractors', value: result.stats.extractors },
                { label: 'Transactions', value: result.stats.transactions },
                { label: 'Transaction markers', value: result.stats.transaction_markers ?? 0 },
                { label: 'Source functions', value: result.stats.total_functions },
              ]}
            >
              {(replayPreflight?.checks?.filter(check => check.status === 'pass').length || 0) > 0 && <details className="import-evidence-section"><summary>Passed static checks ({replayPreflight?.checks?.filter(check => check.status === 'pass').length})</summary><ul>{replayPreflight?.checks?.filter(check => check.status === 'pass').map(check => <li key={check.id}><strong>{check.label}</strong>: {check.detail}</li>)}</ul></details>}
            {unsupportedFunctions.length > 0 && (
              <details className="import-evidence-section">
                <summary>Unsupported source functions ({unsupportedFunctions.length})</summary>
                <ul>
                  {unsupportedFunctions.map((fnName, index) => {
                    const repairSuggestion = findUnsupportedRepairSuggestion(repairSuggestions, fnName)
                    return (
                      <li key={`${fnName}-${index}`}>
                        {fnName}
                        {repairSuggestion?.target_action && importedEditorAvailable && (
                          <>
                            {' '}
                            <button className="btn btn-secondary btn-sm" disabled={editorBlocked} title={editorBlocked ? editorBlockedReason : undefined} onClick={() => openImportedScriptInEditor({ targetAction: repairSuggestion.target_action })}>
                              Open Fix In Editor
                            </button>
                          </>
                        )}
                      </li>
                    )
                  })}
                </ul>
                {importedEditorAvailable && renderEditorActionButtons([
                  firstUnsupportedTargetAction ? {
                    key: 'unsupported-summary-action',
                    label: 'Open First Unsupported Action In Editor',
                    onClick: () => openImportedScriptInEditor({ targetAction: firstUnsupportedTargetAction }),
                  } : null,
                  {
                    key: 'unsupported-summary-script',
                    label: 'Open Imported Script In Editor',
                    onClick: () => openImportedScriptInEditor(),
                  },
                ].filter((button): button is EditorActionButton => Boolean(button)), sectionActionStyle)}
              </details>
            )}

            {(reviewSections.length > 0 || repairSuggestions.length > 0) && (
              <div className="import-evidence-reports">
                {reviewSections.length > 0 && (
                  <details className="import-evidence-section">
                    <summary>Converter assessments ({reviewSections.length})</summary>
                    {reviewSections.map(section => (
                      <div key={section.key} className="lr-review-section">
                        <div className="lr-review-section-header">
                          <strong>{section.title}</strong>
                          <span>{section.severity}</span>
                        </div>
                        {section.summary && <p>{section.summary}</p>}
                        {renderEditorActionButtons(getReviewSectionActions(section.key), sectionActionStyle)}
                        {section.items.length > 0 && (
                          <ul>{section.items.slice(0, 6).map((item, index) => <li key={`${section.key}-${index}`}>{item}</li>)}</ul>
                        )}
                      </div>
                    ))}
                  </details>
                )}

                {repairSuggestions.length > 0 && (
                  <details className="import-evidence-section">
                    <summary>Original repair suggestions ({repairSuggestions.length})</summary>
                    <p>Suggestions recorded during conversion. The issue queue reflects the current draft.</p>
                    {repairSuggestions.map(suggestion => {
                      const suggestionTitle = suggestion.category === 'replay'
                        ? normalizeReplaySuggestionCopy(suggestion.title)
                        : suggestion.title
                      const suggestionDescription = suggestion.category === 'replay'
                        ? normalizeReplaySuggestionCopy(suggestion.description)
                        : suggestion.description
                      const suggestionAction = suggestion.category === 'replay'
                        ? normalizeReplaySuggestionCopy(suggestion.action)
                        : suggestion.action

                      return (
                      <div key={suggestion.id} className="lr-review-section">
                        <div className="lr-review-section-header">
                          <strong>{suggestionTitle}</strong>
                          <span>{suggestion.severity}</span>
                        </div>
                        {suggestion.target_action && <p>Action: {suggestion.target_action}</p>}
                        {typeof suggestion.selector_stability_score === 'number' && (
                          <p>Selector stability: {suggestion.selector_stability_score}/100{suggestion.selector_signals?.length ? ` - ${suggestion.selector_signals.join(', ')}` : ''}</p>
                        )}
                        {suggestionDescription && <p>{suggestionDescription}</p>}
                        {suggestionAction && <ul><li>{suggestionAction}</li></ul>}
                        {suggestion.target_action && importedEditorAvailable && (
                          renderEditorActionButtons([
                            {
                              key: `${suggestion.id}:target-action`,
                              label: 'Open Target Action In Editor',
                              onClick: () => openImportedScriptInEditor({ targetAction: suggestion.target_action }),
                            },
                          ])
                        )}
                        {!suggestion.target_action && suggestion.category === 'runtime' && (
                          renderEditorActionButtons([
                            importedEditorAvailable ? {
                              key: `${suggestion.id}:runtime-review`,
                              label: 'Open Runtime Review In Editor',
                              onClick: () => openImportedScriptInEditor({ targetSection: 'runtime' }),
                            } : null,
                            importedEditorAvailable && firstRuntimeBlockingArea && suggestion.id === 'runtime:migration-readiness' ? {
                              key: `${suggestion.id}:blocking-area`,
                              label: 'Open First Blocking Setting In Editor',
                              onClick: () => openImportedRuntimeSettingsInEditor(firstRuntimeBlockingArea),
                            } : null,
                            ...(
                              suggestion.id === 'runtime:migration-readiness'
                                ? buildMigrationPolicyRelaxationButtons(`${suggestion.id}:relax`)
                                : []
                            ),
                          ].filter((button): button is EditorActionButton => Boolean(button)))
                        )}
                        {!suggestion.target_action && suggestion.category === 'replay' && importedEditorAvailable && (
                          renderEditorActionButtons([
                            {
                              key: `${suggestion.id}:open-script`,
                              label: 'Open Imported Draft In Editor',
                              onClick: () => openImportedScriptInEditor(),
                            },
                          ])
                        )}
                        {!suggestion.target_action && suggestion.category === 'salesforce' && importedEditorAvailable && (
                          renderEditorActionButtons([
                            salesforceNativeScript ? {
                              key: `${suggestion.id}:native-preview`,
                              label: 'Review Native Preview Limitation',
                              opensEditor: false,
                              onClick: openSalesforceNativePreviewInEditor,
                            } : null,
                            {
                              key: `${suggestion.id}:exact-replay`,
                              label: 'Open Exact Replay',
                              onClick: () => openImportedScriptInEditor(),
                            },
                          ].filter((button): button is EditorActionButton => Boolean(button)))
                        )}
                      </div>
                    )})}
                  </details>
                )}
              </div>
            )}

            </ImportConversionDetails>
            </div>

            </ScriptImportReview>
          </div>
        </div>
      )}
      </ArchiveReviewBoundary>
    </div>
  )
}

function summarizeEnterpriseArchiveSupportMatrix(entries: EnterpriseArchiveSupportMatrixEntry[]): EnterpriseArchiveSupportMatrixSection[] {
  return [
    buildEnterpriseArchiveSupportMatrixSection('executed', 'Executed', entries),
    buildEnterpriseArchiveSupportMatrixSection('partial', 'Partial', entries),
    buildEnterpriseArchiveSupportMatrixSection('compatibility_only', 'Compatibility-only', entries),
    buildEnterpriseArchiveSupportMatrixSection('non_applicable', 'Non-applicable', entries),
    buildEnterpriseArchiveSupportMatrixSection('blocked', 'Blocked', entries),
  ]
}

function summarizeEnterpriseArchiveProtocolSupport(entries: EnterpriseArchiveProtocolSupportEntry[]): EnterpriseArchiveProtocolCoverageSection[] {
  if (entries.length === 0) return []
  return [
    buildEnterpriseArchiveProtocolCoverageSection('executable', 'Executable', entries),
    buildEnterpriseArchiveProtocolCoverageSection('review_only', 'Review-only', entries),
    buildEnterpriseArchiveProtocolCoverageSection('external_runner_required', 'External Runner', entries),
    buildEnterpriseArchiveProtocolCoverageSection('unsupported', 'Unsupported', entries),
  ].filter(section => section.items.length > 0)
}

function buildMigrationBlockStatuses(options: Pick<ImportOptions, 'block_partial' | 'block_compatibility_only' | 'block_non_applicable'>) {
  const statuses = ['blocked']
  if (options.block_partial) statuses.push('partial')
  if (options.block_compatibility_only) statuses.push('compatibility_only')
  if (options.block_non_applicable) statuses.push('non_applicable')
  return statuses
}

function normalizeRelaxableMigrationBlockStatus(status?: string | null): RelaxableMigrationBlockStatus | null {
  switch (status?.trim().toLowerCase()) {
    case 'partial':
      return 'partial'
    case 'compatibility_only':
      return 'compatibility_only'
    case 'non_applicable':
      return 'non_applicable'
    default:
      return null
  }
}

function getRelaxableMigrationBlockStatuses(
  readiness: EnterpriseArchiveMigrationReadiness | null,
  options: Pick<ImportOptions, 'block_partial' | 'block_compatibility_only' | 'block_non_applicable'>,
): RelaxableMigrationBlockStatus[] {
  if (!readiness?.blocked || !Array.isArray(readiness.blocking_statuses)) return []

  const statuses = readiness.blocking_statuses
    .map((status) => normalizeRelaxableMigrationBlockStatus(status))
    .filter((status): status is RelaxableMigrationBlockStatus => Boolean(status))
    .filter((status) => {
      switch (status) {
        case 'partial':
          return options.block_partial
        case 'compatibility_only':
          return options.block_compatibility_only
        case 'non_applicable':
          return options.block_non_applicable
      }
    })

  return Array.from(new Set(statuses))
}

function getRelaxedMigrationBlockOptions(options: ImportOptions, status: RelaxableMigrationBlockStatus): ImportOptions | null {
  switch (status) {
    case 'partial':
      return options.block_partial ? { ...options, block_partial: false } : null
    case 'compatibility_only':
      return options.block_compatibility_only ? { ...options, block_compatibility_only: false } : null
    case 'non_applicable':
      return options.block_non_applicable ? { ...options, block_non_applicable: false } : null
  }
}

function buildEnterpriseArchiveSupportMatrixSection(status: string, title: string, entries: EnterpriseArchiveSupportMatrixEntry[]): EnterpriseArchiveSupportMatrixSection {
  const items = entries.filter(entry => entry.status === status)
  return {
    key: status,
    title,
    items,
    preview: items.slice(0, 2).map(entry => `${entry.area}: ${entry.concept}`).join(' • '),
  }
}

function buildEnterpriseArchiveProtocolCoverageSection(mode: string, title: string, entries: EnterpriseArchiveProtocolSupportEntry[]): EnterpriseArchiveProtocolCoverageSection {
  const items = entries.filter(entry => protocolImportMode(entry) === mode)
  return {
    key: mode,
    title,
    items,
    preview: items.map(enterpriseArchiveProtocolName).join(' • '),
    hint: enterpriseArchiveProtocolCoverageHint(mode),
  }
}

function enterpriseArchiveProtocolName(entry: EnterpriseArchiveProtocolSupportEntry): string {
  return entry.display_name || entry.protocol || entry.id
}

function enterpriseArchiveProtocolSupportDetail(entry: EnterpriseArchiveProtocolSupportEntry): string {
  return [
    entry.notes,
    entry.runner_requirement ? `Requires ${entry.runner_requirement}.` : '',
    entry.recommended_workflow,
  ].filter(Boolean).join(' ')
}

function formatJavaCoverageStatus(entry: EnterpriseArchiveJavaSupportEntry): string {
  return [
    formatRuntimeSettingStatusLabel(entry.status),
    entry.execution_status ? formatRuntimeSettingStatusLabel(entry.execution_status) : '',
    entry.review_required ? 'Review Required' : '',
  ].filter(Boolean).join(' / ')
}

function enterpriseArchiveProtocolCoverageHint(mode: string): string {
  switch (mode) {
    case 'executable':
      return 'Protocols that can produce runnable APEX Load scripts from source evidence.'
    case 'review_only':
      return 'Protocols that preserve source evidence for review before a runnable script can be promoted.'
    case 'external_runner_required':
      return 'Protocols that require an approved external runner or bridge before execution.'
    case 'unsupported':
      return 'Protocols that are classified for visibility but do not currently have an import pathway.'
    default:
      return 'Protocols detected in the enterprise script archive support matrix.'
  }
}

function protocolImportMode(entry: EnterpriseArchiveProtocolSupportEntry): string {
  const status = entry.status || entry.import_mode || ''
  switch (status) {
    case 'supported':
    case 'review_then_executable':
      return 'executable'
    case 'review_only':
      return 'review_only'
    case 'external_runner':
    case 'external_runner_required':
      return 'external_runner_required'
    case 'unsupported':
    case 'out_of_scope':
      return 'unsupported'
    default:
      return entry.runner_requirement ? 'external_runner_required' : 'review_only'
  }
}

function getImportedParameterMappingValue(values: Record<string, string>, aliases: string[]): string {
  const normalizedAliases = aliases.map(alias => alias.toLowerCase())
  const match = Object.entries(values).find(([key]) => normalizedAliases.includes(key.toLowerCase()))
  return match?.[1]?.trim() || ''
}

function sanitizeImportedParameterDataSourceName(dataFile?: string | null): string {
  const normalizedFile = dataFile?.trim()
  if (!normalizedFile) return 'parameter_data'

  const baseName = normalizedFile.split(/[\\/]/).pop() || normalizedFile
  const withoutExtension = baseName.replace(/\.[^.]+$/, '')
  const normalized = withoutExtension
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '')

  return normalized || 'parameter_data'
}

function findImportedParameterMappingDataSourceName(
  dataSources: Array<{ name: string }> | undefined,
  values: Record<string, string>,
): string | null {
  const dataFile = getImportedParameterMappingValue(values, ['table', 'filename', 'file', 'datafile', 'sourcefile'])
  if (!dataFile || !dataSources?.length) return null

  const sanitizedName = sanitizeImportedParameterDataSourceName(dataFile)
  const exactMatch = dataSources.find(source => source.name.trim() === sanitizedName)
  if (exactMatch) return exactMatch.name

  const lowerMatch = dataSources.find(source => source.name.trim().toLowerCase() === sanitizedName)
  return lowerMatch?.name || null
}
