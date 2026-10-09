import type { TelemetryStageDistribution } from '../types'

// STAGE_ADOPTION_SLACK_MS is how long after the run's start sampling may
// begin before the panel says the distributions cover only part of the run.
export const STAGE_ADOPTION_SLACK_MS = 60_000

// stageCoverageNote describes distributions this controller began sampling
// well after the run started (it adopted the run after a failover or
// restart), or ones it never sampled (also a run that ended before
// publishing anything).
export function stageCoverageNote(stages: TelemetryStageDistribution[], startedAt?: string): string {
  if (stages.some(stage => stage.reason === 'recorder_lost')) {
    return 'This controller holds no stage samples for the run (it restarted or took over after the run, or the run ended before publishing any); stage distributions are unavailable.'
  }
  const from = Date.parse(stages.find(stage => stage.sampled_from)?.sampled_from || '')
  const started = Date.parse(startedAt || '')
  if (!Number.isFinite(from) || !Number.isFinite(started) || from - started <= STAGE_ADOPTION_SLACK_MS) return ''
  return `Sampled from ${new Date(from).toLocaleTimeString()}: this controller took over the run then, so earlier publications are not included.`
}
