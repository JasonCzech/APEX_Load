import { describe, expect, it } from 'vitest'
import type { BrowserResourceEvidenceV2 } from '../types/browserResourceEvidence'
import { browserResourceEvidenceView, browserResourcePhaseRows, browserResourceRowKey } from './browserResourceEvidenceModel'

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
    for (const input of [undefined, null, {}, { version: 3 }]) {
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
    ['version', 3], ['source_generation', generation.toUpperCase()], ['source_generation', 'a'.repeat(31)],
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
})
