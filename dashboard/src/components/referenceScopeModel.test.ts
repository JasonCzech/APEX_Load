import { describe, expect, it } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import { buildReferenceScope, stepOutputs, stepsBefore, templateTokens, unresolvedReferences, type InsertionPoint, type ReferenceScope } from './referenceScopeModel'

const step = (name: string, extra: Record<string, unknown> = {}) => ({ name, type: 'GET', target: '/', ...extra })
const extract = (name: string) => ({ extractors: [{ name, type: 'json', expression: '$.id' }] })
const script = (raw: Record<string, unknown>) => ({ name: 's', protocol: 'http', config: {}, actions: [], ...raw }) as unknown as ScriptDefinition
const expressions = (scope: ReferenceScope, group?: string) => scope.items.filter(item => !group || item.group === group).map(item => `${item.expression}${item.mayBeUnset ? '?' : ''}`)

describe('templateTokens', () => {
  it('finds each reference with its root and place', () => {
    expect(templateTokens('/orders/{{ var.id }}?u={{param.user}}')).toEqual([
      { text: '{{ var.id }}', expression: 'var.id', root: 'var', from: 8, to: 20 },
      { text: '{{param.user}}', expression: 'param.user', root: 'param', from: 23, to: 37 },
    ])
  })
})

describe('buildReferenceScope', () => {
  const subject = script({
    parameters: [{ name: 'user', type: 'list', values: ['alice', 'bob'] }],
    data_sources: [{ name: 'cards', type: 'csv', columns: ['number', 'cvv'] }],
    config: { environment: { BASE: 'x' } },
    init: [step('Login', extract('token'))],
    actions: [
      step('Search', extract('order_id')),
      { name: 'Maybe', type: 'control_if', target: '', control: { condition: 'x', steps: [step('Then', extract('coupon'))], else: [step('Else', extract('fallback'))] } },
      { name: 'Lanes', type: 'control_parallel', target: '', control: { branches: [{ name: 'a', steps: [step('A1', extract('lane_a')), step('A2')] }, { name: 'b', steps: [step('B1', extract('lane_b'))] }] } },
      step('Pay', { headers: { Authorization: 'Bearer {{secret.API_KEY}}' } }),
      step('Later', extract('late')),
    ],
  })

  it('offers parameters, data, earlier outputs, environment, secrets and functions before a step', () => {
    const scope = buildReferenceScope(subject, { phase: 'action', actionIndex: 3 })
    expect(expressions(scope, 'parameters')).toEqual(['param.user'])
    expect(scope.items[0].detail).toBe('list parameter · e.g. alice')
    expect(expressions(scope, 'data')).toEqual(['data.cards.number', 'data.cards.cvv'])
    // A value set inside an if may be unset after it; parallel lanes have all run by then.
    expect(expressions(scope, 'steps')).toEqual(['var.token', 'var.order_id', 'var.coupon?', 'var.fallback?', 'var.lane_a', 'var.lane_b'])
    expect(expressions(scope, 'environment')).toEqual(['env.BASE', 'run.id'])
    expect(expressions(scope, 'secrets')).toEqual(['secret.API_KEY'])
    expect(expressions(scope, 'functions')).toContain('fn.uuid')
    expect(scope.byExpression.get('var.token')?.source).toEqual({ phase: 'init', actionIndex: 0, name: 'Login' })
  })

  it('leaves out later steps, the other branch and sibling lanes', () => {
    expect(expressions(buildReferenceScope(subject, { phase: 'init', actionIndex: 0 }), 'steps')).toEqual([])
    // Inside the else branch, the then branch never ran.
    expect(expressions(buildReferenceScope(subject, { phase: 'action', actionIndex: 1, stepPath: ['else[0]'] }), 'steps')).toEqual(['var.token', 'var.order_id'])
    // In lane a, lane b's values are not there; the earlier step in the lane is.
    expect(expressions(buildReferenceScope(subject, { phase: 'action', actionIndex: 2, stepPath: ['branches[0].steps[1]'] }), 'steps')).toEqual(['var.token', 'var.order_id', 'var.coupon?', 'var.fallback?', 'var.lane_a'])
  })

  it('names a live table step’s outputs, columns included', () => {
    expect(stepOutputs({ name: 'Read', type: 'live_table', target: '', params: { alias: 'orders', operation: 'read', result_prefix: 'row', columns: ['status'] } } as never).map(output => output.expression)).toEqual([
      'var.row.status', 'var.row.revision', 'var.row.record_id', 'var.row.record_version', 'var.row.values.status',
    ])
    expect(stepOutputs({ name: 'Token', type: 'http_oauth2_token', target: '', params: {} } as never).map(output => output.expression)).toEqual(['var.access_token'])
  })

  it('counts what a sequence’s requests save as the sequence’s outputs', () => {
    const sequence = { name: 'Group', type: 'http_sequence', target: '', params: { requests: [{ name: 'one', method: 'GET', url: '/1' }, { name: 'two', method: 'GET', url: '/2', extractors: [{ name: 'token', type: 'regex', expression: 't=(\\w+)' }] }] } }
    expect(stepOutputs(sequence as never).map(output => `${output.expression}: ${output.detail}`)).toEqual(['var.token: Saved by a request’s regex extractor'])
    const scope = buildReferenceScope(script({ actions: [sequence, step('Use')] }), { phase: 'action', actionIndex: 1 })
    expect(unresolvedReferences('{{var.token}}', scope)).toEqual([])
  })

  it('lists scenario-bound data aliases and project secrets it is given', () => {
    const scope = buildReferenceScope(script({ actions: [step('A')] }), { phase: 'action', actionIndex: 0 }, { secretNames: ['DB_PASS'], dataAliases: { work: ['id'] } })
    expect(expressions(scope, 'data')).toEqual(['data.work.id'])
    expect(scope.byExpression.get('data.work.id')?.detail).toBe('Column of work, bound by a scenario at run time')
    expect(expressions(scope, 'secrets')).toEqual(['secret.DB_PASS'])
    // An alias no saved scenario binds says so.
    const unbound = buildReferenceScope(script({ actions: [step('A')] }), { phase: 'action', actionIndex: 0 }, { dataAliases: { work: ['id'] }, unboundAliases: new Set(['work']) })
    expect(unbound.byExpression.get('data.work.id')?.detail).toBe('Column of work, no scenario binds it yet')
  })
})

