import type { LiveObservationState } from '../utils/liveObservation'

export default function LiveObservationNotice({ state }: { state: LiveObservationState }) {
  if (state === 'current') return null
  return <p role="status" aria-label="Load observation status">
    {state === 'historical'
      ? 'Run ended. Values below describe the last available sample; they do not indicate currently running VUsers.'
      : state === 'stale'
        ? 'Telemetry is stale or incomplete. Values below are the last observations; current load and recovery are unconfirmed.'
        : 'Awaiting telemetry. No current load observation is available.'}
  </p>
}
