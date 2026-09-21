import { TestRunManifest, TestSummary } from '../types'

interface Props {
  manifest: TestRunManifest | null
  manifestError: string
  manifestLoading: boolean
  test: TestSummary
}

function telemetryHealth(test: TestSummary): string {
  const telemetry = test.live_metrics?.telemetry
  if (!telemetry) return 'No telemetry observation yet'
  const age = telemetry.end_to_end_age_ms ?? telemetry.sample_age_ms
  if (age === undefined) return 'Telemetry received'
  return age > 2000 ? `Telemetry is stale (${Math.round(age)} ms)` : `Telemetry healthy (${Math.round(age)} ms)`
}

export default function TestExecutionInfrastructure({ manifest, manifestError, manifestLoading, test }: Props) {
  const shards = manifest?.load_generators.generator_shards || []
  const elk = test.live_metrics?.elk
  const dynatrace = test.live_metrics?.dynatrace
  return (
    <section className="advanced-infrastructure" aria-label="Infrastructure evidence">
      <header className="advanced-section-header">
        <div>
          <h3>Infrastructure evidence</h3>
          <p>Telemetry and generator assignment recorded for this run.</p>
        </div>
      </header>
      <dl className="advanced-fact-grid">
        <div><dt>Telemetry health</dt><dd>{telemetryHealth(test)}</dd></div>
        <div><dt>Assignment mode</dt><dd>{manifest?.load_generators.assignment?.mode || 'Not recorded'}</dd></div>
        <div><dt>Generator shards</dt><dd>{manifestLoading ? 'Loading…' : shards.length || 'None recorded'}</dd></div>
        <div><dt>Selected targets</dt><dd>{manifest?.load_generators.assignment?.selected_targets?.join(', ') || 'Not recorded'}</dd></div>
      </dl>
      {manifestError && <p className="advanced-inline-warning" role="status">Infrastructure record unavailable: {manifestError}</p>}
      {elk && <section aria-label="Elastic capture">
        <h4>Elastic capture · {elk.connection_name || elk.connection_id}</h4>
        <p>{elk.status}{elk.last_polled_at ? ` · refreshed ${new Date(elk.last_polled_at).toLocaleTimeString()}` : ''}{elk.message ? ` · ${elk.message}` : ''}</p>
        {elk.metrics && <dl className="advanced-fact-grid">{Object.entries(elk.metrics).map(([name, value]) => <div key={name}><dt>{name}</dt><dd>{value}</dd></div>)}</dl>}
        {elk.samples && elk.samples.length > 0 && <div className="advanced-group-table-wrap"><table className="advanced-group-table"><thead><tr><th>Time</th><th>Level</th><th>Message</th></tr></thead><tbody>{elk.samples.map((sample, index) => <tr key={sample.id || `${sample.timestamp}-${index}`}><td>{sample.timestamp ? new Date(sample.timestamp).toLocaleTimeString() : '—'}</td><td>{sample.level || '—'}</td><td>{sample.message || '—'}</td></tr>)}</tbody></table></div>}
      </section>}
      {dynatrace && <section aria-label="Dynatrace capture">
        <h4>Dynatrace capture · {dynatrace.connection_name || dynatrace.connection_id}</h4>
        <p>{dynatrace.status}{dynatrace.last_polled_at ? ` · refreshed ${new Date(dynatrace.last_polled_at).toLocaleTimeString()}` : ''}{dynatrace.message ? ` · ${dynatrace.message}` : ''}</p>
        {dynatrace.capabilities && <p>{Object.entries(dynatrace.capabilities).map(([name, health]) => `${name}: ${health.status}`).join(' · ')}</p>}
        {dynatrace.metrics && <dl className="advanced-fact-grid">{Object.entries(dynatrace.metrics).map(([name, value]) => <div key={name}><dt>{name}</dt><dd>{value}</dd></div>)}</dl>}
        {dynatrace.problems && dynatrace.problems.length > 0 && <div className="advanced-group-table-wrap"><table className="advanced-group-table"><thead><tr><th>Problem</th><th>Severity</th><th>Status</th><th>Entity</th></tr></thead><tbody>{dynatrace.problems.map((problem, index) => <tr key={problem.id || `${problem.title}-${index}`}><td>{problem.title || problem.display_id || '—'}</td><td>{problem.severity || '—'}</td><td>{problem.status || '—'}</td><td>{problem.affected_entity || '—'}</td></tr>)}</tbody></table></div>}
        {dynatrace.logs && dynatrace.logs.length > 0 && <div className="advanced-group-table-wrap"><table className="advanced-group-table"><thead><tr><th>Time</th><th>Status</th><th>Message</th></tr></thead><tbody>{dynatrace.logs.map((sample, index) => <tr key={`${sample.timestamp}-${sample.trace_id}-${index}`}><td>{sample.timestamp ? new Date(sample.timestamp).toLocaleTimeString() : '—'}</td><td>{sample.status || '—'}</td><td>{sample.message || '—'}</td></tr>)}</tbody></table></div>}
      </section>}
      {shards.length > 0 && <div className="advanced-group-table-wrap"><table className="advanced-group-table"><thead><tr><th>Group</th><th>Target</th><th>VUsers</th><th>Strategy</th></tr></thead><tbody>
        {shards.map((shard, index) => <tr key={`${shard.group}-${shard.target}-${index}`}><th scope="row">{shard.group}</th><td>{shard.target}</td><td>{shard.vusers}</td><td>{shard.shard_strategy || 'Not recorded'}</td></tr>)}
      </tbody></table></div>}
    </section>
  )
}
