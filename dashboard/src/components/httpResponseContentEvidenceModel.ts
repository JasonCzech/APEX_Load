import type { ProtocolEvidence } from '../types/replay'

export function httpResponseContentEvidence(evidence?: ProtocolEvidence, bodyState?: unknown) {
  const state = evidence?.response_content_state
  const basis = evidence?.response_content_basis
  const validBasis = basis === 'decoded' || basis === 'identity'
  const sourcePresent = evidence !== undefined && evidence !== null && Object.prototype.hasOwnProperty.call(evidence, 'response_content_source')
  const source = evidence?.response_content_source
  const validSource = !sourcePresent || source === 'wire' || source === 'cache'
  const cached = source === 'cache' || (!sourcePresent && evidence?.http_version_source === 'cache')
  if (state === 'complete' && validBasis && validSource && bodyState !== 'partial') return {
    state: 'Complete response',
    basis: cached ? 'Stored response content (cached)' : basis === 'decoded' ? 'Decoded response content' : 'Identity response content',
    qualification: cached ? 'Complete cached content was admitted. The original wire representation was not observed in this replay.' : 'Complete response content was admitted after the full response was read.',
  }
  if (validSource && state !== 'unavailable' && (state === 'collection_window' || bodyState === 'partial')) return {
    state: state === 'collection_window' ? 'Collection window (partial response)' : 'Partial captured response',
    basis: validBasis ? basis === 'decoded' ? 'Decoded response content' : 'Identity response content' : 'Not established',
    qualification: 'This is sampled stream content. Full-response completion was not established, so it cannot supply a complete correlation response.',
  }
  if (state === undefined && basis === undefined && !sourcePresent) return null
  return {
    state: 'Response content unavailable',
    basis: 'Not established',
    qualification: 'No complete response content was admitted. Partial decoded bytes are not a usable response.',
  }
}