describe('buildReferenceScope in a sequence request', () => {
  const sequence = (mode?: string) => script({
    actions: [
      step('Login', extract('sid')),
      { name: 'Batch', type: 'http_sequence', target: '', params: { ...(mode ? { execution_mode: mode } : {}), requests: [{ method: 'GET', target: '/a', ...extract('a') }, { method: 'GET', target: '/b', ...extract('b') }, { method: 'GET', target: '/c', ...extract('c') }] } },
      step('Later', extract('late')),
    ],
  })
  const at = { phase: 'action' as const, actionIndex: 1, stepPath: ['requests[1]'] }

  it('sees the steps before its sequence and, in order, the earlier requests', () => {
    const scope = buildReferenceScope(sequence(), at)
    expect(expressions(scope, 'steps')).toEqual(['var.sid', 'var.a'])
    expect(scope.byExpression.get('var.a')!.source).toMatchObject({ actionIndex: 1, stepPath: ['requests[0]'] })
    expect([...scope.laterVariables.keys()].sort()).toEqual(['var.c', 'var.late'])
  })

  it('sees none of a concurrent sequence’s other requests as earlier', () => {
    const scope = buildReferenceScope(sequence('concurrent'), at)
    expect(expressions(scope, 'steps')).toEqual(['var.sid'])
    expect([...scope.laterVariables.keys()].sort()).toEqual(['var.a', 'var.c', 'var.late'])
  })
})

describe('unresolvedReferences', () => {
  const subject = script({
    parameters: [{ name: 'user', type: 'list', values: ['a'] }],
    data_sources: [{ name: 'cards', type: 'csv', columns: ['number'] }],
    actions: [step('Search', extract('order_id')), step('Pay'), step('Later', extract('late'))],
  })
  const scope = buildReferenceScope(subject, { phase: 'action', actionIndex: 1 })

  it('flags a misspelled variable with close names, a later one, an unknown parameter and column', () => {
    const found = unresolvedReferences('{{var.ordr_id}} {{var.late}} {{param.usr}} {{data.cards.cvv}} {{oops.x}} {{var.order_id}} {{var.user}} {{env.X}} {{secret.K}} {{fn.uuid}}', scope)
    expect(found.map(item => item.message)).toEqual([
      '{{var.ordr_id}} is not set by an earlier step',
      '{{var.late}} is set only by a later step (Later)',
      '{{param.usr}} is not a parameter of this script',
      '{{data.cards.cvv}}: cards has no column cvv',
      '{{oops.x}} does not start with var, param, data, env, secret, file, run, fn or fake',
    ])
    expect(found[0].suggestions).toEqual(['var.order_id'])
    expect(found[0].name).toBe('ordr_id')
    expect(found[2].suggestions).toEqual(['param.user'])
  })
})

describe('stepsBefore', () => {
  const subject = script({
    init: [step('I')],
    actions: [step('A'), { name: 'If', type: 'control_if', target: '', control: { condition: { left: 'x', op: 'exists' }, steps: [step('T1'), step('T2')], else: [step('E1')] } }, step('B')],
    teardown: [step('Z')],
  })
  const names = (point: InsertionPoint) => stepsBefore(subject, point).map(placed => placed.action.name)

  it('counts the steps on the new step’s path, by where it goes', () => {
    expect(names({ phase: 'action', position: 'end' })).toEqual(['I', 'A', 'If', 'T1', 'T2', 'E1', 'B'])
    expect(names({ phase: 'init', position: 'end' })).toEqual(['I'])
    expect(names({ phase: 'action', position: 'before', anchor: { phase: 'action', actionIndex: 1, stepPath: ['then[1]'] } })).toEqual(['I', 'A', 'T1'])
    // After a step, that step and what is inside it count; another branch does not.
    expect(names({ phase: 'action', position: 'after', anchor: { phase: 'action', actionIndex: 1, stepPath: ['then[0]'] } })).toEqual(['I', 'A', 'T1'])
    expect(names({ phase: 'action', position: 'after', anchor: { phase: 'action', actionIndex: 1 } })).toEqual(['I', 'A', 'If', 'T1', 'T2', 'E1'])
    expect(names({ phase: 'action', position: 'inside', anchor: { phase: 'action', actionIndex: 1 } })).toEqual(['I', 'A', 'T1', 'T2'])
    expect(names({ phase: 'action', position: 'before', anchor: { phase: 'action', actionIndex: 0 } })).toEqual(['I'])
    // A sequence's request is not a step: after it means after the step it is in.
    expect(names({ phase: 'action', position: 'after', anchor: { phase: 'action', actionIndex: 0, stepPath: ['requests[1]'] } })).toEqual(['I', 'A'])
  })
})
