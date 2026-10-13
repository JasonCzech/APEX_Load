import { describe, expect, it } from 'vitest'
import type { ScriptDefinition, ScriptReplayActionEvent } from '../types/scripts'
import { summarizeActionContext } from './scriptActionContext'

const script = {
  name: 'Checkout', version: '1', protocol: 'http', config: { base_url: 'https://shop.test' },
  init: [{ name: 'Login', type: 'request', method: 'POST', target: '/login', body: 'user={{param.user}}', extractors: [{ name: 'token', source: 'body', type: 'jsonpath', expression: '$.token' }] }],
  actions: [
    { name: 'Cart', type: 'request', method: 'GET', target: '/cart', headers: { Authorization: 'Bearer {{var.token}}' } },
    { name: 'Pay', type: 'request', method: 'POST', target: '/pay/{{ var.token }}', metadata: { source_body: '{{var.unused}}' } },
    { name: 'Browse', type: 'request', method: 'GET', target: '/browse' },
  ],
} as unknown as ScriptDefinition

const event = (patch: Partial<ScriptReplayActionEvent>) => ({ phase: 'action', action_name: '', action_type: 'request', target: '', started_at: '', completed_at: '', duration_ms: 10, success: true, ...patch }) as ScriptReplayActionEvent

describe('summarizeActionContext', () => {
  it('lists what an action reads, what it extracts and who reads that', () => {
    const login = summarizeActionContext(script, 'init', 0)!
    expect(login.uses).toEqual(['param.user'])
    expect(login.outputs.map(output => output.expression)).toEqual(['var.token'])
    expect(login.outputs[0].usedBy.map(location => `${location.phase}:${location.actionIndex}:${location.name}`)).toEqual(['action:0:Cart', 'action:1:Pay'])
  })

  it('ignores imported evidence in metadata', () => {
    expect(summarizeActionContext(script, 'action', 1)!.uses).toEqual(['var.token'])
  })

  it('reports the action\'s own last debug event, not its sub-requests', () => {
    const events = [
      event({ action_index: 0, status_code: 500, success: false, duration_ms: 40, error_message: 'boom' }),
      event({ action_index: 0, status_code: 200, duration_ms: 30 }),
      event({ action_index: 0, request_index: 1, status_code: 404, success: false }),
      event({ action_index: 0, step_path: ['steps[0]'], status_code: 418, success: false }),
    ]
    expect(summarizeActionContext(script, 'action', 0, events)!.lastDebug).toEqual({ success: true, statusCode: 200, durationMs: 30, error: undefined })
    expect(summarizeActionContext(script, 'action', 2, events)!.lastDebug).toBeNull()
  })

  it('counts a request nested in a block as a reader, and describes a selected nested step by itself', () => {
    const nestedReader = { name: 'Poll', type: 'request', method: 'GET', target: '/poll', headers: { Authorization: 'Bearer {{var.token}}' } }
    const withBlock = { ...script, actions: [...script.actions, { name: 'Loop', type: 'control_loop', target: '', control: { count: 2, steps: [nestedReader] } }] } as unknown as ScriptDefinition
    expect(summarizeActionContext(withBlock, 'init', 0)!.outputs[0].usedBy.map(location => location.name)).toContain('Loop')
    const step = summarizeActionContext(withBlock, 'action', 3, [event({ action_index: 3, status_code: 200 })], nestedReader as never)!
    expect(step.uses).toEqual(['var.token'])
    expect(step.outputs).toEqual([])
    expect(step.debugOfEnclosingAction).toBe(true)
    expect(step.lastDebug?.statusCode).toBe(200)
  })

  it('lists a live-table step’s results and who reads them', () => {
    const tables = { ...script, actions: [
      { name: 'Read', type: 'live_table', target: '', params: { alias: 'orders', operation: 'read', position: 1, result_prefix: 'row', columns: ['status'] } },
      { name: 'Use', type: 'request', method: 'GET', target: '/orders/{{var.row.record_id}}?s={{var.row.values.status}}' },
    ] } as unknown as ScriptDefinition
    const read = summarizeActionContext(tables, 'action', 0)!
    expect(read.outputs.map(output => `${output.expression}:${output.usedBy.map(location => location.name).join(',')}`)).toEqual([
      'var.row.status:', 'var.row.revision:', 'var.row.record_id:Use', 'var.row.record_version:', 'var.row.values.status:Use',
    ])
  })

  it('returns nothing for an action that no longer exists', () => {
    expect(summarizeActionContext(script, 'teardown', 0)).toBeNull()
  })
})
