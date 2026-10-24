import { request, requestWithResponse } from '../apiClient'
import type { TelemetryPolicySegmentReceipt, TelemetryPolicySegmentRequest } from '../../types/telemetryPolicy'

// TEL-B: a running test's telemetry budget policy segments. A change appends
// the next segment; a retry with the same request_id returns it. Paths are
// written out in each call, so the OpenAPI binding check can resolve them.

// Bounded, so a request that never settles cannot hold a run's change (a
// timeout is not an answer: the change is retried under its request_id).
const segmentsTimeoutMs = 30_000

export const telemetryPolicySegmentsApi = {
  list: (testId: string, signal?: AbortSignal) => request<{ segments?: unknown }>(`/tests/${encodeURIComponent(testId)}/telemetry-policy-segments`, { signal, timeoutMs: segmentsTimeoutMs }),
  /** replayed: the request_id had already made the change (200, not 201). */
  append: async (testId: string, body: TelemetryPolicySegmentRequest): Promise<{ segment: TelemetryPolicySegmentReceipt; replayed: boolean }> => {
    const response = await requestWithResponse<TelemetryPolicySegmentReceipt>(`/tests/${encodeURIComponent(testId)}/telemetry-policy-segments`, { method: 'POST', body: JSON.stringify(body), timeoutMs: segmentsTimeoutMs })
    return { segment: response.body, replayed: response.status === 200 }
  },
}
