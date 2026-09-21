import type { RunArchiveReport } from '../types'

export default function CaptureArchiveSummary({ report }: { report: RunArchiveReport }) {
  return <section aria-label="Archived monitoring capture"><h3>Monitoring capture evidence</h3>
    <p>Samples are bounded, deduplicated, and redacted—not a complete log export. Empty evidence does not prove that no errors occurred.</p>
    {(['elk', 'dynatrace'] as const).map(provider => {
      const label = provider === 'elk' ? 'Elastic' : 'Dynatrace'
      const selected = report.manifest.provenance?.[`${provider}_capture`]
      const samples = provider === 'elk' ? report.elk_samples : report.dynatrace_evidence
      const artifacts = report.manifest.artifacts?.filter(a => a.name.includes(provider)) || []
      const unavailable = report.integrity?.artifacts?.some(a => a.name.includes(provider) && ['missing', 'mismatch', 'unverifiable'].includes(a.status))
      const state = unavailable ? 'Evidence unavailable or integrity check failed' : samples?.length ? `${samples.length} retained evidence records` : selected ? 'Capture selected; no evidence records returned in this report' : 'Capture not recorded for this run (disabled or older archive)'
      return <p key={provider}><strong>{label}:</strong> {state}.{artifacts.length > 0 && ` ${artifacts.length} capture artifact(s) listed in the manifest.`}</p>
    })}
  </section>
}
