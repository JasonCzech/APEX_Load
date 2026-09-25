import { describe, expect, it } from 'vitest'
import { comparisonMagnitude, comparisonRequestSignature, defaultPolicy, difference, measurementValue, number, type ComparisonRequest, type ComparisonRow, type Measurement } from './model'

describe('baseline comparison presentation', () => {
  it('does not invent a percentage for zero or missing baselines', () => {
    expect(difference(10, 0)).toBe('+10 · baseline zero')
    expect(difference(10, undefined)).toBe('Unavailable')
    expect(difference(50, 100)).toBe('-50 (-50%)')
    expect(number(Number.NaN)).toBe('Unavailable')
  })
  it('does not present missing successful latency as zero', () => {
    const failed = { successes: 0, samples: 100, p95: 0, mean: 0 } as Measurement
    expect(measurementValue(failed, 'p95')).toBeUndefined()
    expect(measurementValue(failed, 'samples')).toBe(100)
    expect(measurementValue(failed, 'mean', true)).toBe(0)
  })
  it('uses the published advisory thresholds' , () => {
    expect(defaultPolicy).toEqual({ revision: 0, load_tolerance_pct: 5, latency_pct: 10, latency_ms: 10, throughput_pct: 10, error_points: 1, resource_pct: 20 })
  })
})

it('recognizes equivalent settings after a server reorders JSON object keys', () => {
 const a: ComparisonRequest = { baseline_ids:['a','b'], policy: {...defaultPolicy, group_mappings:{z:'Z',a:'A'}}, windows:{b:{start_seconds:0,end_seconds:90},a:{start_seconds:30,end_seconds:120}}, population:'transaction',allow_other_scenarios:false }
 const b: ComparisonRequest = { ...a, minimum_load:0, maximum_load:0, policy: {...defaultPolicy, group_mappings:{a:'A',z:'Z'}}, windows:{a:{end_seconds:120,start_seconds:30},b:{end_seconds:90,start_seconds:0}} }
 expect(comparisonRequestSignature(a)).toBe(comparisonRequestSignature(b))
 expect(comparisonRequestSignature({...b,maximum_load:100})).not.toBe(comparisonRequestSignature(a))
})

it('ignores omitted empty policy mappings and default mapping options', () => {
 const request: ComparisonRequest={baseline_ids:['b'],policy:defaultPolicy,population:'transaction',allow_other_scenarios:false}
 expect(comparisonRequestSignature(request)).toBe(comparisonRequestSignature({...request,policy:{...defaultPolicy,group_mappings:{},resource_mappings:[]}}))
 const mapping={run_id:'*',identity:'heap',keys:['heap'],unit:'bytes',kind:'gauge' as const,aggregation:'mean' as const,confirmed:true}
 const withMapping={...request,policy:{...defaultPolicy,resource_mappings:[mapping]}}
 expect(comparisonRequestSignature(withMapping)).toBe(comparisonRequestSignature({...request,policy:{...defaultPolicy,resource_mappings:[{...mapping,group:'',absolute_threshold:0}]}}))
})

it('does not rank unavailable latency or incompatible units as changes', () => {
 const row = { current: { successes: 0, p95: 0, mean: 200 }, baselines: { b: { measurement: { successes: 100, p95: 100, mean: 100 } } } } as unknown as ComparisonRow
 expect(comparisonMagnitude(row, 'p95')).toBe(0)
 expect(comparisonMagnitude(row, 'mean', true)).toBe(0)
 row.current!.successes = 100
 expect(comparisonMagnitude(row, 'p95')).toBe(1)
 row.baselines.b.delta = 100
 expect(comparisonMagnitude(row, 'mean', true)).toBe(1)
})
