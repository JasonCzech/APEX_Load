import { useCallback, useEffect, useMemo, useState } from 'react'

import { useEntitlementsApi } from '../hooks/api/useEntitlementsApi'
import { ApiError } from '../hooks/apiClient'
import { EntitlementsResponse, EntitlementsUsageResponse } from '../types'
import './parityPanels.css'

function headroomTone(used: number, limit: number): { pct: number; cls: string } {
  if (!limit || limit <= 0) return { pct: 0, cls: 'pp-bar-fill-success' }
  const pct = Math.min(100, Math.round((used / limit) * 100))
  const cls = pct >= 90 ? 'pp-bar-fill-danger' : pct >= 70 ? 'pp-bar-fill-warning' : 'pp-bar-fill-success'
  return { pct, cls }
}

export default function EntitlementsPanel() {
  const api = useEntitlementsApi()
  const [entitlements, setEntitlements] = useState<EntitlementsResponse | null>(null)
  const [usage, setUsage] = useState<EntitlementsUsageResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const [ent, use] = await Promise.all([api.getEntitlements(), api.getEntitlementsUsage()])
      setEntitlements(ent)
      setUsage(use)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to load entitlements.')
    } finally {
      setLoading(false)
    }
  }, [api])

  useEffect(() => {
    void load()
  }, [load])

  const concurrent = useMemo(() => {
    const used = usage?.concurrent_tests.used ?? 0
    const limit = usage?.concurrent_tests.limit ?? entitlements?.limits.max_concurrent_tests ?? 0
    return { used, limit, ...headroomTone(used, limit) }
  }, [usage, entitlements])

  return (
    <div className="pp-panel">
      <div className="pp-toolbar">
        <span className="pp-toolbar-title">
          Read-only view of what this deployment is entitled to and its current consumption.
        </span>
        <button type="button" className="btn btn-ghost btn-sm" disabled={loading} onClick={() => void load()}>
          {loading ? 'Refreshing…' : 'Refresh'}
        </button>
      </div>

      {error && <div className="pp-error">{error}</div>}

      {loading && !entitlements ? (
        <div className="pp-empty">Loading entitlements…</div>
      ) : (
        <>
          <div className="pp-stat-grid">
            <div className="pp-stat">
              <span className="pp-stat-label">Edition</span>
              <span className="pp-stat-value">{entitlements?.edition ?? '—'}</span>
            </div>
            <div className="pp-stat">
              <span className="pp-stat-label">Service accounts</span>
              <span className="pp-stat-value">{usage?.service_accounts ?? '—'}</span>
            </div>
            <div className="pp-stat">
              <span className="pp-stat-label">Projects</span>
              <span className="pp-stat-value">{usage?.projects ?? '—'}</span>
            </div>
          </div>

          <div className="pp-stat pp-stat-wide">
            <span className="pp-stat-label">Concurrent tests (used / limit)</span>
            <span className="pp-stat-value">
              {concurrent.used} / {concurrent.limit}
            </span>
            <div className="pp-bar" aria-hidden="true">
              <div className={`pp-bar-fill ${concurrent.cls}`} style={{ width: `${concurrent.pct}%` }} />
            </div>
          </div>

          <div>
            <div className="pp-toolbar-title" style={{ marginBottom: 8 }}>
              Enabled protocol bundles ({entitlements?.protocol_bundles.count ?? 0})
            </div>
            {entitlements && entitlements.protocol_bundles.bundles.length > 0 ? (
              <div className="pp-list">
                {entitlements.protocol_bundles.bundles.map(bundle => (
                  <div className="pp-row" key={bundle.id}>
                    <div className="pp-row-main">
                      <div className="pp-row-title">{bundle.display_name}</div>
                      <div className="pp-row-meta">
                        {bundle.protocols?.length ? <span>protocols: {bundle.protocols.join(', ')}</span> : null}
                        {bundle.governance ? <span>governance: {bundle.governance}</span> : null}
                      </div>
                    </div>
                    {bundle.default ? <span className="pp-pill pp-pill-muted">{bundle.default}</span> : null}
                  </div>
                ))}
              </div>
            ) : (
              <div className="pp-empty">No protocol bundles reported.</div>
            )}
          </div>

          <p className="pp-muted">
            Entitlements are surfaced for visibility only — APEX Load does not enforce a licensing tier.
          </p>
        </>
      )}
    </div>
  )
}
