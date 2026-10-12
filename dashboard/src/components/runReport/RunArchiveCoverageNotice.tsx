import type { RunArchiveReport } from '../../types'

export default function RunArchiveCoverageNotice({ report }: { report: RunArchiveReport | null }) {
  const findings = report?.completeness?.findings?.filter(finding => finding.dimension === 'generator_sources') || []
  if (!findings.length) return null
  const sources = report?.manifest?.source_coverage?.sources || []
  return <section className="run-report-panel run-archive-coverage" aria-label="Archive source coverage">
    <h2>Archive evidence needs review</h2>
    {findings.map((finding, index) => <p key={index}>{finding.message}</p>)}
    {sources.length > 0 && <details>
      <summary>Inspect generator sources ({sources.length})</summary>
      <p>These sources were known at collection time. Collection does not establish complete shard history or prove that all measurements survived.</p>
      <div className="run-archive-coverage-table"><table className="tx-table">
        <thead><tr><th scope="col">Generator</th><th scope="col">Region</th><th scope="col">Groups</th><th scope="col">Archive collection</th></tr></thead>
        <tbody>{sources.map((source, index) => <tr key={index}>
          <td>{source.generator_id ? <>{source.generator_id}<br /><small>{source.target}</small></> : source.target || 'Not recorded'}</td>
          <td>{source.region || 'Not recorded'}</td>
          <td>{source.groups?.length ? source.groups.join(', ') : 'Not recorded'}</td>
          <td>{source.collected ? 'Collected' : 'Not collected'}</td>
        </tr>)}</tbody>
      </table></div>
    </details>}
  </section>
}
