import ImportWorkflowGuide from './ImportWorkflowGuide'
import ImportSaveConfirmation from './ImportSaveConfirmation'
import { useImportSaveOutcome, type ImportSaveDestination } from './useImportSaveOutcome'
import { useCallback, useEffect, useReducer, useRef, useState, type CSSProperties } from 'react'
import { useAsyncTask } from '../hooks/useAsyncTask'
import { useApi } from '../hooks/useApi'
import {
  describeMergedFiles,
  formatFileLabel,
  mergePrimarySamples,
  mergeSecondarySamples,
  sampleAcceptForFormat,
  supportsMultiPrimary,
  supportsMultiSecondary,
  type LoadedSample,
  type SampleFormat,
} from './sampleImportUtils'
import { type SampleImportResult, type SecretBindingSelection, type SharedValues } from '../types/scriptImports'
import { type StoredScript } from '../types'
import { type ReplayPreflight, type ScriptDataSource, type ScriptDefinition, type SourceReview } from '../types/scripts'
import { getErrorMessage } from '../utils/errorUtils'
import { isDesktopSurface } from '../desktop/desktopBridge'
import { canUseNativeDialogs, filesFromDroppedPaths, nativeFileReadErrorMessage, pickFilesViaBridge, shouldUseBrowserFileFallback } from '../desktop/desktopFiles'
import { useDesktopFileDrop } from '../desktop/useDesktopFileDrop'
import DropIngestOverlay from './DropIngestOverlay'
import { describeAccepted, describeReading, useDropIngestFeedback } from './dropIngestFeedback'
import { createJavaHttpDraft } from './scriptDraftFactories'
import { attachImportCorrelationSuggestions, createReplaySessionId } from './scriptEditorUtils'
import ScriptImportReview from './ScriptImportReview'
import { type SecretBindingReviewHandle } from './SecretBindingReview'
import SourceMappingReview from './SourceMappingReview'
import ReplayPreflightCard from './ReplayPreflightCard'
import { buildLocalReplayPreflight } from './replayPreflightUtils'
import { withFallbackPersistedScript } from './importSaveUtils'
import { useImportReviewGeneration } from './importReviewGeneration'
import { isValidSecretBindingName, refreshedSecretBindingRequirements, secretBindingReference } from './secretBindingUtils'
import { credentialPreservingImportDraftRequiresSave, earliestImportDraftExpiry } from './protectedImportSession'
import { type ImportedSaveResult } from './useScriptsAuthoringState'
import SecondsFromMillisecondsInput from './SecondsFromMillisecondsInput'
import { useSourceSecretSessionLifecycle } from './sourceSecretSessionLifecycle'
import './SampleScriptImport.css'

type AuthType = 'none' | 'basic' | 'bearer'
type PacingType = 'none' | 'fixed_delay'
type SampleImportPreset = 'default' | 'java-http'

type SampleImportOptions = {
  name: string
  base_url: string
  include_assertions: boolean
  infer_extractors: boolean
  sanitize_sensitive: boolean
  include_static_assets: boolean
  auth_type: AuthType
  auth_username: string
  auth_password: string
  auth_token: string
  think_time_ms: string
  pacing_type: PacingType
  pacing_delay_ms: string
  continue_on_error: boolean
  max_retries: string
  request_timeout_ms: string
  user_agent: string
  default_headers: string
}

type SampleImportOptionsAction =
  | { type: 'update'; field: keyof SampleImportOptions; value: SampleImportOptions[keyof SampleImportOptions] }
  | { type: 'reset' }

type AssetUploadState = Record<string, { pending?: boolean; error?: string; fileName?: string }>

interface Props {
  initialFiles?: File[] | null
  projectId?: string
  // Destination folder for saved scripts; see ProtocolAssetImport.
  folderId?: string | null
  onImported?: (result: ImportedSaveResult) => void
  onEdit?: (script: ScriptDefinition) => void
  onClose?: () => void
  preset?: SampleImportPreset
  onOperationBusyChange?: (busy: boolean) => void
}

const formatHelp: Record<SampleFormat, string> = {
  raw_http: 'Paste a raw HTTP request and an optional sample response. Include a Host header or provide a base URL override.',
  curl: 'Paste a curl command. The importer derives method, URL, headers, and body automatically.',
  rest_json: 'Paste structured JSON with request/response objects or a requests array.',
  har: 'Paste a HAR file or a single HAR entry JSON object.',
}

const sensitiveDefaultHeaderNames = new Set([
  'authorization',
  'proxy-authorization',
  'x-api-key',
  'api-key',
  'x-auth-token',
  'x-access-token',
  'x-csrf-token',
  'x-xsrf-token',
  'x-session-id',
  'x-amz-security-token',
  'cookie',
  'set-cookie',
])
const portableSecretHeaderValuePattern = /^(?:(?:Bearer|Basic|Digest)\s+)?\{\{secret\.[A-Za-z0-9][A-Za-z0-9._-]{0,248}\}\}$/i

function unsafeDefaultHeader(headers: Record<string, string>): string {
  for (const [name, value] of Object.entries(headers)) {
    const normalizedName = name.trim().toLowerCase()
    const normalizedField = normalizedName.replace(/[^a-z0-9]/g, '')
    const sensitive = sensitiveDefaultHeaderNames.has(normalizedName) ||
      ['password', 'passwd', 'passphrase', 'secret', 'token', 'apikey', 'credential', 'cookie', 'sessionid', 'privatekey', 'accesskey']
        .some(suffix => normalizedField.endsWith(suffix))
    if (sensitive && !portableSecretHeaderValuePattern.test(value.trim())) {
      return name
    }
  }
  return ''
}

const sampleTemplates: Record<SampleFormat, { primary: string; secondary?: string }> = {
  raw_http: {
    primary: 'POST /v1/login HTTP/1.1\nHost: api.example.com\nContent-Type: application/json\n\n{"username":"demo","password":"secret"}',
    secondary: 'HTTP/1.1 200 OK\nContent-Type: application/json\n\n{"token":"session-token-123","userId":91234}',
  },
  curl: {
    primary: `curl -X POST https://api.example.com/v1/orders \\
  -H 'Content-Type: application/json' \\
  -H 'Authorization: Bearer my-token' \\
  -d '{"item":"widget","qty":5}'`,
  },
  rest_json: {
    primary: `{
  "requests": [
    {
      "request": {
        "method": "POST",
        "url": "https://api.example.com/v1/session",
        "headers": {"Content-Type": "application/json"},
        "body": {"username": "demo", "password": "secret"}
      },
      "response": {
        "status": 201,
        "body": {"sessionId": "session-12345"}
      }
    }
  ]
}`,
  },
  har: {
    primary: `{
  "log": {
    "entries": [
      {
        "request": {
          "method": "GET",
          "url": "https://api.example.com/v1/orders",
          "headers": [{"name": "Accept", "value": "application/json"}]
        },
        "response": {
          "status": 200,
          "content": {"text": "{"orders":[]}"}
        }
      }
    ]
  }
}`,
  },
}

