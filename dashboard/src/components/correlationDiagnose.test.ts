import { describe, expect, it } from 'vitest'

import {
  diagnoseCorrelations,
  duplicateCorrelationStepIds,
  eventStep,
  formatStepLocation,
  HarToStepsLimitError,
  harToSteps,
  nestedCorrelationStepIds,
  replayEventsToSteps,
  resolveStepTarget,
  sameStepRef,
  stepRefForCorrelationStep,
  unmappedNestedCorrelationStepIds,
  walkScriptSteps,
} from './correlationDiagnose'
import type { ScriptDefinition, ScriptReplayActionEvent } from '../types/scripts'

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

  it('includes init and teardown HTTP events and maps step references across all 3 phases', () => {
    const events = [
      {
        phase: 'init', action_index: 0, action_name: 'vuser_init login', action_type: 'POST', target: '/api/login',
        started_at: '', completed_at: '', duration_ms: 1, status_code: 200, success: true,
        snapshot: { request_url: '/api/login', response_status: 200, response_body: '{"auth_token":"tok-1"}' },
      },
      {
        phase: 'action', action_index: 0, action_name: 'fetch orders', action_type: 'GET', target: '/api/orders',
        started_at: '', completed_at: '', duration_ms: 1, status_code: 200, success: true,
      },
      {
        phase: 'teardown', action_index: 0, action_name: 'vuser_end logout', action_type: 'POST', target: '/api/logout',
        started_at: '', completed_at: '', duration_ms: 1, status_code: 204, success: true,
      },
    ] as ScriptReplayActionEvent[]

    const steps = replayEventsToSteps(events)
    expect(steps.map(s => s.id)).toEqual([0, 1, 2])
    expect(duplicateCorrelationStepIds(steps)).toEqual([])
    expect(stepRefForCorrelationStep(steps[0])).toEqual({ phase: 'init', actionIndex: 0 })
    expect(stepRefForCorrelationStep(steps[1])).toEqual({ phase: 'action', actionIndex: 0 })
    expect(stepRefForCorrelationStep(steps[2])).toEqual({ phase: 'teardown', actionIndex: 0 })
    // Ensure non-enumerable step metadata is omitted from wire JSON payloads.
    expect(JSON.parse(JSON.stringify(steps[0]))).not.toHaveProperty('step')
    expect(JSON.parse(JSON.stringify(steps[0]))).not.toHaveProperty('stepLocationLabel')
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
    expect(unmappedNestedCorrelationStepIds(steps)).toEqual([])
    expect(duplicateCorrelationStepIds(steps)).toEqual([])
  })
})

