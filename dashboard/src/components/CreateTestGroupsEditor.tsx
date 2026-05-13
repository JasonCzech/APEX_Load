import { StoredScript, ScenarioDraftGroup, ScenarioMode } from '../types'

interface Props {
  scenarioMode: ScenarioMode
  groups: ScenarioDraftGroup[]
  scripts: StoredScript[]
  onAddGroup: () => void
  onUpdateGroup: (index: number, patch: Partial<ScenarioDraftGroup>) => void
  onRemoveGroup: (index: number) => void
}

export default function CreateTestGroupsEditor({
  scenarioMode,
  groups,
  scripts,
  onAddGroup,
  onUpdateGroup,
  onRemoveGroup,
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
                onChange={(event) => onUpdateGroup(idx, { scriptId: event.target.value })}
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
                onChange={(event) => onUpdateGroup(idx, { vusers: Number(event.target.value) })}
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
                onChange={(event) => onUpdateGroup(idx, { rampUp: event.target.value })}
                placeholder="e.g. 30s"
              />
            </label>
            <label className="narrow">
              <span>Ramp-down</span>
              <input
                type="text"
                value={group.rampDown}
                onChange={(event) => onUpdateGroup(idx, { rampDown: event.target.value })}
                placeholder="e.g. 30s"
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