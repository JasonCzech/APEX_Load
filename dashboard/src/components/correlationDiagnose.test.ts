import { describe, expect, it } from 'vitest'

import { harToSteps, replayEventsToSteps } from './correlationDiagnose'
import type { ScriptReplayActionEvent } from '../types/scripts'

describe('replayEventsToSteps', () => {
  it('builds steps keyed by action index from event snapshots, skipping non-action phases', () => {
    const events = [
      { phase: 'init', action_index: 0, action_name: 'setup', action_type: 'x', target: '', started_at: '', completed_at: '', duration_ms: 0, success: true },
      {
        phase: 'action', action_index: 0, action_name: 'login', action_type: 'POST', target: '/login',
        started_at: '', completed_at: '', duration_ms: 1, success: true,
        snapshot: { request_url: '/login', request_body: 'u=a', response_body: '{"t":"1"}', response_headers: { 'Set-Cookie': 'x' } },
      },
      {
        phase: 'action', action_index: 1, action_name: 'orders', action_type: 'GET', target: '/orders',
        started_at: '', completed_at: '', duration_ms: 1, success: true,
        headers: { 'X-Token': '1' },
      },
    ] as ScriptReplayActionEvent[]

    const steps = replayEventsToSteps(events)
    expect(steps.map(s => s.id)).toEqual([0, 1])
    expect(steps[0].request_url).toBe('/login')
    expect(steps[0].response_body).toBe('{"t":"1"}')
    // Falls back to event-level request fields when no snapshot.
    expect(steps[1].request_headers).toEqual({ 'X-Token': '1' })
    expect(steps[1].request_url).toBe('/orders')
  })

  it('handles undefined events', () => {
    expect(replayEventsToSteps(undefined)).toEqual([])
  })
})

describe('harToSteps', () => {
  const har = {
    log: {
      entries: [
        { _resourceType: 'stylesheet', request: { url: '/app.css' }, response: { content: { mimeType: 'text/css', text: '...' } } },
        {
          request: { url: '/login', postData: { text: 'u=a' }, headers: [{ name: 'Content-Type', value: 'application/json' }] },
          response: { content: { mimeType: 'application/json', text: '{"t":"1"}' }, headers: [{ name: 'Set-Cookie', value: 's=1' }] },
        },
        { request: { url: '/logo.png' }, response: { content: { mimeType: 'image/png' } } },
        { request: { url: '/orders' }, response: { content: { mimeType: 'application/json', text: '[]' } } },
      ],
    },
  }

  it('parses meaningful exchanges and skips static assets, re-indexing ids', () => {
    const steps = harToSteps(har)
    expect(steps.map(s => s.request_url)).toEqual(['/login', '/orders'])
    expect(steps.map(s => s.id)).toEqual([0, 1])
    expect(steps[0].request_body).toBe('u=a')
    expect(steps[0].request_headers).toEqual({ 'Content-Type': 'application/json' })
    expect(steps[0].response_body).toBe('{"t":"1"}')
  })

  it('returns [] for a non-HAR object', () => {
    expect(harToSteps({})).toEqual([])
    expect(harToSteps(null)).toEqual([])
  })
})
