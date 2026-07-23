import { describe, expect, it } from 'vitest'

import { duplicateCorrelationStepIds, HarToStepsLimitError, harToSteps, nestedCorrelationStepIds, replayEventsToSteps } from './correlationDiagnose'
import type { ScriptReplayActionEvent } from '../types/scripts'

describe('replayEventsToSteps', () => {
  it('builds steps keyed by action index from event snapshots, skipping non-action phases', () => {
    const events = [
      { phase: 'init', action_index: 0, action_name: 'setup', action_type: 'x', target: '', started_at: '', completed_at: '', duration_ms: 0, success: true },
      {
        phase: 'action', action_index: 0, action_name: 'login', action_type: 'POST', target: '/login',
        started_at: '', completed_at: '', duration_ms: 1, status_code: 201, success: true,
        snapshot: { request_url: '/login', request_body: 'u=a', response_status: 202, response_body: '{"t":"1"}', response_headers: { 'Set-Cookie': 'x' } },
      },
      {
        phase: 'action', action_index: 1, action_name: 'orders', action_type: 'GET', target: '/orders',
        started_at: '', completed_at: '', duration_ms: 1, status_code: 204, success: true,
        headers: { 'X-Token': '1' },
      },
    ] as ScriptReplayActionEvent[]

    const steps = replayEventsToSteps(events)
    expect(steps.map(s => s.id)).toEqual([0, 1])
    expect(steps[0].request_url).toBe('/login')
    expect(steps[0].response_status).toBe(202)
    expect(steps[0].response_success).toBe(true)
    expect(steps[0].response_body).toBe('{"t":"1"}')
    // Falls back to event-level request fields when no snapshot.
    expect(steps[1].request_headers).toEqual({ 'X-Token': '1' })
    expect(steps[1].request_url).toBe('/orders')
    expect(steps[1].response_status).toBe(204)
  })

  it('handles undefined events', () => {
    expect(replayEventsToSteps(undefined)).toEqual([])
  })

  it('reports ambiguous action IDs emitted by sequence or loop leaves', () => {
    const steps = replayEventsToSteps([
      { phase: 'action', action_index: 2, action_name: 'sequence request 1', action_type: 'GET', target: '/one', started_at: '', completed_at: '', duration_ms: 1, success: true },
      { phase: 'action', action_index: 2, action_name: 'sequence request 2', action_type: 'GET', target: '/two', started_at: '', completed_at: '', duration_ms: 1, success: true },
      { phase: 'action', action_index: 3, action_name: 'loop request iteration 1', action_type: 'GET', target: '/three', started_at: '', completed_at: '', duration_ms: 1, success: true },
      { phase: 'action', action_index: 3, action_name: 'loop request iteration 2', action_type: 'GET', target: '/three', started_at: '', completed_at: '', duration_ms: 1, success: true },
    ] as ScriptReplayActionEvent[])

    expect(duplicateCorrelationStepIds(steps)).toEqual([2, 3])
  })

  it('reports even one nested leaf as unsafe for top-level correlation mapping', () => {
    const steps = replayEventsToSteps([{
      phase: 'action', action_index: 2, action_name: 'sequence request', action_type: 'GET', target: '/one',
      started_at: '', completed_at: '', duration_ms: 1, success: false,
      parent_action_name: 'sequence', request_index: 0, step_path: ['requests[0]'],
    }] as ScriptReplayActionEvent[])

    expect(steps[0].step_path).toEqual(['requests[0]'])
    expect(steps[0].response_success).toBe(false)
    expect(nestedCorrelationStepIds(steps)).toEqual([2])
    expect(duplicateCorrelationStepIds(steps)).toEqual([])
  })
})

describe('harToSteps', () => {
  const har = {
    log: {
      entries: [
        { _resourceType: 'stylesheet', request: { method: 'GET', url: '/app.css' }, response: { content: { mimeType: 'text/css', text: '...' } } },
        {
          request: { method: 'POST', url: '/login', postData: { text: 'Y3NyZj1PTEQ=', encoding: 'base64' }, headers: [{ name: 'Content-Type', value: 'application/json' }] },
          response: { status: 201, content: { mimeType: 'application/json', text: 'eyJ0IjoiMSJ9', encoding: 'base64' }, headers: [{ name: 'Set-Cookie', value: 's=1' }] },
        },
        { request: { method: 'GET', url: '/logo.png' }, response: { content: { mimeType: 'image/png' } } },
        { request: { method: 'POST', url: '/upload-image', postData: { text: 'preview-token' } }, response: { status: 202, content: { mimeType: 'image/png', text: 'preview-token' } } },
        { request: { method: 'GET', url: '/orders' }, response: { status: 200, content: { mimeType: 'application/json', text: '[]' } } },
      ],
    },
  }

  it('parses meaningful exchanges and skips static assets, re-indexing ids', () => {
    const steps = harToSteps(har)
    expect(steps.map(s => s.request_url)).toEqual(['/login', '/upload-image', '/orders'])
    expect(steps.map(s => s.id)).toEqual([0, 1, 2])
    expect(steps[0].request_body).toBe('csrf=OLD')
    expect(steps[0].request_headers).toEqual({ 'Content-Type': 'application/json' })
    expect(steps[0].response_body).toBe('{"t":"1"}')
    expect(steps[0].response_status).toBe(201)
    expect(steps[1].response_status).toBe(202)
  })

  it('returns [] for a non-HAR object', () => {
    expect(harToSteps({})).toEqual([])
    expect(harToSteps(null)).toEqual([])
  })

  it('preserves repeated HAR headers without corrupting Set-Cookie values', () => {
    const steps = harToSteps({
      log: {
        entries: [{
          request: {
            method: 'GET',
            url: '/login',
            headers: [{ name: 'X-Trace', value: 'one' }, { name: 'x-trace', value: 'two' }],
          },
          response: {
            status: 200,
            headers: [
              { name: 'Set-Cookie', value: 'sid=first; Expires=Wed, 01 Jan 2030 00:00:00 GMT' },
              { name: 'set-cookie', value: 'route=second; Path=/' },
            ],
          },
        }],
      },
    })

    expect(steps[0].request_headers).toEqual({ 'X-Trace': 'one\ntwo' })
    expect(steps[0].response_headers?.['Set-Cookie']).toBe(
      'sid=first; Expires=Wed, 01 Jan 2030 00:00:00 GMT\nroute=second; Path=/',
    )
  })

  it('rejects compact HARs that exceed entry or decoded-content limits', () => {
    expect(() => harToSteps({
      log: { entries: [{ request: { url: '/one' } }, { request: { url: '/two' } }] },
    }, { maxEntries: 1 })).toThrow(HarToStepsLimitError)

    expect(() => harToSteps({
      log: { entries: [{ request: { url: '/large' }, response: { content: { text: '123456' } } }] },
    }, { maxDecodedChars: 5 })).toThrow(HarToStepsLimitError)
  })
})
