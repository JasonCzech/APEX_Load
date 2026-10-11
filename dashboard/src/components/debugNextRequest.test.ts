import { describe, expect, it } from 'vitest'
import type { ScriptDebugAction } from '../types/replay'
import type { ScriptDefinition } from '../types/scripts'
import { debugNextAction, debugNextRequestParts, previewTemplate } from './debugNextRequest'

const at = (overrides: Partial<ScriptDebugAction>): ScriptDebugAction => ({ phase: 'action', action_index: 0, event_index: 0, action_name: '', action_type: 'request', ...overrides })

describe('previewTemplate', () => {
  it('fills in held values and marks what it cannot', () => {
    const segments = previewTemplate('/runs/{{run.id}}/cart/{{var.cart}}?u={{var.user}}&t={{secret.TOKEN}}&n={{fn.uuid()}}', { 'var.cart': 'c-9' }, 'debug-1')
    expect(segments.map(segment => [segment.kind, segment.text])).toEqual([
      ['text', '/runs/'], ['value', 'debug-1'], ['text', '/cart/'], ['value', 'c-9'], ['text', '?u='],
      ['unresolved', '{{var.user}}'], ['text', '&t='], ['secret', '{{secret.TOKEN}}'], ['text', '&n='], ['generated', '{{fn.uuid()}}'],
    ])
  })

  it('leaves run.id unresolved without a session identity, and text with no references whole', () => {
    expect(previewTemplate('{{ run.id }}', null)).toEqual([{ text: '{{ run.id }}', kind: 'unresolved' }])
    expect(previewTemplate('plain', null)).toEqual([{ text: 'plain', kind: 'text' }])
  })
})

describe('debugNextAction', () => {
  const script = {
    name: 's', version: '1', protocol: 'http', config: {},
    init: [{ name: 'login', type: 'request', method: 'POST', target: '/login' }],
    actions: [{ name: 'flow', type: 'http_sequence', params: { requests: [{ name: 'a', method: 'GET', target: '/a' }, { name: 'b', method: 'PUT', target: '/b', headers: { 'X-Run': '{{run.id}}' }, body: '{"id":"{{var.id}}"}' }] } }],
  } as unknown as ScriptDefinition

  it('finds the step by phase, and the sub-request a sequence pauses at', () => {
    expect(debugNextAction(script, at({ phase: 'init' }))?.name).toBe('login')
    const sub = debugNextAction(script, at({ request_index: 1 }))
    expect(sub?.name).toBe('b')
    expect(debugNextRequestParts(sub!)).toEqual({ target: '/b', headers: [['X-Run', '{{run.id}}']], body: '{"id":"{{var.id}}"}' })
    expect(debugNextAction(script, at({ action_index: 5 }))).toBeNull()
  })
})
