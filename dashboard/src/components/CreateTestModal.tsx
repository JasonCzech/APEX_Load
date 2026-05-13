import { useEffect, useReducer, useState } from 'react'
import { useApi } from '../hooks/useApi'
import { DraftCapacityEstimate, ScenarioDraft, ScenarioDraftGroup, StoredScript, TestSummary } from '../types'
import './CreateTestModal.css'
import { parseDurationToSeconds } from '../utils/timeUtils'
import CreateTestCapacityPreview from './CreateTestCapacityPreview'
import CreateTestGoalSettings from './CreateTestGoalSettings'
import CreateTestGroupsEditor from './CreateTestGroupsEditor'
import CreateTestRuntimeOverrides from './CreateTestRuntimeOverrides'
import { getErrorMessage } from '../utils/errorUtils'
import { buildDefaultScenarioGroup, buildInitialScenarioDraft, scenarioDraftReducer, ScenarioDraftField, ScenarioDraftState } from './scenarioDraftUtils'
import { buildCreateTestPayload } from './scenarioPayloadUtils'

interface Props {
  scripts: StoredScript[]
  projectId?: string
  folderId?: string | null
  onClose: () => void
  onCreated: (test: TestSummary) => void
  initialScenario?: ScenarioDraft
  mode?: 'create-queued-run' | 'revise-queued-run'
}

type LoadProfilePoint = {
  time: number
  total: number
} & Record<`group${number}`, number>

export default function CreateTestModal({ scripts, projectId, folderId, onClose, onCreated, initialScenario, mode = 'create-queued-run' }: Props) {
  const api = useApi()
  const baselineSeconds = 5 * 60
  const [scenarioDraft, dispatchScenarioDraft] = useReducer(
    scenarioDraftReducer,
    buildInitialScenarioDraft(initialScenario, scripts),
  )
  const [draftEstimate, setDraftEstimate] = useState<DraftCapacityEstimate | null>(null)
  const [estimateLoading, setEstimateLoading] = useState(false)
  const [estimateError, setEstimateError] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const { name, duration, startDelay, scenarioMode, scriptShakeout, resourceBaseline, groups, goalConfig, runtimeSettings } = scenarioDraft
  const isReviseMode = mode === 'revise-queued-run'
  const modalTitle = isReviseMode ? 'Create Revised Queued Run' : 'Create Queued Run'
  const submitLabel = isReviseMode ? 'Create Revised Copy' : 'Create Queued Run'
  const loadingLabel = isReviseMode ? 'Creating revised copy...' : 'Creating queued run...'
  const errorMessage = isReviseMode ? 'Failed to create revised queued run' : 'Failed to create queued run'

  const updateScenarioField = <K extends ScenarioDraftField>(field: K, value: ScenarioDraftState[K]) => {
    dispatchScenarioDraft({ type: 'setField', field, value })
  }

  const updateGroup = (idx: number, patch: Partial<ScenarioDraftGroup>) => {
    dispatchScenarioDraft({ type: 'updateGroup', index: idx, patch })
  }

  const addGroup = () => {
    dispatchScenarioDraft({ type: 'addGroup', group: buildDefaultScenarioGroup(scripts) })
  }

  const updateGoalConfig = <K extends keyof ScenarioDraftState['goalConfig']>(field: K, value: ScenarioDraftState['goalConfig'][K]) => {
    dispatchScenarioDraft({ type: 'setGoalConfigField', field, value })
  }

  const updateRuntimeSetting = <K extends keyof ScenarioDraftState['runtimeSettings']>(field: K, value: ScenarioDraftState['runtimeSettings'][K]) => {
    dispatchScenarioDraft({ type: 'setRuntimeSettingField', field, value })
  }

  const removeGroup = (idx: number) => {
    dispatchScenarioDraft({ type: 'removeGroup', index: idx })
  }

  const totalVUsers = groups.reduce((sum, g) => sum + g.vusers, 0)
  const hasGoalModeConfig = Boolean(
    goalConfig.target_average_tps ||
    goalConfig.p95_latency_ms ||
    goalConfig.max_error_pct ||
    goalConfig.max_error_count ||
    goalConfig.min_vusers ||
    goalConfig.max_vusers ||
    goalConfig.stop_on_sla_breach ||
    goalConfig.stop_on_max_errors,
  )

  // Calculate chart data
  const chartData = (() => {
    const durSec = parseDurationToSeconds(duration)
    let maxRampDown = 0
    groups.forEach(g => {
      const rd = parseDurationToSeconds(g.rampDown)
      if (rd > maxRampDown) maxRampDown = rd
    })

    const startDelaySeconds = parseDurationToSeconds(startDelay)
    const preBaseline = (resourceBaseline ? baselineSeconds : 0) + startDelaySeconds
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
  const buildPayload = () => buildCreateTestPayload({
    name,
    duration,
    startDelay,
    scenarioMode,
    scriptShakeout,
    resourceBaseline,
    groups,
    goalConfig,
    runtimeSettings,
    projectId,
    folderId,
  })

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
  }, [api, duration, goalConfig, groups, name, resourceBaseline, runtimeSettings, scenarioMode, scriptShakeout, startDelay])

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
      setError(getErrorMessage(error, errorMessage))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal modal-wide" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <h3>{modalTitle}</h3>
          <span className="total-vusers">{totalVUsers} total VUsers</span>
        </div>
        <form onSubmit={handleSubmit} className="modal-form">
          {isReviseMode && (
            <p className="page-subtitle">Saving here creates a new queued run. The current queued run remains unchanged.</p>
          )}
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
              <label>
                <span>Global Start Delay</span>
                <input
                  type="text"
                  value={startDelay}
                  onChange={(e) => updateScenarioField('startDelay', e.target.value)}
                  placeholder="Optional, e.g. 30s"
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
              {loading ? loadingLabel : `${submitLabel} (${groups.length} group${groups.length > 1 ? 's' : ''})`}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