const javaHttpTemplates: Partial<Record<SampleFormat, { primary: string; secondary?: string }>> = {
  raw_http: {
    primary: 'POST /java/session HTTP/1.1\nHost: api.example.com\nContent-Type: application/x-java-serialized-object\nAccept: application/x-java-serialized-object\n\nrO0ABXQABGRlbW8=',
    secondary: 'HTTP/1.1 200 OK\nContent-Type: application/x-java-serialized-object\n\nrO0ABXQACnNlc3Npb24=',
  },
  curl: {
    primary: `curl -X POST https://api.example.com/java/session \\
  -H 'Content-Type: application/x-java-serialized-object' \\
  -H 'Accept: application/x-java-serialized-object' \\
  --data 'rO0ABXQABGRlbW8='`,
  },
  har: {
    primary: `{
  "log": {
    "entries": [
      {
        "request": {
          "method": "POST",
          "url": "https://api.example.com/java/session",
          "headers": [
            {"name": "Content-Type", "value": "application/x-java-serialized-object"},
            {"name": "Accept", "value": "application/x-java-serialized-object"}
          ],
          "postData": {"text": "rO0ABXQABGRlbW8="}
        },
        "response": {
          "status": 200,
          "content": {"text": "rO0ABXQACnNlc3Npb24="}
        }
      }
    ]
  }
}`,
  },
}

const defaultFormatByPreset: Record<SampleImportPreset, SampleFormat> = {
  default: 'rest_json',
  'java-http': 'raw_http',
}

const availableFormatsByPreset: Record<SampleImportPreset, SampleFormat[]> = {
  default: ['rest_json', 'raw_http', 'curl', 'har'],
  'java-http': ['raw_http', 'curl', 'har'],
}

const javaHttpDefaults = createJavaHttpDraft().config.java_http || {}

function getSampleTemplate(preset: SampleImportPreset, format: SampleFormat) {
  if (preset === 'java-http' && javaHttpTemplates[format]) {
    return javaHttpTemplates[format]!
  }
  return sampleTemplates[format]
}

function getFormatHelp(preset: SampleImportPreset, format: SampleFormat): string {
  if (preset === 'java-http') {
    if (format === 'raw_http') {
      return 'Paste a raw HTTP request whose body is already a base64-encoded serialized Java payload. Optional sample responses should also preserve the opaque base64 body.'
    }
    if (format === 'curl') {
      return 'Paste a curl command whose --data body is already a base64-encoded serialized Java payload.'
    }
    if (format === 'har') {
      return 'Paste a HAR captured from a Java-RPC-over-HTTP client. Request/response bodies should already be base64-encoded serialized Java payloads.'
    }
  }
  return formatHelp[format]
}

function looksLikeBase64(value: string): boolean {
  const trimmed = value.trim()
  if (!trimmed) return true
  return trimmed.length % 4 === 0 && /^[A-Za-z0-9+/=\r\n]+$/.test(trimmed)
}

// A Java serialized object stream begins with the magic bytes 0xAC 0xED 0x00 0x05,
// which always base64-encode to a leading "rO0AB" prefix regardless of the payload
// that follows. Checking the prefix avoids decoding while still validating the codec.
function looksLikeJavaSerialized(value: string): boolean {
  return value.replace(/[\r\n]/g, '').trim().startsWith('rO0AB')
}

function promoteScriptToJavaHTTP(script: ScriptDefinition): ScriptDefinition {
  return {
    ...script,
    protocol: 'java_http',
    config: {
      ...script.config,
      java_http: {
        ...javaHttpDefaults,
        ...(script.config?.java_http || {}),
      },
    },
  }
}

function appendJavaHTTPWarnings(script: ScriptDefinition, warnings: string[]): string[] {
  const nextWarnings = [...warnings]
  const nonEmptyBodies = script.actions
    .map(action => (typeof action.body === 'string' ? action.body : ''))
    .filter(body => {
      const trimmed = body.trim()
      return trimmed !== '' && !/^\{\{file\.[^}]+\}\}$/.test(trimmed)
    })
  const hasNonBase64Body = nonEmptyBodies.some(body => !looksLikeBase64(body))
  // A body can be valid base64 yet not be a Java serialized object — flag that
  // separately so the user can verify the codec/source rather than silently
  // shipping a payload the java_http protocol can't meaningfully replay.
  const hasNonJavaSerializedBody = nonEmptyBodies.some(body => looksLikeBase64(body) && !looksLikeJavaSerialized(body))

  const warning = hasNonBase64Body
    ? 'One or more imported request bodies are not base64-encoded. Java-over-HTTP replay expects opaque base64-encoded serialized bytes, so review those actions before saving.'
    : 'Java-over-HTTP imports assume request and response payloads are already base64-encoded serialized Java bytes. Review the generated draft before saving.'
  if (!nextWarnings.includes(warning)) {
    nextWarnings.push(warning)
  }
  if (hasNonJavaSerializedBody) {
    const magicWarning = 'One or more base64 bodies do not decode to a Java serialized object stream (expected magic bytes 0xAC 0xED 0x00 0x05). Verify the codec or source before saving.'
    if (!nextWarnings.includes(magicWarning)) {
      nextWarnings.push(magicWarning)
    }
  }
  return nextWarnings
}

function sourceReviewForSample(result?: SampleImportResult | null, script?: ScriptDefinition | null): SourceReview | null {
  const metadataReview = script?.metadata?.import?.preserved?.source_review as SourceReview | undefined
  const review = result?.source_review || metadataReview
  if (!review || (!Array.isArray(review.source_files) && !Array.isArray(review.mappings))) return null
  return review
}

function fileAssetSources(script?: ScriptDefinition | null): ScriptDataSource[] {
  return (script?.data_sources || []).filter(source => source.type === 'file')
}

function isFileAssetReady(source: ScriptDataSource): boolean {
  return Boolean(source.storage_ref?.trim() || source.path?.trim() || source.inline_data?.trim())
}

// assetUploadKey is keyed primarily on the data source's index so that multiple
// nameless file data sources do not collide onto one key (which would make their
// upload status and merge target route to the wrong asset).
function assetUploadKey(source: ScriptDataSource, index: number): string {
  return `${index}::${source.name || source.file_name || 'file'}`
}

function importAssetID(script: ScriptDefinition): string {
  return script.id || script.ref_key || script.name || 'sample-import'
}

// mergeUploadedFileAsset merges the uploaded asset into the data source at
// targetIndex (the asset's stable position in data_sources), rather than matching
// by name — a nameless source would otherwise match the first empty-named entry
// and mis-target the merge.
function mergeUploadedFileAsset(script: ScriptDefinition, targetIndex: number, uploaded: ScriptDataSource): ScriptDefinition {
  const nextSources = [...(script.data_sources || [])]
  const inRange = targetIndex >= 0 && targetIndex < nextSources.length
  const existing = inRange ? nextSources[targetIndex] : undefined
  const merged: ScriptDataSource = {
    ...(existing || {}),
    ...uploaded,
    name: existing?.name || uploaded.name,
    type: 'file',
    strategy: uploaded.strategy || existing?.strategy || 'sequential',
  }
  if (inRange) {
    nextSources[targetIndex] = merged
  } else {
    nextSources.push(merged)
  }
  return {
    ...script,
    data_sources: nextSources,
  }
}

function attachReplayPreflight(script: ScriptDefinition, preflight: ReplayPreflight): ScriptDefinition {
  return {
    ...script,
    metadata: {
      ...(script.metadata || {}),
      import: {
        ...(script.metadata?.import || {}),
        preserved: {
          ...(script.metadata?.import?.preserved || {}),
          replay_preflight: preflight,
        },
      },
    },
  }
}

function replaceImportResultScript(result: SampleImportResult, script: ScriptDefinition, preflight: ReplayPreflight): SampleImportResult {
  const scripts = result.scripts?.length
    ? result.scripts.map((entry, index) => (index === 0 || entry.name === script.name ? script : entry))
    : [script]
  return {
    ...result,
    script,
    scripts,
    preflight,
  }
}

const initialOptions: SampleImportOptions = {
  name: '',
  base_url: '',
  include_assertions: true,
  infer_extractors: true,
  sanitize_sensitive: true,
  include_static_assets: false,
  auth_type: 'none',
  auth_username: '',
  auth_password: '',
  auth_token: '',
  think_time_ms: '1000',
  pacing_type: 'none',
  pacing_delay_ms: '0',
  continue_on_error: false,
  max_retries: '0',
  request_timeout_ms: '60000',
  user_agent: '',
  default_headers: '',
}

