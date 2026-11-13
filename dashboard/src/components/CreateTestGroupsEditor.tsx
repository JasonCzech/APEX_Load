import { useEffect, useRef, useState } from 'react'

import { LoadStageDraft, StoredScript, ScenarioDraftGroup, ScenarioMode, SystemInventoryGenerator } from '../types'
import type { RuntimeSettingsOverride, ScenarioRuntimeSettingsDraft } from '../types/runtimeSettings'
import type { ScriptConfig } from '../types/scripts'
import { useScriptApi } from '../hooks/api/useScriptApi'
import { useSystemApi } from '../hooks/api/useSystemApi'
import { useModalDialog } from '../hooks/useModalDialog'
import DialogLayer from './ux/DialogLayer'
import { dialogBackdropProps } from './dialogBackdropProps'
import RuntimeSettingsGroupEditor from './RuntimeSettingsGroupEditor'
import {
  isFieldOverridden,
  isPartialRuntimeDraft,
  isRuntimeDraftEmpty,
  overrideFieldValue,
  runtimeDraftOwnsIterations,
  runtimeOverrideRowFromDraft,
  setOverrideField,
} from './runtimeSettingsModel'
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
  /** The test-wide runtime row: the scenario-default layer every group inherits. */
  testRuntimeSettings?: ScenarioRuntimeSettingsDraft
  /** Draft scope for the runtime editors' unsent-input recovery. */
  draftScope?: string
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


/**
 * The group patch a runtime editor change makes. A runtime iteration override and the compact
 * Iterations field are alternate representations of one setting, so when the edited row owns the
 * iteration the compact field shows the row's value (and is not sent on its own); a partial row that lists run_logic without its
 * iteration first adopts the compact value, so the row does not silently drop it.
 */
function groupRuntimePatch(group: ScenarioDraftGroup, next: ScenarioRuntimeSettingsDraft | undefined): Partial<ScenarioDraftGroup> {
  let runtimeSettings = next && !isRuntimeDraftEmpty(next) ? next : undefined
  if (runtimeSettings && isPartialRuntimeDraft(runtimeSettings) && group.iterations !== undefined
    && runtimeSettings.run_logic !== undefined && !isFieldOverridden(runtimeSettings, 'run_logic.iterations')) {
    runtimeSettings = setOverrideField(runtimeSettings, 'run_logic.iterations', group.iterations)
  }
  if (runtimeSettings && runtimeDraftOwnsIterations(runtimeSettings)) {
    return { runtimeSettings, iterations: rowIterations(runtimeSettings), iterationPayloadSource: 'runtime' }
  }
  // The row no longer carries the iteration: a compact value displayed from it goes too, so a
  // reset to inherited is not re-pinned as a top-level iteration.
  if (group.iterationPayloadSource === 'runtime') {
    return { runtimeSettings, iterations: undefined, iterationPayloadSource: undefined }
  }
  return { runtimeSettings }
}

/** The iteration count a row that owns the iteration runs (undefined: the engine default). */
function rowIterations(runtimeSettings: ScenarioRuntimeSettingsDraft): number | undefined {
  const value = isPartialRuntimeDraft(runtimeSettings)
    ? overrideFieldValue(runtimeSettings, 'run_logic.iterations')
    : runtimeSettings.run_logic !== undefined ? runtimeSettings.run_logic.iterations : runtimeSettings.iterations
  return typeof value === 'number' ? value : undefined
}

/** A stored script carries its config only when the library returned the full definition. */
function scriptConfigOf(script?: StoredScript): ScriptConfig | undefined {
  return script?.script?.config
}

/**
 * The per-group runtime settings drawer: the shared group editor mounted on one group, with the
 * test-wide row as its scenario-default layer and the group's own script config (fetched when
 * the library list carries only summaries) so every inherited value is the real one. Edits are
 * partial rows written through the runtime settings model; Apply hands them to the group.
 */
