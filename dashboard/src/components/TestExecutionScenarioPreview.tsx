import {
  AreaChart, Area, XAxis, YAxis, CartesianGrid,
  Tooltip, ResponsiveContainer, Legend, ReferenceArea,
} from 'recharts'

import { CapacityAdvisory, TestSummary } from '../types'
import { QueuedScenarioPreview, QueuedScenarioRow, ScenarioScriptDetail } from './testExecutionScenarioModel'
import { formatTimeCompact } from '../utils/timeUtils'

interface Props {
  test: TestSummary
  startCapacityConflict: CapacityAdvisory | null
  startFeedback: string
  onDismissStartConflict: () => void
  onConfirmStartAnyway: () => void
  queuedScenarioPreview: QueuedScenarioPreview
  queuedScenarioRows: QueuedScenarioRow[]
  scriptDetailsLoading: boolean
  scriptDetailsError: string
  expandedScenarioScript: string | null
  onExpandedScenarioScriptChange: (value: string | null) => void
  onEditScript: (scriptId: string) => void
  groupColors: string[]
}

function formatCapacityValue(value: number, decimals = 0): string {
  return new Intl.NumberFormat('en-US', {
    minimumFractionDigits: decimals,
    maximumFractionDigits: decimals,
  }).format(value)
}

function formatCapacityBottleneck(advisory?: CapacityAdvisory): string {
  if (!advisory) return 'Unknown'
  switch (advisory.bottleneck) {
    case 'cpu':
      return 'CPU bound'
    case 'memory':
      return 'Memory bound'
    case 'network':
      return 'Network bound'
    case 'vusers':
      return 'VUser ceiling'
    default:
      return 'Unknown'
  }
}

function formatAvailabilitySource(advisory?: CapacityAdvisory): string {
  if (!advisory) return 'Configured inventory'
  if (advisory.availability_source === 'configured_plus_live') {
    return advisory.live_available_generators !== undefined ? 'Live snapshot + configured ceiling' : 'Configured ceiling'
  }
  return 'Configured inventory'
}

function formatAvailabilityDetail(advisory?: CapacityAdvisory): string {
  if (!advisory) return 'Configured inventory only'
  if (advisory.availability_source === 'configured_plus_live') {
    if (advisory.live_available_generators !== undefined) {
      return `${formatCapacityValue(advisory.live_available_generators)} live / ${formatCapacityValue(advisory.configured_available_generators || advisory.available_generators)} configured`
    }
    return `${formatCapacityValue(advisory.configured_available_generators || advisory.available_generators)} configured ceiling`
  }
  return `${formatCapacityValue(advisory.configured_available_generators || advisory.available_generators)} configured`
}

function formatThinkTimeLabel(scriptDetail?: ScenarioScriptDetail): string {
  const thinkTime = scriptDetail?.script?.config?.think_time
  if (!thinkTime) return 'Script default'
  if (thinkTime.ignore) return 'Disabled'
  return `${thinkTime.type || 'fixed'} · ${thinkTime.mean_ms || 0}ms`
}

function formatPacingLabel(scriptDetail?: ScenarioScriptDetail): string {
  const pacing = scriptDetail?.script?.config?.pacing
  if (!pacing?.type || pacing.type === 'none') return 'None'
  if (pacing.type === 'fixed_delay') return `Fixed delay · ${pacing.delay_ms || 0}ms`
  if (pacing.type === 'fixed_interval') return `Fixed interval · ${pacing.interval_ms || 0}ms`
  if (pacing.type === 'tps') return `Target TPS · ${pacing.tps || 0}`
  return pacing.type.replace(/_/g, ' ')
}

function formatEffectiveIterations(group: TestSummary['groups'][number], scriptDetail: ScenarioScriptDetail | undefined, test: TestSummary): string {
  if (group.iterations !== undefined) return `${group.iterations} (group override)`
  const override = test.runtime_settings?.iterations
  if (override !== undefined) return `${override} (scenario override)`
  const iterations = scriptDetail?.script?.config?.run_logic?.iterations
  if (!iterations) return 'Duration driven'
  return `${iterations}`
}

