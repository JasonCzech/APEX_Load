import type { RunArchiveReport } from '../types'

export function runOutcomeLabels(manifest?: RunArchiveReport['manifest']) {
  const execution: Record<string, string> = { workload_completed: 'Finished', operator_stopped: 'Stopped by operator', stopped_early: 'Stopped early', aborted: 'Aborted', failed: 'Failed' }
  const start = manifest?.provenance?.started_at
  const elapsed = start && manifest?.completed_at ? (Date.parse(manifest.completed_at) - Date.parse(start)) / 1000 : undefined
  return {
    execution: execution[manifest?.termination_reason || ''] || 'Unknown termination reason',
    elapsed: elapsed !== undefined && Number.isFinite(elapsed) && elapsed >= 0 ? `${Math.round(elapsed)}s` : 'Unknown',
  }
}
