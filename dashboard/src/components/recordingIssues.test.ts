import { describe, expect, it } from 'vitest'
import type { BrowserRecorderAnnotation } from '../hooks/api/useBrowserRecorderApi'
import { siteOf, summarizeRecordingIssues } from './recordingIssues'

const at = '2026-10-14T08:00:00Z'
const request = (text: string, extra: Partial<BrowserRecorderAnnotation> = {}): BrowserRecorderAnnotation => ({ kind: 'request_failed', text, timestamp: at, ...extra })

describe('summarizeRecordingIssues', () => {
  it('groups by host and error, the recorded site first, and folds cancelled requests', () => {
    const summary = summarizeRecordingIssues([
      request('POST https://beacon.telemetry.test/ctp — net::ERR_ABORTED', { url: 'https://beacon.telemetry.test/ctp' }),
      request('GET https://cdn.vidyard.test/p.js — net::ERR_ABORTED', { url: 'https://cdn.vidyard.test/p.js' }),
      request('POST https://beacon.telemetry.test/ctp — net::ERR_ABORTED', { url: 'https://beacon.telemetry.test/ctp' }),
      request('403 https://beacon.telemetry.test/ctp', { url: 'https://beacon.telemetry.test/ctp', status: 403 }),
      request('403 https://beacon.telemetry.test/v2', { url: 'https://beacon.telemetry.test/v2', status: 403 }),
      request('404 https://www.shop.example.co.uk/missing.png', { url: 'https://www.shop.example.co.uk/missing.png', status: 404 }),
      { kind: 'console_error', text: 'Uncaught TypeError: x is undefined', url: 'https://www.shop.example.co.uk/', timestamp: at },
      // No url: the first URL in the text names the host.
      request('GET https://api.shop.example.co.uk/cart — net::ERR_CONNECTION_RESET'),
    ], 'https://www.shop.example.co.uk/start')
    expect(summary.cancelled).toEqual({ count: 3, hosts: ['beacon.telemetry.test', 'cdn.vidyard.test'] })
    expect(summary.groups.map(group => [group.firstParty, group.kind, group.host, group.error, group.count])).toEqual([
      [true, 'request', 'www.shop.example.co.uk', 'HTTP 404', 1],
      [true, 'console', 'www.shop.example.co.uk', 'console error', 1],
      [true, 'request', 'api.shop.example.co.uk', 'net::ERR_CONNECTION_RESET', 1],
      [false, 'request', 'beacon.telemetry.test', 'HTTP 403', 2],
    ])
    expect(summary.groups[3].sample).toBe('403 https://beacon.telemetry.test/v2')
  })

  it('lists different console messages on one page separately', () => {
    const page = 'https://www.shop.example/'
    const summary = summarizeRecordingIssues([
      { kind: 'console_error', text: 'Uncaught TypeError in checkout.js', url: page, timestamp: at },
      { kind: 'console_error', text: 'Third-party widget failed', url: page, timestamp: at },
      { kind: 'console_error', text: 'Third-party widget failed', url: page, timestamp: at },
    ], page)
    expect(summary.groups.map(group => [group.sample, group.count])).toEqual([
      ['Third-party widget failed', 2],
      ['Uncaught TypeError in checkout.js', 1],
    ])
  })

  it('leaves out the console echo of a failed request', () => {
    const page = 'https://www.shop.example/'
    const summary = summarizeRecordingIssues([
      request('404 https://www.shop.example/missing', { url: 'https://www.shop.example/missing', status: 404 }),
      { kind: 'console_error', text: 'Failed to load resource: the server responded with a status of 404 (Not Found)', url: page, timestamp: at },
    ], page)
    expect(summary.groups.map(group => group.kind)).toEqual(['request'])
  })

  it('treats issues it cannot place as the page own', () => {
    const summary = summarizeRecordingIssues([request('something failed')], 'not a url')
    expect(summary.groups).toHaveLength(1)
    expect(summary.groups[0]).toMatchObject({ host: '', error: 'request failed', firstParty: true })
  })
})

describe('siteOf', () => {
  it('keeps the registrable part of a host', () => {
    expect(siteOf('a.b.salesforce.com')).toBe('salesforce.com')
    expect(siteOf('www.shop.example.co.uk')).toBe('example.co.uk')
    expect(siteOf('localhost:8080')).toBe('localhost')
    // A short name under a country code is a site of its own.
    expect(siteOf('api.web.de')).toBe('web.de')
    expect(siteOf('www.shop.example.com.au')).toBe('example.com.au')
    expect(siteOf('127.0.0.1:8080')).toBe('127.0.0.1')
  })
})
