import DialogLayer from './ux/DialogLayer'
import MetricPackPicker from './MetricPackPicker'
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'

import { useMonitoringCollectorsApi } from '../hooks/api/useMonitoringCollectorsApi'
import { ApiError } from '../hooks/apiClient'
import { useNavigationGuard, useNavigationGuardRunner, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { useModalDialog } from '../hooks/useModalDialog'
import { CollectorMetricSnapshot, MonitoringCollector } from '../types'
import { copyTextToClipboard } from '../utils/clipboardUtils'
import ConfirmDestructiveModal from './ConfirmDestructiveModal'
import { dialogBackdropProps } from './dialogBackdropProps'
import { listAllMonitoringCollectors } from './monitoringCatalogModel'
import './parityPanels.css'
import './MonitoringWorkspace.css'
import LoadingState from './ux/LoadingState'
import EmptyState from './ux/EmptyState'
import { formatDateTime } from '../utils/dateTime'

interface Props {
  projectId?: string
  canManage?: boolean
}

function healthPillClass(health: string): string {
  switch (health) {
    case 'healthy':
      return 'pp-pill pp-pill-success'
    case 'degraded':
      return 'pp-pill pp-pill-warning'
    default:
      return 'pp-pill pp-pill-muted'
  }
}

function packsToText(packs: string[] | undefined): string {
  return (packs || []).join(', ')
}

function textToPacks(text: string): string[] {
  return text
    .split(',')
    .map(part => part.trim())
    .filter(Boolean)
}

// Shared formatter: dates read the same, zone included, as the rest of the app.
function formatTimestamp(value?: string): string {
  return formatDateTime(value, { seconds: true, fallback: '—' })
}

function freshness(snapshot: CollectorMetricSnapshot): { label: string; cls: string } {
  if (snapshot.pending) return { label: 'awaiting first heartbeat', cls: 'pp-pill pp-pill-muted' }
  if (snapshot.stale) return { label: 'stale', cls: 'pp-pill pp-pill-warning' }
  return { label: 'online', cls: 'pp-pill pp-pill-success' }
}

type EditState = { id: string; name: string; description: string; health: string; packs: string; baseline: string }

function editSnapshot(edit: Pick<EditState, 'name' | 'description' | 'health' | 'packs'>): string {
  return JSON.stringify({
    name: edit.name,
    description: edit.description,
    health: edit.health,
    packs: edit.packs,
  })
}

export default function MonitoringCollectorsCard({ projectId, canManage = false }: Props) {
  const api = useMonitoringCollectorsApi()
  const { runGuarded } = useNavigationGuardRunner()
  const [collectors, setCollectors] = useState<MonitoringCollector[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [showCreate, setShowCreate] = useState(false)
  const [createForm, setCreateForm] = useState({ name: '', description: '', packs: '' })
  const [edit, setEdit] = useState<EditState | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<MonitoringCollector | null>(null)
  const [oneTimeToken, setOneTimeToken] = useState<{ id: string; name: string; token: string } | null>(null)
  const [metrics, setMetrics] = useState<{ id: string; name: string; snapshot: CollectorMetricSnapshot } | null>(null)
  const [lastLoaded, setLastLoaded] = useState<Date | null>(null)
  const listPendingRef = useRef(false)
  const metricsGenerationRef = useRef(0)
  const [metricsError, setMetricsError] = useState<string | null>(null)
  const [metricsLoading, setMetricsLoading] = useState(false)
  const [busy, setBusy] = useState(false)
  const [copied, setCopied] = useState(false)
  const [copyError, setCopyError] = useState<string | null>(null)
  const scopeRef = useRef({ projectId, generation: 0 })
  const loadGenerationRef = useRef(0)
  if (scopeRef.current.projectId !== projectId) {
    scopeRef.current = { projectId, generation: scopeRef.current.generation + 1 }
  }
  const isCurrentScope = useCallback((scope: { projectId?: string; generation: number }) => (
    scopeRef.current.projectId === scope.projectId && scopeRef.current.generation === scope.generation
  ), [])
  const createDialogRef = useRef<HTMLDivElement>(null)
  const editDialogRef = useRef<HTMLDivElement>(null)
  const tokenDialogRef = useRef<HTMLDivElement>(null)
  const metricsDialogRef = useRef<HTMLDivElement>(null)
  const resetCreate = useCallback(() => setCreateForm({ name: '', description: '', packs: '' }), [])
  const discardCreate = useCallback(() => {
    setShowCreate(false)
    resetCreate()
    setError(null)
  }, [resetCreate])
  const discardEdit = useCallback(() => {
    setEdit(null)
    setError(null)
  }, [])
  const closeCreate = useCallback(() => runGuarded(discardCreate), [discardCreate, runGuarded])
  const closeEdit = useCallback(() => runGuarded(discardEdit), [discardEdit, runGuarded])
  const closeToken = useCallback(() => setOneTimeToken(null), [])
  const closeMetrics = useCallback(() => { metricsGenerationRef.current += 1; setMetrics(null); setMetricsError(null); setMetricsLoading(false) }, [])

  useModalDialog({ dialogRef: createDialogRef, enabled: showCreate, onClose: busy ? undefined : closeCreate })
  useModalDialog({ dialogRef: editDialogRef, enabled: Boolean(edit), onClose: busy ? undefined : closeEdit })
  useModalDialog({ dialogRef: tokenDialogRef, enabled: Boolean(oneTimeToken), onClose: closeToken })
  useModalDialog({ dialogRef: metricsDialogRef, enabled: Boolean(metrics), onClose: closeMetrics })

  useLayoutEffect(() => {
    loadGenerationRef.current += 1
    setCollectors([])
    setLastLoaded(null)
    listPendingRef.current = false
    metricsGenerationRef.current += 1
    setMetricsError(null)
    setMetricsLoading(false)
    setLoading(false)
    setError(null)
    setShowCreate(false)
    setCreateForm({ name: '', description: '', packs: '' })
    setEdit(null)
    setDeleteTarget(null)
    setOneTimeToken(null)
    setMetrics(null)
    setBusy(false)
    setCopied(false)
    setCopyError(null)
  }, [projectId])

  useEffect(() => {
    setCopied(false)
    setCopyError(null)
  }, [oneTimeToken?.token])

  const load = useCallback(async () => {
    const scope = { ...scopeRef.current }
    const requestGeneration = ++loadGenerationRef.current
    if (!projectId) {
      if (isCurrentScope(scope)) setCollectors([])
      return
    }
    listPendingRef.current = true
    setLoading(true)
    setError(null)
    try {
			const nextCollectors = await listAllMonitoringCollectors(api.listCollectors, projectId)
      if (!isCurrentScope(scope) || requestGeneration !== loadGenerationRef.current) return
			setCollectors(nextCollectors)
      setLastLoaded(new Date())
    } catch (err) {
      if (!isCurrentScope(scope) || requestGeneration !== loadGenerationRef.current) return
      setError(err instanceof ApiError ? err.message : 'Failed to load monitoring collectors.')
    } finally {
      if (isCurrentScope(scope) && requestGeneration === loadGenerationRef.current) setLoading(false)
      if (requestGeneration === loadGenerationRef.current) listPendingRef.current = false
    }
  }, [api, isCurrentScope, projectId])

  useEffect(() => {
    void load()
    const poll = () => { if (document.visibilityState === 'visible' && !listPendingRef.current) void load() }
    const interval = window.setInterval(poll, 5000)
    document.addEventListener('visibilitychange', poll)
    return () => { window.clearInterval(interval); document.removeEventListener('visibilitychange', poll); loadGenerationRef.current += 1 }
  }, [load])

  const submitCreate = useCallback(async (): Promise<boolean> => {
    if (!projectId || !createForm.name.trim()) return false
    const scope = { ...scopeRef.current }
    setBusy(true)
    setError(null)
    try {
      const res = await api.createCollector({
        project_id: projectId,
        name: createForm.name.trim(),
        description: createForm.description.trim() || undefined,
        allowed_metric_packs: textToPacks(createForm.packs),
      })
			if (!isCurrentScope(scope)) return false
			setCollectors(current => [...current.filter(collector => collector.id !== res.collector.id), res.collector])
      setShowCreate(false)
      resetCreate()
      setOneTimeToken({ id: res.collector.id, name: res.collector.name, token: res.token })
      await load()
			return true
    } catch (err) {
      if (isCurrentScope(scope)) setError(err instanceof ApiError ? err.message : 'Failed to register collector.')
			return false
    } finally {
      if (isCurrentScope(scope)) setBusy(false)
    }
	}, [api, createForm, isCurrentScope, load, projectId, resetCreate])

  const submitEdit = useCallback(async (): Promise<boolean> => {
    if (!edit) return false
    const scope = { ...scopeRef.current }
    setBusy(true)
    setError(null)
    try {
      const saved = await api.patchCollector(edit.id, {
        name: edit.name.trim() || undefined,
        description: edit.description,
        health: edit.health,
        allowed_metric_packs: textToPacks(edit.packs),
      })
			if (!isCurrentScope(scope)) return false
			setCollectors(current => current.map(collector => collector.id === saved.id ? saved : collector))
      setEdit(null)
      await load()
			return true
    } catch (err) {
      if (isCurrentScope(scope)) setError(err instanceof ApiError ? err.message : 'Failed to update collector.')
			return false
    } finally {
      if (isCurrentScope(scope)) setBusy(false)
    }
  }, [api, edit, isCurrentScope, load])

  const rotate = useCallback(
    async (collector: MonitoringCollector) => {
      const scope = { ...scopeRef.current }
      setBusy(true)
      setError(null)
      try {
        const res = await api.rotateCollectorToken(collector.id)
        if (!isCurrentScope(scope)) return
				if (res.collector) {
					setCollectors(current => current.map(candidate => candidate.id === res.collector.id ? res.collector : candidate))
				}
        setOneTimeToken({ id: collector.id, name: collector.name, token: res.token })
        await load()
      } catch (err) {
        if (isCurrentScope(scope)) setError(err instanceof ApiError ? err.message : 'Failed to rotate token.')
      } finally {
        if (isCurrentScope(scope)) setBusy(false)
      }
    },
    [api, isCurrentScope, load],
  )

  const viewMetrics = useCallback(
    async (collector: MonitoringCollector) => {
      const scope = { ...scopeRef.current }
      setBusy(true)
      setError(null)
      try {
        const snapshot = await api.getCollectorMetrics(collector.id)
        if (!isCurrentScope(scope)) return
        setMetrics({ id: collector.id, name: collector.name, snapshot }); setMetricsError(null)
      } catch (err) {
        if (isCurrentScope(scope)) setError(err instanceof ApiError ? err.message : 'Failed to load collector metrics.')
      } finally {
        if (isCurrentScope(scope)) setBusy(false)
      }
    },
    [api, isCurrentScope],
  )

  const refreshMetrics = useCallback(async () => {
    if (!metrics || metricsLoading) return
    const scope = { ...scopeRef.current }
    const generation = ++metricsGenerationRef.current
    setMetricsLoading(true)
    setMetricsError(null)
    try {
      const snapshot = await api.getCollectorMetrics(metrics.id)
      if (isCurrentScope(scope) && generation === metricsGenerationRef.current) setMetrics(current => current?.id === metrics.id ? { ...current, snapshot } : current)
    } catch (err) {
      if (isCurrentScope(scope) && generation === metricsGenerationRef.current) setMetricsError(err instanceof Error ? err.message : 'Unable to refresh metrics.')
    } finally {
      if (isCurrentScope(scope) && generation === metricsGenerationRef.current) setMetricsLoading(false)
    }
  }, [api, isCurrentScope, metrics, metricsLoading])

  const confirmDelete = useCallback(async () => {
    if (!deleteTarget) return
    const scope = { ...scopeRef.current }
    setBusy(true)
    setError(null)
    try {
      await api.deleteCollector(deleteTarget.id)
      if (!isCurrentScope(scope)) return
			setCollectors(current => current.filter(collector => collector.id !== deleteTarget.id))
      setDeleteTarget(null)
      await load()
    } catch (err) {
      if (isCurrentScope(scope)) setError(err instanceof ApiError ? err.message : 'Failed to delete collector.')
    } finally {
      if (isCurrentScope(scope)) setBusy(false)
    }
  }, [api, deleteTarget, isCurrentScope, load])

  const copyToken = useCallback(async () => {
    if (!oneTimeToken) return
    setCopyError(null)
    if (await copyTextToClipboard(oneTimeToken.token)) {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
      return
    }
    setCopied(false)
    setCopyError('Copy unavailable. Select the token and copy it manually.')
  }, [oneTimeToken])

  const createValid = useMemo(() => Boolean(projectId && createForm.name.trim()), [projectId, createForm.name])
	const createDirty = showCreate && Boolean(createForm.name.trim() || createForm.description.trim() || createForm.packs.trim())
	const editDirty = Boolean(edit && editSnapshot(edit) !== edit.baseline)
	const draftDirty = createDirty || editDirty
	const draftDirtyRef = useRef(draftDirty)
	draftDirtyRef.current = draftDirty
	const saveDraftRef = useRef<() => Promise<boolean>>(async () => false)
	saveDraftRef.current = async () => {
		if (createDirty) return submitCreate()
		if (editDirty) return submitEdit()
		return true
	}
	const discardDraft = useCallback(() => {
		discardCreate()
		discardEdit()
	}, [discardCreate, discardEdit])
	const draftGuard = useMemo<NavigationGuard>(() => ({
		id: 'monitoring-collector-draft',
		label: 'Monitoring collector editor',
		isDirty: () => draftDirtyRef.current,
		save: () => saveDraftRef.current(),
		discard: discardDraft,
	}), [discardDraft])
	useNavigationGuard(draftGuard, draftDirty)

  return (
    <div className="monitor-body pp-panel">
      <div className="pp-toolbar">
        <span className="pp-toolbar-title">
          {collectors.length} registered collector{collectors.length === 1 ? '' : 's'}
        </span>
        {canManage && (
          <button
            type="button"
            className="btn btn-primary btn-sm"
            disabled={!projectId}
            title={projectId ? undefined : 'Select a project to register a collector'}
            onClick={() => {
								discardEdit()
								resetCreate()
              setShowCreate(true)
            }}
          >
            Register Collector
          </button>
        )}
      </div>

      <div className="monitor-workflow-actions"><button type="button" className="btn btn-secondary btn-sm" disabled={loading || !projectId} onClick={() => void load()}>{loading ? 'Refreshing collectors…' : 'Refresh collectors'}</button><span className="monitor-status-note">{lastLoaded ? `Collectors checked ${lastLoaded.toLocaleTimeString()}. Refreshes every 5 seconds while visible.` : 'Collector status has not been retrieved.'}</span></div>
      <details><summary>Collector setup and verification</summary><ol><li>Register a collector and select the packs it may submit.</li><li>Install the apexload-collector-agent binary on the monitored host. Set APEXLOAD_CONTROLLER_URL, APEXLOAD_COLLECTOR_ID, APEXLOAD_COLLECTOR_TOKEN, and APEXLOAD_COLLECTOR_PACKS in its service environment. Use the collector ID shown below and the token shown once at registration.</li><li>Start the agent with a reachable HTTPS controller URL. Its default heartbeat interval is 15 seconds.</li><li>Check Last seen, then open Metrics to verify the expected pack and sample values. Registration alone does not confirm collection.</li></ol></details>
      {error && <div className="pp-error" role="alert">{error} {lastLoaded ? 'Previously loaded collector data may be stale.' : ''}</div>}

      {!projectId ? (
        <EmptyState title="No project selected." hint="Select a project to view its monitoring collectors." />
      ) : loading && !lastLoaded ? (
        <LoadingState what="collectors" className="pp-loading" />
      ) : collectors.length === 0 ? (
        <EmptyState title="No monitoring collectors yet." hint="Collectors are restricted-network agents that report scoped metric packs." />
      ) : (
        <div className="pp-list">
          {collectors.map(collector => (
            <div className="pp-row" key={collector.id}>
              <div className="pp-row-main">
                <div className="pp-row-title">
                  {collector.name}
                  {/* A collector that has never sent a heartbeat is new, not unreachable;
                      say so the same way its metrics dialog does. */}
                  {collector.last_seen_at
                    ? <span className={healthPillClass(collector.health)}>{collector.health}</span>
                    : <span className="pp-pill pp-pill-muted">awaiting first heartbeat</span>}
                </div>
                <div className="pp-row-meta">
                  <span>collector ID: {collector.id}</span>
                  <span>packs: {collector.allowed_metric_packs?.length ? packsToText(collector.allowed_metric_packs) : 'none'}</span>
                  <span>last seen: {formatTimestamp(collector.last_seen_at)}</span>
                </div>
              </div>
              <div className="pp-row-actions">
                <button type="button" className="btn btn-ghost btn-sm" disabled={busy} onClick={() => void viewMetrics(collector)}>
                  Metrics
                </button>
                {canManage && (
                  <>
                    <button
                      type="button"
                      className="btn btn-ghost btn-sm"
                      disabled={busy}
						onClick={() => {
										const fields = {
                          id: collector.id,
                          name: collector.name,
                          description: collector.description || '',
                          health: collector.health,
                          packs: packsToText(collector.allowed_metric_packs),
										}
										setEdit({ ...fields, baseline: editSnapshot(fields) })
									}}
                    >
                      Edit
                    </button>
                    <button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => void rotate(collector)}>
                      Rotate token
                    </button>
                    <button type="button" className="btn btn-danger btn-sm" disabled={busy} onClick={() => setDeleteTarget(collector)}>
                      Delete
                    </button>
                  </>
                )}
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Register modal */}
      {showCreate && (
        <DialogLayer><div className="pp-modal-overlay" {...dialogBackdropProps(closeCreate)}>
          <div ref={createDialogRef} className="pp-modal" role="dialog" aria-modal="true" aria-labelledby="collector-create-dialog-title">
            <div className="pp-modal-header">
              <h3 id="collector-create-dialog-title">Register Monitoring Collector</h3>
								<button type="button" className="btn btn-ghost btn-sm" onClick={closeCreate} disabled={busy}>
                Close
              </button>
            </div>
            <div className="pp-modal-form">
              <label className="pp-field">
                <span>Name</span>
                <input
                  value={createForm.name}
                  onChange={e => setCreateForm(f => ({ ...f, name: e.target.value }))}
                  placeholder="e.g. edge-eu-collector"
                />
              </label>
              <label className="pp-field">
                <span>Description</span>
                <input
                  value={createForm.description}
                  onChange={e => setCreateForm(f => ({ ...f, description: e.target.value }))}
                  placeholder="Optional"
                />
              </label>
              <MetricPackPicker value={createForm.packs} onChange={packs => setCreateForm(form => ({ ...form, packs }))} />
            </div>
            <div className="pp-modal-actions">
								<button type="button" className="btn btn-secondary" onClick={closeCreate} disabled={busy}>
                Cancel
              </button>
              <button type="button" className="btn btn-primary" disabled={!createValid || busy} onClick={() => void submitCreate()}>
                {busy ? 'Registering…' : 'Register'}
              </button>
            </div>
          </div>
        </div></DialogLayer>
      )}

      {/* Edit modal */}
      {edit && (
        <DialogLayer><div className="pp-modal-overlay" {...dialogBackdropProps(closeEdit)}>
          <div ref={editDialogRef} className="pp-modal" role="dialog" aria-modal="true" aria-labelledby="collector-edit-dialog-title">
            <div className="pp-modal-header">
              <h3 id="collector-edit-dialog-title">Edit Collector</h3>
								<button type="button" className="btn btn-ghost btn-sm" onClick={closeEdit} disabled={busy}>
                Close
              </button>
            </div>
            <div className="pp-modal-form">
              <label className="pp-field">
                <span>Name</span>
                <input value={edit.name} onChange={e => setEdit(s => (s ? { ...s, name: e.target.value } : s))} />
              </label>
              <label className="pp-field">
                <span>Description</span>
                <input value={edit.description} onChange={e => setEdit(s => (s ? { ...s, description: e.target.value } : s))} />
              </label>
              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Manual status override</span>
                  <small>Normally set by the collector’s heartbeat. Override only to record a known outage.</small>
                  <select value={edit.health} onChange={e => setEdit(s => (s ? { ...s, health: e.target.value } : s))}>
                    <option value="healthy">healthy</option>
                    <option value="degraded">degraded</option>
                    <option value="unreachable">unreachable</option>
                  </select>
                </label>
                <MetricPackPicker value={edit.packs} onChange={packs => setEdit(form => form ? { ...form, packs } : form)} />
              </div>
            </div>
            <div className="pp-modal-actions">
								<button type="button" className="btn btn-secondary" onClick={closeEdit} disabled={busy}>
                Cancel
              </button>
              <button type="button" className="btn btn-primary" disabled={busy} onClick={() => void submitEdit()}>
                {busy ? 'Saving…' : 'Save'}
              </button>
            </div>
          </div>
        </div></DialogLayer>
      )}

      {/* One-time token reveal */}
      {oneTimeToken && (
        <DialogLayer><div className="pp-modal-overlay" {...dialogBackdropProps(closeToken)}>
          <div ref={tokenDialogRef} className="pp-modal" role="dialog" aria-modal="true" aria-labelledby="collector-token-dialog-title">
            <div className="pp-modal-header">
              <h3 id="collector-token-dialog-title">Collector token for “{oneTimeToken.name}”</h3>
              <button type="button" className="btn btn-ghost btn-sm" onClick={closeToken}>
                Close
              </button>
            </div>
            <div className="pp-token">
              <span className="pp-token-warning">This token is shown only once. Copy it now and store it securely.</span>
              <p>Collector ID: <code>{oneTimeToken.id}</code></p>
              <div className="pp-token-value">
                <code>{oneTimeToken.token}</code>
                <button type="button" className="btn btn-secondary btn-sm" onClick={() => void copyToken()}>
                  {copied ? 'Copied' : 'Copy'}
                </button>
              </div>
              {copyError && <span className="setting-error" role="alert">{copyError}</span>}
            </div>
            <div className="pp-modal-actions">
              <button type="button" className="btn btn-primary" onClick={closeToken}>
                Done
              </button>
            </div>
          </div>
        </div></DialogLayer>
      )}

      {/* Latest ingested metrics snapshot */}
      {metrics && (
        <DialogLayer><div className="pp-modal-overlay" {...dialogBackdropProps(closeMetrics)}>
          <div ref={metricsDialogRef} className="pp-modal" role="dialog" aria-modal="true" aria-labelledby="collector-metrics-dialog-title">
            <div className="pp-modal-header">
              <h3 id="collector-metrics-dialog-title">
                Latest captured metrics — “{metrics.name}”
                <span className={freshness(metrics.snapshot).cls} style={{ marginLeft: 8 }}>
                  {freshness(metrics.snapshot).label}
                </span>
              </h3>
              <button type="button" className="btn btn-ghost btn-sm" onClick={closeMetrics}>
                Close
              </button>
            </div>
            <div className="pp-modal-form">
              <p className="monitor-status-note">This is a snapshot of the last heartbeat. Refresh to check for new samples.</p>
              <button type="button" className="btn btn-secondary btn-sm" disabled={metricsLoading} onClick={() => void refreshMetrics()}>{metricsLoading ? 'Refreshing metrics…' : 'Refresh metrics'}</button>
              {metricsError && <p role="alert">{metricsError} The displayed snapshot may be stale.</p>}
              <div className="pp-row-meta">
                <span>health: {metrics.snapshot.health}</span>
                <span>last received: {formatTimestamp(metrics.snapshot.received_at)}</span>
                {typeof metrics.snapshot.age_seconds === 'number' ? <span>age: {metrics.snapshot.age_seconds}s</span> : null}
              </div>
              {metrics.snapshot.packs.length === 0 ? (
                <EmptyState title="No metrics ingested yet." hint="Run the collector agent to push a heartbeat." />
              ) : (
                <div className="pp-list">
                  {metrics.snapshot.packs.map(pack => (
                    <div className="pp-row" key={pack.pack}>
                      <div className="pp-row-main">
                        <div className="pp-row-title">{pack.pack}</div>
                        <div className="pp-row-meta">
                          {Object.entries(pack.samples || {}).map(([key, value]) => (
                            <span key={key}>
                              {key}: {Math.round(value * 100) / 100}
                            </span>
                          ))}
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
            <div className="pp-modal-actions">
              <button type="button" className="btn btn-primary" onClick={closeMetrics}>
                Done
              </button>
            </div>
          </div>
        </div></DialogLayer>
      )}

      {deleteTarget && (
        <ConfirmDestructiveModal
          title="Delete monitoring collector"
          description={`This permanently deletes “${deleteTarget.name}” and revokes its token.`}
          target={deleteTarget.name}
          targetLabel="Collector"
          confirmLabel="Delete collector"
          loading={busy}
          onCancel={() => setDeleteTarget(null)}
          onConfirm={() => void confirmDelete()}
        />
      )}
    </div>
  )
}
