import { useEffect, useReducer, useState } from 'react'
import { useApi } from '../hooks/useApi'
import { DraftCapacityEstimate, GoalConfig, ScenarioDraft, ScenarioDraftGroup, ScenarioMode, StoredScript, TestSummary } from '../types'
import './CreateTestModal.css'
import { parseDurationToSeconds } from '../utils/timeUtils'
import CreateTestCapacityPreview from './CreateTestCapacityPreview'
import CreateTestGoalSettings from './CreateTestGoalSettings'
import CreateTestGroupsEditor from './CreateTestGroupsEditor'
import CreateTestRuntimeOverrides from './CreateTestRuntimeOverrides'
import { getErrorMessage } from '../utils/errorUtils'
import { buildDefaultScenarioGroup, buildInitialScenarioDraft, ScenarioDraftState, ScenarioRuntimeSettings } from './scenarioDraftUtils'

interface Props {
  scripts: StoredScript[]
  projectId?: string
  folderId?: string | null
  onClose: () => void
  onCreated: (test: TestSummary) => void
  initialScenario?: ScenarioDraft
}

type LoadProfilePoint = {
  time: number
  total: number
} & Record<`group${number}`, number>

type CreateTestPayloadGroup = {
  name?: string
  script_id: string
  vusers: number
  ramp_up?: string
  ramp_down?: string
  initialization: ScenarioDraftGroup['initialization']
}

type CreateTestPayload = {
  name?: string
  duration: string
  project_id?: string
  folder_id?: string | null
  scenario_mode: ScenarioMode
  script_shakeout: boolean
  resource_baseline: boolean
  script_id?: string
  vusers?: number
  ramp_up?: string
  ramp_down?: string
  initialization?: ScenarioDraftGroup['initialization']
  groups?: CreateTestPayloadGroup[]
  goal_config?: GoalConfig
  runtime_settings?: {
    iterations?: number
    log_level?: string
    auto_transactions?: string
    error_handling?: {
      snapshot_on_error: true
    }
  }
}

type GoalConfigAction = {
  field: keyof GoalConfig
  value: GoalConfig[keyof GoalConfig]
}

type RuntimeSettingsAction = {
  field: keyof RuntimeSettingsState
  value: RuntimeSettingsState[keyof RuntimeSettingsState]
}

type ScenarioDraftField = keyof Omit<ScenarioDraftState, 'groups' | 'goalConfig' | 'runtimeSettings'>

type ScenarioDraftAction =
  | { type: 'setField'; field: ScenarioDraftField; value: ScenarioDraftState[ScenarioDraftField] }
  | { type: 'updateGroup'; index: number; patch: Partial<ScenarioDraftGroup> }
  | { type: 'addGroup'; group: ScenarioDraftGroup }
  | { type: 'removeGroup'; index: number }

function goalConfigReducer(state: GoalConfig, action: GoalConfigAction): GoalConfig {
  return {
    ...state,
    [action.field]: action.value,
  }
}

function runtimeSettingsReducer(state: RuntimeSettingsState, action: RuntimeSettingsAction): RuntimeSettingsState {
  return {
    ...state,
    [action.field]: action.value,
  }
}

function scenarioDraftReducer(state: ScenarioDraftState, action: ScenarioDraftAction): ScenarioDraftState {
  switch (action.type) {
    case 'setField':
      return {
        ...state,
        [action.field]: action.value,
      }
    case 'updateGroup':
      return {
        ...state,
        groups: state.groups.map((group, index) => (
          index === action.index ? { ...group, ...action.patch } : group
        )),
      }
    case 'addGroup':
      return {
        ...state,
        groups: [...state.groups, action.group],
      }
    case 'removeGroup':
      if (state.groups.length <= 1) {
        return state
      }
      return {
        ...state,
        groups: state.groups.filter((_, index) => index !== action.index),
      }
    default:
      return state
  }
}