function formatEffectiveAutoTransactions(scriptDetail: ScenarioScriptDetail | undefined, test: TestSummary): string {
  const override = test.runtime_settings?.auto_transactions
  if (override) return `${override} (scenario override)`
  return scriptDetail?.script?.config?.run_logic?.auto_transactions || 'none'
}

function formatEffectiveLogLevel(scriptDetail: ScenarioScriptDetail | undefined, test: TestSummary): string {
  const override = test.runtime_settings?.log_level
  if (override) return `${override} (scenario override)`
  return scriptDetail?.script?.config?.log?.level || 'standard'
}

function formatSnapshotOnError(scriptDetail: ScenarioScriptDetail | undefined, test: TestSummary): string {
  const override = test.runtime_settings?.error_handling?.snapshot_on_error
  if (override !== undefined) return override ? 'Enabled (scenario override)' : 'Disabled (scenario override)'
  return scriptDetail?.script?.config?.error_handling?.snapshot_on_error ? 'Enabled' : 'Disabled'
}

function formatGoalNumber(value: number, suffix = ''): string {
  return `${Number.isInteger(value) ? value.toString() : value.toFixed(1)}${suffix}`
}

export default function TestExecutionScenarioPreview({
  test,
  startCapacityConflict,
  startFeedback,
  onDismissStartConflict,
  onConfirmStartAnyway,
  queuedScenarioPreview,
  queuedScenarioRows,
  scriptDetailsLoading,
  scriptDetailsError,
  expandedScenarioScript,
  onExpandedScenarioScriptChange,
  onEditScript,
  groupColors,
}: Props) {
  if (test.status !== 'QUEUED') {
    return null
  }

  return (
    <>
      {startCapacityConflict && (
        <div className="scenario-alert-banner scenario-alert-warning" role="alert">
          <div>
            <h3 className="scenario-alert-title">Additional load generators recommended before start</h3>
            <p className="scenario-alert-message">
              This scenario is estimated to need {startCapacityConflict.recommended_generators} load generators, but only {startCapacityConflict.available_generators} are currently available. Bottleneck: {formatCapacityBottleneck(startCapacityConflict)}.
            </p>
            <p className="scenario-alert-message secondary">
              Availability source: {formatAvailabilitySource(startCapacityConflict)}. Snapshot: {formatAvailabilityDetail(startCapacityConflict)}.
            </p>
            {startFeedback && <p className="scenario-alert-message secondary">{startFeedback}</p>}
          </div>
          <div className="scenario-alert-actions">
            <button className="btn btn-secondary" onClick={onDismissStartConflict}>
              Cancel
            </button>
            <button className="btn btn-warning" onClick={onConfirmStartAnyway}>
              Start Anyway
            </button>
          </div>
        </div>
      )}

      {queuedScenarioPreview.points.length > 0 && (
        <div className="scenario-preview-card">
          <div className="scenario-preview-header">
            <div>
              <h3 className="scenario-preview-title">Scenario Run Model</h3>
              <p className="scenario-preview-subtitle">This is the saved load shape that will run when you start the scenario.</p>
            </div>
            <div className="scenario-preview-stats">
              <div className="scenario-preview-stat">
                <span className="scenario-preview-stat-label">Peak</span>
                <span className="scenario-preview-stat-value">{queuedScenarioPreview.totalVUsers} VUsers</span>
              </div>
              <div className="scenario-preview-stat">
                <span className="scenario-preview-stat-label">Steady State</span>
                <span className="scenario-preview-stat-value">{formatTimeCompact(queuedScenarioPreview.steadySeconds)}</span>
              </div>
              <div className="scenario-preview-stat">
                <span className="scenario-preview-stat-label">Total Timeline</span>
                <span className="scenario-preview-stat-value">{formatTimeCompact(queuedScenarioPreview.totalSeconds)}</span>
              </div>
              <div className="scenario-preview-stat">
                <span className="scenario-preview-stat-label">Baseline</span>
                <span className="scenario-preview-stat-value">{test.resource_baseline ? '5m pre/post' : 'Disabled'}</span>
              </div>
              <div className="scenario-preview-stat">
                <span className="scenario-preview-stat-label">Start Delay</span>
                <span className="scenario-preview-stat-value">{test.start_delay || 'Disabled'}</span>
              </div>
            </div>
          </div>

          {test.capacity_advisory && (
            <div className={`capacity-advisory-card ${test.capacity_advisory.requires_override ? 'warning' : 'ready'}`}>
              <div className="capacity-advisory-header">
                <div>
                  <h3 className="capacity-advisory-title">Load Generator Advisory</h3>
                  <p className="capacity-advisory-subtitle">
                    Estimated from script complexity, generator baseline resources, {formatCapacityValue(test.capacity_advisory.headroom_pct)}% reserved headroom, and {formatAvailabilitySource(test.capacity_advisory).toLowerCase()}.
                  </p>
                </div>
                <span className={`capacity-advisory-badge ${test.capacity_advisory.requires_override ? 'warning' : 'ready'}`}>
                  {test.capacity_advisory.requires_override ? 'Capacity Shortfall' : 'Capacity Ready'}
                </span>
              </div>

              <div className="capacity-advisory-grid">
                <div className="capacity-advisory-metric">
                  <span className="capacity-advisory-label">Recommended</span>
                  <span className="capacity-advisory-value">{formatCapacityValue(test.capacity_advisory.recommended_generators)}</span>
                  <span className="capacity-advisory-detail">load generators</span>
                </div>
                <div className="capacity-advisory-metric">
                  <span className="capacity-advisory-label">Available</span>
                  <span className="capacity-advisory-value">{formatCapacityValue(test.capacity_advisory.available_generators)}</span>
                  <span className="capacity-advisory-detail">{formatAvailabilityDetail(test.capacity_advisory)}</span>
                </div>
                <div className="capacity-advisory-metric">
                  <span className="capacity-advisory-label">Bottleneck</span>
                  <span className="capacity-advisory-value text">{formatCapacityBottleneck(test.capacity_advisory)}</span>
                  <span className="capacity-advisory-detail">primary sizing driver</span>
                </div>
                <div className="capacity-advisory-metric">
                  <span className="capacity-advisory-label">Usable Per Generator</span>
                  <span className="capacity-advisory-value text">{formatCapacityValue(test.capacity_advisory.usable_per_generator.cpu_cores, 1)} CPU / {formatCapacityValue(test.capacity_advisory.usable_per_generator.memory_mb)} MB</span>
                  <span className="capacity-advisory-detail">after reserved headroom</span>
                </div>
              </div>

              <div className="capacity-advisory-grid compact">
                <div className="capacity-advisory-metric compact">
                  <span className="capacity-advisory-label">Total CPU</span>
                  <span className="capacity-advisory-value">{formatCapacityValue(test.capacity_advisory.estimated_total.cpu_cores, 2)}</span>
                </div>
                <div className="capacity-advisory-metric compact">
                  <span className="capacity-advisory-label">Total Memory</span>
                  <span className="capacity-advisory-value">{formatCapacityValue(test.capacity_advisory.estimated_total.memory_mb)}</span>
                </div>
                <div className="capacity-advisory-metric compact">
                  <span className="capacity-advisory-label">Total Network</span>
                  <span className="capacity-advisory-value">{formatCapacityValue(test.capacity_advisory.estimated_total.network_mbps)}</span>
                </div>
                <div className="capacity-advisory-metric compact">
                  <span className="capacity-advisory-label">Availability Source</span>
                  <span className="capacity-advisory-value text">{formatAvailabilitySource(test.capacity_advisory)}</span>
                </div>
                <div className="capacity-advisory-metric compact">
                  <span className="capacity-advisory-label">Complexity</span>
                  <span className="capacity-advisory-value">{formatCapacityValue(test.capacity_advisory.complexity_score, 2)}</span>
                </div>
              </div>

              {test.capacity_advisory.warnings && test.capacity_advisory.warnings.length > 0 && (
                <div className="capacity-advisory-warnings">
                  {test.capacity_advisory.warnings.map((warning) => (
                    <p key={warning}>{warning}</p>
                  ))}
                </div>
              )}
            </div>
          )}

          <div className="scenario-preview-chart">
            <ResponsiveContainer width="100%" height={260}>
              <AreaChart data={queuedScenarioPreview.points} margin={{ top: 8, right: 12, left: -18, bottom: 4 }}>
                <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                <XAxis
                  dataKey="second"
                  stroke="var(--text-muted)"
                  fontSize={11}
                  tickLine={false}
                  tickFormatter={(value) => formatTimeCompact(Number(value))}
                />
                <YAxis
                  stroke="var(--text-muted)"
                  fontSize={11}
                  tickLine={false}
                  allowDecimals={false}
                />
                <Tooltip
                  labelFormatter={(value) => `T+${formatTimeCompact(Number(value))}`}
                  contentStyle={{ background: 'rgba(33, 36, 47, 0.92)', border: '1px solid var(--border)', borderRadius: 10 }}
                  formatter={(value: number, name: string) => [value, name === 'total' ? 'Total' : name]}
                />
                <Legend verticalAlign="top" height={32} />
                {test.groups.map((group, index) => (
                  <Area
                    key={group.name}
                    type="monotone"
                    dataKey={`group${index}`}
                    name={group.name}
                    stackId="scenario"
                    stroke={groupColors[index % groupColors.length]}
                    fill={groupColors[index % groupColors.length]}
                    fillOpacity={0.34}
                    isAnimationActive={false}
                  />
                ))}
                <Area
                  type="monotone"
                  dataKey="total"
                  name="Total"
                  stroke="var(--text-primary)"
                  strokeWidth={2}
                  fill="transparent"
                  isAnimationActive={false}
                />
                <ReferenceArea
                  x1={queuedScenarioPreview.preBaselineSeconds}
                  x2={queuedScenarioPreview.preBaselineSeconds + queuedScenarioPreview.steadySeconds}
                  fill="var(--accent)"
                  fillOpacity={0.05}
                  strokeOpacity={0}
                />
                {queuedScenarioPreview.preBaselineSeconds > 0 && (
                  <ReferenceArea
                    x1={0}
                    x2={queuedScenarioPreview.preBaselineSeconds}
                    fill="var(--warning)"
                    fillOpacity={0.05}
                    strokeOpacity={0}
                  />
                )}
                {queuedScenarioPreview.postBaselineSeconds > 0 && (
                  <ReferenceArea
                    x1={queuedScenarioPreview.totalSeconds - queuedScenarioPreview.postBaselineSeconds}
                    x2={queuedScenarioPreview.totalSeconds}
                    fill="var(--warning)"
                    fillOpacity={0.05}
                    strokeOpacity={0}
                  />
                )}
              </AreaChart>
            </ResponsiveContainer>
            <div className="scenario-preview-caption">
              Ramp-up and ramp-down are derived from each group's saved configuration. The blue span marks steady-state load, and the amber spans mark the zero-load start delay and optional resource baseline windows.
            </div>
          </div>

          <div className="scenario-settings-grid">
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Duration</span>
              <span className="scenario-setting-value">{test.duration}</span>
            </div>
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Start Delay</span>
              <span className="scenario-setting-value">{test.start_delay || 'Disabled'}</span>
            </div>
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Iterations</span>
              <span className="scenario-setting-value">{test.runtime_settings?.iterations ?? 'Script default'}</span>
            </div>
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Log Level</span>
              <span className="scenario-setting-value">{test.runtime_settings?.log_level || 'Script default'}</span>
            </div>
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Auto Transactions</span>
              <span className="scenario-setting-value">{test.runtime_settings?.auto_transactions || 'Script default'}</span>
            </div>
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Snapshot On Error</span>
              <span className="scenario-setting-value">{test.runtime_settings?.error_handling?.snapshot_on_error ? 'Enabled' : 'Script default'}</span>
            </div>
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Script Shakeout</span>
              <span className="scenario-setting-value">{test.script_shakeout ? 'Enabled' : 'Disabled'}</span>
            </div>
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Resource Baseline</span>
              <span className="scenario-setting-value">{test.resource_baseline ? '5m before and after' : 'Disabled'}</span>
            </div>
            {test.scenario_mode === 'goal_oriented' && test.goal_config?.target_average_tps !== undefined && (
              <div className="scenario-setting-card">
                <span className="scenario-setting-label">Target Avg TPS</span>
                <span className="scenario-setting-value">{formatGoalNumber(test.goal_config.target_average_tps)}</span>
              </div>
            )}
            {test.scenario_mode === 'goal_oriented' && test.goal_config?.p95_latency_ms !== undefined && (
              <div className="scenario-setting-card">
                <span className="scenario-setting-label">P95 Latency Ceiling</span>
                <span className="scenario-setting-value">{formatGoalNumber(test.goal_config.p95_latency_ms, ' ms')}</span>
              </div>
            )}
            {test.scenario_mode === 'goal_oriented' && test.goal_config?.max_error_pct !== undefined && (
              <div className="scenario-setting-card">
                <span className="scenario-setting-label">Max Error %</span>
                <span className="scenario-setting-value">{formatGoalNumber(test.goal_config.max_error_pct, '%')}</span>
              </div>
            )}
            {test.scenario_mode === 'goal_oriented' && test.goal_config?.max_error_count !== undefined && (
              <div className="scenario-setting-card">
                <span className="scenario-setting-label">Max Error Count</span>
                <span className="scenario-setting-value">{formatGoalNumber(test.goal_config.max_error_count)}</span>
              </div>
            )}
            {test.scenario_mode === 'goal_oriented' && (test.goal_config?.min_vusers !== undefined || test.goal_config?.max_vusers !== undefined) && (
              <div className="scenario-setting-card">
                <span className="scenario-setting-label">VUser Guardrails</span>
                <span className="scenario-setting-value">{test.goal_config?.min_vusers ?? 0} - {test.goal_config?.max_vusers ?? 'unbounded'}</span>
              </div>
            )}
            {test.scenario_mode === 'goal_oriented' && (test.goal_config?.sla_breach_window || test.goal_config?.stop_on_sla_breach) && (
              <div className="scenario-setting-card">
                <span className="scenario-setting-label">SLA Breach Window</span>
                <span className="scenario-setting-value">{test.goal_config?.sla_breach_window || '30s'}</span>
              </div>
            )}
            {test.scenario_mode === 'goal_oriented' && (
              <div className="scenario-setting-card">
                <span className="scenario-setting-label">Stop On SLA Breach</span>
                <span className="scenario-setting-value">{test.goal_config?.stop_on_sla_breach ? 'Enabled' : 'Disabled'}</span>
              </div>
            )}
            {test.scenario_mode === 'goal_oriented' && (
              <div className="scenario-setting-card">
                <span className="scenario-setting-label">Stop On Max Errors</span>
                <span className="scenario-setting-value">{test.goal_config?.stop_on_max_errors ? 'Enabled' : 'Disabled'}</span>
              </div>
            )}
          </div>

          <div className="scenario-script-section">
            <div className="scenario-script-section-header">
              <div>
                <h4 className="scenario-script-section-title">Scenario Scripts</h4>
                <p className="scenario-script-section-subtitle">Each row is a script group in this scenario. Expand a row to inspect the effective script configuration that will run.</p>
              </div>
            </div>

            {scriptDetailsLoading && <p className="scenario-script-status">Loading script settings...</p>}
            {scriptDetailsError && <p className="scenario-script-status error">{scriptDetailsError}</p>}

            <div className="scenario-script-rows">
              {queuedScenarioRows.map(({ group, detail, actionCount, initCount, teardownCount, dataSourceCount }, index) => {
                const isExpanded = expandedScenarioScript === group.name
                const toggleExpanded = () => onExpandedScenarioScriptChange(isExpanded ? null : group.name)
                return (
                  <div key={`${group.name}-${group.script_id}`} className={`scenario-script-row ${isExpanded ? 'expanded' : ''}`}>
                    <div
                      className="scenario-script-summary"
                      role="button"
                      tabIndex={0}
                      onClick={toggleExpanded}
                      onKeyDown={(event) => {
                        if (event.key === 'Enter' || event.key === ' ') {
                          event.preventDefault()
                          toggleExpanded()
                        }
                      }}
                    >
                      <span className="scenario-script-color" style={{ background: groupColors[index % groupColors.length] }}></span>
                      <span className="scenario-script-cell scenario-script-name-cell">
                        <span className="scenario-script-primary">{group.name}</span>
                        <span className="scenario-script-secondary">{group.script_name}</span>
                      </span>
                      <span className="scenario-script-cell">{group.protocol.toUpperCase()}</span>
                      <span className="scenario-script-cell">{group.vusers} VUsers</span>
                      <span className="scenario-script-cell">{group.ramp_up || 'No ramp-up'}</span>
                      <span className="scenario-script-cell">{group.ramp_down || 'No ramp-down'}</span>
                      <span className="scenario-script-cell">{group.initialization || 'at_runtime'}</span>
                      <span className="scenario-script-actions">
                        <button
                          type="button"
                          className="btn btn-sm btn-ghost scenario-script-edit-btn"
                          onClick={(event) => {
                            event.stopPropagation()
                            onEditScript(group.script_id)
                          }}
                        >
                          Edit Script
                        </button>
                        <span className="scenario-script-toggle">{isExpanded ? 'Hide' : 'Inspect'}</span>
                      </span>
                    </div>

                    {isExpanded && (
                      <div className="scenario-script-detail-panel">
                        <div className="scenario-script-detail-grid">
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">Base URL</span>
                            <span className="scenario-script-detail-value">{detail?.script?.config?.base_url || 'Not configured'}</span>
                          </div>
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">Think Time</span>
                            <span className="scenario-script-detail-value">{formatThinkTimeLabel(detail)}</span>
                          </div>
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">Pacing</span>
                            <span className="scenario-script-detail-value">{formatPacingLabel(detail)}</span>
                          </div>
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">Iterations</span>
                            <span className="scenario-script-detail-value">{formatEffectiveIterations(group, detail, test)}</span>
                          </div>
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">Auto Transactions</span>
                            <span className="scenario-script-detail-value">{formatEffectiveAutoTransactions(detail, test)}</span>
                          </div>
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">Log Level</span>
                            <span className="scenario-script-detail-value">{formatEffectiveLogLevel(detail, test)}</span>
                          </div>
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">Snapshot On Error</span>
                            <span className="scenario-script-detail-value">{formatSnapshotOnError(detail, test)}</span>
                          </div>
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">HTTP Runtime</span>
                            <span className="scenario-script-detail-value">{detail?.script?.config?.http?.version || 'auto'} · {detail?.script?.config?.http?.max_conns_per_host || 100} conns/host</span>
                          </div>
                        </div>

                        <div className="scenario-script-asset-grid">
                          <div className="scenario-script-asset-card">
                            <span className="scenario-script-detail-label">Actions</span>
                            <span className="scenario-script-detail-value">{actionCount}</span>
                          </div>
                          <div className="scenario-script-asset-card">
                            <span className="scenario-script-detail-label">Init</span>
                            <span className="scenario-script-detail-value">{initCount}</span>
                          </div>
                          <div className="scenario-script-asset-card">
                            <span className="scenario-script-detail-label">Teardown</span>
                            <span className="scenario-script-detail-value">{teardownCount}</span>
                          </div>
                          <div className="scenario-script-asset-card">
                            <span className="scenario-script-detail-label">Data Sources</span>
                            <span className="scenario-script-detail-value">{dataSourceCount}</span>
                          </div>
                        </div>
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          </div>
        </div>
      )}
    </>
  )
}