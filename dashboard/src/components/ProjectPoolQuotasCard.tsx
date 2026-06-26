import { useCallback, useEffect, useMemo, useState } from 'react'

import { useFleetApi } from '../hooks/api/useFleetApi'
import { useProjectApi } from '../hooks/api/useProjectApi'
import { ApiError } from '../hooks/apiClient'
import { FleetPool, PoolProjectQuota, Project } from '../types'
import './parityPanels.css'

interface Props {
  project: Project
  canManage?: boolean
  onUpdated?: (project: Project) => void
}

type QuotaRow = { pool_id: string; max_vusers: string; max_concurrent_tests: string }

function rowsFromProject(project: Project): QuotaRow[] {
  const quotas = project.settings?.pool_quotas || {}
  return Object.entries(quotas).map(([poolID, quota]) => ({
    pool_id: poolID,
    max_vusers: quota.max_vusers ? String(quota.max_vusers) : '',
    max_concurrent_tests: quota.max_concurrent_tests ? String(quota.max_concurrent_tests) : '',
  }))
}

// Per-pool project quotas: cap this project's concurrent footprint (VUsers /
// tests) on a specific fleet pool. Saves the FULL project settings (spreading the
// existing ones) so no other governance field is dropped.
export default function ProjectPoolQuotasCard({ project, canManage = false, onUpdated }: Props) {
  const fleetApi = useFleetApi()
  const projectApi = useProjectApi()
  const [pools, setPools] = useState<FleetPool[]>([])
  const [rows, setRows] = useState<QuotaRow[]>(() => rowsFromProject(project))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [message, setMessage] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    fleetApi.listFleetPools().then(res => {
      if (!cancelled) setPools(res.fleet_pools || [])
    }).catch(() => {
      if (!cancelled) setPools([])
    })
    return () => { cancelled = true }
  }, [fleetApi])

  // Re-seed when the selected project changes (id) or its saved quotas change.
  useEffect(() => {
    setRows(rowsFromProject(project))
    setError(null)
    setMessage(null)
  }, [project])

  const usedPools = useMemo(() => new Set(rows.map(r => r.pool_id)), [rows])
  const availablePools = useMemo(() => pools.filter(p => !usedPools.has(p.id)), [pools, usedPools])
  const poolName = useCallback((id: string) => pools.find(p => p.id === id)?.name || id, [pools])

  const addRow = (poolID: string) => {
    if (!poolID || usedPools.has(poolID)) return
    setRows(prev => [...prev, { pool_id: poolID, max_vusers: '', max_concurrent_tests: '' }])
    setMessage(null)
  }
  const updateRow = (idx: number, patch: Partial<QuotaRow>) =>
    setRows(prev => prev.map((r, i) => (i === idx ? { ...r, ...patch } : r)))
  const removeRow = (idx: number) => setRows(prev => prev.filter((_, i) => i !== idx))

  const save = useCallback(async () => {
    setBusy(true)
    setError(null)
    setMessage(null)
    const quotas: Record<string, PoolProjectQuota> = {}
    for (const row of rows) {
      const mv = parseInt(row.max_vusers, 10)
      const mc = parseInt(row.max_concurrent_tests, 10)
      const quota: PoolProjectQuota = {}
      if (Number.isFinite(mv) && mv > 0) quota.max_vusers = mv
      if (Number.isFinite(mc) && mc > 0) quota.max_concurrent_tests = mc
      // A row with neither limit is meaningless — drop it.
      if (quota.max_vusers || quota.max_concurrent_tests) quotas[row.pool_id] = quota
    }
    try {
      const updated = await projectApi.updateProject(project.id, {
        name: project.name,
        settings: {
          ...(project.settings || {}),
          pool_quotas: Object.keys(quotas).length > 0 ? quotas : undefined,
        },
      })
      onUpdated?.(updated)
      setMessage('Pool quotas saved.')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to save pool quotas.')
    } finally {
      setBusy(false)
    }
  }, [project, projectApi, rows, onUpdated])

  return (
    <div className="settings-subsection">
      <div className="settings-manager-summary" style={{ marginTop: 16 }}>
        <div>
          <span className="setting-label">Per-Pool Quotas</span>
          <span className="setting-desc">
            Cap this project's concurrent VUsers / tests on a specific fleet pool. Additive to the
            project-wide limits — a pool-assigned run must clear both.
          </span>
        </div>
        <span className="script-health-token">{rows.length} pool{rows.length === 1 ? '' : 's'}</span>
      </div>

      {error && <div className="pp-error">{error}</div>}
      {message && <div className="setting-desc">{message}</div>}

      {rows.length === 0 ? (
        <div className="pp-empty">No per-pool quotas. Add one to bound this project on a fleet pool.</div>
      ) : (
        <div className="pp-list">
          {rows.map((row, idx) => (
            <div className="pp-row" key={row.pool_id}>
              <div className="pp-row-main">
                <div className="pp-row-title">{poolName(row.pool_id)}</div>
                <div className="pp-field-row">
                  <label className="pp-field">
                    <span>Max VUsers</span>
                    <input
                      type="number"
                      min={0}
                      value={row.max_vusers}
                      disabled={!canManage}
                      placeholder="no cap"
                      onChange={e => updateRow(idx, { max_vusers: e.target.value })}
                    />
                  </label>
                  <label className="pp-field">
                    <span>Max concurrent tests</span>
                    <input
                      type="number"
                      min={0}
                      value={row.max_concurrent_tests}
                      disabled={!canManage}
                      placeholder="no cap"
                      onChange={e => updateRow(idx, { max_concurrent_tests: e.target.value })}
                    />
                  </label>
                </div>
              </div>
              {canManage && (
                <div className="pp-row-actions">
                  <button type="button" className="btn btn-danger btn-sm" onClick={() => removeRow(idx)}>
                    Remove
                  </button>
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      {canManage && (
        <div className="pp-toolbar" style={{ marginTop: 8 }}>
          <select
            aria-label="Add pool quota"
            className="settings-input"
            value=""
            disabled={availablePools.length === 0}
            onChange={e => { addRow(e.target.value); e.target.value = '' }}
          >
            <option value="">{availablePools.length === 0 ? 'No more pools' : '+ Add pool quota…'}</option>
            {availablePools.map(pool => (
              <option key={pool.id} value={pool.id}>{pool.name}</option>
            ))}
          </select>
          <button type="button" className="btn btn-primary btn-sm" disabled={busy} onClick={() => void save()}>
            {busy ? 'Saving…' : 'Save pool quotas'}
          </button>
        </div>
      )}
    </div>
  )
}
