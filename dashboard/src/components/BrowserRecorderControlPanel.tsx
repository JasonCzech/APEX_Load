import { useState, useEffect, useCallback, useRef } from 'react'

import { useApi } from '../hooks/useApi'
import { downloadBlob } from '../utils/fileUtils'
import {
  type BrowserRecorderSession,
  type BrowserRecorderStep,
  type BrowserRecorderTransactionSuggestion,
} from '../hooks/api/useBrowserRecorderApi'
import { type BrowserReplayReport, type ReplayRepairSuggestion, type StoredScript } from '../types'
import { type RecorderImportResult, type SalesforceImportResult } from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'
import {
  composeHttpURL,
  DEFAULT_HTTP_URL_SCHEME,
  normalizeHttpURLInput,
  type HttpURLScheme,
} from '../utils/httpUrlInput'
import { type ImportedSaveResult } from './useScriptsAuthoringState'
import BrowserReplayProgress from './BrowserReplayProgress'
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
  const [session, setSession] = useState<BrowserRecorderSession | null>(null)
  const [loadError, setLoadError] = useState('')
  const [scriptName, setScriptName] = useState('')
  const [preview, setPreview] = useState<RecorderImportResult | null>(null)
  const [salesforceDraft, setSalesforceDraft] = useState<SalesforceImportResult | null>(null)
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
  const [lastVerifyReport, setLastVerifyReport] = useState<BrowserReplayReport | null>(null)
  const [stepSearch, setStepSearch] = useState('')
  const refreshInFlightRef = useRef(false)

  const showFeedback = (msg: string, type: 'success' | 'error' = 'success') => {
    setFeedback(msg)
    setFeedbackType(type)
    setTimeout(() => setFeedback(''), 4000)
  }

  const refresh = useCallback(async () => {
    if (refreshInFlightRef.current) return
    refreshInFlightRef.current = true
    try {
      const res = await api.getBrowserRecording(sessionId)
      setSession(res.session)
      setLoadError('')
    } catch (e: any) {
      setLoadError(e.message || 'Failed to load browser recording session')
    } finally {
      refreshInFlightRef.current = false
    }
  }, [api, sessionId])

  const loadSuggestions = useCallback(async () => {
    try {
      const res = await api.suggestBrowserRecordingTransactions(sessionId)
      setSuggestions(res.suggestions || [])
    } catch {
      /* suggestions are best-effort */
    }
  }, [api, sessionId])

  useEffect(() => {
    refresh()
  }, [refresh])

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
    // Seed the script name once from the session; later renames stay manual.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session?.name])

  useEffect(() => {
    if (standalone && session) {
      document.title = `Recording — ${session.name}`
    }
  }, [standalone, session])

  // Refresh auto-transaction suggestions as navigations accumulate.
  useEffect(() => {
    if (session && (session.navigation_count || 0) > 0) {
      loadSuggestions()
    }
  }, [session?.navigation_count, loadSuggestions])

  const handleStop = async () => {
    setBusy(true)
    try {
      const res = await api.stopBrowserRecording(sessionId)
      setSession(res.session)
      if (res.session.status === 'stopping') {
        showFeedback('Stop requested — finalizing browser and HAR capture')
      } else {
        showFeedback(`Recording stopped — ${res.session.step_count} steps captured`)
      }
    } catch (e: any) {
      showFeedback(e.message, 'error')
    } finally {
      setBusy(false)
    }
  }

  const handleForceStop = async () => {
    setBusy(true)
    try {
      const res = await api.forceStopBrowserRecording(sessionId)
      setSession(res.session)
      showFeedback(`Recording force-finished — ${res.session.step_count} captured steps preserved`)
    } catch (e: any) {
      showFeedback(e.message, 'error')
    } finally {
      setBusy(false)
    }
  }

  const handlePauseResume = async () => {
    if (!session) return
    setBusy(true)
    try {
      const res = session.status === 'paused'
        ? await api.resumeBrowserRecording(sessionId)
        : await api.pauseBrowserRecording(sessionId)
      setSession(res.session)
      showFeedback(res.session.status === 'paused'
        ? 'Recording paused — interactions and think time are not captured'
        : 'Recording resumed')
    } catch (e: any) {
      showFeedback(e.message, 'error')
    } finally {
      setBusy(false)
    }
  }

  const handleFocus = async () => {
    try {
      await api.focusBrowserRecording(sessionId)
      showFeedback('Recording browser brought to the front')
    } catch (e: any) {
      showFeedback(e.message, 'error')
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
      setSession(res.session)
      setNavigateAddress('')
      showFeedback(`Recording browser navigated to ${url}`)
    } catch (e: any) {
      showFeedback(e.message, 'error')
    } finally {
      setBusy(false)
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
      setSession(res.session)
      setTransactionName('')
      showFeedback(`Transaction "${name}" started`)
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleEndTransaction = async (name: string) => {
    try {
      const res = await api.endBrowserRecordingTransaction(sessionId, name)
      setSession(res.session)
      showFeedback(`Transaction "${res.marker.name || name}" ended`)
    } catch (e: any) {
      showFeedback(e.message, 'error')
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
      setSession(res.session)
      setNoteText('')
      showFeedback('Note added — it will attach to the next recorded action')
    } catch (e: any) {
      showFeedback(e.message, 'error')
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
    setBusy(true)
    try {
      const result = await api.previewBrowserRecordingConversion(sessionId, convertRequest())
      setPreview(result)
      const suggestions = result.parameterization?.summary?.total ?? 0
      showFeedback(`Preview generated with ${result.script?.actions?.length || 0} actions and ${suggestions} parameterization suggestions`)
    } catch (e: any) {
      showFeedback(e.message, 'error')
    } finally {
      setBusy(false)
    }
  }

  const handleSave = async () => {
    if (!confirmReplaySaveRisk()) return
    setBusy(true)
    try {
      if (api.browserRecorderBridge) {
        const result = await api.previewBrowserRecordingConversion(sessionId, convertRequest())
        const scripts = (result.scripts?.length ? result.scripts : result.script ? [result.script] : []) as ScriptDefinition[]
        if (scripts.length === 0) {
          throw new Error('Conversion produced no scripts to save')
        }
        const saved = await Promise.all(scripts.map(script => api.createScript(script, session?.project_id)))
        const primary = saved[0]
        setSavedScript(primary)
        setPreview(result)
        onImported?.({ scripts: saved })
        showFeedback(`Saved ${saved.length === 1 ? `script "${primary.name}"` : `${saved.length} scripts`} to the remote script library`)
        return
      }

      const result = await api.convertBrowserRecording(sessionId, convertRequest())
      const stored = result.script as unknown as StoredScript | undefined
      if (stored) {
        setSavedScript(stored)
        onImported?.({ scripts: [stored] })
      }
      const httpNote = result.http_script ? ` (plus HTTP script "${result.http_script.name}")` : ''
      showFeedback(`Script "${stored?.name || scriptName}" saved to the script library${httpNote}`)
    } catch (e: any) {
      showFeedback(e.message, 'error')
    } finally {
      setBusy(false)
    }
  }

  const handleSection = async (section: string) => {
    try {
      const res = await api.setBrowserRecordingSection(sessionId, section)
      setSession(res.session)
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleTogglePick = async () => {
    if (!session) return
    try {
      const res = await api.setBrowserRecordingPickMode(sessionId, !session.picking)
      setSession(res.session)
      showFeedback(res.session.picking
        ? 'Pick mode on — click an element in the recording browser to add a verification'
        : 'Pick mode off')
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleDeleteLastStep = async () => {
    try {
      const res = await api.deleteLastBrowserRecordingStep(sessionId)
      setSession(res.session)
      showFeedback('Last step removed')
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleToggleStepExcluded = async (seq: number, excluded: boolean) => {
    try {
      const res = await api.updateBrowserRecordingStep(sessionId, seq, { excluded })
      setSession(res.session)
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleMarkStep = async (seq: number, kind: 'secret' | 'param') => {
    try {
      const res = await api.updateBrowserRecordingStep(sessionId, seq, kind === 'secret' ? { mark_secret: true } : { mark_param: true })
      setSession(res.session)
      showFeedback(`Step #${seq} marked as ${kind === 'secret' ? 'a secret' : 'a data parameter'}`)
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleAcceptSuggestion = async (suggestion: BrowserRecorderTransactionSuggestion) => {
    try {
      const res = await api.startBrowserRecordingTransaction(sessionId, suggestion.name, suggestion.at_step_seq)
      setSession(res.session)
      setSuggestions(prev => prev.filter(s => s.name !== suggestion.name || s.at_step_seq !== suggestion.at_step_seq))
      showFeedback(`Transaction "${suggestion.name}" started`)
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleVerify = async () => {
    setBusy(true)
    try {
      setLastVerifyReport(null)
      const res = await api.verifyBrowserRecording(sessionId)
      setReplaySessionId(res.replay_session_id)
      showFeedback('Verifying — replaying the recorded script')
    } catch (e: any) {
      showFeedback(e.message, 'error')
    } finally {
      setBusy(false)
    }
  }

  const handleReplayComplete = (report: BrowserReplayReport | null) => {
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
    const patched = await api.updateBrowserRecordingStep(sessionId, seq, { promote_selector: selector })
    setSession(patched.session)
    setLastVerifyReport(null)
    const verify = await api.verifyBrowserRecording(sessionId)
    setReplaySessionId(verify.replay_session_id)
    showFeedback(`Selector promoted for step #${seq}; verifying again`)
  }

  const editableScript = (preview?.script as ScriptDefinition | undefined) || savedScript?.script

  const handleOpenEditor = () => {
    if (editableScript) {
      onEdit?.(editableScript)
    }
  }

  const handleDownloadHAR = async () => {
    try {
      const res = await api.exportBrowserRecording(sessionId, 'har')
      if (!res.ok) {
        const payload = await res.json().catch(() => ({ error: 'Failed to export HAR' }))
        throw new Error(payload.error || 'Failed to export HAR')
      }
      const blob = await res.blob()
      const safeName = safeRecordingFileName(session?.name)
      downloadBlob(blob, `${safeName || 'browser-recording'}.har`)
      showFeedback('Side-car HAR downloaded')
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleReviewSalesforceHTTP = async () => {
    if (!session) return
    setBusy(true)
    try {
      const res = await api.exportBrowserRecording(sessionId, 'har')
      if (!res.ok) {
        const payload = await res.json().catch(() => ({ error: 'Failed to export HAR' }))
        throw new Error(payload.error || 'Failed to export HAR')
      }
      const blob = await res.blob()
      const file = new File([blob], `${safeRecordingFileName(session.name)}.har`, { type: blob.type || 'application/json' })
      const result = await api.importSalesforce(file, {
        name: scriptName || `${session.name} Salesforce HTTP`,
        project_id: session.project_id,
      })
      setSalesforceDraft(result)
      const script = result.script || result.scripts?.[0]
      showFeedback(`Salesforce HTTP draft ready with ${script?.actions?.length || 0} actions`)
    } catch (e: any) {
      showFeedback(e.message, 'error')
    } finally {
      setBusy(false)
    }
  }

  const handleSaveSalesforceDraft = async () => {
    const script = salesforceDraft?.script || salesforceDraft?.scripts?.[0]
    if (!script) return
    setBusy(true)
    try {
      const saved = await api.createScript(script, session?.project_id)
      setSavedScript(saved)
      onImported?.({ scripts: [saved] })
      showFeedback(`Salesforce HTTP script "${saved.name}" saved to the script library`)
    } catch (e: any) {
      showFeedback(e.message, 'error')
    } finally {
      setBusy(false)
    }
  }

  const handleOpenSalesforceDraft = () => {
    const script = salesforceDraft?.script || salesforceDraft?.scripts?.[0]
    if (script) {
      onEdit?.(script)
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

  return (
    <div className={`brp-panel ${standalone ? 'brp-standalone' : ''}`} data-testid="browser-recorder-panel">
      <div className="brp-header">
        <div className="brp-title">
          {live && <span className="rec-live-dot" />}
          <span className="brp-name">{session.name}</span>
          <span className={`brp-status brp-status-${session.status}`}>{statusLabel(session)}</span>
        </div>
        {onDismiss && !live && (
          <button type="button" className="rec-inline-action" onClick={onDismiss}>Dismiss</button>
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
          onComplete={handleReplayComplete}
          onDismiss={() => setReplaySessionId(null)}
        />
      )}

      {stopped && (
        <div className="brp-convert">
          <div className="brp-convert-title">Save as Script</div>
          <div className="rec-form-row">
            <label className="rec-label">Script Name</label>
            <input
              className="rec-input"
              value={scriptName}
              onChange={e => setScriptName(e.target.value)}
              placeholder="e.g. Login & Checkout Flow"
            />
          </div>
          <label className="rec-checkbox-row">
            <input type="checkbox" checked={splitAtTransactions} onChange={e => setSplitAtTransactions(e.target.checked)} />
            <span>Split into one script per transaction</span>
          </label>
          {session.har_available && (
            <label className="rec-checkbox-row">
              <input type="checkbox" checked={alsoCreateHTTP} onChange={e => setAlsoCreateHTTP(e.target.checked)} />
              <span>Also create a protocol-level HTTP script from the recorded HAR</span>
            </label>
          )}
          <label className="rec-checkbox-row">
            <input type="checkbox" checked={parameterizeInputs} onChange={e => setParameterizeInputs(e.target.checked)} />
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
            <button className="btn btn-primary" onClick={handleSave} disabled={busy}>Save to Script Library</button>
            {onEdit && (
              <button className="btn btn-secondary" onClick={handleOpenEditor} disabled={busy || !editableScript}>
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
                <button className="btn btn-primary" onClick={handleSaveSalesforceDraft} disabled={busy}>Save Salesforce HTTP Script</button>
                {onEdit && (
                  <button className="btn btn-secondary" onClick={handleOpenSalesforceDraft} disabled={busy}>Open Salesforce Draft</button>
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
