import { useState, useEffect, useCallback } from 'react'
import { downloadBlob } from '../utils/fileUtils'
import { useAsyncTask } from '../hooks/useAsyncTask'
import { useApi } from '../hooks/useApi'
import { BASE } from '../hooks/apiClient'
import { Project } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import { type RecorderCorrelation as Correlation, type ScriptImportResult } from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'
import ScriptImportReview from './ScriptImportReview'
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
  body_size_bytes?: number
  body_truncated?: boolean
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
  }
}

interface StartRecordingRequest {
  name: string
  project_id?: string
  exclude_static: boolean
  include_hosts?: string[]
  exclude_hosts?: string[]
}

type RecorderPreviewResult = ScriptImportResult<Record<string, unknown>, Correlation, ScriptDefinition>

interface Props {
  activeProject?: Project | null
  onImported?: (script: ScriptDefinition) => void
  onEdit?: (script: ScriptDefinition) => void
}

export default function RecordingStudio({ activeProject, onImported, onEdit }: Props) {
  const api = useApi()
  const [sessions, setSessions] = useState<RecordingSession[]>([])
  const [activeSession, setActiveSession] = useState<RecordingSession | null>(null)
  const [proxyAddr, setProxyAddr] = useState('')
  const [caCertPath, setCaCertPath] = useState('')
  const [sessionName, setSessionName] = useState('')
  const [includeHosts, setIncludeHosts] = useState('')
  const [excludeHosts, setExcludeHosts] = useState('')
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
  const convertTask = useAsyncTask<RecorderPreviewResult>()
  const saveTask = useAsyncTask<boolean>()

  const proxyPort = (proxyAddr || ':8081').split(':').pop() || '8081'

  const handleCopyCAPath = async () => {
    if (!caCertPath) return
    try {
      if (!navigator.clipboard?.writeText) {
        throw new Error('Clipboard access is not available in this browser')
      }
      await navigator.clipboard.writeText(caCertPath)
      showFeedback('CA certificate path copied')
    } catch (error: unknown) {
      showFeedback(getErrorMessage(error, 'Failed to copy CA certificate path'), 'error')
    }
  }

  const handleDownloadCA = async (format: 'crt' | 'pem' = 'crt') => {
    try {
      const params = new URLSearchParams()
      if (format === 'pem') params.set('format', 'pem')
      if (activeProject?.id) params.set('project_id', activeProject.id)
      const url = `${BASE}/recorder/ca.pem${params.size ? `?${params.toString()}` : ''}`
      const res = await fetch(url)
      if (!res.ok) throw new Error('Failed to download CA certificate')
      const blob = await res.blob()
      const filename = format === 'pem' ? 'stormrunner-ca.pem' : 'stormrunner-ca.crt'
      downloadBlob(blob, filename)
    } catch (error: unknown) {
      showFeedback(getErrorMessage(error, 'Failed to download CA certificate'), 'error')
    }
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
        api.getRecorderStatus(activeProject?.id),
      ])
      setSessions(listRes.sessions || [])
      setProxyAddr(statusRes.proxy_addr || '')
      setCaCertPath(statusRes.ca_cert_path || '')
      if (statusRes.active) {
        const full = await api.getRecording(statusRes.active.id)
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

  const handleStart = async () => {
    try {
      const data: StartRecordingRequest = {
        name: sessionName || 'Recording Session',
        project_id: activeProject?.id,
        exclude_static: true,
      }
      if (includeHosts.trim()) {
        data.include_hosts = includeHosts.split(',').map(host => host.trim()).filter(Boolean)
      }
      if (excludeHosts.trim()) {
        data.exclude_hosts = excludeHosts.split(',').map(host => host.trim()).filter(Boolean)
      }
      const res = await api.startRecording(data)
      setActiveSession(res.session)
      showFeedback('Recording started — configure your browser proxy and navigate the target site')
    } catch (error: unknown) {
      showFeedback(getErrorMessage(error, 'Failed to start recording'), 'error')
    }
  }

  const handleStop = async () => {
    if (!activeSession) return
    try {
      await api.stopRecording(activeSession.id)
      showFeedback(`Recording stopped — ${activeSession.requests?.length || 0} requests captured`)
      setActiveSession(null)
      refresh()
    } catch (error: unknown) {
      showFeedback(getErrorMessage(error, 'Failed to stop recording'), 'error')
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
    } catch (error: unknown) {
      showFeedback(getErrorMessage(error, 'Failed to update recording state'), 'error')
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
    } catch (error: unknown) {
      showFeedback(getErrorMessage(error, 'Failed to update recorded request'), 'error')
    }
  }

  const handleSaveRequestDetails = async () => {
    if (!selectedSession || !selectedRequest) return
    try {
      const updated = await api.updateRecordedRequest(selectedSession.id, selectedRequest.seq, requestDraft)
      replaceRequest(selectedSession.id, updated)
      showFeedback(`Request #${updated.seq} updated`)
    } catch (error: unknown) {
      showFeedback(getErrorMessage(error, 'Failed to save request details'), 'error')
    }
  }

  const handlePreviewConversion = async (session: RecordingSession) => {
    try {
      const result = await convertTask.run(
        async () => {
          const response = await api.previewRecordingConversion(session.id, session.name)
          return {
            ...response,
            script: response.script as ScriptDefinition | undefined,
            scripts: response.scripts as ScriptDefinition[],
          }
        },
        { clearValue: true },
      )
      const previewScript = result.script ?? result.scripts?.[0]
      showFeedback(`Preview generated with ${previewScript?.actions?.length || 0} actions and ${result.correlations?.length || 0} correlations`)
    } catch (error: unknown) {
      showFeedback(getErrorMessage(error, 'Failed to preview recording conversion'), 'error')
    }
  }

  const handleExportHAR = async (session: RecordingSession) => {
    try {
      const res = await fetch(`${BASE}/recorder/${session.id}/export?format=har`)
      if (!res.ok) {
        const payload = await res.json().catch(() => ({ error: 'Failed to export HAR' }))
        throw new Error(payload.error || 'Failed to export HAR')
      }
      const blob = await res.blob()
      const safeName = (session.name || 'recording').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')
      downloadBlob(blob, `${safeName || 'recording'}.har`)
      showFeedback(`Exported "${session.name}" as HAR`)
    } catch (error: unknown) {
      showFeedback(getErrorMessage(error, 'Failed to export HAR'), 'error')
    }
  }

  const handleSaveConverted = async () => {
    if (!convertScript) return
    try {
      await saveTask.run(async () => {
        await api.createScript(convertScript)
        return true
      })
      showFeedback(`Script "${convertScript.name}" created with ${convertTask.value?.correlations?.length || 0} correlations detected`)
      onImported?.(convertScript)
    } catch (error: unknown) {
      showFeedback(getErrorMessage(error, 'Failed to save generated script'), 'error')
    }
  }

  const handleDelete = async (id: string) => {
    try {
      await api.deleteRecording(id)
      if (selectedSession?.id === id) setSelectedSession(null)
      refresh()
    } catch (error: unknown) {
      showFeedback(getErrorMessage(error, 'Failed to delete recording'), 'error')
    }
  }

  const stoppedSessions = sessions.filter(s => s.status === 'stopped')
  const convertResult = convertTask.value
  const convertScript = convertResult?.script ?? convertResult?.scripts?.[0]
  const converting = convertTask.pending
  const saving = saveTask.pending

  return (
    <div className="page recording-studio">
      <div className="page-header">
        <div>
          <h2>Recording Studio</h2>
          <p className="page-subtitle">Record browser traffic and convert to test scripts</p>
        </div>
      </div>

      {feedback && (
        <div className={`rec-feedback ${feedbackType}`}>{feedback}</div>
      )}

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
                  Install the Stormrunner CA certificate so the proxy can intercept HTTPS traffic.
                </div>
                <div className="rec-step-note">
                  The recorder now reuses the same CA across controller restarts. Trust it once, then only re-download if you delete the CA files or change the recorder CA directory.
                  {caCertPath && (
                    <div className="rec-step-note-path">
                      <span>Current certificate path:</span>
                      <code>{caCertPath}</code>
                      <button type="button" className="rec-inline-action" onClick={handleCopyCAPath}>Copy path</button>
                    </div>
                  )}
                  <div className="rec-step-note-troubleshoot">
                    If the browser still says the connection is not secure, remove older Stormrunner CA entries, trust this CA for SSL/TLS, and restart the browser. Firefox may require explicit import or system-root trust to be enabled.
                  </div>
                </div>
                <div className="rec-download-row">
                  <button className="rec-download-btn" onClick={() => handleDownloadCA('crt')}>
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                      <polyline points="7 10 12 15 17 10" />
                      <line x1="12" y1="15" x2="12" y2="3" />
                    </svg>
                    Download CA Certificate (.crt)
                  </button>
                  <button className="rec-download-alt" onClick={() => handleDownloadCA('pem')}>
                    PEM format (Firefox / Linux)
                  </button>
                </div>
                <div className="rec-os-instructions">
                  <div><span className="rec-os-label">macOS:</span> Double-click the .crt file &rarr; Keychain Access opens &rarr; Find &ldquo;Stormrunner Recording Proxy CA&rdquo; &rarr; Double-click &rarr; Trust &rarr; Always Trust</div>
                  <div><span className="rec-os-label">Windows:</span> Double-click .crt &rarr; Install Certificate &rarr; Local Machine &rarr; Place in &ldquo;Trusted Root Certification Authorities&rdquo;</div>
                  <div><span className="rec-os-label">Firefox:</span> Download PEM format &rarr; Settings &rarr; Privacy &amp; Security &rarr; Certificates &rarr; View &rarr; Import &rarr; Check &ldquo;Trust for websites&rdquo;</div>
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
          </div>
        )}
      </div>

      {/* Previous Sessions */}
      {stoppedSessions.length > 0 && (
        <div className="rec-sessions-section">
          <h3 className="rec-section-title">Recorded Sessions</h3>
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
                    onClick={(e) => { e.stopPropagation(); handlePreviewConversion(session) }}
                    disabled={converting}
                  >
                    {converting ? 'Generating...' : 'Review Script'}
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
      {convertResult && convertScript && (
        <div className="rec-convert-result">
          <ScriptImportReview
            title="Conversion Result"
            subtitle="Review the generated script, warnings, and correlations before saving."
            script={convertScript}
            warnings={convertResult.warnings}
            saving={saving}
            onSave={handleSaveConverted}
            onEdit={onEdit ? () => onEdit(convertScript) : undefined}
          >
            <div className="rec-result-summary">
              <div className="rec-result-item">
                <span className="rec-result-label">Script</span>
                <span className="rec-result-value">{convertScript.name}</span>
              </div>
              <div className="rec-result-item">
                <span className="rec-result-label">Protocol</span>
                <span className="rec-result-value">{convertScript.protocol}</span>
              </div>
              <div className="rec-result-item">
                <span className="rec-result-label">Actions</span>
                <span className="rec-result-value">{convertScript.actions?.length || 0}</span>
              </div>
            </div>

            {convertResult.correlations && convertResult.correlations.length > 0 && (
              <div className="rec-correlations">
                <h4 className="rec-subsection-title">Auto-Detected Correlations ({convertResult.correlations.length})</h4>
                <div className="rec-corr-list">
                  {convertResult.correlations.map((c, i) => (
                    <div key={i} className="rec-corr-item">
                      <div className="rec-corr-header">
                        <code className="rec-corr-name">{`{{var.${c.name}}}`}</code>
                        <span className="rec-corr-type">{c.extract_type} / {c.source}</span>
                      </div>
                      <div className="rec-corr-detail">
                        <span>Extracted from request #{c.found_in_seq}</span>
                        <span>Used in #{c.used_in_seqs.join(', #')}</span>
                      </div>
                      <div className="rec-corr-sample">
                        Sample: <code>{c.sample_value}</code>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </ScriptImportReview>
        </div>
      )}
    </div>
  )
}
