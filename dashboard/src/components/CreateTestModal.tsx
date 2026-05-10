import { useEffect, useState } from 'react'
import { useApi } from '../hooks/useApi'
import { DraftCapacityEstimate, GoalConfig, ScenarioDraft, ScenarioMode, StoredScript } from '../types'
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
  initialScenario?: ScenarioDraft
}

function formatDraftAvailabilitySource(source?: 'configured' | 'configured_plus_live', hasLive?: boolean): string {
  if (source === 'configured_plus_live') {
    return hasLive ? 'Live snapshot + configured ceiling' : 'Configured ceiling'
  }
  return 'Configured inventory'
}

function formatDraftAvailabilityDetail(estimate: DraftCapacityEstimate): string {
  const advisory = estimate.capacity_advisory
  if (!advisory) return 'Configured inventory only'
  if (advisory.availability_source === 'configured_plus_live') {
    if (advisory.live_available_generators !== undefined) {
      return `${advisory.live_available_generators} live / ${advisory.configured_available_generators || advisory.available_generators} configured`
    }
    return `${advisory.configured_available_generators || advisory.available_generators} configured ceiling`
  }
  return `${advisory.configured_available_generators || advisory.available_generators} configured`
}

const buildDefaultGroup = (scripts: StoredScript[]): GroupEntry => ({
  name: '',
  scriptId: scripts[0]?.id || '',
  vusers: 10,
  rampUp: '',
  rampDown: '',
  initialization: 'at_runtime',
})

