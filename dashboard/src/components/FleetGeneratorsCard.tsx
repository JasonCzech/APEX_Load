import { useCallback, useEffect, useState } from 'react'

import { useFleetApi, GeneratorMetadataPatch } from '../hooks/api/useFleetApi'
import { ApiError } from '../hooks/apiClient'
import { FleetGeneratorMetadata } from '../types'
import './parityPanels.css'

interface Props {
  canManage?: boolean
}

type MetaDraft = {
  generator_id: string
  isNew: boolean
  region: string
  fleet_pool_id: string
  provider_status: string
  cordoned: boolean
}

export default function FleetGeneratorsCard({ canManage = false }: Props) {
  const api = useFleetApi()
  const [generators, setGenerators] = useState<FleetGeneratorMetadata[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [draft, setDraft] = useState<MetaDraft | null>(null)
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await api.listGeneratorMetadata()
      setGenerators(res.generators || [])
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Failed to load generator metadata.')
      setGenerators([])
    } finally {
      setLoading(false)
    }
  }, [api])

  useEffect(() => {
    void load()
  }, [load])

  const patch = useCallback(
    async (generatorId: string, body: GeneratorMetadataPatch) => {
      setBusy(true)
      setError(null)
      try {
        await api.patchGeneratorMetadata(generatorId, body)
        await load()
      } catch (err) {
        setError(err instanceof ApiError ? err.message : 'Failed to update generator metadata.')
      } finally {
        setBusy(false)
      }
    },
    [api, load],
  )

  const submitDraft = useCallback(async () => {
    if (!draft || !draft.generator_id.trim()) return
    await patch(draft.generator_id.trim(), {
      region: draft.region,
      fleet_pool_id: draft.fleet_pool_id, // empty string clears membership (backend convention)
      provider_status: draft.provider_status,
      cordoned: draft.cordoned,
    })
    setDraft(null)
  }, [draft, patch])

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
            onClick={() => setDraft({ generator_id: '', isNew: true, region: '', fleet_pool_id: '', provider_status: '', cordoned: false })}
          >
            Add Metadata
          </button>
        )}
      </div>

      {error && <div className="pp-error">{error}</div>}

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
                  {gen.cordoned ? <span className="pp-pill pp-pill-warning">cordoned</span> : <span className="pp-pill pp-pill-success">schedulable</span>}
                </div>
                <div className="pp-row-meta">
                  {gen.region ? <span>region: {gen.region}</span> : null}
                  {gen.fleet_pool_id ? <span>pool: {gen.fleet_pool_id}</span> : <span>pool: —</span>}
                  {gen.provider_status ? <span>{gen.provider_status}</span> : null}
                </div>
              </div>
              {canManage && (
                <div className="pp-row-actions">
                  <button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => void patch(gen.generator_id, { cordoned: !gen.cordoned })}>
                    {gen.cordoned ? 'Uncordon' : 'Cordon'}
                  </button>
                  <button
                    type="button"
                    className="btn btn-ghost btn-sm"
                    disabled={busy}
                    onClick={() =>
                      setDraft({
                        generator_id: gen.generator_id,
                        isNew: false,
                        region: gen.region || '',
                        fleet_pool_id: gen.fleet_pool_id || '',
                        provider_status: gen.provider_status || '',
                        cordoned: Boolean(gen.cordoned),
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
        <div className="pp-modal-overlay" onClick={() => setDraft(null)}>
          <div className="pp-modal" onClick={event => event.stopPropagation()}>
            <div className="pp-modal-header">
              <h3>{draft.isNew ? 'Add Generator Metadata' : 'Edit Generator Metadata'}</h3>
              <button type="button" className="btn btn-ghost btn-sm" onClick={() => setDraft(null)}>Close</button>
            </div>
            <div className="pp-modal-form">
              <label className="pp-field">
                <span>Generator ID</span>
                <input value={draft.generator_id} disabled={!draft.isNew} autoFocus={draft.isNew} onChange={e => setDraft(d => (d ? { ...d, generator_id: e.target.value } : d))} placeholder="host:port or pod name" />
              </label>
              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Region</span>
                  <input value={draft.region} onChange={e => setDraft(d => (d ? { ...d, region: e.target.value } : d))} placeholder="optional" />
                </label>
                <label className="pp-field">
                  <span>Fleet pool id</span>
                  <input value={draft.fleet_pool_id} onChange={e => setDraft(d => (d ? { ...d, fleet_pool_id: e.target.value } : d))} placeholder="blank = none" />
                </label>
              </div>
              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Provider status</span>
                  <input value={draft.provider_status} onChange={e => setDraft(d => (d ? { ...d, provider_status: e.target.value } : d))} placeholder="optional" />
                </label>
                <label className="pp-field pp-entry-enabled" style={{ justifyContent: 'flex-start' }}>
                  <span>Cordoned</span>
                  <input type="checkbox" checked={draft.cordoned} onChange={e => setDraft(d => (d ? { ...d, cordoned: e.target.checked } : d))} />
                </label>
              </div>
            </div>
            <div className="pp-modal-actions">
              <button type="button" className="btn btn-secondary" onClick={() => setDraft(null)}>Cancel</button>
              <button type="button" className="btn btn-primary" disabled={busy || !draft.generator_id.trim()} onClick={() => void submitDraft()}>
                {busy ? 'Saving…' : 'Save'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
