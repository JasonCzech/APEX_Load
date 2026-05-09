import { useState, useEffect, useCallback } from 'react'
import { useApi } from '../hooks/useApi'
import { StoredScript } from '../types'

export default function ScriptsPage() {
  const api = useApi()
  const [scripts, setScripts] = useState<StoredScript[]>([])

  const refresh = useCallback(async () => {
    try {
      const res = await api.listScripts()
      setScripts(res.scripts || [])
    } catch {
      // API not available
    }
  }, [api])

  useEffect(() => {
    refresh()
  }, [refresh])

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>Scripts</h2>
          <p className="page-subtitle">Manage your test scripts and upload new ones</p>
        </div>
        <button className="btn btn-primary" disabled>Upload Script</button>
      </div>

      <div className="page-section">
        <h3 className="section-title">Script Library ({scripts.length})</h3>
        {scripts.length === 0 ? (
          <div className="empty-card">
            <div className="empty-icon">
              <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
                <polyline points="16 18 22 12 16 6" />
                <polyline points="8 6 2 12 8 18" />
              </svg>
            </div>
            <p>No scripts uploaded yet.</p>
            <p className="empty-hint">Use the API to upload a JSON DSL script, or upload a LoadRunner script for conversion.</p>
          </div>
        ) : (
          <div className="data-table">
            <table>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Protocol</th>
                  <th>Version</th>
                  <th>Created</th>
                  <th>ID</th>
                </tr>
              </thead>
              <tbody>
                {scripts.map((s) => (
                  <tr key={s.id}>
                    <td className="cell-name">{s.name}</td>
                    <td><span className="protocol-badge">{s.protocol}</span></td>
                    <td>{s.version || '—'}</td>
                    <td className="cell-muted">{new Date(s.created_at).toLocaleDateString()}</td>
                    <td className="cell-mono">{s.id.slice(0, 8)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}
