import { describe, expect, it } from 'vitest'

import {
  formatCapturedBody,
  formatSnapshotRequestSection,
  formatSnapshotResponseSection,
} from './snapshotFormatUtils'

describe('formatCapturedBody', () => {
  it('pretty prints XML payloads detected from headers', () => {
    const body = '<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope"><env:Body><env:Fault><env:Code><env:Value>env:Sender</env:Value></env:Code><env:Reason><env:Text xml:lang="en">Request validation failed</env:Text></env:Reason></env:Fault></env:Body></env:Envelope>'

    expect(formatCapturedBody(body, { 'Content-Type': 'application/soap+xml' })).toContain('\n  <env:Body>')
    expect(formatCapturedBody(body, { 'Content-Type': 'application/soap+xml' })).toContain('\n        <env:Value>env:Sender</env:Value>')
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