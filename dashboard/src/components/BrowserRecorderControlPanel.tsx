import { type ReactNode, useState, useEffect, useCallback, useLayoutEffect, useMemo, useRef } from 'react'

import { useApi } from '../hooks/useApi'
import { hasDesktopApiCapability } from '../hooks/apiClient'
import { getErrorMessage } from '../utils/errorUtils'
import { downloadBlob } from '../utils/fileUtils'
import { isDesktopSurface } from '../desktop/desktopBridge'
import { saveApiResourceViaBridge, startDesktopBrowserDownload } from '../desktop/desktopFiles'
import {
  type BrowserRecorderSession,
  type BrowserRecorderStep,
  type BrowserRecorderTransactionSuggestion,
} from '../hooks/api/useBrowserRecorderApi'
import { type BrowserReplayReport, type ReplayRepairSuggestion, type StoredScript } from '../types'
import {
  type RecorderImportResult,
  type SalesforceImportResult,
  type SecretBindingSelection,
} from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'
import {
  composeHttpURL,
  DEFAULT_HTTP_URL_SCHEME,
  normalizeHttpURLInput,
  type HttpURLScheme,
} from '../utils/httpUrlInput'
import { type ImportedSaveResult } from './useScriptsAuthoringState'
import BrowserReplayProgress, { type BrowserReplayProgressProps } from './BrowserReplayProgress'
import { withFallbackPersistedScript } from './importSaveUtils'
import ParameterizationReview from './ParameterizationReview'
import SecretBindingReview, {
  type PreparedSecretBindings,
  type SecretBindingReviewHandle,
} from './SecretBindingReview'
import {
  refreshedSecretBindingRequirements,
  replaceSecretBindingRequirementsForScript,
  secretBindingRequirementsForScript,
} from './secretBindingUtils'
import {
  credentialPreservingImportDraftRequiresSave,
  importDraftExpiryStatus,
} from './protectedImportSession'
import { createReplaySessionId, isDefiniteReplayStartRejection } from './scriptEditorUtils'
import './RecordingStudio.css'

const SECTIONS: { value: string; label: string }[] = [
  { value: 'init', label: 'Init' },
  { value: 'actions', label: 'Main' },
  { value: 'teardown', label: 'Teardown' },
]

// selectorQuality buckets a step's best selector confidence (0–100 ladder) into
// a badge tone, mirroring ParameterizationReview's banding.
function selectorQuality(step: BrowserRecorderStep): { tone: 'high' | 'medium' | 'low'; score: number } | null {
  const top = step.candidates?.[0]
  if (!top) return null
  const score = top.confidence
  if (score >= 82) return { tone: 'high', score }
  if (score >= 68) return { tone: 'medium', score }
  return { tone: 'low', score }
}

const INTERACTION_KINDS = new Set(['click', 'fill', 'select', 'press_key', 'set_files'])

const LIVE_STATUSES = new Set(['recording', 'paused', 'stopping'])
const CONTROLLABLE_STATUSES = new Set(['recording', 'paused'])
const IMPORT_DRAFT_EXPIRED_MESSAGE = 'This credential-preserving import draft has expired. Re-run the import before saving, editing, or changing it.'

function importDraftHasExpired(candidate: { import_draft_expires_at?: string } | null | undefined): boolean {
  return importDraftExpiryStatus(candidate?.import_draft_expires_at)?.expired === true
}

const STEP_KIND_LABELS: Record<string, string> = {
  navigate: 'Navigate',
  click: 'Click',
  fill: 'Fill',
  select: 'Select',
  press_key: 'Key',
  set_files: 'Upload',
  assert: 'Verify',
  switch_window: 'Window',
}

function stepDetail(step: BrowserRecorderStep): string {
  if (step.masked) return '•••••• (masked)'
  const target = step.element?.aria_label || step.element?.text || step.element?.name || step.element?.placeholder || step.element?.id || step.candidates?.[0]?.value
  if (target && step.kind !== 'navigate') return `${target}${step.value ? `: ${step.value}` : ''}`
  if (step.value) return step.value
  if (step.title) return step.title
  if (step.url) return step.url
  return ''
}

function statusLabel(session: BrowserRecorderSession): string {
  switch (session.status) {
    case 'recording':
      return 'Recording'
    case 'paused':
      return 'Paused'
    case 'stopping':
      return 'Stopping…'
    case 'stopped':
      if (session.stop_reason === 'browser_closed') return 'Stopped (browser closed)'
      if (session.stop_reason === 'forced') return 'Force finished'
      return 'Stopped'
    case 'failed':
      return 'Failed'
    default:
      return session.status
  }
}

function safeRecordingFileName(name?: string): string {
  return (name || 'browser-recording').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'browser-recording'
}

function hasSalesforceSignal(session: BrowserRecorderSession, preview?: RecorderImportResult | null): boolean {
  const text = [
    session.start_url,
    session.current_url,
    ...(session.warnings || []),
    ...(preview?.warnings || []),
  ].join(' ').toLowerCase()
  return [
    'salesforce traffic detected',
    'salesforce.com',
    '.force.com',
    'lightning.force',
    '/aura',
    'agentforce',
    'mulesoft',
    'anypoint',
  ].some(signal => text.includes(signal))
}

function importResultScripts(result?: RecorderImportResult | SalesforceImportResult | null): ScriptDefinition[] {
  if (result?.scripts?.length) return result.scripts
  return result?.script ? [result.script] : []
}

export interface BrowserRecorderControlPanelProps {
  sessionId: string
  /** Rendered as its own chrome-less page (popup window route). */
  standalone?: boolean
  /**
   * Destination folder for saved recordings. Absent in standalone mode, which
   * is a popup route with no surrounding folder context, so those saves keep
   * landing at the project root.
   */
  destinationControl?: ReactNode
  folderId?: string | null
  onImported?: (result: ImportedSaveResult) => void
  onEdit?: (script: ScriptDefinition) => void
  /** Docked mode: remove the panel from the studio after the user is done. */
  onDismiss?: () => void
  /** Reports nested save/review work so the parent can fence destination changes. */
  onOperationBusyChange?: (busy: boolean) => void
}

function HistoricalVerification(props: BrowserReplayProgressProps) {
  const [open, setOpen] = useState(false)
  return <details onToggle={event => setOpen(event.currentTarget.open)}><summary>Replay {props.replaySessionId}</summary>{open && <BrowserReplayProgress {...props} />}</details>
}

