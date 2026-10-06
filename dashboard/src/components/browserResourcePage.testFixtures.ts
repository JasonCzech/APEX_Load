import type { BrowserResourcePage } from '../types/browserResourcePage'
export const RESOURCE_GENERATION = '0123456789abcdef0123456789abcdef'
export function resourcePageFixture(): BrowserResourcePage {
  return { version: 1, test_id: 'run-one', project_id: 'project-one', source_format: 'jsonl', source_revision: 'a'.repeat(64),
    source_integrity: 'verified_manifest_and_artifact', source_timestamp_precision: 'rfc3339_nanosecond',
    population: { availability: 'retained_observations', transaction_records: 2, retained_entry_observations: 80, matched_retained_observations: 80 },
    capture_coverage: { availability: 'unavailable', reason: 'retained_observations_are_not_capture_coverage' },
    items: [{ locator: { transaction_ordinal: 1, resource_ordinal: 1, branch: 'network_entries' }, timestamp: '2026-10-05T12:00:00.123456789Z', timestamp_precision: 'rfc3339_nanosecond',
      url: '/resource-zero', action_name: 'Navigate', status_code: 0, duration_ms: 0, bytes_received: 0, evidence_state: 'available',
      browser_evidence: { version: 2, source_generation: RESOURCE_GENERATION, request_id: `${RESOURCE_GENERATION}-request-1`, redirect_hop: 0,
        observed_at: '2026-10-05T12:00:00.123456789Z', timing_source: 'playwright', state: 'finished', epoch_start_ms: 0, phases: [
          { name: 'dns', start_ms: 0, end_ms: 0 }, { name: 'connect', start_ms: 0, end_ms: 8 }, { name: 'tls', start_ms: 2, end_ms: 6 },
          { name: 'request_wait', unavailable_reason: 'not_exposed' }], bytes: { response_encoded_body: 0 }, service_worker: false } }],
    next_cursor: `payload.${'A'.repeat(43)}` }
}
