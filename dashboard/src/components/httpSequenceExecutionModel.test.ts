import { describe, expect, it } from 'vitest'
import type { ScriptActionDefinition } from '../types/scripts'
import { httpSequenceExecutionSettings, setHTTPSequenceExecutionMode, setHTTPSequenceMaxConcurrency } from './httpSequenceExecutionModel'

const requests = [{ method: 'GET', target: '/one', keep: { body: 'exact' } }, { method: 'GET', target: '/two' }]
const sequence: ScriptActionDefinition = { name: 'Group', type: 'http_sequence', target: '', params: { requests, unknown: { keep: true } }, metadata: { source_transaction: 'Group', source_path: 'Action.c:1' } }
const settings = (params: Record<string, unknown>) => httpSequenceExecutionSettings({ ...sequence, params }, 'http')!

describe('HTTP sequence execution settings', () => {
  it('keeps absence sequential and derives bounded automatic concurrency only for explicit concurrent groups', () => {
    expect(settings({ requests })).toMatchObject({ mode: 'sequential', defaultMode: true, valid: true, effectiveConcurrency: undefined })
    expect(settings({ requests, execution_mode: 'sequential' })).toMatchObject({ mode: 'sequential', defaultMode: false, valid: true })
    expect(settings({ requests: new Array(10).fill({}), execution_mode: 'concurrent' })).toMatchObject({ effectiveConcurrency: 8, valid: true })
    expect(settings({ requests, execution_mode: 'concurrent', max_concurrency: 16 })).toMatchObject({ effectiveConcurrency: 2, valid: true })
  })

  it('applies the child count cap to concurrent mode while preserving serial behavior', () => {
    expect(settings({ requests: new Array(256).fill({}), execution_mode: 'concurrent', max_concurrency: 16 }).valid).toBe(true)
    expect(settings({ requests: new Array(257).fill({}), execution_mode: 'concurrent' }).valid).toBe(false)
    expect(settings({ requests: new Array(257).fill({}), max_concurrency: 16 }).valid).toBe(true)
    for (const requests of [[], {}, [null], [[]]]) expect(settings({ requests, execution_mode: 'concurrent' }).valid).toBe(false)
  })

  it.each(['', ' ', 'private-mode', null, [], { toString: null }])('retains malformed authored mode %j as explicit invalid evidence', execution_mode => {
    const params = { requests, execution_mode }
    const before = JSON.stringify(params)
    expect(settings(params)).toMatchObject({ mode: null, defaultMode: false, valid: false, effectiveConcurrency: undefined })
    expect(JSON.stringify(params)).toBe(before)
  })

  it.each(['', '2', 'private-limit', null, {}, [], 0, -1, 1.5, 17, Infinity, NaN])('requires integer limits 1–16 even when sequential: %j', max_concurrency => {
    expect(settings({ requests, max_concurrency })).toMatchObject({ mode: 'sequential', limitValid: false, valid: false })
    expect(settings({ requests, execution_mode: 'concurrent', max_concurrency }).effectiveConcurrency).toBeUndefined()
  })

  it.each(['browser', 'soap', 'http/1.1', 'http/2', 'grpc', 'java_http', 'kafka'])('does not author HTTP sequence policy for %s', protocol => {
    expect(httpSequenceExecutionSettings(sequence, protocol)).toBeNull()
    expect(setHTTPSequenceExecutionMode(sequence, protocol, 'concurrent')).toBe(sequence)
    expect(setHTTPSequenceMaxConcurrency(sequence, protocol, 2)).toBe(sequence)
  })

  it('edits only the selected policy key without changing raw requests, unknown params or metadata', () => {
    const concurrent = setHTTPSequenceExecutionMode(sequence, 'http', 'concurrent')
    const limited = setHTTPSequenceMaxConcurrency(concurrent, 'http', 2)
    expect(limited.params).toEqual({ ...sequence.params, execution_mode: 'concurrent', max_concurrency: 2 })
    expect(limited.params?.requests).toBe(requests)
    expect(limited.params?.unknown).toBe(sequence.params?.unknown)
    expect(limited.metadata).toBe(sequence.metadata)
    expect(sequence.params).not.toHaveProperty('execution_mode')
    const reverted = setHTTPSequenceExecutionMode(limited, 'http', undefined)
    expect(reverted.params).toEqual({ ...sequence.params, max_concurrency: 2 })
    expect(setHTTPSequenceMaxConcurrency(reverted, 'http', undefined).params).toEqual(sequence.params)
    expect(setHTTPSequenceMaxConcurrency(sequence, 'http', 99).params?.max_concurrency).toBe(99)
  })

  it.each([null, [], 'private-params'])('does not rewrite malformed parameter envelopes %j', params => {
    const raw = { ...sequence, params } as unknown as ScriptActionDefinition
    expect(httpSequenceExecutionSettings(raw, 'http')?.paramsInvalid).toBe(true)
    expect(setHTTPSequenceExecutionMode(raw, 'http', 'concurrent')).toBe(raw)
    expect(setHTTPSequenceMaxConcurrency(raw, 'http', 2)).toBe(raw)
  })
})
