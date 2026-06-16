import { useCallback, useEffect, useMemo, useState } from 'react'

import { useEntitlementsApi } from '../hooks/api/useEntitlementsApi'
import { ApiError } from '../hooks/apiClient'
import { EntitlementsUsageResponse } from '../types'
import './parityPanels.css'

function headroomTone(used: number, limit: number): { pct: number; cls: string } {
  if (!limit || limit <= 0) return { pct: 0, cls: 'pp-bar-fill-success' }
  const pct = Math.min(100, Math.round((used / limit) * 100))
  const cls = pct >= 90 ? 'pp-bar-fill-danger' : pct >= 70 ? 'pp-bar-fill-warning' : 'pp-bar-fill-success'
  return { pct, cls }
}

// Compact, read-only "active tests vs concurrency limit" headroom strip.
// Surfaced inline with usage data so operators see remaining concurrency at a glance.
export default function EntitlementHeadroom() {
  const api = useEntitlementsApi()
  const [usage, setUsage] = useState<EntitlementsUsageResponse | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      setUsage(await api.getEntitlementsUsage())
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to load entitlement headroom.')
      setUsage(null)
    } finally {
      setLoading(false)
    }
  }, [api])

  useEffect(() => {
    void load()
  }, [load])

  const concurrent = useMemo(() => {
    const used = usage?.concurrent_tests.used ?? 0
    const limit = usage?.concurrent_tests.limit ?? 0
    const remaining = Math.max(0, limit - used)
    return { used, limit, remaining, ...headroomTone(used, limit) }
  }, [usage])

  return (
    <div className="settings-subsection" data-testid="entitlement-headroom">
      <div className="settings-manager-summary">
        <div>
          <span className="setting-label">Entitlement Headroom</span>
          <span className="setting-desc">Active concurrent tests against this deployment's entitlement limit (visibility only — not enforced).</span>
        </div>
        <button type="button" className="btn btn-ghost btn-sm" disabled={loading} onClick={() => void load()}>
          {loading ? 'Refreshing…' : 'Refresh'}
        </button>
      </div>
      {error ? (
        <div className="pp-error">{error}</div>
      ) : (
        <div className="pp-stat pp-stat-wide">
          <span className="pp-stat-label">Concurrent tests (used / limit · {concurrent.remaining} free)</span>
          <span className="pp-stat-value">
            {concurrent.used} / {concurrent.limit || '∞'}
          </span>
          <div className="pp-bar" aria-hidden="true">
            <div className={`pp-bar-fill ${concurrent.cls}`} style={{ width: `${concurrent.pct}%` }} />
          </div>
        </div>
      )}
    </div>
  )
}
