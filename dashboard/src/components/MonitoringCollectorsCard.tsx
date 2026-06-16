import { useCallback, useEffect, useMemo, useState } from 'react'

import { useMonitoringCollectorsApi } from '../hooks/api/useMonitoringCollectorsApi'
import { ApiError } from '../hooks/apiClient'
import { MonitoringCollector } from '../types'
import ConfirmDestructiveModal from './ConfirmDestructiveModal'
import './parityPanels.css'

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

function formatTimestamp(value?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

type EditState = { id: string; name: string; description: string; health: string; packs: string }

export default function MonitoringCollectorsCard({ projectId, canManage = false }: Props) {
  const api = useMonitoringCollectorsApi()
  const [collectors, setCollectors] = useState<MonitoringCollector[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [showCreate, setShowCreate] = useState(false)
  const [createForm, setCreateForm] = useState({ name: '', description: '', packs: '' })
  const [edit, setEdit] = useState<EditState | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<MonitoringCollector | null>(null)
  const [oneTimeToken, setOneTimeToken] = useState<{ name: string; token: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const [copied, setCopied] = useState(false)

  const load = useCallback(async () => {
    if (!projectId) {
      setCollectors([])
      return
    }
    setLoading(true)
    setError(null)
    try {
      const res = await api.listCollectors({ project_id: projectId })
      setCollectors(res.collectors || [])
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to load monitoring collectors.')
      setCollectors([])
    } finally {
      setLoading(false)
    }
  }, [api, projectId])

  useEffect(() => {
    void load()
  }, [load])

  const resetCreate = () => setCreateForm({ name: '', description: '', packs: '' })

  const submitCreate = useCallback(async () => {
    if (!projectId || !createForm.name.trim()) return
    setBusy(true)
    setError(null)
    try {
      const res = await api.createCollector({
        project_id: projectId,
        name: createForm.name.trim(),
        description: createForm.description.trim() || undefined,
        allowed_metric_packs: textToPacks(createForm.packs),
      })
      setShowCreate(false)
      resetCreate()
      setOneTimeToken({ name: res.collector.name, token: res.token })
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to register collector.')
    } finally {
      setBusy(false)
    }
  }, [api, createForm, load, projectId])

  const submitEdit = useCallback(async () => {
    if (!edit) return
    setBusy(true)
    setError(null)
    try {
      await api.patchCollector(edit.id, {
        name: edit.name.trim() || undefined,
        description: edit.description,
        health: edit.health,
        allowed_metric_packs: textToPacks(edit.packs),
      })
      setEdit(null)
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to update collector.')
    } finally {
      setBusy(false)
    }
  }, [api, edit, load])

  const rotate = useCallback(
    async (collector: MonitoringCollector) => {
      setBusy(true)
      setError(null)
      try {
        const res = await api.rotateCollectorToken(collector.id)
        setOneTimeToken({ name: collector.name, token: res.token })
        await load()
      } catch (err) {
        setError(err instanceof ApiError ? err.message : 'Failed to rotate token.')
      } finally {
        setBusy(false)
      }
    },
    [api, load],
  )

  const confirmDelete = useCallback(async () => {
    if (!deleteTarget) return
    setBusy(true)
    setError(null)
    try {
      await api.deleteCollector(deleteTarget.id)
      setDeleteTarget(null)
      await load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to delete collector.')
    } finally {
      setBusy(false)
    }
  }, [api, deleteTarget, load])

  const copyToken = useCallback(() => {
    if (!oneTimeToken) return
    void navigator.clipboard?.writeText(oneTimeToken.token).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    })
  }, [oneTimeToken])

  const createValid = useMemo(() => Boolean(projectId && createForm.name.trim()), [projectId, createForm.name])

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
              resetCreate()
              setShowCreate(true)
            }}
          >
            Register Collector
          </button>
        )}
      </div>

      {error && <div className="pp-error">{error}</div>}

      {!projectId ? (
        <div className="pp-empty">Select a project to view its monitoring collectors.</div>
      ) : loading ? (
        <div className="pp-empty">Loading collectors…</div>
      ) : collectors.length === 0 ? (
        <div className="pp-empty">
          No monitoring collectors registered. Collectors are restricted-network agents that report
          scoped metric packs.
        </div>
      ) : (
        <div className="pp-list">
          {collectors.map(collector => (
            <div className="pp-row" key={collector.id}>
              <div className="pp-row-main">
                <div className="pp-row-title">
                  {collector.name}
                  <span className={healthPillClass(collector.health)}>{collector.health}</span>
                </div>
                <div className="pp-row-meta">
                  <span>packs: {collector.allowed_metric_packs?.length ? packsToText(collector.allowed_metric_packs) : 'none'}</span>
                  <span>last seen: {formatTimestamp(collector.last_seen_at)}</span>
                </div>
              </div>
              {canManage && (
                <div className="pp-row-actions">
                  <button
                    type="button"
                    className="btn btn-ghost btn-sm"
                    disabled={busy}
                    onClick={() =>
                      setEdit({
                        id: collector.id,
                        name: collector.name,
                        description: collector.description || '',
                        health: collector.health,
                        packs: packsToText(collector.allowed_metric_packs),
                      })
                    }
                  >
                    Edit
                  </button>
                  <button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => void rotate(collector)}>
                    Rotate token
                  </button>
                  <button type="button" className="btn btn-danger btn-sm" disabled={busy} onClick={() => setDeleteTarget(collector)}>
                    Delete
                  </button>
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      {/* Register modal */}
      {showCreate && (
        <div className="pp-modal-overlay" onClick={() => setShowCreate(false)}>
          <div className="pp-modal" onClick={event => event.stopPropagation()}>
            <div className="pp-modal-header">
              <h3>Register Monitoring Collector</h3>
              <button type="button" className="btn btn-ghost btn-sm" onClick={() => setShowCreate(false)}>
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
                  autoFocus
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
              <label className="pp-field">
                <span>Allowed metric packs (comma-separated)</span>
                <input
                  value={createForm.packs}
                  onChange={e => setCreateForm(f => ({ ...f, packs: e.target.value }))}
                  placeholder="oracle-db, host-cpu"
                />
              </label>
            </div>
            <div className="pp-modal-actions">
              <button type="button" className="btn btn-secondary" onClick={() => setShowCreate(false)}>
                Cancel
              </button>
              <button type="button" className="btn btn-primary" disabled={!createValid || busy} onClick={() => void submitCreate()}>
                {busy ? 'Registering…' : 'Register'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Edit modal */}
      {edit && (
        <div className="pp-modal-overlay" onClick={() => setEdit(null)}>
          <div className="pp-modal" onClick={event => event.stopPropagation()}>
            <div className="pp-modal-header">
              <h3>Edit Collector</h3>
              <button type="button" className="btn btn-ghost btn-sm" onClick={() => setEdit(null)}>
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
                  <span>Health</span>
                  <select value={edit.health} onChange={e => setEdit(s => (s ? { ...s, health: e.target.value } : s))}>
                    <option value="healthy">healthy</option>
                    <option value="degraded">degraded</option>
                    <option value="unreachable">unreachable</option>
                  </select>
                </label>
                <label className="pp-field">
                  <span>Metric packs</span>
                  <input value={edit.packs} onChange={e => setEdit(s => (s ? { ...s, packs: e.target.value } : s))} placeholder="oracle-db, host-cpu" />
                </label>
              </div>
            </div>
            <div className="pp-modal-actions">
              <button type="button" className="btn btn-secondary" onClick={() => setEdit(null)}>
                Cancel
              </button>
              <button type="button" className="btn btn-primary" disabled={busy} onClick={() => void submitEdit()}>
                {busy ? 'Saving…' : 'Save'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* One-time token reveal */}
      {oneTimeToken && (
        <div className="pp-modal-overlay" onClick={() => setOneTimeToken(null)}>
          <div className="pp-modal" onClick={event => event.stopPropagation()}>
            <div className="pp-modal-header">
              <h3>Collector token for “{oneTimeToken.name}”</h3>
              <button type="button" className="btn btn-ghost btn-sm" onClick={() => setOneTimeToken(null)}>
                Close
              </button>
            </div>
            <div className="pp-token">
              <span className="pp-token-warning">This token is shown only once. Copy it now and store it securely.</span>
              <div className="pp-token-value">
                <code>{oneTimeToken.token}</code>
                <button type="button" className="btn btn-secondary btn-sm" onClick={copyToken}>
                  {copied ? 'Copied' : 'Copy'}
                </button>
              </div>
            </div>
            <div className="pp-modal-actions">
              <button type="button" className="btn btn-primary" onClick={() => setOneTimeToken(null)}>
                Done
              </button>
            </div>
          </div>
        </div>
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
