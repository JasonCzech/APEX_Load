import { describe, expect, it } from 'vitest'
import type { HTTPActionVersion, ScriptActionDefinition } from '../types/scripts'
import type { ProtocolEvidence } from '../types/replay'
import { capturedHTTPVersionLabel, httpCapturedVersionEvidence, httpActionVersionSettings, httpReplayVersionEvidence, inheritedHTTPActionVersion, setHTTPActionVersion, supportsHTTPActionVersion } from './httpActionVersionModel'
import { httpSequenceRequests } from './scriptActionHelpers'
import { updateNestedAction } from './scriptEditorJsonModel'

const action = (patch: Partial<ScriptActionDefinition> = {}): ScriptActionDefinition => ({ name: 'Read', type: 'GET', target: '/read', ...patch })
const raw = (value: unknown) => ({ ...action(), http_version: value } as ScriptActionDefinition)

describe('per-request HTTP version policy', () => {
  it.each(['http', 'http/1.1', 'http/2', 'soap', 'java_http'])('offers policies for supported method leaves in %s', protocol => {
    for (const type of ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS', ' get ']) expect(supportsHTTPActionVersion(action({ type }), protocol)).toBe(true)
  })

  it.each(['browser', 'kafka', 'grpc', 'network', 'database', 'websocket', 'java_vuser'])('omits HTTP policy controls in %s', protocol => {
    expect(httpActionVersionSettings(action(), protocol, '2')).toBeNull()
  })

  it.each(['http_sequence', 'http_oauth2_token', 'cookie_clear', 'cache_clear', 'http_connections_close', 'control_if', 'source_review', 'TRACE', 'PROPFIND'])('does not offer a leaf override on %s', type => {
    expect(httpActionVersionSettings(action({ type }), 'http', '2')).toBeNull()
  })

  it('resolves explicit methods for legacy request leaves and still excludes non-request actions', () => {
    expect(supportsHTTPActionVersion(action({ type: 'request', method: 'POST' }), 'http')).toBe(true)
    expect(supportsHTTPActionVersion(action({ type: 'GET', method: 'TRACE' }), 'http')).toBe(false)
    expect(supportsHTTPActionVersion(action({ type: 'request', method: ' post ' }), 'soap')).toBe(true)
    for (const type of ['http_sequence', 'http_oauth2_token', 'cookie_clear', 'control_parallel', 'source_message', 'loadrunner_timer', 'rendezvous', 'error_message'])
      expect(supportsHTTPActionVersion(action({ type, method: 'POST' }), 'http')).toBe(false)
  })

  it.each(['auto', '1.1', '2'] as const)('keeps the exact authored %s policy independent of the script default', policy => {
    expect(httpActionVersionSettings(action({ http_version: policy }), 'http', '2')).toMatchObject({ present: true, valid: true, policy, selection: policy })
  })

  it.each([null, '', 'HTTP/2', '2.0', ' 1.1 ', 'AUTO', 2, false, [], {}, undefined].map(value => ({ value })))('preserves invalid own HTTP version $value', ({ value }) => {
    const original = raw(value)
    expect(httpActionVersionSettings(original, 'http', '2')).toMatchObject({ present: true, valid: false, policy: null, selection: 'invalid' })
    expect(Object.prototype.hasOwnProperty.call(original, 'http_version')).toBe(true)
    expect(original.http_version).toBe(value)
  })

  it.each([
    ['http', undefined, 'auto'], ['http', '', 'auto'], ['http/1.1', undefined, '1.1'], ['http/2', '', '2'],
    ['http/2', '   ', 'auto'], ['http/2', '1.1', '1.1'], ['http/1.1', 'HTTP/2.0', '2'], ['soap', 'HTTP/1.1', '1.1'], ['java_http', '2', '2'],
    ['http', 'auto', null], ['http', '1.0', null],
  ] as const)('inherits %s with script setting %j as %j', (protocol, scriptVersion, policy) => {
    expect(inheritedHTTPActionVersion(protocol, scriptVersion)).toBe(policy)
    expect(httpActionVersionSettings(action(), protocol, scriptVersion)).toMatchObject({ present: false, valid: true, policy, selection: 'inherit' })
  })

  it('does not reinterpret malformed script defaults as inheritance', () => {
    for (const value of [null, 2, {}, []]) expect(inheritedHTTPActionVersion('http/2', value as unknown as string)).toBeNull()
  })

  it('deletes an inherited override while preserving unknown fields and every typed request field', () => {
    const input = { ...raw(null), auth: { type: 'basic', username: '{{secret.user}}' }, header_list: [{ name: 'X-Trace', value: 'keep' }],
      body_encoding: 'base64', body: 'YQ==', timeout_ms: 1200, params: { resources: [{ url: '/resource' }] }, metadata: { captured: 'keep' },
      future_field: { preserved: true } } as ScriptActionDefinition
    const edited = setHTTPActionVersion(input, 'auto')
    expect(edited).toEqual({ ...input, http_version: 'auto' })
    const inherited = setHTTPActionVersion(edited, undefined)
    expect(Object.prototype.hasOwnProperty.call(inherited, 'http_version')).toBe(false)
    expect(inherited).toEqual(Object.fromEntries(Object.entries(input).filter(([key]) => key !== 'http_version')))
    expect(JSON.parse(JSON.stringify(inherited))).not.toHaveProperty('http_version')
    expect(input.http_version).toBeNull()
    expect(setHTTPActionVersion(input, 'HTTP/2' as HTTPActionVersion)).toBe(input)
  })

  it('preserves malformed presence in sequence display and changes only a selected nested leaf', () => {
    const child = { ...raw(null), vendor_extension: { keep: 1 } }
    const sibling = action({ name: 'Other', http_version: '2' })
    const sequence = action({ type: 'http_sequence', params: { requests: [child, sibling], future_option: true }, metadata: { keep: 'parent' } })
    const display = httpSequenceRequests(sequence)
    expect(Object.prototype.hasOwnProperty.call(display[0], 'http_version')).toBe(true)
    expect(display[0].http_version).toBeNull()
    expect(httpSequenceRequests(action({ type: 'http_sequence', params: { requests: [action()] } }))[0]).not.toHaveProperty('http_version')
    const root = action({ type: 'control_if', control: { steps: [sequence] }, metadata: { keep: 'root' } })
    const edited = updateNestedAction(root, ['then[0]', 'requests[0]'], leaf => setHTTPActionVersion(leaf, '1.1'))
    const requests = edited.control!.steps![0].params!.requests as ScriptActionDefinition[]
    expect(requests[0]).toEqual({ ...child, http_version: '1.1' })
    expect(requests[1]).toEqual(sibling)
    expect(edited.metadata).toEqual(root.metadata)
    expect(edited.control!.steps![0].metadata).toEqual(sequence.metadata)
    const inherited = updateNestedAction(edited, ['then[0]', 'requests[0]'], leaf => setHTTPActionVersion(leaf, undefined))
    expect((inherited.control!.steps![0].params!.requests as ScriptActionDefinition[])[0]).not.toHaveProperty('http_version')
    expect(JSON.parse(JSON.stringify(inherited)).control.steps[0].params.requests[0].vendor_extension).toEqual({ keep: 1 })
    expect(child.http_version).toBeNull()
  })
})

describe('qualified HTTP version evidence', () => {
  it.each([
    ['HTTP/1.1', 'HTTP/1.1'], ['HTTP/2.0', 'HTTP/2'], ['unknown', 'Not observed'],
    ['HTTP/1.0', 'HTTP/1.0 (captured; unsupported for replay)'], ['untrusted-detail', 'Unrecognized captured version'],
    [undefined, 'Not observed'], [null, 'Not observed'], [2, 'Not observed'],
  ])('labels captured observation %j without treating it as replay proof', (raw, expected) => {
    expect(capturedHTTPVersionLabel(raw)).toBe(expected)
  })

  it('prioritizes bounded observations and identifies the two proven proxy legs', () => {
    const metadata = { http_version_capture: { version: 1, request_state: 'available', response_state: 'available', proxy: true, provenance: 'proxy_legs', request_version: '2', response_version: '1.1' },
      'har.request.http_version': 'HTTP/1.1', 'har.response.http_version': 'HTTP/2' }
    expect(httpCapturedVersionEvidence(metadata)).toEqual({ proxyLegs: true, requestLabel: 'Captured request HTTP version (client to proxy)', responseLabel: 'Captured response HTTP version (proxy to origin)', request: 'HTTP/2', response: 'HTTP/1.1' })
    expect(metadata['har.request.http_version']).toBe('HTTP/1.1')
  })

  it('explains withheld unsupported and malformed observations without exposing raw detail', () => {
    const metadata = { http_version_capture: { version: 1, request_state: 'unsupported', response_state: 'malformed', proxy: false, provenance: 'request' }, 'har.request.http_version': 'arbitrary-source-value', 'har.response.http_version': 'arbitrary-source-value' }
    const result = httpCapturedVersionEvidence(metadata)
    expect(result.request).toBe('Unsupported captured HTTP version (source value not retained)')
    expect(result.response).toBe('Malformed captured HTTP version (source value not retained)')
    expect(JSON.stringify(result)).not.toContain('arbitrary-source-value')
    expect(result.proxyLegs).toBe(false)
  })

  it('does not admit fabricated legacy H1 observations or malformed proxy provenance', () => {
    const unavailable = { http_version_capture: { version: 1, request_state: 'unavailable', response_state: 'unavailable', proxy: true, provenance: 'unavailable' }, 'har.request.http_version': 'HTTP/1.1', 'har.response.http_version': 'HTTP/1.1' }
    expect(httpCapturedVersionEvidence(unavailable)).toMatchObject({ proxyLegs: false, request: 'Not observed', response: 'Not observed' })
    expect(httpCapturedVersionEvidence({ 'har.http_version_provenance': { version: 1, request_leg: 'client_to_proxy', response_leg: 'proxy_to_origin' }, 'har.request.http_version': 'HTTP/2' })).toMatchObject({ proxyLegs: true, request: 'HTTP/2' })
    expect(httpCapturedVersionEvidence({ 'har.http_version_provenance': { version: 2, request_leg: 'client_to_proxy', response_leg: 'proxy_to_origin' }, 'har.request.http_version': 'HTTP/2' })).toMatchObject({ proxyLegs: false, request: 'HTTP/2' })
  })

  it('retains a measured wire version independently of the selected policy', () => {
    expect(httpReplayVersionEvidence({ http_version_policy: 'auto', http_version_negotiated: '1.1', http_version_source: 'wire' }))
      .toEqual({ policy: 'Auto-negotiate (HTTP/2 or HTTP/1.1)', actual: 'HTTP/1.1', source: 'Wire response', advertisesHTTP3: false, http3: [] })
    expect(httpReplayVersionEvidence({ http_version_policy: '2', http_version_negotiated: '2', http_version_source: 'wire' })?.actual).toBe('HTTP/2')
    expect(httpReplayVersionEvidence({ http_version_policy: '3', http_version_negotiated: '3', http_version_source: 'wire', alt_svc_h3_advertised: true })).toMatchObject({ policy: 'HTTP/3 required (no fallback)', actual: 'HTTP/3', advertisesHTTP3: false })
    expect(httpReplayVersionEvidence({ http_version_policy: '3', http_version_negotiated: '3', http_version_source: 'wire', quic_attempted: true, http_version_selection_reason: 'exact_policy', quic_connection: 'new', tls_resumed: true, attempt_count: 1, tls_in_transport_handshake: true })?.http3).toEqual([
      { label: 'HTTP/3 routing', value: 'Exact HTTP/3 policy' },
      { label: 'QUIC connection', value: 'New connection (TLS resumed)' },
      { label: 'HTTP attempts', value: '1' },
      { label: 'TLS timing', value: 'Part of the QUIC handshake (counted in connect time)' },
    ])
    expect(httpReplayVersionEvidence({ http_version_policy: '3', quic_attempted: true, quic_connection: 'dial_failed', http3_failure_certainty: 'not_sent' })?.http3).toEqual([
      { label: 'QUIC connection', value: 'Could not open a connection' },
      { label: 'Failure certainty', value: 'Not sent' },
    ])
    expect(httpReplayVersionEvidence({ http_version_policy: '3', quic_attempted: true, http3_failure_certainty: 'possibly_sent', quic_connection: 'surprise', attempt_count: -1 } as unknown as ProtocolEvidence)?.http3).toEqual([
      { label: 'Failure certainty', value: 'Possibly sent (the target may have processed it)' },
    ])
  })

  it('never infers negotiation from policy, cached responses or malformed evidence', () => {
    expect(httpReplayVersionEvidence({ http_version_policy: '2' })?.actual).toBe('Not observed')
    expect(httpReplayVersionEvidence({ http_version_policy: '2', http_version_source: 'cache', http_version_negotiated: '2' })?.actual).toBe('Not measured (cached response)')
    expect(httpReplayVersionEvidence({ http_version_source: 'wire', http_version_negotiated: 'HTTP/2', http_version_policy: 'private-detail' } as unknown as ProtocolEvidence))
      .toEqual({ policy: 'Not retained', actual: 'Not observed', source: 'Wire response', advertisesHTTP3: false, http3: [] })
    expect(httpReplayVersionEvidence(undefined)).toBeNull()
    expect(httpReplayVersionEvidence({ http_version_source: 'wire', http_version_negotiated: '2', alt_svc_h3_advertised: true })?.advertisesHTTP3).toBe(true)
    expect(httpReplayVersionEvidence({ http_version_source: 'cache', alt_svc_h3_advertised: true })?.advertisesHTTP3).toBe(false)
    expect(httpReplayVersionEvidence({ http_version_source: 'wire', alt_svc_h3_advertised: 'yes' } as unknown as ProtocolEvidence)?.advertisesHTTP3).toBe(false)
    expect(httpReplayVersionEvidence({ protocol: 'http', metadata: { http_version: '2' } })).toBeNull()
  })
})
