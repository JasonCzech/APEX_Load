import { describe, expect, it } from 'vitest'
import type { BrowserResourceEvidenceV2 } from '../types/browserResourceEvidence'
import { browserDocumentWaterfall, browserResourceEvidenceView, browserResourcePhaseRows, browserResourceProtocolLabel, browserResourceRowKey } from './browserResourceEvidenceModel'

const generation = '0123456789abcdef0123456789abcdef'
function fixture(): BrowserResourceEvidenceV2 {
  return { version: 2, source_generation: generation, request_id: `${generation}-r1`, frame_id: `${generation}-f1`,
    redirect_chain_id: `${generation}-r1`, redirect_hop: 0, initiating_action_id: `${generation}-a1`, observation_action_id: `${generation}-a2`,
    observed_at: '2026-10-05T12:00:00.123456789Z', timing_source: 'playwright', epoch_start_ms: 0,
    origin: 'https://example.test:443', identity_unavailable_reason: 'document_unavailable',
    phases: [{ name: 'dns', start_ms: 0, end_ms: 0 }, { name: 'connect', start_ms: 0, end_ms: 30 },
      { name: 'tls', start_ms: 10, end_ms: 30 }, { name: 'request_wait', start_ms: 30, end_ms: null, unavailable_reason: 'not_exposed' }],
    bytes: { request_headers: 0, request_body: 0, response_headers: 42, response_encoded_body: 0 },
    service_worker: false, cache_unavailable_reason: 'not_exposed', initiator_unavailable_reason: 'not_exposed', state: 'finished' }
}
function change(field: string, value: unknown): unknown {
  return { ...fixture(), [field]: value }
}