export default function CreateTestModal({ scripts, onClose, onCreated, initialScenario }: Props) {
  const api = useApi()
  const baselineSeconds = 5 * 60
  const [name, setName] = useState(initialScenario?.name || '')
  const [duration, setDuration] = useState(initialScenario?.duration || '1m')
  const [scenarioMode, setScenarioMode] = useState<ScenarioMode>(initialScenario?.scenarioMode || 'vuser_driven')
  const [scriptShakeout, setScriptShakeout] = useState(Boolean(initialScenario?.scriptShakeout))
  const [resourceBaseline, setResourceBaseline] = useState(Boolean(initialScenario?.resourceBaseline))
  const [groups, setGroups] = useState<GroupEntry[]>(
    initialScenario?.groups.length
      ? initialScenario.groups
      : [buildDefaultGroup(scripts)],
  )
  const [goalConfig, setGoalConfig] = useState<GoalConfig>({
    target_average_tps: initialScenario?.goalConfig?.target_average_tps,
    p95_latency_ms: initialScenario?.goalConfig?.p95_latency_ms,
    max_error_pct: initialScenario?.goalConfig?.max_error_pct,
    min_vusers: initialScenario?.goalConfig?.min_vusers,
    max_vusers: initialScenario?.goalConfig?.max_vusers,
    stop_on_sla_breach: initialScenario?.goalConfig?.stop_on_sla_breach || false,
    sla_breach_window: initialScenario?.goalConfig?.sla_breach_window || '30s',
  })
  const [runtimeSettings, setRuntimeSettings] = useState({
    iterations: initialScenario?.runtimeSettings.iterations,
    log_level: initialScenario?.runtimeSettings.log_level || '',
    auto_transactions: initialScenario?.runtimeSettings.auto_transactions || '',
    snapshot_on_error: initialScenario?.runtimeSettings.snapshot_on_error || false,
  })
  const [draftEstimate, setDraftEstimate] = useState<DraftCapacityEstimate | null>(null)
  const [estimateLoading, setEstimateLoading] = useState(false)
  const [estimateError, setEstimateError] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const updateGroup = (idx: number, patch: Partial<GroupEntry>) => {
    setGroups((prev) => prev.map((g, i) => (i === idx ? { ...g, ...patch } : g)))
  }

  const addGroup = () => {
    setGroups((prev) => [
      ...prev,
      buildDefaultGroup(scripts),
    ])
  }

  const removeGroup = (idx: number) => {
    if (groups.length <= 1) return
    setGroups((prev) => prev.filter((_, i) => i !== idx))
  }

  const totalVUsers = groups.reduce((sum, g) => sum + g.vusers, 0)
  const hasGoalModeConfig = Boolean(
    goalConfig.target_average_tps ||
    goalConfig.p95_latency_ms ||
    goalConfig.max_error_pct ||
    goalConfig.min_vusers ||
    goalConfig.max_vusers ||
    goalConfig.stop_on_sla_breach,
  )

  // Calculate chart data
  const chartData = (() => {
    const durSec = parseDuration(duration)
    let maxRampDown = 0
    groups.forEach(g => {
      const rd = parseDuration(g.rampDown)
      if (rd > maxRampDown) maxRampDown = rd
    })

    const preBaseline = resourceBaseline ? baselineSeconds : 0
    const postBaseline = resourceBaseline ? baselineSeconds : 0
    const totalSec = preBaseline + durSec + maxRampDown + postBaseline
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
        const loadSecond = Math.max(0, t - preBaseline)
        if (t < preBaseline) {
          v = 0
        } else if (loadSecond < gRampUp) {
          v = gRampUp > 0 ? (loadSecond / gRampUp) * gVUsers : gVUsers
        } else if (loadSecond < durSec) {
          v = gVUsers
        } else if (loadSecond < durSec + gRampDown) {
          v = gRampDown > 0 ? (1 - (loadSecond - durSec) / gRampDown) * gVUsers : 0
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

  const buildPayload = () => {
    const payload: any = { name: name || undefined, duration }
    payload.scenario_mode = scenarioMode
    payload.script_shakeout = scriptShakeout
    payload.resource_baseline = resourceBaseline

    if (groups.length === 1 && !groups[0].name) {
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

    if (scenarioMode === 'goal_oriented') {
      payload.goal_config = {
        target_average_tps: goalConfig.target_average_tps || undefined,
        p95_latency_ms: goalConfig.p95_latency_ms || undefined,
        max_error_pct: goalConfig.max_error_pct || undefined,
        min_vusers: goalConfig.min_vusers || undefined,
        max_vusers: goalConfig.max_vusers || undefined,
        stop_on_sla_breach: goalConfig.stop_on_sla_breach || undefined,
        sla_breach_window: goalConfig.sla_breach_window || undefined,
      }
    }

    if (runtimeSettings.iterations || runtimeSettings.log_level || runtimeSettings.auto_transactions || runtimeSettings.snapshot_on_error) {
      payload.runtime_settings = {
        iterations: runtimeSettings.iterations ? Number(runtimeSettings.iterations) : undefined,
        log_level: runtimeSettings.log_level || undefined,
        auto_transactions: runtimeSettings.auto_transactions || undefined,
        error_handling: runtimeSettings.snapshot_on_error ? { snapshot_on_error: true } : undefined,
      }
    }

    return payload
  }

  useEffect(() => {
    const canEstimate = Boolean(duration.trim()) && groups.every((group) => group.scriptId)
    if (!canEstimate) {
      setDraftEstimate(null)
      setEstimateError('')
      setEstimateLoading(false)
      return
    }

    let cancelled = false
    setEstimateLoading(true)
    setEstimateError('')
    const timer = window.setTimeout(async () => {
      try {
        const estimate = await api.estimateTest(buildPayload())
        if (!cancelled) {
          setDraftEstimate(estimate)
        }
      } catch (err: any) {
        if (!cancelled) {
          setDraftEstimate(null)
          setEstimateError(err.message || 'Unable to estimate load generator requirements')
        }
      } finally {
        if (!cancelled) {
          setEstimateLoading(false)
        }
      }
    }, 300)

    return () => {
      cancelled = true
      window.clearTimeout(timer)
    }
  }, [api, duration, goalConfig, groups, name, resourceBaseline, runtimeSettings, scenarioMode, scriptShakeout])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const payload = buildPayload()

      if (scenarioMode === 'goal_oriented') {
        if (!hasGoalModeConfig) {
          throw new Error('Goal-oriented scenarios require at least one goal or guardrail setting')
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
          <h3>{initialScenario ? 'Edit Scenario' : 'Create Scenario'}</h3>
          <span className="total-vusers">{totalVUsers} total VUsers</span>
        </div>
        <form onSubmit={handleSubmit} className="modal-form">
          <div className="form-row">
            <div className="form-column flex-2">
              <div className="scenario-mode-switcher">
                <button
                  type="button"
                  className={`mode-chip ${scenarioMode === 'vuser_driven' ? 'active' : ''}`}
                  onClick={() => setScenarioMode('vuser_driven')}
                >
                  VUser-Driven
                </button>
                <button
                  type="button"
                  className={`mode-chip ${scenarioMode === 'goal_oriented' ? 'active' : ''}`}
                  onClick={() => setScenarioMode('goal_oriented')}
                >
                  Goal-Oriented
                </button>
              </div>
              <label>
                <span>Scenario Name</span>
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
              <label className="setting-checkbox-row">
                <span>Pre-Run Script Shakeout</span>
                <div className="setting-checkbox-control">
                  <input
                    type="checkbox"
                    checked={scriptShakeout}
                    onChange={(e) => setScriptShakeout(e.target.checked)}
                  />
                  <p>Run one instance of each script before full scale startup. Any failure halts the scenario and raises an alert.</p>
                </div>
              </label>
              <label className="setting-checkbox-row">
                <span>Resource Baseline</span>
                <div className="setting-checkbox-control">
                  <input
                    type="checkbox"
                    checked={resourceBaseline}
                    onChange={(e) => setResourceBaseline(e.target.checked)}
                  />
                  <p>Add a fixed 5 minute baseline before load starts and another 5 minute baseline after load stops so resource utilization can be observed without user traffic.</p>
                </div>
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
              <div className={`draft-capacity-card ${draftEstimate?.capacity_advisory?.requires_override ? 'warning' : 'ready'}`}>
                <div className="draft-capacity-header">
                  <h4>Draft Capacity Estimate</h4>
                  {estimateLoading && <span className="draft-capacity-status">Refreshing...</span>}
                </div>
                {estimateError ? (
                  <p className="draft-capacity-error">{estimateError}</p>
                ) : draftEstimate?.capacity_advisory ? (
                  <>
                    <div className="draft-capacity-metrics">
                      <div className="draft-capacity-metric">
                        <span className="draft-capacity-label">Recommended</span>
                        <strong>{draftEstimate.capacity_advisory.recommended_generators}</strong>
                        <span>{draftEstimate.capacity_advisory.recommended_generators === 1 ? 'generator' : 'generators'}</span>
                      </div>
                      <div className="draft-capacity-metric">
                        <span className="draft-capacity-label">Available</span>
                        <strong>{draftEstimate.capacity_advisory.available_generators}</strong>
                        <span>{formatDraftAvailabilityDetail(draftEstimate)}</span>
                      </div>
                      <div className="draft-capacity-metric">
                        <span className="draft-capacity-label">Bottleneck</span>
                        <strong>{draftEstimate.capacity_advisory.bottleneck}</strong>
                        <span>{draftEstimate.capacity_advisory.headroom_pct}% headroom</span>
                      </div>
                    </div>
                    <p className="draft-capacity-copy secondary">
                      Availability source: {formatDraftAvailabilitySource(draftEstimate.capacity_advisory.availability_source, draftEstimate.capacity_advisory.live_available_generators !== undefined)}. Snapshot: {formatDraftAvailabilityDetail(draftEstimate)}.
                    </p>
                    <p className="draft-capacity-copy">
                      Estimated total load: {draftEstimate.capacity_advisory.estimated_total.cpu_cores.toFixed(2)} CPU, {Math.round(draftEstimate.capacity_advisory.estimated_total.memory_mb)} MB memory, {Math.round(draftEstimate.capacity_advisory.estimated_total.network_mbps)} Mbps network.
                    </p>
                    {draftEstimate.capacity_advisory.warnings && draftEstimate.capacity_advisory.warnings.length > 0 && (
                      <div className="draft-capacity-warnings">
                        {draftEstimate.capacity_advisory.warnings.map((warning) => (
                          <p key={warning}>{warning}</p>
                        ))}
                      </div>
                    )}
                  </>
                ) : (
                  <p className="draft-capacity-copy">Enter a complete scenario to estimate the required load generators.</p>
                )}
              </div>
            </div>
          </div>

          {scenarioMode === 'goal_oriented' && (
            <div className="goal-settings">
              <div className="goal-settings-header">
                <h4>Goal Settings</h4>
                <p>Define the target and guardrails. The script groups below still provide the starting seed load and script mix for the scenario.</p>
              </div>
              <div className="goal-settings-grid">
                <label>
                  <span>Target Avg TPS</span>
                  <input
                    type="number"
                    min={1}
                    step="1"
                    value={goalConfig.target_average_tps || ''}
                    onChange={(e) => setGoalConfig({ ...goalConfig, target_average_tps: e.target.value ? Number(e.target.value) : undefined })}
                    placeholder="e.g. 300"
                  />
                </label>
                <label>
                  <span>P95 Latency Ceiling (ms)</span>
                  <input
                    type="number"
                    min={1}
                    step="1"
                    value={goalConfig.p95_latency_ms || ''}
                    onChange={(e) => setGoalConfig({ ...goalConfig, p95_latency_ms: e.target.value ? Number(e.target.value) : undefined })}
                    placeholder="e.g. 800"
                  />
                </label>
                <label>
                  <span>Max Error %</span>
                  <input
                    type="number"
                    min={0}
                    max={100}
                    step="0.1"
                    value={goalConfig.max_error_pct || ''}
                    onChange={(e) => setGoalConfig({ ...goalConfig, max_error_pct: e.target.value ? Number(e.target.value) : undefined })}
                    placeholder="e.g. 2.5"
                  />
                </label>
                <label>
                  <span>Min VUsers</span>
                  <input
                    type="number"
                    min={1}
                    step="1"
                    value={goalConfig.min_vusers || ''}
                    onChange={(e) => setGoalConfig({ ...goalConfig, min_vusers: e.target.value ? Number(e.target.value) : undefined })}
                    placeholder="e.g. 10"
                  />
                </label>
                <label>
                  <span>Max VUsers</span>
                  <input
                    type="number"
                    min={1}
                    step="1"
                    value={goalConfig.max_vusers || ''}
                    onChange={(e) => setGoalConfig({ ...goalConfig, max_vusers: e.target.value ? Number(e.target.value) : undefined })}
                    placeholder="e.g. 300"
                  />
                </label>
                <label>
                  <span>SLA Breach Window</span>
                  <input
                    type="text"
                    value={goalConfig.sla_breach_window || ''}
                    onChange={(e) => setGoalConfig({ ...goalConfig, sla_breach_window: e.target.value })}
                    placeholder="e.g. 30s"
                  />
                </label>
                <label className="checkbox-label goal-checkbox-label">
                  <input
                    type="checkbox"
                    checked={goalConfig.stop_on_sla_breach || false}
                    onChange={(e) => setGoalConfig({ ...goalConfig, stop_on_sla_breach: e.target.checked })}
                  />
                  <span>Stop on sustained SLA breach</span>
                </label>
              </div>
            </div>
          )}

          <div className="groups-section">
            <div className="groups-header">
              <div>
                <h4>{scenarioMode === 'goal_oriented' ? 'Script Seed Groups' : 'VUser Groups'}</h4>
                {scenarioMode === 'goal_oriented' && (
                  <p className="groups-note">Seed VUsers define the initial script distribution until goal-based autoscaling is implemented.</p>
                )}
              </div>
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
                    <span>{scenarioMode === 'goal_oriented' ? 'Seed VUsers' : 'VUsers'}</span>
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
              {loading ? (initialScenario ? 'Saving...' : 'Creating...') : `${initialScenario ? 'Save Scenario' : 'Create Scenario'} (${groups.length} group${groups.length > 1 ? 's' : ''})`}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