export default function BrowserRecorderControlPanel({
  sessionId,
  standalone = false,
  folderId,
  destinationControl,
  onImported,
  onEdit,
  onDismiss,
  onOperationBusyChange,
}: BrowserRecorderControlPanelProps) {
  const api = useApi()
  const genericBatchSaveRef = useRef<{
    source: RecorderImportResult | SalesforceImportResult
    projectId?: string
    folderId: string | null
    profile: 'browser_recorder' | 'salesforce'
    operationId: string
  } | null>(null)
  const panelActiveRef = useRef(true)
  useEffect(() => {
    panelActiveRef.current = true
    return () => {
      panelActiveRef.current = false
    }
  }, [])
  const [loadedSession, setSession] = useState<BrowserRecorderSession | null>(null)
  const session = loadedSession?.id === sessionId ? loadedSession : null
  const [loadError, setLoadError] = useState('')
  const [scriptName, setScriptName] = useState('')
  const [preview, setPreview] = useState<RecorderImportResult | null>(null)
  const [salesforceDraft, setSalesforceDraft] = useState<SalesforceImportResult | null>(null)
  const [previewPreparedSecrets, setPreviewPreparedSecrets] = useState<PreparedSecretBindings | null>(null)
  const [salesforcePreparedSecrets, setSalesforcePreparedSecrets] = useState<PreparedSecretBindings | null>(null)
  const [previewParameterizationApplying, setPreviewParameterizationApplying] = useState(false)
  const [salesforceParameterizationApplying, setSalesforceParameterizationApplying] = useState(false)
  const [importDraftExpiryGeneration, setImportDraftExpiryGeneration] = useState(0)
  const [savedScript, setSavedScript] = useState<StoredScript | null>(null)
  const [busy, setBusy] = useState(false)
  const operationBusy = busy || previewParameterizationApplying || salesforceParameterizationApplying
  useEffect(() => {
    onOperationBusyChange?.(operationBusy)
    return () => onOperationBusyChange?.(false)
  }, [onOperationBusyChange, operationBusy])
  const [feedback, setFeedback] = useState('')
  const [feedbackType, setFeedbackType] = useState<'success' | 'error'>('success')
  const [navigateScheme, setNavigateScheme] = useState<HttpURLScheme>(DEFAULT_HTTP_URL_SCHEME)
  const [navigateAddress, setNavigateAddress] = useState('')
  const [transactionName, setTransactionName] = useState('')
  const [noteText, setNoteText] = useState('')
  const [splitAtTransactions, setSplitAtTransactions] = useState(false)
  const [alsoCreateHTTP, setAlsoCreateHTTP] = useState(false)
  const [parameterizeInputs, setParameterizeInputs] = useState(false)
  const [suggestions, setSuggestions] = useState<BrowserRecorderTransactionSuggestion[]>([])
  const [replaySessionId, setReplaySessionId] = useState<string | null>(null)
  const replaySessionIdRef = useRef<string | null>(replaySessionId)
  replaySessionIdRef.current = replaySessionId
  const [lastVerifyReport, setLastVerifyReport] = useState<BrowserReplayReport | null>(null)
  const [stepSearch, setStepSearch] = useState('')
  const [showAllSteps, setShowAllSteps] = useState(false)
  const steps = useMemo(() => session?.steps || [], [session?.steps])
  const stepSearchQuery = stepSearch.trim().toLowerCase()
  const visibleSteps = useMemo(() => [...steps].sort((a, b) => session?.status === 'stopped' ? a.seq - b.seq : b.seq - a.seq).filter(step => {
    if (!stepSearchQuery) return true
    const detail = stepDetail(step)
    return [
      String(step.seq),
      step.kind,
      detail,
      step.url,
      step.title,
      step.section,
      step.candidates?.[0]?.value,
    ].filter(Boolean).join(' ').toLowerCase().includes(stepSearchQuery)
  }), [stepSearchQuery, steps, session?.status])
  const renderedSteps = useMemo(
    () => showAllSteps ? visibleSteps : visibleSteps.slice(0, 25),
    [showAllSteps, visibleSteps],
  )
  const [stepThumbnails, setStepThumbnails] = useState<Record<number, string>>({})
  const [stepThumbnailsLoading, setStepThumbnailsLoading] = useState(false)
  useEffect(() => {
    setShowAllSteps(false)
  }, [sessionId])
  const recorderRequestScope = api.browserRecorderRequestScope
  const refreshInFlightRef = useRef<{ sessionId: string; recorderRequestScope: string; request: Promise<void> } | null>(null)
  const verificationStartScopeRef = useRef<object | null>(null)
  const sessionScopeRef = useRef({ sessionId, recorderRequestScope, generation: 0 })
  const feedbackTimerRef = useRef<number | null>(null)
  const previewCredentialReviewRef = useRef<SecretBindingReviewHandle | null>(null)
  const salesforceCredentialReviewRef = useRef<SecretBindingReviewHandle | null>(null)
  const historyKey = `browser-verification-history:${recorderRequestScope}:${sessionId}`
  const [verificationHistory, setVerificationHistory] = useState<string[]>([])
  useEffect(() => {
    try {
      const ids: unknown = JSON.parse(sessionStorage.getItem(historyKey) || '[]')
      setVerificationHistory(Array.isArray(ids) ? ids.filter((id): id is string => typeof id === 'string').slice(0, 8) : [])
    } catch { setVerificationHistory([]) }
  }, [historyKey])
  const rememberVerification = (id: string) => {
    setVerificationHistory(previous => {
      const next = [id, ...previous.filter(value => value !== id)].slice(0, 8)
      try { sessionStorage.setItem(historyKey, JSON.stringify(next)) } catch { /* Evidence remains available for this panel. */ }
      return next
    })
  }
  const previewReviewGenerationRef = useRef(0)
  const salesforceReviewGenerationRef = useRef(0)
  const [previewReviewGeneration, setPreviewReviewGeneration] = useState(0)
  const [salesforceReviewGeneration, setSalesforceReviewGeneration] = useState(0)
  const [previewConfiguredCredentialReviewIdentity, setPreviewConfiguredCredentialReviewIdentity] = useState('')
  const [salesforceConfiguredCredentialReviewIdentity, setSalesforceConfiguredCredentialReviewIdentity] = useState('')
  const previewCredentialReviewIdentity = `${previewReviewGeneration}:${sessionId}`
  const salesforceCredentialReviewIdentity = `${salesforceReviewGeneration}:${sessionId}`
  const previewProjectSecretsConfiguredForReview = previewConfiguredCredentialReviewIdentity === previewCredentialReviewIdentity
  const salesforceProjectSecretsConfiguredForReview = salesforceConfiguredCredentialReviewIdentity === salesforceCredentialReviewIdentity
  const verificationRevisionRef = useRef(0)
  const verificationGenerationRef = useRef(0)
  const verificationOutputRef = useRef(0)
  const [outputReports, setOutputReports] = useState<Record<number, BrowserReplayReport>>({})
  const invalidateVerification = useCallback(() => {
    verificationRevisionRef.current += 1
    setFeedback(current => /^(Replay verification|Verifying)/.test(current) ? '' : current)
    setLastVerifyReport(null)
    setReplaySessionId(null)
    setOutputReports({})
  }, [])
  const advancePreviewReviewGeneration = useCallback(() => {
    invalidateVerification()
    const next = previewReviewGenerationRef.current + 1
    previewReviewGenerationRef.current = next
    setPreviewReviewGeneration(next)
    return next
  }, [invalidateVerification])
  const advanceSalesforceReviewGeneration = useCallback(() => {
    const next = salesforceReviewGenerationRef.current + 1
    salesforceReviewGenerationRef.current = next
    setSalesforceReviewGeneration(next)
    return next
  }, [])
  const previewPreparedScriptsRef = useRef<ScriptDefinition[] | null>(null)
  const previewSaveBindingsRef = useRef<Array<SecretBindingSelection & { script_index: number }>>([])
  const salesforcePreparedScriptsRef = useRef<ScriptDefinition[] | null>(null)
  const previewRequestKeyRef = useRef('')
  // Only value-free suggestion IDs cross the preview/save boundary. The server
  // re-derives their mutation instructions against the fresh conversion.
  const previewParameterizationSelectionsRef = useRef<Map<number, Set<string>>>(new Map())

  if (
    sessionScopeRef.current.sessionId !== sessionId
    || sessionScopeRef.current.recorderRequestScope !== recorderRequestScope
  ) {
    sessionScopeRef.current = {
      sessionId,
      recorderRequestScope,
      generation: sessionScopeRef.current.generation + 1,
    }
  }
  const renderedSessionScope = sessionScopeRef.current
  const isCurrentSession = () => panelActiveRef.current && sessionScopeRef.current === renderedSessionScope

  const showFeedback = (msg: string, type: 'success' | 'error' = 'success') => {
    setFeedback(msg)
    setFeedbackType(type)
    if (feedbackTimerRef.current !== null) window.clearTimeout(feedbackTimerRef.current)
    feedbackTimerRef.current = window.setTimeout(() => {
      feedbackTimerRef.current = null
      setFeedback('')
    }, 4000)
  }

  const recordingFingerprintRef = useRef('')
  const acceptRecordingSession = useCallback((next: BrowserRecorderSession) => {
    const fingerprint = JSON.stringify([next.id, next.steps, next.markers, next.start_url, next.device_profile])
    if (recordingFingerprintRef.current && recordingFingerprintRef.current !== fingerprint) {
      invalidateVerification()
      previewRequestKeyRef.current = ''
    }
    recordingFingerprintRef.current = fingerprint
    setSession(next)
  }, [invalidateVerification])

  const refresh = useCallback(async () => {
    const inFlight = refreshInFlightRef.current
    if (inFlight?.sessionId === sessionId && inFlight.recorderRequestScope === recorderRequestScope) return inFlight.request
    const scope = { ...sessionScopeRef.current }
    const request = (async () => {
      try {
        const res = await api.getBrowserRecording(sessionId)
        if (sessionScopeRef.current.sessionId !== scope.sessionId || sessionScopeRef.current.recorderRequestScope !== scope.recorderRequestScope || sessionScopeRef.current.generation !== scope.generation) return
        acceptRecordingSession(res.session)
        setLoadError('')
      } catch (e: any) {
        if (sessionScopeRef.current.sessionId !== scope.sessionId || sessionScopeRef.current.recorderRequestScope !== scope.recorderRequestScope || sessionScopeRef.current.generation !== scope.generation) return
        setLoadError(e.message || 'Failed to load browser recording session')
      }
    })()
    refreshInFlightRef.current = { sessionId, recorderRequestScope, request }
    try {
      await request
    } finally {
      if (refreshInFlightRef.current?.request === request) refreshInFlightRef.current = null
    }
  }, [api, recorderRequestScope, sessionId, acceptRecordingSession])

  const loadStepThumbnails = async () => {
    if (stepThumbnailsLoading || typeof api.getRawBrowserRecording !== 'function') return
    const scope = { ...sessionScopeRef.current }
    const requestIsCurrent = () => (
      panelActiveRef.current
      && sessionScopeRef.current.sessionId === scope.sessionId
      && sessionScopeRef.current.recorderRequestScope === scope.recorderRequestScope
      && sessionScopeRef.current.generation === scope.generation
    )
    setStepThumbnailsLoading(true)
    try {
      const response = await api.getRawBrowserRecording(sessionId)
      if (!requestIsCurrent()) return
      const thumbnails = Object.fromEntries(
        (response.session.steps || [])
          .filter(step => Boolean(step.thumbnail))
          .map(step => [step.seq, step.thumbnail!]),
      )
      setStepThumbnails(thumbnails)
      const count = Object.keys(thumbnails).length
      showFeedback(count > 0
        ? `Loaded ${count} screenshot thumbnail${count === 1 ? '' : 's'}.`
        : 'No screenshot thumbnails are available for this recording.')
    } catch (error: unknown) {
      if (requestIsCurrent()) showFeedback(getErrorMessage(error, 'Unable to load screenshot thumbnails'), 'error')
    } finally {
      if (requestIsCurrent()) setStepThumbnailsLoading(false)
    }
  }

  const loadSuggestions = useCallback(async () => {
    const scope = { ...sessionScopeRef.current }
    try {
      const res = await api.suggestBrowserRecordingTransactions(sessionId)
      if (sessionScopeRef.current.sessionId !== scope.sessionId || sessionScopeRef.current.generation !== scope.generation) return
      setSuggestions(res.suggestions || [])
    } catch {
      /* suggestions are best-effort */
    }
  }, [api, sessionId])

  useLayoutEffect(() => {
    void previewCredentialReviewRef.current?.discardSourceSession()
    void salesforceCredentialReviewRef.current?.discardSourceSession()
    if (feedbackTimerRef.current !== null) {
      window.clearTimeout(feedbackTimerRef.current)
      feedbackTimerRef.current = null
    }
    setSession(null)
    setLoadError('')
    setScriptName('')
    advancePreviewReviewGeneration()
    advanceSalesforceReviewGeneration()
    setPreview(null)
    setSalesforceDraft(null)
    setPreviewParameterizationApplying(false)
    setSalesforceParameterizationApplying(false)
    setPreviewPreparedSecrets(null)
    setSalesforcePreparedSecrets(null)
    previewPreparedScriptsRef.current = null
    previewSaveBindingsRef.current = []
    salesforcePreparedScriptsRef.current = null
    previewRequestKeyRef.current = ''
    previewParameterizationSelectionsRef.current = new Map()
    setSavedScript(null)
    setSuggestions([])
    setReplaySessionId(null)
    setLastVerifyReport(null)
    setStepSearch('')
    setStepThumbnails({})
    setStepThumbnailsLoading(false)
    setBusy(false)
    setFeedback('')
    setNavigateScheme(DEFAULT_HTTP_URL_SCHEME)
    setNavigateAddress('')
    setTransactionName('')
    setNoteText('')
    setSplitAtTransactions(false)
    setAlsoCreateHTTP(false)
    setParameterizeInputs(false)
    void refresh()
  }, [advancePreviewReviewGeneration, advanceSalesforceReviewGeneration, recorderRequestScope, refresh, sessionId])

  useEffect(() => () => {
    if (feedbackTimerRef.current !== null) window.clearTimeout(feedbackTimerRef.current)
    void previewCredentialReviewRef.current?.discardSourceSession()
    void salesforceCredentialReviewRef.current?.discardSourceSession()
  }, [])

  useEffect(() => {
    const now = Date.now()
    const nextExpiry = [
      Date.parse(preview?.import_draft_expires_at || ''),
      Date.parse(salesforceDraft?.import_draft_expires_at || ''),
    ]
      .filter(expiry => Number.isFinite(expiry) && expiry > now)
      .sort((left, right) => left - right)[0]
    if (nextExpiry === undefined) return
    const timeout = window.setTimeout(
      () => setImportDraftExpiryGeneration(current => current + 1),
      Math.min(nextExpiry - now + 10, 2_147_483_647),
    )
    return () => window.clearTimeout(timeout)
  }, [
    importDraftExpiryGeneration,
    preview?.import_draft_expires_at,
    salesforceDraft?.import_draft_expires_at,
  ])

  const live = !!session && LIVE_STATUSES.has(session.status)

  // The recorder broadcasts browser_recording_* WebSocket events; the panel
  // polls instead so the standalone popup needs no socket plumbing — the
  // 1.5s cadence keeps the live feed within the dashboard's 2s freshness bar.
  useEffect(() => {
    if (!live) return
    const interval = setInterval(refresh, 1500)
    return () => clearInterval(interval)
  }, [live, refresh])

  useEffect(() => {
    if (session && !scriptName) {
      setScriptName(session.name)
    }
    // Seed from the session only while the field is empty; later renames stay manual.
  }, [scriptName, session])

  useEffect(() => {
    if (standalone && session) {
      document.title = `Recording — ${session.name}`
    }
  }, [standalone, session])

  // Refresh auto-transaction suggestions as navigations accumulate.
  const navigationCount = session?.navigation_count || 0
  useEffect(() => {
    if (navigationCount > 0) {
      loadSuggestions()
    }
  }, [navigationCount, loadSuggestions])

  const handleStop = async () => {
    setBusy(true)
    try {
      const res = await api.stopBrowserRecording(sessionId)
      if (!isCurrentSession()) return
      acceptRecordingSession(res.session)
      if (res.session.status === 'stopping') {
        showFeedback('Stop requested — finalizing browser and HAR capture')
      } else {
        showFeedback(`Recording stopped — ${res.session.step_count} steps captured`)
      }
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    } finally {
      if (isCurrentSession()) setBusy(false)
    }
  }

  const handleForceStop = async () => {
    setBusy(true)
    try {
      const res = await api.forceStopBrowserRecording(sessionId)
      if (!isCurrentSession()) return
      acceptRecordingSession(res.session)
      showFeedback(`Recording force-finished — ${res.session.step_count} captured steps preserved`)
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    } finally {
      if (isCurrentSession()) setBusy(false)
    }
  }

  const handlePauseResume = async () => {
    if (!session) return
    setBusy(true)
    try {
      const res = session.status === 'paused'
        ? await api.resumeBrowserRecording(sessionId)
        : await api.pauseBrowserRecording(sessionId)
      if (!isCurrentSession()) return
      acceptRecordingSession(res.session)
      showFeedback(res.session.status === 'paused'
        ? 'Recording paused — interactions and think time are not captured'
        : 'Recording resumed')
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    } finally {
      if (isCurrentSession()) setBusy(false)
    }
  }

  const handleFocus = async () => {
    try {
      await api.focusBrowserRecording(sessionId)
      if (!isCurrentSession()) return
      showFeedback('Recording browser brought to the front')
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    }
  }

  const handleNavigateAddressChange = (value: string) => {
    const normalized = normalizeHttpURLInput(value, navigateScheme)
    setNavigateScheme(normalized.scheme)
    setNavigateAddress(normalized.address)
  }

  const handleNavigate = async () => {
    const url = composeHttpURL(navigateScheme, navigateAddress)
    if (!url) {
      showFeedback('Enter an http(s) URL to navigate to', 'error')
      return
    }
    setBusy(true)
    try {
      const res = await api.navigateBrowserRecording(sessionId, url)
      if (!isCurrentSession()) return
      acceptRecordingSession(res.session)
      setNavigateAddress('')
      showFeedback(`Recording browser navigated to ${url}`)
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    } finally {
      if (isCurrentSession()) setBusy(false)
    }
  }

  const handleStartTransaction = async () => {
    const name = transactionName.trim()
    if (!name) {
      showFeedback('Enter a transaction name', 'error')
      return
    }
    try {
      const res = await api.startBrowserRecordingTransaction(sessionId, name)
      if (!isCurrentSession()) return
      acceptRecordingSession(res.session)
      setTransactionName('')
      showFeedback(`Transaction "${name}" started`)
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    }
  }

  const handleEndTransaction = async (name: string) => {
    try {
      const res = await api.endBrowserRecordingTransaction(sessionId, name)
      if (!isCurrentSession()) return
      acceptRecordingSession(res.session)
      showFeedback(`Transaction "${res.marker.name || name}" ended`)
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    }
  }

  const handleAddNote = async () => {
    const text = noteText.trim()
    if (!text) {
      showFeedback('Enter a note', 'error')
      return
    }
    try {
      const res = await api.addBrowserRecordingNote(sessionId, text)
      if (!isCurrentSession()) return
      acceptRecordingSession(res.session)
      setNoteText('')
      showFeedback('Note added — it will attach to the next recorded action')
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    }
  }

  const convertRequest = () => ({
    name: scriptName || undefined,
    split_at_transactions: splitAtTransactions,
    also_create_http_script: alsoCreateHTTP,
    parameterize: parameterizeInputs,
  })

  const discardSourceSecretSession = async (
    sourceSecretSessionId: string | undefined,
    projectId: string | undefined,
  ) => {
    if (!sourceSecretSessionId || !projectId) return
    await api.discardImportSecretSession({
      project_id: projectId,
      source_secret_session_id: sourceSecretSessionId,
    })
  }

  const discardSupersededSourceSecretSession = async (
    previousSourceSecretSessionId: string | undefined,
    nextSourceSecretSessionId: string | undefined,
    projectId: string | undefined,
  ) => {
    if (
      !previousSourceSecretSessionId
      || previousSourceSecretSessionId === nextSourceSecretSessionId
    ) return
    try {
      await discardSourceSecretSession(previousSourceSecretSessionId, projectId)
    } catch (error) {
      // Keep the currently displayed draft usable if retiring its predecessor
      // failed, and best-effort retire the replacement that will not be shown.
      try {
        await discardSourceSecretSession(nextSourceSecretSessionId, projectId)
      } catch {
        // Preserve the original failure; it identifies the lifecycle operation
        // the user can retry by running the preview again.
      }
      throw error
    }
  }

  const confirmReplaySaveRisk = () => {
    if (!stopped) return true
    if (!lastVerifyReport) {
      return true
    }
    if (!lastVerifyReport.success) {
      return window.confirm('The latest replay verification found issues. Save this script anyway?') !== false
    }
    return true
  }

  const handlePreview = async () => {
    const previousSourceSecretSessionId = preview?.source_secret_session_id
    const projectId = session?.project_id
    setPreviewParameterizationApplying(false)
    advancePreviewReviewGeneration()
    setBusy(true)
    try {
      const result = await api.previewBrowserRecordingConversion(sessionId, convertRequest())
      if (!isCurrentSession()) {
        await discardSourceSecretSession(result.source_secret_session_id, projectId)
        return
      }
      if (previousSourceSecretSessionId !== result.source_secret_session_id) {
        await discardSupersededSourceSecretSession(
          previousSourceSecretSessionId,
          result.source_secret_session_id,
          projectId,
        )
        if (!isCurrentSession()) {
          await discardSourceSecretSession(result.source_secret_session_id, projectId)
          return
        }
      }
      previewPreparedScriptsRef.current = null
      previewSaveBindingsRef.current = []
      previewParameterizationSelectionsRef.current = new Map()
      setPreviewPreparedSecrets(null)
      setPreview(result)
      previewRequestKeyRef.current = JSON.stringify(convertRequest())
      const suggestions = result.parameterization?.summary?.total ?? 0
      const scripts = importResultScripts(result)
      const actionCount = scripts.reduce((total, script) => total + (script.actions?.length || 0), 0)
      showFeedback(`Preview generated with ${actionCount} actions and ${suggestions} parameterization suggestions`)
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    } finally {
      if (isCurrentSession()) setBusy(false)
    }
  }

  async function applyBindingsToResult<T extends RecorderImportResult | SalesforceImportResult>(
    result: T,
    bindings: SecretBindingSelection[],
    publish: (next: T) => void,
    requestIsCurrent: () => boolean,
  ): Promise<T> {
    let resolvedResult = result
    let scripts = importResultScripts(result)
    for (let scriptIndex = 0; scriptIndex < scripts.length; scriptIndex += 1) {
      const script = scripts[scriptIndex]
      const requirements = secretBindingRequirementsForScript(
        resolvedResult.secret_binding_requirements,
        scriptIndex,
        script,
      )
      const requirementIDs = new Set(requirements.map(requirement => requirement.id))
      const scriptBindings = bindings.filter(binding => requirementIDs.has(binding.requirement_id))
      if (scriptBindings.length === 0) continue
      try {
        if (!requestIsCurrent()) throw new Error('The recording draft changed while credentials were being prepared. Review the current draft and try again.')
        const resolved = await api.applySecretBindings({
          script,
          script_index: scriptIndex,
          bindings: scriptBindings,
        })
        if (!requestIsCurrent()) throw new Error('The recording draft changed while credentials were being prepared. Review the current draft and try again.')
        scripts = scripts.map((entry, index) => index === scriptIndex ? resolved.script : entry)
        resolvedResult = {
          ...resolvedResult,
          script: scripts[0],
          scripts,
          ...(scripts.length === 1 && resolved.import_draft_expires_at
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
          publish(resolvedResult)
        }
        throw error
      }
    }
    if (!requestIsCurrent()) throw new Error('The recording draft changed while credentials were being prepared. Review the current draft and try again.')
    publish(resolvedResult)
    return resolvedResult
  }

  const handleApplyPreviewSecretBindings = async (bindings: SecretBindingSelection[]) => {
    if (!preview) return
    if (importDraftHasExpired(preview)) throw new Error(IMPORT_DRAFT_EXPIRED_MESSAGE)
    const sourceGeneration = previewReviewGenerationRef.current
    const sourceSession = { ...sessionScopeRef.current }
    const requestIsCurrent = () => previewReviewGenerationRef.current === sourceGeneration
      && sessionScopeRef.current.sessionId === sourceSession.sessionId
      && sessionScopeRef.current.generation === sourceSession.generation
      && !importDraftHasExpired(preview)
    const resolved = await applyBindingsToResult(preview, bindings, next => {
      if (isCurrentSession()) setPreview(next)
    }, requestIsCurrent)
    if (!isCurrentSession()) return
    if (importDraftHasExpired(resolved)) throw new Error(IMPORT_DRAFT_EXPIRED_MESSAGE)
    const requirementsByID = new Map(
      (preview.secret_binding_requirements || []).map(requirement => [requirement.id, requirement]),
    )
    previewSaveBindingsRef.current = bindings.map(binding => ({
      requirement_id: binding.requirement_id,
      secret_name: binding.secret_name,
      script_index: binding.script_index
        ?? requirementsByID.get(binding.requirement_id)?.script_index
        ?? 0,
    }))
    invalidateVerification()
    previewPreparedScriptsRef.current = importResultScripts(resolved)
  }

  const handlePreviewProjectSecretsConfigured = useCallback((names: readonly string[]) => {
    if (names.length > 0) setPreviewConfiguredCredentialReviewIdentity(previewCredentialReviewIdentity)
  }, [previewCredentialReviewIdentity])

  const handleApplyPreviewParameterization = (scriptIndex: number) => (
    parameterizedScript: ScriptDefinition,
    _diffs: unknown[],
    selectedIds: string[],
    importDraftExpiresAt?: string,
  ) => {
    if (!preview) return
    if (importDraftHasExpired(preview)) {
      showFeedback(IMPORT_DRAFT_EXPIRED_MESSAGE, 'error')
      return
    }
    const scripts = importResultScripts(preview)
    if (!scripts[scriptIndex]) return
    advancePreviewReviewGeneration()
    previewPreparedScriptsRef.current = null
    const selected = previewParameterizationSelectionsRef.current.get(scriptIndex) ?? new Set<string>()
    for (const id of selectedIds) selected.add(id)
    previewParameterizationSelectionsRef.current.set(scriptIndex, selected)
    const nextScripts = scripts.map((entry, index) => index === scriptIndex ? parameterizedScript : entry)
    setPreview({
      ...preview,
      script: nextScripts[0],
      scripts: nextScripts,
      ...(nextScripts.length === 1 && importDraftExpiresAt
        ? { import_draft_expires_at: importDraftExpiresAt }
        : {}),
    })
  }

  const preparePreviewScripts = async (result: RecorderImportResult): Promise<ScriptDefinition[] | null> => {
    if (importDraftHasExpired(result)) throw new Error(IMPORT_DRAFT_EXPIRED_MESSAGE)
    if ((result.secret_binding_requirements?.length || 0) === 0) return importResultScripts(result)
    const selectedBindingCount = previewPreparedSecrets?.selectedBindingCount
      ?? result.secret_binding_requirements?.filter(requirement => (
        requirement.destination_value_preserved !== true
      )).length
      ?? 0
    const sourceGeneration = previewReviewGenerationRef.current
    const sourceSession = { ...sessionScopeRef.current }
    previewPreparedScriptsRef.current = null
    // commit() intentionally does not invoke onApply when every occurrence is
    // preserved. Clear an earlier explicit migration before preparing again so
    // switching back to Preserve imported value cannot leave a stale save-time
    // override behind.
    previewSaveBindingsRef.current = []
    const committed = await previewCredentialReviewRef.current?.commit()
    if (importDraftHasExpired(result)) throw new Error(IMPORT_DRAFT_EXPIRED_MESSAGE)
    if (
      committed
      && selectedBindingCount === 0
      && previewReviewGenerationRef.current === sourceGeneration
      && sessionScopeRef.current.sessionId === sourceSession.sessionId
      && sessionScopeRef.current.generation === sourceSession.generation
    ) {
      return importResultScripts(result)
    }
    return committed
      && previewReviewGenerationRef.current === sourceGeneration
      && sessionScopeRef.current.sessionId === sourceSession.sessionId
      && sessionScopeRef.current.generation === sourceSession.generation
      ? previewPreparedScriptsRef.current
      : null
  }

  // Bridge-mode and Salesforce drafts cannot use the local recording-session
  // transaction, but they still use one remote create-only batch with a stable
  // receipt. This prevents duplicate/partial scripts when a 201 is lost.
  const saveScriptsAtomically = async (
    scripts: ScriptDefinition[],
    profile: 'browser_recorder' | 'salesforce',
    source: RecorderImportResult | SalesforceImportResult,
    assertCanPersist: () => void = () => {},
  ): Promise<StoredScript[]> => {
    assertCanPersist()
    const destinationFolderID = scripts[0]?.folder_id ?? folderId ?? null
    if (scripts.some(script => (script.folder_id ?? folderId ?? null) !== destinationFolderID)) {
      throw new Error('Atomic save requires every generated script to use the same destination folder.')
    }
    const retained = genericBatchSaveRef.current
    const operationId = retained
      && retained.source === source
      && retained.projectId === session?.project_id
      && retained.folderId === destinationFolderID
      && retained.profile === profile
      ? retained.operationId
      : `browser-import-${createReplaySessionId()}`
    genericBatchSaveRef.current = {
      source,
      projectId: session?.project_id,
      folderId: destinationFolderID,
      profile,
      operationId,
    }
    const response = await api.commitGenericImportBatch({
      scripts,
      folder_id: destinationFolderID,
      operation_id: operationId,
      source_profile: profile,
    })
    if (response.count !== scripts.length || response.scripts.length !== scripts.length) {
      throw new Error('The atomic save did not return every script. Retry the same save to reconcile its result.')
    }
    genericBatchSaveRef.current = null
    return response.scripts.map((stored, index) => withFallbackPersistedScript(stored, scripts[index]))
  }

  // Headed-browser drafts must be persisted through the server-side conversion
  // endpoint: createScript rejects a browser-recording session id outright
  // ("headed-browser drafts must be saved through the server-side browser
  // conversion endpoint") and the server transaction keeps split/dual-script
  // saves atomic. Credential choices are sent as opaque requirement bindings
  // and applied inside that transaction. Parameterization choices cross this
  // boundary only as suggestion IDs and are re-derived by the server.
  const saveThroughServerConversion = async (reviewedScripts: ScriptDefinition[]): Promise<StoredScript[]> => {
    const parameterizationSelections = [...previewParameterizationSelectionsRef.current.entries()]
      .sort(([left], [right]) => left - right)
      .map(([script_index, selected]) => ({
        script_index,
        selected_ids: [...selected].sort(),
      }))
      .filter(selection => selection.selected_ids.length > 0)
    const converted = await api.convertBrowserRecording(sessionId, {
      ...convertRequest(),
      folder_id: folderId ?? null,
      ...(previewSaveBindingsRef.current.length > 0
        ? { secret_bindings: previewSaveBindingsRef.current.map(binding => ({ ...binding })) }
        : {}),
      ...(parameterizationSelections.length > 0
        ? { parameterization_selections: parameterizationSelections }
        : {}),
      expected_script_count: reviewedScripts.length,
    })
    const stored = converted.scripts?.length
      ? converted.scripts
      : converted.script ? [converted.script] : []
    if (stored.length !== reviewedScripts.length) {
      throw new Error(
        `The server returned ${stored.length} persisted script(s), but ${reviewedScripts.length} reviewed script(s) were expected. Refresh the Script Library before retrying.`,
      )
    }
    return stored.map((entry, index) => withFallbackPersistedScript(entry, reviewedScripts[index]))
  }

  const handleSave = async () => {
    if (!confirmReplaySaveRisk()) return
    const requestKey = JSON.stringify(convertRequest())
    const hadCurrentPreview = preview !== null && previewRequestKeyRef.current === requestKey
    if (hadCurrentPreview && importDraftHasExpired(preview)) {
      showFeedback(IMPORT_DRAFT_EXPIRED_MESSAGE, 'error')
      return
    }
    setBusy(true)
    try {
      let result = hadCurrentPreview ? preview : null
      if (!result) {
        const previousSourceSecretSessionId = preview?.source_secret_session_id
        const projectId = session?.project_id
        advancePreviewReviewGeneration()
        result = await api.previewBrowserRecordingConversion(sessionId, convertRequest())
        if (!isCurrentSession()) {
          await discardSourceSecretSession(result.source_secret_session_id, projectId)
          return
        }
        if (previousSourceSecretSessionId !== result.source_secret_session_id) {
          await discardSupersededSourceSecretSession(
            previousSourceSecretSessionId,
            result.source_secret_session_id,
            projectId,
          )
          if (!isCurrentSession()) {
            await discardSourceSecretSession(result.source_secret_session_id, projectId)
            return
          }
        }
        previewPreparedScriptsRef.current = null
        previewSaveBindingsRef.current = []
        previewParameterizationSelectionsRef.current = new Map()
        setPreviewPreparedSecrets(null)
        setPreview(result)
        previewRequestKeyRef.current = requestKey
      }
      if (importDraftHasExpired(result)) throw new Error(IMPORT_DRAFT_EXPIRED_MESSAGE)
      if (!hadCurrentPreview && (result.secret_binding_requirements?.length || 0) > 0) {
        showFeedback('Credentials detected. Review the optional values below, then save with the prefilled references.')
        return
      }
      const scripts = await preparePreviewScripts(result)
      if (!scripts) return
      if (!isCurrentSession()) return
      if (importDraftHasExpired(result)) throw new Error(IMPORT_DRAFT_EXPIRED_MESSAGE)
      if (scripts.length === 0) throw new Error('Conversion produced no scripts to save')
      // Bridge recordings live on the local recording agent; the remote
      // controller cannot run the conversion for them, so their reviewed
      // scripts are created directly (without a source recording session id).
      let persisted: StoredScript[]
      if (api.browserRecorderBridge) {
        // The preview token belongs to the local recording agent. Transfer the
        // reviewed exact draft to a remote-owned one-shot attestation before
        // using the remote script-library API; otherwise the controller cannot
        // distinguish the imported credential from a forged client marker.
        const remoteDraft = await api.attestBridgedBrowserRecordingImport(
          scripts,
          session?.project_id,
        )
        const remoteScripts = importResultScripts(remoteDraft)
        if (remoteScripts.length !== scripts.length) {
          throw new Error('The remote controller did not attest every reviewed browser recording script')
        }
        if (importDraftHasExpired(remoteDraft)) throw new Error(IMPORT_DRAFT_EXPIRED_MESSAGE)
        persisted = await saveScriptsAtomically(remoteScripts, 'browser_recorder', result, () => {
          if (!isCurrentSession()) throw new Error('The recording session changed while scripts were being saved.')
          if (importDraftHasExpired(remoteDraft)) throw new Error(IMPORT_DRAFT_EXPIRED_MESSAGE)
        })
      } else {
        persisted = await saveThroughServerConversion(scripts)
      }
      void discardSourceSecretSession(
        result.source_secret_session_id,
        session?.project_id,
      ).catch(() => {
        // The durable save succeeded. Unmount/dismiss cleanup will retry any
        // transient protected-session retirement failure.
      })
      if (!isCurrentSession()) {
        // Persistence already succeeded. Publish the durable result to the
        // captured owner callback so a late panel/session transition cannot
        // leave newly created scripts untracked.
        onImported?.({ scripts: persisted, stayInImportReview: true })
        return
      }
      setSavedScript(persisted[0])
      onImported?.({ scripts: persisted })
      const libraryLabel = api.browserRecorderBridge ? 'remote script library' : 'script library'
      showFeedback(persisted.length === 1
        ? `Script "${persisted[0].name}" saved to the ${libraryLabel}`
        : `Saved ${persisted.length} scripts to the ${libraryLabel}`)
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    } finally {
      if (isCurrentSession()) setBusy(false)
    }
  }

  const handleSection = async (section: string) => {
    try {
      const res = await api.setBrowserRecordingSection(sessionId, section)
      if (!isCurrentSession()) return
      acceptRecordingSession(res.session)
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    }
  }

  const handleTogglePick = async () => {
    if (!session) return
    try {
      const res = await api.setBrowserRecordingPickMode(sessionId, !session.picking)
      if (!isCurrentSession()) return
      acceptRecordingSession(res.session)
      showFeedback(res.session.picking
        ? 'Pick mode on — click an element in the recording browser to add a verification'
        : 'Pick mode off')
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    }
  }

  const handleDeleteLastStep = async () => {
    try {
      const res = await api.deleteLastBrowserRecordingStep(sessionId)
      if (!isCurrentSession()) return
      acceptRecordingSession(res.session)
      showFeedback('Last step removed')
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    }
  }

  const handleToggleStepExcluded = async (seq: number, excluded: boolean) => {
    try {
      const res = await api.updateBrowserRecordingStep(sessionId, seq, { excluded })
      if (!isCurrentSession()) return
      acceptRecordingSession(res.session)
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    }
  }

  const handleMarkStep = async (seq: number, kind: 'secret' | 'param') => {
    try {
      const res = await api.updateBrowserRecordingStep(sessionId, seq, kind === 'secret' ? { mark_secret: true } : { mark_param: true })
      if (!isCurrentSession()) return
      acceptRecordingSession(res.session)
      showFeedback(`Step #${seq} marked as ${kind === 'secret' ? 'a secret' : 'a data parameter'}`)
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    }
  }

  const handleAcceptSuggestion = async (suggestion: BrowserRecorderTransactionSuggestion) => {
    try {
      const res = await api.startBrowserRecordingTransaction(sessionId, suggestion.name, suggestion.at_step_seq)
      if (!isCurrentSession()) return
      acceptRecordingSession(res.session)
      setSuggestions(prev => prev.filter(s => s.name !== suggestion.name || s.at_step_seq !== suggestion.at_step_seq))
      showFeedback(`Transaction "${suggestion.name}" started`)
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    }
  }

  const startVerificationReplay = async (successMessage: string, scriptIndex = 0) => {
    if (verificationStartScopeRef.current === renderedSessionScope) return false
    const verificationScope = renderedSessionScope
    verificationStartScopeRef.current = verificationScope
    const revision = verificationRevisionRef.current
    const clientReplaySessionId = createReplaySessionId()
    verificationGenerationRef.current = verificationRevisionRef.current
    verificationOutputRef.current = scriptIndex
    setLastVerifyReport(null)
    setReplaySessionId(clientReplaySessionId || null)
    try {
      const res = await api.verifyBrowserRecording(sessionId, clientReplaySessionId || undefined, {
        ...convertRequest(),
        secret_bindings: previewSaveBindingsRef.current,
        parameterization_selections: [...previewParameterizationSelectionsRef.current.entries()].map(([script_index, selected]) => ({ script_index, selected_ids: [...selected].sort() })),
        expected_script_count: preview ? importResultScripts(preview).length : undefined,
      }, scriptIndex)
      if (!isCurrentSession()) return false
      rememberVerification(res.replay_session_id)
      if (verificationRevisionRef.current !== revision) return false
      setReplaySessionId(res.replay_session_id)
      showFeedback(successMessage)
      return true
    } catch (e: any) {
      if (!isCurrentSession()) return false
      if (clientReplaySessionId && !isDefiniteReplayStartRejection(e)) {
        rememberVerification(clientReplaySessionId)
        if (verificationRevisionRef.current !== revision) return false
        showFeedback('The replay start response was interrupted. Confirming the accepted replay session…')
        return true
      }
      setReplaySessionId(null)
      showFeedback(e.message || 'Failed to start replay verification', 'error')
      return false
    } finally {
      if (verificationStartScopeRef.current === verificationScope) {
        verificationStartScopeRef.current = null
      }
    }
  }

  const handleVerify = async () => {
    if (verificationStartScopeRef.current === renderedSessionScope) return
    setBusy(true)
    try {
      await startVerificationReplay('Verifying — replaying the recorded script')
    } finally {
      if (isCurrentSession()) setBusy(false)
    }
  }

  const handleReplayComplete = (sourceReplaySessionId: string, report: BrowserReplayReport | null) => {
    if (!isCurrentSession()) return
    if (replaySessionIdRef.current !== sourceReplaySessionId) return
    if (report?.replay_session_id && report.replay_session_id !== sourceReplaySessionId) return
    if (verificationGenerationRef.current !== verificationRevisionRef.current) return
    setLastVerifyReport(report)
    if (report) setOutputReports(previous => ({ ...previous, [verificationOutputRef.current]: report }))
    if (!report) {
      setReplaySessionId(null)
      showFeedback('Replay verification stopped before a final report was available', 'error')
      return
    }
    if (report.success) {
      showFeedback('Replay verification passed')
    } else {
      showFeedback('Replay verification found issues to review', 'error')
    }
  }

  const handleApplyReplaySuggestion = async (suggestion: ReplayRepairSuggestion) => {
    if (suggestion.draft_patch_type !== 'promote_selector') {
      showFeedback(`Replay suggestion type "${suggestion.draft_patch_type || 'unknown'}" is not supported for recorded-session repair yet.`, 'error')
      return
    }
    const fields = suggestion.fields || {}
    const selector = typeof fields.selector === 'string' ? fields.selector.trim() : ''
    const seqRaw = fields.recorder_step_seq
    const seq = typeof seqRaw === 'number' ? seqRaw : Number(seqRaw)
    if (!selector || !Number.isFinite(seq)) {
      showFeedback('This replay suggestion is missing the original recorder step or selector', 'error')
      return
    }
    setBusy(true)
    try {
      const patched = await api.updateBrowserRecordingStep(sessionId, seq, { promote_selector: selector })
      if (!isCurrentSession()) return
      acceptRecordingSession(patched.session)
      invalidateVerification()
      previewRequestKeyRef.current = ''
      await startVerificationReplay(`Selector promoted for step #${seq}; verifying again`, verificationOutputRef.current)
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message || 'Failed to apply replay suggestion', 'error')
    } finally {
      if (isCurrentSession()) setBusy(false)
    }
  }

  const editableScript = importResultScripts(preview)[0] || savedScript?.script
  const previewParameterizationPanels = useMemo(() => {
    const parameterization = preview?.parameterization
    if (!parameterization?.suggestions?.length) return []
    return importResultScripts(preview).flatMap((script, scriptIndex) => {
      const suggestions = parameterization.suggestions.filter(suggestion =>
        suggestion.script_name
          ? suggestion.script_name === script.name
          : scriptIndex === 0,
      )
      return suggestions.length > 0
        ? [{
            script,
            scriptIndex,
            parameterization: { ...parameterization, suggestions },
          }]
        : []
    })
  }, [preview])

  const handleOpenEditor = async () => {
    if (!preview) return
    if (importDraftHasExpired(preview)) {
      showFeedback(IMPORT_DRAFT_EXPIRED_MESSAGE, 'error')
      return
    }
    const script = importResultScripts(preview)[0]
    if (credentialPreservingImportDraftRequiresSave(
      preview,
      script,
      preview.secret_binding_requirements,
    )) {
      if (!onImported) {
        showFeedback('Save this credential-preserving import before opening it in the Visual Editor.', 'error')
        return
      }
      await handleSave()
      return
    }
    setBusy(true)
    try {
      const scripts = await preparePreviewScripts(preview)
      if (isCurrentSession() && scripts?.[0]) onEdit?.(scripts[0])
    } finally {
      if (isCurrentSession()) setBusy(false)
    }
  }

  const handleDownloadHAR = async () => {
    setFeedback('')
    try {
      const safeName = safeRecordingFileName(session?.name)
      const filename = `${safeName || 'browser-recording'}.har`
      if (!api.browserRecorderBridge) {
        const nativeResourcePath = `/api/v1/browser-recorder/sessions/${encodeURIComponent(sessionId)}/export?format=har`
        const nativeOutcome = await saveApiResourceViaBridge(nativeResourcePath, filename)
        if (nativeOutcome === 'saved') {
          if (isCurrentSession()) showFeedback('Side-car HAR downloaded')
          return
        }
        if (nativeOutcome === 'cancelled') return
        if (!isDesktopSurface() && hasDesktopApiCapability()) {
          await startDesktopBrowserDownload(nativeResourcePath, filename)
          if (isCurrentSession()) showFeedback('Side-car HAR download request opened; any server error will appear in the browser.')
          return
        }
      }
      const res = await api.exportBrowserRecording(sessionId, 'har')
      if (!isCurrentSession()) return
      if (!res.ok) {
        const payload = await res.json().catch(() => ({ error: 'Failed to export HAR' }))
        throw new Error(payload.error || 'Failed to export HAR')
      }
      const blob = await res.blob()
      if (!isCurrentSession()) return
      const outcome = await downloadBlob(blob, filename)
      if (outcome === 'cancelled' || !isCurrentSession()) return
      showFeedback('Side-car HAR downloaded')
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    }
  }

  const handleReviewSalesforceHTTP = async () => {
    if (!session) return
    const previousSourceSecretSessionId = salesforceDraft?.source_secret_session_id
    const projectId = session.project_id
    setSalesforceParameterizationApplying(false)
    advanceSalesforceReviewGeneration()
    setBusy(true)
    try {
      const res = await api.exportBrowserRecording(sessionId, 'har')
      if (!isCurrentSession()) return
      if (!res.ok) {
        const payload = await res.json().catch(() => ({ error: 'Failed to export HAR' }))
        throw new Error(payload.error || 'Failed to export HAR')
      }
      const blob = await res.blob()
      if (!isCurrentSession()) return
      const file = new File([blob], `${safeRecordingFileName(session.name)}.har`, { type: blob.type || 'application/json' })
      const result = await api.importSalesforce(file, {
        name: scriptName || `${session.name} Salesforce HTTP`,
        project_id: session.project_id,
      })
      if (!isCurrentSession()) {
        await discardSourceSecretSession(result.source_secret_session_id, projectId)
        return
      }
      if (previousSourceSecretSessionId !== result.source_secret_session_id) {
        await discardSupersededSourceSecretSession(
          previousSourceSecretSessionId,
          result.source_secret_session_id,
          projectId,
        )
        if (!isCurrentSession()) {
          await discardSourceSecretSession(result.source_secret_session_id, projectId)
          return
        }
      }
      salesforcePreparedScriptsRef.current = null
      setSalesforcePreparedSecrets(null)
      setSalesforceDraft(result)
      const script = result.script || result.scripts?.[0]
      showFeedback(`Salesforce HTTP draft ready with ${script?.actions?.length || 0} actions`)
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    } finally {
      if (isCurrentSession()) setBusy(false)
    }
  }

  const handleApplySalesforceSecretBindings = async (bindings: SecretBindingSelection[]) => {
    if (!salesforceDraft) return
    if (importDraftHasExpired(salesforceDraft)) throw new Error(IMPORT_DRAFT_EXPIRED_MESSAGE)
    const sourceGeneration = salesforceReviewGenerationRef.current
    const sourceSession = { ...sessionScopeRef.current }
    const requestIsCurrent = () => salesforceReviewGenerationRef.current === sourceGeneration
      && sessionScopeRef.current.sessionId === sourceSession.sessionId
      && sessionScopeRef.current.generation === sourceSession.generation
      && !importDraftHasExpired(salesforceDraft)
    const resolved = await applyBindingsToResult(salesforceDraft, bindings, next => {
      if (isCurrentSession()) setSalesforceDraft(next)
    }, requestIsCurrent)
    if (!isCurrentSession()) return
    if (importDraftHasExpired(resolved)) throw new Error(IMPORT_DRAFT_EXPIRED_MESSAGE)
    salesforcePreparedScriptsRef.current = importResultScripts(resolved)
  }

  const handleSalesforceProjectSecretsConfigured = useCallback((names: readonly string[]) => {
    if (names.length > 0) setSalesforceConfiguredCredentialReviewIdentity(salesforceCredentialReviewIdentity)
  }, [salesforceCredentialReviewIdentity])

  const handleApplySalesforceParameterization = (
    parameterizedScript: ScriptDefinition,
    _diffs: unknown[],
    _selectedIds: string[],
    importDraftExpiresAt?: string,
  ) => {
    if (!salesforceDraft) return
    if (importDraftHasExpired(salesforceDraft)) {
      showFeedback(IMPORT_DRAFT_EXPIRED_MESSAGE, 'error')
      return
    }
    advanceSalesforceReviewGeneration()
    salesforcePreparedScriptsRef.current = null
    setSalesforceDraft({
      ...salesforceDraft,
      script: parameterizedScript,
      scripts: salesforceDraft.scripts?.length
        ? salesforceDraft.scripts.map((entry, index) => index === 0 ? parameterizedScript : entry)
        : [parameterizedScript],
      ...((salesforceDraft.scripts?.length ?? 1) <= 1 && importDraftExpiresAt
        ? { import_draft_expires_at: importDraftExpiresAt }
        : {}),
    })
  }

  const prepareSalesforceScripts = async (): Promise<ScriptDefinition[] | null> => {
    if (!salesforceDraft) return null
    if (importDraftHasExpired(salesforceDraft)) throw new Error(IMPORT_DRAFT_EXPIRED_MESSAGE)
    if ((salesforceDraft.secret_binding_requirements?.length || 0) === 0) return importResultScripts(salesforceDraft)
    const selectedBindingCount = salesforcePreparedSecrets?.selectedBindingCount
      ?? salesforceDraft.secret_binding_requirements?.filter(requirement => (
        requirement.destination_value_preserved !== true
      )).length
      ?? 0
    const sourceGeneration = salesforceReviewGenerationRef.current
    const sourceSession = { ...sessionScopeRef.current }
    salesforcePreparedScriptsRef.current = null
    const committed = await salesforceCredentialReviewRef.current?.commit()
    if (importDraftHasExpired(salesforceDraft)) throw new Error(IMPORT_DRAFT_EXPIRED_MESSAGE)
    if (
      committed
      && selectedBindingCount === 0
      && salesforceReviewGenerationRef.current === sourceGeneration
      && sessionScopeRef.current.sessionId === sourceSession.sessionId
      && sessionScopeRef.current.generation === sourceSession.generation
    ) {
      return importResultScripts(salesforceDraft)
    }
    return committed
      && salesforceReviewGenerationRef.current === sourceGeneration
      && sessionScopeRef.current.sessionId === sourceSession.sessionId
      && sessionScopeRef.current.generation === sourceSession.generation
      ? salesforcePreparedScriptsRef.current
      : null
  }

  const handleSaveSalesforceDraft = async () => {
    if (!salesforceDraft) return
    if (importDraftHasExpired(salesforceDraft)) {
      showFeedback(IMPORT_DRAFT_EXPIRED_MESSAGE, 'error')
      return
    }
    setBusy(true)
    try {
      const scripts = await prepareSalesforceScripts()
      if (!scripts?.length) return
      if (!isCurrentSession()) return
      if (importDraftHasExpired(salesforceDraft)) throw new Error(IMPORT_DRAFT_EXPIRED_MESSAGE)
      const persisted = await saveScriptsAtomically(scripts, 'salesforce', salesforceDraft, () => {
        if (!isCurrentSession()) throw new Error('The recording session changed while scripts were being saved.')
        if (importDraftHasExpired(salesforceDraft)) throw new Error(IMPORT_DRAFT_EXPIRED_MESSAGE)
      })
      void discardSourceSecretSession(
        salesforceDraft?.source_secret_session_id,
        session?.project_id,
      ).catch(() => {
        // The durable save succeeded. Unmount/dismiss cleanup will retry.
      })
      if (!isCurrentSession()) {
        onImported?.({ scripts: persisted, stayInImportReview: true })
        return
      }
      setSavedScript(persisted[0])
      onImported?.({ scripts: persisted })
      showFeedback(`Saved ${persisted.length === 1 ? `Salesforce HTTP script "${persisted[0].name}"` : `${persisted.length} Salesforce HTTP scripts`} to the script library`)
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    } finally {
      if (isCurrentSession()) setBusy(false)
    }
  }

  const handleOpenSalesforceDraft = async () => {
    if (importDraftHasExpired(salesforceDraft)) {
      showFeedback(IMPORT_DRAFT_EXPIRED_MESSAGE, 'error')
      return
    }
    const script = salesforceDraft?.script || salesforceDraft?.scripts?.[0]
    if (credentialPreservingImportDraftRequiresSave(
      salesforceDraft,
      script,
      salesforceDraft?.secret_binding_requirements,
    )) {
      if (!onImported) {
        showFeedback('Save this credential-preserving import before opening it in the Visual Editor.', 'error')
        return
      }
      await handleSaveSalesforceDraft()
      return
    }
    setBusy(true)
    try {
      const scripts = await prepareSalesforceScripts()
      if (isCurrentSession() && scripts?.[0]) onEdit?.(scripts[0])
    } finally {
      if (isCurrentSession()) setBusy(false)
    }
  }

  if (loadError && !session) {
    return (
      <div className={`brp-panel ${standalone ? 'brp-standalone' : ''}`}>
        <div className="rec-feedback error">{loadError}</div>
      </div>
    )
  }

  if (!session) {
    return (
      <div className={`brp-panel ${standalone ? 'brp-standalone' : ''}`}>
        <div className="brp-loading">Loading recording session…</div>
      </div>
    )
  }

  const stopped = session.status === 'stopped'
  const controllable = CONTROLLABLE_STATUSES.has(session.status)
  const openTransactions = session.open_transactions || []
  const showSalesforceHandoff = session.har_available && hasSalesforceSignal(session, preview)
  const salesforceScript = salesforceDraft?.script || salesforceDraft?.scripts?.[0] || null
  const salesforceReview = salesforceDraft?.summary?.salesforce_review
  const previewSecretRequirements = preview?.secret_binding_requirements || []
  const previewDeferredSecretCount = previewSecretRequirements.length === 0
    ? 0
    : previewPreparedSecrets?.configureLaterCount ??
      new Set(previewSecretRequirements
        .filter(requirement => requirement.destination_value_preserved !== true)
        .map(requirement => requirement.suggested_name)).size
  const allOutputsVerified = !!preview && importResultScripts(preview).length > 0 && importResultScripts(preview).every((_, index) => outputReports[index]?.success && !outputReports[index]?.stopped)
  const previewSaveLabel = previewDeferredSecretCount > 0
    ? `Save with ${previewDeferredSecretCount} ${previewDeferredSecretCount === 1 ? 'secret' : 'secrets'} to configure`
    : allOutputsVerified ? 'Save verified draft' : 'Save unverified draft'
  const salesforceSecretRequirements = salesforceDraft?.secret_binding_requirements || []
  const previewRequiresCredentialPreservingSave = credentialPreservingImportDraftRequiresSave(
    preview,
    editableScript,
    previewSecretRequirements,
  )
  const previewImportDraftExpiry = importDraftExpiryStatus(preview?.import_draft_expires_at)
  const salesforceRequiresCredentialPreservingSave = credentialPreservingImportDraftRequiresSave(
    salesforceDraft,
    salesforceScript,
    salesforceSecretRequirements,
  )
  const salesforceImportDraftExpiry = importDraftExpiryStatus(salesforceDraft?.import_draft_expires_at)
  const salesforceDeferredSecretCount = salesforceSecretRequirements.length === 0
    ? 0
    : salesforcePreparedSecrets?.configureLaterCount ??
      new Set(salesforceSecretRequirements
        .filter(requirement => requirement.destination_value_preserved !== true)
        .map(requirement => requirement.suggested_name)).size
  const salesforceSaveLabel = salesforceDeferredSecretCount > 0
    ? `Save with ${salesforceDeferredSecretCount} ${salesforceDeferredSecretCount === 1 ? 'secret' : 'secrets'} to configure`
    : 'Save Salesforce HTTP Script'

  return (
    <div className={`brp-panel ${standalone ? 'brp-standalone' : ''}`} data-testid="browser-recorder-panel">
      <div className="brp-header">
        <div className="brp-title">
          {live && <span className="rec-live-dot" />}
          <span className="brp-name">{session.name}</span>
          <span className={`brp-status brp-status-${session.status}`}>{statusLabel(session)}</span>
        </div>
        {onDismiss && !live && (
          <button type="button" className="rec-inline-action" disabled={busy} onClick={() => {
            if (busy) return
            panelActiveRef.current = false
            previewReviewGenerationRef.current += 1
            salesforceReviewGenerationRef.current += 1
            void previewCredentialReviewRef.current?.discardSourceSession()
            void salesforceCredentialReviewRef.current?.discardSourceSession()
            onDismiss()
          }}>Dismiss</button>
        )}
      </div>

      {feedback && <div className={`rec-feedback ${feedbackType}`}>{feedback}</div>}
      {loadError && <div className="rec-feedback error" role="alert">{loadError}</div>}
      {session.error && <div className="rec-feedback error">{session.error}</div>}

      {session.current_url && (
        <div className="brp-url" title={session.current_url}>
          <span className="brp-url-label">Current URL</span>
          <code>{session.current_url}</code>
        </div>
      )}

      <div className="rec-active-stats brp-stats">
        <div className="rec-stat">
          <span className="rec-stat-value">{session.step_count}</span>
          <span className="rec-stat-label">Steps</span>
        </div>
        <div className="rec-stat">
          <span className="rec-stat-value">{session.navigation_count}</span>
          <span className="rec-stat-label">Pages</span>
        </div>
        <div className="rec-stat">
          <span className="rec-stat-value">{session.network_request_count}</span>
          <span className="rec-stat-label">Requests</span>
        </div>
        <div className="rec-stat">
          <span className="rec-stat-value">{session.websocket_count}</span>
          <span className="rec-stat-label">WebSockets</span>
        </div>
        <div className="rec-stat">
          <span className="rec-stat-value">{session.har_available ? 'Ready' : session.capture_har ? 'Capturing' : 'Off'}</span>
          <span className="rec-stat-label">HAR</span>
        </div>
      </div>

      <div className="brp-actions">
        {controllable && (
          <button className="btn btn-danger" onClick={handleStop} disabled={busy}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><rect x="4" y="4" width="16" height="16" rx="2" /></svg>
            Stop Recording
          </button>
        )}
        {session.status === 'stopping' && (
          <button
            className="btn btn-danger"
            onClick={handleForceStop}
            disabled={busy}
            title="Force-finish a stuck stop. Captured browser steps are preserved, but HAR and storage-state cleanup may be incomplete."
          >
            Force Finish
          </button>
        )}
        {controllable && (
          <button className="btn btn-secondary" onClick={handlePauseResume} disabled={busy}>
            {session.status === 'paused' ? 'Resume Recording' : 'Pause Recording'}
          </button>
        )}
        {controllable && (
          <button className="btn btn-secondary" onClick={handleFocus} disabled={busy}>Focus Browser</button>
        )}
        {controllable && (
          <button
            className={`btn btn-secondary ${session.picking ? 'brp-pick-active' : ''}`}
            onClick={handleTogglePick}
            disabled={busy}
            data-testid="pick-mode-toggle"
          >
            {session.picking ? 'Picking… (click an element)' : 'Pick Verification'}
          </button>
        )}
        {session.har_available && (
          <button className="btn btn-secondary" onClick={handleDownloadHAR} disabled={busy}>Download HAR</button>
        )}
      </div>

      {controllable && (
        <div className="brp-section-row">
          <span className="brp-section-label">Recording into</span>
          {SECTIONS.map(sec => (
            <button
              key={sec.value}
              type="button"
              className={`brp-section-btn ${(session.section || 'actions') === sec.value ? 'active' : ''}`}
              data-testid={`section-${sec.value}`}
              onClick={() => handleSection(sec.value)}
            >
              {sec.label}
            </button>
          ))}
        </div>
      )}

      {controllable && (
        <div className="brp-row">
          <div className="rec-url-input-group brp-url-input-group">
            <select
              className="rec-input rec-url-scheme-select"
              value={navigateScheme}
              onChange={e => setNavigateScheme(e.target.value as HttpURLScheme)}
              aria-label="Navigate URL scheme"
            >
              <option value="https://">https://</option>
              <option value="http://">http://</option>
            </select>
            <input
              className="rec-input"
              aria-label="Navigate the recording browser"
              value={navigateAddress}
              onChange={e => handleNavigateAddressChange(e.target.value)}
              placeholder="app.example.com/path"
            />
          </div>
          <button className="btn btn-secondary btn-sm" onClick={handleNavigate} disabled={busy}>Go</button>
        </div>
      )}

      {controllable && suggestions.length > 0 && (
        <div className="brp-suggestions" data-testid="transaction-suggestions">
          <div className="brp-suggestions-title">Suggested transactions</div>
          {suggestions.map(s => (
            <div key={s.name + s.at_step_seq} className="brp-suggestion">
              <span className="brp-suggestion-name">{s.name}</span>
              <span className="brp-suggestion-detail">at step #{s.at_step_seq}</span>
              <button type="button" className="rec-inline-action" onClick={() => handleAcceptSuggestion(s)}>Add</button>
            </div>
          ))}
        </div>
      )}

      {controllable && (
        <div className="brp-markers">
          <div className="brp-row">
            <input
              className="rec-input"
              aria-label="Transaction name"
              value={transactionName}
              onChange={e => setTransactionName(e.target.value)}
              placeholder="Transaction name, e.g. Checkout"
            />
            <button className="btn btn-secondary btn-sm" onClick={handleStartTransaction}>Start Transaction</button>
          </div>
          {openTransactions.length > 0 && (
            <div className="brp-open-transactions" aria-label="Open transactions">
              <div className="brp-open-transactions-title">Open transactions</div>
              {openTransactions.map(name => (
                <div key={name} className="brp-txn-row" data-testid="open-transaction">
                  <span className="brp-txn-name">{name}</span>
                  <button
                    type="button"
                    className="btn btn-secondary btn-sm brp-txn-end"
                    onClick={() => handleEndTransaction(name)}
                    aria-label={`End transaction ${name}`}
                  >
                    End Transaction
                  </button>
                </div>
              ))}
            </div>
          )}
          <div className="brp-row">
            <input
              className="rec-input"
              aria-label="Note"
              value={noteText}
              onChange={e => setNoteText(e.target.value)}
              placeholder="Add a note for the next action…"
            />
            <button className="btn btn-secondary btn-sm" onClick={handleAddNote}>Add Note</button>
          </div>
        </div>
      )}

      {live && (
        <div className="brp-hint">
          {session.status === 'paused'
            ? 'Capture is paused — interactions, navigations, and elapsed think time are not recorded until you resume.'
            : 'Interact with the recording browser window — every click, fill, and navigation is captured here. Use the in-page toolbar to pause capture or pick verification elements.'}
        </div>
      )}

      {steps.length > 0 && (
        <div className="rec-live-feed brp-feed">
          <div className="rec-feed-header">
            <span>{live ? 'Live Steps' : 'Recorded Steps · chronological journey'}</span>
            <span>
              <button
                type="button"
                className="rec-inline-action"
                disabled={stepThumbnailsLoading}
                onClick={() => void loadStepThumbnails()}
              >
                {stepThumbnailsLoading
                  ? 'Loading thumbnails…'
                  : Object.keys(stepThumbnails).length > 0
                    ? 'Refresh thumbnails'
                    : 'Load thumbnails'}
              </button>
              <span className="rec-feed-count">{session.step_count}</span>
            </span>
          </div>
          {steps.some(step => step.candidates?.length) && <p className="brp-hint">Selector quality is a heuristic ranking of captured locators, not a measured replay success rate. Open Step details for the locator and its capture reason; verify the generated script to test it.</p>}
          {steps.length > 10 && (
            <div className="brp-step-search">
              <input
                className="rec-input"
                type="search"
                value={stepSearch}
                onChange={event => setStepSearch(event.target.value)}
                placeholder="Search recorded steps"
                aria-label="Search recorded steps"
              />
              {stepSearchQuery && <span>{visibleSteps.length} match{visibleSteps.length === 1 ? '' : 'es'}</span>}
            </div>
          )}
          <p className="brp-hint">Selector strength is a locator heuristic, not a measured replay success rate.</p>
          <div className="rec-feed-list">
            {renderedSteps.map(step => {
              const quality = selectorQuality(step)
              const thumbnail = stepThumbnails[step.seq]
              return (
                <div key={step.seq} className={`rec-feed-item brp-step ${step.excluded ? 'brp-step-excluded' : ''}`}>
                  {thumbnail && <img className="brp-step-thumbnail" src={thumbnail} alt="" />}
                  <span className="brp-step-seq">#{step.seq}</span>
                  <span className={`brp-step-kind brp-step-kind-${step.kind}`}>{STEP_KIND_LABELS[step.kind] || step.kind}</span>
                  <span className="rec-path" title={stepDetail(step)}>{stepDetail(step)}</span>
                  <details><summary>Step details</summary><div>Section: {step.section || 'actions'}</div><div>{step.frame_selector ? `Frame: ${step.frame_selector}` : 'Main page'}</div>{step.candidates?.map((candidate, index) => <div key={index}>{candidate.kind}: {candidate.value} — {candidate.reason || 'Captured locator'}</div>)}</details>
                  {step.masked && <span className="brp-masked" title="Value masked at capture">masked</span>}
                  {step.mark_secret && <span className="brp-masked" title="Marked as a runtime secret">secret</span>}
                  {step.mark_param && <span className="brp-masked" title="Marked as a data parameter">param</span>}
                  {quality && (
                    <span className={`brp-quality-badge brp-quality-${quality.tone}`} title={`Heuristic selector quality ${quality.score}/100 (${step.candidates?.[0]?.kind}); not a replay success probability`}>
                      Selector: {quality.tone} ({quality.score}/100)
                    </span>
                  )}
                  {controllable && INTERACTION_KINDS.has(step.kind) && (
                    <span className="brp-step-actions">
                      <button type="button" className="brp-step-action" title={step.excluded ? 'Include' : 'Exclude'} onClick={() => handleToggleStepExcluded(step.seq, !step.excluded)}>
                        {step.excluded ? '+' : '−'}
                      </button>
                      {step.kind === 'fill' && !step.mark_secret && (
                        <button type="button" className="brp-step-action" title="Mark as secret" onClick={() => handleMarkStep(step.seq, 'secret')}>🔒</button>
                      )}
                      {step.kind === 'fill' && !step.mark_param && (
                        <button type="button" className="brp-step-action" title="Mark as data parameter" onClick={() => handleMarkStep(step.seq, 'param')}>≈</button>
                      )}
                    </span>
                  )}
                </div>
              )
            })}
            {renderedSteps.length === 0 && (
              <div className="brp-step-empty">No recorded steps match this search.</div>
            )}
          </div>
          {visibleSteps.length > 25 && (
            <button type="button" className="rec-inline-action" onClick={() => setShowAllSteps(current => !current)}>
              {showAllSteps ? 'Show fewer' : `Show all ${visibleSteps.length} steps`}
            </button>
          )}
          {controllable && session.step_count > 0 && (
            <button type="button" className="rec-inline-action brp-delete-last" onClick={handleDeleteLastStep}>Delete last step</button>
          )}
        </div>
      )}

      {verificationHistory.some(id => id !== replaySessionId) && (
        <details className="brp-verification-history">
          <summary>Previous recording verification</summary>
          <p>Historical evidence only. Conversion settings or recorded steps may have changed. Verify each current output again before relying on these results.</p>
          {verificationHistory.filter(id => id !== replaySessionId).map(id => <HistoricalVerification key={id} replaySessionId={id} projectId={session.project_id} bridge={api.browserRecorderBridge} />)}
        </details>
      )}
      {(session.annotations?.length ?? 0) > 0 && (
        <div className="brp-warnings" data-testid="recording-annotations">
          <div className="brp-warnings-title">Console & network issues ({session.annotation_count ?? session.annotations!.length})</div>
          {session.annotations!.slice(-6).map((ann, idx) => (
            <div key={idx} className="brp-warning">[{ann.kind === 'console_error' ? 'console' : 'request'}] {ann.text}</div>
          ))}
        </div>
      )}

      {(session.warnings?.length ?? 0) > 0 && (
        <div className="brp-warnings">
          <div className="brp-warnings-title">Warnings</div>
          {session.warnings!.map((warning, idx) => (
            <div key={idx} className="brp-warning">{warning}</div>
          ))}
        </div>
      )}

      {stopped && replaySessionId && (
        <BrowserReplayProgress
          replaySessionId={replaySessionId}
          projectId={session.project_id}
          bridge={api.browserRecorderBridge}
          onApplySuggestion={handleApplyReplaySuggestion}
          onComplete={report => handleReplayComplete(replaySessionId, report)}
          onDismiss={() => setReplaySessionId(null)}
        />
      )}

      {stopped && (
        <div className="brp-convert">
          <div className="brp-convert-title">Save as Script</div>
          {previewSecretRequirements.length > 0 && (
            <SecretBindingReview
              ref={previewCredentialReviewRef}
              requirements={previewSecretRequirements}
              reviewGeneration={previewCredentialReviewIdentity}
              projectId={session.project_id}
              sourceSecretSessionId={preview?.source_secret_session_id}
              sourceSecretSessionExpiresAt={preview?.source_secret_session_expires_at}
              sourceMigrationAllowed={preview?.source_secret_migration_allowed}
              disabled={previewImportDraftExpiry?.expired || busy || previewParameterizationApplying}
              onApply={handleApplyPreviewSecretBindings}
              onPreparedChange={setPreviewPreparedSecrets}
              onProjectSecretsConfigured={handlePreviewProjectSecretsConfigured}
            />
          )}
          {previewProjectSecretsConfiguredForReview && (
            <div className="rec-feedback" role="status">
              Project-secret values configured during this review remain in Project Secrets independently of script saving.
              {' '}If script saving fails, retrying this save reuses those configured values. Any atomic-save promise on this page applies to scripts, not external secret-store writes.
            </div>
          )}
          {previewParameterizationPanels.map(({ parameterization, script, scriptIndex }) => (
            <ParameterizationReview
              key={`${scriptIndex}:${script.name}`}
              parameterization={parameterization}
              script={script}
              scriptName={script.name}
              reviewGeneration={`${previewReviewGeneration}:${sessionId}`}
              disabled={previewImportDraftExpiry?.expired || busy}
              onApplyingChange={setPreviewParameterizationApplying}
              onApplied={handleApplyPreviewParameterization(scriptIndex)}
            />
          ))}
          <div className="rec-form-row">
            <label className="rec-label" htmlFor="browser-recording-script-name">Script Name</label>
            <input
              id="browser-recording-script-name"
              className="rec-input"
              value={scriptName}
              onChange={e => { advancePreviewReviewGeneration(); setScriptName(e.target.value) }}
              disabled={busy}
              placeholder="e.g. Login & Checkout Flow"
            />
          </div>
          <label className="rec-checkbox-row">
            <input type="checkbox" checked={splitAtTransactions} disabled={busy} onChange={e => { advancePreviewReviewGeneration(); setSplitAtTransactions(e.target.checked) }} />
            <span>Split into one script per transaction</span>
          </label>
          {session.har_available && (
            <label className="rec-checkbox-row">
              <input type="checkbox" checked={alsoCreateHTTP} disabled={busy} onChange={e => { advancePreviewReviewGeneration(); setAlsoCreateHTTP(e.target.checked) }} />
              <span>Also create a protocol-level HTTP script from the recorded HAR</span>
            </label>
          )}
          <label className="rec-checkbox-row">
            <input type="checkbox" checked={parameterizeInputs} disabled={busy} onChange={e => { advancePreviewReviewGeneration(); setParameterizeInputs(e.target.checked) }} />
            <span>Parameterize inputs</span>
          </label>
          {preview && (
            <div className="brp-preview-summary">
              {previewRequestKeyRef.current !== JSON.stringify(convertRequest()) && <p role="status">Conversion changed. Preview Conversion again to review the current outputs.</p>}
              <div>
                Conversion review · {importResultScripts(preview).length} outputs
                {(preview.parameterization?.summary?.total ?? 0) > 0 &&
                  ` · ${preview.parameterization!.summary.total} parameterization suggestions`}
              </div>
              <p>Destination: Script Library · {folderId ? 'selected folder' : 'project root'}. Saves remain drafts; debug and approve each saved script before execution. Recording verification checks the selected conversion with think time skipped.</p>
              {importResultScripts(preview).map((script, index) => (
                <section key={index} className="brp-output-review" aria-label={`Output ${index + 1}: ${script.name}`}>
                  <strong>{script.name} · {script.protocol}</strong>
                  <p>{script.init?.length || 0} init · {script.actions?.length || 0} main · {script.teardown?.length || 0} teardown actions</p>
                  <p role="status">{outputReports[index] ? outputReports[index].success ? 'Verification passed for this conversion' : 'Verification incomplete or failed — review evidence' : 'Unverified draft'}</p>
                  <details><summary>Journey and dependencies</summary>
                    {(['init', 'actions', 'teardown'] as const).map(phase => <ol key={phase} aria-label={phase}>{script[phase]?.map((action, actionIndex) => <li key={actionIndex}>{action.name || action.type} · {action.type}</li>)}</ol>)}
                    <p>Data sources: {(script.data_sources || []).map(source => source.name).join(', ') || 'None'}</p>
                    <p>Parameters: {(script.parameters || []).map(parameter => parameter.name).join(', ') || 'None'}</p>
                    <p>Secrets to review: {secretBindingRequirementsForScript(preview.secret_binding_requirements || [], index, script).length}</p>
                  </details>
                  <button className="btn btn-secondary" disabled={busy || (!!replaySessionId && !lastVerifyReport) || previewParameterizationApplying || previewRequestKeyRef.current !== JSON.stringify(convertRequest())} onClick={() => void startVerificationReplay(`Verifying ${script.name}`, index)}>Verify output {index + 1}</button>
                </section>
              ))}
              {(preview.warnings?.length ?? 0) > 0 && (
                <div className="brp-warnings">
                  {preview.warnings.map((warning, idx) => (
                    <div key={idx} className="brp-warning">{warning}</div>
                  ))}
                </div>
              )}
            </div>
          )}
          {!lastVerifyReport && (
            <div className="brp-hint">Replay verification has not completed for this recording.</div>
          )}
          {lastVerifyReport && !lastVerifyReport.success && (
            <div className="rec-feedback error">Latest replay verification found issues. Save is still available as an explicit draft decision.</div>
          )}
          {showSalesforceHandoff && (
            <div className="brp-hint">
              Salesforce-family traffic was detected in the recorded HAR. Review a native HTTP draft before saving when Aura, Lightning, Mulesoft, or Agentforce calls are part of the flow.
            </div>
          )}
          {destinationControl}
          <div className="brp-actions">
            {!replaySessionId && (
              <button className="btn btn-secondary" onClick={handleVerify} disabled={busy} data-testid="verify-recording">Verify primary output</button>
            )}
            <button className="btn btn-secondary" onClick={handlePreview} disabled={busy}>Preview Conversion</button>
            {showSalesforceHandoff && (
              <button className="btn btn-secondary" onClick={handleReviewSalesforceHTTP} disabled={busy}>Review Salesforce HTTP Draft</button>
            )}
            <button className="btn btn-primary" onClick={handleSave} disabled={previewImportDraftExpiry?.expired || busy || previewParameterizationApplying || previewPreparedSecrets?.valid === false}>{previewSaveLabel}</button>
            {onEdit && (
              <button className="btn btn-secondary" onClick={() => { void handleOpenEditor() }} disabled={previewImportDraftExpiry?.expired || busy || previewParameterizationApplying || !editableScript || previewPreparedSecrets?.valid === false}>
                {previewRequiresCredentialPreservingSave ? 'Save And Open In Visual Editor' : 'Open in Visual Editor'}
              </button>
            )}
          </div>
          {previewImportDraftExpiry && (
            <div className={`rec-feedback ${previewImportDraftExpiry.expired ? 'error' : ''}`} role={previewImportDraftExpiry.expired ? 'alert' : 'status'}>
              {previewImportDraftExpiry.label}
            </div>
          )}
          {salesforceDraft && salesforceScript && (
            <div className="brp-preview-summary">
              {salesforceSecretRequirements.length > 0 && (
                <SecretBindingReview
                  ref={salesforceCredentialReviewRef}
                  requirements={salesforceSecretRequirements}
                  reviewGeneration={salesforceCredentialReviewIdentity}
                  projectId={session.project_id}
                  sourceSecretSessionId={salesforceDraft.source_secret_session_id}
                  sourceSecretSessionExpiresAt={salesforceDraft.source_secret_session_expires_at}
                  sourceMigrationAllowed={salesforceDraft.source_secret_migration_allowed}
                  disabled={salesforceImportDraftExpiry?.expired || busy || salesforceParameterizationApplying}
                  onApply={handleApplySalesforceSecretBindings}
                  onPreparedChange={setSalesforcePreparedSecrets}
                  onProjectSecretsConfigured={handleSalesforceProjectSecretsConfigured}
                />
              )}
              {salesforceProjectSecretsConfiguredForReview && (
                <div className="rec-feedback" role="status">
                  Project-secret values configured during this review remain in Project Secrets independently of script saving.
                  {' '}If script saving fails, retrying this save reuses those configured values. Any atomic-save promise on this page applies to scripts, not external secret-store writes.
                </div>
              )}
              {salesforceDraft.parameterization && (salesforceDraft.parameterization.suggestions?.length || 0) > 0 && salesforceScript && (
                <ParameterizationReview
                  parameterization={salesforceDraft.parameterization}
                  script={salesforceScript}
                  reviewGeneration={`${salesforceReviewGeneration}:${sessionId}`}
                  disabled={salesforceImportDraftExpiry?.expired || busy}
                  onApplyingChange={setSalesforceParameterizationApplying}
                  onApplied={handleApplySalesforceParameterization}
                />
              )}
              <div>
                Salesforce HTTP draft: {salesforceScript.actions?.length || 0} actions
                {salesforceReview?.har_match_coverage &&
                  ` · ${Math.round(salesforceReview.har_match_coverage.coverage_pct || 0)}% HAR coverage`}
              </div>
              {(salesforceDraft.warnings?.length ?? 0) > 0 && (
                <div className="brp-warnings">
                  {salesforceDraft.warnings.slice(0, 4).map((warning, idx) => (
                    <div key={idx} className="brp-warning">{warning}</div>
                  ))}
                </div>
              )}
              <div className="brp-actions">
                <button className="btn btn-primary" onClick={() => { void handleSaveSalesforceDraft() }} disabled={salesforceImportDraftExpiry?.expired || busy || salesforceParameterizationApplying || salesforcePreparedSecrets?.valid === false}>{salesforceSaveLabel}</button>
                {onEdit && (
                  <button className="btn btn-secondary" onClick={() => { void handleOpenSalesforceDraft() }} disabled={salesforceImportDraftExpiry?.expired || busy || salesforceParameterizationApplying || salesforcePreparedSecrets?.valid === false}>
                    {salesforceRequiresCredentialPreservingSave ? 'Save And Open Salesforce Draft' : 'Open Salesforce Draft'}
                  </button>
                )}
              </div>
              {salesforceImportDraftExpiry && (
                <div className={`rec-feedback ${salesforceImportDraftExpiry.expired ? 'error' : ''}`} role={salesforceImportDraftExpiry.expired ? 'alert' : 'status'}>
                  {salesforceImportDraftExpiry.label}
                </div>
              )}
            </div>
          )}
          {savedScript && (
            <div className="brp-saved-note">
              Saved as “{savedScript.name}”{standalone ? ' — find it in the Script Library.' : '.'}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
