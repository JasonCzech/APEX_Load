import { StoredScript, ScenarioDraftGroup, ScenarioMode, SystemInventoryGenerator } from '../types'
import DurationField from './DurationField'
import GroupPlacementEditor from './GroupPlacementEditor'
import { clampVUsersForRole, maxVUsersForRole, OBSERVER_MAX_VUSERS } from './observerGroupModel'

interface Props {
  scenarioMode: ScenarioMode
  groups: ScenarioDraftGroup[]
  scripts: StoredScript[]
  onAddGroup: () => void
  onUpdateGroup: (index: number, patch: Partial<ScenarioDraftGroup>) => void
  onRemoveGroup: (index: number) => void
  availableTargets?: string[]
  recommendedTargets?: string[]
  generatorInventory?: SystemInventoryGenerator[]
  availableLabels?: Record<string, string[]>
}

function isBrowserScript(scripts: StoredScript[], scriptId: string): boolean {
  return scripts.some((script) => script.id === scriptId && script.protocol === 'browser')
}

export default function CreateTestGroupsEditor({
  scenarioMode,
  groups,
  scripts,
  onAddGroup,
  onUpdateGroup,
  onRemoveGroup,
  availableTargets,
  recommendedTargets,
  generatorInventory,
  availableLabels,
}: Props) {
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

      {groups.map((group, idx) => (
        <div key={idx} className="group-row">
          <div className="group-number">{idx + 1}</div>
          <div className="group-fields">
            <label>
              <span>Group Name</span>
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
                  onUpdateGroup(idx, {
                    scriptId,
                    ...(group.role === 'observer' && !nextIsBrowser ? { role: 'load' as const } : {}),
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
              <span>{scenarioMode === 'goal_oriented' ? 'Seed VUsers' : 'VUsers'}</span>
              <input
                type="number"
                value={group.vusers}
                onChange={(event) => onUpdateGroup(idx, {
                  vusers: clampVUsersForRole(Number(event.target.value), group.role, 1),
                })}
                min={1}
                max={maxVUsersForRole(group.role)}
                required
              />
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
                    ...(role === 'observer' && group.vusers > OBSERVER_MAX_VUSERS
                      ? { vusers: OBSERVER_MAX_VUSERS }
                      : {}),
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
                recommendedTargets={recommendedTargets}
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
                onClick={() => onRemoveGroup(idx)}
                title="Remove group"
              >
                x
              </button>
            )}
          </div>
        </div>
      ))}
    </div>
  )
}