function sampleImportOptionsReducer(state: SampleImportOptions, action: SampleImportOptionsAction): SampleImportOptions {
  switch (action.type) {
    case 'update':
      return {
        ...state,
        [action.field]: action.value,
      }
    case 'reset':
      return initialOptions
    default:
      return state
  }
}

function inferSampleFormatFromFile(file: File | undefined, fallback: SampleFormat): SampleFormat {
  const name = file?.name.toLowerCase() || ''
  if (name.endsWith('.curl') || name.endsWith('.sh')) return 'curl'
  if (name.endsWith('.har')) return 'har'
  if (name.endsWith('.http') || name.endsWith('.req') || name.endsWith('.request')) return 'raw_http'
  return fallback
}

export default function SampleScriptImport({ initialFiles, projectId, folderId, onImported, onEdit, onClose, preset: initialPreset = 'default', onOperationBusyChange }: Props) {
  const api = useApi()
  const sourceSecretSessions = useSourceSecretSessionLifecycle(api.discardImportSecretSession)
  const primaryFileRef = useRef<HTMLInputElement>(null)
  const secondaryFileRef = useRef<HTMLInputElement>(null)
  const primaryDropRef = useRef<HTMLLabelElement>(null)
  const secondaryDropRef = useRef<HTMLLabelElement>(null)
  const [preset, setPreset] = useState<SampleImportPreset>(initialPreset)
  const defaultFormat = defaultFormatByPreset[initialPreset]
  const defaultTemplate = getSampleTemplate(initialPreset, defaultFormat)
  const [format, setFormat] = useState<SampleFormat>(defaultFormat)
  const [primaryContent, setPrimaryContent] = useState(defaultTemplate.primary)
  const [secondaryContent, setSecondaryContent] = useState(defaultTemplate.secondary || '')
  const [primaryFileName, setPrimaryFileName] = useState('')
  const [secondaryFileName, setSecondaryFileName] = useState('')
  const [primaryFileNames, setPrimaryFileNames] = useState<string[]>([])
  const [secondaryFileNames, setSecondaryFileNames] = useState<string[]>([])
  const [primaryMergedCount, setPrimaryMergedCount] = useState(0)
  const [secondaryMergedCount, setSecondaryMergedCount] = useState(0)
  const [dragTarget, setDragTarget] = useState<'primary' | 'secondary' | null>(null)
  // One drop animation per sample field. Destructured callbacks stay stable so
  // the native drop handlers keep their identity for useDesktopFileDrop.
  const primaryIngest = useDropIngestFeedback()
  const secondaryIngest = useDropIngestFeedback()
  const { begin: beginPrimaryIngest, succeed: succeedPrimaryIngest, fail: failPrimaryIngest } = primaryIngest
  const { begin: beginSecondaryIngest, succeed: succeedSecondaryIngest, fail: failSecondaryIngest } = secondaryIngest
  const [messageError, setMessageError] = useState('')
  const convertTask = useAsyncTask<SampleImportResult>()
  const saveTask = useAsyncTask<StoredScript>()
  const [options, dispatchOptions] = useReducer(sampleImportOptionsReducer, initialOptions)
  const [assetUploads, setAssetUploads] = useState<AssetUploadState>({})
  const credentialReviewRef = useRef<SecretBindingReviewHandle | null>(null)
  const [internalReviewBusy, setInternalReviewBusy] = useState(false)
  const desktop = isDesktopSurface()
  const [browserPickerFallback, setBrowserPickerFallback] = useState(false)
  const nativeDialogs = desktop && canUseNativeDialogs() && !browserPickerFallback

  const result = convertTask.value
  const latestResultRef = useRef<SampleImportResult | null>(result)
  latestResultRef.current = result
  const genericBatchSaveOperationRef = useRef<{
    result: SampleImportResult
    projectId?: string
    folderId: string | null
    operationId: string
  } | null>(null)
  const {
    generation: reviewGeneration,
    generationRef: reviewGenerationRef,
    advance: advanceReviewGeneration,
  } = useImportReviewGeneration()
  const reviewScope = `${reviewGeneration}:${projectId || ''}`
  const { savedResult, publish: publishSaved, openSaved } = useImportSaveOutcome(reviewScope, onImported)
  const reviewProjectIdRef = useRef(projectId)
  reviewProjectIdRef.current = projectId
  const resetConvertTask = convertTask.reset
  const resetSaveTask = saveTask.reset
  const invalidateSampleSource = useCallback(() => {
    sourceSecretSessions.clear('conversion')
    const generation = advanceReviewGeneration()
    resetConvertTask()
    resetSaveTask()
    setMessageError('')
    setAssetUploads({})
    return generation
  }, [advanceReviewGeneration, resetConvertTask, resetSaveTask, sourceSecretSessions])
  useEffect(() => () => {
    // Route transitions can unmount this importer without invoking Back.
    // Fence any persistence response so it refreshes the library without
    // navigating over whichever authoring surface replaced this review.
    reviewGenerationRef.current += 1
  }, [reviewGenerationRef])
  const updateOption = <K extends keyof SampleImportOptions>(field: K, value: SampleImportOptions[K]) => {
    invalidateSampleSource()
    dispatchOptions({ type: 'update', field, value })
  }
  const previousProjectIdRef = useRef(projectId)
  useEffect(() => {
    if (previousProjectIdRef.current === projectId) return
    previousProjectIdRef.current = projectId
    invalidateSampleSource()
  }, [invalidateSampleSource, projectId])
  const error = messageError || convertTask.error || saveTask.error
  const converting = convertTask.pending
  const saving = saveTask.pending
  const operationBusy = converting || saving || internalReviewBusy
  useEffect(() => {
    onOperationBusyChange?.(operationBusy)
    return () => onOperationBusyChange?.(false)
  }, [onOperationBusyChange, operationBusy])
  const availableFormats = availableFormatsByPreset[preset]
  const isJavaHttpPreset = preset === 'java-http'
  const reviewedScript = result?.script || result?.scripts?.[0] || null
  const sourceReview = sourceReviewForSample(result, reviewedScript)
  const importFileAssets = fileAssetSources(reviewedScript)
  const hasMissingFileAssets = importFileAssets.some(source => !isFileAssetReady(source))
  const replayPreflight = reviewedScript ? (result?.preflight || buildLocalReplayPreflight(reviewedScript)) : null
  const editRequiresCredentialPreservingSave = credentialPreservingImportDraftRequiresSave(
    result,
    reviewedScript,
    result?.secret_binding_requirements,
  )

  const handleFormatChange = (nextFormat: SampleFormat) => {
    invalidateSampleSource()
    const template = getSampleTemplate(preset, nextFormat)
    setFormat(nextFormat)
    setPrimaryContent(template.primary)
    setSecondaryContent(template.secondary || '')
    setPrimaryFileName('')
    setSecondaryFileName('')
    setPrimaryFileNames([])
    setSecondaryFileNames([])
    setPrimaryMergedCount(0)
    setSecondaryMergedCount(0)
  }

  const handlePresetChange = (nextPreset: SampleImportPreset) => {
    invalidateSampleSource()
    const nextFormat = defaultFormatByPreset[nextPreset]
    const template = getSampleTemplate(nextPreset, nextFormat)
    setPreset(nextPreset)
    setFormat(nextFormat)
    setPrimaryContent(template.primary)
    setSecondaryContent(template.secondary || '')
    setPrimaryFileName('')
    setSecondaryFileName('')
    setPrimaryFileNames([])
    setSecondaryFileNames([])
    setPrimaryMergedCount(0)
    setSecondaryMergedCount(0)
  }

  // Resolves with what happened, so drop handlers can settle the drop animation.
  // 'superseded' means a newer load took over and now owns the animation.
  const handleFilesLoad = useCallback(async (files: File[], target: 'primary' | 'secondary', formatOverride?: SampleFormat): Promise<'loaded' | 'superseded' | 'error'> => {
    const sourceGeneration = invalidateSampleSource()
    const sourceProjectId = reviewProjectIdRef.current
    const requestIsCurrent = () => reviewGenerationRef.current === sourceGeneration && reviewProjectIdRef.current === sourceProjectId
    const effectiveFormat = formatOverride || format
    try {
      const loaded = await Promise.all(files.map(async (file) => ({ file, text: await file.text() })))
      if (!requestIsCurrent()) return 'superseded'
      const normalized: LoadedSample[] = loaded.map(item => ({ name: item.file.name, text: item.text }))
      if (target === 'primary') {
        const merged = mergePrimarySamples(effectiveFormat, normalized)
        const names = normalized.map(item => item.name)
        setPrimaryContent(merged.content)
        setPrimaryFileName(formatFileLabel(names))
        setPrimaryFileNames(names)
        setPrimaryMergedCount(merged.mergedCount)
      } else {
        const merged = mergeSecondarySamples(effectiveFormat, normalized)
        const names = normalized.map(item => item.name)
        setSecondaryContent(merged.content)
        setSecondaryFileName(formatFileLabel(names))
        setSecondaryFileNames(names)
        setSecondaryMergedCount(merged.mergedCount)
      }
      setMessageError('')
      return 'loaded'
    } catch (error: unknown) {
      if (!requestIsCurrent()) return 'superseded'
      setMessageError(getErrorMessage(error, `Failed to read ${files[0]?.name || 'sample file'}`))
      return 'error'
    }
  }, [format, invalidateSampleSource, reviewGenerationRef])

  // Load a set of externally-dropped files exactly ONCE, when that set changes —
  // not on every format/preset change. initialFiles is held in authoring state
  // for the whole import session, so depending on `format`/`handleFilesLoad`
  // (which itself depends on format) re-ran this effect on every user format
  // change, re-loading the dropped file over the user's edits and snapping the
  // Format dropdown back to the file's inferred format.
  const processedInitialFilesRef = useRef<typeof initialFiles | null>(null)
  useEffect(() => {
    if (!initialFiles || initialFiles.length === 0) return
    if (processedInitialFilesRef.current === initialFiles) return
    processedInitialFilesRef.current = initialFiles
    const nextFormat = inferSampleFormatFromFile(initialFiles[0], format)
    if (nextFormat !== format) {
      setFormat(nextFormat)
    }
    void handleFilesLoad(supportsMultiPrimary(nextFormat) ? initialFiles : [initialFiles[0]], 'primary', nextFormat)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialFiles])

  const handleDragOver = (event: React.DragEvent, target: 'primary' | 'secondary') => {
    event.preventDefault()
    if (event.dataTransfer.types.includes('Files')) {
      setDragTarget(target)
      event.dataTransfer.dropEffect = 'copy'
    }
  }

  const handleDragLeave = (event: React.DragEvent, target: 'primary' | 'secondary') => {
    event.preventDefault()
    if (dragTarget === target) {
      setDragTarget(null)
    }
  }

  // Drop feedback is per field, so route to the matching animation.
  const ingestFor = useCallback((target: 'primary' | 'secondary') => (
    target === 'primary'
      ? { begin: beginPrimaryIngest, succeed: succeedPrimaryIngest, fail: failPrimaryIngest }
      : { begin: beginSecondaryIngest, succeed: succeedSecondaryIngest, fail: failSecondaryIngest }
  ), [beginPrimaryIngest, succeedPrimaryIngest, failPrimaryIngest, beginSecondaryIngest, succeedSecondaryIngest, failSecondaryIngest])

  // Only the newest drop per field owns that field's animation. A load can be
  // superseded by an unrelated invalidation (typing in the textarea, changing
  // format/options) as well as by a newer drop, so the sequence — not the
  // 'superseded' outcome — decides who is allowed to settle the spinner.
  const ingestSeqRef = useRef({ primary: 0, secondary: 0 })

  const beginIngest = useCallback((target: 'primary' | 'secondary', count: number) => {
    const seq = ingestSeqRef.current[target] + 1
    ingestSeqRef.current[target] = seq
    ingestFor(target).begin(describeReading(count))
    return seq
  }, [ingestFor])

  const settleIngest = useCallback((target: 'primary' | 'secondary', seq: number, outcome: 'loaded' | 'superseded' | 'error', count: number) => {
    // A newer drop on this field is already animating — leave it alone.
    if (ingestSeqRef.current[target] !== seq) return
    const ingest = ingestFor(target)
    // 'superseded' here means the content never landed, so it must clear rather
    // than confirm — otherwise the spinner runs until the watchdog fires.
    if (outcome === 'loaded') ingest.succeed(describeAccepted(count))
    else ingest.fail()
  }, [ingestFor])

  const handleDrop = (event: React.DragEvent, target: 'primary' | 'secondary') => {
    event.preventDefault()
    setDragTarget(null)
    if (desktop) return
    const files = Array.from(event.dataTransfer.files || [])
    if (files.length > 0) {
      const allowMultiple = target === 'primary' ? supportsMultiPrimary(format) : supportsMultiSecondary(format)
      const selected = allowMultiple ? files : [files[0]]
      const seq = beginIngest(target, selected.length)
      void handleFilesLoad(selected, target).then(outcome => settleIngest(target, seq, outcome, selected.length))
    }
  }

  const handleNativeSamplePaths = useCallback((paths: string[], target: 'primary' | 'secondary') => {
    const seq = beginIngest(target, paths.length)
    void filesFromDroppedPaths(paths).then(files => {
      setDragTarget(null)
      if (files.length === 0) {
        settleIngest(target, seq, 'error', 0)
        return
      }
      const allowMultiple = target === 'primary' ? supportsMultiPrimary(format) : supportsMultiSecondary(format)
      const selected = allowMultiple ? files : [files[0]]
      void handleFilesLoad(selected, target).then(outcome => settleIngest(target, seq, outcome, selected.length))
    }).catch(error => {
      setDragTarget(null)
      settleIngest(target, seq, 'error', 0)
      setMessageError(nativeFileReadErrorMessage(error))
      if (shouldUseBrowserFileFallback(error)) setBrowserPickerFallback(true)
    })
  }, [format, handleFilesLoad, beginIngest, settleIngest])
  const handleNativePrimaryPaths = useCallback((paths: string[]) => handleNativeSamplePaths(paths, 'primary'), [handleNativeSamplePaths])
  const handleNativeSecondaryPaths = useCallback((paths: string[]) => handleNativeSamplePaths(paths, 'secondary'), [handleNativeSamplePaths])
  useDesktopFileDrop(primaryDropRef, handleNativePrimaryPaths, desktop)
  useDesktopFileDrop(secondaryDropRef, handleNativeSecondaryPaths, desktop)

  const handleBrowseSample = useCallback((target: 'primary' | 'secondary') => {
    const accept = target === 'primary' ? sampleAcceptForFormat(format) : '.txt,.json,.http,.log'
    const multiple = target === 'primary' ? supportsMultiPrimary(format) : supportsMultiSecondary(format)
    if (nativeDialogs) {
      void pickFilesViaBridge(accept, multiple).then(files => {
        if (files.length > 0) {
          void handleFilesLoad(multiple ? files : [files[0]], target)
        }
      }).catch(error => {
        setMessageError(nativeFileReadErrorMessage(error))
        if (shouldUseBrowserFileFallback(error)) {
          setBrowserPickerFallback(true)
          if (target === 'primary') primaryFileRef.current?.click()
          else secondaryFileRef.current?.click()
        }
      })
      return
    }
    if (target === 'primary') {
      primaryFileRef.current?.click()
    } else {
      secondaryFileRef.current?.click()
    }
  }, [format, handleFilesLoad, nativeDialogs])

  const buildScriptWithOptions = (baseScript?: ScriptDefinition) => {
    const script = JSON.parse(JSON.stringify(baseScript || {})) as ScriptDefinition
    script.name = options.name.trim() || script.name
    script.config = script.config || {}
    script.config.base_url = options.base_url.trim() || script.config.base_url || ''
    script.config.think_time = {
      type: 'fixed',
      mean_ms: Number(options.think_time_ms) || 1000,
    }
    script.config.pacing = options.pacing_type === 'fixed_delay'
      ? { type: 'fixed_delay', delay_ms: Number(options.pacing_delay_ms) || 0 }
      : { type: 'none' }
    script.config.error_handling = {
      ...(script.config.error_handling || {}),
      continue_on_error: options.continue_on_error,
      max_retries: Number(options.max_retries) || 0,
    }
    script.config.http = {
      ...(script.config.http || {}),
      request_timeout_ms: Number(options.request_timeout_ms) || 60000,
    }
    if (options.user_agent.trim()) {
      script.config.http.user_agent = options.user_agent.trim()
    }

    const parsedHeaders = parseDefaultHeaders(options.default_headers)
    if (parsedHeaders && parsedHeaders !== true) {
      script.config.http.default_headers = parsedHeaders
    }

    if (options.auth_type === 'basic') {
      script.config.http.auth = {
        type: 'basic',
        username: options.auth_username.trim(),
        password: options.auth_password.trim() ? secretBindingReference(options.auth_password) : '',
      }
    } else if (options.auth_type === 'bearer') {
      script.config.http.auth = {
        type: 'bearer',
        token: options.auth_token.trim() ? secretBindingReference(options.auth_token) : '',
      }
    }

    return script
  }

  const handleConvert = async () => {
    if (!primaryContent.trim()) {
      setMessageError('Provide sample input before converting.')
      return
    }
    const headersValid = parseDefaultHeaders(options.default_headers, true)
    if (headersValid === false) {
      setMessageError('Default headers must be a valid JSON object.')
      return
    }
    const parsedDefaultHeaders = parseDefaultHeaders(options.default_headers)
    if (parsedDefaultHeaders && parsedDefaultHeaders !== true) {
      const unsafeHeader = unsafeDefaultHeader(parsedDefaultHeaders)
      if (unsafeHeader) {
        setMessageError(`Default header ${unsafeHeader} is sensitive. Use a {{secret.NAME}} reference (optionally prefixed by Bearer, Basic, or Digest) instead of a credential value.`)
        return
      }
    }
    if (options.auth_type === 'basic' && options.auth_password.trim() && !isValidSecretBindingName(options.auth_password)) {
      setMessageError('Password secret binding must be a 1–249 character logical name using uppercase letters, numbers, or underscores.')
      return
    }
    if (options.auth_type === 'bearer' && options.auth_token.trim() && !isValidSecretBindingName(options.auth_token)) {
      setMessageError('Bearer token secret binding must be a 1–249 character logical name using uppercase letters, numbers, or underscores.')
      return
    }

    const sourceGeneration = advanceReviewGeneration()
    const sourceProjectId = reviewProjectIdRef.current
    const requestIsCurrent = () => reviewGenerationRef.current === sourceGeneration
      && reviewProjectIdRef.current === sourceProjectId
    setMessageError('')
    resetSaveTask()
    try {
      await convertTask.run(
        async () => {
          const res = await api.importSample({
            name: options.name || undefined,
            project_id: sourceProjectId,
            format,
            content: primaryContent,
            response_content: secondaryContent || undefined,
            options: {
              base_url: options.base_url || undefined,
              include_assertions: options.include_assertions,
              infer_extractors: options.infer_extractors,
              sanitize_sensitive: options.sanitize_sensitive,
              include_static_assets: format === 'har' ? options.include_static_assets : undefined,
            },
          })

          if (!requestIsCurrent()) {
            sourceSecretSessions.discardUnaccepted(sourceProjectId, res.source_secret_session_id)
            throw new Error('The sample source changed while conversion was running. Review the current input and try again.')
          }
          sourceSecretSessions.replace('conversion', sourceProjectId, res.source_secret_session_id)

          const builtScript = buildScriptWithOptions(res.script)
          const finalScript = isJavaHttpPreset ? promoteScriptToJavaHTTP(builtScript) : builtScript
          const correlatedScript = attachImportCorrelationSuggestions(finalScript, 'sample-import', res.correlations)
          const warnings = isJavaHttpPreset ? appendJavaHTTPWarnings(finalScript, res.warnings) : res.warnings

          return {
            ...res,
            script: correlatedScript,
            warnings,
          }
        },
        { clearValue: true },
      )
      if (requestIsCurrent()) setAssetUploads({})
    } catch (error: unknown) {
      if (requestIsCurrent()) setMessageError(getErrorMessage(error, 'Sample conversion failed'))
    }
  }

  const handleSave = async (destination: ImportSaveDestination = 'stay') => {
    if (savedResult) return
    const publish = (result: ImportedSaveResult) => publishSaved(result, destination)
    const sourceGeneration = reviewGenerationRef.current
    const sourceProjectId = reviewProjectIdRef.current
    const sourceResult = latestResultRef.current
    const scriptToSave = latestResultRef.current?.script ?? latestResultRef.current?.scripts?.[0]
    if (!scriptToSave) return
    if (fileAssetSources(scriptToSave).some(source => !isFileAssetReady(source))) {
      setMessageError('Upload every imported file asset before saving this script.')
      return
    }
    const requestIsCurrent = () => reviewGenerationRef.current === sourceGeneration
      && reviewProjectIdRef.current === sourceProjectId
      && latestResultRef.current === sourceResult
    setMessageError('')
    try {
      const savedScript = await saveTask.run(async () => {
        const destinationFolderId = scriptToSave.folder_id ?? folderId ?? null
        const retained = genericBatchSaveOperationRef.current
        const operationId = retained
          && retained.result === sourceResult
          && retained.projectId === sourceProjectId
          && retained.folderId === destinationFolderId
          ? retained.operationId
          : `sample-import-${createReplaySessionId()}`
        genericBatchSaveOperationRef.current = {
          result: sourceResult!,
          projectId: sourceProjectId,
          folderId: destinationFolderId,
          operationId,
        }
        const response = await api.commitGenericImportBatch({
          scripts: [scriptToSave],
          folder_id: destinationFolderId,
          operation_id: operationId,
          source_profile: 'sample',
        })
        if (response.count !== 1 || response.scripts.length !== 1) {
          throw new Error('The atomic sample import save did not return the stored script. Retry the same save to reconcile its result.')
        }
        if (genericBatchSaveOperationRef.current?.operationId === operationId) {
          genericBatchSaveOperationRef.current = null
        }
        return response.scripts[0]
      })
      publish({
        scripts: [withFallbackPersistedScript(savedScript, scriptToSave)],
        ...(!requestIsCurrent() ? { stayInImportReview: true } : {}),
      })
    } catch (error: unknown) {
      if (requestIsCurrent()) setMessageError(getErrorMessage(error, 'Failed to save generated script'))
    }
  }

  const handleEdit = () => {
    const currentResult = latestResultRef.current
    const currentScript = currentResult?.script ?? currentResult?.scripts?.[0]
    if (!currentScript) return
    if (credentialPreservingImportDraftRequiresSave(
      currentResult,
      currentScript,
      currentResult?.secret_binding_requirements,
    )) {
      if (!onImported) {
        setMessageError('Save this credential-preserving import before opening it in the Visual Editor.')
        return
      }
      void handleSave('editor')
      return
    }
    onEdit?.(currentScript)
  }

  const handleParameterizationApplied = (
    parameterizedScript: ScriptDefinition,
    _diffs: unknown[],
    _selectedIds: string[],
    importDraftExpiresAt?: string,
  ) => {
    if (!result) return
    advanceReviewGeneration()
    resetSaveTask()
    const preflight = buildLocalReplayPreflight(parameterizedScript)
    convertTask.setValue({
      ...replaceImportResultScript(result, attachReplayPreflight(parameterizedScript, preflight), preflight),
      import_draft_expires_at: importDraftExpiresAt,
    })
  }

  const handleSharedValuesApplied = (
    updatedScripts: ScriptDefinition[],
    _diffs: unknown[],
    refreshed?: SharedValues,
    importDraftExpiresAt?: (string | null)[],
  ) => {
    if (!result || updatedScripts.length === 0) return
    advanceReviewGeneration()
    resetSaveTask()
    const preflight = buildLocalReplayPreflight(updatedScripts[0])
    const scripts = updatedScripts.map((script, index) => (
      index === 0 ? attachReplayPreflight(script, preflight) : script
    ))
    const rotatedExpiry = earliestImportDraftExpiry(importDraftExpiresAt)
    const nextResult: SampleImportResult = {
      ...result,
      script: scripts[0],
      scripts,
      preflight,
      ...(refreshed ? { shared_values: refreshed } : {}),
      ...(rotatedExpiry ? { import_draft_expires_at: rotatedExpiry } : {}),
    }
    latestResultRef.current = nextResult
    convertTask.setValue(nextResult)
  }

  const handleSecretBindingsApply = async (bindings: SecretBindingSelection[]) => {
    if (!result || !reviewedScript) return
    const sourceGeneration = reviewGenerationRef.current
    const sourceProjectId = reviewProjectIdRef.current
    const requestIsCurrent = () => reviewGenerationRef.current === sourceGeneration && reviewProjectIdRef.current === sourceProjectId
    try {
      const resolved = await api.applySecretBindings({ script: reviewedScript, bindings })
      if (!requestIsCurrent()) throw new Error('The import result changed while credentials were being prepared. Review the current draft and try again.')
      const preflight = buildLocalReplayPreflight(resolved.script)
      const nextResult = {
        ...replaceImportResultScript(result, attachReplayPreflight(resolved.script, preflight), preflight),
        secret_binding_requirements: resolved.secret_binding_requirements,
        import_draft_expires_at: resolved.import_draft_expires_at,
      }
      latestResultRef.current = nextResult
      convertTask.setValue(nextResult)
    } catch (error: unknown) {
      if (!requestIsCurrent()) throw error
      const refreshed = refreshedSecretBindingRequirements(error)
      if (refreshed) convertTask.setValue({ ...result, secret_binding_requirements: refreshed })
      throw error
    }
  }

  const handleAssetUpload = async (source: ScriptDataSource, file: File) => {
    if (!result || !reviewedScript) return
    const sourceGeneration = advanceReviewGeneration()
    const sourceProjectId = reviewProjectIdRef.current
    const targetIndex = (reviewedScript.data_sources || []).indexOf(source)
    const key = assetUploadKey(source, targetIndex)
    setMessageError('')
    setAssetUploads(prev => ({
      ...prev,
      [key]: { pending: true, fileName: file.name },
    }))
    try {
      const uploaded = await api.uploadImportAsset(importAssetID(reviewedScript), file, {
        project_id: projectId,
        name: source.name,
        content_type: source.content_type || file.type || undefined,
      })
      if (reviewGenerationRef.current !== sourceGeneration || reviewProjectIdRef.current !== sourceProjectId) return
      const mergedScript = mergeUploadedFileAsset(reviewedScript, targetIndex, uploaded.data_source)
      const preflight = buildLocalReplayPreflight(mergedScript)
      const scriptWithPreflight = attachReplayPreflight(mergedScript, preflight)
      convertTask.setValue(replaceImportResultScript(result, scriptWithPreflight, preflight))
      setAssetUploads(prev => ({
        ...prev,
        [key]: { pending: false, fileName: file.name },
      }))
    } catch (error: unknown) {
      if (reviewGenerationRef.current !== sourceGeneration || reviewProjectIdRef.current !== sourceProjectId) return
      setAssetUploads(prev => ({
        ...prev,
        [key]: {
          pending: false,
          fileName: file.name,
          error: getErrorMessage(error, 'Asset upload failed'),
        },
      }))
    }
  }

  return (
    <div className="sample-import">
      <ImportSaveConfirmation result={savedResult} onImportMore={() => handleBrowseSample('primary')} onOpen={openSaved} disabled={operationBusy} />
      <div className="sample-import-header">
        <div>
          <h2>{isJavaHttpPreset ? 'Import Java over HTTP Samples' : 'Build Script From Sample'}</h2>
          <p className="sample-import-subtitle">
            {isJavaHttpPreset
              ? 'Create a Java-over-HTTP draft from raw HTTP or cURL samples that already preserve base64-encoded serialized Java payloads.'
              : 'Create an APEX Load script from raw HTTP, curl, REST JSON, or HAR data, then layer in runtime details before saving.'}
          </p>
        </div>
        {onClose && <button type="button" className="btn btn-secondary" onClick={() => {
          invalidateSampleSource()
          onClose()
        }}>Back</button>}
      </div>

      <ImportWorkflowGuide stage={savedResult ? 'saved' : result ? 'review' : 'source'} />

      <details className="import-source-setup" open={!result}>
        <summary>Source & conversion options — changing these requires a new review</summary>
      <div className="sample-import-card">
        <div className="sample-import-section-header">
          <div>
            <h3>1. Input Format</h3>
            <p>{getFormatHelp(preset, format)}</p>
          </div>
          <div className="sample-import-format-controls">
            <select aria-label="Sample format" value={format} onChange={e => handleFormatChange(e.target.value as SampleFormat)}>
              {availableFormats.map((option) => (
                <option key={option} value={option}>
                  {option === 'rest_json' ? 'REST JSON' : option === 'raw_http' ? 'Raw HTTP' : option === 'curl' ? 'cURL' : 'HAR / HAR Entry'}
                </option>
              ))}
            </select>
            <select aria-label="Payload profile" value={preset} onChange={e => handlePresetChange(e.target.value as SampleImportPreset)}>
              <option value="default">Standard API</option>
              <option value="java-http">Java over HTTP</option>
            </select>
          </div>
        </div>

        <div className="settings-notice">
          {isJavaHttpPreset
            ? 'Java-over-HTTP imports are intentionally bounded to raw HTTP and cURL samples whose opaque request bodies are already base64-encoded serialized Java bytes. The generated draft is promoted to the `java_http` protocol for review in the editor.'
            : 'SOAP-shaped raw HTTP, curl, REST JSON, and HAR request samples can now be promoted into SOAP scripts when the imported sample set is consistently SOAP-oriented. Mixed sample sets still stay on the HTTP protocol surface.'}
        </div>

        <div className="sample-import-grid two-col">
          <label
            ref={primaryDropRef}
            className={`sample-import-field sample-import-span-2 ${dragTarget === 'primary' ? 'sample-import-drop-active' : ''}`}
            style={desktop ? ({ '--wails-drop-target': 'drop' } as CSSProperties) : undefined}
            onDragOver={event => handleDragOver(event, 'primary')}
            onDragLeave={event => handleDragLeave(event, 'primary')}
            onDrop={event => handleDrop(event, 'primary')}
          >
            <span>Primary Sample</span>
            <div className={`sample-import-upload-row ${primaryIngest.hostClassName}`}>
              <DropIngestOverlay phase={primaryIngest.phase} label={primaryIngest.label} inline />
              <input
                ref={primaryFileRef}
                type="file"
                accept={sampleAcceptForFormat(format)}
                multiple={supportsMultiPrimary(format)}
                hidden
                onChange={e => {
                  const files = Array.from(e.target.files || [])
                  if (files.length > 0) handleFilesLoad(files, 'primary')
                  e.target.value = ''
                }}
              />
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => handleBrowseSample('primary')}>
                {supportsMultiPrimary(format) ? 'Upload Sample Files' : 'Upload Sample File'}
              </button>
              <span className="sample-import-drop-hint">{supportsMultiPrimary(format) ? 'or drag files here' : 'or drag a file here'}</span>
              {primaryFileName && <span className="sample-import-file-name">{primaryFileName}</span>}
            </div>
            {primaryFileNames.length > 0 && (
              <div className="sample-import-upload-summary">
                <p>{describeMergedFiles(format, 'primary', primaryFileNames, primaryMergedCount)}</p>
                <div className="sample-import-file-list">
                  {primaryFileNames.map((name) => <span key={name} className="sample-import-file-chip">{name}</span>)}
                </div>
              </div>
            )}
            <textarea aria-label="Primary Sample" value={primaryContent} onChange={e => { invalidateSampleSource(); setPrimaryContent(e.target.value) }} rows={format === 'curl' ? 8 : 14} spellCheck={false} />
          </label>

          {(format === 'raw_http' || format === 'curl') && (
            <label
              ref={secondaryDropRef}
              className={`sample-import-field sample-import-span-2 ${dragTarget === 'secondary' ? 'sample-import-drop-active' : ''}`}
              style={desktop ? ({ '--wails-drop-target': 'drop' } as CSSProperties) : undefined}
              onDragOver={event => handleDragOver(event, 'secondary')}
              onDragLeave={event => handleDragLeave(event, 'secondary')}
              onDrop={event => handleDrop(event, 'secondary')}
            >
              <span>Sample Response (optional)</span>
              <div className={`sample-import-upload-row ${secondaryIngest.hostClassName}`}>
                <DropIngestOverlay phase={secondaryIngest.phase} label={secondaryIngest.label} inline />
                <input
                  ref={secondaryFileRef}
                  type="file"
                  accept=".txt,.json,.http,.log"
                  multiple={supportsMultiSecondary(format)}
                  hidden
                  onChange={e => {
                    const files = Array.from(e.target.files || [])
                    if (files.length > 0) handleFilesLoad(files, 'secondary')
                    e.target.value = ''
                  }}
                />
                <button type="button" className="btn btn-secondary btn-sm" onClick={() => handleBrowseSample('secondary')}>
                  {supportsMultiSecondary(format) ? 'Upload Response Files' : 'Upload Response File'}
                </button>
                <span className="sample-import-drop-hint">{supportsMultiSecondary(format) ? 'or drag files here' : 'or drag a file here'}</span>
                {secondaryFileName && <span className="sample-import-file-name">{secondaryFileName}</span>}
              </div>
              {secondaryFileNames.length > 0 && (
                <div className="sample-import-upload-summary">
                  <p>{describeMergedFiles(format, 'secondary', secondaryFileNames, secondaryMergedCount)}</p>
                  <div className="sample-import-file-list">
                    {secondaryFileNames.map((name) => <span key={name} className="sample-import-file-chip">{name}</span>)}
                  </div>
                </div>
              )}
              <textarea aria-label="Sample Response (optional)" value={secondaryContent} onChange={e => { invalidateSampleSource(); setSecondaryContent(e.target.value) }} rows={10} spellCheck={false} />
            </label>
          )}
        </div>
      </div>

      <div className="sample-import-card">
        <div className="sample-import-section-header">
          <div>
            <h3>2. Script Details</h3>
            <p>Capture the runtime and protocol details that usually need to be set before a script is usable.</p>
          </div>
        </div>

        <div className="sample-import-grid three-col">
          <label className="sample-import-field">
            <span>Script Name</span>
            <input value={options.name} onChange={e => updateOption('name', e.target.value)} placeholder="Order Flow Sample" />
          </label>
          <label className="sample-import-field">
            <span>Base URL Override</span>
            <input value={options.base_url} onChange={e => updateOption('base_url', e.target.value)} placeholder="https://api.example.com" />
          </label>
          <label className="sample-import-field">
            <span>Request Timeout (ms)</span>
            <input value={options.request_timeout_ms} onChange={e => updateOption('request_timeout_ms', e.target.value)} inputMode="numeric" />
          </label>

          <label className="sample-import-field">
            <span>Think Time (ms)</span>
            <input value={options.think_time_ms} onChange={e => updateOption('think_time_ms', e.target.value)} inputMode="numeric" />
          </label>
          <label className="sample-import-field">
            <span>Pacing</span>
            <select value={options.pacing_type} onChange={e => updateOption('pacing_type', e.target.value as PacingType)}>
              <option value="none">None</option>
              <option value="fixed_delay">Fixed Delay</option>
            </select>
          </label>
          <label className="sample-import-field">
            <span>Pacing Delay (seconds)</span>
            <SecondsFromMillisecondsInput
              valueMs={options.pacing_delay_ms.trim() === '' ? undefined : Number(options.pacing_delay_ms)}
              emptyWhenUndefined
              onChangeMs={valueMs => updateOption('pacing_delay_ms', valueMs === undefined ? '' : String(valueMs))}
              disabled={options.pacing_type !== 'fixed_delay'}
            />
          </label>

          <label className="sample-import-field">
            <span>Authentication</span>
            <select value={options.auth_type} onChange={e => updateOption('auth_type', e.target.value as AuthType)}>
              <option value="none">None</option>
              <option value="basic">Basic</option>
              <option value="bearer">Bearer</option>
            </select>
          </label>
          <label className="sample-import-field">
            <span>User Agent</span>
            <input value={options.user_agent} onChange={e => updateOption('user_agent', e.target.value)} placeholder="APEX Load Sample Import" />
          </label>
          <label className="sample-import-field">
            <span>Max Retries</span>
            <input value={options.max_retries} onChange={e => updateOption('max_retries', e.target.value)} inputMode="numeric" />
          </label>

          {options.auth_type === 'basic' && (
            <>
              <label className="sample-import-field">
                <span>Auth Username</span>
                <input value={options.auth_username} onChange={e => updateOption('auth_username', e.target.value)} />
              </label>
              <label className="sample-import-field">
                <span>Password Secret Binding</span>
                <input type="text" value={options.auth_password} onChange={e => updateOption('auth_password', e.target.value)} autoComplete="off" spellCheck={false} placeholder="LOGIN_PASSWORD" />
                <small>Enter a logical binding name, not the password value.</small>
              </label>
            </>
          )}

          {options.auth_type === 'bearer' && (
            <label className="sample-import-field sample-import-span-2">
              <span>Bearer Token Secret Binding</span>
              <input type="text" value={options.auth_token} onChange={e => updateOption('auth_token', e.target.value)} autoComplete="off" spellCheck={false} placeholder="API_TOKEN" />
              <small>Enter a logical binding name, not the token value.</small>
            </label>
          )}

          <label className="sample-import-field sample-import-span-3">
            <span>Default Headers (JSON object, optional)</span>
            <textarea value={options.default_headers} onChange={e => updateOption('default_headers', e.target.value)} rows={5} spellCheck={false} placeholder='{"x-tenant-id":"demo"}' />
          </label>
        </div>

        <div className="sample-import-toggles">
          <label className="sample-toggle"><input type="checkbox" checked={options.include_assertions} onChange={e => updateOption('include_assertions', e.target.checked)} />Infer status assertions</label>
          <label className="sample-toggle"><input type="checkbox" checked={options.infer_extractors} onChange={e => updateOption('infer_extractors', e.target.checked)} />Infer extractors and correlation variables</label>
          <label className="sample-toggle"><input type="checkbox" checked={options.sanitize_sensitive} onChange={e => updateOption('sanitize_sensitive', e.target.checked)} />Sanitize obvious secrets</label>
          {format === 'har' && <label className="sample-toggle"><input type="checkbox" checked={options.include_static_assets} onChange={e => updateOption('include_static_assets', e.target.checked)} />Include static assets</label>}
          <label className="sample-toggle"><input type="checkbox" checked={options.continue_on_error} onChange={e => updateOption('continue_on_error', e.target.checked)} />Continue on error</label>
        </div>
      </div>

      <div className="sample-import-actions">
        <button className="btn btn-primary btn-lg" onClick={handleConvert} disabled={converting || !primaryContent.trim()}>
          {converting ? <><span className="spinner" /> Converting…</> : 'Inspect & review'}
        </button>
        <button className="btn btn-secondary" onClick={() => handleFormatChange(format)}>Reset Template</button>
      </div>

      </details>
      {error && <div className="sample-import-error" role="alert">{error}</div>}
      {result && reviewedScript && (
        <div className="sample-import-result">
          <ScriptImportReview
            tabbed
            dataContent={importFileAssets.length > 0 ? <>
            {importFileAssets.length > 0 && (
              <div className="sample-import-assets">
                <div className="sample-import-assets-header">
                  <div>
                    <h4>Import Assets</h4>
                    <p>{hasMissingFileAssets ? 'Upload the captured binary or multipart files before saving this script.' : 'All imported file assets are ready for replay.'}</p>
                  </div>
                  <span>{importFileAssets.filter(isFileAssetReady).length}/{importFileAssets.length} ready</span>
                </div>
                <div className="sample-import-assets-list">
                  {importFileAssets.map(source => {
                    const ready = isFileAssetReady(source)
                    const fullIndex = (reviewedScript?.data_sources || []).indexOf(source)
                    const key = assetUploadKey(source, fullIndex)
                    const uploadState = assetUploads[key]
                    return (
                      <div key={key} className={`sample-import-asset-row ${ready ? 'ready' : 'missing'}`}>
                        <div className="sample-import-asset-main">
                          <strong>{source.file_name || source.name}</strong>
                          <small>{source.content_type || 'application/octet-stream'} · {source.name}</small>
                          {ready && <small>{source.storage_ref || source.path || 'Inline asset data'}</small>}
                          {uploadState?.error && <small className="sample-import-asset-error">{uploadState.error}</small>}
                          {uploadState?.fileName && !uploadState.error && <small>Uploaded {uploadState.fileName}</small>}
                        </div>
                        <div className="sample-import-asset-controls">
                          <span className={`sample-import-asset-status ${ready ? 'ready' : 'missing'}`}>{ready ? 'Ready' : 'Missing asset'}</span>
                          {!ready && (
                            <label className="btn btn-secondary btn-sm">
                              {uploadState?.pending ? 'Uploading...' : 'Upload Asset'}
                              <input
                                type="file"
                                aria-label={`Upload asset for ${source.name}`}
                                disabled={uploadState?.pending}
                                hidden
                                onChange={event => {
                                  const file = event.target.files?.[0]
                                  event.target.value = ''
                                  if (file) void handleAssetUpload(source, file)
                                }}
                              />
                            </label>
                          )}
                        </div>
                      </div>
                    )
                  })}
                </div>
              </div>
            )}

            </> : undefined}
            runtimeContent={<p>Timeout, pacing, authentication, and default headers were captured from Script Details. To change conversion options, expand source setup and inspect again. Effective runtime settings can also be edited in the Visual Editor.</p>}
            title="3. Review Generated Script"
            reviewGeneration={reviewScope}
            subtitle={`${result.stats.converted_actions} action(s) derived from ${result.stats.total_requests} request sample(s).`}
            script={reviewedScript}
            warnings={result.warnings}
            unsupportedElements={result.unsupported_elements}
            reviewSections={result.review_sections}
            parameterization={result.parameterization}
            onParameterizationApplied={handleParameterizationApplied}
            secretBindingRequirements={result.secret_binding_requirements}
            onSecretBindingsApply={handleSecretBindingsApply}
            sharedValues={result.shared_values}
            sharedValueScripts={result.scripts}
            onSharedValuesApplied={handleSharedValuesApplied}
            projectId={projectId}
            sourceSecretSessionId={result.source_secret_session_id}
            sourceSecretSessionExpiresAt={result.source_secret_session_expires_at}
            sourceSecretMigrationAllowed={result.source_secret_migration_allowed}
            importDraftExpiresAt={result.import_draft_expires_at}
            credentialReviewRef={credentialReviewRef}
            saving={saving}
            onReviewBusyChange={setInternalReviewBusy}
            saveDisabled={Boolean(savedResult) || hasMissingFileAssets}
            saveLabel={savedResult ? 'Saved to Script Library' : 'Save draft'}
            saveDisabledReason={savedResult ? 'This script is already saved. Open it above or import more files.' : hasMissingFileAssets
              ? 'Upload every missing imported file asset before saving this script.'
              : undefined}
            onSave={handleSave}
            onEdit={onEdit && !savedResult ? handleEdit : undefined}
            editLabel={editRequiresCredentialPreservingSave ? 'Save and Open in Visual Editor' : 'Open Draft in Visual Editor'}
            previewAside={sourceReview ? <SourceMappingReview sourceReview={sourceReview} script={reviewedScript} title="Source Comparison" /> : undefined}
          >
            {replayPreflight && (
              <div className="sample-import-preflight">
                <ReplayPreflightCard preflight={replayPreflight} />
              </div>
            )}

            <div className="sample-import-stats-grid">
              <div className="sample-stat"><span className="sample-stat-value">{result.stats.converted_actions}</span><span className="sample-stat-label">Actions</span></div>
              <div className="sample-stat"><span className="sample-stat-value">{result.stats.assertions}</span><span className="sample-stat-label">Assertions</span></div>
              <div className="sample-stat"><span className="sample-stat-value">{result.stats.extractors}</span><span className="sample-stat-label">Extractors</span></div>
              <div className="sample-stat"><span className="sample-stat-value">{result.stats.correlations_detected}</span><span className="sample-stat-label">Correlations</span></div>
              {typeof result.stats.filtered_assets === 'number' && result.stats.filtered_assets > 0 && <div className="sample-stat"><span className="sample-stat-value">{result.stats.filtered_assets}</span><span className="sample-stat-label">Filtered assets</span></div>}
            </div>
          </ScriptImportReview>
        </div>
      )}
    </div>
  )
}

function parseDefaultHeaders(value: string, validateOnly = false) {
  if (!value.trim()) {
    return validateOnly ? true : null
  }
  try {
    const parsed = JSON.parse(value)
    if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') {
      return validateOnly ? false : null
    }
    const normalized: Record<string, string> = {}
    Object.entries(parsed).forEach(([key, entryValue]) => {
      normalized[key] = String(entryValue)
    })
    return validateOnly ? true : normalized
  } catch {
    return validateOnly ? false : null
  }
}
