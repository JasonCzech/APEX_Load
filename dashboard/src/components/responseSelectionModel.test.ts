import { describe, expect, it } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import { evaluateExtractor, boundaryExpression } from './extractorMatch'
import { applyValueCorrelation, laterUses, stepLayoutKey, suggestBoundaries, suggestValueName, updateStep, repairLocations } from './responseSelectionModel'

const select = (body: string, text: string, from = 0) => { const start = body.indexOf(text, from); return [start, start + text.length] as const }

describe('boundaries around a selected value', () => {
  it('suggests the shortest boundaries that save exactly the selection', () => {
    const body = '{"id":"7","token":"abc123","user":"Ada"}'
    const bounds = suggestBoundaries(body, ...select(body, 'abc123'))!
    expect(bounds).toEqual({ left: 'n":"', right: '"' })
    expect(evaluateExtractor('boundary', boundaryExpression(bounds.left, bounds.right), body).matches[0]).toBe('abc123')
  })

  it('lengthens a left boundary until its first occurrence ends at the value', () => {
    const body = 'a=1&b=2&c=xyz&d=4'
    const bounds = suggestBoundaries(body, ...select(body, 'xyz'))!
    expect(bounds.left).toBe('c=')
    expect(evaluateExtractor('boundary', boundaryExpression(bounds.left, bounds.right), body).matches[0]).toBe('xyz')
  })

  it('takes the rest of the body when the value ends it, unless the replay kept only part of it', () => {
    const body = 'session:s-99'
    expect(suggestBoundaries(body, ...select(body, 's-99'))).toEqual({ left: ':', right: '' })
    expect(suggestBoundaries(body, ...select(body, 's-99'), false)).toBeNull()
    expect(suggestBoundaries('a:b;', 2, 3, false)).toEqual({ left: ':', right: ';' })
  })

  it('has none for a value at the very start, an empty selection or one past the end', () => {
    expect(suggestBoundaries('abc', 0, 2)).toBeNull()
    expect(suggestBoundaries('abc', 1, 1)).toBeNull()
    expect(suggestBoundaries('abc', 1, 9)).toBeNull()
  })

  it('has none when no boundary up to 64 characters finds this occurrence', () => {
    const run = 'x'.repeat(70)
    const body = `${run}V${run}V${run}`
    const start = body.lastIndexOf('V')
    expect(suggestBoundaries(body, start, start + 1)).toBeNull()
  })
})

describe('a name for the value', () => {
  it('comes from what labels it, and is not one the script uses', () => {
    const json = '{"access-token":"abc"}'
    expect(suggestValueName(json, json.indexOf('abc'), new Set())).toBe('access_token')
    const form = '<input type="hidden" name="csrf" value="q1">'
    expect(suggestValueName(form, form.indexOf('q1'), new Set())).toBe('csrf')
    const query = 'next?sessionId=55'
    expect(suggestValueName(query, query.indexOf('55'), new Set(['sessionId']))).toBe('sessionId_2')
    const path = '{"path":"/cart/c-1"}'
    expect(suggestValueName(path, path.indexOf('c-1'), new Set())).toBe('cart')
    const inText = '{"note":"see c-1"}'
    expect(suggestValueName(inText, inText.indexOf('c-1'), new Set())).toBe('note')
    expect(suggestValueName('plain text 42', 11, new Set())).toBe('value')
  })
})

const script = (): ScriptDefinition => ({
  name: 'Shop', version: '1.0.0', protocol: 'http', config: {},
  actions: [
    { name: 'Login', type: 'POST', target: '/login', body: 'user=ada' },
    { name: 'Order', type: 'GET', target: '/orders?token=abc123&x=abc123', header_list: [{ name: 'X-Token', value: 'abc123' }, { name: 'x-token', value: 'other' }] },
    { name: 'Branch', type: 'control_if', control: { condition: '1', steps: [{ name: 'Inner', type: 'POST', target: '/inner', body: '{"t":"abc123"}' }] } },
    { name: 'Batch', type: 'http_sequence', params: { requests: [{ method: 'GET', target: '/a?t=abc123' }, { name: 'B', method: 'GET', target: '/b' }] } },
  ],
} as unknown as ScriptDefinition)

describe('later uses of a value', () => {
  it('finds the value in later URLs, bodies, headers and sequence requests', () => {
    const uses = laterUses(script(), { phase: 'action', actionIndex: 0 }, 'abc123')
    expect(uses.map(use => [use.stepName, use.fieldLabel, use.start])).toEqual([
      ['Order', 'URL', 14],
      ['Order', 'URL', 23],
      ['Order', 'Header X-Token', 0],
      ['Inner', 'Body', 6],
      ['GET', 'URL', 5],
    ])
    expect(uses[3].position).toEqual({ phase: 'action', actionIndex: 2, stepPath: ['then[0]'] })
    expect(uses[4].position).toEqual({ phase: 'action', actionIndex: 3, stepPath: ['requests[0]'] })
    expect(uses[0]).toMatchObject({ before: '/orders?token=', after: '&x=abc123' })
    // Where the field goes on past the 30 characters shown, an ellipsis says so.
    const long = { ...script(), actions: [script().actions[0], { name: 'Long', type: 'GET', target: `/${'p'.repeat(40)}?t=abc123&${'q'.repeat(40)}` }] } as ScriptDefinition
    const [use] = laterUses(long, { phase: 'action', actionIndex: 0 }, 'abc123')
    expect(use.before).toBe(`…${'p'.repeat(27)}?t=`)
    expect(use.after).toBe(`&${'q'.repeat(29)}…`)
  })

  it('leaves out the source and steps before it', () => {
    expect(laterUses(script(), { phase: 'action', actionIndex: 2, stepPath: ['then[0]'] }, 'abc123').map(use => use.stepName)).toEqual(['GET'])
    expect(laterUses(script(), { phase: 'action', actionIndex: 0 }, '')).toEqual([])
  })

  it('counts the later requests of the source’s own sequence only when it runs them in order', () => {
    const withSequence = script()
    const batch = withSequence.actions[3]
    batch.params = { requests: [{ method: 'GET', target: '/a' }, { method: 'GET', target: '/b?t=abc123' }] }
    expect(laterUses(withSequence, { phase: 'action', actionIndex: 3, stepPath: ['requests[0]'] }, 'abc123').map(use => use.position.stepPath)).toEqual([['requests[1]']])
    batch.params = { ...batch.params, execution_mode: 'concurrent' }
    expect(laterUses(withSequence, { phase: 'action', actionIndex: 3, stepPath: ['requests[0]'] }, 'abc123')).toEqual([])
  })

  it('groups the chosen uses by step for the repair', () => {
    const uses = laterUses(script(), { phase: 'action', actionIndex: 0 }, 'abc123')
    expect(repairLocations(uses).map(entry => [entry.position.actionIndex, entry.locations.map(location => location.field)])).toEqual([
      [1, ['request_url', 'request_url', 'request_headers.x-token']],
      [2, ['request_body']],
      [3, ['request_url']],
    ])
  })
})

