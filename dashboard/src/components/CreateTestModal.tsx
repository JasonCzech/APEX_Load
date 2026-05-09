import { useState } from 'react'
import { useApi } from '../hooks/useApi'
import { StoredScript } from '../types'
import './CreateTestModal.css'

interface GroupEntry {
  name: string
  scriptId: string
  vusers: number
  rampUp: string
}

interface Props {
  scripts: StoredScript[]
  onClose: () => void
  onCreated: (test: any) => void
}

export default function CreateTestModal({ scripts, onClose, onCreated }: Props) {
  const api = useApi()
  const [name, setName] = useState('')
  const [duration, setDuration] = useState('1m')
  const [groups, setGroups] = useState<GroupEntry[]>([
    { name: '', scriptId: scripts[0]?.id || '', vusers: 10, rampUp: '' },
  ])
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const updateGroup = (idx: number, patch: Partial<GroupEntry>) => {
    setGroups((prev) => prev.map((g, i) => (i === idx ? { ...g, ...patch } : g)))
  }

  const addGroup = () => {
    setGroups((prev) => [
      ...prev,
      { name: '', scriptId: scripts[0]?.id || '', vusers: 10, rampUp: '' },
    ])
  }

  const removeGroup = (idx: number) => {
    if (groups.length <= 1) return
    setGroups((prev) => prev.filter((_, i) => i !== idx))
  }

  const totalVUsers = groups.reduce((sum, g) => sum + g.vusers, 0)

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const payload: any = { name: name || undefined, duration }

      if (groups.length === 1 && !groups[0].name) {
        // Single group — use shorthand
        payload.script_id = groups[0].scriptId
        payload.vusers = groups[0].vusers
        payload.ramp_up = groups[0].rampUp || undefined
      } else {
        payload.groups = groups.map((g) => ({
          name: g.name || undefined,
          script_id: g.scriptId,
          vusers: g.vusers,
          ramp_up: g.rampUp || undefined,
        }))
      }

      const test = await api.createTest(payload)
      onCreated(test)
    } catch (err: any) {
      setError(err.message)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal modal-wide" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <h3>Create Test</h3>
          <span className="total-vusers">{totalVUsers} total VUsers</span>
        </div>
        <form onSubmit={handleSubmit} className="modal-form">
          <div className="form-row">
            <label className="flex-2">
              <span>Test Name</span>
              <input
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Optional — auto-generated if empty"
              />
            </label>
            <label>
              <span>Duration</span>
              <input
                type="text"
                value={duration}
                onChange={(e) => setDuration(e.target.value)}
                placeholder="e.g. 5m, 1h"
                required
              />
            </label>
          </div>

          <div className="groups-section">
            <div className="groups-header">
              <h4>VUser Groups</h4>
              <button type="button" className="btn btn-secondary btn-sm" onClick={addGroup}>
                + Add Group
              </button>
            </div>

            {groups.map((group, idx) => (
              <div key={idx} className="group-row">
                <div className="group-number">{idx + 1}</div>
                <div className="group-fields">
                  <label>
                    <span>Group Name</span>
                    <input
                      type="text"
                      value={group.name}
                      onChange={(e) => updateGroup(idx, { name: e.target.value })}
                      placeholder={`Group ${idx + 1}`}
                    />
                  </label>
                  <label>
                    <span>Script</span>
                    <select
                      value={group.scriptId}
                      onChange={(e) => updateGroup(idx, { scriptId: e.target.value })}
                      required
                    >
                      {scripts.length === 0 && <option value="">No scripts</option>}
                      {scripts.map((s) => (
                        <option key={s.id} value={s.id}>
                          {s.name} ({s.protocol})
                        </option>
                      ))}
                    </select>
                  </label>
                  <label className="narrow">
                    <span>VUsers</span>
                    <input
                      type="number"
                      value={group.vusers}
                      onChange={(e) => updateGroup(idx, { vusers: Number(e.target.value) })}
                      min={1}
                      max={100000}
                      required
                    />
                  </label>
                  <label className="narrow">
                    <span>Ramp-up</span>
                    <input
                      type="text"
                      value={group.rampUp}
                      onChange={(e) => updateGroup(idx, { rampUp: e.target.value })}
                      placeholder="e.g. 30s"
                    />
                  </label>
                  {groups.length > 1 && (
                    <button
                      type="button"
                      className="btn-remove"
                      onClick={() => removeGroup(idx)}
                      title="Remove group"
                    >
                      x
                    </button>
                  )}
                </div>
              </div>
            ))}
          </div>

          {error && <p className="form-error">{error}</p>}

          <div className="modal-actions">
            <button type="button" className="btn btn-secondary" onClick={onClose}>
              Cancel
            </button>
            <button
              type="submit"
              className="btn btn-primary"
              disabled={loading || groups.every((g) => !g.scriptId)}
            >
              {loading ? 'Creating...' : `Create Test (${groups.length} group${groups.length > 1 ? 's' : ''})`}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
