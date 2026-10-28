import TelemetryPolicy from '../TelemetryPolicy'
import TelemetryAcceptanceVerdict from '../TelemetryAcceptanceVerdict'
import { TelemetryPolicySegmentList } from '../TelemetryPolicySegments'
import SeriesChartExport from "../SeriesChartExport"
import LoadPlanChart from '../LoadPlanChart'
import { LOAD_PLAN_COLORS } from '../loadPlanSteps'
import type { MetricsHistoryPoint, RunArchiveReport, TestRunManifest, TestSummary } from '../../types'
import type { useExecutionTestManifest } from '../useExecutionTestManifest'
import { executionDate, executionFacts, executionLoadPlan, executionLoadPoints, executionObservedLoad, executionPauses, recordedNumber } from './executionPresentation'
import { formatTimeLong } from '../../utils/timeUtils'

export function RunExecutionFacts({ test, report, manifest }: { test: TestSummary; report: RunArchiveReport | null; manifest?: TestRunManifest | null }) {
  const facts = executionFacts(test, report, manifest)
  return <dl className="run-execution-facts" aria-label="Execution facts">
    <div><dt>Configured duration</dt><dd>{facts.duration || 'Not recorded'}</dd></div>
    <div><dt>Lifecycle elapsed</dt><dd>{facts.lifecycleElapsed}</dd></div>
    {report && <div><dt>Archived evidence elapsed</dt><dd>{facts.elapsed}</dd></div>}
    <div><dt>Configured VUsers</dt><dd>{recordedNumber(facts.vusers)}</dd></div>
    <div><dt>Observed peak VUsers</dt><dd>{facts.peak}</dd></div>
  </dl>
}

export function RetainedRunMetrics({ test }: { test: TestSummary }) {
  const metrics = test.live_metrics
  return <section className="run-report-panel" aria-label="Retained controller sample">
    <h2>Retained controller sample</h2>
    <p>Captured at {executionDate(metrics?.timestamp)}. The metric population and aggregation window are not established for this retained record. These values are not whole-run result KPIs.</p>
    {metrics ? <>
      <p role="status">Historical sample. VUsers at capture do not indicate currently running VUsers.</p>
      <dl className="run-execution-facts">
        <div><dt>VUsers at capture</dt><dd>{recordedNumber(metrics.active_vusers)}</dd></div>
        <div><dt>Reported transaction count</dt><dd>{recordedNumber(metrics.total_transactions)}</dd></div>
        <div><dt>Reported error count</dt><dd>{recordedNumber(metrics.total_errors)}</dd></div>
      </dl>
      <p className="run-report-muted">Sampled transaction success does not establish successful execution or performance acceptance. Whole-run rates and latency percentiles are unavailable without qualified archive evidence.</p>
    </> : <p>No controller metric sample was retained. This does not mean that no work executed.</p>}
  </section>
}

export function RetainedRunDiagnostics({ test }: { test: TestSummary }) {
  const sample = test.live_metrics
  const telemetry = sample?.telemetry
  return <section className="run-report-panel" aria-label="Retained diagnostics">
    <h2>Retained diagnostics</h2>
    <p>These records describe the last retained sample, not current system health or a successful run outcome.</p>
    <dl className="run-execution-facts">
      <div><dt>Metric capture</dt><dd>{executionDate(sample?.timestamp)}</dd></div>
      <div><dt>Generator publish</dt><dd>{executionDate(telemetry?.generator_published_at)}</dd></div>
      <div><dt>Controller receive</dt><dd>{executionDate(telemetry?.controller_received_at)}</dd></div>
    </dl>
    <p>Archived transaction diagnostics are unavailable. Review the recorded execution reason and retained events for the available failure evidence.</p>
  </section>
}

interface Props {
  test: TestSummary
  report: RunArchiveReport | null
  history?: MetricsHistoryPoint[]
  historyLoading?: boolean
  historyError?: string
  evidenceOnly?: boolean
  manifestModel: ReturnType<typeof useExecutionTestManifest>
}

