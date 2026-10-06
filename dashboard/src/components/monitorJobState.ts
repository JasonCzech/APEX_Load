import type { RunMonitorJob } from '../types/runMonitoring'
export function monitorJobState(job: RunMonitorJob): string {
  if (job.state === 'draining' || job.state === 'reconciling') return `Load finished; collecting delayed ${job.source.pack === 'dynatrace' ? 'Dynatrace' : 'AppDynamics'} data`
  if (job.state === 'complete' || job.state === 'stopped') return 'Collection finished'
  if (job.state === 'incomplete') return 'Collection incomplete'
  return job.state
}
