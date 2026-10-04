import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import type { ProtocolEvidence } from '../types/replay'
import { HTTPResponseContentEvidence } from './HTTPResponseContentEvidence'
import { httpResponseContentEvidence } from './httpResponseContentEvidenceModel'

describe('HTTP response content evidence', () => {
  it.each(['decoded', 'identity'] as const)('qualifies only a complete response with the admitted %s representation', basis => {
    const html = renderToStaticMarkup(<HTTPResponseContentEvidence evidence={{ response_content_state: 'complete', response_content_basis: basis }} />)
    expect(html).toContain('Response content status</dt><dd>Complete response')
    expect(html).toContain(basis === 'decoded' ? 'Decoded response content' : 'Identity response content')
    expect(html).toContain('full response was read')
  })

  it('labels a successful SSE collection as a partial sample rather than a complete response', () => {
    const html = renderToStaticMarkup(<HTTPResponseContentEvidence evidence={{ response_content_state: 'collection_window', response_content_basis: 'decoded' }} bodyState="partial" />)
    expect(html).toContain('Collection window (partial response)')
    expect(html).toContain('Decoded response content')
    expect(html).toContain('Full-response completion was not established')
    expect(html).toContain('cannot supply a complete correlation response')
    expect(html).not.toContain('Complete response</dd>')
    const legacy = httpResponseContentEvidence(undefined, 'partial')
    expect(legacy?.state).toBe('Partial captured response')
    expect(legacy?.basis).toBe('Not established')
  })

  it('never infers completion from snapshot capture or an unsupported receipt', () => {
    expect(httpResponseContentEvidence(undefined, 'captured')).toBeNull()
    expect(httpResponseContentEvidence({ response_content_state: 'complete' })?.state).toBe('Response content unavailable')
    const html = renderToStaticMarkup(<HTTPResponseContentEvidence evidence={{ response_content_state: 'synthetic-secret-canary', response_content_basis: 'synthetic-secret-canary' } as unknown as ProtocolEvidence} />)
    expect(html).toContain('Response content unavailable')
    expect(html).not.toContain('synthetic-secret-canary')
    const unavailable = httpResponseContentEvidence({ response_content_state: 'unavailable', response_content_basis: 'decoded' })
    expect(unavailable?.basis).toBe('Not established')
    expect(httpResponseContentEvidence({ response_content_state: 'unavailable', response_content_basis: 'decoded' }, 'partial')?.state).toBe('Response content unavailable')
    expect(httpResponseContentEvidence({ response_content_state: 'complete', response_content_basis: 'decoded' }, 'partial')?.state).toBe('Partial captured response')
  })

  it('qualifies cached decoder input without claiming the original wire representation', () => {
    const html = renderToStaticMarkup(<HTTPResponseContentEvidence evidence={{ response_content_state: 'complete', response_content_basis: 'identity', http_version_source: 'cache' }} />)
    expect(html).toContain('Stored response content (cached)')
    expect(html).toContain('original wire representation was not observed')
    expect(html).not.toContain('Identity response content')
  })

  it('qualifies cached content independently of the negotiated revalidation wire version', () => {
    const evidence: ProtocolEvidence = { response_content_state: 'complete', response_content_basis: 'identity', response_content_source: 'cache', http_version_source: 'wire', http_version_negotiated: '2' }
    const html = renderToStaticMarkup(<HTTPResponseContentEvidence evidence={evidence} />)
    expect(html).toContain('Stored response content (cached)')
    expect(html).toContain('original wire representation was not observed')
    expect(html).not.toContain('Identity response content')
    expect(evidence.http_version_source).toBe('wire')
    expect(evidence.http_version_negotiated).toBe('2')
    expect(httpResponseContentEvidence({ ...evidence, response_content_source: 'wire', http_version_source: 'cache' })?.basis).toBe('Identity response content')
  })

  it.each(['synthetic-source-canary', '', null, undefined])('rejects an explicitly invalid content source %s without falling back to cache evidence', source => {
    const evidence = { response_content_state: 'complete', response_content_basis: 'identity', response_content_source: source, http_version_source: 'cache' } as unknown as ProtocolEvidence
    const html = renderToStaticMarkup(<HTTPResponseContentEvidence evidence={evidence} />)
    expect(html).toContain('Response content unavailable')
    expect(html).toContain('Not established')
    expect(html).not.toContain('synthetic-source-canary')
    expect(html).not.toContain('Complete response</dd>')
    expect(html).not.toContain('Stored response content (cached)')
    expect(httpResponseContentEvidence(evidence, 'partial')?.state).toBe('Response content unavailable')
  })

  it('gives sticky runtime evidence omission precedence over receipts and partial body states', () => {
    expect(renderToStaticMarkup(<HTTPResponseContentEvidence unavailable evidence={{ response_content_state: 'collection_window', response_content_basis: 'decoded' }} bodyState="partial" />)).toBe('')
  })
})
