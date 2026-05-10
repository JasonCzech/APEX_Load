import { useState, useEffect, useCallback } from 'react'
import { downloadBlob } from '../utils/fileUtils'
import { useApi } from '../hooks/useApi'
import './RecordingStudio.css'

interface RecordedRequest {
  seq: number
  timestamp: string
  method: string
  url: string
  path: string
  host: string
  status_code: number
  response_time_ms: number
  content_type: string
  body: string
  think_time_ms: number
  is_static: boolean
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

interface Correlation {
  name: string
  source: string
  expression: string
  extract_type: string
  found_in_seq: number
  used_in_seqs: number[]
  sample_value: string
}

export default function RecordingStudio() {
  const api = useApi()
  const [sessions, setSessions] = useState<RecordingSession[]>([])
  const [activeSession, setActiveSession] = useState<RecordingSession | null>(null)
  const [proxyAddr, setProxyAddr] = useState('')
  const [sessionName, setSessionName] = useState('')
  const [includeHosts, setIncludeHosts] = useState('')
  const [excludeHosts, setExcludeHosts] = useState('')
  const [feedback, setFeedback] = useState('')
  const [feedbackType, setFeedbackType] = useState<'success' | 'error'>('success')
  const [converting, setConverting] = useState(false)
  const [convertResult, setConvertResult] = useState<{ script: any; correlations: Correlation[]; warnings: string[] } | null>(null)
  const [selectedSession, setSelectedSession] = useState<RecordingSession | null>(null)
  const [showSetup, setShowSetup] = useState(true)

  const proxyPort = (proxyAddr || ':8081').split(':').pop() || '8081'

  const handleDownloadCA = async (format: 'crt' | 'pem' = 'crt') => {
    try {
      const url = format === 'pem' ? '/api/v1/recorder/ca.pem?format=pem' : '/api/v1/recorder/ca.pem'
      const res = await fetch(url)
      if (!res.ok) throw new Error('Failed to download CA certificate')
      const blob = await res.blob()
      const filename = format === 'pem' ? 'stormrunner-ca.pem' : 'stormrunner-ca.crt'
      downloadBlob(blob, filename)
    } catch (e: any) {
      showFeedback(e.message || 'Failed to download CA certificate', 'error')
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
        api.listRecordings(),
        api.getRecorderStatus(),
      ])
      setSessions(listRes.sessions || [])
      setProxyAddr(statusRes.proxy_addr || '')
      if (statusRes.active) {
        const full = await api.getRecording(statusRes.active.id)
        setActiveSession(full)
      } else {
        setActiveSession(null)
      }
    } catch { /* API not ready */ }
  }, [api])

  useEffect(() => {
    refresh()
    const interval = setInterval(refresh, 2000)
    return () => clearInterval(interval)
  }, [refresh])

  const handleStart = async () => {
    try {
      const data: any = {
        name: sessionName || 'Recording Session',
        exclude_static: true,
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

  const handleConvert = async (session: RecordingSession) => {
    setConverting(true)
    setConvertResult(null)
    try {
      const result = await api.convertRecording(session.id, session.name)
      setConvertResult(result)
      showFeedback(`Script "${result.script.name}" created with ${result.correlations?.length || 0} correlations detected`)
    } catch (e: any) {
      showFeedback(e.message, 'error')
    } finally {
      setConverting(false)
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
              Recording: {activeSession.name}
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
                onClick={() => setSelectedSession(selectedSession?.id === session.id ? null : session)}
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
                </tr>
              </thead>
              <tbody>
                {selectedSession.requests?.map(req => (
                  <tr key={req.seq} className={req.is_static ? 'static' : ''}>
                    <td className="cell-mono">{req.seq}</td>
                    <td><span className={`rec-method ${req.method.toLowerCase()}`}>{req.method}</span></td>
                    <td className="cell-path" title={req.url}>{req.path}</td>
                    <td className={req.status_code >= 400 ? 'text-danger' : ''}>{req.status_code}</td>
                    <td className="cell-mono">{req.response_time_ms.toFixed(0)}ms</td>
                    <td className="cell-mono">{req.think_time_ms > 0 ? `${req.think_time_ms}ms` : '—'}</td>
                    <td className="cell-muted">{req.content_type ? req.content_type.split(';')[0] : '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Convert Result */}
      {convertResult && (
        <div className="rec-convert-result">
          <h3 className="rec-section-title">Conversion Result</h3>
          <div className="rec-result-summary">
            <div className="rec-result-item">
              <span className="rec-result-label">Script</span>
              <span className="rec-result-value">{convertResult.script.name}</span>
            </div>
            <div className="rec-result-item">
              <span className="rec-result-label">Protocol</span>
              <span className="rec-result-value">{convertResult.script.protocol}</span>
            </div>
            <div className="rec-result-item">
              <span className="rec-result-label">Script ID</span>
              <code className="rec-result-value">{convertResult.script.id}</code>
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

          {convertResult.warnings && convertResult.warnings.length > 0 && (
            <div className="rec-warnings">
              {convertResult.warnings.map((w, i) => (
                <div key={i} className="rec-warning-item">{w}</div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
