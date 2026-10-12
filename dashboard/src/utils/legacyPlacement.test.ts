import { describe, expect, it } from 'vitest'

import { isLegacyApplicationId, isLegacyReleaseId } from './legacyPlacement'

describe('legacyPlacement', () => {
  it('recognises the controller legacy buckets and nothing else', () => {
    expect(isLegacyApplicationId('legacy')).toBe(true)
    expect(isLegacyApplicationId('legacy-3c4f08157674')).toBe(true)
    expect(isLegacyApplicationId('legacy-app')).toBe(false)
    expect(isLegacyApplicationId('6db51fa9-fb6b-4927-84b8-8f0826bcddfe')).toBe(false)
    expect(isLegacyApplicationId(undefined)).toBe(false)
    expect(isLegacyReleaseId('legacy-imported')).toBe(true)
    expect(isLegacyReleaseId('legacy-imported-3c4f08157674')).toBe(true)
    expect(isLegacyReleaseId('legacy')).toBe(false)
  })
})
