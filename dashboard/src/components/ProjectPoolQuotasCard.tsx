import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react'

import { useFleetApi } from '../hooks/api/useFleetApi'
import { useProjectApi } from '../hooks/api/useProjectApi'
import { ApiError } from '../hooks/apiClient'
import { FleetPool, PoolProjectQuota, Project } from '../types'
import { listAllFleetPools } from './fleetCatalogModel'
import './parityPanels.css'

interface Props {
  project: Project
  canManage?: boolean
  onUpdate?: (
    projectID: string,
    project: { name: string; settings: Project['settings'] },
  ) => Promise<Project>
  onUpdated?: (project: Project) => void
  onDirtyChange?: (dirty: boolean) => void
}

export interface ProjectPoolQuotasDraftController {
  isDirty: () => boolean
	// An authoritative base is supplied by Settings "Save All" when project
	// governance was committed immediately before quotas. This prevents the
	// quota write from spreading a stale project prop over that newer response.
	save: (baseProject?: Project) => Promise<boolean>
  discard: () => void
}

type QuotaRow = { pool_id: string; max_vusers: string; max_concurrent_tests: string }

function rowsFromProject(project: Project): QuotaRow[] {
  const quotas = project.settings?.pool_quotas || {}
  return Object.entries(quotas)
    .map(([poolID, quota]) => ({
      pool_id: poolID,
      max_vusers: quota.max_vusers ? String(quota.max_vusers) : '',
      max_concurrent_tests: quota.max_concurrent_tests ? String(quota.max_concurrent_tests) : '',
    }))
    .sort((left, right) => left.pool_id.localeCompare(right.pool_id))
}

function rowsEqual(left: QuotaRow[], right: QuotaRow[]) {
  return JSON.stringify(left) === JSON.stringify(right)
}

// Per-pool project quotas: cap this project's concurrent footprint (VUsers /
// tests) on a specific fleet pool. Saves the FULL project settings (spreading the
// existing ones) so no other governance field is dropped.
const ProjectPoolQuotasCard = forwardRef<ProjectPoolQuotasDraftController, Props>(function ProjectPoolQuotasCard({
  project,
  canManage = false,
  onUpdate,
  onUpdated,
  onDirtyChange,
}, ref) {
  const fleetApi = useFleetApi()
  const projectApi = useProjectApi()
  const initialRows = rowsFromProject(project)
  const [pools, setPools] = useState<FleetPool[]>([])
  const [rows, setRows] = useState<QuotaRow[]>(initialRows)
  const [baselineRows, setBaselineRows] = useState<QuotaRow[]>(initialRows)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [message, setMessage] = useState<string | null>(null)
  const rowsRef = useRef(rows)
  const baselineRowsRef = useRef(baselineRows)
  const baselineProjectIdRef = useRef(project.id)
  rowsRef.current = rows
  baselineRowsRef.current = baselineRows

  useEffect(() => {
    let cancelled = false
    listAllFleetPools(fleetApi.listFleetPools).then(nextPools => {
      if (!cancelled) setPools(nextPools)
    }).catch(() => {
      if (!cancelled) setPools([])
    })
    return () => { cancelled = true }
  }, [fleetApi])

  // Re-seed when the selected project changes, or when a fresh authoritative
  // project response arrives while this editor is clean. Never overwrite an
  // in-progress quota edit with a background project refresh.
  useEffect(() => {
    const next = rowsFromProject(project)
    const projectChanged = baselineProjectIdRef.current !== project.id
    if (projectChanged || rowsEqual(rowsRef.current, baselineRowsRef.current)) {
      baselineProjectIdRef.current = project.id
      baselineRowsRef.current = next
      rowsRef.current = next
      setBaselineRows(next)
      setRows(next)
    }
    setError(null)
    setMessage(null)
  }, [project])

  const dirty = !rowsEqual(rows, baselineRows)
  useEffect(() => {
    onDirtyChange?.(dirty)
    return () => onDirtyChange?.(false)
  }, [dirty, onDirtyChange])

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

	const save = useCallback(async (baseProject?: Project): Promise<boolean> => {
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
			const projectBase = baseProject?.id === project.id ? baseProject : project
			const updateProject = onUpdate || projectApi.updateProject
			const updated = await updateProject(projectBase.id, {
				name: projectBase.name,
        settings: {
					...(projectBase.settings || {}),
          pool_quotas: Object.keys(quotas).length > 0 ? quotas : undefined,
        },
      })
      const savedRows = rowsFromProject(updated)
      baselineProjectIdRef.current = updated.id
      baselineRowsRef.current = savedRows
      rowsRef.current = savedRows
      setBaselineRows(savedRows)
      setRows(savedRows)
      onUpdated?.(updated)
      setMessage('Pool quotas saved.')
      return true
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to save pool quotas.')
      return false
    } finally {
      setBusy(false)
    }
	}, [onUpdate, onUpdated, project, projectApi.updateProject, rows])

  const discard = useCallback(() => {
    const next = baselineRowsRef.current
    rowsRef.current = next
    setRows(next)
    setError(null)
    setMessage(null)
  }, [])

  useImperativeHandle(ref, () => ({
    isDirty: () => !rowsEqual(rowsRef.current, baselineRowsRef.current),
    save,
    discard,
  }), [discard, save])

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
})

export default ProjectPoolQuotasCard