export default function CreateTestModal({ scripts, projectId, folderId, onClose, onCreated, initialScenario }: Props) {
  const api = useApi()
  const baselineSeconds = 5 * 60
  const [scenarioDraft, dispatchScenarioDraft] = useReducer(
    scenarioDraftReducer,
    buildInitialScenarioDraft(initialScenario, scripts),
  )
  const [goalConfig, dispatchGoalConfig] = useReducer(goalConfigReducer, {
    target_average_tps: initialScenario?.goalConfig?.target_average_tps,
    p95_latency_ms: initialScenario?.goalConfig?.p95_latency_ms,
    max_error_pct: initialScenario?.goalConfig?.max_error_pct,
    min_vusers: initialScenario?.goalConfig?.min_vusers,
    max_vusers: initialScenario?.goalConfig?.max_vusers,
    stop_on_sla_breach: initialScenario?.goalConfig?.stop_on_sla_breach || false,
    sla_breach_window: initialScenario?.goalConfig?.sla_breach_window || '30s',
  })
  const [runtimeSettings, dispatchRuntimeSettings] = useReducer(runtimeSettingsReducer, {
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

  const { name, duration, scenarioMode, scriptShakeout, resourceBaseline, groups } = scenarioDraft

  const updateScenarioField = <K extends ScenarioDraftField>(field: K, value: ScenarioDraftState[K]) => {
    dispatchScenarioDraft({ type: 'setField', field, value })
  }

  const updateGroup = (idx: number, patch: Partial<ScenarioDraftGroup>) => {
    dispatchScenarioDraft({ type: 'updateGroup', index: idx, patch })
  }

  const addGroup = () => {
    dispatchScenarioDraft({ type: 'addGroup', group: buildDefaultScenarioGroup(scripts) })
  }

  const updateGoalConfig = <K extends keyof GoalConfig>(field: K, value: GoalConfig[K]) => {
    dispatchGoalConfig({ field, value })
  }

  const updateRuntimeSetting = <K extends keyof RuntimeSettingsState>(field: K, value: RuntimeSettingsState[K]) => {
    dispatchRuntimeSettings({ field, value })
  }

  const removeGroup = (idx: number) => {
    dispatchScenarioDraft({ type: 'removeGroup', index: idx })
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
    const durSec = parseDurationToSeconds(duration)
    let maxRampDown = 0
    groups.forEach(g => {
      const rd = parseDurationToSeconds(g.rampDown)
      if (rd > maxRampDown) maxRampDown = rd
    })

    const preBaseline = resourceBaseline ? baselineSeconds : 0
    const postBaseline = resourceBaseline ? baselineSeconds : 0
    const totalSec = preBaseline + durSec + maxRampDown + postBaseline
    if (totalSec === 0) return []

    const points: LoadProfilePoint[] = []
    const step = Math.max(1, Math.floor(totalSec / 30)) // More points for clarity

    for (let t = 0; t <= totalSec; t += step) {
      const point: LoadProfilePoint = { time: t, total: 0 }
      groups.forEach((g, idx) => {
        const gVUsers = g.vusers
        const gRampUp = parseDurationToSeconds(g.rampUp)
        const gRampDown = parseDurationToSeconds(g.rampDown)

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
        const last: LoadProfilePoint = { time: totalSec, total: 0 }
        groups.forEach((_, idx) => last[`group${idx}`] = 0)
        points.push(last)
    }

    return points
  })()
  const buildPayload = (): CreateTestPayload => {
    const payload: CreateTestPayload = { name: name || undefined, duration, scenario_mode: scenarioMode, script_shakeout: scriptShakeout, resource_baseline: resourceBaseline }
    if (projectId) payload.project_id = projectId
    if (folderId !== undefined) payload.folder_id = folderId

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
      } catch (error: unknown) {
        if (!cancelled) {
          setDraftEstimate(null)
          setEstimateError(getErrorMessage(error, 'Unable to estimate load generator requirements'))
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
    } catch (error: unknown) {
      setError(getErrorMessage(error, 'Failed to create scenario'))
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
                  onClick={() => updateScenarioField('scenarioMode', 'vuser_driven')}
                >
                  VUser-Driven
                </button>
                <button
                  type="button"
                  className={`mode-chip ${scenarioMode === 'goal_oriented' ? 'active' : ''}`}
                  onClick={() => updateScenarioField('scenarioMode', 'goal_oriented')}
                >
                  Goal-Oriented
                </button>
              </div>
              <label>
                <span>Scenario Name</span>
                <input
                  type="text"
                  value={name}
                  onChange={(e) => updateScenarioField('name', e.target.value)}
                  placeholder="Optional — auto-generated if empty"
                />
              </label>
              <label>
                <span>Steady State Duration</span>
                <input
                  type="text"
                  value={duration}
                  onChange={(e) => updateScenarioField('duration', e.target.value)}
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
                    onChange={(e) => updateScenarioField('scriptShakeout', e.target.checked)}
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
                    onChange={(e) => updateScenarioField('resourceBaseline', e.target.checked)}
                  />
                  <p>Add a fixed 5 minute baseline before load starts and another 5 minute baseline after load stops so resource utilization can be observed without user traffic.</p>
                </div>
              </label>
            </div>
            
            <CreateTestCapacityPreview
              chartData={chartData}
              groups={groups}
              draftEstimate={draftEstimate}
              estimateLoading={estimateLoading}
              estimateError={estimateError}
            />
          </div>

          {scenarioMode === 'goal_oriented' && (
            <CreateTestGoalSettings
              goalConfig={goalConfig}
              onGoalConfigChange={updateGoalConfig}
            />
          )}

          <CreateTestGroupsEditor
            scenarioMode={scenarioMode}
            groups={groups}
            scripts={scripts}
            onAddGroup={addGroup}
            onUpdateGroup={updateGroup}
            onRemoveGroup={removeGroup}
          />

          <CreateTestRuntimeOverrides
            runtimeSettings={runtimeSettings}
            onRuntimeSettingChange={updateRuntimeSetting}
          />

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
