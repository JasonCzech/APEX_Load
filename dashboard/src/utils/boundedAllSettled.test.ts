import { describe, expect, it } from 'vitest'

import { boundedAllSettled } from './boundedAllSettled'

describe('boundedAllSettled', () => {
  it('limits active tasks and retains input-ordered outcomes', async () => {
    let active = 0
    let maxActive = 0
    let releaseFirstWave = () => {}
    const firstWave = new Promise<void>(resolve => { releaseFirstWave = resolve })

    const pending = boundedAllSettled(
      [0, 1, 2, 3, 4],
      async value => {
        active += 1
        maxActive = Math.max(maxActive, active)
        await firstWave
        active -= 1
        if (value === 2) throw new Error('locked')
        return `deleted-${value}`
      },
      2,
    )

    while (active < 2) await Promise.resolve()
    releaseFirstWave()
    const outcomes = await pending

    expect(maxActive).toBe(2)
    expect(outcomes.map(outcome => outcome.status)).toEqual([
      'fulfilled',
      'fulfilled',
      'rejected',
      'fulfilled',
      'fulfilled',
    ])
    expect(outcomes[0]).toEqual({ status: 'fulfilled', value: 'deleted-0' })
    expect((outcomes[2] as PromiseRejectedResult).reason).toEqual(new Error('locked'))
  })

  it('handles empty input and clamps invalid concurrency to one worker', async () => {
    expect(await boundedAllSettled([], async value => value, 0)).toEqual([])

    let active = 0
    let maxActive = 0
    const outcomes = await boundedAllSettled([1, 2], async value => {
      active += 1
      maxActive = Math.max(maxActive, active)
      await Promise.resolve()
      active -= 1
      return value
    }, Number.NaN)

    expect(maxActive).toBe(1)
    expect(outcomes).toEqual([
      { status: 'fulfilled', value: 1 },
      { status: 'fulfilled', value: 2 },
    ])
  })
})
