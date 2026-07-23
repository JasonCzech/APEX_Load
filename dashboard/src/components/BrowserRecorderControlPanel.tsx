import { useState, useEffect, useCallback, useLayoutEffect, useMemo, useRef } from 'react'

import { useApi } from '../hooks/useApi'
import { hasDesktopApiCapability } from '../hooks/apiClient'
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
import BrowserReplayProgress from './BrowserReplayProgress'
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
  onImported?: (result: ImportedSaveResult) => void
  onEdit?: (script: ScriptDefinition) => void
  /** Docked mode: remove the panel from the studio after the user is done. */
  onDismiss?: () => void
}

export default function BrowserRecorderControlPanel({
  sessionId,
  standalone = false,
  onImported,
  onEdit,
  onDismiss,
}: BrowserRecorderControlPanelProps) {
  const api = useApi()
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
  const [savedScript, setSavedScript] = useState<StoredScript | null>(null)
  const [busy, setBusy] = useState(false)
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
  const recorderRequestScope = api.browserRecorderRequestScope
  const refreshInFlightRef = useRef<{ sessionId: string; recorderRequestScope: string; request: Promise<void> } | null>(null)
  const verificationStartScopeRef = useRef<object | null>(null)
  const sessionScopeRef = useRef({ sessionId, recorderRequestScope, generation: 0 })
  const feedbackTimerRef = useRef<number | null>(null)
  const previewCredentialReviewRef = useRef<SecretBindingReviewHandle | null>(null)
  const salesforceCredentialReviewRef = useRef<SecretBindingReviewHandle | null>(null)
  const previewReviewGenerationRef = useRef(0)
  const salesforceReviewGenerationRef = useRef(0)
  const [previewReviewGeneration, setPreviewReviewGeneration] = useState(0)
  const [salesforceReviewGeneration, setSalesforceReviewGeneration] = useState(0)
  const advancePreviewReviewGeneration = useCallback(() => {
    const next = previewReviewGenerationRef.current + 1
    previewReviewGenerationRef.current = next
    setPreviewReviewGeneration(next)
    return next
  }, [])
  const advanceSalesforceReviewGeneration = useCallback(() => {
    const next = salesforceReviewGenerationRef.current + 1
    salesforceReviewGenerationRef.current = next
    setSalesforceReviewGeneration(next)
    return next
  }, [])
  const previewPreparedScriptsRef = useRef<ScriptDefinition[] | null>(null)
  const salesforcePreparedScriptsRef = useRef<ScriptDefinition[] | null>(null)
  const previewRequestKeyRef = useRef('')
  // True once the user changed the previewed draft away from what the server's
  // own conversion would produce (applied parameterization selections or
  // renamed a suggested credential reference). Those reviewed edits are pushed
  // over the server-converted scripts after the save-time conversion commits.
  const previewDraftEditedRef = useRef(false)

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
  const isCurrentSession = () => sessionScopeRef.current === renderedSessionScope

  const showFeedback = (msg: string, type: 'success' | 'error' = 'success') => {
    setFeedback(msg)
    setFeedbackType(type)
    if (feedbackTimerRef.current !== null) window.clearTimeout(feedbackTimerRef.current)
    feedbackTimerRef.current = window.setTimeout(() => {
      feedbackTimerRef.current = null
      setFeedback('')
    }, 4000)
  }

  const refresh = useCallback(async () => {
    const inFlight = refreshInFlightRef.current
    if (inFlight?.sessionId === sessionId && inFlight.recorderRequestScope === recorderRequestScope) return inFlight.request
    const scope = { ...sessionScopeRef.current }
    const request = (async () => {
      try {
        const res = await api.getBrowserRecording(sessionId)
        if (sessionScopeRef.current.sessionId !== scope.sessionId || sessionScopeRef.current.recorderRequestScope !== scope.recorderRequestScope || sessionScopeRef.current.generation !== scope.generation) return
        setSession(res.session)
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
  }, [api, recorderRequestScope, sessionId])

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
    salesforcePreparedScriptsRef.current = null
    previewRequestKeyRef.current = ''
    previewDraftEditedRef.current = false
    setSavedScript(null)
    setSuggestions([])
    setReplaySessionId(null)
    setLastVerifyReport(null)
    setStepSearch('')
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
      setSession(res.session)
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
      setSession(res.session)
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
      setSession(res.session)
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
      setSession(res.session)
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
      setSession(res.session)
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
      setSession(res.session)
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
      setSession(res.session)
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
    setPreviewParameterizationApplying(false)
    advancePreviewReviewGeneration()
    setBusy(true)
    try {
      const result = await api.previewBrowserRecordingConversion(sessionId, convertRequest())
      if (!isCurrentSession()) return
      previewPreparedScriptsRef.current = null
      previewDraftEditedRef.current = false
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
    const sourceGeneration = previewReviewGenerationRef.current
    const sourceSession = { ...sessionScopeRef.current }
    const requestIsCurrent = () => previewReviewGenerationRef.current === sourceGeneration
      && sessionScopeRef.current.sessionId === sourceSession.sessionId
      && sessionScopeRef.current.generation === sourceSession.generation
    // The save-time server conversion re-applies the suggested references on
    // its own; only a rename away from a suggestion is a client-side edit that
    // must be pushed over the converted scripts afterwards.
    const suggestedNames = new Map(
      (preview.secret_binding_requirements || []).map(requirement => [requirement.id, requirement.suggested_name]),
    )
    const renamedSuggestion = bindings.some(binding => suggestedNames.get(binding.requirement_id) !== binding.secret_name)
    const resolved = await applyBindingsToResult(preview, bindings, next => {
      if (isCurrentSession()) setPreview(next)
    }, requestIsCurrent)
    if (!isCurrentSession()) return
    if (renamedSuggestion) previewDraftEditedRef.current = true
    previewPreparedScriptsRef.current = importResultScripts(resolved)
  }

  const handleApplyPreviewParameterization = (scriptIndex: number) => (parameterizedScript: ScriptDefinition) => {
    if (!preview) return
    const scripts = importResultScripts(preview)
    if (!scripts[scriptIndex]) return
    previewCredentialReviewRef.current?.preserveEditsForNextReviewGeneration()
    advancePreviewReviewGeneration()
    previewPreparedScriptsRef.current = null
    previewDraftEditedRef.current = true
    const nextScripts = scripts.map((entry, index) => index === scriptIndex ? parameterizedScript : entry)
    setPreview({
      ...preview,
      script: nextScripts[0],
      scripts: nextScripts,
    })
  }

  const preparePreviewScripts = async (result: RecorderImportResult): Promise<ScriptDefinition[] | null> => {
    if ((result.secret_binding_requirements?.length || 0) === 0) return importResultScripts(result)
    const sourceGeneration = previewReviewGenerationRef.current
    const sourceSession = { ...sessionScopeRef.current }
    previewPreparedScriptsRef.current = null
    const committed = await previewCredentialReviewRef.current?.commit()
    return committed
      && previewReviewGenerationRef.current === sourceGeneration
      && sessionScopeRef.current.sessionId === sourceSession.sessionId
      && sessionScopeRef.current.generation === sourceSession.generation
      ? previewPreparedScriptsRef.current
      : null
  }

  // Direct createScript persistence for drafts the controller cannot attest to
  // a headed-browser recording: bridge-mode saves (the remote controller never
  // observed the local recording session) and Salesforce HAR drafts. These are
  // intentionally saved without a source recording session id — createScript
  // rejects headed-browser session ids by design.
  const saveScriptsWithRollback = async (scripts: ScriptDefinition[]): Promise<StoredScript[]> => {
    const created: StoredScript[] = []
    try {
      for (const script of scripts) {
        const stored = await api.createScript(script, session?.project_id)
        created.push(stored)
      }
    } catch (error: unknown) {
      const cleanupResults = await Promise.allSettled(
        [...created].reverse().map(stored => api.deleteScript(stored.id)),
      )
      const cleanupFailures = cleanupResults
        .map((result, index) => result.status === 'rejected' ? created[created.length - 1 - index] : null)
        .filter((stored): stored is StoredScript => stored !== null)
      const saveError = error instanceof Error && error.message.trim()
        ? error.message.trim()
        : 'an unexpected error occurred'

      if (cleanupFailures.length > 0) {
        const remaining = cleanupFailures.map(stored => `“${stored.name}”`).join(', ')
        throw new Error(
          `Could not save all scripts: ${saveError}. Automatic cleanup failed for ${remaining}; delete ${cleanupFailures.length === 1 ? 'it' : 'them'} from the Script Library before retrying.`,
        )
      }

      const cleanupSummary = created.length === 0
        ? 'No scripts were created.'
        : `${created.length === 1 ? 'The script created' : `All ${created.length} scripts created`} before the failure ${created.length === 1 ? 'was' : 'were'} removed, so no partial import remains.`
      throw new Error(`Could not save all scripts: ${saveError}. ${cleanupSummary}`)
    }

    return created.map((stored, index) => withFallbackPersistedScript(stored, scripts[index]))
  }

  // Headed-browser drafts must be persisted through the server-side conversion
  // endpoint: createScript rejects a browser-recording session id outright
  // ("headed-browser drafts must be saved through the server-side browser
  // conversion endpoint") and the server transaction keeps split/dual-script
  // saves atomic. Reviewed client-side edits (parameterization selections,
  // renamed credential references) are then pushed over the converted scripts.
  const saveThroughServerConversion = async (reviewedScripts: ScriptDefinition[]): Promise<StoredScript[]> => {
    const converted = await api.convertBrowserRecording(sessionId, convertRequest())
    const stored = converted.scripts?.length
      ? converted.scripts
      : converted.script ? [converted.script] : []
    if (stored.length === 0) throw new Error('Conversion produced no scripts to save')
    const persisted = stored.map((entry, index) => withFallbackPersistedScript(entry, reviewedScripts[index]))
    if (!previewDraftEditedRef.current) return persisted
    try {
      // Both lists share one order (converted scripts first, the optional
      // HAR-derived HTTP script appended last), so pair them by index.
      for (let index = 0; index < persisted.length && index < reviewedScripts.length; index += 1) {
        const updated = await api.updateScript(persisted[index].id, reviewedScripts[index])
        persisted[index] = withFallbackPersistedScript(updated, reviewedScripts[index])
      }
    } catch (error: unknown) {
      const message = error instanceof Error && error.message.trim()
        ? error.message.trim()
        : 'an unexpected error occurred'
      throw new Error(
        `${persisted.length === 1 ? 'The converted script was' : `${persisted.length} converted scripts were`} saved, but applying the reviewed changes failed: ${message}. Review the saved ${persisted.length === 1 ? 'script' : 'scripts'} in the Script Library.`,
      )
    }
    return persisted
  }

  const handleSave = async () => {
    if (!confirmReplaySaveRisk()) return
    setBusy(true)
    try {
      const requestKey = JSON.stringify(convertRequest())
      const hadCurrentPreview = preview !== null && previewRequestKeyRef.current === requestKey
      let result = hadCurrentPreview ? preview : null
      if (!result) {
        advancePreviewReviewGeneration()
        result = await api.previewBrowserRecordingConversion(sessionId, convertRequest())
        if (!isCurrentSession()) return
        previewPreparedScriptsRef.current = null
        previewDraftEditedRef.current = false
        setPreviewPreparedSecrets(null)
        setPreview(result)
        previewRequestKeyRef.current = requestKey
      }
      if (!hadCurrentPreview && (result.secret_binding_requirements?.length || 0) > 0) {
        showFeedback('Credentials detected. Review the optional values below, then save with the prefilled references.')
        return
      }
      const scripts = await preparePreviewScripts(result)
      if (!scripts) return
      if (!isCurrentSession()) return
      if (scripts.length === 0) throw new Error('Conversion produced no scripts to save')
      // Bridge recordings live on the local recording agent; the remote
      // controller cannot run the conversion for them, so their reviewed
      // scripts are created directly (without a source recording session id).
      const persisted = api.browserRecorderBridge
        ? await saveScriptsWithRollback(scripts)
        : await saveThroughServerConversion(scripts)
      if (!isCurrentSession()) return
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
      setSession(res.session)
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    }
  }

  const handleTogglePick = async () => {
    if (!session) return
    try {
      const res = await api.setBrowserRecordingPickMode(sessionId, !session.picking)
      if (!isCurrentSession()) return
      setSession(res.session)
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
      setSession(res.session)
      showFeedback('Last step removed')
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    }
  }

  const handleToggleStepExcluded = async (seq: number, excluded: boolean) => {
    try {
      const res = await api.updateBrowserRecordingStep(sessionId, seq, { excluded })
      if (!isCurrentSession()) return
      setSession(res.session)
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    }
  }

  const handleMarkStep = async (seq: number, kind: 'secret' | 'param') => {
    try {
      const res = await api.updateBrowserRecordingStep(sessionId, seq, kind === 'secret' ? { mark_secret: true } : { mark_param: true })
      if (!isCurrentSession()) return
      setSession(res.session)
      showFeedback(`Step #${seq} marked as ${kind === 'secret' ? 'a secret' : 'a data parameter'}`)
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    }
  }

  const handleAcceptSuggestion = async (suggestion: BrowserRecorderTransactionSuggestion) => {
    try {
      const res = await api.startBrowserRecordingTransaction(sessionId, suggestion.name, suggestion.at_step_seq)
      if (!isCurrentSession()) return
      setSession(res.session)
      setSuggestions(prev => prev.filter(s => s.name !== suggestion.name || s.at_step_seq !== suggestion.at_step_seq))
      showFeedback(`Transaction "${suggestion.name}" started`)
    } catch (e: any) {
      if (isCurrentSession()) showFeedback(e.message, 'error')
    }
  }

  const startVerificationReplay = async (successMessage: string) => {
    if (verificationStartScopeRef.current === renderedSessionScope) return false
    const verificationScope = renderedSessionScope
    verificationStartScopeRef.current = verificationScope
    const clientReplaySessionId = createReplaySessionId()
    setLastVerifyReport(null)
    setReplaySessionId(clientReplaySessionId || null)
    try {
      const res = await api.verifyBrowserRecording(sessionId, clientReplaySessionId || undefined)
      if (!isCurrentSession()) return false
      setReplaySessionId(res.replay_session_id)
      showFeedback(successMessage)
      return true
    } catch (e: any) {
      if (!isCurrentSession()) return false
      if (clientReplaySessionId && !isDefiniteReplayStartRejection(e)) {
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
    setLastVerifyReport(report)
    if (!report) {
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
      setSession(patched.session)
      await startVerificationReplay(`Selector promoted for step #${seq}; verifying again`)
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
      if (!isCurrentSession()) return
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
    const sourceGeneration = salesforceReviewGenerationRef.current
    const sourceSession = { ...sessionScopeRef.current }
    const requestIsCurrent = () => salesforceReviewGenerationRef.current === sourceGeneration
      && sessionScopeRef.current.sessionId === sourceSession.sessionId
      && sessionScopeRef.current.generation === sourceSession.generation
    const resolved = await applyBindingsToResult(salesforceDraft, bindings, next => {
      if (isCurrentSession()) setSalesforceDraft(next)
    }, requestIsCurrent)
    if (!isCurrentSession()) return
    salesforcePreparedScriptsRef.current = importResultScripts(resolved)
  }

  const handleApplySalesforceParameterization = (parameterizedScript: ScriptDefinition) => {
    if (!salesforceDraft) return
    salesforceCredentialReviewRef.current?.preserveEditsForNextReviewGeneration()
    advanceSalesforceReviewGeneration()
    salesforcePreparedScriptsRef.current = null
    setSalesforceDraft({
      ...salesforceDraft,
      script: parameterizedScript,
      scripts: salesforceDraft.scripts?.length
        ? salesforceDraft.scripts.map((entry, index) => index === 0 ? parameterizedScript : entry)
        : [parameterizedScript],
    })
  }

  const prepareSalesforceScripts = async (): Promise<ScriptDefinition[] | null> => {
    if (!salesforceDraft) return null
    if ((salesforceDraft.secret_binding_requirements?.length || 0) === 0) return importResultScripts(salesforceDraft)
    const sourceGeneration = salesforceReviewGenerationRef.current
    const sourceSession = { ...sessionScopeRef.current }
    salesforcePreparedScriptsRef.current = null
    const committed = await salesforceCredentialReviewRef.current?.commit()
    return committed
      && salesforceReviewGenerationRef.current === sourceGeneration
      && sessionScopeRef.current.sessionId === sourceSession.sessionId
      && sessionScopeRef.current.generation === sourceSession.generation
      ? salesforcePreparedScriptsRef.current
      : null
  }

  const handleSaveSalesforceDraft = async () => {
    setBusy(true)
    try {
      const scripts = await prepareSalesforceScripts()
      if (!scripts?.length) return
      if (!isCurrentSession()) return
      const persisted = await saveScriptsWithRollback(scripts)
      if (!isCurrentSession()) return
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

  const steps = session.steps || []
  const stepSearchQuery = stepSearch.trim().toLowerCase()
  const visibleSteps = [...steps].reverse().filter(step => {
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
  })
  const renderedSteps = live && !stepSearchQuery ? visibleSteps.slice(0, 25) : visibleSteps
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
      new Set(previewSecretRequirements.map(requirement => requirement.suggested_name)).size
  const previewSaveLabel = previewDeferredSecretCount > 0
    ? `Save with ${previewDeferredSecretCount} ${previewDeferredSecretCount === 1 ? 'secret' : 'secrets'} to configure`
    : 'Save to Script Library'
  const salesforceSecretRequirements = salesforceDraft?.secret_binding_requirements || []
  const salesforceDeferredSecretCount = salesforceSecretRequirements.length === 0
    ? 0
    : salesforcePreparedSecrets?.configureLaterCount ??
      new Set(salesforceSecretRequirements.map(requirement => requirement.suggested_name)).size
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
          <button type="button" className="rec-inline-action" onClick={() => {
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
            <span>{live ? 'Live Steps' : 'Recorded Steps'}</span>
            <span className="rec-feed-count">{session.step_count}</span>
          </div>
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
          <div className="rec-feed-list">
            {renderedSteps.map(step => {
              const quality = selectorQuality(step)
              return (
                <div key={step.seq} className={`rec-feed-item brp-step ${step.excluded ? 'brp-step-excluded' : ''}`}>
                  {step.thumbnail && <img className="brp-step-thumbnail" src={step.thumbnail} alt="" />}
                  <span className="brp-step-seq">#{step.seq}</span>
                  <span className={`brp-step-kind brp-step-kind-${step.kind}`}>{STEP_KIND_LABELS[step.kind] || step.kind}</span>
                  <span className="rec-path" title={stepDetail(step)}>{stepDetail(step)}</span>
                  {step.masked && <span className="brp-masked" title="Value masked at capture">masked</span>}
                  {step.mark_secret && <span className="brp-masked" title="Marked as a runtime secret">secret</span>}
                  {step.mark_param && <span className="brp-masked" title="Marked as a data parameter">param</span>}
                  {quality && (
                    <span className={`brp-quality-badge brp-quality-${quality.tone}`} title={`Selector confidence ${quality.score}/100 (${step.candidates?.[0]?.kind})`}>
                      {quality.score}
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
          {controllable && session.step_count > 0 && (
            <button type="button" className="rec-inline-action brp-delete-last" onClick={handleDeleteLastStep}>Delete last step</button>
          )}
        </div>
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
              reviewGeneration={`${previewReviewGeneration}:${sessionId}`}
              projectId={session.project_id}
              sourceSecretSessionId={preview?.source_secret_session_id}
              sourceSecretSessionExpiresAt={preview?.source_secret_session_expires_at}
              sourceMigrationAllowed={preview?.source_secret_migration_allowed}
              disabled={busy || previewParameterizationApplying}
              onApply={handleApplyPreviewSecretBindings}
              onPreparedChange={setPreviewPreparedSecrets}
            />
          )}
          {previewParameterizationPanels.map(({ parameterization, script, scriptIndex }) => (
            <ParameterizationReview
              key={`${scriptIndex}:${script.name}`}
              parameterization={parameterization}
              script={script}
              scriptName={script.name}
              reviewGeneration={`${previewReviewGeneration}:${sessionId}`}
              disabled={busy}
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
          <div className="brp-actions">
            {!replaySessionId && (
              <button className="btn btn-secondary" onClick={handleVerify} disabled={busy} data-testid="verify-recording">Verify Recording</button>
            )}
            <button className="btn btn-secondary" onClick={handlePreview} disabled={busy}>Preview Conversion</button>
            {showSalesforceHandoff && (
              <button className="btn btn-secondary" onClick={handleReviewSalesforceHTTP} disabled={busy}>Review Salesforce HTTP Draft</button>
            )}
            <button className="btn btn-primary" onClick={handleSave} disabled={busy || previewParameterizationApplying || previewPreparedSecrets?.valid === false}>{previewSaveLabel}</button>
            {onEdit && (
              <button className="btn btn-secondary" onClick={() => { void handleOpenEditor() }} disabled={busy || previewParameterizationApplying || !editableScript || previewPreparedSecrets?.valid === false}>
                Open in Visual Editor
              </button>
            )}
          </div>
          {preview && (
            <div className="brp-preview-summary">
              <div>
                Preview: {preview.script?.actions?.length || 0} actions
                {(preview.parameterization?.summary?.total ?? 0) > 0 &&
                  ` · ${preview.parameterization!.summary.total} parameterization suggestions`}
              </div>
              {(preview.warnings?.length ?? 0) > 0 && (
                <div className="brp-warnings">
                  {preview.warnings.map((warning, idx) => (
                    <div key={idx} className="brp-warning">{warning}</div>
                  ))}
                </div>
              )}
            </div>
          )}
          {salesforceDraft && salesforceScript && (
            <div className="brp-preview-summary">
              {salesforceSecretRequirements.length > 0 && (
                <SecretBindingReview
                  ref={salesforceCredentialReviewRef}
                  requirements={salesforceSecretRequirements}
                  reviewGeneration={`${salesforceReviewGeneration}:${sessionId}`}
                  projectId={session.project_id}
                  sourceSecretSessionId={salesforceDraft.source_secret_session_id}
                  sourceSecretSessionExpiresAt={salesforceDraft.source_secret_session_expires_at}
                  sourceMigrationAllowed={salesforceDraft.source_secret_migration_allowed}
                  disabled={busy || salesforceParameterizationApplying}
                  onApply={handleApplySalesforceSecretBindings}
                  onPreparedChange={setSalesforcePreparedSecrets}
                />
              )}
              {salesforceDraft.parameterization && (salesforceDraft.parameterization.suggestions?.length || 0) > 0 && salesforceScript && (
                <ParameterizationReview
                  parameterization={salesforceDraft.parameterization}
                  script={salesforceScript}
                  reviewGeneration={`${salesforceReviewGeneration}:${sessionId}`}
                  disabled={busy}
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
                <button className="btn btn-primary" onClick={() => { void handleSaveSalesforceDraft() }} disabled={busy || salesforceParameterizationApplying || salesforcePreparedSecrets?.valid === false}>{salesforceSaveLabel}</button>
                {onEdit && (
                  <button className="btn btn-secondary" onClick={() => { void handleOpenSalesforceDraft() }} disabled={busy || salesforceParameterizationApplying || salesforcePreparedSecrets?.valid === false}>Open Salesforce Draft</button>
                )}
              </div>
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
