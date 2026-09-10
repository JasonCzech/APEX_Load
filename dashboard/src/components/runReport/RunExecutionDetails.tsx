import { Area, CartesianGrid, ComposedChart, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import type { MetricsHistoryPoint, RunArchiveReport, TestRunManifest, TestSummary } from '../../types'
import type { useExecutionTestManifest } from '../useExecutionTestManifest'
import { chartTooltipContentStyle, chartTooltipLabelStyle } from '../chartTooltipTheme'
import { executionDate, executionFacts, executionLoadPoints, recordedNumber } from './executionPresentation'

export function RunExecutionFacts({ test, report, manifest }: { test: TestSummary; report: RunArchiveReport | null; manifest?: TestRunManifest | null }) {
  const facts = executionFacts(test, report, manifest)
  return <dl className="run-execution-facts" aria-label="Execution facts">
    <div><dt>Configured duration</dt><dd>{facts.duration || 'Not recorded'}</dd></div>
    <div><dt>Observed elapsed</dt><dd>{facts.elapsed}</dd></div>
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
  const events = manifest?.runtime_changes || []
  return <>
    {!evidenceOnly && <>
      <p className="run-report-muted">Read-only execution record. {facts.immutable ? 'Original configuration is preserved.' : 'Configured values come from a mutable retained record; the original plan is unverified.'} Execution completion and acceptance are assessed separately.</p>
      <RunExecutionFacts test={test} report={report} manifest={manifest} />
      <section className="run-report-panel" aria-label="Execution load timeline">
        <h2>Configured and observed load</h2>
        <p>Configured load: {recordedNumber(facts.vusers)} VUsers. This reference is not a ramp schedule. Observed samples cover only the captured intervals.</p>
        {historyLoading ? <p role="status">Loading captured load history…</p> : historyError ? <p role="alert">{historyError}</p> : points.length > 0 ? <>
          <div className="run-execution-chart" aria-label="Captured VUsers with configured load reference">
            <ResponsiveContainer width="100%" height={220}>
              <ComposedChart data={points} margin={{ top: 16, right: 24, left: 0, bottom: 12 }}>
                <CartesianGrid stroke="var(--border)" strokeDasharray="3 3" />
                <XAxis dataKey="timestampMs" type="number" scale="time" domain={['dataMin', 'dataMax']} tickFormatter={value => new Date(value).toLocaleTimeString()} tick={{ fill: 'var(--text-secondary)', fontSize: 12 }} minTickGap={48} />
                <YAxis domain={[0, 'auto']} tick={{ fill: 'var(--text-secondary)', fontSize: 12 }} />
                <Tooltip contentStyle={chartTooltipContentStyle} labelStyle={chartTooltipLabelStyle} labelFormatter={value => executionDate(new Date(Number(value)).toISOString())} />
                {facts.vusers !== undefined && Number.isFinite(facts.vusers) && facts.vusers >= 0 && <ReferenceLine y={facts.vusers} stroke="var(--text-secondary)" strokeDasharray="5 5" ifOverflow="extendDomain" />}
                <Area dataKey="vusers" name="Observed VUsers" stroke="var(--accent)" fill="var(--accent)" fillOpacity={0.12} isAnimationActive={false} />
              </ComposedChart>
            </ResponsiveContainer>
          </div>
          <p className="run-report-muted">Solid curve: observed VUsers.{facts.vusers !== undefined && Number.isFinite(facts.vusers) && facts.vusers >= 0 ? ' Dashed line: configured VUsers.' : ' Configured reference unavailable.'}</p>
        </> : <p>No interval history was retained. Planned workload completion cannot be inferred from the final controller sample.</p>}
      </section>
      <section className="run-report-panel" aria-label="Execution groups">
        <h2>Groups and configuration</h2>
        <p>{facts.immutable ? 'Configuration preserved in the immutable run manifest.' : 'Configuration from the retained test record. It may reflect later changes; the original plan is unverified.'}</p>
        {facts.groups.length > 0 ? <div className="run-report-table-wrap"><table className="run-report-table run-execution-groups">
          <thead><tr><th scope="col">Group</th><th scope="col">Script</th><th scope="col">Configured VUsers</th><th scope="col">Configuration details</th></tr></thead>
          <tbody>{facts.groups.map((group, index) => <tr key={`${group.name}-${index}`}>
            <th scope="row">{group.name || 'Unnamed group'}</th><td>{group.script_name || group.script_id || 'Not recorded'}</td><td>{recordedNumber(group.vusers)}</td>
            <td><details><summary>View configuration</summary><dl>
              <dt>Protocol</dt><dd>{manifest?.scripts?.find(script => script.group_name === group.name)?.protocol || 'Not recorded'}</dd>
              <dt>Ramp up</dt><dd>{group.ramp_up || 'Not recorded'}</dd>
              <dt>Initialization</dt><dd>{group.initialization || 'Not recorded'}</dd>
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
    <details className="run-execution-records"><summary>Configuration and infrastructure evidence</summary>
      <p>{manifest?.source === 'archive' ? 'Archived run manifest.' : 'Retained controller record.'} Document generated: {executionDate(manifest?.generated_at)}. Generation time is not the time the run was captured.</p>
      {manifest?.warnings?.map(warning => <p key={warning}>{warning}</p>)}
      <p>{facts.immutable ? 'Initial configuration is immutable.' : 'Initial configuration is not verified as immutable.'}</p>
      {manifest ? <details><summary>Raw manifest and captured infrastructure</summary><pre>{JSON.stringify(manifest, null, 2)}</pre></details> : <p>Manifest evidence is unavailable.</p>}
    </details>
  </>
}
