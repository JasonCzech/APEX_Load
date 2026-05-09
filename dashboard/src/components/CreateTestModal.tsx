import { useState } from 'react'
import { useApi } from '../hooks/useApi'
import { StoredScript } from '../types'
import { AreaChart, Area, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer } from 'recharts'
import './CreateTestModal.css'

// Helper to parse duration strings (e.g. "1m", "30s") to seconds
function parseDuration(s: string): number {
  if (!s) return 0
  const match = s.match(/^(\d+)([smh])$/)
  if (!match) return 0
  const val = parseInt(match[1])
  const unit = match[2]
  switch (unit) {
    case 's': return val
    case 'm': return val * 60
    case 'h': return val * 3600
    default: return 0
  }
}

// Helper to format seconds to time string
function formatTime(seconds: number): string {
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  const s = seconds % 60
  if (h > 0) return `${h}h${m}m${s}s`
  if (m > 0) return `${m}m${s}s`
  return `${s}s`
}

interface GroupEntry {
  name: string
  scriptId: string
  vusers: number
  rampUp: string
  rampDown: string
  initialization: 'at_runtime' | 'at_once'
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
    { name: '', scriptId: scripts[0]?.id || '', vusers: 10, rampUp: '', rampDown: '', initialization: 'at_runtime' },
  ])
  const [runtimeSettings, setRuntimeSettings] = useState({
    iterations: undefined as number | undefined,
    log_level: '',
    auto_transactions: '',
    snapshot_on_error: false,
  })
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const updateGroup = (idx: number, patch: Partial<GroupEntry>) => {
    setGroups((prev) => prev.map((g, i) => (i === idx ? { ...g, ...patch } : g)))
  }

  const addGroup = () => {
    setGroups((prev) => [
      ...prev,
      { name: '', scriptId: scripts[0]?.id || '', vusers: 10, rampUp: '', rampDown: '', initialization: 'at_runtime' },
    ])
  }

  const removeGroup = (idx: number) => {
    if (groups.length <= 1) return
    setGroups((prev) => prev.filter((_, i) => i !== idx))
  }

  const totalVUsers = groups.reduce((sum, g) => sum + g.vusers, 0)

  // Calculate chart data
  const chartData = (() => {
    const durSec = parseDuration(duration)
    let maxRampDown = 0
    groups.forEach(g => {
      const rd = parseDuration(g.rampDown)
      if (rd > maxRampDown) maxRampDown = rd
    })

    const totalSec = durSec + maxRampDown
    if (totalSec === 0) return []

    const points: any[] = []
    const step = Math.max(1, Math.floor(totalSec / 30)) // More points for clarity

    for (let t = 0; t <= totalSec; t += step) {
      const point: any = { time: t, total: 0 }
      groups.forEach((g, idx) => {
        const gVUsers = g.vusers
        const gRampUp = parseDuration(g.rampUp)
        const gRampDown = parseDuration(g.rampDown)

        let v = 0
        if (t < gRampUp) {
          v = gRampUp > 0 ? (t / gRampUp) * gVUsers : gVUsers
        } else if (t < durSec) {
          v = gVUsers
        } else if (t < durSec + gRampDown) {
          v = gRampDown > 0 ? (1 - (t - durSec) / gRampDown) * gVUsers : 0
        }
        
        const roundedV = Math.round(v)
        point[`group${idx}`] = roundedV
        point.total += roundedV
      })
      points.push(point)
    }
    
    // Ensure last point
    if (points[points.length - 1].time < totalSec) {
        const last: any = { time: totalSec, total: 0 }
        groups.forEach((_, idx) => last[`group${idx}`] = 0)
        points.push(last)
    }

    return points
  })()

  const GROUP_COLORS = ['#00a3ff', '#00ff94', '#ff006e', '#ffbe0b', '#8338ec', '#fb5607']

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
        payload.ramp_down = groups[0].rampDown || undefined
        payload.initialization = groups[0].initialization
      } else {
        payload.groups = groups.map((g) => ({
          name: g.name || undefined,
          script_id: g.scriptId,
          vusers: g.vusers,
          ramp_up: g.rampUp || undefined,
          ramp_down: g.rampDown || undefined,
          initialization: g.initialization,
        }))
      }

      // Add runtime overrides if any are set
      if (runtimeSettings.iterations || runtimeSettings.log_level || runtimeSettings.auto_transactions || runtimeSettings.snapshot_on_error) {
        payload.runtime_settings = {
          iterations: runtimeSettings.iterations ? Number(runtimeSettings.iterations) : undefined,
          log_level: runtimeSettings.log_level || undefined,
          auto_transactions: runtimeSettings.auto_transactions || undefined,
          error_handling: runtimeSettings.snapshot_on_error ? { snapshot_on_error: true } : undefined,
        }
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
            <div className="form-column flex-2">
              <label>
                <span>Test Name</span>
                <input
                  type="text"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="Optional — auto-generated if empty"
                />
              </label>
              <label>
                <span>Steady State Duration</span>
                <input
                  type="text"
                  value={duration}
                  onChange={(e) => setDuration(e.target.value)}
                  placeholder="e.g. 5m, 1h"
                  required
                />
              </label>
            </div>
            
            <div className="load-profile-preview">
              <div className="chart-container">
                <ResponsiveContainer width="100%" height={160}>
                  <AreaChart data={chartData} margin={{ top: 10, right: 10, left: -20, bottom: 0 }}>
                    <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.1)" vertical={false} />
                    <XAxis 
                      dataKey="time" 
                      tickFormatter={(v) => formatTime(v)}
                      stroke="var(--text-muted)"
                      fontSize={10}
                      tickLine={false}
                      label={{ value: 'Time', position: 'insideBottomRight', offset: -5, fontSize: 10, fill: 'var(--text-muted)' }}
                    />
                    <YAxis 
                      stroke="var(--text-muted)"
                      fontSize={10}
                      tickLine={false}
                      label={{ value: 'VUsers', angle: -90, position: 'insideLeft', offset: 10, fontSize: 10, fill: 'var(--text-muted)' }}
                    />
                    <Tooltip 
                      labelFormatter={(value) => `Time: ${formatTime(value)}`}
                      contentStyle={{ background: 'var(--bg-secondary)', border: '1px solid var(--border)', fontSize: '12px' }}
                      itemStyle={{ padding: '2px 0' }}
                    />
                    {groups.map((g, idx) => (
                      <Area
                        key={idx}
                        type="monotone"
                        dataKey={`group${idx}`}
                        name={g.name || `Group ${idx + 1}`}
                        stackId="1"
                        stroke={GROUP_COLORS[idx % GROUP_COLORS.length]}
                        fill={GROUP_COLORS[idx % GROUP_COLORS.length]}
                        fillOpacity={0.4}
                        animationDuration={300}
                      />
                    ))}
                  </AreaChart>
                </ResponsiveContainer>
                <div className="chart-label">Scenario Load Profile</div>
              </div>
            </div>
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
                  <label className="narrow">
                    <span>Ramp-down</span>
                    <input
                      type="text"
                      value={group.rampDown}
                      onChange={(e) => updateGroup(idx, { rampDown: e.target.value })}
                      placeholder="e.g. 30s"
                    />
                  </label>
                  <label className="medium">
                    <span>Initialization</span>
                    <select
                      value={group.initialization}
                      onChange={(e) => updateGroup(idx, { initialization: e.target.value as any })}
                    >
                      <option value="at_runtime">At Runtime</option>
                      <option value="at_once">At Once</option>
                    </select>
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

          <div className="runtime-overrides">
            <h4>Runtime Settings Overrides</h4>
            <div className="overrides-grid">
              <label>
                <span>Iterations</span>
                <input
                  type="number"
                  value={runtimeSettings.iterations || ''}
                  onChange={(e) => setRuntimeSettings({ ...runtimeSettings, iterations: e.target.value ? Number(e.target.value) : undefined })}
                  placeholder="Script default"
                />
              </label>
              <label>
                <span>Log Level</span>
                <select
                  value={runtimeSettings.log_level}
                  onChange={(e) => setRuntimeSettings({ ...runtimeSettings, log_level: e.target.value })}
                >
                  <option value="">Script default</option>
                  <option value="disabled">Disabled</option>
                  <option value="standard">Standard</option>
                  <option value="extended">Extended</option>
                </select>
              </label>
              <label>
                <span>Auto-Transactions</span>
                <select
                  value={runtimeSettings.auto_transactions}
                  onChange={(e) => setRuntimeSettings({ ...runtimeSettings, auto_transactions: e.target.value })}
                >
                  <option value="">Script default</option>
                  <option value="none">None</option>
                  <option value="per_action">Per Action</option>
                  <option value="per_iteration">Per Iteration</option>
                </select>
              </label>
              <label className="checkbox-label">
                <input
                  type="checkbox"
                  checked={runtimeSettings.snapshot_on_error}
                  onChange={(e) => setRuntimeSettings({ ...runtimeSettings, snapshot_on_error: e.target.checked })}
                />
                <span>Snapshot on error</span>
              </label>
            </div>
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
