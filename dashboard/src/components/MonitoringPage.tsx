import { useState, useEffect, useCallback } from 'react'
import { useApi } from '../hooks/useApi'

export default function MonitoringPage({ connected }: { connected: boolean }) {
  const api = useApi()
  const [health, setHealth] = useState<any>(null)

  const refresh = useCallback(async () => {
    try {
      const res = await api.getHealth()
      setHealth(res)
    } catch {
      // API not available
    }
  }, [api])

  useEffect(() => {
    refresh()
    const interval = setInterval(refresh, 5000)
    return () => clearInterval(interval)
  }, [refresh])

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>Monitoring</h2>
          <p className="page-subtitle">System health, generator pods, and connection status</p>
        </div>
      </div>

      <div className="monitoring-grid">
        <div className="monitor-card">
          <div className="monitor-header">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <polyline points="22 12 18 12 15 21 9 3 6 12 2 12" />
            </svg>
            <span>System Health</span>
          </div>
          <div className="monitor-body">
            <div className="monitor-stat">
              <span className={`monitor-indicator ${health ? 'healthy' : 'unknown'}`} />
              <span className="monitor-value">{health?.status || 'Checking...'}</span>
            </div>
          </div>
        </div>

        <div className="monitor-card">
          <div className="monitor-header">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <rect x="2" y="3" width="20" height="14" rx="2" ry="2" />
              <line x1="8" y1="21" x2="16" y2="21" />
              <line x1="12" y1="17" x2="12" y2="21" />
            </svg>
            <span>Controller</span>
          </div>
          <div className="monitor-body">
            <div className="monitor-row">
              <span className="monitor-label">Version</span>
              <span className="monitor-value">{health?.version || '—'}</span>
            </div>
            <div className="monitor-row">
              <span className="monitor-label">Active Tests</span>
              <span className="monitor-value">{health?.test_count ?? '—'}</span>
            </div>
            <div className="monitor-row">
              <span className="monitor-label">WS Clients</span>
              <span className="monitor-value">{health?.ws_clients ?? '—'}</span>
            </div>
          </div>
        </div>

        <div className="monitor-card">
          <div className="monitor-header">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M5 12.55a11 11 0 0 1 14.08 0" />
              <path d="M1.42 9a16 16 0 0 1 21.16 0" />
              <path d="M8.53 16.11a6 6 0 0 1 6.95 0" />
              <line x1="12" y1="20" x2="12.01" y2="20" />
            </svg>
            <span>WebSocket</span>
          </div>
          <div className="monitor-body">
            <div className="monitor-row">
              <span className="monitor-label">Connection</span>
              <span className={`monitor-value ${connected ? 'text-success' : 'text-danger'}`}>
                {connected ? 'Connected' : 'Disconnected'}
              </span>
            </div>
            <div className="monitor-row">
              <span className="monitor-label">Endpoint</span>
              <span className="monitor-value cell-mono">/api/v1/ws</span>
            </div>
          </div>
        </div>

        <div className="monitor-card">
          <div className="monitor-header">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <rect x="4" y="4" width="16" height="16" rx="2" ry="2" />
              <rect x="9" y="9" width="6" height="6" />
              <line x1="9" y1="1" x2="9" y2="4" />
              <line x1="15" y1="1" x2="15" y2="4" />
              <line x1="9" y1="20" x2="9" y2="23" />
              <line x1="15" y1="20" x2="15" y2="23" />
              <line x1="20" y1="9" x2="23" y2="9" />
              <line x1="20" y1="14" x2="23" y2="14" />
              <line x1="1" y1="9" x2="4" y2="9" />
              <line x1="1" y1="14" x2="4" y2="14" />
            </svg>
            <span>Generators</span>
          </div>
          <div className="monitor-body">
            <div className="monitor-stat">
              <span className="monitor-value text-muted">Phase 2</span>
            </div>
            <p className="monitor-hint">Distributed generator pods will appear here in Phase 2.</p>
          </div>
        </div>
      </div>
    </div>
  )
}
