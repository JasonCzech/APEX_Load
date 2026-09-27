import { describe, expect, it } from 'vitest'
import type { LiveMetrics } from '../types'
import { liveObservationState } from './liveObservation'

const now = Date.parse('2026-09-27T12:00:00Z')
const sample = (delta = 0, fields: Partial<LiveMetrics> = {}) => ({ timestamp: new Date(now + delta).toISOString(), active_vusers: 120, ...fields }) as LiveMetrics

describe('live observation freshness', () => {
  it.each([
    ['fresh', sample(), 'current'],
    ['expires at boundary', sample(-5000), 'stale'],
    ['future timestamp', sample(1), 'stale'],
    ['invalid timestamp', sample(0, { timestamp: 'invalid' }), 'stale'],
    ['missing sample', undefined, 'missing'],
    ['server stale', sample(0, { observation_state: 'stale' }), 'stale'],
    ['old contributor', sample(0, { observation_valid_until: new Date(now - 1).toISOString() }), 'stale'],
    ['invalid deadline', sample(0, { observation_valid_until: 'invalid' }), 'stale'],
    ['deadline cannot renew sample', sample(-5000, { observation_valid_until: new Date(now + 60000).toISOString() }), 'stale'],
  ])('%s', (_name, metrics, expected) => {
    expect(liveObservationState('RUNNING', metrics as LiveMetrics | undefined, now)).toBe(expected)
  })

  it('gives terminal state precedence and preserves the sample', () => {
    const metrics = sample(-60000, { observation_state: 'stale' })
    const before = JSON.stringify(metrics)
    expect(liveObservationState('FAILED', metrics, now)).toBe('historical')
    expect(JSON.stringify(metrics)).toBe(before)
  })
})
