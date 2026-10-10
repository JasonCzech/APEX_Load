import type { RcaAnalysisResponse } from '../../types/rca'

// browserBaselineAbsence says why the area shows no baseline comparison, or
// '' when the analysis holds one.
export function browserBaselineAbsence({ response, loading, error }: { response: RcaAnalysisResponse | null; loading: boolean; error: string }): string {
  if (response?.assessment?.browser_baseline) return ''
  if (loading && !response) return 'Loading the run\'s root-cause analysis…'
  if (error && !response) return 'The run\'s root-cause analysis could not be read.'
  if (response?.state === 'queued' || response?.state === 'running') return 'A root-cause analysis is running; its browser baseline comparison appears here when it completes.'
  if (response?.state === 'completed') return 'The latest root-cause analysis made no browser baseline comparison: it found no browser documents to compare, or stopped before comparing them.'
  if (response?.state === 'failed' || response?.state === 'cancelled') return 'The latest root-cause analysis did not complete, so it made no browser baseline comparison.'
  return response?.analysis_enabled === false
    ? 'The baseline comparison of browser documents is part of root-cause analysis, which has not been run for this run; new analyses are not enabled for this project.'
    : 'The baseline comparison of browser documents (anomalies, qualification and exclusions) is part of root-cause analysis, which has not been run for this run.'
}