describe('using the value in later steps', () => {
  it('saves it on the source and sends it in the chosen uses, leaving the rest', () => {
    const before = script()
    const uses = laterUses(before, { phase: 'action', actionIndex: 0 }, 'abc123')
    const chosen = uses.filter(use => use.fieldLabel !== 'Body')
    const extractor = { name: 'token', source: 'body', type: 'boundary', expression: boundaryExpression('"token":"', '"') }
    const result = applyValueCorrelation(before, { phase: 'action', actionIndex: 0 }, extractor, chosen)
    expect(result).toMatchObject({ replaced: 4, skipped: 0 })
    const after = result.script.actions
    expect(after[0].extractors).toEqual([extractor])
    expect(after[1].target).toBe('/orders?token={{var.token}}&x={{var.token}}')
    expect(after[1].header_list).toEqual([{ name: 'X-Token', value: '{{var.token}}' }, { name: 'x-token', value: 'other' }])
    expect(after[2].control!.steps![0].body).toBe('{"t":"abc123"}')
    expect((after[3].params!.requests as Array<{ target: string }>)[0].target).toBe('/a?t={{var.token}}')
    // The script it started from is unchanged, so undo restores it.
    expect(before.actions[1].target).toBe('/orders?token=abc123&x=abc123')
    expect(before.actions[0].extractors).toBeUndefined()
  })

  it('counts a use that is no longer where it was as skipped', () => {
    const before = script()
    const uses = laterUses(before, { phase: 'action', actionIndex: 0 }, 'abc123').filter(use => use.fieldLabel === 'Body')
    const moved = { ...before, actions: before.actions.map((action, index) => index === 2 ? { ...action, control: { ...action.control, steps: [{ name: 'Inner', type: 'POST', target: '/inner', body: 'changed' }] } } : action) } as ScriptDefinition
    const result = applyValueCorrelation(moved, { phase: 'action', actionIndex: 0 }, { name: 'token', source: 'body', type: 'boundary', expression: 'a|||b' }, uses)
    expect(result).toMatchObject({ replaced: 0, skipped: 1 })
  })

  it('changes one step wherever it sits, and nothing for a step that is not there', () => {
    const before = script()
    const next = updateStep(before, { phase: 'action', actionIndex: 2, stepPath: ['then[0]'] }, action => ({ ...action, name: 'Renamed' }))
    expect(next.actions[2].control!.steps![0].name).toBe('Renamed')
    expect(updateStep(before, { phase: 'teardown', actionIndex: 0 }, action => ({ ...action, name: 'x' }))).toBe(before)
  })
})

describe('where the replayed steps sit', () => {
  it('stays the same while checks, saved values and request values change', () => {
    const before = script()
    const checked = updateStep(before, { phase: 'action', actionIndex: 0 }, action => ({ ...action, assertions: [{ type: 'status_code', expected: 200 }], extractors: [{ name: 't', source: 'body', type: 'boundary', expression: 'a|||b' }] }))
    const correlated = updateStep(checked, { phase: 'action', actionIndex: 3, stepPath: ['requests[0]'] }, action => ({ ...action, target: '/a?t={{var.t}}' }))
    const inURL = updateStep(correlated, { phase: 'action', actionIndex: 1 }, action => ({ ...action, target: '/orders?token={{var.t}}', header_list: [{ name: 'X-Token', value: '{{var.t}}' }] }))
    expect(stepLayoutKey(inURL)).toBe(stepLayoutKey(before))
  })

  it('changes when a step moves, is renamed, or a sequence gains a request', () => {
    const before = script()
    const key = stepLayoutKey(before)
    expect(stepLayoutKey({ ...before, actions: [{ name: 'First', type: 'GET', target: '/' }, ...before.actions] })).not.toBe(key)
    expect(stepLayoutKey(updateStep(before, { phase: 'action', actionIndex: 1 }, action => ({ ...action, name: 'Orders' })))).not.toBe(key)
    expect(stepLayoutKey(updateStep(before, { phase: 'action', actionIndex: 2, stepPath: ['then[0]'] }, action => ({ ...action, name: 'Renamed' })))).not.toBe(key)
    const grown = updateStep(before, { phase: 'action', actionIndex: 3 }, action => ({ ...action, params: { requests: [...(action.params!.requests as unknown[]), { method: 'GET', target: '/c' }] } }))
    expect(stepLayoutKey(grown)).not.toBe(key)
  })
})
