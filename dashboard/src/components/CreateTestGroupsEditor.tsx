import { useRef } from 'react'

import { LoadStageDraft, StoredScript, ScenarioDraftGroup, ScenarioMode, SystemInventoryGenerator } from '../types'
import DurationField from './DurationField'
import GroupPlacementEditor from './GroupPlacementEditor'
import { clampVUsersForRole, maxVUsersForRole, stagePeakVUsers, OBSERVER_MAX_VUSERS } from './observerGroupModel'

interface Props {
  scenarioMode: ScenarioMode
  groups: ScenarioDraftGroup[]
  scripts: StoredScript[]
  onAddGroup: () => void
  onUpdateGroup: (index: number, patch: Partial<ScenarioDraftGroup>) => void
  onRemoveGroup: (index: number) => void
  availableTargets?: string[]
  generatorInventory?: SystemInventoryGenerator[]
  availableLabels?: Record<string, string[]>
}

function isBrowserScript(scripts: StoredScript[], scriptId: string): boolean {
  return scripts.some((script) => script.id === scriptId && script.protocol === 'browser')
}

// What the observer clamp overwrote, so leaving the observer role can put it
// back. Only the editor's own clamp is undone: the restore is skipped unless
// the group still holds exactly the values the clamp wrote.
type ObserverClampSnapshot = {
  clampedVUsers: number
  clampedStages?: LoadStageDraft[]
  previousVUsers: number
  previousStages?: LoadStageDraft[]
}

function sameStageTargets(left?: LoadStageDraft[], right?: LoadStageDraft[]): boolean {
  if (!left || !right) return !left && !right
  if (left.length !== right.length) return false
  return left.every((stage, index) => stage.target_vusers === right[index].target_vusers)
}

