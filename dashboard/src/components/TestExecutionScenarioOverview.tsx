import { TestSummary } from '../types'

interface Props {
  test: TestSummary
  isMultiGroup: boolean
  shakeoutFailed: boolean
  groupColors: string[]
}

export default function TestExecutionScenarioOverview({
  test,
  isMultiGroup,
  shakeoutFailed,
  groupColors,
}: Props) {
  return (
    <>
      {isMultiGroup && (
        <div className="group-config-bar">
          {test.groups.map((group, index) => (
            <div key={group.name} className="group-chip" style={{ borderLeftColor: groupColors[index % groupColors.length] }}>
              <span className="group-chip-name">{group.name}</span>
              <span className="group-chip-detail">{group.script_name} · {group.vusers} VUsers</span>
            </div>
          ))}
        </div>
      )}

      {shakeoutFailed && (
        <div className="scenario-alert-banner" role="alert">
          <div>
            <h3 className="scenario-alert-title">Script shakeout failed</h3>
            <p className="scenario-alert-message">{test.error}</p>
          </div>
        </div>
      )}
    </>
  )
}