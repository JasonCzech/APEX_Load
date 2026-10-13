import { describe, expect, it } from 'vitest'

import type { SystemInventoryGenerator } from '../types'
import { formatEstimate, generatorDisplayName } from './generatorNames'

const inventory = [
  { target: '127.0.0.1:19091', generator_id: 'gen-a', region: 'us-east' },
  { target: '127.0.0.1:19092', generator_id: 'gen-b' },
] as SystemInventoryGenerator[]

describe('generatorDisplayName', () => {
  it('names a generator by ID and region, and falls back to the address', () => {
    expect(generatorDisplayName('127.0.0.1:19091', inventory)).toBe('gen-a · us-east')
    expect(generatorDisplayName('127.0.0.1:19092', inventory)).toBe('gen-b')
    expect(generatorDisplayName('10.0.0.5:9091', inventory)).toBe('10.0.0.5:9091')
    expect(generatorDisplayName('10.0.0.5:9091', undefined)).toBe('10.0.0.5:9091')
  })
})

describe('formatEstimate', () => {
  it('keeps two significant figures so a small load never reads as zero', () => {
    expect(formatEstimate(0.0123)).toBe('0.012')
    expect(formatEstimate(1.456)).toBe('1.5')
    expect(formatEstimate(123)).toBe('120')
    expect(formatEstimate(0)).toBe('0')
    expect(formatEstimate(Number.NaN)).toBe('—')
  })
})
