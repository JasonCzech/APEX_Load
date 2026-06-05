import { useCallback, useEffect, useReducer, useState } from 'react'
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
import DurationField from './DurationField'

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

function isValidDurationInput(value?: string): boolean {
  return !value || parseDurationToSeconds(value) > 0
}

function validateGoalConfig(goalConfig: ScenarioDraftState['goalConfig']): string | null {
  if (goalConfig.p95_latency_ms !== undefined && goalConfig.p95_latency_ms <= 0) {
    return 'Goal-oriented scenarios require P95 latency ceiling to be greater than 0 ms'
  }
  if (goalConfig.max_error_pct !== undefined && (goalConfig.max_error_pct < 0 || goalConfig.max_error_pct > 100)) {
    return 'Goal-oriented scenarios require max error % to be between 0 and 100'
  }
  if (goalConfig.max_avg_server_processing_ms !== undefined && goalConfig.max_avg_server_processing_ms <= 0) {
    return 'Goal-oriented scenarios require max average server processing to be greater than 0 ms'
  }
  if (goalConfig.max_avg_transfer_ms !== undefined && goalConfig.max_avg_transfer_ms <= 0) {
    return 'Goal-oriented scenarios require max average transfer time to be greater than 0 ms'
  }
  if (goalConfig.min_vusers !== undefined && goalConfig.min_vusers < 1) {
    return 'Goal-oriented scenarios require min VUsers to be greater than 0'
  }
  if (goalConfig.max_vusers !== undefined && goalConfig.max_vusers < 1) {
    return 'Goal-oriented scenarios require max VUsers to be greater than 0'
  }
  if (
    goalConfig.min_vusers !== undefined &&
    goalConfig.max_vusers !== undefined &&
    goalConfig.min_vusers > goalConfig.max_vusers
  ) {
    return 'Goal-oriented scenarios require min VUsers to be less than or equal to max VUsers'
  }
  if (goalConfig.max_error_count !== undefined && goalConfig.max_error_count < 1) {
    return 'Goal-oriented scenarios require max error count to be greater than 0'
  }
  if (goalConfig.sla_breach_window && !isValidDurationInput(goalConfig.sla_breach_window)) {
    return 'Goal-oriented scenarios require a valid SLA breach window duration'
  }
  if (
    goalConfig.stop_on_sla_breach &&
    goalConfig.p95_latency_ms === undefined &&
    goalConfig.max_error_pct === undefined &&
    goalConfig.max_avg_server_processing_ms === undefined &&
    goalConfig.max_avg_transfer_ms === undefined
  ) {
    return 'Goal-oriented scenarios require a latency, error, server processing, or transfer threshold when stop on sustained SLA breach is enabled'
  }
  if (goalConfig.stop_on_max_errors && goalConfig.max_error_count === undefined) {
    return 'Goal-oriented scenarios require max error count when stop on max errors is enabled'
  }
  return null
}

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

  const { name, duration, startDelay, scenarioMode, scriptShakeout, resourceBaseline, assignment, groups, goalConfig, runtimeSettings } = scenarioDraft
  const isReviseMode = mode === 'revise-queued-run'
  const modalTitle = isReviseMode ? 'Create Revised Queued Run' : 'Create Queued Run'
  const submitLabel = isReviseMode ? 'Create Revised Copy' : 'Create Queued Run'
  const loadingLabel = isReviseMode ? 'Creating revised copy...' : 'Creating queued run...'
  const errorMessage = isReviseMode ? 'Failed to create revised queued run' : 'Failed to create queued run'

  const updateScenarioField = <K extends ScenarioDraftField>(field: K, value: ScenarioDraftState[K]) => {
    dispatchScenarioDraft({ type: 'setField', field, value })
  }

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        onClose()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [onClose])

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

  const setAssignmentMode = (mode: 'auto' | 'specific_hosts') => {
    updateScenarioField('assignment', {
      mode,
      selected_targets: mode === 'specific_hosts' ? (assignment.selected_targets || []) : [],
    })
  }

  const toggleAssignmentTarget = (target: string, checked: boolean) => {
    const current = new Set(assignment.selected_targets || [])
    if (checked) current.add(target)
    else current.delete(target)
    updateScenarioField('assignment', {
      mode: 'specific_hosts',
      selected_targets: Array.from(current),
    })
  }

  const removeGroup = (idx: number) => {
    dispatchScenarioDraft({ type: 'removeGroup', index: idx })
  }

  const totalVUsers = groups.reduce((sum, g) => sum + g.vusers, 0)
  const assignmentRecommendation = draftEstimate?.assignment_recommendation
  const availableTargets = assignmentRecommendation?.available_targets || []
  const recommendedTargets = assignmentRecommendation?.recommended_targets || []
  const footprint = draftEstimate?.footprint
  const hasGoalModeConfig = Boolean(
    goalConfig.target_average_tps ||
    goalConfig.p95_latency_ms ||
    goalConfig.max_error_pct ||
    goalConfig.max_avg_server_processing_ms ||
    goalConfig.max_avg_transfer_ms ||
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
    assignment,
    projectId,
    folderId,
  })

  const buildEstimatePayload = useCallback(() => buildCreateTestPayload({
    name,
    duration,
    startDelay,
    scenarioMode,
    scriptShakeout,
    resourceBaseline,
    groups,
    goalConfig,
    runtimeSettings,
    assignment: assignment.mode === 'specific_hosts' && (assignment.selected_targets || []).length === 0
      ? { mode: 'auto', selected_targets: [] }
      : assignment,
    projectId,
    folderId,
  }), [assignment, duration, folderId, goalConfig, groups, name, projectId, resourceBaseline, runtimeSettings, scenarioMode, scriptShakeout, startDelay])

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
        const estimate = await api.estimateTest(buildEstimatePayload())
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
  }, [api, buildEstimatePayload, groups, duration])

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
        const goalConfigError = validateGoalConfig(goalConfig)
        if (goalConfigError) {
          throw new Error(goalConfigError)
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
              <DurationField
                label="Steady State Duration"
                value={duration}
                required
                showPresets
                onChange={(value) => updateScenarioField('duration', value)}
              />
              <DurationField
                label="Global Start Delay"
                value={startDelay}
                allowEmpty
                showPresets
                onChange={(value) => updateScenarioField('startDelay', value)}
              />
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

            <section className="assignment-settings">
              <div className="assignment-settings-header">
                <div>
                  <h4>Controller / Generator Assignment</h4>
                  <p>Overall size is derived from the scenario footprint, current generator capacity profile, and the configured distributed targets.</p>
                </div>
              </div>

              <div className="scenario-mode-switcher assignment-mode-switcher">
                <button
                  type="button"
                  className={`mode-chip ${assignment.mode !== 'specific_hosts' ? 'active' : ''}`}
                  onClick={() => setAssignmentMode('auto')}
                >
                  Auto Assignment
                </button>
                <button
                  type="button"
                  className={`mode-chip ${assignment.mode === 'specific_hosts' ? 'active' : ''}`}
                  onClick={() => setAssignmentMode('specific_hosts')}
                >
                  Specific Hostnames
                </button>
              </div>

              {footprint && (
                <div className="assignment-footprint-grid">
                  <div className="assignment-footprint-card">
                    <span>Footprint</span>
                    <strong>{footprint.total_vusers} VUsers</strong>
                    <small>{footprint.duration} steady state</small>
                  </div>
                  <div className="assignment-footprint-card">
                    <span>Complexity</span>
                    <strong>{footprint.complexity_score.toFixed(2)}</strong>
                    <small>script design score</small>
                  </div>
                  <div className="assignment-footprint-card">
                    <span>Estimated Load</span>
                    <strong>{footprint.estimated_cpu_cores.toFixed(2)} CPU</strong>
                    <small>{Math.round(footprint.estimated_memory_mb)} MB / {Math.round(footprint.estimated_network_mbps)} Mbps</small>
                  </div>
                </div>
              )}

              {assignmentRecommendation?.summary && (
                <p className="assignment-copy">{assignmentRecommendation.summary}</p>
              )}

              {assignment.mode === 'specific_hosts' ? (
                availableTargets.length > 0 ? (
                  <>
                    {(assignment.selected_targets || []).length === 0 && (
                      <p className="assignment-copy secondary">Select at least one hostname to lock this run to explicit generator targets.</p>
                    )}
                    <div className="assignment-target-list">
                      {availableTargets.map((target) => {
                        const checked = (assignment.selected_targets || []).includes(target)
                        const recommended = recommendedTargets.includes(target)
                        return (
                          <label key={target} className={`assignment-target ${checked ? 'selected' : ''}`}>
                            <input
                              type="checkbox"
                              checked={checked}
                              onChange={(event) => toggleAssignmentTarget(target, event.target.checked)}
                            />
                            <div>
                              <strong>{target}</strong>
                              <span>{recommended ? 'Recommended for current footprint' : 'Available configured host'}</span>
                            </div>
                          </label>
                        )
                      })}
                    </div>
                  </>
                ) : (
                  <p className="assignment-copy secondary">No distributed generator hostnames are currently configured on this controller.</p>
                )
              ) : recommendedTargets.length > 0 ? (
                <div className="assignment-chip-row">
                  {recommendedTargets.map((target) => (
                    <span key={target} className="assignment-chip">{target}</span>
                  ))}
                </div>
              ) : (
                <p className="assignment-copy secondary">Auto-assignment will use the controller&apos;s configured generator inventory when distributed execution is enabled.</p>
              )}

              {assignmentRecommendation?.warnings && assignmentRecommendation.warnings.length > 0 && (
                <div className="assignment-warnings">
                  {assignmentRecommendation.warnings.map((warning) => (
                    <p key={warning}>{warning}</p>
                  ))}
                </div>
              )}
            </section>

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
