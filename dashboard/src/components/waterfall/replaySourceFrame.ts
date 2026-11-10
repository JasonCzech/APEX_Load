import type { BrowserReplayEvent, ReplayRuntimeSnapshot } from '../../types/replay'

export type ReplaySourceFrame = ReplayRuntimeSnapshot['frames'][number]

/** The script location of an execution, as a runtime frame the editor can reveal. */
export function replayFrameForEvent(event: BrowserReplayEvent): ReplaySourceFrame | null {
  if (event.phase !== 'init' && event.phase !== 'action' && event.phase !== 'teardown') return null
  // The replay API omits a zero action_index, like every other event-to-script mapper assumes.
  const actionIndex = event.action_index ?? 0
  if (typeof actionIndex !== 'number' || actionIndex < 0) return null
  const stepPath = event.step_path?.length ? [...event.step_path] : event.request_index != null ? [`requests[${event.request_index}]`] : undefined
  return { kind: 'phase', phase: event.phase, action_index: actionIndex, step_path: stepPath, iteration_path: event.iteration_path }
}
