import { describe, expect, it } from 'vitest'

import {
  MAX_THINK_TIME_MULTIPLIER,
  MIN_THINK_TIME_MULTIPLIER,
  canonicalThinkTimeType,
  normalizeThinkTimeMultiplier,
  thinkTimeConfigForType,
  thinkTimeUsesBounds,
  thinkTimeUsesMean,
  thinkTimeUsesStdDeviation,
} from './thinkTimeConfig'

describe('thinkTimeConfigForType', () => {
  it('removes distribution fields for recorded replay and preserves policy controls', () => {
    expect(thinkTimeConfigForType({
      type: 'gaussian',
      mean_ms: 1000,
      std_ms: 200,
      min_ms: 100,
      max_ms: 2500,
      multiplier: 1.5,
      limit_ms: 5000,
      ignore: false,
    }, 'recorded')).toEqual({
      type: 'recorded',
      multiplier: 1.5,
      limit_ms: 5000,
      ignore: false,
    })
  })

  it('represents disabled think time without hidden distribution fields', () => {
    expect(thinkTimeConfigForType({
      type: 'gaussian',
      mean_ms: 1000,
      std_ms: 200,
      min_ms: 100,
      max_ms: 2500,
      multiplier: 1.5,
      limit_ms: 5000,
    }, 'none')).toEqual({
      type: 'none',
      multiplier: 1.5,
      limit_ms: 5000,
    })
    expect(thinkTimeUsesMean('none')).toBe(false)
    expect(thinkTimeUsesStdDeviation('none')).toBe(false)
    expect(thinkTimeUsesBounds('none')).toBe(false)
  })

  it('adds the required editable mean when leaving recorded replay', () => {
    expect(thinkTimeConfigForType({ type: 'recorded', multiplier: 2 }, 'fixed')).toEqual({
      type: 'fixed',
      mean_ms: 0,
      multiplier: 2,
    })
  })

  it('initializes uniform bounds from the prior fixed value without retaining an unused mean', () => {
    expect(thinkTimeConfigForType({
      type: 'fixed',
      mean_ms: 1250,
      multiplier: 1.5,
    }, 'uniform')).toEqual({
      type: 'uniform',
      min_ms: 1250,
      max_ms: 1250,
      multiplier: 1.5,
    })
  })

  it('round-trips uniform bounds and derives a gaussian mean when changing modes', () => {
    const uniform = thinkTimeConfigForType({
      type: 'uniform',
      min_ms: 250,
      max_ms: 1750,
      limit_ms: 2000,
    }, 'uniform')
    expect(uniform).toEqual({
      type: 'uniform',
      min_ms: 250,
      max_ms: 1750,
      limit_ms: 2000,
    })
    expect(thinkTimeConfigForType(uniform, 'gaussian')).toEqual({
      type: 'gaussian',
      mean_ms: 1000,
      min_ms: 250,
      max_ms: 1750,
      limit_ms: 2000,
    })
  })

  it('identifies the fields consumed by each engine distribution', () => {
    expect(thinkTimeUsesMean('fixed')).toBe(true)
    expect(thinkTimeUsesMean('gaussian')).toBe(true)
    expect(thinkTimeUsesMean('uniform')).toBe(false)
    expect(thinkTimeUsesStdDeviation('gaussian')).toBe(true)
    expect(thinkTimeUsesStdDeviation('uniform')).toBe(false)
    expect(thinkTimeUsesBounds('gaussian')).toBe(true)
    expect(thinkTimeUsesBounds('uniform')).toBe(true)
    expect(thinkTimeUsesBounds('fixed')).toBe(false)
  })

  it('canonicalizes the legacy normal alias without dropping distribution fields', () => {
    expect(canonicalThinkTimeType(' NORMAL ')).toBe('gaussian')
    expect(thinkTimeConfigForType({
      type: 'normal',
      mean_ms: 500,
      std_ms: 75,
      min_ms: 100,
      max_ms: 900,
    }, 'normal')).toEqual({
      type: 'gaussian',
      mean_ms: 500,
      std_ms: 75,
      min_ms: 100,
      max_ms: 900,
    })
  })

  it('round-trips every positive finite multiplier while treating legacy zero as inherited', () => {
    expect(MIN_THINK_TIME_MULTIPLIER).toBe(0)
    expect(MAX_THINK_TIME_MULTIPLIER).toBe(100)
    expect(normalizeThinkTimeMultiplier(0)).toBeUndefined()
    expect(normalizeThinkTimeMultiplier('0')).toBeUndefined()
    expect(normalizeThinkTimeMultiplier(-0.01)).toBeUndefined()
    expect(normalizeThinkTimeMultiplier(0.05)).toBe(0.05)
    expect(normalizeThinkTimeMultiplier(Number.MIN_VALUE)).toBe(Number.MIN_VALUE)
    expect(normalizeThinkTimeMultiplier(0.1)).toBe(0.1)
    expect(normalizeThinkTimeMultiplier(100)).toBe(100)
    expect(normalizeThinkTimeMultiplier(100.1)).toBeUndefined()
    expect(normalizeThinkTimeMultiplier('2.5')).toBe(2.5)
    expect(thinkTimeConfigForType({ type: 'recorded', multiplier: 0 }, 'recorded')).toEqual({
      type: 'recorded',
    })
  })
})

it('edits percentage bounds independently of millisecond distributions', () => {
 const percentage = thinkTimeConfigForType({ type: 'fixed', mean_ms: 4000, std_ms: 20, min_ms: 10, max_ms: 30 }, 'random_percentage')
 expect(percentage).toEqual({ type: 'random_percentage', min_percent: 50, max_percent: 150 })
 expect(thinkTimeUsesMean('random_percentage')).toBe(false)
 expect(thinkTimeUsesBounds('random_percentage')).toBe(false)
 expect(thinkTimeConfigForType({ ...percentage, min_percent: 0, max_percent: 0 }, 'random_percentage')).toEqual({ type: 'random_percentage', min_percent: 0, max_percent: 0 })
 expect(thinkTimeConfigForType(percentage, 'recorded')).toEqual({ type: 'recorded' })
})

it('preserves zero percentage bounds omitted by Go JSON serialization', () => {
 expect(thinkTimeConfigForType({ type: 'random_percentage' }, 'random_percentage')).toEqual({ type: 'random_percentage', min_percent: 0, max_percent: 0 })
})
