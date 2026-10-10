/** Optional v2 native request evidence. Each field keeps its own availability;
 * request clocks are not proof of cross-source clock synchronization. */
export interface BrowserResourcePhase {
  name: 'dns' | 'connect' | 'tls' | 'request_wait' | 'response'
  start_ms?: number | null
  end_ms?: number | null
  unavailable_reason?: string
}
export interface BrowserResourceBytes {
  request_headers?: number | null
  request_body?: number | null
  response_headers?: number | null
  response_encoded_body?: number | null
  unavailable_reason?: string
}
/** Version 2 records carry no association fields; version 3 adds them;
 * version 4 adds the negotiated protocol. */
export interface BrowserResourceEvidenceV2 {
  version: 2 | 3 | 4
  source_generation: string
  request_id: string
  frame_id?: string
  document_id?: string
  navigation_id?: string
  redirect_chain_id?: string
  redirect_from_request_id?: string
  redirect_hop: number
  initiating_action_id?: string
  observation_action_id?: string
  origin?: string
  identity_unavailable_reason?: string
  epoch_start_ms?: number | null
  document_offset_ms?: number | null
  observed_at: string
  timing_source: 'playwright'
  phases: BrowserResourcePhase[] | null
  bytes: BrowserResourceBytes
  service_worker?: boolean | null
  service_worker_unavailable_reason?: string
  cache?: boolean | null
  cache_unavailable_reason?: string
  initiator_unavailable_reason?: string
  state: 'finished' | 'failed' | 'censored'
  /** How document_id was proven: the issuing Chromium loader, or a unique
   * Resource Timing entry inside one document. */
  document_association?: 'cdp_loader_join' | 'page_resource_timing_join'
  initiator_type?: 'parser' | 'script' | 'preload' | 'preflight' | 'signed_exchange' | 'other'
  /** Scheme and host only; never a path, query or stack. */
  initiator_origin?: string
  /** Version 4: the negotiated application protocol of the network exchange.
   * Absent with a reason when the request was cached, served by a service
   * worker, used a draft protocol, or the browser did not expose it. */
  protocol?: 'h1' | 'h2' | 'h3'
  protocol_unavailable_reason?: string
}