export default function RunExecutionDetails({ test, report, history = [], historyLoading, historyError, evidenceOnly = false, manifestModel }: Props) {
  const { manifest, manifestError, manifestLoading, reloadManifest } = manifestModel
  const facts = executionFacts(test, report, manifest)
  const points = executionLoadPoints(history)
  const plan = executionLoadPlan(test, manifest)
  const observed = executionObservedLoad(points, facts.started)
  // The plan's steps and the observed samples, each at its own elapsed second.
  const exportRows = [
    ...plan.points.map(point => ({ second: point.second, planned: point.total })),
    ...observed.map(sample => ({ second: sample.second, timestampMs: sample.timestampMs, vusers: sample.vusers })),
  ].sort((left, right) => left.second - right.second)
  const events = manifest?.runtime_changes || []
  // The run made each pause up; the plan moves on by the pauses its events place.
  const pausedSeconds = Math.max(0, Number(test.cumulative_pause_ns) || 0) / 1e9
  const placedPauseSeconds = test.termination_mode === 'iterations' ? 0 : executionPauses(test, manifest).reduce((total, pause) => total + pause.seconds, 0)
  const pauseNote = placedPauseSeconds > 0
    ? ` The run was paused for ${formatTimeLong(Math.round(placedPauseSeconds))} and made it up, so the plan moves on by its pauses.`
    : pausedSeconds >= 1 && test.termination_mode !== 'iterations'
      ? ` The run was paused for ${formatTimeLong(Math.round(pausedSeconds))} and made it up, but its retained events do not say when, so the plan is drawn without the pause.`
      : ''
  return <>
    {!evidenceOnly && <>
      <p className="run-report-muted">Read-only execution record. {facts.immutable ? 'Original configuration is preserved.' : 'Configured values come from a mutable retained record; the original plan is unverified.'} Execution completion and acceptance are assessed separately.</p>
      <RunExecutionFacts test={test} report={report} manifest={manifest} />
      <section className="run-report-panel" aria-label="Execution load timeline">
        <h2>Planned and observed load</h2>
        <p>The plan is the engine's schedule for {facts.immutable ? 'the preserved configuration' : 'the retained configuration, which may reflect later changes'}: {recordedNumber(facts.vusers)} VUsers, started and stopped as each group ramps. Observed samples cover only the captured intervals.{pauseNote}</p>
        {historyLoading && <p role="status">Loading captured load history…</p>}
        {historyError && <p role="alert">{historyError}</p>}
        {(plan.points.length > 0 || observed.length > 0) && <div className="run-execution-chart">
          <SeriesChartExport id="execution-load" title="Planned and observed load" rows={exportRows} axes={[{ key: "utc", label: "UTC timestamp", value: point => 'timestampMs' in point && point.timestampMs !== undefined ? new Date(point.timestampMs).toISOString() : undefined }, { key: "elapsed", label: "Elapsed seconds", value: point => point.second }]} series={[{ key: "vusers", label: "Observed VUsers", unit: "VUsers" }, { key: "planned", label: "Planned VUsers", unit: "VUsers" }]} context={{ run_id: test.id, configured_vusers: facts.vusers, configuration_immutable: facts.immutable }}>
            <LoadPlanChart
              label="Planned and observed load"
              points={plan.points}
              totalSeconds={plan.totalSeconds}
              groups={facts.groups.map((group, index) => ({ key: `group${index}`, label: group.name || `Group ${index + 1}`, color: LOAD_PLAN_COLORS[index % LOAD_PLAN_COLORS.length] }))}
              phases={plan}
              observed={observed}
              height={240}
            />
          </SeriesChartExport>
        </div>}
        {!historyLoading && !historyError && observed.length === 0 && <p>No interval history was retained. Planned workload completion cannot be inferred from the final controller sample.</p>}
      </section>
      <section className="run-report-panel" aria-label="Execution groups">
        <h2>Groups and configuration</h2>
        <p>{facts.immutable ? 'Configuration preserved in the immutable run manifest.' : 'Configuration from the retained test record. It may reflect later changes; the original plan is unverified.'}</p>
        {facts.groups.length > 0 ? <div className="run-report-table-wrap"><table className="run-report-table run-execution-groups">
          <thead><tr><th scope="col">Group</th><th scope="col">Script</th><th scope="col">Configured VUsers</th><th scope="col">Configuration details</th></tr></thead>
          <tbody>{facts.groups.map((group, index) => <tr key={`${group.name}-${index}`}>
            <th scope="row">{group.name || 'Unnamed group'}</th><td>{group.script_name || group.script_id || 'Not recorded'}</td><td>{recordedNumber(group.vusers)}</td>
            <td><details><summary>View configuration</summary><dl className="manifest-definition-grid run-execution-group-config">
              <div className="manifest-definition"><dt>Protocol</dt><dd>{manifest?.scripts?.find(script => script.group_name === group.name)?.protocol || 'Not recorded'}</dd></div>
              <div className="manifest-definition"><dt>Ramp up</dt><dd>{group.ramp_up || 'Not recorded'}</dd></div>
              <div className="manifest-definition"><dt>Initialization</dt><dd>{group.initialization || 'Not recorded'}</dd></div>
            </dl></details></td>
          </tr>)}</tbody>
        </table></div> : <p>No group configuration was retained.</p>}
      </section>
    </>}
    <section className="run-report-panel" aria-label="Execution events">
      <h2>Execution events</h2>
      <dl className="run-execution-facts">
        <div><dt>Created</dt><dd>{executionDate(test.created_at)}</dd></div>
        <div><dt>Started</dt><dd>{executionDate(facts.started)}</dd></div>
        <div><dt>Ended</dt><dd>{executionDate(facts.ended)}</dd></div>
      </dl>
      {manifestLoading && <p role="status">Loading retained events…</p>}
      {manifestError && <p role="alert">{manifestError} <button type="button" className="btn btn-secondary" onClick={() => void reloadManifest()}>Retry records</button></p>}
      {events.length > 0 ? <ol className="run-execution-events">{events.map(event => <li key={event.id}>
        <strong>{event.label}</strong><span>{executionDate(event.created_at)}{event.status ? ` · ${event.status}` : ''}</span>
      </li>)}</ol> : !manifestLoading && !manifestError && manifest && <p>No lifecycle event history was retained. Missing events do not establish that an action never occurred.</p>}
    </section>
    {/* On the evidence tabs the telemetry budget receipt is shown; on Execution it
        is evidence about the run, not its story, so it waits with the manifest. */}
    {manifest && evidenceOnly && <TelemetryPolicy evidence={manifest.telemetry_policy} />}
    <TelemetryAcceptanceVerdict verdict={report?.telemetry_acceptance} />
    {/* A run whose policy changed while it ran: its receipt is only its first segment. Before its archive is written, the run's own list. */}
    <TelemetryPolicySegmentList segments={report?.telemetry_policy_segments ?? test.telemetry_policy_segments} />
    <details className="run-execution-records"><summary>Configuration and infrastructure evidence</summary>
      {manifest && !evidenceOnly && <TelemetryPolicy evidence={manifest.telemetry_policy} />}
      <p>{manifest?.source === 'archive' ? 'Archived run manifest.' : 'Retained controller record.'} Document generated: {executionDate(manifest?.generated_at)}. Generation time is not the time the run was captured.</p>
      {manifest?.warnings?.map(warning => <p key={warning}>{warning}</p>)}
      <p>{facts.immutable ? 'Initial configuration is immutable.' : 'Initial configuration is not verified as immutable.'}</p>
      {manifest ? <details><summary>Raw manifest and captured infrastructure</summary><pre>{JSON.stringify(manifest, null, 2)}</pre></details> : <p>Manifest evidence is unavailable.</p>}
    </details>
  </>
}
