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
      {shards.length > 0 && <div className="advanced-group-table-wrap"><table className="advanced-group-table"><thead><tr><th>Group</th><th>Target</th><th>VUsers</th><th>Strategy</th></tr></thead><tbody>
        {shards.map((shard, index) => <tr key={`${shard.group}-${shard.target}-${index}`}><th scope="row">{shard.group}</th><td>{shard.target}</td><td>{shard.vusers}</td><td>{shard.shard_strategy || 'Not recorded'}</td></tr>)}
      </tbody></table></div>}
    </section>
  )
}
