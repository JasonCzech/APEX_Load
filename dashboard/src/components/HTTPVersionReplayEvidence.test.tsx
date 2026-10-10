import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import type { ProtocolEvidence } from '../types/replay'
import { HTTPVersionReplayEvidence } from './HTTPVersionReplayEvidence'

describe('HTTP version replay evidence', () => {
  it('separates the selected replay policy from the observed negotiated wire version', () => {
    const html = renderToStaticMarkup(<HTTPVersionReplayEvidence evidence={{ http_version_policy: 'auto', http_version_source: 'wire', http_version_negotiated: '1.1' }} />)
    expect(html).toContain('aria-label="HTTP version evidence"')
    expect(html).toContain('Replay policy</dt><dd>Auto-negotiate')
    expect(html).toContain('Actual negotiated HTTP version</dt><dd>HTTP/1.1')
    expect(html).toContain('Version evidence source</dt><dd>Wire response')
  })

  it('does not infer exact negotiation from a required policy or cached response', () => {
    const missing = renderToStaticMarkup(<HTTPVersionReplayEvidence evidence={{ http_version_policy: '2' }} />)
    expect(missing).toContain('Actual negotiated HTTP version</dt><dd>Not observed')
    const cached = renderToStaticMarkup(<HTTPVersionReplayEvidence evidence={{ http_version_policy: '2', http_version_source: 'cache', http_version_negotiated: '2' }} />)
    expect(cached).toContain('Actual negotiated HTTP version</dt><dd>Not measured (cached response)')
    expect(cached).not.toContain('Actual negotiated HTTP version</dt><dd>HTTP/2')
  })

  it('notes an HTTP/3 advertisement the replay did not exercise', () => {
    const html = renderToStaticMarkup(<HTTPVersionReplayEvidence evidence={{ http_version_policy: 'auto', http_version_source: 'wire', http_version_negotiated: '2', alt_svc_h3_advertised: true }} />)
    expect(html).toContain('The target advertises HTTP/3 (Alt-Svc). This request used HTTP/2, so HTTP/3 was not exercised.')
    const plain = renderToStaticMarkup(<HTTPVersionReplayEvidence evidence={{ http_version_policy: 'auto', http_version_source: 'wire', http_version_negotiated: '2' }} />)
    expect(plain).not.toContain('advertises HTTP/3')
  })

  it('omits unavailable and legacy evidence, and never echoes malformed receipt values', () => {
    expect(renderToStaticMarkup(<HTTPVersionReplayEvidence unavailable evidence={{ http_version_policy: '2', http_version_source: 'wire', http_version_negotiated: '2' }} />)).toBe('')
    expect(renderToStaticMarkup(<HTTPVersionReplayEvidence evidence={{ protocol: 'http' }} />)).toBe('')
    const invalid = renderToStaticMarkup(<HTTPVersionReplayEvidence evidence={{ http_version_policy: 'unclassified-detail', http_version_negotiated: 'unclassified-detail', http_version_source: 'unclassified-detail' } as unknown as ProtocolEvidence} />)
    expect(invalid).not.toContain('unclassified-detail')
    expect(invalid).toContain('Not observed')
  })
})
