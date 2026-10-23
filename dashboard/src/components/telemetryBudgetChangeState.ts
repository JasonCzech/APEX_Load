import type { TelemetryPolicySegmentRequest } from '../types/telemetryPolicy'

// TEL-B: the telemetry budget change control's state that outlives a control.

// A change sent whose answer is unknown (lost), by run: it must be retried
// under the same submission, so it outlives the control (a remount when the
// run's live metrics drop out) and a review of any other change meanwhile.
// An answer (a success or a refusal) or another change sent ends it, as does
// a later change than it could have made.
export const unansweredChanges = new Map<string, TelemetryPolicySegmentRequest>()
// The runs with a change being sent, so a remounted control cannot send
// another meanwhile.
export const sendingChanges = new Set<string>()
