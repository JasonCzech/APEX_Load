import type { RunArchiveReport } from '../types'

/** Evidence qualifications are independent of whether recorded requests had errors. */
export function reportEvidenceWarnings(report: RunArchiveReport): string[] {
  const warnings = (report.completeness?.findings || [])
    .filter(finding => ['critical', 'high', 'warning'].includes(finding.severity.toLowerCase()))
    .map(finding => finding.message)
  if (!report.manifest?.termination_reason || !report.manifest?.provenance?.started_at || !report.manifest?.completed_at) {
    warnings.push('Run timing or termination evidence is incomplete; completion of the planned workload is unverified.')
  }
  const planned = report.manifest?.provenance?.total_vusers
  if (planned && (report.overview?.peak_active_vusers ?? planned) < planned) {
    warnings.push(`Observed peak load was ${report.overview.peak_active_vusers} of ${planned} planned VUsers. Review workload coverage before accepting performance results.`)
  }
  if (report.overview?.transactions !== undefined && report.overview.transactions < 100) {
    warnings.push(`Only ${report.overview.transactions} archive records were captured. This small sample does not establish representative performance.`)
  }
  // A run its load generators or telemetry could not carry is not
  // acceptance evidence, whatever its numbers say.
  const validity = report.validity
  if (validity && (validity.verdict === 'suspect' || validity.verdict === 'invalid')) {
    const reasons = (validity.reasons || []).slice(0, 3)
    warnings.push(`Run validity is ${validity.verdict}${reasons.length ? `: ${reasons.join('; ')}` : ''}. Symptoms may come from the load generators rather than the system under test.`)
  }
  return [...new Set(warnings)]
}
