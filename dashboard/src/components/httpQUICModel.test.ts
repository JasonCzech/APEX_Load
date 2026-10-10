import { describe, expect, it } from 'vitest'
import { http3PolicyIssues, networkShapingActive, patchQUICConfig, quicFieldIssues, quicPanelDisabledReason, quicUnsupportedKeys } from './httpQUICModel'

describe('httpQUICModel', () => {
  it('mirrors the runtime QUIC bounds at each field', () => {
    expect(quicFieldIssues(undefined)).toEqual({})
    expect(quicFieldIssues({ handshake_timeout_ms: 1000, max_idle_timeout_ms: 600000, keep_alive_ms: 600000, alt_svc_cache: 'per_iteration', broken_ttl_ms: 0 })).toEqual({})
    const issues = quicFieldIssues({ handshake_timeout_ms: 999, max_idle_timeout_ms: 600001, keep_alive_ms: 30001, alt_svc_cache: 'per_origin' as never, broken_ttl_ms: -1 })
    expect(Object.keys(issues).sort()).toEqual(['alt_svc_cache', 'broken_ttl_ms', 'handshake_timeout_ms', 'keep_alive_ms', 'max_idle_timeout_ms'])
    // An invalid idle timeout leaves keep-alive bounded by the default (30 s).
    expect(issues.keep_alive_ms).toContain('30,000')
    // Keep-alive is bounded by the authored idle timeout when that is valid.
    expect(quicFieldIssues({ max_idle_timeout_ms: 5000, keep_alive_ms: 6000 }).keep_alive_ms).toContain('5,000')
    expect(quicFieldIssues({ handshake_timeout_ms: 1500.5 }).handshake_timeout_ms).toBeDefined()
    // Zero means unset for the two timeouts, as on the controller.
    expect(quicFieldIssues({ handshake_timeout_ms: 0, max_idle_timeout_ms: 0 })).toEqual({})
  })

  it('patches fields without dropping unknown keys and removes an empty block', () => {
    const current = { handshake_timeout_ms: 3000, receive_window: 1 } as never
    expect(patchQUICConfig(current, { broken_ttl_ms: 0 })).toEqual({ handshake_timeout_ms: 3000, receive_window: 1, broken_ttl_ms: 0 })
    expect(patchQUICConfig({ handshake_timeout_ms: 3000 }, { handshake_timeout_ms: undefined })).toBeUndefined()
    expect(patchQUICConfig(undefined, { alt_svc_cache: '' })).toBeUndefined()
    expect(quicUnsupportedKeys(current)).toEqual(['receive_window'])
  })

  it('names why the QUIC settings cannot apply', () => {
    expect(quicPanelDisabledReason({}, {})).toBe('')
    expect(quicPanelDisabledReason({ proxy: { url: 'http://proxy:3128' } }, {})).toContain('custom proxy')
    expect(quicPanelDisabledReason({ proxy_mode: 'system', proxy: { url: 'http://proxy:3128' } }, {})).toBe('')
    expect(quicPanelDisabledReason({ auth: { type: 'Negotiate' } }, {})).toContain('Integrated authentication')
    expect(quicPanelDisabledReason({}, { network_simulation: { mode: 'preset', preset: 'dsl' } })).toContain('Network shaping')
  })

  it('detects shaping like the runtime', () => {
    expect(networkShapingActive({ network_simulation: { mode: 'preset', preset: 'unknown' } })).toBe(false)
    expect(networkShapingActive({ network_simulation: { mode: 'custom', latency_ms: 0, packet_loss_pct: 0.5 } })).toBe(true)
    expect(networkShapingActive({ network_simulation: { mode: '', latency_ms: 50 } })).toBe(false)
    expect(networkShapingActive({ network_profiles: [{ name: 'slow', weight: 1, mode: 'custom', jitter_ms: 5 }] })).toBe(true)
  })

  it('reports HTTP/3 policy conflicts at the field', () => {
    expect(http3PolicyIssues('2', { http: { tls: { max_version: '1.2' } } })).toEqual([])
    expect(http3PolicyIssues('3', { target: '/orders', baseURL: 'http://shop.example' })).toEqual([expect.stringContaining('only https origins')])
    expect(http3PolicyIssues('3', { target: 'https://api.example/orders', baseURL: 'http://shop.example' })).toEqual([])
    expect(http3PolicyIssues('3', { http: { tls: { max_version: '1.2' }, proxy: { url: 'http://proxy' }, auth: { type: 'ntlm' } } })).toHaveLength(3)
    // auto+h3 keeps those requests on TCP; only shaping conflicts with it.
    expect(http3PolicyIssues('auto+h3', { target: 'http://shop.example', http: { proxy: { url: 'http://proxy' } } })).toEqual([])
    expect(http3PolicyIssues('auto+h3', { web: { network_simulation: { mode: 'preset', preset: '56k' } } })).toEqual([expect.stringContaining('Network shaping')])
  })
})
