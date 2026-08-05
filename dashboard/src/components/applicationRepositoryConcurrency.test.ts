import { describe, expect, it } from 'vitest'

import {
  createRepositoryCatalogTaskLimiter,
} from './applicationRepositoryConcurrency'

describe('application repository catalog concurrency', () => {
  it('shares one request limit across concurrently loaded asset kinds', async () => {
    const limiter = createRepositoryCatalogTaskLimiter(4)
    let active = 0
    let maxActive = 0
    const loader = async (id: string) => limiter(async () => {
      active += 1
      maxActive = Math.max(maxActive, active)
      await Promise.resolve()
      active -= 1
      return id
    })

    const [scripts, scenarios] = await Promise.all([
      Promise.all(Array.from({ length: 12 }, (_, index) => loader(`script-${index}`))),
      Promise.all(Array.from({ length: 12 }, (_, index) => loader(`scenario-${index}`))),
    ])

    expect(maxActive).toBeLessThanOrEqual(4)
    expect(scripts).toHaveLength(12)
    expect(scenarios).toHaveLength(12)
  })
})
