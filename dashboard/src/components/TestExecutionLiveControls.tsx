import { Area, AreaChart, CartesianGrid, Legend, Line, ReferenceArea, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import { TestGroup, TestSummary } from '../types'
import { LiveControlPreview, VUserGroupAction } from './testExecutionLiveControlModel'
import { formatTimeLong, isValidDurationInput } from '../utils/timeUtils'
import { chartTooltipContentStyle, chartTooltipLabelStyle, preserveChartItemOrder } from './chartTooltipTheme'
import DurationField from './DurationField'
import { clampVUsersForRole, maxVUsersForRole } from './observerGroupModel'

interface Props {
  showControls: boolean
  showRunActions?: boolean
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
  onPauseAllVUsers: () => void
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
  showRunActions = true,
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
  onPauseAllVUsers,
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
  const rampInputMissing = vuserRampEnabled && !isValidDurationInput(vuserRampDuration)
  const durationInputInvalid = !isValidDurationInput(durationInput)
  const runActionsStatus = isPaused ? 'Paused' : isStopping ? 'Stopping' : 'Active'
  const runActionsStatusClass = runActionsStatus.toLowerCase()

  return (
    <div className="live-controls-section">
      <div className="live-controls-toolbar">
        <button
          type="button"
          className="btn btn-secondary live-controls-toggle"
          onClick={onToggleControls}
          aria-expanded={showControls}
          aria-controls="live-controls-panel"
        >
          {showControls ? 'Hide Controls' : 'Live Controls'}
        </button>
        {showRunActions && (
          <div className="run-actions-inline" aria-label="Run actions">
            <span className={`run-actions-status ${runActionsStatusClass}`}>{runActionsStatus}</span>
            {isPaused ? (
              <button type="button" className="btn btn-sm btn-success" onClick={onResume}>
                Resume
              </button>
            ) : (
              <button type="button" className="btn btn-sm btn-secondary" onClick={onPause} disabled={!isRunning || isStopping}>
                Pause
              </button>
            )}
            <button type="button" className="btn btn-sm btn-secondary" onClick={onPauseAllVUsers} disabled={!hasActiveVUsers || rampInputMissing || isStopping}>
              Pause VUsers
            </button>
            <button type="button" className="btn btn-sm btn-secondary" onClick={onStop} disabled={isStopping}>
              Stop run
            </button>
            <button type="button" className="btn btn-sm btn-danger" onClick={onAbort}>
              Abort
            </button>
          </div>
        )}
      </div>
      {showControls && (
        <div id="live-controls-panel" className="live-controls-panel">
          <div className="live-controls-layout">
            <div className="control-group control-group-form">
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
                const maxTargetVusers = maxVUsersForRole(group.role)

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
                      </select>
                    </label>
                    <input
                      type="range"
                      className="vuser-slider"
                      min={0}
                      max={group.role === 'observer'
                        ? maxTargetVusers
                        : Math.min(maxTargetVusers, Math.max(currentVusers * 3, targetVusers * 2, 100))}
                      value={targetVusers}
                      disabled={test.termination_mode === 'iterations' || groupAction !== 'run'}
                      aria-label={`${group.name} VUser target`}
                      onChange={(e) => onVuserTargetChange(
                        group.name,
                        clampVUsersForRole(parseInt(e.target.value, 10), group.role),
                      )}
                    />
                    <input
                      type="number"
                      className="vuser-input"
                      min={0}
                      max={maxTargetVusers}
                      value={targetVusers}
                      disabled={test.termination_mode === 'iterations' || groupAction !== 'run'}
                      aria-label={`${group.name} VUser target value`}
                      onChange={(e) => onVuserTargetChange(
                        group.name,
                        clampVUsersForRole(parseInt(e.target.value, 10) || 0, group.role),
                      )}
                    />
                    <button
                      className="btn btn-sm btn-primary"
                      onClick={() => onScaleVUsers(group.name)}
                      disabled={test.termination_mode === 'iterations' || isApplyDisabled}
                    >
                      {groupAction === 'run' ? 'Apply' : 'Pause'}
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
                <DurationField
                  className="vuser-ramp-duration"
                  label="Over"
                  value={vuserRampDuration}
                  disabled={!vuserRampEnabled}
                  compact
                  required={vuserRampEnabled}
                  showValidation={vuserRampEnabled}
                  onChange={onVuserRampDurationChange}
                />
              </div>

              <div className="control-group control-group-nested">
                <h4 className="control-title">{test.termination_mode === 'iterations' ? 'Fixed iteration completion — duration and scaling disabled' : 'Test Duration'}</h4>
                <div className="duration-control-row">
                  <span className="control-label">Target</span>
                  <DurationField
                    value={durationInput}
                    required
                    showPresets
                    compact
                    className="live-duration-field"
                    disabled={test.termination_mode === 'iterations'}
                    onChange={onDurationInputChange}
                  />
                  <button className="btn btn-sm btn-primary" onClick={onAdjustDuration} disabled={test.termination_mode === 'iterations' || durationInputInvalid}>
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
                  <span className="preview-metric-label">{test.termination_mode === 'iterations' ? 'Completed iterations' : 'Remaining'}</span>
                  <span className="preview-metric-value">{test.termination_mode === 'iterations' ? test.live_metrics?.completed_iterations ?? 0 : formatTimeLong(liveControlPreview.remainingSeconds)}</span>
                  <span className="preview-metric-detail">{test.termination_mode === 'iterations' ? 'finishes after the fixed count' : 'to stop'}</span>
                </div>
              </div>

              <div className="control-preview-chart">
                <ResponsiveContainer width="100%" height={220}>
                  <AreaChart data={liveControlPreview.points} margin={{ top: 10, right: 12, left: 24, bottom: 14 }}>
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                    <XAxis
                      dataKey="second"
                      stroke="var(--text-muted)"
                      fontSize={11}
                      height={42}
                      tickLine={false}
                      tickFormatter={(value) => formatTimeLong(Number(value))}
                      label={{ value: 'Elapsed time', position: 'insideBottom', offset: -4, fill: 'var(--text-muted)', fontSize: 11 }}
                    />
                    <YAxis
                      stroke="var(--text-muted)"
                      fontSize={11}
                      width={76}
                      tickLine={false}
                      allowDecimals={false}
                      label={{ value: 'VUsers', angle: -90, position: 'left', fill: 'var(--text-muted)', fontSize: 11 }}
                    />
                    <Tooltip
                      itemSorter={preserveChartItemOrder}
                      labelFormatter={(value) => formatTimeLong(Number(value))}
                      contentStyle={chartTooltipContentStyle}
                      labelStyle={chartTooltipLabelStyle}
                      formatter={(value, name) => [value ?? 0, name === 'total' ? 'Total' : name ?? '']}
                    />
                    <Legend verticalAlign="top" height={28} itemSorter={preserveChartItemOrder} />
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
                    {/* This band only maps because the X axis is a category axis and the preview
                        model always emits a point at exactly vuserTransitionSeconds. Any bound that
                        is not a sampled `second` needs a numeric axis instead. */}
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
