import { useState, useEffect, useCallback } from 'react'
import { downloadBlob } from '../utils/fileUtils'
import { useAsyncTask } from '../hooks/useAsyncTask'
import { useApi } from '../hooks/useApi'
import { apiFetch } from '../hooks/apiClient'
import { Project } from '../types'
import { type RecorderImportResult } from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'
import { withFallbackPersistedScript } from './importSaveUtils'
import BrowserRecorderControlPanel from './BrowserRecorderControlPanel'
import PageViewToggle from './PageViewToggle'
import { attachImportCorrelationSuggestions } from './scriptEditorUtils'
import { type ImportedSaveResult } from './useScriptsAuthoringState'
import {
  DEFAULT_MACOS_NETWORK_SERVICE,
  buildMacOSRestoreProxyCommand,
  buildMacOSStartProxyCommand,
  buildWindowsRestoreProxyCommand,
  buildWindowsStartProxyCommand,
  recorderProxyPort,
} from './recordingProxyCommands'
import './RecordingStudio.css'

interface RecordedRequest {
  seq: number
  timestamp: string
  method: string
  url: string
  scheme?: string
  path: string
  query_string?: string
  host: string
  status_code: number
  response_time_ms: number
  content_type: string
  body: string
  body_encoding?: string
  body_size_bytes?: number
  body_truncated?: boolean
  response_body_encoding?: string
  response_body_size_bytes?: number
  response_body_truncated?: boolean
  think_time_ms: number
  is_static: boolean
  resource_type?: string
  step_name?: string
  transaction_name?: string
  note?: string
  excluded?: boolean
}

interface RecordingSession {
  id: string
  name: string
  status: string
  started_at: string
  stopped_at?: string
  base_url?: string
  requests: RecordedRequest[]
  filters: {
    exclude_static: boolean
    exclude_hosts?: string[]
    include_hosts?: string[]
    capture_cookies?: boolean
  }
}

// confidenceTone buckets a 0..1 correlation confidence for badge styling.
// >=0.8 is auto-applicable in the engine; the UI mirrors that threshold.
function confidenceTone(confidence?: number): 'high' | 'medium' | 'low' | null {
  if (typeof confidence !== 'number') return null
  if (confidence >= 0.8) return 'high'
  if (confidence >= 0.6) return 'medium'
  return 'low'
}

interface RecorderStatus {
  proxy_addr?: string
  ca_cert_path?: string
  active?: {
    id: string
    name: string
    status: string
    request_count: number
    started_at: string
  } | null
  browser?: {
    availability: { available: boolean; reason?: string }
    active?: { id: string; name: string; status: string } | null
    install?: { status: string; error?: string }
  }
}

// The desktop shell embeds the dashboard in a Wails WebView where window.open
// escapes to the system browser, so the popup panel is web-only there.
function isWailsShell(): boolean {
  return typeof window !== 'undefined' &&
    (window.location.protocol === 'wails:' || window.location.hostname === 'wails.localhost')
}

interface RecordingStudioProps {
  activeProject?: Project | null
  onImported?: (result: ImportedSaveResult) => void
  onEdit?: (script: ScriptDefinition) => void
}