describe('step reference helpers and diagnoseCorrelations', () => {
  const sampleScript: Pick<ScriptDefinition, 'init' | 'actions' | 'teardown'> = {
    init: [
      {
        name: 'vuser_init login',
        type: 'POST',
        method: 'POST',
        target: '/api/login',
        body: '{"user":"alice"}',
      },
    ],
    actions: [
      {
        name: 'conditional checkout',
        type: 'control_if',
        target: '',
        control: {
          steps: [
            {
              name: 'create cart',
              type: 'POST',
              method: 'POST',
              target: '/api/cart',
              headers: { Authorization: 'Bearer tok-init-999' },
            },
          ],
          else: [
            {
              name: 'fallback cart',
              type: 'GET',
              method: 'GET',
              target: '/api/cart/fallback',
            },
          ],
        },
      },
      {
        name: 'concurrent bundle',
        type: 'http_sequence',
        target: '',
        params: {
          requests: [
            {
              name: 'load csrf',
              type: 'GET',
              method: 'GET',
              target: '/api/csrf',
              extractors: [
                { name: 'existing_seq_csrf', source: 'body', type: 'jsonpath', expression: '$.csrf' },
              ],
            },
            {
              name: 'submit order',
              type: 'POST',
              method: 'POST',
              target: '/api/order',
              body: 'csrf={{var.missing_order_token}}',
            },
          ],
        },
      },
    ],
    teardown: [
      {
        name: 'vuser_end logout',
        type: 'POST',
        method: 'POST',
        target: '/api/logout',
        headers: { Authorization: 'Bearer tok-init-999' },
      },
    ],
  }

  it('formats step locations and compares step references across phases and nested paths', () => {
    expect(formatStepLocation({ phase: 'action', actionIndex: 0 })).toBe('#1')
    expect(formatStepLocation({ phase: 'init', actionIndex: 0 })).toBe('Init #1')
    expect(formatStepLocation({ phase: 'teardown', actionIndex: 1 })).toBe('Teardown #2')
    expect(formatStepLocation({ phase: 'action', actionIndex: 0, stepPath: ['then[0]'] })).toBe('#1 · Then #1')
    expect(formatStepLocation({ phase: 'action', actionIndex: 1, stepPath: ['requests[1]'] })).toBe('#2 · Request 2')
    expect(formatStepLocation({ phase: 'action', actionIndex: 0, stepPath: ['case[0][1]'] })).toBe('#1 · Case 1 #2')
    expect(formatStepLocation({ phase: 'action', actionIndex: 0, stepPath: ['branches[1].steps[0]'] })).toBe('#1 · Branch 2 #1')

    expect(sameStepRef(
      { phase: 'init', actionIndex: 0, stepPath: ['then[0]'] },
      { phase: 'init', actionIndex: 0, stepPath: ['then[0]'] },
    )).toBe(true)
    expect(sameStepRef(
      { phase: 'init', actionIndex: 0 },
      { phase: 'action', actionIndex: 0 },
    )).toBe(false)

    expect(eventStep({
      phase: 'init',
      action_index: 0,
      request_index: 2,
    })).toEqual({
      phase: 'init',
      actionIndex: 0,
      stepPath: ['requests[2]'],
    })
  })

  it('resolves action and sequence_request targets and reads extractors from both', () => {
    const initTarget = resolveStepTarget(sampleScript, { phase: 'init', actionIndex: 0 })
    expect(initTarget?.kind).toBe('action')
    expect(initTarget?.action.name).toBe('vuser_init login')

    const ifThenTarget = resolveStepTarget(sampleScript, { phase: 'action', actionIndex: 0, stepPath: ['then[0]'] })
    expect(ifThenTarget?.kind).toBe('action')
    expect(ifThenTarget?.action.name).toBe('create cart')

    const seqReqTarget = resolveStepTarget(sampleScript, { phase: 'action', actionIndex: 1, stepPath: ['requests[0]'] })
    expect(seqReqTarget?.kind).toBe('sequence_request')
    if (seqReqTarget?.kind === 'sequence_request') {
      expect(seqReqTarget.request.name).toBe('load csrf')
      expect(seqReqTarget.extractors).toEqual([
        { name: 'existing_seq_csrf', source: 'body', type: 'jsonpath', expression: '$.csrf' },
      ])
    }

    const walked = walkScriptSteps(sampleScript)
    expect(walked.map(item => formatStepLocation(item.step))).toEqual([
      'Init #1',
      '#1',
      '#1 · Then #1',
      '#1 · Else #1',
      '#2',
      '#2 · Request 1',
      '#2 · Request 2',
      'Teardown #1',
    ])
  })

  it('diagnoses init-phase token extraction, nested control_if / http_sequence steps, and teardown events', () => {
    const replayEvents: ScriptReplayActionEvent[] = [
      {
        phase: 'init',
        action_index: 0,
        action_name: 'vuser_init login',
        action_type: 'POST',
        target: '/api/login',
        started_at: '',
        completed_at: '',
        duration_ms: 5,
        status_code: 200,
        success: true,
        snapshot: {
          request_url: '/api/login',
          response_status: 200,
          response_body: '{"auth_token":"tok-init-999"}',
        },
      },
      {
        phase: 'action',
        action_index: 0,
        action_name: 'create cart',
        action_type: 'POST',
        target: '/api/cart',
        step_path: ['then[0]'],
        started_at: '',
        completed_at: '',
        duration_ms: 4,
        status_code: 401,
        success: false,
        snapshot: {
          request_url: '/api/cart',
          request_headers: { Authorization: 'Bearer tok-init-999' },
          response_status: 401,
        },
      },
      {
        phase: 'action',
        action_index: 1,
        action_name: 'load csrf',
        action_type: 'GET',
        target: '/api/csrf',
        request_index: 0,
        step_path: ['requests[0]'],
        started_at: '',
        completed_at: '',
        duration_ms: 3,
        status_code: 200,
        success: true,
        snapshot: {
          request_url: '/api/csrf',
          response_status: 200,
          response_body: '{"csrf":"c-1","order_token":"ord-777"}',
        },
      },
      {
        phase: 'action',
        action_index: 1,
        action_name: 'submit order',
        action_type: 'POST',
        target: '/api/order',
        request_index: 1,
        step_path: ['requests[1]'],
        started_at: '',
        completed_at: '',
        duration_ms: 4,
        status_code: 403,
        success: false,
        snapshot: {
          request_url: '/api/order',
          request_body: 'csrf=ord-777',
          response_status: 403,
        },
      },
      {
        phase: 'teardown',
        action_index: 0,
        action_name: 'vuser_end logout',
        action_type: 'POST',
        target: '/api/logout',
        started_at: '',
        completed_at: '',
        duration_ms: 2,
        status_code: 204,
        success: true,
        snapshot: {
          request_url: '/api/logout',
          request_headers: { Authorization: 'Bearer tok-init-999' },
          response_status: 204,
        },
      },
    ]

    const rows = diagnoseCorrelations(sampleScript, replayEvents)
    expect(rows).toHaveLength(5)
    expect(rows.map(r => r.stepLocationLabel)).toEqual([
      'Init #1',
      '#1 · Then #1',
      '#2 · Request 1',
      '#2 · Request 2',
      'Teardown #1',
    ])

    // Row 1 (#1 · Then #1) should suggest extracting auth_token from Init #1.
    expect(rows[1].step).toEqual({ phase: 'action', actionIndex: 0, stepPath: ['then[0]'] })
    expect(rows[1].suggestedFixes).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          actionIndex: 0,
          step: { phase: 'init', actionIndex: 0 },
          stepLocationLabel: 'Init #1',
          variableName: 'auth_token',
          extractor: expect.objectContaining({
            name: 'auth_token',
            source: 'body',
            type: 'jsonpath',
            expression: '$.auth_token',
          }),
        }),
      ]),
    )

    // Row 2 (#2 · Request 1) reads existing extractors from sequence_request.request.extractors.
    expect(rows[2].extractedVariables).toContain('existing_seq_csrf')

    // Row 3 (#2 · Request 2) detects missing_order_token / order_token from #2 · Request 1.
    expect(rows[3].unresolvedVariables).toContain('missing_order_token')
    expect(rows[3].suggestedFixes).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          actionIndex: 1,
          step: { phase: 'action', actionIndex: 1, stepPath: ['requests[0]'] },
          stepLocationLabel: '#2 · Request 1',
        }),
      ]),
    )

    // Row 4 (Teardown #1) also links back to Init #1 token producer.
    expect(rows[4].step).toEqual({ phase: 'teardown', actionIndex: 0 })
    expect(rows[4].stepLocationLabel).toBe('Teardown #1')
  })

  it('falls back to URL and HTTP method matching when step_path is omitted on nested container events', () => {
    const rows = diagnoseCorrelations(sampleScript, [
      {
        phase: 'action',
        action_index: 0,
        action_name: 'create cart',
        action_type: 'POST',
        method: 'POST',
        target: '/api/cart',
        started_at: '',
        completed_at: '',
        duration_ms: 2,
        status_code: 200,
        success: true,
      },
    ])

    expect(rows).toHaveLength(1)
    expect(rows[0].step).toEqual({ phase: 'action', actionIndex: 0, stepPath: ['then[0]'] })
    expect(rows[0].stepLocationLabel).toBe('#1 · Then #1')
  })

  it('preserves action_index IDs for multiple nested requests under one action-phase step', () => {
    const steps = replayEventsToSteps([
      {
        phase: 'action',
        action_index: 2,
        action_name: 'sequence request 1',
        action_type: 'GET',
        target: '/one',
        started_at: '',
        completed_at: '',
        duration_ms: 1,
        success: true,
        parent_action_name: 'sequence',
        request_index: 0,
        step_path: ['requests[0]'],
      },
      {
        phase: 'action',
        action_index: 2,
        action_name: 'sequence request 2',
        action_type: 'GET',
        target: '/two',
        started_at: '',
        completed_at: '',
        duration_ms: 1,
        success: true,
        parent_action_name: 'sequence',
        request_index: 1,
        step_path: ['requests[1]'],
      },
    ] as ScriptReplayActionEvent[])

    expect(steps.map(s => s.id)).toEqual([2, 2])
    expect(duplicateCorrelationStepIds(steps)).toEqual([2])
    expect(nestedCorrelationStepIds(steps)).toEqual([2])
  })

  it('preserves raw array indices when non-record entries appear in requests or control steps', () => {
    const sparseScript = {
      actions: [
        {
          name: 'sparse sequence',
          type: 'http_sequence',
          target: '',
          params: {
            requests: [
              null,
              { name: 'second request', type: 'GET', method: 'GET', target: '/second' },
            ],
          },
        },
        {
          name: 'sparse if',
          type: 'control_if',
          target: '',
          control: {
            steps: [
              null as unknown as ScriptDefinition['actions'][number],
              { name: 'second then step', type: 'POST', method: 'POST', target: '/then-2' },
            ],
          },
        },
      ],
    } as Pick<ScriptDefinition, 'init' | 'actions' | 'teardown'>

    expect(resolveStepTarget(sparseScript, { phase: 'action', actionIndex: 0, stepPath: ['requests[0]'] })).toBeNull()
    const resolvedReq = resolveStepTarget(sparseScript, { phase: 'action', actionIndex: 0, stepPath: ['requests[1]'] })
    expect(resolvedReq?.kind).toBe('sequence_request')
    if (resolvedReq?.kind === 'sequence_request') {
      expect(resolvedReq.request.name).toBe('second request')
      expect(resolvedReq.requestIndex).toBe(1)
    }

    expect(resolveStepTarget(sparseScript, { phase: 'action', actionIndex: 1, stepPath: ['then[0]'] })).toBeNull()
    const resolvedThen = resolveStepTarget(sparseScript, { phase: 'action', actionIndex: 1, stepPath: ['then[1]'] })
    expect(resolvedThen?.kind).toBe('action')
    expect(resolvedThen?.action.name).toBe('second then step')
  })

  it('escapes regex metacharacters in HTML input/cookie extractors and inspects authored header_list', () => {
    const script: Pick<ScriptDefinition, 'init' | 'actions' | 'teardown'> = {
      init: [
        {
          name: 'csrf page',
          type: 'GET',
          method: 'GET',
          target: '/form',
        },
      ],
      actions: [
        {
          name: 'submit with header_list',
          type: 'POST',
          method: 'POST',
          target: '/submit',
          header_list: [
            { name: 'X-CSRF-Token', value: 'tok-meta-123', enabled: true },
            { name: 'X-Cookie-Check', value: 'sid-meta-456', enabled: true },
          ],
        },
      ],
    }
    const rows = diagnoseCorrelations(script, [
      {
        phase: 'init',
        action_index: 0,
        action_name: 'csrf page',
        action_type: 'GET',
        target: '/form',
        started_at: '',
        completed_at: '',
        duration_ms: 1,
        status_code: 200,
        success: true,
        snapshot: {
          request_url: '/form',
          response_status: 200,
          response_body: '<input type="hidden" name="user[csrf]" value="tok-meta-123">',
          response_headers: {
            'Set-Cookie': '__Host.sid$1=sid-meta-456; Path=/; HttpOnly',
          },
        },
      },
      {
        phase: 'action',
        action_index: 0,
        action_name: 'submit with header_list',
        action_type: 'POST',
        target: '/submit',
        started_at: '',
        completed_at: '',
        duration_ms: 1,
        status_code: 200,
        success: true,
      },
    ])

    expect(rows).toHaveLength(2)
    expect(rows[1].suggestedFixes).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          variableName: 'user_csrf',
          extractor: expect.objectContaining({
            source: 'body',
            type: 'regex',
            expression: 'name=["\']user\\[csrf\\]["\'][^>]*value=["\']([^"\']+)["\']',
          }),
        }),
        expect.objectContaining({
          variableName: 'host_sid_1',
          extractor: expect.objectContaining({
            source: 'header',
            type: 'regex',
            expression: '__Host\\.sid\\$1=([^;\\s]+)',
          }),
        }),
      ]),
    )
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
