import type { BrowserResourceEvidenceV2 } from './browserResourceEvidence'

export type BrowserResourcePrecision = 'rfc3339_nanosecond' | 'unix_millisecond' | 'unavailable'
export interface BrowserResourcePageItem {
  locator: { transaction_ordinal: number; resource_ordinal: number; branch: 'network_entries' | 'snapshot_network_entries' }
  timestamp?: string
  timestamp_precision: BrowserResourcePrecision
  action_name?: string
  group?: string
  region?: string
  page_url?: string
  url?: string
  method?: string
  status_code?: number
  resource_type?: string
  failure_kind?: 'http' | 'transport' | 'timeout' | 'canceled' | 'body_limit' | ''
  duration_ms?: number
  bytes_received?: number
  device_profile?: string
  device_platform?: string
  browser_engine?: string
  evidence_state: 'available' | 'not_captured' | 'malformed' | 'unsupported_version'
  browser_evidence?: BrowserResourceEvidenceV2
}
export interface BrowserResourcePage {
  version: 1
  test_id: string
  project_id: string
  source_format: 'jsonl' | 'parquet'
  source_revision: string
  source_integrity: 'verified_manifest_and_artifact' | 'legacy_scan_fingerprint'
  source_timestamp_precision: BrowserResourcePrecision | 'mixed'
  population: {
    availability: 'retained_observations' | 'unavailable'
    transaction_records: number
    retained_entry_observations: number | null
    matched_retained_observations: number | null
  }
  capture_coverage: { availability: 'unavailable'; reason: 'retained_observations_are_not_capture_coverage' }
  items: BrowserResourcePageItem[]
  next_cursor?: string
}
export interface BrowserResourcePageQuery {
  limit: 25 | 50 | 100
  sort_by: 'capture_order' | 'duration_ms' | 'native_start_ms' | 'response_encoded_body'
  sort_dir: 'asc' | 'desc'
  action_name?: string
  group?: string
  region?: string
  resource_type?: string
  failure_kind?: Exclude<BrowserResourcePageItem['failure_kind'], ''>
  state?: 'finished' | 'failed' | 'censored'
  source_generation?: string
  request_id?: string
  redirect_chain_id?: string
  document_id?: string
  initiating_action_id?: string
  observation_action_id?: string
  origin?: string
  browser_engine?: string
  device_profile?: string
  cursor?: string
}
export const DEFAULT_BROWSER_RESOURCE_QUERY: BrowserResourcePageQuery = { limit: 50, sort_by: 'capture_order', sort_dir: 'asc' }
