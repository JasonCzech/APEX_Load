import { describe, expect, it } from 'vitest'
import type { ScriptDefinition } from '../types/scripts'
import { advertisedOriginsFrom, observedSummary, requestOrigin, transportEdits, transportRows, transportSurfaces } from './importTransportModel'

describe('import transport model', () => {
  // The same cases as TestRequestOrigin (pkg/convert/requestimport), so the
  // panel and the server agree on every request's origin.
  it.each([
    ['https://App.Example.test', '/a', 'https://app.example.test'],
    ['https://app.example.test:443', '/a', 'https://app.example.test'],
    ['https://app.example.test:8443/base', 'b', 'https://app.example.test:8443'],
    ['https://app.example.test', 'http://other.example.test:80/x', 'http://other.example.test'],
    ['https://app.example.test', 'https://[::1]:443/x', 'https://[::1]'],
    ['https://{{var.host}}', '/a', ''],
    ['https://app.example.test', '{{var.url}}', ''],
    ['', '/relative', ''],
    ['https://app.example.test', 'ftp://files.example.test/x', ''],
    ['https://app.example.test', 'HTTPS://Other.example.test/x', 'https://app.example.test'],
    ['https://app.example.test/api', '//cdn.example.test/x', 'https://app.example.test'],
    ['https://user:secret@App.example.test', '/x', 'https://app.example.test'],
    ['https://Bücher.example', '/x', 'https://bücher.example'],
    ['https://app.example.test', '{{var.base}}/x', ''],
    ['http://app.example.test:80', '/x', 'http://app.example.test'],
  ])('derives the origin of %s + %s', (base, target, want) => {
    expect(requestOrigin(base, target)).toBe(want)
  })

  const script = {
    name: 'Transport', version: '1', protocol: 'http', config: { base_url: 'https://app.example.test' },
    actions: [
      { name: 'home', type: 'GET', method: 'GET', target: '/', http_version: 'auto+h3', metadata: { http_version_capture: { version: 1, request_state: 'available', response_state: 'available', request_version: '3' } } },
      { name: 'batch', type: 'http_sequence', target: '', params: { requests: [
        { name: 'child', type: 'GET', method: 'GET', target: '/child', metadata: { http_version_capture: { version: 1, request_state: 'available', response_state: 'available', request_version: '2' } } },
        { name: 'cdn', type: 'GET', method: 'GET', target: 'https://cdn.example.test/lib.js', http_version: '2' },
      ] } },
      { name: 'branch', type: 'control_if', target: '', control: { steps: [
        { name: 'race', type: 'GET', method: 'GET', target: '/race', metadata: { execution_blockers: ['curl_http3_race_not_modeled'], http_version_rewrite_proposal: { version: 1, policy: 'auto+h3' } } },
      ] } },
      { name: 'dynamic', type: 'GET', method: 'GET', target: '{{var.url}}' },
    ],
  } as unknown as ScriptDefinition

  it('walks nested request surfaces with server paths', () => {
    expect(transportSurfaces(script).map(surface => surface.path)).toEqual([
      'actions[0]', 'actions[1].params.requests[0]', 'actions[1].params.requests[1]', 'actions[2].control.steps[0]', 'actions[3]',
    ])
  })

  it('groups requests into origin rows with observations, policy and fidelity', () => {
    const rows = transportRows(script, advertisedOriginsFrom([{ code: 'origin_advertises_h3', location: 'https://app.example.test' }]))
    expect(rows.map(row => row.origin)).toEqual(['https://app.example.test', 'https://cdn.example.test', ''])
    const [app, cdn, dynamic] = rows
    expect(app.requests.map(request => request.path)).toEqual(['actions[0]', 'actions[1].params.requests[0]', 'actions[2].control.steps[0]'])
    expect(app).toMatchObject({ advertised: true, policy: null, fidelity: '1 request needs an explicit HTTP version' })
    expect(observedSummary(app.counts)).toBe('h2 1 · h3 1')
    expect(app.requests[2]).toMatchObject({ versionGates: ['curl_http3_race_not_modeled'], proposal: 'auto+h3' })
    expect(cdn).toMatchObject({ advertised: false, policy: '2', fidelity: 'No captured version' })
    expect(dynamic.origin).toBe('')
    expect(transportEdits(dynamic, 'auto+h3')).toEqual([])
  })

  it('edits every request of an origin that does not already have the policy', () => {
    const [app] = transportRows(script)
    expect(transportEdits(app, 'auto+h3')).toEqual([
      { path: 'actions[1].params.requests[0]', origin: 'https://app.example.test', from: '', to: 'auto+h3' },
      { path: 'actions[2].control.steps[0]', origin: 'https://app.example.test', from: '', to: 'auto+h3' },
    ])
    expect(transportEdits(app, '')).toEqual([{ path: 'actions[0]', origin: 'https://app.example.test', from: 'auto+h3', to: '' }])
  })
})