describe('typed retained browser request evidence', () => {
  it('owns measured zero/false, partial endpoints and independent source timestamps', () => {
    const input = fixture()
    const view = browserResourceEvidenceView(input)
    expect(view.issue).toBeNull()
    expect(view.evidence).toEqual(input)
    expect(view.evidence?.epoch_start_ms).toBe(0)
    expect(view.evidence?.service_worker).toBe(false)
    expect(view.evidence?.bytes.response_encoded_body).toBe(0)
    expect(view.evidence?.phases?.[3]).toEqual({ name: 'request_wait', start_ms: 30, end_ms: null, unavailable_reason: 'not_exposed' })
    input.bytes.response_encoded_body = 1000
    input.phases![0].start_ms = 10
    expect(view.evidence?.bytes.response_encoded_body).toBe(0)
    expect(view.evidence?.phases?.[0].start_ms).toBe(0)
    expect(view.evidence?.document_id).toBeUndefined()
    expect(view.evidence?.document_offset_ms).toBeUndefined()
    expect(view.evidence?.cache).toBeUndefined()
  })
  it('keeps explicit null collections/measurements unavailable and accepts observed false cache', () => {
    const view = browserResourceEvidenceView(change('phases', null))
    expect(view.evidence?.phases).toBeNull()
    expect(browserResourceEvidenceView({ ...fixture(), epoch_start_ms: null, service_worker: null, cache: false,
      bytes: {}, document_offset_ms: -1 }).evidence).toMatchObject({ epoch_start_ms: null, service_worker: null, cache: false, bytes: {}, document_offset_ms: -1 })
  })
  it('treats missing or unsupported evidence as a gap without a native identity', () => {
    for (const input of [undefined, null, {}, { version: 3 }, { ...fixture(), version: 5 }]) {
      const view = browserResourceEvidenceView(input)
      expect(view.evidence).toBeNull()
      expect(view.issue).toBeTruthy()
    }
  })
  it.each(['version', 'source_generation', 'request_id', 'redirect_hop', 'observed_at', 'timing_source', 'phases', 'bytes', 'state'])('requires field presence: %s', field => {
    const input = fixture() as unknown as Record<string, unknown>
    delete input[field]
    expect(browserResourceEvidenceView(input).evidence).toBeNull()
  })
  it.each([
    ['version', 5], ['source_generation', generation.toUpperCase()], ['source_generation', 'a'.repeat(31)],
    ['request_id', `${'a'.repeat(32)}-r1`], ['request_id', `${generation}-secret\n`], ['request_id', `${generation}-${'a'.repeat(128)}`],
    ['request_id', `${generation}-bad\0id`],
    ['frame_id', null], ['document_id', `${'a'.repeat(32)}-d1`], ['redirect_hop', -1], ['redirect_hop', 0.5], ['redirect_hop', 65],
    ['observed_at', '2026-02-30T12:00:00Z'], ['observed_at', '2026-10-05T12:00:00+00:00'], ['observed_at', '0001-01-01T00:00:00.000000000Z'],
    ['state', ['finished']], ['state', 'pending'], ['timing_source', 'server'], ['epoch_start_ms', -1], ['epoch_start_ms', Infinity],
    ['epoch_start_ms', 253402300800000], ['document_offset_ms', -86400001], ['service_worker', 'false'], ['cache', 0],
    ['origin', 'https://user:secret@example.test'], ['origin', 'https://example.test/path'], ['origin', 'https://example.test?secret=value'],
    ['origin', 'https://example.test/#fragment'], ['identity_unavailable_reason', 'RAW-SECRET'], ['bytes', null], ['bytes', []],
    ['bytes', { request_body: -1 }], ['bytes', { response_encoded_body: 2 ** 50 + 1 }], ['bytes', { response_headers: 1.5 }],
    ['bytes', { unexpected: 'RAW-SECRET' }], ['phases', {}], ['phases', [{ name: 'dns', start_ms: 0 }]],
    ['phases', [{ name: 'dns', start_ms: 10, end_ms: 0 }]], ['phases', [{ name: 'dns', start_ms: 0, end_ms: 0, unavailable_reason: 'not_exposed' }]],
    ['phases', [{ name: 'dns', start_ms: NaN, end_ms: 0 }]], ['phases', [{ name: 'server_processing', start_ms: 0, end_ms: 0 }]],
    ['phases', [{ name: 'dns', start_ms: 0, end_ms: 0 }, { name: 'dns', start_ms: 0, end_ms: 0 }]],
    ['phases', [{ name: 'response', start_ms: 0, end_ms: 86400001 }]], ['unexpected', 'RAW-SECRET'],
  ])('refuses invalid %s=%j without disclosing supplied content', (field, value) => {
    const view = browserResourceEvidenceView(change(field as string, value))
    expect(view.evidence).toBeNull()
    expect(view.issue).toBe('Retained request evidence is malformed or unsupported.')
    expect(view.issue).not.toContain('RAW-SECRET')
  })
  it('retains nonzero source nanoseconds at year one and bounded Unicode origin text', () => {
    expect(browserResourceEvidenceView(change('observed_at', '0001-01-01T00:00:00.000000001Z')).evidence).not.toBeNull()
    expect(browserResourceEvidenceView(change('origin', 'https://bücher.example')).evidence?.origin).toBe('https://bücher.example')
  })
  it('admits the shared 512-byte native origin boundary and refuses overflow', () => {
    const origin = `https://${'a'.repeat(504)}`
    expect(new TextEncoder().encode(origin)).toHaveLength(512)
    expect(browserResourceEvidenceView(change('origin', origin)).evidence?.origin).toBe(origin)
    expect(browserResourceEvidenceView(change('origin', `${origin}a`)).evidence).toBeNull()
  })
  it('plots overlapping captured offsets and keeps absent endpoints as gaps', () => {
    const input = fixture()
    const rows = browserResourcePhaseRows(input)
    expect(rows[0]).toMatchObject({ start_percent: 0, end_percent: 0 })
    expect(rows[1]).toMatchObject({ start_percent: 0, end_percent: 100 })
    expect(rows[2].start_percent).toBeCloseTo(100 / 3, 10)
    expect(rows[2].end_percent).toBe(100)
    expect(rows[3].start_percent).toBe(100)
    expect(rows[3].end_percent).toBeUndefined()
    expect(rows[3].unavailable_reason).toBe('not_exposed')
    expect(browserResourcePhaseRows({ ...input, phases: null })).toEqual([])
  })
  it('uses locators and native identities without URL/action-derived collisions', () => {
    const input = fixture()
    expect(browserResourceRowKey('transaction:1/resource:0', input)).not.toBe(browserResourceRowKey('transaction:2/resource:0', input))
    expect(browserResourceRowKey('transaction:1/resource:0', input)).not.toBe(browserResourceRowKey('transaction:1/resource:0', { ...input, request_id: `${generation}-r2` }))
    expect(browserResourceRowKey('transaction:1/resource:0')).toBe(JSON.stringify(['transaction:1/resource:0', null, null]))
  })

  it('admits proven document association and bounded initiator provenance only', () => {
    const associated = { ...fixture(), version: 3, document_id: `${generation}-d1`, document_association: 'cdp_loader_join', identity_unavailable_reason: undefined,
      initiator_type: 'parser', initiator_origin: 'https://example.test', initiator_unavailable_reason: undefined, cache: true, document_offset_ms: -0.5 }
    expect(browserResourceEvidenceView(associated).issue).toBeNull()
    expect(browserResourceEvidenceView({ ...fixture(), version: 3, identity_unavailable_reason: 'ambiguous_cdp_join' }).issue).toBeNull()
    expect(browserResourceEvidenceView({ ...fixture(), version: 3, identity_unavailable_reason: 'request_start_unavailable' }).issue).toBeNull()
    // Version 2 predates association: neither its fields nor its reasons.
    expect(browserResourceEvidenceView({ ...associated, version: 2 }).evidence).toBeNull()
    expect(browserResourceEvidenceView({ ...fixture(), version: 2, identity_unavailable_reason: 'ambiguous_cdp_join' }).evidence).toBeNull()
    for (const bad of [
      { ...associated, document_id: undefined },
      { ...associated, document_association: 'frame_guess' },
      { ...associated, initiator_type: 'stack' },
      { ...associated, initiator_origin: 'https://example.test/script.js' },
      { ...fixture(), initiator_origin: 'https://example.test' },
    ]) expect(browserResourceEvidenceView(bad).evidence).toBeNull()
  })

  it('places document requests at their true start and keeps unattributed time as gaps', () => {
    const document = `${generation}-d1`
    const evidence = (offset: number | null, phases: BrowserResourceEvidenceV2['phases'], doc = document): BrowserResourceEvidenceV2 => ({ ...fixture(), document_id: doc, document_offset_ms: offset, phases })
    const rows = browserDocumentWaterfall([
      { key: 'late', label: 'late.js', evidence: evidence(40, [{ name: 'request_wait', start_ms: 5, end_ms: 15 }, { name: 'response', start_ms: 15, end_ms: 20 }]) },
      { key: 'early', label: 'app.css', evidence: evidence(2, [{ name: 'dns', start_ms: 0, end_ms: 1 }, { name: 'response', start_ms: 4, end_ms: 9 }]) },
      { key: 'other', label: 'other document', evidence: evidence(1, [], `${generation}-d2`) },
      { key: 'no-offset', label: 'unanchored', evidence: evidence(null, []) },
    ], document)
    expect(rows.map(row => row.key)).toEqual(['early', 'late'])
    expect(rows[0]).toMatchObject({ start: 2, end: 11, segments: [{ name: 'dns', start: 2, end: 3 }, { name: 'response', start: 6, end: 11 }], gaps: [{ start: 3, end: 6 }] })
    expect(rows[1].gaps).toEqual([{ start: 40, end: 45 }])
  })
})