function GroupRuntimeSettingsDrawer({ groups, scripts, index, testRuntimeSettings, draftScope, onApply, onClose }: {
  groups: ScenarioDraftGroup[]
  scripts: StoredScript[]
  index: number
  testRuntimeSettings?: ScenarioRuntimeSettingsDraft
  draftScope?: string
  onApply: (next: ScenarioRuntimeSettingsDraft | undefined) => void
  onClose: () => void
}) {
  const dialogRef = useRef<HTMLDivElement>(null)
  const { getScript } = useScriptApi()
  const { getConfigDefaults } = useSystemApi()
  const group = groups[index]
  const listed = scripts.find(script => script.id === group?.scriptId)
  const [fetchedConfig, setFetchedConfig] = useState<{ scriptId: string; config?: ScriptConfig; failed?: boolean }>()
  const [engineDefaults, setEngineDefaults] = useState<ScriptConfig | undefined>()
  const [draft, setDraft] = useState<ScenarioRuntimeSettingsDraft | undefined>(group?.runtimeSettings)
  const [touched, setTouched] = useState(false)
  useModalDialog({ dialogRef, onClose })
  const needsScriptFetch = Boolean(group?.scriptId && !scriptConfigOf(listed))
  useEffect(() => {
    if (!needsScriptFetch || !group?.scriptId) return undefined
    let cancelled = false
    const scriptId = group.scriptId
    getScript(scriptId)
      .then(full => { if (!cancelled) setFetchedConfig({ scriptId, config: scriptConfigOf(full) }) })
      .catch(() => { if (!cancelled) setFetchedConfig({ scriptId, failed: true }) })
    return () => { cancelled = true }
  }, [getScript, group?.scriptId, needsScriptFetch])
  useEffect(() => {
    let cancelled = false
    getConfigDefaults().then(res => { if (!cancelled) setEngineDefaults(res.config) }).catch(() => {})
    return () => { cancelled = true }
  }, [getConfigDefaults])
  if (!group) return null
  const scriptConfig = scriptConfigOf(listed) ?? (fetchedConfig?.scriptId === group.scriptId ? fetchedConfig.config : undefined)
  const scriptLoading = needsScriptFetch && fetchedConfig?.scriptId !== group.scriptId
  const scriptFailed = needsScriptFetch && fetchedConfig?.scriptId === group.scriptId && fetchedConfig.failed
  const label = group.name || `Group ${index + 1}`
  const editorGroups = groups.map((candidate, candidateIndex) => {
    const script = scripts.find(entry => entry.id === candidate.scriptId)
    return {
      name: candidate.name || `Group ${candidateIndex + 1}`,
      scriptId: candidate.scriptId,
      protocol: script?.protocol,
      scriptConfig: candidateIndex === index ? scriptConfig : scriptConfigOf(script),
    }
  })
  const overrides = groups.map((candidate, candidateIndex) => (candidateIndex === index ? draft : candidate.runtimeSettings))
  return (
    <DialogLayer><div className="modal-overlay create-test-group-runtime-overlay" {...dialogBackdropProps(onClose)}>
      <div ref={dialogRef} className="modal modal-wide create-test-group-runtime-dialog" role="dialog" aria-modal="true" aria-labelledby="create-test-group-runtime-title" data-testid="create-test-group-runtime-dialog">
        <div className="modal-header">
          <h3 id="create-test-group-runtime-title">Runtime settings · {label}</h3>
        </div>
        <div className="modal-form">
          {scriptLoading && <p className="groups-note" role="status">Loading the script's runtime settings…</p>}
          {scriptFailed && <p className="groups-note" role="alert">The script's own runtime settings could not be loaded, so inherited values show the scenario and engine defaults.</p>}
          <RuntimeSettingsGroupEditor
            draftScope={draftScope ? `${draftScope}:group:${index}` : undefined}
            title={`Runtime overrides · ${label}`}
            description="Overrides for this group only. Unset fields inherit the test-wide runtime overrides, then the script and engine defaults."
            hideApplyPanel
            hideGroupSelector
            groups={editorGroups}
            focusedIndex={index}
            onFocusChange={() => {}}
            scenarioDefault={runtimeOverrideRowFromDraft(testRuntimeSettings) as RuntimeSettingsOverride | undefined}
            engineDefaults={engineDefaults}
            overrides={overrides}
            onChange={(_, next) => { setTouched(true); setDraft(next) }}
            onApplyToGroups={() => {}}
          />
        </div>
        <div className="modal-actions">
          <button type="button" className="btn btn-secondary" onClick={onClose}>Cancel</button>
          <button type="button" className="btn btn-primary" onClick={() => (touched ? onApply(draft) : onClose())}>Apply to group</button>
        </div>
      </div>
    </div></DialogLayer>
  )
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
  testRuntimeSettings,
  draftScope,
}: Props) {
  const [runtimeGroupIndex, setRuntimeGroupIndex] = useState<number | null>(null)
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
            <div className="group-runtime-settings-row">
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                aria-label={`Runtime settings for ${group.name || `group ${idx + 1}`}`}
                data-testid={`create-test-group-${idx}-runtime-settings`}
                disabled={!group.scriptId}
                onClick={() => setRuntimeGroupIndex(idx)}
              >
                Runtime settings
              </button>
              {group.runtimeSettings && !isRuntimeDraftEmpty(group.runtimeSettings) && (
                <span className="group-runtime-overrides-pill" data-testid={`create-test-group-${idx}-runtime-overrides`}>Group overrides set</span>
              )}
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
      {runtimeGroupIndex !== null && groups[runtimeGroupIndex] && (
        <GroupRuntimeSettingsDrawer
          key={runtimeGroupIndex}
          groups={groups}
          scripts={scripts}
          index={runtimeGroupIndex}
          testRuntimeSettings={testRuntimeSettings}
          draftScope={draftScope}
          onClose={() => setRuntimeGroupIndex(null)}
          onApply={next => {
            const target = runtimeGroupIndex
            onUpdateGroup(target, groupRuntimePatch(groups[target], next))
            setRuntimeGroupIndex(null)
          }}
        />
      )}
    </div>
  )
}
