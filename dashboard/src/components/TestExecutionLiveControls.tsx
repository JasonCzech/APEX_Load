import { Area, AreaChart, CartesianGrid, Legend, Line, ReferenceArea, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import { TestGroup, TestSummary } from '../types'
import { LiveControlPreview, VUserGroupAction } from './testExecutionLiveControlModel'
import { formatTimeLong } from '../utils/timeUtils'

interface Props {
  showControls: boolean
  onToggleControls: () => void
  test: TestSummary
  vuserTargets: Record<string, number>
  onVuserTargetChange: (groupName: string, value: number) => void
  vuserGroupActions: Record<string, VUserGroupAction>
  onVuserGroupActionChange: (groupName: string, value: VUserGroupAction) => void
  vuserRampEnabled: boolean
  onVuserRampEnabledChange: (value: boolean) => void
  vuserRampDuration: string
  onVuserRampDurationChange: (value: string) => void
  onScaleVUsers: (groupName: string) => void
  onStopAllVUsers: () => void
  durationInput: string
  onDurationInputChange: (value: string) => void
  onAdjustDuration: () => void
  isRunning: boolean
  isPaused: boolean
  isStopping: boolean
  onPause: () => void
  onResume: () => void
  onStop: () => void
  onAbort: () => void
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
  vuserGroupActions,
  onVuserGroupActionChange,
  vuserRampEnabled,
  onVuserRampEnabledChange,
  vuserRampDuration,
  onVuserRampDurationChange,
  onScaleVUsers,
  onStopAllVUsers,
  durationInput,
  onDurationInputChange,
  onAdjustDuration,
  isRunning,
  isPaused,
  isStopping,
  onPause,
  onResume,
  onStop,
  onAbort,
  liveControlPreview,
  controlFeedback,
  groupColors,
}: Props) {
  const hasActiveVUsers = (test.groups || []).some((group) => {
    const currentVUsers = test.live_metrics?.groups?.[group.name]?.vusers ?? group.vusers
    return currentVUsers > 0 || (test.vuser_ramps?.[group.name]?.target_vusers ?? 0) > 0
  })
  const rampInputMissing = vuserRampEnabled && vuserRampDuration.trim() === ''

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
              <div className="control-group control-group-nested run-actions-card">
                <div className="run-actions-header">
                  <h4 className="control-title">Run Actions</h4>
                  <span className="run-actions-status">{isPaused ? 'Paused' : isStopping ? 'Stopping' : 'Active'}</span>
                </div>
                <div className="run-actions-row">
                  {isPaused ? (
                    <button className="btn btn-sm btn-success" onClick={onResume}>
                      Resume
                    </button>
                  ) : (
                    <button className="btn btn-sm btn-secondary" onClick={onPause} disabled={!isRunning || isStopping}>
                      Pause
                    </button>
                  )}
                  <button className="btn btn-sm btn-secondary" onClick={onStopAllVUsers} disabled={!hasActiveVUsers || rampInputMissing || isStopping}>
                    Stop VUsers
                  </button>
                  <button className="btn btn-sm btn-secondary" onClick={onStop} disabled={isStopping}>
                    Stop Test
                  </button>
                  <button className="btn btn-sm btn-danger" onClick={onAbort} disabled={isStopping}>
                    Abort
                  </button>
                </div>
              </div>

              <h4 className="control-title">VUser Scaling</h4>
              {(test.groups || []).map((group: TestGroup) => {
                const activeRampTarget = test.vuser_ramps?.[group.name]?.target_vusers
                const currentVusers = test.live_metrics?.groups?.[group.name]?.vusers ?? group.vusers
                const targetVusers = vuserTargets[group.name] ?? activeRampTarget ?? group.vusers
                const groupAction = vuserGroupActions[group.name] ?? 'run'
                const effectiveTargetVusers = groupAction === 'run' ? targetVusers : 0
                const isActiveRampTarget = activeRampTarget !== undefined && effectiveTargetVusers === activeRampTarget
                const isNoChange = effectiveTargetVusers === currentVusers && activeRampTarget === undefined
                const isApplyDisabled = isNoChange || isActiveRampTarget || rampInputMissing

                return (
                  <div key={group.name} className="vuser-control-row">
                    <span className="control-label">{group.name}</span>
                    <span className="control-current">Current: {currentVusers}</span>
                    <label className="vuser-action-field">
                      <span>Action</span>
                      <select
                        className="vuser-action-select"
                        value={groupAction}
                        onChange={(e) => onVuserGroupActionChange(group.name, e.target.value as VUserGroupAction)}
                        aria-label={`${group.name} VUser action`}
                      >
                        <option value="run">Run</option>
                        <option value="pause">Pause</option>
                        <option value="stop">Stop</option>
                      </select>
                    </label>
                    <input
                      type="range"
                      className="vuser-slider"
                      min={0}
                      max={Math.max(currentVusers * 3, targetVusers * 2, 100)}
                      value={targetVusers}
                      disabled={groupAction !== 'run'}
                      aria-label={`${group.name} VUser target`}
                      onChange={(e) => onVuserTargetChange(group.name, parseInt(e.target.value, 10))}
                    />
                    <input
                      type="number"
                      className="vuser-input"
                      min={0}
                      value={targetVusers}
                      disabled={groupAction !== 'run'}
                      aria-label={`${group.name} VUser target value`}
                      onChange={(e) => onVuserTargetChange(group.name, parseInt(e.target.value, 10) || 0)}
                    />
                    <button
                      className="btn btn-sm btn-accent"
                      onClick={() => onScaleVUsers(group.name)}
                      disabled={isApplyDisabled}
                    >
                      {groupAction === 'run' ? 'Apply' : groupAction === 'pause' ? 'Pause' : 'Stop'}
                    </button>
                  </div>
                )
              })}
              <div className="vuser-ramp-options">
                <label className="control-checkbox">
                  <input
                    type="checkbox"
                    checked={vuserRampEnabled}
                    onChange={(e) => onVuserRampEnabledChange(e.target.checked)}
                  />
                  <span>Gradual change</span>
                </label>
                <label className="vuser-ramp-duration">
                  <span>Over</span>
                  <input
                    type="text"
                    className="duration-input"
                    placeholder="1m"
                    value={vuserRampDuration}
                    disabled={!vuserRampEnabled}
                    onChange={(e) => onVuserRampDurationChange(e.target.value)}
                  />
                </label>
              </div>

              <div className="control-group control-group-nested">
                <h4 className="control-title">Test Duration</h4>
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
                        type={liveControlPreview.isGradualVuserChange ? 'linear' : 'stepAfter'}
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
                      type={liveControlPreview.isGradualVuserChange ? 'linear' : 'stepAfter'}
                      dataKey="total"
                      name="Total"
                      stroke="var(--text-primary)"
                      strokeWidth={2}
                      dot={false}
                      isAnimationActive={false}
                    />
                    <ReferenceArea
                      x1={0}
                      x2={Math.max(0, liveControlPreview.vuserTransitionSeconds)}
                      fill="var(--accent)"
                      fillOpacity={0.06}
                      strokeOpacity={0}
                    />
                  </AreaChart>
                </ResponsiveContainer>
                <div className="control-preview-caption">
                  {liveControlPreview.hasActiveVuserRamp
                    ? `The active VUser ramp continues to its scheduled target over the next ${formatTimeLong(liveControlPreview.vuserTransitionSeconds)}.`
                    : liveControlPreview.isGradualVuserChange
                    ? `The profile ramps to the entered targets over ${formatTimeLong(liveControlPreview.vuserTransitionSeconds)}, then holds until the projected end of the run.`
                    : 'The profile steps from the current live load to the entered targets, then holds until the projected end of the run.'}
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