describe('version 4 negotiated protocol', () => {
  const v4 = (patch: Record<string, unknown>) => ({ ...fixture(), version: 4, identity_unavailable_reason: 'cdp_unobserved', ...patch })
  it('admits a negotiated protocol or an unavailable reason, never both', () => {
    for (const protocol of ['h1', 'h2', 'h3']) expect(browserResourceEvidenceView(v4({ protocol })).issue).toBeNull()
    for (const reason of ['cached', 'service_worker', 'draft_protocol', 'not_exposed', 'no_observation', 'no_response', 'not_network', 'unsupported']) {
      expect(browserResourceEvidenceView(v4({ protocol_unavailable_reason: reason })).issue).toBeNull()
    }
    expect(browserResourceEvidenceView(v4({ protocol: 'h3', protocol_unavailable_reason: 'cached' })).issue).not.toBeNull()
    expect(browserResourceEvidenceView(v4({ protocol: 'h3-29' })).issue).not.toBeNull()
    expect(browserResourceEvidenceView(v4({ protocol_unavailable_reason: 'guessed' })).issue).not.toBeNull()
  })
  it('refuses protocol fields on version 2 and 3 records', () => {
    expect(browserResourceEvidenceView(change('protocol', 'h3')).issue).not.toBeNull()
    expect(browserResourceEvidenceView({ ...fixture(), version: 3, identity_unavailable_reason: 'cdp_unobserved', protocol: 'h2' }).issue).not.toBeNull()
  })
  it('labels the protocol, the reason it is unknown, and older evidence', () => {
    expect(browserResourceProtocolLabel(v4({ protocol: 'h3' }) as BrowserResourceEvidenceV2)).toBe('HTTP/3 (QUIC)')
    expect(browserResourceProtocolLabel(v4({ protocol: 'h1' }) as BrowserResourceEvidenceV2)).toBe('HTTP/1.x')
    expect(browserResourceProtocolLabel(v4({ protocol_unavailable_reason: 'cached' }) as BrowserResourceEvidenceV2)).toBe('Unavailable: served from cache')
    expect(browserResourceProtocolLabel(fixture())).toBe('Not recorded (older evidence)')
  })
})
