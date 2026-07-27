import { useCallback, useEffect, useId, useMemo, useReducer, useRef, useState } from 'react'
import { useApi } from '../hooks/useApi'
import { useModalDialog } from '../hooks/useModalDialog'
import { DraftCapacityEstimate, ScenarioDraft, ScenarioDraftGroup, StoredScript, SystemInventoryGenerator, TestSummary } from '../types'
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
import { useNavigationGuard, useNavigationGuardRunner, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { hasGoalModeConfiguration, validateGoalConfig } from './goalConfigValidation'
import GroupPlacementEditor from './GroupPlacementEditor'
import { effectiveGroupVUsers, validateAggregateVUserDemand, validateGroupExecutionEnvelope } from './observerGroupModel'
import { validateGeneratorAssignment } from './generatorAssignmentValidation'

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
  const { runGuarded } = useNavigationGuardRunner()
  const baselineSeconds = 5 * 60
  const [scenarioDraft, dispatchScenarioDraft] = useReducer(
    scenarioDraftReducer,
    buildInitialScenarioDraft(initialScenario, scripts),
  )
  const baselineDraftRef = useRef(JSON.stringify(scenarioDraft))
  const submitDraftRef = useRef<() => Promise<boolean>>(async () => false)
  const modalDirtyRef = useRef(false)
  const [draftEstimate, setDraftEstimate] = useState<DraftCapacityEstimate | null>(null)
  const [estimateLoading, setEstimateLoading] = useState(false)
  const [estimateError, setEstimateError] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [generatorInventory, setGeneratorInventory] = useState<SystemInventoryGenerator[]>([])
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const scenarioNameRef = useRef<HTMLInputElement | null>(null)
  const titleId = useId()

  const { name, duration, startDelay, scenarioMode, scriptShakeout, resourceBaseline, assignment, groups, goalConfig, runtimeSettings, revisionContext } = scenarioDraft
  const isReviseMode = mode === 'revise-queued-run'
  const modalTitle = isReviseMode ? 'Create Revised Queued Run' : 'Create Queued Run'
  const submitLabel = isReviseMode ? 'Create Revised Copy' : 'Create Queued Run'
  const loadingLabel = isReviseMode ? 'Creating revised copy...' : 'Creating queued run...'
  const errorMessage = isReviseMode ? 'Failed to create revised queued run' : 'Failed to create queued run'

  const updateScenarioField = <K extends ScenarioDraftField>(field: K, value: ScenarioDraftState[K]) => {
    dispatchScenarioDraft({ type: 'setField', field, value })
  }

  const handleGuardedClose = useCallback(() => {
    runGuarded(onClose)
  }, [onClose, runGuarded])

  useModalDialog({
    dialogRef,
    initialFocusRef: scenarioNameRef,
    onClose: loading ? undefined : handleGuardedClose,
  })

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

  const totalVUsers = groups.reduce((sum, group) => sum + effectiveGroupVUsers(group), 0)
  const assignmentRecommendation = draftEstimate?.assignment_recommendation
  const availableTargets = assignmentRecommendation?.available_targets || []
  const recommendedTargets = assignmentRecommendation?.recommended_targets || []
  const availableLabels = assignmentRecommendation?.available_labels || {}
  const footprint = draftEstimate?.footprint
  const hasGoalModeConfig = hasGoalModeConfiguration(goalConfig)

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
    applicationId: revisionContext?.applicationId,
    releaseId: revisionContext?.releaseId,
    scenarioId: revisionContext?.scenarioId,
    revisionSourceTestId: revisionContext?.sourceTestId,
    projectId: revisionContext?.projectId ?? projectId,
    folderId: revisionContext ? revisionContext.folderId : folderId,
    dataPools: revisionContext?.dataPools,
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
    applicationId: revisionContext?.applicationId,
    releaseId: revisionContext?.releaseId,
    scenarioId: revisionContext?.scenarioId,
    revisionSourceTestId: revisionContext?.sourceTestId,
    projectId: revisionContext?.projectId ?? projectId,
    folderId: revisionContext ? revisionContext.folderId : folderId,
    dataPools: revisionContext?.dataPools,
  }), [assignment, duration, folderId, goalConfig, groups, name, projectId, resourceBaseline, revisionContext, runtimeSettings, scenarioMode, scriptShakeout, startDelay])

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

  useEffect(() => {
    let cancelled = false
    api.getSystemInventory()
      .then(response => {
        if (!cancelled) setGeneratorInventory(response.generators || [])
      })
      .catch(() => {
        if (!cancelled) setGeneratorInventory([])
      })
    return () => {
      cancelled = true
    }
  }, [api])

  const submitDraft = async (): Promise<boolean> => {
    setError('')
    setLoading(true)
    try {
      const rootAssignmentError = validateGeneratorAssignment(assignment, 'scenario', 'Scenario assignment')
      if (rootAssignmentError) throw new Error(rootAssignmentError)
      for (let index = 0; index < groups.length; index += 1) {
        const group = groups[index]
        const script = scripts.find(item => item.id === group.scriptId)
        const groupError = validateGroupExecutionEnvelope({
          role: group.role,
          protocol: script?.protocol,
          vusers: effectiveGroupVUsers(group),
          label: group.name || `Group ${index + 1}`,
        })
        if (groupError) throw new Error(groupError)
        const assignmentError = validateGeneratorAssignment(group.assignment, 'group', group.name || `Group ${index + 1}`)
        if (assignmentError) throw new Error(assignmentError)
      }
      const aggregateError = validateAggregateVUserDemand(groups)
      if (aggregateError) throw new Error(aggregateError)
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
      return true
    } catch (error: unknown) {
      setError(getErrorMessage(error, errorMessage))
      return false
    } finally {
      setLoading(false)
    }
  }
  submitDraftRef.current = submitDraft
  const modalDirty = JSON.stringify(scenarioDraft) !== baselineDraftRef.current
  modalDirtyRef.current = modalDirty
  const modalGuard = useMemo<NavigationGuard>(() => ({
    id: `create-test-modal-${mode}`,
    label: modalTitle,
    isDirty: () => modalDirtyRef.current,
    save: () => submitDraftRef.current(),
    discard: () => undefined,
  }), [modalTitle, mode])
  useNavigationGuard(modalGuard, modalDirty)

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    await submitDraft()
  }

  return (
    <div
      className="modal-overlay"
      role="presentation"
      onPointerDown={event => {
        if (event.target === event.currentTarget && !loading) handleGuardedClose()
      }}
    >
      <div ref={dialogRef} className="modal modal-wide" role="dialog" aria-modal="true" aria-labelledby={titleId}>
        <div className="modal-header">
          <h3 id={titleId}>{modalTitle}</h3>
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
                  ref={scenarioNameRef}
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

              <GroupPlacementEditor
                assignment={assignment}
                onChange={next => updateScenarioField('assignment', next || { mode: 'auto' })}
                availableTargets={availableTargets}
                recommendedTargets={recommendedTargets}
                generatorInventory={generatorInventory}
                availableLabels={availableLabels}
                allowInherit={false}
                scope="scenario"
              />

              {(!assignment.mode || assignment.mode === 'auto') && recommendedTargets.length > 0 ? (
                <div className="assignment-chip-row">
                  {recommendedTargets.map((target) => (
                    <span key={target} className="assignment-chip">{target}</span>
                  ))}
                </div>
              ) : null}

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
            availableTargets={availableTargets}
            recommendedTargets={recommendedTargets}
            generatorInventory={generatorInventory}
            availableLabels={availableLabels}
          />

          <CreateTestRuntimeOverrides
            draftScope={`${revisionContext?.projectId ?? projectId ?? ''}:${revisionContext?.scenarioId ?? 'new'}`}
            runtimeSettings={runtimeSettings}
            onRuntimeSettingChange={updateRuntimeSetting}
          />

          {error && <p className="form-error" role="alert">{error}</p>}

          <div className="modal-actions">
            <button type="button" className="btn btn-secondary" onClick={handleGuardedClose} disabled={loading}>
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
