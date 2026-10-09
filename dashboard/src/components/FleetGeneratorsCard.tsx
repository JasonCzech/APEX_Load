import DialogLayer from './ux/DialogLayer'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { useFleetApi, GeneratorMetadataPatch } from '../hooks/api/useFleetApi'
import { ApiError, request } from '../hooks/apiClient'
import { confirmAction } from './ux/confirmAction'
import { useNavigationGuard, useNavigationGuardRunner, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { useModalDialog } from '../hooks/useModalDialog'
import { dialogBackdropProps } from './dialogBackdropProps'
import { FleetGeneratorMetadata, FleetPool } from '../types'
import { listAllGeneratorMetadata, listAllFleetPools } from './fleetCatalogModel'
import './parityPanels.css'

interface Props {
  canManage?: boolean
}

type MetaDraft = {
  generator_id: string
  isNew: boolean
  target: string
  region: string
  fleet_pool_id: string
  provider_status: string
  cordoned: boolean
  tagsText: string
  baseline: Omit<MetaDraft, 'generator_id' | 'isNew' | 'baseline'> | null
}

function formatTags(tags?: Record<string, string>) {
  return Object.entries(tags || {})
    .filter(([key, value]) => key && value)
    .sort(([leftKey], [rightKey]) => leftKey.localeCompare(rightKey))
    .map(([key, value]) => `${key}=${value}`)
    .join(', ')
}

function parseTagsText(value: string): Record<string, string> {
  const tags: Record<string, string> = {}
  value
    .split(/[,\n]/)
    .map(entry => entry.trim())
    .filter(Boolean)
    .forEach(entry => {
      const separator = entry.includes('=') ? '=' : ':'
      const index = entry.indexOf(separator)
      if (index < 0) return
      const key = entry.slice(0, index).trim()
      const tagValue = entry.slice(index + 1).trim()
      if (key && tagValue) tags[key] = tagValue
    })
  return tags
}

function draftFields(draft: MetaDraft) {
  return {
    target: draft.target.trim(),
    region: draft.region.trim(),
    fleet_pool_id: draft.fleet_pool_id.trim(),
    provider_status: draft.provider_status.trim(),
    cordoned: draft.cordoned,
    tagsText: formatTags(parseTagsText(draft.tagsText)),
  }
}

function draftIsDirty(draft: MetaDraft | null): boolean {
  if (!draft) return false
  const fields = draftFields(draft)
  if (draft.isNew) {
    return Boolean(
      draft.generator_id.trim()
      || fields.target
      || fields.region
      || fields.fleet_pool_id
      || fields.provider_status
      || fields.cordoned
      || fields.tagsText,
    )
  }
  return JSON.stringify(fields) !== JSON.stringify(draft.baseline)
}

function changedMetadataPatch(draft: MetaDraft): GeneratorMetadataPatch {
  const fields = draftFields(draft)
  const target = fields.target || (draft.isNew ? draft.generator_id.trim() : draft.baseline?.target || '')
  if (draft.isNew || !draft.baseline) {
    return {
      target,
      region: fields.region,
      fleet_pool_id: fields.fleet_pool_id,
      provider_status: fields.provider_status,
      cordoned: fields.cordoned,
      tags: parseTagsText(draft.tagsText),
    }
  }

  const patch: GeneratorMetadataPatch = {}
  if (target !== draft.baseline.target) patch.target = target
  if (fields.region !== draft.baseline.region) patch.region = fields.region
  if (fields.fleet_pool_id !== draft.baseline.fleet_pool_id) patch.fleet_pool_id = fields.fleet_pool_id
  if (fields.provider_status !== draft.baseline.provider_status) patch.provider_status = fields.provider_status
  if (fields.cordoned !== draft.baseline.cordoned) patch.cordoned = fields.cordoned
  if (fields.tagsText !== draft.baseline.tagsText) patch.tags = parseTagsText(draft.tagsText)
  return patch
}

function snapshotCaughtUp(
  listed: { updated_at?: string },
  committed: { updated_at?: string },
): boolean {
  if (JSON.stringify(listed) === JSON.stringify(committed)) return true
  const listedTimestamp = Date.parse(listed.updated_at || '')
  const committedTimestamp = Date.parse(committed.updated_at || '')
  return Number.isFinite(listedTimestamp)
    && Number.isFinite(committedTimestamp)
    && listedTimestamp > committedTimestamp
}

function runtimeTargetError(target: string): string {
  if (!target) return ''
  if (target.includes('://')) return 'Use host or host:port, without http:// or another scheme.'
  if (/[\s/?#]/.test(target)) return 'Use host or host:port, without spaces or a path.'
  const ipv6 = /^\[([0-9a-fA-F:.]+)\]:(\d+)$/.exec(target)
  const hostPort = ipv6 ? ['', ipv6[1], ipv6[2]] : /^([^:]+)(?::(\d+))?$/.exec(target)
  if (!hostPort) return 'Use host or host:port.'
  const port = hostPort[2]
  if (port !== undefined && (Number(port) < 1 || Number(port) > 65535)) return 'The port must be between 1 and 65535.'
  if (!ipv6 && !/^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$/.test(hostPort[1])) return 'The host name is not valid.'
  return ''
}

export default function FleetGeneratorsCard({ canManage = false }: Props) {
  const api = useFleetApi()
  // Which generators are actually connected: a metadata row alone does not make
  // a generator schedulable. null when the live inventory cannot be read.
  const [liveGenerators, setLiveGenerators] = useState<Set<string> | null>(null)
  useEffect(() => {
    let cancelled = false
    void request<{ execution_mode?: string; generators?: { generator_id?: string; target?: string }[] }>('/system/inventory')
      .then(inventory => {
        if (cancelled) return
        // The in-process engine has no generator inventory to enroll into, so
        // enrollment cannot be judged there.
        setLiveGenerators(inventory?.execution_mode === 'distributed'
          ? new Set((inventory.generators || []).flatMap(entry => [entry.generator_id, entry.target]).filter((value): value is string => Boolean(value)))
          : null)
      })
      .catch(() => { if (!cancelled) setLiveGenerators(null) })
    return () => { cancelled = true }
  }, [])
  const [pools, setPools] = useState<FleetPool[]>([])
  const [poolError, setPoolError] = useState('')
  useEffect(() => {
    let current = true
    listAllFleetPools(api.listFleetPools).then(rows => { if (current) { setPools(rows); setPoolError('') } }).catch(() => { if (current) setPoolError('Pool catalog unavailable. Existing assignments are preserved.') })
    return () => { current = false }
  }, [api.listFleetPools])
  const { runGuarded } = useNavigationGuardRunner()
  const [generators, setGenerators] = useState<FleetGeneratorMetadata[]>([])
  const generatorsRef = useRef(generators)
  generatorsRef.current = generators
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [draft, setDraft] = useState<MetaDraft | null>(null)
  const [busy, setBusy] = useState(false)
  const loadRequestRef = useRef(0)
  const [loadError, setLoadError] = useState(false)
  const committedGeneratorsRef = useRef(new Map<string, FleetGeneratorMetadata>())
  const actionInFlightRef = useRef(false)
  const dialogRef = useRef<HTMLDivElement>(null)
  const discardDraft = useCallback(() => {
    if (!actionInFlightRef.current) setDraft(null)
  }, [])
  const closeDraft = useCallback(() => {
    if (!actionInFlightRef.current) runGuarded(discardDraft)
  }, [discardDraft, runGuarded])

  useModalDialog({ dialogRef, enabled: Boolean(draft), onClose: busy ? undefined : closeDraft })

  const load = useCallback(async () => {
    const requestId = ++loadRequestRef.current
    const requestIsCurrent = () => loadRequestRef.current === requestId
    setLoading(true)
    setError(null)
    setLoadError(false)
    try {
      const listed = await listAllGeneratorMetadata(api.listGeneratorMetadata)
      if (!requestIsCurrent()) return false
      const next = [...listed]
      committedGeneratorsRef.current.forEach((generator, id) => {
        const listed = next.find(candidate => candidate.generator_id === id)
        if (listed && snapshotCaughtUp(listed, generator)) {
          committedGeneratorsRef.current.delete(id)
          return
        }
        const index = next.findIndex(candidate => candidate.generator_id === id)
        if (index >= 0) next[index] = generator
        else next.push(generator)
      })
      setGenerators(next.sort((left, right) => left.generator_id.localeCompare(right.generator_id)))
      return true
    } catch (err) {
      if (!requestIsCurrent()) return false
      setError(err instanceof Error ? err.message : 'Failed to load generator metadata.')
      setLoadError(true)
      return false
    } finally {
      if (requestIsCurrent()) setLoading(false)
    }
  }, [api])

  useEffect(() => {
    void load()
  }, [load])

  const patch = useCallback(
    async (generatorId: string, body: GeneratorMetadataPatch): Promise<boolean> => {
      if (actionInFlightRef.current) return false
      actionInFlightRef.current = true
      setBusy(true)
      setError(null)
      setLoadError(false)
      try {
        const committed = await api.patchGeneratorMetadata(generatorId, body)
        committedGeneratorsRef.current.set(committed.generator_id, committed)
        setGenerators(current => [
          ...current.filter(generator => generator.generator_id !== committed.generator_id),
          committed,
        ].sort((left, right) => left.generator_id.localeCompare(right.generator_id)))
        await load()
        return true
      } catch (err) {
        const message = err instanceof ApiError ? err.message : 'Failed to update generator metadata.'
        const refreshed = await load()
        setError(refreshed
          ? `${message} Current generator metadata was reloaded; verify the row before retrying.`
          : `${message} Current generator metadata could not be reloaded; retry the catalog refresh before editing again.`)
        setLoadError(!refreshed)
        return false
      } finally {
        actionInFlightRef.current = false
        setBusy(false)
      }
    },
    [api, load],
  )

  const toggleCordon = useCallback(
    async (generatorId: string, cordoned: boolean) => {
      if (actionInFlightRef.current) return
      actionInFlightRef.current = true
      setBusy(true)
      setError(null)
      setLoadError(false)
      try {
        let result
        if (cordoned) {
          result = await api.uncordonGenerator(generatorId)
        } else {
          result = await api.cordonGenerator(generatorId)
        }
        const existing = generatorsRef.current.find(generator => generator.generator_id === generatorId)
        if (existing) committedGeneratorsRef.current.set(generatorId, { ...existing, cordoned: result.cordoned })
        setGenerators(current => current.map(generator => (
          generator.generator_id === generatorId ? { ...generator, cordoned: result.cordoned } : generator
        )))
        await load()
      } catch (err) {
        const message = err instanceof ApiError ? err.message : 'Failed to update generator lifecycle.'
        const refreshed = await load()
        setError(refreshed
          ? `${message} Current generator metadata was reloaded; verify the lifecycle state before retrying.`
          : `${message} Current generator metadata could not be reloaded; retry the catalog refresh before acting again.`)
        setLoadError(!refreshed)
      } finally {
        actionInFlightRef.current = false
        setBusy(false)
      }
    },
    [api, load],
  )

  const submitDraft = useCallback(async () => {
    if (!draft || !draft.generator_id.trim()) return false
    const body = changedMetadataPatch(draft)
    if (Object.keys(body).length === 0) {
      setDraft(null)
      return true
    }
    const saved = await patch(draft.generator_id.trim(), body)
    if (saved) setDraft(null)
    return saved
  }, [draft, patch])

  // Same rule as the server: host or host:port (IPv6 in brackets), not a URL.
  const draftTargetError = draft ? runtimeTargetError(draft.target.trim()) : ''
  const removeMetadata = useCallback(async (generatorId: string) => {
    if (!(await confirmAction(`Remove the metadata for generator “${generatorId}”? Use this for a mistyped or decommissioned generator; a connected or pooled generator cannot be removed.`, { title: 'Remove generator metadata', confirmLabel: 'Remove', destructive: true }))) return
    setBusy(true)
    setError(null)
    try {
      await api.deleteGeneratorMetadata(generatorId)
      committedGeneratorsRef.current.delete(generatorId)
      setGenerators(current => current.filter(generator => generator.generator_id !== generatorId))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to remove generator metadata.')
    } finally {
      setBusy(false)
    }
  }, [api])
  const draftDirty = draftIsDirty(draft)
  const draftDirtyRef = useRef(false)
  draftDirtyRef.current = draftDirty
  const submitDraftRef = useRef(submitDraft)
  submitDraftRef.current = submitDraft
  const draftGuard = useMemo<NavigationGuard>(() => ({
    id: 'fleet-generator-metadata-draft',
    label: 'Generator metadata editor',
    isDirty: () => draftDirtyRef.current,
    save: () => submitDraftRef.current(),
    discard: discardDraft,
  }), [discardDraft])
  useNavigationGuard(draft ? draftGuard : null, draftDirty)

  return (
    <div className="monitor-body pp-panel">
      <div className="pp-toolbar">
        <span className="pp-toolbar-title">
          {generators.length} generator{generators.length === 1 ? '' : 's'} with persisted metadata
        </span>
        {canManage && (
          <button
            type="button"
            className="btn btn-primary btn-sm"
            disabled={busy}
            onClick={() => setDraft({ generator_id: '', isNew: true, target: '', region: '', fleet_pool_id: '', provider_status: '', cordoned: false, tagsText: '', baseline: null })}
          >
            Add Metadata
          </button>
        )}
      </div>

      {error && (
        <div>
          <div className="pp-error" role="alert">{error}</div>
          {loadError && (
            <button type="button" className="btn btn-secondary btn-sm" disabled={loading || busy} onClick={() => { void load() }}>
              Retry generator metadata
            </button>
          )}
        </div>
      )}

      {loading ? (
        <div className="pp-empty">Loading generator metadata…</div>
      ) : generators.length === 0 ? (
        <div className="pp-empty">No generator metadata yet. Label generators with a region, pool, or cordon state.</div>
      ) : (
        <div className="pp-list">
          {generators.map(gen => (
            <div className="pp-row" key={gen.generator_id}>
              <div className="pp-row-main">
                <div className="pp-row-title">
                  {gen.generator_id}
                  {gen.cordoned
                    ? <span className="pp-pill pp-pill-warning">cordoned</span>
                    : liveGenerators && !liveGenerators.has(gen.generator_id) && !(gen.target && liveGenerators.has(gen.target))
                      ? <span className="pp-pill pp-pill-muted" title="No connected generator reports this ID or target yet.">awaiting enrollment</span>
                      : <span className="pp-pill pp-pill-success">schedulable</span>}
                </div>
                <div className="pp-row-meta">
                  {gen.region ? <span>region: {gen.region}</span> : null}
                  {gen.target ? <span>target: {gen.target}</span> : <span>target: not configured</span>}
                  {gen.fleet_pool_id ? <span>pool: {gen.fleet_pool_id}</span> : <span>pool: —</span>}
                  {gen.provider_status ? <span>{gen.provider_status}</span> : null}
                  {formatTags(gen.tags) ? <span>capabilities: {formatTags(gen.tags)}</span> : null}
                </div>
              </div>
              {canManage && (
                <div className="pp-row-actions">
                  <button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => void toggleCordon(gen.generator_id, Boolean(gen.cordoned))}>
                    {gen.cordoned ? 'Uncordon' : 'Cordon'}
                  </button>
                  <button type="button" className="btn btn-ghost btn-sm" disabled={busy} aria-label={`Remove metadata for ${gen.generator_id}`} onClick={() => void removeMetadata(gen.generator_id)}>
                    Remove
                  </button>
                  <button
                    type="button"
                    className="btn btn-ghost btn-sm"
                    disabled={busy}
                    onClick={() =>
                      setDraft({
                        generator_id: gen.generator_id,
                        isNew: false,
                        target: gen.target || '',
                        region: gen.region || '',
                        fleet_pool_id: gen.fleet_pool_id || '',
                        provider_status: gen.provider_status || '',
                        cordoned: Boolean(gen.cordoned),
                        tagsText: formatTags(gen.tags),
                        baseline: {
                          target: gen.target || '',
                          region: (gen.region || '').trim(),
                          fleet_pool_id: (gen.fleet_pool_id || '').trim(),
                          provider_status: (gen.provider_status || '').trim(),
                          cordoned: Boolean(gen.cordoned),
                          tagsText: formatTags(gen.tags),
                        },
                      })
                    }
                  >
                    Edit
                  </button>
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      {draft && (
        <DialogLayer><div className="pp-modal-overlay" {...dialogBackdropProps(closeDraft)}>
          <div
            ref={dialogRef}
            className="pp-modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="fleet-generator-dialog-title"
          >
            <div className="pp-modal-header">
              <h3 id="fleet-generator-dialog-title">{draft.isNew ? 'Add Generator Metadata' : 'Edit Generator Metadata'}</h3>
              <button type="button" className="btn btn-ghost btn-sm" onClick={closeDraft} disabled={busy}>Close</button>
            </div>
            <div className="pp-modal-form">
              <label className="pp-field">
                <span>Generator ID</span>
                <input value={draft.generator_id} disabled={!draft.isNew || busy} onChange={e => setDraft(d => (d ? { ...d, generator_id: e.target.value } : d))} placeholder="stable generator identity" />
              </label>
              <label className="pp-field">
                <span>Runtime target</span>
                <input
                  value={draft.target}
                  disabled={busy}
                  aria-invalid={Boolean(draftTargetError) || undefined}
                  aria-describedby={draftTargetError ? 'generator-target-error' : undefined}
                  onChange={e => setDraft(d => (d ? { ...d, target: e.target.value } : d))}
                  placeholder={draft.generator_id.trim() || 'host:port or pod DNS name'}
                />
                {draftTargetError && <small id="generator-target-error" className="field-error" role="alert">{draftTargetError}</small>}
                <small className="pp-muted">Required for pool placement. Blank defaults to the generator ID.</small>
              </label>
              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Region</span>
                  <input value={draft.region} disabled={busy} onChange={e => setDraft(d => (d ? { ...d, region: e.target.value } : d))} placeholder="optional" />
                </label>
                <label className="pp-field">
                  <span>Fleet pool</span>
                  <select value={draft.fleet_pool_id} disabled={busy} onChange={e => setDraft(d => (d ? { ...d, fleet_pool_id: e.target.value } : d))}><option value="">No pool</option>{pools.map(pool => <option key={pool.id} value={pool.id}>{pool.name}</option>)}{draft.fleet_pool_id && !pools.some(pool => pool.id === draft.fleet_pool_id) && <option value={draft.fleet_pool_id}>Unavailable pool ({draft.fleet_pool_id})</option>}</select>
                  {poolError && <span role="status">{poolError}</span>}
                </label>
              </div>
              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Provider status</span>
                  <input value={draft.provider_status} disabled={busy} onChange={e => setDraft(d => (d ? { ...d, provider_status: e.target.value } : d))} placeholder="optional" />
                </label>
                <label className="pp-field pp-entry-enabled" style={{ justifyContent: 'flex-start' }}>
                  <span>Cordoned</span>
                  <input type="checkbox" checked={draft.cordoned} disabled={busy} onChange={e => setDraft(d => (d ? { ...d, cordoned: e.target.checked } : d))} />
                </label>
              </div>
              <label className="pp-field">
                <span>Capabilities / labels</span>
                <textarea
                  value={draft.tagsText}
                  disabled={busy}
                  onChange={e => setDraft(d => (d ? { ...d, tagsText: e.target.value } : d))}
                  placeholder="jvm=17, arch=amd64"
                  rows={3}
                />
              </label>
            </div>
            <div className="pp-modal-actions">
              <button type="button" className="btn btn-secondary" onClick={closeDraft} disabled={busy}>Cancel</button>
              <button
                type="button"
                className="btn btn-primary"
                disabled={busy || !draft.generator_id.trim() || Boolean(draftTargetError) || Boolean(draft.fleet_pool_id.trim() && !(draft.target.trim() || (draft.isNew && draft.generator_id.trim())))}
                onClick={() => void submitDraft()}
              >
                {busy ? 'Saving…' : 'Save'}
              </button>
            </div>
          </div>
        </div></DialogLayer>
      )}
    </div>
  )
}