export default function RecordingStudio({ activeProject, onImported, onEdit }: RecordingStudioProps) {
  const api = useApi()
  const [sessions, setSessions] = useState<RecordingSession[]>([])
  const [activeSession, setActiveSession] = useState<RecordingSession | null>(null)
  const [certificateFormatView, setCertificateFormatView] = useState<'crt' | 'pem'>('crt')
  const [proxyPlatformView, setProxyPlatformView] = useState<'macos' | 'windows'>('macos')
  const [proxyAddr, setProxyAddr] = useState('')
  const [caCertPath, setCaCertPath] = useState('')
  const [macOSNetworkService, setMacOSNetworkService] = useState(DEFAULT_MACOS_NETWORK_SERVICE)
  const [sessionName, setSessionName] = useState('')
  const [includeHosts, setIncludeHosts] = useState('')
  const [excludeHosts, setExcludeHosts] = useState('')
  const [captureCookies, setCaptureCookies] = useState(false)
  const [feedback, setFeedback] = useState('')
  const [feedbackType, setFeedbackType] = useState<'success' | 'error'>('success')
  const [selectedSession, setSelectedSession] = useState<RecordingSession | null>(null)
  const [selectedRequest, setSelectedRequest] = useState<RecordedRequest | null>(null)
  const [requestDraft, setRequestDraft] = useState({
    step_name: '',
    transaction_name: '',
    note: '',
    think_time_ms: 0,
    excluded: false,
  })
  const [showSetup, setShowSetup] = useState(true)
  const [parameterizeInputs, setParameterizeInputs] = useState(false)
  const [browserAvailability, setBrowserAvailability] = useState<{ available: boolean; reason?: string } | null>(null)
  const [browserPanelSessionId, setBrowserPanelSessionId] = useState<string | null>(null)
  const [browserName, setBrowserName] = useState('')
  const [browserStartURL, setBrowserStartURL] = useState('')
  const [browserCaptureHAR, setBrowserCaptureHAR] = useState(true)
  const [browserDevice, setBrowserDevice] = useState('')
  const [browserDevices, setBrowserDevices] = useState<{ name: string; platform: string }[]>([])
  const [browserScreenshots, setBrowserScreenshots] = useState(false)
  const [browserWSPayloads, setBrowserWSPayloads] = useState(false)
  const [browserStorageState, setBrowserStorageState] = useState('')
  const [browserExcludeHosts, setBrowserExcludeHosts] = useState('')
  const [browserOptionsOpen, setBrowserOptionsOpen] = useState(false)
  const [installState, setInstallState] = useState<{ status: string; error?: string } | null>(null)
  const [showProxyAdvanced, setShowProxyAdvanced] = useState(false)
  const convertTask = useAsyncTask<RecorderImportResult>()

  const proxyPort = recorderProxyPort(proxyAddr)

  const handleCopyCAPath = async () => {
    if (!caCertPath) return
    try {
      if (!navigator.clipboard?.writeText) {
        throw new Error('Clipboard access is not available in this browser')
      }
      await navigator.clipboard.writeText(caCertPath)
      showFeedback('CA certificate path copied')
    } catch (e: any) {
      showFeedback(e.message || 'Failed to copy CA certificate path', 'error')
    }
  }

  const handleDownloadCA = async (format: 'crt' | 'pem' = 'crt') => {
    try {
      const url = format === 'pem' ? '/recorder/ca.pem?format=pem' : '/recorder/ca.pem'
      const res = await apiFetch(url)
      if (!res.ok) throw new Error('Failed to download CA certificate')
      const blob = await res.blob()
      const filename = format === 'pem' ? 'apexload-ca.pem' : 'apexload-ca.crt'
      downloadBlob(blob, filename)
    } catch (e: any) {
      showFeedback(e.message || 'Failed to download CA certificate', 'error')
    }
  }

  const copyProxyCommand = async (command: string, successMessage: string, failureMessage: string) => {
    try {
      if (!navigator.clipboard?.writeText) {
        throw new Error('Clipboard access is not available in this browser')
      }
      await navigator.clipboard.writeText(command)
      showFeedback(successMessage)
    } catch (e: any) {
      showFeedback(e.message || failureMessage, 'error')
    }
  }

  const handleCopyMacOSProxyCommand = async (mode: 'start' | 'restore') => {
    const command = mode === 'start'
      ? buildMacOSStartProxyCommand(proxyPort, macOSNetworkService)
      : buildMacOSRestoreProxyCommand(macOSNetworkService)
    await copyProxyCommand(
      command,
      mode === 'start' ? 'macOS start proxy command copied' : 'macOS restore proxy command copied',
      'Failed to copy macOS proxy command',
    )
  }

  const handleCopyWindowsProxyCommand = async (mode: 'start' | 'restore') => {
    const command = mode === 'start'
      ? buildWindowsStartProxyCommand(proxyPort)
      : buildWindowsRestoreProxyCommand()
    await copyProxyCommand(
      command,
      mode === 'start' ? 'Windows start proxy command copied' : 'Windows restore proxy command copied',
      'Failed to copy Windows proxy command',
    )
  }

  const handleOpenMacOSNetworkSettings = () => {
    window.location.href = 'x-apple.systempreferences:com.apple.Network-Settings.extension'
  }

  const handleOpenWindowsProxySettings = () => {
    window.location.href = 'ms-settings:network-proxy'
  }

  const showFeedback = (msg: string, type: 'success' | 'error' = 'success') => {
    setFeedback(msg)
    setFeedbackType(type)
    setTimeout(() => setFeedback(''), 4000)
  }

  const refresh = useCallback(async () => {
    try {
      const [listRes, statusRes] = await Promise.all([
        api.listRecordings(activeProject?.id),
        api.getRecorderStatus(),
      ])
      const recorderStatus = statusRes as RecorderStatus
      setSessions(listRes.sessions || [])
      setProxyAddr(recorderStatus.proxy_addr || '')
      setCaCertPath(recorderStatus.ca_cert_path || '')
      setBrowserAvailability(recorderStatus.browser?.availability ?? null)
      if (recorderStatus.browser?.install) {
        setInstallState(recorderStatus.browser.install)
      }
      const liveBrowser = recorderStatus.browser?.active
      if (liveBrowser?.id) {
        // Adopt a recording started elsewhere (popup, another tab, reload).
        setBrowserPanelSessionId(current => current ?? liveBrowser.id)
      }
      if (recorderStatus.active) {
        const full = await api.getRecording(recorderStatus.active.id)
        setActiveSession(full)
      } else {
        setActiveSession(null)
      }
    } catch { /* API not ready */ }
  }, [api, activeProject?.id])

  useEffect(() => {
    refresh()
    const interval = setInterval(refresh, 2000)
    return () => clearInterval(interval)
  }, [refresh])

  useEffect(() => {
    if (!selectedRequest) return
    setRequestDraft({
      step_name: selectedRequest.step_name || '',
      transaction_name: selectedRequest.transaction_name || '',
      note: selectedRequest.note || '',
      think_time_ms: selectedRequest.think_time_ms || 0,
      excluded: !!selectedRequest.excluded,
    })
  }, [selectedRequest])

  // Surface the proxy fallback when headed browser recording cannot run here.
  useEffect(() => {
    if (browserAvailability && !browserAvailability.available) {
      setShowProxyAdvanced(true)
    }
  }, [browserAvailability])

  // Load device presets once for the launcher.
  useEffect(() => {
    api.listBrowserRecorderDevices()
      .then(res => setBrowserDevices(res.devices || []))
      .catch(() => { /* devices are optional */ })
  }, [api])

  const handleInstallBrowsers = async () => {
    try {
      const res = await api.installBrowserRecorder()
      setInstallState(res.install)
      showFeedback('Installing the recording browser — this can take a few minutes')
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleStartBrowserRecording = async () => {
    const startURL = browserStartURL.trim()
    if (!/^https?:\/\//i.test(startURL)) {
      showFeedback('Enter an absolute http(s) URL to record', 'error')
      return
    }
    try {
      const res = await api.startBrowserRecording({
        name: browserName.trim() || 'Browser Recording',
        start_url: startURL,
        project_id: activeProject?.id,
        capture_har: browserCaptureHAR,
        device_preset: browserDevice || undefined,
        capture_screenshots: browserScreenshots,
        capture_ws_payloads: browserWSPayloads,
        storage_state_path: browserStorageState.trim() || undefined,
        exclude_hosts: browserExcludeHosts.trim()
          ? browserExcludeHosts.split(',').map(h => h.trim()).filter(Boolean)
          : undefined,
      })
      const sessionId = res.session.id
      setBrowserPanelSessionId(sessionId)
      showFeedback('Recording browser launched — interact with the page, then stop from the control panel')
      if (!isWailsShell()) {
        // Companion popup next to the recording browser; the docked panel
        // below stays as the fallback when the popup is blocked.
        window.open(
          `#/recorder-panel?session=${encodeURIComponent(sessionId)}`,
          'apexload-browser-recorder',
          'width=440,height=760',
        )
      }
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleStart = async () => {
    try {
      const data: any = {
        name: sessionName || 'Recording Session',
        project_id: activeProject?.id,
        exclude_static: true,
        capture_cookies: captureCookies,
      }
      if (includeHosts.trim()) {
        data.include_hosts = includeHosts.split(',').map((h: string) => h.trim()).filter(Boolean)
      }
      if (excludeHosts.trim()) {
        data.exclude_hosts = excludeHosts.split(',').map((h: string) => h.trim()).filter(Boolean)
      }
      const res = await api.startRecording(data)
      setActiveSession(res.session)
      showFeedback('Recording started — configure your browser proxy and navigate the target site')
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleStop = async () => {
    if (!activeSession) return
    try {
      await api.stopRecording(activeSession.id)
      showFeedback(`Recording stopped — ${activeSession.requests?.length || 0} requests captured`)
      setActiveSession(null)
      refresh()
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handlePauseResume = async () => {
    if (!activeSession) return
    try {
      const next = activeSession.status === 'paused'
        ? await api.resumeRecording(activeSession.id)
        : await api.pauseRecording(activeSession.id)
      setActiveSession(next)
      showFeedback(next.status === 'paused' ? 'Recording paused' : 'Recording resumed')
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const replaceRequest = (sessionId: string, updated: RecordedRequest) => {
    const apply = (session: RecordingSession | null) => {
      if (!session || session.id !== sessionId) return session
      return {
        ...session,
        requests: session.requests.map(req => req.seq === updated.seq ? updated : req),
      }
    }
    setSelectedSession(apply)
    setActiveSession(apply)
    setSessions(prev => prev.map(session => apply(session) || session))
    setSelectedRequest(current => current?.seq === updated.seq ? updated : current)
  }

  const handleToggleRequestExcluded = async (session: RecordingSession, req: RecordedRequest) => {
    try {
      const updated = await api.updateRecordedRequest(session.id, req.seq, { excluded: !req.excluded })
      replaceRequest(session.id, updated)
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleSaveRequestDetails = async () => {
    if (!selectedSession || !selectedRequest) return
    try {
      const updated = await api.updateRecordedRequest(selectedSession.id, selectedRequest.seq, requestDraft)
      replaceRequest(selectedSession.id, updated)
      showFeedback(`Request #${updated.seq} updated`)
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handlePreviewConversion = async (session: RecordingSession) => {
    try {
      const result = await convertTask.run(
        () => api.previewRecordingConversion(session.id, session.name, parameterizeInputs),
        { clearValue: true },
      )
      showFeedback(`Preview generated with ${result.script?.actions?.length || 0} actions and ${result.correlations?.length || 0} correlations`)
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleExportHAR = async (session: RecordingSession) => {
    try {
      const res = await apiFetch(`/recorder/${session.id}/export?format=har`)
      if (!res.ok) {
        const payload = await res.json().catch(() => ({ error: 'Failed to export HAR' }))
        throw new Error(payload.error || 'Failed to export HAR')
      }
      const blob = await res.blob()
      const safeName = (session.name || 'recording').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')
      downloadBlob(blob, `${safeName || 'recording'}.har`)
      showFeedback(`Exported "${session.name}" as HAR`)
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleConvert = async (session: RecordingSession) => {
    try {
      const result = await convertTask.run(
        () => api.convertRecording(session.id, session.name, parameterizeInputs),
        { clearValue: true },
      )
      showFeedback(`Script "${result.script?.name || session.name}" created with ${result.correlations?.length || 0} correlations detected`)
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const buildReviewedScript = (): ScriptDefinition | null => {
    const script = convertTask.value?.script
    if (!script) {
      return null
    }
    return attachImportCorrelationSuggestions(script as ScriptDefinition, 'recorder', convertTask.value?.correlations)
  }

  const handleEditConvertedScript = () => {
    const script = buildReviewedScript()
    if (!script) {
      return
    }
    onEdit?.(script)
  }

  const handleSaveConvertedScript = async () => {
    const script = buildReviewedScript()
    if (!script) {
      return
    }
    try {
      const savedScript = await api.createScript(script, activeProject?.id)
      showFeedback(`Script "${script.name}" created with ${convertTask.value?.correlations?.length || 0} correlations detected`)
      onImported?.({ scripts: [withFallbackPersistedScript(savedScript, script)] })
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const handleDelete = async (id: string) => {
    try {
      await api.deleteRecording(id)
      if (selectedSession?.id === id) setSelectedSession(null)
      refresh()
    } catch (e: any) {
      showFeedback(e.message, 'error')
    }
  }

  const stoppedSessions = sessions.filter(s => s.status === 'stopped')
  const convertResult = convertTask.value
  const convertedScript = convertResult?.script
  const converting = convertTask.pending

  return (
    <div className="page recording-studio">
      <div className="page-header">
        <div>
          <h2>Recording Studio</h2>
          <p className="page-subtitle">Record a browser session and convert it to a test script</p>
        </div>
      </div>

      {feedback && (
        <div className={`rec-feedback ${feedbackType}`}>{feedback}</div>
      )}

      {/* Primary: headed browser recording on this controller */}
      <div className="rec-controls-card rec-browser-launcher">
        <h3 className="rec-section-title">Record Browser Session</h3>
        {browserAvailability && !browserAvailability.available ? (
          <div className="rec-browser-unavailable" data-testid="browser-recorder-unavailable">
            Browser recording is not available on this controller
            {browserAvailability.reason ? `: ${browserAvailability.reason}` : '.'}{' '}
            Use the Advanced proxy recorder below instead.
            {installState && installState.status !== 'idle' && (
              <div className="rec-install-status">Recording browser install: {installState.status}{installState.error ? ` — ${installState.error}` : ''}</div>
            )}
            <button className="btn btn-secondary btn-sm" onClick={handleInstallBrowsers} disabled={installState?.status === 'installing'}>
              {installState?.status === 'installing' ? 'Installing…' : 'Install Recording Browser'}
            </button>
          </div>
        ) : (
          <div className="rec-start-form">
            <div className="rec-form-row">
              <label className="rec-label">Start URL</label>
              <input
                className="rec-input"
                value={browserStartURL}
                onChange={e => setBrowserStartURL(e.target.value)}
                placeholder="https://app.example.com/login"
              />
            </div>
            <div className="rec-form-row">
              <label className="rec-label">Session Name</label>
              <input
                className="rec-input"
                value={browserName}
                onChange={e => setBrowserName(e.target.value)}
                placeholder="e.g. Login & Checkout Flow"
              />
            </div>
            <div className="rec-form-row">
              <label className="rec-label">Device</label>
              <select className="rec-input" value={browserDevice} onChange={e => setBrowserDevice(e.target.value)} aria-label="Device preset">
                <option value="">Desktop (default)</option>
                {browserDevices.filter(d => d.name !== 'desktop_1080p').map(d => (
                  <option key={d.name} value={d.name}>{d.name.replace(/_/g, ' ')} ({d.platform})</option>
                ))}
              </select>
            </div>
            <label className="rec-checkbox-row">
              <input
                type="checkbox"
                checked={browserCaptureHAR}
                onChange={e => setBrowserCaptureHAR(e.target.checked)}
              />
              <span>Capture side-car HAR (network traffic for protocol-level scripting)</span>
            </label>

            <button type="button" className="rec-inline-action" onClick={() => setBrowserOptionsOpen(v => !v)}>
              {browserOptionsOpen ? 'Hide advanced options' : 'Advanced options'}
            </button>
            {browserOptionsOpen && (
              <div className="rec-browser-options">
                <label className="rec-checkbox-row">
                  <input type="checkbox" checked={browserScreenshots} onChange={e => setBrowserScreenshots(e.target.checked)} />
                  <span>Capture per-step screenshot thumbnails</span>
                </label>
                <label className="rec-checkbox-row">
                  <input type="checkbox" checked={browserWSPayloads} onChange={e => setBrowserWSPayloads(e.target.checked)} />
                  <span>Capture WebSocket frame payloads (scrubbed)</span>
                </label>
                <div className="rec-form-row">
                  <label className="rec-label">Exclude hosts <span className="rec-hint">(comma-separated, HAR noise filter)</span></label>
                  <input className="rec-input" value={browserExcludeHosts} onChange={e => setBrowserExcludeHosts(e.target.value)} placeholder="analytics.google.com, cdn.example.com" />
                </div>
                <div className="rec-form-row">
                  <label className="rec-label">Reuse session state <span className="rec-hint">(saved storage-state path, skip login)</span></label>
                  <input className="rec-input" value={browserStorageState} onChange={e => setBrowserStorageState(e.target.value)} placeholder="/path/to/state.json" />
                </div>
              </div>
            )}

            <button
              className="btn btn-primary rec-start-btn"
              onClick={handleStartBrowserRecording}
              disabled={!!browserPanelSessionId}
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><circle cx="12" cy="12" r="10" /></svg>
              Record Browser
            </button>
            <div className="rec-browser-unavailable">
              A Chromium window opens on this machine with an in-page recording toolbar (pause and verification picker).
              Manage the session from the control panel docked below — it also opens as a compact popup window.
            </div>
          </div>
        )}
      </div>

      {browserPanelSessionId && (
        <BrowserRecorderControlPanel
          sessionId={browserPanelSessionId}
          onImported={onImported}
          onEdit={onEdit}
          onDismiss={() => {
            setBrowserPanelSessionId(null)
            refresh()
          }}
        />
      )}

      {/* Advanced: network-level proxy/HAR recorder (works with remote controllers) */}
      <div className="rec-advanced-section">
        <div
          className="rec-advanced-toggle"
          data-testid="rec-advanced-toggle"
          onClick={() => setShowProxyAdvanced(prev => !prev)}
        >
          <span>
            Advanced: Proxy Recorder
            <span className="rec-advanced-sub">network-level HTTP(S) capture through the local proxy</span>
          </span>
          <svg
            className={`rec-chevron ${showProxyAdvanced ? 'open' : ''}`}
            width="16" height="16" viewBox="0 0 24 24" fill="none"
            stroke="var(--text-muted)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"
          >
            <polyline points="6 9 12 15 18 9" />
          </svg>
        </div>
        {showProxyAdvanced && (
          <>

      {/* Proxy Setup */}
      <div className="rec-setup-section">
        <div className="rec-setup-toggle" onClick={() => setShowSetup(!showSetup)}>
          <div className="rec-proxy-header">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="var(--accent)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="12" cy="12" r="10" />
              <line x1="12" y1="16" x2="12" y2="12" />
              <line x1="12" y1="8" x2="12.01" y2="8" />
            </svg>
            <span>Proxy Setup</span>
          </div>
          <svg
            className={`rec-chevron ${showSetup ? 'open' : ''}`}
            width="16" height="16" viewBox="0 0 24 24" fill="none"
            stroke="var(--text-muted)" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"
          >
            <polyline points="6 9 12 15 18 9" />
          </svg>
        </div>

        {showSetup && (
          <div className="rec-setup-steps">
            <div className="rec-step">
              <div className="rec-step-number">1</div>
              <div className="rec-step-content">
                <div className="rec-step-title">Download &amp; Install CA Certificate</div>
                <div className="rec-step-desc">
                  Install and trust the APEX Load CA certificate for SSL/TLS so the proxy can intercept HTTPS traffic.
                </div>
                <div className="rec-step-note">
                  Only trust this private recording CA on a workstation you control. The recorder reuses the same CA across controller restarts, so install it again only if you delete the CA files or change the recorder CA directory.
                  {caCertPath && (
                    <div className="rec-step-note-path">
                      <span>Current certificate path:</span>
                      <code>{caCertPath}</code>
                      <button type="button" className="rec-inline-action" onClick={handleCopyCAPath}>Copy path</button>
                    </div>
                  )}
                  <div className="rec-step-note-troubleshoot">
                    If the browser still reports <code>ERR_CERT_AUTHORITY_INVALID</code>, the CA is imported but not trusted for SSL/TLS, the browser is using a separate trust store, or an older APEX Load CA is still selected.
                  </div>
                </div>
                <div className="rec-download-row">
                  <PageViewToggle
                    value={certificateFormatView}
                    ariaLabel="Certificate format"
                    className="rec-certificate-format-toggle script-page-toggle"
                    onChange={value => setCertificateFormatView(value)}
                    options={[
                      { value: 'crt', label: 'CRT' },
                      { value: 'pem', label: 'PEM' },
                    ]}
                  />
                  <button className="rec-download-btn" onClick={() => handleDownloadCA(certificateFormatView)}>
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                      <polyline points="7 10 12 15 17 10" />
                      <line x1="12" y1="15" x2="12" y2="3" />
                    </svg>
                    {certificateFormatView === 'crt' ? 'Download CA Certificate (.crt)' : 'Download CA Certificate (.pem)'}
                  </button>
                </div>
                <div className="rec-os-instructions">
                  {certificateFormatView === 'crt' ? (
                    <>
                      <div><span className="rec-os-label">macOS Chrome, Edge, Safari:</span> Download .crt &rarr; open Keychain Access &rarr; import into the System keychain &rarr; open &ldquo;APEX Load Recording Proxy CA&rdquo; &rarr; expand Trust &rarr; set &ldquo;When using this certificate&rdquo; or &ldquo;Secure Sockets Layer (SSL)&rdquo; to Always Trust &rarr; close the window, enter your password, and restart the browser.</div>
                      <div><span className="rec-os-label">Windows Chrome, Edge:</span> Download .crt &rarr; Install Certificate &rarr; Local Machine &rarr; place it in &ldquo;Trusted Root Certification Authorities&rdquo; &rarr; finish the wizard and restart the browser.</div>
                    </>
                  ) : (
                    <>
                      <div><span className="rec-os-label">Firefox:</span> Download PEM format &rarr; Settings &rarr; Privacy &amp; Security &rarr; Certificates &rarr; View Certificates &rarr; Authorities &rarr; Import &rarr; enable &ldquo;Trust this CA to identify websites&rdquo; &rarr; restart Firefox. If Firefox is configured to use system roots, restart after trusting the OS root instead.</div>
                      <div><span className="rec-os-label">Linux:</span> Download PEM format and add it to the browser or distro trust store used by your browser, then restart the browser.</div>
                    </>
                  )}
                  <div><span className="rec-os-label">Cleanup:</span> Remove older &ldquo;APEX Load Recording Proxy CA&rdquo; entries if more than one exists, then trust the current CA shown above.</div>
                </div>
              </div>
            </div>

            <div className="rec-step">
              <div className="rec-step-number">2</div>
              <div className="rec-step-content">
                <div className="rec-step-title">Configure Browser Proxy</div>
                <div className="rec-step-desc">
                  Route browser traffic through <code>localhost:{proxyPort}</code>
                </div>
                <div className="rec-os-instructions">
                  <div>Use a browser extension like SwitchyOmega (Chrome) or FoxyProxy (Firefox) to route traffic through the proxy.</div>
                  <div><span className="rec-os-label">System proxy:</span> macOS System Settings &rarr; Network &rarr; Wi-Fi &rarr; Details &rarr; Proxies &rarr; Web Proxy (HTTP) &amp; Secure Web Proxy (HTTPS) &rarr; <code>localhost</code> port <code>{proxyPort}</code></div>
                  <div><span className="rec-os-label">Windows 11:</span> Settings &rarr; Network &amp; internet &rarr; Proxy &rarr; Manual proxy setup &rarr; <code>localhost</code> port <code>{proxyPort}</code></div>
                </div>
                <div className="rec-proxy-shortcut">
                  <PageViewToggle
                    value={proxyPlatformView}
                    ariaLabel="Proxy command platform"
                    className="rec-proxy-platform-toggle script-page-toggle"
                    onChange={value => setProxyPlatformView(value)}
                    options={[
                      { value: 'macos', label: 'macOS' },
                      { value: 'windows', label: 'Windows 11' },
                    ]}
                  />
                  {proxyPlatformView === 'macos' ? (
                    <div className="rec-proxy-platform">
                      <div className="rec-proxy-platform-title">macOS</div>
                      <label className="rec-proxy-service">
                        <span>Network service</span>
                        <input
                          className="rec-input"
                          aria-label="macOS network service"
                          value={macOSNetworkService}
                          onChange={e => setMacOSNetworkService(e.target.value)}
                        />
                      </label>
                      <div className="rec-shortcut-actions">
                        <button className="rec-download-alt" type="button" onClick={() => handleCopyMacOSProxyCommand('start')}>
                          Copy Start Proxy Command
                        </button>
                        <button className="rec-download-alt" type="button" onClick={() => handleCopyMacOSProxyCommand('restore')}>
                          Copy Restore Proxy Command
                        </button>
                        <button className="rec-download-alt" type="button" onClick={handleOpenMacOSNetworkSettings}>
                          Open Network Settings
                        </button>
                      </div>
                    </div>
                  ) : (
                    <div className="rec-proxy-platform">
                      <div className="rec-proxy-platform-title">Windows 11</div>
                      <div className="rec-shortcut-actions">
                        <button className="rec-download-alt" type="button" onClick={() => handleCopyWindowsProxyCommand('start')}>
                          Copy Windows Start Command
                        </button>
                        <button className="rec-download-alt" type="button" onClick={() => handleCopyWindowsProxyCommand('restore')}>
                          Copy Windows Restore Command
                        </button>
                        <button className="rec-download-alt" type="button" onClick={handleOpenWindowsProxySettings}>
                          Open Windows Proxy Settings
                        </button>
                      </div>
                    </div>
                  )}
                </div>
              </div>
            </div>

            <div className="rec-step">
              <div className="rec-step-number">3</div>
              <div className="rec-step-content">
                <div className="rec-step-title">Start Recording</div>
                <div className="rec-step-desc">
                  Click &ldquo;Start Recording&rdquo; below, then browse the target website. All HTTP and HTTPS traffic will be captured.
                </div>
              </div>
            </div>

            <div className="rec-step">
              <div className="rec-step-number">4</div>
              <div className="rec-step-content">
                <div className="rec-step-title">Stop &amp; Convert</div>
                <div className="rec-step-desc">
                  When done, stop the recording and convert to a test script. Remember to disable the proxy.
                </div>
              </div>
            </div>
          </div>
        )}
      </div>

      {/* Recording Controls */}
      <div className="rec-controls-card">
        <h3 className="rec-section-title">
          {activeSession ? (
            <>
              <span className="rec-live-dot" />
              {activeSession.status === 'paused' ? 'Paused' : 'Recording'}: {activeSession.name}
            </>
          ) : (
            'New Recording'
          )}
        </h3>

        {!activeSession ? (
          <div className="rec-start-form">
            <div className="rec-form-row">
              <label className="rec-label">Session Name</label>
              <input
                className="rec-input"
                value={sessionName}
                onChange={e => setSessionName(e.target.value)}
                placeholder="e.g. Login & Checkout Flow"
              />
            </div>
            <div className="rec-form-row">
              <label className="rec-label">Include Hosts <span className="rec-hint">(comma-separated, optional)</span></label>
              <input
                className="rec-input"
                value={includeHosts}
                onChange={e => setIncludeHosts(e.target.value)}
                placeholder="e.g. api.example.com, app.example.com"
              />
            </div>
            <div className="rec-form-row">
              <label className="rec-label">Exclude Hosts <span className="rec-hint">(comma-separated, optional)</span></label>
              <input
                className="rec-input"
                value={excludeHosts}
                onChange={e => setExcludeHosts(e.target.value)}
                placeholder="e.g. analytics.google.com, cdn.example.com"
              />
            </div>
            <label className="rec-checkbox-row">
              <input
                type="checkbox"
                checked={captureCookies}
                onChange={e => setCaptureCookies(e.target.checked)}
              />
              <span>Capture cookies (may include secrets)</span>
            </label>
            <button className="btn btn-primary rec-start-btn" onClick={handleStart}>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><circle cx="12" cy="12" r="10" /></svg>
              Start Recording
            </button>
          </div>
        ) : (
          <div className="rec-active-panel">
            <div className="rec-active-stats">
              <div className="rec-stat">
                <span className="rec-stat-value">{activeSession.requests?.length || 0}</span>
                <span className="rec-stat-label">Requests</span>
              </div>
              <div className="rec-stat">
                <span className="rec-stat-value">{activeSession.base_url || '—'}</span>
                <span className="rec-stat-label">Base URL</span>
              </div>
              <div className="rec-stat">
                <span className="rec-stat-value">{new Date(activeSession.started_at).toLocaleTimeString()}</span>
                <span className="rec-stat-label">Started</span>
              </div>
            </div>

            {/* Live request feed */}
            {activeSession.requests && activeSession.requests.length > 0 && (
              <div className="rec-live-feed">
                <div className="rec-feed-header">
                  <span>Live Requests</span>
                  <span className="rec-feed-count">{activeSession.requests.length}</span>
                </div>
                <div className="rec-feed-list">
                  {[...activeSession.requests].reverse().slice(0, 20).map(req => (
                    <div key={req.seq} className="rec-feed-item">
                      <span className={`rec-method ${req.method.toLowerCase()}`}>{req.method}</span>
                      <span className="rec-path" title={req.url}>{req.path}</span>
                      <span className={`rec-status ${req.status_code >= 400 ? 'error' : ''}`}>{req.status_code}</span>
                      <span className="rec-timing">{req.response_time_ms.toFixed(0)}ms</span>
                    </div>
                  ))}
                </div>
              </div>
            )}

            <button className="btn btn-danger rec-stop-btn" onClick={handleStop}>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><rect x="4" y="4" width="16" height="16" rx="2" /></svg>
              Stop Recording
            </button>
            <button className="btn btn-secondary rec-stop-btn" onClick={handlePauseResume}>
              {activeSession.status === 'paused' ? 'Resume Recording' : 'Pause Recording'}
            </button>
            <button className="btn btn-secondary rec-stop-btn" type="button" onClick={() => handleCopyMacOSProxyCommand('restore')}>
              Copy Restore Proxy Command
            </button>
            <button className="btn btn-secondary rec-stop-btn" type="button" onClick={() => handleCopyWindowsProxyCommand('restore')}>
              Copy Windows Restore Command
            </button>
          </div>
        )}
      </div>

      {/* Previous Sessions */}
      {stoppedSessions.length > 0 && (
        <div className="rec-sessions-section">
          <h3 className="rec-section-title">Recorded Sessions</h3>
          <label className="rec-convert-option">
            <input
              type="checkbox"
              checked={parameterizeInputs}
              onChange={e => setParameterizeInputs(e.target.checked)}
            />
            Parameterize input values (promote recorded form/query inputs into a data source)
          </label>
          <div className="rec-sessions-list">
            {stoppedSessions.map(session => (
              <div
                key={session.id}
                className={`rec-session-card ${selectedSession?.id === session.id ? 'selected' : ''}`}
                onClick={() => {
                  setSelectedSession(selectedSession?.id === session.id ? null : session)
                  setSelectedRequest(null)
                }}
              >
                <div className="rec-session-header">
                  <span className="rec-session-name">{session.name}</span>
                  <span className="rec-session-count">{session.requests?.length || 0} requests</span>
                </div>
                <div className="rec-session-meta">
                  <span>{session.base_url || 'No base URL'}</span>
                  <span>{new Date(session.started_at).toLocaleString()}</span>
                </div>
                <div className="rec-session-actions">
                  <button
                    className="btn btn-sm btn-primary"
                    onClick={(e) => { e.stopPropagation(); handleConvert(session) }}
                    disabled={converting}
                  >
                    {converting ? 'Converting...' : 'Convert to Script'}
                  </button>
                  <button
                    className="btn btn-sm btn-secondary"
                    onClick={(e) => { e.stopPropagation(); handlePreviewConversion(session) }}
                    disabled={converting}
                  >
                    Review Script
                  </button>
                  <button
                    className="btn btn-sm btn-secondary"
                    onClick={(e) => { e.stopPropagation(); handleExportHAR(session) }}
                  >
                    Export HAR
                  </button>
                  <button
                    className="btn btn-sm btn-secondary"
                    style={{ color: 'var(--danger)' }}
                    onClick={(e) => { e.stopPropagation(); handleDelete(session.id) }}
                  >
                    Delete
                  </button>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Selected session detail */}
      {selectedSession && (
        <div className="rec-detail-section">
          <h3 className="rec-section-title">
            Request Detail: {selectedSession.name}
            <span className="rec-detail-count">{selectedSession.requests?.length || 0} requests</span>
          </h3>
          <div className="rec-detail-table-wrap">
            <table className="rec-detail-table">
              <thead>
                <tr>
                  <th>#</th>
                  <th>Method</th>
                  <th>Path</th>
                  <th>Status</th>
                  <th>Time</th>
                  <th>Think</th>
                  <th>Type</th>
                  <th>Size</th>
                  <th>Use</th>
                </tr>
              </thead>
              <tbody>
                {selectedSession.requests?.map(req => (
                  <tr
                    key={req.seq}
                    className={`${req.is_static ? 'static' : ''} ${req.excluded ? 'static' : ''} ${selectedRequest?.seq === req.seq ? 'selected' : ''}`}
                    onClick={() => setSelectedRequest(req)}
                  >
                    <td className="cell-mono">{req.seq}</td>
                    <td><span className={`rec-method ${req.method.toLowerCase()}`}>{req.method}</span></td>
                    <td className="cell-path" title={req.url}>{req.step_name || req.path}{req.query_string ? `?${req.query_string}` : ''}</td>
                    <td className={req.status_code >= 400 ? 'text-danger' : ''}>{req.status_code}</td>
                    <td className="cell-mono">{req.response_time_ms.toFixed(0)}ms</td>
                    <td className="cell-mono">{req.think_time_ms > 0 ? `${req.think_time_ms}ms` : '—'}</td>
                    <td className="cell-muted">{req.resource_type || (req.content_type ? req.content_type.split(';')[0] : '—')}</td>
                    <td className="cell-mono">
                      {(req.body_size_bytes || 0) + (req.response_body_size_bytes || 0)}B
                      {(req.body_truncated || req.response_body_truncated) ? '*' : ''}
                    </td>
                    <td>
                      <button
                        className="btn btn-sm btn-secondary"
                        onClick={(e) => { e.stopPropagation(); handleToggleRequestExcluded(selectedSession, req) }}
                      >
                        {req.excluded ? 'Include' : 'Exclude'}
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {selectedRequest && (
            <div className="rec-request-editor">
              <div className="rec-editor-header">
                <div>
                  <span className={`rec-method ${selectedRequest.method.toLowerCase()}`}>{selectedRequest.method}</span>
                  <code>{selectedRequest.path}{selectedRequest.query_string ? `?${selectedRequest.query_string}` : ''}</code>
                </div>
                <span className="cell-mono">#{selectedRequest.seq}</span>
              </div>
              <div className="rec-editor-grid">
                <label className="rec-form-row">
                  <span className="rec-label">Step Name</span>
                  <input
                    className="rec-input"
                    value={requestDraft.step_name}
                    onChange={e => setRequestDraft(prev => ({ ...prev, step_name: e.target.value }))}
                    placeholder={`${selectedRequest.method} ${selectedRequest.path}`}
                  />
                </label>
                <label className="rec-form-row">
                  <span className="rec-label">Transaction</span>
                  <input
                    className="rec-input"
                    value={requestDraft.transaction_name}
                    onChange={e => setRequestDraft(prev => ({ ...prev, transaction_name: e.target.value }))}
                    placeholder="e.g. Login, Browse Catalog, Checkout"
                  />
                </label>
                <label className="rec-form-row">
                  <span className="rec-label">Think Time (ms)</span>
                  <input
                    className="rec-input"
                    type="number"
                    min="0"
                    value={requestDraft.think_time_ms}
                    onChange={e => setRequestDraft(prev => ({ ...prev, think_time_ms: Math.max(0, Number(e.target.value) || 0) }))}
                  />
                </label>
                <label className="rec-editor-check">
                  <input
                    type="checkbox"
                    checked={!requestDraft.excluded}
                    onChange={e => setRequestDraft(prev => ({ ...prev, excluded: !e.target.checked }))}
                  />
                  Use in generated script
                </label>
              </div>
              <label className="rec-form-row">
                <span className="rec-label">Note</span>
                <textarea
                  className="rec-input rec-textarea"
                  value={requestDraft.note}
                  onChange={e => setRequestDraft(prev => ({ ...prev, note: e.target.value }))}
                  placeholder="Capture intent, manual correlation notes, or cleanup reminders"
                />
              </label>
              <div className="rec-editor-actions">
                <button className="btn btn-primary btn-sm" onClick={handleSaveRequestDetails}>Save Request</button>
                <button className="btn btn-secondary btn-sm" onClick={() => setSelectedRequest(null)}>Close</button>
              </div>
            </div>
          )}
        </div>
      )}

      {/* Convert Result */}
      {convertResult && convertedScript && (
        <div className="rec-convert-result">
          <h3 className="rec-section-title">Conversion Result</h3>
          <div className="rec-result-summary">
            <div className="rec-result-item">
              <span className="rec-result-label">Script</span>
              <span className="rec-result-value">{convertedScript.name}</span>
            </div>
            <div className="rec-result-item">
              <span className="rec-result-label">Protocol</span>
              <span className="rec-result-value">{convertedScript.protocol}</span>
            </div>
            <div className="rec-result-item">
              <span className="rec-result-label">Script ID</span>
              <code className="rec-result-value">{convertedScript.id || 'preview'}</code>
            </div>
          </div>
          <div className="rec-result-actions">
            <button className="btn btn-primary" type="button" onClick={handleSaveConvertedScript}>Save To Script Library</button>
            {onEdit && <button className="btn btn-secondary" type="button" onClick={handleEditConvertedScript}>Open In Visual Editor</button>}
          </div>

          {convertResult.correlations && convertResult.correlations.length > 0 && (
            <div className="rec-correlations">
              <h4 className="rec-subsection-title">Auto-Detected Correlations ({convertResult.correlations.length})</h4>
              <div className="rec-corr-list">
                {convertResult.correlations.map((c, i) => (
                  <div key={i} className="rec-corr-item">
                    <div className="rec-corr-header">
                      <code className="rec-corr-name">{`{{var.${c.name}}}`}</code>
                      {confidenceTone(c.confidence) && (
                        <span
                          className={`rec-corr-confidence tone-${confidenceTone(c.confidence)}`}
                          title={c.rule_id ? `rule: ${c.rule_id}` : undefined}
                        >
                          {Math.round((c.confidence as number) * 100)}% confidence
                        </span>
                      )}
                      <span className="rec-corr-type">{c.extract_type} / {c.source}</span>
                    </div>
                    <div className="rec-corr-detail">
                      <span>Extracted from request #{c.found_in_seq}</span>
                      <span>Used in #{c.used_in_seqs.join(', #')}</span>
                    </div>
                    {c.reasons && c.reasons.length > 0 && (
                      <div className="rec-corr-reasons">Why: {c.reasons.join('; ')}</div>
                    )}
                    <div className="rec-corr-sample">
                      Sample: <code>{c.sample_value}</code>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}

          {convertResult.warnings && convertResult.warnings.length > 0 && (
            <div className="rec-warnings">
              {convertResult.warnings.map((w, i) => (
                <div key={i} className="rec-warning-item">{w}</div>
              ))}
            </div>
          )}
        </div>
      )}
          </>
        )}
      </div>
    </div>
  )
}
