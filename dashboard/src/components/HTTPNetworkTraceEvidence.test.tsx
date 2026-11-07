import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { HTTPNetworkTraceEvidence } from './HTTPNetworkTraceEvidence'
import type { BrowserReplayNetworkEntry, ProtocolEvidence } from '../types/replay'

describe('HTTPNetworkTraceEvidence', () => {
  const fullEvidence: ProtocolEvidence = {
    network_trace: {
      http_protocol: 'HTTP/2.0',
      remote_addr: '203.0.113.10:443',
      local_addr: '10.0.0.5:54321',
      dns_resolved_ips: ['203.0.113.10', '203.0.113.11'],
      dns_coalesced: true,
      connection_reused: true,
      was_idle: true,
      idle_time_ms: 14.25,
      got_100_continue: true,
      wait_100_continue_ms: 3.5,
      tls_version: 'TLS 1.3',
      tls_cipher_suite: 'TLS_AES_128_GCM_SHA256',
      tls_negotiated_proto: 'h2',
      tls_server_name: 'api.example.test',
      tls_did_resume: true,
      tls_cert_subject: 'CN=api.example.test',
      tls_cert_issuer: 'CN=Example Root CA',
      tls_cert_expiry: '2027-01-01T00:00:00Z',
      request_compression: 'gzip',
      content_encoding: 'gzip',
      transfer_encoding: ['chunked'],
      encoded_body_bytes: 512,
      decoded_body_bytes: 2048,
      cookies_sent_count: 2,
      cookie_names_sent: ['session_id', 'csrf_token'],
      set_cookies_received_count: 1,
      set_cookie_names_received: ['session_id'],
      cross_origin_redirect: true,
      stripped_redirect_headers: ['Authorization', 'Cookie'],
    },
  }

  const hops: BrowserReplayNetworkEntry[] = [
    {
      method: 'POST',
      url: 'https://auth.example.test/login',
      status_code: 302,
      duration_ms: 18.4,
      har: {
        started_at: '2026-01-01T00:00:00Z',
        phases: [
          { name: 'dns', start_ms: 0, end_ms: 1.2 },
          { name: 'connect', start_ms: 1.2, end_ms: 4.6 },
          { name: 'tls', start_ms: 4.6, end_ms: 9.1 },
          { name: 'send', start_ms: 9.1, end_ms: 9.6 },
          { name: 'request_wait', start_ms: 9.6, end_ms: 17.4 },
          { name: 'response', start_ms: 17.4, end_ms: 18.4 },
        ],
      },
      trace: {
        http_protocol: 'HTTP/2.0',
        connection_reused: false,
        remote_addr: '203.0.113.9:443',
        tls_version: 'TLS 1.3',
        tls_negotiated_proto: 'h2',
        stripped_redirect_headers: ['Authorization'],
      },
    },
    {
      method: 'GET',
      url: 'https://api.example.test/dashboard',
      status_code: 200,
      duration_ms: 9.2,
      har: {
        started_at: '2026-01-01T00:00:00.020Z',
        phases: [
          { name: 'send', start_ms: 0, end_ms: 0.3 },
          { name: 'request_wait', start_ms: 0.3, end_ms: 8.4 },
          { name: 'response', start_ms: 8.4, end_ms: 9.2 },
        ],
      },
      trace: {
        http_protocol: 'HTTP/2.0',
        connection_reused: true,
        remote_addr: '203.0.113.10:443',
        tls_version: 'TLS 1.3',
        tls_negotiated_proto: 'h2',
        content_encoding: 'gzip',
      },
    },
  ]

  it('renders transport, TLS, compression, cookie, redirect, and hop waterfall telemetry', () => {
    const html = renderToStaticMarkup(<HTTPNetworkTraceEvidence evidence={fullEvidence} networkEntries={hops} />)
    expect(html).toContain('Network &amp; transport trace · HTTP/2.0 · reused socket · TLS 1.3 · gzip · 2 hops')
    expect(html).toContain('Reused connection (idle 14.25 ms)')
    expect(html).toContain('203.0.113.10:443')
    expect(html).toContain('10.0.0.5:54321')
    expect(html).toContain('203.0.113.10, 203.0.113.11')
    expect(html).toContain('(coalesced)')
    expect(html).toContain('Waited for 100-Continue response (3.50 ms)')
    expect(html).toContain('TLS 1.3 · TLS_AES_128_GCM_SHA256 · ALPN h2 · resumed session')
    expect(html).toContain('api.example.test')
    expect(html).toContain('subject CN=api.example.test · issuer CN=Example Root CA · expires 2027-01-01T00:00:00Z')
    expect(html).toContain('request gzip · response gzip · transfer chunked')
    expect(html).toContain('512 B wire → 2048 B decoded')
    expect(html).toContain('sent 2 (session_id, csrf_token) · received 1 (session_id)')
    expect(html).toContain('2 hops · stripped cross-origin headers: Authorization, Cookie')
    expect(html).toContain('Observed HTTP hops (2)')
    expect(html).toContain('Hop 1: POST 302')
    expect(html).toContain('Hop 2: GET 200')
    expect(html).toContain('dns 1.2 ms · connect 3.4 ms · tls 4.5 ms · send 0.5 ms · request_wait 7.8 ms · response 1.0 ms')
  })

  it('renders nothing when unavailable or when no trace or hop HAR is present', () => {
    expect(renderToStaticMarkup(<HTTPNetworkTraceEvidence evidence={fullEvidence} networkEntries={hops} unavailable />)).toBe('')
    expect(renderToStaticMarkup(<HTTPNetworkTraceEvidence evidence={{}} networkEntries={[{ url: 'https://example.test', method: 'GET' }]} />)).toBe('')
  })
})
