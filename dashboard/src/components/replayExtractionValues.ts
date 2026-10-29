import type { BrowserReplayEvent } from '../types/replay'

// Children are executed in order, followed by the parent result. Diagnostic
// values that were not admitted must never enter reconstructed variables.
export function appliedReplayExtractions(event: BrowserReplayEvent): Record<string, string> {
  const values: Record<string, string> = Object.create(null)
  for (const child of event.streamed_children || []) Object.assign(values, child.extractions || {})
  Object.assign(values, event.extractions || {})
  return values
}
