import LoadPlanChart from './LoadPlanChart'

import { CapacityAdvisory, RuntimeSettingsOverride, ScriptShakeoutReport, TestSummary } from '../types'
import { formatBrowserRuntimeSummary } from './browserRuntimeSummary'
import { effectiveRuntimeSection, resolveRuntimeRowsValue, runtimeBrowserSettings, runtimeSectionSource } from './runtimeSettingsModel'
import { QueuedScenarioPreview, QueuedScenarioRow, ScenarioScriptDetail } from './testExecutionScenarioModel'
import type { ScenarioSnapshotSource } from './useExecutionScenarioPreviewModel'
import { formatTimeCompact } from '../utils/timeUtils'
import { formatScriptHTTPVersion } from './httpVersionModel'
import { formatPacingTiming } from './runtimeTiming'
import { formatDateTime } from '../utils/dateTime'

interface Props {
  test: TestSummary
  startCapacityConflict: CapacityAdvisory | null
  startShakeoutConflict: ScriptShakeoutReport | null
  startFeedback: string
  onDismissStartConflict: () => void
  onConfirmStartAnyway: () => void
  onContinueAfterShakeoutFailure: () => void
  onCancelShakeoutRun: () => void
  queuedScenarioPreview: QueuedScenarioPreview
  queuedScenarioRows: QueuedScenarioRow[]
  snapshotSource: ScenarioSnapshotSource
  snapshotLoading: boolean
  snapshotError: string
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

function scenarioSnapshotSummary(source: ScenarioSnapshotSource, loading: boolean): string {
  switch (source) {
    case 'immutable_manifest':
      return 'Load shape and runtime values come from this run’s immutable queued snapshot.'
    case 'stored_manifest':
      return 'An immutable queued snapshot is unavailable; this preview was reconstructed from the mutable stored run record and may include later changes.'
    case 'retained_manifest':
      return 'An immutable queued snapshot is unavailable; this preview was synthesized from retained controller state and may reflect later changes.'
    default:
      return loading
        ? 'Loading the immutable queued snapshot; the retained run summary is shown temporarily.'
        : 'The immutable queued snapshot is unavailable; this preview falls back to the retained run summary.'
  }
}

function formatThinkTimeLabel(scriptDetail?: ScenarioScriptDetail): string {
  if (!scriptDetail) return 'Not exposed by run snapshot'
  const thinkTime = scriptDetail?.script?.config?.think_time
  if (!thinkTime) return 'Not configured in captured script detail'
  if (thinkTime.ignore) return 'Disabled'
  if (thinkTime.type === 'recorded') return 'Replay recorded think time'
  return `${thinkTime.type || 'fixed'} · ${thinkTime.mean_ms || 0}ms`
}

function formatPacingLabel(scriptDetail?: ScenarioScriptDetail): string {
  if (!scriptDetail) return 'Not exposed by run snapshot'
  const pacing = scriptDetail?.script?.config?.pacing
  return formatPacingTiming(pacing) || 'None'
}

type PreviewGroup = TestSummary['groups'][number] | undefined

// Each displayed path is resolved through the group row and then the run's row the way the
// backend applies them (resolveRuntimeRowsValue): a partial row (override_fields) supplies only
// the paths it lists, canonical sections and compact aliases alike, while a v1 row supplies every
// field of a section it carries — error_handling included, whose omitted half executes false.
function previewRuntimeValue(path: string, group: PreviewGroup, test: TestSummary | undefined): { value: unknown; origin: string } | null {
  const { index, value } = resolveRuntimeRowsValue(path, [group?.runtime_settings, test?.runtime_settings])
  if (index < 0) return null
  return { value, origin: index === 0 && group ? ' (group override)' : ' (scenario override)' }
}

/** A listed path with a usable value (an empty listed value falls through to the next layer). */
function previewRuntimeSetting(path: string, group: PreviewGroup, test: TestSummary | undefined): { value: unknown; origin: string } | null {
  const resolved = previewRuntimeValue(path, group, test)
  return resolved && resolved.value !== undefined && resolved.value !== null && resolved.value !== '' ? resolved : null
}

type TimingSection = 'pacing' | 'think_time'

/**
 * A pacing or think_time section resolved the same way: each row contributes only the paths it
 * lists, laid over the script's section when the snapshot carries it, and a row listing the whole
 * section (a v1 row, or a partial row listing it) replaces what is below. The origin is the
 * highest row that lists anything inside the section.
 */
function previewRuntimeSection<K extends TimingSection>(
  section: K,
  group: PreviewGroup,
  test: TestSummary | undefined,
  scriptDetail?: ScenarioScriptDetail,
): { value: RuntimeSettingsOverride[K] | undefined; origin: string; whole: boolean } | null {
  const layers = [group?.runtime_settings, test?.runtime_settings]
  const source = runtimeSectionSource(section, layers)
  if (!source) return null
  const base = scriptDetail?.script?.config?.[section] as RuntimeSettingsOverride[K] | undefined
  const value = effectiveRuntimeSection<RuntimeSettingsOverride[K]>(section, layers, source.whole ? undefined : base) ?? undefined
  return { value, origin: source.layer === 0 && group ? ' (group override)' : ' (scenario override)', whole: source.whole }
}

function formatPreviewThinkTime(thinkTime?: RuntimeSettingsOverride['think_time'] | null): string | null {
  if (!thinkTime) return null
  if (thinkTime.ignore) return 'Disabled'
  if (thinkTime.type === 'recorded') return 'Replay recorded think time'
  return `${thinkTime.type || 'fixed'} · ${thinkTime.mean_ms || 0}ms`
}

/** A partial row over a script section the snapshot does not expose has no complete value. */
const PARTIAL_TIMING_LABEL: Record<TimingSection, string> = {
  pacing: 'Script pacing with overridden fields',
  think_time: 'Script think time with overridden fields',
}

/** A whole section that names no type (an empty v1 section) leaves the script's timing. */
const UNTYPED_TIMING_LABEL: Record<TimingSection, string> = {
  pacing: 'Script timing',
  think_time: 'Script think time',
}

function timingFallbackLabel(section: TimingSection, whole: boolean): string {
  return whole ? UNTYPED_TIMING_LABEL[section] : PARTIAL_TIMING_LABEL[section]
}

function formatPreviewTiming(section: TimingSection, group: PreviewGroup, test: TestSummary | undefined, scriptDetail?: ScenarioScriptDetail): string | null {
  const resolved = previewRuntimeSection(section, group, test, scriptDetail)
  if (!resolved) return null
  const text = section === 'pacing'
    ? formatPacingTiming(resolved.value as RuntimeSettingsOverride['pacing'])
    : formatPreviewThinkTime(resolved.value as RuntimeSettingsOverride['think_time'])
  return `${text || timingFallbackLabel(section, resolved.whole)}${resolved.origin}`
}

function formatOverridePacing(test: TestSummary): string {
  const resolved = previewRuntimeSection('pacing', undefined, test)
  if (!resolved) return 'Script-level value not exposed'
  return formatPacingTiming(resolved.value) || timingFallbackLabel('pacing', resolved.whole)
}

function formatOverrideThinkTime(test: TestSummary): string {
  const resolved = previewRuntimeSection('think_time', undefined, test)
  if (!resolved) return 'Script-level value not exposed'
  return formatPreviewThinkTime(resolved.value) || timingFallbackLabel('think_time', resolved.whole)
}

function formatEffectivePacing(group: TestSummary['groups'][number], scriptDetail: ScenarioScriptDetail | undefined, test: TestSummary): string {
  return formatPreviewTiming('pacing', group, test, scriptDetail) ?? formatPacingLabel(scriptDetail)
}

function formatEffectiveThinkTime(group: TestSummary['groups'][number], scriptDetail: ScenarioScriptDetail | undefined, test: TestSummary): string {
  return formatPreviewTiming('think_time', group, test, scriptDetail) ?? formatThinkTimeLabel(scriptDetail)
}

function formatContinueOnError(test: TestSummary): string {
  const resolved = previewRuntimeValue('error_handling.continue_on_error', undefined, test)
  if (!resolved) return 'Script-level value not exposed'
  return resolved.value ? 'Enabled' : 'Disabled'
}

function formatRunSnapshotOnError(test: TestSummary): string {
  const resolved = previewRuntimeValue('error_handling.snapshot_on_error', undefined, test)
  if (!resolved) return 'Script-level value not exposed'
  return resolved.value ? 'Enabled' : 'Disabled'
}

function formatRunRuntimeValue(test: TestSummary, path: string): string {
  const resolved = previewRuntimeSetting(path, undefined, test)
  return resolved ? String(resolved.value) : 'Script-level value not exposed'
}

function formatEffectiveIterations(group: TestSummary['groups'][number], scriptDetail: ScenarioScriptDetail | undefined, test: TestSummary): string {
  const groupOverride = previewRuntimeSetting('run_logic.iterations', group, undefined)
  if (groupOverride) return `${groupOverride.value} (group override)`
  if (group.iterations !== undefined) return `${group.iterations} (group override)`
  const override = previewRuntimeSetting('run_logic.iterations', undefined, test)
  if (override) return `${override.value} (scenario override)`
  const iterations = scriptDetail?.script?.config?.run_logic?.iterations
  if (!scriptDetail) return 'Not exposed by run snapshot'
  if (!iterations) return 'Duration driven'
  return `${iterations}`
}

function formatEffectiveAutoTransactions(group: TestSummary['groups'][number], scriptDetail: ScenarioScriptDetail | undefined, test: TestSummary): string {
  const resolved = previewRuntimeSetting('run_logic.auto_transactions', group, test)
  if (resolved) return `${resolved.value}${resolved.origin}`
  if (!scriptDetail) return 'Not exposed by run snapshot'
  return scriptDetail.script?.config?.run_logic?.auto_transactions || 'none'
}

function formatEffectiveLogLevel(group: TestSummary['groups'][number], scriptDetail: ScenarioScriptDetail | undefined, test: TestSummary): string {
  const resolved = previewRuntimeSetting('log.level', group, test)
  if (resolved) return `${resolved.value}${resolved.origin}`
  if (!scriptDetail) return 'Not exposed by run snapshot'
  return scriptDetail.script?.config?.log?.level || 'standard'
}

function formatSnapshotOnError(group: TestSummary['groups'][number], scriptDetail: ScenarioScriptDetail | undefined, test: TestSummary): string {
  const resolved = previewRuntimeValue('error_handling.snapshot_on_error', group, test)
  if (resolved) return `${resolved.value ? 'Enabled' : 'Disabled'}${resolved.origin}`
  if (!scriptDetail) return 'Not exposed by run snapshot'
  return scriptDetail.script?.config?.error_handling?.snapshot_on_error ? 'Enabled' : 'Disabled'
}

function formatEffectiveHTTPRuntime(group: TestSummary['groups'][number], scriptDetail: ScenarioScriptDetail | undefined, test: TestSummary): string {
  const versionLayer = previewRuntimeValue('http.version', group, test)
  const maxConnsLayer = previewRuntimeValue('http.max_conns_per_host', group, test)
  if (!scriptDetail && !versionLayer && !maxConnsLayer) return 'Not exposed by run snapshot'
  const protocol = scriptDetail?.protocol || group.protocol
  const version = versionLayer
    ? versionLayer.value as NonNullable<RuntimeSettingsOverride['http']>['version']
    : scriptDetail?.script?.config?.http?.version
  const maxConns = (maxConnsLayer?.value as number | undefined)
    ?? scriptDetail?.script?.config?.http?.max_conns_per_host
  return `${formatScriptHTTPVersion(protocol, version)} · ${maxConns || 100} conns/host`
}

function formatGoalNumber(value: number, suffix = ''): string {
  return `${Number.isInteger(value) ? value.toString() : value.toFixed(1)}${suffix}`
}

export default function TestExecutionScenarioPreview({
  test,
  startCapacityConflict,
  startShakeoutConflict,
  startFeedback,
  onDismissStartConflict,
  onConfirmStartAnyway,
  onContinueAfterShakeoutFailure,
  onCancelShakeoutRun,
  queuedScenarioPreview,
  queuedScenarioRows,
  snapshotSource,
  snapshotLoading,
  snapshotError,
  expandedScenarioScript,
  onExpandedScenarioScriptChange,
  onEditScript,
  groupColors,
}: Props) {
  if (test.status !== 'QUEUED' && test.status !== 'PENDING' && test.status !== 'RESERVED' && test.status !== 'SHAKEOUT' && test.status !== 'SHAKEOUT_REVIEW') {
    return null
  }
  const browserRuntimeSummary = formatBrowserRuntimeSummary(runtimeBrowserSettings(test.runtime_settings))
  const snapshotSummary = scenarioSnapshotSummary(snapshotSource, snapshotLoading)
  const shakeoutReviewRequired = test.status === 'SHAKEOUT_REVIEW' || Boolean(startShakeoutConflict)
  const shakeoutEvidence = startShakeoutConflict?.results?.filter(result => result.status === 'failed') || []

  return (
    <>
      {test.status === 'SHAKEOUT' && (
        <div className="scenario-alert-banner scenario-alert-info" role="status">
          <div>
            <h3 className="scenario-alert-title">Running script shakeout</h3>
            <p className="scenario-alert-message">APEX Load is executing a one-pass canary for each script group before the full load starts.</p>
          </div>
        </div>
      )}

      {shakeoutReviewRequired && (
        <div className="scenario-alert-banner scenario-alert-warning" role="alert">
          <div>
            <h3 className="scenario-alert-title">Script shakeout needs review</h3>
            <p className="scenario-alert-message">{startShakeoutConflict?.summary || test.error || 'Script shakeout returned errors; the full load test has not started.'}</p>
            {shakeoutEvidence.length > 0 ? (
              <div className="scenario-alert-evidence">
                {shakeoutEvidence.map(result => (
                  <div key={`${result.group_name}-${result.script_id || result.script_name || result.error}`}>
                    <strong>{result.group_name}{result.script_name ? ` · ${result.script_name}` : ''}</strong>
                    <span>{result.error || 'Shakeout failed without an error message.'}</span>
                  </div>
                ))}
              </div>
            ) : test.error ? (
              <pre className="scenario-alert-evidence-text">{test.error}</pre>
            ) : null}
            {startFeedback && <p className="scenario-alert-message secondary">{startFeedback}</p>}
          </div>
          <div className="scenario-alert-actions">
            <button className="btn btn-secondary" onClick={onCancelShakeoutRun}>
              Cancel Run
            </button>
            <button className="btn btn-warning" onClick={onContinueAfterShakeoutFailure}>
              Continue Full Load
            </button>
          </div>
        </div>
      )}

      {test.status === 'PENDING' && (
        <div className="scenario-alert-banner scenario-alert-info" role="status">
          <div>
            <h3 className="scenario-alert-title">Queued for project capacity</h3>
            <p className="scenario-alert-message">
              {test.queue_position ? `Queue position #${test.queue_position}. ` : ''}
              {test.pending_reason || 'Waiting for an available project slot.'}
            </p>
            {test.pending_since && (
              <p className="scenario-alert-message secondary">Waiting since {formatDateTime(test.pending_since, { seconds: true })}.</p>
            )}
          </div>
        </div>
      )}

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

      {(snapshotSource !== 'immutable_manifest' || snapshotLoading || snapshotError) && (
        <p className={`scenario-script-status ${snapshotError ? 'error' : ''}`} role="status">
          {snapshotSummary}{snapshotError ? ` ${snapshotError}` : ''}
        </p>
      )}

      {queuedScenarioPreview.points.length > 0 && (
        <div className="scenario-preview-card">
          <div className="scenario-preview-header">
            <div>
              <h3 className="scenario-preview-title">Scenario Run Model</h3>
              <p className="scenario-preview-subtitle">{snapshotSummary}</p>
            </div>
            <div className="scenario-preview-stats">
              <div className="scenario-preview-stat">
                <span className="scenario-preview-stat-label">Peak</span>
                <span className="scenario-preview-stat-value">{queuedScenarioPreview.totalVUsers} VUsers</span>
              </div>
              <div className="scenario-preview-stat">
                <span className="scenario-preview-stat-label">Steady state</span>
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
                <span className="scenario-preview-stat-label">Start delay</span>
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
            <LoadPlanChart
              label="Queued scenario load"
              points={queuedScenarioPreview.points}
              totalSeconds={queuedScenarioPreview.totalSeconds}
              groups={test.groups.map((group, index) => ({ key: `group${index}`, label: group.name, color: groupColors[index % groupColors.length] }))}
              phases={queuedScenarioPreview}
              height={260}
            />
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
              <span className="scenario-setting-label">Start delay</span>
              <span className="scenario-setting-value">{test.start_delay || 'Disabled'}</span>
            </div>
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Iterations</span>
              <span className="scenario-setting-value">{formatRunRuntimeValue(test, 'run_logic.iterations')}</span>
            </div>
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Log level</span>
              <span className="scenario-setting-value">{formatRunRuntimeValue(test, 'log.level')}</span>
            </div>
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Auto transactions</span>
              <span className="scenario-setting-value">{formatRunRuntimeValue(test, 'run_logic.auto_transactions')}</span>
            </div>
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Pacing</span>
              <span className="scenario-setting-value">{formatOverridePacing(test)}</span>
            </div>
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Think time</span>
              <span className="scenario-setting-value">{formatOverrideThinkTime(test)}</span>
            </div>
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Browser Runtime</span>
              <span className="scenario-setting-value">{browserRuntimeSummary || 'Script-level value not exposed'}</span>
            </div>
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Continue on error</span>
              <span className="scenario-setting-value">{formatContinueOnError(test)}</span>
            </div>
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Snapshot on error</span>
              <span className="scenario-setting-value">{formatRunSnapshotOnError(test)}</span>
            </div>
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Script shakeout</span>
              <span className="scenario-setting-value">{test.script_shakeout ? 'Enabled' : 'Disabled'}</span>
            </div>
            <div className="scenario-setting-card">
              <span className="scenario-setting-label">Resource baseline</span>
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
            {test.goal_config?.transaction_response_time_sla_ms !== undefined && (
              <div className="scenario-setting-card">
                <span className="scenario-setting-label">Transaction SLA default</span>
                <span className="scenario-setting-value">{formatGoalNumber(test.goal_config.transaction_response_time_sla_ms, ' ms')} · 95% required</span>
              </div>
            )}
            {test.groups.some(group => group.transaction_response_time_sla_ms !== undefined) && (
              <div className="scenario-setting-card">
                <span className="scenario-setting-label">Transaction SLA overrides</span>
                <span className="scenario-setting-value">{test.groups.filter(group => group.transaction_response_time_sla_ms !== undefined).map(group => `${group.name}: ${formatGoalNumber(group.transaction_response_time_sla_ms!, ' ms')}`).join(' · ')}</span>
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
                <p className="scenario-script-section-subtitle">Group and runtime override values come from the run snapshot. Full script bodies are not included; open the current library script only as a separate reference.</p>
              </div>
            </div>

            {snapshotLoading && <p className="scenario-script-status">Loading run configuration provenance…</p>}
            {snapshotError && <p className="scenario-script-status error">Immutable run snapshot unavailable: {snapshotError}</p>}

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
                          Open Current Library Script
                        </button>
                        <span className="scenario-script-toggle">{isExpanded ? 'Hide' : 'Inspect'}</span>
                      </span>
                    </div>

                    {isExpanded && (
                      <div className="scenario-script-detail-panel">
                        <div className="scenario-script-detail-grid">
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">Base URL</span>
                            <span className="scenario-script-detail-value">{detail ? detail.script?.config?.base_url || 'Not configured' : 'Not exposed by run snapshot'}</span>
                          </div>
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">Think time</span>
                            <span className="scenario-script-detail-value">{formatEffectiveThinkTime(group, detail, test)}</span>
                          </div>
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">Pacing</span>
                            <span className="scenario-script-detail-value">{formatEffectivePacing(group, detail, test)}</span>
                          </div>
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">Iterations</span>
                            <span className="scenario-script-detail-value">{formatEffectiveIterations(group, detail, test)}</span>
                          </div>
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">Auto transactions</span>
                            <span className="scenario-script-detail-value">{formatEffectiveAutoTransactions(group, detail, test)}</span>
                          </div>
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">Log level</span>
                            <span className="scenario-script-detail-value">{formatEffectiveLogLevel(group, detail, test)}</span>
                          </div>
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">Snapshot on error</span>
                            <span className="scenario-script-detail-value">{formatSnapshotOnError(group, detail, test)}</span>
                          </div>
                          <div className="scenario-script-detail-card">
                            <span className="scenario-script-detail-label">HTTP Runtime</span>
                            <span className="scenario-script-detail-value">{formatEffectiveHTTPRuntime(group, detail, test)}</span>
                          </div>
                        </div>

                        <div className="scenario-script-asset-grid">
                          <div className="scenario-script-asset-card">
                            <span className="scenario-script-detail-label">Actions</span>
                            <span className="scenario-script-detail-value">{actionCount ?? 'Not exposed'}</span>
                          </div>
                          <div className="scenario-script-asset-card">
                            <span className="scenario-script-detail-label">Init</span>
                            <span className="scenario-script-detail-value">{initCount ?? 'Not exposed'}</span>
                          </div>
                          <div className="scenario-script-asset-card">
                            <span className="scenario-script-detail-label">Teardown</span>
                            <span className="scenario-script-detail-value">{teardownCount ?? 'Not exposed'}</span>
                          </div>
                          <div className="scenario-script-asset-card">
                            <span className="scenario-script-detail-label">Data Sources</span>
                            <span className="scenario-script-detail-value">{dataSourceCount ?? 'Not exposed'}</span>
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