export default function CreateTestGroupsEditor({
  scenarioMode,
  groups,
  scripts,
  onAddGroup,
  onUpdateGroup,
  onRemoveGroup,
  availableTargets,
  generatorInventory,
  availableLabels,
}: Props) {
  // Keyed by group index -- the same handle onUpdateGroup uses. Removing a
  // group shifts the indexes, so the map is dropped wholesale on removal.
  const observerClampSnapshots = useRef(new Map<number, ObserverClampSnapshot>())

  // The observer envelope is validated against a group's *peak* demand
  // (effectiveGroupVUsers), so switching into the role has to clamp the stage
  // targets as well as the group VUsers, or a staged group is left unsaveable
  // with no stage editor on this surface to repair it. Clamping is destructive
  // and rewrites the interior of the schedule, not just its peak, so record
  // what it overwrote.
  function clampGroupIntoObserverRole(idx: number, group: ScenarioDraftGroup): Partial<ScenarioDraftGroup> {
    // Route the clamp through the shared envelope invariant rather than
    // re-deriving it here, so this editor stays in step with the other
    // role-switch consumer (TestHub) on the whole rule -- the role ceiling,
    // the integral-VUser rule and the minimum of 1.
    const clampedVUsers = clampVUsersForRole(group.vusers, 'observer', 1)
    const clampedStages = group.stages?.map(stage => ({
      ...stage,
      ...(stage.target_vusers !== undefined
        ? { target_vusers: clampVUsersForRole(stage.target_vusers, 'observer') }
        : {}),
    }))
    const vusersChanged = clampedVUsers !== group.vusers
    const stagesChanged = clampedStages !== undefined && !sameStageTargets(clampedStages, group.stages)
    if (vusersChanged || stagesChanged) {
      observerClampSnapshots.current.set(idx, {
        clampedVUsers,
        clampedStages: stagesChanged ? clampedStages : group.stages,
        previousVUsers: group.vusers,
        previousStages: group.stages,
      })
    } else {
      observerClampSnapshots.current.delete(idx)
    }
    return {
      ...(vusersChanged ? { vusers: clampedVUsers } : {}),
      ...(stagesChanged ? { stages: clampedStages } : {}),
    }
  }

  // Leaving the observer role undoes this editor's own clamp. Without it a
  // mis-click silently ships a flattened schedule: the round-tripped draft now
  // validates, and no control on this surface can bring the original stage
  // targets back.
  function restoreObserverClamp(idx: number, group: ScenarioDraftGroup): Partial<ScenarioDraftGroup> {
    const snapshot = observerClampSnapshots.current.get(idx)
    observerClampSnapshots.current.delete(idx)
    if (!snapshot) return {}
    // Anything the user changed since the clamp is theirs, not ours to revert.
    if (group.vusers !== snapshot.clampedVUsers || !sameStageTargets(group.stages, snapshot.clampedStages)) {
      return {}
    }
    return {
      vusers: snapshot.previousVUsers,
      ...(snapshot.previousStages ? { stages: snapshot.previousStages } : {}),
    }
  }

  return (
    <div className="groups-section">
      <div className="groups-header">
        <div>
          <h4>{scenarioMode === 'goal_oriented' ? 'Script Seed Groups' : 'VUser Groups'}</h4>
          {scenarioMode === 'goal_oriented' && (
            <p className="groups-note">Seed VUsers define the initial script distribution until goal-based autoscaling is implemented.</p>
          )}
        </div>
        <button type="button" className="btn btn-secondary btn-sm" onClick={onAddGroup}>
          + Add Group
        </button>
      </div>

      {groups.map((group, idx) => {
      // A staged group's demand is owned by its stages: the shared validator
      // rejects any VUsers value other than the peak, and this surface renders
      // no stage editor. Presenting the field as editable there offers exactly
      // one accepted value, so it is locked and annotated instead -- but only
      // once the invariant actually holds, otherwise locking would strand a
      // mismatched draft with no way to repair it.
      const stagePeak = stagePeakVUsers(group.stages)
      const vusersOwnedByStages = stagePeak > 0 && group.vusers === stagePeak
      return (
        <div key={idx} className="group-row">
          <div className="group-number">{idx + 1}</div>
          <div className="group-fields">
            <label>
              <span>Group name</span>
              <input
                type="text"
                value={group.name}
                onChange={(event) => onUpdateGroup(idx, { name: event.target.value })}
                placeholder={`Group ${idx + 1}`}
              />
            </label>
            <label>
              <span>Script</span>
              <select
                value={group.scriptId}
                onChange={(event) => {
                  const scriptId = event.target.value
                  const nextIsBrowser = isBrowserScript(scripts, scriptId)
                  const leavesObserver = group.role === 'observer' && !nextIsBrowser
                  onUpdateGroup(idx, {
                    scriptId,
                    ...(leavesObserver
                      ? { role: 'load' as const, ...restoreObserverClamp(idx, group) }
                      : {}),
                  })
                }}
                required
              >
                {scripts.length === 0 && <option value="">No scripts</option>}
                {scripts.map((script) => (
                  <option key={script.id} value={script.id}>
                    {script.name} ({script.protocol})
                  </option>
                ))}
              </select>
            </label>
            <label className="narrow">
              <span>
                {scenarioMode === 'goal_oriented'
                  ? 'Seed VUsers'
                  : (stagePeak > 0 ? 'Peak VUsers (stage-derived)' : 'VUsers')}
              </span>
              <input
                type="number"
                value={group.vusers}
                onChange={(event) => onUpdateGroup(idx, {
                  vusers: clampVUsersForRole(Number(event.target.value), group.role, 1),
                })}
                min={1}
                max={maxVUsersForRole(group.role)}
                disabled={vusersOwnedByStages}
                required
              />
              {stagePeak > 0 && (
                <small>
                  {vusersOwnedByStages
                    ? `This staged group is fixed at its peak stage target of ${stagePeak} VUsers. Edit the scenario stages to change it.`
                    : `This staged group must match its peak stage target of ${stagePeak} VUsers.`}
                </small>
              )}
            </label>
            <DurationField
              className="narrow"
              label="Ramp-up"
              value={group.rampUp}
              allowEmpty
              compact
              onChange={(value) => onUpdateGroup(idx, { rampUp: value })}
            />
            <DurationField
              className="narrow"
              label="Ramp-down"
              value={group.rampDown}
              allowEmpty
              compact
              onChange={(value) => onUpdateGroup(idx, { rampDown: value })}
            />
            <label className="narrow">
              <span>Iterations</span>
              <input
                type="number"
                value={group.iterations ?? ''}
                onChange={(event) => onUpdateGroup(idx, { iterations: event.target.value ? Number(event.target.value) : undefined })}
                min={1}
                placeholder="script"
              />
            </label>
            <label className="medium">
              <span>Initialization</span>
              <select
                value={group.initialization}
                onChange={(event) => onUpdateGroup(idx, { initialization: event.target.value as ScenarioDraftGroup['initialization'] })}
              >
                <option value="at_runtime">At Runtime</option>
                <option value="at_once">At Once</option>
              </select>
            </label>
            <label className="medium">
              <span>Role</span>
              <select
                value={group.role || 'load'}
                onChange={(event) => {
                  const role = event.target.value as ScenarioDraftGroup['role']
                  onUpdateGroup(idx, {
                    role,
                    ...(role === 'observer'
                      ? clampGroupIntoObserverRole(idx, group)
                      : restoreObserverClamp(idx, group)),
                  })
                }}
                title="Observer canaries are small browser groups that continuously report Web Vitals and stay pinned during goal-based scaling."
              >
                <option value="load">Load</option>
                <option value="observer" disabled={!isBrowserScript(scripts, group.scriptId)}>
                  Observer Canary{!isBrowserScript(scripts, group.scriptId) ? ' (browser scripts only)' : ''}
                </option>
              </select>
              {group.role === 'observer' && <small>Observer canaries are capped at {OBSERVER_MAX_VUSERS} VUsers.</small>}
            </label>
            <div className="group-placement-row">
              <GroupPlacementEditor
                assignment={group.assignment}
                onChange={assignment => onUpdateGroup(idx, { assignment })}
                availableTargets={availableTargets}
                generatorInventory={generatorInventory}
                availableLabels={availableLabels}
                protocol={scripts.find(script => script.id === group.scriptId)?.protocol}
                compact
                scope="group"
              />
            </div>
            {groups.length > 1 && (
              <button
                type="button"
                className="btn-remove"
                onClick={() => {
                  // Removal renumbers every later group, so index-keyed
                  // snapshots would restore onto the wrong group.
                  observerClampSnapshots.current.clear()
                  onRemoveGroup(idx)
                }}
                title="Remove group"
              >
                x
              </button>
            )}
          </div>
        </div>
      )
      })}
    </div>
  )
}
