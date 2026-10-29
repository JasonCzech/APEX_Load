import { describe, expect, it } from 'vitest'

import {
  formatCapturedBody,
  formatSnapshotRequestLineAndHeaders,
  formatSnapshotRequestSection,
  formatSnapshotResponseStatusAndHeaders,
  formatSnapshotResponseSection,
} from './snapshotFormatUtils'

describe('formatCapturedBody', () => {
  it('pretty prints XML payloads detected from headers', () => {
    const body = '<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope"><env:Body><env:Fault><env:Code><env:Value>env:Sender</env:Value></env:Code><env:Reason><env:Text xml:lang="en">Request validation failed</env:Text></env:Reason></env:Fault></env:Body></env:Envelope>'

    expect(formatCapturedBody(body, { 'Content-Type': 'application/soap+xml' })).toContain('\n  <env:Body>')
    expect(formatCapturedBody(body, { 'Content-Type': 'application/soap+xml' })).toContain('\n        <env:Value>env:Sender</env:Value>')
  })

  it('decodes safe HTML entities in markup text nodes', () => {
    const body = '<HTML><BODY>"http&#58;&#47;&#47;csi&#45;gw&#45;ext&#46;att&#46;com&#47;Services" on this server.<P>Reference&#32;&#35;18&#46;f7d2f17</P></BODY></HTML>'

    expect(formatCapturedBody(body, { 'Content-Type': 'text/html' })).toBe([
      '<HTML>',
      '  <BODY>',
      '    "http://csi-gw-ext.att.com/Services" on this server.',
      '    <P>Reference #18.f7d2f17</P>',
      '  </BODY>',
      '</HTML>',
    ].join('\n'))
  })

  it('does not decode escaped angle brackets into rendered markup', () => {
    const body = '<HTML><BODY>Expected &lt;Success&gt; marker</BODY></HTML>'

    expect(formatCapturedBody(body, { 'Content-Type': 'text/html' })).toContain('Expected &lt;Success&gt; marker')
  })

  it('pretty prints JSON payloads detected from headers', () => {
    const body = '{"status":"failed","code":500}'

    expect(formatCapturedBody(body, { 'Content-Type': 'application/json' })).toBe('{' + '\n' + '  "status": "failed",' + '\n' + '  "code": 500' + '\n' + '}')
  })

  it('leaves plain text payloads unchanged', () => {
    expect(formatCapturedBody('plain text body', { 'Content-Type': 'text/plain' })).toBe('plain text body')
  })

  it('formats a full request section for clipboard copy', () => {
    expect(formatSnapshotRequestSection({
      request_method: 'POST',
      request_url: 'https://api.example.test/orders',
      request_headers: { 'Content-Type': 'application/json' },
      request_body: '{"orderId":123}',
    })).toBe([
      'Request',
      'POST https://api.example.test/orders',
      '',
      'Headers',
      'Content-Type: application/json',
      '',
      'Body',
      '{',
      '  "orderId": 123',
      '}',
    ].join('\n'))
  })

  it('includes the request header omission notice in copied evidence', () => {
    expect(formatSnapshotRequestSection({ request_headers_omitted: true })).toContain('Some request headers were omitted or shortened by retention limits.')
    expect(formatSnapshotRequestSection({ request_wire_headers_unavailable: true })).toContain('Actual wire headers could not be verified; the displayed headers describe the connection attempt and may be incomplete.')
  })

  it('formats request line and headers for the drill-down preview', () => {
    expect(formatSnapshotRequestLineAndHeaders({
      request_method: 'POST',
      request_url: 'https://api.example.test/orders',
      request_headers: { 'Content-Type': 'application/xml' },
    })).toBe([
      'POST https://api.example.test/orders',
      '',
      'Headers',
      'Content-Type: application/xml',
    ].join('\n'))
  })

  it('formats response status and headers for the drill-down preview', () => {
    expect(formatSnapshotResponseStatusAndHeaders({
      response_status: 403,
      response_headers: { 'Content-Type': 'application/xml' },
    }, 500)).toBe([
      'Status: 403',
      '',
      'Headers',
      'Content-Type: application/xml',
    ].join('\n'))
  })

  it('formats a full response section for clipboard copy', () => {
    expect(formatSnapshotResponseSection({
      response_status: 200,
      response_headers: { 'Content-Type': 'application/xml' },
      response_body: '<root><ok>true</ok></root>',
    })).toBe([
      'Response',
      'Status: 200',
      '',
      'Headers',
      'Content-Type: application/xml',
      '',
      'Body',
      '<root>',
      '  <ok>true</ok>',
      '</root>',
    ].join('\n'))
  })
})
