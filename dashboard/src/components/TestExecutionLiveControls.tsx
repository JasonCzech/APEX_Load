import { Area, AreaChart, CartesianGrid, Legend, Line, ReferenceArea, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import { TestGroup, TestSummary } from '../types'
import { formatTimeLong } from '../utils/timeUtils'

interface LiveControlPreview {
  points: Array<Record<string, number>>
  currentTotal: number
  projectedTotal: number
  remainingSeconds: number
  projectedDurationSeconds: number
  hasPendingChanges: boolean
}

interface Props {
  showControls: boolean
  onToggleControls: () => void
  test: TestSummary
  vuserTargets: Record<string, number>
  onVuserTargetChange: (groupName: string, value: number) => void
  onScaleVUsers: (groupName: string) => void
  durationInput: string
  onDurationInputChange: (value: string) => void
  onAdjustDuration: () => void
  liveControlPreview: LiveControlPreview
  controlFeedback: string
  groupColors: string[]
}

export default function TestExecutionLiveControls({
  showControls,
  onToggleControls,
  test,
  vuserTargets,
  onVuserTargetChange,
  onScaleVUsers,
  durationInput,
  onDurationInputChange,
  onAdjustDuration,
  liveControlPreview,
  controlFeedback,
  groupColors,
}: Props) {
  return (
    <div className="live-controls-section">
      <button
        className="btn btn-secondary live-controls-toggle"
        onClick={onToggleControls}
      >
        {showControls ? 'Hide Controls' : 'Live Controls'}
      </button>
      {showControls && (
        <div className="live-controls-panel">
          <div className="live-controls-layout">
            <div className="control-group control-group-form">
              <h4 className="control-title">VUser Scaling</h4>
              {(test.groups || []).map((group: TestGroup) => (
                <div key={group.name} className="vuser-control-row">
                  <span className="control-label">{group.name}</span>
                  <span className="control-current">Current: {group.vusers}</span>
                  <input
                    type="range"
                    className="vuser-slider"
                    min={0}
                    max={Math.max(group.vusers * 3, 100)}
                    value={vuserTargets[group.name] ?? group.vusers}
                    onChange={(e) => onVuserTargetChange(group.name, parseInt(e.target.value, 10))}
                  />
                  <input
                    type="number"
                    className="vuser-input"
                    min={0}
                    value={vuserTargets[group.name] ?? group.vusers}
                    onChange={(e) => onVuserTargetChange(group.name, parseInt(e.target.value, 10) || 0)}
                  />
                  <button
                    className="btn btn-sm btn-accent"
                    onClick={() => onScaleVUsers(group.name)}
                    disabled={(vuserTargets[group.name] ?? group.vusers) === group.vusers}
                  >
                    Apply
                  </button>
                </div>
              ))}

              <div className="control-group control-group-nested">
                <h4 className="control-title">Duration</h4>
                <div className="duration-control-row">
                  <span className="control-label">Target</span>
                  <input
                    type="text"
                    className="duration-input"
                    placeholder="e.g. 10m, 1h30m"
                    value={durationInput}
                    onChange={(e) => onDurationInputChange(e.target.value)}
                  />
                  <div className="duration-quick-btns">
                    <button className="btn btn-sm btn-secondary" onClick={() => onDurationInputChange('5m')}>5m</button>
                    <button className="btn btn-sm btn-secondary" onClick={() => onDurationInputChange('10m')}>10m</button>
                    <button className="btn btn-sm btn-secondary" onClick={() => onDurationInputChange('30m')}>30m</button>
                    <button className="btn btn-sm btn-secondary" onClick={() => onDurationInputChange('1h')}>1h</button>
                  </div>
                  <button className="btn btn-sm btn-accent" onClick={onAdjustDuration}>
                    Apply
                  </button>
                </div>
              </div>
            </div>

            <div className="control-group control-group-preview">
              <div className="preview-header-row">
                <div>
                  <h4 className="control-title">Projected Model</h4>
                  <p className="control-preview-note">Preview uses the values currently entered on the left and shows the load shape from now until the adjusted stop time.</p>
                </div>
                {liveControlPreview.hasPendingChanges && (
                  <span className="preview-pending-badge">Pending edits</span>
                )}
              </div>

              <div className="preview-metrics">
                <div className="preview-metric-card">
                  <span className="preview-metric-label">Now</span>
                  <span className="preview-metric-value">{liveControlPreview.currentTotal}</span>
                  <span className="preview-metric-detail">active VUsers</span>
                </div>
                <div className="preview-metric-card">
                  <span className="preview-metric-label">After Apply</span>
                  <span className="preview-metric-value">{liveControlPreview.projectedTotal}</span>
                  <span className="preview-metric-detail">projected VUsers</span>
                </div>
                <div className="preview-metric-card">
                  <span className="preview-metric-label">Remaining</span>
                  <span className="preview-metric-value">{formatTimeLong(liveControlPreview.remainingSeconds)}</span>
                  <span className="preview-metric-detail">to stop</span>
                </div>
              </div>

              <div className="control-preview-chart">
                <ResponsiveContainer width="100%" height={220}>
                  <AreaChart data={liveControlPreview.points} margin={{ top: 10, right: 12, left: -18, bottom: 0 }}>
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                    <XAxis
                      dataKey="second"
                      stroke="var(--text-muted)"
                      fontSize={11}
                      tickLine={false}
                      tickFormatter={(value) => formatTimeLong(Number(value))}
                    />
                    <YAxis
                      stroke="var(--text-muted)"
                      fontSize={11}
                      tickLine={false}
                      allowDecimals={false}
                    />
                    <Tooltip
                      labelFormatter={(value) => `T+${formatTimeLong(Number(value))}`}
                      contentStyle={{ background: 'rgba(33, 36, 47, 0.92)', border: '1px solid var(--border)', borderRadius: 10 }}
                      formatter={(value: number, name: string) => [value, name === 'total' ? 'Total' : name]}
                    />
                    <Legend verticalAlign="top" height={28} />
                    {(test.groups || []).map((group, index) => (
                      <Area
                        key={group.name}
                        type="stepAfter"
                        dataKey={`group${index}`}
                        name={group.name}
                        stackId="projected"
                        stroke={groupColors[index % groupColors.length]}
                        fill={groupColors[index % groupColors.length]}
                        fillOpacity={0.3}
                        isAnimationActive={false}
                      />
                    ))}
                    <Line
                      type="stepAfter"
                      dataKey="total"
                      name="Total"
                      stroke="var(--text-primary)"
                      strokeWidth={2}
                      dot={false}
                      isAnimationActive={false}
                    />
                    <ReferenceArea
                      x1={0}
                      x2={Math.max(0, Math.min(liveControlPreview.points[1]?.second ?? 0, liveControlPreview.points[2]?.second ?? 0))}
                      fill="var(--accent)"
                      fillOpacity={0.06}
                      strokeOpacity={0}
                    />
                  </AreaChart>
                </ResponsiveContainer>
                <div className="control-preview-caption">
                  The profile steps from the current live load to the entered targets, then holds until the projected end of the run.
                </div>
              </div>
            </div>
          </div>
          {controlFeedback && (
            <div className={`control-feedback ${controlFeedback.startsWith('Error') ? 'error' : 'success'}`}>
              {controlFeedback}
            </div>
          )}
        </div>
      )}
    </div>
  )
}