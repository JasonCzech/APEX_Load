import { describe, it, expect } from 'vitest'
import { messagingScheduleSummary } from './jmeterMessaging'

describe('messagingScheduleSummary', () => {
  it('integrates the source ramps and plateaus without rounding to the filename volume', () => {
    const totals = [84, 42, 42].map(rate => messagingScheduleSummary({ name: 'producer', script_name: 'producer', vusers: 50, initialization: 'at_runtime', sampler_schedules: [{ id: 'timer', segments: [{ start_rps: 1, end_rps: rate, duration_seconds: 300 }, { start_rps: rate, end_rps: rate, duration_seconds: 3600 }] }] })[0])
    expect(totals.reduce((sum, item) => sum + item.planned, 0)).toBe(630450)
    expect(totals.every(item => item.seconds === 3900)).toBe(true)
    expect(totals[0].points).toContain('490,10')
  })
})
