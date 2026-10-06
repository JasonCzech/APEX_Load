import type { BrowserResourceEvidenceV2 } from '../types/browserResourceEvidence'
import { browserResourcePhaseRows } from './browserResourceEvidenceModel'

const measured = (value: number | null | undefined, suffix: string) => value === undefined || value === null ? 'Unavailable' : `${value.toLocaleString('en-US', { maximumFractionDigits: 6 })}${suffix}`
const observedBoolean = (value: boolean | null | undefined, reason?: string) => value === undefined || value === null ? `Unavailable${reason ? `: ${reason}` : ''}` : `${value} (observed)`

const associationLabels: Record<string, string> = {
  cdp_loader_join: 'Chromium loader of the issuing document',
  page_resource_timing_join: 'Unique Resource Timing entry in the document',
}

export default function BrowserNativeRequestEvidence({ evidence, onSelectDocument }: { evidence: BrowserResourceEvidenceV2; onSelectDocument?: (document: string) => void }) {
  const rows = browserResourcePhaseRows(evidence)
  return <div className="browser-native-request-evidence">
    <h4>Native request evidence</h4>
    <p>Request-local offsets can overlap. Clock alignment, server processing and whole-page capture coverage are unverified.</p>
    <dl className="browser-resource-details">
      <dt>Source generation</dt><dd>{evidence.source_generation}</dd>
      <dt>Request identity</dt><dd>{evidence.request_id}</dd>
      <dt>Frame identity</dt><dd>{evidence.frame_id || 'Unavailable'}</dd>
      <dt>Document identity</dt><dd>{evidence.document_id
        ? <>{evidence.document_id}{onSelectDocument && <> <button type="button" className="btn btn-secondary btn-sm" onClick={() => onSelectDocument(evidence.document_id as string)}>Show this document's resources</button></>}</>
        : `Unavailable${evidence.identity_unavailable_reason ? `: ${evidence.identity_unavailable_reason}` : ''}`}</dd>
      <dt>Document association</dt><dd>{evidence.document_association ? associationLabels[evidence.document_association] : 'Unavailable'}</dd>
      <dt>Navigation identity</dt><dd>{evidence.navigation_id || 'Unavailable'}</dd>
      <dt>Redirect chain / hop</dt><dd>{evidence.redirect_chain_id || 'Unavailable'} / {evidence.redirect_hop}</dd>
      <dt>Redirected from</dt><dd>{evidence.redirect_from_request_id || 'Unavailable'}</dd>
      <dt>Initiating action identity</dt><dd>{evidence.initiating_action_id || 'Unavailable'}</dd>
      <dt>Observation action identity</dt><dd>{evidence.observation_action_id || 'Unavailable'}</dd>
      <dt>Origin</dt><dd>{evidence.origin || 'Unavailable'}</dd>
      <dt>Observed at (source UTC)</dt><dd>{evidence.observed_at}</dd>
      <dt>Native epoch start</dt><dd>{measured(evidence.epoch_start_ms, ' ms')} · clock unverified</dd>
      <dt>Document-relative offset</dt><dd>{measured(evidence.document_offset_ms, ' ms')}</dd>
      <dt>Request state / timing source</dt><dd>{evidence.state} / {evidence.timing_source}</dd>
      <dt>Service worker</dt><dd>{observedBoolean(evidence.service_worker, evidence.service_worker_unavailable_reason)}</dd>
      <dt>Cache</dt><dd>{observedBoolean(evidence.cache, evidence.cache_unavailable_reason)}</dd>
      <dt>Initiator provenance</dt><dd>{evidence.initiator_type
        ? `${evidence.initiator_type}${evidence.initiator_origin ? ` from ${evidence.initiator_origin}` : ''}`
        : evidence.initiator_unavailable_reason ? `Unavailable: ${evidence.initiator_unavailable_reason}` : 'Unavailable'}</dd>
    </dl>
    <h4>Request-local phase offsets (may overlap)</h4>
    {rows.length ? <ul className="browser-resource-phases">{rows.map(phase => <li key={phase.name}>
      <span>{phase.name.replace('_', ' ')}</span>
      <span className="browser-resource-phase-track" aria-hidden="true">{phase.start_percent !== undefined && phase.end_percent !== undefined &&
        <span className={`browser-resource-phase-bar${phase.start_percent === phase.end_percent ? ' measured-point' : ''}`} style={{ left: `${phase.start_percent}%`, width: `${phase.end_percent - phase.start_percent}%` }} />}</span>
      <span>{measured(phase.start_ms, ' ms')} → {measured(phase.end_ms, ' ms')}{phase.unavailable_reason ? ` · ${phase.unavailable_reason}` : ''}</span>
    </li>)}</ul> : <p>No native phase endpoints were retained.</p>}
    <h4>Native byte measurements</h4>
    <dl className="browser-resource-details">
      <dt>Request headers</dt><dd>{measured(evidence.bytes.request_headers, ' B')}</dd>
      <dt>Request body</dt><dd>{measured(evidence.bytes.request_body, ' B')}</dd>
      <dt>Response headers</dt><dd>{measured(evidence.bytes.response_headers, ' B')}</dd>
      <dt>Encoded response body</dt><dd>{measured(evidence.bytes.response_encoded_body, ' B')}</dd>
      {evidence.bytes.unavailable_reason && <><dt>Byte availability</dt><dd>{evidence.bytes.unavailable_reason}</dd></>}
    </dl>
  </div>
}
