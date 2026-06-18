import { useCallback, useEffect, useMemo, useState } from 'react'

import { useEntitlementsApi } from '../hooks/api/useEntitlementsApi'
import { ApiError } from '../hooks/apiClient'
import { EntitlementLimitView, EntitlementsResponse, EntitlementsUsageResponse } from '../types'
import './parityPanels.css'

function headroomTone(used: number, limit: number): { pct: number; cls: string } {
  if (!limit || limit <= 0) return { pct: 0, cls: 'pp-bar-fill-success' }
  const pct = Math.min(100, Math.round((used / limit) * 100))
  const cls = pct >= 90 ? 'pp-bar-fill-danger' : pct >= 70 ? 'pp-bar-fill-warning' : 'pp-bar-fill-success'
  return { pct, cls }
}

function LimitBar({ limit }: { limit: EntitlementLimitView }) {
  const { pct, cls } = headroomTone(limit.used, limit.limit)
  return (
    <div className="pp-stat pp-stat-wide">
      <span className="pp-stat-label">
        {limit.label} ({limit.scope}){limit.unlimited ? ' — no cap' : ''}
      </span>
      <span className="pp-stat-value">{limit.unlimited ? `${limit.used}` : `${limit.used} / ${limit.limit}`}</span>
      <div className="pp-bar" aria-hidden="true">
        <div className={`pp-bar-fill ${cls}`} style={{ width: `${pct}%` }} />
      </div>
    </div>
  )
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

  // Per-bundle historical consumption, keyed by bundle id, merged into the
  // catalog bundle rows below.
  const bundleConsumption = useMemo(() => {
    const map = new Map<string, { run_count: number; total_vusers: number }>()
    for (const b of usage?.protocol_bundles ?? []) {
      map.set(b.id, { run_count: b.run_count, total_vusers: b.total_vusers })
    }
    return map
  }, [usage])

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

          {usage?.limits && usage.limits.length > 0 ? (
            usage.limits.map(limit => <LimitBar key={`${limit.scope}-${limit.key}`} limit={limit} />)
          ) : (
            <div className="pp-stat pp-stat-wide">
              <span className="pp-stat-label">Concurrent tests (used / limit)</span>
              <span className="pp-stat-value">
                {concurrent.used} / {concurrent.limit}
              </span>
              <div className="pp-bar" aria-hidden="true">
                <div className={`pp-bar-fill ${concurrent.cls}`} style={{ width: `${concurrent.pct}%` }} />
              </div>
            </div>
          )}

          <div>
            <div className="pp-toolbar-title" style={{ marginBottom: 8 }}>
              Enabled protocol bundles ({entitlements?.protocol_bundles.count ?? 0})
            </div>
            {entitlements && entitlements.protocol_bundles.bundles.length > 0 ? (
              <div className="pp-list">
                {entitlements.protocol_bundles.bundles.map(bundle => {
                  const consumption = bundleConsumption.get(bundle.id)
                  const consumed = consumption && (consumption.run_count > 0 || consumption.total_vusers > 0)
                  return (
                    <div className="pp-row" key={bundle.id}>
                      <div className="pp-row-main">
                        <div className="pp-row-title">{bundle.display_name}</div>
                        <div className="pp-row-meta">
                          {bundle.protocols?.length ? <span>protocols: {bundle.protocols.join(', ')}</span> : null}
                          {bundle.governance ? <span>governance: {bundle.governance}</span> : null}
                          {consumed ? (
                            <span>
                              consumption: {consumption!.run_count} runs · {consumption!.total_vusers} VUsers
                            </span>
                          ) : null}
                        </div>
                      </div>
                      {bundle.default ? <span className="pp-pill pp-pill-muted">{bundle.default}</span> : null}
                    </div>
                  )
                })}
              </div>
            ) : (
              <div className="pp-empty">No protocol bundles reported.</div>
            )}
          </div>

          {usage?.by_project && usage.by_project.length > 0 ? (
            <div>
              <div className="pp-toolbar-title" style={{ marginBottom: 8 }}>
                Per-project consumption ({usage.by_project.length})
              </div>
              <div className="pp-list">
                {usage.by_project.map(project => (
                  <div className="pp-row" key={project.project_id}>
                    <div className="pp-row-main">
                      <div className="pp-row-title">{project.name || project.project_id}</div>
                      <div className="pp-row-meta">
                        {project.limits.map(limit => (
                          <span key={limit.key}>
                            {limit.label}: {limit.unlimited ? `${limit.used} (no cap)` : `${limit.used} / ${limit.limit}`}
                          </span>
                        ))}
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          ) : null}

          <p className="pp-muted">
            Entitlements are surfaced for visibility only — APEX Load does not enforce a licensing tier.
          </p>
        </>
      )}
    </div>
  )
}
