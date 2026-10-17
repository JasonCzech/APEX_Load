import { describe, expect, it } from 'vitest'

import { isDegradedLivePercentileQuality, percentileQualityDescription } from './percentileQuality'

describe('percentile quality', () => {
  it('explains each approximate quality and nothing else', () => {
    expect(percentileQualityDescription('approximate')).toContain('within 0.5%')
    expect(percentileQualityDescription('approximate_tdigest')).toContain('t-digests')
    expect(percentileQualityDescription('averaged')).toContain('average')
    expect(percentileQualityDescription(undefined)).toBeUndefined()
    expect(percentileQualityDescription('')).toBeUndefined()
    expect(percentileQualityDescription('merged_tdigest')).toBeUndefined()
  })

  it('treats only fallbacks from the merged sketch as degraded live windows', () => {
    expect(isDegradedLivePercentileQuality('approximate_tdigest')).toBe(true)
    expect(isDegradedLivePercentileQuality('averaged')).toBe(true)
    expect(isDegradedLivePercentileQuality('approximate')).toBe(false)
    expect(isDegradedLivePercentileQuality(undefined)).toBe(false)
  })
})
