import type { RunArchiveReport, TestSummary } from '../types'
import { parseDurationToSeconds } from '../utils/timeUtils'
import { reportPopulationOverview, type MetricPopulation } from './runReport/reportPresentation'

/** Missing metadata is unknown, never proof that two executions match. */
export function reportComparisonQualification(current: RunArchiveReport, baseline: RunArchiveReport, test: TestSummary, baselineTest: TestSummary | null, population: MetricPopulation) {
  const a = current.manifest?.provenance
  const b = baseline.manifest?.provenance
  const fields: Array<[string, unknown, unknown]> = [
    ['Scenario', a?.scenario_id || test.scenario_id, b?.scenario_id || baselineTest?.scenario_id],
    ['Project', test.project_id, baselineTest?.project_id],
    ['Application', test.application_id, baselineTest?.application_id],
    ['Planned VUsers', a?.total_vusers ?? test.total_vusers, b?.total_vusers ?? baselineTest?.total_vusers],
    ['Planned duration', parseDurationToSeconds(a?.duration || test.duration) || undefined, parseDurationToSeconds(b?.duration || baselineTest?.duration) || undefined],
  ]
  const missing = fields.filter(([, left, right]) => left === undefined || left === null || left === '' || right === undefined || right === null || right === '')
  const differences = fields.filter(([, left, right]) => left !== undefined && left !== null && left !== '' && right !== undefined && right !== null && right !== '' && left !== right)
  const metrics = reportPopulationOverview(current, population)
  const baselineMetrics = reportPopulationOverview(baseline, population)
  const currentRevisions = current.change_attribution
  const baselineRevisions = baseline.change_attribution
  const revisionsKnown = Boolean(currentRevisions?.scenario_revision && baselineRevisions?.scenario_revision && currentRevisions?.script_revisions?.length && baselineRevisions?.script_revisions?.length)
  const revisionsMatch = revisionsKnown && currentRevisions?.scenario_revision === baselineRevisions?.scenario_revision
    && JSON.stringify([...currentRevisions!.script_revisions!].sort()) === JSON.stringify([...baselineRevisions!.script_revisions!].sort())
  const currentLoad = current.overview?.peak_active_vusers
  const baselineLoad = baseline.overview?.peak_active_vusers
  const observedLoadKnown = typeof currentLoad === 'number' && Number.isFinite(currentLoad) && currentLoad >= 0
    && typeof baselineLoad === 'number' && Number.isFinite(baselineLoad) && baselineLoad >= 0
  return {
    revisions: !revisionsKnown ? 'Script and scenario revision equivalence is unverified.' : revisionsMatch ? 'Recorded script and scenario revisions match.' : 'Recorded script or scenario revisions differ.',
    summary: differences.length ? `Different planned context: ${differences.map(([name]) => name.toLowerCase()).join(', ')}.` : missing.length ? 'Planned context cannot be fully verified.' : 'Recorded planned context matches.',
    missing: missing.map(([name]) => name),
    currentRecords: current.metric_populations?.[population]?.transactions,
    baselineRecords: baseline.metric_populations?.[population]?.transactions,
    limitedSamples: !metrics || !baselineMetrics || metrics.transactions < 100 || baselineMetrics.transactions < 100,
    observedLoadUnknown: !observedLoadKnown,
    differentObservedLoad: observedLoadKnown && currentLoad !== baselineLoad,
  }
}
